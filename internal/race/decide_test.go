package race

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

const fixedTime = "2026-01-01T00:00:00Z"

// --- fixtures ---------------------------------------------------------
//
// Every fixture receipt is BOUND to the world it names (valid_for ==
// {world.tree, world.env}): an unbound receipt is not evidence (M1e
// decision 10), so a test that wants a decision must record honest
// freshness — and the tests that want the unbound path say so explicitly.

func compileV0Policy(t *testing.T, gates, ranking []string) policy.Policy {
	t.Helper()
	p := object.Policy{Schema: object.SchemaPolicy, HardGates: gates, Ranking: ranking}
	_, canon, err := object.Digest(p)
	if err != nil {
		t.Fatalf("digest v0 policy: %v", err)
	}
	pol, err := policy.Decode(canon)
	if err != nil {
		t.Fatalf("decode v0 policy: %v", err)
	}
	return pol
}

func testPolicy(t *testing.T) policy.Policy {
	t.Helper()
	return compileV0Policy(t, []string{GateSuitePass}, []string{"gate_pass", "wall_ms_asc"})
}

// suiteSpec/collectSpec are the two declared oracle instances every v1
// fixture policy uses; their config digests are what receipts must carry.
// suiteSpec measures coverage, as the shipped default does: a coverage gate
// or a coverage_desc key is only authorable against an instance configured
// to produce coverage_bp, so the fixture must be one.
func suiteSpec() object.OracleSpec {
	return object.OracleSpec{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{}, Coverage: true}
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

func compileV1(t *testing.T, gates []object.GateSpec, ranking []string, esc object.EscalationSpec) policy.Policy {
	t.Helper()
	if esc.RequireEvidence == nil {
		esc.RequireEvidence = []string{}
	}
	p := object.PolicyV1{
		Schema:     object.SchemaPolicyV1,
		Name:       "fixture",
		Oracles:    []object.OracleSpec{collectSpec(), suiteSpec()},
		HardGates:  gates,
		Ranking:    ranking,
		Escalation: esc,
	}
	_, canon, err := object.Digest(p)
	if err != nil {
		t.Fatalf("digest v1 policy: %v", err)
	}
	pol, err := policy.Decode(canon)
	if err != nil {
		t.Fatalf("decode v1 policy: %v", err)
	}
	return pol
}

func gate(name, oracle string, threshold int64) object.GateSpec {
	return object.GateSpec{Gate: name, Oracle: oracle, Basis: object.BasisConstruction, Threshold: threshold}
}

func mkWorld(t *testing.T, name, outcome string, opts ...func(*object.World)) object.RecordedWorld {
	t.Helper()
	w := object.World{
		Schema:        object.SchemaWorld,
		Intent:        "mv0:intent",
		Tree:          "git:" + name,
		Env:           "mv0:env",
		IsolationTier: object.TierT0Worktree,
		Producer: object.Producer{
			Adapter: "script@v0", IdentityTier: "claimed", Role: "generator",
		},
		Context:    "sha256:ctx-" + name,
		Patch:      "sha256:" + name,
		PatchBytes: int64(len(name)),
		Trace:      "sha256:trace-" + name,
		Cost:       object.RunCost{WallMS: 1, Source: "none"},
		Outcome:    outcome,
		CreatedAt:  fixedTime,
	}
	for _, opt := range opts {
		opt(&w)
	}
	dig, _, err := object.Digest(w)
	if err != nil {
		t.Fatalf("digest world %s: %v", name, err)
	}
	return object.RecordedWorld{Digest: dig, World: w}
}

// mkSuiteV0 is an M0-shaped suite receipt: family "suite", command oracle,
// no metrics — exactly what M0 through M1d recorded.
func mkSuiteV0(t *testing.T, rw object.RecordedWorld, status string, wallMS int64) object.RecordedReceipt {
	t.Helper()
	return mkReceipt(t, rw, "command", "mv0:cfg", policy.FamilySuite, status, wallMS, nil)
}

func mkReceipt(t *testing.T, rw object.RecordedWorld, id, cfg, family, status string,
	wallMS int64, metrics map[string]int64, opts ...func(*object.Receipt)) object.RecordedReceipt {
	t.Helper()
	if metrics == nil {
		metrics = map[string]int64{}
	}
	exit := 0
	if status != "pass" {
		exit = 1
	}
	r := object.Receipt{
		Schema: object.SchemaReceipt,
		World:  rw.Digest,
		Oracle: object.OracleRef{ID: id, Version: "v0", Config: cfg},
		Execution: object.Execution{
			Argv: []string{"true"}, ExitCode: exit, DurationMS: wallMS,
			IsolationTier: object.TierT0Worktree,
		},
		Result: object.Result{
			Status: status, Metrics: metrics, Tools: map[string]string{}, Artifacts: []string{},
		},
		Freshness: object.Freshness{
			Basis:    object.BasisConstruction,
			ValidFor: object.ValidFor{Tree: rw.World.Tree, Env: rw.World.Env},
		},
		RecheckTier: "V1-replayable",
		Family:      family,
		Cost:        object.Cost{WallMS: wallMS},
		CreatedAt:   fixedTime,
	}
	for _, opt := range opts {
		opt(&r)
	}
	dig, _, err := object.Digest(r)
	if err != nil {
		t.Fatalf("digest receipt for %s: %v", rw.Digest, err)
	}
	return object.RecordedReceipt{Digest: dig, Receipt: r}
}

func digests(recs ...object.RecordedReceipt) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Digest)
	}
	sort.Strings(out)
	return out
}

