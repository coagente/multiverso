package agent

import "github.com/coagente/multiverso/internal/gitx"

// Diff captures the world's changes as a binary patch (AG-4): git add -A,
// then git diff --binary --cached <baseTree>. It never consults adapter
// output — one control-plane implementation for every adapter, so no
// adapter code path can ever supply its own diff. Staging first captures
// untracked files; diffing the index against the base tree is also robust
// against an agent that commits despite instructions (the index reflects
// final worktree content regardless of where HEAD moved). An empty diff is
// legal — the agent did nothing. baseTree arrives "git:"-prefixed and is
// stripped by gitx.
//
// Trust boundary: the caller must first verify the worktree's git
// identity is still the control plane's (gitx.VerifyWorktreeRepo) — the
// worktree's `.git` pointer is agent-writable. Known residual gap: an
// agent-written in-tree .gitignore can hide files the agent created from
// `git add -A`; admit re-runs the gate on the landing tree, so nothing
// hidden can land (see M1b design, "Diff capture").
func Diff(worldDir, baseTree string) ([]byte, error) {
	if err := gitx.AddAll(worldDir); err != nil {
		return nil, err
	}
	return gitx.DiffCached(worldDir, baseTree)
}
