// Command mvo is the Multiverso CLI: an evidence-native, Git-compatible
// control plane for speculative software change produced by AI agents.
//
// M0 walking skeleton. See PRD.md and docs/design/M0.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// Exit codes per the M0 CLI contract: 0 ok, 1 failure, 2 usage.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	verb, rest := args[0], args[1:]

	var err error
	switch verb {
	case "init":
		err = cmdInit(rest, stdout, stderr)
	case "intent":
		if len(rest) == 0 || rest[0] != "new" {
			fmt.Fprintln(stderr, "mvo: usage: mvo intent new --title T [--desc D] [--budget-candidates N] [--budget-wall-ms MS] [--dir DIR]")
			return exitUsage
		}
		err = cmdIntentNew(rest[1:], stdout, stderr)
	case "race":
		err = cmdRace(rest, stdout, stderr)
	case "worlds":
		err = cmdWorlds(rest, stdout, stderr)
	case "explain":
		err = cmdExplain(rest, stdout, stderr)
	case "audit":
		err = cmdAudit(rest, stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "mvo: unknown command %q\n", verb)
		usage(stderr)
		return exitUsage
	}

	if err == nil {
		return exitOK
	}
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	fmt.Fprintf(stderr, "mvo: %v\n", err)
	var ue usageError
	if errors.As(err, &ue) {
		return exitUsage
	}
	return exitFail
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: mvo <command> [flags]

commands:
  init                              create the .multiverso workspace
  intent new --title T [--desc D]   record an intent; prints its digest
             [--budget-candidates N] [--budget-wall-ms MS]
  race <intent-digest> --patches DIR --oracle-cmd CMD [--keep-worlds]
  worlds <intent-digest>            table of worlds: digest, outcome, gate, wall_ms
  explain <intent-digest>           render the recorded decision and evidence
  audit [--json]                    verify hash chain and replay all decisions

every command accepts --dir <repo> (default ".").
`)
}
