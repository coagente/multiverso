package oracle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// SchemaTreeGuardReport is the guard's report artifact schema. The report
// is the one artifact in the system whose bytes no candidate influenced.
const SchemaTreeGuardReport = "multiverso.dev/tree-guard-report/v0"

// artTreeGuard names the guard's single artifact kind.
const artTreeGuard = "tree-guard-report"

// Violation classes, as the report spells them.
const (
	VProtectedModified = "protected_modified"
	VProtectedDeleted  = "protected_deleted"
	VProtectedAdded    = "protected_added"
	VHarnessModified   = "harness_modified"
	VHarnessDeleted    = "harness_deleted"
	VHarnessAdded      = "harness_added"
	// VTreeDrift is how "an earlier oracle wrote into the tree" or
	// "something raced us" surfaces as a FAILURE rather than as a quiet
	// difference: the tree the guard measured is not the tree the world
	// was recorded with.
	VTreeDrift = "tree_drift"
)

// Violation is one offending path.
type Violation struct {
	BaseBlob      string `json:"base_blob"`
	CandidateBlob string `json:"candidate_blob"`
	Kind          string `json:"kind"`
	Path          string `json:"path"`
}

// GuardReport is the control-plane-authored report artifact.
type GuardReport struct {
	Schema           string      `json:"schema"`
	BaseTree         string      `json:"base_tree"`
	CandidateTree    string      `json:"candidate_tree"`
	AllowedAdditions []string    `json:"allowed_additions"`
	Violations       []Violation `json:"violations"`
}

// guardOracle is the tree-guard: two git tree walks, one comparison, and
// NO process execution in the world at all (M1f decision 5).
//
// This is the single change that closes three of the study's five vectors
// in one move, it costs two `git ls-tree` walks, and it is the one gate
// whose evidence the adversary provably cannot author: the candidate chose
// the content, but it cannot change what the content IS.
//
// Counting tests cannot do this job. @pytest.mark.skip, a weakened
// assertion and a rewritten expected value all leave collected_delta == 0,
// which is precisely how the study's cheats passed.
type guardOracle struct {
	spec  policy.Oracle
	store artifactStore
	paths policy.PathSet
	// repo is the git repository the trees are read from; base is the
	// denominator (M1f decision 19: intent.base.tree in a race, the
	// pre-apply trunk tree at admission); tree is the world's recorded
	// tree digest, which the re-derived candidate tree must equal.
	repo string
	base string
	tree string
}

// ID implements Oracle.
func (o *guardOracle) ID() string { return KindTreeGuard }

// Version implements Oracle.
func (o *guardOracle) Version() string { return oracleVersion }

