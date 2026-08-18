package race

// PHASE B, SCHEDULED (M2b §10). The per-world verification loop becomes an
// allocation: compute the frontier, score it, dispatch the top-k affordable
// purchases concurrently, record each receipt exactly as before, re-evaluate.
//
// Two things do NOT change, and they are the reason this is a scheduling
// change rather than an evidence change:
//
//   - The scheduler decides WHETHER a rung runs, never HOW. Receipts are
//     produced by the same oracles, bound by the same code (rec.World,
//     rec.Freshness.ValidFor) and recorded by the same ledger path.
//   - `Decide` is untouched. The scheduler holds a REFERENCE to it and asks
//     it questions; it owns no opinion about what evidence means.
//
// Under max_oracle_ms == 0 the loop is equivalent to the M1 exhaustive
// ladder: every rung of every alive world is affordable, every frontier
// purchase that a gate, key or escalation rule reads scores flip = 1, and
// the ladder's short-circuit is preserved verbatim by the frontier rule
// (decision 2) — a world may not buy rung k+1 before rung k, and a world
// that failed a gate is not alive.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/schedule"
)

// ScheduleTrace is the RECORDING seam, defined by the consumer so the
// orchestrator names the three calls it makes and nothing more.
// *schedule.Recorder satisfies it. A nil trace records nothing and changes
// no allocation: the scheduler's product is observational, so a race that
// records none of it decides exactly the same way (decision 17).
type ScheduleTrace interface {
	Started(schedule.Started) error
	Step(schedule.Step) error
	Finished(schedule.Finished, []schedule.Skipped) error
}

// scheduleArm resolves which arm this race runs.
//
// The library falls back to the exhaustive ladder — it does not refuse —
// when the pinned policy's ranking is ALLOCATION-SENSITIVE (validation rule
// 25, M2b decision 15): `wall_ms_asc` sums a world's counted receipts, so
// under adaptive allocation the world we verified LEAST wins the tiebreak.
// Refusing here would brick a legitimate M1 configuration, which is why the
// rule is a `mvo race` PRE-FLIGHT refusal naming the key and the operator's
// two outs; the library's job is to make sure that a policy which slipped
// past pre-flight is never scheduled adaptively anyway. The arm is recorded
// in schedule.started, so the fallback is visible rather than silent.
// BOTH halves of decision 15's definition are required, and the second is
// the one that keeps M0's `--oracle-cmd` path alive: a key is
// allocation-sensitive iff its value can change when a receipt is withheld
// from a world THAT STILL PASSES EVERY HARD GATE, and withholding a
// hard-gated receipt drops the world out of the pass set entirely. So the
// fallback needs a sensitive key AND evidence the policy counts that no hard
// gate backs. cmd/mvo's pre-flight refusal reads the same two predicates, so
// the CLI and the library cannot disagree about which policies are safe.
// M2b1 decision 8 widens the rule to any arm that can WITHHOLD, and splits
// the two arms' responses. Where the adaptive arm falls back, `fixed-budget`
// ERRORS: a silent fallback from a budgeted arm to an unbudgeted one turns a
// matched-budget experiment into an unmatched one and records nothing that
// says so — which is precisely the failure M2b1 exists to correct.
func scheduleArm(cfg Config, pol policy.Policy) (string, []string, error) {
	sensitive := pol.AllocationSensitiveKeys()
	if len(sensitive) > 0 && len(pol.UngatedEvidence()) == 0 {
		sensitive = nil
	}
	switch cfg.Schedule {
	case schedule.ScheduleFixed:
		return schedule.ScheduleFixed, sensitive, nil
	case schedule.ScheduleFixedBudget:
		if len(sensitive) > 0 {
			return "", sensitive, fmt.Errorf("race: policy %s ranks by %s AND counts evidence no hard gate backs (%s), which cannot be raced under --schedule=%s (M2b validation rule 25, M2b1 decision 8): a budgeted arm withholds, the key rewards the world verified least, and this arm REFUSES rather than falling back to the unbudgeted ladder — a silent fallback would turn a matched-budget comparison into an unmatched one",
				pol.Digest, strings.Join(sensitive, ","), strings.Join(pol.UngatedEvidence(), ","),
				schedule.ScheduleFixedBudget)
		}
		return schedule.ScheduleFixedBudget, nil, nil
	}
	if len(sensitive) > 0 {
		return schedule.ScheduleFixed, sensitive, nil
	}
	return schedule.ScheduleAdaptive, nil, nil
}

