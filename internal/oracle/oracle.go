// Package oracle defines the Oracle interface and CommandOracle (EP-1,
// EP-7): a verifier that runs a command inside a world and emits a Receipt
// with its stdout/stderr stored in CAS as artifacts. Since M1c the run
// surface is the world handle (PRD EP-1 is literally "run(world) →
// Receipt"): a string dir can no longer say where execution must happen.
package oracle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/object"
)

// Receipt result statuses (receipt/v0 subset).
const (
	StatusPass  = "pass"
	StatusFail  = "fail"
	StatusError = "error"
)

const (
	recheckTier   = "V1-replayable"
	family        = "suite"
	freshnessBase = "construction"
	// waitDelay bounds Wait after the group kill, in case a process
	// escaped the group and still holds the output pipes open.
	waitDelay = 5 * time.Second
)

// Oracle produces evidence receipts for a world.
type Oracle interface {
	ID() string      // "command"
	Version() string // "v0"
	Run(ctx context.Context, w backend.World) (object.Receipt, error)
}

// CommandOracle runs Argv inside the world and maps the exit code to a
// receipt status: 0 → pass, non-zero → fail, timeout or spawn error →
// error. On timeout the world is killed (T1: docker kill, pid-namespace
// teardown) and then the whole host process group (EP-7), not just the
// leader.
//
// The receipt records the IN-WORLD argv (the verification command is the
// evidence; any docker exec wrapper is transport — M1c decision 12) and
// the world's tier and caps. Run fills every receipt field it can observe;
// World and Freshness.ValidFor are completed by the caller (the race
// orchestrator), which alone knows the world's object digest, tree, and
// env digests.
type CommandOracle struct {
	Argv    []string
	Timeout time.Duration
	CAS     *cas.Store
}

// ID implements Oracle.
func (o *CommandOracle) ID() string { return "command" }

// Version implements Oracle.
func (o *CommandOracle) Version() string { return "v0" }

// Run executes the command in the world and returns the receipt. A failing
// or timing-out command is evidence, not an error: the receipt records it
// and err stays nil. Non-nil err means the evidence itself could not be
// produced (bad config, CAS failure).
func (o *CommandOracle) Run(ctx context.Context, w backend.World) (object.Receipt, error) {
	if len(o.Argv) == 0 {
		return object.Receipt{}, errors.New("oracle: command oracle: empty argv")
	}
	if o.CAS == nil {
		return object.Receipt{}, errors.New("oracle: command oracle: nil CAS store")
	}
	if w == nil {
		return object.Receipt{}, errors.New("oracle: command oracle: nil world")
	}
	cfgDig, _, err := object.Digest(map[string]any{
		"argv":       o.Argv,
		"timeout_ms": o.Timeout.Milliseconds(),
	})
	if err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: digest config: %w", err)
	}

	runCtx := ctx
	cancel := func() {}
	if o.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, o.Timeout)
	}
	defer cancel()

	hostArgv, hostEnv := w.Command(o.Argv, nil)
	cmd := exec.CommandContext(runCtx, hostArgv[0], hostArgv[1:]...)
	cmd.Dir = w.Dir()
	cmd.Env = hostEnv // nil ⇒ inherit the ambient environment (exactly M0 for T0)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// EP-7 + M1c decision 5: on timeout, kill the world first (T1: docker
	// kill tears down the pid namespace — signaling the docker exec client
	// kills nothing inside the container), then the whole host process
	// group so no orphaned test runners survive.
	cmd.Cancel = func() error {
		_ = w.Kill()
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	wallMS := time.Since(start).Milliseconds()

	status, exitCode := StatusPass, 0
	switch {
	case runErr == nil:
	case runCtx.Err() != nil:
		// Timeout (or caller cancellation): the verdict is inconclusive.
		status, exitCode = StatusError, -1
	default:
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			status, exitCode = StatusFail, exitErr.ExitCode()
		} else {
			// Spawn error: the command never ran.
			status, exitCode = StatusError, -1
		}
	}

	stdoutKey, err := o.CAS.Put(stdout.Bytes())
	if err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: store stdout: %w", err)
	}
	stderrKey, err := o.CAS.Put(stderr.Bytes())
	if err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: store stderr: %w", err)
	}

	return object.Receipt{
		Schema: object.SchemaReceipt,
		Oracle: object.OracleRef{ID: o.ID(), Version: o.Version(), Config: cfgDig},
		Execution: object.Execution{
			Argv:          append([]string(nil), o.Argv...),
			ExitCode:      exitCode,
			DurationMS:    wallMS,
			IsolationTier: w.Tier(),
			IsolationCaps: w.Caps(),
		},
		Result:      object.Result{Status: status, Artifacts: []string{stdoutKey, stderrKey}},
		Freshness:   object.Freshness{Basis: freshnessBase},
		RecheckTier: recheckTier,
		Family:      family,
		Cost:        object.Cost{WallMS: wallMS},
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}
