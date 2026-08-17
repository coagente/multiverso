package eval

// HIDDEN-ORACLE DISCIPLINE: FOUR DETECTORS, A CANARY, AND A PROOF OBJECT
// (§2, decisions 3–5).
//
// This is the file a reviewer attacks first, so it specifies MECHANISMS,
// never intentions. The threat is not a malicious developer; it is the
// ordinary way leaks happen — a path in an argv, a fixture copied into a
// fixture, a test that "just needs the node ids", a policy file that names the
// strengthened suite. M1f's history is the argument: the entry-point plugin
// vector was assumed closed by a path-glob set and was not, and what closed it
// was REMOVING THE CAPABILITY plus A NONCE THAT WOULD HAVE DETECTED THE
// BREACH. Enumeration is not the defence. D5 is.
//
// Everything here is PURE over recorded inputs — argv, tree listings, CAS
// keys, bytes — with one shell (WalkFiles) that turns a directory into those
// inputs. That split is deliberate: the detectors are testable without a
// filesystem, and the walker has no judgement in it.
//
// ON A HIT the instance is VOIDED: its row is dropped from every metric, the
// canary token and the hitting path are printed, and the harness exits
// non-zero. There is no "reported with a caveat" mode, because a leaked
// instance's TCAR is not a noisy measurement of scheduling — it is a
// measurement of something else entirely.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
)

// Detector ids.
const (
	DetectorArgv       = "D1-argv"
	DetectorTree       = "D2-tree"
	DetectorCAS        = "D3-cas"
	DetectorTranscript = "D4-transcript"
	DetectorCanary     = "D5-canary"
	DetectorMount      = "D6-mount" // the bind-mount refusal (decision 5's extra row)
)

// Needle kinds. The kind travels with a finding so a report can say WHAT
// leaked, which is the difference between a name collision and a breach.
const (
	NeedleCanary       = "canary-token"
	NeedleNodeID       = "hidden-node-id"
	NeedleHiddenPath   = "hidden-suite-path"
	NeedleOracleDigest = "oracle-digest"
	NeedleEvalHome     = "eval-home-path"
	NeedleGoldDigest   = "gold-patch-digest"
	NeedleHiddenDigest = "hidden-bytes-digest"
	// NeedleGoldBytes is RETIRED and kept named so a future reader finds the
	// reasoning rather than reinventing the detector. See the note on
	// Needles: gold's text is legitimately present wherever gold or a
	// derivative of gold is a candidate, so it cannot be a needle.
	NeedleGoldBytes = "gold-patch-line(retired)"
)

// Needles is the needle set. It is built from an instance and its hidden
// oracle and is the only thing the detectors know about secrecy.
type Needles struct {
	CanaryToken       string
	CanaryID          string
	NodeIDs           []string
	HiddenPaths       []string
	OracleDigest      string
	EvalHome          string
	GoldPatchDigest   string
	HiddenBytesDigest string
}

// WHY THERE IS NO GOLD-TEXT NEEDLE, and this is a finding rather than an
// omission. An earlier version of this file scanned every recorded byte surface
// for distinctive lines of the gold patch — its `index` line and its hunk
// headers — on the reasoning that gold's diff header "has no business in a
// generation transcript". The first end-to-end run voided three instances with
// it, correctly by its own rules and wrongly in fact:
//
//   - gold is RACED AS AN ORDINARY CANDIDATE on every family-A and family-C
//     instance, so its bytes are legitimately the world's captured patch and
//     (under the script adapter, whose prompt IS the patch) its context;
//   - every S2 mutant is a PERTURBATION OF GOLD, so it shares gold's hunk
//     header by construction.
//
// A text needle over gold therefore cannot distinguish a leak from the
// experiment working, and a detector that fires on the experiment working is
// not a detector. What replaces it is strictly better: the CANARY is embedded
// in the hidden half's gold header, so it appears only if the HIDDEN bytes
// leaked, and D3 checks gold's exact digest against the workspace CAS on the
// family-B instances where those bytes must not exist at all.

type needle struct {
	kind  string
	value string
}

