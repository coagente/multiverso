// Package schedule is the M2b adaptive scheduler (CP-4, CP-2's budget half):
// the function from a budget to a purchase.
//
// The separation this package exists to preserve, stated once and load
// bearing everywhere: THE SCHEDULER DECIDES WHAT EVIDENCE TO BUY; `Decide`
// DECIDES WHAT THE EVIDENCE MEANS. Nothing here evaluates a candidate,
// nothing here is recorded into a decision, and nothing here may be authored
// by a candidate (M1f). The scheduler's whole output is observational: it
// may be rewritten, retuned or replaced, forever, without invalidating a
// single recorded decision.
//
// The rule, in one line: among the purchases each still-alive world is next
// entitled to make, buy the one maximizing
//
//	flip × correlation-discount × executor-weight / predicted-cost
//
// where `flip` is not an estimate but a three-outcome bracketing lookahead
// over the pure decision function. Measured on this tree, `Decide` over six
// worlds costs ~15.8 µs (internal/race/decide_bench_test.go) against a
// pytest rung's 100–400 ms: the metalevel is four orders of magnitude
// cheaper than the object level, which is the precondition Hay et al.
// require for metareasoning to be worth doing at all — and a precondition we
// hold only because `Decide` is pure and total.
//
// Stopping is an EXHAUSTION rule and explicitly not a statistical test
// (decision 9): Fishtest's GSPRT is the best real-world precedent for
// evidence-native admission and we have none of its five assumptions. What
// replaces type-I error control is structural — withholding monotonicity
// (decision 4) proves FAR(adaptive) ≤ FAR(exhaustive) for any policy and any
// candidate set, so adaptivity provably cannot cause a false admission. It
// can cause a false REJECTION, and that is the risk M2d must price.
package schedule

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// Config wires one scheduler. Policy, Decide and Batch are required.
type Config struct {
	Policy policy.Policy
	// Decide is decision 1's seam: a REFERENCE to the decision rule. The
	// scheduler asks; it does not model.
	Decide DecideFn
	Costs  *Table
	Bounds Bounds
	// BudgetMS is intent.budget.max_oracle_ms; 0 ⇒ unbounded ⇒ M1 semantics
	// (decision 12).
	BudgetMS int64
	// Batch is the dispatch degree k. The M1c orchestrator runs worlds
	// concurrently and a scheduler that picked one purchase at a time would
	// serialize the race, so the top-k affordable frontier purchases go out
	// together — ASHA's asynchronous halving, whose cost is that the batch
	// is priced against a decision that may have moved by up to k−1 receipts
	// (recorded as the step's staleness).
	Batch int
	// CollectInert is `mvo race --collect-inert` (decision 11): buy
	// decision-inert rungs on worlds that are still alive, mark the rows
	// BasisResearch, and let the waste metric exclude them by construction.
	// Off by default. It is the one place in this design where evidence is
	// bought that cannot matter, and it is labelled rather than smuggled in
	// as diligence.
	CollectInert bool
	// CorpusDigest is the pinned corpus object's digest: the `corpus`
	// coordinate of the correlation descriptor for the kinds that read it.
	CorpusDigest string
}

// Scheduler is the allocation loop's state. It is deterministic given
// (policy, worlds, receipts, cost table, budget, constants) — all six of
// which are recorded — and there is no randomness anywhere: a stochastic
// scheduler needs a recorded seed, and this project exposes seeds nowhere
// (AG-3, decision 13).
type Scheduler struct {
	cfg    Config
	worlds []object.RecordedWorld
	byDig  map[string]object.RecordedWorld
	rungs  []Rung

	// costRows is the cost-model SNAPSHOT this race allocated against, taken
	// once: the fit moves as the workspace's ledger grows, which is correct
	// behaviour and would silently make two races incomparable.
	costRows   []CostRow
	costByKind map[string]CostRow

	receipts []object.RecordedReceipt
	perWorld map[string][]object.RecordedReceipt
	bought   map[string]map[string]bool
	// contender is the set of worlds that had a frontier purchase at the
	// last step, so a world leaving it can release its share.
	contender map[string]bool

	bud     budget
	step    int
	stop    string
	nBought int
	nSeen   int
	nDecl   int
	skipped []Skipped
	done    bool
}

