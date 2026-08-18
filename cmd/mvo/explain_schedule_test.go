package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/schedule"
)

// renderSchedule is the human surface under test. The block RENDERS ROWS AND
// RECOMPUTES NO SCORE, so every one of these tests hands it recorded numbers
// and asserts what appears — which is exactly what an operator would read.
func renderSchedule(s *explainSchedule) string {
	var sb strings.Builder
	writeSchedule(&sb, s)
	return sb.String()
}

// A race with no allocation trace is reported as ABSENT, never as an
// allocation that bought nothing. Absent source implies absent metric, and
// an empty table would read as a scheduler that considered nothing.
func TestScheduleRendersAbsenceHonestly(t *testing.T) {
	got := renderSchedule(&explainSchedule{Recorded: false})
	if !strings.Contains(got, "no allocation trace recorded for this race") {
		t.Fatalf("absent trace does not say so:\n%s", got)
	}
	if !strings.Contains(got, "fixed ladder") {
		t.Fatalf("absent trace does not name the two ways it can happen:\n%s", got)
	}
	if strings.Contains(got, "STEP") {
		t.Fatalf("absent trace rendered a table:\n%s", got)
	}
}

// A trace this binary cannot decode is a DIFFERENT fact from a trace that
// does not exist, and an operator must be able to tell them apart.
func TestScheduleRendersUnreadableTraceDistinctly(t *testing.T) {
	got := renderSchedule(&explainSchedule{Recorded: false, Stop: "unreadable: bad json"})
	if !strings.Contains(got, "present but unreadable") {
		t.Fatalf("an unreadable trace reads as an absent one:\n%s", got)
	}
}

// The DECLINED rung is the row an operator actually reads: it is the only
// place the record says why the budget went somewhere else. A declined
// decision-inert rung must render its reason AND no millisecond figure,
// because it was priced by declared rank and nobody measured it.
func TestScheduleRendersDeclinedInertRung(t *testing.T) {
	s := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleAdaptive, Mode: schedule.ModeDecision,
		BudgetMS: 10000, Batches: 3, SpentMS: 1676, Stop: schedule.StopFrontier,
		Steps: []explainScheduleRow{
			{
				Step: 3, World: "mv0:aa1000000000", Oracle: "suite", Kind: policy.KindPytestSuite,
				Flip: 1, DiscountBP: 5000, ExecutorBP: 5000, ValueBP: 2500, CostMS: 427,
				CostBasis: "fit(pytest-suite,off) n=37", ScoreBPPS: 5854,
				Bought: true, Status: "pass", ActualMS: 430,
			},
			{
				Step: 3, World: "mv0:aa1000000000", Oracle: "mutate", Kind: policy.KindMutationDiff,
				Flip: 0, DiscountBP: 10000, ExecutorBP: 5000, ValueBP: 0, CostMS: 18400,
				CostBasis: schedule.CostBasisDeclaredRank + "(rank 6, n=0)",
				Declined:  "decision-inert: no gate, ranking key or escalation rule reads mutation-diff",
			},
		},
		CostModel: []explainCostRow{
			{Kind: policy.KindPytestSuite, Basis: "fit", N: 37, Seal: policy.AutoloadOff,
				FixedMS: 402, PerUnitUS: 3100, Estimator: schedule.EstimatorTheilSen,
				Unit: policy.UnitTests, Measured: true},
			{Kind: policy.KindMutationDiff, Basis: schedule.CostBasisDeclaredRank,
				Unit: policy.UnitMutants, Measured: false},
		},
	}
	got := renderSchedule(s)
	for _, want := range []string{
		"oracle budget 10000 ms", "stopped: S-frontier",
		"STEP", "suite", "bought  (pass)", "declined",
		"decision-inert: no gate, ranking key or escalation rule reads mutation-diff",
		"402 ms + 3.1 ms/test", schedule.EstimatorTheilSen,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendering is missing %q:\n%s", want, got)
		}
	}
	// M2a's rule, extended from the report to the allocator: a millisecond
	// figure nobody measured must never appear beside one somebody did.
	if strings.Contains(got, "18400 ms") {
		t.Fatalf("a declared-rank row printed a millisecond figure:\n%s", got)
	}
	if !strings.Contains(got, "rank-only") {
		t.Fatalf("a declared-rank row does not say it carries no measurement:\n%s", got)
	}
	if !strings.Contains(got, "no local measurement") {
		t.Fatalf("the cost model does not refuse a number for the unfitted kind:\n%s", got)
	}
}

