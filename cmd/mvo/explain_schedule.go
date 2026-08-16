package main

// M2b: `mvo explain --schedule` — what the scheduler bought, what it
// considered and DECLINED, what each purchase was priced at, and how much of
// the spend influenced nothing.
//
// The block RENDERS ROWS AND RECOMPUTES NO SCORE. Every flip, discount,
// executor weight, predicted cost and score printed here came off the ledger
// exactly as the scheduler computed it (M2b decision 17). That is what makes
// the allocation auditable after the cost table has moved, and it is what an
// acceptance step proves by corrupting the workspace's fitted coefficients
// after a race and asserting this rendering is byte-identical.
//
// Evidence waste is the one number here that is DERIVED (decision 18), and
// it has to be: at buy time the scheduler does not know the final decision.
// It is computed from the recorded trace, the recorded receipts and the
// recorded decision, so improving its definition never invalidates a race.

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/schedule"
)

// explainSchedule is the derived allocation block. Nothing in it is stored:
// the rows are RE-READ from the ledger (never recomputed) and the waste
// numbers are recomputed from them by pure functions.
type explainSchedule struct {
	// Recorded reports whether an allocation trace exists at all. A
	// fixed-ladder race and a pre-M2b race record none, and the block says so
	// rather than rendering an empty table that reads like "the scheduler
	// bought nothing".
	Recorded  bool                 `json:"recorded"`
	Arm       string               `json:"arm"`
	Mode      string               `json:"mode"`
	BudgetMS  int64                `json:"budget_ms"`
	Parallel  int                  `json:"parallel"`
	Inert     bool                 `json:"collect_inert"`
	Steps     []explainScheduleRow `json:"steps"`
	CostModel []explainCostRow     `json:"cost_model"`
	Skipped   []schedule.Skipped   `json:"skipped"`
	Stop      string               `json:"stop"`
	Batches   int                  `json:"batches"`
	SpentMS   int64                `json:"spent_ms"`
	Released  int64                `json:"released_ms"`
	// Predicted and Actual are the calibration residual's two halves and
	// they cover THE SAME ROWS: the purchases priced from a fit. A
	// declared-rank purchase has no prediction to compare against, so its
	// spend is reported separately rather than folded into a percentage.
	Predicted      int64 `json:"predicted_ms"`
	Actual         int64 `json:"actual_ms"`
	PricedBought   int   `json:"priced_bought"`
	UnpricedMS     int64 `json:"unpriced_ms"`
	UnpricedBought int   `json:"unpriced_bought"`
	// Unscheduled is the spend that no schedule.step named — phase B2's
	// cohort barrier, which the design describes as an action and which v0
	// runs as an unscheduled barrier (§10, amended).
	Unscheduled   []explainUnscheduled  `json:"unscheduled"`
	UnscheduledMS int64                 `json:"unscheduled_ms"`
	Totals        explainScheduleTotals `json:"totals"`
	Waste         *schedule.WasteReport `json:"waste"`
	// RankingIncomplete is decision 4's one honest corner: withholding
	// monotonicity holds for the PASS SET, not for the RANKING.
	RankingIncomplete bool `json:"ranking_incomplete"`
	// Violation is the purchase-law assertion, and it must always be empty.
	// It is rendered only when it is not, because an invariant this
	// load-bearing should be observed rather than assumed.
	Violation string `json:"violation"`
}

// explainUnscheduled is one receipt the allocation trace never named: it was
// produced by a phase the scheduler does not allocate over. Rendering it
// under its own heading is the honest alternative to letting the trace imply
// completeness — every millisecond of the race's oracle spend appears
// somewhere, and the ones the allocator never chose say so.
type explainUnscheduled struct {
	World    string `json:"world"`
	Kind     string `json:"kind"`
	Receipt  string `json:"receipt"`
	Status   string `json:"status"`
	ActualMS int64  `json:"actual_ms"`
	Phase    string `json:"phase"`
}

type explainScheduleTotals struct {
	Considered int `json:"considered"`
	Bought     int `json:"bought"`
	Declined   int `json:"declined"`
}

