package schedule_test

// The waste metric's tests live in an EXTERNAL test package so they can call
// the real race.Decide: internal/race imports internal/schedule, so an
// in-package test importing it back would be an import cycle. Testing the
// counterfactual against a hand-written fake decision rule would also test
// the wrong thing — decision 18's whole claim is about what the SHIPPED
// decision function does with a substituted receipt.

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/schedule"
)

const wastePolicy = `{"escalation":{"min_candidates_passing":0,"on_all_worlds_failed_machinery":false,"on_ranking_tie":false,"require_evidence":[]},` +
	`"hard_gates":[{"basis":"construction","gate":"collect-nonempty","oracle":"collect","threshold":0},` +
	`{"basis":"construction","gate":"status-pass","oracle":"suite","threshold":0}],"name":"waste",` +
	`"oracles":[{"args":[],"argv":[],"coverage":false,"kind":"pytest-collect","name":"collect","reruns":0,"timeout_ms":0},` +
	`{"args":[],"argv":[],"coverage":false,"kind":"pytest-suite","name":"suite","reruns":0,"timeout_ms":0}],` +
	`"ranking":["gate_pass","tests_passed_desc"],"schema":"multiverso.dev/policy/v1"}`

// wallMSPolicy is the same shape with the ALLOCATION-SENSITIVE key added
// (decision 15): wall_ms_asc's value is a function of which receipts a world
// holds, which is the quantity a substitution deliberately holds fixed.
const wallMSPolicy = `{"escalation":{"min_candidates_passing":0,"on_all_worlds_failed_machinery":false,"on_ranking_tie":false,"require_evidence":[]},` +
	`"hard_gates":[{"basis":"construction","gate":"collect-nonempty","oracle":"collect","threshold":0},` +
	`{"basis":"construction","gate":"status-pass","oracle":"suite","threshold":0}],"name":"waste-wall",` +
	`"oracles":[{"args":[],"argv":[],"coverage":false,"kind":"pytest-collect","name":"collect","reruns":0,"timeout_ms":0},` +
	`{"args":[],"argv":[],"coverage":false,"kind":"pytest-suite","name":"suite","reruns":0,"timeout_ms":0}],` +
	`"ranking":["gate_pass","wall_ms_asc"],"schema":"multiverso.dev/policy/v1"}`

const (
	fixtureTree = "git:t0"
	fixtureEnv  = "mv0:env"
	collectBase = 8
)

func decodePolicy(t *testing.T, src string) policy.Policy {
	t.Helper()
	pol, err := policy.Decode([]byte(src))
	if err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	return pol
}

func makeWorld(t *testing.T, patch string) object.RecordedWorld {
	t.Helper()
	w := object.World{
		Schema: object.SchemaWorld, Intent: "mv0:intent", Tree: fixtureTree, Env: fixtureEnv,
		IsolationTier: object.TierT0Worktree,
		Producer:      object.Producer{Adapter: "script@v0", IdentityTier: "claimed", Role: "generator"},
		Context:       "sha256:ctx", Patch: patch, PatchBytes: 10, Trace: "sha256:tr",
		Cost: object.RunCost{Source: "none"}, Outcome: object.OutcomeCompleted, CreatedAt: "2026-01-01T00:00:00Z",
	}
	dig, _, err := object.Digest(w)
	if err != nil {
		t.Fatalf("digest world: %v", err)
	}
	return object.RecordedWorld{Digest: dig, World: w}
}

func makeReceipt(t *testing.T, pol policy.Policy, world, oracleName, status string, wallMS int64, metrics map[string]int64) object.RecordedReceipt {
	t.Helper()
	spec, ok := pol.OracleByName(oracleName)
	if !ok {
		t.Fatalf("policy declares no oracle %q", oracleName)
	}
	rec := object.Receipt{
		Schema: object.SchemaReceipt, World: world,
		Oracle: object.OracleRef{ID: spec.Kind, Version: "v0", Config: spec.Config},
		Result: object.Result{Status: status, Metrics: metrics, Tools: map[string]string{}, Artifacts: []string{}},
		Freshness: object.Freshness{
			Basis: object.BasisConstruction, ValidFor: object.ValidFor{Tree: fixtureTree, Env: fixtureEnv},
		},
		RecheckTier: "V1-replayable", Family: spec.Family,
		Cost:   object.Cost{WallMS: wallMS, Units: collectBase, Unit: policy.UnitTests},
		Inputs: object.NoInputs(), Correlation: policy.KindCorrelation(spec.Kind),
		CreatedAt: "2026-01-01T00:00:01Z",
	}
	dig, _, err := object.Digest(rec)
	if err != nil {
		t.Fatalf("digest receipt: %v", err)
	}
	return object.RecordedReceipt{Digest: dig, Receipt: rec}
}

