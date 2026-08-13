package agent

import (
	"fmt"

	"github.com/coagente/multiverso/internal/object"
)

// promptTemplate is the normative world-scoped prompt (byte-exact; the
// verbs substitute the candidate ordinal, count, and task). The ordinal
// line is the AG-5 v0 variation hook and doubles as a world-digest
// uniqueness guarantee: two candidates producing identical trees,
// transcripts, and costs still differ in context.
const promptTemplate = `You are candidate %d of %d, working alone in an isolated git worktree.

# Task
%s

# Rules
- Modify files only inside the current directory.
- Do not commit, branch, push, or otherwise drive git; the control plane
  captures your working-tree changes when you exit.
- Do not read or modify .git or .multiverso.
- When the task is done, stop; do not wait for further input.
`

// RenderPrompt builds the world-scoped prompt: variation header + task.
// k is the 1-based candidate ordinal, n the candidate count. task is the
// operator override (--prompt/--prompt-file); "" renders the intent spec.
//
// AI-control rule (PRD AG-7 note, ch. 13), normative: the rendered prompt
// NEVER contains policy digests, hard gates, ranking keys, oracle
// commands, sibling-world content or status, scheduler state, or budget
// internals beyond what the tool's own flags already expose. The generator
// is untrusted; anything it learns about the gate is a gaming surface.
func RenderPrompt(spec object.Spec, k, n int, task string) string {
	if task == "" {
		task = fmt.Sprintf("Title: %s\n\n%s", spec.Title, spec.Description)
	}
	return fmt.Sprintf(promptTemplate, k, n, task)
}
