package eval

// THE `local-derived` CORPUS: the whole mechanism, zero downloads (§1.1).
//
// MODULE-LAYOUT DELTA, NAMED. The design's module list does not assign corpus
// materialization a file. It is here because it is the one place where a
// FIXTURE becomes an INSTANCE, and that transformation is where a hidden oracle
// is invented — the step a reviewer most needs to read in one piece.
//
// WHY THIS CORPUS EXISTS AND WHAT IT IS WORTH. `local-derived` makes the
// harness testable, acceptance-gated and honest today with no network access at
// all. Its ground truth is not borrowed: testdata/adversarial already measures
// correctness OUT OF BAND, by `python3 -S -c` with an explicit sys.path, so no
// conftest.py, no sitecustomize.py and no pytest plugin can reach the
// interpreter that decides whether the bug is gone. THAT IS ALREADY A HIDDEN
// ORACLE, built in M1f for a different reason, and this file generalizes its
// mechanism rather than inventing one.
//
// AND WHAT IT IS NOT WORTH: n = 2 REPOSITORIES. A diagnosis, never a rate with
// a confidence interval. Every table says so.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/gitx"
)

// LocalVersion is the local-derived corpus version. It moves when the
// GENERATED shape moves (a new operator, a new check, a new hidden runner), so
// a freeze file always names a corpus whose bytes it could have seen.
//
// v0 -> v1 BECAUSE THE HIDDEN RUNNER WAS FORGEABLE AND IS NOW SPLIT IN TWO.
// Under v0 the runner did `sys.path.insert(0, repo)` and `__import__(mod)` in
// the SAME interpreter that already held the mutable check table, and it
// carried `--report <path> --nonce <value>` on that interpreter's argv. A
// ten-line candidate rewrote `sys.modules['mvo_hidden_checks'].CHECKS` to a
// table of `lambda: True` and was labelled `correct`; this was reproduced end
// to end against the real generated suite. v1's oracle never imports candidate
// code (see hiddenRunner), so a v0 eval home must not be scored by a v1 scorer
// and the version is what refuses it.
const LocalVersion = "v1"

// Hidden suite file names. They are PREDICTABLE on purpose: acceptance step
// m2d-7b has to be able to plant one in a candidate patch to prove D2 fires,
// and a name derived from the canary would make that test impossible to write.
// The canary lives in the file CONTENTS and in the node ids instead.
const (
	hiddenRunnerFile = "mvo_hidden_run.py"
	hiddenChecksFile = "mvo_hidden_checks.py"
)

// localCall is ONE observation the probe child makes of the candidate:
// `getattr(<module>, <attr>)(*<args>)`. The child returns the VALUE (or the
// exception's MRO names); it never decides anything.
//
// Args is the Python-and-JSON literal source of the argument LIST — it is
// control-plane authored, it is rendered into the parent's spec table and then
// json.dumps'd onto the child's stdin, so it must be a literal both languages
// read the same way (lists, ints, floats, strings).
type localCall struct {
	Module string
	Attr   string
	Args   string
}

// localCheck is one hidden assertion, and its SHAPE is the fix for the block's
// worst defect.
//
// Under v0 a check was a Python expression evaluated in the process that had
// just imported the candidate's modules, so the candidate could rewrite the
// check table, the exit code and the report — all three "cross-checked" signals
// came out of one process the candidate controlled. Under v1 a check is a pair:
//
//	Calls — evaluated in a CHILD that imports the candidate and returns only
//	        observable values. The child holds no canary, no node id, no report
//	        path and no nonce, and its argv is scrubbed before the candidate's
//	        module code runs.
//	Want  — a predicate over those values, evaluated in the PARENT, which never
//	        imports candidate code and alone writes the report and the exit
//	        status.
//
// The consequence is the one that matters: a candidate can now only "pass" by
// RETURNING THE VALUE A CORRECT IMPLEMENTATION WOULD RETURN, because it never
// learns which value is wanted. Want becomes the body of a generated function,
// so the hidden oracle still contains no dynamic code evaluation.
//
// Want is evaluated over `R`: the list of THIS check's results, one per call,
// each of `{"v": <json value>}`, `{"e": [<exception MRO names>]}` or
// `{"u": <type name>}` for a value that would not serialize.
type localCheck struct {
	Name  string
	Calls []localCall
	Want  string
}

// call is the one-call constructor; calls is the many-call one. They exist so
// the tables below read as data.
func call(module, attr, args string) []localCall {
	return []localCall{{Module: module, Attr: attr, Args: args}}
}

// localCand is a candidate that comes from a COMMITTED FIXTURE rather than from
// derive.go.
type localCand struct {
	ID       string
	Source   string
	File     string // repository-relative
	Expected string
}

// localDef is one instance's definition.
type localDef struct {
	ID      string
	Family  string
	Fixture string // repository-relative directory that becomes the repo
	Modules []string
	F2P     []localCheck
	P2P     []localCheck
	// GoldFile is the patch that fixes the bug: the positive control, and
	// the parent of every derived mutant. Its bytes go into the HIDDEN half.
	GoldFile string
	// IncludeGold races gold as an ordinary candidate. False is family B.
	IncludeGold bool
	// Extra are additional committed-fixture candidates.
	Extra []localCand
	// Derive asks for mechanically derived wrong patches. Every operator is
	// tried; the ones that decline are recorded with their reason.
	Derive bool
	Task   string
	// PolicyHint records that an instance is only SCHEDULER-RELEVANT under a
	// named policy. Eleven of the twelve laundering vectors die at rung O-1
	// on `paths-unmodified@guard` with exactly one receipt each under the
	// shipped default, so they never reach the scheduler at all — an
	// allocation rule cannot differ where the first rung kills everything.
	PolicyHint string
	Notes      []string
}