// New builds a scheduler over a FIXED world set. Worlds are held in digest
// order — the order the orchestrator assembles decision inputs in (M1c
// decision 17) — so the lookahead sees exactly what the recorded decision
// will see.
func New(cfg Config, worlds []object.RecordedWorld) (*Scheduler, error) {
	switch {
	case cfg.Decide == nil:
		return nil, errors.New("schedule: config: nil DecideFn")
	case cfg.Batch < 1:
		return nil, errors.New("schedule: config: batch must be at least 1")
	}
	ws := append([]object.RecordedWorld(nil), worlds...)
	sort.Slice(ws, func(i, j int) bool { return ws[i].Digest < ws[j].Digest })
	s := &Scheduler{
		cfg:        cfg,
		worlds:     ws,
		byDig:      make(map[string]object.RecordedWorld, len(ws)),
		rungs:      Ladder(cfg.Policy, cfg.Bounds, cfg.CorpusDigest, len(ws), cfg.CollectInert),
		costByKind: map[string]CostRow{},
		perWorld:   map[string][]object.RecordedReceipt{},
		bought:     map[string]map[string]bool{},
		contender:  map[string]bool{},
		bud:        budget{max: cfg.BudgetMS},
	}
	for _, w := range ws {
		s.byDig[w.Digest] = w
	}
	s.costRows = CostTable(cfg.Costs, s.Kinds(), cfg.Costs.SampleCounts())
	for _, r := range s.costRows {
		s.costByKind[r.Kind] = r
	}
	return s, nil
}

// Kinds are the registry kinds this race's ladder can buy, sorted. The cost
// snapshot covers exactly them: a kind no rung names is a kind this race
// never priced, and recording a coefficient for it would describe an
// allocation nobody made.
func (s *Scheduler) Kinds() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(s.rungs))
	for _, r := range s.rungs {
		if r.Kind == "" || seen[r.Kind] {
			continue
		}
		seen[r.Kind] = true
		out = append(out, r.Kind)
	}
	sort.Strings(out)
	return out
}

// Started returns the allocation's preamble: the pinned budget, the dispatch
// degree, the COST TABLE SNAPSHOT and the compiled scheduler constants. The
// snapshot is what makes an allocation auditable — the fit moves as the
// workspace's ledger grows, which is correct behaviour and would silently
// make two races incomparable.
func (s *Scheduler) Started(intent, arm string, parallel int) Started {
	mode := ModeDecision
	if s.cfg.CollectInert {
		mode = ModeCollectInert
	}
	return Started{
		Budget:    StartBudget{MaxOracleMS: s.cfg.BudgetMS},
		Constants: Constants{ExecutorBP: ExecutorConstants(), RedundancyBP: RedundancyConstants()},
		CostTable: s.costRows,
		Intent:    intent,
		Mode:      mode,
		Parallel:  parallel,
		Schedule:  arm,
	}
}

// purchase is one prospective buy: a world and the one rung it is next
// entitled to. It is unexported because a caller has no business
// constructing one — the frontier is the policy's own ladder over the
// scheduler's own state — but its two identifiers are readable, so an
// orchestrator can ask which worlds are still buying without holding a
// second model of the frontier.
type purchase struct {
	world object.RecordedWorld
	rung  Rung
	// rest is the world's remaining unbought ladder AFTER this rung, which
	// the pass outcomes of the bracket complete: the question a frontier
	// purchase has to answer is whether a world that pays for this rung can
	// reach a different decision, not whether this one receipt moves it.
	rest []Rung
}

// World is the world digest this purchase would buy evidence for.
func (p purchase) World() string { return p.world.Digest }

// Oracle is the policy-local instance name this purchase would run.
func (p purchase) Oracle() string { return p.rung.Name }

