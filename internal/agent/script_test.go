package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
)

const fixPatch = `--- a/x.txt
+++ b/x.txt
@@ -1 +1 @@
-broken
+fixed
`

const bogusPatch = "this is not a patch\n"

func runScript(t *testing.T, ctx context.Context, worldDir, prompt string) *RunResult {
	t.Helper()
	a, err := New("script")
	if err != nil {
		t.Fatal(err)
	}
	h, err := a.Start(ctx, RunSpec{WorldDir: worldDir, Prompt: prompt})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// The events channel is closed-empty; Interrupt is a no-op.
	h.Interrupt()
	if _, ok := <-h.Events(); ok {
		t.Error("script events channel yielded an event; want closed-empty")
	}
	return res
}

// The script adapter reproduces M0 apply behavior byte-for-byte: a good
// patch lands in the index (COMPLETED), a conflicting patch leaves the
// worktree at the base tree (CONFIG_ERROR, atomic apply).
func TestScriptApplyAndConfigError(t *testing.T) {
	repo := initRepo(t)
	baseTree, err := gitx.TreeDigest(repo)
	if err != nil {
		t.Fatal(err)
	}

	res := runScript(t, context.Background(), repo, fixPatch)
	if res.Outcome != object.OutcomeCompleted {
		t.Fatalf("outcome = %q, want COMPLETED", res.Outcome)
	}
	if res.ExitCode != 0 || res.KilledBy != "" {
		t.Errorf("exit %d killed_by %q, want 0 / \"\"", res.ExitCode, res.KilledBy)
	}
	if string(res.Transcript) != "" || string(res.Stderr) != "" {
		t.Errorf("transcript/stderr = %q/%q, want empty", res.Transcript, res.Stderr)
	}
	if res.Cost.Source != CostSourceNone || res.Cost.USDMicro != 0 ||
		res.Cost.TokensIn != 0 || res.Cost.TokensOut != 0 {
		t.Errorf("cost = %+v, want zeros with source none", res.Cost)
	}
	if b, err := os.ReadFile(filepath.Join(repo, "x.txt")); err != nil || string(b) != "fixed\n" {
		t.Errorf("x.txt = %q, err %v; want fixed", b, err)
	}
	tree, err := gitx.WriteTree(repo)
	if err != nil {
		t.Fatal(err)
	}
	if tree == baseTree {
		t.Error("tree unchanged after a successful apply")
	}

	// Conflict: fresh repo, bogus patch.
	repo2 := initRepo(t)
	res2 := runScript(t, context.Background(), repo2, bogusPatch)
	if res2.Outcome != object.OutcomeConfigError {
		t.Fatalf("outcome = %q, want CONFIG_ERROR", res2.Outcome)
	}
	if res2.ExitCode != 1 {
		t.Errorf("exit = %d, want 1", res2.ExitCode)
	}
	tree2, err := gitx.WriteTree(repo2)
	if err != nil {
		t.Fatal(err)
	}
	base2, err := gitx.TreeDigest(repo2)
	if err != nil {
		t.Fatal(err)
	}
	if tree2 != base2 {
		t.Errorf("failed apply moved the tree: %s != %s (apply must be atomic)", tree2, base2)
	}
}

// An empty patch is CONFIG_ERROR (design script mapping row 3).
func TestScriptEmptyPatch(t *testing.T) {
	res := runScript(t, context.Background(), initRepo(t), "")
	if res.Outcome != object.OutcomeConfigError {
		t.Errorf("outcome = %q, want CONFIG_ERROR", res.Outcome)
	}
}

// ctx canceled before applying → INTERRUPTED; expired deadline →
// BUDGET_EXCEEDED (script mapping row 1).
func TestScriptContext(t *testing.T) {
	repo := initRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := runScript(t, ctx, repo, fixPatch)
	if res.Outcome != object.OutcomeInterrupted || res.KilledBy != KilledByInterrupt {
		t.Errorf("canceled: outcome %q killed_by %q, want INTERRUPTED/interrupt", res.Outcome, res.KilledBy)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "x.txt")); string(b) != "broken\n" {
		t.Errorf("canceled run still applied the patch: x.txt = %q", b)
	}

	dctx, dcancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer dcancel()
	time.Sleep(2 * time.Millisecond) // let the deadline expire
	res2 := runScript(t, dctx, repo, fixPatch)
	if res2.Outcome != object.OutcomeBudgetExceeded || res2.KilledBy != KilledByWatchdog {
		t.Errorf("deadline: outcome %q killed_by %q, want BUDGET_EXCEEDED/watchdog", res2.Outcome, res2.KilledBy)
	}
}

func TestScriptEmptyWorldDir(t *testing.T) {
	a, err := New("script")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(context.Background(), RunSpec{Prompt: fixPatch}); err == nil {
		t.Error("Start with empty WorldDir: want error (invalid spec)")
	}
}

func TestNewAdapters(t *testing.T) {
	for _, tt := range []struct{ name, id, bin string }{
		{"script", "script", ""},
		{"claude-code", "claude-code", "claude"},
		{"codex", "codex", "codex"},
	} {
		a, err := New(tt.name)
		if err != nil {
			t.Fatalf("New(%s): %v", tt.name, err)
		}
		if a.ID() != tt.id || a.Version() != "v0" {
			t.Errorf("New(%s) = %s@%s, want %s@v0", tt.name, a.ID(), a.Version(), tt.id)
		}
		if got := Binary(a); got != tt.bin {
			t.Errorf("Binary(%s) = %q, want %q", tt.name, got, tt.bin)
		}
	}
	if _, err := New("aider"); err == nil {
		t.Error("New(aider): want error for unknown adapter")
	}
}