// The STARVED stop is the interesting one, because "we stopped because
// nothing left could change the decision" and "we stopped because the money
// ran out" are opposite facts about a race.
func TestScheduleRendersStarvedStopAndTerminalDeclines(t *testing.T) {
	s := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleAdaptive, BudgetMS: 500, Batches: 2,
		SpentMS: 480, Stop: schedule.StopBudget,
		Steps: []explainScheduleRow{{
			Step: 2, World: "mv0:bb2000000000", Oracle: "suite", Kind: policy.KindPytestSuite,
			Flip: 1, DiscountBP: 5000, ExecutorBP: 5000, ValueBP: 2500, CostMS: 427,
			CostBasis: "fit(pytest-suite,off) n=4", Affordable: false,
			Declined: "unaffordable: predicted 427 ms exceeds this world's share of 20 ms",
		}},
		Skipped: []schedule.Skipped{{
			World: "mv0:bb2000000000", Oracle: "suite",
			Reason: "unaffordable: predicted 427 ms exceeds this world's share of 20 ms",
		}},
		RankingIncomplete: true,
	}
	got := renderSchedule(s)
	for _, want := range []string{
		"stopped: " + schedule.StopBudget, "never bought (terminal declines)",
		"unaffordable", "ranking:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendering is missing %q:\n%s", want, got)
		}
	}
}

// THE CALIBRATION RESIDUAL COVERS THE PRICED ROWS ON BOTH SIDES, and the
// spend that carried no prediction is reported beside it rather than folded
// into a percentage. Summing predictions over the fitted rows and actuals
// over every receipt printed "predicted 2 ms, actual 2073 ms (+103550.0%)" on
// a real race — a number sold as the calibration residual M2d needs, which
// measured nothing. The overrun of a budget that did not bind gets its own
// marker for the same reason: a budget-matched experiment cannot silently
// compare unequal spends.
func TestScheduleResidualCoversPricedRowsAndFlagsTheBreach(t *testing.T) {
	s := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleAdaptive, BudgetMS: 500, Batches: 2,
		SpentMS: 1500, Stop: schedule.StopBudget,
		Predicted: 427, Actual: 431, PricedBought: 1,
		UnpricedMS: 1069, UnpricedBought: 2,
		Steps: []explainScheduleRow{{
			Step: 1, World: "mv0:aa1000000000", Oracle: "suite", Kind: policy.KindPytestSuite,
			Flip: 1, DiscountBP: 5000, ExecutorBP: 5000, ValueBP: 2500, CostMS: 427,
			CostBasis: "fit(pytest-suite,off) n=4", Affordable: true, Bought: true, Status: "pass",
		}},
	}
	got := renderSchedule(s)
	for _, want := range []string{
		"residual:   predicted 427 ms, actual 431 ms (+0.9%), over 1 priced purchase(s)",
		"unpriced:   1069 ms over 2 purchase(s) bought at declared rank",
		"BUDGET EXCEEDED by 1000 ms",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendering is missing %q:\n%s", want, got)
		}
	}
}

// A research purchase is labelled in the table AND excluded from the waste
// total, with the exclusion said out loud: a number that vanished silently
// would be the same over-claim in the other direction.
func TestScheduleRendersResearchModeAndExcludedWaste(t *testing.T) {
	s := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleAdaptive, Mode: schedule.ModeCollectInert,
		Inert: true, BudgetMS: 0, Batches: 1, SpentMS: 900, Stop: schedule.StopEmpty,
		Steps: []explainScheduleRow{{
			Step: 1, World: "mv0:aa1000000000", Oracle: "mutate", Kind: policy.KindMutationDiff,
			Flip: 0, ValueBP: 0, CostMS: 18400, CostBasis: schedule.CostBasisDeclaredRank + "(rank 6, n=0)",
			Basis: schedule.BasisResearch, Bought: true, Status: "pass",
		}},
		Waste: &schedule.WasteReport{
			Available: true, SpentMS: 900, WasteMS: 400, WasteBP: 4444,
			GreedyMS: 400, GreedyBP: 4444, ResearchMS: 18000,
			Wasted: []schedule.WasteRow{{
				World: "mv0:bb2000000000", Oracle: "collect", Kind: policy.KindPytestCollect,
				CostMS: 400, Reason: "mv0:bb200000… was eliminated at [status-pass@suite] either way",
			}},
			Rows: []schedule.WasteRow{}, GreedyWasted: []schedule.WasteRow{}, Unbounded: []string{},
		},
	}
	got := renderSchedule(s)
	for _, want := range []string{
		"oracle budget unbounded", "--collect-inert", "bought (research)",
		"evidence waste: 400 ms of 900 (44.4%), greedy 400 ms (44.4%)",
		"research rows excluded from the total: 18000 ms",
		"was eliminated at [status-pass@suite] either way",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendering is missing %q:\n%s", want, got)
		}
	}
}

