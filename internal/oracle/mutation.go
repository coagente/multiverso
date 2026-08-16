package oracle

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// SchemaMutationReport is the mutation rung's report artifact: the target
// set, the selected mutants, and every survivor with its diff — the
// actionable half, and the only reason a mutation number is worth a
// maintainer's time.
const SchemaMutationReport = "multiverso.dev/mutation-report/v0"

// artMutationReport names the report artifact kind.
const artMutationReport = "mutation-report"

// Mutant outcomes. Timeouts and unviable mutants are EXCLUDED from
// mutation_score_bp's denominator and counted separately, because "the
// mutant hung" and "the mutant did not import" are not "the tests caught
// it".
const (
	MutantKilled   = "killed"
	MutantSurvived = "survived"
	MutantTimeout  = "timeout"
	MutantUnviable = "unviable"
)

// Mutant selection provenance, recorded in inputs["mutant_selection"].
// Under cosmic-ray the control plane enumerates, orders and caps; under
// mutmut the tool chooses and the receipt says so — an honest label on
// strictly weaker provenance rather than a silent equivalence (M2a
// decision 19).
const (
	SelectionControlPlane = "control-plane"
	SelectionTool         = "tool"
)

// Tool names as result.tools reports them (and as the probe spells the
// distributions).
const (
	ToolCosmicRay  = policy.MutationToolCosmicRay
	ToolMutmut     = policy.MutationToolMutmut
	ToolHypothesis = "hypothesis"
)

// Mutant is one enumerated mutation of one target line.
//
// Ref is the TOOL's own handle (a cosmic-ray occurrence, a mutmut id) and
// is opaque to everything but its adapter. Digest is the content address of
// the mutant's canonical description and is the last component of the
// canonical order — so two tools that enumerate the same mutation in a
// different order still select the same set.
type Mutant struct {
	Path     string `json:"path"`
	Line     int64  `json:"line"`
	Col      int64  `json:"col"`
	Operator string `json:"operator"`
	Ref      string `json:"ref"`
	Digest   string `json:"digest"`
}

// canonical is the digest pre-image: what the mutant IS, with no tool
// bookkeeping in it.
func (m Mutant) canonical() map[string]any {
	return map[string]any{
		"col":      m.Col,
		"line":     m.Line,
		"operator": m.Operator,
		"path":     m.Path,
		"ref":      m.Ref,
	}
}

// WithDigest returns the mutant with its content address filled in.
func (m Mutant) WithDigest() Mutant {
	b, err := object.Canonical(m.canonical())
	if err != nil {
		return m
	}
	m.Digest = object.CASKeyBytes(b)
	return m
}

// MutantRecord is one selected mutant plus what happened to it. Diff is the
// tool's own rendering of the mutation, stored for survivors so a
// maintainer can act on the report without re-running anything.
type MutantRecord struct {
	Mutant
	Outcome string `json:"outcome"`
	Diff    string `json:"diff,omitempty"`
}

// MutationReport is the control-plane-authored report artifact.
type MutationReport struct {
	Schema string `json:"schema"`
	Tool   string `json:"tool"`
	// Selection is SelectionControlPlane or SelectionTool — the same
	// string the receipt carries in inputs["mutant_selection"].
	Selection string `json:"selection"`
	// MaxPerLineApplied is false under tool selection: mutmut reports ids
	// without positions, so the per-line cap cannot be applied to its
	// enumeration and saying otherwise would be a claim about a filter
	// that never ran.
	MaxPerLineApplied  bool           `json:"max_per_line_applied"`
	Budget             int64          `json:"budget"`
	MaxPerLine         int64          `json:"max_per_line"`
	PerMutantTimeoutMS int64          `json:"per_mutant_timeout_ms"`
	BaselineWallMS     int64          `json:"baseline_wall_ms"`
	Target             TargetSet      `json:"target"`
	Candidates         int64          `json:"candidates"`
	Truncated          int64          `json:"truncated"`
	Selected           []MutantRecord `json:"selected"`
	Survivors          []MutantRecord `json:"survivors"`
	// Timeouts are the mutants that hung. They are listed BESIDE the
	// survivors, with their diffs, because they are the same actionable
	// fact wearing a different exit path: the suite did not discriminate
	// against them. A report printing `survivors: 0` next to a mutant
	// nothing killed is the actionable half of the artifact quietly
	// refusing to be actionable.
	Timeouts []MutantRecord `json:"timeouts"`
}

// MutationRun is everything an adapter needs to build its argv: the
// resolved budget, the control-plane scratch it must keep its state in, and
// the in-world suite command each mutant is tested with.
//
// SessionDir is NEVER inside the worktree (M2a decision 18). mutmut's cache
// and cosmic-ray's session database are EVIDENCE STORES; in the candidate's
// tree a planted one asserting "all mutants killed" is a two-line forgery
// (corpus vector 15). Every run gets a fresh session in /mvo/scratch and
// incremental mode is off — there is no field here to turn it on, and there
// will not be one.
type MutationRun struct {
	Spec       object.MutationSpec // RESOLVED: MaxMutants is a ceiling, not a sentinel
	SessionDir string              // in-world control-plane scratch
	ConfigPath string              // in-world path of the control-plane-written tool config
	PatchPath  string              // in-world path of the captured patch
	TestArgv   []string            // the in-world suite command, evidence plugin included
	Paths      []string            // the target paths, sorted
	TimeoutSec int64               // per-mutant timeout, in whole seconds, for tool configs
}

