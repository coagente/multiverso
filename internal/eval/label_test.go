package eval

// LABELS: the cross-check that never resolves a disagreement as `correct`, the
// batch controls that void a whole batch, and the ORDERING — labels are applied
// only after a decision is recorded, asserted rather than assumed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

func hiddenFor(f2p, p2p []string) HiddenOracle {
	return HiddenOracle{
		Schema: SchemaOracle, Instance: "i1",
		FailToPass: f2p, PassToPass: p2p,
		CanaryToken: "CANARY", CanaryID: "canary-x",
		Files: map[string]string{"mvo_hidden_run.py": "# CANARY"},
		Tier:  Tier1,
	}
}

// reportXML builds a hidden-suite report with the given outcomes.
func reportXML(nonce string, pass map[string]bool) []byte {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<testsuite name="mvo-hidden" nonce="` + nonce + `">` + "\n")
	for _, k := range sortedKeys(pass) {
		parts := strings.SplitN(k, "::", 2)
		if pass[k] {
			sb.WriteString(`  <testcase classname="` + parts[0] + `" name="` + parts[1] + `"/>` + "\n")
		} else {
			sb.WriteString(`  <testcase classname="` + parts[0] + `" name="` + parts[1] +
				`"><failure message="no">no</failure></testcase>` + "\n")
		}
	}
	sb.WriteString("</testsuite>\n")
	return []byte(sb.String())
}

func TestJudgeCorrectIncorrectAndTheThreeSignalCrossCheck(t *testing.T) {
	h := hiddenFor([]string{"f1", "f2"}, []string{"p1"})
	cand := Candidate{ID: "c1", Source: SourceGold, Expected: ExpectCorrect}
	base := JudgeInput{
		Instance: "i1", Candidate: cand, Hidden: h,
		WorldTree: "git:abc", EnvDigest: "mv0:env", OracleDigest: "sha256:oracle",
	}

	// All pass, exit 0, nonce echoed: correct.
	all := map[string]bool{"f2p::f1": true, "f2p::f2": true, "p2p::p1": true}
	in := base
	in.Obs = Observation{Nonce: "N", ExitCode: 0, ReportXML: reportXML("N", all), Reconstructed: true}
	l := Judge(in)
	if l.Verdict != VerdictCorrect || l.Reason != ReasonPass {
		t.Errorf("all-pass label = %s/%s", l.Verdict, l.Reason)
	}
	if l.F2PPassed != 2 || l.P2PPassed != 1 || !l.NonceEchoed {
		t.Errorf("counts wrong: %+v", l)
	}

	// One F2P fails, exit 1: incorrect, and the reason distinguishes it from
	// a pass_to_pass regression.
	f2pFail := map[string]bool{"f2p::f1": false, "f2p::f2": true, "p2p::p1": true}
	in.Obs = Observation{Nonce: "N", ExitCode: 1, ReportXML: reportXML("N", f2pFail), Reconstructed: true}
	if l := Judge(in); l.Verdict != VerdictIncorrect || l.Reason != ReasonF2PFail {
		t.Errorf("f2p-fail label = %s/%s", l.Verdict, l.Reason)
	}
	p2pFail := map[string]bool{"f2p::f1": true, "f2p::f2": true, "p2p::p1": false}
	in.Obs = Observation{Nonce: "N", ExitCode: 1, ReportXML: reportXML("N", p2pFail), Reconstructed: true}
	if l := Judge(in); l.Verdict != VerdictIncorrect || l.Reason != ReasonP2PRegress {
		t.Errorf("p2p-regress label = %s/%s", l.Verdict, l.Reason)
	}

	// THE CROSS-CHECK. Each disagreement in turn must yield `unknown`, and
	// NEVER `correct` — a labeller that resolved disagreements in favour of
	// correct would launder exactly the attacks M1f exists to stop.
	cases := []struct {
		name   string
		obs    Observation
		reason string
	}{
		{"exit 1 with an all-pass report", Observation{
			Nonce: "N", ExitCode: 1, ReportXML: reportXML("N", all), Reconstructed: true}, ReasonExitDisagree},
		{"exit 0 with a failing report", Observation{
			Nonce: "N", ExitCode: 0, ReportXML: reportXML("N", f2pFail), Reconstructed: true}, ReasonExitDisagree},
		{"a report echoing the wrong nonce", Observation{
			Nonce: "N", ExitCode: 0, ReportXML: reportXML("OTHER", all), Reconstructed: true}, ReasonNonceMissing},
		{"a report that does not parse", Observation{
			Nonce: "N", ExitCode: 0, ReportXML: []byte("<<<not xml"), Reconstructed: true}, ReasonReportUnparsed},
		{"no report at all", Observation{
			Nonce: "N", ExitCode: 0, Reconstructed: true}, ReasonReportUnparsed},
		{"a report missing a declared node", Observation{
			Nonce: "N", ExitCode: 0, Reconstructed: true,
			ReportXML: reportXML("N", map[string]bool{"f2p::f1": true, "p2p::p1": true})}, ReasonNodesMissing},
		{"a timeout", Observation{
			Nonce: "N", TimedOut: true, Reconstructed: true}, ReasonTimeout},
		{"a runner error", Observation{
			Nonce: "N", RunnerErr: "boom", Reconstructed: true}, ReasonRunnerError},
		{"a tree that was never reconstructed", Observation{Nonce: "N"}, ReasonNotReconstructed},
	}
	for _, c := range cases {
		in := base
		in.Obs = c.obs
		l := Judge(in)
		if l.Verdict != VerdictUnknown {
			t.Errorf("%s: verdict = %s, want unknown", c.name, l.Verdict)
		}
		if l.Verdict == VerdictCorrect {
			t.Fatalf("%s: a disagreement produced `correct`", c.name)
		}
		if l.Reason != c.reason {
			t.Errorf("%s: reason = %q, want %q", c.name, l.Reason, c.reason)
		}
	}
}

