package eval

// THE ARMS RUNNER: RACING A FIXED CANDIDATE SET (decision 2's run-time half).
//
// MODULE-LAYOUT DELTA, NAMED. The design's module list assigns arms.go "the
// eval-plane derived selector arms + their evidence footprints" and leaves the
// RACED arms' exec surface implicit. It is here, in one file, because decision
// 2's run-time half is three properties that must be auditable together:
//
//	1. `mvo` is exec'd with every MVO_EVAL_* variable REMOVED — not
//	   overwritten with "", removed, so os.Getenv reports absence.
//	2. its cwd is outside the eval home, and no path under the eval home
//	   appears anywhere in its argv.
//	3. it is handed the PUBLIC PROJECTION and nothing else: patch bytes plus
//	   ordinals, written into the workspace, with no source tag, no node id,
//	   no oracle digest and no canary.
//
// All three are RECORDED in the result and checked by the caller against the
// needle set, so the non-consultation proof is assembled from measurements
// rather than from this comment.
//
// ZERO AGENT SPEND IS ENFORCED, NOT PROMISED. Every race is `--agent script`
// over patch bytes we already have, and the runner front-loads PATH with
// poisoned stubs for every agent CLI the adapters know about — the same
// mechanism scripts/adversarial.sh uses. A stub firing is a loud, free bug
// instead of a silent, billed one.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AgentBinaries are the CLIs the poisoned stubs shadow. The list is the
// adapters' own, and a name added to an adapter without being added here is a
// gap — which is why the runner also asserts that no stub was executed.
func AgentBinaries() []string {
	return []string{"claude", "codex", "gpt5", "gemini", "cursor", "aider"}
}

// RunSpec is one raced arm on one instance.
type RunSpec struct {
	Arm Arm
	// MVO is the path to the racing binary. The runner never builds it and
	// never looks it up on PATH: an eval run must be able to say which
	// binary produced its numbers.
	MVO string
	// Instance is the eval-plane record; Handoff is what the racer gets.
	Instance Instance
	// Patches maps handoff file name -> patch bytes, already fetched out of
	// the eval home by the caller. The runner does not read the eval home:
	// that is what keeps its argv clean by construction.
	Patches map[string][]byte
	// WorkRoot must be outside the eval home. The runner refuses otherwise.
	WorkRoot string
	EvalHome string
	// RepoSrc is the directory whose CONTENTS become the workspace. The
	// caller copies out of the eval home; the runner never resolves an
	// eval-home path into a race.
	RepoSrc  string
	BudgetMS int64
	// PolicyFile installs and pins a policy; empty keeps the workspace
	// default `mvo init` writes.
	PolicyFile string
	Parallel   int
	ExtraFlags []string
	// Needles is used ONLY to check the argv and env the runner built. It is
	// never passed to the child.
	Needles Needles
	// Env overrides the ambient environment (tests supply a closed one).
	Env []string
}

// RunResult records what the runner did, in enough detail to prove what it did
// not do.
type RunResult struct {
	Workspace string     `json:"workspace"`
	Intent    string     `json:"intent"`
	Argv      [][]string `json:"argv"`
	ChildEnv  []string   `json:"child_env"`
	CWD       string     `json:"cwd"`
	// The three measured properties. They are computed here and consumed by
	// NonConsultation.Prove.
	EnvScrubbed        bool `json:"env_scrubbed"`
	EnvClean           bool `json:"env_clean"`
	ArgvClean          bool `json:"argv_clean"`
	CWDOutsideEvalHome bool `json:"cwd_outside_eval_home"`
	// StubsFired names any poisoned agent stub that was executed. Nonempty
	// means a code path tried to spawn a real agent CLI, which is a bug and
	// is reported as one.
	StubsFired []string `json:"stubs_fired"`
	StartedAt  string   `json:"started_at"`
	// FinishedAt is when the last racer process exited, and FinishedAtUnixMS is
	// the same instant as a number. The number exists because the ordering
	// conjunct in the non-consultation witness used to be `FinishedAt != ""`,
	// which is a constant true — this field is set on both the error and the
	// success path before Race returns — so one of nine conjuncts behind
	// "PROVED" measured nothing. A clock corroborates rather than proves; the
	// SEAL is what proves the ordering, and this makes the corroboration real.
	FinishedAt       string `json:"finished_at"`
	FinishedAtUnixMS int64  `json:"finished_at_unix_ms"`
	WallMS           int64  `json:"wall_ms"`
	Output           string `json:"output"`
}

// markFinished records the instant the last racer process exited, in both
// renderings, so the ordering conjunct has something to compare.
func (r *RunResult) markFinished() {
	now := time.Now()
	r.FinishedAt = now.UTC().Format(time.RFC3339Nano)
	r.FinishedAtUnixMS = now.UnixMilli()
}

