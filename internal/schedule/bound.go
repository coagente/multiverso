package schedule

// THE RETROSPECTIVE ALLOCATION BOUND (M2b1 §5, decision 10).
//
// The question, in one line: given everything the exhaustive ladder bought,
// what is the CHEAPEST allocation of that same evidence that reaches the same
// decision — and is it reachable at budget B at all?
//
// It is a PURE, DERIVED function of a recorded ledger. It records nothing, it
// invalidates nothing, and it can be improved forever — M1e decision 21's
// discipline, which is why `mvo explain --bound` computes it at read time
// instead of a race writing it down.
//
// WHAT IT BUYS, and it is three things a paper cannot do without:
//
//  1. A DENOMINATOR. "Adaptive reached the full-evidence decision on 61% of
//     instances at B1, fixed on 52%, and the bound says 68% were reachable"
//     turns a 9-point gap into 9 of the 16 available points. Without it, 9
//     points is a number with no scale.
//  2. An EXCLUSION CRITERION. Instances with minspend > B are unwinnable by
//     any allocator; pooling them dilutes a real effect and manufactures a
//     null.
//  3. A REGRET METRIC THAT NEEDS NO LABELS: per arm per instance, reached d*
//     or not, and spend over minspend.
//
// WHAT IT IS NOT, said plainly. It is not PRD §11's arm 7, the retrospective
// oracle. It bounds ALLOCATION OF A FIXED EVIDENCE SET, not selection and not
// generation: if every candidate is wrong, d* is wrong, and the bound is a
// bound on reaching a wrong answer efficiently. Only M2d's three-tier labels
// turn it into an upper bound on decision QUALITY. The name says so —
// allocation bound, never oracle.