// explainScheduleRow is one considered purchase as recorded, plus the ONE
// derived cell: the receipt's own status, so a reader sees what the purchase
// actually reported next to what it was predicted to be worth.
type explainScheduleRow struct {
	Step         int      `json:"step"`
	World        string   `json:"world"`
	Oracle       string   `json:"oracle"`
	Kind         string   `json:"kind"`
	Flip         int      `json:"flip"`
	FlipOutcomes []string `json:"flip_outcomes"`
	DiscountBP   int64    `json:"discount_bp"`
	ExecutorBP   int64    `json:"executor_bp"`
	ValueBP      int64    `json:"value_bp"`
	CostMS       int64    `json:"cost_ms"`
	CostBasis    string   `json:"cost_basis"`
	ScoreBPPS    int64    `json:"score_bpps"`
	Affordable   bool     `json:"affordable"`
	HardGate     bool     `json:"hard_gate"`
	Basis        string   `json:"basis"`
	Bought       bool     `json:"bought"`
	Declined     string   `json:"declined"`
	Receipt      string   `json:"receipt"`
	Status       string   `json:"status"` // the receipt's verdict; "" when unjoined
	ActualMS     int64    `json:"actual_ms"`
}

// explainCostRow is one kind's recorded cost model. A row whose basis is
// `declared-rank` carries NO millisecond figure and never renders one: a
// number nobody measured must not appear beside one somebody did (M2a's own
// "no local measurement (n=…)" rule, extended from the report to the
// allocator).
type explainCostRow struct {
	Kind      string `json:"kind"`
	Basis     string `json:"basis"`
	N         int    `json:"n"`
	Seal      string `json:"plugin_autoload"`
	FixedMS   int64  `json:"fixed_ms"`
	PerUnitUS int64  `json:"per_unit_micro_ms"`
	Estimator string `json:"estimator"`
	Unit      string `json:"unit"`
	Measured  bool   `json:"measured"`
}