// MutationStep is one in-world command an adapter asks the oracle to run.
// Parse marks the step whose stdout carries the enumeration.
type MutationStep struct {
	Argv  []string
	Parse bool
}

// ToolClaim is what a tool says about one mutant's run. It is a
// CROSS-CHECK and a source of the report's diff — never the metric. The
// classification comes from the evidence stream the control plane read
// live, because a tool's summary is a file the run wrote after the fact.
type ToolClaim struct {
	Outcome string // the tool's own word, unmapped; "" when it said nothing
	Diff    string
}

// MutationTool is the adapter surface. Two implementations ship:
// cosmic-ray (control-plane selection, the v0 default) and mutmut (tool
// selection, labelled as such).
type MutationTool interface {
	// Name is the tool's distribution name, as the probe reports it.
	Name() string
	// Selection is SelectionControlPlane or SelectionTool.
	Selection() string
	// EnumerateSteps lists the mutants the tool would generate over the
	// target set. The steps run in order; the Parse step's stdout goes to
	// ParseEnumeration.
	EnumerateSteps(python string, t TargetSet, run MutationRun) []MutationStep
	// ParseEnumeration is PURE: recorded tool output in, mutants out. It
	// is the half of every adapter that is tested without the tool.
	ParseEnumeration(stdout []byte) ([]Mutant, error)
	// ExecArgv tests exactly one selected mutant.
	ExecArgv(python string, m Mutant, run MutationRun) []string
	// ParseExec reads the tool's claim about one mutant's run.
	ParseExec(stdout []byte) (ToolClaim, error)
	// Config returns the bytes of the tool configuration the control plane
	// must write into scratch, or nil when the tool needs none.
	Config(t TargetSet, run MutationRun) []byte
}

// SelectMutants applies M2a decision 17's selection, in order: the
// per-line cap first, then the canonical order (path, line, col, operator,
// mutant_digest), then the first max_mutants.
//
// It is PURE and DETERMINISTIC, which is the whole argument for why
// count-truncation is admissible partial evidence: a run stopped at
// max_mutants tested a control-plane-selected PREFIX of the mutant
// population, so it is replayable and its denominator is recorded. A run
// stopped by a wall clock tested whatever the machine got through, which is
// neither — and that is why the two truncations end differently.
//
// maxPerLine <= 0 leaves the per-line population alone; maxMutants <= 0
// selects nothing at all, which no resolved spec produces (the wire default
// is 20) and which is refused earlier as a negative budget.
func SelectMutants(ms []Mutant, maxPerLine, maxMutants int64) []Mutant {
	ordered := append([]Mutant(nil), ms...)
	for i, m := range ordered {
		if m.Digest == "" {
			ordered[i] = m.WithDigest()
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return mutantLess(ordered[i], ordered[j]) })
	out := make([]Mutant, 0, len(ordered))
	perLine := map[string]int64{}
	for _, m := range ordered {
		if maxPerLine > 0 {
			key := fmt.Sprintf("%s:%d", m.Path, m.Line)
			if perLine[key] >= maxPerLine {
				continue
			}
			perLine[key]++
		}
		out = append(out, m)
		if maxMutants > 0 && int64(len(out)) >= maxMutants {
			break
		}
	}
	return out
}

// mutantLess is the canonical order, in full.
func mutantLess(a, b Mutant) bool {
	switch {
	case a.Path != b.Path:
		return a.Path < b.Path
	case a.Line != b.Line:
		return a.Line < b.Line
	case a.Col != b.Col:
		return a.Col < b.Col
	case a.Operator != b.Operator:
		return a.Operator < b.Operator
	default:
		return a.Digest < b.Digest
	}
}

// FilterToTarget drops mutants outside the diff-scoped target set. A tool
// asked to mutate a whole file may offer mutants of lines the candidate
// never touched; those are not what this rung measures, and counting them
// would put the candidate's neighbours' code in its denominator.
func FilterToTarget(ms []Mutant, t TargetSet) []Mutant {
	out := make([]Mutant, 0, len(ms))
	for _, m := range ms {
		// A mutant with no position (tool selection) cannot be filtered
		// here; the tool was given the patch and is trusted to have
		// scoped it, which is exactly the weaker provenance the receipt
		// labels.
		if m.Path == "" || m.Line == 0 || t.Has(m.Path, m.Line) {
			out = append(out, m)
		}
	}
	return out
}

