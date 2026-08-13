package backend

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/dockerx"
	"github.com/coagente/multiverso/internal/object"
)

func testStore(t *testing.T) *cas.Store {
	t.Helper()
	store, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	return store
}

func testImage() dockerx.Image {
	return dockerx.Image{
		Ref:    "python:3.12-alpine",
		Digest: "sha256:" + strings.Repeat("d", 64),
		RunRef: "python@sha256:" + strings.Repeat("d", 64),
		OS:     "linux",
	}
}

// New selection table: default T0; unknown tier and T1-without-image are
// errors.
func TestNewSelection(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		wantTier string
		wantErr  string
	}{
		{"default empty tier is T0", Config{}, object.TierT0Worktree, ""},
		{"explicit T0", Config{Tier: object.TierT0Worktree}, object.TierT0Worktree, ""},
		{"T1 with image", Config{Tier: object.TierT1Container, Image: testImage()}, object.TierT1Container, ""},
		{"T1 without image", Config{Tier: object.TierT1Container}, "", "digest-pinned image"},
		{"unknown tier", Config{Tier: "T2-vm"}, "", "unknown tier"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := New(tt.cfg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("New: err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if b.Tier() != tt.wantTier {
				t.Errorf("Tier = %q, want %q", b.Tier(), tt.wantTier)
			}
		})
	}
}

