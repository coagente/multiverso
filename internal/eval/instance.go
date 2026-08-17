// Package eval is M2d's EVAL PLANE: the labels that make M2b.1's
// 5/5-REJECT row readable.
//
// WHY THIS PACKAGE EXISTS. M2b.1 raced two budgeted arms at a binding
// budget and got adaptive REJECT 5/5 against fixed-budget SELECT 3/5 — the
// first difference in the project that clears both arms' own run-to-run
// instability. What it could not say is which arm was right, because a
// REJECT where no candidate was any good is the correct answer and a REJECT
// where an honest fix sat there unbought is a failure, and telling those
// apart needs labelled correctness. This package produces the labels and
// the arithmetic over them (TCAR, FAR, FRR, regret), and nothing else.
//
// WHAT IT MAY NOT DO. It adds no ledger event, no receipt field, no policy
// field and no wire field anywhere: it is a READER of ledgers and a WRITER
// of eval-plane artifacts that live outside every workspace. `Decide` gains
// nothing. And per M2d decision 2 it is NOT LINKED INTO `cmd/mvo`: the
// binary that races must not contain a symbol that can open the eval home.
// `scripts/accept.sh` step m2d-7c asserts that mechanically
// (`go list -deps ./cmd/mvo | grep -q internal/eval` must not match), so a
// leak through the racing binary would require an import the acceptance
// script rejects.
//
// ZERO AGENT SPEND. No code path in this package, or in any of its tests,
// invokes a real agent CLI or any API. Candidates come from an instance's
// gold patch, from mechanically derived perturbations of it, from the 22
// laundering vectors already shipped, and from worlds recorded by past runs
// (of which this repository has none). That buys a real experiment about
// SELECTION AND ALLOCATION over a fixed candidate set and nothing at all
// about GENERATION, which is why every table this plane prints carries
// SYNTHETIC-CANDIDATES.
package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
)

// Schemas. Every eval-plane artifact names its own shape, and none of them
// is a product schema: an eval artifact must never be mistakable for
// something the admission path reads.
const (
	SchemaInstance     = "multiverso.dev/eval-instance/v0"
	SchemaOracle       = "multiverso.dev/eval-oracle/v0"
	SchemaHandoff      = "multiverso.dev/eval-handoff/v0"
	SchemaLabel        = "multiverso.dev/eval-label/v0"
	SchemaManifest     = "multiverso.dev/eval-manifest/v0"
	SchemaSplit        = "multiverso.dev/eval-split/v0"
	SchemaFreeze       = "multiverso.dev/eval-freeze/v0"
	SchemaRun          = "multiverso.dev/eval-run/v0"
	SchemaNonConsult   = "multiverso.dev/eval-nonconsultation/v0"
	SchemaAdjudication = "multiverso.dev/eval-adjudication/v0"
)

// EnvHome names the eval home, and EnvPrefix is the prefix of every
// variable the racer's environment must NOT contain. Decision 2's run-time
// half: `mvo` is exec'd with every MVO_EVAL_* variable REMOVED — not
// overwritten with the empty string, removed, so os.Getenv reports absence
// rather than an empty value a code path could misread as "unset but
// present".
const (
	EnvHome   = "MVO_EVAL_HOME"
	EnvPrefix = "MVO_EVAL_"
)

// Candidate source tags (§3). The tag travels with a candidate all the way
// to the report (decision 8): there is no untagged aggregate, because the
// single most likely way this plane produces a wrong published claim is a
// pooled FAR over gold + mutants + laundering vectors reported as though it
// were FAR over agent output.
const (
	SourceGold        = "S1-gold"         // the instance's own patch, test hunks stripped
	SourceDerived     = "S2-derived"      // derive.go's mechanical perturbations
	SourceAdversarial = "S3-adversarial"  // the 22 declared laundering vectors
	SourceRecorded    = "S4-recorded-run" // worlds imported from a real run's ledger
	SourceNull        = "S5-null"         // the empty patch; unrepresentable today
)

