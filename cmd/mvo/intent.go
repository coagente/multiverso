package main

import (
	"fmt"
	"io"

	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/workspace"
)

func cmdIntentNew(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("intent new", stderr)
	dir := fs.String("dir", ".", "repository directory")
	title := fs.String("title", "", "intent title (required)")
	desc := fs.String("desc", "", "intent description")
	budgetCandidates := fs.Int("budget-candidates", 2, "max candidate worlds")
	budgetWallMS := fs.Int64("budget-wall-ms", 600000, "max wall-clock budget in milliseconds")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *title == "" {
		return usagef("intent new: --title is required")
	}
	if *budgetCandidates < 1 || *budgetWallMS < 1 {
		return usagef("intent new: budgets must be positive")
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("intent new: %w", err)
	}
	defer ws.Close()

	commit, tree, err := gitx.Head(*dir)
	if err != nil {
		return fmt.Errorf("intent new: %w", err)
	}

	in := object.Intent{
		Schema:    object.SchemaIntent,
		Base:      object.Base{Commit: commit, Tree: tree},
		Spec:      object.Spec{Title: *title, Description: *desc},
		Budget:    object.Budget{MaxCandidates: *budgetCandidates, MaxWallMS: *budgetWallMS},
		Policy:    ws.Config.DefaultPolicy,
		CreatedAt: nowRFC3339(),
	}
	dig, canon, err := object.Digest(in)
	if err != nil {
		return fmt.Errorf("intent new: %w", err)
	}
	// Objects live in CAS and the ledger; race.Run loads the intent from
	// CAS by this digest.
	if _, err := ws.CAS.Put(canon); err != nil {
		return fmt.Errorf("intent new: %w", err)
	}
	if _, err := ws.Ledger.Append(evIntentCreated, canon); err != nil {
		return fmt.Errorf("intent new: %w", err)
	}

	fmt.Fprintln(stdout, dig)
	return nil
}
