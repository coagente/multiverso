package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/coagente/multiverso/internal/object"
)

// claudeAdapter drives Claude Code headless: `claude -p` with stream-json
// output, parsed line-by-line by claudeParser.
type claudeAdapter struct{}

// ID implements Adapter.
func (claudeAdapter) ID() string { return "claude-code" }

// Version implements Adapter.
func (claudeAdapter) Version() string { return "v0" }

func (claudeAdapter) binary() string { return "claude" }

// Start implements Adapter.
func (a claudeAdapter) Start(ctx context.Context, spec RunSpec) (Run, error) {
	return startProc(ctx, spec, claudeArgv(spec), &claudeParser{})
}

// claudeArgv builds the fixed-order claude-code argv. --bare keeps the run
// hermetic (no hooks/skills/CLAUDE.md auto-discovery); bypassPermissions
// because in -p mode an unanswerable permission prompt is a crippled run
// and worlds are control-plane-owned isolated worktrees gated by oracles
// (decision 12). Budget flags are appended only when the corresponding
// limit > 0 (decision 16). No MCP flags in M1b (AG-7 is out).
func claudeArgv(spec RunSpec) []string {
	argv := []string{
		"claude", "-p", spec.Prompt,
		"--output-format", "stream-json",
		"--bare",
		"--permission-mode", "bypassPermissions",
	}
	if spec.Model != "" {
		argv = append(argv, "--model", spec.Model)
	}
	if spec.Budget.MaxTurns > 0 {
		argv = append(argv, "--max-turns", strconv.Itoa(spec.Budget.MaxTurns))
	}
	if spec.Budget.MaxUSDMicro > 0 {
		argv = append(argv, "--max-budget-usd", FormatUSDMicro(spec.Budget.MaxUSDMicro))
	}
	return argv
}

// claudeProviderRe spots provider trouble in an error result's text
// (mapping row 4 fallback).
var claudeProviderRe = regexp.MustCompile(`(?i)rate.?limit|overloaded|billing`)

// claudeRetryCategories are the system/api_retry error categories that
// mark a run as provider-degraded (mapping row 4).
var claudeRetryCategories = map[string]bool{
	"rate_limit": true, "overloaded": true, "billing_error": true,
}

// claudeResult is the harvested terminal result event.
type claudeResult struct {
	subtype string
	isError bool
	raw     []byte // verbatim line, for the row-4 error-text fallback
}

// claudeParser parses Claude Code stream-json (NDJSON). Every line lands
// in the transcript first (the runner tees); unknown types and unparseable
// lines are tolerated, never fatal.
type claudeParser struct {
	initSeen  bool
	retryHit  bool
	result    *claudeResult
	cost      object.RunCost
	numTurns  int
	sessionID string
}

func (p *claudeParser) line(raw []byte, oversized bool) string {
	if oversized {
		return EventUnknown
	}
	var ev struct {
		Type      string          `json:"type"`
		Subtype   string          `json:"subtype"`
		IsError   bool            `json:"is_error"`
		Error     json.RawMessage `json:"error"`
		TotalCost json.RawMessage `json:"total_cost_usd"`
		Usage     json.RawMessage `json:"usage"`
		NumTurns  int             `json:"num_turns"`
		SessionID string          `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return EventUnknown
	}
	switch {
	case ev.Type == "system" && ev.Subtype == "init":
		// Launched clean (mapping row 1 threshold). Reported model and
		// version stay evidential in the transcript (decision 15).
		p.initSeen = true
		if ev.SessionID != "" {
			p.sessionID = ev.SessionID
		}
		return EventInit
	case ev.Type == "system" && ev.Subtype == "api_retry":
		if claudeRetryCategories[retryCategory(ev.Error)] {
			p.retryHit = true
		}
		return EventRetry
	case ev.Type == "result":
		p.result = &claudeResult{subtype: ev.Subtype, isError: ev.IsError, raw: bytes.Clone(raw)}
		if ev.NumTurns > 0 {
			p.numTurns = ev.NumTurns
		}
		if ev.SessionID != "" {
			p.sessionID = ev.SessionID
		}
		// Cost source is honest: "client-estimate" whenever the result
		// carried cost or usage, however malformed the values (AG-2).
		if len(ev.TotalCost) > 0 || len(ev.Usage) > 0 {
			p.cost.Source = CostSourceClientEstimate
		}
		if len(ev.TotalCost) > 0 {
			p.cost.USDMicro = parseReportedUSD(numberText(ev.TotalCost))
		}
		if len(ev.Usage) > 0 {
			var u struct {
				InputTokens              int64 `json:"input_tokens"`
				CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
				OutputTokens             int64 `json:"output_tokens"`
			}
			if err := json.Unmarshal(ev.Usage, &u); err == nil {
				p.cost.TokensIn = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
				p.cost.TokensOut = u.OutputTokens
			}
		}
		return EventResult
	case ev.Type == "assistant" || ev.Type == "user":
		return EventItem
	default:
		return EventUnknown
	}
}

// outcome implements the claude-code mapping table (first matching row
// wins). Rule 0 — control-plane kills — already ran in the runner.
func (p *claudeParser) outcome(exitCode int) string {
	switch {
	case exitCode != 0 && !p.initSeen:
		// Row 1: in-run exec failure (binary vanished, bad flags) — the
		// process died before announcing itself.
		return object.OutcomeConfigError
	case p.result != nil && p.result.subtype == "success" && !p.result.isError && exitCode == 0:
		return object.OutcomeCompleted // row 2
	case p.result != nil && strings.HasPrefix(p.result.subtype, "error_max_"):
		// Row 3: a native cap fired (--max-turns, --max-budget-usd); the
		// flag-derived subtype family is prefix-matched to survive drift.
		return object.OutcomeBudgetExceeded
	case p.result != nil && p.result.isError && (p.retryHit || claudeProviderRe.Match(p.result.raw)):
		return object.OutcomeProviderError // row 4
	case p.result != nil && p.result.isError:
		return object.OutcomeCrash // row 5
	default:
		// Rows 6-7: non-zero exit after init with no result, exit 0
		// without a result, or a success result contradicting a non-zero
		// exit (stream contract violated; honesty over optimism).
		return object.OutcomeCrash
	}
}

func (p *claudeParser) harvest() (object.RunCost, int, string) {
	cost := p.cost
	if cost.Source == "" {
		cost.Source = CostSourceNone
	}
	return cost, p.numTurns, p.sessionID
}

// retryCategory extracts the error category from a system/api_retry event:
// a bare string, or an object's "category"/"type" field.
func retryCategory(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Category string `json:"category"`
		Type     string `json:"type"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if obj.Category != "" {
			return obj.Category
		}
		return obj.Type
	}
	return ""
}

// numberText unwraps a raw JSON scalar to the text a decimal parser wants:
// quotes stripped when a tool emitted the number as a string.
func numberText(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	var q string
	if err := json.Unmarshal(raw, &q); err == nil {
		return q
	}
	return s
}
