package oracle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// oracleWorkRoot is the in-world directory every pytest kind writes its
// native artifacts into. The path is RELATIVE so the argv is
// tier-independent and deterministic: the same evidence-producing command
// string appears in a T0 receipt and a T1 receipt.
const oracleWorkRoot = ".mvo-oracle"

// artifactCapBytes bounds one stored artifact (EP-7). An over-cap file is
// neither stored nor parsed: the control plane must not be talked into
// buffering a gigabyte because a candidate looped a print statement.
const artifactCapBytes = 64 << 20

// Artifact kinds, in Result.Artifacts order. The order is fixed by kind
// with absent kinds skipped, and result.tools says which of the optional
// three can be present — so the list is unambiguous without positional
// guessing.
const (
	artStdout    = "stdout"
	artStderr    = "stderr"
	artProbe     = "tools-probe"
	artJUnit     = "junit-xml"
	artReportlog = "reportlog"
	artCoverage  = "coverage-json"
)

// probeScript reports which structured sources are importable in the world
// (decision 16). Absence is the record: a source missing from the output is
// a source whose metrics will be ABSENT from the receipt — never zero,
// never "assumed 100 %".
const probeScript = `import json,importlib.metadata as m
o={}
for n in ("pytest","coverage","pytest-reportlog","pytest-rerunfailures"):
    try: o[n]=m.version(n)
    except Exception: pass
print(json.dumps(o,sort_keys=True))
`

// probeTools is the ordered list of sources the probe may report; anything
// else in its output is ignored (the probe runs in a world an agent wrote
// to, so its output is input, not truth).
var probeTools = []string{ToolCoverage, ToolPytest, ToolReportlog, ToolRerunfailures}

// artifactStore is the CAS surface the pytest oracles write through. The
// concrete *cas.Store satisfies it; the narrow interface is what lets the
// EP-7 ordering test prove that every artifact reaches CAS BEFORE any of it
// is parsed (research ch. 19 recommendation 2: hash-and-store first, parse
// into typed evidence second).
type artifactStore interface {
	Put(b []byte) (string, error)
}

// pytestOracle implements both Python rungs of the v0 ladder: O0
// (pytest-collect) and O1 (pytest-suite). They share a probe, a working
// directory discipline, the artifact pipeline and the status mapping; they
// differ in argv and in what they parse.
type pytestOracle struct {
	kind     string
	spec     policy.Oracle
	store    artifactStore
	timeout  time.Duration
	baseline int64
	cap      int64 // artifactCapBytes in production; small in tests
}

// ID implements Oracle: the registry kind.
func (o *pytestOracle) ID() string { return o.kind }

// Version implements Oracle: our contract version, not pytest's.
func (o *pytestOracle) Version() string { return oracleVersion }

// prefix is the runner invocation the kind's flags are appended to.
func (o *pytestOracle) prefix() []string {
	if len(o.spec.Argv) > 0 {
		return append([]string(nil), o.spec.Argv...)
	}
	return policy.DefaultPytestPrefix()
}

// python is the interpreter the probe and the coverage reporter run under:
// the head of the runner prefix, so a repo pinned to a virtualenv's
// interpreter is probed with that interpreter and not with some other
// python that happens to be first on PATH.
func (o *pytestOracle) python() string {
	if p := o.prefix(); len(p) > 0 && p[0] != "" {
		return p[0]
	}
	return policy.DefaultPytestPrefix()[0]
}

// workRel is the kind's in-world working directory, forward-slashed.
func (o *pytestOracle) workRel() string { return path.Join(oracleWorkRoot, o.kind) }

func (o *pytestOracle) junitRel() string     { return path.Join(o.workRel(), "junit.xml") }
func (o *pytestOracle) reportlogRel() string { return path.Join(o.workRel(), "reportlog.jsonl") }
func (o *pytestOracle) coverageRel() string  { return path.Join(o.workRel(), "coverage.json") }

