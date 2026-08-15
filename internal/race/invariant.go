package race

import (
	"fmt"

	"github.com/coagente/multiverso/internal/policy"
)

// Invariant results, in the vocabulary the gate results already use so an
// explain table can render both columns the same way.
const (
	InvariantOK           = "ok"
	InvariantViolated     = "violated"
	InvariantNotEvaluated = policy.GateNotEvaluated
)

// InvariantResult is one declared invariant's outcome for one candidate.
type InvariantResult struct {
	Name   string `json:"name"`
	Result string `json:"result"`
	Detail string `json:"detail"` // violation detail; "" otherwise
}

// evalInvariants evaluates every declared invariant for one candidate,
// PURELY, over the receipts the world already counted.
//
// It runs only for worlds that passed every hard gate: a world stopped at
// rung O-1 has no suite receipt, and its invariants are `not-evaluated`,
// exactly as later gates are. A world that violates one is NOT machinery-
// failed — its outcome is COMPLETED and its receipts are `pass`; it is a
// candidate whose evidence contradicts itself, and escalation rule 0 makes
// the race say so.
func evalInvariants(pol policy.Policy, c *CandidateTrace) {
	if len(pol.Invariants) == 0 {
		return
	}
	c.Invariants = make([]InvariantResult, 0, len(pol.Invariants))
	if !c.Pass {
		for _, inv := range pol.Invariants {
			c.Invariants = append(c.Invariants,
				InvariantResult{Name: inv.Name, Result: InvariantNotEvaluated})
		}
		return
	}
	for _, inv := range pol.Invariants {
		holds, detail := policy.Holds(inv, func(role, metric string) (int64, bool) {
			sel, ok := inv.Roles[role]
			if !ok {
				return 0, false
			}
			cr := c.counted[sel]
			if cr.rec == nil {
				return 0, false
			}
			v, present := cr.rec.Result.Metrics[metric]
			return v, present
		})
		res := InvariantResult{Name: inv.Name, Result: InvariantOK}
		if !holds {
			res.Result, res.Detail = InvariantViolated, detail
			c.Pass = false
		}
		c.Invariants = append(c.Invariants, res)
	}
}

// violatedInvariants returns a candidate's violations in policy order.
func violatedInvariants(c *CandidateTrace) []InvariantResult {
	var out []InvariantResult
	for _, inv := range c.Invariants {
		if inv.Result == InvariantViolated {
			out = append(out, inv)
		}
	}
	return out
}

// invariantClause renders one candidate's first violation as the REJECT
// per-world clause. It is emitted only on inputs no M1e policy can
// produce, which is what keeps every M1e rationale byte-identical.
func invariantClause(c *CandidateTrace) string {
	v := violatedInvariants(c)
	if len(v) == 0 {
		return ""
	}
	return fmt.Sprintf("%s violated invariant %s (%s)", c.World, v[0].Name, v[0].Detail)
}
