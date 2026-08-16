package race

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/policy"
)

// corpusPlan is phase 0's output: the race's pinned inputs, the base tree's
// own observation of them, and where the corpus lives on disk.
//
// PHASE 0 runs after race.started and BEFORE phase A, on one worktree at
// the intent's base commit through the same backend, tier, image and env as
// the candidates — the M1e baseline's fixture, reused. What it produces is
// an INPUT to the race, not evidence about a candidate: a Receipt must bind
// a World, and the base tree is nobody's candidate, so nothing here is
// minted as one. `corpus.recorded` is observational, exactly like
// `baseline.recorded`.
type corpusPlan struct {
	spec     policy.Oracle // the declared corpus-observe instance
	corpus   oracle.Corpus
	digest   string // "mv0:…" over the corpus object's canonical bytes
	dir      string // phase 0's own directory — control-plane owned, NEVER a worktree
	hostFile string
	// inWorldFile is where the BASE world read the corpus during phase 0.
	// Candidate worlds read their own per-world copy instead, resolved in
	// buildLadder: the host path on T0, the read-only per-world mount on T1.
	inWorldFile string
	// baseObs is the base tree's observation — the ANCHOR every "vs base"
	// count is measured against, and never a cohort member.
	baseObs       oracle.Observation
	baseObsKey    string // CAS key of the base observation stream
	runner        string // in-world path of mvo_corpus.py
	runnerHost    string
	runnerDigest  string
	differential  policy.Oracle
	hasDifference bool
}

// needsCorpus reports whether a policy obliges the race to materialize a
// corpus at all. Declared-but-unrequired oracles are never run, so a corpus
// nothing consumes is never built: evidence waste is a measured PRD metric.
func needsCorpus(pol policy.Policy) (policy.Oracle, bool) {
	o, ok := pol.CorpusOracle()
	if !ok {
		return policy.Oracle{}, false
	}
	for _, name := range pol.Required {
		if name == o.Name {
			return o, true
		}
	}
	return policy.Oracle{}, false
}

// preflightCorpus refuses, as MACHINERY and before race.started, a policy
// whose corpus provider this binary cannot materialize. M1e decision 15's
// shape and M1f's `isolated` precedent: a binary that cannot deliver what a
// policy pins must say so in one line with the ledger untouched, never
// substitute something else and race on it.
func preflightCorpus(pol policy.Policy) error {
	o, ok := needsCorpus(pol)
	if !ok {
		return nil
	}
	if oracle.CorpusProviderSupported(o.Corpus.Provider) {
		return nil
	}
	return fmt.Errorf(
		"race: policy %s declares corpus provider %q on oracle %q, which this binary cannot materialize (it ships %q and %q); a corpus is the INPUT every world is compared on, so substituting a different one would make the comparison meaningless — declare a provider this binary ships, or use a binary that ships this one",
		pol.Digest, o.Corpus.Provider, o.Name, policy.ProviderDeclared, policy.ProviderRepoSuite)
}

