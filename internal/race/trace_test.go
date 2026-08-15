package race

import (
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// unbound rewrites a receipt's freshness so it names the world but judged
// another tree: inadmissible, not decisive (M1e decision 10).
func unbound(r *object.Receipt) {
	r.Freshness.ValidFor = object.ValidFor{Tree: "git:another-tree", Env: "mv0:another-env"}
}

func basis(b string) func(*object.Receipt) {
	return func(r *object.Receipt) { r.Freshness.Basis = b }
}

func exitCode(n int) func(*object.Receipt) {
	return func(r *object.Receipt) { r.Execution.ExitCode = n }
}

// TestGatePredicates walks every gate predicate against every way its
// evidence can be missing, stale, inconclusive or insufficient. Absence is
// never a pass, and every failure names its reason.
func TestGatePredicates(t *testing.T) {
	suiteCfg := cfgDigest(t, suiteSpec())
	collectCfg := cfgDigest(t, collectSpec())

	tests := []struct {
		name       string
		gate       object.GateSpec
		kind       string // "" = record no receipt at all
		cfg        string
		family     string
		status     string
		metrics    map[string]int64
		opts       []func(*object.Receipt)
		wantPass   bool
		wantDetail string
	}{
		// status-pass
		{name: "status-pass on a passing suite", gate: gate(policy.GateStatusPass, "suite", 0),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "pass", wantPass: true},
		{name: "status-pass on a failing suite", gate: gate(policy.GateStatusPass, "suite", 0),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "fail",
			wantDetail: "status=fail"},
		{name: "status-pass on an inconclusive run", gate: gate(policy.GateStatusPass, "suite", 0),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "error",
			wantDetail: "status=error"},
		{name: "status-pass with no receipt", gate: gate(policy.GateStatusPass, "suite", 0),
			wantDetail: policy.ReasonNoReceipt},
		{name: "status-pass with an unbound receipt", gate: gate(policy.GateStatusPass, "suite", 0),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "pass",
			opts: []func(*object.Receipt){unbound}, wantDetail: "unbound receipt"},
		{name: "status-pass with too weak a basis", gate: gate(policy.GateStatusPass, "suite", 0),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "pass",
			opts:       []func(*object.Receipt){basis(object.BasisProbabilistic)},
			wantDetail: "basis=probabilistic (want >= construction)"},
		{name: "status-pass with an unknown basis ranks 0 and satisfies nothing",
			gate: gate(policy.GateStatusPass, "suite", 0),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "pass",
			opts:       []func(*object.Receipt){basis("vibes")},
			wantDetail: "basis=vibes (want >= construction)"},
		{name: "status-pass on the wrong oracle instance is no evidence",
			gate: gate(policy.GateStatusPass, "suite", 0),
			kind: policy.KindPytestSuite, cfg: "mv0:some-other-config", family: policy.FamilySuite, status: "pass",
			wantDetail: policy.ReasonNoReceipt},

		// collect-nonempty
		{name: "collect-nonempty with tests collected", gate: gate(policy.GateCollectNonempty, "collect", 0),
			kind: policy.KindPytestCollect, cfg: collectCfg, family: policy.FamilyCollect, status: "pass",
			metrics: map[string]int64{policy.MetricCollectedTotal: 8}, wantPass: true},
		{name: "collect-nonempty on pytest exit 5", gate: gate(policy.GateCollectNonempty, "collect", 0),
			kind: policy.KindPytestCollect, cfg: collectCfg, family: policy.FamilyCollect, status: "fail",
			metrics: map[string]int64{policy.MetricCollectedTotal: 0}, opts: []func(*object.Receipt){exitCode(5)},
			wantDetail: "collected_total=0 (exit 5)"},
		{name: "collect-nonempty without the metric", gate: gate(policy.GateCollectNonempty, "collect", 0),
			kind: policy.KindPytestCollect, cfg: collectCfg, family: policy.FamilyCollect, status: "pass",
			wantDetail: "collected_total absent (source unavailable)"},

		// collected-not-below
		{name: "collected-not-below at zero drop", gate: gate(policy.GateCollectedNotBelow, "collect", 0),
			kind: policy.KindPytestCollect, cfg: collectCfg, family: policy.FamilyCollect, status: "pass",
			metrics: map[string]int64{policy.MetricCollectedDelta: 0}, wantPass: true},
		{name: "collected-not-below with tests added", gate: gate(policy.GateCollectedNotBelow, "collect", 0),
			kind: policy.KindPytestCollect, cfg: collectCfg, family: policy.FamilyCollect, status: "pass",
			metrics: map[string]int64{policy.MetricCollectedDelta: 2}, wantPass: true},
		{name: "collected-not-below catches deleted tests", gate: gate(policy.GateCollectedNotBelow, "collect", 0),
			kind: policy.KindPytestCollect, cfg: collectCfg, family: policy.FamilyCollect, status: "pass",
			metrics:    map[string]int64{policy.MetricCollectedDelta: -3},
			wantDetail: "collected_delta=-3 (tolerance -0)"},
		{name: "collected-not-below inside an explicit tolerance", gate: gate(policy.GateCollectedNotBelow, "collect", 2),
			kind: policy.KindPytestCollect, cfg: collectCfg, family: policy.FamilyCollect, status: "pass",
			metrics: map[string]int64{policy.MetricCollectedDelta: -2}, wantPass: true},
		{name: "collected-not-below outside an explicit tolerance", gate: gate(policy.GateCollectedNotBelow, "collect", 2),
			kind: policy.KindPytestCollect, cfg: collectCfg, family: policy.FamilyCollect, status: "pass",
			metrics:    map[string]int64{policy.MetricCollectedDelta: -3},
			wantDetail: "collected_delta=-3 (tolerance -2)"},
		{name: "collected-not-below without a baseline is not a pass",
			gate: gate(policy.GateCollectedNotBelow, "collect", 0),
			kind: policy.KindPytestCollect, cfg: collectCfg, family: policy.FamilyCollect, status: "pass",
			metrics:    map[string]int64{policy.MetricCollectedTotal: 8},
			wantDetail: "collected_delta absent (source unavailable)"},

		// no-failed-tests
		{name: "no-failed-tests on a clean suite", gate: gate(policy.GateNoFailedTests, "suite", 0),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "pass",
			metrics:  map[string]int64{policy.MetricTestsFailed: 0, policy.MetricTestsErrored: 0},
			wantPass: true},
		{name: "no-failed-tests with failures and errors", gate: gate(policy.GateNoFailedTests, "suite", 0),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "fail",
			metrics:    map[string]int64{policy.MetricTestsFailed: 2, policy.MetricTestsErrored: 1},
			wantDetail: "tests_failed=2 tests_errored=1"},
		{name: "no-failed-tests without the error count", gate: gate(policy.GateNoFailedTests, "suite", 0),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "pass",
			metrics:    map[string]int64{policy.MetricTestsFailed: 0},
			wantDetail: "tests_errored absent (source unavailable)"},

		// coverage-at-least
		{name: "coverage-at-least above the bar", gate: gate(policy.GateCoverageAtLeast, "suite", 8735),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "pass",
			metrics: map[string]int64{policy.MetricCoverageBP: 8800}, wantPass: true},
		{name: "coverage-at-least below the bar", gate: gate(policy.GateCoverageAtLeast, "suite", 8735),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "pass",
			metrics:    map[string]int64{policy.MetricCoverageBP: 8000},
			wantDetail: "coverage_bp=8000 (want >= 8735)"},
		{name: "coverage-at-least with coverage.py absent", gate: gate(policy.GateCoverageAtLeast, "suite", 8735),
			kind: policy.KindPytestSuite, cfg: suiteCfg, family: policy.FamilySuite, status: "pass",
			wantDetail: "coverage_bp absent (source unavailable)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pol := compileV1(t, []object.GateSpec{tt.gate}, nil, object.EscalationSpec{})
			w := mkWorld(t, "patch-a", OutcomeCompleted)
			var receipts []object.RecordedReceipt
			if tt.kind != "" {
				receipts = append(receipts,
					mkReceipt(t, w, tt.kind, tt.cfg, tt.family, tt.status, 10, tt.metrics, tt.opts...))
			}
			tr := Trace(pol, []object.RecordedWorld{w}, receipts)
			got := tr.Candidates[0].Gates[0]
			if tt.wantPass {
				if got.Result != policy.GatePass || got.Detail != "" {
					t.Fatalf("gate = %+v, want pass", got)
				}
				if !tr.Candidates[0].Pass || tr.Type != TypeSelect {
					t.Fatalf("candidate pass = %v, type = %s, want a SELECT", tr.Candidates[0].Pass, tr.Type)
				}
				return
			}
			if got.Result != policy.GateFail {
				t.Fatalf("gate = %+v, want fail", got)
			}
			if !strings.Contains(got.Detail, tt.wantDetail) {
				t.Errorf("detail = %q, want it to contain %q", got.Detail, tt.wantDetail)
			}
			if tr.Type != TypeReject {
				t.Errorf("type = %s, want REJECT", tr.Type)
			}
		})
	}
}

