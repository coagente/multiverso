package oracle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StreamSchema is the evidence stream's wire schema, and the value
// result.tools reports under ToolEvidence whenever a USABLE stream was
// produced (so escalation.require_evidence's usability test keeps working
// unchanged).
const (
	StreamSchema  = "mvo-evidence/v0"
	StreamVersion = "v0"
	ToolEvidence  = "mvo-evidence"
)

// streamFile is the channel's name inside the evidence directory. The
// directory contains nothing else: under `isolated` it is owned by the
// invoking uid and mode 0755, so the oracle uid can write the channel
// (mode 0622) but can neither unlink nor replace it.
const streamFile = "stream"

// Stream record kinds.
const (
	recSessionStart  = "session_start"
	recCollected     = "collected"
	recTest          = "test"
	recSessionFinish = "session_finish"
	// recCase is M2a's ONE additive record kind: the corpus runner's
	// per-case observation. It rides the same header, the same nonce, the
	// same strictly-increasing seq and the same parse rules 1-7 as every
	// M1f record, which is why adding it needed no wire version bump.
	recCase = "case"
	// recPropertyCase is O2p's additive record: ONE Hypothesis example,
	// forwarded onto the control plane's stream by the observability
	// callback the plugin registers (M2a decision 15).
	//
	// It exists so property_cases_* can be METRICS at all. Hypothesis's
	// own observability output is JSONL written into a directory the test
	// process can write, which makes those records candidate-authorable
	// AFTER EXIT — coverage_bp's status, and unacceptable for a number a
	// gate reads. Records that arrive here instead carry the stream's
	// S1/S2/S3 protections; when the callback cannot be registered the
	// metrics are ABSENT and result.tools says which path ran.
	recPropertyCase = "property_case"
)

// Test outcomes, in the JUnit equivalence classes so the cross-check
// compares like with like (M1f wire format).
const (
	outcomePassed  = "passed"
	outcomeFailed  = "failed"
	outcomeError   = "error"
	outcomeSkipped = "skipped"
	outcomeXFailed = "xfailed" // counted as skipped
	outcomeXPassed = "xpassed" // counted as passed
)

// Stream is the parsed result of one evidence stream. Usable == false
// means every stream-derived metric is ABSENT (rule S1): a stream that
// cannot be trusted yields no numbers at all, never a fabricated zero.
type Stream struct {
	Raw      []byte // the exact bytes that reached CAS, before any parsing
	Usable   bool
	Complete bool   // a well-formed session_finish was present
	Reason   string // why it is unusable; "" when usable
	Notes    string // control-plane notes appended to the stderr artifact

	Collected      int64
	HasCollected   bool
	Total          int64
	Passed         int64
	Failed         int64
	Errored        int64
	Skipped        int64
	FailedFirstRun int64
	PassedAfterRun int64
	DurationMS     int64
	ExitStatus     int
	// FinishTotals are session_finish's own tallies, kept beside the
	// per-nodeid derivation so a disagreement between the plugin's tally
	// and the record stream is visible rather than silently reconciled.
	FinishTotal, FinishFailed, FinishErrored, FinishSkipped int64

	// O2p: Hypothesis examples that arrived ON THE STREAM.
	// HasPropertyCases is what decides whether property_cases_* are
	// EMITTED AT ALL — false means the observability callback could not be
	// registered, the JSONL fallback ran, and the metrics are absent
	// (decision 15). A zero PropertyCases with HasPropertyCases true is a
	// different and much stronger statement: the search ran and found
	// nothing to run.
	PropertyCases        int64
	PropertyCasesInvalid int64
	HasPropertyCases     bool
}

// evidenceChannel is one run's append channel: a control-plane-owned file
// created BEFORE the process is spawned, tailed live in a goroutine while
// it runs, and torn down after it exits.
//
// M1f's design specified a FIFO, and a FIFO is what this was. It does not
// survive the tier the strongest regime requires: a bind-mounted FIFO on
// Docker Desktop is a pipe INSIDE THE VM, unconnected to the host's read
// end, so the first in-container write blocks forever. A control plane a
// candidate's environment can wedge is a control plane the candidate
// controls, so the channel is a file and the reason is written down here.
//
// The security properties that mattered are unchanged, because they came
// from the LIVE READ and the parse rules, not from the inode type:
//
//   - the already-received prefix is immutable — the reader holds one
//     handle at a monotonically increasing offset and never seeks back, so
//     nothing it has consumed can be retracted;
//   - a candidate that truncates and rewrites at atexit desynchronizes the
//     sequence, which makes the stream UNUSABLE (rule 2) — a failed gate,
//     never a forged pass;
//   - records appended after session_finish are discarded (rule 4);
//   - under `isolated` the containing directory is not writable by the
//     oracle uid, so the channel can be appended to and neither unlinked
//     nor replaced — the one OS guarantee in this design.
type evidenceChannel struct {
	dir   string
	path  string
	nonce string
	cap   int64

	rd     *os.File
	mu     sync.Mutex
	buf    []byte
	over   bool
	done   chan struct{}
	closed bool
}

