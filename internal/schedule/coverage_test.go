package schedule

// M2d.1's testing bar for the coverage module: each witness against a
// POSITIVE and a NEGATIVE hand-built trace, totality over an empty trace, a
// single-step trace, a ladder trace and a pre-M2b.2 trace, and the property
// that two computations of coverage over one trace agree byte-for-byte.
//
// The traces are HAND-BUILT rather than raced, which is the point of decision
// 8: coverage is a pure function of a recorded trace, so it is testable
// without a workspace, a python, a docker or a clock.

import (
	"encoding/json"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

// warmVOC2Trace is a `voc2` race on a warmed workspace: the reservation ran,
// the finish denominator priced every row, and |C| >= 1 on some steps and not
// on others — which is decision 5's amendment doing exactly what it says.
func warmVOC2Trace() Trace {
	return Trace{
		HasStarted: true,
		Started: Started{
			Selector: SelectorNameVOC2,
			CostTable: []CostRow{
				{Kind: "pytest-suite", Basis: CostBasisFit, N: 6},
				{Kind: "tree-guard", Basis: CostBasisFit, N: 6},
			},
		},
		Steps: []Step{
			{
				Step: 1, CommitBasis: CommitBasisReserved, Scarce: true,
				CommitSet: []string{"mv0:aaa"},
				Considered: []Considered{
					{World: "mv0:aaa", Oracle: "guard", ScoreBasis: ScoreBasisFinish,
						Admissible: true, Affordable: true, HardGate: true, AllowanceMS: 900},
					{World: "mv0:bbb", Oracle: "guard", ScoreBasis: ScoreBasisFinish,
						Admissible: true, Affordable: false, HardGate: true, AllowanceMS: 100,
						PassWithheld: true, Declined: DeclineReserved + "…"},
				},
			},
			{
				Step: 2, CommitBasis: CommitBasisReserved, Scarce: true,
				CommitSet: []string{},
				Considered: []Considered{
					{World: "mv0:bbb", Oracle: "suite", ScoreBasis: ScoreBasisFinish,
						Admissible: false, Affordable: true, HardGate: true, AllowanceMS: 400},
				},
			},
		},
		HasFinished: true,
		Finished:    Finished{Stop: StopBudget, Steps: 2},
	}
}

// coldVOC2Trace is M2b.2's own vacuous cell: nothing is priced, so
// `finish_ms` is unknown, decision 1's fallback fires on every step, and the
// revision IS M2b's rule.
func coldVOC2Trace() Trace {
	return Trace{
		HasStarted: true,
		Started: Started{
			Selector: SelectorNameVOC2,
			CostTable: []CostRow{
				{Kind: "pytest-suite", Basis: CostBasisDeclaredRank, Rank: 6},
				{Kind: "tree-guard", Basis: CostBasisDeclaredRank, Rank: 1},
			},
		},
		Steps: []Step{
			{
				Step: 1, CommitBasis: CommitBasisUnpriced + "(pytest-collect,pytest-suite,tree-guard)",
				Considered: []Considered{
					{World: "mv0:aaa", Oracle: "guard", ScoreBasis: ScoreBasisRung,
						Admissible: true, Affordable: true, HardGate: true},
				},
			},
			{
				Step: 2, CommitBasis: CommitBasisUnpriced + "(pytest-collect,pytest-suite,tree-guard)",
				Considered: []Considered{
					{World: "mv0:bbb", Oracle: "guard", ScoreBasis: ScoreBasisRung,
						Admissible: true, Affordable: true, HardGate: true},
				},
			},
		},
		HasFinished: true,
		Finished:    Finished{Stop: StopEmpty, Steps: 2},
	}
}

func TestCoverageOnAWarmedVOC2RaceReportsEveryWitnessSeparately(t *testing.T) {
	rep := Coverage(warmVOC2Trace())
	if !rep.Known || !rep.Applicable {
		t.Fatalf("a voc2 trace from this binary must be known and applicable: %+v", rep)
	}
	if rep.Rule != SelectorNameVOC2 || rep.Baseline != SelectorNameVOC {
		t.Fatalf("rule/baseline = %q/%q, want voc2/voc", rep.Rule, rep.Baseline)
	}
	if rep.Steps != 2 || rep.Consulted != 2 {
		t.Fatalf("consulted = %d of %d steps, want 2 of 2 (both steps recorded `reserved`)", rep.Consulted, rep.Steps)
	}
	// B3: EXERCISED IS DEPENDENCE. Step 1 committed a world (W3); step 2
	// lapsed a hard gate (W5). Both are differences `voc` cannot produce.
	if rep.Exercised != 2 {
		t.Fatalf("exercised = %d of %d steps, want 2 of 2 (W3 on step 1, W5 on step 2)", rep.Exercised, rep.Steps)
	}
	if rep.Vacuous() {
		t.Error("a fully exercised race reported VACUOUS")
	}
	if rep.CostRegime != CostRegimeWarm {
		t.Errorf("cost_regime = %q, want %q (every recorded row is a fit)", rep.CostRegime, CostRegimeWarm)
	}
	if rep.BudgetBoundRaces != 1 {
		t.Error("a race that stopped S-budget did not report its budget as binding")
	}
	want := map[string]int{
		WitnessReservationReserved: 1, // |C| >= 1 on step 1 only
		WitnessPassWithheld:        1, // step 1's uncommitted world
		WitnessHardGateLapsed:      1, // step 2's hard-gated, inadmissible row
		WitnessHeadMoved:           0, // one row per step, and step 1 withheld a pass
	}
	got := map[string]int{}
	for _, w := range rep.Witnesses {
		got[w.ID] = w.Steps
		if w.Total != 2 {
			t.Errorf("witness %s reports a denominator of %d, want the step count 2", w.ID, w.Total)
		}
		if w.ID == WitnessFinishDenominator {
			t.Error("W2 is still in the witness list: it is the CONSULTED set by construction, " +
				"and reporting it as an independent witness made two facts look like four")
		}
	}
	for id, n := range want {
		if got[id] != n {
			t.Errorf("witness %s fired on %d step(s), want %d", id, got[id], n)
		}
	}
	// W2 is COUNTED so its retirement stays falsifiable, and the identity it
	// is retired on must hold on every trace this binary writes.
	if rep.FinishBasisSteps != rep.Consulted || rep.W2Breaks != 0 {
		t.Errorf("W2 identity broken: finish_basis=%d consulted=%d breaks=%d",
			rep.FinishBasisSteps, rep.Consulted, rep.W2Breaks)
	}
	// THE WITNESSES ARE INDEPENDENT AND THE REPORT SAYS SO. A single
	// percentage would have hidden that the reservation stopped reserving the
	// moment the head world completed.
	if rep.CommitSetSteps != 1 {
		t.Errorf("|C| >= 1 on %d step(s), want 1: `commit_basis: reserved` names the REGIME, not the act",
			rep.CommitSetSteps)
	}
}

// THE NEGATIVE HALF. Every witness must be capable of NOT firing, or it is
// not a witness — it is a constant dressed as one.
func TestCoverageWitnessesDoNotFireOnAColdVOC2Race(t *testing.T) {
	rep := Coverage(coldVOC2Trace())
	if !rep.Known || !rep.Applicable {
		t.Fatalf("a cold voc2 trace is still a trace this binary can price: %+v", rep)
	}
	if rep.Exercised != 0 || rep.Steps != 2 {
		t.Fatalf("coverage = %d of %d, want 0 of 2: the unpriced fallback ran M2b's rule on every step",
			rep.Exercised, rep.Steps)
	}
	if !rep.Vacuous() {
		t.Fatal("a race whose rule never fired was not reported VACUOUS")
	}
	if rep.CostRegime != CostRegimeCold {
		t.Errorf("cost_regime = %q, want %q", rep.CostRegime, CostRegimeCold)
	}
	for _, w := range rep.Witnesses {
		if w.Steps != 0 {
			t.Errorf("witness %s fired %d time(s) on a cold race, where the rule never ran", w.ID, w.Steps)
		}
	}
	// The refusal must NAME what it observed rather than assert an absence.
	reason := rep.VacuityReason()
	for _, want := range []string{CommitBasisUnpriced, "pytest-suite", SelectorNameVOC2} {
		if !contains(reason, want) {
			t.Errorf("the vacuity reason does not name %q:\n%s", want, reason)
		}
	}
	if len(rep.VacuityBanner()) == 0 {
		t.Error("the vacuity banner is empty")
	}
}

// DECISION 5's SECOND ROW: a budgeted comparison at a budget that never binds
// is a comparison of two arms that are both the exhaustive ladder. Nothing in
// the harness said so before this block.
func TestCoverageOfVOCIsZeroWhenTheBudgetNeverBinds(t *testing.T) {
	tr := Trace{
		HasStarted: true,
		Started:    Started{Selector: SelectorNameVOC, CostTable: []CostRow{{Kind: "tree-guard", Basis: CostBasisFit, N: 4}}},
		Steps: []Step{{
			Step: 1, CommitBasis: CommitBasisNotScarce,
			Considered: []Considered{
				{World: "mv0:aaa", Oracle: "guard", ScoreBasis: ScoreBasisRung, Admissible: true, Affordable: true},
			},
		}},
		HasFinished: true,
		Finished:    Finished{Stop: StopEmpty},
	}
	rep := Coverage(tr)
	if rep.Rule != SelectorNameVOC || rep.Baseline == "" {
		t.Fatalf("a voc trace must name its baseline: %+v", rep)
	}
	if rep.Exercised != 0 || !rep.Vacuous() {
		t.Fatalf("voc coverage = %d of %d, want 0 of 1 at a budget that never bound", rep.Exercised, rep.Steps)
	}
	if rep.BudgetBoundRaces != 0 {
		t.Error("a race that stopped S-empty with every row affordable reported its budget as binding")
	}

	// And it fires the moment the budget does bind.
	tr.Steps[0].Considered[0].Affordable = false
	rep = Coverage(tr)
	if rep.Exercised != 1 || rep.Vacuous() {
		t.Fatalf("voc coverage = %d of %d, want 1 of 1 once a row is admissible and unaffordable",
			rep.Exercised, rep.Steps)
	}
}

// TOTALITY, and the three answers that are NOT a percentage.
func TestCoverageIsTotalAndReportsAbsenceAsAbsence(t *testing.T) {
	// An empty trace: no allocation trace recorded.
	empty := Coverage(Trace{})
	if empty.Vacuous() {
		t.Error("a race with no trace at all was reported VACUOUS: absent source implies absent metric")
	}
	if empty.Summary() != CoverageNoTrace {
		t.Errorf("empty trace summary = %q, want %q", empty.Summary(), CoverageNoTrace)
	}

	// A LADDER trace: it computes no scarcity test and no score at all. The
	// ARM answers this before the ERA is consulted, or a current binary's
	// ladder race would be dated to a binary that never existed.
	ladder := Coverage(Trace{
		HasStarted: true,
		Started:    Started{Selector: SelectorNameLadder},
		Steps: []Step{{Step: 1, Considered: []Considered{
			{World: "mv0:aaa", Oracle: "guard", Order: 1},
		}}},
	})
	if ladder.Summary() != CoverageNotApplicable {
		t.Errorf("ladder summary = %q, want %q", ladder.Summary(), CoverageNotApplicable)
	}
	if ladder.Vacuous() {
		t.Error("a ladder race was reported VACUOUS: a gate that refuses every run is a rubber stamp")
	}

	// A PRE-M2b.2 trace: neither commit_basis nor score_basis was ever
	// recorded, so coverage is UNKNOWN and never 0.
	old := Coverage(Trace{
		HasStarted: true,
		Started:    Started{Selector: SelectorNameVOC},
		Steps: []Step{{Step: 1, Considered: []Considered{
			{World: "mv0:aaa", Oracle: "guard", Flip: 1},
		}}},
	})
	if old.Summary() != CoverageUnknownPreM2b2 {
		t.Errorf("pre-M2b.2 summary = %q, want %q", old.Summary(), CoverageUnknownPreM2b2)
	}
	if old.Vacuous() || old.Percent() != 0 || old.Known {
		t.Errorf("a pre-M2b.2 trace was priced rather than reported unknown: %+v", old)
	}

	// A single-step trace with no rows at all.
	one := Coverage(Trace{
		HasStarted: true,
		Started:    Started{Selector: SelectorNameVOC2},
		Steps:      []Step{{Step: 1, CommitBasis: CommitBasisReserved, CommitSet: []string{"mv0:aaa"}}},
	})
	if one.Steps != 1 || one.Exercised != 1 {
		t.Errorf("single-step trace: coverage = %d of %d, want 1 of 1", one.Exercised, one.Steps)
	}
}

// DECISION 8: coverage is a PURE FUNCTION of a trace, so two computations
// over one trace agree byte-for-byte. If they did not, a run's coverage would
// be a property of when it was asked rather than of what it recorded.
func TestCoverageIsDeterministicOverOneTrace(t *testing.T) {
	tr := warmVOC2Trace()
	a, err := json.Marshal(Coverage(tr))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b, err := json.Marshal(Coverage(tr))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("two computations of coverage over one trace disagree:\n%s\n%s", a, b)
	}
}

