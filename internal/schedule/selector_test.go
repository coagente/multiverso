package schedule

// M2b1: the SELECTOR SEAM under test — the ladder's order, the rotation's
// totality, the equivalence of the two arms where the design says they must
// be equivalent, and the one call path both of them must reach.
//
// Everything here is pure: no worlds on disk, no Python, no ledger, and a
// decision rule the test hands in and counts.

import (
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// ladderSched builds a scheduler on the budgeted fixed arm over the given
// worlds, with the CONTROL-PLANE order handed in as the caller's own order —
// which is what internal/race does with candidate ordinals.
func ladderSched(t *testing.T, pol policy.Policy, cfg Config, ws ...object.RecordedWorld) *Scheduler {
	t.Helper()
	cfg.Selector = SelectorLadder()
	if cfg.Order == nil {
		order := make([]string, 0, len(ws))
		for _, w := range ws {
			order = append(order, w.Digest)
		}
		cfg.Order = order
	}
	return newSched(t, pol, cfg, ws...)
}

// buyAll drains a scheduler, recording a passing receipt for every purchase
// it dispatches, and returns the (world, oracle) pairs in PURCHASE ORDER.
func buyAll(t *testing.T, pol policy.Policy, s *Scheduler, ws []object.RecordedWorld, wallMS int64) [][2]string {
	t.Helper()
	return buyAllCosted(t, pol, s, ws, func(string) int64 { return wallMS })
}

// buyAllCosted is buyAll with a per-rung ACTUAL cost, so a test can make the
// stopwatch agree with the cost model — which is the only way to exercise the
// starvation boundary deliberately, since affordability reads the prediction
// and the pool is charged the measurement.
func buyAllCosted(t *testing.T, pol policy.Policy, s *Scheduler, ws []object.RecordedWorld,
	wallOf func(oracle string) int64) [][2]string {
	t.Helper()
	byDig := map[string]object.RecordedWorld{}
	for _, w := range ws {
		byDig[w.Digest] = w
	}
	var got [][2]string
	for i := 0; i < 64; i++ {
		step, more := s.Next()
		if !more {
			return got
		}
		for _, c := range step.Chosen {
			got = append(got, [2]string{c.World, c.Oracle})
			s.Record(passingReceipt(t, pol, byDig[c.World], c.Oracle, wallOf(c.Oracle)))
		}
	}
	t.Fatal("scheduler did not terminate in 64 steps")
	return nil
}

// passingReceipt is a bound PASS for one rung, with the metrics that rung's
// gates read, so a world that buys its whole ladder actually passes it.
func passingReceipt(t *testing.T, pol policy.Policy, w object.RecordedWorld, name string, wallMS int64) object.RecordedReceipt {
	t.Helper()
	switch name {
	case "guard":
		return receiptFor(t, pol, w, name, "pass", wallMS, guardPass())
	case "collect":
		return receiptFor(t, pol, w, name, "pass", wallMS, collectPass())
	default:
		return receiptFor(t, pol, w, name, "pass", wallMS, suitePass(8))
	}
}

// THE LADDER'S ORDER: depth-first over worlds in the control-plane order,
// each world's rungs in policy gate order. World #1 completes its whole
// ladder before world #2 buys anything — which is the property the arm exists
// to have, and the one the round-robin adaptive rule does not.
func TestLadderIsDepthFirstInControlPlaneOrder(t *testing.T) {
	pol := testPolicy(t)
	// Digest order is c < a < b; the CONTROL-PLANE order is the one handed
	// in, and it is deliberately the opposite, so a test that passed by
	// accident on digest order fails here.
	a, b, c := world(t, "a"), world(t, "b"), world(t, "c")
	s := ladderSched(t, pol, Config{
		Bounds: Bounds{CollectedBase: 8},
		Order:  []string{b.Digest, a.Digest, c.Digest},
	}, a, b, c)

	got := buyAll(t, pol, s, []object.RecordedWorld{a, b, c}, 10)
	var seq []string
	for _, p := range got {
		short := map[string]string{a.Digest: "a", b.Digest: "b", c.Digest: "c"}[p[0]]
		seq = append(seq, short+"/"+p[1])
	}
	want := "b/guard,b/collect,b/suite,a/guard,a/collect,a/suite,c/guard,c/collect,c/suite"
	if strings.Join(seq, ",") != want {
		t.Errorf("ladder purchase order =\n  %s\nwant\n  %s", strings.Join(seq, ","), want)
	}
}

// The ladder ranks on the ORDER IT WAS HANDED, never on the world digest.
// Permuting the digests while holding the control-plane order fixed must not
// move a single purchase: a world digest is a function of candidate-authored
// bytes, and M1f's rule is absolute.
func TestLadderOrderIgnoresWorldDigests(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	order := []string{b.Digest, a.Digest}

	first := buyAll(t, pol, ladderSched(t, pol, Config{
		Bounds: Bounds{CollectedBase: 8}, Order: order,
	}, a, b), []object.RecordedWorld{a, b}, 10)

	// Same worlds, same control-plane order, handed to New in the opposite
	// slice order — which is what a caller that sorted by digest would do.
	second := buyAll(t, pol, ladderSched(t, pol, Config{
		Bounds: Bounds{CollectedBase: 8}, Order: order,
	}, b, a), []object.RecordedWorld{a, b}, 10)

	if len(first) != len(second) {
		t.Fatalf("purchase counts differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("purchase %d differs: %v vs %v — the order was derived from something other than the control plane", i, first[i], second[i])
		}
	}
}

// ROTATION IS TOTAL over N: replicate r puts candidate r at the head, so over
// N replicates every candidate holds the head position EXACTLY ONCE. That is
// what turns the depth-first arm's positional advantage into a measured
// variance component rather than a confound — and it stays deterministic and
// recorded, which randomization would not.
func TestRotationIsTotalOverN(t *testing.T) {
	order := []string{"w1", "w2", "w3", "w4"}
	heads := map[string]int{}
	for r := 0; r < len(order); r++ {
		got := rotate(order, r)
		if len(got) != len(order) {
			t.Fatalf("rotation %d changed the order's length: %v", r, got)
		}
		seen := map[string]bool{}
		for _, w := range got {
			if seen[w] {
				t.Fatalf("rotation %d duplicated %s: %v", r, w, got)
			}
			seen[w] = true
		}
		heads[got[0]]++
	}
	for _, w := range order {
		if heads[w] != 1 {
			t.Errorf("world %s held the head position %d times over %d replicates, want exactly 1", w, heads[w], len(order))
		}
	}
	// ρ is taken mod N, so replicate 5 of a 4-world race is replicate 1's
	// order: a rotation that ran off the end would silently stop rotating.
	if got, want := rotate(order, 5), rotate(order, 1); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rotation 5 over 4 worlds = %v, want %v", got, want)
	}
	if got := rotate(nil, 3); len(got) != 0 {
		t.Errorf("rotating an empty order produced %v", got)
	}
}

