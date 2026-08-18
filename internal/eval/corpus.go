package eval

// CORPUS BOOKKEEPING: the manifest, the split, the freeze and the eval-use
// counter (§1.1 and decision 6).
//
// MODULE-LAYOUT DELTA, NAMED. The design's module list assigns instance.go
// "the labelled instance: schema, load, and the public projection" and does
// not say where the manifest, the split and the freeze live. They are here
// rather than in instance.go because they are properties of a CORPUS, not of
// an instance, and because decision 6's refusal path — score --split eval
// refuses under a moved policy digest — is easier to review as one file than
// as a section of a long one.
//
// THE REPOSITORY HOLDS MANIFESTS ONLY. Text, small, reviewable, and the
// thing a paper cites. No instance payload, no image, no test suite and no
// gold patch is ever committed.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/schedule"
)

// Corpus names. The difference between them is the difference between a
// mechanism that can be acceptance-gated today and instance counts a paper
// needs, and it must never be blurred.
const (
	// CorpusLocalDerived ships. No network, n = 2 repositories: a
	// DIAGNOSIS, never a rate with a confidence interval.
	CorpusLocalDerived = "local-derived"
	// CorpusSWEBenchLivePrefix names the fetched slices. Needs network,
	// explicitly, opt-in, and printed before contact.
	CorpusSWEBenchLivePrefix = "swebench-live-"
)

// ManifestSource is one committed fixture the local-derived corpus is built
// from, pinned by digest. Its digest is over the FIXTURE BYTES IN THE
// REPOSITORY — not over the generated instance JSON — because the generator
// is code that will improve and the fixture is data that must not move
// silently. If a fixture is edited, materialization fails loudly with
// digest-mismatch, which is the correct outcome: a corpus that drifted is a
// different corpus.
type ManifestSource struct {
	Path   string `json:"path"`   // repository-relative
	Digest string `json:"digest"` // "sha256:<hex>"
}

// ManifestInstance is one fetched instance, pinned by digest. Empty digests
// are a REFUSAL, not a permission: fetch will not write bytes it cannot
// verify.
type ManifestInstance struct {
	ID           string `json:"id"`
	URL          string `json:"url"`
	PublicDigest string `json:"public_digest"`
	OracleDigest string `json:"oracle_digest"`
	ImageRef     string `json:"image_ref"`
}

// Manifest is eval/corpora/<corpus>-<version>.manifest.json.
type Manifest struct {
	Schema  string `json:"schema"`
	Corpus  string `json:"corpus"`
	Version string `json:"version"`
	// Upstream and Revision are what the paper cites. Empty for
	// local-derived, which borrows nothing.
	Upstream string `json:"upstream"`
	Revision string `json:"revision"`
	// Network is DECLARED, not inferred. A manifest that needs the network
	// says so in the file a reviewer reads, so nobody discovers it from a
	// stack trace.
	Network bool `json:"network"`
	// DigestBasis is "fixture-sources" (local-derived: the generator runs
	// here, so the pinned bytes are its inputs) or "instance-payload"
	// (fetched: the pinned bytes are the payload itself).
	DigestBasis string             `json:"digest_basis"`
	Sources     []ManifestSource   `json:"sources"`
	Instances   []ManifestInstance `json:"instances"`
	Notes       []string           `json:"notes"`
}

const (
	DigestBasisFixtures = "fixture-sources"
	DigestBasisPayload  = "instance-payload"
)

// LoadManifest reads and structurally checks a manifest.
func LoadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("eval: read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("eval: decode manifest %s: %w", path, err)
	}
	if m.Schema != SchemaManifest {
		return Manifest{}, fmt.Errorf("eval: manifest %s: schema %q, want %q", path, m.Schema, SchemaManifest)
	}
	switch m.DigestBasis {
	case DigestBasisFixtures, DigestBasisPayload:
	default:
		return Manifest{}, fmt.Errorf("eval: manifest %s: digest_basis %q is not in the closed vocabulary",
			path, m.DigestBasis)
	}
	return m, nil
}

