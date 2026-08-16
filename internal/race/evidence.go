package race

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/policy"
)

// evidenceSetup is one race's resolved observation plumbing, computed once
// before race.started and shared by every world.
type evidenceSetup struct {
	regime       string // RESOLVED: never policy.RegimeAuto
	crosscheck   string
	autoload     string // policy.AutoloadOff | policy.AutoloadOn
	pluginDir    string // host directory holding mvo_evidence.py
	pluginDigest string
}

// isolatedExecAvailable reports whether this binary can actually DELIVER
// the `isolated` regime, which needs two things beyond the T1 mounts:
// oracle execs carrying `--user <distinct uid>`, and a cwd of /work-ro.
//
// Neither ships. `t1Mounts` builds all four mounts, but `t1World.Command`
// execs with workdir /work (read-write) and no --user, so an oracle runs
// as the invoking uid — the same uid that owns the worktree AND the
// evidence directory. Every property the regime table promises under
// `isolated` (the candidate cannot modify its own tree during the run,
// cannot unlink or replace the channel, cannot author the JUnit file the
// cross-check reads) therefore does NOT hold.
//
// It is a constant rather than a probe because the missing piece is this
// binary's exec path, not the host's: when `--user` ships, this flips to a
// real capability test and the two call sites below need no other change.
//
// Until then the regime is not available, and the whole point of M1f is
// that a receipt says what actually happened. Recording `isolated` over a
// run that had none of its guarantees would be the study's finding
// exactly — a signed claim stronger than the evidence behind it — with
// the label itself doing the laundering.
const isolatedExecAvailable = false

// resolveRegime turns the policy's declared regime into the one this race
// will actually run under, and refuses — as MACHINERY, before anything is
// recorded — a policy that requires more isolation than this binary and
// tier can give.
//
// A regime is CHOSEN BY CAPABILITY (M1f decision 13) and recorded in every
// receipt, so `auto` may only resolve to something the run will really be.
// With no isolated exec path, `auto` is `streamed` on every tier — said
// loudly here, printed by `mvo explain`, and recorded in every receipt,
// because a guarantee nobody can see the absence of is a guarantee nobody
// has. An explicitly declared `isolated` is refused rather than quietly
// downgraded: a binary that cannot enforce a policy must not pretend to
// (decision 3).
// CheckRegime reports whether this binary can DELIVER the regime a policy
// declares, in the one sentence `mvo race`'s pre-flight uses. It is
// exported so the refusal can also happen where an operator can act on it
// — at `mvo policy use`, before the policy becomes the workspace default —
// rather than only at the first race. A shipped example policy that
// installs cleanly and then refuses every race is a workspace bricked by a
// file the docs told you to read.
func CheckRegime(verb string, pol policy.Policy) error {
	if pol.Evidence.Regime != object.RegimeIsolated || isolatedExecAvailable {
		return nil
	}
	return fmt.Errorf(
		"%s: policy %s requires evidence regime %q, which needs oracle execs under a distinct uid with a read-only worktree; this binary does not ship that exec path (oracles run as the invoking uid with a writable cwd), so the guarantee cannot be delivered — set evidence.regime to %q (resolves to %q) or %q",
		verb, pol.Digest, object.RegimeIsolated, policy.RegimeAuto, object.RegimeStreamed, object.RegimeInTree)
}

func resolveRegime(pol policy.Policy, tier string) (string, error) {
	switch pol.Evidence.Regime {
	case "", policy.RegimeAuto:
		if isolatedExecAvailable && tier == object.TierT1Container {
			return object.RegimeIsolated, nil
		}
		return object.RegimeStreamed, nil
	case object.RegimeIsolated:
		if !isolatedExecAvailable {
			return "", CheckRegime("race", pol)
		}
		if tier != object.TierT1Container {
			return "", fmt.Errorf(
				"race: policy %s requires evidence regime %q, which needs isolation tier %s (this race is %s); re-run with --tier T1 or set evidence.regime to %q",
				pol.Digest, object.RegimeIsolated, object.TierT1Container, tier, policy.RegimeAuto)
		}
		return object.RegimeIsolated, nil
	default:
		return pol.Evidence.Regime, nil
	}
}

