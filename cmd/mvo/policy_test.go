package main

// M1e CLI tests: the policy verbs (CP-5). A policy is an authored,
// versioned, content-addressed artifact — these tests pin what the verbs
// print, what they refuse, and the byte-stability of the authoring round
// trip. No git, no agent, no oracle: policy is a pure data plane.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/workspace"
)

// fixturePolicy copies one of testdata/toyrepo/policies into the
// workspace's authoring directory and returns its path and digest.
func fixturePolicy(t *testing.T, repo, name string) (string, string) {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "toyrepo", "policies", name+".json")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dst := filepath.Join(repo, workspace.DirName, "policies", name+".json")
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dst, object.DigestBytes(b)
}

// initWorkspace makes a git worktree and initializes a workspace in it.
// The `git init` is not decoration: `mvo init` refuses a directory git
// does not recognise, because a workspace outside a worktree is a dead end
// that used to surface one verb later as a raw `git rev-parse` failure —
// after minting a signing keypair into it.
func initWorkspace(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitCLI(t, repo, "init", "-q")
	mustMvo(t, "init", "--dir", repo)
	return repo
}

func TestPolicyList(t *testing.T) {
	repo := initWorkspace(t)
	out := mustMvo(t, "policy", "list", "--dir", repo)
	if !strings.HasPrefix(out, "NAME") || !strings.Contains(out, "DIGEST") {
		t.Fatalf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "recorded (default)") {
		t.Errorf("list does not mark the workspace default:\n%s", out)
	}

	// A file nothing has used yet is listed honestly as unrecorded.
	_, dig := fixturePolicy(t, repo, "rank-two-keys")
	out = mustMvo(t, "policy", "list", "--dir", repo)
	if !strings.Contains(out, "rank-two-keys") || !strings.Contains(out, dig) {
		t.Fatalf("list omits the new file:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "rank-two-keys") && !strings.Contains(line, "unrecorded (file only)") {
			t.Errorf("row %q: an unused file must not read as recorded", line)
		}
	}

	// Rows are digest-sorted, so the listing is stable across machines.
	var digs []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) > 1 {
			digs = append(digs, fields[1])
		}
	}
	for i := 1; i < len(digs); i++ {
		if digs[i-1] > digs[i] {
			t.Errorf("rows are not digest-sorted: %v", digs)
		}
	}
}

