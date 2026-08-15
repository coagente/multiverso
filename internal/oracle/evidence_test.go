package oracle

// M1f: the severed evidence path, end to end through the oracle. The
// structural rules S1–S3 are the whole product fix, so they get the table
// the design asks for: {exit 0, exit 1, exit 5} × {stream ok, incomplete,
// absent} × {junit agrees, disagrees, absent}.
//
// Nothing here needs pytest: the fake interpreter plays both the runner
// AND the control-plane plugin, which is exactly what makes the test
// runnable on a machine with no plugins installed.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// streamRecords builds a well-formed record body (header excluded) for a
// suite of `total` tests of which `failed` fail.
func streamRecords(total, failed int, exitstatus int) string {
	var b strings.Builder
	b.WriteString("1\tsession_start\t{\"pid\":1}\n")
	b.WriteString("2\tcollected\t{\"count\":" + itoa(total) + "}\n")
	seq := 3
	for i := 0; i < total; i++ {
		outcome := "passed"
		if i < failed {
			outcome = "failed"
		}
		b.WriteString(itoa(seq) + "\ttest\t{\"duration_ms\":1,\"nodeid\":\"t.py::t" + itoa(i) +
			"\",\"outcome\":\"" + outcome + "\",\"run\":1}\n")
		seq++
	}
	b.WriteString(itoa(seq) + "\tsession_finish\t{\"duration_ms\":10,\"errored\":0,\"exitstatus\":" +
		itoa(exitstatus) + ",\"failed\":" + itoa(failed) + ",\"passed\":" + itoa(total-failed) +
		",\"skipped\":0,\"total\":" + itoa(total) + "}\n")
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		return "-" + string(d)
	}
	return string(d)
}

func junitXML(total, failures, errors, skipped int) string {
	return `<testsuite name="p" tests="` + itoa(total) + `" failures="` + itoa(failures) +
		`" errors="` + itoa(errors) + `" skipped="` + itoa(skipped) + `" time="0.100"></testsuite>`
}