// scheduleBlock assembles the block for one recorded race decision. It
// returns nil when the decision is not a race decision at all; a race with no
// trace returns a block with Recorded == false, which is a different fact and
// gets a different sentence.
func scheduleBlock(st *ledgerState, dr *decisionRec, pol policy.Policy,
	worlds []object.RecordedWorld, receipts []object.RecordedReceipt) *explainSchedule {

	raceStart := st.raceStartBefore(dr.Decision.Intent, dr.Seq)
	tr, err := schedule.Collect(st.scheduleWindow(raceStart, dr.Seq))
	if err != nil {
		// A trace this binary cannot decode is reported as absent rather than
		// as empty: "we could not read it" and "there was none" are different
		// facts and the operator has to be able to tell them apart.
		return &explainSchedule{Recorded: false, Stop: "unreadable: " + err.Error()}
	}
	out := &explainSchedule{
		Recorded:          !tr.Empty(),
		Mode:              tr.Started.Mode,
		BudgetMS:          tr.Started.Budget.MaxOracleMS,
		Parallel:          tr.Started.Parallel,
		Inert:             tr.Started.Mode == schedule.ModeCollectInert,
		Steps:             []explainScheduleRow{},
		CostModel:         []explainCostRow{},
		Skipped:           tr.Skipped,
		Arm:               tr.Started.Schedule,
		Stop:              tr.Finished.Stop,
		Batches:           len(tr.Steps),
		SpentMS:           tr.Finished.Budget.SpentMS,
		Released:          tr.Finished.Budget.ReleasedMS,
		RankingIncomplete: tr.Finished.RankingIncomplete,
		Violation:         tr.Finished.Violation,
		Totals: explainScheduleTotals{
			Considered: tr.Finished.Considered,
			Bought:     tr.Finished.Bought,
			Declined:   tr.Finished.Declined,
		},
	}
	if out.Skipped == nil {
		out.Skipped = []schedule.Skipped{}
	}
	if !out.Recorded {
		return out
	}

	joined := map[string]schedule.Purchase{}
	for _, p := range tr.Join(pol, receipts) {
		joined[rowKey(p.Step, p.Row.World, p.Row.Oracle)] = p
	}
	seenReceipt := map[string]bool{}
	for _, s := range tr.Steps {
		for _, r := range s.Considered {
			row := explainScheduleRow{
				Step: s.Step, World: r.World, Oracle: r.Oracle, Kind: r.Kind,
				Flip: r.Flip, FlipOutcomes: r.FlipOutcomes, DiscountBP: r.DiscountBP,
				ExecutorBP: r.ExecutorBP, ValueBP: r.ValueBP, CostMS: r.CostMS,
				CostBasis: r.CostBasis, ScoreBPPS: r.ScoreBPPS, Affordable: r.Affordable,
				HardGate: r.HardGate, Basis: r.Basis, Bought: r.Declined == "",
				Declined: r.Declined,
			}
			if row.FlipOutcomes == nil {
				row.FlipOutcomes = []string{}
			}
			priced := !strings.HasPrefix(r.CostBasis, schedule.CostBasisDeclaredRank)
			if p, ok := joined[rowKey(s.Step, r.World, r.Oracle)]; ok && p.Rec != nil {
				row.Receipt, row.Status, row.ActualMS = p.Receipt, p.Rec.Result.Status, p.Rec.Cost.WallMS
				seenReceipt[p.Receipt] = true
				if priced {
					out.Actual += p.Rec.Cost.WallMS
					out.Predicted += r.CostMS
					out.PricedBought++
				} else {
					out.UnpricedMS += p.Rec.Cost.WallMS
					out.UnpricedBought++
				}
			}
			out.Steps = append(out.Steps, row)
		}
	}
	// Phase B2's receipts, which no schedule.step named. §2.2 and §10
	// describe the cohort reducer as a dependent PURCHASE that closes the
	// cohort; v0 runs it as an unscheduled barrier after phase B (§10,
	// amended), so its spend is real, is charged to no budget, and is
	// reported here rather than left to look like completeness.
	for _, rr := range receipts {
		if seenReceipt[rr.Digest] {
			continue
		}
		out.Unscheduled = append(out.Unscheduled, explainUnscheduled{
			World: rr.Receipt.World, Kind: rr.Receipt.Oracle.ID, Receipt: rr.Digest,
			Status: rr.Receipt.Result.Status, ActualMS: rr.Receipt.Cost.WallMS,
			Phase: unscheduledPhase(rr.Receipt.Oracle.ID),
		})
		out.UnscheduledMS += rr.Receipt.Cost.WallMS
	}
	sort.Slice(out.Unscheduled, func(i, j int) bool {
		if out.Unscheduled[i].Kind != out.Unscheduled[j].Kind {
			return out.Unscheduled[i].Kind < out.Unscheduled[j].Kind
		}
		return out.Unscheduled[i].World < out.Unscheduled[j].World
	})
	if out.Unscheduled == nil {
		out.Unscheduled = []explainUnscheduled{}
	}
	out.CostModel = costModelRows(tr)

	bounds, _ := scheduleBounds(st, dr, raceStart, receipts)
	rep, werr := schedule.Waste(schedule.WasteInput{
		Policy: pol, Worlds: worlds, Receipts: receipts, Trace: tr,
		Bounds: bounds, Decide: race.Decide,
	})
	if werr == nil {
		out.Waste = &rep
	}
	return out
}

func rowKey(step int, world, oracle string) string {
	return fmt.Sprintf("%d\x00%s\x00%s", step, world, oracle)
}

// unscheduledPhase names the phase a receipt the allocator never chose came
// from. The cohort reducer is the only one v0 produces: it runs after phase
// B as a barrier over every world that observed, which is why the scheduler
// neither prices it nor charges it.
func unscheduledPhase(kind string) string {
	if kind == policy.KindCorpusDifferential {
		return "B2 (cohort barrier, not scheduled)"
	}
	return "not scheduled"
}

