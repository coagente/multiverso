package race

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/coagente/multiverso/internal/agent"
	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/policy"
)

// Candidate is one world's generation spec (AG-5): the literal prompt, a
// pinned model, and the per-world budget the adapter enforces (AG-2).
type Candidate struct {
	Prompt string       // literal prompt text (script: patch bytes as a string)
	Model  string       // model pin; "" = tool default
	Budget agent.Budget // per-world budget (AG-2)
	Env    []string     // extra parent env var NAMES passed through (names only, decision 14)
}

// Config wires one race run. All fields are required except KeepWorlds,
// LegacyOracle and OracleTimeout.
type Config struct {
	Repo       string          // git repo root; worlds are worktrees of it
	Ledger     *ledger.Ledger  // event log
	CAS        *cas.Store      // object + artifact store
	Intent     string          // intent digest ("mv0:...") already in CAS
	Adapter    agent.Adapter   // generator for every world (AG-1)
	Candidates []Candidate     // one world per candidate, len ≥ 1 (CP-2)
	WorldsDir  string          // parent directory for world worktrees
	Backend    backend.Backend // isolation provider (XP-1), required
	Parallel   int             // bounded workers, required ≥ 1; 1 = the M1b schedule
	KeepWorlds bool            // keep worktrees after the race (--keep-worlds; never containers)
	// LegacyOracle is the v0-dialect verifier: a v0 policy names a gate
	// (suite-pass) but NOT the command that decides it, so under that shape
	// the race must be handed one — `mvo race --oracle-cmd`, exactly as in
	// M0–M1d. Under a v1 policy it must be nil: the policy names its own
	// oracles and the orchestrator builds them (M1e decision 18).
	LegacyOracle oracle.Oracle
	// OracleTimeout is the fallback wall bound for policy-declared oracles
	// that name none of their own; zero means the intent's max_wall_ms.
	OracleTimeout time.Duration
}

// WorldRun is one world's trajectory through the race.
type WorldRun struct {
	Ordinal int // 1-based candidate ordinal
	Dir     string
	Digest  string
	World   object.World
	Run     *agent.RunResult
	// Receipts holds every receipt the ladder recorded for this world, in
	// the order its oracles ran (M1e decision 12): a world that failed the
	// first hard gate has exactly one.
	Receipts      []object.RecordedReceipt
	ReceiptDigest string          // Receipts[0].Digest; "" when the ladder recorded none
	Receipt       *object.Receipt // &Receipts[0].Receipt; nil when none
}

// Result is what a race produced.
type Result struct {
	Decision       object.Decision
	DecisionDigest string
	Worlds         []WorldRun
}

// raceRun is the shared state of one Run: the two-phase bounded worker
// pool (M1c decision 15), the ledger serialization point (decision 16),
// and the worktree serialization lock (decision 17).
type raceRun struct {
	cfg     Config
	intent  object.Intent
	pol     policy.Policy
	adapter string
	raceDir string
	// ev is the race's resolved observation plumbing (M1f): the regime
	// every receipt will record, and the materialized observer's digest.
	ev evidenceSetup
	// baseline is collected_delta's denominator, measured once on the base
	// tree; oracle instances are built PER WORLD (M1f decision 18), so it
	// is held here rather than baked into a shared ladder.
	baseline      int64
	oracleTimeout time.Duration

	recMu  sync.Mutex // one total order over every ledger append in the race path
	repoMu sync.Mutex // git worktree add/remove/prune contend on repo-level locks

	slots []slot

	errMu    sync.Mutex
	firstErr error
	cancel   context.CancelFunc
}

// slot is candidate k's pre-sized result cell (index = ordinal-1): no
// ordering ambiguity to repair afterward (decision 15).
type slot struct {
	dir      string
	added    bool          // worktree exists (cleanup owes a removal)
	wh       backend.World // non-nil once the backend opened the world
	dig      string
	world    object.World
	run      *agent.RunResult
	receipts []object.RecordedReceipt // ladder order
	// evRoot and scratchRoot are this world's control-plane directories:
	// the FIFO the plugin writes to, and the scratch the cross-check files
	// land in. Both are created before the world is opened, because a T1
	// container's mounts are fixed at open time.
	evRoot, scratchRoot string
}

