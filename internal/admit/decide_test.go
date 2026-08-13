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
)

const (
	worldDig  = "mv0:" + "1111111111111111111111111111111111111111111111111111111111111111"
	intentDig = "mv0:" + "2222222222222222222222222222222222222222222222222222222222222222"
	trunkTree = "git:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	landTree  = "git:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func testPolicy() object.Policy {
	return object.Policy{
		Schema:    object.SchemaPolicy,
		HardGates: []string{"suite-pass"},
		Ranking:   []string{"gate_pass", "wall_ms_asc"},
	}
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
		Result:      object.Result{Status: status, Artifacts: []string{"sha256:out", "sha256:err"}},
		Freshness:   object.Freshness{Basis: "construction", ValidFor: object.ValidFor{Tree: trunkTree, Env: "mv0:env"}},
		RecheckTier: "V1-replayable",
		Family:      FamilyLandingApply,
		Cost:        object.Cost{WallMS: 7},
		CreatedAt:   "2026-01-01T00:00:00Z",
	}
}

func gateReceipt(status string) object.Receipt {
	return object.Receipt{
		Schema:      object.SchemaReceipt,
		World:       worldDig,
		Oracle:      object.OracleRef{ID: "command", Version: "v0", Config: "mv0:cfg"},
		Execution:   object.Execution{Argv: []string{"true"}, ExitCode: 0, DurationMS: 11, IsolationTier: "T0-worktree"},
		Result:      object.Result{Status: status, Artifacts: []string{"sha256:out", "sha256:err"}},
		Freshness:   object.Freshness{Basis: "construction", ValidFor: object.ValidFor{Tree: landTree, Env: "mv0:env"}},
		RecheckTier: "V1-replayable",
		Family:      "suite",
		Cost:        object.Cost{WallMS: 11},
		CreatedAt:   "2026-01-01T00:00:01Z",
	}
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
	pol := testPolicy()
	unknownPol := object.Policy{
		Schema:    object.SchemaPolicy,
		HardGates: []string{"suite-pass", "mutation-score"},
		Ranking:   []string{"gate_pass"},
	}

	applyPass := applyReceipt("pass", 0)
	applyFail := applyReceipt("fail", 1)
	gatePass := gateReceipt("pass")
	gateFail := gateReceipt("fail")
	gateError := gateReceipt("error")

	applyPassDig := mustDigest(t, applyPass)
	applyFailDig := mustDigest(t, applyFail)

	sorted := func(digs ...string) []string {
		out := append([]string(nil), digs...)
		sort.Strings(out)
		return out
	}

	tests := []struct {
		name          string
		policy        object.Policy
		apply         object.Receipt
		gate          *object.Receipt
		wantType      string
		wantEvidence  []string
		wantRationale string
	}{
		{
			name: "escalate on apply conflict", policy: pol,
			apply: applyFail, gate: nil,
			wantType:     TypeEscalate,
			wantEvidence: []string{applyFailDig},
			wantRationale: fmt.Sprintf(
				"landing apply of %s onto trunk tree %s failed (exit 1); conflicts are never auto-resolved (CP-8) — conflict set in apply receipt artifacts",
				worldDig, trunkTree),
		},
		{
			name: "admit on gate pass", policy: pol,
			apply: applyPass, gate: &gatePass,
			wantType:     TypeAdmit,
			wantEvidence: sorted(applyPassDig, mustDigest(t, gatePass)),
			wantRationale: fmt.Sprintf(
				"landing gates [suite-pass] passed on tree %s; admitting %s", landTree, worldDig),
		},
		{
			name: "reject on gate fail", policy: pol,
			apply: applyPass, gate: &gateFail,
			wantType:     TypeReject,
			wantEvidence: sorted(applyPassDig, mustDigest(t, gateFail)),
			wantRationale: fmt.Sprintf(
				"landing gates [suite-pass] failed on tree %s; suite-pass (status=fail)", landTree),
		},
		{
			name: "reject on gate error", policy: pol,
			apply: applyPass, gate: &gateError,
			wantType:     TypeReject,
			wantEvidence: sorted(applyPassDig, mustDigest(t, gateError)),
			wantRationale: fmt.Sprintf(
				"landing gates [suite-pass] failed on tree %s; suite-pass (status=error)", landTree),
		},
		{
			name: "reject on nil gate", policy: pol,
			apply: applyPass, gate: nil,
			wantType:     TypeReject,
			wantEvidence: []string{applyPassDig},
			wantRationale: fmt.Sprintf(
				"landing gates [suite-pass] failed on tree %s; suite-pass (no landing suite receipt)", trunkTree),
		},
		{
			name: "unknown gate fails", policy: unknownPol,
			apply: applyPass, gate: &gatePass,
			wantType:     TypeReject,
			wantEvidence: sorted(applyPassDig, mustDigest(t, gatePass)),
			wantRationale: fmt.Sprintf(
				"landing gates [suite-pass,mutation-score] failed on tree %s; mutation-score (unknown gate)", landTree),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Decide(tt.policy, intentDig, worldDig, tt.apply, tt.gate)
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
			if want := mustDigest(t, tt.policy); d.Policy != want {
				t.Errorf("policy = %q, want %q", d.Policy, want)
			}
			if d.Rationale != tt.wantRationale {
				t.Errorf("rationale = %q, want %q", d.Rationale, tt.wantRationale)
			}
			if d.CreatedAt != "" {
				t.Errorf("CreatedAt = %q, want empty (recorder stamps it)", d.CreatedAt)
			}
			// NFR-1: byte-for-byte stability on replay.
			replay := Decide(tt.policy, intentDig, worldDig, tt.apply, tt.gate)
			if !reflect.DeepEqual(d, replay) {
				t.Errorf("Decide not deterministic: %+v vs %+v", d, replay)
			}
		})
	}
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
