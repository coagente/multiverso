package main

// M1c CLI tests: the exec/parallel flag matrix and the T1-without-docker
// pre-flight. No docker daemon is required here — the pre-flight test
// deliberately pins PATH to an empty dir so it is deterministic on
// machines that HAVE docker.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/workspace"
)

// writeTestPatch drops one patch file into dir for script candidates.
func writeTestPatch(dir string) error {
	return os.WriteFile(filepath.Join(dir, "patch-a.patch"), []byte(fixPatch), 0o644)
}

// Flag discipline (M1c "CLI"): misuse is a usage error, exit 2.
func TestRaceExecFlagMatrix(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"exec other than T0/T1",
			[]string{"race", "mv0:x", "--patches", "p", "--exec", "T2", "--oracle-cmd", "true"},
			"--exec must be T0 or T1"},
		{"exec-image without exec T1",
			[]string{"race", "mv0:x", "--patches", "p", "--exec-image", "img", "--oracle-cmd", "true"},
			"--exec-image requires --exec T1"},
		{"memory-mb without exec T1",
			[]string{"race", "mv0:x", "--patches", "p", "--memory-mb", "512", "--oracle-cmd", "true"},
			"--memory-mb requires --exec T1"},
		{"cpus without exec T1",
			[]string{"race", "mv0:x", "--patches", "p", "--cpus", "1", "--oracle-cmd", "true"},
			"--cpus requires --exec T1"},
		{"pids without exec T1",
			[]string{"race", "mv0:x", "--patches", "p", "--pids", "16", "--oracle-cmd", "true"},
			"--pids requires --exec T1"},
		{"allow-network without exec T1",
			[]string{"race", "mv0:x", "--patches", "p", "--allow-network", "--oracle-cmd", "true"},
			"--allow-network requires --exec T1"},
		{"exec T1 without exec-image",
			[]string{"race", "mv0:x", "--patches", "p", "--exec", "T1", "--oracle-cmd", "true"},
			"--exec T1 requires --exec-image"},
		{"parallel below 1",
			[]string{"race", "mv0:x", "--patches", "p", "--parallel", "0", "--oracle-cmd", "true"},
			"--parallel must be at least 1"},
		{"memory-mb below the docker floor",
			[]string{"race", "mv0:x", "--patches", "p", "--exec", "T1", "--exec-image", "img", "--memory-mb", "5", "--oracle-cmd", "true"},
			"--memory-mb must be at least 6"},
		{"malformed cpus",
			[]string{"race", "mv0:x", "--patches", "p", "--exec", "T1", "--exec-image", "img", "--cpus", "1.2345", "--oracle-cmd", "true"},
			"--cpus"},
		{"pids below 1",
			[]string{"race", "mv0:x", "--patches", "p", "--exec", "T1", "--exec-image", "img", "--pids", "0", "--oracle-cmd", "true"},
			"--pids must be at least 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := mvo(t, tt.args...)
			if code != exitUsage {
				t.Fatalf("exit = %d, want %d (usage)\nstderr: %s", code, exitUsage, stderr)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr %q missing %q", stderr, tt.wantErr)
			}
		})
	}
}

// countEventsIn returns the ledger event count for a repo.
func countEventsIn(t *testing.T, repo string) int {
	t.Helper()
	ws, err := workspace.Open(repo)
	if err != nil {
		t.Fatalf("workspace.Open: %v", err)
	}
	defer ws.Close()
	n := 0
	if err := ws.Ledger.Scan(func(ledger.Event) error { n++; return nil }); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return n
}

// T1 pre-flight without docker (M1c decision 18): a clean machinery error
// naming docker, exit 1, and ZERO new race events in the ledger. PATH is
// pinned to an empty dir so the test is deterministic on machines that
// have docker.
func TestRaceExecT1WithoutDockerPreflight(t *testing.T) {
	sc := newScenario(t) // records init + intent + one script race
	patches := t.TempDir()
	if err := writeTestPatch(patches); err != nil {
		t.Fatal(err)
	}
	before := countEventsIn(t, sc.repo)

	t.Setenv("PATH", t.TempDir()) // no docker (and nothing else) on PATH

	_, stderr, code := mvo(t, "race", sc.intentDig, "--dir", sc.repo,
		"--agent", "script", "--patches", patches,
		"--exec", "T1", "--exec-image", "multiverso-t1-fixture:v1",
		"--oracle-cmd", "true")
	if code != exitFail {
		t.Fatalf("exit = %d, want %d (machinery failure)\nstderr: %s", code, exitFail, stderr)
	}
	if !strings.Contains(stderr, "docker daemon unavailable") {
		t.Errorf("stderr %q does not name docker", stderr)
	}

	if after := countEventsIn(t, sc.repo); after != before {
		t.Errorf("pre-flight failure appended %d ledger events; want 0 (aborts before race.started)", after-before)
	}
}
