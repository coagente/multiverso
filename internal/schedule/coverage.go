package schedule

// M2d.1 — COVERAGE: did the rule under test ever actually fire?
//
// DERIVED, NEVER RECORDED (decision 8). Everything in this file is a pure,
// total function of a recorded Trace. `schedule.finished` gains no coverage
// field, because a recorded aggregate would be a number computed by the
// binary being measured, and because a definition that improves must not
// invalidate a race that already happened. The same discipline M1e decision
// 21 bought `mvo explain` and M2b decision 18 bought evidence waste.
//
// The third consequence is the one worth the file: a ledger written months
// ago can be priced retroactively, because `commit_basis` and `score_basis`
// have been on the wire since M2b.2. The instrument can go back and price
// its own past claims.
//
// AND ABSENT IS ABSENT. A pre-M2b.2 trace records neither field, so its
// coverage is `unknown (pre-M2b.2 trace)` — never 0, never 100 %. A LADDER
// trace computes no scarcity test and no score at all, so its coverage is
// `—`, answered from the ARM rather than from the era. Reporting either as
// 0 % would be inventing a measurement, which is the failure this whole
// block exists to remove.
//
// THE PREDICATE IS READ OFF THE LEDGER, NEVER ASKED OF THE ARM. This is
// reservesPerWorld's discipline held one level up: a rule that could
// self-report its own coverage would be a rule grading its own homework.

import (
	"fmt"
	"sort"
	"strings"
)

// BLOCKER B3 — CONSULTED IS NOT EXERCISED, AND THE HEADLINE IS EXERCISED.
//
// The first version of this file counted a step on which the rule was
// CONSULTED — `commit_basis` becomes `reserved` the moment `scarce` is true,
// which is BEFORE the commit set is built — so a step whose allowance is
// M2b's equal share EXACTLY, whose admissibility is M2b's exactly and whose
// order is M2b's exactly still counted as exercised. A hostile reviewer
// measured the consequence: the published 68 % was inflated.
//
// So the figure is SPLIT and both halves are printed:
//
//	consulted(step) ⟺ ¬inert(arm, step)          -- the rule's code path ran
//	exercised(step) ⟺ depended(arm, step)        -- the step's ALLOCATION
//	                                                would have differed under
//	                                                the declared baseline
//
// `exercised ⊆ consulted` by construction, and `exercised` is the number a
// caption may quote. Every term of `depended` is a difference the baseline
// rule provably does not produce, read off recorded fields alone.
//
// BLOCKER B4's SECOND HALF — W2 IS RETIRED AS A WITNESS. `score_basis ==
// "finish"` is set by `scoreVOC2`, which `selectorVOC2.Rank` calls exactly
// when `pl.scarce` — and `commit_basis == "reserved"` is set by exactly the
// same test in exactly the same plan. So W2 was DEFINITIONALLY the consulted
// set (measured identical at 27/27, 21/21, 25/25), and reporting it beside
// the numerator made two facts look like four. It is still COUNTED, because
// counting it is how the identity that retires it stays falsifiable: a run
// where the two sets disagree is a run where this comment stopped being true,
// and the report says so rather than quietly re-deriving.
const (
	// WitnessFinishDenominator (W2) — M2b.2 decision 2: the denominator
	// moved. RETIRED AS A WITNESS: it is the CONSULTED predicate itself. Kept
	// as a name so the identity can be asserted rather than assumed.
	WitnessFinishDenominator = "W2"
	// WitnessReservationReserved (W3) — decision 3: the reservation reserved.
	// |C| >= 1. With C empty the allowance IS budget.share, i.e. M2b decision
	// 8 exactly, which is why `commit_basis: reserved` alone is not this
	// witness — and why it was never enough to make a step EXERCISED.
	WitnessReservationReserved = "W3"
	// WitnessPassWithheld (W4) — decision 4: a pass outcome was withheld, so
	// this row's value_bp is not the value_bp `voc` would have computed.
	WitnessPassWithheld = "W4"
	// WitnessHardGateLapsed (W5) — decision 5: the hard-gate override lapsed.
	// IMPOSSIBLE under `voc`, where admissible = flip ∨ hard_gate.
	WitnessHardGateLapsed = "W5"
	// WitnessHeadMoved (W6) — M2d.1 blocker B3's ORDER term, and the reason
	// `|commit_set| >= 1` alone is not the whole of `depended`. It is the
	// per-step re-evaluation of the BASELINE rule's own ranking: with every
	// rung priced (which scarcity guarantees) M2b's score is
	// `value_bp × 1000 / max(cost_ms, 1)` over the SAME recorded rows, and
	// `sortRanked` puts the strictly higher score first among priced rows
	// before any tie-break is reached. So if some row the rule ranked BELOW
	// the head strictly outscores the head under M2b's denominator, the two
	// rules disagree about what to buy first — no tie-break, no world order,
	// no guess.
	WitnessHeadMoved = "W6"
)

