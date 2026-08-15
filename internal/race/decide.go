// Package race implements the M0 fixed orchestrator (CP-2, CP-3) and the
// pure decision function (CP-5, CP-6, NFR-1).
package race

import (
	"fmt"
	"math"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// Decision types (decision/v0). ESCALATE is a first-class race outcome
// since M1e: a recorded ESCALATE is a product, not an error.
const (
	TypeSelect   = "SELECT"
	TypeReject   = "REJECT"
	TypeEscalate = "ESCALATE"
)

// World outcomes — aliases of the object constants (M0 API compatibility;
// the full six-value taxonomy lives in internal/object since M1b).
const (
	OutcomeCompleted   = object.OutcomeCompleted
	OutcomeConfigError = object.OutcomeConfigError
)

// GateSuitePass is M0's single hard gate, kept as an alias of the policy
// vocabulary constant for the callers M0 shipped with.
const GateSuitePass = policy.GateSuitePass

// Decide is the race decision function (CP-6). It is pure, TOTAL and
// order-independent: Type, Subject, Evidence and Rationale depend only on
// (pol, worlds, receipts), so audit replay reproduces them byte-for-byte
// (NFR-1). CreatedAt is left empty for the recorder to stamp — it must not
// influence the decision.
//
// Inputs carry the digests they were RECORDED under: nothing here is
// re-derived from a re-serialization of a decoded object, which is what
// lets M1e add fields to World and Receipt and still replay pre-M1e
// ledgers byte-for-byte (M1e decision 1).
func Decide(pol policy.Policy, worlds []object.RecordedWorld, receipts []object.RecordedReceipt) object.Decision {
	t := Trace(pol, worlds, receipts)
	subject := make([]string, 0, len(t.Candidates))
	for i := range t.Candidates {
		subject = append(subject, t.Candidates[i].World)
	}
	return object.Decision{
		Schema:    object.SchemaDecision,
		Type:      t.Type,
		Intent:    t.Intent,
		Subject:   subject,
		Evidence:  t.Evidence,
		Policy:    pol.Digest,
		Rationale: t.Rationale,
	}
}

// rationale renders the decision sentence in the dialect frozen for the
// policy's schema version (M1e decision 3). An ESCALATE wraps the
// SELECT/REJECT sentence that would otherwise have been emitted, so the
// human sees both the rule and the verdict it displaced.
func rationale(pol policy.Policy, t *RaceTrace, base string) string {
	body := rationaleV1(pol, t, base)
	if pol.Dialect == policy.DialectV0 {
		body = rationaleV0(pol, t, base)
	}
	if t.Escalation.Rule == "" {
		return body
	}
	return fmt.Sprintf("escalated by policy rule %s: %s; %s", t.Escalation.Rule, t.Escalation.Detail, body)
}

// rationaleV1 is the M1e sentence: it names the decisive ranking key, which
// is the whole point of a lexicographic spec.
func rationaleV1(pol policy.Policy, t *RaceTrace, base string) string {
	gates := strings.Join(t.Gates, ",")
	keys := strings.Join(t.Keys, ",")
	switch {
	case base == TypeSelect && t.PassCount >= 2:
		c := t.Comparisons[0]
		return fmt.Sprintf(
			"%d/%d worlds passed hard gates [%s]; selected %s over %s at ranking key %d %s (%s); ranking [%s]",
			t.PassCount, len(t.Candidates), gates, t.Winner, c.Other, c.DecidedAt, c.Key, c.Text, keys)
	case base == TypeSelect:
		return fmt.Sprintf(
			"%d/%d worlds passed hard gates [%s]; selected %s (sole world passing all hard gates); ranking [%s]",
			t.PassCount, len(t.Candidates), gates, t.Winner, keys)
	case len(t.Candidates) == 0:
		return "no candidate worlds"
	default:
		details := make([]string, 0, len(t.Candidates))
		for i := range t.Candidates {
			c := &t.Candidates[i]
			// A world that cleared every gate and still did not pass was
			// stopped by an invariant, and the sentence must say which.
			// Emitted only on inputs no M1e policy can produce (M1f
			// decision 3's compatibility rule).
			if c.failIdx < 0 {
				if clause := invariantClause(c); clause != "" {
					details = append(details, clause)
					continue
				}
			}
			label, reason := "", ""
			if c.failIdx >= 0 {
				label, reason = c.Gates[c.failIdx].Label, c.Gates[c.failIdx].Detail
			}
			details = append(details, fmt.Sprintf("%s failed [%s] (%s)", c.World, label, reason))
		}
		return fmt.Sprintf("0/%d worlds passed hard gates [%s]; %s",
			len(t.Candidates), gates, strings.Join(details, "; "))
	}
}

// rationaleV0 reproduces M0's two format strings character for character.
// A policy pinned in the past keeps the meaning it was pinned with, and
// audit compares the recorded sentence byte-for-byte.
func rationaleV0(pol policy.Policy, t *RaceTrace, base string) string {
	gates := strings.Join(t.Gates, ",")
	switch {
	case base == TypeSelect:
		return fmt.Sprintf("%d/%d worlds passed hard gates [%s]; selected %s by ranking [%s] (wall_ms=%d)",
			t.PassCount, len(t.Candidates), gates, t.Winner,
			strings.Join(pol.Ranking, ","), suiteWallMS(&t.Candidates[0]))
	case len(t.Candidates) == 0:
		return "no candidate worlds"
	default:
		details := make([]string, 0, len(t.Candidates))
		for i := range t.Candidates {
			c := &t.Candidates[i]
			failed := make([]string, 0, len(c.Gates))
			for _, g := range c.Gates {
				if g.Result == policy.GateFail {
					failed = append(failed, g.Label)
				}
			}
			details = append(details, fmt.Sprintf("%s failed [%s] (%s)",
				c.World, strings.Join(failed, ","), failReasonV0(c, c.counted[policy.Selector{Family: policy.FamilySuite}])))
		}
		return fmt.Sprintf("0/%d worlds passed hard gates [%s]; %s",
			len(t.Candidates), gates, strings.Join(details, "; "))
	}
}

// suiteWallMS is M0's wall_ms for a candidate: its suite receipt's cost, or
// MaxInt64 when it produced none — the value M0 printed and ranked by.
func suiteWallMS(c *CandidateTrace) int64 {
	if cr := c.counted[policy.Selector{Family: policy.FamilySuite}]; cr.rec != nil {
		return cr.rec.Cost.WallMS
	}
	return math.MaxInt64
}
