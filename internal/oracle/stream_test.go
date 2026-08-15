package oracle

// M1f: the evidence stream. The parser tests are PURE — recorded fixtures
// in testdata/stream/, no Python, no plugins, no FIFO — and the lifecycle
// tests drive a fake writer process, so the whole severed evidence path is
// under test without pytest being installed anywhere.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// streamNonce is the nonce every fixture's header carries.
const streamNonce = "0123456789abcdef0123456789abcdef"

func streamFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "stream", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// Parse rules 1–7, one row each. Every violation of 1, 2, 3 or 6 makes the
// stream UNUSABLE, which makes every stream-derived metric ABSENT — never
// a fabricated zero, and never a pass.
func TestParseStreamRules(t *testing.T) {
	cases := []struct {
		name       string
		file       string
		nonce      string
		over       bool
		usable     bool
		reasonHas  string
		notesHave  string
		wantsNotes bool
	}{
		{name: "rule 1 — a well-formed header is the entry price", file: "stream-pass.txt", nonce: streamNonce, usable: true},
		{name: "rule 1 — a nonce from another run is a stale stream", file: "stream-bad-nonce.txt", nonce: streamNonce,
			reasonHas: "stream header missing or nonce mismatch"},
		{name: "rule 2 — a gap in the sequence", file: "stream-seq-gap.txt", nonce: streamNonce,
			reasonHas: "stream sequence broken at line"},
		{name: "rule 2 — a repeated sequence number", file: "stream-seq-repeat.txt", nonce: streamNonce,
			reasonHas: "stream sequence broken at line"},
		{name: "rule 3 — no session_start at seq 1", file: "stream-no-start.txt", nonce: streamNonce,
			reasonHas: "no session_start at seq 1"},
		{name: "rule 3 — a second session_start", file: "stream-double-start.txt", nonce: streamNonce,
			reasonHas: "second session_start"},
		{name: "rule 4 — records after session_finish are discarded and noted",
			file: "stream-after-finish.txt", nonce: streamNonce, usable: true,
			notesHave: "after session_finish discarded", wantsNotes: true},
		{name: "rule 5 — an unknown kind is ignored and noted",
			file: "stream-unknown-kind.txt", nonce: streamNonce, usable: true,
			notesHave: "unknown evidence record kind", wantsNotes: true},
		{name: "rule 6 — over the cap", file: "stream-pass.txt", nonce: streamNonce, over: true,
			reasonHas: "exceeds the 64 MiB cap"},
		{name: "rule 7 — no session_finish is INCOMPLETE, and incomplete is absent",
			file: "stream-no-finish.txt", nonce: streamNonce,
			reasonHas: "no session_finish"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ParseStream(streamFixture(t, tc.file), tc.nonce, tc.over)
			if s.Usable != tc.usable {
				t.Fatalf("usable = %v (%s), want %v", s.Usable, s.Reason, tc.usable)
			}
			if tc.reasonHas != "" && !strings.Contains(s.Reason, tc.reasonHas) {
				t.Errorf("reason = %q, want it to contain %q", s.Reason, tc.reasonHas)
			}
			if tc.wantsNotes && !strings.Contains(s.Notes, tc.notesHave) {
				t.Errorf("notes = %q, want it to contain %q", s.Notes, tc.notesHave)
			}
			// The honesty rule, stated as an invariant of the parser: an
			// unusable stream carries no numbers at all.
			if !s.Usable && (s.Total != 0 || s.Passed != 0 || s.HasCollected) {
				t.Errorf("an unusable stream carried metrics: %+v", s)
			}
		})
	}
}

// Rule 4 in its adversarial form: an appended "corrected" tally after
// session_finish is written into a stream nobody reads any more.
func TestRecordsAfterFinishCannotRewriteTheTally(t *testing.T) {
	s := ParseStream(streamFixture(t, "stream-after-finish.txt"), streamNonce, false)
	if !s.Usable {
		t.Fatalf("stream unusable: %s", s.Reason)
	}
	if s.Total != 3 || s.Passed != 3 {
		t.Errorf("total/passed = %d/%d, want 3/3 — the post-finish records must be discarded", s.Total, s.Passed)
	}
	if s.FinishTotal != 3 {
		t.Errorf("session_finish total = %d, want the FIRST finish's 3, not the appended 500", s.FinishTotal)
	}
}

// The metric derivation table, including the rerun rules and the JUnit
// equivalence classes.
func TestStreamMetricDerivation(t *testing.T) {
	t.Run("a passing suite", func(t *testing.T) {
		s := ParseStream(streamFixture(t, "stream-pass.txt"), streamNonce, false)
		want := map[string]int64{"collected": 3, "total": 3, "passed": 3, "failed": 0, "duration": 812}
		got := map[string]int64{"collected": s.Collected, "total": s.Total, "passed": s.Passed,
			"failed": s.Failed, "duration": s.DurationMS}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %d, want %d", k, got[k], v)
			}
		}
	})
	t.Run("failures, errors and skips are distinct", func(t *testing.T) {
		s := ParseStream(streamFixture(t, "stream-fail.txt"), streamNonce, false)
		if s.Total != 4 || s.Passed != 1 || s.Failed != 1 || s.Errored != 1 || s.Skipped != 1 {
			t.Errorf("total/passed/failed/errored/skipped = %d/%d/%d/%d/%d, want 4/1/1/1/1",
				s.Total, s.Passed, s.Failed, s.Errored, s.Skipped)
		}
		if s.ExitStatus != 1 {
			t.Errorf("exitstatus = %d, want 1", s.ExitStatus)
		}
	})
	t.Run("reruns: first run vs final outcome, per nodeid", func(t *testing.T) {
		s := ParseStream(streamFixture(t, "stream-rerun.txt"), streamNonce, false)
		// flaky: failed then passed. broken: failed twice. expected:
		// xfailed → skipped. surprise: xpassed → passed.
		if s.Total != 4 {
			t.Errorf("total = %d, want 4 distinct nodeids", s.Total)
		}
		if s.FailedFirstRun != 2 {
			t.Errorf("tests_failed_first_run = %d, want 2 (flaky + broken)", s.FailedFirstRun)
		}
		if s.PassedAfterRun != 1 {
			t.Errorf("tests_passed_after_rerun = %d, want 1 (flaky alone)", s.PassedAfterRun)
		}
		if s.Failed != 1 {
			t.Errorf("tests_failed = %d, want 1 (broken's FINAL outcome)", s.Failed)
		}
		if s.Passed != 2 {
			t.Errorf("tests_passed = %d, want 2 (flaky's rerun + the xpassed)", s.Passed)
		}
		if s.Skipped != 1 {
			t.Errorf("tests_skipped = %d, want 1 (xfailed counts as skipped)", s.Skipped)
		}
	})
}