// fixture builds a two-world race in which the WINNER passes both rungs and
// the LOSER passes collect and fails suite — the shape M2b §4.3 works
// through, and the smallest one where the answer is not trivially "all of it
// mattered".
type fixture struct {
	pol      policy.Policy
	worlds   []object.RecordedWorld
	receipts []object.RecordedReceipt
	winner   object.RecordedWorld
	loser    object.RecordedWorld
	trace    schedule.Trace
}

func newFixture(t *testing.T, src string) fixture {
	t.Helper()
	pol := decodePolicy(t, src)
	a := makeWorld(t, "sha256:patch-a")
	b := makeWorld(t, "sha256:patch-b")
	f := fixture{pol: pol, worlds: []object.RecordedWorld{a, b}, winner: a, loser: b}
	f.receipts = []object.RecordedReceipt{
		makeReceipt(t, pol, a.Digest, "collect", "pass", 400, map[string]int64{
			policy.MetricCollectedTotal: collectBase, policy.MetricCollectedDelta: 0,
		}),
		makeReceipt(t, pol, a.Digest, "suite", "pass", 427, map[string]int64{
			policy.MetricTestsPassed: 8, policy.MetricTestsFailed: 0, policy.MetricTestsErrored: 0,
		}),
		makeReceipt(t, pol, b.Digest, "collect", "pass", 400, map[string]int64{
			policy.MetricCollectedTotal: collectBase, policy.MetricCollectedDelta: 0,
		}),
		makeReceipt(t, pol, b.Digest, "suite", "fail", 427, map[string]int64{
			policy.MetricTestsPassed: 6, policy.MetricTestsFailed: 2, policy.MetricTestsErrored: 0,
		}),
	}
	f.trace = traceFor(f)
	return f
}

// traceFor builds the recorded trace this fixture's receipts would have come
// from: one step per rung, every row bought on the decision basis.
func traceFor(f fixture) schedule.Trace {
	step := func(n int, oracle, kind string, cost int64) schedule.Step {
		rows := make([]schedule.Considered, 0, 2)
		chosen := make([]schedule.Chosen, 0, 2)
		for _, w := range f.worlds {
			rows = append(rows, schedule.Considered{
				Affordable: true, Basis: schedule.BasisDecision,
				CostBasis: "fit(" + kind + ",off) n=37", CostMS: cost,
				DiscountBP: 10000, ExecutorBP: 5000, Flip: 1,
				FlipOutcomes: []string{}, HardGate: true, Kind: kind, Oracle: oracle,
				ScoreBPPS: 1, ValueBP: 5000, World: w.Digest,
			})
			chosen = append(chosen, schedule.Chosen{Oracle: oracle, Reason: schedule.ReasonTopScore, World: w.Digest})
		}
		return schedule.Step{
			Batch: 2, Chosen: chosen, Considered: rows,
			DecisionNow: schedule.DecisionNow{Subject: []string{}, Type: "REJECT"}, Step: n,
		}
	}
	return schedule.Trace{
		HasStarted: true,
		Started: schedule.Started{
			Budget: schedule.StartBudget{MaxOracleMS: 10000}, Intent: "mv0:intent",
			Mode: schedule.ModeDecision, Parallel: 2, Schedule: schedule.ScheduleAdaptive,
		},
		Steps: []schedule.Step{
			step(1, "collect", policy.KindPytestCollect, 400),
			step(2, "suite", policy.KindPytestSuite, 427),
		},
		HasFinished: true,
		Finished:    schedule.Finished{Bought: 4, Considered: 4, Steps: 2, Stop: schedule.StopFrontier},
	}
}

func (f fixture) waste(t *testing.T, b schedule.Bounds) schedule.WasteReport {
	t.Helper()
	rep, err := schedule.Waste(schedule.WasteInput{
		Policy: f.pol, Worlds: f.worlds, Receipts: f.receipts, Trace: f.trace,
		Bounds: b, Decide: race.Decide,
	})
	if err != nil {
		t.Fatalf("Waste: %v", err)
	}
	return rep
}

