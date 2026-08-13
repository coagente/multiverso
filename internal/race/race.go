package race

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
)

const (
	isolationTier   = "T0-worktree"
	producerAdapter = "script@v0" // M0: scripted patches, no agent adapters (AG-1 is M1)
)

// lockfileNames are hashed into the env manifest when present in a world.
var lockfileNames = []string{
	"Cargo.lock", "go.sum", "package-lock.json",
	"poetry.lock", "requirements.txt", "uv.lock",
}

// Config wires one race run. All fields are required except KeepWorlds.
type Config struct {
	Repo       string         // git repo root; worlds are worktrees of it
	Ledger     *ledger.Ledger // event log
	CAS        *cas.Store     // object + artifact store
	Intent     string         // intent digest ("mv0:...") already in CAS
	PatchesDir string         // directory of candidate patch files
	WorldsDir  string         // parent directory for world worktrees
	Oracle     oracle.Oracle  // verifier run in each COMPLETED world
	KeepWorlds bool           // keep worktrees after the race (--keep-worlds)
}

// WorldRun is one world's trajectory through the race.
type WorldRun struct {
	PatchFile     string
	Dir           string
	Digest        string
	World         object.World
	ReceiptDigest string          // "" for CONFIG_ERROR worlds
	Receipt       *object.Receipt // nil for CONFIG_ERROR worlds
}

// Result is what a race produced.
type Result struct {
	Decision       object.Decision
	DecisionDigest string
	Worlds         []WorldRun
}

