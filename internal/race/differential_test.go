package race

// M2a: the cohort barrier, the on_behavioral_split escalation rule, and the
// end-to-end proof that the differential detects a REAL behavioural
// divergence between two candidates that both pass the whole suite.
//
// The escalation tests are pure — hand-built receipts, no world, no Python.
// The end-to-end test drives real python3 over the real toyrepo and SKIPS
// WITH A NAMED REASON when python3 or pytest is absent, because a test that
// silently stops running is worse than no test.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/policy"
)

// --- the escalation rule, pure -----------------------------------------

// splitPolicy compiles the shipped differential fixture's shape with
// on_behavioral_split at the given threshold.
func splitPolicy(t *testing.T, threshold int) policy.Policy {
	t.Helper()
	p := object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "split",
		Oracles: []object.OracleSpec{
			{Name: "diff", Kind: policy.KindCorpusDifferential, Argv: []string{}, Args: []string{}},
			{Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{}},
			{Name: "observe", Kind: policy.KindCorpusObserve, Argv: []string{}, Args: []string{},
				Corpus: object.CorpusSpec{Provider: policy.ProviderDeclared, File: "corpora/c.json"}},
		},
		HardGates: []object.GateSpec{
			// Rule 24: corpora/c.json compiles into paths.harness, so a
			// paths-unmodified gate must actually read it.
			{Gate: policy.GatePathsUnmodified, Oracle: "guard", Basis: object.BasisConstruction},
			// Scope race (rule 23): the corpus this gate reads is
			// materialized by phase 0, which admission does not have.
			{Gate: policy.GateCorpusComplete, Oracle: "observe",
				Basis: object.BasisConstruction, Scope: policy.ScopeRace},
		},
		Ranking: []string{policy.KeyGatePass},
		Escalation: object.EscalationSpec{
			RequireEvidence: []string{}, OnBehavioralSplit: threshold,
			OnAllWorldsFailedMachinery: true, OnRankingTie: true,
		},
		Paths:      object.PathSpec{Protected: []string{}, Harness: []string{}},
		Invariants: []object.InvariantSpec{},
	}
	if threshold == 0 {
		p.Escalation.OnBehavioralSplit = 0
	}
	if err := policy.Validate(p); err != nil {
		t.Fatalf("split policy does not validate: %v", err)
	}
	pol, err := policy.Compile("mv0:split", p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return pol
}

// splitWorld builds one world plus its two receipts: a passing
// corpus-observe and a comparison receipt with the given partition.
func splitWorld(t *testing.T, pol policy.Policy, dig string, classes, cohortN, classSize int64, class, first string) (
	object.RecordedWorld, []object.RecordedReceipt) {
	t.Helper()
	w := object.World{
		Schema: object.SchemaWorld, Intent: "mv0:intent", Tree: "git:tree-" + dig[4:],
		Env: "mv0:env", Outcome: object.OutcomeCompleted,
	}
	rw := object.RecordedWorld{Digest: dig, World: w}
	valid := object.Freshness{Basis: object.BasisConstruction,
		ValidFor: object.ValidFor{Tree: w.Tree, Env: w.Env}}

	observe, _ := pol.OracleByName("observe")
	obsRec := object.Receipt{
		Schema: object.SchemaReceipt, World: dig,
		Oracle:    object.OracleRef{ID: observe.Kind, Version: "v0", Config: observe.Config},
		Execution: object.Execution{Argv: []string{"python3"}, EvidenceRegime: object.RegimeStreamed},
		Result: object.Result{Status: "pass", Tools: map[string]string{}, Artifacts: []string{},
			Metrics: map[string]int64{
				policy.MetricCorpusCasesTotal: 4, policy.MetricCorpusCasesObserved: 4,
				policy.MetricCorpusCasesOpaque: 0, policy.MetricCorpusCasesErrored: 0,
			}},
		Freshness: valid, RecheckTier: "V1-replayable", Family: policy.FamilyBehavior,
		Inputs: object.NoInputs(),
	}
	diff, _ := pol.OracleByName("diff")
	metrics := map[string]int64{policy.MetricDiffCohortN: cohortN}
	detail := ""
	if cohortN >= 2 {
		metrics[policy.MetricDiffClasses] = classes
		metrics[policy.MetricDiffClassSize] = classSize
		metrics[policy.MetricDiffCasesCompared] = 4
		detail = behaviorDetailFor(class, "mv0:cohort", "mv0:corpus", first)
	} else {
		detail = behaviorDetailFor("", "mv0:cohort", "mv0:corpus", "")
	}
	diffRec := object.Receipt{
		Schema: object.SchemaReceipt, World: dig,
		Oracle:    object.OracleRef{ID: diff.Kind, Version: "v0", Config: diff.Config},
		Execution: object.Execution{Argv: []string{}, EvidenceRegime: object.RegimeDerived},
		Result: object.Result{Status: "pass", Tools: map[string]string{}, Artifacts: []string{"sha256:report"},
			Metrics: metrics, Detail: detail},
		Freshness: valid, RecheckTier: "V1-replayable", Family: policy.FamilyBehavior,
		Inputs: map[string]string{object.InputEvidenceFloor: object.RegimeStreamed},
	}
	guard, _ := pol.OracleByName("guard")
	guardRec := object.Receipt{
		Schema: object.SchemaReceipt, World: dig,
		Oracle:    object.OracleRef{ID: guard.Kind, Version: "v0", Config: guard.Config},
		Execution: object.Execution{Argv: []string{"git"}, EvidenceRegime: object.RegimeControlPlane},
		Result: object.Result{Status: "pass", Tools: map[string]string{}, Artifacts: []string{},
			Metrics: map[string]int64{
				policy.MetricProtectedModified: 0, policy.MetricProtectedDeleted: 0,
				policy.MetricProtectedAdded: 0, policy.MetricHarnessModified: 0,
				policy.MetricHarnessDeleted: 0, policy.MetricHarnessAdded: 0,
				policy.MetricPathsExamined: 1,
			}},
		Freshness: valid, RecheckTier: "V1-replayable", Family: policy.FamilyTree,
		Inputs: object.NoInputs(),
	}
	return rw, []object.RecordedReceipt{
		{Digest: "mv0:r0" + dig[4:], Receipt: guardRec},
		{Digest: "mv0:r1" + dig[4:], Receipt: obsRec},
		{Digest: "mv0:r2" + dig[4:], Receipt: diffRec},
	}
}

// behaviorDetailFor mirrors the reducer's result.detail shape. It goes
// through the exported prefix so a change to the rendering breaks here
// rather than silently disabling the escalation rule.
func behaviorDetailFor(class, cohort, corpus, first string) string {
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	return oracle.DetailPrefix + dash(class) + " (cohort " + dash(cohort) +
		", corpus " + dash(corpus) + ", first distinguishing case " + dash(first) + ")"
}

