package eval

// DERIVATION: determinism under a fixed seed; every operator either produces an
// APPLYING patch on the fixture or reports why; and the expectation census is
// REPORTED and never asserted.
//
// The last one is the point worth restating: there is no test in this file that
// asserts a mutant is wrong. A test that did would be decision 7's
// assumed-label bug in test form — it would encode this file's intentions as
// ground truth, which is exactly what the hidden oracle exists to replace.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const toyGold = `diff --git a/stats.py b/stats.py
index f206bcb..a1dd77c 100644
--- a/stats.py
+++ b/stats.py
@@ -5,7 +5,7 @@ def mean(values):
     """Return the arithmetic mean of a non-empty sequence."""
     if not values:
         raise ValueError("mean() of empty sequence")
-    return sum(values) / (len(values) - 1)
+    return sum(values) / len(values)


 def clamp(value, low, high):
`

const toyBase = `"""Tiny numeric helpers."""


def mean(values):
    """Return the arithmetic mean of a non-empty sequence."""
    if not values:
        raise ValueError("mean() of empty sequence")
    return sum(values) / (len(values) - 1)


def clamp(value, low, high):
    """Return value limited to the inclusive range [low, high]."""
    if low > high:
        raise ValueError("clamp() with low > high")
    return min(max(value, low), high)


def total(values):
    """Return the sum of a sequence."""
    return sum(values)
`

const twoHunkGold = `diff --git a/stats.py b/stats.py
--- a/stats.py
+++ b/stats.py
@@ -5,7 +5,7 @@ def mean(values):
     """Return the arithmetic mean of a non-empty sequence."""
     if not values:
         raise ValueError("mean() of empty sequence")
-    return sum(values) / (len(values) - 1)
+    return sum(values) / len(values)
@@ -14,7 +14,7 @@ def clamp(value, low, high):
     """Return value limited to the inclusive range [low, high]."""
     if low > high:
         raise ValueError("clamp() with low > high")