// MERGING NEVER POOLS AN ABSENCE INTO THE PERCENTAGE. A set of races that
// were all pre-M2b.2 is still `unknown`, and a set that mixes warm and cold
// cost tables refuses to call itself either (decision 11).
func TestMergeCoverageKeepsAbsencesOutOfThePercentage(t *testing.T) {
	warm := Coverage(warmVOC2Trace())
	cold := Coverage(coldVOC2Trace())
	m := MergeCoverage([]CoverageReport{warm, cold})
	if m.Steps != 4 || m.Exercised != 2 {
		t.Fatalf("merged coverage = %d of %d, want 2 of 4", m.Exercised, m.Steps)
	}
	if m.Races != 2 || m.RacesExercised != 1 {
		t.Fatalf("merged races = %d of %d exercised, want 1 of 2", m.RacesExercised, m.Races)
	}
	if m.Vacuous() {
		t.Error("a merged set with one exercised race was reported VACUOUS: a MEASURED-NULL must be publishable")
	}
	if m.CostRegime != "mixed(cold,warm)" {
		t.Errorf("merged cost_regime = %q, want the mixed label: a table may never pool warm and cold rows",
			m.CostRegime)
	}

	onlyOld := MergeCoverage([]CoverageReport{
		{Races: 1, Unknown: 1, Rule: SelectorNameVOC2, Baseline: SelectorNameVOC},
		{Races: 1, Unknown: 1, Rule: SelectorNameVOC2, Baseline: SelectorNameVOC},
	})
	if onlyOld.Vacuous() {
		t.Error("a set of pre-M2b.2 traces was reported VACUOUS rather than unknown")
	}
	if onlyOld.Summary() != CoverageUnknownPreM2b2 {
		t.Errorf("summary = %q, want %q", onlyOld.Summary(), CoverageUnknownPreM2b2)
	}
}

