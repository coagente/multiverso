package dockerx

// The shared docker test guard (M1c "Fixture image & docker test guard").
// Packages that dockerx must not import back (backend, oracle, agent,
// race) carry small copies of these helpers — test helpers in _test.go
// files are not importable, and a production-code testing dependency is
// worse than ~40 duplicated lines.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// fixtureImageTag is the local-only T1 fixture image: python:3.12-alpine +
// bash + pytest + the fake agent fixtures baked into /usr/local/bin. Bump
// the version when testdata/t1image/Dockerfile or the fixtures change.
const fixtureImageTag = "multiverso-t1-fixture:v1"

// requireDocker skips the test when the docker daemon is unreachable (CI
// has no docker) or MVO_SKIP_DOCKER_TESTS=1. Skip messages name the reason.
func requireDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("MVO_SKIP_DOCKER_TESTS") == "1" {
		t.Skip("MVO_SKIP_DOCKER_TESTS=1")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI not on PATH: %v", err)
	}
	cmd := exec.Command("docker", "version", "--format", "{{.Server.Version}}")
	cmd.Env = ClientEnv()
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Skipf("docker version failed to start: %v", err)
	}
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

// ensureFixtureImage inspects the fixture image, building it once per
// machine on miss (the build needs network — offline machines degrade
// gracefully to a skip with the reason).
func ensureFixtureImage(t *testing.T) {
	t.Helper()
	requireDocker(t)
	inspect := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", fixtureImageTag)
	inspect.Env = ClientEnv()
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
	build.Env = ClientEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("fixture image build failed (offline?): %v\n%s", err, out)
	}
}