// setupEvidence resolves the regime and materializes the observer. The
// plugin is written ONCE per workspace, under its own content address, so
// "which observer saw this run" is an auditable fact rather than a version
// guess — and two binaries with different plugins never share a copy.
func (r *raceRun) setupEvidence(pol policy.Policy, tier string) (evidenceSetup, error) {
	regime, err := resolveRegime(pol, tier)
	if err != nil {
		return evidenceSetup{}, err
	}
	out := evidenceSetup{
		regime:     regime,
		crosscheck: pol.Evidence.Crosscheck,
		autoload:   pol.Evidence.PluginAutoload,
	}
	if regime == object.RegimeInTree {
		return out, nil
	}
	root := filepath.Join(filepath.Dir(r.cfg.WorldsDir), "plugin")
	dir, digest, err := oracle.MaterializePlugin(root)
	if err != nil {
		return evidenceSetup{}, fmt.Errorf("race: %w", err)
	}
	// The observer's bytes reach CAS before any run consumes them, so a
	// receipt naming a plugin digest always resolves to the source that
	// produced it.
	if _, err := r.cfg.CAS.Put(oracle.PluginSource()); err != nil {
		return evidenceSetup{}, fmt.Errorf("race: store evidence plugin: %w", err)
	}
	out.pluginDir, out.pluginDigest = dir, digest
	return out, nil
}

// worldEvidenceDirs builds one world's control-plane directories:
// <raceDir>/ev/<ordinal>/ for the FIFOs (owned by the invoking uid, so the
// oracle uid cannot unlink a channel) and <raceDir>/scratch/<ordinal>/ for
// the cross-check files (writable by the test process, which is exactly
// why they are a cross-check and not a source). Each oracle kind gets a
// subdirectory of each, so two rungs of one ladder never share a channel.
//
// A container is opened once per world, so the ROOTS are what gets
// mounted; the per-kind subdirectory is a path inside the mount.
func (r *raceRun) worldEvidenceDirs(ordinal int) (evRoot, scratchRoot string, err error) {
	tag := fmt.Sprintf("%03d", ordinal)
	evRoot = filepath.Join(r.raceDir, "ev", tag)
	scratchRoot = filepath.Join(r.raceDir, "scratch", tag)
	if err := os.MkdirAll(evRoot, 0o755); err != nil {
		return "", "", fmt.Errorf("race: evidence dir: %w", err)
	}
	if err := os.MkdirAll(scratchRoot, 0o777); err != nil {
		return "", "", fmt.Errorf("race: scratch dir: %w", err)
	}
	if err := os.Chmod(scratchRoot, 0o777); err != nil {
		return "", "", fmt.Errorf("race: scratch dir: %w", err)
	}
	return evRoot, scratchRoot, nil
}

// dropEvidenceDirs removes the race's control-plane evidence and scratch
// trees. They hold no evidence — every stream and every cross-check file
// that mattered is already in CAS — so they are removed on every exit
// path, including --keep-worlds, which keeps the WORKTREES (the evidence
// surface an operator inspects) and never the plumbing.
func (r *raceRun) dropEvidenceDirs() {
	if r.raceDir == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(r.raceDir, "ev"))
	_ = os.RemoveAll(filepath.Join(r.raceDir, "scratch"))
}

// inWorldPath maps a control-plane directory onto the path the world sees
// it at. On T0 they are the same path — nothing is mounted, so the host
// path IS the in-world path — and the oracle is told "" to mean that.
func inWorldPath(tier, mount, kind string) string {
	if tier != object.TierT1Container {
		return ""
	}
	if kind == "" {
		return mount
	}
	return mount + "/" + kind
}

// openOptsFor builds the mounts one world needs. T0 ignores them by
// construction (M1c identity mapping, preserved byte-for-byte).
//
// corpusDir is the caller's, never the race's shared one, and that is the
// whole of the fix for a red-team finding: this function used to hand
// `r.corpusDir` to EVERY world unconditionally, the keeper is opened before
// `Adapter.Start` runs the agent inside it (M1c decision 4: one long-lived
// keeper per world), and phase 0 has already written the corpus by then. A
// `docker inspect` of the candidate keepers during a T1 race showed
// `<raceDir>/corpus -> /mvo/corpus ro=true` on every one of them, for the
// whole of phase A — which is exactly what decision 13, backend.OpenOpts's
// own comment and M2a's verdict on corpus vector 19 all say does not
// happen. The mount is now per world and its directory is EMPTY until phase
// A has joined.
func (r *raceRun) openOptsFor(evDir, scratchDir, corpusDir string) backend.OpenOpts {
	if r.ev.regime == object.RegimeInTree {
		return backend.OpenOpts{}
	}
	return backend.OpenOpts{
		EvidenceDir: evDir,
		ScratchDir:  scratchDir,
		PluginDir:   r.ev.pluginDir,
		// Read-only, outside the worktree, and empty when no policy asks
		// for a corpus — a race that declares none mounts nothing, so
		// every pre-M2a keeper argv is unchanged.
		CorpusDir: corpusDir,
	}
}