// NeedlesFor builds the needle set. hiddenBytes must be the bytes actually
// read off disk, not a re-serialization: D3 looks for the digest the control
// plane would have assigned, and a re-serialization digests to something no
// ledger ever saw.
func NeedlesFor(inst Instance, h HiddenOracle, hiddenBytes []byte, evalHome string) Needles {
	n := Needles{
		CanaryToken:       h.CanaryToken,
		CanaryID:          h.CanaryID,
		NodeIDs:           append(append([]string{}, h.FailToPass...), h.PassToPass...),
		HiddenPaths:       h.HiddenPaths(),
		OracleDigest:      inst.OracleDigest,
		EvalHome:          evalHome,
		HiddenBytesDigest: DigestBytes(hiddenBytes),
	}
	if h.GoldPatch != "" {
		// THE NEEDLE IS THE BYTES A RACER COULD HOLD, NOT THE BYTES WE STORE.
		// The hidden half prefixes gold with `# mvo-hidden-canary <token>` so a
		// leak of gold's TEXT trips D5; the racer, when gold is raced at all,
		// receives the stripped patch WITHOUT that header. Digesting the
		// prefixed form made D3's gold needle structurally incapable of firing:
		// it is armed only on the family-B instances where gold is withheld, and
		// on exactly those instances the key it looked for was one no workspace
		// could ever contain. Digest the raceable form.
		n.GoldPatchDigest = CASKeyBytes([]byte(GoldAsRaced(h)))
	}
	sort.Strings(n.NodeIDs)
	return n
}

// GoldAsRaced is gold's patch bytes with the hidden half's canary header
// removed: what a racer would hold if gold's bytes reached a workspace. It is
// exported because both the D3 needle and `mvo-eval derive` need exactly this
// form, and two places computing it by hand is how the needle drifted in the
// first place.
func GoldAsRaced(h HiddenOracle) string {
	if h.CanaryToken == "" {
		return h.GoldPatch
	}
	return strings.TrimPrefix(h.GoldPatch, "# mvo-hidden-canary "+h.CanaryToken+"\n")
}

// textNeedles are the needles searched for as SUBSTRINGS in arbitrary bytes.
func (n Needles) textNeedles() []needle {
	var out []needle
	if n.CanaryToken != "" {
		out = append(out, needle{NeedleCanary, n.CanaryToken})
	}
	for _, id := range n.NodeIDs {
		if id != "" {
			out = append(out, needle{NeedleNodeID, id})
		}
	}
	for _, p := range n.HiddenPaths {
		if p != "" {
			out = append(out, needle{NeedleHiddenPath, p})
		}
	}
	if n.OracleDigest != "" {
		out = append(out, needle{NeedleOracleDigest, n.OracleDigest})
	}
	if n.EvalHome != "" {
		out = append(out, needle{NeedleEvalHome, n.EvalHome})
	}
	if n.HiddenBytesDigest != "" {
		out = append(out, needle{NeedleHiddenDigest, n.HiddenBytesDigest})
	}
	return out
}

// Finding is one hit. It names the detector, the surface, the reference and
// the needle — everything a reader needs to decide whether the harness or the
// experiment is broken.
type Finding struct {
	Detector   string `json:"detector"`
	Surface    string `json:"surface"`
	Ref        string `json:"ref"`
	NeedleKind string `json:"needle_kind"`
	Needle     string `json:"needle"`
	Detail     string `json:"detail"`
}

func (f Finding) String() string {
	s := fmt.Sprintf("LEAK %s %s ref=%s needle=%s:%s", f.Detector, f.Surface, f.Ref, f.NeedleKind, f.Needle)
	if f.Detail != "" {
		s += " (" + f.Detail + ")"
	}
	return s
}

// Report is the leak verdict for one instance. Scanned is not decoration: a
// report with no findings and nothing scanned is not evidence of anything, and
// the only way to tell those apart is to count what was looked at.
type Report struct {
	Findings  []Finding      `json:"findings"`
	Detectors []string       `json:"detectors"`
	Scanned   map[string]int `json:"scanned"`
	// Skipped is what a detector COULD NOT see, keyed by surface. It exists
	// because both counters that measure it — WalkFiles' size/permission cap
	// and TranscriptDocs' CAS misses — were returned and then discarded by the
	// only production caller, so a ledger.db above MaxScanBytes or a CAS blob
	// the store could not serve narrowed D4/D5 silently while the report still
	// said the detectors ran. "We scanned three of six transcripts" and "we
	// scanned six" are different claims, and Prove() refuses on the first.
	Skipped map[string]int `json:"skipped"`
}

