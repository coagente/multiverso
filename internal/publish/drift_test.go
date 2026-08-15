package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// driftRepo builds a repo with one baseline commit and returns (repo,
// baseline sha).
func driftRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	gitT(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "baseline")
	return repo, gitT(t, repo, "rev-parse", "HEAD")
}

func advance(t *testing.T, repo, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "advance")
	return gitT(t, repo, "rev-parse", "HEAD")
}

// TestTrunkDrift covers the five normative rows with byte-exact details.
func TestTrunkDrift(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		repo, base := driftRepo(t)
		status, detail := TrunkDrift(repo, base)
		if status != DriftFresh {
			t.Fatalf("status = %s, want FRESH", status)
		}
		if want := "base " + base[:12] + " == main head"; detail != want {
			t.Errorf("detail = %q, want %q", detail, want)
		}
	})
	t.Run("stale advanced", func(t *testing.T) {
		repo, base := driftRepo(t)
		advance(t, repo, "two\n")
		status, detail := TrunkDrift(repo, base)
		if status != DriftStale {
			t.Fatalf("status = %s, want STALE", status)
		}
		if want := "main advanced past base " + base[:12]; detail != want {
			t.Errorf("detail = %q, want %q", detail, want)
		}
	})
	t.Run("stale diverged", func(t *testing.T) {
		repo, root := driftRepo(t)
		base := advance(t, repo, "two\n") // the intent base, soon rewritten away
		gitT(t, repo, "reset", "-q", "--hard", root)
		head := advance(t, repo, "rewritten\n")
		status, detail := TrunkDrift(repo, base)
		if status != DriftStale {
			t.Fatalf("status = %s, want STALE", status)
		}
		want := "base " + base[:12] + " is not an ancestor of main head " + head[:12]
		if detail != want {
			t.Errorf("detail = %q, want %q", detail, want)
		}
	})
	t.Run("detached head", func(t *testing.T) {
		repo, base := driftRepo(t)
		gitT(t, repo, "checkout", "-q", "--detach")
		status, detail := TrunkDrift(repo, base)
		if status != DriftUnknown || detail != "detached HEAD" {
			t.Errorf("= (%s, %q), want (UNKNOWN, \"detached HEAD\")", status, detail)
		}
	})
	t.Run("base missing", func(t *testing.T) {
		repo, _ := driftRepo(t)
		bogus := strings.Repeat("1", 40)
		status, detail := TrunkDrift(repo, bogus)
		if status != DriftUnknown {
			t.Fatalf("status = %s, want UNKNOWN", status)
		}
		if want := "base commit " + bogus[:12] + " not found"; detail != want {
			t.Errorf("detail = %q, want %q", detail, want)
		}
	})
}