// witnessName renders the sentence a reader reads beside the count.
func witnessName(id string) string {
	switch id {
	case WitnessFinishDenominator:
		return "finish denominator"
	case WitnessReservationReserved:
		return "reservation reserved"
	case WitnessPassWithheld:
		return "pass outcome withheld"
	case WitnessHardGateLapsed:
		return "hard-gate override lapsed"
	case WitnessHeadMoved:
		return "head of the queue moved"
	}
	return id
}

// The three answers that are NOT a percentage. They are strings rather than
// a bool because a reader has to be told WHICH kind of absence this is.
const (
	// CoverageUnknownPreM2b2 is a trace from a binary that predates the
	// finishing rule: it recorded no commit_basis and no score_basis, so
	// nothing here can be computed from it.
	CoverageUnknownPreM2b2 = "unknown (pre-M2b.2 trace)"
	// CoverageNotApplicable is the LADDER arm: it runs no scarcity test and
	// computes no score, so there is no rule of this kind to have fired.
	CoverageNotApplicable = "— (computes no scarcity test)"
	// CoverageNoTrace is a race that recorded no allocation trace at all.
	CoverageNoTrace = "— (no allocation trace recorded)"
)

// Witness is one mechanism's per-step tally.
type Witness struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Steps int    `json:"steps"`
	Total int    `json:"total"`
}

// CoverageReport is what fraction of a race's (or a set of races') recorded
// steps exercised the rule under test, per mechanism.
//
// It is printed ALWAYS, INCLUDING AT 100 % (decision 10). A number that
// appears only when it is bad is a number nobody learns to read.
type CoverageReport struct {
	// Rule is the selector the trace recorded — the rule under test. Baseline
	// is what that rule provably collapses to when it is inert, which is the
	// half of the claim that makes "0 %" mean something.
	Rule     string `json:"rule"`
	Baseline string `json:"baseline"`
	// Known is false for a pre-M2b.2 trace; Applicable is false for a ladder
	// trace and for a trace that recorded nothing. Absence is reported as
	// absence and NEVER as zero.
	Known      bool `json:"known"`
	Applicable bool `json:"applicable"`

	Steps int `json:"steps"`
	// Consulted is B3's demoted figure: the steps on which the rule's OWN
	// code path ran. It is NOT the headline and it may not be captioned as
	// coverage — `commit_basis` becomes `reserved` the moment scarcity is
	// true, before the commit set is built, so a consulted step can be one
	// whose allowance, admissibility and order are M2b's exactly.
	Consulted int `json:"consulted"`
	// Exercised is THE HEADLINE: the steps whose allocation DEPENDED on the
	// rule — some recorded quantity the baseline provably does not produce.
	// `Exercised <= Consulted` always.
	Exercised int `json:"exercised"`
	// Races, RacesConsulted and RacesExercised are the per-race figures. Both
	// units are printed because the REFUSAL is per comparison and the
	// INTERPRETATION is per step: a race with ten steps of which one depended
	// on the rule is 10 % exercised, and calling it "one exercised race"
	// would report nine tenths of an allocation as though the rule had
	// produced it.
	Races          int `json:"races"`
	RacesConsulted int `json:"races_consulted"`
	RacesExercised int `json:"races_exercised"`
	// Unknown and NotApplicable count merged races whose coverage could not be
	// computed, so an aggregate never silently drops them.
	Unknown       int `json:"unknown_races"`
	NotApplicable int `json:"not_applicable_races"`
	// VacuousParts is BLOCKER B4 made structural: how many of the reports
	// this one was merged from were THEMSELVES vacuous. A pooled numerator
	// can be rescued by a single exercised step somewhere in the pool — a
	// probe merging 99 vacuous races with one exercised step printed
	// `1 of 199 steps (0%)` and `vacuous=false` — so the parts are carried
	// into the whole and printed there. The REFUSAL is taken per cell by the
	// caller and never off this pooled figure.
	VacuousParts int `json:"vacuous_parts"`

	Witnesses []Witness `json:"witnesses"`
	// CommitSetSteps is |C| >= 1 counted separately from `commit_basis:
	// reserved`, because a scarce step with |C| = 0 records `reserved` and
	// allocates by equal shares. The wire vocabulary is not changed here, so
	// coverage reports the two apart and no reader can mistake one for the
	// other (M2d.1 §9.7).
	CommitSetSteps int `json:"commit_set_steps"`
	// FinishBasisSteps is the retired W2's count: steps carrying at least one
	// row with `score_basis: finish`. It is reported ONLY as the assertion
	// that retires it — under `voc2` it is the CONSULTED set by construction.
	FinishBasisSteps int `json:"finish_basis_steps"`
	// W2Breaks counts steps where that identity did NOT hold. It is 0 on
	// every trace this binary writes, and a nonzero value is the report
	// saying that the construction changed underneath the claim rather than
	// silently re-deriving a witness from it.
	W2Breaks int `json:"w2_identity_breaks"`
	// BudgetBoundRaces counts races whose budget actually bound: a race that
	// spends past its bound and stops S-empty was not budget-matched in the
	// sense its caption claims, whichever rule allocated it.
	BudgetBoundRaces int `json:"budget_bound_races"`
	// Regimes is every distinct commit_basis observed, sorted. It is what the
	// refusal quotes: "commit_basis was unpriced-fallback(...) on every step".
	Regimes []string `json:"regimes"`
	// CostRegime is CostRegimeWarm or CostRegimeCold, derived from the
	// RECORDED cost table rather than from the flag that was passed.
	CostRegime string `json:"cost_regime"`
}

