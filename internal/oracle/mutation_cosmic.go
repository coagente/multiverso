package oracle

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// cosmicRayTool is the v0 DEFAULT mutation adapter, and choosing it
// reverses ch. 19's recommendation on purpose (M2a decision 19).
//
// Ch. 19 preferred mutmut "for speed and incremental state". M2a forbids
// incremental state (decision 18) and requires control-plane mutant
// SELECTION (decision 17), which inverts the trade: cosmic-ray's
// init → session → worker model is the only one of the two that lets the
// control plane enumerate the population, apply the canonical order and the
// cap, and then execute exactly that set. Speed matters less than being
// able to say which mutants ran and why those.
//
// The argv shapes below are this adapter's declared CONTRACT. Neither tool
// is installed on the machine this was written on, so they are pinned here,
// asserted by golden argv tests, and exercised end-to-end only by the
// acceptance script's step 3v — which SKIPS WITH A NAMED REASON when the
// tool is absent rather than pretending to have run.
type cosmicRayTool struct{}

// Name implements MutationTool.
func (cosmicRayTool) Name() string { return ToolCosmicRay }

// Selection implements MutationTool: the control plane enumerates, orders
// and caps, so the receipt records the stronger provenance.
func (cosmicRayTool) Selection() string { return SelectionControlPlane }

// cosmicCLI is the module the adapter drives. It goes through the
// INTERPRETER rather than a bare `cosmic-ray` script so a repository pinned
// to a virtualenv is mutated by that virtualenv's tool, exactly as the
// tools probe is run by that virtualenv's python.
const cosmicCLI = "cosmic_ray.cli"

// Config writes cosmic-ray's session configuration. Two things in it are
// load-bearing:
//
//   - test-command names OUR suite invocation, with `-p mvo_evidence` in
//     it, so every mutant's outcome arrives on a stream the control plane
//     read LIVE instead of in a report the mutated run wrote afterwards;
//   - there is no incremental or resume setting, and the session lives in
//     scratch. A cache is an evidence store, and an evidence store in the
//     candidate's tree is a two-line forgery (corpus vector 15).
func (cosmicRayTool) Config(t TargetSet, run MutationRun) []byte {
	var b strings.Builder
	b.WriteString("# written by mvo — control-plane owned, regenerated per run.\n")
	b.WriteString("[cosmic-ray]\n")
	fmt.Fprintf(&b, "module-path = %q\n", firstPath(t))
	fmt.Fprintf(&b, "timeout = %d\n", run.TimeoutSec)
	fmt.Fprintf(&b, "test-command = %q\n", shellJoin(run.TestArgv))
	b.WriteString("excluded-modules = []\n")
	b.WriteString("\n[cosmic-ray.distributor]\nname = \"local\"\n")
	if ops := cosmicOperators(run.Spec.Operators); len(ops) > 0 {
		b.WriteString("\n[cosmic-ray.filters.operators-filter]\n")
		fmt.Fprintf(&b, "exclude-operators = []\ninclude-operators = [%s]\n", quoteJoin(ops))
	}
	return []byte(b.String())
}

// EnumerateSteps is init-then-dump, once per target file: cosmic-ray's
// config names ONE module path, so a patch touching three files is three
// sessions, and the fragments concatenate.
func (c cosmicRayTool) EnumerateSteps(python string, t TargetSet, run MutationRun) []MutationStep {
	steps := make([]MutationStep, 0, 2*len(t.Files))
	for i, f := range t.Files {
		session := path.Join(run.SessionDir, fmt.Sprintf("session-%02d.sqlite", i))
		steps = append(steps,
			MutationStep{Argv: []string{python, "-m", cosmicCLI, "init", "--force",
				"--module-path", f.Path, run.ConfigPath, session}},
			MutationStep{Argv: []string{python, "-m", cosmicCLI, "dump", session}, Parse: true},
		)
	}
	return steps
}

// cosmicWorkItem is one line of `cosmic-ray dump`: a work item, optionally
// paired with a result. Only the item half is read here — a result in an
// enumeration dump would be a result from a session we did not run.
type cosmicWorkItem struct {
	JobID      string  `json:"job_id"`
	ModulePath string  `json:"module_path"`
	Operator   string  `json:"operator_name"`
	Occurrence int64   `json:"occurrence"`
	StartPos   []int64 `json:"start_pos"`
	EndPos     []int64 `json:"end_pos"`
}

// ParseEnumeration reads `cosmic-ray dump` output: JSON per line, each line
// either a bare work item or a [work_item, result] pair.
//
// It is PURE and it is the half of this adapter that testdata/mutation
// exercises with no tool installed. A line it cannot parse is SKIPPED and
// the mutant is simply not enumerated — never invented with a guessed
// position, because a mutant at the wrong line would be selected by the
// canonical order against a line the candidate never wrote.
func (cosmicRayTool) ParseEnumeration(stdout []byte) ([]Mutant, error) {
	out := []Mutant{}
	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	parsed, skipped := 0, 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		item, ok := decodeCosmicLine([]byte(line))
		if !ok {
			skipped++
			continue
		}
		parsed++
		if item.ModulePath == "" || item.Operator == "" {
			continue
		}
		m := Mutant{
			Path:     item.ModulePath,
			Operator: item.Operator,
			Ref:      item.JobID,
		}
		if len(item.StartPos) >= 2 {
			m.Line, m.Col = item.StartPos[0], item.StartPos[1]
		}
		out = append(out, m.WithDigest())
	}
	if parsed == 0 && skipped > 0 {
		// A dump whose shape we do not recognize yields NO mutants and an
		// error, never a count: this is where a tool-version change shows
		// up, and a silent zero would read as "your diff is unmutable".
		return nil, fmt.Errorf("no recognizable work item in %d line(s) of `cosmic-ray dump` output", skipped)
	}
	return out, nil
}