// --- M0 compatibility: the v0 dialect, byte-for-byte ------------------

func TestDecideV0Goldens(t *testing.T) {
	pol := testPolicy(t)

	wA := mkWorld(t, "patch-a", OutcomeCompleted)
	wB := mkWorld(t, "patch-b", OutcomeCompleted)
	wC := mkWorld(t, "patch-c", OutcomeConfigError)

	lo, hi := wA, wB
	if wB.Digest < wA.Digest {
		lo, hi = wB, wA
	}

	rAPass50 := mkSuiteV0(t, wA, "pass", 50)
	rAFail50 := mkSuiteV0(t, wA, "fail", 50)
	rBPass200 := mkSuiteV0(t, wB, "pass", 200)
	rBFail200 := mkSuiteV0(t, wB, "fail", 200)
	rLoPass100 := mkSuiteV0(t, lo, "pass", 100)
	rHiPass100 := mkSuiteV0(t, hi, "pass", 100)

	tests := []struct {
		name          string
		worlds        []object.RecordedWorld
		receipts      []object.RecordedReceipt
		wantType      string
		wantSubject   []string
		wantEvidence  []string
		wantRationale string
	}{
		{
			name:         "one pass one fail selects the passer even if slower",
			worlds:       []object.RecordedWorld{wA, wB},
			receipts:     []object.RecordedReceipt{rAFail50, rBPass200},
			wantType:     TypeSelect,
			wantSubject:  []string{wB.Digest, wA.Digest},
			wantEvidence: digests(rAFail50, rBPass200),
			wantRationale: fmt.Sprintf(
				"1/2 worlds passed hard gates [suite-pass]; selected %s by ranking [gate_pass,wall_ms_asc] (wall_ms=200)",
				wB.Digest),
		},
		{
			name:         "both pass, faster suite wins",
			worlds:       []object.RecordedWorld{wA, wB},
			receipts:     []object.RecordedReceipt{rAPass50, rBPass200},
			wantType:     TypeSelect,
			wantSubject:  []string{wA.Digest, wB.Digest},
			wantEvidence: digests(rAPass50, rBPass200),
			wantRationale: fmt.Sprintf(
				"2/2 worlds passed hard gates [suite-pass]; selected %s by ranking [gate_pass,wall_ms_asc] (wall_ms=50)",
				wA.Digest),
		},
		{
			name:         "equal wall_ms tie broken by world digest ascending",
			worlds:       []object.RecordedWorld{hi, lo},
			receipts:     []object.RecordedReceipt{rHiPass100, rLoPass100},
			wantType:     TypeSelect,
			wantSubject:  []string{lo.Digest, hi.Digest},
			wantEvidence: digests(rLoPass100, rHiPass100),
			wantRationale: fmt.Sprintf(
				"2/2 worlds passed hard gates [suite-pass]; selected %s by ranking [gate_pass,wall_ms_asc] (wall_ms=100)",
				lo.Digest),
		},
		{
			name:         "zero pass rejects all with failing gates listed",
			worlds:       []object.RecordedWorld{wA, wB},
			receipts:     []object.RecordedReceipt{rAFail50, rBFail200},
			wantType:     TypeReject,
			wantSubject:  []string{wA.Digest, wB.Digest},
			wantEvidence: digests(rAFail50, rBFail200),
			wantRationale: fmt.Sprintf(
				"0/2 worlds passed hard gates [suite-pass]; %s failed [suite-pass] (status=fail); %s failed [suite-pass] (status=fail)",
				wA.Digest, wB.Digest),
		},
		{
			name:         "config-error world loses to passing world",
			worlds:       []object.RecordedWorld{wC, wA},
			receipts:     []object.RecordedReceipt{rAPass50},
			wantType:     TypeSelect,
			wantSubject:  []string{wA.Digest, wC.Digest},
			wantEvidence: digests(rAPass50),
			wantRationale: fmt.Sprintf(
				"1/2 worlds passed hard gates [suite-pass]; selected %s by ranking [gate_pass,wall_ms_asc] (wall_ms=50)",
				wA.Digest),
		},
		{
			name:        "config-error world alone is rejected",
			worlds:      []object.RecordedWorld{wC},
			wantType:    TypeReject,
			wantSubject: []string{wC.Digest},
			wantRationale: fmt.Sprintf(
				"0/1 worlds passed hard gates [suite-pass]; %s failed [suite-pass] (outcome=%s)",
				wC.Digest, OutcomeConfigError),
		},
		{
			name:        "completed world without receipt is rejected",
			worlds:      []object.RecordedWorld{wA},
			wantType:    TypeReject,
			wantSubject: []string{wA.Digest},
			wantRationale: fmt.Sprintf(
				"0/1 worlds passed hard gates [suite-pass]; %s failed [suite-pass] (no suite receipt)", wA.Digest),
		},
		{
			name:          "no worlds rejects",
			wantType:      TypeReject,
			wantSubject:   []string{},
			wantRationale: "no candidate worlds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Decide(pol, tt.worlds, tt.receipts)
			if d.Schema != object.SchemaDecision {
				t.Errorf("schema = %q, want %q", d.Schema, object.SchemaDecision)
			}
			if d.Type != tt.wantType {
				t.Errorf("type = %q, want %q", d.Type, tt.wantType)
			}
			want := tt.wantSubject
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(d.Subject, want) {
				t.Errorf("subject = %v, want %v", d.Subject, want)
			}
			wantEv := tt.wantEvidence
			if wantEv == nil {
				wantEv = []string{}
			}
			if !reflect.DeepEqual(d.Evidence, wantEv) {
				t.Errorf("evidence = %v, want %v", d.Evidence, wantEv)
			}
			if d.Rationale != tt.wantRationale {
				t.Errorf("rationale =\n %q\nwant\n %q", d.Rationale, tt.wantRationale)
			}
			if d.Policy != pol.Digest {
				t.Errorf("policy = %q, want %q", d.Policy, pol.Digest)
			}
			if len(tt.worlds) > 0 && d.Intent != tt.worlds[0].World.Intent {
				t.Errorf("intent = %q, want %q", d.Intent, tt.worlds[0].World.Intent)
			}
			// NFR-1: CreatedAt is the recorder's, never the decider's.
			if d.CreatedAt != "" {
				t.Errorf("created_at = %q, want empty from Decide", d.CreatedAt)
			}
		})
	}
}

