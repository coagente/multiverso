// Package admit implements the M1a admission orchestrator (CP-8, EP-3,
// TP-1) and the pure admission gate (CP-6, NFR-1): landing the SELECT
// winner on trunk behind recomputed gates, with a signed attestation.
package admit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/race"
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

// Decide is the pure admission gate (CP-6, NFR-1): Type, Subject,
// Evidence, Rationale depend only on (policy, intent, world, apply, gate).
// gate is nil when the apply conflicted. CreatedAt is left empty for the
// recorder — it must not influence the decision. It performs no I/O and
// reads no clock; audit replay reproduces it byte-for-byte from recorded
// receipts alone.
func Decide(policy object.Policy, intent, world string,
	apply object.Receipt, gate *object.Receipt) object.Decision {
	polDig, _, _ := object.Digest(policy)
	applyDig, _, _ := object.Digest(apply)

	d := object.Decision{
		Schema:  object.SchemaDecision,
		Intent:  intent,
		Subject: []string{world},
		Policy:  polDig,
	}

	// CP-8: a conflicted landing is never resolved, always escalated; the
	// conflict set lives in the apply receipt's artifacts.
	if apply.Result.Status != "pass" {
		d.Type = TypeEscalate
		d.Evidence = []string{applyDig}
		d.Rationale = fmt.Sprintf(
			"landing apply of %s onto trunk tree %s failed (exit %d); conflicts are never auto-resolved (CP-8) — conflict set in apply receipt artifacts",
			world, apply.Freshness.ValidFor.Tree, apply.Execution.ExitCode)
		return d
	}

	evidence := []string{applyDig}
	if gate != nil {
		gateDig, _, _ := object.Digest(*gate)
		evidence = append(evidence, gateDig)
	}
	sort.Strings(evidence)
	d.Evidence = evidence

	gates := strings.Join(policy.HardGates, ",")
	tree := apply.Freshness.ValidFor.Tree
	if gate != nil {
		tree = gate.Freshness.ValidFor.Tree
	}

	// Gate evaluation, restricted to the landing suite receipt. Unknown
	// gates fail: what it cannot attest, it must not admit. A gate receipt
	// with status "error" (timeout/spawn) rejects, never admits.
	var details []string
	for _, g := range policy.HardGates {
		switch g {
		case race.GateSuitePass:
			switch {
			case gate == nil:
				details = append(details, "suite-pass (no landing suite receipt)")
			case gate.Result.Status != "pass":
				details = append(details, fmt.Sprintf("suite-pass (status=%s)", gate.Result.Status))
			}
		default:
			details = append(details, fmt.Sprintf("%s (unknown gate)", g))
		}
	}

	if len(details) == 0 {
		d.Type = TypeAdmit
		d.Rationale = fmt.Sprintf("landing gates [%s] passed on tree %s; admitting %s", gates, tree, world)
		return d
	}
	d.Type = TypeReject
	d.Rationale = fmt.Sprintf("landing gates [%s] failed on tree %s; %s", gates, tree, strings.Join(details, ", "))
	return d
}
