package admit

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

const (
	worldDig  = "mv0:" + "1111111111111111111111111111111111111111111111111111111111111111"
	intentDig = "mv0:" + "2222222222222222222222222222222222222222222222222222222222222222"
	trunkTree = "git:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	landTree  = "git:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// compileV0 compiles M0's frozen policy shape — the dialect M1a's landing
// sentences were written under, and must still be rendered in.
func compileV0(t *testing.T, gates []string) policy.Policy {
	t.Helper()
	_, canon, err := object.Digest(object.Policy{
		Schema: object.SchemaPolicy, HardGates: gates, Ranking: []string{"gate_pass", "wall_ms_asc"},
	})
	if err != nil {
		t.Fatalf("digest v0 policy: %v", err)
	}
	pol, err := policy.Decode(canon)
	if err != nil {
		t.Fatalf("decode v0 policy: %v", err)
	}
	return pol
}

// suiteSpec/collectSpec are the declared instances of the v1 fixture
// policies; a landing receipt must carry the kind and resolved config
// digest of the instance whose gate it answers.
func suiteSpec() object.OracleSpec {
	return object.OracleSpec{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{}}
}

func collectSpec() object.OracleSpec {
	return object.OracleSpec{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{}, Args: []string{}}
}

func cfgDigest(t *testing.T, spec object.OracleSpec) string {
	t.Helper()
	dig, err := policy.ConfigDigest(spec)
	if err != nil {
		t.Fatalf("config digest: %v", err)
	}
	return dig
}

func compileV1(t *testing.T, gates []object.GateSpec) policy.Policy {
	t.Helper()
	_, canon, err := object.Digest(object.PolicyV1{
		Schema:     object.SchemaPolicyV1,
		Name:       "fixture",
		Oracles:    []object.OracleSpec{collectSpec(), suiteSpec()},
		HardGates:  gates,
		Ranking:    []string{},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}},
	})
	if err != nil {
		t.Fatalf("digest v1 policy: %v", err)
	}
	pol, err := policy.Decode(canon)
	if err != nil {
		t.Fatalf("decode v1 policy: %v", err)
	}
	return pol
}

func gateSpec(name, oracle string, threshold int64) object.GateSpec {
	return object.GateSpec{Gate: name, Oracle: oracle, Basis: object.BasisConstruction, Threshold: threshold}
}

func applyReceipt(status string, exitCode int) object.Receipt {
	return object.Receipt{
		Schema: object.SchemaReceipt,
		World:  worldDig,
		Oracle: object.OracleRef{ID: OracleIDLandingApply, Version: "v0", Config: "mv0:cfg"},
		Execution: object.Execution{
			Argv: []string{"git", "apply", "--index", "-"}, ExitCode: exitCode,
			DurationMS: 7, IsolationTier: "T0-worktree",
		},
		Result:      object.NewResult(status, "sha256:out", "sha256:err"),
		Freshness:   object.Freshness{Basis: object.BasisConstruction, ValidFor: object.ValidFor{Tree: trunkTree, Env: "mv0:env"}},
		RecheckTier: "V1-replayable",
		Family:      FamilyLandingApply,
		Cost:        object.Cost{WallMS: 7},
		CreatedAt:   "2026-01-01T00:00:00Z",
	}
}

// gateReceipt is an M0/M1a-shaped landing suite receipt: family "suite",
// command oracle, no metrics.
func gateReceipt(status string) object.Receipt {
	return object.Receipt{
		Schema:      object.SchemaReceipt,
		World:       worldDig,
		Oracle:      object.OracleRef{ID: "command", Version: "v0", Config: "mv0:cfg"},
		Execution:   object.Execution{Argv: []string{"true"}, ExitCode: 0, DurationMS: 11, IsolationTier: "T0-worktree"},
		Result:      object.NewResult(status, "sha256:out", "sha256:err"),
		Freshness:   object.Freshness{Basis: object.BasisConstruction, ValidFor: object.ValidFor{Tree: landTree, Env: "mv0:env"}},
		RecheckTier: "V1-replayable",
		Family:      "suite",
		Cost:        object.Cost{WallMS: 11},
		CreatedAt:   "2026-01-01T00:00:01Z",
	}
}

