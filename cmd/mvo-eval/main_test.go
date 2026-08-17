package main

// THE SECOND BINARY'S OWN TESTS: degradation, the import-graph property, and
// the fetch disclosure.
//
// The most important one is the shortest: `go list -deps ./cmd/mvo` must not
// mention internal/eval. That is decision 2's structural half, and asserting it
// in Go as well as in scripts/accept.sh means a developer who runs `go test`
// finds out before CI does.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/eval"
	"github.com/coagente/multiverso/internal/schedule"
)

// buildEvalBinary builds cmd/mvo-eval once per test binary.
func buildEvalBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "mvo-eval")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = "."
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, b)
	}
	return out
}

// runEval runs the binary with a closed environment and returns
// (combined output, exit code).
func runEval(t *testing.T, bin, home string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, EnvHomeVar + "=" + home}
	b, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("running %v: %v: %s", args, err, b)
		}
	}
	return string(b), code
}

func asExit(err error, out **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*out = ee
		return true
	}
	return false
}

// EnvHomeVar is spelled out here rather than imported so this test does not
// depend on the package it is testing for the name of the variable it sets.
const EnvHomeVar = "MVO_EVAL_HOME"

func TestTheRacingBinaryCannotReadALabel(t *testing.T) {
	// Decision 2, asserted mechanically. `mvo` must not, at any optimization
	// level, contain a symbol that opens the eval home — and the way to
	// guarantee that is for the import to be absent from its dependency graph.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	cmd := exec.Command("go", "list", "-deps", "../mvo")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps ../mvo: %v: %s", err, b)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "github.com/coagente/multiverso/internal/eval" {
			t.Fatalf("cmd/mvo depends on internal/eval: the binary that races can open the eval home.\n" +
				"There is no `mvo eval` subcommand and there must not be one; the scorer is cmd/mvo-eval.")
		}
	}
	// And the converse: the scorer DOES depend on it, or it could not score.
	cmd = exec.Command("go", "list", "-deps", ".")
	b, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps .: %v: %s", err, b)
	}
	if !strings.Contains(string(b), "github.com/coagente/multiverso/internal/eval") {
		t.Errorf("cmd/mvo-eval does not depend on internal/eval: it cannot be the scorer")
	}
}

func TestCorpusAbsentSkipsWithANamedReasonAndPrintsNoMetric(t *testing.T) {
	// The harness skips cleanly with a NAMED REASON when the instance corpus
	// is absent, and the assertion is on the ABSENCE OF A NUMBER — the only
	// way to test the rule.
	bin := buildEvalBinary(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	out, code := runEval(t, bin, home, "run", "--repo-root", "../..")
	if code != 0 {
		t.Fatalf("an absent corpus exited %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, string(SkipCorpusAbsentName)) {
		t.Errorf("the skip is not named %q:\n%s", SkipCorpusAbsentName, out)
	}
	if !strings.Contains(out, "SKIP") {
		t.Errorf("no census line:\n%s", out)
	}
	for _, forbidden := range []string{"TCAR", "FAR"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("an absent corpus printed %q:\n%s", forbidden, out)
		}
	}
	if !strings.Contains(out, "no metric line is printed") {
		t.Errorf("the harness did not say why there is no number:\n%s", out)
	}
	// Under --strict the same run is non-zero, so CI cannot mistake "nothing
	// was measured" for "everything passed".
	out, code = runEval(t, bin, home, "run", "--repo-root", "../..", "--strict")
	if code != 3 {
		t.Errorf("--strict over an absent corpus exited %d, want 3:\n%s", code, out)
	}
}

// SkipCorpusAbsentName is the skip reason's wire spelling, written out for the
// same reason EnvHomeVar is.
const SkipCorpusAbsentName = "corpus-absent"

func TestOtherVerbsSkipWithAReasonWhenTheCorpusIsAbsent(t *testing.T) {
	bin := buildEvalBinary(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"derive", "toyrepo-mean-A"},
		{"score", "--instance", "toyrepo-mean-A", "--workspace", home},
		{"leakcheck", "--instance", "toyrepo-mean-A", "--workspace", home},
		{"freeze"},
	} {
		out, code := runEval(t, bin, home, args...)
		if code != exitSkip {
			t.Errorf("%v exited %d, want %d (a named skip):\n%s", args, code, exitSkip, out)
		}
		if !strings.Contains(out, "SKIP") || !strings.Contains(out, SkipCorpusAbsentName) {
			t.Errorf("%v did not name the reason:\n%s", args, out)
		}
	}
}

