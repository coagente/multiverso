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
	"time"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// nowNS is the ONE clock this package reads, and it reads it for exactly one
// purpose: measuring the arm's own metalevel time for
// schedule.finished.selection_us (decision 6). No allocation decision is a
// function of it — the allocation is a pure function of (policy, worlds,
// receipts, cost table, budget, constants, order), which is what makes a
// replicate replayable — and a test replaces it to keep the measurement out
// of a golden.
var nowNS = func() int64 { return time.Now().UnixNano() }

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
	// Selector is decision 1's seam: the arm. Nil means SelectorVOC, so every
	// caller written before M2b1 keeps the adaptive rule it asked for — and
	// so that M2b.2's revision is never reached by DEFAULTING into it. Which
	// rule a race allocates by is a choice the orchestrator makes explicitly
	// and records; AdaptiveRuleDefault is what `mvo race --schedule=adaptive`
	// passes here, and this library keeps the published rule as its own
	// no-argument answer.
	Selector Selector
	// Order is the CONTROL-PLANE world order (decision 3): world digests in
	// the orchestrator's own order, which is candidate ordinal ascending.
	// It is HANDED IN and never derived here — internal/race knows which
	// world came from which candidate ordinal and this package must not
	// guess, because the only key available to it would be the world digest,
	// which is a function of candidate-authored bytes.
	//
	// Empty is tolerated (the pure tests hand no order) and falls back to
	// digest order for tie-breaking only, which is exactly what M2b did.
	Order []string
	// Rotation is the replicate's order rotation ρ (decision 6). Order is
	// rotated by ρ mod N before use, and both are recorded.
	Rotation int
	// BudgetBasis is what the pool is CHARGED per purchase (decision 5b):
	// BudgetBasisActual (default) charges the receipt's measured wall_ms,
	// BudgetBasisPredicted charges the pinned cost table's prediction.
	BudgetBasis string
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
	// heldAllow is what the LAST step's apportionment actually granted each
	// contender, and heldEqual says whether that apportionment was an equal
	// share. Both exist so that `released_ms` reports what a departing world
	// actually held rather than a share nobody granted it: under M2b.2's
	// reservation a committed world holds `pool − Σ siblings' finish`, which
	// is not `remaining / |contenders|` and is frequently several times it.
	// The equal-share arms keep the published arithmetic to the byte.
	heldAllow map[string]int64
	heldEqual bool
	// reserves is the last DECISIVE answer to "does this arm apportion the
	// pool across its contenders" — decisive meaning a frontier of two or more
	// rows, the only shape whose allowances distinguish the arms.
	reserves bool

	// sel is the arm (decision 1). order is the control-plane world order
	// AFTER rotation — the list the ranking reads and the trace records —
	// and orderOf is its index, so a rank comparison costs no scan.
	sel     Selector
	order   []string
	orderOf map[string]int
	basis   string

	bud     budget
	step    int
	stop    string
	nBought int
	nSeen   int
	nDecl   int
	selUS   int64
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
	sel := cfg.Selector
	if sel == nil {
		sel = SelectorVOC()
	}
	basis := cfg.BudgetBasis
	if basis == "" {
		basis = BudgetBasisActual
	}
	if basis != BudgetBasisActual && basis != BudgetBasisPredicted {
		return nil, fmt.Errorf("schedule: config: budget basis %q must be %s or %s",
			basis, BudgetBasisActual, BudgetBasisPredicted)
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
		heldAllow:  map[string]int64{},
		heldEqual:  true,
		sel:        sel,
		order:      rotate(cfg.Order, cfg.Rotation),
		orderOf:    map[string]int{},
		basis:      basis,
		bud:        budget{max: cfg.BudgetMS},
	}
	for i, w := range s.order {
		s.orderOf[w] = i
	}
	for _, w := range ws {
		s.byDig[w.Digest] = w
	}
	s.costRows = CostTable(cfg.Costs, s.Kinds(), cfg.Costs.SampleCounts())
	for _, r := range s.costRows {
		s.costByKind[r.Kind] = r
	}
	if unpriced := s.UnpricedKinds(); basis == BudgetBasisPredicted && len(unpriced) > 0 {
		// A basis under which half the rungs are free is not a basis. Under
		// `predicted` the pool is charged the model's prediction, and a
		// declared-rank kind HAS no prediction — its cost_ms is 0 and that
		// zero is not a measurement of zero (M2b decision 7c) — so the budget
		// would silently stop binding on exactly the kinds nobody measured.
		// Refused by name, so the operator learns which measurement is
		// missing rather than which flag to drop.
		return nil, fmt.Errorf("schedule: --budget-basis=%s needs a fitted cost for every buyable kind; this workspace has no local measurement for %s (race once under --budget-basis=%s to accumulate samples, or drop the basis)",
			BudgetBasisPredicted, strings.Join(unpriced, ", "), BudgetBasisActual)
	}
	return s, nil
}

