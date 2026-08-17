package eval

// THE DERIVED ARMS: every one declares its evidence footprint, is charged for
// exactly the receipts it reads, and prints no number when it has nothing to
// read. An arm that read the whole ledger for free would dominate everything
// for free.

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/race"
)

func worldRec(dig, tree string) object.RecordedWorld {
	return object.RecordedWorld{Digest: dig, World: object.World{
		Tree: tree, Outcome: object.OutcomeCompleted}}
}

func receiptFor(world, kind, metric string, value, wallMS int64) object.RecordedReceipt {
	r := object.Receipt{
		World:  world,
		Oracle: object.OracleRef{ID: kind, Version: "v0"},
		Result: object.NewResult("pass"),
		Cost:   object.Cost{WallMS: wallMS, Units: 1, Unit: "test"},
	}
	r.Result.Metrics[metric] = value
	return object.RecordedReceipt{Digest: "mv0:r-" + world + "-" + kind, Receipt: r}
}

func twoWorldView() LedgerView {
	return LedgerView{
		Worlds: []object.RecordedWorld{worldRec("mv0:wa", "git:a"), worldRec("mv0:wb", "git:b")},
		Receipts: []object.RecordedReceipt{
			receiptFor("mv0:wa", policy.KindPytestSuite, policy.MetricTestsPassed, 7, 300),
			receiptFor("mv0:wb", policy.KindPytestSuite, policy.MetricTestsPassed, 8, 320),
		},
	}
}

func TestEveryArmDeclaresItsFootprintAndItsMapping(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Arms() {
		if seen[a.ID] {
			t.Errorf("duplicate arm id %s", a.ID)
		}
		seen[a.ID] = true
		if !a.FootprintDeclared {
			t.Errorf("arm %s declares no footprint: it would print no number", a.ID)
		}
		if a.PRDArm == "" {
			t.Errorf("arm %s has no PRD §11 mapping: the mapping is printed with every table", a.ID)
		}
		switch a.Kind {
		case KindRaced:
			if len(a.RaceFlags) == 0 {
				t.Errorf("raced arm %s declares no treatment", a.ID)
			}
		case KindDerived, KindScorerOnly:
			if len(a.RaceFlags) != 0 {
				t.Errorf("derived arm %s declares race flags", a.ID)
			}
		default:
			t.Errorf("arm %s has kind %q, which is not in the vocabulary", a.ID, a.Kind)
		}
		// Every surrogate must say what it is NOT. The two Multiverso arms
		// under test are the exception: they ARE the thing.
		if a.NotArm == "" && a.ID != ArmAdaptive && a.ID != ArmFixedBudget {
			t.Errorf("arm %s does not say what it is not", a.ID)
		}
	}
	mapping := strings.Join(ArmMapping(), "\n")
	if !strings.Contains(mapping, LabelSelectorArms) {
		t.Errorf("the mapping does not carry the selector-arms label")
	}
	// PRD §11 arms 1 and 4 must be named as ABSENT, not quietly missing.
	if !strings.Contains(mapping, "arms 1 (serial self-repair) and 4 (LLM judge) are ABSENT") {
		t.Errorf("the mapping does not name the absent arms:\n%s", mapping)
	}
	// The mapping is printed on the no-metric path too, so it must not name
	// the two headline metrics (acceptance step m2d-7a).
	for _, forbidden := range []string{"TCAR", "FAR"} {
		if strings.Contains(mapping, forbidden) {
			t.Errorf("the arm mapping names %q, which breaks the no-metric assertion", forbidden)
		}
	}
}

func TestUndeclaredFootprintPrintsNoNumber(t *testing.T) {
	a := Arm{ID: "sneaky", Kind: KindDerived, FootprintDeclared: false}
	out := RunDerived(a, DerivedInput{View: twoWorldView(), Instance: "i1"})
	if out.Available {
		t.Fatalf("an arm with no declared footprint printed a number: %+v", out)
	}
	if !strings.Contains(out.Absent, "footprint-undeclared") {
		t.Errorf("the absence does not name the reason: %q", out.Absent)
	}
}

