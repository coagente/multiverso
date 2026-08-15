package race

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/policy"
)

// baseWorld is one world-shaped worktree at the intent's base commit, opened
// through the SAME backend as the candidates (same tier, image and caps): a
// measurement taken under a different environment is not a measurement of
// this race's denominator, and a probe run on the host says nothing about
// what is importable inside the image.
//
// It serves both pre-race duties — the tools probe (M1e decision 15, which
// must precede race.started) and the base-state collect measurement (decision
// 13, which must follow it) — so a race pays for one worktree and, under T1,
// one keeper, not two.
type baseWorld struct {
	dir                 string
	added               bool
	wh                  backend.World
	evRoot, scratchRoot string
}

// openBaseWorld creates the base worktree and opens it behind the backend.
func (r *raceRun) openBaseWorld(ctx context.Context) (*baseWorld, error) {
	dir := filepath.Join(r.raceDir, "base")
	r.repoMu.Lock()
	err := gitx.AddWorktree(r.cfg.Repo, dir, r.intent.Base.Commit)
	r.repoMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("race: base world: %w", err)
	}
	bw := &baseWorld{dir: dir, added: true}
	// The denominator is observed the same way the candidates are: a
	// baseline measured through a different evidence path is not a
	// baseline for THIS race's numbers.
	bw.evRoot = filepath.Join(r.raceDir, "ev", "base")
	bw.scratchRoot = filepath.Join(r.raceDir, "scratch", "base")
	if err := os.MkdirAll(bw.evRoot, 0o755); err != nil {
		bw.close(r)
		return nil, fmt.Errorf("race: base world: %w", err)
	}
	if err := os.MkdirAll(bw.scratchRoot, 0o777); err != nil {
		bw.close(r)
		return nil, fmt.Errorf("race: base world: %w", err)
	}
	wh, err := r.cfg.Backend.Open(ctx, dir, r.openOptsFor(bw.evRoot, bw.scratchRoot))
	if err != nil {
		bw.close(r)
		return nil, fmt.Errorf("race: base world: %w", err)
	}
	bw.wh = wh
	return bw, nil
}

// close tears down the base world: the handle first (containers are
// transport), then the worktree. Idempotent.
func (bw *baseWorld) close(r *raceRun) {
	if bw == nil {
		return
	}
	if bw.wh != nil {
		_ = bw.wh.Close()
		bw.wh = nil
	}
	if !bw.added {
		return
	}
	bw.added = false
	r.repoMu.Lock()
	defer r.repoMu.Unlock()
	_ = removeWorld(r.cfg.Repo, bw.dir) // best effort: nothing is recorded from it
}

// needsBaseWorld reports whether a policy obliges the race to measure the
// base state at all: a pytest kind must be pre-flighted (decision 15), and a
// collected-not-below gate needs its denominator (decision 13).
func needsBaseWorld(pol policy.Policy) bool {
	if _, ok := pol.CollectOracle(); ok {
		return true
	}
	return len(pytestOracles(pol)) > 0
}

// pytestOracles lists the REQUIRED pytest-kind instances, in ladder order.
// Declared-but-unrequired oracles are never run and never pre-flighted.
func pytestOracles(pol policy.Policy) []policy.Oracle {
	var out []policy.Oracle
	for _, name := range pol.Required {
		o, ok := pol.OracleByName(name)
		if !ok {
			continue
		}
		if o.Kind == policy.KindPytestCollect || o.Kind == policy.KindPytestSuite {
			out = append(out, o)
		}
	}
	return out
}

// oraclePython is the interpreter an instance's probe runs under: the head of
// its runner prefix, so a repo pinned to a virtualenv is probed with THAT
// interpreter and not with whatever python happens to be first on PATH.
func oraclePython(o policy.Oracle) string {
	if len(o.Argv) > 0 {
		return o.Argv[0]
	}
	return ""
}

// preflight refuses a race whose policy requires a pytest kind in an
// environment where pytest is not importable (M1e decision 15). It runs
// BEFORE race.started, so a missing toolchain leaves the ledger empty of race
// events: a missing toolchain is machinery, never a failing candidate, and
// recording it as one would be a lie about the candidates.
//
// An empty tools map is not by itself evidence that pytest is absent: a
// probe the race context cancelled or timed out reports exactly the same
// nothing. Diagnosing that as "pytest is not importable" was confidently
// wrong AND sent the operator off to rewrite their policy for a language
// problem they did not have, so the two causes are told apart here and
// budgetMS names the bound that actually stopped it.
func preflight(ctx context.Context, pol policy.Policy, w backend.World, envDesc string, budgetMS int64) error {
	probed := make(map[string]map[string]string)
	for _, o := range pytestOracles(pol) {
		py := oraclePython(o)
		tools, seen := probed[py]
		if !seen {
			tools, _ = oracle.Probe(ctx, w, py)
			probed[py] = tools
		}
		if tools[oracle.ToolPytest] != "" {
			continue
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("race: oracle %q (%s) pre-flight probe did not complete: %s (intent budget_wall_ms=%d) — raise --budget-wall-ms on a new intent, or drop the budget; this says nothing about whether pytest is installed",
				o.Name, o.Kind, probeStopReason(ctxErr), budgetMS)
		}
		return fmt.Errorf("race: policy requires oracle %q (%s) but pytest is not importable in this environment (%s); Multiverso's oracle ladder is Python-first (PRD §10) — author a command-kind policy for other languages",
			o.Name, o.Kind, envDesc)
	}
	return nil
}

