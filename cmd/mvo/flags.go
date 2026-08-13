package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

// usageError marks command-line misuse so main can exit 2 instead of 1.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error {
	return usageError{msg: fmt.Sprintf(format, a...)}
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parseFlags parses args, converting flag errors into usage errors
// (exit code 2) while letting -h/--help through as flag.ErrHelp (exit 0).
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{msg: fmt.Sprintf("%s: %v", fs.Name(), err)}
	}
	return nil
}

// splitDigestArg peels a leading positional argument (the documented
// `mvo <verb> <intent-digest> --flags...` order, which stdlib flag would
// otherwise stop at) and leaves the rest for flag parsing.
func splitDigestArg(args []string) (string, []string) {
	if len(args) > 0 && args[0] != "" && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

// positionalDigest resolves the intent digest from either the peeled
// leading argument or a trailing positional after the flags. Leftover
// arguments are usage errors: stdlib flag stops parsing at the first
// positional, so a flag placed after a trailing digest would otherwise be
// dropped without diagnostic.
func positionalDigest(peeled string, fs *flag.FlagSet, verb string) (string, error) {
	dig, extra := peeled, fs.Args()
	if dig == "" && len(extra) > 0 {
		dig, extra = extra[0], extra[1:]
	}
	if dig == "" {
		return "", usagef("%s: intent digest required", verb)
	}
	if len(extra) > 0 {
		return "", usagef("%s: unexpected arguments after intent digest: %s (place flags before the digest)",
			verb, strings.Join(extra, " "))
	}
	return dig, nil
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
