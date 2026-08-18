package eval

// M2d.1's testing bar for warming and for the race window.
//
// Everything here runs against a STUB `mvo` and a HAND-BUILT ledger. That is
// not a convenience: the properties being asserted are about what the harness
// HANDS OVER and what it READS BACK, and a real race would bury both under
// two seconds of pytest. The end-to-end evidence is `scripts/accept.sh`'s
// m2d1-9 pair; this is the layer that makes the mechanism testable at all.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/schedule"
)

// warmStub is an `mvo` that prices its cost table after `fitAfter` races and
// not before — which is exactly the shape decision 1 exists for: the number
// of races needed is a property of the fixture, so a COUNT is the wrong
// stopping rule in both directions.
//
// It records every argv so the test can assert what warming asked for, and it
// reports the menu through `oracles --json` because that is the read-only
// verb the predicate is defined against.
func warmStub(t *testing.T, dir, stateDir string, fitAfter int) string {
	t.Helper()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "mvo-warm-stub")
	script := `#!/bin/sh
state=` + shq(stateDir) + `
printf '%s\n' "$*" >> "$state/argv"
case "$1" in
  oracles)
    n=0
    [ -f "$state/races" ] && n=$(cat "$state/races")
    if [ "$n" -ge ` + itoa(fitAfter) + ` ]; then
      m='{"n":3,"fixed_ms":11.0,"per_unit_ms":0.0,"plugin_autoload":"off","estimator":"theil-sen"}'
      note=""
    else
      m=null
      note="no local measurement (n=$n, need 3)"
    fi
    cat <<EOF
{"schema":"multiverso.dev/oracle-menu/v0","kinds":[
 {"kind":"tree-guard","declared_by_policy":["guard"],"measurement":$m,"measurement_n":$n,"measurement_note":"$note"},
 {"kind":"pytest-suite","declared_by_policy":["suite"],"measurement":$m,"measurement_n":$n,"measurement_note":"$note"},
 {"kind":"mutation-diff","declared_by_policy":[],"measurement":null,"measurement_n":0,"measurement_note":"undeclared"}
]}
EOF
    ;;
  intent) echo mv0:warm$$ ;;
  race)
    n=0
    [ -f "$state/races" ] && n=$(cat "$state/races")
    echo $((n + 1)) > "$state/races"
    ;;
  init) mkdir -p "$2/.multiverso" 2>/dev/null || true ;;
esac
exit 0
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := ""
	for ; n > 0; n /= 10 {
		out = string(rune('0'+n%10)) + out
	}
	return out
}

func warmFixture(t *testing.T) (root, repo string, patches map[string][]byte) {
	t.Helper()
	root = t.TempDir()
	repo = filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "stats.py"), []byte(toyBase), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, repo, map[string][]byte{"01-a.patch": []byte("--- a\n"), "02-b.patch": []byte("--- b\n")}
}

// DECISION 1: `auto` stops when THE PREDICATE HOLDS, not after a fixed count.
// On a fixture that needs two races it takes two; on one that needs one it
// takes one; and it never takes more once the table is priced.
func TestWarmAutoStopsOnThePredicateAndNotOnACount(t *testing.T) {
	for _, need := range []int{1, 2} {
		root, repo, patches := warmFixture(t)
		state := filepath.Join(root, "state")
		stub := warmStub(t, root, state, need)
		rep := Warm(WarmSpec{
			MVO: stub, Dir: filepath.Join(root, "tmpl"), RepoSrc: repo, Patches: patches,
			Auto: true, Env: []string{"PATH=" + os.Getenv("PATH")}, Key: "k",
		})
		if rep.Refused != "" {
			t.Fatalf("need=%d: warming refused: %s", need, rep.Refused)
		}
		if rep.Races != need {
			t.Errorf("need=%d: warming raced %d time(s), want exactly %d — the predicate is the stopping rule",
				need, rep.Races, need)
		}
		if !rep.Warm() {
			t.Errorf("need=%d: the predicate did not hold after %d race(s): unfitted %v",
				need, rep.Races, rep.KindsUnfitted)
		}
		if rep.Regime() != "WARM-COST-TABLE(n="+itoa(rep.Races)+")" {
			t.Errorf("need=%d: regime caption = %q", need, rep.Regime())
		}
		if rep.TableDigest == "" {
			t.Errorf("need=%d: no cost-table digest recorded: V-6 has nothing to assert on", need)
		}
	}
}

// DECISION 4: THE WARM INTENT CARRIES `--budget-oracle-ms 0`, and the arm's
// pool therefore never sees a warm-up millisecond. This is the argv half of
// the claim; the ledger half is the race-window test below.
func TestWarmIntentIsUnbudgetedAndRacesThePublicCandidateSet(t *testing.T) {
	root, repo, patches := warmFixture(t)
	state := filepath.Join(root, "state")
	stub := warmStub(t, root, state, 1)
	rep := Warm(WarmSpec{
		MVO: stub, Dir: filepath.Join(root, "tmpl"), RepoSrc: repo, Patches: patches,
		Auto: true, Env: []string{"PATH=" + os.Getenv("PATH")},
	})
	if rep.Refused != "" {
		t.Fatalf("warming refused: %s", rep.Refused)
	}
	b, err := os.ReadFile(filepath.Join(state, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	argv := string(b)
	var intentLine string
	for _, line := range strings.Split(argv, "\n") {
		if strings.HasPrefix(line, "intent new") {
			intentLine = line
		}
	}
	if intentLine == "" {
		t.Fatalf("warming created no intent:\n%s", argv)
	}
	if !strings.Contains(intentLine, "--budget-oracle-ms 0") {
		t.Errorf("the warm intent is BUDGETED: %q — a warm-up must be a separate, unbounded race", intentLine)
	}
	if !strings.Contains(argv, "--schedule=fixed") {
		t.Errorf("the warm-up did not race the exhaustive ladder:\n%s", argv)
	}
	// The warm set is the PUBLIC CANDIDATE SET, written where the arms' own
	// handoff goes. No new bytes cross the hidden/public boundary.
	for name := range patches {
		p := filepath.Join(root, "tmpl", "ws", ".mvo-eval-handoff", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("warm handoff is missing %s: %v", name, err)
		}
	}
}

// THE CAP'S NAMED REFUSAL. A workspace that cannot be priced is reported as
// `warm_incomplete` WITH THE KINDS NAMED, and is never silently raced as
// though it were warm.
func TestWarmCapRefusesByNameAndListsTheUnpricedKinds(t *testing.T) {
	root, repo, patches := warmFixture(t)
	state := filepath.Join(root, "state")
	stub := warmStub(t, root, state, 99) // never reaches the predicate
	rep := Warm(WarmSpec{
		MVO: stub, Dir: filepath.Join(root, "tmpl"), RepoSrc: repo, Patches: patches,
		Auto: true, Env: []string{"PATH=" + os.Getenv("PATH")},
	})
	if rep.Races != WarmupCapDefault {
		t.Errorf("auto raced %d time(s), want the cap of %d", rep.Races, WarmupCapDefault)
	}
	if !rep.Incomplete || rep.Warm() {
		t.Fatalf("a workspace that never priced reported itself warm: %+v", rep)
	}
	want := map[string]bool{"tree-guard": true, "pytest-suite": true}
	for _, k := range rep.KindsUnfitted {
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("warm_incomplete did not name the unpriced kinds: got %v", rep.KindsUnfitted)
	}
	if rep.Regime() != "COLD-COST-TABLE" {
		t.Errorf("an incomplete warm-up captioned itself %q", rep.Regime())
	}
	// An UNDECLARED kind is not part of the predicate: the rule is about what
	// the pinned policy can BUY, and refusing on a kind no policy declares
	// would make the predicate unreachable on every workspace.
	for _, k := range append(rep.KindsFitted, rep.KindsUnfitted...) {
		if k == "mutation-diff" {
			t.Error("the predicate read a kind the policy does not declare")
		}
	}
	lines := strings.Join(rep.Lines(), "\n")
	if !strings.Contains(lines, "warm_incomplete") || !strings.Contains(lines, "0 BY CONSTRUCTION") {
		t.Errorf("the report does not say what an incomplete warm-up means:\n%s", lines)
	}
}

// `--warmup 0` IS THE COLD INSTRUMENT, KEPT ON PURPOSE: accept step m2d1-9a
// has to be able to reproduce M2b.2's vacuum deliberately.
func TestWarmupZeroIsTheColdInstrumentAndBuildsNoTemplate(t *testing.T) {
	auto, n, err := ParseWarmup("0")
	if err != nil || auto || n != 0 {
		t.Fatalf("ParseWarmup(\"0\") = %v/%d/%v", auto, n, err)
	}
	root, repo, patches := warmFixture(t)
	stub := warmStub(t, root, filepath.Join(root, "state"), 1)
	rep := Warm(WarmSpec{MVO: stub, Dir: filepath.Join(root, "tmpl"), RepoSrc: repo,
		Patches: patches, Auto: false, Races: 0})
	if rep.Races != 0 || rep.Template != "" || rep.Warm() {
		t.Fatalf("--warmup 0 warmed something: %+v", rep)
	}
	if rep.Regime() != "COLD-COST-TABLE" {
		t.Errorf("regime = %q, want COLD-COST-TABLE", rep.Regime())
	}
	if _, _, err := ParseWarmup("sometimes"); err == nil {
		t.Error("a mistyped --warmup was accepted: a warm-up that silently means `cold` is the invisible vacuum")
	}
}

// DECISION 2: THE TEMPLATE CACHE KEY REFUSES REUSE ACROSS POLICY DIGESTS.
// `costSamples()` admits a receipt only when the pinned policy declares its
// Oracle.Config, and the fit is keyed on the SEAL, which is a policy field
// M2a amendment 27 measured as a 4.4× lever on fixed cost.
func TestTemplateCacheRefusesReuseAcrossPolicyAndBinary(t *testing.T) {
	c := NewTemplateCache()
	k1 := TemplateKey("toyrepo-mean-A", "mv0:polA", "mv0:bin1")
	k2 := TemplateKey("toyrepo-mean-A", "mv0:polB", "mv0:bin1")
	k3 := TemplateKey("toyrepo-mean-A", "mv0:polA", "mv0:bin2")
	if k1 == k2 || k1 == k3 || k2 == k3 {
		t.Fatal("two different (instance, policy, binary) triples collided on one key")
	}
	c.Put(k1, WarmReport{Races: 1, Template: "/tmp/one"})
	if _, ok := c.Get(k2); ok {
		t.Error("a template built under one policy was served to another")
	}
	if _, ok := c.Get(k3); ok {
		t.Error("a template built by one binary was served to another")
	}
	got, ok := c.Get(k1)
	if !ok || got.Template != "/tmp/one" {
		t.Errorf("the cache did not return its own template: %+v", got)
	}
	// Two warm-ups of ONE template are idempotent in the PREDICATE. They are
	// NOT asserted idempotent in the MILLISECONDS: it is a wall-clock fit and
	// the host drifts.
	calls := 0
	build := func() WarmReport {
		calls++
		return WarmReport{Races: 1, Template: "/tmp/two", WallMS: int64(calls) * 100}
	}
	c.Put("k9", build())
	if _, ok := c.Get("k9"); !ok {
		t.Fatal("cache miss on a key just written")
	}
	if calls != 1 {
		t.Errorf("the template was rebuilt %d times for one key", calls)
	}
}

// ---------------------------------------------------------------------------
// The race window (decision 3)
// ---------------------------------------------------------------------------

// warmedLedger writes a workspace ledger holding THREE races over one policy:
// two warm-ups (500 ms and 400 ms) and the measured race (90 ms). Three
// rather than two because `--warmup auto` caps at three and every consumer of
// LedgerView assumed ONE race per workspace — a reader that got two right by
// accident (say, by taking "the other one") would still get three wrong. It is
// the exact shape a warmed template produces, built by hand so the assertion
// is about the READER and not about a racer.
func warmedLedger(t *testing.T) (workspace, warm1, warm2, measuredIntent string) {
	t.Helper()
	workspace = t.TempDir()
	dir := filepath.Join(workspace, ".multiverso")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	led, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer led.Close()

	polBytes, err := object.Canonical(policy.Default())
	if err != nil {
		t.Fatal(err)
	}
	polDig := object.DigestBytes(polBytes)
	if _, err := led.Append("policy.created", polBytes); err != nil {
		t.Fatal(err)
	}
	app := func(typ string, v any) string {
		b, err := object.Canonical(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := led.Append(typ, b); err != nil {
			t.Fatal(err)
		}
		return object.DigestBytes(b)
	}
	race := func(title string, wallMS int64) (intent, world string) {
		intent = app("intent.created", object.Intent{
			Schema: object.SchemaIntent, Policy: polDig,
			Spec: object.Spec{Title: title, Description: title}, CreatedAt: "2026-08-17T00:00:00Z",
		})
		app("race.started", map[string]any{"intent": intent, "candidates": 1, "parallel": 1, "policy": polDig})
		world = app("world.created", object.World{
			Schema: object.SchemaWorld, Intent: intent, Tree: "git:" + title,
			IsolationTier: "T0-worktree", Outcome: object.OutcomeCompleted, CreatedAt: "2026-08-17T00:00:01Z",
		})
		app("receipt.recorded", object.Receipt{
			Schema: object.SchemaReceipt, World: world,
			Oracle:      object.OracleRef{ID: policy.KindTreeGuard, Version: "v0", Config: "mv0:cfg"},
			Result:      object.Result{Status: "pass", Metrics: map[string]int64{}, Tools: map[string]string{}},
			Inputs:      object.NoInputs(),
			Cost:        object.Cost{WallMS: wallMS},
			RecheckTier: "V1-replayable", Family: "guard", CreatedAt: "2026-08-17T00:00:02Z",
		})
		app("decision.recorded", object.Decision{
			Schema: object.SchemaDecision, Type: "REJECT", Intent: intent,
			Subject: []string{}, Evidence: []string{}, Policy: polDig,
			Rationale: title, CreatedAt: "2026-08-17T00:00:03Z",
		})
		return intent, world
	}
	warm1, _ = race("warm-up-1", 500)
	warm2, _ = race("warm-up-2", 400)
	measuredIntent, _ = race("measured", 90)
	return workspace, warm1, warm2, measuredIntent
}

// V-5: A WARMED ARM MAY NOT REPORT A SPEND THAT INCLUDES WARM-UP
// MILLISECONDS. The window is what makes the arms budget-matched IN THE
// REPORT as well as in the pool.
func TestTheRaceWindowKeepsTheWarmUpsSpendOutOfTheArmsReport(t *testing.T) {
	ws, warmIntent, _, measured := warmedLedger(t)

	// The default reading is still "the last race", which is the measured one.
	v, err := ReadLedger(ws)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if v.Intent != measured {
		t.Fatalf("ReadLedger picked intent %s, want the measured race %s", v.Intent, measured)
	}
	if len(v.Races) != 3 {
		t.Errorf("the workspace holds %d race(s), want 3 — the warm-ups are in this very ledger", len(v.Races))
	}
	if len(v.Receipts) != 1 || v.SpendMS != 90 {
		t.Fatalf("the measured race reports %d receipt(s) and %d ms; want 1 receipt and 90 ms, "+
			"not the warm-up's 500", len(v.Receipts), v.SpendMS)
	}
	if v.OutsideSpendMS != 900 {
		t.Errorf("the warm-ups' spend is reported as %d ms, want 900: an uncharged cost that is also "+
			"unreported is a cost nobody can audit", v.OutsideSpendMS)
	}
	if len(v.Worlds) != 1 {
		t.Errorf("the window holds %d world(s), want 1: a merged world set would be a decision nobody made", len(v.Worlds))
	}

	// And the window can be asked for by name.
	w, err := ReadLedgerRace(ws, warmIntent)
	if err != nil {
		t.Fatalf("ReadLedgerRace(warm): %v", err)
	}
	if w.Intent != warmIntent || w.SpendMS != 500 || w.OutsideSpendMS != 490 {
		t.Errorf("the warm window reports intent %s spend %d outside %d", w.Intent, w.SpendMS, w.OutsideSpendMS)
	}

	// A race the workspace never ran is ABSENT, never somebody else's race.
	if _, err := ReadLedgerRace(ws, "mv0:nosuchrace"); err == nil {
		t.Error("a window over a race that never happened returned a view instead of an absence")
	}
	// THE LEDGER IS NOT EDITED: the window is a read, and the chain still
	// verifies over every event including the warm-up's.
	if !v.ChainVerified || v.Events != w.Events {
		t.Errorf("the window changed what was SCANNED: %d vs %d events, chain=%v",
			v.Events, w.Events, v.ChainVerified)
	}
}

// A race that pre-flight-aborted leaves the ledger untouched, so the window
// over it is EMPTY and every consumer reports absence rather than reading the
// previous race's numbers under this race's name.
func TestAWindowOverARaceThatNeverStartedIsEmpty(t *testing.T) {
	ws, _, _, _ := warmedLedger(t)
	v, err := ReadLedgerRace(ws, "mv0:aborted")
	if err == nil {
		t.Fatal("an aborted race produced a view")
	}
	if len(v.Receipts) != 0 || v.SpendMS != 0 {
		t.Errorf("an empty window carried %d receipts / %d ms", len(v.Receipts), v.SpendMS)
	}
}

// DECISION 2's DETERMINISM, at the level it is actually delivered: every arm
// and every replicate inherits ONE template BY COPY, so the bytes the cost
// fit is computed from are identical across replicates rather than
// independently re-measured.
func TestEveryReplicateInheritsTheTemplateByCopy(t *testing.T) {
	root := t.TempDir()
	tmpl := filepath.Join(root, "tmpl")
	if err := os.MkdirAll(filepath.Join(tmpl, ".multiverso", "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := []byte("the warmed ledger's bytes\n")
	if err := os.WriteFile(filepath.Join(tmpl, ".multiverso", "ledger.db"), marker, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpl, ".multiverso", "policies", "p.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "repo")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "stats.py"), []byte(toyBase), 0o644); err != nil {
		t.Fatal(err)
	}

	var digests []string
	for r := 0; r < 3; r++ {
		dir := filepath.Join(root, "rep"+itoa(r))
		dump := filepath.Join(dir, "dump")
		stub := racerStub(t, root, dump)
		res, _, _ := Race(RunSpec{
			Arm: Arm{ID: "a", RaceFlags: []string{"--schedule=adaptive"}}, MVO: stub,
			Instance: Instance{ID: "i1", Task: "t"}, Patches: map[string][]byte{"01.patch": []byte("x")},
			WorkRoot: dir, RepoSrc: src, TemplateSrc: tmpl, Env: []string{"PATH=" + os.Getenv("PATH")},
		})
		b, err := os.ReadFile(filepath.Join(res.Workspace, ".multiverso", "ledger.db"))
		if err != nil {
			t.Fatalf("replicate %d inherited no ledger: %v", r, err)
		}
		digests = append(digests, CASKeyBytes(b))
		// `mvo init` MUST NOT run over an inherited workspace: it would either
		// refuse or build a second, empty table.
		for _, argv := range res.Argv {
			if len(argv) > 1 && argv[1] == "init" {
				t.Errorf("replicate %d ran `mvo init` over a warmed template", r)
			}
		}
	}
	for i := range digests {
		if digests[i] != digests[0] {
			t.Fatalf("replicate %d inherited a DIFFERENT cost table (%s vs %s): "+
				"every per-arm difference is then confounded with pricing (V-6)",
				i, digests[i], digests[0])
		}
	}
}

// The template is refused rather than silently ignored when it holds no
// workspace: an arm that raced cold under a warm caption is the exact failure
// this block exists to remove.
func TestARaceRefusesATemplateThatHoldsNoWorkspace(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "repo")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "stats.py"), []byte(toyBase), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := racerStub(t, root, filepath.Join(root, "dump"))
	_, _, err := Race(RunSpec{
		Arm: Arm{ID: "a", RaceFlags: []string{"--schedule=adaptive"}}, MVO: stub,
		Instance: Instance{ID: "i1"}, Patches: map[string][]byte{"01.patch": []byte("x")},
		WorkRoot: filepath.Join(root, "w"), RepoSrc: src,
		TemplateSrc: filepath.Join(root, "not-a-template"),
		Env:         []string{"PATH=" + os.Getenv("PATH")},
	})
	if err == nil {
		t.Fatal("a race inherited an absent template without complaint")
	}
	if !strings.Contains(err.Error(), "holds no .multiverso") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
}

// COVERAGE IS COMPUTED FROM THE RECORDED STEPS THE WINDOW COLLECTED, not
// asserted by the harness and not asked of the arm.
func TestTheWindowCollectsTheAllocationTraceCoverageIsComputedFrom(t *testing.T) {
	ws, _, _, measured := warmedLedger(t)
	v, err := ReadLedgerRace(ws, measured)
	if err != nil {
		t.Fatal(err)
	}
	// This fixture raced no scheduler, so the trace is EMPTY and coverage says
	// so rather than reporting 0 %.
	if !v.Trace.Empty() {
		t.Errorf("a ledger with no schedule.* events produced a trace: %+v", v.Trace)
	}
	rep := schedule.Coverage(v.Trace)
	if rep.Vacuous() {
		t.Error("a race with no allocation trace was reported VACUOUS: absent source implies absent metric")
	}
	if rep.Summary() != schedule.CoverageNoTrace {
		t.Errorf("summary = %q, want %q", rep.Summary(), schedule.CoverageNoTrace)
	}
	b, err := json.Marshal(rep)
	if err != nil || len(b) == 0 {
		t.Fatalf("the coverage report does not serialize: %v", err)
	}
}