func rowFor(rep schedule.WasteReport, dig string) (schedule.WasteRow, bool) {
	for _, r := range rep.Rows {
		if r.Receipt == dig {
			return r, true
		}
	}
	return schedule.WasteRow{}, false
}

// The winner's receipts are load-bearing and the loser's cheap rung is not:
// the ladder spent 400 ms collecting on a candidate the NEXT rung was going
// to eliminate, which is a statement about ladder order and discriminator
// cost — that is, about scheduling, which is what the metric is for.
func TestWasteFindsTheLosersPreEliminationRung(t *testing.T) {
	f := newFixture(t, wastePolicy)
	base := race.Decide(f.pol, f.worlds, f.receipts)
	if base.Type != race.TypeSelect {
		t.Fatalf("fixture does not SELECT: %s (%s)", base.Type, base.Rationale)
	}
	rep := f.waste(t, schedule.Bounds{CollectedBase: collectBase})
	if !rep.Available {
		t.Fatal("waste reported unavailable on a complete fixture")
	}
	if rep.SpentMS != 400+427+400+427 {
		t.Fatalf("spend is %d ms, want the sum of every decision-basis receipt", rep.SpentMS)
	}
	for _, rr := range f.receipts {
		row, ok := rowFor(rep, rr.Digest)
		if !ok {
			t.Fatalf("receipt %s has no waste row", rr.Digest)
		}
		if rr.Receipt.World == f.winner.Digest && !row.Influenced {
			t.Fatalf("the winner's %s receipt reads as waste: %s", rr.Receipt.Oracle.ID, row.Reason)
		}
		if rr.Receipt.World == f.loser.Digest && rr.Receipt.Oracle.ID == policy.KindPytestCollect && row.Influenced {
			t.Fatalf("the loser's pre-elimination collect receipt reads as influential: %s", row.Reason)
		}
	}
	if rep.WasteMS == 0 {
		t.Fatal("waste is 0 ms on a race that verified a candidate the next rung eliminated")
	}
	// The percentage is a derived rendering of the same two integers, and it
	// must agree with them.
	if want := rep.WasteMS * 10000 / rep.SpentMS; rep.WasteBP != want {
		t.Fatalf("waste_bp %d does not agree with %d/%d", rep.WasteBP, rep.WasteMS, rep.SpentMS)
	}
	for _, row := range rep.Wasted {
		if row.Reason == "" {
			t.Fatalf("a wasted row carries no reason: %+v", row)
		}
	}
}

// The naive leave-one-out definition would call EVERY receipt on a
// non-winner waste, including the one that told us the candidate was worse.
// Bracket substitution must not: the loser's SUITE receipt is what
// eliminated it, and under pass-max it would have passed.
func TestWasteDoesNotCallTheDiscriminatingReceiptWaste(t *testing.T) {
	f := newFixture(t, wastePolicy)
	rep := f.waste(t, schedule.Bounds{CollectedBase: collectBase})
	for _, rr := range f.receipts {
		if rr.Receipt.World != f.loser.Digest || rr.Receipt.Oracle.ID != policy.KindPytestSuite {
			continue
		}
		row, _ := rowFor(rep, rr.Digest)
		if !row.Influenced {
			t.Fatalf("the receipt that ELIMINATED the loser reads as waste (%s); "+
				"pass-max must reach the world where it passed", row.Reason)
		}
	}
}

// Greedy substitution is a heuristic, so it has to be replayable before it
// can be printed beside a measured number: the order is canonical and the
// commit rule deterministic, so the same input gives the same answer.
func TestGreedyWasteIsDeterministic(t *testing.T) {
	f := newFixture(t, wastePolicy)
	first := f.waste(t, schedule.Bounds{CollectedBase: collectBase})
	for i := 0; i < 3; i++ {
		again := f.waste(t, schedule.Bounds{CollectedBase: collectBase})
		if again.GreedyMS != first.GreedyMS || len(again.GreedyWasted) != len(first.GreedyWasted) {
			t.Fatalf("greedy waste is not deterministic: %d ms then %d ms", first.GreedyMS, again.GreedyMS)
		}
		for j := range again.GreedyWasted {
			if again.GreedyWasted[j].Receipt != first.GreedyWasted[j].Receipt {
				t.Fatalf("greedy order moved between runs at %d", j)
			}
		}
	}
}

