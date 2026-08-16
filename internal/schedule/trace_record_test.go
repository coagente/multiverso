package schedule

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// twoRungPolicy is a v1 policy with a collect rung and a suite rung: the
// smallest shape with two decision-relevant rungs on one world, which is
// what a trace has to be able to describe.
const twoRungPolicy = `{"escalation":{"min_candidates_passing":0,"on_all_worlds_failed_machinery":true,"on_ranking_tie":false,"require_evidence":[]},` +
	`"hard_gates":[{"basis":"construction","gate":"collect-nonempty","oracle":"collect","threshold":0},` +
	`{"basis":"construction","gate":"status-pass","oracle":"suite","threshold":0}],"name":"two-rung",` +
	`"oracles":[{"args":[],"argv":[],"coverage":false,"kind":"pytest-collect","name":"collect","reruns":0,"timeout_ms":0},` +
	`{"args":[],"argv":[],"coverage":false,"kind":"pytest-suite","name":"suite","reruns":0,"timeout_ms":0}],` +
	`"ranking":["gate_pass","tests_passed_desc"],"schema":"multiverso.dev/policy/v1"}`

func mustPolicy(t *testing.T, src string) policy.Policy {
	t.Helper()
	pol, err := policy.Decode([]byte(src))
	if err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	return pol
}

// collector is the pure test's ledger: it records (type, payload) in append
// order and never touches a database.
type collector struct {
	types    []string
	payloads [][]byte
}

func (c *collector) fn() AppendFn {
	return func(typ string, payload []byte) error {
		c.types = append(c.types, typ)
		c.payloads = append(c.payloads, append([]byte(nil), payload...))
		return nil
	}
}

func (c *collector) events() []Event {
	out := make([]Event, 0, len(c.types))
	for i := range c.types {
		out = append(out, Event{Type: c.types[i], Payload: c.payloads[i]})
	}
	return out
}

func sampleStep(n int) Step {
	return Step{
		Batch:  1,
		Budget: BudgetState{RemainingMS: 9178, SpentMS: 822},
		Chosen: []Chosen{{Oracle: "suite", Reason: ReasonTopScore, World: "mv0:aa1"}},
		Considered: []Considered{{
			Affordable: true, Basis: BasisDecision, CostBasis: "fit(pytest-suite,off) n=37",
			CostMS: 427, DiscountBP: 5000, ExecutorBP: 5000, Flip: 1,
			FlipOutcomes: []string{"fail-closed:REJECT(unchanged)", "pass-max:REJECT->SELECT mv0:aa1"},
			HardGate:     true, Kind: policy.KindPytestSuite, Oracle: "suite",
			ScoreBPPS: 5854, ValueBP: 2500, World: "mv0:aa1",
		}},
		DecisionNow: DecisionNow{PassCount: 0, Subject: []string{}, Type: "REJECT"},
		Step:        n,
	}
}

// A recorded event is canonical JSON: keys sorted at every level, integers
// exact, no floats. The trace IS canonical JSON (DP-1), so this is not a
// stylistic assertion — a payload that sorted differently on another machine
// would break the ledger's hash chain reproducibility.
func TestPayloadIsCanonical(t *testing.T) {
	b, err := Payload(sampleStep(3))
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	got := string(b)
	if !strings.HasPrefix(got, `{"batch":1,"budget":{"released_ms":0,"remaining_ms":9178,"spent_ms":822},"chosen":[`) {
		t.Fatalf("payload is not canonically ordered:\n%s", got)
	}
	if strings.Contains(got, ".") && !strings.Contains(got, "pytest") && !strings.Contains(got, "mv0:") {
		t.Fatalf("payload appears to contain a float, which DP-1 forbids:\n%s", got)
	}
	// The §4.2 key set, verbatim: a renamed key is a broken consumer.
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"batch", "budget", "chosen", "considered", "decision_now", "staleness", "step"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("schedule.step payload is missing key %q: %s", key, got)
		}
	}
	row := decoded["considered"].([]any)[0].(map[string]any)
	for _, key := range []string{
		"affordable", "basis", "cost_basis", "cost_ms", "declined", "discount_bp",
		"executor_bp", "flip", "flip_outcomes", "hard_gate", "kind", "oracle",
		"score_bpps", "value_bp", "world",
	} {
		if _, ok := row[key]; !ok {
			t.Fatalf("considered row is missing key %q: %s", key, got)
		}
	}
}

// A nil slice canonicalizes to `null`, and `null` is a lie about the shape
// of the record: "the scheduler considered nothing" and "the field is
// missing" are different facts.
func TestPayloadNeverEmitsNull(t *testing.T) {
	for name, v := range map[string]any{
		"started":  normalizeStarted(Started{}),
		"step":     normalizeStep(Step{}),
		"finished": Finished{},
	} {
		b, err := Payload(v)
		if err != nil {
			t.Fatalf("%s: Payload: %v", name, err)
		}
		if strings.Contains(string(b), "null") {
			t.Fatalf("%s payload contains null: %s", name, b)
		}
	}
}