// materializeCorpus is phase 0. It aborts the race as machinery — ledger
// untouched beyond the observational event it records first — on any of
// three conditions, and each of them is a case where every downstream
// number would be a fiction:
//
//   - a corpus of ZERO cases: a differential over no inputs partitions
//     nothing and reports "no divergence" over a comparison that never
//     happened;
//   - an UNUSABLE base observation: without the anchor there is no "vs
//     base" column and no way to tell a case the base could not execute
//     from a case a candidate changed;
//   - a base observation that is not COMPLETE over the corpus: the base
//     tree could not run inputs we are about to hold candidates to.
func (r *raceRun) materializeCorpus(ctx context.Context, pol policy.Policy, bw *baseWorld) (*corpusPlan, error) {
	spec, ok := needsCorpus(pol)
	if !ok {
		return nil, nil
	}
	if r.ev.pluginDir == "" {
		return nil, fmt.Errorf("race: corpus: the observer directory was not materialized (evidence regime %q)", r.ev.regime)
	}
	runnerHost, runnerDigest, err := oracle.MaterializeCorpusRunner(r.ev.pluginDir)
	if err != nil {
		return nil, fmt.Errorf("race: %w", err)
	}
	if _, err := r.cfg.CAS.Put(oracle.CorpusRunnerSource()); err != nil {
		return nil, fmt.Errorf("race: store corpus runner: %w", err)
	}
	tier := r.cfg.Backend.Tier()
	plan := &corpusPlan{
		spec: spec,
		// Phase 0's own directory, under the race's corpus ROOT and outside
		// the worlds tree entirely. Only the base world ever reads it.
		dir:          r.corpusDir,
		runnerHost:   runnerHost,
		runnerDigest: runnerDigest,
	}
	plan.runner = runnerHost
	worldRoot := bw.dir
	if tier == object.TierT1Container {
		plan.runner = backend.InWorldPlugin + "/" + filepath.Base(runnerHost)
		worldRoot = backend.InWorldRoot
	}
	plan.differential, plan.hasDifference = pol.DifferentialOracle()

	corpus, stdout, stderr, err := oracle.MaterializeCorpus(ctx, bw.wh, oracle.MaterializeParams{
		Spec:      spec,
		BaseTree:  r.intent.Base.Tree,
		Dir:       plan.dir,
		Runner:    plan.runner,
		WorldRoot: worldRoot,
		NodeIDs:   r.baseNodeIDs,
	})
	stdoutKey, putErr := r.cfg.CAS.Put(stdout)
	if putErr != nil {
		return nil, fmt.Errorf("race: corpus: store stdout: %w", putErr)
	}
	stderrKey, putErr := r.cfg.CAS.Put(stderr)
	if putErr != nil {
		return nil, fmt.Errorf("race: corpus: store stderr: %w", putErr)
	}
	if err != nil {
		return nil, fmt.Errorf("race: %w", err)
	}
	dig, canon, err := corpus.Digest()
	if err != nil {
		return nil, fmt.Errorf("race: corpus: %w", err)
	}
	if _, err := r.cfg.CAS.Put(canon); err != nil {
		return nil, fmt.Errorf("race: corpus: store corpus: %w", err)
	}
	plan.corpus, plan.digest = corpus, dig
	if plan.hostFile, err = oracle.WriteCorpus(plan.dir, canon); err != nil {
		return nil, fmt.Errorf("race: %w", err)
	}
	plan.inWorldFile = plan.hostFile
	if tier == object.TierT1Container {
		plan.inWorldFile = backend.InWorldCorpus + "/" + oracle.CorpusFile
	}

	// The base observation: the same oracle, the same runner, the same
	// stream, run on the base tree. It is the anchor, never a member.
	baseRec, baseObs, obsErr := r.observeBase(ctx, plan, bw, worldRoot)
	plan.baseObs = baseObs
	if len(baseRec.Result.Artifacts) == 3 {
		plan.baseObsKey = baseRec.Result.Artifacts[2]
	}

	// Recorded BEFORE any abort, so a failed materialization is on the
	// record rather than merely in an error string.
	if err := r.appendEventLocked("corpus.recorded", map[string]any{
		"base_observation": plan.baseObsKey,
		"cases":            int64(len(corpus.Cases)),
		"corpus":           dig,
		"dropped":          corpus.Dropped,
		"intent":           r.cfg.Intent,
		"observer":         runnerDigest,
		"oracle": map[string]any{
			"config":  plan.spec.Config,
			"id":      plan.spec.Kind,
			"version": "v0",
		},
		"provenance": corpus.Provenance,
		"provider":   corpus.Provider,
		"stderr":     stderrKey,
		"stdout":     stdoutKey,
		"tree":       r.intent.Base.Tree,
	}); err != nil {
		return nil, err
	}
	if obsErr != nil {
		return nil, obsErr
	}
	switch {
	case len(corpus.Cases) == 0:
		return nil, fmt.Errorf("race: corpus: provider %q produced 0 cases on base tree %s (dropped: unresolved=%d not-representable=%d); a differential over an empty corpus produces numbers that are all fictions",
			corpus.Provider, r.intent.Base.Tree,
			corpus.Dropped[oracle.DropTargetUnresolved], corpus.Dropped[oracle.DropNotRepresentable])
	case !baseObs.Usable:
		return nil, fmt.Errorf("race: corpus: the base tree produced no usable observation of corpus %s (%s); without the anchor there is no \"vs base\" column and no way to tell a case the base could not run from a case a candidate changed",
			dig, baseObs.Reason)
	case baseObs.ObservedCount() != int64(len(corpus.Cases)):
		return nil, fmt.Errorf("race: corpus: the base tree observed %d of %d cases of corpus %s; a race must not hold candidates to inputs its own base tree could not execute",
			baseObs.ObservedCount(), len(corpus.Cases), dig)
	}
	return plan, nil
}