// THE EQUIVALENCE TEST (decision 1's own): under an UNBOUNDED budget the two
// selectors buy the IDENTICAL receipt set. They differ in the order of the
// steps and in nothing else.
//
// This is the property that makes the comparison a comparison. If the arms
// bought different evidence at unbounded budget, then every difference
// measured at a binding budget would be confounded with a difference in what
// they buy when money is not the question.
func TestSelectorsBuyTheSameSetUnderAnUnboundedBudget(t *testing.T) {
	pol := testPolicy(t)
	a, b, c := world(t, "a"), world(t, "b"), world(t, "c")
	ws := []object.RecordedWorld{a, b, c}
	order := []string{c.Digest, a.Digest, b.Digest}

	voc := buyAll(t, pol, newSched(t, pol, Config{
		Bounds: Bounds{CollectedBase: 8}, Order: order,
	}, ws...), ws, 10)
	ladder := buyAll(t, pol, ladderSched(t, pol, Config{
		Bounds: Bounds{CollectedBase: 8}, Order: order,
	}, ws...), ws, 10)

	key := func(ps [][2]string) []string {
		out := make([]string, 0, len(ps))
		for _, p := range ps {
			out = append(out, p[0]+"/"+p[1])
		}
		sort.Strings(out)
		return out
	}
	x, y := key(voc), key(ladder)
	if strings.Join(x, ",") != strings.Join(y, ",") {
		t.Errorf("the arms bought different evidence at an unbounded budget:\n  voc:    %v\n  ladder: %v", x, y)
	}
	// And they DID differ in order, or the test proves nothing about the arms
	// being two arms.
	same := true
	for i := range voc {
		if voc[i] != ladder[i] {
			same = false
		}
	}
	if same {
		t.Error("the two arms bought in the identical order; the fixture cannot distinguish them")
	}
}

