package oracle

// M1f: the tree-guard. Every case here runs against a real git repository
// and a real worktree, because the whole point of the gate is that it
// compares BYTES THE CONTROL PLANE HASHED ITSELF — a fake would be testing
// the wrong thing.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// guardRepo builds a repo with a base commit holding a test file, a
// harness-free root, and one application file.
func guardRepo(t *testing.T) (repo, baseTree string) {
	t.Helper()
	repo = t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("billing.py", "def split(n):\n    return n\n")
	write("test_billing.py", "def test_split():\n    assert split(3) == 3\n")
	write("tests/test_more.py", "def test_more():\n    assert True\n")
	git("add", "-A")
	git("commit", "-qm", "base")
	tree, err := gitx.TreeDigest(repo)
	if err != nil {
		t.Fatalf("TreeDigest: %v", err)
	}
	return repo, tree
}

func guardPaths(t *testing.T) policy.PathSet {
	t.Helper()
	pol, err := policy.Decode(mustCanonicalPolicy(t, policy.Default()))
	if err != nil {
		t.Fatalf("decode default policy: %v", err)
	}
	return pol.Paths
}

func mustCanonicalPolicy(t *testing.T, v any) []byte {
	t.Helper()
	b, err := object.Canonical(v)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	return b
}

func runGuard(t *testing.T, repo, baseTree string, paths policy.PathSet) (object.Receipt, GuardReport) {
	t.Helper()
	store := newStore(t)
	o, err := New(Params{
		Spec:     testSpec(KindTreeGuard, ""),
		CAS:      store,
		Paths:    paths,
		Repo:     repo,
		BaseTree: baseTree,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec, err := o.Run(context.Background(), backend.HostDir(repo))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.Result.Artifacts) != 1 {
		t.Fatalf("artifacts = %v, want exactly [tree-guard-report]", rec.Result.Artifacts)
	}
	raw, err := store.Get(rec.Result.Artifacts[0])
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report GuardReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	return rec, report
}

// Every violation class, plus the two the study's vectors actually are:
// a modified test file and a NEW conftest.py.
func TestGuardViolationClasses(t *testing.T) {
	paths := guardPaths(t)
	cases := []struct {
		name       string
		mutate     func(t *testing.T, repo string)
		wantKind   string
		wantPath   string
		wantMetric string
	}{
		{
			name: "a weakened assertion is a modified protected path",
			mutate: func(t *testing.T, repo string) {
				writeFile(t, repo, "test_billing.py", "def test_split():\n    assert split(3) >= 0\n")
			},
			wantKind: VProtectedModified, wantPath: "test_billing.py",
			wantMetric: policy.MetricProtectedModified,
		},
		{
			name: "a deleted test is a deleted protected path",
			mutate: func(t *testing.T, repo string) {
				if err := os.Remove(filepath.Join(repo, "test_billing.py")); err != nil {
					t.Fatal(err)
				}
			},
			wantKind: VProtectedDeleted, wantPath: "test_billing.py",
			wantMetric: policy.MetricProtectedDeleted,
		},
		{
			name: "a NEW conftest.py is always a violation — the study's forgery patch, verbatim",
			mutate: func(t *testing.T, repo string) {
				writeFile(t, repo, "conftest.py", "import atexit\n")
			},
			wantKind: VHarnessAdded, wantPath: "conftest.py",
			wantMetric: policy.MetricHarnessAdded,
		},
		{
			name: "a pytest.ini that silences the plugin is a new harness file",
			mutate: func(t *testing.T, repo string) {
				writeFile(t, repo, "pytest.ini", "[pytest]\naddopts = -p no:mvo_evidence\n")
			},
			wantKind: VHarnessAdded, wantPath: "pytest.ini",
			wantMetric: policy.MetricHarnessAdded,
		},
		{
			name: "a mode-only change still breaks the seal",
			mutate: func(t *testing.T, repo string) {
				if err := os.Chmod(filepath.Join(repo, "tests/test_more.py"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantKind: VProtectedModified, wantPath: "tests/test_more.py",
			wantMetric: policy.MetricProtectedModified,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, baseTree := guardRepo(t)
			tc.mutate(t, repo)
			rec, report := runGuard(t, repo, baseTree, paths)
			if rec.Result.Status != StatusFail {
				t.Fatalf("status = %q (%s), want fail", rec.Result.Status, rec.Result.Detail)
			}
			if rec.Result.Metrics[tc.wantMetric] != 1 {
				t.Errorf("%s = %d, want 1 (metrics %v)", tc.wantMetric,
					rec.Result.Metrics[tc.wantMetric], rec.Result.Metrics)
			}
			if len(report.Violations) != 1 {
				t.Fatalf("violations = %+v, want exactly one", report.Violations)
			}
			v := report.Violations[0]
			if v.Kind != tc.wantKind || v.Path != tc.wantPath {
				t.Errorf("violation = %+v, want kind %s path %s", v, tc.wantKind, tc.wantPath)
			}
			// result.detail is what the gate's fail reason quotes as
			// "(first: %s)": a name in a table cell an operator can act on.
			if rec.Result.Detail != tc.wantPath {
				t.Errorf("detail = %q, want the first offending path %q", rec.Result.Detail, tc.wantPath)
			}
		})
	}
}

// Adding a regression test is the behaviour we WANT, so it is allowed by
// default and recorded as an allowed addition rather than a violation.
func TestGuardAllowsAddedTests(t *testing.T) {
	repo, baseTree := guardRepo(t)
	writeFile(t, repo, "tests/test_regression.py", "def test_regression():\n    assert True\n")
	rec, report := runGuard(t, repo, baseTree, guardPaths(t))
	if rec.Result.Status != StatusPass {
		t.Fatalf("status = %q, want pass: %+v", rec.Result.Status, report.Violations)
	}
	if rec.Result.Metrics[policy.MetricProtectedAdded] != 1 {
		t.Errorf("protected_added = %d, want 1 (counted, not a violation)",
			rec.Result.Metrics[policy.MetricProtectedAdded])
	}
	if len(report.AllowedAdditions) != 1 || report.AllowedAdditions[0] != "tests/test_regression.py" {
		t.Errorf("allowed_additions = %v, want the new test", report.AllowedAdditions)
	}

	// Under protected_additions "refuse" the same tree is a violation, and
	// the gate reads the policy's choice from the compiled gate rather than
	// reaching back into the document.
	sealed := guardPaths(t)
	sealed.ProtectedAdditions = policy.AdditionsRefuse
	rec2, report2 := runGuard(t, repo, baseTree, sealed)
	if rec2.Result.Status != StatusFail {
		t.Errorf("status under refuse = %q, want fail", rec2.Result.Status)
	}
	if len(report2.Violations) != 1 || report2.Violations[0].Kind != VProtectedAdded {
		t.Errorf("violations under refuse = %+v, want one protected_added", report2.Violations)
	}
}

// A clean tree passes, and the receipt carries the invariants of the kind:
// no process, no exit-code verdict, and the strongest regime there is.
func TestGuardCleanTreeAndReceiptInvariants(t *testing.T) {
	repo, baseTree := guardRepo(t)
	rec, report := runGuard(t, repo, baseTree, guardPaths(t))
	if rec.Result.Status != StatusPass {
		t.Fatalf("status = %q, want pass: %+v", rec.Result.Status, report.Violations)
	}
	if len(rec.Execution.Argv) != 0 {
		t.Errorf("argv = %v, want [] — the guard runs no process", rec.Execution.Argv)
	}
	if rec.Execution.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0 — the guard has no process to exit", rec.Execution.ExitCode)
	}
	if rec.Execution.EvidenceRegime != object.RegimeControlPlane {
		t.Errorf("evidence_regime = %q, want %q", rec.Execution.EvidenceRegime, object.RegimeControlPlane)
	}
	if rec.Execution.EvidencePlugin != "" {
		t.Errorf("evidence_plugin = %q, want empty — no observer ran", rec.Execution.EvidencePlugin)
	}
	if len(rec.Result.Tools) != 0 {
		t.Errorf("tools = %v, want {} — the guard parses no tool output", rec.Result.Tools)
	}
	if rec.Family != policy.FamilyTree {
		t.Errorf("family = %q, want %q", rec.Family, policy.FamilyTree)
	}
	if rec.Result.Metrics[policy.MetricPathsExamined] != 2 {
		t.Errorf("paths_examined = %d, want 2 (the two test files)",
			rec.Result.Metrics[policy.MetricPathsExamined])
	}
	if report.Schema != SchemaTreeGuardReport {
		t.Errorf("report schema = %q, want %q", report.Schema, SchemaTreeGuardReport)
	}
	if report.BaseTree != baseTree {
		t.Errorf("base_tree = %q, want %q", report.BaseTree, baseTree)
	}
}

// The gate reads the receipt. Every violating count must be zero AND
// paths_examined must be present; a required metric that is absent FAILS
// the gate rather than passing it.
func TestPathsUnmodifiedGate(t *testing.T) {
	repo, baseTree := guardRepo(t)
	writeFile(t, repo, "conftest.py", "import atexit\n")
	rec, _ := runGuard(t, repo, baseTree, guardPaths(t))

	gate := policy.Gate{Predicate: policy.GatePathsUnmodified, Basis: object.BasisConstruction}
	rec.Freshness.Basis = object.BasisConstruction
	ok, reason := gate.Eval(&rec)
	if ok {
		t.Fatal("the gate passed a tree with a new conftest.py")
	}
	want := "protected_modified=0 protected_deleted=0 harness_modified=0 harness_deleted=0 harness_added=1 (first: conftest.py)"
	if reason != want {
		t.Errorf("reason =\n %q\nwant\n %q", reason, want)
	}

	// Absence never passes. A receipt with no metrics at all fails the
	// gate on the MISSING METRIC, never on a fabricated zero — and a
	// status=error receipt fails one step earlier, on the status, which is
	// the more fundamental cause and the one an operator should read.
	empty := rec
	empty.Result = object.NewResult(StatusPass)
	ok, reason = gate.Eval(&empty)
	if ok {
		t.Fatal("the gate passed a receipt carrying no metrics")
	}
	if !strings.Contains(reason, "absent (source unavailable)") {
		t.Errorf("reason = %q, want an absence reason", reason)
	}
	errored := rec
	errored.Result = object.NewResult(StatusError)
	if ok, reason := gate.Eval(&errored); ok || reason != "status=error" {
		t.Errorf("an errored guard receipt: ok=%v reason=%q, want the status reason", ok, reason)
	}
}

// A base tree that cannot be read is status=error with NO metrics, so the
// gate fails and the world escalates as machinery rather than being
// reported as a bad candidate.
func TestGuardUnreadableBaseTree(t *testing.T) {
	repo, _ := guardRepo(t)
	rec, report := runGuard(t, repo, "git:"+strings.Repeat("0", 40), guardPaths(t))
	if rec.Result.Status != StatusError {
		t.Fatalf("status = %q, want error", rec.Result.Status)
	}
	if len(rec.Result.Metrics) != 0 {
		t.Errorf("metrics = %v, want none: a tree that could not be read measured nothing", rec.Result.Metrics)
	}
	if len(report.Violations) != 0 {
		t.Errorf("violations = %+v, want none reported from a failed read", report.Violations)
	}
	if rec.Result.Detail == "" {
		t.Error("detail is empty; the operator is owed the reason the tree could not be read")
	}
}

// The report's bytes are canonical and its violations sort by (kind, path)
// — the one artifact in the system whose bytes no candidate influenced.
func TestGuardReportIsCanonicalAndSorted(t *testing.T) {
	repo, baseTree := guardRepo(t)
	writeFile(t, repo, "conftest.py", "x = 1\n")
	writeFile(t, repo, "test_billing.py", "def test_split():\n    assert True\n")
	if err := os.Remove(filepath.Join(repo, "tests/test_more.py")); err != nil {
		t.Fatal(err)
	}
	_, report := runGuard(t, repo, baseTree, guardPaths(t))
	got := make([]string, 0, len(report.Violations))
	for _, v := range report.Violations {
		got = append(got, v.Kind+" "+v.Path)
	}
	want := []string{"harness_added conftest.py", "protected_deleted tests/test_more.py", "protected_modified test_billing.py"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("violations = %v, want %v (sorted by kind then path)", got, want)
	}
	// The stored bytes are canonical JSON: re-canonicalizing the decoded
	// report reproduces them exactly.
	store := newStore(t)
	key, err := store.Put(mustCanonicalPolicy(t, report))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), `{"allowed_additions":`) {
		t.Errorf("canonical report does not start with its first sorted key: %s", raw)
	}
}