// fakeWriter is a shell process that writes to the channel. It stands in
// for the plugin so the FIFO lifecycle is tested with no Python at all.
func fakeWriter(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "writer")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake writer: %v", err)
	}
	return path
}

// The FIFO lifecycle, one row per way a writer can misbehave. Every one of
// them must yield ABSENT metrics and an unusable stream — never a pass.
func TestEvidenceChannelLifecycle(t *testing.T) {
	header := fmt.Sprintf("mvo-evidence/v0\t%s\n", streamNonce)
	cases := []struct {
		name   string
		script string
		usable bool
		cap    int64
	}{
		{
			name:   "a writer that never opens the channel",
			script: "exit 0",
		},
		{
			name:   "a writer that opens and never finishes",
			script: fmt.Sprintf("printf '%s1\\tsession_start\\t{}\\n' >> \"$1\"", header),
		},
		{
			name: "a writer that floods past the cap",
			script: fmt.Sprintf("printf '%s1\\tsession_start\\t{}\\n' >> \"$1\"; "+
				"i=0; while [ $i -lt 200 ]; do printf '%%0512d\\n' 0 >> \"$1\"; i=$((i+1)); done", header),
			cap: 1024,
		},
		{
			name: "a writer that unlinks and replaces the channel",
			// The candidate's uid CAN do this under T0. It gains nothing:
			// the control plane holds a handle on the ORIGINAL inode, so
			// the replacement is a file nobody reads, and the stream ends
			// with no terminal record.
			script: "rm -f \"$1\"; : > \"$1\"; " +
				"printf 'mvo-evidence/v0\\tzz\\n1\\tsession_finish\\t{\"total\":500}\\n' >> \"$1\"",
		},
		{
			name: "a writer that truncates and rewrites the whole stream at exit",
			// The atexit-class attack in its strongest form: a COMPLETE,
			// correctly-nonced forged stream written over the honest one.
			// The reader never rewinds, so it resumes at its old offset,
			// the sequence desynchronizes, and the stream is UNUSABLE —
			// a failed gate, never a forged pass.
			script: fmt.Sprintf("printf '%s1\\tsession_start\\t{}\\n2\\tcollected\\t{\"count\":8}\\n' >> \"$1\"; "+
				"sleep 1; : > \"$1\"; "+
				"printf '%s1\\tsession_start\\t{}\\n2\\tsession_finish\\t{\"total\":500,\"passed\":500,\"failed\":0,\"errored\":0,\"skipped\":0,\"exitstatus\":0,\"duration_ms\":1}\\n' >> \"$1\"",
				header, header),
		},
		{
			name: "an honest writer",
			script: fmt.Sprintf("printf '%s1\\tsession_start\\t{}\\n2\\tsession_finish\\t"+
				"{\"duration_ms\":1,\"errored\":0,\"exitstatus\":0,\"failed\":0,\"passed\":0,\"skipped\":0,\"total\":0}\\n' >> \"$1\"", header),
			usable: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			capBytes := tc.cap
			if capBytes == 0 {
				capBytes = artifactCapBytes
			}
			ch, err := openEvidenceChannel(dir, streamNonce, capBytes)
			if err != nil {
				t.Fatalf("openEvidenceChannel: %v", err)
			}
			out, err := exec.Command(fakeWriter(t, tc.script), ch.Path()).CombinedOutput()
			if err != nil {
				t.Fatalf("fake writer: %v: %s", err, out)
			}
			s := ParseStream(ch.Close(), streamNonce, ch.Over())
			if s.Usable != tc.usable {
				t.Fatalf("usable = %v (%s), want %v", s.Usable, s.Reason, tc.usable)
			}
			if !s.Usable && (s.HasCollected || s.Total != 0) {
				t.Errorf("an unusable stream carried metrics: %+v", s)
			}
		})
	}
}

// The channel is torn down completely: the FIFO is the control plane's, so
// a channel left behind is a channel a later run could be talked into
// reading.
func TestEvidenceChannelRemovesItsFIFO(t *testing.T) {
	dir := t.TempDir()
	ch, err := openEvidenceChannel(dir, streamNonce, artifactCapBytes)
	if err != nil {
		t.Fatalf("openEvidenceChannel: %v", err)
	}
	path := ch.Path()
	ch.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the FIFO survived teardown at %s (err=%v)", path, err)
	}
	// Close is idempotent: the deferred sweep in Run calls it a second
	// time, and it must return the same bytes rather than re-reading
	// anything (there is nothing left to read from).
	if got := ch.Close(); len(got) != 0 {
		t.Errorf("a second Close returned %q, want the same empty stream", got)
	}
}
