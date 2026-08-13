package main

import (
	"fmt"
	"io"

	"github.com/coagente/multiverso/internal/workspace"
)

func cmdExplain(args []string, stdout, stderr io.Writer) error {
	digArg, rest := splitDigestArg(args)
	fs := newFlagSet("explain", stderr)
	dir := fs.String("dir", ".", "repository directory")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	digArg, err := positionalDigest(digArg, fs, "explain")
	if err != nil {
		return err
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}
	defer ws.Close()
	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}
	intentDig, intent, err := st.resolveIntent(digArg)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}

	var found *decisionRec
	for i := range st.Decisions {
		if st.Decisions[i].Decision.Intent == intentDig {
			found = &st.Decisions[i] // latest decision wins
		}
	}
	if found == nil {
		return fmt.Errorf("explain: no decision recorded for intent %s (run \"mvo race\" first)", intentDig)
	}
	dec := found.Decision

	fmt.Fprintf(stdout, "decision:  %s\n", found.Dig)
	fmt.Fprintf(stdout, "type:      %s\n", dec.Type)
	fmt.Fprintf(stdout, "intent:    %s  (%s)\n", intentDig, intent.Spec.Title)
	fmt.Fprintf(stdout, "policy:    %s\n", dec.Policy)
	if (dec.Type == "SELECT" || dec.Type == "ADMIT") && len(dec.Subject) > 0 {
		fmt.Fprintf(stdout, "winner:    %s\n", dec.Subject[0])
	}
	label := "subject:  "
	for _, s := range dec.Subject {
		fmt.Fprintf(stdout, "%s %s\n", label, s)
		label = "          "
	}
	label = "evidence: "
	for _, e := range dec.Evidence {
		fmt.Fprintf(stdout, "%s %s\n", label, e)
		label = "          "
	}
	fmt.Fprintf(stdout, "rationale: %s\n", dec.Rationale)
	return nil
}
