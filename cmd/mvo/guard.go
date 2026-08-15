package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/workspace"
)

// cmdGuard is the adoption wedge (M1f): the one verb an evaluating
// maintainer can run before adopting anything. It compares two trees under
// a policy's path sets, prints the violations, and exits 0 clean / 1
// violating.
//
// No ledger writes, no worktree, no race — `mvo guard --base HEAD~50
// --policy default` answers "would this gate have blocked my last fifty
// commits" in one command, and the answer costs two git tree walks and no
// process execution at all.
func cmdGuard(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("guard", stderr)
	dir := fs.String("dir", ".", "repository directory")
	base := fs.String("base", "", "base revision or tree the candidate is measured against")
	tree := fs.String("tree", "", "candidate revision or tree (default: the working tree)")
	polRef := fs.String("policy", "default", "policy name or digest whose path sets are applied")
	jsonOut := fs.Bool("json", false, "emit the tree-guard report")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *base == "" {
		return usagef("guard: --base is required (the tree the candidate is measured against)")
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	defer ws.Close()
	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	resolved, err := resolvePolicy(ws, st, *polRef)
	if err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	if resolved.Pol.Paths.Empty() {
		return fmt.Errorf("guard: policy %s (%s) declares no protected or harness pattern: there is nothing to guard",
			resolved.Digest, resolved.Pol.Name)
	}

	baseTree, err := resolveTreeish(ws.Root, *base)
	if err != nil {
		return fmt.Errorf("guard: --base %s: %w", *base, err)
	}

	// The candidate defaults to the WORKING TREE, snapshotted through a
	// temporary index exactly as the oracle does — the operator's own index
	// is never touched.
	worldDir := ws.Root
	candidateTree := ""
	if *tree != "" {
		if candidateTree, err = resolveTreeish(ws.Root, *tree); err != nil {
			return fmt.Errorf("guard: --tree %s: %w", *tree, err)
		}
		// A named tree is compared by checking it out into a throwaway
		// worktree: the guard compares TREES, and the only way to hand it
		// one that is not the working tree is to materialize it.
		checkout, cleanup, err := tempWorktree(ws.Root, *tree)
		if err != nil {
			return fmt.Errorf("guard: %w", err)
		}
		defer cleanup()
		worldDir = checkout
	}

	// A throwaway CAS: `mvo guard` records nothing, and an adoption wedge
	// that writes to the operator's workspace is not a wedge.
	store, err := cas.Open(filepath.Join(os.TempDir(), "mvo-guard-cas"))
	if err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	o, err := oracle.New(oracle.Params{
		Spec:          guardSpec(resolved.Pol),
		CAS:           store,
		Paths:         resolved.Pol.Paths,
		Repo:          ws.Root,
		BaseTree:      baseTree,
		CandidateTree: candidateTree,
	})
	if err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	rec, err := o.Run(context.Background(), backend.HostDir(worldDir))
	if err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	raw, err := store.Get(rec.Result.Artifacts[0])
	if err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	var report oracle.GuardReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return fmt.Errorf("guard: decode report: %w", err)
	}

	// The blind spot the guard's own strength creates: it compares git
	// TREES, and `git add -A` honours the in-tree .gitignore. pytest does
	// not — it loads a conftest.py that .gitignore names — so a tree the
	// guard calls clean can carry a live harness file. In a race the
	// equivalent is caught (a force-applied file is in the world's index,
	// and the drift between that tree and a fresh snapshot is a
	// tree_drift), but `mvo guard` has no recorded world tree to drift
	// against, so it has to look.
	hidden := hiddenPathSetFiles(worldDir, resolved.Pol)

	if *jsonOut {
		if _, err := fmt.Fprintf(stdout, "%s\n", raw); err != nil {
			return fmt.Errorf("guard: %w", err)
		}
	} else {
		writeGuardHuman(stdout, resolved, report, rec, hidden)
	}
	if rec.Result.Status == oracle.StatusError {
		return fmt.Errorf("guard: %s", rec.Result.Detail)
	}
	if len(report.Violations) > 0 {
		return fmt.Errorf("guard: %d path violation(s) against base %s", len(report.Violations), report.BaseTree)
	}
	if len(hidden) > 0 {
		// A false clean verdict is worse than no verdict: this is the verb
		// a maintainer runs to decide whether to trust the gate at all.
		return fmt.Errorf("guard: %d path-set file(s) present in the working tree but excluded by ignore rules, so no tree comparison can see them: %s",
			len(hidden), strings.Join(hidden, " "))
	}
	return nil
}