// Cost regimes (decision 11). Derived from the recorded
// `schedule.started.cost_table`: a row with basis `fit` is warm, one with
// `declared-rank` is cold. No wire field is needed and none is added.
const (
	CostRegimeWarm    = "warm"
	CostRegimeCold    = "cold"
	CostRegimeUnknown = "unknown"
)

// baselineOf names the arm a rule collapses to when it is inert (decision 5).
// A rule with no row here has no declared baseline, and coverage refuses to
// guess one: any future rule ships with its own row or it does not ship.
func baselineOf(rule string) string {
	switch rule {
	case SelectorNameVOC2:
		return SelectorNameVOC
	case SelectorNameVOC, SelectorNameLadder:
		return "the M1 exhaustive ladder"
	}
	return ""
}

// PreM2b2 reports whether this trace was written by a binary that predates
// the finishing rule.
//
// IT CANNOT BE READ OFF `adaptive_rule`. That field is the BINARY's default
// rule; an absent value normalizes to "voc" EXACTLY (M2b.2 decision 8); and
// M2b.2 ships `voc` as the default, so "voc" is what a pre-M2b.2 ledger AND a
// race run this morning both record. A reader that dated the binary from it
// would stamp "pre-M2b.2 trace" on current races — inventing evidence in
// exactly the direction M2b.1 decision 6 forbids, in the sentence written to
// prevent that.
//
// What separates the eras is the finishing rule's OWN VOCABULARY: a binary
// that has the rule records a `commit_basis` on every step of a `voc2` race
// and a `score_basis` on every VOC row of any race, and a binary that does
// not records neither. A LADDER race carries neither in either era, so the
// ladder is answered from the ARM before this test is reached.
//
// It lives here rather than in `cmd/mvo` because a second copy of an era test
// is how the two copies eventually disagree about which era a ledger is from
// (decision 8).
func PreM2b2(t Trace) bool {
	for _, s := range t.Steps {
		if s.CommitBasis != "" {
			return false
		}
		for _, c := range s.Considered {
			if c.ScoreBasis != "" {
				return false
			}
		}
	}
	return true
}

// InertVOC2 is decision 5's declared inertness predicate for M2b.2's rule,
// over RECORDED fields alone: the revision collapses to M2b's `voc` exactly
// when the step was not apportioned by the reservation. It is pinned by
// M2b.2's own `voc2 ≡ voc under ¬scarce` property test rather than believed.
func InertVOC2(s Step) bool {
	switch {
	case s.CommitBasis == "":
		return true
	case s.CommitBasis == CommitBasisNotScarce:
		return true
	case strings.HasPrefix(s.CommitBasis, CommitBasisUnpriced):
		return true
	}
	return false
}

