package policy

import (
	"reflect"
	"strings"
	"testing"
)

// The closed glob grammar of M1f decision 7, one row per rule. It is
// deliberately NOT a regexp: a policy is data that reaches the decision
// core, and a regexp engine is an unbounded execution surface with
// catastrophic-backtracking failure modes.
func TestPatternGrammar(t *testing.T) {
	cases := []struct {
		pattern string
		match   []string
		miss    []string
	}{
		{
			// A pattern with no ** and no / matches ONLY at the repo root.
			// conftest.py is root-only; **/conftest.py is what reaches
			// nested files — explicit, testable, and surprising to nobody
			// who reads `mvo policy show`.
			pattern: "conftest.py",
			match:   []string{"conftest.py"},
			miss:    []string{"tests/conftest.py", "a/b/conftest.py"},
		},
		{
			pattern: "**/conftest.py",
			match:   []string{"conftest.py", "tests/conftest.py", "a/b/c/conftest.py"},
			miss:    []string{"conftest.pyc", "tests/conftest.py.bak"},
		},
		{
			// ** matches ZERO or more whole segments, which is what makes
			// src/**/conftest.py reach src/conftest.py. A trailing ** is
			// the same rule, so tests/** also matches a FILE named
			// `tests` — and that is the behaviour we want: a candidate
			// cannot replace the sealed test directory with a file of the
			// same name and call it untouched.
			pattern: "tests/**",
			match:   []string{"tests/a.py", "tests/unit/b.py", "tests/unit/deep/c.py", "tests"},
			miss:    []string{"testsuite/a.py", "src/tests/a.py"},
		},
		{
			// * matches within ONE segment and never crosses a slash.
			pattern: "src/*.py",
			match:   []string{"src/a.py", "src/billing.py"},
			miss:    []string{"src/pkg/a.py", "a.py"},
		},
		{
			pattern: "**/test_*.py",
			match:   []string{"test_a.py", "tests/test_b.py", "a/b/test_c.py"},
			miss:    []string{"tests/atest_b.py", "tests/test.py.orig"},
		},
		{
			pattern: "**/*_test.py",
			match:   []string{"a_test.py", "pkg/b_test.py"},
			miss:    []string{"test_a.py"},
		},
		{
			// [...] is a per-segment character class.
			pattern: "conf[ti]g.py",
			match:   []string{"config.py", "config.py"},
			miss:    []string{"confxg.py", "a/config.py"},
		},
		{
			// ? matches exactly one character, within one segment.
			pattern: "tox.in?",
			match:   []string{"tox.ini"},
			miss:    []string{"tox.in", "a/tox.ini"},
		},
		{
			pattern: "**/*.pth",
			match:   []string{"x.pth", "site/y.pth"},
			miss:    []string{"x.path"},
		},
		{
			// ** in the middle: zero or more segments between two anchors.
			pattern: "src/**/conftest.py",
			match:   []string{"src/conftest.py", "src/a/conftest.py", "src/a/b/conftest.py"},
			miss:    []string{"conftest.py", "lib/a/conftest.py"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			pat, err := ParsePattern(tc.pattern)
			if err != nil {
				t.Fatalf("ParsePattern(%q): %v", tc.pattern, err)
			}
			for _, p := range tc.match {
				if !pat.Match(p) {
					t.Errorf("%q does NOT match %q, want a match", tc.pattern, p)
				}
			}
			for _, p := range tc.miss {
				if pat.Match(p) {
					t.Errorf("%q matches %q, want no match", tc.pattern, p)
				}
			}
		})
	}
}

// Every construct outside the grammar is refused BY NAME at parse time. An
// authoring mistake must never be a silent no-match — the gate would then
// pass forever and nobody would learn why.
func TestPatternRejections(t *testing.T) {
	cases := []struct{ pattern, want string }{
		{"", "empty pattern"},
		{"/tests/a.py", "leading"},
		{"tests/", "trailing"},
		{"tests\\a.py", "backslashes"},
		{"{a,b}.py", "brace alternation"},
		{"!tests/a.py", "negation"},
		{"tests//a.py", "empty path segment"},
		{"./a.py", "relative segment"},
		{"../a.py", "relative segment"},
		{"te**sts/a.py", "must stand alone between slashes"},
		{"a[.py", "syntax error in pattern"},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			_, err := ParsePattern(tc.pattern)
			if err == nil {
				t.Fatalf("ParsePattern(%q) accepted an out-of-grammar pattern", tc.pattern)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to name %q", err, tc.want)
			}
		})
	}
}

// Harness wins a tie with protected: it is the strictly stronger seal, and
// a path in both sets must get the stronger treatment.
func TestPathSetClass(t *testing.T) {
	set, err := compilePaths(
		[]string{"tests/**", "**/test_*.py"},
		[]string{"**/conftest.py", "pyproject.toml"},
		"")
	if err != nil {
		t.Fatalf("compilePaths: %v", err)
	}
	if set.ProtectedAdditions != AdditionsAllow {
		t.Errorf("protected_additions = %q, want the compiled default %q", set.ProtectedAdditions, AdditionsAllow)
	}
	for path, want := range map[string]string{
		"tests/conftest.py":  ClassHarness, // in BOTH sets: the stronger wins
		"conftest.py":        ClassHarness,
		"pyproject.toml":     ClassHarness,
		"tests/test_a.py":    ClassProtected,
		"test_a.py":          ClassProtected,
		"billing.py":         "",
		"src/pyproject.toml": "", // root-only: a sub-package's is not the config
	} {
		if got := set.Class(path); got != want {
			t.Errorf("Class(%q) = %q, want %q", path, got, want)
		}
	}
}

// Patterns compile sorted and deduplicated: a policy file is data an
// operator diffs, and a pattern listed twice seals nothing extra.
func TestPathSetCanonicalOrder(t *testing.T) {
	set, err := compilePaths([]string{"tests/**", "**/test_*.py", "tests/**"}, nil, AdditionsRefuse)
	if err != nil {
		t.Fatalf("compilePaths: %v", err)
	}
	got := make([]string, 0, len(set.Protected))
	for _, p := range set.Protected {
		got = append(got, p.Raw)
	}
	if !reflect.DeepEqual(got, []string{"**/test_*.py", "tests/**"}) {
		t.Errorf("compiled protected = %v, want sorted and deduplicated", got)
	}
	if !set.Empty() && len(set.Harness) != 0 {
		t.Errorf("harness = %v, want empty", set.Harness)
	}
	if set.ProtectedAdditions != AdditionsRefuse {
		t.Errorf("protected_additions = %q, want it preserved", set.ProtectedAdditions)
	}
}