// A waste number computed with an unbounded ranking metric is an
// UNDER-COUNT, and the reader has to be told: every receipt carrying that
// metric was failed open into "influential".
func TestScheduleRendersUnboundedWasteAsAnUnderCount(t *testing.T) {
	s := &explainSchedule{
		Recorded: true, Batches: 1, SpentMS: 800, Stop: schedule.StopFrontier,
		Waste: &schedule.WasteReport{
			Available: true, SpentMS: 800, WasteMS: 0,
			Wasted: []schedule.WasteRow{}, Rows: []schedule.WasteRow{},
			GreedyWasted: []schedule.WasteRow{},
			Unbounded:    []string{policy.KeyWallMSAsc, policy.MetricTestsPassed},
		},
	}
	got := renderSchedule(s)
	if !strings.Contains(got, "UNDER-COUNT") {
		t.Fatalf("an unbounded waste number is not flagged:\n%s", got)
	}
	if !strings.Contains(got, policy.KeyWallMSAsc) {
		t.Fatalf("the unbounded key is not named:\n%s", got)
	}
}

// The purchase-law assertion must never fire; when it does, it is the
// loudest line in the block, because it means the decision rule admitted a
// world that did not pay.
func TestScheduleRendersPurchaseLawViolation(t *testing.T) {
	got := renderSchedule(&explainSchedule{
		Recorded: true, Batches: 1, Stop: schedule.StopEmpty,
		Violation: "purchase law violated: mv0:aa1 was selected with 1 unpurchased hard gate(s)",
	})
	if !strings.Contains(got, "VIOLATION:") {
		t.Fatalf("a purchase-law violation is not rendered:\n%s", got)
	}
}

// The residual is the cost model's calibration error and it falls out of the
// trace for free: the row records what was predicted and the receipt records
// what it cost.
func TestScheduleRendersCostResidual(t *testing.T) {
	got := renderSchedule(&explainSchedule{
		Recorded: true, Batches: 1, Stop: schedule.StopFrontier,
		Predicted: 1000, Actual: 1006,
	})
	if !strings.Contains(got, "predicted 1000 ms, actual 1006 ms (+0.6%)") {
		t.Fatalf("residual line is wrong:\n%s", got)
	}
}

// A bought row that joined no receipt must not read like a successful
// purchase: "bought" alone would let a missing receipt pass for a passing
// one.
func TestScheduleFlagsAnUnjoinedPurchase(t *testing.T) {
	got := renderSchedule(&explainSchedule{
		Recorded: true, Batches: 1, Stop: schedule.StopEmpty,
		Steps: []explainScheduleRow{{
			Step: 1, World: "mv0:aa1", Oracle: "suite", Kind: policy.KindPytestSuite,
			CostMS: 427, CostBasis: "fit(pytest-suite,off) n=3", Bought: true,
		}},
	})
	if !strings.Contains(got, "no receipt joined") {
		t.Fatalf("an unjoined purchase reads as a completed one:\n%s", got)
	}
}