// Race runs one arm over the fixed candidate set and returns the result plus
// the sealed ledger view.
//
// It is deliberately sequential and deliberately dumb: every choice that could
// differ between arms (the workspace, the intent, the budget, the dispatch
// degree) is made HERE, once, from the spec, so two arms cannot differ in
// anything the caller did not set.
func Race(spec RunSpec) (RunResult, LedgerView, error) {
	res := RunResult{StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	start := time.Now()
	if spec.MVO == "" {
		return res, LedgerView{}, fmt.Errorf("eval: no mvo binary given: an eval run must name the binary that produced its numbers")
	}
	if len(spec.Arm.RaceFlags) == 0 {
		return res, LedgerView{}, fmt.Errorf("eval: arm %s declares no race flags: refusing to race an arm whose treatment is unspecified", spec.Arm.ID)
	}
	if spec.WorkRoot == "" {
		return res, LedgerView{}, fmt.Errorf("eval: no work root")
	}
	if err := outsideEvalHome(spec.WorkRoot, spec.EvalHome); err != nil {
		return res, LedgerView{}, err
	}
	ws := filepath.Join(spec.WorkRoot, "ws")
	if err := copyTree(spec.RepoSrc, ws); err != nil {
		return res, LedgerView{}, err
	}
	if err := gitSeed(ws); err != nil {
		return res, LedgerView{}, err
	}

	// The handoff: patch bytes plus ordinals, and the public projection as a
	// file the racer may read. Nothing else lands in the workspace.
	handoffDir := filepath.Join(ws, ".mvo-eval-handoff")
	if err := os.MkdirAll(handoffDir, 0o755); err != nil {
		return res, LedgerView{}, fmt.Errorf("eval: create handoff dir: %w", err)
	}
	names := make([]string, 0, len(spec.Patches))
	for n := range spec.Patches {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(handoffDir, n), spec.Patches[n], 0o644); err != nil {
			return res, LedgerView{}, fmt.Errorf("eval: write handoff patch %s: %w", n, err)
		}
	}

	// The poisoned stubs, and the env scrub.
	shimDir := filepath.Join(spec.WorkRoot, "no-agents")
	firedDir := filepath.Join(spec.WorkRoot, "stubs-fired")
	if err := writeAgentStubs(shimDir, firedDir); err != nil {
		return res, LedgerView{}, err
	}
	env := ScrubEnv(spec.Env, shimDir)
	res.ChildEnv = env
	res.EnvScrubbed = envScrubbed(env)
	res.EnvClean = !containsNeedle(spec.Needles, env)
	res.CWD = ws
	res.CWDOutsideEvalHome = outsideEvalHome(ws, spec.EvalHome) == nil
	res.Workspace = ws

	var out strings.Builder
	run := func(args ...string) error {
		res.Argv = append(res.Argv, append([]string{spec.MVO}, args...))
		cmd := exec.Command(spec.MVO, args...)
		cmd.Dir = ws
		cmd.Env = env
		b, err := cmd.CombinedOutput()
		out.Write(b)
		if err != nil {
			return fmt.Errorf("eval: %s %s: %w: %s", filepath.Base(spec.MVO),
				strings.Join(args, " "), err, strings.TrimSpace(string(b)))
		}
		return nil
	}

	if err := run("init", "--dir", ws); err != nil {
		res.Output = out.String()
		return res, LedgerView{}, err
	}
	if spec.PolicyFile != "" {
		// THERE IS NO `mvo policy install`. The verbs are list, show, validate,
		// use, and a policy is installed by placing the file in the workspace's
		// own policies dir and pinning it by NAME — `policy use` requires the
		// document's `name` to equal its filename stem, so the basename is
		// carried through unchanged. This function used to shell out to
		// `policy install`, which exits 2 on every call, so EVERY instance
		// carrying a PolicyHint died at pre-flight and was filed as a named
		// skip. It degraded cleanly, which is exactly why it survived: the
		// harness stayed green while two of five instances silently left the
		// corpus.
		name := strings.TrimSuffix(filepath.Base(spec.PolicyFile), ".json")
		b, err := os.ReadFile(spec.PolicyFile)
		if err != nil {
			res.Output = out.String()
			return res, LedgerView{}, fmt.Errorf("eval: read policy %s: %w", spec.PolicyFile, err)
		}
		polDir := filepath.Join(ws, ".multiverso", "policies")
		if err := os.MkdirAll(polDir, 0o755); err != nil {
			res.Output = out.String()
			return res, LedgerView{}, fmt.Errorf("eval: create policies dir: %w", err)
		}
		if err := os.WriteFile(filepath.Join(polDir, name+".json"), b, 0o644); err != nil {
			res.Output = out.String()
			return res, LedgerView{}, fmt.Errorf("eval: write policy %s: %w", name, err)
		}
		if err := run("policy", "use", name, "--dir", ws); err != nil {
			res.Output = out.String()
			return res, LedgerView{}, err
		}
	}
	// The intent carries the task text and the budget. It is created BEFORE
	// any arm-specific flag, so both arms consume the same object (M2b.1 F4).
	// The budget lives on the INTENT (that is where max_oracle_ms is), and it
	// is passed ALWAYS, including as 0: `mvo intent new` defines 0 as
	// unbounded, so the reference arm's null case and a budgeted arm's
	// binding case differ in one number and in nothing else. Omitting the
	// flag instead of passing 0 would make the two arms differ in the shape
	// of their argv, which is the kind of difference a comparison must not
	// have (M2b.1 F4: both arms consume the same object).
	intentArgs := []string{"intent", "new", "--dir", ws,
		"--title", "eval:" + spec.Instance.ID, "--desc", spec.Instance.Task,
		"--budget-oracle-ms", strconv.FormatInt(spec.BudgetMS, 10),
		"--budget-candidates", strconv.Itoa(len(spec.Patches)),
	}
	res.Argv = append(res.Argv, append([]string{spec.MVO}, intentArgs...))
	cmd := exec.Command(spec.MVO, intentArgs...)
	cmd.Dir = ws
	cmd.Env = env
	b, err := cmd.Output()
	out.Write(b)
	if err != nil {
		res.Output = out.String()
		return res, LedgerView{}, fmt.Errorf("eval: mvo intent new: %w", err)
	}
	res.Intent = strings.TrimSpace(string(b))
	if res.Intent == "" {
		res.Output = out.String()
		return res, LedgerView{}, fmt.Errorf("eval: mvo intent new printed no digest")
	}

	raceArgs := []string{"race", res.Intent, "--dir", ws, "--agent", "script", "--patches", handoffDir}
	raceArgs = append(raceArgs, spec.Arm.RaceFlags...)
	if spec.Parallel > 0 {
		raceArgs = append(raceArgs, "--parallel", strconv.Itoa(spec.Parallel))
	}
	raceArgs = append(raceArgs, spec.ExtraFlags...)
	if err := run(raceArgs...); err != nil {
		// A race that ends in REJECT is not an error, but a race that
		// cannot run is. mvo distinguishes them by exit code; anything
		// non-zero here is reported and the ledger is still read, because a
		// PREFLIGHT_ABORT leaves a readable ledger and a named refusal.
		res.Output = out.String()
		res.ArgvClean = argvClean(spec.Needles, res.Argv, spec.EvalHome)
		res.StubsFired = stubsFired(firedDir)
		res.markFinished()
		res.WallMS = time.Since(start).Milliseconds()
		view, verr := ReadLedger(ws)
		if verr != nil {
			return res, LedgerView{}, err
		}
		return res, view, err
	}
	res.Output = out.String()
	res.ArgvClean = argvClean(spec.Needles, res.Argv, spec.EvalHome)
	res.StubsFired = stubsFired(firedDir)
	res.markFinished()
	res.WallMS = time.Since(start).Milliseconds()

	view, err := ReadLedger(ws)
	if err != nil {
		return res, LedgerView{}, err
	}
	return res, view, nil
}

