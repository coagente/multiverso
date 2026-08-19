package main

// `mvo-eval run` — THE PROTOCOL.
//
// Per instance, in this order and no other:
//
//	1. RACE THE REFERENCE ARM (A3: the fixed ladder, unbudgeted), REPLICATED.
//	   Its ledger is the source of three things at once — every candidate's
//	   world, the full-evidence decision d*, and M2b.1's allocation bound
//	   (minspend, S) — so the budget levels the other arms are given are DERIVED
//	   FROM A MEASUREMENT rather than chosen. Which is why it is replicated:
//	   taken once, the experiment's independent variable is a noisy draw, and
//	   across three runs of one cell minspend came out 1961 / 1537 / 983 ms.
//	   B comes from the MEDIAN and the spread is printed.
//	2. SCORE — after the ledger is sealed, on fresh reconstructions in the
//	   scorer's own tmpdir, with the batch controls binding. Labels are joined
//	   to worlds by TREE, which is what lets one scoring serve every arm.
//	3. RACE THE BUDGETED ARMS at the derived budget, R replicates each,
//	   INTERLEAVED, and take each arm's modal decision.
//	4. EVALUATE THE DERIVED ARMS over the reference ledger, each charged its
//	   declared evidence footprint — and CARRY the charge onto the row, so every
//	   arm lands on the same cost/accuracy axis.
//	5. RUN THE LEAK DETECTORS over EVERY WORKSPACE the instance raced, not just
//	   the reference one: the budgeted arms' workspaces are where every reported
//	   decision comes from. A hit VOIDS the instance, prints the token and the
//	   hitting path, and exits non-zero. There is no "reported with a caveat".
//	6. COMPUTE THE METRICS per (arm, family, POLICY), print the census above
//	   them, and print BOTH FAMILY COLUMNS side by side — always, because the
//	   same 5/5 REJECT is a plain failure on family A and the only correct
//	   answer on family B.
//
// Step 1 before step 2 before step 3 is not a convenience. It is the ordering
// the whole block rests on: the label cannot exist before the decision it
// scores, and the seal is what proves it did not.
//
// THE POLICY IS A CELL KEY. An instance carrying a PolicyHint races under its
// hint, not under the cell's --policy, because under the shipped guards eleven
// of the twelve laundering vectors die at rung O-1 and never reach the
// scheduler. That is deliberate; pooling the result into a cell captioned with
// the OTHER policy was not. Every row carries the policy it actually raced
// under and Compute is called per policy, so a printed cell is uniform by
// construction. --policy-override clears the hints when a uniform cell over the
// whole corpus is what is wanted.

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
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/schedule"
)

