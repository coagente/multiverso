package oracle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/policy"
)

// O2p — the property oracle, and the observability parser it refuses to
// derive metrics from.
//
// The rung is an ordinary pytest run: the repository's own @given tests
// plus the policy-declared property module, under the control-plane
// observer. What is new is the CASE-LEVEL evidence, and the whole of
// decision 15 is about where it came from:
//
//   - through the observability callback onto our stream ⇒ the records
//     carry the stream's S1/S2/S3 protections and property_cases_* are
//     METRICS;
//   - through Hypothesis's own .hypothesis/observed/*.jsonl ⇒ the records
//     are candidate-authorable AFTER EXIT (coverage_bp's status, M1f
//     decision 12), so the file is stored as an ARTIFACT, result.tools
//     says `jsonl`, and the metrics are ABSENT.
//
// One metric name, one provenance, forever. A metric whose trustworthiness
// varied silently by code path would be worse than no metric — the reader
// could not tell which one they had.

// ToolObservability is the result.tools key that names WHICH of the two
// paths produced the per-case records, and ToolObsStream / ToolObsJSONL are
// its only two values. It is the field a reviewer checks before believing
// a case count, and it is why the count is absent under `jsonl`.
const (
	ToolObservability = "hypothesis-observability"
	ToolObsStream     = "stream"
	ToolObsJSONL      = "jsonl"
)

// Hypothesis example statuses. `invalid` and `gave_up` both mean the search
// rejected the draw rather than running the property.
const (
	propertyPassed  = "passed"
	propertyFailed  = "failed"
	propertyInvalid = "invalid"
	propertyGaveUp  = "gave_up"
)

// envObservability turns Hypothesis's experimental observability on. It is
// pointed at CONTROL-PLANE SCRATCH rather than left to default into the
// worktree's .hypothesis/ — not because that makes the JSONL trustworthy
// (it does not, and no metric derives from it) but because an evidence
// artifact written into the candidate's tree is an artifact the NEXT rung's
// tree-guard has to explain.
const envObservability = "HYPOTHESIS_EXPERIMENTAL_OBSERVABILITY"

// artObservability names the stored JSONL artifact.
const artObservability = "hypothesis-observability"

// obsDirName is the directory Hypothesis writes its JSONL into, under
// whichever root it was pointed at.
const obsDirName = "observed"

// propertiesArgv is O2p's invocation:
//
//	[<python> -m coverage run] <prefix> -p mvo_evidence -p no:cacheprovider
//	  --junit-xml=<scratch>/junit.xml [<the declared property module>] [args…]
//
// The declared module comes FIRST among the positional arguments, so a
// policy that names one always runs it even if the repository's own tests
// are elsewhere. It is a harness-frozen path (decision 14): a candidate
// that edits it dies at rung O-1, before this argv is ever built.
func (o *pytestOracle) propertiesPlan(tools map[string]string) suitePlan {
	var p suitePlan
	base := o.prefix()
	if o.spec.Coverage && tools[ToolCoverage] != "" && policy.CoverageWrappable(base) {
		base, p.coverage = wrapCoverage(base), true
	}
	argv := append(base, o.pluginFlags()...)
	argv = append(argv, "--junit-xml="+o.junitPath(), "-p", "no:cacheprovider")
	if mod := o.spec.Corpus.Module; mod != "" {
		argv = append(argv, mod)
	}
	p.argv = append(argv, o.spec.Args...)
	return p
}

// observabilityRoot is where the run is told to write its JSONL: the
// control-plane scratch directory when there is one, the in-tree oracle
// workdir otherwise (the in-tree regime, where nothing else is outside the
// tree either).
func (o *pytestOracle) observabilityRoot() string {
	if s := o.ev.inWorldScrap; s != "" {
		return s
	}
	if o.ev.hostScratch != "" {
		return filepath.ToSlash(o.ev.hostScratch)
	}
	return o.workRel()
}

// propertiesEnv adds the observability switch to the run's environment.
func (o *pytestOracle) propertiesEnv(streamPath, nonce string) []string {
	return append(o.streamEnv(streamPath, nonce), envObservability+"="+o.observabilityRoot())
}

// hostObservabilityDir is where the control plane reads the JSONL back
// from.
func (o *pytestOracle) hostObservabilityDir(worldDir string) string {
	if o.ev.hostScratch != "" {
		return filepath.Join(o.ev.hostScratch, obsDirName)
	}
	return joinRel(worldDir, path.Join(o.workRel(), obsDirName))
}

