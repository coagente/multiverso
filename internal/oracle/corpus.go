package oracle

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// SchemaCorpus is the corpus object's schema. A corpus is materialized ONCE
// PER RACE on the BASE tree, put in CAS, and announced by a
// `corpus.recorded` ledger event — observational, like `baseline.recorded`,
// because a Receipt must bind a World and the base tree is nobody's
// candidate.
//
// Materializing on the base tree is what makes cross-candidate comparison
// possible at all (M2a decision 4), and each of the four reasons is load-
// bearing:
//
//   - IDENTITY. Comparison requires that every world executed the same
//     inputs. A generator run inside each world does not give that:
//     Hypothesis stops early on failure, so a world that fails on case 3
//     never sees cases 4…100, and comparing a 3-case observation with a
//     100-case one is comparing nothing.
//   - TRUST. A corpus a candidate can influence is a corpus a candidate can
//     choose to agree on. Materializing on the base tree fixes the inputs
//     before any candidate exists, from code the candidate did not write.
//   - DETERMINISM. The corpus is bytes in CAS with a digest in the ledger,
//     so `mvo audit` replays a differential decision like any other.
//   - COST. One extra worktree and one short interpreter run per race,
//     amortized over N worlds — the same fixture the M1e collect baseline
//     already opens, at the same tier, image and env.
const SchemaCorpus = "multiverso.dev/corpus/v0"

// CorpusFile is the name the materialized corpus is written under, inside
// the race's control-plane corpus directory. It is NEVER written into a
// world's tree and never mounted during phase A: a generator that can read
// the corpus can special-case it (M2a decision 13, AG-7).
const CorpusFile = "corpus.json"

// Drop reasons, recorded as counts on the corpus object. A dropped case is
// never silently re-added by a candidate that happens to define the missing
// name: the corpus is immutable once materialized.
const (
	DropNotRepresentable = "not_representable"
	DropTargetUnresolved = "target_unresolved"
)

// CorpusCase is one input: a target, positional arguments and keyword
// arguments, all in mvo-value/v0 encoding.
//
// ID is `c%04d`, assigned in materialization order, and it is the ONLY case
// identity there is. A corpus is immutable, so an id means the same input
// in every world and in every replay — which is exactly what lets a
// fingerprint vector be compared across worlds at all.
type CorpusCase struct {
	Args   []json.RawMessage          `json:"args"`
	ID     string                     `json:"id"`
	Kwargs map[string]json.RawMessage `json:"kwargs"`
	Target string                     `json:"target"` // module:qualname
}

// Corpus is the materialized input set.
type Corpus struct {
	Schema     string           `json:"schema"`
	Provider   string           `json:"provider"`
	Provenance string           `json:"provenance"` // the file, the node-id source, or the model
	BaseTree   string           `json:"base_tree"`
	Seed       string           `json:"seed"` // 32 hex for hypothesis, "" otherwise
	Cases      []CorpusCase     `json:"cases"`
	Dropped    map[string]int64 `json:"dropped"`
}

// IDs returns the declared case ids in materialization order.
func (c Corpus) IDs() []string {
	out := make([]string, 0, len(c.Cases))
	for _, cs := range c.Cases {
		out = append(out, cs.ID)
	}
	return out
}

// Declares reports whether id is one of the corpus's own cases. A world
// that reports on a case the corpus does not contain has told us it is not
// running our corpus (corpus vector 17), and that is what makes the whole
// observation unusable rather than merely noisy.
func (c Corpus) Declares(id string) bool {
	for _, cs := range c.Cases {
		if cs.ID == id {
			return true
		}
	}
	return false
}

// Case returns the declared case with the given id.
func (c Corpus) Case(id string) (CorpusCase, bool) {
	for _, cs := range c.Cases {
		if cs.ID == id {
			return cs, true
		}
	}
	return CorpusCase{}, false
}

// Digest is the corpus's content address over its canonical bytes.
func (c Corpus) Digest() (string, []byte, error) { return object.Digest(c) }

// declaredFile is the wire shape of a policy-declared corpus file. It is
// authored by a human (or, through the synthesis seam, by a model) and it
// carries NO ids: ids are assigned at materialization, in file order, so
// two races over the same file produce the same identities and a file
// edited between races produces a different corpus rather than a corpus
// whose c0003 quietly means something new.
type declaredFile struct {
	Cases []struct {
		Args   []json.RawMessage          `json:"args"`
		Kwargs map[string]json.RawMessage `json:"kwargs"`
		Target string                     `json:"target"`
	} `json:"cases"`
}

// candidateCase is one case before target resolution: what the control
// plane proposes, which the base-tree runner then accepts or drops.
type candidateCase struct {
	Args   []json.RawMessage
	Kwargs map[string]json.RawMessage
	Target string
}

