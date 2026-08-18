package main

// M2d.1 decision 7 — THE REFUSAL, asserted at the level it is delivered.
//
// A run whose rule under test never fired must be REFUSED WITH A NAMED REASON
// rather than reported: exit 5, the banner, the recorded field that proves it,
// and NO METRIC LINE AT ALL. The last conjunct is the one that matters — M2d
// decision 1b's own shape is that the assertion is on the ABSENCE of a number,
// because that is the only way to test the rule.

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/eval"
	"github.com/coagente/multiverso/internal/schedule"
)

// captureStdout runs fn with stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

// vacuousManifest is M2b.2's own cell: a cold workspace, the unpriced
// fallback on every step, and therefore a comparison of `voc` against `voc`.
func vacuousManifest(t *testing.T) (eval.RunManifest, []eval.Row) {
	t.Helper()
	cold := schedule.Coverage(schedule.Trace{
		HasStarted: true,
		Started: schedule.Started{
			Selector:  schedule.SelectorNameVOC2,
			CostTable: []schedule.CostRow{{Kind: "pytest-suite", Basis: schedule.CostBasisDeclaredRank}},
		},
		Steps: []schedule.Step{{
			Step:        1,
			CommitBasis: schedule.CommitBasisUnpriced + "(pytest-collect,pytest-suite,tree-guard)",
			Considered: []schedule.Considered{{
				World: "mv0:a", Oracle: "guard", ScoreBasis: schedule.ScoreBasisRung,
				Admissible: true, Affordable: true, HardGate: true,
			}},
		}},
		HasFinished: true,
		Finished:    schedule.Finished{Stop: schedule.StopEmpty},
	})
	man := eval.RunManifest{
		Schema: eval.SchemaRun, Corpus: eval.CorpusLocalDerived, Version: eval.LocalVersion,
		Arms:          []string{eval.ArmAdaptive, eval.ArmFixedBudget},
		CanaryVerdict: eval.CanaryClean,
		CoverageByArm: map[string]schedule.CoverageReport{eval.ArmAdaptive: cold},
	}
	rows := []eval.Row{
		{Instance: "toyrepo-mean-A", Arm: eval.ArmAdaptive, Family: eval.FamilyGoldPresent,
			Policy: "default", CostRegime: "cold", Decision: "REJECT", Stable: true,
			Replicates: 3, ModalCount: 3, Avail: true, DStar: "SELECT",
			DStarWinnerLabel: eval.VerdictCorrect},
		{Instance: "toyrepo-mean-A", Arm: eval.ArmFixedBudget, Family: eval.FamilyGoldPresent,
			Policy: "default", CostRegime: "cold", Decision: "SELECT", Stable: true,
			Replicates: 3, ModalCount: 3, Avail: true, WinnerLabel: eval.VerdictCorrect,
			DStar: "SELECT", DStarWinnerLabel: eval.VerdictCorrect},
	}
	return man, rows
}

func TestAVacuousCellIsRefusedByNameAndPrintsNoMetricLine(t *testing.T) {
	man, rows := vacuousManifest(t)
	o := runOpts{common: &commonFlags{corpus: eval.CorpusLocalDerived, version: eval.LocalVersion}}
	var err error
	out := captureStdout(t, func() { err = o.finish(man, rows, t.TempDir()) })

	var ce codedError
	if !asCoded(err, &ce) || ce.code != exitVacuous {
		t.Fatalf("a vacuous cell returned %v, want exit %d", err, exitVacuous)
	}
	if !strings.Contains(out, "VACUOUS") || !strings.Contains(out, "NO VERDICT") {
		t.Errorf("the refusal printed no banner:\n%s", out)
	}
	// THE REASON IS NAMED FROM A RECORDED FIELD, not asserted.
	if !strings.Contains(out, schedule.CommitBasisUnpriced) || !strings.Contains(out, "pytest-suite") {
		t.Errorf("the refusal does not quote the recorded commit_basis and its kinds:\n%s", out)
	}
	if !strings.Contains(out, "--warmup auto") {
		t.Errorf("the refusal names no remedy:\n%s", out)
	}
	// NO METRIC LINE AT ALL. This is the assertion that actually tests the
	// rule: the tables exist, the rows are there, and none of it is printed.
	for _, forbidden := range []string{"TCAR", "FAR ", "FRR_reach", "paired "} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a vacuous cell printed %q:\n%s", forbidden, out)
		}
	}
	// And the coverage block IS printed — the refusal without its number
	// would be an assertion rather than a measurement.
	if !strings.Contains(out, "COVERAGE") || !strings.Contains(out, "W2") {
		t.Errorf("the coverage block with its per-witness breakdown is missing:\n%s", out)
	}
}

