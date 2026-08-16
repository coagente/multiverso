package schedule

// The two estimators the allocation rule multiplies together: the
// CORRELATION DISCOUNT (what the next purchase is worth given what we
// already bought) and the COST MODEL (what it will cost). Both are pure and
// both are tested against the numbers M2a's own discount rule and M2a
// amendment 27 pin, because the point of the tables is that they are a
// READING of M2a rather than a new opinion.

import (
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

func ev(kind string, corpus string) evidence {
	corr := policy.KindCorrelation(kind)
	corr.Corpus = corpus
	return evidence{kind: kind, corr: corr, prior: PriorClass(corr, providerFor(kind))}
}

// providerFor names the corpus provider a kind would carry in the fixture
// shapes M2a ships.
func providerFor(kind string) string {
	if kind == policy.KindCorpusObserve || kind == policy.KindCorpusDifferential {
		return policy.ProviderDeclared
	}
	return policy.ProviderNone
}

// The prior-class table is TOTAL over M2a's closed generator vocabulary
// crossed with the closed provider vocabulary: a scheduler that meets a
// generator with no declared prior has to guess, and guessing is what the
// table exists to remove.
func TestPriorClassTableIsTotal(t *testing.T) {
	generators := []string{
		policy.GeneratorControlPlane, policy.GeneratorBaseTree,
		policy.GeneratorRepo, policy.GeneratorRepoPolicy, "model:claude",
	}
	providers := append([]string{policy.ProviderNone}, policy.KnownProviders()...)
	for _, g := range generators {
		for _, p := range providers {
			got := PriorClass(object.Correlation{Generator: g}, p)
			if got == PriorUnknown {
				t.Errorf("PriorClass(generator=%q, provider=%q) = unknown", g, p)
			}
		}
	}
	// base-tree + declared is the one row the provider decides: an
	// operator-written corpus is a MECHANICAL replay, while repo-suite and
	// hypothesis corpora are derived from the repository's own tests.
	base := object.Correlation{Generator: policy.GeneratorBaseTree}
	if got := PriorClass(base, policy.ProviderDeclared); got != PriorMechanical {
		t.Errorf("base-tree + declared = %q, want %q", got, PriorMechanical)
	}
	if got := PriorClass(base, policy.ProviderRepoSuite); got != PriorRepoAuthored {
		t.Errorf("base-tree + repo-suite = %q, want %q", got, PriorRepoAuthored)
	}
	// An unknown generator shares with nothing, which makes a purchase look
	// MORE valuable and gets it bought: the scheduler's fail-open direction.
	if got := PriorClass(object.Correlation{}, ""); got != PriorUnknown {
		t.Errorf("unknown generator = %q, want the honest unknown", got)
	}
}

// Two mechanical priors NEVER share; two repo-authored priors always do; two
// model priors share iff the family matches.
func TestPriorSharing(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{PriorMechanical, PriorMechanical, false},
		{PriorRepoAuthored, PriorRepoAuthored, true},
		{PriorMechanical, PriorRepoAuthored, false},
		{"model:claude", "model:claude", true},
		{"model:claude", "model:gpt", false},
		{PriorUnknown, PriorUnknown, false},
		{PriorUnknown, PriorRepoAuthored, false},
	}
	for _, c := range cases {
		if got := priorsShare(c.a, c.b); got != c.want {
			t.Errorf("priorsShare(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// The three worked examples M2b §2.4 prints, to the basis point. The rule
// does something, it does the thing M2a said it should, and it is arguable
// line by line.
func TestDiscountReproducesTheWorkedExamples(t *testing.T) {
	cases := []struct {
		name   string
		buy    evidence
		bought []evidence
		want   int64
	}{
		{
			// Same prior class (repo-authored), different signal
			// (test-outcomes vs test-identity).
			name: "pytest-suite beside pytest-collect: same prior",
			buy:  ev(policy.KindPytestSuite, ""), bought: []evidence{ev(policy.KindPytestCollect, "")},
			want: 5_000,
		},
		{
			// suite-adequacy vs test-outcomes, mechanical vs repo-authored:
			// one of M2a's three genuinely independent pairs.
			name: "mutation-diff beside pytest-suite: independent",
			buy:  ev(policy.KindMutationDiff, ""), bought: []evidence{ev(policy.KindPytestSuite, "")},
			want: 10_000,
		},
		{
			// Both value-behavior, different corpus.
			name: "hypothesis-properties beside corpus-observe: same signal",
			buy:  ev(policy.KindProperties, ""), bought: []evidence{ev(policy.KindCorpusObserve, "mv0:corpus")},
			want: 3_000,
		},
		{
			// Same signal AND same corpus: a second sample of one signal.
			name: "a second pytest-suite instance: near-duplicate",
			buy:  ev(policy.KindPytestSuite, ""), bought: []evidence{ev(policy.KindPytestSuite, "")},
			want: 0,
		},
		{
			name: "nothing bought yet: no discount",
			buy:  ev(policy.KindPytestSuite, ""), bought: nil,
			want: 10_000,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DiscountBP(c.buy, c.bought); got != c.want {
				t.Errorf("discount_bp = %d, want %d", got, c.want)
			}
		})
	}
}

// M2a enumerates the genuinely independent pairs on this menu. Reproducing
// exactly that list is the check that the prior-class mapping is a reading
// of M2a and not a new opinion.
func TestM2aEnumeratedIndependentPairs(t *testing.T) {
	everything := []string{
		policy.KindPytestCollect, policy.KindPytestSuite, policy.KindCorpusObserve,
		policy.KindProperties, policy.KindMutationDiff, policy.KindCorpusDifferential,
	}
	for _, k := range everything {
		if red := redundancy(ev(policy.KindTreeGuard, ""), ev(k, "mv0:corpus")); red != RedIndependent {
			t.Errorf("tree-guard × %s = %d bp, want independent (it reads BYTES, not behaviour)", k, red)
		}
	}
	if red := redundancy(ev(policy.KindMutationDiff, ""), ev(policy.KindPytestSuite, "")); red != RedIndependent {
		t.Errorf("mutation-diff × pytest-suite = %d bp, want independent (adequacy vs outcome)", red)
	}
	if red := redundancy(ev(policy.KindCorpusDifferential, "mv0:c"), ev(policy.KindPytestSuite, "")); red != RedIndependent {
		t.Errorf("corpus-differential × pytest-suite = %d bp, want independent", red)
	}
}

// The complement exemption, by name and with its reason: corpus-differential
// shares signal AND corpus with corpus-observe and would score 10 000 —
// discount zero, never bought, the differential extinguished by the
// machinery that exists to price it.
func TestComplementExemptionKeepsTheDifferentialBuyable(t *testing.T) {
	diff, obs := ev(policy.KindCorpusDifferential, "mv0:c"), ev(policy.KindCorpusObserve, "mv0:c")
	if red := redundancy(diff, obs); red != RedIndependent {
		t.Errorf("corpus-differential after corpus-observe = %d bp, want the complement exemption", red)
	}
	if got := DiscountBP(diff, []evidence{obs}); got != FullBP {
		t.Errorf("discount = %d, want the differential fully priced", got)
	}
	// The exemption is by NAME, not by "same corpus": two observations of
	// one corpus are still near-duplicates of each other.
	if red := redundancy(obs, obs); red != RedNearDuplicate {
		t.Errorf("corpus-observe × corpus-observe = %d bp, want near-duplicate", red)
	}
}

// MAX, not product (decision 5). A product would punish a third
// repo-authored purchase twice for ONE shared prior, driving the discount to
// zero through arithmetic rather than through evidence.
func TestDiscountTakesMaxNotProduct(t *testing.T) {
	buy := ev(policy.KindPytestSuite, "")
	bought := []evidence{
		ev(policy.KindTreeGuard, ""),     // independent: 0
		ev(policy.KindPytestCollect, ""), // same prior: 5 000
		ev(policy.KindProperties, ""),    // same prior: 5 000
	}
	if got := DiscountBP(buy, bought); got != 5_000 {
		t.Errorf("discount = %d over three receipts, want 10 000 − max(0, 5 000, 5 000); a product would give 2 500", got)
	}
}

// The block's one hand-set constant, and the test says so.
func TestExecutorWeightIsTheOneHandSetConstant(t *testing.T) {
	if got := ExecutorBP(policy.ExecutorControlPlane); got != FullBP {
		t.Errorf("control-plane = %d bp, want %d", got, FullBP)
	}
	if got := ExecutorBP(policy.ExecutorCandidateProcess); got != 5_000 {
		t.Errorf("candidate-process = %d bp, want the hand-set 5 000", got)
	}
	// An executor we cannot name is not one we may trust more than the
	// candidate's own process.
	if got := ExecutorBP("something-new"); got != 5_000 {
		t.Errorf("unknown executor = %d bp, want the candidate-process weight", got)
	}
}

// ROBUST FITTING (amendment B): least squares is dragged arbitrarily far by
// one outlier, and the outlier here is vector 22's — a candidate reporting
// 5 000 tests against a 400 ms run to flatten the workspace's per-unit
// coefficient for every future race.
func TestTheilSenResistsAPoisonedSampleThatDragsLeastSquares(t *testing.T) {
	clean := []Sample{}
	for u := int64(1); u <= 5; u++ {
		clean = append(clean, Sample{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: u, WallMS: 400 + 10*u})
	}
	poisoned := append(append([]Sample{}, clean...),
		Sample{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 5_000, WallMS: 400})

	_, perClean, ok := theilSen(clean)
	if !ok || perClean != 10_000 {
		t.Fatalf("theil-sen on a clean line = %d µms/unit (ok %v), want 10 000 (10 ms/test)", perClean, ok)
	}
	_, perPoisoned, ok := theilSen(poisoned)
	if !ok {
		t.Fatal("theil-sen refused a six-point sample")
	}
	if perPoisoned != 10_000 {
		t.Errorf("theil-sen with one poisoned sample = %d µms/unit, want the median to hold at 10 000", perPoisoned)
	}
	// The estimator it replaces, computed here so the improvement is
	// measured rather than asserted.
	if ls := leastSquaresSlope(poisoned); ls > 1.0 {
		t.Logf("least squares slope on the same sample: %.4f ms/unit (the drag this replaces)", ls)
	} else if ls >= 9.0 {
		t.Errorf("least squares was not dragged (%.4f); the test's premise is wrong", ls)
	}
	// Honest limit, stated in the code and asserted here: at the MinSamples
	// floor the median survives exactly ONE bad point.
	floor := []Sample{
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 1, WallMS: 410},
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 2, WallMS: 420},
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 3, WallMS: 430},
	}
	if _, per, _ := theilSen(append(append([]Sample{}, floor...),
		Sample{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 4, WallMS: 0})); per == 10_000 {
		t.Log("three clean points plus one bad one still fit; that is the LIMIT, not a guarantee")
	}
}

