package main

// M1f: `mvo guard`, the adoption wedge. The one verb an evaluating
// maintainer runs before adopting anything — and the one that must write
// nothing at all.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/oracle"
)

// guardRepo is an initialized workspace with a committed test file, so the
// default policy's protected set has something in it.
func guardRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitCLI(t, repo, "init", "-q")
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("billing.py", "def split(n):\n    return n\n")
	write("test_billing.py", "def test_split():\n    assert split(3) == 3\n")
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "-c", "user.name=t", "-c", "user.email=t@x.invalid", "commit", "-qm", "base")
	mustMvo(t, "init", "--dir", repo)
	return repo
}

func TestGuardCleanTreeExitsZero(t *testing.T) {
	repo := guardRepo(t)
	stdout, stderr, code := mvo(t, "guard", "--base", "HEAD", "--policy", "default", "--dir", repo)
	if code != exitOK {
		t.Fatalf("guard exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "OK: no protected or harness path was modified") {
		t.Errorf("guard output does not state the clean verdict:\n%s", stdout)
	}
	if !strings.Contains(stdout, "examined:") {
		t.Errorf("guard output does not say how much it examined:\n%s", stdout)
	}
}

// The two vectors the guard exists for: a modified test, and a NEW
// conftest.py. Both exit 1 and name the path.
func TestGuardNamesViolations(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(t *testing.T, repo string)
		wantKind   string
		wantPath   string
		wantExamin bool
	}{
		{
			name: "a weakened assertion",
			mutate: func(t *testing.T, repo string) {
				if err := os.WriteFile(filepath.Join(repo, "test_billing.py"),
					[]byte("def test_split():\n    assert split(3) >= 0\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantKind: "protected_modified", wantPath: "test_billing.py",
		},
		{
			name: "a new conftest.py",
			mutate: func(t *testing.T, repo string) {
				if err := os.WriteFile(filepath.Join(repo, "conftest.py"),
					[]byte("import atexit\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantKind: "harness_added", wantPath: "conftest.py",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := guardRepo(t)
			tc.mutate(t, repo)
			stdout, _, code := mvo(t, "guard", "--base", "HEAD", "--policy", "default", "--dir", repo)
			if code != exitFail {
				t.Fatalf("guard exit %d, want 1\n%s", code, stdout)
			}
			if !strings.Contains(stdout, "VIOLATION: "+tc.wantKind) ||
				!strings.Contains(stdout, tc.wantPath) {
				t.Errorf("guard output does not name %s %s:\n%s", tc.wantKind, tc.wantPath, stdout)
			}

			// --json emits the tree-guard-report shape, unchanged from the
			// artifact a race records.
			jsonOut, _, code := mvo(t, "guard", "--base", "HEAD", "--policy", "default",
				"--dir", repo, "--json")
			if code != exitFail {
				t.Fatalf("guard --json exit %d, want 1", code)
			}
			var report oracle.GuardReport
			if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
				t.Fatalf("guard --json is not a report: %v\n%s", err, jsonOut)
			}
			if report.Schema != oracle.SchemaTreeGuardReport {
				t.Errorf("schema = %q, want %q", report.Schema, oracle.SchemaTreeGuardReport)
			}
			if len(report.Violations) != 1 || report.Violations[0].Kind != tc.wantKind ||
				report.Violations[0].Path != tc.wantPath {
				t.Errorf("violations = %+v, want one %s on %s", report.Violations, tc.wantKind, tc.wantPath)
			}
		})
	}
}

// No ledger writes, ever: an adoption wedge that mutates the operator's
// workspace is not a wedge.
func TestGuardWritesNothing(t *testing.T) {
	repo := guardRepo(t)
	before := ledgerEventCount(t, repo)
	mvo(t, "guard", "--base", "HEAD", "--policy", "default", "--dir", repo)
	if err := os.WriteFile(filepath.Join(repo, "conftest.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mvo(t, "guard", "--base", "HEAD", "--policy", "default", "--dir", repo)
	if after := ledgerEventCount(t, repo); after != before {
		t.Errorf("ledger grew from %d to %d events; mvo guard must record nothing", before, after)
	}
	// And the operator's own index is untouched: the candidate tree is
	// snapshotted through a TEMPORARY index.
	out := gitCLI(t, repo, "status", "--porcelain")
	if !strings.Contains(out, "?? conftest.py") {
		t.Errorf("git status = %q, want conftest.py still untracked (the index was written to)", out)
	}
}

// A policy that declares no path set has nothing to guard, and says so
// rather than passing vacuously.
func TestGuardRefusesAPolicyWithNoPaths(t *testing.T) {
	repo := guardRepo(t)
	src := filepath.Join("..", "..", "testdata", "toyrepo", "policies", "no-paths.json")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".multiverso", "policies", "no-paths.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := mvo(t, "guard", "--base", "HEAD", "--policy", "no-paths", "--dir", repo)
	if code == exitOK {
		t.Fatal("guard exited 0 under a policy with no path set")
	}
	if !strings.Contains(stderr, "declares no protected or harness pattern") {
		t.Errorf("stderr = %q, want it to say there is nothing to guard", stderr)
	}
}

func ledgerEventCount(t *testing.T, repo string) int {
	t.Helper()
	out := mustMvo(t, "audit", "--dir", repo, "--json", "--cas-sweep=false")
	var report struct {
		Events int `json:"events"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("audit --json: %v\n%s", err, out)
	}
	return report.Events
}

// The blind spot the guard's own strength creates. It compares git TREES,
// and `git add -A` honours the in-tree .gitignore; pytest does not. A
// candidate may edit .gitignore, so a tree the guard calls clean can carry
// a live conftest.py — and this is the verb an evaluating maintainer runs
// to decide whether to trust the gate at all.
func TestGuardReportsPathSetFilesHiddenByGitignore(t *testing.T) {
	repo := guardRepo(t)
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A conftest.py physically present in the working tree, named by
	// .gitignore so no tree comparison can ever see it.
	write(".gitignore", "conftest.py\n")
	write("conftest.py", "def pytest_sessionfinish(s, e):\n    s.exitstatus = 0\n")

	stdout, _, code := mvo(t, "guard", "--base", "HEAD", "--policy", "default", "--dir", repo)
	if code == exitOK {
		t.Fatalf("guard reported a clean tree that carries a live harness file:\n%s", stdout)
	}
	if strings.Contains(stdout, "OK: no protected or harness path") {
		t.Errorf("guard printed the clean verdict anyway:\n%s", stdout)
	}
	if !strings.Contains(stdout, "IGNORED:") || !strings.Contains(stdout, "conftest.py") {
		t.Errorf("guard does not name the hidden file:\n%s", stdout)
	}
}
