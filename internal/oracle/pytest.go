package oracle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/coagente/multiverso/internal/oracle/pyplugin"
	"github.com/coagente/multiverso/internal/policy"
)

// oracleWorkRoot is the in-world directory the IN-TREE regime writes its
// native artifacts into (M1e behaviour, kept for operators who explicitly
// declare `evidence.regime: "in-tree"`). Under every other regime the
// cross-check files go to control-plane scratch instead, and nothing in
// the candidate's tree is read as a metric source after the process exits.
const oracleWorkRoot = ".mvo-oracle"

// artifactCapBytes bounds one stored artifact (EP-7). An over-cap file is
// neither stored nor parsed: the control plane must not be talked into
// buffering a gigabyte because a candidate looped a print statement.
const artifactCapBytes = 64 << 20

// Artifact kinds, in Result.Artifacts order. The order is fixed by kind
// with absent kinds skipped, and result.tools says which of the optional
// sources can be present — so the list is unambiguous without positional
// guessing. evidence-stream is inserted after the probe (M1f).
const (
	artStdout    = "stdout"
	artStderr    = "stderr"
	artProbe     = "tools-probe"
	artStream    = "evidence-stream"
	artJUnit     = "junit-xml"
	artReportlog = "reportlog"
	artCoverage  = "coverage-json"
)

// probeScript reports which structured sources are importable in the world
// (M1e decision 16). Absence is the record: a source missing from the
// output is a source whose metrics will be ABSENT from the receipt —
// never zero, never "assumed 100 %".
const probeScript = `import json,importlib.metadata as m
o={}
for n in ("pytest","coverage","pytest-reportlog","pytest-rerunfailures","hypothesis","mutmut","cosmic-ray"):
    try: o[n]=m.version(n)
    except Exception: pass
print(json.dumps(o,sort_keys=True))
`

// probeTools is the ordered list of sources the probe may report; anything
// else in its output is ignored (the probe runs in a world an agent wrote
// to, so its output is input, not truth).
//
// M2a extends it with the three distributions the new rungs need
// (decision 20). None of them is installed on the machine this was written
// on, so every degradation path they feed is the path this tree actually
// takes today rather than a hypothetical: a policy that requires one of
// them is refused at pre-flight, and an oracle run anyway records absent
// metrics with a named reason.
var probeTools = []string{ToolCosmicRay, ToolCoverage, ToolHypothesis, ToolMutmut,
	ToolPytest, ToolReportlog, ToolRerunfailures}

// Evidence-channel environment. The plugin reads exactly these two names
// and is inert without them.
const (
	envStream = "MVO_EVIDENCE_STREAM"
	envNonce  = "MVO_EVIDENCE_NONCE"
	envPyPath = "PYTHONPATH"
	// envNoAutoload is pytest's own switch for setuptools entry-point
	// plugin discovery. Set to 1, the ONLY plugins that load are the ones
	// named on argv — which is the whole seal (M1f-a decision 24).
	envNoAutoload = "PYTEST_DISABLE_PLUGIN_AUTOLOAD"
	// envSafePath is CPython's own switch for the sys.path[0] that `-m`
	// prepends: with it set, the working directory is NOT put on the import
	// path. It is applied to the mutation adapter's ENUMERATION steps and
	// to nothing else.
	//
	// The reason is a red-team finding. `python -m cosmic_ray.cli` runs with
	// cwd = the candidate's worktree, so a directory named `cosmic_ray/` in
	// the tree root shadows the installed distribution outright — and a
	// six-line `cosmic_ray/cli.py` whose `init` exits 0 and whose `dump`
	// prints nothing yields `mutants_candidates=0` and a vacuously passing
	// gate. No harness pattern was ever going to close that: the shadow is
	// an ordinary package directory named after a tool, matching no glob any
	// policy would think to write. Removing the capability beats naming the
	// file, which is the M1f lesson about entry-point plugins, restated one
	// rung along.
	//
	// It is deliberately NOT set on the mutant execution steps: cosmic-ray's
	// worker spawns the repository's own suite, which must import the
	// candidate's modules from the worktree. Interpreters older than 3.11
	// ignore the variable, so the enumeration contradiction check below is
	// the backstop that does not depend on the interpreter's version.
	envSafePath = "PYTHONSAFEPATH"
)

// Entry-point module names for the two optional plugins mvo itself
// requests. Under the autoload seal nothing loads by entry point, so a
// plugin the probe found must be named on argv explicitly or its flag
// would be an unknown option and the run would die with a usage error.
const (
	moduleReportlog = "pytest_reportlog.plugin"
	moduleReruns    = "pytest_rerunfailures"
)