// openEvidenceChannel creates the FIFO and starts the reader. dir must
// already exist and be owned by the control plane.
func openEvidenceChannel(dir, nonce string, capBytes int64) (*evidenceChannel, error) {
	path := filepath.Join(dir, streamFile)
	_ = os.Remove(path)
	// Mode 0622: the oracle uid may APPEND to the channel and may not read
	// it back. It cannot unlink it either — the containing directory is not
	// writable by it — which is the one place M1f upgrades "detected" to
	// "impossible" (decision 13).
	wr, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o622)
	if err != nil {
		return nil, fmt.Errorf("oracle: create evidence stream %s: %w", path, err)
	}
	_ = wr.Close()
	if err := os.Chmod(path, 0o622); err != nil { // umask does not apply to us
		return nil, fmt.Errorf("oracle: chmod evidence stream %s: %w", path, err)
	}
	rd, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("oracle: open evidence stream %s: %w", path, err)
	}
	ch := &evidenceChannel{
		dir: dir, path: path, nonce: nonce, cap: capBytes,
		rd: rd, done: make(chan struct{}),
	}
	go ch.read()
	return ch, nil
}

// streamPollInterval is how long the reader idles at end-of-file before
// looking for more. Polling is what keeps teardown BOUNDED: a blocking
// read against a stray writer would let a candidate's leaked process wedge
// the control plane forever.
const streamPollInterval = 5 * time.Millisecond

// read tails the channel live, while the process runs. A 64 MiB cap bounds
// the buffer: the control plane must not be talked into buffering a
// gigabyte because a candidate looped a write.
//
// The handle is never rewound. Bytes already consumed are in c.buf and
// nothing the candidate does afterwards can retract them — which is the
// property the atexit-class attacks die on.
func (c *evidenceChannel) read() {
	defer close(c.done)
	for {
		c.drain()
		if c.tornDown() {
			return
		}
		time.Sleep(streamPollInterval)
	}
}

// drain reads everything available right now, appending under the cap.
func (c *evidenceChannel) drain() {
	chunk := make([]byte, 32<<10)
	for {
		n, err := c.rd.Read(chunk)
		if n > 0 {
			c.mu.Lock()
			if int64(len(c.buf))+int64(n) > c.cap {
				c.over = true
				if remaining := c.cap - int64(len(c.buf)); remaining > 0 {
					c.buf = append(c.buf, chunk[:remaining]...)
				}
			} else {
				c.buf = append(c.buf, chunk[:n]...)
			}
			c.mu.Unlock()
			continue
		}
		if err != nil {
			return // EOF for now, or a hard error; either way, nothing more
		}
	}
}

// tornDown reports whether Close has run.
func (c *evidenceChannel) tornDown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Close tears the channel down: the reader is stopped, one final drain
// picks up anything written between its last look and the process exiting,
// and the handle is closed. Callers invoke it only after cmd.Wait has
// returned, so every writer is already gone and the final drain is the end
// of the stream, not a second chance for the candidate.
func (c *evidenceChannel) Close() []byte {
	c.mu.Lock()
	already := c.closed
	c.closed = true
	c.mu.Unlock()
	if !already {
		<-c.done
		c.drain()
		_ = c.rd.Close()
		// The channel is the control plane's, so the control plane removes
		// it: one left behind is one a later run could be talked into
		// reading.
		_ = os.Remove(c.path)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf...)
}

// Path is the in-world path the plugin appends to.
func (c *evidenceChannel) Path() string { return c.path }

// Over reports whether the cap was hit.
func (c *evidenceChannel) Over() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.over
}

// record is one parsed stream line.
type record struct {
	seq     int64
	kind    string
	payload []byte
}

