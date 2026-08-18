package main

// The smaller subcommands: arms, derive, score, report, leakcheck,
// import-worlds, adjudicate, freeze.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/eval"
	"github.com/coagente/multiverso/internal/gitx"
)

// cmdArms prints the arm declaration table. It is a separate verb because the
// mapping to PRD §11 is printed with every table anyway, and a reader who wants
// only the mapping should not have to run an experiment to see it.
func cmdArms(args []string) error {
	fs := flag.NewFlagSet("arms", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit the declaration table as JSON")
	if err := fs.Parse(args); err != nil {
		return codedError{code: exitUsage, msg: err.Error()}
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(eval.Arms())
	}
	fmt.Println(joinLines(eval.ArmMapping()))
	return nil
}

// cmdDerive prints the derived population for one instance's gold patch,
// declines included. It writes nothing: it is the verb for asking "what would
// this operator list produce here?" before spending minutes racing it.
func cmdDerive(args []string) error {
	fs := flag.NewFlagSet("derive", flag.ContinueOnError)
	instance, args := takeLeadingArg(args)
	common := addCommon(fs)
	seed := fs.Int64("seed", 20260817, "derivation seed")
	jsonOut := fs.Bool("json", false, "emit the derivations as JSON")
	if err := fs.Parse(args); err != nil {
		return codedError{code: exitUsage, msg: err.Error()}
	}
	if instance == "" {
		return codedError{code: exitUsage, msg: "usage: mvo-eval derive <instance> [flags]"}
	}
	store, home, err := openStore(common)
	if err != nil {
		return err
	}
	_ = home
	inst, err := store.LoadInstance(common.corpus, common.version, instance)
	if err != nil {
		return skipf("%v", err)
	}
	hidden, _, err := store.LoadHidden(inst)
	if err != nil {
		return err
	}
	base, err := baseFilesOf(store.RepoPath(inst))
	if err != nil {
		return err
	}
	gold := strings.TrimPrefix(hidden.GoldPatch, "# mvo-hidden-canary "+hidden.CanaryToken+"\n")
	ds := eval.DeriveAll(eval.DeriveInput{Gold: []byte(gold), Base: base, Seed: *seed})
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ds)
	}
	for _, d := range ds {
		if d.Applied {
			fmt.Printf("%-20s APPLIED  expected=%-9s params=%s\n", d.Operator, d.Expected, d.Params)
			continue
		}
		fmt.Printf("%-20s DECLINED expected=%-9s %s\n", d.Operator, d.Expected, d.Reason)
	}
	fmt.Println("the generator PROPOSES, the hidden oracle LABELS: no expectation above is a label, " +
		"and the report prints an expectation-violated census rather than asserting these are wrong")
	return nil
}

