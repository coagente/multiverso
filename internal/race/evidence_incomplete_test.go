package race

// M2b rule 1a — `on_evidence_incomplete` (decision 14).
//
// M2a stated that "a scheduler that runs out of budget before anyone finished
// the ladder produces an ESCALATE, which is the honest verdict." That was
// FALSE against the shipped machineryFailure, and the gap is exactly where an
// adaptive scheduler lands: a COMPLETED world that bought `collect` and
// passed it, and never bought `suite`, has no failing receipt and a first
// gate that did produce one. So rule 1 does not fire, PassCount is 0, and the
// race records REJECT — "these candidates are bad" — when the truth is "we
// never bought the evidence."
//
// Every test here is a pure call into Decide. The rule reads no scheduler
// state and must not: "the budget ran out" is not a function of (policy,
// worlds, receipts).

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// incompletePolicy is the two-rung fixture the rule is about: collect gates
// first, suite gates second. `on` toggles the rule so the same evidence can
// be decided both ways and the difference attributed to the rule alone.
func incompletePolicy(t *testing.T, on bool) policy.Policy {
	t.Helper()
	return compileV1(t,
		[]object.GateSpec{
			gate(policy.GateCollectNonempty, "collect", 0),
			gate(policy.GateStatusPass, "suite", 0),
		},
		[]string{policy.KeyGatePass, policy.KeyTestsPassedDesc},
		object.EscalationSpec{
			OnAllWorldsFailedMachinery: true,
			OnEvidenceIncomplete:       on,
		})
}

// THE STARVED RACE. Both worlds passed everything they paid for and neither
// paid for the suite. With the rule OFF this is M2a's REJECT, unchanged; with
// it ON it is an ESCALATE naming the gate nobody bought.
func TestEvidenceIncompleteReplacesRejectOnAnUnboughtGate(t *testing.T) {
	polOff := incompletePolicy(t, false)
	polOn := incompletePolicy(t, true)
	collectCfg := cfgDigest(t, collectSpec())

	a := mkWorld(t, "a", object.OutcomeCompleted)
	b := mkWorld(t, "b", object.OutcomeCompleted)
	recs := []object.RecordedReceipt{
		mkReceipt(t, a, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 10,
			map[string]int64{policy.MetricCollectedTotal: 8}),
		mkReceipt(t, b, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 10,
			map[string]int64{policy.MetricCollectedTotal: 8}),
	}
	worlds := []object.RecordedWorld{a, b}

	off := Decide(polOff, worlds, recs)
	if off.Type != TypeReject {
		t.Fatalf("with the rule OFF the starved race decided %s, want %s — the M2a behaviour must be unchanged",
			off.Type, TypeReject)
	}

	on := Decide(polOn, worlds, recs)
	if on.Type != TypeEscalate {
		t.Fatalf("with the rule ON the starved race decided %s, want %s", on.Type, TypeEscalate)
	}
	tr := Trace(polOn, worlds, recs)
	if tr.Escalation.Rule != RuleOnEvidenceIncomplete {
		t.Fatalf("escalation rule = %q, want %q", tr.Escalation.Rule, RuleOnEvidenceIncomplete)
	}
	// The sentence has to name what was never bought, because that is the
	// next command the operator runs.
	want := []string{
		"2 worlds passed every gate their evidence could evaluate",
		"1 hard gate(s) were never purchased for the leading world",
		"first unpurchased: " + policy.GateStatusPass + "@suite",
		"this race did not buy enough evidence to reject these candidates",
	}
	for _, frag := range want {
		if !strings.Contains(tr.Escalation.Detail, frag) {
			t.Errorf("escalation detail is missing %q:\n%s", frag, tr.Escalation.Detail)
		}
	}
}

