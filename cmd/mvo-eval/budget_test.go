package main

// BLOCKER B1 — THE TWO RULES MUST BE COMPARED AT THE SAME BUDGET, asserted at
// the level the harness delivers it.
//
// `--selector` was a per-RUN choice, and B is derived inside a run from that
// run's own reference races, so comparing `voc` against `voc2` meant two runs,
// two reference draws and two budgets — measured on one host, same instance,
// same day: minspend 1553 against 1013 — printed under a cell captioned
// ORACLE-BUDGET-MATCHED. Two things close it and both are tested here: the plan
// races every rule inside ONE run, and `finish` refuses a report whose arms did
// not share a budget.

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/eval"
	"github.com/coagente/multiverso/internal/schedule"
)

// THE FIX. Naming both rules races both inside one run, so they share the
// warmed template, the reference races and the derived B.
func TestArmPlanRacesEveryRuleInsideOneRun(t *testing.T) {
	plan := armPlan([]string{eval.ArmAdaptive, eval.ArmFixedBudget}, []string{"voc", "voc2"})
	if len(plan) != 3 {
		t.Fatalf("two rules over two arms planned %d treatment(s), want 3: %v", len(plan), armKeys(plan))
	}
	ada := adaptiveKeys(armKeys(plan))
	if len(ada) != 2 {
		t.Fatalf("the adaptive arm is not one treatment per rule: %v", armKeys(plan))
	}
	seen := map[string]string{}
	for _, p := range plan {
		if _, dup := seen[p.key]; dup {
			t.Fatalf("two treatments share the arm id %q: their rows would pool into one cell", p.key)
		}
		seen[p.key] = p.selector
	}
	if seen[eval.ArmAdaptive+"@voc"] != "voc" || seen[eval.ArmAdaptive+"@voc2"] != "voc2" {
		t.Errorf("a treatment does not carry its own rule: %v", seen)
	}
	// The rule is a flag on the adaptive arm and on NO OTHER (M2b.2 decision
	// 6), and the ladder is raced ONCE and shared: racing it per rule would
	// buy nothing and would put two identical rows in one cell.
	if seen[eval.ArmFixedBudget] != "" {
		t.Errorf("the ladder was handed an allocation rule: %q", seen[eval.ArmFixedBudget])
	}
	// And every treatment must actually be raceable: an arm with no race flags
	// is one the runner refuses.
	for _, p := range plan {
		if len(p.arm.RaceFlags) == 0 {
			t.Errorf("treatment %q declares no race flags", p.key)
		}
	}
}

// The one-rule and no-rule plans are EXACTLY what they were, so every published
// M2d number keeps its arm names and its cell keys.
func TestOneRuleKeepsTheArmIdsM2dPublished(t *testing.T) {
	for _, sel := range [][]string{nil, {"voc2"}} {
		plan := armPlan([]string{eval.ArmAdaptive, eval.ArmFixedBudget}, sel)
		got := armKeys(plan)
		want := []string{eval.ArmAdaptive, eval.ArmFixedBudget}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("--selector=%v moved the arm ids: %v", sel, got)
		}
	}
	// The rule is still recorded on the row even when the arm id does not move
	// — a cell that cannot say which rule produced it is a cell whose caption
	// is a guess.
	if plan := armPlan([]string{eval.ArmAdaptive}, []string{"voc2"}); plan[0].selector != "voc2" {
		t.Errorf("a single rule was not carried onto its treatment: %q", plan[0].selector)
	}
}

// twoRuleManifest is one cell raced under both rules, with both rules
// exercised, so the ONLY thing under test below is the budget.
func twoRuleManifest(budgetVOC, budgetVOC2 int64) (eval.RunManifest, []eval.Row) {
	warm := schedule.Coverage(schedule.Trace{
		HasStarted: true,
		Started: schedule.Started{
			Selector:  schedule.SelectorNameVOC2,
			CostTable: []schedule.CostRow{{Kind: "pytest-suite", Basis: schedule.CostBasisFit, N: 6}},
		},
		Steps: []schedule.Step{{
			Step: 1, CommitBasis: schedule.CommitBasisReserved, Scarce: true,
			CommitSet: []string{"mv0:a"},
			Considered: []schedule.Considered{{
				World: "mv0:a", Oracle: "guard", ScoreBasis: schedule.ScoreBasisFinish,
				Admissible: true, Affordable: true, HardGate: true, AllowanceMS: 700,
			}},
		}},
		HasFinished: true,
		Finished:    schedule.Finished{Stop: schedule.StopBudget},
	})
	keyVOC, keyVOC2 := eval.ArmAdaptive+"@voc", eval.ArmAdaptive+"@voc2"
	man := eval.RunManifest{
		Schema: eval.SchemaRun, Corpus: eval.CorpusLocalDerived, Version: eval.LocalVersion,
		Arms:          []string{keyVOC, keyVOC2},
		CanaryVerdict: eval.CanaryClean,
		CoverageByArm: map[string]schedule.CoverageReport{keyVOC: warm, keyVOC2: warm},
		BudgetByInstance: map[string]int64{
			"toyrepo-mean-A": budgetVOC,
		},
	}
	base := eval.Row{
		Instance: "toyrepo-mean-A", Family: eval.FamilyGoldPresent, Tier: 1,
		Policy: "default", CostRegime: "warm", Cluster: "toyrepo",
		Decision: "SELECT", Stable: true, Replicates: 3, ModalCount: 3,
		WinnerLabel: eval.VerdictCorrect, Avail: true,
		DStar: "SELECT", DStarWinnerLabel: eval.VerdictCorrect,
		MinSpendMS: 1412, BoundAvailable: true,
	}
	a, b := base, base
	a.Arm, a.Selector, a.BudgetMS = keyVOC, "voc", budgetVOC
	b.Arm, b.Selector, b.BudgetMS = keyVOC2, "voc2", budgetVOC2
	return man, []eval.Row{a, b}
}