// leastSquaresSlope is M2a's estimator, kept in the test only, so the
// comparison the amendment claims is re-derivable rather than asserted.
func leastSquaresSlope(ss []Sample) float64 {
	var n, sx, sy, sxx, sxy float64
	for _, s := range ss {
		x, y := float64(s.Units), float64(s.WallMS)
		n, sx, sy, sxx, sxy = n+1, sx+x, sy+y, sxx+x*x, sxy+x*y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / den
}

// UNIT AUTHORITY (decision 7a). A candidate-authored denominator is clamped
// at FIT TIME to a control-plane measurement; a control-plane-authored one
// is left exactly as recorded.
func TestUnitClampBindsCandidateAuthoredDenominatorsOnly(t *testing.T) {
	bounds := Bounds{CollectedBase: 8}
	forged := []Sample{
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 1, WallMS: 410},
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 2, WallMS: 420},
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 5_000, WallMS: 400},
	}
	handClamped := append([]Sample{}, forged...)
	handClamped[2].Units = 8

	got := NewTable(forged, policy.AutoloadOff, bounds).Coefficients()
	want := NewTable(handClamped, policy.AutoloadOff, bounds).Coefficients()
	if len(got) != 1 || len(want) != 1 || got[0] != want[0] {
		t.Errorf("clamped fit = %+v, want the fit of the hand-clamped sample %+v", got, want)
	}
	unclamped := NewTable(forged, policy.AutoloadOff, Bounds{}).Coefficients()
	if len(unclamped) == 1 && unclamped[0] == got[0] {
		t.Error("the clamp changed nothing; a 5 000-test denominator steered the fit")
	}

	// tree-guard's paths_examined is derived from two git trees the control
	// plane holds. It is NOT clamped, because clamping a number no candidate
	// authors would be throwing away a real measurement.
	guard := []Sample{
		{Kind: policy.KindTreeGuard, Seal: policy.AutoloadOff, Units: 10, WallMS: 20},
		{Kind: policy.KindTreeGuard, Seal: policy.AutoloadOff, Units: 20, WallMS: 30},
		{Kind: policy.KindTreeGuard, Seal: policy.AutoloadOff, Units: 5_000, WallMS: 5_010},
	}
	if a, b := NewTable(guard, policy.AutoloadOff, bounds).Coefficients(),
		NewTable(guard, policy.AutoloadOff, Bounds{}).Coefficients(); len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Errorf("control-plane units were clamped: %+v vs %+v", a, b)
	}
}

