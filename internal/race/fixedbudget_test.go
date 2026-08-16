package race

// M2b1: THE BUDGETED FIXED ARM through the real orchestrator — worktrees, the
// script adapter and the fake interpreter, no pytest, no plugin, no docker
// and no agent CLI.
//
// Three claims are under test here and nowhere else, because they are claims
// about the ORCHESTRATOR rather than about the allocation rule:
//
//   - the world order the scheduler ranks by is the CONTROL PLANE's, derived
//     from candidate ordinals and never from a world digest (M1f);
//   - `--schedule=fixed-budget` REFUSES an allocation-sensitive policy where
//     the adaptive arm falls back, because a silent fallback from a budgeted
//     arm to an unbudgeted one turns a matched-budget experiment into an
//     unmatched one;
//   - the arm is a real arm: it traces, it charges the same pool, and at a
//     binding budget it stops with S-budget and says which rungs it never
//     bought.

import (
	"context"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/schedule"
)

// THE WORLD ORDER IS THE ORCHESTRATOR'S. Slot k−1 is candidate k (M1c
// decision 15), so the order is slot order — and it must not move when the
// digests do. The test permutes the digests over the same slots and asserts
// the recorded order is unchanged, which is the property M1f actually
// requires: a candidate cannot buy itself a place in the queue by grinding
// its tree until the digest sorts low.
func TestWorldOrderIsControlPlaneNotDigestOrder(t *testing.T) {
	r := &raceRun{slots: make([]slot, 3)}
	r.slots[0].dig = "mv0:ccc"
	r.slots[1].dig = "mv0:aaa"
	r.slots[2].dig = "mv0:bbb"
	got := strings.Join(r.worldOrder(), ",")
	if got != "mv0:ccc,mv0:aaa,mv0:bbb" {
		t.Fatalf("world order = %s, want candidate ordinal ascending (slot order)", got)
	}

	// Same candidates, different trees, therefore different digests: the
	// ORDER is unchanged because it never read a digest.
	r2 := &raceRun{slots: make([]slot, 3)}
	r2.slots[0].dig = "mv0:zzz"
	r2.slots[1].dig = "mv0:yyy"
	r2.slots[2].dig = "mv0:xxx"
	order := r2.worldOrder()
	if order[0] != "mv0:zzz" || order[2] != "mv0:xxx" {
		t.Errorf("world order = %v; a digest-sorted order would have reversed it", order)
	}

	// A slot with no world (generation failed before the object was recorded)
	// contributes nothing: an empty digest is not a position in the queue.
	r3 := &raceRun{slots: make([]slot, 2)}
	r3.slots[1].dig = "mv0:only"
	if got := r3.worldOrder(); len(got) != 1 || got[0] != "mv0:only" {
		t.Errorf("world order = %v over one recorded world, want [mv0:only]", got)
	}
}