func TestOnBehavioralSplitEscalates(t *testing.T) {
	pol := splitPolicy(t, 2)
	wa, ra := splitWorld(t, pol, "mv0:aa11", 2, 2, 1, "mv0:aa11", "c0001")
	wb, rb := splitWorld(t, pol, "mv0:bb22", 2, 2, 1, "mv0:bb22", "c0001")
	tr := Trace(pol, []object.RecordedWorld{wa, wb}, append(ra, rb...))

	if tr.Type != TypeEscalate {
		t.Fatalf("type = %s, want ESCALATE — the shared evidence does not say which behaviour is intended", tr.Type)
	}
	if tr.Escalation.Rule != RuleOnBehavioralSplit {
		t.Fatalf("rule = %q, want %q", tr.Escalation.Rule, RuleOnBehavioralSplit)
	}
	want := "2 worlds split into 2 behavior classes over 4 compared cases of corpus mv0:corpus; " +
		"the shared evidence does not say which behavior is intended (first distinguishing case c0001)"
	if tr.Escalation.Detail != want {
		t.Errorf("detail =\n %q\nwant\n %q", tr.Escalation.Detail, want)
	}
	// BOTH worlds passed every hard gate. That is the case the rule exists
	// for: the ladder has nothing left to say and the difference is real.
	if tr.PassCount != 2 {
		t.Errorf("pass count = %d, want both worlds passing every hard gate", tr.PassCount)
	}
	// An ESCALATE never renders like a win (M1e decision 21): the rationale
	// carries the rule and the displaced sentence, and no world is a winner.
	if !strings.Contains(tr.Rationale, "escalated by policy rule on_behavioral_split") {
		t.Errorf("rationale = %q", tr.Rationale)
	}
	if strings.Contains(tr.Rationale, " won ") || strings.Contains(tr.Rationale, "WINNER") {
		t.Errorf("an ESCALATE rationale claims a winner: %q", tr.Rationale)
	}
}

// The rule has TEETH rather than firing on everything: one behaviour class
// is not a split, and the race decides normally.
func TestOnBehavioralSplitDoesNotFireOnAgreement(t *testing.T) {
	pol := splitPolicy(t, 2)
	wa, ra := splitWorld(t, pol, "mv0:aa11", 1, 2, 2, "mv0:aa11", "")
	wb, rb := splitWorld(t, pol, "mv0:bb22", 1, 2, 2, "mv0:aa11", "")
	tr := Trace(pol, []object.RecordedWorld{wa, wb}, append(ra, rb...))
	if tr.Escalation.Rule == RuleOnBehavioralSplit {
		t.Fatalf("the rule fired on a cohort with ONE behaviour class: %s", tr.Escalation.Detail)
	}
}

// A COMPARISON OF ONE IS NOT A COMPARISON: diff_classes is absent, and an
// absent metric can never be read as "one class" — the rule simply does not
// fire.
func TestOnBehavioralSplitIgnoresACohortOfOne(t *testing.T) {
	pol := splitPolicy(t, 2)
	wa, ra := splitWorld(t, pol, "mv0:aa11", 0, 1, 0, "", "")
	tr := Trace(pol, []object.RecordedWorld{wa}, ra)
	if tr.Escalation.Rule == RuleOnBehavioralSplit {
		t.Fatalf("the rule fired over a cohort of one: %s", tr.Escalation.Detail)
	}
	m := ra[2].Receipt.Result.Metrics
	if _, present := m[policy.MetricDiffClasses]; present {
		t.Error("the fixture itself carries diff_classes in a cohort of one")
	}
}

// Rule 1b sits BELOW machinery failure. If nothing produced evidence,
// "they disagree" is a false statement about a race that never ran.
func TestMachineryFailureOutranksTheSplit(t *testing.T) {
	pol := splitPolicy(t, 2)
	wa, ra := splitWorld(t, pol, "mv0:aa11", 2, 2, 1, "mv0:aa11", "c0001")
	wb, rb := splitWorld(t, pol, "mv0:bb22", 2, 2, 1, "mv0:bb22", "c0001")
	// Both worlds' observation receipts error out: the ladder produced no
	// verdict anywhere.
	for _, rr := range [][]object.RecordedReceipt{ra, rb} {
		rr[0].Receipt.Result.Status = "error"
		rr[0].Receipt.Result.Metrics = map[string]int64{}
	}
	tr := Trace(pol, []object.RecordedWorld{wa, wb}, append(ra, rb...))
	if tr.Escalation.Rule != RuleAllWorldsFailedMachinery {
		t.Fatalf("rule = %q, want %q to outrank the split", tr.Escalation.Rule, RuleAllWorldsFailedMachinery)
	}
}

// Rule 1b sits ABOVE on_ranking_tie: two candidates that tie on every key
// AND behave differently must be escalated with the INPUT, not with "two
// digests tied". That difference is the block.
func TestSplitOutranksTheRankingTie(t *testing.T) {
	pol := splitPolicy(t, 2)
	wa, ra := splitWorld(t, pol, "mv0:aa11", 2, 2, 1, "mv0:aa11", "c0001")
	wb, rb := splitWorld(t, pol, "mv0:bb22", 2, 2, 1, "mv0:bb22", "c0001")
	tr := Trace(pol, []object.RecordedWorld{wa, wb}, append(ra, rb...))
	if tr.Escalation.Rule != RuleOnBehavioralSplit {
		t.Fatalf("rule = %q, want the split to outrank %q", tr.Escalation.Rule, RuleOnRankingTie)
	}
	if !strings.Contains(tr.Escalation.Detail, "c0001") {
		t.Error("the escalation does not name the distinguishing case; without the input it says no more than a ranking tie")
	}
}

// The rule is OFF in every policy that does not name it, which is every
// policy predating M2a — the whole reason inserting a rule into a fixed
// precedence list is safe for replay.
func TestOnBehavioralSplitOffByDefault(t *testing.T) {
	pol := splitPolicy(t, 0)
	wa, ra := splitWorld(t, pol, "mv0:aa11", 2, 2, 1, "mv0:aa11", "c0001")
	wb, rb := splitWorld(t, pol, "mv0:bb22", 2, 2, 1, "mv0:bb22", "c0001")
	tr := Trace(pol, []object.RecordedWorld{wa, wb}, append(ra, rb...))
	if tr.Escalation.Rule == RuleOnBehavioralSplit {
		t.Fatal("the rule fired under a policy that did not declare it")
	}
	// Under the SHIPPED default it cannot even be declared: no corpus
	// oracle, so validation refuses the rule (rule 21).
	def := policy.Default()
	if def.Escalation.OnBehavioralSplit != 0 {
		t.Error("the shipped default turns on_behavioral_split ON; the base rate is a measurement M2b owes, not a guess with somebody else's attention")
	}
}