// DECISION 6: EXERCISED and DIVERGENT are two different questions. The
// purchase order is keyed on the candidate ORDINAL and never on the world
// digest, or every run would report "different subject" including the null
// case where the arms agree perfectly.
func TestPurchaseOrderIsKeyedOnTheOrdinalAndNotTheDigest(t *testing.T) {
	tr := Trace{Steps: []Step{
		{Step: 1, Considered: []Considered{
			{World: "mv0:run1aaa", Oracle: "guard"},
			{World: "mv0:run1bbb", Oracle: "guard", Declined: "not this batch"},
		}},
		{Step: 2, Considered: []Considered{{World: "mv0:run1bbb", Oracle: "guard"}}},
	}}
	other := Trace{Steps: []Step{
		{Step: 1, Considered: []Considered{{World: "mv0:run2xxx", Oracle: "guard"}}},
		{Step: 2, Considered: []Considered{{World: "mv0:run2yyy", Oracle: "guard"}}},
	}}
	a := PurchaseOrder(tr, map[string]int{"mv0:run1aaa": 1, "mv0:run1bbb": 2})
	b := PurchaseOrder(other, map[string]int{"mv0:run2xxx": 1, "mv0:run2yyy": 2})
	if !SameOrder(a, b) {
		t.Fatalf("two runs that bought the same ordinals in the same order diverged: %v vs %v", a, b)
	}
	if len(a) != 2 || a[0] != "1.guard" || a[1] != "2.guard" {
		t.Fatalf("purchase order = %v, want [1.guard 2.guard]", a)
	}
	// An unknown ordinal is reported as unknown rather than guessed at.
	if got := PurchaseOrder(tr, nil); got[0] != "?.guard" {
		t.Fatalf("a world with no known ordinal rendered %q, want %q", got[0], "?.guard")
	}
}