// A world that never completed reports the most fundamental cause, not the
// symptom that it has no receipts.
func TestGateReasonPrefersOutcome(t *testing.T) {
	pol := compileV1(t, []object.GateSpec{gate(policy.GateStatusPass, "suite", 0)}, nil, object.EscalationSpec{})
	w := mkWorld(t, "patch-a", object.OutcomeBudgetExceeded)
	tr := Trace(pol, []object.RecordedWorld{w}, nil)
	if got := tr.Candidates[0].Gates[0].Detail; got != "outcome=BUDGET_EXCEEDED" {
		t.Errorf("detail = %q, want outcome=BUDGET_EXCEEDED", got)
	}
}

// Two receipts from the same oracle instance: the smallest digest is the
// counted one, so the choice is order-independent — the M0 disambiguation,
// unchanged.
func TestCountedReceiptIsSmallestDigest(t *testing.T) {
	suiteCfg := cfgDigest(t, suiteSpec())
	pol := compileV1(t, []object.GateSpec{gate(policy.GateStatusPass, "suite", 0)}, nil, object.EscalationSpec{})
	w := mkWorld(t, "patch-a", OutcomeCompleted)
	r1 := mkReceipt(t, w, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10, nil)
	r2 := mkReceipt(t, w, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "fail", 20, nil)
	want := r1.Digest
	if r2.Digest < r1.Digest {
		want = r2.Digest
	}
	for _, order := range [][]object.RecordedReceipt{{r1, r2}, {r2, r1}} {
		tr := Trace(pol, []object.RecordedWorld{w}, order)
		if got := tr.Candidates[0].Gates[0].Receipt; got != want {
			t.Errorf("counted receipt = %s, want the smallest digest %s", got, want)
		}
	}
}