// streamKinds and obsKinds are the record kinds each reader KEEPS. Every
// other kind is ignored and counted (rule 5), which is precisely what makes
// the two wire dialects forward- and backward-compatible with each other:
// an M1f-era reader tolerates a corpus stream and an M2a reader tolerates a
// suite stream, because neither has to know the other's records exist.
var (
	streamKinds = map[string]bool{
		recSessionStart: true, recCollected: true, recTest: true, recSessionFinish: true,
		// A property run IS a pytest run, so O2p reads the suite dialect
		// and its per-example records ride in the same stream.
		recPropertyCase: true,
	}
	obsKinds = map[string]bool{
		recSessionStart: true, recCase: true, recSessionFinish: true,
	}
)

// framed is the result of applying the normative FRAMING rules (1-6) to raw
// stream bytes. reason != "" means the stream is unusable and no record in
// it may be read.
type framed struct {
	recs   []record
	notes  string
	reason string
}

// parseFrames applies parse rules 1-6, which are identical for every
// dialect the channel carries: the header and its nonce, the strictly
// increasing sequence, exactly one session_start at seq 1, records after
// session_finish discarded, unknown kinds ignored and counted, and the
// 64 MiB cap. The DERIVATION differs per dialect; the framing does not, and
// a second copy of these rules is a second place for them to drift.
func parseFrames(raw []byte, nonce string, over bool, keep map[string]bool) framed {
	unusable := func(format string, a ...any) framed {
		return framed{reason: fmt.Sprintf(format, a...)}
	}
	if over || int64(len(raw)) > artifactCapBytes {
		return unusable("evidence stream exceeds the %d MiB cap", artifactCapBytes>>20)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return unusable("stream header missing or nonce mismatch")
	}
	// Rule 1: the header names the schema AND the nonce this run generated
	// — the identity tag against staleness (M1f decision 15). It proves the
	// stream belongs to the run we started, defeating a replayed stream
	// left in a tree; it authenticates nothing against an in-process
	// adversary, and is documented as exactly that.
	if lines[0] != StreamSchema+"\t"+nonce {
		return unusable("stream header missing or nonce mismatch")
	}

	var recs []record
	unknown, afterFinish, finished := 0, 0, false
	want := int64(1)
	for i, line := range lines[1:] {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			return unusable("stream sequence broken at line %d", i+2)
		}
		seq, err := strconv.ParseInt(parts[0], 10, 64)
		// Rule 2: strictly increasing from 1, no gap, repeat or decrease.
		if err != nil || seq != want {
			return unusable("stream sequence broken at line %d", i+2)
		}
		want++
		if finished {
			// Rule 4: records after session_finish are DISCARDED and
			// noted. An adversary appending a corrected tally at the end
			// is writing into a stream nobody reads any more.
			afterFinish++
			continue
		}
		if keep[parts[1]] {
			recs = append(recs, record{seq: seq, kind: parts[1], payload: []byte(parts[2])})
			if parts[1] == recSessionFinish {
				finished = true
			}
			continue
		}
		unknown++ // rule 5: ignored and counted
	}
	// Rule 3: exactly one session_start, at seq 1.
	if len(recs) == 0 || recs[0].kind != recSessionStart || recs[0].seq != 1 {
		return unusable("stream has no session_start at seq 1")
	}
	for _, r := range recs[1:] {
		if r.kind == recSessionStart {
			return unusable("stream has a second session_start at seq %d", r.seq)
		}
	}
	out := framed{recs: recs}
	if afterFinish > 0 {
		out.notes += fmt.Sprintf("mvo: oracle: %d evidence record(s) after session_finish discarded\n", afterFinish)
	}
	if unknown > 0 {
		out.notes += fmt.Sprintf("mvo: oracle: %d unknown evidence record kind(s) ignored\n", unknown)
	}
	return out
}