// The era test lives in ONE place now (decision 8), and it still separates
// the eras by the finishing rule's own vocabulary rather than by
// `adaptive_rule` — which normalizes to "voc" for a pre-M2b.2 ledger AND for a
// race run this morning.
func TestPreM2b2ReadsTheFinishingRulesOwnVocabulary(t *testing.T) {
	older := Trace{Steps: []Step{{Step: 1, Considered: []Considered{{World: "w", Flip: 1}}}}}
	if !PreM2b2(older) {
		t.Error("a trace with no commit_basis and no score_basis was not dated pre-M2b.2")
	}
	current := Trace{Steps: []Step{{Step: 1, Considered: []Considered{{World: "w", ScoreBasis: ScoreBasisRung}}}}}
	if PreM2b2(current) {
		t.Error("a voc race from this binary, which records score_basis, was dated pre-M2b.2")
	}
	// And `adaptive_rule` may not be used to date it: this binary defaults to
	// voc, so the field reads "voc" in both eras.
	dated := Trace{
		HasStarted: true,
		Started:    Started{Constants: Constants{AdaptiveRule: SelectorNameVOC}},
		Steps:      []Step{{Step: 1, CommitBasis: CommitBasisNotScarce}},
	}
	if PreM2b2(dated) {
		t.Error("a current trace whose adaptive_rule is `voc` was dated pre-M2b.2")
	}
}