// microMSText must not round a real per-unit cost to zero: the per-unit term
// of a cheap rung is exactly the term a scheduler multiplies by a large
// number.
func TestMicroMSTextKeepsCheapCoefficientsVisible(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0"}, {3100, "3.1"}, {400, "0.400"}, {15, "0.015"}, {2, "0.00200"},
	} {
		if got := microMSText(tc.in); got != tc.want {
			t.Fatalf("microMSText(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// 0 means UNBOUNDED means M1 semantics, and it must never render as a budget
// of zero — the opposite statement.
func TestBudgetTextNeverPrintsZeroAsABudget(t *testing.T) {
	if got := budgetText(0); !strings.Contains(got, "unbounded") {
		t.Fatalf("budgetText(0) = %q", got)
	}
	if got := budgetText(1500); got != "oracle budget 1500 ms" {
		t.Fatalf("budgetText(1500) = %q", got)
	}
}

// The ledger view is the other half of "recorded, never recomputed": the
// trace has to survive a round trip through the event log and come back
// bracketed by its own race window.
//
// The window matters more than it looks. A workspace holds many races, and a
// trace assembled across two of them would describe an allocation nobody
// made — so events from an EARLIER race for the same intent must not leak
// into a later decision's block.
func TestScheduleWindowIsBoundedByTheRace(t *testing.T) {
	dir := t.TempDir()
	led, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer led.Close()

	appendEvent := func(typ string, v any) {
		t.Helper()
		payload, err := schedule.Payload(v)
		if err != nil {
			t.Fatalf("encode %s: %v", typ, err)
		}
		if _, err := led.Append(typ, payload); err != nil {
			t.Fatalf("append %s: %v", typ, err)
		}
	}
	intent := "mv0:intent"

	// Race 1: a step that must NOT appear in race 2's block.
	appendEvent(evRaceStarted, map[string]any{"intent": intent})
	appendEvent(schedule.EventStarted, schedule.Started{Intent: intent, Schedule: schedule.ScheduleAdaptive})
	appendEvent(schedule.EventStep, schedule.Step{Step: 99, Considered: []schedule.Considered{}, Chosen: []schedule.Chosen{}})
	appendEvent(evDecisionRecorded, map[string]any{
		"schema": object.SchemaDecision, "intent": intent, "type": "REJECT",
		"subject": []string{}, "evidence": []string{}, "policy": "mv0:pol", "rationale": "r",
	})

	// Race 2: the one the block should describe.
	appendEvent(evRaceStarted, map[string]any{"intent": intent})
	appendEvent(evBaselineRecorded, map[string]any{"intent": intent, "collected_total": 8})
	appendEvent(schedule.EventStarted, schedule.Started{
		Intent: intent, Schedule: schedule.ScheduleAdaptive, Mode: schedule.ModeDecision,
		Budget: schedule.StartBudget{MaxOracleMS: 5000}, Parallel: 2,
	})
	appendEvent(schedule.EventStep, schedule.Step{Step: 1, Considered: []schedule.Considered{}, Chosen: []schedule.Chosen{}})
	appendEvent(schedule.EventOracleSkipped, schedule.Skipped{World: "mv0:w", Oracle: "mutate", Reason: "decision-inert"})
	appendEvent(schedule.EventFinished, schedule.Finished{Stop: schedule.StopFrontier, Steps: 1})
	appendEvent(evDecisionRecorded, map[string]any{
		"schema": object.SchemaDecision, "intent": intent, "type": "REJECT",
		"subject": []string{}, "evidence": []string{}, "policy": "mv0:pol", "rationale": "r",
	})

	st, err := loadState(led)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if len(st.Decisions) != 2 {
		t.Fatalf("recorded %d decisions, want 2", len(st.Decisions))
	}
	dr := &st.Decisions[1]
	raceStart := st.raceStartBefore(intent, dr.Seq)
	tr, err := schedule.Collect(st.scheduleWindow(raceStart, dr.Seq))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(tr.Steps) != 1 || tr.Steps[0].Step != 1 {
		t.Fatalf("the window leaked another race's steps: %+v", tr.Steps)
	}
	if !tr.HasStarted || !tr.HasFinished || tr.Started.Budget.MaxOracleMS != 5000 {
		t.Fatalf("the second race's bracketing events did not survive the round trip: %+v", tr)
	}
	if len(tr.Skipped) != 1 || tr.Skipped[0].Oracle != "mutate" {
		t.Fatalf("oracle.skipped did not survive: %+v", tr.Skipped)
	}
	// The control-plane bound comes from baseline.recorded — a base-tree
	// measurement produced before any candidate existed — and never from a
	// candidate's receipt.
	if v, ok := lastBound(st.Baselines, intent, raceStart, dr.Seq); !ok || v != 8 {
		t.Fatalf("collected_base bound = (%d, %v), want (8, true)", v, ok)
	}
	if _, ok := lastBound(st.Corpora, intent, raceStart, dr.Seq); ok {
		t.Fatal("a corpus bound was reported for a race that recorded none; absence must stay absent")
	}
}

// M2b1: A LADDER ROW RENDERS "—" WHERE A VOC ROW RENDERS A NUMBER
// (decision 6). The depth-first arm computes no flip, no discount, no
// executor weight and no score, and under "absent source implies absent
// metric" those columns must not print zeros a reader could aggregate — a `0`
// under FLIP is a VOC row that scored zero, which is a different fact about a
// different arm.
func TestScheduleRendersLadderRowsAsAbsentNotZero(t *testing.T) {
	s := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleFixedBudget, Selector: schedule.SelectorNameLadder,
		BudgetBasis: schedule.BudgetBasisActual, Parallel: 1,
		WorldOrder: []string{"mv0:aa1000000000", "mv0:bb2000000000"},
		BudgetMS:   5000, Batches: 2, SpentMS: 900, Stop: schedule.StopEmpty, SelectionUS: 74,
		Steps: []explainScheduleRow{{
			Step: 1, Order: 1, World: "mv0:aa1000000000", Oracle: "suite", Kind: policy.KindPytestSuite,
			CostMS: 427, CostBasis: "fit(pytest-suite,off) n=4",
			Affordable: true, Bought: true, Status: "pass", ActualMS: 430,
		}},
	}
	got := renderSchedule(s)
	for _, want := range []string{
		"selector:   ladder (depth-first, world order recorded)",
		"charged:    actual",
		"#1 mv0:aa100000…", "rotation 0",
		"—",
		"selection:  74 µs of metalevel time, reported and NOT charged",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendering is missing %q:\n%s", want, got)
		}
	}
	// The row's own cells: a ladder row prints no zeros in the VOC columns.
	for _, line := range strings.Split(got, "\n") {
		if !strings.Contains(line, "suite") || strings.Contains(line, "STEP") {
			continue
		}
		if strings.Contains(line, " 0 ") {
			t.Fatalf("a ladder row printed a zero in a value column:\n%s", line)
		}
	}
	// A VOC row scoring zero still renders 0: the two facts stay distinct.
	//
	// The assertion is scoped to the VALUE-OF-COMPUTATION columns rather than
	// to the whole line, and the scoping is M2b.2's own rule rather than a
	// weakening. A `voc` row carries no finish cost and belongs to no commit
	// set — the rule that computes those did not run — so its FINISH and
	// COMMIT cells are em dashes for exactly the reason the flip cell of a
	// ladder row is one: absent source implies absent metric.
	voc := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleAdaptive, Selector: schedule.SelectorNameVOC,
		BudgetBasis: schedule.BudgetBasisActual, WorldOrder: []string{"mv0:aa1000000000"},
		BudgetMS: 5000, Batches: 1, SpentMS: 10, Stop: schedule.StopFrontier,
		Steps: []explainScheduleRow{{
			Step: 1, World: "mv0:aa1000000000", Oracle: "suite", Kind: policy.KindPytestSuite,
			Flip: 0, DiscountBP: 0, ExecutorBP: 5000, ValueBP: 0, CostMS: 427,
			CostBasis: "fit(pytest-suite,off) n=4", ScoreBasis: schedule.ScoreBasisRung,
			Declined: "no bracket outcome moves the decision",
		}},
	}
	for _, line := range strings.Split(renderSchedule(voc), "\n") {
		if !strings.Contains(line, "suite") || strings.Contains(line, "STEP") {
			continue
		}
		// STEP WORLD ORACLE FLIP DISC EXEC COST(2 fields) SCORE …
		f := strings.Fields(line)
		if len(f) < 9 {
			t.Fatalf("a VOC row rendered too few cells to check:\n%s", line)
		}
		for i, cell := range f[3:9] {
			if cell == "—" {
				t.Fatalf("a VOC row rendered an em dash in value column %d where it holds a measured number:\n%s", i+4, line)
			}
		}
	}
}