// observeBase runs the corpus once on the base tree.
func (r *raceRun) observeBase(ctx context.Context, plan *corpusPlan, bw *baseWorld, worldRoot string) (object.Receipt, oracle.Observation, error) {
	tier := r.cfg.Backend.Tier()
	p := oracle.Params{
		Spec: plan.spec, CAS: r.cfg.CAS, Timeout: r.oracleTimeout,
		Regime: r.ev.regime, Crosscheck: r.ev.crosscheck, PluginAutoload: r.ev.autoload,
		Corpus: plan.corpus, CorpusDigest: plan.digest, BaseTree: r.intent.Base.Tree,
		CorpusPath: plan.inWorldFile, CorpusRunner: plan.runner,
		CorpusRunnerDigest: plan.runnerDigest, WorldRoot: worldRoot,
		EvidenceDir:     filepath.Join(bw.evRoot, plan.spec.Kind),
		ScratchDir:      filepath.Join(bw.scratchRoot, plan.spec.Kind),
		InWorldEvidence: inWorldPath(tier, backend.InWorldEvidence, plan.spec.Kind),
		InWorldScratch:  inWorldPath(tier, backend.InWorldScratch, plan.spec.Kind),
	}
	o, err := oracle.New(p)
	if err != nil {
		return object.Receipt{}, oracle.Observation{}, fmt.Errorf("race: corpus: %w", err)
	}
	ob, ok := o.(oracle.Observer)
	if !ok {
		return object.Receipt{}, oracle.Observation{}, fmt.Errorf("race: corpus: oracle %q does not observe", plan.spec.Name)
	}
	rec, obs, err := ob.Observe(ctx, bw.wh)
	if err != nil {
		return object.Receipt{}, oracle.Observation{}, fmt.Errorf("race: corpus: base observation: %w", err)
	}
	// NO RECEIPT IS RECORDED. A receipt binds a world and the base tree is
	// nobody's candidate (M1e decision 13's rule, unchanged): the stream
	// reaches CAS as an artifact and its digest travels on the
	// observational event, so the measurement is inside the audited
	// closure without pretending to be evidence about anyone.
	return rec, obs, nil
}

// worldCorpusDir creates one world's own corpus directory, EMPTY. It is
// created before the world is opened because a T1 container's mounts are
// fixed at open time, and it stays empty until phase A has joined: the
// mount exists throughout generation, the inputs do not.
func (r *raceRun) worldCorpusDir(ordinal int) (string, error) {
	if r.corpusRoot == "" {
		return "", nil
	}
	dir := filepath.Join(r.corpusRoot, fmt.Sprintf("w%03d", ordinal))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("race: corpus dir: %w", err)
	}
	return dir, nil
}

// deliverCorpus writes each completed world's own copy of the pinned
// corpus, AFTER phase A has joined and before any oracle runs.
//
// One shared, mutable file was the whole of a cross-world attack. The
// runner re-reads the corpus from disk on every replay, so a candidate that
// wrote to the shared file at import time poisoned the NEXT world's replay
// and never its own observation — either declaring a case id nobody pinned,
// which made the honest sibling's observation unusable and convicted it of
// evidence tampering it did not do, or preserving every id and rewriting
// one argument so that both worlds agreed and the split was suppressed.
// Neither variant touched a protected or harness path.
//
// A per-world copy removes the shared object. At T1 it is also structurally
// closed: each world binds only its own directory, read-only. At T0 the
// oracle and the candidate run as the same uid on one filesystem, so a
// sibling that walks to another world's directory can still write it —
// which is why the digest is re-checked either side of every replay
// (checkCorpusFile) and why the observation itself is bound to the bytes it
// loaded (ParseObservation's fourth usability rule). The residual is
// recorded as an open vector rather than described as closed.
func (r *raceRun) deliverCorpus(plan *corpusPlan, completed []int) error {
	if plan == nil || r.corpusRoot == "" {
		return nil
	}
	canon, err := os.ReadFile(plan.hostFile)
	if err != nil {
		return fmt.Errorf("race: corpus: re-read the pinned corpus: %w", err)
	}
	if got := object.DigestBytes(canon); got != plan.digest {
		return fmt.Errorf("race: corpus: the pinned corpus at %s now digests to %s, not %s; the race's shared inputs changed between phase 0 and phase B, so no comparison over them means anything",
			plan.hostFile, got, plan.digest)
	}
	for _, i := range completed {
		s := &r.slots[i]
		if s.corpusDir == "" {
			continue
		}
		file, err := oracle.WriteCorpus(s.corpusDir, canon)
		if err != nil {
			return fmt.Errorf("race: %w", err)
		}
		s.corpusFile = file
	}
	return nil
}

// checkCorpusFile re-digests one world's delivered corpus. It is called
// immediately BEFORE and immediately AFTER that world's replay, and a
// mismatch aborts the RACE as machinery.
//
// Aborting is the honest verdict and failing a world is not. The bytes are
// the control plane's, the digest is pinned in the ledger, and a change to
// them is a statement about the machine rather than about any candidate —
// so the one thing this must never do is convict the world that was handed
// the altered file.
func (r *raceRun) checkCorpusFile(s *slot, when string) error {
	if s.corpusFile == "" || r.corpus == nil {
		return nil
	}
	b, err := os.ReadFile(s.corpusFile)
	if err != nil {
		return fmt.Errorf("race: corpus: %s the observation of world %s, its corpus copy could not be read: %w",
			when, s.dig, err)
	}
	if got := object.DigestBytes(b); got != r.corpus.digest {
		return fmt.Errorf("race: corpus: %s the observation of world %s, its corpus copy digests to %s and the race pinned %s; the inputs every world is compared on were rewritten during the race, so this is machinery and no candidate is judged on it",
			when, s.dig, got, r.corpus.digest)
	}
	return nil
}

