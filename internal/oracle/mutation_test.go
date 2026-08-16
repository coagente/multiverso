package oracle

import (
	"context"
	"encoding/json"
	"fmt"
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

// ---------------------------------------------------------------------------
// Pure: canonical ordering, the per-line cap, and the budget ceiling.
// ---------------------------------------------------------------------------

func mut(path string, line, col int64, op, ref string) Mutant {
	return Mutant{Path: path, Line: line, Col: col, Operator: op, Ref: ref}.WithDigest()
}

// The canonical order is (path, line, col, operator, mutant_digest) and it
// is what makes count-truncation admissible partial evidence: the tested
// set is a DETERMINISTIC prefix, so the same patch and the same tool
// enumerate the same first N mutants on any machine, in any input order.
func TestSelectMutantsCanonicalOrderIsInputIndependent(t *testing.T) {
	ms := []Mutant{
		mut("util.py", 3, 1, "boolean", "u1"),
		mut("stats.py", 12, 15, "constant", "s2"),
		mut("stats.py", 12, 4, "comparison", "s3"),
		mut("stats.py", 9, 20, "arithmetic", "s4"),
	}
	want := []string{"stats.py:9", "stats.py:12", "stats.py:12", "util.py:3"}

	key := func(sel []Mutant) []string {
		out := make([]string, 0, len(sel))
		for _, m := range sel {
			out = append(out, fmt.Sprintf("%s:%d", m.Path, m.Line))
		}
		return out
	}
	if got := key(SelectMutants(ms, 0, 10)); !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	// Reversed input, identical output: the order is a function of the
	// mutants, never of the enumeration.
	rev := []Mutant{ms[3], ms[2], ms[1], ms[0]}
	if got := key(SelectMutants(rev, 0, 10)); !reflect.DeepEqual(got, want) {
		t.Errorf("reversed input order = %v, want %v", got, want)
	}
	// And within one line, the column decides before the operator does.
	sel := SelectMutants(ms, 0, 10)
	if sel[1].Col != 4 || sel[2].Col != 15 {
		t.Errorf("within stats.py:12 the columns are %d,%d, want 4,15", sel[1].Col, sel[2].Col)
	}
}

// max_per_line runs FIRST, then the ceiling: two mutants of one line is a
// diverse sample of that line, twenty is a sample of nothing else.
func TestSelectMutantsPerLineCapThenBudget(t *testing.T) {
	var ms []Mutant
	for i := int64(0); i < 6; i++ {
		ms = append(ms, mut("stats.py", 12, i, "constant", fmt.Sprint(i)))
	}
	for i := int64(0); i < 6; i++ {
		ms = append(ms, mut("stats.py", 13, i, "constant", fmt.Sprint(i)))
	}
	sel := SelectMutants(ms, 2, 3)
	if len(sel) != 3 {
		t.Fatalf("selected %d mutants, want the ceiling of 3", len(sel))
	}
	lines := []int64{sel[0].Line, sel[1].Line, sel[2].Line}
	if !reflect.DeepEqual(lines, []int64{12, 12, 13}) {
		t.Errorf("lines = %v, want [12 12 13]: the per-line cap applies before the ceiling", lines)
	}
	// The ceiling is a CEILING: a population under it is taken whole.
	if got := len(SelectMutants(ms, 2, 20)); got != 4 {
		t.Errorf("selected %d, want 4 (2 per line over 2 lines)", got)
	}
}

// The digest is part of the order, so two mutants that agree on everything
// else still have a total, stable order — no coin flip decides which one
// the budget buys.
func TestSelectMutantsDigestBreaksTies(t *testing.T) {
	a := mut("s.py", 1, 1, "constant", "a")
	b := mut("s.py", 1, 1, "constant", "b")
	if a.Digest == b.Digest {
		t.Fatal("two distinct mutants share a digest")
	}
	first := SelectMutants([]Mutant{a, b}, 0, 1)
	second := SelectMutants([]Mutant{b, a}, 0, 1)
	if len(first) != 1 || len(second) != 1 || first[0].Ref != second[0].Ref {
		t.Errorf("tie broken differently by input order: %+v vs %+v", first, second)
	}
}

// A tool asked to mutate a file may offer mutants of lines the candidate
// never touched. Those are somebody else's code, and counting them would
// put a neighbour's lines in this candidate's denominator.
func TestFilterToTargetDropsOutOfDiffMutants(t *testing.T) {
	set := DiffTargets([]byte(patchTwoFiles), testPaths(t))
	in := []Mutant{
		mut("stats.py", 11, 1, "constant", "in"),
		mut("stats.py", 40, 1, "constant", "out"),
		{Operator: "mutmut", Ref: "7"}, // tool selection: no position to filter on
	}
	got := FilterToTarget(in, set)
	if len(got) != 2 || got[0].Ref != "in" || got[1].Ref != "7" {
		t.Errorf("filtered = %+v, want the in-diff mutant and the position-less one", got)
	}
}

// ---------------------------------------------------------------------------
// Pure: the adapter parsers, over RECORDED tool output. Neither cosmic-ray
// nor mutmut is installed here, and no test may require one.
// ---------------------------------------------------------------------------

func readMutationFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mutation", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func TestCosmicRayParseEnumeration(t *testing.T) {
	ms, err := cosmicRayTool{}.ParseEnumeration(readMutationFixture(t, "cosmic-dump.jsonl"))
	if err != nil {
		t.Fatalf("ParseEnumeration: %v", err)
	}
	if len(ms) != 5 {
		t.Fatalf("parsed %d mutants, want 5", len(ms))
	}
	want := Mutant{Path: "stats.py", Line: 12, Col: 11, Operator: "core/NumberReplacer",
		Ref: "stats.py:core/NumberReplacer:0"}
	got := ms[0]
	got.Digest = ""
	if got != want {
		t.Errorf("mutant[0] = %+v, want %+v", got, want)
	}
	if ms[0].Digest == "" {
		t.Error("mutant carries no digest: the canonical order has no tiebreak")
	}
	if ms[0].Occurrence() != 0 || ms[1].Occurrence() != 1 {
		t.Errorf("occurrences = %d,%d, want 0,1", ms[0].Occurrence(), ms[1].Occurrence())
	}
}

