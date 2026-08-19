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
	Recorded bool   `json:"recorded"`
	Arm      string `json:"arm"`
	// Selector is which of decision 1's two rules ordered this race's
	// purchases, and it is a different fact from the arm label: `arm` is what
	// the operator asked for, `selector` is what ranked the frontier. On a
	// pre-M2b1 trace it reads "voc" exactly rather than by assumption — no
	// earlier binary could record a trace for any other arm.
	Selector string `json:"selector"`
	// BudgetBasis, Rotation and WorldOrder are the three fields that make two
	// races COMPARABLE (M2b1 §3): what the pool was charged, which rotation
	// of the control-plane order this replicate ran, and the order itself. An
	// empty world order is reported as unknown and NEVER as digest order,
	// because inventing a past ordering is inventing evidence.
	BudgetBasis string   `json:"budget_basis"`
	Rotation    int      `json:"rotation"`
	WorldOrder  []string `json:"world_order"`
	// AdaptiveRule is the rule THE BINARY defaulted to for --schedule=adaptive
	// when this race ran — a property of the build, not of the run, and not
	// the same fact as `selector`. It reads "voc" EXACTLY on a pre-M2b.2
	// trace, because no earlier binary could allocate by any other rule, and
	// that is what tells a reader the regime fields below were never recorded
	// rather than recorded false.
	AdaptiveRule string `json:"adaptive_rule"`
	// Regimes is one row per step: decision 1's scarcity test, decision 3's
	// commit set, and the pool the reservation did not commit. It is EMPTY on
	// a pre-M2b.2 trace — absent, never a fabricated `false` — and empty on an
	// arm that holds no such concept.
	Regimes []explainRegime `json:"regimes"`
	// Coverage is M2d.1 decision 8's DERIVED figure: what fraction of this
	// race's recorded steps exercised the rule the race allocated under, per
	// witness. It is RENDERED and never recomputed into a score — extending
	// accept step 5d's rule to the coverage line — and it is absent for a
	// race with no trace, `unknown` for a pre-M2b.2 one and `—` for a ladder,
	// never 0 %.
	Coverage *schedule.CoverageReport `json:"coverage,omitempty"`
	// noRegime says the regime fields were NEVER RECORDED rather than recorded
	// empty: a pre-M2b.2 trace, which ran no scarcity test at all. It is not on
	// the wire because it is not a fact about the race — it is what the
	// renderer knows about the ledger it is reading.
	noRegime  bool
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
	// SelectionUS is the arm's own metalevel time, measured: REPORTED AND NOT
	// CHARGED (F8). PRD §11's budget includes selection cost and this harness
	// charges only oracle milliseconds, so the field is what keeps the claim
	// "selection is 0.07-0.3% of the purchase it prices" re-checkable.
	SelectionUS int64 `json:"selection_us"`
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
	// Bound is the retrospective allocation bound (M2b1 §5), computed only
	// under --bound because it costs a quarter-second. It is DERIVED, never
	// recorded: improving its definition invalidates no race.
	Bound *schedule.BoundReport `json:"bound,omitempty"`
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