// The Recorder emits one event per call, under the documented type names,
// and one oracle.skipped per TERMINAL decline beside schedule.finished —
// because "not this batch" and "not ever" are different facts and M2a's
// per-rung event is what consumers were written against.
func TestRecorderEmitsFourEventTypes(t *testing.T) {
	var c collector
	r := NewRecorder(c.fn())
	if err := r.Started(Started{Intent: "mv0:i", Schedule: ScheduleAdaptive, Mode: ModeDecision, Parallel: 2}); err != nil {
		t.Fatalf("Started: %v", err)
	}
	if err := r.Step(sampleStep(1)); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if err := r.Finished(Finished{Stop: StopFrontier, Bought: 1, Considered: 2, Declined: 1}, []Skipped{
		{World: "mv0:bb2", Oracle: "mutate", Reason: "decision-inert"},
	}); err != nil {
		t.Fatalf("Finished: %v", err)
	}
	want := []string{EventStarted, EventStep, EventFinished, EventOracleSkipped}
	if len(c.types) != len(want) {
		t.Fatalf("recorded %v, want %v", c.types, want)
	}
	for i := range want {
		if c.types[i] != want[i] {
			t.Fatalf("event %d is %q, want %q", i, c.types[i], want[i])
		}
	}
}

// A nil appender is an error at the call site, never a panic and never a
// silent success: a race that cannot record its trace must say so.
func TestRecorderWithoutAppenderErrors(t *testing.T) {
	if err := NewRecorder(nil).Step(sampleStep(1)); err == nil {
		t.Fatal("a Recorder with no appender recorded a step without complaining")
	}
}

// Collect orders steps by their RECORDED index, not by ledger position:
// under parallel dispatch the appends are serialized by a mutex whose order
// is not the batch order.
func TestCollectOrdersStepsByIndex(t *testing.T) {
	var c collector
	r := NewRecorder(c.fn())
	_ = r.Started(Started{Intent: "mv0:i"})
	_ = r.Step(sampleStep(3))
	_ = r.Step(sampleStep(1))
	_ = r.Step(sampleStep(2))
	_ = r.Finished(Finished{Stop: StopEmpty}, nil)

	tr, err := Collect(c.events())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !tr.HasStarted || !tr.HasFinished {
		t.Fatalf("Collect lost the bracketing events: %+v", tr)
	}
	for i, s := range tr.Steps {
		if s.Step != i+1 {
			t.Fatalf("step %d has recorded index %d; Collect must sort by index", i, s.Step)
		}
	}
}