// ParseStream applies the normative parse rules over raw stream bytes and
// derives the metrics. It is PURE: no I/O, no clock — the FIFO fixtures in
// testdata/stream/ drive it directly, with no Python and no plugins.
//
// Any violation of rules 1, 2, 3 or 6 makes the stream UNUSABLE, which
// makes every stream-derived metric absent. Rule 7 (no session_finish)
// makes it incomplete, which does the same thing and additionally feeds
// S1's "a 0-exit with no usable stream is error, never pass".
func ParseStream(raw []byte, nonce string, over bool) Stream {
	s := Stream{Raw: raw}
	f := parseFrames(raw, nonce, over, streamKinds)
	if f.reason != "" {
		s.Usable, s.Reason = false, f.reason
		return s
	}
	s.Notes = f.notes
	s.Usable = true
	deriveStream(&s, f.recs)
	if !s.Complete {
		// Rule 7: an absent session_finish is INCOMPLETE. The prefix we
		// received is honest, but a run that never said it finished may
		// have been killed mid-suite, so no tally derived from it is
		// admissible. The derived fields are dropped rather than reported
		// alongside "unusable": a number a caller could still read is a
		// number a caller will eventually read.
		raw, notes := s.Raw, s.Notes
		s = Stream{Raw: raw, Notes: notes}
		s.Reason = "evidence stream has no session_finish"
	}
	return s
}

// deriveStream computes the metric derivation table over the records.
//
//	collected_total          the collected record's count
//	tests_total              distinct nodeids with >= 1 test record
//	tests_{passed,failed,…}  each nodeid's HIGHEST-seq outcome
//	tests_failed_first_run   nodeids whose LOWEST-seq outcome is failed|error
//	tests_passed_after_rerun nodeids that first failed and finally passed
//	duration_ms              session_finish.duration_ms
func deriveStream(s *Stream, recs []record) {
	type node struct {
		firstSeq, lastSeq int64
		first, last       string
	}
	nodes := map[string]*node{}
	order := []string{}
	for _, r := range recs {
		switch r.kind {
		case recCollected:
			var p struct {
				Count int64 `json:"count"`
			}
			if json.Unmarshal(r.payload, &p) == nil {
				s.Collected, s.HasCollected = p.Count, true
			}
		case recTest:
			var p struct {
				NodeID  string `json:"nodeid"`
				Outcome string `json:"outcome"`
			}
			if json.Unmarshal(r.payload, &p) != nil || p.NodeID == "" {
				continue
			}
			n, ok := nodes[p.NodeID]
			if !ok {
				n = &node{firstSeq: r.seq, first: p.Outcome}
				nodes[p.NodeID] = n
				order = append(order, p.NodeID)
			}
			if r.seq >= n.lastSeq {
				n.lastSeq, n.last = r.seq, p.Outcome
			}
		case recPropertyCase:
			// One Hypothesis example. `invalid` and `gave_up` are the
			// honest-degradation statuses PBT usually hides: a property
			// whose assume() filters rejected almost every draw has
			// SEARCHED NOTHING while reporting a pass.
			var p struct {
				Property string `json:"property"`
				Status   string `json:"status"`
			}
			if json.Unmarshal(r.payload, &p) != nil {
				continue
			}
			s.HasPropertyCases = true
			s.PropertyCases++
			if p.Status == propertyInvalid || p.Status == propertyGaveUp {
				s.PropertyCasesInvalid++
			}
		case recSessionFinish:
			var p struct {
				DurationMS int64 `json:"duration_ms"`
				Errored    int64 `json:"errored"`
				ExitStatus int   `json:"exitstatus"`
				Failed     int64 `json:"failed"`
				Skipped    int64 `json:"skipped"`
				Total      int64 `json:"total"`
			}
			if json.Unmarshal(r.payload, &p) != nil {
				continue
			}
			s.Complete = true
			s.DurationMS = p.DurationMS
			s.ExitStatus = p.ExitStatus
			s.FinishTotal, s.FinishFailed = p.Total, p.Failed
			s.FinishErrored, s.FinishSkipped = p.Errored, p.Skipped
		}
	}
	sort.Strings(order)
	for _, id := range order {
		n := nodes[id]
		s.Total++
		switch bucketOf(n.last) {
		case outcomePassed:
			s.Passed++
		case outcomeFailed:
			s.Failed++
		case outcomeError:
			s.Errored++
		case outcomeSkipped:
			s.Skipped++
		}
		switch bucketOf(n.first) {
		case outcomeFailed, outcomeError:
			s.FailedFirstRun++
			if bucketOf(n.last) == outcomePassed {
				s.PassedAfterRun++
			}
		}
	}
}

// bucketOf maps a wire outcome onto its JUnit equivalence class.
func bucketOf(outcome string) string {
	switch outcome {
	case outcomeXPassed:
		return outcomePassed
	case outcomeXFailed:
		return outcomeSkipped
	case outcomePassed, outcomeFailed, outcomeError, outcomeSkipped:
		return outcome
	default:
		return ""
	}
}
