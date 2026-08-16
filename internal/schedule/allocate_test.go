package schedule

// The allocation rule on CONSTRUCTED evidence states. Everything here is
// pure: no worlds, no Python, no docker, no ledger, and no real decision
// function — the scheduler holds a REFERENCE to the decision rule
// (decision 1), so a test can hand it one that counts its own calls.

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// testPolicy compiles the shipped default's SHAPE — guard, collect, suite,
// ranked by gate_pass then tests_passed_desc — through the real validator
// and compiler, so every selector, gate predicate and effective key in these
// tests is the one a race would use.
func testPolicy(t *testing.T) policy.Policy {
	t.Helper()
	p := object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "sched",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{}, Args: []string{}},
			{Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{}},
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{}},
		},
		HardGates: []object.GateSpec{
			{Gate: policy.GatePathsUnmodified, Oracle: "guard", Basis: object.BasisConstruction},
			{Gate: policy.GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{policy.KeyGatePass, policy.KeyTestsPassedDesc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}},
		Paths:      object.PathSpec{Protected: []string{"tests/**"}, Harness: []string{"conftest.py"}},
	}
	b, err := object.Canonical(p)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	pol, err := policy.Decode(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return pol
}

func world(t *testing.T, tag string) object.RecordedWorld {
	t.Helper()
	return object.RecordedWorld{
		Digest: "mv0:" + strings.Repeat(tag, 64/len(tag)),
		World: object.World{
			Schema: object.SchemaWorld, Outcome: object.OutcomeCompleted,
			Tree: "git:" + strings.Repeat(tag, 40/len(tag)), Env: "mv0:env",
		},
	}
}

// receiptFor builds a BOUND receipt for one declared oracle instance, the
// same shape the orchestrator records.
func receiptFor(t *testing.T, pol policy.Policy, w object.RecordedWorld, name string,
	status string, wallMS int64, metrics map[string]int64) object.RecordedReceipt {
	t.Helper()
	o, ok := pol.OracleByName(name)
	if !ok {
		t.Fatalf("policy declares no oracle %q", name)
	}
	rec := object.Receipt{
		Schema: object.SchemaReceipt, World: w.Digest,
		Oracle:    object.OracleRef{ID: o.Kind, Config: o.Config},
		Execution: object.Execution{Argv: []string{}},
		Result: object.Result{
			Status: status, Metrics: metrics,
			Tools: map[string]string{}, Artifacts: []string{},
		},
		Freshness: object.Freshness{
			Basis:    object.BasisConstruction,
			ValidFor: object.ValidFor{Tree: w.World.Tree, Env: w.World.Env},
		},
		Family:      policy.KindFamily(o.Kind),
		Cost:        object.Cost{WallMS: wallMS},
		Inputs:      object.NoInputs(),
		Correlation: policy.KindCorrelation(o.Kind),
	}
	dig, _, err := object.Digest(rec)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return object.RecordedReceipt{Digest: dig, Receipt: rec}
}

func guardPass() map[string]int64 {
	return map[string]int64{
		policy.MetricProtectedModified: 0, policy.MetricProtectedDeleted: 0,
		policy.MetricProtectedAdded: 0, policy.MetricHarnessModified: 0,
		policy.MetricHarnessDeleted: 0, policy.MetricHarnessAdded: 0,
		policy.MetricPathsExamined: 12,
	}
}

func collectPass() map[string]int64 {
	return map[string]int64{policy.MetricCollectedTotal: 8, policy.MetricCollectedDelta: 0}
}

func suitePass(passed int64) map[string]int64 {
	return map[string]int64{policy.MetricTestsTotal: passed, policy.MetricTestsPassed: passed}
}

// stubDecide is a stand-in decision rule with the two properties the
// allocation rule actually reads: it is a PURE function of the receipts, and
// its Subject is EVERY candidate in ranked order (passing worlds first, by
// tests_passed descending, then by digest) — which is what race.Decide's
// Subject is, and why withholding a receipt from a passing world can move
// the subject at all.
//
// It counts its calls, which is how the lookahead's cost is asserted rather
// than assumed.
func stubDecide(calls *int) DecideFn {
	return func(pol policy.Policy, ws []object.RecordedWorld, rs []object.RecordedReceipt) object.Decision {
		*calls++
		type row struct {
			dig    string
			pass   bool
			passed int64
		}
		rows := make([]row, 0, len(ws))
		for _, w := range ws {
			mine := make([]object.RecordedReceipt, 0, len(rs))
			for _, r := range rs {
				if r.Receipt.World == w.Digest {
					mine = append(mine, r)
				}
			}
			r := row{dig: w.Digest, pass: gatesPassed(pol, w, mine)}
			for _, k := range pol.Keys {
				if k.Metric != policy.MetricTestsPassed {
					continue
				}
				if rec := countedReceipt(k.Sel, w, mine); rec != nil {
					r.passed = rec.Result.Metrics[policy.MetricTestsPassed]
				}
			}
			rows = append(rows, r)
		}
		sort.SliceStable(rows, func(i, j int) bool {
			switch {
			case rows[i].pass != rows[j].pass:
				return rows[i].pass
			case rows[i].pass && rows[i].passed != rows[j].passed:
				return rows[i].passed > rows[j].passed
			default:
				return rows[i].dig < rows[j].dig
			}
		})
		d := object.Decision{Type: "REJECT", Subject: []string{}}
		for _, r := range rows {
			d.Subject = append(d.Subject, r.dig)
			if r.pass {
				d.Type = "SELECT"
			}
		}
		// M1e rule 2, reproduced because it is the one consumer that reads
		// metric PRESENCE rather than a threshold — the shape the bracket
		// could not construct evidence for. The winner must carry a counted
		// receipt from every required oracle, and that receipt must emit at
		// least one metric of its kind's vocabulary.
		if d.Type == "SELECT" && len(pol.Esc.RequireEvidence) > 0 {
			win := d.Subject[0]
			for _, req := range pol.Esc.RequireEvidence {
				var w object.RecordedWorld
				for _, cand := range ws {
					if cand.Digest == win {
						w = cand
					}
				}
				mine := make([]object.RecordedReceipt, 0, len(rs))
				for _, r := range rs {
					if r.Receipt.World == win {
						mine = append(mine, r)
					}
				}
				rec := countedReceipt(req.Sel, w, mine)
				emits := false
				if rec != nil {
					for _, m := range policy.KindMetrics(req.Sel.ID) {
						if _, ok := rec.Result.Metrics[m]; ok {
							emits = true
						}
					}
				}
				if rec == nil || !emits {
					d.Type = "ESCALATE"
				}
			}
		}
		return d
	}
}