import (
	"errors"
	"fmt"
	"sort"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// BoundCapDefault is the enumeration ceiling. Above it the bound REFUSES and
// says so, rather than approximating: an approximation reported under the
// name of an exact bound is the same over-claim this project exists to
// remove. Six worlds × four rungs is 5⁶ = 15 625 subsets ≈ 0.25 s; six worlds
// × seven rungs is 8⁶ ≈ 262 000 ≈ 4 s, which a 300-instance stratum absorbs.
const BoundCapDefault = 1_000_000

// BoundInput is one recorded race, as read back off the ledger.
type BoundInput struct {
	Policy   policy.Policy
	Worlds   []object.RecordedWorld
	Receipts []object.RecordedReceipt
	// Decide is the same seam the scheduler holds: a REFERENCE to the
	// decision rule, never a copy of it. The bound asks the real function
	// what a subset would have decided.
	Decide DecideFn
	// BudgetMS is B, the budget the reachability question is asked at. 0 asks
	// no reachability question and reports minspend alone.
	BudgetMS int64
	// Cap overrides BoundCapDefault.
	Cap int64
}

// BoundPrefix is one world's share of the cheapest sufficient allocation: how
// many rungs of its ladder the allocation buys, out of how many the reference
// race actually bought.
type BoundPrefix struct {
	World   string   `json:"world"`
	Rungs   int      `json:"rungs"`  // the prefix length in the cheapest allocation
	Bought  int      `json:"bought"` // L_w: what the reference race bought
	CostMS  int64    `json:"cost_ms"`
	Oracles []string `json:"oracles"`
}

// BoundReport is what §5 renders.
type BoundReport struct {
	// Available is false when the bound could not be computed at all — no
	// receipts, or the enumeration refused. Absent source implies absent
	// metric: a bound that could not be computed is reported as absent and
	// never as zero.
	Available bool `json:"available"`
	// Refused names why the enumeration declined, and is empty otherwise. It
	// is a REFUSAL and not an approximation.
	Refused string `json:"refused"`
	Subsets int64  `json:"subsets"`
	Cap     int64  `json:"cap"`
	// TotalMS is S: what the reference race spent on the rungs this bound
	// enumerates over. MinSpendMS is the least any prefix-respecting
	// allocation could have spent and still reached d*.
	TotalMS    int64 `json:"total_ms"`
	MinSpendMS int64 `json:"minspend_ms"`
	// SavingMS and SavingBP are the headroom: what an optimal allocator had
	// available on this instance. A comparison whose arms differ by less than
	// nothing was available is not a result.
	SavingMS int64 `json:"saving_ms"`
	SavingBP int64 `json:"saving_bp"`
	// Decision and Subject are d*: what the full evidence decided, and the
	// target every candidate subset is tested against.
	Decision string `json:"decision"`
	Subject  string `json:"subject"`
	// BudgetMS and Reachable answer §5's second question. Reachable is false
	// when NO prefix-respecting allocation reaches d* within the budget —
	// which makes the instance unwinnable by any allocator, and therefore an
	// instance neither arm can be blamed for losing.
	BudgetMS  int64 `json:"budget_ms"`
	Reachable bool  `json:"reachable"`
	// Prefixes is the cheapest sufficient allocation itself, world by world.
	Prefixes []BoundPrefix `json:"prefixes"`
	// AlwaysMS is the spend the enumeration holds CONSTANT: the cohort-stage
	// receipts phase B2 produces as an unscheduled barrier over every world
	// that observed, plus any receipt the policy's ladder order cannot place.
	// Neither arm's budget charges it and neither arm can withhold it, so it
	// is present in every subset and excluded from every cost — reported here
	// so that exclusion is visible rather than silent.
	AlwaysMS int64 `json:"always_ms"`
	// Caveats are recorded WITH the number, because every one of them bounds
	// what the number means.
	Caveats []string `json:"caveats"`
}

// Bound computes the retrospective allocation bound over a recorded race.
//
// FEASIBLE ALLOCATIONS are the PREFIX-CLOSED subsets S ⊆ R*: per world, a
// prefix of the policy's gate order. That is exactly the constraint both arms
// operate under (M2b decision 2 — a world may not buy rung k+1 before rung
// k), which is what keeps the bound tight and fair: it bounds ALLOCATORS, not
// oracles. A bound over arbitrary subsets would compare our arms against a
// machine that can buy the last rung without the first, which neither arm is
// allowed to be.
//
//	cost(S)      = Σ cost.wall_ms over S       -- counterfactual: the reference run's measurements
//	reachable(B) = ∃ S feasible : cost(S) ≤ B ∧ Decide(pol,W,S) ≡ d*
//	minspend     = min { cost(S) : Decide(pol,W,S) ≡ d* }
func Bound(in BoundInput) (BoundReport, error) {
	if in.Decide == nil {
		return BoundReport{}, errors.New("schedule: bound: nil DecideFn")
	}
	limit := in.Cap
	if limit <= 0 {
		limit = BoundCapDefault
	}
	rep := BoundReport{
		Cap:      limit,
		BudgetMS: in.BudgetMS,
		Prefixes: []BoundPrefix{},
		Caveats:  boundCaveats(),
	}
	worlds := append([]object.RecordedWorld(nil), in.Worlds...)
	sort.Slice(worlds, func(i, j int) bool { return worlds[i].Digest < worlds[j].Digest })

	chains, always := boundChains(in.Policy, worlds, in.Receipts)
	for _, rr := range always {
		rep.AlwaysMS += rr.Receipt.Cost.WallMS
	}

	// d* is recomputed from the FULL receipt set rather than read off the
	// recorded decision, so the bound is derived end to end: improving it
	// never touches a recorded race, and a mismatch between the two would be
	// a replay failure that `mvo audit` owns, not a bound this code should
	// paper over.
	star := in.Decide(in.Policy, worlds, in.Receipts)
	rep.Decision = star.Type
	if len(star.Subject) > 0 {
		rep.Subject = star.Subject[0]
	}

	subsets := int64(1)
	for _, w := range worlds {
		n := int64(len(chains[w.Digest])) + 1
		if subsets > limit/n {
			rep.Refused = fmt.Sprintf("enumeration exceeds the cap of %d prefix-closed subsets; no approximation is reported under the name of an exact bound", limit)
			return rep, nil
		}
		subsets *= n
	}
	rep.Subsets = subsets
	total := int64(0)
	for _, w := range worlds {
		for _, rr := range chains[w.Digest] {
			total += rr.Receipt.Cost.WallMS
		}
	}
	rep.TotalMS = total
	if len(in.Receipts) == 0 {
		return rep, nil
	}

	lens := make([]int, len(worlds))
	best := int64(-1)
	var bestLens []int
	for {
		cost := int64(0)
		for i, w := range worlds {
			for _, rr := range chains[w.Digest][:lens[i]] {
				cost += rr.Receipt.Cost.WallMS
			}
		}
		// A strictly-cheaper test over a LEXICOGRAPHIC enumeration is what
		// makes the answer unique: among allocations of equal cost the first
		// in prefix order wins, so two computations agree byte for byte.
		if best < 0 || cost < best {
			sub := append([]object.RecordedReceipt(nil), always...)
			for i, w := range worlds {
				sub = append(sub, chains[w.Digest][:lens[i]]...)
			}
			if boundEquivalent(in.Decide(in.Policy, worlds, sub), star) {
				best, bestLens = cost, append([]int(nil), lens...)
			}
		}
		if !boundNext(lens, worlds, chains) {
			break
		}
	}
	if best < 0 {
		// Unreachable: the maximal allocation is R* itself, which decides d*
		// by construction. Reported rather than asserted, because a bound that
		// silently returned zero would be worse than one that says it failed.
		rep.Refused = "no prefix-closed subset reproduces the full-evidence decision, including the full evidence itself"
		return rep, nil
	}
	rep.Available = true
	rep.MinSpendMS = best
	rep.SavingMS = total - best
	rep.SavingBP = shareBP(rep.SavingMS, total)
	rep.Reachable = in.BudgetMS <= 0 || best <= in.BudgetMS
	for i, w := range worlds {
		p := BoundPrefix{World: w.Digest, Rungs: bestLens[i], Bought: len(chains[w.Digest]), Oracles: []string{}}
		for _, rr := range chains[w.Digest][:bestLens[i]] {
			p.CostMS += rr.Receipt.Cost.WallMS
			p.Oracles = append(p.Oracles, rr.Receipt.Oracle.ID)
		}
		rep.Prefixes = append(rep.Prefixes, p)
	}
	return rep, nil
}

// boundNext advances the odometer over prefix lengths, least-significant
// world last, so the enumeration order is lexicographic and total.
func boundNext(lens []int, worlds []object.RecordedWorld, chains map[string][]object.RecordedReceipt) bool {
	for i := len(lens) - 1; i >= 0; i-- {
		if lens[i] < len(chains[worlds[i].Digest]) {
			lens[i]++
			return true
		}
		lens[i] = 0
	}
	return false
}

// boundEquivalent is the ≡ of §5, stated exactly: same decision TYPE and same
// SUBJECT — the world the decision is about.
//
// It compares Subject[0] and not the whole list, and the reason is a fact
// about `Decide` rather than a weakening. Decide's Subject is the FULL RANKED
// candidate list, losers included, and the order BELOW the winner moves as
// evidence is withheld — that is decision 4's own admitted caveat
// (withholding monotonicity holds for the pass set, not for the ranking).
// Demanding the whole ranking survive would make the bound measure something
// no arm claims and no comparison reads: the harness compares arms on
// decision and winner, and the bound has to be the denominator of exactly
// that comparison.
func boundEquivalent(a, b object.Decision) bool {
	if a.Type != b.Type {
		return false
	}
	return boundSubject(a) == boundSubject(b)
}

func boundSubject(d object.Decision) string {
	if len(d.Subject) == 0 {
		return ""
	}
	return d.Subject[0]
}

// boundChains splits the recorded receipts into the per-world LADDER CHAINS
// the enumeration takes prefixes of, and the receipts that are present in
// every subset.
//
// A receipt is chained when it is the world's counted receipt for a
// WORLD-STAGE rung of the policy's own ladder, in ladder order, with no gap:
// prefix-closure means rung k+1 cannot appear without rung k, so a chain ends
// at the first rung the reference race did not buy.
//
// Everything else is ALWAYS PRESENT, and this is caveat (ii) implemented
// rather than described. The cohort-stage reducer is a pure function of the
// cohort's observations and runs as an unscheduled barrier over every world
// that observed, in BOTH arms — no allocator can withhold it, so a bound that
// let a subset drop it would bound an allocator nobody can build.
func boundChains(pol policy.Policy, worlds []object.RecordedWorld,
	receipts []object.RecordedReceipt) (map[string][]object.RecordedReceipt, []object.RecordedReceipt) {

	chains := make(map[string][]object.RecordedReceipt, len(worlds))
	chained := map[string]bool{}
	for _, w := range worlds {
		mine := make([]object.RecordedReceipt, 0, len(receipts))
		for _, rr := range receipts {
			if rr.Receipt.World == w.Digest {
				mine = append(mine, rr)
			}
		}
		var chain []object.RecordedReceipt
		for _, name := range LadderNames(pol, false) {
			sel := policy.Selector{Family: name}
			if o, ok := pol.OracleByName(name); ok {
				if policy.KindStage(o.Kind) != policy.StageWorld {
					continue
				}
				sel = policy.Selector{ID: o.Kind, Config: o.Config}
			}
			rr := boundReceipt(sel, w, mine)
			if rr == nil {
				break
			}
			chain = append(chain, *rr)
			chained[rr.Digest] = true
		}
		chains[w.Digest] = chain
	}
	always := make([]object.RecordedReceipt, 0, len(receipts))
	for _, rr := range receipts {
		if !chained[rr.Digest] {
			always = append(always, rr)
		}
	}
	return chains, always
}

// boundReceipt picks one selector's counted receipt for one world — the same
// order-independent rule the decision function uses, so the chain is built
// out of exactly the receipts a gate would have read.
func boundReceipt(sel policy.Selector, w object.RecordedWorld, mine []object.RecordedReceipt) *object.RecordedReceipt {
	best := ""
	var out *object.RecordedReceipt
	for i := range mine {
		rr := mine[i]
		if !sel.Match(rr.Receipt) {
			continue
		}
		if rr.Receipt.Freshness.ValidFor.Tree != w.World.Tree || rr.Receipt.Freshness.ValidFor.Env != w.World.Env {
			continue
		}
		if best == "" || rr.Digest < best {
			best, out = rr.Digest, &mine[i]
		}
	}
	return out
}

// boundCaveats travel WITH the number, because each of them bounds what it
// means. They are recorded in the report rather than left in a design
// document nobody reads beside the figure.
func boundCaveats() []string {
	return []string{
		"costs are counterfactual: the reference run's measured wall_ms, not this arm's",
		"the cohort-stage reducer is present in every subset (it is unscheduled in both arms) and excluded from every cost",
		"R* is one draw: a flaky rung makes this a bound on that draw, and no-repurchase means we cannot do better",
		"it bounds ALLOCATION of a fixed evidence set — not selection, not generation, and not decision quality without M2d's labels",
	}
}
