package oracle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// MaterializeParams is phase 0's input: everything needed to turn a
// policy's corpus declaration into a pinned, content-addressed corpus
// object on the BASE tree.
type MaterializeParams struct {
	Spec     policy.Oracle // the declared corpus-observe instance
	BaseTree string        // "git:…" — recorded on the corpus object
	// Dir is the control-plane corpus directory: <raceDir>/corpus/. The
	// plan and the corpus are written here and NEVER inside a worktree.
	Dir string
	// Runner is the in-world path of mvo_corpus.py; WorldRoot is the
	// in-world worktree root.
	Runner    string
	WorldRoot string
	// NodeIDs is the base tree's own test node-id list, for the repo-suite
	// provider. It comes from the base collect run the race already paid
	// for, so the provider costs no extra process.
	NodeIDs []string
}

// MaterializeCorpus builds the race's corpus on the base tree.
//
// The control plane proposes the cases; the RUNNER, executing on the base
// tree, decides which of them resolve and which arguments the encoding can
// represent. Both halves matter: proposing in Go keeps the case ORDER and
// therefore the case IDS a control-plane fact, and resolving in Python is
// the only way to know that `stats:clamp` names something callable in the
// repository as it exists before any candidate touched it.
//
// Returns the corpus, the runner's raw stdout and stderr (stored as
// artifacts by the caller, EP-7 order), and an error only when the corpus
// could not be produced at all. A corpus of ZERO cases is not returned as
// an error here — the caller aborts the race on it, with the ledger
// untouched, because a differential over an empty corpus produces numbers
// that are all fictions.
func MaterializeCorpus(ctx context.Context, w backend.World, p MaterializeParams) (Corpus, []byte, []byte, error) {
	spec := p.Spec.Corpus
	if !CorpusProviderSupported(spec.Provider) {
		return Corpus{}, nil, nil, fmt.Errorf(
			"corpus provider %q is declared by policy but this binary does not materialize it (it ships %s); a binary that cannot produce the inputs a policy pins must not substitute different ones",
			spec.Provider, strings.Join([]string{policy.ProviderDeclared, policy.ProviderRepoSuite}, ", "))
	}
	proposed, provenance, err := p.propose(w)
	if err != nil {
		return Corpus{}, nil, nil, err
	}

	planBytes, err := json.Marshal(map[string]any{"cases": proposed})
	if err != nil {
		return Corpus{}, nil, nil, fmt.Errorf("corpus: encode plan: %w", err)
	}
	if err := ensureDir(p.Dir, 0o755); err != nil {
		return Corpus{}, nil, nil, fmt.Errorf("corpus: %w", err)
	}
	planHost := filepath.Join(p.Dir, "plan.json")
	if err := os.WriteFile(planHost, planBytes, 0o644); err != nil {
		return Corpus{}, nil, nil, fmt.Errorf("corpus: write plan: %w", err)
	}
	defer func() { _ = os.Remove(planHost) }()

	python := policy.DefaultPytestPrefix()[0]
	if len(p.Spec.Argv) > 0 && p.Spec.Argv[0] != "" {
		python = p.Spec.Argv[0]
	}
	// The plan is read from the same in-world path the corpus will later
	// be: on T0 the host path IS the in-world path, and on T1 the corpus
	// directory is bind-mounted read-only.
	planInWorld := planHost
	if p.WorldRoot == backend.InWorldRoot {
		planInWorld = backend.InWorldCorpus + "/plan.json"
	}
	argv := []string{python, p.Runner, "--materialize", "--plan", planInWorld, "--root", p.WorldRoot}
	res := runInWorld(ctx, w, argv, materializeEnv(w, p.WorldRoot))
	if res.Status != StatusPass {
		return Corpus{}, res.Stdout, res.Stderr, fmt.Errorf(
			"corpus: materialize on the base tree exited %d; a corpus that cannot be built on the base tree is machinery, not a fact about any candidate",
			res.ExitCode)
	}
	var out struct {
		Cases []struct {
			Args   []json.RawMessage          `json:"args"`
			Kwargs map[string]json.RawMessage `json:"kwargs"`
			Target string                     `json:"target"`
		} `json:"cases"`
		Dropped map[string]int64 `json:"dropped"`
	}
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return Corpus{}, res.Stdout, res.Stderr, fmt.Errorf("corpus: parse materialization output: %w", err)
	}
	resolved := make([]candidateCase, 0, len(out.Cases))
	for _, c := range out.Cases {
		resolved = append(resolved, candidateCase{Args: c.Args, Kwargs: c.Kwargs, Target: c.Target})
	}
	dropped := map[string]int64{DropNotRepresentable: 0, DropTargetUnresolved: 0}
	for k, v := range out.Dropped {
		if _, known := dropped[k]; known {
			dropped[k] = v
		}
	}
	return Corpus{
		Schema:     SchemaCorpus,
		Provider:   spec.Provider,
		Provenance: provenance,
		BaseTree:   p.BaseTree,
		Seed:       spec.Seed,
		Cases:      assignIDs(resolved),
		Dropped:    dropped,
	}, res.Stdout, res.Stderr, nil
}

