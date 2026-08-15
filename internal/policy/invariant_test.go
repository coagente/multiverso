package policy

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

// invPolicy is the shipped default's shape reduced to what an invariant
// needs, so each case below changes exactly one thing.
func invPolicy(mutate func(p *object.PolicyV1)) object.PolicyV1 {
	p := object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "inv",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: KindPytestCollect, Argv: []string{}, Args: []string{}},
			{Name: "suite", Kind: KindPytestSuite, Argv: []string{}, Args: []string{}},
		},
		HardGates: []object.GateSpec{
			{Gate: GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{KeyGatePass},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}},
		Invariants: []object.InvariantSpec{{
			Name:    InvariantCollectEqualsSuiteTotal,
			Oracles: map[string]string{RoleCollect: "collect", RoleSuite: "suite"},
		}},
	}
	if mutate != nil {
		mutate(&p)
	}
	return p
}

// Validation rules 9–11 and 14, one row each. An unknown name, a wrong
// role set, a role naming an oracle that cannot emit the metric, unequal
// args, and a duplicate are all LOAD errors: the decision functions never
// see an invariant they cannot evaluate.
func TestInvariantValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(p *object.PolicyV1)
		want   string
	}{
		{
			name: "rule 9 — an unknown invariant name",
			mutate: func(p *object.PolicyV1) {
				p.Invariants[0].Name = "collect-equals-the-vibes"
			},
			want: `unknown invariant "collect-equals-the-vibes" (known: collect-equals-suite-total, suite-parts-sum-to-total)`,
		},
		{
			name: "rule 9 — a missing role",
			mutate: func(p *object.PolicyV1) {
				delete(p.Invariants[0].Oracles, RoleSuite)
			},
			want: `declares roles [collect, suite], got [collect]`,
		},
		{
			name: "rule 9 — an extra role",
			mutate: func(p *object.PolicyV1) {
				p.Invariants[0].Oracles["stopwatch"] = "suite"
			},
			want: `declares roles [collect, suite], got [collect, stopwatch, suite]`,
		},
		{
			name: "rule 9 — a role naming an oracle the policy does not declare",
			mutate: func(p *object.PolicyV1) {
				p.Invariants[0].Oracles[RoleCollect] = "nope"
			},
			want: `names oracle "nope", which the policy does not declare`,
		},
		{
			name: "rule 9 — a role naming an oracle that cannot emit its metric",
			mutate: func(p *object.PolicyV1) {
				// A suite oracle cannot supply collected_total.
				p.Invariants[0].Oracles[RoleCollect] = "suite"
			},
			want: `needs metric "collected_total" from role "collect", which oracle "suite" (kind "pytest-suite") does not emit`,
		},
		{
			name: "rule 10 — unequal args would fire on a correct configuration",
			mutate: func(p *object.PolicyV1) {
				p.Oracles[0].Args = []string{"-k", "fast"}
			},
			want: `collect-equals-suite-total: oracles "collect" and "suite" must declare identical args (got ["-k","fast"] and [])`,
		},
		{
			name: "rule 11 — the same invariant over the same oracles, twice",
			mutate: func(p *object.PolicyV1) {
				p.Invariants = append(p.Invariants, p.Invariants[0])
			},
			want: `duplicate invariant "collect-equals-suite-total" over the same oracles`,
		},
		{
			name:   "rule 14 — an unknown evidence regime",
			mutate: func(p *object.PolicyV1) { p.Evidence.Regime = "vibes" },
			want:   `unknown value "vibes" (known: auto, in-tree, isolated, streamed)`,
		},
		{
			name:   "rule 14 — an unknown crosscheck",
			mutate: func(p *object.PolicyV1) { p.Evidence.Crosscheck = "maybe" },
			want:   `unknown value "maybe" (known: off, require)`,
		},
		{
			name:   "rule 14 — an unknown plugin_autoload",
			mutate: func(p *object.PolicyV1) { p.Evidence.PluginAutoload = "sometimes" },
			want:   `unknown value "sometimes" (known: off, on)`,
		},
		{
			name:   "rule 14 — an unknown protected_additions",
			mutate: func(p *object.PolicyV1) { p.Paths.ProtectedAdditions = "sometimes" },
			want:   `unknown value "sometimes" (known: allow, refuse)`,
		},
		{
			name: "rule 12 — paths-unmodified with nothing to guard",
			mutate: func(p *object.PolicyV1) {
				p.Oracles = append(p.Oracles, object.OracleSpec{
					Name: "guard", Kind: KindTreeGuard, Argv: []string{}, Args: []string{}})
				p.HardGates = append(p.HardGates, object.GateSpec{
					Gate: GatePathsUnmodified, Oracle: "guard", Basis: object.BasisConstruction})
			},
			want: `paths-unmodified requires policy.paths to declare at least one pattern`,
		},
		{
			name: "rule 13 — an unparseable pattern",
			mutate: func(p *object.PolicyV1) {
				p.Paths.Protected = []string{"{a,b}.py"}
			},
			want: `brace alternation is not part of the grammar`,
		},
		{
			name: "rule 15 — a tree-guard that was handed a process to run",
			mutate: func(p *object.PolicyV1) {
				p.Oracles = append(p.Oracles, object.OracleSpec{
					Name: "guard", Kind: KindTreeGuard,
					Argv: []string{"python3"}, Args: []string{"-k", "x"},
					Coverage: true, TimeoutMS: 5,
				})
			},
			want: `kind "tree-guard" runs no process`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(invPolicy(tc.mutate))
			if err == nil {
				t.Fatalf("Validate accepted the policy")
			}
			found := false
			for _, p := range Problems(err) {
				if strings.Contains(p.Detail, tc.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("problems %v do not contain %q", Problems(err), tc.want)
			}
		})
	}
}

