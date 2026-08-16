package policy

// The allocation-sensitivity table (M2b decision 15). A key is
// ALLOCATION-SENSITIVE iff its value can change when a receipt is WITHHELD
// from a world that still passes every hard gate — which is precisely what
// an adaptive scheduler does, and which withholding monotonicity does NOT
// protect (it protects the pass set, not the ranking).

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

// TOTALITY. Every key in the closed vocabulary must be classified, because a
// future key that nobody classified would be scheduled adaptively by
// default — and the one key we have that behaves this way is wrong-signed,
// not merely noisy.
func TestAllocationSensitivityTableIsTotalOverTheKeyVocabulary(t *testing.T) {
	for _, k := range KnownKeys() {
		if _, classified := AllocationSensitive(k); !classified {
			t.Errorf("ranking key %q is not classified as allocation-sensitive or not: "+
				"a new key must be classified before it ships", k)
		}
	}
	// And the classification itself, key by key, with the reason each one
	// carries in the table.
	for key, want := range map[string]bool{
		KeyGatePass:        false, // structural
		KeyTestsPassedDesc: false, // reads ONE counted receipt's metric
		KeyCoverageDesc:    false, // reads ONE counted receipt's metric
		KeyWallMSAsc:       true,  // SUMS a world's counted receipts
		KeyCostAsc:         false, // reads the world object
		KeyPatchSizeAsc:    false, // reads the world object
		KeyWorldDigestAsc:  false, // structural
	} {
		got, classified := AllocationSensitive(key)
		if !classified {
			t.Errorf("%s: unclassified", key)
			continue
		}
		if got != want {
			t.Errorf("%s: allocation-sensitive = %v, want %v", key, got, want)
		}
	}
	// An unknown key is NOT "safe": it is a key nobody has thought about.
	if _, classified := AllocationSensitive("some_future_key"); classified {
		t.Error("an unknown key reported as classified")
	}
}