// collectArgv is O0: pytest --collect-only -q -p no:cacheprovider.
func (o *pytestOracle) collectArgv() []string {
	argv := append(o.prefix(), "--collect-only", "-q", "-p", "no:cacheprovider")
	return append(argv, o.spec.Args...)
}

// suitePlan is O1's resolved invocation plus which optional sources it
// actually engaged — the record of what was USED, which is what
// result.tools reports.
type suitePlan struct {
	argv      []string
	coverage  bool
	reportlog bool
	reruns    bool
}

// planSuite resolves O1's single invocation against what the probe found.
//
// Every optional flag is requested only when its plugin is present, so a
// missing plugin degrades the evidence (metrics absent) instead of failing
// the run with a usage error. --reruns is special: it is requested only
// when pytest-rerunfailures AND pytest-reportlog are both present, because
// JUnit XML records only the final outcome — without the JSONL there is no
// honest way to see the first run, so we do not ask for reruns at all
// (decision 17).
func (o *pytestOracle) planSuite(tools map[string]string) suitePlan {
	var p suitePlan
	base := o.prefix()
	if o.spec.Coverage && tools[ToolCoverage] != "" && policy.CoverageWrappable(base) {
		base, p.coverage = wrapCoverage(base), true
	}
	argv := append(base, "--junit-xml="+o.junitRel(), "-p", "no:cacheprovider")
	if tools[ToolReportlog] != "" {
		argv, p.reportlog = append(argv, "--report-log="+o.reportlogRel()), true
	}
	if o.spec.Reruns > 0 && p.reportlog && tools[ToolRerunfailures] != "" {
		argv, p.reruns = append(argv, "--reruns", strconv.Itoa(o.spec.Reruns)), true
	}
	p.argv = append(argv, o.spec.Args...)
	return p
}

// wrapCoverage turns `<python> -m pytest …` into
// `<python> -m coverage run -m pytest …`.
func wrapCoverage(prefix []string) []string {
	out := []string{prefix[0], "-m", "coverage", "run"}
	return append(out, prefix[1:]...)
}

// coverageJSONArgv extracts the coverage report AFTER the suite ran. Its
// failure drops the coverage source and never touches the receipt's status
// or exit code: artifact extraction is not verification.
func (o *pytestOracle) coverageJSONArgv() []string {
	return []string{o.python(), "-m", "coverage", "json", "-o", o.coverageRel()}
}

// Probe runs the tools probe in w and reports which structured sources are
// importable there, plus the raw probe stdout for storage. Pre-flight
// (decision 15) uses it to refuse a race whose policy requires a pytest
// kind in an environment without pytest — a missing toolchain is machinery,
// never a failing candidate. python may be "" for the default interpreter.
//
// There is no error return: a probe that cannot run reports NOTHING, which
// is the honest record and fails every metric gate on its own.
func Probe(ctx context.Context, w backend.World, python string) (map[string]string, []byte) {
	raw := probeRaw(ctx, w, python)
	return parseProbe(raw), raw
}

// probeRaw runs the probe and returns its raw stdout, unparsed — the bytes
// that go to CAS before anything reads them (EP-7).
func probeRaw(ctx context.Context, w backend.World, python string) []byte {
	if python == "" {
		python = policy.DefaultPytestPrefix()[0]
	}
	res := runInWorld(ctx, w, []string{python, "-c", probeScript}, nil)
	if res.Status != StatusPass {
		return nil
	}
	return res.Stdout
}

// parseProbe reads the probe's JSON object, keeping only the four names the
// script asks about. Unparseable output yields an empty map.
func parseProbe(raw []byte) map[string]string {
	out := map[string]string{}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		return out
	}
	for _, name := range probeTools {
		if v := strings.TrimSpace(got[name]); v != "" {
			out[name] = v
		}
	}
	return out
}

