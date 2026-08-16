package policy

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/object"
)

func canonical(t *testing.T, v any) []byte {
	t.Helper()
	b, err := object.Canonical(v)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	return b
}

func decode(t *testing.T, v any) Policy {
	t.Helper()
	pol, err := Decode(canonical(t, v))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return pol
}

// The shipped default is pinned byte-for-byte: `mvo init` writes these
// bytes, and every intent created afterwards pins THIS digest.
func TestDefaultGolden(t *testing.T) {
	const wantCanon = `{"escalation":{"min_candidates_passing":0,"on_all_worlds_failed_machinery":true,"on_invariant_violation":true,"on_ranking_tie":true,"require_evidence":[]},` +
		`"evidence":{"crosscheck":"require","plugin_autoload":"off","regime":"auto"},` +
		`"hard_gates":[{"basis":"construction","gate":"paths-unmodified","oracle":"guard","threshold":0},` +
		`{"basis":"construction","gate":"collect-nonempty","oracle":"collect","threshold":0},` +
		`{"basis":"construction","gate":"collected-not-below","oracle":"collect","threshold":0},` +
		`{"basis":"construction","gate":"status-pass","oracle":"suite","threshold":0}],` +
		`"invariants":[{"name":"collect-equals-suite-total","oracles":{"collect":"collect","suite":"suite"}}],` +
		`"name":"default",` +
		`"oracles":[{"args":[],"argv":[],"coverage":true,"kind":"pytest-collect","name":"collect","reruns":0,"timeout_ms":0},` +
		`{"args":[],"argv":[],"coverage":false,"kind":"tree-guard","name":"guard","reruns":0,"timeout_ms":0},` +
		`{"args":[],"argv":[],"coverage":true,"kind":"pytest-suite","name":"suite","reruns":0,"timeout_ms":0}],` +
		`"paths":{"harness":["**/*.dist-info/**","**/*.egg-info/**","**/*.pth","**/.gitignore","**/conftest.py","**/sitecustomize.py","pyproject.toml","pytest.ini","setup.cfg","tox.ini"],` +
		`"protected":["**/*_test.py","**/test_*.py","test/**","tests/**"],"protected_additions":"allow"},` +
		`"ranking":["gate_pass","tests_passed_desc"],` +
		`"schema":"multiverso.dev/policy/v1"}`
	const wantDig = "mv0:f207c3fad59d0fc973e5f342ac54d8b1bc9e5c6cae2a2cff0b33477ddee3c543"

	dig, canon, err := object.Digest(Default())
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if string(canon) != wantCanon {
		t.Errorf("canonical =\n %s\nwant\n %s", canon, wantCanon)
	}
	if dig != wantDig {
		t.Errorf("digest = %s, want %s", dig, wantDig)
	}

	pol := decode(t, Default())
	if pol.Dialect != DialectV1 || pol.Name != "default" {
		t.Errorf("dialect/name = %s/%s, want %s/default", pol.Dialect, pol.Name, DialectV1)
	}
	// The guard is rung O-1: a candidate that edited a test or added a
	// conftest.py never pays for a collect or a suite run.
	wantGates := []string{"paths-unmodified@guard", "collect-nonempty@collect",
		"collected-not-below@collect", "status-pass@suite"}
	if got := pol.GateLabels(); !reflect.DeepEqual(got, wantGates) {
		t.Errorf("gates = %v, want %v", got, wantGates)
	}
	// wall_ms_asc is GONE from the shipped ranking (M1f decision 20): the
	// study measured a cheat winning 6 of 10 identical races on ~100 ms of
	// jitter, with a signed rationale naming the stopwatch as decisive.
	wantKeys := []string{KeyGatePass, KeyTestsPassedDesc, KeyWorldDigestAsc}
	if got := pol.KeyNames(); !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("effective keys = %v, want %v", got, wantKeys)
	}
	// Ladder order, and no declared-but-unrequired oracle: evidence waste
	// is a measured metric, so the race runs exactly what the policy needs.
	if got := pol.Required; !reflect.DeepEqual(got, []string{"guard", "collect", "suite"}) {
		t.Errorf("required = %v, want [guard collect suite]", got)
	}
	if !pol.Esc.OnAllWorldsFailedMachinery || !pol.Esc.OnRankingTie ||
		!pol.Esc.OnInvariantViolation ||
		pol.Esc.MinCandidatesPassing != 0 || len(pol.Esc.RequireEvidence) != 0 {
		t.Errorf("escalation = %+v, want machinery+tie+invariant", pol.Esc)
	}
	if len(pol.Invariants) != 1 || pol.Invariants[0].Name != InvariantCollectEqualsSuiteTotal {
		t.Errorf("invariants = %+v, want one collect-equals-suite-total", pol.Invariants)
	}
	// An M1e-era policy names none of these; the compiled sentinels must
	// resolve to the STRONGER setting, never the weaker one (decision 4).
	if pol.Evidence.Regime != RegimeAuto || pol.Evidence.Crosscheck != CrosscheckRequire ||
		pol.Evidence.PluginAutoload != AutoloadOff {
		t.Errorf("evidence = %+v, want auto/require/off", pol.Evidence)
	}
	if pol.Paths.Empty() || pol.Paths.ProtectedAdditions != AdditionsAllow {
		t.Errorf("paths = %+v, want a non-empty set allowing additions", pol.Paths)
	}
	// The default's pytest oracles resolve to the same runner prefix but are
	// DIFFERENT instances: their identity in evidence includes the kind.
	collect, _ := pol.OracleByName("collect")
	suite, _ := pol.OracleByName("suite")
	if collect.Config == suite.Config {
		t.Error("collect and suite share a resolved config: two kinds must be two instances")
	}
	if !reflect.DeepEqual(collect.Argv, DefaultPytestPrefix()) {
		t.Errorf("collect argv = %v, want the default pytest prefix", collect.Argv)
	}
	if collect.Family != FamilyCollect || suite.Family != FamilySuite {
		t.Errorf("families = %s/%s, want collect/suite", collect.Family, suite.Family)
	}
}

