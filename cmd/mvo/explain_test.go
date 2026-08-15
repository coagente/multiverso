package main

// M1e CLI tests for the explainability surface (CP-6): the derived gate
// table, the key-by-key comparison, the machine-readable report, and
// --diffs. Nothing here needs Python: the pinned policies are
// command-oracle policies, which is exactly the migration shape
// `mvo intent new --oracle-cmd` produces.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/workspace"
)

// explainJSON runs `mvo explain --json` and decodes the report.
func explainJSON(t *testing.T, repo, intent string, extra ...string) map[string]any {
	t.Helper()
	args := append([]string{"explain", intent, "--dir", repo, "--json"}, extra...)
	out := mustMvo(t, args...)
	var rep map[string]any
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode explain --json: %v\n%s", err, out)
	}
	return rep
}

func TestExplainRendersTheDecision(t *testing.T) {
	sc := newScenario(t) // one patch, a command-oracle policy, SELECT

	human := mustMvo(t, "explain", sc.intentDig, "--dir", sc.repo)
	for _, want := range []string{
		"type:      SELECT",
		"policy:    ",
		"(command, policy/v1)",
		"gates (ordered, first failure stops the ladder):",
		"status-pass@suite",
		"PASS",
		"rationale: ",
		"freshness: ",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("explain output missing %q:\n%s", want, human)
		}
	}

	rep := explainJSON(t, sc.repo, sc.intentDig)
	if rep["schema"] != schemaExplainReport {
		t.Errorf("schema = %v, want %s", rep["schema"], schemaExplainReport)
	}
	if rep["type"] != "SELECT" || rep["winner"] == "" {
		t.Errorf("report type/winner = %v/%v", rep["type"], rep["winner"])
	}
	cands, _ := rep["candidates"].([]any)
	if len(cands) != 1 {
		t.Fatalf("candidates = %v, want 1", rep["candidates"])
	}
	c := cands[0].(map[string]any)
	if c["pass"] != true || c["rank"].(float64) != 1 {
		t.Errorf("candidate = %v", c)
	}
	gates, _ := c["gates"].([]any)
	if len(gates) != 1 || gates[0].(map[string]any)["result"] != "pass" {
		t.Fatalf("gates = %v", c["gates"])
	}
	if gates[0].(map[string]any)["receipt"] == "" {
		t.Error("a passing gate names no counted receipt")
	}
	// Every effective ranking key is reported per candidate, including the
	// implicit first and terminal ones.
	keys, _ := c["keys"].([]any)
	var names []string
	for _, k := range keys {
		names = append(names, k.(map[string]any)["key"].(string))
	}
	if strings.Join(names, ",") != "gate_pass,wall_ms_asc,world_digest_asc" {
		t.Errorf("candidate keys = %v", names)
	}
	// Escalation is a first-class, always-present field: "" means no rule
	// fired, never an omission the reader must interpret.
	esc, _ := rep["escalation"].(map[string]any)
	if esc == nil || esc["rule"] != "" {
		t.Errorf("escalation = %v", rep["escalation"])
	}
	// Evidence rows are annotated with what produced them.
	ev, _ := rep["evidence"].([]any)
	if len(ev) == 0 || ev[0].(map[string]any)["oracle"] != "command" {
		t.Fatalf("evidence = %v", rep["evidence"])
	}

	// --diffs N appends the top-N ranked candidates' captured patches.
	withDiff := mustMvo(t, "explain", sc.intentDig, "--dir", sc.repo, "--diffs", "1")
	if !strings.Contains(withDiff, "patch (rank 1) ") || !strings.Contains(withDiff, "+goodbye") {
		t.Errorf("--diffs did not render the winner's patch:\n%s", withDiff)
	}
	if _, _, code := mvo(t, "explain", sc.intentDig, "--dir", sc.repo, "--diffs", "-1"); code != exitUsage {
		t.Errorf("--diffs -1: exit %d, want %d", code, exitUsage)
	}
}