// The vocabulary's two invariants, evaluated over resolved metrics. A
// required metric that is ABSENT makes the invariant VIOLATED: an
// invariant that cannot be evaluated does not hold by default.
func TestInvariantHolds(t *testing.T) {
	lookup := func(m map[string]int64) func(string, string) (int64, bool) {
		return func(role, metric string) (int64, bool) {
			v, ok := m[role+"."+metric]
			return v, ok
		}
	}
	collectSuite := Invariant{
		Name:  InvariantCollectEqualsSuiteTotal,
		Roles: map[string]Selector{RoleCollect: {ID: KindPytestCollect}, RoleSuite: {ID: KindPytestSuite}},
	}
	parts := Invariant{
		Name:  InvariantSuitePartsSumToTotal,
		Roles: map[string]Selector{RoleSuite: {ID: KindPytestSuite}},
	}

	t.Run("it holds when the two sources agree", func(t *testing.T) {
		ok, detail := Holds(collectSuite, lookup(map[string]int64{
			"collect.collected_total": 8, "suite.tests_total": 8,
		}))
		if !ok || detail != "" {
			t.Errorf("holds = %v (%s), want true", ok, detail)
		}
	})
	t.Run("the study's reproduction: 500 tests in an eight-test repository", func(t *testing.T) {
		ok, detail := Holds(collectSuite, lookup(map[string]int64{
			"collect.collected_total": 8, "suite.tests_total": 500,
		}))
		if ok {
			t.Fatal("the invariant held over a 500-test receipt in an eight-test repository")
		}
		if detail != "collected_total=8 != tests_total=500" {
			t.Errorf("detail = %q, want the frozen sentence", detail)
		}
	})
	t.Run("an absent metric is a violation, not a pass", func(t *testing.T) {
		ok, detail := Holds(collectSuite, lookup(map[string]int64{"suite.tests_total": 8}))
		if ok {
			t.Fatal("the invariant held with a metric missing")
		}
		if detail != "collected_total absent (source unavailable)" {
			t.Errorf("detail = %q, want the absence sentence", detail)
		}
	})
	t.Run("the parts of a suite sum to its total", func(t *testing.T) {
		base := map[string]int64{
			"suite.tests_total": 8, "suite.tests_passed": 6, "suite.tests_failed": 2,
			"suite.tests_errored": 0, "suite.tests_skipped": 0,
		}
		if ok, detail := Holds(parts, lookup(base)); !ok {
			t.Errorf("holds = false (%s), want true", detail)
		}
		base["suite.tests_passed"] = 500
		ok, detail := Holds(parts, lookup(base))
		if ok {
			t.Fatal("the invariant held over parts that do not sum")
		}
		if detail != "tests_total=8 != passed+failed+errored+skipped=502" {
			t.Errorf("detail = %q, want the frozen sentence", detail)
		}
	})
}