// TestRankingKeys covers every key in the vocabulary, in both directions,
// including the totality rule: a candidate with a known value outranks one
// without, whichever side of the comparison it is on.
func TestRankingKeys(t *testing.T) {
	suiteCfg := cfgDigest(t, suiteSpec())
	collectCfg := cfgDigest(t, collectSpec())
	statusGate := []object.GateSpec{gate(policy.GateStatusPass, "suite", 0)}

	// better/worse are two worlds that pass every gate; each case gives
	// them the evidence the key under test reads.
	type build func(t *testing.T, better, worse object.RecordedWorld) []object.RecordedReceipt

	suiteWith := func(metrics ...map[string]int64) build {
		return func(t *testing.T, better, worse object.RecordedWorld) []object.RecordedReceipt {
			return []object.RecordedReceipt{
				mkReceipt(t, better, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10, metrics[0]),
				mkReceipt(t, worse, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10, metrics[1]),
			}
		}
	}

	tests := []struct {
		name    string
		key     string
		build   build
		betterW func(*object.World)
		worseW  func(*object.World)
	}{
		{
			name:  "tests_passed_desc prefers more passing tests",
			key:   policy.KeyTestsPassedDesc,
			build: suiteWith(map[string]int64{policy.MetricTestsPassed: 10}, map[string]int64{policy.MetricTestsPassed: 8}),
		},
		{
			name:  "tests_passed_desc: a measured value beats no measurement",
			key:   policy.KeyTestsPassedDesc,
			build: suiteWith(map[string]int64{policy.MetricTestsPassed: 0}, nil),
		},
		{
			name:  "coverage_desc prefers more covered lines",
			key:   policy.KeyCoverageDesc,
			build: suiteWith(map[string]int64{policy.MetricCoverageBP: 9000}, map[string]int64{policy.MetricCoverageBP: 100}),
		},
		{
			name: "wall_ms_asc sums EVERY counted receipt of the world",
			key:  policy.KeyWallMSAsc,
			build: func(t *testing.T, better, worse object.RecordedWorld) []object.RecordedReceipt {
				return []object.RecordedReceipt{
					// 3 + 10 = 13 against 1 + 20 = 21: the slower suite loses
					// even though its collect step was faster.
					mkReceipt(t, better, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 3, nil),
					mkReceipt(t, better, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10, nil),
					mkReceipt(t, worse, policy.KindPytestCollect, collectCfg, policy.FamilyCollect, "pass", 1, nil),
					mkReceipt(t, worse, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 20, nil),
				}
			},
		},
		{
			name:    "cost_asc prefers the cheaper world when both reported a cost",
			key:     policy.KeyCostAsc,
			build:   suiteWith(nil, nil),
			betterW: func(w *object.World) { w.Cost = object.RunCost{USDMicro: 100, Source: "client-estimate"} },
			worseW:  func(w *object.World) { w.Cost = object.RunCost{USDMicro: 900, Source: "client-estimate"} },
		},
		{
			name:    "cost_asc: a reported cost beats source=none, which is unknown, not free",
			key:     policy.KeyCostAsc,
			build:   suiteWith(nil, nil),
			betterW: func(w *object.World) { w.Cost = object.RunCost{USDMicro: 5000, Source: "client-estimate"} },
			worseW:  func(w *object.World) { w.Cost = object.RunCost{USDMicro: 0, Source: "none"} },
		},
		{
			name:    "patch_size_asc prefers the smaller patch, and 0 is a real size",
			key:     policy.KeyPatchSizeAsc,
			build:   suiteWith(nil, nil),
			betterW: func(w *object.World) { w.PatchBytes = 0 },
			worseW:  func(w *object.World) { w.PatchBytes = 412 },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pol := compileV1(t, statusGate, []string{tt.key}, object.EscalationSpec{})
			opts := func(f func(*object.World)) []func(*object.World) {
				if f == nil {
					return nil
				}
				return []func(*object.World){f}
			}
			better := mkWorld(t, "patch-better", OutcomeCompleted, opts(tt.betterW)...)
			worse := mkWorld(t, "patch-worse", OutcomeCompleted, opts(tt.worseW)...)
			receipts := tt.build(t, better, worse)
			// Unknown must lose in BOTH directions: the same winner whether
			// the weaker candidate is compared from the left or the right.
			for _, worlds := range [][]object.RecordedWorld{{better, worse}, {worse, better}} {
				d := Decide(pol, worlds, receipts)
				if d.Type != TypeSelect {
					t.Fatalf("type = %s, want SELECT (rationale %q)", d.Type, d.Rationale)
				}
				if d.Subject[0] != better.Digest {
					t.Fatalf("winner = %s, want %s", d.Subject[0], better.Digest)
				}
			}
		})
	}
}

