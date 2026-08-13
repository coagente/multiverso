package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/object"
)

func TestClaudeArgvGolden(t *testing.T) {
	full := claudeArgv(RunSpec{
		Prompt: "do it",
		Model:  "claude-sonnet-5",
		Budget: Budget{MaxTurns: 8, MaxUSDMicro: 250_000},
	}, object.TierT0Worktree)
	want := []string{
		"claude", "-p", "do it",
		"--output-format", "stream-json",
		"--bare",
		"--permission-mode", "bypassPermissions",
		"--model", "claude-sonnet-5",
		"--max-turns", "8",
		"--max-budget-usd", "0.25",
	}
	if !reflect.DeepEqual(full, want) {
		t.Errorf("claudeArgv(full) = %q, want %q", full, want)
	}

	// claude-code is tier-independent (M1c decision 14): T1 argv is
	// byte-identical to T0.
	if t1 := claudeArgv(RunSpec{
		Prompt: "do it",
		Model:  "claude-sonnet-5",
		Budget: Budget{MaxTurns: 8, MaxUSDMicro: 250_000},
	}, object.TierT1Container); !reflect.DeepEqual(t1, want) {
		t.Errorf("claudeArgv(T1) = %q, want the T0 argv %q", t1, want)
	}

	// Budget flags appear iff the corresponding limit is set (decision 16).
	bare := claudeArgv(RunSpec{Prompt: "p"}, object.TierT0Worktree)
	wantBare := []string{
		"claude", "-p", "p",
		"--output-format", "stream-json",
		"--bare",
		"--permission-mode", "bypassPermissions",
	}
	if !reflect.DeepEqual(bare, wantBare) {
		t.Errorf("claudeArgv(bare) = %q, want %q", bare, wantBare)
	}

	usdOnly := claudeArgv(RunSpec{Prompt: "p", Budget: Budget{MaxUSDMicro: 4_200}}, object.TierT0Worktree)
	if want := "0.0042"; usdOnly[len(usdOnly)-1] != want {
		t.Errorf("claudeArgv usd flag = %q, want %q (FormatUSDMicro exact)", usdOnly[len(usdOnly)-1], want)
	}
	if got := strings.Join(usdOnly, " "); strings.Contains(got, "--max-turns") {
		t.Errorf("claudeArgv with only usd budget grew --max-turns: %q", got)
	}
}

