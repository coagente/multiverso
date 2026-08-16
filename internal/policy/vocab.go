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
	// M1f. paths-unmodified is the one gate whose evidence the adversary
	// provably cannot author: it compares tree blob hashes the control
	// plane holds. skips-not-above exists because tests_skipped was
	// recorded in every suite receipt since M1e and no gate in the closed
	// vocabulary could read it — the study's third vector, unreadable.
	GatePathsUnmodified = "paths-unmodified"
	GateSkipsNotAbove   = "skips-not-above"
	// M2a. corpus-complete is the observation's own gate: a world that did
	// not produce a record for every declared case has told us it is not
	// running our corpus, and its metrics are absent rather than partial.
	// differential-cohort-at-least is the ONE cohort gate, and it is
	// documented as an operator choice with a stated cost — with N=2 a
	// single silenced world denies the comparison to the whole race
	// (corpus vector 18), which is why the shipped fixture escalates
	// instead of gating.
	GateCorpusComplete            = "corpus-complete"
	GateDifferentialCohortAtLeast = "differential-cohort-at-least"
	// M2a O2p/O3. properties-pass is the property rung's verdict gate.
	// property-cases-at-least reads the honest-degradation metric PBT
	// usually hides: a property whose assume() filters rejected almost
	// every draw has SEARCHED NOTHING while reporting a pass. It is
	// correctly signed because the case budget is fixed by the corpus
	// config and identical across worlds, so a low count means the search
	// collapsed, not that the candidate is small.
	//
	// mutation-survivors-not-above is the ONLY mutation gate (decision
	// 10). A survivor is a concrete, actionable artifact: a mutant of a
	// line THE CANDIDATE WROTE that no test killed. The absolute count is
	// correctly signed under diff-scoping — a larger diff makes the gate
	// strictly harder — and it is immune to the padding attack (corpus
	// vector 14), because padding adds mutants that may themselves
	// survive. mutation_score_bp is a ratio over a denominator the
	// candidate chooses: recorded, rendered, and gated by nothing.
	GatePropertiesPass            = "properties-pass"
	GatePropertyCasesAtLeast      = "property-cases-at-least"
	GateMutationSurvivorsNotAbove = "mutation-survivors-not-above"
)

// Gate scope (M2a decision 21). "" ⇒ ScopeBoth, which reproduces M1e/M1f
// semantics exactly. A gate over a COHORT-stage kind must declare
// ScopeRace: `mvo admit` has one subject, so a cohort of one is not a
// comparison, and a differential gate there would fail forever on absent
// metrics — the sealed.json failure M1f's red team found, rebuilt.
const (
	ScopeBoth    = "both"
	ScopeRace    = "race"
	ScopeLanding = "landing"
)

// KnownScopes returns the legal gate scopes, sorted.
func KnownScopes() []string { return []string{ScopeBoth, ScopeLanding, ScopeRace} }

// Corpus providers (M2a decision 5). The set is CLOSED.
//
//   - ProviderDeclared: the policy names a JSON corpus file. Zero
//     dependencies, fully deterministic, and the honest answer for a
//     repository that has neither Hypothesis nor an appetite for one.
//   - ProviderRepoSuite: cases are the base tree's own test node ids. It is
//     documented as the honest FLOOR and as nearly information-free — among
//     candidates that passed the suite gate every world's outcome vector is
//     identical, so the partition has one class and the differential says
//     "no divergence", which is true and useless.
//   - ProviderHypothesis: materialized on the base tree from the repo's
//     @given tests plus the policy-declared property module.
const (
	ProviderNone       = ""
	ProviderRepoSuite  = "repo-suite"
	ProviderDeclared   = "declared"
	ProviderHypothesis = "hypothesis"
)

// KnownProviders returns the legal corpus.provider values, sorted.
func KnownProviders() []string {
	return []string{ProviderDeclared, ProviderHypothesis, ProviderRepoSuite}
}