// Command() is the migration path for --oracle-cmd: the gate's command
// lives INSIDE the pinned artifact, so the policy digest determines it.
func TestCommandGolden(t *testing.T) {
	const wantCanon = `{"escalation":{"min_candidates_passing":0,"on_all_worlds_failed_machinery":false,"on_invariant_violation":false,"on_ranking_tie":false,"require_evidence":[]},` +
		`"evidence":{"crosscheck":"","plugin_autoload":"","regime":""},` +
		`"hard_gates":[{"basis":"construction","gate":"status-pass","oracle":"suite","threshold":0}],` +
		`"invariants":[],` +
		`"name":"command",` +
		`"oracles":[{"args":[],"argv":["python3","-m","pytest","-q"],"coverage":false,"kind":"command","name":"suite","reruns":0,"timeout_ms":600000}],` +
		`"paths":{"harness":[],"protected":[],"protected_additions":""},` +
		`"ranking":["gate_pass","wall_ms_asc"],` +
		`"schema":"multiverso.dev/policy/v1"}`
	canon := canonical(t, Command([]string{"python3", "-m", "pytest", "-q"}, 600000))
	if string(canon) != wantCanon {
		t.Errorf("canonical =\n %s\nwant\n %s", canon, wantCanon)
	}
	pol, err := Decode(canon)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := pol.GateLabels(); !reflect.DeepEqual(got, []string{"status-pass@suite"}) {
		t.Errorf("gates = %v", got)
	}
	o, _ := pol.OracleByName("suite")
	if o.Kind != KindCommand || o.TimeoutMS != 600000 {
		t.Errorf("oracle = %+v, want a command kind carrying the intent's wall budget", o)
	}
}