// ladderRung is one required oracle instance, named by the policy.
type ladderRung struct {
	name   string
	oracle oracle.Oracle
}

// fail records the first control-plane failure and cancels the race ctx:
// all workers drain, their in-flight kills follow the ctx path honestly
// (INTERRUPTED), and Run returns this error. Already-recorded events stay
// recorded.
func (r *raceRun) fail(err error) {
	r.errMu.Lock()
	if r.firstErr == nil {
		r.firstErr = err
		r.cancel()
	}
	r.errMu.Unlock()
}

func (r *raceRun) failed() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.firstErr
}

// appendEventLocked serializes one observational event append (decision
// 16: a race-owned mutex, not an appender goroutine — an append failure
// must abort the appending worker synchronously).
func (r *raceRun) appendEventLocked(typ string, body map[string]any) error {
	r.recMu.Lock()
	defer r.recMu.Unlock()
	return appendEvent(r.cfg.Ledger, typ, body)
}

// recordObjectLocked serializes one object record (CAS put + ledger
// append). CAS puts are lock-free-safe on their own (temp-file + rename,
// idempotent); the mutex is for the ledger's total order.
func (r *raceRun) recordObjectLocked(typ string, v any) (string, error) {
	r.recMu.Lock()
	defer r.recMu.Unlock()
	return recordObject(r.cfg, typ, v)
}

// pool runs f(i) for each index in order of submission with at most
// cfg.Parallel concurrent workers. With Parallel == 1 it degenerates to
// the sequential M1b schedule structurally (decision 15). Workers drain
// without running once a control-plane failure is recorded.
func (r *raceRun) pool(indices []int, f func(i int) error) {
	idx := make(chan int)
	var wg sync.WaitGroup
	workers := r.cfg.Parallel
	if workers > len(indices) {
		workers = len(indices)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				if r.failed() != nil {
					continue // drain
				}
				if err := f(i); err != nil {
					r.fail(err)
				}
			}
		}()
	}
	for _, i := range indices {
		idx <- i
	}
	close(idx)
	wg.Wait()
}

