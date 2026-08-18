package schedule_test

// WITHHOLDING MONOTONICITY (M2b decision 4), under test rather than in
// prose. It is the strongest result in the block and it is the one that
// replaces the type-I error control we do not have:
//
//	For any policy, any world set, and any receipts R ⊆ R* the exhaustive
//	ladder would have produced, the set of worlds passing every hard gate
//	under R is a SUBSET of the set passing under R*.
//
// Therefore FAR(adaptive) ≤ FAR(exhaustive) for any fixed policy and
// candidate set: adaptivity provably cannot cause a false admission. It can
// cause a false REJECTION, and that is the risk M2d must price.
//
// The tests live in an EXTERNAL test package on purpose: they use the real
// race.Decide, and internal/schedule must never import internal/race
// (decision 1). The dependency runs the other way — race holds a reference
// to the decision rule and hands it to the scheduler — so only a test binary
// may join the two.

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/schedule"
)

func compile(t *testing.T, p object.PolicyV1) policy.Policy {
	t.Helper()
	b, err := object.Canonical(p)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	pol, err := policy.Decode(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return pol
}

// policyShapes are the gate lattices the property is checked over: one
// ladder per shape, each with a different mix of gate predicates and ranking
// keys, so the claim is not an artifact of one policy.
func policyShapes(t *testing.T) []policy.Policy {
	t.Helper()
	base := func(gates []object.GateSpec, ranking []string, esc object.EscalationSpec) object.PolicyV1 {
		return object.PolicyV1{
			Schema: object.SchemaPolicyV1, Name: "shape",
			Oracles: []object.OracleSpec{
				{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{}, Args: []string{}},
				{Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{}},
				{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{}},
			},
			HardGates:  gates,
			Ranking:    ranking,
			Escalation: esc,
			Paths:      object.PathSpec{Protected: []string{"tests/**"}, Harness: []string{"conftest.py"}},
		}
	}
	guard := object.GateSpec{Gate: policy.GatePathsUnmodified, Oracle: "guard", Basis: object.BasisConstruction}
	collect := object.GateSpec{Gate: policy.GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction}
	delta := object.GateSpec{Gate: policy.GateCollectedNotBelow, Oracle: "collect", Basis: object.BasisConstruction}
	suite := object.GateSpec{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction}
	noFail := object.GateSpec{Gate: policy.GateNoFailedTests, Oracle: "suite", Basis: object.BasisConstruction}

	return []policy.Policy{
		compile(t, base([]object.GateSpec{guard, collect, suite},
			[]string{policy.KeyGatePass, policy.KeyTestsPassedDesc},
			object.EscalationSpec{RequireEvidence: []string{}})),
		compile(t, base([]object.GateSpec{collect, delta, suite, noFail},
			[]string{policy.KeyGatePass, policy.KeyPatchSizeAsc},
			object.EscalationSpec{RequireEvidence: []string{}, OnAllWorldsFailedMachinery: true})),
		compile(t, base([]object.GateSpec{suite},
			[]string{policy.KeyGatePass, policy.KeyTestsPassedDesc},
			object.EscalationSpec{RequireEvidence: []string{"suite"}, OnRankingTie: true})),
	}
}

func mkWorld(tag string, outcome string) object.RecordedWorld {
	return object.RecordedWorld{
		Digest: "mv0:" + strings.Repeat(tag, 64/len(tag)),
		World: object.World{
			Schema: object.SchemaWorld, Outcome: outcome,
			Tree: "git:" + strings.Repeat(tag, 40/len(tag)), Env: "mv0:env",
			PatchBytes: 100,
		},
	}
}

func mkReceipt(t *testing.T, pol policy.Policy, w object.RecordedWorld, name, status string,
	metrics map[string]int64) object.RecordedReceipt {
	t.Helper()
	o, ok := pol.OracleByName(name)
	if !ok {
		t.Fatalf("no oracle %q", name)
	}
	rec := object.Receipt{
		Schema: object.SchemaReceipt, World: w.Digest,
		Oracle:    object.OracleRef{ID: o.Kind, Config: o.Config},
		Execution: object.Execution{Argv: []string{}},
		Result: object.Result{Status: status, Metrics: metrics,
			Tools: map[string]string{}, Artifacts: []string{}},
		Freshness: object.Freshness{Basis: object.BasisConstruction,
			ValidFor: object.ValidFor{Tree: w.World.Tree, Env: w.World.Env}},
		Family: policy.KindFamily(o.Kind), Cost: object.Cost{WallMS: 100},
		Inputs: object.NoInputs(), Correlation: policy.KindCorrelation(o.Kind),
	}
	dig, _, err := object.Digest(rec)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return object.RecordedReceipt{Digest: dig, Receipt: rec}
}

// exhaustive builds R*: what the M1 fixed ladder would have produced for
// every world.
func exhaustive(t *testing.T, pol policy.Policy, ws []object.RecordedWorld) []object.RecordedReceipt {
	t.Helper()
	guard := map[string]int64{
		policy.MetricProtectedModified: 0, policy.MetricProtectedDeleted: 0,
		policy.MetricProtectedAdded: 0, policy.MetricHarnessModified: 0,
		policy.MetricHarnessDeleted: 0, policy.MetricHarnessAdded: 0,
		policy.MetricPathsExamined: 12,
	}
	var out []object.RecordedReceipt
	for i, w := range ws {
		status := "pass"
		if i == len(ws)-1 {
			status = "fail" // one world that genuinely fails, so R* is not trivial
		}
		out = append(out,
			mkReceipt(t, pol, w, "guard", "pass", guard),
			mkReceipt(t, pol, w, "collect", "pass", map[string]int64{
				policy.MetricCollectedTotal: 8, policy.MetricCollectedDelta: 0}),
			mkReceipt(t, pol, w, "suite", status, map[string]int64{
				policy.MetricTestsTotal: 8, policy.MetricTestsPassed: int64(8 - i),
				policy.MetricTestsFailed: 0, policy.MetricTestsErrored: 0}),
		)
	}
	return out
}

// passSet is the set of worlds race.Decide's own trace reports as passing
// every hard gate. It is read off the decision function, never modelled.
func passSet(pol policy.Policy, ws []object.RecordedWorld, rs []object.RecordedReceipt) map[string]bool {
	out := map[string]bool{}
	tr := race.Trace(pol, ws, rs)
	for _, c := range tr.Candidates {
		if c.Pass {
			out[c.World] = true
		}
	}
	return out
}

// THE SAFETY PROPERTY. Over several policy shapes and EVERY subset of the
// exhaustive receipt set, the pass set only ever shrinks.
func TestWithholdingMonotonicity(t *testing.T) {
	ws := []object.RecordedWorld{
		mkWorld("a", object.OutcomeCompleted),
		mkWorld("b", object.OutcomeCompleted),
		mkWorld("c", object.OutcomeCrash),
	}
	for i, pol := range policyShapes(t) {
		star := exhaustive(t, pol, ws)
		full := passSet(pol, ws, star)
		if len(full) == 0 {
			t.Fatalf("shape %d: nothing passes under exhaustion; the property would be vacuous", i)
		}
		for mask := 0; mask < 1<<len(star); mask++ {
			var subset []object.RecordedReceipt
			for j := range star {
				if mask&(1<<j) != 0 {
					subset = append(subset, star[j])
				}
			}
			for w := range passSet(pol, ws, subset) {
				if !full[w] {
					t.Fatalf("shape %d, subset %b: world %s passes under WITHHELD evidence but not under exhaustion",
						i, mask, w)
				}
			}
		}
	}
}

// THE SAME PROPERTY, WITH EVERY SELECTOR IN THE GENERATOR'S SET (M2b.2 §3.2).
//
// The pass-set claim is the one structural safety result in the project and
// M2b.2 changes the allocation rule underneath it, so the property is re-run
// over allocations the REVISED rule actually makes, at budgets that bind, on
// the real `race.Decide`. Nothing about the proof changes — the scheduler still
// only ever WITHHOLDS receipts relative to exhaustion, withholding makes a
// required metric absent, and an absent required metric fails the gate — and
// this is what makes that a checked fact rather than a re-derivation.
//
// F-8: if this fails with `voc2` in the selector set, nothing ships.
func TestWithholdingMonotonicityAcrossEverySelector(t *testing.T) {
	ws := []object.RecordedWorld{
		mkWorld("a", object.OutcomeCompleted),
		mkWorld("b", object.OutcomeCompleted),
		mkWorld("c", object.OutcomeCompleted),
	}
	// A fitted table, so the budget binds on predictions rather than failing
	// open on unpriced rungs: guard 10 ms, collect 100 ms, suite 200 ms, so a
	// world's whole ladder costs 310 ms and three of them cost 930.
	var samples []schedule.Sample
	for kind, ms := range map[string]int64{
		policy.KindTreeGuard: 10, policy.KindPytestCollect: 100, policy.KindPytestSuite: 200,
	} {
		for i := 0; i < 3; i++ {
			samples = append(samples, schedule.Sample{Kind: kind, Seal: policy.AutoloadOff, Units: 8, WallMS: ms})
		}
	}
	bounds := schedule.Bounds{CollectedBase: 8}
	costs := schedule.NewTable(samples, policy.AutoloadOff, bounds)

	arms := []struct {
		name string
		sel  schedule.Selector
	}{
		{schedule.SelectorNameVOC, schedule.SelectorVOC()},
		{schedule.SelectorNameVOC2, schedule.SelectorVOC2()},
		{schedule.SelectorNameLadder, schedule.SelectorLadder()},
	}
	for i, pol := range policyShapes(t) {
		star := exhaustive(t, pol, ws)
		full := passSet(pol, ws, star)
		if len(full) == 0 {
			t.Fatalf("shape %d: nothing passes under exhaustion; the property would be vacuous", i)
		}
		for _, arm := range arms {
			for _, budget := range []int64{0, 1, 120, 310, 311, 620, 930, 5000} {
				order := make([]string, 0, len(ws))
				for _, w := range ws {
					order = append(order, w.Digest)
				}
				s, err := schedule.New(schedule.Config{
					Policy: pol, Decide: race.Decide, Batch: 1, Costs: costs, Bounds: bounds,
					BudgetMS: budget, Selector: arm.sel, Order: order,
				}, ws)
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				for n := 0; n < 64; n++ {
					step, more := s.Next()
					if !more {
						break
					}
					for _, c := range step.Chosen {
						// THE SCHEDULER NEVER FABRICATES AND NEVER ALTERS a
						// receipt: it is handed exactly the one the exhaustive
						// ladder would have produced for that world and that
						// rung, so the only difference between R and R* is
						// WHICH ones were bought.
						rr, ok := receiptFromStar(pol, star, c.World, c.Oracle)
						if !ok {
							t.Fatalf("shape %d %s budget %d: the arm chose %s/%s, which exhaustion never produced",
								i, arm.name, budget, c.World, c.Oracle)
						}
						s.Record(rr)
					}
				}
				for w := range passSet(pol, ws, s.Receipts()) {
					if !full[w] {
						t.Fatalf("shape %d %s budget %d: world %s passes under WITHHELD evidence but not under exhaustion",
							i, arm.name, budget, w)
					}
				}
				if v := s.PurchaseLaw(); v != "" {
					t.Fatalf("shape %d %s budget %d: %s", i, arm.name, budget, v)
				}
			}
		}
	}
}

// receiptFromStar picks the receipt the exhaustive ladder produced for one
// world's declared oracle instance. The join is through the PINNED POLICY,
// because a trace row names a policy-local instance and a receipt names the
// registry kind plus the resolved config (M1e decision 8).
func receiptFromStar(pol policy.Policy, star []object.RecordedReceipt, world, oracle string) (object.RecordedReceipt, bool) {
	spec, ok := pol.OracleByName(oracle)
	if !ok {
		return object.RecordedReceipt{}, false
	}
	for _, rr := range star {
		if rr.Receipt.World == world && rr.Receipt.Oracle.ID == spec.Kind && rr.Receipt.Oracle.Config == spec.Config {
			return rr, true
		}
	}
	return object.RecordedReceipt{}, false
}

// The purchase law, stated as the thing an adaptive scheduler is allowed to
// rely on: A WORLD THAT HAS NOT PAID FOR EVERY HARD GATE'S ORACLE CANNOT
// PASS. It is not a rule the scheduler maintains — it is what a SELECT from
// `Decide` MEANS, because an unpurchased hard gate has an absent required
// metric and an absent required metric fails the gate.
func TestAWorldCannotPassAHardGateItDidNotPayFor(t *testing.T) {
	ws := []object.RecordedWorld{mkWorld("a", object.OutcomeCompleted), mkWorld("b", object.OutcomeCompleted)}
	for i, pol := range policyShapes(t) {
		star := exhaustive(t, pol, ws)
		for mask := 0; mask < 1<<len(star); mask++ {
			var subset []object.RecordedReceipt
			for j := range star {
				if mask&(1<<j) != 0 {
					subset = append(subset, star[j])
				}
			}
			d := race.Decide(pol, ws, subset)
			for _, w := range ws {
				mine := forWorld(subset, w.Digest)
				unpaid := schedule.UnpaidHardGates(pol, w, mine)
				if len(unpaid) == 0 {
					continue
				}
				if passSet(pol, ws, subset)[w.Digest] {
					t.Fatalf("shape %d, subset %b: %s passed with %v unpurchased", i, mask, w.Digest, unpaid)
				}
				if d.Type == race.TypeSelect && len(d.Subject) > 0 && d.Subject[0] == w.Digest {
					t.Fatalf("shape %d, subset %b: %s was SELECTED with %v unpurchased", i, mask, w.Digest, unpaid)
				}
			}
		}
	}
}

// And the scheduler's own assertion agrees with the decision function on
// every one of those states: it never fires, which is the whole point of
// decision 9's refusal to add an S-mandatory stop clause.
func TestSchedulerPurchaseLawAssertionNeverFiresAgainstRealDecide(t *testing.T) {
	ws := []object.RecordedWorld{mkWorld("a", object.OutcomeCompleted), mkWorld("b", object.OutcomeCompleted)}
	for i, pol := range policyShapes(t) {
		star := exhaustive(t, pol, ws)
		for mask := 0; mask < 1<<len(star); mask++ {
			s, err := schedule.New(schedule.Config{
				Policy: pol, Decide: race.Decide, Batch: 1,
				Bounds: schedule.Bounds{CollectedBase: 8},
			}, ws)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			for j := range star {
				if mask&(1<<j) != 0 {
					s.Record(star[j])
				}
			}
			if v := s.PurchaseLaw(); v != "" {
				t.Fatalf("shape %d, subset %b: %s", i, mask, v)
			}
		}
	}
}

// The scheduler and the real decision function agree about ELIMINATION: a
// world the ladder stops is a world the scheduler will not buy for, and
// vice versa. One function, exported for exactly this reason.
func TestLadderStopsAgreesWithTheDecisionFunction(t *testing.T) {
	pol := policyShapes(t)[0]
	w := mkWorld("a", object.OutcomeCompleted)
	bad := map[string]int64{
		policy.MetricProtectedModified: 1, policy.MetricProtectedDeleted: 0,
		policy.MetricProtectedAdded: 0, policy.MetricHarnessModified: 0,
		policy.MetricHarnessDeleted: 0, policy.MetricHarnessAdded: 0,
		policy.MetricPathsExamined: 12,
	}
	rs := []object.RecordedReceipt{mkReceipt(t, pol, w, "guard", "fail", bad)}
	if !schedule.LadderStops(pol, rs) {
		t.Fatal("the ladder did not stop on a failed hard gate")
	}
	if passSet(pol, []object.RecordedWorld{w}, rs)[w.Digest] {
		t.Fatal("a world whose ladder stopped is in the pass set")
	}
	if d := race.Decide(pol, []object.RecordedWorld{w}, rs); d.Type == race.TypeSelect {
		t.Fatalf("decision = %s over a world that failed its first gate", d.Type)
	}
}

func forWorld(rs []object.RecordedReceipt, world string) []object.RecordedReceipt {
	out := make([]object.RecordedReceipt, 0, len(rs))
	for _, r := range rs {
		if r.Receipt.World == world {
			out = append(out, r)
		}
	}
	return out
}
