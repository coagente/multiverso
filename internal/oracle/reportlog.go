package oracle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// reportlogSummary is the FIRST-RUN accounting pytest-reportlog makes
// possible (M1e decision 17). JUnit XML records only the final outcome of a
// test, so under --reruns a flaky test that passed on retry is
// indistinguishable from one that passed outright. The JSONL keeps every
// attempt, so the gate can see the first run and a pass-after-rerun can be
// recorded as strictly weaker, separately named evidence.
type reportlogSummary struct {
	failedFirstRun   int64
	passedAfterRerun int64
}

// reportlogRecord is the sliver of a pytest-reportlog line we read. The
// plugin writes one self-contained JSON object per line, flushed after each
// line (research ch. 19).
type reportlogRecord struct {
	Type    string `json:"$report_type"`
	NodeID  string `json:"nodeid"`
	When    string `json:"when"`
	Outcome string `json:"outcome"`
}

// parseReportlog scans a pytest-reportlog JSONL file and accounts the call
// phase per node id: the FIRST record for a node id is its first-run
// outcome, and a later passing record for a node id that first failed
// counts once as a pass-after-rerun.
//
// Strict by design: a line that is not JSON fails the whole parse, and the
// caller records no first-run metrics at all. A partially readable JSONL
// cannot honestly support the claim "the first run was clean" — a dropped
// failure record would turn a failing suite into a passing one, which is
// exactly the laundering this milestone exists to prevent. A run whose log
// holds no call records at all (an all-skipped suite) is likewise no
// evidence about first runs, not evidence of zero failures.
func parseReportlog(b []byte) (reportlogSummary, error) {
	var (
		sum     reportlogSummary
		first   = map[string]string{}
		counted = map[string]bool{}
		calls   int
	)
	for i, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec reportlogRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return reportlogSummary{}, fmt.Errorf("oracle: reportlog: line %d: %w", i+1, err)
		}
		if rec.Type != "TestReport" || rec.When != "call" || rec.NodeID == "" {
			continue
		}
		calls++
		outcome, seen := first[rec.NodeID]
		if !seen {
			first[rec.NodeID] = rec.Outcome
			if rec.Outcome == "failed" {
				sum.failedFirstRun++
			}
			continue
		}
		if outcome == "failed" && rec.Outcome == "passed" && !counted[rec.NodeID] {
			counted[rec.NodeID] = true
			sum.passedAfterRerun++
		}
	}
	if calls == 0 {
		return reportlogSummary{}, errors.New("oracle: reportlog: no TestReport call records")
	}
	return sum, nil
}