// Run executes the race (CP-2, CP-3, M1c): worlds generated by the
// configured adapter behind the configured execution backend (XP-1), in a
// two-phase bounded worker pool — phase A generates worlds, a barrier
// joins, phase B runs oracles — then one recorded decision assembled from
// digest-sorted inputs (decision 17). The control plane captures each
// world's diff (AG-4) and transcript (AG-3) host-side regardless of tier.
// Agent failure is evidence and the race continues; a non-nil error means
// evidence itself could not be produced and the race aborts (decision 10).
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	var intent object.Intent
	if err := loadObject(cfg.CAS, cfg.Intent, &intent); err != nil {
		return nil, err
	}
	if intent.Schema != object.SchemaIntent {
		return nil, fmt.Errorf("race: %s has schema %q, want %q", cfg.Intent, intent.Schema, object.SchemaIntent)
	}
	// The pinned policy is loaded through internal/policy, which accepts
	// both schema versions and compiles them to one in-memory form: the
	// orchestrator never branches on a policy's wire shape.
	pol, err := policy.Load(cfg.CAS, intent.Policy)
	if err != nil {
		return nil, fmt.Errorf("race: %w", err)
	}
	// Decision 18, enforced where the policy is known rather than where the
	// flag was typed: a v0 policy cannot name the command its gate means, so
	// one must be supplied; a v1 policy names its own and refuses an
	// override, which is the CP-5 hole this milestone closes.
	if pol.Dialect == policy.DialectV0 && cfg.LegacyOracle == nil {
		return nil, fmt.Errorf("race: policy %s is %s: it names a gate but not the command that decides it, so an oracle must be supplied (--oracle-cmd)",
			pol.Digest, policy.SchemaShort(pol.Schema))
	}
	if pol.Dialect != policy.DialectV0 && cfg.LegacyOracle != nil {
		return nil, fmt.Errorf("race: an oracle override is not permitted with policy %s (%s): the policy names its own oracles",
			pol.Digest, policy.SchemaShort(pol.Schema))
	}
	// CP-2: the candidate count is hard-capped before race.started — an
	// over-budget race is refused, never recorded (closes the M0 TODO).
	if len(cfg.Candidates) > intent.Budget.MaxCandidates {
		// Name the flag that fixes it: the default max_candidates is 2, so
		// every user with three or more patches hits this, and citing a PRD
		// section instead of a flag leaves them nowhere to go.
		return nil, fmt.Errorf("race: %d candidates exceed intent budget max_candidates=%d (CP-2) — an intent's budget is pinned at creation, so raise it on a NEW intent with `mvo intent new --budget-candidates %d`",
			len(cfg.Candidates), intent.Budget.MaxCandidates, len(cfg.Candidates))
	}
	// Budget: MaxWallMS bounds the whole race; per-world wall budgets are
	// the candidates' own (agent watchdog, AG-2).
	if intent.Budget.MaxWallMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(intent.Budget.MaxWallMS)*time.Millisecond)
		defer cancel()
	}
	// The race ctx: the first control-plane failure cancels it and every
	// worker drains (failure semantics under parallelism).
	raceCtx, cancelRace := context.WithCancel(ctx)
	defer cancelRace()

	r := &raceRun{
		cfg:     cfg,
		intent:  intent,
		pol:     pol,
		adapter: cfg.Adapter.ID() + "@" + cfg.Adapter.Version(),
		slots:   make([]slot, len(cfg.Candidates)),
		cancel:  cancelRace,
	}
	// A policy oracle that names no timeout of its own inherits the intent's
	// wall budget: the race's own bound, never an unbounded verifier.
	oracleTimeout := cfg.OracleTimeout
	if oracleTimeout <= 0 {
		oracleTimeout = time.Duration(intent.Budget.MaxWallMS) * time.Millisecond
	}

	// Each race gets its own fresh directory under WorldsDir (unique via
	// MkdirTemp): re-racing an intent is a first-class flow, so world paths
	// must never collide with worktrees left behind by --keep-worlds or a
	// crashed race. Created before race.started so a broken worlds dir
	// cannot leave a dangling half-race in the permanent ledger.
	if err := os.MkdirAll(cfg.WorldsDir, 0o755); err != nil {
		return nil, fmt.Errorf("race: create worlds dir: %w", err)
	}
	r.raceDir, err = os.MkdirTemp(cfg.WorldsDir, "race-")
	if err != nil {
		return nil, fmt.Errorf("race: create race worlds dir: %w", err)
	}

	// The base world serves the pre-flight probe (which must precede
	// race.started) and the baseline measurement (which must follow it), so
	// both are paid for once. It is torn down before phase A: no candidate
	// competes with it for a worker or a container.
	// M1f pre-flight, alongside M1e's pytest probe and for the same
	// reason: a policy that requires more isolation than the tier can give
	// aborts as MACHINERY, one line, ledger untouched. Resolving the
	// regime here also means no receipt ever records the word "auto".
	r.ev, err = r.setupEvidence(pol, cfg.Backend.Tier())
	if err != nil {
		r.dropEvidenceDirs()
		os.Remove(r.raceDir)
		return nil, err
	}
	r.oracleTimeout = oracleTimeout

	var base *baseWorld
	if needsBaseWorld(pol) {
		base, err = r.openBaseWorld(raceCtx)
		if err != nil {
			r.dropEvidenceDirs()
			os.Remove(r.raceDir)
			return nil, err
		}
		// Decision 15: a repo without pytest fails at pre-flight as
		// machinery, never as a receipt — and the ledger stays empty of race
		// events.
		if err := preflight(raceCtx, pol, base.wh, execDesc(cfg.Backend), intent.Budget.MaxWallMS); err != nil {
			base.close(r)
			r.dropEvidenceDirs()
			os.Remove(r.raceDir)
			return nil, err
		}
	}

	// race.started gains five always-present observational keys: exec_tier,
	// exec_image_digest ("" for T0), parallel (M1c), plus policy and the
	// required oracle set in ladder order (M1e). Audit reads only "intent" —
	// unaffected. Container IDs appear nowhere in the ledger: containers are
	// transport, the image digest is the evidence.
	imageDigest := ""
	if b, ok := cfg.Backend.(interface{ ImageDigest() string }); ok {
		imageDigest = b.ImageDigest()
	}
	if err := appendEvent(cfg.Ledger, "race.started", map[string]any{
		"adapter":           r.adapter,
		"candidates":        len(cfg.Candidates),
		"exec_image_digest": imageDigest,
		"exec_tier":         cfg.Backend.Tier(),
		"intent":            cfg.Intent,
		"oracles":           append([]string{}, pol.Required...),
		"parallel":          cfg.Parallel,
		"policy":            pol.Digest,
	}); err != nil {
		base.close(r)
		r.dropEvidenceDirs()
		os.Remove(r.raceDir)
		return nil, err
	}

	// The base-state collect measurement: collected_delta's denominator
	// (decision 13). It runs after race.started, before phase A, and only
	// when a collected-not-below gate makes the delta mean something.
	if base != nil {
		r.baseline, err = r.measureBaseline(raceCtx, pol, base, oracleTimeout)
		base.close(r)
		if err != nil {
			r.dropEvidenceDirs()
			os.Remove(r.raceDir)
			return nil, err
		}
	}

	// Cleanup ladder (NFR-3): every opened world handle is closed however
	// Run exits — containers are ALWAYS removed (--keep-worlds keeps
	// worktrees, the evidence surface, never containers, which hold
	// nothing the CAS and worktree don't) — and worktrees are removed
	// unless kept or already removed on the success path.
	removed := false
	defer func() {
		for i := range r.slots {
			if r.slots[i].wh != nil {
				_ = r.slots[i].wh.Close()
			}
		}
		if cfg.KeepWorlds || removed {
			return
		}
		r.repoMu.Lock()
		defer r.repoMu.Unlock()
		for i := range r.slots {
			if r.slots[i].added {
				_ = removeWorld(cfg.Repo, r.slots[i].dir) // best effort on error paths
			}
		}
		r.dropEvidenceDirs()
		_ = os.Remove(r.raceDir) // fails harmlessly if a world survived
	}()

	// Phase A — generation, bounded at Parallel workers; candidate k owns
	// slot k-1.
	all := make([]int, len(cfg.Candidates))
	for i := range all {
		all[i] = i
	}
	r.pool(all, func(i int) error { return r.generate(raceCtx, i) })

	// Barrier. Non-COMPLETED worlds' handles are closed here: they get no
	// oracle run, so their isolation is done serving.
	if err := r.failed(); err != nil {
		return nil, err
	}
	var completed []int
	for i := range r.slots {
		if r.slots[i].world.Outcome == object.OutcomeCompleted {
			completed = append(completed, i)
			continue
		}
		if r.slots[i].wh != nil {
			_ = r.slots[i].wh.Close()
		}
	}

	// Phase B — verification, bounded at Parallel.
	r.pool(completed, func(i int) error { return r.verify(raceCtx, i) })
	if err := r.failed(); err != nil {
		return nil, err
	}

	// Decision inputs are assembled deterministically: worlds sorted by
	// world digest ascending, receipts by receipt digest ascending
	// (decision 17; Decide is order-independent and audit replay — which
	// assembles from ledger-scan order — reproduces it byte-for-byte).
	// The sort keys are the digests recordObjectLocked already computed
	// (slot.dig / slot.receiptDig) — no re-serialization inside the
	// comparators, and no swallowed Digest error ordering by "".
	runs := make([]WorldRun, 0, len(r.slots))
	byWorld := make([]int, len(r.slots))
	for i := range r.slots {
		s := &r.slots[i]
		run := WorldRun{
			Ordinal: i + 1, Dir: s.dir, Digest: s.dig, World: s.world,
			Run: s.run, Receipts: s.receipts,
		}
		if len(s.receipts) > 0 {
			run.ReceiptDigest = s.receipts[0].Digest
			run.Receipt = &s.receipts[0].Receipt
		}
		runs = append(runs, run)
		byWorld[i] = i
	}
	sort.Slice(byWorld, func(a, b int) bool {
		return r.slots[byWorld[a]].dig < r.slots[byWorld[b]].dig
	})
	worlds := make([]object.RecordedWorld, 0, len(r.slots))
	for _, i := range byWorld {
		worlds = append(worlds, object.RecordedWorld{Digest: r.slots[i].dig, World: r.slots[i].world})
	}
	var receipts []object.RecordedReceipt
	for i := range r.slots {
		receipts = append(receipts, r.slots[i].receipts...)
	}
	sort.Slice(receipts, func(a, b int) bool { return receipts[a].Digest < receipts[b].Digest })
	if receipts == nil {
		receipts = []object.RecordedReceipt{}
	}

	decision := Decide(pol, worlds, receipts)
	decision.CreatedAt = nowRFC3339()
	decisionDig, err := recordObject(cfg, "decision.recorded", decision)
	if err != nil {
		return nil, err
	}
	if err := appendEvent(cfg.Ledger, "race.finished", map[string]any{
		"intent": cfg.Intent, "decision": decisionDig,
	}); err != nil {
		return nil, err
	}

	if !cfg.KeepWorlds {
		removed = true
		r.repoMu.Lock()
		for i := range r.slots {
			if !r.slots[i].added {
				continue
			}
			if err := removeWorld(cfg.Repo, r.slots[i].dir); err != nil {
				r.repoMu.Unlock()
				return nil, err
			}
		}
		r.repoMu.Unlock()
		r.dropEvidenceDirs()
		if err := os.Remove(r.raceDir); err != nil {
			return nil, fmt.Errorf("race: cleanup: %w", err)
		}
	}
	return &Result{Decision: decision, DecisionDigest: decisionDig, Worlds: runs}, nil
}

