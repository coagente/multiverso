package oracle

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/object"
)

func newOracle(t *testing.T, argv []string, timeout time.Duration) *CommandOracle {
	t.Helper()
	store, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	return &CommandOracle{Argv: argv, Timeout: timeout, CAS: store}
}

func TestRunStatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		argv       []string
		wantStatus string
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "exit 0 is pass",
			argv:       []string{"/bin/sh", "-c", "echo out; echo err 1>&2"},
			wantStatus: StatusPass,
			wantExit:   0,
			wantStdout: "out\n",
			wantStderr: "err\n",
		},
		{
			name:       "non-zero exit is fail",
			argv:       []string{"/bin/sh", "-c", "echo boom; exit 3"},
			wantStatus: StatusFail,
			wantExit:   3,
			wantStdout: "boom\n",
			wantStderr: "",
		},
		{
			name:       "spawn error is error",
			argv:       []string{"/nonexistent/mvo-no-such-binary"},
			wantStatus: StatusError,
			wantExit:   -1,
			wantStdout: "",
			wantStderr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newOracle(t, tt.argv, 30*time.Second)
			rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if rec.Result.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", rec.Result.Status, tt.wantStatus)
			}
			if rec.Execution.ExitCode != tt.wantExit {
				t.Errorf("exit_code = %d, want %d", rec.Execution.ExitCode, tt.wantExit)
			}
			if len(rec.Result.Artifacts) != 2 {
				t.Fatalf("artifacts = %v, want [stdout stderr]", rec.Result.Artifacts)
			}
			gotOut, err := o.CAS.Get(rec.Result.Artifacts[0])
			if err != nil {
				t.Fatalf("get stdout artifact: %v", err)
			}
			if string(gotOut) != tt.wantStdout {
				t.Errorf("stdout artifact = %q, want %q", gotOut, tt.wantStdout)
			}
			gotErr, err := o.CAS.Get(rec.Result.Artifacts[1])
			if err != nil {
				t.Fatalf("get stderr artifact: %v", err)
			}
			if string(gotErr) != tt.wantStderr {
				t.Errorf("stderr artifact = %q, want %q", gotErr, tt.wantStderr)
			}
		})
	}
}

func TestRunReceiptShape(t *testing.T) {
	o := newOracle(t, []string{"/bin/sh", "-c", "true"}, time.Second)
	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Schema != object.SchemaReceipt {
		t.Errorf("schema = %q, want %q", rec.Schema, object.SchemaReceipt)
	}
	if rec.Oracle.ID != "command" || rec.Oracle.Version != "v0" {
		t.Errorf("oracle ref = %+v, want id command version v0", rec.Oracle)
	}
	if got, want := rec.Oracle.Config[:len(object.DigestPrefix)], object.DigestPrefix; got != want {
		t.Errorf("oracle config = %q, want %q prefix", rec.Oracle.Config, want)
	}
	if rec.Execution.IsolationTier != object.TierT0Worktree {
		t.Errorf("isolation_tier = %q, want %q (from the world handle)", rec.Execution.IsolationTier, object.TierT0Worktree)
	}
	// M1c: tier and caps come from the world handle, never from a package
	// constant — a T0 receipt carries the honest uncapped-bare-host record.
	if rec.Execution.IsolationCaps != object.HostCaps() {
		t.Errorf("isolation_caps = %+v, want HostCaps %+v", rec.Execution.IsolationCaps, object.HostCaps())
	}
	if rec.Family != "suite" {
		t.Errorf("family = %q, want suite", rec.Family)
	}
	if rec.RecheckTier != "V1-replayable" {
		t.Errorf("recheck_tier = %q, want V1-replayable", rec.RecheckTier)
	}
	if rec.Freshness.Basis != "construction" {
		t.Errorf("freshness basis = %q, want construction", rec.Freshness.Basis)
	}
	// World and ValidFor belong to the orchestrator, which knows the
	// world digest and tree; the oracle must leave them empty.
	if rec.World != "" || rec.Freshness.ValidFor.Tree != "" || rec.Freshness.ValidFor.Env != "" {
		t.Errorf("world/valid_for pre-filled: %q %+v", rec.World, rec.Freshness.ValidFor)
	}
	if rec.Execution.DurationMS < 0 || rec.Cost.WallMS != rec.Execution.DurationMS {
		t.Errorf("duration_ms = %d, wall_ms = %d; want equal and non-negative",
			rec.Execution.DurationMS, rec.Cost.WallMS)
	}
	if _, err := time.Parse(time.RFC3339, rec.CreatedAt); err != nil {
		t.Errorf("created_at %q is not RFC3339: %v", rec.CreatedAt, err)
	}
}

// A timed-out command must take its whole process group with it: the shell
// leaves a background child holding the stdout pipe, so Run only returns
// promptly if the kill reaches the group, not just the leader.
func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	o := newOracle(t, []string{"/bin/sh", "-c", "sleep 30 & sleep 30"}, 200*time.Millisecond)
	start := time.Now()
	rec, err := o.Run(context.Background(), backend.HostDir(t.TempDir()))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Status != StatusError {
		t.Errorf("status = %q, want %q on timeout", rec.Result.Status, StatusError)
	}
	if rec.Execution.ExitCode != -1 {
		t.Errorf("exit_code = %d, want -1 on timeout", rec.Execution.ExitCode)
	}
	if elapsed > 3*time.Second {
		t.Errorf("Run took %v; process group not killed on timeout", elapsed)
	}
}

func TestRunConfigDigest(t *testing.T) {
	a := newOracle(t, []string{"true"}, time.Second)
	b := newOracle(t, []string{"true"}, time.Second)
	c := newOracle(t, []string{"true"}, 2*time.Second)
	ra, err := a.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run a: %v", err)
	}
	rb, err := b.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run b: %v", err)
	}
	rc, err := c.Run(context.Background(), backend.HostDir(t.TempDir()))
	if err != nil {
		t.Fatalf("Run c: %v", err)
	}
	if ra.Oracle.Config != rb.Oracle.Config {
		t.Errorf("same argv+timeout produced different configs: %q vs %q", ra.Oracle.Config, rb.Oracle.Config)
	}
	if ra.Oracle.Config == rc.Oracle.Config {
		t.Errorf("different timeout produced same config %q", ra.Oracle.Config)
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name   string
		oracle *CommandOracle
	}{
		{"empty argv", &CommandOracle{CAS: newOracle(t, []string{"true"}, 0).CAS}},
		{"nil cas", &CommandOracle{Argv: []string{"true"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.oracle.Run(context.Background(), backend.HostDir(t.TempDir())); err == nil {
				t.Fatal("Run: want error, got nil")
			}
		})
	}
	t.Run("nil world", func(t *testing.T) {
		o := newOracle(t, []string{"true"}, time.Second)
		if _, err := o.Run(context.Background(), nil); err == nil {
			t.Fatal("Run(nil world): want error, got nil")
		}
	})
}