func newSched(t *testing.T, pol policy.Policy, cfg Config, ws ...object.RecordedWorld) *Scheduler {
	t.Helper()
	cfg.Policy = pol
	if cfg.Batch == 0 {
		cfg.Batch = 1
	}
	if cfg.Decide == nil {
		n := 0
		cfg.Decide = stubDecide(&n)
	}
	if cfg.Costs == nil {
		cfg.Costs = NewTable(nil, policy.AutoloadOff, cfg.Bounds)
	}
	s, err := New(cfg, ws)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func rowFor(step Step, world, oracle string) (Considered, bool) {
	for _, r := range step.Considered {
		if r.World == world && r.Oracle == oracle {
			return r, true
		}
	}
	return Considered{}, false
}

// The frontier is ONE purchase per alive world, its next unpurchased rung in
// POLICY GATE ORDER (decision 2). A world may not buy rung k+1 before rung
// k, which is what preserves M1e decision 12's short-circuit verbatim.
func TestFrontierIsOneRungPerWorldInPolicyOrder(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, a, b)

	front := s.Frontier()
	if len(front) != 2 {
		t.Fatalf("frontier = %d purchases, want one per alive world", len(front))
	}
	for _, p := range front {
		if p.Oracle() != "guard" {
			t.Errorf("world %s frontier = %q, want the ladder's first rung", p.World(), p.Oracle())
		}
	}
	s.Record(receiptFor(t, pol, a, "guard", "pass", 11, guardPass()))
	for _, p := range s.Frontier() {
		want := "guard"
		if p.World() == a.Digest {
			want = "collect"
		}
		if p.Oracle() != want {
			t.Errorf("world %s frontier = %q, want %q", p.World(), p.Oracle(), want)
		}
	}
}

// A world whose evaluated hard gate FAILED is not alive: it has no frontier
// purchase and buys nothing more. That is the elimination the whole budget
// argument rests on — and it is the policy's own gate predicate deciding it,
// not a second opinion held by the scheduler.
func TestFailedGateEliminatesTheWorld(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, a, b)
	bad := guardPass()
	bad[policy.MetricHarnessModified] = 1
	s.Record(receiptFor(t, pol, a, "guard", "fail", 11, bad))

	if s.Alive(a.Digest) {
		t.Error("world that failed a hard gate is still alive")
	}
	front := s.Frontier()
	if len(front) != 1 || front[0].World() != b.Digest {
		t.Fatalf("frontier = %v, want only the surviving world", front)
	}
}