-    return min(max(value, low), high)
+    return min(max(value, low), high)  # normalized
`

func toyInput() DeriveInput {
	return DeriveInput{
		Gold: []byte(toyGold),
		Base: map[string]string{"stats.py": toyBase},
		Seed: 20260817,
	}
}

func TestDeriveIsDeterministicUnderAFixedSeed(t *testing.T) {
	a := DeriveAll(toyInput())
	b := DeriveAll(toyInput())
	if len(a) != len(b) || len(a) != len(Operators()) {
		t.Fatalf("derivation count moved: %d vs %d (operators: %d)", len(a), len(b), len(Operators()))
	}
	for i := range a {
		if a[i].Operator != b[i].Operator || a[i].Params != b[i].Params ||
			a[i].Applied != b[i].Applied || a[i].Reason != b[i].Reason ||
			string(a[i].Patch) != string(b[i].Patch) {
			t.Errorf("operator %s is not deterministic:\n%+v\n%+v", a[i].Operator, a[i], b[i])
		}
	}
	// A different seed may choose differently, but it must still be stable
	// with itself.
	in := toyInput()
	in.Seed = 99
	c1, c2 := DeriveAll(in), DeriveAll(in)
	for i := range c1 {
		if string(c1[i].Patch) != string(c2[i].Patch) {
			t.Errorf("operator %s is not deterministic at seed 99", c1[i].Operator)
		}
	}
}

func TestEveryOperatorAppliesOrSaysWhy(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stats.py"), []byte(toyBase), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "base"}} {
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, b)
		}
	}
	applied, declined := 0, 0
	for _, d := range DeriveAll(toyInput()) {
		if !d.Applied {
			declined++
			if d.Reason == "" {
				t.Errorf("operator %s declined with no reason", d.Operator)
			}
			if len(d.Patch) != 0 {
				t.Errorf("operator %s declined but produced a patch", d.Operator)
			}
			continue
		}
		applied++
		if d.Reason != "" {
			t.Errorf("operator %s applied AND gave a reason %q", d.Operator, d.Reason)
		}
		tree, err := applyAndTree(dir, d.Patch)
		if err != nil {
			t.Errorf("operator %s produced a patch that does not apply: %v\n%s", d.Operator, err, d.Patch)
			continue
		}
		if !strings.HasPrefix(tree, "git:") {
			t.Errorf("operator %s: result tree %q is not TreePrefix-ed", d.Operator, tree)
		}
	}
	if applied == 0 {
		t.Fatalf("no operator applied on the fixture: the population would be empty")
	}
	t.Logf("%d operator(s) applied, %d declined — and that ratio is DATA about the "+
		"population, not a warning: the diversity of S2 is a function of this operator list", applied, declined)
}

func TestRevertHunkNeedsTwoHunksAndSaysSo(t *testing.T) {
	one := Derive(OpRevertHunk, toyInput())
	if one.Applied {
		t.Errorf("revert-hunk applied to a one-hunk patch: reverting it would leave the empty patch, which is S5")
	}
	if !strings.Contains(one.Reason, "S5-null") {
		t.Errorf("the refusal does not explain itself: %q", one.Reason)
	}
	in := toyInput()
	in.Gold = []byte(twoHunkGold)
	two := Derive(OpRevertHunk, in)
	if !two.Applied {
		t.Fatalf("revert-hunk declined a two-hunk patch: %s", two.Reason)
	}
	p, err := ParsePatch(two.Patch)
	if err != nil {
		t.Fatalf("the reverted patch does not parse: %v\n%s", err, two.Patch)
	}
	hunks := 0
	for _, f := range p.Files {
		hunks += len(f.Hunks)
	}
	if hunks != 1 {
		t.Errorf("revert-hunk left %d hunks, want 1", hunks)
	}
}

func TestTransplantExpectationIsUnknownNeverIncorrect(t *testing.T) {
	// A foreign patch may not apply at all, and on a coincidentally similar
	// repository it might even be correct. Guessing is the assumed-label bug.
	in := toyInput()
	in.Foreign = []byte(toyGold)
	in.ForeignID = "other-instance"
	d := Derive(OpTransplantForeign, in)
	if d.Expected != ExpectUnknown {
		t.Errorf("transplant expectation = %q, want %q", d.Expected, ExpectUnknown)
	}
	if !d.Applied {
		t.Errorf("transplant declined a parseable foreign patch: %s", d.Reason)
	}
	// With no foreign patch it declines and names the reason.
	in.Foreign = nil
	d = Derive(OpTransplantForeign, in)
	if d.Applied || !strings.Contains(d.Reason, "one-instance corpus") {
		t.Errorf("a corpus with no foreign gold did not decline clearly: %+v", d)
	}
}

func TestDeriveIsTotalOnGarbage(t *testing.T) {
	for _, gold := range []string{"", "not a patch at all", "@@ -1 +1 @@\n", "diff --git a/x b/x\n"} {
		for _, op := range Operators() {
			d := Derive(op, DeriveInput{Gold: []byte(gold), Seed: 1})
			if d.Applied && len(d.Patch) == 0 {
				t.Errorf("operator %s on %q claims to have applied with no patch", op, gold)
			}
			if !d.Applied && d.Reason == "" {
				t.Errorf("operator %s on %q declined with no reason", op, gold)
			}
		}
	}
	if d := Derive("no-such-operator", toyInput()); d.Applied || d.Expected != ExpectUnknown {
		t.Errorf("an unknown operator did not decline with an unknown expectation: %+v", d)
	}
}

func TestPatchRoundTripAndHeaderRecount(t *testing.T) {
	p, err := ParsePatch([]byte(toyGold))
	if err != nil {
		t.Fatal(err)
	}
	// The renderer DROPS the index line (its blob hashes are invalidated by
	// any edit) and RECOMPUTES the hunk header from the lines, which is what
	// keeps a surgically edited patch appliable.
	out := string(p.Render())
	if strings.Contains(out, "index f206bcb") {
		t.Errorf("the renderer kept an index line whose hashes a derived patch invalidates:\n%s", out)
	}
	if !strings.Contains(out, "@@ -5,7 +5,7 @@") {
		t.Errorf("the recomputed hunk header is wrong:\n%s", out)
	}
	// Delete an added line: the new count must shrink by exactly one.
	for i, l := range p.Files[0].Hunks[0].Lines {
		if strings.HasPrefix(l, "+") {
			p.Files[0].Hunks[0].Lines = append(p.Files[0].Hunks[0].Lines[:i],
				p.Files[0].Hunks[0].Lines[i+1:]...)
			break
		}
	}
	if got := string(p.Render()); !strings.Contains(got, "@@ -5,7 +5,6 @@") {
		t.Errorf("the header was not recounted after an edit:\n%s", got)
	}
}

func TestStripTestHunksRecordsWhatItStripped(t *testing.T) {
	withTest := `diff --git a/stats.py b/stats.py
--- a/stats.py
+++ b/stats.py
@@ -1,1 +1,1 @@
-a
+b
diff --git a/test_stats.py b/test_stats.py
--- a/test_stats.py
+++ b/test_stats.py
@@ -1,1 +1,1 @@
-assert False
+assert True
`
	out, removed, err := StripTestHunks([]byte(withTest))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "test_stats.py") {
		t.Errorf("the stripped patch still touches a test file:\n%s", out)
	}
	if len(removed) != 1 || removed[0] != "test_stats.py" {
		t.Errorf("the strip was not recorded: %v", removed)
	}
	// A patch that touches ONLY tests cannot be stripped into a candidate:
	// gold with its test hunks still in would pass any suite by construction.
	onlyTests := `diff --git a/tests/test_x.py b/tests/test_x.py
--- a/tests/test_x.py
+++ b/tests/test_x.py
@@ -1,1 +1,1 @@
-a
+b
`
	if _, _, err := StripTestHunks([]byte(onlyTests)); err == nil {
		t.Errorf("stripping a tests-only patch produced a candidate")
	}
}

func TestIsTestPathIsDeliberatelyBroad(t *testing.T) {
	// A false positive strips a hunk gold needed and surfaces as
	// gold-fails-control, which is loud. A false negative leaves a test edit
	// in gold, which is silent and fatal.
	for _, p := range []string{
		"test_stats.py", "a/test_stats.py", "stats_test.py", "conftest.py",
		"tests/x.py", "src/test/y.py", "pkg/testing/z.py",
	} {
		if !IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = false", p)
		}
	}
	for _, p := range []string{"", "/dev/null", "stats.py", "src/latest.py", "contest.py"} {
		if IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = true", p)
		}
	}
}
