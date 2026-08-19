package main

// BLOCKER B4 — THE REFUSAL IS PER ARM AND PER CELL, AND IT IS NOT SATISFIABLE
// BY ONE STEP.
//
// The refusal used to be taken off a numerator merged over every race in the
// run. A hostile reviewer's probe merged 99 vacuous races with ONE race
// holding one exercised step and the harness printed `1 of 199 steps (0%)`
// and `vacuous=false` — a verdict reported at a printed zero, from the
// safeguard the block existed to build.
//
// So: the question is asked of every `<arm>|<instance>` cell, ANY cell at
// zero refuses the whole run, a cell whose coverage could not be computed is
// an ABSENCE and never a zero, and a nonzero numerator never prints as `0%`.

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/eval"
	"github.com/coagente/multiverso/internal/schedule"
)

// exercisedRace is one `voc2` race whose allocation DEPENDED on the rule: it
// committed a world, so the allowance is `reserve(...)` and not M2b's equal
// share.
func exercisedRace() schedule.CoverageReport {
	return schedule.Coverage(schedule.Trace{
		HasStarted: true,
		Started: schedule.Started{
			Selector:  schedule.SelectorNameVOC2,
			CostTable: []schedule.CostRow{{Kind: "pytest-suite", Basis: schedule.CostBasisFit, N: 6}},
		},
		Steps: []schedule.Step{{
			Step: 1, CommitBasis: schedule.CommitBasisReserved, Scarce: true,
			CommitSet: []string{"mv0:a"},
			Considered: []schedule.Considered{{
				World: "mv0:a", Oracle: "guard", ScoreBasis: schedule.ScoreBasisFinish,
				Admissible: true, Affordable: true, HardGate: true, AllowanceMS: 700,
			}},
		}},
		HasFinished: true,
		Finished:    schedule.Finished{Stop: schedule.StopBudget},
	})
}

// consultedButInertRace is BLOCKER B3's step raced: `commit_basis: reserved`
// on the wire, |C| = 0, nothing withheld, no gate lapsed, the queue head
// unmoved. The rule was asked and allocated exactly what M2b decision 8
// allocates.
func consultedButInertRace() schedule.CoverageReport {
	return schedule.Coverage(schedule.Trace{
		HasStarted: true,
		Started: schedule.Started{
			Selector:  schedule.SelectorNameVOC2,
			CostTable: []schedule.CostRow{{Kind: "pytest-suite", Basis: schedule.CostBasisFit, N: 6}},
		},
		Steps: []schedule.Step{{
			Step: 1, CommitBasis: schedule.CommitBasisReserved, Scarce: true,
			CommitSet: []string{}, UncommittedMS: 1000,
			Considered: []schedule.Considered{
				{World: "mv0:a", Oracle: "guard", ScoreBasis: schedule.ScoreBasisFinish,
					Admissible: true, Affordable: true, HardGate: true,
					ValueBP: 5000, CostMS: 100, FinishMS: 300, AllowanceMS: 500},
				{World: "mv0:b", Oracle: "guard", ScoreBasis: schedule.ScoreBasisFinish,
					Admissible: true, Affordable: true, HardGate: true,
					ValueBP: 5000, CostMS: 200, FinishMS: 900, AllowanceMS: 500},
			},
		}},
		HasFinished: true,
		Finished:    schedule.Finished{Stop: schedule.StopBudget},
	})
}

