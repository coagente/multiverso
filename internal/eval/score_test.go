package eval

// THE SCORER, END TO END, WITHOUT A RACE: a real git repository, real
// reconstructions, the real generated hidden runner under a real python3.
//
// What this file asserts is the claim the whole block rests on and the one that
// is easiest to assert by accident: A HIDDEN TEST NEVER APPEARS IN A WORLD TREE
// OR IN AN ORACLE ARGV. It is checked against the reconstructed directory and
// against the argv the scorer would execute — measured, not assumed.
//
// It needs git and python3 and SKIPS WITH A REASON when either is absent, which
// is the same degradation rule the harness itself follows.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

// scoreFixture builds a git repo at the buggy base, plus the trees two
// candidates produce, and returns everything the scorer needs.
type scoreFixture struct {
	repo     string
	baseTree string
	inst     Instance
	hidden   HiddenOracle
	view     LedgerView
	trees    map[string]string // candidate id -> tree
}

func newScoreFixture(t *testing.T) scoreFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH: " + string(SkipToolAbsent))
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not on PATH: " + string(SkipToolAbsent))
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "stats.py"), []byte(toyBase), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gitSeed(repo); err != nil {
		t.Fatal(err)
	}
	baseTree, err := gitTree(repo)
	if err != nil {
		t.Fatal(err)
	}

	// Two candidates: gold, and an off-by-one derived from it. The oracle
	// decides which is which; this fixture only says what was raced.
	gold := []byte(toyGold)
	obo := Derive(OpOffByOne, DeriveInput{Gold: gold, Base: map[string]string{"stats.py": toyBase}, Seed: 1})
	if !obo.Applied {
		t.Fatalf("off-by-one declined on the fixture: %s", obo.Reason)
	}
	trees := map[string]string{}
	for id, patch := range map[string][]byte{"gold": gold, "obo": obo.Patch} {
		tree, err := applyAndTree(repo, patch)
		if err != nil {
			t.Fatalf("candidate %s does not apply: %v", id, err)
		}
		trees[id] = tree
	}
	if trees["gold"] == trees["obo"] {
		t.Fatalf("the two candidates produce the same tree")
	}

	canary := "CANARY" + strings.Repeat("f", 26)
	def := localDefs()[0] // toyrepo-mean-A's check table
	h := HiddenOracle{
		Schema: SchemaOracle, Instance: "i1",
		// No {report} and no {nonce}: both travel on the runner's stdin under
		// v1, because on an argv they were readable by the process that ran
		// candidate code.
		SuiteArgv: []string{"{python}", "-S", "-B", "{hidden}/" + hiddenRunnerFile,
			"--repo", "{repo}"},
		TimeoutMS: 30_000, CanaryToken: canary, CanaryID: "canary-test",
		GoldPatch:     "# mvo-hidden-canary " + canary + "\n" + string(gold),
		GoldCandidate: "gold", Tier: Tier1,
		Files: map[string]string{
			hiddenRunnerFile: hiddenRunner(canary),
			hiddenChecksFile: hiddenChecks(canary, def),
		},
		StrippedHunks: []string{},
	}
	for _, c := range def.F2P {
		h.FailToPass = append(h.FailToPass, c.Name+"@"+canary)
	}
	for _, c := range def.P2P {
		h.PassToPass = append(h.PassToPass, c.Name+"@"+canary)
	}

	inst := Instance{
		Schema: SchemaInstance, ID: "i1", Corpus: CorpusLocalDerived, Version: LocalVersion,
		Family: FamilyGoldPresent, Repo: "repos/i1", BaseCommit: "x", T0OK: true,
		Task: "fix mean()",
		Candidates: []Candidate{
			{Ord: 0, ID: "gold", Source: SourceGold, Patch: CASKeyBytes(gold),
				PatchBytes: int64(len(gold)), ResultTree: trees["gold"], Expected: ExpectCorrect},
			{Ord: 1, ID: "obo", Source: SourceDerived, Patch: CASKeyBytes(obo.Patch),
				PatchBytes: int64(len(obo.Patch)), ResultTree: trees["obo"],
				Generator: OpOffByOne, Seed: 1, Params: obo.Params, Expected: ExpectIncorrect},
		},
		OracleDigest: "sha256:" + strings.Repeat("c", 64), CanaryID: "canary-test",
	}
	view := LedgerView{
		Worlds: []object.RecordedWorld{
			{Digest: "mv0:w-gold", World: object.World{Tree: trees["gold"], Env: "mv0:env", Outcome: object.OutcomeCompleted}},
			{Digest: "mv0:w-obo", World: object.World{Tree: trees["obo"], Env: "mv0:env", Outcome: object.OutcomeCompleted}},
		},
	}
	return scoreFixture{repo: repo, baseTree: baseTree, inst: inst, hidden: h, view: view, trees: trees}
}

