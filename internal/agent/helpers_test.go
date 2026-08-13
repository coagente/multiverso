package agent

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fixtureDir returns testdata/fakeagent at the repo root. The fake `claude`
// and `codex` executables there are the ONLY agent CLIs any test spawns.
func fixtureDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fakeagent"))
	if err != nil {
		t.Fatalf("resolve fixture dir: %v", err)
	}
	for _, bin := range []string{"claude", "codex"} {
		if _, err := os.Stat(filepath.Join(abs, bin)); err != nil {
			t.Fatalf("fixture missing: %v", err)
		}
	}
	return abs
}

// usePATHFixtures prepends the fixture dir to PATH so `claude`/`codex`
// resolve to the fakes, never to a real CLI — and FAILS CLOSED: if either
// fixture would not win the LookPath (missing file, lost executable bit
// after a mode-stripping transport), the test dies here, before any spawn
// could fall through to a real, money-spending CLI (design bar: "No test
// ever invokes a real agent CLI").
func usePATHFixtures(t *testing.T) {
	t.Helper()
	dir := fixtureDir(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, bin := range []string{"claude", "codex"} {
		got, err := exec.LookPath(bin)
		if err != nil {
			t.Fatalf("fail closed: %q does not resolve after the fixture PATH override (lost executable bit?): %v", bin, err)
		}
		abs, err := filepath.Abs(got)
		if err != nil || abs != filepath.Join(dir, bin) {
			t.Fatalf("fail closed: %q resolves to %q, not the fixture in %s — refusing to risk a real CLI", bin, got, dir)
		}
	}
}

// startFixture starts an adapter run against the fake CLI in the given
// mode. The mode reaches the fixture through the env allowlist (decision
// 14): FAKE_AGENT_MODE is set on the parent env and allowlisted in the
// spec.
func startFixture(t *testing.T, ctx context.Context, adapter, mode string, spec RunSpec) Run {
	t.Helper()
	usePATHFixtures(t)
	if mode != "" {
		t.Setenv("FAKE_AGENT_MODE", mode)
		spec.Env = append(spec.Env, "FAKE_AGENT_MODE")
	}
	if spec.WorldDir == "" {
		spec.WorldDir = t.TempDir()
	}
	a, err := New(adapter)
	if err != nil {
		t.Fatalf("New(%s): %v", adapter, err)
	}
	h, err := a.Start(ctx, spec)
	if err != nil {
		t.Fatalf("Start(%s, %s): %v", adapter, mode, err)
	}
	return h
}

// waitFixture runs a fixture mode to completion and returns the result.
func waitFixture(t *testing.T, adapter, mode string, spec RunSpec) *RunResult {
	t.Helper()
	h := startFixture(t, context.Background(), adapter, mode, spec)
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait(%s, %s): %v", adapter, mode, err)
	}
	return res
}

// fixtureStdout runs the fixture binary directly and returns its raw
// stdout — the byte-exact transcript the adapter must have captured.
func fixtureStdout(t *testing.T, bin, mode string) []byte {
	t.Helper()
	cmd := exec.Command(filepath.Join(fixtureDir(t), bin))
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "FAKE_AGENT_MODE="+mode)
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run() // non-zero exits are part of the fixture contract
	return out.Bytes()
}

// shrinkKillGrace makes the TERM→KILL escalation fast for watchdog tests.
func shrinkKillGrace(t *testing.T, d time.Duration) {
	t.Helper()
	old := setKillGrace(d)
	t.Cleanup(func() { setKillGrace(old) })
}

// assertProcessGone polls until pid no longer exists (the whole process
// group died, sleeping child included) or fails after ~3s.
func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if err == syscall.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("process %d still alive after group kill", pid)
}

// childPID reads the fake_child.pid the slow fixture modes leave in the
// world dir, waiting briefly for the fixture to write it.
func childPID(t *testing.T, worldDir string) int {
	t.Helper()
	path := filepath.Join(worldDir, "fake_child.pid")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil && len(bytes.TrimSpace(b)) > 0 {
			var pid int
			for _, c := range bytes.TrimSpace(b) {
				pid = pid*10 + int(c-'0')
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fixture never wrote %s", path)
	return 0
}

// awaitEventKind consumes Events() until an event of the wanted kind
// arrives (or the channel closes / times out).
func awaitEventKind(t *testing.T, h Run, kind string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-h.Events():
			if !ok {
				t.Fatalf("events closed before %q arrived", kind)
			}
			if ev.Kind == kind {
				return
			}
		case <-timeout:
			t.Fatalf("no %q event within timeout", kind)
		}
	}
}

// git runs git with a hermetic identity for repo-backed tests.
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

// initRepo creates a temp git repo whose x.txt says "broken".
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}
