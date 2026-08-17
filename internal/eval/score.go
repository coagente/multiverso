package eval

// SCORING: A FRESH RECONSTRUCTION, AFTER THE LEDGER IS SEALED (decision 3).
//
// MODULE-LAYOUT DELTA, NAMED. The design's module list does not assign the
// reconstruction and the hidden-suite invocation a file; label.go is given "the
// label store, the vocabulary, unknown propagation and the controls", which is
// the PURE half. This file is the impure half, kept separate so that the judge
// stays testable without a filesystem.
//
// THE MECHANISM, and it is arithmetic over directory identity rather than a
// policy:
//
//	1. The race runs to completion; the decision is recorded; the ledger is
//	   sealed. Nothing here runs before that, and Store.Apply refuses a label
//	   set without the seal.
//	2. For each candidate the scorer reconstructs its tree FROM `world.tree`
//	   via gitx.LsTreeRecursive/CatBlob into a brand-new directory under the
//	   scorer's own tmpdir — a directory no race, no world, no oracle and no
//	   keeper ever saw.
//	3. The hidden suite is materialized OUTSIDE the reconstructed repo root.
//	   It is never copied into the repo tree, never staged, never committed,
//	   never in `git status`.
//	4. The suite runs; its report is written to control-plane scratch outside
//	   the reconstructed tree; the reconstruction is deleted.
//
// So "no hidden test entered a world tree" is a property of where the bytes
// were written, and the ordering claim is process-structural: the scorer runs
// after the racer's process exits. Timestamps corroborate; they do not prove,
// and this comment does not pretend a clock is evidence.
//
// THE LABELLER IS INSIDE THE TRUST BOUNDARY TOO (decision 4). Scoring imports
// the candidate's modules — that is the point of it — so the candidate executes
// code during its own labelling. The invocation is therefore the hardened
// recipe scripts/adversarial.sh already uses for its ground truth:
// `python3 -S` (no site, so no sitecustomize), `-B` (no bytecode written into
// the tree it is judging), PYTHONNOUSERSITE=1,
// PYTEST_DISABLE_PLUGIN_AUTOLOAD=1, an explicit sys.path built by the runner,
// and a cwd outside the reconstructed repo so the empty path entry is not the
// candidate's own directory. Candidate MODULE code runs, which is unavoidable
// and is what is being measured; candidate HOOK code does not load.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
)

// HiddenDirPrefix is the prefix of the RANDOMLY NAMED temporary directory the
// hidden suite is materialized in. Under T1 the same directory is bind mounted
// read-only at InWorldHidden.
//
// WHY IT IS A SEPARATE TEMP ROOT WITH A RANDOM NAME, and this is a fix rather
// than a preference. The design says the suite lives "outside the reconstructed
// repo root ... at /mvo/hidden (T1) or a sibling path (T0)", and the first
// implementation of this file did exactly that: `<root>/hidden` beside
// `<root>/recon/<tree>`. At T0 that is REACHABLE BY A RELATIVE PATH —
// `../../hidden/mvo_hidden_run.py` from inside the reconstruction — and the
// scorer imports candidate module code by construction, so a candidate could
// read the suite that is judging it. That is the same mistake M2a amendment 29
// found with the pinned corpus, where "outside the worktree" was also "the
// parent's sibling" and `../corpus/corpus.json` walked out with everything.
//
// So the hidden suite gets its own top-level temp directory with an unguessable
// name, unrelated to the reconstruction's parent. What remains, and is named in
// Residuals rather than implied away: a determined candidate can still scan the
// whole temp filesystem by absolute path. Under T1 the mount is what closes
// that; at T0 the honest claim is "not delivered, and not reachable by any
// relative path a candidate would try" — which is precisely the claim
// testdata/toyrepo/patches-hidden-reach asserts instead of asserting.
const HiddenDirPrefix = "mvo-hidden-"

// InWorldHidden is the T1 mount point.
const InWorldHidden = "/mvo/hidden"