// mutationOracle is O3: diff-scoped mutation with a pinned ceiling.
//
// The sequence is Google's recipe with its constraints intact — target set,
// baseline, enumerate, select, execute — and every piece of state lives in
// control-plane scratch. What the candidate cannot do here is choose which
// mutants run (under cosmic-ray), plant a cache the run will read, or make
// the target set exclude a line it wrote.
type mutationOracle struct {
	spec  policy.Oracle
	store artifactStore
	// patch is the AG-4 captured patch. A nil patch is MACHINERY FAILURE,
	// not an empty target set: "the candidate changed no mutable line" and
	// "nobody gave us the diff" must never render the same, because the
	// first passes the survivor gate vacuously.
	patch   []byte
	paths   policy.PathSet
	timeout time.Duration
	cap     int64
	ev      evidencePlan
	// tool overrides adapter resolution. Production leaves it nil and
	// resolves against the probe; tests inject a fake, because the two
	// real tools are not installed here and no test may require one.
	tool MutationTool
}

// ID implements Oracle.
func (o *mutationOracle) ID() string { return KindMutationDiff }

// Version implements Oracle.
func (o *mutationOracle) Version() string { return oracleVersion }

func (o *mutationOracle) prefix() []string {
	if len(o.spec.Argv) > 0 {
		return append([]string(nil), o.spec.Argv...)
	}
	return policy.DefaultPytestPrefix()
}

func (o *mutationOracle) python() string {
	if p := o.prefix(); len(p) > 0 && p[0] != "" {
		return p[0]
	}
	return policy.DefaultPytestPrefix()[0]
}

// suiteArgv is the command the baseline and every mutant are judged by: the
// repository's suite with the control-plane observer loaded FIRST by argv,
// so each mutant's outcome arrives on a stream the control plane read live
// rather than in a report the mutated run wrote afterwards.
func (o *mutationOracle) suiteArgv() []string {
	argv := append(o.prefix(), o.pluginFlags()...)
	argv = append(argv, "-q", "-p", "no:cacheprovider")
	return append(argv, o.spec.Args...)
}

func (o *mutationOracle) pluginFlags() []string {
	if !o.ev.streaming() || o.ev.pluginDigest == "" {
		return nil
	}
	return []string{"-p", "mvo_evidence"}
}

// inWorldScratch is where the tool's session, config and the captured patch
// live: control-plane scratch, never the worktree.
func (o *mutationOracle) inWorldScratch() string {
	if o.ev.inWorldScrap != "" {
		return o.ev.inWorldScrap
	}
	return filepath.ToSlash(o.ev.hostScratch)
}

func (o *mutationOracle) inWorldStream() string {
	if o.ev.inWorldEvidence != "" {
		return path.Join(o.ev.inWorldEvidence, streamFile)
	}
	return filepath.ToSlash(filepath.Join(o.ev.hostEvidence, streamFile))
}

