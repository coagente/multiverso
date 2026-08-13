package race

// M1c parallel-race tests (docs/design/M1c-containers-parallel.md
// "Testing bar", internal/race). These run under `go test -race`: no
// interleaving corruption, no data races.

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
)

// raceOutcome is the schedule-independent summary two runs must agree on:
// decision type, winner identified by its context CAS key (world digests
// carry timestamps and differ run to run), and evidence count.
type raceOutcome struct {
	typ           string
	winnerContext string
	evidence      int
	worlds        int
}

func summarize(t *testing.T, res *Result) raceOutcome {
	t.Helper()
	out := raceOutcome{typ: res.Decision.Type, evidence: len(res.Decision.Evidence), worlds: len(res.Worlds)}
	if res.Decision.Type == TypeSelect {
		winner := res.Decision.Subject[0]
		for _, wr := range res.Worlds {
			if wr.Digest == winner {
				out.winnerContext = wr.World.Context
			}
		}
		if out.winnerContext == "" {
			t.Fatalf("winner %s not among the race's worlds", winner)
		}
	}
	return out
}

// verifyLedgerReplay asserts the chain is intact and the recorded decision
// reproduces from the ledger's own scan order — completion order under
// parallelism — exactly as mvo audit replays it (decision 17).
func verifyLedgerReplay(t *testing.T, cfg Config) {
	t.Helper()
	if err := cfg.Ledger.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	var worlds []object.World
	var receipts []object.Receipt
	var recorded object.Decision
	if err := cfg.Ledger.Scan(func(e ledger.Event) error {
		switch e.Type {
		case "world.created":
			var w object.World
			if err := json.Unmarshal(e.Payload, &w); err != nil {
				return err
			}
			worlds = append(worlds, w)
		case "receipt.recorded":
			var r object.Receipt
			if err := json.Unmarshal(e.Payload, &r); err != nil {
				return err
			}
			receipts = append(receipts, r)
		case "decision.recorded":
			if err := json.Unmarshal(e.Payload, &recorded); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	policy, err := LoadPolicy(cfg.CAS, recorded.Policy)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	replayed := Decide(policy, worlds, receipts)
	if replayed.Type != recorded.Type ||
		!reflect.DeepEqual(replayed.Subject, recorded.Subject) ||
		!reflect.DeepEqual(replayed.Evidence, recorded.Evidence) ||
		replayed.Rationale != recorded.Rationale {
		t.Errorf("ledger-scan replay differs from recorded:\nreplayed: %+v\nrecorded: %+v", replayed, recorded)
	}
}

// assertPerWorldEventOrder checks the per-ordinal ledger order the M1c
// contract guarantees under parallelism (goroutine locality): agent.started
// < agent.finished < world.created < receipt.recorded. Cross-world
// interleaving is allowed.
func assertPerWorldEventOrder(t *testing.T, cfg Config, res *Result) {
	t.Helper()
	started := map[int]int64{}  // ordinal → seq
	finished := map[int]int64{} // ordinal → seq
	worldSeq := map[string]int64{}
	receiptSeq := map[string]int64{} // world digest → seq
	if err := cfg.Ledger.Scan(func(e ledger.Event) error {
		switch e.Type {
		case "agent.started", "agent.finished":
			var body struct {
				Ordinal int `json:"ordinal"`
			}
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return err
			}
			if e.Type == "agent.started" {
				started[body.Ordinal] = e.Seq
			} else {
				finished[body.Ordinal] = e.Seq
			}
		case "world.created":
			worldSeq[e.PayloadDig] = e.Seq
		case "receipt.recorded":
			var r object.Receipt
			if err := json.Unmarshal(e.Payload, &r); err != nil {
				return err
			}
			receiptSeq[r.World] = e.Seq
		}
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, wr := range res.Worlds {
		k := wr.Ordinal
		s, f, w := started[k], finished[k], worldSeq[wr.Digest]
		if s == 0 || f == 0 || w == 0 {
			t.Fatalf("ordinal %d: missing events (started %d finished %d world %d)", k, s, f, w)
		}
		if !(s < f && f < w) {
			t.Errorf("ordinal %d: event order broken: started %d, finished %d, world %d", k, s, f, w)
		}
		if r, ok := receiptSeq[wr.Digest]; ok && !(w < r) {
			t.Errorf("ordinal %d: receipt seq %d not after world.created %d", k, r, w)
		}
	}
}

// --parallel 3 vs --parallel 1 over the same script candidates: equal
// decision type, equal winner-by-context, equal evidence counts, both
// ledgers chain-clean and replayable.
func TestRunParallelMatchesSerialScript(t *testing.T) {
	patches := map[string]string{
		"a-fix.patch":   fixPatch,   // the unique gate-passer (decision 17)
		"b-nofix.patch": noFixPatch, // applies, suite fails
		"c-bogus.patch": bogusPatch, // CONFIG_ERROR
	}

	serialCfg := newConfig(t, patches)
	serial, err := Run(context.Background(), serialCfg)
	if err != nil {
		t.Fatalf("serial Run: %v", err)
	}
	parallelCfg := newConfig(t, patches)
	parallelCfg.Parallel = 3
	par, err := Run(context.Background(), parallelCfg)
	if err != nil {
		t.Fatalf("parallel Run: %v", err)
	}

	s, p := summarize(t, serial), summarize(t, par)
	if s.typ != TypeSelect {
		t.Fatalf("serial decision = %q (%s), want SELECT", s.typ, serial.Decision.Rationale)
	}
	if p != s {
		t.Errorf("parallel outcome %+v differs from serial %+v", p, s)
	}
	verifyLedgerReplay(t, serialCfg)
	verifyLedgerReplay(t, parallelCfg)
	assertPerWorldEventOrder(t, parallelCfg, par)

	// race.started carries the M1c observational keys.
	if err := parallelCfg.Ledger.Scan(func(e ledger.Event) error {
		if e.Type != "race.started" {
			return nil
		}
		var body map[string]any
		if err := json.Unmarshal(e.Payload, &body); err != nil {
			return err
		}
		if body["parallel"] != float64(3) {
			t.Errorf("race.started parallel = %v, want 3", body["parallel"])
		}
		if body["exec_tier"] != object.TierT0Worktree {
			t.Errorf("race.started exec_tier = %v, want %q", body["exec_tier"], object.TierT0Worktree)
		}
		if dig, ok := body["exec_image_digest"]; !ok || dig != "" {
			t.Errorf("race.started exec_image_digest = %v (present %v), want empty string for T0", dig, ok)
		}
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Worktrees cleaned up in both runs.
	for _, res := range []*Result{serial, par} {
		for _, wr := range res.Worlds {
			if _, err := os.Stat(wr.Dir); !os.IsNotExist(err) {
				t.Errorf("world dir %s still present: stat err = %v", wr.Dir, err)
			}
		}
	}
}

// usePATHFixtures — small copy of internal/agent's helper (test helpers
// are not importable): resolves claude/codex to the fake fixtures and
// FAILS CLOSED if either would not win the LookPath.
func usePATHFixtures(t *testing.T) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fakeagent"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, bin := range []string{"claude", "codex"} {
		got, err := exec.LookPath(bin)
		if err != nil {
			t.Fatalf("fail closed: %q does not resolve after the fixture PATH override: %v", bin, err)
		}
		abs, err := filepath.Abs(got)
		if err != nil || abs != filepath.Join(dir, bin) {
			t.Fatalf("fail closed: %q resolves to %q, not the fixture — refusing to risk a real CLI", bin, got)
		}
	}
}

// ordinalOracle passes exactly one world — ordinal 1, identified by its
// worktree basename — and only if the fake agent demonstrably ran there
// (AGENT_TOUCH.txt). One deterministic gate-passer keeps winner-by-context
// schedule-independent (decision 17) with an adapter whose fixtures edit
// every world identically.
type ordinalOracle struct{}

func (ordinalOracle) ID() string      { return "stub" }
func (ordinalOracle) Version() string { return "v0" }

func (ordinalOracle) Run(_ context.Context, w backend.World) (object.Receipt, error) {
	status := "fail"
	if filepath.Base(w.Dir()) == "001" {
		if _, err := os.Stat(filepath.Join(w.Dir(), "AGENT_TOUCH.txt")); err == nil {
			status = "pass"
		}
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

// The fake-claude variant of the parallel determinism test: three worlds
// generated by the claude-code adapter against the fake fixture, exactly
// one gate-passer, --parallel 3 vs 1.
func TestRunParallelMatchesSerialFakeClaude(t *testing.T) {
	usePATHFixtures(t)
	t.Setenv("FAKE_AGENT_MODE", "happy")

	build := func(parallel int) Config {
		cfg := newConfig(t, map[string]string{"u1": "x", "u2": "y", "u3": "z"})
		cfg.Adapter = mustAdapter(t, "claude-code")
		cfg.Candidates = []Candidate{
			{Prompt: "candidate 1 of 3", Env: []string{"FAKE_AGENT_MODE"}},
			{Prompt: "candidate 2 of 3", Env: []string{"FAKE_AGENT_MODE"}},
			{Prompt: "candidate 3 of 3", Env: []string{"FAKE_AGENT_MODE"}},
		}
		cfg.Oracle = ordinalOracle{}
		cfg.Parallel = parallel
		return cfg
	}

	serialCfg := build(1)
	serial, err := Run(context.Background(), serialCfg)
	if err != nil {
		t.Fatalf("serial Run: %v", err)
	}
	parallelCfg := build(3)
	par, err := Run(context.Background(), parallelCfg)
	if err != nil {
		t.Fatalf("parallel Run: %v", err)
	}

	s, p := summarize(t, serial), summarize(t, par)
	if s.typ != TypeSelect {
		t.Fatalf("serial decision = %q (%s), want SELECT", s.typ, serial.Decision.Rationale)
	}
	if s.evidence != 3 {
		t.Errorf("serial evidence = %d, want 3 (every COMPLETED world gets a receipt)", s.evidence)
	}
	if p != s {
		t.Errorf("parallel outcome %+v differs from serial %+v", p, s)
	}
	// All six worlds ran the fake agent: COMPLETED with a real diff.
	for _, res := range []*Result{serial, par} {
		for _, wr := range res.Worlds {
			if wr.World.Outcome != object.OutcomeCompleted {
				t.Errorf("world %d outcome = %q, want COMPLETED", wr.Ordinal, wr.World.Outcome)
			}
			if wr.World.Cost.USDMicro != 4200 {
				t.Errorf("world %d usd_micro = %d, want 4200", wr.Ordinal, wr.World.Cost.USDMicro)
			}
		}
	}
	verifyLedgerReplay(t, serialCfg)
	verifyLedgerReplay(t, parallelCfg)
	assertPerWorldEventOrder(t, parallelCfg, par)
}

// A mixed race (COMPLETED + CRASH) under --parallel 2 decides correctly:
// the crash stays evidence, the sibling wins.
func TestRunParallelMixedCompletedAndCrash(t *testing.T) {
	cfg := newConfig(t, map[string]string{"unused-a": "x", "unused-b": "y"})
	cfg.Adapter = mixedAdapter{}
	cfg.Candidates = []Candidate{{Prompt: "fix it"}, {Prompt: "crash"}}
	cfg.Parallel = 2
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
	verifyLedgerReplay(t, cfg)
	assertPerWorldEventOrder(t, cfg, res)
}

// Parallel < 1 is a config error, refused before any ledger event.
func TestRunParallelConfigError(t *testing.T) {
	cfg := newConfig(t, map[string]string{"a-fix.patch": fixPatch})
	cfg.Parallel = 0
	if _, err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "parallel") {
		t.Fatalf("Run with Parallel=0: err = %v, want parallel config error", err)
	}
	cfg.Parallel = -1
	if _, err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "parallel") {
		t.Fatalf("Run with Parallel=-1: err = %v, want parallel config error", err)
	}
	events := 0
	if err := cfg.Ledger.Scan(func(ledger.Event) error { events++; return nil }); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Errorf("refused race appended %d events; want 0", events)
	}

	// A nil backend is equally refused.
	cfg = newConfig(t, map[string]string{"a-fix.patch": fixPatch})
	cfg.Backend = nil
	if _, err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("Run with nil backend: err = %v, want backend config error", err)
	}
}

// Decide is permutation-invariant (decision 17): any permutation of the
// worlds and receipts produces a byte-identical decision — which is why
// audit replay from ledger-scan (completion) order reproduces parallel
// races with zero audit changes.
func TestDecidePermutationInvariance(t *testing.T) {
	policy := testPolicy()
	var worlds []object.World
	var receipts []object.Receipt
	// Five worlds: three pass at different wall times, one fails, one
	// CONFIG_ERROR without a receipt.
	for i, spec := range []struct {
		outcome string
		status  string
		wall    int64
	}{
		{OutcomeCompleted, "pass", 30},
		{OutcomeCompleted, "pass", 10},
		{OutcomeCompleted, "pass", 20},
		{OutcomeCompleted, "fail", 5},
		{OutcomeConfigError, "", 0},
	} {
		w, dig := mkWorld(t, "patch-"+strings.Repeat("x", i+1), spec.outcome)
		worlds = append(worlds, w)
		if spec.status != "" {
			r, _ := mkReceipt(t, dig, spec.status, spec.wall)
			receipts = append(receipts, r)
		}
	}

	base := Decide(policy, worlds, receipts)
	baseCanon, err := object.Canonical(base)
	if err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 25; trial++ {
		ws := append([]object.World(nil), worlds...)
		rs := append([]object.Receipt(nil), receipts...)
		rng.Shuffle(len(ws), func(i, j int) { ws[i], ws[j] = ws[j], ws[i] })
		rng.Shuffle(len(rs), func(i, j int) { rs[i], rs[j] = rs[j], rs[i] })
		got := Decide(policy, ws, rs)
		canon, err := object.Canonical(got)
		if err != nil {
			t.Fatal(err)
		}
		if string(canon) != string(baseCanon) {
			t.Fatalf("trial %d: shuffled inputs changed the decision:\n%s\nwant\n%s", trial, canon, baseCanon)
		}
	}
}