// Frontier is the set of purchases the scheduler may consider right now: at
// most ONE per alive world — its next unpurchased rung IN POLICY GATE ORDER
// (decision 2).
//
// The policy's gate order is the fidelity ladder and the scheduler may not
// reorder it within a world. This preserves M1e decision 12's short-circuit
// verbatim (a world that fails a cheap gate never pays for an expensive
// one), preserves the property that recorded gate order equals evaluation
// order, and confines adaptivity to the two questions that are actually
// adaptive: WHICH WORLD ADVANCES NEXT, and WHETHER A WORLD ADVANCES AT ALL.
func (s *Scheduler) Frontier() []purchase {
	out := make([]purchase, 0, len(s.worlds))
	for _, w := range s.worlds {
		if !s.Alive(w.Digest) {
			continue
		}
		for j, r := range s.rungs {
			if s.bought[w.Digest][r.Name] {
				continue
			}
			rest := make([]Rung, 0, len(s.rungs)-j-1)
			for _, later := range s.rungs[j+1:] {
				if !s.bought[w.Digest][later.Name] {
					rest = append(rest, later)
				}
			}
			out = append(out, purchase{world: w, rung: r, rest: rest})
			break
		}
	}
	return out
}

// Alive reports whether a world may still buy: its outcome is COMPLETED and
// no EVALUATED hard gate has failed. An eliminated world has no frontier
// purchase and its share returns to the pool.
//
// v0 NEVER ELIMINATES ON RANK. Only hard gates eliminate; the scheduler
// defers a world, it does not prune it. That is real conservatism and it
// costs real efficiency — successive halving's rank-based pruning imports
// the assumption that cheap-rung rank predicts final rank, which ch. 3 flags
// as the one LLM evidence violates, and only a MEASURED correlation licenses
// it (§8 row 8).
func (s *Scheduler) Alive(world string) bool {
	w, ok := s.byDig[world]
	if !ok || w.World.Outcome != object.OutcomeCompleted {
		return false
	}
	return !LadderStops(s.cfg.Policy, s.perWorld[world])
}

// Next scores the frontier and returns the batch to dispatch. ok is false
// when the scheduler has stopped; Finish().Stop then names the clause.
func (s *Scheduler) Next() (Step, bool) {
	if s.done {
		return Step{}, false
	}
	front := s.Frontier()
	s.releaseNonContenders(front)
	if len(front) == 0 {
		s.stop, s.done = StopEmpty, true
		return Step{}, false
	}
	s.step++

	// The lookahead reasons over CLAMPED receipts (decision 3b): the
	// incumbent's ranking key values are bounded by the same control-plane
	// ceilings the bracket is, or the bracket is measured against an
	// unbounded self-report and rival starvation works anyway. The RECORDED
	// decision is computed from the real receipts and is untouched by this.
	clamped := clampReceipts(s.cfg.Policy, s.receipts, s.cfg.Bounds)
	base := s.cfg.Decide(s.cfg.Policy, s.worlds, clamped)
	share := s.bud.share(len(front))

	rows := make([]Considered, 0, len(front))
	for _, p := range front {
		rows = append(rows, s.score(p, clamped, base, share))
	}
	chosen := s.choose(rows, share)

	real := s.cfg.Decide(s.cfg.Policy, s.worlds, s.receipts)
	st := Step{
		Batch:      len(chosen),
		Budget:     BudgetState{ReleasedMS: s.bud.released, RemainingMS: s.bud.remaining(), SpentMS: s.bud.spent},
		Chosen:     chosen,
		Considered: rows,
		DecisionNow: DecisionNow{
			PassCount: passCount(s.cfg.Policy, s.worlds, s.receipts),
			Subject:   append([]string{}, real.Subject...),
			Type:      real.Type,
		},
		Step: s.step,
	}
	if st.Batch > 0 {
		st.Staleness = st.Batch - 1
	}
	s.nSeen += len(rows)
	for _, r := range rows {
		if !r.Bought() {
			s.nDecl++
		}
	}
	if len(chosen) == 0 {
		s.done = true
		s.stop = s.stopClause(rows)
		// Every row of the final step is a TERMINAL decline: the rung is not
		// deferred to a later batch, it is never bought. schedule.step says
		// "not this batch"; oracle.skipped says "not ever".
		for _, r := range rows {
			s.skipped = append(s.skipped, Skipped{Oracle: r.Oracle, Reason: r.Declined, World: r.World})
		}
		return st, false
	}
	s.nBought += len(chosen)
	return st, true
}