// A REJECT renders the gate that failed and why, per candidate — the
// operator's whole picture, not a bare verdict.
func TestExplainRendersRejectWithTheFailedGate(t *testing.T) {
	sc := newScenario(t)
	intent := strings.TrimSpace(mustMvo(t, "intent", "new", "--dir", sc.repo,
		"--title", "failing gate", "--oracle-cmd", "false"))
	patches := t.TempDir()
	if err := os.WriteFile(filepath.Join(patches, "patch-a.patch"), []byte(fixPatch), 0o644); err != nil {
		t.Fatal(err)
	}
	mustMvo(t, "race", intent, "--dir", sc.repo, "--patches", patches)

	human := mustMvo(t, "explain", intent, "--dir", sc.repo)
	if !strings.Contains(human, "type:      REJECT") {
		t.Fatalf("explain does not report REJECT:\n%s", human)
	}
	if !strings.Contains(human, "FAIL (status=fail)") {
		t.Errorf("explain does not show the failed gate's reason:\n%s", human)
	}

	rep := explainJSON(t, sc.repo, intent)
	cands, _ := rep["candidates"].([]any)
	c := cands[0].(map[string]any)
	if c["pass"] != false {
		t.Errorf("candidate = %v, want pass=false", c)
	}
	g := c["gates"].([]any)[0].(map[string]any)
	if g["result"] != "fail" || g["detail"] != "status=fail" {
		t.Errorf("gate = %v", g)
	}

	// `mvo worlds` renders the failed gate's LABEL, not a bare "fail".
	worlds := mustMvo(t, "worlds", intent, "--dir", sc.repo)
	if !strings.Contains(worlds, "status-pass@suite") {
		t.Errorf("worlds GATE column does not name the failed gate:\n%s", worlds)
	}
}

// altPatch is a second, distinct edit of the same file: two distinct trees,
// identical evidence. Two byte-identical worlds would be ONE world under
// content addressing, which is a different and uninteresting situation.
const altPatch = `--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-hello
+farewell
`

// writePolicy authors a v1 policy file into the workspace, canonically, and
// installs it as the default. Building the bytes through object.Canonical is
// what `mvo policy use` requires: the file's in-document name must equal its
// filename stem, and the digest is the bytes.
func writePolicy(t *testing.T, repo string, pol object.PolicyV1) {
	t.Helper()
	canon, err := object.Canonical(pol)
	if err != nil {
		t.Fatalf("canonical policy: %v", err)
	}
	path := filepath.Join(repo, workspace.DirName, "policies", pol.Name+".json")
	if err := os.WriteFile(path, canon, 0o644); err != nil {
		t.Fatal(err)
	}
	mustMvo(t, "policy", "use", pol.Name, "--dir", repo)
}

// An ESCALATE is not a win, and `mvo explain` must not render it as one. The
// ranking walk is still shown — an operator needs to see that the leaders
// tie — but nothing in it may claim the race was decided, least of all on
// the terminal digest tie-break, which is precisely the coin flip the rule
// refused.
func TestExplainDoesNotLaunderAnEscalateIntoAWin(t *testing.T) {
	sc := newScenario(t)
	// A command-oracle policy (no Python) whose only ranking key is
	// gate_pass: two candidates that both pass tie on every key up to the
	// terminal world_digest_asc.
	writePolicy(t, sc.repo, object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "tie-cmd",
		Oracles: []object.OracleSpec{{
			Name: "suite", Kind: policy.KindCommand, Argv: []string{"true"}, Args: []string{},
		}},
		HardGates: []object.GateSpec{
			{Gate: policy.GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{policy.KeyGatePass},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}, OnRankingTie: true},
	})
	intent := strings.TrimSpace(mustMvo(t, "intent", "new", "--dir", sc.repo, "--title", "tie escalates"))
	patches := t.TempDir()
	for name, body := range map[string]string{"patch-x.patch": fixPatch, "patch-y.patch": altPatch} {
		if err := os.WriteFile(filepath.Join(patches, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustMvo(t, "race", intent, "--dir", sc.repo, "--patches", patches)

	human := mustMvo(t, "explain", intent, "--dir", sc.repo)
	if !strings.Contains(human, "type:      ESCALATE") {
		t.Fatalf("the race did not escalate:\n%s", human)
	}
	for _, want := range []string{
		"leader:    ",                     // never "winner:"
		"ranking walk for leader ",        // never "why … won"
		"tie-break only — not a decision", // the world_digest_asc row
		"escalation: on_ranking_tie",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("explain output missing %q:\n%s", want, human)
		}
	}
	// The laundering tokens, absent in every form.
	for _, forbidden := range []string{"won  (ranking", "WINNER ", "← decided here", "winner:   "} {
		if strings.Contains(human, forbidden) {
			t.Errorf("explain launders the ESCALATE with %q:\n%s", forbidden, human)
		}
	}

	rep := explainJSON(t, sc.repo, intent)
	if rep["type"] != "ESCALATE" {
		t.Fatalf("report type = %v", rep["type"])
	}
	esc, _ := rep["escalation"].(map[string]any)
	if esc["rule"] != "on_ranking_tie" {
		t.Fatalf("escalation = %v", rep["escalation"])
	}
	cands, _ := rep["candidates"].([]any)
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2", len(cands))
	}
	for _, c := range cands {
		if c.(map[string]any)["pass"] != true {
			t.Errorf("a tied candidate did not pass every gate: %v", c)
		}
		if !strings.Contains(esc["detail"].(string), c.(map[string]any)["world"].(string)) {
			t.Errorf("the escalation detail does not name %v", c)
		}
	}
	// The trace is still reported — the tie is the evidence — and it ends on
	// the terminal key, which is exactly why no winner may be claimed.
	tr, _ := rep["trace"].([]any)
	if len(tr) != 1 || tr[0].(map[string]any)["key"] != policy.KeyWorldDigestAsc {
		t.Errorf("trace = %v, want one comparison decided only by %s", rep["trace"], policy.KeyWorldDigestAsc)
	}
}

