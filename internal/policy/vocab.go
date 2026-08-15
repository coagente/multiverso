package policy

import (
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
)

// Gate predicates (CP-5). The vocabulary is CLOSED: an unknown gate name in
// a v1 policy is a load error, never a runtime surprise (M1e decision 7).
// suite-pass exists only in the v0 dialect, where it is M0's single gate.
const (
	GateStatusPass        = "status-pass"
	GateCollectNonempty   = "collect-nonempty"
	GateCollectedNotBelow = "collected-not-below"
	GateNoFailedTests     = "no-failed-tests"
	GateCoverageAtLeast   = "coverage-at-least"
	GateSuitePass         = "suite-pass" // v0 dialect only
)

// Ranking keys. gate_pass is an implicit FIRST key and world_digest_asc an
// implicit TERMINAL key of every compiled policy (M1e decision 4): the
// winner therefore provably passes every hard gate, and the order is total.
const (
	KeyGatePass        = "gate_pass"
	KeyTestsPassedDesc = "tests_passed_desc"
	KeyCoverageDesc    = "coverage_desc"
	KeyWallMSAsc       = "wall_ms_asc"
	KeyCostAsc         = "cost_asc"
	KeyPatchSizeAsc    = "patch_size_asc"
	KeyWorldDigestAsc  = "world_digest_asc"
)

// Metric names (Receipt.Result.Metrics keys, EP-2). Integers only.
const (
	MetricCollectedTotal      = "collected_total"
	MetricCollectedBase       = "collected_base"
	MetricCollectedDelta      = "collected_delta"
	MetricTestsTotal          = "tests_total"
	MetricTestsPassed         = "tests_passed"
	MetricTestsFailed         = "tests_failed"
	MetricTestsErrored        = "tests_errored"
	MetricTestsSkipped        = "tests_skipped"
	MetricDurationMS          = "duration_ms"
	MetricCoverageBP          = "coverage_bp"
	MetricTestsFailedFirstRun = "tests_failed_first_run"
	MetricTestsPassedAfterRun = "tests_passed_after_rerun"
)

// Oracle kinds (the registry's closed key space) and the correlation
// families their receipts carry.
const (
	KindCommand       = "command"
	KindPytestCollect = "pytest-collect"
	KindPytestSuite   = "pytest-suite"

	FamilySuite   = "suite"
	FamilyCollect = "collect"
)

// DefaultPytestPrefix is the runner prefix a pytest-kind oracle resolves to
// when its spec leaves argv empty.
func DefaultPytestPrefix() []string { return []string{"python3", "-m", "pytest"} }

// gateDef declares what a gate predicate reads: the metrics it needs (in
// the order they appear in its fail reason) and whether its threshold
// carries meaning. It is the validator's source of truth for rule 5 — a
// coverage-at-least gate on a pytest-collect oracle is refused at load, not
// at 3 a.m.
type gateDef struct {
	metrics   []string
	threshold bool
}

var gateDefs = map[string]gateDef{
	GateStatusPass:        {},
	GateCollectNonempty:   {metrics: []string{MetricCollectedTotal}},
	GateCollectedNotBelow: {metrics: []string{MetricCollectedDelta}, threshold: true},
	GateNoFailedTests:     {metrics: []string{MetricTestsFailed, MetricTestsErrored}},
	GateCoverageAtLeast:   {metrics: []string{MetricCoverageBP}, threshold: true},
}

// keyDef declares a ranking key's direction and, for metric-bearing keys,
// the metric it reads. Metric keys resolve to exactly one declared oracle
// at validation time.
type keyDef struct {
	desc   bool
	metric string
}

var keyDefs = map[string]keyDef{
	KeyGatePass:        {desc: true},
	KeyTestsPassedDesc: {desc: true, metric: MetricTestsPassed},
	KeyCoverageDesc:    {desc: true, metric: MetricCoverageBP},
	KeyWallMSAsc:       {},
	KeyCostAsc:         {},
	KeyPatchSizeAsc:    {},
	KeyWorldDigestAsc:  {},
}

