package schedule

// THE SELECTOR SEAM (M2b1 decision 1, M2b.2 decision 6): ONE LOOP, THREE
// SELECTORS.
//
// The arms of the M2 experiment differ in ORDERING and in APPORTIONMENT and in
// nothing else. The frontier rule (M2b decision 2), the affordability
// predicate, the charge point, the elimination rule, the stop clauses, the
// batch dispatch, the no-repurchase rule (M2b decision 10) and the whole trace
// live in schedule.go and are executed identically by all three.
//
// THE PUBLISHED RULE IS RETAINED RATHER THAN UPGRADED IN PLACE. `voc` is M2b's
// rule, unedited; `voc2` is M2b.2's revision; `ladder` is M2b.1's depth-first
// reference. Deleting `voc` would make the M2b.2 before/after a comparison
// against a DIFFERENT BINARY — every published M2b.1 and M2d number confounded
// with every other change in the tree since — and no per-instance difference
// would be attributable to the rule (F15).
//
// WHY THIS AND NOT A SECOND LOOP. A second loop is the obvious
// implementation and it silently breaks eight of M2b1 §3's sixteen fairness
// conditions in one commit — a second affordability rule, a second charge
// point, a second treatment of unpriced rungs, a second stop vocabulary, a
// second trace shape — and every one of those differences would show up in
// the published comparison as SCHEDULING. The comparison's validity is a
// property of the code layout, and this is the layout that makes the invalid
// version hard to write.
//
// Everything in this file is PURE: no I/O, no clock, no ledger, and no
// Decide call a selector does not declare. A selector may not CHARGE the
// budget and it may not filter the frontier for any reason but admissibility.
// It now SEES the remaining pool, because M2b.2's reservation is an
// apportionment of the pool and an arm that cannot see the pool cannot
// apportion it; what stays in the loop is the one affordability predicate and
// the one charge point that make the arms comparable at all (F5).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// Selector names, recorded in schedule.started.selector so a ledger says
// which arm produced it without anybody reconstructing a command line.
//
// An ABSENT selector on a pre-M2b1 trace means "voc" EXACTLY rather than by
// assumption: no earlier binary could record a trace for any arm but the
// adaptive one, because the fixed ladder was untraced (M2b1 §1).
const (
	SelectorNameVOC = "voc"
	// SelectorNameVOC2 is M2b.2's revision: the finish-cost denominator plus
	// the commit-set reservation, active only under scarcity.
	SelectorNameVOC2   = "voc2"
	SelectorNameLadder = "ladder"
)

// Selector is the one thing the arms differ in.
//
// The interface is deliberately not implementable outside this package: Rank
// takes the unexported frontier type, so an ordering rule cannot be injected
// by a caller — and therefore cannot be authored by a candidate (M1f). The
// arms share, they do not each implement.
type Selector interface {
	// Rank orders an already-computed frontier and prices each purchase. It
	// may NOT filter the frontier for any reason but admissibility, and it
	// may not CHARGE the budget: affordability is the loop's business, and one
	// affordability rule is what makes the arms comparable at all.
	Rank(frontier []purchase, st State) []Ranked
	// Allowances is how much of the remaining pool this arm lets each
	// frontier purchase's world spend at this step: one entry per frontier
	// row, IN FRONTIER ORDER, plus the step-level record of how the
	// apportionment was made.
	//
	// It is M2b.2 decision 6's generalization of M2b.1's `Contenders(frontier)
	// int`, and it is a generalization rather than a replacement:
	//
	//	voc     pool / len(frontier) for every row  == budget.share(len(frontier))
	//	voc2    decision 3's reservation           == voc's value when C = ∅
	//	ladder  pool for every row                 == budget.share(1)
	//
	// Both existing arms' behaviour is pinned bit-identical by test, because
	// deleting or "upgrading in place" the published rule would make the
	// before/after comparison a comparison against a different binary.
	//
	// The pool travels on State together with the cost-table snapshot and the
	// control-plane world order — every other input the reservation reads —
	// rather than as a bare second parameter, so one value carries the whole
	// input of the rule and a caller cannot hand the two halves different
	// steps.
	Allowances(frontier []purchase, st State) ([]int64, Regime)
	// Name is the recorded selector label: SelectorNameVOC |
	// SelectorNameVOC2 | SelectorNameLadder.
	Name() string
}