// collectedRe and noCollectRe match pytest's --collect-only -q summary line
// ("8 tests collected in 0.01s", "1 test collected", "3 tests collected, 1
// error in 0.05s", "no tests collected in 0.00s").
var (
	collectedRe = regexp.MustCompile(`^(\d+) tests? collected`)
	noCollectRe = regexp.MustCompile(`^no tests collected`)
)

// nodeIDSuffix ends a bare file node id ("tests/test_stats.py"); a node id
// naming a test carries "::".
const nodeIDSuffix = ".py"

// parseCollected extracts the collected-test count from --collect-only
// stdout: the LAST summary line wins, falling back to counting node-id
// lines when no summary line is present. When neither source yields a
// value the count is ABSENT (false) and the collect-nonempty gate fails on
// a missing metric rather than on a fabricated zero.
func parseCollected(stdout []byte) (int64, bool) {
	nodes := int64(0)
	total, found := int64(0), false
	for _, raw := range strings.Split(string(stdout), "\n") {
		line := strings.TrimRight(strings.TrimSpace(raw), "\r")
		if line == "" {
			continue
		}
		if m := collectedRe.FindStringSubmatch(line); m != nil {
			if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				total, found = n, true
			}
			continue
		}
		if noCollectRe.MatchString(line) {
			total, found = 0, true
			continue
		}
		if strings.Contains(line, "::") || strings.HasSuffix(line, nodeIDSuffix) {
			nodes++
		}
	}
	switch {
	case found:
		return total, true
	case nodes > 0:
		return nodes, true
	}
	return 0, false
}

// runOutcome is one kind's execution and parse, before the receipt
// envelope.
type runOutcome struct {
	argv      []string
	proc      procResult
	status    string
	metrics   map[string]int64
	tools     map[string]string
	artifacts []string
}