// Void reports whether the instance must be dropped from every metric.
func (r Report) Void() bool { return len(r.Findings) > 0 }

func (r *Report) note(detector, surface string, count int) {
	if r.Scanned == nil {
		r.Scanned = map[string]int{}
	}
	r.Scanned[surface] += count
	for _, d := range r.Detectors {
		if d == detector {
			return
		}
	}
	r.Detectors = append(r.Detectors, detector)
}

// NoteSkipped records a surface a detector could not see. A cap that is silent
// is a hole, so the count travels with the report instead of being returned to
// a caller that drops it.
func (r *Report) NoteSkipped(surface string, count int) {
	if count <= 0 || surface == "" {
		return
	}
	if r.Skipped == nil {
		r.Skipped = map[string]int{}
	}
	r.Skipped[surface] += count
}

// SkippedTotal is the number of surfaces no detector could read.
func (r Report) SkippedTotal() int {
	n := 0
	for _, v := range r.Skipped {
		n += v
	}
	return n
}

// Merge folds another report in.
func (r *Report) Merge(o Report) {
	r.Findings = append(r.Findings, o.Findings...)
	for _, d := range o.Detectors {
		r.note(d, "", 0)
	}
	if r.Scanned == nil {
		r.Scanned = map[string]int{}
	}
	for k, v := range o.Scanned {
		if k == "" {
			continue
		}
		r.Scanned[k] += v
	}
	for k, v := range o.Skipped {
		r.NoteSkipped(k, v)
	}
}

// Lines renders findings in a stable order.
func (r Report) Lines() []string {
	fs := append([]Finding(nil), r.Findings...)
	sort.Slice(fs, func(i, j int) bool {
		if fs[i].Detector != fs[j].Detector {
			return fs[i].Detector < fs[j].Detector
		}
		if fs[i].Ref != fs[j].Ref {
			return fs[i].Ref < fs[j].Ref
		}
		return fs[i].Needle < fs[j].Needle
	})
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.String())
	}
	return out
}

// Doc is one recorded byte surface: a transcript, a patch, a CAS blob, a
// ledger payload, a file. Kind and Ref are for the report; Bytes is what gets
// scanned.
type Doc struct {
	Kind  string
	Ref   string
	Bytes []byte
}

// ---------------------------------------------------------------------------
// D1 — argv / config scan
// ---------------------------------------------------------------------------

// D1Argv scans every receipt's in-world argv and oracle config for any hidden
// node id, any path under the eval home, or the oracle digest.
//
// Receipts record the IN-WORLD ARGV, so this is a TOTAL scan of what actually
// ran rather than a sample of what was intended — which is the only reason it
// can be trusted. Result.Detail is scanned too: it is the one non-numeric
// string a gate may quote, so it is a path a name could reach a rationale
// through. That widening is ours, not the design's, and it costs nothing.
func D1Argv(n Needles, receipts []object.RecordedReceipt) Report {
	var r Report
	r.note(DetectorArgv, "receipt", len(receipts))
	ns := n.textNeedles()
	for _, rr := range receipts {
		surfaces := map[string]string{
			"argv":          strings.Join(rr.Receipt.Execution.Argv, " "),
			"oracle.config": rr.Receipt.Oracle.Config,
			"result.detail": rr.Receipt.Result.Detail,
		}
		for _, k := range sortedKeys(surfaces) {
			hay := surfaces[k]
			if hay == "" {
				continue
			}
			for _, nd := range ns {
				if strings.Contains(hay, nd.value) {
					r.Findings = append(r.Findings, Finding{
						Detector: DetectorArgv, Surface: "receipt." + k,
						Ref: rr.Digest, NeedleKind: nd.kind, Needle: nd.value,
						Detail: fmt.Sprintf("oracle %s@%s", rr.Receipt.Oracle.ID, rr.Receipt.Oracle.Version),
					})
				}
			}
		}
	}
	return r
}

