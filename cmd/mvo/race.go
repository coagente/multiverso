package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/workspace"
)

// cmdRace wires the workspace into the v0 race orchestrator (CP-2, CP-3):
// one T0 worktree world per patch file, one suite oracle run per COMPLETED
// world, then the pure decision function. All engine logic lives in
// internal/race.
func cmdRace(args []string, stdout, stderr io.Writer) error {
	digArg, rest := splitDigestArg(args)
	fs := newFlagSet("race", stderr)
	dir := fs.String("dir", ".", "repository directory")
	patchesDir := fs.String("patches", "", "directory with candidate patch files (required)")
	oracleCmd := fs.String("oracle-cmd", "", "suite oracle command (required)")
	keepWorlds := fs.Bool("keep-worlds", false, "keep world worktrees after the race")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	digArg, err := positionalDigest(digArg, fs, "race")
	if err != nil {
		return err
	}
	if *patchesDir == "" {
		return usagef("race: --patches is required")
	}
	argv := strings.Fields(*oracleCmd)
	if len(argv) == 0 {
		return usagef("race: --oracle-cmd is required")
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("race: %w", err)
	}
	defer ws.Close()

	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("race: %w", err)
	}
	intentDig, intent, err := st.resolveIntent(digArg)
	if err != nil {
		return fmt.Errorf("race: %w", err)
	}

	res, err := race.Run(context.Background(), race.Config{
		Repo:       ws.Root,
		Ledger:     ws.Ledger,
		CAS:        ws.CAS,
		Intent:     intentDig,
		PatchesDir: *patchesDir,
		WorldsDir:  ws.WorldsDir(),
		Oracle: &oracle.CommandOracle{
			Argv:    argv,
			Timeout: time.Duration(intent.Budget.MaxWallMS) * time.Millisecond,
			CAS:     ws.CAS,
		},
		KeepWorlds: *keepWorlds,
	})
	if err != nil {
		return fmt.Errorf("race: %w", err)
	}

	dec := res.Decision
	if dec.Type == race.TypeSelect && len(dec.Subject) > 0 {
		fmt.Fprintf(stdout, "SELECT %s (decision %s, %d worlds)\n",
			dec.Subject[0], res.DecisionDigest, len(res.Worlds))
	} else {
		fmt.Fprintf(stdout, "%s (decision %s, %d worlds)\n",
			dec.Type, res.DecisionDigest, len(res.Worlds))
	}
	return nil
}