// The claude-code outcome mapping table, every row, driven by the fake
// fixture (never a real CLI).
func TestClaudeOutcomeMapping(t *testing.T) {
	tests := []struct {
		mode    string
		want    string
		exit    int
		details func(t *testing.T, res *RunResult)
	}{
		{mode: "happy", want: object.OutcomeCompleted, exit: 0,
			details: func(t *testing.T, res *RunResult) {
				if res.Cost.USDMicro != 4200 {
					t.Errorf("usd_micro = %d, want 4200", res.Cost.USDMicro)
				}
				if res.Cost.Source != CostSourceClientEstimate {
					t.Errorf("source = %q, want client-estimate", res.Cost.Source)
				}
				if res.NumTurns != 3 {
					t.Errorf("num_turns = %d, want 3", res.NumTurns)
				}
				if res.SessionID != "fake-session-1" {
					t.Errorf("session = %q, want fake-session-1", res.SessionID)
				}
				if len(res.Transcript) == 0 {
					t.Error("transcript is empty")
				}
			}},
		{mode: "cost-report", want: object.OutcomeCompleted, exit: 0,
			details: func(t *testing.T, res *RunResult) {
				if res.Cost.USDMicro != 3142 { // 0.0031415 half-up at 6 digits
					t.Errorf("usd_micro = %d, want 3142", res.Cost.USDMicro)
				}
				if res.Cost.TokensIn != 1300 { // 1200 + cache_read 100
					t.Errorf("tokens_in = %d, want 1300", res.Cost.TokensIn)
				}
				if res.Cost.TokensOut != 345 {
					t.Errorf("tokens_out = %d, want 345", res.Cost.TokensOut)
				}
				if res.Cost.Source != CostSourceClientEstimate {
					t.Errorf("source = %q, want client-estimate", res.Cost.Source)
				}
			}},
		{mode: "native-cap", want: object.OutcomeBudgetExceeded, exit: 1,
			details: func(t *testing.T, res *RunResult) {
				if res.KilledBy != "" { // the tool exited itself: not a control-plane kill
					t.Errorf("killed_by = %q, want \"\"", res.KilledBy)
				}
			}},
		{mode: "provider-error", want: object.OutcomeProviderError, exit: 1},
		{mode: "bad-exit", want: object.OutcomeCrash, exit: 3},
		{mode: "garbage-output", want: object.OutcomeCrash, exit: 0,
			details: func(t *testing.T, res *RunResult) {
				want := fixtureStdout(t, "claude", "garbage-output")
				if !bytes.Equal(res.Transcript, want) {
					t.Errorf("transcript not byte-identical to fixture output:\ngot  %q\nwant %q", res.Transcript, want)
				}
			}},
		{mode: "usage-error", want: object.OutcomeConfigError, exit: 2,
			details: func(t *testing.T, res *RunResult) {
				if len(res.Transcript) != 0 {
					t.Errorf("transcript = %q, want empty (no stdout events)", res.Transcript)
				}
				if !strings.Contains(string(res.Stderr), "Usage") {
					t.Errorf("stderr = %q, want the usage text", res.Stderr)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			res := waitFixture(t, "claude-code", tt.mode, RunSpec{Prompt: "fix it"})
			if res.Outcome != tt.want {
				t.Fatalf("outcome = %q, want %q", res.Outcome, tt.want)
			}
			if res.ExitCode != tt.exit {
				t.Errorf("exit code = %d, want %d", res.ExitCode, tt.exit)
			}
			if tt.mode != "native-cap" && res.KilledBy != "" {
				t.Errorf("killed_by = %q, want \"\" (self exit)", res.KilledBy)
			}
			if res.Cost.WallMS < 0 {
				t.Errorf("wall_ms = %d, want ≥ 0", res.Cost.WallMS)
			}
			if tt.details != nil {
				tt.details(t, res)
			}
		})
	}
}

// A world dir without stats.py gets AGENT_TOUCH.txt — the guarantee that
// unit-test worlds always produce a non-empty diff.
func TestClaudeEditsCwd(t *testing.T) {
	world := t.TempDir()
	res := waitFixture(t, "claude-code", "happy", RunSpec{Prompt: "fix it", WorldDir: world})
	if res.Outcome != object.OutcomeCompleted {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	b, err := os.ReadFile(filepath.Join(world, "AGENT_TOUCH.txt"))
	if err != nil || string(b) != "fake agent was here\n" {
		t.Errorf("AGENT_TOUCH.txt = %q, err %v", b, err)
	}
}

// Missing binary at spawn: CONFIG_ERROR evidence, not an error (decision
// 10) — the race records the world and continues.
func TestClaudeMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no claude anywhere
	a, err := New("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	h, err := a.Start(context.Background(), RunSpec{WorldDir: t.TempDir(), Prompt: "p"})
	if err != nil {
		t.Fatalf("Start: %v (missing binary must be evidence, not error)", err)
	}
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Outcome != object.OutcomeConfigError {
		t.Errorf("outcome = %q, want CONFIG_ERROR", res.Outcome)
	}
	if res.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1 (never ran)", res.ExitCode)
	}
	// Interrupt on a never-started run is a safe no-op; Wait stays
	// idempotent.
	h.Interrupt()
	if res2, _ := h.Wait(); res2 != res {
		t.Error("Wait not idempotent")
	}
}

// The watchdog kills the whole process group at MaxWall: the fixture's
// sleeping child dies too (pgid probe), and the outcome is BUDGET_EXCEEDED
// with killed_by=watchdog.
func TestClaudeWatchdogKillsProcessGroup(t *testing.T) {
	shrinkKillGrace(t, 100*time.Millisecond)
	world := t.TempDir()
	h := startFixture(t, context.Background(), "claude-code", "slow",
		RunSpec{Prompt: "p", WorldDir: world, Budget: Budget{MaxWall: 300 * time.Millisecond}})
	pid := childPID(t, world) // read before the group dies
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Outcome != object.OutcomeBudgetExceeded {
		t.Errorf("outcome = %q, want BUDGET_EXCEEDED", res.Outcome)
	}
	if res.KilledBy != KilledByWatchdog {
		t.Errorf("killed_by = %q, want watchdog", res.KilledBy)
	}
	if res.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1 (signaled)", res.ExitCode)
	}
	assertProcessGone(t, pid)
}