// M0 failed closed on a gate it could not evaluate, and a pinned v0 policy
// must keep doing so — the strictness upgrade applies to the shape that can
// express the vocabulary, never retroactively (M1e decision 7).
func TestDecideV0UnknownGateFailsClosed(t *testing.T) {
	pol := compileV0Policy(t, []string{"suite-pass", "coverage-please"}, []string{"gate_pass", "wall_ms_asc"})
	w := mkWorld(t, "patch-a", OutcomeCompleted)
	r := mkSuiteV0(t, w, "pass", 10)

	d := Decide(pol, []object.RecordedWorld{w}, []object.RecordedReceipt{r})
	if d.Type != TypeReject {
		t.Fatalf("type = %q, want REJECT (an unevaluable gate must not admit)", d.Type)
	}
	want := fmt.Sprintf("0/1 worlds passed hard gates [suite-pass,coverage-please]; %s failed [coverage-please] (status=pass)", w.Digest)
	if d.Rationale != want {
		t.Errorf("rationale =\n %q\nwant\n %q", d.Rationale, want)
	}
}

// M0's rankLess ignored key names it did not know; replay must keep them
// no-ops, whether or not M1e later gave the name a meaning.
func TestDecideV0UnknownKeyIsNoOp(t *testing.T) {
	pol := compileV0Policy(t, []string{"suite-pass"},
		[]string{"gate_pass", "tests_passed_desc", "wall_ms_asc"})
	slow := mkWorld(t, "patch-slow", OutcomeCompleted)
	fast := mkWorld(t, "patch-fast", OutcomeCompleted)
	// The slower world has MORE passing tests: a v1 policy would rank it
	// first, a v0 policy must not see the key at all.
	rSlow := mkReceipt(t, slow, "command", "mv0:cfg", policy.FamilySuite, "pass", 200,
		map[string]int64{policy.MetricTestsPassed: 99})
	rFast := mkReceipt(t, fast, "command", "mv0:cfg", policy.FamilySuite, "pass", 10,
		map[string]int64{policy.MetricTestsPassed: 1})

	d := Decide(pol, []object.RecordedWorld{slow, fast}, []object.RecordedReceipt{rSlow, rFast})
	if d.Subject[0] != fast.Digest {
		t.Errorf("winner = %s, want the faster world %s (tests_passed_desc is a no-op in v0)",
			d.Subject[0], fast.Digest)
	}
	if !strings.Contains(d.Rationale, "ranking [gate_pass,tests_passed_desc,wall_ms_asc]") {
		t.Errorf("rationale %q must print the AUTHORED v0 ranking", d.Rationale)
	}
}