// dropCorpusDir removes the race's corpus tree. It holds no evidence —
// the corpus object is in CAS and its digest is in the ledger — so it goes
// on every exit path, including --keep-worlds, which keeps the WORKTREES
// and never the plumbing.
func (r *raceRun) dropCorpusDir() {
	if r.corpusRoot == "" {
		return
	}
	_ = os.RemoveAll(r.corpusRoot)
}

// cohort assembles phase B2's membership: every world whose corpus-observe
// receipt PASSED with a usable observation, sorted by world digest.
//
// BOTH halves of that sentence are enforced, and the second one was missing
// once. `Usable` is a statement about the STREAM's framing — it kills on a
// missing session_finish, an undeclared case id and a repeated id — while
// the receipt's status is the statement about the RUN: `pass` iff the
// observation is usable AND the world produced a record for every declared
// case. A stream that answers three of four declared cases is usable, fails
// its own corpus-complete gate, and used to enter the cohort anyway; since
// the comparison denominator is an intersection over every member, that
// already-eliminated world deleted the distinguishing case from every
// honest sibling. Filtering on the receipt is what keeps the removal
// self-inflicted.
//
// A world outside the cohort is not sabotaged: its own corpus-complete gate
// fails on absent or short metrics, and diff_cohort_n records the shrinkage
// so every surviving number still has a named denominator. A WORLD CAN ONLY
// REMOVE ITSELF — with the residual the reducer names in
// oracle.Reduce, and with the T0 corpus-delivery residual recorded as an
// open vector in the design document.
func (r *raceRun) cohort() []oracle.CohortMember {
	out := make([]oracle.CohortMember, 0, len(r.slots))
	for i := range r.slots {
		s := &r.slots[i]
		if s.dig == "" || !s.obsOK || !s.obsPass || !s.obs.Usable {
			continue
		}
		out = append(out, oracle.CohortMember{World: s.dig, Obs: s.obs})
	}
	return out
}

// runCohortBarrier is PHASE B2. After phase B joins, the reducer runs ONCE
// over the cohort and its N receipts are recorded in cohort order.
//
// No process executes and nothing is opened. A race with fewer than two
// cohort members still records its receipts, with diff_cohort_n present and
// every other diff_* ABSENT — because a comparison of one is not a
// comparison, and recording a zero would be inventing an answer.
func (r *raceRun) runCohortBarrier(plan *corpusPlan) error {
	if plan == nil || !plan.hasDifference {
		return nil
	}
	res, err := oracle.Reduce(oracle.DifferentialInputs{
		Corpus:          plan.corpus,
		CorpusDigest:    plan.digest,
		BaseTree:        r.intent.Base.Tree,
		BaseObservation: plan.baseObsKey,
		Base:            plan.baseObs,
		Members:         r.cohort(),
		Spec:            plan.differential,
	})
	if err != nil {
		return fmt.Errorf("race: %w", err)
	}
	if len(res.Receipts) == 0 {
		return nil
	}
	// The report is ONE artifact with N referrers, and it reaches CAS
	// before any receipt that names it is recorded (EP-7 order).
	key, err := r.cfg.CAS.Put(res.Report)
	if err != nil {
		return fmt.Errorf("race: differential: store report: %w", err)
	}
	if key != res.ReportKey {
		return fmt.Errorf("race: differential: report stored as %s, receipts name %s", key, res.ReportKey)
	}
	// The cohort list too: inputs["cohort"] names it, `mvo audit`'s sweep
	// walks inputs[*], and a provenance digest that resolves to nothing is
	// a citation to a missing page.
	if _, err := r.cfg.CAS.Put(res.Cohort); err != nil {
		return fmt.Errorf("race: differential: store cohort: %w", err)
	}
	byWorld := make(map[string]int, len(r.slots))
	for i := range r.slots {
		byWorld[r.slots[i].dig] = i
	}
	for _, rec := range res.Receipts {
		i, ok := byWorld[rec.World]
		if !ok {
			// Unreachable: every cohort member is a slot's world digest.
			return fmt.Errorf("race: differential: receipt names world %s, which this race did not produce", rec.World)
		}
		s := &r.slots[i]
		rec.Freshness.ValidFor = object.ValidFor{Tree: s.world.Tree, Env: s.world.Env}
		rec.CreatedAt = nowRFC3339()
		dig, err := r.recordObjectLocked("receipt.recorded", rec)
		if err != nil {
			return err
		}
		s.receipts = append(s.receipts, object.RecordedReceipt{Digest: dig, Receipt: rec})
	}
	return nil
}
