package race

// M1e orchestrator tests: the policy-driven oracle ladder (decision 12),
// the base-state measurement (decision 13) and the pytest pre-flight
// (decision 15). No pytest and no plugin is required anywhere here — the
// pytest kinds are driven through a fake interpreter, exactly as the oracle
// package's own tests are, because a test that needs a plugin installed is a
// test that silently stops running.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// fakePython is a stand-in interpreter for the pytest kinds: it answers the
// tools probe, reports a collected count that depends on the world's own
// x.txt, and writes a JUnit file for the suite run. It is not an
// interpreter, not pytest, and never touches the network.
func fakePython(t *testing.T, reportPytest bool) string {
	t.Helper()
	probe := `{"pytest":"8.4.0"}`
	if !reportPytest {
		probe = `{}`
	}
	// Since M1f it also plays the part of the control-plane plugin: when
	// the evidence channel is present it writes the framed stream the
	// oracle derives its metrics from. That is the point — the
	// orchestrator tests exercise the REAL severed evidence path, with no
	// pytest and no plugin installed anywhere.
	script := `#!/bin/sh
if [ "$1" = "-c" ]; then printf '%s\n' '` + probe + `'; exit 0; fi
collect=""
junit=""
for a in "$@"; do
  case "$a" in
    --collect-only) collect=1 ;;
    --junit-xml=*) junit="${a#--junit-xml=}" ;;
  esac
done
n=2
if [ -f x.txt ] && [ "$(cat x.txt)" = "still-broken" ]; then n=1; fi
emit() { if [ -n "$MVO_EVIDENCE_STREAM" ]; then printf '%s' "$1" >> "$MVO_EVIDENCE_STREAM"; fi; }
emit "mvo-evidence/v0	$MVO_EVIDENCE_NONCE
1	session_start	{\"pid\":1}
"
if [ -n "$collect" ]; then
  echo "$n tests collected in 0.01s"
  emit "2	collected	{\"count\":$n}
3	session_finish	{\"duration_ms\":10,\"errored\":0,\"exitstatus\":0,\"failed\":0,\"passed\":0,\"skipped\":0,\"total\":0}
"
  exit 0
fi
emit "2	collected	{\"count\":$n}
"
seq=2
i=1
while [ "$i" -le "$n" ]; do
  seq=$((seq+1))
  emit "$seq	test	{\"duration_ms\":1,\"nodeid\":\"t.py::t$i\",\"outcome\":\"passed\",\"run\":1}
"
  i=$((i+1))
done
seq=$((seq+1))
emit "$seq	session_finish	{\"duration_ms\":100,\"errored\":0,\"exitstatus\":0,\"failed\":0,\"passed\":$n,\"skipped\":0,\"total\":$n}
"
if [ -n "$junit" ]; then
  printf '<testsuite name="p" tests="%s" failures="0" errors="0" skipped="0" time="0.100"></testsuite>' "$n" > "$junit"
fi
exit 0
`
	path := filepath.Join(t.TempDir(), "fakepython")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// ladderPolicy is the shipped default's shape driven by the fake
// interpreter, plus one declared-but-UNREQUIRED oracle: no gate, key or
// escalation rule names it, so the race must never run it (decision 12 —
// evidence waste is a measured PRD metric).
func ladderPolicy(python, spareMarker string) object.PolicyV1 {
	return object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "ladder",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{python, "-m", "pytest"}, Args: []string{}},
			{Name: "spare", Kind: policy.KindCommand, Argv: []string{"touch", spareMarker}, Args: []string{}},
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{python, "-m", "pytest"}, Args: []string{}},
		},
		HardGates: []object.GateSpec{
			{Gate: policy.GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateCollectedNotBelow, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{policy.KeyGatePass, policy.KeyTestsPassedDesc, policy.KeyWallMSAsc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}, OnAllWorldsFailedMachinery: true},
	}
}