// UnpricedKinds are the buyable kinds this race would price by DECLARED RANK
// — no local fit, no millisecond figure, and therefore no prediction a
// `predicted` budget basis could charge. It is read at construction and by
// `mvo race`'s pre-flight, so the CLI and the library refuse the same
// workspaces for the same reason.
func (s *Scheduler) UnpricedKinds() []string {
	var out []string
	for _, r := range s.costRows {
		if r.Basis == CostBasisDeclaredRank {
			out = append(out, r.Kind)
		}
	}
	sort.Strings(out)
	return out
}

// WorldOrder is the control-plane order this race allocates in, AFTER
// rotation: what schedule.started records and what both selectors rank by.
func (s *Scheduler) WorldOrder() []string { return append([]string{}, s.order...) }

// orderIndex is a world's position in the control-plane order. A world the
// order does not name sorts AFTER every world it does, by digest — the
// fallback the pure tests and every pre-M2b1 caller run under, and the only
// place a digest is still allowed to order anything.
func (s *Scheduler) orderIndex(world string) int {
	if i, ok := s.orderOf[world]; ok {
		return i
	}
	n := len(s.order)
	for _, w := range s.worlds {
		if w.Digest < world {
			if _, named := s.orderOf[w.Digest]; !named {
				n++
			}
		}
	}
	return n
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
		Budget:      StartBudget{MaxOracleMS: s.cfg.BudgetMS},
		BudgetBasis: s.basis,
		Constants: Constants{
			// The BINARY's default rule, rendered from the same constant the
			// selector registry holds rather than restated here: a snapshot
			// that can disagree with the thing it snapshots is worse than no
			// snapshot (M2b.2 decision 8).
			AdaptiveRule: AdaptiveRule(),
			ExecutorBP:   ExecutorConstants(),
			RedundancyBP: RedundancyConstants(),
		},
		CostTable:  s.costRows,
		Intent:     intent,
		Mode:       mode,
		Parallel:   parallel,
		Rotation:   s.cfg.Rotation,
		Schedule:   arm,
		Selector:   s.sel.Name(),
		WorldOrder: s.WorldOrder(),
	}
}

