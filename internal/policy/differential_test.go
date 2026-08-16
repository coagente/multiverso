package policy

// M2a: the corpus plane's policy surface — validation rules 16-19, 21 and
// 22, gate scope, the profile table's totality, and the compatibility claim
// the block makes up front and must prove.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

// diffPolicy is a minimal, VALID differential policy the rejection table
// mutates one field at a time.
func diffPolicy() object.PolicyV1 {
	return object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "differential",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: KindPytestCollect, Argv: []string{}, Args: []string{}},
			{Name: "observe", Kind: KindCorpusObserve, Argv: []string{}, Args: []string{},
				Corpus: object.CorpusSpec{Provider: ProviderDeclared, File: "corpora/clamp-nan.json"}},
			{Name: "diff", Kind: KindCorpusDifferential, Argv: []string{}, Args: []string{}},
			// The guard is LAST so the mutation cases below keep their
			// indices, and it is not decoration: corpus.file compiles into
			// paths.harness (decision 14), and rule 24 refuses a policy that
			// freezes a path no paths-unmodified gate reads.
			{Name: "guard", Kind: KindTreeGuard, Argv: []string{}, Args: []string{}},
		},
		HardGates: []object.GateSpec{
			{Gate: GatePathsUnmodified, Oracle: "guard", Basis: object.BasisConstruction},
			{Gate: GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			// Scope race, and rule 23 is why: the corpus this gate reads is
			// materialized by the race's phase 0, and admission has no
			// phase 0. A both-scope gate here is an admission that can
			// never succeed.
			{Gate: GateCorpusComplete, Oracle: "observe", Basis: object.BasisConstruction, Scope: ScopeRace},
		},
		Ranking:    []string{KeyGatePass},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}, OnBehavioralSplit: 2},
		Paths:      object.PathSpec{Protected: []string{}, Harness: []string{}},
		Invariants: []object.InvariantSpec{},
	}
}

func TestDifferentialPolicyValidates(t *testing.T) {
	if err := Validate(diffPolicy()); err != nil {
		t.Fatalf("the shipped differential shape does not validate: %v", err)
	}
}