type runOpts struct {
	common     *commonFlags
	mvo        string
	arms       string
	instances  string
	split      string
	replicates int
	level      string
	budget     int64
	strict     bool
	seed       int64
	floor      int
	jsonOut    string
	unfreeze   string
	policy     string
	policyOver bool
	refReps    int
	keepUnbudg bool
	repoRoot   string
	keep       string
	python     string
	selector   string
	// selectors is --selector parsed as a LIST. More than one rule races
	// every rule inside ONE run, against ONE reference draw and therefore ONE
	// derived B — which is blocker B1's fix, and the reason the field is a
	// slice rather than a string.
	selectors []string
	// armIDs is --arms parsed. It is kept apart from RunManifest.Arms, which
	// records the TREATMENTS that raced (one per rule for the adaptive arm),
	// because the plan is built from the arm table and the manifest reports
	// what actually ran.
	armIDs []string
	// warmup is M2d.1 decision 1: {auto|N|0}. `auto` races into a TEMPLATE
	// until every kind the pinned policy can buy carries a local fit, or
	// refuses by name; `0` is the COLD instrument, kept because accept step
	// m2d1-9a has to be able to reproduce M2b.2's vacuum on purpose.
	warmup string
	// warmAuto / warmRaces are ParseWarmup's answer, resolved once.
	warmAuto  bool
	warmRaces int
	// allowVacuous is decision 7's one escape hatch. It prints the same
	// banner, exits 0, and stamps VACUOUS on every table it produced —
	// because the flag that suppresses a refusal must not also suppress its
	// caption.
	allowVacuous bool
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	o := runOpts{common: addCommon(fs)}
	fs.StringVar(&o.mvo, "mvo", "", "path to the mvo binary (required: a run must name the binary that produced its numbers)")
	fs.StringVar(&o.arms, "arms", eval.ArmAdaptive+","+eval.ArmFixedBudget, "comma-separated budgeted arms to race")
	fs.StringVar(&o.instances, "instances", "", "comma-separated instance ids (default: every instance in the cache)")
	fs.StringVar(&o.split, "split", "", "restrict to a split: dev | eval (default: no restriction)")
	fs.IntVar(&o.replicates, "replicates", 3, "replicates per arm (no verdict below 3)")
	fs.StringVar(&o.level, "level", "B2", "budget level derived from the reference run: B1 | B2 | B3")
	fs.Int64Var(&o.budget, "budget", 0, "explicit oracle budget in ms for every arm (overrides --level)")
	fs.BoolVar(&o.strict, "strict", false, "exit non-zero when nothing could be scored")
	fs.Int64Var(&o.seed, "seed", 20260817, "seed for the derived random arm and the bootstrap")
	fs.IntVar(&o.floor, "instance-floor", eval.InstanceFloorDefault, "declared instance floor for inferential statistics")
	fs.StringVar(&o.jsonOut, "json", "", "write the signed run manifest here as JSON")
	fs.StringVar(&o.unfreeze, "unfreeze", "", "proceed despite freeze drift, recording this reason")
	fs.StringVar(&o.policy, "policy", "", "policy file every instance without a PolicyHint races under (default: the workspace default `mvo init` writes)")
	fs.BoolVar(&o.policyOver, "policy-override", false,
		"race EVERY instance under --policy, ignoring its PolicyHint. The hint exists because eleven of the "+
			"twelve laundering vectors die at rung O-1 under the shipped guards and never reach the scheduler, "+
			"so overriding it makes those instances scheduler-irrelevant — and it makes the cell UNIFORM, "+
			"which is the only way to publish a single-policy number over the whole corpus")
	fs.IntVar(&o.refReps, "reference-replicates", 3,
		"replicates of the UNBUDGETED reference race whose median minspend derives B (1 is a noisy draw: "+
			"across three runs of the same cell minspend for one instance came out 1961/1537/983 ms)")
	fs.BoolVar(&o.keepUnbudg, "keep-unbudgeted", false,
		"keep rows whose derived budget came out 0, which `mvo intent new` reads as UNBOUNDED. They are "+
			"excluded by default: a row handed infinite money inside a cell captioned with the tightest "+
			"budget is not budget-matched, and absent source implies absent metric")
	fs.StringVar(&o.repoRoot, "repo-root", ".", "repository root, for the split and freeze files")
	fs.StringVar(&o.keep, "keep", "", "keep the run's workspaces in this directory")
	fs.StringVar(&o.python, "python", "python3", "interpreter for the hidden suite")
	fs.StringVar(&o.selector, "selector", "",
		"which allocation RULE(s) the adaptive arm races under: voc (M2b's published rule, the binary "+
			"default), voc2 (M2b.2's finishing rule), or a COMMA-SEPARATED LIST of both. It is how the "+
			"published M2d numbers stay reproducible on a binary that ships the revision: the arm, the "+
			"instances, the labels, the scoring and the metrics are unchanged, and only the rule moves. "+
			"A LIST RACES EVERY RULE INSIDE ONE RUN, which is the only way two rules can be compared at "+
			"the same budget: B is derived from the instance's own reference races, so two RUNS of one "+
			"cell take two reference draws and get two budgets — measured, 1553 ms against 1013 ms on the "+
			"same instance on the same day, under a caption reading ORACLE-BUDGET-MATCHED. One run, one "+
			"draw, one B, and the harness refuses if the arms ever disagree about it")
	fs.StringVar(&o.warmup, "warmup", eval.WarmupAuto,
		"how the COST TABLE is priced before the arms race against it (M2d.1 decision 1): `auto` races an "+
			"UNBUDGETED warm-up into a template until every kind the pinned policy can buy carries a local "+
			"fit (cap 3, then `warm_incomplete` naming the unpriced kinds); `N` pins a count; `0` is the COLD "+
			"instrument. On a cold workspace nothing is priced, so an unpriced purchase is affordable while "+
			"any pool remains, THE BUDGET DOES NOT BIND, and every allocation rule collapses to the "+
			"exhaustive ladder — which is what made every M2d cell byte-identical between voc and voc2. "+
			"Warming is charged to NO arm: it is a separate intent at --budget-oracle-ms 0")
	fs.BoolVar(&o.allowVacuous, "allow-vacuous", false,
		"print the tables of a cell whose rule under test never fired, stamped VACUOUS, and exit 0 instead "+
			"of 5. The banner and its reason are printed either way: a flag that suppresses a refusal must "+
			"not also suppress its caption")
	if err := fs.Parse(args); err != nil {
		return codedError{code: exitUsage, msg: err.Error()}
	}
	warmAuto, warmRaces, werr := eval.ParseWarmup(o.warmup)
	if werr != nil {
		return codedError{code: exitUsage, msg: werr.Error()}
	}
	o.selectors = splitList(o.selector)
	// A rule named twice would race twice and be pooled into one cell, which
	// is a replicate wearing a second rule's name. Refused at usage rather
	// than deduplicated silently.
	for i, s := range o.selectors {
		for j := 0; j < i; j++ {
			if o.selectors[j] == s {
				return codedError{code: exitUsage, msg: fmt.Sprintf(
					"mvo-eval run: --selector names %q twice: a rule raced twice inside one cell is a "+
						"replicate wearing a second rule's name", s)}
			}
		}
	}

	home := o.common.home
	if home == "" {
		var err error
		home, err = eval.HomeFromEnv()
		if err != nil {
			return err
		}
	}
	o.armIDs = splitList(o.arms)
	man := eval.RunManifest{
		Schema: eval.SchemaRun, Corpus: o.common.corpus, Version: o.common.version,
		Split: o.split, Replicates: o.replicates, BudgetLevel: o.level,
		// THE TREATMENTS THAT RACED, not the arm ids that were asked for: with
		// two rules the adaptive arm is two treatments, and a manifest whose
		// arm list did not say so would be a manifest whose rows nobody could
		// join to its header.
		Arms: armKeys(armPlan(o.armIDs, o.selectors)), CanaryVerdict: eval.CanaryNotInUse,
		Derivations:   map[string][]eval.Derived{},
		AllowVacuous:  o.allowVacuous,
		CoverageByArm: map[string]schedule.CoverageReport{},
		// B4: the refusal's real unit. One entry per (arm, instance).
		CoverageByCell: map[string]schedule.CoverageReport{},
		Notes: []string{
			"n = 1–2 repositories, synthetic candidates, Tier-1 labels, oracle-budget-matched only, " +
				"selection cost measured and uncharged, tokens and runner time unmeasured, no agent output anywhere in it. " +
				"This is a DIAGNOSIS OF A SCHEDULING RULE and it is captioned as one.",
			"THE INSTANCES ARE NESTED SLICES, NOT INDEPENDENT DRAWS. advrepo-split-B's candidate set is " +
				"advrepo-split-A's minus gold and v07; toyrepo-mean-A/B/C share one gold and one mutant pool. " +
				"Every denominator prints its cluster count, and coverage and A9's ceiling are properties of " +
				"how the corpus was assembled rather than measurements of anything.",
			"THE WRONG-CANDIDATE POOL IS UNIFORMLY TRIVIAL FOR THE HIDDEN SUITE. Three of derive.go's seven " +
				"operators decline or fail to apply on both fixtures, so each instance's S2 population is four " +
				"perturbations of a single-line gold change, every one labelled incorrect with reason f2p-fail " +
				"and expectation_violated 0. Every FAR here is therefore a FLOOR, and a FAR of 100% on a " +
				"family-B cell carries no information about a real cohort.",
		},
	}
	if len(o.selectors) > 0 {
		// A cell that cannot say which allocation rule produced it is a cell
		// whose caption is a guess (M2b.2 §5.2). The binary's default rule is
		// already recorded per race in schedule.started.constants; this is the
		// per-CELL statement, printed with the numbers.
		man.Notes = append(man.Notes, fmt.Sprintf(
			"THE ADAPTIVE ARM RACED UNDER --selector=%s. The arm, the instances, the split, the labels, "+
				"the scoring and the metrics are unchanged; the ALLOCATION RULE is what moved. "+
				"M2b.2 ships voc2 as the binary default and retains voc so every published M2b.1 and M2d "+
				"number stays reproducible under ONE binary.", strings.Join(o.selectors, ",")))
	}
	if len(o.selectors) > 1 {
		man.Notes = append(man.Notes, fmt.Sprintf(
			"THE %d RULES RACED INSIDE ONE RUN, so they share ONE warmed template, ONE reference draw and "+
				"therefore ONE derived B per instance. This is the only configuration in which two "+
				"allocation rules are comparable at all: B is derived from the reference races, so two RUNS "+
				"of one cell take two draws and are handed two budgets — measured, 1553 ms against 1013 ms "+
				"on the same instance on the same day, under a caption reading ORACLE-BUDGET-MATCHED. "+
				"Every cell prints the B it was raced at, and the run REFUSES if the arms of an instance "+
				"ever held different ones.", len(o.selectors)))
	}

	// Degradation first: an absent corpus is a NAMED SKIP for every instance
	// and NO metric line at all (decision 1b, acceptance step m2d-7a).
	store, err := eval.OpenStore(home)
	if err != nil || !store.CorpusPresent(o.common.corpus, o.common.version) {
		detail := fmt.Sprintf("%s is absent", filepath.Join(home, o.common.corpus, o.common.version))
		ids := o.instanceIDs(nil)
		if len(ids) == 0 {
			ids = []string{""}
		}
		for _, id := range ids {
			man.Census.Add(id, eval.SkipCorpusAbsent, detail)
		}
		return o.finish(man, nil, home)
	}

	// Tool prerequisites are named skips, never fabricated labels.
	if _, err := exec.LookPath("git"); err != nil {
		return skipf("git is not on PATH: the eval plane reconstructs trees with it")
	}
	if _, err := exec.LookPath(o.python); err != nil {
		for _, id := range o.instanceIDs(store) {
			man.Census.Add(id, eval.SkipToolAbsent, o.python+" is not on PATH: the hidden suite cannot run")
		}
		return o.finish(man, nil, home)
	}
	man.PolicyDigest = defaultPolicyDigest()
	man.PolicyConfig = policyName(o.policy)
	// A cell that NAMES a policy must race it or refuse. Reading the bytes
	// here — before any race — is what turns "the flag was ignored" into an
	// error instead of a silent fallback to the shipped default, which is the
	// exact shape of the bug this flag was added to fix: two cells whose
	// headers claimed opposite escalation rules and whose manifests carried a
	// byte-identical policy_digest.
	if o.policy != "" {
		b, err := os.ReadFile(o.policy)
		if err != nil {
			return codedError{code: exitFailure, msg: fmt.Sprintf(
				"mvo-eval run: --policy %s: %v: refusing to race the shipped default under another policy's name", o.policy, err)}
		}
		man.PolicyConfig = policyName(o.policy)
		man.PolicyDigest = object.DigestBytes(b)
	}

	// The freeze comes BEFORE the binary check on purpose. Both are
	// preconditions, but a refusal about freeze drift is the more important of
	// the two and must not be masked by "your --mvo path is wrong": the point
	// of the mechanism is that the drift is impossible to MISS, not merely
	// impossible to hide.
	//
	// Tuning after the freeze is not forbidden; it is made impossible to do
	// quietly.
	// The instrument this run will actually use. The COST REGIME is derived
	// from the flag here — before any race — and re-derived from the RECORDED
	// cost tables afterwards; the pre-flight check is what makes the drift
	// impossible to MISS rather than merely impossible to hide.
	liveInstrument := eval.LiveInstrument(o.warmup, schedule.BudgetBasisActual, warmRegimeOf(warmAuto, warmRaces))
	digest, drift, ferr := checkFreeze(o.repoRoot, o.common.corpus, o.common.version, o.split, o.unfreeze, "run",
		&liveInstrument)
	if ferr != nil {
		return ferr
	}
	man.FreezeDigest, man.Drift, man.Unfreeze = digest, drift, o.unfreeze

	if o.mvo == "" {
		return codedError{code: exitUsage, msg: "mvo-eval run: --mvo is required: " +
			"a run must be able to say which binary produced its numbers"}
	}
	if _, err := os.Stat(o.mvo); err != nil {
		return skipf("mvo binary %s is absent: %v", o.mvo, err)
	}
	man.BinaryDigest = fileDigest(o.mvo)

	splitFile, haveSplit := loadSplitFile(o.repoRoot, o.common.corpus, o.common.version)

	workRoot := o.keep
	if workRoot == "" {
		workRoot, err = os.MkdirTemp("", "mvo-eval-run-")
		if err != nil {
			return fmt.Errorf("mvo-eval run: temp dir: %w", err)
		}
		defer os.RemoveAll(workRoot)
	} else if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return fmt.Errorf("mvo-eval run: create %s: %w", workRoot, err)
	}

	// DECISION 2: ONE TEMPLATE PER (INSTANCE, POLICY DIGEST, BINARY), and
	// every arm and every replicate inherits it by COPY. The cost table is a
	// property of the host and the repository, not of the arm — and the
	// amortization is the whole reason it is a decision: a full protocol cell
	// races 9 times per instance, and one warm-up serves all nine.
	templates := eval.NewTemplateCache()
	o.warmAuto, o.warmRaces = warmAuto, warmRaces

	var rows []eval.Row
	for _, id := range o.instanceIDs(store) {
		if haveSplit && o.split != "" {
			sp, known := splitFile.Of(id)
			if !known || sp != o.split {
				continue
			}
		}
		inst, err := store.LoadInstance(o.common.corpus, o.common.version, id)
		if err != nil {
			man.Census.Add(id, eval.SkipInstanceAbsent, err.Error())
			continue
		}
		iRows, nc, derivs, skip, err := o.runInstance(store, home, inst, workRoot, templates, &man)
		if err != nil {
			return err
		}
		man.NonConsult = append(man.NonConsult, nc)
		if len(derivs) > 0 {
			man.Derivations[id] = derivs
		}
		if skip != "" {
			continue
		}
		rows = append(rows, iRows...)
	}
	return o.finish(man, rows, home)
}