// A research purchase is one whose stated purpose is to influence no
// decision. Counting it would make the metric meaningless, so it is excluded
// BY CONSTRUCTION and its spend is reported separately rather than hidden.
func TestResearchRowsAreExcludedFromWaste(t *testing.T) {
	f := newFixture(t, wastePolicy)
	for i := range f.trace.Steps[1].Considered {
		f.trace.Steps[1].Considered[i].Basis = schedule.BasisResearch
	}
	rep := f.waste(t, schedule.Bounds{CollectedBase: collectBase})
	if rep.ResearchMS != 427+427 {
		t.Fatalf("research spend is %d ms, want the two suite receipts", rep.ResearchMS)
	}
	if rep.SpentMS != 400+400 {
		t.Fatalf("the waste denominator is %d ms; research rows must not be in it", rep.SpentMS)
	}
	for _, row := range rep.Rows {
		if row.Basis == schedule.BasisResearch && row.Influenced {
			t.Fatal("a research row was scored for influence rather than excluded")
		}
	}
}

// Decision 15: wall_ms_asc's value is a function of WHICH receipts a world
// holds — the quantity a substitution holds fixed — so the bracket cannot
// speak about it and the metric must FAIL OPEN and say so, rather than
// printing a number the key would have moved.
func TestAllocationSensitiveKeyFailsOpen(t *testing.T) {
	f := newFixture(t, wallMSPolicy)
	rep := f.waste(t, schedule.Bounds{CollectedBase: collectBase})
	if len(rep.Unbounded) == 0 {
		t.Fatal("a policy declaring wall_ms_asc reported a fully bounded waste number")
	}
	found := false
	for _, m := range rep.Unbounded {
		if m == policy.KeyWallMSAsc {
			found = true
		}
	}
	if !found {
		t.Fatalf("wall_ms_asc is not named in the unbounded list: %v", rep.Unbounded)
	}
	if rep.WasteMS != 0 {
		t.Fatalf("failing open must count every receipt as influential; waste is %d ms", rep.WasteMS)
	}
}

// An unmeasured collected_base is an UNKNOWN ceiling for tests_passed, and
// an unknown ceiling makes the bracket fail open — under-reporting waste
// rather than over-reporting it, which is the safe direction for a metric
// that grades the scheduler.
//
// It fails open PER RECEIPT, not for the whole race: only the receipts that
// carry the unbounded metric lose their verdict. A collect receipt reads no
// ranking key, so its bracket is fully bounded and its verdict stands — and
// blanking the whole report would throw away the number the metric exists
// for.
func TestUnknownCollectedBaseFailsOpenPerReceipt(t *testing.T) {
	f := newFixture(t, wastePolicy)
	rep := f.waste(t, schedule.Bounds{})
	if len(rep.Unbounded) == 0 {
		t.Fatal("an unmeasured collected_base did not produce an unbounded ranking metric")
	}
	named := false
	for _, m := range rep.Unbounded {
		if m == policy.MetricTestsPassed {
			named = true
		}
	}
	if !named {
		t.Fatalf("tests_passed is not named in the unbounded list: %v", rep.Unbounded)
	}
	for _, rr := range f.receipts {
		row, _ := rowFor(rep, rr.Digest)
		if rr.Receipt.Oracle.ID == policy.KindPytestSuite {
			// Influential either way: fail-closed may settle it before the
			// unknown ceiling is ever needed, and where it does not, the
			// missing ceiling fails open to the same verdict. What must never
			// happen is a suite receipt reading as WASTE on a bound nobody
			// holds.
			if !row.Influenced {
				t.Fatalf("a suite receipt carrying the unbounded ranking metric read as waste: %+v", row)
			}
			continue
		}
		if !row.Bounded {
			t.Fatalf("a collect receipt reads no ranking key and must stay bounded: %+v", row)
		}
	}
	bounded := f.waste(t, schedule.Bounds{CollectedBase: collectBase})
	if rep.WasteMS >= bounded.WasteMS+1 {
		t.Fatalf("failing open must never report MORE waste than the bounded run: %d vs %d",
			rep.WasteMS, bounded.WasteMS)
	}
}