// cellManifest is a two-instance run of one adaptive rule, with the pooled
// per-arm figure computed exactly the way the runner computes it.
func cellManifest(a, b schedule.CoverageReport) (eval.RunManifest, []eval.Row) {
	const arm = eval.ArmAdaptive + "@voc2"
	man := eval.RunManifest{
		Schema: eval.SchemaRun, Corpus: eval.CorpusLocalDerived, Version: eval.LocalVersion,
		Arms:           []string{arm, eval.ArmFixedBudget},
		CanaryVerdict:  eval.CanaryClean,
		CoverageByArm:  map[string]schedule.CoverageReport{arm: schedule.MergeCoverage([]schedule.CoverageReport{a, b})},
		CoverageByCell: map[string]schedule.CoverageReport{},
	}
	man.CoverageByCell[coverageCellKey(arm, "toyrepo-mean-A")] = a
	man.CoverageByCell[coverageCellKey(arm, "toyrepo-mean-B")] = b
	var rows []eval.Row
	for _, inst := range []string{"toyrepo-mean-A", "toyrepo-mean-B"} {
		rows = append(rows,
			eval.Row{Instance: inst, Arm: arm, Family: eval.FamilyGoldPresent,
				Policy: "default", CostRegime: "warm", Decision: "REJECT", Stable: true,
				Replicates: 3, ModalCount: 3, Avail: true, DStar: "SELECT",
				DStarWinnerLabel: eval.VerdictCorrect},
			eval.Row{Instance: inst, Arm: eval.ArmFixedBudget, Family: eval.FamilyGoldPresent,
				Policy: "default", CostRegime: "warm", Decision: "SELECT", Stable: true,
				Replicates: 3, ModalCount: 3, Avail: true, WinnerLabel: eval.VerdictCorrect,
				DStar: "SELECT", DStarWinnerLabel: eval.VerdictCorrect})
	}
	return man, rows
}

func newFinishOpts() runOpts {
	return runOpts{common: &commonFlags{corpus: eval.CorpusLocalDerived, version: eval.LocalVersion}}
}

// THE PROBE ITSELF. One exercised cell and one cell in which the rule was
// consulted on every step and depended on by none. The pooled numerator is
// NONZERO and the run must still be refused.
func TestB4OneExercisedCellDoesNotRescueAVacuousOne(t *testing.T) {
	man, rows := cellManifest(exercisedRace(), consultedButInertRace())
	if man.CoverageByArm[eval.ArmAdaptive+"@voc2"].Exercised == 0 {
		t.Fatal("the pooled numerator is zero, so this test is not the probe it claims to be")
	}
	var err error
	out := captureStdout(t, func() { err = newFinishOpts().finish(man, rows, t.TempDir()) })

	var ce codedError
	if !asCoded(err, &ce) || ce.code != exitVacuous {
		t.Fatalf("BLOCKER B4: a run holding a vacuous cell returned %v, want exit %d", err, exitVacuous)
	}
	// THE CELL IS NAMED. "somewhere in this run the rule fired" is what the
	// pooled numerator used to say.
	if !strings.Contains(out, "toyrepo-mean-B") {
		t.Errorf("the refusal does not name the vacuous cell:\n%s", out)
	}
	if !strings.Contains(out, "NO VERDICT") {
		t.Errorf("the refusal printed no named non-verdict:\n%s", out)
	}
	// AND IT SAYS WHAT ACTUALLY HAPPENED. The rule ran on every step of that
	// cell; claiming it never fired would be a second false statement.
	if !strings.Contains(out, "CHANGED NOTHING IT ALLOCATED") {
		t.Errorf("the refusal misdescribes a consulted-but-inert cell:\n%s", out)
	}
	// NO METRIC LINE AT ALL, for any cell: a table whose rows were not all
	// measured is not a table.
	for _, forbidden := range []string{"TCAR", "FRR_reach", "paired "} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a run with a vacuous cell printed %q:\n%s", forbidden, out)
		}
	}
	if !strings.Contains(ce.msg, "toyrepo-mean-B") {
		t.Errorf("the exit message does not name the cell: %s", ce.msg)
	}
}

// THE ANTI-RUBBER-STAMP HALF. Every cell exercised means no refusal — a gate
// that only ever refuses is M2b.2 F-9's rubber stamp, and this one has to be
// capable of passing.
func TestB4EveryCellExercisedIsNotRefused(t *testing.T) {
	man, rows := cellManifest(exercisedRace(), exercisedRace())
	var err error
	out := captureStdout(t, func() { err = newFinishOpts().finish(man, rows, t.TempDir()) })
	if err != nil {
		t.Fatalf("a fully exercised run was refused: %v", err)
	}
	if strings.Contains(out, "NO VERDICT") {
		t.Errorf("the refusal fired on a run whose every cell was exercised:\n%s", out)
	}
	if !strings.Contains(out, "TCAR") {
		t.Errorf("a fully exercised run printed no metric line:\n%s", out)
	}
	// The per-cell figures are printed ALWAYS, including at 100 %.
	if !strings.Contains(out, "cell coverage [") {
		t.Errorf("the per-cell coverage block is missing:\n%s", out)
	}
}