// ---------------------------------------------------------------------------
// D2 — tree membership
// ---------------------------------------------------------------------------

// D2Trees tests every world tree (and the base tree) against the hidden
// suite's path set. Trees maps a tree ref -> the paths it contains, which is
// what `git ls-tree -r` gives.
//
// A NAME COLLISION FIRES IT TOO. If a candidate happens to write
// tests/test_issue_1234.py and the hidden suite has that path, the instance is
// voided rather than accepted: from inside the harness a collision is
// indistinguishable from a leak, and resolving that ambiguity in the
// experiment's favour is how a leaked instance gets published.
func D2Trees(n Needles, trees map[string][]string) Report {
	var r Report
	hidden := map[string]bool{}
	for _, p := range n.HiddenPaths {
		hidden[p] = true
		hidden[filepath.Base(p)] = true
	}
	total := 0
	for _, ref := range sortedKeys(trees) {
		paths := trees[ref]
		total += len(paths)
		for _, p := range paths {
			if hidden[p] || hidden[filepath.Base(p)] {
				r.Findings = append(r.Findings, Finding{
					Detector: DetectorTree, Surface: "world.tree",
					Ref: ref, NeedleKind: NeedleHiddenPath, Needle: p,
					Detail: "a hidden suite path is a member of this tree (a name collision is voided too: from here it is indistinguishable from a leak)",
				})
			}
		}
	}
	r.note(DetectorTree, "tree-entry", total)
	return r
}

// ---------------------------------------------------------------------------
// D3 — CAS absence
// ---------------------------------------------------------------------------

// D3CAS looks for the hidden bytes' digest and the gold patch's digest among
// the workspace's CAS keys. The ledger is content-addressed: if hidden bytes
// ever passed through the control plane, they are in CAS under a key we can
// compute without looking at a single blob.
//
// The gold patch is a special case and the comment matters. Gold's PATCH BYTES
// are legitimately in CAS whenever gold is raced as a candidate — that is the
// design. So D3 flags gold's digest only when goldRaced is false, which is
// exactly the family-B instances where gold was deliberately withheld.
func D3CAS(n Needles, casKeys []string, goldRaced bool) Report {
	var r Report
	r.note(DetectorCAS, "cas-key", len(casKeys))
	want := map[string]string{}
	if n.HiddenBytesDigest != "" {
		want[n.HiddenBytesDigest] = NeedleHiddenDigest
	}
	if !goldRaced && n.GoldPatchDigest != "" {
		want[n.GoldPatchDigest] = NeedleGoldDigest
	}
	for _, k := range casKeys {
		if kind, ok := want[k]; ok {
			r.Findings = append(r.Findings, Finding{
				Detector: DetectorCAS, Surface: "workspace.cas",
				Ref: k, NeedleKind: kind, Needle: k,
				Detail: "content-addressed bytes that must never have entered the control plane are in its CAS",
			})
		}
	}
	return r
}

// ---------------------------------------------------------------------------
// D4 — prompt / transcript scan
// ---------------------------------------------------------------------------

// D4Transcripts scans every world's captured patch, agent context and
// transcript for node ids, the canary, hidden suite paths and the oracle
// digest. This is accept.sh step 3r's negative test — "no corpus digest or case
// id in any prompt" — with the hidden oracle's identifiers added to the needle
// set.
//
// It does NOT scan for gold's text: see the note on Needles for why that
// detector was removed rather than tuned.
func D4Transcripts(n Needles, docs []Doc) Report {
	var r Report
	r.note(DetectorTranscript, "transcript-doc", len(docs))
	ns := n.textNeedles()
	for _, d := range docs {
		hay := string(d.Bytes)
		for _, nd := range ns {
			if strings.Contains(hay, nd.value) {
				r.Findings = append(r.Findings, Finding{
					Detector: DetectorTranscript, Surface: d.Kind,
					Ref: d.Ref, NeedleKind: nd.kind, Needle: nd.value,
				})
			}
		}
	}
	return r
}