// A v0 SELECT with no gates at all still prints M0's wall_ms field, and a
// world with no receipt still ranks last with M0's MaxInt64.
func TestDecideV0NoGatesPrintsMaxWall(t *testing.T) {
	pol := compileV0Policy(t, nil, []string{"gate_pass", "wall_ms_asc"})
	w := mkWorld(t, "patch-a", OutcomeCompleted)
	d := Decide(pol, []object.RecordedWorld{w}, nil)
	want := fmt.Sprintf("1/1 worlds passed hard gates []; selected %s by ranking [gate_pass,wall_ms_asc] (wall_ms=%d)",
		w.Digest, int64(math.MaxInt64))
	if d.Rationale != want {
		t.Errorf("rationale =\n %q\nwant\n %q", d.Rationale, want)
	}
}

// --- v1: the M1e sentences --------------------------------------------

func TestDecideV1Rationales(t *testing.T) {
	suiteCfg := cfgDigest(t, suiteSpec())
	pol := compileV1(t,
		[]object.GateSpec{gate(policy.GateStatusPass, "suite", 0)},
		[]string{policy.KeyTestsPassedDesc, policy.KeyWallMSAsc},
		object.EscalationSpec{})

	rich := mkWorld(t, "patch-rich", OutcomeCompleted)
	poor := mkWorld(t, "patch-poor", OutcomeCompleted)
	broken := mkWorld(t, "patch-broken", OutcomeCompleted)
	rRich := mkReceipt(t, rich, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 500,
		map[string]int64{policy.MetricTestsPassed: 10})
	rPoor := mkReceipt(t, poor, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 5,
		map[string]int64{policy.MetricTestsPassed: 8})
	rBroken := mkReceipt(t, broken, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "fail", 7,
		map[string]int64{policy.MetricTestsPassed: 0})

	t.Run("two passing: the SECOND key decides and the sentence says so", func(t *testing.T) {
		d := Decide(pol, []object.RecordedWorld{rich, poor}, []object.RecordedReceipt{rRich, rPoor})
		want := fmt.Sprintf(
			"2/2 worlds passed hard gates [status-pass@suite]; selected %s over %s at ranking key 2 tests_passed_desc (10 > 8); ranking [gate_pass,tests_passed_desc,wall_ms_asc,world_digest_asc]",
			rich.Digest, poor.Digest)
		if d.Type != TypeSelect || d.Rationale != want {
			t.Errorf("type=%s rationale =\n %q\nwant\n %q", d.Type, d.Rationale, want)
		}
	})

	t.Run("sole passer", func(t *testing.T) {
		d := Decide(pol, []object.RecordedWorld{rich, broken}, []object.RecordedReceipt{rRich, rBroken})
		want := fmt.Sprintf(
			"1/2 worlds passed hard gates [status-pass@suite]; selected %s (sole world passing all hard gates); ranking [gate_pass,tests_passed_desc,wall_ms_asc,world_digest_asc]",
			rich.Digest)
		if d.Rationale != want {
			t.Errorf("rationale =\n %q\nwant\n %q", d.Rationale, want)
		}
	})

	t.Run("reject names the FIRST failing gate and its reason", func(t *testing.T) {
		d := Decide(pol, []object.RecordedWorld{broken}, []object.RecordedReceipt{rBroken})
		want := fmt.Sprintf("0/1 worlds passed hard gates [status-pass@suite]; %s failed [status-pass@suite] (status=fail)",
			broken.Digest)
		if d.Type != TypeReject || d.Rationale != want {
			t.Errorf("type=%s rationale =\n %q\nwant\n %q", d.Type, d.Rationale, want)
		}
	})
}