// score prices one prospective purchase. DP-1 forbids floats in canonical
// JSON and the trace is canonical JSON, so the score is integer basis points
// throughout — the coverage_bp / mutation_score_bp discipline, reused:
//
//	value_bp   = flip × discount_bp × executor_bp / 10 000    ∈ [0, 10 000]
//	score_bpps = value_bp × 1 000 / max(ĉost_ms, 1)           -- bp per second
//
// ADMISSIBILITY IS NOT THE SCORE, and separating them is the red team's
// correction (M2b decision 3c). `flip` answers "may this purchase be
// declined?"; the discount and the executor weight answer "in what order
// should the affordable ones go out". A row is BOUGHT iff it is admissible
// and affordable; value_bp and score_bpps only order the queue. Conflating
// the two let a correlation discount of 0 — the near-duplicate tier, which
// two instances of one kind on one world reach — refuse a HARD GATE, which
// is the one thing an ordering term must never be able to do (§2.2's own
// ASHA argument: "VOC estimates reordering the queue rather than owning
// termination").
func (s *Scheduler) score(p purchase, clamped []object.RecordedReceipt, base object.Decision, share int64) Considered {
	flip, notes := Lookahead(s.cfg.Decide, s.cfg.Policy, s.worlds, clamped, base, p.world, p.rung, p.rest, s.cfg.Bounds)
	disc := DiscountBP(p.rung.evidence(), s.boughtEvidence(p.world.Digest))
	exec := ExecutorBP(p.rung.Corr.Executor)
	value := flip * disc * exec / FullBP
	cost := s.cfg.Costs.Predict(p.rung)
	row := Considered{
		Admissible:   flip == 1 || p.rung.HardGate,
		Affordable:   s.bud.affordable(cost, share),
		Basis:        BasisDecision,
		CostBasis:    s.costBasis(p.rung, cost),
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
	// unpriced one (decision 7c). One argmax over both was an argmax over
	// milliseconds and rank positions under one name — `guard` fitted at
	// 1 ms scoring 10 000 000 against `mutation-diff` at rank 6 scoring
	// 833 333 is not a comparison, it is a unit error.
	if cost.Measured {
		row.ScoreBPPS = value * 1000 / cost.Divisor()
	} else {
		row.ScoreRank = cost.Rank
	}
	switch {
	case !row.Admissible && s.cfg.CollectInert:
		// M2a ships the mutation and property metrics with NO ranking key,
		// because every key derivable from them is wrong-signed — and
		// correctly so. The consequence for a flip-driven scheduler is sharp
		// and must be said out loud: A RUNG NOTHING READS IS DECISION-INERT
		// AND IS NEVER BOUGHT. But M2a also ships those metrics because
		// M2b's evaluation must correlate them against ground truth before
		// anyone ranks by them, which requires buying them. This is that
		// mode.
		row.Basis = BasisResearch
	case !row.Admissible:
		row.Declined = s.inadmissibleReason(p, notes)
	case !row.Affordable:
		row.Declined = unaffordableReason(cost, share, s.bud.remaining())
	}
	return row
}

// inadmissibleReason names WHY a purchase was refused for good, derived from
// the term that actually refused it rather than printed from a template.
//
// The first draft emitted "decision-inert: no gate, ranking key or
// escalation rule reads X" for EVERY zero-valued row, which is a permanently
// recorded false statement whenever something does read the rung — and
// `oracle.skipped` carries it into the ledger, where `mvo explain --schedule`
// renders it to an operator who has no way to check it. A trace that is
// "recorded evidence, never recomputed" (decision 17) has to be evidence.
func (s *Scheduler) inadmissibleReason(p purchase, notes []string) string {
	if !s.readBy(p.rung) {
		return fmt.Sprintf("decision-inert: no gate, ranking key or escalation rule reads %s", p.rung.label())
	}
	return fmt.Sprintf("no bracket outcome moves the decision at this world's control-plane ceiling [%s]",
		strings.Join(notes, "; "))
}

// readBy reports whether anything in the pinned policy reads this rung: a
// hard gate, a metric-bearing ranking key, an invariant role, or an
// escalation rule that names the instance. It is the predicate the
// decision-inert sentence claims, so it is computed rather than assumed.
func (s *Scheduler) readBy(r Rung) bool {
	if r.HardGate {
		return true
	}
	sel := r.Selector()
	if len(keysFor(s.cfg.Policy, sel)) > 0 {
		return true
	}
	for _, inv := range s.cfg.Policy.Invariants {
		for _, role := range inv.Roles {
			if role == sel {
				return true
			}
		}
	}
	for _, req := range s.cfg.Policy.Esc.RequireEvidence {
		if req.Sel == sel || req.OracleName == r.Name {
			return true
		}
	}
	return false
}

// unaffordableReason names WHICH bound refused the purchase, because the two
// are different sentences about different situations. A priced rung is
// refused by its own prediction against its world's share. An UNPRICED one is
// never refused by a share at all — it is refused only when the pool is
// empty (budget.affordable: the scheduler fails open on an unknown cost) —
// and saying "predicted … exceeds this world's share" about a rung nobody
// measured would print a comparison that never happened.
func unaffordableReason(c Cost, share, remaining int64) string {
	if !c.Measured {
		return fmt.Sprintf("unaffordable: %s and the oracle budget is spent (%d ms remain)",
			c.render(), remaining)
	}
	return fmt.Sprintf("unaffordable: predicted %s exceeds this world's share of %d ms", c.render(), share)
}

// costBasis is the row's `cost_basis` string, taken from the RECORDED cost
// table snapshot so a per-row label and schedule.started's cost-model block
// cannot disagree about what priced a purchase.
func (s *Scheduler) costBasis(r Rung, c Cost) string {
	if row, ok := s.costByKind[r.Kind]; ok {
		return row.CostBasisText()
	}
	return c.Basis
}

// choose takes the top-k affordable frontier purchases by score, ties broken
// by (ĉost_ms asc, world digest asc, oracle name asc) — total,
// deterministic, replayable. A row is eligible iff it is ADMISSIBLE and
// affordable; research rows are admitted by decision 11's mode.
//
// PRICED ROWS GO FIRST, and unpriced ones are ordered among themselves by
// declared rank. Ordering the two together would argmax over two units
// (decision 7c), and buying the priced purchases first is also what makes
// the budget bind at all: an unpriced purchase is affordable while any pool
// remains, so spending the pool on unpriced rungs first is precisely how the
// bound is overrun.
func (s *Scheduler) choose(rows []Considered, share int64) []Chosen {
	idx := make([]int, 0, len(rows))
	for i, r := range rows {
		if !r.Bought() || !r.Affordable {
			continue
		}
		idx = append(idx, i)
	}
	sort.SliceStable(idx, func(a, b int) bool {
		x, y := rows[idx[a]], rows[idx[b]]
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
		case x.World != y.World:
			return x.World < y.World
		default:
			return x.Oracle < y.Oracle
		}
	})
	k := s.cfg.Batch
	if k > len(idx) {
		k = len(idx)
	}
	out := make([]Chosen, 0, k)
	for i, j := range idx {
		if i < k {
			out = append(out, Chosen{Oracle: rows[j].Oracle, Reason: ReasonTopScore, World: rows[j].World})
			continue
		}
		// Considered, priced, and beaten — not refused. It may be bought two
		// batches later, which is exactly what distinguishes a step decline
		// from oracle.skipped.
		rows[j].Declined = "not this batch"
	}
	return out
}