// cmdScore labels an existing, SEALED workspace. It exists so the scoring half
// can be exercised (and its determinism asserted) without racing anything.
//
// IT IS SUBJECT TO DECISION 6, and it was not. This is the verb that opens the
// hidden oracle (store.LoadHidden), runs it and writes labels — and it had no
// --split flag, ran no freeze check and appended no eval-use line. All three
// lived in `run`'s finish(), so an eval-split instance could be scored again
// and again under a moved policy digest leaving no freeze mismatch, no
// --unfreeze reason and no run-count inflation. The freeze file's own notes say
// twice that `mvo-eval score --split eval` refuses. Now it does, through the
// same helper `run` uses, and every scoring appends its line.
func cmdScore(args []string) error {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	common := addCommon(fs)
	instance := fs.String("instance", "", "instance id")
	workspace := fs.String("workspace", "", "a sealed workspace to label")
	python := fs.String("python", "python3", "interpreter for the hidden suite")
	scoreRoot := fs.String("score-root", "", "the scorer's own tmpdir (default: a fresh one)")
	split := fs.String("split", "", "declare which split half this scoring is on: dev | eval. "+
		"On `eval` the freeze binds and a moved policy digest or scheduler constant REFUSES")
	unfreeze := fs.String("unfreeze", "", "proceed despite freeze drift on the eval split, recording this reason")
	repoRoot := fs.String("repo-root", ".", "repository root, for the split and freeze files")
	jsonOut := fs.Bool("json", false, "emit the batch as JSON")
	if err := fs.Parse(args); err != nil {
		return codedError{code: exitUsage, msg: err.Error()}
	}
	if *instance == "" || *workspace == "" {
		return codedError{code: exitUsage, msg: "usage: mvo-eval score --instance <id> --workspace <dir>"}
	}
	if _, err := exec.LookPath(*python); err != nil {
		return skipf("%s is not on PATH: %s", *python, eval.SkipToolAbsent)
	}
	// An instance the split file assigns to `eval`, scored WITHOUT --split eval,
	// is the accidental version: the freeze would have bound and did not. It is
	// refused by name rather than silently allowed.
	effSplit := *split
	if assigned, known := splitOf(*repoRoot, common.corpus, common.version, *instance); known {
		if assigned == eval.SplitEval && *split != eval.SplitEval && *unfreeze == "" {
			return codedError{code: exitUsage, msg: fmt.Sprintf(
				"mvo-eval score: %s is on the EVAL split and --split eval was not passed: the freeze would "+
					"not have bound, so this scoring would leave no trace of a moved policy digest or a moved "+
					"scheduler constant. Pass --split eval (the freeze then binds) or --unfreeze \"<reason>\"",
				*instance)}
		}
		if *split == "" {
			effSplit = assigned
		}
	}
	freezeDigest, drift, ferr := checkFreeze(*repoRoot, common.corpus, common.version, effSplit, *unfreeze, "score")
	if ferr != nil {
		return ferr
	}
	store, home, err := openStore(common)
	if err != nil {
		return err
	}
	inst, err := store.LoadInstance(common.corpus, common.version, *instance)
	if err != nil {
		return skipf("%v", err)
	}
	hidden, hiddenBytes, err := store.LoadHidden(inst)
	if err != nil {
		return err
	}
	view, err := eval.ReadLedger(*workspace)
	if err != nil {
		return err
	}
	root := *scoreRoot
	if root == "" {
		root, err = os.MkdirTemp("", "mvo-eval-score-")
		if err != nil {
			return fmt.Errorf("mvo-eval score: temp dir: %w", err)
		}
		defer os.RemoveAll(root)
	}
	_, baseTree, err := gitx.Head(*workspace)
	if err != nil {
		return fmt.Errorf("mvo-eval score: base tree: %w", err)
	}
	scorer, err := eval.NewScorer(inst, hidden, hiddenBytes, *workspace, root, *python)
	if err != nil {
		return err
	}
	defer scorer.Close()
	batch, err := scorer.ScoreBatch(view, baseTree)
	if err != nil {
		return err
	}
	seal := view.Seal()
	if err := store.Apply(inst.Corpus, inst.Version, seal, batch.Labels); err != nil {
		return err
	}
	// THE EVAL-USE COUNTER. Each scoring is a leaderboard query in Blum &
	// Hardt's sense and the count is published, so the verb that actually runs
	// the hidden oracle appends its own line rather than relying on `run` to
	// have done it.
	runs := 0
	_ = store.AppendEvalRun(common.corpus, eval.EvalRun{
		BinaryDigest: "", PolicyDigest: defaultPolicyDigest(),
		Arms: []string{"score"}, InstanceCount: 1,
		Split: effSplit, Unfreeze: *unfreeze,
	})
	if rs, rerr := store.ReadEvalRuns(common.corpus); rerr == nil {
		runs = len(rs)
	}
	if err := scorer.AssertScratchDrained(); err != nil {
		return err
	}
	_ = home
	_ = freezeDigest
	for _, d := range drift {
		fmt.Printf("FREEZE DRIFT: %s frozen=%s now=%s\n", d.What, d.Frozen, d.Now)
	}
	if *unfreeze != "" {
		fmt.Printf("UNFREEZE: %s\n", *unfreeze)
	}
	fmt.Printf("split: %s; eval-use count for %s: %d scoring(s)\n",
		orDash(effSplit), common.corpus, runs)
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(batch)
	}
	fmt.Printf("instance %s: decision %s (seq %d) sealed before %d label(s) were written\n",
		inst.ID, seal.DecisionType, seal.DecisionSeq, len(batch.Labels))
	fmt.Printf("controls: negative ran=%v ok=%v; positive ran=%v ok=%v %s\n",
		batch.Controls.NegativeRan, batch.Controls.NegativeOK,
		batch.Controls.PositiveRan, batch.Controls.PositiveOK, batch.Controls.Detail)
	for _, l := range batch.Labels {
		fmt.Printf("  %-34s %-9s tier=%d %s f2p=%d/%d p2p=%d/%d expected=%s\n",
			l.Candidate, l.Verdict, l.Tier, l.Reason,
			l.F2PPassed, l.F2PTotal, l.P2PPassed, l.P2PTotal, l.Expected)
	}
	return nil
}

