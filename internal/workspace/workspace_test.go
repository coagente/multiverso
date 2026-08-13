package workspace

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
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
	var pol object.Policy
	if err := json.Unmarshal(got, &pol); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	if !reflect.DeepEqual(pol, DefaultPolicy()) {
		t.Errorf("policy = %+v, want %+v", pol, DefaultPolicy())
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
