package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// git runs git in dir with identity and signing pinned so temp repos work
// on any machine, failing the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := []string{
		"-c", "user.name=mvo-test",
		"-c", "user.email=mvo-test@invalid",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// initRepo creates a temp git repo with hello.txt committed.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

const modifyPatch = `--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-hello
+goodbye
`

const createPatch = `--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+created
`

const mismatchPatch = `--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-something else entirely
+goodbye
`

func TestHeadAndTreeDigest(t *testing.T) {
	repo := initRepo(t)
	commit, tree, err := Head(repo)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if !shaRe.MatchString(commit) {
		t.Errorf("commit = %q, want 40-hex sha", commit)
	}
	sha, ok := strings.CutPrefix(tree, TreePrefix)
	if !ok || !shaRe.MatchString(sha) {
		t.Errorf("tree = %q, want %q + 40-hex sha", tree, TreePrefix)
	}
	if commit != git(t, repo, "rev-parse", "HEAD") {
		t.Errorf("commit = %q, disagrees with rev-parse HEAD", commit)
	}
	if want := TreePrefix + git(t, repo, "rev-parse", "HEAD^{tree}"); tree != want {
		t.Errorf("tree = %q, want %q", tree, want)
	}
	td, err := TreeDigest(repo)
	if err != nil {
		t.Fatalf("TreeDigest: %v", err)
	}
	if td != tree {
		t.Errorf("TreeDigest = %q, Head tree = %q", td, tree)
	}
}

// GIT_DIR / GIT_WORK_TREE in the parent environment (git hooks, `git
// rebase -x`, CI) must not redirect gitx away from the directory it was
// asked to operate on.
func TestEnvDoesNotRedirectRepo(t *testing.T) {
	repoA := initRepo(t)
	repoB := initRepo(t)
	if err := os.WriteFile(filepath.Join(repoB, "other.txt"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repoB, "add", "-A")
	git(t, repoB, "commit", "-q", "-m", "diverge")

	wantA := git(t, repoA, "rev-parse", "HEAD")
	headB := git(t, repoB, "rev-parse", "HEAD")
	if wantA == headB {
		t.Fatal("fixture repos share a HEAD; test cannot distinguish them")
	}

	t.Setenv("GIT_DIR", filepath.Join(repoB, ".git"))
	t.Setenv("GIT_WORK_TREE", repoB)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(repoB, ".git", "index"))

	commit, _, err := Head(repoA)
	if err != nil {
		t.Fatalf("Head with GIT_DIR set: %v", err)
	}
	if commit == headB {
		t.Fatalf("Head(%s) returned repoB's HEAD %s: GIT_DIR redirected the command", repoA, commit)
	}
	if commit != wantA {
		t.Fatalf("Head = %q, want %q", commit, wantA)
	}

	// Apply must also stay in its own worktree.
	dst := filepath.Join(t.TempDir(), "wt")
	if err := AddWorktree(repoA, dst, commit); err != nil {
		t.Fatalf("AddWorktree with GIT_DIR set: %v", err)
	}
	if err := Apply(dst, []byte(modifyPatch)); err != nil {
		t.Fatalf("Apply with GIT_DIR set: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "goodbye\n" {
		t.Fatalf("hello.txt = %q, want patched content", got)
	}
	if err := RemoveWorktree(repoA, dst); err != nil {
		t.Fatalf("RemoveWorktree with GIT_DIR set: %v", err)
	}
}

func TestHeadErrorsOutsideRepo(t *testing.T) {
	if _, _, err := Head(t.TempDir()); err == nil {
		t.Fatal("Head on a non-repo dir: want error, got nil")
	} else if !strings.Contains(err.Error(), "gitx: git rev-parse") {
		t.Errorf("error %q does not name the failing command", err)
	}
}

func TestWorktreeLifecycle(t *testing.T) {
	repo := initRepo(t)
	commit, _, err := Head(repo)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "wt")
	if err := AddWorktree(repo, dst, commit); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "hello.txt")); err != nil {
		t.Fatalf("worktree missing checked-out file: %v", err)
	}
	// The worktree carries staged, uncommitted changes when removed —
	// the M0 world lifecycle.
	if err := Apply(dst, []byte(modifyPatch)); err != nil {
		t.Fatalf("Apply in worktree: %v", err)
	}
	if err := RemoveWorktree(repo, dst); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still present after remove: stat err = %v", err)
	}
}