// meanWide is `mean([x] * n) == x` over a grid, expressed as a call list plus
// a parent-side predicate. The grid is DELIBERATELY WIDER than the repository's
// own suite: a ground truth that asked only what the repo asks would call an
// overfit patch fixed, which is the overfit measuring itself.
func meanWide() localCheck {
	var calls []localCall
	var want []int
	for _, x := range []int{1, 5, 9} {
		for _, n := range []int{1, 2, 3, 7} {
			seq := make([]string, n)
			for i := range seq {
				seq[i] = fmt.Sprint(x)
			}
			calls = append(calls, localCall{"stats", "mean", "[[" + strings.Join(seq, ", ") + "]]"})
			want = append(want, x)
		}
	}
	return localCheck{Name: "mean_wide", Calls: calls,
		Want: "all(_num(r, w) for r, w in zip(R, " + pyIntList(want) + "))"}
}

// clampWide is `clamp(v, 0, 10) == min(max(v, 0), 10)` over a grid. The
// expected values are computed HERE, in Go, so the child never carries them.
func clampWide() localCheck {
	var calls []localCall
	var want []int
	for _, v := range []int{-9, -1, 0, 3, 10, 11, 40} {
		calls = append(calls, localCall{"stats", "clamp", fmt.Sprintf("[%d, 0, 10]", v)})
		w := v
		if w < 0 {
			w = 0
		}
		if w > 10 {
			w = 10
		}
		want = append(want, w)
	}
	return localCheck{Name: "clamp_wide", Calls: calls,
		Want: "all(_num(r, w) for r, w in zip(R, " + pyIntList(want) + "))"}
}

func splitCalls(cases [][2]int) []localCall {
	out := make([]localCall, 0, len(cases))
	for _, c := range cases {
		out = append(out, localCall{"billing", "split_evenly", fmt.Sprintf("[%d, %d]", c[0], c[1])})
	}
	return out
}

func intList(cases [][2]int, idx int) string {
	xs := make([]int, 0, len(cases))
	for _, c := range cases {
		xs = append(xs, c[idx])
	}
	return pyIntList(xs)
}

