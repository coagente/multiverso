package eval

// THE METRIC ARITHMETIC, ON CONSTRUCTED DECISION/LABEL PAIRS.
//
// Every case here is a hand-built (decision, label) pair, which is the only way
// to test arithmetic whose inputs are otherwise produced by a race: a test that
// raced would be testing the racer.

import (
	"fmt"
	"math"
	"testing"

	"github.com/coagente/multiverso/internal/race"
)

// row is a terse builder so a table of cases reads as a table.
func row(arm, inst, fam, decision, winner string, avail bool) Row {
	return Row{
		Instance: inst, Arm: arm, Family: fam, Tier: Tier1,
		Decision: decision, Stable: true, Replicates: 3, ModalCount: 3,
		WinnerLabel: winner, Avail: avail,
		DStar: race.TypeSelect, DStarWinnerLabel: VerdictCorrect,
		BoundAvailable: true, MinSpendMS: 100, BudgetMS: 1000,
	}
}

func TestTCARAndFARArithmetic(t *testing.T) {
	// Four instances: one admitted-correct, one admitted-incorrect, one
	// rejected with a correct candidate available, one escalated with nothing
	// available.
	rows := []Row{
		row("a", "i1", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true),
		row("a", "i2", FamilyGoldPresent, race.TypeSelect, VerdictIncorrect, true),
		row("a", "i3", FamilyGoldPresent, race.TypeReject, "", true),
		row("a", "i4", FamilyAllWrong, race.TypeEscalate, "", false),
	}
	m := Compute("a", rows)
	if m.Instances != 4 {
		t.Fatalf("instances = %d, want 4", m.Instances)
	}
	// TCAR's denominator is INSTANCES ATTEMPTED.
	if m.TCAR.Num != 1 || m.TCAR.Den != 4 {
		t.Errorf("TCAR = %s, want 1/4 (denominator: instances)", m.TCAR)
	}
	// FAR's denominator is ADMISSIONS. Two instances were admitted.
	if m.FAR.Low.Num != 1 || m.FAR.Low.Den != 2 {
		t.Errorf("FAR = %s, want 1/2 (denominator: admissions)", m.FAR)
	}
	if !m.FAR.Point() {
		t.Errorf("FAR should be a point estimate with no unscored admissions, got %s", m.FAR)
	}
	if m.ESC.Num != 1 || m.ESC.Den != 4 {
		t.Errorf("ESC = %s, want 1/4", m.ESC)
	}
	// The single escalation had nothing available, so it was justified.
	if v, ok := m.ESCJust.Value(); !ok || v != 1 {
		t.Errorf("ESC_just = %s, want 1/1", m.ESCJust)
	}
	// coverage: three of four instances had a correct candidate.
	if m.Coverage.Num != 3 || m.Coverage.Den != 4 {
		t.Errorf("coverage = %s, want 3/4", m.Coverage)
	}
	// FRR_label: of the three instances where a correct candidate existed,
	// one was rejected. (i4 escalated but had nothing available.)
	if m.FRRLabel.Num != 1 || m.FRRLabel.Den != 3 {
		t.Errorf("FRR_label = %s, want 1/3", m.FRRLabel)
	}
}

func TestZeroAdmissionsHasNoFARAtAll(t *testing.T) {
	// Decision 9(c). An arm that admitted nothing has NO denominator, and
	// "0 % FAR" for an arm that never acted is the single most misleading
	// number this harness could print.
	rows := []Row{
		row("cautious", "i1", FamilyAllWrong, race.TypeReject, "", false),
		row("cautious", "i2", FamilyAllWrong, race.TypeReject, "", false),
	}
	m := Compute("cautious", rows)
	if m.FAR.Present {
		t.Fatalf("FAR is present with zero admissions: %s", m.FAR)
	}
	if got := m.FAR.String(); got != "—" {
		t.Errorf("FAR renders as %q, want the absent marker", got)
	}
	if v, ok := m.FAR.Low.Value(); ok {
		t.Errorf("an absent FAR yielded a value %v", v)
	}
}

