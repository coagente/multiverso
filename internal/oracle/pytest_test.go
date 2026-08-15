package oracle

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// fullProbe is what a fully equipped world reports.
var fullProbe = map[string]string{
	ToolPytest:        "9.1.1",
	ToolCoverage:      "7.15.4",
	ToolReportlog:     "0.4.0",
	ToolRerunfailures: "16.3",
}

// runOracle builds an instance through the registry and runs it in a
// throwaway world.
func runOracle(t *testing.T, p Params) (object.Receipt, string) {
	t.Helper()
	o, err := New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dir := t.TempDir()
	rec, err := o.Run(context.Background(), backend.HostDir(dir))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rec, dir
}

// ---------------------------------------------------------------- O0 ----

// Exit 5 ("no tests collected") is the laundering vector research ch. 19
// names: it is non-zero, and no wrapper, no `|| true`, and no "exit != 1
// means fine" shortcut may ever turn it into a pass. The count is recorded
// EXPLICITLY as 0 so collect-nonempty fails with a reason.
func TestCollectExitFiveIsFailWithZeroCount(t *testing.T) {
	py := fakePython{
		probe:  map[string]string{ToolPytest: "9.1.1"},
		stdout: "\nno tests collected in 0.00s",
		exit:   5,
	}.write(t)
	rec, _ := runOracle(t, Params{
		Spec:     testSpec(KindPytestCollect, py),
		CAS:      newStore(t),
		Timeout:  30 * time.Second,
		Baseline: 8,
	})
	if rec.Result.Status != StatusFail {
		t.Errorf("status = %q, want %q — exit 5 must never pass", rec.Result.Status, StatusFail)
	}
	if rec.Execution.ExitCode != 5 {
		t.Errorf("exit_code = %d, want the RAW 5 (never a boolean)", rec.Execution.ExitCode)
	}
	want := map[string]int64{
		MetricCollectedTotal: 0,
		MetricCollectedBase:  8,
		MetricCollectedDelta: -8,
	}
	if !reflect.DeepEqual(rec.Result.Metrics, want) {
		t.Errorf("metrics = %v, want %v", rec.Result.Metrics, want)
	}
}

// Even a candidate that deletes every test and prints nothing useful must
// not produce a passing collect receipt: exit 5 forces the count.
func TestCollectExitFiveOverridesStdout(t *testing.T) {
	py := fakePython{
		probe:  map[string]string{ToolPytest: "9.1.1"},
		stdout: "999 tests collected in 0.01s",
		exit:   5,
	}.write(t)
	rec, _ := runOracle(t, Params{
		Spec: testSpec(KindPytestCollect, py), CAS: newStore(t), Timeout: 30 * time.Second,
	})
	if got := rec.Result.Metrics[MetricCollectedTotal]; got != 0 {
		t.Errorf("collected_total = %d, want 0 (exit 5 is the documented meaning)", got)
	}
	if rec.Result.Status == StatusPass {
		t.Error("status = pass on exit 5")
	}
}

