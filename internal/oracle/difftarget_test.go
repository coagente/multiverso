package oracle

import (
	"reflect"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// testPaths is the shipped default's shape: tests protected, harness
// frozen. The mutation target set is derived THROUGH it, so a patch that
// edits a test file contributes no mutants.
func testPaths(t *testing.T) policy.PathSet {
	t.Helper()
	pol := policy.Default()
	compiled, err := policy.Compile("mv0:test", pol)
	if err != nil {
		t.Fatalf("compile default policy: %v", err)
	}
	return compiled.Paths
}

const patchTwoFiles = `diff --git a/stats.py b/stats.py
index 1111111..2222222 100644
--- a/stats.py
+++ b/stats.py
@@ -7,6 +7,9 @@ def mean(xs):
     return sum(xs) / len(xs)


+def clamp(v, lo, hi):
+    return min(max(v, lo), hi)
+
 def total(xs):
     return sum(xs)
diff --git a/util.py b/util.py
index 3333333..4444444 100644
--- a/util.py
+++ b/util.py
@@ -1,3 +1,3 @@
 def ok():
-    return 0
+    return 1
`

func TestDiffTargetsAddedAndModifiedLines(t *testing.T) {
	set := DiffTargets([]byte(patchTwoFiles), testPaths(t))
	want := []TargetFile{
		{Path: "stats.py", Ranges: []LineRange{{Start: 10, End: 12}}},
		{Path: "util.py", Ranges: []LineRange{{Start: 2, End: 2}}},
	}
	if !reflect.DeepEqual(set.Files, want) {
		t.Errorf("files = %+v, want %+v", set.Files, want)
	}
	if set.Lines != 4 {
		t.Errorf("lines = %d, want 4", set.Lines)
	}
	// A modified line is the + half of a -/+ pair, so it is IN scope: the
	// candidate wrote that line, and mutation asks whether the tests
	// noticed.
	if !set.Has("util.py", 2) {
		t.Error("the modified line util.py:2 is not in the target set")
	}
	if set.Has("util.py", 3) {
		t.Error("an untouched line is in the target set: the candidate's neighbours' code is not its denominator")
	}
}

// The exclusions, each COUNTED. A patch that only edits tests has an empty
// target set, which is the honest correction to M1f's table: mutation says
// nothing about assertion weakening, and pretending otherwise was the
// over-claim.
func TestDiffTargetsExclusions(t *testing.T) {
	const patch = `diff --git a/tests/test_stats.py b/tests/test_stats.py
--- a/tests/test_stats.py
+++ b/tests/test_stats.py
@@ -1,2 +1,2 @@
 def test_x():
-    assert clamp(5, 0, 10) == 5
+    assert True
diff --git a/conftest.py b/conftest.py
--- /dev/null
+++ b/conftest.py
@@ -0,0 +1,2 @@
+import sys
+sys.path.insert(0, ".")
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1 +1,2 @@
 # repo
+more words
diff --git a/gone.py b/gone.py
--- a/gone.py
+++ /dev/null
@@ -1,2 +0,0 @@
-def gone():
-    pass
diff --git a/logo.png b/logo.png
index 5555555..6666666 100644
Binary files a/logo.png and b/logo.png differ
`
	set := DiffTargets([]byte(patch), testPaths(t))
	if !set.Empty() {
		t.Fatalf("target set = %+v, want empty", set)
	}
	want := map[string]int64{
		DropProtected: 1, // tests/test_stats.py
		DropHarness:   1, // conftest.py
		DropNonPython: 1, // README.md
		DropDeleted:   1, // gone.py
		DropBinary:    1, // logo.png, which has no ---/+++ header at all
	}
	if !reflect.DeepEqual(set.Dropped, want) {
		t.Errorf("dropped = %v, want %v", set.Dropped, want)
	}
}

// A rename follows the NEW path: the moved file's added lines stay in scope
// under the name they will land under.
func TestDiffTargetsRename(t *testing.T) {
	const patch = `diff --git a/old.py b/new.py
similarity index 88%
rename from old.py
rename to new.py
--- a/old.py
+++ b/new.py
@@ -1,3 +1,4 @@
 def f():
     return 1
+# added

`
	set := DiffTargets([]byte(patch), testPaths(t))
	if len(set.Files) != 1 || set.Files[0].Path != "new.py" {
		t.Fatalf("files = %+v, want one entry for new.py", set.Files)
	}
	if !set.Has("new.py", 3) {
		t.Errorf("ranges = %+v, want the added line 3", set.Files[0].Ranges)
	}
}

// A new file is entirely in scope, and its hunk header is `+1` or `+0,0`.
func TestDiffTargetsNewFile(t *testing.T) {
	const patch = `diff --git a/added.py b/added.py
new file mode 100644
--- /dev/null
+++ b/added.py
@@ -0,0 +1,3 @@
+def g():
+    return 2
+
`
	set := DiffTargets([]byte(patch), testPaths(t))
	if set.Lines != 3 {
		t.Fatalf("lines = %d, want 3 (%+v)", set.Lines, set.Files)
	}
}

// The digest is what inputs["diff_target"] carries, so it must be stable
// and it must MOVE when the scope moves. A reader who cannot tell two
// target sets apart cannot check what was in scope.
func TestDiffTargetDigestIsStableAndDiscriminating(t *testing.T) {
	paths := testPaths(t)
	a := DiffTargets([]byte(patchTwoFiles), paths)
	digA, bytesA, err := a.Digest()
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	again, _, err := DiffTargets([]byte(patchTwoFiles), paths).Digest()
	if err != nil || again != digA {
		t.Errorf("digest = %s, want the stable %s (err %v)", again, digA, err)
	}
	if got := object.DigestBytes(bytesA); got != digA {
		t.Errorf("canonical bytes digest to %s, want %s", got, digA)
	}
	b := DiffTargets([]byte(patchTwoFiles+`diff --git a/extra.py b/extra.py
--- a/extra.py
+++ b/extra.py
@@ -1 +1,2 @@
 x = 1
+y = 2
`), paths)
	digB, _, err := b.Digest()
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if digA == digB {
		t.Error("two different target sets share a digest: inputs[\"diff_target\"] would not say what was in scope")
	}
}

// An empty patch is an empty target set — and an empty target set is not an
// error. The distinction that IS an error is a patch that was never
// supplied, which New refuses at construction.
func TestDiffTargetsEmptyPatch(t *testing.T) {
	set := DiffTargets(nil, testPaths(t))
	if !set.Empty() || len(set.Files) != 0 {
		t.Errorf("empty patch produced %+v", set)
	}
	if _, _, err := set.Digest(); err != nil {
		t.Errorf("an empty target set must still digest: %v", err)
	}
}
