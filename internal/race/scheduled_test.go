package race

// PHASE B UNDER THE SCHEDULER, end to end through the real orchestrator —
// worktrees, the script adapter and the fake interpreter, no pytest, no
// plugin, no docker and no agent CLI.
//
// The claim under test is M2b decision 13's, and it is the compatibility
// proof the whole block rests on: UNDER AN UNBOUNDED BUDGET THE ADAPTIVE
// SCHEDULER BUYS WHAT THE FIXED LADDER BOUGHT AND DECIDES IDENTICALLY.
// Shipping a scheduler that cannot be shown to be inert where it should be
// inert is how a research result becomes an artifact of its harness.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/schedule"
)

// schedulablePolicy is ladderPolicy's shape with an ALLOCATION-INSENSITIVE
// ranking: wall_ms_asc sums a world's counted receipts, so under adaptive
// allocation the world we verified LEAST would win the tiebreak (decision
// 15), and a policy declaring it is not scheduled adaptively at all.
func schedulablePolicy(python string) object.PolicyV1 {
	return object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "schedulable",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{python, "-m", "pytest"}, Args: []string{}},
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{python, "-m", "pytest"}, Args: []string{}},
		},
		HardGates: []object.GateSpec{
			{Gate: policy.GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateCollectedNotBelow, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{policy.KeyGatePass, policy.KeyTestsPassedDesc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}, OnAllWorldsFailedMachinery: true},
	}
}

// collector is a trace Sink that keeps what the scheduler authored. It
// records nothing to the ledger: the recording half of M2b is a different
// owner, and the seam is what this test exercises.
type collector struct {
	started  []schedule.Started
	steps    []schedule.Step
	finished []schedule.Finished
	skipped  []schedule.Skipped
}

func (c *collector) Started(s schedule.Started) error { c.started = append(c.started, s); return nil }
func (c *collector) Step(s schedule.Step) error       { c.steps = append(c.steps, s); return nil }
func (c *collector) Finished(f schedule.Finished, sk []schedule.Skipped) error {
	c.finished = append(c.finished, f)
	c.skipped = append(c.skipped, sk...)
	return nil
}

// THE NULL CASE, AND IT MUST BE NULL. Same repository, same patches, same
// policy, unbounded budget: the two arms buy the same receipts per world, in
// the same ladder order, and record the same decision byte for byte.
func TestScheduledPhaseBMatchesTheFixedLadder(t *testing.T) {
	patches := map[string]string{
		"a-fix.patch":   fixPatch,   // collects 2 → delta 0 → the suite runs
		"b-nofix.patch": noFixPatch, // collects 1 → delta -1 → the ladder stops
	}
	run := func(arm string) *Result {
		t.Helper()
		cfg := newLadderConfig(t, schedulablePolicy(fakePython(t, true)), patches)
		cfg.Parallel = 2
		cfg.Schedule = arm
		res, err := Run(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Run(%s): %v", arm, err)
		}
		return res
	}
	fixed := run(schedule.ScheduleFixed)
	adaptive := run(schedule.ScheduleAdaptive)

	if fixed.Decision.Type != adaptive.Decision.Type {
		t.Errorf("decision type: fixed %s, adaptive %s", fixed.Decision.Type, adaptive.Decision.Type)
	}
	// The two races run in two temporary repositories, so world digests
	// differ by construction: the comparison is over the DECISION's shape
	// with each digest replaced by the candidate ordinal that produced it.
	if f, a := normalizeDigests(fixed), normalizeDigests(adaptive); f.rationale != a.rationale {
		t.Errorf("rationale differs:\n fixed: %s\nadapt: %s", f.rationale, a.rationale)
	} else if !reflect.DeepEqual(f.subject, a.subject) {
		t.Errorf("subject: fixed %v, adaptive %v", f.subject, a.subject)
	}
	// The evidence itself: same rungs, same order, per world.
	kinds := func(res *Result) map[int][]string {
		out := map[int][]string{}
		for _, w := range res.Worlds {
			for _, rr := range w.Receipts {
				out[w.Ordinal] = append(out[w.Ordinal], rr.Receipt.Oracle.ID)
			}
		}
		return out
	}
	if f, a := kinds(fixed), kinds(adaptive); !reflect.DeepEqual(f, a) {
		t.Errorf("purchases per world: fixed %v, adaptive %v", f, a)
	}
	// And the short-circuit is preserved verbatim: the world that failed the
	// collect gate never paid for the suite.
	for _, w := range adaptive.Worlds {
		if w.Ordinal == 2 && len(w.Receipts) != 1 {
			t.Errorf("the eliminated world bought %d rungs, want 1 — the ladder must still short-circuit", len(w.Receipts))
		}
	}
}

// normalized is one race's decision with every world digest replaced by the
// ordinal of the candidate that produced it, so two races over two temporary
// repositories are comparable at all.
type normalized struct {
	subject   []string
	rationale string
}