// explainRegime is one step's apportionment record (M2b.2 decisions 1 and 3):
// which rule allocated it, whether the pool could finish every alive world,
// which worlds it committed to finishing, and what it left uncommitted.
type explainRegime struct {
	Step          int      `json:"step"`
	Scarce        bool     `json:"scarce"`
	CommitBasis   string   `json:"commit_basis"`
	CommitSet     []string `json:"commit_set"`
	UncommittedMS int64    `json:"uncommitted_ms"`
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
	Step int `json:"step"`
	// Order is the LADDER arm's depth-first rank of this row within its step,
	// and 0 on a VOC row, which has no depth-first rank at all.
	Order        int      `json:"order"`
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
	// ScoreBasis says which denominator produced ScoreBPPS: "rung" is M2b's
	// own cost of the next purchase, "finish" is M2b.2's cost of completing
	// the world. "" is a row that computed no score at all and renders "—".
	ScoreBasis string `json:"score_basis"`
	// FinishMS and Committed are the finishing rule's two facts about this
	// world at this step. A row whose ScoreBasis is "rung" may still carry a
	// finish cost — a measurement the rule computed and did NOT divide by —
	// and the rendering labels it as such rather than letting it read as the
	// denominator.
	FinishMS  int64 `json:"finish_ms"`
	Committed bool  `json:"committed"`
	// Admissible, AllowanceMS and PassWithheld are what the COVERAGE
	// witnesses are computed from (M2d.1 decision 5). They are rendered
	// because a coverage figure a reader cannot re-derive from the same JSON
	// the harness read is a figure nobody can check.
	Admissible   bool   `json:"admissible"`
	Affordable   bool   `json:"affordable"`
	AllowanceMS  int64  `json:"allowance_ms"`
	PassWithheld bool   `json:"pass_withheld"`
	HardGate     bool   `json:"hard_gate"`
	Basis        string `json:"basis"`
	Bought       bool   `json:"bought"`
	Declined     string `json:"declined"`
	Receipt      string `json:"receipt"`
	Status       string `json:"status"` // the receipt's verdict; "" when unjoined
	ActualMS     int64  `json:"actual_ms"`
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
	worlds []object.RecordedWorld, receipts []object.RecordedReceipt,
	bound bool, boundCap int64) *explainSchedule {

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
		Selector:          tr.Started.Selector,
		AdaptiveRule:      tr.Started.Constants.AdaptiveRule,
		Regimes:           []explainRegime{},
		noRegime:          preM2b2(tr),
		BudgetBasis:       tr.Started.BudgetBasis,
		Rotation:          tr.Started.Rotation,
		WorldOrder:        tr.Started.WorldOrder,
		SelectionUS:       tr.Finished.SelectionUS,
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
	cov := schedule.Coverage(tr)
	out.Coverage = &cov
	if out.WorldOrder == nil {
		out.WorldOrder = []string{}
	}
	// THE BOUND IS COMPUTED WHETHER OR NOT A TRACE EXISTS, and that is not an
	// oversight: it is a function of the recorded POLICY, WORLDS and RECEIPTS
	// alone, so it answers "how much allocation headroom did this instance
	// have" for an untraced M1 race exactly as well as for a scheduled one.
	// A denominator that only existed for the arm that recorded a trace would
	// be no denominator at all.
	if bound {
		rep, berr := schedule.Bound(schedule.BoundInput{
			Policy: pol, Worlds: worlds, Receipts: receipts,
			Decide: race.Decide, BudgetMS: tr.Started.Budget.MaxOracleMS, Cap: boundCap,
		})
		if berr == nil {
			out.Bound = &rep
		}
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
		// ABSENT IS ABSENT (M2b.2 §3.4). A pre-M2b.2 trace carries no
		// scarcity test at all, and reporting one as `false` would invent a
		// regime nobody measured — the same argument M2b.1 made about
		// world_order, reused because it is the same argument.
		if !preM2b2(tr) {
			out.Regimes = append(out.Regimes, explainRegime{
				Step: s.Step, Scarce: s.Scarce, CommitBasis: s.CommitBasis,
				CommitSet: s.CommitSet, UncommittedMS: s.UncommittedMS,
			})
		}
		for _, r := range s.Considered {
			row := explainScheduleRow{
				Step: s.Step, Order: r.Order, World: r.World, Oracle: r.Oracle, Kind: r.Kind,
				Flip: r.Flip, FlipOutcomes: r.FlipOutcomes, DiscountBP: r.DiscountBP,
				ExecutorBP: r.ExecutorBP, ValueBP: r.ValueBP, CostMS: r.CostMS,
				CostBasis: r.CostBasis, ScoreBPPS: r.ScoreBPPS, ScoreBasis: r.ScoreBasis,
				FinishMS: r.FinishMS, Committed: r.Committed, Affordable: r.Affordable,
				Admissible: r.Admissible, AllowanceMS: r.AllowanceMS, PassWithheld: r.PassWithheld,
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

// preM2b2 reports whether this trace was written by a binary that predates the
// finishing rule.
//
// THE TEST LIVES IN `internal/schedule/coverage.go`, beside the witnesses it
// belongs with, and this is the call (M2d.1 decision 8). A second copy of an
// era test is how the two copies eventually disagree about which era a ledger
// is from — and this renderer already made the version of that mistake the
// package comment there records: dating the binary off `adaptive_rule`, which
// normalizes to "voc" for a pre-M2b.2 ledger AND for a race run this morning.
func preM2b2(tr schedule.Trace) bool { return schedule.PreM2b2(tr) }

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
	fmt.Fprintf(w, "  selector:   %s\n", selectorText(s))
	fmt.Fprintf(w, "  charged:    %s\n", basisText(s.BudgetBasis))
	fmt.Fprintf(w, "  order:      %s\n", worldOrderText(s))
	fmt.Fprintf(w, "  regime:     %s\n", regimeText(s))
	// M2d.1 decision 10: printed ALWAYS, including at 100 %. A number that
	// appears only when it is bad is a number nobody learns to read. It is
	// RENDERED from the derived report and recomputes no score.
	if s.Coverage != nil {
		// BLOCKER B3: BOTH FIGURES, ALWAYS. `exercised` is the steps whose
		// allocation depended on the rule; `consulted` is the steps on which
		// the rule merely ran, which is what the first version of this line
		// printed under the name `coverage`.
		fmt.Fprintf(w, "  exercised:  %s (rule %s, baseline %s)\n",
			s.Coverage.Summary(), s.Coverage.Rule, dash(s.Coverage.Baseline))
		fmt.Fprintf(w, "  consulted:  %s (the rule's own regime ran; it is NOT coverage)\n",
			s.Coverage.ConsultedSummary())
	}

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
	ladder := s.Selector == schedule.SelectorNameLadder
	fmt.Fprintln(tw, "  STEP\tWORLD\tORACLE\tFLIP\tDISC\tEXEC\tCOST\tSCORE\tPER\tFINISH\tCOMMIT\tOUTCOME")
	for _, r := range s.Steps {
		// A LADDER ROW RENDERS "—" WHERE A VOC ROW RENDERS A NUMBER (decision
		// 6). The depth-first arm computes no flip, no discount, no executor
		// weight and no score, and under "absent source implies absent metric"
		// those columns must not print zeros a reader could aggregate: a `0`
		// under FLIP is a VOC row that scored zero, which is a different fact.
		fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Step, short(r.World), r.Oracle,
			voc(ladder, int64(r.Flip)), voc(ladder, r.DiscountBP), voc(ladder, r.ExecutorBP),
			costCell(r), voc(ladder, r.ScoreBPPS), dash(r.ScoreBasis),
			finishCell(r), commitCell(r), outcomeCell(r))
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
	if s.SelectionUS > 0 {
		// Reported, not charged (F8). PRD §11's matched budget is tokens +
		// runner time + oracle cost + selection cost; this harness charges the
		// third term and part of it, so no figure it produces may be called
		// budget-matched in PRD §11's sense. The correct label is
		// ORACLE-BUDGET-MATCHED, and this line is the measurement that makes
		// the omission checkable rather than assumed.
		fmt.Fprintf(w, "  selection:  %d µs of metalevel time, reported and NOT charged (PRD §11's selection-cost term)\n", s.SelectionUS)
	}
	if s.RankingIncomplete {
		fmt.Fprintln(w, "  ranking:    a passing candidate is missing a receipt a ranking key reads; the ORDER is not monotone under withholding (only the pass set is)")
	}
	if s.Violation != "" {
		fmt.Fprintf(w, "  VIOLATION:  %s\n", s.Violation)
	}
	writeWaste(w, s.Waste)
	writeBound(w, s.Bound)
}

// selectorText names the rule that ordered this race's purchases, and at
// k > 1 it names what the depth-first arm actually did instead.
//
// Decision 7 is not a footnote: a world's rung k+1 needs rung k's result, so
// a strictly depth-first arm has exactly one dispatchable purchase at a time.
// At k > 1 it fills the batch from the next k worlds — money committed to
// world 3's rung while world 1's next rung is pending, which is the
// truncation behaviour the arm exists to avoid. Results at different k are
// never pooled, so the rendering says which one produced this trace.
func selectorText(s *explainSchedule) string {
	switch s.Selector {
	case schedule.SelectorNameLadder:
		if s.Parallel > 1 {
			return fmt.Sprintf("ladder (depth-first PRIORITY FILL (k=%d) — not pure depth-first; do not pool with k=1 results)", s.Parallel)
		}
		return "ladder (depth-first, world order recorded)"
	case schedule.SelectorNameVOC:
		return "voc (value of computation: flip x discount x executor / predicted cost)"
	case schedule.SelectorNameVOC2:
		return "voc2 (value of computation; under scarcity the denominator is the cost to FINISH the world and the pool is reserved to a commit set)"
	default:
		return dash(s.Selector)
	}
}

// regimeText renders M2b.2 decision 1's gate for the whole race: which rule
// actually apportioned the pool, and on how many steps the pool could not
// finish every alive world.
//
// It is also the FAR claim's precondition made readable. M2b decision 4's
// restored claim holds "whenever the budget can pay for every hard gate of
// every live world", which is exactly ¬scarce — so a race whose every step
// says `not scarce` carries that claim with equality, from its own ledger,
// rather than from a sentence in a design document.
func regimeText(s *explainSchedule) string {
	if s.Selector == schedule.SelectorNameLadder {
		// THE ARM ANSWERS BEFORE THE ERA DOES. A depth-first ladder computes
		// no scarcity test in either era, so dating its binary would be
		// answering a question nobody asked with a fact nobody can check.
		return "— (the ladder arm computes no scarcity test and commits to nothing)"
	}
	if s.noRegime {
		// Absent is absent. A pre-M2b.2 binary ran no scarcity test, and
		// printing "not scarce" for it would report a measurement nobody took.
		return "unknown (pre-M2b.2 trace); no scarcity test was recorded"
	}
	if len(s.Regimes) == 0 {
		return "— (no allocation steps recorded)"
	}
	scarce, basis := 0, ""
	var last explainRegime
	for _, r := range s.Regimes {
		if r.Scarce {
			scarce++
		}
		// The last step that COMMITTED to anything, because the last scarce
		// step of a finished race is routinely the one whose commit set is
		// empty — the head world is complete and the remainder cannot finish
		// the rival — and reporting that as the race's commitment would read
		// as "it committed to nobody".
		if len(r.CommitSet) > 0 {
			last = r
		}
		if basis == "" || strings.HasPrefix(r.CommitBasis, schedule.CommitBasisUnpriced) {
			basis = r.CommitBasis
		}
	}
	if basis == "" {
		return fmt.Sprintf("— (the %s arm computes no scarcity test and commits to nothing)", dash(s.Selector))
	}
	if strings.HasPrefix(basis, schedule.CommitBasisUnpriced) {
		return fmt.Sprintf("%s — the finish cost is UNKNOWN for a kind with no local fit, so the scarcity test is undecidable and M2b's rule allocated the whole race",
			basis)
	}
	if scarce == 0 {
		return fmt.Sprintf("%s on all %d step(s): the pool could finish every alive world, so M2b's rule allocated and the FAR claim holds with equality",
			schedule.CommitBasisNotScarce, len(s.Regimes))
	}
	if len(last.CommitSet) == 0 {
		return fmt.Sprintf("%s on %d of %d step(s); the pool could finish NO alive world, so equal shares allocated (decision 3's degenerate case)",
			schedule.CommitBasisReserved, scarce, len(s.Regimes))
	}
	parts := make([]string, 0, len(last.CommitSet))
	for _, w := range last.CommitSet {
		parts = append(parts, short(w))
	}
	return fmt.Sprintf("%s on %d of %d step(s); last commit set: %d world(s) [%s], %d ms uncommitted",
		schedule.CommitBasisReserved, scarce, len(s.Regimes), len(last.CommitSet),
		strings.Join(parts, ", "), last.UncommittedMS)
}

// finishCell renders the cost to FINISH the row's world. A row that computed
// none prints an em dash, and so does a row whose finish cost is UNKNOWN,
// because 0 is not a measurement of zero milliseconds. The cell says whether
// the number was the score's DENOMINATOR or merely a measurement beside it —
// a `voc2` row under ¬scarce carries a finish cost it did not divide by, and
// letting the two look alike would misreport which rule priced the row.
func finishCell(r explainScheduleRow) string {
	if r.ScoreBasis == "" || r.FinishMS <= 0 {
		return "—"
	}
	if r.ScoreBasis == schedule.ScoreBasisFinish {
		return fmt.Sprintf("%d ms", r.FinishMS)
	}
	return fmt.Sprintf("(%d ms)", r.FinishMS)
}

// commitCell says whether the pool was reserved to FINISH this world at this
// step. It is meaningful only under the reservation: a row priced by the rung
// denominator was never in or out of a commit set, and printing "no" for it
// would be a claim about a set that was never formed.
func commitCell(r explainScheduleRow) string {
	if r.ScoreBasis != schedule.ScoreBasisFinish {
		return "—"
	}
	if r.Committed {
		return "yes"
	}
	return "no"
}

// basisText says what the pool was charged, because the two bases answer
// different questions and a comparison across them is not a comparison.
func basisText(basis string) string {
	switch basis {
	case schedule.BudgetBasisPredicted:
		return "predicted (the pinned cost table's prediction — allocation is replayable; the model's error is the calibration residual)"
	case schedule.BudgetBasisActual:
		return "actual (each receipt's measured wall_ms — honest, and NOT in the determinism tuple: two replicates can buy different things)"
	default:
		return dash(basis)
	}
}

// worldOrderText renders the control-plane order this race allocated in.
//
// An empty order is "unknown (pre-M2b1 trace)" and never digest order.
// Reporting a past race's order as digest order would be inventing evidence
// about the one field that decides, under a binding budget, who was verified
// at all.
func worldOrderText(s *explainSchedule) string {
	if len(s.WorldOrder) == 0 {
		return "unknown (pre-M2b1 trace); no control-plane world order was recorded"
	}
	parts := make([]string, 0, len(s.WorldOrder))
	for i, w := range s.WorldOrder {
		parts = append(parts, fmt.Sprintf("#%d %s", i+1, short(w)))
	}
	return fmt.Sprintf("%s  (control-plane, candidate ordinal ascending, rotation %d)",
		strings.Join(parts, ", "), s.Rotation)
}

// voc renders a VALUE-OF-COMPUTATION cell: the number for a VOC row, and an
// em dash for a ladder row, which computed no such term at all.
func voc(ladder bool, v int64) string {
	if ladder {
		return "—"
	}
	return fmt.Sprintf("%d", v)
}

// writeBound renders §5's block: the headroom an optimal prefix-respecting
// allocator had on this instance.
func writeBound(w io.Writer, b *schedule.BoundReport) {
	if b == nil {
		return
	}
	fmt.Fprintln(w, "")
	if !b.Available {
		// A refusal, not an approximation, and not a zero. No approximation is
		// reported under the name of an exact bound.
		fmt.Fprintf(w, "  allocation bound: not computed — %s\n", dash(b.Refused))
		return
	}
	fmt.Fprintf(w, "  allocation bound: minspend %d ms of %d spent (%s headroom) over %d prefix-closed allocations\n",
		b.MinSpendMS, b.TotalMS, pctText(b.SavingBP), b.Subsets)
	fmt.Fprintf(w, "    target d* = %s%s; reachable at %s: %v\n",
		b.Decision, subjectText(b.Subject), budgetText(b.BudgetMS), b.Reachable)
	for _, p := range b.Prefixes {
		fmt.Fprintf(w, "    %s  %d of %d rungs, %6d ms  [%s]\n",
			short(p.World), p.Rungs, p.Bought, p.CostMS, strings.Join(p.Oracles, " "))
	}
	if b.AlwaysMS > 0 {
		fmt.Fprintf(w, "    %d ms held constant (unscheduled in both arms: no allocator can withhold it)\n", b.AlwaysMS)
	}
	for _, c := range b.Caveats {
		fmt.Fprintf(w, "    caveat: %s\n", c)
	}
}

func subjectText(subject string) string {
	if subject == "" {
		return ""
	}
	return " " + short(subject)
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