// A ledger with no schedule.* events is EMPTY, not a zero-valued allocation.
// The renderer depends on the distinction: absent source implies absent
// metric, never a fabricated one.
func TestCollectEmptyTraceIsReportedAsAbsent(t *testing.T) {
	tr, err := Collect([]Event{{Type: "receipt.recorded", Payload: []byte(`{}`)}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !tr.Empty() {
		t.Fatal("a ledger with no schedule events reported a trace")
	}
}

// A later scheduler may record more fields. This binary must render what it
// understands rather than refusing the whole trace (M1f decision 3's
// forward-compatibility rule).
func TestDecodeToleratesUnknownFields(t *testing.T) {
	s, err := DecodeStep([]byte(`{"step":7,"batch":2,"a_future_field":{"x":1},"considered":[]}`))
	if err != nil {
		t.Fatalf("DecodeStep refused an unknown field: %v", err)
	}
	if s.Step != 7 || s.Batch != 2 {
		t.Fatalf("decoded %+v, want step 7 batch 2", s)
	}
}

func receipt(world, kind, config, status string, wallMS int64, metrics map[string]int64) object.Receipt {
	rec := object.Receipt{
		Schema: object.SchemaReceipt, World: world,
		Oracle:    object.OracleRef{ID: kind, Version: "v0", Config: config},
		Result:    object.Result{Status: status, Metrics: metrics, Tools: map[string]string{}, Artifacts: []string{}},
		Freshness: object.Freshness{Basis: object.BasisConstruction, ValidFor: object.ValidFor{Tree: "git:t", Env: "mv0:e"}},
		Family:    policy.KindFamily(kind), Cost: object.Cost{WallMS: wallMS},
		Inputs: object.NoInputs(),
	}
	return rec
}

func recorded(t *testing.T, rec object.Receipt) object.RecordedReceipt {
	t.Helper()
	dig, _, err := object.Digest(rec)
	if err != nil {
		t.Fatalf("digest receipt: %v", err)
	}
	return object.RecordedReceipt{Digest: dig, Receipt: rec}
}

// Join maps a trace row to the receipt it bought THROUGH THE PINNED POLICY:
// the row names a policy-local instance and the receipt names a kind plus a
// resolved-config digest, and only the policy relates the two.
func TestJoinMapsRowsToReceiptsThroughThePolicy(t *testing.T) {
	pol := mustPolicy(t, twoRungPolicy)
	suite, ok := pol.OracleByName("suite")
	if !ok {
		t.Fatal("policy fixture declares no suite oracle")
	}
	rr := recorded(t, receipt("mv0:aa1", policy.KindPytestSuite, suite.Config, "pass", 427,
		map[string]int64{policy.MetricTestsPassed: 8}))

	var c collector
	r := NewRecorder(c.fn())
	_ = r.Step(sampleStep(1))
	tr, err := Collect(c.events())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	joined := tr.Join(pol, []object.RecordedReceipt{rr})
	if len(joined) != 1 || joined[0].Receipt != rr.Digest {
		t.Fatalf("Join produced %+v, want the suite receipt %s", joined, rr.Digest)
	}
	if got := tr.BasisOf(pol, []object.RecordedReceipt{rr})[rr.Digest]; got != BasisDecision {
		t.Fatalf("basis is %q, want %q", got, BasisDecision)
	}
	if got := tr.PredictedMS(); got != 427 {
		t.Fatalf("PredictedMS is %d, want the bought row's 427", got)
	}
}

// A row whose oracle the policy does not declare joins NOTHING. Joining it
// to whatever receipt happened to be on the same world would attribute a
// purchase to a rung nobody bought.
func TestJoinRefusesAnUndeclaredOracleName(t *testing.T) {
	pol := mustPolicy(t, twoRungPolicy)
	rr := recorded(t, receipt("mv0:aa1", policy.KindPytestSuite, "mv0:other", "pass", 1, nil))
	step := sampleStep(1)
	step.Considered[0].Oracle = "not-declared"
	tr := Trace{Steps: []Step{step}, HasStarted: true}
	joined := tr.Join(pol, []object.RecordedReceipt{rr})
	if len(joined) != 1 || joined[0].Receipt != "" {
		t.Fatalf("Join attached a receipt to an undeclared oracle: %+v", joined)
	}
}

// DeclinedRows is the half of the record that exists nowhere else: a receipt
// proves what was bought; only the trace proves what was weighed and
// refused.
func TestDeclinedRowsCarryTheirReason(t *testing.T) {
	step := sampleStep(4)
	step.Considered = append(step.Considered, Considered{
		World: "mv0:aa1", Oracle: "mutate", Kind: policy.KindMutationDiff, Flip: 0,
		DiscountBP: 10000, ExecutorBP: 5000, ValueBP: 0, CostMS: 18400,
		CostBasis: CostBasisDeclaredRank + "(rank 6, n=0)",
		Declined:  "decision-inert: no gate, ranking key or escalation rule reads mutation-diff",
	})
	tr := Trace{Steps: []Step{step}, HasStarted: true}
	dec := tr.DeclinedRows()
	if len(dec) != 1 || !strings.Contains(dec[0].Row.Declined, "decision-inert") {
		t.Fatalf("DeclinedRows produced %+v", dec)
	}
	if !tr.Unmeasured(policy.KindMutationDiff) {
		t.Fatal("a declared-rank row must report the kind as UNMEASURED so no ms figure is printed for it")
	}
	if tr.Unmeasured(policy.KindPytestSuite) {
		t.Fatal("a fitted row must not report the kind as unmeasured")
	}
}

// The declared-rank cost row carries NO millisecond figure, and its zeros
// are absence rather than a measurement of zero (decision 7c).
func TestDeclaredRankRowPrintsNoMilliseconds(t *testing.T) {
	row := DeclaredRankRow(policy.KindMutationDiff, 6, 0)
	if row.FixedMS != 0 || row.PerUnitMicroMS != 0 || row.Estimator != "" {
		t.Fatalf("declared-rank row carries a coefficient: %+v", row)
	}
	if got := row.CostBasisText(); !strings.HasPrefix(got, CostBasisDeclaredRank) || strings.Contains(got, "ms") {
		t.Fatalf("declared-rank basis text %q must name the rank and no millisecond figure", got)
	}
	fit := FitRow(Coefficient{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, N: 37, FixedMS: 402, PerUnitMicroMS: 3100, Estimator: EstimatorTheilSen})
	if got := fit.CostBasisText(); got != "fit(pytest-suite,off) n=37" {
		t.Fatalf("fit basis text is %q", got)
	}
}