// A world that FAILED a gate it did buy is not "incomplete", it is rejected.
// Reporting a real failure as unbought evidence would be the over-claim in
// the other direction: the whole point of the rule is that the two are
// different statements.
func TestEvidenceIncompleteDoesNotFireOnARealFailure(t *testing.T) {
	pol := incompletePolicy(t, true)
	collectCfg, suiteCfg := cfgDigest(t, collectSpec()), cfgDigest(t, suiteSpec())

	a := mkWorld(t, "a", object.OutcomeCompleted)
	recs := []object.RecordedReceipt{
		mkReceipt(t, a, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 10,
			map[string]int64{policy.MetricCollectedTotal: 8}),
		mkReceipt(t, a, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "fail", 20,
			map[string]int64{policy.MetricTestsPassed: 6, policy.MetricTestsTotal: 8}),
	}
	got := Decide(pol, []object.RecordedWorld{a}, recs)
	if got.Type != TypeReject {
		t.Fatalf("a world that bought its suite and failed it decided %s, want %s", got.Type, TypeReject)
	}
}

// One world starved, one world genuinely bad: the rule still fires, because
// at least one world failed only for want of a receipt — and the count in the
// sentence reports how many, which is the number an operator acts on.
func TestEvidenceIncompleteCountsOnlyTheStarvedWorlds(t *testing.T) {
	pol := incompletePolicy(t, true)
	collectCfg, suiteCfg := cfgDigest(t, collectSpec()), cfgDigest(t, suiteSpec())

	a := mkWorld(t, "a", object.OutcomeCompleted) // starved after collect
	b := mkWorld(t, "b", object.OutcomeCompleted) // bought its suite and failed
	recs := []object.RecordedReceipt{
		mkReceipt(t, a, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 10,
			map[string]int64{policy.MetricCollectedTotal: 8}),
		mkReceipt(t, b, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 10,
			map[string]int64{policy.MetricCollectedTotal: 8}),
		mkReceipt(t, b, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "fail", 20,
			map[string]int64{policy.MetricTestsPassed: 6, policy.MetricTestsTotal: 8}),
	}
	tr := Trace(pol, []object.RecordedWorld{a, b}, recs)
	if tr.Escalation.Rule != RuleOnEvidenceIncomplete {
		t.Fatalf("escalation rule = %q, want %q", tr.Escalation.Rule, RuleOnEvidenceIncomplete)
	}
	if !strings.Contains(tr.Escalation.Detail, "1 worlds passed every gate their evidence could evaluate") {
		t.Errorf("the sentence counts the wrong worlds:\n%s", tr.Escalation.Detail)
	}
}

// PRECEDENCE 1a: below on_all_worlds_failed_machinery, because broken
// machinery and unbought evidence are different statements and the first is
// the more fundamental. A world that never COMPLETED is machinery, even
// though its gates are also unbought.
func TestEvidenceIncompleteSitsBelowMachineryFailure(t *testing.T) {
	pol := incompletePolicy(t, true)
	a := mkWorld(t, "a", object.OutcomeConfigError)
	tr := Trace(pol, []object.RecordedWorld{a}, nil)
	if tr.Escalation.Rule != RuleAllWorldsFailedMachinery {
		t.Fatalf("escalation rule = %q, want %q (machinery outranks unbought evidence)",
			tr.Escalation.Rule, RuleAllWorldsFailedMachinery)
	}
}