// The detail parser is TOTAL: a receipt with no detail (an older
// implementation, a hand-written fixture) disables the rule rather than
// producing a half-filled sentence.
func TestBehaviorDetailParserIsTotal(t *testing.T) {
	for _, tc := range []struct {
		in    string
		ok    bool
		first string
	}{
		{behaviorDetailFor("mv0:a", "mv0:c", "mv0:p", "c0007"), true, "c0007"},
		{behaviorDetailFor("", "mv0:c", "mv0:p", ""), true, "-"},
		{"", false, ""},
		{"behavior class mv0:a", false, ""},
		{"something else entirely", false, ""},
		{oracle.DetailPrefix + "mv0:a (cohort", false, ""},
	} {
		got, ok := parseBehaviorDetail(tc.in)
		if ok != tc.ok {
			t.Errorf("parseBehaviorDetail(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got.First != tc.first {
			t.Errorf("parseBehaviorDetail(%q).First = %q, want %q", tc.in, got.First, tc.first)
		}
	}
}

// --- the gate predicates ------------------------------------------------

func TestCorpusGatePredicates(t *testing.T) {
	pol := splitPolicy(t, 2)
	observe, _ := pol.OracleByName("observe")
	sel := policy.Selector{ID: observe.Kind, Config: observe.Config}
	_ = sel
	gate := policy.Gate{
		Predicate: policy.GateCorpusComplete, Oracle: "observe",
		Basis: object.BasisConstruction, Label: "corpus-complete@observe",
	}
	rec := func(status string, metrics map[string]int64) *object.Receipt {
		return &object.Receipt{
			Result:    object.Result{Status: status, Metrics: metrics},
			Freshness: object.Freshness{Basis: object.BasisConstruction},
		}
	}
	for _, tc := range []struct {
		name string
		rec  *object.Receipt
		pass bool
		want string
	}{
		{"complete", rec("pass", map[string]int64{"corpus_cases_observed": 4, "corpus_cases_total": 4}), true, ""},
		{"partial", rec("pass", map[string]int64{"corpus_cases_observed": 3, "corpus_cases_total": 4}), false, "corpus_cases_observed=3 of 4"},
		// Cohort starvation (corpus vector 18): the metrics are ABSENT, and
		// an absent metric fails the gate. The starving world eliminates
		// itself; it cannot remove anyone else.
		{"silenced", rec("error", map[string]int64{}), false, "status=error"},
		{"absent metrics on a passing status", rec("pass", map[string]int64{}), false, "corpus_cases_observed absent (source unavailable)"},
		{"no receipt at all", nil, false, policy.ReasonNoReceipt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := gate.Eval(tc.rec)
			if ok != tc.pass {
				t.Fatalf("pass = %v (%s), want %v", ok, reason, tc.pass)
			}
			if !ok && reason != tc.want {
				t.Errorf("reason = %q, want %q", reason, tc.want)
			}
		})
	}

	cohort := policy.Gate{
		Predicate: policy.GateDifferentialCohortAtLeast, Oracle: "diff",
		Basis: object.BasisConstruction, Threshold: 2, Scope: policy.ScopeRace,
	}
	if ok, _ := cohort.Eval(rec("pass", map[string]int64{"diff_cohort_n": 2})); !ok {
		t.Error("a cohort of two failed differential-cohort-at-least 2")
	}
	ok, reason := cohort.Eval(rec("pass", map[string]int64{"diff_cohort_n": 1}))
	if ok || reason != "diff_cohort_n=1 (want >= 2)" {
		t.Errorf("cohort of one: pass=%v reason=%q", ok, reason)
	}
	// Absence, again, never passes.
	if ok, reason := cohort.Eval(rec("pass", map[string]int64{})); ok || reason != "diff_cohort_n absent (source unavailable)" {
		t.Errorf("absent cohort count: pass=%v reason=%q", ok, reason)
	}
}

// --- the end-to-end proof -----------------------------------------------

// toyrepoRace builds a git repo from testdata/toyrepo and races the named
// patches under the given fixture policy.
func toyrepoRace(t *testing.T, policyFile string, patchNames ...string) (Config, *policyFixture) {
	t.Helper()
	repo := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "toyrepo")
	for _, rel := range []string{"stats.py", "test_stats.py", "corpora/clamp-nan.json"} {
		b, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		dst := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "init", "-q")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "toyrepo")

	raw, err := os.ReadFile(filepath.Join(src, "policies", policyFile))
	if err != nil {
		t.Fatal(err)
	}
	store, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	led, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { led.Close() })
	polDig, err := store.Put(raw)
	if err != nil {
		t.Fatal(err)
	}
	polDigest := object.DigestBytes(raw)
	_ = polDig
	commit, tree, err := gitx.Head(repo)
	if err != nil {
		t.Fatal(err)
	}
	intent := object.Intent{
		Schema: object.SchemaIntent,
		Base:   object.Base{Commit: commit, Tree: tree},
		Spec:   object.Spec{Title: "fix mean", Description: "make mean() correct"},
		Budget: object.Budget{MaxCandidates: max(len(patchNames), 1), MaxWallMS: 600000},
		Policy: polDigest, CreatedAt: fixedTime,
	}
	intentDig, intentCanon, err := object.Digest(intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(intentCanon); err != nil {
		t.Fatal(err)
	}
	cands := make([]Candidate, 0, len(patchNames))
	for _, name := range patchNames {
		rel := name
		if !strings.Contains(rel, "/") {
			rel = "patches-behave/" + rel
		}
		b, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read patch %s: %v", name, err)
		}
		cands = append(cands, Candidate{Prompt: string(b)})
	}
	return Config{
		Repo: repo, Ledger: led, CAS: store, Intent: intentDig,
		Adapter: mustAdapter(t, "script"), Candidates: cands,
		WorldsDir: filepath.Join(t.TempDir(), "worlds"),
		Backend:   mustBackend(t), Parallel: 1,
	}, &policyFixture{digest: polDigest, raw: raw}
}

type policyFixture struct {
	digest string
	raw    []byte
}

func requirePytest(t *testing.T) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("skipping the end-to-end differential with a named reason: python3 is not on PATH")
	}
	if err := exec.Command(py, "-c", "import pytest").Run(); err != nil {
		t.Skip("skipping the end-to-end differential with a named reason: pytest is not importable")
	}
}