// The ladder short-circuits: a world that fails gate 1 never reports gate 2
// as a failure, because its oracle was never run (M1e decision 12).
func TestDecideV1LadderShortCircuits(t *testing.T) {
	collectCfg := cfgDigest(t, collectSpec())
	suiteCfg := cfgDigest(t, suiteSpec())
	pol := compileV1(t, []object.GateSpec{
		gate(policy.GateCollectNonempty, "collect", 0),
		gate(policy.GateStatusPass, "suite", 0),
	}, nil, object.EscalationSpec{})

	w := mkWorld(t, "patch-wipe", OutcomeCompleted)
	// pytest exit 5: no tests collected. Non-zero is fail, never pass, and
	// collected_total is recorded as 0 so the gate fails with a REASON.
	rCollect := mkReceipt(t, w, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "fail", 30,
		map[string]int64{policy.MetricCollectedTotal: 0}, func(r *object.Receipt) { r.Execution.ExitCode = 5 })

	tr := Trace(pol, []object.RecordedWorld{w}, []object.RecordedReceipt{rCollect})
	c := tr.Candidates[0]
	if c.Gates[0].Result != policy.GateFail || c.Gates[0].Detail != "collected_total=0 (exit 5)" {
		t.Errorf("gate 1 = %+v, want fail with the exit-5 reason", c.Gates[0])
	}
	if c.Gates[1].Result != policy.GateNotEvaluated {
		t.Errorf("gate 2 = %+v, want not-evaluated (the ladder stopped)", c.Gates[1])
	}
	if got := tr.Rationale; !strings.Contains(got, "failed [collect-nonempty@collect] (collected_total=0 (exit 5))") {
		t.Errorf("rationale %q must name the first failing gate", got)
	}
	// A suite receipt that exists anyway must not resurrect the world.
	rSuite := mkReceipt(t, w, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10, nil)
	if d := Decide(pol, []object.RecordedWorld{w}, []object.RecordedReceipt{rCollect, rSuite}); d.Type != TypeReject {
		t.Errorf("type = %s, want REJECT: a green suite cannot launder a deleted test file", d.Type)
	}
}

// --- escalation --------------------------------------------------------

