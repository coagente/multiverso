package main

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/coagente/multiverso/internal/admit"
	"github.com/coagente/multiverso/internal/audit"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/signing"
	"github.com/coagente/multiverso/internal/workspace"
)

type auditMismatch struct {
	Seq      int64  `json:"seq"`
	Decision string `json:"decision"`
	Detail   string `json:"detail"`
}

type auditReport struct {
	Schema          string `json:"schema"`
	Events          int    `json:"events"`
	Decisions       int    `json:"decisions"`
	Admissions      int    `json:"admissions"` // decisions replayed via the admission path (M1a, additive)
	ChainOK         bool   `json:"chain_ok"`
	ReplayIdentical bool   `json:"replay_identical"`
	// M1f: the CAS integrity sweep over everything the ledger references.
	// There is deliberately NO field claiming the closure is complete with
	// respect to the world — audit verifies what was recorded, and cannot
	// verify that something was recorded which never was.
	CASChecked           int             `json:"cas_checked"`
	CASMissing           []audit.Problem `json:"cas_missing"`
	CASCorrupt           []audit.Problem `json:"cas_corrupt"`
	CASUnreferenced      int             `json:"cas_unreferenced"`
	AttestationsChecked  int             `json:"attestations_checked"`
	AttestationsVerified int             `json:"attestations_verified"`
	// AttestationsVerifiedAgainst names the trust anchor the signatures
	// were checked with: "workspace" is a SELF-check that a rogue clone
	// reproduces byte-for-byte, and the study documented exactly that
	// confusion ("a fresh workspace signing with its own key produces a
	// visually identical OK"). A reader must be able to tell the two apart
	// without reading the source.
	AttestationsVerifiedAgainst string          `json:"attestations_verified_against"`
	Mismatches                  []auditMismatch `json:"mismatches"`
	Error                       string          `json:"error,omitempty"`
}

// The schema bumps to v1: a report, not a content-addressed object, so
// there are no replay implications — but scripts pinning the string must
// update, which is why it moves rather than silently gaining fields.
const schemaAuditReport = "multiverso.dev/audit-report/v1"

