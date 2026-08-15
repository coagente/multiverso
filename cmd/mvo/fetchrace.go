package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"text/tabwriter"

	"github.com/coagente/multiverso/internal/publish"
	"github.com/coagente/multiverso/internal/signing"
	"github.com/coagente/multiverso/internal/workspace"
)

const schemaFetchRaceReport = "multiverso.dev/fetchrace-report/v0"

var shortHexRe = regexp.MustCompile(`^[0-9a-f]{12}$`)

// fetchRaceJSON is the --json report shape (single line, like audit).
type fetchRaceJSON struct {
	Schema    string               `json:"schema"`
	Short     string               `json:"short"`
	Intent    string               `json:"intent"`
	Title     string               `json:"title"`
	Decision  string               `json:"decision"`
	Type      string               `json:"type"`
	Winner    string               `json:"winner"`
	Admitted  bool                 `json:"admitted"`
	Freshness string               `json:"freshness"`
	Items     []publish.ItemReport `json:"items"`
	Refs      int                  `json:"refs"`
	OK        bool                 `json:"ok"`
}

// cmdFetchRace is the consumer side of publication (M1d decision 13):
// workspace-less, ledger-less offline verification of a published race in
// any clone with the remote configured. Exit 0 iff every check passed.
func cmdFetchRace(args []string, stdout, stderr io.Writer) error {
	arg, rest := splitDigestArg(args)
	fs := newFlagSet("fetch-race", stderr)
	dir := fs.String("dir", ".", "repository directory (any clone; no workspace needed)")
	remote := fs.String("remote", "origin", "remote to fetch the namespace from")
	keyPath := fs.String("key", "", "trusted public key PEM (default: .multiverso/keys/local.pub when a workspace exists)")
	jsonOut := fs.Bool("json", false, "emit a machine-readable report")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	arg, err := positionalDigest(arg, fs, "fetch-race")
	if err != nil {
		return err
	}

	// Accept the 12-hex short or a full mv0: digest (shortened).
	short := arg
	if !shortHexRe.MatchString(short) {
		s, err := publish.IntentShort(arg)
		if err != nil {
			return usagef("fetch-race: %q is neither a %d-hex intent-short nor an mv0: digest", arg, publish.ShortLen)
		}
		short = s
	}

	// The trust root is explicit (--key); a workspace key at --dir is only
	// a convenience default.
	trusted := *keyPath
	if trusted == "" {
		def := filepath.Join(*dir, workspace.DirName, "keys", signing.PubName)
		if _, err := os.Stat(def); err != nil {
			return usagef("fetch-race: --key is required (no workspace key at %s)", def)
		}
		trusted = def
	}
	pub, _, err := signing.LoadPublicKeyFile(trusted)
	if err != nil {
		return fmt.Errorf("fetch-race: %w", err)
	}

	rep, err := publish.FetchRace(publish.FetchConfig{
		Repo:   *dir,
		Remote: *remote,
		Short:  short,
		Pub:    pub,
	})
	if err != nil {
		return fmt.Errorf("fetch-race: %w", err)
	}

	// Every bad item is loud AND specific (M1d decision 14).
	failed := 0
	for _, it := range rep.Items {
		if !it.OK {
			failed++
			fmt.Fprintf(stderr, "mvo: fetch-race: %s: %s\n", it.Path, it.Err)
		}
	}

	freshness := fmt.Sprintf("%s (%s)", rep.Freshness, rep.FreshnessDetail)
	if *jsonOut {
		if err := emitJSON(stdout, fetchRaceJSON{
			Schema:    schemaFetchRaceReport,
			Short:     rep.Short,
			Intent:    rep.IntentDig,
			Title:     rep.Title,
			Decision:  rep.SelectDig,
			Type:      rep.DecisionType,
			Winner:    rep.Winner,
			Admitted:  rep.Admitted,
			Freshness: freshness,
			Items:     rep.Items,
			Refs:      len(rep.Refs),
			OK:        rep.OK,
		}); err != nil {
			return fmt.Errorf("fetch-race: %w", err)
		}
	} else {
		admitted := "no"
		if rep.Admitted {
			admitted = "yes"
		}
		fmt.Fprintf(stdout, "intent:    %s (%s)\n", rep.IntentDig, rep.Title)
		fmt.Fprintf(stdout, "decision:  %s %s\n", rep.SelectDig, rep.DecisionType)
		fmt.Fprintf(stdout, "winner:    %s\n", rep.Winner)
		fmt.Fprintf(stdout, "admitted:  %s\n", admitted)
		fmt.Fprintf(stdout, "freshness: %s\n", freshness)
		tw := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "ORDINAL\tWORLD\tOUTCOME\tGATE\tSIGNED\tREF")
		for _, row := range rep.Worlds {
			ordinal := "-"
			if row.Ordinal > 0 {
				ordinal = fmt.Sprintf("%d", row.Ordinal)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
				ordinal, row.Dig, row.Outcome, row.Gate, row.Signed, row.Ref)
		}
		if err := tw.Flush(); err != nil {
			return fmt.Errorf("fetch-race: %w", err)
		}
		if rep.OK {
			fmt.Fprintf(stdout, "OK: race verified (%d items, %d refs)\n", len(rep.Items), len(rep.Refs))
		} else {
			fmt.Fprintf(stdout, "FAIL: %d of %d items failed verification\n", failed, len(rep.Items))
		}
	}
	if !rep.OK {
		return fmt.Errorf("fetch-race: %d of %d items failed verification", failed, len(rep.Items))
	}
	return nil
}
