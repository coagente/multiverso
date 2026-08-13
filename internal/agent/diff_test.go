package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/gitx"
)

// Diff captures untracked files: add -A stages them before the cached
// diff, and the patch replays onto a fresh worktree at the base.
func TestDiffCapturesUntrackedFiles(t *testing.T) {
	repo := initRepo(t)
	commit := git(t, repo, "rev-parse", "HEAD")
	baseTree, err := gitx.TreeDigest(repo)
	if err != nil {
		t.Fatal(err)
	}

	// Modify a tracked file and create an untracked one, agent-style.
	if err := os.WriteFile(filepath.Join(repo, "x.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "NEW.txt"), []byte("hello new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patch, err := Diff(repo, baseTree)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	s := string(patch)
	if !strings.Contains(s, "diff --git a/x.txt b/x.txt") {
		t.Errorf("patch missing tracked-file diff:\n%s", s)
	}
	if !strings.Contains(s, "diff --git a/NEW.txt b/NEW.txt") {
		t.Errorf("patch missing untracked-file diff:\n%s", s)
	}

	// The captured patch replays onto a fresh worktree at the base commit
	// and reproduces the exact tree.
	wt := filepath.Join(t.TempDir(), "wt")
	if err := gitx.AddWorktree(repo, wt, commit); err != nil {
		t.Fatal(err)
	}
	defer gitx.RemoveWorktree(repo, wt)
	if err := gitx.Apply(wt, patch); err != nil {
		t.Fatalf("captured patch does not apply: %v", err)
	}
	wantTree, err := gitx.WriteTree(repo)
	if err != nil {
		t.Fatal(err)
	}
	gotTree, err := gitx.WriteTree(wt)
	if err != nil {
		t.Fatal(err)
	}
	if gotTree != wantTree {
		t.Errorf("replayed tree = %s, want %s", gotTree, wantTree)
	}
}

// Diff survives an agent that commits despite instructions: the index
// reflects final worktree content regardless of where HEAD moved (AG-4).
func TestDiffSurvivesInWorldCommit(t *testing.T) {
	repo := initRepo(t)
	baseTree, err := gitx.TreeDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "x.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "agent went rogue and committed")

	patch, err := Diff(repo, baseTree)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(string(patch), "-broken") || !strings.Contains(string(patch), "+fixed") {
		t.Errorf("patch does not carry the change made before the rogue commit:\n%s", patch)
	}
}

// An agent that did nothing yields a legal empty diff.
func TestDiffEmptyIsLegal(t *testing.T) {
	repo := initRepo(t)
	baseTree, err := gitx.TreeDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := Diff(repo, baseTree)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patch) != 0 {
		t.Errorf("patch = %q, want empty", patch)
	}
}

// DiffCached output is raw bytes: the trailing newline git emits is
// preserved (TrimSpace would corrupt patches).
func TestDiffOutputIsRaw(t *testing.T) {
	repo := initRepo(t)
	baseTree, err := gitx.TreeDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "x.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch, err := Diff(repo, baseTree)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(patch, []byte("\n")) {
		t.Error("patch lost its trailing newline (raw stdout required)")
	}
}
