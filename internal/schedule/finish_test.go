package schedule

// THE FINISHING RULE UNDER TEST (M2b.2).
//
// The block's central claim is arithmetic and it is testable without a world,
// a container, a Python interpreter or an agent: at a budget that can finish
// ONE world and not two, the published rule advances everybody one rung and
// finishes nobody, and the revision finishes one. §1.1's dead stop — pool 943,
// finishing rung 689, share 471, both refused — is reproduced here to the
// millisecond and then shown to be unrepresentable under the revision.
//
// Everything here is pure: no ledger, no clock that matters, and a decision
// rule the test hands in.

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// fixedCosts builds a cost table whose prediction for each kind is EXACTLY the
// millisecond figure asked for: a population with no unit variance fits
// `median-fixed` (M2b decision 7d), so the fit is the median wall time and
// there is no per-unit term to move it.
func fixedCosts(t *testing.T, b Bounds, ms map[string]int64) *Table {
	t.Helper()
	kinds := make([]string, 0, len(ms))
	for k := range ms {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var samples []Sample
	for _, k := range kinds {
		for i := 0; i < MinSamples; i++ {
			samples = append(samples, Sample{Kind: k, Seal: policy.AutoloadOff, Units: b.CollectedBase, WallMS: ms[k]})
		}
	}
	return NewTable(samples, policy.AutoloadOff, b)
}

// §1.1's ladder, to the millisecond: guard + collect = 293, suite = 689, a
// world's whole ladder = 982, two worlds = 1 964 against a 1 529 ms budget.
var raceLadderMS = map[string]int64{
	policy.KindTreeGuard: 11, policy.KindPytestCollect: 282, policy.KindPytestSuite: 689,
}

const (
	raceBudgetMS = 1529 // B2, the published cell
	worldLadder  = 982  // 11 + 282 + 689
)

// drain runs a scheduler to its stop, recording a passing receipt for every
// purchase, and returns the steps and the purchases in order.
func drain(t *testing.T, pol policy.Policy, s *Scheduler, ws []object.RecordedWorld) ([]Step, [][2]string) {
	t.Helper()
	byDig := map[string]object.RecordedWorld{}
	for _, w := range ws {
		byDig[w.Digest] = w
	}
	var steps []Step
	var bought [][2]string
	for i := 0; i < 64; i++ {
		step, more := s.Next()
		if step.Step > 0 {
			steps = append(steps, step)
		}
		if !more {
			return steps, bought
		}
		for _, c := range step.Chosen {
			bought = append(bought, [2]string{c.World, c.Oracle})
			s.Record(passingReceipt(t, pol, byDig[c.World], c.Oracle, raceLadderMS[kindOf(pol, c.Oracle)]))
		}
	}
	t.Fatal("scheduler did not terminate in 64 steps")
	return nil, nil
}

func kindOf(pol policy.Policy, oracle string) string {
	if o, ok := pol.OracleByName(oracle); ok {
		return o.Kind
	}
	return ""
}

// completeWorlds counts the worlds holding a counted receipt for every hard
// gate. It is the quantity the whole block is about: under M2a's purchase law
// only a complete world can be `Subject`, so a race that finishes nobody
// rejects however much money it has left.
func completeWorlds(pol policy.Policy, s *Scheduler, ws []object.RecordedWorld) []string {
	var out []string
	for _, w := range ws {
		if len(UnpaidHardGates(pol, w, s.perWorld[w.Digest])) == 0 {
			out = append(out, w.Digest)
		}
	}
	sort.Strings(out)
	return out
}

// THE BLOCK'S REASON FOR EXISTING, as a paired race under one binary.
//
// The pool holds 1 529 ms. A world's whole ladder costs 982 ms. Two worlds
// cost 1 964, so the budget can finish exactly one world and not two.
//
//	voc   advances both worlds through guard and collect (586 ms), then offers
//	      each of them 943/2 = 471 ms against a 689 ms suite and refuses BOTH —
//	      to every world simultaneously, which is the one thing an
//	      apportionment rule must never do. 943 ms unspent, zero complete
//	      worlds, nothing can pass.
//	voc2  commits the pool to the prefix it can finish, buys that world's whole
//	      ladder, and finishes it.
func TestVOC2FinishesAWorldTheOldRuleSpreadAcrossEveryWorld(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	run := func(sel Selector) (*Scheduler, []Step, [][2]string) {
		s := newSched(t, pol, Config{
			Bounds: bounds, Costs: costs, BudgetMS: raceBudgetMS, Selector: sel,
			Order: []string{a.Digest, b.Digest},
			// The pool is charged the PINNED PREDICTION, so the comparison is
			// of allocation rules and not of two machines' load (M2b1
			// decision 5b).
			BudgetBasis: BudgetBasisPredicted,
		}, ws...)
		steps, bought := drain(t, pol, s, ws)
		return s, steps, bought
	}

	// --- the published rule, reproducing the published failure ------------
	voc, vocSteps, vocBought := run(SelectorVOC())
	if got := completeWorlds(pol, voc, ws); len(got) != 0 {
		t.Fatalf("voc finished %v; the fixture does not reproduce the failure it exists to reproduce", got)
	}
	if len(vocBought) != 4 {
		t.Errorf("voc bought %d receipts, want 4 (both guards, both collects)", len(vocBought))
	}
	if f := voc.Finish(); f.Budget.SpentMS != 586 {
		t.Errorf("voc spent %d ms of %d, want §1.1's 586", f.Budget.SpentMS, raceBudgetMS)
	}
	refused := 0
	for _, r := range vocSteps[len(vocSteps)-1].Considered {
		if r.Oracle == "suite" && strings.Contains(r.Declined, "share") {
			refused++
		}
	}
	if refused != 2 {
		t.Errorf("voc refused %d of 2 finishing purchases on a share; the round robin is not the one M2d priced", refused)
	}

	// --- the revision ------------------------------------------------------
	voc2, steps, bought := run(SelectorVOC2())
	done := completeWorlds(pol, voc2, ws)
	if len(done) != 1 {
		t.Fatalf("voc2 finished %d worlds, want exactly 1 (the pool can pay for one ladder and not two): bought %v",
			len(done), bought)
	}
	// F-1, as an assertion: the money was there, and a whole ladder of it went
	// to a world the pool could actually finish. What the arm does with the
	// remainder afterwards is decision 5's qualifier — with the commit set
	// empty (nothing left is finishable) M2b decision 3c stands and the
	// remaining hard gates are bought while the pool can pay for them.
	f := voc2.Finish()
	if f.Budget.SpentMS < worldLadder {
		t.Errorf("voc2 spent %d ms, want at least one full ladder (%d)", f.Budget.SpentMS, worldLadder)
	}
	if f.Budget.SpentMS > raceBudgetMS {
		t.Errorf("voc2 spent %d ms against a %d ms bound", f.Budget.SpentMS, raceBudgetMS)
	}
	scarce, committed := 0, 0
	for _, st := range steps {
		if !st.Scarce {
			continue
		}
		scarce++
		if st.CommitBasis != CommitBasisReserved {
			t.Errorf("step %d is scarce and records commit_basis %q", st.Step, st.CommitBasis)
		}
		if len(st.CommitSet) == 0 {
			// The LAST step: the head world is finished, 547 ms remain, and
			// they cannot finish the rival's 982 ms ladder — so C is empty and
			// the equal-share rule is back, which is decision 3's degenerate
			// case rather than a missing commitment.
			continue
		}
		committed++
		if len(st.CommitSet) != 1 || st.CommitSet[0] != done[0] {
			t.Errorf("step %d commit_set = %v, want exactly the world that was finished (%s)",
				st.Step, st.CommitSet, done[0])
		}
	}
	if scarce == 0 {
		t.Fatal("no step recorded scarce: true; the fixture never reached the regime under test")
	}
	if committed == 0 {
		t.Fatal("no step committed to a world; the reservation never ran")
	}
	// And the rival is not silently forgotten. It holds a STRICT PREFIX of the
	// ladder — it was never finishable — and every rung it will never buy
	// carries an oracle.skipped naming the arithmetic that refused it. "Skipped,
	// assume fine" is not a state and never will be.
	rival := a.Digest
	if done[0] == a.Digest {
		rival = b.Digest
	}
	if len(UnpaidHardGates(pol, byDigest(ws, rival), voc2.perWorld[rival])) == 0 {
		t.Fatalf("the rival paid for every hard gate; the fixture does not starve anybody")
	}
	skipped := map[string]string{}
	for _, sk := range voc2.Skipped() {
		skipped[sk.World+"/"+sk.Oracle] = sk.Reason
	}
	for _, name := range []string{"guard", "collect", "suite"} {
		if voc2.bought[rival][name] {
			continue
		}
		reason, ok := skipped[rival+"/"+name]
		if !ok {
			t.Errorf("the unmeasured rival's rung %q has no oracle.skipped row (skips: %v)", name, skipped)
			continue
		}
		if !moneyDecline(reason) && !strings.Contains(reason, "budget") &&
			!strings.Contains(reason, "pool") {
			t.Errorf("rung %q was skipped with %q, which names neither the reservation nor the budget", name, reason)
		}
	}
}

// byDigest is the world with this digest, for an assertion that reads the
// purchase law over one of them.
func byDigest(ws []object.RecordedWorld, digest string) object.RecordedWorld {
	for _, w := range ws {
		if w.Digest == digest {
			return w
		}
	}
	return object.RecordedWorld{}
}

// THE COMMITMENT INVARIANT, as a property rather than as prose: for every step
// and every committed world, the world's next rung is AFFORDABLE. §1.1's dead
// stop is not made unlikely by the revision, it is made unrepresentable.
//
// And its consequence, which is F-1 stated as an observation: if the pool can
// finish ANY alive world's remaining ladder, the race finishes at least one.
// One occurrence of the opposite is a failure — not "rare", not "an edge case".
func TestCommitmentInvariantHoldsAtEveryStep(t *testing.T) {
	pol := testPolicy(t)
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)
	tags := []string{"a", "b", "c", "d"}

	for _, n := range []int{1, 2, 3, 4} {
		for _, budget := range []int64{1, 11, 293, 700, 982, 983, 1529, 1964, 2946, 4000} {
			ws := make([]object.RecordedWorld, 0, n)
			order := make([]string, 0, n)
			for _, tag := range tags[:n] {
				w := world(t, tag)
				ws = append(ws, w)
				order = append(order, w.Digest)
			}
			s := newSched(t, pol, Config{
				Bounds: bounds, Costs: costs, BudgetMS: budget, Selector: SelectorVOC2(),
				Order: order, BudgetBasis: BudgetBasisPredicted,
			}, ws...)
			steps, _ := drain(t, pol, s, ws)
			for _, st := range steps {
				for _, r := range st.Considered {
					if r.Committed && !r.Affordable {
						t.Fatalf("n=%d budget=%d step=%d: committed world %s could not afford %s (%d ms) — the commitment invariant does not hold",
							n, budget, st.Step, r.World, r.Oracle, r.CostMS)
					}
				}
			}
			if budget >= worldLadder && len(completeWorlds(pol, s, ws)) == 0 {
				t.Fatalf("n=%d budget=%d: the pool could pay for a whole ladder (%d ms) and no world was finished",
					n, budget, worldLadder)
			}
		}
	}
}

