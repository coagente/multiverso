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

// WriteTreeTemp snapshots dir's WORKING TREE as a git tree object through
// a TEMPORARY index, and returns its TreePrefix-ed digest.
//
// The temporary index is the point (M1f): the tree-guard must be able to
// ask "what does this worktree contain right now" without touching the
// index the caller owns — an operator's staged work in `mvo guard`, or a
// world's index that AG-4's patch capture is about to read. GIT_INDEX_FILE
// points at a scratch file that is removed afterwards, and .gitignore
// semantics stay exactly what `git add -A` sees, so the guard's view of
// the tree and the captured patch's view of it cannot drift.
func WriteTreeTemp(dir string) (string, error) {
	tmp, err := os.CreateTemp("", "mvo-index-")
	if err != nil {
		return "", fmt.Errorf("gitx: temp index: %w", err)
	}
	idx := tmp.Name()
	_ = tmp.Close()
	// git refuses to read a zero-length file as an index, so start clean.
	_ = os.Remove(idx)
	defer os.Remove(idx)

	env := append(gitEnv(), "GIT_INDEX_FILE="+idx)
	runIdx := func(args ...string) (string, error) {
		full := append(append([]string{}, baseArgs...), args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = dir
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("gitx: git %s in %s: %w: %s",
				strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(stdout.String()), nil
	}
	if _, err := runIdx("add", "-A"); err != nil {
		return "", err
	}
	out, err := runIdx("write-tree")
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
	return commitTree(repo, tree, parent, message, nil)
}

// commitTree is the shared commit-tree plumbing behind CommitTree (real
// timestamps, admission commits) and CommitTreeEpoch (pinned timestamps,
// publication commits).
func commitTree(repo, tree, parent, message string, extraEnv []string) (string, error) {
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
	cmd.Env = append(cmd.Env, extraEnv...)
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
// the ref no longer points at oldCommit (trunk moved mid-admission). An
// oldCommit of "" means the ref must not exist — create-only CAS.
func UpdateRef(repo, ref, newCommit, oldCommit string) error {
	_, err := run(repo, "update-ref", ref, newCommit, oldCommit)
	return err
}

// PublishEpoch pins publication-commit timestamps (M1d decision 2):
// publication commits are transport containers, not events — time lives in
// the ledger — and a fixed date is what makes idempotent republish
// structural (identical content re-mints identical shas).
const PublishEpoch = "1970-01-01T00:00:00Z"

// HashObject writes data into repo's object database as a blob
// (hash-object -w --stdin) and returns its sha.
func HashObject(repo string, data []byte) (string, error) {
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "hash-object", "-w", "--stdin")...)
	cmd.Dir = repo
	cmd.Env = gitEnv()
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gitx: git hash-object in %s: %w: %s",
			repo, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// TreeEntry is one tree line for Mktree input and LsTreeRecursive output.
// For LsTreeRecursive, Name is the full path relative to the tree root.
type TreeEntry struct{ Mode, Type, SHA, Name string }

// Mktree builds a tree object from entries (pre-sorted by Name by the
// caller) and returns its sha. Entries are one level deep — nested trees
// are built bottom-up by the caller.
func Mktree(repo string, entries []TreeEntry) (string, error) {
	var input bytes.Buffer
	for _, e := range entries {
		fmt.Fprintf(&input, "%s %s %s\t%s\n", e.Mode, e.Type, e.SHA, e.Name)
	}
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "mktree")...)
	cmd.Dir = repo
	cmd.Env = gitEnv()
	cmd.Stdin = &input
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gitx: git mktree in %s: %w: %s",
			repo, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// CommitTreeEpoch is CommitTree with author and committer dates pinned to
// PublishEpoch: the commit sha becomes a pure function of (tree, parent,
// message) under the fixed mvo identity.
func CommitTreeEpoch(repo, tree, parent, message string) (string, error) {
	return commitTree(repo, tree, parent, message, []string{
		"GIT_AUTHOR_DATE=" + PublishEpoch,
		"GIT_COMMITTER_DATE=" + PublishEpoch,
	})
}

// RefValue resolves ref to a sha; an absent ref is ("", nil), not an error
// (rev-parse --verify --quiet: exit 1 with silent stderr means absent).
func RefValue(repo, ref string) (string, error) {
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "rev-parse", "--verify", "--quiet", ref)...)
	cmd.Dir = repo
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.TrimSpace(stderr.String()) == "" {
			return "", nil
		}
		return "", fmt.Errorf("gitx: git rev-parse --verify --quiet %s in %s: %w: %s",
			ref, repo, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ForEachRef enumerates the refs under prefix as a ref → sha map.
func ForEachRef(repo, prefix string) (map[string]string, error) {
	out, err := run(repo, "for-each-ref", "--format=%(objectname) %(refname)", prefix)
	if err != nil {
		return nil, err
	}
	refs := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		refs[ref] = sha
	}
	return refs, nil
}