// Run executes the v0 race (CP-2, CP-3): sequential worlds built from the
// patch files in sorted filename order, one oracle run per COMPLETED
// world, then one recorded decision. Every object is stored in CAS and
// appended to the ledger as it is created.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	var intent object.Intent
	if err := loadObject(cfg.CAS, cfg.Intent, &intent); err != nil {
		return nil, err
	}
	if intent.Schema != object.SchemaIntent {
		return nil, fmt.Errorf("race: %s has schema %q, want %q", cfg.Intent, intent.Schema, object.SchemaIntent)
	}
	policy, err := LoadPolicy(cfg.CAS, intent.Policy)
	if err != nil {
		return nil, err
	}
	// Budget: MaxWallMS bounds the whole race; MaxCandidates is not
	// enforced in M0 (the patch set is scripted). TODO(M1): enforce.
	if intent.Budget.MaxWallMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(intent.Budget.MaxWallMS)*time.Millisecond)
		defer cancel()
	}

	patches, err := listPatches(cfg.PatchesDir)
	if err != nil {
		return nil, err
	}
	// Each race gets its own fresh directory under WorldsDir (unique via
	// MkdirTemp): re-racing an intent is a first-class flow, so world paths
	// must never collide with worktrees left behind by --keep-worlds or a
	// crashed race. Created before race.started so a broken worlds dir
	// cannot leave a dangling half-race in the permanent ledger.
	if err := os.MkdirAll(cfg.WorldsDir, 0o755); err != nil {
		return nil, fmt.Errorf("race: create worlds dir: %w", err)
	}
	raceDir, err := os.MkdirTemp(cfg.WorldsDir, "race-")
	if err != nil {
		return nil, fmt.Errorf("race: create race worlds dir: %w", err)
	}

	if err := appendEvent(cfg.Ledger, "race.started", map[string]any{
		"intent": cfg.Intent, "patches": patches,
	}); err != nil {
		os.Remove(raceDir)
		return nil, err
	}

	// Worlds are removed however Run exits, unless the caller keeps them.
	var worktrees []string
	removed := false
	defer func() {
		if cfg.KeepWorlds || removed {
			return
		}
		for _, dir := range worktrees {
			_ = gitx.RemoveWorktree(cfg.Repo, dir) // best effort on error paths
		}
		_ = os.Remove(raceDir) // fails harmlessly if a world survived
	}()

	runs := make([]WorldRun, 0, len(patches))
	for i, name := range patches {
		patch, err := os.ReadFile(filepath.Join(cfg.PatchesDir, name))
		if err != nil {
			return nil, fmt.Errorf("race: read patch %s: %w", name, err)
		}
		patchKey, err := cfg.CAS.Put(patch)
		if err != nil {
			return nil, fmt.Errorf("race: store patch %s: %w", name, err)
		}
		dir := filepath.Join(raceDir, fmt.Sprintf("%03d-%s", i+1, strings.TrimSuffix(name, filepath.Ext(name))))
		if err := gitx.AddWorktree(cfg.Repo, dir, intent.Base.Commit); err != nil {
			return nil, fmt.Errorf("race: world for %s: %w", name, err)
		}
		worktrees = append(worktrees, dir)

		// CP-3: a patch that does not apply yields a CONFIG_ERROR world
		// with no receipts; the race continues with the base tree recorded.
		outcome, tree := OutcomeCompleted, ""
		if err := gitx.Apply(dir, patch); err != nil {
			outcome, tree = OutcomeConfigError, intent.Base.Tree
		} else if tree, err = gitx.WriteTree(dir); err != nil {
			return nil, fmt.Errorf("race: world for %s: %w", name, err)
		}
		env, err := EnvDigest(cfg.CAS, dir)
		if err != nil {
			return nil, err
		}
		w := object.World{
			Schema:        object.SchemaWorld,
			Intent:        cfg.Intent,
			Tree:          tree,
			Env:           env,
			IsolationTier: isolationTier,
			Producer: object.Producer{
				Adapter:      producerAdapter,
				Model:        "",
				IdentityTier: "claimed",
				Role:         "generator",
			},
			Patch:     patchKey,
			Outcome:   outcome,
			CreatedAt: nowRFC3339(),
		}
		dig, err := recordObject(cfg, "world.created", w)
		if err != nil {
			return nil, err
		}
		runs = append(runs, WorldRun{PatchFile: name, Dir: dir, Digest: dig, World: w})
	}

	worlds := make([]object.World, 0, len(runs))
	receipts := make([]object.Receipt, 0, len(runs))
	for i := range runs {
		worlds = append(worlds, runs[i].World)
		if runs[i].World.Outcome != OutcomeCompleted {
			continue
		}
		rec, err := cfg.Oracle.Run(ctx, runs[i].Dir)
		if err != nil {
			return nil, fmt.Errorf("race: oracle in %s: %w", runs[i].Dir, err)
		}
		// The orchestrator alone knows the world digest and tree the
		// receipt attests to (valid_for.tree = the world's tree digest).
		rec.World = runs[i].Digest
		rec.Freshness.ValidFor = object.ValidFor{Tree: runs[i].World.Tree, Env: runs[i].World.Env}
		dig, err := recordObject(cfg, "receipt.recorded", rec)
		if err != nil {
			return nil, err
		}
		runs[i].Receipt = &rec
		runs[i].ReceiptDigest = dig
		receipts = append(receipts, rec)
	}

	decision := Decide(policy, worlds, receipts)
	decision.CreatedAt = nowRFC3339()
	decisionDig, err := recordObject(cfg, "decision.recorded", decision)
	if err != nil {
		return nil, err
	}
	if err := appendEvent(cfg.Ledger, "race.finished", map[string]any{
		"intent": cfg.Intent, "decision": decisionDig,
	}); err != nil {
		return nil, err
	}

	if !cfg.KeepWorlds {
		removed = true
		for _, dir := range worktrees {
			if err := gitx.RemoveWorktree(cfg.Repo, dir); err != nil {
				return nil, fmt.Errorf("race: cleanup: %w", err)
			}
		}
		if err := os.Remove(raceDir); err != nil {
			return nil, fmt.Errorf("race: cleanup: %w", err)
		}
	}
	return &Result{Decision: decision, DecisionDigest: decisionDig, Worlds: runs}, nil
}

