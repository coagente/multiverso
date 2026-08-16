package main

// M2a: the behaviour block's rendering. The escalation sentence tells a
// maintainer THAT the candidates disagree; this block is what tells them
// ON WHAT, and it is the only reason a behavioural ESCALATE is actionable.

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteBehaviorRendersTheDistinguishingCase(t *testing.T) {
	var buf bytes.Buffer
	writeBehavior(&buf, &explainBehavior{
		Corpus: "mv0:3ab1234567890abcdef", Provider: "declared", CasesCompared: 4, CasesIncomparable: 0, CohortN: 2,
		Classes: []explainBehaviorClass{
			{ID: "mv0:aa1111111111", Members: []string{"mv0:aa1111111111"}, AgreesWithBase: true},
			{ID: "mv0:bb2222222222", Members: []string{"mv0:bb2222222222"}, AgreesWithBase: false},
		},
		Distinguishing: []explainDistinguishing{{
			Case: "c0001", Target: "stats:clamp", Call: "stats:clamp(nan, 0, 10)", Base: "nan",
			Observations: []explainBehaviorAnswer{
				{World: "mv0:aa1111111111", Value: "nan"},
				{World: "mv0:bb2222222222", Value: "0"},
			},
		}},
	})
	out := buf.String()
	for _, want := range []string{
		"behavior (corpus mv0:3ab12345…, provider declared, 4 cases compared, 0 incomparable)",
		"CLASS", "vs BASE", "same", "changed",
		"distinguishing cases (2 classes)",
		"c0001  stats:clamp(nan, 0, 10)",
		"base         → nan",
		"→ 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the behaviour block does not contain %q:\n%s", want, out)
		}
	}
	// The block reports evidence, never a verdict: no world is a winner and
	// nothing here says which behaviour is right.
	for _, forbidden := range []string{"WINNER", " won ", "correct", "wrong"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the behaviour block claims %q; a difference is evidence of AMBIGUITY, not of correctness:\n%s", forbidden, out)
		}
	}
}

// A comparison of one is not a comparison, and the rendering says so out
// loud rather than printing a one-row class table that reads like a result.
func TestWriteBehaviorCohortOfOne(t *testing.T) {
	var buf bytes.Buffer
	writeBehavior(&buf, &explainBehavior{Corpus: "mv0:3ab", CohortN: 1})
	out := buf.String()
	if !strings.Contains(out, "a comparison of one is not a comparison") {
		t.Errorf("cohort-of-one rendering = %q", out)
	}
	if strings.Contains(out, "CLASS") {
		t.Errorf("a cohort of one rendered a class table:\n%s", out)
	}
}

// Under every policy that declares no differential — which is every policy
// predating M2a — the block is absent and the report is unchanged.
func TestWriteBehaviorAbsentIsSilent(t *testing.T) {
	var buf bytes.Buffer
	writeBehavior(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("a report with no differential printed %q", buf.String())
	}
}