// DECISION 1'S GATE, at its exact boundary. `scarce` is a STRICT inequality:
// a pool that exactly covers every alive world's remaining ladder is not
// scarce, and M2b's rule allocates it.
func TestScarcityTestIsStrictAtExactEquality(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	for _, tc := range []struct {
		budget int64
		scarce bool
	}{
		{2 * worldLadder, false}, // exactly enough for both ladders
		{2*worldLadder - 1, true},
		{0, false}, // unbounded: there is no remainder to be short of
	} {
		s := newSched(t, pol, Config{
			Bounds: bounds, Costs: costs, BudgetMS: tc.budget, Selector: SelectorVOC2(),
			Order: []string{a.Digest, b.Digest}, BudgetBasis: BudgetBasisPredicted,
		}, ws...)
		step, _ := s.Next()
		if step.Scarce != tc.scarce {
			t.Errorf("budget %d: scarce = %v, want %v (Σ finish = %d)",
				tc.budget, step.Scarce, tc.scarce, 2*worldLadder)
		}
		wantBasis := CommitBasisNotScarce
		if tc.scarce {
			wantBasis = CommitBasisReserved
		}
		if step.CommitBasis != wantBasis {
			t.Errorf("budget %d: commit_basis = %q, want %q", tc.budget, step.CommitBasis, wantBasis)
		}
	}
}