func TestJudgeIsPureAndCarriesNoWallClock(t *testing.T) {
	// m2d-7d's determinism, at the unit level: two Judges of the same
	// (tree, oracle, env, observation) produce BYTE-IDENTICAL label bytes.
	// That is only possible because the label carries no timestamp, no
	// duration and no nonce VALUE — those are properties of the run and they
	// live in the run manifest.
	h := hiddenFor([]string{"f1"}, []string{"p1"})
	obs := Observation{Nonce: "N", ExitCode: 0, Reconstructed: true, DurationMS: 123,
		ReportXML: reportXML("N", map[string]bool{"f2p::f1": true, "p2p::p1": true})}
	in := JudgeInput{Instance: "i1", Candidate: Candidate{ID: "c1"}, Hidden: h,
		Obs: obs, WorldTree: "git:abc", EnvDigest: "mv0:env", OracleDigest: "sha256:o"}
	b1, err := object.Canonical(Judge(in))
	if err != nil {
		t.Fatal(err)
	}
	// A different duration and a different nonce, same everything else.
	in.Obs.DurationMS = 999
	in.Obs.Nonce = "M"
	in.Obs.ReportXML = reportXML("M", map[string]bool{"f2p::f1": true, "p2p::p1": true})
	b2, err := object.Canonical(Judge(in))
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Errorf("the label is not a pure function of (tree, oracle, env, outcome):\n%s\n%s", b1, b2)
	}
	if strings.Contains(string(b1), "\"nonce\"") || strings.Contains(string(b1), "duration") ||
		strings.Contains(string(b1), "created_at") {
		t.Errorf("the label carries a per-run value: %s", b1)
	}
}