// kindReceipt is an M1e landing receipt from a declared oracle instance.
func kindReceipt(kind, cfg, family, status string, metrics map[string]int64) object.Receipt {
	if metrics == nil {
		metrics = map[string]int64{}
	}
	r := gateReceipt(status)
	r.Oracle = object.OracleRef{ID: kind, Version: "v0", Config: cfg}
	r.Family = family
	r.Result.Metrics = metrics
	r.CreatedAt = "2026-01-01T00:00:0" + string(rune('2'+len(family)%6)) + "Z"
	return r
}

func recorded(t *testing.T, r object.Receipt) object.RecordedReceipt {
	t.Helper()
	return object.RecordedReceipt{Digest: mustDigest(t, r), Receipt: r}
}

func recordedAll(t *testing.T, recs ...object.Receipt) []object.RecordedReceipt {
	t.Helper()
	out := make([]object.RecordedReceipt, 0, len(recs))
	for _, r := range recs {
		out = append(out, recorded(t, r))
	}
	return out
}

func mustDigest(t *testing.T, v any) string {
	t.Helper()
	dig, _, err := object.Digest(v)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return dig
}

func TestDecideTable(t *testing.T) {
	pol := compileV0(t, []string{"suite-pass"})
	unknownPol := compileV0(t, []string{"suite-pass", "mutation-score"})
	suiteCfg, collectCfg := cfgDigest(t, suiteSpec()), cfgDigest(t, collectSpec())
	twoGates := compileV1(t, []object.GateSpec{
		gateSpec(policy.GateCollectNonempty, "collect", 0),
		gateSpec(policy.GateStatusPass, "suite", 0),
	})

	applyPass := applyReceipt("pass", 0)
	applyFail := applyReceipt("fail", 1)
	gatePass := gateReceipt("pass")
	gateFail := gateReceipt("fail")
	gateError := gateReceipt("error")
	collectOK := kindReceipt(policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass",
		map[string]int64{policy.MetricCollectedTotal: 8})
	collectEmpty := kindReceipt(policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "fail",
		map[string]int64{policy.MetricCollectedTotal: 0})
	suiteOK := kindReceipt(policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", nil)
	suiteBad := kindReceipt(policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "fail", nil)
	otherTree := kindReceipt(policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", nil)
	otherTree.Freshness.ValidFor.Tree = "git:cccccccccccccccccccccccccccccccccccccccc"

	applyPassDig := mustDigest(t, applyPass)
	applyFailDig := mustDigest(t, applyFail)

	sorted := func(digs ...string) []string {
		out := append([]string(nil), digs...)
		sort.Strings(out)
		return out
	}

	tests := []struct {
		name          string
		policy        policy.Policy
		apply         object.Receipt
		gates         []object.Receipt
		wantType      string
		wantEvidence  []string
		wantRationale string
	}{
		{
			name: "escalate on apply conflict", policy: pol,
			apply:        applyFail,
			wantType:     TypeEscalate,
			wantEvidence: []string{applyFailDig},
			wantRationale: fmt.Sprintf(
				"landing apply of %s onto trunk tree %s failed (exit 1); conflicts are never auto-resolved (CP-8) — conflict set in apply receipt artifacts",
				worldDig, trunkTree),
		},
		{
			name: "admit on gate pass", policy: pol,
			apply: applyPass, gates: []object.Receipt{gatePass},
			wantType:     TypeAdmit,
			wantEvidence: sorted(applyPassDig, mustDigest(t, gatePass)),
			wantRationale: fmt.Sprintf(
				"landing gates [suite-pass] passed on tree %s; admitting %s", landTree, worldDig),
		},
		{
			name: "reject on gate fail", policy: pol,
			apply: applyPass, gates: []object.Receipt{gateFail},
			wantType:     TypeReject,
			wantEvidence: sorted(applyPassDig, mustDigest(t, gateFail)),
			wantRationale: fmt.Sprintf(
				"landing gates [suite-pass] failed on tree %s; suite-pass (status=fail)", landTree),
		},
		{
			name: "reject on gate error", policy: pol,
			apply: applyPass, gates: []object.Receipt{gateError},
			wantType:     TypeReject,
			wantEvidence: sorted(applyPassDig, mustDigest(t, gateError)),
			wantRationale: fmt.Sprintf(
				"landing gates [suite-pass] failed on tree %s; suite-pass (status=error)", landTree),
		},
		{
			name: "reject with no gate receipt", policy: pol,
			apply:        applyPass,
			wantType:     TypeReject,
			wantEvidence: []string{applyPassDig},
			wantRationale: fmt.Sprintf(
				"landing gates [suite-pass] failed on tree %s; suite-pass (no landing suite receipt)", trunkTree),
		},
		{
			name: "unknown gate fails", policy: unknownPol,
			apply: applyPass, gates: []object.Receipt{gatePass},
			wantType:     TypeReject,
			wantEvidence: sorted(applyPassDig, mustDigest(t, gatePass)),
			wantRationale: fmt.Sprintf(
				"landing gates [suite-pass,mutation-score] failed on tree %s; mutation-score (unknown gate)", landTree),
		},
		{
			name: "v1: two landing gates, both pass", policy: twoGates,
			apply: applyPass, gates: []object.Receipt{collectOK, suiteOK},
			wantType:     TypeAdmit,
			wantEvidence: sorted(applyPassDig, mustDigest(t, collectOK), mustDigest(t, suiteOK)),
			wantRationale: fmt.Sprintf(
				"landing gates [collect-nonempty@collect,status-pass@suite] passed on tree %s; admitting %s",
				landTree, worldDig),
		},
		{
			name: "v1: the SECOND gate rejects", policy: twoGates,
			apply: applyPass, gates: []object.Receipt{collectOK, suiteBad},
			wantType:     TypeReject,
			wantEvidence: sorted(applyPassDig, mustDigest(t, collectOK), mustDigest(t, suiteBad)),
			wantRationale: fmt.Sprintf(
				"landing gates [collect-nonempty@collect,status-pass@suite] failed on tree %s; status-pass@suite (status=fail)",
				landTree),
		},
		{
			name: "v1: every failing gate is reported, never short-circuited", policy: twoGates,
			apply: applyPass, gates: []object.Receipt{collectEmpty, suiteBad},
			wantType:     TypeReject,
			wantEvidence: sorted(applyPassDig, mustDigest(t, collectEmpty), mustDigest(t, suiteBad)),
			wantRationale: fmt.Sprintf(
				"landing gates [collect-nonempty@collect,status-pass@suite] failed on tree %s; collect-nonempty@collect (collected_total=0 (exit 0)), status-pass@suite (status=fail)",
				landTree),
		},
		{
			name:   "v1: gate receipts that judged different trees are not evidence about one landing",
			policy: twoGates,
			apply:  applyPass, gates: []object.Receipt{collectOK, otherTree},
			wantType:     TypeReject,
			wantEvidence: sorted(applyPassDig, mustDigest(t, collectOK), mustDigest(t, otherTree)),
			wantRationale: fmt.Sprintf(
				"landing gates [collect-nonempty@collect,status-pass@suite] failed on tree %s; landing gate receipts disagree on the landing tree",
				trunkTree),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gates := recordedAll(t, tt.gates...)
			d := Decide(tt.policy, intentDig, worldDig, recorded(t, tt.apply), gates)
			if d.Schema != object.SchemaDecision {
				t.Errorf("schema = %q, want %q", d.Schema, object.SchemaDecision)
			}
			if d.Type != tt.wantType {
				t.Errorf("type = %q, want %q", d.Type, tt.wantType)
			}
			if d.Intent != intentDig {
				t.Errorf("intent = %q, want %q", d.Intent, intentDig)
			}
			if !reflect.DeepEqual(d.Subject, []string{worldDig}) {
				t.Errorf("subject = %v, want [%s]", d.Subject, worldDig)
			}
			if !reflect.DeepEqual(d.Evidence, tt.wantEvidence) {
				t.Errorf("evidence = %v, want %v", d.Evidence, tt.wantEvidence)
			}
			if d.Policy != tt.policy.Digest {
				t.Errorf("policy = %q, want %q", d.Policy, tt.policy.Digest)
			}
			if d.Rationale != tt.wantRationale {
				t.Errorf("rationale =\n %q\nwant\n %q", d.Rationale, tt.wantRationale)
			}
			if d.CreatedAt != "" {
				t.Errorf("CreatedAt = %q, want empty (recorder stamps it)", d.CreatedAt)
			}
			// NFR-1: byte-for-byte stability on replay, and independence
			// from the order the gate receipts arrive in.
			replay := Decide(tt.policy, intentDig, worldDig, recorded(t, tt.apply), reverse(gates))
			if !reflect.DeepEqual(d, replay) {
				t.Errorf("Decide not deterministic: %+v vs %+v", d, replay)
			}
		})
	}
}

func reverse(in []object.RecordedReceipt) []object.RecordedReceipt {
	out := make([]object.RecordedReceipt, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		out = append(out, in[i])
	}
	return out
}

func TestLandingOracleArgv(t *testing.T) {
	store, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	put := func(rec object.Receipt) string {
		t.Helper()
		dig, canon, err := object.Digest(rec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Put(canon); err != nil {
			t.Fatal(err)
		}
		return dig
	}

	recA := gateReceipt("pass")
	recA.Execution.Argv = []string{"python3", "-m", "pytest", "-q"}
	recB := gateReceipt("pass")
	recB.Execution.Argv = []string{"other-command"}
	recB.CreatedAt = "2026-01-01T00:00:02Z" // distinct digest
	otherWorld := gateReceipt("pass")
	otherWorld.World = "mv0:" + strings.Repeat("99", 32)
	otherWorld.Execution.Argv = []string{"loser-command"}
	nonSuite := applyReceipt("pass", 0)

	digA, digB := put(recA), put(recB)
	otherDig, applyDig := put(otherWorld), put(nonSuite)

	wantArgv := recA.Execution.Argv
	if digB < digA {
		wantArgv = recB.Execution.Argv
	}

	sel := object.Decision{
		Schema: object.SchemaDecision, Type: "SELECT", Intent: intentDig,
		Subject: []string{worldDig}, Policy: "mv0:pol",
	}

	t.Run("smallest digest wins, order-independent", func(t *testing.T) {
		for _, evidence := range [][]string{
			{digA, digB, otherDig, applyDig},
			{applyDig, otherDig, digB, digA},
		} {
			s := sel
			s.Evidence = evidence
			argv, err := LandingOracleArgv(store, s)
			if err != nil {
				t.Fatalf("LandingOracleArgv(%v): %v", evidence, err)
			}
			if !reflect.DeepEqual(argv, wantArgv) {
				t.Errorf("argv = %v, want %v", argv, wantArgv)
			}
		}
	})

	t.Run("no suite receipt for winner", func(t *testing.T) {
		s := sel
		s.Evidence = []string{otherDig, applyDig} // wrong world / wrong family only
		if _, err := LandingOracleArgv(store, s); err == nil {
			t.Fatal("want error, got nil")
		} else if !strings.Contains(err.Error(), "no suite receipt") {
			t.Errorf("error = %q, want 'no suite receipt'", err)
		}
	})

	t.Run("empty subject", func(t *testing.T) {
		s := sel
		s.Subject = nil
		if _, err := LandingOracleArgv(store, s); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

// A policy that gates NOTHING cannot admit. Every predicate loop is
// vacuously satisfied by an empty gate list, so the landing would otherwise
// be ADMITTED — and signed, and attested — on the strength of no gate at
// all. Only the v0 shape can express this (v1 validation requires a gate),
// and admission refuses it in both directions: `admit.Run` never builds the
// landing oracles, and the pure gate below fails closed for any caller that
// gets past it, replay included.
func TestDecideRefusesAPolicyWithNoHardGate(t *testing.T) {
	pol := compileV0(t, nil)
	if len(pol.Gates) != 0 {
		t.Fatalf("fixture compiled to %d gates, want 0", len(pol.Gates))
	}
	apply := recorded(t, applyReceipt("pass", 0))
	got := Decide(pol, intentDig, worldDig, apply, recordedAll(t, gateReceipt("pass")))
	if got.Type != TypeReject {
		t.Fatalf("type = %s, want REJECT: what cannot be attested must not be admitted", got.Type)
	}
	if !strings.Contains(got.Rationale, "declares no hard gate") {
		t.Errorf("rationale = %q", got.Rationale)
	}
	// Evidence is what the decision RESTED on. No gate was consulted, so no
	// gate receipt is cited — which is also exactly what `mvo audit` derives
	// from the policy's own (empty) selectors, so replay cannot diverge.
	if len(got.Evidence) != 1 || got.Evidence[0] != apply.Digest {
		t.Errorf("evidence = %v, want [%s]", got.Evidence, apply.Digest)
	}
}