// costModelRows renders the recorded cost table: the fitted coefficients the
// race allocated against, plus a `declared-rank` row for every kind whose
// trace rows say it was priced without a fit. The RECORDED rows decide,
// never the workspace's current table — that is the whole point of the
// snapshot (decision 13).
func costModelRows(tr schedule.Trace) []explainCostRow {
	out := []explainCostRow{}
	seen := map[string]bool{}
	for _, c := range tr.Started.CostTable {
		row := explainCostRow{
			Kind: c.Kind, Basis: c.Basis, N: c.N, Seal: c.PluginAutoload,
			FixedMS: c.FixedMS, PerUnitUS: c.PerUnitMicroMS,
			Estimator: c.Estimator, Unit: c.Unit,
			Measured: c.Basis != schedule.CostBasisDeclaredRank,
		}
		out = append(out, row)
		seen[c.Kind] = true
	}
	// A kind the snapshot does not name but the rows priced anyway: report
	// it from the ROWS, which are the record, rather than leaving it out and
	// letting a reader assume it was free.
	for kind, basis := range tr.CostBasisByKind() {
		if seen[kind] || !strings.HasPrefix(basis, schedule.CostBasisDeclaredRank) {
			continue
		}
		prof, _ := policy.KindProfile(kind)
		out = append(out, explainCostRow{
			Kind: kind, Basis: schedule.CostBasisDeclaredRank, Unit: prof.Unit, Measured: false,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// scheduleBounds assembles the CONTROL-PLANE ceilings the waste metric's
// bracket may use, from the race's own ledger window: the base tree's
// collect measurement (`baseline.recorded`, produced before any candidate
// existed) and the pinned corpus's case count (`corpus.recorded`, phase 0).
//
// Nothing here is read from a candidate's receipt, and that is the whole
// point of the function. An absent bound is returned as zero AND reported as
// absent, so the bracket fails OPEN — an unbounded metric makes every
// receipt carrying it read as influential, which under-reports waste rather
// than over-reporting it.
func scheduleBounds(st *ledgerState, dr *decisionRec, raceStart int64, receipts []object.RecordedReceipt) (schedule.Bounds, bool) {
	var b schedule.Bounds
	complete := true
	if v, ok := lastBound(st.Baselines, dr.Decision.Intent, raceStart, dr.Seq); ok {
		b.CollectedBase = v
	} else {
		complete = false
	}
	if v, ok := lastBound(st.Corpora, dr.Decision.Intent, raceStart, dr.Seq); ok {
		b.CorpusCases = v
	} else if v, ok := corpusCasesFromReceipts(receipts); ok {
		// M2a's metric-provenance table is explicit that corpus_cases_total
		// comes from the CORPUS OBJECT, materialized on the base tree before
		// any candidate existed — "no candidate authors that number in any
		// regime" — so a receipt is a legitimate second reading of it when
		// the corpus event is outside the window. collected_base has no such
		// second reading and is deliberately not given one.
		b.CorpusCases = v
	}
	return b, complete
}

func corpusCasesFromReceipts(receipts []object.RecordedReceipt) (int64, bool) {
	for _, rr := range receipts {
		if v, ok := rr.Receipt.Result.Metrics[policy.MetricCorpusCasesTotal]; ok && v > 0 {
			return v, true
		}
	}
	return 0, false
}

// writeSchedule renders the human surface (M2b §4.4). It prints recorded
// numbers and one derived block, and it never recomputes a score.
func writeSchedule(w io.Writer, s *explainSchedule) {
	if s == nil {
		return
	}
	fmt.Fprintln(w, "")
	if !s.Recorded {
		// Absent source implies absent metric. A race under the fixed ladder
		// has no allocation to explain, and printing an empty table would
		// read as "the scheduler considered nothing", which is a claim about
		// a scheduler that never ran.
		detail := "no allocation trace recorded for this race"
		if strings.HasPrefix(s.Stop, "unreadable:") {
			detail = "allocation trace present but unreadable by this binary (" +
				strings.TrimPrefix(s.Stop, "unreadable: ") + ")"
		}
		fmt.Fprintf(w, "schedule: %s\n", detail)
		fmt.Fprintln(w, "          (raced under the fixed ladder, or by a binary predating the scheduler)")
		return
	}

	fmt.Fprintf(w, "schedule (%s", budgetText(s.BudgetMS))
	fmt.Fprintf(w, ", spent %d ms, %d batches, stopped: %s", s.SpentMS, s.Batches, dash(s.Stop))
	if s.Inert {
		fmt.Fprint(w, ", --collect-inert")
	}
	fmt.Fprintln(w, "):")

	// The table is laid out ALONE and the decline reasons are interleaved
	// afterwards, and that two-step is not fussiness. §4.4 puts a declined
	// row's reason on its own indented line directly under the row — it is
	// the only place the record says why the budget went somewhere else, and
	// a reason that names no world and no rung tells a reader nothing. But a
	// line with no tab has no cell in any column, so writing it INTO the
	// tabwriter terminates the column block and every subsequent row loses
	// its alignment. Laying the rows out first and splicing the sentences in
	// afterwards is what gives both properties at once.
	var tb strings.Builder
	tw := tabwriter.NewWriter(&tb, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "  STEP\tWORLD\tORACLE\tFLIP\tDISC\tEXEC\tCOST\tSCORE\tOUTCOME")
	for _, r := range s.Steps {
		fmt.Fprintf(tw, "  %d\t%s\t%s\t%d\t%d\t%d\t%s\t%d\t%s\n",
			r.Step, short(r.World), r.Oracle, r.Flip, r.DiscountBP, r.ExecutorBP,
			costCell(r), r.ScoreBPPS, outcomeCell(r))
	}
	_ = tw.Flush()
	laid := strings.Split(strings.TrimSuffix(tb.String(), "\n"), "\n")
	if len(laid) > 0 {
		fmt.Fprintln(w, laid[0]) // the header
	}
	for i, r := range s.Steps {
		if i+1 < len(laid) {
			fmt.Fprintln(w, laid[i+1])
		}
		if r.Declined != "" {
			fmt.Fprintf(w, "          %s\n", r.Declined)
		}
	}
	if len(s.Skipped) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "  never bought (terminal declines):")
		for _, sk := range s.Skipped {
			fmt.Fprintf(w, "    %s/%-10s %s\n", short(sk.World), sk.Oracle, sk.Reason)
		}
	}

	if len(s.CostModel) > 0 {
		fmt.Fprintln(w, "")
		label := "  cost model:"
		for _, c := range s.CostModel {
			fmt.Fprintf(w, "%s %-22s %s\n", label, c.Kind, costModelText(c))
			label = "             "
		}
	}
	if s.Predicted > 0 || s.Actual > 0 {
		// THE RESIDUAL IS OVER THE PRICED ROWS ONLY, on both sides. A
		// declared-rank row has no millisecond prediction at all (decision
		// 7c), so summing predictions over the fitted rows and actuals over
		// every receipt divided a partial sum by a total one and printed
		// "+103550.0%" — a number that measures nothing, sold in §2.6 as the
		// calibration residual M2d needs. The unpriced spend is real and is
		// reported on its own line, where it says what it is.
		fmt.Fprintf(w, "  residual:   predicted %d ms, actual %d ms (%s), over %d priced purchase(s)\n",
			s.Predicted, s.Actual, residualText(s.Predicted, s.Actual), s.PricedBought)
	}
	if s.UnpricedMS > 0 || s.UnpricedBought > 0 {
		fmt.Fprintf(w, "  unpriced:   %d ms over %d purchase(s) bought at declared rank, outside the residual (no prediction to compare against)\n",
			s.UnpricedMS, s.UnpricedBought)
	}
	if s.BudgetMS > 0 && s.SpentMS > s.BudgetMS {
		// The bound did not bind, and a budget-matched experiment cannot
		// silently compare unequal spends. An unpriced purchase is
		// affordable while any pool remains (budget.go's named residual), so
		// the overrun is the cost of the last dispatch — and its SIZE is
		// chosen by whatever ran, which is the lever adversarial vector 24
		// pulls.
		fmt.Fprintf(w, "  BUDGET EXCEEDED by %d ms (%d ms bought at declared rank; an unpriced purchase is affordable while any pool remains)\n",
			s.SpentMS-s.BudgetMS, s.UnpricedMS)
	}
	if len(s.Unscheduled) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "  not scheduled (%d ms, chosen by no schedule.step):\n", s.UnscheduledMS)
		for _, u := range s.Unscheduled {
			fmt.Fprintf(w, "    %s/%-20s %6d ms  %s — %s\n",
				short(u.World), u.Kind, u.ActualMS, u.Status, u.Phase)
		}
	}
	if s.RankingIncomplete {
		fmt.Fprintln(w, "  ranking:    a passing candidate is missing a receipt a ranking key reads; the ORDER is not monotone under withholding (only the pass set is)")
	}
	if s.Violation != "" {
		fmt.Fprintf(w, "  VIOLATION:  %s\n", s.Violation)
	}
	writeWaste(w, s.Waste)
}

