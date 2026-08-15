package oracle

import (
	"encoding/json"
	"fmt"
	"math"
)

// bpScale is the basis-point scale: 10000 bp = 100 %, so 8735 reads 87.35 %.
// Coverage travels as an integer because DP-1 forbids floats in canonical
// JSON, and a gate threshold that depends on binary rounding is not a gate.
const bpScale = 10000

// coverageDoc is the sliver of coverage.py's JSON report we read. Only the
// two INTEGER counters are declared: coverage.py also reports
// percent_covered as a float, and parsing it would put a float on the
// primary metric path for no gain (the counters are exact, the percentage
// is derived).
type coverageDoc struct {
	Totals struct {
		CoveredLines  json.Number `json:"covered_lines"`
		NumStatements json.Number `json:"num_statements"`
	} `json:"totals"`
}

// parseCoverageBP derives coverage_bp from coverage.py's JSON report by
// integer arithmetic over totals.covered_lines / totals.num_statements,
// rounded half-up. A report with no statements (num_statements == 0) yields
// an ERROR, not 100 %: "nothing to cover" is not evidence of coverage, and
// a fabricated 10000 would silently satisfy a coverage-at-least gate.
func parseCoverageBP(b []byte) (int64, error) {
	var doc coverageDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return 0, fmt.Errorf("oracle: coverage json: %w", err)
	}
	if doc.Totals.NumStatements == "" || doc.Totals.CoveredLines == "" {
		return 0, fmt.Errorf("oracle: coverage json: totals.covered_lines/num_statements absent")
	}
	covered, err := doc.Totals.CoveredLines.Int64()
	if err != nil {
		return 0, fmt.Errorf("oracle: coverage json: covered_lines: %w", err)
	}
	total, err := doc.Totals.NumStatements.Int64()
	if err != nil {
		return 0, fmt.Errorf("oracle: coverage json: num_statements: %w", err)
	}
	switch {
	case total <= 0:
		return 0, fmt.Errorf("oracle: coverage json: num_statements = %d (nothing to cover)", total)
	case covered < 0 || covered > total:
		return 0, fmt.Errorf("oracle: coverage json: covered_lines = %d of %d statements", covered, total)
	case covered > (math.MaxInt64-total/2)/bpScale:
		return 0, fmt.Errorf("oracle: coverage json: covered_lines = %d overflows basis points", covered)
	}
	return (covered*bpScale + total/2) / total, nil
}
