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

// The four EXERCISE WITNESSES, one per mechanism M2b.2 added (decision 5).
// They are reported SEPARATELY and never pooled into one percentage: on the
// measured warmed race W2 fired on 6 of 6 steps and W3 on 3 of 6, and a
// single "62 %" would have hidden that the reservation stopped reserving the
// moment the head world completed — which is decision 5's amendment doing
// exactly what it says.
const (
	// WitnessFinishDenominator (W2) — M2b.2 decision 2: the denominator moved.
	WitnessFinishDenominator = "W2"
	// WitnessReservationReserved (W3) — decision 3: the reservation reserved.
	// |C| >= 1. With C empty the allowance IS budget.share, i.e. M2b decision
	// 8 exactly, which is why `commit_basis: reserved` alone is not this
	// witness.
	WitnessReservationReserved = "W3"
	// WitnessPassWithheld (W4) — decision 4: a pass outcome was withheld.
	WitnessPassWithheld = "W4"
	// WitnessHardGateLapsed (W5) — decision 5: the hard-gate override lapsed.
	// IMPOSSIBLE under `voc`, where admissible = flip ∨ hard_gate.
	WitnessHardGateLapsed = "W5"
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

	Steps     int `json:"steps"`
	Exercised int `json:"exercised"`
	// Races and RacesExercised are the per-race figures. Both are printed
	// because the REFUSAL is per comparison and the INTERPRETATION is per
	// step: a race with ten steps of which one was scarce is 10 % exercised,
	// and calling it "one exercised race" would report nine tenths of an
	// allocation as though the rule had produced it.
	Races          int `json:"races"`
	RacesExercised int `json:"races_exercised"`
	// Unknown and NotApplicable count merged races whose coverage could not be
	// computed, so an aggregate never silently drops them.
	Unknown       int `json:"unknown_races"`
	NotApplicable int `json:"not_applicable_races"`

	Witnesses []Witness `json:"witnesses"`
	// CommitSetSteps is |C| >= 1 counted separately from `commit_basis:
	// reserved`, because a scarce step with |C| = 0 records `reserved` and
	// allocates by equal shares. The wire vocabulary is not changed here, so
	// coverage reports the two apart and no reader can mistake one for the
	// other (M2d.1 §9.7).
	CommitSetSteps int `json:"commit_set_steps"`
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
	w2, w3, w4, w5 := 0, 0, 0, 0
	regimes := map[string]bool{}
	for i, s := range t.Steps {
		rep.Steps++
		if s.CommitBasis != "" {
			regimes[s.CommitBasis] = true
		}
		last := i == len(t.Steps)-1
		inert := true
		if rule == SelectorNameVOC2 {
			inert = InertVOC2(s)
		} else {
			inert = InertBudgeted(s, last, stop)
		}
		if !inert {
			rep.Exercised++
		}
		if len(s.CommitSet) > 0 {
			rep.CommitSetSteps++
			w3++
		}
		for _, c := range s.Considered {
			if c.ScoreBasis == ScoreBasisFinish {
				w2++
				break
			}
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
	rep.Witnesses = []Witness{
		{ID: WitnessFinishDenominator, Name: witnessName(WitnessFinishDenominator), Steps: w2, Total: rep.Steps},
		{ID: WitnessReservationReserved, Name: witnessName(WitnessReservationReserved), Steps: w3, Total: rep.Steps},
		{ID: WitnessPassWithheld, Name: witnessName(WitnessPassWithheld), Steps: w4, Total: rep.Steps},
		{ID: WitnessHardGateLapsed, Name: witnessName(WitnessHardGateLapsed), Steps: w5, Total: rep.Steps},
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
		out.RacesExercised += r.RacesExercised
		out.Steps += r.Steps
		out.Exercised += r.Exercised
		out.Unknown += r.Unknown
		out.NotApplicable += r.NotApplicable
		out.CommitSetSteps += r.CommitSetSteps
		out.BudgetBoundRaces += r.BudgetBoundRaces
		out.Known = out.Known || r.Known
		out.Applicable = out.Applicable || r.Applicable
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
func (r CoverageReport) Vacuous() bool {
	return r.Applicable && r.Known && r.Steps > 0 && r.Exercised == 0
}

// Percent is exercised steps over steps, floored. It is meaningful only when
// Known && Applicable; Summary is what callers should print.
func (r CoverageReport) Percent() int {
	if r.Steps == 0 {
		return 0
	}
	return r.Exercised * 100 / r.Steps
}

// Summary is the one-line answer, and it is a STRING because three of its
// possible values are not numbers.
func (r CoverageReport) Summary() string {
	switch {
	case !r.Applicable && r.NotApplicable > 0 && r.Steps == 0:
		if r.Rule == SelectorNameLadder {
			return CoverageNotApplicable
		}
		return CoverageNoTrace
	case !r.Known && r.Unknown > 0 && r.Steps == 0:
		return CoverageUnknownPreM2b2
	case r.Steps == 0:
		return CoverageNoTrace
	}
	return fmt.Sprintf("%d of %d steps (%d%%)", r.Exercised, r.Steps, r.Percent())
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
		fmt.Sprintf("  steps exercised     %s", r.Summary()),
	}
	if r.Races > 0 {
		out = append(out, fmt.Sprintf("  races exercised     %d of %d", r.RacesExercised, r.Races))
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
	return fmt.Sprintf(
		"the rule under test never fired: %s of every replicate, so --selector=%s ran %s "+
			"and this is a comparison of %s against %s. %s",
		regime, r.Rule, base, base, base, remedy)
}

// VacuityBanner is the whole refusal, ready to print.
func (r CoverageReport) VacuityBanner() []string {
	return []string{
		fmt.Sprintf("VACUOUS (coverage %d of %d steps, %d of %d replicates): NO VERDICT",
			r.Exercised, r.Steps, r.RacesExercised, r.Races),
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
