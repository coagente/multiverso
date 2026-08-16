package policy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

// mutationPolicy is the shipped fixture's shape: the ladder, plus the
// mutation rung with a survivor ceiling of zero — "no mutant of a line this
// patch wrote survived".
func mutationPolicy(spec object.MutationSpec) object.PolicyV1 {
	p := Default()
	p.Name = "mutation"
	p.Oracles = append(p.Oracles, object.OracleSpec{
		Name: "mutate", Kind: KindMutationDiff, Argv: []string{}, Args: []string{}, Mutation: spec,
	})
	p.HardGates = append(p.HardGates, object.GateSpec{
		Gate: GateMutationSurvivorsNotAbove, Oracle: "mutate", Basis: object.BasisConstruction,
	})
	return p
}

// Validation rule 20, in full. Every rejection names the field AND what is
// wrong with it: an authoring mistake must never be a silent no-op.
func TestValidateMutationRule20(t *testing.T) {
	for _, tt := range []struct {
		name  string
		spec  object.MutationSpec
		field string
		want  string
	}{
		{"unknown tool", object.MutationSpec{Tool: "stryker"},
			"oracles[3].mutation.tool", `unknown value "stryker"`},
		{"negative budget", object.MutationSpec{MaxMutants: -1},
			"oracles[3].mutation.max_mutants", "negative"},
		{"negative per-line cap", object.MutationSpec{MaxPerLine: -2},
			"oracles[3].mutation.max_per_line", "negative"},
		{"negative per-mutant timeout", object.MutationSpec{TimeoutPerMutant: -5},
			"oracles[3].mutation.timeout_per_mutant_ms", "negative"},
		{"unknown operator", object.MutationSpec{Operators: []string{"telepathy"}},
			"oracles[3].mutation.operators[0]", `unknown mutation operator "telepathy"`},
		{"unsorted operators", object.MutationSpec{Operators: []string{OpComparison, OpArithmetic}},
			"oracles[3].mutation.operators[1]", "must be sorted"},
		{"duplicate operator", object.MutationSpec{Operators: []string{OpArithmetic, OpArithmetic}},
			"oracles[3].mutation.operators[1]", "duplicate mutation operator"},
		// mutmut selects its own mutants, so an operator list against it is
		// refused BY NAME rather than silently ignored — decision 19's
		// "honest label on strictly weaker provenance", enforced at load.
		{"operators against mutmut", object.MutationSpec{Tool: MutationToolMutmut, Operators: []string{OpArithmetic}},
			"oracles[3].mutation.operators", "selects its own mutants"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(mutationPolicy(tt.spec))
			if err == nil {
				t.Fatalf("Validate accepted %+v", tt.spec)
			}
			found := false
			for _, p := range Problems(err) {
				if p.Field == tt.field && strings.Contains(p.Detail, tt.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("problems = %v, want %s: %s", Problems(err), tt.field, tt.want)
			}
		})
	}
}

// A mutation budget on a rung that runs no mutants reads as if the policy
// bought something it did not — the M1f rule-15 argument, reused.
func TestValidateMutationOnAForeignKind(t *testing.T) {
	p := Default()
	p.Oracles[2].Mutation = object.MutationSpec{MaxMutants: 5} // the suite oracle
	err := Validate(p)
	if err == nil {
		t.Fatal("Validate accepted a mutation budget on a pytest-suite oracle")
	}
	if got := Problems(err)[0]; got.Field != "oracles[2].mutation" ||
		!strings.Contains(got.Detail, "not \"pytest-suite\"") {
		t.Errorf("problem = %+v, want oracles[2].mutation naming the kind", got)
	}
}