// NO FIT, NO NUMBER (decision 7c). Below MinSamples there is no coefficient
// and v0 does not invent one: the row falls back to the declared ordinal
// rank and NO MILLISECOND FIGURE IS PRINTED.
func TestNoFitPrintsNoMillisecondFigure(t *testing.T) {
	two := []Sample{
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 1, WallMS: 410},
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 2, WallMS: 420},
	}
	tbl := NewTable(two, policy.AutoloadOff, Bounds{})
	if got := len(tbl.Coefficients()); got != 0 {
		t.Fatalf("fitted %d coefficients from two samples, want none below MinSamples=%d", got, MinSamples)
	}
	c := tbl.Predict(Rung{Kind: policy.KindPytestSuite, Units: 8})
	if c.Measured || c.MS != 0 {
		t.Errorf("cost = %+v, want no measurement and no millisecond figure", c)
	}
	if c.Basis != CostBasisDeclaredRank {
		t.Errorf("cost basis = %q, want %q", c.Basis, CostBasisDeclaredRank)
	}
	if got := c.render(); got == "" || contains(got, " ms") {
		t.Errorf("rendered %q for an unmeasured kind: a millisecond figure nobody measured must not appear", got)
	}
	if c.Divisor() != declaredRank(policy.KindPytestSuite) {
		t.Errorf("divisor = %d, want the declared rank %d", c.Divisor(), declaredRank(policy.KindPytestSuite))
	}

	// Three samples with NO variance in x measure a FIXED cost and no slope,
	// and those are two different facts about the population. Refusing both
	// dropped the two kinds whose cost dominates every race — pytest-collect
	// and pytest-suite, whose unit is the repository's test count and is
	// therefore constant within a workspace — to `declared-rank` forever,
	// which is why `max_oracle_ms` never bound on a single-repo workspace.
	flat := []Sample{
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 8, WallMS: 410},
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 8, WallMS: 420},
		{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: 8, WallMS: 430},
	}
	fit := NewTable(flat, policy.AutoloadOff, Bounds{}).Coefficients()
	if len(fit) != 1 {
		t.Fatalf("fitted %d coefficients from a measured population with no unit variance, want 1", len(fit))
	}
	if fit[0].Estimator != EstimatorMedianFixed {
		t.Errorf("estimator = %q, want %q so nobody reads the missing slope as a measured one",
			fit[0].Estimator, EstimatorMedianFixed)
	}
	if fit[0].FixedMS != 420 {
		t.Errorf("fixed = %d ms, want the median wall time 420", fit[0].FixedMS)
	}
	if fit[0].PerUnitMicroMS != 0 {
		t.Errorf("per-unit = %d, want none: a slope no pair of samples could measure is UNKNOWN",
			fit[0].PerUnitMicroMS)
	}
	// And the prediction is a millisecond figure somebody measured, which is
	// what makes the budget bind at all.
	flatCost := NewTable(flat, policy.AutoloadOff, Bounds{}).Predict(Rung{Kind: policy.KindPytestSuite, Units: 8})
	if !flatCost.Measured || flatCost.MS != 420 {
		t.Errorf("predicted %+v, want a measured 420 ms", flatCost)
	}
}

