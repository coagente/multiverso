package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/coagente/multiverso/internal/object"
)

const (
	// eventBuffer is the Events() channel capacity; sends drop when it is
	// full rather than ever blocking or truncating evidence (decision 11:
	// the transcript is the record).
	eventBuffer = 64
	// maxLineBytes caps one parsed stream line (10 MiB). An oversized line
	// becomes one "unknown" event and parsing continues; the transcript
	// still holds it byte-for-byte.
	maxLineBytes = 10 << 20
	// waitDelay bounds cmd.Wait if an escaped process holds the output
	// pipes open after the leader exits (M1a oracle precedent).
	waitDelay = 5 * time.Second
)

// killGrace is the SIGTERM→SIGKILL grace for process-group kills. A var so
// tests can shrink it; 5 s in production (design decision 9).
var killGrace = 5 * time.Second

// baseEnvNames is the fixed env allowlist every agent subprocess receives
// (decision 14). Values come from mvo's own environment; unset names are
// omitted. HOME passthrough is what lets locally-authenticated CLIs find
// their credentials; API-key vars must be allowlisted explicitly via
// RunSpec.Env (NFR-4: no secret enters a world unless allowlisted).
var baseEnvNames = []string{"PATH", "HOME", "TMPDIR", "USER", "LANG", "LC_ALL"}

// buildEnv returns the allowlisted child environment: the base set plus
// the extra parent env var NAMES, each copied from the parent environment
// when set. Nothing else leaks. Duplicate names collapse to one entry.
func buildEnv(extra []string) []string {
	names := make([]string, 0, len(baseEnvNames)+len(extra))
	names = append(names, baseEnvNames...)
	names = append(names, extra...)
	seen := make(map[string]bool, len(names))
	env := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// streamParser is one adapter's stdout-line parser — a pure state machine
// fed by the shared runner. line classifies one raw line (newline
// stripped; oversized forces "unknown") and updates parser state; outcome
// classifies a run the control plane did NOT kill (rule 0 ran first in the
// runner); harvest reports what the stream claimed about cost, turns, and
// session identity.
type streamParser interface {
	line(raw []byte, oversized bool) string
	outcome(exitCode int) string
	harvest() (cost object.RunCost, numTurns int, sessionID string)
}

// procRun is the shared subprocess Run implementation behind claude-code
// and codex: one process group, a wall-clock watchdog (AG-2), and a stdout
// tee into the transcript buffer ahead of any parsing (AG-3).
type procRun struct {
	cmd    *exec.Cmd
	parser streamParser
	events chan Event

	transcript bytes.Buffer // fed only via lw, under lw.mu
	stderr     syncBuffer
	lw         *lineWriter

	start    time.Time
	watchdog *time.Timer

	mu         sync.Mutex
	killReason string // pinned by the first killer (decision 8)
	exited     bool

	done   chan struct{} // closed when result is ready
	result *RunResult
}

// startProc spawns argv in spec.WorldDir under the shared runner. A spawn
// failure (missing binary) is CONFIG_ERROR evidence, not an error: err is
// reserved for specs the runner cannot even attempt (decision 10).
func startProc(ctx context.Context, spec RunSpec, argv []string, parser streamParser) (Run, error) {
	if spec.WorldDir == "" {
		return nil, errors.New("agent: empty WorldDir in run spec")
	}
	r := &procRun{
		parser: parser,
		events: make(chan Event, eventBuffer),
		done:   make(chan struct{}),
	}
	r.lw = &lineWriter{transcript: &r.transcript, parse: r.parseLine}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = spec.WorldDir
	cmd.Env = buildEnv(spec.Env)
	cmd.Stdin = nil // /dev/null: agents must never block on reads
	cmd.Stdout = r.lw
	cmd.Stderr = &r.stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	r.cmd = cmd

	r.start = time.Now()
	if err := cmd.Start(); err != nil {
		// The agent never ran (binary vanished between LookPath and exec,
		// PATH broken, …). Record the refusal as evidence so the race can
		// continue; the error text is the honest stderr record.
		r.finish(&RunResult{
			Outcome:  object.OutcomeConfigError,
			ExitCode: -1,
			Cost: object.RunCost{
				WallMS: time.Since(r.start).Milliseconds(),
				Source: CostSourceNone,
			},
			Stderr: []byte(err.Error()),
		})
		return r, nil
	}
	if spec.Budget.MaxWall > 0 {
		r.watchdog = time.AfterFunc(spec.Budget.MaxWall, func() { r.kill(KilledByWatchdog) })
	}
	go r.watchCtx(ctx)
	go r.run()
	return r, nil
}

// Events implements Run.
func (r *procRun) Events() <-chan Event { return r.events }

// Interrupt implements Run: TERM the group, KILL after the grace period.
// Idempotent; no-op after exit.
func (r *procRun) Interrupt() {
	select {
	case <-r.done: // already finished (spawn failures included)
	default:
		r.kill(KilledByInterrupt)
	}
}

// Wait implements Run. The error is reserved for evidence-collection
// failure; the subprocess runner always produces a RunResult.
func (r *procRun) Wait() (*RunResult, error) {
	<-r.done
	return r.result, nil
}

// parseLine is the lineWriter callback: classify the line and emit a
// best-effort event (dropped under a slow consumer, never blocking).
func (r *procRun) parseLine(raw []byte, oversized bool) {
	kind := r.parser.line(raw, oversized)
	select {
	case r.events <- Event{Kind: kind, Raw: json.RawMessage(bytes.Clone(raw))}:
	default:
	}
}

// watchCtx folds ctx termination into the kill path with the reason pinned
// from ctx.Err(): a deadline is a budget kill, a cancellation an interrupt
// (decision 8).
func (r *procRun) watchCtx(ctx context.Context) {
	select {
	case <-ctx.Done():
		reason := KilledByInterrupt
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			reason = KilledByWatchdog
		}
		r.kill(reason)
	case <-r.done:
	}
}