// A mutation-diff instance measures suite adequacy, not coverage, and
// consumes no corpus. Both are refused by name.
func TestValidateMutationRefusesCoverageAndCorpus(t *testing.T) {
	p := mutationPolicy(object.MutationSpec{})
	p.Oracles[3].Coverage = true
	p.Oracles[3].Corpus = object.CorpusSpec{Provider: ProviderDeclared, File: "corpora/x.json"}
	err := Validate(p)
	if err == nil {
		t.Fatal("Validate accepted coverage and a corpus on mutation-diff")
	}
	fields := map[string]bool{}
	for _, pr := range Problems(err) {
		fields[pr.Field] = true
	}
	for _, want := range []string{"oracles[3].coverage", "oracles[3].corpus"} {
		if !fields[want] {
			t.Errorf("problems = %v, want one at %s", Problems(err), want)
		}
	}
}

// The defaults resolve exactly once, where they can be tested: a caller
// reading MaxMutants gets a CEILING, never a sentinel a second reader might
// take for "unbounded".
func TestResolvedMutationDefaults(t *testing.T) {
	got := ResolvedMutation(object.OracleSpec{Kind: KindMutationDiff})
	if got.Tool != MutationToolAuto || got.MaxMutants != DefaultMaxMutants || got.MaxPerLine != DefaultMaxPerLine {
		t.Errorf("resolved = %+v, want auto/%d/%d", got, DefaultMaxMutants, DefaultMaxPerLine)
	}
	if got.Operators == nil || len(got.Operators) != 0 {
		t.Errorf("operators = %v, want the empty list (the tool's default set)", got.Operators)
	}
	// Sorted and deduplicated, so an instance's identity in evidence does
	// not depend on the order an author happened to type.
	got = ResolvedMutation(object.OracleSpec{Kind: KindMutationDiff, Mutation: object.MutationSpec{
		Operators: []string{OpComparison, OpArithmetic, OpArithmetic},
	}})
	if !reflect.DeepEqual(got.Operators, []string{OpArithmetic, OpComparison}) {
		t.Errorf("operators = %v, want sorted and deduplicated", got.Operators)
	}
	// Any other kind resolves to the ZERO value: a pytest oracle carrying a
	// budget it will never spend would be a lie in the compiled policy.
	if got := ResolvedMutation(object.OracleSpec{Kind: KindPytestSuite}); !got.IsZero() {
		t.Errorf("a pytest-suite spec resolved to %+v, want the zero value", got)
	}
}

