package workspace

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/signing"
)

func mustInit(t *testing.T, root string) *Workspace {
	t.Helper()
	ws, err := Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

func TestInitLayout(t *testing.T) {
	root := t.TempDir()
	ws := mustInit(t, root)

	for _, rel := range []string{
		"config.json", "ledger.db", "cas", "policies/default.json",
		"keys/" + signing.PrivName, "keys/" + signing.PubName,
	} {
		if _, err := os.Stat(filepath.Join(root, DirName, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	// config.json names the default policy stored in CAS.
	polDig, polCanon, err := object.Digest(DefaultPolicy())
	if err != nil {
		t.Fatalf("digest default policy: %v", err)
	}
	if ws.Config.Schema != SchemaConfig {
		t.Errorf("config schema = %q, want %q", ws.Config.Schema, SchemaConfig)
	}
	if ws.Config.DefaultPolicy != polDig {
		t.Errorf("default_policy = %q, want %q", ws.Config.DefaultPolicy, polDig)
	}
	got, err := ws.GetObject(ws.Config.DefaultPolicy)
	if err != nil {
		t.Fatalf("GetObject(default policy): %v", err)
	}
	if string(got) != string(polCanon) {
		t.Errorf("policy in CAS = %q, want canonical %q", got, polCanon)
	}
	// Since M1e the default is the v1 artifact that names its own oracles
	// (M1e decision 19) — the shape whose digest determines what its gates
	// mean.
	var pol object.PolicyV1
	if err := json.Unmarshal(got, &pol); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	if pol.Schema != object.SchemaPolicyV1 {
		t.Errorf("default policy schema = %q, want %q", pol.Schema, object.SchemaPolicyV1)
	}
	if !reflect.DeepEqual(pol, DefaultPolicy()) {
		t.Errorf("policy = %+v, want %+v", pol, DefaultPolicy())
	}
	// And it compiles: `mvo init` never writes a policy the decision
	// functions could not evaluate.
	if _, err := policy.Decode(got); err != nil {
		t.Errorf("default policy does not compile: %v", err)
	}

	// policies/default.json holds the same canonical bytes.
	onDisk, err := os.ReadFile(filepath.Join(ws.Dir, "policies", "default.json"))
	if err != nil {
		t.Fatalf("read default.json: %v", err)
	}
	if string(onDisk) != string(polCanon) {
		t.Errorf("default.json = %q, want canonical %q", onDisk, polCanon)
	}

	// The policy and keypair are recorded in the ledger in deterministic
	// order and the chain verifies.
	var events []ledger.Event
	if err := ws.Ledger.Scan(func(e ledger.Event) error {
		events = append(events, e)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(events) != 2 || events[0].Type != "policy.created" || events[1].Type != "key.generated" {
		t.Fatalf("events = %+v, want [policy.created, key.generated]", events)
	}
	if events[0].PayloadDig != polDig {
		t.Errorf("recorded payload digest = %q, want %q", events[0].PayloadDig, polDig)
	}

	// key.generated names the keypair on disk.
	pub, keyID, err := signing.LoadPublicKeyFile(filepath.Join(ws.KeysDir(), signing.PubName))
	if err != nil {
		t.Fatalf("LoadPublicKeyFile: %v", err)
	}
	var keyBody struct {
		KeyID     string `json:"key_id"`
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(events[1].Payload, &keyBody); err != nil {
		t.Fatalf("decode key.generated: %v", err)
	}
	if keyBody.KeyID != keyID {
		t.Errorf("key.generated key_id = %q, want %q", keyBody.KeyID, keyID)
	}
	if keyBody.PublicKey != base64.StdEncoding.EncodeToString(pub) {
		t.Errorf("key.generated public_key = %q does not match on-disk key", keyBody.PublicKey)
	}
	if err := ws.Ledger.VerifyChain(); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}

// Keys live under the 0700 keys dir with 0600 modes — and the workspace is
// git-ignored, so they can never enter history.
func TestInitKeyModes(t *testing.T) {
	root := t.TempDir()
	ws := mustInit(t, root)
	info, err := os.Stat(ws.KeysDir())
	if err != nil {
		t.Fatalf("stat keys dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("keys dir mode = %o, want 0700", got)
	}
	for _, name := range []string{signing.PrivName, signing.PubName} {
		info, err := os.Stat(filepath.Join(ws.KeysDir(), name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 0600", name, got)
		}
	}
}

func TestGenerateKeysRefusesExisting(t *testing.T) {
	ws := mustInit(t, t.TempDir())
	if _, err := ws.GenerateKeys(); err == nil {
		t.Fatal("GenerateKeys over existing keys: want error, got nil")
	}
}

func TestSigner(t *testing.T) {
	ws := mustInit(t, t.TempDir())
	s, err := ws.Signer()
	if err != nil {
		t.Fatalf("Signer: %v", err)
	}
	_, keyID, err := signing.LoadPublicKeyFile(filepath.Join(ws.KeysDir(), signing.PubName))
	if err != nil {
		t.Fatal(err)
	}
	if s.KeyID != keyID {
		t.Errorf("Signer KeyID = %q, want %q", s.KeyID, keyID)
	}

	// A pre-M1a workspace (no keys) points the operator at mvo init --keys.
	if err := os.RemoveAll(ws.KeysDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Signer(); err == nil {
		t.Fatal("Signer without keys: want error, got nil")
	} else if !strings.Contains(err.Error(), "mvo init --keys") {
		t.Errorf("error = %q, want mention of `mvo init --keys`", err)
	}
}

func TestInitRefusesReinit(t *testing.T) {
	root := t.TempDir()
	mustInit(t, root)
	if _, err := Init(root); err == nil {
		t.Fatal("re-init: want error, got nil")
	} else if !strings.Contains(err.Error(), "already initialized") {
		t.Errorf("re-init error = %q, want 'already initialized'", err)
	}
}

// TestInitIgnoresViaExcludeInAGitRepo is the regression test for the key
// disclosure this rule exists to prevent. In a real worktree, Init must
// write the ignore rule to the UNTRACKED .git/info/exclude and leave the
// tracked .gitignore — and therefore the working tree — untouched.
//
// The bug it locks out: init dirtied .gitignore, `mvo admit` then warned
// "working tree lags main", the documented remedy `git reset --hard`
// reverted mvo's own line, and the next `git add -A && git commit` swept
// .multiverso/keys/local.key — the unencrypted ed25519 private key that
// signs every attestation in the workspace — into the repo's history.
func TestInitIgnoresViaExcludeInAGitRepo(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	gitignore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("__pycache__/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "-c", "user.name=t", "-c", "user.email=t@invalid",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "baseline")

	ws := mustInit(t, root)

	if ws.Ignore.Fallback {
		t.Fatalf("fell back to the tracked .gitignore in a git repo: %s", ws.Ignore.Reason)
	}
	// Compared through EvalSymlinks: CommonDir resolves symlinks, and on
	// macOS t.TempDir() hands back /var/... for a real /private/var/...
	exclude := filepath.Join(resolve(t, root), ".git", "info", "exclude")
	if resolve(t, ws.Ignore.Path) != exclude {
		t.Errorf("ignore rule written to %q, want %q", ws.Ignore.Path, exclude)
	}
	if b, err := os.ReadFile(exclude); err != nil {
		t.Fatalf("read exclude: %v", err)
	} else if !strings.Contains(string(b), ignoreLine) {
		t.Errorf("exclude does not carry %q:\n%s", ignoreLine, b)
	}
	// The tracked file must be byte-identical to what the user committed.
	if b, err := os.ReadFile(gitignore); err != nil {
		t.Fatalf("read .gitignore: %v", err)
	} else if string(b) != "__pycache__/\n" {
		t.Errorf(".gitignore was modified: %q", b)
	}
	// The working tree must be clean, so admit's fast-forward runs and no
	// operator is ever told to `git reset --hard` to fix it.
	if out := gitRun(t, root, "status", "--porcelain"); out != "" {
		t.Errorf("init dirtied the working tree:\n%s", out)
	}
	// And the key must actually be ignored.
	if out := gitRun(t, root, "check-ignore", ".multiverso/keys/local.key"); out == "" {
		t.Error("git does not ignore .multiverso/keys/local.key")
	}
}

// TestInitReusesAnExistingGitignoreRule keeps a repo that already ignores
// the workspace in its tracked .gitignore working unchanged: the rule is
// honoured where it is and nothing new is written anywhere.
func TestInitReusesAnExistingGitignoreRule(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	gitignore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("vendor/\n.multiverso/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := mustInit(t, root)
	if !ws.Ignore.Existed || ws.Ignore.Path != gitignore {
		t.Errorf("Ignore = %+v, want the existing .gitignore honoured", ws.Ignore)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "info", "exclude")); err == nil {
		if b, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude")); strings.Contains(string(b), ignoreLine) {
			t.Error("wrote a duplicate rule to exclude when .gitignore already had one")
		}
	}
}

// resolve canonicalizes a path for comparison, leaving it as-is when it
// cannot be resolved (the file may not exist yet).
func resolve(t *testing.T, path string) string {
	t.Helper()
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init", "-q")
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	// check-ignore exits 1 when the path is not ignored; the caller reads
	// the empty output rather than failing here.
	if err != nil && args[0] != "check-ignore" {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestInitGitignore covers the FALLBACK path: outside a git worktree there
// is no .git/info/exclude to write, so the rule goes to .gitignore. The
// `mvo init` CLI refuses this case outright; the library keeps working so
// callers that manage their own repo layout are not broken.
func TestInitGitignore(t *testing.T) {
	tests := []struct {
		name     string
		existing string // "" means no .gitignore beforehand
		create   bool
		want     string
	}{
		{"created when missing", "", false, ".multiverso/\n"},
		{"appended to existing", "node_modules/\n", true, "node_modules/\n.multiverso/\n"},
		{"newline added first when missing", "node_modules/", true, "node_modules/\n.multiverso/\n"},
		{"not duplicated", "vendor/\n.multiverso/\n", true, "vendor/\n.multiverso/\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".gitignore")
			if tt.create {
				if err := os.WriteFile(path, []byte(tt.existing), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			mustInit(t, root)
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read .gitignore: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf(".gitignore = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenRoundTrip(t *testing.T) {
	root := t.TempDir()
	ws := mustInit(t, root)
	cfg := ws.Config
	if err := ws.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })
	if !reflect.DeepEqual(reopened.Config, cfg) {
		t.Errorf("reopened config = %+v, want %+v", reopened.Config, cfg)
	}
	if _, err := reopened.GetObject(cfg.DefaultPolicy); err != nil {
		t.Errorf("GetObject after reopen: %v", err)
	}
	if err := reopened.Ledger.VerifyChain(); err != nil {
		t.Errorf("VerifyChain after reopen: %v", err)
	}
	if got, want := reopened.WorldsDir(), filepath.Join(root, DirName, "worlds"); got != want {
		t.Errorf("WorldsDir = %q, want %q", got, want)
	}
	if got, want := reopened.AdmitDir(), filepath.Join(root, DirName, "admit"); got != want {
		t.Errorf("AdmitDir = %q, want %q", got, want)
	}
	if got, want := reopened.KeysDir(), filepath.Join(root, DirName, "keys"); got != want {
		t.Errorf("KeysDir = %q, want %q", got, want)
	}
}

func TestOpenUninitialized(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("Open on uninitialized root: want error, got nil")
	}
}

func TestGetObjectBadDigest(t *testing.T) {
	ws := mustInit(t, t.TempDir())
	if _, err := ws.GetObject("sha256:deadbeef"); err == nil {
		t.Fatal("GetObject with non-mv0 digest: want error, got nil")
	}
}

// The authoring surface: policies live in one directory named after their
// stem, and switching the default rewrites config.json canonically without
// touching a single content-addressed byte (M1e, CP-5).
func TestPoliciesDirAndSetDefaultPolicy(t *testing.T) {
	root := t.TempDir()
	ws := mustInit(t, root)

	if got, want := ws.PoliciesDir(), filepath.Join(root, DirName, "policies"); got != want {
		t.Errorf("PoliciesDir = %q, want %q", got, want)
	}
	if got, want := ws.PolicyFile("mine"), filepath.Join(root, DirName, "policies", "mine.json"); got != want {
		t.Errorf("PolicyFile = %q, want %q", got, want)
	}

	before := ws.Config.DefaultPolicy
	next := "mv0:" + strings.Repeat("7", 64)
	if err := ws.SetDefaultPolicy(next); err != nil {
		t.Fatalf("SetDefaultPolicy: %v", err)
	}
	if ws.Config.DefaultPolicy != next {
		t.Errorf("in-memory default = %q, want %q", ws.Config.DefaultPolicy, next)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer reopened.Close()
	if reopened.Config.DefaultPolicy != next {
		t.Errorf("persisted default = %q, want %q", reopened.Config.DefaultPolicy, next)
	}
	// The previously recorded policy is untouched and still resolvable:
	// intents pinned to it keep replaying against it forever.
	if _, err := reopened.GetObject(before); err != nil {
		t.Errorf("previous default no longer in CAS: %v", err)
	}
	// config.json stays canonical.
	b, err := os.ReadFile(filepath.Join(root, DirName, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	canon, err := object.Canonical(reopened.Config)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(canon) {
		t.Errorf("config.json = %s, want canonical %s", b, canon)
	}
}
