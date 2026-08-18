package schedule

// THE FINISHING RULE (M2b.2): finish_ms, the scarcity test, the commit order,
// the commit set and the allowance. EVERYTHING IN THIS FILE IS PURE, INTEGER,
// AND CALLS `Decide` ZERO ADDITIONAL TIMES.
//
// WHY IT EXISTS, in the arithmetic that convicted M2b's rule. At the tightest
// published budget the adaptive arm held 943 ms, the purchase that would have
// completed a world cost 689 ms, equal shares offered each of two symmetric
// worlds 471 ms — and refused BOTH. Zero worlds held a full gate set, nothing
// passed, the race recorded REJECT with the money still in its pocket. M2d
// priced that at FRR_reachable = 2/2 and filed all of it under
// regret_allocation, avoidable branch.
//
// Two defects compound and both are repaired here, because neither half works
// alone (M2b.2 §4):
//
//   - THE DENOMINATOR priced a fragment of an indivisible good. Under M2a's
//     purchase law a world's evidence is all-or-nothing — an unpurchased hard
//     gate leaves a required metric absent, the gate fails, and `Decide` never
//     names a failing world as Subject — so a world holding two of three rungs
//     is worth exactly what a world holding zero is worth: nothing. Under
//     scarcity the score's denominator therefore becomes the cost to FINISH
//     the world rather than the cost of the next rung.
//   - EQUAL SHARES refused the finishing purchase on every world at once. The
//     last rung of a fidelity ladder is the most expensive one, so it is
//     exactly the purchase most likely to exceed a fractional share — and on
//     symmetric worlds it exceeds every world's share in the same step. Under
//     scarcity the pool is COMMITTED to a prefix of worlds it can actually
//     finish, and a committed world's allowance is the pool minus what its
//     co-committed siblings still need.
//
// ZERO NEW CONSTANTS. The completion term is not a knob: it is a sum over the
// pinned cost table of rungs the policy itself declares, in milliseconds, in
// the same currency as the budget it is spent against. There is nothing here
// for an operator to tune and nothing for M2d to sweep.

import (
	"fmt"
	"sort"
	"strings"
)

// Score bases (M2b.2 decision 2). The row records WHICH DENOMINATOR PRICED
// IT, so no reader and no aggregate can mistake one for the other: they are
// two different measurements of two different things that happen to share a
// unit.
const (
	// ScoreBasisRung is M2b's denominator: the rung's own predicted cost.
	ScoreBasisRung = "rung"
	// ScoreBasisFinish is M2b.2's: the cost to complete the world's whole
	// remaining ladder, which is the only quantity that buys a decision.
	ScoreBasisFinish = "finish"
)

// Commit bases (M2b.2 decision 1). A reader must be able to tell FROM THE
// TRACE ALONE which rule allocated a race, which is the second of the three
// reasons the revision is gated on scarcity at all.
const (
	// CommitBasisNotScarce: the pool can finish every alive world, so
	// decision 1's gate is shut and M2b's rule ran — the same code path, not
	// an equivalent one.
	CommitBasisNotScarce = "not-scarce"
	// CommitBasisReserved: decision 3's reservation ran.
	CommitBasisReserved = "reserved"
	// CommitBasisUnpriced: some kind this race can buy has no fitted cost, so
	// finish_ms is UNKNOWN, `scarce` is undecidable, and the allocator falls
	// back to M2b's rule for the whole race. A fix whose mechanism is
	// arithmetic over predicted costs has no business inventing a cost. The
	// kinds are named in the recorded string.
	CommitBasisUnpriced = "unpriced-fallback"
)

