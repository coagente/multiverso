package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeagentPATH prepends testdata/fakeagent so `claude` resolves to the
// fake fixture — cmd tests never spawn a real agent CLI. It FAILS CLOSED:
// if a fixture would not win the LookPath (missing file, lost executable
// bit), the test dies before any spawn could fall through to a real,
// money-spending CLI.
func fakeagentPATH(t *testing.T) {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fakeagent"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", abs+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, bin := range []string{"claude", "codex"} {
		got, err := exec.LookPath(bin)
		if err != nil {
			t.Fatalf("fail closed: %q does not resolve after the fixture PATH override: %v", bin, err)
		}
		resolved, err := filepath.Abs(got)
		if err != nil || resolved != filepath.Join(abs, bin) {
			t.Fatalf("fail closed: %q resolves to %q, not the fixture in %s — refusing to risk a real CLI", bin, got, abs)
		}
	}
}

// Flag discipline (design "CLI"): misuse is a usage error, exit 2.
func TestRaceFlagMatrix(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"patches with agent adapter",
			[]string{"race", "mv0:x", "--agent", "claude-code", "--patches", "p", "--oracle-cmd", "true"},
			"--patches applies only to --agent script"},
		{"agent flag with script",
			[]string{"race", "mv0:x", "--patches", "p", "--model", "m", "--oracle-cmd", "true"},
			"--model requires an agent adapter"},
		{"prompt flag with script",
			[]string{"race", "mv0:x", "--patches", "p", "--prompt", "do", "--oracle-cmd", "true"},
			"--prompt requires an agent adapter"},
		{"max-wall-ms with script",
			[]string{"race", "mv0:x", "--patches", "p", "--max-wall-ms", "5", "--oracle-cmd", "true"},
			"--max-wall-ms requires an agent adapter"},
		{"both prompt and prompt-file",
			[]string{"race", "mv0:x", "--agent", "codex", "--prompt", "a", "--prompt-file", "b", "--oracle-cmd", "true"},
			"mutually exclusive"},
		{"malformed max-usd",
			[]string{"race", "mv0:x", "--agent", "claude-code", "--max-usd", "1.2345678", "--oracle-cmd", "true"},
			"--max-usd"},
		{"unknown adapter",
			[]string{"race", "mv0:x", "--agent", "aider", "--oracle-cmd", "true"},
			"unknown adapter"},
		{"missing patches for script",
			[]string{"race", "mv0:x", "--oracle-cmd", "true"},
			"--patches is required"},
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

// Budget-dependent usage errors need a recorded intent (CP-2 checks read
// the intent's budget).
func TestRaceAgentBudgetFlagErrors(t *testing.T) {
	fakeagentPATH(t)
	sc := newScenario(t) // intent budget: max_candidates 2 (default)

	_, stderr, code := mvo(t, "race", sc.intentDig, "--dir", sc.repo,
		"--agent", "claude-code", "--candidates", "3")
	if code != exitUsage || !strings.Contains(stderr, "max_candidates") {
		t.Errorf("candidates over budget: exit %d, stderr %q; want usage error naming max_candidates", code, stderr)
	}

	_, stderr, code = mvo(t, "race", sc.intentDig, "--dir", sc.repo,
		"--agent", "claude-code", "--max-wall-ms", "0")
	if code != exitUsage || !strings.Contains(stderr, "--max-wall-ms must be positive") {
		t.Errorf("wall 0: exit %d, stderr %q; want usage error (uncapped agent runs never launch implicitly)", code, stderr)
	}

	_, stderr, code = mvo(t, "race", sc.intentDig, "--dir", sc.repo,
		"--agent", "claude-code", "--candidates", "0")
	if code != exitUsage || !strings.Contains(stderr, "--candidates must be at least 1") {
		t.Errorf("candidates 0: exit %d, stderr %q; want usage error", code, stderr)
	}
}

// Pre-flight LookPath (decision 10): a missing adapter binary aborts with
// a named error before race.started — exit 1, not a usage error.
func TestRacePreflightLookPath(t *testing.T) {
	sc := newScenario(t)
	t.Setenv("PATH", t.TempDir()) // no claude anywhere

	_, stderr, code := mvo(t, "race", sc.intentDig, "--dir", sc.repo,
		"--agent", "claude-code")
	if code != exitFail {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, exitFail, stderr)
	}
	if !strings.Contains(stderr, `binary "claude" not found in PATH`) {
		t.Errorf("stderr %q does not name the missing binary", stderr)
	}
}

