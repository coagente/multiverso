package oracle

import (
	"fmt"
	"time"

	"github.com/coagente/multiverso/internal/cas"
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
)

// Correlation families a kind's receipts carry.
const (
	FamilySuite   = policy.FamilySuite
	FamilyCollect = policy.FamilyCollect
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
	return &pytestOracle{
		kind:     s.Kind,
		spec:     s,
		store:    p.CAS,
		timeout:  timeout,
		baseline: p.Baseline,
		cap:      artifactCapBytes,
	}, nil
}