func TestScorerLabelsGoldCorrectAndAMutantIncorrect(t *testing.T) {
	f := newScoreFixture(t)
	root := t.TempDir()
	s, err := NewScorer(f.inst, f.hidden, []byte("hidden"), f.repo, root, "python3")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.ScoreBatch(f.view, f.baseTree)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Unjoined) != 0 {
		t.Errorf("worlds no candidate's tree matched: %v", b.Unjoined)
	}
	if !b.Controls.OK() {
		t.Fatalf("the controls did not hold: %s", b.Controls.Detail)
	}
	got := map[string]string{}
	for _, l := range b.Labels {
		got[l.Candidate] = l.Verdict
	}
	if got["gold"] != VerdictCorrect {
		t.Errorf("gold's label = %q, want correct", got["gold"])
	}
	// The mutant's verdict is the ORACLE's answer, not the generator's. It is
	// asserted here because on this specific fixture the oracle's F2P set
	// covers the perturbed expression; a test that asserted every mutant is
	// wrong would be decision 7's bug, and this one names its scope.
	if got["obo"] != VerdictIncorrect {
		t.Errorf("the off-by-one mutant's label = %q, want incorrect on THIS fixture "+
			"(its F2P set covers the perturbed expression)", got["obo"])
	}
	// The tree join is what the metric path consumes.
	if b.TreeVerdict[f.trees["gold"]] != VerdictCorrect {
		t.Errorf("the tree verdict map lost gold: %v", b.TreeVerdict)
	}
	if b.WorldVerdict["mv0:w-obo"] != VerdictIncorrect {
		t.Errorf("the world verdict map lost the mutant: %v", b.WorldVerdict)
	}
}

