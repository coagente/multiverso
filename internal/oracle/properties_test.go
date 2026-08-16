package oracle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// ---------------------------------------------------------------------------
// Pure: the observability parser, and the rule that its output is not a
// metric.
// ---------------------------------------------------------------------------

func readOracleFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "oracle", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func TestParseObservability(t *testing.T) {
	sum, err := ParseObservability(readOracleFixture(t, "hypothesis-observed.jsonl"))
	if err != nil {
		t.Fatalf("ParseObservability: %v", err)
	}
	if sum.Cases != 4 {
		t.Errorf("cases = %d, want 4 (the info record is not an example)", sum.Cases)
	}
	// invalid and gave_up are both "the search rejected the draw" — the
	// honest-degradation signal PBT usually hides.
	if sum.Invalid != 2 {
		t.Errorf("invalid = %d, want 2", sum.Invalid)
	}
}

// A shape we do not recognize yields an ERROR, never a fabricated zero: a
// parser that silently returned "0 cases" for a renamed record would be
// indistinguishable from a property suite that searched nothing.
func TestParseObservabilityUnknownShapeIsAbsence(t *testing.T) {
	sum, err := ParseObservability(readOracleFixture(t, "hypothesis-observed-unknown.jsonl"))
	if err == nil {
		t.Fatalf("unknown shape parsed to %+v, want an error", sum)
	}
	if sum.Cases != 0 {
		t.Errorf("cases = %d, want none", sum.Cases)
	}
}

// ---------------------------------------------------------------------------
// The oracle. No hypothesis is installed here, so both provenance paths are
// driven by a fake interpreter.
// ---------------------------------------------------------------------------

// fakeProperties is a stand-in interpreter for the property rung: it
// answers the tools probe, writes an evidence stream, and optionally writes
// Hypothesis's own JSONL into the directory the run was pointed at.
type fakeProperties struct {
	probe map[string]string
	// stream is the record body (the header is written with THIS run's
	// nonce, so a replayed stream is never what the test asserts on).
	stream string
	// observed is the JSONL Hypothesis would write on its own. It lands in
	// <HYPOTHESIS_EXPERIMENTAL_OBSERVABILITY>/observed/, which is
	// control-plane scratch — writable by the run, and therefore never a
	// metric source.
	observed string
	exit     int
}