// FRAGILE RANKING vs DECIDED, the same construction twice with one number
// changed.
//
// The incumbent holds the whole ladder. A rival standing at rung 1 is worth
// buying iff it could still overtake — which is decided by the CONTROL-PLANE
// ceiling on the ranking metric, never by the incumbent's self-report.
func TestFlipDistinguishesFragileRankingFromDecided(t *testing.T) {
	pol := testPolicy(t)
	// "a" sorts before "b", so a tie on tests_passed leaves the incumbent in
	// front and the rival cannot take the subject by tying.
	inc, rival := world(t, "a"), world(t, "b")

	for _, tc := range []struct {
		name       string
		incPassed  int64
		wantFlip   int
		wantBought bool
	}{
		{"fragile: the incumbent is below the ceiling and can be overtaken", 5, 1, true},
		{"decided: the incumbent is AT the ceiling and cannot be overtaken", 8, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, inc, rival)
			s.Record(receiptFor(t, pol, inc, "guard", "pass", 11, guardPass()))
			s.Record(receiptFor(t, pol, inc, "collect", "pass", 400, collectPass()))
			s.Record(receiptFor(t, pol, inc, "suite", "pass", 420, suitePass(tc.incPassed)))

			step, _ := s.Next()
			row, ok := rowFor(step, rival.Digest, "guard")
			if !ok {
				t.Fatalf("no considered row for the rival: %+v", step.Considered)
			}
			if row.Flip != tc.wantFlip {
				t.Errorf("flip = %d, want %d (outcomes %v)", row.Flip, tc.wantFlip, row.FlipOutcomes)
			}
			// THE RUNG IS BOUGHT EITHER WAY, and that is the red team's
			// correction (decision 3c). `flip` still discriminates — it is
			// the research signal, and it is what orders the queue — but a
			// HARD GATE may not be declined on a live world, because
			// withholding its receipt fabricates a gate failure: the world
			// drops out of the pass set, the tie or the shortfall that
			// would have escalated never materialises, and the race admits
			// where the exhaustive ladder escalated. Measured: 5 of 8 races
			// admitted a cheat whose rival's last gate was never bought.
			bought := len(step.Chosen) == 1 && step.Chosen[0].World == rival.Digest
			if !bought {
				t.Errorf("a hard-gated rung on a live world was not bought (declined %q)", row.Declined)
			}
			if row.Declined != "" {
				t.Errorf("declined reason = %q, want none: a hard gate is never decision-inert", row.Declined)
			}
			if !row.Admissible {
				t.Errorf("admissible = false on a hard-gated rung (flip %d)", row.Flip)
			}
		})
	}
}

// The decision-inert sentence is a CLAIM about the policy — "nothing reads
// this rung" — and it must never appear on a rung a hard gate reads. The
// first draft printed it for every zero-valued row, which put a permanently
// recorded false statement into `oracle.skipped` under a policy whose gates
// named the very kind it said nothing read.
func TestDeclineSentenceNeverContradictsTheHardGateFlag(t *testing.T) {
	pol := testPolicy(t)
	inc, rival := world(t, "a"), world(t, "b")
	s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, inc, rival)
	s.Record(receiptFor(t, pol, inc, "guard", "pass", 11, guardPass()))
	s.Record(receiptFor(t, pol, inc, "collect", "pass", 400, collectPass()))
	s.Record(receiptFor(t, pol, inc, "suite", "pass", 420, suitePass(8)))

	for step, more := s.Next(); ; step, more = s.Next() {
		for _, row := range step.Considered {
			if row.HardGate && strings.Contains(row.Declined, "decision-inert") {
				t.Errorf("hard-gated %s/%s carries the decision-inert sentence: %q",
					row.World, row.Oracle, row.Declined)
			}
			if row.HardGate && !row.Admissible {
				t.Errorf("hard-gated %s/%s scored inadmissible", row.World, row.Oracle)
			}
		}
		if !more {
			break
		}
		for _, c := range step.Chosen {
			s.Record(receiptFor(t, pol, s.byDig[c.World], c.Oracle, "pass", 10, ladderMetrics(c.Oracle)))
		}
	}
}

// TWO HARD-GATED INSTANCES OF ONE KIND, which is the shape the shipped
// fixtures never had and the discount could therefore refuse.
//
// The second `pytest-suite` instance shares SIGNAL and CORPUS with the first,
// so the redundancy tier is near-duplicate, `discount_bp` is 0, and
// `value_bp = flip × 0 × exec / 10 000` is 0. Under the first rule — "nothing
// with value_bp == 0 is bought" — the rung was refused, its gate had no
// receipt, the gate failed, and the adaptive arm recorded REJECT where the
// fixed ladder recorded SELECT. A CORRELATION DISCOUNT MUST NOT OWN
// TERMINATION: it orders the queue, and a gate the policy declares is not
// marginal evidence whose value can be discounted away.
func TestNearDuplicateDiscountCannotRefuseAHardGate(t *testing.T) {
	pol := twoSuitePolicy(t)
	a := world(t, "a")
	s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, a)
	s.Record(receiptFor(t, pol, a, "guard", "pass", 11, guardPass()))
	s.Record(receiptFor(t, pol, a, "collect", "pass", 400, collectPass()))
	s.Record(receiptFor(t, pol, a, "suite", "pass", 420, suitePass(8)))

	step, more := s.Next()
	row, ok := rowFor(step, a.Digest, "suite2")
	if !ok {
		t.Fatalf("no considered row for the second suite instance: %+v", step.Considered)
	}
	if row.DiscountBP != 0 {
		t.Fatalf("discount = %d, want 0: the fixture must reach the near-duplicate tier to test anything",
			row.DiscountBP)
	}
	if !row.HardGate {
		t.Fatal("the fixture's second suite instance is not hard-gated")
	}
	if row.ValueBP != 0 {
		t.Errorf("value_bp = %d, want 0: the discount still ORDERS the row", row.ValueBP)
	}
	if !row.Admissible || row.Declined != "" {
		t.Errorf("a hard gate was refused by an ordering term: admissible=%v declined=%q",
			row.Admissible, row.Declined)
	}
	if !more || len(step.Chosen) != 1 || step.Chosen[0].Oracle != "suite2" {
		t.Errorf("chosen = %+v, want the second suite instance bought", step.Chosen)
	}
}