// DependedVOC2 is BLOCKER B3's headline predicate: did this step's allocation
// actually DIFFER from what `voc` would have produced?
//
// Consultation is necessary and nowhere near sufficient. Under scarcity with
// `C = ∅` the allowance is `equalShare(pool, |frontier|)` — M2b decision 8
// reached through the same arithmetic — decision 5's override does not lapse
// because `reserving()` is false, and decision 4 withholds nothing because
// every world is completable at the equal share. Such a step is `reserved` on
// the wire and is M2b's step in every observable respect, which is exactly
// what the reviewer measured and exactly what inflated the number.
//
// So `depended` is the disjunction of the FOUR differences the baseline
// provably cannot produce, each of them a lookup on a recorded field:
//
//	W3  |commit_set| >= 1   the allowance is `reserve(...)`, not the equal share
//	W4  pass_withheld       value_bp is the fail-closed bracket's, not voc's
//	W5  hard_gate ∧ ¬admissible   impossible under voc (admissible = flip ∨ hard_gate)
//	W6  the head of the queue moved under M2b's own denominator
//
// It is deliberately CONSERVATIVE — it under-counts rather than over-counts —
// because the refusal this figure gates fires on ZERO, so an under-count
// refuses louder and an over-count is what B3 was.
func DependedVOC2(s Step) bool {
	if InertVOC2(s) {
		return false
	}
	if len(s.CommitSet) > 0 {
		return true
	}
	for _, c := range s.Considered {
		if c.PassWithheld {
			return true
		}
		if c.HardGate && !c.Admissible {
			return true
		}
	}
	return HeadMovedByFinish(s)
}

// HeadMovedByFinish is W6: the per-step re-evaluation of the BASELINE rule's
// order, over the rows the rule itself recorded.
//
// M2b's score for a priced row is `value_bp × 1000 / max(cost_ms, 1)`, and
// `sortRanked` compares that field FIRST among priced rows — so a strictly
// larger baseline score is a strictly earlier position under `voc`, before
// any tie-break is consulted. The rows are recorded IN THE RULE'S OWN RANK
// ORDER, so if any row below the head strictly outscores the head under M2b's
// denominator, the two rules disagree about which purchase to fill the batch
// with first.
//
// Three refusals keep it sound rather than merely cheap:
//
//   - it answers false unless EVERY row was priced by the finish denominator
//     (`score_basis: finish`, `score_rank == 0`), which is precisely the
//     scarce regime — outside it the recorded order IS voc's order;
//   - it answers false when any row withheld a pass outcome, because then
//     `value_bp` is not the number `voc` would have computed and the
//     comparison would be against a baseline nobody ran. That case is already
//     `depended` by W4, so nothing is lost;
//   - it compares only against the HEAD, and only STRICTLY, so no tie-break —
//     `cost_ms`, the control-plane world order, the oracle name — is ever
//     guessed at. `OrderIndex` is not on the wire and this function never
//     needs it.
func HeadMovedByFinish(s Step) bool {
	rows := s.Considered
	if len(rows) < 2 {
		return false
	}
	for _, c := range rows {
		if c.ScoreBasis != ScoreBasisFinish || c.ScoreRank != 0 || c.PassWithheld {
			return false
		}
	}
	head := baselineRungScore(rows[0])
	for _, c := range rows[1:] {
		if baselineRungScore(c) > head {
			return true
		}
	}
	return false
}

// baselineRungScore is M2b's score_bpps recomputed from the recorded row:
// value_bp x 1000 / max(cost_ms, 1). The divisor floor is `Cost.Divisor`'s,
// which exists so the score stays total, and it is repeated here rather than
// borrowed because this reads a RECORDED row and not a live Cost.
func baselineRungScore(c Considered) int64 {
	d := c.CostMS
	if d < 1 {
		d = 1
	}
	return c.ValueBP * 1000 / d
}

// InertBudgeted is decision 5's predicate for `voc` and for `ladder`, both of
// which collapse to the M1 exhaustive ladder when the budget never binds.
//
// This is the row that catches the vacuity nobody had named: A BUDGETED
// COMPARISON AT A BUDGET THAT NEVER BINDS IS A COMPARISON OF TWO ARMS THAT
// ARE BOTH THE EXHAUSTIVE LADDER. A cold eval cell measurably was one — 2 164
// ms spent against a 1 500 ms bound, stopping S-empty — and nothing in the
// harness said so.
//
// `last` and `stop` carry the race-level half of the predicate: a race that
// stopped S-budget is a race the budget bound, and the final step is where
// that happened.
func InertBudgeted(s Step, last bool, stop string) bool {
	for _, c := range s.Considered {
		if c.Admissible && !c.Affordable {
			return false
		}
	}
	return !(last && stop == StopBudget)
}

// DependedBudgeted is B3's headline predicate for `voc` and for `ladder`.
//
// It keeps only the STRICT half of InertBudgeted: a row that was admissible
// and could not be afforded is a purchase the M1 exhaustive ladder would have
// made and this arm did not, which is a difference in the allocation and not
// merely in the code path. The race-level half (`last && stop == S-budget`)
// is dropped here on purpose: `stopClause` returns `S-budget` only when some
// row was admissible and unaffordable, so it adds no step the strict clause
// misses, and a race-level fact is not a per-step dependence.
//
// The two figures therefore coincide for these arms in practice, and that is
// the honest answer rather than a coincidence to paper over: unlike `voc2`'s
// `reserved`, this predicate never fired on the mere fact that the rule was
// asked.
func DependedBudgeted(s Step) bool {
	for _, c := range s.Considered {
		if c.Admissible && !c.Affordable {
			return true
		}
	}
	return false
}