func TestEscalationRules(t *testing.T) {
	suiteCfg := cfgDigest(t, suiteSpec())
	collectCfg := cfgDigest(t, collectSpec())

	pass1 := mkWorld(t, "patch-x", OutcomeCompleted)
	pass2 := mkWorld(t, "patch-y", OutcomeCompleted)
	crashed := mkWorld(t, "patch-crash", object.OutcomeCrash)

	rPass1 := mkReceipt(t, pass1, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10,
		map[string]int64{policy.MetricTestsPassed: 8})
	rPass2 := mkReceipt(t, pass2, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10,
		map[string]int64{policy.MetricTestsPassed: 8})
	rErr := mkReceipt(t, pass1, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "error", 10, nil)
	rCollect1 := mkReceipt(t, pass1, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 3,
		map[string]int64{policy.MetricCollectedTotal: 8})

	statusGate := []object.GateSpec{gate(policy.GateStatusPass, "suite", 0)}
	// No wall_ms_asc: wall time is noise, and a tie fixture must be
	// deterministic.
	tieRanking := []string{policy.KeyTestsPassedDesc}

	tests := []struct {
		name       string
		pol        policy.Policy
		worlds     []object.RecordedWorld
		receipts   []object.RecordedReceipt
		wantType   string
		wantRule   string
		wantDetail string
	}{
		{
			name:       "on_ranking_tie fires when only the digest could order",
			pol:        compileV1(t, statusGate, tieRanking, object.EscalationSpec{OnRankingTie: true}),
			worlds:     []object.RecordedWorld{pass1, pass2},
			receipts:   []object.RecordedReceipt{rPass1, rPass2},
			wantType:   TypeEscalate,
			wantRule:   RuleOnRankingTie,
			wantDetail: "tie on every ranking key [gate_pass,tests_passed_desc]; only world_digest_asc would order them",
		},
		{
			name:       "min_candidates_passing fires below the floor",
			pol:        compileV1(t, statusGate, tieRanking, object.EscalationSpec{MinCandidatesPassing: 2}),
			worlds:     []object.RecordedWorld{pass1},
			receipts:   []object.RecordedReceipt{rPass1},
			wantType:   TypeEscalate,
			wantRule:   RuleMinCandidatesPassing,
			wantDetail: "1 of 1 worlds passed, policy requires at least 2",
		},
		{
			name: "require_evidence fires when the winner lacks a required oracle",
			pol: compileV1(t, statusGate, tieRanking,
				object.EscalationSpec{RequireEvidence: []string{"collect"}}),
			worlds:     []object.RecordedWorld{pass1},
			receipts:   []object.RecordedReceipt{rPass1},
			wantType:   TypeEscalate,
			wantRule:   RuleRequireEvidence,
			wantDetail: "has no counted receipt from required oracle \"collect\"",
		},
		{
			name: "require_evidence is satisfied by a counted receipt",
			pol: compileV1(t, statusGate, tieRanking,
				object.EscalationSpec{RequireEvidence: []string{"collect"}}),
			worlds:   []object.RecordedWorld{pass1},
			receipts: []object.RecordedReceipt{rPass1, rCollect1},
			wantType: TypeSelect,
		},
		{
			name:       "all_worlds_failed_machinery fires on an errored oracle",
			pol:        compileV1(t, statusGate, tieRanking, object.EscalationSpec{OnAllWorldsFailedMachinery: true}),
			worlds:     []object.RecordedWorld{pass1},
			receipts:   []object.RecordedReceipt{rErr},
			wantType:   TypeEscalate,
			wantRule:   RuleAllWorldsFailedMachinery,
			wantDetail: "no world produced conclusive evidence (" + pass1.Digest + " status=error)",
		},
		{
			name:       "all_worlds_failed_machinery fires on a world that never completed",
			pol:        compileV1(t, statusGate, tieRanking, object.EscalationSpec{OnAllWorldsFailedMachinery: true}),
			worlds:     []object.RecordedWorld{crashed},
			wantType:   TypeEscalate,
			wantRule:   RuleAllWorldsFailedMachinery,
			wantDetail: "no world produced conclusive evidence (" + crashed.Digest + " outcome=CRASH)",
		},
		{
			name:     "a measured failure is a REJECT, not an escalation",
			pol:      compileV1(t, statusGate, tieRanking, object.EscalationSpec{OnAllWorldsFailedMachinery: true}),
			worlds:   []object.RecordedWorld{pass1},
			receipts: []object.RecordedReceipt{mkReceipt(t, pass1, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "fail", 10, nil)},
			wantType: TypeReject,
		},
		{
			name:     "rules off leave the decision alone",
			pol:      compileV1(t, statusGate, tieRanking, object.EscalationSpec{}),
			worlds:   []object.RecordedWorld{pass1, pass2},
			receipts: []object.RecordedReceipt{rPass1, rPass2},
			wantType: TypeSelect,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := Trace(tt.pol, tt.worlds, tt.receipts)
			if tr.Type != tt.wantType {
				t.Fatalf("type = %q, want %q (rationale %q)", tr.Type, tt.wantType, tr.Rationale)
			}
			if tr.Escalation.Rule != tt.wantRule {
				t.Fatalf("rule = %q, want %q", tr.Escalation.Rule, tt.wantRule)
			}
			if tt.wantRule == "" {
				return
			}
			if !strings.Contains(tr.Escalation.Detail, tt.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", tr.Escalation.Detail, tt.wantDetail)
			}
			// The ESCALATE sentence carries the rule AND the verdict it
			// displaced, so the human sees both.
			prefix := fmt.Sprintf("escalated by policy rule %s: %s; ", tr.Escalation.Rule, tr.Escalation.Detail)
			if !strings.HasPrefix(tr.Rationale, prefix) {
				t.Errorf("rationale %q must start with %q", tr.Rationale, prefix)
			}
			if strings.HasSuffix(tr.Rationale, "; ") {
				t.Errorf("rationale %q lost the would-have-been sentence", tr.Rationale)
			}
		})
	}
}