// DECISION 1'S FALLBACK. A fix whose mechanism is arithmetic over predicted
// costs has no business inventing a cost: when any buyable kind is priced at
// declared rank, finish_ms is UNKNOWN, the scarcity test is undecidable, and
// the whole race falls back to M2b's rule — recorded, with the kinds named.
func TestUnpricedKindFallsTheWholeRaceBackToTheOldRule(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	// Everything priced but the suite — the kind whose cost dominates.
	costs := fixedCosts(t, bounds, map[string]int64{
		policy.KindTreeGuard: 11, policy.KindPytestCollect: 282,
	})
	s := newSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: raceBudgetMS, Selector: SelectorVOC2(),
		Order: []string{a.Digest, b.Digest},
	}, ws...)
	step, _ := s.Next()
	if step.Scarce {
		t.Error("the scarcity test returned an answer for a race whose finish cost is unknown")
	}
	if !strings.HasPrefix(step.CommitBasis, CommitBasisUnpriced) {
		t.Errorf("commit_basis = %q, want the %s regime", step.CommitBasis, CommitBasisUnpriced)
	}
	if !strings.Contains(step.CommitBasis, policy.KindPytestSuite) {
		t.Errorf("commit_basis = %q, want the unpriced kind NAMED", step.CommitBasis)
	}
	for _, r := range step.Considered {
		if r.FinishMS != 0 {
			t.Errorf("row %s/%s reports finish_ms = %d under an unknown finish cost; 0 means unknown and a number would be an invention",
				r.World, r.Oracle, r.FinishMS)
		}
		if r.ScoreBasis != ScoreBasisRung {
			t.Errorf("row %s/%s priced by %q under the fallback, want M2b's own denominator", r.World, r.Oracle, r.ScoreBasis)
		}
	}
}