// At k > 1 the arm is no longer pure depth-first (decision 7) and the
// rendering says so, because results at different k are never pooled.
func TestScheduleRendersPriorityFillAtParallelAboveOne(t *testing.T) {
	got := renderSchedule(&explainSchedule{
		Recorded: true, Arm: schedule.ScheduleFixedBudget, Selector: schedule.SelectorNameLadder,
		BudgetBasis: schedule.BudgetBasisPredicted, Parallel: 4, BudgetMS: 500, Stop: schedule.StopBudget,
	})
	if !strings.Contains(got, "depth-first PRIORITY FILL (k=4)") {
		t.Fatalf("a k=4 ladder race does not say it is not pure depth-first:\n%s", got)
	}
	if !strings.Contains(got, "charged:    predicted") {
		t.Fatalf("the predicted basis is not rendered:\n%s", got)
	}
}

// A pre-M2b1 trace records no world order, and the renderer says UNKNOWN
// rather than printing digest order. Inventing a past ordering is inventing
// evidence about the one field that decides, under a binding budget, who was
// verified at all.
func TestScheduleRendersUnknownWorldOrderRatherThanInventingOne(t *testing.T) {
	got := renderSchedule(&explainSchedule{
		Recorded: true, Arm: schedule.ScheduleAdaptive, Selector: schedule.SelectorNameVOC,
		BudgetBasis: schedule.BudgetBasisActual, BudgetMS: 0, Stop: schedule.StopEmpty,
	})
	if !strings.Contains(got, "unknown (pre-M2b1 trace)") {
		t.Fatalf("an absent world order is not reported as unknown:\n%s", got)
	}
}