// Precedence is fixed: the first matching rule wins and supplies the
// reason, so two rules that both hold cannot make the outcome depend on
// evaluation accidents.
func TestEscalationPrecedence(t *testing.T) {
	suiteCfg := cfgDigest(t, suiteSpec())
	w1 := mkWorld(t, "patch-x", OutcomeCompleted)
	w2 := mkWorld(t, "patch-y", OutcomeCompleted)
	r1 := mkReceipt(t, w1, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10,
		map[string]int64{policy.MetricTestsPassed: 8})
	r2 := mkReceipt(t, w2, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10,
		map[string]int64{policy.MetricTestsPassed: 8})

	// require_evidence (rule 2), min_candidates_passing (rule 3) and
	// on_ranking_tie (rule 4) all hold at once.
	pol := compileV1(t,
		[]object.GateSpec{gate(policy.GateStatusPass, "suite", 0)},
		[]string{policy.KeyTestsPassedDesc},
		object.EscalationSpec{
			RequireEvidence:      []string{"collect"},
			MinCandidatesPassing: 3,
			OnRankingTie:         true,
		})
	tr := Trace(pol, []object.RecordedWorld{w1, w2}, []object.RecordedReceipt{r1, r2})
	if tr.Escalation.Rule != RuleRequireEvidence {
		t.Errorf("rule = %q, want %q (rule 2 precedes 3 and 4)", tr.Escalation.Rule, RuleRequireEvidence)
	}
}

// --- purity ------------------------------------------------------------

// NFR-1: the decision is invariant under the ordering of worlds and
// receipts — replay from a ledger scan must reproduce it byte-for-byte.
func TestDecideOrderInvariant(t *testing.T) {
	suiteCfg := cfgDigest(t, suiteSpec())
	collectCfg := cfgDigest(t, collectSpec())
	pol := compileV1(t, []object.GateSpec{
		gate(policy.GateCollectNonempty, "collect", 0),
		gate(policy.GateStatusPass, "suite", 0),
	}, []string{policy.KeyTestsPassedDesc, policy.KeyWallMSAsc, policy.KeyPatchSizeAsc},
		object.EscalationSpec{OnAllWorldsFailedMachinery: true})

	var worlds []object.RecordedWorld
	var receipts []object.RecordedReceipt
	for i, spec := range []struct {
		outcome  string
		status   string
		wall     int64
		passed   int64
		collects int64
	}{
		{OutcomeCompleted, "pass", 30, 8, 8},
		{OutcomeCompleted, "pass", 10, 8, 8},
		{OutcomeCompleted, "pass", 20, 9, 9},
		{OutcomeCompleted, "fail", 5, 0, 8},
		{OutcomeConfigError, "", 0, 0, 0},
	} {
		w := mkWorld(t, "patch-"+strings.Repeat("x", i+1), spec.outcome)
		worlds = append(worlds, w)
		if spec.status == "" {
			continue
		}
		receipts = append(receipts,
			mkReceipt(t, w, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 3,
				map[string]int64{policy.MetricCollectedTotal: spec.collects}),
			mkReceipt(t, w, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, spec.status, spec.wall,
				map[string]int64{policy.MetricTestsPassed: spec.passed}))
	}

	base := Decide(pol, worlds, receipts)
	baseCanon, err := object.Canonical(base)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 25; trial++ {
		ws := append([]object.RecordedWorld(nil), worlds...)
		rs := append([]object.RecordedReceipt(nil), receipts...)
		rng.Shuffle(len(ws), func(i, j int) { ws[i], ws[j] = ws[j], ws[i] })
		rng.Shuffle(len(rs), func(i, j int) { rs[i], rs[j] = rs[j], rs[i] })
		canon, err := object.Canonical(Decide(pol, ws, rs))
		if err != nil {
			t.Fatal(err)
		}
		if string(canon) != string(baseCanon) {
			t.Fatalf("trial %d: shuffled inputs changed the decision:\n%s\nwant\n%s", trial, canon, baseCanon)
		}
	}
}