// ---------------------------------------------------------------------------
// D5 — the canary
// ---------------------------------------------------------------------------

// D5Canary greps ONE needle — the 32-byte hex token — over every byte surface
// it is given, and it is the detector that exists because the first four are
// an enumeration.
//
// It is M1f's evidence-nonce reasoning applied to the experiment: the value of
// a nonce is that it detects the breach you did not model. D5 catches a canary
// planted in a transcript, which no tree or argv scan can see, and that case
// is exactly why acceptance step m2d-7b has a second half.
func D5Canary(n Needles, docs []Doc) Report {
	var r Report
	r.note(DetectorCanary, "doc", len(docs))
	if n.CanaryToken == "" {
		r.Findings = append(r.Findings, Finding{
			Detector: DetectorCanary, Surface: "needle-set",
			NeedleKind: NeedleCanary, Needle: "",
			Detail: "no canary token in force: D5 cannot detect what it was not given, and a run without a canary must not be reported as canary-clean",
		})
		return r
	}
	for _, d := range docs {
		if strings.Contains(string(d.Bytes), n.CanaryToken) {
			r.Findings = append(r.Findings, Finding{
				Detector: DetectorCanary, Surface: d.Kind,
				Ref: d.Ref, NeedleKind: NeedleCanary, Needle: n.CanaryToken,
			})
		}
	}
	return r
}

// MaxScanBytes caps one file's contribution to a scan. A pack file or a
// container image is not a leak surface worth loading into memory, and a cap
// that is silent is a hole: files above it are RECORDED as unscanned in the
// report's own counters.
const MaxScanBytes = 8 << 20

// WalkFiles turns a directory into Docs for D5. It is the only shell in this
// file: no judgement, no needle knowledge, and it records what it skipped.
func WalkFiles(root, kind string) ([]Doc, int, error) {
	var docs []Doc
	skipped := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished mid-walk is not a leak; a
			// directory we cannot read is not evidence of cleanliness
			// either, so it counts as skipped.
			skipped++
			return nil //nolint:nilerr // recorded, not swallowed
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > MaxScanBytes {
			skipped++
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			skipped++
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		docs = append(docs, Doc{Kind: kind, Ref: rel, Bytes: b})
		return nil
	})
	if err != nil {
		return nil, skipped, fmt.Errorf("eval: walk %s: %w", root, err)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Ref < docs[j].Ref })
	return docs, skipped, nil
}

// ---------------------------------------------------------------------------
// D6 — the bind-mount refusal (decision 5's extra row)
// ---------------------------------------------------------------------------

