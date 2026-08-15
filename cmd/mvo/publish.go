package main

import (
	"fmt"
	"io"

	"github.com/coagente/multiverso/internal/publish"
	"github.com/coagente/multiverso/internal/workspace"
)

// cmdPublish publishes an intent's candidates and evidence closure under
// refs/multiverso/intent/<short>/ on a remote (FI-1): deterministic
// publication commits, DSSE-signed receipts and decisions (TP-1),
// plan-diffed leased pushes, and namespace reconciliation. Exit 0 on
// success including the no-op republish.
func cmdPublish(args []string, stdout, stderr io.Writer) error {
	digArg, rest := splitDigestArg(args)
	fs := newFlagSet("publish", stderr)
	dir := fs.String("dir", ".", "repository directory")
	remote := fs.String("remote", "origin", "remote to publish to")
	includeRejected := fs.Bool("include-rejected", false, "publish loser candidate refs too (loser evidence always ships)")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	digArg, err := positionalDigest(digArg, fs, "publish")
	if err != nil {
		return err
	}
	if _, err := publish.IntentShort(digArg); err != nil {
		return usagef("publish: %v", err)
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	defer ws.Close()
	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	intentDig, _, err := st.resolveIntent(digArg)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	signer, err := ws.Signer()
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	res, err := publish.Run(publish.Config{
		Repo:            ws.Root,
		Ledger:          ws.Ledger,
		CAS:             ws.CAS,
		Signer:          signer,
		Intent:          intentDig,
		Remote:          *remote,
		IncludeRejected: *includeRejected,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "published refs/multiverso/intent/%s to %s (%d pushed, %d up-to-date, %d removed)\n",
		res.Short, *remote, len(res.Pushed), len(res.UpToDate), len(res.Removed))
	for _, rt := range res.Pushed {
		fmt.Fprintf(stdout, "  %s %s\n", rt.Ref, rt.Tip)
	}
	return nil
}
