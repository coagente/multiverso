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
const (
	ScheduleAdaptive = "adaptive"
	ScheduleFixed    = "fixed"
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