// hiddenPathSetFiles lists working-tree files that match the policy's path
// sets and that git's exclude rules keep out of the snapshot. A read
// failure yields nothing: this is an ADDITIONAL warning over the guard's
// own verdict, and turning a git hiccup into a violation would be the
// over-claim in the other direction.
func hiddenPathSetFiles(dir string, pol policy.Policy) []string {
	ignored, err := gitx.IgnoredFiles(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range ignored {
		if pol.Paths.Class(p) != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// writeGuardHuman prints the violation table an operator acts on.
func writeGuardHuman(w io.Writer, resolved resolvedPolicy, report oracle.GuardReport, rec object.Receipt, hidden []string) {
	fmt.Fprintf(w, "policy:    %s (%s)\n", resolved.Digest, resolved.Pol.Name)
	fmt.Fprintf(w, "base:      %s\n", report.BaseTree)
	fmt.Fprintf(w, "candidate: %s\n", report.CandidateTree)
	fmt.Fprintf(w, "examined:  %d path(s) in the protected and harness sets\n",
		rec.Result.Metrics[policy.MetricPathsExamined])
	for _, p := range hidden {
		fmt.Fprintf(w, "IGNORED:   %-20s present in the working tree, excluded by ignore rules — pytest would load it, no tree comparison can see it\n", p)
	}
	if len(report.Violations) == 0 {
		if len(hidden) == 0 {
			fmt.Fprintln(w, "OK: no protected or harness path was modified, deleted or added")
		} else {
			// The comparison itself found nothing, and saying only that
			// would be the false clean verdict. The scope goes in the
			// same sentence as the result.
			fmt.Fprintf(w, "INCOMPLETE: the tree comparison is clean, but %d path-set file(s) are invisible to it\n", len(hidden))
		}
		if len(report.AllowedAdditions) > 0 {
			fmt.Fprintf(w, "     (%d allowed addition(s): %s)\n",
				len(report.AllowedAdditions), strings.Join(report.AllowedAdditions, " "))
		}
		return
	}
	for _, v := range report.Violations {
		fmt.Fprintf(w, "VIOLATION: %-20s %s\n", v.Kind, v.Path)
	}
	fmt.Fprintf(w, "%d violation(s)\n", len(report.Violations))
}

// guardSpec is the tree-guard instance `mvo guard` runs: the policy's own
// when it declares one — so the receipt's identity matches what a race
// would produce — and a synthetic one otherwise, because the path sets are
// the policy's regardless of whether it wired a guard oracle.
func guardSpec(pol policy.Policy) policy.Oracle {
	for _, o := range pol.Oracles {
		if o.Kind == policy.KindTreeGuard {
			return o
		}
	}
	spec := object.OracleSpec{Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{}}
	cfg, err := policy.ConfigDigest(spec)
	if err != nil {
		cfg = pol.Digest // unreachable; never empty, which is what New requires
	}
	return policy.Oracle{
		Name: "guard", Kind: policy.KindTreeGuard,
		Family: policy.FamilyTree, Config: cfg,
		Argv: []string{}, Args: []string{},
	}
}

// resolveTreeish accepts a revision (HEAD, a branch, a sha) or a
// "git:<sha1>" tree digest and returns the tree digest.
func resolveTreeish(repo, ref string) (string, error) {
	if strings.HasPrefix(ref, gitx.TreePrefix) {
		return ref, nil
	}
	return gitx.TreeOf(repo, ref)
}

// tempWorktree checks ref out into a throwaway detached worktree and
// returns it plus its removal.
func tempWorktree(repo, ref string) (string, func(), error) {
	commit, err := gitx.ResolveCommit(repo, ref)
	if err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp("", "mvo-guard-")
	if err != nil {
		return "", func() {}, err
	}
	// git worktree add refuses an existing directory.
	_ = os.Remove(dir)
	if err := gitx.AddWorktree(repo, dir, commit); err != nil {
		return "", func() {}, err
	}
	return dir, func() {
		_ = gitx.RemoveWorktree(repo, dir)
		_ = os.RemoveAll(dir)
	}, nil
}