// runInstance is the per-instance protocol. It returns the rows, the
// non-consultation proof, the derivation census and a skip reason.
func (o runOpts) runInstance(store *eval.Store, home string, inst eval.Instance,
	workRoot string, templates *eval.TemplateCache,
	man *eval.RunManifest) ([]eval.Row, eval.NonConsultation, []eval.Derived, eval.SkipReason, error) {

	nc := eval.NonConsultation{Instance: inst.ID, OracleDigest: inst.OracleDigest, CanaryID: inst.CanaryID}
	hidden, hiddenBytes, err := store.LoadHidden(inst)
	if err != nil {
		// A digest mismatch is a HARD ERROR, not a skip.
		if strings.Contains(err.Error(), string(eval.SkipDigestMismatch)) {
			return nil, nc, nil, "", err
		}
		man.Census.Add(inst.ID, eval.SkipInstanceAbsent, err.Error())
		return nil, nc, nil, eval.SkipInstanceAbsent, nil
	}
	nc.HiddenModeOK = modeAtMost(store.OraclePathFor(inst), 0o600)
	nc.HomeModeOK = modeAtMost(home, 0o700)
	needles := eval.NeedlesFor(inst, hidden, hiddenBytes, home)
	man.CanaryIDs = appendUnique(man.CanaryIDs, inst.CanaryID)

	patches, err := store.HandoffPatches(inst)
	if err != nil {
		return nil, nc, nil, "", err
	}
	// The policy this instance actually races under, recorded per instance
	// because it is NOT always the cell's policy: an instance carrying a
	// PolicyHint keeps its hint, since the hint is what makes that instance
	// scheduler-relevant at all (under the shipped guards eleven of the twelve
	// laundering vectors die at rung O-1 with one receipt each and never reach
	// the scheduler). A cell that assumed uniformity would caption itself with
	// a policy two of its five instances never saw.
	//
	// --policy-override clears the hint, which is how a UNIFORM cell over the
	// whole corpus becomes runnable. Either way the answer is recorded per
	// instance and becomes a COLUMN on every row, so metrics are computed per
	// policy and a cell can no longer pool two of them under one caption.
	policyFile := o.policy
	switch {
	case o.policyOver:
	case inst.PolicyHint != "":
		hint := filepath.Join(o.repoRoot, inst.PolicyHint)
		if _, err := os.Stat(hint); err == nil {
			policyFile = hint
		}
	}
	if man.PolicyByInstance == nil {
		man.PolicyByInstance = map[string]string{}
	}
	rowPolicy := policyName(policyFile)
	man.PolicyByInstance[inst.ID] = rowPolicy

	// 0. WARM THE COST TABLE — decisions 1, 2 and 4.
	//
	// It happens BEFORE the reference arm because every arm of this instance,
	// reference included, must inherit the SAME table: a reference raced
	// against an empty table and budgeted arms raced against a priced one
	// would derive B from a race nobody else ran.
	//
	// The warm-up is a SEPARATE INTENT at --budget-oracle-ms 0, so its spend
	// is structurally outside every arm's pool. What it cost is recorded
	// rather than merely uncharged.
	warmKey := eval.TemplateKey(inst.ID, rowPolicy+"/"+man.PolicyDigest, man.BinaryDigest)
	warm := templates.Warm(warmKey, eval.WarmSpec{
		MVO: o.mvo, Dir: filepath.Join(workRoot, sanitize(inst.ID), "template"),
		RepoSrc: store.RepoPath(inst), Patches: patches, PolicyFile: policyFile,
		Parallel: 1, Auto: o.warmAuto, Races: o.warmRaces, EvalHome: home,
	})
	man.Warmups = appendWarm(man.Warmups, warm)
	template := warm.Template

	// 1. The reference arm, REPLICATED.
	//
	// B is derived from this race's own bound (B1 = ceil(minspend x 1.1)), so
	// the independent variable of the whole experiment is a measurement taken
	// here. Taking it ONCE made it a noisy draw: across three runs of the same
	// cell, minspend for advrepo-split-B came out 1961 / 1537 / 983 ms, and B1
	// collapsed to 0 on a different instance each time. The median over
	// --reference-replicates is the treatment level; every replicate's own
	// minspend is recorded beside it so the dispersion is readable rather than
	// implied.
	refArm, _ := eval.ArmByID(eval.ArmReference)
	refReps := o.refReps
	if refReps < 1 {
		refReps = 1
	}
	var (
		refRun    eval.RunResult
		refView   eval.LedgerView
		spends    []int64
		totals    []int64
		boundOK   = true
		refDirs   []string
		refViews  []eval.LedgerView
		refBounds []schedule.BoundReport
	)
	for r := 0; r < refReps; r++ {
		dir := filepath.Join(workRoot, inst.ID, fmt.Sprintf("ref-r%d", r))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nc, nil, "", err
		}
		run, view, err := eval.Race(eval.RunSpec{
			Arm: refArm, MVO: o.mvo, Instance: inst, Patches: patches,
			WorkRoot: dir, EvalHome: home, RepoSrc: store.RepoPath(inst),
			TemplateSrc: template,
			PolicyFile:  policyFile, Parallel: 1, Needles: needles,
		})
		if err != nil {
			if r == 0 {
				man.Census.Add(inst.ID, eval.SkipPreflightAbort, err.Error())
				return nil, nc, nil, eval.SkipPreflightAbort, nil
			}
			man.Census.Add(inst.ID, eval.SkipPreflightAbort,
				fmt.Sprintf("reference replicate %d: %v", r, err))
			continue
		}
		if len(run.StubsFired) > 0 {
			return nil, nc, nil, "", fmt.Errorf(
				"mvo-eval run: a poisoned agent stub fired (%s): a code path tried to spawn a real agent CLI",
				strings.Join(run.StubsFired, " "))
		}
		b, _ := schedule.Bound(schedule.BoundInput{
			Policy: view.Policy, Worlds: view.Worlds, Receipts: view.Receipts,
			Decide: race.Decide,
		})
		if !b.Available {
			boundOK = false
		}
		spends = append(spends, b.MinSpendMS)
		totals = append(totals, b.TotalMS)
		refDirs = append(refDirs, run.Workspace)
		refViews = append(refViews, view)
		refBounds = append(refBounds, b)
		if r == 0 {
			refRun, refView = run, view
		}
	}
	if len(refViews) == 0 {
		man.Census.Add(inst.ID, eval.SkipPreflightAbort, "no reference replicate produced a ledger")
		return nil, nc, nil, eval.SkipPreflightAbort, nil
	}
	nc.ArgvClean = refRun.ArgvClean
	nc.EnvScrubbed = refRun.EnvScrubbed
	nc.EnvClean = refRun.EnvClean
	nc.CWDOutsideEvalHome = refRun.CWDOutsideEvalHome
	nc.DecisionDigest = refView.DecisionDigest
	nc.DecisionSeq = refView.DecisionSeq
	bound := refBounds[0]
	bound.MinSpendMS = medianOf(spends)
	bound.TotalMS = medianOf(totals)
	bound.Available = boundOK
	if refReps > 1 {
		man.ReferenceSpread = append(man.ReferenceSpread, fmt.Sprintf(
			"%s: minspend over %d reference replicate(s) = %v ms (median %d), S = %v ms (median %d)",
			inst.ID, len(spends), spends, bound.MinSpendMS, totals, bound.TotalMS))
	}

	// 2. The detectors, over EVERY workspace this instance produced, not just
	// the reference one. The 2 arms x R budgeted workspaces are the ones every
	// reported decision comes from; scanning only the reference meant "canary
	// clean" and "no leak detector fired" covered 1 of 11 raced workspaces per
	// instance, and specifically not the ones the numbers come from. The scans
	// are pure over recorded bytes and cost milliseconds, so there is no reason
	// to sample. Each scanned workspace is counted, so the scope is legible.
	scanned := map[string]eval.LedgerView{}
	for i, d := range refDirs {
		scanned[d] = refViews[i]
	}

	// 3. Score, after the seal.
	_, baseTree, err := gitx.Head(refRun.Workspace)
	if err != nil {
		return nil, nc, nil, "", fmt.Errorf("mvo-eval run: %s: base tree: %w", inst.ID, err)
	}
	scoreRoot := filepath.Join(workRoot, inst.ID, "score")
	scorerStart := time.Now()
	scorer, err := eval.NewScorer(inst, hidden, hiddenBytes, refRun.Workspace, scoreRoot, o.python)
	if err != nil {
		return nil, nc, nil, "", err
	}
	// The hidden suite's own directory is removed when scoring ends: it holds
	// the node ids, the canary and the gold patch, and leaving it behind would
	// make every later D5 scan of that filesystem a lie.
	defer scorer.Close()
	nc.HiddenOutsideWorkspace = !strings.HasPrefix(scorer.HiddenDir(), refRun.Workspace+string(os.PathSeparator))
	batch, err := scorer.ScoreBatch(refView, baseTree)
	if err != nil {
		return nil, nc, nil, "", err
	}
	seal := refView.Seal()
	if err := store.Apply(inst.Corpus, inst.Version, seal, batch.Labels); err != nil {
		return nil, nc, nil, "", err
	}
	nc.LabelsAfterSeal = seal.Check() == nil
	// MEASURED, not tautological. `refRun.FinishedAt != ""` was a constant true
	// — RunResult.FinishedAt is set unconditionally on both the error and the
	// success path before Race returns — so one of the nine conjuncts behind
	// "non-consultation PROVED" measured nothing. What is measured now is the
	// ordering itself: the last racer process exited before the scorer was
	// constructed. A clock corroborates rather than proves, which is why the
	// SEAL is still the conjunct that carries the claim.
	nc.ScorerAfterRacer = refRun.FinishedAtUnixMS > 0 &&
		refRun.FinishedAtUnixMS <= scorerStart.UnixMilli()
	nc.RacerExitedAtMS = refRun.FinishedAtUnixMS
	nc.ScorerStartedAtMS = scorerStart.UnixMilli()
	if !batch.Controls.OK() {
		leak, lerr := o.detectAll(scanned, needles, inst, hidden)
		if lerr != nil {
			return nil, nc, nil, "", lerr
		}
		nc.Leak = leak
		nc.Prove()
		man.Census.Add(inst.ID, eval.SkipGoldFailsControl, batch.Controls.Detail)
		return nil, nc, nil, eval.SkipGoldFailsControl, nil
	}

	avail, availUnknown := coverage(batch)
	dstar := refView.DStar()
	dstarLabel := ""
	if dstar.Type == race.TypeSelect && len(dstar.Subject) > 0 {
		if w, ok := refView.WorldByDigest(dstar.Subject[0]); ok {
			dstarLabel = batch.TreeVerdict[w.World.Tree]
		}
	}

	budget := o.budget
	if budget == 0 {
		budget = budgetLevel(o.level, bound)
	}
	// A DERIVED BUDGET OF ZERO IS NOT THE TIGHTEST BUDGET — IT IS NO BUDGET.
	//
	// B1 = ceil(minspend x 1.1), and minspend is 0 whenever the reference run
	// reaches d* without buying anything (a REJECT or an ESCALATE the first
	// rung already forces). `mvo intent new` defines --budget-oracle-ms 0 as
	// UNBOUNDED, so the level silently inverts: the row labelled "tightest
	// budget" is the one row that was handed infinite money. Both arms get the
	// same treatment, so the pairing stays fair — but the row is NOT
	// budget-matched, and a cell whose caption says B1 must not contain it at
	// all. It is EXCLUDED by default and named above the metrics; --keep-unbudgeted
	// puts it back for a reader who wants the unbounded observation, which is
	// still a real observation of M2b's inertness-under-unbounded-budget.
	unbudgeted := budget <= 0
	if unbudgeted {
		man.Unbudgeted = append(man.Unbudgeted, fmt.Sprintf(
			"%s at level %s: derived budget is 0, which `mvo intent new` reads as UNBOUNDED "+
				"(minspend=%dms, S=%dms, bound_available=%t) — this row is NOT budget-matched and is %s",
			inst.ID, o.level, bound.MinSpendMS, bound.TotalMS, bound.Available,
			map[bool]string{true: "KEPT (--keep-unbudgeted)", false: "EXCLUDED"}[o.keepUnbudg]))
	}

	// The source census of the WHOLE candidate set, not of the winners. It is
	// what the +ADVERSARIAL(declared) caption fires on.
	tableSources := sourcesOnTable(inst)
	// The cluster is the FIXTURE, not the instance: inst.Repo is "repos/<id>"
	// and is distinct per instance, which would report five nested slices of
	// two repositories as five independent bugs.
	cluster := inst.Cluster
	if cluster == "" {
		cluster = inst.Repo
	}

	var rows []eval.Row
	// 4. The budgeted arms, R replicates each, interleaved.
	type armState struct {
		arm eval.Arm
		// selector is the ALLOCATION RULE this treatment raced under. Two
		// rules are two treatments of one arm inside one run, sharing the
		// template, the reference races and B.
		selector string
		// budgets is the set of budgets this arm's races RECORDED on their own
		// `schedule.started`. Read from the artifact rather than from the
		// variable that was passed, for the reason V-6 reads the cost table
		// from the artifact: a check on the variable checks the assignment.
		budgets   map[int64]bool
		decisions []string
		winners   []string
		sources   []string
		costs     []int64
		admits    []string
		// M2d.1: the instrument's own per-replicate record. Coverage is
		// computed from the RECORDED steps of each replicate's own trace;
		// orders are the purchase sequences the paired divergence compares;
		// regimes are decision 11's derived cost-table label.
		coverage []schedule.CoverageReport
		orders   [][]string
		regimes  map[string]bool
		// tables is the set of RECORDED cost-table snapshots this arm
		// allocated against, digested. Decision 2's template makes it a
		// singleton by construction; V-6 is the assertion that it is.
		tables map[string]bool
	}
	states := map[string]*armState{}
	var order []string
	plan := armPlan(o.armIDs, o.selectors)
	for _, p := range plan {
		states[p.key] = &armState{
			arm: p.arm, selector: p.selector, budgets: map[int64]bool{},
			regimes: map[string]bool{}, tables: map[string]bool{},
		}
		order = append(order, p.key)
	}
	for r := 0; r < o.replicates; r++ {
		for _, id := range order {
			st := states[id]
			dir := filepath.Join(workRoot, inst.ID, fmt.Sprintf("%s-r%d", sanitize(id), r))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, nc, nil, "", err
			}
			run, view, err := eval.Race(eval.RunSpec{
				Arm: st.arm, MVO: o.mvo, Instance: inst, Patches: patches,
				WorkRoot: dir, EvalHome: home, RepoSrc: store.RepoPath(inst),
				TemplateSrc: template,
				// ONE `budget`, computed ONCE above from ONE reference draw,
				// handed to EVERY arm of this instance including both
				// allocation rules. That is blocker B1's fix, and it is one
				// variable rather than a promise: the assertion below reads
				// the budget each race RECORDED and refuses if they differ.
				PolicyFile: policyFile, Parallel: 1, BudgetMS: budget, Needles: needles,
				// M2b.2 decision 6: the RULE is a flag on the adaptive arm and
				// on no other. The arm table does not move — no new arm, no new
				// instance, no new metric — so the only thing this changes about
				// a published cell is which allocation rule produced it.
				ExtraFlags: selectorFlags(st.selector, st.arm.ID),
			})
			if err != nil {
				man.Census.Add(inst.ID, eval.SkipPreflightAbort,
					fmt.Sprintf("%s replicate %d: %v", id, r, err))
				continue
			}
			if len(run.StubsFired) > 0 {
				return nil, nc, nil, "", fmt.Errorf("mvo-eval run: a poisoned agent stub fired: %v", run.StubsFired)
			}
			if !run.ArgvClean || !run.EnvScrubbed || !run.EnvClean {
				return nil, nc, nil, "", fmt.Errorf(
					"mvo-eval run: %s replicate %d: the racer's argv or environment is not clean: "+
						"argv_clean=%v env_scrubbed=%v env_clean=%v", id, r, run.ArgvClean, run.EnvScrubbed, run.EnvClean)
			}
			scanned[run.Workspace] = view
			st.decisions = append(st.decisions, view.Decision.Type)
			winner, source := "", ""
			if w, ok := view.Winner(); ok {
				winner = batch.TreeVerdict[w.World.Tree]
				if winner == "" {
					winner = eval.VerdictUnknown
				}
				// BY IDENTITY, NEVER BY VERDICT. The winning world's own tree is
				// in hand here; looking a source up by scanning for the first
				// label with a matching verdict returned the alphabetically-first
				// same-verdict candidate, which on advrepo-split-B reported a
				// laundering vector's win as a derived mutant's.
				source = winnerSource(inst, w.World.Tree)
			}
			st.winners = append(st.winners, winner)
			st.sources = append(st.sources, source)
			// THE SPEND IS THE RACE WINDOW'S, NEVER THE WORKSPACE'S (decision
			// 3, falsifier V-5). A warmed workspace holds the warm-up's
			// receipts in the very same ledger, and an arm charged with them
			// would stop being budget-matched IN THE REPORT even though it is
			// matched IN THE POOL.
			st.costs = append(st.costs, oracleSpend(view))
			st.admits = append(st.admits, view.AdmitResult)

			// M2d.1: coverage from the RECORDED steps, the purchase order for
			// the paired divergence, and the cost regime derived from the
			// recorded cost table.
			st.coverage = append(st.coverage, schedule.Coverage(view.Trace))
			st.orders = append(st.orders, schedule.PurchaseOrder(view.Trace, ordinalsOf(inst, view)))
			st.regimes[schedule.CostRegimeOf(view.Trace)] = true
			if dig := costTableDigest(view.Trace); dig != "" {
				st.tables[dig] = true
			}
			// B1: what this race's OWN `schedule.started` says it was given.
			// A traced race that recorded no budget is an ABSENCE and is not
			// folded into the set as a 0, which `mvo intent new` reads as
			// unbounded — absent source implies absent metric.
			if view.Trace.HasStarted {
				st.budgets[view.Trace.Started.Budget.MaxOracleMS] = true
			}
		}
	}
	// V-6: THE COST TABLE MUST BE BYTE-EQUAL ACROSS ARMS. Decision 2's
	// template exists to make it so by construction; asserting it is what
	// turns "by construction" into a measurement, and a cell whose arms priced
	// differently has every per-arm difference confounded with pricing.
	allTables := map[string]bool{}
	for _, id := range order {
		for dig := range states[id].tables {
			allTables[dig] = true
		}
	}
	if len(allTables) > 1 {
		man.CostTableDrift = append(man.CostTableDrift, fmt.Sprintf(
			"%s: %d distinct recorded cost tables across %d arm(s) x %d replicate(s)",
			inst.ID, len(allTables), len(order), o.replicates))
	}
	// BLOCKER B1: THE ARMS MUST HAVE HELD THE SAME BUDGET, AND THE CHECK IS ON
	// WHAT THEY RECORDED.
	//
	// B is one variable, computed once above from one reference draw, so
	// equality is true by construction — exactly as the cost table's was
	// before V-6 asserted it. Asserting it against each race's own
	// `schedule.started.budget.max_oracle_ms` is what turns "by construction"
	// into a measurement, and it is the only form of the check that survives a
	// future refactor that reintroduces a per-arm derivation.
	if man.BudgetByInstance == nil {
		man.BudgetByInstance = map[string]int64{}
		man.RecordedBudgets = map[string]string{}
	}
	man.BudgetByInstance[inst.ID] = budget
	recorded := map[int64][]string{}
	var perArm []string
	for _, id := range order {
		bs := sortedInt64Keys(states[id].budgets)
		if len(bs) == 0 {
			perArm = append(perArm, id+"=— (no trace)")
			continue
		}
		for _, b := range bs {
			recorded[b] = append(recorded[b], id)
		}
		perArm = append(perArm, fmt.Sprintf("%s=%s", id, joinInt64s(bs)))
	}
	man.RecordedBudgets[inst.ID] = strings.Join(perArm, " ")
	if len(recorded) > 1 {
		var parts []string
		for _, b := range sortedInt64Keys(recorded) {
			arms := append([]string(nil), recorded[b]...)
			sort.Strings(arms)
			parts = append(parts, fmt.Sprintf("B=%dms: %s", b, strings.Join(arms, ", ")))
		}
		man.BudgetMismatch = append(man.BudgetMismatch, fmt.Sprintf(
			"%s: the arms of this cell RECORDED %d different budgets — %s",
			inst.ID, len(recorded), strings.Join(parts, "; ")))
	}
	// DECISION 6: divergence is the PAIRED question and it is kept apart from
	// coverage. `coverage > 0, divergence 0` is a MEASURED NULL and must stay
	// publishable — it is precisely M2b.2 §7.5's pre-registered null, and a
	// refusal that swallowed it would destroy the one finding that block
	// earned.
	if len(order) >= 2 {
		man.Divergence = append(man.Divergence,
			divergenceLine(inst.ID, order[0], order[1], states[order[0]].orders, states[order[1]].orders))
	}
	for _, id := range order {
		st := states[id]
		if len(st.decisions) == 0 {
			continue
		}
		if len(st.coverage) > 0 {
			// BLOCKER B4: THE CELL IS RECORDED BEFORE THE POOL, because the
			// pool is the operation that hides it. `<arm>|<instance>` is one
			// rule, one instance, one budget, R replicates — the scope the
			// ORACLE-BUDGET-MATCHED caption is asserted over, and therefore
			// the scope the refusal has to be taken at.
			man.CoverageByCell[coverageCellKey(id, inst.ID)] = schedule.MergeCoverage(st.coverage)
			man.CoverageByArm[id] = schedule.MergeCoverage(append(
				[]schedule.CoverageReport{man.CoverageByArm[id]}, st.coverage...))
		}
		modal, n, stable := eval.Modal(st.decisions)
		winnerLabel := modalWinner(st.decisions, st.winners, modal)
		admitRan, admitResult := modalAdmit(st.decisions, st.admits, modal)
		rows = append(rows, eval.Row{
			Instance: inst.ID, Arm: id, Family: inst.Family, Tier: hidden.Tier,
			Policy: rowPolicy, Cluster: cluster, CostRegime: oneRegime(st.regimes),
			Selector: st.selector,
			Decision: modal, Stable: stable, Replicates: len(st.decisions), ModalCount: n,
			WinnerLabel:  winnerLabel,
			WinnerSource: modalString(st.decisions, st.sources, modal),
			TableSources: tableSources,
			Avail:        avail, AvailUnknown: availUnknown,
			DStar: dstar.Type, DStarWinnerLabel: dstarLabel,
			MinSpendMS: bound.MinSpendMS, BudgetMS: budget, BoundAvailable: bound.Available,
			AdmitRan: admitRan, AdmitResult: admitResult,
			OracleCostMS: medianOf(st.costs), Footprint: st.arm.Footprint,
			Expected: "",
		})
	}

	// 5. The derived arms, over the reference ledger, each charged its
	// declared footprint.
	for _, a := range eval.Arms() {
		if a.Kind == eval.KindRaced {
			continue
		}
		in := eval.DerivedInput{View: refView, Salt: fmt.Sprint(o.seed), Instance: inst.ID, BudgetMS: budget}
		if a.ID == eval.ArmLabelRetrospective {
			in.Labels = batch.WorldVerdict
			in.ScoringMS = batch.ScoringMS
		}
		out := eval.RunDerived(a, in)
		if !out.Available {
			// An arm with nothing to read is an ARM absence, not an
			// instance skip: the instance is fully scored and filing this in
			// the skip census would make it read as unscorable.
			man.ArmAbsences = append(man.ArmAbsences, inst.ID+"/"+a.ID+": "+out.Absent)
			continue
		}
		label, source := "", ""
		if out.Subject != "" {
			if w, ok := refView.WorldByDigest(out.Subject); ok {
				label = batch.TreeVerdict[w.World.Tree]
				source = winnerSource(inst, w.World.Tree)
			}
		}
		rows = append(rows, eval.Row{
			Instance: inst.ID, Arm: a.ID, Family: inst.Family, Tier: hidden.Tier,
			Policy: rowPolicy, Cluster: cluster,
			Decision: out.Decision, Stable: true, Replicates: 1, ModalCount: 1,
			WinnerLabel: label, WinnerSource: source, TableSources: tableSources,
			Avail: avail, AvailUnknown: availUnknown,
			DStar: dstar.Type, DStarWinnerLabel: dstarLabel,
			MinSpendMS: bound.MinSpendMS, BudgetMS: budget, BoundAvailable: bound.Available,
			// DECISION 12's CHARGE, carried instead of discarded. A9's scoring
			// milliseconds stay in their own field and are never added to the
			// oracle pool.
			OracleCostMS: out.OracleCostMS, ScoringMS: out.ScoringMS, Footprint: out.Footprint,
		})
	}

	// 6. The detectors, over EVERY workspace this instance raced.
	leak, err := o.detectAll(scanned, needles, inst, hidden)
	if err != nil {
		return nil, nc, nil, "", err
	}
	nc.Leak = leak
	nc.WorkspacesScanned = len(scanned)
	if leak.Void() {
		man.Census.Add(inst.ID, eval.SkipLeakDetected, strings.Join(leak.Lines(), "; "))
		man.CanaryVerdict = eval.CanaryLeak
		nc.Prove()
		fmt.Fprintln(os.Stderr, "LEAK DETECTED — instance "+inst.ID+" is VOIDED:")
		for _, l := range leak.Lines() {
			fmt.Fprintln(os.Stderr, "  "+l)
		}
		return nil, nc, nil, "", codedError{code: exitLeak,
			msg: "mvo-eval run: a leak detector fired: the instance is voided and its row is dropped from every metric"}
	}
	if man.CanaryVerdict != eval.CanaryLeak {
		man.CanaryVerdict = eval.CanaryClean
	}
	nc.Prove()

	if unbudgeted && !o.keepUnbudg {
		return nil, nc, nil, "", nil
	}
	return rows, nc, nil, "", nil
}