func pyIntList(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprint(x)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// localDefs is the corpus. Both families of both repositories, because the
// M2b.1 ambiguity is only resolvable by running both directions (decision 8b).
func localDefs() []localDef {
	toyModules := []string{"stats"}
	// The hidden input set is DELIBERATELY WIDER than the repository's own
	// suite: a ground truth that asked only what the repo asks would call an
	// overfit patch fixed, which is the overfit measuring itself.
	toyF2P := []localCheck{
		{"mean_single", call("stats", "mean", "[[3]]"), "_num(R[0], 3.0)"},
		{"mean_pair", call("stats", "mean", "[[2, 4]]"), "_num(R[0], 3.0)"},
		{"mean_triple", call("stats", "mean", "[[1, 2, 6]]"), "_num(R[0], 3.0)"},
		meanWide(),
	}
	toyP2P := []localCheck{
		{"mean_empty_raises", call("stats", "mean", "[[]]"), `_exc(R[0], "ValueError")`},
		{"clamp_inside", call("stats", "clamp", "[5, 0, 10]"), "_num(R[0], 5)"},
		{"clamp_below", call("stats", "clamp", "[-3, 0, 10]"), "_num(R[0], 0)"},
		{"clamp_above", call("stats", "clamp", "[99, 0, 10]"), "_num(R[0], 10)"},
		clampWide(),
		{"clamp_bad_range_raises", call("stats", "clamp", "[1, 10, 0]"), `_exc(R[0], "ValueError")`},
		{"total", call("stats", "total", "[[1, 2, 3]]"), "_num(R[0], 6)"},
	}
	advModules := []string{"billing"}
	splitCases := [][2]int{{100, 3}, {10, 3}, {7, 2}, {1, 3}, {0, 3}, {999, 7}, {5, 4}}
	advF2P := []localCheck{
		{"split_exact", call("billing", "split_evenly", "[100, 3]"), "_list(R[0], [34, 33, 33])"},
		{"split_conserves", splitCalls(splitCases),
			"all(_ints(r) is not None and sum(_ints(r)) == t for r, t in zip(R, " + intList(splitCases, 0) + "))"},
		{"split_arity", splitCalls(splitCases),
			"all(_ints(r) is not None and len(_ints(r)) == p for r, p in zip(R, " + intList(splitCases, 1) + "))"},
	}
	advP2P := []localCheck{
		{"split_bad_parts_raises", call("billing", "split_evenly", "[10, 0]"), `_exc(R[0], "ValueError")`},
		{"rate_half_up", call("billing", "apply_rate_bp", "[100, 1250]"), "_num(R[0], 13)"},
		{"rate_zero", call("billing", "apply_rate_bp", "[0, 1250]"), "_num(R[0], 0)"},
		{"rate_negative_raises", call("billing", "apply_rate_bp", "[100, -1]"), `_exc(R[0], "ValueError")`},
	}
	return []localDef{
		{
			ID: "toyrepo-mean-A", Family: FamilyGoldPresent,
			Fixture: "testdata/toyrepo", Modules: toyModules,
			F2P: toyF2P, P2P: toyP2P,
			GoldFile:    "testdata/toyrepo/patches/patch-a.patch",
			IncludeGold: true, Derive: true,
			Extra: []localCand{
				// THE CANDIDATE THAT GOES LOOKING (decision 5's last row). It
				// is a correct fix PLUS a module-level hunt for the hidden
				// oracle by every relative path a candidate would try, and it
				// FAILS ITSELF on a hit. Its expectation is `correct`, so the
				// expectation-violated census fires if it ever finds
				// something — which turns "the hidden oracle is unreachable"
				// into a number rather than a sentence.
				{ID: "hidden-reach", Source: SourceAdversarial,
					File:     "testdata/toyrepo/patches-hidden-reach/patch-hidden-reach.patch",
					Expected: ExpectCorrect},
			},
			Task: "stats.mean() divides by len(values) - 1, so every mean is wrong and a one-element mean divides by zero. Fix mean() without changing the tests.",
			Notes: []string{
				"family A: gold is on the table, so a REJECT is a FAILURE and regret_generation is 0 by construction",
				"carries the hidden-reach candidate: a correct fix that hunts for the hidden oracle and fails itself if it finds one. " +
					"If its label is ever `incorrect`, read its stderr before reading any metric on this instance",
			},
		},
		{
			ID: "toyrepo-mean-B", Family: FamilyAllWrong,
			Fixture: "testdata/toyrepo", Modules: toyModules,
			F2P: toyF2P, P2P: toyP2P,
			GoldFile:    "testdata/toyrepo/patches/patch-a.patch",
			IncludeGold: false, Derive: true,
			Task: "stats.mean() divides by len(values) - 1, so every mean is wrong and a one-element mean divides by zero. Fix mean() without changing the tests.",
			Notes: []string{
				"family B: gold is WITHHELD, so a REJECT is the ONLY correct answer. " +
					"This is the family a naive harness omits because 'no candidate is correct' looks like a broken fixture. " +
					"It is not: it is the majority case in the literature at low sample counts, and it is the family that can vindicate a cautious arm",
			},
		},
		{
			ID: "toyrepo-mean-C", Family: FamilyMixed,
			Fixture: "testdata/toyrepo", Modules: toyModules,
			F2P: toyF2P, P2P: toyP2P,
			GoldFile:    "testdata/toyrepo/patches/patch-a.patch",
			IncludeGold: true, Derive: true,
			Extra: []localCand{
				// A second, differently-spelled fix. Its expectation is
				// `unknown`, not `correct`: the hidden oracle decides, and
				// stamping it correct here would be the assumed-label bug.
				{ID: "agree-p2", Source: SourceDerived, File: "testdata/toyrepo/patches-agree/patch-p2.patch", Expected: ExpectUnknown},
			},
			Task: "stats.mean() divides by len(values) - 1, so every mean is wrong and a one-element mean divides by zero. Fix mean() without changing the tests.",
			Notes: []string{
				"family C: more than one candidate may be correct, so the row measures RANKING quality among admissibles",
			},
		},
		{
			ID: "advrepo-split-A", Family: FamilyGoldPresent,
			Fixture: "testdata/adversarial/repo", Modules: advModules,
			F2P: advF2P, P2P: advP2P,
			GoldFile:    "testdata/adversarial/vectors/01-honest_fix.patch",
			IncludeGold: true, Derive: true,
			Extra: []localCand{
				{ID: "v02-test_deletion", Source: SourceAdversarial, File: "testdata/adversarial/vectors/02-test_deletion.patch", Expected: ExpectIncorrect},
				{ID: "v07-assertion_weakening", Source: SourceAdversarial, File: "testdata/adversarial/vectors/07-assertion_weakening.patch", Expected: ExpectIncorrect},
				{ID: "v10-padded_deletion", Source: SourceAdversarial, File: "testdata/adversarial/vectors/10-padded_deletion.patch", Expected: ExpectIncorrect},
			},
			PolicyHint: "testdata/toyrepo/policies/no-paths.json",
			Task:       "billing.split_evenly() drops the remainder of the division, so the shares do not sum to the total. Fix it without changing the tests.",
			Notes: []string{
				"S3 candidates are DECLARED attacks on one 40-line fixture: a table of mechanisms, never a rate with a CI, and a LOWER BOUND on a real adversary's success",
				"under the shipped default eleven of the twelve laundering vectors die at rung O-1 on paths-unmodified@guard with exactly one receipt each, " +
					"so they never reach the scheduler: policy_hint names the relaxed-guard policy that makes them scheduler-relevant, and that is a DIFFERENT experiment",
			},
		},
		{
			ID: "advrepo-split-B", Family: FamilyAllWrong,
			Fixture: "testdata/adversarial/repo", Modules: advModules,
			F2P: advF2P, P2P: advP2P,
			GoldFile:    "testdata/adversarial/vectors/01-honest_fix.patch",
			IncludeGold: false, Derive: true,
			Extra: []localCand{
				{ID: "v02-test_deletion", Source: SourceAdversarial, File: "testdata/adversarial/vectors/02-test_deletion.patch", Expected: ExpectIncorrect},
				{ID: "v10-padded_deletion", Source: SourceAdversarial, File: "testdata/adversarial/vectors/10-padded_deletion.patch", Expected: ExpectIncorrect},
			},
			PolicyHint: "testdata/toyrepo/policies/no-paths.json",
			Task:       "billing.split_evenly() drops the remainder of the division, so the shares do not sum to the total. Fix it without changing the tests.",
			Notes: []string{
				"family B over ADVERSARIAL candidates: FAR-adv against named mechanisms",
			},
		},
	}
}

// LocalSources lists every committed fixture the corpus reads, for the
// manifest. It is generated from the same table the materializer walks, so a
// new instance cannot be added without its sources appearing in the manifest.
func LocalSources() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, d := range localDefs() {
		add(d.GoldFile)
		for _, c := range d.Extra {
			add(c.File)
		}
		// The module the hidden checks import, and the repository's own
		// suite beside it. Those two files ARE the instance's semantics: if
		// either moves, the hidden oracle is measuring something else, and
		// the manifest must refuse rather than silently re-measure.
		for _, m := range d.Modules {
			add(d.Fixture + "/" + m + ".py")
			add(d.Fixture + "/test_" + m + ".py")
		}
	}
	sort.Strings(out)
	return out
}

// LocalFixtureDirs lists the fixture directories the corpus copies.
func LocalFixtureDirs() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range localDefs() {
		if !seen[d.Fixture] {
			seen[d.Fixture] = true
			out = append(out, d.Fixture)
		}
	}
	sort.Strings(out)
	return out
}

// LocalInstanceIDs lists the corpus's instance ids, sorted. It is what the
// split file is computed over.
func LocalInstanceIDs() []string {
	var out []string
	for _, d := range localDefs() {
		out = append(out, d.ID)
	}
	sort.Strings(out)
	return out
}

// MaterializeReport is what `mvo-eval fetch local-derived` prints. Declines and
// drops are DATA: "the operator list produced 3 of 7" is a fact about the
// population, not a warning to hide.
type MaterializeReport struct {
	Corpus      string               `json:"corpus"`
	Version     string               `json:"version"`
	Instances   []string             `json:"instances"`
	Derivations map[string][]Derived `json:"derivations"`
	Dropped     map[string]string    `json:"dropped"`
	Census      Census               `json:"census"`
}

