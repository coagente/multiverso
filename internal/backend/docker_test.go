package backend

// Docker-gated backend tests: skip cleanly when no daemon is reachable.
// The guard helpers are small copies of internal/dockerx's (test helpers
// in _test.go files are not importable; see the M1c test-guard note).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/dockerx"
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

// dockerInspect returns one --format field of a container.
func dockerInspect(t *testing.T, cid, format string) (string, error) {
	t.Helper()
	cmd := exec.Command("docker", "inspect", "--format", format, cid)
	cmd.Env = dockerx.ClientEnv()
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// execInWorld runs argv inside the world via its own Command mapping.
func execInWorld(t *testing.T, w World, argv []string) (string, error) {
	t.Helper()
	hostArgv, hostEnv := w.Command(argv, nil)
	cmd := exec.Command(hostArgv[0], hostArgv[1:]...)
	cmd.Env = hostEnv
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The T1 world lifecycle against the fixture image: Open provisions a
// keeper whose docker-visible state matches the recorded caps, Command
// executes inside it, the read-only posture holds (decision 7), and
// Kill/Close are idempotent teardown.
func TestT1WorldLifecycleDocker(t *testing.T) {
	ensureFixtureImage(t)
	img, err := dockerx.ResolveImage(fixtureImageTag)
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	b, err := New(Config{
		Tier:         object.TierT1Container,
		Image:        img,
		CPUMilli:     1000,
		MemoryMB:     256,
		PidsLimit:    128,
		KeeperTTL:    5 * time.Minute,
		IntentDigest: "mv0:test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dir := t.TempDir()
	w, err := b.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	t1, ok := w.(*t1World)
	if !ok {
		t.Fatalf("Open returned %T, want *t1World", w)
	}

	// Recorded caps match the config (XP-2: auditable enforcement).
	wantCaps := object.IsolationCaps{
		CapDrop: "ALL", CPUMilli: 1000, MemoryBytes: 256 << 20,
		Network: object.NetworkNone, PidsLimit: 128, ReadOnlyRoot: true,
		User: t1.caps.User, // uid:gid of this process (image has no user)
	}
	if w.Caps() != wantCaps {
		t.Errorf("Caps = %+v, want %+v", w.Caps(), wantCaps)
	}
	if w.Caps().User == "" {
		t.Error("effective user not recorded")
	}

	// docker inspect shows the enforcement: memory, pids, cap-drop,
	// network none, read-only root.
	checks := map[string]string{
		"{{.HostConfig.Memory}}":          "268435456",
		"{{.HostConfig.MemorySwap}}":      "268435456",
		"{{.HostConfig.PidsLimit}}":       "128",
		"{{.HostConfig.NanoCpus}}":        "1000000000",
		"{{.HostConfig.NetworkMode}}":     "none",
		"{{.HostConfig.ReadonlyRootfs}}":  "true",
		"{{index .HostConfig.CapDrop 0}}": "ALL",
	}
	for format, want := range checks {
		got, err := dockerInspect(t, t1.cid, format)
		if err != nil {
			t.Fatalf("inspect %s: %v", format, err)
		}
		if got != want {
			t.Errorf("inspect %s = %q, want %q", format, got, want)
		}
	}

	// Command executes in the container, cwd /work = the bind-mounted
	// worktree.
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("host wrote this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := execInWorld(t, w, []string{"cat", "marker.txt"})
	if err != nil {
		t.Fatalf("in-world cat: %v\n%s", err, out)
	}
	if !strings.Contains(out, "host wrote this") {
		t.Errorf("in-world cat = %q, want the host-written bytes", out)
	}

	// Read-only proven in-world: /etc is immutable, /tmp and /work are the
	// only writable surfaces.
	if out, err := execInWorld(t, w, []string{"touch", "/etc/x"}); err == nil {
		t.Errorf("touch /etc/x succeeded in a read-only root: %s", out)
	}
	if out, err := execInWorld(t, w, []string{"touch", "/tmp/x"}); err != nil {
		t.Errorf("touch /tmp/x failed: %v\n%s", err, out)
	}
	if out, err := execInWorld(t, w, []string{"touch", "/work/x"}); err != nil {
		t.Errorf("touch /work/x failed: %v\n%s", err, out)
	}
	// The in-world write landed in the host worktree (one mutable state).
	if _, err := os.Stat(filepath.Join(dir, "x")); err != nil {
		t.Errorf("in-world /work/x not visible on the host: %v", err)
	}

	// HOME=/tmp is pinned on every exec (decision 7).
	out, err = execInWorld(t, w, []string{"sh", "-c", "echo $HOME"})
	if err != nil {
		t.Fatalf("echo $HOME: %v", err)
	}
	if strings.TrimSpace(out) != "/tmp" {
		t.Errorf("in-world HOME = %q, want /tmp", strings.TrimSpace(out))
	}

	// Kill tears down the pid namespace; Close removes; both idempotent.
	if err := w.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := w.Kill(); err != nil {
		t.Errorf("second Kill: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := dockerInspect(t, t1.cid, "{{.State.Status}}"); err == nil {
		t.Error("container still inspectable after Close")
	}
}