// The two decline sentences M2b.2 decision 7 adds, as prefixes, because two
// different budget facts must not print the same sentence and a consumer must
// be able to tell them apart without parsing prose.
//
// Neither may ever appear on a row that was bought, and neither may carry the
// decision-inert sentence — which is a claim about the POLICY ("nothing reads
// this rung") rather than about the money.
const (
	// DeclineReserved: the pool is committed to finishing other worlds, and
	// this world's rung does not fit what is left uncommitted.
	DeclineReserved = "reserved: "
	// DeclineUnreachable: this world's remaining ladder costs more than its
	// allowance AT THIS STEP, so no PASS outcome is purchasable now and no
	// bracket outcome moves the decision.
	//
	// IT IS A STATEMENT ABOUT THIS STEP AND NOT ABOUT THE WORLD'S FUTURE, and
	// the first draft of this comment said the opposite ("it terminates the
	// world's whole ladder"). That claim was false as printed and MEASURED
	// false: on the tie fixture at B = 1 500 the arm declined a world as
	// unreachable on steps 1 and 2 and BOUGHT that exact rung on step 3, for
	// 312 ms, because the head world had completed, C had emptied, and
	// decision 5's amendment reinstated the hard-gate override. The allowance
	// is recomputed every step from a pool that moves, and decision 3 says in
	// as many words that membership in C "is recomputed every step and is
	// never recorded as a property of the world" — a world deferred by poverty
	// is resurrected the moment a committed world dies, which is the whole
	// reason §4c does not need to eliminate anything. So the sentence names
	// the arithmetic of the step it was printed on, and says that it is the
	// step's arithmetic.
	DeclineUnreachable = "unreachable: "
)

// moneyDecline reports whether a decline sentence was made by the BUDGET
// rather than by the policy ("nothing reads this rung") or by the evidence
// ("no bracket outcome moves the decision").
//
// It is consulted at the STOP, which is the only moment "not this batch"
// becomes "not ever": every rung such a world will never buy then gets its
// oracle.skipped, because M2a's purchase law forbids "skipped, assume fine"
// and a partially verified world otherwise reads like a fully verified one
// that lost. It is deliberately NOT consulted mid-race — a world the money
// refused at step 2 may be bought at step 4, and recording a terminal skip for
// it while the race is still running would record a fact that is not yet true.
func moneyDecline(reason string) bool {
	return strings.HasPrefix(reason, DeclineUnreachable) || strings.HasPrefix(reason, DeclineReserved)
}

// Regime is one step's record of HOW the pool was apportioned: decision 1's
// gate, decision 3's commit set, and the money the reservation deliberately
// did not commit. It is observational — nothing in it reaches `Decide`, and a
// race that recorded none of it decides identically.
//
// An arm that computes no reservation returns the zero value, whose Basis is
// EMPTY: "this arm holds no commit set" and "the commit set was empty" are
// different facts, and the renderer prints an em dash for the first.
type Regime struct {
	Scarce        bool
	CommitSet     []string
	UncommittedMS int64
	Basis         string
}

// commitPlan is decisions 1-3 computed for one step, from one snapshot of the
// world: the pinned cost table, the policy's own ladder, the receipts bought
// so far, the remaining pool and the control-plane world order. Nothing in it
// is authored by a candidate (M2b.2 §3.4).
type commitPlan struct {
	// known is false when this race prices any buyable kind at declared rank.
	// finish_ms is then UNKNOWN and the scarcity test is UNDECIDABLE, so the
	// whole race falls back to M2b's rule — recorded, never guessed.
	known bool
	// scarce is decision 1's gate: the priced remaining ladders of the alive
	// worlds cost more than the pool holds. It is also decision 4's
	// precondition and the FAR claim's, which is why it is a recorded boolean
	// on every step rather than a caveat in prose.
	scarce  bool
	bounded bool
	pool    int64

	finish      map[string]int64
	commit      []string // the commit set C, IN COMMIT ORDER
	committed   map[string]bool
	allowance   map[string]int64
	uncommitted int64
	basis       string
}

// completable is decision 4's predicate: can the pool still finish this
// world's ladder at this world's allowance? Under ¬scarce it is true for every
// world by definition of the regime — the pool can finish everybody, which is
// what ¬scarce MEANS.
func (pl commitPlan) completable(world string) bool {
	if !pl.scarce {
		return true
	}
	return pl.finish[world] <= pl.allowance[world]
}