// writeWaste renders decision 18's two numbers and the rows behind them.
func writeWaste(w io.Writer, rep *schedule.WasteReport) {
	if rep == nil || !rep.Available {
		return
	}
	fmt.Fprintln(w, "")
	if rep.SpentMS == 0 {
		fmt.Fprintln(w, "  evidence waste: no spend recorded on decision-basis receipts")
		return
	}
	fmt.Fprintf(w, "  evidence waste: %d ms of %d (%s), greedy %d ms (%s)\n",
		rep.WasteMS, rep.SpentMS, pctText(rep.WasteBP), rep.GreedyMS, pctText(rep.GreedyBP))
	for _, row := range rep.Wasted {
		fmt.Fprintf(w, "    %s/%-10s %6d ms  — %s\n", short(row.World), oracleLabel(row), row.CostMS, row.Reason)
	}
	if rep.ResearchMS > 0 {
		// A purchase whose stated purpose is to influence no decision is
		// 100 % waste under the PRD's definition, so counting it would make
		// the metric meaningless. Excluded by construction — and said out
		// loud, because an excluded number that vanished silently would be
		// the same over-claim in the other direction.
		fmt.Fprintf(w, "    research rows excluded from the total: %d ms (--collect-inert)\n", rep.ResearchMS)
	}
	if len(rep.Unbounded) > 0 {
		fmt.Fprintf(w, "    UNDER-COUNT: no control-plane ceiling for %s; every receipt carrying it failed OPEN and is counted as influential\n",
			strings.Join(rep.Unbounded, ", "))
	}
}