// artifactStore is the CAS surface the pytest oracles write through. The
// concrete *cas.Store satisfies it; the narrow interface is what lets the
// EP-7 ordering test prove that every artifact reaches CAS BEFORE any of
// it is parsed.
type artifactStore interface {
	Put(b []byte) (string, error)
}

// evidencePlan is one oracle instance's resolved observation plumbing: the
// regime it will run under, the control-plane directories it owns, and the
// in-world paths those directories appear at.
type evidencePlan struct {
	regime     string // object.Regime*
	crosscheck string // policy.Crosscheck*
	autoload   string // policy.AutoloadOff | policy.AutoloadOn
	// hostEvidence/hostScratch are control-plane-owned host directories.
	// inWorldEvidence/inWorldScratch are where the candidate's process
	// sees them (identical on T0, /mvo/… on T1).
	hostEvidence, hostScratch     string
	inWorldEvidence, inWorldScrap string
	// pluginDir is the read-only directory holding mvo_evidence.py;
	// inWorldPlugin is its in-world path. pluginDigest is recorded in
	// every receipt the plugin produced.
	pluginDir, inWorldPlugin, pluginDigest string
	// workdir overrides the oracle's cwd (isolated: /work-ro).
	workdir string
}

// streaming reports whether metrics come from the stream rather than from
// files in the candidate's tree.
func (p evidencePlan) streaming() bool {
	return (p.regime == object.RegimeStreamed || p.regime == object.RegimeIsolated) &&
		p.hostEvidence != ""
}