// selectorFor maps the recorded arm and the requested rule to decision 1's
// selector. It is the ONE place the arms diverge in this package: everything
// downstream — the frontier, the budget, the charge point, the dispatch, the
// trace — is the same code for all three.
//
// THE PUBLISHED RULE IS RETAINED AND SELECTABLE (M2b.2 decision 6), AND IT IS
// STILL THE DEFAULT. M2d convicted `voc` of losing reachable admissions it had
// the money for, and `voc2` fixes that — but under the shipped default policy
// it fixes it by admitting where the full-evidence decision escalates, at every
// budget in the informative band, and no gate in this repository measures it
// (M2b.2 §7.6). So the revision is one flag away rather than the default, and
// the before/after stays a paired comparison under ONE binary, which is why
// neither rule was deleted or upgraded in place.
func selectorFor(arm, rule string) (schedule.Selector, error) {
	if arm == schedule.ScheduleFixedBudget {
		if rule != "" && rule != schedule.SelectorNameLadder {
			return nil, fmt.Errorf("race: --selector=%s applies to --schedule=%s only; --schedule=%s IS the depth-first ladder and reserves nothing",
				rule, schedule.ScheduleAdaptive, schedule.ScheduleFixedBudget)
		}
		return schedule.SelectorLadder(), nil
	}
	switch rule {
	case "":
		// The BINARY's default rule, read from the same constant the trace
		// records as `adaptive_rule` — so a race, its ledger and the eval
		// freeze check cannot disagree about which rule this build allocates
		// by.
		return selectorFor(arm, schedule.AdaptiveRule())
	case schedule.SelectorNameVOC:
		return schedule.SelectorVOC(), nil
	case schedule.SelectorNameVOC2:
		return schedule.SelectorVOC2(), nil
	default:
		return nil, fmt.Errorf("race: --selector must be %s or %s (got %q)",
			schedule.SelectorNameVOC2, schedule.SelectorNameVOC, rule)
	}
}

// worldOrder is the CONTROL-PLANE world order (M2b1 decision 3): candidate
// ordinal ascending, which is slot order, because slot k−1 is candidate k by
// construction (M1c decision 15).
//
// It is computed HERE and handed to the scheduler, never derived there, and
// it is emphatically not world-digest order. A world digest is a function of
// candidate-authored bytes, and under a binding budget the verification order
// decides who is verified at all — so ordering on the digest would hand a
// candidate a lever on whether its rivals are ever measured, which M1f
// forbids absolutely. It is also not stable across runs (world.created_at,
// world.cost and the transcript digest are in the pre-image), so digest order
// would be randomization without a recorded seed.
func (r *raceRun) worldOrder() []string {
	out := make([]string, 0, len(r.slots))
	for i := range r.slots {
		if r.slots[i].dig != "" {
			out = append(out, r.slots[i].dig)
		}
	}
	return out
}

// verifyAll is phase B: the scheduled allocation, or the M1 fixed ladder
// when the arm says so.
func (r *raceRun) verifyAll(ctx context.Context, completed []int) error {
	arm, _, err := scheduleArm(r.cfg, r.pol)
	if err != nil {
		return err
	}
	if arm == schedule.ScheduleFixed {
		r.pool(completed, func(i int) error { return r.verify(ctx, i) })
		return r.failed()
	}
	return r.scheduledVerify(ctx, completed, arm)
}