// The SIGKILL escalation reaches the process GROUP even after the leader
// was reaped: the fixture's grandchild traps SIGTERM (and holds no pipes),
// so the leader dies to the initial SIGTERM and cmd.Wait reaps it well
// inside the grace period — the common case with real CLIs, which exit
// promptly on SIGTERM. Only an unconditional SIGKILL to the pgid
// (decision 9) kills the grandchild; gating it on the leader's exit would
// leave the grandchild spending forever (AG-2).
func TestClaudeWatchdogKillsTermTrappingGrandchild(t *testing.T) {
	shrinkKillGrace(t, 200*time.Millisecond)
	world := t.TempDir()
	h := startFixture(t, context.Background(), "claude-code", "slow-trap-term",
		RunSpec{Prompt: "p", WorldDir: world, Budget: Budget{MaxWall: 300 * time.Millisecond}})
	pid := childPID(t, world) // the TERM-trapping grandchild, read before the group dies
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Outcome != object.OutcomeBudgetExceeded {
		t.Errorf("outcome = %q, want BUDGET_EXCEEDED", res.Outcome)
	}
	if res.KilledBy != KilledByWatchdog {
		t.Errorf("killed_by = %q, want watchdog", res.KilledBy)
	}
	assertProcessGone(t, pid)
}

// Rule 0 precedence (decision 8): a watchdog kill beats a flushed success
// result — control-plane-primary means exactly this.
func TestClaudeWatchdogBeatsFlushedSuccess(t *testing.T) {
	shrinkKillGrace(t, 100*time.Millisecond)
	world := t.TempDir()
	h := startFixture(t, context.Background(), "claude-code", "slow-after-result",
		RunSpec{Prompt: "p", WorldDir: world, Budget: Budget{MaxWall: 300 * time.Millisecond}})
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !bytes.Contains(res.Transcript, []byte(`"subtype":"success"`)) {
		t.Fatalf("fixture did not flush its success result; transcript: %q", res.Transcript)
	}
	if res.Outcome != object.OutcomeBudgetExceeded {
		t.Errorf("outcome = %q, want BUDGET_EXCEEDED (kill reason outranks the stream)", res.Outcome)
	}
	if res.KilledBy != KilledByWatchdog {
		t.Errorf("killed_by = %q, want watchdog", res.KilledBy)
	}
	// The flushed result's cost is still harvested honestly.
	if res.Cost.USDMicro != 4200 || res.Cost.Source != CostSourceClientEstimate {
		t.Errorf("cost = %+v, want harvested usd_micro 4200 client-estimate", res.Cost)
	}
}

// Interrupt() → INTERRUPTED, killed_by=interrupt.
func TestClaudeInterrupt(t *testing.T) {
	shrinkKillGrace(t, 100*time.Millisecond)
	h := startFixture(t, context.Background(), "claude-code", "slow", RunSpec{Prompt: "p"})
	awaitEventKind(t, h, EventInit)
	h.Interrupt()
	h.Interrupt() // idempotent
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Outcome != object.OutcomeInterrupted {
		t.Errorf("outcome = %q, want INTERRUPTED", res.Outcome)
	}
	if res.KilledBy != KilledByInterrupt {
		t.Errorf("killed_by = %q, want interrupt", res.KilledBy)
	}
}

