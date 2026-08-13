// Package race implements the M0 fixed orchestrator (CP-2, CP-3) and the
// pure decision function (CP-6, NFR-1).
package race

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
)

// Decision types (decision/v0 subset; ESCALATE is post-M0).
const (
	TypeSelect = "SELECT"
	TypeReject = "REJECT"
)

// World outcomes (world/v0 M0 subset).
const (
	OutcomeCompleted   = "COMPLETED"
	OutcomeConfigError = "CONFIG_ERROR"
)

// GateSuitePass is the only hard gate M0 knows how to evaluate.
const GateSuitePass = "suite-pass"

// candidate pairs a world with its suite evidence for ranking.
type candidate struct {
	digest  string
	world   object.World
	receipt *object.Receipt // suite receipt; nil when the world produced none
	pass    bool            // all hard gates passed
	wallMS  int64           // suite wall time; MaxInt64 when no receipt
}

// Decide is the v0 decision function (CP-6). It is pure and
// order-independent: Type, Subject, Evidence, and Rationale depend only on
// (policy, worlds, receipts), so audit replay reproduces them
// byte-for-byte (NFR-1). CreatedAt is left empty for the recorder to
// stamp — it must not influence the decision.
func Decide(policy object.Policy, worlds []object.World, receipts []object.Receipt) object.Decision {
	polDig, _, _ := object.Digest(policy)

	evidence := make([]string, 0, len(receipts))
	suite := make(map[string]object.Receipt, len(receipts)) // world digest → suite receipt
	suiteDig := make(map[string]string, len(receipts))      // world digest → receipt digest
	for i := range receipts {
		r := receipts[i]
		dig, _, err := object.Digest(r)
		if err != nil {
			continue // unreachable for well-formed receipts
		}
		evidence = append(evidence, dig)
		if r.Family != "suite" {
			continue
		}
		// M0 records one suite receipt per world; if several appear, the
		// smallest receipt digest wins so the choice is order-independent.
		if prev, ok := suiteDig[r.World]; !ok || dig < prev {
			suite[r.World] = r
			suiteDig[r.World] = dig
		}
	}
	sort.Strings(evidence)

	intent := ""
	passCount := 0
	cands := make([]candidate, 0, len(worlds))
	for i := range worlds {
		w := worlds[i]
		if intent == "" {
			intent = w.Intent
		}
		dig, _, err := object.Digest(w)
		if err != nil {
			continue // unreachable for well-formed worlds
		}
		c := candidate{digest: dig, world: w, wallMS: math.MaxInt64}
		if r, ok := suite[dig]; ok {
			r := r
			c.receipt = &r
			c.wallMS = r.Cost.WallMS
		}
		c.pass = len(failedGates(policy.HardGates, c)) == 0
		if c.pass {
			passCount++
		}
		cands = append(cands, c)
	}

	sort.Slice(cands, func(i, j int) bool { return rankLess(policy.Ranking, cands[i], cands[j]) })

	subject := make([]string, 0, len(cands))
	for _, c := range cands {
		subject = append(subject, c.digest)
	}

	d := object.Decision{
		Schema:   object.SchemaDecision,
		Intent:   intent,
		Subject:  subject,
		Evidence: evidence,
		Policy:   polDig,
	}
	gates := strings.Join(policy.HardGates, ",")
	switch {
	case passCount > 0:
		d.Type = TypeSelect
		d.Rationale = fmt.Sprintf(
			"%d/%d worlds passed hard gates [%s]; selected %s by ranking [%s] (wall_ms=%d)",
			passCount, len(cands), gates, cands[0].digest,
			strings.Join(policy.Ranking, ","), cands[0].wallMS)
	case len(cands) == 0:
		d.Type = TypeReject
		d.Rationale = "no candidate worlds"
	default:
		details := make([]string, 0, len(cands))
		for _, c := range cands {
			details = append(details, fmt.Sprintf("%s failed [%s] (%s)",
				c.digest, strings.Join(failedGates(policy.HardGates, c), ","), failReason(c)))
		}
		d.Type = TypeReject
		d.Rationale = fmt.Sprintf("0/%d worlds passed hard gates [%s]; %s",
			len(cands), gates, strings.Join(details, "; "))
	}
	return d
}

// gatePass evaluates one hard gate for a candidate. Unknown gates fail:
// what M0 cannot attest, it must not admit.
func gatePass(gate string, c candidate) bool {
	switch gate {
	case GateSuitePass:
		return c.receipt != nil && c.receipt.Result.Status == "pass"
	default:
		return false
	}
}

func failedGates(gates []string, c candidate) []string {
	failed := make([]string, 0, len(gates))
	for _, g := range gates {
		if !gatePass(g, c) {
			failed = append(failed, g)
		}
	}
	return failed
}

// failReason explains why a candidate failed, most fundamental cause first.
func failReason(c candidate) string {
	switch {
	case c.world.Outcome != OutcomeCompleted:
		return "outcome=" + c.world.Outcome
	case c.receipt == nil:
		return "no suite receipt"
	default:
		return "status=" + c.receipt.Result.Status
	}
}

// rankLess orders candidates lexicographically by the policy ranking keys
// (M0: gate_pass, wall_ms_asc), with world digest ascending as the final
// deterministic tie-break (NFR-1).
func rankLess(ranking []string, a, b candidate) bool {
	for _, key := range ranking {
		switch key {
		case "gate_pass":
			if a.pass != b.pass {
				return a.pass
			}
		case "wall_ms_asc":
			if a.wallMS != b.wallMS {
				return a.wallMS < b.wallMS
			}
		}
	}
	return a.digest < b.digest
}