// pytestOracle implements both Python rungs of the v0 ladder: O0
// (pytest-collect) and O1 (pytest-suite). They share a probe, an evidence
// channel, the artifact pipeline and the status mapping; they differ in
// argv and in what they cross-check.
type pytestOracle struct {
	kind     string
	spec     policy.Oracle
	store    artifactStore
	timeout  time.Duration
	baseline int64
	cap      int64 // artifactCapBytes in production; small in tests
	ev       evidencePlan
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

// workRel is the kind's in-world working directory under the in-tree
// regime, forward-slashed.
func (o *pytestOracle) workRel() string { return path.Join(oracleWorkRoot, o.kind) }

// junitPath, reportlogPath and coveragePath are where the cross-check
// files are written. Under a streaming regime they live in CONTROL-PLANE
// SCRATCH, never in the candidate's tree — the candidate can still author
// them, which is exactly why they are a cross-check and not a source
// (M1f decision 9).
func (o *pytestOracle) junitPath() string     { return o.crossPath("junit.xml") }
func (o *pytestOracle) reportlogPath() string { return o.crossPath("reportlog.jsonl") }
func (o *pytestOracle) coveragePath() string  { return o.crossPath("coverage.json") }

func (o *pytestOracle) crossPath(name string) string {
	if !o.ev.streaming() {
		return path.Join(o.workRel(), name)
	}
	// On T0 the in-world path IS the host path — nothing is mounted — so
	// an empty in-world scratch means "use the host directory", never
	// "fall back into the candidate's tree".
	scratch := o.ev.inWorldScrap
	if scratch == "" {
		scratch = filepath.ToSlash(o.ev.hostScratch)
	}
	if scratch == "" {
		return path.Join(o.workRel(), name)
	}
	return path.Join(scratch, name)
}

// hostCrossPath maps a cross-check file back to the host path the control
// plane reads it from.
func (o *pytestOracle) hostCrossPath(worldDir, name string) string {
	if o.ev.streaming() && o.ev.hostScratch != "" {
		return filepath.Join(o.ev.hostScratch, name)
	}
	return joinRel(worldDir, path.Join(o.workRel(), name))
}

// collectArgv is O0: pytest --collect-only -q -p no:cacheprovider, with
// the control plane's own plugin loaded FIRST by argv.
func (o *pytestOracle) collectArgv() []string {
	argv := append(o.prefix(), o.pluginFlags()...)
	argv = append(argv, "--collect-only", "-q", "-p", "no:cacheprovider")
	return append(argv, o.spec.Args...)
}

// pluginFlags loads the control-plane plugin ahead of conftest.py
// collection. `-p` plugins load at startup, so the plugin is registered
// before the candidate's first line runs. A conftest.py that unregisters
// it, or a pytest.ini addopts that adds `-p no:mvo_evidence`, produces a
// stream with no terminal record — a FAILED GATE, never a pass (M1f
// decision 2).
func (o *pytestOracle) pluginFlags() []string {
	if !o.ev.streaming() || o.ev.pluginDigest == "" {
		return nil
	}
	return []string{"-p", "mvo_evidence"}
}

// loadPlugin names one optional plugin on argv when the autoload seal is
// on. Under `plugin_autoload: "on"` the entry point loads it and naming it
// again would be redundant, so nothing is added.
func (o *pytestOracle) loadPlugin(module string) []string {
	if o.ev.autoload == policy.AutoloadOn {
		return nil
	}
	return []string{"-p", module}
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
// the run with a usage error. --report-log is requested only under the
// IN-TREE regime: under a streaming regime the stream subsumes it, and
// tests_failed_first_run no longer requires a plugin to be installed
// (M1f's metric derivation table).
func (o *pytestOracle) planSuite(tools map[string]string) suitePlan {
	var p suitePlan
	base := o.prefix()
	if o.spec.Coverage && tools[ToolCoverage] != "" && policy.CoverageWrappable(base) {
		base, p.coverage = wrapCoverage(base), true
	}
	argv := append(base, o.pluginFlags()...)
	argv = append(argv, "--junit-xml="+o.junitPath(), "-p", "no:cacheprovider")
	if !o.ev.streaming() && tools[ToolReportlog] != "" {
		argv = append(argv, o.loadPlugin(moduleReportlog)...)
		argv, p.reportlog = append(argv, "--report-log="+o.reportlogPath()), true
	}
	if o.spec.Reruns > 0 && tools[ToolRerunfailures] != "" && (p.reportlog || o.ev.streaming()) {
		argv = append(argv, o.loadPlugin(moduleReruns)...)
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
	return []string{o.python(), "-m", "coverage", "json", "-o", o.coveragePath()}
}

// Probe runs the tools probe in w and reports which structured sources are
// importable there, plus the raw probe stdout for storage. Pre-flight
// (M1e decision 15) uses it to refuse a race whose policy requires a
// pytest kind in an environment without pytest — a missing toolchain is
// machinery, never a failing candidate. python may be "" for the default
// interpreter.
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

// parseProbe reads the probe's JSON object, keeping only the four names
// the script asks about. Unparseable output yields an empty map.
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

// collectedRe and noCollectRe match pytest's --collect-only -q summary
// line ("8 tests collected in 0.01s", "1 test collected", "3 tests
// collected, 1 error in 0.05s", "no tests collected in 0.00s").
var (
	collectedRe = regexp.MustCompile(`^(\d+) tests? collected`)
	noCollectRe = regexp.MustCompile(`^no tests collected`)
)

// nodeIDSuffix ends a bare file node id ("tests/test_stats.py"); a node id
// naming a test carries "::".
const nodeIDSuffix = ".py"

// parseCollected extracts the collected-test count from --collect-only
// stdout: the LAST summary line wins, falling back to counting node-id
// lines when no summary line is present.
//
// Since M1f this is a CROSS-CHECK SOURCE ONLY (rule S3). It is stdout the
// candidate's process wrote, and the study's ninth vector walked past the
// one gate that worked by printing "8 tests collected in 0.01s" from an
// atexit hook. The number that reaches a gate comes from the stream.
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
	regime    string
	plugin    string
	notes     string
}

// Run implements Oracle: probe, open the channel, run, store, parse — in
// that order.
//
// A failing suite is evidence and comes back in the receipt with err nil.
// A non-nil error means the EVIDENCE could not be produced (a CAS write
// failed, the evidence channel could not be created): the caller records
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
	// Under the in-tree regime the worktree is still the write surface, so
	// M1e's host-side scrub stays: a planted junit.xml must never be read
	// as evidence. Under a streaming regime the scrub is not what defends
	// us — the file is not a metric source at all — but the directory is
	// still created so a stale one cannot be mistaken for this run's.
	if !o.ev.streaming() {
		hostWork := joinRel(w.Dir(), o.workRel())
		if err := os.RemoveAll(hostWork); err != nil {
			return object.Receipt{}, fmt.Errorf("oracle: %s: scrub %s: %w", o.kind, o.workRel(), err)
		}
		if err := os.MkdirAll(hostWork, 0o755); err != nil {
			return object.Receipt{}, fmt.Errorf("oracle: %s: create %s: %w", o.kind, o.workRel(), err)
		}
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

	r, err := o.runKind(runCtx, w, tools, probeKey)
	if err != nil {
		return object.Receipt{}, err
	}

	return object.Receipt{
		Schema: object.SchemaReceipt,
		Oracle: object.OracleRef{ID: o.kind, Version: o.Version(), Config: o.spec.Config},
		Execution: object.Execution{
			Argv:           r.argv,
			ExitCode:       r.proc.ExitCode,
			DurationMS:     r.proc.WallMS,
			IsolationTier:  w.Tier(),
			IsolationCaps:  w.Caps(),
			EvidenceRegime: r.regime,
			EvidencePlugin: r.plugin,
		},
		Result: object.Result{
			Status:    r.status,
			Metrics:   r.metrics,
			Tools:     r.tools,
			Detail:    "",
			Artifacts: r.artifacts,
		},
		Freshness:   object.Freshness{Basis: object.BasisConstruction},
		RecheckTier: recheckTier,
		Family:      Family(o.kind),
		// The receipt's cost is the WHOLE oracle: probe, run and report
		// extraction. execution.duration_ms is the verdict-producing
		// process alone.
		//
		// Units is the scaling denominator that makes wall_ms learnable
		// (M2a decision 22): the fixed cost of anything that imports
		// pytest is ~400 ms on this tree and it DWARFS the tests, so a
		// scheduler modelling oracle cost as "test time" is wrong by an
		// order of magnitude on small repositories and right on large
		// ones. Recording the count is what lets M2b fit
		// wall_ms ≈ fixed + per_unit × units per repository instead.
		// Absent (0, "") when the run produced no count at all — the same
		// honest absence the metrics carry.
		Cost:        o.cost(time.Since(start).Milliseconds(), r.metrics),
		Inputs:      object.NoInputs(),
		Correlation: policy.KindCorrelation(o.kind),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// cost pairs a run's wall time with the count its kind scales by:
// collected tests for O0, executed tests for O1, and PROPERTY CASES for
// O2p.
//
// The per-kind denominator is a switch rather than a default because the
// default was wrong for one kind and silently so. O2p's vocabulary is
// `properties_*`, `property_cases_*`, `duration_ms` and `coverage_bp` — it
// never emits `tests_total`, so a lookup of `tests_total` always missed and
// every property receipt recorded `{0, ""}`. That sentinel means "unknown",
// which made `mvo oracles` unable to fit the kind forever ("units do not
// vary: every receipt scaled 0") and left M2b without a denominator for one
// of the four new rungs, while `internal/policy/profile.go` declared the
// kind's unit as `cases` and the menu declared its marginal cost as
// "cases × per-case".
//
// `property_cases_total` is itself honestly ABSENT under the JSONL fallback
// (decision 15), so `{0, ""}` still happens — for the right reason, and one
// a reader can check against `result.tools["hypothesis-observability"]`.
func (o *pytestOracle) cost(wallMS int64, metrics map[string]int64) object.Cost {
	name := MetricTestsTotal
	switch o.kind {
	case KindPytestCollect:
		name = MetricCollectedTotal
	case KindProperties:
		name = policy.MetricPropertyCasesTotal
	}
	units, ok := metrics[name]
	if !ok {
		return object.Cost{WallMS: wallMS}
	}
	return sizedCost(wallMS, units, policy.KindUnit(o.kind))
}

// runKind opens the evidence channel around the kind's own run. The
// channel is created BEFORE the process is spawned and torn down after it
// exits; whatever arrived is what there is.
func (o *pytestOracle) runKind(ctx context.Context, w backend.World,
	tools map[string]string, probeKey string) (runOutcome, error) {
	switch o.kind {
	case KindPytestCollect, KindPytestSuite, KindProperties:
	default:
		// Unreachable: New refused every other kind.
		return runOutcome{}, fmt.Errorf("oracle: unknown kind %q", o.kind)
	}
	if o.ev.hostScratch != "" {
		if err := ensureDir(o.ev.hostScratch, 0o777); err != nil {
			return runOutcome{}, fmt.Errorf("oracle: %s: scratch dir: %w", o.kind, err)
		}
	}
	var ch *evidenceChannel
	nonce := ""
	if o.ev.streaming() && o.ev.hostEvidence != "" {
		var err error
		if nonce, err = newNonce(); err != nil {
			return runOutcome{}, fmt.Errorf("oracle: %s: %w", o.kind, err)
		}
		if err := ensureDir(o.ev.hostEvidence, 0o755); err != nil {
			return runOutcome{}, fmt.Errorf("oracle: %s: evidence dir: %w", o.kind, err)
		}
		if ch, err = openEvidenceChannel(o.ev.hostEvidence, nonce, o.artifactCap()); err != nil {
			return runOutcome{}, err
		}
		// However this returns, the channel is torn down: a FIFO whose
		// reader outlives the run is a channel a later run could be talked
		// into reading.
		defer ch.Close()
	}
	switch o.kind {
	case KindPytestSuite:
		return o.finishSuite(ctx, w, tools, probeKey, o.planSuite(tools), ch, nonce)
	case KindProperties:
		return o.finishProperties(ctx, w, tools, probeKey, o.propertiesPlan(tools), ch, nonce)
	}
	return o.finishCollect(ctx, w, tools, probeKey, o.collectArgv(), ch, nonce)
}

// streamEnv is the in-world environment for one run: the entry-point
// autoload seal, and — when a channel exists — the plugin's PYTHONPATH,
// stream path and nonce.
//
// The seal is unconditional on the policy, not on the channel: a run with
// no stream is still a run whose collected count and exit code reach a
// gate, and an entry-point plugin can author both.
//
// PYTHONPATH is PREPENDED, never assigned over. The plugin directory must
// come first (it is read-only and outside the candidate's tree), but an
// operator's PYTHONPATH is how a src-layout repo, an uninstalled package
// or a virtualenv-less checkout finds its own code: clobbering it made
// honest repositories collect zero tests on the very first race a new user
// runs. os/exec keeps the LAST occurrence of a duplicated key, so the
// assignment silently won.
func (o *pytestOracle) streamEnv(streamPath, nonce string) []string {
	return evidenceEnv(o.ev, streamPath, nonce)
}

// prependPath puts dir at the head of a PATH-list value, dropping empties
// so the result never carries a bare separator (an empty entry is "the
// current directory" to CPython, which is the candidate's tree).
func prependPath(dir, rest string) string {
	switch {
	case dir == "":
		return rest
	case rest == "":
		return dir
	}
	return dir + string(os.PathListSeparator) + rest
}

// inWorldStream is where the plugin writes: the host FIFO path on T0, the
// mounted path on T1.
func (o *pytestOracle) inWorldStream() string {
	if o.ev.inWorldEvidence != "" {
		return path.Join(o.ev.inWorldEvidence, streamFile)
	}
	return filepath.ToSlash(filepath.Join(o.ev.hostEvidence, streamFile))
}

// finishCollect is O0: the collected-test count from the STREAM, the
// exit-5 rule, and the collect-only stdout cross-check (S3).
func (o *pytestOracle) finishCollect(ctx context.Context, w backend.World, tools map[string]string,
	probeKey string, argv []string, ch *evidenceChannel, nonce string) (runOutcome, error) {
	r := runOutcome{
		argv:    argv,
		metrics: map[string]int64{},
		tools:   map[string]string{},
		regime:  o.ev.regime,
	}
	if ch != nil {
		r.plugin = o.ev.pluginDigest
	}
	r.proc = runInWorld(ctx, w, argv, o.worldEnv(w, o.streamEnv(o.inWorldStream(), nonce)))
	r.status = r.proc.Status

	stream := o.closeStream(ch, nonce)
	streamKey := ""
	if ch != nil {
		var err error
		// EP-7 order, unchanged: the RAW stream bytes reach CAS before
		// anything is parsed.
		streamKey, err = o.store.Put(stream.Raw)
		if err != nil {
			return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStream, err)
		}
	}

	// The verdict is computed BEFORE the stderr artifact is written, so a
	// disagreement reason lands in the same stored bytes an operator reads
	// (the M1b/M1c control-plane-note precedent). Nothing is parsed until
	// the raw stream is already in CAS (EP-7 order, unchanged).
	if v := tools[ToolPytest]; v != "" {
		r.tools[ToolPytest] = v
	}
	reason := o.collectVerdict(&r, stream, ch != nil)

	stdoutKey, err := o.store.Put(r.proc.Stdout)
	if err != nil {
		return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStdout, err)
	}
	notes := stream.Notes
	if ch != nil && !stream.Usable {
		notes += "mvo: oracle: " + stream.Reason + "\n"
	}
	if reason != "" {
		notes += "mvo: oracle: " + reason + "\n"
		r.notes = reason
	}
	stderrKey, err := o.store.Put(append(append([]byte(nil), r.proc.Stderr...), notes...))
	if err != nil {
		return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStderr, err)
	}
	r.artifacts = []string{stdoutKey, stderrKey, probeKey}
	if streamKey != "" {
		r.artifacts = append(r.artifacts, streamKey)
	}
	return r, nil
}

// collectVerdict applies the collect rung's rules and returns the reason it
// recorded, or "" when the run was consistent.
//
// The count that reaches a gate comes from the STREAM. The --collect-only
// summary line survives only as a cross-check: it is stdout the
// candidate's process wrote, and the study's ninth vector walked past the
// one gate that worked by printing "8 tests collected in 0.01s" from an
// atexit hook.
func (o *pytestOracle) collectVerdict(r *runOutcome, stream Stream, streamed bool) string {
	stdoutTotal, stdoutKnown := parseCollected(r.proc.Stdout)
	if !streamed {
		// The in-tree regime: M1e's behaviour, byte-for-byte.
		total, known := stdoutTotal, stdoutKnown
		if r.proc.ExitCode == 5 {
			total, known = 0, true
		}
		o.recordCollected(r, total, known)
		return ""
	}
	if !stream.Usable {
		// S1 — absence never passes. No usable stream ⇒ every
		// stream-derived metric is absent, and a process that exited 0
		// with no usable stream is `error`, never `pass`. The cheapest
		// attack on a streaming oracle — unregister the plugin — buys a
		// failed gate, not a green one.
		if r.proc.ExitCode == 0 {
			r.status = StatusError
		}
		return ""
	}
	r.tools[ToolEvidence] = StreamVersion
	total := int64(0)
	if stream.HasCollected {
		total = stream.Collected
	}
	if r.proc.ExitCode == 5 {
		// M1e decision 14, preserved: exit 5 is "no tests collected". The
		// count is recorded EXPLICITLY as 0 so collect-nonempty fails with
		// a reason rather than for want of a metric — and a stream
		// claiming otherwise is a disagreement, not a correction.
		o.recordCollected(r, 0, true)
		if stream.HasCollected && stream.Collected != 0 {
			r.status = StatusError
			return fmt.Sprintf("exit_code=5 (no tests collected) but the evidence stream reports collected=%d",
				stream.Collected)
		}
		return ""
	}
	if !stream.HasCollected {
		return "" // absent, honestly
	}
	o.recordCollected(r, total, true)
	// S3 — the second source must agree.
	if o.ev.crosscheck == policy.CrosscheckRequire && stdoutKnown && stdoutTotal != total {
		r.status = StatusError
		return fmt.Sprintf("collect-only stdout reports %d collected, the evidence stream reports %d",
			stdoutTotal, total)
	}
	return ""
}

// recordCollected writes the collect metrics, including the baseline delta
// when a denominator was measured (M1e decision 13).
func (o *pytestOracle) recordCollected(r *runOutcome, total int64, known bool) {
	if !known {
		return
	}
	r.metrics[MetricCollectedTotal] = total
	if o.baseline > 0 {
		r.metrics[MetricCollectedBase] = o.baseline
		r.metrics[MetricCollectedDelta] = total - o.baseline
	}
}

// finishSuite is O1: the suite gate, its stream, its cross-check sources,
// and the S1/S2/S3 rules.
func (o *pytestOracle) finishSuite(ctx context.Context, w backend.World, tools map[string]string,
	probeKey string, plan suitePlan, ch *evidenceChannel, nonce string) (runOutcome, error) {
	r := runOutcome{
		argv:    plan.argv,
		metrics: map[string]int64{},
		tools:   map[string]string{},
		regime:  o.ev.regime,
	}
	if ch != nil {
		r.plugin = o.ev.pluginDigest
	}
	r.proc = runInWorld(ctx, w, plan.argv, o.worldEnv(w, o.streamEnv(o.inWorldStream(), nonce)))
	r.status = r.proc.Status

	// Report extraction, never verification: whatever it does, the
	// receipt's status and exit code are the suite's.
	coverageExtracted := false
	if plan.coverage {
		coverageExtracted = runInWorld(ctx, w, o.coverageJSONArgv(), o.worldEnv(w, nil)).Status == StatusPass
	}

	stream := o.closeStream(ch, nonce)

	notes := stream.Notes
	junitBytes, note := o.readCapped(o.hostCrossPath(w.Dir(), "junit.xml"), o.junitPath())
	notes += note
	var reportlogBytes, coverageBytes []byte
	if plan.reportlog {
		reportlogBytes, note = o.readCapped(o.hostCrossPath(w.Dir(), "reportlog.jsonl"), o.reportlogPath())
		notes += note
	}
	if coverageExtracted {
		coverageBytes, note = o.readCapped(o.hostCrossPath(w.Dir(), "coverage.json"), o.coveragePath())
		notes += note
	}

	stdoutKey, err := o.store.Put(r.proc.Stdout)
	if err != nil {
		return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStdout, err)
	}
	streamKey := ""
	if ch != nil {
		if streamKey, err = o.store.Put(stream.Raw); err != nil {
			return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStream, err)
		}
		if !stream.Usable {
			notes += "mvo: oracle: " + stream.Reason + "\n"
		}
	}

	// Parse the JUnit cross-check before the stderr artifact is written so
	// a disagreement reason lands in the SAME stored bytes an operator
	// reads (the M1b/M1c control-plane-note precedent).
	var junit *junitSummary
	if junitBytes != nil {
		if sum, err := parseJUnit(junitBytes); err == nil {
			junit = &sum
		}
	}
	reason := ""
	if ch != nil {
		reason = o.suiteVerdict(&r, stream, junit)
	}
	if reason != "" {
		notes += "mvo: oracle: " + reason + "\n"
		r.notes = reason
	}

	stderrKey, err := o.store.Put(append(append([]byte(nil), r.proc.Stderr...), notes...))
	if err != nil {
		return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStderr, err)
	}
	r.artifacts = []string{stdoutKey, stderrKey, probeKey}
	if streamKey != "" {
		r.artifacts = append(r.artifacts, streamKey)
	}
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

	if v := tools[ToolPytest]; v != "" {
		r.tools[ToolPytest] = v
	}
	if plan.reruns {
		r.tools[ToolRerunfailures] = tools[ToolRerunfailures]
	}
	if coverageBytes != nil {
		if bp, err := parseCoverageBP(coverageBytes); err == nil {
			// coverage_bp comes from a data file the TEST PROCESS writes.
			// It is candidate-influenceable under every regime and there
			// is no cheap fix; it stays in the vocabulary, stays out of
			// the default policy, and is labelled rather than banned
			// (M1f decision 12).
			r.metrics[MetricCoverageBP] = bp
			r.tools[ToolCoverage] = tools[ToolCoverage]
		}
	}

	if ch == nil {
		// The in-tree regime: M1e's parse path, byte-for-byte.
		if junit != nil {
			r.metrics[MetricTestsTotal] = junit.total
			r.metrics[MetricTestsPassed] = junit.passed()
			r.metrics[MetricTestsFailed] = junit.failures
			r.metrics[MetricTestsErrored] = junit.errors
			r.metrics[MetricTestsSkipped] = junit.skipped
			r.metrics[MetricDurationMS] = junit.durationMS
		}
		if reportlogBytes != nil {
			if sum, err := parseReportlog(reportlogBytes); err == nil {
				r.metrics[MetricTestsFailedFirstRun] = sum.failedFirstRun
				r.metrics[MetricTestsPassedAfterRerun] = sum.passedAfterRerun
				r.tools[ToolReportlog] = tools[ToolReportlog]
			}
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

// suiteVerdict applies S1, S2 and S3 in that order and returns the reason
// it recorded, or "" when the run was consistent. Metrics are written here
// too: they exist only when the stream is usable.
func (o *pytestOracle) suiteVerdict(r *runOutcome, stream Stream, junit *junitSummary) string {
	if !stream.Usable {
		// S1 — absence never passes.
		if r.proc.ExitCode == 0 {
			r.status = StatusError
			return fmt.Sprintf("no usable evidence stream (%s)", stream.Reason)
		}
		// The process said it failed and there is nothing to contradict.
		r.status = StatusFail
		return ""
	}
	r.tools[ToolEvidence] = StreamVersion
	r.metrics[MetricTestsTotal] = stream.Total
	r.metrics[MetricTestsPassed] = stream.Passed
	r.metrics[MetricTestsFailed] = stream.Failed
	r.metrics[MetricTestsErrored] = stream.Errored
	r.metrics[MetricTestsSkipped] = stream.Skipped
	r.metrics[MetricDurationMS] = stream.DurationMS
	r.metrics[MetricTestsFailedFirstRun] = stream.FailedFirstRun
	r.metrics[MetricTestsPassedAfterRerun] = stream.PassedAfterRun

	// S2 — the exit code is cross-examined, not trusted. This is the whole
	// of vector 2: an atexit handler calling os._exit(0) against a stream
	// that already reported two failures.
	if r.proc.ExitCode == 0 && (stream.Failed > 0 || stream.Errored > 0) {
		r.status = StatusError
		return fmt.Sprintf("exit_code=0 but the evidence stream reports failed=%d errored=%d",
			stream.Failed, stream.Errored)
	}
	if stream.ExitStatus != r.proc.ExitCode {
		r.status = StatusError
		return fmt.Sprintf("evidence stream reports exitstatus=%d but the process exited %d",
			stream.ExitStatus, r.proc.ExitCode)
	}
	// S3 — the second source must agree. Honest runs agree; when they do
	// not, either someone forged one of them or a pytest/plugin version
	// changed a counting rule, and mvo cannot tell which. `error` is the
	// honest verdict: it fails every gate while
	// on_all_worlds_failed_machinery routes it to a human.
	if o.ev.crosscheck == policy.CrosscheckRequire && junit != nil {
		if junit.total != stream.Total || junit.failures != stream.Failed ||
			junit.errors != stream.Errored || junit.skipped != stream.Skipped {
			r.status = StatusError
			return fmt.Sprintf(
				"junit-xml and the evidence stream disagree: junit(total=%d,failed=%d,errored=%d,skipped=%d) stream(total=%d,failed=%d,errored=%d,skipped=%d)",
				junit.total, junit.failures, junit.errors, junit.skipped,
				stream.Total, stream.Failed, stream.Errored, stream.Skipped)
		}
	}
	return ""
}

// closeStream tears the channel down and parses whatever arrived. A nil
// channel is the in-tree regime: no stream, and the caller says so.
func (o *pytestOracle) closeStream(ch *evidenceChannel, nonce string) Stream {
	if ch == nil {
		return Stream{}
	}
	raw := ch.Close()
	return ParseStream(raw, nonce, ch.Over())
}

// worldEnv builds the in-world environment. On T0 the process inherits the
// host environment plus our additions (nil extras keeps M0/M1b's exact
// inherit-everything behaviour); on T1 only the additions travel, because
// the image owns PATH and the backend maps names to values client-side.
//
// On T0 our PYTHONPATH addition is MERGED with the operator's rather than
// assigned over it: os/exec deduplicates env keys keeping the last, so a
// bare assignment silently discarded the ambient value and a src-layout
// repo — or any repo whose tests import through PYTHONPATH — collected
// zero tests on the first race a new user ever ran. On T1 nothing is
// inherited by construction (the image owns its environment exactly as it
// owns PATH), which is stated in the contract rather than papered over.
func (o *pytestOracle) worldEnv(w backend.World, extra []string) []string {
	return mergeWorldEnv(w, extra)
}

// newNonce is the 32-hex identity tag in the stream header (M1f decision
// 15). It proves the stream belongs to the run we started, defeating a
// replayed stream left in a tree. It authenticates NOTHING against an
// in-process adversary, and is documented as exactly that: a MAC would
// need a key the plugin can read, in a process the adversary controls,
// from before the first test reports.
func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("evidence nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// readCapped reads one cross-check file under the size cap. An absent file
// yields nil bytes and no note (absence is the record); an over-cap or
// unreadable file yields nil bytes and a note for the stderr artifact.
func (o *pytestOracle) readCapped(hostPath, label string) ([]byte, string) {
	st, err := os.Stat(hostPath)
	if err != nil || st.IsDir() {
		return nil, ""
	}
	if limit := o.artifactCap(); st.Size() > limit {
		return nil, fmt.Sprintf("mvo: oracle: %s exceeds the %d MiB artifact cap; not stored, metrics absent\n",
			label, limit>>20)
	}
	b, err := os.ReadFile(hostPath)
	if err != nil {
		return nil, fmt.Sprintf("mvo: oracle: %s: %v; not stored, metrics absent\n", label, err)
	}
	return b, ""
}

func (o *pytestOracle) artifactCap() int64 {
	if o.cap > 0 {
		return o.cap
	}
	return artifactCapBytes
}

// MaterializePlugin writes the embedded plugin into dir/<digest>/ (mode
// 0444 in a 0555 directory) and returns the directory holding it. It is
// idempotent: the digest names the content, so a second call over the same
// bytes is a no-op.
func MaterializePlugin(root string) (dir, digest string, err error) {
	digest = pyplugin.Digest
	dir = filepath.Join(root, strings.ReplaceAll(digest, ":", "-"))
	file := filepath.Join(dir, pyplugin.Filename)
	if b, err := os.ReadFile(file); err == nil && object.CASKeyBytes(b) == digest {
		return dir, digest, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("oracle: materialize plugin: %w", err)
	}
	// A previous run sealed the directory at 0555; re-open it for the
	// write and seal it again below.
	if err := os.Chmod(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("oracle: materialize plugin: %w", err)
	}
	// Written through a temp file and renamed: two racing worlds must
	// never see a half-written observer.
	tmp, err := os.CreateTemp(dir, ".mvo_evidence-")
	if err != nil {
		return "", "", fmt.Errorf("oracle: materialize plugin: %w", err)
	}
	if _, err := tmp.Write(pyplugin.Source); err != nil {
		_ = tmp.Close()
		return "", "", fmt.Errorf("oracle: materialize plugin: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", "", fmt.Errorf("oracle: materialize plugin: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o444); err != nil {
		return "", "", fmt.Errorf("oracle: materialize plugin: %w", err)
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		return "", "", fmt.Errorf("oracle: materialize plugin: %w", err)
	}
	// The FILE is 0444; the directory stays 0755. Sealing the directory
	// would only make the workspace undeletable: on T0 everything runs as
	// one uid, so no file mode is a seal there, and under `isolated` the
	// real seal is the read-only bind mount at /mvo/plugin plus the
	// distinct oracle uid — an OS guarantee rather than an inference.
	return dir, digest, nil
}

// PluginDigest is the embedded observer's content address — the value
// every streamed receipt records in execution.evidence_plugin.
func PluginDigest() string { return pyplugin.Digest }

// PluginSource is the embedded observer's exact bytes, for the CAS put
// that makes every recorded evidence_plugin digest resolvable.
func PluginSource() []byte { return append([]byte(nil), pyplugin.Source...) }