// A compiled policy names its offending keys, in ranking order, so a refusal
// can quote them. A no-op key — an unknown name in a v0 policy, which M0's
// rankLess ignored — is not listed: a key that orders nothing cannot be
// reordered by an allocation.
func TestAllocationSensitiveKeysOnCompiledPolicies(t *testing.T) {
	mk := func(t *testing.T, ranking []string) Policy {
		t.Helper()
		p := object.PolicyV1{
			Schema: object.SchemaPolicyV1, Name: "alloc",
			Oracles: []object.OracleSpec{
				{Name: "suite", Kind: KindPytestSuite, Argv: []string{}, Args: []string{}},
			},
			HardGates: []object.GateSpec{
				{Gate: GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
			},
			Ranking:    ranking,
			Escalation: object.EscalationSpec{RequireEvidence: []string{}},
		}
		b, err := object.Canonical(p)
		if err != nil {
			t.Fatalf("canonical: %v", err)
		}
		pol, err := Decode(b)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return pol
	}

	clean := mk(t, []string{KeyGatePass, KeyTestsPassedDesc})
	if got := clean.AllocationSensitiveKeys(); len(got) != 0 {
		t.Errorf("clean ranking reported %v", got)
	}
	dirty := mk(t, []string{KeyGatePass, KeyWallMSAsc, KeyTestsPassedDesc})
	if got := dirty.AllocationSensitiveKeys(); len(got) != 1 || got[0] != KeyWallMSAsc {
		t.Errorf("sensitive keys = %v, want [%s]", got, KeyWallMSAsc)
	}

	// The v0 dialect: M0's default ranking DOES declare wall_ms_asc, so it is
	// listed here — and TestUngatedEvidence below is the other half of rule
	// 25 that decides whether listing it is enough to refuse a race. Its
	// unknown key names compile to no-ops and are not listed.
	v0, err := Decode(mustCanonical(t, object.Policy{
		Schema:    object.SchemaPolicy,
		HardGates: []string{GateSuitePass},
		Ranking:   []string{KeyGatePass, KeyWallMSAsc, "some_unknown_key"},
	}))
	if err != nil {
		t.Fatalf("decode v0: %v", err)
	}
	got := v0.AllocationSensitiveKeys()
	if len(got) != 1 || got[0] != KeyWallMSAsc {
		t.Errorf("v0 sensitive keys = %v, want only [%s] (a no-op key orders nothing)", got, KeyWallMSAsc)
	}
}

// The SHIPPED DEFAULT is not allocation-sensitive, which is what lets the
// default configuration be scheduled adaptively at all — and its digest does
// not move: M2b adds no policy field.
func TestShippedDefaultIsSchedulable(t *testing.T) {
	pol, err := Decode(mustCanonical(t, Default()))
	if err != nil {
		t.Fatalf("decode default: %v", err)
	}
	if got := pol.AllocationSensitiveKeys(); len(got) != 0 {
		t.Errorf("the shipped default declares allocation-sensitive keys %v", got)
	}
}

// THE SECOND HALF OF RULE 25, and the half that decides whether a listed key
// is actually steerable. Decision 15's definition is "the key's value can
// change when a receipt is withheld FROM A WORLD THAT STILL PASSES EVERY HARD
// GATE". Withholding a hard-gated receipt makes its metric absent, the gate
// fails, and the world leaves the pass set — where no ranking key compares
// it. So the danger needs evidence the policy COUNTS but no hard gate BACKS.
func TestUngatedEvidence(t *testing.T) {
	compile := func(p object.PolicyV1) Policy {
		t.Helper()
		pol, err := Decode(mustCanonical(t, p))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return pol
	}
	suite := object.OracleSpec{Name: "suite", Kind: KindPytestSuite, Argv: []string{}, Args: []string{}}
	collect := object.OracleSpec{Name: "collect", Kind: KindPytestCollect, Argv: []string{}, Args: []string{}}

	// Fully gated: every counted selector is a hard gate's. Nothing is
	// withholdable from a passing world.
	gatedOnly := compile(object.PolicyV1{
		Schema: object.SchemaPolicyV1, Name: "gated",
		Oracles: []object.OracleSpec{collect, suite},
		HardGates: []object.GateSpec{
			{Gate: GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{KeyGatePass, KeyWallMSAsc, KeyTestsPassedDesc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}},
	})
	if got := gatedOnly.UngatedEvidence(); len(got) != 0 {
		t.Errorf("a fully gated policy reports ungated evidence %v", got)
	}

	// require_evidence names an oracle no gate reads: a world can decline
	// `collect`, keep its pass, and carry a smaller wall_ms sum than the
	// sibling that bought it. THIS is the shape rule 25 refuses.
	requireOnly := compile(object.PolicyV1{
		Schema: object.SchemaPolicyV1, Name: "require",
		Oracles: []object.OracleSpec{collect, suite},
		HardGates: []object.GateSpec{
			{Gate: GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{KeyGatePass, KeyWallMSAsc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{"collect"}},
	})
	if got := requireOnly.UngatedEvidence(); len(got) != 1 || got[0] != "collect" {
		t.Errorf("ungated evidence = %v, want [collect]", got)
	}

	// A RANKING KEY reading an ungated oracle is the same shape by a
	// different door.
	keyOnly := compile(object.PolicyV1{
		Schema: object.SchemaPolicyV1, Name: "keyed",
		Oracles: []object.OracleSpec{collect, suite},
		HardGates: []object.GateSpec{
			{Gate: GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
		},
		Ranking:    []string{KeyGatePass, KeyWallMSAsc, KeyTestsPassedDesc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}},
	})
	if got := keyOnly.UngatedEvidence(); len(got) != 1 || got[0] != "suite" {
		t.Errorf("ungated evidence = %v, want [suite] (tests_passed_desc reads it, no gate does)", got)
	}

	// The v0 command policy M0 pins: one gate, one selector, and it declares
	// no instances at all. Nothing is withholdable, so `mvo race
	// --oracle-cmd` keeps working under the adaptive arm — refusing it would
	// have bricked M0's own quickstart to prevent an attack its shape makes
	// impossible.
	cmd, err := Decode(mustCanonical(t, Command([]string{"python3", "-m", "pytest"}, 60000)))
	if err != nil {
		t.Fatalf("decode command policy: %v", err)
	}
	if got := cmd.AllocationSensitiveKeys(); len(got) != 1 || got[0] != KeyWallMSAsc {
		t.Fatalf("the command policy's sensitive keys = %v, want [%s]", got, KeyWallMSAsc)
	}
	if got := cmd.UngatedEvidence(); len(got) != 0 {
		t.Errorf("the synthesized command policy reports ungated evidence %v", got)
	}
}

// on_evidence_incomplete decodes, compiles, and ships OFF. Its ABSENCE from
// the shipped default's canonical bytes is what keeps the M1f/M2a digest
// where it is: a field that always serialized would mint a new digest for
// every existing policy — a silent replay break dressed as a schema addition.
func TestOnEvidenceIncompleteDecodesAndShipsOff(t *testing.T) {
	canon := mustCanonical(t, Default())
	if strings.Contains(string(canon), "on_evidence_incomplete") {
		t.Fatal("the shipped default serializes on_evidence_incomplete; every pre-M2b policy digest would move")
	}
	def, err := Decode(canon)
	if err != nil {
		t.Fatalf("decode default: %v", err)
	}
	if def.Esc.OnEvidenceIncomplete {
		t.Error("the shipped default compiles the rule ON")
	}

	spec := Default()
	spec.Escalation.OnEvidenceIncomplete = true
	on, err := Decode(mustCanonical(t, spec))
	if err != nil {
		t.Fatalf("decode a policy declaring the rule: %v", err)
	}
	if !on.Esc.OnEvidenceIncomplete {
		t.Error("a policy declaring on_evidence_incomplete compiled it OFF")
	}
	// And declaring it MOVES the digest, which is the correct behaviour: it
	// is a different policy, and the pinned digest is what a decision is
	// judged by.
	if on.Digest == def.Digest {
		t.Error("declaring an escalation rule did not change the policy digest")
	}
}

func mustCanonical(t *testing.T, v any) []byte {
	t.Helper()
	b, err := object.Canonical(v)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	return b
}