// DECISION 5's LOAD-BEARING PROPERTY, PINNED RATHER THAN BELIEVED.
//
// The inertness predicate is a CLAIM about the arm — "on a step where this
// holds, voc2 is voc" — and a coverage number computed from a false claim is
// worse than no coverage number, because it looks like a measurement. So the
// claim is generated over the same fixture M2b.2's own `voc2 ≡ voc` test uses
// (testPolicy, fixedCosts, the §1.1 ladder) across budgets, world counts and
// control-plane orders: while every step so far has been declared inert, the
// two arms must have bought the IDENTICAL purchases in the IDENTICAL order.
//
// It is stated as a PREFIX property on purpose. Once a step is exercised the
// two arms may legitimately diverge, and every later step of the voc2 race is
// then reasoning over a different history — so comparing them would test
// nothing. The first exercised step is where the comparison stops, and that
// boundary is exactly what decision 6's `INERTNESS VIOLATED` row watches from
// the other side.
func TestInertVOC2StepsBuyExactlyWhatVOCBuys(t *testing.T) {
	pol := testPolicy(t)
	a, b, c := world(t, "a"), world(t, "b"), world(t, "c")
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	sets := [][]object.RecordedWorld{
		{a},
		{a, b},
		{a, b, c},
	}
	orders := [][]string{
		nil,
		{c.Digest, a.Digest, b.Digest},
		{b.Digest, c.Digest, a.Digest},
	}
	budgets := []int64{0, 1, 300, worldLadder / 2, worldLadder, raceBudgetMS, 3 * worldLadder, 10 * worldLadder}

	stepsOf := func(ws []object.RecordedWorld, order []string, budget int64, sel Selector) []Step {
		s := newSched(t, pol, Config{
			Bounds: bounds, Costs: costs, BudgetMS: budget, Selector: sel,
			Order: order, BudgetBasis: BudgetBasisPredicted,
		}, ws...)
		steps, _ := drain(t, pol, s, ws)
		return steps
	}
	chosenOf := func(s Step) string {
		out := ""
		for _, ch := range s.Chosen {
			out += ch.World + "/" + ch.Oracle + ","
		}
		return out
	}

	inertSteps, exercisedSteps := 0, 0
	for _, ws := range sets {
		for _, order := range orders {
			if order != nil && len(order) != 3 {
				continue
			}
			for _, budget := range budgets {
				two := stepsOf(ws, order, budget, SelectorVOC2())
				one := stepsOf(ws, order, budget, SelectorVOC())
				for i, s := range two {
					if !InertVOC2(s) {
						exercisedSteps++
						break
					}
					inertSteps++
					if i >= len(one) {
						t.Fatalf("worlds=%d order=%v budget=%d: voc2 took %d steps while inert, voc took %d",
							len(ws), order, budget, len(two), len(one))
					}
					if got, want := chosenOf(s), chosenOf(one[i]); got != want {
						t.Fatalf("worlds=%d order=%v budget=%d step %d is DECLARED INERT "+
							"(commit_basis %q) but voc2 bought %q where voc bought %q",
							len(ws), order, budget, s.Step, s.CommitBasis, got, want)
					}
				}
			}
		}
	}
	// A property test that never reached either branch would be a green light
	// nobody earned. Both must be exercised by the generator itself.
	if inertSteps == 0 {
		t.Fatal("the generator produced no inert step: the predicate was never tested")
	}
	if exercisedSteps == 0 {
		t.Fatal("the generator produced no EXERCISED step: the predicate is a constant true here, " +
			"so this test would pass against a broken definition of inertness")
	}
	t.Logf("inertness property: %d inert steps compared, %d races reached an exercised step",
		inertSteps, exercisedSteps)
}