// Decision 1, the load-bearing one: a policy's digest is the digest of the
// bytes it was loaded FROM, never a re-serialization of the decoded struct.
// A legal but non-canonical encoding must keep its own identity.
func TestDigestIsTheInputBytes(t *testing.T) {
	raw := []byte(`{
	  "schema": "multiverso.dev/policy/v1",
	  "ranking": ["gate_pass", "wall_ms_asc"],
	  "name": "spaced",
	  "escalation": {"min_candidates_passing": 0, "on_ranking_tie": false,
	                 "require_evidence": [], "on_all_worlds_failed_machinery": false},
	  "hard_gates": [{"gate": "status-pass", "oracle": "s", "basis": "construction", "threshold": 0}],
	  "oracles": [{"name": "s", "kind": "command", "argv": ["true"], "args": [],
	               "timeout_ms": 0, "coverage": false, "reruns": 0}]
	}`)
	pol, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if want := object.DigestBytes(raw); pol.Digest != want {
		t.Errorf("digest = %s, want the INPUT bytes' digest %s", pol.Digest, want)
	}
	// And it is emphatically NOT the digest of the canonical re-encoding.
	var v1 object.PolicyV1
	if err := json.Unmarshal(raw, &v1); err != nil {
		t.Fatal(err)
	}
	if reencoded := object.DigestBytes(canonical(t, v1)); pol.Digest == reencoded {
		t.Error("fixture is not exercising the property: the input was already canonical")
	}
}

func TestLoadFromCAS(t *testing.T) {
	store, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	canon := canonical(t, Default())
	if _, err := store.Put(canon); err != nil {
		t.Fatal(err)
	}
	dig := object.DigestBytes(canon)
	pol, err := Load(store, dig)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if pol.Digest != dig {
		t.Errorf("digest = %s, want %s", pol.Digest, dig)
	}
	// A v0 policy — the historical shape — loads from the same store, which
	// is what makes replay of an old intent possible at all.
	v0 := canonical(t, object.Policy{
		Schema: object.SchemaPolicy, HardGates: []string{"suite-pass"},
		Ranking: []string{"gate_pass", "wall_ms_asc"},
	})
	if _, err := store.Put(v0); err != nil {
		t.Fatal(err)
	}
	old, err := Load(store, object.DigestBytes(v0))
	if err != nil {
		t.Fatalf("Load v0: %v", err)
	}
	if old.Dialect != DialectV0 {
		t.Errorf("dialect = %s, want %s", old.Dialect, DialectV0)
	}
	if _, err := Load(store, "mv0:"+strings.Repeat("0", 64)); err == nil {
		t.Error("Load of an absent digest: want error")
	}
	if _, err := Load(store, "not-a-digest"); err == nil {
		t.Error("Load of a malformed digest: want error")
	}
}

