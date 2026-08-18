package schedule

// Budget shares and the starvation boundary (M2b §2.7).
//
// The budget is intent.budget.max_oracle_ms, additive across parallel
// purchases, 0 ⇒ unbounded ⇒ M1 semantics (decision 12). It is NOT
// max_wall_ms, which bounds the race's clock and is not additive under
// parallelism.

// budget is the race's oracle-millisecond pool.
//
// nAfford and nCharge count the calls to the two methods that make a
// comparison fair (M2b1 F5: same affordability predicate, same charge point).
// They are OBSERVED rather than asserted in prose, because "both arms reach
// one `affordable` and one `charge`" is the kind of claim that stays true
// until somebody adds a second path, and a counter is what notices. They
// influence no allocation.
type budget struct {
	max      int64 // 0 = unbounded
	spent    int64
	released int64
	nAfford  int
	nCharge  int
}

func (b *budget) unbounded() bool { return b.max <= 0 }

// remaining is what is left of the pool. Under an unbounded budget there is
// no remainder to report and the honest figure is 0 — schedule.started
// records budget_ms = 0, which is what says "unbounded" to a reader.
func (b *budget) remaining() int64 {
	if b.unbounded() {
		return 0
	}
	if r := b.max - b.spent; r > 0 {
		return r
	}
	return 0
}

// share is one still-buying world's equal share of the remaining pool
// (decision 8). Equal shares look like the opposite of adaptivity, and the
// RECOMPUTATION is what makes them adaptive: eliminating four of six worlds
// triples the survivors' shares. That is successive halving's semantics —
// elimination releasing budget — rather than unequal initial allocation, and
// the reason is the attack. Unequal shares are a better allocator and they
// reintroduce cross-world starvation: a world granted a large share because
// it looked promising can burn it and take the race's budget with it. Under
// equal shares a world that burns its budget STARVES ONLY ITSELF, which is
// the containment property worth more than the efficiency.
//
// The denominator is the worlds that still have something to buy rather than
// every alive world: a world that finished its ladder needs no share, and
// reserving one for it would starve its siblings for nothing.
func (b *budget) share(contenders int) int64 {
	if b.unbounded() {
		return 0
	}
	return equalShare(b.remaining(), contenders)
}

// equalShare is decision 8's apportionment as a pure function of a pool, so
// the loop's share and a selector's allowance are ONE piece of arithmetic
// rather than two that can drift. `SelectorVOC.Allowances` is asserted
// bit-identical to `budget.share(len(frontier))` for every frontier, and it is
// bit-identical because it is the same line of code.
func equalShare(pool int64, contenders int) int64 {
	if contenders < 1 || pool <= 0 {
		return 0
	}
	return pool / int64(contenders)
}

// reserve is M2b.2 decision 3's allowance for a COMMITTED world: the pool
// minus what its co-committed siblings still need to finish.
//
//	allowance(w) = pool − Σ_{v∈C, v≠w} finish_ms(v)
//
// It is the half of the revision that moves MONEY rather than ORDER, and it is
// what makes the commitment invariant hold: since Σ_{v∈C} finish_ms(v) ≤ pool
// by construction of C, allowance(w) ≥ finish_ms(w) for every committed world,
// so every remaining rung of a committed world is affordable until that world
// is complete or eliminated.
//
// It replaces equal shares ONLY under scarcity. `share = remaining /
// |contenders|` imputed every world an equal NEED; `reserve` uses the need the
// policy and the fitted cost table actually declare. Decision 8's own
// objection to unequal shares — "a world granted a large share because it
// looked promising can burn it" — does not reach it: nothing is granted for
// looking promising, the allowance is a function of the DECLARED ladder and
// the fitted per-kind cost, both control-plane, and a candidate cannot make
// its own ladder look short.
func reserve(pool, committedTotal, mine int64) int64 {
	v := pool - (committedTotal - mine)
	if v < 0 {
		return 0
	}
	return v
}