// cmdAudit verifies the ledger hash chain and recomputes every recorded
// decision from its recorded evidence (NFR-1): Type, Subject, Evidence and
// Rationale must reproduce byte-for-byte.
func cmdAudit(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("audit", stderr)
	dir := fs.String("dir", ".", "repository directory")
	jsonOut := fs.Bool("json", false, "emit a machine-readable report")
	// The CI knob decision 23 argues for: "nothing verified" keeps exit 0
	// and keeps saying so in words, because `audit` is the obvious verb to
	// wire into a smoke test and redefining it for everyone is not the fix
	// for an over-claim. A required minimum is stated explicitly instead.
	requireDecisions := fs.Int("require-decisions", 0, "fail unless at least N decisions replay")
	casSweep := fs.Bool("cas-sweep", true, "re-read and re-hash every CAS object the ledger references")
	// The trust anchor, spelled the same way `mvo verify --key` and
	// `mvo fetch-race --key` spell it. Without it audit can only check the
	// workspace's signatures against the workspace's OWN key, which a rogue
	// clone reproduces — a self-check, and the output must say so.
	keyPath := fs.String("key", "", "trusted public key PEM (default: the workspace's own key — a SELF-check)")
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
		// Historical policies replay from CAS, whatever schema version they
		// were written in: CAS is never pruned, so an intent pinned in the
		// past always resolves (M1e decision 2).
		pol, err := policy.Load(ws.CAS, dr.Decision.Policy)
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
			worlds := make([]object.RecordedWorld, 0, len(worldRecs))
			for _, wr := range worldRecs {
				worldDigs[wr.Dig] = true
				// The ledger's own digest, never a re-serialization of the
				// decoded struct (M1e decision 1) — which is why a ledger
				// written before M1e still replays byte-for-byte here.
				worlds = append(worlds, object.RecordedWorld{Digest: wr.Dig, World: wr.World})
			}
			receipts := make([]object.RecordedReceipt, 0)
			for _, rr := range st.receiptsFor(worldDigs, raceStart, dr.Seq) {
				receipts = append(receipts, object.RecordedReceipt{Digest: rr.Dig, Receipt: rr.Receipt})
			}
			got = race.Decide(pol, worlds, receipts)
		}

		if detail := diffDecision(dr.Decision, got); detail != "" {
			report.Mismatches = append(report.Mismatches,
				auditMismatch{Seq: dr.Seq, Decision: dr.Dig, Detail: detail})
		}
	}
	report.ReplayIdentical = len(report.Mismatches) == 0

	// The sweep: everything the ledger references, re-read and re-hashed.
	sweepRan := false
	if *casSweep {
		sweep, err := audit.Sweep(ws.Ledger, ws.CAS)
		if err != nil {
			return fail(err)
		}
		sweepRan = true
		report.CASChecked = sweep.Checked
		report.CASMissing = sweep.Missing
		report.CASCorrupt = sweep.Corrupt
		report.CASUnreferenced = sweep.Unreferenced
		checked, verified, against, err := verifyAttestations(ws, *keyPath)
		if err != nil {
			return fail(err)
		}
		report.AttestationsChecked, report.AttestationsVerified = checked, verified
		report.AttestationsVerifiedAgainst = against
	}
	if report.CASMissing == nil {
		report.CASMissing = []audit.Problem{}
	}
	if report.CASCorrupt == nil {
		report.CASCorrupt = []audit.Problem{}
	}
	casOK := len(report.CASMissing) == 0 && len(report.CASCorrupt) == 0
	// The block's own rule: a skipped check must never render identically
	// to a passed one, and the OK line appears iff every check this
	// invocation was asked for passed. --require-decisions is one of those
	// checks, so it belongs in the guard and not in a switch after the
	// line has already been printed: any CI check keyed on `OK:` would
	// otherwise read a failed audit as a pass.
	requireOK := *requireDecisions <= 0 || report.Decisions >= *requireDecisions

	if *jsonOut {
		if err := emitJSON(stdout, report); err != nil {
			return fmt.Errorf("audit: %w", err)
		}
	} else if report.ReplayIdentical && casOK && requireOK {
		// The OK line appears iff the replay is identical (M0 CLI contract).
		// Zero decisions is a vacuous pass — nothing was verified because
		// there was nothing to verify — and it must not look like the real
		// thing. `audit` is the obvious verb to wire into a required check,
		// and as shipped it exits 0 on any workspace with no races.
		scope := ""
		if report.Decisions == 0 {
			// A vacuous pass must not look like the real thing: nothing was
			// verified because there was nothing to verify, and the line
			// says so in words.
			scope = " (no races in this workspace — nothing was decided)"
		}
		// A skipped check must never render identically to a passed one:
		// that IS the over-claim being removed.
		sweepText := fmt.Sprintf(", %d CAS objects verified", report.CASChecked)
		if !sweepRan {
			sweepText = " (CAS sweep skipped)"
		} else if report.AttestationsVerified > 0 {
			// Naming the anchor is the whole point: "verified" against the
			// workspace's own key is a self-check, and the study found a
			// rogue clone producing a visually identical OK.
			sweepText += fmt.Sprintf(", %d attestation signature(s) verified against the %s key",
				report.AttestationsVerified, report.AttestationsVerifiedAgainst)
		}
		fmt.Fprintf(stdout, "OK: %d events, %d decisions replayed%s%s\n",
			report.Events, report.Decisions, sweepText, scope)
	} else {
		for _, m := range report.Mismatches {
			fmt.Fprintf(stdout, "DIVERGED: seq %d decision %s: %s\n", m.Seq, m.Decision, m.Detail)
		}
		for _, m := range report.CASMissing {
			fmt.Fprintf(stdout, "MISSING: %s referenced by %s\n", m.Key, m.Referrer)
		}
		for _, m := range report.CASCorrupt {
			fmt.Fprintf(stdout, "CORRUPT: %s %s, referenced by %s\n", m.Key, m.Detail, m.Referrer)
		}
		if !requireOK {
			fmt.Fprintf(stdout, "SHORTFALL: %d decisions replayed, --require-decisions %d\n",
				report.Decisions, *requireDecisions)
		}
	}
	switch {
	case !report.ReplayIdentical:
		return fmt.Errorf("audit: replay diverged on %d of %d decisions", len(report.Mismatches), report.Decisions)
	case len(report.CASMissing) > 0 || len(report.CASCorrupt) > 0:
		return fmt.Errorf("audit: CAS sweep found %d missing and %d corrupt of %d referenced objects",
			len(report.CASMissing), len(report.CASCorrupt), report.CASChecked)
	case *requireDecisions > 0 && report.Decisions < *requireDecisions:
		return fmt.Errorf("audit: %d decisions replayed, --require-decisions %d",
			report.Decisions, *requireDecisions)
	}
	return nil
}