// Ranked is one frontier purchase as a selector priced and ordered it: the
// trace row it authored, and the PRICE the loop must test affordability
// against. The cost travels with the row rather than being re-derived,
// because a second prediction is a second cost model (F5).
type Ranked struct {
	P    purchase
	Row  Considered
	Cost Cost
	// Reason is what schedule.step records for a purchase this selector put
	// at the head of the queue. It is authored by the arm because only the arm
	// knows why: "highest score" is a false sentence about a depth-first
	// ladder, and a trace that prints it would describe an allocation rule
	// nobody ran.
	Reason string
	// Unaffordable is the sentence this arm prints when the LOOP's one
	// affordability predicate refuses this row, and it is authored by the arm
	// for the same reason Reason is: two different budget facts must not print
	// the same sentence (M2b.2 decision 7). "This purchase exceeds its world's
	// equal share" and "the pool is committed to finishing somebody else" are
	// different statements about different arithmetic, and an operator reading
	// oracle.skipped has no way to tell them apart afterwards.
	//
	// Empty means the loop's own share/pool sentence, which is every arm's
	// ordinary case.
	Unaffordable string
}

// State is the READ-ONLY view of the scheduler a selector may consult. It is
// a struct of accessors rather than a pointer to the Scheduler so that the
// compiler enforces what the prose asserts: a selector reads the policy, the
// worlds, the clamped evidence and the pinned cost table, and writes
// nothing.
//
// Clamped and Base are LAZY, and that laziness is decision 6 made
// mechanical: the ladder selector does not call Decide for lookahead at all,
// so a selector that never asks for the base decision costs exactly zero
// Decide calls. A State that computed it eagerly would charge the depth-first
// arm for a metalevel it does not run.
type State struct {
	Policy policy.Policy
	Worlds []object.RecordedWorld
	Decide DecideFn
	Bounds Bounds
	// Inert is `--collect-inert` (M2b decision 11).
	Inert bool

	// pool is the REMAINING oracle budget at this step and bounded says what
	// a zero means: an unbounded race reports 0 remaining because there is no
	// remainder to report, and "unbounded" and "spent" are opposite facts.
	//
	// M2b1's seam kept the budget out of the selector entirely. M2b.2
	// decision 6 hands it over, because the reservation is an apportionment of
	// the pool and an arm that cannot see the pool cannot apportion it. What
	// stays out is the CHARGE: no selector may spend, and the loop keeps one
	// affordability predicate and one charge point (F5).
	pool    int64
	bounded bool

	clamped   func() []object.RecordedReceipt
	base      func() object.Decision
	price     func(Rung) Cost
	costBasis func(Rung, Cost) string
	bought    func(string) []evidence
	readBy    func(Rung) bool
	orderIdx  func(string) int
	unpriced  func() []string
	look      func(purchase) Verdicts
}

// Clamped is the evidence the lookahead reasons over: the recorded receipts
// with every ranking-key metric bounded by its control-plane ceiling (M2b
// decision 3b). Computed at most once per step.
func (s State) Clamped() []object.RecordedReceipt { return s.clamped() }

// Base is Decide over the clamped evidence: the decision a bracket outcome
// has to move. Computed at most once per step, and never at all by an arm
// that does not ask.
func (s State) Base() object.Decision { return s.base() }

// Price prices one rung from the race's cost-table SNAPSHOT — the same table
// the loop's affordability test reads, so a purchase cannot be ordered
// against one prediction and refused against another.
func (s State) Price(r Rung) Cost { return s.price(r) }

// CostBasis is the row's `cost_basis` label, taken from the recorded
// snapshot so a per-row label and schedule.started's cost-model block cannot
// disagree about what priced a purchase.
func (s State) CostBasis(r Rung, c Cost) string { return s.costBasis(r, c) }

// BoughtEvidence is the correlation view of everything already bought for
// one world — the input to the redundancy discount, and nothing else about a
// receipt enters the allocation.
func (s State) BoughtEvidence(world string) []evidence { return s.bought(world) }

// ReadBy reports whether anything in the pinned policy reads this rung: a
// hard gate, a metric-bearing ranking key, an invariant role, or an
// escalation rule that names the instance.
func (s State) ReadBy(r Rung) bool { return s.readBy(r) }