// stopClause names why the scheduler stopped when it declined everything
// (decision 9).
func (s *Scheduler) stopClause(rows []Considered) string {
	for _, r := range rows {
		if r.Admissible && !r.Affordable {
			// S-budget, the STARVED stop: something could still have changed
			// the decision and the money ran out. A starved race that
			// records REJECT is claiming "these candidates are bad" when the
			// truth is "we never bought the evidence".
			return StopBudget
		}
	}
	if !s.RankingComplete() {
		// The frontier is exhausted of value while a passing candidate still
		// lacks a receipt a ranking key reads: decision 4's caveat, and the
		// honest clause is the one that names it rather than a clean
		// S-frontier.
		return StopRanking
	}
	return StopFrontier
}

// Record charges one completed purchase and re-evaluates the frontier's
// inputs. The scheduler decides WHETHER a rung runs, never HOW: the receipt
// is produced and recorded by exactly the code path that produced it before,
// with exactly the same binding.
func (s *Scheduler) Record(rr object.RecordedReceipt) {
	s.receipts = append(s.receipts, rr)
	w := rr.Receipt.World
	s.perWorld[w] = append(s.perWorld[w], rr)
	for _, r := range s.rungs {
		if r.Selector().Match(rr.Receipt) {
			if s.bought[w] == nil {
				s.bought[w] = map[string]bool{}
			}
			s.bought[w][r.Name] = true
			break
		}
	}
	// The ACTUAL cost, from the receipt itself, never the prediction. The
	// trace records predicted and the receipt records actual, so
	// cost_error_ms = actual − predicted falls out for free.
	s.bud.charge(rr.Receipt.Cost.WallMS)
}

