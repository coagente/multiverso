package oracle

// Docker-gated oracle tests: skip cleanly when no daemon is reachable.
// Guard helpers are small copies of internal/dockerx's (see the M1c
// test-guard note). No test requires real egress — the NFR-4 proof is a
// probe that PASSES when the connect fails.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/dockerx"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
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

// openT1World provisions one T1 world over a temp dir for oracle tests.
func openT1World(t *testing.T) backend.World {
	t.Helper()
	ensureFixtureImage(t)
	img, err := dockerx.ResolveImage(fixtureImageTag)
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	b, err := backend.New(backend.Config{
		Tier:         object.TierT1Container,
		Image:        img,
		KeeperTTL:    5 * time.Minute,
		IntentDigest: "mv0:test",
	})
	if err != nil {
		t.Fatalf("backend.New: %v", err)
	}
	w, err := b.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

// The command oracle runs inside the container: pass status, T1 tier and
// caps recorded from the world handle, in-world argv as the evidence.
func TestRunInContainerDocker(t *testing.T) {
	w := openT1World(t)
	o := newOracle(t, []string{"python3", "-c", "print('in-world')"}, 30*time.Second)
	rec, err := o.Run(context.Background(), w)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusPass {
		t.Errorf("status = %q, want pass", rec.Result.Status)
	}
	if rec.Execution.IsolationTier != object.TierT1Container {
		t.Errorf("isolation_tier = %q, want %q", rec.Execution.IsolationTier, object.TierT1Container)
	}
	if rec.Execution.IsolationCaps != w.Caps() {
		t.Errorf("isolation_caps = %+v, want the world's %+v", rec.Execution.IsolationCaps, w.Caps())
	}
	// The receipt records the IN-WORLD argv (decision 12), not the docker
	// exec transport.
	if len(rec.Execution.Argv) == 0 || rec.Execution.Argv[0] != "python3" {
		t.Errorf("argv = %q, want the in-world command", rec.Execution.Argv)
	}
	out, err := o.CAS.Get(rec.Result.Artifacts[0])
	if err != nil || string(out) != "in-world\n" {
		t.Errorf("stdout artifact = %q, err %v; want %q", out, err, "in-world\n")
	}
}

// Oracle timeout against an in-container sleep 60: receipt status "error"
// AND the container is gone — the Kill mapping proven (docker kill tears
// down the pid namespace; signaling the docker exec client alone would
// kill nothing inside).
func TestRunTimeoutKillsContainerDocker(t *testing.T) {
	w := openT1World(t)
	o := newOracle(t, []string{"sleep", "60"}, 500*time.Millisecond)
	start := time.Now()
	rec, err := o.Run(context.Background(), w)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusError {
		t.Errorf("status = %q, want error on timeout", rec.Result.Status)
	}
	if elapsed > 10*time.Second {
		t.Errorf("Run took %v; the in-container process was not killed promptly", elapsed)
	}
	// The keeper was killed (--rm removes it): a fresh exec must fail.
	hostArgv, hostEnv := w.Command([]string{"true"}, nil)
	probe := exec.Command(hostArgv[0], hostArgv[1:]...)
	probe.Env = hostEnv
	if out, err := probe.CombinedOutput(); err == nil {
		t.Errorf("container survived the oracle timeout kill: %s", out)
	}
}

// The Python ladder inside a T1 container: the SAME relative argv a T0
// receipt carries, native artifacts arriving through the bind mount, and
// honest degradation — the fixture image ships pytest and nothing else, so
// no coverage or plugin metric may appear however loudly the spec asks.
func TestPytestLadderInContainerDocker(t *testing.T) {
	w := openT1World(t)
	for _, name := range []string{"stats.py", "test_stats.py"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "toyrepo", name))
		if err != nil {
			t.Fatalf("read toyrepo %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(w.Dir(), name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	store := newStore(t)
	cfg := "mv0:" + strings.Repeat("7", 64)

	collect, err := New(Params{
		Spec: policy.Oracle{Kind: KindPytestCollect, Config: cfg}, CAS: store,
		Timeout: 2 * time.Minute, Baseline: 8,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec, err := collect.Run(context.Background(), w)
	if err != nil {
		t.Fatalf("collect Run: %v", err)
	}
	if rec.Result.Status != StatusPass || rec.Result.Metrics[MetricCollectedTotal] != 8 {
		t.Errorf("collect: status %q metrics %v, want pass with collected_total 8",
			rec.Result.Status, rec.Result.Metrics)
	}
	if rec.Result.Metrics[MetricCollectedDelta] != 0 {
		t.Errorf("collected_delta = %d, want 0", rec.Result.Metrics[MetricCollectedDelta])
	}
	if rec.Execution.IsolationTier != object.TierT1Container {
		t.Errorf("isolation_tier = %q, want %q", rec.Execution.IsolationTier, object.TierT1Container)
	}

	suite, err := New(Params{
		Spec: policy.Oracle{Kind: KindPytestSuite, Config: cfg, Coverage: true, Reruns: 2},
		CAS:  store, Timeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec, err = suite.Run(context.Background(), w)
	if err != nil {
		t.Fatalf("suite Run: %v", err)
	}
	// The evidence-producing command is tier-independent: the identical
	// relative argv appears in a T0 and a T1 receipt (M1c decision 12).
	wantArgv := []string{
		"python3", "-m", "pytest",
		"--junit-xml=.mvo-oracle/pytest-suite/junit.xml", "-p", "no:cacheprovider",
	}
	if !reflect.DeepEqual(rec.Execution.Argv, wantArgv) {
		t.Errorf("argv = %v, want %v (no flag for a plugin the image lacks)", rec.Execution.Argv, wantArgv)
	}
	if got := rec.Result.Metrics[MetricTestsTotal]; got != 8 {
		t.Errorf("tests_total = %d, want 8; metrics %v", got, rec.Result.Metrics)
	}
	for _, absent := range []string{MetricCoverageBP, MetricTestsFailedFirstRun} {
		if _, ok := rec.Result.Metrics[absent]; ok {
			t.Errorf("%s present although the image ships no such source", absent)
		}
	}
	if len(rec.Result.Tools) != 1 || rec.Result.Tools[ToolPytest] == "" {
		t.Errorf("tools = %v, want only pytest — the record of what was really there", rec.Result.Tools)
	}
	if len(rec.Result.Artifacts) != 4 {
		t.Errorf("artifacts = %v, want 4 (stdout, stderr, probe, junit through the bind mount)",
			rec.Result.Artifacts)
	}
}

// NFR-4 egress proof: the probe exits 0 iff a connect to 1.1.1.1:443 (3 s
// timeout) FAILS, so the passing receipt is the recorded proof of no
// egress; the same receipt's caps record network "none".
func TestRunNoEgressProofDocker(t *testing.T) {
	w := openT1World(t)
	probe := "import socket,sys\n" +
		"s=socket.socket(); s.settimeout(3)\n" +
		"try:\n s.connect(('1.1.1.1',443))\nexcept OSError:\n sys.exit(0)\n" +
		"sys.exit(1)"
	o := newOracle(t, []string{"python3", "-c", probe}, 30*time.Second)
	rec, err := o.Run(context.Background(), w)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusPass {
		t.Fatalf("status = %q, want pass — the container reached the network (NFR-4 violated)", rec.Result.Status)
	}
	if rec.Execution.IsolationCaps.Network != object.NetworkNone {
		t.Errorf("caps network = %q, want %q", rec.Execution.IsolationCaps.Network, object.NetworkNone)
	}
}