// detectAll runs the detector suite over every workspace an instance produced.
// The scans are pure over recorded bytes, so covering all of them costs
// milliseconds — and covering only the reference workspace meant the canary
// verdict described 1 of 11 raced workspaces, none of them the ones the
// reported decisions came from.
func (o runOpts) detectAll(workspaces map[string]eval.LedgerView, n eval.Needles,
	inst eval.Instance, hidden eval.HiddenOracle) (eval.Report, error) {

	var rep eval.Report
	paths := make([]string, 0, len(workspaces))
	for p := range workspaces {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		one, err := o.detect(p, n, inst, workspaces[p], hidden)
		if err != nil {
			return rep, err
		}
		rep.Merge(one)
	}
	if rep.Scanned == nil {
		rep.Scanned = map[string]int{}
	}
	rep.Scanned["workspace"] = len(paths)
	return rep, nil
}

// detect runs all five detectors and merges their reports.
//
// BOTH SKIP COUNTERS ARE KEPT. TranscriptDocs returns the CAS keys the store
// could not serve and WalkFiles returns the files it could not read or that
// exceeded MaxScanBytes; the first version of this function discarded both with
// `_`, so a ledger.db above 8 MiB or an unservable blob narrowed D4/D5 while the
// report still said the detectors ran. They now land in Report.Skipped, they
// print beside the scanned counts, and NonConsultation.Prove refuses on them.
func (o runOpts) detect(workspace string, n eval.Needles, inst eval.Instance,
	view eval.LedgerView, hidden eval.HiddenOracle) (eval.Report, error) {

	var rep eval.Report
	rep.Merge(eval.D1Argv(n, view.Receipts))
	_, baseTree, err := gitx.Head(workspace)
	if err != nil {
		return rep, fmt.Errorf("mvo-eval: base tree of %s: %w", workspace, err)
	}
	trees, err := eval.TreeListings(workspace, view, baseTree)
	if err != nil {
		return rep, err
	}
	rep.Merge(eval.D2Trees(n, trees))
	goldRaced := hidden.GoldCandidate != ""
	rep.Merge(eval.D3CAS(n, view.CASKeys, goldRaced))
	docs, casMisses, err := eval.TranscriptDocs(workspace, view)
	if err != nil {
		return rep, err
	}
	rep.NoteSkipped("transcript-doc", casMisses)
	rep.Merge(eval.D4Transcripts(n, docs))
	all, unread, err := eval.WalkFiles(workspace, "workspace.file")
	if err != nil {
		return rep, err
	}
	rep.NoteSkipped("workspace.file", unread)
	all = append(all, docs...)
	rep.Merge(eval.D5Canary(n, all))
	return rep, nil
}