// Run implements Oracle. The verdict is result.status: pass (no
// violations), fail (violations), error (a tree could not be read, or the
// snapshot failed). execution.argv is [] and execution.exit_code is 0 in
// every case — the guard produces no process, so its exit code is not a
// verdict source and rule S2 does not apply to it.
func (o *guardOracle) Run(_ context.Context, w backend.World) (object.Receipt, error) {
	switch {
	case o.store == nil:
		return object.Receipt{}, fmt.Errorf("oracle: %s: nil CAS store", KindTreeGuard)
	case w == nil:
		return object.Receipt{}, fmt.Errorf("oracle: %s: nil world", KindTreeGuard)
	case o.spec.Config == "":
		return object.Receipt{}, fmt.Errorf("oracle: %s: spec carries no resolved config digest", KindTreeGuard)
	case o.repo == "":
		return object.Receipt{}, fmt.Errorf("oracle: %s: no repository to read trees from", KindTreeGuard)
	}

	report := GuardReport{
		Schema:           SchemaTreeGuardReport,
		BaseTree:         o.base,
		CandidateTree:    o.tree,
		AllowedAdditions: []string{},
		Violations:       []Violation{},
	}
	metrics := map[string]int64{}
	status := StatusPass
	detail := ""

	counts, allowed, viol, drift, err := o.compare(w.Dir(), &report)
	switch {
	case err != nil:
		// A tree that cannot be read yields NO metrics: the gate then
		// fails on "paths_examined absent (source unavailable)" rather
		// than on a fabricated zero, and the world escalates as machinery.
		status = StatusError
		detail = err.Error()
	case drift != "":
		// Tree drift is MACHINERY, not a path violation: the contract
		// describes it as "an earlier oracle wrote into the tree, or
		// something raced us", and the guard measured a different world
		// than the one every receipt binds to. Borrowing protected_modified
		// for it would put a false statement about the candidate's diff
		// into a content-addressed receipt and into the signed rationale —
		// an honest victim of cross-world sabotage convicted of editing a
		// file it never touched. Metrics stay ABSENT (the same shape as an
		// unreadable tree), so the gate fails on
		// "paths_examined absent (source unavailable)" and the world
		// escalates as machinery. The violation list still carries the
		// drift, so the report names both trees.
		report.AllowedAdditions = allowed
		report.Violations = viol
		status = StatusError
		detail = drift
	default:
		report.AllowedAdditions = allowed
		report.Violations = viol
		for name, v := range counts {
			metrics[name] = v
		}
		if len(viol) > 0 {
			status = StatusFail
			// The contract says "(first: %s) names the lexicographically
			// first offending path". `compare` sorts by (kind, path) for
			// the report artifact, so viol[0] is the first by KIND — a
			// different path whenever the two orders disagree.
			detail = GuardReport{Violations: viol}.FirstOffender()
		}
	}

	body, err := object.Canonical(report)
	if err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: %s: encode report: %w", KindTreeGuard, err)
	}
	key, err := o.store.Put(body)
	if err != nil {
		return object.Receipt{}, fmt.Errorf("oracle: %s: store %s: %w", KindTreeGuard, artTreeGuard, err)
	}

	return object.Receipt{
		Schema: object.SchemaReceipt,
		Oracle: object.OracleRef{ID: KindTreeGuard, Version: o.Version(), Config: o.spec.Config},
		Execution: object.Execution{
			Argv:          []string{},
			ExitCode:      0,
			DurationMS:    0,
			IsolationTier: w.Tier(),
			IsolationCaps: w.Caps(),
			// No candidate code executes at all, in any tier: this is the
			// strongest regime the system has, and the receipt says so.
			EvidenceRegime: object.RegimeControlPlane,
			EvidencePlugin: "",
		},
		Result: object.Result{
			Status:  status,
			Metrics: metrics,
			// The guard parses no tool output, so it names no structured
			// source: {} is "measured nothing through a tool", which is
			// the honest record for an oracle that read git trees.
			Tools:     map[string]string{},
			Detail:    detail,
			Artifacts: []string{key},
		},
		Freshness:   object.Freshness{Basis: object.BasisConstruction},
		RecheckTier: recheckTier,
		Family:      policy.FamilyTree,
		Cost: object.Cost{
			WallMS: 0,
			// The guard scales by PATHS considered, not by time: two
			// `git ls-tree` walks over a 40-path repo and over a
			// 40 000-path repo are the same wall_ms on this fixture and
			// nothing alike on a real one (M2a decision 22).
			Units: metrics[policy.MetricPathsExamined],
			Unit:  policy.UnitPaths,
		},
		Inputs:      object.NoInputs(),
		Correlation: policy.KindCorrelation(KindTreeGuard),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// compare walks the two trees and classifies every considered path.
// Deletion counts as modification of the seal; a NEW file matching the
// harness set is a violation whatever else is true.
func (o *guardOracle) compare(worldDir string, report *GuardReport) (map[string]int64, []string, []Violation, string, error) {
	// Tree digests are TreePrefix-ed in objects ("git:<sha1>"); git itself
	// wants the bare sha.
	baseEntries, err := gitx.LsTreeRecursive(o.repo, strings.TrimPrefix(o.base, gitx.TreePrefix))
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("read base tree %s: %w", o.base, err)
	}
	// The candidate tree is re-derived from the worktree through a
	// TEMPORARY index, so the operator's index is never touched and
	// .gitignore semantics match exactly what AG-4's patch capture sees.
	candidate, err := gitx.WriteTreeTemp(worldDir)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("snapshot candidate tree: %w", err)
	}
	report.CandidateTree = candidate
	candEntries, err := gitx.LsTreeRecursive(o.repo, strings.TrimPrefix(candidate, gitx.TreePrefix))
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("read candidate tree %s: %w", candidate, err)
	}

	counts := map[string]int64{
		policy.MetricProtectedModified: 0,
		policy.MetricProtectedDeleted:  0,
		policy.MetricProtectedAdded:    0,
		policy.MetricHarnessModified:   0,
		policy.MetricHarnessDeleted:    0,
		policy.MetricHarnessAdded:      0,
		policy.MetricPathsExamined:     0,
	}
	var violations []Violation
	allowed := []string{}

	type entry struct{ mode, sha string }
	baseByPath := make(map[string]entry, len(baseEntries))
	for _, e := range baseEntries {
		baseByPath[e.Name] = entry{e.Mode, e.SHA}
	}
	candByPath := make(map[string]entry, len(candEntries))
	for _, e := range candEntries {
		candByPath[e.Name] = entry{e.Mode, e.SHA}
	}

	add := func(kind, path, baseBlob, candBlob string, metric string) {
		counts[metric]++
		violations = append(violations, Violation{
			BaseBlob: baseBlob, CandidateBlob: candBlob, Kind: kind, Path: path,
		})
	}

	for path, b := range baseByPath {
		class := o.paths.Class(path)
		if class == "" {
			continue
		}
		counts[policy.MetricPathsExamined]++
		c, present := candByPath[path]
		switch {
		case !present:
			if class == policy.ClassHarness {
				add(VHarnessDeleted, path, b.sha, "", policy.MetricHarnessDeleted)
			} else {
				add(VProtectedDeleted, path, b.sha, "", policy.MetricProtectedDeleted)
			}
		case c.sha != b.sha || c.mode != b.mode:
			// Mode-only changes count: a test file turned executable, or a
			// symlink swapped for a regular file, is a modification of the
			// thing that was sealed.
			if class == policy.ClassHarness {
				add(VHarnessModified, path, b.sha, c.sha, policy.MetricHarnessModified)
			} else {
				add(VProtectedModified, path, b.sha, c.sha, policy.MetricProtectedModified)
			}
		}
	}
	for path, c := range candByPath {
		if _, inBase := baseByPath[path]; inBase {
			continue
		}
		class := o.paths.Class(path)
		if class == "" {
			continue
		}
		counts[policy.MetricPathsExamined]++
		if class == policy.ClassHarness {
			// ALWAYS a violation. The study's forgery patch's entire
			// content was a new conftest.py.
			add(VHarnessAdded, path, "", c.sha, policy.MetricHarnessAdded)
			continue
		}
		counts[policy.MetricProtectedAdded]++
		if o.paths.ProtectedAdditions == policy.AdditionsRefuse {
			violations = append(violations, Violation{
				BaseBlob: "", CandidateBlob: c.sha, Kind: VProtectedAdded, Path: path,
			})
			continue
		}
		allowed = append(allowed, path)
	}

	// The recorded world tree is the tree every receipt binds to; a guard
	// that measured a different one measured a different world. That is
	// machinery — reported through `drift`, never through a path metric.
	drift := ""
	if o.tree != "" && candidate != o.tree {
		violations = append(violations, Violation{
			BaseBlob: o.tree, CandidateBlob: candidate, Kind: VTreeDrift, Path: "",
		})
		drift = fmt.Sprintf(
			"tree drift: the world records tree %s, the worktree now snapshots to %s (an earlier oracle wrote into the tree, or something raced us)",
			o.tree, candidate)
	}

	sort.Strings(allowed)
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Kind != violations[j].Kind {
			return violations[i].Kind < violations[j].Kind
		}
		return violations[i].Path < violations[j].Path
	})
	return counts, allowed, violations, drift, nil
}

// FirstOffender returns the lexicographically first offending path of a
// report — the value result.detail carries into the gate's fail reason.
// Violations that name no path (tree_drift) are skipped: the contract's
// "(first: %s)" is a path an operator can open, and "" renders as "-".
func (r GuardReport) FirstOffender() string {
	best := ""
	for _, v := range r.Violations {
		if v.Path == "" {
			continue
		}
		if best == "" || v.Path < best {
			best = v.Path
		}
	}
	return best
}

// GuardWorld wraps a bare directory as the guard's world when no execution
// backend is involved at all — `mvo guard`, the adoption wedge, which
// writes no ledger and opens no container.
func GuardWorld(dir string) backend.World { return backend.HostDir(dir) }

// ensureDir creates a control-plane-owned directory with the exact mode,
// defeating the process umask (0755 would otherwise become 0750+).
func ensureDir(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func joinRel(dir, rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }
