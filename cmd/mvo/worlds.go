package main

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/publish"
	"github.com/coagente/multiverso/internal/race"
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
	intentDig, intent, err := st.resolveIntent(digArg)
	if err != nil {
		return fmt.Errorf("worlds: %w", err)
	}

	gates, walls := gateColumn(ws, st, intentDig)

	tw := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "WORLD\tOUTCOME\tGATE\tWALL_MS\tUSD_MICRO\tTIER")
	for _, wr := range st.worldsFor(intentDig, 0, 0) {
		gate, wall := "-", "-"
		if g, ok := gates[wr.Dig]; ok {
			gate = g
		}
		if ms, ok := walls[wr.Dig]; ok {
			wall = strconv.FormatInt(ms, 10)
		}
		// Production cost comes from the world itself (AG-2, NFR-5), not
		// from any receipt: generation is not an oracle run. TIER is the
		// world's recorded isolation tier (XP-1: recorded, never assumed).
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
			wr.Dig, wr.World.Outcome, gate, wall, wr.World.Cost.USDMicro, wr.World.IsolationTier)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("worlds: %w", err)
	}
	// Trunk drift is a display concept, computed at render time — never a
	// ledger mutation (M1d decision 16).
	status, detail := publish.TrunkDrift(*dir, intent.Base.Commit)
	fmt.Fprintf(stdout, "freshness: %s (%s)\n", status, detail)
	return nil
}

// gateColumn derives each world's GATE cell from the intent's latest race
// decision, evaluated through the pinned policy: "pass" when every hard gate
// passed, otherwise the label of the FIRST gate that failed — which is the
// one that stopped the ladder, and the only one an operator needs to read.
// Falls back to a bare pass/fail over the world's receipts when no decision
// has been recorded yet (a race that aborted mid-flight still has worlds).
func gateColumn(ws *workspace.Workspace, st *ledgerState, intentDig string) (map[string]string, map[string]int64) {
	gates := map[string]string{}
	walls := map[string]int64{}
	traced := map[string]bool{}

	// The latest RACE decision, not merely the latest decision: after an
	// admission the newest decision judges one landing tree and says nothing
	// about the candidates this table lists.
	var found *decisionRec
	for i := range st.Decisions {
		if st.Decisions[i].Decision.Intent != intentDig {
			continue
		}
		if _, _, ok := raceWindow(st, &st.Decisions[i]); ok {
			found = &st.Decisions[i]
		}
	}
	if found != nil {
		if pol, err := policy.Load(ws.CAS, found.Decision.Policy); err == nil {
			if worlds, receipts, ok := raceWindow(st, found); ok {
				for _, rr := range receipts {
					// The ladder records several receipts per world; the wall
					// time a world cost to verify is their sum, which is also
					// what wall_ms_asc ranks by.
					walls[rr.Receipt.World] += rr.Receipt.Cost.WallMS
				}
				for _, c := range race.Trace(pol, worlds, receipts).Candidates {
					gates[c.World], traced[c.World] = c.GateCell(), true
				}
			}
		}
	}
	// Worlds outside the latest race window (an earlier race of the same
	// intent) keep the pre-M1e rendering: the bare verdict of their own
	// receipts, which is all that was ever computed for them.
	for _, rr := range st.Receipts {
		w := rr.Receipt.World
		if traced[w] {
			continue
		}
		walls[w] += rr.Receipt.Cost.WallMS
		gates[w] = "fail"
		if rr.Receipt.Result.Status == "pass" {
			gates[w] = "pass"
		}
	}
	return gates, walls
}
