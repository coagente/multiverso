package oracle

import "testing"

func TestParseReportlogGolden(t *testing.T) {
	// The recorded session: one clean test, one flaky test that failed its
	// first call and passed on the rerun, one test that failed both times,
	// and one skipped test (setup phase only, so it has no call record).
	got, err := parseReportlog(fixture(t, "reportlog-rerun.jsonl"))
	if err != nil {
		t.Fatalf("parseReportlog: %v", err)
	}
	want := reportlogSummary{failedFirstRun: 2, passedAfterRerun: 1}
	if got != want {
		t.Errorf("summary = %+v, want %+v", got, want)
	}
}

func TestParseReportlogTable(t *testing.T) {
	const (
		pass  = `{"$report_type":"TestReport","nodeid":"t.py::a","when":"call","outcome":"passed"}`
		fail  = `{"$report_type":"TestReport","nodeid":"t.py::a","when":"call","outcome":"failed"}`
		other = `{"$report_type":"TestReport","nodeid":"t.py::b","when":"call","outcome":"passed"}`
		setup = `{"$report_type":"TestReport","nodeid":"t.py::a","when":"setup","outcome":"failed"}`
		start = `{"$report_type":"SessionStart","pytest_version":"9.1.1"}`
	)
	tests := []struct {
		name    string
		lines   []string
		want    reportlogSummary
		wantErr bool
	}{
		{
			name:  "clean first run",
			lines: []string{start, pass, other},
			want:  reportlogSummary{},
		},
		{
			// The first record per node id IS the first run: a later pass
			// never rewrites it, which is what makes the gate see the
			// first run (EP-6 / decision 17).
			name:  "failed then passed on rerun",
			lines: []string{start, fail, pass},
			want:  reportlogSummary{failedFirstRun: 1, passedAfterRerun: 1},
		},
		{
			name:  "pass after rerun counts once per node id",
			lines: []string{fail, pass, pass, pass},
			want:  reportlogSummary{failedFirstRun: 1, passedAfterRerun: 1},
		},
		{
			name:  "failed every attempt",
			lines: []string{fail, fail, fail},
			want:  reportlogSummary{failedFirstRun: 1},
		},
		{
			// A test that passed first and failed on a later attempt is
			// NOT a pass-after-rerun; the first run still stands.
			name:  "passed then failed",
			lines: []string{pass, fail},
			want:  reportlogSummary{},
		},
		{
			// Only the call phase is accounted: a setup failure is a
			// collection/fixture problem, not a first-run test outcome.
			name:    "setup records only",
			lines:   []string{start, setup},
			wantErr: true,
		},
		{
			// A dropped failure record would turn a failing suite into a
			// passing one, so a partially readable log is no log at all.
			name:    "malformed line",
			lines:   []string{pass, `{"$report_type":"TestReport",`},
			wantErr: true,
		},
		{
			name:    "empty file",
			lines:   nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b []byte
			for _, l := range tt.lines {
				b = append(b, l...)
				b = append(b, '\n')
			}
			got, err := parseReportlog(b)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseReportlog: want error, got %+v", got)
				}
				if got != (reportlogSummary{}) {
					t.Errorf("parseReportlog returned %+v alongside an error; want the zero summary", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseReportlog: %v", err)
			}
			if got != tt.want {
				t.Errorf("summary = %+v, want %+v", got, tt.want)
			}
		})
	}
}