// ---------------------------------------------------------------------------
// M2d.1 helpers: all pure over what was RECORDED.
// ---------------------------------------------------------------------------

// appendWarm keeps one warm report per template key. The cache builds a
// template once; the manifest reports it once.
func appendWarm(xs []eval.WarmReport, w eval.WarmReport) []eval.WarmReport {
	for _, x := range xs {
		if x.Key == w.Key {
			return xs
		}
	}
	return append(xs, w)
}

// ordinalsOf maps this race's world digests to CANDIDATE ORDINALS, which is
// the only stable identity across runs: a world binds created_at, the agent
// RunCost and a transcript digest, so the same patch produces a different
// world digest in every run (M2b.1 F2). A purchase order keyed on digests
// would report "different subject" on every run, including the null case
// where the arms agree perfectly.
func ordinalsOf(inst eval.Instance, v eval.LedgerView) map[string]int {
	out := map[string]int{}
	for _, w := range v.Worlds {
		if c, ok := inst.CandidateByTree(w.World.Tree); ok {
			out[w.Digest] = c.Ord
		}
	}
	return out
}

// costTableDigest digests the RECORDED cost-table snapshot a race allocated
// against — the one on `schedule.started`, not a fit re-derived at read time,
// which would have moved. An untraced race has no snapshot and digests to "",
// which is an absence rather than a table everybody agrees on.
func costTableDigest(tr schedule.Trace) string {
	if !tr.HasStarted || len(tr.Started.CostTable) == 0 {
		return ""
	}
	b, err := schedule.Payload(tr.Started.CostTable)
	if err != nil {
		return ""
	}
	return eval.CASKeyBytes(b)
}