// THE RETAINED ARM IS M2b, PROVABLY: `SelectorVOC.Allowances` is bit-identical
// to `budget.share(len(frontier))` and `SelectorLadder.Allowances` to
// `budget.share(1)`, for every frontier and every pool. The reference arm of
// every published comparison did not move.
func TestRetainedArmsAllowancesAreBitIdenticalToTheShare(t *testing.T) {
	pol := testPolicy(t)
	ws := []object.RecordedWorld{world(t, "a"), world(t, "b"), world(t, "c")}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	for _, pool := range []int64{0, 1, 300, 982, 1529, 100000} {
		for _, tc := range []struct {
			name string
			sel  Selector
			want func(b *budget, n int) int64
		}{
			{SelectorNameVOC, SelectorVOC(), func(b *budget, n int) int64 { return b.share(n) }},
			{SelectorNameLadder, SelectorLadder(), func(b *budget, _ int) int64 { return b.share(1) }},
		} {
			s := newSched(t, pol, Config{
				Bounds: bounds, Costs: costs, BudgetMS: pool, Selector: tc.sel,
			}, ws...)
			front := s.Frontier()
			got, reg := tc.sel.Allowances(front, s.state())
			want := tc.want(&s.bud, len(front))
			for i, v := range got {
				if v != want {
					t.Errorf("%s at budget %d: allowance[%d] = %d, want budget.share = %d",
						tc.name, pool, i, v, want)
				}
			}
			if reg.Basis != "" || reg.Scarce || len(reg.CommitSet) != 0 {
				t.Errorf("%s reported a regime it does not compute: %+v", tc.name, reg)
			}
		}
	}
}

