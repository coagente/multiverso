package schedule

// The correlation discount (M2b §2.4), which is where ch. 3 §3.5's named
// white space is entered: "nobody discounts marginal evidence value by
// correlation in the allocation loop". M2a made it computable by declaring
// Correlation{signal, corpus, generator, executor} per kind and recording it
// per receipt; this file spends that declaration and nothing else. There is
// no tuning knob here and no learned matrix — every number is one of the
// four hand-set tiers M2a's own discount rule enumerates, and every input is
// a declared field on a receipt.

import (
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// Prior classes: the AUTHORSHIP prior behind correlation.generator. The wire
// label is not enough on its own — M2a's rule 2 is about shared authorship
// ("ten tests written by one team are not ten either") while `control-plane`
// denotes a mechanical function of the tree — so the generator is mapped
// through a TABLE, never a heuristic (M2b §2.4).
const (
	// PriorMechanical is evidence a machine derived from bytes nobody wrote
	// for this race. Two mechanical priors NEVER share.
	PriorMechanical = "mechanical"
	// PriorRepoAuthored is evidence derived from what the repository's own
	// authors wrote. Two repo-authored priors ALWAYS share.
	PriorRepoAuthored = "repo-authored"
	// PriorUnknown is the honest record of a generator label this table does
	// not know. It shares with nothing, so an unknown prior makes a purchase
	// look MORE valuable and gets it bought — the scheduler's fail-open
	// direction (M2b decision 3b's generalization).
	PriorUnknown = ""
)

// PriorClass maps a receipt's declared correlation plus the corpus
// PROVIDER — which the policy holds and the wire label does not — onto the
// authorship prior. It is total over M2a's closed generator vocabulary; a
// `model:<family>` generator passes through unchanged, which is the seam
// LLM-synthesized corpora arrive through with no contract change.
//
// It reproduces M2a's own enumeration of independent pairs exactly
// (tree-guard × everything, mutation-diff × pytest-suite,
// corpus-differential × pytest-suite), which is the check that this mapping
// is a READING of M2a rather than a new opinion.
func PriorClass(corr object.Correlation, provider string) string {
	switch corr.Generator {
	case policy.GeneratorControlPlane:
		return PriorMechanical
	case policy.GeneratorBaseTree:
		// The corpus provider decides: an operator-declared corpus is a
		// mechanical replay of cases the OPERATOR wrote, while repo-suite and
		// hypothesis corpora are derived from the repository's own tests and
		// therefore share the repository's prior.
		if provider == policy.ProviderDeclared {
			return PriorMechanical
		}
		return PriorRepoAuthored
	case policy.GeneratorRepo, policy.GeneratorRepoPolicy:
		return PriorRepoAuthored
	case "":
		return PriorUnknown
	default:
		// model:<family> and anything a future menu declares. Two model
		// priors share iff the family matches, which falls out of string
		// equality below.
		return corr.Generator
	}
}

// Redundancy tiers, integer basis points (DP-1 forbids floats in canonical
// JSON and the trace is canonical JSON).
const (
	RedNearDuplicate = 10_000 // same signal AND same corpus
	RedSameSignal    = 7_000  // same signal, different corpus
	RedSamePrior     = 5_000  // same prior class
	RedIndependent   = 0      // neither
)

// FullBP is the basis-point unit: 10 000 bp = 1.
const FullBP = 10_000

// evidence is one purchase or one already-bought receipt, reduced to the
// three fields the discount reads. Nothing else about a receipt enters the
// allocation.
type evidence struct {
	kind  string
	corr  object.Correlation
	prior string
}

// priorsShare reports whether two authorship priors are the same prior.
// Two `mechanical` priors never share — a tree walk and a hash of an
// operator-written corpus have no author in common — two `repo-authored`
// priors always do, and two `model` priors share iff the family matches.
func priorsShare(a, b string) bool {
	if a == PriorUnknown || b == PriorUnknown {
		return false
	}
	if a == PriorMechanical && b == PriorMechanical {
		return false
	}
	return a == b
}

// complements reports whether two kinds stand in a DEPENDENCY relation
// rather than a substitute one. Redundancy prices substitutes; a complement
// consumes the other purchase rather than duplicating it.
//
// One exemption, by name, with its reason recorded (M1e decision 6's
// discipline — a table entry reviewed once, not a mini-language):
// corpus-differential shares `signal` AND `corpus` with corpus-observe and
// would score red_bp = 10 000 — discount zero, never bought, the differential
// extinguished by the machinery that exists to price it.
func complements(a, b string) bool {
	return (a == policy.KindCorpusDifferential && b == policy.KindCorpusObserve) ||
		(a == policy.KindCorpusObserve && b == policy.KindCorpusDifferential)
}

// redundancy prices one prospective purchase against one receipt already
// bought for the SAME world.
func redundancy(o, r evidence) int64 {
	if complements(o.kind, r.kind) {
		return RedIndependent
	}
	// A kind that declares no signal (the `command` kind: "its signal is
	// unknown to us — that is what command means") has no signal to share.
	// An unknown is not a match.
	if o.corr.Signal != "" && o.corr.Signal == r.corr.Signal {
		if o.corr.Corpus == r.corr.Corpus {
			return RedNearDuplicate
		}
		return RedSameSignal
	}
	if priorsShare(o.prior, r.prior) {
		return RedSamePrior
	}
	return RedIndependent
}

// DiscountBP is the correlation discount for one prospective purchase given
// everything already bought FOR THE SAME WORLD (M2b decision 5):
//
//	discount_bp(w,o) = 10 000 − max_{r ∈ bought(w)} red_bp(o, r)
//
// MAX, not product, because M2a's rule is a DOMINANCE rule ("independent
// only when signal AND generator both differ") and the families are
// equivalence-class shaped. A product would punish a third repo-authored
// purchase twice for one shared prior, driving the discount to zero through
// arithmetic rather than through evidence.
func DiscountBP(o evidence, bought []evidence) int64 {
	worst := int64(RedIndependent)
	for _, r := range bought {
		if red := redundancy(o, r); red > worst {
			worst = red
		}
	}
	return FullBP - worst
}

// ExecutorBP is M2b decision 6, the block's one honest constant: 10 000 for
// a control-plane-executed purchase, 5 000 for one executed inside the
// candidate's own process.
//
// M2a's discount rule 4: under M1f's residual, any number a candidate's own
// process reported is forgeable by a competent in-process adversary, so
// buying a third candidate-process oracle buys independence from honest
// error, never from that adversary.
//
// WHAT THE CONSTANT SHOULD BE, NOBODY KNOWS. It is not calibrated and not
// fitted, and calling it 0.5 is a statement we have no evidence for. It
// stays a COMPILED constant recorded in schedule.started rather than a
// policy field, because a knob nobody has calibrated must not invite an
// operator to tune it: M2d's adversarial arm sweeps
// executor_bp ∈ {0, 2 500, 5 000, 7 500, 10 000} at matched budget and the
// value that minimizes FAR-adv without costing TCAR becomes the default —
// and only THEN a policy field.
func ExecutorBP(executor string) int64 {
	if executor == policy.ExecutorControlPlane {
		return FullBP
	}
	// candidate-process, and every unknown executor with it: an executor we
	// cannot name is not one we may trust more than the candidate's own.
	return 5_000
}