// oneRegime collapses an arm's per-replicate cost regimes into the row's
// label. A set that MIXES regimes is reported as mixed rather than as either
// one: a row that pooled a warm replicate with a cold one is exactly the
// untagged aggregate decision 11 exists to stop.
func oneRegime(seen map[string]bool) string {
	var got []string
	for k := range seen {
		if k != "" && k != schedule.CostRegimeUnknown {
			got = append(got, k)
		}
	}
	sort.Strings(got)
	switch len(got) {
	case 0:
		return ""
	case 1:
		return got[0]
	default:
		return "mixed(" + strings.Join(got, ",") + ")"
	}
}

// divergenceLine is decision 6's paired figure: how many replicates bought a
// DIFFERENT SEQUENCE of (ordinal, oracle). Within a replicate both arms share
// the rotation ρ, so the sequences are comparable.
func divergenceLine(instance, armA, armB string, a, b [][]string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return fmt.Sprintf("%s: %s vs %s — no comparable replicate", instance, armA, armB)
	}
	diff := 0
	for i := 0; i < n; i++ {
		if !schedule.SameOrder(a[i], b[i]) {
			diff++
		}
	}
	return fmt.Sprintf("%s: %s vs %s — %d of %d replicate(s) bought a different (ordinal, oracle) sequence",
		instance, armA, armB, diff, n)
}

// finish computes the metrics, signs the manifest, appends the eval-use line
// and prints. It is the ONE place a report is rendered, so the "no metric line
// when nothing was scored" rule cannot be bypassed by a second printer.
func (o runOpts) finish(man eval.RunManifest, rows []eval.Row, home string) error {
	man.Rows = rows

	// BLOCKER B1's REFUSAL, decided FIRST — before the vacuity refusal and
	// before any metric — because it is prior to both. A cell whose arms held
	// different budgets is not a comparison of two rules at all, whatever its
	// coverage says, and M2d.1's own BUILDLOG entry says what that costs:
	// THIS INVALIDATES EVERY NUMBER.
	//
	// Two sources and both are checked: what the RUNNER handed each arm (read
	// off the rows here) and what each race RECORDED on its own
	// `schedule.started` (collected per instance while racing, and already in
	// man.BudgetMismatch by the time this runs).
	man.BudgetMismatch = append(man.BudgetMismatch, eval.BudgetMismatches(rows)...)
	if len(man.BudgetMismatch) > 0 {
		fmt.Println(joinLines(man.Render()))
		if o.jsonOut != "" {
			if err := writeSignedManifest(home, o.jsonOut, man); err != nil {
				return err
			}
		}
		return codedError{code: exitFailure, msg: "mvo-eval run: ARMS NOT BUDGET-MATCHED — " +
			strings.Join(man.BudgetMismatch, " | ") +
			". Race every allocation rule inside ONE run (--selector=voc,voc2) so they share one " +
			"reference draw, or pin B explicitly with --budget"}
	}
	// DECISION 7's REFUSAL, decided BEFORE any metric is computed.
	//
	// THE RULE UNDER TEST is the ADAPTIVE arm's selector, because that is what
	// a cell's caption is a claim about. If it provably never fired, this is
	// not a comparison of two rules — it is a comparison of one rule against
	// itself, and the honest output is nothing.
	//
	// It is NOT behind --strict. `R < 3` is a THIN measurement and a reader
	// may reasonably want to look at it; a 0 % comparison is NOT A MEASUREMENT
	// OF ANYTHING.
	//
	// B1 makes the adaptive arm one treatment PER RULE, so what gates the run
	// is the coverage of EVERY rule that raced: a comparison in which either
	// rule never fired is a comparison of one rule against itself just as
	// surely as one in which the only rule never fired.
	// BLOCKER B4, AND IT IS DECIDED PER CELL BEFORE ANYTHING IS POOLED.
	//
	// The refusal used to read a numerator merged over every race in the run,
	// and a merged numerator is satisfiable by ONE STEP: a probe merging 99
	// vacuous races with one exercised step printed `1 of 199 steps (0%)` and
	// `vacuous=false`. So the question is asked of each `<arm>|<instance>`
	// cell — the scope a caption is a claim about — and ANY cell whose rule
	// was exercised on no step refuses the whole run. A cell that recorded no
	// computable coverage at all is an ABSENCE and `Vacuous` is false for it
	// by construction: refusing to report a number you could not compute is
	// not the same act as refusing a rule that changed nothing.
	for _, k := range adaptiveCellKeys(sortedStrings(man.CoverageByCell)) {
		c := man.CoverageByCell[k]
		if !c.Vacuous() {
			continue
		}
		man.Vacuous = true
		man.VacuousCells = append(man.VacuousCells, fmt.Sprintf(
			"%s: exercised %d of %d step(s), consulted %d — %s",
			k, c.Exercised, c.Steps, c.Consulted, c.VacuityReason()))
	}
	if keys := adaptiveKeys(sortedStrings(man.CoverageByArm)); len(keys) > 0 {
		reports := make([]schedule.CoverageReport, 0, len(keys))
		for _, k := range keys {
			c := man.CoverageByArm[k]
			reports = append(reports, c)
			// ANY vacuous rule makes the comparison vacuous. Merging first and
			// asking afterwards would let an exercised rule carry an inert one
			// over the threshold, which is B4's shape one arm over.
			//
			// This is the BACKSTOP, not the gate. The gate is the per-cell
			// loop above, and it is strictly stronger: an arm pooled to zero
			// had every one of its cells at zero. `Vacuous` and not
			// `AnyVacuous` deliberately — a single replicate that never
			// reached scarcity is a thin measurement and not a vacuous cell,
			// and a refusal that fired on it would be F-9's rubber stamp
			// pointed the other way. The count is PRINTED as `VACUOUS PARTS`
			// instead, which is B4's "report absence rather than a floored 0 %".
			if c.Vacuous() {
				man.Vacuous = true
			}
		}
		merged := schedule.MergeCoverage(reports)
		// MergeCoverage takes the FIRST report's rule name, which was right
		// while a run held one rule and is a guess now that it can hold two:
		// a block pooling voc and voc2 captioned `the rule under test: voc`
		// would be a caption nobody measured. The per-arm lines above it are
		// the unpooled numbers; this names what the pooled one is over.
		if rules := distinctOf(reports, func(c schedule.CoverageReport) string { return c.Rule }); len(rules) > 1 {
			merged.Rule = strings.Join(rules, "+")
			merged.Baseline = strings.Join(
				distinctOf(reports, func(c schedule.CoverageReport) string { return c.Baseline }), "+")
		}
		man.RuleCoverage = &merged
	}
	if man.Vacuous && !man.AllowVacuous {
		// NO METRIC LINE IS PRINTED AT ALL — M2d decision 1b's own shape: the
		// assertion is on the ABSENCE of a number, which is the only way to
		// test the rule.
		fmt.Println(joinLines(man.Render()))
		if o.jsonOut != "" {
			if err := writeSignedManifest(home, o.jsonOut, man); err != nil {
				return err
			}
		}
		msg := "mvo-eval run: VACUOUS — the rule under test changed nothing it allocated; " +
			"no verdict and no metric line. Warm the workspace (--warmup auto), lower the budget until it " +
			"binds, or pass --allow-vacuous to print the tables stamped VACUOUS"
		if len(man.VacuousCells) > 0 {
			// B4: NAME THE CELLS. "somewhere in this run the rule fired" is
			// what the pooled numerator used to say, and it is what let a
			// verdict be printed at a measured zero.
			msg = fmt.Sprintf("mvo-eval run: VACUOUS in %d cell(s) — %s. %s",
				len(man.VacuousCells), strings.Join(man.VacuousCells, " | "),
				"No verdict and no metric line is printed for ANY cell: a table whose rows were not all "+
					"measured is not a table. Warm the workspace (--warmup auto), lower the budget until "+
					"it binds, or pass --allow-vacuous to print the tables stamped VACUOUS")
		}
		return codedError{code: exitVacuous, msg: msg}
	}

	armIDs := map[string]bool{}
	for _, r := range rows {
		armIDs[r.Arm] = true
	}
	var ids []string
	for id := range armIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	// BOTH FAMILY COLUMNS, ALWAYS, side by side.
	fams := eval.FamilyColumns(rows)
	for _, fam := range sortedStrings(fams) {
		for _, id := range ids {
			m := eval.Compute(id, fams[fam])
			if m.Instances == 0 {
				continue
			}
			m.Arm = id + " [" + fam + "]"
			man.Metrics = append(man.Metrics, m)
		}
	}
	if len(ids) >= 2 {
		a, b := ids[0], ids[1]
		for _, id := range man.Arms {
			if id == eval.ArmAdaptive {
				a = id
			}
			if id == eval.ArmFixedBudget {
				b = id
			}
		}
		// WHEN TWO RULES RACED, THE PAIR IS THE TWO RULES. That is the whole
		// point of racing them in one run: they share the instance, the
		// template, the reference draw and B, so the paired table is a
		// comparison of the RULES and of nothing else. With one rule the pair
		// stays adaptive-against-ladder, exactly as M2d published it.
		if ada := adaptiveKeys(ids); len(ada) >= 2 {
			a, b = ada[0], ada[1]
		}
		p := eval.Pair(a, b, rows)
		man.Paired = append(man.Paired, p)
		man.Tests = append(man.Tests, eval.TestPaired(p, o.floor, o.seed))
	}
	// The global caption is the UNION over every arm's census, not the first
	// arm's on the first family. Built from Metrics[0] it understated the tier
	// and source mix of every other cell in the same report.
	man.Captions = eval.UnionCaptions(rows)

	// The eval-use counter: one line per scoring, published as a number.
	if store, err := eval.OpenStore(home); err == nil && man.BinaryDigest != "" {
		_ = store.AppendEvalRun(man.Corpus, eval.EvalRun{
			BinaryDigest: man.BinaryDigest, PolicyDigest: man.PolicyDigest,
			Arms: man.Arms, InstanceCount: len(uniqueInstances(rows)),
			Split: man.Split, Unfreeze: man.Unfreeze,
		})
		if runs, err := store.ReadEvalRuns(man.Corpus); err == nil {
			man.EvalRunCount = len(runs)
		}
	}

	fmt.Println(joinLines(man.Render()))
	if !man.Census.Empty() {
		fmt.Printf("eval-use count for %s: %d scoring(s)\n", man.Corpus, man.EvalRunCount)
	}
	if o.jsonOut != "" {
		if err := writeSignedManifest(home, o.jsonOut, man); err != nil {
			return err
		}
	}
	if man.Census.Fatal() {
		return codedError{code: exitLeak, msg: "mvo-eval run: the census contains a fatal reason"}
	}
	if len(rows) == 0 && o.strict {
		return codedError{code: exitNoMetric, msg: "mvo-eval run: nothing was scored and --strict was passed"}
	}
	return nil
}