// propose builds the control-plane's case list, before any target is
// resolved. It is per provider and it is the only place a provider's
// identity shows up on the Go side.
func (p MaterializeParams) propose(w backend.World) ([]map[string]any, string, error) {
	spec := p.Spec.Corpus
	var cases []candidateCase
	provenance := ""
	switch spec.Provider {
	case policy.ProviderDeclared:
		// The file is read from the BASE worktree, where the candidate has
		// not been. It is also harness-frozen (decision 14), so a candidate
		// that edits it in its own world dies at rung O-1 — but the copy
		// that becomes the corpus is this one, read before any candidate
		// existed.
		host := filepath.Join(w.Dir(), filepath.FromSlash(spec.File))
		raw, err := os.ReadFile(host)
		if err != nil {
			return nil, "", fmt.Errorf("corpus: read declared corpus %s: %w", spec.File, err)
		}
		parsed, err := ParseDeclaredCorpus(raw, spec.CasesMax)
		if err != nil {
			return nil, "", fmt.Errorf("corpus: %w", err)
		}
		cases, provenance = parsed, spec.File
	case policy.ProviderRepoSuite:
		for _, id := range p.NodeIDs {
			if spec.CasesMax > 0 && int64(len(cases)) >= spec.CasesMax {
				break
			}
			target, ok := NodeIDTarget(id)
			if !ok {
				continue
			}
			cases = append(cases, candidateCase{
				Args: []json.RawMessage{}, Kwargs: map[string]json.RawMessage{}, Target: target,
			})
		}
		provenance = "the base tree's collected node ids"
	default:
		return nil, "", fmt.Errorf("corpus: provider %q is not materializable here", spec.Provider)
	}
	out := make([]map[string]any, 0, len(cases))
	for _, c := range cases {
		out = append(out, map[string]any{"args": c.Args, "kwargs": c.Kwargs, "target": c.Target})
	}
	return out, provenance, nil
}

// materializeEnv mirrors the replay environment minus the channel:
// materialization writes no stream (it observes nothing about any
// candidate), so it needs only an importable worktree.
func materializeEnv(w backend.World, root string) []string {
	extra := []string{envPyPath + "=" + root}
	if w.Tier() != object.TierT0Worktree {
		return extra
	}
	return append(os.Environ(), envPyPath+"="+prependPath(root, os.Getenv(envPyPath)))
}

// WriteCorpus writes the materialized corpus into the control-plane corpus
// directory, mode 0444, and returns the host path. The bytes are the
// canonical object bytes, so the file on disk and the digest in the ledger
// are the same thing — which is what lets `mvo audit` replay a
// differential decision exactly like any other.
func WriteCorpus(dir string, canonical []byte) (string, error) {
	if err := ensureDir(dir, 0o755); err != nil {
		return "", fmt.Errorf("corpus: %w", err)
	}
	file := filepath.Join(dir, CorpusFile)
	if err := os.WriteFile(file, canonical, 0o644); err != nil {
		return "", fmt.Errorf("corpus: write corpus: %w", err)
	}
	if err := os.Chmod(file, 0o444); err != nil {
		return "", fmt.Errorf("corpus: seal corpus: %w", err)
	}
	return file, nil
}