// The recorded allowance must be the number the affordability predicate
// actually tested against — the same number the arm's own `reserved:` /
// `unreachable:` sentence prints. A field that merely looked plausible would
// be worse than no field, because a reader would derive coverage from it.
func TestRecordedAllowanceIsTheNumberAffordabilityTested(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)
	s := newSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: raceBudgetMS, Selector: SelectorVOC2(),
		BudgetBasis: BudgetBasisPredicted,
	}, ws...)
	steps, _ := drain(t, pol, s, ws)
	seen := 0
	for _, st := range steps {
		for _, r := range st.Considered {
			if r.AllowanceMS <= 0 {
				continue
			}
			seen++
			// The predicate is `cost <= allowance` and nothing else, so a row
			// priced at or below its allowance must be affordable and one above
			// it must not be.
			if want := r.CostMS <= r.AllowanceMS; r.Affordable != want {
				t.Errorf("step %d %s/%s: cost %d ms against allowance %d ms, affordable=%v",
					st.Step, r.World, r.Oracle, r.CostMS, r.AllowanceMS, r.Affordable)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no row recorded an allowance: the field is dead and coverage would read a zero")
	}
}

// ---------------------------------------------------------------------------
// BLOCKER B3 — CONSULTED IS NOT EXERCISED.
//
// `commit_basis` becomes `reserved` the moment `scarce` is true, BEFORE the
// commit set is built. A step whose commit set is empty allocates by
// `equalShare(pool, |frontier|)` — M2b decision 8 through the same arithmetic
// — declines nothing decision 5 would not have declined, and withholds no
// pass outcome. It is `voc`'s step in every observable respect, and the first
// version of this file counted it as coverage.
// ---------------------------------------------------------------------------

// scarceButIdenticalToVOC is the step the reviewer measured: `reserved` on the
// wire, |C| = 0, nothing withheld, no hard gate lapsed, and the finish
// denominator agreeing with the rung denominator about who goes first.
//
// The two rows are constructed so that BOTH orderings put mv0:aaa at the head:
// under finish, aaa is cheaper to complete (300 < 900); under M2b's rung
// denominator, aaa scores 5000*1000/100 = 50 000 against bbb's
// 5000*1000/200 = 25 000. Same head, both bases.
func scarceButIdenticalToVOC() Step {
	return Step{
		Step: 1, CommitBasis: CommitBasisReserved, Scarce: true, CommitSet: []string{},
		UncommittedMS: 1000,
		Considered: []Considered{
			{World: "mv0:aaa", Oracle: "guard", ScoreBasis: ScoreBasisFinish,
				Admissible: true, Affordable: true, HardGate: true,
				ValueBP: 5000, CostMS: 100, FinishMS: 300, AllowanceMS: 500},
			{World: "mv0:bbb", Oracle: "guard", ScoreBasis: ScoreBasisFinish,
				Admissible: true, Affordable: true, HardGate: true,
				ValueBP: 5000, CostMS: 200, FinishMS: 900, AllowanceMS: 500},
		},
	}
}

func TestB3AScarceStepThatAllocatesLikeVOCIsConsultedAndNotExercised(t *testing.T) {
	s := scarceButIdenticalToVOC()
	if InertVOC2(s) {
		t.Fatal("a `reserved` step is CONSULTED: the rule's own code path ran and the predicate must say so")
	}
	if DependedVOC2(s) {
		t.Fatal("BLOCKER B3: a step with |C| = 0, nothing withheld, no lapsed gate and an unmoved " +
			"queue head allocates EXACTLY as M2b decision 8 does, and it was counted as exercised")
	}

	tr := Trace{
		HasStarted:  true,
		Started:     Started{Selector: SelectorNameVOC2, CostTable: []CostRow{{Kind: "tree-guard", Basis: CostBasisFit, N: 6}}},
		Steps:       []Step{s},
		HasFinished: true,
		Finished:    Finished{Stop: StopBudget, Steps: 1},
	}
	rep := Coverage(tr)
	if rep.Consulted != 1 {
		t.Errorf("consulted = %d of %d, want 1: the rule ran", rep.Consulted, rep.Steps)
	}
	if rep.Exercised != 0 {
		t.Errorf("exercised = %d of %d, want 0: the rule changed nothing it allocated", rep.Exercised, rep.Steps)
	}
	if !rep.Vacuous() {
		t.Error("a race that consulted the rule on every step and depended on it on none was not VACUOUS: " +
			"the refusal keys on EXERCISED, or B3's inflated number gates the refusal too")
	}
	// The two refusals are different sentences and the harness must print the
	// one that is true: this run is not "the rule never ran".
	reason := rep.VacuityReason()
	if contains(reason, "never fired") {
		t.Errorf("the refusal claims the rule never fired on a run where it ran on every step:\n%s", reason)
	}
	if !contains(reason, "CHANGED NOTHING IT ALLOCATED") {
		t.Errorf("the refusal does not say what actually happened:\n%s", reason)
	}
	// Both figures are printed, always, and the headline is the smaller one.
	lines := joinedLines(rep.Lines())
	for _, want := range []string{"steps EXERCISED", "steps consulted"} {
		if !contains(lines, want) {
			t.Errorf("the coverage block omits %q:\n%s", want, lines)
		}
	}
}

// EACH DEPENDENCE TERM ON ITS OWN, positive and negative, from the same step
// that is otherwise identical to `voc`'s. A term that cannot flip the answer
// by itself is not a term.
func TestB3EachDependenceTermPromotesConsultedToExercised(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Step)
	}{
		{"W3 the reservation reserved", func(s *Step) { s.CommitSet = []string{"mv0:aaa"} }},
		{"W4 a pass outcome was withheld", func(s *Step) { s.Considered[1].PassWithheld = true }},
		{"W5 the hard-gate override lapsed", func(s *Step) { s.Considered[1].Admissible = false }},
		{"W6 the head of the queue moved", func(s *Step) {
			// M2b's denominator strictly prefers the SECOND row: it costs 10 ms
			// for the same value, so it scores 500 000 against the head's
			// 50 000 — while the finish denominator kept it second because its
			// world is the expensive one to complete.
			s.Considered[1].CostMS = 10
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := scarceButIdenticalToVOC()
			if DependedVOC2(base) {
				t.Fatal("the control step already depends on the rule; the term proves nothing")
			}
			s := scarceButIdenticalToVOC()
			tc.mut(&s)
			if InertVOC2(s) {
				t.Fatal("the mutation made the step inert, so it is not testing dependence")
			}
			if !DependedVOC2(s) {
				t.Fatalf("%s did not make the step exercised", tc.name)
			}
		})
	}
}