// seedIntentV1 stores a v1 policy and an intent naming it.
func seedIntentV1(t *testing.T, store *cas.Store, repo string, pol object.PolicyV1, maxCandidates int) string {
	t.Helper()
	commit, tree, err := gitx.Head(repo)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	polDig, polCanon, err := object.Digest(pol)
	if err != nil {
		t.Fatalf("digest policy: %v", err)
	}
	if _, err := store.Put(polCanon); err != nil {
		t.Fatalf("store policy: %v", err)
	}
	if _, err := policy.Decode(polCanon); err != nil {
		t.Fatalf("the test policy does not validate: %v", err)
	}
	intent := object.Intent{
		Schema:    object.SchemaIntent,
		Base:      object.Base{Commit: commit, Tree: tree},
		Spec:      object.Spec{Title: "fix x", Description: "make x.txt say fixed"},
		Budget:    object.Budget{MaxCandidates: maxCandidates, MaxWallMS: 600000},
		Policy:    polDig,
		CreatedAt: fixedTime,
	}
	intentDig, intentCanon, err := object.Digest(intent)
	if err != nil {
		t.Fatalf("digest intent: %v", err)
	}
	if _, err := store.Put(intentCanon); err != nil {
		t.Fatalf("store intent: %v", err)
	}
	return intentDig
}

// newLadderConfig wires a v1-policy race over the fake interpreter.
func newLadderConfig(t *testing.T, pol object.PolicyV1, patches map[string]string) Config {
	t.Helper()
	repo := initRepo(t)
	store, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	led, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	t.Cleanup(func() { led.Close() })
	return Config{
		Repo:       repo,
		Ledger:     led,
		CAS:        store,
		Intent:     seedIntentV1(t, store, repo, pol, max(len(patches), 1)),
		Adapter:    mustAdapter(t, "script"),
		Candidates: scriptCands(patches),
		WorldsDir:  filepath.Join(t.TempDir(), "worlds"),
		Backend:    mustBackend(t),
		Parallel:   1,
	}
}

