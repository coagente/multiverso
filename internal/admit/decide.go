// Package admit implements the M1a admission orchestrator (CP-8, EP-3,
// TP-1) and the pure admission gate (CP-6, NFR-1): landing the SELECT
// winner on trunk behind recomputed gates, with a signed attestation.
package admit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// Oracle/receipt identifiers for the landing-apply receipt.
const (
	OracleIDLandingApply = "landing-apply"
	FamilyLandingApply   = "landing-apply"
)

// Decision types this package can emit (decision/v0).
const (
	TypeAdmit    = "ADMIT"
	TypeReject   = "REJECT"
	TypeEscalate = "ESCALATE"
)

// Decide is the pure admission gate (CP-6, NFR-1): Type, Subject, Evidence
// and Rationale depend only on (pol, intent, world, apply, gates). gates
// carries one receipt per hard-gate oracle of the PINNED policy — M1a's
// single gate generalizes, and the landing gates now come from the same
// attested artifact the race was judged under rather than from receipt
// archaeology (M1e decision 20). It is empty when the apply conflicted.
//
// CreatedAt is left empty for the recorder — it must not influence the
// decision. Decide performs no I/O and reads no clock; audit replay
// reproduces it byte-for-byte from recorded receipts alone.
//
// Escalation rules are NOT evaluated here: admission has one subject and no
// ranking, and CP-8's conflict path is the only escalation it knows.
func Decide(pol policy.Policy, intent, world string,
	apply object.RecordedReceipt, gates []object.RecordedReceipt) object.Decision {

	d := object.Decision{
		Schema:  object.SchemaDecision,
		Intent:  intent,
		Subject: []string{world},
		Policy:  pol.Digest,
	}

	// CP-8: a conflicted landing is never resolved, always escalated; the
	// conflict set lives in the apply receipt's artifacts.
	if apply.Receipt.Result.Status != "pass" {
		d.Type = TypeEscalate
		d.Evidence = []string{apply.Digest}
		d.Rationale = fmt.Sprintf(
			"landing apply of %s onto trunk tree %s failed (exit %d); conflicts are never auto-resolved (CP-8) — conflict set in apply receipt artifacts",
			world, apply.Receipt.Freshness.ValidFor.Tree, apply.Receipt.Execution.ExitCode)
		return d
	}

	// Fail closed on a policy that gates nothing. An empty gate list makes
	// every predicate loop below vacuously true, so the landing would be
	// ADMITTED — and signed, and attested — on the strength of no gate at
	// all. What cannot be attested must not be admitted; the ingest boundary
	// refuses such a policy, and this is the second lock on the same door
	// (only a v0 policy can express it, and only by having been pinned
	// before that refusal existed).
	if len(pol.Gates) == 0 {
		d.Type = TypeReject
		d.Evidence = []string{apply.Digest}
		d.Rationale = fmt.Sprintf("landing gates [] failed on tree %s; policy %s declares no hard gate: an unattested landing is not an admission",
			apply.Receipt.Freshness.ValidFor.Tree, pol.Digest)
		return d
	}

	evidence := make([]string, 0, len(gates)+1)
	evidence = append(evidence, apply.Digest)
	for _, g := range gates {
		evidence = append(evidence, g.Digest)
	}
	sort.Strings(evidence)
	d.Evidence = evidence

	labels := strings.Join(pol.GateLabels(), ",")

	// One admission lands ONE tree: gate receipts that disagree about which
	// tree they judged are not evidence about a landing, they are noise.
	tree := apply.Receipt.Freshness.ValidFor.Tree
	if len(gates) > 0 {
		tree = gates[0].Receipt.Freshness.ValidFor.Tree
		for _, g := range gates {
			if g.Receipt.Freshness.ValidFor.Tree != tree || tree == "" {
				d.Type = TypeReject
				d.Rationale = fmt.Sprintf("landing gates [%s] failed on tree %s; %s",
					labels, apply.Receipt.Freshness.ValidFor.Tree,
					"landing gate receipts disagree on the landing tree")
				return d
			}
		}
	}

	// M1f: a violated invariant on the LANDING evidence is an ESCALATE,
	// evaluated before the ADMIT/REJECT split and reachable only when the
	// pinned policy declares invariants (so no M1a or M1e admission can
	// reach this text). At admission there is one subject and no ranking,
	// so REJECT would read "this candidate is bad" when what happened is
	// "the landing tree's evidence contradicts itself" — and a
	// contradiction on the tree that is about to become trunk must reach a
	// human. CP-8's ESCALATE is the existing shape for exactly that.
	for _, inv := range pol.Invariants {
		holds, detail := policy.Holds(inv, func(role, metric string) (int64, bool) {
			sel, ok := inv.Roles[role]
			if !ok {
				return 0, false
			}
			rec := counted(sel, gates)
			if rec == nil {
				return 0, false
			}
			v, present := rec.Result.Metrics[metric]
			return v, present
		})
		if holds {
			continue
		}
		d.Type = TypeEscalate
		d.Rationale = fmt.Sprintf("landing evidence violates invariant %s (%s)", inv.Name, detail)
		return d
	}

	// Gate evaluation over the landing receipts. No short-circuit: there is
	// one candidate and the operator deserves the full gate picture.
	var details []string
	for _, g := range pol.Gates {
		rec := counted(g.Sel, gates)
		ok, reason := g.Eval(rec)
		if ok {
			continue
		}
		details = append(details, fmt.Sprintf("%s (%s)", g.Label, admitReason(pol, g, rec, reason)))
	}

	if len(details) == 0 {
		d.Type = TypeAdmit
		d.Rationale = fmt.Sprintf("landing gates [%s] passed on tree %s; admitting %s", labels, tree, world)
		return d
	}
	d.Type = TypeReject
	d.Rationale = fmt.Sprintf("landing gates [%s] failed on tree %s; %s", labels, tree, strings.Join(details, ", "))
	return d
}

// admitReason renders a failed landing gate's reason. The v0 dialect
// reproduces M1a's sentences byte-for-byte — a policy pinned then must mean
// then what it meant then, and audit compares rationales byte-for-byte.
func admitReason(pol policy.Policy, g policy.Gate, rec *object.Receipt, reason string) string {
	if pol.Dialect != policy.DialectV0 {
		return reason
	}
	switch {
	case g.AlwaysFails:
		return "unknown gate"
	case rec == nil:
		return "no landing suite receipt"
	default:
		// "status=fail" / "status=error" are already M1a's words; anything
		// else (a basis too weak for the gate) is reported as it happened
		// rather than dressed up as a status.
		return reason
	}
}

// counted picks a selector's landing receipt: the smallest-digest match, so
// the choice is order-independent — the same disambiguation race.Decide
// uses. Landing receipts are NOT world-bound: they judge the landing tree,
// which is by construction not the world's tree (EP-3).
func counted(sel policy.Selector, gates []object.RecordedReceipt) *object.Receipt {
	best := ""
	var out *object.Receipt
	for i := range gates {
		if !sel.Match(gates[i].Receipt) {
			continue
		}
		if best == "" || gates[i].Digest < best {
			best = gates[i].Digest
			out = &gates[i].Receipt
		}
	}
	return out
}