// A RUNG WHOSE ONLY CONSUMER IS `require_evidence` (M1e rule 2), which tests
// metric PRESENCE rather than any threshold.
//
// The bracket seeded only the metrics a gate or a ranking key reads, so the
// synthetic receipt for such a rung carried an EMPTY metric map, rule 2 fired
// on every bracket outcome exactly as it fired on the empty base, nothing
// moved, flip was 0 — and the scheduler refused to buy the very receipt the
// escalation is about, escalating on its own refusal.
func TestRequireEvidenceOnlyRungIsPurchasable(t *testing.T) {
	pol := requireEvidencePolicy(t)
	a := world(t, "a")
	s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8, CorpusCases: 4}}, a)
	s.Record(receiptFor(t, pol, a, "guard", "pass", 11, guardPass()))
	s.Record(receiptFor(t, pol, a, "collect", "pass", 400, collectPass()))
	s.Record(receiptFor(t, pol, a, "suite", "pass", 420, suitePass(8)))

	step, more := s.Next()
	row, ok := rowFor(step, a.Digest, "observe")
	if !ok {
		t.Fatalf("no considered row for the require_evidence rung: %+v", step.Considered)
	}
	if row.HardGate {
		t.Fatal("the fixture's observe rung is hard-gated; then it proves nothing about presence-only consumers")
	}
	if row.Flip != 1 {
		t.Errorf("flip = %d on a rung an escalation rule reads, want 1 (outcomes %v)", row.Flip, row.FlipOutcomes)
	}
	if row.Declined != "" {
		t.Errorf("declined %q — the scheduler refused the receipt its own escalation rule is about", row.Declined)
	}
	if !more || len(step.Chosen) != 1 || step.Chosen[0].Oracle != "observe" {
		t.Errorf("chosen = %+v, want the observe rung bought", step.Chosen)
	}
}

// twoSuitePolicy declares TWO hard-gated pytest-suite instances: the
// near-duplicate shape that drives the correlation discount to zero.
func twoSuitePolicy(t *testing.T) policy.Policy {
	t.Helper()
	p := object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "twosuite",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{}, Args: []string{}},
			{Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{}},
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{}},
			{Name: "suite2", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{"-q"}},
		},
		HardGates: []object.GateSpec{
			{Gate: policy.GatePathsUnmodified, Oracle: "guard", Basis: object.BasisConstruction},
			{Gate: policy.GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
			{Gate: policy.GateNoFailedTests, Oracle: "suite2", Basis: object.BasisConstruction},
		},
		Ranking:    []string{policy.KeyGatePass},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}},
		Paths:      object.PathSpec{Protected: []string{"tests/**"}, Harness: []string{"conftest.py"}},
	}
	return compile(t, p)
}

// requireEvidencePolicy declares a corpus-observe instance that NO gate and
// NO ranking key reads: its only consumer is `require_evidence`, which tests
// whether the winner's receipt carries any of the kind's metrics at all.
func requireEvidencePolicy(t *testing.T) policy.Policy {
	t.Helper()
	p := object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "reqev",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{}, Args: []string{}},
			{Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{}},
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{}},
			{Name: "observe", Kind: policy.KindCorpusObserve, Argv: []string{}, Args: []string{},
				Corpus: object.CorpusSpec{Provider: policy.ProviderDeclared, File: "corpora/c.json"}},
		},
		HardGates: []object.GateSpec{
			{Gate: policy.GatePathsUnmodified, Oracle: "guard", Basis: object.BasisConstruction},
			{Gate: policy.GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{policy.KeyGatePass, policy.KeyTestsPassedDesc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{"observe"}},
		Paths:      object.PathSpec{Protected: []string{"tests/**"}, Harness: []string{"conftest.py"}},
	}
	return compile(t, p)
}

