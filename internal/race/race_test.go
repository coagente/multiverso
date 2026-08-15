package race

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/agent"
	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{
		"-c", "user.name=mvo-test",
		"-c", "user.email=mvo-test@invalid",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// initRepo creates a repo whose x.txt says "broken".
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

const fixPatch = `--- a/x.txt
+++ b/x.txt
@@ -1 +1 @@
-broken
+fixed
`

const noFixPatch = `--- a/x.txt
+++ b/x.txt
@@ -1 +1 @@
-broken
+still-broken
`

const bogusPatch = "this is not a patch\n"

// stubOracle passes iff the world's x.txt says "fixed".
type stubOracle struct{}

func (stubOracle) ID() string      { return "stub" }
func (stubOracle) Version() string { return "v0" }

func (stubOracle) Run(_ context.Context, w backend.World) (object.Receipt, error) {
	b, err := os.ReadFile(filepath.Join(w.Dir(), "x.txt"))
	if err != nil {
		return object.Receipt{}, err
	}
	status := "fail"
	if strings.TrimSpace(string(b)) == "fixed" {
		status = "pass"
	}
	return object.Receipt{
		Schema:      object.SchemaReceipt,
		Oracle:      object.OracleRef{ID: "stub", Version: "v0", Config: "mv0:cfg"},
		Execution:   object.Execution{Argv: []string{"stub"}, IsolationTier: w.Tier(), IsolationCaps: w.Caps()},
		Result:      object.Result{Status: status, Artifacts: []string{}},
		Freshness:   object.Freshness{Basis: "construction"},
		RecheckTier: "V1-replayable",
		Family:      "suite",
		Cost:        object.Cost{WallMS: 1},
		CreatedAt:   fixedTime,
	}, nil
}

// seedIntent stores a default policy and an intent for repo's HEAD in the
// CAS and returns the intent digest.
func seedIntent(t *testing.T, store *cas.Store, repo string, maxCandidates int) string {
	t.Helper()
	commit, tree, err := gitx.Head(repo)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	polDig, polCanon, err := object.Digest(object.Policy{
		Schema: object.SchemaPolicy, HardGates: []string{GateSuitePass},
		Ranking: []string{"gate_pass", "wall_ms_asc"},
	})
	if err != nil {
		t.Fatalf("digest policy: %v", err)
	}
	if _, err := store.Put(polCanon); err != nil {
		t.Fatalf("store policy: %v", err)
	}
	intent := object.Intent{
		Schema:    object.SchemaIntent,
		Base:      object.Base{Commit: commit, Tree: tree},
		Spec:      object.Spec{Title: "fix x", Description: "make x.txt say fixed"},
		Budget:    object.Budget{MaxCandidates: maxCandidates, MaxWallMS: 600000},
		Policy:    polDig,
		CreatedAt: fixedTime,
	}
	intentDig, intentCanon, err := object.Digest(intent)
	if err != nil {
		t.Fatalf("digest intent: %v", err)
	}
	if _, err := store.Put(intentCanon); err != nil {
		t.Fatalf("store intent: %v", err)
	}
	return intentDig
}

// scriptCands builds script candidates (prompt = patch bytes, decision 7)
// in sorted name order, mirroring the CLI's patch-file ordering.
func scriptCands(patches map[string]string) []Candidate {
	names := make([]string, 0, len(patches))
	for name := range patches {
		names = append(names, name)
	}
	sort.Strings(names)
	cands := make([]Candidate, 0, len(names))
	for _, name := range names {
		cands = append(cands, Candidate{Prompt: patches[name]})
	}
	return cands
}

func mustAdapter(t *testing.T, name string) agent.Adapter {
	t.Helper()
	a, err := agent.New(name)
	if err != nil {
		t.Fatalf("agent.New(%s): %v", name, err)
	}
	return a
}

func newConfig(t *testing.T, patches map[string]string) Config {
	t.Helper()
	repo := initRepo(t)
	store, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	led, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	t.Cleanup(func() { led.Close() })
	return Config{
		Repo:         repo,
		Ledger:       led,
		CAS:          store,
		Intent:       seedIntent(t, store, repo, max(len(patches), 1)),
		Adapter:      mustAdapter(t, "script"),
		Candidates:   scriptCands(patches),
		WorldsDir:    filepath.Join(t.TempDir(), "worlds"),
		LegacyOracle: stubOracle{},
		Backend:      mustBackend(t),
		Parallel:     1,
	}
}

// mustBackend returns the default T0 backend.
func mustBackend(t *testing.T) backend.Backend {
	t.Helper()
	b, err := backend.New(backend.Config{})
	if err != nil {
		t.Fatalf("backend.New: %v", err)
	}
	return b
}

func TestRunSelectsWinnerAndRecordsEverything(t *testing.T) {
	cfg := newConfig(t, map[string]string{
		"a-fix.patch":   fixPatch,   // applies, suite passes
		"b-nofix.patch": noFixPatch, // applies, suite fails
		"c-bogus.patch": bogusPatch, // does not apply → CONFIG_ERROR
	})
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Decision.Type != TypeSelect {
		t.Fatalf("decision type = %q, want SELECT (rationale: %s)", res.Decision.Type, res.Decision.Rationale)
	}
	if len(res.Worlds) != 3 {
		t.Fatalf("worlds = %d, want 3", len(res.Worlds))
	}
	// Candidates run in order (CP-2): a-fix, b-nofix, c-bogus.
	wantOutcomes := []string{OutcomeCompleted, OutcomeCompleted, OutcomeConfigError}
	for i, wr := range res.Worlds {
		if wr.Ordinal != i+1 {
			t.Errorf("world %d ordinal = %d, want %d", i, wr.Ordinal, i+1)
		}
		if wr.World.Outcome != wantOutcomes[i] {
			t.Errorf("world %d outcome = %q, want %q", i, wr.World.Outcome, wantOutcomes[i])
		}
	}
	if got, want := res.Decision.Subject[0], res.Worlds[0].Digest; got != want {
		t.Errorf("winner = %q, want a-fix world %q", got, want)
	}
	if len(res.Decision.Subject) != 3 {
		t.Errorf("subject = %v, want all 3 worlds", res.Decision.Subject)
	}
	if len(res.Decision.Evidence) != 2 {
		t.Errorf("evidence = %v, want 2 receipts (none for CONFIG_ERROR)", res.Decision.Evidence)
	}
	if res.Worlds[2].Receipt != nil {
		t.Error("CONFIG_ERROR world has a receipt; want none")
	}
	if res.Decision.CreatedAt == "" {
		t.Error("recorded decision missing created_at")
	}

	// M1b world fields: context = CAS key of the prompt (= patch) bytes,
	// trace = the empty transcript, cost source honest.
	for i, wr := range res.Worlds {
		wantCtx, err := cfg.CAS.Put([]byte(cfg.Candidates[i].Prompt))
		if err != nil {
			t.Fatal(err)
		}
		if wr.World.Context != wantCtx {
			t.Errorf("world %d context = %q, want prompt CAS key %q", i, wr.World.Context, wantCtx)
		}
		emptyKey, err := cfg.CAS.Put([]byte{})
		if err != nil {
			t.Fatal(err)
		}
		if wr.World.Trace != emptyKey {
			t.Errorf("world %d trace = %q, want empty-bytes key %q", i, wr.World.Trace, emptyKey)
		}
		if wr.World.Cost.Source != agent.CostSourceNone {
			t.Errorf("world %d cost source = %q, want %q", i, wr.World.Cost.Source, agent.CostSourceNone)
		}
		if wr.World.Producer.Adapter != "script@v0" {
			t.Errorf("world %d producer adapter = %q, want script@v0", i, wr.World.Producer.Adapter)
		}
		if wr.World.Patch == "" {
			t.Errorf("world %d has empty patch key", i)
		}
	}
	// The CONFIG_ERROR world's captured diff is empty (apply is atomic),
	// while the applied worlds carry a real captured diff.
	if b, err := cfg.CAS.Get(res.Worlds[2].World.Patch); err != nil || len(b) != 0 {
		t.Errorf("CONFIG_ERROR world patch = %d bytes, err %v; want empty", len(b), err)
	}
	if b, err := cfg.CAS.Get(res.Worlds[0].World.Patch); err != nil || !strings.Contains(string(b), "diff --git") {
		t.Errorf("winner captured patch missing 'diff --git' (err %v): %q", err, b)
	}

	// Worktrees are removed after the race.
	for _, wr := range res.Worlds {
		if _, err := os.Stat(wr.Dir); !os.IsNotExist(err) {
			t.Errorf("world dir %s still present: stat err = %v", wr.Dir, err)
		}
	}

	// The ledger tells the whole story, in order, with an intact chain:
	// per world agent.started → agent.finished → world.created.
	var types []string
	if err := cfg.Ledger.Scan(func(e ledger.Event) error {
		types = append(types, e.Type)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	wantTypes := []string{
		"race.started",
		"agent.started", "agent.finished", "world.created",
		"agent.started", "agent.finished", "world.created",
		"agent.started", "agent.finished", "world.created",
		"receipt.recorded", "receipt.recorded",
		"decision.recorded",
		"race.finished",
	}
	if !reflect.DeepEqual(types, wantTypes) {
		t.Errorf("event types = %v, want %v", types, wantTypes)
	}
	if err := cfg.Ledger.VerifyChain(); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}

	// race.started carries the adapter and candidate count (M1b body).
	if err := cfg.Ledger.Scan(func(e ledger.Event) error {
		if e.Type != "race.started" {
			return nil
		}
		var body struct {
			Adapter    string `json:"adapter"`
			Candidates int    `json:"candidates"`
			Intent     string `json:"intent"`
		}
		if err := json.Unmarshal(e.Payload, &body); err != nil {
			return err
		}
		if body.Adapter != "script@v0" || body.Candidates != 3 || body.Intent != cfg.Intent {
			t.Errorf("race.started body = %+v", body)
		}
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Every recorded object is retrievable from CAS by its digest.
	var d object.Decision
	if err := loadObject(cfg.CAS, res.DecisionDigest, &d); err != nil {
		t.Fatalf("load recorded decision: %v", err)
	}
	if !reflect.DeepEqual(d, res.Decision) {
		t.Errorf("decision in CAS = %+v, want %+v", d, res.Decision)
	}
}

// NFR-1: recomputing the decision from the recorded worlds and receipts
// reproduces Type, Subject, Evidence, Rationale byte-for-byte.
func TestRunDecisionReplaysFromLedger(t *testing.T) {
	cfg := newConfig(t, map[string]string{
		"a-fix.patch":   fixPatch,
		"b-nofix.patch": noFixPatch,
	})
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Replay inputs carry the digests the LEDGER recorded them under, never
	// a re-serialization of the decoded structs (M1e decision 1).
	var worlds []object.RecordedWorld
	var receipts []object.RecordedReceipt
	var recorded object.Decision
	if err := cfg.Ledger.Scan(func(e ledger.Event) error {
		switch e.Type {
		case "world.created":
			var w object.World
			if err := json.Unmarshal(e.Payload, &w); err != nil {
				return err
			}
			worlds = append(worlds, object.RecordedWorld{Digest: e.PayloadDig, World: w})
		case "receipt.recorded":
			var r object.Receipt
			if err := json.Unmarshal(e.Payload, &r); err != nil {
				return err
			}
			receipts = append(receipts, object.RecordedReceipt{Digest: e.PayloadDig, Receipt: r})
		case "decision.recorded":
			if err := json.Unmarshal(e.Payload, &recorded); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	pol, err := policy.Load(cfg.CAS, recorded.Policy)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}

	replayed := Decide(pol, worlds, receipts)
	if replayed.Type != recorded.Type ||
		!reflect.DeepEqual(replayed.Subject, recorded.Subject) ||
		!reflect.DeepEqual(replayed.Evidence, recorded.Evidence) ||
		replayed.Rationale != recorded.Rationale {
		t.Errorf("replayed decision differs from recorded:\nreplayed: %+v\nrecorded: %+v", replayed, recorded)
	}
	if recorded.Type != TypeSelect || recorded.Subject[0] != res.Worlds[0].Digest {
		t.Errorf("recorded decision = %+v, want SELECT of a-fix world", recorded)
	}
}

// CP-2: a race with more candidates than the intent budget allows is
// refused before race.started — nothing lands in the ledger.
func TestRunMaxCandidatesEnforced(t *testing.T) {
	cfg := newConfig(t, map[string]string{
		"a-fix.patch":   fixPatch,
		"b-nofix.patch": noFixPatch,
	})
	cfg.Candidates = append(cfg.Candidates, Candidate{Prompt: bogusPatch}) // 3 > MaxCandidates 2
	_, err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "max_candidates") {
		t.Fatalf("Run over budget: err = %v, want max_candidates error", err)
	}
	events := 0
	if err := cfg.Ledger.Scan(func(ledger.Event) error { events++; return nil }); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if events != 0 {
		t.Errorf("over-budget race appended %d events; want 0 (refused before race.started)", events)
	}
}

// stubRun is a pre-finished agent.Run for stub adapters.
type stubRun struct {
	res    *agent.RunResult
	events chan agent.Event
}

func newStubRun(res *agent.RunResult) *stubRun {
	ch := make(chan agent.Event)
	close(ch)
	return &stubRun{res: res, events: ch}
}

func (r *stubRun) Events() <-chan agent.Event      { return r.events }
func (r *stubRun) Interrupt()                      {}
func (r *stubRun) Wait() (*agent.RunResult, error) { return r.res, nil }

// mixedAdapter crashes candidates whose prompt is "crash" and completes
// the rest by fixing x.txt in the world.
type mixedAdapter struct{}

func (mixedAdapter) ID() string      { return "stub" }
func (mixedAdapter) Version() string { return "v0" }

func (mixedAdapter) Start(_ context.Context, spec agent.RunSpec) (agent.Run, error) {
	if spec.Prompt == "crash" {
		return newStubRun(&agent.RunResult{
			Outcome:    object.OutcomeCrash,
			ExitCode:   3,
			Cost:       object.RunCost{WallMS: 1, Source: agent.CostSourceNone},
			Transcript: []byte("boom\n"),
			Stderr:     []byte{},
		}), nil
	}
	if err := os.WriteFile(filepath.Join(spec.WorldDir, "x.txt"), []byte("fixed\n"), 0o644); err != nil {
		return nil, err
	}
	return newStubRun(&agent.RunResult{
		Outcome:    object.OutcomeCompleted,
		Cost:       object.RunCost{WallMS: 1, Source: agent.CostSourceNone},
		Transcript: []byte("ok\n"),
		Stderr:     []byte{},
	}), nil
}

// A mixed race — one COMPLETED world, one CRASH world — decides correctly
// with the crash recorded as evidence: the crashed world gets no oracle
// run, appears in the subject, and its outcome survives in the ledger.
func TestRunMixedCompletedAndCrash(t *testing.T) {
	cfg := newConfig(t, map[string]string{"unused-a": "x", "unused-b": "y"})
	cfg.Adapter = mixedAdapter{}
	cfg.Candidates = []Candidate{{Prompt: "fix it"}, {Prompt: "crash"}}
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision.Type != TypeSelect {
		t.Fatalf("decision = %q (%s), want SELECT", res.Decision.Type, res.Decision.Rationale)
	}
	if got := res.Worlds[0].World.Outcome; got != object.OutcomeCompleted {
		t.Errorf("world 1 outcome = %q, want COMPLETED", got)
	}
	if got := res.Worlds[1].World.Outcome; got != object.OutcomeCrash {
		t.Errorf("world 2 outcome = %q, want CRASH", got)
	}
	if res.Worlds[1].Receipt != nil {
		t.Error("CRASH world has a receipt; want none (no oracle run)")
	}
	if res.Decision.Subject[0] != res.Worlds[0].Digest {
		t.Errorf("winner = %q, want the COMPLETED world %q", res.Decision.Subject[0], res.Worlds[0].Digest)
	}
	if len(res.Decision.Subject) != 2 {
		t.Errorf("subject = %v, want both worlds", res.Decision.Subject)
	}
	// The crashed world's transcript is in CAS under the recorded trace.
	if b, err := cfg.CAS.Get(res.Worlds[1].World.Trace); err != nil || string(b) != "boom\n" {
		t.Errorf("crash trace = %q, err %v; want %q", b, err, "boom\n")
	}
}

// hostileAdapter damages its own worktree per the prompt: "fifo" drops a
// FIFO at a lockfile name, "chmod000" leaves an unreadable file (breaks
// `git add -A`), "repoint:<gitdir>" replaces the worktree's `.git`
// pointer file, anything else fixes x.txt. It always reports a clean
// COMPLETED run — the damage is what the control plane must catch.
type hostileAdapter struct{}

func (hostileAdapter) ID() string      { return "stub" }
func (hostileAdapter) Version() string { return "v0" }

func (hostileAdapter) Start(_ context.Context, spec agent.RunSpec) (agent.Run, error) {
	switch {
	case spec.Prompt == "fifo":
		if err := syscall.Mkfifo(filepath.Join(spec.WorldDir, "requirements.txt"), 0o644); err != nil {
			return nil, err
		}
	case spec.Prompt == "chmod000":
		if err := os.WriteFile(filepath.Join(spec.WorldDir, "secret.bin"), []byte("x"), 0o000); err != nil {
			return nil, err
		}
	case strings.HasPrefix(spec.Prompt, "repoint:"):
		gitdir := "gitdir: " + strings.TrimPrefix(spec.Prompt, "repoint:") + "\n"
		if err := os.WriteFile(filepath.Join(spec.WorldDir, ".git"), []byte(gitdir), 0o644); err != nil {
			return nil, err
		}
	default:
		if err := os.WriteFile(filepath.Join(spec.WorldDir, "x.txt"), []byte("fixed\n"), 0o644); err != nil {
			return nil, err
		}
	}
	return newStubRun(&agent.RunResult{
		Outcome:    object.OutcomeCompleted,
		Cost:       object.RunCost{WallMS: 1, Source: agent.CostSourceNone},
		Transcript: []byte("ok\n"),
		Stderr:     []byte("agent stderr\n"),
	}), nil
}

// agentFinishedBodies returns the agent.finished payloads in ledger order.
func agentFinishedBodies(t *testing.T, led *ledger.Ledger) []map[string]any {
	t.Helper()
	var bodies []map[string]any
	if err := led.Scan(func(e ledger.Event) error {
		if e.Type != "agent.finished" {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal(e.Payload, &m); err != nil {
			return err
		}
		bodies = append(bodies, m)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return bodies
}

// assertHostileWorldRace asserts the shared invariants of a two-candidate
// race whose first world was sabotaged: the race completes with no error,
// the damage is CRASH evidence carrying note in its stderr artifact, the
// sibling wins SELECT, and every worktree is cleaned up.
func assertHostileWorldRace(t *testing.T, cfg Config, res *Result, note string) {
	t.Helper()
	if res.Decision.Type != TypeSelect {
		t.Fatalf("decision = %q (%s), want SELECT", res.Decision.Type, res.Decision.Rationale)
	}
	if got := res.Worlds[0].World.Outcome; got != object.OutcomeCrash {
		t.Errorf("hostile world outcome = %q, want CRASH", got)
	}
	if res.Worlds[0].Receipt != nil {
		t.Error("hostile world has a receipt; want none (no oracle run in a sabotaged worktree)")
	}
	if got := res.Worlds[1].World.Outcome; got != object.OutcomeCompleted {
		t.Errorf("sibling world outcome = %q, want COMPLETED", got)
	}
	if res.Decision.Subject[0] != res.Worlds[1].Digest {
		t.Errorf("winner = %q, want the sibling world %q", res.Decision.Subject[0], res.Worlds[1].Digest)
	}

	// The capture error is preserved as evidence in the stderr artifact;
	// agent.finished still records the RUN's own outcome (it completed —
	// the worktree it left behind is what could not serve as evidence).
	finished := agentFinishedBodies(t, cfg.Ledger)
	if len(finished) != 2 {
		t.Fatalf("agent.finished events = %d, want 2", len(finished))
	}
	if got := finished[0]["outcome"]; got != object.OutcomeCompleted {
		t.Errorf("agent.finished outcome = %v, want COMPLETED (the run's own record)", got)
	}
	stderrKey, _ := finished[0]["stderr"].(string)
	b, err := cfg.CAS.Get(stderrKey)
	if err != nil {
		t.Fatalf("get stderr artifact %q: %v", stderrKey, err)
	}
	if !strings.Contains(string(b), "agent stderr\n") || !strings.Contains(string(b), note) {
		t.Errorf("stderr artifact %q must keep the agent bytes and carry %q", b, note)
	}

	// Sabotage must not break cleanup either: evidence is recorded, so a
	// damaged worktree is removed (with fallback) rather than failing the
	// race.
	for _, wr := range res.Worlds {
		if _, err := os.Stat(wr.Dir); !os.IsNotExist(err) {
			t.Errorf("world dir %s still present: stat err = %v", wr.Dir, err)
		}
	}
	if err := cfg.Ledger.VerifyChain(); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}

// A hostile candidate that leaves a writer-less FIFO at a lockfile name
// must never hang the control plane: before hardening, EnvDigest's
// ReadFile would block forever (the agent is dead, no writer ever
// arrives). Git itself tolerates FIFOs (`add -A` skips unsupported file
// types), so the world completes with the FIFO honestly absent from the
// captured patch and tree, and the race decides normally.
func TestRunHostileFIFOWorldDoesNotHangRace(t *testing.T) {
	cfg := newConfig(t, map[string]string{"unused-a": "x", "unused-b": "y"})
	cfg.Adapter = hostileAdapter{}
	cfg.Candidates = []Candidate{{Prompt: "fifo"}, {Prompt: "fix"}}

	type out struct {
		res *Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := Run(context.Background(), cfg)
		done <- out{res, err}
	}()
	var got out
	select {
	case got = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("race hung — EnvDigest blocked on the hostile FIFO?")
	}
	if got.err != nil {
		t.Fatalf("Run: %v (a hostile worktree must not abort the race)", got.err)
	}
	if got.res.Decision.Type != TypeSelect {
		t.Fatalf("decision = %q (%s), want SELECT", got.res.Decision.Type, got.res.Decision.Rationale)
	}
	if got.res.Decision.Subject[0] != got.res.Worlds[1].Digest {
		t.Errorf("winner = %q, want the fixing world %q", got.res.Decision.Subject[0], got.res.Worlds[1].Digest)
	}
	for _, wr := range got.res.Worlds {
		if _, err := os.Stat(wr.Dir); !os.IsNotExist(err) {
			t.Errorf("world dir %s still present: stat err = %v", wr.Dir, err)
		}
	}
}

// A hostile candidate that leaves an unreadable file becomes CRASH
// evidence, not a race abort: `git add -A` cannot index it, so
// control-plane diff capture fails — agent-induced worktree damage is
// that world's evidence (decision 10) and the sibling still races to a
// SELECT.
func TestRunUnreadableFileWorldIsCrashEvidence(t *testing.T) {
	cfg := newConfig(t, map[string]string{"unused-a": "x", "unused-b": "y"})
	cfg.Adapter = hostileAdapter{}
	cfg.Candidates = []Candidate{{Prompt: "chmod000"}, {Prompt: "fix"}}
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v (agent-induced capture failure must be evidence, not an abort)", err)
	}
	assertHostileWorldRace(t, cfg, res, "mvo: capture diff:")
}

// A candidate that repoints its worktree's `.git` file at a fabricated
// repository must not get to choose its own "captured" patch and tree
// (AG-4): the identity check records the world as CRASH and the race
// continues.
func TestRunRepointedWorktreeIsCrashEvidence(t *testing.T) {
	cfg := newConfig(t, map[string]string{"unused-a": "x", "unused-b": "y"})
	decoy := initRepo(t) // the agent's fabricated repository
	cfg.Adapter = hostileAdapter{}
	cfg.Candidates = []Candidate{
		{Prompt: "repoint:" + filepath.Join(decoy, ".git")},
		{Prompt: "fix"},
	}
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v (agent-induced capture failure must be evidence, not an abort)", err)
	}
	assertHostileWorldRace(t, cfg, res, "mvo: capture git identity:")
}

// EnvDigest never opens irregular or unreadable files: FIFOs (would block
// forever), symlinks to devices (unbounded read), and chmod-000 files are
// skipped, leaving the manifest identical to one with no lockfiles at all.
func TestEnvDigestSkipsIrregularAndUnreadableLockfiles(t *testing.T) {
	store, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	want, err := EnvDigest(store, t.TempDir()) // no lockfiles at all
	if err != nil {
		t.Fatalf("EnvDigest(clean): %v", err)
	}

	hostile := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(hostile, "requirements.txt"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(hostile, "go.sum")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostile, "package-lock.json"), []byte("x"), 0o000); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := EnvDigest(store, hostile) // must neither hang nor error
	if err != nil {
		t.Fatalf("EnvDigest(hostile): %v", err)
	}
	if got != want {
		t.Errorf("hostile manifest digest = %q, want %q (all hostile entries skipped)", got, want)
	}

	// A plain readable lockfile is still hashed.
	honest := t.TempDir()
	if err := os.WriteFile(filepath.Join(honest, "requirements.txt"), []byte("requests==2.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err = EnvDigest(store, honest)
	if err != nil {
		t.Fatalf("EnvDigest(honest): %v", err)
	}
	if got == want {
		t.Error("a readable lockfile did not change the manifest digest")
	}
}

func TestRunKeepWorlds(t *testing.T) {
	cfg := newConfig(t, map[string]string{"a-fix.patch": fixPatch})
	cfg.KeepWorlds = true
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	dir := res.Worlds[0].Dir
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); err != nil {
		t.Errorf("kept world missing: %v", err)
	}
	if err := gitx.RemoveWorktree(cfg.Repo, dir); err != nil {
		t.Errorf("RemoveWorktree: %v", err)
	}
}

// Re-racing an intent is a first-class flow (audit replays races per
// race.started window), so a second race must not collide with worktrees
// kept by --keep-worlds — or left behind by a crashed race.
func TestRunReRaceAfterKeepWorlds(t *testing.T) {
	cfg := newConfig(t, map[string]string{"a-fix.patch": fixPatch})
	cfg.KeepWorlds = true
	first, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}

	cfg.KeepWorlds = false
	second, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("second Run after --keep-worlds: %v", err)
	}
	if first.Worlds[0].Dir == second.Worlds[0].Dir {
		t.Fatalf("both races used world dir %s; want unique per-race dirs", first.Worlds[0].Dir)
	}
	// The kept world from the first race is untouched.
	if _, err := os.Stat(filepath.Join(first.Worlds[0].Dir, "x.txt")); err != nil {
		t.Errorf("kept world from first race missing: %v", err)
	}
	if err := gitx.RemoveWorktree(cfg.Repo, first.Worlds[0].Dir); err != nil {
		t.Errorf("RemoveWorktree: %v", err)
	}
	if err := cfg.Ledger.VerifyChain(); err != nil {
		t.Errorf("VerifyChain after two races: %v", err)
	}
}

func TestRunZeroPassRejects(t *testing.T) {
	cfg := newConfig(t, map[string]string{"b-nofix.patch": noFixPatch})
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision.Type != TypeReject {
		t.Errorf("decision type = %q, want REJECT", res.Decision.Type)
	}
	if !strings.Contains(res.Decision.Rationale, GateSuitePass) {
		t.Errorf("rationale %q does not list the failing gate", res.Decision.Rationale)
	}
}

func TestRunConfigValidation(t *testing.T) {
	if _, err := Run(context.Background(), Config{}); err == nil {
		t.Fatal("Run with empty config: want error, got nil")
	}
	cfg := newConfig(t, map[string]string{"a-fix.patch": fixPatch})
	cfg.Adapter = nil
	if _, err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "adapter") {
		t.Fatalf("Run with nil adapter: err = %v, want adapter error", err)
	}
	cfg = newConfig(t, map[string]string{"a-fix.patch": fixPatch})
	cfg.Candidates = nil
	if _, err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "candidates") {
		t.Fatalf("Run with no candidates: err = %v, want candidates error", err)
	}
}

func TestRunUnknownIntent(t *testing.T) {
	cfg := newConfig(t, map[string]string{"a-fix.patch": fixPatch})
	cfg.Intent = "mv0:" + strings.Repeat("0", 64)
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Fatal("Run with unknown intent digest: want error, got nil")
	}
}

// A machinery failure in the adapter aborts the race with an error
// (decision 10: evidence that cannot be produced is an error, not a world).
type errorAdapter struct{}

func (errorAdapter) ID() string      { return "err" }
func (errorAdapter) Version() string { return "v0" }
func (errorAdapter) Start(context.Context, agent.RunSpec) (agent.Run, error) {
	return nil, errors.New("pipe plumbing failed")
}

func TestRunAdapterErrorAborts(t *testing.T) {
	cfg := newConfig(t, map[string]string{"a-fix.patch": fixPatch})
	cfg.Adapter = errorAdapter{}
	if _, err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "pipe plumbing failed") {
		t.Fatalf("Run with erroring adapter: err = %v, want plumbing error", err)
	}
}