// DECISION 6'S INVARIANT, asserted rather than hoped for: a ladder row
// carries NO value-of-computation term. The fields stay serialized (M1b
// decision 5 forbids omitempty games) and they are zero, which is why `mvo
// explain` renders "—" rather than "0" for them: a 0 under FLIP is a VOC row
// that scored zero, and that is a different fact.
func TestLadderRowsCarryNoVOCTerms(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	s := ladderSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, ws...)
	byDig := map[string]object.RecordedWorld{a.Digest: a, b.Digest: b}

	rows := 0
	for i := 0; i < 32; i++ {
		step, more := s.Next()
		for _, r := range step.Considered {
			rows++
			switch {
			case r.Flip != 0:
				t.Errorf("ladder row %s/%s carries flip=%d", r.World, r.Oracle, r.Flip)
			case len(r.FlipOutcomes) != 0:
				t.Errorf("ladder row %s/%s carries flip_outcomes=%v", r.World, r.Oracle, r.FlipOutcomes)
			case r.DiscountBP != 0 || r.ExecutorBP != 0 || r.ValueBP != 0:
				t.Errorf("ladder row %s/%s carries a value term: disc=%d exec=%d value=%d",
					r.World, r.Oracle, r.DiscountBP, r.ExecutorBP, r.ValueBP)
			case r.ScoreBPPS != 0 || r.ScoreRank != 0:
				t.Errorf("ladder row %s/%s carries a score: bpps=%d rank=%d",
					r.World, r.Oracle, r.ScoreBPPS, r.ScoreRank)
			case r.Order < 1:
				t.Errorf("ladder row %s/%s carries no depth-first rank", r.World, r.Oracle)
			}
			if !r.Admissible {
				t.Errorf("ladder row %s/%s is inadmissible; the exhaustive ladder declines nothing on value", r.World, r.Oracle)
			}
		}
		if !more {
			break
		}
		for _, c := range step.Chosen {
			s.Record(passingReceipt(t, pol, byDig[c.World], c.Oracle, 10))
		}
	}
	if rows == 0 {
		t.Fatal("the ladder considered nothing")
	}
	// The VOC arm's rows still carry them, or the assertion above is about a
	// scheduler that computes nothing rather than about this arm.
	voc := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, ws...)
	step, _ := voc.Next()
	for _, r := range step.Considered {
		if r.ExecutorBP == 0 {
			t.Errorf("a VOC row carries no executor weight: %+v", r)
		}
		if r.Order != 0 {
			t.Errorf("a VOC row carries a depth-first rank of %d; it has none", r.Order)
		}
	}
}

// F5, as a CALL-PATH assertion: both arms reach ONE affordability predicate
// and ONE charge point. The counters live on the budget object because the
// claim is about the code path rather than about the numbers — a second
// affordability rule added for one arm would show up here as an arm that
// stopped calling this one.
func TestBothArmsReachOneAffordableAndOneCharge(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	for _, tc := range []struct {
		name string
		sel  Selector
	}{{SelectorNameVOC, SelectorVOC()}, {SelectorNameLadder, SelectorLadder()}} {
		cfg := Config{Bounds: Bounds{CollectedBase: 8}, BudgetMS: 100_000, Selector: tc.sel,
			Order: []string{a.Digest, b.Digest}}
		s := newSched(t, pol, cfg, ws...)
		bought := buyAll(t, pol, s, ws, 10)
		if s.bud.nAfford < len(bought) {
			t.Errorf("%s: %d affordability tests for %d purchases; the arm is buying through some other predicate",
				tc.name, s.bud.nAfford, len(bought))
		}
		if s.bud.nCharge != len(bought) {
			t.Errorf("%s: %d charges for %d purchases; the arm is charging through some other point",
				tc.name, s.bud.nCharge, len(bought))
		}
		if s.bud.spent != int64(10*len(bought)) {
			t.Errorf("%s: spent %d ms over %d purchases at 10 ms each", tc.name, s.bud.spent, len(bought))
		}
	}
}