// state is the read-only view handed to the selector. The two Decide-bearing
// reads are LAZY and memoized per step: the VOC arm asks for the base
// decision once and the ladder arm never asks at all, so decision 6's "it
// does not call Decide for lookahead" is enforced by the seam rather than
// promised by the prose.
func (s *Scheduler) state() State {
	var clamped []object.RecordedReceipt
	var base object.Decision
	haveClamped, haveBase := false, false
	clampedFn := func() []object.RecordedReceipt {
		if !haveClamped {
			clamped, haveClamped = clampReceipts(s.cfg.Policy, s.receipts, s.cfg.Bounds), true
		}
		return clamped
	}
	st := State{
		Policy:  s.cfg.Policy,
		Worlds:  s.worlds,
		Decide:  s.cfg.Decide,
		Bounds:  s.cfg.Bounds,
		Inert:   s.cfg.CollectInert,
		pool:    s.bud.remaining(),
		bounded: !s.bud.unbounded(),
		clamped: clampedFn,
		base: func() object.Decision {
			if !haveBase {
				base, haveBase = s.cfg.Decide(s.cfg.Policy, s.worlds, clampedFn()), true
			}
			return base
		},
		price:     s.cfg.Costs.Predict,
		costBasis: s.costBasis,
		bought:    s.boughtEvidence,
		readBy:    s.readBy,
		orderIdx:  s.orderIndex,
		unpriced:  s.UnpricedKinds,
	}
	// THE LOOKAHEAD MEMO, one step wide. A revision that reads the bracket
	// under two regimes must not pay for it twice: the metalevel is worth
	// running only because it costs 0.07-0.3 % of the purchase it prices, and
	// that ratio is a property of the number of `Decide` calls per step. The
	// memo makes M2b.2 §3.1's "zero additional Decide calls" mechanical rather
	// than a promise, and it is per-step because the receipt set moves between
	// steps and a stale verdict would price a purchase against a decision that
	// no longer stands.
	seen := map[string]Verdicts{}
	st.look = func(p purchase) Verdicts {
		key := p.world.Digest + "\x00" + p.rung.Name
		if v, ok := seen[key]; ok {
			return v
		}
		v := Evaluate(st.Decide, st.Policy, st.Worlds, clampedFn(), st.Base(),
			p.world, p.rung, p.rest, st.Bounds)
		seen[key] = v
		return v
	}
	return st
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
	// THE MEASURED WINDOW OPENS HERE, before the frontier walk, because
	// `selection_us` is the arm's WHOLE metalevel time and the comment at the
	// charge point has claimed the frontier walk since M2b.1 while the timer
	// started three statements later. Under M2b.2 that gap stopped being
	// cosmetic: `Allowances` is where `voc2` does ALL of its lookahead
	// (voc2Plan → newCommitPlan → score → State.Look → Evaluate → Decide) and
	// `Rank` then only hits the per-step memo, so a timer started after the
	// apportionment reported ~1/1000th of the work on a scarce race and the
	// full figure on every other arm — a bias specific to the one arm being
	// measured. Reported, not charged (F8), and it must measure everything the
	// arm spends deciding what to buy.
	t0 := nowNS()
	front := s.Frontier()
	st := s.state()
	// THE APPORTIONMENT COMES FIRST, and the ordering comes second. Under
	// M2b.2 decision 4 the bracket's pass outcomes are conditioned on the
	// world being completable AT ITS ALLOWANCE, so a rule that ranked before
	// it apportioned would have to price every row twice. The arms that hold
	// no reservation compute this in constant time and ignore the answer.
	//
	// AND IT RUNS BEFORE THE EMPTY-FRONTIER RETURN, because the terminal step
	// — where every remaining contender leaves the set at once — is where the
	// largest release happens, and a `released_ms` that skipped it would make
	// this binary's `--selector=voc` race unable to reproduce the pre-M2b.2
	// binary's ledger, which is the one thing decision 6 retains `voc` to
	// guarantee. `Allowances` is total on an empty frontier for all three arms.
	allow, regime := s.sel.Allowances(front, st)
	s.releaseNonContenders(front, allow)
	if len(front) == 0 {
		s.stop, s.done = StopEmpty, true
		s.selUS += (nowNS() - t0) / 1000
		return Step{}, false
	}
	s.step++

	// THE SELECTOR ORDERS; THE LOOP PAYS (decision 1). Everything from here
	// to the end of the function is shared verbatim by both arms: one
	// affordability predicate, one share rule, one batch fill, one stop
	// vocabulary, one trace shape. The arms differ in the line below and in
	// nothing else.
	//
	// The lookahead — which only the VOC arms run — reasons over CLAMPED
	// receipts (decision 3b): the incumbent's ranking key values are bounded
	// by the same control-plane ceilings the bracket is, or the bracket is
	// measured against an unbounded self-report and rival starvation works
	// anyway. The RECORDED decision is computed from the real receipts and is
	// untouched by this.
	ranked := s.sel.Rank(front, st)
	// ONE ALLOWANCE PER WORLD, keyed on the world rather than on the row's
	// position, because Rank reorders and the apportionment is a fact about a
	// world rather than about a queue position.
	allowOf := make(map[string]int64, len(front))
	for i, p := range front {
		if i < len(allow) {
			allowOf[p.world.Digest] = allow[i]
		}
	}

	rows := make([]Considered, 0, len(ranked))
	for _, r := range ranked {
		row := r.Row
		a := allowOf[row.World]
		row.Affordable = s.bud.affordable(r.Cost, a)
		// M2d.1: the number the ONE affordability predicate above tested
		// against, recorded rather than left as prose in a decline sentence.
		row.AllowanceMS = a
		if row.Declined == "" && row.Admissible && !row.Affordable {
			// The arm's own sentence when it has one: two different budget
			// facts must not print the same sentence, and only the arm knows
			// which arithmetic refused the row (M2b.2 decision 7).
			if row.Declined = r.Unaffordable; row.Declined == "" {
				row.Declined = unaffordableReason(r.Cost, a, s.bud.remaining())
			}
		}
		rows = append(rows, row)
	}
	chosen := s.batch(ranked, rows)
	// Reported, not charged (F8). The measurement covers the frontier walk,
	// the APPORTIONMENT, the ranking, the lookahead and the batch fill —
	// everything the arm spends on deciding what to buy, and nothing it spends
	// buying.
	s.selUS += (nowNS() - t0) / 1000

	real := s.cfg.Decide(s.cfg.Policy, s.worlds, s.receipts)
	step := Step{
		Batch:       len(chosen),
		Budget:      BudgetState{ReleasedMS: s.bud.released, RemainingMS: s.bud.remaining(), SpentMS: s.bud.spent},
		Chosen:      chosen,
		CommitBasis: regime.Basis,
		CommitSet:   regime.CommitSet,
		Considered:  rows,
		DecisionNow: DecisionNow{
			PassCount: passCount(s.cfg.Policy, s.worlds, s.receipts),
			Subject:   append([]string{}, real.Subject...),
			Type:      real.Type,
		},
		Scarce:        regime.Scarce,
		Step:          s.step,
		UncommittedMS: regime.UncommittedMS,
	}
	if step.Batch > 0 {
		step.Staleness = step.Batch - 1
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
		s.skipStarvedRemainder(rows)
		return step, false
	}
	s.nBought += len(chosen)
	return step, true
}