func writeSignedManifest(home, path string, man eval.RunManifest) error {
	signer, err := eval.LoadOrCreateEvalKey(home)
	if err != nil {
		return err
	}
	_, env, err := eval.SignRunManifest(signer, man)
	if err != nil {
		return err
	}
	out := struct {
		Manifest eval.RunManifest `json:"manifest"`
		Envelope any              `json:"envelope"`
		KeyID    string           `json:"eval_key_id"`
		KeyDir   string           `json:"eval_key_dir"`
	}{man, env, signer.KeyID, eval.EvalKeysDir(home)}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("mvo-eval: encode run manifest: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("mvo-eval: write %s: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (o runOpts) instanceIDs(store *eval.Store) []string {
	if o.instances != "" {
		return splitList(o.instances)
	}
	if store != nil {
		if ids, err := store.ListInstances(o.common.corpus, o.common.version); err == nil && len(ids) > 0 {
			return ids
		}
	}
	if o.common.corpus == eval.CorpusLocalDerived {
		return eval.LocalInstanceIDs()
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// budgetLevel derives B1/B2/B3 from the reference run's own bound (M2b.1 §4):
//
//	B1 = ceil(minspend x 1.1) — the tightest budget where winning is possible
//	B2 = (B1 + S) / 2         — the middle of the band
//	B3 = S                    — the null case
func budgetLevel(level string, b schedule.BoundReport) int64 {
	if !b.Available || b.TotalMS <= 0 {
		return 0
	}
	b1 := (b.MinSpendMS*11 + 9) / 10
	switch strings.ToUpper(level) {
	case "B1":
		return b1
	case "B3":
		return b.TotalMS
	default:
		return (b1 + b.TotalMS) / 2
	}
}

func coverage(b eval.Batch) (avail bool, availUnknown bool) {
	for _, l := range b.Labels {
		switch l.Verdict {
		case eval.VerdictCorrect:
			avail = true
		case eval.VerdictUnknown:
			availUnknown = true
		}
	}
	if avail {
		availUnknown = false
	}
	return avail, availUnknown
}

// selectorFlags is `--selector` applied to the ADAPTIVE ARM ALONE. The ladder
// is depth-first and reserves nothing and the reference is the ladder handed an
// unbounded budget, so naming a rule for either would be a flag `mvo race`
// refuses — and refusing loudly there is what keeps a mis-flagged cell from
// being raced and published.
func selectorFlags(selector, armID string) []string {
	if selector == "" || armID != eval.ArmAdaptive {
		return nil
	}
	return []string{"--selector=" + selector}
}

// racedArm is one TREATMENT of one raced arm: the arm and the allocation rule
// it races under. Two rules are two treatments of the adaptive arm, and they
// belong to ONE run for blocker B1's reason.
type racedArm struct {
	key      string // the arm id every row, coverage report and caption uses
	arm      eval.Arm
	selector string
}

// armPlan expands the --arms list against the --selector list.
//
// BLOCKER B1, AND THIS FUNCTION IS THE WHOLE OF IT. `--selector` used to be a
// per-RUN choice, so comparing two allocation rules meant two runs; B is
// derived inside a run from that run's own reference races, so the two runs
// took two draws and the two rules were handed different budgets under a cell
// captioned ORACLE-BUDGET-MATCHED (measured: minspend 1553 against 1013, same
// instance, same host, same day). Naming both rules races both inside ONE run,
// against ONE warmed template, ONE reference draw and therefore ONE B.
//
// With zero or one rule the plan is EXACTLY what it was: the same arm ids, so
// every published M2d number keeps its arm names and its cell keys.
func armPlan(armIDs []string, selectors []string) []racedArm {
	rules := selectors
	if len(rules) == 0 {
		rules = []string{""}
	}
	var out []racedArm
	for _, id := range armIDs {
		a, ok := eval.ArmByID(id)
		if !ok || a.Kind != eval.KindRaced {
			continue
		}
		// The rule is a flag on the adaptive arm and on no other (M2b.2
		// decision 6), so every other arm is one treatment however many rules
		// were named — and it is raced ONCE, shared by both rules' cells,
		// because racing the ladder twice would buy nothing and would put two
		// identical rows in one cell.
		if id != eval.ArmAdaptive {
			out = append(out, racedArm{key: id, arm: a})
			continue
		}
		if len(rules) == 1 {
			out = append(out, racedArm{key: id, arm: a, selector: rules[0]})
			continue
		}
		for _, r := range rules {
			out = append(out, racedArm{key: id + "@" + r, arm: a, selector: r})
		}
	}
	return out
}

// armKeys is the plan's arm ids, in plan order.
func armKeys(plan []racedArm) []string {
	out := make([]string, 0, len(plan))
	for _, p := range plan {
		out = append(out, p.key)
	}
	return out
}

// adaptiveKeys are the arm ids in `ids` that are the ADAPTIVE ARM under some
// rule — `A2-adaptive` itself, or `A2-adaptive@<rule>` when more than one rule
// raced. It is what keeps the vacuity refusal and the pairing pointed at the
// rule under test after B1 split the arm into one treatment per rule.
func adaptiveKeys(ids []string) []string {
	var out []string
	for _, id := range ids {
		if isAdaptiveArm(id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func isAdaptiveArm(id string) bool {
	return id == eval.ArmAdaptive || strings.HasPrefix(id, eval.ArmAdaptive+"@")
}

// coverageCellSep separates the arm from the instance in a cell key. It is a
// character no arm id and no instance id contains — arm ids are
// `adaptive@<rule>` and instance ids are `<fixture>-<stat>-<letter>`.
const coverageCellSep = "|"

// coverageCellKey names BLOCKER B4's unit of refusal: one allocation rule on
// one instance, over that cell's replicates.
func coverageCellKey(arm, instance string) string { return arm + coverageCellSep + instance }

// adaptiveCellKeys keeps the cells belonging to an ADAPTIVE arm, which is the
// only arm a rule-coverage claim is about. A cell key whose arm half is not
// recognised is DROPPED rather than guessed at: the gate may not be applied to
// an arm nobody declared an inertness predicate for.
func adaptiveCellKeys(keys []string) []string {
	var out []string
	for _, k := range keys {
		if arm, _, ok := strings.Cut(k, coverageCellSep); ok && isAdaptiveArm(arm) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// distinctOf is the set of values a field takes across reports, in first-seen
// order, empties dropped.
func distinctOf[T any](xs []T, get func(T) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		v := get(x)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// sortedInt64Keys renders a set of budgets in a fixed order, so a refusal is a
// function of what was recorded and never of map iteration.
func sortedInt64Keys[V any](m map[int64]V) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func joinInt64s(xs []int64) string {
	parts := make([]string, 0, len(xs))
	for _, x := range xs {
		parts = append(parts, fmt.Sprintf("%dms", x))
	}
	return strings.Join(parts, "/")
}

// modalWinner returns the winner label of the replicates that produced the
// modal decision. Replicates that decided otherwise are not consulted: their
// winner is a different decision's winner.
func modalWinner(decisions, winners []string, modal string) string {
	counts := map[string]int{}
	for i, d := range decisions {
		if d != modal || i >= len(winners) {
			continue
		}
		counts[winners[i]]++
	}
	best, bestN := "", -1
	for _, k := range sortedStringsOf(counts) {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best
}

// winnerSource resolves the winning candidate's source tag BY IDENTITY: the
// winning world's own result tree, through Instance.CandidateByTree, which is
// the documented join key and the same one the labels use.
//
// The version this replaces scanned the label list for the first label whose
// VERDICT equalled the winner's and returned that label's source. Labels are
// sorted by candidate id, so on any instance with two same-verdict candidates it
// named the alphabetically-first one. On advrepo-split-B the fixed arm actually
// selected v10-padded_deletion (S3-adversarial) and the manifest recorded
// S2-derived; the headline sentence "the selected candidate was a derived
// mutant" was therefore not supported by any recorded field, and
// +ADVERSARIAL(declared) never fired on any published number.
func winnerSource(inst eval.Instance, tree string) string {
	if tree == "" {
		return ""
	}
	if c, ok := inst.CandidateByTree(tree); ok {
		return c.Source
	}
	return ""
}

// sourcesOnTable is every source tag in the instance's candidate set, sorted
// and deduplicated. The caption rule is about the POPULATION a number describes,
// so it reads this rather than the winners.
func sourcesOnTable(inst eval.Instance) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range inst.Candidates {
		if c.Source == "" || seen[c.Source] {
			continue
		}
		seen[c.Source] = true
		out = append(out, c.Source)
	}
	sort.Strings(out)
	return out
}

// oracleSpend is what a raced arm was charged on the oracle axis: the recorded
// wall_ms of every receipt its own replicate bought. It is the raced arms' entry
// on the same cost/accuracy axis the derived arms' declared footprints put them
// on (decision 12).
func oracleSpend(v eval.LedgerView) int64 {
	var ms int64
	for _, r := range v.Receipts {
		ms += r.Receipt.Cost.WallMS
	}
	return ms
}

// medianOf is the median of a small sample. A median rather than a mean because
// a single long replicate must not become the treatment level.
func medianOf(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// modalString is modalWinner for any per-replicate string.
func modalString(decisions, values []string, modal string) string {
	return modalWinner(decisions, values, modal)
}

// modalAdmit reports whether `mvo admit` ran in the modal replicates and what it
// said. Absent is absent: where admit never ran the TCAR_adm/FAR_adm columns do
// not appear at all, which is not the same claim as "admit agreed".
func modalAdmit(decisions, results []string, modal string) (bool, string) {
	got := modalWinner(decisions, results, modal)
	switch got {
	case eval.AdmitConfirmed, eval.AdmitRejected:
		return true, got
	case "":
		return false, ""
	default:
		// The ledger recorded a result string outside the two-word vocabulary.
		// Treating it as "confirmed" would invent an agreement, so it is
		// recorded as a divergence.
		return true, eval.AdmitRejected
	}
}

func sortedStrings[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStringsOf(m map[string]int) []string { return sortedStrings(m) }

// policyName is the name `mvo policy use` takes: the filename stem, which the
// document's own `name` field must equal. The empty path is the workspace
// default `mvo init` writes, and it is named as such rather than rendered as
// an empty string that reads like a missing value.
func policyName(path string) string {
	if path == "" {
		return "default"
	}
	return strings.TrimSuffix(filepath.Base(path), ".json")
}

func uniqueInstances(rows []eval.Row) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if !seen[r.Instance] {
			seen[r.Instance] = true
			out = append(out, r.Instance)
		}
	}
	sort.Strings(out)
	return out
}

func appendUnique(xs []string, x string) []string {
	for _, s := range xs {
		if s == x {
			return xs
		}
	}
	return append(xs, x)
}

func modeAtMost(path string, want os.FileMode) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return st.Mode().Perm()&^want == 0
}

func fileDigest(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return eval.CASKeyBytes(b)
}

func defaultPolicyDigest() string {
	dig, _, err := object.Digest(policy.Default())
	if err != nil {
		return ""
	}
	return dig
}

// checkFreeze is decision 6's refusal, shared by every verb that can open the
// hidden oracle.
//
// It is a shared helper because it was not one: the freeze check and the
// eval-use counter lived only in `run`, and `score` — the verb that actually
// calls store.LoadHidden, runs the suite and writes labels — had no --split
// flag at all, performed no freeze check and appended no run line. An
// eval-split instance could therefore be scored repeatedly under a moved policy
// digest with no freeze mismatch, no --unfreeze reason and no run-count
// inflation, which is exactly the accidental version the mechanism exists to
// stop. The committed freeze file's own notes promise `mvo-eval score --split
// eval` refuses; now it does.
func checkFreeze(repoRoot, corpus, version, split, unfreeze, verb string,
	instrument *eval.Instrument) (string, []eval.FreezeDrift, error) {
	path := filepath.Join(repoRoot, "eval", "freeze", fmt.Sprintf("%s-%s.json", corpus, version))
	fz, err := eval.LoadFreeze(path)
	if err != nil {
		// No freeze file for this corpus is not drift: it is nothing to check
		// against, and inventing a refusal would train a reader to pass
		// --unfreeze reflexively.
		return "", nil, nil
	}
	b, _ := os.ReadFile(path)
	// The freeze pins the SHIPPED DEFAULT, so it is checked against the shipped
	// default and not against a cell's own --policy: racing a cell under a named
	// alternative policy is a declared experiment, not quiet tuning of the
	// default.
	drift := fz.CheckFreeze(defaultPolicyDigest(), eval.SchedulerConstants(), eval.SchedulerRules(), nil)
	// M2d.1 decision 12: the freeze also pins WHAT THE INSTRUMENT COULD
	// AFFORD, and a moved regime reads `instrument.cost_regime: "cold" ->
	// "warm"`. Verbs that take no measurement pass nil and are unaffected.
	if instrument != nil {
		drift = append(drift, fz.CheckInstrument(*instrument)...)
	}
	if len(drift) > 0 && split == eval.SplitEval && unfreeze == "" {
		var what []string
		for _, d := range drift {
			what = append(what, fmt.Sprintf("%s (frozen %s, now %s)", d.What, d.Frozen, d.Now))
		}
		return eval.CASKeyBytes(b), drift, codedError{code: exitFailure,
			msg: "mvo-eval " + verb + ": refusing to score the eval split: " +
				strings.Join(what, "; ") + " moved since the freeze. Pass --unfreeze \"<reason>\" to proceed; " +
				"the reason, the diff and the timestamp are appended to the run log"}
	}
	return eval.CASKeyBytes(b), drift, nil
}

// warmRegimeOf is the regime a run INTENDS, from its flag alone. A run that
// asks for no warm-up is cold by construction; a run that asks for one is warm
// unless its races say otherwise, and the manifest's own cost_regime column is
// derived from the recorded tables rather than from this.
func warmRegimeOf(auto bool, races int) string {
	if !auto && races == 0 {
		return schedule.CostRegimeCold
	}
	return schedule.CostRegimeWarm
}

// splitOf reports which half of the recorded split an instance is assigned to.
func splitOf(repoRoot, corpus, version, id string) (string, bool) {
	sf, ok := loadSplitFile(repoRoot, corpus, version)
	if !ok {
		return "", false
	}
	return sf.Of(id)
}

func loadSplitFile(root, corpus, version string) (eval.SplitFile, bool) {
	p := filepath.Join(root, "eval", "splits", fmt.Sprintf("%s-%s.json", corpus, version))
	s, err := eval.LoadSplit(p)
	if err != nil {
		return eval.SplitFile{}, false
	}
	return s, true
}

func sanitize(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	return sb.String()
}
