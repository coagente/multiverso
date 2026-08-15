package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/attest"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/signing"
	"github.com/coagente/multiverso/internal/workspace"
)

const fixPatch = `--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-hello
+goodbye
`

var trailerKeyRe = regexp.MustCompile(`(?m)^Multiverso-Attestation: (sha256:[0-9a-f]{64})$`)

func gitCLI(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := []string{
		"-c", "user.name=mvo-test",
		"-c", "user.email=mvo-test@invalid",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// mvo runs the CLI in-process and returns stdout, stderr, and exit code.
func mvo(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func mustMvo(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, code := mvo(t, args...)
	if code != 0 {
		t.Fatalf("mvo %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, stdout, stderr)
	}
	return stdout
}

// scenario is a repo taken through init → intent → race (SELECT), ready
// for admission.
type scenario struct {
	repo      string
	intentDig string
	branch    string
	parent    string // trunk head before admission
	commit    string // admitted commit, set by admit
}

func newScenario(t *testing.T) *scenario {
	t.Helper()
	repo := t.TempDir()
	gitCLI(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "commit", "-q", "-m", "baseline")

	mustMvo(t, "init", "--dir", repo)
	// Commit the .gitignore that init wrote so the working tree is clean
	// and the admission fast-forward path runs.
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "commit", "-q", "-m", "ignore workspace")

	// The intent pins a synthesized command-oracle policy (M1e decision 18):
	// this repo has no Python suite, so the gate is a command — named INSIDE
	// the pinned artifact, where the policy digest determines it, instead of
	// on a race-time flag.
	intentDig := strings.TrimSpace(mustMvo(t, "intent", "new", "--dir", repo,
		"--title", "fix hello", "--oracle-cmd", "true"))
	patches := t.TempDir()
	if err := os.WriteFile(filepath.Join(patches, "patch-a.patch"), []byte(fixPatch), 0o644); err != nil {
		t.Fatal(err)
	}
	mustMvo(t, "race", intentDig, "--dir", repo, "--patches", patches)

	return &scenario{
		repo:      repo,
		intentDig: intentDig,
		branch:    gitCLI(t, repo, "symbolic-ref", "--short", "HEAD"),
		parent:    gitCLI(t, repo, "rev-parse", "HEAD"),
	}
}

func (sc *scenario) admit(t *testing.T) string {
	t.Helper()
	out := mustMvo(t, "admit", sc.intentDig, "--dir", sc.repo)
	sc.commit = gitCLI(t, sc.repo, "rev-parse", "HEAD")
	return out
}

func openWS(t *testing.T, repo string) *workspace.Workspace {
	t.Helper()
	ws, err := workspace.Open(repo)
	if err != nil {
		t.Fatalf("workspace.Open: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

// admission is the decoded honest attestation of sc.commit.
type admission struct {
	bundleKey   string
	stmt        attest.Statement
	landingTree string // "git:…" tree of the admitted commit
	parentTree  string // "git:…" tree of its parent
}

func loadAdmission(t *testing.T, ws *workspace.Workspace, sc *scenario) *admission {
	t.Helper()
	msg, err := gitx.CommitMessage(sc.repo, sc.commit)
	if err != nil {
		t.Fatal(err)
	}
	m := trailerKeyRe.FindAllStringSubmatch(msg, -1)
	if len(m) == 0 {
		t.Fatalf("no attestation trailer in %q", msg)
	}
	bundleKey := m[len(m)-1][1]
	bundle, err := ws.CAS.Get(bundleKey)
	if err != nil {
		t.Fatal(err)
	}
	var env signing.Envelope
	if err := json.Unmarshal(bundle, &env); err != nil {
		t.Fatal(err)
	}
	signer, err := ws.Signer()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := signing.Verify(env, signer.Public)
	if err != nil {
		t.Fatal(err)
	}
	var stmt attest.Statement
	if err := json.Unmarshal(payload, &stmt); err != nil {
		t.Fatal(err)
	}
	landingTree, err := gitx.TreeOf(sc.repo, sc.commit)
	if err != nil {
		t.Fatal(err)
	}
	parentTree, err := gitx.TreeOf(sc.repo, sc.parent)
	if err != nil {
		t.Fatal(err)
	}
	return &admission{bundleKey: bundleKey, stmt: stmt, landingTree: landingTree, parentTree: parentTree}
}

// recordObj mirrors what the engine does: canonical bytes into CAS and the
// ledger. Used to fabricate doctored-but-recorded objects.
func recordObj(t *testing.T, ws *workspace.Workspace, typ string, v any) string {
	t.Helper()
	dig, canon, err := object.Digest(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.CAS.Put(canon); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Ledger.Append(typ, canon); err != nil {
		t.Fatal(err)
	}
	return dig
}

// commitWithTrailer fabricates a commit over (tree, parent) whose trailer
// names bundleKey.
func commitWithTrailer(t *testing.T, repo, tree, parent, bundleKey string) string {
	t.Helper()
	msg := "doctored admission\n\nMultiverso-Attestation: " + bundleKey + "\n"
	commit, err := gitx.CommitTree(repo, tree, parent, msg)
	if err != nil {
		t.Fatalf("CommitTree: %v", err)
	}
	return commit
}

// fabricate signs stmt with the workspace key, stores the bundle, and
// commits (tree, parent) with its trailer.
func fabricate(t *testing.T, ws *workspace.Workspace, repo string, stmt attest.Statement, tree, parent string) string {
	t.Helper()
	signer, err := ws.Signer()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := object.Canonical(stmt)
	if err != nil {
		t.Fatal(err)
	}
	env, err := signing.Sign(signer, signing.PayloadTypeInToto, payload)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := object.Canonical(env)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ws.CAS.Put(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return commitWithTrailer(t, repo, tree, parent, key)
}

func TestAdmitCLI(t *testing.T) {
	sc := newScenario(t)
	out := sc.admit(t)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("admit output = %q, want 4 lines", out)
	}
	if !strings.HasPrefix(lines[0], "ADMIT mv0:") {
		t.Errorf("line 1 = %q, want ADMIT <world>", lines[0])
	}
	if want := "commit:      " + sc.commit; lines[1] != want {
		t.Errorf("line 2 = %q, want %q", lines[1], want)
	}
	if !strings.HasPrefix(lines[2], "attestation: sha256:") {
		t.Errorf("line 3 = %q, want attestation key", lines[2])
	}
	if !strings.HasPrefix(lines[3], "decision:    mv0:") {
		t.Errorf("line 4 = %q, want decision digest", lines[3])
	}
	if sc.commit == sc.parent {
		t.Error("admit did not move HEAD")
	}
	got, err := os.ReadFile(filepath.Join(sc.repo, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "goodbye\n" {
		t.Errorf("hello.txt = %q, want fast-forwarded content", got)
	}

	// explain renders the ADMIT decision with a winner line.
	explain := mustMvo(t, "explain", sc.intentDig, "--dir", sc.repo)
	if !strings.Contains(explain, "type:      ADMIT") {
		t.Errorf("explain = %q, want ADMIT type", explain)
	}
	if !strings.Contains(explain, "winner:    mv0:") {
		t.Errorf("explain = %q, want winner line", explain)
	}

	// One admission per intent: the second admit is refused.
	_, stderr, code := mvo(t, "admit", sc.intentDig, "--dir", sc.repo)
	if code != 1 {
		t.Fatalf("second admit: exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "intent already admitted (commit "+sc.commit+")") {
		t.Errorf("second admit stderr = %q, want already-admitted guard", stderr)
	}
}

func TestAdmitCLIRequiresSelect(t *testing.T) {
	repo := t.TempDir()
	gitCLI(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "commit", "-q", "-m", "baseline")
	mustMvo(t, "init", "--dir", repo)
	intentDig := strings.TrimSpace(mustMvo(t, "intent", "new", "--dir", repo, "--title", "fix hello"))

	_, stderr, code := mvo(t, "admit", intentDig, "--dir", repo)
	if code != 1 {
		t.Fatalf("admit without race: exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "no SELECT decision for intent") {
		t.Errorf("stderr = %q, want no-SELECT error", stderr)
	}
}

func TestAdmitCLIEscalatesOnConflict(t *testing.T) {
	sc := newScenario(t)
	// Move trunk so the winner's patch conflicts (CP-8).
	if err := os.WriteFile(filepath.Join(sc.repo, "hello.txt"), []byte("conflicting\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCLI(t, sc.repo, "add", "-A")
	gitCLI(t, sc.repo, "commit", "-q", "-m", "trunk moved")

	_, stderr, code := mvo(t, "admit", sc.intentDig, "--dir", sc.repo)
	if code != 1 {
		t.Fatalf("conflicting admit: exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "ESCALATE") || !strings.Contains(stderr, "never auto-resolved (CP-8)") {
		t.Errorf("stderr = %q, want ESCALATE with CP-8 rationale", stderr)
	}
}

func TestInitKeysFlag(t *testing.T) {
	repo := t.TempDir()
	gitCLI(t, repo, "init", "-q")
	mustMvo(t, "init", "--dir", repo)

	// Keys already exist (fresh init generates them): --keys refuses.
	_, stderr, code := mvo(t, "init", "--keys", "--dir", repo)
	if code != 1 {
		t.Fatalf("init --keys over existing keys: exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "refusing to overwrite") {
		t.Errorf("stderr = %q, want overwrite refusal", stderr)
	}

	// Pre-M1a workspace (keys removed): --keys generates them.
	if err := os.RemoveAll(filepath.Join(repo, workspace.DirName, "keys")); err != nil {
		t.Fatal(err)
	}
	out := mustMvo(t, "init", "--keys", "--dir", repo)
	if !strings.Contains(out, "generated signing key mv0:") {
		t.Errorf("init --keys output = %q, want generated key line", out)
	}
	ws := openWS(t, repo)
	if _, err := ws.Signer(); err != nil {
		t.Errorf("Signer after init --keys: %v", err)
	}
}

func TestVerifyHappyPath(t *testing.T) {
	sc := newScenario(t)
	sc.admit(t)

	out := mustMvo(t, "verify", "HEAD", "--dir", sc.repo)
	if !strings.Contains(out, "commit:      "+sc.commit) {
		t.Errorf("verify output = %q, want commit line", out)
	}
	if !strings.Contains(out, "OK: attestation verified (7 checks)") {
		t.Errorf("verify output = %q, want OK line", out)
	}

	// --json: all seven checks true.
	jsonOut := mustMvo(t, "verify", "HEAD", "--json", "--dir", sc.repo)
	var report verifyReport
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("decode report %q: %v", jsonOut, err)
	}
	if report.Schema != schemaVerifyReport || !report.OK || report.Error != "" {
		t.Errorf("report = %+v, want ok with no error", report)
	}
	if report.Commit != sc.commit {
		t.Errorf("report commit = %q, want %q", report.Commit, sc.commit)
	}
	if len(report.Checks) != 7 {
		t.Errorf("checks = %v, want 7 entries", report.Checks)
	}
	for name, ok := range report.Checks {
		if !ok {
			t.Errorf("check %s = false, want true", name)
		}
	}

	// --key with the workspace public key path exercises the flag.
	pub := filepath.Join(sc.repo, workspace.DirName, "keys", signing.PubName)
	mustMvo(t, "verify", "HEAD", "--key", pub, "--dir", sc.repo)
}

// One failing verification per check, in check order; the bundle byte-flip
// runs last because it corrupts the honest bundle blob.
func TestVerifyFailures(t *testing.T) {
	sc := newScenario(t)
	sc.admit(t)
	ws := openWS(t, sc.repo)
	adm := loadAdmission(t, ws, sc)

	expectFail := func(t *testing.T, check, rev string, extra ...string) string {
		t.Helper()
		args := append([]string{"verify", rev, "--dir", sc.repo}, extra...)
		_, stderr, code := mvo(t, args...)
		if code != 1 {
			t.Fatalf("verify %s: exit %d, want 1\nstderr: %s", rev, code, stderr)
		}
		if !strings.Contains(stderr, "verify: "+check+":") {
			t.Errorf("stderr = %q, want failure named %q", stderr, check)
		}
		return stderr
	}

	t.Run("bundle_digest missing trailer", func(t *testing.T) {
		stderr := expectFail(t, "bundle_digest", sc.parent)
		if !strings.Contains(stderr, "no Multiverso-Attestation trailer") {
			t.Errorf("stderr = %q", stderr)
		}
	})

	t.Run("signature wrong key", func(t *testing.T) {
		otherDir := filepath.Join(t.TempDir(), "keys")
		if _, err := signing.Generate(otherDir); err != nil {
			t.Fatal(err)
		}
		expectFail(t, "signature", "HEAD", "--key", filepath.Join(otherDir, signing.PubName))
	})

	t.Run("statement wrong type", func(t *testing.T) {
		stmt := adm.stmt
		stmt.Type = "https://in-toto.io/Statement/v0.1"
		commit := fabricate(t, ws, sc.repo, stmt, adm.landingTree, sc.parent)
		expectFail(t, "statement", commit)
	})

	t.Run("subject wrong tree", func(t *testing.T) {
		// Honest bundle, but the enclosing commit carries the parent's
		// tree instead of the attested landing tree.
		commit := commitWithTrailer(t, sc.repo, adm.parentTree, sc.parent, adm.bundleKey)
		expectFail(t, "subject", commit)
	})

	t.Run("references missing object", func(t *testing.T) {
		stmt := adm.stmt
		stmt.Predicate.Intent = "mv0:" + strings.Repeat("00", 32)
		commit := fabricate(t, ws, sc.repo, stmt, adm.landingTree, sc.parent)
		expectFail(t, "references", commit)
	})

	t.Run("freshness stale gate receipt", func(t *testing.T) {
		// Fabricate a fully recorded admission whose suite receipt was
		// valid for the wrong tree: references passes, freshness fails.
		var apply, gate object.Receipt
		var applyDig string
		for _, dig := range adm.stmt.Predicate.Evidence {
			var rec object.Receipt
			if err := loadFromCAS(ws, dig, &rec); err != nil {
				t.Fatal(err)
			}
			if rec.Family == "suite" {
				gate = rec
			} else {
				apply, applyDig = rec, dig
			}
		}
		docGate := gate
		docGate.Freshness.ValidFor.Tree = adm.parentTree // stale: not the landing tree
		docGateDig := recordObj(t, ws, evReceiptRecorded, docGate)

		var honest object.Decision
		if err := loadFromCAS(ws, adm.stmt.Predicate.Decision, &honest); err != nil {
			t.Fatal(err)
		}
		docDec := honest
		docDec.Evidence = []string{applyDig, docGateDig}
		sort.Strings(docDec.Evidence)
		docDecDig := recordObj(t, ws, evDecisionRecorded, docDec)

		stmt := adm.stmt
		stmt.Predicate.Decision = docDecDig
		stmt.Predicate.Evidence = docDec.Evidence
		stmt.Predicate.BudgetConsumed.WallMS = apply.Cost.WallMS + docGate.Cost.WallMS
		commit := fabricate(t, ws, sc.repo, stmt, adm.landingTree, sc.parent)
		expectFail(t, "freshness", commit)
	})

	t.Run("budget mismatch", func(t *testing.T) {
		stmt := adm.stmt
		stmt.Predicate.BudgetConsumed.WallMS++
		commit := fabricate(t, ws, sc.repo, stmt, adm.landingTree, sc.parent)
		expectFail(t, "budget", commit)
	})

	t.Run("bundle_digest tampered bundle", func(t *testing.T) {
		hex := strings.TrimPrefix(adm.bundleKey, "sha256:")
		path := filepath.Join(sc.repo, workspace.DirName, "cas", "sha256", hex[:2], hex[2:])
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		b[0] ^= 0xFF
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}

		// --json still emits the report before exit 1.
		stdout, stderr, code := mvo(t, "verify", "HEAD", "--json", "--dir", sc.repo)
		if code != 1 {
			t.Fatalf("verify tampered: exit %d, want 1\nstderr: %s", code, stderr)
		}
		var report verifyReport
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatalf("decode report %q: %v", stdout, err)
		}
		if report.OK || report.Checks["bundle_digest"] {
			t.Errorf("report = %+v, want bundle_digest failure", report)
		}
		if !strings.HasPrefix(report.Error, "bundle_digest: ") {
			t.Errorf("report error = %q, want bundle_digest detail", report.Error)
		}
	})
}

func TestAuditReplaysAdmission(t *testing.T) {
	sc := newScenario(t)
	sc.admit(t)

	out := mustMvo(t, "audit", "--json", "--dir", sc.repo)
	var report auditReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode report %q: %v", out, err)
	}
	if !report.ChainOK || !report.ReplayIdentical {
		t.Fatalf("report = %+v, want chain_ok and replay_identical", report)
	}
	if report.Decisions != 2 {
		t.Errorf("decisions = %d, want 2 (SELECT + ADMIT)", report.Decisions)
	}
	if report.Admissions != 1 {
		t.Errorf("admissions = %d, want 1", report.Admissions)
	}

	// A doctored admission decision appended to the ledger must be caught:
	// replay recomputes the honest rationale from the recorded receipts.
	ws := openWS(t, sc.repo)
	adm := loadAdmission(t, ws, sc)
	var honest object.Decision
	if err := loadFromCAS(ws, adm.stmt.Predicate.Decision, &honest); err != nil {
		t.Fatal(err)
	}
	doctored := honest
	doctored.Rationale = "doctored rationale"
	recordObj(t, ws, evDecisionRecorded, doctored)

	stdout, _, code := mvo(t, "audit", "--json", "--dir", sc.repo)
	if code != 1 {
		t.Fatalf("audit with doctored decision: exit %d, want 1", code)
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode report %q: %v", stdout, err)
	}
	if report.ReplayIdentical || len(report.Mismatches) != 1 {
		t.Fatalf("report = %+v, want exactly one mismatch", report)
	}
	if !strings.Contains(report.Mismatches[0].Detail, "rationale") {
		t.Errorf("mismatch detail = %q, want rationale divergence", report.Mismatches[0].Detail)
	}
	if report.Admissions != 2 {
		t.Errorf("admissions = %d, want 2 (honest + doctored)", report.Admissions)
	}
}
