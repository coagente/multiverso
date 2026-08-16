package schedule

// The ladder: the policy's REQUIRED oracle instances reduced to what the
// allocator needs to price them, plus the synthetic receipts the bracket
// lookahead is built from.
//
// Every field on a Rung comes from the compiled policy or from a
// control-plane measurement. NOTHING HERE IS AUTHORED BY A CANDIDATE (M1f),
// which is the rule a scheduler that lets a candidate steer its own
// verification budget breaks.

import (
	"sort"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// Rung is one purchasable oracle instance in ladder order.
type Rung struct {
	Name   string // declared oracle name; the family in the v0 dialect
	Kind   string // registry kind; "" in the v0 dialect, which declares none
	Family string
	Config string // resolved-config digest
	// Provider is the corpus provider this instance declares, which is the
	// prior class's second input (the wire label alone cannot tell an
	// operator-written corpus from one derived from the repo's tests).
	Provider string
	// CorpusCases is the policy's own resolved materialization ceiling — a
	// control-plane number, used as the bracket's corpus bound when the
	// pinned corpus object's count is not to hand.
	CorpusCases int64
	// Units is the PREDICTED scaling unit count, and it comes from a
	// CONTROL-PLANE denominator or from nothing at all. Never from the
	// candidate's own report: a candidate reporting tests_total = 5 000
	// against wall_ms = 400 would otherwise steer the cost model of every
	// future race in the workspace (decision 7a, adversarial vector 22).
	Units int64
	// HardGate records whether a hard gate reads this rung — the fact that
	// makes declining it an elimination rather than a saving.
	HardGate bool
	Corr     object.Correlation
	prior    string
}

// Selector is the compiled policy's evidence selector for this rung: the v0
// dialect selects by family (M0's rule), v1 by the receipt's own oracle
// identity — the registry kind plus the resolved-config digest.
func (r Rung) Selector() policy.Selector {
	if r.Kind == "" {
		return policy.Selector{Family: r.Family}
	}
	return policy.Selector{ID: r.Kind, Config: r.Config}
}

func (r Rung) evidence() evidence {
	return evidence{kind: r.Kind, corr: r.Corr, prior: r.prior}
}

// label names the rung the way a declined row reads: the kind when there is
// one, the declared name otherwise.
func (r Rung) label() string {
	if r.Kind != "" {
		return r.Kind
	}
	return r.Name
}

// receipt builds one SYNTHETIC bracket receipt. It is never recorded, never
// digested into a decision and never leaves the lookahead — it exists so the
// pure decision function can be asked "and if the purchase said THIS?".
//
// It is bound to exactly the world it stands for, with the strongest
// freshness basis, so a gate's basis requirement cannot make a bracket
// outcome unreachable for a reason that has nothing to do with the outcome.
func (r Rung) receipt(w object.RecordedWorld, status string, metrics map[string]int64) object.RecordedReceipt {
	rec := object.Receipt{
		Schema: object.SchemaReceipt,
		World:  w.Digest,
		Oracle: object.OracleRef{ID: r.Kind, Config: r.Config},
		Execution: object.Execution{
			Argv:          []string{},
			IsolationCaps: object.IsolationCaps{},
		},
		Result: object.Result{
			Status:    status,
			Metrics:   metrics,
			Tools:     map[string]string{},
			Artifacts: []string{},
		},
		Freshness: object.Freshness{
			Basis:    object.BasisConstruction,
			ValidFor: object.ValidFor{Tree: w.World.Tree, Env: w.World.Env},
		},
		Family:      r.Family,
		Inputs:      object.NoInputs(),
		Correlation: r.Corr,
	}
	dig, _, err := object.Digest(rec)
	if err != nil {
		// Unreachable: the value is canonical by construction. A digest we
		// cannot compute is still usable as a lookahead key, because the
		// digest only breaks ties between receipts matching one selector and
		// a frontier purchase is unpurchased by definition.
		dig = "mv0:bracket"
	}
	return object.RecordedReceipt{Digest: dig, Receipt: rec}
}

// LadderNames is the ordered list of oracle instances a race may purchase.
//
// Normally it is exactly `pol.Required`: declared-but-unrequired oracles are
// never run, because evidence waste is a measured PRD metric and buying a
// rung nothing reads is the purest form of it.
//
// Under `--collect-inert` (decision 11) it is Required followed by every
// other DECLARED WORLD-STAGE instance, name-sorted. That extension is what
// makes the mode mean anything: `Required` is derived from what a gate, key,
// invariant or escalation rule reads, so a rung that is decision-inert
// BECAUSE NOTHING READS IT is by construction absent from it — and those are
// exactly the rungs M2a ships unranked (mutation and property metrics, whose
// every derivable ranking key is wrong-signed) and that M2d must correlate
// against ground truth before anyone ranks by them. Without the extension
// `--collect-inert` could only ever re-buy rungs the policy already reads,
// which is not a research mode, it is a rounding error.
//
// COHORT reducers are deliberately not added. A cohort rung is a dependent
// purchase whose denominator is the cohort membership at close time (M2a
// amendment 29); adding one that no rule reads would change the cohort's
// shape to record evidence nothing consumes.
//
// The appended rungs come LAST, so the policy's gate order — decision 2's
// fidelity ladder, which the scheduler may not reorder — is untouched.
func LadderNames(pol policy.Policy, collectInert bool) []string {
	out := append([]string{}, pol.Required...)
	if !collectInert {
		return out
	}
	required := make(map[string]bool, len(out))
	for _, name := range out {
		required[name] = true
	}
	var extra []string
	for _, o := range pol.Oracles {
		if required[o.Name] || policy.KindStage(o.Kind) != policy.StageWorld {
			continue
		}
		extra = append(extra, o.Name)
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// Ladder reduces a compiled policy to the purchasable rungs, in the order
// LadderNames gives them.
func Ladder(pol policy.Policy, b Bounds, corpusDigest string, worlds int, collectInert bool) []Rung {
	gated := map[string]bool{}
	for _, g := range pol.Gates {
		gated[g.Oracle] = true
	}
	names := LadderNames(pol, collectInert)
	out := make([]Rung, 0, len(names))
	for _, name := range names {
		o, ok := pol.OracleByName(name)
		if !ok {
			// The v0 dialect declares no instances at all: its one required
			// rung is the supplied legacy oracle, selected by family exactly
			// as M0 selected it.
			out = append(out, Rung{Name: name, Family: name, HardGate: true})
			continue
		}
		corr := policy.KindCorrelation(o.Kind)
		if consumesCorpus(o.Kind) {
			corr.Corpus = corpusDigest
		}
		r := Rung{
			Name:        o.Name,
			Kind:        o.Kind,
			Family:      policy.KindFamily(o.Kind),
			Config:      o.Config,
			Provider:    o.Corpus.Provider,
			CorpusCases: o.Corpus.CasesMax,
			HardGate:    gated[o.Name],
			Corr:        corr,
		}
		r.Units = predictedUnits(o, b, worlds)
		r.prior = PriorClass(corr, r.Provider)
		out = append(out, r)
	}
	return out
}

// consumesCorpus reports whether a kind reads the race's pinned corpus, and
// therefore whether the corpus digest is part of its correlation
// coordinates. hypothesis-properties is deliberately absent: its cases come
// from the repository's own @given tests plus the policy-declared property
// module, not from the pinned corpus, so calling them the same corpus would
// make two different bodies of inputs look like one.
func consumesCorpus(kind string) bool {
	return kind == policy.KindCorpusObserve || kind == policy.KindCorpusDifferential
}

// predictedUnits is decision 7a's unit authority applied at PREDICTION time:
// the denominator comes from a control-plane measurement, the pinned corpus,
// or the policy's own ceiling. A unit count nobody in the control plane
// holds is 0 — honest absence — and the fixed term carries the estimate.
func predictedUnits(o policy.Oracle, b Bounds, worlds int) int64 {
	cases := b.CorpusCases
	if cases <= 0 {
		cases = o.Corpus.CasesMax
	}
	switch o.Kind {
	case policy.KindPytestCollect, policy.KindPytestSuite:
		// The BASE TREE's collected count (M1e decision 13), never the
		// candidate's own collected_total.
		return b.CollectedBase
	case policy.KindCorpusObserve, policy.KindProperties:
		return cases
	case policy.KindCorpusDifferential:
		return int64(worlds) * cases
	case policy.KindMutationDiff:
		return o.Mutation.MaxMutants
	default:
		// tree-guard scales by paths and command by nothing: neither has a
		// control-plane denominator before the run, so neither invents one.
		return 0
	}
}