func TestCollectDeltas(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		exit     int
		baseline int64
		want     map[string]int64
	}{
		{
			name:     "no baseline measured: no denominator, no delta",
			stdout:   "8 tests collected in 0.01s",
			baseline: 0,
			want:     map[string]int64{MetricCollectedTotal: 8},
		},
		{
			name:     "unchanged suite",
			stdout:   "8 tests collected in 0.01s",
			baseline: 8,
			want: map[string]int64{
				MetricCollectedTotal: 8, MetricCollectedBase: 8, MetricCollectedDelta: 0,
			},
		},
		{
			name:     "tests added",
			stdout:   "10 tests collected in 0.01s",
			baseline: 8,
			want: map[string]int64{
				MetricCollectedTotal: 10, MetricCollectedBase: 8, MetricCollectedDelta: 2,
			},
		},
		{
			// The subtle laundering vector: delete SOME tests so the suite
			// stays green. collected-not-below is what catches it.
			name:     "tests deleted",
			stdout:   "5 tests collected in 0.01s",
			baseline: 8,
			want: map[string]int64{
				MetricCollectedTotal: 5, MetricCollectedBase: 8, MetricCollectedDelta: -3,
			},
		},
		{
			// No summary line and no node ids: the metric is ABSENT, and a
			// gate that needs it fails on the absence, never on a zero.
			name:     "no parseable output",
			stdout:   "",
			baseline: 8,
			want:     map[string]int64{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			py := fakePython{
				probe:  map[string]string{ToolPytest: "9.1.1"},
				stdout: tt.stdout,
				exit:   tt.exit,
			}.write(t)
			rec, _ := runOracle(t, Params{
				Spec:     testSpec(KindPytestCollect, py),
				CAS:      newStore(t),
				Timeout:  30 * time.Second,
				Baseline: tt.baseline,
			})
			if !reflect.DeepEqual(rec.Result.Metrics, tt.want) {
				t.Errorf("metrics = %v, want %v", rec.Result.Metrics, tt.want)
			}
		})
	}
}

func TestParseCollected(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   int64
		known  bool
	}{
		{
			name:   "quiet summary",
			stdout: "test_stats.py::test_mean\ntest_stats.py::test_total\n\n8 tests collected in 0.01s\n",
			want:   8, known: true,
		},
		{name: "singular", stdout: "1 test collected in 0.00s\n", want: 1, known: true},
		{name: "no tests collected", stdout: "\nno tests collected in 0.00s\n", want: 0, known: true},
		{
			name:   "collected with errors",
			stdout: "3 tests collected, 1 error in 0.05s\n",
			want:   3, known: true,
		},
		{
			// The LAST summary line wins: a candidate that prints an
			// earlier "999 tests collected" cannot outvote pytest's own.
			name:   "last summary wins",
			stdout: "999 tests collected in 9.99s\n6 tests collected in 0.01s\n",
			want:   6, known: true,
		},
		{
			name:   "node-id fallback",
			stdout: "test_stats.py::test_mean\ntest_stats.py::test_total\npkg/test_more.py\n",
			want:   3, known: true,
		},
		{name: "nothing parseable", stdout: "", known: false},
		{name: "only noise", stdout: "ERROR: interrupted\n", known: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := parseCollected([]byte(tt.stdout))
			if known != tt.known || got != tt.want {
				t.Errorf("parseCollected = (%d, %v), want (%d, %v)", got, known, tt.want, tt.known)
			}
		})
	}
}