// skipStarvedRemainder is decision 4 made observable: when the money runs
// out mid-ladder a world keeps the prefix it bought, EVERY remaining rung is
// never purchased, and every one of them gets an oracle.skipped naming budget
// exhaustion — not only the frontier rung that was priced and refused.
//
// M2a's purchase law, verbatim: the scheduler may not mark a rung "skipped,
// assume fine"; there is no such state and there will not be one. An
// unpurchased hard gate leaves a required metric absent, an absent required
// metric fails the gate, and `Decide` never names a failing world as Subject.
// The skips are the operator's whole record of what was never found out —
// without them a partially verified world reads like a fully verified one
// that lost.
func (s *Scheduler) skipStarvedRemainder(rows []Considered) {
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		seen[r.World+"\x00"+r.Oracle] = true
	}
	budget := fmt.Sprintf("budget exhausted after %d ms", s.bud.spent)
	for _, r := range rows {
		// A STARVED STOP abandons every world's remainder, because the pool
		// is empty for all of them. A world whose last row was refused BY THE
		// MONEY (M2b.2 decision 7's `reserved`/`unreachable`) is abandoned on
		// its own, whatever the stop clause says: this function runs only at
		// the stop, so the money that refused its next rung refuses every rung
		// behind it too, and the purchase law's record must cover them —
		// otherwise the finishing rule buys its efficiency with a silence
		// M2a's own event exists to prevent. It is the STOP that makes "not
		// this batch" mean "not ever"; mid-race those same sentences are
		// statements about one step's allowance and nothing is skipped for
		// them, because a world the money refused at step 2 may be bought at
		// step 4 when a committed world completes and C re-forms.
		reason := ""
		switch {
		case s.stop == StopBudget:
			reason = budget
		case moneyDecline(r.Declined):
			reason = r.Declined
		default:
			continue
		}
		for _, rung := range s.rungs {
			key := r.World + "\x00" + rung.Name
			if s.bought[r.World][rung.Name] || seen[key] {
				continue
			}
			seen[key] = true
			s.skipped = append(s.skipped, Skipped{Oracle: rung.Name, Reason: reason, World: r.World})
		}
	}
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
	if share >= remaining {
		// ONE CONTENDER: the share IS the pool, and there is no division to
		// report. This is the depth-first arm's ordinary case (decision 5 —
		// equal shares are a VOC-arm concept) and the VOC arm's last-world
		// case, and printing "this world's share" for either would describe an
		// apportionment that did not happen.
		return fmt.Sprintf("unaffordable: predicted %s exceeds the %d ms left in the pool", c.render(), remaining)
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

// batch fills the dispatch from the SELECTOR'S OWN ORDER: the first k rows
// that are admissible and affordable. It applies no ordering of its own —
// ordering is the one thing the arms differ in, and a batch fill that re-sorted
// would put a second ranking rule underneath the first.
//
// A row is eligible iff it is ADMISSIBLE and affordable; research rows are
// admitted by decision 11's mode. Everything else in the frontier is either
// terminally declined (the selector said so, or the budget did) or DEFERRED —
// considered, beaten, and buyable two batches later, which is exactly what
// distinguishes a step decline from oracle.skipped.
//
// At k > 1 under the ladder selector this is DEPTH-FIRST PRIORITY FILL rather
// than depth-first (decision 7): a world's rung k+1 needs rung k's result, so
// a strictly depth-first arm has exactly one dispatchable purchase at a time,
// and filling the batch from the next k worlds commits money to world 3's rung
// while world 1's next rung is still pending. That is why the canonical
// comparison runs both arms at --parallel 1 and why the harness refuses to
// compare two arms whose recorded `parallel` differs.
func (s *Scheduler) batch(ranked []Ranked, rows []Considered) []Chosen {
	k := s.cfg.Batch
	out := make([]Chosen, 0, k)
	for i := range rows {
		if !rows[i].Bought() || !rows[i].Affordable {
			continue
		}
		if len(out) < k {
			out = append(out, Chosen{Oracle: rows[i].Oracle, Reason: ranked[i].Reason, World: rows[i].World})
			continue
		}
		rows[i].Declined = "not this batch"
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
	var rung Rung
	for _, r := range s.rungs {
		if r.Selector().Match(rr.Receipt) {
			if s.bought[w] == nil {
				s.bought[w] = map[string]bool{}
			}
			s.bought[w][r.Name] = true
			rung = r
			break
		}
	}
	s.bud.charge(s.chargeFor(rung, rr))
}

// chargeFor is THE CHARGE POINT, and there is exactly one of it for both
// arms (F5).
//
// Under BudgetBasisActual the pool is charged what the purchase ACTUALLY
// cost — the receipt's own cost.wall_ms, never the prediction — so the trace
// records predicted, the receipt records actual, and cost_error_ms = actual −
// predicted falls out for free. That is the honest basis, and its price is
// that actual wall-clock is not in the determinism tuple: two replicates at
// one budget can buy different things because the machine was busier.
//
// Under BudgetBasisPredicted the pool is charged the PINNED SNAPSHOT's
// prediction instead, which puts spend back inside the tuple and makes every
// difference between the arms allocation rather than jitter (decision 5b).
// The receipt still records what it really cost, so nothing is hidden: the
// residual between the two is the cost model's own error, already reported.
func (s *Scheduler) chargeFor(rung Rung, rr object.RecordedReceipt) int64 {
	if s.basis != BudgetBasisPredicted {
		return rr.Receipt.Cost.WallMS
	}
	if c := s.cfg.Costs.Predict(rung); c.Measured {
		return c.MS
	}
	// Unreachable: New refuses this basis when any buyable kind is unpriced,
	// precisely so that no purchase is ever charged a prediction that does
	// not exist. Charging the actual is the failure that loses the least.
	return rr.Receipt.Cost.WallMS
}

// releaseNonContenders records the reserves of worlds that have left the
// contender set since the last step. Equal shares are made adaptive by this
// recomputation and by nothing else (decision 8).
//
// It is a NO-OP for an arm that does not hold per-world reserves. A
// depth-first arm reserves nothing for the worlds behind the head, so it has
// nothing to release, and a released_ms it never reserved would be a number
// about a mechanism that did not run.
//
// IT RUNS ON THE TERMINAL STEP TOO, and that is not an implementation detail:
// the step at which the frontier empties is where every remaining contender
// leaves the set at once, so it carries the largest release of the race. The
// M2b.2 refactor moved this call below the empty-frontier return and cut a
// bounded `voc` race's released_ms from 3 171 ms to 1 309 ms on an otherwise
// identical fixture — a published observational field moving under the arm
// decision 6 retains precisely so that old ledgers keep reproducing.
func (s *Scheduler) releaseNonContenders(front []purchase, allow []int64) {
	// WHETHER THIS ARM RESERVES PER WORLD IS READ FROM THE NUMBERS IT HANDED
	// BACK, never from its name (M2b1 decision 1: the loop is not allowed to
	// know which rule it is running). Two frontier shapes carry no answer: a
	// ONE-ROW frontier hands back the same number under every arm — the share
	// IS the pool — and an EMPTY frontier, which is the terminal step, hands
	// back no number at all. So the last DECISIVE answer stands, and the
	// terminal release is made under the apportionment the race actually ran.
	//
	// The named residual: a race whose frontier NEVER holds two rows — one
	// world, start to finish — is never decisive, so it releases nothing. The
	// pre-M2b.2 binary released the whole remaining pool there for the equal-
	// share arm and nothing for the ladder, a difference that was a fact about
	// the arm's identity and not about any number either arm produced. It is
	// recorded here rather than reproduced by asking the seam what it is.
	if len(allow) >= 2 {
		s.reserves = reservesPerWorld(allow, s.bud.remaining())
	}
	if !s.reserves {
		s.contender = map[string]bool{}
		s.heldAllow = map[string]int64{}
		s.heldEqual = true
		return
	}
	now := make(map[string]bool, len(front))
	for _, p := range front {
		now[p.world.Digest] = true
	}
	gone := make([]string, 0, len(s.contender))
	for w := range s.contender {
		if !now[w] {
			gone = append(gone, w)
		}
	}
	if len(gone) > 0 {
		if s.heldEqual {
			// THE EQUAL-SHARE ARMS KEEP THE PUBLISHED ARITHMETIC, to the byte.
			// `voc`'s released_ms is on the wire of every M2b/M2b.1/M2d ledger
			// and decision 6 retains the arm so those ledgers stay
			// reproducible; recomputing it "better" would move the reference
			// of a published comparison, which §5 forbids outright.
			s.bud.release(int64(len(gone)) * s.bud.share(len(s.contender)))
		} else {
			// AN ARM THAT DID NOT APPORTION EQUALLY RELEASES WHAT IT ACTUALLY
			// RESERVED. Under M2b.2's reservation the departing world held
			// `pool − Σ_{siblings ∈ C} finish_ms`, not `remaining /
			// |contenders|`, and crediting back an equal share would print a
			// number about a mechanism that did not run — which is the
			// sentence this file already uses to refuse releasing anything for
			// the depth-first arm.
			total := int64(0)
			for _, w := range gone {
				total += s.heldAllow[w]
			}
			s.bud.release(total)
		}
	}
	s.contender = now
	// What THIS step granted, for the next step to release from. Recorded
	// after the release, because the release is about the apportionment that
	// granted the reserve rather than the one replacing it.
	s.heldAllow = make(map[string]int64, len(front))
	s.heldEqual = true
	for i, p := range front {
		if i < len(allow) {
			s.heldAllow[p.world.Digest] = allow[i]
			if allow[i] != allow[0] {
				s.heldEqual = false
			}
		}
	}
}

// reservesPerWorld reports whether this step's allowances APPORTION the pool
// across the contenders rather than handing each of them the whole of it.
//
// It reads the apportionment the arm produced rather than asking the arm what
// kind of arm it is, which is M2b.1 decision 1's discipline held under a wider
// seam: the loop is not allowed to know which rule it is running, so the one
// thing it may key on is the number the rule handed it. An arm that reserves
// nothing releases nothing, and a released_ms it never reserved would be a
// number about a mechanism that did not run.
func reservesPerWorld(allow []int64, pool int64) bool {
	if len(allow) < 2 {
		// ONE CONTENDER: the share IS the pool, and the two readings coincide.
		return true
	}
	for _, a := range allow {
		if a != pool {
			return true
		}
	}
	return false
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
		SelectionUS:       s.selUS,
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