func TestNoHiddenTestEnterAnyReconstructedTreeOrOracleArgv(t *testing.T) {
	// THE CLAIM, ASSERTED. It is arithmetic over directory identity: the
	// hidden suite is a SIBLING of every reconstruction, and no hidden file
	// name is a member of one.
	f := newScoreFixture(t)
	root := t.TempDir()
	s, err := NewScorer(f.inst, f.hidden, []byte("hidden"), f.repo, root, "python3")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for id, tree := range f.trees {
		dir, err := s.Reconstruct(tree)
		if err != nil {
			t.Fatalf("reconstructing %s: %v", id, err)
		}
		if err := s.AssertHiddenOutsideTree(dir); err != nil {
			t.Errorf("candidate %s: %v", id, err)
		}
		// And the stronger claim: not reachable by any relative path a
		// candidate would try. The first implementation of this package put
		// the hidden suite at `<root>/hidden` beside `<root>/recon/<tree>`,
		// which THIS assertion catches — `../../hidden/mvo_hidden_run.py`
		// resolved, and the scorer imports candidate module code by
		// construction, so it was a read rather than a theory.
		if err := s.AssertHiddenUnreachableByRelativePath(dir); err != nil {
			t.Errorf("candidate %s: %v", id, err)
		}
		// The reconstruction is a directory no race ever saw: not under the
		// workspace, not under the eval home.
		if strings.HasPrefix(dir, f.repo+string(os.PathSeparator)) {
			t.Errorf("the reconstruction %s is inside the workspace %s", dir, f.repo)
		}
		// And the canary is nowhere in it.
		docs, _, err := WalkFiles(dir, "recon")
		if err != nil {
			t.Fatal(err)
		}
		n := NeedlesFor(f.inst, f.hidden, []byte("hidden"), root)
		if rep := D5Canary(n, docs); rep.Void() {
			t.Errorf("the canary is inside the reconstruction of %s: %v", id, rep.Lines())
		}
		// The ORACLE ARGV the scorer would execute names the hidden runner —
		// that is the scorer's own oracle, and it is correct. What must never
		// happen is a hidden path in a RECEIPT argv, which is D1's job and is
		// asserted there. Here we assert the complement: the argv names no
		// path inside the reconstruction.
		argv := s.Argv(dir, filepath.Join(s.ScratchDir(), "r.xml"), "NONCE")
		for _, a := range argv {
			if strings.HasPrefix(a, dir+string(os.PathSeparator)) {
				t.Errorf("the oracle argv names a path inside the tree it judges: %q", a)
			}
		}
		if !strings.Contains(strings.Join(argv, " "), s.HiddenDir()) {
			t.Errorf("the oracle argv does not name the hidden mount at all: %v", argv)
		}
		// The report goes to control-plane scratch OUTSIDE the tree.
		if strings.HasPrefix(s.ScratchDir(), dir) {
			t.Errorf("the scratch dir is inside the reconstruction")
		}
		os.RemoveAll(dir)
	}
	// The hidden mount is a sibling, and its files are mode 0600.
	for _, rel := range f.hidden.HiddenPaths() {
		st, err := os.Stat(filepath.Join(s.HiddenDir(), rel))
		if err != nil {
			t.Fatalf("hidden file %s: %v", rel, err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("hidden file %s is mode %04o, want 0600", rel, st.Mode().Perm())
		}
	}
}

func TestScorerRefusesToLiveInsideTheWorkspace(t *testing.T) {
	f := newScoreFixture(t)
	inside := filepath.Join(f.repo, "scorer")
	_, err := NewScorer(f.inst, f.hidden, []byte("hidden"), f.repo, inside, "python3")
	if err == nil {
		t.Fatalf("NewScorer accepted a root inside the workspace")
	}
	if !strings.Contains(err.Error(), "where no race could see it") {
		t.Errorf("the refusal does not explain itself: %v", err)
	}
}

func TestTwoScoringsOfTheSameTreeProduceByteIdenticalLabels(t *testing.T) {
	// Acceptance step m2d-7d, at the unit level: two scorings of the same
	// (world tree, oracle digest, env digest) write byte-identical label files.
	f := newScoreFixture(t)
	var first []byte
	for i := 0; i < 2; i++ {
		root := t.TempDir()
		s, err := NewScorer(f.inst, f.hidden, []byte("hidden"), f.repo, root, "python3")
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		b, err := s.ScoreBatch(f.view, f.baseTree)
		if err != nil {
			t.Fatal(err)
		}
		bytes, err := object.Canonical(b.Labels)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = bytes
			continue
		}
		if string(first) != string(bytes) {
			t.Errorf("two scorings produced different labels:\n%s\n%s", first, bytes)
		}
	}
}