// eventsOfType returns the payloads of every event of one type.
func eventsOfType(t *testing.T, led *ledger.Ledger, typ string) [][]byte {
	t.Helper()
	var out [][]byte
	if err := led.Scan(func(e ledger.Event) error {
		if e.Type == typ {
			out = append(out, append([]byte(nil), e.Payload...))
		}
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return out
}

// The ladder: the required oracles run in ladder order, a world that fails
// gate 2 never pays for the suite oracle, and the declared-but-unrequired
// oracle never runs at all.
func TestRunLadderShortCircuitsAndSkipsUnrequiredOracles(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spare-ran")
	pol := ladderPolicy(fakePython(t, true), marker)
	cfg := newLadderConfig(t, pol, map[string]string{
		"a-fix.patch":   fixPatch,   // collects 2 → delta 0 → suite runs
		"b-nofix.patch": noFixPatch, // collects 1 → delta -1 → ladder stops
	})
	// One oracle instance per kind is shared by every worker, so the ladder
	// runs under -race here too: instances are immutable configuration, and
	// two worlds must never see each other's artifacts.
	cfg.Parallel = 2
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision.Type != TypeSelect {
		t.Fatalf("decision = %s (%s), want SELECT", res.Decision.Type, res.Decision.Rationale)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the declared-but-unrequired oracle ran: evidence waste is never implicit")
	}

	byOrdinal := map[int][]object.RecordedReceipt{}
	for _, w := range res.Worlds {
		byOrdinal[w.Ordinal] = w.Receipts
	}
	fixed, broken := byOrdinal[1], byOrdinal[2]
	if len(fixed) != 2 {
		t.Fatalf("the gate-passing world has %d receipts, want 2 (collect + suite)", len(fixed))
	}
	if got := []string{fixed[0].Receipt.Oracle.ID, fixed[1].Receipt.Oracle.ID}; got[0] != policy.KindPytestCollect || got[1] != policy.KindPytestSuite {
		t.Errorf("ladder order = %v, want [%s %s]", got, policy.KindPytestCollect, policy.KindPytestSuite)
	}
	if len(broken) != 1 {
		t.Fatalf("the gate-failing world has %d receipts, want 1 (the ladder stops at the first failed gate)", len(broken))
	}
	if broken[0].Receipt.Oracle.ID != policy.KindPytestCollect {
		t.Errorf("the sole receipt is %s, want %s", broken[0].Receipt.Oracle.ID, policy.KindPytestCollect)
	}
	// The delta is measured against the recorded baseline, not assumed.
	if got := broken[0].Receipt.Result.Metrics[policy.MetricCollectedDelta]; got != -1 {
		t.Errorf("collected_delta = %d, want -1", got)
	}
	if got := fixed[0].Receipt.Result.Metrics[policy.MetricCollectedBase]; got != 2 {
		t.Errorf("collected_base = %d, want 2 (the measured base state)", got)
	}

	// baseline.recorded: observational, one per race, naming what it ran.
	base := eventsOfType(t, cfg.Ledger, "baseline.recorded")
	if len(base) != 1 {
		t.Fatalf("baseline.recorded events = %d, want 1", len(base))
	}
	var body struct {
		CollectedTotal int64  `json:"collected_total"`
		Tree           string `json:"tree"`
		Oracle         struct {
			ID string `json:"id"`
		} `json:"oracle"`
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal(base[0], &body); err != nil {
		t.Fatal(err)
	}
	if body.CollectedTotal != 2 || body.Oracle.ID != policy.KindPytestCollect || body.Stdout == "" {
		t.Errorf("baseline.recorded = %s", base[0])
	}
	if _, tree, err := gitx.Head(cfg.Repo); err != nil {
		t.Fatal(err)
	} else if body.Tree != tree {
		t.Errorf("baseline tree = %q, want the base tree %q", body.Tree, tree)
	}

	// race.started carries the policy and the required set in ladder order —
	// declared-but-unrequired oracles are absent from it too.
	started := eventsOfType(t, cfg.Ledger, "race.started")
	if len(started) != 1 {
		t.Fatalf("race.started events = %d, want 1", len(started))
	}
	var start map[string]any
	if err := json.Unmarshal(started[0], &start); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"adapter", "candidates", "exec_image_digest", "exec_tier", "intent", "oracles", "parallel", "policy"}
	got := make([]string, 0, len(start))
	for k := range start {
		got = append(got, k)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantKeys, ",") {
		t.Errorf("race.started keys = %v, want %v", got, wantKeys)
	}
	if fmt.Sprint(start["oracles"]) != "[collect suite]" {
		t.Errorf("race.started oracles = %v, want [collect suite]", start["oracles"])
	}
	if start["policy"] == "" {
		t.Error("race.started carries no policy digest")
	}
}

// Pre-flight (decision 15): a policy that requires a pytest kind in an
// environment where pytest is not importable aborts as MACHINERY — exit
// before race.started, with an empty ledger and no world created. A missing
// toolchain is never recorded as a failing candidate.
func TestRunPreflightRefusesWhenPytestIsAbsent(t *testing.T) {
	pol := ladderPolicy(fakePython(t, false), filepath.Join(t.TempDir(), "spare-ran"))
	cfg := newLadderConfig(t, pol, map[string]string{"a-fix.patch": fixPatch})
	_, err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("Run: want a machinery error, got nil")
	}
	for _, want := range []string{`policy requires oracle "collect"`, "pytest is not importable", "T0 host"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if n := len(eventsOfType(t, cfg.Ledger, "race.started")); n != 0 {
		t.Errorf("pre-flight failure recorded %d race.started events, want 0", n)
	}
	if n := len(eventsOfType(t, cfg.Ledger, "world.created")); n != 0 {
		t.Errorf("pre-flight failure recorded %d worlds, want 0", n)
	}
}

// The baseline is an input, and an input that cannot be measured honestly
// stops the race: a collected_delta against a fiction is worse than no
// delta at all.
func TestRunAbortsWhenTheBaseTreeCollectsNothing(t *testing.T) {
	python := filepath.Join(t.TempDir(), "emptycollect")
	script := `#!/bin/sh
if [ "$1" = "-c" ]; then printf '%s\n' '{"pytest":"8.4.0"}'; exit 0; fi
echo "no tests collected in 0.00s"
exit 5
`
	if err := os.WriteFile(python, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	pol := ladderPolicy(python, filepath.Join(t.TempDir(), "spare-ran"))
	cfg := newLadderConfig(t, pol, map[string]string{"a-fix.patch": fixPatch})
	_, err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("Run: want a machinery error, got nil")
	}
	if !strings.Contains(err.Error(), "no honest denominator") {
		t.Errorf("error %q does not say why the baseline is unusable", err)
	}
	// The measurement itself is on the record; no candidate world is.
	if n := len(eventsOfType(t, cfg.Ledger, "baseline.recorded")); n != 1 {
		t.Errorf("baseline.recorded events = %d, want 1 (recorded, then refused)", n)
	}
	if n := len(eventsOfType(t, cfg.Ledger, "world.created")); n != 0 {
		t.Errorf("a failed baseline created %d worlds, want 0", n)
	}
}

// Decision 18, in the engine: a v0 policy must be handed the oracle its
// digest cannot name, and a v1 policy refuses one.
func TestRunOracleOverrideDiscipline(t *testing.T) {
	v0 := newConfig(t, map[string]string{"a-fix.patch": fixPatch})
	v0.LegacyOracle = nil
	if _, err := Run(context.Background(), v0); err == nil ||
		!strings.Contains(err.Error(), "an oracle must be supplied") {
		t.Errorf("v0 without an oracle: err = %v", err)
	}

	v1 := newLadderConfig(t, ladderPolicy(fakePython(t, true), filepath.Join(t.TempDir(), "spare")),
		map[string]string{"a-fix.patch": fixPatch})
	v1.LegacyOracle = stubOracle{}
	if _, err := Run(context.Background(), v1); err == nil ||
		!strings.Contains(err.Error(), "not permitted with policy") {
		t.Errorf("v1 with an oracle override: err = %v", err)
	}
	if n := len(eventsOfType(t, v1.Ledger, "race.started")); n != 0 {
		t.Errorf("a refused override recorded %d race.started events, want 0", n)
	}
}

// silentPython answers the tools probe but prints nothing a collect run can
// be counted from: the honest shape of "the structured source was
// unavailable" (decision 16). It exits 0, so the run PASSES — the metric is
// simply absent, which is the only record a missing source ever gets.
func silentPython(t *testing.T) string {
	t.Helper()
	script := `#!/bin/sh
if [ "$1" = "-c" ]; then printf '%s\n' '{"pytest":"8.4.0"}'; exit 0; fi
exit 0
`
	path := filepath.Join(t.TempDir(), "silentpython")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The ladder stops at the first gate in POLICY order, not at whichever gate
// happens to name the oracle that just ran. With interleaved gate oracles —
// which validation accepts — the old rule stopped the ladder on gate 3 before
// gate 2's oracle had run at all, and the trace then reported gate 2 (which
// never ran) as the failure and gate 3 (which did fail) as not-evaluated:
// decision 12 inverted, and the wrong gate frozen into the rationale.
func TestLadderStopsAtTheFirstFAILEDGateInPolicyOrder(t *testing.T) {
	python := fakePython(t, true)
	pol := object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "interleaved",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{python, "-m", "pytest"}, Args: []string{}},
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{python, "-m", "pytest"}, Args: []string{}},
		},
		// The gates interleave oracles: collect, suite, collect.
		HardGates: []object.GateSpec{
			{Gate: policy.GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
			{Gate: policy.GateCollectedNotBelow, Oracle: "collect", Basis: object.BasisConstruction},
		},
		Ranking:    []string{policy.KeyGatePass, policy.KeyWallMSAsc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}, OnAllWorldsFailedMachinery: true},
	}
	// One candidate, and it deletes a test: the decision is a REJECT whose
	// rationale must name the gate that actually failed.
	cfg := newLadderConfig(t, pol, map[string]string{"b-nofix.patch": noFixPatch})
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision.Type != TypeReject {
		t.Fatalf("decision = %s (%s), want REJECT", res.Decision.Type, res.Decision.Rationale)
	}
	// The ladder climbed past the interleaved gate: the world paid for the
	// suite oracle because gate 2 could not be evaluated without it.
	got := res.Worlds[0].Receipts
	if len(got) != 2 {
		t.Fatalf("world has %d receipts, want 2 (collect + suite): the ladder stopped before an earlier gate could be judged", len(got))
	}
	if got[0].Receipt.Oracle.ID != policy.KindPytestCollect || got[1].Receipt.Oracle.ID != policy.KindPytestSuite {
		t.Errorf("ladder order = [%s %s]", got[0].Receipt.Oracle.ID, got[1].Receipt.Oracle.ID)
	}
	if !strings.Contains(res.Decision.Rationale, "failed [collected-not-below@collect] (collected_delta=-1 (tolerance -0))") {
		t.Errorf("the rationale does not name the gate that failed:\n%s", res.Decision.Rationale)
	}
	if strings.Contains(res.Decision.Rationale, "failed [status-pass@suite]") {
		t.Errorf("the rationale blames a gate that passed:\n%s", res.Decision.Rationale)
	}
}