// Instance families (decision 8b). The M2b.1 ambiguity is only resolvable
// by running both directions, so a corpus carries both and the report
// prints both columns side by side, always.
const (
	FamilyGoldPresent = "A-gold-present" // REJECT is a failure
	FamilyAllWrong    = "B-all-wrong"    // REJECT is the only correct answer
	FamilyMixed       = "C-mixed"        // k correct of N: ranking quality
)

// Label tiers (§1 decision 1, §3 item 3). v0 is Tier 1 on every instance
// because Tier 2 needs LLM-generated strengthened suites, which is spend.
// The tier is carried on every label so no aggregate silently mixes tiers.
const (
	Tier1 = 1 // the corpus's own fail_to_pass / pass_to_pass sets
	Tier2 = 2 // + a strengthened suite the corpus itself shipped
	Tier3 = 3 // + human adjudication
)

// Candidate is one candidate as the EVAL PLANE records it. It carries a
// source tag and the CAS key of its patch bytes, and it never carries a
// label: a public label is a public oracle.
//
// THE JOIN KEY IS ResultTree, AND THE FIRST END-TO-END RUN IS WHY. The obvious
// join between a race's worlds and this candidate set is the patch CAS key —
// content-addressed, order-independent, exactly what the rest of the project
// uses. It does not work: `World.Patch` is the CAS key of the CONTROL-PLANE
// CAPTURED DIFF (AG-4), which mvo re-derives from the worktree after applying,
// so it never byte-matches the input patch. Joining by ORDINAL would work and
// is what §4's pseudocode assumes ("winner w ↦ candidate ord(w)"), but it makes
// the label depend on world creation ORDER, which is a property of the racer.
//
// So the join is the digest of the TREE THE PATCH PRODUCES, computed by the
// control plane at materialization by applying the candidate to the base tree.
// It is still a content address — of the result rather than of the input — and
// it is strictly stronger than an ordinal: it depends on the bytes, and it is
// also the key that lets ONE scoring serve every arm, because two arms racing
// the same patch mint different world digests and the same tree.
type Candidate struct {
	Ord        int    `json:"ord"`
	ID         string `json:"id"`
	Source     string `json:"source"`
	Patch      string `json:"patch"` // "sha256:<hex>" of the patch bytes
	PatchBytes int64  `json:"patch_bytes"`
	// ResultTree is "git:<sha1>" of the tree this patch produces on the
	// instance's base commit. Empty is a materialization that could not
	// compute it, and an empty ResultTree means the candidate CANNOT BE
	// LABELLED — which is reported, never guessed around.
	ResultTree string `json:"result_tree"`
	// Generator, Seed and Params record HOW a derived candidate was made,
	// so a population is reproducible from the record alone (AG-3 forbids
	// unrecorded randomness). Empty for S1/S3/S4.
	Generator string `json:"generator"`
	Seed      int64  `json:"seed"`
	Params    string `json:"params"`
	// Expected is derive.go's CROSS-CHECK ONLY (decision 7): the generator
	// PROPOSES, the hidden oracle LABELS. Nothing in the metric path reads
	// it; the report prints an `expectation-violated` census over it,
	// which is information about the oracle's strength rather than an
	// error.
	Expected string `json:"expected"`
}