func TestDifferentialValidationRejections(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*object.PolicyV1)
		want string
	}{
		{
			// Rule 16.
			name: "unknown corpus provider",
			mut:  func(p *object.PolicyV1) { p.Oracles[1].Corpus.Provider = "llm" },
			want: `unknown value "llm" (known: declared, hypothesis, repo-suite)`,
		},
		{
			name: "declared provider with no file",
			mut:  func(p *object.PolicyV1) { p.Oracles[1].Corpus.File = "" },
			want: `provider "declared" requires a corpus file`,
		},
		{
			name: "hypothesis provider with no module",
			mut: func(p *object.PolicyV1) {
				p.Oracles[1].Corpus = object.CorpusSpec{Provider: ProviderHypothesis}
			},
			want: `provider "hypothesis" requires a property module`,
		},
		{
			name: "a seed that is not 32 hex",
			mut:  func(p *object.PolicyV1) { p.Oracles[1].Corpus.Seed = "abc" },
			want: `seed "abc" is not 32 lowercase hex digits`,
		},
		{
			name: "a negative cases_max",
			mut:  func(p *object.PolicyV1) { p.Oracles[1].Corpus.CasesMax = -1 },
			want: "cases_max -1 is negative",
		},
		{
			// Rule 17: the reducer runs no process.
			name: "a differential that declares a timeout",
			mut:  func(p *object.PolicyV1) { p.Oracles[2].TimeoutMS = 1000 },
			want: `kind "corpus-differential" runs no process: timeout_ms must be unset`,
		},
		{
			name: "a differential that declares a corpus of its own",
			mut: func(p *object.PolicyV1) {
				p.Oracles[2].Corpus = object.CorpusSpec{Provider: ProviderDeclared, File: "x.json"}
			},
			want: `kind "corpus-differential" runs no process: corpus must be unset`,
		},
		{
			// Rule 18: two observers make "the cohort" ambiguous.
			name: "two corpus observers",
			mut: func(p *object.PolicyV1) {
				p.Oracles = append(p.Oracles, object.OracleSpec{
					Name: "observe2", Kind: KindCorpusObserve, Argv: []string{}, Args: []string{},
					Corpus: object.CorpusSpec{Provider: ProviderDeclared, File: "corpora/other.json"},
				})
			},
			want: `requires exactly one declared "corpus-observe" oracle (got 2)`,
		},
		{
			// Rule 18: zero observers make the reducer a function of nothing.
			name: "a differential with no observer",
			mut: func(p *object.PolicyV1) {
				p.Oracles = []object.OracleSpec{p.Oracles[0], p.Oracles[2]}
				p.HardGates = p.HardGates[:1]
			},
			want: `requires exactly one declared "corpus-observe" oracle (got 0)`,
		},
		{
			// Rule 19: a cohort of one is not a comparison.
			name: "a cohort gate without scope race",
			mut: func(p *object.PolicyV1) {
				p.HardGates = append(p.HardGates, object.GateSpec{
					Gate: GateDifferentialCohortAtLeast, Oracle: "diff",
					Basis: object.BasisConstruction, Threshold: 2,
				})
			},
			want: `is a cohort-stage gate and must declare scope "race"`,
		},
		{
			name: "a cohort gate whose threshold accepts a cohort of one",
			mut: func(p *object.PolicyV1) {
				p.HardGates = append(p.HardGates, object.GateSpec{
					Gate: GateDifferentialCohortAtLeast, Oracle: "diff", Scope: ScopeRace,
					Basis: object.BasisConstruction, Threshold: 1,
				})
			},
			want: "threshold 1 is below 2 (a cohort of one is not a comparison)",
		},
		{
			// Rule 23: decision 21's argument one rung below the cohort
			// stage. `mvo admit` has no phase 0, so a landing-scope
			// corpus-complete gate aborts admission as machinery forever.
			name: "a corpus gate without scope race",
			mut:  func(p *object.PolicyV1) { p.HardGates[2].Scope = ScopeBoth },
			want: `reads a corpus materialized by the race's phase 0 and must declare scope "race"`,
		},
		{
			name: "a corpus gate scoped to landing",
			mut:  func(p *object.PolicyV1) { p.HardGates[2].Scope = ScopeLanding },
			want: `admission has no phase 0, so the corpus it replays does not exist there`,
		},
		{
			name: "an unknown gate scope",
			mut:  func(p *object.PolicyV1) { p.HardGates[0].Scope = "sometimes" },
			want: `unknown value "sometimes" (known: both, landing, race)`,
		},
		{
			// Rule 24, the converse of rule 12. Decision 14's entire defence
			// for the corpus file and the property module is that they
			// compile into paths.harness and are "closed by the gate that
			// already closes conftest.py" — and nothing required that gate to
			// exist. The shipped differential fixture was exactly this shape:
			// `mvo policy validate` printed "paths (frozen against the
			// candidate)" over a freeze the policy could not keep, and a
			// candidate that rewrote the corpus passed every hard gate.
			name: "a corpus file frozen by nothing",
			mut: func(p *object.PolicyV1) {
				p.HardGates = p.HardGates[1:] // drop the paths-unmodified gate
			},
			want: "corpora/clamp-nan.json compiles into paths.harness but no paths-unmodified gate evaluates it",
		},
		{
			// The same rule over a PROPERTY MODULE, where the hole is total
			// rather than small: a vacuous @given module passes
			// properties-pass while asserting nothing, which is the whole of
			// what corpus vector 16 claims to close.
			name: "a property module frozen by nothing",
			mut: func(p *object.PolicyV1) {
				p.HardGates = p.HardGates[1:]
				p.Oracles[1].Corpus = object.CorpusSpec{
					Provider: ProviderHypothesis, Module: "props/mvo_props.py",
				}
				p.Oracles[1].Kind = KindProperties
				p.HardGates = append(p.HardGates, object.GateSpec{
					Gate: GatePropertiesPass, Oracle: "observe", Basis: object.BasisConstruction,
				})
			},
			want: "props/mvo_props.py compiles into paths.harness but no paths-unmodified gate evaluates it",
		},
		{
			// Rule 21: unreachable = load error.
			name: "on_behavioral_split with no reducer",
			mut: func(p *object.PolicyV1) {
				p.Oracles = p.Oracles[:2]
			},
			want: `requires a declared "corpus-differential" oracle: a rule that can never fire is an authoring bug`,
		},
		{
			// Rule 22: a corpus the candidate may ADD is a corpus the
			// candidate can agree with itself on.
			name: "a corpus file paths.protected would swallow",
			mut: func(p *object.PolicyV1) {
				p.Paths.Protected = []string{"corpora/**"}
			},
			want: "which allows additions: it must be harness-frozen, not protected",
		},
		{
			name: "a corpus declared on a kind that consumes none",
			mut: func(p *object.PolicyV1) {
				p.Oracles[0].Corpus = object.CorpusSpec{Provider: ProviderDeclared, File: "x.json"}
			},
			want: `kind "pytest-collect" consumes no corpus`,
		},
		{
			// SpecEmits, instance level: a gate that can only ever fail on
			// an absent metric is an authoring bug caught at load.
			name: "corpus-complete on an observer with no provider",
			mut: func(p *object.PolicyV1) {
				p.Oracles[1].Corpus = object.CorpusSpec{}
				p.Oracles = p.Oracles[:2]
				p.Escalation.OnBehavioralSplit = 0
			},
			want: `gate "corpus-complete" needs metric "corpus_cases_observed"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := diffPolicy()
			tc.mut(&p)
			err := Validate(p)
			if err == nil {
				t.Fatalf("accepted a policy it must refuse (want %q)", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("problems %v\ndo not contain %q", Problems(err), tc.want)
			}
		})
	}
}

// Gate scope compiles once, here, where it can be tested: "" is "both",
// which is what every M1e/M1f gate meant.
func TestGateScopeCompilesAndSelects(t *testing.T) {
	p := diffPolicy()
	p.HardGates = append(p.HardGates, object.GateSpec{
		Gate: GateDifferentialCohortAtLeast, Oracle: "diff", Scope: ScopeRace,
		Basis: object.BasisConstruction, Threshold: 2,
	})
	if err := Validate(p); err != nil {
		t.Fatalf("validate: %v", err)
	}
	pol, err := Compile("mv0:test", p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, g := range pol.Gates {
		if g.Scope == "" {
			t.Errorf("gate %s compiled with an empty scope; the sentinel must resolve once", g.Label)
		}
	}
	race := pol.GatesAt(ScopeRace)
	landing := pol.GatesAt(ScopeLanding)
	if len(race) != 4 {
		t.Errorf("race gates = %d, want all four", len(race))
	}
	if len(landing) != 2 {
		t.Errorf("landing gates = %d, want the two both-scope gates", len(landing))
	}
	// TWO gates are excluded at admission, and the pair is the point: the
	// cohort gate because admission has one subject (rule 19), and the
	// corpus gate because admission has no phase 0 to materialize what it
	// replays (rule 23). Both would otherwise be admissions that can never
	// succeed — the sealed.json failure, twice.
	skipped := pol.GatesNotAt(ScopeLanding)
	got := []string{}
	for _, g := range skipped {
		got = append(got, g.Label)
	}
	sort.Strings(got)
	want := []string{GateCorpusComplete + "@observe", GateDifferentialCohortAtLeast + "@diff"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("gates excluded at admission = %v, want %v", got, want)
	}
	// A landing gate set weaker than the race's is a legitimate policy
	// choice and must never be an INVISIBLE one: the excluded set is
	// enumerable so `mvo admit` can name it.
}

// THE COMPATIBILITY CLAIM, under test rather than in prose (M2a decision
// 25): the shipped default policy does not move, and an M1f-era policy file
// still decodes to M1f-identical gates, keys, escalation and path set with
// scope "both" on every gate.
func TestM2aDoesNotMoveTheShippedDefault(t *testing.T) {
	dig, _, err := object.Digest(Default())
	if err != nil {
		t.Fatal(err)
	}
	const m1f = "mv0:f207c3fad59d0fc973e5f342ac54d8b1bc9e5c6cae2a2cff0b33477ddee3c543"
	if dig != m1f {
		t.Fatalf("the shipped default digest moved to %s, want %s — every new rung is opt-in and the default must be byte-for-byte M1f's", dig, m1f)
	}
	// The mechanism that keeps it true: the additive policy fields are
	// omitted when unset, so a policy that never heard of a corpus
	// serializes exactly as it did before.
	b, err := object.Canonical(Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{`"corpus"`, `"scope"`, `"on_behavioral_split"`} {
		if strings.Contains(string(b), absent) {
			t.Errorf("the default policy serializes %s; an additive field that always appears mints a new digest for every existing policy", absent)
		}
	}
}

func TestM1fEraPolicyStillCompiles(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "toyrepo", "policies", "no-paths.json"))
	if err != nil {
		t.Fatal(err)
	}
	pol, err := Decode(raw)
	if err != nil {
		t.Fatalf("an M1f-era policy no longer decodes: %v", err)
	}
	if len(pol.Gates) != 3 {
		t.Fatalf("gates = %d, want 3", len(pol.Gates))
	}
	for _, g := range pol.Gates {
		if g.Scope != ScopeBoth {
			t.Errorf("gate %s compiled with scope %q, want %q — the M1f meaning, unchanged", g.Label, g.Scope, ScopeBoth)
		}
	}
	if pol.Esc.OnBehavioralSplit != 0 {
		t.Errorf("on_behavioral_split = %d on a policy that cannot name it", pol.Esc.OnBehavioralSplit)
	}
	if _, ok := pol.CorpusOracle(); ok {
		t.Error("an M1f-era policy resolved a corpus oracle")
	}
	if _, ok := pol.DifferentialOracle(); ok {
		t.Error("an M1f-era policy resolved a differential oracle")
	}
	// The config digests a gate SELECTS on must not move either, or every
	// receipt recorded before M2a would stop matching its own gate.
	suite, ok := pol.OracleByName("suite")
	if !ok {
		t.Fatal("no suite oracle")
	}
	m1fCfg, err := ConfigDigest(object.OracleSpec{
		Name: "suite", Kind: KindPytestSuite, Argv: []string{}, Args: []string{}, Coverage: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if suite.Config != m1fCfg {
		t.Errorf("the suite instance's config digest moved to %s; replay recomputes selectors from the pinned policy and would stop matching every historical receipt", suite.Config)
	}
}

// The shipped fixture policy is the DoS-resistant shape of corpus vector
// 18: escalation, not a hard cohort gate. With a hard gate and N=2 a single
// silenced world denies the comparison to the whole race.
func TestDifferentialFixturePolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "toyrepo", "policies", "differential.json"))
	if err != nil {
		t.Fatal(err)
	}
	pol, err := Decode(raw)
	if err != nil {
		t.Fatalf("the shipped differential fixture does not load: %v", err)
	}
	if pol.Esc.OnBehavioralSplit != 2 {
		t.Errorf("on_behavioral_split = %d, want 2", pol.Esc.OnBehavioralSplit)
	}
	for _, g := range pol.Gates {
		if g.Predicate == GateDifferentialCohortAtLeast {
			t.Error("the shipped fixture declares a differential HARD GATE: with N=2 one silenced world denies the comparison to the whole race (corpus vector 18)")
		}
	}
	obs, ok := pol.CorpusOracle()
	if !ok {
		t.Fatal("no corpus oracle")
	}
	if obs.Corpus.CasesMax != DefaultCasesMax {
		t.Errorf("cases_max = %d, want the resolved default %d", obs.Corpus.CasesMax, DefaultCasesMax)
	}
	// Decision 14: the declared corpus file JOINS the harness set, so a
	// candidate that edits it dies at rung O-1 before any Python runs.
	if pol.Paths.Class("corpora/clamp-nan.json") != ClassHarness {
		t.Error("the declared corpus file is not harness-frozen; a corpus a candidate can edit is a corpus a candidate can agree with itself on")
	}
	if _, ok := pol.DifferentialOracle(); !ok {
		t.Error("no differential oracle")
	}
}

// The profile table is TOTAL over the registry's kinds. A scheduler that
// meets a kind with no declared profile has to guess, and guessing is what
// the table exists to remove.
func TestProfileTableIsTotal(t *testing.T) {
	for _, kind := range KnownKinds() {
		p, ok := KindProfile(kind)
		if !ok {
			t.Errorf("kind %q has no declared profile", kind)
			continue
		}
		if p.Stage != StageWorld && p.Stage != StageCohort {
			t.Errorf("kind %q declares stage %q", kind, p.Stage)
		}
		if p.Corr.Executor == "" {
			t.Errorf("kind %q declares no executor; who ran it bounds the value of everything it reports", kind)
		}
	}
	if KindStage(KindCorpusDifferential) != StageCohort {
		t.Error("the differential is not cohort-staged")
	}
	if KindStage(KindCorpusObserve) != StageWorld {
		t.Error("the observer is not world-staged")
	}
	// The one row the discount rule turns on: the reducer is executed by
	// the CONTROL PLANE over base-tree-authored inputs, which is what makes
	// corpus-differential × pytest-suite a genuinely independent pair.
	corr := KindCorrelation(KindCorpusDifferential)
	if corr.Executor != ExecutorControlPlane || corr.Generator != GeneratorBaseTree {
		t.Errorf("differential correlation = %+v", corr)
	}
	if KindCorrelation(KindCorpusObserve).Executor != ExecutorCandidateProcess {
		t.Error("the observer claims not to run in the candidate's process")
	}
}

// A corpus-bearing instance gets its OWN identity in evidence, and an
// instance that declares none keeps exactly the digest it had before M2a.
func TestCorpusJoinsTheConfigDigestOnlyWhenDeclared(t *testing.T) {
	plain := object.OracleSpec{Name: "suite", Kind: KindPytestSuite, Argv: []string{}, Args: []string{}}
	withCorpus := plain
	withCorpus.Kind = KindCorpusObserve
	withCorpus.Corpus = object.CorpusSpec{Provider: ProviderDeclared, File: "a.json"}
	other := withCorpus
	other.Corpus.File = "b.json"

	a, err := ConfigDigest(withCorpus)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ConfigDigest(other)
	if a == b {
		t.Error("two observers over different corpora share a config digest; a gate could not tell their receipts apart")
	}
	// And the encoding that keeps replay working: the key is absent when
	// no corpus is declared.
	raw, err := json.Marshal(ResolvedCorpus(plain))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Errorf("an undeclared corpus resolves to %s, want {}", raw)
	}
}