// require_evidence, driven through the orchestrator rather than a hand-built
// receipt slice — the only way to catch a rule that cannot fire. The winner
// passed every hard gate, so its ladder ran to completion and every required
// oracle DID produce a receipt: what the rule has to test is whether that
// receipt carries usable evidence, which is the case decision 16 points
// operators at ("a policy that would rather route a human than reject").
func TestRequireEvidenceEscalatesWhenTheRequiredSourceWasUnavailable(t *testing.T) {
	python := fakePython(t, true)
	pol := object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "requires-extra",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: policy.KindPytestCollect, Argv: []string{python, "-m", "pytest"}, Args: []string{}},
			// A second collect instance (distinct resolved config, so it is a
			// distinct instance) whose source yields nothing countable.
			{Name: "extra", Kind: policy.KindPytestCollect, Argv: []string{silentPython(t), "-m", "pytest"}, Args: []string{}},
			{Name: "suite", Kind: policy.KindPytestSuite, Argv: []string{python, "-m", "pytest"}, Args: []string{}},
		},
		HardGates: []object.GateSpec{
			{Gate: policy.GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateCollectedNotBelow, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking: []string{policy.KeyGatePass, policy.KeyTestsPassedDesc, policy.KeyWallMSAsc},
		Escalation: object.EscalationSpec{
			RequireEvidence:            []string{"extra"},
			OnAllWorldsFailedMachinery: true,
		},
	}
	cfg := newLadderConfig(t, pol, map[string]string{"a-fix.patch": fixPatch})
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision.Type != TypeEscalate {
		t.Fatalf("decision = %s (%s), want ESCALATE", res.Decision.Type, res.Decision.Rationale)
	}
	if !strings.Contains(res.Decision.Rationale, "escalated by policy rule require_evidence") {
		t.Errorf("the rationale does not name the rule:\n%s", res.Decision.Rationale)
	}
	if !strings.Contains(res.Decision.Rationale, `no usable evidence from required oracle "extra"`) {
		t.Errorf("the rationale does not say what was missing:\n%s", res.Decision.Rationale)
	}
	// The required oracle DID run and DID produce a bound receipt — the rule
	// fired on the receipt's emptiness, not on its absence.
	var extra *object.Receipt
	for i := range res.Worlds[0].Receipts {
		r := &res.Worlds[0].Receipts[i].Receipt
		if r.Oracle.ID == policy.KindPytestCollect && len(r.Result.Metrics) == 0 {
			extra = r
		}
	}
	if extra == nil {
		t.Fatalf("the required oracle produced no metric-less receipt: %d receipts", len(res.Worlds[0].Receipts))
	}
	// M1f rule S1 supersedes M1e's "an absent source is not a failing
	// run": a process that exited 0 with NO usable evidence stream is
	// `error`, never `pass`. The cheapest attack on a streaming oracle is
	// to silence the plugin, and silence must buy a failed gate.
	if extra.Result.Status != "error" {
		t.Errorf("the required oracle's receipt status = %q, want error (S1: a 0-exit with no usable stream is never a pass)",
			extra.Result.Status)
	}
}