// generate is phase A for candidate i (ordinal i+1): worktree (serialized
// under repoMu), backend Open, agent run behind the world handle, host-
// side evidence capture (M1b semantics verbatim), env digest from the
// handle, world.created. Per-world event order is preserved by goroutine
// locality — one worker owns one world.
func (r *raceRun) generate(ctx context.Context, i int) error {
	cfg, s := r.cfg, &r.slots[i]
	ordinal := i + 1
	cand := cfg.Candidates[i]
	dir := filepath.Join(r.raceDir, fmt.Sprintf("%03d", ordinal))

	r.repoMu.Lock()
	err := gitx.AddWorktree(cfg.Repo, dir, r.intent.Base.Commit)
	r.repoMu.Unlock()
	if err != nil {
		return fmt.Errorf("race: world %d: %w", ordinal, err)
	}
	s.dir, s.added = dir, true

	// The evidence directories are created BEFORE the world is opened: a
	// T1 container's mounts are fixed at open time, and the FIFO must live
	// in a directory the world can reach but not own.
	if s.evRoot, s.scratchRoot, err = r.worldEvidenceDirs(ordinal); err != nil {
		return err
	}
	wh, err := cfg.Backend.Open(ctx, dir, r.openOptsFor(s.evRoot, s.scratchRoot))
	if err != nil {
		return fmt.Errorf("race: world %d: %w", ordinal, err)
	}
	s.wh = wh

	// World.Context = CAS key of the literal prompt bytes for every
	// adapter (DP-3; for script the prompt IS the patch, decision 7).
	contextKey, err := cfg.CAS.Put([]byte(cand.Prompt))
	if err != nil {
		return fmt.Errorf("race: store context %d: %w", ordinal, err)
	}
	envNames := cand.Env // allowlisted NAMES only, never values (decision 14)
	if envNames == nil {
		envNames = []string{}
	}
	if err := r.appendEventLocked("agent.started", map[string]any{
		"adapter": r.adapter,
		"budget": map[string]any{
			"max_turns":     cand.Budget.MaxTurns,
			"max_usd_micro": cand.Budget.MaxUSDMicro,
			"max_wall_ms":   cand.Budget.MaxWall.Milliseconds(),
		},
		"context": contextKey,
		"env":     envNames,
		"intent":  cfg.Intent,
		"model":   cand.Model,
		"ordinal": ordinal,
	}); err != nil {
		return err
	}

	h, err := cfg.Adapter.Start(ctx, agent.RunSpec{
		WorldDir: dir,
		World:    wh,
		Prompt:   cand.Prompt,
		Model:    cand.Model,
		Budget:   cand.Budget,
		Env:      cand.Env,
	})
	if err != nil {
		return fmt.Errorf("race: world %d: %w", ordinal, err)
	}
	res, err := h.Wait()
	if err != nil {
		return fmt.Errorf("race: world %d: %w", ordinal, err)
	}

	// Post-run evidence capture — host-side and tier-independent (AG-3/
	// AG-4 ownership is structural: the bind mount means the host worktree
	// IS what the container saw). The worktree is the agent's write
	// surface, so a capture failure here is agent-induced damage —
	// evidence, not machinery failure (decision 10): the world is recorded
	// as CRASH, the error text preserved in the stderr artifact, and the
	// race continues. Only control-plane failures (CAS, ledger) abort.
	outcome := res.Outcome
	captureNote := ""
	captureFail := func(stage string, err error) {
		outcome = object.OutcomeCrash
		captureNote += "mvo: capture " + stage + ": " + err.Error() + "\n"
	}

	var patch []byte
	// Fallback when no post-run tree is capturable: record the base tree
	// the agent was handed (the CRASH outcome, not the tree, carries the
	// verdict; CRASH worlds get no oracle run and no receipt binds to this
	// tree).
	tree := r.intent.Base.Tree
	if err := gitx.VerifyWorktreeRepo(cfg.Repo, dir); err != nil {
		captureFail("git identity", err)
	} else {
		p, err := agent.Diff(dir, r.intent.Base.Tree)
		if err != nil {
			captureFail("diff", err)
		} else {
			patch = p
		}
		// Always the ACTUAL post-run tree: for a failed script apply,
		// git apply --index is atomic, so this equals the base tree.
		t, err := gitx.WriteTree(dir)
		if err != nil {
			captureFail("write-tree", err)
		} else {
			tree = t
		}
	}
	patchKey, err := cfg.CAS.Put(patch)
	if err != nil {
		return fmt.Errorf("race: store patch %d: %w", ordinal, err)
	}
	traceKey, err := cfg.CAS.Put(res.Transcript)
	if err != nil {
		return fmt.Errorf("race: store transcript %d: %w", ordinal, err)
	}
	stderrBytes := res.Stderr
	if captureNote != "" {
		stderrBytes = append(bytes.Clone(res.Stderr), captureNote...)
	}
	stderrKey, err := cfg.CAS.Put(stderrBytes)
	if err != nil {
		return fmt.Errorf("race: store stderr %d: %w", ordinal, err)
	}
	// agent.finished records the RUN's own terminal state; a capture
	// failure downgrades only the World's outcome (the run may have
	// completed while the worktree it left behind is unusable as
	// evidence — both facts are recorded honestly).
	if err := r.appendEventLocked("agent.finished", map[string]any{
		"context":    contextKey,
		"exit_code":  res.ExitCode,
		"intent":     cfg.Intent,
		"killed_by":  res.KilledBy,
		"num_turns":  res.NumTurns,
		"ordinal":    ordinal,
		"outcome":    res.Outcome,
		"session":    res.SessionID,
		"stderr":     stderrKey,
		"tokens_in":  res.Cost.TokensIn,
		"tokens_out": res.Cost.TokensOut,
		"transcript": traceKey,
		"usd_micro":  res.Cost.USDMicro,
		"wall_ms":    res.Cost.WallMS,
	}); err != nil {
		return err
	}

	env, err := wh.EnvDigest(cfg.CAS)
	if err != nil {
		return err
	}
	w := object.World{
		Schema:        object.SchemaWorld,
		Intent:        cfg.Intent,
		Tree:          tree,
		Env:           env,
		IsolationTier: wh.Tier(), // from the handle, never a package constant
		Producer: object.Producer{
			Adapter:      r.adapter,
			Model:        cand.Model, // the PINNED model (what we asked for)
			IdentityTier: "claimed",
			Role:         "generator",
		},
		Context: contextKey,
		Patch:   patchKey,
		// The size is recorded where it is known: patch_size_asc must be
		// evaluable by a pure decision function with no CAS access (M1e
		// decision 22).
		PatchBytes: int64(len(patch)),
		Trace:      traceKey,
		Cost:       res.Cost,
		Outcome:    outcome,
		CreatedAt:  nowRFC3339(),
	}
	dig, err := r.recordObjectLocked("world.created", w)
	if err != nil {
		return err
	}
	s.dig, s.world, s.run = dig, w, res
	return nil
}

