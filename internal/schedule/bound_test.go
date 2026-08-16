package schedule

// The RETROSPECTIVE ALLOCATION BOUND under test (M2b1 §5): agreement with a
// brute-force reference, prefix-closure as a property, determinism, the cap's
// refusal, and — the one that proves the bound measures anything at all — a
// case where minspend is STRICTLY less than what the reference race spent.

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// boundFixture is a 2×2 race: two worlds, the shipped default's shape, one
// world passing every gate and one failing its suite. It is the smallest
// instance on which "the cheapest allocation that still decides d*" is a
// question with a non-trivial answer.
func boundFixture(t *testing.T) (policy.Policy, []object.RecordedWorld, []object.RecordedReceipt) {
	t.Helper()
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	recs := []object.RecordedReceipt{
		receiptFor(t, pol, a, "guard", "pass", 10, guardPass()),
		receiptFor(t, pol, a, "collect", "pass", 300, collectPass()),
		receiptFor(t, pol, a, "suite", "pass", 400, suitePass(8)),
		receiptFor(t, pol, b, "guard", "pass", 10, guardPass()),
		receiptFor(t, pol, b, "collect", "pass", 300, collectPass()),
		receiptFor(t, pol, b, "suite", "fail", 500, suitePass(6)),
	}
	return pol, []object.RecordedWorld{a, b}, recs
}

// bruteMin is the reference implementation: every prefix vector, scored by a
// separate loop, with no early exit and no shared code with Bound. Two
// implementations that agree are evidence; one implementation that agrees
// with itself is a tautology.
func bruteMin(t *testing.T, pol policy.Policy, ws []object.RecordedWorld,
	recs []object.RecordedReceipt, decide DecideFn) (int64, bool) {
	t.Helper()
	chains, always := boundChains(pol, ws, recs)
	star := decide(pol, ws, recs)
	best, found := int64(0), false
	var walk func(i int, chosen []object.RecordedReceipt, cost int64)
	walk = func(i int, chosen []object.RecordedReceipt, cost int64) {
		if i == len(ws) {
			d := decide(pol, ws, chosen)
			if d.Type != star.Type || boundSubject(d) != boundSubject(star) {
				return
			}
			if !found || cost < best {
				best, found = cost, true
			}
			return
		}
		chain := chains[ws[i].Digest]
		for n := 0; n <= len(chain); n++ {
			sub := append([]object.RecordedReceipt(nil), chosen...)
			add := int64(0)
			for _, rr := range chain[:n] {
				sub = append(sub, rr)
				add += rr.Receipt.Cost.WallMS
			}
			walk(i+1, sub, cost+add)
		}
	}
	walk(0, append([]object.RecordedReceipt(nil), always...), 0)
	return best, found
}

// The bound agrees with a brute-force reference, and it is STRICTLY cheaper
// than the reference race — which is what makes it a measurement rather than
// a restatement of the spend.
func TestBoundAgreesWithBruteForceAndIsStrictlyCheaper(t *testing.T) {
	pol, ws, recs := boundFixture(t)
	decide := stubDecide(new(int))
	rep, err := Bound(BoundInput{Policy: pol, Worlds: ws, Receipts: recs, Decide: decide})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	if !rep.Available {
		t.Fatalf("bound unavailable on a 2x2 fixture: %s", rep.Refused)
	}
	want, ok := bruteMin(t, pol, ws, recs, decide)
	if !ok {
		t.Fatal("the brute-force reference found no sufficient allocation")
	}
	if rep.MinSpendMS != want {
		t.Errorf("minspend = %d ms, brute force says %d ms", rep.MinSpendMS, want)
	}
	if rep.TotalMS != 1520 {
		t.Errorf("S = %d ms, want the fixture's 1520", rep.TotalMS)
	}
	if rep.MinSpendMS >= rep.TotalMS {
		t.Errorf("minspend %d is not strictly below S %d; the bound measures nothing on this fixture",
			rep.MinSpendMS, rep.TotalMS)
	}
	if rep.SavingMS != rep.TotalMS-rep.MinSpendMS {
		t.Errorf("saving %d != %d - %d", rep.SavingMS, rep.TotalMS, rep.MinSpendMS)
	}
	if rep.Subsets != 16 {
		t.Errorf("subsets = %d, want 4x4 = 16", rep.Subsets)
	}
}