// THE STARVED ADMISSION, which is the case the rule was written for and the
// case its first guard made unreachable.
//
// One world bought its whole ladder and passed. Its rival bought `collect`,
// passed it, and its suite was never purchased. `PassCount == 1`, so a rule
// guarded on `PassCount == 0` never fires, and the race SELECTs — admitting
// a change while the only candidate that could have beaten it went
// unmeasured. Measured against the shipped default plus the rule: 5 of 5
// races admitted the burner and left trunk buggy. The guard is gone; the
// PREDICATE is unchanged and still pure over (pol, worlds, receipts).
func TestEvidenceIncompleteFiresOnTheStarvedAdmission(t *testing.T) {
	pol := incompletePolicy(t, true)
	collectCfg, suiteCfg := cfgDigest(t, collectSpec()), cfgDigest(t, suiteSpec())

	a := mkWorld(t, "a", object.OutcomeCompleted) // complete and passing
	b := mkWorld(t, "b", object.OutcomeCompleted) // starved after collect
	recs := []object.RecordedReceipt{
		mkReceipt(t, a, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 10,
			map[string]int64{policy.MetricCollectedTotal: 8}),
		mkReceipt(t, a, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 20,
			map[string]int64{policy.MetricTestsPassed: 8, policy.MetricTestsTotal: 8}),
		mkReceipt(t, b, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 10,
			map[string]int64{policy.MetricCollectedTotal: 8}),
	}
	got := Decide(pol, []object.RecordedWorld{a, b}, recs)
	if got.Type != TypeEscalate {
		t.Fatalf("decided %s over a rival whose suite was never purchased, want %s", got.Type, TypeEscalate)
	}
	tr := Trace(pol, []object.RecordedWorld{a, b}, recs)
	if tr.Escalation.Rule != RuleOnEvidenceIncomplete {
		t.Fatalf("escalation rule = %q, want %q", tr.Escalation.Rule, RuleOnEvidenceIncomplete)
	}
	if !strings.Contains(tr.Escalation.Detail, "rank these candidates against each other") {
		t.Errorf("the sentence claims the wrong thing about a race that had a passing world:\n%s",
			tr.Escalation.Detail)
	}
}

// AND IT STAYS SILENT ON THE ORDINARY SELECT, which is the compatibility
// guarantee that matters: a rival that FAILED a gate was measured and lost.
// Its later rungs are unbought because the ladder short-circuited (M1e
// decision 12), which is a fact about the candidate and not about the
// budget — so the race decides exactly as it always did.
func TestEvidenceIncompleteIsSilentWhenTheRivalWasMeasuredAndFailed(t *testing.T) {
	pol := incompletePolicy(t, true)
	collectCfg, suiteCfg := cfgDigest(t, collectSpec()), cfgDigest(t, suiteSpec())

	a := mkWorld(t, "a", object.OutcomeCompleted)
	b := mkWorld(t, "b", object.OutcomeCompleted)
	recs := []object.RecordedReceipt{
		mkReceipt(t, a, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 10,
			map[string]int64{policy.MetricCollectedTotal: 8}),
		mkReceipt(t, a, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 20,
			map[string]int64{policy.MetricTestsPassed: 8, policy.MetricTestsTotal: 8}),
		mkReceipt(t, b, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "fail", 10,
			map[string]int64{policy.MetricCollectedTotal: 0}),
	}
	got := Decide(pol, []object.RecordedWorld{a, b}, recs)
	if got.Type != TypeSelect {
		t.Fatalf("decided %s over a rival that was measured and failed, want %s", got.Type, TypeSelect)
	}
	if strings.Contains(got.Rationale, "evidence incomplete") {
		t.Errorf("the rationale claims unbought evidence about a measured failure:\n%s", got.Rationale)
	}
}

// THE REPLAY GUARANTEE. No pre-M2b policy can declare the field, so no
// pre-M2b policy can reach the branch — and a policy that leaves it off
// decides exactly as it did before, on evidence that would fire the rule.
func TestEvidenceIncompleteIsOffByDefaultEverywhere(t *testing.T) {
	def := policy.Default()
	if def.Escalation.OnEvidenceIncomplete {
		t.Fatal("the shipped default declares on_evidence_incomplete; M2d has not measured the base rate yet")
	}
	// The v0 dialect cannot express it at all: its escalation set is M0's.
	v0 := testPolicy(t)
	if v0.Esc.OnEvidenceIncomplete {
		t.Fatal("a v0-dialect policy compiled the rule ON; no historical evaluation may move")
	}
}