// buildLadder instantiates the oracles the policy REQUIRES for ONE world,
// in ladder order (M1e decision 12): declared-but-unrequired oracles are
// never built and never run, because evidence waste is a measured PRD
// metric. The v0 dialect has exactly one rung — the supplied legacy oracle
// — since a v0 policy declares no instances at all.
//
// Per WORLD, not per race (M1f decision 18). The tree-guard needs the
// candidate's tree digest and its own evidence channel, both of which are
// per-world; the alternative was smuggling per-world state through the
// Oracle interface or a type assertion, and this is two lines with no I/O
// cost.
func (r *raceRun) buildLadder(s *slot) ([]ladderRung, error) {
	pol, cfg := r.pol, r.cfg
	if pol.Dialect == policy.DialectV0 {
		return []ladderRung{{name: policy.FamilySuite, oracle: cfg.LegacyOracle}}, nil
	}
	tier := object.TierT0Worktree
	if s.wh != nil {
		tier = s.wh.Tier()
	}
	rungs := make([]ladderRung, 0, len(pol.Required))
	for _, name := range pol.Required {
		spec, ok := pol.OracleByName(name)
		if !ok {
			// Unreachable: validation refused a gate, key or requirement
			// naming an undeclared oracle.
			return nil, fmt.Errorf("race: policy %s requires oracle %q, which it does not declare", pol.Digest, name)
		}
		p := oracle.Params{
			Spec: spec, CAS: cfg.CAS, Timeout: r.oracleTimeout, Baseline: r.baseline,
			Paths: pol.Paths, Repo: cfg.Repo,
			// The guard's denominator in a race is the intent's base tree
			// (M1f decision 19): "did this patch touch the harness" is a
			// question about what the patch does to what is landing.
			BaseTree: r.intent.Base.Tree, CandidateTree: s.world.Tree,
			Regime: r.ev.regime, Crosscheck: r.ev.crosscheck, PluginAutoload: r.ev.autoload,
			PluginDir: r.ev.pluginDir, PluginDigest: r.ev.pluginDigest,
			InWorldPlugin: inWorldPath(tier, backend.InWorldPlugin, ""),
		}
		if s.evRoot != "" {
			p.EvidenceDir = filepath.Join(s.evRoot, spec.Kind)
			p.ScratchDir = filepath.Join(s.scratchRoot, spec.Kind)
			p.InWorldEvidence = inWorldPath(tier, backend.InWorldEvidence, spec.Kind)
			p.InWorldScratch = inWorldPath(tier, backend.InWorldScratch, spec.Kind)
		}
		if p.InWorldPlugin == "" {
			p.InWorldPlugin = r.ev.pluginDir
		}
		o, err := oracle.New(p)
		if err != nil {
			return nil, fmt.Errorf("race: %w", err)
		}
		rungs = append(rungs, ladderRung{name: name, oracle: o})
	}
	return rungs, nil
}