// scheduledVerify runs phase B under the M2b scheduler — either arm. The loop
// below is decision 1 in force: it does not know which selector it is running.
func (r *raceRun) scheduledVerify(ctx context.Context, completed []int, arm string) error {
	// The oracles are built ONCE per world, exactly as the fixed ladder
	// builds them, and the scheduler never sees them: it allocates over the
	// policy's declared instances and hands back names.
	rungs := make(map[string][]ladderRung, len(completed))
	slotOf := make(map[string]int, len(completed))
	worlds := make([]object.RecordedWorld, 0, len(r.slots))
	for i := range r.slots {
		if r.slots[i].dig == "" {
			continue
		}
		worlds = append(worlds, object.RecordedWorld{Digest: r.slots[i].dig, World: r.slots[i].world})
	}
	for _, i := range completed {
		s := &r.slots[i]
		built, err := r.buildLadder(s)
		if err != nil {
			return err
		}
		rungs[s.dig] = built
		slotOf[s.dig] = i
	}

	// The CONTROL-PLANE measurement, not the gate denominator: every policy
	// that runs pytest has one, including the policies with no
	// collected-not-below gate, which is where the clamp used to lapse.
	bounds := schedule.Bounds{CollectedBase: r.collectedBase}
	corpusDigest := ""
	if r.corpus != nil {
		bounds.CorpusCases = int64(len(r.corpus.corpus.Cases))
		corpusDigest = r.corpus.digest
	}
	sel, err := selectorFor(arm, r.cfg.Selector)
	if err != nil {
		return err
	}
	sch, err := schedule.New(schedule.Config{
		Policy:       r.pol,
		Decide:       Decide,
		Costs:        schedule.NewTable(r.costSamples(), r.ev.autoload, bounds),
		Bounds:       bounds,
		BudgetMS:     r.intent.Budget.MaxOracleMS,
		Batch:        r.cfg.Parallel,
		CollectInert: r.cfg.CollectInert,
		CorpusDigest: corpusDigest,
		Selector:     sel,
		Order:        r.worldOrder(),
		Rotation:     r.cfg.Rotation,
		BudgetBasis:  r.cfg.BudgetBasis,
	}, worlds)
	if err != nil {
		return fmt.Errorf("race: %w", err)
	}
	if err := r.traceStarted(sch.Started(r.cfg.Intent, arm, r.cfg.Parallel)); err != nil {
		return err
	}

	for {
		step, more := sch.Next()
		if step.Step > 0 {
			if err := r.traceStep(step); err != nil {
				return err
			}
		}
		if !more {
			break
		}
		if err := r.dispatch(ctx, step.Chosen, rungs, slotOf, sch); err != nil {
			return err
		}
		r.closeSpent(completed, sch)
	}
	r.closeSpent(completed, sch)

	// The assertion that must never fire (decision 9). A SELECT over a world
	// that did not pay for every hard gate would mean the decision rule
	// admitted an unpurchased gate, which is a broken decision rule and not
	// a scheduling bug — so the race aborts rather than records it.
	if v := sch.PurchaseLaw(); v != "" {
		return fmt.Errorf("race: %s", v)
	}
	return r.traceFinished(sch.Finish(), sch.Skipped())
}

// dispatch runs one batch concurrently and records its receipts. The batch
// is at most cfg.Parallel purchases, which is the same bound the M1c worker
// pool enforced on worlds.
func (r *raceRun) dispatch(ctx context.Context, chosen []schedule.Chosen,
	rungs map[string][]ladderRung, slotOf map[string]int, sch *schedule.Scheduler) error {

	type result struct {
		rr  object.RecordedReceipt
		err error
	}
	out := make([]result, len(chosen))
	var wg sync.WaitGroup
	for k, c := range chosen {
		rung, ok := findRung(rungs[c.World], c.Oracle)
		if !ok {
			// Unreachable: the scheduler allocates over the policy's own
			// Required list, which is what buildLadder instantiates.
			return fmt.Errorf("race: scheduler chose oracle %q, which world %s has no rung for", c.Oracle, c.World)
		}
		i := slotOf[c.World]
		wg.Add(1)
		go func(k, i int, rung ladderRung) {
			defer wg.Done()
			rec, err := r.runRung(ctx, &r.slots[i], rung)
			if err != nil {
				out[k] = result{err: err}
				return
			}
			dig, err := r.recordObjectLocked("receipt.recorded", rec)
			if err != nil {
				out[k] = result{err: err}
				return
			}
			out[k] = result{rr: object.RecordedReceipt{Digest: dig, Receipt: rec}}
		}(k, i, rung)
	}
	wg.Wait()
	for k := range out {
		if out[k].err != nil {
			return out[k].err
		}
		// Recorded in BATCH ORDER, which is the scheduler's own deterministic
		// order, so two runs of the same race hand the scheduler the same
		// receipts in the same sequence even though the goroutines finished
		// in whatever order they finished.
		i := slotOf[out[k].rr.Receipt.World]
		r.slots[i].receipts = append(r.slots[i].receipts, out[k].rr)
		sch.Record(out[k].rr)
	}
	return nil
}