// gate_pass is an implicit FIRST key: a policy that never mentions it still
// cannot select a gate-failing world over a passing one, however much
// better the failing world looks on every other key.
func TestGatePassIsImplicitFirstKey(t *testing.T) {
	suiteCfg := cfgDigest(t, suiteSpec())
	pol := compileV1(t,
		[]object.GateSpec{gate(policy.GateStatusPass, "suite", 0)},
		[]string{policy.KeyTestsPassedDesc},
		object.EscalationSpec{})
	if got := pol.KeyNames(); !reflect.DeepEqual(got,
		[]string{policy.KeyGatePass, policy.KeyTestsPassedDesc, policy.KeyWorldDigestAsc}) {
		t.Fatalf("effective keys = %v, want gate_pass first and world_digest_asc last", got)
	}
	winner := mkWorld(t, "patch-modest", OutcomeCompleted)
	loser := mkWorld(t, "patch-liar", OutcomeCompleted)
	rWin := mkReceipt(t, winner, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10,
		map[string]int64{policy.MetricTestsPassed: 1})
	rLose := mkReceipt(t, loser, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "fail", 10,
		map[string]int64{policy.MetricTestsPassed: 999})

	d := Decide(pol, []object.RecordedWorld{winner, loser}, []object.RecordedReceipt{rWin, rLose})
	if d.Subject[0] != winner.Digest {
		t.Errorf("winner = %s, want the gate-passing world %s", d.Subject[0], winner.Digest)
	}
}

