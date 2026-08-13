package main

import (
	"fmt"
	"io"

	"github.com/coagente/multiverso/internal/workspace"
)

func cmdInit(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("init", stderr)
	dir := fs.String("dir", ".", "repository directory")
	keys := fs.Bool("keys", false, "generate signing keys in an existing workspace (error if present)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	// --keys retrofits a pre-M1a workspace with a keypair; plain init
	// still refuses to re-init and always generates keys in fresh
	// workspaces.
	if *keys {
		ws, err := workspace.Open(*dir)
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		defer ws.Close()
		signer, err := ws.GenerateKeys()
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		fmt.Fprintf(stdout, "generated signing key %s\n", signer.KeyID)
		return nil
	}

	ws, err := workspace.Init(*dir)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	defer ws.Close()

	fmt.Fprintf(stdout, "initialized %s (default policy %s)\n", ws.Dir, ws.Config.DefaultPolicy)
	return nil
}