func TestControlsBindAndVoidTheWholeBatch(t *testing.T) {
	h := hiddenFor([]string{"f1"}, []string{"p1"})
	// The negative control must FAIL >= 1 f2p and PASS all p2p.
	goodNeg := Observation{Nonce: "N", ExitCode: 1, Reconstructed: true,
		ReportXML: reportXML("N", map[string]bool{"f2p::f1": false, "p2p::p1": true})}
	goodPos := Observation{Nonce: "N", ExitCode: 0, Reconstructed: true,
		ReportXML: reportXML("N", map[string]bool{"f2p::f1": true, "p2p::p1": true})}
	c := CheckControls(h, goodNeg, goodPos)
	if !c.OK() {
		t.Fatalf("valid controls did not hold: %+v", c)
	}

	// A base tree that already passes F2P means the instance's task is not
	// the task.
	badNeg := Observation{Nonce: "N", ExitCode: 0, Reconstructed: true,
		ReportXML: reportXML("N", map[string]bool{"f2p::f1": true, "p2p::p1": true})}
	c = CheckControls(h, badNeg, goodPos)
	if c.OK() {
		t.Errorf("a base tree that passes F2P was accepted")
	}
	if !strings.Contains(c.Detail, "negative control") {
		t.Errorf("the failure does not name the control: %q", c.Detail)
	}
	// Gold that does not pass the hidden suite drops the instance.
	badPos := Observation{Nonce: "N", ExitCode: 1, Reconstructed: true,
		ReportXML: reportXML("N", map[string]bool{"f2p::f1": false, "p2p::p1": true})}
	if CheckControls(h, goodNeg, badPos).OK() {
		t.Errorf("gold failing the hidden suite was accepted")
	}
	// A control that was NOT RUN must never render as one that passed.
	c = CheckControls(h, Observation{}, Observation{})
	if c.OK() || c.NegativeRan || c.PositiveRan {
		t.Errorf("unrun controls were treated as passing: %+v", c)
	}
	if !strings.Contains(c.Detail, "was not run") {
		t.Errorf("the detail does not say the controls were not run: %q", c.Detail)
	}

	// UNKNOWN PROPAGATION AT BATCH SCOPE: if either control moved, the WHOLE
	// batch becomes unknown, and a `correct` label never survives it.
	labels := []Label{
		{Schema: SchemaLabel, Candidate: "a", Verdict: VerdictCorrect, Reason: ReasonPass, ControlsOK: true},
		{Schema: SchemaLabel, Candidate: "b", Verdict: VerdictIncorrect, Reason: ReasonF2PFail, ControlsOK: true},
	}
	out := ApplyControls(labels, CheckControls(h, badNeg, goodPos))
	for _, l := range out {
		if l.Verdict != VerdictUnknown || l.Reason != ReasonControlDrift || l.ControlsOK {
			t.Errorf("control drift did not void label %s: %+v", l.Candidate, l)
		}
	}
	// The input is not mutated: the pre-control labels stay available for the
	// run manifest, which is what makes "the controls moved and here is what
	// we would have said" reportable.
	if labels[0].Verdict != VerdictCorrect {
		t.Errorf("ApplyControls mutated its input")
	}
	// And with controls holding, nothing changes.
	out = ApplyControls(labels, c2ok(h))
	if out[0].Verdict != VerdictCorrect || out[1].Verdict != VerdictIncorrect {
		t.Errorf("holding controls changed the labels: %+v", out)
	}
}

func c2ok(h HiddenOracle) ControlOutcome {
	return CheckControls(h,
		Observation{Nonce: "N", ExitCode: 1, Reconstructed: true,
			ReportXML: reportXML("N", map[string]bool{"f2p::f1": false, "p2p::p1": true})},
		Observation{Nonce: "N", ExitCode: 0, Reconstructed: true,
			ReportXML: reportXML("N", map[string]bool{"f2p::f1": true, "p2p::p1": true})})
}

