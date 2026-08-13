package agent

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
)

// Live smoke tests: optional, NEVER CI. They spawn the real CLIs and cost
// money, so they run only under MVO_LIVE_AGENT_TEST=1. When opted in, a
// missing CLI is a test FAILURE — explicit opt-in must not silently no-op.
// They document flag reality drift (decision 15): if a pinned flag
// vanished upstream, these fail with CONFIG_ERROR while the fake matrix
// stays green.

func liveGate(t *testing.T, bin string) {
	t.Helper()
	if os.Getenv("MVO_LIVE_AGENT_TEST") != "1" {
		t.Skip("live agent test: set MVO_LIVE_AGENT_TEST=1 to opt in (spawns real CLIs, costs money)")
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Fatalf("MVO_LIVE_AGENT_TEST=1 but %q is not installed: %v", bin, err)
	}
}

func liveRepo(t *testing.T) (repo, baseTree string) {
	t.Helper()
	repo = initRepo(t)
	tree, err := gitx.TreeDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, tree
}

func TestLiveClaudeCode(t *testing.T) {
	liveGate(t, "claude")
	repo, baseTree := liveRepo(t)

	a, err := New("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	h, err := a.Start(context.Background(), RunSpec{
		WorldDir: repo,
		Prompt:   "Create a file named HELLO.txt containing exactly: hello",
		Budget: Budget{
			MaxWall:     120 * time.Second,
			MaxTurns:    2,
			MaxUSDMicro: 50_000, // 0.05 USD
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Outcome != object.OutcomeCompleted {
		t.Fatalf("outcome = %q (exit %d, stderr %q)", res.Outcome, res.ExitCode, res.Stderr)
	}
	patch, err := Diff(repo, baseTree)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patch) == 0 {
		t.Error("live run produced an empty diff")
	}
	if res.Cost.USDMicro <= 0 {
		t.Errorf("usd_micro = %d, want > 0 from the result event", res.Cost.USDMicro)
	}
	if len(res.Transcript) == 0 {
		t.Error("live transcript is empty")
	}
}

func TestLiveCodex(t *testing.T) {
	liveGate(t, "codex")
	repo, baseTree := liveRepo(t)

	a, err := New("codex")
	if err != nil {
		t.Fatal(err)
	}
	h, err := a.Start(context.Background(), RunSpec{
		WorldDir: repo,
		Prompt:   "Create a file named HELLO.txt containing exactly: hello",
		Budget:   Budget{MaxWall: 120 * time.Second}, // codex: watchdog-only
		Env:      []string{"CODEX_API_KEY"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Outcome != object.OutcomeCompleted {
		t.Fatalf("outcome = %q (exit %d, stderr %q)", res.Outcome, res.ExitCode, res.Stderr)
	}
	patch, err := Diff(repo, baseTree)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patch) == 0 {
		t.Error("live run produced an empty diff")
	}
	if res.Cost.TokensIn <= 0 || res.Cost.TokensOut <= 0 {
		t.Errorf("tokens = %d/%d, want > 0 (codex reports tokens, not dollars)", res.Cost.TokensIn, res.Cost.TokensOut)
	}
	if res.Cost.USDMicro != 0 {
		t.Errorf("usd_micro = %d, want 0 (no price table in M1b)", res.Cost.USDMicro)
	}
	if len(res.Transcript) == 0 {
		t.Error("live transcript is empty")
	}
}
