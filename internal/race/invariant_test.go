package race

// M1f: cross-oracle invariants in the decision function, and escalation
// rule 0. Everything here is PURE — hand-built worlds and receipts, no
// git, no oracles, no clock — because that is what Decide is.

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// invariantPolicy is the shipped default's shape without the guard: two
// pytest oracles, one status gate, and the collect-equals-suite-total
// invariant with rule 0 on. It is testdata/toyrepo/policies's
// no-paths-invariants.json in Go form — the invariant tested in isolation
// from the guard.
func invariantPolicy(t *testing.T, ruleOn bool) policy.Policy {
	t.Helper()
	spec := object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "no-paths-invariants",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{}, Args: []string{}},
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{}},
		},
		HardGates: []object.GateSpec{
			{Gate: policy.GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking: []string{policy.KeyGatePass, policy.KeyTestsPassedDesc},
		Escalation: object.EscalationSpec{
			RequireEvidence:            []string{},
			OnAllWorldsFailedMachinery: true,
			OnInvariantViolation:       ruleOn,
		},
		Invariants: []object.InvariantSpec{{
			Name:    policy.InvariantCollectEqualsSuiteTotal,
			Oracles: map[string]string{policy.RoleCollect: "collect", policy.RoleSuite: "suite"},
		}},
	}
	canon, err := object.Canonical(spec)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	pol, err := policy.Decode(canon)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return pol
}

// invWorld builds one world plus its collect and suite receipts.
func invWorld(t *testing.T, pol policy.Policy, name string, collected, total, passed int64,
	suiteStatus string) (object.RecordedWorld, []object.RecordedReceipt) {
	t.Helper()
	tree := "git:" + strings.Repeat(name, 40)[:40]
	env := "mv0:" + strings.Repeat("e", 64)
	w := object.World{
		Schema: object.SchemaWorld, Intent: "mv0:" + strings.Repeat("1", 64),
		Tree: tree, Env: env, IsolationTier: object.TierT0Worktree,
		Outcome: object.OutcomeCompleted,
	}
	dig := "mv0:" + strings.Repeat(name, 64)[:64]
	rw := object.RecordedWorld{Digest: dig, World: w}

	mk := func(kind string, metrics map[string]int64, status, suffix string) object.RecordedReceipt {
		o, _ := pol.OracleByName(map[string]string{
			policy.KindPytestCollect: "collect", policy.KindPytestSuite: "suite",
		}[kind])
		rec := object.Receipt{
			Schema: object.SchemaReceipt, World: dig,
			Oracle: object.OracleRef{ID: kind, Version: "v0", Config: o.Config},
			Result: object.Result{Status: status, Metrics: metrics,
				Tools: map[string]string{}, Artifacts: []string{}},
			Freshness: object.Freshness{
				Basis: object.BasisConstruction, ValidFor: object.ValidFor{Tree: tree, Env: env}},
			Family: policy.KindFamily(kind),
		}
		return object.RecordedReceipt{Digest: "mv0:" + strings.Repeat(suffix, 64)[:64], Receipt: rec}
	}
	return rw, []object.RecordedReceipt{
		mk(policy.KindPytestCollect, map[string]int64{policy.MetricCollectedTotal: collected}, "pass", name+"c"),
		mk(policy.KindPytestSuite, map[string]int64{
			policy.MetricTestsTotal: total, policy.MetricTestsPassed: passed,
		}, suiteStatus, name+"s"),
	}
}

// The evaluation table: holds, violated, metric absent, receipt absent,
// and not-evaluated after a gate failure.
func TestInvariantEvaluation(t *testing.T) {
	pol := invariantPolicy(t, false)

	t.Run("it holds, and the world passes", func(t *testing.T) {
		w, recs := invWorld(t, pol, "a", 8, 8, 8, "pass")
		tr := Trace(pol, []object.RecordedWorld{w}, recs)
		c := tr.Candidates[0]
		if !c.Pass {
			t.Fatalf("candidate did not pass: %+v", c.Gates)
		}
		if len(c.Invariants) != 1 || c.Invariants[0].Result != InvariantOK {
			t.Errorf("invariants = %+v, want one ok", c.Invariants)
		}
	})

	t.Run("the study's reproduction fails the world without failing a gate", func(t *testing.T) {
		w, recs := invWorld(t, pol, "b", 8, 500, 500, "pass")
		tr := Trace(pol, []object.RecordedWorld{w}, recs)
		c := tr.Candidates[0]
		for _, g := range c.Gates {
			if g.Result == policy.GateFail {
				t.Fatalf("a hard gate failed (%s: %s); this vector passes every gate", g.Label, g.Detail)
			}
		}
		if c.Pass {
			t.Fatal("the world passed with self-contradictory evidence")
		}
		if c.Invariants[0].Result != InvariantViolated ||
			c.Invariants[0].Detail != "collected_total=8 != tests_total=500" {
			t.Errorf("invariant = %+v, want VIOLATED with the frozen detail", c.Invariants[0])
		}
	})

	t.Run("an absent metric is a violation", func(t *testing.T) {
		w, recs := invWorld(t, pol, "c", 8, 8, 8, "pass")
		delete(recs[1].Receipt.Result.Metrics, policy.MetricTestsTotal)
		tr := Trace(pol, []object.RecordedWorld{w}, recs)
		c := tr.Candidates[0]
		if c.Pass {
			t.Fatal("the world passed with an invariant it could not evaluate")
		}
		if c.Invariants[0].Detail != "tests_total absent (source unavailable)" {
			t.Errorf("detail = %q, want the absence sentence", c.Invariants[0].Detail)
		}
	})

	t.Run("a world stopped at a gate has NOT-EVALUATED invariants", func(t *testing.T) {
		// collected_total = 0 fails collect-nonempty, so the ladder stops
		// before the suite runs — exactly as later gates are not-evaluated.
		w, recs := invWorld(t, pol, "d", 0, 8, 8, "pass")
		tr := Trace(pol, []object.RecordedWorld{w}, recs[:1])
		c := tr.Candidates[0]
		if c.Pass {
			t.Fatal("a world that failed collect-nonempty passed")
		}
		if c.Invariants[0].Result != InvariantNotEvaluated {
			t.Errorf("invariant = %+v, want not-evaluated", c.Invariants[0])
		}
	})
}