// verify is phase B for one COMPLETED world: the policy's oracle LADDER
// behind the world handle, each receipt bound to the exact world state (M0
// rule, unchanged) and recorded, stopping at the first failed hard gate
// (M1e decision 12) — gates are ordered, so a world that fails O0 never pays
// for O1, and no test-deleting candidate ever reaches a green suite. The
// handle is closed at the end (containers are transport).
func (r *raceRun) verify(ctx context.Context, i int) error {
	s := &r.slots[i]
	defer func() { _ = s.wh.Close() }() // idempotent; the deferred sweep is defense in depth
	rungs, err := r.buildLadder(s)
	if err != nil {
		return err
	}
	for _, rung := range rungs {
		rec, err := rung.oracle.Run(ctx, s.wh)
		if err != nil {
			return fmt.Errorf("race: oracle %s in %s: %w", rung.name, s.dir, err)
		}
		// The orchestrator alone knows the world digest and tree the receipt
		// attests to (valid_for = the world's exact {tree, env}: T1 receipts
		// bind the image digest through valid_for.env with zero new
		// plumbing).
		rec.World = s.dig
		rec.Freshness.ValidFor = object.ValidFor{Tree: s.world.Tree, Env: s.world.Env}
		dig, err := r.recordObjectLocked("receipt.recorded", rec)
		if err != nil {
			return err
		}
		s.receipts = append(s.receipts, object.RecordedReceipt{Digest: dig, Receipt: rec})
		if ladderStops(r.pol, s.receipts) {
			return nil
		}
	}
	return nil
}