// cmdReport renders a saved run manifest. It is the read-only half of `run`, so
// a report can be regenerated from a recorded manifest without re-racing.
func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	path := fs.String("manifest", "", "a run manifest written by `mvo-eval run --json`")
	if err := fs.Parse(args); err != nil {
		return codedError{code: exitUsage, msg: err.Error()}
	}
	if *path == "" {
		return codedError{code: exitUsage, msg: "usage: mvo-eval report --manifest <file>"}
	}
	b, err := os.ReadFile(*path)
	if err != nil {
		return fmt.Errorf("mvo-eval report: %w", err)
	}
	var wrapper struct {
		Manifest eval.RunManifest `json:"manifest"`
	}
	if err := json.Unmarshal(b, &wrapper); err != nil {
		return fmt.Errorf("mvo-eval report: decode %s: %w", *path, err)
	}
	fmt.Println(joinLines(wrapper.Manifest.Render()))
	return nil
}

// cmdLeakcheck runs D1..D5 over a workspace against one instance's needles. It
// is the verb the acceptance script uses for the tripwire test, and it exits
// non-zero on a hit.
func cmdLeakcheck(args []string) error {
	fs := flag.NewFlagSet("leakcheck", flag.ContinueOnError)
	common := addCommon(fs)
	instance := fs.String("instance", "", "instance id")
	workspace := fs.String("workspace", "", "workspace to scan")
	jsonOut := fs.Bool("json", false, "emit the report as JSON")
	if err := fs.Parse(args); err != nil {
		return codedError{code: exitUsage, msg: err.Error()}
	}
	if *instance == "" || *workspace == "" {
		return codedError{code: exitUsage, msg: "usage: mvo-eval leakcheck --instance <id> --workspace <dir>"}
	}
	store, home, err := openStore(common)
	if err != nil {
		return err
	}
	inst, err := store.LoadInstance(common.corpus, common.version, *instance)
	if err != nil {
		return skipf("%v", err)
	}
	hidden, hiddenBytes, err := store.LoadHidden(inst)
	if err != nil {
		return err
	}
	needles := eval.NeedlesFor(inst, hidden, hiddenBytes, home)
	view, err := eval.ReadLedger(*workspace)
	if err != nil {
		return err
	}
	o := runOpts{common: common}
	rep, err := o.detect(*workspace, needles, inst, view, hidden)
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		// SCANNED AND SKIPPED ARE PRINTED SEPARATELY. "We scanned three of six
		// transcripts" and "we scanned six" are different claims, and a cap that
		// is silent is a hole.
		fmt.Printf("detectors run: %s; surfaces scanned: %v; surfaces a detector could NOT read: %v\n",
			strings.Join(rep.Detectors, " "), rep.Scanned, rep.Skipped)
		for _, l := range rep.Lines() {
			fmt.Println(l)
		}
	}
	if rep.Void() {
		return codedError{code: exitLeak, msg: fmt.Sprintf(
			"mvo-eval leakcheck: %s: instance %s is VOIDED and its row is dropped from every metric",
			eval.SkipLeakDetected, inst.ID)}
	}
	fmt.Println("no detector fired")
	return nil
}