func TestEscalateCostsTCARLikeRejectAndFARNotAtAll(t *testing.T) {
	// Decision 9(b), and it is made against the adaptive arm's interest on
	// purpose: escalating costs exactly as much TCAR as rejecting, and moves
	// FAR not at all.
	rej := []Row{
		row("r", "i1", FamilyGoldPresent, race.TypeReject, "", true),
		row("r", "i2", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true),
	}
	esc := []Row{
		row("e", "i1", FamilyGoldPresent, race.TypeEscalate, "", true),
		row("e", "i2", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true),
	}
	mr, me := Compute("r", rej), Compute("e", esc)
	if mr.TCAR != me.TCAR {
		t.Errorf("TCAR differs between REJECT (%s) and ESCALATE (%s): "+
			"escalation must cost exactly as much TCAR as rejection", mr.TCAR, me.TCAR)
	}
	if mr.FAR != me.FAR {
		t.Errorf("FAR differs between REJECT (%s) and ESCALATE (%s): "+
			"neither may enter FAR's denominator", mr.FAR, me.FAR)
	}
	// And an arm cannot drive FAR to zero by escalating everything, because
	// there is no FAR at all in that case.
	all := []Row{
		row("all-esc", "i1", FamilyGoldPresent, race.TypeEscalate, "", true),
		row("all-esc", "i2", FamilyGoldPresent, race.TypeEscalate, "", true),
	}
	if m := Compute("all-esc", all); m.FAR.Present {
		t.Errorf("escalating everything produced a FAR of %s instead of nothing", m.FAR)
	}
}

func TestUnknownWidensFARMonotonically(t *testing.T) {
	base := []Row{
		row("a", "i1", FamilyGoldPresent, race.TypeSelect, VerdictIncorrect, true),
		row("a", "i2", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true),
	}
	m0 := Compute("a", base)
	if !m0.FAR.Point() {
		t.Fatalf("FAR with no unknowns is not a point: %s", m0.FAR)
	}
	widths := []float64{0}
	for k := 1; k <= 3; k++ {
		rows := append([]Row(nil), base...)
		for j := 0; j < k; j++ {
			rows = append(rows, row("a", fmt.Sprintf("u%d", j), FamilyGoldPresent,
				race.TypeSelect, VerdictUnknown, true))
		}
		m := Compute("a", rows)
		if m.UnscoredAdmissions != k {
			t.Errorf("k=%d: unscored_admissions = %d", k, m.UnscoredAdmissions)
		}
		lo, _ := m.FAR.Low.Value()
		hi, _ := m.FAR.High.Value()
		w := hi - lo
		if w < widths[len(widths)-1]-1e-12 {
			t.Errorf("k=%d: FAR interval width %v shrank below %v", k, w, widths[len(widths)-1])
		}
		if m.FAR.Point() {
			t.Errorf("k=%d: FAR printed a point estimate with %d unscored admissions", k, k)
		}
		widths = append(widths, w)
	}
}

