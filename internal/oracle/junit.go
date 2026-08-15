package oracle

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// junitSummary aggregates every <testsuite> element of one JUnit XML
// report. JUnit XML is the stable machine-readable interface of core pytest
// (research ch. 19) — no plugin is required for it, which is why it is the
// only always-present structured source of the suite oracle.
type junitSummary struct {
	total      int64
	failures   int64
	errors     int64
	skipped    int64
	durationMS int64
}

// passed derives tests_passed: JUnit records failures, errors and skips,
// never passes. A report whose counters do not add up yields 0 rather than
// a negative count — the weakest honest reading of a broken report.
func (s junitSummary) passed() int64 {
	p := s.total - s.failures - s.errors - s.skipped
	if p < 0 {
		return 0
	}
	return p
}

// parseJUnit walks b as XML and sums every <testsuite> element, so a
// <testsuites> root and a bare <testsuite> are both handled without
// branching on the document shape.
//
// It is total and pure: malformed or truncated XML is an error, and the
// caller records NO metrics rather than the partial sums accumulated before
// the break — half a report is not evidence. An absent counter attribute
// contributes 0 (nothing recorded); a counter that is present but not an
// integer is corruption and fails the parse.
func parseJUnit(b []byte) (junitSummary, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var sum junitSummary
	seen := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return junitSummary{}, fmt.Errorf("oracle: junit: %w", err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok || el.Name.Local != "testsuite" {
			continue
		}
		seen++
		for _, attr := range el.Attr {
			switch attr.Name.Local {
			case "tests", "failures", "errors", "skipped":
				n, err := strconv.ParseInt(strings.TrimSpace(attr.Value), 10, 64)
				if err != nil || n < 0 {
					return junitSummary{}, fmt.Errorf("oracle: junit: testsuite %s=%q: not a count",
						attr.Name.Local, attr.Value)
				}
				switch attr.Name.Local {
				case "tests":
					sum.total += n
				case "failures":
					sum.failures += n
				case "errors":
					sum.errors += n
				case "skipped":
					sum.skipped += n
				}
			case "time":
				ms, ok := decimalMS(attr.Value)
				if !ok {
					return junitSummary{}, fmt.Errorf("oracle: junit: testsuite time=%q: not a duration",
						attr.Value)
				}
				sum.durationMS += ms
			}
		}
	}
	if seen == 0 {
		return junitSummary{}, errors.New("oracle: junit: no <testsuite> element")
	}
	return sum, nil
}

// decimalMS converts a JUnit time attribute — seconds as a decimal string
// ("0.123", "12", "1.0e-3") — into whole milliseconds, rounded half-up.
// Decimal-string arithmetic on the primary path (the usd.go discipline:
// DP-1 forbids floats in canonical JSON, and a duration that reaches a
// receipt must not depend on binary rounding); strconv.ParseFloat is the
// fallback for the exponent form alone. A negative or malformed value is
// refused rather than coerced.
func decimalMS(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s[0] == '-' || s[0] == '+' {
		return 0, false
	}
	if strings.ContainsAny(s, "eE") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f < 0 || f > math.MaxInt64/1000 {
			return 0, false
		}
		return int64(math.Floor(f*1000 + 0.5)), true
	}
	wholeStr, fracStr, _ := strings.Cut(s, ".")
	if wholeStr == "" {
		wholeStr = "0"
	}
	if !isDigits(wholeStr) || (fracStr != "" && !isDigits(fracStr)) {
		return 0, false
	}
	whole, err := strconv.ParseInt(wholeStr, 10, 64)
	if err != nil {
		return 0, false
	}
	keep := fracStr
	if len(keep) > 3 {
		keep = keep[:3]
	}
	frac, err := strconv.ParseInt("0"+keep+strings.Repeat("0", 3-len(keep)), 10, 64)
	if err != nil {
		return 0, false
	}
	if len(fracStr) > 3 && fracStr[3] >= '5' { // half-up at the millisecond
		frac++
	}
	if whole > (math.MaxInt64-frac)/1000 {
		return 0, false
	}
	return whole*1000 + frac, true
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}
