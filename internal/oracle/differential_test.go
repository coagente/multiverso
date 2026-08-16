package oracle

// M2a: the cross-candidate differential reducer.
//
// Everything here is PURE — recorded corpora and recorded observation
// streams, no Python, no world, no backend, no clock. That is the property
// decision 1 bought and it is asserted directly: a fake world whose every
// method fails is handed to nothing, because the reducer takes no world at
// all, and the type system says so.

import (
	"encoding/json"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

const (
	worldA = "mv0:aa11111111111111111111111111111111111111111111111111111111111111"
	worldB = "mv0:bb22222222222222222222222222222222222222222222222222222222222222"
	worldC = "mv0:cc33333333333333333333333333333333333333333333333333333333333333"
)

// diffSpec is the declared reducer instance the fixtures' receipts name.
var diffSpec = policy.Oracle{
	Name: "diff", Kind: policy.KindCorpusDifferential,
	Family: policy.FamilyBehavior, Config: "mv0:" + "cfgdiff",
}

func member(t *testing.T, world, fixture string, corpus Corpus) CohortMember {
	t.Helper()
	obs := ParseObservation(corpusFixture(t, fixture), obsNonce, false, corpus, fixtureCorpusDigest)
	if !obs.Usable {
		t.Fatalf("fixture %s is unusable: %s", fixture, obs.Reason)
	}
	return CohortMember{World: world, Obs: obs}
}

func baseObservation(t *testing.T, corpus Corpus) Observation {
	t.Helper()
	obs := ParseObservation(corpusFixture(t, "obs-base.txt"), obsNonce, false, corpus, fixtureCorpusDigest)
	if !obs.Usable {
		t.Fatalf("base fixture is unusable: %s", obs.Reason)
	}
	return obs
}

func reduce(t *testing.T, corpus Corpus, base Observation, members ...CohortMember) DifferentialResult {
	t.Helper()
	res, err := Reduce(DifferentialInputs{
		Corpus:          corpus,
		CorpusDigest:    "mv0:3ab" + "0000000000000000000000000000000000000000000000000000000000",
		BaseTree:        "git:9c1f2a3b4c5d6e7f8091a2b3c4d5e6f708192a3b",
		BaseObservation: "sha256:base",
		Base:            base,
		Members:         members,
		Spec:            diffSpec,
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	return res
}

// THE BLOCK'S CLAIM, under test: two candidates that agree on every case
// the eight-test suite exercises and disagree on one uncovered input land
// in DIFFERENT behaviour classes, and the report names the input and both
// answers.
func TestReducePartitionsARealSplit(t *testing.T) {
	corpus := declaredCorpus(t)
	base := baseObservation(t, corpus)
	res := reduce(t, corpus,
		base,
		member(t, worldA, "obs-agree-a.txt", corpus),
		member(t, worldB, "obs-split.txt", corpus),
	)
	if res.Classes != 2 {
		t.Fatalf("classes = %d, want 2", res.Classes)
	}
	if res.Compared != 4 {
		t.Errorf("compared = %d, want every declared case", res.Compared)
	}
	if res.FirstDistinguishing != "c0001" {
		t.Errorf("first distinguishing case = %q, want c0001 (clamp(nan, 0, 10))", res.FirstDistinguishing)
	}
	if len(res.Receipts) != 2 {
		t.Fatalf("receipts = %d, want ONE PER COHORT MEMBER", len(res.Receipts))
	}

	var report differentialReport
	if err := json.Unmarshal(res.Report, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Schema != SchemaDifferentialReport {
		t.Errorf("report schema = %q", report.Schema)
	}
	if len(report.Distinguishing) != 1 {
		t.Fatalf("distinguishing = %d cases, want 1", len(report.Distinguishing))
	}
	d := report.Distinguishing[0]
	if d.Case != "c0001" || d.Target != "stats:clamp" {
		t.Errorf("distinguishing case = %s on %s, want c0001 on stats:clamp", d.Case, d.Target)
	}
	// The payload a maintainer acts on: the INPUT and what each candidate
	// returned on it.
	if got := RenderValue(d.Args[0]); got != "nan" {
		t.Errorf("the distinguishing input renders as %q, want nan", got)
	}
	if len(d.Observations) != 2 {
		t.Fatalf("observations = %d, want one per world", len(d.Observations))
	}
	answers := map[string]string{}
	for _, o := range d.Observations {
		answers[o.World] = RenderValue(o.Value)
	}
	if answers[worldA] != "nan" || answers[worldB] != "0" {
		t.Errorf("answers = %v, want %s → nan and %s → 0", answers, worldA, worldB)
	}
	if RenderValue(d.Base.Value) != "nan" {
		t.Errorf("base answer = %q, want nan", RenderValue(d.Base.Value))
	}
	// Exactly one class agrees with base on every compared case: base's
	// mean is still buggy, so NEITHER candidate agrees with it — and that
	// is the point of decision 7. "Agrees with base" is not a virtue.
	for _, c := range report.Classes {
		if c.AgreesWithBase {
			t.Errorf("class %s claims to agree with base, but base's mean differs from both candidates'", c.ID)
		}
	}
}

// The per-world metrics of a split cohort, in full.
func TestReduceMetricsOfASplitCohort(t *testing.T) {
	corpus := declaredCorpus(t)
	res := reduce(t, corpus, baseObservation(t, corpus),
		member(t, worldA, "obs-agree-a.txt", corpus),
		member(t, worldB, "obs-agree-b.txt", corpus),
		member(t, worldC, "obs-split.txt", corpus),
	)
	byWorld := map[string]map[string]int64{}
	for _, rec := range res.Receipts {
		byWorld[rec.World] = rec.Result.Metrics
	}
	// A and B are one class of two; C is a singleton.
	for _, tc := range []struct {
		world string
		want  map[string]int64
	}{
		{worldA, map[string]int64{
			policy.MetricDiffCohortN: 3, policy.MetricDiffClasses: 2, policy.MetricDiffClassSize: 2,
			policy.MetricDiffCasesCompared: 4, policy.MetricDiffCasesIncomparable: 0,
			policy.MetricDiffCasesDivergent: 1, policy.MetricDiffCasesUnilateral: 0,
			// mean differs from base's buggy mean on c0003; clamp(nan)
			// agrees with base. So one case differs from base, and it is
			// not unilateral because B differs from base there too.
			policy.MetricDiffCasesVsBase: 1, policy.MetricDiffCasesUnilateralVBase: 0,
		}},
		{worldC, map[string]int64{
			policy.MetricDiffCohortN: 3, policy.MetricDiffClasses: 2, policy.MetricDiffClassSize: 1,
			policy.MetricDiffCasesCompared: 4, policy.MetricDiffCasesIncomparable: 0,
			// C differs from both siblings on c0001 and is alone there.
			policy.MetricDiffCasesDivergent: 1, policy.MetricDiffCasesUnilateral: 1,
			policy.MetricDiffCasesVsBase: 2, policy.MetricDiffCasesUnilateralVBase: 1,
		}},
	} {
		got := byWorld[tc.world]
		if got == nil {
			t.Fatalf("no receipt for %s", tc.world)
		}
		for name, want := range tc.want {
			if got[name] != want {
				t.Errorf("%s: %s = %d, want %d", tc.world[:8], name, got[name], want)
			}
		}
	}
}

// A COMPARISON OF ONE IS NOT A COMPARISON. diff_cohort_n is present so a
// reader can see WHY the others are missing; every other diff_* metric is
// ABSENT, never a fabricated zero.
func TestReduceCohortOfOneEmitsOnlyTheCount(t *testing.T) {
	corpus := declaredCorpus(t)
	res := reduce(t, corpus, baseObservation(t, corpus), member(t, worldA, "obs-agree-a.txt", corpus))
	if len(res.Receipts) != 1 {
		t.Fatalf("receipts = %d, want 1", len(res.Receipts))
	}
	m := res.Receipts[0].Result.Metrics
	if m[policy.MetricDiffCohortN] != 1 {
		t.Errorf("diff_cohort_n = %d, want 1", m[policy.MetricDiffCohortN])
	}
	if len(m) != 1 {
		t.Errorf("metrics = %v, want ONLY diff_cohort_n: absence is the record, not zero", m)
	}
	for _, name := range policy.KindMetrics(policy.KindCorpusDifferential) {
		if name == policy.MetricDiffCohortN {
			continue
		}
		if _, present := m[name]; present {
			t.Errorf("%s is present in a cohort of one", name)
		}
	}
}

// An empty cohort produces no receipts at all: a receipt binds a world, and
// there is no world to bind to.
func TestReduceEmptyCohort(t *testing.T) {
	corpus := declaredCorpus(t)
	res := reduce(t, corpus, baseObservation(t, corpus))
	if len(res.Receipts) != 0 {
		t.Errorf("receipts = %d over an empty cohort, want none", len(res.Receipts))
	}
}

// An opaque observation is excluded from the DENOMINATOR and counted.
// Absence of comparability is not agreement.
func TestReduceExcludesOpaqueCasesFromTheDenominator(t *testing.T) {
	corpus := declaredCorpus(t)
	res := reduce(t, corpus, baseObservation(t, corpus),
		member(t, worldA, "obs-agree-a.txt", corpus),
		member(t, worldB, "obs-opaque.txt", corpus),
	)
	m := res.Receipts[0].Result.Metrics
	if m[policy.MetricDiffCasesCompared] != 3 {
		t.Errorf("compared = %d, want 3 (the opaque case is not comparable)", m[policy.MetricDiffCasesCompared])
	}
	if m[policy.MetricDiffCasesIncomparable] != 1 {
		t.Errorf("incomparable = %d, want 1", m[policy.MetricDiffCasesIncomparable])
	}
	// The two worlds agree on every case that COULD be compared, so the
	// partition has one class — and the opaque case did not manufacture
	// agreement, it was removed from the question.
	if res.Classes != 1 {
		t.Errorf("classes = %d, want 1", res.Classes)
	}
}

// PERMUTATION INVARIANCE. The orchestrator's workers finish in whatever
// order the machine gives them, and a decision that depended on that order
// would not replay. Extended from M1c's property test.
func TestReduceIsPermutationInvariant(t *testing.T) {
	corpus := declaredCorpus(t)
	base := baseObservation(t, corpus)
	a := member(t, worldA, "obs-agree-a.txt", corpus)
	b := member(t, worldB, "obs-split.txt", corpus)
	c := member(t, worldC, "obs-agree-b.txt", corpus)
	orders := [][]CohortMember{
		{a, b, c}, {c, b, a}, {b, a, c}, {b, c, a}, {a, c, b}, {c, a, b},
	}
	var want DifferentialResult
	for i, order := range orders {
		got := reduce(t, corpus, base, order...)
		if i == 0 {
			want = got
			continue
		}
		if string(got.Report) != string(want.Report) {
			t.Fatalf("order %d produced different report bytes", i)
		}
		if got.CohortDigest != want.CohortDigest || got.Classes != want.Classes ||
			got.FirstDistinguishing != want.FirstDistinguishing {
			t.Fatalf("order %d produced a different summary", i)
		}
		if len(got.Receipts) != len(want.Receipts) {
			t.Fatalf("order %d produced %d receipts, want %d", i, len(got.Receipts), len(want.Receipts))
		}
		for j := range got.Receipts {
			gd, _, err := object.Digest(got.Receipts[j])
			if err != nil {
				t.Fatal(err)
			}
			wd, _, _ := object.Digest(want.Receipts[j])
			if gd != wd {
				t.Fatalf("order %d receipt %d digests to %s, want %s", i, j, gd, wd)
			}
		}
	}
	// A repeated call over the SAME order is byte-identical too: the
	// reducer reads no clock, so nothing in it can vary per call.
	again := reduce(t, corpus, base, orders[0]...)
	if string(again.Report) != string(want.Report) {
		t.Error("two calls over the same inputs produced different report bytes")
	}
	if again.Receipts[0].CreatedAt != "" {
		t.Error("the reducer stamped a timestamp; it must leave CreatedAt for the recorder, as Decide does")
	}
}

// The regime label, which is the whole of decision 3. `control-plane` would
// be the flattering answer and it would be an over-claim: every byte the
// reducer consumed was produced by a streamed run inside a candidate's
// process, and the receipt says so from the receipt alone.
func TestDifferentialReceiptNamesItsEvidenceFloor(t *testing.T) {
	corpus := declaredCorpus(t)
	res := reduce(t, corpus, baseObservation(t, corpus),
		member(t, worldA, "obs-agree-a.txt", corpus),
		member(t, worldB, "obs-split.txt", corpus),
	)
	for _, rec := range res.Receipts {
		if rec.Execution.EvidenceRegime != object.RegimeDerived {
			t.Errorf("evidence_regime = %q, want %q", rec.Execution.EvidenceRegime, object.RegimeDerived)
		}
		if rec.Inputs[object.InputEvidenceFloor] != object.RegimeStreamed {
			t.Errorf("inputs[evidence_floor] = %q, want %q — the comparison is arithmetic over candidate-influenced observations",
				rec.Inputs[object.InputEvidenceFloor], object.RegimeStreamed)
		}
		if len(rec.Execution.Argv) != 0 {
			t.Errorf("argv = %v, want empty: the reducer runs no process", rec.Execution.Argv)
		}
		if rec.Execution.ExitCode != 0 || rec.Execution.EvidencePlugin != "" {
			t.Error("a derived receipt must carry no exit code verdict and name no observer of its own")
		}
		if rec.Family != policy.FamilyBehavior {
			t.Errorf("family = %q, want %q", rec.Family, policy.FamilyBehavior)
		}
		if rec.Cost.Unit != policy.UnitWorldCases || rec.Cost.Units != 2*4 {
			t.Errorf("cost = %+v, want 8 world-cases", rec.Cost)
		}
		// The cohort's identity travels in the RECEIPT, never in the
		// selector's config digest (decision 2): a policy cannot know at
		// authoring time which worlds a future race will produce, and
		// putting the cohort in the config would make the gate's selector
		// unmatchable forever.
		if rec.Inputs[object.InputCohort] != res.CohortDigest {
			t.Errorf("inputs[cohort] = %q, want %q", rec.Inputs[object.InputCohort], res.CohortDigest)
		}
		if rec.Oracle.Config != diffSpec.Config {
			t.Errorf("oracle.config = %q, want the declared instance's %q", rec.Oracle.Config, diffSpec.Config)
		}
		// One artifact, N referrers.
		if len(rec.Result.Artifacts) != 1 || rec.Result.Artifacts[0] != res.ReportKey {
			t.Errorf("artifacts = %v, want exactly the report %s", rec.Result.Artifacts, res.ReportKey)
		}
	}
}

// The report's distinguishing list is CAPPED and the overflow COUNTED: the
// report is the ESCALATE payload, and an unbounded one would be a
// candidate-driven denial of a maintainer's attention.
func TestReportTruncatesDistinguishingCases(t *testing.T) {
	n := maxDistinguishing + 7
	corpus := Corpus{Schema: SchemaCorpus, Provider: policy.ProviderDeclared, Dropped: map[string]int64{}}
	baseCases := map[string]CaseObservation{}
	aCases := map[string]CaseObservation{}
	bCases := map[string]CaseObservation{}
	for i := 0; i < n; i++ {
		id := caseID(i + 1)
		corpus.Cases = append(corpus.Cases, CorpusCase{
			ID: id, Target: "m:f", Args: []json.RawMessage{}, Kwargs: map[string]json.RawMessage{},
		})
		baseCases[id] = CaseObservation{ID: id, Outcome: OutcomeValue, FP: "sha256:base"}
		aCases[id] = CaseObservation{ID: id, Outcome: OutcomeValue, FP: "sha256:aaa"}
		bCases[id] = CaseObservation{ID: id, Outcome: OutcomeValue, FP: "sha256:bbb"}
	}
	res, err := Reduce(DifferentialInputs{
		Corpus: corpus, CorpusDigest: "mv0:corpus", BaseTree: "git:base",
		Base: Observation{Usable: true, Cases: baseCases},
		Members: []CohortMember{
			{World: worldA, Obs: Observation{Usable: true, Cases: aCases}},
			{World: worldB, Obs: Observation{Usable: true, Cases: bCases}},
		},
		Spec: diffSpec,
	})
	if err != nil {
		t.Fatal(err)
	}
	var report differentialReport
	if err := json.Unmarshal(res.Report, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Distinguishing) != maxDistinguishing {
		t.Errorf("distinguishing = %d, want the %d cap", len(report.Distinguishing), maxDistinguishing)
	}
	if report.DistinguishingTruncated != 7 {
		t.Errorf("truncated = %d, want the 7 that did not fit COUNTED", report.DistinguishingTruncated)
	}
	// The cap keeps the smallest ids, so the escalation's "first
	// distinguishing case" is always in the list an operator can read.
	if report.Distinguishing[0].Case != "c0001" {
		t.Errorf("first reported case = %s, want the smallest id", report.Distinguishing[0].Case)
	}
}

func caseID(n int) string {
	const digits = "0123456789"
	out := []byte("c0000")
	for i := 4; i >= 1 && n > 0; i-- {
		out[i] = digits[n%10]
		n /= 10
	}
	return string(out)
}

// The reducer takes no world and opens nothing. Stated as a compile-time
// fact rather than a runtime one: DifferentialInputs has no backend.World
// field, so there is no handle for a hostile world to be passed through,
// and `oracle.New` refuses to build a runnable instance of the kind at all.
func TestDifferentialIsNotARunnableOracle(t *testing.T) {
	_, err := New(Params{
		Spec: policy.Oracle{Name: "diff", Kind: policy.KindCorpusDifferential, Config: "mv0:cfg"},
		CAS:  nil,
	})
	if err == nil {
		t.Fatal("New built a runnable corpus-differential; the reducer runs at the cohort barrier, not per world")
	}
}
