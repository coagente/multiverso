// Package gitx shells out to the system git for worktree, patch, and
// tree-digest operations (XP-1, AG-4). No go-git: git itself is the
// engine. Every command runs with hooks disabled, config-driven command
// execution off (core.fsmonitor), user-level excludes neutralized
// (core.excludesFile — hermetic AND an agent cannot rely on the
// operator's global gitignore), and stderr captured into returned errors.
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

// Committer identity for admission commits (deterministic; real bot
// identity is FI-1, v1). Set via GIT_{AUTHOR,COMMITTER}_{NAME,EMAIL} on the
// commit-tree command env so ambient env vars cannot override it.
const (
	CommitterName  = "mvo"
	CommitterEmail = "mvo@multiverso.invalid"
)

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

// baseArgs harden every git invocation: hooks disabled (M0), fsmonitor
// off and user-level excludes neutralized (M1b AG-4 hardening — a world's
// git state is agent-writable after the run, and config-driven command
// execution or ambient ignore rules must never shape captured evidence).
var baseArgs = []string{
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.fsmonitor=false",
	"-c", "core.excludesFile=/dev/null",
}

// run executes git in dir with baseArgs applied and returns trimmed
// stdout. On failure the captured stderr is folded into the error.
func run(dir string, args ...string) (string, error) {
	full := append(append([]string{}, baseArgs...), args...)
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

// CommonDir returns the absolute path of dir's git common directory (the
// main repository's .git), symlinks resolved so paths compare stably.
func CommonDir(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(out)
	if err != nil {
		return "", fmt.Errorf("gitx: resolve git common dir %s: %w", out, err)
	}
	return resolved, nil
}

// VerifyWorktreeRepo checks that the worktree at dir still belongs to
// repo: its git common dir must be repo's own. A worktree's `.git`
// pointer is a plain file inside the world — agent-writable — so after an
// agent runs, a mismatch (or an unreadable git identity) means the
// world's git state was replaced and no git-derived evidence from it can
// be trusted (AG-4).
func VerifyWorktreeRepo(repo, dir string) error {
	want, err := CommonDir(repo)
	if err != nil {
		return err
	}
	got, err := CommonDir(dir)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("gitx: worktree %s points at git dir %s, not the control plane's %s", dir, got, want)
	}
	return nil
}

// PruneWorktrees drops stale worktree registrations from repo — the
// companion of removing a corrupted worktree directory that `git worktree
// remove` itself refused to handle.
func PruneWorktrees(repo string) error {
	_, err := run(repo, "worktree", "prune")
	return err
}

// Apply applies patch to dir's worktree and index via git apply --index,
// then git add -A so everything the patch touched is staged. M0 applies
// without committing; the resulting tree digest comes from WriteTree.
func Apply(dir string, patch []byte) error {
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "apply", "--index", "-")...)
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

// AddAll stages everything in dir's worktree (git add -A), untracked files
// included. Idempotent. Staging before diffing is what makes control-plane
// diff capture see files the agent created (AG-4).
func AddAll(dir string) error {
	_, err := run(dir, "add", "-A")
	return err
}

// DiffCached returns the binary patch from baseTree (accepted with or
// without TreePrefix) to dir's index: git diff --binary --cached <tree>.
// The returned bytes are RAW stdout — no trimming, because the trailing
// newline is significant to git apply — so this takes its own exec path
// instead of run()'s TrimSpace. An empty diff is legal (nothing changed).
func DiffCached(dir, baseTree string) ([]byte, error) {
	sha := strings.TrimPrefix(baseTree, TreePrefix)
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "diff", "--binary", "--cached", sha)...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gitx: git diff --binary --cached in %s: %w: %s",
			dir, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// ApplyCapture is Apply with the streams captured for evidence: it applies
// the patch (git apply --index, then git add -A) and returns git's
// stdout/stderr bytes; applyErr is non-nil on conflict, and the captured
// stderr is CP-8's conflict set. Apply stays as-is for race.
func ApplyCapture(dir string, patch []byte) (stdout, stderr []byte, applyErr error) {
	var outBuf, errBuf bytes.Buffer
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "apply", "--index", "-")...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdin = bytes.NewReader(patch)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return outBuf.Bytes(), errBuf.Bytes(),
			fmt.Errorf("gitx: git apply --index in %s: %w", dir, err)
	}
	add := exec.Command("git", append(append([]string{}, baseArgs...), "add", "-A")...)
	add.Dir = dir
	add.Env = gitEnv()
	add.Stdout = &outBuf
	add.Stderr = &errBuf
	if err := add.Run(); err != nil {
		return outBuf.Bytes(), errBuf.Bytes(),
			fmt.Errorf("gitx: git add -A in %s: %w", dir, err)
	}
	return outBuf.Bytes(), errBuf.Bytes(), nil
}

// CurrentBranch returns the short name of the branch HEAD points at; a
// detached HEAD is an error.
func CurrentBranch(repo string) (string, error) {
	return run(repo, "symbolic-ref", "--short", "HEAD")
}

// ResolveCommit resolves rev to a full commit sha.
func ResolveCommit(repo, rev string) (string, error) {
	return run(repo, "rev-parse", "--verify", rev+"^{commit}")
}

// TreeOf returns commit's tree digest, TreePrefix-ed.
func TreeOf(repo, commit string) (string, error) {
	out, err := run(repo, "rev-parse", commit+"^{tree}")
	if err != nil {
		return "", err
	}
	return TreePrefix + out, nil
}

// ParentOf returns the sha of commit's first parent; a root commit is an
// error.
func ParentOf(repo, commit string) (string, error) {
	return run(repo, "rev-parse", "--verify", commit+"^")
}

// CommitMessage returns commit's full message (subject + body + trailers),
// trailing whitespace trimmed.
func CommitMessage(repo, commit string) (string, error) {
	return run(repo, "log", "-1", "--format=%B", commit)
}

// CommitTree creates a commit object for tree (with or without TreePrefix)
// on parent, message on stdin, under the fixed mvo identity — plumbing
// only, no working tree involved. Returns the new commit sha.
func CommitTree(repo, tree, parent, message string) (string, error) {
	sha := strings.TrimPrefix(tree, TreePrefix)
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "commit-tree", sha, "-p", parent)...)
	cmd.Dir = repo
	// Appended after the ambient env so the fixed identity always wins.
	cmd.Env = append(gitEnv(),
		"GIT_AUTHOR_NAME="+CommitterName,
		"GIT_AUTHOR_EMAIL="+CommitterEmail,
		"GIT_COMMITTER_NAME="+CommitterName,
		"GIT_COMMITTER_EMAIL="+CommitterEmail,
	)
	cmd.Stdin = strings.NewReader(message)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gitx: git commit-tree in %s: %w: %s",
			repo, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// UpdateRef compare-and-swaps ref from oldCommit to newCommit; it fails if
// the ref no longer points at oldCommit (trunk moved mid-admission).
func UpdateRef(repo, ref, newCommit, oldCommit string) error {
	_, err := run(repo, "update-ref", ref, newCommit, oldCommit)
	return err
}

// StatusClean reports whether repo's working tree and index are clean
// (git status --porcelain prints nothing).
func StatusClean(repo string) (bool, error) {
	out, err := run(repo, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// ResetHard resets repo's working tree and index to HEAD. Only called when
// StatusClean reported true before the ref moved: a pure fast-forward of
// already-committed content.
func ResetHard(repo string) error {
	_, err := run(repo, "reset", "--hard", "--quiet")
	return err
}
