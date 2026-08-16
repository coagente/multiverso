package oracle

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// SchemaDiffTarget is the mutation target set's schema. The object is
// CONTROL-PLANE AUTHORED, content-addressed, and named by every mutation
// receipt in inputs["diff_target"], so a reader can check exactly what was
// in scope without trusting the number that came out (M2a decision 16).
const SchemaDiffTarget = "multiverso.dev/diff-target/v0"

// Reasons a file in the captured patch contributes no target lines. They
// are COUNTED rather than discarded silently: "mutation had nothing to say
// about this patch" and "mutation ignored half of this patch" are different
// facts, and a reader of the report must be able to tell them apart.
const (
	DropNonPython = "non_python"
	DropProtected = "protected"
	DropHarness   = "harness"
	DropDeleted   = "deleted"
	DropBinary    = "binary"
)

// LineRange is an inclusive, 1-based range of NEW-file lines.
type LineRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// TargetFile is one file's mutable line ranges, merged and sorted.
type TargetFile struct {
	Path   string      `json:"path"`
	Ranges []LineRange `json:"ranges"`
}

// TargetSet is the diff-scoped mutation target: the added and modified
// source lines of the AG-4 captured patch, minus everything mutation cannot
// speak about.
//
// It is derived by the CONTROL PLANE from bytes the control plane captured
// and hashed. The candidate chose the content but cannot change what the
// content IS (M1f threat-model point 1), which is why
// mutation_lines_targeted is one of the two metrics on this rung that no
// candidate can author in any regime.
type TargetSet struct {
	Files   []TargetFile     `json:"files"`
	Lines   int64            `json:"lines"`
	Dropped map[string]int64 `json:"dropped"`
}

// Empty reports whether the patch left nothing to mutate.
//
// An empty target set is NOT an error (M2a's O3 step 1): a patch that
// changed no mutable source line has nothing for mutation to say about it,
// and inventing a score for it would be the fabricated-zero failure this
// project exists to remove. The survivor gate passes vacuously and the
// receipt records mutants_candidates = 0 with mutation_score_bp ABSENT, so
// the reader can see the difference between "nothing to test" and
// "everything survived".
func (t TargetSet) Empty() bool { return t.Lines == 0 }

// Has reports whether one new-file line is in scope. The mutation adapter
// filters the tool's enumeration through it, so a tool that offers a mutant
// outside the diff never reaches the canonical order or the cap.
func (t TargetSet) Has(path string, line int64) bool {
	for _, f := range t.Files {
		if f.Path != path {
			continue
		}
		for _, r := range f.Ranges {
			if line >= r.Start && line <= r.End {
				return true
			}
		}
		return false
	}
	return false
}

// Paths returns the target paths, sorted — the `--paths-to-mutate` argument
// of every adapter.
func (t TargetSet) Paths() []string {
	out := make([]string, 0, len(t.Files))
	for _, f := range t.Files {
		out = append(out, f.Path)
	}
	return out
}

// Canonical returns the canonical object the digest is taken over.
func (t TargetSet) Canonical() map[string]any {
	files := make([]any, 0, len(t.Files))
	for _, f := range t.Files {
		ranges := make([]any, 0, len(f.Ranges))
		for _, r := range f.Ranges {
			ranges = append(ranges, []any{r.Start, r.End})
		}
		files = append(files, map[string]any{"path": f.Path, "ranges": ranges})
	}
	dropped := map[string]any{}
	for k, v := range t.Dropped {
		dropped[k] = v
	}
	return map[string]any{
		"schema":  SchemaDiffTarget,
		"files":   files,
		"lines":   t.Lines,
		"dropped": dropped,
	}
}

// Digest returns the target set's object digest and canonical bytes.
func (t TargetSet) Digest() (string, []byte, error) {
	dig, b, err := object.Digest(t.Canonical())
	if err != nil {
		return "", nil, fmt.Errorf("oracle: digest diff target: %w", err)
	}
	return dig, b, nil
}

