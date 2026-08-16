package schedule

// The research mode's LADDER (M2b decision 11).
//
// `pol.Required` is derived from what a gate, ranking key, invariant or
// escalation rule READS. So a rung that is decision-inert BECAUSE NOTHING
// READS IT is, by construction, absent from it — and those are exactly the
// rungs M2a ships unranked, whose every derivable ranking key is wrong-signed
// and which M2d must correlate against ground truth before anyone ranks by
// them. Without the extension `--collect-inert` could only ever re-buy rungs
// the policy already reads, which is not a research mode.

import (
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// inertPolicy declares two rungs nothing reads: a mutation-diff and a
// hypothesis-properties instance with no gate, no key and no requirement, and
// one COHORT reducer that must NOT be added even so.
func inertPolicy(t *testing.T) policy.Policy {
	t.Helper()
	p := object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "inert",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{}, Args: []string{}},
			{Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{}},
			{Name: "mutate", Kind: policy.KindMutationDiff, Argv: []string{}, Args: []string{},
				Mutation: object.MutationSpec{MaxMutants: 3, MaxPerLine: 2, Tool: "auto"}},
			{Name: "observe", Kind: policy.KindCorpusObserve, Argv: []string{}, Args: []string{},
				Corpus: object.CorpusSpec{File: "corpora/c.json", Provider: "declared"}},
			{Name: "diff", Kind: policy.KindCorpusDifferential, Argv: []string{}, Args: []string{}},
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{}},
		},
		HardGates: []object.GateSpec{
			{Gate: policy.GatePathsUnmodified, Oracle: "guard", Basis: object.BasisConstruction},
			{Gate: policy.GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{policy.KeyGatePass, policy.KeyTestsPassedDesc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}},
		Paths:      object.PathSpec{Protected: []string{"tests/**"}, Harness: []string{"conftest.py"}},
	}
	b, err := object.Canonical(p)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	pol, err := policy.Decode(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return pol
}

func TestLadderNamesAddsOnlyResearchRungsAndOnlyInResearchMode(t *testing.T) {
	pol := inertPolicy(t)

	normal := LadderNames(pol, false)
	if len(normal) != len(pol.Required) {
		t.Fatalf("the decision ladder is %v, want exactly pol.Required %v", normal, pol.Required)
	}
	for i, name := range pol.Required {
		if normal[i] != name {
			t.Fatalf("the decision ladder reordered the policy: %v vs %v", normal, pol.Required)
		}
	}
	for _, name := range normal {
		if name == "mutate" || name == "observe" {
			t.Fatalf("a rung nothing reads is in the decision ladder: %v", normal)
		}
	}

	inert := LadderNames(pol, true)
	// The policy's gate order is the fidelity ladder and the scheduler may
	// not reorder it (decision 2), so the research rungs go LAST.
	for i := range normal {
		if inert[i] != normal[i] {
			t.Fatalf("research mode reordered the gate ladder: %v vs %v", inert, normal)
		}
	}
	extra := map[string]bool{}
	for _, name := range inert[len(normal):] {
		extra[name] = true
	}
	for _, want := range []string{"mutate", "observe"} {
		if !extra[want] {
			t.Errorf("--collect-inert did not add the decision-inert rung %q: %v", want, inert)
		}
	}
	// THE COHORT REDUCER IS NOT ADDED. A cohort rung is a dependent purchase
	// whose denominator is the membership at close time (M2a amendment 29);
	// adding one no rule reads would change the cohort's shape to record
	// evidence nothing consumes.
	if extra["diff"] {
		t.Errorf("--collect-inert added the cohort reducer %q: %v", "diff", inert)
	}
}

// The rungs the ALLOCATOR prices are the rungs LadderNames names, so the
// scheduler cannot price a purchase the orchestrator has no rung for — and a
// research rung is marked as not hard-gated, which is what makes its
// flip 0 and its row `research` rather than `decision`.
func TestLadderPricesTheResearchRungs(t *testing.T) {
	pol := inertPolicy(t)
	rungs := Ladder(pol, Bounds{CollectedBase: 8}, "mv0:corpus", 2, true)
	byName := map[string]Rung{}
	for _, r := range rungs {
		byName[r.Name] = r
	}
	for _, name := range []string{"guard", "collect", "suite", "mutate", "observe"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("research ladder is missing rung %q: %v", name, LadderNames(pol, true))
		}
	}
	for _, name := range []string{"mutate", "observe"} {
		if byName[name].HardGate {
			t.Errorf("rung %q is marked hard-gated, but no gate reads it", name)
		}
	}
	for _, name := range []string{"guard", "collect", "suite"} {
		if !byName[name].HardGate {
			t.Errorf("rung %q is not marked hard-gated, but a gate reads it", name)
		}
	}
}