func TestCollectArgvGolden(t *testing.T) {
	o := &pytestOracle{kind: KindPytestCollect, spec: policy.Oracle{Args: []string{"tests/", "-x"}}}
	want := []string{
		"python3", "-m", "pytest",
		"--collect-only", "-q", "-p", "no:cacheprovider",
		"tests/", "-x",
	}
	if got := o.collectArgv(); !reflect.DeepEqual(got, want) {
		t.Errorf("collect argv = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------- O1 ----

func TestSuiteArgvGoldens(t *testing.T) {
	const (
		junit = "--junit-xml=.mvo-oracle/pytest-suite/junit.xml"
		rlog  = "--report-log=.mvo-oracle/pytest-suite/reportlog.jsonl"
	)
	tests := []struct {
		name  string
		spec  policy.Oracle
		tools map[string]string
		want  []string
		plan  suitePlan
	}{
		{
			name:  "every plugin present",
			spec:  policy.Oracle{Coverage: true, Reruns: 2, Args: []string{"-x"}},
			tools: fullProbe,
			want: []string{
				"python3", "-m", "coverage", "run", "-m", "pytest",
				junit, "-p", "no:cacheprovider", rlog, "--reruns", "2", "-x",
			},
			plan: suitePlan{coverage: true, reportlog: true, reruns: true},
		},
		{
			// Core pytest only: JUnit XML needs no plugin, so the run still
			// produces evidence — just less of it.
			name:  "bare pytest",
			spec:  policy.Oracle{Coverage: true, Reruns: 2},
			tools: map[string]string{ToolPytest: "9.1.1"},
			want:  []string{"python3", "-m", "pytest", junit, "-p", "no:cacheprovider"},
		},
		{
			// Without the JSONL there is no honest way to see the first
			// run, so reruns are not requested at all (decision 17).
			name:  "rerunfailures without reportlog: no --reruns",
			spec:  policy.Oracle{Reruns: 3},
			tools: map[string]string{ToolPytest: "9.1.1", ToolRerunfailures: "16.3"},
			want:  []string{"python3", "-m", "pytest", junit, "-p", "no:cacheprovider"},
		},
		{
			name:  "reportlog without rerunfailures: log but no reruns",
			spec:  policy.Oracle{Reruns: 3},
			tools: map[string]string{ToolPytest: "9.1.1", ToolReportlog: "0.4.0"},
			want:  []string{"python3", "-m", "pytest", junit, "-p", "no:cacheprovider", rlog},
			plan:  suitePlan{reportlog: true},
		},
		{
			name:  "coverage present but not requested",
			spec:  policy.Oracle{Coverage: false},
			tools: fullProbe,
			want: []string{
				"python3", "-m", "pytest", junit, "-p", "no:cacheprovider", rlog,
			},
			plan: suitePlan{reportlog: true},
		},
		{
			// A prefix coverage.py cannot drive is left alone: coverage
			// stays unmeasured and says so, rather than mangling the
			// command that produces the verdict.
			name:  "non -m prefix is never coverage-wrapped",
			spec:  policy.Oracle{Coverage: true, Argv: []string{"/usr/local/bin/pytest"}},
			tools: fullProbe,
			want: []string{
				"/usr/local/bin/pytest", junit, "-p", "no:cacheprovider", rlog,
			},
			plan: suitePlan{reportlog: true},
		},
		{
			name:  "zero reruns never asks for reruns",
			spec:  policy.Oracle{Reruns: 0},
			tools: fullProbe,
			want:  []string{"python3", "-m", "pytest", junit, "-p", "no:cacheprovider", rlog},
			plan:  suitePlan{reportlog: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &pytestOracle{kind: KindPytestSuite, spec: tt.spec}
			got := o.planSuite(tt.tools)
			if !reflect.DeepEqual(got.argv, tt.want) {
				t.Errorf("suite argv =\n  %v\nwant\n  %v", got.argv, tt.want)
			}
			got.argv = nil
			if !reflect.DeepEqual(got, tt.plan) {
				t.Errorf("plan = %+v, want %+v", got, tt.plan)
			}
		})
	}
}

// The full ladder rung with every source present: artifacts in kind order,
// the complete metric set, tools naming what was used — and the first-run
// rule turning a rerun-rescued green run into a FAIL (EP-6, decision 17).
func TestSuiteFullEvidence(t *testing.T) {
	py := fakePython{
		probe:        fullProbe,
		stdout:       "8 passed in 0.13s",
		exit:         0,
		junit:        string(fixture(t, "junit-pass.xml")),
		reportlog:    string(fixture(t, "reportlog-rerun.jsonl")),
		coverageJSON: string(fixture(t, "coverage.json")),
	}.write(t)
	spec := testSpec(KindPytestSuite, py)
	spec.Coverage, spec.Reruns = true, 2
	rec, _ := runOracle(t, Params{Spec: spec, CAS: newStore(t), Timeout: 30 * time.Second})

	wantMetrics := map[string]int64{
		MetricTestsTotal:            8,
		MetricTestsPassed:           8,
		MetricTestsFailed:           0,
		MetricTestsErrored:          0,
		MetricTestsSkipped:          0,
		MetricDurationMS:            132,
		MetricCoverageBP:            8571,
		MetricTestsFailedFirstRun:   2,
		MetricTestsPassedAfterRerun: 1,
	}
	if !reflect.DeepEqual(rec.Result.Metrics, wantMetrics) {
		t.Errorf("metrics = %v, want %v", rec.Result.Metrics, wantMetrics)
	}
	if !reflect.DeepEqual(rec.Result.Tools, fullProbe) {
		t.Errorf("tools = %v, want %v", rec.Result.Tools, fullProbe)
	}
	// exit 0, but two tests failed their FIRST run: the gate sees the
	// first run, and the pass-after-rerun survives as weaker evidence.
	if rec.Result.Status != StatusFail {
		t.Errorf("status = %q, want %q (first-run failures)", rec.Result.Status, StatusFail)
	}
	if rec.Execution.ExitCode != 0 {
		t.Errorf("exit_code = %d, want the raw 0", rec.Execution.ExitCode)
	}
	if len(rec.Result.Artifacts) != 6 {
		t.Fatalf("artifacts = %v, want 6 (stdout, stderr, probe, junit, reportlog, coverage)",
			rec.Result.Artifacts)
	}
}

// A green suite whose first run was clean passes.
func TestSuitePassIsPass(t *testing.T) {
	py := fakePython{
		probe:     map[string]string{ToolPytest: "9.1.1", ToolReportlog: "0.4.0"},
		junit:     string(fixture(t, "junit-pass.xml")),
		reportlog: `{"$report_type":"TestReport","nodeid":"t.py::a","when":"call","outcome":"passed"}`,
	}.write(t)
	rec, _ := runOracle(t, Params{
		Spec: testSpec(KindPytestSuite, py), CAS: newStore(t), Timeout: 30 * time.Second,
	})
	if rec.Result.Status != StatusPass {
		t.Errorf("status = %q, want %q", rec.Result.Status, StatusPass)
	}
	if got := rec.Result.Metrics[MetricTestsFailedFirstRun]; got != 0 {
		t.Errorf("tests_failed_first_run = %d, want 0", got)
	}
}

// Missing plugins degrade the EVIDENCE, not the run: no flag is passed for
// a plugin that is not there, the metrics it would have carried are absent
// (never zero), and result.tools names only what was really used.
func TestSuiteDegradesWhenPluginsAbsent(t *testing.T) {
	py := fakePython{
		probe: map[string]string{ToolPytest: "9.1.1"},
		junit: string(fixture(t, "junit-fail.xml")),
		exit:  1,
	}.write(t)
	spec := testSpec(KindPytestSuite, py)
	spec.Coverage, spec.Reruns = true, 2
	rec, _ := runOracle(t, Params{Spec: spec, CAS: newStore(t), Timeout: 30 * time.Second})

	wantMetrics := map[string]int64{
		MetricTestsTotal:   8,
		MetricTestsPassed:  4,
		MetricTestsFailed:  2,
		MetricTestsErrored: 1,
		MetricTestsSkipped: 1,
		MetricDurationMS:   457,
	}
	if !reflect.DeepEqual(rec.Result.Metrics, wantMetrics) {
		t.Errorf("metrics = %v, want %v", rec.Result.Metrics, wantMetrics)
	}
	if want := map[string]string{ToolPytest: "9.1.1"}; !reflect.DeepEqual(rec.Result.Tools, want) {
		t.Errorf("tools = %v, want %v", rec.Result.Tools, want)
	}
	for _, absent := range []string{MetricCoverageBP, MetricTestsFailedFirstRun} {
		if _, ok := rec.Result.Metrics[absent]; ok {
			t.Errorf("%s present although its source was unavailable", absent)
		}
	}
	if len(rec.Result.Artifacts) != 4 {
		t.Errorf("artifacts = %v, want 4 (stdout, stderr, probe, junit)", rec.Result.Artifacts)
	}
	if rec.Result.Status != StatusFail || rec.Execution.ExitCode != 1 {
		t.Errorf("status/exit = %q/%d, want fail/1", rec.Result.Status, rec.Execution.ExitCode)
	}
}

// A probe that cannot run at all leaves result.tools EMPTY, which fails
// every metric gate honestly rather than pretending the sources are there.
func TestSuiteUnrunnableProbeRecordsNoTools(t *testing.T) {
	py := fakePython{probeExit: 1, junit: string(fixture(t, "junit-pass.xml"))}.write(t)
	rec, _ := runOracle(t, Params{
		Spec: testSpec(KindPytestSuite, py), CAS: newStore(t), Timeout: 30 * time.Second,
	})
	if len(rec.Result.Tools) != 0 {
		t.Errorf("tools = %v, want empty", rec.Result.Tools)
	}
	if rec.Result.Tools == nil || rec.Result.Metrics == nil {
		t.Error("nil metrics/tools map: {} is 'measured nothing', null is a lie about the record")
	}
}

// Coverage extraction is artifact extraction, never verification: when
// `coverage json` fails the source is dropped — absent metric, absent from
// result.tools — and the receipt's status and exit code are untouched.
func TestSuiteCoverageExtractionFailureNeverChangesStatus(t *testing.T) {
	py := fakePython{
		probe:        fullProbe,
		junit:        string(fixture(t, "junit-pass.xml")),
		reportlog:    `{"$report_type":"TestReport","nodeid":"t.py::a","when":"call","outcome":"passed"}`,
		coverageJSON: string(fixture(t, "coverage.json")),
		coverageExit: 3,
	}.write(t)
	spec := testSpec(KindPytestSuite, py)
	spec.Coverage = true
	rec, _ := runOracle(t, Params{Spec: spec, CAS: newStore(t), Timeout: 30 * time.Second})
	if rec.Result.Status != StatusPass || rec.Execution.ExitCode != 0 {
		t.Errorf("status/exit = %q/%d, want pass/0", rec.Result.Status, rec.Execution.ExitCode)
	}
	if _, ok := rec.Result.Metrics[MetricCoverageBP]; ok {
		t.Error("coverage_bp present although the report could not be extracted")
	}
	if _, ok := rec.Result.Tools[ToolCoverage]; ok {
		t.Error("coverage in result.tools although its report was never produced")
	}
}

// Malformed XML yields NO junit metrics — the artifact is still stored, so
// a human can look, but half a report never becomes half a verdict.
func TestSuiteMalformedJUnitYieldsNoMetrics(t *testing.T) {
	py := fakePython{
		probe: map[string]string{ToolPytest: "9.1.1"},
		junit: string(fixture(t, "junit-malformed.xml")),
	}.write(t)
	rec, _ := runOracle(t, Params{
		Spec: testSpec(KindPytestSuite, py), CAS: newStore(t), Timeout: 30 * time.Second,
	})
	if len(rec.Result.Metrics) != 0 {
		t.Errorf("metrics = %v, want none from a malformed report", rec.Result.Metrics)
	}
	if len(rec.Result.Artifacts) != 4 {
		t.Errorf("artifacts = %v, want the malformed report stored anyway", rec.Result.Artifacts)
	}
}

// The worktree is agent-writable, so the working directory is scrubbed
// host-side before every run: a planted junit.xml must never be read as
// evidence.
func TestSuiteRemovesPlantedArtifacts(t *testing.T) {
	py := fakePython{probe: map[string]string{ToolPytest: "9.1.1"}}.write(t) // writes nothing
	o, err := New(Params{
		Spec: testSpec(KindPytestSuite, py), CAS: newStore(t), Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dir := t.TempDir()
	plant(t, dir, ".mvo-oracle/pytest-suite/junit.xml",
		`<testsuite name="planted" tests="999" failures="0" errors="0" skipped="0" time="0.1"/>`)
	rec, err := o.Run(context.Background(), backend.HostDir(dir))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.Result.Metrics) != 0 {
		t.Errorf("metrics = %v; a planted junit.xml was read as evidence", rec.Result.Metrics)
	}
	if len(rec.Result.Artifacts) != 3 {
		t.Errorf("artifacts = %v, want 3 (stdout, stderr, probe)", rec.Result.Artifacts)
	}
}

// EP-7, normative order: run → read under the cap → store EVERY artifact →
// only then parse. A store that fails part-way must therefore yield no
// receipt at all, and the earlier artifacts must already be in CAS.
func TestArtifactsReachCASBeforeParsing(t *testing.T) {
	junit := string(fixture(t, "junit-pass.xml"))
	reportlog := `{"$report_type":"TestReport","nodeid":"t.py::a","when":"call","outcome":"passed"}`
	coverage := string(fixture(t, "coverage.json"))
	newFake := func(t *testing.T) string {
		return fakePython{
			probe:        fullProbe,
			stdout:       "8 passed",
			stderr:       "warning",
			junit:        junit,
			reportlog:    reportlog,
			coverageJSON: coverage,
		}.write(t)
	}
	spec := func(py string) policy.Oracle {
		s := testSpec(KindPytestSuite, py)
		s.Coverage = true
		return s
	}

	t.Run("order", func(t *testing.T) {
		store := &recordingStore{real: newStore(t)}
		o := &pytestOracle{
			kind: KindPytestSuite, spec: spec(newFake(t)), store: store, timeout: 30 * time.Second,
		}
		rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		// The probe's bytes are stored before they are parsed, too — its
		// result decides the run's argv, so it necessarily comes first.
		wantPuts := []string{"probe", "stdout", "stderr", "junit", "reportlog", "coverage"}
		if len(store.puts) != len(wantPuts) {
			t.Fatalf("%d puts, want %v", len(store.puts), wantPuts)
		}
		for i, want := range []string{
			`"pytest":"9.1.1"`, "8 passed", "warning", "<testsuite", `"$report_type"`, `"covered_lines"`,
		} {
			if !strings.Contains(string(store.puts[i]), want) {
				t.Errorf("put %d (%s) = %q, want to contain %q",
					i+1, wantPuts[i], truncate(store.puts[i]), want)
			}
		}
		// Result.Artifacts orders by KIND: stdout, stderr, tools-probe,
		// junit-xml, reportlog, coverage-json.
		wantArtifacts := []string{
			mustPut(t, store.real, store.puts[1]), // stdout
			mustPut(t, store.real, store.puts[2]), // stderr
			mustPut(t, store.real, store.puts[0]), // tools-probe
			mustPut(t, store.real, store.puts[3]), // junit-xml
			mustPut(t, store.real, store.puts[4]), // reportlog
			mustPut(t, store.real, store.puts[5]), // coverage-json
		}
		if !reflect.DeepEqual(rec.Result.Artifacts, wantArtifacts) {
			t.Errorf("artifacts = %v, want %v", rec.Result.Artifacts, wantArtifacts)
		}
	})

	for _, failOn := range []int{1, 2, 3, 4, 5, 6} {
		t.Run("cas failure at put "+string(rune('0'+failOn)), func(t *testing.T) {
			store := &recordingStore{real: newStore(t), failOn: failOn}
			o := &pytestOracle{
				kind: KindPytestSuite, spec: spec(newFake(t)), store: store, timeout: 30 * time.Second,
			}
			rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
			if err == nil {
				t.Fatal("Run: want error when an artifact cannot be stored")
			}
			if !reflect.DeepEqual(rec, object.Receipt{}) {
				t.Error("Run returned a receipt alongside a storage failure: " +
					"no metric may exist that its artifact does not")
			}
			if len(store.puts) != failOn {
				t.Errorf("%d puts before the failure, want %d — parsing must not run first",
					len(store.puts), failOn)
			}
		})
	}
}

// An over-cap artifact is neither stored nor parsed; the note lands in the
// stored stderr bytes and every metric it would have carried is absent.
func TestArtifactCap(t *testing.T) {
	big := "<testsuite tests=\"8\" failures=\"0\" errors=\"0\" skipped=\"0\" time=\"0.1\">" +
		"<!--" + strings.Repeat("padding ", 300) + "-->" + "</testsuite>"
	py := fakePython{probe: map[string]string{ToolPytest: "9.1.1"}, junit: big, stderr: "boom"}.write(t)
	store := newStore(t)
	o := &pytestOracle{
		kind: KindPytestSuite, spec: testSpec(KindPytestSuite, py), store: store,
		timeout: 30 * time.Second, cap: 512,
	}
	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.Result.Metrics) != 0 {
		t.Errorf("metrics = %v, want none: an over-cap artifact is never parsed", rec.Result.Metrics)
	}
	if len(rec.Result.Artifacts) != 3 {
		t.Errorf("artifacts = %v, want 3: an over-cap artifact is never stored", rec.Result.Artifacts)
	}
	stderr, err := store.Get(rec.Result.Artifacts[1])
	if err != nil {
		t.Fatalf("get stderr artifact: %v", err)
	}
	want := "mvo: oracle: .mvo-oracle/pytest-suite/junit.xml exceeds the 0 MiB artifact cap; " +
		"not stored, metrics absent"
	if !strings.Contains(string(stderr), want) {
		t.Errorf("stderr artifact = %q, want it to note %q", stderr, want)
	}
	if !strings.HasPrefix(string(stderr), "boom") {
		t.Errorf("stderr artifact = %q, want the process's own stderr first", stderr)
	}
}

// The shipped cap is 64 MiB and the note says so.
func TestArtifactCapDefault(t *testing.T) {
	o := &pytestOracle{kind: KindPytestSuite}
	if got := o.artifactCap(); got != artifactCapBytes || got>>20 != 64 {
		t.Errorf("artifactCap = %d, want %d (64 MiB)", got, int64(artifactCapBytes))
	}
}

func TestReceiptShape(t *testing.T) {
	py := fakePython{
		probe: map[string]string{ToolPytest: "9.1.1"},
		junit: string(fixture(t, "junit-pass.xml")),
	}.write(t)
	spec := testSpec(KindPytestSuite, py)
	rec, _ := runOracle(t, Params{Spec: spec, CAS: newStore(t), Timeout: 30 * time.Second})

	if rec.Schema != object.SchemaReceipt {
		t.Errorf("schema = %q, want %q", rec.Schema, object.SchemaReceipt)
	}
	if rec.Oracle.ID != KindPytestSuite || rec.Oracle.Version != "v0" {
		t.Errorf("oracle ref = %+v, want id %s version v0", rec.Oracle, KindPytestSuite)
	}
	// The instance's identity in evidence is (id, config) — the resolved
	// config the policy assigned, never the policy-local name.
	if rec.Oracle.Config != spec.Config {
		t.Errorf("oracle config = %q, want the policy's resolved config %q", rec.Oracle.Config, spec.Config)
	}
	if rec.Family != FamilySuite {
		t.Errorf("family = %q, want %q", rec.Family, FamilySuite)
	}
	if rec.RecheckTier != "V1-replayable" {
		t.Errorf("recheck_tier = %q, want V1-replayable", rec.RecheckTier)
	}
	if rec.Freshness.Basis != object.BasisConstruction {
		t.Errorf("basis = %q, want %q — M1's oracles measure by construction",
			rec.Freshness.Basis, object.BasisConstruction)
	}
	// World and ValidFor belong to the orchestrator, which alone knows the
	// world digest and tree.
	if rec.World != "" || rec.Freshness.ValidFor != (object.ValidFor{}) {
		t.Errorf("world/valid_for pre-filled: %q %+v", rec.World, rec.Freshness.ValidFor)
	}
	if rec.Execution.IsolationTier != object.TierT0Worktree || rec.Execution.IsolationCaps != object.HostCaps() {
		t.Errorf("isolation = %q %+v, want the world handle's own record",
			rec.Execution.IsolationTier, rec.Execution.IsolationCaps)
	}
	if rec.Execution.DurationMS < 0 || rec.Cost.WallMS < rec.Execution.DurationMS {
		t.Errorf("duration_ms = %d, cost.wall_ms = %d; the receipt's cost covers the WHOLE oracle",
			rec.Execution.DurationMS, rec.Cost.WallMS)
	}
	if _, err := time.Parse(time.RFC3339, rec.CreatedAt); err != nil {
		t.Errorf("created_at %q is not RFC3339: %v", rec.CreatedAt, err)
	}
	if rec.Result.Metrics == nil || rec.Result.Tools == nil {
		t.Error("nil metrics/tools map: a nil map canonicalizes to null, which no receipt may carry")
	}
	// The receipt records what ran IN the world: relative artifact paths,
	// so the argv is identical under T0 and T1.
	for _, arg := range rec.Execution.Argv {
		if strings.HasPrefix(arg, "--junit-xml=") && !strings.HasPrefix(arg, "--junit-xml=.mvo-oracle/") {
			t.Errorf("argv %q is not a relative in-world path", arg)
		}
	}
}

// A timed-out oracle is INCONCLUSIVE (error), never a failing candidate,
// and the kill reaches the whole process group.
func TestSuiteTimeoutIsError(t *testing.T) {
	py := fakePython{probe: map[string]string{ToolPytest: "9.1.1"}, sleep: 30}.write(t)
	spec := testSpec(KindPytestSuite, py)
	spec.TimeoutMS = 300
	start := time.Now()
	rec, _ := runOracle(t, Params{Spec: spec, CAS: newStore(t), Timeout: time.Hour})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Run took %v; the spec's timeout_ms did not bound it", elapsed)
	}
	if rec.Result.Status != StatusError || rec.Execution.ExitCode != -1 {
		t.Errorf("status/exit = %q/%d, want error/-1 on timeout",
			rec.Result.Status, rec.Execution.ExitCode)
	}
}

// Every emitted metric name must be in the kind's declared vocabulary —
// the table internal/policy validates gates and ranking keys against.
func TestKindMetricConformance(t *testing.T) {
	fixtures := map[string]fakePython{
		KindPytestCollect: {
			probe:  fullProbe,
			stdout: "8 tests collected in 0.01s",
		},
		KindPytestSuite: {
			probe:        fullProbe,
			junit:        string(fixture(t, "junit-fail.xml")),
			reportlog:    string(fixture(t, "reportlog-rerun.jsonl")),
			coverageJSON: string(fixture(t, "coverage.json")),
			exit:         1,
		},
	}
	for kind, f := range fixtures {
		t.Run(kind, func(t *testing.T) {
			declared := map[string]bool{}
			for _, m := range MetricVocabulary(kind) {
				declared[m] = true
			}
			spec := testSpec(kind, f.write(t))
			spec.Coverage = true
			if kind == KindPytestSuite {
				spec.Reruns = 2
			}
			rec, _ := runOracle(t, Params{
				Spec: spec, CAS: newStore(t), Timeout: 30 * time.Second, Baseline: 8,
			})
			if len(rec.Result.Metrics) == 0 {
				t.Fatal("no metrics emitted; the conformance check would be vacuous")
			}
			for name := range rec.Result.Metrics {
				if !declared[name] {
					t.Errorf("emitted metric %q is outside %s's declared vocabulary %v",
						name, kind, MetricVocabulary(kind))
				}
			}
		})
	}
}

func plant(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("plant %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("plant %s: %v", rel, err)
	}
}

func mustPut(t *testing.T, store *cas.Store, b []byte) string {
	t.Helper()
	key, err := store.Put(b)
	if err != nil {
		t.Fatalf("cas.Put: %v", err)
	}
	return key
}

func truncate(b []byte) string {
	if len(b) > 60 {
		return string(b[:60]) + "…"
	}
	return string(b)
}