// releaseNonContenders records the shares of worlds that have left the
// contender set since the last step. Equal shares are made adaptive by this
// recomputation and by nothing else (decision 8).
func (s *Scheduler) releaseNonContenders(front []purchase) {
	now := make(map[string]bool, len(front))
	for _, p := range front {
		now[p.world.Digest] = true
	}
	gone := 0
	for w := range s.contender {
		if !now[w] {
			gone++
		}
	}
	if gone > 0 {
		s.bud.release(int64(gone) * s.bud.share(len(s.contender)))
	}
	s.contender = now
}

// Finish closes the allocation and returns schedule.finished's totals.
func (s *Scheduler) Finish() Finished {
	s.done = true
	if s.stop == "" {
		s.stop = StopEmpty
	}
	return Finished{
		Bought:     s.nBought,
		Budget:     BudgetState{ReleasedMS: s.bud.released, RemainingMS: s.bud.remaining(), SpentMS: s.bud.spent},
		Considered: s.nSeen,
		Declined:   s.nDecl,
		// Decision 4's one honest corner, actually reported. The field and
		// the renderer that reads it were both dead until the red team
		// noticed that `ranking_incomplete` was false in every real race
		// including the ones that stopped on S-ranking: the one caveat the
		// safety claim admits has to be observable in the artifact, or the
		// claim is stronger in the document than in the ledger.
		RankingIncomplete: !s.RankingComplete(),
		Steps:             s.step,
		Stop:              s.stop,
		Violation:         s.PurchaseLaw(),
	}
}

// Skipped returns the TERMINAL declines, one per rung the scheduler
// abandoned for good — the oracle.skipped rows M2a's purchase law requires
// so an operator can see where the budget went.
func (s *Scheduler) Skipped() []Skipped {
	if s.skipped == nil {
		return []Skipped{}
	}
	return s.skipped
}

// Receipts returns everything bought, in purchase order.
func (s *Scheduler) Receipts() []object.RecordedReceipt { return s.receipts }

