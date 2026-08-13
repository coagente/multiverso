package agent

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// microPerUSD is the micro-USD scale (decision 3): 1 USD = 1,000,000.
// Integer micro-USD makes Claude Code's smallest observed estimates
// (~10⁻⁴ USD) exactly representable and sums without overflow.
const microPerUSD = 1_000_000

// usdFlagRe is the strict CLI-flag grammar: signs, exponents, or more than
// 6 fractional digits are a usage error.
var usdFlagRe = regexp.MustCompile(`^\d+(\.\d{1,6})?$`)

// ParseUSDMicro parses a CLI-flag decimal ("0.25", "1", "0.0042") into
// micro-USD. Strict: ^\d+(\.\d{1,6})?$. Integer arithmetic only — no
// float64 anywhere on this path (DP-1 discipline).
func ParseUSDMicro(s string) (int64, error) {
	if !usdFlagRe.MatchString(s) {
		return 0, fmt.Errorf("agent: invalid USD amount %q (want a plain decimal with at most 6 fractional digits)", s)
	}
	wholeStr, fracStr, _ := strings.Cut(s, ".")
	whole, err := strconv.ParseInt(wholeStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("agent: USD amount %q out of range", s)
	}
	frac, err := strconv.ParseInt(fracStr+strings.Repeat("0", 6-len(fracStr)), 10, 64)
	if err != nil { // unreachable given the regex; belt and braces
		return 0, fmt.Errorf("agent: USD amount %q out of range", s)
	}
	if whole > (math.MaxInt64-frac)/microPerUSD {
		return 0, fmt.Errorf("agent: USD amount %q overflows micro-USD", s)
	}
	return whole*microPerUSD + frac, nil
}

// FormatUSDMicro renders micro-USD as the shortest exact decimal for
// native flags: 250000 → "0.25", 1000000 → "1", 4200 → "0.0042".
// Round-trip law: ParseUSDMicro(FormatUSDMicro(m)) == m for all m ≥ 0.
func FormatUSDMicro(m int64) string {
	whole, frac := m/microPerUSD, m%microPerUSD
	if frac == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strconv.FormatInt(whole, 10) + "." +
		strings.TrimRight(fmt.Sprintf("%06d", frac), "0")
}

// parseReportedUSD parses a tool-REPORTED decimal (never a flag) into
// micro-USD, rounding half-up at the 6th fractional digit: "0.0031415" →
// 3142. Exponent forms fall back to float64 (the only float on any cost
// path); malformed or negative reports yield 0 — absence over garbage, the
// raw claim survives in the transcript.
func parseReportedUSD(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s[0] == '-' || s[0] == '+' {
		return 0
	}
	if strings.ContainsAny(s, "eE") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f < 0 || f > math.MaxInt64/microPerUSD {
			return 0
		}
		return int64(math.Round(f * microPerUSD))
	}
	wholeStr, fracStr, _ := strings.Cut(s, ".")
	if wholeStr == "" {
		wholeStr = "0"
	}
	if !isDigits(wholeStr) || (fracStr != "" && !isDigits(fracStr)) {
		return 0
	}
	whole, err := strconv.ParseInt(wholeStr, 10, 64)
	if err != nil {
		return 0
	}
	keep := fracStr
	if len(keep) > 6 {
		keep = keep[:6]
	}
	frac, err := strconv.ParseInt(keep+strings.Repeat("0", 6-len(keep)), 10, 64)
	if err != nil {
		return 0
	}
	if len(fracStr) > 6 && fracStr[6] >= '5' { // half-up at the 6th digit
		frac++
	}
	if whole > (math.MaxInt64-frac)/microPerUSD {
		return 0
	}
	return whole*microPerUSD + frac
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}
