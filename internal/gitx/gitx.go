// Package gitx shells out to the system git for worktree, patch, and
// tree-digest operations (XP-1, AG-4). No go-git: git itself is the
// engine. Every command runs with hooks disabled and stderr captured
// into returned errors.
package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// TreePrefix tags git tree digests in v0 objects ("git:<sha1>").
const TreePrefix = "git:"

// repoEnvVars redirect git away from cmd.Dir (repo location, worktree,
// index, object store). Git hooks, `git rebase -x`, and some CI contexts
// export them, which would silently point every command at a different
// repository.
var repoEnvVars = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_PREFIX",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR",
}

// gitEnv returns the parent environment with repoEnvVars stripped, so
// cmd.Dir is the sole authority over which repository a command touches.
func gitEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(repoEnvVars, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// run executes git in dir with hooks disabled and returns trimmed stdout.
// On failure the captured stderr is folded into the error.
func run(dir string, args ...string) (string, error) {
	full := append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gitx: git %s in %s: %w: %s",
			strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// TreeDigest returns the digest of HEAD's tree, TreePrefix-ed.
func TreeDigest(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	return TreePrefix + out, nil
}

// Head returns HEAD's commit sha and TreePrefix-ed tree digest.
func Head(repo string) (commit, tree string, err error) {
	commit, err = run(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	tree, err = TreeDigest(repo)
	if err != nil {
		return "", "", err
	}
	return commit, tree, nil
}

// AddWorktree creates a detached worktree of repo at dst, checked out at
// commit. dst must not already exist. dst is pinned to an absolute path:
// git would resolve a relative dst against the repo (its cwd), not against
// the caller's, silently placing the worktree somewhere else.
func AddWorktree(repo, dst, commit string) error {
	abs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("gitx: resolve worktree path %s: %w", dst, err)
	}
	_, err = run(repo, "worktree", "add", "--detach", abs, commit)
	return err
}

// RemoveWorktree removes the worktree at dst registered in repo. --force:
// M0 worlds carry staged, uncommitted changes by design. dst is pinned to
// an absolute path for the same reason as in AddWorktree.
func RemoveWorktree(repo, dst string) error {
	abs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("gitx: resolve worktree path %s: %w", dst, err)
	}
	_, err = run(repo, "worktree", "remove", "--force", abs)
	return err
}

// Apply applies patch to dir's worktree and index via git apply --index,
// then git add -A so everything the patch touched is staged. M0 applies
// without committing; the resulting tree digest comes from WriteTree.
func Apply(dir string, patch []byte) error {
	cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null", "apply", "--index", "-")
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdin = bytes.NewReader(patch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gitx: git apply --index in %s: %w: %s",
			dir, err, strings.TrimSpace(stderr.String()))
	}
	if _, err := run(dir, "add", "-A"); err != nil {
		return err
	}
	return nil
}

// WriteTree writes dir's index as a tree object and returns its
// TreePrefix-ed digest — the world tree after Apply (worlds are
// uncommitted, so HEAD^{tree} would still be the base tree).
func WriteTree(dir string) (string, error) {
	out, err := run(dir, "write-tree")
	if err != nil {
		return "", err
	}
	return TreePrefix + out, nil
}
