package agent

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

// The normative template is byte-exact (design "Prompt template").
func TestRenderPromptGolden(t *testing.T) {
	spec := object.Spec{Title: "fix mean()", Description: "mean divides by len-1"}

	wantSpec := "You are candidate 1 of 2, working alone in an isolated git worktree.\n" +
		"\n" +
		"# Task\n" +
		"Title: fix mean()\n" +
		"\n" +
		"mean divides by len-1\n" +
		"\n" +
		"# Rules\n" +
		"- Modify files only inside the current directory.\n" +
		"- Do not commit, branch, push, or otherwise drive git; the control plane\n" +
		"  captures your working-tree changes when you exit.\n" +
		"- Do not read or modify .git or .multiverso.\n" +
		"- When the task is done, stop; do not wait for further input.\n"
	if got := RenderPrompt(spec, 1, 2, ""); got != wantSpec {
		t.Errorf("RenderPrompt(spec default) =\n%q\nwant\n%q", got, wantSpec)
	}

	wantTask := "You are candidate 3 of 5, working alone in an isolated git worktree.\n" +
		"\n" +
		"# Task\n" +
		"Do the thing.\n" +
		"\n" +
		"# Rules\n" +
		"- Modify files only inside the current directory.\n" +
		"- Do not commit, branch, push, or otherwise drive git; the control plane\n" +
		"  captures your working-tree changes when you exit.\n" +
		"- Do not read or modify .git or .multiverso.\n" +
		"- When the task is done, stop; do not wait for further input.\n"
	if got := RenderPrompt(spec, 3, 5, "Do the thing."); got != wantTask {
		t.Errorf("RenderPrompt(task override) =\n%q\nwant\n%q", got, wantTask)
	}
}

// AI-control rule (ch. 13, normative): render the prompt for a race and
// assert that nothing about the gate leaks into it — no policy digest, no
// hard-gate names, no ranking keys, no oracle argv.
func TestRenderPromptAIControlRule(t *testing.T) {
	policy := object.Policy{
		Schema:    object.SchemaPolicy,
		HardGates: []string{"suite-pass"},
		Ranking:   []string{"gate_pass", "wall_ms_asc"},
	}
	polDig, _, err := object.Digest(policy)
	if err != nil {
		t.Fatal(err)
	}
	oracleArgv := "python3 -m pytest -q"
	spec := object.Spec{Title: "fix mean()", Description: "mean divides by len-1"}

	forbidden := append([]string{polDig, oracleArgv, "pytest"},
		policy.HardGates...)
	forbidden = append(forbidden, policy.Ranking...)

	for k := 1; k <= 2; k++ {
		for _, task := range []string{"", "operator override task"} {
			prompt := RenderPrompt(spec, k, 2, task)
			for _, bad := range forbidden {
				if strings.Contains(prompt, bad) {
					t.Errorf("prompt (k=%d, task=%q) leaks %q:\n%s", k, task, bad, prompt)
				}
			}
		}
	}
}

// The ordinal line is the AG-5 v0 variation hook: two candidates with
// identical everything still render different prompts.
func TestRenderPromptOrdinalVariation(t *testing.T) {
	spec := object.Spec{Title: "t", Description: "d"}
	if RenderPrompt(spec, 1, 2, "") == RenderPrompt(spec, 2, 2, "") {
		t.Error("prompts for candidate 1 and 2 are identical; ordinal variation lost")
	}
}