// ParseDeclaredCorpus reads a policy-declared corpus file into proposed
// cases, applying the cases_max ceiling and refusing malformed input by
// name. Nothing here resolves a target — that needs the base tree and an
// interpreter — so this is pure and testable without Python.
func ParseDeclaredCorpus(raw []byte, casesMax int64) ([]candidateCase, error) {
	var f declaredFile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("corpus file: %w", err)
	}
	out := make([]candidateCase, 0, len(f.Cases))
	for i, c := range f.Cases {
		if casesMax > 0 && int64(len(out)) >= casesMax {
			break
		}
		if err := validTarget(c.Target); err != nil {
			return nil, fmt.Errorf("corpus file: cases[%d].target: %w", i, err)
		}
		for j, a := range c.Args {
			if err := ValidEncoded(a); err != nil {
				return nil, fmt.Errorf("corpus file: cases[%d].args[%d]: %w", i, j, err)
			}
		}
		names := make([]string, 0, len(c.Kwargs))
		for k := range c.Kwargs {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			if err := ValidEncoded(c.Kwargs[k]); err != nil {
				return nil, fmt.Errorf("corpus file: cases[%d].kwargs[%q]: %w", i, k, err)
			}
		}
		args := c.Args
		if args == nil {
			args = []json.RawMessage{}
		}
		kwargs := c.Kwargs
		if kwargs == nil {
			kwargs = map[string]json.RawMessage{}
		}
		out = append(out, candidateCase{Args: args, Kwargs: kwargs, Target: c.Target})
	}
	return out, nil
}

// validTarget checks the `module:qualname` shape the runner resolves with
// importlib. A malformed target is refused at materialization rather than
// counted as a drop: a typo is an authoring bug, and silently dropping it
// would leave an operator staring at a corpus that is mysteriously smaller
// than the file they wrote.
func validTarget(t string) error {
	mod, qual, ok := strings.Cut(t, ":")
	if !ok {
		return fmt.Errorf("%q is not module:qualname", t)
	}
	if mod == "" || qual == "" {
		return fmt.Errorf("%q is not module:qualname", t)
	}
	for _, part := range append(strings.Split(mod, "."), strings.Split(qual, ".")...) {
		if part == "" {
			return fmt.Errorf("%q is not module:qualname", t)
		}
		for _, r := range part {
			if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				continue
			}
			return fmt.Errorf("%q contains %q, which is not a Python identifier character", t, string(r))
		}
	}
	return nil
}

// NodeIDTarget maps a pytest node id onto the `module:qualname` target the
// corpus runner can resolve, for the `repo-suite` provider.
//
// The provider is documented as the honest FLOOR and as NEARLY
// INFORMATION-FREE (M2a decision 5): among candidates that passed the suite
// gate, every world's per-test outcome vector is identical, so the
// partition has one class and the differential truthfully says "no
// divergence". It ships because it costs nothing extra to materialize; it
// is deliberately absent from every fixture policy, because putting it in
// one would be pretending it discriminates.
//
// Node ids that name a parametrization or a class-nested test resolve to no
// callable this runner can invoke with no arguments, so they are DROPPED
// and counted rather than guessed at.
func NodeIDTarget(nodeID string) (string, bool) {
	file, rest, ok := strings.Cut(nodeID, "::")
	if !ok || !strings.HasSuffix(file, ".py") {
		return "", false
	}
	if strings.ContainsAny(rest, "[:") {
		return "", false // parametrized, or nested in a class
	}
	mod := strings.TrimSuffix(file, ".py")
	mod = strings.ReplaceAll(strings.TrimPrefix(mod, "./"), "/", ".")
	target := mod + ":" + rest
	if validTarget(target) != nil {
		return "", false
	}
	return target, true
}

// assignIDs stamps `c%04d` onto resolved cases in materialization order.
func assignIDs(in []candidateCase) []CorpusCase {
	out := make([]CorpusCase, 0, len(in))
	for i, c := range in {
		args := c.Args
		if args == nil {
			args = []json.RawMessage{}
		}
		kwargs := c.Kwargs
		if kwargs == nil {
			kwargs = map[string]json.RawMessage{}
		}
		out = append(out, CorpusCase{
			Args: args, ID: fmt.Sprintf("c%04d", i+1), Kwargs: kwargs, Target: c.Target,
		})
	}
	return out
}

// CorpusProviderSupported reports whether this binary can MATERIALIZE a
// provider, and the sentence to refuse it with when it cannot.
//
// The vocabulary is the document's; the implementation is this binary's,
// and M1f's `isolated` regime set the precedent for saying so out loud
// rather than degrading quietly. `hypothesis` needs the package (which is
// absent on the machine M2a was written on, so decision 20's abort is the
// LIVE path here, not a contingency) and a materializer that belongs with
// the property rung; until both ship, a policy that declares it is refused
// at pre-flight with the ledger untouched — never downgraded to a different
// corpus, which would be a race comparing inputs nobody asked for.
func CorpusProviderSupported(provider string) bool {
	switch provider {
	case policy.ProviderDeclared, policy.ProviderRepoSuite:
		return true
	default:
		return false
	}
}
