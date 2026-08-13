package main

import (
	"fmt"
	"io"

	"github.com/coagente/multiverso/internal/workspace"
)

func cmdInit(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("init", stderr)
	dir := fs.String("dir", ".", "repository directory")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	ws, err := workspace.Init(*dir)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	defer ws.Close()

	fmt.Fprintf(stdout, "initialized %s (default policy %s)\n", ws.Dir, ws.Config.DefaultPolicy)
	return nil
}