// The v0 compile table, normatively: family selector, fail-closed unknown
// gate, no-op unknown key, escalation off, one required oracle.
func TestCompileV0(t *testing.T) {
	pol := decode(t, object.Policy{
		Schema:    object.SchemaPolicy,
		HardGates: []string{"suite-pass", "mutation-score"},
		Ranking:   []string{"gate_pass", "wall_ms_asc", "kittens_desc"},
	})
	if pol.Dialect != DialectV0 || pol.Schema != object.SchemaPolicy || pol.Name != "" {
		t.Errorf("compiled = %+v, want the v0 dialect with no name", pol)
	}
	if len(pol.Gates) != 2 {
		t.Fatalf("gates = %d, want 2", len(pol.Gates))
	}
	if g := pol.Gates[0]; g.Predicate != GateSuitePass || g.AlwaysFails ||
		g.Sel != (Selector{Family: FamilySuite}) || g.Basis != object.BasisConstruction {
		t.Errorf("gate 0 = %+v, want the suite-pass family selector", g)
	}
	if g := pol.Gates[1]; !g.AlwaysFails || g.Label != "mutation-score" {
		t.Errorf("gate 1 = %+v, want a fail-closed unknown gate", g)
	}
	wantKeys := []string{KeyGatePass, KeyWallMSAsc, "kittens_desc", KeyWorldDigestAsc}
	if got := pol.KeyNames(); !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("effective keys = %v, want %v", got, wantKeys)
	}
	if !pol.Keys[2].NoOp {
		t.Error("an unknown v0 key must compile to a no-op, as M0's rankLess treated it")
	}
	if !reflect.DeepEqual(pol.Esc, Escalation{}) {
		t.Errorf("escalation = %+v, want every rule off in v0", pol.Esc)
	}
	if !reflect.DeepEqual(pol.Required, []string{FamilySuite}) {
		t.Errorf("required = %v, want [suite]", pol.Required)
	}
	// A key M1e knows but M0 did not honor is still a no-op: the dialect is
	// frozen at what M0 did, not at what the name means today.
	withMetric := decode(t, object.Policy{
		Schema: object.SchemaPolicy, HardGates: []string{"suite-pass"},
		Ranking: []string{"gate_pass", "tests_passed_desc"},
	})
	if !withMetric.Keys[1].NoOp {
		t.Error("tests_passed_desc must be a no-op under the v0 dialect")
	}
}

// --- validation --------------------------------------------------------

func base() object.PolicyV1 {
	return object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "fixture",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: KindPytestCollect, Argv: []string{}, Args: []string{}},
			// Coverage-measuring, like the shipped default: a coverage gate
			// is only authorable against an instance that will actually
			// produce coverage_bp.
			{Name: "suite", Kind: KindPytestSuite, Argv: []string{}, Args: []string{}, Coverage: true},
		},
		HardGates: []object.GateSpec{
			{Gate: GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{KeyGatePass, KeyWallMSAsc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}},
	}
}

func with(f func(*object.PolicyV1)) object.PolicyV1 {
	p := base()
	f(&p)
	return p
}