// OrderIndex is the world's position in the CONTROL-PLANE world order
// (decision 3) — candidate ordinal ascending, rotated by the replicate's
// rotation, handed to the scheduler and recorded in
// schedule.started.world_order.
//
// It is the terminal tie-break of BOTH arms and the ranking key of the
// ladder arm, and it is emphatically NOT the world digest. A world digest is
// a function of candidate-authored bytes (world.tree is the candidate's own
// tree), and under a binding budget the verification order decides who gets
// verified at all — so ordering on the digest hands a candidate a lever on
// whether its rivals are ever measured. M1f's rule is absolute: nothing a
// scheduler or an arm consumes may be authored by a candidate.
func (s State) OrderIndex(world string) int { return s.orderIdx(world) }

// Pool is the REMAINING oracle budget, in milliseconds. It is 0 under an
// unbounded budget and 0 under an exhausted one; Bounded is what separates
// them.
func (s State) Pool() int64 { return s.pool }

// Bounded reports whether this race has an oracle budget at all
// (max_oracle_ms > 0). Under ¬Bounded the finishing rule is inert by
// construction: an unbounded pool can finish every world, so `scarce` is false
// on every step and M2b's rule runs unchanged (M2b.2 decision 1).
func (s State) Bounded() bool { return s.bounded }

// Unpriced are the buyable kinds this race prices at DECLARED RANK — no local
// fit, no millisecond figure, and therefore no finish cost the scarcity test
// could be computed from. A non-empty list falls the whole race back to M2b's
// rule, recorded as `unpriced-fallback` with these kinds named.
func (s State) Unpriced() []string { return s.unpriced() }

// Look is the flip lookahead for one frontier purchase, MEMOIZED PER STEP.
//
// The memo is what keeps M2b.2 §3.1's claim true: the revision reads the
// bracket twice — once unconditioned, to order the commit set, and once under
// decision 4's reachability condition, to price the row — and pays for it
// once. Zero additional `Decide` calls per step, plus O(Σ_w L_w) integer adds.
func (s State) Look(p purchase) Verdicts { return s.look(p) }

// SelectorVOC is M2b's rule, MOVED AND UNCHANGED except for decision 3's
// tie-break: among the purchases each still-alive world is next entitled to
// make, prefer the one maximizing flip × discount × executor-weight /
// predicted-cost.
//
// IT IS RETAINED RATHER THAN UPGRADED IN PLACE, and that is M2b.2 decision 6
// rather than nostalgia. F15 requires both arms of a published comparison to
// share one `Decide`, one oracle set, one cost model and one binary; deleting
// this rule would make the M2b.2 before/after a comparison against A DIFFERENT
// BINARY, confounding the revision's effect with every other change in the
// tree since M2d. Every published M2b.1 and M2d number stays reproducible by
// `--selector=voc` on the binary that also runs `voc2`.
func SelectorVOC() Selector { return selectorVOC{} }

// SelectorVOC2 is M2b.2's revision, and it changes NOTHING unless the pool
// cannot finish every alive world:
//
//	finish_ms(w) = Σ ĉost_ms(r) over w's UNBOUGHT rungs, in policy gate order
//	scarce       = Σ_{w alive} finish_ms(w) > remaining_pool
//
// Under ¬scarce — which includes every unbounded race — this arm executes
// M2b's rule through M2b's own code path, not through an equivalent one.
// Under scarce, two things change together and neither works alone: the
// score's denominator becomes the cost to FINISH the world (decision 2), and
// equal shares become a RESERVATION over the prefix of worlds the pool can
// actually complete (decision 3).
func SelectorVOC2() Selector { return selectorVOC2{} }

// SelectorLadder is M2b1's new arm: depth-first over worlds in the
// control-plane order, buying each world's rungs in policy gate order.
//
// It is not an allocator with a different objective. It is the M1 exhaustive
// ladder with a budget attached — the arm the M2 thesis has to be measured
// against, and the one `--schedule=fixed` never was, because that path reads
// max_oracle_ms never.
func SelectorLadder() Selector { return selectorLadder{} }

type selectorVOC struct{}

func (selectorVOC) Name() string { return SelectorNameVOC }

// Allowances is decision 8's equal share: every world with a frontier
// purchase is buying at once, so they all hold an equal share of the
// remainder, and a world leaving the set releases one. It is
// budget.share(len(frontier)) — the same arithmetic, reached through the same
// function — and a test asserts the bit-identity, because the retained arm has
// to BE M2b rather than resemble it.
//
// It computes no commit set and returns the zero Regime, whose empty Basis
// says "this arm holds no such concept" rather than "the commit set was
// empty". Absent is absent.
func (selectorVOC) Allowances(frontier []purchase, st State) ([]int64, Regime) {
	return flatAllowances(equalShare(st.Pool(), len(frontier)), len(frontier)), Regime{}
}

