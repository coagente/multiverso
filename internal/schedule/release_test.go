package schedule

// THE TWO OBSERVATIONAL FIELDS THE M2b.2 REFACTOR MOVED WITHOUT MEANING TO,
// pinned so the next refactor cannot move them either.
//
// Neither `released_ms` nor `selection_us` changes a single allocation, which
// is exactly why both went unnoticed: no test asserted them and no published
// aggregate consumed them. They are on the wire, `mvo explain --schedule`
// prints them, and M2b.1's F8 makes `selection_us` the fairness field of the
// whole three-arm protocol — a number nobody checks is a number nobody may
// publish.

import (
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// A BOUNDED `voc` RACE THAT ENDS S-EMPTY MUST STILL RECORD ITS FINAL RELEASE.
//
// The M2b.2 refactor moved `releaseNonContenders` below the empty-frontier
// early return, so the terminal step — where every remaining contender leaves
// the set at once, which is the largest release of the race — stopped being
// recorded. Measured on the toy repo before the fix: released_ms 3 398 (HEAD)
// against 1 362 (the working tree) on an otherwise identical race, same stop,
// same spend. Decision 6 retains `voc` so that every published M2b.1 and M2d
// ledger stays reproducible on the binary that also runs the revision, and a
// `--selector=voc` race that does not reproduce the pre-M2b.2 ledger defeats
// the whole reason the arm is still here.
func TestVOCRecordsItsTerminalReleaseWhenTheFrontierEmpties(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	// A budget that pays for BOTH ladders, so the race runs out of frontier
	// rather than out of money and stops S-empty with the contenders still in
	// the set at the last step.
	s := newSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: 4 * worldLadder,
		Selector: SelectorVOC(), Order: []string{a.Digest, b.Digest},
		BudgetBasis: BudgetBasisPredicted,
	}, ws...)
	steps, _ := drain(t, pol, s, ws)
	f := s.Finish()
	if f.Stop != StopEmpty {
		t.Fatalf("stop %q, want %s: the fixture does not reach the terminal step under test", f.Stop, StopEmpty)
	}
	if len(steps) == 0 {
		t.Fatal("no steps recorded")
	}
	if f.Budget.ReleasedMS <= 0 {
		t.Fatalf("released_ms = %d on a two-world voc race that ended %s: the terminal step, where every "+
			"remaining contender leaves the set at once, recorded no release", f.Budget.ReleasedMS, StopEmpty)
	}
	// The release is at least the last recorded step's remaining pool: at the
	// terminal step both worlds leave together, and each held a share of it.
	last := steps[len(steps)-1]
	if f.Budget.ReleasedMS < last.Budget.RemainingMS {
		t.Errorf("released_ms = %d, want at least the %d ms still in the pool when the last two contenders left",
			f.Budget.ReleasedMS, last.Budget.RemainingMS)
	}
}

// A DEPTH-FIRST ARM RELEASES NOTHING, INCLUDING AT THE TERMINAL STEP.
//
// The ladder reserves nothing for the worlds behind the head, so it has
// nothing to give back, and "a released_ms it never reserved would be a number
// about a mechanism that did not run" is this file's own standard. It is also
// M2b.2 §5's rule: the reference arm of every published comparison does not
// move, in its allocation OR in what it records.
func TestTheLadderReleasesNothingEverIncludingTheTerminalStep(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	s := newSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: 4 * worldLadder,
		Selector: SelectorLadder(), Order: []string{a.Digest, b.Digest},
		BudgetBasis: BudgetBasisPredicted,
	}, ws...)
	drain(t, pol, s, ws)
	if f := s.Finish(); f.Budget.ReleasedMS != 0 {
		t.Fatalf("the ladder released %d ms it never reserved", f.Budget.ReleasedMS)
	}
}

