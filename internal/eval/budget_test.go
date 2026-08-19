package eval

// BLOCKER B1 — THE TWO RULES MUST BE COMPARED AT THE SAME BUDGET.
//
// The measured failure, verbatim from M2d.1's BUILDLOG entry: `--selector` was
// per RUN, and B is derived inside a run from that run's own reference races,
// so the `voc` run and the `voc2` run of the same cell were handed DIFFERENT
// budgets — same host, same instance, same day, minspend 1553 against 1013 —
// under a cell captioned ORACLE-BUDGET-MATCHED. The numbers below are those
// numbers, so this file fails on exactly the state that shipped.

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/race"
)

// twoRuleRows is one cell of one instance raced under both allocation rules,
// which is what a single run now produces.
func twoRuleRows(budgetVOC, budgetVOC2 int64) []Row {
	base := Row{
		Instance: "advrepo-split-B", Family: FamilyGoldPresent, Tier: 1,
		Policy: "default", CostRegime: "warm", Cluster: "advrepo",
		Decision: race.TypeSelect, Stable: true, Replicates: 3, ModalCount: 3,
		WinnerLabel: VerdictCorrect, Avail: true,
		DStar: race.TypeSelect, DStarWinnerLabel: VerdictCorrect,
		MinSpendMS: 1412, BoundAvailable: true,
	}
	a, b := base, base
	a.Arm, a.Selector, a.BudgetMS = ArmAdaptive+"@voc", "voc", budgetVOC
	b.Arm, b.Selector, b.BudgetMS = ArmAdaptive+"@voc2", "voc2", budgetVOC2
	return []Row{a, b}
}

// THE TEST THE BLOCKER ASKED FOR. If the two arms of a cell are ever handed
// different budgets again, this fails.
func TestTwoArmsOfACellMayNotReceiveDifferentBudgets(t *testing.T) {
	bad := BudgetMismatches(twoRuleRows(1553, 1013))
	if len(bad) != 1 {
		t.Fatalf("1553 ms against 1013 ms on one instance produced %d refusal(s), want 1: %v", len(bad), bad)
	}
	// The refusal must be READABLE: which instance, which budgets, which arms.
	for _, want := range []string{"advrepo-split-B", "1553", "1013", "--selector=voc", "--selector=voc2"} {
		if !strings.Contains(bad[0], want) {
			t.Errorf("the refusal does not name %q:\n%s", want, bad[0])
		}
	}
	// And it must be SILENT on the fixed state — a check that refuses on every
	// run is M2b.2 F-9's rubber stamp, which is how a gate stops being read.
	if got := BudgetMismatches(twoRuleRows(1553, 1553)); len(got) != 0 {
		t.Errorf("a budget-matched cell was refused: %v", got)
	}
}

// The mismatch is per INSTANCE, because B is derived per instance from that
// instance's own reference draw. Two instances at two budgets is the normal,
// correct state and must not be refused.
func TestDifferentInstancesMayCarryDifferentBudgets(t *testing.T) {
	rows := twoRuleRows(1553, 1553)
	other := twoRuleRows(880, 880)
	for i := range other {
		other[i].Instance = "toyrepo-mean-A"
	}
	if got := BudgetMismatches(append(rows, other...)); len(got) != 0 {
		t.Errorf("two instances with their own derived budgets were refused: %v", got)
	}
	// But a mismatch inside EITHER instance is still caught, and the message
	// names the instance it belongs to rather than the run.
	other[1].BudgetMS = 640
	got := BudgetMismatches(append(rows, other...))
	if len(got) != 1 || !strings.Contains(got[0], "toyrepo-mean-A") {
		t.Fatalf("the mismatch was not attributed to its instance: %v", got)
	}
}

// The caption carries B, per arm. That is what makes a mismatch visible to a
// READER of two cells rather than only to the harness.
func TestTheCellCaptionNamesTheBudgetItClaimsToHaveMatched(t *testing.T) {
	rows := twoRuleRows(1553, 1013)
	capVOC := Captions(Compute(ArmAdaptive+"@voc", rows))
	capVOC2 := Captions(Compute(ArmAdaptive+"@voc2", rows))
	find := func(caps []string) string {
		for _, c := range caps {
			if strings.HasPrefix(c, LabelOracleBudgetMatched) {
				return c
			}
		}
		return ""
	}
	a, b := find(capVOC), find(capVOC2)
	if a != LabelOracleBudgetMatched+"(B=1553ms)" {
		t.Errorf("the voc cell's budget caption is %q", a)
	}
	if b != LabelOracleBudgetMatched+"(B=1013ms)" {
		t.Errorf("the voc2 cell's budget caption is %q", b)
	}
	if a == b {
		t.Errorf("two cells at different budgets printed the same caption %q — this is the blocker", a)
	}
	// The RULE is in the caption too: a cell that cannot say which allocation
	// rule produced it is a cell whose caption is a guess (M2b.2 §5.2).
	if !hasCaption(capVOC, "RULE-VOC") || !hasCaption(capVOC2, "RULE-VOC2") {
		t.Errorf("the cells do not name their rule: %v / %v", capVOC, capVOC2)
	}
}

// An UNBOUNDED row says so rather than printing B=0ms, which reads as the
// tightest budget when it is the one row that was handed infinite money.
func TestAnUnboundedCellSaysUnboundedRatherThanZero(t *testing.T) {
	rows := twoRuleRows(0, 0)
	c := ""
	for _, x := range Captions(Compute(ArmAdaptive+"@voc", rows)) {
		if strings.HasPrefix(x, LabelOracleBudgetMatched) {
			c = x
		}
	}
	if !strings.Contains(c, "UNBOUNDED") {
		t.Errorf("a budget of 0 was captioned %q", c)
	}
}

func hasCaption(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
