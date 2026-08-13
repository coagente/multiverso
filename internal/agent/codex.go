package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"

	"github.com/coagente/multiverso/internal/object"
)

// codexAdapter drives Codex non-interactively: `codex exec --json`, JSONL
// parsed line-by-line by codexParser. cwd is pinned to the world dir
// (worlds are git worktrees, so codex's git-repo check passes; -C would
// only duplicate cwd with a nondeterministic temp path in argv).
// --ephemeral is deliberately not passed: session files enable
// `codex exec resume` for REPAIR (v1). CODEX_API_KEY reaches the process
// only via the env allowlist. Codex has no native caps, so codex runs are
// watchdog-only (decision 16).
type codexAdapter struct{}

// ID implements Adapter.
func (codexAdapter) ID() string { return "codex" }

// Version implements Adapter.
func (codexAdapter) Version() string { return "v0" }

func (codexAdapter) binary() string { return "codex" }

// Start implements Adapter.
func (a codexAdapter) Start(ctx context.Context, spec RunSpec) (Run, error) {
	return startProc(ctx, spec, codexArgv(spec), &codexParser{})
}

// codexArgv builds the codex argv: --sandbox workspace-write is the
// OS-level equivalent of what the task defines (decision 12).
func codexArgv(spec RunSpec) []string {
	argv := []string{"codex", "exec", "--json", "--sandbox", "workspace-write"}
	if spec.Model != "" {
		argv = append(argv, "-m", spec.Model)
	}
	return append(argv, spec.Prompt)
}

// codexProviderRe spots provider trouble in a turn.failed error (mapping
// row 3).
var codexProviderRe = regexp.MustCompile(`(?i)rate.?limit|overloaded|quota|insufficient_quota|429`)

// codexParser parses codex exec JSONL. Codex reports tokens, not dollars,
// so usd_micro stays 0 with non-zero tokens — that combination IS the
// honest record (decision 3).
type codexParser struct {
	threadStarted bool
	turnsDone     int
	failed        bool
	failedRaw     []byte // verbatim turn.failed line, for row-3 matching
	cost          object.RunCost
	sessionID     string
}

func (p *codexParser) line(raw []byte, oversized bool) string {
	if oversized {
		return EventUnknown
	}
	var ev struct {
		Type     string          `json:"type"`
		ThreadID string          `json:"thread_id"`
		Usage    json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return EventUnknown
	}
	switch ev.Type {
	case "thread.started":
		p.threadStarted = true
		if ev.ThreadID != "" {
			p.sessionID = ev.ThreadID
		}
		return EventInit
	case "turn.started", "item.started", "item.completed":
		return EventItem
	case "turn.completed":
		p.turnsDone++
		if len(ev.Usage) > 0 {
			p.cost.Source = CostSourceClientEstimate
			var u struct {
				InputTokens       int64 `json:"input_tokens"`
				CachedInputTokens int64 `json:"cached_input_tokens"`
				OutputTokens      int64 `json:"output_tokens"`
			}
			if err := json.Unmarshal(ev.Usage, &u); err == nil {
				p.cost.TokensIn += u.InputTokens + u.CachedInputTokens
				p.cost.TokensOut += u.OutputTokens
			}
		}
		return EventItem
	case "turn.failed":
		p.failed = true
		p.failedRaw = bytes.Clone(raw)
		return EventResult
	default:
		return EventUnknown
	}
}

// outcome implements the codex mapping table (first matching row wins).
// Rule 0 — control-plane kills — already ran in the runner. No native-cap
// row: codex has no caps (decision 16).
func (p *codexParser) outcome(exitCode int) string {
	switch {
	case exitCode != 0 && !p.threadStarted:
		// Row 1: bad flags, missing auth, missing binary at exec time.
		return object.OutcomeConfigError
	case exitCode == 0 && p.turnsDone >= 1 && !p.failed:
		return object.OutcomeCompleted // row 2
	case p.failed && codexProviderRe.Match(p.failedRaw):
		return object.OutcomeProviderError // row 3
	case p.failed:
		return object.OutcomeCrash // row 4
	default:
		// Rows 5-6: non-zero exit after thread.started without turn.failed,
		// or exit 0 without any turn.completed.
		return object.OutcomeCrash
	}
}

func (p *codexParser) harvest() (object.RunCost, int, string) {
	cost := p.cost
	if cost.Source == "" {
		cost.Source = CostSourceNone
	}
	return cost, p.turnsDone, p.sessionID
}
