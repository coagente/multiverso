package oracle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/coagente/multiverso/internal/backend"
)

// procResult is one in-world process execution: the captured output, M0's
// status mapping over the outcome, the raw exit code (never a boolean —
// research ch. 19: pytest's exit 5 is only distinguishable from 1 if the
// number survives), and the wall time it took.
type procResult struct {
	Stdout   []byte
	Stderr   []byte
	Status   string // StatusPass | StatusFail | StatusError
	ExitCode int    // -1 for timeout and spawn failures
	WallMS   int64
}

// runInWorld executes argv inside w and maps the outcome onto M0's receipt
// status: 0 → pass, non-zero → fail, timeout or spawn error → error. The
// deadline is whatever ctx carries; on cancellation the world is killed
// first (T1: docker kill tears down the pid namespace — signaling the
// docker exec client kills nothing inside the container) and then the whole
// host process group (EP-7), so no orphaned test runner survives holding
// the output pipes.
//
// A failing or timing-out command is evidence, not an error: the verdict
// lands in the returned status, and only the caller decides whether the
// evidence could be recorded.
func runInWorld(ctx context.Context, w backend.World, argv, env []string) procResult {
	hostArgv, hostEnv := w.Command(argv, env)
	cmd := exec.CommandContext(ctx, hostArgv[0], hostArgv[1:]...)
	cmd.Dir = w.Dir()
	cmd.Env = hostEnv // nil ⇒ inherit the ambient environment (exactly M0 for T0)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	res := procResult{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
		Status: StatusPass,
		WallMS: time.Since(start).Milliseconds(),
	}
	switch {
	case runErr == nil:
	case ctx.Err() != nil:
		// Timeout (or caller cancellation): the verdict is inconclusive.
		res.Status, res.ExitCode = StatusError, -1
	default:
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			res.Status, res.ExitCode = StatusFail, exitErr.ExitCode()
		} else {
			// Spawn error: the command never ran.
			res.Status, res.ExitCode = StatusError, -1
		}
	}
	return res
}