func compile(t *testing.T, p object.PolicyV1) policy.Policy {
	t.Helper()
	b, err := object.Canonical(p)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	pol, err := policy.Decode(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return pol
}

// ladderMetrics is a passing metric set for one rung of the test policy's
// ladder.
func ladderMetrics(oracle string) map[string]int64 {
	switch oracle {
	case "guard":
		return guardPass()
	case "collect":
		return collectPass()
	default:
		return suitePass(8)
	}
}

// Vector 23, the rival-starvation attack: a candidate reports an unreachable
// tests_passed so that no rival's pass-max can overtake it, driving flip to
// 0 on every rival and stopping the buying.
//
// The clamp neutralizes it, and this test asserts the mechanism (the
// lookahead sees the CEILING, not the self-report) and the consequence (the
// honest rival's rung is still bought).
func TestControlPlaneClampNeutralizesRivalStarvation(t *testing.T) {
	pol := testPolicy(t)
	// The honest world sorts FIRST, so on a tie at the control-plane ceiling
	// it takes the subject — which is the outcome the forgery exists to make
	// unreachable.
	honest, forger := world(t, "a"), world(t, "b")
	bounds := Bounds{CollectedBase: 8}

	s := newSched(t, pol, Config{Bounds: bounds}, honest, forger)
	s.Record(receiptFor(t, pol, forger, "guard", "pass", 11, guardPass()))
	s.Record(receiptFor(t, pol, forger, "collect", "pass", 400, collectPass()))
	// An eight-test repository reporting five hundred passing tests. M1f's
	// collect-equals-suite-total invariant does not save us here: a COHERENT
	// forgery authors both numbers.
	s.Record(receiptFor(t, pol, forger, "suite", "pass", 420, suitePass(500)))

	calls := 0
	dec := stubDecide(&calls)
	raw := s.Receipts()
	guard, rest := s.rungs[0], s.rungs[1:]

	// THE ATTACK, with the bracket clamped and the incumbent NOT — the half
	// measure the design warns against. The rival's pass-max is bounded at 8
	// and the incumbent's self-report is 500, so no outcome can overtake it
	// and the honest world is priced as worthless.
	flipHalf, _ := Lookahead(dec, pol, s.worlds, raw, dec(pol, s.worlds, raw), honest, guard, rest, bounds)
	if flipHalf != 0 {
		t.Fatalf("flip = %d with an unclamped incumbent; the starvation attack is supposed to work here", flipHalf)
	}

	// THE FIX: the incumbent is clamped by the SAME control-plane bound the
	// bracket is, so the comparison is between two numbers the candidate did
	// not author.
	clamped := clampReceipts(pol, raw, bounds)
	for _, rr := range clamped {
		if v, ok := rr.Receipt.Result.Metrics[policy.MetricTestsPassed]; ok && v != 8 {
			t.Errorf("lookahead saw tests_passed = %d, want it clamped to collected_base = 8", v)
		}
	}
	for _, rr := range raw {
		if v, ok := rr.Receipt.Result.Metrics[policy.MetricTestsPassed]; ok && v != 500 {
			t.Errorf("RECORDED tests_passed = %d, want the receipt untouched at 500 — Decide still sees it", v)
		}
	}

	step, more := s.Next()
	if !more || len(step.Chosen) != 1 || step.Chosen[0].World != honest.Digest {
		t.Fatalf("the honest rival was not bought: chosen = %+v", step.Chosen)
	}
	row, _ := rowFor(step, honest.Digest, "guard")
	if row.Flip != 1 {
		t.Errorf("flip = %d on the honest rival, want 1 (outcomes %v)", row.Flip, row.FlipOutcomes)
	}
}

// An unknown bound makes a purchase look VALUABLE and gets it bought.
// `Decide` fails closed, the scheduler fails open: two layers, two opposite
// fail directions, each safe in its own layer.
func TestUnknownCeilingFailsOpen(t *testing.T) {
	pol := testPolicy(t)
	inc, rival := world(t, "a"), world(t, "b")
	// No CollectedBase: tests_passed has no control-plane ceiling at all.
	s := newSched(t, pol, Config{Bounds: Bounds{}}, inc, rival)
	s.Record(receiptFor(t, pol, inc, "guard", "pass", 11, guardPass()))
	s.Record(receiptFor(t, pol, inc, "collect", "pass", 400, collectPass()))
	s.Record(receiptFor(t, pol, inc, "suite", "pass", 420, suitePass(8)))

	step, _ := s.Next()
	row, ok := rowFor(step, rival.Digest, "guard")
	if !ok {
		t.Fatalf("no row for the rival")
	}
	if row.Flip != 1 {
		t.Errorf("flip = %d with no ceiling to bound the incumbent, want 1 (fail open)", row.Flip)
	}
	if len(row.FlipOutcomes) == 0 || !strings.Contains(row.FlipOutcomes[0], "unbounded") {
		t.Errorf("flip_outcomes = %v, want the unbounded note", row.FlipOutcomes)
	}
}

// The metalevel's cost, asserted rather than assumed: one step costs exactly
// 1 base call + 3 per frontier purchase + 1 for the decision the trace
// records. Purity is what makes that affordable — the scheduler may call the
// decision rule as often as it likes, with no side effects and no ledger
// writes.
func TestLookaheadCallCountIsExactlyThreePerPurchase(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	calls := 0
	s := newSched(t, pol, Config{Decide: stubDecide(&calls), Bounds: Bounds{CollectedBase: 8}}, a, b)

	calls = 0
	step, _ := s.Next()
	want := 1 + 3*len(step.Considered) + 1
	if calls != want {
		t.Errorf("Decide called %d times for %d considered purchases, want %d",
			calls, len(step.Considered), want)
	}
}

// The batch is the top-k affordable frontier purchases, and the tie-break is
// TOTAL: equal scores order by (cost asc, world digest asc, oracle asc), so
// two runs of one race allocate identically.
func TestBatchIsTopKWithDeterministicTies(t *testing.T) {
	pol := testPolicy(t)
	ws := []object.RecordedWorld{world(t, "c"), world(t, "a"), world(t, "b")}
	s := newSched(t, pol, Config{Batch: 2, Bounds: Bounds{CollectedBase: 8}}, ws...)

	step, more := s.Next()
	if !more {
		t.Fatal("scheduler stopped on a fresh race")
	}
	if len(step.Chosen) != 2 {
		t.Fatalf("batch = %d, want the parallelism degree", len(step.Chosen))
	}
	if step.Staleness != 1 {
		t.Errorf("staleness = %d, want batch-1", step.Staleness)
	}
	if step.Chosen[0].World >= step.Chosen[1].World {
		t.Errorf("tied purchases not in digest order: %+v", step.Chosen)
	}
	// The third world is DEFERRED, not refused: it may be bought next batch.
	deferred := 0
	for _, r := range step.Considered {
		if r.Declined == "not this batch" {
			deferred++
		}
	}
	if deferred != 1 {
		t.Errorf("deferred rows = %d, want exactly the purchase that lost the batch", deferred)
	}
}

// ON AN UNFITTED WORKSPACE THE BUDGET DOES NOT BIND, AND THAT IS DELIBERATE.
//
// This test pins a KNOWN RESIDUAL rather than a property anybody is proud of,
// and it exists so the residual cannot be closed by accident. With no fitted
// coefficient every rung is priced `declared-rank` and carries no millisecond
// figure, so no purchase can be compared against a share; v0 buys it anyway
// while the pool is non-empty, because `Decide` fails closed and THE
// SCHEDULER FAILS OPEN (decision 3b). The measured cost of that: adversarial
// vector 24 spends 3 238 ms of a 400 ms budget, an 8.1x overrun.
//
// The obvious repair — refuse an unpriced rung once its world's own spend
// exceeds its share — was implemented and reverted, because at MATCHED budget
// on testdata/toyrepo/policies/schedule.json it turned the fixed ladder's
// SELECT into an ESCALATE by refusing a 33 ms `corpus-observe` with 38 ms
// still in the pool. Trading an overrun for a false rejection is the wrong
// trade in a block whose one safety theorem is about false ADMISSIONS.
//
// So: the overrun is asserted here, with the reason, so that a future change
// that makes the budget bind has to come with a measurement showing it did
// not manufacture a rejection.
func TestUnpricedPurchaseOverrunsTheBudgetAndSaysSo(t *testing.T) {
	pol := testPolicy(t)
	a := world(t, "a")
	// No samples at all: every rung falls back to the declared rank (decision
	// 7c) and NO millisecond figure exists for any of them.
	s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}, BudgetMS: 400, Batch: 1}, a)

	step, more := s.Next()
	if !more || len(step.Chosen) != 1 {
		t.Fatalf("step 1 chose %d purchases, want the first rung affordable on a fresh workspace", len(step.Chosen))
	}
	if r, ok := rowFor(step, a.Digest, "guard"); !ok || r.CostMS != 0 || !r.Affordable {
		t.Errorf("first row = %+v, want an unpriced but affordable rung", r)
	}
	s.Record(receiptFor(t, pol, a, "guard", "pass", 11, guardPass()))

	// 305 ms of a 400 ms budget: 95 ms remain, which prices nothing, and the
	// next rung is bought against it anyway.
	step, more = s.Next()
	if !more {
		t.Fatal("the scheduler stopped before buying an unpriced rung it could not price")
	}
	s.Record(receiptFor(t, pol, a, "collect", "pass", 305, collectPass()))

	step, more = s.Next()
	if !more || len(step.Chosen) != 1 || step.Chosen[0].Oracle != "suite" {
		t.Fatalf("with 84 ms left the scheduler chose %+v, want it to fail OPEN and buy the unpriced suite", step.Chosen)
	}
	// The suite costs 3 238 ms — vector 24's real figure — against 84 ms of
	// remaining budget. It is bought, and the overrun is recorded rather than
	// hidden.
	s.Record(receiptFor(t, pol, a, "suite", "pass", 3238, suitePass(9)))
	f := s.Finish()
	if f.Budget.SpentMS <= 400 {
		t.Errorf("spent = %d ms, want the overrun to be visible against the 400 ms bound", f.Budget.SpentMS)
	}
	if f.Budget.RemainingMS != 0 {
		t.Errorf("remaining = %d ms, want an exhausted pool to report 0 rather than a negative", f.Budget.RemainingMS)
	}
}