// DECISION 1, THE OTHER HALF: under ¬scarce `voc2` IS `voc` — the same code
// path, so the same receipts in the same order, and every null-case proof M2b
// and M2b.1 published survives by construction.
func TestVOC2EqualsVOCUnderNonScarcity(t *testing.T) {
	pol := testPolicy(t)
	a, b, c := world(t, "a"), world(t, "b"), world(t, "c")
	ws := []object.RecordedWorld{a, b, c}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)
	order := []string{c.Digest, a.Digest, b.Digest}

	for _, budget := range []int64{0, 3 * worldLadder, 10 * worldLadder} {
		seq := func(sel Selector) []string {
			s := newSched(t, pol, Config{
				Bounds: bounds, Costs: costs, BudgetMS: budget, Selector: sel,
				Order: order, BudgetBasis: BudgetBasisPredicted,
			}, ws...)
			steps, bought := drain(t, pol, s, ws)
			for _, st := range steps {
				if st.Scarce {
					t.Fatalf("budget %d: the fixture is scarce; it cannot test the ¬scarce gate", budget)
				}
			}
			out := make([]string, 0, len(bought))
			for _, p := range bought {
				out = append(out, p[0]+"/"+p[1])
			}
			return out
		}
		got, want := seq(SelectorVOC2()), seq(SelectorVOC())
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("budget %d: voc2 bought\n  %v\nvoc bought\n  %v", budget, got, want)
		}
	}
}

// A `voc2` ROW UNDER ¬SCARCE CARRIES A FINISH COST IT DID NOT DIVIDE BY, and
// says so. The measurement is worth having; mistaking it for the denominator
// would misreport which rule priced the row.
func TestNonScarceRowsCarryFinishAsAMeasurementNotADenominator(t *testing.T) {
	pol := testPolicy(t)
	a := world(t, "a")
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)
	s := newSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: 10 * worldLadder, Selector: SelectorVOC2(),
	}, a)
	step, _ := s.Next()
	row, ok := rowFor(step, a.Digest, "guard")
	if !ok {
		t.Fatalf("no guard row: %+v", step.Considered)
	}
	if row.ScoreBasis != ScoreBasisRung {
		t.Errorf("score_basis = %q under ¬scarce, want %q", row.ScoreBasis, ScoreBasisRung)
	}
	if row.FinishMS != worldLadder {
		t.Errorf("finish_ms = %d, want the world's whole ladder (%d)", row.FinishMS, worldLadder)
	}
	if row.ScoreBPPS != row.ValueBP*1000/row.CostMS {
		t.Errorf("score %d was not divided by the RUNG's cost %d", row.ScoreBPPS, row.CostMS)
	}
}

// THE COMMIT ORDER IS DETERMINISTIC UNDER PERMUTED INPUT. An allocation that
// could reorder itself between two runs is not replayable, and the terminal
// tie-break is the CONTROL-PLANE world order — never the world digest, which
// is a function of candidate-authored bytes.
func TestCommitOrderIsDeterministicUnderPermutedInput(t *testing.T) {
	pol := testPolicy(t)
	a, b, c := world(t, "a"), world(t, "b"), world(t, "c")
	order := []string{c.Digest, b.Digest, a.Digest}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	commit := func(ws ...object.RecordedWorld) []string {
		s := newSched(t, pol, Config{
			Bounds: bounds, Costs: costs, BudgetMS: 2 * worldLadder, Selector: SelectorVOC2(),
			Order: order, BudgetBasis: BudgetBasisPredicted,
		}, ws...)
		step, _ := s.Next()
		if !step.Scarce {
			t.Fatal("the fixture is not scarce; there is no commit set to be deterministic about")
		}
		return step.CommitSet
	}
	first := commit(a, b, c)
	second := commit(c, a, b)
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Errorf("the commit set moved when the input order moved: %v vs %v", first, second)
	}
	if len(first) != 2 || first[0] != c.Digest || first[1] != b.Digest {
		t.Errorf("commit set = %v, want the control-plane order's first two worlds [%s %s]",
			first, c.Digest, b.Digest)
	}
}