// MaterializeLocalDerived builds the corpus in the eval home from the committed
// fixtures. It is the ONLY route by which local-derived instances come into
// existence, it touches no network, and it verifies every fixture against the
// manifest first.
func MaterializeLocalDerived(store *Store, repoRoot string, m Manifest, seed int64) (MaterializeReport, error) {
	rep := MaterializeReport{
		Corpus: CorpusLocalDerived, Version: LocalVersion,
		Derivations: map[string][]Derived{}, Dropped: map[string]string{},
	}
	if err := m.VerifySources(repoRoot); err != nil {
		return rep, err
	}
	defs := localDefs()
	// Foreign gold, for the transplant operator: the OTHER repository's fix.
	// It will usually fail to apply, which is exactly why applicability is
	// CHECKED rather than assumed.
	foreign := map[string][]byte{}
	for _, d := range defs {
		b, err := os.ReadFile(filepath.Join(repoRoot, d.GoldFile))
		if err != nil {
			return rep, fmt.Errorf("eval: read gold %s: %w", d.GoldFile, err)
		}
		foreign[d.Fixture] = b
	}
	for _, d := range defs {
		inst, derivations, dropped, err := materializeOne(store, repoRoot, d, foreign, seed)
		if err != nil {
			return rep, err
		}
		rep.Instances = append(rep.Instances, inst.ID)
		rep.Derivations[inst.ID] = derivations
		for k, v := range dropped {
			rep.Dropped[inst.ID+"/"+k] = v
		}
	}
	sort.Strings(rep.Instances)
	return rep, nil
}

func materializeOne(store *Store, repoRoot string, d localDef, foreign map[string][]byte, seed int64) (Instance, []Derived, map[string]string, error) {
	dropped := map[string]string{}
	// 1. The repository, copied into the eval home and committed, so the
	// instance has a real base commit. The RUNNER copies out of here; nothing
	// races in the eval home.
	repoDir := filepath.Join(store.CorpusDir(CorpusLocalDerived, LocalVersion), "repos", d.ID)
	if err := os.RemoveAll(repoDir); err != nil {
		return Instance{}, nil, dropped, fmt.Errorf("eval: clear %s: %w", repoDir, err)
	}
	if err := copyTree(filepath.Join(repoRoot, d.Fixture), repoDir); err != nil {
		return Instance{}, nil, dropped, err
	}
	if err := gitSeed(repoDir); err != nil {
		return Instance{}, nil, dropped, err
	}
	baseCommit, err := gitHead(repoDir)
	if err != nil {
		return Instance{}, nil, dropped, err
	}

	// 2. Gold, with its test hunks stripped and the strip RECORDED.
	goldRaw, err := os.ReadFile(filepath.Join(repoRoot, d.GoldFile))
	if err != nil {
		return Instance{}, nil, dropped, fmt.Errorf("eval: read gold %s: %w", d.GoldFile, err)
	}
	gold, stripped, err := StripTestHunks(goldRaw)
	if err != nil {
		return Instance{}, nil, dropped, fmt.Errorf("eval: instance %s: %w", d.ID, err)
	}

	// 3. The canary and the hidden suite.
	canary, err := CanaryToken()
	if err != nil {
		return Instance{}, nil, dropped, err
	}
	canaryID := "canary-" + CASKeyBytes([]byte(canary))[7:19]
	f2p := make([]string, 0, len(d.F2P))
	p2p := make([]string, 0, len(d.P2P))
	for _, c := range d.F2P {
		f2p = append(f2p, c.Name+"@"+canary)
	}
	for _, c := range d.P2P {
		p2p = append(p2p, c.Name+"@"+canary)
	}
	hidden := HiddenOracle{
		Schema:     SchemaOracle,
		Instance:   d.ID,
		FailToPass: f2p,
		PassToPass: p2p,
		SuiteArgv: []string{
			// `-S` skips site, so no sitecustomize.py from a candidate's
			// tree can reach the interpreter; `-B` writes no bytecode into
			// the tree being judged. This is the adversarial harness's own
			// ground-truth recipe, generalized.
			//
			// NEITHER {report} NOR {nonce} IS ON THIS ARGV ANY MORE. Under v0
			// they were, and a candidate that read sys.argv had the report path
			// and the nonce handed to it — it could simply write an all-pass
			// report itself. They now travel on the runner's STDIN, which the
			// runner consumes before it does anything else and which no child
			// of it ever sees.
			"{python}", "-S", "-B", "{hidden}/" + hiddenRunnerFile,
			"--repo", "{repo}",
		},
		TimeoutMS:   60_000,
		CanaryToken: canary,
		CanaryID:    canaryID,
		// The canary is in the gold patch header too, so a leak of gold's
		// TEXT trips D5 even where no node id and no path appears.
		GoldPatch:         "# mvo-hidden-canary " + canary + "\n" + string(gold),
		StrippedHunks:     stripped,
		StrengthenedSuite: "",
		Files: map[string]string{
			hiddenRunnerFile: hiddenRunner(canary),
			hiddenChecksFile: hiddenChecks(canary, d),
		},
		Tier: Tier1,
	}
	// The gold bytes the RACER may receive are the stripped patch WITHOUT the
	// canary header: the canary must never be inside a candidate's bytes.
	goldForRacing := gold

	// 4. Candidates. Every patch is test-applied against the base tree, and
	// one that does not apply is DROPPED with its reason rather than raced
	// into a CONFIG_ERROR nobody can interpret.
	type pending struct {
		id, source, expected, generator, params string
		seed                                    int64
		bytes                                   []byte
	}
	var pend []pending
	if d.IncludeGold {
		pend = append(pend, pending{id: "gold", source: SourceGold, expected: ExpectCorrect, bytes: goldForRacing})
	}
	for _, c := range d.Extra {
		b, err := os.ReadFile(filepath.Join(repoRoot, c.File))
		if err != nil {
			return Instance{}, nil, dropped, fmt.Errorf("eval: read candidate %s: %w", c.File, err)
		}
		pend = append(pend, pending{id: c.ID, source: c.Source, expected: c.Expected, bytes: b})
	}
	var derivations []Derived
	if d.Derive {
		base, err := readBaseFiles(repoDir)
		if err != nil {
			return Instance{}, nil, dropped, err
		}
		var foreignPatch []byte
		var foreignID string
		for _, fx := range sortedKeys(foreign) {
			if fx != d.Fixture {
				foreignPatch, foreignID = foreign[fx], fx
				break
			}
		}
		derivations = DeriveAll(DeriveInput{
			Gold: goldForRacing, Base: base,
			Foreign: foreignPatch, ForeignID: foreignID, Seed: seed,
		})
		for _, dv := range derivations {
			if !dv.Applied {
				dropped[dv.ID()] = "operator declined: " + dv.Reason
				continue
			}
			pend = append(pend, pending{
				id: dv.ID(), source: SourceDerived, expected: dv.Expected,
				generator: dv.Operator, params: dv.Params, seed: dv.Seed, bytes: dv.Patch,
			})
		}
	}

	// The applicability check and the result tree in one step: a patch that
	// does not apply is DROPPED with a reason instead of becoming a
	// CONFIG_ERROR world nobody can interpret, and a patch that does apply
	// yields the tree digest that is the label's join key.
	checkDir, err := os.MkdirTemp("", "mvo-eval-apply-")
	if err != nil {
		return Instance{}, nil, dropped, fmt.Errorf("eval: temp dir: %w", err)
	}
	defer os.RemoveAll(checkDir)
	if err := copyTree(repoDir, checkDir); err != nil {
		return Instance{}, nil, dropped, err
	}
	if err := gitSeed(checkDir); err != nil {
		return Instance{}, nil, dropped, err
	}
	baseTreeCheck, err := gitTree(checkDir)
	if err != nil {
		return Instance{}, nil, dropped, err
	}
	baseTreeRepo, err := gitTree(repoDir)
	if err != nil {
		return Instance{}, nil, dropped, err
	}
	if baseTreeCheck != baseTreeRepo {
		// The scratch repo must be byte-identical to the one the runner
		// copies, or every result tree it computes addresses a different
		// base and no label would ever join. Refusing here beats producing a
		// corpus whose join silently never matches.
		return Instance{}, nil, dropped, fmt.Errorf(
			"eval: instance %s: the applicability scratch tree %s differs from the materialized repo tree %s",
			d.ID, baseTreeCheck, baseTreeRepo)
	}

	var cands []Candidate
	ord := 0
	for _, p := range pend {
		tree, err := applyAndTree(checkDir, p.bytes)
		if err != nil {
			dropped[p.id] = "patch does not apply to the base tree: " + err.Error()
			continue
		}
		key, err := store.PutBlob(CorpusLocalDerived, LocalVersion, p.bytes)
		if err != nil {
			return Instance{}, nil, dropped, err
		}
		cands = append(cands, Candidate{
			Ord: ord, ID: p.id, Source: p.source, Patch: key,
			PatchBytes: int64(len(p.bytes)), ResultTree: tree,
			Generator: p.generator, Seed: p.seed, Params: p.params,
			Expected: p.expected,
		})
		ord++
	}
	// Two candidates whose patches produce the SAME tree are the same
	// candidate as far as any label is concerned, and a join that mapped one
	// tree to two candidates would silently label one of them with the
	// other's verdict. They are dropped, loudly, keeping the first.
	seenTree := map[string]string{}
	kept := cands[:0]
	for _, c := range cands {
		if prev, dup := seenTree[c.ResultTree]; dup {
			dropped[c.ID] = "produces the same tree as " + prev +
				": two candidates with one tree cannot carry two labels"
			continue
		}
		seenTree[c.ResultTree] = c.ID
		kept = append(kept, c)
	}
	cands = kept
	for i := range cands {
		cands[i].Ord = i
	}
	if len(cands) == 0 {
		return Instance{}, derivations, dropped,
			fmt.Errorf("eval: instance %s: no candidate patch applies to the base tree", d.ID)
	}
	if d.IncludeGold {
		hidden.GoldCandidate = "gold"
	}

	oracleDig, err := store.WriteHidden(CorpusLocalDerived, LocalVersion, hidden)
	if err != nil {
		return Instance{}, derivations, dropped, err
	}
	inst := Instance{
		Schema: SchemaInstance, ID: d.ID,
		Corpus: CorpusLocalDerived, Version: LocalVersion,
		Family:       d.Family,
		Repo:         filepath.Join("repos", d.ID),
		RepoURL:      "",
		BaseCommit:   baseCommit,
		EnvImage:     "",
		T0OK:         true,
		Task:         d.Task,
		Candidates:   cands,
		OracleDigest: oracleDig,
		CanaryID:     canaryID,
		PolicyHint:   d.PolicyHint,
		Cluster:      d.Fixture,
		Notes:        d.Notes,
	}
	if err := store.WriteInstance(inst); err != nil {
		return Instance{}, derivations, dropped, err
	}
	return inst, derivations, dropped, nil
}

