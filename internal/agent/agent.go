// Package agent implements the AgentAdapter contract (AG-1): a
// generator-agnostic way to spawn a coding agent inside one world, cap its
// spend (AG-2), and harvest three artifacts — the transcript (AG-3), the
// diff (AG-4), and the cost. Adapters: script (M0's scripted patches
// behind the interface), claude-code, and codex.
//
// Diff and Transcript are deliberately NOT adapter operations: diff
// capture is the control-plane Diff function (one implementation for every
// adapter — no adapter code path can supply its own diff), and the
// transcript is the raw stdout byte stream captured by the shared runner's
// tee. See docs/design/M1b-agent-adapters.md.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coagente/multiverso/internal/object"
)

// Cost sources (object.RunCost.Source): "client-estimate" for anything a
// CLI reported about itself, "none" when nothing was reported (AG-2).
const (
	CostSourceNone           = "none"
	CostSourceClientEstimate = "client-estimate"
)

// KilledBy values (RunResult.KilledBy): who terminated the run. Empty
// means the process exited on its own (native cap included).
const (
	KilledByWatchdog  = "watchdog"
	KilledByInterrupt = "interrupt"
)

// Normalized event kinds (Event.Kind).
const (
	EventInit    = "init"
	EventItem    = "item"
	EventRetry   = "retry"
	EventResult  = "result"
	EventUnknown = "unknown"
)

// Adapter turns a RunSpec into a running agent inside one world. It is the
// generator-side extension surface (PRD §9). Implementations: script,
// claude-code, codex.
type Adapter interface {
	ID() string      // "script" | "claude-code" | "codex"
	Version() string // OUR adapter contract version ("v0"), not the CLI's
	Start(ctx context.Context, spec RunSpec) (Run, error)
}

// New returns the adapter registered under name; unknown names error.
// Producer.Adapter is always ID()+"@"+Version() ("script@v0" — unchanged
// from M0 — "claude-code@v0", "codex@v0").
func New(name string) (Adapter, error) {
	switch name {
	case "script":
		return scriptAdapter{}, nil
	case "claude-code":
		return claudeAdapter{}, nil
	case "codex":
		return codexAdapter{}, nil
	}
	return nil, fmt.Errorf("agent: unknown adapter %q (want script, claude-code, or codex)", name)
}

// Binary returns the executable an adapter spawns, or "" for in-process
// adapters (script). cmd/mvo pre-flights exec.LookPath on it so a missing
// CLI aborts before race.started instead of burning N CONFIG_ERROR worlds
// (design decision 10).
func Binary(a Adapter) string {
	if b, ok := a.(interface{ binary() string }); ok {
		return b.binary()
	}
	return ""
}

// RunSpec is one candidate run. WorldDir is the agent's cwd and the only
// place it may write (enforced by isolation tier, recorded not assumed).
type RunSpec struct {
	WorldDir string // absolute path to the world worktree
	Prompt   string // literal prompt text; script: the patch bytes as a string
	Model    string // model pin passed to the CLI; "" = tool default
	Budget   Budget
	Env      []string // extra parent env var NAMES to pass through (base set always included)
}

// Budget bounds one run (AG-2). Control-plane wall clock is primary; the
// other two are native caps forwarded where the tool supports them
// (claude-code: --max-turns, --max-budget-usd; codex: none).
type Budget struct {
	MaxWall     time.Duration // watchdog: kill the process group at this age; 0 = no per-run timer
	MaxTurns    int           // native cap where supported; 0 = uncapped
	MaxUSDMicro int64         // native cap where supported, micro-USD; 0 = uncapped
}

// Run is a live (or, for script, already-finished) agent run.
type Run interface {
	// Events streams best-effort normalized events; closed when the run
	// ends. May drop under a slow consumer — the transcript is the record.
	Events() <-chan Event
	// Interrupt requests termination: SIGTERM to the process group, then
	// SIGKILL after the grace period. Wait then reports INTERRUPTED.
	// Idempotent; no-op after exit.
	Interrupt()
	// Wait blocks until the terminal state and returns the result.
	// Idempotent. error is reserved for evidence-collection failure
	// (design decision 10); a crashed/killed/failed agent is a RunResult.
	Wait() (*RunResult, error)
}

// Event is one normalized stream event. Kind ∈ {"init","item","retry",
// "result","unknown"}; Raw is the verbatim line (also in the transcript).
type Event struct {
	Kind string
	Raw  json.RawMessage
}

// RunResult is the terminal record of one run.
type RunResult struct {
	Outcome    string         // object.Outcome* — exactly one of the six
	ExitCode   int            // process exit code; -1 when signaled / never ran
	KilledBy   string         // "watchdog" | "interrupt" | "" (native/self exit)
	Cost       object.RunCost // wall measured by the runner; usd/tokens parsed from the stream
	NumTurns   int            // turns reported/counted; 0 when unknown
	SessionID  string         // tool session/thread id when reported; ""
	Transcript []byte         // raw stdout event-stream bytes, verbatim (AG-3)
	Stderr     []byte         // raw stderr bytes
}
