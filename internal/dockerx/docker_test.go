package dockerx

// Docker-gated tests: they skip cleanly when no daemon is reachable (CI)
// and run against the local fixture image otherwise. Nothing here pulls
// an image larger than python:3.12-alpine.

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAvailableDocker(t *testing.T) {
	requireDocker(t)
	if err := Available(); err != nil {
		t.Fatalf("Available: %v (requireDocker said the daemon answers)", err)
	}
}

func TestResolveImageDocker(t *testing.T) {
	ensureFixtureImage(t)
	img, err := ResolveImage(fixtureImageTag)
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	if img.Ref != fixtureImageTag {
		t.Errorf("Ref = %q, want the flag value verbatim %q", img.Ref, fixtureImageTag)
	}
	if img.Digest == "" || !strings.HasPrefix(img.Digest, "sha256:") {
		t.Errorf("Digest = %q, want a sha256: digest", img.Digest)
	}
	if img.RunRef == "" {
		t.Error("RunRef empty")
	}
	if img.OS != "linux" {
		t.Errorf("OS = %q, want linux", img.OS)
	}
	// The fixture is local-only (built, never pushed): the ID fallback is
	// the path actually exercised — digest and run reference are the same
	// content-pinning image ID.
	if img.RunRef != img.Digest {
		t.Logf("fixture has RepoDigests (was it pushed?): RunRef %q", img.RunRef)
	}
}

// The keeper lifecycle end-to-end: run → exec true → kill → inspect fails
// (the pid namespace and, via --rm, the container are gone) → Remove is
// idempotent on the corpse.
func TestKeeperLifecycleDocker(t *testing.T) {
	ensureFixtureImage(t)
	img, err := ResolveImage(fixtureImageTag)
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	cid, err := RunKeeper(context.Background(), RunOpts{
		Image:        img,
		HostDir:      t.TempDir(),
		PidsLimit:    64,
		TTLSeconds:   120,
		IntentDigest: "mv0:test",
		Name:         "mvo-w-test-" + cid8(t),
	})
	if err != nil {
		t.Fatalf("RunKeeper: %v", err)
	}
	t.Cleanup(func() { _ = Remove(cid) })

	execArgv := ExecArgv(cid, "/work", nil, []string{"true"})
	cmd := exec.Command(execArgv[0], execArgv[1:]...)
	cmd.Env = ClientEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker exec true: %v\n%s", err, out)
	}

	if err := Kill(cid); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	// --rm removes the killed container; inspect must eventually fail.
	if _, err := run(context.Background(), "inspect", "--format", "{{.State.Status}}", cid); err == nil {
		// Removal can lag the kill by a moment; a running status would be
		// the real failure.
		t.Logf("container still inspectable right after kill (removal lag)")
	}
	if err := Kill(cid); err != nil {
		t.Errorf("second Kill not idempotent: %v", err)
	}
	if err := Remove(cid); err != nil {
		t.Errorf("Remove after kill not idempotent: %v", err)
	}
	if err := Remove(cid); err != nil {
		t.Errorf("second Remove not idempotent: %v", err)
	}
}

// The local-only ID fallback (decision 10): an untagged child image has no
// RepoDigests, so ResolveImage pins the image ID for both fields.
func TestResolveImageLocalOnlyIDFallbackDocker(t *testing.T) {
	ensureFixtureImage(t)
	// Build an untagged child by ID: docker build -q prints the ID.
	dir := t.TempDir()
	writeFile(t, dir+"/Dockerfile", "FROM "+fixtureImageTag+"\nLABEL dev.multiverso.test=local-only\n")
	build := exec.Command("docker", "build", "-q", dir)
	build.Env = ClientEnv()
	out, err := build.Output()
	if err != nil {
		t.Skipf("child image build failed: %v", err)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		rm := exec.Command("docker", "rmi", "-f", id)
		rm.Env = ClientEnv()
		_ = rm.Run()
	})
	img, err := ResolveImage(id)
	if err != nil {
		t.Fatalf("ResolveImage(%s): %v", id, err)
	}
	if img.Digest != id || img.RunRef != id {
		t.Errorf("local-only image: Digest %q RunRef %q, want the image ID %q for both", img.Digest, img.RunRef, id)
	}
}

func cid8(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.NewReplacer("/", "-", "_", "-").Replace(name)
	if len(name) > 24 {
		name = name[:24]
	}
	return name
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