// verifyAttestations re-verifies every recorded DSSE bundle against a
// trust anchor: the PEM at keyPath when the operator supplied one, the
// workspace's own key otherwise. `mvo verify` remains THE attestation verb
// and its seven checks are unchanged; audit's job here is only "is the
// closure the ledger points at intact and self-consistent".
//
// against is the anchor's name, and it is reported rather than assumed:
// "workspace" means the ledger was checked against the key that wrote it,
// which a rogue clone reproduces exactly. Only an external key makes
// "verified" a statement about provenance.
func verifyAttestations(ws *workspace.Workspace, keyPath string) (checked, verified int, against string, err error) {
	var bundles []string
	if scanErr := ws.Ledger.Scan(func(e ledger.Event) error {
		if e.Type != "attestation.recorded" {
			return nil
		}
		if key := eventString(e.Payload, "bundle"); key != "" {
			bundles = append(bundles, key)
		}
		return nil
	}); scanErr != nil {
		return 0, 0, "", scanErr
	}
	var pub ed25519.PublicKey
	switch {
	case keyPath != "":
		// An unreadable trust anchor is an operator error, not a quiet
		// downgrade to the self-check: refusing here is the only way
		// --key can mean anything.
		p, _, kerr := signing.LoadPublicKeyFile(keyPath)
		if kerr != nil {
			return 0, 0, "", fmt.Errorf("--key %s: %w", keyPath, kerr)
		}
		pub, against = p, "supplied"
	default:
		signer, signerErr := ws.Signer()
		if signerErr == nil {
			pub, against = signer.Public, "workspace"
		}
	}
	for _, key := range bundles {
		checked++
		if pub == nil {
			continue // no key to verify against; counted, never claimed
		}
		raw, getErr := ws.CAS.Get(key)
		if getErr != nil {
			continue // already reported by the sweep as missing or corrupt
		}
		var env signing.Envelope
		if json.Unmarshal(raw, &env) != nil {
			continue
		}
		if _, verr := signing.Verify(env, pub); verr == nil {
			verified++
		}
	}
	if checked == 0 {
		against = ""
	}
	return checked, verified, against, nil
}

// eventString pulls one top-level string out of an event payload.
func eventString(payload []byte, name string) string {
	var body map[string]any
	if json.Unmarshal(payload, &body) != nil {
		return ""
	}
	s, _ := body[name].(string)
	return s
}

// replayAdmission recomputes an admission decision from the ledger scan
// and CAS alone — no git, no clock (NFR-1). Window receipts are the
// receipt.recorded events between the admission.started and the decision
// whose World is the SELECT winner: the smallest-digest landing-apply
// receipt, plus the smallest-digest receipt matching each of the policy's
// gate selectors. A non-empty detail is a mismatch entry, not an error.
func replayAdmission(st *ledgerState, pol policy.Policy, dr decisionRec, admStart *admissionStartRec) (object.Decision, string) {
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

	var apply *object.RecordedReceipt
	gates := make(map[policy.Selector]object.RecordedReceipt, len(pol.Gates))
	for i := range st.Receipts {
		rr := &st.Receipts[i]
		if rr.Seq <= admStart.Seq || rr.Seq >= dr.Seq || rr.Receipt.World != winner {
			continue
		}
		rec := object.RecordedReceipt{Digest: rr.Dig, Receipt: rr.Receipt}
		if rr.Receipt.Oracle.ID == admit.OracleIDLandingApply && (apply == nil || rr.Dig < apply.Digest) {
			r := rec
			apply = &r
			continue
		}
		for _, g := range pol.Gates {
			if !g.Sel.Match(rr.Receipt) {
				continue
			}
			if prev, ok := gates[g.Sel]; !ok || rr.Dig < prev.Digest {
				gates[g.Sel] = rec
			}
		}
	}
	if apply == nil {
		return object.Decision{}, fmt.Sprintf(
			"no landing-apply receipt for winner %s between seq %d and %d", winner, admStart.Seq, dr.Seq)
	}
	gateRecs := make([]object.RecordedReceipt, 0, len(gates))
	for _, g := range gates {
		gateRecs = append(gateRecs, g)
	}
	sort.Slice(gateRecs, func(i, j int) bool { return gateRecs[i].Digest < gateRecs[j].Digest })
	return admit.Decide(pol, dr.Decision.Intent, winner, *apply, gateRecs), ""
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