// ladderStops reports whether this world's ladder has reached its first
// failed hard gate, walking pol.Gates in POLICY order over the receipts the
// world has accumulated so far.
//
// Policy order is the whole point. Gates may interleave oracles, and
// stopping merely because SOME gate naming the rung that just ran failed
// lets a later gate halt the ladder before an earlier gate's oracle has run
// at all — the trace then reports the never-run gate as the failure (it has
// no receipt) and the gate that actually failed as not-evaluated, inverting
// decision 12 and naming the wrong gate in the recorded rationale and the
// `mvo worlds` GATE column. A gate whose oracle has produced no receipt yet
// is not a failure; it is a rung still to climb.
func ladderStops(pol policy.Policy, receipts []object.RecordedReceipt) bool {
	for _, g := range pol.Gates {
		rec := ladderReceipt(g.Sel, receipts)
		if rec == nil {
			return false
		}
		if ok, _ := g.Eval(rec); !ok {
			return true
		}
	}
	return false
}

// ladderReceipt picks a selector's receipt out of the ones a world has
// accumulated: the smallest-digest match, the same order-independent
// disambiguation Decide's counted receipt uses. Binding is not re-checked
// here — the orchestrator sets valid_for from the world it just judged, so
// every receipt in this slice is bound by construction.
func ladderReceipt(sel policy.Selector, receipts []object.RecordedReceipt) *object.Receipt {
	best := ""
	var out *object.Receipt
	for i := range receipts {
		if !sel.Match(receipts[i].Receipt) {
			continue
		}
		if best == "" || receipts[i].Digest < best {
			best, out = receipts[i].Digest, &receipts[i].Receipt
		}
	}
	return out
}