// THE LADDER HAS ONE CONTENDER (decision 5). Equal shares exist so that
// elimination releases budget among worlds buying at once; a depth-first arm
// buys for the head world alone, so apportioning the pool across the worlds
// behind it would starve the only world that can spend. It is not a second
// predicate — it is budget.affordable's own contenders == 1 case.
func TestLadderAffordabilityIsAgainstTheWholeRemainingPool(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	// A fitted table, so the rows carry real millisecond predictions and the
	// share test can actually bind.
	var samples []Sample
	for _, k := range []struct {
		kind  string
		fixed int64
	}{{policy.KindTreeGuard, 10}, {policy.KindPytestCollect, 300}, {policy.KindPytestSuite, 400}} {
		for u := int64(1); u <= 3; u++ {
			samples = append(samples, Sample{Kind: k.kind, Seal: policy.AutoloadOff, Units: u, WallMS: k.fixed + u})
		}
	}
	costs := NewTable(samples, policy.AutoloadOff, bounds)

	// 500 ms: enough for world #1's guard + collect against the WHOLE pool,
	// and not enough for either world under an equal two-way share.
	s := ladderSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: 500,
		Order: []string{a.Digest, b.Digest},
	}, ws...)
	got := buyAll(t, pol, s, ws, 10)
	if len(got) < 2 || got[0][0] != a.Digest || got[1][0] != a.Digest {
		t.Fatalf("the ladder did not spend the pool on the head world first: %v", got)
	}
	if got[1][1] != "collect" {
		t.Errorf("second purchase = %q, want collect at ~301 ms against a 490 ms pool (an equal share would have refused it)",
			got[1][1])
	}

	// The VOC arm at the same budget divides the pool, which is the behaviour
	// the ladder must NOT have inherited.
	voc := newSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: 500, Order: []string{a.Digest, b.Digest},
	}, ws...)
	step, _ := voc.Next()
	for _, r := range step.Considered {
		if r.Oracle == "guard" && !r.Affordable {
			t.Fatalf("the VOC arm could not afford a 11 ms guard out of a 250 ms share: %+v", r)
		}
	}
}

// The PREDICTED basis refuses a workspace that cannot price every buyable
// kind, and NAMES the kinds. A basis under which half the rungs are free is
// not a basis: a declared-rank kind has no prediction at all, so the budget
// would silently stop binding on exactly the rungs nobody measured.
func TestPredictedBasisRefusesAnUnpricedWorkspaceByName(t *testing.T) {
	pol := testPolicy(t)
	a := world(t, "a")
	_, err := New(Config{
		Policy: pol, Decide: stubDecide(new(int)), Batch: 1,
		Costs:       NewTable(nil, policy.AutoloadOff, Bounds{CollectedBase: 8}),
		Bounds:      Bounds{CollectedBase: 8},
		BudgetMS:    500,
		BudgetBasis: BudgetBasisPredicted,
	}, []object.RecordedWorld{a})
	if err == nil {
		t.Fatal("the predicted basis was accepted on a workspace with no fitted cost at all")
	}
	for _, kind := range []string{policy.KindTreeGuard, policy.KindPytestCollect, policy.KindPytestSuite} {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("the refusal does not name %s: %v", kind, err)
		}
	}
	if !strings.Contains(err.Error(), BudgetBasisActual) {
		t.Errorf("the refusal names no way out: %v", err)
	}
}

// Under the PREDICTED basis the pool is charged the pinned table's
// prediction, not the receipt's measured cost — which is what puts the
// allocation back inside the determinism tuple. The receipt still records
// what it really cost; nothing is hidden.
func TestPredictedBasisChargesTheModelAndNotTheStopwatch(t *testing.T) {
	pol := testPolicy(t)
	a := world(t, "a")
	bounds := Bounds{CollectedBase: 8}
	var samples []Sample
	for _, k := range []string{policy.KindTreeGuard, policy.KindPytestCollect, policy.KindPytestSuite} {
		for u := int64(1); u <= 3; u++ {
			samples = append(samples, Sample{Kind: k, Seal: policy.AutoloadOff, Units: u, WallMS: 100})
		}
	}
	costs := NewTable(samples, policy.AutoloadOff, bounds)
	s := ladderSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: 10_000, BudgetBasis: BudgetBasisPredicted,
	}, a)
	// Every receipt claims 7 777 ms of wall time; the model says 100 ms.
	got := buyAll(t, pol, s, []object.RecordedWorld{a}, 7777)
	want := int64(100 * len(got))
	if s.bud.spent != want {
		t.Errorf("charged %d ms over %d purchases, want the model's %d ms (the stopwatch said %d)",
			s.bud.spent, len(got), want, 7777*len(got))
	}
	if f := s.Finish(); f.Budget.SpentMS != want {
		t.Errorf("schedule.finished reports %d ms spent, want %d", f.Budget.SpentMS, want)
	}
}

// schedule.started records what the comparison reads: the selector, the
// basis, the rotation and the CONTROL-PLANE order. A trace without them
// cannot be paired with another trace, which is the whole of §3.
func TestStartedRecordsTheComparisonFields(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	s := ladderSched(t, pol, Config{
		Bounds: Bounds{CollectedBase: 8}, BudgetMS: 900, Rotation: 1,
		Order: []string{a.Digest, b.Digest},
	}, a, b)
	st := s.Started("mv0:intent", ScheduleFixedBudget, 1)
	if st.Selector != SelectorNameLadder {
		t.Errorf("selector = %q, want %q", st.Selector, SelectorNameLadder)
	}
	if st.BudgetBasis != BudgetBasisActual {
		t.Errorf("budget_basis = %q, want the default %q", st.BudgetBasis, BudgetBasisActual)
	}
	if st.Rotation != 1 {
		t.Errorf("rotation = %d, want 1", st.Rotation)
	}
	if len(st.WorldOrder) != 2 || st.WorldOrder[0] != b.Digest {
		t.Errorf("world_order = %v, want the order rotated by 1 (head %s)", st.WorldOrder, b.Digest)
	}
	if st.Schedule != ScheduleFixedBudget {
		t.Errorf("schedule = %q, want %q", st.Schedule, ScheduleFixedBudget)
	}
}

