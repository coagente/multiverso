package oracle

import (
	"fmt"
	"time"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// Registry kinds (EP-1). The set is CLOSED: a policy naming anything else
// is refused when it is loaded, so New's unknown-kind branch is a machinery
// assertion rather than a runtime decision.
//
// Every name here is ALIASED from internal/policy's closed vocabulary,
// which is validation's source of truth. A kind, family or metric this
// package could spell differently from the validator is a name that could
// drift — a gate validated against "tests_passed" while the oracle emitted
// "testsPassed" would fail forever with no error anywhere. The compiler now
// forbids that.
const (
	KindCommand       = policy.KindCommand
	KindPytestCollect = policy.KindPytestCollect
	KindPytestSuite   = policy.KindPytestSuite
	KindTreeGuard     = policy.KindTreeGuard
	// M2a's two per-world rungs beside the differential: O2p, the property
	// oracle, and O3, diff-scoped mutation.
	KindProperties   = policy.KindProperties
	KindMutationDiff = policy.KindMutationDiff
	// M2a's cross-candidate differential, in two halves (decision 1): an
	// ordinary per-world observer, and a pure control-plane reducer.
	KindCorpusObserve      = policy.KindCorpusObserve
	KindCorpusDifferential = policy.KindCorpusDifferential
)

// Correlation families a kind's receipts carry.
const (
	FamilySuite    = policy.FamilySuite
	FamilyCollect  = policy.FamilyCollect
	FamilyTree     = policy.FamilyTree
	FamilyProperty = policy.FamilyProperty
	FamilyMutation = policy.FamilyMutation
	FamilyBehavior = policy.FamilyBehavior
)

// Metric names the pytest kinds emit (EP-2). Absence is meaningful: a
// metric whose structured source was unavailable is simply not in the map.
const (
	MetricCollectedTotal        = policy.MetricCollectedTotal
	MetricCollectedBase         = policy.MetricCollectedBase
	MetricCollectedDelta        = policy.MetricCollectedDelta
	MetricTestsTotal            = policy.MetricTestsTotal
	MetricTestsPassed           = policy.MetricTestsPassed
	MetricTestsFailed           = policy.MetricTestsFailed
	MetricTestsErrored          = policy.MetricTestsErrored
	MetricTestsSkipped          = policy.MetricTestsSkipped
	MetricDurationMS            = policy.MetricDurationMS
	MetricCoverageBP            = policy.MetricCoverageBP
	MetricTestsFailedFirstRun   = policy.MetricTestsFailedFirstRun
	MetricTestsPassedAfterRerun = policy.MetricTestsPassedAfterRun

	// O2p. property_cases_* exist ONLY when the per-case records reached
	// the control-plane stream (M2a decision 15); under the JSONL fallback
	// they are absent and result.tools says which path ran.
	MetricPropertiesTotal      = policy.MetricPropertiesTotal
	MetricPropertiesPassed     = policy.MetricPropertiesPassed
	MetricPropertiesFailed     = policy.MetricPropertiesFailed
	MetricPropertiesErrored    = policy.MetricPropertiesErrored
	MetricPropertyCasesTotal   = policy.MetricPropertyCasesTotal
	MetricPropertyCasesInvalid = policy.MetricPropertyCasesInvalid

	// O3.
	MetricMutationLinesTargeted = policy.MetricMutationLinesTargeted
	MetricMutantsBudget         = policy.MetricMutantsBudget
	MetricMutantsCandidates     = policy.MetricMutantsCandidates
	MetricMutantsTested         = policy.MetricMutantsTested
	MetricMutantsKilled         = policy.MetricMutantsKilled
	MetricMutantsSurvived       = policy.MetricMutantsSurvived
	MetricMutantsTimeout        = policy.MetricMutantsTimeout
	MetricMutantsUnviable       = policy.MetricMutantsUnviable
	MetricMutationScoreBP       = policy.MetricMutationScoreBP
)

// Structured sources the tools probe reports (decision 16). These are
// oracle-local: they name PyPI distributions, not policy vocabulary. The
// keys of Result.Tools are exactly the sources that were available AND
// used.
const (
	ToolPytest        = "pytest"
	ToolCoverage      = "coverage"
	ToolReportlog     = "pytest-reportlog"
	ToolRerunfailures = "pytest-rerunfailures"
)

// oracleVersion is OUR oracle contract version, not the tool's: the tool's
// version is recorded in result.tools, where it belongs.
const oracleVersion = "v0"

// Kinds lists the registry's kinds, sorted.
func Kinds() []string { return policy.KnownKinds() }

// Family reports the correlation family a kind's receipts carry, or "" for
// an unknown kind.
func Family(kind string) string { return policy.KindFamily(kind) }

// MetricVocabulary reports the metric names a kind may emit, sorted. An
// implementation emits this set or a SUBSET of it (a source that was
// unavailable yields no metric); it never emits a name outside it, and a
// conformance test asserts exactly that.
func MetricVocabulary(kind string) []string { return policy.KindMetrics(kind) }

// Params builds one oracle instance from a compiled policy's declaration.
type Params struct {
	// Spec is the resolved configuration of one declared oracle. Its
	// Config is the digest of the canonical resolved configuration WITHOUT
	// the name (M1e decision 8): an instance's identity in evidence is
	// (receipt.oracle.id, receipt.oracle.config), so a receipt records what
	// ran, not what an operator called it, and two policies running the
	// identical oracle produce comparable evidence.
	Spec policy.Oracle
	CAS  *cas.Store
	// Timeout is the fallback wall bound when the spec names none: the
	// intent's max_wall_ms.
	Timeout time.Duration
	// Baseline is the base-state collected-test count (decision 13) —
	// collected_delta's denominator, measured once per race on the base
	// tree through the same backend. ZERO MEANS "NOT MEASURED": a collect
	// oracle then omits collected_base/collected_delta rather than
	// inventing a denominator, because a delta against a fiction is worse
	// than no delta at all.
	Baseline int64

	// --- M1f -----------------------------------------------------------
	// Paths is the compiled path grammar the tree-guard walks; Repo,
	// BaseTree and CandidateTree are the per-WORLD inputs that made oracle
	// construction per-world rather than per-race (decision 18): the guard
	// needs the candidate's tree digest, and smuggling per-world state
	// through the Oracle interface or a type assertion would have been the
	// alternative.
	Paths         policy.PathSet
	Repo          string
	BaseTree      string
	CandidateTree string

	// --- M2a: O3 --------------------------------------------------------
	// Patch is the AG-4 captured patch of THIS world — the bytes the
	// control plane captured and hashed, never a file read back out of the
	// candidate's tree. mutation-diff derives its target set from it, so a
	// nil patch is refused at construction: an unsupplied patch and an
	// empty one must not produce the same vacuous pass.
	Patch []byte

	// --- M2a: the corpus plane -----------------------------------------
	// Corpus is the PINNED corpus a corpus-observe instance replays, and
	// CorpusDigest its content address. Both come from phase 0, which
	// materialized them on the base tree before any candidate existed —
	// which is what makes corpus_cases_total a number no candidate can
	// author in any regime.
	Corpus       Corpus
	CorpusDigest string
	// CorpusPath is where the runner reads the corpus (host path on T0,
	// the read-only mount on T1). It is never inside the worktree.
	CorpusPath string
	// CorpusRunner is the in-world path of mvo_corpus.py and
	// CorpusRunnerDigest its content address, recorded in every receipt it
	// produced. WorldRoot is the in-world worktree root, prepended to
	// PYTHONPATH so the corpus's targets resolve to the CANDIDATE's code.
	CorpusRunner       string
	CorpusRunnerDigest string
	WorldRoot          string

	// Regime is the RESOLVED evidence regime for this run (never "auto":
	// the orchestrator resolves auto against the tier before building the
	// instance, so a receipt never records a word that means "it
	// depends"). Crosscheck is the policy's compiled setting.
	Regime     string
	Crosscheck string
	// PluginAutoload is the policy's compiled evidence.plugin_autoload.
	// "" is read as policy.AutoloadOff — the seal, never the hole: a
	// caller that forgot to thread it must not silently reopen the
	// entry-point surface.
	PluginAutoload string
	// EvidenceDir and ScratchDir are control-plane-owned HOST directories;
	// InWorld* are where the world sees them (identical on T0). PluginDir
	// holds the materialized observer and PluginDigest is its content
	// address, recorded in every receipt it produced.
	EvidenceDir     string
	ScratchDir      string
	InWorldEvidence string
	InWorldScratch  string
	PluginDir       string
	InWorldPlugin   string
	PluginDigest    string
}

// New builds the oracle instance a compiled policy declares. An unknown
// kind is an error the policy validator has already made unreachable.
func New(p Params) (Oracle, error) {
	s := p.Spec
	fam := policy.KindFamily(s.Kind)
	switch {
	case fam == "":
		return nil, fmt.Errorf("oracle: unknown kind %q (known: %v)", s.Kind, Kinds())
	case p.CAS == nil:
		return nil, fmt.Errorf("oracle: %s: nil CAS store", s.Kind)
	case s.Config == "":
		// Without the resolved-config digest a receipt cannot say WHICH
		// instance produced it, and no gate could select it.
		return nil, fmt.Errorf("oracle: %s: spec carries no resolved config digest", s.Kind)
	case s.Family != "" && s.Family != fam:
		return nil, fmt.Errorf("oracle: %s: spec family %q disagrees with the registry (%q)",
			s.Kind, s.Family, fam)
	case s.TimeoutMS < 0:
		return nil, fmt.Errorf("oracle: %s: negative timeout_ms %d", s.Kind, s.TimeoutMS)
	case s.Reruns < 0:
		return nil, fmt.Errorf("oracle: %s: negative reruns %d", s.Kind, s.Reruns)
	case s.Reruns > 0 && s.Kind != KindPytestSuite:
		return nil, fmt.Errorf("oracle: %s: reruns is a %s setting", s.Kind, KindPytestSuite)
	}
	timeout := p.Timeout
	if s.TimeoutMS > 0 {
		timeout = time.Duration(s.TimeoutMS) * time.Millisecond
	}
	if s.Kind == KindCommand {
		if len(s.Argv) == 0 {
			return nil, fmt.Errorf("oracle: %s: empty argv", s.Kind)
		}
		return &CommandOracle{
			Argv:    append([]string(nil), s.Argv...),
			Timeout: timeout,
			CAS:     p.CAS,
			Config:  s.Config,
		}, nil
	}
	if s.Kind == KindTreeGuard {
		switch {
		case p.Repo == "":
			return nil, fmt.Errorf("oracle: %s: no repository to read trees from", s.Kind)
		case p.BaseTree == "":
			return nil, fmt.Errorf("oracle: %s: no base tree (the denominator is the intent's base in a race and the pre-apply trunk tree at admission)", s.Kind)
		case p.Paths.Empty():
			return nil, fmt.Errorf("oracle: %s: the policy declares no protected or harness pattern", s.Kind)
		}
		return &guardOracle{
			spec:  s,
			store: p.CAS,
			paths: p.Paths,
			repo:  p.Repo,
			base:  p.BaseTree,
			tree:  p.CandidateTree,
		}, nil
	}
	if s.Kind == KindCorpusDifferential {
		// The reducer is a PURE function, not a runnable oracle: it takes
		// many worlds' observations and emits many receipts, so it cannot
		// satisfy Run(ctx, world) → Receipt and must not pretend to. The
		// orchestrator calls Reduce at the cohort barrier (phase B2)
		// instead, and building an instance here would only produce a
		// handle whose Run would have to lie.
		return nil, fmt.Errorf("oracle: %s is a cohort-stage reducer: it is run once at the cohort barrier over every world's observation, not per world",
			s.Kind)
	}
	regime := p.Regime
	if regime == "" || regime == policy.RegimeAuto {
		// A receipt must never record a word that means "it depends". An
		// unresolved regime here is a caller that did not resolve it, and
		// `streamed` is the honest floor — it needs nothing but a FIFO.
		regime = object.RegimeStreamed
	}
	if p.EvidenceDir == "" {
		// No channel was supplied, so no stream can exist. The regime a
		// receipt records must be what ACTUALLY happened, so this is
		// `in-tree` — the M1e path — and it is labelled as such rather
		// than claiming a boundary that was never drawn. `isolated` is the
		// one regime that may not degrade quietly: a policy that demands
		// it is refused instead.
		if regime == object.RegimeIsolated {
			return nil, fmt.Errorf("oracle: %s: evidence regime %q requires a control-plane evidence channel, and none was supplied",
				s.Kind, object.RegimeIsolated)
		}
		regime = object.RegimeInTree
	}
	crosscheck := p.Crosscheck
	if crosscheck == "" {
		crosscheck = policy.CrosscheckRequire
	}
	autoload := p.PluginAutoload
	if autoload == "" {
		autoload = policy.AutoloadOff
	}
	if s.Kind == KindMutationDiff {
		// The captured patch is the target set's ONLY source (M2a decision
		// 16), and a nil one is machinery failure rather than an empty
		// diff: "the candidate changed no mutable line" passes the
		// survivor gate vacuously, so it must never be what an unsupplied
		// patch degrades into.
		if p.Patch == nil {
			return nil, fmt.Errorf("oracle: %s: no captured patch supplied: the mutation target set is derived from the AG-4 patch, and an unsupplied patch must not read as an empty one", s.Kind)
		}
		return &mutationOracle{
			spec:    s,
			store:   p.CAS,
			patch:   append([]byte(nil), p.Patch...),
			paths:   p.Paths,
			timeout: timeout,
			cap:     artifactCapBytes,
			ev: evidencePlan{
				regime:          regime,
				crosscheck:      crosscheck,
				autoload:        autoload,
				hostEvidence:    p.EvidenceDir,
				hostScratch:     p.ScratchDir,
				inWorldEvidence: p.InWorldEvidence,
				inWorldScrap:    p.InWorldScratch,
				pluginDir:       p.PluginDir,
				inWorldPlugin:   p.InWorldPlugin,
				pluginDigest:    p.PluginDigest,
			},
		}, nil
	}
	if s.Kind == KindCorpusObserve {
		switch {
		case p.CorpusDigest == "":
			return nil, fmt.Errorf("oracle: %s: no pinned corpus (phase 0 materializes it on the base tree before any world exists)", s.Kind)
		case p.CorpusPath == "":
			return nil, fmt.Errorf("oracle: %s: the corpus was not delivered to this world", s.Kind)
		case p.CorpusRunner == "":
			return nil, fmt.Errorf("oracle: %s: no corpus runner was materialized", s.Kind)
		case p.EvidenceDir == "":
			// The observation IS the stream. Without a channel there is
			// nothing to observe with, and an oracle that ran anyway would
			// produce a receipt whose absent metrics look like a candidate
			// problem rather than a plumbing one.
			return nil, fmt.Errorf("oracle: %s: evidence regime %q requires a control-plane evidence channel, and none was supplied",
				s.Kind, regime)
		}
		root := p.WorldRoot
		if root == "" {
			return nil, fmt.Errorf("oracle: %s: no in-world worktree root to import the candidate's modules from", s.Kind)
		}
		return &observeOracle{
			spec:         s,
			store:        p.CAS,
			timeout:      timeout,
			cap:          artifactCapBytes,
			corpus:       p.Corpus,
			corpusDigest: p.CorpusDigest,
			baseTree:     p.BaseTree,
			corpusPath:   p.CorpusPath,
			runner:       p.CorpusRunner,
			runnerDigest: p.CorpusRunnerDigest,
			worldRoot:    root,
			ev: evidencePlan{
				regime:          regime,
				crosscheck:      crosscheck,
				autoload:        autoload,
				hostEvidence:    p.EvidenceDir,
				hostScratch:     p.ScratchDir,
				inWorldEvidence: p.InWorldEvidence,
				inWorldScrap:    p.InWorldScratch,
			},
		}, nil
	}
	return &pytestOracle{
		kind:     s.Kind,
		spec:     s,
		store:    p.CAS,
		timeout:  timeout,
		baseline: p.Baseline,
		cap:      artifactCapBytes,
		ev: evidencePlan{
			regime:          regime,
			crosscheck:      crosscheck,
			autoload:        autoload,
			hostEvidence:    p.EvidenceDir,
			hostScratch:     p.ScratchDir,
			inWorldEvidence: p.InWorldEvidence,
			inWorldScrap:    p.InWorldScratch,
			pluginDir:       p.PluginDir,
			inWorldPlugin:   p.InWorldPlugin,
			pluginDigest:    p.PluginDigest,
		},
	}, nil
}