// ctx cancellation → INTERRUPTED; ctx deadline → BUDGET_EXCEEDED
// (decision 8: the kill reason comes from ctx.Err()).
func TestClaudeContextCancelAndDeadline(t *testing.T) {
	shrinkKillGrace(t, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	h := startFixture(t, ctx, "claude-code", "slow", RunSpec{Prompt: "p"})
	awaitEventKind(t, h, EventInit)
	cancel()
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Outcome != object.OutcomeInterrupted || res.KilledBy != KilledByInterrupt {
		t.Errorf("cancel: outcome %q killed_by %q, want INTERRUPTED/interrupt", res.Outcome, res.KilledBy)
	}

	dctx, dcancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer dcancel()
	h2 := startFixture(t, dctx, "claude-code", "slow", RunSpec{Prompt: "p"})
	res2, err := h2.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res2.Outcome != object.OutcomeBudgetExceeded || res2.KilledBy != KilledByWatchdog {
		t.Errorf("deadline: outcome %q killed_by %q, want BUDGET_EXCEEDED/watchdog", res2.Outcome, res2.KilledBy)
	}
}

// The normalized event stream for a happy run: init → item → result, and
// garbage lines surface as tolerated "unknown" events.
func TestClaudeEventKinds(t *testing.T) {
	h := startFixture(t, context.Background(), "claude-code", "happy", RunSpec{Prompt: "p"})
	var kinds []string
	for ev := range h.Events() {
		kinds = append(kinds, ev.Kind)
	}
	if want := []string{EventInit, EventItem, EventResult}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("event kinds = %v, want %v", kinds, want)
	}

	h2 := startFixture(t, context.Background(), "claude-code", "garbage-output", RunSpec{Prompt: "p"})
	kinds = nil
	for ev := range h2.Events() {
		kinds = append(kinds, ev.Kind)
	}
	if want := []string{EventInit, EventUnknown, EventUnknown, EventUnknown}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("garbage event kinds = %v, want %v", kinds, want)
	}
}

// Direct parser coverage for stream shapes the fixtures cannot exercise:
// retry categories, string-typed cost, malformed and negative costs.
func TestClaudeParserEdgeCases(t *testing.T) {
	// Provider signal via api_retry category, error object form.
	p := &claudeParser{}
	p.line([]byte(`{"type":"system","subtype":"init"}`), false)
	p.line([]byte(`{"type":"system","subtype":"api_retry","error":{"category":"overloaded"}}`), false)
	p.line([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom"}`), false)
	if got := p.outcome(1); got != object.OutcomeProviderError {
		t.Errorf("retry-category run outcome = %q, want PROVIDER_ERROR", got)
	}

	// Result error text fallback without any retry event.
	p = &claudeParser{}
	p.line([]byte(`{"type":"system","subtype":"init"}`), false)
	p.line([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"hit the rate limit"}`), false)
	if got := p.outcome(1); got != object.OutcomeProviderError {
		t.Errorf("error-text run outcome = %q, want PROVIDER_ERROR", got)
	}

	// Malformed / negative cost reports → usd_micro 0, source still honest.
	p = &claudeParser{}
	p.line([]byte(`{"type":"result","subtype":"success","is_error":false,"total_cost_usd":-0.5}`), false)
	cost, _, _ := p.harvest()
	if cost.USDMicro != 0 || cost.Source != CostSourceClientEstimate {
		t.Errorf("negative cost: %+v, want usd 0 / client-estimate", cost)
	}
	p = &claudeParser{}
	p.line([]byte(`{"type":"result","subtype":"success","is_error":false,"total_cost_usd":"0.0042"}`), false)
	cost, _, _ = p.harvest()
	if cost.USDMicro != 4200 {
		t.Errorf("string cost: usd = %d, want 4200", cost.USDMicro)
	}
	// Exponent form goes through the float fallback.
	p = &claudeParser{}
	p.line([]byte(`{"type":"result","subtype":"success","is_error":false,"total_cost_usd":4.2e-3}`), false)
	cost, _, _ = p.harvest()
	if cost.USDMicro != 4200 {
		t.Errorf("exponent cost: usd = %d, want 4200", cost.USDMicro)
	}

	// A success result with a non-zero exit is a stream-contract violation
	// (row 7): honesty over optimism.
	p = &claudeParser{}
	p.line([]byte(`{"type":"system","subtype":"init"}`), false)
	p.line([]byte(`{"type":"result","subtype":"success","is_error":false}`), false)
	if got := p.outcome(1); got != object.OutcomeCrash {
		t.Errorf("success+exit1 outcome = %q, want CRASH", got)
	}
}
