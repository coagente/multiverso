// Package backend implements the ExecutionBackend contract (XP-1): the
// isolation surface a world executes behind. Two implementations ship in
// M1c — T0-worktree (bare host, M1b behavior byte-compatible) and
// T1-container (docker keeper containers over dockerx). Policy and
// plumbing must not mix: this package knows worlds and nothing about
// docker flags beyond calling dockerx. See
// docs/design/M1c-containers-parallel.md.
package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/dockerx"
	"github.com/coagente/multiverso/internal/object"
)

// Backend provisions isolation for worlds. Implementations: T0-worktree
// (bare host, M1b behavior), T1-container (docker). It is one of the five
// PRD §9 extension surfaces (ExecutionBackend). Open is deliberately
// warm-pool-shaped (decision 19): a future pool pre-starts keepers and
// hands them out behind the same call — nothing here assumes cold starts.
type Backend interface {
	Tier() string // object.TierT0Worktree | object.TierT1Container
	// Open provisions isolation for one world whose worktree the
	// orchestrator has already created at dir. T0: a no-op wrapper.
	// T1: docker run of the keeper container. Errors are machinery
	// failures and abort the race.
	Open(ctx context.Context, dir string, opts OpenOpts) (World, error)
}

// OpenOpts carries M1f's evidence plumbing into the world (the T1 mount
// layout). Every field may be empty, which is the T0 case and the M1c
// behaviour byte-for-byte: on the bare host the in-world paths ARE the
// host paths, so nothing needs mounting.
//
// The layout on T1, in full:
//
//	/work         the worktree                    rw   the agent (phase A)
//	/work-ro      the SAME worktree               ro   nobody — the oracle's cwd
//	/mvo/evidence <raceDir>/ev/<world>/<kind>/    dir 0755, invoking uid
//	/mvo/scratch  <raceDir>/scratch/<world>/…     0777, the test process
//	/mvo/plugin   <wsDir>/plugin/<digest>/        ro, 0555
//
// M1c decision 3's "one mutable state" is preserved: /work and /work-ro
// are the same host directory, mounted twice.
type OpenOpts struct {
	EvidenceDir string // host dir, control-plane-owned; bind → /mvo/evidence
	ScratchDir  string // host dir, oracle-uid-writable;  bind → /mvo/scratch
	PluginDir   string // host dir, read-only;            bind → /mvo/plugin
}

// In-world mount points (T1). Under T0 the in-world path of each of these
// is the host path itself.
const (
	InWorldRO       = "/work-ro"
	InWorldEvidence = "/mvo/evidence"
	InWorldScratch  = "/mvo/scratch"
	InWorldPlugin   = "/mvo/plugin"
)

// World is one provisioned world's execution surface.
type World interface {
	Tier() string
	Dir() string // host worktree path — the evidence-capture surface (AG-4)
	// Command maps an in-world invocation onto the host invocation that
	// executes it inside the world. env holds NAME=value pairs for the
	// in-world process; nil means the world's own default environment.
	//   T0: identity — hostArgv = argv, hostEnv = env (nil ⇒ the spawner
	//       inherits the ambient environment, exactly M0/M1b behavior).
	//   T1: hostArgv = docker exec argv with name-only -e flags (decision
	//       7 env mapping — values never sit on the host command line),
	//       hostEnv = the docker-client allowlist (decision 2) plus the
	//       NAME=value pairs the name-only flags resolve from.
	Command(argv, env []string) (hostArgv, hostEnv []string)
	// Kill terminates everything executing in the world. T0: no-op (the
	// runner's host process-group kill is authoritative). T1: docker kill
	// (pid-namespace teardown). Idempotent; a dead world is not an error.
	Kill() error
	Caps() object.IsolationCaps
	// EnvDigest builds the XP-3 env manifest for the world, stores its
	// canonical bytes in CAS, and returns its "mv0:" digest.
	EnvDigest(store *cas.Store) (string, error)
	// Close tears down the world's isolation (T1: docker rm -f;
	// idempotent). The worktree itself remains the orchestrator's to
	// remove. Safe to call more than once and after Kill.
	Close() error
}

// Config selects and parameterizes a backend. Zero caps mean uncapped and
// are recorded as such (weak defaults, honest labels).
type Config struct {
	Tier         string        // object.TierT0Worktree (default "") | object.TierT1Container
	Image        dockerx.Image // resolved, digest-pinned (T1 only; zero value for T0)
	CPUMilli     int64         // 0 = uncapped; 1500 = --cpus 1.5
	MemoryMB     int64         // 0 = uncapped
	PidsLimit    int64         // 0 = uncapped
	AllowNetwork bool          // false = --network none (NFR-4 default)
	KeeperTTL    time.Duration // container self-expiry (decision 4); 0 = 86400 s
	IntentDigest string        // label for orphan identification
}

// New returns the backend selected by cfg. The default (empty) tier is
// T0-worktree; T1 requires a resolved, digest-pinned image; unknown tiers
// are errors.
func New(cfg Config) (Backend, error) {
	switch cfg.Tier {
	case "", object.TierT0Worktree:
		return t0Backend{}, nil
	case object.TierT1Container:
		if cfg.Image.Digest == "" || cfg.Image.RunRef == "" {
			return nil, errors.New("backend: T1-container requires a resolved, digest-pinned image")
		}
		return &t1Backend{cfg: cfg}, nil
	}
	return nil, fmt.Errorf("backend: unknown tier %q (want %q or %q)",
		cfg.Tier, object.TierT0Worktree, object.TierT1Container)
}