// `cosmic-ray dump` emits [work_item, result] pairs once a session has run.
// The item half is read; the result half is NOT — a result in an
// enumeration dump is a result from a session we did not run, and reading
// it would let a planted session author our counts (corpus vector 15).
func TestCosmicRayParseEnumerationPairedShape(t *testing.T) {
	ms, err := cosmicRayTool{}.ParseEnumeration(readMutationFixture(t, "cosmic-dump-paired.jsonl"))
	if err != nil {
		t.Fatalf("ParseEnumeration: %v", err)
	}
	if len(ms) != 2 || ms[1].Line != 14 {
		t.Fatalf("parsed %+v, want two positioned mutants", ms)
	}
}

// A dump shape we do not recognize yields an ERROR and no mutants. A silent
// zero would read as "your diff is unmutable", which is a statement about
// the candidate we would have no right to make.
func TestCosmicRayParseEnumerationUnknownShapeIsAbsenceNotZero(t *testing.T) {
	ms, err := cosmicRayTool{}.ParseEnumeration(readMutationFixture(t, "cosmic-dump-unknown-shape.jsonl"))
	if err == nil {
		t.Fatalf("unknown dump shape parsed to %d mutants, want an error", len(ms))
	}
	if len(ms) != 0 {
		t.Errorf("mutants = %+v, want none", ms)
	}
}

func TestCosmicRayParseExecSuppliesTheDiffNotTheVerdict(t *testing.T) {
	claim, err := cosmicRayTool{}.ParseExec(readMutationFixture(t, "cosmic-worker-survived.json"))
	if err != nil {
		t.Fatalf("ParseExec: %v", err)
	}
	if !strings.Contains(claim.Diff, "hi + 1") {
		t.Errorf("diff = %q, want the mutant's own diff", claim.Diff)
	}
	if claim.Outcome != "survived" {
		t.Errorf("outcome = %q, want the tool's unmapped word", claim.Outcome)
	}
}

func TestMutmutParseEnumeration(t *testing.T) {
	ms, err := mutmutTool{}.ParseEnumeration(readMutationFixture(t, "mutmut-result-ids.txt"))
	if err != nil {
		t.Fatalf("ParseEnumeration: %v", err)
	}
	if len(ms) != 7 || ms[0].Ref != "1" {
		t.Fatalf("parsed %+v, want 7 ids starting at 1", ms)
	}
	// mutmut reports ids, not positions — which is exactly why its receipts
	// record the weaker mutant_selection and why max_per_line cannot apply.
	if ms[0].Path != "" || ms[0].Line != 0 {
		t.Errorf("mutmut mutant carries a position it cannot know: %+v", ms[0])
	}
}

func TestMutmutParseEnumerationNoIDsIsAnError(t *testing.T) {
	if ms, err := (mutmutTool{}).ParseEnumeration(readMutationFixture(t, "mutmut-result-ids-empty.txt")); err == nil {
		t.Errorf("parsed %+v, want an error: 'mutmut generated nothing' and 'we did not understand mutmut' must not share a zero", ms)
	}
}

// The adapters' argv is their declared contract, pinned here because
// neither tool is installed to check it against. Two properties are
// load-bearing rather than cosmetic: the tool's state directory is in
// CONTROL-PLANE SCRATCH (decision 18, corpus vector 15), and the per-mutant
// test command carries the control-plane observer.
func TestAdapterArgvKeepsStateOutOfTheWorktree(t *testing.T) {
	run := MutationRun{
		Spec:       policy.ResolvedMutation(object.OracleSpec{Kind: policy.KindMutationDiff}),
		SessionDir: "/mvo/scratch/mutation-diff/mutation-session",
		ConfigPath: "/mvo/scratch/mutation-diff/mutation-tool.conf",
		PatchPath:  "/mvo/scratch/mutation-diff/captured.patch",
		TestArgv:   []string{"python3", "-m", "pytest", "-p", "mvo_evidence", "-q"},
		Paths:      []string{"stats.py"},
		TimeoutSec: 5,
	}
	target := TargetSet{Files: []TargetFile{{Path: "stats.py", Ranges: []LineRange{{Start: 1, End: 3}}}}}

	cosmic := cosmicRayTool{}
	for _, step := range cosmic.EnumerateSteps("python3", target, run) {
		joined := strings.Join(step.Argv, " ")
		if strings.Contains(joined, ".mutmut-cache") {
			t.Errorf("cosmic-ray argv names a mutmut cache: %s", joined)
		}
		if strings.Contains(joined, "session") && !strings.Contains(joined, "/mvo/scratch/") {
			t.Errorf("session path is not in control-plane scratch: %s", joined)
		}
	}
	cfg := string(cosmic.Config(target, run))
	if !strings.Contains(cfg, "mvo_evidence") {
		t.Errorf("cosmic-ray test-command does not load the observer:\n%s", cfg)
	}
	if strings.Contains(cfg, "incremental") || strings.Contains(cfg, "resume") {
		t.Errorf("cosmic-ray config enables incremental state, which decision 18 forbids:\n%s", cfg)
	}
	mm := strings.Join(mutmutTool{}.ExecArgv("python3", Mutant{Ref: "3"}, run), " ")
	if !strings.Contains(mm, "--cache-path /mvo/scratch/") {
		t.Errorf("mutmut argv does not pin its cache into scratch: %s", mm)
	}
	if !strings.Contains(mm, "mvo_evidence") {
		t.Errorf("mutmut runner does not load the observer: %s", mm)
	}
}