// RankingComplete is decision 4's caveat made operational (S-ranking).
// Monotonicity holds for the PASS SET, not for the RANKING: among worlds
// that all passed every hard gate, withholding a receipt that a ranking key
// reads makes that key unknown, unknown always loses (M1e decision 5), and
// the winner can change. So: no stop while any candidate in the pass set is
// missing a receipt for an oracle any ranking key reads.
//
// The pass set is OVER-approximated by "every declared hard gate has a
// counted receipt and passes it" — invariants are Decide's business, and a
// superset only makes the scheduler buy for longer, which is the safe
// direction.
func (s *Scheduler) RankingComplete() bool {
	for _, w := range s.worlds {
		mine := s.perWorld[w.Digest]
		if !gatesPassed(s.cfg.Policy, w, mine) {
			// A world is outside the pass set for one of two reasons, and
			// they are not the same fact. It FAILED a gate — decided, and no
			// ranking question survives it — or it never PAID for one, in
			// which case the receipt nobody bought is the whole reason it is
			// not in the set, and its position in the ranking is unresolved
			// too. Reading the pass set alone made the starved world
			// invisible to the very clause that exists to notice it.
			if w.World.Outcome != object.OutcomeCompleted || LadderStops(s.cfg.Policy, mine) {
				continue
			}
			if len(UnpaidHardGates(s.cfg.Policy, w, mine)) > 0 {
				return false
			}
			continue
		}
		for _, k := range s.cfg.Policy.Keys {
			if k.Metric == "" || k.Sel == (policy.Selector{}) {
				continue
			}
			if countedReceipt(k.Sel, w, s.perWorld[w.Digest]) == nil {
				return false
			}
		}
	}
	return true
}

// PurchaseLaw is the assertion that MUST NEVER FIRE (decision 9).
//
// The first draft of the stopping rule carried an S-mandatory clause — "do
// not stop while the prospective winner has an unpurchased hard-gate oracle"
// — as M2a's purchase law hoisted from an invariant into a stop predicate.
// It is VACUOUS. A world with an unpurchased hard-gate oracle has an absent
// required metric; the gate therefore fails; the world therefore does not
// pass; `Decide` therefore never names it Subject. "The prospective winner
// has paid for everything" is not a condition the scheduler maintains — it
// is what a SELECT from `Decide` MEANS.
//
// That is the precise sense in which M2a's purchase law lets M2b be adaptive
// without touching `Decide`: the law is enforced by the decision function,
// and the scheduler inherits it for free, in every allocation, including
// allocations nobody has thought of yet. A non-empty return means the
// decision rule handed to this scheduler admitted a world that did not pay,
// which is a broken decision rule and not a scheduling bug — so the caller
// must abort rather than record it.
func (s *Scheduler) PurchaseLaw() string {
	d := s.cfg.Decide(s.cfg.Policy, s.worlds, s.receipts)
	if d.Type != "SELECT" || len(d.Subject) == 0 {
		return ""
	}
	w, ok := s.byDig[d.Subject[0]]
	if !ok {
		return ""
	}
	if unpaid := UnpaidHardGates(s.cfg.Policy, w, s.perWorld[w.Digest]); len(unpaid) > 0 {
		return fmt.Sprintf("purchase law violated: %s was selected with %d unpurchased hard gate(s) (first: %s)",
			w.Digest, len(unpaid), unpaid[0])
	}
	return ""
}

// UnpaidHardGates returns the labels of the hard gates a world holds no
// counted receipt for, in ladder order. It is the observable form of the
// purchase law and it reads nothing a candidate authored: a receipt either
// exists and is bound to this world's exact {tree, env}, or it does not.
func UnpaidHardGates(pol policy.Policy, w object.RecordedWorld, receipts []object.RecordedReceipt) []string {
	var out []string
	for _, g := range pol.Gates {
		if countedReceipt(g.Sel, w, receipts) == nil {
			out = append(out, g.Label)
		}
	}
	return out
}

// gatesPassed reports whether every declared hard gate has a counted receipt
// for this world AND passes it. It is the policy's own compiled predicate,
// never a second copy of the decision rule: no ranking, no escalation, no
// invariants, no rationale.
func gatesPassed(pol policy.Policy, w object.RecordedWorld, receipts []object.RecordedReceipt) bool {
	if w.World.Outcome != object.OutcomeCompleted {
		return false
	}
	for _, g := range pol.Gates {
		rec := countedReceipt(g.Sel, w, receipts)
		if rec == nil {
			return false
		}
		if ok, _ := g.Eval(rec); !ok {
			return false
		}
	}
	return true
}

