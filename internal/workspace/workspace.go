// Package workspace manages the .multiverso directory: layout, config,
// init, and open (see docs/design/M0.md "Workspace").
package workspace

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/signing"
)

// SchemaConfig identifies .multiverso/config.json.
const SchemaConfig = "multiverso.dev/config/v0"

// DirName is the workspace directory created inside a repo.
const DirName = ".multiverso"

// ignoreLine keeps the workspace — and the unencrypted signing key inside
// it — out of the repo's history.
const ignoreLine = ".multiverso/"

// IgnoreResult records where Init put the rule that keeps .multiverso/ out
// of git, so `mvo init` can say so out loud.
//
// The rule belongs in .git/info/exclude, not in the tracked .gitignore:
// exclude is untracked, so it survives `git reset --hard` and `git checkout
// -- .`, and writing it leaves the working tree CLEAN. Editing .gitignore
// instead did the opposite on both counts — it dirtied the tree at init,
// and the documented remedy for the resulting "working tree lags" warning
// (`git reset --hard`) reverted mvo's own line, after which the next `git
// add -A` committed .multiverso/keys/local.key: the unencrypted ed25519
// private key that signs every attestation in the workspace.
type IgnoreResult struct {
	Path     string // file the rule was written to, or already present in
	Existed  bool   // the rule was already there; nothing was written
	Fallback bool   // .git/info/exclude was unusable, .gitignore was used
	Reason   string // why the fallback happened; "" unless Fallback
}

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
	// Ignore is where Init wrote the git ignore rule. Set by Init only;
	// Open leaves it zero. Render-only: nothing decides on it.
	Ignore IgnoreResult
}

// DefaultPolicy is the policy `mvo init` writes: since M1e the v1 artifact
// that names its own oracles (policy.Default(), M1e decision 19) — the Python
// ladder, ordered so a test-deleting candidate is stopped by O0's counts
// before its suite is ever run. The M0 v0 shape stays loadable, inspectable
// and deliberately pinnable per intent, but never the workspace default: a
// shape whose gate its own digest does not determine must not silently judge
// everything created afterwards.
func DefaultPolicy() object.PolicyV1 { return policy.Default() }

// Init creates <root>/.multiverso/{ledger.db,cas/,config.json,
// policies/default.json,keys/}, stores the default policy in CAS and
// records it in the ledger, generates the local signing keypair (M1a), and
// git-ignores the workspace. Fresh-workspace event order is deterministic:
// policy.created, then key.generated. It refuses to re-init, and removes
// the partial directory if initialization fails partway.
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
	ign, err := ensureIgnored(root)
	if err != nil {
		return nil, err
	}
	ws = &Workspace{Root: root, Dir: dir, Config: cfg, Ledger: led, CAS: store, Ignore: ign}
	if _, err = ws.GenerateKeys(); err != nil {
		return nil, err
	}
	return ws, nil
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

// AdmitDir is the parent directory for landing worktrees.
func (w *Workspace) AdmitDir() string { return filepath.Join(w.Dir, "admit") }

// KeysDir is where the local signing keypair lives — inside the
// git-ignored workspace, never anywhere else.
func (w *Workspace) KeysDir() string { return filepath.Join(w.Dir, "keys") }

// PoliciesDir holds the workspace's file-backed policies, one JSON document
// per name (`mvo policy use <name>` reads <name>.json). Files are authoring
// surface only: what a race is judged by is the digest an intent pinned.
func (w *Workspace) PoliciesDir() string { return filepath.Join(w.Dir, "policies") }

// PolicyFile is the path of the file-backed policy called name.
func (w *Workspace) PolicyFile(name string) string {
	return filepath.Join(w.PoliciesDir(), name+".json")
}