// THIS IS THE BLOCK. Two candidates that pass every hard gate, tie on every
// ranking key that measures anything, and DIVERGE on one input the
// eight-test suite never exercises.
//
// Under M1f this race ESCALATEs on on_ranking_tie and tells the maintainer
// nothing except that two digests tied. Under M2a it ESCALATEs on
// on_behavioral_split and hands them the input and both answers.
func TestDifferentialEscalatesOnARealSplit(t *testing.T) {
	requirePytest(t)
	cfg, fixture := toyrepoRace(t, "differential.json", "patch-p.patch", "patch-q.patch")
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("race: %v", err)
	}
	pol, err := policy.Load(cfg.CAS, fixture.digest)
	if err != nil {
		t.Fatal(err)
	}

	// Both worlds pass every hard gate — including the suite. The
	// divergence is invisible to the whole M1 ladder.
	worlds := make([]object.RecordedWorld, 0, len(res.Worlds))
	var receipts []object.RecordedReceipt
	for _, w := range res.Worlds {
		worlds = append(worlds, object.RecordedWorld{Digest: w.Digest, World: w.World})
		receipts = append(receipts, w.Receipts...)
	}
	sort.Slice(worlds, func(i, j int) bool { return worlds[i].Digest < worlds[j].Digest })
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].Digest < receipts[j].Digest })
	tr := Trace(pol, worlds, receipts)
	if tr.PassCount != 2 {
		for i := range tr.Candidates {
			t.Logf("world %s: %s", tr.Candidates[i].World, tr.Candidates[i].GateCell())
			for _, g := range tr.Candidates[i].Gates {
				t.Logf("   %s %s %s", g.Label, g.Result, g.Detail)
			}
		}
		t.Fatalf("pass count = %d, want both candidates passing every hard gate", tr.PassCount)
	}

	if res.Decision.Type != TypeEscalate {
		t.Fatalf("decision = %s, want ESCALATE", res.Decision.Type)
	}
	if tr.Escalation.Rule != RuleOnBehavioralSplit {
		t.Fatalf("rule = %q (%s), want %q", tr.Escalation.Rule, tr.Escalation.Detail, RuleOnBehavioralSplit)
	}
	if !strings.Contains(res.Decision.Rationale, "escalated by policy rule on_behavioral_split") {
		t.Errorf("rationale = %q", res.Decision.Rationale)
	}
	// NEITHER world is rendered as a winner.
	if strings.Contains(res.Decision.Rationale, " won ") || strings.Contains(res.Decision.Rationale, "WINNER") {
		t.Errorf("an ESCALATE rationale claims a winner: %q", res.Decision.Rationale)
	}

	// The comparison receipts: one per world, each bound to exactly the
	// world it judges, each pointing at the SAME report.
	diffSpec, ok := pol.DifferentialOracle()
	if !ok {
		t.Fatal("the fixture policy declares no reducer")
	}
	sel := policy.Selector{ID: diffSpec.Kind, Config: diffSpec.Config}
	reports := map[string]bool{}
	seenWorlds := map[string]bool{}
	for _, rr := range receipts {
		if !sel.Match(rr.Receipt) {
			continue
		}
		seenWorlds[rr.Receipt.World] = true
		if rr.Receipt.Execution.EvidenceRegime != object.RegimeDerived {
			t.Errorf("comparison receipt regime = %q, want %q", rr.Receipt.Execution.EvidenceRegime, object.RegimeDerived)
		}
		if rr.Receipt.Inputs[object.InputEvidenceFloor] != object.RegimeStreamed {
			t.Errorf("evidence_floor = %q, want streamed", rr.Receipt.Inputs[object.InputEvidenceFloor])
		}
		if got := rr.Receipt.Result.Metrics[policy.MetricDiffClasses]; got != 2 {
			t.Errorf("diff_classes = %d, want 2", got)
		}
		if got := rr.Receipt.Result.Metrics[policy.MetricDiffCohortN]; got != 2 {
			t.Errorf("diff_cohort_n = %d, want 2", got)
		}
		// BOUND TO EXACTLY THE WORLDS IT COMPARED: valid_for pins this
		// world's own tree and env, so the receipt is not claiming to
		// judge a tree it never saw.
		for _, w := range res.Worlds {
			if w.Digest != rr.Receipt.World {
				continue
			}
			if rr.Receipt.Freshness.ValidFor.Tree != w.World.Tree ||
				rr.Receipt.Freshness.ValidFor.Env != w.World.Env {
				t.Errorf("comparison receipt for %s is not bound to its world's tree/env", w.Digest)
			}
		}
		if len(rr.Receipt.Result.Artifacts) != 1 {
			t.Fatalf("artifacts = %v, want exactly the report", rr.Receipt.Result.Artifacts)
		}
		reports[rr.Receipt.Result.Artifacts[0]] = true
	}
	if len(seenWorlds) != 2 {
		t.Fatalf("comparison receipts cover %d worlds, want one per cohort member", len(seenWorlds))
	}
	if len(reports) != 1 {
		t.Fatalf("the cohort produced %d reports, want ONE artifact with N referrers", len(reports))
	}

	// The report: the input and what each candidate returned on it.
	var key string
	for k := range reports {
		key = k
	}
	raw, err := cfg.CAS.Get(key)
	if err != nil {
		t.Fatalf("the report is not in CAS: %v", err)
	}
	var report struct {
		Corpus         string `json:"corpus"`
		Cohort         []string
		CasesCompared  int64 `json:"cases_compared"`
		Classes        []struct{ ID string }
		Distinguishing []struct {
			Args         []json.RawMessage
			Case         string
			Target       string
			Base         struct{ V json.RawMessage }
			Observations []struct {
				World string
				V     json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.CasesCompared != 4 {
		t.Errorf("cases_compared = %d, want the whole four-case corpus", report.CasesCompared)
	}
	if len(report.Classes) != 2 || len(report.Cohort) != 2 {
		t.Fatalf("classes = %d, cohort = %d, want 2 and 2", len(report.Classes), len(report.Cohort))
	}
	if len(report.Distinguishing) != 1 {
		t.Fatalf("distinguishing = %d, want exactly the NaN case", len(report.Distinguishing))
	}
	d := report.Distinguishing[0]
	if d.Case != "c0001" || d.Target != "stats:clamp" {
		t.Errorf("distinguishing case = %s on %s", d.Case, d.Target)
	}
	if got := oracle.RenderValue(d.Args[0]); got != "nan" {
		t.Errorf("the distinguishing input is %q, want nan", got)
	}
	answers := []string{}
	for _, o := range d.Observations {
		answers = append(answers, oracle.RenderValue(o.V))
	}
	sort.Strings(answers)
	if strings.Join(answers, ",") != "0,nan" {
		t.Errorf("answers = %v, want one world returning 0 and one returning nan", answers)
	}

	// The corpus was materialized on the base tree and announced ONCE,
	// before any world was created.
	assertCorpusPrecedesWorlds(t, cfg, report.Corpus)
	// And it never reached a generator: no world's captured patch, agent
	// context or transcript contains the corpus digest or a case id.
	assertCorpusNeverReachedAGenerator(t, cfg, res, report.Corpus)
}

// The control: the mechanism must have teeth rather than fire on
// everything. Two candidates that behave IDENTICALLY produce one behaviour
// class and no escalation from this rule.
func TestDifferentialDoesNotEscalateOnAgreement(t *testing.T) {
	requirePytest(t)
	// Two DISTINCT trees that behave identically: same fix, different
	// comment. Racing the same patch twice would collapse to one world
	// digest and test nothing.
	cfg, fixture := toyrepoRace(t, "differential.json",
		"patches-agree/patch-p.patch", "patches-agree/patch-p2.patch")
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("race: %v", err)
	}
	pol, err := policy.Load(cfg.CAS, fixture.digest)
	if err != nil {
		t.Fatal(err)
	}
	var worlds []object.RecordedWorld
	var receipts []object.RecordedReceipt
	for _, w := range res.Worlds {
		worlds = append(worlds, object.RecordedWorld{Digest: w.Digest, World: w.World})
		receipts = append(receipts, w.Receipts...)
	}
	tr := Trace(pol, worlds, receipts)
	if tr.Escalation.Rule == RuleOnBehavioralSplit {
		t.Fatalf("the rule fired on two behaviourally identical candidates: %s", tr.Escalation.Detail)
	}
	diffSpec, _ := pol.DifferentialOracle()
	sel := policy.Selector{ID: diffSpec.Kind, Config: diffSpec.Config}
	found := false
	for _, rr := range receipts {
		if !sel.Match(rr.Receipt) {
			continue
		}
		found = true
		if got := rr.Receipt.Result.Metrics[policy.MetricDiffClasses]; got != 1 {
			t.Errorf("diff_classes = %d, want 1", got)
		}
		if got := rr.Receipt.Result.Metrics[policy.MetricDiffCasesDivergent]; got != 0 {
			t.Errorf("diff_cases_divergent = %d, want 0", got)
		}
	}
	if !found {
		t.Fatal("no comparison receipt was recorded")
	}
}

// A COMPARISON OF ONE IS NOT A COMPARISON, end to end: a one-candidate race
// still records its comparison receipt, with diff_cohort_n present and
// every other diff_* ABSENT — absence, not zero.
func TestDifferentialCohortOfOneRecordsAbsence(t *testing.T) {
	requirePytest(t)
	cfg, fixture := toyrepoRace(t, "differential.json", "patch-p.patch")
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("race: %v", err)
	}
	pol, err := policy.Load(cfg.CAS, fixture.digest)
	if err != nil {
		t.Fatal(err)
	}
	diffSpec, _ := pol.DifferentialOracle()
	sel := policy.Selector{ID: diffSpec.Kind, Config: diffSpec.Config}
	found := false
	for _, w := range res.Worlds {
		for _, rr := range w.Receipts {
			if !sel.Match(rr.Receipt) {
				continue
			}
			found = true
			m := rr.Receipt.Result.Metrics
			if m[policy.MetricDiffCohortN] != 1 {
				t.Errorf("diff_cohort_n = %d, want 1", m[policy.MetricDiffCohortN])
			}
			if len(m) != 1 {
				t.Errorf("metrics = %v, want ONLY diff_cohort_n", m)
			}
		}
	}
	if !found {
		t.Fatal("a one-candidate race recorded no comparison receipt at all")
	}
	if res.Decision.Type == TypeEscalate && strings.Contains(res.Decision.Rationale, RuleOnBehavioralSplit) {
		t.Error("a cohort of one escalated as a behavioural split")
	}
}