// Instance is the PUBLIC half of a labelled instance (decision 1). It may
// be read by anything, including the racer, because it carries no node id,
// no test file name, no gold patch and no canary token — only a DIGEST of
// the hidden half, and a digest cannot be opened.
type Instance struct {
	Schema  string `json:"schema"`
	ID      string `json:"id"`
	Corpus  string `json:"corpus"`
	Version string `json:"version"`
	Family  string `json:"family"`
	// Repo is the eval-home-RELATIVE path of the materialized repository
	// ("repos/<id>"). It is relative on purpose: an absolute path under
	// the eval home is exactly the string D1 scans argv for, and the
	// runner resolves + COPIES the repo out of the eval home before any
	// race sees it, so no race is ever handed a path inside it.
	Repo string `json:"repo"`
	// RepoURL is the upstream the fetcher cloned from, recorded for
	// citation. Never used at race time: fetch does the network, races
	// never do.
	RepoURL      string      `json:"repo_url"`
	BaseCommit   string      `json:"base_commit"`
	EnvImage     string      `json:"env_image"`
	T0OK         bool        `json:"t0_ok"`
	Task         string      `json:"task"`
	Candidates   []Candidate `json:"candidates"`
	OracleDigest string      `json:"oracle_digest"` // sha256 of the hidden bytes
	// CanaryID is the canary's IDENTITY, never its token: it lets a report
	// say which canary was in force without printing the needle.
	CanaryID string `json:"canary_id"`
	// PolicyHint names the policy under which this instance is
	// SCHEDULER-RELEVANT at all, when the shipped default kills every
	// candidate at the first rung. It is eval-plane metadata: it never
	// reaches the handoff, and a run that uses it must say so, because
	// racing under a relaxed guard is a DIFFERENT EXPERIMENT.
	PolicyHint string `json:"policy_hint"`
	// Cluster is the INDEPENDENT BUG this instance is a slice of: the fixture
	// repository it was built from. Five instances over two repositories are
	// not five independent observations — `advrepo-split-B`'s candidate set is
	// `advrepo-split-A`'s minus gold and v07, and `toyrepo-mean-A/B/C` share
	// one gold and one mutant pool — so treating instance-slices as
	// independent inflates every denominator and the paired n. Every
	// denominator prints its cluster count beside it.
	Cluster string `json:"cluster"`
	// Notes are the caveats that travel with the instance. They are printed
	// with any table this instance contributes a row to.
	Notes []string `json:"notes"`
}

// HiddenOracle is the HIDDEN half. It is written mode 0600 under a home at
// mode 0700, it is never copied into any tree a race touched, and the only
// binary that can open it is cmd/mvo-eval.
type HiddenOracle struct {
	Schema   string `json:"schema"`
	Instance string `json:"instance"`
	// FailToPass and PassToPass are node ids in the hidden suite's own
	// namespace. They are needles: D1/D4/D5 scan for them.
	FailToPass []string `json:"fail_to_pass"`
	PassToPass []string `json:"pass_to_pass"`
	// SuiteArgv is a TEMPLATE. Placeholders are substituted by the scorer
	// at run time: {python} {hidden} {repo} {report} {nonce}. The template
	// lives here rather than in the scorer so a corpus can ship its own
	// runner without a code change.
	SuiteArgv []string `json:"suite_argv"`
	TimeoutMS int64    `json:"timeout_ms"`
	// CanaryToken is D5's needle: a random 32-byte hex token embedded in
	// every hidden test file, in the node-id lists and in the gold patch
	// header, grepped over the entire workspace. It needs no enumeration
	// of leak paths, which is the whole reason it exists.
	CanaryToken string `json:"canary_token"`
	CanaryID    string `json:"canary_id"`
	// GoldPatch is the instance's own fix with test hunks stripped, and
	// StrippedHunks records WHAT was stripped (§1.3: the strip is
	// recorded). It is a candidate AND an oracle, which is why it lives
	// here: the racer receives its BYTES as an ordinary candidate, the
	// scorer receives its IDENTITY.
	GoldPatch     string   `json:"gold_patch"`
	StrippedHunks []string `json:"stripped_hunks"`
	// GoldCandidate is the candidate id whose bytes are gold, or "" on a
	// family-B instance where gold was deliberately not raced.
	GoldCandidate string `json:"gold_candidate"`
	// StrengthenedSuite is present only when the CORPUS ITSELF ships one.
	// We generate none — that is LLM work — so it is "" in v0 and the
	// field exists so a Tier-2 upgrade never requires re-racing.
	StrengthenedSuite string `json:"strengthened_suite"`
	// Files is the hidden suite itself: relative path -> contents. It is
	// mounted read-only OUTSIDE the reconstructed repo root and is never
	// written into any tree a race touched.
	Files map[string]string `json:"files"`
	Tier  int               `json:"tier"`
}