// Run implements Oracle: probe, run, store, parse — in that order.
//
// A failing suite is evidence and comes back in the receipt with err nil.
// A non-nil error means the EVIDENCE could not be produced (a CAS write
// failed, the working directory could not be scrubbed): the caller records
// nothing rather than a receipt whose artifacts are not all in the store.
func (o *pytestOracle) Run(ctx context.Context, w backend.World) (object.Receipt, error) {
	switch {
	case o.store == nil:
		return object.Receipt{}, fmt.Errorf("oracle: %s: nil CAS store", o.kind)
	case w == nil:
		return object.Receipt{}, fmt.Errorf("oracle: %s: nil world", o.kind)
	case o.spec.Config == "":
		return object.Receipt{}, fmt.Errorf("oracle: %s: spec carries no resolved config digest", o.kind)
	}
	// The worktree is the agent's write surface, so the working directory
	// is scrubbed HOST-SIDE before the run: a planted junit.xml must never
	// be read as evidence, and a stale file from a previous run must never
	// be parsed as this one's. The bind mount (M1c decision 3) makes the
	// host removal reach a T1 world with no extra exec.
	hostWork := filepath.Join(w.Dir(), filepath.FromSlash(o.workRel()))
	if err := os.RemoveAll(hostWork); err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: %s: scrub %s: %w", o.kind, o.workRel(), err)
	}
	if err := os.MkdirAll(hostWork, 0o755); err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: %s: create %s: %w", o.kind, o.workRel(), err)
	}

	start := time.Now()
	runCtx, cancel := ctx, func() {}
	if o.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, o.timeout)
	}
	defer cancel()

	// The probe decides the run's argv, so it necessarily precedes the
	// run; its bytes still reach CAS before they are parsed.
	probeBytes := probeRaw(runCtx, w, o.python())
	probeKey, err := o.store.Put(probeBytes)
	if err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artProbe, err)
	}
	tools := parseProbe(probeBytes)

	var r runOutcome
	switch o.kind {
	case KindPytestCollect:
		r, err = o.runCollect(runCtx, w, tools, probeKey)
	case KindPytestSuite:
		r, err = o.runSuite(runCtx, w, tools, probeKey)
	default: // unreachable: New refused every other kind
		return object.Receipt{}, fmt.Errorf("oracle: unknown kind %q", o.kind)
	}
	if err != nil {
		return object.Receipt{}, err
	}

	return object.Receipt{
		Schema: object.SchemaReceipt,
		Oracle: object.OracleRef{ID: o.kind, Version: o.Version(), Config: o.spec.Config},
		Execution: object.Execution{
			Argv:          r.argv,
			ExitCode:      r.proc.ExitCode,
			DurationMS:    r.proc.WallMS,
			IsolationTier: w.Tier(),
			IsolationCaps: w.Caps(),
		},
		Result: object.Result{
			Status:    r.status,
			Metrics:   r.metrics,
			Tools:     r.tools,
			Artifacts: r.artifacts,
		},
		Freshness:   object.Freshness{Basis: object.BasisConstruction},
		RecheckTier: recheckTier,
		Family:      Family(o.kind),
		// The receipt's cost is the WHOLE oracle: probe, run and report
		// extraction. execution.duration_ms is the verdict-producing
		// process alone.
		Cost:      object.Cost{WallMS: time.Since(start).Milliseconds()},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// runCollect is O0: the collected-test count, and the exit-5 rule.
func (o *pytestOracle) runCollect(ctx context.Context, w backend.World, tools map[string]string, probeKey string) (runOutcome, error) {
	r := runOutcome{
		argv:    o.collectArgv(),
		metrics: map[string]int64{},
		tools:   map[string]string{},
	}
	r.proc = runInWorld(ctx, w, r.argv, nil)
	r.status = r.proc.Status

	stdoutKey, err := o.store.Put(r.proc.Stdout)
	if err != nil {
		return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStdout, err)
	}
	stderrKey, err := o.store.Put(r.proc.Stderr)
	if err != nil {
		return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStderr, err)
	}
	r.artifacts = []string{stdoutKey, stderrKey, probeKey}

	// Only now is anything parsed (EP-7).
	if v := tools[ToolPytest]; v != "" {
		r.tools[ToolPytest] = v
	}
	total, known := parseCollected(r.proc.Stdout)
	if r.proc.ExitCode == 5 {
		// Exit 5 is "no tests collected" — the laundering vector research
		// ch. 19 names. It is non-zero, so the status mapping already
		// refuses it (fail, never pass, no tolerant wrapper anywhere), and
		// the count is recorded EXPLICITLY as 0 so collect-nonempty fails
		// with a reason instead of for want of a metric.
		total, known = 0, true
	}
	if known {
		r.metrics[MetricCollectedTotal] = total
		if o.baseline > 0 {
			r.metrics[MetricCollectedBase] = o.baseline
			r.metrics[MetricCollectedDelta] = total - o.baseline
		}
	}
	return r, nil
}