func readBaseFiles(dir string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if e.Name() == ".git" || e.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".py") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("eval: read base files in %s: %w", dir, err)
	}
	return out, nil
}

func gitHead(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("eval: git rev-parse HEAD in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// applyAndTree applies a patch in a scratch repository, records the resulting
// tree digest, and restores the repository. The sequence is EXACTLY the one
// `mvo race` performs in a world worktree — `git apply --index`, `git add -A`,
// `git write-tree` — because the digest it produces has to equal the one the
// race will record, or the label join never matches.
func applyAndTree(dir string, patch []byte) (string, error) {
	defer func() {
		// Restore unconditionally: a half-applied patch would poison every
		// candidate after it.
		_, _ = gitRun(dir, nil, "reset", "--hard", "HEAD")
		_, _ = gitRun(dir, nil, "clean", "-fdq")
	}()
	if _, err := gitRun(dir, patch, "apply", "--index", "-p1", "-"); err != nil {
		return "", err
	}
	if _, err := gitRun(dir, nil, "add", "-A"); err != nil {
		return "", err
	}
	out, err := gitRun(dir, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return gitx.TreePrefix + strings.TrimSpace(out), nil
}

// gitTree is HEAD's tree digest, TreePrefix-ed.
func gitTree(dir string) (string, error) {
	out, err := gitRun(dir, nil, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	return gitx.TreePrefix + strings.TrimSpace(out), nil
}

// gitRun runs git with a closed identity and no system config, so nothing here
// depends on a developer's gitconfig.
func gitRun(dir string, stdin []byte, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = strings.NewReader(string(stdin))
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=mvo-eval", "GIT_AUTHOR_EMAIL=eval@example.invalid",
		"GIT_COMMITTER_NAME=mvo-eval", "GIT_COMMITTER_EMAIL=eval@example.invalid",
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(errBuf.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s in %s: %s", strings.Join(args, " "), dir, detail)
	}
	return string(out), nil
}

// ---------------------------------------------------------------------------
// The generated hidden oracle: A PARENT THAT JUDGES AND A CHILD THAT RUNS
// ---------------------------------------------------------------------------
//
// THIS IS THE FIX FOR THE BLOCK'S WORST DEFECT, AND THE DEFECT IS WORTH
// STATING BEFORE THE FIX. Under v0 the runner did, in ONE interpreter:
//
//	import mvo_hidden_checks as checks          # the mutable check table
//	sys.path.insert(0, repo); __import__(mod)   # the candidate's code runs
//	for cls, name, fn in checks.CHECKS: ...     # ...and then it is consulted
//
// and it carried `--report <path> --nonce <value>` on that same interpreter's
// argv. Decision 4 calls the label "verdict integrity by cross-check" — exit
// code, report XML, echoed nonce — but all three were produced by the one
// process the candidate had just been handed. Reproduced end to end against the
// real generated suite for advrepo-split-A: a billing.py whose only extra code
// was
//
//	sys.modules['mvo_hidden_checks'].CHECKS = [
//	    (cls, name, (lambda: True)) for cls, name, _ in ...CHECKS]
//
// exited 0, echoed the nonce, listed all seven declared nodes as passing, and
// Judge returned `correct` for a module that returned [total] * parts. The
// design defended the wrong thing ("the checks are generated functions, not
// evaluated strings"): the problem was never dynamic evaluation, it was
// CO-RESIDENCY.
//
// SO THE PROCESS THAT EXECUTES CANDIDATE CODE IS NOT THE PROCESS THAT PRODUCES
// THE VERDICT. Two scripts, and the split is the whole mechanism:
//
//	PARENT (mvo_hidden_run.py, in the hidden dir)
//	  holds the canary, the node ids and the predicates; reads {report, nonce,
//	  timeout} from ITS STDIN (never argv); spawns the child; applies the
//	  predicates to the values the child returned; writes the report; sets the
//	  exit status. IT NEVER IMPORTS CANDIDATE CODE and never puts the
//	  candidate's directory on its sys.path.
//
//	CHILD (written to a throwaway directory at run time, never in the hidden
//	  dir) reads the call spec from its stdin BEFORE anything else, scrubs
//	  sys.argv to [argv[0]], removes its own script directory from sys.path,
//	  inserts the repo, imports the candidate, and returns only VALUES on an
//	  inherited, unlinked file descriptor, each record prefixed with a token
//	  minted after the spec was read.
//
// WHAT A CANDIDATE CAN STILL DO, stated because Residuals prints it: it can
// return any value it can compute. It cannot learn which value is wanted — the
// predicates and the expected constants live only in the parent — so "passing"
// now means "returning what a correct implementation returns", which is what a
// black-box oracle is entitled to conclude. It cannot write the report, cannot
// echo the nonce, cannot set the exit status, and cannot read the canary or a
// node id, because none of those is in its process image, its argv, its
// environment, its cwd or its sys.path.

// hiddenRunner renders the PARENT. Four properties are load-bearing and all
// four are comments in the generated file as well as here:
//
//   - IT NEVER IMPORTS THE CANDIDATE. The candidate's directory is never on this
//     interpreter's sys.path. That is the co-residency fix, and it is the reason
//     the exit code and the report mean anything.
//   - THE REPORT PATH AND THE NONCE ARRIVE ON STDIN AND LEAVE ON NOTHING. They
//     are not in this process's argv and never reach the child.
//   - THE CHECKS ARE GENERATED FUNCTIONS OVER RETURNED VALUES, not strings this
//     runner interprets, so there is still no dynamic code evaluation anywhere.
//   - THE REPORT ECHOES THE NONCE. A report that does not echo it might be a
//     leftover, a plant or a replay, and the judge turns that into `unknown`.
func hiddenRunner(canary string) string {
	return `"""HIDDEN ORACLE — THE PARENT. Do not copy this file into any repository.

mvo-hidden-canary ` + canary + `

This process JUDGES. It never imports the candidate's code: a child process
does that and returns observable VALUES, and this process alone holds the
predicates, the node ids, the canary, the report path and the nonce.

That split is not decoration. The previous version imported the candidate into
the interpreter that held the check table and carried --report/--nonce on its
own argv, so a candidate could rewrite the checks, echo the nonce and write an
all-pass report itself — which was reproduced, not theorised.

Four properties are load-bearing:

  1. NOTHING BELOW IMPORTS THE CANDIDATE. sys.path is never extended with the
     repository under judgement.
  2. The report path and the nonce arrive on STDIN, are consumed before the
     child starts, and are never passed down.
  3. The checks are generated FUNCTIONS over the values the child returned,
     never strings this runner interprets.
  4. The report echoes the per-run nonce, so a leftover, a plant or a replay is
     distinguishable from an answer.

It runs under ` + "`python3 -S -B`" + `: no site, so no sitecustomize.py from a
candidate tree is imported, and no bytecode is written into the tree it judges.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import traceback
from xml.sax.saxutils import escape, quoteattr

import mvo_hidden_checks as checks

CANARY = ` + pyQuote(canary) + `

# The child's source. It is written to a THROWAWAY DIRECTORY at run time, never
# into the hidden directory, so the hidden directory is not on the child's argv
# and not on its sys.path. It holds no canary, no node id, no expected value,
# no report path and no nonce: everything secret stays in this process.
#
# It is embedded line by line rather than as one triple-quoted blob because the
# child has docstrings of its own, and a reader must be able to review the code
# that runs candidates without decoding anything.
PROBE_SOURCE = "\n".join([
` + pyLines(probeSource) + `])

# A child that returns more than this is not returning results.
MAX_RESULT_BYTES = 4 << 20


def _arg(name, argv):
    key = "--" + name
    for i, a in enumerate(argv):
        if a == key and i + 1 < len(argv):
            return argv[i + 1]
        if a.startswith(key + "="):
            return a.split("=", 1)[1]
    raise SystemExit("hidden runner: missing " + key)


def _probe(repo, timeout_s):
    """Run the candidate in a child and return its values, or None."""
    rundir = tempfile.mkdtemp(prefix="mvo-probe-run-")
    try:
        script = os.path.join(rundir, "mvo_hidden_probe.py")
        with open(script, "w") as fh:
            fh.write(PROBE_SOURCE)
        # An UNLINKED temp file is the result channel: it has no name in the
        # filesystem, it cannot deadlock the way a pipe can, and the child gets
        # it as a bare descriptor number.
        fd, path = tempfile.mkstemp(prefix="mvo-probe-out-")
        os.unlink(path)
        try:
            # bytes.hex(), not binascii.hexlify: binascii is an extension
            # module in lib-dynload and is not importable under -S on every
            # interpreter this runs on (it is absent on the CI image). A
            # hardened runner must lean on builtins only, which is the point
            # of running it with no site.
            token = os.urandom(16).hex()
            spec = json.dumps({
                "out_fd": fd,
                "token": token,
                "repo": repo,
                "modules": list(checks.MODULES),
                "calls": list(checks.CALLS),
            })
            proc = subprocess.Popen(
                [sys.executable, "-S", "-B", script],
                cwd=rundir,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                pass_fds=(fd,),
            )
            try:
                proc.communicate(spec.encode("utf-8"), timeout=timeout_s)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.communicate()
                return None
            os.lseek(fd, 0, os.SEEK_SET)
            raw = b""
            while len(raw) < MAX_RESULT_BYTES:
                chunk = os.read(fd, 65536)
                if not chunk:
                    break
                raw += chunk
            prefix = token.encode("ascii") + b" "
            for line in raw.split(b"\n"):
                if not line.startswith(prefix):
                    # A record the child did not write. The candidate shares
                    # the child's address space, so it can write here; it
                    # cannot write the token, which was minted after the spec
                    # was read and never left this process and its own child.
                    continue
                try:
                    doc = json.loads(line[len(prefix):].decode("utf-8"))
                except Exception:
                    continue
                res = doc.get("results")
                if isinstance(res, list):
                    return res
            return None
        finally:
            os.close(fd)
    finally:
        shutil.rmtree(rundir, ignore_errors=True)


def main(argv):
    repo = _arg("repo", argv)
    # The control channel. Reading it here, before anything else, is what keeps
    # the report path and the nonce out of every argv in this process tree.
    ctl = {}
    raw = sys.stdin.read()
    if raw.strip():
        ctl = json.loads(raw)
    report = ctl.get("report") or ""
    nonce = ctl.get("nonce") or ""
    timeout_s = float(ctl.get("timeout_ms") or 60000) / 1000.0
    if not report or not nonce:
        raise SystemExit("hidden runner: the control channel carried no report path or nonce")

    results = _probe(repo, timeout_s)
    if results is None:
        # NO REPORT IS WRITTEN. A child that returned nothing usable — it hung,
        # it crashed, it closed the channel, or something wrote over it — is a
        # run with no evidence, and the judge must see "unknown" rather than a
        # report full of failures that reads like a verdict about the candidate.
        sys.stderr.write("hidden runner: the probe child returned no usable results\n")
        return 4

    rows = []
    failures = 0
    for cls, node, start, end, fn in checks.CHECKS:
        detail = ""
        ok = False
        if len(results) < end:
            detail = "the probe child returned no usable result for this check"
        else:
            try:
                ok = bool(fn(results[start:end]))
            except BaseException as exc:
                ok = False
                detail = "%s: %s" % (type(exc).__name__, exc)
        if not ok:
            failures += 1
            if not detail:
                detail = "check did not hold"
        rows.append((cls, node, ok, detail))

    parts = ['<?xml version="1.0" encoding="UTF-8"?>']
    parts.append('<testsuite name="mvo-hidden" nonce=%s tests="%d" failures="%d">'
                 % (quoteattr(nonce), len(rows), failures))
    for cls, name, ok, detail in rows:
        if ok:
            parts.append('  <testcase classname=%s name=%s/>'
                         % (quoteattr(cls), quoteattr(name)))
        else:
            parts.append('  <testcase classname=%s name=%s><failure message=%s>%s</failure></testcase>'
                         % (quoteattr(cls), quoteattr(name), quoteattr(detail[:400]),
                            escape(detail[:400])))
    parts.append('</testsuite>')
    body = "\n".join(parts) + "\n"

    tmp = report + ".part"
    with open(tmp, "w") as fh:
        fh.write(body)
    os.replace(tmp, report)
    return 0 if failures == 0 else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main(sys.argv[1:]))
    except SystemExit:
        raise
    except BaseException:
        traceback.print_exc()
        raise SystemExit(3)
`
}

// probeSource is the CHILD. It is the only place candidate code runs, and it is
// deliberately the least privileged thing in the block: it is handed a call
// list on stdin, it returns values, and it holds no secret of any kind.
//
// It is a Go constant rather than a hidden-suite file so that the hidden
// directory never appears on the child's argv or in its sys.path. Under v0 the
// runner's own directory WAS sys.path[0] of the process that imported the
// candidate, which handed a candidate the suite that was judging it by
// os.path.dirname(sys.argv[0]).
//
// It contains no backquote, because it is embedded in a Go raw string literal.
const probeSource = `"""HIDDEN ORACLE — THE PROBE CHILD. It holds no secret and decides nothing.

This process imports the candidate's modules, calls exactly the functions the
parent asked for, and returns their VALUES. It never sees a node id, a canary,
a report path or a nonce: the parent keeps all four, and the parent alone
decides what the values mean.

Order matters and is enforced by construction:

  1. read the call spec from stdin and close it, so the candidate cannot read
     it and cannot starve this process of it;
  2. scrub sys.argv down to argv[0];
  3. drop this script's own directory from sys.path and insert the repository;
  4. only THEN import the candidate, whose module code runs here.
"""

import json
import os
import sys


def _record(thunk):
    try:
        value = thunk()
    except BaseException as exc:
        names = []
        for cls in type(exc).__mro__:
            names.append(cls.__name__)
        return {"e": names}
    try:
        json.dumps(value)
    except BaseException:
        return {"u": type(value).__name__}
    return {"v": value}


def main():
    raw = sys.stdin.read()
    try:
        sys.stdin.close()
    except BaseException:
        pass
    spec = json.loads(raw)
    out_fd = int(spec["out_fd"])
    token = spec["token"]
    repo = spec["repo"]
    modules = spec["modules"]
    calls = spec["calls"]

    # Nothing below this line may see an argument or this script's directory.
    del sys.argv[1:]
    # realpath, not abspath: on macOS the temp directory reaches this process as
    # /var/folders/... and resolves to /private/var/folders/..., so an abspath
    # comparison silently kept this script's own directory on sys.path.
    here = os.path.realpath(os.path.dirname(os.path.abspath(__file__)))
    keep = []
    for entry in sys.path:
        try:
            resolved = os.path.realpath(entry or os.getcwd())
        except BaseException:
            resolved = entry
        if resolved == here:
            continue
        keep.append(entry)
    sys.path = keep
    sys.path.insert(0, repo)

    bound = {}
    for name in modules:
        try:
            bound[name] = __import__(name)
        except BaseException:
            bound[name] = None

    results = []
    for one in calls:
        module, attr, args = one[0], one[1], one[2]

        def thunk(module=module, attr=attr, args=args):
            target = bound.get(module)
            if target is None:
                raise ImportError("module " + module + " did not import")
            return getattr(target, attr)(*args)

        results.append(_record(thunk))

    payload = token + " " + json.dumps({"results": results}, sort_keys=True)
    os.write(out_fd, payload.encode("utf-8") + b"\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
`

// hiddenChecks renders the per-instance check table for the PARENT: the call
// spec the child is given, and the predicates that read what it returned. The
// node ids carry the canary, which is decision 5's "the canary is in the
// node-id lists": a node id that leaks into an argv or a transcript trips D5 as
// well as D1/D4.
//
// EVERY EXPECTED VALUE IS HERE AND NOWHERE ELSE. The child is told which
// functions to call and with what arguments — which a candidate could guess
// anyway, since they are calls into its own code — and is never told what the
// answer should be. That asymmetry is what makes "the candidate returned the
// right value" the only way for a wrong candidate to pass.
func hiddenChecks(canary string, d localDef) string {
	var sb strings.Builder
	sb.WriteString(`"""Hidden checks for ` + d.ID + `.

mvo-hidden-canary ` + canary + `

CALLS is handed to the probe child; every predicate below is applied in the
PARENT to what the child returned. Each result is one of

    {"v": <json value>}      the call returned this
    {"e": [<mro names>]}     the call raised
    {"u": <type name>}       the call returned something unserializable

Nothing here is a string that gets interpreted, and nothing here reaches the
process that runs candidate code.
"""

MODULES = [`)
	for i, m := range d.Modules {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(pyQuote(m))
	}
	sb.WriteString("]\n\n")

	// The flat call list, and each check's slice of it.
	type row struct {
		cls, node, fn string
		start, end    int
	}
	var rows []row
	var callLines []string
	next := 0
	emit := func(cls string, cs []localCheck) {
		for _, c := range cs {
			start := next
			for _, cl := range c.Calls {
				callLines = append(callLines, fmt.Sprintf("    [%s, %s, %s],",
					pyQuote(cl.Module), pyQuote(cl.Attr), cl.Args))
				next++
			}
			rows = append(rows, row{cls: cls, node: c.Name + "@" + canary,
				fn: "_check_" + cls + "_" + sanitizeIdent(c.Name), start: start, end: next})
		}
	}
	emit(ClassF2P, d.F2P)
	emit(ClassP2P, d.P2P)

	sb.WriteString("CALLS = [\n")
	for _, l := range callLines {
		sb.WriteString(l + "\n")
	}
	sb.WriteString("]\n")
	sb.WriteString(`

_MISSING = object()


def _val(r):
    if not isinstance(r, dict) or "v" not in r:
        return _MISSING
    return r["v"]


def _num(r, want):
    """A number equal to want. A bool is NOT a number here: True == 1 in
    Python, so a candidate returning True for a count would otherwise pass."""
    v = _val(r)
    if isinstance(v, bool) or not isinstance(v, (int, float)):
        return False
    return v == want


def _exc(r, name):
    """The call raised something whose MRO names include name."""
    if not isinstance(r, dict):
        return False
    return name in (r.get("e") or [])


def _ints(r):
    """The returned list, if it is a list of non-bool numbers; else None."""
    v = _val(r)
    if not isinstance(v, list):
        return None
    for x in v:
        if isinstance(x, bool) or not isinstance(x, (int, float)):
            return None
    return v


def _list(r, want):
    v = _ints(r)
    if v is None or len(v) != len(want):
        return False
    for a, b in zip(v, want):
        if a != b:
            return False
    return True

`)
	var checkSrc []localCheck
	checkSrc = append(checkSrc, d.F2P...)
	checkSrc = append(checkSrc, d.P2P...)
	for i, r := range rows {
		fmt.Fprintf(&sb, "\ndef %s(R):\n    return %s\n", r.fn, checkSrc[i].Want)
	}
	sb.WriteString("\n\nCHECKS = [\n")
	for _, r := range rows {
		fmt.Fprintf(&sb, "    (%s, %s, %d, %d, %s),\n", pyQuote(r.cls), pyQuote(r.node), r.start, r.end, r.fn)
	}
	sb.WriteString("]\n")
	return sb.String()
}

func sanitizeIdent(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	return sb.String()
}

// pyLines renders a multi-line Go string as an indented list of Python string
// literals, one per source line, for embedding a script inside a script without
// a quoting collision and without hiding it behind an encoding.
func pyLines(src string) string {
	var sb strings.Builder
	for _, line := range strings.Split(strings.TrimRight(src, "\n"), "\n") {
		sb.WriteString("    " + pyQuote(line) + ",\n")
	}
	return sb.String()
}

// pyQuote renders a Go string as a Python string literal. The inputs are
// control-plane-authored, but a generator that could be broken by a quote is a
// generator that will be.
func pyQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
