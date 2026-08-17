package main

// `mvo-eval fetch` — THE ONLY NETWORK ACCESS IN THE BLOCK, and it is opt-in,
// printed before it happens, and digest-verified.
//
// Three rules, in the order a reader needs them:
//
//  1. IT PRINTS EVERY URL IT WILL CONTACT BEFORE CONTACTING ANY OF THEM. The
//     URL list is a pure function of the manifest (Manifest.URLs), so what is
//     printed and what is fetched cannot disagree.
//  2. --dry-run CONTACTS NOTHING; a non-interactive run REQUIRES --yes.
//  3. IT REFUSES TO WRITE BYTES IT CANNOT VERIFY. A manifest row with no
//     digest is a refusal, not a permission.
//
// `fetch local-derived` contacts nothing at all: the corpus is generated from
// committed fixtures whose digests the manifest pins, so the network rules above
// are vacuous for it — which is exactly why that corpus exists.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/eval"
)

func cmdFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	// The corpus is a LEADING positional; strip it before parsing so the
	// flags after it are seen (see takeLeadingArg).
	positional, args := takeLeadingArg(args)
	common := addCommon(fs)
	dryRun := fs.Bool("dry-run", false, "print every URL and contact none")
	yes := fs.Bool("yes", false, "proceed without a prompt (required non-interactively)")
	images := fs.Bool("images", false, "also pull the per-instance environment images")
	manifestPath := fs.String("manifest", "", "manifest path (default eval/corpora/<corpus>-<version>.manifest.json)")
	repoRoot := fs.String("repo-root", ".", "repository root, for a fixture-basis manifest")
	seed := fs.Int64("seed", 20260817, "derivation seed, recorded with every derived candidate")
	jsonOut := fs.Bool("json", false, "emit the materialization report as JSON")
	if err := fs.Parse(args); err != nil {
		return codedError{code: exitUsage, msg: err.Error()}
	}
	corpus := common.corpus
	if positional != "" {
		corpus = positional
	}
	// THE GENERATOR'S VERSION IS NOT NEGOTIABLE. local-derived's instances are
	// written under eval.LocalVersion, which moves whenever the generated shape
	// moves — a new operator, a new check, a new hidden runner. Accepting a
	// different --version here would materialize into one directory and then
	// read from another, which is how a "materialized 5 instances" line was
	// immediately followed by "no such file or directory".
	if corpus == eval.CorpusLocalDerived && common.version != eval.LocalVersion {
		return codedError{code: exitUsage, msg: fmt.Sprintf(
			"mvo-eval fetch: %s materializes at version %s, not %s: the version moves when the GENERATED "+
				"shape moves (v0's hidden runner imported candidate code into the process that judged it, "+
				"and a v0 eval home must not be scored by a v1 scorer)",
			eval.CorpusLocalDerived, eval.LocalVersion, common.version)}
	}
	mp := *manifestPath
	if mp == "" {
		mp = filepath.Join(*repoRoot, "eval", "corpora",
			fmt.Sprintf("%s-%s.manifest.json", corpus, common.version))
		if _, err := os.Stat(mp); err != nil {
			// The network corpora ship as unpinned templates, whose file
			// name carries no version.
			alt := filepath.Join(*repoRoot, "eval", "corpora", corpus+".manifest.json")
			if _, err2 := os.Stat(alt); err2 == nil {
				mp = alt
			}
		}
	}
	m, err := eval.LoadManifest(mp)
	if err != nil {
		return err
	}

	// Rule 1: print, then decide.
	urls := m.URLs()
	fmt.Printf("corpus %s@%s: digest_basis=%s network=%v\n", m.Corpus, m.Version, m.DigestBasis, m.Network)
	if len(urls) == 0 {
		fmt.Println("URLs it will contact: NONE")
	} else {
		fmt.Printf("URLs it will contact (%d):\n", len(urls))
		for _, u := range urls {
			fmt.Println("  " + u)
		}
	}
	for _, n := range m.Notes {
		fmt.Println("note: " + n)
	}
	if *dryRun {
		fmt.Println("--dry-run: contacted nothing.")
		return nil
	}
	if m.Network {
		if !*yes {
			return codedError{code: exitUsage, msg: "mvo-eval fetch: this corpus needs the network: " +
				"pass --yes to proceed, or --dry-run to see the URLs and stop"}
		}
		if len(m.Instances) == 0 || firstMissingDigest(m) != "" {
			return codedError{code: exitFailure, msg: fmt.Sprintf(
				"mvo-eval fetch: manifest %s carries no digest for %s: refusing to write unverified bytes. "+
					"A manifest with plausible-looking digests nobody has verified is worse than no manifest",
				mp, orNone(firstMissingDigest(m)))}
		}
	}

	home := common.home
	if home == "" {
		home, err = eval.HomeFromEnv()
		if err != nil {
			return err
		}
	}
	store, err := eval.EnsureStore(home)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(store.CorpusDir(m.Corpus, m.Version), 0o700); err != nil {
		return fmt.Errorf("mvo-eval fetch: create corpus dir: %w", err)
	}

	if !m.Network {
		if m.Corpus != eval.CorpusLocalDerived {
			return fmt.Errorf("mvo-eval fetch: corpus %s declares no network and is not %s: "+
				"there is no other route by which instances come into existence", m.Corpus, eval.CorpusLocalDerived)
		}
		rep, err := eval.MaterializeLocalDerived(store, *repoRoot, m, *seed)
		if err != nil {
			return err
		}
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rep)
		}
		fmt.Printf("materialized %d instance(s) into %s\n", len(rep.Instances), store.CorpusDir(m.Corpus, m.Version))
		for _, id := range rep.Instances {
			inst, err := store.LoadInstance(m.Corpus, m.Version, id)
			if err != nil {
				return err
			}
			fmt.Printf("  %-20s family=%-14s candidates=%d oracle=%s canary=%s\n",
				id, inst.Family, len(inst.Candidates), short(inst.OracleDigest), inst.CanaryID)
			for _, c := range inst.Candidates {
				fmt.Printf("      ord %d %-34s source=%-16s expected=%-9s %s\n",
					c.Ord, c.ID, c.Source, c.Expected, short(c.Patch))
			}
		}
		// Declines and drops are DATA: "the operator list produced 3 of 7"
		// is a fact about the population, not a warning to bury.
		for _, id := range rep.Instances {
			for _, d := range rep.Derivations[id] {
				if !d.Applied {
					fmt.Printf("  declined %s/%s: %s\n", id, d.Operator, d.Reason)
				}
			}
		}
		for k, v := range rep.Dropped {
			fmt.Printf("  dropped %s: %s\n", k, v)
		}
		return nil
	}

	// The network path. Each row is fetched, verified, and only then written.
	client := &http.Client{Timeout: 60 * time.Second}
	for _, in := range m.Instances {
		body, err := fetchURL(client, in.URL)
		if err != nil {
			return err
		}
		if got := "sha256:" + hexSum(body); got != in.PublicDigest {
			return fmt.Errorf("mvo-eval fetch: %s: %s: bytes digest %s, manifest says %s",
				in.ID, eval.SkipDigestMismatch, got, in.PublicDigest)
		}
		var inst eval.Instance
		if err := json.Unmarshal(body, &inst); err != nil {
			return fmt.Errorf("mvo-eval fetch: %s: decode: %w", in.ID, err)
		}
		if err := store.WriteInstance(inst); err != nil {
			return err
		}
		fmt.Printf("  fetched %s (%d bytes, verified)\n", in.ID, len(body))
	}
	if *images {
		fmt.Println("--images: image pulls are NOT implemented in M2d v0. " +
			"Every instance declares t0_ok or is skipped image-absent; naming that is the honest option, " +
			"and a silent no-op would not be.")
	}
	return nil
}

func firstMissingDigest(m eval.Manifest) string {
	for _, in := range m.Instances {
		if in.PublicDigest == "" || in.OracleDigest == "" {
			return in.ID
		}
	}
	return ""
}

func orNone(s string) string {
	if s == "" {
		return "(no instance rows at all)"
	}
	return s
}

func fetchURL(c *http.Client, url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("mvo-eval fetch: refusing a non-https URL: %q", url)
	}
	resp, err := c.Get(url)
	if err != nil {
		return nil, fmt.Errorf("mvo-eval fetch: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mvo-eval fetch: GET %s: status %d", url, resp.StatusCode)
	}
	// A cap, because an eval corpus row is a JSON object and anything much
	// larger is not the thing we asked for.
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("mvo-eval fetch: read %s: %w", url, err)
	}
	return b, nil
}

func hexSum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func short(dig string) string {
	if len(dig) > 19 {
		return dig[:19]
	}
	return dig
}