func TestValidateRejections(t *testing.T) {
	tests := []struct {
		name  string
		pol   object.PolicyV1
		field string
		want  string
	}{
		{
			name:  "unknown schema",
			pol:   with(func(p *object.PolicyV1) { p.Schema = "multiverso.dev/policy/v2" }),
			field: "schema",
			want:  `unknown policy schema "multiverso.dev/policy/v2"`,
		},
		{
			name:  "invalid policy name",
			pol:   with(func(p *object.PolicyV1) { p.Name = "Default Policy" }),
			field: "name",
			want:  `invalid policy name "Default Policy"`,
		},
		{
			name:  "invalid oracle name",
			pol:   with(func(p *object.PolicyV1) { p.Oracles[0].Name = "Collect!" }),
			field: "oracles[0].name",
			want:  `invalid oracle name "Collect!"`,
		},
		{
			name: "duplicate oracle name",
			pol: with(func(p *object.PolicyV1) {
				p.Oracles[1].Name = "collect"
				p.HardGates[0].Oracle = "collect"
				p.HardGates[0].Gate = GateCollectNonempty
			}),
			field: "oracles[1].name",
			want:  `duplicate oracle name "collect"`,
		},
		{
			name:  "unknown oracle kind",
			pol:   with(func(p *object.PolicyV1) { p.Oracles[1].Kind = "cargo-nextest" }),
			field: "oracles[1].kind",
			want:  `unknown oracle kind "cargo-nextest" (known: command, corpus-differential, corpus-observe, hypothesis-properties, mutation-diff, pytest-collect, pytest-suite, tree-guard)`,
		},
		{
			name:  "command kind without argv",
			pol:   with(func(p *object.PolicyV1) { p.Oracles[0].Kind = KindCommand }),
			field: "oracles[0].argv",
			want:  `kind "command" requires a non-empty argv`,
		},
		{
			name:  "negative timeout",
			pol:   with(func(p *object.PolicyV1) { p.Oracles[0].TimeoutMS = -1 }),
			field: "oracles[0].timeout_ms",
			want:  "timeout_ms -1 is negative",
		},
		{
			name:  "reruns on a kind that cannot rerun",
			pol:   with(func(p *object.PolicyV1) { p.Oracles[0].Reruns = 2 }),
			field: "oracles[0].reruns",
			want:  `reruns is only meaningful for kind "pytest-suite", not "pytest-collect"`,
		},
		{
			name: "two names for one resolved config",
			pol: with(func(p *object.PolicyV1) {
				p.Oracles[0].Kind = KindPytestSuite
				p.Oracles[0].Name = "suite-again"
				p.Oracles[0].Coverage = true // every resolved field must match
			}),
			field: "oracles[1]",
			want:  `one instance under two names`,
		},
		{
			name:  "no hard gates",
			pol:   with(func(p *object.PolicyV1) { p.HardGates = nil }),
			field: "hard_gates",
			want:  "at least one hard gate",
		},
		{
			name:  "unknown gate",
			pol:   with(func(p *object.PolicyV1) { p.HardGates[0].Gate = "suite-passes" }),
			field: "hard_gates[0].gate",
			want:  `unknown gate "suite-passes" (known: collect-nonempty, collected-not-below, corpus-complete, coverage-at-least, differential-cohort-at-least, mutation-survivors-not-above, no-failed-tests, paths-unmodified, properties-pass, property-cases-at-least, skips-not-above, status-pass)`,
		},
		{
			name:  "gate names an undeclared oracle",
			pol:   with(func(p *object.PolicyV1) { p.HardGates[0].Oracle = "mutation" }),
			field: "hard_gates[0].oracle",
			want:  `gate names oracle "mutation", which the policy does not declare`,
		},
		{
			name:  "unknown freshness basis",
			pol:   with(func(p *object.PolicyV1) { p.HardGates[0].Basis = "vibes" }),
			field: "hard_gates[0].basis",
			want:  `unknown freshness basis "vibes" (known: construction, dependency, probabilistic)`,
		},
		{
			name:  "negative threshold",
			pol:   with(func(p *object.PolicyV1) { p.HardGates[0].Threshold = -1 }),
			field: "hard_gates[0].threshold",
			want:  "threshold -1 is negative",
		},
		{
			name: "gate metric outside its oracle's vocabulary",
			pol: with(func(p *object.PolicyV1) {
				p.HardGates[0] = object.GateSpec{
					Gate: GateCoverageAtLeast, Oracle: "collect",
					Basis: object.BasisConstruction, Threshold: 8000,
				}
			}),
			field: "hard_gates[0].gate",
			want:  `gate "coverage-at-least" needs metric "coverage_bp", which oracle "collect" (kind "pytest-collect") does not emit`,
		},
		{
			// gateDef.threshold is what makes "this gate takes a parameter"
			// decidable. A number a predicate silently discards makes the
			// file read as if it demanded something it does not.
			name:  "threshold on a gate that takes none",
			pol:   with(func(p *object.PolicyV1) { p.HardGates[0].Threshold = 9000 }),
			field: "hard_gates[0].threshold",
			want:  `gate "status-pass" takes no threshold parameter (got 9000)`,
		},
		{
			// Instance-level, not kind-level: pytest-suite CAN emit
			// coverage_bp, but this instance never will, so the gate would
			// fail on "coverage_bp absent (source unavailable)" for every
			// candidate, forever.
			name: "coverage gate on a coverage-disabled instance",
			pol: with(func(p *object.PolicyV1) {
				p.Oracles[1].Coverage = false
				p.HardGates[0] = object.GateSpec{
					Gate: GateCoverageAtLeast, Oracle: "suite",
					Basis: object.BasisConstruction, Threshold: 8000,
				}
			}),
			field: "hard_gates[0].gate",
			want:  `gate "coverage-at-least" needs metric "coverage_bp", which oracle "suite" (kind "pytest-suite") does not emit`,
		},
		{
			name: "coverage_desc key on a coverage-disabled instance",
			pol: with(func(p *object.PolicyV1) {
				p.Oracles[1].Coverage = false
				p.Ranking = []string{KeyCoverageDesc}
			}),
			field: "ranking[0]",
			want:  `ranking key "coverage_desc" needs metric "coverage_bp", which no declared oracle emits`,
		},
		{
			// A prefix coverage.py cannot drive is the same unsatisfiable
			// gate by another route — and the validator reads the very
			// predicate the suite oracle reads, so the two cannot drift.
			name: "coverage gate on a prefix coverage.py cannot wrap",
			pol: with(func(p *object.PolicyV1) {
				p.Oracles[1].Argv = []string{"pytest"}
				p.HardGates[0] = object.GateSpec{
					Gate: GateCoverageAtLeast, Oracle: "suite",
					Basis: object.BasisConstruction, Threshold: 8000,
				}
			}),
			field: "hard_gates[0].gate",
			want:  `gate "coverage-at-least" needs metric "coverage_bp"`,
		},
		{
			name:  "unknown ranking key",
			pol:   with(func(p *object.PolicyV1) { p.Ranking = []string{"kittens_desc"} }),
			field: "ranking[0]",
			want:  `unknown ranking key "kittens_desc"`,
		},
		{
			name:  "duplicate ranking key",
			pol:   with(func(p *object.PolicyV1) { p.Ranking = []string{KeyWallMSAsc, KeyWallMSAsc} }),
			field: "ranking[1]",
			want:  `duplicate ranking key "wall_ms_asc"`,
		},
		{
			name:  "a key after the terminal key is unreachable",
			pol:   with(func(p *object.PolicyV1) { p.Ranking = []string{KeyWorldDigestAsc, KeyWallMSAsc} }),
			field: "ranking[0]",
			want:  `"world_digest_asc" is the terminal ranking key: nothing can follow it`,
		},
		{
			name: "a metric key no declared oracle can answer",
			pol: with(func(p *object.PolicyV1) {
				p.Oracles = []object.OracleSpec{{
					Name: "suite", Kind: KindCommand, Argv: []string{"true"}, Args: []string{},
				}}
				p.Ranking = []string{KeyTestsPassedDesc}
			}),
			field: "ranking[0]",
			want:  `ranking key "tests_passed_desc" needs metric "tests_passed", which no declared oracle emits`,
		},
		{
			name: "an ambiguous metric key is refused, never coin-flipped",
			pol: with(func(p *object.PolicyV1) {
				p.Oracles = append(p.Oracles, object.OracleSpec{
					Name: "suite-fast", Kind: KindPytestSuite, Argv: []string{}, Args: []string{"-x"},
				})
				p.Ranking = []string{KeyTestsPassedDesc}
			}),
			field: "ranking[0]",
			want:  `ranking key "tests_passed_desc" is ambiguous: oracles [suite, suite-fast] both emit "tests_passed"`,
		},
		{
			name:  "negative min_candidates_passing",
			pol:   with(func(p *object.PolicyV1) { p.Escalation.MinCandidatesPassing = -1 }),
			field: "escalation.min_candidates_passing",
			want:  "min_candidates_passing -1 is negative",
		},
		{
			name:  "require_evidence names an undeclared oracle",
			pol:   with(func(p *object.PolicyV1) { p.Escalation.RequireEvidence = []string{"mutation"} }),
			field: "escalation.require_evidence[0]",
			want:  `requires evidence from oracle "mutation", which the policy does not declare`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(canonical(t, tt.pol))
			if err == nil {
				t.Fatal("Decode accepted an invalid policy")
			}
			probs := Problems(err)
			if len(probs) == 0 {
				t.Fatalf("error %v carries no located problems", err)
			}
			for _, p := range probs {
				if p.Field == tt.field && strings.Contains(p.Detail, tt.want) {
					return
				}
			}
			t.Errorf("problems %v do not contain %s: %s", probs, tt.field, tt.want)
		})
	}
}

