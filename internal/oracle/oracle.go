// Package oracle implements the verification plane (EP-1, EP-7): the
// Oracle interface, the closed registry of kinds a policy may declare, and
// their implementations — the generic CommandOracle plus the v0 Python
// ladder, O0 `pytest --collect-only` (collected-test counts, the exit-5
// guard) and O1 the suite gate (JUnit XML always; reportlog and coverage
// when the plugins are there). Since M1c the run surface is the world
// handle (PRD EP-1 is literally "run(world) → Receipt"): a string dir can
// no longer say where execution must happen.
//
// Two rules run through every kind. EP-7: each native artifact is read
// under a size cap and stored content-addressed BEFORE anything parses it,
// so the parsers never sit between the tool and the record. And evidence
// honesty: a metric whose structured source was unavailable is ABSENT from
// the receipt — never zero, never assumed — while result.tools names the
// sources that were actually available and used.
package oracle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// Receipt result statuses (receipt/v0 subset).
const (
	StatusPass  = "pass"
	StatusFail  = "fail"
	StatusError = "error"
)

const (
	recheckTier = "V1-replayable"
	// waitDelay bounds Wait after the group kill, in case a process
	// escaped the group and still holds the output pipes open.
	waitDelay = 5 * time.Second
)

// sizedCost pairs a wall time with the count its kind scales by, and it
// enforces object.Cost's documented invariant — `Unit == "" iff Units == 0`
// — at the one place every kind funnels through.
//
// The invariant matters because M2b fits `wall_ms ≈ fixed + per_unit ×
// units` over these pairs. A receipt that carries a named unit beside a zero
// count reads as "this rung ran and scaled by nothing", and it enters the
// least-squares fit at x = 0 — precisely the intercept a scheduler reads as
// the kind's FIXED cost. `{0, ""}` is the sentinel for "unknown" (M2a
// decision 22), and unknown is what a machinery path leaves behind.
func sizedCost(wallMS, units int64, unit string) object.Cost {
	if units == 0 {
		return object.Cost{WallMS: wallMS}
	}
	return object.Cost{WallMS: wallMS, Units: units, Unit: unit}
}

// Oracle produces evidence receipts for a world (EP-1).
type Oracle interface {
	ID() string      // the registry KIND: command | pytest-collect | pytest-suite
	Version() string // OUR contract version ("v0"); the TOOL's is in result.tools
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
	// Config is the resolved-config digest a compiled policy assigns this
	// instance (M1e decision 8). Empty means the M0 path — `mvo race
	// --oracle-cmd` under a v0 policy, where no policy names the oracle —
	// and Run then derives the digest from argv+timeout exactly as M0 did,
	// so v0 receipts keep digesting to the same bytes and old ledgers keep
	// replaying.
	Config string
}

// ID implements Oracle.
func (o *CommandOracle) ID() string { return KindCommand }

// Version implements Oracle.
func (o *CommandOracle) Version() string { return oracleVersion }

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
	cfgDig := o.Config
	if cfgDig == "" {
		dig, _, err := object.Digest(map[string]any{
			"argv":       o.Argv,
			"timeout_ms": o.Timeout.Milliseconds(),
		})
		if err != nil {
			return object.Receipt{}, fmt.Errorf("oracle: digest config: %w", err)
		}
		cfgDig = dig
	}

	runCtx := ctx
	cancel := func() {}
	if o.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, o.Timeout)
	}
	defer cancel()

	res := runInWorld(runCtx, w, o.Argv, nil)

	stdoutKey, err := o.CAS.Put(res.Stdout)
	if err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: store stdout: %w", err)
	}
	stderrKey, err := o.CAS.Put(res.Stderr)
	if err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: store stderr: %w", err)
	}

	return object.Receipt{
		Schema: object.SchemaReceipt,
		Oracle: object.OracleRef{ID: o.ID(), Version: o.Version(), Config: cfgDig},
		Execution: object.Execution{
			Argv:          append([]string(nil), o.Argv...),
			ExitCode:      res.ExitCode,
			DurationMS:    res.WallMS,
			IsolationTier: w.Tier(),
			IsolationCaps: w.Caps(),
		},
		// A command oracle parses nothing, so it emits NO metrics and
		// names NO structured sources — {} and {}, never null: the empty
		// map is "measured nothing", null is a lie about the shape of the
		// record (EP-2).
		Result:      object.NewResult(res.Status, stdoutKey, stderrKey),
		Freshness:   object.Freshness{Basis: object.BasisConstruction},
		RecheckTier: recheckTier,
		Family:      FamilySuite,
		// A command oracle parses nothing, so it knows no scaling unit:
		// {0, ""} is the honest record of "unknown", exactly as an absent
		// metric is (M2a decision 22).
		Cost: object.Cost{WallMS: res.WallMS},
		// {} rather than null: this kind is supplied no control-plane
		// inputs at all, which is a different statement from "the field is
		// missing" (M2a decision 24).
		Inputs:      object.NoInputs(),
		Correlation: policy.KindCorrelation(KindCommand),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}
