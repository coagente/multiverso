package race

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
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

func (stubOracle) Run(_ context.Context, dir string) (object.Receipt, error) {
	b, err := os.ReadFile(filepath.Join(dir, "x.txt"))
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
		Execution:   object.Execution{Argv: []string{"stub"}, IsolationTier: "T0-worktree"},
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
func seedIntent(t *testing.T, store *cas.Store, repo string) string {
	t.Helper()
	commit, tree, err := gitx.Head(repo)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	polDig, polCanon, err := object.Digest(testPolicy())
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
		Budget:    object.Budget{MaxCandidates: 2, MaxWallMS: 600000},
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

func writePatches(t *testing.T, patches map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range patches {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
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
		Repo:       repo,
		Ledger:     led,
		CAS:        store,
		Intent:     seedIntent(t, store, repo),
		PatchesDir: writePatches(t, patches),
		WorldsDir:  filepath.Join(t.TempDir(), "worlds"),
		Oracle:     stubOracle{},
	}
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
	wantOutcomes := map[string]string{
		"a-fix.patch":   OutcomeCompleted,
		"b-nofix.patch": OutcomeCompleted,
		"c-bogus.patch": OutcomeConfigError,
	}
	for i, wr := range res.Worlds {
		if wr.World.Outcome != wantOutcomes[wr.PatchFile] {
			t.Errorf("world %d (%s) outcome = %q, want %q", i, wr.PatchFile, wr.World.Outcome, wantOutcomes[wr.PatchFile])
		}
	}
	// Patch files are processed in sorted filename order (CP-2).
	if res.Worlds[0].PatchFile != "a-fix.patch" || res.Worlds[2].PatchFile != "c-bogus.patch" {
		t.Errorf("world order = %v, want sorted filenames",
			[]string{res.Worlds[0].PatchFile, res.Worlds[1].PatchFile, res.Worlds[2].PatchFile})
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

	// Worktrees are removed after the race.
	for _, wr := range res.Worlds {
		if _, err := os.Stat(wr.Dir); !os.IsNotExist(err) {
			t.Errorf("world dir %s still present: stat err = %v", wr.Dir, err)
		}
	}

	// The ledger tells the whole story, in order, with an intact chain.
	var types []string
	if err := cfg.Ledger.Scan(func(e ledger.Event) error {
		types = append(types, e.Type)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	wantTypes := []string{
		"race.started",
		"world.created", "world.created", "world.created",
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

	var worlds []object.World
	var receipts []object.Receipt
	var recorded object.Decision
	var policy object.Policy
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
	if err := loadObject(cfg.CAS, recorded.Policy, &policy); err != nil {
		t.Fatalf("load policy: %v", err)
	}

	replayed := Decide(policy, worlds, receipts)
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
// kept by --keep-worlds — or left behind by a crashed race — even though
// the patch filenames repeat.
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
}

func TestRunUnknownIntent(t *testing.T) {
	cfg := newConfig(t, map[string]string{"a-fix.patch": fixPatch})
	cfg.Intent = "mv0:" + strings.Repeat("0", 64)
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Fatal("Run with unknown intent digest: want error, got nil")
	}
}