// reserving reports whether the pool is committed to finishing ANYBODY at this
// step, and it is the qualifier decision 5's amendment needs.
//
// MEASURED, and this is why the qualifier exists. Decision 5 lapses M2b
// decision 3c's hard-gate override for a world the budget cannot finish, on
// the argument that "buying its prefix cannot put it in the pass set — it can
// only spend money on a world that is already out for reasons of poverty".
// That argument is about the PASS SET and it is sound. What it misses is the
// ESCALATION LAYER, and the miss is not hypothetical: at a budget below ONE
// ladder no world is completable, so on the very first step every hard gate on
// every world lapses at once, nothing is admissible, and THE RACE BUYS NOTHING
// AT ALL. `Decide` then reports `on_all_worlds_failed_machinery` — "no world
// produced conclusive evidence" — which files unbought evidence under BROKEN
// MACHINERY, the exact misattribution M2b §3.4 exists to remove, and it makes
// `on_evidence_incomplete` (decision 14, measured 4 of 4 on this fixture)
// unobservable: the rule needs one world that passed everything it paid for,
// and nothing was paid for.
//
// The flip test is the design's own answer to "can this purchase matter", and
// it does not see this case: while ANY world lacks its first receipt the
// machinery-failure rule dominates every bracket outcome, so `fail-closed`
// moves nothing and flip is honestly 0. This is the same bootstrap failure
// M2b decision 3's amendment already refused by name — "a scheduler that
// refuses every zero-valued purchase buys NOTHING AT ALL; we ran that version
// and it stopped at step 1 with an empty receipt set on every race".
//
// So the amendment is read at its own justification's strength: the override
// lapses for a world the pool cannot finish WHILE THE POOL IS COMMITTED TO
// FINISHING SOMEBODY ELSE. With C empty there is no somebody else, the money
// has no better use, no reservation is being protected, and M2b decision 3c
// stands verbatim — which is also the regime decision 3 already calls "M2b
// decision 8 exactly".
func (pl commitPlan) reserving() bool { return len(pl.commit) > 0 }

// allowanceOf is the pool this world may spend at this step. It is the ONE
// number the loop's single affordability predicate tests a prediction
// against, whichever arm produced it.
func (pl commitPlan) allowanceOf(world string) int64 { return pl.allowance[world] }

// regime renders the plan as the step-level record.
func (pl commitPlan) regime() Regime {
	return Regime{
		Scarce:        pl.scarce,
		CommitSet:     append([]string{}, pl.commit...),
		UncommittedMS: pl.uncommitted,
		Basis:         pl.basis,
	}
}

// finishMS is the cost to FINISH one world: the pinned cost table's
// prediction summed over every rung the world has NOT bought, in policy gate
// order — the frontier purchase plus the ladder behind it.
//
// ok is false when any of those rungs has no fitted cost. There is no
// millisecond figure for such a rung (M2b decision 7c) and this rule does not
// invent one: an UNKNOWN finish cost makes the scarcity test undecidable and
// falls the whole race back to M2b, which is decision 7c's own rule pointed at
// the new term.
func finishMS(p purchase, st State) (int64, bool) {
	c := st.Price(p.rung)
	if !c.Measured {
		return 0, false
	}
	total := c.MS
	for _, r := range p.rest {
		rc := st.Price(r)
		if !rc.Measured {
			return 0, false
		}
		total += rc.MS
	}
	return total, true
}