// flatAllowances hands every frontier row the same number, which is what both
// arms that do not reserve per world do.
func flatAllowances(v int64, n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// Rank prices the frontier and orders it by score.
//
// ADMISSIBILITY IS NOT THE SCORE, and separating them is the red team's
// correction (M2b decision 3c). `flip` answers "may this purchase be
// declined?"; the discount and the executor weight answer "in what order
// should the affordable ones go out". A row is BOUGHT iff it is admissible
// and affordable; value_bp and score_bpps only order the queue.
func (selectorVOC) Rank(frontier []purchase, st State) []Ranked {
	out := make([]Ranked, 0, len(frontier))
	for _, p := range frontier {
		out = append(out, scoreVOC(p, st))
	}
	sortRanked(out, st)
	return out
}

// sortRanked is the value-driven queue order, shared by both value-driven
// arms so that the revision cannot quietly acquire a second ordering rule.
//
// PRICED ROWS GO FIRST, and unpriced ones are ordered among themselves by
// declared rank. Ordering the two together would argmax over two units
// (M2b decision 7c), and buying the priced purchases first is also what
// makes the budget bind at all: an unpriced purchase is affordable while
// any pool remains, so spending the pool on unpriced rungs first is
// precisely how the bound is overrun.
//
// The TERMINAL tie-break is the control-plane world order, not the world
// digest (M2b1 decision 3). M2b's own adaptive tie-break had that defect;
// decision 3 removes it from both arms rather than importing it into a
// new one.
func sortRanked(out []Ranked, st State) {
	sort.SliceStable(out, func(a, b int) bool {
		x, y := out[a].Row, out[b].Row
		xp, yp := x.ScoreRank == 0, y.ScoreRank == 0
		switch {
		case xp != yp:
			return xp
		case xp && x.ScoreBPPS != y.ScoreBPPS:
			return x.ScoreBPPS > y.ScoreBPPS
		case !xp && x.ScoreRank != y.ScoreRank:
			return x.ScoreRank < y.ScoreRank
		case !xp && x.ValueBP != y.ValueBP:
			return x.ValueBP > y.ValueBP
		case x.CostMS != y.CostMS:
			return x.CostMS < y.CostMS
		case st.OrderIndex(x.World) != st.OrderIndex(y.World):
			return st.OrderIndex(x.World) < st.OrderIndex(y.World)
		default:
			return x.Oracle < y.Oracle
		}
	})
}

// scoreVOC prices one prospective purchase. DP-1 forbids floats in canonical
// JSON and the trace is canonical JSON, so the score is integer basis points
// throughout:
//
//	value_bp   = flip × discount_bp × executor_bp / 10 000    ∈ [0, 10 000]
//	score_bpps = value_bp × 1 000 / max(ĉost_ms, 1)           -- bp per second
func scoreVOC(p purchase, st State) Ranked {
	v := st.Look(p)
	flip, notes := v.Flip(), v.Notes()
	disc := DiscountBP(p.rung.evidence(), st.BoughtEvidence(p.world.Digest))
	exec := ExecutorBP(p.rung.Corr.Executor)
	value := flip * disc * exec / FullBP
	cost := st.Price(p.rung)
	row := Considered{
		Admissible:   flip == 1 || p.rung.HardGate,
		Basis:        BasisDecision,
		CostBasis:    st.CostBasis(p.rung, cost),
		CostMS:       cost.MS,
		DiscountBP:   disc,
		ExecutorBP:   exec,
		Flip:         int(flip),
		FlipOutcomes: notes,
		HardGate:     p.rung.HardGate,
		Kind:         p.rung.Kind,
		Oracle:       p.rung.Name,
		// M2b's denominator is THE RUNG'S OWN COST, and the row says so.
		// M2b.2 divides by the cost to FINISH the world under scarcity, and
		// the two are different measurements of different things that happen
		// to share a unit — so no reader and no aggregate may mistake one for
		// the other (M2b.2 decision 2).
		ScoreBasis: ScoreBasisRung,
		ValueBP:    value,
		World:      p.world.Digest,
	}
	// The two orderings never share a field, because they never shared a
	// unit: score_bpps is basis points per second and exists only for a row
	// priced from a fit, score_rank is the declared ordinal position of an
	// unpriced one (M2b decision 7c).
	if cost.Measured {
		row.ScoreBPPS = value * 1000 / cost.Divisor()
	} else {
		row.ScoreRank = cost.Rank
	}
	switch {
	case !row.Admissible && st.Inert:
		// A rung nothing reads is decision-inert and is never bought — except
		// in the mode whose whole point is to buy it, so that M2d can
		// correlate the unranked metrics against ground truth before anyone
		// ranks by them. Labelled, never smuggled in as diligence.
		row.Basis = BasisResearch
	case !row.Admissible:
		row.Declined = inadmissibleReason(p, notes, st)
	}
	return Ranked{P: p, Row: row, Cost: cost, Reason: ReasonTopScore}
}

// inadmissibleReason names WHY a purchase was refused for good, derived from
// the term that actually refused it rather than printed from a template.
//
// The first draft emitted "decision-inert: no gate, ranking key or
// escalation rule reads X" for EVERY zero-valued row, which is a permanently
// recorded false statement whenever something does read the rung — and
// `oracle.skipped` carries it into the ledger, where `mvo explain --schedule`
// renders it to an operator who has no way to check it.
func inadmissibleReason(p purchase, notes []string, st State) string {
	if !st.ReadBy(p.rung) {
		return fmt.Sprintf("decision-inert: no gate, ranking key or escalation rule reads %s", p.rung.label())
	}
	return fmt.Sprintf("no bracket outcome moves the decision at this world's control-plane ceiling [%s]",
		strings.Join(notes, "; "))
}

type selectorVOC2 struct{}

func (selectorVOC2) Name() string { return SelectorNameVOC2 }

// Allowances is decision 3's reservation, and under ¬scarce it is decision
// 8's equal share reached through the same arithmetic — the interpolation
// between the two cases M2b got right, replacing the one it got wrong.
func (selectorVOC2) Allowances(frontier []purchase, st State) ([]int64, Regime) {
	pl := voc2Plan(frontier, st)
	out := make([]int64, 0, len(frontier))
	for _, p := range frontier {
		out = append(out, pl.allowanceOf(p.world.Digest))
	}
	return out, pl.regime()
}

// Rank prices the frontier under whichever regime the pool puts the race in.
//
// The plan is recomputed here rather than carried over from Allowances, and
// that costs nothing that matters: it is O(Σ_w L_w) integer additions plus one
// sort over the alive worlds, against a lookahead that is MEMOIZED PER STEP,
// so the second plan calls `Decide` zero times. What it buys is that the arm
// stays a stateless value — a selector that cached a plan between two calls
// would be a selector whose answer depended on the order the loop asked its
// questions in.
func (selectorVOC2) Rank(frontier []purchase, st State) []Ranked {
	pl := voc2Plan(frontier, st)
	if !pl.scarce {
		// DECISION 1'S GATE. Under ¬scarce the allocator is M2b VERBATIM:
		// same score field, same denominator, same equal shares, same
		// admissibility, same stop clauses. Not "equivalent" — the same code
		// path, so that every null-case proof M2b and M2b.1 published
		// survives by construction rather than by re-derivation.
		//
		// The rows still carry the finish cost the rule computed and did not
		// divide by, labelled `score_basis: "rung"`. That is a measurement
		// worth having and it is not the denominator, and the trace says
		// which.
		out := selectorVOC{}.Rank(frontier, st)
		for i := range out {
			out[i].Row.FinishMS = pl.finish[out[i].Row.World]
		}
		return out
	}
	out := make([]Ranked, 0, len(frontier))
	for _, p := range frontier {
		out = append(out, scoreVOC2(p, st, pl))
	}
	sortRanked(out, st)
	return out
}

// voc2Plan is decisions 1-3 for one step: finish_ms per alive world, the
// scarcity test, the commit order, the commit set and the allowance.
//
// THE COMMIT-ORDER SCORE IS DECISION 2'S SCORE WITH THE TWO RUNG-PRICING
// TERMS HELD AT THEIR MAXIMUM, and this is the one place the implementation
// reads decision 3 rather than transcribing it. Written literally, the commit
// order ranks WORLDS by the score of the ONE RUNG each of them would buy next
// — and `discount_bp` and `executor_bp` price a rung's marginal evidence
// value, not a world's. On the shipped default's own ladder they are not
// comparable across worlds standing at different ladder positions: `tree-guard`
// is control-plane (executor_bp 10 000) and the pytest kinds are
// candidate-process (5 000), so a world that has just bought its guard offers a
// rung worth HALF of what an untouched world's guard is worth, whatever either
// world's finish cost.
//
// MEASURED, on §1.1's own arithmetic (two symmetric worlds, ladder 11 + 282 +
// 689 = 982, budget 1 529): the literal reading commits to world A at step 1,
// to world B at step 2 — because B's untouched guard outscores A's collect on
// the executor weight alone — and back again, buying 5 receipts for 1 275 ms
// and finishing one world by accident of where the churn stopped. That is the
// LADDER's published behaviour, and it destroys the exact property decision 2
// says the denominator produces: "finish_ms(w) falls monotonically as w buys,
// so a world at the head of the order becomes STRICTLY MORE PREFERRED with
// every purchase it makes". A commitment that moves every step is not a
// commitment.
//
// Held at maximum, the score is `flip × 10 000 × 1 000 / finish_ms`, so the
// order reads: worlds that can still change the decision first, then the
// cheapest to FINISH, then the control-plane world order. Decision 3's
// objective is preserved exactly — "count among worlds that can still change
// the decision", which is what the flip term selects and the finish term ranks
// — and the same fixture then buys 3 receipts for 982 ms, finishes a world and
// leaves 547 ms unspent, which is §7's P1 and P2 to the millisecond.
//
// `value_bp` ITSELF IS UNCHANGED (decision 2 says so in as many words): the
// discount and the executor weight still order the QUEUE, still appear on
// every row, and still are what M2d correlates. What they no longer do is
// decide which world the pool is committed to finishing.
//
// The flip is the UNCONDITIONED one — decision 2's score with decision 4's
// reachability condition not yet applied — because that condition needs the
// allowance the commit set produces. Applying it first would let a world
// declared uncompletable sink in the very order that decides whether it is
// committed, which is the failure this block exists to remove rebuilt out of
// its own repair.
func voc2Plan(frontier []purchase, st State) commitPlan {
	return newCommitPlan(frontier, st, func(p purchase, finish int64) int64 {
		return st.Look(p).Flip() * FullBP * 1000 / finishDivisor(finish)
	})
}

// finishDivisor keeps the score total: a finish cost below 1 ms would make the
// score a division by zero, and the divisor is never allowed to mean anything
// but "at least a millisecond".
func finishDivisor(finish int64) int64 {
	if finish < 1 {
		return 1
	}
	return finish
}

// scoreVOC2 prices one prospective purchase under SCARCITY.
//
//	value_bp   = flip × discount_bp × executor_bp / 10 000        -- UNCHANGED
//	score_bpps = value_bp × 1 000 / max(finish_ms(w), 1)          -- the revision
//
// The denominator is the whole fix and it introduces no constant: finish_ms is
// a sum over the pinned cost table of rungs the policy itself declares, in
// milliseconds, in the same currency as the budget it is being spent against.
// A multiplicative completion bonus was the obvious shape and is refused — it
// would be a second hand-set number in the same expression as `executor_bp`,
// with no evidence behind it. The defect is a unit error, and the repair is to
// fix the unit rather than to add a term that compensates for it.
func scoreVOC2(p purchase, st State, pl commitPlan) Ranked {
	w := p.world.Digest
	v := st.Look(p)
	completable := pl.completable(w)
	flip, notes := v.Flip(), v.Notes()
	if !completable {
		// DECISION 4, in its exact cheap form. A pass outcome completes the
		// world's entire remaining ladder; if the budget cannot buy that
		// ladder, the outcome is not a decision this race could reach, and
		// certifying a purchase against it is certifying it against money that
		// does not exist. `fail-closed` stays unconditional: a world can
		// always fail, and failing costs one rung rather than a ladder.
		flip, notes = v.FailClosedFlip(), v.FailClosedNotes()
	}
	disc := DiscountBP(p.rung.evidence(), st.BoughtEvidence(w))
	exec := ExecutorBP(p.rung.Corr.Executor)
	value := flip * disc * exec / FullBP
	cost := st.Price(p.rung)
	finish := pl.finish[w]
	row := Considered{
		// DECISION 5 amends M2b decision 3c at exactly one point: the
		// hard-gate override applies to worlds the budget can still FINISH,
		// WHILE THE POOL IS COMMITTED TO FINISHING SOMEBODY ELSE (see
		// commitPlan.reserving — the qualifier is the amendment's own
		// justification, and without it a budget below one ladder makes the
		// race buy nothing at all).
		//
		// All three red-team findings decision 3c encodes are intact, because
		// all three happened at budgets that could complete the world. What
		// lapses is buying a prefix of a world poverty has already put out
		// WITH MONEY RESERVED FOR A WORLD THAT CAN BE FINISHED — which cannot
		// put the poor world in the pass set, so refusing it is not a choice
		// to withhold, it is the absence of an alternative. And the flip test
		// still governs: where such a purchase genuinely can move the
		// decision, it is still bought.
		Admissible:   flip == 1 || (p.rung.HardGate && (completable || !pl.reserving())),
		Basis:        BasisDecision,
		Committed:    pl.committed[w],
		CostBasis:    st.CostBasis(p.rung, cost),
		CostMS:       cost.MS,
		DiscountBP:   disc,
		ExecutorBP:   exec,
		FinishMS:     finish,
		Flip:         int(flip),
		FlipOutcomes: notes,
		HardGate:     p.rung.HardGate,
		Kind:         p.rung.Kind,
		Oracle:       p.rung.Name,
		ScoreBasis:   ScoreBasisFinish,
		ScoreBPPS:    value * 1000 / finishDivisor(finish),
		ValueBP:      value,
		World:        w,
	}
	switch {
	case !row.Admissible && st.Inert:
		row.Basis = BasisResearch
	case !row.Admissible:
		row.Declined = unreachableReason(p, notes, st, pl, completable || !pl.reserving())
	}
	out := Ranked{P: p, Row: row, Cost: cost, Reason: ReasonFinishScore}
	if !pl.committed[w] && len(pl.commit) > 0 {
		// The sentence the LOOP will print if its one affordability predicate
		// refuses this row. A committed world's rungs are affordable by the
		// commitment invariant unless a cost prediction was exceeded, and in
		// that case the pool sentence is the true one. So is it when C is
		// EMPTY: nothing was committed, the allowance IS the equal share, and
		// claiming a reservation that did not happen would describe an
		// apportionment nobody made.
		out.Unaffordable = fmt.Sprintf(
			"%sthe pool is committed to finishing %d world(s) (%s); this world needs %d ms to finish and %d ms are uncommitted",
			DeclineReserved, len(pl.commit), commitSetText(pl), finish, pl.uncommitted)
	}
	return out
}

// unreachableReason names WHICH term refused a purchase for good under
// scarcity, and the three sentences may never be confused: "nothing reads this
// rung" is a claim about the policy, "no bracket outcome moves the decision"
// is a claim about the evidence, and "this world cannot be finished" is a
// claim about the money. Only the last one is new.
func unreachableReason(p purchase, notes []string, st State, pl commitPlan, completable bool) string {
	if !st.ReadBy(p.rung) {
		return fmt.Sprintf("decision-inert: no gate, ranking key or escalation rule reads %s", p.rung.label())
	}
	if !completable {
		// THE SENTENCE IS ABOUT THIS STEP, and it says so, because the
		// allowance is recomputed every step from a pool that moves: a world
		// declined here is bought two steps later when a committed world
		// completes and C re-forms empty (measured on the tie fixture at
		// B = 1 500). A decline that read as an abandonment while the arm went
		// on to buy from that world would be a permanently recorded false
		// statement — the class of defect `inadmissibleReason` already exists
		// to prevent.
		return fmt.Sprintf(
			"%sat this step this world's remaining ladder costs %d ms against a %d ms allowance, so no pass outcome is purchasable now and no bracket outcome moves the decision; the allowance is recomputed every step and the world is not abandoned",
			DeclineUnreachable, pl.finish[p.world.Digest], pl.allowanceOf(p.world.Digest))
	}
	return fmt.Sprintf("no bracket outcome moves the decision at this world's control-plane ceiling [%s]",
		strings.Join(notes, "; "))
}

// commitSetText names the worlds the pool committed to, in commit order, so a
// declined row says who took the money rather than merely that somebody did.
func commitSetText(pl commitPlan) string {
	if len(pl.commit) == 0 {
		return "none"
	}
	return strings.Join(pl.commit, ", ")
}

type selectorLadder struct{}

func (selectorLadder) Name() string { return SelectorNameLadder }

// Allowances is THE WHOLE POOL, always, for every row. A strictly depth-first
// arm buys for the head world until that world's ladder is complete or it is
// eliminated, so reserving a share for the worlds behind it would starve the
// only world that can spend. This is not a second affordability predicate — it
// is budget.share(1), M2b.1 decision 5's `contenders == 1` case, and a test
// asserts the bit-identity.
//
// THE LADDER RESERVES NOTHING AND NEEDS TO: its ORDERING already serializes
// the spend, and changing it would move the reference arm of every published
// comparison, which M2b.2 §5 forbids outright.
func (selectorLadder) Allowances(frontier []purchase, st State) ([]int64, Regime) {
	return flatAllowances(st.Pool(), len(frontier)), Regime{}
}

// Rank orders the frontier by the CONTROL-PLANE world order and prices each
// purchase from the same table the VOC arm reads.
//
// It computes no flip, no discount_bp, no executor_bp, no value_bp and no
// score_bpps, and under the "absent source implies absent metric" rule those
// columns are not reported as zeros a reader could aggregate: the row carries
// them as zero because the wire forbids omitempty games (M1b decision 5), a
// test asserts they are zero, and `mvo explain --schedule` renders `—` rather
// than `0` for a ladder race.
//
// Every ladder row is ADMISSIBLE. That is the arm's definition rather than an
// oversight: the exhaustive ladder buys every rung of every alive world in
// policy gate order, and the only question a budget adds is whether it can
// still afford the next one.
//
// WHAT HAPPENS WHEN THE HEAD WORLD'S NEXT RUNG IS UNAFFORDABLE, stated
// because it is the one place a reader could expect something else. The arm
// does NOT stop: the stop clause is shared (schedule.go), it fires only when
// nothing in the frontier is affordable, and so a starved ladder spends its
// remainder on the deepest prefix it can still afford further down the order.
// That is a consequence of decision 1 — one loop, and the arms differ in
// ordering alone — rather than a second rule. A variant that halted the whole
// race the moment the leader stalled would be a second STOP vocabulary, which
// is exactly the asymmetry that would show up in the published comparison as
// scheduling. It also differs from the "budget-truncated ladder" arithmetic
// M2b's BUILDLOG replayed over a recorded ledger, which cut the global
// purchase sequence at the first purchase that did not fit; the difference is
// real, it is why that arithmetic was never a race, and this arm is the race.
func (selectorLadder) Rank(frontier []purchase, st State) []Ranked {
	sorted := append([]purchase(nil), frontier...)
	sort.SliceStable(sorted, func(a, b int) bool {
		ia, ib := st.OrderIndex(sorted[a].world.Digest), st.OrderIndex(sorted[b].world.Digest)
		if ia != ib {
			return ia < ib
		}
		// Unreachable with a well-formed order (one index per world); a total
		// comparator anyway, because an allocation that could reorder itself
		// between two runs is not replayable.
		return sorted[a].world.Digest < sorted[b].world.Digest
	})
	out := make([]Ranked, 0, len(sorted))
	for i, p := range sorted {
		cost := st.Price(p.rung)
		out = append(out, Ranked{
			P:      p,
			Cost:   cost,
			Reason: ReasonLadderOrder,
			Row: Considered{
				Admissible:   true,
				Basis:        BasisDecision,
				CostBasis:    st.CostBasis(p.rung, cost),
				CostMS:       cost.MS,
				FlipOutcomes: []string{},
				HardGate:     p.rung.HardGate,
				Kind:         p.rung.Kind,
				Oracle:       p.rung.Name,
				Order:        i + 1,
				World:        p.world.Digest,
			},
		})
	}
	return out
}

// rotate turns the control-plane world order into replicate r's order:
// ρ(r) = r mod N, so over N replicates every candidate holds the head
// position exactly once (decision 3, reason 3).
//
// Ordinal order is not neutral — candidate 1 goes first — and randomizing it
// would make the advantage invisible rather than absent, besides being
// randomization without a recorded seed, which AG-3 forbids. Rotation makes
// the positional advantage a MEASURED VARIANCE COMPONENT instead of a
// confound, and keeps the whole thing deterministic and recorded.
func rotate(order []string, r int) []string {
	n := len(order)
	if n == 0 {
		return []string{}
	}
	k := r % n
	if k < 0 {
		k += n
	}
	out := make([]string, 0, n)
	out = append(out, order[k:]...)
	out = append(out, order[:k]...)
	return out
}