// DeleteRef deletes ref iff it still points at oldCommit (update-ref -d
// compare-and-swap).
func DeleteRef(repo, ref, oldCommit string) error {
	_, err := run(repo, "update-ref", "-d", ref, oldCommit)
	return err
}

// LsRemote lists remote's refs matching pattern as a ref → sha map. An
// empty namespace is an empty map; an unreachable remote is an error.
//
// The result is anchored to pattern's fixed prefix: git ls-remote
// tail-matches its pattern from any slash boundary (it rewrites the pattern
// to "*/<pattern>"), so a ref merely CONTAINING the pattern —
// refs/heads/refs/multiverso/intent/<short>/wip — comes back too. Callers
// turn this survey into delete refspecs, so an unanchored result is a
// deletion of refs outside the surveyed namespace; the anchor is what makes
// "publish and prune only ever touch their own namespace" true rather than
// merely intended.
func LsRemote(repo, remote, pattern string) (map[string]string, error) {
	out, err := run(repo, "ls-remote", remote, pattern)
	if err != nil {
		return nil, err
	}
	fixed, _, _ := strings.Cut(pattern, "*")
	refs := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || !strings.HasPrefix(ref, fixed) {
			continue
		}
		refs[ref] = sha
	}
	return refs, nil
}

// MultiversoRefRoot is the only ref namespace Push may write to.
const MultiversoRefRoot = "refs/multiverso/"

// Push pushes explicit refspecs — "<sha>:<ref>" (update/create) or
// ":<ref>" (delete) — each guarded by --force-with-lease=<ref>:<old> from
// leases; a lease value of "" means the ref must not exist (expect-absent).
//
// Two properties are enforced here rather than assumed:
//   - Every refspec destination must sit under MultiversoRefRoot. M1d
//     decision 10 claims refs/heads can never appear "by construction";
//     construction only covers the ref builders, not a survey that fed the
//     refspec list, so the claim is asserted.
//   - --atomic: the batch lands whole or not at all. Publish and prune both
//     record what a push did in an append-only ledger and treat a failed
//     push as "nothing landed"; without atomicity a partially applied batch
//     makes that record permanently wrong.
func Push(repo, remote string, refspecs []string, leases map[string]string) error {
	for _, spec := range refspecs {
		dst := spec
		if _, after, ok := strings.Cut(spec, ":"); ok {
			dst = after
		}
		if !strings.HasPrefix(dst, MultiversoRefRoot) {
			return fmt.Errorf("gitx: refusing to push refspec %q: destination is outside %s", spec, MultiversoRefRoot)
		}
	}
	args := []string{"push", "--atomic"}
	leased := make([]string, 0, len(leases))
	for ref := range leases {
		leased = append(leased, ref)
	}
	slices.Sort(leased)
	for _, ref := range leased {
		args = append(args, "--force-with-lease="+ref+":"+leases[ref])
	}
	args = append(args, remote)
	args = append(args, refspecs...)
	_, err := run(repo, args...)
	return err
}

// Fetch fetches refspecs from remote, pruning local refs the refspecs
// cover that vanished remotely when prune is set.
func Fetch(repo, remote string, refspecs []string, prune bool) error {
	args := []string{"fetch"}
	if prune {
		args = append(args, "--prune")
	}
	args = append(args, remote)
	args = append(args, refspecs...)
	_, err := run(repo, args...)
	return err
}