// DefaultCasesMax is the materialization ceiling a corpus spec leaving
// cases_max at 0 resolves to.
const DefaultCasesMax = 100

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

	// M1f tree-guard metrics. Every one of them is derived from two git
	// trees the control plane holds: no candidate authors these numbers in
	// any regime (the metric provenance table).
	MetricProtectedModified = "protected_modified"
	MetricProtectedDeleted  = "protected_deleted"
	MetricProtectedAdded    = "protected_added"
	MetricHarnessModified   = "harness_modified"
	MetricHarnessDeleted    = "harness_deleted"
	MetricHarnessAdded      = "harness_added"
	MetricPathsExamined     = "paths_examined"

	// M2a corpus-observe metrics. corpus_cases_total comes from the CORPUS
	// OBJECT, materialized on the base tree before any candidate existed:
	// no candidate authors that number in any regime. The other three are
	// stream-derived and carry exactly the streamed regime's guarantees —
	// they cannot be suppressed into a pass (S1), cannot be authored after
	// exit, and can be forged by an adversary already executing in the
	// process.
	MetricCorpusCasesTotal    = "corpus_cases_total"
	MetricCorpusCasesObserved = "corpus_cases_observed"
	MetricCorpusCasesOpaque   = "corpus_cases_opaque"
	MetricCorpusCasesErrored  = "corpus_cases_errored"

	// M2a corpus-differential metrics: this world's POSITION IN THE
	// COMPARISON, computed by the control-plane reducer over the cohort's
	// observations. diff_cohort_n < 2 ⇒ every other diff_* metric is
	// ABSENT: a comparison of one is not a comparison, and diff_cohort_n
	// itself is recorded so a reader can see WHY the others are missing.
	//
	// diff_cases_vs_base and diff_cases_unilateral_vs_base are RECORDED
	// AND NOT CONSUMED. "Agrees with base" is indistinguishable from "did
	// nothing" without a fail-to-pass reproduction test, so a ranking key
	// over them would rank the candidate that changed nothing above the
	// honest fix (M2a decision 7). M2b's evaluation needs the numbers to
	// correlate against ground truth before anyone builds on them.
	MetricDiffCohortN              = "diff_cohort_n"
	MetricDiffClasses              = "diff_classes"
	MetricDiffClassSize            = "diff_class_size"
	MetricDiffCasesCompared        = "diff_cases_compared"
	MetricDiffCasesIncomparable    = "diff_cases_incomparable"
	MetricDiffCasesDivergent       = "diff_cases_divergent"
	MetricDiffCasesUnilateral      = "diff_cases_unilateral"
	MetricDiffCasesVsBase          = "diff_cases_vs_base"
	MetricDiffCasesUnilateralVBase = "diff_cases_unilateral_vs_base"

	// M2a hypothesis-properties (O2p) metrics. The four properties_*
	// counts are stream-derived and carry the streamed regime's
	// guarantees. property_cases_* are emitted ONLY when the per-case
	// records reached the control-plane stream through the observability
	// callback (decision 15): under the JSONL fallback the records are
	// candidate-authorable AFTER EXIT — coverage_bp's status, and
	// unacceptable for a number a gate reads — so the JSONL is stored as
	// an artifact, result.tools says so, and THE METRICS ARE ABSENT. One
	// metric name, one provenance, forever: a metric whose
	// trustworthiness varied silently by code path would be worse than no
	// metric.
	MetricPropertiesTotal      = "properties_total"
	MetricPropertiesPassed     = "properties_passed"
	MetricPropertiesFailed     = "properties_failed"
	MetricPropertiesErrored    = "properties_errored"
	MetricPropertyCasesTotal   = "property_cases_total"
	MetricPropertyCasesInvalid = "property_cases_invalid"

	// M2a mutation-diff (O3) metrics. mutation_lines_targeted is derived
	// by the CONTROL PLANE from the AG-4 captured patch: the candidate
	// chose the content but cannot change what the content IS, so this
	// one is not candidate-authorable in any regime. The rest are
	// stream-derived per mutant run.
	//
	// mutants_budget / mutants_candidates / mutants_tested are all three
	// recorded (decision 11) so a reader can see HOW PARTIAL a partial
	// score is without consulting the policy. Timeouts and unviable
	// mutants are excluded from mutation_score_bp's denominator and
	// counted separately, because "the mutant hung" and "the mutant did
	// not import" are not "the tests caught it".
	MetricMutationLinesTargeted = "mutation_lines_targeted"
	MetricMutantsBudget         = "mutants_budget"
	MetricMutantsCandidates     = "mutants_candidates"
	MetricMutantsTested         = "mutants_tested"
	MetricMutantsKilled         = "mutants_killed"
	MetricMutantsSurvived       = "mutants_survived"
	MetricMutantsTimeout        = "mutants_timeout"
	MetricMutantsUnviable       = "mutants_unviable"
	MetricMutationScoreBP       = "mutation_score_bp"
)

