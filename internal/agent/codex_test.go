package agent

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/object"
)

func TestCodexArgvGolden(t *testing.T) {
	full := codexArgv(RunSpec{Prompt: "do it", Model: "gpt-5.3-codex"}, object.TierT0Worktree)
	want := []string{"codex", "exec", "--json", "--sandbox", "workspace-write", "-m", "gpt-5.3-codex", "do it"}
	if !reflect.DeepEqual(full, want) {
		t.Errorf("codexArgv(model) = %q, want %q", full, want)
	}
	bare := codexArgv(RunSpec{Prompt: "do it"}, object.TierT0Worktree)
	wantBare := []string{"codex", "exec", "--json", "--sandbox", "workspace-write", "do it"}
	if !reflect.DeepEqual(bare, wantBare) {
		t.Errorf("codexArgv(bare) = %q, want %q", bare, wantBare)
	}

	// Under T1 the container IS the sandbox (M1c decision 14): codex's own
	// sandbox is disabled and the git-repo check skipped (the worktree's
	// .git points at a host path that does not exist in the container).
	t1 := codexArgv(RunSpec{Prompt: "do it", Model: "gpt-5.3-codex"}, object.TierT1Container)
	wantT1 := []string{"codex", "exec", "--json", "--sandbox", "danger-full-access", "--skip-git-repo-check", "-m", "gpt-5.3-codex", "do it"}
	if !reflect.DeepEqual(t1, wantT1) {
		t.Errorf("codexArgv(T1) = %q, want %q", t1, wantT1)
	}
	t1bare := codexArgv(RunSpec{Prompt: "do it"}, object.TierT1Container)
	wantT1Bare := []string{"codex", "exec", "--json", "--sandbox", "danger-full-access", "--skip-git-repo-check", "do it"}
	if !reflect.DeepEqual(t1bare, wantT1Bare) {
		t.Errorf("codexArgv(T1 bare) = %q, want %q", t1bare, wantT1Bare)
	}
}

