package race

import (
	"fmt"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/policy"
)

// behaviorFacts are the non-numeric identities a comparison receipt carries
// in result.detail: which behaviour class this world landed in, which
// cohort it was compared against, which corpus the comparison ran over, and
// the smallest case id the cohort disagreed on.
type behaviorFacts struct {
	Class  string
	Cohort string
	Corpus string
	First  string
}

// parseBehaviorDetail reads the facts back out of a comparison receipt's
// result.detail. It is TOTAL — an unparseable detail yields zeroes and
// `ok == false`, never a panic and never a partially-filled sentence.
//
// Reading a string here rather than a field is deliberate and it is the
// smallest honest option. The escalation sentence names the corpus and the
// first distinguishing case; `Decide` may read neither `Inputs` (M2a
// decision 24 — a provenance field a gate could read is a metric with extra
// steps) nor CAS (Decide is pure and replay depends on it); and
// result.detail is exactly the channel the design already provides for "the
// single string a pure consumer may quote when a count is not enough to act
// on". A hand-written fixture that carries no detail simply does not fire
// the rule, which is the fail-closed direction.
func parseBehaviorDetail(detail string) (behaviorFacts, bool) {
	rest, ok := strings.CutPrefix(detail, oracle.DetailPrefix)
	if !ok {
		return behaviorFacts{}, false
	}
	class, tail, ok := strings.Cut(rest, " (")
	if !ok || !strings.HasSuffix(tail, ")") {
		return behaviorFacts{}, false
	}
	out := behaviorFacts{Class: class}
	for _, part := range strings.Split(strings.TrimSuffix(tail, ")"), ", ") {
		field, value, ok := strings.Cut(part, " ")
		if !ok {
			return behaviorFacts{}, false
		}
		switch field {
		case "cohort":
			out.Cohort = value
		case "corpus":
			out.Corpus = value
		case "first":
			// "first distinguishing case c0001"
			if fields := strings.Fields(part); len(fields) == 4 {
				out.First = fields[3]
			}
			_ = value
		}
	}
	return out, out.Cohort != ""
}

// behavioralSplit evaluates M2a rule 1b over the candidates' comparison
// receipts.
//
// The rule reads the receipts of the worlds that were actually compared, so
// it fires on the FACT of a partition rather than on any world's verdict: a
// cohort can split while every member passes every hard gate, and that is
// precisely the case the rule exists for. Under M1f such a race ESCALATEd
// on on_ranking_tie and told the maintainer nothing except that two digests
// tied; under M2a it escalates with the input and both answers attached.
func behavioralSplit(threshold int, t *RaceTrace) (EscalationResult, bool) {
	sel, ok := differentialSelector(t.Policy)
	if !ok {
		return EscalationResult{}, false
	}
	classes, cohortN, compared := int64(0), int64(0), int64(0)
	facts := behaviorFacts{}
	seen := false
	for i := range t.Candidates {
		cr := t.Candidates[i].counted[sel]
		if cr.rec == nil {
			continue
		}
		n, hasN := cr.rec.Result.Metrics[policy.MetricDiffCohortN]
		c, hasC := cr.rec.Result.Metrics[policy.MetricDiffClasses]
		if !hasN || !hasC {
			// A comparison of one carries diff_cohort_n and nothing else.
			// Absence is the honest record that there was no partition to
			// report, and it must never be read as "one class".
			continue
		}
		f, parsed := parseBehaviorDetail(cr.rec.Result.Detail)
		if !parsed {
			continue
		}
		if !seen || c > classes {
			classes, cohortN, facts, seen = c, n, f, true
			compared = cr.rec.Result.Metrics[policy.MetricDiffCasesCompared]
		}
	}
	if !seen || cohortN < 2 || classes < int64(threshold) {
		return EscalationResult{}, false
	}
	return EscalationResult{
		Rule: RuleOnBehavioralSplit,
		Detail: fmt.Sprintf(
			"%d worlds split into %d behavior classes over %d compared cases of corpus %s; the shared evidence does not say which behavior is intended (first distinguishing case %s)",
			cohortN, classes, compared, facts.Corpus, facts.First),
	}, true
}

// differentialSelector is the selector a compiled policy reads comparison
// receipts through. It is derived from the declared reducer instance, so a
// policy with no reducer has none and the rule cannot fire.
func differentialSelector(pol policy.Policy) (policy.Selector, bool) {
	o, ok := pol.DifferentialOracle()
	if !ok {
		return policy.Selector{}, false
	}
	return policy.Selector{ID: o.Kind, Config: o.Config}, true
}

// behaviorReceipt returns one candidate's comparison receipt, for the
// renderers. Decide reads it through the same selector, so what `mvo
// explain` prints and what the rule fired on cannot drift.
func behaviorReceipt(pol policy.Policy, c *CandidateTrace) (*object.Receipt, string) {
	sel, ok := differentialSelector(pol)
	if !ok {
		return nil, ""
	}
	cr := c.counted[sel]
	return cr.rec, cr.dig
}