func TestRegretDecompositionSumsExactly(t *testing.T) {
	// One instance in each bucket, plus two more, and the identity must close
	// on the nose. An identity that does not close is a bug in the harness,
	// not a result.
	hit := row("a", "hit", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true)

	// A family-B row must be SELF-CONSISTENT: if no candidate is correct then
	// full evidence cannot select a correct one either. Encoding the
	// impossible row (¬avail with d* = SELECT(correct)) would make the test
	// assert arithmetic over a state no race can produce.
	gen := row("a", "gen", FamilyAllWrong, race.TypeReject, "", false)
	gen.DStarWinnerLabel = VerdictIncorrect

	gates := row("a", "gates", FamilyGoldPresent, race.TypeReject, "", true)
	gates.DStar = race.TypeReject // full evidence would not select it either

	rank := row("a", "rank", FamilyGoldPresent, race.TypeSelect, VerdictIncorrect, true)

	allocAvoidable := row("a", "alloc-ok", FamilyGoldPresent, race.TypeReject, "", true)
	allocAvoidable.MinSpendMS = 500
	allocAvoidable.BudgetMS = 1000

	allocUnwinnable := row("a", "alloc-poor", FamilyGoldPresent, race.TypeEscalate, "", true)
	allocUnwinnable.MinSpendMS = 5000
	allocUnwinnable.BudgetMS = 1000

	allocNoBound := row("a", "alloc-nobound", FamilyGoldPresent, race.TypeReject, "", true)
	allocNoBound.BoundAvailable = false

	unscored := row("a", "unscored", FamilyGoldPresent, race.TypeSelect, VerdictUnknown, true)

	rows := []Row{hit, gen, gates, rank, allocAvoidable, allocUnwinnable, allocNoBound, unscored}
	m := Compute("a", rows)
	g := m.Regret
	if err := g.Closes(); err != nil {
		t.Fatalf("%v", err)
	}
	want := map[string]int{
		BucketHit: 1, BucketGeneration: 1, BucketGates: 1, BucketRanking: 1,
		BucketAllocation: 3, BucketUnscored: 1,
	}
	for k, v := range want {
		if m.Buckets[k] != v {
			t.Errorf("bucket %s = %d, want %d (buckets: %v)", k, m.Buckets[k], v, m.Buckets)
		}
	}
	if g.AllocationAvoidable != 1 || g.AllocationUnwinnable != 1 || g.AllocationUnknownBound != 1 {
		t.Errorf("allocation split = (%d, %d, %d), want (1, 1, 1)",
			g.AllocationAvoidable, g.AllocationUnwinnable, g.AllocationUnknownBound)
	}
	// FRR_reachable is the number the M2b.1 BUILDLOG asked for: a correct
	// candidate was there, full evidence would have selected it, and the money
	// was in the arm's pocket. Its DENOMINATOR is the four rows where
	// d* = SELECT(correct) AND the bound exists AND minspend <= B — hit, rank,
	// alloc-ok and unscored. `gen` is out because full evidence selects
	// nothing correct there, `gates` because d* is a REJECT, `alloc-poor`
	// because minspend > B (unwinnable by ANY allocator), and `alloc-nobound`
	// because the bound refused and an enumeration limit must not become a
	// scheduling number. Of those four, only alloc-ok was rejected.
	if !m.FRRReachable.Present {
		t.Fatalf("FRR_reachable is absent")
	}
	if m.FRRReachable.Num != 1 || m.FRRReachable.Den != 4 {
		t.Errorf("FRR_reachable = %s, want 1/4", m.FRRReachable)
	}
	// FRR_gates is restricted to instances where a correct candidate existed
	// and full evidence would still not have selected one: `gates` alone.
	if m.FRRGates.Num != 1 || m.FRRGates.Den != 1 {
		t.Errorf("FRR_gates = %s, want 1/1", m.FRRGates)
	}
}

func TestRegretClosesOverRandomTables(t *testing.T) {
	// A property test rather than a case: over every combination of decision,
	// label, availability, d* and affordability, the identity must close.
	decisions := []string{race.TypeSelect, race.TypeReject, race.TypeEscalate}
	labels := []string{VerdictCorrect, VerdictIncorrect, VerdictUnknown, ""}
	dstars := []string{race.TypeSelect, race.TypeReject, race.TypeEscalate}
	dlabels := []string{VerdictCorrect, VerdictIncorrect, VerdictUnknown, ""}
	n := 0
	var rows []Row
	for _, d := range decisions {
		for _, l := range labels {
			for _, ds := range dstars {
				for _, dl := range dlabels {
					for _, avail := range []bool{true, false} {
						for _, bound := range []bool{true, false} {
							for _, poor := range []bool{true, false} {
								r := row("a", fmt.Sprintf("i%d", n), FamilyMixed, d, l, avail)
								r.DStar, r.DStarWinnerLabel = ds, dl
								r.BoundAvailable = bound
								if poor {
									r.MinSpendMS = 10_000
								}
								rows = append(rows, r)
								n++
							}
						}
					}
				}
			}
		}
	}
	m := Compute("a", rows)
	if err := m.Regret.Closes(); err != nil {
		t.Fatalf("over %d rows: %v", n, err)
	}
	// Every row landed in exactly one bucket.
	total := 0
	for _, v := range m.Buckets {
		total += v
	}
	if total != n {
		t.Errorf("buckets total %d over %d rows: the classification is not exhaustive-and-exclusive", total, n)
	}
}

