package main

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/coagente/multiverso/internal/publish"
	"github.com/coagente/multiverso/internal/workspace"
)

// cmdPrune applies the FI-1 retention policy to an intent's published
// namespace (M1d decision 12). --remote has NO default: deleting remote
// refs is explicit, always. CAS and ledger are never touched.
func cmdPrune(args []string, stdout, stderr io.Writer) error {
	digArg, rest := splitDigestArg(args)
	fs := newFlagSet("prune", stderr)
	dir := fs.String("dir", ".", "repository directory")
	remote := fs.String("remote", "", "remote to prune too (omitted = local-only)")
	olderThan := fs.String("older-than", "", "refuse unless the latest publication is older than DUR (time.ParseDuration; hours are the largest unit)")
	keepAdmitted := fs.Bool("keep-admitted", true, "keep the winner's candidate ref and the evidence ref of an admitted intent")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	digArg, err := positionalDigest(digArg, fs, "prune")
	if err != nil {
		return err
	}
	if _, err := publish.IntentShort(digArg); err != nil {
		return usagef("prune: %v", err)
	}
	var age time.Duration
	if *olderThan != "" {
		age, err = time.ParseDuration(*olderThan)
		if err != nil {
			return usagef("prune: --older-than: %v", err)
		}
		if age <= 0 {
			return usagef("prune: --older-than must be positive (got %s)", age)
		}
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	defer ws.Close()
	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	intentDig, _, err := st.resolveIntent(digArg)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}

	res, err := publish.Prune(publish.PruneConfig{
		Repo:         ws.Root,
		Ledger:       ws.Ledger,
		Intent:       intentDig,
		Remote:       *remote,
		OlderThan:    age,
		KeepAdmitted: *keepAdmitted,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "pruned refs/multiverso/intent/%s: %d local, %d remote deleted, %d kept\n",
		res.Short, len(res.DeletedLocal), len(res.DeletedRemote), len(res.Kept))
	deleted := map[string]bool{}
	for _, ref := range res.DeletedLocal {
		deleted[ref] = true
	}
	for _, ref := range res.DeletedRemote {
		deleted[ref] = true
	}
	refs := make([]string, 0, len(deleted))
	for ref := range deleted {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		fmt.Fprintf(stdout, "  %s\n", ref)
	}
	return nil
}