func normalizeDigests(res *Result) normalized {
	byDigest := map[string]string{}
	for _, w := range res.Worlds {
		byDigest[w.Digest] = fmt.Sprintf("world#%d", w.Ordinal)
	}
	out := normalized{rationale: res.Decision.Rationale}
	for _, s := range res.Decision.Subject {
		out.subject = append(out.subject, byDigest[s])
	}
	for dig, name := range byDigest {
		out.rationale = strings.ReplaceAll(out.rationale, dig, name)
	}
	return out
}

// The trace seam: every receipt phase B recorded was named as CHOSEN by some
// step, schedule.started brackets them, and the stop clause is recorded. The
// scheduler AUTHORS this; recording it to the ledger belongs to the trace
// owner, and a nil sink changes nothing about the allocation.
func TestScheduleTraceIsAuthoredForEveryPurchase(t *testing.T) {
	cfg := newLadderConfig(t, schedulablePolicy(fakePython(t, true)), map[string]string{
		"a-fix.patch":   fixPatch,
		"b-nofix.patch": noFixPatch,
	})
	c := &collector{}
	cfg.ScheduleTrace = c
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(c.started) != 1 || len(c.finished) != 1 {
		t.Fatalf("started %d, finished %d — the trace must bracket the allocation", len(c.started), len(c.finished))
	}
	if c.started[0].Schedule != schedule.ScheduleAdaptive {
		t.Errorf("recorded arm = %q, want %q", c.started[0].Schedule, schedule.ScheduleAdaptive)
	}
	if c.started[0].Budget.MaxOracleMS != 0 {
		t.Errorf("recorded budget = %d, want 0 (unbounded)", c.started[0].Budget.MaxOracleMS)
	}
	if len(c.started[0].Constants.ExecutorBP) == 0 || len(c.started[0].Constants.RedundancyBP) == 0 {
		t.Error("schedule.started recorded no scheduler constants; the allocation is not auditable without them")
	}
	chosen := map[string]bool{}
	for _, s := range c.steps {
		if len(s.Considered) == 0 {
			t.Errorf("step %d considered nothing", s.Step)
		}
		for _, ch := range s.Chosen {
			chosen[ch.World+"/"+ch.Oracle] = true
		}
	}
	for _, w := range res.Worlds {
		for _, rr := range w.Receipts {
			name := ""
			for _, o := range []string{"collect", "suite"} {
				if spec, ok := mustPolicy(t, cfg).OracleByName(o); ok && spec.Config == rr.Receipt.Oracle.Config {
					name = o
				}
			}
			if name == "" {
				t.Fatalf("receipt %s matches no declared instance", rr.Digest)
			}
			if !chosen[w.Digest+"/"+name] {
				t.Errorf("receipt %s (%s on %s) was recorded but no step chose it", rr.Digest, name, w.Digest)
			}
		}
	}
	if c.finished[0].Stop == "" {
		t.Error("schedule.finished recorded no stop clause")
	}
	if c.finished[0].Bought != len(chosen) {
		t.Errorf("finished.bought = %d, want %d", c.finished[0].Bought, len(chosen))
	}
	// Decision 4's one honest corner, REPORTED. Both fields were dead in
	// every real race — `Finish()` never set either — so the caveat the
	// safety claim admits was rendered by a warning nothing could trigger
	// and the purchase-law assertion was recorded as empty whether or not it
	// held. This race buys everything, so the honest value is false; the
	// point is that the field is now written from the scheduler's own state
	// rather than left at its zero value.
	if c.finished[0].RankingIncomplete {
		t.Error("finished.ranking_incomplete = true on a race that bought every rung of every world")
	}
	if c.finished[0].Violation != "" {
		t.Errorf("finished.violation = %q, and it must always be empty", c.finished[0].Violation)
	}
}

// A STARVED RACE REPORTS THAT ITS RANKING IS UNSETTLED. A world dropped from
// the pass set because nobody bought its gate is not a world that lost: the
// receipt is missing, so its position in the ranking is unknown, and
// `schedule.finished.ranking_incomplete` is the field that says so.
func TestStarvedRaceRecordsRankingIncomplete(t *testing.T) {
	patches := map[string]string{"a-fix.patch": fixPatch, "b-nofix.patch": noFixPatch}
	cfg := newLadderConfig(t, schedulablePolicy(fakePython(t, true)), patches)
	// One millisecond buys nothing beyond the first dispatch: every rung is
	// unpriced on a fresh workspace, so the opening purchase goes out and
	// empties the pool, and every later one is refused.
	pol := schedulablePolicy(fakePython(t, true))
	cfg.Intent = seedIntentBudget(t, cfg.CAS, cfg.Repo, pol, len(patches), 1)
	c := &collector{}
	cfg.ScheduleTrace = c
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(c.finished) != 1 {
		t.Fatalf("finished %d times", len(c.finished))
	}
	if c.finished[0].Stop != schedule.StopBudget {
		t.Fatalf("stop = %q, want %q", c.finished[0].Stop, schedule.StopBudget)
	}
	if !c.finished[0].RankingIncomplete {
		t.Error("a race that stopped starved recorded ranking_incomplete = false")
	}
	if c.finished[0].Violation != "" {
		t.Errorf("finished.violation = %q, and it must always be empty", c.finished[0].Violation)
	}
}