func TestTotalityOnAdversarialInputs(t *testing.T) {
	// Empty set, all-unknown labels, a single candidate: every one of these
	// must produce absences rather than a panic or a zero.
	m := Compute("a", nil)
	if m.TCAR.Present || m.FAR.Present || m.Coverage.Present {
		t.Errorf("the empty instance set produced present metrics: %+v", m)
	}
	if err := m.Regret.Closes(); err != nil {
		t.Errorf("empty: %v", err)
	}
	allUnknown := []Row{
		row("a", "i1", FamilyMixed, race.TypeSelect, VerdictUnknown, false),
		row("a", "i2", FamilyMixed, race.TypeSelect, VerdictUnknown, false),
	}
	m = Compute("a", allUnknown)
	if v, ok := m.TCAR.Value(); !ok || v != 0 {
		t.Errorf("all-unknown TCAR = %s, want a present 0 (the instances were attempted)", m.TCAR)
	}
	if m.FAR.Point() {
		t.Errorf("all-unknown FAR is a point: %s", m.FAR)
	}
	if err := m.Regret.Closes(); err != nil {
		t.Errorf("all-unknown: %v", err)
	}
	single := []Row{row("a", "i1", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true)}
	if m := Compute("a", single); m.TCAR.Num != 1 || m.TCAR.Den != 1 {
		t.Errorf("single candidate TCAR = %s", m.TCAR)
	}
	// A row for another arm must not leak in.
	mixed := []Row{
		row("a", "i1", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true),
		row("b", "i1", FamilyGoldPresent, race.TypeSelect, VerdictIncorrect, true),
	}
	if m := Compute("a", mixed); m.Instances != 1 {
		t.Errorf("Compute(\"a\") consumed %d rows, want 1", m.Instances)
	}
}

func TestModalAndSelfDisagreement(t *testing.T) {
	cases := []struct {
		in     []string
		modal  string
		n      int
		stable bool
	}{
		{[]string{"SELECT", "SELECT", "SELECT"}, "SELECT", 3, true},
		{[]string{"SELECT", "SELECT", "REJECT"}, "SELECT", 2, true}, // 2 >= ceil(2)
		{[]string{"SELECT", "REJECT", "ESCALATE"}, "ESCALATE", 1, false},
		{[]string{"SELECT", "SELECT", "REJECT", "REJECT", "REJECT"}, "REJECT", 3, false}, // 3 < ceil(10/3)=4
		{nil, "", 0, false},
	}
	for _, c := range cases {
		got, n, stable := Modal(c.in)
		if got != c.modal || n != c.n || stable != c.stable {
			t.Errorf("Modal(%v) = (%q, %d, %v), want (%q, %d, %v)",
				c.in, got, n, stable, c.modal, c.n, c.stable)
		}
	}
	if d := SelfDisagreement([]string{"SELECT", "SELECT", "REJECT"}); d.Num != 1 || d.Den != 3 {
		t.Errorf("self-disagreement = %s, want 1/3", d)
	}
	if d := SelfDisagreement(nil); d.Present {
		t.Errorf("self-disagreement over no replicates is present: %s", d)
	}
}

func TestInferenceRefusesBelowTheInstanceFloor(t *testing.T) {
	var rows []Row
	for i := 0; i < 4; i++ {
		rows = append(rows,
			row("a", fmt.Sprintf("i%d", i), FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true),
			row("b", fmt.Sprintf("i%d", i), FamilyGoldPresent, race.TypeReject, "", true))
	}
	p := Pair("a", "b", rows)
	if p.Instances != 4 || p.AOnly != 4 {
		t.Fatalf("paired table = %+v", p)
	}
	inf := TestPaired(p, InstanceFloorDefault, 1)
	if inf.Available {
		t.Errorf("an inferential statistic was produced at n=4: %+v", inf)
	}
	if inf.Refused == "" {
		t.Errorf("no refusal sentence")
	}
	// Above the floor it is produced, and McNemar's exact p on a perfectly
	// discordant table of 30 is 2^-30 x 2.
	rows = nil
	for i := 0; i < 30; i++ {
		rows = append(rows,
			row("a", fmt.Sprintf("j%d", i), FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true),
			row("b", fmt.Sprintf("j%d", i), FamilyGoldPresent, race.TypeReject, "", true))
	}
	p = Pair("a", "b", rows)
	inf = TestPaired(p, InstanceFloorDefault, 1)
	if !inf.Available {
		t.Fatalf("no statistic at n=30: %+v", inf)
	}
	want := 2 * math.Pow(2, -30)
	if math.Abs(inf.PValue-want) > 1e-15 {
		t.Errorf("McNemar exact p = %g, want %g", inf.PValue, want)
	}
	if inf.CILow > inf.CIHigh {
		t.Errorf("CI is inverted: [%v, %v]", inf.CILow, inf.CIHigh)
	}
	if inf.Method == "" {
		t.Errorf("the interval does not name its method")
	}
}