// Rule 0 outranks every M1e rule, and a violated invariant reaches a human
// rather than being filed under "the tests didn't pass".
func TestRuleZeroPrecedenceAndText(t *testing.T) {
	pol := invariantPolicy(t, true)
	honest, hr := invWorld(t, pol, "a", 8, 8, 8, "pass")
	cheat, cr := invWorld(t, pol, "b", 8, 500, 500, "pass")
	worlds := []object.RecordedWorld{honest, cheat}
	receipts := append(append([]object.RecordedReceipt{}, hr...), cr...)

	dec := Decide(pol, worlds, receipts)
	if dec.Type != TypeEscalate {
		t.Fatalf("type = %s, want ESCALATE (%s)", dec.Type, dec.Rationale)
	}
	if !strings.Contains(dec.Rationale, "escalated by policy rule on_invariant_violation") {
		t.Errorf("rationale does not name rule 0:\n%s", dec.Rationale)
	}
	if !strings.Contains(dec.Rationale, "1 of 2 worlds produced self-contradictory evidence") {
		t.Errorf("rationale does not count the worlds:\n%s", dec.Rationale)
	}
	if !strings.Contains(dec.Rationale, "violated collect-equals-suite-total (collected_total=8 != tests_total=500)") {
		t.Errorf("rationale does not name the invariant and the numbers:\n%s", dec.Rationale)
	}
	// The honest world is still the LEADER — rule 0 replaces the verdict,
	// it does not reorder the ranking.
	tr := Trace(pol, worlds, receipts)
	if tr.Winner != honest.Digest {
		t.Errorf("leader = %s, want the honest world %s", tr.Winner, honest.Digest)
	}

	t.Run("rule 0 outranks on_all_worlds_failed_machinery", func(t *testing.T) {
		// Both worlds contradict themselves AND one is machinery-failed.
		// Rule 1 would say "no world produced conclusive evidence", which
		// is the wrong description of a detected forgery.
		bad, br := invWorld(t, pol, "c", 8, 500, 500, "pass")
		dec := Decide(pol, []object.RecordedWorld{cheat, bad}, append(append([]object.RecordedReceipt{}, cr...), br...))
		if dec.Type != TypeEscalate {
			t.Fatalf("type = %s, want ESCALATE", dec.Type)
		}
		if !strings.Contains(dec.Rationale, RuleOnInvariantViolation) {
			t.Errorf("rationale names the wrong rule:\n%s", dec.Rationale)
		}
		if strings.Contains(dec.Rationale, RuleAllWorldsFailedMachinery) {
			t.Errorf("rule 1 fired over rule 0:\n%s", dec.Rationale)
		}
	})
}

// The REJECT per-world clause: a world that cleared every gate and still
// did not pass was stopped by an invariant, and the sentence says which.
func TestInvariantRejectClause(t *testing.T) {
	pol := invariantPolicy(t, false) // rule 0 OFF: the verdict stays REJECT
	cheat, cr := invWorld(t, pol, "b", 8, 500, 500, "pass")
	dec := Decide(pol, []object.RecordedWorld{cheat}, cr)
	if dec.Type != TypeReject {
		t.Fatalf("type = %s, want REJECT (%s)", dec.Type, dec.Rationale)
	}
	want := cheat.Digest + " violated invariant collect-equals-suite-total (collected_total=8 != tests_total=500)"
	if !strings.Contains(dec.Rationale, want) {
		t.Errorf("rationale =\n%s\nwant it to contain\n%s", dec.Rationale, want)
	}
}

// M1f decision 3's compatibility proof: a policy that declares NO invariant
// reaches none of the new text, and rule 0 cannot fire.
func TestNoInvariantsReachesNoNewText(t *testing.T) {
	spec := object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "m1e",
		Oracles: []object.OracleSpec{
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{}},
		},
		HardGates: []object.GateSpec{
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{policy.KeyGatePass, policy.KeyTestsPassedDesc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}, OnAllWorldsFailedMachinery: true},
	}
	canon, err := object.Canonical(spec)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := policy.Decode(canon)
	if err != nil {
		t.Fatal(err)
	}
	w, recs := invWorld(t, pol, "a", 8, 8, 8, "fail")
	tr := Trace(pol, []object.RecordedWorld{w}, recs)
	if len(tr.Candidates[0].Invariants) != 0 {
		t.Errorf("invariants = %+v, want nil for a policy declaring none", tr.Candidates[0].Invariants)
	}
	dec := Decide(pol, []object.RecordedWorld{w}, recs)
	for _, forbidden := range []string{"violated invariant", "self-contradictory", RuleOnInvariantViolation} {
		if strings.Contains(dec.Rationale, forbidden) {
			t.Errorf("an M1e-shaped policy emitted M1f text %q:\n%s", forbidden, dec.Rationale)
		}
	}
}