// HiddenPaths returns the hidden suite's path set, sorted. D2 tests every
// world tree against it.
func (h HiddenOracle) HiddenPaths() []string {
	out := make([]string, 0, len(h.Files))
	for p := range h.Files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// HandoffCandidate is one candidate as the RACER receives it.
type HandoffCandidate struct {
	Ord        int    `json:"ord"`
	File       string `json:"file"` // patch file name inside the handoff dir
	Patch      string `json:"patch"`
	PatchBytes int64  `json:"patch_bytes"`
}

// Handoff is THE PUBLIC PROJECTION: the only shape the racer ever receives.
//
// It is a strict subset of Instance, and deliberately narrower than the
// design's prose. §1 describes "a temporary public instance file written to
// the workspace" carrying the public part's fields, which includes the
// candidates' SOURCE TAGS. We do not ship that: a `source: "S1-gold"` tag
// in a world-readable file is gold's identity in machine-readable form, and
// §1's own argument for putting gold in the hidden half is that the racer
// must be able to tell gold from a mutant only FROM THE BYTES. So the
// handoff carries ordinals and patch bytes and nothing that ranks them; the
// source census lives in the eval-plane record, outside every workspace.
//
// It also carries no oracle digest and no canary id, though both are
// harmless in principle (a digest cannot be opened), because D1 scans argv
// for the oracle digest and the cheapest way to keep a needle out of an
// argv is for the needle never to be in the file the argv is built from.
type Handoff struct {
	Schema     string             `json:"schema"`
	Instance   string             `json:"instance"`
	BaseCommit string             `json:"base_commit"`
	EnvImage   string             `json:"env_image"`
	Task       string             `json:"task"`
	Candidates []HandoffCandidate `json:"candidates"`
}

// Project builds the public projection. It is a whitelist, field by field:
// a new hidden field cannot leak by being forgotten here, because nothing
// here is copied wholesale.
func (i Instance) Project() Handoff {
	h := Handoff{
		Schema:     SchemaHandoff,
		Instance:   i.ID,
		BaseCommit: i.BaseCommit,
		EnvImage:   i.EnvImage,
		Task:       i.Task,
		Candidates: make([]HandoffCandidate, 0, len(i.Candidates)),
	}
	for _, c := range i.Candidates {
		h.Candidates = append(h.Candidates, HandoffCandidate{
			Ord:        c.Ord,
			File:       fmt.Sprintf("cand-%02d.patch", c.Ord),
			Patch:      c.Patch,
			PatchBytes: c.PatchBytes,
		})
	}
	return h
}

// CandidateByTree finds the candidate whose patch produces a world's tree. This
// is the ONLY join between a race's worlds and the eval plane's candidate set;
// see the note on Candidate for why it is the result tree and not the patch key
// or the ordinal.
func (i Instance) CandidateByTree(tree string) (Candidate, bool) {
	if tree == "" {
		return Candidate{}, false
	}
	for _, c := range i.Candidates {
		if c.ResultTree != "" && c.ResultTree == tree {
			return c, true
		}
	}
	return Candidate{}, false
}

// Validate is total: it reports every structural problem it can see at
// once, so a malformed corpus produces one readable refusal instead of a
// sequence of them.
func (i Instance) Validate() error {
	var bad []string
	if i.Schema != SchemaInstance {
		bad = append(bad, fmt.Sprintf("schema %q, want %q", i.Schema, SchemaInstance))
	}
	if i.ID == "" {
		bad = append(bad, "empty id")
	}
	switch i.Family {
	case FamilyGoldPresent, FamilyAllWrong, FamilyMixed:
	default:
		bad = append(bad, fmt.Sprintf("family %q is not in the closed vocabulary", i.Family))
	}
	if i.OracleDigest == "" {
		bad = append(bad, "no oracle_digest: an instance with no hidden oracle cannot produce a row")
	}
	if len(i.Candidates) == 0 {
		bad = append(bad, "no candidates")
	}
	seen := map[int]bool{}
	for _, c := range i.Candidates {
		if seen[c.Ord] {
			bad = append(bad, fmt.Sprintf("duplicate candidate ordinal %d", c.Ord))
		}
		seen[c.Ord] = true
		if !strings.HasPrefix(c.Patch, "sha256:") {
			bad = append(bad, fmt.Sprintf("candidate %s: patch %q is not a CAS key", c.ID, c.Patch))
		}
		switch c.Source {
		case SourceGold, SourceDerived, SourceAdversarial, SourceRecorded, SourceNull:
		default:
			bad = append(bad, fmt.Sprintf("candidate %s: source %q is not in the closed vocabulary", c.ID, c.Source))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("eval: instance %s: %s", i.ID, strings.Join(bad, "; "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// The eval home
// ---------------------------------------------------------------------------

// DefaultHome is ~/.cache/multiverso/eval — OUTSIDE the repository and
// outside every workspace, so no corpus, image, test suite or gold patch is
// ever committed and the repository stays free of large fixtures.
func DefaultHome() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("eval: no home directory: %w", err)
	}
	return filepath.Join(h, ".cache", "multiverso", "eval"), nil
}

// HomeFromEnv resolves the eval home from MVO_EVAL_HOME, falling back to
// DefaultHome. It does NOT create anything and does not care whether the
// path exists: absence is a named skip, not an error (decision 1b).
func HomeFromEnv() (string, error) {
	if v := os.Getenv(EnvHome); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return "", fmt.Errorf("eval: %s=%q: %w", EnvHome, v, err)
		}
		return abs, nil
	}
	return DefaultHome()
}

// Store is the eval home, opened. Every read of a hidden byte in this
// project goes through here.
type Store struct {
	Home string
}

// OpenStore opens an existing eval home and refuses one whose mode lets
// anyone but the owner read it. A hidden oracle at mode 0644 is not hidden,
// and a permission check is the cheapest detector in the block.
func OpenStore(home string) (*Store, error) {
	st, err := os.Stat(home)
	if err != nil {
		return nil, fmt.Errorf("eval: eval home %s: %w", home, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("eval: eval home %s is not a directory", home)
	}
	if err := checkMode(home, st.Mode().Perm(), 0o700); err != nil {
		return nil, err
	}
	return &Store{Home: home}, nil
}

// EnsureStore creates the eval home at mode 0700 if it is absent.
func EnsureStore(home string) (*Store, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("eval: create eval home %s: %w", home, err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return nil, fmt.Errorf("eval: chmod eval home %s: %w", home, err)
	}
	return OpenStore(home)
}

func checkMode(path string, got, want fs.FileMode) error {
	if got&^want != 0 {
		return fmt.Errorf("eval: %s has mode %04o, want no bits outside %04o: "+
			"a hidden oracle that anyone can read is not hidden", path, got, want)
	}
	return nil
}

// CorpusDir is $MVO_EVAL_HOME/<corpus>/<version>.
func (s *Store) CorpusDir(corpus, version string) string {
	return filepath.Join(s.Home, corpus, version)
}

// CorpusPresent answers decision 1b's first row without opening anything.
func (s *Store) CorpusPresent(corpus, version string) bool {
	st, err := os.Stat(s.CorpusDir(corpus, version))
	return err == nil && st.IsDir()
}

func (s *Store) instancePath(corpus, version, id string) string {
	return filepath.Join(s.CorpusDir(corpus, version), "instance", id+".json")
}

func (s *Store) oraclePath(corpus, version, id string) string {
	return filepath.Join(s.CorpusDir(corpus, version), "oracle", id+".json")
}

// OraclePathFor exposes the hidden half's path for the ONE purpose a caller
// legitimately has: checking its mode. It returns a path, not bytes, and the
// only reader of those bytes in this project is LoadHidden, which verifies the
// digest link before decoding.
func (s *Store) OraclePathFor(inst Instance) string {
	return s.oraclePath(inst.Corpus, inst.Version, inst.ID)
}

// LabelPath is labels/<id>/<cand>.json — written by the scorer, AFTER the
// ledger is sealed.
func (s *Store) LabelPath(corpus, version, id, cand string) string {
	return filepath.Join(s.CorpusDir(corpus, version), "labels", id, cand+".json")
}

// RepoPath resolves an instance's materialized repository. It is the ONE
// place an eval-home path is turned into something a filesystem call can
// use, and the runner's contract is that it copies out of here rather than
// handing the result to a race.
func (s *Store) RepoPath(inst Instance) string {
	return filepath.Join(s.CorpusDir(inst.Corpus, inst.Version), inst.Repo)
}

// ListInstances lists the instance ids present in the cache, sorted. A
// missing instance directory is an empty list, not an error: absence is a
// named skip.
func (s *Store) ListInstances(corpus, version string) ([]string, error) {
	dir := filepath.Join(s.CorpusDir(corpus, version), "instance")
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("eval: list instances in %s: %w", dir, err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(out)
	return out, nil
}

// LoadInstance reads the public half.
func (s *Store) LoadInstance(corpus, version, id string) (Instance, error) {
	p := s.instancePath(corpus, version, id)
	b, err := os.ReadFile(p)
	if err != nil {
		return Instance{}, fmt.Errorf("eval: read instance %s: %w", p, err)
	}
	var inst Instance
	if err := json.Unmarshal(b, &inst); err != nil {
		return Instance{}, fmt.Errorf("eval: decode instance %s: %w", p, err)
	}
	if inst.Corpus == "" {
		inst.Corpus = corpus
	}
	if inst.Version == "" {
		inst.Version = version
	}
	if err := inst.Validate(); err != nil {
		return Instance{}, err
	}
	return inst, nil
}

// LoadHidden reads the hidden half AND verifies the one-way digest link:
// sha256(hidden bytes) must equal the public part's oracle_digest. A
// mismatch is a HARD ERROR — a corpus that drifted is a different corpus —
// and the raw bytes come back so the caller can compute the needle digests
// D3 looks for without re-serializing (a re-serialization would digest to
// something the ledger never saw).
func (s *Store) LoadHidden(inst Instance) (HiddenOracle, []byte, error) {
	p := s.oraclePath(inst.Corpus, inst.Version, inst.ID)
	st, err := os.Stat(p)
	if err != nil {
		return HiddenOracle{}, nil, fmt.Errorf("eval: read hidden oracle %s: %w", p, err)
	}
	if err := checkMode(p, st.Mode().Perm(), 0o600); err != nil {
		return HiddenOracle{}, nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return HiddenOracle{}, nil, fmt.Errorf("eval: read hidden oracle %s: %w", p, err)
	}
	if got := DigestBytes(b); got != inst.OracleDigest {
		return HiddenOracle{}, nil, fmt.Errorf(
			"eval: instance %s: %s: hidden bytes digest %s, instance says %s "+
				"(digest-mismatch is a hard error, not a skip: a corpus that drifted is a different corpus)",
			inst.ID, SkipDigestMismatch, got, inst.OracleDigest)
	}
	var h HiddenOracle
	if err := json.Unmarshal(b, &h); err != nil {
		return HiddenOracle{}, nil, fmt.Errorf("eval: decode hidden oracle %s: %w", p, err)
	}
	if h.Schema != SchemaOracle {
		return HiddenOracle{}, nil, fmt.Errorf("eval: hidden oracle %s: schema %q, want %q", p, h.Schema, SchemaOracle)
	}
	return h, b, nil
}

// WriteInstance and WriteHidden are the materialization half, used by
// `mvo-eval fetch`. WriteHidden returns the digest the public part must
// carry, so the link is computed from the bytes that were actually written
// and never from a struct that might re-serialize differently.
func (s *Store) WriteInstance(inst Instance) error {
	if err := inst.Validate(); err != nil {
		return err
	}
	b, err := object.Canonical(inst)
	if err != nil {
		return fmt.Errorf("eval: canonicalize instance %s: %w", inst.ID, err)
	}
	p := s.instancePath(inst.Corpus, inst.Version, inst.ID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("eval: create %s: %w", filepath.Dir(p), err)
	}
	return writeFileMode(p, b, 0o600)
}

// WriteHidden writes the hidden half at mode 0600 and returns its digest.
func (s *Store) WriteHidden(corpus, version string, h HiddenOracle) (string, error) {
	b, err := object.Canonical(h)
	if err != nil {
		return "", fmt.Errorf("eval: canonicalize hidden oracle %s: %w", h.Instance, err)
	}
	p := s.oraclePath(corpus, version, h.Instance)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", fmt.Errorf("eval: create %s: %w", filepath.Dir(p), err)
	}
	if err := writeFileMode(p, b, 0o600); err != nil {
		return "", err
	}
	return DigestBytes(b), nil
}

// Blob and PutBlob hold candidate patch bytes, content-addressed. They live
// in the eval home because a candidate set is corpus data; the runner
// copies the bytes it needs OUT of here before any race starts.
func (s *Store) blobPath(corpus, version, key string) (string, error) {
	hexPart, ok := strings.CutPrefix(key, "sha256:")
	if !ok || len(hexPart) != 64 {
		return "", fmt.Errorf("eval: not a CAS key: %q", key)
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", fmt.Errorf("eval: not a CAS key: %q", key)
	}
	return filepath.Join(s.CorpusDir(corpus, version), "blobs", hexPart[:2], hexPart[2:]), nil
}

// Blob reads content-addressed bytes and VERIFIES them against their key:
// the eval plane never trusts its own cache, for the same reason `mvo
// audit` sweeps the CAS.
func (s *Store) Blob(corpus, version, key string) ([]byte, error) {
	p, err := s.blobPath(corpus, version, key)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("eval: read blob %s: %w", key, err)
	}
	if got := CASKeyBytes(b); got != key {
		return nil, fmt.Errorf("eval: blob %s: %s: bytes digest %s", key, SkipDigestMismatch, got)
	}
	return b, nil
}

// PutBlob stores bytes and returns their CAS key.
func (s *Store) PutBlob(corpus, version string, b []byte) (string, error) {
	key := CASKeyBytes(b)
	p, err := s.blobPath(corpus, version, key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", fmt.Errorf("eval: create %s: %w", filepath.Dir(p), err)
	}
	if err := writeFileMode(p, b, 0o600); err != nil {
		return "", err
	}
	return key, nil
}

func writeFileMode(path string, b []byte, mode fs.FileMode) error {
	if err := os.WriteFile(path, b, mode); err != nil {
		return fmt.Errorf("eval: write %s: %w", path, err)
	}
	// WriteFile respects umask on creation and does nothing to an existing
	// file's mode, so the chmod is not redundant.
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("eval: chmod %s: %w", path, err)
	}
	return nil
}

// DigestBytes is the eval plane's spelling of a hidden-byte digest:
// "sha256:<hex>", the same string a CAS key uses, because the whole point
// of D3 is that if these bytes ever passed through the control plane they
// are in the workspace CAS under a key we can compute.
func DigestBytes(b []byte) string { return CASKeyBytes(b) }

// CASKeyBytes returns "sha256:<hex>" of b.
func CASKeyBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Degradation: skip with a reason, never fabricate (decision 1b)
// ---------------------------------------------------------------------------

// SkipReason is a CLOSED vocabulary. Every absence in this plane is one of
// these, recorded per instance, and it propagates into the metric as
// ABSENCE rather than as a zero.
type SkipReason string

const (
	SkipCorpusAbsent     SkipReason = "corpus-absent"
	SkipInstanceAbsent   SkipReason = "instance-absent"
	SkipDigestMismatch   SkipReason = "digest-mismatch"
	SkipImageAbsent      SkipReason = "image-absent"
	SkipToolAbsent       SkipReason = "tool-absent"
	SkipGoldFailsControl SkipReason = "gold-fails-control"
	SkipLeakDetected     SkipReason = "leak-detected"
	SkipUnstable         SkipReason = "unstable"
	SkipPreflightAbort   SkipReason = "preflight-abort"
)

// skipOrder fixes the census's print order. A census that reorders itself
// between runs is a diff nobody can read.
var skipOrder = []SkipReason{
	SkipCorpusAbsent,
	SkipInstanceAbsent,
	SkipDigestMismatch,
	SkipImageAbsent,
	SkipToolAbsent,
	SkipPreflightAbort,
	SkipGoldFailsControl,
	SkipLeakDetected,
	SkipUnstable,
}

// Valid reports whether r is in the closed vocabulary. A skip with a reason
// outside it is a bug, not a skip, and the harness says so.
func (r SkipReason) Valid() bool {
	for _, k := range skipOrder {
		if k == r {
			return true
		}
	}
	return false
}

// Fatal marks the two reasons that are NOT skips: a drifted corpus and a
// detected leak both abort the run non-zero. A leaked instance's TCAR is
// not a noisy measurement of scheduling — it is a measurement of something
// else entirely — so there is no "reported with a caveat" mode.
func (r SkipReason) Fatal() bool {
	return r == SkipDigestMismatch || r == SkipLeakDetected
}

// Skip is one recorded absence.
type Skip struct {
	Instance string     `json:"instance"`
	Reason   SkipReason `json:"reason"`
	Detail   string     `json:"detail"`
}

// Census is the per-run skip record. A run whose census is nonempty prints
// it ABOVE the metrics, never in a footnote.
type Census struct {
	Skips []Skip `json:"skips"`
}

// Add records a skip. An unknown reason is recorded as such rather than
// silently normalized: the vocabulary is closed and a violation must be
// visible.
func (c *Census) Add(instance string, reason SkipReason, detail string) {
	if !reason.Valid() {
		detail = fmt.Sprintf("reason %q is not in the closed skip vocabulary: %s", reason, detail)
	}
	c.Skips = append(c.Skips, Skip{Instance: instance, Reason: reason, Detail: detail})
}

// Empty reports whether anything was skipped.
func (c Census) Empty() bool { return len(c.Skips) == 0 }

// Counts tallies by reason.
func (c Census) Counts() map[SkipReason]int {
	out := map[SkipReason]int{}
	for _, s := range c.Skips {
		out[s.Reason]++
	}
	return out
}

// Fatal reports whether any recorded skip aborts the run.
func (c Census) Fatal() bool {
	for _, s := range c.Skips {
		if s.Reason.Fatal() {
			return true
		}
	}
	return false
}

// Has reports whether a reason was recorded at all.
func (c Census) Has(r SkipReason) bool {
	for _, s := range c.Skips {
		if s.Reason == r {
			return true
		}
	}
	return false
}

// Instances returns the ids skipped for reason r, sorted.
func (c Census) Instances(r SkipReason) []string {
	var out []string
	for _, s := range c.Skips {
		if s.Reason == r {
			out = append(out, s.Instance)
		}
	}
	sort.Strings(out)
	return out
}

// Lines renders the census in fixed order, one line per reason, with the
// instance ids and the first distinct detail. It is what gets printed above
// the metrics.
func (c Census) Lines() []string {
	byReason := map[SkipReason][]Skip{}
	for _, s := range c.Skips {
		byReason[s.Reason] = append(byReason[s.Reason], s)
	}
	var out []string
	for _, r := range skipOrder {
		ss := byReason[r]
		if len(ss) == 0 {
			continue
		}
		ids := make([]string, 0, len(ss))
		detail := ""
		for _, s := range ss {
			if s.Instance != "" {
				ids = append(ids, s.Instance)
			}
			if detail == "" {
				detail = s.Detail
			}
		}
		sort.Strings(ids)
		line := fmt.Sprintf("SKIP %-18s n=%d", string(r), len(ss))
		if len(ids) > 0 {
			line += " [" + strings.Join(ids, " ") + "]"
		}
		if detail != "" {
			line += ": " + detail
		}
		out = append(out, line)
	}
	// Anything outside the vocabulary prints last and loudly.
	for _, s := range c.Skips {
		if !s.Reason.Valid() {
			out = append(out, fmt.Sprintf("SKIP(INVALID) %s %s: %s", s.Reason, s.Instance, s.Detail))
		}
	}
	return out
}