// Oracle kinds (the registry's closed key space) and the correlation
// families their receipts carry.
const (
	KindCommand       = "command"
	KindPytestCollect = "pytest-collect"
	KindPytestSuite   = "pytest-suite"
	// KindTreeGuard is a registry kind, not a special case in Decide (M1f
	// decision 8): its receipt carries violation counts as metrics and the
	// violation list as a CAS artifact, so ladder ordering, the explain
	// table, the ESCALATE payload, admission re-gating, replay and
	// publication all work for free.
	KindTreeGuard = "tree-guard"
	// M2a's cross-candidate differential is TWO kinds, not one, and that
	// is decision 1: corpus-observe is an ordinary per-world oracle bound
	// to exactly the world it observed, and corpus-differential is a pure
	// control-plane reducer that emits ONE COMPARISON RECEIPT PER WORLD.
	// A single N-world oracle would have had to make Receipt.World and
	// Freshness.ValidFor plural, and a receipt whose valid_for names one
	// tree while its metrics derive from N trees is lying about what it
	// judged — M1f's "the label doing the laundering".
	KindCorpusObserve      = "corpus-observe"
	KindCorpusDifferential = "corpus-differential"
	// KindProperties is O2p: the repository's own Hypothesis tests plus a
	// policy-declared property module, run through pytest under the
	// control-plane observer. KindMutationDiff is O3: Google's recipe —
	// mutate only the diff, cap the budget, and never run a full mutation
	// pass (ch. 8 §8.1: exhaustive mutation "does not scale").
	KindProperties   = "hypothesis-properties"
	KindMutationDiff = "mutation-diff"

	FamilySuite   = "suite"
	FamilyCollect = "collect"
	FamilyTree    = "tree"
	// FamilyBehavior is shared by both differential kinds: family is the
	// v0 dialect's evidence selector and is FROZEN in meaning (M2a
	// decision 23), so the new correlation descriptor — not the family —
	// carries the scheduler's view of what a receipt reads.
	FamilyBehavior = "behavior"
	// FamilyProperty and FamilyMutation are the two rungs M2a adds beside
	// the differential. They are distinct families because family is the
	// v0 dialect's evidence selector: a property receipt must never be
	// selectable by a v0 suite gate that has no idea what it is reading.
	FamilyProperty = "property"
	FamilyMutation = "mutation"
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
	GatePathsUnmodified: {metrics: []string{
		MetricProtectedModified, MetricProtectedDeleted, MetricProtectedAdded,
		MetricHarnessModified, MetricHarnessDeleted, MetricHarnessAdded,
		MetricPathsExamined,
	}},
	// skips-not-above TAKES a parameter, so threshold == 0 is legal and
	// meaningful ("no skipped tests at all"): M1e validation rule 5's
	// "threshold must be 0 when the predicate takes none" does not apply.
	GateSkipsNotAbove: {metrics: []string{MetricTestsSkipped}, threshold: true},
	GateCorpusComplete: {metrics: []string{
		MetricCorpusCasesObserved, MetricCorpusCasesTotal,
	}},
	// The threshold is a world count and must be >= 2 (rule 19's
	// companion): a cohort gate that accepts a cohort of one accepts a
	// comparison that never happened.
	GateDifferentialCohortAtLeast: {metrics: []string{MetricDiffCohortN}, threshold: true},
	GatePropertiesPass: {metrics: []string{
		MetricPropertiesFailed, MetricPropertiesErrored,
	}},
	GatePropertyCasesAtLeast: {metrics: []string{MetricPropertyCasesTotal}, threshold: true},
	// mutation-survivors-not-above TAKES a parameter, so threshold == 0 is
	// legal and is in fact the shipped fixture's value ("no mutant of a
	// line this patch wrote went unkilled"). It reads two ABSOLUTE counts,
	// never the ratio: the ratio's denominator is the candidate's own diff.
	// `mutants_timeout` is in the numerator beside `mutants_survived`
	// because a mutant that hung is a mutant the tests did not kill, and
	// leaving it out made a hang a free escape from the only mutation gate
	// M2a ships.
	GateMutationSurvivorsNotAbove: {
		metrics:   []string{MetricMutantsSurvived, MetricMutantsTimeout},
		threshold: true,
	},
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
	KindCommand: {family: FamilySuite},
	KindTreeGuard: {family: FamilyTree, metrics: []string{
		MetricProtectedModified, MetricProtectedDeleted, MetricProtectedAdded,
		MetricHarnessModified, MetricHarnessDeleted, MetricHarnessAdded,
		MetricPathsExamined,
	}},
	KindPytestCollect: {family: FamilyCollect, metrics: []string{MetricCollectedTotal, MetricCollectedBase, MetricCollectedDelta}},
	KindPytestSuite: {family: FamilySuite, metrics: []string{
		MetricTestsTotal, MetricTestsPassed, MetricTestsFailed, MetricTestsErrored,
		MetricTestsSkipped, MetricDurationMS, MetricCoverageBP,
		MetricTestsFailedFirstRun, MetricTestsPassedAfterRun,
	}},
	KindCorpusObserve: {family: FamilyBehavior, metrics: []string{
		MetricCorpusCasesTotal, MetricCorpusCasesObserved,
		MetricCorpusCasesOpaque, MetricCorpusCasesErrored,
	}},
	KindCorpusDifferential: {family: FamilyBehavior, metrics: []string{
		MetricDiffCohortN, MetricDiffClasses, MetricDiffClassSize,
		MetricDiffCasesCompared, MetricDiffCasesIncomparable, MetricDiffCasesDivergent,
		MetricDiffCasesUnilateral, MetricDiffCasesVsBase, MetricDiffCasesUnilateralVBase,
	}},
	KindProperties: {family: FamilyProperty, metrics: []string{
		MetricPropertiesTotal, MetricPropertiesPassed, MetricPropertiesFailed,
		MetricPropertiesErrored, MetricPropertyCasesTotal, MetricPropertyCasesInvalid,
		MetricDurationMS, MetricCoverageBP,
	}},
	KindMutationDiff: {family: FamilyMutation, metrics: []string{
		MetricMutationLinesTargeted, MetricMutantsBudget, MetricMutantsCandidates,
		MetricMutantsTested, MetricMutantsKilled, MetricMutantsSurvived,
		MetricMutantsTimeout, MetricMutantsUnviable, MetricMutationScoreBP,
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

// Evidence regime selection (M1f decision 13). RegimeAuto is the compiled
// default and picks `isolated` when the tier is T1 and the platform
// supplies a distinct oracle uid, `streamed` otherwise. The default tier
// is T0, so THE DEFAULT REGIME ON A LAPTOP IS `streamed` — said loudly
// here and printed by `mvo explain`, because a guarantee nobody can see
// the absence of is a guarantee nobody has.
const RegimeAuto = "auto"

// Cross-source agreement (M1f decision 9). "require" is the compiled
// default: honest runs agree, and disagreement is `status = error` —
// brittleness escalates, it never convicts and never acquits.
const (
	CrosscheckRequire = "require"
	CrosscheckOff     = "off"
)

// Entry-point plugin autoloading inside the oracle run. AutoloadOff is the
// compiled default and the seal the red team's entry-point attack needed:
// pytest imports pytest11 entry points from any *.egg-info / *.dist-info on
// sys.path, the candidate tree root is on sys.path, and the plugin module
// can be called anything — so the harness glob set cannot close it and
// PYTEST_DISABLE_PLUGIN_AUTOLOAD must. AutoloadOn is the escape hatch for
// suites that genuinely need an installed plugin, and it is a pinned,
// attested policy choice with a stated cost.
const (
	AutoloadOff = "off"
	AutoloadOn  = "on"
)

// KnownRegimes returns the legal evidence.regime values, sorted.
func KnownRegimes() []string {
	return []string{RegimeAuto, object.RegimeInTree, object.RegimeIsolated, object.RegimeStreamed}
}

// KnownCrosschecks returns the legal evidence.crosscheck values, sorted.
func KnownCrosschecks() []string { return []string{CrosscheckOff, CrosscheckRequire} }

// KnownPluginAutoload returns the legal evidence.plugin_autoload values,
// sorted.
func KnownPluginAutoload() []string { return []string{AutoloadOff, AutoloadOn} }

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
	// A corpus-observe instance that declares no provider has no corpus to
	// replay, so it emits nothing at all. Catching that at load is the same
	// authoring-bug class as a coverage gate on a coverage-disabled suite:
	// the alternative is a gate that fails forever on
	// "corpus_cases_total absent (source unavailable)".
	if spec.Kind == KindCorpusObserve {
		return spec.Corpus.Provider != ProviderNone
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