// The codex outcome mapping table, every row, driven by the fake fixture.
func TestCodexOutcomeMapping(t *testing.T) {
	tests := []struct {
		mode    string
		want    string
		exit    int
		details func(t *testing.T, res *RunResult)
	}{
		{mode: "happy", want: object.OutcomeCompleted, exit: 0,
			details: func(t *testing.T, res *RunResult) {
				if res.Cost.USDMicro != 0 { // codex reports tokens, never dollars (decision 3)
					t.Errorf("usd_micro = %d, want 0", res.Cost.USDMicro)
				}
				if res.Cost.TokensIn != 1000 || res.Cost.TokensOut != 200 {
					t.Errorf("tokens = %d/%d, want 1000/200", res.Cost.TokensIn, res.Cost.TokensOut)
				}
				if res.Cost.Source != CostSourceClientEstimate {
					t.Errorf("source = %q, want client-estimate", res.Cost.Source)
				}
				if res.NumTurns != 1 {
					t.Errorf("num_turns = %d, want 1", res.NumTurns)
				}
				if res.SessionID != "fake-thread-1" {
					t.Errorf("session = %q, want fake-thread-1", res.SessionID)
				}
			}},
		{mode: "cost-report", want: object.OutcomeCompleted, exit: 0,
			details: func(t *testing.T, res *RunResult) {
				// usd_micro 0 with non-zero tokens IS the honest record.
				if res.Cost.USDMicro != 0 {
					t.Errorf("usd_micro = %d, want 0", res.Cost.USDMicro)
				}
				if res.Cost.TokensIn != 1300 { // 1200 + cached 100
					t.Errorf("tokens_in = %d, want 1300", res.Cost.TokensIn)
				}
				if res.Cost.TokensOut != 345 {
					t.Errorf("tokens_out = %d, want 345", res.Cost.TokensOut)
				}
				if res.Cost.Source != CostSourceClientEstimate {
					t.Errorf("source = %q, want client-estimate", res.Cost.Source)
				}
			}},
		// codex has no native caps: the native-cap mode behaves as happy.
		{mode: "native-cap", want: object.OutcomeCompleted, exit: 0},
		{mode: "provider-error", want: object.OutcomeProviderError, exit: 1},
		{mode: "bad-exit", want: object.OutcomeCrash, exit: 3},
		{mode: "garbage-output", want: object.OutcomeCrash, exit: 0,
			details: func(t *testing.T, res *RunResult) {
				want := fixtureStdout(t, "codex", "garbage-output")
				if !bytes.Equal(res.Transcript, want) {
					t.Errorf("transcript not byte-identical to fixture output:\ngot  %q\nwant %q", res.Transcript, want)
				}
			}},
		{mode: "usage-error", want: object.OutcomeConfigError, exit: 2,
			details: func(t *testing.T, res *RunResult) {
				if len(res.Transcript) != 0 {
					t.Errorf("transcript = %q, want empty", res.Transcript)
				}
				if len(res.Stderr) == 0 {
					t.Error("stderr empty, want the usage text")
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			res := waitFixture(t, "codex", tt.mode, RunSpec{Prompt: "fix it"})
			if res.Outcome != tt.want {
				t.Fatalf("outcome = %q, want %q", res.Outcome, tt.want)
			}
			if res.ExitCode != tt.exit {
				t.Errorf("exit code = %d, want %d", res.ExitCode, tt.exit)
			}
			if res.KilledBy != "" {
				t.Errorf("killed_by = %q, want \"\" (self exit)", res.KilledBy)
			}
			if tt.details != nil {
				tt.details(t, res)
			}
		})
	}
}

// Watchdog + rule 0 for codex: group kill (child probe) and a flushed
// turn.completed never outranking the control plane's kill reason.
func TestCodexWatchdog(t *testing.T) {
	shrinkKillGrace(t, 100*time.Millisecond)
	world := t.TempDir()
	h := startFixture(t, context.Background(), "codex", "slow",
		RunSpec{Prompt: "p", WorldDir: world, Budget: Budget{MaxWall: 300 * time.Millisecond}})
	pid := childPID(t, world)
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Outcome != object.OutcomeBudgetExceeded || res.KilledBy != KilledByWatchdog {
		t.Errorf("outcome %q killed_by %q, want BUDGET_EXCEEDED/watchdog", res.Outcome, res.KilledBy)
	}
	assertProcessGone(t, pid)
}

func TestCodexWatchdogBeatsFlushedTurn(t *testing.T) {
	shrinkKillGrace(t, 100*time.Millisecond)
	h := startFixture(t, context.Background(), "codex", "slow-after-result",
		RunSpec{Prompt: "p", Budget: Budget{MaxWall: 300 * time.Millisecond}})
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !bytes.Contains(res.Transcript, []byte(`"turn.completed"`)) {
		t.Fatalf("fixture did not flush turn.completed; transcript: %q", res.Transcript)
	}
	if res.Outcome != object.OutcomeBudgetExceeded || res.KilledBy != KilledByWatchdog {
		t.Errorf("outcome %q killed_by %q, want BUDGET_EXCEEDED/watchdog", res.Outcome, res.KilledBy)
	}
}

func TestCodexInterrupt(t *testing.T) {
	shrinkKillGrace(t, 100*time.Millisecond)
	h := startFixture(t, context.Background(), "codex", "slow", RunSpec{Prompt: "p"})
	awaitEventKind(t, h, EventInit)
	h.Interrupt()
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Outcome != object.OutcomeInterrupted || res.KilledBy != KilledByInterrupt {
		t.Errorf("outcome %q killed_by %q, want INTERRUPTED/interrupt", res.Outcome, res.KilledBy)
	}
}

// Token accumulation across multiple turns (the parser sums usage; the
// fixtures only emit one turn).
func TestCodexParserAccumulatesTurns(t *testing.T) {
	p := &codexParser{}
	p.line([]byte(`{"type":"thread.started","thread_id":"t-1"}`), false)
	p.line([]byte(`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":5,"output_tokens":3}}`), false)
	p.line([]byte(`{"type":"item.completed","item":{"type":"agent_message"}}`), false)
	p.line([]byte(`{"type":"turn.completed","usage":{"input_tokens":20,"cached_input_tokens":0,"output_tokens":7}}`), false)
	cost, turns, session := p.harvest()
	if cost.TokensIn != 35 || cost.TokensOut != 10 {
		t.Errorf("tokens = %d/%d, want 35/10", cost.TokensIn, cost.TokensOut)
	}
	if turns != 2 {
		t.Errorf("turns = %d, want 2", turns)
	}
	if session != "t-1" {
		t.Errorf("session = %q, want t-1", session)
	}
	if cost.USDMicro != 0 || cost.Source != CostSourceClientEstimate {
		t.Errorf("cost = %+v, want usd 0 / client-estimate", cost)
	}
	if got := p.outcome(0); got != object.OutcomeCompleted {
		t.Errorf("outcome = %q, want COMPLETED", got)
	}
}

// Mixed success + failure in one stream: turn.failed wins over completed
// turns (rows 3/4 precede nothing — row 2 requires no turn.failed).
func TestCodexParserTurnFailedAfterCompleted(t *testing.T) {
	p := &codexParser{}
	p.line([]byte(`{"type":"thread.started","thread_id":"t-1"}`), false)
	p.line([]byte(`{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}`), false)
	p.line([]byte(`{"type":"turn.failed","error":{"message":"insufficient_quota"}}`), false)
	if got := p.outcome(1); got != object.OutcomeProviderError {
		t.Errorf("outcome = %q, want PROVIDER_ERROR", got)
	}

	p = &codexParser{}
	p.line([]byte(`{"type":"thread.started"}`), false)
	p.line([]byte(`{"type":"turn.failed","error":{"message":"segfault in tool"}}`), false)
	if got := p.outcome(1); got != object.OutcomeCrash {
		t.Errorf("outcome = %q, want CRASH (no provider signal)", got)
	}
}