// Scorer runs the hidden oracle. One Scorer per instance per batch, so the
// controls bind to exactly the labels they were measured beside.
type Scorer struct {
	Instance    Instance
	Hidden      HiddenOracle
	HiddenBytes []byte
	// Repo is the git repository whose object store holds the world trees.
	// The scorer READS it and writes nothing into it.
	Repo string
	// Root is the scorer's own tmpdir: the parent of every reconstruction,
	// the hidden mount and the scratch. It must be outside the workspace and
	// outside the eval home, and New refuses otherwise.
	Root string
	// Python is the interpreter. Absent is a NAMED SKIP (tool-absent), never
	// a fabricated label.
	Python string
	// TimeoutMS overrides the hidden oracle's own timeout when nonzero.
	TimeoutMS int64

	hiddenDir string
	scratch   string
	// runDir is the hidden runner's cwd: a directory that is EMPTY and stays
	// empty. It is not the scratch, because the scratch holds reports.
	runDir string
	// reportsWritten and reportsDrained count what was written into the
	// scratch and what was removed after being parsed. AssertScratchDrained
	// turns "we delete the reports" from a comment into an assertion.
	reportsWritten int
	reportsDrained int
}

// NewScorer prepares the scorer's private directories.
func NewScorer(inst Instance, h HiddenOracle, hiddenBytes []byte, repo, root, python string) (*Scorer, error) {
	if root == "" {
		return nil, errors.New("eval: scorer needs its own root directory")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("eval: resolve scorer root: %w", err)
	}
	wsAbs, err := filepath.Abs(repo)
	if err != nil {
		return nil, fmt.Errorf("eval: resolve repo: %w", err)
	}
	if abs == wsAbs || strings.HasPrefix(abs, wsAbs+string(os.PathSeparator)) {
		return nil, fmt.Errorf("eval: scorer root %s is inside the workspace %s: "+
			"the reconstruction must live where no race could see it", abs, wsAbs)
	}
	s := &Scorer{Instance: inst, Hidden: h, HiddenBytes: hiddenBytes,
		Repo: repo, Root: abs, Python: python}
	// The hidden suite gets its OWN top-level temp directory with an
	// unguessable name: see HiddenDirPrefix for why a sibling of the
	// reconstruction is not good enough.
	hd, err := os.MkdirTemp("", HiddenDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("eval: create the hidden suite directory: %w", err)
	}
	if err := os.Chmod(hd, 0o700); err != nil {
		return nil, fmt.Errorf("eval: chmod %s: %w", hd, err)
	}
	s.hiddenDir = hd
	s.scratch = filepath.Join(abs, "scratch")
	if err := os.MkdirAll(s.scratch, 0o700); err != nil {
		return nil, fmt.Errorf("eval: create %s: %w", s.scratch, err)
	}
	// THE RUNNER'S CWD IS NOT THE SCRATCH, and that is a fix rather than a
	// preference. The first implementation set cmd.Dir = s.scratch, and the
	// scratch is where every hidden report lands — each one carrying the canary
	// and the complete f2p/p2p node-id list. Because candidate module code ran
	// in that same process, the candidate's cwd was the directory holding the
	// oracle's own outputs, and the negative and positive controls run FIRST, so
	// the very first candidate scored saw a full pass/fail map of the oracle. No
	// detector covered it: D5 scans the racer's workspace, and the reports are
	// named report-<nonce>.xml, which matches no hidden-suite basename.
	//
	// Under the v1 split the candidate no longer runs in this process at all,
	// but a directory that must stay unreadable should not also be a working
	// directory, so the runner is given an empty one and the reports are removed
	// as soon as they are parsed (see RunHidden and AssertScratchDrained).
	s.runDir = filepath.Join(abs, "run")
	if err := os.MkdirAll(s.runDir, 0o700); err != nil {
		return nil, fmt.Errorf("eval: create %s: %w", s.runDir, err)
	}
	if err := s.materializeHidden(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close removes the hidden suite directory. It is not optional bookkeeping: the
// directory holds the node ids, the canary and the gold patch, and leaving it
// behind after a run makes every later run's D5 scan of that filesystem a lie.
func (s *Scorer) Close() error {
	if s.hiddenDir == "" {
		return nil
	}
	if err := os.RemoveAll(s.hiddenDir); err != nil {
		return fmt.Errorf("eval: remove the hidden suite directory %s: %w", s.hiddenDir, err)
	}
	s.hiddenDir = ""
	return nil
}

// HiddenDir is where the suite lives: a sibling of every reconstruction, never
// a child of one.
func (s *Scorer) HiddenDir() string { return s.hiddenDir }

// ScratchDir is control-plane scratch, outside every reconstructed tree.
func (s *Scorer) ScratchDir() string { return s.scratch }

func (s *Scorer) materializeHidden() error {
	if len(s.Hidden.Files) == 0 {
		return fmt.Errorf("eval: instance %s: hidden oracle ships no suite files", s.Instance.ID)
	}
	for _, rel := range s.Hidden.HiddenPaths() {
		p := filepath.Join(s.hiddenDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return fmt.Errorf("eval: create %s: %w", filepath.Dir(p), err)
		}
		if err := writeFileMode(p, []byte(s.Hidden.Files[rel]), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// Reconstruct materializes one tree into a brand-new directory under the
// scorer's root and returns its path. The directory name is derived from the
// tree digest, so two reconstructions of the same tree collide and the second
// is a no-op — which is what makes m2d-7d's byte-identity assertion cheap.
func (s *Scorer) Reconstruct(tree string) (string, error) {
	treeish := strings.TrimPrefix(tree, "git:")
	if treeish == "" {
		return "", fmt.Errorf("eval: empty tree digest")
	}
	dir := filepath.Join(s.Root, "recon", sanitizeName(treeish))
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("eval: clear %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("eval: create %s: %w", dir, err)
	}
	ents, err := gitx.LsTreeRecursive(s.Repo, treeish)
	if err != nil {
		return "", fmt.Errorf("eval: ls-tree %s: %w", treeish, err)
	}
	for _, e := range ents {
		if e.Type != "blob" {
			continue
		}
		b, err := gitx.CatBlob(s.Repo, e.SHA)
		if err != nil {
			return "", fmt.Errorf("eval: cat-blob %s (%s): %w", e.SHA, e.Name, err)
		}
		p := filepath.Join(dir, e.Name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return "", fmt.Errorf("eval: create %s: %w", filepath.Dir(p), err)
		}
		mode := os.FileMode(0o600)
		if strings.HasSuffix(e.Mode, "755") {
			mode = 0o700
		}
		if err := writeFileMode(p, b, mode); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// RunHidden runs the hidden suite over a reconstructed tree and returns the
// Observation. It NEVER returns a verdict: judging is label.go's pure job, and
// keeping the two apart is what makes the cross-check testable.
func (s *Scorer) RunHidden(reconDir string) (Observation, error) {
	obs := Observation{Reconstructed: true}
	if s.Python == "" {
		return Observation{}, fmt.Errorf("eval: %s: no python3 interpreter", SkipToolAbsent)
	}
	nonce, err := Nonce()
	if err != nil {
		return Observation{}, err
	}
	obs.Nonce = nonce
	reportPath := filepath.Join(s.scratch, "report-"+nonce+".xml")
	s.reportsWritten++
	argv := s.Argv(reconDir, reportPath, nonce)
	if len(argv) == 0 {
		return Observation{}, fmt.Errorf("eval: instance %s: hidden oracle declares no suite argv", s.Instance.ID)
	}
	timeout := s.Hidden.TimeoutMS
	if s.TimeoutMS > 0 {
		timeout = s.TimeoutMS
	}
	if timeout <= 0 {
		timeout = 60_000
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	// The cwd is an EMPTY directory: outside the reconstructed tree so the
	// interpreter's implicit path entry is not the candidate's own directory,
	// and outside the scratch so it is not the directory the reports land in.
	cmd.Dir = s.runDir
	cmd.Env = HiddenEnv()
	// THE CONTROL CHANNEL. The report path, the nonce and the timeout travel on
	// the runner's STDIN and appear in no argv anywhere in the process tree.
	// Under v0 they were `--report <path> --nonce <value>` on the argv of the
	// very process that imported the candidate's modules, so a candidate that
	// read sys.argv could write an all-pass report and echo the nonce itself.
	ctl, err := jsonControl(reportPath, nonce, timeout)
	if err != nil {
		return Observation{}, err
	}
	cmd.Stdin = strings.NewReader(ctl)
	start := time.Now()
	done := make(chan error, 1)
	var combined strings.Builder
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := cmd.Start(); err != nil {
		obs.RunnerErr = err.Error()
		return obs, nil
	}
	go func() { done <- cmd.Wait() }()
	select {
	case werr := <-done:
		obs.DurationMS = time.Since(start).Milliseconds()
		if werr != nil {
			var ee *exec.ExitError
			if errors.As(werr, &ee) {
				obs.ExitCode = ee.ExitCode()
			} else {
				obs.RunnerErr = werr.Error()
			}
		}
	case <-time.After(time.Duration(timeout) * time.Millisecond):
		_ = cmd.Process.Kill()
		<-done
		obs.TimedOut = true
		obs.DurationMS = time.Since(start).Milliseconds()
		return obs, nil
	}
	b, rerr := os.ReadFile(reportPath)
	if rerr != nil {
		// No report is not an error: it is a cross-check failure, and the
		// judge turns it into `unknown` with a reason. The runner's own
		// output is ALWAYS kept here, including on a non-zero exit: keeping
		// it only when the exit code was 0 discarded the stderr of exactly
		// the runs that crashed, which left "empty report" as the whole
		// diagnosis of a child that never started.
		detail := strings.TrimSpace(combined.String())
		if detail == "" {
			detail = "(the runner wrote nothing to stdout or stderr)"
		}
		if obs.RunnerErr != "" {
			obs.RunnerErr += "; "
		}
		obs.RunnerErr += fmt.Sprintf("no report written (exit %d): %s", obs.ExitCode, detail)
		return obs, nil
	}
	obs.ReportXML = b
	obs.ReportKey = CASKeyBytes(b)
	// THE REPORT IS REMOVED AS SOON AS IT IS PARSED. Its bytes carry the canary
	// and the complete f2p/p2p node-id list; the run manifest keeps its CAS key,
	// which is a digest and cannot be opened. Leaving them behind made the
	// scorer's scratch an accumulating pile of the oracle's own answers — ten of
	// them on one kept run — which no leak detector scans.
	if err := os.Remove(reportPath); err != nil && !os.IsNotExist(err) {
		return obs, fmt.Errorf("eval: remove the hidden report %s: %w", reportPath, err)
	}
	s.reportsDrained++
	return obs, nil
}

// jsonControl is the runner's control channel: the report path, the nonce and
// the timeout, encoded for stdin. The timeout handed to the runner is SHORTER
// than the scorer's own kill, so a hung child is reported by the runner (exit
// 4, no report, hence `unknown`) rather than killed by us halfway through
// writing one.
func jsonControl(reportPath, nonce string, timeoutMS int64) (string, error) {
	inner := timeoutMS * 8 / 10
	if inner <= 0 {
		inner = timeoutMS
	}
	b, err := json.Marshal(struct {
		Report    string `json:"report"`
		Nonce     string `json:"nonce"`
		TimeoutMS int64  `json:"timeout_ms"`
	}{reportPath, nonce, inner})
	if err != nil {
		return "", fmt.Errorf("eval: encode the hidden runner's control channel: %w", err)
	}
	return string(b) + "\n", nil
}

// AssertScratchDrained refuses when a hidden report survived its parse. It is
// the assertion that replaces a sentence: every report holds the canary and the
// node ids, so "we delete them" has to be measured rather than promised.
func (s *Scorer) AssertScratchDrained() error {
	if s.reportsDrained != s.reportsWritten {
		// A report that was never written (a crashed runner) is not a leak, so
		// the counters may legitimately differ; what must not survive is a FILE.
		_ = s.reportsDrained
	}
	ents, err := os.ReadDir(s.scratch)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("eval: read the scorer scratch %s: %w", s.scratch, err)
	}
	var left []string
	for _, e := range ents {
		left = append(left, e.Name())
	}
	if len(left) > 0 {
		sort.Strings(left)
		return fmt.Errorf("eval: %s: the scorer's scratch still holds %s after scoring: "+
			"every hidden report carries the canary and the full node-id list, and a scratch "+
			"that accumulates them is a leak surface no detector scans",
			SkipLeakDetected, strings.Join(left, " "))
	}
	return nil
}

// Argv substitutes the hidden oracle's template. It is a separate, pure-ish
// method so a test can assert what would be executed without executing it —
// which is how "no hidden path is in an oracle argv" gets asserted rather than
// assumed.
//
// The {report} and {nonce} placeholders are still substituted for any corpus
// whose template names them, but the shipped local-derived template NO LONGER
// DOES: both travel on the runner's stdin instead, because under v0 they sat on
// the argv of the very process that imported the candidate's modules.
func (s *Scorer) Argv(reconDir, reportPath, nonce string) []string {
	repl := map[string]string{
		"{python}": s.Python,
		"{hidden}": s.hiddenDir,
		"{repo}":   reconDir,
		"{report}": reportPath,
		"{nonce}":  nonce,
	}
	out := make([]string, 0, len(s.Hidden.SuiteArgv))
	for _, a := range s.Hidden.SuiteArgv {
		for k, v := range repl {
			a = strings.ReplaceAll(a, k, v)
		}
		out = append(out, a)
	}
	return out
}

// HiddenEnv is the labeller's hardened environment. It is CLOSED — built from
// nothing, not filtered from the ambient environment — so a variable a
// developer exported cannot change a label.
func HiddenEnv() []string {
	return []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"PYTHONNOUSERSITE=1",
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTEST_DISABLE_PLUGIN_AUTOLOAD=1",
		"PYTHONHASHSEED=0",
		"LC_ALL=C",
		"HOME=/nonexistent",
	}
}

// Nonce is the per-run cross-check nonce: 16 random bytes, hex.
func Nonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("eval: nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// CanaryToken is D5's needle: 32 random bytes, hex. It is generated once per
// instance at materialization and embedded in every hidden test file, in the
// node-id lists and in the gold patch header.
func CanaryToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("eval: canary: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Batch is one scoring batch: the labels, the controls that bound them, and
// the observations they were derived from.
type Batch struct {
	Instance string                 `json:"instance"`
	Labels   []Label                `json:"labels"`
	Pre      []Label                `json:"pre_control_labels"`
	Controls ControlOutcome         `json:"controls"`
	Obs      map[string]Observation `json:"observations"`
	// WorldVerdict maps world digest -> verdict, which is what A9 consumes.
	WorldVerdict map[string]string `json:"world_verdict"`
	// TreeVerdict maps world TREE -> verdict. It is the portable key: a
	// world digest binds created_at, the agent RunCost and a transcript
	// digest, so two arms racing the same patch produce different world
	// digests and the SAME tree. Labelling once per instance and joining by
	// tree is the only way a label computed on one arm's workspace can
	// legitimately score another's (M2b.1 F2's gap, closed for LABELS even
	// though it stays open for receipts).
	TreeVerdict map[string]string `json:"tree_verdict"`
	ScoringMS   int64             `json:"scoring_ms"`
	Skipped     SkipReason        `json:"skipped"`
	SkipDetail  string            `json:"skip_detail"`
	// Unjoined lists worlds no candidate's result tree matched. A nonempty
	// Unjoined on an instance whose candidates all raced is a HARNESS BUG,
	// not a skip, and it is recorded so that it cannot present as "0 labels"
	// the way it did on this block's first end-to-end run.
	Unjoined []string `json:"unjoined"`
}

// ScoreBatch labels every world in a sealed ledger view, runs the two controls
// in the SAME environment, and applies unknown-propagation at batch scope.
//
// THE JOIN FROM A WORLD TO A CANDIDATE IS BY RESULT TREE
// (Instance.CandidateByTree): the digest of the tree the candidate's patch
// produces on the base commit, which is the same digest the race records on the
// world. It is not the patch CAS key and not the ordinal — the long note on
// eval.Candidate says why the patch key does not work (the racer re-derives the
// patch it applies, so the bytes it records are not always the bytes it was
// handed) and why the ordinal is the racer's rather than ours. An earlier
// version of this comment named a `CandidateByPatch` that has never existed.
func (s *Scorer) ScoreBatch(v LedgerView, baseTree string) (Batch, error) {
	start := time.Now()
	b := Batch{
		Instance:     s.Instance.ID,
		Obs:          map[string]Observation{},
		WorldVerdict: map[string]string{},
		TreeVerdict:  map[string]string{},
	}
	// The controls first: a batch whose controls moved must not spend
	// minutes labelling worlds whose labels it is about to void.
	baseObs, goldObs, err := s.runControls(baseTree)
	if err != nil {
		return b, err
	}
	b.Obs["control:negative"] = baseObs
	b.Obs["control:positive"] = goldObs
	b.Controls = CheckControls(s.Hidden, baseObs, goldObs)

	for _, w := range v.Worlds {
		cand, ok := s.Instance.CandidateByTree(w.World.Tree)
		if !ok {
			// A world whose tree no candidate produces is not ours to
			// label. That is a real condition (a workspace with another
			// race in it, or a candidate whose result tree could not be
			// computed) and it is skipped rather than guessed at.
			b.Unjoined = append(b.Unjoined, w.Digest+" tree="+w.World.Tree)
			continue
		}
		obs, err := s.scoreTree(w.World.Tree)
		if err != nil {
			return b, err
		}
		b.Obs[w.Digest] = obs
		l := Judge(JudgeInput{
			Instance:  s.Instance.ID,
			Candidate: cand,
			Hidden:    s.Hidden,
			Obs:       obs,
			WorldTree: w.World.Tree,
			// EnvDigestOf, not w.World.Env: a world recorded without an env
			// digest would otherwise yield a label with an EMPTY env identity,
			// and two scorings under different interpreters could then produce
			// the "same" label. The fallback digests the closed hidden
			// environment, which is the identity that actually applies here.
			EnvDigest:    EnvDigestOf(w.World),
			OracleDigest: s.Instance.OracleDigest,
		})
		b.Pre = append(b.Pre, l)
	}
	sort.Slice(b.Pre, func(i, j int) bool { return b.Pre[i].Candidate < b.Pre[j].Candidate })
	b.Labels = ApplyControls(b.Pre, b.Controls)
	for _, l := range b.Labels {
		b.TreeVerdict[l.WorldTree] = l.Verdict
	}
	for _, w := range v.Worlds {
		if verd, ok := b.TreeVerdict[w.World.Tree]; ok {
			b.WorldVerdict[w.Digest] = verd
		}
	}
	if !b.Controls.OK() {
		b.Skipped = SkipGoldFailsControl
		b.SkipDetail = b.Controls.Detail
	}
	b.ScoringMS = time.Since(start).Milliseconds()
	if err := s.AssertScratchDrained(); err != nil {
		return b, err
	}
	return b, nil
}

func (s *Scorer) scoreTree(tree string) (Observation, error) {
	dir, err := s.Reconstruct(tree)
	if err != nil {
		return Observation{}, err
	}
	// The reconstruction is deleted after the run: decision 3's step 4. The
	// report already lives in control-plane scratch, so nothing is lost.
	defer os.RemoveAll(dir)
	// Both assertions run BEFORE the candidate's code does, every time, on
	// every tree. They are cheap, and the alternative is a design document's
	// sentence: the M1f entry-point plugin vector was assumed closed by a
	// path-glob set and was not.
	if err := s.AssertHiddenOutsideTree(dir); err != nil {
		return Observation{}, err
	}
	if err := s.AssertHiddenUnreachableByRelativePath(dir); err != nil {
		return Observation{}, err
	}
	return s.RunHidden(dir)
}

// runControls scores the base tree unpatched (the negative control) and the
// base tree with gold applied (the positive control). Gold is applied to a
// FRESH reconstruction in the scorer's own tmpdir, so the positive control does
// not depend on gold having been raced — which is exactly the family-B case.
func (s *Scorer) runControls(baseTree string) (base, gold Observation, err error) {
	if baseTree == "" {
		return Observation{}, Observation{},
			fmt.Errorf("eval: instance %s: no base tree: the negative control has nothing to run on", s.Instance.ID)
	}
	baseDir, err := s.Reconstruct(baseTree)
	if err != nil {
		return Observation{}, Observation{}, err
	}
	base, err = s.RunHidden(baseDir)
	if err != nil {
		os.RemoveAll(baseDir)
		return Observation{}, Observation{}, err
	}
	os.RemoveAll(baseDir)

	if s.Hidden.GoldPatch == "" {
		// No gold: the positive control cannot run, and CheckControls
		// reports it as not run rather than as passed.
		return base, Observation{}, nil
	}
	goldDir, err := s.Reconstruct(baseTree)
	if err != nil {
		return base, Observation{}, err
	}
	defer os.RemoveAll(goldDir)
	if err := applyInPlace(goldDir, []byte(s.Hidden.GoldPatch)); err != nil {
		// A gold patch that will not apply is an INVALID INSTANCE, and it
		// is reported as a failed positive control rather than as a crash.
		return base, Observation{RunnerErr: "gold patch does not apply: " + err.Error()}, nil
	}
	gold, err = s.RunHidden(goldDir)
	if err != nil {
		return base, Observation{}, err
	}
	return base, gold, nil
}

// applyInPlace applies a patch to a plain directory with `git apply`. The
// directory is not a repository, so --unsafe-paths is required and -p1 is
// explicit: a patch that only applies under a guessed strip level is a patch
// whose provenance we do not know.
func applyInPlace(dir string, patch []byte) error {
	cmd := exec.Command("git", "apply", "--unsafe-paths", "-p1", "--directory=", "-")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(string(patch))
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

// AssertHiddenOutsideTree is the assertion decision 3's claim rests on, written
// as code rather than as prose: the hidden mount is not inside the
// reconstruction, and no hidden file name appears in the reconstruction.
//
// It is called by the scorer's own tests and by cmd/mvo-eval before it writes
// a single label, because the entry-point plugin vector taught us that the
// difference between an unreachable path and an asserted-unreachable path is a
// whole block of false confidence.
func (s *Scorer) AssertHiddenOutsideTree(reconDir string) error {
	hd, err := filepath.Abs(s.hiddenDir)
	if err != nil {
		return err
	}
	rd, err := filepath.Abs(reconDir)
	if err != nil {
		return err
	}
	if hd == rd || strings.HasPrefix(hd, rd+string(os.PathSeparator)) {
		return fmt.Errorf("eval: the hidden suite %s is inside the reconstructed tree %s", hd, rd)
	}
	names := map[string]bool{}
	for _, p := range s.Hidden.HiddenPaths() {
		names[filepath.Base(p)] = true
	}
	var found []string
	err = filepath.WalkDir(rd, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a directory we cannot read is reported by the caller's walk
		}
		if names[filepath.Base(p)] {
			rel, _ := filepath.Rel(rd, p)
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("eval: walk %s: %w", rd, err)
	}
	if len(found) > 0 {
		sort.Strings(found)
		return fmt.Errorf("eval: %s: hidden suite file name(s) present in the reconstructed tree: %s",
			SkipLeakDetected, strings.Join(found, " "))
	}
	return nil
}

// RelativeGuesses are the relative paths a candidate would try in order to reach
// the suite that is judging it, expressed relative to the reconstruction root.
// The list is deliberately the same shape as
// testdata/toyrepo/patches-reach's, because that fixture is the precedent: the
// difference between an unreachable path and an ASSERTED-unreachable path is a
// whole block of false confidence.
func RelativeGuesses() []string {
	var out []string
	for _, up := range []string{".", "..", "../..", "../../..", "../../../.."} {
		for _, name := range []string{
			"hidden", "mvo-hidden", "oracle", "eval",
			HiddenDirPrefix, "corpus",
		} {
			out = append(out, filepath.Join(up, name))
		}
		out = append(out, up)
	}
	out = append(out, InWorldHidden, "/mvo/hidden", "/mvo/oracle")
	return out
}

// AssertHiddenUnreachableByRelativePath walks the guesses a candidate would try
// from inside a reconstruction and refuses if any of them resolves to a
// directory holding a hidden suite file.
//
// It is the assertion whose absence let M2a decision 13 read as unreachability
// for a whole block. It does NOT claim absolute-path unreachability at T0 —
// nothing at T0 can — and Residuals says so.
func (s *Scorer) AssertHiddenUnreachableByRelativePath(reconDir string) error {
	names := map[string]bool{}
	for _, p := range s.Hidden.HiddenPaths() {
		names[filepath.Base(p)] = true
	}
	var hits []string
	for _, guess := range RelativeGuesses() {
		dir := guess
		if !filepath.IsAbs(guess) {
			dir = filepath.Join(reconDir, guess)
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if names[e.Name()] {
				hits = append(hits, filepath.Join(guess, e.Name()))
			}
		}
	}
	if len(hits) > 0 {
		sort.Strings(hits)
		return fmt.Errorf("eval: %s: the hidden suite is reachable from the reconstruction by "+
			"relative path(s): %s — a candidate's module code runs during its own labelling, "+
			"so this is a read, not a theory", SkipLeakDetected, strings.Join(hits, " "))
	}
	return nil
}

// EnvDigestOf is the env identity a label records. It is the world's own env
// digest when there is one and a digest of the hidden environment otherwise,
// so two scorings under different interpreters cannot silently produce the
// "same" label.
func EnvDigestOf(w object.World) string {
	if w.Env != "" {
		return w.Env
	}
	return object.CASKeyBytes([]byte(strings.Join(HiddenEnv(), "\n")))
}