// passCount is the trace's own observational count of worlds holding a
// passing receipt for every hard gate. It is not Decide's pass set — that
// one also evaluates invariants — and it is not used to decide anything.
func passCount(pol policy.Policy, worlds []object.RecordedWorld, receipts []object.RecordedReceipt) int {
	n := 0
	for _, w := range worlds {
		mine := make([]object.RecordedReceipt, 0, len(receipts))
		for _, r := range receipts {
			if r.Receipt.World == w.Digest {
				mine = append(mine, r)
			}
		}
		if gatesPassed(pol, w, mine) {
			n++
		}
	}
	return n
}

// countedReceipt picks one selector's counted receipt for one world: the
// smallest-digest receipt that names the world, is BOUND to it
// (freshness.valid_for == {world.tree, world.env} — evidence is bound or it
// is noise, PRD principle 2), and matches. It is the same order-independent
// disambiguation the decision function uses, and it is written here rather
// than imported because internal/schedule must not depend on internal/race
// (decision 1).
func countedReceipt(sel policy.Selector, w object.RecordedWorld, receipts []object.RecordedReceipt) *object.Receipt {
	best := ""
	var out *object.Receipt
	for i := range receipts {
		rr := receipts[i]
		if rr.Receipt.World != w.Digest || !sel.Match(rr.Receipt) {
			continue
		}
		if rr.Receipt.Freshness.ValidFor.Tree != w.World.Tree || rr.Receipt.Freshness.ValidFor.Env != w.World.Env {
			continue
		}
		if best == "" || rr.Digest < best {
			best, out = rr.Digest, &receipts[i].Receipt
		}
	}
	return out
}

// LadderStops reports whether a world's ladder has reached its first failed
// hard gate, walking the policy's gates IN POLICY ORDER over the receipts the
// world has accumulated so far.
//
// Policy order is the whole point. Gates may interleave oracles, and
// stopping merely because SOME gate naming the rung that just ran failed
// lets a later gate halt the ladder before an earlier gate's oracle has run
// at all. A gate whose oracle has produced no receipt yet is not a failure;
// it is a rung still to climb.
//
// It is exported so the orchestrator's fixed ladder and the scheduler's
// elimination rule are ONE function rather than two that can drift.
func LadderStops(pol policy.Policy, receipts []object.RecordedReceipt) bool {
	for _, g := range pol.Gates {
		rec := ladderReceipt(g.Sel, receipts)
		if rec == nil {
			return false
		}
		if ok, _ := g.Eval(rec); !ok {
			return true
		}
	}
	return false
}

// ladderReceipt picks a selector's receipt out of the ones a world has
// accumulated. Binding is not re-checked: the orchestrator sets valid_for
// from the world it just judged, so every receipt in this slice is bound by
// construction.
func ladderReceipt(sel policy.Selector, receipts []object.RecordedReceipt) *object.Receipt {
	best := ""
	var out *object.Receipt
	for i := range receipts {
		if !sel.Match(receipts[i].Receipt) {
			continue
		}
		if best == "" || receipts[i].Digest < best {
			best, out = receipts[i].Digest, &receipts[i].Receipt
		}
	}
	return out
}

// boughtEvidence is the correlation view of everything already bought for
// one world — the input to the discount, and nothing else about a receipt
// enters the allocation.
func (s *Scheduler) boughtEvidence(world string) []evidence {
	recs := s.perWorld[world]
	out := make([]evidence, 0, len(recs))
	for _, rr := range recs {
		corr := rr.Receipt.Correlation
		out = append(out, evidence{
			kind:  rr.Receipt.Oracle.ID,
			corr:  corr,
			prior: PriorClass(corr, s.providerOf(rr.Receipt.Oracle.ID)),
		})
	}
	return out
}

// providerOf is the corpus provider the policy declares for a kind — the
// prior class's second input, which lives in the policy rather than on the
// wire.
func (s *Scheduler) providerOf(kind string) string {
	for _, r := range s.rungs {
		if r.Kind == kind {
			return r.Provider
		}
	}
	return ""
}