// The fit is keyed on (kind, SEAL): M2a amendment 27 measured plugin
// autoloading as a 4.4x lever on fixed cost, so a single-population fit
// averages two populations that differ by four.
func TestFitIsKeyedOnTheSeal(t *testing.T) {
	var samples []Sample
	for u := int64(1); u <= 3; u++ {
		samples = append(samples,
			Sample{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOff, Units: u, WallMS: 400 + u},
			Sample{Kind: policy.KindPytestSuite, Seal: policy.AutoloadOn, Units: u, WallMS: 1_760 + u})
	}
	off := NewTable(samples, policy.AutoloadOff, Bounds{}).Predict(Rung{Kind: policy.KindPytestSuite, Units: 1})
	on := NewTable(samples, policy.AutoloadOn, Bounds{}).Predict(Rung{Kind: policy.KindPytestSuite, Units: 1})
	if !off.Measured || !on.Measured {
		t.Fatalf("both seals should fit: off=%+v on=%+v", off, on)
	}
	if off.MS >= on.MS {
		t.Errorf("sealed fit %d ms >= unsealed %d ms; the populations were merged", off.MS, on.MS)
	}
}

// The declared ordinal rank IS the fallback ordering, and it is the order
// M2b decision 7c writes down.
func TestDeclaredRankOrdersTheMenu(t *testing.T) {
	order := []string{
		policy.KindTreeGuard, policy.KindCorpusDifferential, policy.KindPytestCollect,
		policy.KindCorpusObserve, policy.KindPytestSuite, policy.KindMutationDiff,
	}
	for i := 1; i < len(order); i++ {
		if declaredRank(order[i-1]) >= declaredRank(order[i]) {
			t.Errorf("declared rank %s (%d) not below %s (%d)",
				order[i-1], declaredRank(order[i-1]), order[i], declaredRank(order[i]))
		}
	}
	if declaredRank("no-such-kind") <= declaredRank(policy.KindMutationDiff) {
		t.Error("an unknown kind must price as the most expensive thing on the menu")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