func TestFetchPrintsEveryURLAndContactsNoneUnderDryRun(t *testing.T) {
	bin := buildEvalBinary(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// The network corpus template: its URL is printed, and --dry-run contacts
	// nothing. The proof that nothing was contacted is that the command
	// succeeds with no network in the environment and writes no instance.
	out, code := runEval(t, bin, home, "fetch", "swebench-live-lite",
		"--repo-root", "../..", "--dry-run")
	if code != 0 {
		t.Fatalf("fetch --dry-run exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "https://huggingface.co/datasets/SWE-bench-Live/SWE-bench-Live") {
		t.Errorf("fetch did not print the URL it would contact:\n%s", out)
	}
	if !strings.Contains(out, "contacted nothing") {
		t.Errorf("fetch --dry-run did not say it contacted nothing:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "swebench-live-lite")); err == nil {
		t.Errorf("fetch --dry-run created a corpus directory")
	}
	// Without --yes it refuses, non-interactively.
	out, code = runEval(t, bin, home, "fetch", "swebench-live-lite", "--repo-root", "../..")
	if code != exitUsage {
		t.Errorf("a network fetch without --yes exited %d, want %d:\n%s", code, exitUsage, out)
	}
	if !strings.Contains(out, "--yes") {
		t.Errorf("the refusal does not name --yes:\n%s", out)
	}
	// With --yes it STILL refuses, because the manifest carries no digest: a
	// manifest with plausible-looking digests nobody has verified is worse
	// than no manifest.
	out, code = runEval(t, bin, home, "fetch", "swebench-live-lite", "--repo-root", "../..", "--yes")
	if code != exitFailure {
		t.Errorf("an unpinned network fetch exited %d, want %d:\n%s", code, exitFailure, out)
	}
	if !strings.Contains(out, "refusing to write unverified bytes") {
		t.Errorf("the refusal does not explain itself:\n%s", out)
	}
}

func TestFetchLocalDerivedContactsNothingAndMaterializes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	bin := buildEvalBinary(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	out, code := runEval(t, bin, home, "fetch", "local-derived", "--repo-root", "../..")
	if code != 0 {
		t.Fatalf("fetch local-derived exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "URLs it will contact: NONE") {
		t.Errorf("the local corpus did not declare that it contacts nothing:\n%s", out)
	}
	if !strings.Contains(out, "materialized 5 instance(s)") {
		t.Errorf("the corpus did not materialize:\n%s", out)
	}
	// The declines are DATA, printed rather than buried.
	if !strings.Contains(out, "declined") {
		t.Errorf("no operator declines were reported: the population's provenance is missing:\n%s", out)
	}
	// The hidden halves are 0600 and the instances carry only a digest.
	corpus := filepath.Join(home, "local-derived", eval.LocalVersion)
	oracles, err := os.ReadDir(filepath.Join(corpus, "oracle"))
	if err != nil {
		t.Fatal(err)
	}
	if len(oracles) != 5 {
		t.Errorf("%d hidden oracles written, want 5", len(oracles))
	}
	for _, e := range oracles {
		st, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("hidden oracle %s is mode %04o, want 0600", e.Name(), st.Mode().Perm())
		}
	}
	// No canary and no node id may appear in any PUBLIC instance file.
	pubs, err := os.ReadDir(filepath.Join(corpus, "instance"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range pubs {
		b, err := os.ReadFile(filepath.Join(corpus, "instance", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		body := string(b)
		if strings.Contains(body, "canary_token") || strings.Contains(body, "fail_to_pass") ||
			strings.Contains(body, "gold_patch") {
			t.Errorf("public instance %s carries a hidden field:\n%s", e.Name(), body)
		}
	}
	// And `freeze` now pins the materialized digests, which the committed
	// freeze file cannot (the oracles are generated under a fresh canary).
	out, code = runEval(t, bin, home, "freeze", "--write")
	if code != 0 {
		t.Fatalf("freeze --write exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "pinning 5 oracle digest(s)") {
		t.Errorf("freeze did not pin the materialized oracles:\n%s", out)
	}
}

// A DERIVED BUDGET OF ZERO IS NO BUDGET, NOT THE TIGHTEST ONE.
//
// B1 = ceil(minspend x 1.1), and minspend is 0 on every instance whose
// reference run reaches d* without buying a rung — a REJECT or ESCALATE the
// first rung already forces. `mvo intent new` defines --budget-oracle-ms 0 as
// UNBOUNDED, so the level inverts and the row labelled "tightest budget" is the
// one that was handed infinite money. Three of the six cells in the first full
// run contained such a row. This pins the arithmetic that produces it, so the
// naming above the metrics cannot be dropped without a test going red.
func TestBudgetLevelCollapsesToZeroWhenMinspendIsZero(t *testing.T) {
	b := schedule.BoundReport{Available: true, MinSpendMS: 0, TotalMS: 4000}
	if got := budgetLevel("B1", b); got != 0 {
		t.Fatalf("B1 with minspend 0 = %d, want 0 — the collapse this test documents", got)
	}
	// And it is only B1 that collapses: the middle of the band and the null
	// case are both bounded away from zero by S.
	if got := budgetLevel("B2", b); got != 2000 {
		t.Errorf("B2 = %d, want 2000", got)
	}
	if got := budgetLevel("B3", b); got != 4000 {
		t.Errorf("B3 = %d, want 4000", got)
	}
	// An unavailable bound yields 0 for every level, which is the same
	// unbounded hazard reached by a different road.
	for _, lvl := range []string{"B1", "B2", "B3"} {
		if got := budgetLevel(lvl, schedule.BoundReport{}); got != 0 {
			t.Errorf("%s with no bound = %d, want 0", lvl, got)
		}
	}
}

// A CELL THAT NAMES A POLICY MUST RACE IT OR REFUSE.
//
// scripts/eval.sh looped over two policy configurations, printed a header
// reading `on_evidence_incomplete: ON` for the second, and passed nothing to
// the runner — which had no policy flag at all. Both cells raced the shipped
// default and their two manifests carried a byte-identical policy_digest, so
// M2b.1's F14 ("both directions, or the table is about one escalation rule
// rather than about the scheduler") was captioned and not delivered. The flag
// exists now; this asserts that an unreadable one is an ERROR rather than a
// silent fallback, because a silent fallback is indistinguishable from the bug.
func TestRunRefusesAPolicyItCannotRead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	bin := buildEvalBinary(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, code := runEval(t, bin, home, "fetch", "local-derived", "--repo-root", "../.."); code != 0 {
		t.Fatalf("fetch local-derived exited %d:\n%s", code, out)
	}
	out, code := runEval(t, bin, home, "run", "--repo-root", "../..",
		"--policy", filepath.Join(home, "no-such-policy.json"))
	if code == 0 {
		t.Fatalf("a --policy that cannot be read exited 0:\n%s", out)
	}
	if !strings.Contains(out, "refusing to race the shipped default under another policy's name") {
		t.Errorf("the refusal does not say what it refused:\n%s", out)
	}
}

func TestArmsPrintsTheMappingAndNamesTheAbsentArms(t *testing.T) {
	bin := buildEvalBinary(t)
	home := t.TempDir()
	out, code := runEval(t, bin, home, "arms")
	if code != 0 {
		t.Fatalf("arms exited %d:\n%s", code, out)
	}
	for _, want := range []string{
		"SELECTOR-ARMS-OVER-A-FIXED-CANDIDATE-SET",
		"A9-label-retrospective",
		"footprint: {} (declared empty",
		"arms 1 (serial self-repair) and 4 (LLM judge) are ABSENT",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the arm mapping is missing %q:\n%s", want, out)
		}
	}
}

func TestUsageAndUnknownCommand(t *testing.T) {
	bin := buildEvalBinary(t)
	home := t.TempDir()
	out, code := runEval(t, bin, home)
	if code != exitUsage {
		t.Errorf("no command exited %d, want %d", code, exitUsage)
	}
	if !strings.Contains(out, "the SECOND binary") {
		t.Errorf("usage does not say what this binary is:\n%s", out)
	}
	out, code = runEval(t, bin, home, "eval")
	if code != exitUsage {
		t.Errorf("an unknown command exited %d, want %d:\n%s", code, exitUsage, out)
	}
}