// The two adapters differ in what they can promise, and the receipt says
// which one ran. Under mutmut the control plane cannot order or per-line
// cap the population, so the label is the weaker one — an honest label on
// strictly weaker provenance rather than a silent equivalence (decision 19).
func TestAdapterSelectionLabels(t *testing.T) {
	if (cosmicRayTool{}).Selection() != SelectionControlPlane {
		t.Error("cosmic-ray must record control-plane selection")
	}
	if (mutmutTool{}).Selection() != SelectionTool {
		t.Error("mutmut must record tool selection")
	}
}

// ---------------------------------------------------------------------------
// The oracle, end to end, against a fake interpreter. No real tool runs.
// ---------------------------------------------------------------------------

// fakeMutation is a stand-in interpreter that plays three roles: the tools
// probe, the mutation tool, and pytest. Each pytest invocation pops the
// next verdict from a list, which is how a baseline plus N mutant runs are
// scripted without a mutation tool being installed.
type fakeMutation struct {
	probe    map[string]string
	verdicts []string // per pytest run: "pass" | "fail" | "silent" | "empty" | "hang"
	dump     string   // what `cosmic_ray.cli dump` prints
}

func (f fakeMutation) write(t *testing.T) string {
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
	verdicts := filepath.Join(dir, "verdicts")
	if err := os.WriteFile(verdicts, []byte(strings.Join(f.verdicts, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(dir, "dump.jsonl")
	if err := os.WriteFile(dump, []byte(f.dump), 0o644); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(dir, "counter")
	script := `#!/bin/sh
# fake python3 for the mutation oracle's tests: not an interpreter, not a tool.
if [ "$1" = "-c" ]; then
cat <<'MVO_PROBE'
` + probeJSON + `
MVO_PROBE
exit 0
fi
# The tool half. init records which module a session was opened over and
# dump replays only that module work items, exactly as a real session would:
# a fake that returned every file mutants for every session would make the
# oracle look like it double-counts.
case "$2" in
cosmic_ray.cli)
  case "$3" in
    init) echo "$6" > "$8.module" ; exit 0 ;;
    dump) grep -F "\"module_path\":\"$(cat "$4.module")\"" ` + dump + ` ; exit 0 ;;
    # worker applies one mutant and runs the configured test command, so it
    # falls through to the pytest half below ON PURPOSE: the stream the
    # control plane classifies from is written by that run.
    worker) printf '{"worker_outcome":"normal","test_outcome":"unknown","diff":"--- a/stats.py\\n+++ b/stats.py"}\n' ;;
  esac
  ;;
esac
n=$(cat ` + counter + ` 2>/dev/null || echo 0)
n=$((n+1))
echo $n > ` + counter + `
verdict=$(sed -n "${n}p" ` + verdicts + `)
if [ -z "$verdict" ]; then verdict=pass; fi
if [ "$verdict" = "hang" ]; then sleep 30; fi
if [ "$verdict" != "silent" ] && [ -n "$MVO_EVIDENCE_STREAM" ]; then
  printf 'mvo-evidence/v0\t%s\n' "$MVO_EVIDENCE_NONCE" >> "$MVO_EVIDENCE_STREAM"
  printf '1\tsession_start\t{"pid":1}\n' >> "$MVO_EVIDENCE_STREAM"
  case "$verdict" in
    pass)
      printf '2\ttest\t{"nodeid":"t.py::a","outcome":"passed"}\n' >> "$MVO_EVIDENCE_STREAM"
      printf '3\tsession_finish\t{"duration_ms":3,"errored":0,"exitstatus":0,"failed":0,"passed":1,"skipped":0,"total":1}\n' >> "$MVO_EVIDENCE_STREAM"
      ;;
    fail)
      printf '2\ttest\t{"nodeid":"t.py::a","outcome":"failed"}\n' >> "$MVO_EVIDENCE_STREAM"
      printf '3\tsession_finish\t{"duration_ms":3,"errored":0,"exitstatus":1,"failed":1,"passed":0,"skipped":0,"total":1}\n' >> "$MVO_EVIDENCE_STREAM"
      ;;
    empty)
      printf '2\tsession_finish\t{"duration_ms":1,"errored":0,"exitstatus":5,"failed":0,"passed":0,"skipped":0,"total":0}\n' >> "$MVO_EVIDENCE_STREAM"
      ;;
  esac
fi
case "$verdict" in
  fail) exit 1 ;;
  empty) exit 5 ;;
esac
exit 0
`
	path := filepath.Join(dir, "python3")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// mutationInstance wires an instance the way the race orchestrator does,
// with control-plane-owned evidence and scratch directories.
func mutationInstance(t *testing.T, python string, patch []byte, mspec object.MutationSpec) *mutationOracle {
	t.Helper()
	root := t.TempDir()
	o := &mutationOracle{
		spec: policy.Oracle{
			Name: "mutate", Kind: KindMutationDiff, Family: policy.FamilyMutation,
			Config:   "mv0:" + strings.Repeat("7", 64),
			Argv:     []string{python, "-m", "pytest"},
			Args:     []string{},
			Mutation: policy.ResolvedMutation(object.OracleSpec{Kind: policy.KindMutationDiff, Mutation: mspec}),
		},
		store:   newStore(t),
		patch:   patch,
		paths:   testPaths(t),
		timeout: 30 * time.Second,
		cap:     artifactCapBytes,
		ev: evidencePlan{
			regime:       object.RegimeStreamed,
			crosscheck:   policy.CrosscheckRequire,
			autoload:     policy.AutoloadOff,
			hostEvidence: filepath.Join(root, "evidence"),
			hostScratch:  filepath.Join(root, "scratch"),
			pluginDir:    filepath.Join(root, "plugin"),
			pluginDigest: "sha256:" + strings.Repeat("a", 64),
		},
	}
	return o
}

// THE DEGRADED PATH. Neither cosmic-ray nor mutmut is installed on this
// machine, so this is the live path rather than a hypothetical: the run
// ends `error`, every derived metric is ABSENT (never a zero the survivor
// gate would read as a pass), and the two CONTROL-PLANE facts survive
// because they were true before the toolchain was consulted.
func TestMutationMissingToolchainDegradesToAbsence(t *testing.T) {
	py := fakeMutation{probe: map[string]string{"pytest": "8.4.0"}}.write(t)
	o := mutationInstance(t, py, []byte(patchTwoFiles), object.MutationSpec{})

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusError {
		t.Errorf("status = %s, want %s: a missing toolchain is machinery, never a failing candidate",
			rec.Result.Status, StatusError)
	}
	for _, m := range []string{
		policy.MetricMutantsSurvived, policy.MetricMutantsKilled, policy.MetricMutantsTested,
		policy.MetricMutantsCandidates, policy.MetricMutationScoreBP,
	} {
		if v, ok := rec.Result.Metrics[m]; ok {
			t.Errorf("%s = %d is present; a missing tool must yield an ABSENT metric, never a zero", m, v)
		}
	}
	if rec.Result.Metrics[policy.MetricMutationLinesTargeted] != 4 {
		t.Errorf("mutation_lines_targeted = %v, want 4: it is derived from the captured patch, not from the tool",
			rec.Result.Metrics[policy.MetricMutationLinesTargeted])
	}
	if rec.Result.Metrics[policy.MetricMutantsBudget] != policy.DefaultMaxMutants {
		t.Errorf("mutants_budget = %v, want the pinned %d", rec.Result.Metrics[policy.MetricMutantsBudget], policy.DefaultMaxMutants)
	}
	// And the gate that reads the absent metric FAILS, with a reason that
	// says the source was unavailable rather than blaming the candidate.
	// The gate fails, and it fails for the honest reason: an inconclusive
	// run is refused before its absent metric is even consulted (M1e's
	// "every predicate first requires that the receipt did not error").
	gate := policy.Gate{Predicate: policy.GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction}
	ok, reason := gate.Eval(&rec)
	if ok || reason != "status=error" {
		t.Errorf("gate = (%v, %q), want a failure on the errored receipt", ok, reason)
	}
	// And with the status set aside, the metric really is absent rather
	// than a zero a survivor gate would have read as a pass.
	clean := rec
	clean.Result.Status = StatusPass
	if ok, reason := gate.Eval(&clean); ok || !strings.Contains(reason, "absent") {
		t.Errorf("gate on the same metrics = (%v, %q), want a failure naming the absent metric", ok, reason)
	}
	notes := storedNotes(t, o.store, rec)
	if !strings.Contains(notes, "machinery") {
		t.Errorf("stderr artifact does not explain the degradation:\n%s", notes)
	}
}

// THE BUDGET CAP, and the freshness basis a partial result carries.
// Count-truncation is admissible partial evidence: the tested set is a
// deterministic, control-plane-selected prefix, so the receipt keeps
// basis=construction and records all three numbers — the ceiling, the
// population, and what actually ran.
func TestMutationBudgetCapIsPartialEvidenceWithConstructionBasis(t *testing.T) {
	dump := string(readMutationFixture(t, "cosmic-dump.jsonl"))
	py := fakeMutation{
		probe: map[string]string{"pytest": "8.4.0", "cosmic-ray": "8.3.7"},
		dump:  dump,
		// 1 baseline + 2 mutants, all killed.
		verdicts: []string{"pass", "fail", "fail"},
	}.write(t)
	o := mutationInstance(t, py, []byte(patchTwoFiles), object.MutationSpec{MaxMutants: 2})

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusPass {
		t.Fatalf("status = %s (%s), want pass", rec.Result.Status, storedNotes(t, o.store, rec))
	}
	m := rec.Result.Metrics
	// The fixture enumerates 5 mutants; 3 of them fall inside the diff
	// (stats.py:9 and stats.py:12 ×2 — util.py:3 is outside it), and the
	// pinned ceiling buys 2.
	if m[policy.MetricMutantsCandidates] != 3 {
		t.Errorf("mutants_candidates = %d, want 3", m[policy.MetricMutantsCandidates])
	}
	if m[policy.MetricMutantsBudget] != 2 || m[policy.MetricMutantsTested] != 2 {
		t.Errorf("budget/tested = %d/%d, want 2/2", m[policy.MetricMutantsBudget], m[policy.MetricMutantsTested])
	}
	if m[policy.MetricMutantsTested] > m[policy.MetricMutantsBudget] {
		t.Error("the scheduler spent over the pinned ceiling")
	}
	// A partial result is still CONSTRUCTION evidence: what it tested, it
	// tested against the exact tree. What makes it partial is in the
	// metrics, not smuggled into a weaker freshness word.
	if rec.Freshness.Basis != object.BasisConstruction {
		t.Errorf("basis = %q, want %q: count-truncation is deterministic and replayable",
			rec.Freshness.Basis, object.BasisConstruction)
	}
	if rec.Cost.Unit != policy.UnitMutants || rec.Cost.Units != 2 {
		t.Errorf("cost = %+v, want 2 mutants: the scheduler's denominator", rec.Cost)
	}
	// The reader can see HOW partial without consulting the policy.
	notes := storedNotes(t, o.store, rec)
	if !strings.Contains(notes, "count-truncation") {
		t.Errorf("stderr artifact does not name the truncation:\n%s", notes)
	}
	var report MutationReport
	loadArtifact(t, o.store, rec.Result.Artifacts[0], &report)
	if report.Truncated != 1 || report.Candidates != 3 {
		t.Errorf("report truncation = %d of %d, want 1 of 3", report.Truncated, report.Candidates)
	}
	if !report.MaxPerLineApplied {
		t.Error("cosmic-ray selection must apply the per-line cap")
	}
}

// Clock-truncation is NOT partial evidence. A run the wall clock stopped
// tested whatever the machine got through, which is not reproducible and
// not a fact about the candidate: status error, metrics absent. Determinism
// is the dividing line between the two truncations.
func TestMutationClockTruncationIsMachineryError(t *testing.T) {
	py := fakeMutation{
		probe:    map[string]string{"pytest": "8.4.0", "cosmic-ray": "8.3.7"},
		dump:     string(readMutationFixture(t, "cosmic-dump.jsonl")),
		verdicts: []string{"pass", "fail", "fail", "fail"},
	}.write(t)
	o := mutationInstance(t, py, []byte(patchTwoFiles), object.MutationSpec{})

	// The oracle's wall bound expires once the mutant phase has begun. It
	// is driven by a world that cancels on the Nth in-world command —
	// probe, baseline, init, dump — rather than by a sleep, so the test
	// asserts the rule instead of racing a clock.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &cancelOnArgvWorld{World: backend.HostDir(t.TempDir()), match: "worker", cancel: cancel}

	rec, err := o.Run(ctx, w)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusError {
		t.Errorf("status = %s, want error", rec.Result.Status)
	}
	for _, m := range []string{policy.MetricMutantsSurvived, policy.MetricMutationScoreBP, policy.MetricMutantsTested} {
		if _, ok := rec.Result.Metrics[m]; ok {
			t.Errorf("%s survived a clock-truncated run; a wall-clock prefix is not evidence", m)
		}
	}
	if notes := storedNotes(t, o.store, rec); !strings.Contains(notes, "wall clock") {
		t.Errorf("stderr artifact does not name the clock truncation:\n%s", notes)
	}
}

// An empty target set passes VACUOUSLY with the score absent: a patch that
// changed no mutable source line has nothing for mutation to say about it,
// and a fabricated 100% would be exactly the over-claim this project exists
// to remove. No process runs at all, so the rung is free.
func TestMutationEmptyTargetSetPassesVacuously(t *testing.T) {
	const testOnly = `diff --git a/tests/test_stats.py b/tests/test_stats.py
--- a/tests/test_stats.py
+++ b/tests/test_stats.py
@@ -1,2 +1,2 @@
 def test_x():
-    assert clamp(5, 0, 10) == 5
+    assert True
`
	py := fakeMutation{probe: map[string]string{"pytest": "8.4.0"}}.write(t)
	o := mutationInstance(t, py, []byte(testOnly), object.MutationSpec{})

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusPass {
		t.Errorf("status = %s, want pass", rec.Result.Status)
	}
	if got := rec.Result.Metrics[policy.MetricMutantsCandidates]; got != 0 {
		t.Errorf("mutants_candidates = %d, want 0", got)
	}
	if _, ok := rec.Result.Metrics[policy.MetricMutationScoreBP]; ok {
		t.Error("mutation_score_bp is present over an empty denominator: a ratio over nothing is not a zero score")
	}
	gate := policy.Gate{Predicate: policy.GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction}
	if ok, reason := gate.Eval(&rec); !ok {
		t.Errorf("the survivor gate failed on an empty target set: %s", reason)
	}
	if len(rec.Execution.Argv) > 0 {
		t.Errorf("argv = %v, want none: an empty target set costs no process", rec.Execution.Argv)
	}
}

// A red baseline is `error`, not a score. Mutation over a suite that
// already fails measures nothing at all, and every mutant would look
// "killed" by a failure that was there before the mutation.
func TestMutationRedBaselineIsError(t *testing.T) {
	py := fakeMutation{
		probe:    map[string]string{"pytest": "8.4.0", "cosmic-ray": "8.3.7"},
		dump:     string(readMutationFixture(t, "cosmic-dump.jsonl")),
		verdicts: []string{"fail"},
	}.write(t)
	o := mutationInstance(t, py, []byte(patchTwoFiles), object.MutationSpec{})

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusError {
		t.Errorf("status = %s, want error", rec.Result.Status)
	}
	if _, ok := rec.Result.Metrics[policy.MetricMutantsSurvived]; ok {
		t.Error("a red baseline produced a survivor count")
	}
	if notes := storedNotes(t, o.store, rec); !strings.Contains(notes, "red suite") {
		t.Errorf("stderr artifact does not explain the refusal:\n%s", notes)
	}
}

// THE TRUST-BOUNDARY POSTURE. Three claims, each asserted rather than
// described: the metrics that reach a gate are stream-derived or
// control-plane-derived; the receipt names its inputs' provenance; and a
// silenced observer NEVER resolves to the outcome that helps the candidate.
func TestMutationTrustBoundaryPosture(t *testing.T) {
	py := fakeMutation{
		probe:    map[string]string{"pytest": "8.4.0", "cosmic-ray": "8.3.7"},
		dump:     string(readMutationFixture(t, "cosmic-dump.jsonl")),
		verdicts: []string{"pass", "fail", "pass", "fail"},
	}.write(t)
	o := mutationInstance(t, py, []byte(patchTwoFiles), object.MutationSpec{})

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusPass {
		t.Fatalf("status = %s (%s)", rec.Result.Status, storedNotes(t, o.store, rec))
	}
	if rec.Execution.EvidenceRegime != object.RegimeStreamed {
		t.Errorf("evidence_regime = %q, want %q", rec.Execution.EvidenceRegime, object.RegimeStreamed)
	}
	if rec.Execution.EvidencePlugin == "" {
		t.Error("the receipt does not say WHICH observer saw the mutant runs")
	}
	// Provenance: what was in scope, and who chose the mutants.
	if rec.Inputs[object.InputDiffTarget] == "" {
		t.Error("inputs[diff_target] is empty: a reader cannot check what was in scope")
	}
	if got := rec.Inputs[object.InputMutantSelection]; got != SelectionControlPlane {
		t.Errorf("inputs[mutant_selection] = %q, want %q", got, SelectionControlPlane)
	}
	// The correlation descriptor is scheduler metadata and says the truth
	// about this rung: control-plane-chosen inputs, candidate-process
	// execution.
	if rec.Correlation.Generator != policy.GeneratorControlPlane ||
		rec.Correlation.Executor != policy.ExecutorCandidateProcess ||
		rec.Correlation.Signal != policy.SignalSuiteAdequacy {
		t.Errorf("correlation = %+v, want control-plane/candidate-process/suite-adequacy", rec.Correlation)
	}
	// Every metric is in the kind's declared vocabulary — an implementation
	// that emitted a name outside it would be a metric no gate can read.
	vocab := map[string]bool{}
	for _, m := range MetricVocabulary(KindMutationDiff) {
		vocab[m] = true
	}
	for name := range rec.Result.Metrics {
		if !vocab[name] {
			t.Errorf("metric %q is outside the declared vocabulary of %s", name, KindMutationDiff)
		}
	}
	// One killed, one survived: the score is a ratio over the two, and the
	// survivor is in the report with its diff.
	m := rec.Result.Metrics
	if m[policy.MetricMutantsKilled] != 1 || m[policy.MetricMutantsSurvived] != 1 {
		t.Fatalf("killed/survived = %d/%d, want 1/1", m[policy.MetricMutantsKilled], m[policy.MetricMutantsSurvived])
	}
	if m[policy.MetricMutationScoreBP] != 5000 {
		t.Errorf("mutation_score_bp = %d, want 5000", m[policy.MetricMutationScoreBP])
	}
	var report MutationReport
	loadArtifact(t, o.store, rec.Result.Artifacts[0], &report)
	if len(report.Survivors) != 1 || report.Survivors[0].Diff == "" {
		t.Errorf("survivors = %+v, want one with its diff — the actionable half", report.Survivors)
	}
}

// The attack this rung must not be open to: silence the observer on every
// mutant run and exit non-zero, and every mutant would "look killed" —
// fewer survivors, a passing gate. Absence never passes, INCLUDING when the
// absence is convenient, so the whole run errors instead.
func TestMutationSilencedObserverNeverCountsAsKilled(t *testing.T) {
	py := fakeMutation{
		probe:    map[string]string{"pytest": "8.4.0", "cosmic-ray": "8.3.7"},
		dump:     string(readMutationFixture(t, "cosmic-dump.jsonl")),
		verdicts: []string{"pass", "silent", "silent", "silent"},
	}.write(t)
	o := mutationInstance(t, py, []byte(patchTwoFiles), object.MutationSpec{})

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusError {
		t.Errorf("status = %s, want error", rec.Result.Status)
	}
	if v, ok := rec.Result.Metrics[policy.MetricMutantsKilled]; ok {
		t.Errorf("mutants_killed = %d: a mutant that could not be observed was counted as caught", v)
	}
	gate := policy.Gate{Predicate: policy.GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction}
	if ok, _ := gate.Eval(&rec); ok {
		t.Error("the survivor gate PASSED on a run whose observer was silenced")
	}
}

// A mutant that empties the suite is `unviable`, not `killed`: nothing ran,
// so nothing caught it, and it stays out of the score's denominator.
func TestMutationUnviableMutantIsExcludedFromTheDenominator(t *testing.T) {
	py := fakeMutation{
		probe:    map[string]string{"pytest": "8.4.0", "cosmic-ray": "8.3.7"},
		dump:     string(readMutationFixture(t, "cosmic-dump.jsonl")),
		verdicts: []string{"pass", "empty", "empty", "empty"},
	}.write(t)
	o := mutationInstance(t, py, []byte(patchTwoFiles), object.MutationSpec{})

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	m := rec.Result.Metrics
	if m[policy.MetricMutantsUnviable] != 2 {
		t.Errorf("mutants_unviable = %d, want 2 (%s)", m[policy.MetricMutantsUnviable], storedNotes(t, o.store, rec))
	}
	if _, ok := m[policy.MetricMutationScoreBP]; ok {
		t.Error("mutation_score_bp is present with an empty denominator")
	}
	if m[policy.MetricMutantsKilled] != 0 || m[policy.MetricMutantsSurvived] != 0 {
		t.Errorf("killed/survived = %d/%d, want 0/0", m[policy.MetricMutantsKilled], m[policy.MetricMutantsSurvived])
	}
}

// The patch is the target set's only source, and an UNSUPPLIED patch must
// never degrade into an empty one: that would pass the survivor gate
// vacuously on a machinery failure.
func TestMutationRefusesAnUnsuppliedPatch(t *testing.T) {
	_, err := New(Params{
		Spec: policy.Oracle{
			Name: "mutate", Kind: KindMutationDiff, Config: "mv0:x",
			Mutation: policy.ResolvedMutation(object.OracleSpec{Kind: policy.KindMutationDiff}),
		},
		CAS:         newStore(t),
		EvidenceDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "captured patch") {
		t.Errorf("New = %v, want a refusal naming the missing patch", err)
	}
	// An EMPTY patch is legal: the candidate changed nothing.
	if _, err := New(Params{
		Spec: policy.Oracle{
			Name: "mutate", Kind: KindMutationDiff, Config: "mv0:x",
			Mutation: policy.ResolvedMutation(object.OracleSpec{Kind: policy.KindMutationDiff}),
		},
		CAS:         newStore(t),
		Patch:       []byte{},
		EvidenceDir: t.TempDir(),
	}); err != nil {
		t.Errorf("New with an empty patch = %v, want an instance", err)
	}
}

// ---------------------------------------------------------------------------
// Test plumbing.
// ---------------------------------------------------------------------------

// cancelOnArgvWorld cancels the run's context the first time the world is
// asked to run a command containing a marker word — here, the first mutant
// execution. It is how the wall-clock path is driven deterministically: no
// sleep, no timing assumption, and no seam inside the oracle that
// production code would have to carry.
type cancelOnArgvWorld struct {
	backend.World
	match  string
	cancel context.CancelFunc
	fired  bool
}

func (w *cancelOnArgvWorld) Command(argv, env []string) ([]string, []string) {
	if !w.fired {
		for _, a := range argv {
			if a == w.match {
				w.fired = true
				w.cancel()
				break
			}
		}
	}
	return w.World.Command(argv, env)
}

func loadBytes(t *testing.T, store artifactStore, key string) []byte {
	t.Helper()
	s, ok := store.(*cas.Store)
	if !ok {
		t.Fatalf("store is %T, not a CAS", store)
	}
	b, err := s.Get(key)
	if err != nil {
		t.Fatalf("cas get %s: %v", key, err)
	}
	return b
}

// storedNotes is the receipt's stderr artifact: where every degradation
// reason lands, so an operator reads the WHY in the same bytes as the what.
func storedNotes(t *testing.T, store artifactStore, rec object.Receipt) string {
	t.Helper()
	if len(rec.Result.Artifacts) < 2 {
		return ""
	}
	return string(loadBytes(t, store, rec.Result.Artifacts[1]))
}

func loadArtifact(t *testing.T, store artifactStore, key string, v any) {
	t.Helper()
	if err := json.Unmarshal(loadBytes(t, store, key), v); err != nil {
		t.Fatalf("decode artifact %s: %v", key, err)
	}
}

// Corpus vector 14, `mutation_padding`: pad the diff with lines whose every
// mutant is trivially killed, inflating mutation_score_bp.
//
// NEUTRALIZED, and the mechanism is asserted rather than asserted-in-prose.
// The only mutation gate reads the ABSOLUTE survivor count, so padding
// cannot reduce it — it can only add mutants that may themselves survive,
// which makes the gate strictly HARDER. The ratio moves, and nothing reads
// the ratio (decision 10, and TestNoRankingKeyReadsTheNewMetrics next door
// in internal/policy).
func TestMutationPaddingCannotBuyAPass(t *testing.T) {
	gate := policy.Gate{Predicate: policy.GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction}

	// The honest patch: two mutants, one survives.
	honest := &object.Receipt{
		Result: object.Result{Status: StatusPass, Metrics: map[string]int64{
			policy.MetricMutantsKilled: 1, policy.MetricMutantsSurvived: 1,
			policy.MetricMutantsTimeout: 0, policy.MetricMutationScoreBP: 5000,
		}},
		Freshness: object.Freshness{Basis: object.BasisConstruction},
	}
	// The same patch, padded with 98 trivially-killed lines. The RATIO goes
	// from 50% to 99%; the survivor count does not move, so the gate's
	// verdict does not move either.
	padded := &object.Receipt{
		Result: object.Result{Status: StatusPass, Metrics: map[string]int64{
			policy.MetricMutantsKilled: 99, policy.MetricMutantsSurvived: 1,
			policy.MetricMutantsTimeout: 0, policy.MetricMutationScoreBP: 9900,
		}},
		Freshness: object.Freshness{Basis: object.BasisConstruction},
	}
	okHonest, whyHonest := gate.Eval(honest)
	okPadded, whyPadded := gate.Eval(padded)
	if okHonest || okPadded {
		t.Fatalf("padding bought a pass: honest=(%v,%q) padded=(%v,%q)", okHonest, whyHonest, okPadded, whyPadded)
	}
	if whyHonest != whyPadded {
		t.Errorf("the fail reason moved under padding: %q vs %q", whyHonest, whyPadded)
	}
	// And the direction that matters: padding a diff can only ADD mutants,
	// each of which may survive, so the gate is strictly harder to satisfy.
	unlucky := &object.Receipt{
		Result: object.Result{Status: StatusPass, Metrics: map[string]int64{
			policy.MetricMutantsKilled: 99, policy.MetricMutantsSurvived: 2,
			policy.MetricMutantsTimeout: 0, policy.MetricMutationScoreBP: 9801,
		}},
		Freshness: object.Freshness{Basis: object.BasisConstruction},
	}
	if _, why := gate.Eval(unlucky); why != "mutants_survived=2 mutants_timeout=0 (want survived+timeout <= 0)" {
		t.Errorf("reason = %q, want the higher survivor count", why)
	}
}

// A MUTANT THAT HANGS IS NOT A MUTANT THE TESTS KILLED, and the gate must
// say so.
//
// The escape a red team ran end to end: the candidate authors both the
// baseline duration and the mutated code's runtime, so it can make one
// mutant exceed 5 × the baseline. runMutant classifies that as
// MutantTimeout, which is its own bucket and — correctly, per decision 17 —
// outside mutation_score_bp's denominator. What was NOT correct is that the
// bucket reached no gate: `mutation-survivors-not-above` is the only
// mutation gate M2a ships, it read `mutants_survived` alone, and a receipt
// asserting a 100 % score beside an escaped mutant PASSED it.
//
// Three assertions, in the order a reader needs them: the mutant is counted
// as a timeout, the gate FAILS naming both numbers, and the report lists the
// mutant so `survivors: 0` is never printed beside something nothing killed.
func TestMutationTimeoutReachesTheGate(t *testing.T) {
	py := fakeMutation{
		probe: map[string]string{"pytest": "8.4.0", "cosmic-ray": "8.3.7"},
		dump:  string(readMutationFixture(t, "cosmic-dump.jsonl")),
		// baseline green, first mutant killed, second mutant hangs.
		verdicts: []string{"pass", "fail", "hang"},
	}.write(t)
	o := mutationInstance(t, py, []byte(patchTwoFiles),
		object.MutationSpec{MaxMutants: 2, TimeoutPerMutant: 400})

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusPass {
		t.Fatalf("status = %s (%s), want pass: a PER-mutant timeout is normal and is not machinery",
			rec.Result.Status, storedNotes(t, o.store, rec))
	}
	m := rec.Result.Metrics
	if m[policy.MetricMutantsTimeout] != 1 || m[policy.MetricMutantsKilled] != 1 {
		t.Fatalf("killed/timeout = %d/%d, want 1/1", m[policy.MetricMutantsKilled], m[policy.MetricMutantsTimeout])
	}
	if m[policy.MetricMutantsSurvived] != 0 {
		t.Fatalf("mutants_survived = %d, want 0: the buckets stay separate", m[policy.MetricMutantsSurvived])
	}
	// Decision 17 still governs the SCORE, and the score still says 100%.
	// That is why the GATE may not read it.
	if got := m[policy.MetricMutationScoreBP]; got != 10000 {
		t.Errorf("mutation_score_bp = %d, want 10000 (timeouts stay out of the denominator)", got)
	}
	gate := policy.Gate{Predicate: policy.GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction}
	ok, reason := gate.Eval(&rec)
	if ok {
		t.Fatalf("a mutant nothing killed bought a pass (metrics %v)", m)
	}
	if reason != "mutants_survived=0 mutants_timeout=1 (want survived+timeout <= 0)" {
		t.Errorf("reason = %q, want both counts named", reason)
	}
	var report MutationReport
	loadArtifact(t, o.store, rec.Result.Artifacts[0], &report)
	if len(report.Timeouts) != 1 {
		t.Fatalf("report.timeouts = %+v, want the hung mutant: `survivors: 0` must never be the whole story",
			report.Timeouts)
	}
	if report.Timeouts[0].Outcome != MutantTimeout {
		t.Errorf("report timeout outcome = %q, want %q", report.Timeouts[0].Outcome, MutantTimeout)
	}
}

// A TOOL THAT ENUMERATES NOTHING OVER A NON-EMPTY TARGET SET IS MACHINERY,
// NOT A VACUOUS PASS.
//
// `python -m cosmic_ray.cli` runs with cwd = the candidate's worktree, so a
// `cosmic_ray/` package in the tree root shadows the installed distribution
// — an ordinary directory named after a tool, matching no harness glob any
// policy would think to write. A red team's six-line shadow whose `init`
// exits 0 and whose `dump` prints nothing produced mutants_candidates=0
// beside the control plane's own mutation_lines_targeted=4, and the gate
// passed.
//
// The control plane derived that line count itself, from bytes it captured
// and hashed, so a tool reporting zero over it is contradicting a number it
// did not produce. That is the same refusal runMutant already applies to
// "exit 0 but the stream reports failures".
func TestMutationEmptyEnumerationOverANonEmptyTargetIsMachinery(t *testing.T) {
	py := fakeMutation{
		probe: map[string]string{"pytest": "8.4.0", "cosmic-ray": "8.3.7"},
		dump:  "", // the shadow: `dump` prints nothing at all
	}.write(t)
	o := mutationInstance(t, py, []byte(patchTwoFiles), object.MutationSpec{})

	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusError {
		t.Fatalf("status = %s, want %s (%s)", rec.Result.Status, StatusError, storedNotes(t, o.store, rec))
	}
	for _, name := range []string{
		policy.MetricMutantsCandidates, policy.MetricMutantsSurvived, policy.MetricMutantsTimeout,
	} {
		if v, ok := rec.Result.Metrics[name]; ok {
			t.Errorf("%s = %d is present; a contradiction yields ABSENCE, never a zero", name, v)
		}
	}
	if rec.Result.Metrics[policy.MetricMutationLinesTargeted] != 4 {
		t.Errorf("the control plane's own line count did not survive: %v", rec.Result.Metrics)
	}
	gate := policy.Gate{Predicate: policy.GateMutationSurvivorsNotAbove, Basis: object.BasisConstruction}
	if ok, _ := gate.Eval(&rec); ok {
		t.Error("a tool that enumerated nothing bought a pass")
	}
	if notes := storedNotes(t, o.store, rec); !strings.Contains(notes, "shadowed tool module") {
		t.Errorf("stderr artifact does not name the contradiction:\n%s", notes)
	}
	// The genuinely empty target set keeps its vacuous pass: a patch that
	// changed no mutable source line has nothing for mutation to say.
	const testOnly = `diff --git a/tests/test_stats.py b/tests/test_stats.py
--- a/tests/test_stats.py
+++ b/tests/test_stats.py
@@ -1,2 +1,2 @@
 def test_x():
-    assert clamp(5, 0, 10) == 5
+    assert True
`
	empty := mutationInstance(t, py, []byte(testOnly), object.MutationSpec{})
	recEmpty, err := empty.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run (empty target): %v", err)
	}
	if recEmpty.Result.Status != StatusPass || recEmpty.Result.Metrics[policy.MetricMutantsCandidates] != 0 {
		t.Errorf("the empty-target path moved: status=%s metrics=%v",
			recEmpty.Result.Status, recEmpty.Result.Metrics)
	}
}
