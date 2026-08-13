package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
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

	// The policy is recorded in the ledger and the chain verifies.
	var events []ledger.Event
	if err := ws.Ledger.Scan(func(e ledger.Event) error {
		events = append(events, e)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(events) != 1 || events[0].Type != "policy.created" {
		t.Fatalf("events = %+v, want one policy.created", events)
	}
	if events[0].PayloadDig != polDig {
		t.Errorf("recorded payload digest = %q, want %q", events[0].PayloadDig, polDig)
	}
	if err := ws.Ledger.VerifyChain(); err != nil {
		t.Errorf("VerifyChain: %v", err)
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
