package schedule

// THE SELECTOR SEAM (M2b1 decision 1): ONE LOOP, TWO SELECTORS.
//
// The two arms of the M2 experiment differ in ORDERING and in nothing else.
// The frontier rule (M2b decision 2), the affordability predicate, the charge
// point, the elimination rule, the stop clauses, the batch dispatch, the
// no-repurchase rule (M2b decision 10) and the whole trace live in
// schedule.go and are executed identically by both arms.
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
// Decide call a selector does not declare. A selector may not touch the
// budget — it does not see it — and it may not filter the frontier for any
// reason but admissibility.

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
	SelectorNameVOC    = "voc"
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
	// may not touch the budget: affordability is the loop's business, and one
	// affordability rule is what makes the two arms comparable at all.
	Rank(frontier []purchase, st State) []Ranked
	// Contenders is the denominator of the equal-share rule for this arm
	// (M2b decision 8). Equal shares are a VOC-arm concept: they exist so
	// that elimination RELEASES budget among worlds that are all buying at
	// once. A depth-first arm has one contender at a time by construction, so
	// its affordability test is `cost ≤ remaining` — which is what
	// budget.affordable already computes when contenders == 1. No second
	// predicate, no second charge point.
	Contenders(frontier []purchase) int
	// Name is the recorded selector label: SelectorNameVOC | SelectorNameLadder.
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

	clamped   func() []object.RecordedReceipt
	base      func() object.Decision
	price     func(Rung) Cost
	costBasis func(Rung, Cost) string
	bought    func(string) []evidence
	readBy    func(Rung) bool
	orderIdx  func(string) int
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

// SelectorVOC is M2b's rule, MOVED AND UNCHANGED except for decision 3's
// tie-break: among the purchases each still-alive world is next entitled to
// make, prefer the one maximizing flip × discount × executor-weight /
// predicted-cost.
func SelectorVOC() Selector { return selectorVOC{} }

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

// Contenders is every world with a frontier purchase: they are all buying at
// once, so they all hold a share, and a world leaving the set releases one.
func (selectorVOC) Contenders(frontier []purchase) int { return len(frontier) }

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
	return out
}

// scoreVOC prices one prospective purchase. DP-1 forbids floats in canonical
// JSON and the trace is canonical JSON, so the score is integer basis points
// throughout:
//
//	value_bp   = flip × discount_bp × executor_bp / 10 000    ∈ [0, 10 000]
//	score_bpps = value_bp × 1 000 / max(ĉost_ms, 1)           -- bp per second
func scoreVOC(p purchase, st State) Ranked {
	clamped := st.Clamped()
	flip, notes := Lookahead(st.Decide, st.Policy, st.Worlds, clamped, st.Base(),
		p.world, p.rung, p.rest, st.Bounds)
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
		ValueBP:      value,
		World:        p.world.Digest,
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

type selectorLadder struct{}

func (selectorLadder) Name() string { return SelectorNameLadder }

// Contenders is ONE, always. A strictly depth-first arm buys for the head
// world until that world's ladder is complete or it is eliminated, so
// reserving a share for the worlds behind it would starve the only world
// that can spend. This is not a second affordability predicate — it is
// budget.affordable's own `contenders == 1` case (decision 5).
func (selectorLadder) Contenders(frontier []purchase) int { return 1 }

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