// An explicit gate_pass / world_digest_asc changes nothing (M0's
// ["gate_pass","wall_ms_asc"] is exactly the effective list), and nothing
// may follow the terminal key.
func TestEffectiveKeysAreIdempotent(t *testing.T) {
	pol := compileV1(t,
		[]object.GateSpec{gate(policy.GateStatusPass, "suite", 0)},
		[]string{policy.KeyGatePass, policy.KeyWallMSAsc, policy.KeyWorldDigestAsc},
		object.EscalationSpec{})
	want := []string{policy.KeyGatePass, policy.KeyWallMSAsc, policy.KeyWorldDigestAsc}
	if got := pol.KeyNames(); !reflect.DeepEqual(got, want) {
		t.Errorf("effective keys = %v, want %v", got, want)
	}
}

// The trace is what mvo explain renders: the key-by-key walk that shows
// WHERE the decision was made, and it must agree with Decide everywhere.
func TestTraceComparisonWalk(t *testing.T) {
	suiteCfg := cfgDigest(t, suiteSpec())
	pol := compileV1(t,
		[]object.GateSpec{gate(policy.GateStatusPass, "suite", 0)},
		[]string{policy.KeyTestsPassedDesc, policy.KeyWallMSAsc},
		object.EscalationSpec{})

	win := mkWorld(t, "patch-c", OutcomeCompleted)
	second := mkWorld(t, "patch-a", OutcomeCompleted)
	failing := mkWorld(t, "patch-z", OutcomeCompleted)
	rWin := mkReceipt(t, win, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 500,
		map[string]int64{policy.MetricTestsPassed: 10})
	rSecond := mkReceipt(t, second, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 5,
		map[string]int64{policy.MetricTestsPassed: 8})
	rFailing := mkReceipt(t, failing, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "fail", 1,
		map[string]int64{policy.MetricTestsPassed: 0})

	worlds := []object.RecordedWorld{second, failing, win}
	receipts := []object.RecordedReceipt{rWin, rSecond, rFailing}
	tr := Trace(pol, worlds, receipts)

	if tr.Winner != win.Digest {
		t.Fatalf("winner = %s, want %s", tr.Winner, win.Digest)
	}
	if len(tr.Comparisons) != 2 {
		t.Fatalf("comparisons = %d, want one per other candidate", len(tr.Comparisons))
	}
	// Key 1 (gate_pass) ties, key 2 (tests_passed_desc) decides: the proof
	// that ranking is lexicographic and not a score.
	c := tr.Comparisons[0]
	if c.Other != second.Digest || c.DecidedAt != 2 || c.Key != policy.KeyTestsPassedDesc {
		t.Errorf("comparison 1 = %+v, want the second key deciding against %s", c, second.Digest)
	}
	if c.Text != "10 > 8" || c.WinnerValue != "10" || c.OtherValue != "8" {
		t.Errorf("comparison text = %q (%q vs %q), want \"10 > 8\"", c.Text, c.WinnerValue, c.OtherValue)
	}
	if len(c.Steps) != 1 || c.Steps[0].Key != policy.KeyGatePass || c.Steps[0].Result != StepTie {
		t.Errorf("steps = %+v, want gate_pass tied at index 1", c.Steps)
	}
	// The gate-failing candidate is decided at key 1, "pass > fail".
	if g := tr.Comparisons[1]; g.DecidedAt != 1 || g.Key != policy.KeyGatePass || g.Text != "pass > fail" {
		t.Errorf("comparison 2 = %+v, want gate_pass (pass > fail)", g)
	}
	// Ordinals record the caller's input order, ranks the decided order.
	byWorld := map[string]CandidateTrace{}
	for _, c := range tr.Candidates {
		byWorld[c.World] = c
	}
	if got := byWorld[second.Digest].Ordinal; got != 1 {
		t.Errorf("ordinal of the input-first world = %d, want 1", got)
	}
	if got := byWorld[win.Digest].Rank; got != 1 {
		t.Errorf("winner rank = %d, want 1", got)
	}
	// Metrics are display data merged from the world's counted receipts.
	if got := byWorld[win.Digest].Metrics[policy.MetricTestsPassed]; got != 10 {
		t.Errorf("winner metrics[tests_passed] = %d, want 10", got)
	}

	// Trace and Decide never disagree: one evaluation, two renderings.
	d := Decide(pol, worlds, receipts)
	if d.Type != tr.Type || d.Rationale != tr.Rationale {
		t.Errorf("Decide/Trace disagree: %s %q vs %s %q", d.Type, d.Rationale, tr.Type, tr.Rationale)
	}
	for i, c := range tr.Candidates {
		if d.Subject[i] != c.World {
			t.Errorf("subject[%d] = %s, trace rank %d = %s", i, d.Subject[i], i+1, c.World)
		}
	}
}