// THE ANTI-RUBBER-STAMP HALF. A gate that only ever refuses is M2b.2 F-9's
// rubber stamp, so the refusal must NOT fire on a cell whose rule did run —
// and `coverage > 0, divergence 0` is a MEASURED NULL that stays publishable.
func TestAnExercisedCellIsNotRefusedAndStillPrintsItsCoverage(t *testing.T) {
	man, rows := vacuousManifest(t)
	warm := schedule.Coverage(schedule.Trace{
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
	man.CoverageByArm[eval.ArmAdaptive] = warm
	o := runOpts{common: &commonFlags{corpus: eval.CorpusLocalDerived, version: eval.LocalVersion}}
	var err error
	out := captureStdout(t, func() { err = o.finish(man, rows, t.TempDir()) })
	if err != nil {
		t.Fatalf("an exercised cell was refused: %v", err)
	}
	if strings.Contains(out, "NO VERDICT") {
		t.Errorf("the refusal fired on an exercised cell:\n%s", out)
	}
	if !strings.Contains(out, "TCAR") {
		t.Errorf("an exercised cell printed no metric line:\n%s", out)
	}
	// PRINTED ALWAYS, INCLUDING AT 100 %: a number that appears only when it
	// is bad is a number nobody learns to read.
	if !strings.Contains(out, "COVERAGE") || !strings.Contains(out, "1 of 1 steps (100%)") {
		t.Errorf("a fully covered cell did not print its coverage:\n%s", out)
	}
	// DECISION 11: the cost regime is in the cell's OWN NAME, not a warning
	// above it.
	if !strings.Contains(out, "COLD-COST-TABLE") {
		t.Errorf("the cell is not captioned with its cost regime:\n%s", out)
	}
}

// --allow-vacuous prints the tables and exits 0, and it does NOT suppress the
// caption: a flag that silences a refusal must not also silence its reason.
func TestAllowVacuousStampsEveryTableItPrints(t *testing.T) {
	man, rows := vacuousManifest(t)
	man.AllowVacuous = true
	o := runOpts{common: &commonFlags{corpus: eval.CorpusLocalDerived, version: eval.LocalVersion},
		allowVacuous: true}
	var err error
	out := captureStdout(t, func() { err = o.finish(man, rows, t.TempDir()) })
	if err != nil {
		t.Fatalf("--allow-vacuous still exited non-zero: %v", err)
	}
	if !strings.Contains(out, "VACUOUS") {
		t.Errorf("--allow-vacuous suppressed the caption as well as the refusal:\n%s", out)
	}
	if !strings.Contains(out, "may not be quoted") {
		t.Errorf("the stamped tables carry no warning:\n%s", out)
	}
	if !strings.Contains(out, "TCAR") {
		t.Errorf("--allow-vacuous printed no tables at all:\n%s", out)
	}
}

// --warmup is parsed by the SAME function the runner uses, and a mistyped
// value is refused at usage rather than silently meaning `cold`.
func TestWarmupFlagIsParsedAndRefusedByName(t *testing.T) {
	bin := buildEvalBinary(t)
	home := t.TempDir()
	out, code := runEval(t, bin, home, "run", "--warmup", "occasionally", "--mvo", "/nonexistent")
	if code != exitUsage {
		t.Fatalf("a mistyped --warmup exited %d, want %d:\n%s", code, exitUsage, out)
	}
	if !strings.Contains(out, "--warmup") {
		t.Errorf("the refusal does not name the flag:\n%s", out)
	}
}