// A relative dst must resolve against the caller's cwd, not against the
// repo (git's own cwd): otherwise the caller's Go-side path and git's
// on-disk worktree diverge (mvo race --dir <relative> hit exactly this).
func TestWorktreeRelativeDst(t *testing.T) {
	repo := initRepo(t)
	commit, _, err := Head(repo)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	work := t.TempDir()
	t.Chdir(work)
	if err := AddWorktree(repo, "relwt", commit); err != nil {
		t.Fatalf("AddWorktree with relative dst: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "relwt", "hello.txt")); err != nil {
		t.Fatalf("worktree not at caller-relative path: %v", err)
	}
	if err := RemoveWorktree(repo, "relwt"); err != nil {
		t.Fatalf("RemoveWorktree with relative dst: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "relwt")); !os.IsNotExist(err) {
		t.Fatalf("worktree still present after remove: stat err = %v", err)
	}
}

func TestAddWorktreeBadCommit(t *testing.T) {
	repo := initRepo(t)
	dst := filepath.Join(t.TempDir(), "wt")
	if err := AddWorktree(repo, dst, "0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("AddWorktree with bogus commit: want error, got nil")
	}
}

func TestApplyAndWriteTree(t *testing.T) {
	tests := []struct {
		name     string
		patch    string
		wantErr  bool
		wantFile string // relative path expected to hold wantText
		wantText string
	}{
		{"modify file", modifyPatch, false, "hello.txt", "goodbye\n"},
		{"create file", createPatch, false, "new.txt", "created\n"},
		{"malformed patch", "this is not a patch\n", true, "", ""},
		{"context mismatch", mismatchPatch, true, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := initRepo(t)
			commit, baseTree, err := Head(repo)
			if err != nil {
				t.Fatalf("Head: %v", err)
			}
			dst := filepath.Join(t.TempDir(), "wt")
			if err := AddWorktree(repo, dst, commit); err != nil {
				t.Fatalf("AddWorktree: %v", err)
			}
			err = Apply(dst, []byte(tt.patch))
			if tt.wantErr {
				if err == nil {
					t.Fatal("Apply: want error, got nil")
				}
				if !strings.Contains(err.Error(), "gitx: git apply") {
					t.Errorf("error %q does not name git apply", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(dst, tt.wantFile))
			if err != nil {
				t.Fatalf("read %s: %v", tt.wantFile, err)
			}
			if string(got) != tt.wantText {
				t.Errorf("%s = %q, want %q", tt.wantFile, got, tt.wantText)
			}
			tree, err := WriteTree(dst)
			if err != nil {
				t.Fatalf("WriteTree: %v", err)
			}
			if !strings.HasPrefix(tree, TreePrefix) {
				t.Errorf("WriteTree = %q, want %q prefix", tree, TreePrefix)
			}
			if tree == baseTree {
				t.Errorf("WriteTree returned the base tree %q; patch not reflected", tree)
			}
		})
	}
}

// The same patch applied to the same base yields the same tree digest in
// independent worktrees (NFR-1: world trees are reproducible).
func TestApplyDeterministicTree(t *testing.T) {
	repo := initRepo(t)
	commit, _, err := Head(repo)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	trees := make([]string, 2)
	for i := range trees {
		dst := filepath.Join(t.TempDir(), "wt")
		if err := AddWorktree(repo, dst, commit); err != nil {
			t.Fatalf("AddWorktree %d: %v", i, err)
		}
		if err := Apply(dst, []byte(modifyPatch)); err != nil {
			t.Fatalf("Apply %d: %v", i, err)
		}
		if trees[i], err = WriteTree(dst); err != nil {
			t.Fatalf("WriteTree %d: %v", i, err)
		}
		if err := RemoveWorktree(repo, dst); err != nil {
			t.Fatalf("RemoveWorktree %d: %v", i, err)
		}
	}
	if trees[0] != trees[1] {
		t.Errorf("tree digests differ across worktrees: %q vs %q", trees[0], trees[1])
	}
}