// THE ALLOCATION BOUND renders its number, its target, the allocation behind
// it and its caveats — and a REFUSAL renders as a refusal rather than as a
// zero, because no approximation is reported under the name of an exact
// bound.
func TestScheduleRendersTheAllocationBound(t *testing.T) {
	s := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleFixedBudget, Selector: schedule.SelectorNameLadder,
		BudgetMS: 2000, Stop: schedule.StopEmpty,
		Bound: &schedule.BoundReport{
			Available: true, Subsets: 16, TotalMS: 1520, MinSpendMS: 710,
			SavingMS: 810, SavingBP: 5328, Decision: "SELECT", Subject: "mv0:aa1000000000",
			BudgetMS: 2000, Reachable: true, AlwaysMS: 40,
			Prefixes: []schedule.BoundPrefix{{
				World: "mv0:aa1000000000", Rungs: 3, Bought: 3, CostMS: 710,
				Oracles: []string{policy.KindTreeGuard, policy.KindPytestCollect, policy.KindPytestSuite},
			}},
			Caveats: []string{"costs are counterfactual"},
		},
	}
	got := renderSchedule(s)
	for _, want := range []string{
		"allocation bound: minspend 710 ms of 1520 spent (53.3% headroom)",
		"target d* = SELECT mv0:aa100000…",
		"3 of 3 rungs", "held constant", "caveat: costs are counterfactual",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the bound rendering is missing %q:\n%s", want, got)
		}
	}
	refused := renderSchedule(&explainSchedule{
		Recorded: true, Arm: schedule.ScheduleFixedBudget, Selector: schedule.SelectorNameLadder,
		Stop:  schedule.StopEmpty,
		Bound: &schedule.BoundReport{Refused: "enumeration exceeds the cap of 1000000 prefix-closed subsets"},
	})
	if !strings.Contains(refused, "allocation bound: not computed — enumeration exceeds the cap") {
		t.Fatalf("a refused bound does not render as a refusal:\n%s", refused)
	}
	if strings.Contains(refused, "minspend") {
		t.Fatalf("a refused bound printed a number anyway:\n%s", refused)
	}
}