// CheckMounts refuses any backend.OpenOpts that names a path under the eval
// home. This row exists because M2a amendment 29 found the mount surface had
// quietly made the pinned corpus world-visible for a whole block: the
// directory was "outside the worktree" and was also its parent sibling, so
// `../corpus/corpus.json` walked out with everything.
//
// WHAT IT IS AND IS NOT, because the design row overclaimed and is now restated
// to match. It is a PREDICATE over an OpenOpts, tested against hand-built
// literals. It is NOT an assertion about a run, and it cannot be: by decision
// 2 every OpenOpts in an eval run is constructed inside the separate `mvo`
// process, which does not link internal/eval, so this package never observes
// one. What actually covers the run is three measured things elsewhere —
// Race refuses a WorkRoot inside the eval home (outsideEvalHome), the repo is
// COPIED out of the eval home before any race (RepoSrc), and argvClean asserts
// no eval-home path reached the racer's argv — and those are recorded per
// instance in the non-consultation witness. D6 is kept as the predicate a
// future in-process mount surface would be checked with, and it is not counted
// among the detectors Prove() requires, because a detector that never runs must
// not be reported as one that did.
func CheckMounts(opts backend.OpenOpts, home string) []Finding {
	if home == "" {
		return nil
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		abs = home
	}
	var out []Finding
	for _, m := range []struct{ name, path string }{
		{"evidence_dir", opts.EvidenceDir},
		{"scratch_dir", opts.ScratchDir},
		{"plugin_dir", opts.PluginDir},
		{"corpus_dir", opts.CorpusDir},
	} {
		if m.path == "" {
			continue
		}
		p, err := filepath.Abs(m.path)
		if err != nil {
			p = m.path
		}
		if p == abs || strings.HasPrefix(p, abs+string(os.PathSeparator)) {
			out = append(out, Finding{
				Detector: DetectorMount, Surface: "backend.OpenOpts." + m.name,
				Ref: m.path, NeedleKind: NeedleEvalHome, Needle: abs,
				Detail: "a mount would make the eval home visible from inside a world",
			})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The non-consultation proof
// ---------------------------------------------------------------------------

// NonConsultation is the machine-checkable witness that the racer could not
// have consulted the hidden oracle. It is the block's central artifact: the
// TCAR/FAR arithmetic is easy, and the mechanism that keeps decision and label
// apart is the deliverable.
//
// Proved is a CONJUNCTION and every conjunct is a fact somebody measured. What
// it does NOT claim is in Residuals, which are printed with it — because a
// proof object that omits its own assumptions is a stronger claim than the
// evidence supports, which is the thing this project exists to stop doing.
type NonConsultation struct {
	Schema   string `json:"schema"`
	Instance string `json:"instance"`
	// OracleDigest identifies WHICH oracle was withheld. It is a digest, so
	// printing it in a report leaks nothing: a digest cannot be opened.
	OracleDigest string `json:"oracle_digest"`
	CanaryID     string `json:"canary_id"`

	// The structural facts. Each is measured, not assumed.
	ArgvClean              bool `json:"argv_clean"`   // no needle in the argv handed to the racer
	EnvScrubbed            bool `json:"env_scrubbed"` // no MVO_EVAL_* survived into the racer
	EnvClean               bool `json:"env_clean"`    // no needle anywhere in the racer's env
	CWDOutsideEvalHome     bool `json:"cwd_outside_eval_home"`
	HiddenModeOK           bool `json:"hidden_mode_ok"` // 0600
	HomeModeOK             bool `json:"home_mode_ok"`   // 0700
	HiddenOutsideWorkspace bool `json:"hidden_outside_workspace"`
	LabelsAfterSeal        bool `json:"labels_after_seal"`
	// ScorerAfterRacer is the MEASURED ordering: the racer's last process
	// exited before the scorer was constructed. The two instants are recorded
	// beside it, because the conjunct used to be `FinishedAt != ""` — a
	// constant true — and a proof object that advertises "every conjunct is a
	// fact somebody measured" must not carry one that measures nothing. A clock
	// corroborates; the seal proves.
	ScorerAfterRacer  bool  `json:"scorer_after_racer"`
	RacerExitedAtMS   int64 `json:"racer_exited_at_ms"`
	ScorerStartedAtMS int64 `json:"scorer_started_at_ms"`
	// WorkspacesScanned is how many raced workspaces the detectors covered. It
	// is printed because "canary clean" over 1 of 11 workspaces and over 11 of
	// 11 are different claims, and the first version scanned only the reference.
	WorkspacesScanned int `json:"workspaces_scanned"`

	// What was scanned, and what was found.
	Leak Report `json:"leak"`

	// DecisionDigest and DecisionSeq are the seal: the decision existed
	// before any label did.
	DecisionDigest string `json:"decision_digest"`
	DecisionSeq    int64  `json:"decision_seq"`

	Proved    bool     `json:"proved"`
	Refusals  []string `json:"refusals"`
	Residuals []string `json:"residuals"`
}

// Residuals are the honest gaps, stated once, in code, so every report carries
// them and no reader has to find them in a design document.
func Residuals() []string {
	return []string{
		"THE LABELLER IS TWO PROCESSES AND ONLY ONE OF THEM RUNS CANDIDATE CODE. The parent holds the " +
			"check table, the node ids, the canary, the report path and the nonce, and never imports a " +
			"candidate; the child imports the candidate and returns only VALUES, with its argv scrubbed, " +
			"its script directory off sys.path and no secret in its image. So a candidate can no longer " +
			"forge its own verdict — which under v0 it could, and did in a reproduction: rewriting " +
			"sys.modules['mvo_hidden_checks'].CHECKS produced exit 0, the nonce echoed and every declared " +
			"node passing for a module that was wrong. WHAT REMAINS: the child's address space is still " +
			"shared with the candidate, so a candidate may return any value it can compute. It cannot " +
			"learn which value is WANTED, so passing means returning what a correct implementation " +
			"returns — that is the bar a black-box oracle is entitled to, and it is not a proof of " +
			"correctness on unprobed inputs.",
		"T0 puts the scorer, the racer and the candidate under one uid on one filesystem. " +
			"The hidden suite lives in its own top-level temp directory with an unguessable name, so no " +
			"relative walk from a reconstruction reaches it and that is ASSERTED " +
			"(Scorer.AssertHiddenUnreachableByRelativePath, plus the patches-hidden-reach candidate " +
			"which fails itself if it finds anything, including via sys.argv, sys.path and its cwd). " +
			"What remains: candidate code can still scan the whole temp filesystem BY ABSOLUTE PATH. " +
			"Under T1 the read-only mount closes that; at T0 it is open, and the claim is " +
			"'not delivered and not reachable by any relative path a candidate would try', never 'unreachable'.",
		"Process ordering is structural (the scorer starts after the racer exits) and the seal proves a decision " +
			"existed before a label did. Timestamps corroborate; they do not prove, and a clock is not evidence.",
		"Nothing here stops a determined developer from reading a hidden suite on the eval split. " +
			"It makes the reading leave a trace (freeze mismatch, unfreeze reason, run-count inflation) " +
			"and it stops the accidental version, which is the one that actually happens.",
		"Gold is distinguishable from a mutant BY A HUMAN READING THE BYTES (minimal, idiomatic, correctly localized). " +
			"Our machinery cannot tell, and we cannot prove a selector could not.",
	}
}

// Prove assembles the witness and decides. It refuses to report Proved=true
// when a detector did not run: a proof whose premises were skipped is a
// sentence, not a proof.
func (nc *NonConsultation) Prove() {
	nc.Schema = SchemaNonConsult
	nc.Residuals = Residuals()
	var refusals []string
	check := func(ok bool, what string) {
		if !ok {
			refusals = append(refusals, what)
		}
	}
	check(nc.ArgvClean, "a needle appears in the argv handed to the racer")
	check(nc.EnvScrubbed, "an MVO_EVAL_* variable survived into the racer's environment")
	check(nc.EnvClean, "a needle appears in the racer's environment")
	check(nc.CWDOutsideEvalHome, "the racer's cwd is inside the eval home")
	check(nc.HiddenModeOK, "the hidden oracle file is readable by more than its owner")
	check(nc.HomeModeOK, "the eval home is readable by more than its owner")
	check(nc.HiddenOutsideWorkspace, "the hidden suite was materialized inside a workspace")
	check(nc.LabelsAfterSeal, "labels were applied without a sealed decision")
	check(nc.ScorerAfterRacer, "the scorer did not start after the racer exited")
	if nc.Leak.Void() {
		refusals = append(refusals, fmt.Sprintf("%d leak finding(s): %s",
			len(nc.Leak.Findings), strings.Join(nc.Leak.Lines(), "; ")))
	}
	if n := nc.Leak.SkippedTotal(); n > 0 {
		// A detector that could not read a surface it claims to cover has not
		// covered it. Reporting Proved=true beside a nonzero skip count would
		// be "we scanned six" said about three.
		refusals = append(refusals, fmt.Sprintf(
			"%d surface(s) a detector could not read (%v): a proof whose premises were "+
				"unreadable is not a proof", n, nc.Leak.Skipped))
	}
	for _, d := range []string{DetectorArgv, DetectorTree, DetectorCAS, DetectorTranscript, DetectorCanary} {
		found := false
		for _, r := range nc.Leak.Detectors {
			if r == d {
				found = true
			}
		}
		if !found {
			refusals = append(refusals, "detector "+d+" did not run: a proof whose premises were skipped is not a proof")
		}
	}
	nc.Refusals = refusals
	nc.Proved = len(refusals) == 0
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
