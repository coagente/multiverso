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
)

// Candidate is one world's generation spec (AG-5): the literal prompt, a
// pinned model, and the per-world budget the adapter enforces (AG-2).
type Candidate struct {
	Prompt string       // literal prompt text (script: patch bytes as a string)
	Model  string       // model pin; "" = tool default
	Budget agent.Budget // per-world budget (AG-2)
	Env    []string     // extra parent env var NAMES passed through (names only, decision 14)
}

// Config wires one race run. All fields are required except KeepWorlds.
type Config struct {
	Repo       string          // git repo root; worlds are worktrees of it
	Ledger     *ledger.Ledger  // event log
	CAS        *cas.Store      // object + artifact store
	Intent     string          // intent digest ("mv0:...") already in CAS
	Adapter    agent.Adapter   // generator for every world (AG-1)
	Candidates []Candidate     // one world per candidate, len ≥ 1 (CP-2)
	WorldsDir  string          // parent directory for world worktrees
	Oracle     oracle.Oracle   // verifier run in each COMPLETED world
	Backend    backend.Backend // isolation provider (XP-1), required
	Parallel   int             // bounded workers, required ≥ 1; 1 = the M1b schedule
	KeepWorlds bool            // keep worktrees after the race (--keep-worlds; never containers)
}

// WorldRun is one world's trajectory through the race.
type WorldRun struct {
	Ordinal       int // 1-based candidate ordinal
	Dir           string
	Digest        string
	World         object.World
	Run           *agent.RunResult
	ReceiptDigest string          // "" for worlds that produced no receipt
	Receipt       *object.Receipt // nil for worlds that produced no receipt
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
	adapter string
	raceDir string

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
	dir        string
	added      bool          // worktree exists (cleanup owes a removal)
	wh         backend.World // non-nil once the backend opened the world
	dig        string
	world      object.World
	run        *agent.RunResult
	receipt    *object.Receipt
	receiptDig string
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
	policy, err := LoadPolicy(cfg.CAS, intent.Policy)
	if err != nil {
		return nil, err
	}
	// CP-2: the candidate count is hard-capped before race.started — an
	// over-budget race is refused, never recorded (closes the M0 TODO).
	if len(cfg.Candidates) > intent.Budget.MaxCandidates {
		return nil, fmt.Errorf("race: %d candidates exceed intent budget max_candidates=%d (CP-2)",
			len(cfg.Candidates), intent.Budget.MaxCandidates)
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
		adapter: cfg.Adapter.ID() + "@" + cfg.Adapter.Version(),
		slots:   make([]slot, len(cfg.Candidates)),
		cancel:  cancelRace,
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

	// race.started gains three always-present observational keys (M1c):
	// exec_tier, exec_image_digest ("" for T0), parallel. Audit reads only
	// "intent" — unaffected. Container IDs appear nowhere in the ledger:
	// containers are transport, the image digest is the evidence.
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
		"parallel":          cfg.Parallel,
	}); err != nil {
		os.Remove(r.raceDir)
		return nil, err
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
		runs = append(runs, WorldRun{
			Ordinal: i + 1, Dir: s.dir, Digest: s.dig, World: s.world,
			Run: s.run, Receipt: s.receipt, ReceiptDigest: s.receiptDig,
		})
		byWorld[i] = i
	}
	sort.Slice(byWorld, func(a, b int) bool {
		return r.slots[byWorld[a]].dig < r.slots[byWorld[b]].dig
	})
	worlds := make([]object.World, 0, len(r.slots))
	for _, i := range byWorld {
		worlds = append(worlds, r.slots[i].world)
	}
	var byReceipt []int
	for i := range r.slots {
		if r.slots[i].receipt != nil {
			byReceipt = append(byReceipt, i)
		}
	}
	sort.Slice(byReceipt, func(a, b int) bool {
		return r.slots[byReceipt[a]].receiptDig < r.slots[byReceipt[b]].receiptDig
	})
	receipts := make([]object.Receipt, 0, len(byReceipt))
	for _, i := range byReceipt {
		receipts = append(receipts, *r.slots[i].receipt)
	}

	decision := Decide(policy, worlds, receipts)
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

	wh, err := cfg.Backend.Open(ctx, dir)
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
		Context:   contextKey,
		Patch:     patchKey,
		Trace:     traceKey,
		Cost:      res.Cost,
		Outcome:   outcome,
		CreatedAt: nowRFC3339(),
	}
	dig, err := r.recordObjectLocked("world.created", w)
	if err != nil {
		return err
	}
	s.dig, s.world, s.run = dig, w, res
	return nil
}

// verify is phase B for one COMPLETED world: oracle behind the world
// handle, freshness bound to the exact world state (M0 rule, unchanged),
// receipt.recorded, then the handle is closed (containers are transport).
func (r *raceRun) verify(ctx context.Context, i int) error {
	cfg, s := r.cfg, &r.slots[i]
	rec, err := cfg.Oracle.Run(ctx, s.wh)
	if err != nil {
		return fmt.Errorf("race: oracle in %s: %w", s.dir, err)
	}
	// The orchestrator alone knows the world digest and tree the receipt
	// attests to (valid_for = the world's exact {tree, env}: T1 receipts
	// bind the image digest through valid_for.env with zero new plumbing).
	rec.World = s.dig
	rec.Freshness.ValidFor = object.ValidFor{Tree: s.world.Tree, Env: s.world.Env}
	dig, err := r.recordObjectLocked("receipt.recorded", rec)
	if err != nil {
		return err
	}
	s.receipt, s.receiptDig = &rec, dig
	_ = s.wh.Close() // idempotent; the deferred sweep is defense in depth
	return nil
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
	case cfg.Oracle == nil:
		return errors.New("race: config: nil oracle")
	case cfg.Backend == nil:
		return errors.New("race: config: nil backend")
	case cfg.Parallel < 1:
		return errors.New("race: config: parallel must be at least 1")
	}
	return nil
}
