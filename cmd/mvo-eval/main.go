// Command mvo-eval is THE SECOND BINARY (M2d decision 2).
//
// The code that can read a label is not linked into the binary that races.
// `mvo` must not, at any optimization level, contain a symbol that opens
// $MVO_EVAL_HOME; there is no `mvo eval` subcommand and there will not be one.
// The property is asserted mechanically by scripts/accept.sh step m2d-7c:
//
//	go list -deps ./cmd/mvo | grep -q internal/eval   # MUST NOT match
//
// The consequence is structural: a leak through the racing binary would require
// someone to add an import that the acceptance script rejects. The alternative —
// one binary with a flag that "must not be set during a race" — is a promise,
// and promises are what the M2b.1 fairness conditions and the design-partner
// study taught us not to ship.
//
// ZERO AGENT SPEND. No subcommand here invokes a real agent CLI or any API.
// Every race is `--agent script` over patch bytes that already exist, and the
// runner front-loads PATH with poisoned stubs so that a code path which tried
// would die loudly instead of spending money.
//
// NETWORK. Exactly one subcommand can touch it — `fetch`, on a manifest whose
// `network` field is true — and it prints every URL before contacting any of
// them, requires --yes for a non-interactive run, and refuses to write bytes it
// cannot verify against a manifest digest. `fetch local-derived` contacts
// nothing at all.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/coagente/multiverso/internal/eval"
)

// Exit codes. They are part of the interface scripts/eval.sh and
// scripts/accept.sh depend on, so they are named here rather than spelled as
// integer literals at the call sites.
const (
	exitOK       = 0
	exitFailure  = 1
	exitUsage    = 2
	exitNoMetric = 3 // nothing could be scored, and --strict was passed
	exitLeak     = 4 // a leak detector fired: the instance is voided
	// exitVacuous is M2d.1 decision 7's NAMED NON-VERDICT: the rule under
	// test provably never fired, so there is no comparison to report. It is
	// NOT behind --strict, and that is deliberate — `R < 3` is a THIN
	// measurement a reader may reasonably want to look at, while a 0 %
	// comparison is NOT A MEASUREMENT OF ANYTHING, and printing it beside a
	// claim is the exact failure the block exists to correct.
	exitVacuous = 5
	exitSkip    = 77 // a prerequisite is absent; the reason is printed
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitUsage)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "fetch":
		err = cmdFetch(args)
	case "arms":
		err = cmdArms(args)
	case "derive":
		err = cmdDerive(args)
	case "run":
		err = cmdRun(args)
	case "score":
		err = cmdScore(args)
	case "report":
		err = cmdReport(args)
	case "leakcheck":
		err = cmdLeakcheck(args)
	case "import-worlds":
		err = cmdImportWorlds(args)
	case "adjudicate":
		err = cmdAdjudicate(args)
	case "freeze":
		err = cmdFreeze(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "mvo-eval: unknown command %q\n", cmd)
		usage()
		os.Exit(exitUsage)
	}
	if err != nil {
		var ce codedError
		if asCoded(err, &ce) {
			if ce.msg != "" {
				fmt.Fprintln(os.Stderr, ce.msg)
			}
			os.Exit(ce.code)
		}
		fmt.Fprintln(os.Stderr, "mvo-eval: "+err.Error())
		os.Exit(exitFailure)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `mvo-eval — M2d's labelled-evaluation plane (the SECOND binary)

usage: mvo-eval <command> [flags]

  fetch <corpus>        materialize a corpus into $MVO_EVAL_HOME. Prints every URL
                        it would contact BEFORE contacting any of them; --dry-run
                        contacts none; --yes is required non-interactively.
                        `+"`local-derived`"+` needs no network at all.
  arms                  print the arm declaration table and the PRD §11 mapping
  derive <instance>     print the derived candidate population, declines included
  run                   the protocol: race every arm over the fixed candidate set,
                        score against the hidden oracle, print the metrics
  score                 label an existing, SEALED workspace against one instance
  report                render a saved rows file
  leakcheck             run D1..D5 over a workspace against one instance's needles
  import-worlds <ws>    extract (base, patch, tree) triples from a real run's
                        ledger as S4 candidates
  adjudicate            record a Tier-3 human verdict beside a Tier-1 label
  freeze                record the materialized oracle digests into the eval home

common flags:
  --home DIR            eval home (default $MVO_EVAL_HOME, else ~/.cache/multiverso/eval)
  --corpus NAME         corpus name (default local-derived)
  --version V           corpus version (default v1)

exit codes: 0 ok, 1 failure, 2 usage, 3 nothing scorable under --strict,
            4 a leak detector fired, 5 VACUOUS (the rule under test never fired:
            no verdict, no metric line), 77 a named prerequisite is absent
`)
}

// codedError carries an exit code out of a command.
type codedError struct {
	code int
	msg  string
}

func (e codedError) Error() string { return e.msg }

func asCoded(err error, out *codedError) bool {
	if ce, ok := err.(codedError); ok {
		*out = ce
		return true
	}
	return false
}

func skipf(format string, a ...any) error {
	return codedError{code: exitSkip, msg: "SKIP: " + fmt.Sprintf(format, a...)}
}

// commonFlags are the flags every command shares.
type commonFlags struct {
	home    string
	corpus  string
	version string
}

func addCommon(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.home, "home", "", "eval home (default $MVO_EVAL_HOME, else ~/.cache/multiverso/eval)")
	fs.StringVar(&c.corpus, "corpus", "local-derived", "corpus name")
	fs.StringVar(&c.version, "version", eval.LocalVersion, "corpus version")
	return c
}

func joinLines(lines []string) string { return strings.Join(lines, "\n") }

// takeLeadingArg strips a leading positional argument and returns the rest for
// flag parsing.
//
// Go's flag package STOPS at the first non-flag token, so
// `fetch local-derived --repo-root ..` would silently ignore --repo-root and
// fetch against the wrong root — which is exactly how this function came to
// exist. Every verb documented as `<verb> <arg> [flags]` routes through here so
// the documented spelling and the parsed spelling are the same one.
func takeLeadingArg(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}
