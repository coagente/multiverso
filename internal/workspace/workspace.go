// Package workspace manages the .multiverso directory: layout, config,
// init, and open (see docs/design/M0.md "Workspace").
package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
)

// SchemaConfig identifies .multiverso/config.json.
const SchemaConfig = "multiverso.dev/config/v0"

// DirName is the workspace directory created inside a repo.
const DirName = ".multiverso"

// gitignoreLine keeps the workspace out of the repo's history.
const gitignoreLine = ".multiverso/"

// Config is .multiverso/config.json.
type Config struct {
	Schema        string `json:"schema"`
	DefaultPolicy string `json:"default_policy"` // digest of the default Policy object
}

// Workspace is an opened .multiverso directory.
type Workspace struct {
	Root   string // repo root
	Dir    string // <Root>/.multiverso
	Config Config
	Ledger *ledger.Ledger
	CAS    *cas.Store
}

// DefaultPolicy is the M0 policy: suite-pass hard gate; rank by gate_pass,
// then wall_ms ascending.
func DefaultPolicy() object.Policy {
	return object.Policy{
		Schema:    object.SchemaPolicy,
		HardGates: []string{"suite-pass"},
		Ranking:   []string{"gate_pass", "wall_ms_asc"},
	}
}

// Init creates <root>/.multiverso/{ledger.db,cas/,config.json,
// policies/default.json}, stores the default policy in CAS and records it
// in the ledger, and git-ignores the workspace. It refuses to re-init, and
// removes the partial directory if initialization fails partway.
func Init(root string) (ws *Workspace, err error) {
	dir := filepath.Join(root, DirName)
	if _, statErr := os.Stat(dir); statErr == nil {
		return nil, fmt.Errorf("workspace: %s already initialized: %s exists", root, dir)
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("workspace: stat %s: %w", dir, statErr)
	}

	var led *ledger.Ledger
	defer func() {
		if err == nil {
			return
		}
		if led != nil {
			led.Close()
		}
		os.RemoveAll(dir) // dir did not exist before Init; leave no debris
	}()

	if err := os.MkdirAll(filepath.Join(dir, "policies"), 0o755); err != nil {
		return nil, fmt.Errorf("workspace: init %s: %w", dir, err)
	}
	store, err := cas.Open(filepath.Join(dir, "cas"))
	if err != nil {
		return nil, fmt.Errorf("workspace: init: %w", err)
	}
	led, err = ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		return nil, fmt.Errorf("workspace: init: %w", err)
	}

	pol := DefaultPolicy()
	polDig, polCanon, err := object.Digest(pol)
	if err != nil {
		return nil, fmt.Errorf("workspace: digest default policy: %w", err)
	}
	if _, err := store.Put(polCanon); err != nil {
		return nil, fmt.Errorf("workspace: store default policy: %w", err)
	}
	// The default policy is recorded in the ledger at init as
	// policy.created (M0.md "Ledger" event types).
	if _, err := led.Append("policy.created", polCanon); err != nil {
		return nil, fmt.Errorf("workspace: record default policy: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "policies", "default.json"), polCanon, 0o644); err != nil {
		return nil, fmt.Errorf("workspace: write default policy: %w", err)
	}

	cfg := Config{Schema: SchemaConfig, DefaultPolicy: polDig}
	cfgCanon, err := object.Canonical(cfg)
	if err != nil {
		return nil, fmt.Errorf("workspace: encode config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfgCanon, 0o644); err != nil {
		return nil, fmt.Errorf("workspace: write config: %w", err)
	}
	if err := ensureGitignore(root); err != nil {
		return nil, err
	}
	return &Workspace{Root: root, Dir: dir, Config: cfg, Ledger: led, CAS: store}, nil
}

// Open opens an existing workspace at root.
func Open(root string) (*Workspace, error) {
	dir := filepath.Join(root, DirName)
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("workspace: open %s (not initialized? run `mvo init`): %w", root, err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("workspace: decode %s config: %w", root, err)
	}
	if cfg.Schema != SchemaConfig {
		return nil, fmt.Errorf("workspace: %s: config schema %q, want %q", root, cfg.Schema, SchemaConfig)
	}
	store, err := cas.Open(filepath.Join(dir, "cas"))
	if err != nil {
		return nil, fmt.Errorf("workspace: open %s: %w", root, err)
	}
	led, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		return nil, fmt.Errorf("workspace: open %s: %w", root, err)
	}
	return &Workspace{Root: root, Dir: dir, Config: cfg, Ledger: led, CAS: store}, nil
}

// Close releases the underlying ledger handle.
func (w *Workspace) Close() error {
	if err := w.Ledger.Close(); err != nil {
		return fmt.Errorf("workspace: close: %w", err)
	}
	return nil
}

// WorldsDir is where race worktrees live.
func (w *Workspace) WorldsDir() string { return filepath.Join(w.Dir, "worlds") }

// GetObject fetches an object's canonical bytes from CAS by its "mv0:"
// digest.
func (w *Workspace) GetObject(dig string) ([]byte, error) {
	key, err := object.CASKey(dig)
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	b, err := w.CAS.Get(key)
	if err != nil {
		return nil, fmt.Errorf("workspace: get object %s: %w", dig, err)
	}
	return b, nil
}

// ensureGitignore appends gitignoreLine to <root>/.gitignore, creating the
// file if missing and leaving it untouched if the line is already there.
func ensureGitignore(root string) error {
	path := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("workspace: read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == gitignoreLine {
			return nil
		}
	}
	out := append([]byte(nil), b...)
	if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	out = append(out, gitignoreLine+"\n"...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("workspace: write %s: %w", path, err)
	}
	return nil
}
