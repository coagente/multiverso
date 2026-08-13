package agent

// M1c runner/world tests (no docker): the kill path routes through
// World.Kill before the host-group signaling, with the reason pinned
// first — proven with a fake-World spy.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/object"
)

// spyWorld wraps the T0 identity world and counts Kill calls.
type spyWorld struct {
	backend.World
	kills atomic.Int64
}

func newSpyWorld(dir string) *spyWorld {
	return &spyWorld{World: backend.HostDir(dir)}
}

func (w *spyWorld) Kill() error {
	w.kills.Add(1)
	return nil
}

var _ backend.World = (*spyWorld)(nil)

// The watchdog kill calls World.Kill and pins the reason first: the
// result reports BUDGET_EXCEEDED/killed-by-watchdog even though the
// fixture would have kept running (rule 0).
func TestRunnerWatchdogKillsWorld(t *testing.T) {
	restore := setKillGrace(200 * time.Millisecond)
	t.Cleanup(func() { setKillGrace(restore) })

	dir := t.TempDir()
	spy := newSpyWorld(dir)
	h := startFixture(t, context.Background(), "claude-code", "slow", RunSpec{
		WorldDir: dir,
		World:    spy,
		Prompt:   "p",
		Budget:   Budget{MaxWall: 150 * time.Millisecond},
	})
	res, err := h.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Outcome != object.OutcomeBudgetExceeded {
		t.Errorf("outcome = %q, want BUDGET_EXCEEDED", res.Outcome)
	}
	if res.KilledBy != KilledByWatchdog {
		t.Errorf("killed_by = %q, want watchdog (reason pinned before signaling)", res.KilledBy)
	}
	if spy.kills.Load() == 0 {
		t.Error("World.Kill was never called on the watchdog path")
	}
}

// Interrupt takes the same World.Kill-first path with the interrupt
// reason pinned.
func TestRunnerInterruptKillsWorld(t *testing.T) {
	restore := setKillGrace(200 * time.Millisecond)
	t.Cleanup(func() { setKillGrace(restore) })

	dir := t.TempDir()
	spy := newSpyWorld(dir)
	h := startFixture(t, context.Background(), "claude-code", "slow", RunSpec{
		WorldDir: dir,
		World:    spy,
		Prompt:   "p",
	})
	time.Sleep(100 * time.Millisecond) // let the fixture reach its hang
	h.Interrupt()
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
	if spy.kills.Load() == 0 {
		t.Error("World.Kill was never called on the interrupt path")
	}
}

// A nil RunSpec.World means the bare host: the runner behaves exactly as
// M1b (T0 identity world), and the tier fed to argv builders is T0.
func TestRunSpecNilWorldDefaultsToHost(t *testing.T) {
	spec := RunSpec{WorldDir: t.TempDir()}
	w := spec.world()
	if w.Tier() != object.TierT0Worktree {
		t.Errorf("nil-World tier = %q, want %q", w.Tier(), object.TierT0Worktree)
	}
	if w.Dir() != spec.WorldDir {
		t.Errorf("nil-World dir = %q, want %q", w.Dir(), spec.WorldDir)
	}
	// EnvDigest of the identity world is the T0 manifest (compile-level
	// check that the handle is a full backend.World).
	store, err := cas.Open(t.TempDir() + "/cas")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.EnvDigest(store); err != nil {
		t.Fatalf("EnvDigest: %v", err)
	}
}