// URLs lists every URL fetch would contact, sorted and deduplicated. It is
// the input to the "print every URL before contacting it" rule, and it is a
// pure function so the printing and the contacting cannot disagree.
func (m Manifest) URLs() []string {
	seen := map[string]bool{}
	var out []string
	if m.Upstream != "" {
		seen[m.Upstream] = true
		out = append(out, m.Upstream)
	}
	for _, in := range m.Instances {
		if in.URL == "" || seen[in.URL] {
			continue
		}
		seen[in.URL] = true
		out = append(out, in.URL)
	}
	sort.Strings(out)
	return out
}

// VerifySources checks the committed fixtures a fixture-basis manifest pins.
// The returned error names every path that moved, because fixing them one
// error at a time is how a drifted corpus gets half-fixed.
func (m Manifest) VerifySources(root string) error {
	if m.DigestBasis != DigestBasisFixtures {
		return nil
	}
	var bad []string
	for _, s := range m.Sources {
		b, err := os.ReadFile(filepath.Join(root, s.Path))
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", s.Path, err))
			continue
		}
		if got := CASKeyBytes(b); got != s.Digest {
			bad = append(bad, fmt.Sprintf("%s: %s (manifest says %s)", s.Path, got, s.Digest))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("eval: %s: manifest %s-%s pins fixtures that moved:\n  %s",
			SkipDigestMismatch, m.Corpus, m.Version, strings.Join(bad, "\n  "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// The dev/eval split (decision 6)
// ---------------------------------------------------------------------------

// DevThreshold is the first-byte cut: HMAC-SHA256(salt, id)[0] < 77 assigns
// an instance to dev, which is 77/256 ≈ 30 %. The split is a RECORDED
// FUNCTION rather than a curated list, so a hand-picked split is detectable
// by recomputing the HMAC — which SplitFile.Verify does.
const DevThreshold = 77

// Split names are the two halves, and what each licenses:
//
//	dev  — you may look at everything, including the hidden suites, and tune
//	       anything.
//	eval — you may look at the PUBLIC PROJECTION only.
//
// The scheduler, the policy and every gate see the public projection on BOTH
// splits, always: dev freedom is a developer's, never a program's.
const (
	SplitDev  = "dev"
	SplitEval = "eval"
)

// SplitFile is eval/splits/<corpus>-<version>.json.
type SplitFile struct {
	Schema  string   `json:"schema"`
	Corpus  string   `json:"corpus"`
	Version string   `json:"version"`
	Salt    string   `json:"salt"`
	Dev     []string `json:"dev"`
	Eval    []string `json:"eval"`
}

// AssignSplit is the recorded function itself.
func AssignSplit(salt, id string) string {
	mac := hmac.New(sha256.New, []byte(salt))
	mac.Write([]byte(id))
	if mac.Sum(nil)[0] < DevThreshold {
		return SplitDev
	}
	return SplitEval
}

// LoadSplit reads a split file and RECOMPUTES it. A file that disagrees with
// the function is not a split, it is a selection, and it is refused with the
// ids that moved named.
func LoadSplit(path string) (SplitFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SplitFile{}, fmt.Errorf("eval: read split %s: %w", path, err)
	}
	var s SplitFile
	if err := json.Unmarshal(b, &s); err != nil {
		return SplitFile{}, fmt.Errorf("eval: decode split %s: %w", path, err)
	}
	if s.Schema != SchemaSplit {
		return SplitFile{}, fmt.Errorf("eval: split %s: schema %q, want %q", path, s.Schema, SchemaSplit)
	}
	if err := s.Verify(); err != nil {
		return SplitFile{}, err
	}
	return s, nil
}

// Verify recomputes every assignment.
func (s SplitFile) Verify() error {
	if s.Salt == "" {
		return fmt.Errorf("eval: split %s-%s: empty salt: an unsalted split is a list", s.Corpus, s.Version)
	}
	var bad []string
	for _, id := range s.Dev {
		if got := AssignSplit(s.Salt, id); got != SplitDev {
			bad = append(bad, fmt.Sprintf("%s listed dev, HMAC says %s", id, got))
		}
	}
	for _, id := range s.Eval {
		if got := AssignSplit(s.Salt, id); got != SplitEval {
			bad = append(bad, fmt.Sprintf("%s listed eval, HMAC says %s", id, got))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("eval: split %s-%s is not the recorded function:\n  %s",
			s.Corpus, s.Version, strings.Join(bad, "\n  "))
	}
	return nil
}

// Of returns the split an id belongs to according to the file's own lists,
// and false when the file does not mention it at all (an instance nobody
// assigned is scored on no split).
func (s SplitFile) Of(id string) (string, bool) {
	for _, d := range s.Dev {
		if d == id {
			return SplitDev, true
		}
	}
	for _, e := range s.Eval {
		if e == id {
			return SplitEval, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// The freeze (decision 6)
// ---------------------------------------------------------------------------

// SchedulerConstants renders the compiled scheduler constants the freeze
// pins. It CALLS the scheduler's own renderers rather than restating the
// numbers: a snapshot that could disagree with the constant it snapshots is
// worse than no snapshot.
func SchedulerConstants() map[string]int64 {
	out := map[string]int64{}
	for k, v := range schedule.ExecutorConstants() {
		out["executor_bp."+k] = v
	}
	for k, v := range schedule.RedundancyConstants() {
		out["redundancy_bp."+k] = v
	}
	out["bound_cap"] = schedule.BoundCapDefault
	out["cost_min_samples"] = schedule.MinSamples
	return out
}

// FreezeKeyAdaptiveRule is the `constants` member that names THE ALLOCATION
// RULE the binary defaults to for `--schedule=adaptive`.
const FreezeKeyAdaptiveRule = "adaptive_rule"

// SchedulerRules renders the STRING-valued scheduler constants the freeze
// pins. There is exactly one today and it exists because of a defect this
// check had: `eval/freeze/*.json` pinned `executor_bp`, `redundancy_bp`,
// `bound_cap` and `cost_min_samples` — the scheduler's NUMBERS — and pinned
// nothing about its RULE. M2b.2 changes no constant and no policy field, so it
// would have passed the freeze silently: the mechanism that exists to make
// post-freeze tuning impossible to do quietly would have failed to notice a
// change to the allocation rule itself, which is larger than any constant it
// does pin.
//
// The fix is in the direction of MORE REFUSAL, which is the whole
// justification for touching a verification path at all, and any future edit
// to this check must be able to make the same claim.
func SchedulerRules() map[string]string {
	return map[string]string{FreezeKeyAdaptiveRule: schedule.AdaptiveRule()}
}

// FreezeFile is eval/freeze/<corpus>-<version>.json: what was true at
// FREEZE TIME. Tuning after the freeze is not forbidden; it is made
// impossible to do quietly.
type FreezeFile struct {
	Schema   string `json:"schema"`
	Corpus   string `json:"corpus"`
	Version  string `json:"version"`
	FrozenAt string `json:"frozen_at"`
	// OracleDigests pins every EVAL instance's hidden oracle. A hidden
	// suite that changed after the freeze is a different experiment.
	OracleDigests map[string]string `json:"oracle_digests"`
	// PolicyDigest is the SHIPPED DEFAULT policy's digest, and Constants
	// the scheduler constants, both at freeze time.
	PolicyDigest string           `json:"policy_digest"`
	Constants    map[string]int64 `json:"constants"`
	// Rules are the STRING-valued members of the same `constants` block —
	// `adaptive_rule` today. They are split out because the block is otherwise
	// integer-typed, and they are checked exactly as the numbers are.
	//
	// AN ABSENT `adaptive_rule` MEANS "voc" EXACTLY, not by assumption: no
	// binary before M2b.2 could allocate by any other rule. That is M2b.1
	// decision 6's normalization argument reused, and it is what lets the
	// check see the change WITHOUT ONE BYTE OF ANY FROZEN ARTIFACT MOVING —
	// writing a retroactive value into a frozen file would be editing the
	// instrument.
	Rules map[string]string `json:"-"`
	// BinaryDigest is the racing binary's digest, recorded for citation.
	// It is expected to move — every rebuild moves it — so it is NOT part
	// of the refusal: refusing on it would make the freeze a lock nobody
	// could work under, and the mechanism must survive being used.
	BinaryDigest string `json:"binary_digest"`
	// Instrument is M2d.1 decision 12: WHAT THE INSTRUMENT COULD AFFORD.
	//
	// The freeze pinned the policy digest, the scheduler constants and — since
	// M2b.2 — the allocation rule, and pinned NOTHING about the cost table the
	// arms allocate against. Warming is at least as large an effect on a
	// measured cell as `executor_bp` is: on a cold workspace nothing is
	// priced, so an unpriced purchase is affordable while any pool remains and
	// THE BUDGET DOES NOT BIND AT ALL. That is the same defect M2b.2 found one
	// level in, and the fix is in the same direction — more refusal.
	//
	// AN ABSENT BLOCK MEANS {0, cold, actual} EXACTLY, so no byte of any
	// frozen artifact has to move for the check to see the change. Same
	// argument as M2b.1's `world_order` and M2b.2's `adaptive_rule`, reused
	// because it is the same argument.
	Instrument Instrument `json:"instrument"`
	// Notes is the file's own prose: what this freeze is, and — when it is
	// re-cut — WHAT MOVED AND WHY, with the old values quoted. A moved freeze
	// with no stated reason is exactly the quiet tuning the mechanism exists
	// to prevent, so the reason lives in the artifact rather than in a commit
	// message.
	Notes []string `json:"notes"`
}

// Instrument is what the harness could afford when a cell was measured.
type Instrument struct {
	WarmupRaces int    `json:"warmup_races"`
	CostRegime  string `json:"cost_regime"`  // schedule.CostRegimeWarm | CostRegimeCold
	BudgetBasis string `json:"budget_basis"` // schedule.BudgetBasisActual | BudgetBasisPredicted
}

// ColdInstrument is the exact normalization of an ABSENT `instrument` block.
// It is EXACT rather than assumed: no binary before M2d.1 could warm an eval
// workspace, so every number M2d published is COLD-COST-TABLE and that is a
// fact about the binaries that existed rather than a guess about the runs.
func ColdInstrument() Instrument {
	return Instrument{WarmupRaces: 0, CostRegime: schedule.CostRegimeCold, BudgetBasis: schedule.BudgetBasisActual}
}

// LiveInstrument renders the instrument a run is ACTUALLY using, from the
// flag it was given and the regime its races recorded. The regime is derived
// from the artifact rather than from the flag wherever a race happened, which
// is why it is a parameter: a run that asked for `auto` and got
// `warm_incomplete` is a COLD run and must say so.
func LiveInstrument(warmup, budgetBasis, regime string) Instrument {
	races := 0
	if auto, n, err := ParseWarmup(warmup); err == nil && !auto {
		races = n
	} else if err == nil && auto {
		races = WarmupCapDefault
	}
	if regime == "" || regime == schedule.CostRegimeUnknown {
		regime = schedule.CostRegimeCold
	}
	if regime == schedule.CostRegimeCold {
		races = 0
	}
	if budgetBasis == "" {
		budgetBasis = schedule.BudgetBasisActual
	}
	return Instrument{WarmupRaces: races, CostRegime: regime, BudgetBasis: budgetBasis}
}

// CheckInstrument compares the live instrument against the freeze and names
// what moved: `instrument.cost_regime: "cold" -> "warm"`.
//
// It is a separate method rather than a fourth argument to CheckFreeze because
// the instrument is a property of the RUN's flags while everything CheckFreeze
// compares is a property of the BUILD, and folding a run-time value into a
// build-time check is how the two eventually get compared against each other's
// era.
func (f FreezeFile) CheckInstrument(live Instrument) []FreezeDrift {
	frozen := f.Instrument
	if frozen == (Instrument{}) {
		frozen = ColdInstrument()
	}
	var out []FreezeDrift
	if frozen.CostRegime != live.CostRegime {
		out = append(out, FreezeDrift{What: "instrument.cost_regime",
			Frozen: frozen.CostRegime, Now: live.CostRegime})
	}
	if frozen.BudgetBasis != live.BudgetBasis {
		out = append(out, FreezeDrift{What: "instrument.budget_basis",
			Frozen: frozen.BudgetBasis, Now: live.BudgetBasis})
	}
	if frozen.WarmupRaces != live.WarmupRaces {
		out = append(out, FreezeDrift{What: "instrument.warmup_races",
			Frozen: fmt.Sprint(frozen.WarmupRaces), Now: fmt.Sprint(live.WarmupRaces)})
	}
	return out
}

// FreezeDrift is one thing that moved since the freeze.
type FreezeDrift struct {
	What   string `json:"what"`
	Frozen string `json:"frozen"`
	Now    string `json:"now"`
}

// MarshalJSON writes a freeze file, MERGING `Rules` back into the single
// `constants` block that `UnmarshalJSON` splits.
//
// IT EXISTS BECAUSE THE CHECK HAD NO WRITE PATH AND THEREFORE NO FIXED POINT.
// `Rules` is `json:"-"`, so without this a `mvo-eval freeze` emitted a file
// with no `adaptive_rule` key, which the reader then normalizes to "voc" — so
// a freeze cut by a `voc2`-default binary was refused by the very next run of
// that same binary, forever, with no way to record the truth except by hand-
// editing the artifact. That is the failure mode this freeze's own notes name
// in as many words: committing a value guaranteed to mismatch "would train
// every reader to pass --unfreeze, which is worse than not checking". A
// round-trip test pins it: a freeze written by this build and re-read by this
// build reports zero drift.
//
// Decision 8's absent-means-"voc" normalization is untouched and is about
// PRE-EXISTING artifacts; this is about the value the current binary writes.
func (f FreezeFile) MarshalJSON() ([]byte, error) {
	type alias FreezeFile
	consts := make(map[string]any, len(f.Constants)+len(f.Rules))
	for k, v := range f.Constants {
		consts[k] = v
	}
	for k, v := range f.Rules {
		if _, clash := consts[k]; clash {
			return nil, fmt.Errorf("eval: freeze constant %q is pinned as both a number and a rule", k)
		}
		consts[k] = v
	}
	return json.Marshal(struct {
		alias
		Constants map[string]any `json:"constants"`
	}{alias: alias(f), Constants: consts})
}

// UnmarshalJSON reads a freeze file, splitting the `constants` block into its
// numeric and its string-valued members and normalizing an absent
// `adaptive_rule` to "voc".
//
// The split is a decoding detail, not a schema change: `constants` is one
// block in the file and stays one block. What it buys is that a frozen
// artifact written before the rule had a name still says, exactly, which rule
// it was frozen against.
func (f *FreezeFile) UnmarshalJSON(b []byte) error {
	type alias FreezeFile
	var raw struct {
		alias
		Constants map[string]json.RawMessage `json:"constants"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*f = FreezeFile(raw.alias)
	f.Constants = map[string]int64{}
	f.Rules = map[string]string{}
	for k, v := range raw.Constants {
		var n int64
		if err := json.Unmarshal(v, &n); err == nil {
			f.Constants[k] = n
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			f.Rules[k] = s
			continue
		}
		return fmt.Errorf("eval: freeze constant %q is neither an integer nor a string", k)
	}
	if f.Rules[FreezeKeyAdaptiveRule] == "" {
		f.Rules[FreezeKeyAdaptiveRule] = schedule.SelectorNameVOC
	}
	// AN ABSENT `instrument` BLOCK MEANS COLD, EXACTLY. Decision 12's
	// normalization, and the reason no byte of any pre-M2d.1 freeze file has
	// to move for the check to see the change.
	if f.Instrument == (Instrument{}) {
		f.Instrument = ColdInstrument()
	}
	if f.Instrument.CostRegime == "" {
		f.Instrument.CostRegime = schedule.CostRegimeCold
	}
	if f.Instrument.BudgetBasis == "" {
		f.Instrument.BudgetBasis = schedule.BudgetBasisActual
	}
	return nil
}

// CheckFreeze compares the live world against the freeze and names what
// moved. It reports EVERY difference, because "the policy digest moved" and
// "the policy digest and executor_bp moved" are different findings.
func (f FreezeFile) CheckFreeze(policyDigest string, constants map[string]int64,
	rules map[string]string, oracleDigests map[string]string) []FreezeDrift {

	var out []FreezeDrift
	if f.PolicyDigest != policyDigest {
		out = append(out, FreezeDrift{What: "policy_digest", Frozen: f.PolicyDigest, Now: policyDigest})
	}
	// THE RULE IS CHECKED EXACTLY AS THE NUMBERS ARE, and a moved rule reads
	// `constants.adaptive_rule (frozen voc, now voc2)` — the sentence that
	// tells a reader the allocator itself changed rather than one of its
	// knobs.
	for _, k := range unionKeys(f.Rules, rules) {
		fv, fok := f.Rules[k]
		nv, nok := rules[k]
		switch {
		case fok && nok && fv != nv:
			out = append(out, FreezeDrift{What: "constants." + k, Frozen: fv, Now: nv})
		case fok && !nok:
			out = append(out, FreezeDrift{What: "constants." + k, Frozen: fv, Now: "absent"})
		case !fok && nok:
			out = append(out, FreezeDrift{What: "constants." + k, Frozen: "absent", Now: nv})
		}
	}
	keys := map[string]bool{}
	for k := range f.Constants {
		keys[k] = true
	}
	for k := range constants {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fv, fok := f.Constants[k]
		nv, nok := constants[k]
		switch {
		case fok && nok && fv != nv:
			out = append(out, FreezeDrift{What: "constants." + k,
				Frozen: fmt.Sprint(fv), Now: fmt.Sprint(nv)})
		case fok && !nok:
			out = append(out, FreezeDrift{What: "constants." + k, Frozen: fmt.Sprint(fv), Now: "absent"})
		case !fok && nok:
			out = append(out, FreezeDrift{What: "constants." + k, Frozen: "absent", Now: fmt.Sprint(nv)})
		}
	}
	ids := make([]string, 0, len(f.OracleDigests))
	for id := range f.OracleDigests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		now, ok := oracleDigests[id]
		if !ok {
			// Absent is absent: an instance the run did not score cannot
			// have drifted, so it is not reported as drift.
			continue
		}
		if now != f.OracleDigests[id] {
			out = append(out, FreezeDrift{What: "oracle_digest." + id, Frozen: f.OracleDigests[id], Now: now})
		}
	}
	return out
}

// unionKeys is the union of two maps' keys, sorted: a drift report has to be
// deterministic, and a key present on only one side is exactly the kind of
// difference the check exists to name.
func unionKeys(a map[string]string, b map[string]string) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LoadFreeze reads a freeze file.
func LoadFreeze(path string) (FreezeFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return FreezeFile{}, fmt.Errorf("eval: read freeze %s: %w", path, err)
	}
	var f FreezeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return FreezeFile{}, fmt.Errorf("eval: decode freeze %s: %w", path, err)
	}
	if f.Schema != SchemaFreeze {
		return FreezeFile{}, fmt.Errorf("eval: freeze %s: schema %q, want %q", path, f.Schema, SchemaFreeze)
	}
	return f, nil
}

// ---------------------------------------------------------------------------
// The eval-use counter (decision 6)
// ---------------------------------------------------------------------------

// EvalRun is one line of $MVO_EVAL_HOME/<corpus>/eval-runs.log. Each eval
// scoring is a leaderboard query in Blum & Hardt's sense, and the query
// count is published rather than described as restraint.
type EvalRun struct {
	TS            string   `json:"ts"`
	BinaryDigest  string   `json:"binary_digest"`
	PolicyDigest  string   `json:"policy_digest"`
	Arms          []string `json:"arms"`
	InstanceCount int      `json:"instance_count"`
	Split         string   `json:"split"`
	Unfreeze      string   `json:"unfreeze"`
}

// AppendEvalRun appends exactly one line. Append-only by construction:
// O_APPEND, one Write, no truncate path anywhere in this package.
func (s *Store) AppendEvalRun(corpus string, r EvalRun) error {
	if r.TS == "" {
		r.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if r.Arms == nil {
		r.Arms = []string{}
	}
	b, err := object.Canonical(r)
	if err != nil {
		return fmt.Errorf("eval: canonicalize eval-run line: %w", err)
	}
	dir := filepath.Join(s.Home, corpus)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("eval: create %s: %w", dir, err)
	}
	p := filepath.Join(dir, "eval-runs.log")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("eval: open %s: %w", p, err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("eval: append %s: %w", p, err)
	}
	return nil
}

// ReadEvalRuns reads the counter back. A missing log is zero runs, not an
// error.
func (s *Store) ReadEvalRuns(corpus string) ([]EvalRun, error) {
	p := filepath.Join(s.Home, corpus, "eval-runs.log")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("eval: read %s: %w", p, err)
	}
	var out []EvalRun
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r EvalRun
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("eval: decode %s: %w", p, err)
		}
		out = append(out, r)
	}
	return out, nil
}