// A valid invariant compiles to resolved selectors and pulls its oracles
// into the required set: a rule that reads evidence must make the race
// produce it.
func TestInvariantCompiles(t *testing.T) {
	canon, err := object.Canonical(invPolicy(nil))
	if err != nil {
		t.Fatal(err)
	}
	pol, err := Decode(canon)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(pol.Invariants) != 1 {
		t.Fatalf("invariants = %+v, want one", pol.Invariants)
	}
	inv := pol.Invariants[0]
	if got := inv.RoleNames(); len(got) != 2 || got[0] != RoleCollect || got[1] != RoleSuite {
		t.Errorf("roles = %v, want [collect suite]", got)
	}
	if inv.Roles[RoleCollect].ID != KindPytestCollect || inv.Roles[RoleSuite].ID != KindPytestSuite {
		t.Errorf("compiled selectors = %+v, want one per kind", inv.Roles)
	}
	// The gate names only `suite`; the invariant is what makes `collect`
	// required.
	seen := map[string]bool{}
	for _, name := range pol.Required {
		seen[name] = true
	}
	if !seen["collect"] || !seen["suite"] {
		t.Errorf("required = %v, want both roles' oracles", pol.Required)
	}
}

// M1f decision 3's compatibility rule, under test: an M1e-era policy file
// — no paths, no invariants, no evidence — still decodes, and compiles to
// M1e-identical gates, keys and escalation with the M1f defaults resolved.
func TestM1ePolicyStillCompiles(t *testing.T) {
	const m1e = `{"escalation":{"min_candidates_passing":0,"on_all_worlds_failed_machinery":true,"on_ranking_tie":false,"require_evidence":[]},` +
		`"hard_gates":[{"basis":"construction","gate":"collect-nonempty","oracle":"collect","threshold":0},` +
		`{"basis":"construction","gate":"collected-not-below","oracle":"collect","threshold":0},` +
		`{"basis":"construction","gate":"status-pass","oracle":"suite","threshold":0}],` +
		`"name":"default",` +
		`"oracles":[{"args":[],"argv":[],"coverage":true,"kind":"pytest-collect","name":"collect","reruns":0,"timeout_ms":0},` +
		`{"args":[],"argv":[],"coverage":true,"kind":"pytest-suite","name":"suite","reruns":0,"timeout_ms":0}],` +
		`"ranking":["gate_pass","tests_passed_desc","wall_ms_asc"],` +
		`"schema":"multiverso.dev/policy/v1"}`
	pol, err := Decode([]byte(m1e))
	if err != nil {
		t.Fatalf("an M1e policy no longer decodes: %v", err)
	}
	if got := pol.Digest; got != object.DigestBytes([]byte(m1e)) {
		t.Errorf("digest = %s, want the digest of the bytes it was loaded from", got)
	}
	if got := pol.GateLabels(); len(got) != 3 || got[0] != "collect-nonempty@collect" {
		t.Errorf("gates = %v, want M1e's three", got)
	}
	if got := pol.KeyNames(); len(got) != 4 || got[2] != KeyWallMSAsc {
		t.Errorf("keys = %v, want M1e's effective list including wall_ms_asc", got)
	}
	if len(pol.Invariants) != 0 {
		t.Errorf("invariants = %+v, want none: an M1e policy declares none and reaches no new text", pol.Invariants)
	}
	if pol.Esc.OnInvariantViolation {
		t.Error("on_invariant_violation is on for an M1e policy; rule 0 must be unreachable")
	}
	// The compiled M1f defaults: "" ⇒ auto, "" ⇒ require, "" ⇒ allow.
	if pol.Evidence.Regime != RegimeAuto || pol.Evidence.Crosscheck != CrosscheckRequire ||
		pol.Evidence.PluginAutoload != AutoloadOff {
		t.Errorf("evidence = %+v, want auto/require/off", pol.Evidence)
	}
	if !pol.Paths.Empty() || pol.Paths.ProtectedAdditions != AdditionsAllow {
		t.Errorf("paths = %+v, want empty with additions allowed", pol.Paths)
	}
}
