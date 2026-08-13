package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/coagente/multiverso/internal/admit"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/workspace"
)

type auditMismatch struct {
	Seq      int64  `json:"seq"`
	Decision string `json:"decision"`
	Detail   string `json:"detail"`
}

type auditReport struct {
	Schema          string          `json:"schema"`
	Events          int             `json:"events"`
	Decisions       int             `json:"decisions"`
	Admissions      int             `json:"admissions"` // decisions replayed via the admission path (M1a, additive)
	ChainOK         bool            `json:"chain_ok"`
	ReplayIdentical bool            `json:"replay_identical"`
	Mismatches      []auditMismatch `json:"mismatches"`
	Error           string          `json:"error,omitempty"`
}

const schemaAuditReport = "multiverso.dev/audit-report/v0"

// cmdAudit verifies the ledger hash chain and recomputes every recorded
// decision from its recorded evidence (NFR-1): Type, Subject, Evidence and
// Rationale must reproduce byte-for-byte.
func cmdAudit(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("audit", stderr)
	dir := fs.String("dir", ".", "repository directory")
	jsonOut := fs.Bool("json", false, "emit a machine-readable report")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	defer ws.Close()

	report := auditReport{Schema: schemaAuditReport, Mismatches: []auditMismatch{}}
	fail := func(cause error) error {
		report.Error = cause.Error()
		if *jsonOut {
			emitJSON(stdout, report)
		}
		return fmt.Errorf("audit: %w", cause)
	}

	if chainErr := ws.Ledger.VerifyChain(); chainErr != nil {
		countEvents(ws, &report) // best-effort context for the report
		return fail(chainErr)
	}
	report.ChainOK = true

	st, err := loadState(ws.Ledger)
	if err != nil {
		return fail(err)
	}
	report.Events = st.Events
	report.Decisions = len(st.Decisions)

	for _, dr := range st.Decisions {
		pol, err := race.LoadPolicy(ws.CAS, dr.Decision.Policy)
		if err != nil {
			return fail(fmt.Errorf("decision %s: %w", dr.Dig, err))
		}

		// Replay-path discrimination (M1a): a decision replays via
		// admit.Decide iff the closest admission.started for its intent is
		// nearer than the closest race.started; otherwise via race.Decide
		// (M0 behavior, including a == r == 0).
		raceStart := st.raceStartBefore(dr.Decision.Intent, dr.Seq)
		admStart := st.admissionStartBefore(dr.Decision.Intent, dr.Seq)
		var got object.Decision
		if admStart != nil && admStart.Seq > raceStart {
			report.Admissions++
			replayed, detail := replayAdmission(st, pol, dr, admStart)
			if detail != "" {
				report.Mismatches = append(report.Mismatches,
					auditMismatch{Seq: dr.Seq, Decision: dr.Dig, Detail: detail})
				continue
			}
			got = replayed
		} else {
			worldRecs := st.worldsFor(dr.Decision.Intent, raceStart, dr.Seq)
			worldDigs := make(map[string]bool, len(worldRecs))
			worlds := make([]object.World, 0, len(worldRecs))
			for _, wr := range worldRecs {
				worldDigs[wr.Dig] = true
				worlds = append(worlds, wr.World)
			}
			receipts := make([]object.Receipt, 0)
			for _, rr := range st.receiptsFor(worldDigs, raceStart, dr.Seq) {
				receipts = append(receipts, rr.Receipt)
			}
			got = race.Decide(pol, worlds, receipts)
		}

		if detail := diffDecision(dr.Decision, got); detail != "" {
			report.Mismatches = append(report.Mismatches,
				auditMismatch{Seq: dr.Seq, Decision: dr.Dig, Detail: detail})
		}
	}
	report.ReplayIdentical = len(report.Mismatches) == 0

	if *jsonOut {
		if err := emitJSON(stdout, report); err != nil {
			return fmt.Errorf("audit: %w", err)
		}
	} else if report.ReplayIdentical {
		// The OK line appears iff the replay is identical (M0 CLI contract).
		fmt.Fprintf(stdout, "OK: %d events, %d decisions replayed\n", report.Events, report.Decisions)
	} else {
		for _, m := range report.Mismatches {
			fmt.Fprintf(stdout, "DIVERGED: seq %d decision %s: %s\n", m.Seq, m.Decision, m.Detail)
		}
	}
	if !report.ReplayIdentical {
		return fmt.Errorf("audit: replay diverged on %d of %d decisions", len(report.Mismatches), report.Decisions)
	}
	return nil
}

// replayAdmission recomputes an admission decision from the ledger scan
// and CAS alone — no git, no clock (NFR-1). Window receipts are the
// receipt.recorded events between the admission.started and the decision
// whose World is the SELECT winner: the smallest-digest landing-apply
// receipt and the smallest-digest suite receipt (nil if none). A non-empty
// detail is a mismatch entry, not an error.
func replayAdmission(st *ledgerState, pol object.Policy, dr decisionRec, admStart *admissionStartRec) (object.Decision, string) {
	var sel *object.Decision
	for i := range st.Decisions {
		d := &st.Decisions[i]
		if d.Dig == admStart.SelectDecision && d.Decision.Type == race.TypeSelect {
			sel = &d.Decision
			break
		}
	}
	if sel == nil || len(sel.Subject) == 0 {
		return object.Decision{}, fmt.Sprintf("select decision %s not in ledger", admStart.SelectDecision)
	}
	winner := sel.Subject[0]

	var apply, gate *object.Receipt
	var applyDig, gateDig string
	for i := range st.Receipts {
		rr := &st.Receipts[i]
		if rr.Seq <= admStart.Seq || rr.Seq >= dr.Seq || rr.Receipt.World != winner {
			continue
		}
		if rr.Receipt.Oracle.ID == admit.OracleIDLandingApply && (apply == nil || rr.Dig < applyDig) {
			apply, applyDig = &rr.Receipt, rr.Dig
		}
		if rr.Receipt.Family == "suite" && (gate == nil || rr.Dig < gateDig) {
			gate, gateDig = &rr.Receipt, rr.Dig
		}
	}
	if apply == nil {
		return object.Decision{}, fmt.Sprintf(
			"no landing-apply receipt for winner %s between seq %d and %d", winner, admStart.Seq, dr.Seq)
	}
	return admit.Decide(pol, dr.Decision.Intent, winner, *apply, gate), ""
}

// diffDecision compares the replay-deterministic fields (NFR-1); CreatedAt
// is a record of when the original decision happened and is excluded.
func diffDecision(recorded, replayed object.Decision) string {
	switch {
	case recorded.Type != replayed.Type:
		return fmt.Sprintf("type: recorded %q, replayed %q", recorded.Type, replayed.Type)
	case !slices.Equal(recorded.Subject, replayed.Subject):
		return fmt.Sprintf("subject: recorded %v, replayed %v", recorded.Subject, replayed.Subject)
	case !slices.Equal(recorded.Evidence, replayed.Evidence):
		return fmt.Sprintf("evidence: recorded %v, replayed %v", recorded.Evidence, replayed.Evidence)
	case recorded.Rationale != replayed.Rationale:
		return fmt.Sprintf("rationale: recorded %q, replayed %q", recorded.Rationale, replayed.Rationale)
	}
	return ""
}

func emitJSON(w io.Writer, report any) error {
	b, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}

// countEvents fills report.Events even when chain verification failed, so
// the JSON report still says how much ledger was examined.
func countEvents(ws *workspace.Workspace, report *auditReport) {
	n := 0
	if err := ws.Ledger.Scan(func(ledger.Event) error { n++; return nil }); err == nil {
		report.Events = n
	}
}