// LsTreeRecursive lists every blob under treeish (ls-tree -r -z); each
// entry's Name is the full path.
func LsTreeRecursive(repo, treeish string) ([]TreeEntry, error) {
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "ls-tree", "-r", "-z", treeish)...)
	cmd.Dir = repo
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gitx: git ls-tree -r %s in %s: %w: %s",
			treeish, repo, err, strings.TrimSpace(stderr.String()))
	}
	var entries []TreeEntry
	for _, rec := range strings.Split(stdout.String(), "\x00") {
		if rec == "" {
			continue
		}
		meta, name, ok := strings.Cut(rec, "\t")
		if !ok {
			return nil, fmt.Errorf("gitx: ls-tree entry %q has no path", rec)
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return nil, fmt.Errorf("gitx: ls-tree entry %q is malformed", rec)
		}
		entries = append(entries, TreeEntry{Mode: fields[0], Type: fields[1], SHA: fields[2], Name: name})
	}
	return entries, nil
}

// IgnoredFiles lists the repo-relative paths that exist in dir's working
// tree but that git's exclude rules keep OUT of `git add -A` — and
// therefore out of WriteTreeTemp's snapshot.
//
// The tree-guard compares git trees, which is what makes its evidence
// unforgeable; the cost is that everything it can see has to be something
// `git add -A` would stage. pytest has no such rule: it loads a
// `conftest.py` that `.gitignore` names, and `.gitignore` is a file a
// candidate may edit. Without this list `mvo guard` reports a clean tree
// while a live harness file sits in it, which is a false clean verdict on
// the one verb an evaluating maintainer runs before adopting anything.
func IgnoredFiles(dir string) ([]string, error) {
	// Deliberately NOT --directory: an ignored directory collapsed to one
	// entry hides the very file this exists to find (a `*.egg-info/`
	// carrying a pytest11 entry point is the worked example). Listing
	// every ignored path is one git process, and being right matters more
	// here than being brief.
	out, err := run(dir, "ls-files", "--others", "--ignored", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// CatBlob returns the raw bytes of a blob object — no trimming (the bytes
// are evidence; a trailing newline is significant), so this takes its own
// exec path instead of run()'s TrimSpace (the DiffCached precedent).
func CatBlob(repo, sha string) ([]byte, error) {
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "cat-file", "blob", sha)...)
	cmd.Dir = repo
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gitx: git cat-file blob %s in %s: %w: %s",
			sha, repo, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// MergeBase returns the merge base of a and b, or ("", nil) when they share
// no common ancestor (merge-base: exit 1 with silent stderr).
func MergeBase(repo, a, b string) (string, error) {
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "merge-base", a, b)...)
	cmd.Dir = repo
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.TrimSpace(stderr.String()) == "" {
			return "", nil
		}
		return "", fmt.Errorf("gitx: git merge-base %s %s in %s: %w: %s",
			a, b, repo, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// CommitExists reports whether sha resolves to a commit in repo's object
// database.
func CommitExists(repo, sha string) bool {
	_, err := run(repo, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// RemotePushRefspecs returns remote.<remote>.push refspecs; an unset key is
// (nil, nil). Publish pre-flight warns when one covers refs/multiverso
// (M1d decision 17 — the mirror-refspec foot-gun).
func RemotePushRefspecs(repo, remote string) ([]string, error) {
	cmd := exec.Command("git", append(append([]string{}, baseArgs...), "config", "--get-all", "remote."+remote+".push")...)
	cmd.Dir = repo
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.TrimSpace(stderr.String()) == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("gitx: git config --get-all remote.%s.push in %s: %w: %s",
			remote, repo, err, strings.TrimSpace(stderr.String()))
	}
	var specs []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			specs = append(specs, line)
		}
	}
	return specs, nil
}

// RemoteURL resolves remote's URL; an unconfigured remote is an error
// (publish pre-flight: fail before anything is recorded).
func RemoteURL(repo, remote string) (string, error) {
	return run(repo, "remote", "get-url", remote)
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
