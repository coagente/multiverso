package schedule

// The flip lookahead (M2b §2.3), PURE: bracket construction, control-plane
// clamping, and three calls into the decision rule the scheduler HOLDS BY
// REFERENCE and never copies (decision 1).
//
// `flip` is a LOGICAL REACHABILITY TEST over the pure decision function, not
// a probability. We do not estimate P(the candidate is correct) and we do
// not pretend to: that number needs labelled outcomes and there are none
// until M2d. What v0 measures is DECISION-RELEVANCE — could this purchase
// matter at all? — computed exactly against the pinned policy.

import (
	"fmt"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// The three bracket outcomes (M2b §2.3). Three receipts, three Decide calls,
// per frontier purchase.
const (
	// OutcomeFailClosed is the M1f/M1e floor: status = error, metrics
	// ABSENT. An absent required metric fails every gate reading it, so this
	// outcome reaches every elimination the purchase could cause.
	OutcomeFailClosed = "fail-closed"
	// OutcomePassMin is the marginal survivor: every gate-read metric
	// exactly at its threshold, every ranking-read metric at its WORST value
	// consistent with passing. Does this world survive without overtaking
	// anyone?
	OutcomePassMin = "pass-min"
	// OutcomePassMax is the maximal challenger: every ranking-read metric at
	// its best CONTROL-PLANE-BOUNDED value. Could this world overtake the
	// incumbent?
	OutcomePassMax = "pass-max"
)

// Bounds are the control-plane-held ceilings the bracket is allowed to use
// (M2b decision 3b, which is a SECURITY decision before it is an accuracy
// decision). Every field here is a number the control plane produced before
// any candidate existed, or a structural constant of the metric's own
// definition. None of them is candidate-authored, and where none exists the
// metric is UNKNOWN and flip is 1.
//
// Without the clamp there is a clean starvation attack (adversarial vector
// 23): a candidate reports tests_passed = 500 on an eight-test repository,
// becomes an unreachable incumbent, and every rival's pass-max then fails to
// overtake it — flip = 0 on every rival, the scheduler stops buying, and the
// honest rivals are rejected for want of evidence they were never sold.
// M1f's collect-equals-suite-total invariant does not save us: a COHERENT
// forgery authors both numbers.
//
// Clamping costs nothing and touches Decide not at all — Decide still sees
// 500 and still decides whatever it decides — it merely refuses to let an
// unbounded self-report steer the budget.
type Bounds struct {
	// CollectedBase is the base-tree collect measurement (M1e decision 13),
	// produced before any candidate existed. It is tests_passed's ceiling.
	// 0 means UNMEASURED, which makes tests_passed unbounded and flip 1.
	CollectedBase int64
	// CorpusCases is the pinned corpus object's cases_total. 0 falls back to
	// the policy's own resolved cases_max, which is also control-plane held.
	CorpusCases int64
}

// rankCeiling is the control-plane ceiling for one ranking-read metric.
// A metric with no such ceiling is UNKNOWN: the bracket cannot be
// constructed, so the purchase is priced as if it could matter (fail open).
func (b Bounds) rankCeiling(metric string) (int64, bool) {
	switch metric {
	case policy.MetricTestsPassed:
		// NEVER the candidate's own collected_total: that is the number the
		// starvation attack authors.
		if b.CollectedBase > 0 {
			return b.CollectedBase, true
		}
		return 0, false
	case policy.MetricCoverageBP:
		// A basis-point metric caps at 10 000 by the definition of the unit,
		// which is a control-plane-held fact rather than a candidate report.
		return FullBP, true
	default:
		return 0, false
	}
}

// clampMetrics returns v clamped to its control-plane ceiling. A metric with
// no ceiling is returned unchanged: the clamp exists to bound self-reports
// the bracket compares against, never to rewrite evidence.
func (b Bounds) clampMetrics(metric string, v int64) int64 {
	if ceil, ok := b.rankCeiling(metric); ok && v > ceil {
		return ceil
	}
	return v
}

// clampReceipts returns the receipt set the LOOKAHEAD reasons over: every
// ranking-read metric clamped to its control-plane ceiling, on a COPY.
// Recorded receipts are never mutated and the real decision never sees this
// set — Decide is called on the real receipts everywhere else, and its
// verdict is unchanged.
//
// The incumbent must be clamped by the same bounds the bracket is, or the
// bracket's ceiling is measured against an unbounded self-report and the
// starvation attack works anyway.
func clampReceipts(pol policy.Policy, receipts []object.RecordedReceipt, b Bounds) []object.RecordedReceipt {
	metrics := rankedMetrics(pol)
	if len(metrics) == 0 {
		return receipts
	}
	out := make([]object.RecordedReceipt, len(receipts))
	copy(out, receipts)
	for i := range out {
		var clamped map[string]int64
		for name := range metrics {
			v, ok := out[i].Receipt.Result.Metrics[name]
			if !ok {
				continue
			}
			cv := b.clampMetrics(name, v)
			if cv == v {
				continue
			}
			if clamped == nil {
				clamped = make(map[string]int64, len(out[i].Receipt.Result.Metrics))
				for k, mv := range out[i].Receipt.Result.Metrics {
					clamped[k] = mv
				}
			}
			clamped[name] = cv
		}
		if clamped != nil {
			out[i].Receipt.Result.Metrics = clamped
		}
	}
	return out
}

// rankedMetrics is the set of metric names the policy's effective ranking
// keys read. Gate-read metrics are deliberately absent: a gate is a
// pass/fail predicate, and clamping its input could only turn a pass into a
// fail, which is Decide's job and not the scheduler's.
func rankedMetrics(pol policy.Policy) map[string]bool {
	out := map[string]bool{}
	for _, k := range pol.Keys {
		if k.Metric != "" {
			out[k.Metric] = true
		}
	}
	return out
}

// bracketOutcome is one named outcome and the synthetic receipts that stand
// for it.
//
// A pass outcome carries MORE THAN ONE receipt, and that is the one place
// where this implementation had to read the design rather than transcribe
// it. A bracket of exactly one receipt makes every rung but the last
// DECISION-INERT — buying `guard` alone leaves the world failing `collect`
// and `suite` for want of a receipt, so `Decide` returns what it already
// returned, flip is 0, and a scheduler that refuses every zero-value
// purchase buys NOTHING AT ALL. We ran that version: it stopped at step 1
// with an empty receipt set on every race.
//
// What the rendered trace in M2b §4.4 shows instead is flip = 1 on `guard`
// and on `collect`, which is only true of a bracket that asks the question
// the scheduler actually needs answered: NOT "does this receipt alone move
// the decision?" but "could a world that pays for this rung reach a
// different decision?". So a pass outcome completes the world's REMAINING
// ladder at the same extreme — pass-min completes minimally, pass-max
// completes at the control-plane ceiling — and the fail-closed outcome
// completes nothing, because a world that fails a gate climbs no further
// (M1e decision 12's short-circuit, which the frontier rule preserves).
//
// The two properties that make the rule useful both survive: a rung nothing
// reads still scores 0 (completing with it and without it decides the same),
// and a world that cannot overtake the incumbent even at its ceiling still
// scores 0 — which is decision 3's "could this purchase matter at all?",
// answered over the ladder rather than over one rung of it.
type bracketOutcome struct {
	name     string
	receipts []object.RecordedReceipt
}

// Bracket constructs the closed set of synthetic outcomes spanning what a
// purchase could report. ok is false when some bound the bracket needs is
// not control-plane held: the caller must then price the purchase as if it
// could matter (flip = 1), because an unknown bound must never look like a
// zero.
func Bracket(pol policy.Policy, w object.RecordedWorld, r Rung, rest []Rung,
	existing []object.RecordedReceipt, b Bounds) (outcomes []bracketOutcome, ok bool) {
	// fail-closed always exists: status = error with NO metrics at all is
	// constructible for every kind, it needs no bound, and it completes
	// nothing — a world that errored at this rung climbs no further.
	outcomes = append(outcomes, bracketOutcome{
		name:     OutcomeFailClosed,
		receipts: []object.RecordedReceipt{r.receipt(w, "error", map[string]int64{})},
	})

	minRecs, minOK, known := passOutcome(pol, w, append([]Rung{r}, rest...), existing, b, false)
	if !known {
		return outcomes, false
	}
	maxRecs, maxOK, known := passOutcome(pol, w, append([]Rung{r}, rest...), existing, b, true)
	if !known {
		return outcomes, false
	}
	// The two pass outcomes stand or fall INDEPENDENTLY. A policy can pin
	// every part of a suite receipt (no-failed-tests, skips-not-above) so
	// that the marginal survivor is not constructible while the maximal
	// challenger still is, and suppressing both because one is unreachable
	// would price the world's whole ladder at zero — which is a scheduler
	// that buys nothing, not a scheduler that found nothing worth buying.
	if minOK {
		outcomes = append(outcomes, bracketOutcome{name: OutcomePassMin, receipts: minRecs})
	}
	if maxOK {
		outcomes = append(outcomes, bracketOutcome{name: OutcomePassMax, receipts: maxRecs})
	}
	return outcomes, true
}

// synthetic is one rung's prospective receipt while it is being built: the
// metrics so far, plus which of them a GATE fixed (a gate's value may be
// raised to satisfy an invariant, but never lowered out of passing) and
// which a RANKING KEY fixed (those carry the outcome's meaning — the
// marginal survivor or the maximal challenger — and are never moved).
type synthetic struct {
	rung      Rung
	metrics   map[string]int64
	gateFixed map[string]bool
	rankFixed map[string]bool
}

// passOutcome builds the synthetic receipts for one pass extreme over a
// world's remaining ladder: the prospective purchase first, then every rung
// it has not bought yet, each at the same extreme.
//
// The receipts are then made COHERENT with the policy's declared invariants
// (M1f decision 10), and that step is not optional. An invariant reads two
// oracles' metrics and a missing one VIOLATES it, so a bracket that set
// `collected_total = 1` on the collect rung and no `tests_total` at all on
// the suite rung would ask `Decide` about a world whose own evidence
// contradicts itself — and `Decide` would correctly answer "this world does
// not pass", on EVERY rung, for EVERY world. The scheduler would then price
// its whole ladder at flip = 0 and buy nothing. We ran that version against
// the shipped default policy, which declares two invariants: every race
// bought zero receipts and escalated as machinery failure.
//
// The bracket must therefore construct evidence an HONEST RUN COULD HAVE
// PRODUCED. That is the same discipline the gate metrics already follow, one
// level up.
func passOutcome(pol policy.Policy, w object.RecordedWorld, rungs []Rung,
	existing []object.RecordedReceipt, b Bounds, max bool) (recs []object.RecordedReceipt, reachable, known bool) {

	synths := make([]*synthetic, 0, len(rungs))
	for _, r := range rungs {
		sel := r.Selector()
		metrics, ok, known := gatePassMetrics(gatesFor(pol, sel), r, b)
		if !known {
			return nil, false, false
		}
		if !ok {
			return nil, false, true
		}
		syn := &synthetic{
			rung: r, metrics: metrics,
			gateFixed: map[string]bool{}, rankFixed: map[string]bool{},
		}
		for name := range metrics {
			syn.gateFixed[name] = true
		}
		// PRESENCE-ONLY CONSUMERS. `require_evidence` (M1e rule 2) does not
		// read a threshold: it asks whether the winner's receipt carries any
		// of its kind's declared metrics at all, because "the structured
		// source was unavailable" is the failure it exists to route to a
		// human. A bracket that seeds only the metrics a GATE or a KEY reads
		// hands that rule a synthetic receipt with an EMPTY metric map, the
		// rule fires on every outcome, nothing moves, flip is 0 — and the
		// scheduler refuses to buy the very receipt the escalation is about.
		// Reproduced at unbounded budget: adaptive ESCALATE against fixed
		// SELECT, and, with a single candidate, a race that bought ZERO
		// receipts. The bracket must construct evidence an HONEST RUN COULD
		// HAVE PRODUCED, and an honest run of a kind emits its vocabulary.
		if requiresEvidence(pol, sel) {
			for _, m := range policy.KindMetrics(r.Kind) {
				if _, fixed := metrics[m]; !fixed {
					metrics[m] = 0
				}
			}
		}
		for _, k := range keysFor(pol, sel) {
			ceil, haveCeil := b.rankCeiling(k.Metric)
			if !haveCeil {
				// A ranking key's MAGNITUDE decides the winner, and a
				// magnitude we would have to take from the candidate's own
				// report is exactly the number vector 23 forges. No ceiling,
				// no bracket: fail open.
				return nil, false, false
			}
			if max {
				if cur, fixed := syn.metrics[k.Metric]; !fixed || ceil > cur {
					syn.metrics[k.Metric] = ceil
				}
			} else if _, fixed := syn.metrics[k.Metric]; !fixed {
				// The worst value consistent with passing. No gate reads it,
				// so nothing constrains it from below.
				syn.metrics[k.Metric] = 0
			}
			syn.rankFixed[k.Metric] = true
		}
		synths = append(synths, syn)
	}
	if ok, known := satisfyInvariants(pol, w, existing, synths, b); !known {
		return nil, false, false
	} else if !ok {
		return nil, false, true
	}
	for _, syn := range synths {
		recs = append(recs, syn.rung.receipt(w, "pass", syn.metrics))
	}
	return recs, true, true
}

// satisfyInvariants makes the synthetic receipts coherent with every
// invariant the policy declares. The vocabulary is CLOSED (M1f decision 10),
// so this is a table with one row per invariant — an unknown name is an
// unknown constraint, and an unknown constraint fails OPEN.
//
// reachable is false when an invariant is already violated by evidence the
// world REALLY holds: no future purchase can repair that, and the honest
// bracket says the pass outcome is unreachable.
func satisfyInvariants(pol policy.Policy, w object.RecordedWorld, existing []object.RecordedReceipt,
	synths []*synthetic, b Bounds) (reachable, known bool) {

	find := func(sel policy.Selector) *synthetic {
		for _, s := range synths {
			if s.rung.Selector() == sel {
				return s
			}
		}
		return nil
	}
	real := func(sel policy.Selector, metric string) (int64, bool) {
		rec := countedReceipt(sel, w, existing)
		if rec == nil {
			return 0, false
		}
		v, ok := rec.Result.Metrics[metric]
		return v, ok
	}

	for _, inv := range pol.Invariants {
		switch inv.Name {
		case policy.InvariantCollectEqualsSuiteTotal:
			cSel, sSel := inv.Roles[policy.RoleCollect], inv.Roles[policy.RoleSuite]
			cSyn, sSyn := find(cSel), find(sSel)
			cReal, haveC := real(cSel, policy.MetricCollectedTotal)
			sReal, haveS := real(sSel, policy.MetricTestsTotal)
			switch {
			case haveC && haveS:
				// Both sides already recorded. If they disagree the world is
				// already caught by the invariant and no purchase repairs it.
				if cReal != sReal {
					return false, true
				}
			case haveC:
				setAnchor(sSyn, policy.MetricTestsTotal, cReal)
			case haveS:
				setAnchor(cSyn, policy.MetricCollectedTotal, sReal)
			default:
				// Neither side exists yet: anchor both on the CONTROL-PLANE
				// base-tree count when we hold one, and on the smallest
				// passing count when we do not. Never on a candidate report.
				anchor := b.CollectedBase
				if anchor < 1 {
					anchor = 1
				}
				setAnchor(cSyn, policy.MetricCollectedTotal, anchor)
				setAnchor(sSyn, policy.MetricTestsTotal, anchor)
			}
		case policy.InvariantSuitePartsSumToTotal:
			sSel := inv.Roles[policy.RoleSuite]
			syn := find(sSel)
			if syn == nil {
				// The suite receipt already exists: whatever it says, it says
				// — the invariant is evaluated on it as recorded.
				continue
			}
			if !balanceSuiteParts(syn) {
				return false, true
			}
		default:
			// An invariant this table does not know is a constraint we
			// cannot construct evidence for. Fail OPEN: the purchase is
			// priced as if it could matter.
			return false, false
		}
	}
	return true, true
}

// setAnchor raises a synthetic metric to the value an invariant anchors it
// at. A gate-fixed metric may be RAISED — a gate's synthetic value is the
// minimum that passes, not a ceiling — while a ranking-fixed one is left
// alone, because it carries the outcome's whole meaning.
func setAnchor(syn *synthetic, metric string, v int64) {
	if syn == nil || syn.rankFixed[metric] {
		return
	}
	if cur, ok := syn.metrics[metric]; !ok || cur < v {
		syn.metrics[metric] = v
	}
}

// balanceSuiteParts makes tests_total equal passed+failed+errored+skipped on
// a synthetic suite receipt, putting the slack in the first part no gate and
// no ranking key has pinned. The preference order runs from the part a
// passing suite is most free to carry to the one that carries the most
// meaning: a suite that passed its status gate may still report failures
// unless a gate says otherwise, and moving tests_passed would change which
// bracket outcome this is.
func balanceSuiteParts(syn *synthetic) bool {
	parts := []string{
		policy.MetricTestsPassed, policy.MetricTestsFailed,
		policy.MetricTestsErrored, policy.MetricTestsSkipped,
	}
	sum := int64(0)
	for _, p := range parts {
		sum += syn.metrics[p]
	}
	total, hasTotal := syn.metrics[policy.MetricTestsTotal]
	if !hasTotal {
		// The total is free: the parts decide it.
		for _, p := range parts {
			if _, ok := syn.metrics[p]; !ok {
				syn.metrics[p] = 0
			}
		}
		syn.metrics[policy.MetricTestsTotal] = sum
		return true
	}
	slack := total - sum
	if slack < 0 {
		// The pinned parts already exceed the anchored total. Nothing this
		// bracket can set makes an honest run out of it.
		return false
	}
	// The slack goes to the first part NO GATE has pinned, in the order a
	// passing suite is most free to carry it. A gate-pinned part is a
	// policy statement about what passing means and may not be moved.
	//
	// The ranking-read part is the LAST resort rather than an excluded one:
	// under a policy that pins failed, errored and skipped (sealed.json does
	// exactly that), "the worst value consistent with passing" for
	// tests_passed is not zero — it is the whole suite, because the policy
	// left no other place for the tests to be. Refusing to fill it there
	// would make the marginal survivor unconstructible and price a real
	// purchase at zero.
	for _, p := range []string{
		policy.MetricTestsFailed, policy.MetricTestsSkipped,
		policy.MetricTestsErrored, policy.MetricTestsPassed,
	} {
		if syn.gateFixed[p] {
			continue
		}
		syn.metrics[p] = syn.metrics[p] + slack
		slack = 0
		break
	}
	for _, p := range parts {
		if _, ok := syn.metrics[p]; !ok {
			syn.metrics[p] = 0
		}
	}
	if slack != 0 {
		return false
	}
	return true
}

// gatePassMetrics is the metric set that makes every gate reading this
// selector pass, each metric EXACTLY at its threshold. reachable is false
// when a gate can never pass; known is false when a gate needs a bound the
// control plane does not hold.
func gatePassMetrics(gates []policy.Gate, r Rung, b Bounds) (m map[string]int64, reachable, known bool) {
	m = map[string]int64{}
	for _, g := range gates {
		if g.AlwaysFails {
			return m, false, true
		}
		switch g.Predicate {
		case policy.GateStatusPass, policy.GateSuitePass:
			// The status alone decides it.
		case policy.GateCollectNonempty:
			m[policy.MetricCollectedTotal] = 1
		case policy.GateCollectedNotBelow:
			m[policy.MetricCollectedDelta] = -g.Threshold
		case policy.GateNoFailedTests:
			m[policy.MetricTestsFailed] = 0
			m[policy.MetricTestsErrored] = 0
		case policy.GateCoverageAtLeast:
			m[policy.MetricCoverageBP] = g.Threshold
		case policy.GateSkipsNotAbove:
			m[policy.MetricTestsSkipped] = g.Threshold
		case policy.GateCorpusComplete:
			// observed == total, and BOTH come from the control plane: the
			// pinned corpus object's case count, or failing that the
			// policy's own resolved ceiling. Neither is candidate-authored.
			total := b.CorpusCases
			if total <= 0 {
				total = r.CorpusCases
			}
			if total <= 0 {
				return m, true, false
			}
			m[policy.MetricCorpusCasesObserved] = total
			m[policy.MetricCorpusCasesTotal] = total
		case policy.GatePropertiesPass:
			m[policy.MetricPropertiesFailed] = 0
			m[policy.MetricPropertiesErrored] = 0
		case policy.GatePropertyCasesAtLeast:
			m[policy.MetricPropertyCasesTotal] = g.Threshold
		case policy.GateMutationSurvivorsNotAbove:
			m[policy.MetricMutantsSurvived] = g.Threshold
			m[policy.MetricMutantsTimeout] = 0
		case policy.GateDifferentialCohortAtLeast:
			m[policy.MetricDiffCohortN] = g.Threshold
		case policy.GatePathsUnmodified:
			// Every VIOLATING count zero, and paths_examined present — the
			// predicate reads its presence, never its value, so no bound is
			// needed and none is invented.
			for _, name := range []string{
				policy.MetricProtectedModified, policy.MetricProtectedDeleted,
				policy.MetricProtectedAdded, policy.MetricHarnessModified,
				policy.MetricHarnessDeleted, policy.MetricHarnessAdded,
			} {
				m[name] = 0
			}
			m[policy.MetricPathsExamined] = 0
		default:
			// A predicate this table does not know is a bound we do not
			// hold. Fail OPEN: the purchase is priced as if it could matter.
			return m, true, false
		}
	}
	return m, true, true
}

// gatesFor returns the hard gates that read one selector, in ladder order.
func gatesFor(pol policy.Policy, sel policy.Selector) []policy.Gate {
	out := make([]policy.Gate, 0, len(pol.Gates))
	for _, g := range pol.Gates {
		if g.Sel == sel {
			out = append(out, g)
		}
	}
	return out
}

// requiresEvidence reports whether the policy's `require_evidence`
// escalation names one selector. It is the bracket's only presence-only
// consumer: every other rule reads a value, and a value has a threshold the
// bracket can pin.
func requiresEvidence(pol policy.Policy, sel policy.Selector) bool {
	for _, req := range pol.Esc.RequireEvidence {
		if req.Sel == sel {
			return true
		}
	}
	return false
}

// keysFor returns the metric-bearing ranking keys that read one selector.
func keysFor(pol policy.Policy, sel policy.Selector) []policy.Key {
	out := make([]policy.Key, 0, len(pol.Keys))
	for _, k := range pol.Keys {
		if k.Metric != "" && k.Sel == sel {
			out = append(out, k)
		}
	}
	return out
}

func copyMetrics(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Lookahead is the flip test for one prospective purchase: at most three
// calls into the decision rule, on CLAMPED receipts, comparing Type and
// Subject only.
//
//	flip(w,o) = 1 iff some bracket outcome changes Decide in Type or Subject
//
// base must be dec(pol, worlds, receipts) on the SAME clamped receipt set —
// the caller computes it once per batch, so a step costs 1 + 3×|frontier|
// calls rather than 4×|frontier|.
func Lookahead(dec DecideFn, pol policy.Policy, worlds []object.RecordedWorld,
	receipts []object.RecordedReceipt, base object.Decision,
	w object.RecordedWorld, r Rung, rest []Rung, b Bounds) (int64, []string) {

	outcomes, ok := Bracket(pol, w, r, rest, receipts, b)
	if !ok {
		// DECISION 3b's fail-open direction, stated where it happens: Decide
		// fails closed (an absent metric fails a gate), the scheduler fails
		// open (an unknown bound makes a purchase look valuable and gets it
		// bought). Two layers, two opposite fail directions, each safe in
		// its own layer.
		return 1, []string{"unbounded: no control-plane ceiling for this purchase"}
	}
	flip := int64(0)
	notes := make([]string, 0, len(outcomes))
	next := make([]object.RecordedReceipt, len(receipts), len(receipts)+1)
	copy(next, receipts)
	for _, o := range outcomes {
		d := dec(pol, worlds, append(next, o.receipts...))
		if decisionMoved(base, d) {
			flip = 1
			notes = append(notes, fmt.Sprintf("%s:%s->%s%s", o.name, base.Type, d.Type, subjectNote(base, d)))
			continue
		}
		notes = append(notes, fmt.Sprintf("%s:%s(unchanged)", o.name, d.Type))
	}
	return flip, notes
}

// decisionMoved compares two decisions in Type and Subject — never in
// Evidence, which every purchase changes by construction, and never in
// Rationale, which is a rendering of the two.
func decisionMoved(a, b object.Decision) bool {
	if a.Type != b.Type || len(a.Subject) != len(b.Subject) {
		return true
	}
	for i := range a.Subject {
		if a.Subject[i] != b.Subject[i] {
			return true
		}
	}
	return false
}

// subjectNote renders the subject change a flip produced, or "" when only
// the type moved.
func subjectNote(a, b object.Decision) string {
	if len(b.Subject) == 0 || (len(a.Subject) > 0 && a.Subject[0] == b.Subject[0]) {
		return ""
	}
	return " " + b.Subject[0]
}