// decodeCosmicLine accepts both dump shapes.
func decodeCosmicLine(b []byte) (cosmicWorkItem, bool) {
	var item cosmicWorkItem
	if err := json.Unmarshal(b, &item); err == nil && item.ModulePath != "" {
		return item, true
	}
	var pair []json.RawMessage
	if err := json.Unmarshal(b, &pair); err != nil || len(pair) == 0 {
		return cosmicWorkItem{}, false
	}
	if err := json.Unmarshal(pair[0], &item); err != nil || item.ModulePath == "" {
		return cosmicWorkItem{}, false
	}
	return item, true
}

// ExecArgv runs ONE mutant: `cosmic-ray worker` applies the mutation,
// executes the configured test command, and prints a JSON WorkResult. One
// mutant per invocation is what makes the control plane's selection real —
// `cosmic-ray exec` would run whatever the session holds.
func (cosmicRayTool) ExecArgv(python string, m Mutant, run MutationRun) []string {
	return []string{python, "-m", cosmicCLI, "worker",
		"--config", run.ConfigPath, m.Path, m.Operator, fmt.Sprint(m.Occurrence())}
}

// Occurrence recovers cosmic-ray's occurrence index from the mutant's ref
// ("<module>:<operator>:<occurrence>" or a bare integer). A ref that
// carries none is occurrence 0, which is what a single-occurrence operator
// has anyway.
func (m Mutant) Occurrence() int64 {
	parts := strings.Split(m.Ref, ":")
	last := parts[len(parts)-1]
	var n int64
	if _, err := fmt.Sscanf(last, "%d", &n); err != nil {
		return 0
	}
	return n
}

// cosmicWorkResult is the worker's JSON verdict.
type cosmicWorkResult struct {
	WorkerOutcome string `json:"worker_outcome"`
	TestOutcome   string `json:"test_outcome"`
	Diff          string `json:"diff"`
	Output        string `json:"output"`
}

// ParseExec reads the worker's claim. It supplies the mutant DIFF for the
// report — the actionable half of a survivor — and nothing else: the
// outcome that reaches a metric is derived from the evidence stream, and a
// tool's own summary is a file the run wrote about itself.
func (cosmicRayTool) ParseExec(stdout []byte) (ToolClaim, error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return ToolClaim{}, nil
	}
	// The worker prints one JSON object; a run that also printed test
	// output leaves it on the LAST line.
	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var res cosmicWorkResult
		if err := json.Unmarshal([]byte(strings.TrimSpace(lines[i])), &res); err == nil {
			return ToolClaim{Outcome: res.TestOutcome, Diff: res.Diff}, nil
		}
	}
	return ToolClaim{}, fmt.Errorf("no JSON WorkResult in `cosmic-ray worker` output")
}

// cosmicOperatorFamilies maps our portable operator families onto
// cosmic-ray's own operator names. The mapping is OURS and is reviewed like
// any other table entry: a policy pins a family, the adapter pins the
// spelling, and a tool that renames an operator breaks a golden test rather
// than silently mutating nothing.
var cosmicOperatorFamilies = map[string][]string{
	"arithmetic":   {"core/ReplaceBinaryOperator_Add_Sub", "core/ReplaceBinaryOperator_Mul_Div", "core/ReplaceUnaryOperator_USub_UAdd"},
	"comparison":   {"core/ReplaceComparisonOperator_Lt_LtE", "core/ReplaceComparisonOperator_Eq_NotEq", "core/ReplaceComparisonOperator_Gt_GtE"},
	"boolean":      {"core/ReplaceAndWithOr", "core/ReplaceOrWithAnd", "core/ReplaceTrueWithFalse", "core/ReplaceFalseWithTrue"},
	"constant":     {"core/NumberReplacer", "core/StringReplacer"},
	"control-flow": {"core/ReplaceBreakWithContinue", "core/ReplaceContinueWithBreak", "core/ZeroIterationForLoop"},
	"exception":    {"core/ExceptionReplacer"},
	"decorator":    {"core/RemoveDecorator"},
}

// cosmicOperators expands the declared families, sorted and deduplicated.
func cosmicOperators(families []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, f := range families {
		for _, name := range cosmicOperatorFamilies[f] {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// firstPath is the module path a session is initialized over.
func firstPath(t TargetSet) string {
	if len(t.Files) == 0 {
		return ""
	}
	return t.Files[0].Path
}

// quoteJoin renders a TOML string list body.
func quoteJoin(in []string) string {
	parts := make([]string, 0, len(in))
	for _, s := range in {
		parts = append(parts, fmt.Sprintf("%q", s))
	}
	return strings.Join(parts, ", ")
}