// An ascending key renders its comparison with "<", and a key the loser
// cannot answer renders "-".
func TestComparisonTextDirections(t *testing.T) {
	suiteCfg := cfgDigest(t, suiteSpec())
	pol := compileV1(t,
		[]object.GateSpec{gate(policy.GateStatusPass, "suite", 0)},
		[]string{policy.KeyWallMSAsc, policy.KeyCoverageDesc},
		object.EscalationSpec{})
	fast := mkWorld(t, "patch-fast", OutcomeCompleted)
	slow := mkWorld(t, "patch-slow", OutcomeCompleted)
	rFast := mkReceipt(t, fast, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 412, nil)
	rSlow := mkReceipt(t, slow, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 588, nil)
	tr := Trace(pol, []object.RecordedWorld{fast, slow}, []object.RecordedReceipt{rFast, rSlow})
	if got := tr.Comparisons[0].Text; got != "412 < 588" {
		t.Errorf("ascending comparison = %q, want \"412 < 588\"", got)
	}

	measured := mkWorld(t, "patch-measured", OutcomeCompleted)
	silent := mkWorld(t, "patch-silent", OutcomeCompleted)
	rMeasured := mkReceipt(t, measured, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10,
		map[string]int64{policy.MetricCoverageBP: 9})
	rSilent := mkReceipt(t, silent, policy.KindPytestSuite, suiteCfg, policy.FamilySuite, "pass", 10, nil)
	pol2 := compileV1(t,
		[]object.GateSpec{gate(policy.GateStatusPass, "suite", 0)},
		[]string{policy.KeyCoverageDesc},
		object.EscalationSpec{})
	tr2 := Trace(pol2, []object.RecordedWorld{measured, silent}, []object.RecordedReceipt{rMeasured, rSilent})
	if got := tr2.Comparisons[0].Text; got != "9 > -" {
		t.Errorf("unknown comparison = %q, want \"9 > -\"", got)
	}
}