// HandoffPatches reads the candidate patch bytes OUT of the eval home and keys
// them by their handoff file name. This is the seam that keeps every eval-home
// path out of the racer's argv: the bytes cross the boundary, the paths do not.
func (s *Store) HandoffPatches(inst Instance) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, hc := range inst.Project().Candidates {
		var key string
		for _, c := range inst.Candidates {
			if c.Ord == hc.Ord {
				key = c.Patch
			}
		}
		b, err := s.Blob(inst.Corpus, inst.Version, key)
		if err != nil {
			return nil, err
		}
		out[hc.File] = b
	}
	return out, nil
}

// ScrubEnv builds the child environment: every MVO_EVAL_* variable REMOVED
// (not emptied), PATH front-loaded with the poisoned stubs, and the git
// identity pinned so a race never depends on a developer's gitconfig.
//
// Removal rather than overwriting is the whole point. An empty MVO_EVAL_HOME
// would make os.Getenv return "", which a code path can misread as "set to the
// current directory"; absence is unambiguous.
func ScrubEnv(base []string, shimDir string) []string {
	if base == nil {
		base = os.Environ()
	}
	out := make([]string, 0, len(base)+8)
	for _, kv := range base {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if strings.HasPrefix(name, EnvPrefix) {
			continue
		}
		if name == "PATH" {
			continue
		}
		out = append(out, kv)
	}
	path := ""
	for _, kv := range base {
		if strings.HasPrefix(kv, "PATH=") {
			path = strings.TrimPrefix(kv, "PATH=")
		}
	}
	if shimDir != "" {
		if path == "" {
			path = shimDir
		} else {
			path = shimDir + string(os.PathListSeparator) + path
		}
	}
	out = append(out,
		"PATH="+path,
		"GIT_AUTHOR_NAME=mvo-eval", "GIT_AUTHOR_EMAIL=eval@example.invalid",
		"GIT_COMMITTER_NAME=mvo-eval", "GIT_COMMITTER_EMAIL=eval@example.invalid",
	)
	sort.Strings(out)
	return out
}

