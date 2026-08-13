package main

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/workspace"
)

func cmdWorlds(args []string, stdout, stderr io.Writer) error {
	digArg, rest := splitDigestArg(args)
	fs := newFlagSet("worlds", stderr)
	dir := fs.String("dir", ".", "repository directory")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	digArg, err := positionalDigest(digArg, fs, "worlds")
	if err != nil {
		return err
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("worlds: %w", err)
	}
	defer ws.Close()
	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("worlds: %w", err)
	}
	intentDig, _, err := st.resolveIntent(digArg)
	if err != nil {
		return fmt.Errorf("worlds: %w", err)
	}

	recByWorld := make(map[string]object.Receipt)
	for _, rr := range st.Receipts {
		recByWorld[rr.Receipt.World] = rr.Receipt // last suite receipt wins
	}

	tw := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "WORLD\tOUTCOME\tGATE\tWALL_MS")
	for _, wr := range st.worldsFor(intentDig, 0, 0) {
		gate, wall := "-", "-"
		if rec, ok := recByWorld[wr.Dig]; ok {
			gate = "fail"
			if rec.Result.Status == "pass" {
				gate = "pass"
			}
			wall = strconv.FormatInt(rec.Cost.WallMS, 10)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", wr.Dig, wr.World.Outcome, gate, wall)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("worlds: %w", err)
	}
	return nil
}
