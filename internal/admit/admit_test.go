package admit

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/attest"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/signing"
)

func git(t *testing.T, dir string, args ...string) string {
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

// initRepo creates a repo whose hello.txt says "hello".
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

const winnerPatch = `--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-hello
+goodbye
`

var trailerRe = regexp.MustCompile(`(?m)^Multiverso-Attestation: (sha256:[0-9a-f]{64})$`)

// fixture is a fully seeded admission: repo + CAS/ledger + intent, winner
// world, race suite receipt, and SELECT decision — everything cmd/mvo
// would have recorded before `mvo admit`.
type fixture struct {
	repo      string
	store     *cas.Store
	led       *ledger.Ledger
	signer    *signing.Signer
	admitDir  string
	intentDig string
	winnerDig string
	selDig    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	repo := initRepo(t)
	work := t.TempDir()
	store, err := cas.Open(filepath.Join(work, "cas"))
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	led, err := ledger.Open(filepath.Join(work, "ledger.db"))
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	t.Cleanup(func() { led.Close() })
	signer, err := signing.Generate(filepath.Join(work, "keys"))
	if err != nil {
		t.Fatalf("signing.Generate: %v", err)
	}

	put := func(v any) string {
		t.Helper()
		dig, canon, err := object.Digest(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Put(canon); err != nil {
			t.Fatal(err)
		}
		return dig
	}

	commit, tree, err := gitx.Head(repo)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	polDig := put(testPolicy())
	intentDig := put(object.Intent{
		Schema:    object.SchemaIntent,
		Base:      object.Base{Commit: commit, Tree: tree},
		Spec:      object.Spec{Title: "fix hello", Description: "hello should say goodbye"},
		Budget:    object.Budget{MaxCandidates: 2, MaxWallMS: 60000},
		Policy:    polDig,
		CreatedAt: "2026-01-01T00:00:00Z",
	})
	patchKey, err := store.Put([]byte(winnerPatch))
	if err != nil {
		t.Fatal(err)
	}
	winnerDig := put(object.World{
		Schema: object.SchemaWorld, Intent: intentDig, Tree: tree, Env: "mv0:env",
		IsolationTier: "T0-worktree",
		Producer:      object.Producer{Adapter: "script@v0", IdentityTier: "claimed", Role: "generator"},
		Context:       patchKey, // script: the prompt IS the patch (M1b decision 7)
		Patch:         patchKey,
		Trace:         "sha256:" + strings.Repeat("e", 64),
		Cost:          object.RunCost{WallMS: 1, Source: "none"},
		Outcome:       "COMPLETED", CreatedAt: "2026-01-01T00:00:01Z",
	})
	suiteRec := gateReceipt("pass")
	suiteRec.World = winnerDig
	suiteRec.Execution.Argv = []string{"true"}
	suiteDig := put(suiteRec)
	selDig := put(object.Decision{
		Schema: object.SchemaDecision, Type: "SELECT", Intent: intentDig,
		Subject: []string{winnerDig}, Evidence: []string{suiteDig}, Policy: polDig,
		Rationale: "1/1 worlds passed", CreatedAt: "2026-01-01T00:00:02Z",
	})
	return &fixture{
		repo: repo, store: store, led: led, signer: signer,
		admitDir:  filepath.Join(work, "admit"),
		intentDig: intentDig, winnerDig: winnerDig, selDig: selDig,
	}
}

func (f *fixture) config(argv ...string) Config {
	return Config{
		Repo: f.repo, Ledger: f.led, CAS: f.store,
		Intent: f.intentDig, SelectDig: f.selDig,
		Oracle:   &oracle.CommandOracle{Argv: argv, Timeout: time.Minute, CAS: f.store},
		Signer:   f.signer,
		AdmitDir: f.admitDir,
	}
}

func (f *fixture) eventTypes(t *testing.T) []string {
	t.Helper()
	var types []string
	if err := f.led.Scan(func(e ledger.Event) error {
		types = append(types, e.Type)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return types
}

func TestRunAdmitLandsSignedCommit(t *testing.T) {
	f := newFixture(t)
	preHead := git(t, f.repo, "rev-parse", "HEAD")
	branch := git(t, f.repo, "symbolic-ref", "--short", "HEAD")

	res, err := Run(context.Background(), f.config("true"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision.Type != TypeAdmit {
		t.Fatalf("decision = %s (%s), want ADMIT", res.Decision.Type, res.Decision.Rationale)
	}
	if res.Branch != branch {
		t.Errorf("branch = %q, want %q", res.Branch, branch)
	}
	for name, v := range map[string]string{
		"commit": res.Commit, "statement": res.StatementDig, "attestation": res.AttestationKey,
		"apply receipt": res.ApplyReceipt, "gate receipt": res.GateReceipt,
	} {
		if v == "" {
			t.Errorf("Result.%s is empty", name)
		}
	}

	// The branch moved to the new commit and the clean working tree was
	// fast-forwarded.
	if head := git(t, f.repo, "rev-parse", "HEAD"); head != res.Commit {
		t.Errorf("HEAD = %s, want admitted commit %s", head, res.Commit)
	}
	if head := git(t, f.repo, "rev-parse", "refs/heads/"+branch); head != res.Commit {
		t.Errorf("branch tip = %s, want %s", head, res.Commit)
	}
	got, err := os.ReadFile(filepath.Join(f.repo, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "goodbye\n" {
		t.Errorf("hello.txt = %q, want fast-forwarded content", got)
	}
	if parent, err := gitx.ParentOf(f.repo, res.Commit); err != nil || parent != preHead {
		t.Errorf("ParentOf = %q, %v; want %q", parent, err, preHead)
	}

	// Commit identity and message shape.
	if id := git(t, f.repo, "log", "-1", "--format=%an <%ae>"); id != gitx.CommitterName+" <"+gitx.CommitterEmail+">" {
		t.Errorf("committer identity = %q", id)
	}
	msg, err := gitx.CommitMessage(f.repo, res.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg, "fix hello\n\nMultiverso-Intent: "+f.intentDig+"\n") {
		t.Errorf("message = %q, want intent title subject + trailers", msg)
	}
	m := trailerRe.FindStringSubmatch(msg)
	if m == nil {
		t.Fatalf("message %q has no attestation trailer", msg)
	}
	if m[1] != res.AttestationKey {
		t.Errorf("trailer = %s, want %s", m[1], res.AttestationKey)
	}

	// The trailer resolves to a bundle that verifies against the local key
	// and binds the landing tree + parent commit.
	bundle, err := f.store.Get(res.AttestationKey)
	if err != nil {
		t.Fatalf("bundle from CAS: %v", err)
	}
	var env signing.Envelope
	if err := json.Unmarshal(bundle, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.PayloadType != signing.PayloadTypeInToto {
		t.Errorf("payloadType = %q", env.PayloadType)
	}
	payload, err := signing.Verify(env, f.signer.Public)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if object.DigestBytes(payload) != res.StatementDig {
		t.Errorf("statement digest mismatch")
	}
	var stmt attest.Statement
	if err := json.Unmarshal(payload, &stmt); err != nil {
		t.Fatalf("decode statement: %v", err)
	}
	tree, err := gitx.TreeOf(f.repo, res.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if got := stmt.Subject[0].Digest["gitTree"]; "git:"+got != tree {
		t.Errorf("subject gitTree = %q, commit tree = %q", got, tree)
	}
	if stmt.Subject[0].Name != "refs/heads/"+branch {
		t.Errorf("subject name = %q", stmt.Subject[0].Name)
	}
	p := stmt.Predicate
	if p.Intent != f.intentDig || p.World != f.winnerDig || p.Decision != res.DecisionDigest ||
		p.SelectDecision != f.selDig || p.ProducerKeyID != f.signer.KeyID ||
		p.Trunk.Branch != branch || p.Trunk.ParentCommit != preHead {
		t.Errorf("predicate = %+v", p)
	}

	// Ledger event order for one clean admission.
	want := []string{
		"admission.started", "receipt.recorded", "receipt.recorded",
		"decision.recorded", "attestation.recorded", "admission.finished",
	}
	got2 := f.eventTypes(t)
	if strings.Join(got2, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got2, want)
	}

	// Landing worktree cleaned up.
	entries, err := os.ReadDir(f.admitDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("admit dir not empty after Run: %v", entries)
	}
}

func TestRunEscalatesOnConflict(t *testing.T) {
	f := newFixture(t)
	// Move trunk so the winner's patch no longer applies (CP-8 fixture).
	if err := os.WriteFile(filepath.Join(f.repo, "hello.txt"), []byte("conflicting change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, f.repo, "add", "-A")
	git(t, f.repo, "commit", "-q", "-m", "trunk moved")
	preHead := git(t, f.repo, "rev-parse", "HEAD")

	res, err := Run(context.Background(), f.config("true"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision.Type != TypeEscalate {
		t.Fatalf("decision = %s, want ESCALATE", res.Decision.Type)
	}
	if res.Commit != "" || res.GateReceipt != "" || res.AttestationKey != "" {
		t.Errorf("conflict Result carries landing fields: %+v", res)
	}
	if !strings.Contains(res.Decision.Rationale, "never auto-resolved (CP-8)") {
		t.Errorf("rationale = %q", res.Decision.Rationale)
	}
	if head := git(t, f.repo, "rev-parse", "HEAD"); head != preHead {
		t.Errorf("HEAD moved to %s on ESCALATE", head)
	}

	// CP-8: the conflict set survives as the apply receipt's stderr
	// artifact in CAS.
	var apply object.Receipt
	if err := loadObject(f.store, res.ApplyReceipt, &apply); err != nil {
		t.Fatalf("load apply receipt: %v", err)
	}
	if apply.Result.Status != "fail" || apply.Execution.ExitCode != 1 {
		t.Errorf("apply receipt = %+v, want fail/exit 1", apply.Result)
	}
	if len(apply.Result.Artifacts) != 2 {
		t.Fatalf("artifacts = %v, want [stdout, stderr]", apply.Result.Artifacts)
	}
	stderrBytes, err := f.store.Get(apply.Result.Artifacts[1])
	if err != nil {
		t.Fatalf("conflict stderr from CAS: %v", err)
	}
	if len(stderrBytes) == 0 {
		t.Error("conflict stderr artifact is empty")
	}

	want := []string{"admission.started", "receipt.recorded", "decision.recorded", "admission.finished"}
	if got := f.eventTypes(t); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got, want)
	}
	// The finished event says ESCALATE.
	var lastPayload []byte
	if err := f.led.Scan(func(e ledger.Event) error {
		if e.Type == "admission.finished" {
			lastPayload = e.Payload
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Result string `json:"result"`
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal(lastPayload, &body); err != nil {
		t.Fatal(err)
	}
	if body.Result != TypeEscalate || body.Commit != "" {
		t.Errorf("admission.finished = %+v, want ESCALATE with no commit", body)
	}
}

func TestRunRejectsOnGateFailure(t *testing.T) {
	f := newFixture(t)
	preHead := git(t, f.repo, "rev-parse", "HEAD")

	res, err := Run(context.Background(), f.config("false"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Decision.Type != TypeReject {
		t.Fatalf("decision = %s, want REJECT", res.Decision.Type)
	}
	if res.Commit != "" || res.AttestationKey != "" {
		t.Errorf("REJECT Result carries landing fields: %+v", res)
	}
	if res.GateReceipt == "" {
		t.Error("REJECT Result missing gate receipt")
	}
	if head := git(t, f.repo, "rev-parse", "HEAD"); head != preHead {
		t.Errorf("HEAD moved to %s on REJECT", head)
	}
	want := []string{
		"admission.started", "receipt.recorded", "receipt.recorded",
		"decision.recorded", "admission.finished",
	}
	if got := f.eventTypes(t); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got, want)
	}
}

// Detached HEAD errors before anything reaches the ledger.
func TestRunDetachedHeadRecordsNothing(t *testing.T) {
	f := newFixture(t)
	git(t, f.repo, "checkout", "-q", "--detach")
	if _, err := Run(context.Background(), f.config("true")); err == nil {
		t.Fatal("Run on detached HEAD: want error, got nil")
	}
	if got := f.eventTypes(t); len(got) != 0 {
		t.Errorf("events = %v, want none", got)
	}
}

func TestRunConfigValidation(t *testing.T) {
	if _, err := Run(context.Background(), Config{}); err == nil {
		t.Fatal("Run with empty config: want error, got nil")
	}
}