func oracleLabel(row schedule.WasteRow) string {
	if row.Oracle != "" {
		return row.Oracle
	}
	return row.Kind
}

// costCell renders a predicted cost. A row priced without a fit prints its
// declared RANK and no millisecond figure at all.
func costCell(r explainScheduleRow) string {
	if strings.HasPrefix(r.CostBasis, schedule.CostBasisDeclaredRank) {
		return "rank-only"
	}
	return fmt.Sprintf("%d ms", r.CostMS)
}

// outcomeCell says what happened to a considered purchase: bought (with the
// receipt's own verdict when it joined one), or declined.
func outcomeCell(r explainScheduleRow) string {
	if !r.Bought {
		return "declined"
	}
	label := "bought"
	if r.Basis == schedule.BasisResearch {
		label = "bought (research)"
	}
	if r.Status == "" {
		// The row says a purchase was made and no receipt joined it. Saying
		// "bought" alone would let a missing receipt read as a successful
		// one.
		return label + "  (no receipt joined)"
	}
	return label + "  (" + r.Status + ")"
}

func costModelText(c explainCostRow) string {
	if !c.Measured {
		return fmt.Sprintf("%s (no local measurement)", schedule.CostBasisDeclaredRank)
	}
	unit := "unit"
	if c.Unit != "" {
		unit = strings.TrimSuffix(c.Unit, "s")
	}
	if c.Estimator == schedule.EstimatorMedianFixed {
		// The fixed cost was measured n times; the SLOPE was not measurable
		// at all, because every sample in the population had the same unit
		// count. Printing "+ 0 ms/test" would report an unmeasured slope as
		// a measured zero, which is the one thing the no-fit-no-number rule
		// exists to prevent.
		return fmt.Sprintf("fit(%s) n=%d  %d ms fixed, no per-%s coefficient (units do not vary)  (%s)",
			dash(c.Seal), c.N, c.FixedMS, unit, dash(c.Estimator))
	}
	return fmt.Sprintf("fit(%s) n=%d  %d ms + %s ms/%s  (%s)",
		dash(c.Seal), c.N, c.FixedMS, microMSText(c.PerUnitUS), unit, dash(c.Estimator))
}

// microMSText renders thousandths of a millisecond as milliseconds without
// rounding a real per-unit cost to zero: the per-unit term of a cheap rung
// is exactly the term a scheduler multiplies by a large number.
func microMSText(v int64) string {
	switch {
	case v == 0:
		return "0"
	case v >= 1000:
		return fmt.Sprintf("%.1f", float64(v)/1000)
	case v >= 10:
		return fmt.Sprintf("%.3f", float64(v)/1000)
	default:
		return fmt.Sprintf("%.5f", float64(v)/1000)
	}
}

func budgetText(ms int64) string {
	if ms <= 0 {
		// 0 means unbounded means M1 semantics, and it must not render as a
		// budget of zero — the opposite statement.
		return "oracle budget unbounded"
	}
	return fmt.Sprintf("oracle budget %d ms", ms)
}

func residualText(predicted, actual int64) string {
	if predicted == 0 {
		return "no prediction"
	}
	delta := actual - predicted
	return fmt.Sprintf("%+.1f%%", float64(delta)*100/float64(predicted))
}

func pctText(bp int64) string { return fmt.Sprintf("%.1f%%", float64(bp)/100) }