func TestPolicyShow(t *testing.T) {
	repo := initWorkspace(t)
	ws, err := workspace.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	defaultDig := ws.Config.DefaultPolicy
	ws.Close()

	// --json prints ONLY the policy's bytes: the authoring round trip is
	// byte-stable, so a shown policy re-digests to the digest shown.
	out := mustMvo(t, "policy", "show", defaultDig, "--json", "--dir", repo)
	if got := object.DigestBytes([]byte(out)); got != defaultDig {
		t.Errorf("show --json re-digests to %s, want %s (byte-stability of the round trip)", got, defaultDig)
	}

	// The human rendering says what the policy MEANS.
	human := mustMvo(t, "policy", "show", "default", "--dir", repo)
	for _, want := range []string{"digest:", defaultDig, "schema:", "gates (ordered):", "ranking:", "escalation:"} {
		if !strings.Contains(human, want) {
			t.Errorf("human output missing %q:\n%s", want, human)
		}
	}
	// The shipped default is the v1 ladder: it names its own oracles, and
	// the rendering shows the resolved-config digest their receipts carry.
	for _, want := range []string{"collect-nonempty@collect", "kind=pytest-collect", "required:  guard,collect,suite"} {
		if !strings.Contains(human, want) {
			t.Errorf("default policy rendering missing %q:\n%s", want, human)
		}
	}
	// A v0 policy declares no oracles, and says so instead of printing an
	// empty section that looks like an omission.
	fixturePolicy(t, repo, "legacy-v0")
	legacy := mustMvo(t, "policy", "show", "legacy-v0", "--dir", repo)
	if !strings.Contains(legacy, "(none declared:") {
		t.Errorf("v0 policy must say why it declares no oracles:\n%s", legacy)
	}

	// Resolution order: a file beats the ledger, a digest beats both.
	_, dig := fixturePolicy(t, repo, "rank-two-keys")
	fileOut := mustMvo(t, "policy", "show", "rank-two-keys", "--json", "--dir", repo)
	if object.DigestBytes([]byte(fileOut)) != dig {
		t.Error("show <name> did not resolve the file-backed policy")
	}
	v1 := mustMvo(t, "policy", "show", "rank-two-keys", "--dir", repo)
	for _, want := range []string{"collect-nonempty@collect", "oracle=collect", "basis>=construction",
		"gate_pass,tests_passed_desc,wall_ms_asc,world_digest_asc", "kind=pytest-collect", "required:"} {
		if !strings.Contains(v1, want) {
			t.Errorf("v1 rendering missing %q:\n%s", want, v1)
		}
	}

	// The documented authoring flow: show --json > file, then validate it.
	authored := filepath.Join(repo, workspace.DirName, "policies", "authored.json")
	if err := os.WriteFile(authored, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := mvo(t, "policy", "validate", authored, "--dir", repo); code != exitOK {
		t.Errorf("show --json | validate round trip: exit %d, stderr %q", code, stderr)
	}

	_, stderr, code := mvo(t, "policy", "show", "nope", "--dir", repo)
	if code != exitFail || !strings.Contains(stderr, `no policy "nope"`) {
		t.Errorf("show of an unknown name: exit %d, stderr %q", code, stderr)
	}
}

func TestPolicyValidate(t *testing.T) {
	repo := initWorkspace(t)
	badPath, _ := fixturePolicy(t, repo, "bad-gate")
	goodPath, goodDig := fixturePolicy(t, repo, "rank-two-keys")

	// Invalid CONTENT is a failure (exit 1), not CLI misuse (exit 2).
	stdout, stderr, code := mvo(t, "policy", "validate", badPath, "--dir", repo)
	if code != exitFail {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, exitFail, stdout, stderr)
	}
	wantLine := "mvo: policy validate: " + badPath + `: hard_gates[1].gate: unknown gate "suite-passes" ` +
		`(known: collect-nonempty, collected-not-below, coverage-at-least, no-failed-tests, paths-unmodified, skips-not-above, status-pass)`
	if strings.TrimRight(stderr, "\n") != wantLine {
		t.Errorf("stderr =\n %q\nwant\n %q", strings.TrimRight(stderr, "\n"), wantLine)
	}

	// A valid policy reports the digest the workspace would record.
	stdout, _, code = mvo(t, "policy", "validate", goodPath, "--dir", repo)
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, goodDig) || !strings.HasSuffix(stdout, "OK: policy valid\n") {
		t.Errorf("validate output:\n%s", stdout)
	}

	// A v0 file is still loadable and inspectable — deliberately pinnable,
	// never silently default.
	v0Path, _ := fixturePolicy(t, repo, "legacy-v0")
	if _, _, code := mvo(t, "policy", "validate", v0Path, "--dir", repo); code != exitOK {
		t.Errorf("validate of a v0 policy: exit %d, want 0", code)
	}

	// Every problem is reported, one line each.
	multi := filepath.Join(repo, workspace.DirName, "policies", "multi.json")
	if err := os.WriteFile(multi, []byte(`{"schema":"multiverso.dev/policy/v1","name":"BAD NAME",`+
		`"oracles":[{"name":"s","kind":"nope","argv":[],"args":[],"timeout_ms":0,"coverage":false,"reruns":0}],`+
		`"hard_gates":[{"gate":"nope","oracle":"s","basis":"guesswork","threshold":0}],`+
		`"ranking":["nope"],"escalation":{"min_candidates_passing":-1,"on_ranking_tie":false,`+
		`"require_evidence":[],"on_all_worlds_failed_machinery":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = mvo(t, "policy", "validate", multi, "--dir", repo)
	if code != exitFail {
		t.Fatalf("exit = %d, want 1", code)
	}
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) < 5 {
		t.Errorf("want one line per problem, got %d:\n%s", len(lines), stderr)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "mvo: policy validate: "+multi+": ") {
			t.Errorf("line %q is not prefixed like every other CLI error", line)
		}
	}

	_, _, code = mvo(t, "policy", "validate", filepath.Join(repo, "nope.json"), "--dir", repo)
	if code != exitFail {
		t.Errorf("validate of a missing file: exit %d, want 1", code)
	}
	if _, _, code := mvo(t, "policy", "validate", "--dir", repo); code != exitUsage {
		t.Errorf("validate without a file: exit %d, want %d", code, exitUsage)
	}
}

func TestPolicyUse(t *testing.T) {
	repo := initWorkspace(t)
	_, dig := fixturePolicy(t, repo, "rank-two-keys")

	out := mustMvo(t, "policy", "use", "rank-two-keys", "--dir", repo)
	if !strings.Contains(out, dig) || !strings.Contains(out, "policy/v1") {
		t.Errorf("use output = %q", out)
	}
	ws, err := workspace.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Config.DefaultPolicy != dig {
		t.Errorf("default_policy = %s, want %s", ws.Config.DefaultPolicy, dig)
	}
	// The bytes are in CAS, unmodified: what was authored is what is
	// content-addressed.
	stored, err := ws.GetObject(dig)
	if err != nil {
		t.Fatalf("policy not in CAS: %v", err)
	}
	if object.DigestBytes(stored) != dig {
		t.Error("stored bytes do not digest to the recorded digest")
	}
	st, err := loadState(ws.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	ws.Close()
	before := 0
	for _, pr := range st.Policies {
		if pr.Dig == dig {
			before++
		}
	}
	if before != 1 {
		t.Fatalf("policy.created events for %s = %d, want 1", dig, before)
	}

	// Idempotent: re-using a recorded policy does not re-append it.
	mustMvo(t, "policy", "use", "rank-two-keys", "--dir", repo)
	ws2, err := workspace.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	st2, err := loadState(ws2.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	after := 0
	for _, pr := range st2.Policies {
		if pr.Dig == dig {
			after++
		}
	}
	if after != 1 {
		t.Errorf("policy.created events after re-use = %d, want 1 (idempotent)", after)
	}
	// And the listing now says which one is default.
	list := mustMvo(t, "policy", "list", "--dir", repo)
	for _, line := range strings.Split(list, "\n") {
		if strings.HasPrefix(line, "rank-two-keys") && !strings.Contains(line, "recorded (default)") {
			t.Errorf("row %q: the installed policy must read as the recorded default", line)
		}
	}
}

func TestPolicyUseRefusals(t *testing.T) {
	repo := initWorkspace(t)
	fixturePolicy(t, repo, "legacy-v0")
	fixturePolicy(t, repo, "bad-gate")

	// A shape whose gate its digest does not determine must never become
	// the silent default (M1e decision 18).
	_, stderr, code := mvo(t, "policy", "use", "legacy-v0", "--dir", repo)
	if code != exitFail {
		t.Fatalf("exit = %d, want 1", code)
	}
	for _, want := range []string{"legacy-v0.json is policy/v0", "cannot name its oracles", "policy/v1"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q missing %q", stderr, want)
		}
	}

	// An invalid policy never becomes the default either.
	_, stderr, code = mvo(t, "policy", "use", "bad-gate", "--dir", repo)
	if code != exitFail || !strings.Contains(stderr, `unknown gate "suite-passes"`) {
		t.Errorf("use of an invalid policy: exit %d, stderr %q", code, stderr)
	}

	// The in-document name must equal the filename stem: `use` takes a
	// name, and a file that lies about its own is not usable by it.
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "toyrepo", "policies", "rank-two-keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(repo, workspace.DirName, "policies", "mine.json")
	if err := os.WriteFile(renamed, b, 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = mvo(t, "policy", "use", "mine", "--dir", repo)
	if code != exitFail || !strings.Contains(stderr, "must equal its filename stem") {
		t.Errorf("name/stem mismatch: exit %d, stderr %q", code, stderr)
	}

	_, _, code = mvo(t, "policy", "use", "absent", "--dir", repo)
	if code != exitFail {
		t.Errorf("use of a missing file: exit %d, want 1", code)
	}

	// The default is untouched by every refusal above.
	ws, err := workspace.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	pol, err := policy.Load(ws.CAS, ws.Config.DefaultPolicy)
	if err != nil {
		t.Fatalf("default policy no longer loads: %v", err)
	}
	if pol.Digest != ws.Config.DefaultPolicy {
		t.Error("default policy digest drifted")
	}
}

func TestPolicyUsage(t *testing.T) {
	repo := initWorkspace(t)
	tests := []struct {
		name string
		args []string
	}{
		{"no verb", []string{"policy"}},
		{"unknown verb", []string{"policy", "explain"}},
		{"show without a reference", []string{"policy", "show", "--dir", repo}},
		{"use without a name", []string{"policy", "use", "--dir", repo}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, code := mvo(t, tt.args...); code != exitUsage {
				t.Errorf("exit = %d, want %d", code, exitUsage)
			}
		})
	}
}

// A policy installed by `use` resolves by name from the LEDGER even after
// its file is deleted: replay never depends on the authoring directory.
func TestPolicyResolvesFromLedgerAfterFileRemoval(t *testing.T) {
	repo := initWorkspace(t)
	path, dig := fixturePolicy(t, repo, "rank-two-keys")
	mustMvo(t, "policy", "use", "rank-two-keys", "--dir", repo)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	out := mustMvo(t, "policy", "show", "rank-two-keys", "--json", "--dir", repo)
	if object.DigestBytes([]byte(out)) != dig {
		t.Errorf("ledger resolution returned different bytes than were recorded")
	}
	// And by digest, from CAS, which is never pruned.
	out = mustMvo(t, "policy", "show", dig, "--json", "--dir", repo)
	if object.DigestBytes([]byte(out)) != dig {
		t.Errorf("CAS resolution returned different bytes than were recorded")
	}
}

// An intent PINS a policy: --policy resolves it exactly as `policy show`
// does, ingests it so replay never depends on the authoring file, and warns
// when the pinned shape cannot name its own oracles.
func TestIntentNewPinsPolicy(t *testing.T) {
	repo := initWorkspace(t)
	gitCLI(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "commit", "-q", "-m", "baseline")

	_, v1Dig := fixturePolicy(t, repo, "rank-two-keys")
	stdout, stderr, code := mvo(t, "intent", "new", "--dir", repo, "--title", "pinned", "--policy", "rank-two-keys")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("pinning a v1 policy warned: %q", stderr)
	}
	intentDig := strings.TrimSpace(stdout)

	ws, err := workspace.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	raw, err := ws.GetObject(intentDig)
	if err != nil {
		t.Fatal(err)
	}
	var in object.Intent
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	if in.Policy != v1Dig {
		t.Errorf("intent policy = %s, want the pinned %s", in.Policy, v1Dig)
	}
	// The pinned policy is in CAS and recorded, so replay never depends on
	// the file it was authored in.
	if _, err := policy.Load(ws.CAS, in.Policy); err != nil {
		t.Errorf("pinned policy does not load: %v", err)
	}
	if ws.Config.DefaultPolicy == v1Dig {
		t.Error("--policy must pin per intent, not change the workspace default")
	}

	// A v0 policy is pinnable — deliberately, per intent — and says what
	// that costs.
	fixturePolicy(t, repo, "legacy-v0")
	_, stderr, code = mvo(t, "intent", "new", "--dir", repo, "--title", "legacy", "--policy", "legacy-v0")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"is policy/v0", "--oracle-cmd is required", "M1e decision 18"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("warning %q missing %q", stderr, want)
		}
	}

	if _, _, code := mvo(t, "intent", "new", "--dir", repo, "--title", "x", "--policy", "nope"); code != exitFail {
		t.Errorf("--policy with an unknown reference: exit %d, want 1", code)
	}
}

// A policy that declares NO hard gate is refused wherever a policy enters
// the workspace. Only the v0 shape can express it — `Validate` enforces
// "at least one hard gate" for v1, and v0 is decoded exactly as M0 decoded
// it, never re-validated — and the consequences are decisive: every world
// passes a gate list with nothing in it, and `admit` would sign an ADMIT no
// gate ever justified. `policy.Decode` stays total, so historical ledgers
// and published closures keep replaying whatever was recorded.
func TestZeroGatePolicyIsRefusedAtIngest(t *testing.T) {
	// A real repo: `intent new` reads HEAD before it resolves the pin, and
	// the pin is the boundary under test.
	repo := t.TempDir()
	gitCLI(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "commit", "-q", "-m", "baseline")
	mustMvo(t, "init", "--dir", repo)

	const zeroGate = `{"hard_gates":[],"ranking":["gate_pass","wall_ms_asc"],"schema":"multiverso.dev/policy/v0"}`
	path := filepath.Join(repo, workspace.DirName, "policies", "no-gates.json")
	if err := os.WriteFile(path, []byte(zeroGate), 0o644); err != nil {
		t.Fatal(err)
	}

	// The compiled policy really is gate-less: this is not a decode error.
	pol, err := policy.Decode([]byte(zeroGate))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(pol.Gates) != 0 {
		t.Fatalf("fixture compiled to %d gates, want 0", len(pol.Gates))
	}

	out, _, code := mvo(t, "policy", "validate", path, "--dir", repo)
	if code == 0 {
		t.Errorf("policy validate accepted a gate-less policy:\n%s", out)
	}
	if _, errOut, _ := mvo(t, "policy", "validate", path, "--dir", repo); !strings.Contains(errOut, "at least one hard gate") {
		t.Errorf("policy validate does not say why:\n%s", errOut)
	}
	if _, errOut, code := mvo(t, "policy", "use", "no-gates", "--dir", repo); code == 0 ||
		!strings.Contains(errOut, "policy/v0") {
		t.Errorf("policy use accepted a gate-less v0 policy: exit %d\n%s", code, errOut)
	}
	// The pin is the boundary that matters: an intent pinning this policy
	// would make every later race and admission unattested.
	_, errOut, code := mvo(t, "intent", "new", "--dir", repo, "--title", "gateless", "--policy", "no-gates")
	if code == 0 {
		t.Fatalf("intent new pinned a gate-less policy")
	}
	if !strings.Contains(errOut, "at least one hard gate") {
		t.Errorf("intent new does not say why:\n%s", errOut)
	}
}