func (cfg Config) validate() error {
	switch {
	case cfg.Repo == "":
		return errors.New("race: config: empty repo")
	case cfg.Ledger == nil:
		return errors.New("race: config: nil ledger")
	case cfg.CAS == nil:
		return errors.New("race: config: nil CAS")
	case cfg.Intent == "":
		return errors.New("race: config: empty intent digest")
	case cfg.PatchesDir == "":
		return errors.New("race: config: empty patches dir")
	case cfg.WorldsDir == "":
		return errors.New("race: config: empty worlds dir")
	case cfg.Oracle == nil:
		return errors.New("race: config: nil oracle")
	}
	return nil
}

// recordObject digests v, stores its canonical bytes in CAS, and appends
// them to the ledger under typ. Returns the object digest.
func recordObject(cfg Config, typ string, v any) (string, error) {
	dig, canon, err := object.Digest(v)
	if err != nil {
		return "", fmt.Errorf("race: digest %s: %w", typ, err)
	}
	if _, err := cfg.CAS.Put(canon); err != nil {
		return "", fmt.Errorf("race: store %s: %w", typ, err)
	}
	if _, err := cfg.Ledger.Append(typ, canon); err != nil {
		return "", fmt.Errorf("race: record %s: %w", typ, err)
	}
	return dig, nil
}

func appendEvent(led *ledger.Ledger, typ string, body map[string]any) error {
	payload, err := object.Canonical(body)
	if err != nil {
		return fmt.Errorf("race: encode %s: %w", typ, err)
	}
	if _, err := led.Append(typ, payload); err != nil {
		return fmt.Errorf("race: record %s: %w", typ, err)
	}
	return nil
}

// LoadPolicy fetches a Policy object from CAS by its digest and validates
// its schema. Race runs and audit replay share it: both must read the same
// policy the same way (NFR-1).
func LoadPolicy(store *cas.Store, dig string) (object.Policy, error) {
	var pol object.Policy
	if err := loadObject(store, dig, &pol); err != nil {
		return pol, err
	}
	if pol.Schema != object.SchemaPolicy {
		return pol, fmt.Errorf("race: policy %s has schema %q, want %q", dig, pol.Schema, object.SchemaPolicy)
	}
	return pol, nil
}

// loadObject fetches an object's canonical bytes from CAS by its "mv0:"
// digest and decodes them into v.
func loadObject(store *cas.Store, dig string, v any) error {
	key, err := object.CASKey(dig)
	if err != nil {
		return fmt.Errorf("race: %w", err)
	}
	b, err := store.Get(key)
	if err != nil {
		return fmt.Errorf("race: load %s: %w", dig, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("race: decode %s: %w", dig, err)
	}
	return nil
}

// listPatches returns the regular, non-hidden files of dir sorted by
// filename — the deterministic world order (CP-2).
func listPatches(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir) // sorted by filename
	if err != nil {
		return nil, fmt.Errorf("race: list patches: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("race: no patch files in %s", dir)
	}
	return names, nil
}

// EnvDigest builds the M0 env manifest for a directory —
// {"go":"none","os":runtime.GOOS} plus sha256 hashes of any recognized
// lockfiles — stores its canonical bytes in CAS, and returns its digest.
// Exported for admit, which records the same manifest for landing trees.
func EnvDigest(store *cas.Store, dir string) (string, error) {
	manifest := map[string]any{"go": "none", "os": runtime.GOOS}
	locks := map[string]any{}
	for _, name := range lockfileNames {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("race: read lockfile %s: %w", name, err)
		}
		sum := sha256.Sum256(b)
		locks[name] = "sha256:" + hex.EncodeToString(sum[:])
	}
	if len(locks) > 0 {
		manifest["lockfiles"] = locks
	}
	dig, canon, err := object.Digest(manifest)
	if err != nil {
		return "", fmt.Errorf("race: digest env manifest: %w", err)
	}
	if _, err := store.Put(canon); err != nil {
		return "", fmt.Errorf("race: store env manifest: %w", err)
	}
	return dig, nil
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
