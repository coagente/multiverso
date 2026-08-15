package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/admit"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/workspace"
)

// cmdAdmit wires the workspace into the admission orchestrator (CP-8,
// EP-3, TP-1): land the latest SELECT winner on the checked-out trunk
// branch behind recomputed gates, with a signed attestation trailer. Exits
// 0 iff the ADMIT decision landed; ESCALATE, REJECT, and errors exit 1.
func cmdAdmit(args []string, stdout, stderr io.Writer) error {
	digArg, rest := splitDigestArg(args)
	fs := newFlagSet("admit", stderr)
	dir := fs.String("dir", ".", "repository directory")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	digArg, err := positionalDigest(digArg, fs, "admit")
	if err != nil {
		return err
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("admit: %w", err)
	}
	defer ws.Close()

	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("admit: %w", err)
	}
	intentDig, intent, err := st.resolveIntent(digArg)
	if err != nil {
		return fmt.Errorf("admit: %w", err)
	}

	// One admission per intent: refuse when an ADMIT already landed.
	// Re-admission after a failed landing (result ERROR) is allowed.
	for _, fin := range st.AdmissionFinishes {
		if fin.Intent == intentDig && fin.Result == admit.TypeAdmit {
			return fmt.Errorf("admit: intent already admitted (commit %s)", fin.Commit)
		}
	}

	// Latest SELECT decision for the intent.
	var sel *decisionRec
	for i := range st.Decisions {
		d := &st.Decisions[i]
		if d.Decision.Intent == intentDig && d.Decision.Type == race.TypeSelect {
			sel = d
		}
	}
	if sel == nil {
		return fmt.Errorf("admit: no SELECT decision for intent %s (run \"mvo race\" first)", intentDig)
	}

	signer, err := ws.Signer()
	if err != nil {
		return fmt.Errorf("admit: %w", err)
	}

	// No oracle is constructed here: the landing gates come from the intent's
	// pinned policy (v1) or, for a v0 policy, from the race receipt the
	// admission itself recovers (M1e decision 20) — one fewer place where an
	// operator's environment could decide what a gate means.
	res, err := admit.Run(context.Background(), admit.Config{
		Repo:          ws.Root,
		Ledger:        ws.Ledger,
		CAS:           ws.CAS,
		Intent:        intentDig,
		SelectDig:     sel.Dig,
		Signer:        signer,
		AdmitDir:      ws.AdmitDir(),
		OracleTimeout: time.Duration(intent.Budget.MaxWallMS) * time.Millisecond,
	})
	if err != nil {
		return fmt.Errorf("admit: %w", err)
	}
	if res.Decision.Type != admit.TypeAdmit {
		// The frozen rationale points at "conflict set in apply receipt
		// artifacts" — a CAS blob no verb in M1 can print. The two lines
		// of git output that actually say what conflicted are right there
		// in the receipt, so show them instead of sending the operator
		// hex-diving.
		writeApplyOutput(stderr, ws, res.ApplyReceipt)
		return fmt.Errorf("admit: %s: %s", res.Decision.Type, res.Decision.Rationale)
	}

	fmt.Fprintf(stdout, "ADMIT %s\n", res.Decision.Subject[0])
	fmt.Fprintf(stdout, "commit:      %s\n", res.Commit)
	fmt.Fprintf(stdout, "attestation: %s\n", res.AttestationKey)
	fmt.Fprintf(stdout, "decision:    %s\n", res.DecisionDigest)

	// AFTER the success block, and deliberately not in the `mvo: <verb>:`
	// shape every fatal error in this binary uses: this admission
	// SUCCEEDED and exits 0, and printing it first in the error dialect
	// made a working admission read as a failure that somehow returned 0.
	if !res.WorktreeSynced {
		writeSyncNote(stderr, res)
	}
	return nil
}

// writeApplyOutput prints the landing-apply receipt's captured stdout and
// stderr — git's own account of what conflicted. Best effort: a receipt
// that cannot be read costs the operator nothing beyond the rationale they
// already have, so every failure here is silent.
func writeApplyOutput(w io.Writer, ws *workspace.Workspace, receiptDig string) {
	if receiptDig == "" {
		return
	}
	b, err := ws.GetObject(receiptDig)
	if err != nil {
		return
	}
	var rec object.Receipt
	if err := json.Unmarshal(b, &rec); err != nil {
		return
	}
	printed := false
	for _, key := range rec.Result.Artifacts {
		out, err := ws.CAS.Get(key)
		if err != nil || len(bytes.TrimSpace(out)) == 0 {
			continue
		}
		if !printed {
			fmt.Fprintf(w, "git apply output (receipt %s):\n", receiptDig)
			printed = true
		}
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
}

// writeSyncNote explains the state an unsynced admission leaves behind.
// The hazard is not "your tree is out of date": trunk moved to a commit
// that contains the fix, while the index still holds the old content, so
// `git status` shows the admitted change STAGED IN REVERSE. A reflexive
// `git commit` there reships the exact bug that was just signed as fixed.
// The trigger is a tree that was not clean — an untracked file alone is
// enough; no tracked file need be modified.
func writeSyncNote(w io.Writer, res *admit.Result) {
	if res.SyncError != "" {
		fmt.Fprintf(w, "note: trunk advanced to %s but syncing your working tree failed: %s\n",
			res.Commit, res.SyncError)
	} else {
		fmt.Fprintf(w, "note: trunk advanced to %s but your working tree was not clean, so it was not synced.\n",
			res.Commit)
	}
	fmt.Fprintf(w, "note: your index now contains a STAGED REVERSION of the change just admitted — `git status` will show it, and `git commit` would undo the admitted fix.\n")
	fmt.Fprintf(w, "note: run `git reset --hard` to sync, or `git stash` first if you have work in progress.\n")
}
