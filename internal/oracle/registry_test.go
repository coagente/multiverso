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
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

func TestNewKinds(t *testing.T) {
	if got, want := Kinds(), []string{
		KindCommand, KindCorpusDifferential, KindCorpusObserve, KindProperties,
		KindMutationDiff, KindPytestCollect, KindPytestSuite, KindTreeGuard,
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("Kinds() = %v, want %v", got, want)
	}
	for kind, want := range map[string]string{
		KindCommand:            FamilySuite,
		KindPytestCollect:      FamilyCollect,
		KindPytestSuite:        FamilySuite,
		KindTreeGuard:          FamilyTree,
		KindProperties:         FamilyProperty,
		KindMutationDiff:       FamilyMutation,
		KindCorpusObserve:      FamilyBehavior,
		KindCorpusDifferential: FamilyBehavior,
	} {
		if got := Family(kind); got != want {
			t.Errorf("Family(%q) = %q, want %q", kind, got, want)
		}
	}
	if Family("no-such-kind") != "" {
		t.Error("Family reported a family for an unknown kind")
	}
}

func TestNewBuildsEachKind(t *testing.T) {
	store := newStore(t)
	cfg := "mv0:" + strings.Repeat("7", 64)
	tests := []struct {
		name    string
		spec    policy.Oracle
		wantID  string
		wantFam string
	}{
		{
			name:   "command",
			spec:   policy.Oracle{Kind: KindCommand, Config: cfg, Argv: []string{"/bin/sh", "-c", "true"}},
			wantID: KindCommand, wantFam: FamilySuite,
		},
		{
			name:   "pytest-collect",
			spec:   policy.Oracle{Kind: KindPytestCollect, Config: cfg},
			wantID: KindPytestCollect, wantFam: FamilyCollect,
		},
		{
			name:   "pytest-suite",
			spec:   policy.Oracle{Kind: KindPytestSuite, Config: cfg, Coverage: true, Reruns: 2},
			wantID: KindPytestSuite, wantFam: FamilySuite,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, err := New(Params{Spec: tt.spec, CAS: store, Timeout: time.Minute})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if o.ID() != tt.wantID {
				t.Errorf("ID() = %q, want %q", o.ID(), tt.wantID)
			}
			if o.Version() != "v0" {
				t.Errorf("Version() = %q, want v0 (OUR contract version, not the tool's)", o.Version())
			}
			if got := Family(o.ID()); got != tt.wantFam {
				t.Errorf("family = %q, want %q", got, tt.wantFam)
			}
		})
	}
}

func TestNewRejects(t *testing.T) {
	store := newStore(t)
	cfg := "mv0:" + strings.Repeat("7", 64)
	tests := []struct {
		name string
		p    Params
	}{
		{"unknown kind", Params{Spec: policy.Oracle{Kind: "pytest-mutate", Config: cfg}, CAS: store}},
		{"empty kind", Params{Spec: policy.Oracle{Config: cfg}, CAS: store}},
		{"nil cas", Params{Spec: policy.Oracle{Kind: KindPytestSuite, Config: cfg}}},
		{
			// Without the resolved-config digest a receipt cannot say
			// WHICH instance produced it, and no gate could select it.
			name: "no resolved config",
			p:    Params{Spec: policy.Oracle{Kind: KindPytestSuite}, CAS: store},
		},
		{
			name: "family disagrees with the registry",
			p: Params{Spec: policy.Oracle{Kind: KindPytestCollect, Config: cfg, Family: FamilySuite},
				CAS: store},
		},
		{"negative timeout", Params{Spec: policy.Oracle{Kind: KindPytestSuite, Config: cfg, TimeoutMS: -1}, CAS: store}},
		{"negative reruns", Params{Spec: policy.Oracle{Kind: KindPytestSuite, Config: cfg, Reruns: -1}, CAS: store}},
		{
			name: "reruns on a non-suite kind",
			p:    Params{Spec: policy.Oracle{Kind: KindPytestCollect, Config: cfg, Reruns: 2}, CAS: store},
		},
		{"command without argv", Params{Spec: policy.Oracle{Kind: KindCommand, Config: cfg}, CAS: store}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if o, err := New(tt.p); err == nil {
				t.Fatalf("New: want error, got %T", o)
			}
		})
	}
}