// kill pins the kill reason (first caller wins — the control plane's kill
// reason outranks anything the process says, decision 8) BEFORE signaling,
// then SIGTERMs the process group and SIGKILLs it after killGrace. No-op
// once the run has exited.
//
// The SIGKILL escalation is unconditional on the GROUP (decision 9:
// "SIGKILL to the pgid after a 5 s grace"): it must not be gated on
// r.exited, because cmd.Wait reaps the LEADER the moment it dies — the
// common case, since real CLIs exit promptly on SIGTERM — while a
// grandchild that traps or ignores SIGTERM would survive the budget kill
// forever and keep spending (AG-2). Signaling an already-dead group is a
// harmless ESRCH; the escalation timer is anchored at the moment SIGTERM
// was sent, which bounds the pid-reuse window to killGrace.
func (r *procRun) kill(reason string) {
	r.mu.Lock()
	if r.exited {
		r.mu.Unlock()
		return
	}
	if r.killReason == "" {
		r.killReason = reason
	}
	r.mu.Unlock()

	pid := r.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	time.AfterFunc(killGrace, func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	})
}

// run reaps the process and assembles the RunResult. Outcome precedence
// (rule 0): a control-plane kill outranks anything the stream said; only
// an unkilled run is classified by the adapter's mapping table.
func (r *procRun) run() {
	waitErr := r.cmd.Wait()
	wallMS := time.Since(r.start).Milliseconds()
	if r.watchdog != nil {
		r.watchdog.Stop()
	}
	r.mu.Lock()
	r.exited = true
	reason := r.killReason
	r.mu.Unlock()

	// waitErr needs no classification of its own: an ExitError is evidence
	// (exit code below), and ErrWaitDelay means an escaped pipe holder was
	// abandoned after the transcript was captured best-effort.
	_ = waitErr

	exitCode := -1 // -1 when signaled (ProcessState reports it) or never ran
	if state := r.cmd.ProcessState; state != nil {
		exitCode = state.ExitCode()
	}

	var cost object.RunCost
	var turns int
	var session string
	var transcript []byte
	// Flush the final unterminated line and snapshot parser state and the
	// transcript under the same lock the stream writer uses.
	r.lw.finish(func() {
		cost, turns, session = r.parser.harvest()
		transcript = bytes.Clone(r.transcript.Bytes())
	})
	cost.WallMS = wallMS

	var outcome string
	switch reason {
	case KilledByWatchdog:
		outcome = object.OutcomeBudgetExceeded
	case KilledByInterrupt:
		outcome = object.OutcomeInterrupted
	default:
		outcome = r.parser.outcome(exitCode)
	}

	r.finish(&RunResult{
		Outcome:    outcome,
		ExitCode:   exitCode,
		KilledBy:   reason,
		Cost:       cost,
		NumTurns:   turns,
		SessionID:  session,
		Transcript: transcript,
		Stderr:     r.stderr.Snapshot(),
	})
}

// finish publishes the result exactly once: byte slices normalized to
// non-nil (an empty transcript is a real record — its CAS key is the
// well-known empty-bytes key), events closed, waiters released.
func (r *procRun) finish(res *RunResult) {
	if res.Transcript == nil {
		res.Transcript = []byte{}
	}
	if res.Stderr == nil {
		res.Stderr = []byte{}
	}
	r.result = res
	close(r.events)
	close(r.done)
}

// lineWriter tees every stdout byte into the transcript (AG-3) BEFORE
// splitting the stream into newline-terminated lines for parsing. Lines
// beyond maxLineBytes are truncated for parsing only and flagged oversized
// (one "unknown" event); the transcript keeps every byte.
type lineWriter struct {
	mu         sync.Mutex
	transcript *bytes.Buffer
	parse      func(raw []byte, oversized bool)
	cur        bytes.Buffer
	oversized  bool
}

// Write implements io.Writer for the exec stdout copier. It never fails:
// evidence capture must not turn into a pipe error.
func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.transcript.Write(p)
	rest := p
	for {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			w.accumulate(rest)
			return len(p), nil
		}
		w.accumulate(rest[:i])
		w.emit()
		rest = rest[i+1:]
	}
}

func (w *lineWriter) accumulate(b []byte) {
	if room := maxLineBytes - w.cur.Len(); len(b) > room {
		if room < 0 {
			room = 0
		}
		b = b[:room]
		w.oversized = true
	}
	w.cur.Write(b)
}

func (w *lineWriter) emit() {
	w.parse(w.cur.Bytes(), w.oversized)
	w.cur.Reset()
	w.oversized = false
}

// finish flushes a trailing unterminated line, then runs f under the
// writer lock so callers can snapshot parser state race-free.
func (w *lineWriter) finish(f func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur.Len() > 0 || w.oversized {
		w.emit()
	}
	f()
}

// syncBuffer is a mutex-guarded bytes.Buffer for stderr capture: the exec
// copier may outlive Wait by a moment when WaitDelay abandons an escaped
// pipe holder.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Snapshot() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}