// newCommitPlan computes one step's regime.
//
// score is the COMMIT-ORDER score of a frontier purchase, given its world's
// finish cost. It is supplied by the caller rather than computed here for two
// reasons. Mechanically, the score needs the flip lookahead and this file
// calls `Decide` never. Structurally, the commit order is deliberately ranked
// by the UNCONDITIONED value: letting decision 4's reachability veto reorder
// the commitment would be a positive feedback loop — a world declared
// uncompletable scores 0, sinks in the commit order, and becomes even less
// likely to be committed — which is the failure this block exists to remove,
// rebuilt out of its own repair.
func newCommitPlan(frontier []purchase, st State, score func(p purchase, finish int64) int64) commitPlan {
	pl := commitPlan{
		known:     true,
		bounded:   st.Bounded(),
		pool:      st.Pool(),
		finish:    make(map[string]int64, len(frontier)),
		committed: make(map[string]bool, len(frontier)),
		allowance: make(map[string]int64, len(frontier)),
		commit:    []string{},
		basis:     CommitBasisNotScarce,
	}
	// THE FALLBACK IS A PROPERTY OF THE RACE, NOT OF THE STEP. A kind priced
	// at declared rank is priced at declared rank for the whole race — the
	// cost table is a snapshot taken once — so the regime cannot flip halfway
	// through merely because the unpriced rung happened to be bought.
	if unpriced := st.Unpriced(); len(unpriced) > 0 {
		pl.known = false
		pl.basis = fmt.Sprintf("%s(%s)", CommitBasisUnpriced, strings.Join(unpriced, ","))
	}
	total := int64(0)
	for _, p := range frontier {
		ms, ok := finishMS(p, st)
		if !ok {
			pl.known = false
			if !strings.HasPrefix(pl.basis, CommitBasisUnpriced) {
				pl.basis = fmt.Sprintf("%s(%s)", CommitBasisUnpriced, p.rung.label())
			}
			ms = 0
		}
		pl.finish[p.world.Digest] = ms
		total += ms
	}
	if !pl.known {
		// AN UNKNOWN FINISH COST IS RECORDED AS UNKNOWN. A world whose own
		// ladder happens to be priced while another kind in the race is not
		// still has no finish cost this rule may act on, and a real number
		// beside a fallback regime would read as a number the rule declined to
		// use rather than as one it never had. 0 means unknown on the wire.
		for w := range pl.finish {
			pl.finish[w] = 0
		}
	}

	// EQUAL SHARES ARE THE DEFAULT AND THE FALLBACK, and that is decision 3's
	// claim that they are the degenerate case rather than a casualty: this is
	// budget.share(len(frontier)) exactly, reached through the same arithmetic.
	share := equalShare(pl.pool, len(frontier))
	for _, p := range frontier {
		pl.allowance[p.world.Digest] = share
	}
	pl.uncommitted = pl.pool
	// DECISION 1'S GATE. An unbounded pool can finish everything (there is no
	// remainder to divide, which is why `remaining` reports 0 and `bounded`
	// says what that 0 means), an undecidable finish cost falls back, and a
	// pool that covers every alive world's remaining ladder is M2b's regime.
	if !pl.known || !pl.bounded || total <= pl.pool {
		return pl
	}
	pl.scarce = true
	pl.basis = CommitBasisReserved

	type row struct {
		world  string
		finish int64
		score  int64
		idx    int
	}
	rows := make([]row, 0, len(frontier))
	for _, p := range frontier {
		w := p.world.Digest
		rows = append(rows, row{world: w, finish: pl.finish[w], score: score(p, pl.finish[w]), idx: st.OrderIndex(w)})
	}
	// THE COMMIT ORDER (decision 3). The terminal tie-break is the
	// CONTROL-PLANE world order and never the world digest: a digest is a
	// function of candidate-authored bytes, and under a binding budget the
	// order decides who is verified at all (M2b.1 decision 3).
	sort.SliceStable(rows, func(a, b int) bool {
		switch {
		case rows[a].score != rows[b].score:
			return rows[a].score > rows[b].score
		case rows[a].finish != rows[b].finish:
			return rows[a].finish < rows[b].finish
		case rows[a].idx != rows[b].idx:
			return rows[a].idx < rows[b].idx
		default:
			return rows[a].world < rows[b].world
		}
	})

	// C IS THE LONGEST PREFIX THE POOL CAN FINISH — a prefix and not a
	// knapsack. A knapsack maximizing the COUNT of completed worlds is
	// available and is refused: the objective is not count, it is count among
	// worlds that can still change the decision, which is what the commit
	// order already ranks by. And the maximal prefix is the SAFEST available
	// choice, not the cheapest: whenever the pool can finish two worlds it
	// finishes two, so the tie or the shortfall that would have escalated
	// still materialises (§3.3).
	spent := int64(0)
	for _, r := range rows {
		if spent+r.finish > pl.pool {
			break
		}
		spent += r.finish
		pl.committed[r.world] = true
		pl.commit = append(pl.commit, r.world)
	}
	pl.uncommitted = pl.pool - spent
	rest := len(frontier) - len(pl.commit)
	for _, r := range rows {
		if pl.committed[r.world] {
			// THE COMMITMENT INVARIANT: allowance(w) >= finish_ms(w) for every
			// committed world, by construction of C. Every remaining rung of
			// every committed world is therefore affordable at every step
			// until that world is complete or eliminated, unless a cost
			// prediction is exceeded. §1.1's dead stop is not made unlikely —
			// it is made unrepresentable.
			pl.allowance[r.world] = reserve(pl.pool, spent, r.finish)
			continue
		}
		pl.allowance[r.world] = equalShare(pl.uncommitted, rest)
	}
	return pl
}