// M2b.2 §8's RENDERER BAR, as a table over the five trace shapes it names.
//
// It was owed and was not paid: nothing in cmd/mvo exercised `regimeText`,
// `preM2b2`, `finishCell` or `commitCell`, so the untested branches included
// the one that exists to stop the renderer inventing evidence — the pre-M2b.2
// trace, which must say a scarcity test was never recorded rather than print
// `false`, `0` or `[]` as if it had been. That branch was ALSO WRONG: it dated
// the binary from `adaptive_rule`, which reads "voc" on a pre-M2b.2 ledger AND
// on every race this build runs, so it would have stamped "pre-M2b.2 trace" on
// races run this morning.
func TestScheduleRendersEveryRegimeShape(t *testing.T) {
	world := "mv0:aa1000000000"
	rival := "mv0:bb2000000000"
	// A scarce voc2 race: the pool is committed to finishing one world, and
	// the row that priced by the finish denominator says so in three places —
	// the regime header, the PER column and the FINISH/COMMIT cells.
	scarce := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleAdaptive, Selector: schedule.SelectorNameVOC2,
		AdaptiveRule: schedule.SelectorNameVOC, BudgetBasis: schedule.BudgetBasisPredicted,
		WorldOrder: []string{world, rival}, BudgetMS: 1529, Stop: schedule.StopBudget,
		Regimes: []explainRegime{
			{Step: 1, Scarce: true, CommitBasis: schedule.CommitBasisReserved,
				CommitSet: []string{world}, UncommittedMS: 547},
		},
		Steps: []explainScheduleRow{{
			Step: 1, World: world, Oracle: "suite", Kind: policy.KindPytestSuite,
			Flip: 1, DiscountBP: 10000, ExecutorBP: 5000, ValueBP: 5000, CostMS: 689,
			CostBasis: "fit(pytest-suite,off) n=4", ScoreBPPS: 5091,
			ScoreBasis: schedule.ScoreBasisFinish, FinishMS: 982, Committed: true,
		}, {
			Step: 1, World: rival, Oracle: "suite", Kind: policy.KindPytestSuite,
			Flip: 0, CostMS: 689, CostBasis: "fit(pytest-suite,off) n=4",
			ScoreBasis: schedule.ScoreBasisFinish, FinishMS: 982, Committed: false,
			Declined: schedule.DeclineReserved + "the pool is committed to finishing 1 world(s) " +
				"(" + world + "); this world needs 982 ms to finish and 547 ms are uncommitted",
		}},
	}
	got := renderSchedule(scarce)
	for _, want := range []string{
		"regime:     reserved on 1 of 1 step(s); last commit set: 1 world(s) [mv0:aa100000…], 547 ms uncommitted",
		"982 ms", "yes", "no", "finish",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("a scarce race is missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "reserved: the pool is committed to finishing 1 world(s)") {
		t.Fatalf("the reserved decline sentence is not rendered:\n%s", got)
	}

	// A NON-SCARCE voc2 race carries a finish cost it did NOT divide by, and
	// the cell must not let it read as the denominator: parentheses, not a
	// bare number (M2b.2 §3.4).
	notScarce := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleAdaptive, Selector: schedule.SelectorNameVOC2,
		AdaptiveRule: schedule.SelectorNameVOC, BudgetBasis: schedule.BudgetBasisActual,
		WorldOrder: []string{world}, Stop: schedule.StopEmpty,
		Regimes: []explainRegime{{Step: 1, Scarce: false, CommitBasis: schedule.CommitBasisNotScarce,
			CommitSet: []string{}}},
		Steps: []explainScheduleRow{{
			Step: 1, World: world, Oracle: "guard", Kind: policy.KindTreeGuard,
			Flip: 1, DiscountBP: 10000, ExecutorBP: 10000, ValueBP: 10000, CostMS: 11,
			CostBasis: "fit(tree-guard,off) n=3", ScoreBPPS: 909090,
			ScoreBasis: schedule.ScoreBasisRung, FinishMS: 982,
		}},
	}
	got = renderSchedule(notScarce)
	if !strings.Contains(got, "not-scarce on all 1 step(s)") {
		t.Fatalf("a non-scarce race does not name its regime:\n%s", got)
	}
	if !strings.Contains(got, "the FAR claim holds with equality") {
		t.Fatalf("a non-scarce race does not carry the FAR claim it proves from its own ledger:\n%s", got)
	}
	if !strings.Contains(got, "(982 ms)") {
		t.Fatalf("a finish cost that was NOT the denominator is rendered as if it were:\n%s", got)
	}

	// THE UNPRICED FALLBACK. No kind has a local fit, so finish_ms is unknown,
	// the scarcity test is undecidable, and M2b's rule allocated the whole
	// race. The header must say so and name the kinds.
	unpriced := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleAdaptive, Selector: schedule.SelectorNameVOC2,
		AdaptiveRule: schedule.SelectorNameVOC, BudgetBasis: schedule.BudgetBasisActual,
		WorldOrder: []string{world}, Stop: schedule.StopEmpty,
		Regimes: []explainRegime{{Step: 1, CommitBasis: schedule.CommitBasisUnpriced +
			"(pytest-collect,pytest-suite,tree-guard)", CommitSet: []string{}}},
		Steps: []explainScheduleRow{{
			Step: 1, World: world, Oracle: "guard", Kind: policy.KindTreeGuard,
			Flip: 1, CostBasis: "declared-rank(1 of 3)", ScoreBasis: schedule.ScoreBasisRung,
		}},
	}
	got = renderSchedule(unpriced)
	for _, want := range []string{
		"unpriced-fallback(pytest-collect,pytest-suite,tree-guard)",
		"the finish cost is UNKNOWN", "M2b's rule allocated the whole race",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the unpriced fallback is missing %q:\n%s", want, got)
		}
	}
	// UNKNOWN IS NOT ZERO: a row with no finish cost renders an em dash.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "guard") && strings.Contains(line, "0 ms") {
			t.Fatalf("an unknown finish cost rendered as a measurement of zero:\n%s", line)
		}
	}

	// THE LADDER computes no scarcity test and belongs to no commit set, in
	// either era, and the header answers from the ARM rather than from a guess
	// about which binary wrote the trace.
	ladder := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleFixedBudget, Selector: schedule.SelectorNameLadder,
		AdaptiveRule: schedule.SelectorNameVOC, BudgetBasis: schedule.BudgetBasisActual,
		WorldOrder: []string{world}, Stop: schedule.StopEmpty,
		Steps: []explainScheduleRow{{
			Step: 1, Order: 1, World: world, Oracle: "guard", Kind: policy.KindTreeGuard,
			CostMS: 11, CostBasis: "fit(tree-guard,off) n=3",
		}},
	}
	got = renderSchedule(ladder)
	if !strings.Contains(got, "regime:     — (the ladder arm computes no scarcity test and commits to nothing)") {
		t.Fatalf("a ladder race reports a regime it never computed:\n%s", got)
	}
	if strings.Contains(got, "pre-M2b.2") {
		t.Fatalf("a ladder race was dated by a field that does not date it:\n%s", got)
	}

	// A PRE-M2b.2 TRACE: no commit basis anywhere, no score basis on any row.
	// It must say the scarcity test was never recorded, and must never print
	// `false`, `0` or `[]` for a measurement nobody took.
	old := &explainSchedule{
		Recorded: true, Arm: schedule.ScheduleAdaptive, Selector: schedule.SelectorNameVOC,
		AdaptiveRule: schedule.SelectorNameVOC, BudgetBasis: schedule.BudgetBasisActual,
		WorldOrder: []string{world}, Stop: schedule.StopEmpty, noRegime: true,
		Steps: []explainScheduleRow{{
			Step: 1, World: world, Oracle: "guard", Kind: policy.KindTreeGuard,
			Flip: 1, DiscountBP: 10000, ExecutorBP: 10000, ValueBP: 10000, CostMS: 11,
			CostBasis: "fit(tree-guard,off) n=3", ScoreBPPS: 909090,
		}},
	}
	got = renderSchedule(old)
	if !strings.Contains(got, "regime:     unknown (pre-M2b.2 trace); no scarcity test was recorded") {
		t.Fatalf("a pre-M2b.2 trace does not say its regime was never recorded:\n%s", got)
	}
	if strings.Contains(got, "not-scarce") || strings.Contains(got, "reserved") {
		t.Fatalf("a pre-M2b.2 trace was credited with a regime nobody measured:\n%s", got)
	}
	// And a CURRENT `voc` race is NOT mistaken for one: it records a score
	// basis on every row, which no pre-M2b.2 binary could write.
	current := *old
	current.noRegime = false
	current.Regimes = []explainRegime{{Step: 1, CommitSet: []string{}}}
	current.Steps = []explainScheduleRow{{
		Step: 1, World: world, Oracle: "guard", Kind: policy.KindTreeGuard,
		Flip: 1, DiscountBP: 10000, ExecutorBP: 10000, ValueBP: 10000, CostMS: 11,
		CostBasis: "fit(tree-guard,off) n=3", ScoreBPPS: 909090,
		ScoreBasis: schedule.ScoreBasisRung,
	}}
	got = renderSchedule(&current)
	if strings.Contains(got, "pre-M2b.2") {
		t.Fatalf("a current voc race was dated as a pre-M2b.2 trace:\n%s", got)
	}
	if !strings.Contains(got, "computes no scarcity test") {
		t.Fatalf("a current voc race does not say the arm computes no scarcity test:\n%s", got)
	}
}