// THE SECURITY PROPERTY OF THE BLOCK (decision 3b, adversarial vector 23).
// A candidate reporting tests_passed = 500 on an eight-test repository must
// not thereby set the bracket's ceiling: pass-max's ceiling for tests_passed
// is collected_base — a base-tree measurement produced before any candidate
// existed — and NEVER the candidate's own number.
func TestPassMaxCeilingIsNeverCandidateAuthored(t *testing.T) {
	pol := decodePolicy(t, wastePolicy)
	forged := makeReceipt(t, pol, "mv0:world", "suite", "pass", 5, map[string]int64{
		policy.MetricTestsPassed: 500, policy.MetricTestsTotal: 500,
		policy.MetricTestsFailed: 0, policy.MetricTestsErrored: 0,
	})
	sub, bounded := schedule.Substitute(pol, forged.Receipt, schedule.Bounds{CollectedBase: collectBase}, schedule.OutcomePassMax)
	if !bounded {
		t.Fatal("pass-max reported unbounded with collected_base measured")
	}
	if got := sub.Result.Metrics[policy.MetricTestsPassed]; got != collectBase {
		t.Fatalf("pass-max set tests_passed to %d; it must be clamped to collected_base %d, never the candidate's 500", got, collectBase)
	}
	for name, v := range sub.Result.Metrics {
		if v > 500 {
			t.Fatalf("pass-max invented a value above the candidate's own report for %s: %d", name, v)
		}
	}
	// And with no measured base there is no ceiling at all, which fails open
	// rather than borrowing the candidate's number.
	if _, ok := schedule.Substitute(pol, forged.Receipt, schedule.Bounds{}, schedule.OutcomePassMax); ok {
		t.Fatal("pass-max claimed a bound for tests_passed with no collected_base measured")
	}
}

// fail-closed is the M1f/M1e floor and it is structural rather than a
// choice: status = error with the metrics ABSENT is what a purchase we
// declined to make looks like to Decide.
func TestFailClosedIsStatusErrorWithNoMetrics(t *testing.T) {
	pol := decodePolicy(t, wastePolicy)
	rr := makeReceipt(t, pol, "mv0:world", "suite", "pass", 5, map[string]int64{policy.MetricTestsPassed: 8})
	sub, bounded := schedule.Substitute(pol, rr.Receipt, schedule.Bounds{}, schedule.OutcomeFailClosed)
	if !bounded {
		t.Fatal("fail-closed needs no bound and must never report one missing")
	}
	if sub.Result.Status != "error" {
		t.Fatalf("fail-closed status is %q, want error", sub.Result.Status)
	}
	if len(sub.Result.Metrics) != 0 {
		t.Fatalf("fail-closed carries metrics %v; an absent metric is the whole point", sub.Result.Metrics)
	}
	if sub.Result.Metrics == nil {
		t.Fatal("fail-closed metrics map is nil; {} is 'measured nothing', null is a lie about the record")
	}
	// The substitution rewrites the RESULT and nothing else: the world it
	// judged, its freshness binding and its identity are facts about the run,
	// not about the counterfactual.
	if sub.World != rr.Receipt.World || sub.Freshness != rr.Receipt.Freshness || sub.Oracle != rr.Receipt.Oracle {
		t.Fatal("fail-closed rewrote a field outside result")
	}
}

// Substitute is pure: it must not mutate the receipt it is handed, or the
// waste metric would corrupt the very evidence it is reading.
func TestSubstituteDoesNotMutateItsInput(t *testing.T) {
	pol := decodePolicy(t, wastePolicy)
	rr := makeReceipt(t, pol, "mv0:world", "suite", "pass", 5, map[string]int64{policy.MetricTestsPassed: 500})
	for _, outcome := range schedule.Outcomes() {
		_, _ = schedule.Substitute(pol, rr.Receipt, schedule.Bounds{CollectedBase: collectBase}, outcome)
	}
	if got := rr.Receipt.Result.Metrics[policy.MetricTestsPassed]; got != 500 {
		t.Fatalf("Substitute mutated the recorded receipt: tests_passed is now %d", got)
	}
	if rr.Receipt.Result.Status != "pass" {
		t.Fatalf("Substitute mutated the recorded status: %q", rr.Receipt.Result.Status)
	}
}

// Waste without a decision function is an error, not a zero: a metric that
// silently reported "nothing was wasted" because it could not ask anything
// would be the over-claim this project exists to remove.
func TestWasteRefusesWithoutADecisionFunction(t *testing.T) {
	_, err := schedule.Waste(schedule.WasteInput{Policy: decodePolicy(t, wastePolicy)})
	if err == nil || !strings.Contains(err.Error(), "decision function") {
		t.Fatalf("Waste without a DecideFn returned %v", err)
	}
}