func TestAControlThatMovesVoidsTheWholeBatchNeverProducingCorrect(t *testing.T) {
	// A scoring whose NEGATIVE control passes F2P yields `unknown` for the
	// whole batch, never `correct`. The way to make that happen for real is to
	// declare a fail_to_pass set the BASE TREE already satisfies.
	f := newScoreFixture(t)
	h := f.hidden
	// `total` passes on the base tree, so declaring it fail_to_pass makes the
	// instance's task not the task.
	h.FailToPass = []string{"total@" + h.CanaryToken}
	h.PassToPass = []string{"clamp_inside@" + h.CanaryToken}
	def := localDefs()[0]
	var f2p, p2p []localCheck
	for _, c := range append(append([]localCheck{}, def.F2P...), def.P2P...) {
		if c.Name == "total" {
			f2p = append(f2p, c)
		}
		if c.Name == "clamp_inside" {
			p2p = append(p2p, c)
		}
	}
	bad := def
	bad.F2P, bad.P2P = f2p, p2p
	h.Files = map[string]string{
		hiddenRunnerFile: hiddenRunner(h.CanaryToken),
		hiddenChecksFile: hiddenChecks(h.CanaryToken, bad),
	}
	root := t.TempDir()
	s, err := NewScorer(f.inst, h, []byte("hidden"), f.repo, root, "python3")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.ScoreBatch(f.view, f.baseTree)
	if err != nil {
		t.Fatal(err)
	}
	if b.Controls.OK() {
		t.Fatalf("the controls held over a base tree that already passes F2P: %+v", b.Controls)
	}
	if b.Skipped != SkipGoldFailsControl {
		t.Errorf("the batch was not dropped as %s: %q", SkipGoldFailsControl, b.Skipped)
	}
	for _, l := range b.Labels {
		if l.Verdict != VerdictUnknown {
			t.Errorf("candidate %s survived control drift with verdict %q", l.Candidate, l.Verdict)
		}
		if l.Reason != ReasonControlDrift {
			t.Errorf("candidate %s: reason = %q, want %q", l.Candidate, l.Reason, ReasonControlDrift)
		}
	}
	// The PRE-control labels are kept, so "the controls moved and here is what
	// we would have said" stays reportable.
	if len(b.Pre) != len(b.Labels) {
		t.Errorf("the pre-control labels were not kept: %d vs %d", len(b.Pre), len(b.Labels))
	}
}

func TestHiddenEnvIsClosedAndHardened(t *testing.T) {
	env := HiddenEnv()
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"PYTHONNOUSERSITE=1",
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTEST_DISABLE_PLUGIN_AUTOLOAD=1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the labeller's environment is missing %s", want)
		}
	}
	// It is built from NOTHING, not filtered from the ambient environment, so
	// a variable a developer exported cannot change a label.
	t.Setenv("PYTHONPATH", "/attacker/controlled")
	if strings.Contains(strings.Join(HiddenEnv(), "\n"), "/attacker/controlled") {
		t.Errorf("the labeller's environment inherited PYTHONPATH from the process")
	}
	if strings.Contains(joined, EnvPrefix) {
		t.Errorf("an %s* variable is in the labeller's environment", EnvPrefix)
	}
}

func TestNonceAndCanaryAreDistinctAndSizedRight(t *testing.T) {
	n1, err := Nonce()
	if err != nil {
		t.Fatal(err)
	}
	n2, _ := Nonce()
	if n1 == n2 {
		t.Errorf("two nonces collided: %s", n1)
	}
	if len(n1) != 32 {
		t.Errorf("nonce is %d hex chars, want 32 (16 bytes)", len(n1))
	}
	c1, err := CanaryToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(c1) != 64 {
		t.Errorf("canary is %d hex chars, want 64 (32 bytes)", len(c1))
	}
	c2, _ := CanaryToken()
	if c1 == c2 {
		t.Errorf("two canaries collided")
	}
}

