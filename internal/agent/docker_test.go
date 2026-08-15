package agent

// Docker-gated agent test (M1c): the claude-code adapter drives the fake
// agent BAKED INTO the fixture image inside a T1 container, while diff and
// transcript capture stay control-plane-owned on the host. Skips cleanly
// when no daemon is reachable. Guard helpers are small copies of
// internal/dockerx's (test helpers in _test.go files are not importable).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/dockerx"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
)

const fixtureImageTag = "multiverso-t1-fixture:v1"

func requireDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("MVO_SKIP_DOCKER_TESTS") == "1" {
		t.Skip("MVO_SKIP_DOCKER_TESTS=1")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI not on PATH: %v", err)
	}
	cmd := exec.Command("docker", "version", "--format", "{{.Server.Version}}")
	cmd.Env = dockerx.ClientEnv()
	if err := cmd.Start(); err != nil {
		t.Skipf("docker version failed to start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Skipf("docker daemon unreachable: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Skip("docker daemon probe timed out after 5s")
	}
}

func ensureFixtureImage(t *testing.T) {
	t.Helper()
	requireDocker(t)
	inspect := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", fixtureImageTag)
	inspect.Env = dockerx.ClientEnv()
	if err := inspect.Run(); err == nil {
		return
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	build := exec.Command("docker", "build",
		"-f", filepath.Join(root, "testdata", "t1image", "Dockerfile"),
		"-t", fixtureImageTag,
		filepath.Join(root, "testdata"))
	build.Env = dockerx.ClientEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("fixture image build failed (offline?): %v\n%s", err, out)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{
		"-c", "user.name=mvo-test",
		"-c", "user.email=mvo-test@invalid",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// The fake claude runs INSIDE the container (it exists only in the image's
// /usr/local/bin, not on any PATH the host runner would search), driven
// through the T1 world's docker exec mapping; FAKE_AGENT_MODE reaches it
// via the env allowlist → -e flag. Capture stays control-plane-owned: the
// diff is real (`diff --git`), the transcript non-empty, the cost honest
// (usd_micro == 4200).
func TestClaudeCodeInT1ContainerDocker(t *testing.T) {
	ensureFixtureImage(t)
	img, err := dockerx.ResolveImage(fixtureImageTag)
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}

	// A world worktree: a real git repo so control-plane capture works.
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "base")
	_, baseTree, err := gitx.Head(dir)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}

	be, err := backend.New(backend.Config{
		Tier:         object.TierT1Container,
		Image:        img,
		KeeperTTL:    5 * time.Minute,
		IntentDigest: "mv0:test",
	})
	if err != nil {
		t.Fatalf("backend.New: %v", err)
	}
	w, err := be.Open(context.Background(), dir, backend.OpenOpts{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// The mode reaches the in-container fixture only via the allowlist.
	t.Setenv("FAKE_AGENT_MODE", "happy")
	a, err := New("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	h, err := a.Start(context.Background(), RunSpec{
		WorldDir: dir,
		World:    w,
		Prompt:   "fix it",
		Budget:   Budget{MaxWall: 60 * time.Second},
		Env:      []string{"FAKE_AGENT_MODE"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if res.Outcome != object.OutcomeCompleted {
		t.Fatalf("outcome = %q (exit %d, stderr %q), want COMPLETED",
			res.Outcome, res.ExitCode, res.Stderr)
	}
	if len(res.Transcript) == 0 {
		t.Error("transcript empty; want the fixture's stream-json bytes")
	}
	if !strings.Contains(string(res.Transcript), `"type"`) {
		t.Errorf("transcript carries no events: %q", res.Transcript)
	}
	if res.Cost.USDMicro != 4200 {
		t.Errorf("usd_micro = %d, want 4200 (the fixture's total_cost_usd 0.0042)", res.Cost.USDMicro)
	}
	if res.Cost.Source != CostSourceClientEstimate {
		t.Errorf("cost source = %q, want client-estimate", res.Cost.Source)
	}

	// The agent demonstrably ran inside the container writing to /work,
	// and control-plane diff capture sees it on the host (AG-4: the bind
	// mount is one mutable state).
	if _, err := os.Stat(filepath.Join(dir, "AGENT_TOUCH.txt")); err != nil {
		t.Fatalf("AGENT_TOUCH.txt missing on the host side: %v", err)
	}
	patch, err := Diff(dir, baseTree)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(string(patch), "diff --git") {
		t.Errorf("captured patch is not a real diff: %q", patch)
	}
	if !strings.Contains(string(patch), "AGENT_TOUCH.txt") {
		t.Errorf("captured patch misses the agent's file: %q", patch)
	}
}
