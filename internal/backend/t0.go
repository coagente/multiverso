package backend

import (
	"context"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/object"
)

// t0Backend is the bare-host worktree backend: M1b behavior behind the
// XP-1 interface, byte-compatible — same argv, same env, same manifests,
// same world digests.
type t0Backend struct{}

// Tier implements Backend.
func (t0Backend) Tier() string { return object.TierT0Worktree }

// Open implements Backend: a T0 world is the directory itself. OpenOpts
// is ignored by construction — on the bare host the evidence, scratch and
// plugin directories are reachable at their host paths, so there is
// nothing to map and the M1c identity mapping is preserved byte-for-byte.
func (t0Backend) Open(_ context.Context, dir string, _ OpenOpts) (World, error) {
	return HostDir(dir), nil
}

// HostDir wraps a bare directory as a T0 world with no backend — the
// admission path's landing worktree and the T0 backend's own Open result.
func HostDir(dir string) World { return hostWorld{dir: dir} }

// hostWorld is the identity World: execution happens on the bare host in
// the worktree, uncapped, and the record says so (object.HostCaps).
type hostWorld struct{ dir string }

// Tier implements World.
func (hostWorld) Tier() string { return object.TierT0Worktree }

// Dir implements World.
func (w hostWorld) Dir() string { return w.dir }

// Command implements World: identity — hostArgv = argv, hostEnv = env
// (nil ⇒ the spawner inherits the ambient environment, exactly M0/M1b).
func (hostWorld) Command(argv, env []string) ([]string, []string) {
	return argv, env
}

// Kill implements World: no-op — the runner's host process-group kill is
// authoritative on T0.
func (hostWorld) Kill() error { return nil }

// Caps implements World: the honest uncapped-bare-host record (PRD §9).
func (hostWorld) Caps() object.IsolationCaps { return object.HostCaps() }

// EnvDigest implements World: M0/M1b's exact manifest bytes — T0 world
// digests do not move (M1c decision 11).
func (w hostWorld) EnvDigest(store *cas.Store) (string, error) {
	return T0EnvDigest(store, w.dir)
}

// Close implements World: nothing to tear down on the bare host.
func (hostWorld) Close() error { return nil }
