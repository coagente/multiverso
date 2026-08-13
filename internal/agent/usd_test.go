package agent

import "testing"

func TestParseUSDMicro(t *testing.T) {
	valid := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"1", 1_000_000},
		{"0.25", 250_000},
		{"0.0042", 4_200},
		{"0.000001", 1},
		{"12.5", 12_500_000},
		{"3.141592", 3_141_592},
		{"9223372036854", 9_223_372_036_854_000_000}, // near-max whole dollars
	}
	for _, tt := range valid {
		got, err := ParseUSDMicro(tt.in)
		if err != nil {
			t.Errorf("ParseUSDMicro(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseUSDMicro(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}

	invalid := []string{
		"", " ", "-1", "+1", "-0.5", "1.2345678", // 7 fractional digits
		"1e3", "1E-3", ".5", "1.", "0x10", "USD 1", "1,5", "NaN", "Inf",
		"9223372036854.775808", // overflows int64 micro-USD
	}
	for _, in := range invalid {
		if got, err := ParseUSDMicro(in); err == nil {
			t.Errorf("ParseUSDMicro(%q) = %d, want error", in, got)
		}
	}
}

func TestFormatUSDMicro(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{1, "0.000001"},
		{4_200, "0.0042"},
		{250_000, "0.25"},
		{1_000_000, "1"},
		{1_500_000, "1.5"},
		{123_456, "0.123456"},
		{12_000_001, "12.000001"},
	}
	for _, tt := range tests {
		if got := FormatUSDMicro(tt.in); got != tt.want {
			t.Errorf("FormatUSDMicro(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Round-trip law: ParseUSDMicro(FormatUSDMicro(m)) == m for all m ≥ 0.
func TestUSDMicroRoundTrip(t *testing.T) {
	cases := []int64{0, 1, 9, 10, 4200, 3142, 999_999, 1_000_000, 1_000_001,
		250_000, 123_456_789, 9_999_999_999_999}
	for _, m := range cases {
		s := FormatUSDMicro(m)
		got, err := ParseUSDMicro(s)
		if err != nil {
			t.Errorf("round-trip %d: ParseUSDMicro(%q): %v", m, s, err)
			continue
		}
		if got != m {
			t.Errorf("round-trip %d → %q → %d", m, s, got)
		}
	}
}

// Tool-REPORTED numbers are parsed leniently with half-up rounding at the
// 6th fractional digit; malformed or negative reports yield 0.
func TestParseReportedUSD(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"0.0031415", 3142}, // the design's half-up golden
		{"0.0042", 4200},
		{"1", 1_000_000},
		{"1.", 1_000_000}, // lenient trailing dot
		{".5", 500_000},   // lenient leading dot
		{"0.0000004", 0},  // rounds down
		{"0.0000005", 1},  // rounds half-up
		{"1e-3", 1000},    // exponent form falls back to float
		{"2.5E-1", 250_000},
		{"-0.5", 0},                  // negative: absence over garbage
		{"-1e3", 0},                  // negative exponent form
		{"junk", 0},                  // malformed
		{"", 0},                      // empty
		{"1.2.3", 0},                 // malformed
		{"+1", 0},                    // signs are not JSON numbers
		{"0x10", 0},                  // malformed
		{"1e400", 0},                 // overflow
		{"1 000", 0},                 // malformed
		{"12.500000499", 12_500_000}, // truncates then half-up on the 7th digit
	}
	for _, tt := range tests {
		if got := parseReportedUSD(tt.in); got != tt.want {
			t.Errorf("parseReportedUSD(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