// A policy whose ranking is ALLOCATION-SENSITIVE is never scheduled
// adaptively: `wall_ms_asc` would hand the tiebreak to the world we verified
// least. The library falls back to the exhaustive ladder and RECORDS the arm
// it ran, so the fallback is visible rather than silent; `mvo race` refuses
// at pre-flight with the key named and the operator's two outs.
func TestAllocationSensitiveRankingFallsBackToTheFixedLadder(t *testing.T) {
	compile := func(p object.PolicyV1) policy.Policy {
		t.Helper()
		b, err := object.Canonical(p)
		if err != nil {
			t.Fatalf("canonical: %v", err)
		}
		compiled, err := policy.Decode(b)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return compiled
	}

	// THE STEERABLE SHAPE: wall_ms_asc plus an oracle the policy COUNTS
	// (require_evidence names it) that no hard gate backs. A world can
	// decline `guard`, keep its pass, carry a smaller wall_ms sum than the
	// sibling that bought it, and win the tiebreak by having been verified
	// least. This is the one decision 15 refuses.
	steerable := schedulablePolicy("python3")
	steerable.Oracles = append(steerable.Oracles, object.OracleSpec{
		Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{},
	})
	steerable.Escalation.RequireEvidence = []string{"guard"}
	steerable.Ranking = []string{policy.KeyGatePass, policy.KeyWallMSAsc}
	compiled := compile(steerable)
	if keys := compiled.AllocationSensitiveKeys(); len(keys) != 1 || keys[0] != policy.KeyWallMSAsc {
		t.Fatalf("allocation-sensitive keys = %v, want [%s]", keys, policy.KeyWallMSAsc)
	}
	if ung := compiled.UngatedEvidence(); len(ung) != 1 || ung[0] != "guard" {
		t.Fatalf("ungated evidence = %v, want [guard]", ung)
	}
	arm, named := scheduleArm(Config{}, compiled)
	if arm != schedule.ScheduleFixed {
		t.Errorf("arm = %q for an allocation-sensitive ranking, want %q", arm, schedule.ScheduleFixed)
	}
	if len(named) != 1 || named[0] != policy.KeyWallMSAsc {
		t.Errorf("fallback named %v, want the offending key", named)
	}

	// THE SHAPE THAT ONLY LOOKS STEERABLE, and it is M0's own: wall_ms_asc
	// over a policy whose every counted selector is backed by a hard gate.
	// Withholding any of those receipts makes the gate's metric absent, the
	// gate fails, and the world leaves the pass set — where no ranking key
	// compares it. There is no receipt a PASSING world can be missing, so
	// the key cannot be steered and refusing the policy would brick
	// `mvo race --oracle-cmd` to prevent an attack its shape forbids.
	gatedOnly := schedulablePolicy("python3")
	gatedOnly.Ranking = []string{policy.KeyGatePass, policy.KeyWallMSAsc}
	safe := compile(gatedOnly)
	if keys := safe.AllocationSensitiveKeys(); len(keys) != 1 {
		t.Fatalf("allocation-sensitive keys = %v, want [%s]", keys, policy.KeyWallMSAsc)
	}
	if ung := safe.UngatedEvidence(); len(ung) != 0 {
		t.Fatalf("ungated evidence = %v on a fully gated policy, want none", ung)
	}
	if arm, named := scheduleArm(Config{}, safe); arm != schedule.ScheduleAdaptive || len(named) != 0 {
		t.Errorf("a fully gated wall_ms_asc policy runs under %q naming %v, want %q and nothing",
			arm, named, schedule.ScheduleAdaptive)
	}

	// The shipped default is NOT allocation-sensitive, so nothing about the
	// default configuration changes.
	def, err := object.Canonical(policy.Default())
	if err != nil {
		t.Fatalf("canonical default: %v", err)
	}
	defPol, err := policy.Decode(def)
	if err != nil {
		t.Fatalf("decode default: %v", err)
	}
	if keys := defPol.AllocationSensitiveKeys(); len(keys) != 0 {
		t.Errorf("the shipped default declares allocation-sensitive keys %v", keys)
	}
	if arm, _ := scheduleArm(Config{}, defPol); arm != schedule.ScheduleAdaptive {
		t.Errorf("the shipped default runs under %q, want %q", arm, schedule.ScheduleAdaptive)
	}
}

// mustPolicy loads the compiled policy a race config pins.
func mustPolicy(t *testing.T, cfg Config) policy.Policy {
	t.Helper()
	var intent object.Intent
	if err := loadObject(cfg.CAS, cfg.Intent, &intent); err != nil {
		t.Fatalf("load intent: %v", err)
	}
	pol, err := policy.Load(cfg.CAS, intent.Policy)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	return pol
}