// The cheapest sufficient allocation RE-DECIDES d* under the real decision
// function. The bound's whole claim is that this subset would have been
// enough, so the subset itself has to be checkable.
func TestBoundCheapestAllocationReDecidesTheTarget(t *testing.T) {
	pol, ws, recs := boundFixture(t)
	decide := stubDecide(new(int))
	rep, err := Bound(BoundInput{Policy: pol, Worlds: ws, Receipts: recs, Decide: decide})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	chains, always := boundChains(pol, ws, recs)
	sub := append([]object.RecordedReceipt(nil), always...)
	cost := int64(0)
	for _, p := range rep.Prefixes {
		if p.Rungs > p.Bought {
			t.Fatalf("world %s: prefix of %d rungs out of %d bought", p.World, p.Rungs, p.Bought)
		}
		for _, rr := range chains[p.World][:p.Rungs] {
			sub = append(sub, rr)
			cost += rr.Receipt.Cost.WallMS
		}
	}
	if cost != rep.MinSpendMS {
		t.Errorf("the reported prefixes cost %d ms, minspend says %d", cost, rep.MinSpendMS)
	}
	got := decide(pol, ws, sub)
	if got.Type != rep.Decision || boundSubject(got) != rep.Subject {
		t.Errorf("the cheapest allocation decides %s/%s, want d* = %s/%s",
			got.Type, boundSubject(got), rep.Decision, rep.Subject)
	}
}

// PREFIX-CLOSURE as a property: every allocation the bound reports is a
// PREFIX of that world's ladder in policy gate order. That constraint is what
// makes the bound a bound on ALLOCATORS — both arms operate under it — rather
// than on oracles that could buy the last rung without the first.
func TestBoundReportsOnlyPrefixAllocations(t *testing.T) {
	pol, ws, recs := boundFixture(t)
	chains, _ := boundChains(pol, ws, recs)
	want := []string{policy.KindTreeGuard, policy.KindPytestCollect, policy.KindPytestSuite}
	for _, w := range ws {
		chain := chains[w.Digest]
		if len(chain) != len(want) {
			t.Fatalf("world %s chained %d rungs, want %d", w.Digest, len(chain), len(want))
		}
		for i, rr := range chain {
			if rr.Receipt.Oracle.ID != want[i] {
				t.Errorf("world %s rung %d = %s, want %s (the chain is not in policy gate order)",
					w.Digest, i, rr.Receipt.Oracle.ID, want[i])
			}
		}
	}
	rep, err := Bound(BoundInput{Policy: pol, Worlds: ws, Receipts: recs, Decide: stubDecide(new(int))})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	for _, p := range rep.Prefixes {
		for i, name := range p.Oracles {
			if name != want[i] {
				t.Errorf("world %s allocation is not a prefix: %v", p.World, p.Oracles)
			}
		}
	}
}

// A world whose ladder STOPPED (a gate failed, so the reference race bought
// no more) has a chain that ends there, and no subset can reach past it. The
// bound never invents evidence the reference race did not buy.
func TestBoundChainStopsWhereTheReferenceRaceStopped(t *testing.T) {
	pol := testPolicy(t)
	a := world(t, "a")
	recs := []object.RecordedReceipt{
		receiptFor(t, pol, a, "guard", "fail", 10, map[string]int64{
			policy.MetricProtectedModified: 2, policy.MetricPathsExamined: 12,
		}),
	}
	rep, err := Bound(BoundInput{
		Policy: pol, Worlds: []object.RecordedWorld{a}, Receipts: recs, Decide: stubDecide(new(int)),
	})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	if rep.Subsets != 2 {
		t.Errorf("subsets = %d over a world holding one rung, want 2 (buy it or do not)", rep.Subsets)
	}
	if len(rep.Prefixes) != 1 || rep.Prefixes[0].Bought != 1 {
		t.Errorf("prefixes = %+v, want one world with one bought rung", rep.Prefixes)
	}
}