func TestRandomSelectionReadsNoReceiptAndIsSeeded(t *testing.T) {
	a, _ := ArmByID(ArmRandomSelection)
	in := DerivedInput{View: twoWorldView(), Salt: "s", Instance: "i1"}
	out := RunDerived(a, in)
	if !out.Available || out.Decision != race.TypeSelect {
		t.Fatalf("A4 did not select: %+v", out)
	}
	if out.OracleCostMS != 0 {
		t.Errorf("A4 was charged %d ms: it reads no receipt", out.OracleCostMS)
	}
	if len(out.Footprint) != 0 {
		t.Errorf("A4's footprint is not empty: %v", out.Footprint)
	}
	// Seeded and reproducible: the same salt and instance pick the same world.
	if again := RunDerived(a, in); again.Subject != out.Subject {
		t.Errorf("A4 is not reproducible: %s vs %s", out.Subject, again.Subject)
	}
	// And the choice is RECORDED, so an unrecorded random is impossible.
	if !strings.Contains(out.Detail, "HMAC") {
		t.Errorf("A4 did not record how it chose: %q", out.Detail)
	}
	// A different instance may choose differently; over a few instances at
	// least one difference must appear, or the "random" arm is a constant.
	differs := false
	for _, id := range []string{"i2", "i3", "i4", "i5", "i6"} {
		in2 := in
		in2.Instance = id
		if RunDerived(a, in2).Subject != out.Subject {
			differs = true
		}
	}
	if !differs {
		t.Errorf("A4 picked the same world for six instances: it is not selecting")
	}
}

func TestRepoSuiteMaxIsChargedForTheReceiptsItReads(t *testing.T) {
	a, _ := ArmByID(ArmRepoSuiteMax)
	out := RunDerived(a, DerivedInput{View: twoWorldView(), Instance: "i1"})
	if !out.Available {
		t.Fatalf("A5 is absent: %s", out.Absent)
	}
	// The world with the higher tests_passed wins.
	if out.Subject != "mv0:wb" {
		t.Errorf("A5 selected %s, want mv0:wb (8 passed vs 7)", out.Subject)
	}
	// And it is charged the recorded wall_ms of EXACTLY those receipts.
	if out.OracleCostMS != 620 {
		t.Errorf("A5 was charged %d ms, want 620 (300 + 320)", out.OracleCostMS)
	}
	// An absent metric is ABSENT: a receipt that did not measure the metric
	// cannot vote, and substituting zero would let an errored rung outrank a
	// passing one.
	v := twoWorldView()
	v.Receipts[1].Receipt.Result.Metrics = map[string]int64{}
	out = RunDerived(a, DerivedInput{View: v, Instance: "i1"})
	if out.Subject != "mv0:wa" {
		t.Errorf("A5 selected %s after wb's metric went missing, want mv0:wa", out.Subject)
	}
	if out.OracleCostMS != 300 {
		t.Errorf("A5 was charged %d ms, want 300 (only the receipt it could read)", out.OracleCostMS)
	}
}

func TestAnArmWithNothingToReadIsAbsentNotZero(t *testing.T) {
	// Absent source implies absent metric. A5 on a ledger with no suite
	// receipt has no number — it does not silently ESCALATE, and it does not
	// select arbitrarily.
	v := LedgerView{Worlds: []object.RecordedWorld{worldRec("mv0:wa", "git:a")}}
	for _, id := range []string{ArmRepoSuiteMax, ArmDifferentialMajority} {
		a, _ := ArmByID(id)
		out := RunDerived(a, DerivedInput{View: v, Instance: "i1"})
		if out.Available {
			t.Errorf("%s produced a decision with no receipt to read: %+v", id, out)
		}
		if out.Decision != "" {
			t.Errorf("%s invented a decision: %q", id, out.Decision)
		}
		if !strings.Contains(out.Absent, "no number") {
			t.Errorf("%s's absence does not explain itself: %q", id, out.Absent)
		}
	}
	// And an empty ledger is absent for every derived arm.
	for _, a := range Arms() {
		if a.Kind == KindRaced {
			continue
		}
		out := RunDerived(a, DerivedInput{View: LedgerView{}, Instance: "i1"})
		if out.Available {
			t.Errorf("%s produced a number over an empty ledger: %+v", a.ID, out)
		}
	}
}