// readObservability collects the JSONL Hypothesis wrote, under the artifact
// cap, sorted by file name so two runs of the same session produce the same
// bytes. Absence yields nil and no note: a run with no observability output
// is the ordinary case, not an incident.
func (o *pytestOracle) readObservability(worldDir string) ([]byte, string) {
	dir := o.hostObservabilityDir(worldDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []byte
	notes := ""
	for _, name := range names {
		b, note := o.readCapped(filepath.Join(dir, name), path.Join(obsDirName, name))
		notes += note
		if b == nil {
			continue
		}
		out = append(out, b...)
		if len(b) > 0 && b[len(b)-1] != '\n' {
			out = append(out, '\n')
		}
	}
	return out, notes
}

// obsSummary is what the JSONL parser CAN see. It is deliberately not a
// metric: the oracle stores it in a note so a reader knows the records
// existed and were understood, and emits nothing from it.
type obsSummary struct {
	Cases   int64
	Invalid int64
	// Unparsed counts lines that are not recognizable test_case records —
	// the signal that a Hypothesis version changed its shape. A parser
	// that quietly returned 0 cases for an unrecognized dialect would look
	// exactly like a property that searched nothing.
	Unparsed int64
}

// obsRecord is Hypothesis's observability line shape.
type obsRecord struct {
	Type     string `json:"type"`
	Status   string `json:"status"`
	Property string `json:"property"`
}

// ParseObservability reads Hypothesis's JSONL observability output. It is
// PURE, and it is the parser whose output MUST NOT become a metric
// (decision 15): callers use it to say "the records were there and we
// understood them", never to count anything a gate reads.
//
// An input with no recognizable test_case record yields an error, so an
// unrecognized version degrades to ABSENCE with a reason — never to a
// fabricated count of zero.
func ParseObservability(b []byte) (obsSummary, error) {
	var sum obsSummary
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec obsRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Type == "" {
			sum.Unparsed++
			continue
		}
		if rec.Type != "test_case" {
			continue // info/alert records: not examples
		}
		sum.Cases++
		if rec.Status == propertyInvalid || rec.Status == propertyGaveUp {
			sum.Invalid++
		}
	}
	if sum.Cases == 0 {
		return sum, fmt.Errorf("no test_case record in %d line(s) of hypothesis observability output", sum.Unparsed)
	}
	return sum, nil
}