// removeWorld removes one world worktree, falling back to plain directory
// removal plus a registration prune when `git worktree remove` refuses —
// a hostile agent can corrupt a worktree's git metadata, and by cleanup
// time every piece of evidence is already recorded, so a damaged world
// must not fail the race. Callers hold repoMu.
func removeWorld(repo, dir string) error {
	if err := gitx.RemoveWorktree(repo, dir); err == nil {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("race: cleanup %s: %w", dir, err)
	}
	if err := gitx.PruneWorktrees(repo); err != nil {
		return fmt.Errorf("race: cleanup: %w", err)
	}
	return nil
}

func (cfg Config) validate() error {
	switch {
	case cfg.Repo == "":
		return errors.New("race: config: empty repo")
	case cfg.Ledger == nil:
		return errors.New("race: config: nil ledger")
	case cfg.CAS == nil:
		return errors.New("race: config: nil CAS")
	case cfg.Intent == "":
		return errors.New("race: config: empty intent digest")
	case cfg.Adapter == nil:
		return errors.New("race: config: nil adapter")
	case len(cfg.Candidates) == 0:
		return errors.New("race: config: no candidates")
	case cfg.WorldsDir == "":
		return errors.New("race: config: empty worlds dir")
	case cfg.Backend == nil:
		return errors.New("race: config: nil backend")
	case cfg.Parallel < 1:
		return errors.New("race: config: parallel must be at least 1")
	}
	return nil
}
