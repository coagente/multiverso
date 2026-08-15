package agent

import (
	"context"
	"errors"
	"time"

	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
)

// scriptAdapter is M0's scripted-patch behavior, verbatim, behind the
// AgentAdapter interface: the prompt IS the patch bytes (decision 7),
// piped to git apply --index exactly as race did. COMPLETED on success,
// CONFIG_ERROR on an empty patch or apply conflict — no receipts for
// failed worlds, the race continues (CP-3). It completes synchronously
// in-process (its only subprocesses are the same local git calls M0
// made), which keeps the race tests hermetic and fast.
type scriptAdapter struct{}

// ID implements Adapter.
func (scriptAdapter) ID() string { return "script" }

// Version implements Adapter.
func (scriptAdapter) Version() string { return "v0" }

// Start implements Adapter. The returned Run is already finished: empty
// transcript (its CAS key is the well-known empty-bytes key — "the
// captured stream was empty" is the honest record), closed events channel,
// cost = measured wall only, source "none".
func (scriptAdapter) Start(ctx context.Context, spec RunSpec) (Run, error) {
	if spec.WorldDir == "" {
		return nil, errors.New("agent: empty WorldDir in run spec")
	}
	start := time.Now()
	res := &RunResult{Transcript: []byte{}, Stderr: []byte{}}
	switch {
	case ctx.Err() != nil:
		res.ExitCode = -1
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res.Outcome, res.KilledBy = object.OutcomeBudgetExceeded, KilledByWatchdog
		} else {
			res.Outcome, res.KilledBy = object.OutcomeInterrupted, KilledByInterrupt
		}
	case spec.Prompt == "":
		// CONFIG_ERROR is one label over several unrelated causes, and the
		// World schema has no field for a reason (M1). Writing it to the
		// captured stderr at least puts the distinction in CAS, where the
		// ledger's agent.finished event names the blob that holds it.
		res.Outcome, res.ExitCode = object.OutcomeConfigError, 1 // empty patch
		res.Stderr = []byte("mvo: script adapter: candidate patch file is empty — nothing to apply\n")
	default:
		if applyErr := gitx.Apply(spec.WorldDir, []byte(spec.Prompt)); applyErr != nil {
			// git apply --index is atomic on conflict: the worktree stays
			// at the base tree — exact M0 behavior. gitx does not surface
			// git's own exit code (M1a landing-apply precedent: 1 on
			// conflict).
			res.Outcome, res.ExitCode = object.OutcomeConfigError, 1
			res.Stderr = []byte("mvo: script adapter: patch did not apply to the intent's base tree: " +
				applyErr.Error() + "\n")
			break
		}
		res.Outcome = object.OutcomeCompleted
	}
	res.Cost = object.RunCost{WallMS: time.Since(start).Milliseconds(), Source: CostSourceNone}
	return newDoneRun(res), nil
}

// doneRun is an already-finished Run.
type doneRun struct {
	res    *RunResult
	events chan Event
}

func newDoneRun(res *RunResult) *doneRun {
	ch := make(chan Event)
	close(ch)
	return &doneRun{res: res, events: ch}
}

// Events implements Run (closed-empty: script emits no stream).
func (r *doneRun) Events() <-chan Event { return r.events }

// Interrupt implements Run (no-op after exit; a doneRun has exited).
func (r *doneRun) Interrupt() {}

// Wait implements Run.
func (r *doneRun) Wait() (*RunResult, error) { return r.res, nil }