// finishProperties is O2p's run: the same channel, artifact pipeline and
// S1/S2 rules as the suite rung, plus the case-level provenance decision.
func (o *pytestOracle) finishProperties(ctx context.Context, w backend.World, tools map[string]string,
	probeKey string, plan suitePlan, ch *evidenceChannel, nonce string) (runOutcome, error) {
	r := runOutcome{
		argv:    plan.argv,
		metrics: map[string]int64{},
		tools:   map[string]string{},
		regime:  o.ev.regime,
	}
	if ch != nil {
		r.plugin = o.ev.pluginDigest
	}
	r.proc = runInWorld(ctx, w, plan.argv, o.worldEnv(w, o.propertiesEnv(o.inWorldStream(), nonce)))
	r.status = r.proc.Status

	// Report extraction, never verification: whatever it does, the
	// receipt's status and exit code are the property run's.
	coverageExtracted := false
	if plan.coverage {
		coverageExtracted = runInWorld(ctx, w, o.coverageJSONArgv(), o.worldEnv(w, nil)).Status == StatusPass
	}

	stream := o.closeStream(ch, nonce)
	notes := stream.Notes

	obsBytes, note := o.readObservability(w.Dir())
	notes += note
	var coverageBytes []byte
	if coverageExtracted {
		coverageBytes, note = o.readCapped(o.hostCrossPath(w.Dir(), "coverage.json"), o.coveragePath())
		notes += note
	}

	// EP-7 order, unchanged: every artifact reaches CAS before anything
	// parses it.
	stdoutKey, err := o.store.Put(r.proc.Stdout)
	if err != nil {
		return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStdout, err)
	}
	streamKey := ""
	if ch != nil {
		if streamKey, err = o.store.Put(stream.Raw); err != nil {
			return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStream, err)
		}
		if !stream.Usable {
			notes += "mvo: oracle: " + stream.Reason + "\n"
		}
	}
	coverageKey := ""
	if coverageBytes != nil {
		if coverageKey, err = o.store.Put(coverageBytes); err != nil {
			return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artCoverage, err)
		}
	}
	obsKey := ""
	if obsBytes != nil {
		if obsKey, err = o.store.Put(obsBytes); err != nil {
			return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artObservability, err)
		}
	}

	reason := o.propertiesVerdict(&r, stream)
	if reason != "" {
		notes += "mvo: oracle: " + reason + "\n"
		r.notes = reason
	}

	// The provenance decision, in one place.
	switch {
	case stream.Usable && stream.HasPropertyCases:
		r.metrics[MetricPropertyCasesTotal] = stream.PropertyCases
		r.metrics[MetricPropertyCasesInvalid] = stream.PropertyCasesInvalid
		r.tools[ToolObservability] = ToolObsStream
	case obsBytes != nil:
		// The JSONL fallback. The records are stored and NAMED, and no
		// metric derives from them: they were writable by the process
		// after it stopped being observed.
		r.tools[ToolObservability] = ToolObsJSONL
		sum, err := ParseObservability(obsBytes)
		if err != nil {
			notes += fmt.Sprintf("mvo: oracle: hypothesis observability output stored as an artifact but not understood (%v); property_cases_* absent\n", err)
			break
		}
		notes += fmt.Sprintf("mvo: oracle: hypothesis observability arrived as JSONL (%d case(s), %d invalid), which is candidate-authorable after exit; stored as an artifact and property_cases_* are ABSENT (M2a decision 15)\n",
			sum.Cases, sum.Invalid)
	}

	stderrKey, err := o.store.Put(append(append([]byte(nil), r.proc.Stderr...), notes...))
	if err != nil {
		return runOutcome{}, fmt.Errorf("oracle: %s: store %s: %w", o.kind, artStderr, err)
	}
	r.artifacts = []string{stdoutKey, stderrKey, probeKey}
	if streamKey != "" {
		r.artifacts = append(r.artifacts, streamKey)
	}
	if coverageKey != "" {
		r.artifacts = append(r.artifacts, coverageKey)
	}
	if obsKey != "" {
		r.artifacts = append(r.artifacts, obsKey)
	}
	if coverageBytes != nil {
		if bp, err := parseCoverageBP(coverageBytes); err == nil {
			// coverage_bp comes from a data file the TEST PROCESS writes:
			// candidate-influenceable under every regime, labelled rather
			// than banned (M1f decision 12), and out of the default policy.
			r.metrics[MetricCoverageBP] = bp
			r.tools[ToolCoverage] = tools[ToolCoverage]
		}
	}
	if v := tools[ToolHypothesis]; v != "" {
		r.tools[ToolHypothesis] = v
	}
	if v := tools[ToolPytest]; v != "" {
		r.tools[ToolPytest] = v
	}
	return r, nil
}

// propertiesVerdict applies S1 and S2 and writes the stream-derived
// metrics. A property IS a test node id here: `properties_total` is the
// number of property tests the run reported, not the number of examples
// they drew — that is `property_cases_total`, and conflating the two is how
// a suite of three properties that searched nothing reads like a suite that
// searched three hundred examples.
func (o *pytestOracle) propertiesVerdict(r *runOutcome, stream Stream) string {
	if !stream.Usable {
		// S1 — absence never passes.
		if r.proc.ExitCode == 0 {
			r.status = StatusError
			return fmt.Sprintf("no usable evidence stream (%s)", stream.Reason)
		}
		r.status = StatusFail
		return ""
	}
	r.tools[ToolEvidence] = StreamVersion
	r.metrics[MetricPropertiesTotal] = stream.Total
	r.metrics[MetricPropertiesPassed] = stream.Passed
	r.metrics[MetricPropertiesFailed] = stream.Failed
	r.metrics[MetricPropertiesErrored] = stream.Errored
	r.metrics[MetricDurationMS] = stream.DurationMS

	// S2 — the exit code is cross-examined, not trusted.
	if r.proc.ExitCode == 0 && (stream.Failed > 0 || stream.Errored > 0) {
		r.status = StatusError
		return fmt.Sprintf("exit_code=0 but the evidence stream reports failed=%d errored=%d",
			stream.Failed, stream.Errored)
	}
	if stream.ExitStatus != r.proc.ExitCode {
		r.status = StatusError
		return fmt.Sprintf("evidence stream reports exitstatus=%d but the process exited %d",
			stream.ExitStatus, r.proc.ExitCode)
	}
	return ""
}