// SetDefaultPolicy points config.default_policy at dig and rewrites
// config.json canonically. Nothing is mutated in place: a policy's bytes
// are content-addressed, so switching the default mints no new object and
// intents pinned to the old digest keep replaying against it.
func (w *Workspace) SetDefaultPolicy(dig string) error {
	cfg := w.Config
	cfg.DefaultPolicy = dig
	canon, err := object.Canonical(cfg)
	if err != nil {
		return fmt.Errorf("workspace: encode config: %w", err)
	}
	path := filepath.Join(w.Dir, "config.json")
	if err := os.WriteFile(path, canon, 0o644); err != nil {
		return fmt.Errorf("workspace: write %s: %w", path, err)
	}
	w.Config = cfg
	return nil
}

// GenerateKeys generates the local keypair into KeysDir (refusing to
// overwrite an existing one) and records key.generated in the ledger.
func (w *Workspace) GenerateKeys() (*signing.Signer, error) {
	s, err := signing.Generate(w.KeysDir())
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	body, err := object.Canonical(map[string]any{
		"key_id":     s.KeyID,
		"public_key": base64.StdEncoding.EncodeToString(s.Public),
	})
	if err != nil {
		return nil, fmt.Errorf("workspace: encode key.generated: %w", err)
	}
	if _, err := w.Ledger.Append("key.generated", body); err != nil {
		return nil, fmt.Errorf("workspace: record key.generated: %w", err)
	}
	return s, nil
}

// Signer loads the workspace signing keypair.
func (w *Workspace) Signer() (*signing.Signer, error) {
	s, err := signing.Load(w.KeysDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("workspace: no signing keys in %s (run `mvo init --keys`): %w", w.KeysDir(), err)
		}
		return nil, fmt.Errorf("workspace: %w", err)
	}
	return s, nil
}

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

// ensureIgnored makes git ignore the workspace, preferring the untracked
// .git/info/exclude over the tracked .gitignore.
//
// Order: an existing rule in either file is honoured and nothing is written
// (a repo that already ignores .multiverso/ in its .gitignore keeps doing
// so). Otherwise the rule goes to .git/info/exclude. Only when that file
// cannot be written — no git dir, read-only .git, a git that will not
// answer rev-parse — does it fall back to .gitignore, and the caller is
// told so it can warn: that path dirties the operator's tree, and a tree
// dirtied by mvo is how the signing key got committed.
func ensureIgnored(root string) (IgnoreResult, error) {
	gitignore := filepath.Join(root, ".gitignore")
	present, err := ignoreLinePresent(gitignore)
	if err != nil {
		return IgnoreResult{}, err
	}
	if present {
		return IgnoreResult{Path: gitignore, Existed: true}, nil
	}

	exclude, excErr := excludePath(root)
	if excErr == nil {
		present, err := ignoreLinePresent(exclude)
		if err != nil {
			return IgnoreResult{}, err
		}
		if present {
			return IgnoreResult{Path: exclude, Existed: true}, nil
		}
		if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
			excErr = err
		} else if err := appendIgnoreLine(exclude); err != nil {
			excErr = err
		} else {
			return IgnoreResult{Path: exclude}, nil
		}
	}

	if err := appendIgnoreLine(gitignore); err != nil {
		return IgnoreResult{}, err
	}
	return IgnoreResult{Path: gitignore, Fallback: true, Reason: excErr.Error()}, nil
}

// excludePath locates <git-common-dir>/info/exclude for root. The common
// dir is asked of git rather than assumed to be "<root>/.git": in a linked
// worktree .git is a file, and exclude lives with the main repository.
func excludePath(root string) (string, error) {
	common, err := gitx.CommonDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(common, "info", "exclude"), nil
}

// ignoreLinePresent reports whether path already carries the rule. A
// missing file is not present and not an error.
func ignoreLinePresent(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("workspace: read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == ignoreLine {
			return true, nil
		}
	}
	return false, nil
}

// appendIgnoreLine appends the rule to path, creating the file if missing
// and inserting the separating newline the previous last line may lack.
func appendIgnoreLine(path string) error {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("workspace: read %s: %w", path, err)
	}
	out := append([]byte(nil), b...)
	if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	out = append(out, ignoreLine+"\n"...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("workspace: write %s: %w", path, err)
	}
	return nil
}