func TestLabelRetrospectiveRefusesWithoutLabelsAndIsTheDenominator(t *testing.T) {
	a, _ := ArmByID(ArmLabelRetrospective)
	v := twoWorldView()
	// Without labels it refuses: A9 exists only inside the scorer, and a
	// label-reading arm without labels must produce nothing rather than a guess.
	out := RunDerived(a, DerivedInput{View: v, Instance: "i1"})
	if out.Available {
		t.Fatalf("A9 produced a decision with no labels: %+v", out)
	}
	if !strings.Contains(out.Absent, "only inside the scorer") {
		t.Errorf("A9's refusal does not name decision 2: %q", out.Absent)
	}
	// With a correct label it admits, and its cost is in ITS OWN CURRENCY.
	out = RunDerived(a, DerivedInput{View: v, Instance: "i1", ScoringMS: 900,
		Labels: map[string]string{"mv0:wa": VerdictIncorrect, "mv0:wb": VerdictCorrect}})
	if !out.Available || out.Decision != race.TypeSelect || out.Subject != "mv0:wb" {
		t.Errorf("A9 did not admit the correct candidate: %+v", out)
	}
	if out.ScoringMS != 900 {
		t.Errorf("A9's scoring cost = %d, want 900", out.ScoringMS)
	}
	if out.OracleCostMS != 0 {
		t.Errorf("A9's scoring cost leaked into the oracle pool: %d", out.OracleCostMS)
	}
	// With nothing correct it rejects — which is what makes it the regret
	// denominator on family B rather than a free win.
	out = RunDerived(a, DerivedInput{View: v, Instance: "i1",
		Labels: map[string]string{"mv0:wa": VerdictIncorrect, "mv0:wb": VerdictUnknown}})
	if out.Decision != race.TypeReject {
		t.Errorf("A9 did not reject with no correct candidate: %+v", out)
	}
	if !strings.Contains(out.Detail, "unknown or unlabelled") {
		t.Errorf("A9 did not report how many were unscored: %q", out.Detail)
	}
}

func TestRacedArmsAreNotDerivable(t *testing.T) {
	for _, id := range []string{ArmAdaptive, ArmFixedBudget, ArmReference} {
		a, _ := ArmByID(id)
		out := RunDerived(a, DerivedInput{View: twoWorldView(), Instance: "i1"})
		if out.Available {
			t.Errorf("%s was derived from a ledger instead of raced: %+v", id, out)
		}
		if !strings.Contains(out.Absent, "raced-arm") {
			t.Errorf("%s's absence does not say it must be raced: %q", id, out.Absent)
		}
	}
}

func TestAllocationBoundStaysABoundOnAllocation(t *testing.T) {
	a, _ := ArmByID(ArmAllocationBound)
	// With no policy the bound has nothing to enumerate, and it must report
	// that rather than approximate.
	out := RunDerived(a, DerivedInput{View: twoWorldView(), Instance: "i1"})
	if out.Available && out.Decision == "" {
		t.Errorf("A8 reported available with no decision: %+v", out)
	}
	if !out.Available && out.Absent == "" {
		t.Errorf("A8 is absent with no reason")
	}
	// Its declaration says what it is not: a bound on allocation, never on
	// decision quality.
	if !strings.Contains(a.NotArm, "not on decision quality") {
		t.Errorf("A8 does not disclaim decision quality: %q", a.NotArm)
	}
}

func TestArmByIDIsClosed(t *testing.T) {
	if _, ok := ArmByID("A7-invented"); ok {
		t.Errorf("ArmByID resolved an arm that does not exist")
	}
	for _, a := range Arms() {
		if got, ok := ArmByID(a.ID); !ok || got.ID != a.ID {
			t.Errorf("ArmByID(%s) failed", a.ID)
		}
	}
}