// M1f, RE-DERIVED FOR THE NEW RULE RATHER THAN INHERITED: no value entering
// finish_ms, the scarcity test, the commit order or the allowance is derived
// from a candidate-authored metric.
//
// The test moves every candidate-authored number it can — the metrics on the
// recorded receipts, including the one vector 23 forges — and asserts the plan
// does not move. What the plan reads is the POLICY's declared ladder, the
// workspace's own fitted cost table, the scheduler's record of its own
// purchases, and the control-plane world order.
func TestNoCandidateAuthoredValueEntersTheFinishingRule(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	plan := func(passed int64) commitPlan {
		s := newSched(t, pol, Config{
			Bounds: bounds, Costs: costs, BudgetMS: raceBudgetMS, Selector: SelectorVOC2(),
			Order: []string{a.Digest, b.Digest},
		}, ws...)
		s.Record(receiptFor(t, pol, a, "guard", "pass", 11, guardPass()))
		s.Record(receiptFor(t, pol, a, "collect", "pass", 282, collectPass()))
		// The candidate's own self-report, at 8 and at vector 23's 500.
		s.Record(receiptFor(t, pol, b, "guard", "pass", 11, guardPass()))
		s.Record(receiptFor(t, pol, b, "collect", "pass", 282, collectPass()))
		s.Record(receiptFor(t, pol, b, "suite", "pass", 689, suitePass(passed)))
		return voc2Plan(s.Frontier(), s.state())
	}
	honest, forged := plan(8), plan(500)
	if honest.scarce != forged.scarce || honest.uncommitted != forged.uncommitted {
		t.Errorf("the scarcity test moved with a candidate's self-report: %+v vs %+v", honest, forged)
	}
	if strings.Join(honest.commit, ",") != strings.Join(forged.commit, ",") {
		t.Errorf("the commit set moved with a candidate's self-report: %v vs %v", honest.commit, forged.commit)
	}
	for w, ms := range honest.finish {
		if forged.finish[w] != ms {
			t.Errorf("finish_ms(%s) moved with a candidate's self-report: %d vs %d", w, ms, forged.finish[w])
		}
		if forged.allowance[w] != honest.allowance[w] {
			t.Errorf("allowance(%s) moved with a candidate's self-report: %d vs %d",
				w, honest.allowance[w], forged.allowance[w])
		}
	}
}

// THE METALEVEL DOES NOT GET MORE EXPENSIVE. The revision reads the bracket
// under two regimes — unconditioned to order the commit set, conditioned to
// price the row — and pays for it ONCE. A step costs the same number of
// `Decide` calls under voc2 as under voc, which is the ratio the whole design
// rests on.
func TestVOC2CostsNoAdditionalDecideCalls(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	count := func(sel Selector) int {
		calls := 0
		s := newSched(t, pol, Config{
			Decide: stubDecide(&calls), Bounds: bounds, Costs: costs,
			BudgetMS: raceBudgetMS, Selector: sel, Order: []string{a.Digest, b.Digest},
		}, ws...)
		calls = 0
		step, _ := s.Next()
		if len(step.Considered) != 2 {
			t.Fatalf("step considered %d rows, want one per world", len(step.Considered))
		}
		return calls
	}
	if got, want := count(SelectorVOC2()), count(SelectorVOC()); got != want {
		t.Errorf("voc2 spent %d Decide calls on a step voc spent %d on", got, want)
	}
}

// DECISION 4: a bracket over a world the budget cannot finish holds
// `fail-closed` ALONE. Certifying a purchase against a pass outcome the race
// can never reach is certifying it against money that does not exist — and
// `fail-closed` stays unconditional, because a world can always fail and
// failing costs one rung rather than a ladder.
func TestUncompletableWorldKeepsOnlyTheFailClosedOutcome(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)
	s := newSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: raceBudgetMS, Selector: SelectorVOC2(),
		Order: []string{a.Digest, b.Digest}, BudgetBasis: BudgetBasisPredicted,
	}, ws...)
	step, _ := s.Next()

	head, rival := rowOrFail(t, step, a.Digest, "guard"), rowOrFail(t, step, b.Digest, "guard")
	if !head.Committed || rival.Committed {
		t.Fatalf("commit set is not {head}: head=%v rival=%v", head.Committed, rival.Committed)
	}
	if len(head.FlipOutcomes) < 2 {
		t.Errorf("the committed world's bracket held %d outcome(s): %v", len(head.FlipOutcomes), head.FlipOutcomes)
	}
	if len(rival.FlipOutcomes) != 1 || !strings.HasPrefix(rival.FlipOutcomes[0], OutcomeFailClosed) {
		t.Errorf("the uncompletable world's bracket = %v, want fail-closed alone", rival.FlipOutcomes)
	}
	// Decision 5: the hard-gate override lapses for a world poverty has
	// already put out. Refusing that purchase is not a choice to withhold, it
	// is the absence of an alternative — and the sentence says which.
	if rival.Admissible {
		t.Error("a rung on a world the pool cannot finish was admitted by the hard-gate override")
	}
	if !strings.HasPrefix(rival.Declined, DeclineUnreachable) {
		t.Errorf("declined = %q, want the unreachable sentence", rival.Declined)
	}
	if strings.Contains(rival.Declined, "decision-inert") {
		t.Error("a hard-gated rung was declined as decision-inert, which is a claim about the policy")
	}
}