// THE REFUSAL NAMES THE BOUND THAT REFUSED IT. An unpriced rung is never
// refused by a share — it is refused only when the pool is empty — so the
// sentence must not claim a prediction was compared against one. A
// millisecond figure for a kind nobody measured must never appear at all
// (decision 7c), and "predicted … exceeds this world's share" would print
// both a fabricated comparison and a fabricated number.
func TestUnpricedDeclineNamesTheEmptyPoolNotAShare(t *testing.T) {
	pol := testPolicy(t)
	a := world(t, "a")
	s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}, BudgetMS: 50, Batch: 1}, a)

	if _, more := s.Next(); !more {
		t.Fatal("nothing was affordable on a fresh workspace")
	}
	s.Record(receiptFor(t, pol, a, "guard", "pass", 300, guardPass()))

	step, more := s.Next()
	if more {
		t.Fatalf("the pool is empty and the scheduler still chose %+v", step.Chosen)
	}
	row, ok := rowFor(step, a.Digest, "collect")
	if !ok {
		t.Fatalf("no collect row in the final step: %+v", step.Considered)
	}
	if strings.Contains(row.Declined, "predicted") || strings.Contains(row.Declined, "share") {
		t.Errorf("declined = %q, want the empty-pool sentence, not a share comparison nobody made", row.Declined)
	}
	if !strings.Contains(row.Declined, "declared rank") || !strings.Contains(row.Declined, "budget is spent") {
		t.Errorf("declined = %q, want the declared rank and the empty pool named", row.Declined)
	}
}

