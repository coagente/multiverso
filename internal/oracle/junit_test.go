package oracle

import (
	"os"
	"path/filepath"
	"testing"
)

// readFixture reads a recorded oracle artifact. The parsers are pure
// functions over these bytes, so the parser tests need no Python and no
// plugins at all — the whole point of hash-and-store-then-parse (EP-7).
func readFixture(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join("..", "..", "testdata", "oracle", name))
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := readFixture(name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestParseJUnitGoldens(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		want    junitSummary
		passed  int64
		wantErr bool
	}{
		{
			// Bare <testsuite> root: pytest's own single-suite report.
			name:   "pass, bare testsuite root",
			file:   "junit-pass.xml",
			want:   junitSummary{total: 8, durationMS: 132},
			passed: 8,
		},
		{
			// <testsuites> root, one suite; passed is DERIVED (JUnit never
			// records passes) and time rounds half-up: 0.4567 s → 457 ms.
			name:   "failures, errors and skips",
			file:   "junit-fail.xml",
			want:   junitSummary{total: 8, failures: 2, errors: 1, skipped: 1, durationMS: 457},
			passed: 4,
		},
		{
			// Two <testsuite> elements under one root: the walk sums every
			// element, so a sharded report needs no separate code path.
			name:   "multiple suites under testsuites",
			file:   "junit-suites-root.xml",
			want:   junitSummary{total: 8, failures: 1, errors: 1, skipped: 1, durationMS: 1000},
			passed: 5,
		},
		{
			// Truncated mid-element: half a report is not evidence. The
			// caller records NO metrics rather than the partial sums.
			name:    "truncated xml",
			file:    "junit-malformed.xml",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJUnit(fixture(t, tt.file))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseJUnit: want error, got %+v", got)
				}
				if got != (junitSummary{}) {
					t.Errorf("parseJUnit returned %+v alongside an error; want the zero summary", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseJUnit: %v", err)
			}
			if got != tt.want {
				t.Errorf("summary = %+v, want %+v", got, tt.want)
			}
			if got.passed() != tt.passed {
				t.Errorf("passed = %d, want %d", got.passed(), tt.passed)
			}
		})
	}
}

func TestParseJUnitTable(t *testing.T) {
	tests := []struct {
		name    string
		xml     string
		want    junitSummary
		passed  int64
		wantErr bool
	}{
		{
			// Absent counters contribute 0: nothing recorded, nothing
			// invented.
			name:   "missing counter attributes",
			xml:    `<testsuite name="pytest" tests="3"/>`,
			want:   junitSummary{total: 3},
			passed: 3,
		},
		{
			name:   "no attributes at all",
			xml:    `<testsuite/>`,
			want:   junitSummary{},
			passed: 0,
		},
		{
			// Counters that do not add up never yield a negative pass
			// count — the weakest honest reading wins.
			name:   "counters exceed total",
			xml:    `<testsuite tests="2" failures="3"/>`,
			want:   junitSummary{total: 2, failures: 3},
			passed: 0,
		},
		{
			name:   "exponent time",
			xml:    `<testsuite tests="1" time="1.5e-3"/>`,
			want:   junitSummary{total: 1, durationMS: 2},
			passed: 1,
		},
		{
			name:    "non-integer counter",
			xml:     `<testsuite tests="many"/>`,
			wantErr: true,
		},
		{
			name:    "negative counter",
			xml:     `<testsuite tests="-3"/>`,
			wantErr: true,
		},
		{
			name:    "non-numeric time",
			xml:     `<testsuite tests="1" time="fast"/>`,
			wantErr: true,
		},
		{
			name:    "no testsuite element",
			xml:     `<?xml version="1.0"?><report><cases/></report>`,
			wantErr: true,
		},
		{
			name:    "empty input",
			xml:     ``,
			wantErr: true,
		},
		{
			name:    "unclosed element",
			xml:     `<testsuites><testsuite tests="1"></testsuites>`,
			wantErr: true,
		},
		{
			name:    "undeclared charset",
			xml:     `<?xml version="1.0" encoding="iso-8859-1"?><testsuite tests="1"/>`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJUnit([]byte(tt.xml))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseJUnit(%q): want error, got %+v", tt.xml, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseJUnit(%q): %v", tt.xml, err)
			}
			if got != tt.want {
				t.Errorf("summary = %+v, want %+v", got, tt.want)
			}
			if got.passed() != tt.passed {
				t.Errorf("passed = %d, want %d", got.passed(), tt.passed)
			}
		})
	}
}

func TestDecimalMS(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0", 0, true},
		{"1", 1000, true},
		{"0.1", 100, true},
		{"0.132", 132, true},
		{"0.4567", 457, true}, // half-up at the millisecond
		{"0.0005", 1, true},   // half-up at the millisecond
		{"0.0004", 0, true},   // and down below the half
		{"12.3456", 12346, true},
		{".5", 500, true},
		{"1e3", 1000000, true}, // exponent form: the only float path
		{"1.5e-3", 2, true},
		{"", 0, false},
		{"-1", 0, false},
		{"+1", 0, false},
		{"fast", 0, false},
		{"1.2.3", 0, false},
		{"9223372036854775807", 0, false}, // overflows milliseconds
	}
	for _, tt := range tests {
		got, ok := decimalMS(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("decimalMS(%q) = (%d, %v), want (%d, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