// W6 IS SOUND OR IT IS NOTHING. It compares only against the head, only
// strictly, only when every row was priced by the finish denominator, and
// never when a withheld pass outcome means `value_bp` is not the number the
// baseline would have computed.
func TestB3HeadMovedIsRefusedWhereItCannotBeComputed(t *testing.T) {
	inverted := func() Step {
		s := scarceButIdenticalToVOC()
		s.Considered[1].CostMS = 10 // M2b strictly prefers row 1
		return s
	}
	if !HeadMovedByFinish(inverted()) {
		t.Fatal("the positive control does not fire")
	}

	// A row the rung denominator priced: the recorded order IS voc's order.
	rung := inverted()
	rung.Considered[0].ScoreBasis = ScoreBasisRung
	rung.Considered[1].ScoreBasis = ScoreBasisRung
	if HeadMovedByFinish(rung) {
		t.Error("W6 fired on a step the RUNG denominator priced, where the recorded order is the baseline's")
	}

	// An unpriced row: `score_rank` orders it and `score_bpps` does not exist.
	unpriced := inverted()
	unpriced.Considered[1].ScoreRank = 2
	if HeadMovedByFinish(unpriced) {
		t.Error("W6 fired on a step holding an unpriced row, where M2b's score is not even the sort key")
	}

	// A withheld pass outcome: `value_bp` is the fail-closed bracket's, so
	// recomputing M2b's score from it would compare against a baseline nobody
	// ran. W4 already carries that step.
	withheld := inverted()
	withheld.Considered[1].PassWithheld = true
	if HeadMovedByFinish(withheld) {
		t.Error("W6 recomputed the baseline score from a value_bp the baseline would not have computed")
	}
	if !DependedVOC2(withheld) {
		t.Error("the withheld step lost its dependence when W6 refused: W4 must carry it")
	}

	// A single-row step has no head to move.
	if HeadMovedByFinish(Step{Considered: []Considered{{ScoreBasis: ScoreBasisFinish}}}) {
		t.Error("W6 fired on a one-row frontier")
	}
	if HeadMovedByFinish(Step{}) {
		t.Error("W6 is not total on an empty step")
	}
}

// EXERCISED ⊆ CONSULTED, on every trace, or the split is not a split.
func TestB3ExercisedNeverExceedsConsulted(t *testing.T) {
	for _, tr := range []Trace{warmVOC2Trace(), coldVOC2Trace(), {}} {
		rep := Coverage(tr)
		if rep.Exercised > rep.Consulted {
			t.Fatalf("exercised %d > consulted %d for rule %q", rep.Exercised, rep.Consulted, rep.Rule)
		}
	}
}