// A pre-M2b1 trace carries none of the four new fields, and their absence is
// normalized EXACTLY rather than guessed: only the adaptive arm could record
// a trace before M2b1, only actual wall-clock could charge the pool, and the
// world order is UNKNOWN — never digest order, because inventing a past
// ordering is inventing evidence.
func TestPreM2b1TraceNormalizesWithoutInventingAnOrder(t *testing.T) {
	old := []byte(`{"budget":{"max_oracle_ms":0},"constants":{"executor_bp":{},"redundancy_bp":{}},` +
		`"cost_table":[],"intent":"mv0:i","mode":"decision","parallel":1,"schedule":"adaptive"}`)
	st, err := DecodeStarted(old)
	if err != nil {
		t.Fatalf("an M2b-era schedule.started no longer decodes: %v", err)
	}
	if st.Selector != SelectorNameVOC {
		t.Errorf("selector = %q on a pre-M2b1 trace, want %q", st.Selector, SelectorNameVOC)
	}
	if st.BudgetBasis != BudgetBasisActual {
		t.Errorf("budget_basis = %q on a pre-M2b1 trace, want %q", st.BudgetBasis, BudgetBasisActual)
	}
	if len(st.WorldOrder) != 0 {
		t.Errorf("world_order = %v on a pre-M2b1 trace, want empty (unknown)", st.WorldOrder)
	}
}

// The starved stop leaves a world holding a STRICT LADDER PREFIX, and every
// rung it never bought carries an oracle.skipped that names the budget
// (decision 4). "Skipped, assume fine" is not a state and never will be.
func TestStarvedLadderSkipsEveryUnboughtRungByName(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	var samples []Sample
	for _, k := range []struct {
		kind  string
		fixed int64
	}{{policy.KindTreeGuard, 10}, {policy.KindPytestCollect, 300}, {policy.KindPytestSuite, 400}} {
		for u := int64(1); u <= 3; u++ {
			samples = append(samples, Sample{Kind: k.kind, Seal: policy.AutoloadOff, Units: u, WallMS: k.fixed + u})
		}
	}
	s := ladderSched(t, pol, Config{
		Bounds: bounds, Costs: NewTable(samples, policy.AutoloadOff, bounds), BudgetMS: 330,
		Order: []string{a.Digest, b.Digest},
	}, ws...)
	// The stopwatch agrees with the model, so the starvation point is where
	// the design says it is rather than where the machine felt like.
	got := buyAllCosted(t, pol, s, ws, func(oracle string) int64 {
		switch oracle {
		case "guard":
			return 11
		case "collect":
			return 301
		default:
			return 408
		}
	})
	f := s.Finish()
	if f.Stop != StopBudget {
		t.Fatalf("stop = %q over %d purchases at a binding budget, want %q", f.Stop, len(got), StopBudget)
	}
	skipped := map[string]string{}
	for _, sk := range s.Skipped() {
		skipped[sk.World+"/"+sk.Oracle] = sk.Reason
	}
	// World b stalled mid-ladder: the rung it was refused AND the rung behind
	// that one both have to say so. The frontier row alone would leave the
	// deepest rungs of a starved world unaccounted for, which is exactly the
	// silence decision 4 refuses.
	for _, name := range []string{"collect", "suite"} {
		if _, ok := skipped[b.Digest+"/"+name]; !ok {
			t.Errorf("world b's unbought rung %q has no oracle.skipped row (skips: %v)", name, skipped)
		}
	}
	budgetNamed := 0
	for _, r := range skipped {
		if strings.Contains(r, "budget") || strings.Contains(r, "pool") {
			budgetNamed++
		}
	}
	if budgetNamed != len(skipped) {
		t.Errorf("%d of %d skip reasons name the budget: %v", budgetNamed, len(skipped), skipped)
	}
	if len(UnpaidHardGates(pol, b, s.receiptsFor(b.Digest))) == 0 {
		t.Error("the starved world paid for every hard gate; the fixture does not starve")
	}
}