func TestMcNemarExactEdges(t *testing.T) {
	if p := mcNemarExact(0, 0); p != 1 {
		t.Errorf("no discordant pairs gave p=%v, want 1", p)
	}
	if p := mcNemarExact(5, 5); p != 1 {
		t.Errorf("a symmetric table gave p=%v, want 1 (capped)", p)
	}
	if p := mcNemarExact(3, 0); math.Abs(p-0.25) > 1e-15 {
		t.Errorf("b=3,c=0 gave p=%v, want 0.25", p)
	}
}

func TestPairExcludesUnstableAndUnwinnable(t *testing.T) {
	unstableA := row("a", "i1", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true)
	unstableA.Stable = false
	rows := []Row{
		unstableA,
		row("b", "i1", FamilyGoldPresent, race.TypeReject, "", true),
	}
	poorA := row("a", "i2", FamilyGoldPresent, race.TypeReject, "", true)
	poorA.MinSpendMS = 9999
	poorB := row("b", "i2", FamilyGoldPresent, race.TypeReject, "", true)
	poorB.MinSpendMS = 9999
	rows = append(rows, poorA, poorB)
	rows = append(rows,
		row("a", "i3", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true),
		row("b", "i3", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true))
	p := Pair("a", "b", rows)
	if p.Instances != 1 || p.BothHit != 1 {
		t.Errorf("paired table = %+v, want one both-hit row", p)
	}
	if p.Excluded != 2 {
		t.Errorf("excluded = %d, want 2", p.Excluded)
	}
	if p.ExcludedWhy[string(SkipUnstable)] != 1 {
		t.Errorf("unstable exclusion not counted: %v", p.ExcludedWhy)
	}
	if p.ExcludedWhy["unwinnable-by-any-allocator"] != 1 {
		t.Errorf("unwinnable exclusion not counted: %v", p.ExcludedWhy)
	}
}

func TestCaptionsCarryTheLabelSet(t *testing.T) {
	rows := []Row{
		row("a", "i1", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true),
	}
	rows[0].WinnerSource = SourceAdversarial
	caps := Captions(Compute("a", rows))
	need := []string{LabelOracleBudgetMatched, LabelSyntheticCandidates, LabelSelectorArms, LabelAdversarialDeclared}
	for _, w := range need {
		found := false
		for _, c := range caps {
			if c == w {
				found = true
			}
		}
		if !found {
			t.Errorf("caption %q missing from %v", w, caps)
		}
	}
	// Without an S3 candidate the adversarial label must NOT appear: a
	// caption that drifted from the data is worse than no caption.
	rows[0].WinnerSource = SourceGold
	for _, c := range Captions(Compute("a", rows)) {
		if c == LabelAdversarialDeclared {
			t.Errorf("the adversarial label appeared with no S3 candidate: %v", c)
		}
	}
}

func TestExpectationViolatedIsReportedNotAsserted(t *testing.T) {
	// Decision 7: the generator PROPOSES, the oracle LABELS. A derived-wrong
	// patch the oracle calls correct is COUNTED, and nothing here asserts
	// that mutants are wrong — a test that did would be the assumed-label bug
	// in test form.
	r := row("a", "i1", FamilyGoldPresent, race.TypeSelect, VerdictCorrect, true)
	r.Expected = ExpectIncorrect
	m := Compute("a", []Row{r})
	if m.ExpectationViolated != 1 {
		t.Errorf("expectation-violated = %d, want 1", m.ExpectationViolated)
	}
	// And it changes no metric.
	if m.TCAR.Num != 1 {
		t.Errorf("the expectation census moved TCAR: %s", m.TCAR)
	}
	// An `unknown` expectation can never be violated.
	r.Expected = ExpectUnknown
	if m := Compute("a", []Row{r}); m.ExpectationViolated != 0 {
		t.Errorf("an unknown expectation was counted as violated")
	}
}
