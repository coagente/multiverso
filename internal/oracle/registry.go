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
)

// Correlation families a kind's receipts carry.
const (
	FamilySuite   = policy.FamilySuite
	FamilyCollect = policy.FamilyCollect
	FamilyTree    = policy.FamilyTree
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