// runRung runs ONE rung in one world and returns the bound receipt. It is
// the body the fixed ladder runs, lifted so the two paths cannot drift:
// same oracle, same observation handling, same corpus re-digest either side
// of a replay, same binding.
func (r *raceRun) runRung(ctx context.Context, s *slot, rung ladderRung) (object.Receipt, error) {
	var rec object.Receipt
	var err error
	if ob, ok := rung.oracle.(oracle.Observer); ok {
		if cerr := r.checkCorpusFile(s, "before"); cerr != nil {
			return rec, cerr
		}
		var obs oracle.Observation
		rec, obs, err = ob.Observe(ctx, s.wh)
		if err == nil {
			s.obs, s.obsOK = obs, true
			s.obsPass = rec.Result.Status == oracle.StatusPass
		}
		if cerr := r.checkCorpusFile(s, "after"); cerr != nil {
			return rec, cerr
		}
	} else {
		rec, err = rung.oracle.Run(ctx, s.wh)
	}
	if err != nil {
		return rec, fmt.Errorf("race: oracle %s in %s: %w", rung.name, s.dir, err)
	}
	// The orchestrator alone knows the world digest and tree the receipt
	// attests to (valid_for = the world's exact {tree, env}).
	rec.World = s.dig
	rec.Freshness.ValidFor = object.ValidFor{Tree: s.world.Tree, Env: s.world.Env}
	return rec, nil
}

// closeSpent closes the handle of every world that can buy nothing more.
// A world's isolation is done serving the moment it is eliminated or has
// bought its whole ladder, and holding a container open past that is cost
// with no evidence attached. Close is idempotent and the deferred sweep in
// Run is the backstop.
func (r *raceRun) closeSpent(completed []int, sch *schedule.Scheduler) {
	live := map[string]bool{}
	for _, p := range sch.Frontier() {
		live[p.World()] = true
	}
	for _, i := range completed {
		s := &r.slots[i]
		if s.wh == nil || live[s.dig] {
			continue
		}
		_ = s.wh.Close()
		s.wh = nil
	}
}

func findRung(rungs []ladderRung, name string) (ladderRung, bool) {
	for _, r := range rungs {
		if r.name == name {
			return r, true
		}
	}
	return ladderRung{}, false
}

// costSamples reads this workspace's own recorded receipts for the cost fit.
//
// A receipt is sampled only when THIS race's pinned policy declares the
// oracle instance that produced it. The reason is the seal: M2a amendment 27
// measured plugin autoloading as a 4.4x lever on fixed cost, so a sample
// whose seal cannot be attributed would be averaged into a population it
// does not belong to. A receipt from a policy this race does not hold is
// therefore EXCLUDED rather than attributed by guess — `mvo oracles` can
// afford the cross-policy attribution offline; a race cannot.
func (r *raceRun) costSamples() []schedule.Sample {
	configs := make(map[string]bool, len(r.pol.Oracles))
	for _, o := range r.pol.Oracles {
		configs[o.Config] = true
	}
	var recs []object.Receipt
	_ = r.cfg.Ledger.Scan(func(e ledger.Event) error {
		if e.Type != "receipt.recorded" {
			return nil
		}
		var rec object.Receipt
		if err := json.Unmarshal(e.Payload, &rec); err != nil {
			// A payload this binary cannot decode is not a measurement.
			// Skipping it costs one sample; failing the race over a
			// historical row would make an old ledger un-raceable.
			return nil
		}
		if !configs[rec.Oracle.Config] {
			return nil
		}
		recs = append(recs, rec)
		return nil
	})
	return schedule.SamplesFromReceipts(recs, r.ev.autoload)
}

func (r *raceRun) traceStarted(s schedule.Started) error {
	if r.cfg.ScheduleTrace == nil {
		return nil
	}
	return r.cfg.ScheduleTrace.Started(s)
}

func (r *raceRun) traceStep(s schedule.Step) error {
	if r.cfg.ScheduleTrace == nil {
		return nil
	}
	return r.cfg.ScheduleTrace.Step(s)
}

func (r *raceRun) traceFinished(f schedule.Finished, skipped []schedule.Skipped) error {
	if r.cfg.ScheduleTrace == nil {
		return nil
	}
	return r.cfg.ScheduleTrace.Finished(f, skipped)
}