// UNDER THE RESERVATION, `released_ms` IS WHAT THE RULE ACTUALLY GRANTED.
//
// A committed world holds `pool − Σ_{siblings ∈ C} finish_ms`, which is not
// `remaining / |contenders|` and on this fixture is several times it. Crediting
// back an equal share when the head world completes would print a number about
// an apportionment nobody made — the same objection this package already makes
// against the ladder releasing anything.
func TestVOC2ReleasesTheReserveItGrantedAndNotAnEqualShare(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	s := newSched(t, pol, Config{
		Bounds: bounds, Costs: costs, BudgetMS: raceBudgetMS,
		Selector: SelectorVOC2(), Order: []string{a.Digest, b.Digest},
		BudgetBasis: BudgetBasisPredicted,
	}, ws...)
	steps, _ := drain(t, pol, s, ws)

	// Walk the recorded steps for the moment a COMMITTED world leaves the
	// contender set, and compute the reserve it held from the trace alone:
	// allowance(w) = pool − Σ_{v ∈ C, v ≠ w} finish_ms(v) (decision 3).
	reserve, share := int64(0), int64(0)
	var prev *Step
	for i := range steps {
		st := steps[i]
		if prev != nil {
			here := map[string]bool{}
			for _, r := range st.Considered {
				here[r.World] = true
			}
			for _, r := range prev.Considered {
				if here[r.World] || !r.Committed {
					continue
				}
				sib := int64(0)
				for _, o := range prev.Considered {
					if o.World != r.World && o.Committed {
						sib += o.FinishMS
					}
				}
				reserve += prev.Budget.RemainingMS - sib
				share += prev.Budget.RemainingMS / int64(len(prev.Considered))
			}
		}
		prev = &steps[i]
	}
	if reserve == 0 {
		t.Skip("no committed world left the contender set mid-race on this fixture")
	}
	if reserve == share {
		t.Skip("the reserve and the equal share coincide on this fixture; the test would not separate them")
	}
	if f := s.Finish(); f.Budget.ReleasedMS != reserve {
		t.Fatalf("released_ms = %d; the departing committed world held a %d ms RESERVE and an equal share "+
			"would have been %d ms — the trace must credit back what the rule granted",
			f.Budget.ReleasedMS, reserve, share)
	}
}

// `selection_us` MUST COVER THE APPORTIONMENT, which is where the revision
// does ALL of its lookahead.
//
// F8 makes this the reported-not-charged fairness field of the three-arm
// protocol, and `scripts/schedule-compare.sh` aggregates it per arm. The timer
// used to start after `Allowances`, so under scarcity `voc2` — whose whole
// metalevel runs inside `Allowances`, with `Rank` then hitting the per-step
// memo — reported roughly a thousandth of the work `voc` reported for the same
// race. A metalevel figure that depends on where the timer starts is not a
// measurement of a metalevel.
func TestSelectionTimeCoversTheApportionmentPass(t *testing.T) {
	pol := testPolicy(t)
	a, b := world(t, "a"), world(t, "b")
	ws := []object.RecordedWorld{a, b}
	bounds := Bounds{CollectedBase: 8}
	costs := fixedCosts(t, bounds, raceLadderMS)

	// A decision rule that costs REAL time, so a timer that misses the pass
	// that calls it reports a number that is small for a mechanical reason
	// rather than for a measured one.
	run := func(sel Selector) (int64, int) {
		calls := 0
		base := stubDecide(new(int))
		s := newSched(t, pol, Config{
			Bounds: bounds, Costs: costs, BudgetMS: raceBudgetMS, Selector: sel,
			Order: []string{a.Digest, b.Digest}, BudgetBasis: BudgetBasisPredicted,
			Decide: func(p policy.Policy, ws []object.RecordedWorld, rs []object.RecordedReceipt) object.Decision {
				calls++
				time.Sleep(200 * time.Microsecond)
				return base(p, ws, rs)
			},
		}, ws...)
		drain(t, pol, s, ws)
		return s.Finish().SelectionUS, calls
	}
	vocUS, vocCalls := run(SelectorVOC())
	voc2US, voc2Calls := run(SelectorVOC2())
	if vocCalls == 0 || voc2Calls == 0 {
		t.Fatal("no Decide calls were made; the fixture measures nothing")
	}
	// The arms make comparable numbers of Decide calls (§3.1: zero ADDITIONAL
	// calls per step), so their reported metalevel times must be within an
	// order of magnitude of each other. A thousandfold gap is the timer, not
	// the rule.
	if voc2US*20 < vocUS {
		t.Fatalf("voc2 reported selection_us = %d against voc's %d for %d and %d Decide calls: "+
			"the measured window does not cover the pass the revision does its lookahead in",
			voc2US, vocUS, voc2Calls, vocCalls)
	}
}
