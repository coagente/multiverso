package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/coagente/multiverso/internal/admit"
	"github.com/coagente/multiverso/internal/oracle"
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

	// The gate that judged the race judges the landing (design decision 5).
	argv, err := admit.LandingOracleArgv(ws.CAS, sel.Decision)
	if err != nil {
		return fmt.Errorf("admit: %w", err)
	}
	signer, err := ws.Signer()
	if err != nil {
		return fmt.Errorf("admit: %w", err)
	}

	res, err := admit.Run(context.Background(), admit.Config{
		Repo:      ws.Root,
		Ledger:    ws.Ledger,
		CAS:       ws.CAS,
		Intent:    intentDig,
		SelectDig: sel.Dig,
		Oracle: &oracle.CommandOracle{
			Argv:    argv,
			Timeout: time.Duration(intent.Budget.MaxWallMS) * time.Millisecond,
			CAS:     ws.CAS,
		},
		Signer:   signer,
		AdmitDir: ws.AdmitDir(),
	})
	if err != nil {
		return fmt.Errorf("admit: %w", err)
	}
	if res.Decision.Type != admit.TypeAdmit {
		return fmt.Errorf("admit: %s: %s", res.Decision.Type, res.Decision.Rationale)
	}

	fmt.Fprintf(stdout, "ADMIT %s\n", res.Decision.Subject[0])
	fmt.Fprintf(stdout, "commit:      %s\n", res.Commit)
	fmt.Fprintf(stdout, "attestation: %s\n", res.AttestationKey)
	fmt.Fprintf(stdout, "decision:    %s\n", res.DecisionDigest)
	return nil
}
