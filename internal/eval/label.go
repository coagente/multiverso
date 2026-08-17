package eval

// THE LABEL STORE (§1, decision 4).
//
// A label is the hidden oracle's answer for one candidate on one instance. It
// is the only thing in this project that knows whether a candidate was
// actually correct, and four properties keep it worth having:
//
//  1. IT IS DERIVED FROM AGREEMENT, NEVER FROM ONE SIGNAL. Scoring executes
//     candidate code — that is the point of it — so the candidate gets to run
//     during its own labelling. The verdict therefore comes from the
//     AGREEMENT of exit code, structured report, and a per-run nonce echoed
//     in that report. Any disagreement yields `unknown` and NEVER `correct`.
//     This is the cross-oracle count invariant the adversarial corpus
//     computes, pointed at the labeller.
//
//  2. CONTROLS BIND PER BATCH. Every scoring batch re-runs §1.3's positive
//     (gold passes) and negative (base tree fails F2P, passes P2P) controls.
//     If either moves inside a batch, the WHOLE BATCH's labels become
//     `unknown`. Toolchain drift silently converts a `correct` into an
//     `incorrect` and would surface in FAR as if it were a scheduler effect.
//
//  3. IT IS APPLIED AFTER THE DECISION IS RECORDED. Store.Apply refuses a
//     label set that is not accompanied by a SEAL — the digest and sequence
//     number of a decision already in the ledger. The ordering claim is
//     process-structural (the scorer runs after the racer exits) and this is
//     its machine-checked half.
//
//  4. IT IS A PURE FUNCTION OF (world tree, oracle digest, env digest,
//     observation). Two scorings of the same triple write BYTE-IDENTICAL
//     label files, which is acceptance step m2d-7d. That is why no wall
//     clock, no duration, no nonce value and no decision digest is in the
//     label: those are properties of the RUN, they live in the run manifest,
//     and putting them here would make the determinism assertion untestable
//     and therefore untested.

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
)

// The three-value verdict vocabulary. It is CLOSED, and `unknown` is a real
// answer rather than a missing one.
const (
	VerdictCorrect   = "correct"
	VerdictIncorrect = "incorrect"
	VerdictUnknown   = "unknown"
)

// Label reasons. A closed vocabulary, because "why is this unknown" is the
// question every surprising FAR leads to.
const (
	ReasonPass             = "f2p-pass+p2p-pass"
	ReasonF2PFail          = "f2p-fail"
	ReasonP2PRegress       = "p2p-regress"
	ReasonExitDisagree     = "cross-check-exit-disagreement"
	ReasonNonceMissing     = "cross-check-nonce-missing"
	ReasonReportUnparsed   = "cross-check-report-unparsed"
	ReasonNodesMissing     = "cross-check-nodes-missing"
	ReasonTimeout          = "suite-timeout"
	ReasonRunnerError      = "runner-error"
	ReasonControlDrift     = "control-drift"
	ReasonToolAbsent       = "tool-absent"
	ReasonNotReconstructed = "world-not-reconstructed"
)

// Node-set class names in a hidden suite's report. The hidden runner tags
// every case with the set it belongs to, because "which of these was a
// fail_to_pass" cannot be recovered from a name after the fact.
const (
	ClassF2P = "f2p"
	ClassP2P = "p2p"
)

// Observation is everything one hidden-suite run produced. It is the input to
// the pure judge, and it is recorded verbatim in the run manifest so a label
// can be re-derived without re-running anything.
type Observation struct {
	// Nonce is what the scorer generated for THIS run and passed to the
	// runner. The report must echo it; a report that does not is a report
	// that might be a leftover, a plant, or a replay.
	Nonce string `json:"nonce"`
	// ExitCode is the runner's exit status; TimedOut and RunnerErr record
	// the two ways there is no usable exit status at all.
	ExitCode      int    `json:"exit_code"`
	TimedOut      bool   `json:"timed_out"`
	RunnerErr     string `json:"runner_err"`
	ReportXML     []byte `json:"-"`
	ReportKey     string `json:"report_key"` // CAS key of the report bytes
	Reconstructed bool   `json:"reconstructed"`
	DurationMS    int64  `json:"duration_ms"`
}