// A typo'd field name must never silently mean "no gates": the schema is
// closed, so unknown JSON fields are a load error.
func TestDecodeRejectsUnknownFields(t *testing.T) {
	raw := canonical(t, base())
	broken := strings.Replace(string(raw), `"hard_gates"`, `"hard_gate"`, 1)
	_, err := Decode([]byte(broken))
	if err == nil {
		t.Fatal("Decode accepted an unknown field")
	}
	probs := Problems(err)
	if len(probs) != 1 || probs[0].Field != "hard_gate" || !strings.Contains(probs[0].Detail, "unknown field") {
		t.Errorf("problems = %v, want one unknown-field problem for hard_gate", probs)
	}
}

// Validation reports EVERY problem at once: authoring wants the whole list.
func TestValidateReportsEveryProblem(t *testing.T) {
	pol := with(func(p *object.PolicyV1) {
		p.Name = "BAD NAME"
		p.HardGates[0].Gate = "suite-passes"
		p.Ranking = []string{"kittens_desc"}
		p.Escalation.MinCandidatesPassing = -3
	})
	probs := Problems(Validate(pol))
	fields := make([]string, 0, len(probs))
	for _, p := range probs {
		fields = append(fields, p.Field)
	}
	want := []string{"name", "hard_gates[0].gate", "ranking[0]", "escalation.min_candidates_passing"}
	if !reflect.DeepEqual(fields, want) {
		t.Errorf("problem fields = %v, want %v", fields, want)
	}
}