// CostRegimeOf derives decision 11's regime from the RECORDED cost table.
// An empty table is `unknown`, never `cold`: a race that recorded no snapshot
// is not a race that recorded an unfitted one.
func CostRegimeOf(t Trace) string {
	if len(t.Started.CostTable) == 0 {
		return CostRegimeUnknown
	}
	for _, row := range t.Started.CostTable {
		if row.Basis != CostBasisFit {
			return CostRegimeCold
		}
	}
	return CostRegimeWarm
}

// Coverage prices ONE recorded trace. It is pure and total: an empty trace, a
// single-step trace, a ladder trace and a pre-M2b.2 trace all produce a
// report, and none of them produces a fabricated percentage.
func Coverage(t Trace) CoverageReport {
	rule := t.Started.Selector
	if rule == "" {
		rule = SelectorNameVOC
	}
	rep := CoverageReport{
		Rule:       rule,
		Baseline:   baselineOf(rule),
		Races:      1,
		CostRegime: CostRegimeOf(t),
	}
	// A race with no trace at all: absent source implies absent metric.
	if t.Empty() {
		rep.NotApplicable = 1
		rep.CostRegime = CostRegimeUnknown
		return rep
	}
	// THE ARM IS ANSWERED BEFORE THE ERA. A ladder trace carries neither
	// commit_basis nor score_basis in either era, so asking PreM2b2 about it
	// would date a current binary's race to a binary that never existed.
	if rule == SelectorNameLadder {
		rep.NotApplicable = 1
		return rep
	}
	if PreM2b2(t) {
		rep.Unknown = 1
		return rep
	}
	rep.Known, rep.Applicable = true, true

	stop := ""
	if t.HasFinished {
		stop = t.Finished.Stop
	}
	w3, w4, w5, w6 := 0, 0, 0, 0
	regimes := map[string]bool{}
	for i, s := range t.Steps {
		rep.Steps++
		if s.CommitBasis != "" {
			regimes[s.CommitBasis] = true
		}
		last := i == len(t.Steps)-1
		// B3: TWO QUESTIONS, ASKED SEPARATELY. `consulted` is the old
		// predicate, demoted to what it always measured; `depended` is the
		// headline and requires a recorded difference from the baseline.
		consulted, depended := false, false
		if rule == SelectorNameVOC2 {
			consulted, depended = !InertVOC2(s), DependedVOC2(s)
		} else {
			consulted, depended = !InertBudgeted(s, last, stop), DependedBudgeted(s)
		}
		if consulted {
			rep.Consulted++
		}
		if depended {
			rep.Exercised++
		}
		if len(s.CommitSet) > 0 {
			rep.CommitSetSteps++
			w3++
		}
		finishRow := false
		for _, c := range s.Considered {
			if c.ScoreBasis == ScoreBasisFinish {
				finishRow = true
				break
			}
		}
		if finishRow {
			rep.FinishBasisSteps++
		}
		// THE IDENTITY THAT RETIRES W2, asserted rather than assumed, and
		// only for the rule it is a claim about.
		if rule == SelectorNameVOC2 && finishRow != consulted {
			rep.W2Breaks++
		}
		for _, c := range s.Considered {
			if c.PassWithheld {
				w4++
				break
			}
		}
		for _, c := range s.Considered {
			if c.HardGate && !c.Admissible {
				w5++
				break
			}
		}
		if HeadMovedByFinish(s) {
			w6++
		}
	}
	if rep.Consulted > 0 {
		rep.RacesConsulted = 1
	}
	if rep.Exercised > 0 {
		rep.RacesExercised = 1
	}
	if stop == StopBudget {
		rep.BudgetBoundRaces = 1
	} else {
		for _, s := range t.Steps {
			for _, c := range s.Considered {
				if c.Admissible && !c.Affordable {
					rep.BudgetBoundRaces = 1
				}
			}
		}
	}
	// THE WITNESS LIST IS THE DEPENDENCE TERMS AND NOTHING ELSE. W2 is gone
	// from it: it was the consulted set under another name, and four
	// witnesses that were two facts is how a reader is talked into believing
	// a number four times.
	rep.Witnesses = []Witness{
		{ID: WitnessReservationReserved, Name: witnessName(WitnessReservationReserved), Steps: w3, Total: rep.Steps},
		{ID: WitnessPassWithheld, Name: witnessName(WitnessPassWithheld), Steps: w4, Total: rep.Steps},
		{ID: WitnessHardGateLapsed, Name: witnessName(WitnessHardGateLapsed), Steps: w5, Total: rep.Steps},
		{ID: WitnessHeadMoved, Name: witnessName(WitnessHeadMoved), Steps: w6, Total: rep.Steps},
	}
	rep.Regimes = sortedKeys(regimes)
	return rep
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MergeCoverage aggregates the per-race reports of one comparison — every
// replicate of one arm, or every vector of a corpus — WITHOUT pooling the
// absences into the percentage. A merged report whose races were all
// pre-M2b.2 is still `unknown`, never 0 %.
//
// Rules are not merged across arms: two arms' coverage are two numbers, and
// averaging them would be a number about neither.
//
// THE MERGED REPORT IS NOT WHERE THE REFUSAL IS TAKEN (blocker B4). Pooling
// is exactly the operation that lets one exercised step rescue a hundred
// vacuous ones, so the merge carries `VacuousParts` forward and the caller
// refuses per cell. A refusal read off this object's own numerator is the
// defect, not the check.
func MergeCoverage(reports []CoverageReport) CoverageReport {
	out := CoverageReport{}
	regimes := map[string]bool{}
	costRegimes := map[string]bool{}
	witness := map[string]*Witness{}
	var order []string
	for _, r := range reports {
		if out.Rule == "" {
			out.Rule, out.Baseline = r.Rule, r.Baseline
		}
		out.Races += r.Races
		out.RacesConsulted += r.RacesConsulted
		out.RacesExercised += r.RacesExercised
		out.Steps += r.Steps
		out.Consulted += r.Consulted
		out.Exercised += r.Exercised
		out.Unknown += r.Unknown
		out.NotApplicable += r.NotApplicable
		out.CommitSetSteps += r.CommitSetSteps
		out.FinishBasisSteps += r.FinishBasisSteps
		out.W2Breaks += r.W2Breaks
		out.BudgetBoundRaces += r.BudgetBoundRaces
		out.Known = out.Known || r.Known
		out.Applicable = out.Applicable || r.Applicable
		// B4: a part that was vacuous stays counted in the whole, whether it
		// was vacuous in itself or carried its own vacuous parts.
		out.VacuousParts += r.VacuousParts
		if r.Vacuous() {
			out.VacuousParts++
		}
		for _, g := range r.Regimes {
			regimes[g] = true
		}
		if r.CostRegime != "" {
			costRegimes[r.CostRegime] = true
		}
		for _, w := range r.Witnesses {
			acc, ok := witness[w.ID]
			if !ok {
				cp := Witness{ID: w.ID, Name: w.Name}
				witness[w.ID] = &cp
				order = append(order, w.ID)
				acc = &cp
			}
			acc.Steps += w.Steps
			acc.Total += w.Total
		}
	}
	for _, id := range order {
		out.Witnesses = append(out.Witnesses, *witness[id])
	}
	out.Regimes = sortedKeys(regimes)
	switch {
	case len(costRegimes) == 1:
		out.CostRegime = sortedKeys(costRegimes)[0]
	case len(costRegimes) > 1:
		// A set that mixes regimes may not be reported as either one
		// (decision 11): a warm cell and a cold cell are two experiments.
		out.CostRegime = "mixed(" + strings.Join(sortedKeys(costRegimes), ",") + ")"
	default:
		out.CostRegime = CostRegimeUnknown
	}
	return out
}

// Vacuous is decision 6's first row: the rule under test provably never fired
// on a run where it COULD have been computed. It is deliberately false for an
// unknown or an inapplicable report — refusing to report a number you could
// not compute is not the same act as refusing to report a rule that did not
// run, and conflating them would make the refusal fire on every ladder race
// in the repository, which is F-9's rubber stamp.
// It keys on EXERCISED and never on CONSULTED (blocker B3): a run in which
// the rule was asked on every step and depended on by none measured nothing,
// whatever `commit_basis` says.
func (r CoverageReport) Vacuous() bool {
	return r.Applicable && r.Known && r.Steps > 0 && r.Exercised == 0
}

// AnyVacuous is Vacuous widened over the parts a merged report was pooled
// from. It is what a caller asks of an AGGREGATE; `Vacuous` is what it asks
// of one cell.
func (r CoverageReport) AnyVacuous() bool { return r.Vacuous() || r.VacuousParts > 0 }

// Percent is exercised steps over steps, floored. It is meaningful only when
// Known && Applicable, and it is NOT what should be printed — `percentText`
// is, because a floored 0 % over a nonzero numerator is the misreport blocker
// B4 named (`1 of 199 steps (0%)`).
func (r CoverageReport) Percent() int {
	if r.Steps == 0 {
		return 0
	}
	return r.Exercised * 100 / r.Steps
}

// percentText renders a rate WITHOUT ever printing `0%` for a nonzero
// numerator. B4: the probe that merged 99 vacuous races with one exercised
// step printed `1 of 199 steps (0%)`, and a reader who saw the parenthesis
// and not the fraction read a zero that was not measured. Below one per cent
// is `<1%`, and `0%` now means exactly what it says.
func percentText(n, d int) string {
	if d <= 0 {
		return "—"
	}
	p := n * 100 / d
	if p == 0 && n > 0 {
		return "<1%"
	}
	return fmt.Sprintf("%d%%", p)
}

// Summary is the one-line answer for the HEADLINE figure, and it is a STRING
// because three of its possible values are not numbers.
func (r CoverageReport) Summary() string {
	if s, ok := r.absence(); ok {
		return s
	}
	return fmt.Sprintf("%d of %d steps (%s)", r.Exercised, r.Steps, percentText(r.Exercised, r.Steps))
}

// ConsultedSummary is B3's demoted figure, printed beside the headline so the
// gap between "the rule ran" and "the rule mattered" is visible rather than
// collapsed.
func (r CoverageReport) ConsultedSummary() string {
	if s, ok := r.absence(); ok {
		return s
	}
	return fmt.Sprintf("%d of %d steps (%s)", r.Consulted, r.Steps, percentText(r.Consulted, r.Steps))
}

// absence answers WHICH kind of absence this is, or reports that there is
// none. Absent source implies absent metric, and the three absences are three
// different facts.
func (r CoverageReport) absence() (string, bool) {
	switch {
	case !r.Applicable && r.NotApplicable > 0 && r.Steps == 0:
		if r.Rule == SelectorNameLadder {
			return CoverageNotApplicable, true
		}
		return CoverageNoTrace, true
	case !r.Known && r.Unknown > 0 && r.Steps == 0:
		return CoverageUnknownPreM2b2, true
	case r.Steps == 0:
		return CoverageNoTrace, true
	}
	return "", false
}

// Lines is decision 10's block, printed ALWAYS — including at 100 % — beside
// ORACLE-BUDGET-MATCHED and SYNTHETIC-CANDIDATES, which are there for the
// same reason.
func (r CoverageReport) Lines() []string {
	base := r.Baseline
	if base == "" {
		base = "undeclared"
	}
	out := []string{
		fmt.Sprintf("COVERAGE — the rule under test: %s, baseline %s", r.Rule, base),
		// B3: THE HEADLINE IS DEPENDENCE. The consulted line is printed
		// directly beneath it, always, because the gap between the two is the
		// whole of what the first version of this block got wrong.
		fmt.Sprintf("  steps EXERCISED (allocation depended on the rule)  %s", r.Summary()),
		fmt.Sprintf("  steps consulted (the rule's own regime ran)        %s", r.ConsultedSummary()),
	}
	if r.Races > 0 {
		out = append(out, fmt.Sprintf("  races exercised     %d of %d      races consulted %d of %d",
			r.RacesExercised, r.Races, r.RacesConsulted, r.Races))
	}
	if r.VacuousParts > 0 {
		out = append(out, fmt.Sprintf(
			"  VACUOUS PARTS: %d of the reports pooled into this line exercised the rule on NO step "+
				"— a pooled numerator hides that, so it is printed here (B4)", r.VacuousParts))
	}
	if r.Unknown > 0 {
		out = append(out, fmt.Sprintf("  races with no computable coverage: %d (%s)", r.Unknown, CoverageUnknownPreM2b2))
	}
	if r.NotApplicable > 0 {
		out = append(out, fmt.Sprintf("  races with no rule of this kind:   %d (%s)", r.NotApplicable, CoverageNotApplicable))
	}
	for _, w := range r.Witnesses {
		line := fmt.Sprintf("  %s %-26s %d of %d", w.ID, w.Name, w.Steps, w.Total)
		if w.ID == WitnessReservationReserved && w.Total > w.Steps {
			line += fmt.Sprintf("   (|C| = 0 on %d: equal shares, M2b decision 8)", w.Total-w.Steps)
		}
		out = append(out, line)
	}
	// W2 IS REPORTED AS RETIRED, not silently dropped: a witness that
	// vanishes between two versions of a report reads as a witness that
	// stopped firing.
	if r.Rule == SelectorNameVOC2 && r.Steps > 0 {
		line := fmt.Sprintf("  %s %-26s %d of %d   RETIRED AS A WITNESS: score_basis=finish is set by "+
			"exactly the scarcity test that sets commit_basis=reserved, so this IS the consulted set",
			WitnessFinishDenominator, witnessName(WitnessFinishDenominator), r.FinishBasisSteps, r.Steps)
		out = append(out, line)
		if r.W2Breaks > 0 {
			out = append(out, fmt.Sprintf(
				"  W2 IDENTITY BROKEN on %d step(s): score_basis=finish and commit_basis=reserved "+
					"named different sets, so the construction above no longer holds and this report's "+
					"consulted figure must be re-derived before it is quoted", r.W2Breaks))
		}
	}
	out = append(out, fmt.Sprintf("  cost table: %s   budget bound: %d of %d races",
		strings.ToUpper(r.CostRegime), r.BudgetBoundRaces, r.Races))
	if len(r.Regimes) > 0 {
		out = append(out, "  commit_basis observed: "+strings.Join(r.Regimes, " · "))
	}
	return out
}

// VacuityReason is decision 7's NAMED NON-VERDICT: the sentence printed
// instead of a comparison, quoting the recorded field that proves the rule
// never ran. It names WHAT was observed rather than asserting that nothing
// happened, because an unrun check is not a passed check and a reader has to
// be able to check the claim against the ledger.
func (r CoverageReport) VacuityReason() string {
	regime := "no commit_basis was recorded on any step"
	if len(r.Regimes) > 0 {
		regime = "commit_basis was " + strings.Join(r.Regimes, " / ") + " on every step"
	}
	remedy := "Warm the workspace (--warmup auto) or drop the claim."
	if r.Rule != SelectorNameVOC2 {
		remedy = "Lower the budget until it binds, warm the workspace (--warmup auto), or drop the claim."
	}
	base := r.Baseline
	if base == "" {
		base = "its undeclared baseline"
	}
	// B3: THE TWO SENTENCES ARE DIFFERENT REFUSALS AND MUST NOT BE
	// INTERCHANGED. "the rule never ran" is a claim about the code path;
	// "the rule ran and changed nothing it allocated" is a claim about the
	// allocation, and only the second is what a `reserved`-on-every-step,
	// `|C| = 0`-on-every-step run actually is.
	if r.Consulted > 0 {
		return fmt.Sprintf(
			"the rule under test ran on %d of %d step(s) and CHANGED NOTHING IT ALLOCATED on any of "+
				"them: no step recorded a commit set, a withheld pass outcome, a lapsed hard-gate "+
				"override or a moved queue head, so every allowance and every order was %s's. "+
				"--selector=%s allocated exactly as %s would have, and this is a comparison of %s "+
				"against %s. %s",
			r.Consulted, r.Steps, base, r.Rule, base, base, base, remedy)
	}
	return fmt.Sprintf(
		"the rule under test never fired: %s of every replicate, so --selector=%s ran %s "+
			"and this is a comparison of %s against %s. %s",
		regime, r.Rule, base, base, base, remedy)
}

// VacuityBanner is the whole refusal, ready to print. It quotes BOTH figures,
// because "consulted 27 of 27, exercised 0 of 27" is a different and more
// alarming sentence than "0 of 27" alone.
func (r CoverageReport) VacuityBanner() []string {
	return []string{
		fmt.Sprintf("VACUOUS (exercised %d of %d steps, %d of %d replicates; consulted %d of %d steps): NO VERDICT",
			r.Exercised, r.Steps, r.RacesExercised, r.Races, r.Consulted, r.Steps),
		"  " + r.VacuityReason(),
	}
}

// PurchaseOrder is decision 6's divergence key: the sequence of (candidate
// ordinal, oracle instance) over the BOUGHT rows, in step order.
//
// It is keyed on the ORDINAL and never on the world digest, for F2's reason:
// a world binds created_at, the agent RunCost and a transcript digest, so the
// same patch produces a different digest in every run, and a comparison keyed
// on digests would report "different subject" on every run including the null
// case where the arms agree perfectly. A world with no known ordinal renders
// `?`, which is a fact rather than a guess.
func PurchaseOrder(t Trace, ordinal map[string]int) []string {
	out := make([]string, 0, len(t.Steps))
	for _, s := range t.Steps {
		for _, c := range s.Considered {
			if !c.Bought() {
				continue
			}
			ord := "?"
			if n, ok := ordinal[c.World]; ok {
				ord = fmt.Sprint(n)
			}
			out = append(out, ord+"."+c.Oracle)
		}
	}
	return out
}

// SameOrder reports whether two purchase orders are identical. Divergence is
// its negation, and the two are kept apart from COVERAGE on purpose (decision
// 6): coverage comes from ONE trace, divergence from the PAIR, and collapsing
// them would swallow M2b.2's genuine measured null.
func SameOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