// DiffTargets parses a unified diff (the AG-4 captured patch) into the
// mutation target set. It is PURE: no I/O, no clock, no tool — the recorded
// patches in testdata drive it directly.
//
// Exclusions, each counted:
//
//   - files matching paths.protected or paths.harness — mutating a test
//     file measures nothing, and mutating the harness measures less than
//     nothing (M2a decision 16);
//   - non-Python files, because no adapter on this menu mutates them;
//   - deletions, because there is no new-file line to mutate;
//   - binary hunks, because there are no lines at all.
//
// Renames are followed to the NEW path (`+++ b/<new>`), so a moved file's
// added lines stay in scope under the name they will land under.
func DiffTargets(patch []byte, paths policy.PathSet) TargetSet {
	set := TargetSet{Files: []TargetFile{}, Dropped: map[string]int64{}}
	lines := strings.Split(string(patch), "\n")

	var (
		newPath string // the +++ path of the file being read
		// headerPath is the `diff --git a/x b/y` name. A BINARY file has
		// no ---/+++ header at all, so without it a binary hunk would be
		// dropped silently — and "mutation ignored this file" must always
		// be a counted fact, never an omission.
		headerPath string
		hunkNew    int64   // the next new-file line number inside a hunk
		added      []int64 // added/modified new-file line numbers for newPath
		inHunk     bool
		files      = map[string][]int64{}
		order      []string
	)
	flush := func() {
		if newPath == "" || len(added) == 0 {
			return
		}
		if _, seen := files[newPath]; !seen {
			order = append(order, newPath)
		}
		files[newPath] = append(files[newPath], added...)
	}
	drop := func(reason string) { set.Dropped[reason]++ }

	for _, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			newPath, headerPath, added, inHunk = "", gitHeaderPath(line), nil, false
		case strings.HasPrefix(line, "GIT binary patch"),
			strings.HasPrefix(line, "Binary files "):
			// No lines to mutate, and the bytes are not source.
			flush()
			if newPath != "" || headerPath != "" {
				drop(DropBinary)
			}
			newPath, headerPath, added, inHunk = "", "", nil, false
		case strings.HasPrefix(line, "+++ "):
			inHunk = false
			target := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			target = stripTimestamp(target)
			if target == "/dev/null" {
				// A deletion: the file has no new-file lines at all.
				drop(DropDeleted)
				newPath = ""
				continue
			}
			path := stripPrefix(target)
			switch {
			case path == "":
				newPath = ""
			case !strings.HasSuffix(path, ".py"):
				drop(DropNonPython)
				newPath = ""
			case paths.Class(path) == policy.ClassHarness:
				drop(DropHarness)
				newPath = ""
			case paths.Class(path) == policy.ClassProtected:
				drop(DropProtected)
				newPath = ""
			default:
				newPath = path
			}
		case strings.HasPrefix(line, "@@"):
			start, ok := parseHunkNew(line)
			if !ok {
				inHunk = false
				continue
			}
			hunkNew, inHunk = start, true
		case !inHunk || newPath == "":
			// Headers, index lines, mode changes: nothing to count.
		case strings.HasPrefix(line, "+"):
			added = append(added, hunkNew)
			hunkNew++
		case strings.HasPrefix(line, "-"):
			// A removed line occupies no new-file line number. A modified
			// line arrives as a -/+ pair, so its + half is already counted.
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" — a note, not a line.
		case line == "" || strings.HasPrefix(line, " "):
			hunkNew++
		default:
			// Anything else ends the hunk: git's diff body is
			// space/plus/minus-prefixed, so an unprefixed line is the next
			// file's header or trailing junk.
			inHunk = false
		}
	}
	flush()

	sort.Strings(order)
	for _, path := range order {
		nums := files[path]
		sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
		ranges := mergeLines(nums)
		if len(ranges) == 0 {
			continue
		}
		set.Files = append(set.Files, TargetFile{Path: path, Ranges: ranges})
		for _, r := range ranges {
			set.Lines += r.End - r.Start + 1
		}
	}
	return set
}

// mergeLines folds a sorted, possibly repeating line list into inclusive
// ranges.
func mergeLines(nums []int64) []LineRange {
	out := []LineRange{}
	for _, n := range nums {
		if last := len(out) - 1; last >= 0 && n <= out[last].End+1 {
			if n > out[last].End {
				out[last].End = n
			}
			continue
		}
		out = append(out, LineRange{Start: n, End: n})
	}
	return out
}

// parseHunkNew reads the new-file start line out of `@@ -a,b +c,d @@`.
func parseHunkNew(line string) (int64, bool) {
	i := strings.Index(line, "+")
	if i < 0 {
		return 0, false
	}
	rest := line[i+1:]
	end := strings.IndexAny(rest, " ,")
	if end >= 0 {
		rest = rest[:end]
	}
	n, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	if n == 0 {
		// `+0,0` is an empty new file half; the next line is line 1.
		return 1, true
	}
	return n, true
}

// gitHeaderPath reads the NEW path out of `diff --git a/<old> b/<new>`. It
// is the only name a binary hunk ever carries, and it is used for nothing
// else: the ---/+++ pair is authoritative wherever it exists, because it is
// what git writes for a rename.
func gitHeaderPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	i := strings.Index(rest, " b/")
	if i < 0 {
		return ""
	}
	return stripPrefix(strings.TrimSpace(rest[i+1:]))
}

// stripPrefix removes git's a/ or b/ path prefix.
func stripPrefix(p string) string {
	for _, prefix := range []string{"a/", "b/"} {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			return rest
		}
	}
	return p
}

// stripTimestamp drops the tab-separated timestamp `diff -u` appends to a
// header path. git does not write one; other producers do, and a path with
// a timestamp glued to it matches no pattern in any path class.
func stripTimestamp(p string) string {
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		return p[:i]
	}
	return p
}