// probeStopReason names what ended the probe, in the operator's terms: a
// deadline is the intent's wall budget, a cancel is an earlier failure in
// the same race tearing the context down.
func probeStopReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "budget exhausted"
	}
	return "race cancelled"
}

// execDesc names the environment the pre-flight probed, for its error.
func execDesc(be backend.Backend) string {
	if be.Tier() != object.TierT1Container {
		return "T0 host"
	}
	if b, ok := be.(interface{ ImageRef() string }); ok && b.ImageRef() != "" {
		return "T1 image " + b.ImageRef()
	}
	return "T1 image"
}

// measureBaseline runs the policy's collect oracle on the base tree and
// records baseline.recorded (M1e decision 13). The result is an INPUT to the
// race, not evidence about a candidate: a Receipt must bind a World, and the
// base tree is nobody's candidate, so nothing is minted as one.
//
// A baseline that did not pass, or that collected nothing, aborts the race:
// a repo whose base tree collects no tests cannot give the guard meaning, and
// racing on it would produce receipts whose collected_delta was a fiction.
func (r *raceRun) measureBaseline(ctx context.Context, pol policy.Policy, bw *baseWorld, timeout time.Duration) (int64, error) {
	spec, ok := pol.CollectOracle()
	if !ok {
		return 0, nil
	}
	tier := r.cfg.Backend.Tier()
	p := oracle.Params{
		Spec: spec, CAS: r.cfg.CAS, Timeout: timeout,
		Regime: r.ev.regime, Crosscheck: r.ev.crosscheck, PluginAutoload: r.ev.autoload,
		PluginDir: r.ev.pluginDir, PluginDigest: r.ev.pluginDigest,
		InWorldPlugin: inWorldPath(tier, backend.InWorldPlugin, ""),
	}
	if bw.evRoot != "" && r.ev.regime != object.RegimeInTree {
		p.EvidenceDir = filepath.Join(bw.evRoot, spec.Kind)
		p.ScratchDir = filepath.Join(bw.scratchRoot, spec.Kind)
		p.InWorldEvidence = inWorldPath(tier, backend.InWorldEvidence, spec.Kind)
		p.InWorldScratch = inWorldPath(tier, backend.InWorldScratch, spec.Kind)
	}
	if p.InWorldPlugin == "" {
		p.InWorldPlugin = r.ev.pluginDir
	}
	o, err := oracle.New(p)
	if err != nil {
		return 0, fmt.Errorf("race: baseline: %w", err)
	}
	rec, err := o.Run(ctx, bw.wh)
	if err != nil {
		return 0, fmt.Errorf("race: baseline: %w", err)
	}
	total, known := rec.Result.Metrics[policy.MetricCollectedTotal]
	artifact := func(i int) string {
		if i < len(rec.Result.Artifacts) {
			return rec.Result.Artifacts[i]
		}
		return ""
	}
	// Observational (M1b precedent): audit ignores it, the hash chain covers
	// it, and the delta it produced lives in the receipts, which ARE replay
	// inputs. Recorded before any abort, so a failed measurement is on the
	// record rather than merely in an error string.
	if err := r.appendEventLocked("baseline.recorded", map[string]any{
		"collected_total": total,
		"duration_ms":     rec.Execution.DurationMS,
		// The baseline collect run streams like any other run, and
		// collected_base is the denominator collected-not-below divides
		// by. Naming its stream here is what puts that observation inside
		// the audited closure instead of leaving it an orphan blob in the
		// sweep's unreferenced count. "" under the in-tree regime.
		"evidence_stream": artifact(3),
		"exit_code":       rec.Execution.ExitCode,
		"intent":          r.cfg.Intent,
		"oracle": map[string]any{
			"config":  rec.Oracle.Config,
			"id":      rec.Oracle.ID,
			"version": rec.Oracle.Version,
		},
		"probe":  artifact(2),
		"stderr": artifact(1),
		"stdout": artifact(0),
		"tree":   r.intent.Base.Tree,
	}); err != nil {
		return 0, err
	}
	if rec.Result.Status != oracle.StatusPass || !known || total < 1 {
		// Same discipline as the pre-flight probe: a measurement the race
		// budget cut short is a budget fact, not a fact about the repo's
		// tests, and must not be reported as one.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, fmt.Errorf("race: baseline: oracle %q on base tree %s did not complete: %s (intent budget_wall_ms=%d) — raise --budget-wall-ms on a new intent, or drop the budget",
				spec.Name, r.intent.Base.Tree, probeStopReason(ctxErr), r.intent.Budget.MaxWallMS)
		}
		return 0, fmt.Errorf("race: baseline: oracle %q on base tree %s: status=%s exit=%d collected_total=%s; a collected-not-below gate has no honest denominator here",
			spec.Name, r.intent.Base.Tree, rec.Result.Status, rec.Execution.ExitCode, countText(total, known))
	}
	return total, nil
}

func countText(total int64, known bool) string {
	if !known {
		return "absent"
	}
	return fmt.Sprintf("%d", total)
}