// runSuite is O1: the suite gate, its native artifacts, and the metrics
// parsed out of them.
func (o *pytestOracle) runSuite(ctx context.Context, w backend.World, tools map[string]string, probeKey string) (runOutcome, error) {
	plan := o.planSuite(tools)
	r := runOutcome{
		argv:    plan.argv,
		metrics: map[string]int64{},
		tools:   map[string]string{},
	}
	r.proc = runInWorld(ctx, w, r.argv, nil)
	r.status = r.proc.Status

	// Report extraction, never verification: whatever it does, the
	// receipt's status and exit code are the suite's.
	coverageExtracted := false
	if plan.coverage {
		coverageExtracted = runInWorld(ctx, w, o.coverageJSONArgv(), nil).Status == StatusPass
	}

	// Read the native artifacts under the cap BEFORE storing stderr: an
	// over-cap or unreadable file is noted in the stored stderr bytes (the
	// M1b/M1c control-plane-note precedent), so the operator sees why a
	// metric is missing without a second channel.
	notes := ""
	junitBytes, note := o.readCapped(w.Dir(), o.junitRel())
	notes += note
	var reportlogBytes, coverageBytes []byte
	if plan.reportlog {
		reportlogBytes, note = o.readCapped(w.Dir(), o.reportlogRel())
		notes += note
	}
	if coverageExtracted {
		coverageBytes, note = o.readCapped(w.Dir(), o.coverageRel())
		notes += note
	}

	stdoutKey, err := o.store.Put(r.proc.Stdout)
	if err != nil {
		return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStdout, err)
	}
	stderrKey, err := o.store.Put(append(append([]byte(nil), r.proc.Stderr...), notes...))
	if err != nil {
		return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStderr, err)
	}
	r.artifacts = []string{stdoutKey, stderrKey, probeKey}
	for _, art := range []struct {
		name  string
		bytes []byte
	}{
		{artJUnit, junitBytes},
		{artReportlog, reportlogBytes},
		{artCoverage, coverageBytes},
	} {
		if art.bytes == nil {
			continue
		}
		key, err := o.store.Put(art.bytes)
		if err != nil {
			return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, art.name, err)
		}
		r.artifacts = append(r.artifacts, key)
	}

	// Every artifact is in CAS; only now is anything parsed (EP-7).
	if v := tools[ToolPytest]; v != "" {
		r.tools[ToolPytest] = v
	}
	if junitBytes != nil {
		if sum, err := parseJUnit(junitBytes); err == nil {
			r.metrics[MetricTestsTotal] = sum.total
			r.metrics[MetricTestsPassed] = sum.passed()
			r.metrics[MetricTestsFailed] = sum.failures
			r.metrics[MetricTestsErrored] = sum.errors
			r.metrics[MetricTestsSkipped] = sum.skipped
			r.metrics[MetricDurationMS] = sum.durationMS
		}
	}
	if reportlogBytes != nil {
		if sum, err := parseReportlog(reportlogBytes); err == nil {
			r.metrics[MetricTestsFailedFirstRun] = sum.failedFirstRun
			r.metrics[MetricTestsPassedAfterRerun] = sum.passedAfterRerun
			r.tools[ToolReportlog] = tools[ToolReportlog]
		}
	}
	if plan.reruns {
		// --reruns changed how the suite ran, so the plugin was used even
		// if the JSONL it enables turned out unreadable.
		r.tools[ToolRerunfailures] = tools[ToolRerunfailures]
	}
	if coverageBytes != nil {
		if bp, err := parseCoverageBP(coverageBytes); err == nil {
			r.metrics[MetricCoverageBP] = bp
			r.tools[ToolCoverage] = tools[ToolCoverage]
		}
	}
	// EP-6's rule, made structural: the gate sees the FIRST run. A suite
	// that only went green on a retry is a fail here, and its
	// pass-after-rerun survives as separately named, strictly weaker
	// evidence.
	if r.status == StatusPass && r.metrics[MetricTestsFailedFirstRun] > 0 {
		r.status = StatusFail
	}
	return r, nil
}

// readCapped reads one in-world artifact under the size cap. An absent file
// yields nil bytes and no note (absence is the record); an over-cap or
// unreadable file yields nil bytes and a note for the stderr artifact, so
// it is neither stored nor parsed and every metric it would have carried is
// absent.
func (o *pytestOracle) readCapped(hostDir, rel string) ([]byte, string) {
	p := filepath.Join(hostDir, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return nil, ""
	}
	if limit := o.artifactCap(); st.Size() > limit {
		return nil, fmt.Sprintf("mvo: oracle: %s exceeds the %d MiB artifact cap; not stored, metrics absent\n",
			rel, limit>>20)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Sprintf("mvo: oracle: %s: %v; not stored, metrics absent\n", rel, err)
	}
	return b, ""
}

func (o *pytestOracle) artifactCap() int64 {
	if o.cap > 0 {
		return o.cap
	}
	return artifactCapBytes
}