// `preM2b2` READS THE FINISHING RULE'S OWN VOCABULARY, not the binary's
// default rule. The first version read `adaptive_rule`, which works only while
// the default happens to be the revision — and M2b.2 ships `voc` as the
// default, so it would have dated every current trace to before the block that
// wrote it.
func TestPreM2b2ReadsTheRecordedVocabularyAndNotTheDefaultRule(t *testing.T) {
	rule := schedule.Constants{AdaptiveRule: schedule.SelectorNameVOC}
	older := schedule.Trace{
		Started: schedule.Started{Constants: rule},
		Steps: []schedule.Step{{Step: 1, Considered: []schedule.Considered{
			{World: "mv0:aa", Oracle: "guard", ScoreBPPS: 12},
		}}},
	}
	if !preM2b2(older) {
		t.Fatal("a trace with no commit basis and no score basis was not recognized as pre-M2b.2")
	}
	current := older
	current.Steps = []schedule.Step{{Step: 1, Considered: []schedule.Considered{
		{World: "mv0:aa", Oracle: "guard", ScoreBPPS: 12, ScoreBasis: schedule.ScoreBasisRung},
	}}}
	if preM2b2(current) {
		t.Fatal("a current voc race, which records a score basis on every row, was dated pre-M2b.2")
	}
	reserved := older
	reserved.Steps = []schedule.Step{{Step: 1, CommitBasis: schedule.CommitBasisReserved}}
	if preM2b2(reserved) {
		t.Fatal("a race that recorded a commit basis was dated pre-M2b.2")
	}
}