func TestValidAcceptance(t *testing.T) {
	valid := []struct {
		name string
		pol  object.PolicyV1
	}{
		{"the shipped default", Default()},
		{"a synthesized command policy", Command([]string{"make", "test"}, 0)},
		{"an explicit gate_pass and terminal key", with(func(p *object.PolicyV1) {
			p.Ranking = []string{KeyGatePass, KeyWallMSAsc, KeyWorldDigestAsc}
		})},
		{"every escalation rule on", with(func(p *object.PolicyV1) {
			p.Escalation = object.EscalationSpec{
				MinCandidatesPassing:       2,
				OnRankingTie:               true,
				RequireEvidence:            []string{"collect"},
				OnAllWorldsFailedMachinery: true,
			}
		})},
		{"a coverage gate on the oracle that emits it", with(func(p *object.PolicyV1) {
			p.HardGates = append(p.HardGates, object.GateSpec{
				Gate: GateCoverageAtLeast, Oracle: "suite",
				Basis: object.BasisConstruction, Threshold: 8735,
			})
		})},
		{"a weaker accepted basis", with(func(p *object.PolicyV1) {
			p.HardGates[0].Basis = object.BasisDependency
		})},
		{"reruns on pytest-suite", with(func(p *object.PolicyV1) { p.Oracles[1].Reruns = 2 })},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.pol); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if _, err := Decode(canonical(t, tt.pol)); err != nil {
				t.Fatalf("Decode: %v", err)
			}
		})
	}
}

// require_evidence compiles to selectors and pulls its oracles into the
// required set: a rule that reads evidence must make the race produce it.
func TestRequireEvidenceIsRequired(t *testing.T) {
	pol := decode(t, with(func(p *object.PolicyV1) {
		p.Escalation.RequireEvidence = []string{"collect"}
	}))
	if !reflect.DeepEqual(pol.Required, []string{"suite", "collect"}) {
		t.Errorf("required = %v, want gates first then the escalation's oracle", pol.Required)
	}
	if len(pol.Esc.RequireEvidence) != 1 || pol.Esc.RequireEvidence[0].Sel.ID != KindPytestCollect {
		t.Errorf("compiled requirement = %+v, want a selector for the collect kind", pol.Esc.RequireEvidence)
	}
}