// End-to-end through the CLI against the fake claude: SELECT on stdout,
// costs harvested into the worlds table (NFR-5).
func TestRaceFakeClaudeAndWorldsCostColumn(t *testing.T) {
	fakeagentPATH(t)
	t.Setenv("FAKE_AGENT_MODE", "happy")
	sc := newScenario(t) // script race already recorded (worlds with usd 0)

	out := mustMvo(t, "race", sc.intentDig, "--dir", sc.repo,
		"--agent", "claude-code", "--candidates", "2",
		"--model", "fake-model", "--max-usd", "0.25", "--max-turns", "8",
		"--max-wall-ms", "60000", "--agent-env", "FAKE_AGENT_MODE")
	if !strings.HasPrefix(out, "SELECT mv0:") {
		t.Fatalf("race output = %q, want SELECT", out)
	}

	worlds := mustMvo(t, "worlds", sc.intentDig, "--dir", sc.repo)
	lines := strings.Split(strings.TrimSpace(worlds), "\n")
	if !strings.Contains(lines[0], "USD_MICRO") {
		t.Fatalf("worlds header %q missing USD_MICRO column", lines[0])
	}
	// M1c: the TIER column reports each world's recorded isolation tier.
	if !strings.Contains(lines[0], "TIER") {
		t.Fatalf("worlds header %q missing TIER column", lines[0])
	}
	// M1d: the table is followed by the trunk-drift freshness line.
	if !strings.HasPrefix(lines[len(lines)-1], "freshness: ") {
		t.Fatalf("worlds output does not end with the freshness line:\n%s", worlds)
	}
	agentRows := 0
	for _, line := range lines[1 : len(lines)-1] {
		fields := strings.Fields(line)
		if len(fields) != 6 {
			t.Fatalf("worlds row %q has %d columns, want 6", line, len(fields))
		}
		if fields[4] == "4200" {
			agentRows++
		}
		if fields[5] != "T0-worktree" {
			t.Errorf("worlds row %q TIER = %q, want T0-worktree", line, fields[5])
		}
	}
	if agentRows != 2 {
		t.Errorf("worlds shows %d rows with usd_micro 4200, want 2 (fake agent worlds):\n%s", agentRows, worlds)
	}
}

// Decision 18, at the only place it can be enforced: the intent's PINNED
// policy decides whether --oracle-cmd is required or refused. A v0 policy
// names a gate but not the command that decides it, so the flag is
// mandatory; a v1 policy names its own oracles, so the flag is a usage
// error rather than a per-machine override of an attested artifact.
func TestRaceOracleCmdDisciplineFollowsThePinnedPolicy(t *testing.T) {
	repo := t.TempDir()
	gitCLI(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "commit", "-q", "-m", "baseline")
	mustMvo(t, "init", "--dir", repo)
	patches := t.TempDir()
	if err := os.WriteFile(filepath.Join(patches, "patch-a.patch"), []byte(fixPatch), 0o644); err != nil {
		t.Fatal(err)
	}

	// v1 (the workspace default): the flag is refused, by name and schema.
	v1Intent := strings.TrimSpace(mustMvo(t, "intent", "new", "--dir", repo, "--title", "v1"))
	_, stderr, code := mvo(t, "race", v1Intent, "--dir", repo, "--patches", patches, "--oracle-cmd", "true")
	if code != exitUsage || !strings.Contains(stderr, "--oracle-cmd is not permitted with policy") ||
		!strings.Contains(stderr, "policy/v1") {
		t.Errorf("v1 + --oracle-cmd: exit %d, stderr %q", code, stderr)
	}

	// v0 (pinned deliberately, per intent): the flag is required.
	fixturePolicy(t, repo, "legacy-v0")
	v0Intent := strings.TrimSpace(mustMvo(t, "intent", "new", "--dir", repo, "--title", "v0", "--policy", "legacy-v0"))
	_, stderr, code = mvo(t, "race", v0Intent, "--dir", repo, "--patches", patches)
	if code != exitUsage || !strings.Contains(stderr, "--oracle-cmd is required with policy") {
		t.Errorf("v0 without --oracle-cmd: exit %d, stderr %q", code, stderr)
	}
	// And with it, the v0 race runs exactly as M0–M1d did.
	out := mustMvo(t, "race", v0Intent, "--dir", repo, "--patches", patches, "--oracle-cmd", "true")
	if !strings.HasPrefix(out, "SELECT mv0:") {
		t.Errorf("v0 race output = %q, want SELECT", out)
	}

	// --policy and --oracle-cmd pin different policies: naming both is a
	// usage error, never a silent precedence rule.
	if _, _, code := mvo(t, "intent", "new", "--dir", repo, "--title", "both",
		"--policy", "legacy-v0", "--oracle-cmd", "true"); code != exitUsage {
		t.Errorf("--policy + --oracle-cmd: exit %d, want %d", code, exitUsage)
	}
}
