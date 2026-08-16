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
	"runtime"
	"runtime/debug"
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
			fmt.Fprintln(stderr, "mvo: usage: mvo intent new --title T [--desc D] [--budget-candidates N] [--budget-wall-ms MS] [--policy NAME|DIGEST | --oracle-cmd CMD] [--dir DIR]")
			return exitUsage
		}
		err = cmdIntentNew(rest[1:], stdout, stderr)
	case "policy":
		err = cmdPolicy(rest, stdout, stderr)
	case "race":
		err = cmdRace(rest, stdout, stderr)
	case "worlds":
		err = cmdWorlds(rest, stdout, stderr)
	case "explain":
		err = cmdExplain(rest, stdout, stderr)
	case "admit":
		err = cmdAdmit(rest, stdout, stderr)
	case "verify":
		err = cmdVerify(rest, stdout, stderr)
	case "publish":
		err = cmdPublish(rest, stdout, stderr)
	case "prune":
		err = cmdPrune(rest, stdout, stderr)
	case "fetch-race":
		err = cmdFetchRace(rest, stdout, stderr)
	case "audit":
		err = cmdAudit(rest, stdout, stderr)
	case "guard":
		err = cmdGuard(rest, stdout, stderr)
	case "oracles":
		err = cmdOracles(rest, stdout, stderr)
	case "version", "--version", "-v":
		writeVersion(stdout)
		return exitOK
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

// writeVersion prints the build's own identity. There is no stamped
// version string in M1, so this reports what the Go toolchain embedded in
// the binary — the VCS revision when the build had one. CI can pin a
// binary by it, and an operator can say which build replayed an
// attestation, which a usage dump at exit 2 could not.
func writeVersion(w io.Writer) {
	rev, modified, mod := "unknown", false, "unknown"
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" {
			mod = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	if modified {
		rev += " (dirty)"
	}
	fmt.Fprintf(w, "mvo %s\nrevision: %s\ngo:       %s\n", mod, rev, runtime.Version())
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: mvo <command> [flags]

commands:
  init [--keys]                     create the .multiverso workspace (--keys: add
                                    signing keys to an existing workspace)
  policy list                       policies known to the workspace, digest-sorted
  policy show <name|digest> [--json]  render a policy; --json prints its canonical bytes
  policy validate <file>            decode + validate + compile a policy file
  policy use <name>                 install .multiverso/policies/<name>.json as the default
  intent new --title T [--desc D]   record an intent; prints its digest
             [--budget-candidates N] [--budget-wall-ms MS]
             [--budget-oracle-ms MS]  additive oracle spend the M2b scheduler may
                                      allocate; 0 (the default) = unbounded = the
                                      exhaustive M1 ladder
             [--policy NAME|DIGEST | --oracle-cmd CMD]
  race <intent-digest> [--agent script|claude-code|codex]
       [--oracle-cmd CMD]           required with a policy/v0 intent, refused with policy/v1
       [--parallel N] [--exec T0|T1] [--keep-worlds]
       [--schedule adaptive|fixed]  phase-B arm: the M2b adaptive scheduler
                                    (default) or the exhaustive M1 ladder. A
                                    policy ranking by wall_ms_asc is refused
                                    under adaptive (validation rule 25)
       [--collect-inert]            also buy decision-inert rungs on worlds that
                                    passed every gate, labelled basis=research
       script (default):  --patches DIR
       claude-code|codex: [--prompt TEXT | --prompt-file P] [--model NAME[,NAME...]]
                          [--candidates N] [--max-usd USD] [--max-turns N]
                          [--max-wall-ms MS] [--agent-env NAME[,NAME...]]
       --exec T1:         --exec-image REF [--memory-mb N] [--cpus DEC]
                          [--pids N] [--allow-network]
  worlds <intent-digest>            table of worlds: digest, outcome, gate, wall_ms, tier
  explain <intent-digest> [--json] [--diffs N] [--schedule]
                                    render the decision: gates, why the winner
                                    won key by key, evidence, escalation
                                    --schedule appends the recorded allocation
                                    trace — what the scheduler bought, what it
                                    considered and DECLINED and why, the cost
                                    model it allocated against, and evidence
                                    waste. Rows are rendered, never re-scored;
                                    a race with no trace says so
  guard --base <rev|tree> [--tree <rev|tree>] [--policy NAME|DIGEST] [--json]
                                    compare two trees under a policy's protected and
                                    harness path sets; exit 1 on any violation. Writes
                                    nothing: no ledger, no worktree, no race.
  oracles [--json] [--policy NAME|DIGEST]
                                    the oracle menu: every kind's declared cost shape
                                    and correlation, plus coefficients FITTED from this
                                    workspace's receipts. Fewer than 3 receipts for a
                                    kind prints "no local measurement (n=…)", never a fit
  admit <intent-digest>             land the SELECT winner on trunk with a signed attestation
  verify <commit> [--key PUB] [--json]  verify the admission attestation offline
  publish <intent-digest> [--remote R] [--include-rejected]
                                    publish candidates + signed evidence closure
                                    under refs/multiverso/intent/<short>/
  prune <intent-digest> [--remote R] [--older-than DUR] [--keep-admitted=BOOL]
                                    apply the retention policy to published refs
  fetch-race <intent-short> [--remote R] [--key PUB] [--json]
                                    fetch and verify a published race offline
  audit [--json] [--require-decisions N] [--cas-sweep=BOOL]                    verify hash chain and replay all decisions
                                    THIS WORKSPACE's ledger only: it takes no
                                    commit and exits 0 on an empty workspace,
                                    so it is not an admission check
  version                           build revision of this binary

every command accepts --dir <repo> (default ".").
`)
}