// STOPPING UNDER BUDGET EXHAUSTION. The starved stop is the interesting one:
// something could still have changed the decision, and the money ran out.
func TestStopsWhenBudgetIsExhausted(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	// A fitted suite coefficient: 400 ms fixed + 10 ms/test over 8 tests.
	samples := []Sample{
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 1, WallMS: 410},
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 2, WallMS: 420},
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 3, WallMS: 430},
	}
	bounds := Bounds{CollectedBase: 8}
	s := newSched(t, pol, Config{
		Bounds:   bounds,
		Costs:    NewTable(samples, policy.AutoloadOff, bounds),
		BudgetMS: 900,
		Batch:    2,
	}, a, b)

	spent := int64(0)
	steps := 0
	for {
		step, more := s.Next()
		steps++
		if !more {
			break
		}
		for _, c := range step.Chosen {
			w := a
			if c.World == b.Digest {
				w = b
			}
			var rr object.RecordedReceipt
			switch c.Oracle {
			case "guard":
				rr = receiptFor(t, pol, w, "guard", "pass", 11, guardPass())
			case "collect":
				rr = receiptFor(t, pol, w, "collect", "pass", 400, collectPass())
			default:
				t.Fatalf("suite was bought at %d ms remaining; it costs 480 ms per world", 900-spent)
			}
			spent += rr.Receipt.Cost.WallMS
			s.Record(rr)
		}
		if steps > 10 {
			t.Fatal("scheduler did not terminate")
		}
	}
	f := s.Finish()
	if f.Stop != StopBudget {
		t.Errorf("stop clause = %q, want %q", f.Stop, StopBudget)
	}
	if f.Budget.SpentMS != spent {
		t.Errorf("spent = %d ms, want the receipts' own %d ms", f.Budget.SpentMS, spent)
	}
	// A starved stop is a TERMINAL decline, and the operator has to be able
	// to see what was never found out.
	if len(s.Skipped()) == 0 {
		t.Error("a starved stop recorded no oracle.skipped rows")
	}
	for _, sk := range s.Skipped() {
		if !strings.Contains(sk.Reason, "unaffordable") {
			t.Errorf("skip reason = %q, want the unaffordable sentence", sk.Reason)
		}
	}
	// Withholding monotonicity's consequence, on this very race: the worlds
	// the budget starved cannot pass, because an unpurchased hard gate is an
	// absent required metric.
	for _, w := range []object.RecordedWorld{a, b} {
		if len(UnpaidHardGates(pol, w, s.receiptsFor(w.Digest))) == 0 {
			t.Errorf("world %s paid for every hard gate on a starved race", w.Digest)
		}
	}
}

// An UNBOUNDED budget buys the whole ladder, in ladder order, on every alive
// world: the null case the compatibility claim rests on (decision 13).
func TestUnboundedBudgetBuysTheWholeLadder(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}, Batch: 2}, a, b)

	order := map[string][]string{}
	for i := 0; ; i++ {
		step, more := s.Next()
		if !more {
			break
		}
		for _, c := range step.Chosen {
			w := a
			if c.World == b.Digest {
				w = b
			}
			order[c.World] = append(order[c.World], c.Oracle)
			switch c.Oracle {
			case "guard":
				s.Record(receiptFor(t, pol, w, "guard", "pass", 11, guardPass()))
			case "collect":
				s.Record(receiptFor(t, pol, w, "collect", "pass", 400, collectPass()))
			case "suite":
				s.Record(receiptFor(t, pol, w, "suite", "pass", 420, suitePass(8)))
			}
		}
		if i > 10 {
			t.Fatal("scheduler did not terminate")
		}
	}
	for _, w := range []object.RecordedWorld{a, b} {
		got := strings.Join(order[w.Digest], ",")
		if got != "guard,collect,suite" {
			t.Errorf("world %s bought %q, want the policy's ladder order", w.Digest, got)
		}
		if unpaid := UnpaidHardGates(pol, w, s.receiptsFor(w.Digest)); len(unpaid) != 0 {
			t.Errorf("world %s left %v unpurchased under an unbounded budget", w.Digest, unpaid)
		}
	}
	if f := s.Finish(); f.Stop != StopEmpty {
		t.Errorf("stop clause = %q, want %q", f.Stop, StopEmpty)
	}
}

// THE ASSERTION THAT MUST NEVER FIRE. A world with an unpurchased hard gate
// has an absent required metric, the gate fails, and `Decide` never names it
// Subject — so the purchase law needs no clause in the stopping rule. This
// hand-constructs the impossible state (a decision rule that SELECTS a world
// holding no receipts at all) and asserts the scheduler observes it rather
// than assuming it away.
func TestPurchaseLawAssertionFiresOnALyingDecision(t *testing.T) {
	pol := testPolicy(t)
	a := world(t, "a")
	lying := func(policy.Policy, []object.RecordedWorld, []object.RecordedReceipt) object.Decision {
		return object.Decision{Type: "SELECT", Subject: []string{a.Digest}}
	}
	s := newSched(t, pol, Config{Decide: lying, Bounds: Bounds{CollectedBase: 8}}, a)
	v := s.PurchaseLaw()
	if v == "" {
		t.Fatal("purchase law did not fire on a SELECT over an unpurchased ladder")
	}
	if !strings.Contains(v, "unpurchased hard gate") {
		t.Errorf("violation = %q, want it to name the unpurchased hard gate", v)
	}

	// And it stays silent on the honest state: the same world, having paid.
	honest := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, a)
	honest.Record(receiptFor(t, pol, a, "guard", "pass", 11, guardPass()))
	honest.Record(receiptFor(t, pol, a, "collect", "pass", 400, collectPass()))
	honest.Record(receiptFor(t, pol, a, "suite", "pass", 420, suitePass(8)))
	if v := honest.PurchaseLaw(); v != "" {
		t.Errorf("purchase law fired on a fully paid world: %s", v)
	}
}