// affordable reports whether a predicted cost fits within its world's
// current share AND the race's remaining budget.
//
// A purchase with NO local measurement is affordable while any budget
// remains. Refusing it would make every rung unbuyable on a fresh workspace,
// where by construction nothing has been measured yet; the actual spend is
// charged after the fact from the receipt's own cost.wall_ms, so the bound
// is overrun by at most one batch of unpriced purchases and the overrun is
// visible in the trace as spent > max.
//
// THE COST OF THAT LINE IS MEASURED, AND SO IS THE COST OF "FIXING" IT.
// The overrun is not a rounding error. Adversarial vector 24 races a suite
// that sleeps 2.5 s under a 400 ms oracle budget on a fresh workspace: no
// coefficient exists, every row is priced `declared-rank`, and the race
// bought the suite and spent 3 238 ms — an 8.1x overrun of a bound that was
// supposed to bind. On a fresh workspace the budget does not bind at all.
//
// The obvious repair is to bound an unpriced purchase by the share its world
// has ALREADY DRAWN (`worldSpent < share`), which needs no prediction and
// which does close vector 24. It was implemented, measured, and REVERTED,
// because it buys resource control with decision quality:
//
//	scripts/schedule-compare.sh --policy schedule --parallel 1
//	  fixed     SELECT    8 receipts  1267 ms
//	  adaptive  ESCALATE  6 receipts  1229 ms   (S-budget)
//	    declined: observe — "already spent 609 ms of its 38 ms share"
//
// The refused rung is `corpus-observe`, which costs 33 ms and which the
// 38 ms left in the pool could have paid for. At MATCHED budget the arms
// stopped agreeing: a SELECT became an ESCALATE because a cumulative-spend
// test refused a purchase the remaining budget could afford. That is the
// false REJECTION decision 4 names as adaptivity's real risk, manufactured
// by the allocator itself, and it is worse than an overrun.
//
// No test over (spent, remaining, share) separates the two cases, because
// what separates them is the rung's COST and that is exactly what is
// missing. So v0 keeps the direction the design chose — `Decide` fails
// closed, THE SCHEDULER FAILS OPEN (decision 3b) — and the budget's failure
// to bind on an unfitted workspace is a named residual rather than a bug
// somebody should quietly patch.
//
// What would close it without failing closed is the mechanism §6 already
// claims for vector 24 and the code does not yet implement: dispatch the
// unpriced purchase with a DEADLINE equal to the world's remaining share, so
// a rung that outruns the budget is killed by its own oracle timeout
// (status = error, machinery, self-elimination) instead of being refused
// before it runs. That fails open AND bounds the spend. It needs the oracle
// timeout path to produce an errored RECEIPT rather than an error return,
// which is a change to internal/oracle's contract and not an affordability
// fix, so it is named here rather than half-done.
func (b *budget) affordable(c Cost, share int64) bool {
	b.nAfford++
	if b.unbounded() {
		return true
	}
	if b.remaining() <= 0 {
		return false
	}
	if !c.Measured {
		return true
	}
	return c.MS <= share && c.MS <= b.remaining()
}

// charge records what a purchase ACTUALLY cost: the receipt's own
// cost.wall_ms, never the prediction. The trace records predicted and the
// receipt records actual, so cost_error_ms = actual − predicted falls out
// for free — the calibration residual M2d needs to know whether the cost
// model is worth anything, at the cost of zero new recorded bytes.
func (b *budget) charge(ms int64) {
	b.nCharge++
	if ms > 0 {
		b.spent += ms
	}
}

// release records a share returning to the pool when a world leaves the
// contender set. The pool itself needs no adjustment — share() divides the
// remainder by the CURRENT contender count, so release is automatic — this
// counter exists so the trace can say how much moved and when.
func (b *budget) release(ms int64) {
	if ms > 0 {
		b.released += ms
	}
}