// The guard must never execute anything in the world. A world whose
// Command call fails the test proves it structurally.
func TestGuardExecutesNoProcess(t *testing.T) {
	repo, baseTree := guardRepo(t)
	o, err := New(Params{
		Spec: testSpec(KindTreeGuard, ""), CAS: newStore(t),
		Paths: guardPaths(t), Repo: repo, BaseTree: baseTree,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w := &noExecWorld{t: t, dir: repo}
	if _, err := o.Run(context.Background(), w); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// noExecWorld fails the test if anything tries to build a command for it.
type noExecWorld struct {
	t   *testing.T
	dir string
}

func (w *noExecWorld) Tier() string { return object.TierT0Worktree }
func (w *noExecWorld) Dir() string  { return w.dir }
func (w *noExecWorld) Command(argv, env []string) ([]string, []string) {
	w.t.Fatalf("the tree-guard tried to execute %v: it must run NO process in the world", argv)
	return nil, nil
}
func (w *noExecWorld) Kill() error                          { return nil }
func (w *noExecWorld) Caps() object.IsolationCaps           { return object.HostCaps() }
func (w *noExecWorld) EnvDigest(*cas.Store) (string, error) { return "", nil }
func (w *noExecWorld) Close() error                         { return nil }

func writeFile(t *testing.T, repo, rel, body string) {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The contract says `(first: %s)` names the LEXICOGRAPHICALLY first
// offending path. The report artifact sorts by (kind, path), and the two
// orders disagree the moment a late-alphabet harness addition sits beside
// an early-alphabet protected modification — which is when a rationale,
// an explain cell and a worlds GATE column all named the wrong file.
func TestGuardDetailIsLexicographicallyFirstNotKindFirst(t *testing.T) {
	repo, baseTree := guardRepo(t)
	writeFile(t, repo, "zzz/conftest.py", "import atexit\n")
	writeFile(t, repo, "test_billing.py", "def test_split():\n    assert True\n")

	rec, report := runGuard(t, repo, baseTree, guardPaths(t))
	if rec.Result.Status != StatusFail {
		t.Fatalf("status = %q, want fail", rec.Result.Status)
	}
	// Kind order puts harness_added first; path order puts test_billing.py
	// first. The receipt must carry the second.
	if got := report.Violations[0].Path; got != "zzz/conftest.py" {
		t.Fatalf("report.Violations[0].Path = %q, want the kind-sorted first %q "+
			"(the premise of this test is that the two orders differ)", got, "zzz/conftest.py")
	}
	if rec.Result.Detail != "test_billing.py" {
		t.Errorf("detail = %q, want the lexicographically first offender %q",
			rec.Result.Detail, "test_billing.py")
	}
}

// tree_drift is MACHINERY — "an earlier oracle wrote into the tree, or
// something raced us" — not a path violation. Borrowing protected_modified
// for it put a false statement about the candidate's diff into a
// content-addressed receipt and into a signed rationale: the victim of a
// cross-world sabotage was convicted of editing a file it never touched.
func TestGuardTreeDriftIsMachineryNotAPathViolation(t *testing.T) {
	repo, baseTree := guardRepo(t)
	store := newStore(t)
	o, err := New(Params{
		Spec:  testSpec(KindTreeGuard, ""),
		CAS:   store,
		Paths: guardPaths(t),
		Repo:  repo, BaseTree: baseTree,
		// A recorded world tree that is not what the worktree snapshots to.
		CandidateTree: "git:" + strings.Repeat("b", 40),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec, err := o.Run(context.Background(), backend.HostDir(repo))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusError {
		t.Fatalf("status = %q, want error: drift is machinery, and machinery escalates", rec.Result.Status)
	}
	if len(rec.Result.Metrics) != 0 {
		t.Errorf("metrics = %v, want none — no path metric may be borrowed to report drift",
			rec.Result.Metrics)
	}
	if got := rec.Result.Metrics[policy.MetricProtectedModified]; got != 0 {
		t.Errorf("protected_modified = %d, want absent: nothing protected was modified", got)
	}
	if !strings.Contains(rec.Result.Detail, "tree drift") {
		t.Errorf("detail = %q, want it to name the drift", rec.Result.Detail)
	}
	// The gate then fails on the receipt's own status — machinery — and
	// never on a count the guard did not measure.
	gate := policy.Gate{Predicate: policy.GatePathsUnmodified}
	pass, reason := gate.Eval(&rec)
	if pass {
		t.Fatalf("gate passed over a drifted tree (reason %q)", reason)
	}
	if strings.Contains(reason, "protected_modified=1") {
		t.Errorf("gate reason = %q, want no claim that a protected path was modified", reason)
	}
}