// Merged metrics are DISPLAY data, and a policy may legally declare two
// instances of one kind (distinct resolved configs, so rule 4 does not
// refuse them). Both emit the same metric names, so the union cannot answer
// "who measured this" — and a number a consumer cannot attribute to an
// oracle is worse than no number. Values the two receipts agree on stay;
// values they disagree about are dropped, and the single-instance case that
// every shipped policy is stays untouched.
func TestMergedMetricsDropsWhatTwoOraclesDisagreeAbout(t *testing.T) {
	specA := object.OracleSpec{Name: "collect-a", Kind: policy.KindPytestCollect, Argv: []string{}, Args: []string{}}
	specB := object.OracleSpec{Name: "collect-b", Kind: policy.KindPytestCollect, Argv: []string{}, Args: []string{"-x"}}
	cfgA, cfgB := cfgDigest(t, specA), cfgDigest(t, specB)
	if cfgA == cfgB {
		t.Fatal("the fixture instances share a resolved config: validation would refuse them")
	}
	p := object.PolicyV1{
		Schema:  object.SchemaPolicyV1,
		Name:    "two-instances",
		Oracles: []object.OracleSpec{specA, specB},
		HardGates: []object.GateSpec{
			gate(policy.GateCollectNonempty, "collect-a", 0),
			gate(policy.GateCollectNonempty, "collect-b", 0),
		},
		Ranking:    []string{},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}},
	}
	_, canon, err := object.Digest(p)
	if err != nil {
		t.Fatalf("digest policy: %v", err)
	}
	pol, err := policy.Decode(canon)
	if err != nil {
		t.Fatalf("decode policy: %v", err)
	}

	w := mkWorld(t, "patch-a", OutcomeCompleted)
	receipts := []object.RecordedReceipt{
		mkReceipt(t, w, policy.KindPytestCollect, cfgA, policy.FamilyCollect, "pass", 10,
			map[string]int64{policy.MetricCollectedTotal: 8, policy.MetricCollectedBase: 8}),
		mkReceipt(t, w, policy.KindPytestCollect, cfgB, policy.FamilyCollect, "pass", 10,
			map[string]int64{policy.MetricCollectedTotal: 3, policy.MetricCollectedBase: 8}),
	}
	tr := Trace(pol, []object.RecordedWorld{w}, receipts)
	c := tr.Candidates[0]
	if _, ok := c.Metrics[policy.MetricCollectedTotal]; ok {
		t.Errorf("collected_total = %d: an unattributable number was handed to a consumer",
			c.Metrics[policy.MetricCollectedTotal])
	}
	if got := c.Metrics[policy.MetricCollectedBase]; got != 8 {
		t.Errorf("collected_base = %d, want 8: receipts that AGREE are not ambiguous", got)
	}
	// Each gate still reads its own oracle's receipt: the verdicts are
	// per-instance whatever the merged map does.
	if len(c.Gates) != 2 || c.Gates[0].Result != policy.GatePass || c.Gates[1].Result != policy.GatePass {
		t.Fatalf("gates = %+v", c.Gates)
	}
	if c.Gates[0].Receipt == c.Gates[1].Receipt {
		t.Errorf("both gates counted the same receipt: %s", c.Gates[0].Receipt)
	}
}