// THE BOOTSTRAP, which is decision 5's qualifier and the reason it has one.
//
// At a budget below ONE ladder no world is completable, so read literally the
// hard-gate override lapses on every world at once, nothing is admissible, and
// THE RACE BUYS NOTHING AT ALL. That is not caution: `Decide` then reports
// `on_all_worlds_failed_machinery` — "no world produced conclusive evidence" —
// which files unbought evidence under BROKEN MACHINERY, the misattribution
// M2b §3.4 exists to remove, and it makes `on_evidence_incomplete` (measured
// 4 of 4 on the shipped fixture) unobservable, because that rule needs one
// world that passed everything it paid for and nothing was paid for.
//
// The flip test does not catch it: while ANY world lacks its first receipt the
// machinery rule dominates every bracket outcome, so `fail-closed` moves
// nothing and flip is honestly 0. M2b decision 3's own amendment refused this
// same "buys nothing at all" outcome by name.
//
// So with the commit set EMPTY the pool is committed to nobody, no reservation
// is being protected, and M2b decision 3c stands verbatim.
func TestABudgetBelowOneLadderStillBuysTheGatesItCanAfford(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)
	// 320 ms against a 982 ms ladder: nobody is finishable, ever.
	s := newSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: 320, Selector: SelectorVOC2(),
		Order: []string{a.Digest, b.Digest}, BudgetBasis: BudgetBasisPredicted,
	}, ws...)
	steps, bought := drain(t, pol, s, ws)

	if len(bought) == 0 {
		t.Fatal("the race bought NOTHING: every hard gate lapsed at once and the ledger records a machinery failure where the truth is poverty")
	}
	for _, st := range steps {
		if !st.Scarce {
			t.Errorf("step %d is not scarce at a budget below one ladder", st.Step)
		}
		if len(st.CommitSet) != 0 {
			t.Errorf("step %d committed to %v at a budget that can finish nobody", st.Step, st.CommitSet)
		}
	}
	// Equal shares are back, exactly (decision 3's degenerate case), and the
	// stop is the honest one: the money ran out.
	if f := s.Finish(); f.Stop != StopBudget {
		t.Errorf("stop = %q, want %q: the purchases that were refused were refused by the POOL", f.Stop, StopBudget)
	}
	// And every world holds a strict prefix, with every unbought rung named.
	for _, w := range ws {
		if len(UnpaidHardGates(pol, w, s.perWorld[w.Digest])) == 0 {
			t.Errorf("world %s finished its ladder at a budget that cannot pay for one", w.Digest)
		}
	}
	if len(s.Skipped()) == 0 {
		t.Error("a starved race recorded no oracle.skipped rows")
	}
}