// ---------------------------------------------------------------------------
// BLOCKER B4 (second half) — W2 WAS THE CONSULTED SET UNDER ANOTHER NAME.
// ---------------------------------------------------------------------------

func TestB4W2IsRetiredAndItsIdentityIsAsserted(t *testing.T) {
	rep := Coverage(warmVOC2Trace())
	for _, w := range rep.Witnesses {
		if w.ID == WitnessFinishDenominator {
			t.Fatal("W2 is still reported as an independent witness")
		}
	}
	if rep.FinishBasisSteps != rep.Consulted {
		t.Fatalf("score_basis=finish on %d step(s) but consulted on %d: the identity that retires W2 "+
			"does not hold, and the report must say so rather than re-derive a witness from it",
			rep.FinishBasisSteps, rep.Consulted)
	}
	if rep.W2Breaks != 0 {
		t.Fatalf("W2Breaks = %d on a trace this binary could have written", rep.W2Breaks)
	}
	lines := joinedLines(rep.Lines())
	if !contains(lines, "RETIRED AS A WITNESS") {
		t.Errorf("W2's retirement is not printed; a witness that silently vanishes reads as one that "+
			"stopped firing:\n%s", lines)
	}

	// AND THE ASSERTION IS FALSIFIABLE. A trace where the two sets disagree
	// is reported, not smoothed.
	broken := warmVOC2Trace()
	broken.Steps[1].CommitBasis = CommitBasisNotScarce // inert, yet score_basis stays finish
	b := Coverage(broken)
	if b.W2Breaks != 1 {
		t.Fatalf("W2Breaks = %d on a trace where the sets disagree, want 1", b.W2Breaks)
	}
	if !contains(joinedLines(b.Lines()), "W2 IDENTITY BROKEN") {
		t.Error("a broken identity is not printed")
	}
}

// ---------------------------------------------------------------------------
// BLOCKER B4 (first half) — A POOLED NUMERATOR IS NOT A COVERAGE FIGURE.
// ---------------------------------------------------------------------------

// The reviewer's own probe: 99 vacuous races merged with one race holding one
// exercised step printed `1 of 199 steps (0%)` and `vacuous=false`.
func TestB4APooledNumeratorCarriesItsVacuousPartsAndNeverPrintsAFlooredZero(t *testing.T) {
	var parts []CoverageReport
	for i := 0; i < 99; i++ {
		parts = append(parts, CoverageReport{
			Rule: SelectorNameVOC2, Baseline: SelectorNameVOC, Known: true, Applicable: true,
			Races: 1, Steps: 2, Consulted: 2, Exercised: 0,
		})
	}
	parts = append(parts, CoverageReport{
		Rule: SelectorNameVOC2, Baseline: SelectorNameVOC, Known: true, Applicable: true,
		Races: 1, RacesConsulted: 1, RacesExercised: 1, Steps: 1, Consulted: 1, Exercised: 1,
	})
	m := MergeCoverage(parts)
	if m.Steps != 199 || m.Exercised != 1 {
		t.Fatalf("merged = %d of %d, want 1 of 199", m.Exercised, m.Steps)
	}
	if m.VacuousParts != 99 {
		t.Fatalf("vacuous parts = %d, want 99: pooling is exactly the operation that hides them", m.VacuousParts)
	}
	if !m.AnyVacuous() {
		t.Fatal("BLOCKER B4: a pool of 99 vacuous reports and one exercised step did not report " +
			"any vacuity at all")
	}
	// ABSENCE, NOT A FLOORED ZERO. `1 of 199` is not 0 %.
	got := m.Summary()
	if contains(got, "(0%)") {
		t.Fatalf("a nonzero numerator printed as 0%%: %q", got)
	}
	if !contains(got, "<1%") {
		t.Fatalf("summary = %q, want the `<1%%` form", got)
	}
	if !contains(joinedLines(m.Lines()), "VACUOUS PARTS") {
		t.Error("the pooled block does not name its vacuous parts")
	}
	// Zero really is zero, and still prints as such.
	z := CoverageReport{Rule: SelectorNameVOC2, Known: true, Applicable: true, Races: 1, Steps: 4}
	if !contains(z.Summary(), "(0%)") {
		t.Errorf("a genuinely zero numerator did not print 0%%: %q", z.Summary())
	}
}

func joinedLines(ls []string) string {
	out := ""
	for _, l := range ls {
		out += l + "\n"
	}
	return out
}
