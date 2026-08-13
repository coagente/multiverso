package race

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

const fixedTime = "2026-01-01T00:00:00Z"

func testPolicy() object.Policy {
	return object.Policy{
		Schema:    object.SchemaPolicy,
		HardGates: []string{GateSuitePass},
		Ranking:   []string{"gate_pass", "wall_ms_asc"},
	}
}

func mkWorld(t *testing.T, patch, outcome string) (object.World, string) {
	t.Helper()
	w := object.World{
		Schema:        object.SchemaWorld,
		Intent:        "mv0:intent",
		Tree:          "git:" + patch,
		Env:           "mv0:env",
		IsolationTier: "T0-worktree",
		Producer: object.Producer{
			Adapter: "script@v0", IdentityTier: "claimed", Role: "generator",
		},
		Patch:     "sha256:" + patch,
		Outcome:   outcome,
		CreatedAt: fixedTime,
	}
	dig, _, err := object.Digest(w)
	if err != nil {
		t.Fatalf("digest world %s: %v", patch, err)
	}
	return w, dig
}

func mkReceipt(t *testing.T, worldDig, status string, wallMS int64) (object.Receipt, string) {
	t.Helper()
	r := object.Receipt{
		Schema: object.SchemaReceipt,
		World:  worldDig,
		Oracle: object.OracleRef{ID: "command", Version: "v0", Config: "mv0:cfg"},
		Execution: object.Execution{
			Argv: []string{"true"}, ExitCode: 0, DurationMS: wallMS, IsolationTier: "T0-worktree",
		},
		Result:      object.Result{Status: status, Artifacts: []string{}},
		Freshness:   object.Freshness{Basis: "construction"},
		RecheckTier: "V1-replayable",
		Family:      "suite",
		Cost:        object.Cost{WallMS: wallMS},
		CreatedAt:   fixedTime,
	}
	dig, _, err := object.Digest(r)
	if err != nil {
		t.Fatalf("digest receipt for %s: %v", worldDig, err)
	}
	return r, dig
}