// HostDir: Command is the identity with nil and non-nil env, Kill and
// Close are no-ops, Caps is the honest uncapped-bare-host record.
func TestHostDir(t *testing.T) {
	dir := t.TempDir()
	w := HostDir(dir)
	if w.Tier() != object.TierT0Worktree {
		t.Errorf("Tier = %q, want %q", w.Tier(), object.TierT0Worktree)
	}
	if w.Dir() != dir {
		t.Errorf("Dir = %q, want %q", w.Dir(), dir)
	}

	argv := []string{"python3", "-m", "pytest", "-q"}
	hostArgv, hostEnv := w.Command(argv, nil)
	if !reflect.DeepEqual(hostArgv, argv) {
		t.Errorf("Command argv = %q, want identity %q", hostArgv, argv)
	}
	if hostEnv != nil {
		t.Errorf("Command(nil env) hostEnv = %v, want nil (spawner inherits ambient env)", hostEnv)
	}
	env := []string{"PATH=/x", "FAKE_AGENT_MODE=happy"}
	hostArgv, hostEnv = w.Command(argv, env)
	if !reflect.DeepEqual(hostArgv, argv) || !reflect.DeepEqual(hostEnv, env) {
		t.Errorf("Command(env) = %q, %q, want identity", hostArgv, hostEnv)
	}

	if err := w.Kill(); err != nil {
		t.Errorf("Kill: %v, want no-op nil", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v, want no-op nil", err)
	}
	if got := w.Caps(); got != object.HostCaps() {
		t.Errorf("Caps = %+v, want HostCaps %+v", got, object.HostCaps())
	}

	// T0 backend's Open result IS HostDir.
	b, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := b.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.Dir() != dir || opened.Tier() != object.TierT0Worktree {
		t.Errorf("Open = %q/%q, want the T0 world at %q", opened.Tier(), opened.Dir(), dir)
	}
}

// T0 EnvDigest is byte-identical to the M1b golden — the regression pin
// that keeps T0 world digests from moving (M1c decision 11).
func TestT0EnvDigestM1bGolden(t *testing.T) {
	store := testStore(t)

	// No lockfiles: {"go":"none","os":runtime.GOOS} exactly.
	bare := t.TempDir()
	dig, err := HostDir(bare).EnvDigest(store)
	if err != nil {
		t.Fatalf("EnvDigest: %v", err)
	}
	wantCanon := `{"go":"none","os":"` + runtime.GOOS + `"}`
	wantDig, _, err := object.Digest(map[string]any{"go": "none", "os": runtime.GOOS})
	if err != nil {
		t.Fatal(err)
	}
	if dig != wantDig {
		t.Errorf("bare manifest digest = %q, want %q", dig, wantDig)
	}
	key, err := object.CASKey(dig)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Get(key)
	if err != nil {
		t.Fatalf("manifest bytes not in CAS: %v", err)
	}
	if string(b) != wantCanon {
		t.Errorf("manifest bytes = %q, want the M1b golden %q", b, wantCanon)
	}

	// With a lockfile: the lockfiles sub-object appears with the sha256 of
	// the file bytes — same names, same hashing as M1b.
	locked := t.TempDir()
	if err := os.WriteFile(filepath.Join(locked, "requirements.txt"), []byte("requests==2.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dig2, err := HostDir(locked).EnvDigest(store)
	if err != nil {
		t.Fatalf("EnvDigest(locked): %v", err)
	}
	key2, err := object.CASKey(dig2)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := store.Get(key2)
	if err != nil {
		t.Fatal(err)
	}
	want2 := `{"go":"none","lockfiles":{"requirements.txt":"sha256:2c21963ff7f33678a77b9b352ca0107c0d7ca839a9c541bea8735565ab86e3af"},"os":"` + runtime.GOOS + `"}`
	if string(b2) != want2 {
		t.Errorf("locked manifest bytes = %q, want %q", b2, want2)
	}
}

// T1 manifest golden: canonical bytes carry the pinned image digest, the
// verbatim ref, and the IMAGE's os — and the digest differs from the T0
// digest for the same dir (XP-3: a T0 and a T1 race over the same tree
// must produce different valid_for.env values).
func TestT1EnvManifestGolden(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests==2.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	img := testImage()
	dig, err := t1EnvDigest(store, dir, img)
	if err != nil {
		t.Fatalf("t1EnvDigest: %v", err)
	}
	key, err := object.CASKey(dig)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"image_digest":"sha256:` + strings.Repeat("d", 64) + `","image_ref":"python:3.12-alpine","lockfiles":{"requirements.txt":"sha256:2c21963ff7f33678a77b9b352ca0107c0d7ca839a9c541bea8735565ab86e3af"},"os":"linux"}`
	if string(b) != want {
		t.Errorf("T1 manifest bytes = %q\nwant %q", b, want)
	}

	t0dig, err := T0EnvDigest(store, dir)
	if err != nil {
		t.Fatal(err)
	}
	if dig == t0dig {
		t.Errorf("T1 and T0 env digests are equal (%q); the tiers must be distinguishable via valid_for.env", dig)
	}

	// No lockfiles: the key is omitted entirely (same shape rule as T0).
	dig2, err := t1EnvDigest(store, t.TempDir(), img)
	if err != nil {
		t.Fatal(err)
	}
	key2, _ := object.CASKey(dig2)
	b2, err := store.Get(key2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b2), "lockfiles") {
		t.Errorf("manifest without lockfiles still carries the key: %q", b2)
	}
}

// The normative T1 in-world env mapping (decision 7): PATH/HOME/TMPDIR/
// USER filtered, HOME=/tmp injected, everything else kept as NAME-ONLY -e
// flags sorted by name — the values ride in the docker client's process
// environment (hostEnv), never on the host command line, so allowlisted
// secrets are not readable out of the process table (NFR-4).
func TestT1CommandEnvMapping(t *testing.T) {
	w := &t1World{cid: "cid123", dir: "/host/w1", image: testImage()}

	hostArgv, hostEnv := w.Command(
		[]string{"claude", "-p", "task"},
		[]string{"PATH=/usr/bin", "LANG=C.UTF-8", "HOME=/Users/op", "FAKE_AGENT_MODE=happy", "TMPDIR=/var/tmp", "USER=op", "ANTHROPIC_API_KEY=sk-x"},
	)
	want := []string{
		"docker", "exec", "-w", "/work",
		"-e", "ANTHROPIC_API_KEY",
		"-e", "FAKE_AGENT_MODE",
		"-e", "HOME=/tmp",
		"-e", "LANG",
		"cid123",
		"claude", "-p", "task",
	}
	if !reflect.DeepEqual(hostArgv, want) {
		t.Errorf("Command argv = %q\nwant %q", hostArgv, want)
	}
	// The secret VALUE must never appear on the host command line: any
	// local process can read another process's argv.
	for _, a := range hostArgv {
		if strings.Contains(a, "sk-x") {
			t.Errorf("secret value on the host argv: %q", a)
		}
	}
	// hostEnv is the docker-client allowlist plus exactly the kept
	// NAME=value pairs the name-only -e flags resolve from; nothing else
	// leaks into the docker client process.
	wantPairs := map[string]bool{
		"ANTHROPIC_API_KEY=sk-x": false,
		"FAKE_AGENT_MODE=happy":  false,
		"LANG=C.UTF-8":           false,
	}
	for _, kv := range hostEnv {
		if _, ok := wantPairs[kv]; ok {
			wantPairs[kv] = true
			continue
		}
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "PATH", "HOME", "USER", "TMPDIR",
			"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG",
			"DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "DOCKER_API_VERSION":
		default:
			t.Errorf("hostEnv leaks %q beyond the docker-client allowlist", kv)
		}
	}
	for kv, seen := range wantPairs {
		if !seen {
			t.Errorf("hostEnv missing %q (the name-only -e flag has nothing to resolve from)", kv)
		}
	}

	// nil env: the world's default environment plus the HOME=/tmp pin, and
	// hostEnv is exactly the docker-client allowlist.
	hostArgv, hostEnv = w.Command([]string{"true"}, nil)
	wantNil := []string{"docker", "exec", "-w", "/work", "-e", "HOME=/tmp", "cid123", "true"}
	if !reflect.DeepEqual(hostArgv, wantNil) {
		t.Errorf("Command(nil env) argv = %q, want %q", hostArgv, wantNil)
	}
	if !reflect.DeepEqual(hostEnv, dockerx.ClientEnv()) {
		t.Errorf("Command(nil env) hostEnv = %q, want ClientEnv()", hostEnv)
	}
}

// Decision 8: the image's user stands only when .Config.User names a
// NON-ROOT user; unset or explicit root in any spelling falls through to
// the invoking uid:gid.
func TestImageUserIsNonRoot(t *testing.T) {
	nonRoot := []string{"worker", "1000", "1000:1000", "app:0", "nobody"}
	for _, u := range nonRoot {
		if !imageUserIsNonRoot(u) {
			t.Errorf("imageUserIsNonRoot(%q) = false, want true (image's choice stands)", u)
		}
	}
	root := []string{"", "root", "0", "0:0", "root:root", "0:root", "00"}
	for _, u := range root {
		if imageUserIsNonRoot(u) {
			t.Errorf("imageUserIsNonRoot(%q) = true, want false (keeper runs as the invoking uid:gid)", u)
		}
	}
}

// The race.started observational key exec_image_digest reaches race via a
// type assertion on the backend — pin that a T1 backend exposes it.
func TestT1BackendExposesImageDigest(t *testing.T) {
	b, err := New(Config{Tier: object.TierT1Container, Image: testImage()})
	if err != nil {
		t.Fatal(err)
	}
	imgd, ok := b.(interface{ ImageDigest() string })
	if !ok {
		t.Fatal("T1 backend does not expose ImageDigest()")
	}
	if got := imgd.ImageDigest(); got != testImage().Digest {
		t.Errorf("ImageDigest = %q, want %q", got, testImage().Digest)
	}
	// The T0 backend deliberately does not: there is no image.
	t0, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := t0.(interface{ ImageDigest() string }); ok {
		t.Error("T0 backend exposes ImageDigest(); want none")
	}
}