// S-ranking, decision 4's caveat made operational: monotonicity holds for
// the PASS SET, not for the RANKING, so a passing candidate missing a
// receipt a ranking key reads is a stop the trace has to name.
func TestRankingCompletenessTracksTheRankingKeysReceipts(t *testing.T) {
	pol := testPolicy(t)
	a := world(t, "a")
	s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, a)
	// A world that has bought NOTHING is outside the pass set for the one
	// reason this clause has to look through: nobody bought its gates. The
	// first reading tested the pass set alone, which made the starved world
	// invisible to the very clause that exists to notice it — a world whose
	// WITHHELD receipt is what drops it from the pass set is exactly the
	// case decision 4's caveat is about.
	if s.RankingComplete() {
		t.Error("ranking reported complete before a single gate had been purchased")
	}
	s.Record(receiptFor(t, pol, a, "guard", "pass", 11, guardPass()))
	s.Record(receiptFor(t, pol, a, "collect", "pass", 400, collectPass()))
	if s.RankingComplete() {
		t.Error("ranking reported complete for a world still owing its suite receipt")
	}
	s.Record(receiptFor(t, pol, a, "suite", "pass", 420, suitePass(8)))
	if !s.RankingComplete() {
		t.Error("ranking reported incomplete for a world holding every ranking receipt")
	}

	// A world ELIMINATED by a failed gate is decided, not starved: no
	// ranking question survives it and it must not hold the race open.
	b := world(t, "b")
	e := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, b)
	bad := guardPass()
	bad[policy.MetricHarnessModified] = 1
	e.Record(receiptFor(t, pol, b, "guard", "fail", 11, bad))
	if !e.RankingComplete() {
		t.Error("a world eliminated by a FAILED gate reported the ranking incomplete")
	}
}

// THE LADDER-ORDER SANITY TEST. With the frontier constraint lifted, the
// score's ordering over a fresh race reproduces the policy's own gate order:
// the rule re-derives the hand-authored verification discipline from first
// principles instead of inventing a different one.
func TestScoreOrderReproducesTheHandAuthoredLadder(t *testing.T) {
	pol := testPolicy(t)
	a := world(t, "a")
	bounds := Bounds{CollectedBase: 8}
	samples := []Sample{}
	for _, k := range []struct {
		kind  string
		fixed int64
	}{
		{policy.KindTreeGuard, 10}, {policy.KindPytestCollect, 400}, {policy.KindPytestSuite, 420},
	} {
		for u := int64(1); u <= 3; u++ {
			samples = append(samples, Sample{Kind: k.kind, Seal: policy.AutoloadOff, Units: u, WallMS: k.fixed + u})
		}
	}
	s := newSched(t, pol, Config{Bounds: bounds, Costs: NewTable(samples, policy.AutoloadOff, bounds)}, a)

	calls := 0
	dec := stubDecide(&calls)
	base := dec(pol, s.worlds, nil)
	type scored struct {
		name  string
		score int64
	}
	var got []scored
	for i, r := range s.rungs {
		row := s.score(purchase{world: a, rung: r, rest: s.rungs[i+1:]}, nil, base, 0)
		got = append(got, scored{name: row.Oracle, score: row.ScoreBPPS})
	}
	sort.SliceStable(got, func(i, j int) bool { return got[i].score > got[j].score })
	var names []string
	for _, g := range got {
		names = append(names, g.name)
	}
	if strings.Join(names, ",") != "guard,collect,suite" {
		t.Errorf("score order = %v, want the policy's ladder order [guard collect suite]", names)
	}
}

// receiptsFor is a test helper: one world's receipts as the scheduler holds
// them.
func (s *Scheduler) receiptsFor(world string) []object.RecordedReceipt { return s.perWorld[world] }

// Every declined row carries a REASON, and the reasons are the closed set an
// operator has to be able to read.
func TestDeclineReasonsAreNamed(t *testing.T) {
	reasons := []string{"decision-inert", "unaffordable", "not this batch"}
	for _, r := range reasons {
		if r == "" {
			t.Fatal("empty decline reason in the closed set")
		}
	}
	pol := testPolicy(t)
	a := world(t, "a")
	s := newSched(t, pol, Config{Bounds: Bounds{CollectedBase: 8}}, a)
	step, _ := s.Next()
	for _, row := range step.Considered {
		if row.Bought() != (row.Declined == "") {
			t.Errorf("row %+v: Bought() disagrees with Declined", row)
		}
	}
	if got := fmt.Sprint(step.DecisionNow.Type); got != "REJECT" {
		t.Errorf("decision_now.type = %q on a race with no receipts, want REJECT", got)
	}
}