// cmdImportWorlds extracts S4 candidates from a real run's ledger. The path
// exists so that the day a real run is recorded, its patches become a candidate
// source without a code change — and until then S4 IS AN EMPTY CORPUS, and
// pooling it with S1/S2 in one number is forbidden by the tagging rule.
func cmdImportWorlds(args []string) error {
	fs := flag.NewFlagSet("import-worlds", flag.ContinueOnError)
	workspace, args := takeLeadingArg(args)
	// The common flags are accepted (and ignored) so that a caller can pass
	// the same flag set to every verb: this one reads a LEDGER, not the eval
	// home, which is the whole point of it being the S4 seam.
	_ = addCommon(fs)
	jsonOut := fs.Bool("json", false, "emit the triples as JSON")
	if err := fs.Parse(args); err != nil {
		return codedError{code: exitUsage, msg: err.Error()}
	}
	if workspace == "" {
		return codedError{code: exitUsage, msg: "usage: mvo-eval import-worlds <workspace> [flags]"}
	}
	view, err := eval.ReadLedger(workspace)
	if err != nil {
		return err
	}
	type triple struct {
		World      string `json:"world"`
		BaseCommit string `json:"base_commit"`
		Tree       string `json:"tree"`
		Patch      string `json:"patch"`
		Adapter    string `json:"adapter"`
		Model      string `json:"model"`
		Source     string `json:"source"`
		Outcome    string `json:"outcome"`
	}
	var out []triple
	for _, w := range view.Worlds {
		out = append(out, triple{
			World: w.Digest, BaseCommit: "", Tree: w.World.Tree, Patch: w.World.Patch,
			Adapter: w.World.Producer.Adapter, Model: w.World.Producer.Model,
			Source: eval.SourceRecorded, Outcome: w.World.Outcome,
		})
	}
	scripted := 0
	for _, t := range out {
		if strings.HasPrefix(t.Adapter, "script") {
			scripted++
		}
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
	} else {
		for _, t := range out {
			fmt.Printf("%s adapter=%s model=%s tree=%s patch=%s outcome=%s\n",
				t.World, t.Adapter, orDash(t.Model), short(t.Tree), short(t.Patch), t.Outcome)
		}
	}
	fmt.Printf("%d world(s), of which %d were produced by the SCRIPT adapter. "+
		"A scripted patch is not agent output: %s is an EMPTY corpus in this repository, "+
		"and a table that pooled these with S1/S2 would be reporting a property of the fixtures as a property of agents.\n",
		len(out), scripted, eval.SourceRecorded)
	return nil
}

// cmdAdjudicate records a Tier-3 human verdict beside the Tier-1 label. The
// RATERS are out of scope — that is human labour — but the format ships so a
// label can be upgraded without re-racing a single instance.
func cmdAdjudicate(args []string) error {
	fs := flag.NewFlagSet("adjudicate", flag.ContinueOnError)
	common := addCommon(fs)
	instance := fs.String("instance", "", "instance id")
	candidate := fs.String("candidate", "", "candidate id")
	verdict := fs.String("verdict", "", "correct | incorrect | unknown")
	raters := fs.String("raters", "", "comma-separated rater ids (two independent raters)")
	agreement := fs.String("agreement", "", "unanimous | majority | split")
	notes := fs.String("notes", "", "free-text rationale")
	if err := fs.Parse(args); err != nil {
		return codedError{code: exitUsage, msg: err.Error()}
	}
	if *instance == "" || *candidate == "" || *verdict == "" || *raters == "" || *agreement == "" {
		return codedError{code: exitUsage, msg: "usage: mvo-eval adjudicate --instance <id> --candidate <id> " +
			"--verdict <v> --raters <a,b> --agreement <unanimous|majority|split> [--notes <text>]"}
	}
	switch *verdict {
	case eval.VerdictCorrect, eval.VerdictIncorrect, eval.VerdictUnknown:
	default:
		return codedError{code: exitUsage, msg: "mvo-eval adjudicate: verdict " + *verdict +
			" is not in the closed vocabulary"}
	}
	rs := splitList(*raters)
	if len(rs) < 2 {
		return codedError{code: exitUsage, msg: "mvo-eval adjudicate: Tier 3 needs TWO INDEPENDENT RATERS; " +
			"a single rater is a second opinion, not an adjudication, and inter-rater agreement is the number it exists to report"}
	}
	store, _, err := openStore(common)
	if err != nil {
		return err
	}
	a := eval.Adjudication{
		Schema: eval.SchemaAdjudication, Instance: *instance, Candidate: *candidate,
		Verdict: *verdict, Tier: eval.Tier3, Raters: rs, Agreement: *agreement, Notes: *notes,
	}
	if err := store.WriteAdjudication(common.corpus, common.version, a); err != nil {
		return err
	}
	fmt.Printf("recorded a Tier-3 adjudication beside the Tier-1 label for %s/%s. "+
		"The Tier-1 label is NOT overwritten: a label that was upgraded must still show what the "+
		"automated tier said, or the disagreement rate becomes unrecoverable.\n", *instance, *candidate)
	return nil
}