// ABSENCE IS NOT ZERO. A cell whose coverage could not be computed — a
// pre-M2b.2 ledger, a ladder race, a race that recorded no trace — is
// reported as absent and does NOT refuse. Refusing to report a number you
// could not compute is not the same act as refusing a rule that changed
// nothing, and conflating them would fire the gate on every ladder race in
// the repository.
func TestB4ACellWithNoComputableCoverageIsAbsentAndNotVacuous(t *testing.T) {
	absent := schedule.Coverage(schedule.Trace{})
	if absent.Vacuous() {
		t.Fatal("a race with no recorded trace reported VACUOUS rather than absent")
	}
	man, rows := cellManifest(exercisedRace(), absent)
	var err error
	out := captureStdout(t, func() { err = newFinishOpts().finish(man, rows, t.TempDir()) })
	if err != nil {
		t.Fatalf("a cell with no computable coverage was refused as vacuous: %v", err)
	}
	if !strings.Contains(out, schedule.CoverageNoTrace) {
		t.Errorf("the absent cell did not report its absence by name:\n%s", out)
	}
	if strings.Contains(out, "toyrepo-mean-B: exercised 0 of 0 steps (0%)") {
		t.Errorf("an absent cell was printed as a measured zero:\n%s", out)
	}
}

// THE GATE IS PER ARM. A vacuous cell belonging to an arm that declares no
// allocation rule is not a claim about any rule, and gating on it would refuse
// every run that raced the fixed-budget ladder.
func TestB4OnlyAdaptiveCellsGateTheRun(t *testing.T) {
	man, rows := cellManifest(exercisedRace(), exercisedRace())
	man.CoverageByCell[coverageCellKey(eval.ArmFixedBudget, "toyrepo-mean-A")] = consultedButInertRace()
	var err error
	out := captureStdout(t, func() { err = newFinishOpts().finish(man, rows, t.TempDir()) })
	if err != nil {
		t.Fatalf("a non-adaptive cell refused the run: %v", err)
	}
	if !strings.Contains(out, "TCAR") {
		t.Errorf("the run printed no metric line:\n%s", out)
	}

	// And the same cell under the adaptive arm DOES refuse — the difference
	// is the arm and nothing else.
	man2, rows2 := cellManifest(exercisedRace(), exercisedRace())
	man2.CoverageByCell[coverageCellKey(eval.ArmAdaptive+"@voc2", "toyrepo-mean-C")] = consultedButInertRace()
	err = nil
	captureStdout(t, func() { err = newFinishOpts().finish(man2, rows2, t.TempDir()) })
	var ce codedError
	if !asCoded(err, &ce) || ce.code != exitVacuous {
		t.Fatalf("the same vacuous cell under the adaptive arm returned %v, want exit %d", err, exitVacuous)
	}
}

// THE REFUSAL MAY NOT MAKE A FALSE STATEMENT ABOUT THE POOLED FIGURE. A run
// refused by one cell still has a pooled numerator over the others, and
// printing "the rule ran on every step and changed nothing" above a pooled
// report whose own numerator is nonzero would be a permanently recorded false
// statement — the class of defect `inadmissibleReason` exists to prevent one
// layer down.
func TestB4ThePooledBannerIsNotPrintedOverANonVacuousPooledFigure(t *testing.T) {
	man, rows := cellManifest(exercisedRace(), consultedButInertRace())
	man.AllowVacuous = true
	o := newFinishOpts()
	o.allowVacuous = true
	var err error
	out := captureStdout(t, func() { err = o.finish(man, rows, t.TempDir()) })
	if err != nil {
		t.Fatalf("--allow-vacuous still exited non-zero: %v", err)
	}
	if strings.Contains(out, "VACUOUS (exercised 1 of 2 steps") {
		t.Errorf("the pooled banner claims a vacuity its own numerator contradicts:\n%s", out)
	}
	// The true sentence is still printed, and it names the cell.
	if !strings.Contains(out, "VACUOUS (1 cell(s)") || !strings.Contains(out, "toyrepo-mean-B") {
		t.Errorf("the per-cell non-verdict is missing:\n%s", out)
	}
	// The stamp survives: a flag that silences a refusal must not silence its
	// caption.
	if !strings.Contains(out, "may not be quoted") {
		t.Errorf("--allow-vacuous printed no stamp:\n%s", out)
	}
}
