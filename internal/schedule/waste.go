package schedule

import (
	"fmt"
	"sort"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// Evidence waste (PRD §11, M2b decision 18): budget spent on evidence that
// influenced no decision.
//
// THE OBVIOUS DEFINITION IS BROKEN, and it is broken by our own purchase
// law, so the derivation is written out here rather than asserted.
//
// Naive leave-one-out says a receipt influenced the decision iff
// Decide(R \ {r}) != Decide(R). Under withholding monotonicity (decision 4)
// REMOVING a receipt makes its gate's metric absent, an absent required
// metric FAILS the gate, and the world is eliminated. So for every world
// that was going to be eliminated anyway, removal reproduces the same
// decision, and EVERY receipt bought on a non-winner reads as waste —
// including the very receipt that told us the candidate was worse. The
// metric would report ~50 % waste on a healthy two-world race and would
// score a scheduler best when it verified nothing.
//
// What replaces it is BRACKET SUBSTITUTION: a receipt influenced the
// decision iff substituting it with either of its extreme bracket outcomes
// changes Decide in Type or Subject. This is decision 3's bracket reused —
// the scheduler asks "could this purchase matter?" ex ante, and the waste
// metric asks "did it?" ex post, with the same machinery and the same
// control-plane-clamped ceilings. Evidence waste is then precisely the
// purchases whose ex-ante flip was not realized, which is the only
// definition under which the metric measures the SCHEDULER rather than the
// shape of the gate lattice.

// Outcomes returns the bracket in the order decision 3 tabulates it. The
// labels are value.go's; this is the closed set both halves iterate.
func Outcomes() []string { return []string{OutcomeFailClosed, OutcomePassMin, OutcomePassMax} }

// SubstituteFn is the EX-POST half of the bracket. It is deliberately a
// different function from value.go's Bracket, because the two answer
// different questions with the same machinery: Bracket INVENTS a receipt for
// a purchase not yet made and asks "could this matter?", while Substitute
// REWRITES a purchase already made and asks "did it?". One signature could
// not serve both without one of them lying about its inputs.
type SubstituteFn func(pol policy.Policy, rec object.Receipt, b Bounds, outcome string) (object.Receipt, bool)

// Substitute is the audit-time substitution (waste.go's half of decision 3's
// bracket: "bracket substitution", per the M2b module layout). It takes a
// RECORDED receipt and returns what that receipt would have looked like
// under one bracket outcome.
//
// The second return is `bounded`. It is false when a RANKING-read metric on
// this receipt has no control-plane ceiling, because a ranking key's
// MAGNITUDE decides the winner and a magnitude we would have to take from
// the candidate's own report is exactly the number vector 23 forges. Gate-read
// metrics need no ceiling and do not clear the flag: a gate is a threshold,
// so every passing value has the same effect on the gate, and the maximal
// passing value and the minimal one are interchangeable there.
//
// Callers FAIL OPEN on bounded == false: the allocator scores flip = 1 and
// buys, the waste metric calls the receipt influential. Decide fails closed,
// the scheduler fails open.
func Substitute(pol policy.Policy, rec object.Receipt, b Bounds, outcome string) (object.Receipt, bool) {
	out := rec
	out.Result.Artifacts = append([]string(nil), rec.Result.Artifacts...)

	if outcome == OutcomeFailClosed {
		// The M1f/M1e floor, and it is structural rather than a choice:
		// status = error with the metrics ABSENT is what a purchase we
		// declined to make looks like to Decide, because Decide has no way
		// to distinguish "no receipt because we declined to buy" from "no
		// receipt because the oracle crashed" — nor should it.
		out.Result.Status = "error"
		out.Result.Metrics = map[string]int64{}
		out.Result.Detail = ""
		return out, true
	}

	max := outcome == OutcomePassMax
	metrics := make(map[string]int64, len(rec.Result.Metrics)+4)
	for k, v := range rec.Result.Metrics {
		// Every recorded metric with a control-plane ceiling is CLAMPED on
		// the way in, whether or not the policy reads it: an unclamped
		// self-report that survives into the bracket is an unclamped
		// self-report steering the budget. clampMetrics is value.go's, so
		// the ex-ante lookahead and this ex-post substitution provably clamp
		// by the same bounds.
		metrics[k] = b.clampMetrics(k, v)
	}
	out.Result.Status = "pass"
	out.Result.Detail = ""

	for _, g := range pol.Gates {
		if !g.Sel.Match(rec) {
			continue
		}
		applyGate(metrics, g, b, max)
	}
	bounded := true
	for _, k := range pol.Keys {
		if k.Metric == "" || !k.Sel.Match(rec) {
			continue
		}
		ceil, ok := b.rankCeiling(k.Metric)
		if !ok {
			// No control-plane ceiling for a metric the RANKING reads. The
			// honest answer is that we do not know how high this candidate
			// could have gone, and the caller fails open.
			bounded = false
			continue
		}
		if max {
			metrics[k.Metric] = ceil
			continue
		}
		// pass-min: the worst value consistent with passing. A descending
		// key wants the smallest, an ascending key the largest.
		if k.Desc {
			metrics[k.Metric] = worstFor(metrics, k.Metric, pol, rec)
		} else {
			metrics[k.Metric] = ceil
		}
	}
	// wall_ms_asc is ALLOCATION-SENSITIVE (decision 15): its value is a
	// function of WHICH receipts a world holds, which is exactly the
	// quantity a substitution holds fixed. The bracket cannot speak about
	// it, so a policy declaring it fails open rather than reporting a waste
	// number the key would have moved.
	for _, k := range pol.Keys {
		if k.Name == policy.KeyWallMSAsc {
			bounded = false
		}
	}
	out.Result.Metrics = metrics
	return out, bounded
}

// applyGate sets the metrics one gate reads to a PASSING configuration.
// pass-max takes the most favourable passing value, pass-min exactly the
// threshold. Neither needs a ceiling: a gate is a threshold test, so any
// passing value passes it.
func applyGate(m map[string]int64, g policy.Gate, b Bounds, max bool) {
	set := func(name string, v int64) { m[name] = v }
	switch g.Predicate {
	case policy.GateStatusPass, policy.GateSuitePass:
		// status = pass is already set by the caller.
	case policy.GateCollectNonempty:
		v := int64(1)
		if max && b.CollectedBase > 0 {
			// The BASE TREE's collect, never the candidate's own
			// collected_total: that is the number the starvation attack
			// authors.
			v = b.CollectedBase
		}
		set(policy.MetricCollectedTotal, v)
	case policy.GateCollectedNotBelow:
		// delta = collected_total - collected_base, and collected_total is
		// clamped at collected_base, so 0 is the maximum. pass-min sits
		// exactly on the declared tolerance.
		if max {
			set(policy.MetricCollectedDelta, 0)
		} else {
			set(policy.MetricCollectedDelta, -g.Threshold)
		}
	case policy.GateNoFailedTests:
		set(policy.MetricTestsFailed, 0)
		set(policy.MetricTestsErrored, 0)
	case policy.GateCoverageAtLeast:
		if max {
			// A basis-point metric caps at 10 000 by the definition of the
			// unit — a control-plane-held fact, not a candidate report.
			set(policy.MetricCoverageBP, FullBP)
		} else {
			set(policy.MetricCoverageBP, g.Threshold)
		}
	case policy.GateSkipsNotAbove:
		if max {
			set(policy.MetricTestsSkipped, 0)
		} else {
			set(policy.MetricTestsSkipped, g.Threshold)
		}
	case policy.GateCorpusComplete:
		// observed == total, and total comes from the CORPUS OBJECT, which
		// phase 0 materialized on the base tree before any world existed.
		// The recorded corpus_cases_total is the same control-plane number
		// (M2a's metric-provenance table), so falling back to it borrows
		// nothing from the candidate.
		total := b.CorpusCases
		if total <= 0 {
			total = m[policy.MetricCorpusCasesTotal]
		}
		if total <= 0 {
			total = 1
		}
		set(policy.MetricCorpusCasesTotal, total)
		set(policy.MetricCorpusCasesObserved, total)
	case policy.GatePropertiesPass:
		set(policy.MetricPropertiesFailed, 0)
		set(policy.MetricPropertiesErrored, 0)
	case policy.GatePropertyCasesAtLeast:
		// The threshold, in both directions. No ranking key reads
		// property_cases_total, so a larger passing value has exactly the
		// same effect on the decision, and inventing a ceiling for it would
		// add a number nobody holds.
		set(policy.MetricPropertyCasesTotal, g.Threshold)
	case policy.GateMutationSurvivorsNotAbove:
		// survived + timeout <= threshold. The maximum-favourable reading
		// is zero of each; pass-min sits exactly on the threshold, with the
		// hang counted as the survivor it is.
		if max {
			set(policy.MetricMutantsSurvived, 0)
			set(policy.MetricMutantsTimeout, 0)
		} else {
			set(policy.MetricMutantsSurvived, g.Threshold)
			set(policy.MetricMutantsTimeout, 0)
		}
	case policy.GateDifferentialCohortAtLeast:
		// The threshold, in both directions, for the same reason: the gate
		// is a floor and no ranking key reads diff_cohort_n.
		set(policy.MetricDiffCohortN, g.Threshold)
	case policy.GatePathsUnmodified:
		// Every violating count at zero. These are derived from two git
		// trees the CONTROL PLANE holds, so no candidate authors them in any
		// regime — which is why the bracket may set them at all.
		for _, name := range []string{
			policy.MetricProtectedModified, policy.MetricProtectedDeleted,
			policy.MetricProtectedAdded, policy.MetricHarnessModified,
			policy.MetricHarnessDeleted, policy.MetricHarnessAdded,
		} {
			set(name, 0)
		}
		if _, ok := m[policy.MetricPathsExamined]; !ok {
			set(policy.MetricPathsExamined, 1)
		}
	}
}

// worstFor is pass-min's "worst value consistent with passing" for a
// descending ranking key: the gate floor if a gate on this receipt reads the
// same metric, zero otherwise.
func worstFor(m map[string]int64, metric string, pol policy.Policy, rec object.Receipt) int64 {
	for _, g := range pol.Gates {
		if !g.Sel.Match(rec) {
			continue
		}
		if g.Predicate == policy.GateCoverageAtLeast && metric == policy.MetricCoverageBP {
			return g.Threshold
		}
		if g.Predicate == policy.GateCollectNonempty && metric == policy.MetricCollectedTotal {
			return 1
		}
	}
	return 0
}

// WasteInput is everything the waste metric reads. Every field came off the
// ledger: the pinned policy, the race's worlds and receipts, the RECORDED
// allocation trace, and the control-plane bounds. Nothing is recomputed from
// the workspace's current state, which is what makes the number stable after
// the cost table moves.
type WasteInput struct {
	Policy   policy.Policy
	Worlds   []object.RecordedWorld
	Receipts []object.RecordedReceipt
	Trace    Trace
	Bounds   Bounds
	Decide   DecideFn
	// Bracket defaults to Substitute. It is injectable so that a future
	// bracket construction can be swapped in without a second definition of
	// what "influenced" means.
	Bracket SubstituteFn
}

// WasteRow is one bought receipt's verdict.
type WasteRow struct {
	Receipt    string `json:"receipt"`
	World      string `json:"world"`
	Oracle     string `json:"oracle"` // policy-local instance name when the trace names one
	Kind       string `json:"kind"`
	CostMS     int64  `json:"cost_ms"` // the receipt's OWN recorded cost.wall_ms
	Basis      string `json:"basis"`   // BasisDecision | BasisResearch
	Influenced bool   `json:"influenced"`
	Bounded    bool   `json:"bounded"` // false ⇒ failed open, counted as influential
	Reason     string `json:"reason"`
}

// WasteReport is the derived metric. Two numbers, deliberately.
//
// WasteMS is single-receipt substitution. GreedyMS is greedy substitution in
// canonical digest order down to a minimal sufficient set: reported beside
// it because single-receipt substitution mis-states waste whenever two
// receipts are jointly redundant, and the GAP BETWEEN THE TWO is itself a
// publishable measurement — reporting only the first would flatter the
// scheduler on exactly the case correlation discounting exists to catch.
type WasteReport struct {
	Available    bool       `json:"available"`
	SpentMS      int64      `json:"spent_ms"`
	WasteMS      int64      `json:"waste_ms"`
	WasteBP      int64      `json:"waste_bp"`
	GreedyMS     int64      `json:"greedy_ms"`
	GreedyBP     int64      `json:"greedy_bp"`
	ResearchMS   int64      `json:"research_ms"`
	Rows         []WasteRow `json:"rows"`
	Wasted       []WasteRow `json:"wasted"`
	GreedyWasted []WasteRow `json:"greedy_wasted"`
	// Unbounded names the ranking metrics with no control-plane ceiling in
	// this race. It is not a warning decoration: a non-empty list means the
	// waste number is an UNDER-count, because every receipt it touched was
	// failed open into "influential", and a reader has to know that.
	Unbounded []string `json:"unbounded"`
	Decision  string   `json:"decision"`
}

// Waste computes decision 18's two numbers.
//
// Cost: two Decide calls per receipt for WasteMS, and O(n²) for GreedyMS.
// At the measured 15.8 µs per Decide over six worlds that is ~1.3 ms and
// ~56 ms for a 42-receipt race — which is why an ex-post counterfactual over
// a pure total function is affordable at all, and why purity was worth
// having for something other than replay.
func Waste(in WasteInput) (WasteReport, error) {
	if in.Decide == nil {
		return WasteReport{}, fmt.Errorf("schedule: waste: no decision function")
	}
	bracket := in.Bracket
	if bracket == nil {
		bracket = Substitute
	}
	base := in.Decide(in.Policy, in.Worlds, in.Receipts)
	rep := WasteReport{
		Available: true, Decision: base.Type,
		Rows: []WasteRow{}, Wasted: []WasteRow{}, GreedyWasted: []WasteRow{},
		Unbounded: []string{},
	}

	basisOf := in.Trace.BasisOf(in.Policy, in.Receipts)
	oracleOf := map[string]string{}
	for _, p := range in.Trace.Join(in.Policy, in.Receipts) {
		if p.Receipt != "" {
			oracleOf[p.Receipt] = p.Row.Oracle
		}
	}

	order := append([]object.RecordedReceipt(nil), in.Receipts...)
	sort.Slice(order, func(i, j int) bool { return order[i].Digest < order[j].Digest })

	unbounded := map[string]bool{}
	for _, k := range in.Policy.Keys {
		if k.Metric == "" {
			continue
		}
		if _, ok := in.Bounds.rankCeiling(k.Metric); !ok {
			unbounded[k.Metric] = true
		}
	}
	for _, k := range in.Policy.Keys {
		if k.Name == policy.KeyWallMSAsc {
			unbounded[policy.KeyWallMSAsc] = true
		}
	}
	for m := range unbounded {
		rep.Unbounded = append(rep.Unbounded, m)
	}
	sort.Strings(rep.Unbounded)

	elimination := eliminatedAt(in.Policy, in.Worlds, in.Receipts)

	for _, rr := range order {
		row := WasteRow{
			Receipt: rr.Digest, World: rr.Receipt.World, Kind: rr.Receipt.Oracle.ID,
			CostMS: rr.Receipt.Cost.WallMS, Basis: basisOf[rr.Digest],
			Oracle: oracleOf[rr.Digest], Bounded: true,
		}
		if row.Basis == "" {
			row.Basis = BasisDecision
		}
		if row.Basis == BasisResearch {
			// A purchase whose stated purpose is to influence no decision is
			// 100 % waste under the PRD's definition, and counting it would
			// make the metric meaningless. It is excluded BY CONSTRUCTION and
			// its spend is reported separately rather than hidden.
			row.Influenced = false
			row.Reason = "research purchase (--collect-inert): excluded from the waste metric by construction"
			rep.ResearchMS += row.CostMS
			rep.Rows = append(rep.Rows, row)
			continue
		}
		rep.SpentMS += row.CostMS
		influenced, bounded, reason := influencedBy(in, bracket, rr, base)
		row.Influenced, row.Bounded = influenced, bounded
		row.Reason = reason
		if !influenced && reason == "" {
			row.Reason = wasteReason(rr, elimination)
		}
		rep.Rows = append(rep.Rows, row)
		if !influenced {
			rep.WasteMS += row.CostMS
			rep.Wasted = append(rep.Wasted, row)
		}
	}

	greedy := greedyWaste(in, bracket, order, base, basisOf)
	for _, dig := range greedy {
		for _, row := range rep.Rows {
			if row.Receipt == dig {
				rep.GreedyMS += row.CostMS
				rep.GreedyWasted = append(rep.GreedyWasted, row)
				break
			}
		}
	}
	rep.WasteBP = shareBP(rep.WasteMS, rep.SpentMS)
	rep.GreedyBP = shareBP(rep.GreedyMS, rep.SpentMS)
	return rep, nil
}

// influencedBy applies decision 18's test to one receipt: substitute it with
// fail-closed, then with pass-max, and ask whether either moves Decide in
// Type or Subject.
func influencedBy(in WasteInput, bracket SubstituteFn, target object.RecordedReceipt, base object.Decision) (bool, bool, string) {
	bounded := true
	for _, outcome := range []string{OutcomeFailClosed, OutcomePassMax} {
		sub, ok := bracket(in.Policy, target.Receipt, in.Bounds, outcome)
		if !ok {
			bounded = false
			continue
		}
		if decisionMoved(base, in.Decide(in.Policy, in.Worlds, replace(in.Receipts, target.Digest, sub))) {
			return true, bounded, fmt.Sprintf("%s moves the decision", outcome)
		}
	}
	if !bounded {
		return true, false, "no control-plane ceiling for a ranking metric this receipt carries; counted as influential (fail open)"
	}
	return false, true, ""
}

// greedyWaste walks the receipts in canonical digest order, substituting
// each one that is not needed GIVEN THE SUBSTITUTIONS ALREADY COMMITTED, and
// repeats until a pass commits nothing. The committed form is fail-closed:
// a receipt we did not need is a receipt we might as well not have bought,
// and "not bought" is exactly what an absent metric looks like.
//
// The order is canonical, the commit rule is deterministic, and the fixpoint
// is unique for a given input — so the number is replayable, which a greedy
// heuristic has to be before it can appear beside a measured one.
func greedyWaste(in WasteInput, bracket SubstituteFn, order []object.RecordedReceipt, base object.Decision, basisOf map[string]string) []string {
	working := append([]object.RecordedReceipt(nil), in.Receipts...)
	dropped := map[string]bool{}
	var out []string
	for progress := true; progress; {
		progress = false
		for _, rr := range order {
			if dropped[rr.Digest] || basisOf[rr.Digest] == BasisResearch {
				continue
			}
			cur := find(working, rr.Digest)
			if cur == nil {
				continue
			}
			failClosed, okFC := bracket(in.Policy, cur.Receipt, in.Bounds, OutcomeFailClosed)
			passMax, okPM := bracket(in.Policy, cur.Receipt, in.Bounds, OutcomePassMax)
			if !okFC || !okPM {
				continue // an unbounded receipt fails open: it is never dropped
			}
			if decisionMoved(base, in.Decide(in.Policy, in.Worlds, replace(working, rr.Digest, failClosed))) {
				continue
			}
			if decisionMoved(base, in.Decide(in.Policy, in.Worlds, replace(working, rr.Digest, passMax))) {
				continue
			}
			working = replace(working, rr.Digest, failClosed)
			dropped[rr.Digest] = true
			out = append(out, rr.Digest)
			progress = true
		}
	}
	sort.Strings(out)
	return out
}

// replace returns receipts with the entry at dig swapped for sub, re-digested
// under sub's own canonical bytes. Re-digesting is the honest choice: a
// synthetic receipt is different bytes and must not borrow the identity of
// the one it stands in for, and the counted-receipt rule breaks ties by
// digest, so a borrowed digest would silently change which receipt a gate
// reads.
func replace(receipts []object.RecordedReceipt, dig string, sub object.Receipt) []object.RecordedReceipt {
	out := make([]object.RecordedReceipt, 0, len(receipts))
	subDig := digestOf(sub)
	for _, rr := range receipts {
		if rr.Digest == dig {
			out = append(out, object.RecordedReceipt{Digest: subDig, Receipt: sub})
			continue
		}
		out = append(out, rr)
	}
	return out
}

func find(receipts []object.RecordedReceipt, dig string) *object.RecordedReceipt {
	for i := range receipts {
		if receipts[i].Digest == dig {
			return &receipts[i]
		}
	}
	return nil
}

// digestOf digests a synthetic receipt. A receipt that cannot be
// canonicalized is unreachable — every field is a scalar, a string map or a
// string slice — and the fallback keeps the function total rather than
// introducing an error path into a pure metric.
func digestOf(rec object.Receipt) string {
	dig, _, err := object.Digest(rec)
	if err != nil {
		return "mv0:unencodable"
	}
	return dig
}

func shareBP(part, whole int64) int64 {
	if whole <= 0 {
		return 0
	}
	return part * 10000 / whole
}

// eliminatedAt reports, per world, the label of the first hard gate its
// evidence FAILS. It is DISPLAY DATA and nothing else: it produces the
// sentence beside a waste row, never a verdict, and every decision in this
// file comes from the injected DecideFn. Getting it wrong changes no number.
func eliminatedAt(pol policy.Policy, worlds []object.RecordedWorld, receipts []object.RecordedReceipt) map[string]string {
	out := map[string]string{}
	for _, w := range worlds {
		for _, g := range pol.Gates {
			rec := countedReceipt(g.Sel, w, receipts)
			if rec == nil {
				break // a rung still to climb is not a failure
			}
			if ok, _ := g.Eval(rec); !ok {
				out[w.Digest] = g.Label
				break
			}
		}
	}
	return out
}

// wasteReason renders the sentence beside a wasted row. The interesting form
// is the second one: "we spent 400 ms collecting on a candidate the next rung
// was going to eliminate" is a statement about ladder order and discriminator
// cost — that is, about SCHEDULING, which is what the metric is for.
func wasteReason(rr object.RecordedReceipt, elimination map[string]string) string {
	if label, ok := elimination[rr.Receipt.World]; ok {
		return fmt.Sprintf("%s was eliminated at [%s] either way", short(rr.Receipt.World), label)
	}
	return "neither bracket outcome moves the decision"
}

func short(dig string) string {
	if len(dig) <= 12 {
		return dig
	}
	return dig[:12] + "…"
}