func TestLabelsAreAppliedOnlyAfterADecisionIsRecorded(t *testing.T) {
	// THE ORDERING CLAIM, ASSERTED. Store.Apply refuses a label set that is
	// not accompanied by a seal — a decision digest and sequence number read
	// back out of a verified ledger.
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	labels := []Label{{Schema: SchemaLabel, Instance: "i1", Candidate: "c1",
		Verdict: VerdictCorrect, Reason: ReasonPass, Tier: Tier1}}

	// No seal at all.
	err = store.Apply(CorpusLocalDerived, LocalVersion, Seal{}, labels)
	if err == nil {
		t.Fatal("Apply wrote labels with no seal: a label may not exist before the decision it scores")
	}
	for _, want := range []string{"no decision digest", "no decision sequence number", "hash chain"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// A decision digest but an unverified chain: an unsealed ledger is not
	// evidence of ordering.
	err = store.Apply(CorpusLocalDerived, LocalVersion,
		Seal{DecisionDigest: "mv0:d", DecisionSeq: 7, ChainVerified: false}, labels)
	if err == nil {
		t.Fatal("Apply accepted a seal over an unverified ledger")
	}
	// A sequence number of zero is not a recorded decision.
	err = store.Apply(CorpusLocalDerived, LocalVersion,
		Seal{DecisionDigest: "mv0:d", DecisionSeq: 0, ChainVerified: true}, labels)
	if err == nil {
		t.Fatal("Apply accepted a decision with no sequence number")
	}
	// A complete seal writes, at mode 0600, and the file carries NO seal
	// field: the ordering proof belongs in the run manifest, and putting it
	// in the label would break the byte-identity assertion.
	seal := Seal{DecisionDigest: "mv0:d", DecisionType: "SELECT", DecisionSeq: 7,
		Events: 42, ChainVerified: true}
	if err := store.Apply(CorpusLocalDerived, LocalVersion, seal, labels); err != nil {
		t.Fatalf("Apply refused a complete seal: %v", err)
	}
	p := store.LabelPath(CorpusLocalDerived, LocalVersion, "i1", "c1")
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("no label was written: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("the label is mode %04o, want 0600", st.Mode().Perm())
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"mv0:d", "decision", "seal"} {
		if strings.Contains(string(b), forbidden) {
			t.Errorf("the label file carries %q: %s", forbidden, b)
		}
	}
	// TWO APPLICATIONS OF THE SAME LABEL PRODUCE BYTE-IDENTICAL FILES.
	first := append([]byte(nil), b...)
	if err := store.Apply(CorpusLocalDerived, LocalVersion, seal, labels); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(again) {
		t.Errorf("two applications wrote different bytes:\n%s\n%s", first, again)
	}
	// A verdict outside the closed vocabulary is refused.
	bad := []Label{{Schema: SchemaLabel, Instance: "i1", Candidate: "c2", Verdict: "probably"}}
	if err := store.Apply(CorpusLocalDerived, LocalVersion, seal, bad); err == nil {
		t.Errorf("Apply accepted a verdict outside the closed vocabulary")
	}
	// Round trip.
	got, err := store.LoadLabels(CorpusLocalDerived, LocalVersion, "i1")
	if err != nil {
		t.Fatal(err)
	}
	if got["c1"].Verdict != VerdictCorrect {
		t.Errorf("round trip lost the verdict: %+v", got)
	}
	// A missing label directory is no labels, not an error.
	if got, err := store.LoadLabels(CorpusLocalDerived, LocalVersion, "nope"); err != nil || len(got) != 0 {
		t.Errorf("a missing label dir errored: %v %v", got, err)
	}
}

func TestAdjudicationSitsBesideTheLabelAndNeedsTwoRaters(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	seal := Seal{DecisionDigest: "mv0:d", DecisionSeq: 1, ChainVerified: true}
	tier1 := []Label{{Schema: SchemaLabel, Instance: "i1", Candidate: "c1",
		Verdict: VerdictIncorrect, Reason: ReasonF2PFail, Tier: Tier1}}
	if err := store.Apply(CorpusLocalDerived, LocalVersion, seal, tier1); err != nil {
		t.Fatal(err)
	}
	a := Adjudication{Schema: SchemaAdjudication, Instance: "i1", Candidate: "c1",
		Verdict: VerdictCorrect, Tier: Tier3, Raters: []string{"r1", "r2"}, Agreement: "unanimous"}
	if err := store.WriteAdjudication(CorpusLocalDerived, LocalVersion, a); err != nil {
		t.Fatal(err)
	}
	// The Tier-1 label is NOT overwritten: a label that was upgraded must
	// still show what the automated tier said, or the disagreement rate
	// becomes unrecoverable.
	got, err := store.LoadLabels(CorpusLocalDerived, LocalVersion, "i1")
	if err != nil {
		t.Fatal(err)
	}
	if got["c1"].Verdict != VerdictIncorrect || got["c1"].Tier != Tier1 {
		t.Errorf("the adjudication overwrote the Tier-1 label: %+v", got["c1"])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(
		store.LabelPath(CorpusLocalDerived, LocalVersion, "i1", "c1")), "c1.adjudication.json")); err != nil {
		t.Errorf("no adjudication file beside the label: %v", err)
	}
	// A non-Tier-3 adjudication is refused.
	a.Tier = Tier1
	if err := store.WriteAdjudication(CorpusLocalDerived, LocalVersion, a); err == nil {
		t.Errorf("a Tier-1 adjudication was accepted")
	}
}

func TestParseSuiteReportIsTotal(t *testing.T) {
	for _, in := range [][]byte{nil, []byte(""), []byte("garbage"), []byte("<testsuite")} {
		r := ParseSuiteReport(in)
		if r.Parsed {
			t.Errorf("ParseSuiteReport(%q) claims to have parsed", in)
		}
		if r.ParseError == "" {
			t.Errorf("ParseSuiteReport(%q) gave no reason", in)
		}
	}
	r := ParseSuiteReport(reportXML("N", map[string]bool{"f2p::a": true, "p2p::b": false}))
	if !r.Parsed || r.Nonce != "N" {
		t.Fatalf("a valid report did not parse: %+v", r)
	}
	if !r.Outcomes["f2p::a"] || r.Outcomes["p2p::b"] {
		t.Errorf("outcomes wrong: %+v", r.Outcomes)
	}
	if len(r.Failures) != 1 || r.Failures[0] != "p2p::b" {
		t.Errorf("failures wrong: %+v", r.Failures)
	}
	// A skipped case is NOT a pass: a suite that skipped the test that
	// decides correctness has measured nothing.
	skipped := []byte(`<testsuite nonce="N"><testcase classname="f2p" name="a"><skipped/></testcase></testsuite>`)
	if ParseSuiteReport(skipped).Outcomes["f2p::a"] {
		t.Errorf("a skipped case counted as a pass")
	}
}