// THE FORGERY THAT WORKED, AS A REGRESSION TEST.
//
// Under corpus v0 the hidden runner imported the candidate's modules into the
// SAME interpreter that already held the mutable check table, and it carried
// `--report <path> --nonce <value>` on that interpreter's argv. A candidate
// whose only extra code was
//
//	sys.modules['mvo_hidden_checks'].CHECKS = [
//	    (cls, name, (lambda: True)) for cls, name, _ in ...CHECKS]
//
// exited 0, echoed the nonce, listed every declared node as passing, and Judge
// returned `correct` for a module that was wrong. Decision 4's three "agreeing"
// signals were all produced by the one process the candidate controlled.
//
// This test races that candidate through the real scorer and asserts the label
// is NOT `correct`. It is deliberately written against the ATTACK rather than
// against the fix, so a future refactor that reintroduces co-residency fails
// here rather than in a report nobody re-reads.
func TestACandidateThatRewritesTheCheckTableIsNotLabelledCorrect(t *testing.T) {
	f := newScoreFixture(t)
	// A stats.py that is WRONG (mean divides by len-1 still, and clamp is
	// broken) plus every v0 forgery attempt in one module.
	forge := `"""A wrong module that attempts every v0 forgery."""
import os
import sys

# 1. Rewrite the check table, if it is in this interpreter at all.
_c = sys.modules.get("mvo_hidden_checks")
if _c is not None:
    try:
        _c.CHECKS = [(row[0], row[1], row[2], row[3], (lambda R: True)) for row in _c.CHECKS]
    except Exception:
        pass

# 2. Write the report ourselves from --report/--nonce on argv, then exit clean.
_report = ""
_nonce = ""
for _i, _a in enumerate(sys.argv):
    if _a == "--report" and _i + 1 < len(sys.argv):
        _report = sys.argv[_i + 1]
    if _a == "--nonce" and _i + 1 < len(sys.argv):
        _nonce = sys.argv[_i + 1]
if _report and _nonce:
    with open(_report, "w") as _fh:
        _fh.write('<?xml version="1.0" encoding="UTF-8"?>\n')
        _fh.write('<testsuite name="mvo-hidden" nonce="%s" tests="0" failures="0"/>\n' % _nonce)
    os._exit(0)


def mean(values):
    if not values:
        raise ValueError("mean() of empty sequence")
    return sum(values) / (len(values) - 1)


def clamp(value, low, high):
    return value


def total(values):
    return 0
`
	patch := diffFor("stats.py", toyBase, forge)
	tree, err := applyAndTree(f.repo, []byte(patch))
	if err != nil {
		t.Fatalf("the forging candidate does not apply: %v", err)
	}
	inst := f.inst
	inst.Candidates = append(inst.Candidates, Candidate{
		Ord: 2, ID: "forge", Source: SourceAdversarial, Patch: CASKeyBytes([]byte(patch)),
		PatchBytes: int64(len(patch)), ResultTree: tree, Expected: ExpectIncorrect,
	})
	view := f.view
	view.Worlds = append(view.Worlds, object.RecordedWorld{
		Digest: "mv0:w-forge",
		World:  object.World{Tree: tree, Env: "mv0:env", Outcome: object.OutcomeCompleted},
	})

	root := t.TempDir()
	s, err := NewScorer(inst, f.hidden, []byte("hidden"), f.repo, root, "python3")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.ScoreBatch(view, f.baseTree)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Controls.OK() {
		t.Fatalf("the controls did not hold: %s", b.Controls.Detail)
	}
	var got Label
	for _, l := range b.Labels {
		if l.Candidate == "forge" {
			got = l
		}
	}
	if got.Candidate == "" {
		t.Fatalf("the forging candidate produced no label at all: %+v", b.Labels)
	}
	if got.Verdict == VerdictCorrect {
		t.Fatalf("A CANDIDATE FORGED ITS OWN LABEL: %+v — the process that runs candidate "+
			"code is producing the verdict again", got)
	}
	if got.Verdict != VerdictIncorrect {
		t.Errorf("the forging candidate is %q (%s), want incorrect: it is wrong and the "+
			"oracle should be able to say so, not merely decline", got.Verdict, got.Reason)
	}
	// And gold, scored in the same batch, is still correct: the fix must not
	// work by refusing everything.
	for _, l := range b.Labels {
		if l.Candidate == "gold" && l.Verdict != VerdictCorrect {
			t.Errorf("gold is %q in the same batch: the hardening broke the oracle", l.Verdict)
		}
	}
}

