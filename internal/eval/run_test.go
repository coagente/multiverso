package eval

// THE RUNNER: decision 2's run-time half, asserted rather than described.
//
// No MVO_EVAL_* variable survives into the exec'd racer's environment, no
// eval-home path appears in its argv, its cwd is outside the eval home, and a
// poisoned stub firing is a loud bug rather than a silent charge. The racer here
// is a STUB that dumps what it was given — which is the only way to assert what
// the real racer receives without racing.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/workspace"
)

// racerStub writes its argv and environment to dumpDir and succeeds. It stands
// in for `mvo`: the assertion is about what the runner HANDS OVER, and a stub
// that records that is a better witness than a real race, which would bury it.
func racerStub(t *testing.T, dir, dumpDir string) string {
	t.Helper()
	p := filepath.Join(dir, "mvo-stub")
	script := "#!/bin/sh\n" +
		"mkdir -p " + shq(dumpDir) + "\n" +
		"printf '%s\\n' \"$@\" >> " + shq(filepath.Join(dumpDir, "argv")) + "\n" +
		"env >> " + shq(filepath.Join(dumpDir, "env")) + "\n" +
		"pwd >> " + shq(filepath.Join(dumpDir, "cwd")) + "\n" +
		"case \"$1\" in intent) echo mv0:deadbeef ;; esac\n" +
		"exit 0\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func TestRaceScrubsEveryEvalVariableAndKeepsTheArgvClean(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "evalhome")
	if err := os.MkdirAll(filepath.Join(home, CorpusLocalDerived), 0o700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "fixture")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "stats.py"), []byte(toyBase), 0o644); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(root, "dump")
	stub := racerStub(t, root, dump)

	h := hiddenFor([]string{"mean_single@CANARY"}, []string{"total@CANARY"})
	inst := Instance{
		Schema: SchemaInstance, ID: "i1", Corpus: CorpusLocalDerived, Version: LocalVersion,
		Family: FamilyGoldPresent, Repo: "repos/i1", BaseCommit: "x", T0OK: true,
		Task:         "fix mean()",
		Candidates:   []Candidate{{Ord: 0, ID: "gold", Source: SourceGold, Patch: "sha256:" + strings.Repeat("a", 64), ResultTree: "git:a"}},
		OracleDigest: "sha256:" + strings.Repeat("c", 64), CanaryID: "canary-x",
	}
	needles := NeedlesFor(inst, h, []byte("hidden"), home)

	// An ambient environment that is FULL of things that must not survive.
	env := []string{
		"PATH=/usr/bin:/bin",
		EnvHome + "=" + home,
		"MVO_EVAL_CORPUS=local-derived",
		"MVO_EVAL_CANARY=" + h.CanaryToken,
		"HOME=" + root,
		"LANG=C",
	}
	res, _, err := Race(RunSpec{
		Arm: Arm{ID: "stub", Kind: KindRaced, RaceFlags: []string{"--schedule=fixed-budget"}},
		MVO: stub, Instance: inst,
		Patches:  map[string][]byte{"cand-00.patch": []byte(toyGold)},
		WorkRoot: filepath.Join(root, "work"), EvalHome: home, RepoSrc: src,
		Needles: needles, Env: env, Parallel: 1, BudgetMS: 1500,
	})
	// ReadLedger fails because the stub wrote no ledger. That is expected: the
	// assertions are about what was handed over, which is fully recorded.
	if err == nil {
		t.Fatalf("the stub produced a readable ledger, which it cannot have")
	}
	if !res.EnvScrubbed {
		t.Errorf("EnvScrubbed is false")
	}
	if !res.EnvClean {
		t.Errorf("EnvClean is false: a needle reached the child's environment")
	}
	if !res.ArgvClean {
		t.Errorf("ArgvClean is false: %v", res.Argv)
	}
	if !res.CWDOutsideEvalHome {
		t.Errorf("the racer's cwd is inside the eval home: %s", res.CWD)
	}
	if len(res.StubsFired) != 0 {
		t.Errorf("a poisoned agent stub fired: %v", res.StubsFired)
	}

	// Now the same assertions against what the CHILD actually saw, read off
	// disk. This is the half that cannot be faked by the runner's own
	// bookkeeping.
	childEnv := readDump(t, filepath.Join(dump, "env"))
	for _, line := range strings.Split(childEnv, "\n") {
		if strings.HasPrefix(line, EnvPrefix) {
			t.Errorf("an %s* variable survived into the racer: %q", EnvPrefix, line)
		}
		if h.CanaryToken != "" && strings.Contains(line, h.CanaryToken) {
			t.Errorf("the canary reached the racer's environment: %q", line)
		}
		if strings.Contains(line, home) && !strings.HasPrefix(line, "PATH=") {
			t.Errorf("an eval-home path reached the racer's environment: %q", line)
		}
	}
	childArgv := readDump(t, filepath.Join(dump, "argv"))
	if strings.Contains(childArgv, home) {
		t.Errorf("an eval-home path reached the racer's argv:\n%s", childArgv)
	}
	for _, n := range append(h.FailToPass, h.PassToPass...) {
		if strings.Contains(childArgv, n) {
			t.Errorf("a hidden node id reached the racer's argv: %q", n)
		}
	}
	if strings.Contains(childArgv, inst.OracleDigest) {
		t.Errorf("the oracle digest reached the racer's argv")
	}
	for f := range h.Files {
		if strings.Contains(childArgv, f) {
			t.Errorf("a hidden suite file name reached the racer's argv: %q", f)
		}
	}
	// The budget is on the INTENT and the arm flag on the RACE.
	if !strings.Contains(childArgv, "--budget-oracle-ms") || !strings.Contains(childArgv, "1500") {
		t.Errorf("the budget did not reach the intent:\n%s", childArgv)
	}
	if !strings.Contains(childArgv, "--schedule=fixed-budget") {
		t.Errorf("the arm flag did not reach the race:\n%s", childArgv)
	}
	// And the handoff dir holds ONLY patch bytes: no source tag, no oracle
	// digest, no canary, no node id.
	handoff := filepath.Join(res.Workspace, ".mvo-eval-handoff")
	docs, _, err := WalkFiles(handoff, "handoff")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) == 0 {
		t.Fatalf("the handoff dir is empty")
	}
	for _, d := range docs {
		body := string(d.Bytes)
		for _, forbidden := range []string{SourceGold, SourceDerived, inst.OracleDigest,
			h.CanaryToken, inst.CanaryID, "mvo_hidden_run.py"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("handoff file %s contains %q", d.Ref, forbidden)
			}
		}
	}
}