func TestDecide(t *testing.T) {
	policy := testPolicy()
	polDig, _, err := object.Digest(policy)
	if err != nil {
		t.Fatalf("digest policy: %v", err)
	}

	wA, digA := mkWorld(t, "patch-a", OutcomeCompleted)
	wB, digB := mkWorld(t, "patch-b", OutcomeCompleted)
	wC, digC := mkWorld(t, "patch-c", OutcomeConfigError)

	// Deterministic tie-break expectations depend on actual digest order.
	loDig, hiDig := digA, digB
	loWorld, hiWorld := wA, wB
	if digB < digA {
		loDig, hiDig = digB, digA
		loWorld, hiWorld = wB, wA
	}

	rAPass50, digRAPass50 := mkReceipt(t, digA, "pass", 50)
	rAFail50, digRAFail50 := mkReceipt(t, digA, "fail", 50)
	rBPass200, digRBPass200 := mkReceipt(t, digB, "pass", 200)
	rBFail200, digRBFail200 := mkReceipt(t, digB, "fail", 200)
	rLoPass100, digRLoPass100 := mkReceipt(t, loDig, "pass", 100)
	rHiPass100, digRHiPass100 := mkReceipt(t, hiDig, "pass", 100)

	sorted := func(ds ...string) []string { out := append([]string{}, ds...); sort.Strings(out); return out }

	tests := []struct {
		name          string
		worlds        []object.World
		receipts      []object.Receipt
		wantType      string
		wantSubject   []string
		wantEvidence  []string
		wantRationale []string // substrings
	}{
		{
			name:          "one pass one fail selects the passer even if slower",
			worlds:        []object.World{wA, wB},
			receipts:      []object.Receipt{rAFail50, rBPass200},
			wantType:      TypeSelect,
			wantSubject:   []string{digB, digA},
			wantEvidence:  sorted(digRAFail50, digRBPass200),
			wantRationale: []string{"1/2 worlds passed", digB, "wall_ms=200"},
		},
		{
			name:          "both pass, faster suite wins",
			worlds:        []object.World{wA, wB},
			receipts:      []object.Receipt{rAPass50, rBPass200},
			wantType:      TypeSelect,
			wantSubject:   []string{digA, digB},
			wantEvidence:  sorted(digRAPass50, digRBPass200),
			wantRationale: []string{"2/2 worlds passed", digA, "wall_ms=50"},
		},
		{
			name:          "equal wall_ms tie broken by world digest ascending",
			worlds:        []object.World{hiWorld, loWorld},
			receipts:      []object.Receipt{rHiPass100, rLoPass100},
			wantType:      TypeSelect,
			wantSubject:   []string{loDig, hiDig},
			wantEvidence:  sorted(digRLoPass100, digRHiPass100),
			wantRationale: []string{"2/2 worlds passed", loDig, "wall_ms=100"},
		},
		{
			name:          "zero pass rejects all with failing gates listed",
			worlds:        []object.World{wA, wB},
			receipts:      []object.Receipt{rAFail50, rBFail200},
			wantType:      TypeReject,
			wantSubject:   []string{digA, digB}, // wall_ms 50 < 200
			wantEvidence:  sorted(digRAFail50, digRBFail200),
			wantRationale: []string{"0/2 worlds passed", GateSuitePass, digA, digB, "status=fail"},
		},
		{
			name:          "config-error world loses to passing world",
			worlds:        []object.World{wC, wA},
			receipts:      []object.Receipt{rAPass50},
			wantType:      TypeSelect,
			wantSubject:   []string{digA, digC},
			wantEvidence:  []string{digRAPass50},
			wantRationale: []string{"1/2 worlds passed", digA},
		},
		{
			name:          "config-error world alone is rejected",
			worlds:        []object.World{wC},
			receipts:      nil,
			wantType:      TypeReject,
			wantSubject:   []string{digC},
			wantEvidence:  []string{},
			wantRationale: []string{"0/1 worlds passed", "outcome=" + OutcomeConfigError},
		},
		{
			name:          "completed world without receipt is rejected",
			worlds:        []object.World{wA},
			receipts:      nil,
			wantType:      TypeReject,
			wantSubject:   []string{digA},
			wantEvidence:  []string{},
			wantRationale: []string{"no suite receipt"},
		},
		{
			name:          "no worlds rejects",
			worlds:        nil,
			receipts:      nil,
			wantType:      TypeReject,
			wantSubject:   []string{},
			wantEvidence:  []string{},
			wantRationale: []string{"no candidate worlds"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Decide(policy, tt.worlds, tt.receipts)
			if d.Schema != object.SchemaDecision {
				t.Errorf("schema = %q, want %q", d.Schema, object.SchemaDecision)
			}
			if d.Type != tt.wantType {
				t.Errorf("type = %q, want %q", d.Type, tt.wantType)
			}
			if !reflect.DeepEqual(d.Subject, tt.wantSubject) {
				t.Errorf("subject = %v, want %v", d.Subject, tt.wantSubject)
			}
			if !reflect.DeepEqual(d.Evidence, tt.wantEvidence) {
				t.Errorf("evidence = %v, want %v", d.Evidence, tt.wantEvidence)
			}
			for _, sub := range tt.wantRationale {
				if !strings.Contains(d.Rationale, sub) {
					t.Errorf("rationale %q missing %q", d.Rationale, sub)
				}
			}
			if d.Policy != polDig {
				t.Errorf("policy = %q, want %q", d.Policy, polDig)
			}
			if len(tt.worlds) > 0 && d.Intent != tt.worlds[0].Intent {
				t.Errorf("intent = %q, want %q", d.Intent, tt.worlds[0].Intent)
			}
			// NFR-1: CreatedAt is the recorder's, never the decider's.
			if d.CreatedAt != "" {
				t.Errorf("created_at = %q, want empty from Decide", d.CreatedAt)
			}
		})
	}
}

// NFR-1: the decision is invariant under the ordering of worlds and
// receipts — replay from a ledger scan must reproduce it byte-for-byte.
func TestDecideOrderInvariant(t *testing.T) {
	policy := testPolicy()
	wA, digA := mkWorld(t, "patch-a", OutcomeCompleted)
	wB, digB := mkWorld(t, "patch-b", OutcomeCompleted)
	rA, _ := mkReceipt(t, digA, "pass", 100)
	rB, _ := mkReceipt(t, digB, "pass", 100)

	forward := Decide(policy, []object.World{wA, wB}, []object.Receipt{rA, rB})
	reversed := Decide(policy, []object.World{wB, wA}, []object.Receipt{rB, rA})
	if !reflect.DeepEqual(forward, reversed) {
		t.Errorf("Decide is order-dependent:\n forward: %+v\nreversed: %+v", forward, reversed)
	}
}