// DETERMINISM: two computations agree byte for byte. Anything less and the
// bound cannot be a denominator, because two readers would divide by
// different numbers.
func TestBoundIsDeterministic(t *testing.T) {
	pol, ws, recs := boundFixture(t)
	first, err := Bound(BoundInput{Policy: pol, Worlds: ws, Receipts: recs, Decide: stubDecide(new(int))})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	// Receipts handed in in the opposite order: the bound is a function of the
	// SET, exactly as Decide is.
	rev := make([]object.RecordedReceipt, 0, len(recs))
	for i := len(recs) - 1; i >= 0; i-- {
		rev = append(rev, recs[i])
	}
	second, err := Bound(BoundInput{Policy: pol, Worlds: ws, Receipts: rev, Decide: stubDecide(new(int))})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	a, err := Payload(first)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	b, err := Payload(second)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("two computations disagree:\n  %s\n  %s", a, b)
	}
}

// THE CAP REFUSES rather than approximating. An approximation reported under
// the name of an exact bound is the over-claim this project exists to remove.
func TestBoundCapRefusesRatherThanApproximating(t *testing.T) {
	pol, ws, recs := boundFixture(t)
	rep, err := Bound(BoundInput{
		Policy: pol, Worlds: ws, Receipts: recs, Decide: stubDecide(new(int)), Cap: 4,
	})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	if rep.Available {
		t.Fatal("the bound reported a number above its own enumeration cap")
	}
	if !strings.Contains(rep.Refused, "cap") {
		t.Errorf("the refusal does not name the cap: %q", rep.Refused)
	}
	if rep.MinSpendMS != 0 || rep.SavingMS != 0 {
		t.Errorf("a refused bound reported numbers anyway: %+v", rep)
	}
}

// REACHABILITY at a budget: below minspend NO allocation reaches d*, which
// makes the instance unwinnable by any allocator — and therefore an instance
// neither arm can be blamed for losing. That exclusion criterion is half the
// point of the bound.
func TestBoundReachabilityAtABudget(t *testing.T) {
	pol, ws, recs := boundFixture(t)
	decide := stubDecide(new(int))
	full, err := Bound(BoundInput{Policy: pol, Worlds: ws, Receipts: recs, Decide: decide})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	tight, err := Bound(BoundInput{
		Policy: pol, Worlds: ws, Receipts: recs, Decide: decide, BudgetMS: full.MinSpendMS,
	})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	if !tight.Reachable {
		t.Errorf("d* is unreachable at exactly minspend (%d ms)", full.MinSpendMS)
	}
	starved, err := Bound(BoundInput{
		Policy: pol, Worlds: ws, Receipts: recs, Decide: decide, BudgetMS: full.MinSpendMS - 1,
	})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	if starved.Reachable {
		t.Errorf("d* is reachable one millisecond below minspend (%d ms); minspend is not a minimum", full.MinSpendMS)
	}
}

// The caveats travel WITH the number. Each of them bounds what it means, and
// a report that dropped them would be the figure without its footnotes.
func TestBoundCarriesItsCaveats(t *testing.T) {
	pol, ws, recs := boundFixture(t)
	rep, err := Bound(BoundInput{Policy: pol, Worlds: ws, Receipts: recs, Decide: stubDecide(new(int))})
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}
	joined := strings.Join(rep.Caveats, " | ")
	for _, want := range []string{"counterfactual", "cohort-stage", "one draw", "ALLOCATION"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the caveats do not mention %q: %s", want, joined)
		}
	}
}

// A nil decision rule is an error and not a zero. The bound holds a REFERENCE
// to the decision rule, exactly as the scheduler does, and a bound computed
// without one would be a bound on nothing.
func TestBoundRefusesWithoutADecisionRule(t *testing.T) {
	pol, ws, recs := boundFixture(t)
	if _, err := Bound(BoundInput{Policy: pol, Worlds: ws, Receipts: recs}); err == nil {
		t.Fatal("Bound accepted a nil DecideFn")
	}
}