// newStreamingOracle builds a suite or collect oracle with a real evidence
// channel, in the regime a T0 race actually runs under.
func newStreamingOracle(t *testing.T, kind string, f fakePython, crosscheck string) (Oracle, string) {
	t.Helper()
	world := t.TempDir()
	dir := t.TempDir()
	o, err := New(Params{
		Spec:          testSpec(kind, f.write(t)),
		CAS:           newStore(t),
		Regime:        object.RegimeStreamed,
		Crosscheck:    crosscheck,
		EvidenceDir:   filepath.Join(dir, "ev"),
		ScratchDir:    filepath.Join(dir, "scratch"),
		PluginDir:     filepath.Join(dir, "plugin"),
		InWorldPlugin: filepath.Join(dir, "plugin"),
		PluginDigest:  PluginDigest(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return o, world
}

// S1, S2 and S3, one row each, over the suite oracle.
func TestSuiteStructuralRules(t *testing.T) {
	cases := []struct {
		name       string
		fake       fakePython
		crosscheck string
		wantStatus string
		wantReason string
		wantAbsent bool // no tests_* metric at all
		wantPassed int64
	}{
		{
			name: "an honest run: the stream is the source and the file agrees",
			fake: fakePython{
				probe:  map[string]string{ToolPytest: "8.4.1"},
				stream: streamRecords(8, 0, 0),
				junit:  junitXML(8, 0, 0, 0),
			},
			wantStatus: StatusPass, wantPassed: 8,
		},
		{
			name: "S1 — the plugin was silenced: exit 0 with no stream is ERROR, never pass",
			fake: fakePython{
				probe: map[string]string{ToolPytest: "8.4.1"},
				junit: junitXML(500, 0, 0, 0), // the forged file is irrelevant
			},
			wantStatus: StatusError,
			wantReason: "no usable evidence stream",
			wantAbsent: true,
		},
		{
			name: "S1 — an incomplete stream is absent evidence, not a partial tally",
			fake: fakePython{
				probe:  map[string]string{ToolPytest: "8.4.1"},
				stream: "1\tsession_start\t{\"pid\":1}\n2\tcollected\t{\"count\":8}\n",
			},
			wantStatus: StatusError,
			wantReason: "no usable evidence stream",
			wantAbsent: true,
		},
		{
			name: "S1 — a replayed stream from another run fails the nonce check",
			fake: fakePython{
				probe:       map[string]string{ToolPytest: "8.4.1"},
				stream:      streamRecords(8, 0, 0),
				headerNonce: "deadbeefdeadbeefdeadbeefdeadbeef",
			},
			wantStatus: StatusError,
			wantReason: "nonce mismatch",
			wantAbsent: true,
		},
		{
			name: "S2 — os._exit(0) against a stream that reported failures",
			fake: fakePython{
				probe:  map[string]string{ToolPytest: "8.4.1"},
				stream: streamRecords(8, 2, 1),
				junit:  junitXML(8, 2, 0, 0),
				exit:   0, // the atexit handler lied about the exit code
			},
			wantStatus: StatusError,
			wantReason: "exit_code=0 but the evidence stream reports failed=2 errored=0",
			// The metrics are still the STREAM's: the forgery is in the
			// exit code, and the stream told the truth about the tests.
			wantPassed: 6,
		},
		{
			name: "S2 — the stream's own exitstatus disagrees with the process",
			fake: fakePython{
				probe:  map[string]string{ToolPytest: "8.4.1"},
				stream: streamRecords(8, 0, 3),
				junit:  junitXML(8, 0, 0, 0),
				exit:   0,
			},
			wantStatus: StatusError,
			wantReason: "evidence stream reports exitstatus=3 but the process exited 0",
			wantPassed: 8,
		},
		{
			name: "S3 — a forged junit.xml disagrees with the stream",
			fake: fakePython{
				probe:  map[string]string{ToolPytest: "8.4.1"},
				stream: streamRecords(8, 0, 0),
				junit:  junitXML(500, 0, 0, 0), // the study's 500-test lie
			},
			wantStatus: StatusError,
			wantReason: "junit-xml and the evidence stream disagree: junit(total=500,failed=0,errored=0,skipped=0) stream(total=8,failed=0,errored=0,skipped=0)",
			wantPassed: 8,
		},
		{
			name: "S3 — crosscheck off is a PINNED policy choice, not a runtime flag",
			fake: fakePython{
				probe:  map[string]string{ToolPytest: "8.4.1"},
				stream: streamRecords(8, 0, 0),
				junit:  junitXML(500, 0, 0, 0),
			},
			crosscheck: policy.CrosscheckOff,
			wantStatus: StatusPass, wantPassed: 8,
		},
		{
			name: "a failing suite is evidence: exit 1 with a stream that agrees",
			fake: fakePython{
				probe:  map[string]string{ToolPytest: "8.4.1"},
				stream: streamRecords(8, 2, 1),
				junit:  junitXML(8, 2, 0, 0),
				exit:   1,
			},
			wantStatus: StatusFail, wantPassed: 6,
		},
		{
			name: "exit 1 with NO stream is fail, not error: the process said so",
			fake: fakePython{
				probe: map[string]string{ToolPytest: "8.4.1"},
				exit:  1,
			},
			wantStatus: StatusFail, wantAbsent: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			crosscheck := tc.crosscheck
			if crosscheck == "" {
				crosscheck = policy.CrosscheckRequire
			}
			o, world := newStreamingOracle(t, KindPytestSuite, tc.fake, crosscheck)
			rec, err := o.Run(context.Background(), backend.HostDir(world))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if rec.Result.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q (metrics %v)", rec.Result.Status, tc.wantStatus, rec.Result.Metrics)
			}
			if tc.wantAbsent {
				for name := range rec.Result.Metrics {
					if strings.HasPrefix(name, "tests_") {
						t.Errorf("metrics carry %q; an absent source must yield an absent metric", name)
					}
				}
			} else if got := rec.Result.Metrics[MetricTestsPassed]; got != tc.wantPassed {
				t.Errorf("tests_passed = %d, want %d — the STREAM is the source, never the file", got, tc.wantPassed)
			}
			if tc.wantReason != "" {
				stderr := storedStderr(t, o, rec)
				if !strings.Contains(stderr, tc.wantReason) {
					t.Errorf("stderr artifact does not carry the reason %q:\n%s", tc.wantReason, stderr)
				}
			}
			// The receipt says HOW it was observed, always.
			if rec.Execution.EvidenceRegime != object.RegimeStreamed {
				t.Errorf("evidence_regime = %q, want %q", rec.Execution.EvidenceRegime, object.RegimeStreamed)
			}
			if rec.Execution.EvidencePlugin != PluginDigest() {
				t.Errorf("evidence_plugin = %q, want the observer's digest", rec.Execution.EvidencePlugin)
			}
		})
	}
}

// The collect rung: the count that reaches the gate comes from the stream,
// and the study's ninth vector — a forged "8 tests collected" printed from
// an atexit hook — is a DISAGREEMENT rather than the answer.
func TestCollectStreamIsTheSource(t *testing.T) {
	t.Run("stdout that disagrees with the stream is an error, not a correction", func(t *testing.T) {
		o, world := newStreamingOracle(t, KindPytestCollect, fakePython{
			probe:  map[string]string{ToolPytest: "8.4.1"},
			stream: "1\tsession_start\t{\"pid\":1}\n2\tcollected\t{\"count\":6}\n3\tsession_finish\t{\"duration_ms\":1,\"errored\":0,\"exitstatus\":0,\"failed\":0,\"passed\":0,\"skipped\":0,\"total\":0}\n",
			// The forged summary line the study's vector 09 printed.
			stdout: "6 tests collected in 0.01s\n8 tests collected in 0.01s",
		}, policy.CrosscheckRequire)
		rec, err := o.Run(context.Background(), backend.HostDir(world))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rec.Result.Status != StatusError {
			t.Errorf("status = %q, want error", rec.Result.Status)
		}
		if got := rec.Result.Metrics[MetricCollectedTotal]; got != 6 {
			t.Errorf("collected_total = %d, want the STREAM's 6, never the printed 8", got)
		}
		if s := storedStderr(t, o, rec); !strings.Contains(s,
			"collect-only stdout reports 8 collected, the evidence stream reports 6") {
			t.Errorf("stderr artifact does not carry the S3 reason:\n%s", s)
		}
	})

	t.Run("exit 5 still forces collected_total = 0", func(t *testing.T) {
		o, world := newStreamingOracle(t, KindPytestCollect, fakePython{
			probe:  map[string]string{ToolPytest: "8.4.1"},
			stream: "1\tsession_start\t{\"pid\":1}\n2\tcollected\t{\"count\":0}\n3\tsession_finish\t{\"duration_ms\":1,\"errored\":0,\"exitstatus\":5,\"failed\":0,\"passed\":0,\"skipped\":0,\"total\":0}\n",
			stdout: "no tests collected in 0.00s",
			exit:   5,
		}, policy.CrosscheckRequire)
		rec, err := o.Run(context.Background(), backend.HostDir(world))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if v, ok := rec.Result.Metrics[MetricCollectedTotal]; !ok || v != 0 {
			t.Errorf("collected_total = %d (present %v), want an explicit 0", v, ok)
		}
		if rec.Result.Status != StatusFail {
			t.Errorf("status = %q, want fail (exit 5 is non-zero and never a pass)", rec.Result.Status)
		}
	})

	t.Run("a stream that claims a count against exit 5 is a disagreement", func(t *testing.T) {
		o, world := newStreamingOracle(t, KindPytestCollect, fakePython{
			probe:  map[string]string{ToolPytest: "8.4.1"},
			stream: "1\tsession_start\t{\"pid\":1}\n2\tcollected\t{\"count\":8}\n3\tsession_finish\t{\"duration_ms\":1,\"errored\":0,\"exitstatus\":5,\"failed\":0,\"passed\":0,\"skipped\":0,\"total\":0}\n",
			exit:   5,
		}, policy.CrosscheckRequire)
		rec, err := o.Run(context.Background(), backend.HostDir(world))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rec.Result.Status != StatusError {
			t.Errorf("status = %q, want error", rec.Result.Status)
		}
		if s := storedStderr(t, o, rec); !strings.Contains(s,
			"exit_code=5 (no tests collected) but the evidence stream reports collected=8") {
			t.Errorf("stderr artifact does not carry the reason:\n%s", s)
		}
	})
}

// The raw stream bytes reach CAS as their own artifact BEFORE anything is
// parsed (EP-7 order, unchanged), and result.tools names the source.
func TestStreamArtifactAndTools(t *testing.T) {
	o, world := newStreamingOracle(t, KindPytestSuite, fakePython{
		probe:  map[string]string{ToolPytest: "8.4.1"},
		stream: streamRecords(3, 0, 0),
		junit:  junitXML(3, 0, 0, 0),
	}, policy.CrosscheckRequire)
	rec, err := o.Run(context.Background(), backend.HostDir(world))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.Result.Tools[ToolEvidence] != StreamVersion {
		t.Errorf("tools[%q] = %q, want %q", ToolEvidence, rec.Result.Tools[ToolEvidence], StreamVersion)
	}
	// stdout, stderr, probe, evidence-stream, junit-xml.
	if len(rec.Result.Artifacts) != 5 {
		t.Fatalf("artifacts = %d, want 5 (stdout, stderr, probe, evidence-stream, junit-xml)",
			len(rec.Result.Artifacts))
	}
	raw := storedArtifact(t, o, rec, 3)
	if !strings.HasPrefix(raw, StreamSchema+"\t") {
		t.Errorf("the fourth artifact is not the raw stream: %q", raw[:min(40, len(raw))])
	}
}

// storedStderr returns the stderr artifact's bytes: the control-plane
// notes live there, which is the M1b/M1c precedent — an operator sees WHY
// a metric is missing without a second channel.
func storedStderr(t *testing.T, o Oracle, rec object.Receipt) string {
	t.Helper()
	return storedArtifact(t, o, rec, 1)
}

func storedArtifact(t *testing.T, o Oracle, rec object.Receipt, i int) string {
	t.Helper()
	po, ok := o.(*pytestOracle)
	if !ok {
		t.Fatalf("not a pytest oracle: %T", o)
	}
	store, ok := po.store.(interface{ Get(string) ([]byte, error) })
	if !ok {
		t.Fatalf("store does not read back: %T", po.store)
	}
	if i >= len(rec.Result.Artifacts) {
		t.Fatalf("artifact %d of %d", i, len(rec.Result.Artifacts))
	}
	b, err := store.Get(rec.Result.Artifacts[i])
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