// DECISION 7's SENTENCES ARE MUTUALLY EXCLUSIVE, never appear on a bought row,
// and each names the arithmetic that produced it.
func TestDeclineSentencesAreExclusiveAndNeverOnABoughtRow(t *testing.T) {
	pol := testPolicy(t)
	a, b, c := world(t, "a"), world(t, "b"), world(t, "c")
	ws := []object.RecordedWorld{a, b, c}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)
	s := newSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: worldLadder + 400, Selector: SelectorVOC2(),
		Order: []string{a.Digest, b.Digest, c.Digest}, BudgetBasis: BudgetBasisPredicted,
	}, ws...)
	steps, _ := drain(t, pol, s, ws)

	seen := map[string]bool{}
	for _, st := range steps {
		for _, r := range st.Considered {
			reserved := strings.HasPrefix(r.Declined, DeclineReserved)
			unreachable := strings.HasPrefix(r.Declined, DeclineUnreachable)
			seen[DeclineReserved] = seen[DeclineReserved] || reserved
			seen[DeclineUnreachable] = seen[DeclineUnreachable] || unreachable
			if reserved && unreachable {
				t.Errorf("row %s/%s carries both sentences: %q", r.World, r.Oracle, r.Declined)
			}
			if r.Bought() && (reserved || unreachable) {
				t.Errorf("a BOUGHT row carries a decline sentence: %q", r.Declined)
			}
			if (reserved || unreachable) && strings.Contains(r.Declined, "decision-inert") {
				t.Errorf("row %s/%s mixes a money sentence with a policy sentence: %q", r.World, r.Oracle, r.Declined)
			}
		}
	}
	if !seen[DeclineUnreachable] {
		t.Error("no row was declined as unreachable; the fixture never exercised decision 4")
	}
}

// THE `reserved` SENTENCE, on the one shape that reaches it: a world the pool
// has NOT committed to, whose next rung is still admissible because the flip
// test says it can move the decision, and whose cost exceeds what the
// reservation left uncommitted.
//
// It is a separate sentence from `unaffordable: … exceeds this world's share`
// because it is a separate fact — the money is not merely short, it is
// PROMISED to a world that can be finished — and an operator reading
// oracle.skipped afterwards has no other way to tell them apart.
func TestReservedSentenceNamesTheCommitmentThatTookTheMoney(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)
	// A decision rule every purchase MOVES, so `flip` is 1 on every row and
	// the reachability condition never gets to refuse anything: what is left
	// is the reservation, which is exactly the term under test. It stands in
	// for a policy declaring `on_evidence_incomplete`, under which measuring a
	// rival really is decision-relevant (decision 5's own worked table).
	moves := func(_ policy.Policy, _ []object.RecordedWorld, rs []object.RecordedReceipt) object.Decision {
		return object.Decision{Type: "REJECT", Subject: []string{fmt.Sprintf("mv0:r%d", len(rs))}}
	}
	// One full ladder plus 100 ms: the pool commits to the head world and
	// leaves the rival 100 ms, which pays for its guard and not its collect.
	s := newSched(t, pol, Config{
		Decide: moves, Bounds: bounds, Costs: costs, BudgetMS: worldLadder + 100,
		Selector: SelectorVOC2(), Order: []string{a.Digest, b.Digest},
		BudgetBasis: BudgetBasisPredicted,
	}, ws...)
	steps, _ := drain(t, pol, s, ws)

	var found string
	for _, st := range steps {
		for _, r := range st.Considered {
			if !strings.HasPrefix(r.Declined, DeclineReserved) {
				continue
			}
			found = r.Declined
			if !r.Admissible {
				t.Errorf("the reserved sentence was printed on an INADMISSIBLE row: %+v", r)
			}
			if r.Committed {
				t.Errorf("a COMMITTED world was told the pool is committed elsewhere: %+v", r)
			}
			if r.Affordable {
				t.Errorf("the reserved sentence was printed on an AFFORDABLE row: %+v", r)
			}
		}
	}
	if found == "" {
		t.Fatal("no row reached the reserved sentence; the fixture does not exercise decision 7")
	}
	for _, want := range []string{"committed to finishing", "to finish", "uncommitted"} {
		if !strings.Contains(found, want) {
			t.Errorf("the reserved sentence %q does not name %q", found, want)
		}
	}
	// And it is NOT the equal-share sentence, which would describe an
	// apportionment that did not happen.
	if strings.Contains(found, "share") {
		t.Errorf("the reserved sentence claims an equal share: %q", found)
	}
}

func rowOrFail(t *testing.T, step Step, world, oracle string) Considered {
	t.Helper()
	r, ok := rowFor(step, world, oracle)
	if !ok {
		t.Fatalf("no considered row for %s/%s: %+v", world, oracle, step.Considered)
	}
	return r
}
