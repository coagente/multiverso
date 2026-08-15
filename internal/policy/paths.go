package policy

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// The protected-additions tri-state (M1f decision 4). "" is the sentinel
// for the M1f default, never a Go zero-value boolean: a bool whose false
// is the weaker setting would make an old policy silently LESS checked the
// moment a field was added.
const (
	AdditionsAllow  = "allow"
	AdditionsRefuse = "refuse"
)

// KnownAdditions returns the legal protected_additions values, sorted.
func KnownAdditions() []string { return []string{AdditionsAllow, AdditionsRefuse} }

// PathClass names which frozen set a path belongs to.
const (
	ClassProtected = "protected"
	ClassHarness   = "harness"
)

// PathSet is the compiled form of one PathSpec: two ordered, validated
// pattern lists plus the additions policy. Matching is pure and allocates
// nothing beyond the segment split, so the guard can walk two whole trees
// through it.
type PathSet struct {
	Protected          []Pattern
	Harness            []Pattern
	ProtectedAdditions string // AdditionsAllow | AdditionsRefuse (never "")
}

// Empty reports whether the set declares no pattern at all — validation
// rule 12's test for a paths-unmodified gate with nothing to guard.
func (s PathSet) Empty() bool { return len(s.Protected) == 0 && len(s.Harness) == 0 }

// Class reports which frozen class a repo-root-relative, forward-slashed
// path belongs to, or "" for neither. Harness wins a tie: it is the
// strictly stronger seal, and a path in both sets must get the stronger
// treatment (a conftest.py under tests/ is a harness file first).
func (s PathSet) Class(p string) string {
	for _, pat := range s.Harness {
		if pat.Match(p) {
			return ClassHarness
		}
	}
	for _, pat := range s.Protected {
		if pat.Match(p) {
			return ClassProtected
		}
	}
	return ""
}

// Pattern is one compiled glob under the closed grammar of M1f decision 7,
// which is deliberately NOT a regexp: a policy is data that reaches the
// decision core, and a regexp engine is an unbounded execution surface
// with catastrophic-backtracking failure modes (the M1e decision 6
// argument, unchanged).
//
// The grammar, in full:
//   - patterns match repo-root-relative, forward-slashed paths;
//   - `**` matches zero or more WHOLE segments;
//   - `*`, `?` and `[...]` match within ONE segment (Go path.Match
//     semantics, applied per segment);
//   - no alternation, no negation, no anchors;
//   - a pattern with no `**` and no `/` matches ONLY at the repo root, so
//     `conftest.py` is root-only and `**/conftest.py` is what reaches
//     nested files — explicit, testable, and printed by `mvo policy show`.
type Pattern struct {
	Raw  string
	segs []string // "**" is a distinguished segment
}

// ParsePattern compiles one pattern, reporting the offending construct by
// name (validation rule 13). Every rejection names the pattern AND what is
// wrong with it: an authoring mistake must never be a silent no-match.
func ParsePattern(raw string) (Pattern, error) {
	switch {
	case raw == "":
		return Pattern{}, fmt.Errorf("empty pattern")
	case strings.HasPrefix(raw, "/"):
		return Pattern{}, fmt.Errorf("pattern %q: a leading %q is not part of the grammar (patterns are already repo-root-relative)", raw, "/")
	case strings.HasSuffix(raw, "/"):
		return Pattern{}, fmt.Errorf("pattern %q: a trailing %q is not part of the grammar (use %q to match a whole subtree)", raw, "/", raw+"**")
	case strings.Contains(raw, "\\"):
		return Pattern{}, fmt.Errorf("pattern %q: backslashes are not part of the grammar (paths are forward-slashed)", raw)
	case strings.Contains(raw, "{"), strings.Contains(raw, "}"):
		return Pattern{}, fmt.Errorf("pattern %q: brace alternation is not part of the grammar", raw)
	case strings.HasPrefix(raw, "!"):
		return Pattern{}, fmt.Errorf("pattern %q: negation is not part of the grammar", raw)
	}
	segs := strings.Split(raw, "/")
	for _, seg := range segs {
		switch {
		case seg == "":
			return Pattern{}, fmt.Errorf("pattern %q: empty path segment", raw)
		case seg == ".", seg == "..":
			return Pattern{}, fmt.Errorf("pattern %q: relative segment %q is not part of the grammar", raw, seg)
		case seg == "**":
			continue
		case strings.Contains(seg, "**"):
			return Pattern{}, fmt.Errorf("pattern %q: %q matches whole segments and must stand alone between slashes", raw, "**")
		}
		// path.Match validates the per-segment syntax (an unterminated
		// character class is ErrBadPattern) without ever executing it.
		if _, err := path.Match(seg, ""); err != nil {
			return Pattern{}, fmt.Errorf("pattern %q: segment %q: %v", raw, seg, err)
		}
	}
	return Pattern{Raw: raw, segs: segs}, nil
}

// Match reports whether p — repo-root-relative, forward-slashed — matches.
func (pat Pattern) Match(p string) bool {
	if p == "" || len(pat.segs) == 0 {
		return false
	}
	return matchSegs(pat.segs, strings.Split(p, "/"))
}

// matchSegs is the ordinary two-pointer glob walk with `**` backtracking.
// It is O(len(pat)*len(path)) in the worst case and cannot recurse without
// bound, which is the whole reason the grammar is closed.
func matchSegs(pats, segs []string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(segs) {
		switch {
		case pi < len(pats) && pats[pi] == "**":
			star, mark = pi, si
			pi++
		case pi < len(pats) && segMatch(pats[pi], segs[si]):
			pi++
			si++
		case star >= 0:
			// `**` consumes one more whole segment and we retry.
			mark++
			pi, si = star+1, mark
		default:
			return false
		}
	}
	for pi < len(pats) && pats[pi] == "**" {
		pi++
	}
	return pi == len(pats)
}

func segMatch(pat, seg string) bool {
	ok, err := path.Match(pat, seg)
	return err == nil && ok
}

// compilePaths validates and compiles a PathSpec. Patterns are sorted in
// canonical form; duplicates are dropped (a pattern listed twice seals
// nothing extra, and a policy file is data an operator diffs).
func compilePaths(protected, harness []string, additions string) (PathSet, error) {
	out := PathSet{ProtectedAdditions: additions}
	if out.ProtectedAdditions == "" {
		out.ProtectedAdditions = AdditionsAllow
	}
	var firstErr error
	compile := func(raw []string) []Pattern {
		list := append([]string(nil), raw...)
		sort.Strings(list)
		seen := make(map[string]bool, len(list))
		out := make([]Pattern, 0, len(list))
		for _, r := range list {
			if seen[r] {
				continue
			}
			seen[r] = true
			pat, err := ParsePattern(r)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			out = append(out, pat)
		}
		return out
	}
	out.Protected = compile(protected)
	out.Harness = compile(harness)
	return out, firstErr
}