// SuiteReport is the parsed structured half of the cross-check.
type SuiteReport struct {
	Parsed     bool            `json:"parsed"`
	ParseError string          `json:"parse_error"`
	Nonce      string          `json:"nonce"`
	Outcomes   map[string]bool `json:"outcomes"` // "<class>::<name>" -> passed
	Failures   []string        `json:"failures"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
}

type junitCase struct {
	ClassName string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Failure   *junitFailure `xml:"failure"`
	Error     *junitFailure `xml:"error"`
	Skipped   *struct{}     `xml:"skipped"`
}

type junitSuite struct {
	XMLName xml.Name    `xml:"testsuite"`
	Nonce   string      `xml:"nonce,attr"`
	Cases   []junitCase `xml:"testcase"`
}

// ParseSuiteReport parses the hidden runner's report. It is total: a report
// that does not parse comes back with Parsed=false and the reason, which the
// judge turns into `unknown` rather than into a crash.
func ParseSuiteReport(b []byte) SuiteReport {
	r := SuiteReport{Outcomes: map[string]bool{}}
	if len(b) == 0 {
		r.ParseError = "empty report"
		return r
	}
	var s junitSuite
	if err := xml.Unmarshal(b, &s); err != nil {
		r.ParseError = err.Error()
		return r
	}
	r.Parsed = true
	r.Nonce = s.Nonce
	for _, c := range s.Cases {
		key := c.ClassName + "::" + c.Name
		passed := c.Failure == nil && c.Error == nil && c.Skipped == nil
		r.Outcomes[key] = passed
		if !passed {
			r.Failures = append(r.Failures, key)
		}
	}
	sort.Strings(r.Failures)
	return r
}

// Label is one candidate's labelled outcome. Every field is a deterministic
// function of the (tree, oracle, env, observation) tuple.
type Label struct {
	Schema    string `json:"schema"`
	Instance  string `json:"instance"`
	Candidate string `json:"candidate"`
	Source    string `json:"source"`
	Verdict   string `json:"verdict"`
	Tier      int    `json:"tier"`
	Reason    string `json:"reason"`
	// The counts behind the verdict. Absent counts are absent: a run whose
	// report did not parse reports totals of 0 and says so in Reason, and
	// the metric path reads Verdict rather than reconstructing it here.
	F2PPassed int `json:"f2p_passed"`
	F2PTotal  int `json:"f2p_total"`
	P2PPassed int `json:"p2p_passed"`
	P2PTotal  int `json:"p2p_total"`
	// The identity of what was judged. WorldTree is the tree the scorer
	// reconstructed, which is the world's own tree digest — the join back
	// to the ledger.
	WorldTree    string `json:"world_tree"`
	OracleDigest string `json:"oracle_digest"`
	EnvDigest    string `json:"env_digest"`
	ExitCode     int    `json:"exit_code"`
	NonceEchoed  bool   `json:"nonce_echoed"`
	ControlsOK   bool   `json:"controls_ok"`
	// Expected is the generator's cross-check only, copied here so the
	// expectation-violated census is computable from labels alone.
	Expected string `json:"expected"`
}

// JudgeInput is the pure judge's argument.
type JudgeInput struct {
	Instance     string
	Candidate    Candidate
	Hidden       HiddenOracle
	Obs          Observation
	WorldTree    string
	EnvDigest    string
	OracleDigest string
}

// Judge is the label function. PURE and TOTAL: it never touches the
// filesystem, never runs anything, and returns a verdict for every input
// including the ones that describe a broken run.
//
// The one rule that matters: a disagreement between the three signals yields
// `unknown`, never `correct`. A labeller that resolved disagreements in
// favour of `correct` would launder exactly the attacks M1f exists to stop.
func Judge(in JudgeInput) Label {
	l := Label{
		Schema:       SchemaLabel,
		Instance:     in.Instance,
		Candidate:    in.Candidate.ID,
		Source:       in.Candidate.Source,
		Expected:     in.Candidate.Expected,
		Tier:         in.Hidden.Tier,
		WorldTree:    in.WorldTree,
		OracleDigest: in.OracleDigest,
		EnvDigest:    in.EnvDigest,
		ExitCode:     in.Obs.ExitCode,
		F2PTotal:     len(in.Hidden.FailToPass),
		P2PTotal:     len(in.Hidden.PassToPass),
		ControlsOK:   true,
		Verdict:      VerdictUnknown,
	}
	if l.Tier == 0 {
		l.Tier = Tier1
	}
	switch {
	case !in.Obs.Reconstructed:
		l.Reason = ReasonNotReconstructed
		return l
	case in.Obs.TimedOut:
		l.Reason = ReasonTimeout
		return l
	case in.Obs.RunnerErr != "":
		l.Reason = ReasonRunnerError
		return l
	}
	rep := ParseSuiteReport(in.Obs.ReportXML)
	if !rep.Parsed {
		l.Reason = ReasonReportUnparsed
		return l
	}
	l.NonceEchoed = in.Obs.Nonce != "" && rep.Nonce == in.Obs.Nonce
	if !l.NonceEchoed {
		l.Reason = ReasonNonceMissing
		return l
	}
	// Every declared node must appear in the report. A report that silently
	// collected fewer tests than the oracle declares is the collected-count
	// attack (vector 09) aimed at the labeller.
	var missing []string
	for _, n := range in.Hidden.FailToPass {
		if _, ok := rep.Outcomes[ClassF2P+"::"+n]; !ok {
			missing = append(missing, ClassF2P+"::"+n)
		}
	}
	for _, n := range in.Hidden.PassToPass {
		if _, ok := rep.Outcomes[ClassP2P+"::"+n]; !ok {
			missing = append(missing, ClassP2P+"::"+n)
		}
	}
	if len(missing) > 0 {
		l.Reason = ReasonNodesMissing
		return l
	}
	for _, n := range in.Hidden.FailToPass {
		if rep.Outcomes[ClassF2P+"::"+n] {
			l.F2PPassed++
		}
	}
	for _, n := range in.Hidden.PassToPass {
		if rep.Outcomes[ClassP2P+"::"+n] {
			l.P2PPassed++
		}
	}
	allPass := l.F2PPassed == l.F2PTotal && l.P2PPassed == l.P2PTotal
	// The exit-code cross-check: a clean exit must agree with a clean
	// report, and a dirty exit must agree with a dirty one.
	if (in.Obs.ExitCode == 0) != allPass {
		l.Reason = ReasonExitDisagree
		return l
	}
	switch {
	case allPass:
		l.Verdict = VerdictCorrect
		l.Reason = ReasonPass
	case l.F2PPassed < l.F2PTotal:
		l.Verdict = VerdictIncorrect
		l.Reason = ReasonF2PFail
	default:
		l.Verdict = VerdictIncorrect
		l.Reason = ReasonP2PRegress
	}
	return l
}

// ---------------------------------------------------------------------------
// Batch controls (decision 4)
// ---------------------------------------------------------------------------

// ControlOutcome is one batch's control result. Ran is recorded separately
// from OK because "the control passed" and "the control was not run" must
// never render identically — the same rule `mvo audit --cas-sweep=false`
// follows.
type ControlOutcome struct {
	NegativeRan bool   `json:"negative_ran"`
	NegativeOK  bool   `json:"negative_ok"`
	PositiveRan bool   `json:"positive_ran"`
	PositiveOK  bool   `json:"positive_ok"`
	Detail      string `json:"detail"`
}

// OK is true only when both controls RAN and both held.
func (c ControlOutcome) OK() bool {
	return c.NegativeRan && c.NegativeOK && c.PositiveRan && c.PositiveOK
}

// CheckControls evaluates §1.3's two controls from their observations.
//
//	negative — the base tree, unpatched: must FAIL >= 1 fail_to_pass and PASS
//	           all pass_to_pass. A base tree that already passes F2P means the
//	           instance's task is not the task.
//	positive — gold, test hunks stripped: must pass both sets.
//
// Either failing drops the instance as gold-fails-control, with the observed
// counts kept. That is not ceremony: automated curation leaves task-validity
// noise, and dropping invalid instances is the only defence available without
// humans.
// whyNoReport renders what the runner itself said, so a control failure names
// its cause instead of only its symptom. "empty report" alone was the whole
// diagnosis of a child that never started, which cost a CI cycle to work out.
func whyNoReport(o Observation) string {
	var parts []string
	if o.RunnerErr != "" {
		parts = append(parts, o.RunnerErr)
	}
	if o.TimedOut {
		parts = append(parts, "runner timed out")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

func CheckControls(h HiddenOracle, base, gold Observation) ControlOutcome {
	out := ControlOutcome{}
	var details []string
	if base.Reconstructed || base.RunnerErr != "" || base.TimedOut || len(base.ReportXML) > 0 {
		out.NegativeRan = true
		rep := ParseSuiteReport(base.ReportXML)
		switch {
		case !rep.Parsed:
			details = append(details, "negative control report did not parse: "+rep.ParseError+whyNoReport(base))
		case base.Nonce != "" && rep.Nonce != base.Nonce:
			details = append(details, "negative control report did not echo the nonce")
		default:
			f2pFailed := 0
			for _, n := range h.FailToPass {
				if passed, ok := rep.Outcomes[ClassF2P+"::"+n]; ok && !passed {
					f2pFailed++
				}
			}
			p2pPassed := 0
			for _, n := range h.PassToPass {
				if rep.Outcomes[ClassP2P+"::"+n] {
					p2pPassed++
				}
			}
			out.NegativeOK = f2pFailed >= 1 && p2pPassed == len(h.PassToPass)
			if !out.NegativeOK {
				details = append(details, fmt.Sprintf(
					"negative control: %d/%d fail_to_pass failed (want >= 1), %d/%d pass_to_pass passed (want all)",
					f2pFailed, len(h.FailToPass), p2pPassed, len(h.PassToPass)))
			}
		}
	} else {
		details = append(details, "negative control was not run")
	}
	if gold.Reconstructed || gold.RunnerErr != "" || gold.TimedOut || len(gold.ReportXML) > 0 {
		out.PositiveRan = true
		rep := ParseSuiteReport(gold.ReportXML)
		switch {
		case !rep.Parsed:
			details = append(details, "positive control report did not parse: "+rep.ParseError+whyNoReport(gold))
		case gold.Nonce != "" && rep.Nonce != gold.Nonce:
			details = append(details, "positive control report did not echo the nonce")
		default:
			ok := true
			for _, n := range h.FailToPass {
				if !rep.Outcomes[ClassF2P+"::"+n] {
					ok = false
				}
			}
			for _, n := range h.PassToPass {
				if !rep.Outcomes[ClassP2P+"::"+n] {
					ok = false
				}
			}
			out.PositiveOK = ok && gold.ExitCode == 0
			if !out.PositiveOK {
				details = append(details, fmt.Sprintf(
					"positive control: gold does not pass the hidden suite (exit %d, failures %v)",
					gold.ExitCode, rep.Failures))
			}
		}
	} else {
		details = append(details, "positive control was not run")
	}
	out.Detail = strings.Join(details, "; ")
	return out
}

// ApplyControls is unknown-propagation at batch scope: if the controls did
// not hold, every label in the batch becomes `unknown` with reason
// control-drift. It returns a NEW slice; the input is not mutated, so the
// pre-control labels stay available for the run manifest, which is what makes
// "the controls moved and here is what we would have said" reportable.
func ApplyControls(labels []Label, c ControlOutcome) []Label {
	out := make([]Label, len(labels))
	copy(out, labels)
	if c.OK() {
		return out
	}
	for i := range out {
		out[i].Verdict = VerdictUnknown
		out[i].Reason = ReasonControlDrift
		out[i].ControlsOK = false
	}
	return out
}

// ---------------------------------------------------------------------------
// The seal: labels are applied only after a decision is recorded
// ---------------------------------------------------------------------------

// Seal witnesses that a race is over and its decision is in the ledger. It is
// the machine-checked half of decision 3's ordering claim.
//
// Timestamps corroborate; they do not prove. What proves it here is that a
// Seal cannot be constructed without a decision digest that was read back out
// of a sealed ledger, and Store.Apply refuses without one.
type Seal struct {
	Ledger         string `json:"ledger"`
	Intent         string `json:"intent"`
	DecisionDigest string `json:"decision_digest"`
	DecisionType   string `json:"decision_type"`
	DecisionSeq    int64  `json:"decision_seq"`
	Events         int64  `json:"events"`
	ChainVerified  bool   `json:"chain_verified"`
}

// Check names every reason a seal is not a seal.
func (s Seal) Check() error {
	var bad []string
	if s.DecisionDigest == "" {
		bad = append(bad, "no decision digest: a label may not be written before a decision is recorded")
	}
	if s.DecisionSeq <= 0 {
		bad = append(bad, "no decision sequence number")
	}
	if !s.ChainVerified {
		bad = append(bad, "ledger hash chain not verified: an unsealed ledger is not evidence of ordering")
	}
	if len(bad) > 0 {
		return fmt.Errorf("eval: refusing to apply labels: %s", strings.Join(bad, "; "))
	}
	return nil
}

// Apply writes a batch of labels, AFTER checking the seal. The seal is not
// written into the label files — a label is a statement about a tree and an
// oracle, not about a race — it is recorded in the run manifest, which is
// where the ordering proof belongs and where m2d-7d's byte-identity
// assertion is not broken by it.
func (s *Store) Apply(corpus, version string, seal Seal, labels []Label) error {
	if err := seal.Check(); err != nil {
		return err
	}
	for _, l := range labels {
		if l.Schema != SchemaLabel {
			return fmt.Errorf("eval: label for %s/%s: schema %q, want %q",
				l.Instance, l.Candidate, l.Schema, SchemaLabel)
		}
		switch l.Verdict {
		case VerdictCorrect, VerdictIncorrect, VerdictUnknown:
		default:
			return fmt.Errorf("eval: label for %s/%s: verdict %q is not in the closed vocabulary",
				l.Instance, l.Candidate, l.Verdict)
		}
		b, err := object.Canonical(l)
		if err != nil {
			return fmt.Errorf("eval: canonicalize label %s/%s: %w", l.Instance, l.Candidate, err)
		}
		p := s.LabelPath(corpus, version, l.Instance, sanitizeName(l.Candidate))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return fmt.Errorf("eval: create %s: %w", filepath.Dir(p), err)
		}
		if err := writeFileMode(p, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// LoadLabels reads every label recorded for an instance, keyed by candidate
// id. A missing directory is no labels, not an error.
func (s *Store) LoadLabels(corpus, version, instance string) (map[string]Label, error) {
	dir := filepath.Dir(s.LabelPath(corpus, version, instance, "x"))
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Label{}, nil
		}
		return nil, fmt.Errorf("eval: read labels %s: %w", dir, err)
	}
	out := map[string]Label{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") ||
			strings.HasSuffix(e.Name(), ".adjudication.json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("eval: read label %s: %w", e.Name(), err)
		}
		var l Label
		if err := json.Unmarshal(b, &l); err != nil {
			return nil, fmt.Errorf("eval: decode label %s: %w", e.Name(), err)
		}
		out[l.Candidate] = l
	}
	return out, nil
}

// Adjudication is Tier 3's file format. The RATERS are out of scope (§3 item
// 4: human labour), but the format ships so labels can be upgraded without
// re-racing a single instance, and the tier is carried on every label so no
// aggregate silently mixes tiers.
type Adjudication struct {
	Schema    string   `json:"schema"`
	Instance  string   `json:"instance"`
	Candidate string   `json:"candidate"`
	Verdict   string   `json:"verdict"`
	Tier      int      `json:"tier"`
	Raters    []string `json:"raters"`
	Agreement string   `json:"agreement"` // "unanimous" | "majority" | "split"
	Notes     string   `json:"notes"`
	// Signature is over the canonical bytes with the EVAL KEY — never the
	// admission key. The experiment must not be able to mint an admission.
	Signature string `json:"signature"`
	Signer    string `json:"signer"`
}

// WriteAdjudication records a Tier-3 verdict beside the Tier-1 label rather
// than overwriting it: a label that was upgraded must still show what the
// automated tier said, or the disagreement rate becomes unrecoverable.
func (s *Store) WriteAdjudication(corpus, version string, a Adjudication) error {
	if a.Schema != SchemaAdjudication {
		return fmt.Errorf("eval: adjudication schema %q, want %q", a.Schema, SchemaAdjudication)
	}
	if a.Tier != Tier3 {
		return fmt.Errorf("eval: adjudication for %s/%s: tier %d, want %d",
			a.Instance, a.Candidate, a.Tier, Tier3)
	}
	b, err := object.Canonical(a)
	if err != nil {
		return fmt.Errorf("eval: canonicalize adjudication: %w", err)
	}
	p := s.LabelPath(corpus, version, a.Instance, sanitizeName(a.Candidate)+".adjudication")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("eval: create %s: %w", filepath.Dir(p), err)
	}
	return writeFileMode(p, b, 0o600)
}

// sanitizeName makes a candidate id safe as a file name without losing
// injectivity for the ids this plane generates: operator@seed[params] maps to
// operator@seed_params_ and two distinct ids cannot collide because only
// path-hostile bytes are replaced, never removed.
func sanitizeName(id string) string {
	var sb strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == '@' || r == '+':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	return sb.String()
}