func (f fakeProperties) write(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	probeJSON := "{}"
	if f.probe != nil {
		b, err := json.Marshal(f.probe)
		if err != nil {
			t.Fatal(err)
		}
		probeJSON = string(b)
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# fake python3 for the property oracle's tests.\n")
	b.WriteString("if [ \"$1\" = \"-c\" ]; then\ncat <<'MVO_PROBE'\n" + probeJSON + "\nMVO_PROBE\nexit 0\nfi\n")
	if f.stream != "" {
		b.WriteString("if [ -n \"$MVO_EVIDENCE_STREAM\" ]; then\n")
		b.WriteString("printf 'mvo-evidence/v0\\t%s\\n' \"$MVO_EVIDENCE_NONCE\" >> \"$MVO_EVIDENCE_STREAM\"\n")
		b.WriteString("cat >> \"$MVO_EVIDENCE_STREAM\" <<'MVO_STREAM'\n" + f.stream + "\nMVO_STREAM\nfi\n")
	}
	if f.observed != "" {
		b.WriteString("if [ -n \"$HYPOTHESIS_EXPERIMENTAL_OBSERVABILITY\" ]; then\n")
		b.WriteString("mkdir -p \"$HYPOTHESIS_EXPERIMENTAL_OBSERVABILITY/observed\"\n")
		b.WriteString("cat > \"$HYPOTHESIS_EXPERIMENTAL_OBSERVABILITY/observed/run.jsonl\" <<'MVO_OBS'\n" + f.observed + "\nMVO_OBS\nfi\n")
	}
	fmt.Fprintf(&b, "exit %d\n", f.exit)
	path := filepath.Join(dir, "python3")
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// propertiesInstance wires the rung the way the orchestrator does.
func propertiesInstance(t *testing.T, python string) *pytestOracle {
	t.Helper()
	root := t.TempDir()
	return &pytestOracle{
		kind: KindProperties,
		spec: policy.Oracle{
			Name: "props", Kind: KindProperties, Family: policy.FamilyProperty,
			Config: "mv0:" + strings.Repeat("7", 64),
			Argv:   []string{python, "-m", "pytest"},
			Args:   []string{},
			Corpus: object.CorpusSpec{Provider: policy.ProviderHypothesis, Module: "props/mvo_props.py"},
		},
		store:   newStore(t),
		timeout: 30 * time.Second,
		cap:     artifactCapBytes,
		ev: evidencePlan{
			regime:       object.RegimeStreamed,
			crosscheck:   policy.CrosscheckRequire,
			autoload:     policy.AutoloadOff,
			hostEvidence: filepath.Join(root, "evidence"),
			hostScratch:  filepath.Join(root, "scratch"),
			pluginDir:    filepath.Join(root, "plugin"),
			pluginDigest: "sha256:" + strings.Repeat("b", 64),
		},
	}
}

const streamTwoPropertiesFourCases = `1	session_start	{"pid":7}
2	property_case	{"property":"tests/test_props.py::test_clamp","status":"passed"}
3	property_case	{"property":"tests/test_props.py::test_clamp","status":"passed"}
4	property_case	{"property":"tests/test_props.py::test_clamp","status":"invalid"}
5	property_case	{"property":"props/mvo_props.py::test_mean","status":"gave_up"}
6	test	{"nodeid":"tests/test_props.py::test_clamp","outcome":"passed"}
7	test	{"nodeid":"props/mvo_props.py::test_mean","outcome":"passed"}
8	session_finish	{"duration_ms":41,"errored":0,"exitstatus":0,"failed":0,"passed":2,"skipped":0,"total":2}`

// The STREAM path: the observability callback forwarded every example onto
// the control plane's channel, so property_cases_* are metrics and
// result.tools says which path produced them.
func TestPropertiesCasesFromTheStreamAreMetrics(t *testing.T) {
	py := fakeProperties{
		probe:  map[string]string{"pytest": "8.4.0", "hypothesis": "6.100.0"},
		stream: streamTwoPropertiesFourCases,
	}.write(t)
	o := propertiesInstance(t, py)

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusPass {
		t.Fatalf("status = %s", rec.Result.Status)
	}
	m := rec.Result.Metrics
	if m[MetricPropertiesTotal] != 2 || m[MetricPropertiesPassed] != 2 {
		t.Errorf("properties total/passed = %d/%d, want 2/2", m[MetricPropertiesTotal], m[MetricPropertiesPassed])
	}
	if m[MetricPropertyCasesTotal] != 4 {
		t.Errorf("property_cases_total = %d, want 4", m[MetricPropertyCasesTotal])
	}
	// invalid + gave_up: the property that searched nothing is visible.
	if m[MetricPropertyCasesInvalid] != 2 {
		t.Errorf("property_cases_invalid = %d, want 2", m[MetricPropertyCasesInvalid])
	}
	if got := rec.Result.Tools[ToolObservability]; got != ToolObsStream {
		t.Errorf("tools[%s] = %q, want %q", ToolObservability, got, ToolObsStream)
	}
	// A property IS a test node id; an EXAMPLE is not. Conflating them is
	// how three properties that searched nothing read like three hundred
	// examples.
	if m[MetricPropertiesTotal] == m[MetricPropertyCasesTotal] {
		t.Error("properties_total and property_cases_total are the same number: the two are being conflated")
	}
	gate := policy.Gate{Predicate: policy.GatePropertyCasesAtLeast, Basis: object.BasisConstruction, Threshold: 4}
	if ok, reason := gate.Eval(&rec); !ok {
		t.Errorf("property-cases-at-least(4) failed: %s", reason)
	}
	strict := policy.Gate{Predicate: policy.GatePropertyCasesAtLeast, Basis: object.BasisConstruction, Threshold: 5}
	if ok, reason := strict.Eval(&rec); ok || !strings.Contains(reason, "property_cases_total=4") {
		t.Errorf("property-cases-at-least(5) = (%v, %q), want a failure quoting the count", ok, reason)
	}
	// THE SCALING DENOMINATOR (M2a decision 22). cost() used to look up
	// `tests_total` for every kind, which the property rung never emits — its
	// vocabulary is properties_*, property_cases_*, duration_ms and
	// coverage_bp — so the lookup always missed and every O2p receipt
	// recorded the {0, ""} sentinel for UNKNOWN. `mvo oracles` could then
	// never fit the kind, and M2b had no denominator for one of the four new
	// rungs, while internal/policy/profile.go declared its unit as `cases`.
	if rec.Cost.Unit != policy.UnitCases || rec.Cost.Units != 4 {
		t.Errorf("cost = %+v, want 4 %s: the property rung scales by CASES, not by test nodes",
			rec.Cost, policy.UnitCases)
	}
}

// THE DEGRADED PATH — and it is the live path on this machine, where
// hypothesis is not installed at all. The observability callback could not
// be registered, so no per-case record reached the stream; Hypothesis's own
// JSONL is stored as an ARTIFACT, result.tools says `jsonl`, and
// property_cases_* are ABSENT. Never present with `jsonl`.
func TestPropertiesJSONLFallbackYieldsAbsentMetrics(t *testing.T) {
	py := fakeProperties{
		probe: map[string]string{"pytest": "8.4.0"},
		stream: `1	session_start	{"pid":7}
2	test	{"nodeid":"tests/test_props.py::test_clamp","outcome":"passed"}
3	session_finish	{"duration_ms":12,"errored":0,"exitstatus":0,"failed":0,"passed":1,"skipped":0,"total":1}`,
		observed: string(readOracleFixture(t, "hypothesis-observed.jsonl")),
	}.write(t)
	o := propertiesInstance(t, py)

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusPass {
		t.Fatalf("status = %s", rec.Result.Status)
	}
	// The verdict-level metrics still exist: they came from the stream.
	if rec.Result.Metrics[MetricPropertiesTotal] != 1 {
		t.Errorf("properties_total = %v, want 1", rec.Result.Metrics[MetricPropertiesTotal])
	}
	for _, m := range []string{MetricPropertyCasesTotal, MetricPropertyCasesInvalid} {
		if v, ok := rec.Result.Metrics[m]; ok {
			t.Errorf("%s = %d is PRESENT under the JSONL fallback; those records are candidate-authorable after exit", m, v)
		}
	}
	if got := rec.Result.Tools[ToolObservability]; got != ToolObsJSONL {
		t.Errorf("tools[%s] = %q, want %q", ToolObservability, got, ToolObsJSONL)
	}
	// The records are not thrown away — they are stored, unread, so a human
	// can look at what the search did.
	if len(rec.Result.Artifacts) < 5 {
		t.Fatalf("artifacts = %v, want the JSONL stored alongside the rest", rec.Result.Artifacts)
	}
	stored := string(loadBytes(t, o.store, rec.Result.Artifacts[len(rec.Result.Artifacts)-1]))
	if !strings.Contains(stored, "test_case") {
		t.Errorf("the stored artifact is not the observability JSONL:\n%s", stored)
	}
	// And the gate over the absent metric FAILS rather than passing on a
	// number nobody can trust.
	gate := policy.Gate{Predicate: policy.GatePropertyCasesAtLeast, Basis: object.BasisConstruction, Threshold: 1}
	if ok, reason := gate.Eval(&rec); ok || !strings.Contains(reason, "absent") {
		t.Errorf("gate = (%v, %q), want a failure naming the absent metric", ok, reason)
	}
	if notes := storedNotes(t, o.store, rec); !strings.Contains(notes, "ABSENT") {
		t.Errorf("stderr artifact does not explain the fallback:\n%s", notes)
	}
	// And the scaling denominator is honestly UNKNOWN here, for the right
	// reason: the count it would scale by is the one that is absent.
	// object.Cost's invariant is `Unit == "" iff Units == 0`, and {0, ""} is
	// decision 22's sentinel — so a reader can tell "this rung scaled by
	// nothing" from "we do not know what this rung scaled by".
	if rec.Cost.Units != 0 || rec.Cost.Unit != "" {
		t.Errorf("cost = %+v, want the {0, \"\"} unknown sentinel under the JSONL fallback", rec.Cost)
	}
}

// A run with no observability at all — no callback, no JSONL — claims
// NOTHING: result.tools carries no observability key, so a reader never has
// to guess which of the two paths produced an absence.
func TestPropertiesNoObservabilityClaimsNothing(t *testing.T) {
	py := fakeProperties{
		probe: map[string]string{"pytest": "8.4.0"},
		stream: `1	session_start	{"pid":7}
2	test	{"nodeid":"t.py::p","outcome":"passed"}
3	session_finish	{"duration_ms":5,"errored":0,"exitstatus":0,"failed":0,"passed":1,"skipped":0,"total":1}`,
	}.write(t)
	o := propertiesInstance(t, py)

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, ok := rec.Result.Tools[ToolObservability]; ok {
		t.Errorf("tools[%s] = %q on a run that produced no per-case records at all", ToolObservability, got)
	}
	if _, ok := rec.Result.Metrics[MetricPropertyCasesTotal]; ok {
		t.Error("property_cases_total is present with no source at all")
	}
}

// A failing property is a FAILING GATE, not an error: properties-pass reads
// the two stream-derived counts, and the run's own exit code agrees.
func TestPropertiesPassGateReadsTheStream(t *testing.T) {
	py := fakeProperties{
		probe: map[string]string{"pytest": "8.4.0", "hypothesis": "6.100.0"},
		stream: `1	session_start	{"pid":7}
2	property_case	{"property":"t.py::p","status":"failed"}
3	test	{"nodeid":"t.py::p","outcome":"failed"}
4	session_finish	{"duration_ms":9,"errored":0,"exitstatus":1,"failed":1,"passed":0,"skipped":0,"total":1}`,
		exit: 1,
	}.write(t)
	o := propertiesInstance(t, py)

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusFail {
		t.Errorf("status = %s, want fail", rec.Result.Status)
	}
	gate := policy.Gate{Predicate: policy.GatePropertiesPass, Basis: object.BasisConstruction}
	ok, reason := gate.Eval(&rec)
	if ok || reason != "properties_failed=1 properties_errored=0" {
		t.Errorf("gate = (%v, %q), want the failure with its counts", ok, reason)
	}
}

// THE TRUST-BOUNDARY POSTURE. S1 first: a 0-exit with no usable stream is
// `error`, never `pass` — the cheapest attack on a streaming rung is to
// unregister the observer, and it must buy a failed gate rather than a
// green one.
func TestPropertiesSilencedObserverIsErrorNotPass(t *testing.T) {
	py := fakeProperties{probe: map[string]string{"pytest": "8.4.0"}}.write(t) // no stream at all
	o := propertiesInstance(t, py)

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusError {
		t.Errorf("status = %s, want error", rec.Result.Status)
	}
	for _, m := range []string{MetricPropertiesTotal, MetricPropertiesFailed, MetricPropertyCasesTotal} {
		if _, ok := rec.Result.Metrics[m]; ok {
			t.Errorf("%s survived a silenced stream", m)
		}
	}
	gate := policy.Gate{Predicate: policy.GatePropertiesPass, Basis: object.BasisConstruction}
	if ok, _ := gate.Eval(&rec); ok {
		t.Error("properties-pass PASSED on a run with no usable evidence stream")
	}
}

// S2: an exit code that contradicts the stream is a contradiction, not a
// verdict. This is vector 2 in property clothing — an atexit handler
// calling os._exit(0) over a stream that already reported a failure.
func TestPropertiesExitCodeIsCrossExamined(t *testing.T) {
	py := fakeProperties{
		probe: map[string]string{"pytest": "8.4.0"},
		stream: `1	session_start	{"pid":7}
2	test	{"nodeid":"t.py::p","outcome":"failed"}
3	session_finish	{"duration_ms":9,"errored":0,"exitstatus":0,"failed":1,"passed":0,"skipped":0,"total":1}`,
	}.write(t)
	o := propertiesInstance(t, py)

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusError {
		t.Errorf("status = %s, want error", rec.Result.Status)
	}
	if notes := storedNotes(t, o.store, rec); !strings.Contains(notes, "exit_code=0") {
		t.Errorf("stderr artifact does not record the contradiction:\n%s", notes)
	}
}

// The receipt's own posture: streamed regime, the observer named, the
// declared property module on argv, and every metric inside the kind's
// declared vocabulary.
func TestPropertiesReceiptPosture(t *testing.T) {
	py := fakeProperties{
		probe:  map[string]string{"pytest": "8.4.0", "hypothesis": "6.100.0"},
		stream: streamTwoPropertiesFourCases,
	}.write(t)
	o := propertiesInstance(t, py)

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Family != policy.FamilyProperty {
		t.Errorf("family = %q, want %q", rec.Family, policy.FamilyProperty)
	}
	if rec.Execution.EvidenceRegime != object.RegimeStreamed {
		t.Errorf("evidence_regime = %q, want streamed", rec.Execution.EvidenceRegime)
	}
	if rec.Execution.EvidencePlugin != o.ev.pluginDigest {
		t.Errorf("evidence_plugin = %q, want the observer's content address", rec.Execution.EvidencePlugin)
	}
	argv := strings.Join(rec.Execution.Argv, " ")
	if !strings.Contains(argv, "props/mvo_props.py") {
		t.Errorf("argv = %q, want the policy-declared property module", argv)
	}
	if !strings.Contains(argv, "-p mvo_evidence") {
		t.Errorf("argv = %q, want the observer loaded first by argv", argv)
	}
	if !strings.Contains(argv, "-p no:cacheprovider") {
		t.Errorf("argv = %q, want the cache provider disabled", argv)
	}
	vocab := map[string]bool{}
	for _, m := range MetricVocabulary(KindProperties) {
		vocab[m] = true
	}
	for name := range rec.Result.Metrics {
		if !vocab[name] {
			t.Errorf("metric %q is outside the declared vocabulary of %s", name, KindProperties)
		}
	}
	if rec.Result.Tools[ToolHypothesis] != "6.100.0" {
		t.Errorf("tools[hypothesis] = %q, want the probed version", rec.Result.Tools[ToolHypothesis])
	}
}

// Vector 16, closed at rung O-1: the policy-declared property module joins
// the compiled HARNESS set, so a candidate that rewrites every property to
// `assert True` never reaches a Python run.
func TestDeclaredPropertyModuleIsHarnessFrozen(t *testing.T) {
	pol := policy.Default()
	pol.Name = "properties"
	pol.Oracles = append(pol.Oracles, object.OracleSpec{
		Name: "props", Kind: policy.KindProperties, Argv: []string{}, Args: []string{},
		Corpus: object.CorpusSpec{Provider: policy.ProviderHypothesis, Module: "props/mvo_props.py"},
	})
	pol.HardGates = append(pol.HardGates, object.GateSpec{
		Gate: policy.GatePropertiesPass, Oracle: "props", Basis: object.BasisConstruction,
	})
	if err := policy.Validate(pol); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	compiled, err := policy.Compile("mv0:test", pol)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := compiled.Paths.Class("props/mvo_props.py"); got != policy.ClassHarness {
		t.Errorf("the declared property module compiles to class %q, want %q", got, policy.ClassHarness)
	}
	// The same module is therefore excluded from the mutation target set:
	// mutating the harness measures less than nothing.
	set := DiffTargets([]byte(`diff --git a/props/mvo_props.py b/props/mvo_props.py
--- a/props/mvo_props.py
+++ b/props/mvo_props.py
@@ -1,2 +1,2 @@
 def test_p():
-    assert clamp(5, 0, 10) == 5
+    assert True
`), compiled.Paths)
	if !set.Empty() || set.Dropped[DropHarness] != 1 {
		t.Errorf("target set = %+v, want the harness edit dropped and counted", set)
	}
}

// MissingTools is the pre-flight half of decision 20: what a rung cannot
// run without. pytest is checked separately for every Python rung, so it is
// not repeated here, and `auto` is satisfied by EITHER mutation tool.
func TestMissingTools(t *testing.T) {
	none := map[string]string{"pytest": "8.4.0"}
	all := map[string]string{"pytest": "8.4.0", "hypothesis": "6.100.0",
		"cosmic-ray": "8.3.7", "mutmut": "2.4.4"}

	props := policy.Oracle{Kind: KindProperties}
	if got := MissingTools(props, none); len(got) != 1 || got[0] != ToolHypothesis {
		t.Errorf("MissingTools(properties) = %v, want [hypothesis]", got)
	}
	if got := MissingTools(props, all); got != nil {
		t.Errorf("MissingTools(properties, complete) = %v, want none", got)
	}

	auto := policy.Oracle{Kind: KindMutationDiff,
		Mutation: object.MutationSpec{Tool: policy.MutationToolAuto}}
	if got := MissingTools(auto, none); len(got) != 2 {
		t.Errorf("MissingTools(auto) = %v, want both tools named", got)
	}
	// auto is satisfied by EITHER, so having one is enough.
	if got := MissingTools(auto, map[string]string{"mutmut": "2.4.4"}); got != nil {
		t.Errorf("MissingTools(auto, mutmut present) = %v, want none", got)
	}
	// A PINNED tool is not satisfied by the other one: CP-5's rule is that
	// the pinned policy determines the run, and silently substituting a
	// tool with different selection provenance would break decision 19's
	// label.
	pinned := policy.Oracle{Kind: KindMutationDiff,
		Mutation: object.MutationSpec{Tool: policy.MutationToolCosmicRay}}
	if got := MissingTools(pinned, map[string]string{"mutmut": "2.4.4"}); len(got) != 1 || got[0] != ToolCosmicRay {
		t.Errorf("MissingTools(cosmic-ray pinned, mutmut present) = %v, want [cosmic-ray]", got)
	}
	// Kinds with no toolchain of their own need nothing.
	if got := MissingTools(policy.Oracle{Kind: KindPytestSuite}, none); got != nil {
		t.Errorf("MissingTools(pytest-suite) = %v, want none", got)
	}
}

// The embedded observer really does carry the forwarding callback, and it
// really does fail closed: every failure mode ends with no property_case
// records, which the control plane reads as ABSENT metrics rather than as a
// test failure. A plugin that raised on a missing hypothesis would let a
// candidate manufacture a failing property run for a competitor.
func TestEmbeddedObserverForwardsHypothesisObservations(t *testing.T) {
	src := string(PluginSource())
	for _, want := range []string{
		"hypothesis.internal.observability",
		"TESTCASE_CALLBACKS",
		`"property_case"`,
		"_register_hypothesis",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the embedded observer does not contain %q", want)
		}
	}
	// The registration is attempted BEFORE the session plugin registers,
	// and every arm of it is wrapped: `except Exception: return False`,
	// never a raise.
	if !strings.Contains(src, "except Exception:\n        return False") {
		t.Error("the observability import is not fail-closed")
	}
	if strings.Count(src, "TESTCASE_CALLBACKS.append") != 1 {
		t.Error("the callback is registered more than once, or not at all")
	}
}

// The wire record and the derivation, over a stream the control plane
// framed: this is the half of decision 15 that makes property_cases_* a
// metric at all.
func TestStreamCarriesPropertyCases(t *testing.T) {
	const nonce = "0123456789abcdef0123456789abcdef"
	raw := StreamSchema + "\t" + nonce + "\n" + streamTwoPropertiesFourCases + "\n"
	s := ParseStream([]byte(raw), nonce, false)
	if !s.Usable {
		t.Fatalf("stream unusable: %s", s.Reason)
	}
	if !s.HasPropertyCases {
		t.Fatal("HasPropertyCases is false on a stream carrying property_case records")
	}
	if s.PropertyCases != 4 || s.PropertyCasesInvalid != 2 {
		t.Errorf("cases/invalid = %d/%d, want 4/2", s.PropertyCases, s.PropertyCasesInvalid)
	}
	// A suite stream carries none, and says so — absence is a fact about
	// the run, not a zero.
	plain := StreamSchema + "\t" + nonce + "\n" +
		"1\tsession_start\t{}\n" +
		"2\ttest\t{\"nodeid\":\"t.py::a\",\"outcome\":\"passed\"}\n" +
		"3\tsession_finish\t{\"duration_ms\":1,\"errored\":0,\"exitstatus\":0,\"failed\":0,\"passed\":1,\"skipped\":0,\"total\":1}\n"
	if got := ParseStream([]byte(plain), nonce, false); got.HasPropertyCases {
		t.Error("a suite stream claims to carry property cases")
	}
}