// Declared-but-unrequired oracles are never run.
func TestUnrequiredOracleIsNotRequired(t *testing.T) {
	pol := decode(t, base())
	if !reflect.DeepEqual(pol.Required, []string{"suite"}) {
		t.Errorf("required = %v, want only the gate's oracle", pol.Required)
	}
	if len(pol.Oracles) != 2 {
		t.Errorf("oracles = %d, want both declared instances to survive compilation", len(pol.Oracles))
	}
}

func TestVocabularyIsClosed(t *testing.T) {
	if got := KnownGates(); !reflect.DeepEqual(got, []string{
		GateCollectNonempty, GateCollectedNotBelow, GateCorpusComplete, GateCoverageAtLeast,
		GateDifferentialCohortAtLeast, GateMutationSurvivorsNotAbove, GateNoFailedTests,
		GatePathsUnmodified, GatePropertiesPass, GatePropertyCasesAtLeast,
		GateSkipsNotAbove, GateStatusPass,
	}) {
		t.Errorf("known gates = %v", got)
	}
	if got := KnownKinds(); !reflect.DeepEqual(got, []string{
		KindCommand, KindCorpusDifferential, KindCorpusObserve, KindProperties,
		KindMutationDiff, KindPytestCollect, KindPytestSuite, KindTreeGuard,
	}) {
		t.Errorf("known kinds = %v", got)
	}
	// M1f's closed sets: the invariant vocabulary and the three tri-state
	// enums. An unknown value in any of them is a load error by name.
	if got := KnownInvariants(); !reflect.DeepEqual(got, []string{
		InvariantCollectEqualsSuiteTotal, InvariantSuitePartsSumToTotal,
	}) {
		t.Errorf("known invariants = %v", got)
	}
	if got := KnownRegimes(); len(got) != 4 {
		t.Errorf("known regimes = %v, want 4", got)
	}
	if got := KnownPluginAutoload(); !reflect.DeepEqual(got, []string{AutoloadOff, AutoloadOn}) {
		t.Errorf("KnownPluginAutoload = %v", got)
	}
	if got := KnownCrosschecks(); !reflect.DeepEqual(got, []string{CrosscheckOff, CrosscheckRequire}) {
		t.Errorf("known crosschecks = %v", got)
	}
	if got := KnownKeys(); len(got) != 7 {
		t.Errorf("known keys = %v, want the 7 of the vocabulary", got)
	}
	// The basis ranks, and the rule that an unrecognized basis on a receipt
	// ranks 0 and satisfies nothing.
	for basis, want := range map[string]int{
		object.BasisConstruction: 3, object.BasisDependency: 2, object.BasisProbabilistic: 1,
		"formal-proof": 0, "": 0,
	} {
		if got := BasisRank(basis); got != want {
			t.Errorf("BasisRank(%q) = %d, want %d", basis, got, want)
		}
	}
	// command emits no metrics at all: it is a pass/fail oracle, and a
	// policy that ranks by a metric cannot name it.
	if got := KindMetrics(KindCommand); len(got) != 0 {
		t.Errorf("command metrics = %v, want none", got)
	}
	if got := KindMetrics(KindPytestSuite); len(got) != 9 {
		t.Errorf("pytest-suite metrics = %v, want 9", got)
	}
}

func TestSchemaShort(t *testing.T) {
	if got := SchemaShort(object.SchemaPolicyV1); got != "policy/v1" {
		t.Errorf("SchemaShort = %q, want policy/v1", got)
	}
	if got := SchemaShort("weird"); got != "weird" {
		t.Errorf("SchemaShort = %q, want the input unchanged", got)
	}
}

func TestDecodeMalformedJSON(t *testing.T) {
	if _, err := Decode([]byte("{not json")); err == nil {
		t.Error("Decode accepted malformed JSON")
	}
	if _, err := Decode([]byte(`{"schema":"multiverso.dev/policy/v1","hard_gates":"nope"}`)); err == nil {
		t.Error("Decode accepted a wrongly typed field")
	}
}