// A decision made under a pinned policy/v0 renders M0's gate label and M0's
// frozen rationale, through the same explain surface as everything else: the
// dialect is a property of the policy, not of the renderer's era.
func TestExplainRendersAV0PolicyDecision(t *testing.T) {
	sc := newScenario(t)
	src := filepath.Join("..", "..", "testdata", "toyrepo", "policies", "legacy-v0.json")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dst := filepath.Join(sc.repo, workspace.DirName, "policies", "legacy-v0.json")
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
	// v0 never becomes the silent default, but it is pinnable per intent.
	intent := strings.TrimSpace(mustMvo(t, "intent", "new", "--dir", sc.repo,
		"--title", "legacy policy", "--policy", "legacy-v0"))
	patches := t.TempDir()
	if err := os.WriteFile(filepath.Join(patches, "patch-a.patch"), []byte(fixPatch), 0o644); err != nil {
		t.Fatal(err)
	}
	// A v0 policy names a gate but not the command that decides it, so the
	// race must be handed one (decision 18).
	mustMvo(t, "race", intent, "--dir", sc.repo, "--patches", patches, "--oracle-cmd", "true")

	human := mustMvo(t, "explain", intent, "--dir", sc.repo)
	for _, want := range []string{
		"type:      SELECT",
		"(-, policy/v0)", // v0 documents carry no name
		"suite-pass",     // M0's gate label, not status-pass@suite
		"rationale: 1/1 worlds passed hard gates [suite-pass]; selected mv0:",
		"by ranking [gate_pass,wall_ms_asc] (wall_ms=",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("v0 explain output missing %q:\n%s", want, human)
		}
	}
	if strings.Contains(human, "status-pass@suite") {
		t.Errorf("a v0 decision rendered a v1 gate label:\n%s", human)
	}

	rep := explainJSON(t, sc.repo, intent)
	pol, _ := rep["policy"].(map[string]any)
	if pol["schema"] != object.SchemaPolicy || pol["name"] != "" {
		t.Errorf("report policy = %v", rep["policy"])
	}
	gates, _ := pol["gates"].([]any)
	if len(gates) != 1 || gates[0].(map[string]any)["label"] != policy.GateSuitePass {
		t.Errorf("report gates = %v", pol["gates"])
	}
	// The effective key list carries the implicit terminal key even in the v0
	// dialect: the order is total whatever the document listed.
	ranking, _ := pol["ranking"].([]any)
	var names []string
	for _, k := range ranking {
		names = append(names, k.(string))
	}
	if strings.Join(names, ",") != "gate_pass,wall_ms_asc,world_digest_asc" {
		t.Errorf("effective ranking = %v", names)
	}
}

// The number printed beside a gate label is an attribution claim: it must
// come from the receipt THAT GATE was evaluated against, never from the
// candidate's merged, cross-oracle map. Two instances of one kind are legal
// and emit the same metric names, so reading the merged map would put one
// oracle's measurement beside the other's verdict.
func TestGateCellRendersTheGatesOwnMeasurement(t *testing.T) {
	rep := explainReport{receiptByDigest: map[string]object.Receipt{
		"mv0:a": {Result: object.Result{Metrics: map[string]int64{policy.MetricCollectedTotal: 8}}},
		"mv0:b": {Result: object.Result{Metrics: map[string]int64{policy.MetricCollectedTotal: 3}}},
	}}
	a := race.GateResult{Label: "collect-nonempty@collect-a", Result: policy.GatePass, Receipt: "mv0:a"}
	b := race.GateResult{Label: "collect-nonempty@collect-b", Result: policy.GatePass, Receipt: "mv0:b"}
	if got := gateCell(a, rep.gateMetrics(a)); got != "pass (total=8)" {
		t.Errorf("gate a cell = %q, want %q", got, "pass (total=8)")
	}
	if got := gateCell(b, rep.gateMetrics(b)); got != "pass (total=3)" {
		t.Errorf("gate b cell = %q, want %q", got, "pass (total=3)")
	}
	// A gate with no counted receipt shows the verdict and no invented
	// number.
	none := race.GateResult{Label: "collect-nonempty@collect-a", Result: policy.GatePass}
	if got := gateCell(none, rep.gateMetrics(none)); got != "pass" {
		t.Errorf("receipt-less gate cell = %q, want %q", got, "pass")
	}
}
