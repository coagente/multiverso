package schedule

// The contract seam between the ALLOCATION RULE and the INSTRUMENTATION.
//
// M2b splits this package along one line and it is the line decision 1
// draws. The allocation rule — the frontier, the flip lookahead, the cost
// fit, the correlation discount, the budget shares, the dispatch loop —
// lives in schedule.go / ladder.go / value.go / cost.go / correlate.go /
// budget.go. The instrumentation — trace.go and waste.go — turns what the
// rule decided into canonical ledger payloads, reads them back, and derives
// evidence waste from them. This file holds only what both halves must agree
// on.

import (
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// DecideFn is decision 1's seam: the scheduler holds a REFERENCE to the
// decision rule, never a copy. internal/race supplies race.Decide; the pure
// tests supply a counting fake.
//
// Two reasons, and the second is the one that matters. Mechanically, a
// function type breaks the import cycle that would otherwise force the
// decision logic into a shared package. Structurally, it makes a second,
// divergent copy of the decision rule IMPOSSIBLE TO WRITE here — a scheduler
// with its own idea of what passes is a scheduler that will eventually
// allocate against a decision nobody made, and the failure would be silent
// because the recorded decision would still be correct. The scheduler asks;
// it does not model.
type DecideFn func(policy.Policy, []object.RecordedWorld, []object.RecordedReceipt) object.Decision

// Basis labels one trace row's PURPOSE (decision 11). A `research` row is a
// purchase whose stated point is to influence no decision — `mvo race
// --collect-inert` buying the decision-inert rungs M2a ships unranked, so
// that M2d can correlate them against ground truth before anyone ranks by
// them — and the waste metric EXCLUDES those rows by construction, because a
// purchase that cannot matter is 100 % waste under PRD §11's definition and
// counting it would make the metric meaningless.
const (
	BasisDecision = "decision"
	BasisResearch = "research"
)

// CostBasisFit is the trace-row discriminator for a row priced from a fitted
// coefficient. Its counterpart is cost.go's CostBasisDeclaredRank, and the
// two never render alike: a millisecond figure nobody measured must not
// appear beside one somebody did.
const CostBasisFit = "fit"

// Stop clauses (decision 9). Recorded on schedule.finished, so a reader
// learns whether the race stopped because nothing was left to learn or
// because the money ran out — opposite facts about a decision, and only one
// of them is a statement about the candidates.
const (
	// StopRanking: no candidate in the pass set is missing a receipt for an
	// oracle any ranking key reads. It is decision 4's caveat made
	// operational — withholding monotonicity holds for the PASS SET, not for
	// the RANKING.
	StopRanking = "S-ranking"
	// StopFrontier: every remaining frontier purchase has value_bp == 0.
	StopFrontier = "S-frontier"
	// StopBudget is the STARVED stop: no remaining frontier purchase is
	// affordable. It is the interesting one, because a starved race that
	// records REJECT claims "these candidates are bad" when the truth is "we
	// never bought the evidence" — which is what on_evidence_incomplete
	// (decision 14) exists to say instead.
	StopBudget = "S-budget"
	// StopEmpty: the frontier is empty.
	StopEmpty = "S-empty"
)

// Schedule arms recorded in schedule.started, so a ledger says which arm
// produced it without anybody reconstructing a command line. Under
// max_oracle_ms == 0 and the shipped default policy the two are provably
// equivalent (decision 13), which is a test rather than a claim — and the
// comparison harness (scripts/schedule-compare.sh) races the same fixture
// both ways at matched budget to show it.
// ScheduleFixedBudget is M2b1's arm: the depth-first ladder GIVEN THE SAME
// MONEY. The label is not reused, and that is decision 2 rather than
// fastidiousness. `schedule.started.schedule` records the LABEL and not the
// semantics, so a "fixed" in an old ledger and a "fixed" in a new one would
// mean different things depending on which binary wrote it and no reader
// could tell them apart — the honesty rule pointed at our own
// instrumentation. `--schedule=fixed` therefore keeps its meaning exactly:
// the UNBUDGETED exhaustive M1 ladder on the M1c worker pool, unchanged and
// untraced.
//
// The experiment's reference arm is `fixed-budget` with max_oracle_ms = 0,
// not `fixed`: same evidence set (an unbounded budget buys every rung), but
// it emits a trace, so evidence waste, spend and the cost-table snapshot are
// computable for the reference too.
const (
	ScheduleAdaptive    = "adaptive"
	ScheduleFixed       = "fixed"
	ScheduleFixedBudget = "fixed-budget"
)

// Budget bases (M2b1 decision 5b): what the pool is CHARGED per purchase.
//
// Under BudgetBasisActual — the default, and the honest one — the pool is
// charged the receipt's measured wall_ms, and two replicates at one budget
// can buy different things because the machine was busier. Actual wall-clock
// is not in M2b decision 13's determinism tuple, which is why M2b observed
// the same budget producing different decisions run to run.
//
// Under BudgetBasisPredicted the pool is charged the PINNED COST TABLE's
// prediction for that purchase, which puts spend back inside the tuple:
// given (policy, worlds, receipts, cost table, budget, constants, order) the
// allocation is a pure function, both arms are exactly replayable, and every
// difference between the arms is allocation rather than jitter. Its cost is
// stated rather than hidden: it measures allocation under a MODEL of cost,
// and the model's error is already recorded as the calibration residual.
const (
	BudgetBasisActual    = "actual"
	BudgetBasisPredicted = "predicted"
)

// Race modes recorded in schedule.started (decision 11). ModeCollectInert is
// `mvo race --collect-inert`: it buys decision-inert rungs on worlds that
// are still alive and marks each row BasisResearch. Off by default. It is
// the one place in this design where evidence is bought that cannot matter,
// and it is labelled as such rather than smuggled in as diligence.
const (
	ModeDecision     = "decision"
	ModeCollectInert = "collect-inert"
)

// Redundancy tier names. The VALUES are correlate.go's (RedNearDuplicate …);
// these are the keys the tier table is recorded under in schedule.started,
// so a reader of an old trace can see which tier produced a discount after
// the numbers move.
const (
	TierNearDuplicate = "near-duplicate"
	TierSameSignal    = "same-signal"
	TierSamePrior     = "same-prior"
	TierIndependent   = "independent"
)

// AdaptiveRuleDefault is the rule THIS BINARY allocates by for
// `--schedule=adaptive` when no `--selector` is given (M2b.2 decision 6).
//
// It is a property of the BUILD, not of a run: `--selector=voc2` records
// `selector: "voc2"` on that race while `adaptive_rule` still reports what the
// binary defaults to, because what the freeze has to notice is that the
// BINARY's rule moved. M2d's freeze pinned the scheduler's NUMBERS and not its
// RULE, and M2b.2 changes no constant and no policy field — so without this
// the mechanism that exists to make post-freeze tuning impossible to do
// quietly would have missed a change larger than any constant it pins.
//
// IT IS `voc`, AND THAT IS M2b.2's RESULT RATHER THAN ITS PLAN. The revision
// shipped as a selectable arm and NOT as the default, on two measurements the
// block's own pre-registration did not have a falsifier for:
//
//   - MEASURED FALSE ADMISSION. On `testdata/toyrepo/patches-tie` under the
//     SHIPPED DEFAULT policy the unbounded reference decides ESCALATE
//     (`on_ranking_tie`). Deterministic sweep, `--budget-basis=predicted`, at
//     B = 1 200/1 400/1 600/1 800/2 000 ms: `voc` REJECTs at every level and
//     `voc2` SELECTs at every level, with the rival's suite never bought. At
//     B ≈ S all three arms ESCALATE, so the divergence is exactly the budgeted
//     band the revision exists for. §7.3's F-4 was stated against the LADDER
//     and cannot fire — `voc2` ties the ladder there — and no falsifier was
//     stated against `voc`, the arm being replaced. The trade §3.3 predicted
//     (false rejection → starved admission) is real, is one-way at the shipped
//     default, and a rule that makes it may not become the default in the same
//     block that measures it.
//   - NO SAFETY MEASUREMENT EXISTS FOR IT. Every gate that could catch a
//     regression runs `voc2` on a code path it never enters: the adversarial
//     corpus builds a FRESH workspace per vector, so no kind is priced,
//     `finish_ms` is unknown, and the whole race falls back to M2b's rule
//     (`commit_basis: "unpriced-fallback(…)"`, `scarce: false`, `|C| = 0`);
//     21 of the 22 vectors carry no budget at all. Every eval cell is
//     byte-identical between the two rules for the same reason. "FAR unchanged"
//     and "adversarial 22/22" are true statements about `voc`.
//
// Promoting it needs a cell in which `commit_basis` is `reserved` on at least
// one step of a LABELLED instance, and a policy layer that answers the starved
// admission (`on_evidence_incomplete`, still off by default, base rate still
// unmeasured). Until then the freeze does not refuse, because nothing moved —
// which is the check working, not the check sleeping.
const AdaptiveRuleDefault = SelectorNameVOC

// AdaptiveRule renders the compiled default rule for the trace and for the
// eval freeze check. Both call this rather than restating the string: a
// snapshot that can disagree with the thing it snapshots is worse than no
// snapshot.
func AdaptiveRule() string { return AdaptiveRuleDefault }

// ExecutorConstants renders decision 6's compiled constant as the map
// schedule.started records. It calls correlate.go's ExecutorBP rather than
// restating the numbers: a snapshot that could disagree with the constant it
// snapshots is worse than no snapshot.
func ExecutorConstants() map[string]int64 {
	return map[string]int64{
		policy.ExecutorControlPlane:     ExecutorBP(policy.ExecutorControlPlane),
		policy.ExecutorCandidateProcess: ExecutorBP(policy.ExecutorCandidateProcess),
	}
}

// RedundancyConstants renders decision 5's tier table as schedule.started
// records it — hand-set numbers read straight off M2a's discount rule, and
// listed in M2b §8 row 4 as one of the things M2d replaces with a measured
// mistake-agreement matrix.
func RedundancyConstants() map[string]int64 {
	return map[string]int64{
		TierNearDuplicate: RedNearDuplicate,
		TierSameSignal:    RedSameSignal,
		TierSamePrior:     RedSamePrior,
		TierIndependent:   RedIndependent,
	}
}