// The spec's timeout wins when it names one; otherwise the caller's
// fallback (the intent's max_wall_ms) applies.
func TestNewTimeoutResolution(t *testing.T) {
	store := newStore(t)
	cfg := "mv0:" + strings.Repeat("7", 64)
	o, err := New(Params{Spec: policy.Oracle{Kind: KindPytestSuite, Config: cfg, TimeoutMS: 1500}, CAS: store,
		Timeout: time.Hour})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := o.(*pytestOracle).timeout; got != 1500*time.Millisecond {
		t.Errorf("timeout = %v, want 1.5s from the spec", got)
	}
	o, err = New(Params{Spec: policy.Oracle{Kind: KindPytestSuite, Config: cfg}, CAS: store, Timeout: time.Hour})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := o.(*pytestOracle).timeout; got != time.Hour {
		t.Errorf("timeout = %v, want the caller's fallback", got)
	}
	cmd, err := New(Params{Spec: policy.Oracle{Kind: KindCommand, Config: cfg, Argv: []string{"true"}},
		CAS: store, Timeout: 42 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := cmd.(*CommandOracle).Timeout; got != 42*time.Second {
		t.Errorf("command timeout = %v, want the caller's fallback", got)
	}
}

// A command oracle parses nothing, so it emits {} metrics and {} tools —
// empty, never null, and never a fabricated number.
func TestCommandReceiptCarriesEmptyMaps(t *testing.T) {
	store := newStore(t)
	cfg := "mv0:" + strings.Repeat("7", 64)
	o, err := New(Params{
		Spec: policy.Oracle{Kind: KindCommand, Config: cfg, Argv: []string{"/bin/sh", "-c", "echo hi"}},
		CAS:  store, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Metrics == nil || len(rec.Result.Metrics) != 0 {
		t.Errorf("metrics = %v, want an empty non-nil map", rec.Result.Metrics)
	}
	if rec.Result.Tools == nil || len(rec.Result.Tools) != 0 {
		t.Errorf("tools = %v, want an empty non-nil map", rec.Result.Tools)
	}
	if len(MetricVocabulary(KindCommand)) != 0 {
		t.Errorf("command's metric vocabulary = %v, want empty", MetricVocabulary(KindCommand))
	}
	// A registry-built instance records the POLICY's resolved config...
	if rec.Oracle.Config != cfg {
		t.Errorf("oracle config = %q, want the policy's %q", rec.Oracle.Config, cfg)
	}
	// ...while the M0 path (mvo race --oracle-cmd under a v0 policy, where
	// no policy names the oracle) still derives it from argv+timeout, so
	// old receipts keep digesting to the same bytes.
	legacy := &CommandOracle{Argv: []string{"/bin/sh", "-c", "echo hi"}, Timeout: time.Minute, CAS: store}
	legacyRec, err := legacy.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantLegacy, _, err := object.Digest(map[string]any{
		"argv":       legacy.Argv,
		"timeout_ms": legacy.Timeout.Milliseconds(),
	})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if legacyRec.Oracle.Config != wantLegacy {
		t.Errorf("v0-path config = %q, want M0's argv+timeout digest %q",
			legacyRec.Oracle.Config, wantLegacy)
	}
}

// Probe is the pre-flight surface (decision 15): it reports what is
// importable and never errors — absence is the record.
func TestProbeReportsNothingWhenUnrunnable(t *testing.T) {
	tools, raw := Probe(context.Background(), backend.HostDir(t.TempDir()),
		filepath.Join(t.TempDir(), "no-such-python"))
	if len(tools) != 0 || len(raw) != 0 {
		t.Errorf("Probe = (%v, %q), want nothing", tools, raw)
	}
}

func TestParseProbe(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{
			name: "all four sources",
			raw:  `{"coverage":"7.15.4","pytest":"9.1.1","pytest-reportlog":"0.4.0","pytest-rerunfailures":"16.3"}`,
			want: fullProbe,
		},
		{
			name: "core pytest only",
			raw:  `{"pytest": "9.1.1"}` + "\n",
			want: map[string]string{ToolPytest: "9.1.1"},
		},
		{
			// The probe runs in a world an agent wrote to: its output is
			// input, not truth, so unknown names are ignored.
			name: "unknown names are ignored",
			raw:  `{"pytest":"9.1.1","numpy":"2.0.0"}`,
			want: map[string]string{ToolPytest: "9.1.1"},
		},
		{
			// M2a's three additions (decision 20). None of them is
			// installed on the machine this was written on, which is why
			// every degradation path they feed is a live path.
			name: "the M2a toolchain",
			raw:  `{"cosmic-ray":"8.3.7","hypothesis":"6.100.0","mutmut":"2.4.4","pytest":"9.1.1"}`,
			want: map[string]string{
				ToolCosmicRay: "8.3.7", ToolHypothesis: "6.100.0",
				ToolMutmut: "2.4.4", ToolPytest: "9.1.1",
			},
		},
		{name: "not json", raw: "ModuleNotFoundError: importlib\n", want: map[string]string{}},
		{name: "empty", raw: "", want: map[string]string{}},
		{name: "empty object", raw: "{}", want: map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseProbe([]byte(tt.raw)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseProbe(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// The probe script is what pre-flight and every pytest receipt depend on,
// so its text is pinned here byte for byte.
func TestProbeScriptGolden(t *testing.T) {
	const want = "import json,importlib.metadata as m\n" +
		"o={}\n" +
		"for n in (\"pytest\",\"coverage\",\"pytest-reportlog\",\"pytest-rerunfailures\"," +
		"\"hypothesis\",\"mutmut\",\"cosmic-ray\"):\n" +
		"    try: o[n]=m.version(n)\n" +
		"    except Exception: pass\n" +
		"print(json.dumps(o,sort_keys=True))\n"
	if probeScript != want {
		t.Errorf("probe script =\n%q\nwant\n%q", probeScript, want)
	}
}

// A real-toolchain smoke test: it runs the ladder against the committed toy
// repo with whatever Python is actually installed, and SKIPS with a named
// reason when pytest is absent. No test in this package requires it.
func TestLiveLadderAgainstToyRepo(t *testing.T) {
	dir := t.TempDir()
	world := backend.HostDir(dir)
	tools, _ := Probe(context.Background(), world, "")
	if tools[ToolPytest] == "" {
		t.Skipf("skipping: pytest is not importable under %q in this environment", policy.DefaultPytestPrefix()[0])
	}
	for _, name := range []string{"stats.py", "test_stats.py"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "toyrepo", name))
		if err != nil {
			t.Fatalf("read toyrepo %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	store := newStore(t)
	cfg := "mv0:" + strings.Repeat("7", 64)

	collect, err := New(Params{
		Spec: policy.Oracle{Kind: KindPytestCollect, Config: cfg}, CAS: store, Timeout: 2 * time.Minute,
		Baseline: 8,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec, err := collect.Run(context.Background(), world)
	if err != nil {
		t.Fatalf("collect Run: %v", err)
	}
	if rec.Result.Status != StatusPass {
		t.Fatalf("collect status = %q (exit %d), want pass", rec.Result.Status, rec.Execution.ExitCode)
	}
	if got := rec.Result.Metrics[MetricCollectedTotal]; got != 8 {
		t.Errorf("collected_total = %d, want 8 (the toy repo's test count)", got)
	}
	if got := rec.Result.Metrics[MetricCollectedDelta]; got != 0 {
		t.Errorf("collected_delta = %d, want 0 against an identical baseline", got)
	}
	if rec.Result.Tools[ToolPytest] == "" {
		t.Error("result.tools names no pytest although the probe found it")
	}

	suite, err := New(Params{
		Spec: policy.Oracle{Kind: KindPytestSuite, Config: cfg, Coverage: true}, CAS: store,
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec, err = suite.Run(context.Background(), world)
	if err != nil {
		t.Fatalf("suite Run: %v", err)
	}
	// The toy repo's mean() is deliberately broken, so the suite fails —
	// what matters here is that the JUnit report parsed at all.
	if got := rec.Result.Metrics[MetricTestsTotal]; got != 8 {
		t.Errorf("tests_total = %d, want 8; artifacts=%v tools=%v",
			got, rec.Result.Artifacts, rec.Result.Tools)
	}
	if tools[ToolCoverage] != "" {
		if _, ok := rec.Result.Metrics[MetricCoverageBP]; !ok {
			t.Errorf("coverage_bp absent although coverage.py %s is installed", tools[ToolCoverage])
		}
	}

	// The laundering vector against the REAL tool: a tree with no tests
	// collects nothing, pytest exits 5, and the receipt says fail with an
	// explicit zero — the wipe-the-tests candidate never reaches a suite.
	empty, err := New(Params{
		Spec: policy.Oracle{Kind: KindPytestCollect, Config: cfg}, CAS: store, Timeout: 2 * time.Minute,
		Baseline: 8,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec, err = empty.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("collect Run: %v", err)
	}
	if rec.Execution.ExitCode != 5 || rec.Result.Status != StatusFail {
		t.Errorf("empty tree: exit/status = %d/%q, want 5/fail", rec.Execution.ExitCode, rec.Result.Status)
	}
	want := map[string]int64{MetricCollectedTotal: 0, MetricCollectedBase: 8, MetricCollectedDelta: -8}
	if !reflect.DeepEqual(rec.Result.Metrics, want) {
		t.Errorf("empty tree metrics = %v, want %v", rec.Result.Metrics, want)
	}
}