// cmdFreeze records the materialized oracle digests into the eval home. The
// committed freeze file cannot pin them for local-derived, because those oracles
// are generated under a fresh canary on every fetch; this writes the pin for a
// SPECIFIC materialization, where it means something.
func cmdFreeze(args []string) error {
	fs := flag.NewFlagSet("freeze", flag.ContinueOnError)
	common := addCommon(fs)
	write := fs.Bool("write", false, "write the freeze file into the eval home")
	if err := fs.Parse(args); err != nil {
		return codedError{code: exitUsage, msg: err.Error()}
	}
	store, _, err := openStore(common)
	if err != nil {
		return err
	}
	ids, err := store.ListInstances(common.corpus, common.version)
	if err != nil {
		return err
	}
	fz := eval.FreezeFile{
		Schema: eval.SchemaFreeze, Corpus: common.corpus, Version: common.version,
		FrozenAt:     time.Now().UTC().Format(time.RFC3339),
		PolicyDigest: defaultPolicyDigest(),
		Constants:    eval.SchedulerConstants(),
		// The ALLOCATION RULE, not only the scheduler's numbers (M2b.2
		// decision 8). A freeze that could not pin it would be refused by the
		// binary that wrote it the moment it was read.
		Rules:         eval.SchedulerRules(),
		OracleDigests: map[string]string{},
	}
	for _, id := range ids {
		inst, err := store.LoadInstance(common.corpus, common.version, id)
		if err != nil {
			return err
		}
		fz.OracleDigests[id] = inst.OracleDigest
	}
	b, err := json.MarshalIndent(fz, "", "  ")
	if err != nil {
		return err
	}
	if !*write {
		fmt.Println(string(b))
		fmt.Println("(not written: pass --write)")
		return nil
	}
	p := filepath.Join(store.CorpusDir(common.corpus, common.version), "freeze.json")
	if err := os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("mvo-eval freeze: write %s: %w", p, err)
	}
	fmt.Printf("wrote %s pinning %d oracle digest(s)\n", p, len(fz.OracleDigests))
	return nil
}

func openStore(c *commonFlags) (*eval.Store, string, error) {
	home := c.home
	if home == "" {
		var err error
		home, err = eval.HomeFromEnv()
		if err != nil {
			return nil, "", err
		}
	}
	store, err := eval.OpenStore(home)
	if err != nil {
		return nil, home, skipf("%s: %v", eval.SkipCorpusAbsent, err)
	}
	if !store.CorpusPresent(c.corpus, c.version) {
		return nil, home, skipf("%s: %s is absent", eval.SkipCorpusAbsent,
			store.CorpusDir(c.corpus, c.version))
	}
	return store, home, nil
}

func baseFilesOf(dir string) (map[string]string, error) {
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
		return nil, fmt.Errorf("mvo-eval: read base files: %w", err)
	}
	return out, nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

var _ = sort.Strings