// TestTheProcessThatRunsCandidateCodeHoldsNoSecret asserts the four disclosures
// the v0 runner made, from inside a candidate: the report path and the nonce on
// argv, the hidden directory on sys.path, the check table in sys.modules, and
// the scorer's report-bearing scratch as the cwd.
func TestTheProcessThatRunsCandidateCodeHoldsNoSecret(t *testing.T) {
	f := newScoreFixture(t)
	root := t.TempDir()
	sink := filepath.Join(root, "seen.json")
	probe := `"""Records what the process running candidate code can reach."""
import json
import os
import sys

json.dump({
    "argv": sys.argv,
    "path": sys.path,
    "cwd": os.getcwd(),
    "listing": sorted(os.listdir(".")),
    "modules": sorted(m for m in sys.modules if "mvo" in m),
}, open(` + pyQuote(sink) + `, "w"))


def mean(values):
    if not values:
        raise ValueError("mean() of empty sequence")
    return sum(values) / len(values)


def clamp(value, low, high):
    if low > high:
        raise ValueError("clamp() with low > high")
    return min(max(value, low), high)


def total(values):
    return sum(values)
`
	patch := diffFor("stats.py", toyBase, probe)
	tree, err := applyAndTree(f.repo, []byte(patch))
	if err != nil {
		t.Fatalf("the probing candidate does not apply: %v", err)
	}
	s, err := NewScorer(f.inst, f.hidden, []byte("hidden"), f.repo, root, "python3")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir, err := s.Reconstruct(tree)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if _, err := s.RunHidden(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(sink)
	if err != nil {
		t.Fatalf("the candidate's module code did not run at all (%v): this test is "+
			"only meaningful if it did", err)
	}
	var seen struct {
		Argv    []string `json:"argv"`
		Path    []string `json:"path"`
		CWD     string   `json:"cwd"`
		Listing []string `json:"listing"`
		Modules []string `json:"modules"`
	}
	if err := json.Unmarshal(b, &seen); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(append(append([]string{}, seen.Argv...), seen.Path...), " ") +
		" " + seen.CWD + " " + strings.Join(seen.Listing, " ")
	for what, needle := range map[string]string{
		"the hidden suite directory": s.HiddenDir(),
		"the scorer's scratch":       s.ScratchDir(),
		"the canary":                 f.hidden.CanaryToken,
	} {
		if strings.Contains(joined, needle) {
			t.Errorf("%s is reachable from candidate code: argv=%v path=%v cwd=%s listing=%v",
				what, seen.Argv, seen.Path, seen.CWD, seen.Listing)
		}
	}
	if len(seen.Argv) != 1 {
		t.Errorf("the candidate's process has %d argv entries, want 1 (argv[0] alone): %v",
			len(seen.Argv), seen.Argv)
	}
	for _, m := range seen.Modules {
		if strings.Contains(m, "hidden_checks") {
			t.Errorf("the check table is in the candidate's own interpreter: %v", seen.Modules)
		}
	}
	// And the scratch is drained: nothing that carries the node ids survives.
	if err := s.AssertScratchDrained(); err != nil {
		t.Errorf("%v", err)
	}
}

// diffFor builds a whole-file replacement patch for one path.
func diffFor(path, from, to string) string {
	fromLines := strings.Split(strings.TrimRight(from, "\n"), "\n")
	toLines := strings.Split(strings.TrimRight(to, "\n"), "\n")
	var sb strings.Builder
	fmt.Fprintf(&sb, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1,%d +1,%d @@\n",
		path, path, path, path, len(fromLines), len(toLines))
	for _, l := range fromLines {
		sb.WriteString("-" + l + "\n")
	}
	for _, l := range toLines {
		sb.WriteString("+" + l + "\n")
	}
	return sb.String()
}