// kindDef is one registry kind: the family its receipts carry and the
// metric vocabulary its implementation may emit. A conformance test in
// internal/oracle asserts each implementation emits a subset of its
// declared set (a source that was unavailable yields ABSENCE, never zero).
type kindDef struct {
	family  string
	metrics []string
}

var kindDefs = map[string]kindDef{
	KindCommand:       {family: FamilySuite},
	KindPytestCollect: {family: FamilyCollect, metrics: []string{MetricCollectedTotal, MetricCollectedBase, MetricCollectedDelta}},
	KindPytestSuite: {family: FamilySuite, metrics: []string{
		MetricTestsTotal, MetricTestsPassed, MetricTestsFailed, MetricTestsErrored,
		MetricTestsSkipped, MetricDurationMS, MetricCoverageBP,
		MetricTestsFailedFirstRun, MetricTestsPassedAfterRun,
	}},
}

// KnownGates, KnownKeys, KnownKinds and KnownBases return the closed
// vocabularies, sorted — the "(known: …)" tails of load errors, so an
// authoring mistake always prints the whole alternative set.
func KnownGates() []string { return sortedKeys(gateDefs) }

// KnownKeys returns every ranking key name, sorted.
func KnownKeys() []string { return sortedKeys(keyDefs) }

// KnownKinds returns every oracle kind, sorted.
func KnownKinds() []string { return sortedKeys(kindDefs) }

// KnownBases returns the three freshness bases, strongest first.
func KnownBases() []string {
	return []string{object.BasisConstruction, object.BasisDependency, object.BasisProbabilistic}
}

// KindFamily returns the correlation family a kind's receipts carry.
func KindFamily(kind string) string { return kindDefs[kind].family }

// KindMetrics returns the metric vocabulary of a kind, sorted.
func KindMetrics(kind string) []string {
	out := append([]string(nil), kindDefs[kind].metrics...)
	sort.Strings(out)
	return out
}

// SpecEmits reports whether one DECLARED oracle instance can emit metric.
// A kind's vocabulary says what its implementation MAY emit; an instance's
// own configuration decides which of those it ever WILL. coverage_bp is the
// case that matters: pytest-suite measures coverage only when the instance
// asks for it and its runner prefix is one coverage.py can drive, so a
// coverage-at-least gate (or a coverage_desc key) against a
// coverage-disabled oracle is refused at load rather than failing forever
// on "coverage_bp absent (source unavailable)" — the same authoring bug
// class as a coverage gate on a pytest-collect oracle, caught in the same
// place.
func SpecEmits(spec object.OracleSpec, metric string) bool {
	if !emits(spec.Kind, metric) {
		return false
	}
	if metric == MetricCoverageBP {
		return spec.Coverage && CoverageWrappable(ResolvedArgv(spec))
	}
	return true
}

// CoverageWrappable reports whether a runner prefix has the `<python> -m
// <module>` shape coverage.py can drive. Any other prefix (a bare `pytest`
// script, a wrapper) is left alone by the suite oracle: coverage stays
// unmeasured and says so, which beats mangling the command that produces
// the verdict. Validation and the oracle read this ONE predicate, so what
// the validator promises and what the implementation does cannot drift.
func CoverageWrappable(prefix []string) bool {
	return len(prefix) >= 3 && prefix[1] == "-m"
}

// BasisRank ranks freshness bases: construction (3) > dependency (2) >
// probabilistic (1). An unrecognized basis on a RECEIPT ranks 0 and
// satisfies nothing (M1e decision 11); on a policy it is a load error.
func BasisRank(basis string) int {
	switch basis {
	case object.BasisConstruction:
		return 3
	case object.BasisDependency:
		return 2
	case object.BasisProbabilistic:
		return 1
	default:
		return 0
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func known(list []string) string { return strings.Join(list, ", ") }