// Phase 0's abort paths: a corpus of zero cases aborts the race as
// machinery rather than producing numbers that are all fictions.
func TestEmptyCorpusAbortsTheRace(t *testing.T) {
	requirePytest(t)
	cfg, _ := toyrepoRace(t, "differential.json", "patch-p.patch")
	// A corpus file whose every target names something the base tree does
	// not define: every case is dropped, and a differential over an empty
	// corpus is a comparison of nothing.
	path := filepath.Join(cfg.Repo, "corpora", "clamp-nan.json")
	if err := os.WriteFile(path, []byte(`{"cases":[{"target":"stats:nosuchfunction","args":[],"kwargs":{}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, cfg.Repo, "add", "-A")
	git(t, cfg.Repo, "commit", "-q", "-m", "empty corpus")
	commit, tree, err := gitx.Head(cfg.Repo)
	if err != nil {
		t.Fatal(err)
	}
	var intent object.Intent
	if err := loadObject(cfg.CAS, cfg.Intent, &intent); err != nil {
		t.Fatal(err)
	}
	intent.Base = object.Base{Commit: commit, Tree: tree}
	dig, canon, err := object.Digest(intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.CAS.Put(canon); err != nil {
		t.Fatal(err)
	}
	cfg.Intent = dig

	_, err = Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("a race over an empty corpus succeeded")
	}
	if !strings.Contains(err.Error(), "produced 0 cases") ||
		!strings.Contains(err.Error(), "numbers that are all fictions") {
		t.Errorf("abort message = %q", err)
	}
	// No world was created: the abort is machinery, before any candidate ran.
	if n := len(eventsOfType(t, cfg.Ledger, "world.created")); n != 0 {
		t.Errorf("world.created events = %d, want 0", n)
	}
	// And the drop is COUNTED on the record rather than left in an error
	// string: corpus.recorded precedes the abort.
	events := eventsOfType(t, cfg.Ledger, "corpus.recorded")
	if len(events) != 1 {
		t.Fatalf("corpus.recorded events = %d, want 1", len(events))
	}
	var body struct {
		Cases   int64            `json:"cases"`
		Dropped map[string]int64 `json:"dropped"`
	}
	if err := json.Unmarshal(events[0], &body); err != nil {
		t.Fatal(err)
	}
	if body.Cases != 0 || body.Dropped["target_unresolved"] != 1 {
		t.Errorf("corpus.recorded = %+v, want 0 cases and 1 unresolved target", body)
	}
}

// A provider this binary cannot materialize is refused at PRE-FLIGHT, with
// the ledger untouched. Substituting a different corpus would make every
// comparison in the race a comparison of inputs nobody pinned.
func TestUnsupportedCorpusProviderIsPreflightMachinery(t *testing.T) {
	cfg, _ := toyrepoRace(t, "differential.json", "patch-p.patch")
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "toyrepo", "policies", "differential.json"))
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(raw), `"provider":"declared"`, `"provider":"hypothesis"`, 1)
	mutated = strings.Replace(mutated, `"file":"corpora/clamp-nan.json"`, `"module":"props/mvo_props.py"`, 1)
	polDigest := object.DigestBytes([]byte(mutated))
	if _, err := cfg.CAS.Put([]byte(mutated)); err != nil {
		t.Fatal(err)
	}
	var intent object.Intent
	if err := loadObject(cfg.CAS, cfg.Intent, &intent); err != nil {
		t.Fatal(err)
	}
	intent.Policy = polDigest
	dig, canon, err := object.Digest(intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.CAS.Put(canon); err != nil {
		t.Fatal(err)
	}
	cfg.Intent = dig

	_, err = Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("a race under an unmaterializable corpus provider succeeded")
	}
	if !strings.Contains(err.Error(), `corpus provider "hypothesis"`) ||
		!strings.Contains(err.Error(), "substituting a different one would make the comparison meaningless") {
		t.Errorf("refusal = %q", err)
	}
	// LEDGER UNTOUCHED: the refusal precedes race.started.
	if n := len(eventsOfType(t, cfg.Ledger, "race.started")); n != 0 {
		t.Errorf("race.started events = %d, want 0 — a pre-flight refusal records nothing", n)
	}
}

// The corpus is materialized ONCE, on the base tree, before any world
// exists.
func assertCorpusPrecedesWorlds(t *testing.T, cfg Config, corpusDigest string) {
	t.Helper()
	events := eventsOfType(t, cfg.Ledger, "corpus.recorded")
	if len(events) != 1 {
		t.Fatalf("corpus.recorded events = %d, want exactly one per race", len(events))
	}
	var body struct {
		BaseObservation string `json:"base_observation"`
		Cases           int64  `json:"cases"`
		Corpus          string `json:"corpus"`
		Provider        string `json:"provider"`
		Tree            string `json:"tree"`
	}
	if err := json.Unmarshal(events[0], &body); err != nil {
		t.Fatal(err)
	}
	if body.Corpus != corpusDigest {
		t.Errorf("the report names corpus %s, the ledger recorded %s", corpusDigest, body.Corpus)
	}
	if body.Cases != 4 || body.Provider != policy.ProviderDeclared {
		t.Errorf("corpus.recorded = %+v", body)
	}
	// The corpus object re-digests: it is bytes in CAS with a digest in the
	// ledger, so `mvo audit` replays a differential decision like any other.
	key, err := object.CASKey(body.Corpus)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := cfg.CAS.Get(key)
	if err != nil {
		t.Fatalf("the corpus is not in CAS: %v", err)
	}
	if object.DigestBytes(raw) != body.Corpus {
		t.Error("the stored corpus does not re-digest to the recorded digest")
	}
	var corpus oracle.Corpus
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.BaseTree == "" || corpus.Schema != oracle.SchemaCorpus {
		t.Errorf("corpus object = %+v", corpus)
	}
	// The base observation is in CAS too: the anchor is inside the audited
	// closure rather than an orphan blob.
	if body.BaseObservation == "" {
		t.Fatal("corpus.recorded names no base observation")
	}
	if _, err := cfg.CAS.Get(body.BaseObservation); err != nil {
		t.Errorf("the base observation is not in CAS: %v", err)
	}
}

// AG-7 and ch. 13's untrusted-generator posture, under live test: a
// generator that can read the corpus can special-case it, so the corpus
// must never appear in anything an agent saw or produced.
func assertCorpusNeverReachedAGenerator(t *testing.T, cfg Config, res *Result, corpusDigest string) {
	t.Helper()
	needles := []string{corpusDigest, "c0001", "clamp-nan"}
	for _, w := range res.Worlds {
		for _, ref := range []struct{ name, key string }{
			{"captured patch", w.World.Patch},
			{"agent context", w.World.Context},
			{"transcript", w.World.Trace},
		} {
			if ref.key == "" {
				continue
			}
			b, err := cfg.CAS.Get(ref.key)
			if err != nil {
				t.Fatalf("read %s: %v", ref.name, err)
			}
			for _, needle := range needles {
				if strings.Contains(string(b), needle) {
					t.Errorf("world %s: the %s contains %q — the corpus reached the generator, and a generator that can read the corpus can special-case it",
						w.Digest, ref.name, needle)
				}
			}
		}
	}
}

// CORPUS VECTOR 18 — cohort starvation. A candidate silences the corpus
// runner to shrink the cohort and move a rival's unilateral counts.
//
// The expected result is SELF-ELIMINATION, and it is the property that
// makes shipping the differential without a hard cohort gate safe: no
// usable observation means every corpus_* metric is ABSENT, which fails the
// starving world's OWN corpus-complete gate, and diff_cohort_n records the
// shrinkage so every surviving number still has a named denominator.
//
// A WORLD CAN ONLY REMOVE ITSELF. What it must not be able to do is convict
// the honest sibling of anything, and that is asserted directly.
func TestCohortStarvationIsSelfElimination(t *testing.T) {
	requirePytest(t)
	cfg, fixture := toyrepoRace(t, "differential.json", "patches-starve/patch-p.patch", "patches-starve/patch-starve.patch")
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("race: %v", err)
	}
	pol, err := policy.Load(cfg.CAS, fixture.digest)
	if err != nil {
		t.Fatal(err)
	}
	var worlds []object.RecordedWorld
	var receipts []object.RecordedReceipt
	for _, w := range res.Worlds {
		worlds = append(worlds, object.RecordedWorld{Digest: w.Digest, World: w.World})
		receipts = append(receipts, w.Receipts...)
	}
	tr := Trace(pol, worlds, receipts)

	observe, _ := pol.CorpusOracle()
	obsSel := policy.Selector{ID: observe.Kind, Config: observe.Config}
	diffSpec, _ := pol.DifferentialOracle()
	diffSel := policy.Selector{ID: diffSpec.Kind, Config: diffSpec.Config}

	starved, honest := 0, 0
	for i := range tr.Candidates {
		c := &tr.Candidates[i]
		var obsRec *object.Receipt
		for _, rr := range receipts {
			if rr.Receipt.World == c.World && obsSel.Match(rr.Receipt) {
				rec := rr.Receipt
				obsRec = &rec
			}
		}
		if obsRec == nil {
			t.Fatalf("world %s produced no corpus-observe receipt", c.World)
		}
		if _, present := obsRec.Result.Metrics[policy.MetricCorpusCasesObserved]; !present {
			starved++
			// The starving world fails ITS OWN gate, and corpus-complete
			// is the gate that stops it.
			if c.Pass {
				t.Errorf("the starving world %s passed every hard gate", c.World)
			}
			if got := c.GateCell(); got != "corpus-complete@observe" {
				t.Errorf("the starving world failed at %q, want corpus-complete@observe", got)
			}
			// And it is NOT in the cohort: no comparison receipt binds it.
			for _, rr := range receipts {
				if rr.Receipt.World == c.World && diffSel.Match(rr.Receipt) {
					t.Errorf("the starving world %s is in the cohort despite an unusable observation", c.World)
				}
			}
			continue
		}
		honest++
		if !c.Pass {
			t.Errorf("the honest world %s was convicted of its sibling's behaviour: %s", c.World, c.GateCell())
		}
	}
	if starved != 1 || honest != 1 {
		t.Fatalf("starved = %d, honest = %d, want exactly one of each", starved, honest)
	}

	// The shrinkage is RECORDED rather than hidden: the surviving cohort is
	// one world, so diff_cohort_n is 1 and every other diff_* is absent —
	// which is a comparison honestly declining to be a comparison, not a
	// comparison quietly made against a smaller denominator.
	found := false
	for _, rr := range receipts {
		if !diffSel.Match(rr.Receipt) {
			continue
		}
		found = true
		if rr.Receipt.Result.Metrics[policy.MetricDiffCohortN] != 1 {
			t.Errorf("diff_cohort_n = %d, want 1", rr.Receipt.Result.Metrics[policy.MetricDiffCohortN])
		}
		if len(rr.Receipt.Result.Metrics) != 1 {
			t.Errorf("metrics = %v, want ONLY diff_cohort_n", rr.Receipt.Result.Metrics)
		}
	}
	if !found {
		t.Fatal("no comparison receipt survived the starvation")
	}
}

// CORPUS VECTOR 17 — corpus case forgery, at the world level: a candidate
// that emits records for ids the corpus does not declare.
//
// The undeclared half is DETECTED and it is detected the same way
// starvation is — the whole observation becomes unusable, so the forging
// world's metrics are absent and its own gate fails. The declared half (a
// fabricated fingerprint for a case the world never executed) is the M1f
// in-process residual, restated and NOT re-solved: this test asserts what
// is closed, and the receipt's `evidence_floor` is what says the rest is
// not.
func TestUndeclaredCaseIdsAreDetectedNotAbsorbed(t *testing.T) {
	corpus := oracle.Corpus{
		Schema: oracle.SchemaCorpus, Provider: policy.ProviderDeclared,
		Cases:   []oracle.CorpusCase{{ID: "c0001", Target: "m:f"}},
		Dropped: map[string]int64{},
	}
	const nonce = "0123456789abcdef0123456789abcdef"
	raw := "mvo-evidence/v0\t" + nonce + "\n" +
		"1\tsession_start\t{\"runner\":\"mvo_corpus/v0\"}\n" +
		"2\tcase\t{\"fp\":\"sha256:aa\",\"id\":\"c0001\",\"outcome\":\"value\",\"v\":1}\n" +
		"3\tcase\t{\"fp\":\"sha256:bb\",\"id\":\"c9999\",\"outcome\":\"value\",\"v\":2}\n" +
		"4\tsession_finish\t{\"cases\":2,\"duration_ms\":1,\"errored\":0,\"exitstatus\":0,\"opaque\":0}\n"
	obs := oracle.ParseObservation([]byte(raw), nonce, false, corpus, "mv0:fixture")
	if obs.Usable {
		t.Fatal("an observation reporting an undeclared case id was accepted")
	}
	if obs.ObservedCount() != 0 {
		t.Errorf("observed = %d, want 0 — an unusable observation yields NO metrics, not the honest prefix", obs.ObservedCount())
	}
	// And the world it came from cannot enter the cohort at all.
	res, err := oracle.Reduce(oracle.DifferentialInputs{
		Corpus: corpus, CorpusDigest: "mv0:c", BaseTree: "git:b",
		Base: oracle.Observation{Usable: true, Cases: map[string]oracle.CaseObservation{
			"c0001": {ID: "c0001", Outcome: oracle.OutcomeValue, FP: "sha256:aa"},
		}},
		Spec: policy.Oracle{Kind: policy.KindCorpusDifferential, Config: "mv0:cfg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Receipts) != 0 {
		t.Error("a forging world produced a comparison receipt")
	}
}

// RULE 1b REPLACES SELECT, NEVER REJECT.
//
// M2a's precedence table makes rule 1 (`on_all_worlds_failed_machinery`)
// the only REJECT-replacer, and escalate() was evaluating the split ABOVE
// the `PassCount == 0` guard — so a race in which every world failed a hard
// gate was recorded as ESCALATE with "the shared evidence does not say which
// behavior is intended". The shared evidence said exactly that, about all of
// them: they failed the suite.
//
// Here both worlds split cleanly on the corpus AND fail their suite gate.
// The verdict must be REJECT, and the world must be told why it failed.
func TestSplitDoesNotReplaceAReject(t *testing.T) {
	pol := splitPolicyWithSuiteGate(t, 2)
	wa, ra := splitWorld(t, pol, "mv0:aa11", 2, 2, 1, "mv0:aa11", "c0001")
	wb, rb := splitWorld(t, pol, "mv0:bb22", 2, 2, 1, "mv0:bb22", "c0001")
	// Both worlds fail the suite gate: red tests, a real split, no winner.
	ra = append(ra, failingSuiteReceipt(t, pol, "mv0:aa11", wa.World))
	rb = append(rb, failingSuiteReceipt(t, pol, "mv0:bb22", wb.World))

	tr := Trace(pol, []object.RecordedWorld{wa, wb}, append(ra, rb...))
	if tr.PassCount != 0 {
		t.Fatalf("pass count = %d, want 0: the fixture must have both worlds failing", tr.PassCount)
	}
	if tr.Type != TypeReject {
		t.Fatalf("type = %s (rule %q: %s), want REJECT — rule 1b replaces SELECT, and there was no SELECT to replace",
			tr.Type, tr.Escalation.Rule, tr.Escalation.Detail)
	}
	if tr.Escalation.Rule == RuleOnBehavioralSplit {
		t.Fatalf("the split replaced a REJECT: %s", tr.Escalation.Detail)
	}
	if !strings.Contains(tr.Rationale, "status-pass@suite") {
		t.Errorf("the rationale does not name the gate both worlds failed: %q", tr.Rationale)
	}
}

// splitPolicyWithSuiteGate is splitPolicy plus a suite oracle and a
// status-pass gate ordered AFTER corpus-complete — the shipped menu.json
// shape, and the one that made the precedence bug reachable.
func splitPolicyWithSuiteGate(t *testing.T, threshold int) policy.Policy {
	t.Helper()
	p := object.PolicyV1{
		Schema: object.SchemaPolicyV1, Name: "split-suite",
		Oracles: []object.OracleSpec{
			{Name: "diff", Kind: policy.KindCorpusDifferential, Argv: []string{}, Args: []string{}},
			{Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{}},
			{Name: "observe", Kind: policy.KindCorpusObserve, Argv: []string{}, Args: []string{},
				Corpus: object.CorpusSpec{Provider: policy.ProviderDeclared, File: "corpora/c.json"}},
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{}, Args: []string{}},
		},
		HardGates: []object.GateSpec{
			{Gate: policy.GatePathsUnmodified, Oracle: "guard", Basis: object.BasisConstruction},
			{Gate: policy.GateCorpusComplete, Oracle: "observe",
				Basis: object.BasisConstruction, Scope: policy.ScopeRace},
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking: []string{policy.KeyGatePass},
		Escalation: object.EscalationSpec{
			RequireEvidence: []string{}, OnBehavioralSplit: threshold,
			OnAllWorldsFailedMachinery: true, OnRankingTie: true,
		},
		Paths:      object.PathSpec{Protected: []string{}, Harness: []string{}},
		Invariants: []object.InvariantSpec{},
	}
	if err := policy.Validate(p); err != nil {
		t.Fatalf("policy does not validate: %v", err)
	}
	pol, err := policy.Compile("mv0:split-suite", p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return pol
}

// failingSuiteReceipt is a world's honest red suite: the tests ran and two
// of them failed. It is a FAILED GATE, not machinery — which is what keeps
// rule 1 out of the way and leaves the REJECT to stand.
func failingSuiteReceipt(t *testing.T, pol policy.Policy, dig string, w object.World) object.RecordedReceipt {
	t.Helper()
	suite, _ := pol.OracleByName("suite")
	return object.RecordedReceipt{
		Digest: "mv0:r3" + dig[4:],
		Receipt: object.Receipt{
			Schema: object.SchemaReceipt, World: dig,
			Oracle:    object.OracleRef{ID: suite.Kind, Version: "v0", Config: suite.Config},
			Execution: object.Execution{Argv: []string{"python3"}, ExitCode: 1, EvidenceRegime: object.RegimeStreamed},
			Result: object.Result{Status: "fail", Tools: map[string]string{"pytest": "8.4.0"},
				Artifacts: []string{},
				Metrics: map[string]int64{
					policy.MetricTestsTotal: 8, policy.MetricTestsPassed: 6,
					policy.MetricTestsFailed: 2, policy.MetricTestsErrored: 0,
					policy.MetricTestsSkipped: 0,
				}},
			Freshness: object.Freshness{Basis: object.BasisConstruction,
				ValidFor: object.ValidFor{Tree: w.Tree, Env: w.Env}},
			RecheckTier: "V1-replayable", Family: policy.FamilySuite, Inputs: object.NoInputs(),
		},
	}
}

// A USABLE-BUT-INCOMPLETE OBSERVATION IS NOT A COHORT MEMBER.
//
// The cohort is "every world whose corpus-observe receipt is `pass` with a
// usable observation", and the `pass` half was missing. A stream carrying a
// header, a session_start, a SUBSET of the declared ids and a session_finish
// breaks none of the usability rules, so it parsed as usable while the
// oracle's own status was `fail` — and because the comparison denominator is
// an intersection over every member, that already-eliminated world deleted
// the distinguishing case from every honest sibling. The race then degraded
// to a blind ranking tie that told the maintainer nothing.
//
// Reduce is the wrong place to fix it (a short observation is a legitimate
// input to a comparison that was never asked to include it), so the
// assertion is on the ORCHESTRATOR's cohort assembly.
func TestCohortExcludesAWorldWhoseObserveReceiptFailed(t *testing.T) {
	corpus := oracle.Corpus{
		Schema: oracle.SchemaCorpus, Provider: policy.ProviderDeclared,
		Cases: []oracle.CorpusCase{
			{ID: "c0001", Target: "m:f"}, {ID: "c0002", Target: "m:f"},
		},
		Dropped: map[string]int64{},
	}
	full := func(fp1 string) oracle.Observation {
		return oracle.Observation{Usable: true, Complete: true, Cases: map[string]oracle.CaseObservation{
			"c0001": {ID: "c0001", Outcome: oracle.OutcomeValue, FP: fp1},
			"c0002": {ID: "c0002", Outcome: oracle.OutcomeValue, FP: "sha256:same"},
		}}
	}
	// The saboteur: usable, well-formed, and short by one declared case.
	short := oracle.Observation{Usable: true, Complete: true, Cases: map[string]oracle.CaseObservation{
		"c0002": {ID: "c0002", Outcome: oracle.OutcomeValue, FP: "sha256:same"},
	}}

	r := &raceRun{slots: []slot{
		{dig: "mv0:aa", obsOK: true, obsPass: true, obs: full("sha256:one")},
		{dig: "mv0:bb", obsOK: true, obsPass: true, obs: full("sha256:two")},
		// obsPass is false because corpus_cases_observed (1) < total (2):
		// the observe oracle already returned `fail` for this world.
		{dig: "mv0:cc", obsOK: true, obsPass: false, obs: short},
	}}
	members := r.cohort()
	if len(members) != 2 {
		t.Fatalf("cohort = %d members, want 2: a world that failed its own corpus-complete gate is not in the comparison", len(members))
	}
	for _, m := range members {
		if m.World == "mv0:cc" {
			t.Fatal("the eliminated world entered the cohort")
		}
	}
	// And the distinguishing case survives for the honest siblings.
	res, err := oracle.Reduce(oracle.DifferentialInputs{
		Corpus: corpus, CorpusDigest: "mv0:c", BaseTree: "git:b",
		Base:    full("sha256:one"),
		Members: members,
		Spec:    policy.Oracle{Kind: policy.KindCorpusDifferential, Config: "mv0:cfg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Compared != 2 || res.Classes != 2 || res.FirstDistinguishing != "c0001" {
		t.Fatalf("compared=%d classes=%d first=%q, want 2/2/c0001 — the saboteur must not shrink the denominator",
			res.Compared, res.Classes, res.FirstDistinguishing)
	}
}

// A MEMBER THAT COMPARES NOTHING COMPARED NOTHING.
//
// This one is fully compliant on its own gate: it observes every declared
// case, so corpus_cases_observed == corpus_cases_total and corpus-complete
// PASSES — and it answers `opaque` (or `error`, which the shipped runner
// emits for any unresolvable target) everywhere, which collapses the
// intersection to zero for the WHOLE race. Nothing gates or escalates on
// diff_cases_compared, so the split simply vanished.
//
// A member whose contribution is zero is dropped and the drop is recorded.
// The residual — a member that contributes SOME cases still shrinks the
// denominator for everyone — is visible in member_cases_comparable and
// stated as an open vector rather than described as closed.
func TestOpaqueEverywhereMemberCannotDenyTheComparison(t *testing.T) {
	corpus := oracle.Corpus{
		Schema: oracle.SchemaCorpus, Provider: policy.ProviderDeclared,
		Cases: []oracle.CorpusCase{
			{ID: "c0001", Target: "m:f"}, {ID: "c0002", Target: "m:f"},
		},
		Dropped: map[string]int64{},
	}
	full := func(fp1 string) oracle.Observation {
		return oracle.Observation{Usable: true, Complete: true, Cases: map[string]oracle.CaseObservation{
			"c0001": {ID: "c0001", Outcome: oracle.OutcomeValue, FP: fp1},
			"c0002": {ID: "c0002", Outcome: oracle.OutcomeValue, FP: "sha256:same"},
		}}
	}
	blind := oracle.Observation{Usable: true, Complete: true, Cases: map[string]oracle.CaseObservation{
		"c0001": {ID: "c0001", Outcome: oracle.OutcomeOpaque, Type: "Decimal"},
		"c0002": {ID: "c0002", Outcome: oracle.OutcomeError},
	}}

	res, err := oracle.Reduce(oracle.DifferentialInputs{
		Corpus: corpus, CorpusDigest: "mv0:c", BaseTree: "git:b",
		Base: full("sha256:one"),
		Members: []oracle.CohortMember{
			{World: "mv0:aa", Obs: full("sha256:one")},
			{World: "mv0:bb", Obs: full("sha256:two")},
			{World: "mv0:cc", Obs: blind},
		},
		Spec: policy.Oracle{Kind: policy.KindCorpusDifferential, Config: "mv0:cfg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Compared != 2 || res.Classes != 2 || res.FirstDistinguishing != "c0001" {
		t.Fatalf("compared=%d classes=%d first=%q, want 2/2/c0001 — one blind member must not delete the comparison",
			res.Compared, res.Classes, res.FirstDistinguishing)
	}
	if len(res.Receipts) != 2 {
		t.Fatalf("receipts = %d, want 2: a member that compared nothing is not a member", len(res.Receipts))
	}
	for _, rec := range res.Receipts {
		if rec.World == "mv0:cc" {
			t.Error("the blind member received a comparison receipt")
		}
	}
	// The exclusion is RECORDED, with each member's contribution beside it:
	// a denominator that shrank must never shrink anonymously.
	var report struct {
		Excluded              []string         `json:"excluded"`
		MemberCasesComparable map[string]int64 `json:"member_cases_comparable"`
		Provider              string           `json:"provider"`
	}
	if err := json.Unmarshal(res.Report, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Excluded) != 1 || report.Excluded[0] != "mv0:cc" {
		t.Errorf("excluded = %v, want [mv0:cc]", report.Excluded)
	}
	if report.MemberCasesComparable["mv0:cc"] != 0 || report.MemberCasesComparable["mv0:aa"] != 2 {
		t.Errorf("member_cases_comparable = %v, want cc:0 aa:2", report.MemberCasesComparable)
	}
	if report.Provider != policy.ProviderDeclared {
		t.Errorf("provider = %q, want %q: which provider produced the corpus is what a reader weighs the comparison by",
			report.Provider, policy.ProviderDeclared)
	}
}

// THE CORPUS IS NOT MOUNTED INTO A CANDIDATE WORLD DURING PHASE A, and the
// assertion is on the two facts that made it false.
//
// Fact one: openOptsFor handed EVERY world the race's shared corpus
// directory, and the keeper is opened before Adapter.Start runs the agent
// inside it (M1c decision 4: one long-lived keeper per world, agent and
// oracle share it). Under T1 a `docker inspect` of the candidate keepers
// during a race showed `<raceDir>/corpus -> /mvo/corpus ro=true` on every
// one of them, for the whole of generation — so the generating agent could
// read the inputs its output would be compared on. The mount is now per
// world and its directory is EMPTY until phase A has joined.
//
// Fact two: the corpus lived at <raceDir>/corpus, the parent-sibling of
// every world's cwd, so `../corpus/corpus.json` from an ordinary pytest
// collection walked out with the whole pinned corpus. It now lives outside
// the worlds tree entirely. At T0 that is defence in depth and nothing more
// — same uid, same filesystem — which is why decision 13's guarantee is
// restated as "not DELIVERED to the generating agent", not "not REACHABLE
// by it", and recorded as an open vector.
func TestPhaseADeliversNoCorpusToAWorld(t *testing.T) {
	ws := t.TempDir()
	r := &raceRun{
		cfg:        Config{WorldsDir: filepath.Join(ws, "worlds")},
		raceDir:    filepath.Join(ws, "worlds", "race-1"),
		corpusRoot: filepath.Join(ws, "corpora", "race-1"),
		ev:         evidenceSetup{regime: object.RegimeStreamed},
	}
	r.corpusDir = filepath.Join(r.corpusRoot, "base")
	if err := os.MkdirAll(r.corpusDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The corpus exists on disk while agents run — phase 0 precedes phase A
	// — which is exactly why WHERE it is matters.
	if _, err := oracle.WriteCorpus(r.corpusDir, []byte(`{"schema":"x"}`)); err != nil {
		t.Fatal(err)
	}

	// It is not inside the worlds tree, so no relative walk from a world's
	// cwd reaches it as a sibling.
	if strings.HasPrefix(r.corpusRoot, r.cfg.WorldsDir+string(filepath.Separator)) {
		t.Errorf("the corpus root %s is inside the worlds tree %s", r.corpusRoot, r.cfg.WorldsDir)
	}
	worldDir := filepath.Join(r.raceDir, "001")
	if _, err := os.Stat(filepath.Join(worldDir, "..", "corpus", oracle.CorpusFile)); err == nil {
		t.Error("the corpus is reachable from a world's cwd by ../corpus/corpus.json")
	}

	// This world's own corpus directory exists at open time (a T1
	// container's mounts are fixed then) and is EMPTY throughout phase A.
	wd, err := r.worldCorpusDir(1)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("world 1's corpus mount is not empty during phase A: %v", entries)
	}
	opts := r.openOptsFor("/ev", "/scratch", wd)
	if opts.CorpusDir != wd {
		t.Errorf("openOptsFor mounted %q, want this world's own directory %q", opts.CorpusDir, wd)
	}
	if opts.CorpusDir == r.corpusDir {
		t.Error("a candidate world was handed phase 0's own corpus directory")
	}

	// After the barrier the control plane delivers a per-world copy, and it
	// is that copy the observe rung reads.
	r.slots = []slot{{dig: "mv0:aa", corpusDir: wd}}
	plan := &corpusPlan{hostFile: filepath.Join(r.corpusDir, oracle.CorpusFile)}
	plan.digest = object.DigestBytes([]byte(`{"schema":"x"}`))
	if err := r.deliverCorpus(plan, []int{0}); err != nil {
		t.Fatalf("deliverCorpus: %v", err)
	}
	if r.slots[0].corpusFile == "" {
		t.Fatal("no corpus was delivered to the world")
	}
	if got := filepath.Dir(r.slots[0].corpusFile); got != wd {
		t.Errorf("the delivered corpus is at %s, want this world's own directory %s", got, wd)
	}

	// And a copy that changes under the race aborts as MACHINERY, naming the
	// digests — never a failing world, because a world handed an altered
	// file is a victim rather than a suspect.
	r.corpus = &corpusPlan{digest: plan.digest}
	if err := r.checkCorpusFile(&r.slots[0], "before"); err != nil {
		t.Fatalf("an untouched corpus was refused: %v", err)
	}
	if err := os.Chmod(r.slots[0].corpusFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.slots[0].corpusFile, []byte(`{"schema":"poisoned"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err = r.checkCorpusFile(&r.slots[0], "after")
	if err == nil {
		t.Fatal("a rewritten corpus passed the control plane's own check")
	}
	for _, want := range []string{"machinery", "no candidate is judged on it", plan.digest} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}