// THE REGRESSION TEST FOR B1, at the level a reader would meet it. If the two
// arms of a cell are ever handed different budgets again, the run refuses and
// prints no metric line.
func TestARunWhoseArmsHeldDifferentBudgetsIsRefusedAndPrintsNoMetricLine(t *testing.T) {
	man, rows := twoRuleManifest(1553, 1013)
	o := runOpts{common: &commonFlags{corpus: eval.CorpusLocalDerived, version: eval.LocalVersion}}
	var err error
	out := captureStdout(t, func() { err = o.finish(man, rows, t.TempDir()) })

	var ce codedError
	if !asCoded(err, &ce) || ce.code != exitFailure {
		t.Fatalf("1553 ms against 1013 ms returned %v, want exit %d", err, exitFailure)
	}
	if !strings.Contains(out, "ARMS NOT BUDGET-MATCHED") {
		t.Errorf("the refusal printed no banner:\n%s", out)
	}
	for _, want := range []string{"1553", "1013", "voc", "voc2"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, out)
		}
	}
	// NO METRIC LINE AT ALL. The blocker's own words are that a mismatched
	// budget INVALIDATES EVERY NUMBER, so printing one beside the refusal
	// would be the failure this closes.
	for _, forbidden := range []string{"TCAR", "FRR_reach", "paired "} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a budget-mismatched cell printed %q:\n%s", forbidden, out)
		}
	}
}

// THE ANTI-RUBBER-STAMP HALF. A check that refuses on every run is the rubber
// stamp M2b.2 F-9 already named, so a cell whose arms shared one B must be
// reported — and it must PRINT the B it shared.
func TestARunWhoseArmsSharedOneBudgetIsReportedAndPrintsThatBudget(t *testing.T) {
	man, rows := twoRuleManifest(1553, 1553)
	o := runOpts{common: &commonFlags{corpus: eval.CorpusLocalDerived, version: eval.LocalVersion}}
	var err error
	out := captureStdout(t, func() { err = o.finish(man, rows, t.TempDir()) })
	if err != nil {
		t.Fatalf("a budget-matched cell was refused: %v", err)
	}
	if strings.Contains(out, "ARMS NOT BUDGET-MATCHED") {
		t.Errorf("the refusal fired on a budget-matched cell:\n%s", out)
	}
	if !strings.Contains(out, "TCAR") {
		t.Errorf("a budget-matched cell printed no metric line:\n%s", out)
	}
	// PRINTED ALWAYS: B is in the cell's own caption, so a reader comparing
	// two cells sees the budget each was raced at without reading a manifest.
	if !strings.Contains(out, eval.LabelOracleBudgetMatched+"(B=1553ms)") {
		t.Errorf("the cell caption does not name the budget it matched:\n%s", out)
	}
	if !strings.Contains(out, "RULE-VOC2") || !strings.Contains(out, "RULE-VOC ") {
		t.Errorf("the cells do not name the rule that produced them:\n%s", out)
	}
	// And the shared draw is stated above the metrics, per instance.
	if !strings.Contains(out, "derived ONCE from this instance's reference draw") {
		t.Errorf("the report does not say the budget was derived once:\n%s", out)
	}
}

// The vacuity refusal still points at the RULE UNDER TEST after B1 split the
// adaptive arm into one treatment per rule: a comparison in which EITHER rule
// never fired is a comparison of one rule against itself.
func TestVacuityStillBindsWhenOnlyOneOfTwoRulesNeverFired(t *testing.T) {
	man, rows := twoRuleManifest(1553, 1553)
	cold := schedule.Coverage(schedule.Trace{
		HasStarted: true,
		Started: schedule.Started{
			Selector:  schedule.SelectorNameVOC2,
			CostTable: []schedule.CostRow{{Kind: "pytest-suite", Basis: schedule.CostBasisDeclaredRank}},
		},
		Steps: []schedule.Step{{
			Step:        1,
			CommitBasis: schedule.CommitBasisUnpriced + "(pytest-collect,pytest-suite,tree-guard)",
			Considered: []schedule.Considered{{
				World: "mv0:a", Oracle: "guard", ScoreBasis: schedule.ScoreBasisRung,
				Admissible: true, Affordable: true, HardGate: true,
			}},
		}},
		HasFinished: true,
		Finished:    schedule.Finished{Stop: schedule.StopEmpty},
	})
	man.CoverageByArm[eval.ArmAdaptive+"@voc2"] = cold
	o := runOpts{common: &commonFlags{corpus: eval.CorpusLocalDerived, version: eval.LocalVersion}}
	var err error
	out := captureStdout(t, func() { err = o.finish(man, rows, t.TempDir()) })
	var ce codedError
	if !asCoded(err, &ce) || ce.code != exitVacuous {
		t.Fatalf("a cell with one inert rule returned %v, want exit %d", err, exitVacuous)
	}
	if strings.Contains(out, "TCAR") {
		t.Errorf("a vacuous cell printed a metric line:\n%s", out)
	}
}