// DECISION 8: the budgeted arm REFUSES where the adaptive arm falls back.
// `wall_ms_asc` sums a world's counted receipts, so under ANY arm that can
// withhold, the world verified least wins the tiebreak. The adaptive arm
// silently races the exhaustive ladder instead; `fixed-budget` errors,
// because falling back would silently un-budget a budget-matched comparison.
func TestFixedBudgetRefusesAnAllocationSensitivePolicy(t *testing.T) {
	steerable := schedulablePolicy("python3")
	steerable.Oracles = append(steerable.Oracles, object.OracleSpec{
		Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{},
	})
	steerable.Escalation.RequireEvidence = []string{"guard"}
	steerable.Ranking = []string{policy.KeyGatePass, policy.KeyWallMSAsc}
	b, err := object.Canonical(steerable)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	compiled, err := policy.Decode(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	arm, named, err := scheduleArm(Config{Schedule: schedule.ScheduleFixedBudget}, compiled)
	if err == nil {
		t.Fatalf("the budgeted arm fell back to %q instead of refusing", arm)
	}
	if len(named) == 0 || named[0] != policy.KeyWallMSAsc {
		t.Errorf("the refusal names %v, want the offending key", named)
	}
	for _, want := range []string{"rule 25", policy.KeyWallMSAsc, "REFUSES"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}

	// The same policy under the adaptive arm falls back rather than refusing,
	// which is M2b's behaviour and stays exactly as it was: refusing there
	// would brick a legitimate M1 configuration.
	if arm, _, err := scheduleArm(Config{Schedule: schedule.ScheduleAdaptive}, compiled); err != nil || arm != schedule.ScheduleFixed {
		t.Errorf("the adaptive arm now %q/%v, want a silent fallback to %q", arm, err, schedule.ScheduleFixed)
	}
	// And a policy that only LOOKS steerable is refused by neither.
	safe := schedulablePolicy("python3")
	sb, err := object.Canonical(safe)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	safePol, err := policy.Decode(sb)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if arm, _, err := scheduleArm(Config{Schedule: schedule.ScheduleFixedBudget}, safePol); err != nil || arm != schedule.ScheduleFixedBudget {
		t.Errorf("a safe policy under the budgeted arm = %q/%v, want %q", arm, err, schedule.ScheduleFixedBudget)
	}
}

// THE REFERENCE ARM (decision 2): `fixed-budget` at max_oracle_ms = 0 buys
// exactly what `fixed` buys and decides identically — the M1 exhaustive
// ladder PLUS A TRACE, which is what makes evidence waste, spend and the
// cost-table snapshot computable for the reference at all.
func TestFixedBudgetAtZeroBudgetMatchesTheUntracedLadder(t *testing.T) {
	patches := map[string]string{"a-fix.patch": fixPatch, "b-nofix.patch": noFixPatch}
	run := func(arm string) (*Result, *collector) {
		t.Helper()
		cfg := newLadderConfig(t, schedulablePolicy(fakePython(t, true)), patches)
		cfg.Schedule = arm
		c := &collector{}
		cfg.ScheduleTrace = c
		res, err := Run(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Run(%s): %v", arm, err)
		}
		return res, c
	}
	fixed, fixedTrace := run(schedule.ScheduleFixed)
	budgeted, trace := run(schedule.ScheduleFixedBudget)

	if len(fixedTrace.started) != 0 {
		t.Error("--schedule=fixed recorded an allocation trace; it is the UNTRACED M1 ladder and that is why the reference arm exists")
	}
	if len(trace.started) != 1 {
		t.Fatalf("the budgeted arm recorded %d schedule.started, want 1", len(trace.started))
	}
	if trace.started[0].Schedule != schedule.ScheduleFixedBudget {
		t.Errorf("recorded arm = %q, want %q", trace.started[0].Schedule, schedule.ScheduleFixedBudget)
	}
	if trace.started[0].Selector != schedule.SelectorNameLadder {
		t.Errorf("recorded selector = %q, want %q", trace.started[0].Selector, schedule.SelectorNameLadder)
	}
	if len(trace.started[0].WorldOrder) != 2 {
		t.Errorf("recorded world order = %v, want one entry per world", trace.started[0].WorldOrder)
	}
	if trace.finished[0].Stop != schedule.StopEmpty {
		t.Errorf("stop = %q at an unbounded budget, want %q", trace.finished[0].Stop, schedule.StopEmpty)
	}

	if fixed.Decision.Type != budgeted.Decision.Type {
		t.Errorf("decision type: fixed %s, fixed-budget %s", fixed.Decision.Type, budgeted.Decision.Type)
	}
	if f, a := normalizeDigests(fixed), normalizeDigests(budgeted); f.rationale != a.rationale {
		t.Errorf("rationale differs:\n  fixed:        %s\n  fixed-budget: %s", f.rationale, a.rationale)
	}
	kinds := func(res *Result) map[int][]string {
		out := map[int][]string{}
		for _, w := range res.Worlds {
			for _, rr := range w.Receipts {
				out[w.Ordinal] = append(out[w.Ordinal], rr.Receipt.Oracle.ID)
			}
		}
		return out
	}
	f, a := kinds(fixed), kinds(budgeted)
	if len(f) != len(a) {
		t.Fatalf("world counts differ: %v vs %v", f, a)
	}
	for ord, want := range f {
		if strings.Join(a[ord], ",") != strings.Join(want, ",") {
			t.Errorf("candidate %d bought %v under fixed-budget, %v under fixed", ord, a[ord], want)
		}
	}
}

// THE ARM IS DEPTH-FIRST through the real orchestrator: candidate 1 completes
// its ladder before candidate 2 buys anything. That is the property the whole
// block exists to give the experiment — the adaptive rule degenerates to
// round robin on symmetric worlds, advances every world one rung and
// completes none, and a comparison needs an arm that does the other thing.
func TestFixedBudgetIsDepthFirstThroughTheOrchestrator(t *testing.T) {
	cfg := newLadderConfig(t, schedulablePolicy(fakePython(t, true)), map[string]string{
		"a-fix.patch":   fixPatch,   // collects 2 → the whole ladder runs
		"b-nofix.patch": noFixPatch, // collects 1 → the ladder stops at gate 2
	})
	cfg.Schedule = schedule.ScheduleFixedBudget
	c := &collector{}
	cfg.ScheduleTrace = c
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	order := c.started[0].WorldOrder
	if len(order) != 2 {
		t.Fatalf("world order = %v, want two worlds", order)
	}
	var seq []string
	for _, s := range c.steps {
		for _, ch := range s.Chosen {
			seq = append(seq, ch.World)
		}
	}
	if len(seq) < 3 {
		t.Fatalf("the race bought %d rungs; too few to show depth-first order (%v)", len(seq), res.Decision.Type)
	}
	// Every purchase for the head world precedes every purchase for the
	// second: no interleaving at all at k = 1.
	firstOther := len(seq)
	for i, w := range seq {
		if w != order[0] {
			firstOther = i
			break
		}
	}
	for _, w := range seq[firstOther:] {
		if w == order[0] {
			t.Fatalf("the arm returned to the head world after leaving it; that is round robin, not depth first: %v", seq)
		}
	}
	// And each Chosen row says WHY in the arm's own words rather than the
	// VOC arm's.
	for _, s := range c.steps {
		for _, ch := range s.Chosen {
			if ch.Reason != schedule.ReasonLadderOrder {
				t.Errorf("purchase reason = %q, want the ladder's own sentence", ch.Reason)
			}
		}
	}
}

// A BINDING BUDGET binds this arm too, which is the entire point: it stops on
// S-budget, it leaves a world holding a strict ladder prefix, and every rung
// it never bought carries an oracle.skipped. Under M2a's purchase law an
// unpurchased hard gate is an ABSENT required metric, so the starved world
// cannot pass — the arm invents no "skipped, assume fine" state.
func TestFixedBudgetStopsWhenTheMoneyRunsOut(t *testing.T) {
	patches := map[string]string{"a-fix.patch": fixPatch, "b-nofix.patch": noFixPatch}
	pol := schedulablePolicy(fakePython(t, true))
	cfg := newLadderConfig(t, pol, patches)
	cfg.Schedule = schedule.ScheduleFixedBudget
	cfg.Intent = seedIntentBudget(t, cfg.CAS, cfg.Repo, pol, len(patches), 1)
	c := &collector{}
	cfg.ScheduleTrace = c
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	fin := c.finished[0]
	if fin.Stop != schedule.StopBudget {
		t.Fatalf("stop = %q at a 1 ms budget, want %q", fin.Stop, schedule.StopBudget)
	}
	if fin.Violation != "" {
		t.Errorf("finished.violation = %q, and it must always be empty", fin.Violation)
	}
	if len(c.skipped) == 0 {
		t.Fatal("a starved race recorded no oracle.skipped rows; the operator cannot see what was never found out")
	}
	for _, sk := range c.skipped {
		if !strings.Contains(sk.Reason, "budget") && !strings.Contains(sk.Reason, "pool") {
			t.Errorf("skip reason %q names neither the budget nor the pool", sk.Reason)
		}
	}
	// The starved world holds a STRICT prefix: something it needed was never
	// bought, so it is not in the pass set and Decide cannot name it Subject.
	required := len(mustPolicy(t, cfg).Required)
	starved := 0
	for _, w := range res.Worlds {
		if len(w.Receipts) < required {
			starved++
		}
	}
	if starved == 0 {
		t.Error("no world holds a strict ladder prefix; the budget did not bind")
	}
	if res.Decision.Type == TypeSelect {
		for _, w := range res.Worlds {
			if w.Digest == res.Decision.Subject[0] && len(w.Receipts) < required {
				t.Error("a world that never bought its whole ladder was SELECTED; the purchase law is broken")
			}
		}
	}
	// selection_us is measured and reported, and it is NOT charged: the
	// spend is oracle milliseconds alone (F8).
	if fin.SelectionUS < 0 {
		t.Errorf("selection_us = %d", fin.SelectionUS)
	}
}

// Both arms are charged IDENTICALLY: one budget object, one affordability
// predicate, one charge point, and the same recorded budget in
// schedule.started. A comparison whose arms hold different budgets is not a
// comparison, and this is the field the harness asserts on.
func TestBothArmsRecordTheSameBudgetAndBasis(t *testing.T) {
	patches := map[string]string{"a-fix.patch": fixPatch, "b-nofix.patch": noFixPatch}
	pol := schedulablePolicy(fakePython(t, true))
	run := func(arm string) schedule.Started {
		t.Helper()
		cfg := newLadderConfig(t, pol, patches)
		cfg.Schedule = arm
		cfg.Intent = seedIntentBudget(t, cfg.CAS, cfg.Repo, pol, len(patches), 5000)
		c := &collector{}
		cfg.ScheduleTrace = c
		if _, err := Run(context.Background(), cfg); err != nil {
			t.Fatalf("Run(%s): %v", arm, err)
		}
		return c.started[0]
	}
	a := run(schedule.ScheduleAdaptive)
	b := run(schedule.ScheduleFixedBudget)
	if a.Budget.MaxOracleMS != b.Budget.MaxOracleMS {
		t.Errorf("budgets differ: adaptive %d, fixed-budget %d", a.Budget.MaxOracleMS, b.Budget.MaxOracleMS)
	}
	if a.Budget.MaxOracleMS != 5000 {
		t.Errorf("recorded budget = %d, want the intent's 5000", a.Budget.MaxOracleMS)
	}
	if a.BudgetBasis != b.BudgetBasis {
		t.Errorf("bases differ: adaptive %q, fixed-budget %q", a.BudgetBasis, b.BudgetBasis)
	}
	if a.Parallel != b.Parallel {
		t.Errorf("dispatch degrees differ: adaptive %d, fixed-budget %d", a.Parallel, b.Parallel)
	}
	if strings.Join(a.WorldOrder, ",") == "" || strings.Join(b.WorldOrder, ",") == "" {
		t.Error("an arm recorded no world order; the two runs cannot be paired")
	}
	if a.Selector == b.Selector {
		t.Errorf("both arms recorded selector %q; they are supposed to differ in exactly this", a.Selector)
	}
}