func envScrubbed(env []string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, EnvPrefix) {
			return false
		}
	}
	return true
}

func containsNeedle(n Needles, hay []string) bool {
	ns := n.textNeedles()
	for _, kv := range hay {
		for _, nd := range ns {
			if nd.value != "" && strings.Contains(kv, nd.value) {
				return true
			}
		}
	}
	return false
}

// argvClean checks every argv the runner built for a needle or an eval-home
// path. It is checked on the argv the runner ACTUALLY used, recorded above, and
// not on a plan.
func argvClean(n Needles, argvs [][]string, home string) bool {
	var flat []string
	for _, a := range argvs {
		flat = append(flat, a...)
	}
	if containsNeedle(n, flat) {
		return false
	}
	if home == "" {
		return true
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		abs = home
	}
	for _, s := range flat {
		if strings.Contains(s, abs) {
			return false
		}
	}
	return true
}

func outsideEvalHome(path, home string) error {
	if home == "" {
		return nil
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("eval: resolve %s: %w", path, err)
	}
	h, err := filepath.Abs(home)
	if err != nil {
		return fmt.Errorf("eval: resolve %s: %w", home, err)
	}
	if p == h || strings.HasPrefix(p, h+string(os.PathSeparator)) {
		return fmt.Errorf("eval: %s is inside the eval home %s: a race must never run there", path, home)
	}
	return nil
}

// writeAgentStubs installs the poisoned stubs. Each one TOUCHES A FILE before
// refusing, so "no stub fired" is a measurement rather than an absence of
// evidence.
func writeAgentStubs(shimDir, firedDir string) error {
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		return fmt.Errorf("eval: create shim dir: %w", err)
	}
	if err := os.MkdirAll(firedDir, 0o755); err != nil {
		return fmt.Errorf("eval: create stub-fired dir: %w", err)
	}
	for _, bin := range AgentBinaries() {
		script := "#!/bin/sh\n" +
			": > \"" + filepath.Join(firedDir, bin) + "\"\n" +
			"echo \"mvo-eval: refusing to invoke a real agent CLI ($0)\" >&2\n" +
			"exit 97\n"
		p := filepath.Join(shimDir, bin)
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			return fmt.Errorf("eval: write stub %s: %w", p, err)
		}
		if err := os.Chmod(p, 0o755); err != nil {
			return fmt.Errorf("eval: chmod stub %s: %w", p, err)
		}
	}
	return nil
}

func stubsFired(firedDir string) []string {
	ents, err := os.ReadDir(firedDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// copyTree copies src's contents into dst, skipping .git (the workspace gets a
// fresh history so a race never inherits one) and __pycache__ (stale bytecode
// is a real source of cross-run contamination).
func copyTree(src, dst string) error {
	if src == "" {
		return fmt.Errorf("eval: no source tree to copy")
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("eval: create %s: %w", dst, err)
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("eval: read %s: %w", src, err)
	}
	for _, e := range ents {
		name := e.Name()
		if name == ".git" || name == "__pycache__" || name == ".multiverso" {
			continue
		}
		s := filepath.Join(src, name)
		d := filepath.Join(dst, name)
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			return fmt.Errorf("eval: stat %s: %w", s, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(s)
		if err != nil {
			return fmt.Errorf("eval: read %s: %w", s, err)
		}
		if err := os.WriteFile(d, b, info.Mode().Perm()); err != nil {
			return fmt.Errorf("eval: write %s: %w", d, err)
		}
	}
	return nil
}

// gitSeed makes the copied tree a git repository with one commit. The identity
// is pinned in the environment so the commit does not depend on a developer's
// gitconfig, and the branch is named explicitly so it does not depend on their
// init.defaultBranch either.
func gitSeed(dir string) error {
	steps := [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"commit", "-qm", "eval baseline"},
	}
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=mvo-eval", "GIT_AUTHOR_EMAIL=eval@example.invalid",
			"GIT_COMMITTER_NAME=mvo-eval", "GIT_COMMITTER_EMAIL=eval@example.invalid",
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+dir,
		)
		if b, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("eval: git %s in %s: %w: %s",
				strings.Join(args, " "), dir, err, strings.TrimSpace(string(b)))
		}
	}
	return nil
}