// Run implements Oracle.
//
// A failing gate is evidence and comes back in the receipt with err nil. A
// non-nil error means the EVIDENCE could not be produced (a CAS write
// failed, no patch was supplied), so the caller records nothing rather than
// a receipt whose artifacts are not all in the store.
func (o *mutationOracle) Run(ctx context.Context, w backend.World) (object.Receipt, error) {
	switch {
	case o.store == nil:
		return object.Receipt{}, fmt.Errorf("oracle: %s: nil CAS store", KindMutationDiff)
	case w == nil:
		return object.Receipt{}, fmt.Errorf("oracle: %s: nil world", KindMutationDiff)
	case o.spec.Config == "":
		return object.Receipt{}, fmt.Errorf("oracle: %s: spec carries no resolved config digest", KindMutationDiff)
	case o.patch == nil:
		return object.Receipt{}, fmt.Errorf("oracle: %s: no captured patch supplied: the mutation target set is derived from the AG-4 patch, and an unsupplied patch must not read as an empty one", KindMutationDiff)
	}
	start := time.Now()
	runCtx, cancel := ctx, func() {}
	if o.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, o.timeout)
	}
	defer cancel()

	target := DiffTargets(o.patch, o.paths)
	targetDig, targetBytes, err := target.Digest()
	if err != nil {
		return object.Receipt{}, err
	}
	if _, err := o.store.Put(targetBytes); err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: %s: store diff target: %w", KindMutationDiff, err)
	}

	res := &mutationOutcome{
		metrics: map[string]int64{},
		tools:   map[string]string{},
		inputs: map[string]string{
			object.InputDiffTarget: targetDig,
		},
		report: MutationReport{
			Schema:     SchemaMutationReport,
			Budget:     o.spec.Mutation.MaxMutants,
			MaxPerLine: o.spec.Mutation.MaxPerLine,
			Target:     target,
			Selected:   []MutantRecord{},
			Survivors:  []MutantRecord{},
			Timeouts:   []MutantRecord{},
		},
	}
	// Both of these are CONTROL-PLANE facts — one from the captured patch,
	// one from the pinned policy — so they are recorded whatever happens
	// to the toolchain afterwards.
	res.metrics[policy.MetricMutationLinesTargeted] = target.Lines
	res.metrics[policy.MetricMutantsBudget] = o.spec.Mutation.MaxMutants

	if err := o.execute(runCtx, w, target, res); err != nil {
		return object.Receipt{}, err
	}

	artifacts, err := o.storeArtifacts(res)
	if err != nil {
		return object.Receipt{}, err
	}
	corr := policy.KindCorrelation(KindMutationDiff)
	return object.Receipt{
		Schema: object.SchemaReceipt,
		Oracle: object.OracleRef{ID: KindMutationDiff, Version: o.Version(), Config: o.spec.Config},
		Execution: object.Execution{
			Argv:           res.argv,
			ExitCode:       res.exitCode,
			DurationMS:     res.durationMS,
			IsolationTier:  w.Tier(),
			IsolationCaps:  w.Caps(),
			EvidenceRegime: o.ev.regime,
			EvidencePlugin: res.plugin,
		},
		Result: object.Result{
			Status:    res.status,
			Metrics:   res.metrics,
			Tools:     res.tools,
			Detail:    "",
			Artifacts: artifacts,
		},
		// Count-truncation carries the SAME basis as an untruncated run
		// (decision 17): the mutants it did test were tested against the
		// exact tree, deterministically selected and replayable. What
		// makes it partial is recorded in the metrics, not smuggled into
		// a weaker freshness word.
		Freshness:   object.Freshness{Basis: object.BasisConstruction},
		RecheckTier: recheckTier,
		Family:      policy.FamilyMutation,
		// `mutants_tested` is DELETED from the metrics on every machinery
		// path — a missing toolchain, a red baseline, a clock-truncated run —
		// so reading the map directly yielded Units=0 beside Unit="mutants",
		// which is the one shape object.Cost documents as impossible. A
		// zero-unit sample also enters M2b's least-squares fit at x = 0,
		// where the intercept it corrupts is the kind's fixed cost, and an
		// error-status receipt's wall time is a full baseline suite run.
		Cost: sizedCost(time.Since(start).Milliseconds(),
			res.metrics[policy.MetricMutantsTested], policy.UnitMutants),
		Inputs:      res.inputs,
		Correlation: corr,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// mutationOutcome is one run's accumulated state before the envelope.
type mutationOutcome struct {
	argv       []string
	exitCode   int
	durationMS int64
	status     string
	plugin     string
	metrics    map[string]int64
	tools      map[string]string
	inputs     map[string]string
	notes      string
	probeKey   string
	report     MutationReport
}

// fail records a machinery-shaped refusal: status error, EVERY derived
// metric dropped, and the reason preserved for the stderr artifact.
//
// The control-plane facts (lines targeted, the pinned budget) survive,
// because they were true before the toolchain was consulted and dropping
// them would tell the reader less than we know. Everything a run would have
// produced is absent — never a zero, which the survivor gate would read as
// a pass.
func (r *mutationOutcome) fail(format string, a ...any) {
	r.status = StatusError
	for _, m := range []string{
		policy.MetricMutantsCandidates, policy.MetricMutantsTested,
		policy.MetricMutantsKilled, policy.MetricMutantsSurvived,
		policy.MetricMutantsTimeout, policy.MetricMutantsUnviable,
		policy.MetricMutationScoreBP,
	} {
		delete(r.metrics, m)
	}
	r.notes += "mvo: oracle: " + fmt.Sprintf(format, a...) + "\n"
}

// execute is the O3 sequence: probe, empty-target short circuit, baseline,
// enumerate, select, run.
func (o *mutationOracle) execute(ctx context.Context, w backend.World, target TargetSet, res *mutationOutcome) error {
	res.status = StatusPass

	// Step 1 — an empty target set is not an error. A patch that changed
	// no mutable source line has nothing for mutation to say about it, and
	// the gate passes VACUOUSLY with the score ABSENT: pretending
	// otherwise would be the fabricated-zero failure. This is also the
	// honest correction to M1f's table — a patch whose entire content is a
	// test-file edit has an empty target set, so mutation says nothing
	// about assertion weakening; the differential is what addresses that.
	if target.Empty() {
		for _, m := range []string{
			policy.MetricMutantsCandidates, policy.MetricMutantsTested,
			policy.MetricMutantsKilled, policy.MetricMutantsSurvived,
			policy.MetricMutantsTimeout, policy.MetricMutantsUnviable,
		} {
			res.metrics[m] = 0
		}
		res.inputs[object.InputMutantSelection] = SelectionControlPlane
		res.report.Selection = SelectionControlPlane
		res.report.MaxPerLineApplied = true
		res.notes += "mvo: oracle: the captured patch touches no mutable source line; mutants_candidates=0 and mutation_score_bp is absent\n"
		return nil
	}

	if o.ev.hostScratch != "" {
		if err := ensureDir(o.ev.hostScratch, 0o777); err != nil {
			return fmt.Errorf("oracle: %s: scratch dir: %w", KindMutationDiff, err)
		}
	}

	// Step 2 — the toolchain. A missing tool is MACHINERY, never a failing
	// candidate (M1e decision 15, extended by M2a decision 20): a race
	// whose policy declares this rung is refused at pre-flight, and an
	// oracle run anyway degrades to absent metrics rather than to a
	// fabricated score. `mutants_survived` absent fails the survivor gate
	// with "source unavailable", which is the honest verdict.
	tools := map[string]string{}
	if o.tool == nil {
		var probeBytes []byte
		tools, probeBytes = Probe(ctx, w, o.python())
		// EP-7: the probe's bytes reach CAS before anything reads them,
		// and they stay in the receipt whatever the probe said — "the
		// toolchain was absent" is a claim a reader must be able to check.
		key, err := o.store.Put(probeBytes)
		if err != nil {
			return fmt.Errorf("oracle: %s: store %s: %w", KindMutationDiff, artProbe, err)
		}
		res.probeKey = key
		tool, err := resolveMutationTool(o.spec.Mutation.Tool, tools)
		if err != nil {
			res.fail("%v", err)
			return nil
		}
		o.tool = tool
	}
	res.report.Tool = o.tool.Name()
	res.report.Selection = o.tool.Selection()
	res.report.MaxPerLineApplied = o.tool.Selection() == SelectionControlPlane
	res.inputs[object.InputMutantSelection] = o.tool.Selection()
	if v := tools[o.tool.Name()]; v != "" {
		res.tools[o.tool.Name()] = v
	}
	if v := tools[ToolPytest]; v != "" {
		res.tools[ToolPytest] = v
	}

	run := MutationRun{
		Spec:       o.spec.Mutation,
		SessionDir: path.Join(o.inWorldScratch(), "mutation-session"),
		ConfigPath: path.Join(o.inWorldScratch(), "mutation-tool.conf"),
		PatchPath:  path.Join(o.inWorldScratch(), "captured.patch"),
		TestArgv:   o.suiteArgv(),
		Paths:      target.Paths(),
	}

	// Step 3 — the baseline. One suite run on the UNMUTATED tree, in the
	// same session, to establish that the tests pass before mutants are
	// introduced and to fix the per-mutant timeout. A failing baseline is
	// status = error: mutation over a red suite measures nothing.
	base, baseStream, ok := o.runSuite(ctx, w, run.TestArgv)
	res.argv = run.TestArgv
	res.exitCode = base.ExitCode
	res.durationMS = base.WallMS
	if o.ev.streaming() && o.ev.pluginDigest != "" {
		res.plugin = o.ev.pluginDigest
	}
	switch {
	case !ok:
		res.fail("the baseline suite produced no usable evidence stream (%s); mutation over an unobservable suite measures nothing", baseStream.Reason)
		return nil
	case baseStream.Failed > 0 || baseStream.Errored > 0 || base.ExitCode != 0:
		res.fail("the baseline suite is not green (exit_code=%d tests_failed=%d tests_errored=%d); mutation over a red suite measures nothing",
			base.ExitCode, baseStream.Failed, baseStream.Errored)
		return nil
	}
	res.report.BaselineWallMS = base.WallMS
	perMutant := o.perMutantTimeout(base.WallMS)
	res.report.PerMutantTimeoutMS = perMutant.Milliseconds()
	run.TimeoutSec = int64((perMutant + time.Second - 1) / time.Second)

	if err := o.writeToolState(target, run); err != nil {
		return err
	}

	// Step 4 — enumerate, then filter to the diff. Anything the tool
	// offers outside the target set is somebody else's code.
	mutants, err := o.enumerate(ctx, w, target, run)
	if err != nil {
		res.fail("%v", err)
		return nil
	}
	mutants = FilterToTarget(mutants, target)
	// The control plane already counted the mutable diff lines itself, from
	// bytes it captured and hashed. A tool that reports ZERO mutants over a
	// NON-EMPTY target set is therefore contradicting a number we derived
	// without it, and the two readings of that contradiction — "your tool
	// was shadowed by a package in the candidate's tree" and "your tool's
	// output shape changed" — are both machinery, neither is a fact about
	// the candidate, and a vacuous pass would launder both. It is the same
	// refusal runMutant already applies to "exit 0 but the stream reports
	// failures": a contradiction is not a verdict.
	//
	// The genuinely empty target set is handled at step 1 and never reaches
	// here, so the vacuous pass stays available for the one case that earns
	// it — a patch that changed no mutable source line.
	if len(mutants) == 0 {
		res.fail("%s enumerated 0 mutants over %d targeted diff line(s) in %d file(s); the control plane derived that target set from the captured patch, so a tool that finds nothing in it is contradicting a count it did not produce — a shadowed tool module or a changed output shape, never a fact about this candidate",
			o.tool.Name(), target.Lines, len(target.Files))
		return nil
	}
	res.metrics[policy.MetricMutantsCandidates] = int64(len(mutants))
	res.report.Candidates = int64(len(mutants))

	// Step 5 — select: per-line cap, canonical order, then the ceiling.
	selected := SelectMutants(mutants, o.selectPerLine(), o.spec.Mutation.MaxMutants)
	res.report.Truncated = int64(len(mutants) - len(selected))

	// Step 6 — execute exactly that set.
	counts := map[string]int64{
		MutantKilled: 0, MutantSurvived: 0, MutantTimeout: 0, MutantUnviable: 0,
	}
	tested := int64(0)
	for _, m := range selected {
		if ctx.Err() != nil {
			// Clock-truncation. A run stopped by the oracle's wall clock
			// tested whatever the machine got through, which is not
			// reproducible and not a fact about the candidate (decision
			// 17). Determinism is the dividing line between admissible
			// partial evidence and machinery error, and this is the wrong
			// side of it.
			res.fail("the oracle's wall clock stopped the run after %d of %d selected mutants; a clock-truncated mutation score is not reproducible and is not evidence about this candidate",
				tested, len(selected))
			return nil
		}
		rec, reason := o.runMutant(ctx, w, m, run, perMutant)
		if ctx.Err() != nil {
			// The wall clock stopped this mutant mid-flight. Whatever the
			// partial run looked like, it is the same non-reproducible
			// prefix — so it takes the same exit as the check above rather
			// than being classified from a truncated stream.
			res.fail("the oracle's wall clock stopped the run after %d of %d selected mutants; a clock-truncated mutation score is not reproducible and is not evidence about this candidate",
				tested, len(selected))
			return nil
		}
		if reason != "" {
			res.fail("mutant %s (%s:%d): %s", m.Digest, m.Path, m.Line, reason)
			return nil
		}
		tested++
		counts[rec.Outcome]++
		res.report.Selected = append(res.report.Selected, rec)
		switch rec.Outcome {
		case MutantSurvived:
			res.report.Survivors = append(res.report.Survivors, rec)
		case MutantTimeout:
			res.report.Timeouts = append(res.report.Timeouts, rec)
		}
	}

	res.metrics[policy.MetricMutantsTested] = tested
	res.metrics[policy.MetricMutantsKilled] = counts[MutantKilled]
	res.metrics[policy.MetricMutantsSurvived] = counts[MutantSurvived]
	res.metrics[policy.MetricMutantsTimeout] = counts[MutantTimeout]
	res.metrics[policy.MetricMutantsUnviable] = counts[MutantUnviable]
	// The score's denominator excludes timeouts and unviable mutants, and
	// the metric is ABSENT when that denominator is zero — a ratio over
	// nothing is not a zero score.
	if den := counts[MutantKilled] + counts[MutantSurvived]; den > 0 {
		res.metrics[policy.MetricMutationScoreBP] = counts[MutantKilled] * 10000 / den
	}
	if res.report.Truncated > 0 {
		res.notes += fmt.Sprintf("mvo: oracle: mutants_candidates=%d exceeds the pinned ceiling mutants_budget=%d; %d mutant(s) were not tested (count-truncation: a deterministic, control-plane-selected prefix)\n",
			res.report.Candidates, o.spec.Mutation.MaxMutants, res.report.Truncated)
	}
	return nil
}

// selectPerLine is the per-line cap actually applied. Under tool selection
// the enumeration carries no positions, so there is nothing to cap and the
// report says the filter did not run.
func (o *mutationOracle) selectPerLine() int64 {
	if o.tool != nil && o.tool.Selection() == SelectionTool {
		return 0
	}
	return o.spec.Mutation.MaxPerLine
}

// perMutantTimeout is the policy's bound, or cargo-mutants' heuristic over
// the measured baseline (ch. 19): a mutant that takes five times the
// unmutated suite is hung, not slow.
func (o *mutationOracle) perMutantTimeout(baselineMS int64) time.Duration {
	if ms := o.spec.Mutation.TimeoutPerMutant; ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	d := time.Duration(baselineMS*policy.MutantTimeoutFactor) * time.Millisecond
	if d < time.Second {
		return time.Second
	}
	return d
}

// writeToolState writes the tool's configuration and the captured patch
// into CONTROL-PLANE SCRATCH. The patch is written because mutmut scopes
// itself with `--use-patch-file`; the bytes are the ones the control plane
// captured and hashed, so a candidate cannot widen its own target set by
// rewriting a patch file in its tree — there is no patch file in its tree.
func (o *mutationOracle) writeToolState(target TargetSet, run MutationRun) error {
	if o.ev.hostScratch == "" {
		return nil
	}
	// The session directory is the control plane's to create, so a tool
	// that would otherwise fall back to a default inside the worktree has
	// somewhere to write that is not the candidate's tree.
	if err := ensureDir(filepath.Join(o.ev.hostScratch, filepath.Base(run.SessionDir)), 0o777); err != nil {
		return fmt.Errorf("oracle: %s: session dir: %w", KindMutationDiff, err)
	}
	if cfg := o.tool.Config(target, run); cfg != nil {
		p := filepath.Join(o.ev.hostScratch, filepath.Base(run.ConfigPath))
		if err := os.WriteFile(p, cfg, 0o644); err != nil {
			return fmt.Errorf("oracle: %s: write tool config: %w", KindMutationDiff, err)
		}
	}
	p := filepath.Join(o.ev.hostScratch, filepath.Base(run.PatchPath))
	if err := os.WriteFile(p, o.patch, 0o644); err != nil {
		return fmt.Errorf("oracle: %s: write captured patch: %w", KindMutationDiff, err)
	}
	return nil
}

// enumerate runs the adapter's enumeration steps and parses the marked one.
func (o *mutationOracle) enumerate(ctx context.Context, w backend.World, target TargetSet, run MutationRun) ([]Mutant, error) {
	var out []Mutant
	// The enumeration runs under PYTHONSAFEPATH: the tool is control-plane
	// machinery and must not resolve through the candidate's tree. See
	// envSafePath for the shadowing vector this closes and for why the seal
	// stops at the enumeration.
	env := append(evidenceEnv(o.ev, "", ""), envSafePath+"=1")
	for _, step := range o.tool.EnumerateSteps(o.python(), target, run) {
		res := runInWorld(ctx, w, step.Argv, mergeWorldEnv(w, env))
		if res.Status != StatusPass {
			return nil, fmt.Errorf("%s enumeration failed (exit_code=%d): %s",
				o.tool.Name(), res.ExitCode, firstLine(res.Stderr))
		}
		if !step.Parse {
			continue
		}
		ms, err := o.tool.ParseEnumeration(res.Stdout)
		if err != nil {
			return nil, fmt.Errorf("%s enumeration: %w", o.tool.Name(), err)
		}
		out = append(out, ms...)
	}
	for i, m := range out {
		out[i] = m.WithDigest()
	}
	return out, nil
}

// runMutant executes one mutant and classifies it FROM THE EVIDENCE
// STREAM. The second return is a machinery reason: non-empty means the run
// could not be classified honestly, and the whole oracle errors rather than
// guessing.
//
// The classification rules are M1f's S1 and S2, applied per mutant:
//
//   - an unusable stream is never classified. "Killed" is the outcome that
//     HELPS the candidate here — fewer survivors is a passing gate — so a
//     silenced observer must not resolve to it, and there is no honest
//     alternative bucket for "we could not see". Absence never passes,
//     including when the absence is convenient.
//   - a 0-exit whose stream reports failures, or a non-zero exit whose
//     stream reports none, is a contradiction, not a verdict.
func (o *mutationOracle) runMutant(ctx context.Context, w backend.World, m Mutant,
	run MutationRun, perMutant time.Duration) (MutantRecord, string) {
	rec := MutantRecord{Mutant: m}
	mutCtx, cancel := context.WithTimeout(ctx, perMutant)
	defer cancel()

	argv := o.tool.ExecArgv(o.python(), m, run)
	res, stream, usable := o.runStreamed(mutCtx, w, argv)
	claim, err := o.tool.ParseExec(res.Stdout)
	if err == nil {
		rec.Diff = claim.Diff
	}
	switch {
	case mutCtx.Err() != nil && ctx.Err() == nil:
		// A PER-MUTANT timeout is normal and is its own outcome: the
		// mutant hung, which is not "the tests caught it", so it is
		// counted and excluded from the score's denominator.
		rec.Outcome = MutantTimeout
	case !usable:
		return rec, fmt.Sprintf("no usable evidence stream (%s); a mutant that cannot be observed is not a mutant that was killed", stream.Reason)
	case res.ExitCode == exitNoTestsCollected, stream.Total == 0:
		// Nothing ran: the mutant did not import, or it emptied the
		// suite. Counted, and excluded from the denominator.
		rec.Outcome = MutantUnviable
	case stream.Failed > 0 || stream.Errored > 0:
		if res.ExitCode == 0 {
			return rec, fmt.Sprintf("exit_code=0 but the evidence stream reports failed=%d errored=%d",
				stream.Failed, stream.Errored)
		}
		rec.Outcome = MutantKilled
	case res.ExitCode == 0:
		rec.Outcome = MutantSurvived
	default:
		return rec, fmt.Sprintf("exit_code=%d but the evidence stream reports no failure at all", res.ExitCode)
	}
	return rec, ""
}

// exitNoTestsCollected is pytest's exit 5 (M1e decision 14): "no tests
// collected", distinguishable from a failure only because the number
// survives.
const exitNoTestsCollected = 5

// runSuite runs the unmutated suite once under the evidence channel.
func (o *mutationOracle) runSuite(ctx context.Context, w backend.World, argv []string) (procResult, Stream, bool) {
	return o.runStreamed(ctx, w, argv)
}

// runStreamed opens a fresh evidence channel, runs argv inside the world,
// tears the channel down and parses whatever arrived. Each mutant gets its
// OWN channel and its own nonce: a stream left over from the previous
// mutant is a stream this one could be talked into being judged by.
func (o *mutationOracle) runStreamed(ctx context.Context, w backend.World, argv []string) (procResult, Stream, bool) {
	if !o.ev.streaming() || o.ev.hostEvidence == "" {
		res := runInWorld(ctx, w, argv, mergeWorldEnv(w, evidenceEnv(o.ev, "", "")))
		return res, Stream{Reason: "no evidence channel was supplied"}, false
	}
	nonce, err := newNonce()
	if err != nil {
		res := runInWorld(ctx, w, argv, mergeWorldEnv(w, evidenceEnv(o.ev, "", "")))
		return res, Stream{Reason: err.Error()}, false
	}
	if err := ensureDir(o.ev.hostEvidence, 0o755); err != nil {
		res := runInWorld(ctx, w, argv, mergeWorldEnv(w, evidenceEnv(o.ev, "", "")))
		return res, Stream{Reason: err.Error()}, false
	}
	ch, err := openEvidenceChannel(o.ev.hostEvidence, nonce, o.artifactCap())
	if err != nil {
		res := runInWorld(ctx, w, argv, mergeWorldEnv(w, evidenceEnv(o.ev, "", "")))
		return res, Stream{Reason: err.Error()}, false
	}
	res := runInWorld(ctx, w, argv, mergeWorldEnv(w, evidenceEnv(o.ev, o.inWorldStream(), nonce)))
	stream := ParseStream(ch.Close(), nonce, ch.Over())
	return res, stream, stream.Usable
}

func (o *mutationOracle) artifactCap() int64 {
	if o.cap > 0 {
		return o.cap
	}
	return artifactCapBytes
}

// storeArtifacts writes the report and the notes. EP-7's ordering holds:
// every artifact reaches CAS before anything derived from it is reported.
func (o *mutationOracle) storeArtifacts(res *mutationOutcome) ([]string, error) {
	reportBytes, err := object.Canonical(res.report)
	if err != nil {
		return nil, fmt.Errorf("oracle: %s: canonical %s: %w", KindMutationDiff, artMutationReport, err)
	}
	reportKey, err := o.store.Put(reportBytes)
	if err != nil {
		return nil, fmt.Errorf("oracle: %s: store %s: %w", KindMutationDiff, artMutationReport, err)
	}
	notesKey, err := o.store.Put([]byte(res.notes))
	if err != nil {
		return nil, fmt.Errorf("oracle: %s: store %s: %w", KindMutationDiff, artStderr, err)
	}
	out := []string{reportKey, notesKey}
	if res.probeKey != "" {
		out = append(out, res.probeKey)
	}
	return out, nil
}

// resolveMutationTool picks the adapter. `auto` prefers cosmic-ray, which
// is the only one of the two that lets the control plane enumerate, order
// and cap the mutant set (M2a decision 19); mutmut is the fallback and its
// receipts say `mutant_selection: "tool"`.
//
// A tool the probe did not find is a PRE-FLIGHT machinery abort in a race.
// Here — an oracle invoked anyway — it is an error whose metrics are
// absent, which fails the survivor gate for want of a metric rather than
// convicting the candidate of a toolchain problem.
func resolveMutationTool(want string, tools map[string]string) (MutationTool, error) {
	has := func(name string) bool { return tools[name] != "" }
	switch want {
	case policy.MutationToolCosmicRay:
		if has(ToolCosmicRay) {
			return cosmicRayTool{}, nil
		}
		return nil, fmt.Errorf("policy pins mutation tool %q, which is not importable in this environment; a missing toolchain is machinery, never a failing candidate", ToolCosmicRay)
	case policy.MutationToolMutmut:
		if has(ToolMutmut) {
			return mutmutTool{}, nil
		}
		return nil, fmt.Errorf("policy pins mutation tool %q, which is not importable in this environment; a missing toolchain is machinery, never a failing candidate", ToolMutmut)
	default:
		switch {
		case has(ToolCosmicRay):
			return cosmicRayTool{}, nil
		case has(ToolMutmut):
			return mutmutTool{}, nil
		}
		return nil, fmt.Errorf("no mutation toolchain is importable in this environment (looked for %s and %s); a missing toolchain is machinery, never a failing candidate",
			ToolCosmicRay, ToolMutmut)
	}
}

// firstLine is the one line of a tool's stderr worth putting in a reason.
func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// shellJoin renders an argv as one shell word list for a tool config that
// takes a command STRING (cosmic-ray's test-command, mutmut's --runner).
// Single quotes are the only quoting a POSIX shell needs and the only one
// we emit.
func shellJoin(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`") {
			parts = append(parts, a)
			continue
		}
		parts = append(parts, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
	}
	return strings.Join(parts, " ")
}

// MissingTools reports the distributions one declared instance cannot run
// without and the probe did not find, sorted. It is the pre-flight half of
// M2a decision 20: a policy that requires a rung whose toolchain is absent
// is refused BEFORE race.started, with the ledger untouched, because a
// missing toolchain is machinery and never a failing candidate.
//
// pytest itself is checked by the caller for every Python rung, so it is
// not repeated here. `auto` is satisfied by EITHER mutation tool, and the
// error names both — an operator who has neither should be told what to
// install, not which one we would have picked.
func MissingTools(o policy.Oracle, tools map[string]string) []string {
	has := func(name string) bool { return tools[name] != "" }
	switch o.Kind {
	case KindProperties:
		if !has(ToolHypothesis) {
			return []string{ToolHypothesis}
		}
	case KindMutationDiff:
		switch o.Mutation.Tool {
		case policy.MutationToolCosmicRay:
			if !has(ToolCosmicRay) {
				return []string{ToolCosmicRay}
			}
		case policy.MutationToolMutmut:
			if !has(ToolMutmut) {
				return []string{ToolMutmut}
			}
		default:
			if !has(ToolCosmicRay) && !has(ToolMutmut) {
				return []string{ToolCosmicRay, ToolMutmut}
			}
		}
	}
	return nil
}