// The budget is part of the instance's IDENTITY IN EVIDENCE (decision 11):
// two instances that differ only in max_mutants produce differently-partial
// scores, so their receipts must not be interchangeable.
//
// And the other half, which is the compatibility claim: a spec that
// declares no mutation must digest EXACTLY as it did before M2a, or every
// historical receipt would stop matching its own gate's selector on replay.
func TestConfigDigestFoldsTheBudgetInWithoutMovingOldInstances(t *testing.T) {
	base := object.OracleSpec{Name: "mutate", Kind: KindMutationDiff, Argv: []string{}, Args: []string{}}
	twenty, err := ConfigDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	five := base
	five.Mutation = object.MutationSpec{MaxMutants: 5}
	digFive, err := ConfigDigest(five)
	if err != nil {
		t.Fatal(err)
	}
	if twenty == digFive {
		t.Error("two instances with different ceilings share a config digest: their receipts would be interchangeable evidence")
	}
	// The default resolves to 20, so declaring it explicitly is the SAME
	// instance — an author who spells out a default has not created a
	// second oracle.
	explicit := base
	explicit.Mutation = object.MutationSpec{MaxMutants: DefaultMaxMutants, MaxPerLine: DefaultMaxPerLine, Tool: MutationToolAuto}
	if dig, err := ConfigDigest(explicit); err != nil || dig != twenty {
		t.Errorf("explicit defaults digest to %s, want the resolved %s (err %v)", dig, twenty, err)
	}

	// The M1e-era instance, untouched. This is the assertion that keeps old
	// ledgers replaying: the compiled selector must still match receipts
	// recorded months ago.
	const m1eSuiteDigest = "mv0:"
	suite := object.OracleSpec{Name: "suite", Kind: KindPytestSuite, Argv: []string{}, Args: []string{}, Coverage: true}
	dig, err := ConfigDigest(suite)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dig, m1eSuiteDigest) {
		t.Fatalf("config digest = %q", dig)
	}
	// Adding the field to the struct must not have changed the bytes: a
	// spec whose mutation block is zero digests over exactly the M1e keys.
	want, _, err := object.Digest(map[string]any{
		"args": []string{}, "argv": DefaultPytestPrefix(), "coverage": true,
		"kind": KindPytestSuite, "reruns": 0, "timeout_ms": int64(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if dig != want {
		t.Errorf("a pre-M2a instance digests to %s, want the unchanged %s", dig, want)
	}
}

// Rule 16's property clause: a gate over a property oracle needs a
// harness-frozen module to read. "The repo has @given tests" cannot be
// checked at load, so the module is required exactly when a gate depends on
// the answer.
func TestValidatePropertyGateNeedsAModule(t *testing.T) {
	p := Default()
	p.Name = "properties"
	p.Oracles = append(p.Oracles, object.OracleSpec{
		Name: "props", Kind: KindProperties, Argv: []string{}, Args: []string{},
	})
	p.HardGates = append(p.HardGates, object.GateSpec{
		Gate: GatePropertiesPass, Oracle: "props", Basis: object.BasisConstruction,
	})
	err := Validate(p)
	if err == nil {
		t.Fatal("Validate accepted a property gate with no declared module")
	}
	found := false
	for _, pr := range Problems(err) {
		if pr.Field == "hard_gates[4].oracle" && strings.Contains(pr.Detail, "corpus.module") {
			found = true
		}
	}
	if !found {
		t.Errorf("problems = %v, want hard_gates[4].oracle naming corpus.module", Problems(err))
	}
	// With the module declared it validates, compiles, and the module is
	// harness-frozen (decision 14, corpus vector 16).
	p.Oracles[3].Corpus = object.CorpusSpec{Provider: ProviderHypothesis, Module: "props/mvo_props.py"}
	if err := Validate(p); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	compiled, err := Compile("mv0:test", p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := compiled.Paths.Class("props/mvo_props.py"); got != ClassHarness {
		t.Errorf("module class = %q, want %q", got, ClassHarness)
	}
}

// A property rung replaying a declared JSON corpus, or the repo's own node
// ids, would be a rung that runs no properties.
func TestValidatePropertyProviderIsClosed(t *testing.T) {
	p := Default()
	p.Oracles = append(p.Oracles, object.OracleSpec{
		Name: "props", Kind: KindProperties, Argv: []string{}, Args: []string{},
		Corpus: object.CorpusSpec{Provider: ProviderDeclared, File: "corpora/x.json"},
	})
	err := Validate(p)
	if err == nil {
		t.Fatal("Validate accepted a declared-corpus provider on the property rung")
	}
	if got := Problems(err)[0]; got.Field != "oracles[3].corpus.provider" {
		t.Errorf("problem = %+v, want oracles[3].corpus.provider", got)
	}
}

// The three new gate predicates against the full table: pass, fail, metric
// absent, receipt absent, wrong basis, status error. Absence and
// inconclusiveness FAIL — never a fabricated zero, never a pass.
func TestM2aGatePredicates(t *testing.T) {
	receipt := func(status string, metrics map[string]int64) *object.Receipt {
		return &object.Receipt{
			Result:    object.Result{Status: status, Metrics: metrics, Tools: map[string]string{}},
			Freshness: object.Freshness{Basis: object.BasisConstruction},
		}
	}
	for _, tt := range []struct {
		name      string
		gate      Gate
		rec       *object.Receipt
		wantPass  bool
		wantWhyIn string
	}{
		{"properties-pass, clean", Gate{Predicate: GatePropertiesPass, Basis: object.BasisConstruction},
			receipt("pass", map[string]int64{MetricPropertiesFailed: 0, MetricPropertiesErrored: 0}), true, ""},
		{"properties-pass, a failure", Gate{Predicate: GatePropertiesPass, Basis: object.BasisConstruction},
			receipt("fail", map[string]int64{MetricPropertiesFailed: 2, MetricPropertiesErrored: 0}), false,
			"properties_failed=2"},
		{"properties-pass, metric absent", Gate{Predicate: GatePropertiesPass, Basis: object.BasisConstruction},
			receipt("pass", map[string]int64{MetricPropertiesFailed: 0}), false,
			"properties_errored absent"},
		{"properties-pass, no receipt", Gate{Predicate: GatePropertiesPass, Basis: object.BasisConstruction},
			nil, false, ReasonNoReceipt},
		{"properties-pass, errored run", Gate{Predicate: GatePropertiesPass, Basis: object.BasisConstruction},
			receipt("error", map[string]int64{MetricPropertiesFailed: 0, MetricPropertiesErrored: 0}), false,
			"status=error"},
		{"property-cases-at-least, met", Gate{Predicate: GatePropertyCasesAtLeast, Basis: object.BasisConstruction, Threshold: 50},
			receipt("pass", map[string]int64{MetricPropertyCasesTotal: 50}), true, ""},
		{"property-cases-at-least, the search collapsed", Gate{Predicate: GatePropertyCasesAtLeast, Basis: object.BasisConstruction, Threshold: 50},
			receipt("pass", map[string]int64{MetricPropertyCasesTotal: 3}), false,
			"property_cases_total=3 (want >= 50)"},
		{"property-cases-at-least, the JSONL fallback", Gate{Predicate: GatePropertyCasesAtLeast, Basis: object.BasisConstruction, Threshold: 1},
			receipt("pass", map[string]int64{MetricPropertiesTotal: 3}), false,
			"property_cases_total absent"},
		{"mutation survivors, none", Gate{Predicate: GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction},
			receipt("pass", map[string]int64{MetricMutantsSurvived: 0, MetricMutantsTimeout: 0}), true, ""},
		{"mutation survivors, one too many", Gate{Predicate: GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction},
			receipt("pass", map[string]int64{MetricMutantsSurvived: 1, MetricMutantsTimeout: 0}), false,
			"mutants_survived=1 mutants_timeout=0 (want survived+timeout <= 0)"},
		{"mutation survivors, tolerated", Gate{Predicate: GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction, Threshold: 2},
			receipt("pass", map[string]int64{MetricMutantsSurvived: 2, MetricMutantsTimeout: 0}), true, ""},
		// A mutant that HUNG is a mutant the tests did not kill, and it is
		// counted in the same numerator. Without this the cheapest escape
		// from the only mutation gate M2a ships was to make the mutated code
		// slow: the survivor bucket stayed empty, the score read 10000 bp,
		// and the gate passed beside a mutant nothing killed.
		{"mutation survivors, a hang is not a kill", Gate{Predicate: GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction},
			receipt("pass", map[string]int64{MetricMutantsSurvived: 0, MetricMutantsTimeout: 1,
				MetricMutantsKilled: 2, MetricMutationScoreBP: 10000}), false,
			"mutants_survived=0 mutants_timeout=1 (want survived+timeout <= 0)"},
		{"mutation survivors, absent", Gate{Predicate: GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction},
			receipt("pass", map[string]int64{MetricMutantsTested: 0}), false,
			"mutants_survived absent"},
		{"mutation timeouts, absent", Gate{Predicate: GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction},
			receipt("pass", map[string]int64{MetricMutantsSurvived: 0}), false,
			"mutants_timeout absent"},
		{"mutation survivors, weaker basis", Gate{Predicate: GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction},
			&object.Receipt{
				Result:    object.Result{Status: "pass", Metrics: map[string]int64{MetricMutantsSurvived: 0, MetricMutantsTimeout: 0}},
				Freshness: object.Freshness{Basis: object.BasisProbabilistic},
			}, false, "basis=probabilistic"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := tt.gate.Eval(tt.rec)
			if ok != tt.wantPass {
				t.Fatalf("pass = %v (%q), want %v", ok, reason, tt.wantPass)
			}
			if !strings.Contains(reason, tt.wantWhyIn) {
				t.Errorf("reason = %q, want it to contain %q", reason, tt.wantWhyIn)
			}
		})
	}
}

// The two new rows of the profile table, which are what let M2b PRICE
// these rungs: what bounds each one, what it scales by, and how correlated
// its evidence is with everything already bought. (Totality over every kind
// is asserted next door, in TestProfileTableIsTotal.)
func TestProfileTableCoversTheNewRungs(t *testing.T) {
	mut, ok := KindProfile(KindMutationDiff)
	if !ok {
		t.Fatalf("kind %q has no declared profile", KindMutationDiff)
	}
	if mut.Cap != "mutation.max_mutants" {
		t.Errorf("mutation-diff cap = %q, want the policy field that bounds it", mut.Cap)
	}
	if mut.Unit != UnitMutants || KindUnit(KindMutationDiff) != UnitMutants {
		t.Errorf("mutation-diff unit = %q, want %q", mut.Unit, UnitMutants)
	}
	if mut.Discriminate != DiscriminateOrdinal {
		t.Errorf("mutation-diff discriminates %q, want ordinal (recorded, not ranked)", mut.Discriminate)
	}
	// The pairing that makes mutation-diff × pytest-suite genuinely
	// independent: control-plane-chosen inputs, candidate-process
	// execution, a signal no other rung reads.
	if mut.Corr.Signal != SignalSuiteAdequacy || mut.Corr.Generator != GeneratorControlPlane ||
		mut.Corr.Executor != ExecutorCandidateProcess {
		t.Errorf("mutation-diff correlation = %+v", mut.Corr)
	}
	prop, _ := KindProfile(KindProperties)
	if prop.Corr.Generator != GeneratorRepoPolicy {
		t.Errorf("hypothesis-properties generator = %q, want %q: the properties come from the repo plus the policy",
			prop.Corr.Generator, GeneratorRepoPolicy)
	}
	if prop.Corr.Signal != SignalValueBehavior {
		t.Errorf("hypothesis-properties signal = %q, want %q", prop.Corr.Signal, SignalValueBehavior)
	}
}

// ZERO new ranking keys (decision 8). Every candidate-comparing key
// derivable from these rungs is signed by something the candidate chooses:
// mutation_score_bp is a ratio whose denominator is the candidate's own
// diff, and mutants_survived as a KEY punishes the larger honest patch. The
// metrics all ship; the keys do not, and this test is the tripwire on the
// next person who wants one.
func TestNoRankingKeyReadsTheNewMetrics(t *testing.T) {
	forbidden := map[string]bool{
		MetricMutationScoreBP: true, MetricMutantsSurvived: true, MetricMutantsKilled: true,
		MetricPropertyCasesTotal: true, MetricPropertiesPassed: true,
	}
	for _, name := range KnownKeys() {
		if m := keyDefs[name].metric; forbidden[m] {
			t.Errorf("ranking key %q reads %q, which M2a decision 8 refuses to rank by", name, m)
		}
	}
}

// The shipped default does not acquire either rung. A default gate that
// fails when a tool is missing would trade honesty for brittleness at the
// moment a new user meets the tool (decision 25).
func TestDefaultDeclaresNeitherNewRung(t *testing.T) {
	for _, o := range Default().Oracles {
		if o.Kind == KindMutationDiff || o.Kind == KindProperties {
			t.Errorf("the shipped default declares %q, which must stay opt-in", o.Kind)
		}
	}
	for _, g := range Default().HardGates {
		switch g.Gate {
		case GateMutationSurvivorsNotAbove, GatePropertiesPass, GatePropertyCasesAtLeast:
			t.Errorf("the shipped default gates on %q, which must stay opt-in", g.Gate)
		}
	}
}