// A PolicyHint must actually install the policy it names. This is a regression
// test with a measured cost: the runner shelled out to `mvo policy install`,
// which is NOT one of the product binary's verbs (list, show, validate, use),
// so it exited 2 on every call and EVERY instance carrying a PolicyHint —
// advrepo-split-A and advrepo-split-B, i.e. the whole FAR-adv half of the
// corpus — died at pre-flight and was filed as a named `preflight-abort` skip.
// The harness stayed green, the report stayed honest, and two of five instances
// silently left the experiment. The assertion is therefore on BOTH halves: the
// file lands where the product binary's own workspace layout resolves it, and
// the verb handed over is `use`.
func TestPolicyHintInstallsWithAVerbTheProductBinaryHas(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "evalhome")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "fixture")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "stats.py"), []byte(toyBase), 0o644); err != nil {
		t.Fatal(err)
	}
	polSrc := filepath.Join(root, "no-paths.json")
	if err := os.WriteFile(polSrc, []byte(`{"name":"no-paths"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(root, "dump")
	stub := racerStub(t, root, dump)

	res, _, _ := Race(RunSpec{
		Arm: Arm{ID: "stub", Kind: KindRaced, RaceFlags: []string{"--schedule=fixed-budget"}},
		MVO: stub, Instance: Instance{
			Schema: SchemaInstance, ID: "i1", Corpus: CorpusLocalDerived, Version: LocalVersion,
			Family: FamilyAllWrong, Repo: "repos/i1", BaseCommit: "x", T0OK: true, Task: "fix it",
		},
		Patches:  map[string][]byte{"cand-00.patch": []byte(toyGold)},
		WorkRoot: filepath.Join(root, "work"), EvalHome: home, RepoSrc: src,
		PolicyFile: polSrc, Parallel: 1,
	})

	// Half one: the bytes are where the PRODUCT BINARY looks for them. The
	// directory name comes from internal/workspace rather than being spelled
	// again here, so a layout change breaks this test instead of silently
	// un-installing every hinted policy. (The workspace cannot be Open()ed:
	// the racer here is a stub, so `mvo init` wrote no config.)
	want := filepath.Join(res.Workspace, workspace.DirName, "policies", "no-paths.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("the hinted policy is not at the path the product binary resolves: %v", err)
	}

	// Half two: the verb. `install` is the spelling that cost two instances.
	var sawUse bool
	for _, argv := range res.Argv {
		for i := 0; i+1 < len(argv); i++ {
			if argv[i] != "policy" {
				continue
			}
			switch argv[i+1] {
			case "use":
				sawUse = true
			default:
				t.Errorf("the runner emitted `mvo policy %s`, which the product binary does not accept "+
					"(want list, show, validate, use): %v", argv[i+1], argv)
			}
		}
	}
	if !sawUse {
		t.Errorf("a PolicyFile was given and no `mvo policy use` was emitted: %v", res.Argv)
	}
}

func TestRaceRefusesToRunInsideTheEvalHomeOrWithoutAnArm(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "evalhome")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	base := RunSpec{
		Arm: Arm{ID: "stub", Kind: KindRaced, RaceFlags: []string{"--schedule=adaptive"}},
		MVO: "/bin/true", WorkRoot: filepath.Join(home, "inside"), EvalHome: home,
		RepoSrc: root,
	}
	if _, _, err := Race(base); err == nil || !strings.Contains(err.Error(), "inside the eval home") {
		t.Errorf("Race did not refuse a work root inside the eval home: %v", err)
	}
	noArm := base
	noArm.WorkRoot = filepath.Join(root, "outside")
	noArm.Arm = Arm{ID: "x", Kind: KindRaced}
	if _, _, err := Race(noArm); err == nil || !strings.Contains(err.Error(), "declares no race flags") {
		t.Errorf("Race did not refuse an arm with no treatment: %v", err)
	}
	noBin := noArm
	noBin.Arm = base.Arm
	noBin.MVO = ""
	if _, _, err := Race(noBin); err == nil || !strings.Contains(err.Error(), "no mvo binary") {
		t.Errorf("Race did not refuse an unnamed binary: %v", err)
	}
}

func TestScrubEnvRemovesRatherThanEmpties(t *testing.T) {
	// Removal rather than overwriting is the whole point: an empty
	// MVO_EVAL_HOME would make os.Getenv return "", which a code path can
	// misread as "set to the current directory". Absence is unambiguous.
	in := []string{"PATH=/bin", EnvHome + "=/eval", "MVO_EVAL_X=1", "KEEP=yes"}
	out := ScrubEnv(in, "/shim")
	joined := strings.Join(out, "\n")
	if strings.Contains(joined, EnvPrefix) {
		t.Errorf("an %s* variable survived: %v", EnvPrefix, out)
	}
	for _, kv := range out {
		if strings.HasPrefix(kv, EnvHome+"=") {
			t.Errorf("%s was emptied instead of removed: %q", EnvHome, kv)
		}
	}
	if !strings.Contains(joined, "KEEP=yes") {
		t.Errorf("an unrelated variable was dropped: %v", out)
	}
	// The shim is FRONT-LOADED, so a poisoned stub shadows a real agent CLI.
	var path string
	for _, kv := range out {
		if strings.HasPrefix(kv, "PATH=") {
			path = strings.TrimPrefix(kv, "PATH=")
		}
	}
	if !strings.HasPrefix(path, "/shim") {
		t.Errorf("the shim is not front-loaded: PATH=%s", path)
	}
	// The git identity is pinned, so a race never depends on a developer's
	// gitconfig.
	for _, want := range []string{"GIT_AUTHOR_NAME=", "GIT_COMMITTER_EMAIL="} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s is not pinned: %v", want, out)
		}
	}
}

func TestPoisonedStubsExistForEveryKnownAgentCLI(t *testing.T) {
	root := t.TempDir()
	shim := filepath.Join(root, "shim")
	fired := filepath.Join(root, "fired")
	if err := writeAgentStubs(shim, fired); err != nil {
		t.Fatal(err)
	}
	for _, bin := range AgentBinaries() {
		st, err := os.Stat(filepath.Join(shim, bin))
		if err != nil {
			t.Errorf("no poisoned stub for %s: %v", bin, err)
			continue
		}
		if st.Mode().Perm()&0o111 == 0 {
			t.Errorf("the stub for %s is not executable", bin)
		}
		b, err := os.ReadFile(filepath.Join(shim, bin))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "refusing to invoke a real agent CLI") {
			t.Errorf("the stub for %s does not refuse loudly", bin)
		}
		// It TOUCHES A FILE before refusing, so "no stub fired" is a
		// measurement rather than an absence of evidence.
		if !strings.Contains(string(b), fired) {
			t.Errorf("the stub for %s does not record that it fired", bin)
		}
	}
	if got := stubsFired(fired); len(got) != 0 {
		t.Errorf("stubsFired reported %v before any stub ran", got)
	}
}

func TestArgvCleanCatchesAnEvalHomePath(t *testing.T) {
	n := testNeedles()
	if argvClean(n, [][]string{{"mvo", "race", "--patches", "/eval/home/local-derived/v0/blobs"}}, "/eval/home") {
		t.Errorf("argvClean accepted an eval-home path")
	}
	if argvClean(n, [][]string{{"mvo", "race", "--patches", "/tmp/ws/handoff", "mvo_hidden_run.py"}}, "/eval/home") {
		t.Errorf("argvClean accepted a hidden suite file name")
	}
	if !argvClean(n, [][]string{{"mvo", "race", "--patches", "/tmp/ws/handoff"}}, "/eval/home") {
		t.Errorf("argvClean rejected a clean argv")
	}
}

func readDump(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the racer stub wrote no %s: %v", filepath.Base(path), err)
	}
	return string(b)
}
