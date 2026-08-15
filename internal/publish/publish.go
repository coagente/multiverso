package publish

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/signing"
)

// Config wires one publication run. All fields are required except
// IncludeRejected.
type Config struct {
	Repo            string
	Ledger          *ledger.Ledger
	CAS             *cas.Store
	Signer          *signing.Signer
	Intent          string // full digest, already recorded
	Remote          string // default "origin" at the CLI
	IncludeRejected bool
}

// RefTip is one (ref, commit sha) pair.
type RefTip struct{ Ref, Tip string }

// Result is what a publication run did.
type Result struct {
	Short    string
	Pushed   []RefTip // refs actually pushed this run (ref-sorted)
	UpToDate []string // planned refs already correct remotely
	Removed  []string // reconciliation deletions (M1d decision 10)
}

// hookBeforePush, when non-nil, runs between the remote survey and the
// push — the window a concurrent publisher occupies (publish and prune
// share it). Tests use it to prove lease failures surface loudly;
// production never sets it.
var hookBeforePush func()

// Run executes one publication (FI-1): build the deterministic candidate
// and evidence commits, reconcile the local namespace, diff against the
// remote namespace, and push only what differs — each ref as an explicit
// refspec under a compare-and-swap lease (M1d decision 10). Idempotent
// republish is structural: identical content re-mints identical shas, so
// the push plan diffs to zero.
func Run(cfg Config) (*Result, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	// Pre-flight — nothing recorded on failure (M1c decision 18 precedent).
	if _, err := gitx.RemoteURL(cfg.Repo, cfg.Remote); err != nil {
		return nil, fmt.Errorf("publish: remote %q: %w", cfg.Remote, err)
	}
	cl, err := BuildClosure(cfg.Ledger, cfg.CAS, cfg.Signer, cfg.Intent)
	if err != nil {
		return nil, err
	}
	warnPushRefspecs(cfg.Repo, cfg.Remote)

	// Plan: the refs the namespace must contain, ref-sorted. Candidate
	// commits are minted for the winner (all candidates under
	// --include-rejected); the evidence commit always. Both are pure
	// functions of content (epoch timestamps, fixed identity) — decision 2.
	var plan []RefTip
	for _, cand := range cl.Candidates {
		if !cand.Winner && !cfg.IncludeRejected {
			continue
		}
		tip, err := gitx.CommitTreeEpoch(cfg.Repo, cand.World.Tree, cl.Intent.Base.Commit,
			candidateMessage(cand.Ordinal, cl.IntentDig, cand.Dig))
		if err != nil {
			return nil, fmt.Errorf("publish: mint candidate %d: %w", cand.Ordinal, err)
		}
		plan = append(plan, RefTip{Ref: CandRef(cl.Short, cand.Ordinal), Tip: tip})
	}
	evTip, err := evidenceCommit(cfg.Repo, cl)
	if err != nil {
		return nil, err
	}
	plan = append(plan, RefTip{Ref: EvidenceRef(cl.Short), Tip: evTip})
	sort.Slice(plan, func(i, j int) bool { return plan[i].Ref < plan[j].Ref })
	planned := make(map[string]string, len(plan))
	for _, rt := range plan {
		planned[rt.Ref] = rt.Tip
	}

	// Local refs: CAS each planned ref to its planned sha (skip when
	// already correct) and delete namespace refs outside the plan —
	// reconciliation plus GC-pin bookkeeping (decisions 10–11). "namespace
	// ⊆ plan" is the invariant that lets fetch-race treat any unplanned ref
	// as loud tamper evidence.
	for _, rt := range plan {
		cur, err := gitx.RefValue(cfg.Repo, rt.Ref)
		if err != nil {
			return nil, fmt.Errorf("publish: %w", err)
		}
		if cur == rt.Tip {
			continue
		}
		if err := gitx.UpdateRef(cfg.Repo, rt.Ref, rt.Tip, cur); err != nil {
			return nil, fmt.Errorf("publish: %w", err)
		}
	}
	local, err := gitx.ForEachRef(cfg.Repo, Namespace(cl.Short))
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
	for ref, sha := range local {
		if _, ok := planned[ref]; ok {
			continue
		}
		if err := gitx.DeleteRef(cfg.Repo, ref, sha); err != nil {
			return nil, fmt.Errorf("publish: %w", err)
		}
	}

	// Remote survey → push / up-to-date / remove partitions. Each pushed
	// ref carries a lease on its observed remote value (create ⇒
	// expect-absent), so a concurrent publisher surfaces as a lease
	// failure, never a silent clobber.
	remote, err := gitx.LsRemote(cfg.Repo, cfg.Remote, Namespace(cl.Short)+"/*")
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
	var push []RefTip
	upToDate := make([]string, 0, len(plan))
	removed := make([]string, 0)
	var refspecs []string
	leases := make(map[string]string)
	for _, rt := range plan {
		if remote[rt.Ref] == rt.Tip {
			upToDate = append(upToDate, rt.Ref)
			continue
		}
		push = append(push, rt)
		refspecs = append(refspecs, rt.Tip+":"+rt.Ref)
		leases[rt.Ref] = remote[rt.Ref] // "" = expect-absent
	}
	for ref, sha := range remote {
		if _, ok := planned[ref]; ok {
			continue
		}
		removed = append(removed, ref)
		refspecs = append(refspecs, ":"+ref)
		leases[ref] = sha
	}
	sort.Strings(upToDate)
	sort.Strings(removed)
	sort.Strings(refspecs)

	if err := appendEvent(cfg.Ledger, "publish.started", map[string]any{
		"include_rejected": cfg.IncludeRejected,
		"intent":           cfg.Intent,
		"refs":             refTipsAny(plan),
		"remote":           cfg.Remote,
		"select_decision":  cl.SelectDig,
	}); err != nil {
		return nil, err
	}

	// Single push of every update + delete refspec; nothing to push means
	// the push is skipped entirely (the idempotent no-op).
	var pushErr error
	if len(refspecs) > 0 {
		if hookBeforePush != nil {
			hookBeforePush()
		}
		pushErr = gitx.Push(cfg.Repo, cfg.Remote, refspecs, leases)
	}

	finished := map[string]any{
		"error":      "",
		"intent":     cfg.Intent,
		"pushed":     refTipsAny(push),
		"remote":     cfg.Remote,
		"removed":    strsAny(removed),
		"up_to_date": strsAny(upToDate),
	}
	if pushErr != nil {
		// The push is atomic (gitx.Push), so a failure really did land
		// nothing: the event claims no pushes and no removals, and the
		// remote agrees.
		finished["error"] = pushErr.Error()
		finished["pushed"] = []any{}
		finished["removed"] = []any{}
	}
	if err := appendEvent(cfg.Ledger, "publish.finished", finished); err != nil {
		return nil, err
	}
	if pushErr != nil {
		return nil, fmt.Errorf("publish: push to %s failed (a concurrent publisher may have moved the namespace — re-run `mvo publish`): %w",
			cfg.Remote, pushErr)
	}

	if push == nil {
		push = []RefTip{}
	}
	return &Result{Short: cl.Short, Pushed: push, UpToDate: upToDate, Removed: removed}, nil
}

// candidateMessage is the byte-exact candidate commit message (M1d "Ref
// layout"; trailers contiguous).
func candidateMessage(ordinal int, intentDig, worldDig string) string {
	return fmt.Sprintf("multiverso: candidate %d for intent %s\n\n"+
		"Multiverso-Schema: multiverso.dev/candidate-ref/v0\n"+
		"Multiverso-Intent: %s\n"+
		"Multiverso-World: %s\n"+
		"Multiverso-Ordinal: %d\n",
		ordinal, intentDig, intentDig, worldDig, ordinal)
}

// evidenceMessage is the byte-exact evidence commit message.
func evidenceMessage(intentDig, selectDig string) string {
	return fmt.Sprintf("multiverso: evidence for intent %s\n\n"+
		"Multiverso-Schema: multiverso.dev/evidence-ref/v0\n"+
		"Multiverso-Intent: %s\n"+
		"Multiverso-Decision: %s\n",
		intentDig, intentDig, selectDig)
}

// evidenceCommit writes the closure's items as blobs, builds the two-level
// evidence tree (subdirectories by kind, decision 4), and mints the
// deterministic evidence commit — one commit per intent, rebuilt from
// content, never chained (decision 3).
func evidenceCommit(repo string, cl *Closure) (string, error) {
	byDir := map[string][]gitx.TreeEntry{}
	for _, it := range cl.Items {
		dir, file, ok := strings.Cut(it.Path, "/")
		if !ok {
			return "", fmt.Errorf("publish: item path %q has no kind directory", it.Path)
		}
		sha, err := gitx.HashObject(repo, it.Bytes)
		if err != nil {
			return "", fmt.Errorf("publish: %w", err)
		}
		byDir[dir] = append(byDir[dir], gitx.TreeEntry{Mode: "100644", Type: "blob", SHA: sha, Name: file})
	}
	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	var rootEntries []gitx.TreeEntry
	for _, dir := range dirs {
		entries := byDir[dir]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		sha, err := gitx.Mktree(repo, entries)
		if err != nil {
			return "", fmt.Errorf("publish: %w", err)
		}
		rootEntries = append(rootEntries, gitx.TreeEntry{Mode: "040000", Type: "tree", SHA: sha, Name: dir})
	}
	rootSha, err := gitx.Mktree(repo, rootEntries)
	if err != nil {
		return "", fmt.Errorf("publish: %w", err)
	}
	tip, err := gitx.CommitTreeEpoch(repo, rootSha, cl.Intent.Base.Commit,
		evidenceMessage(cl.IntentDig, cl.SelectDig))
	if err != nil {
		return "", fmt.Errorf("publish: mint evidence commit: %w", err)
	}
	return tip, nil
}

// warnPushRefspecs warns (stderr, never fatal) when an operator-configured
// remote.<r>.push refspec is broad enough to sweep refs/multiverso into
// default pushes — the one real namespace-hygiene hazard (decision 17).
func warnPushRefspecs(repo, remote string) {
	specs, err := gitx.RemotePushRefspecs(repo, remote)
	if err != nil {
		return // a config read failure is not a publish failure
	}
	for _, spec := range specs {
		if refspecCoversNamespace(spec) {
			fmt.Fprintf(os.Stderr, "mvo: publish: warning: remote.%s.push refspec %q covers refs/multiverso — default `git push` would sweep the namespace\n",
				remote, spec)
		}
	}
}

// refspecCoversNamespace reports whether a push refspec's source pattern
// could match refs under refs/multiverso.
func refspecCoversNamespace(spec string) bool {
	src, _, _ := strings.Cut(strings.TrimPrefix(spec, "+"), ":")
	prefix, _, _ := strings.Cut(src, "*")
	const ns = "refs/multiverso/"
	return strings.HasPrefix(ns, prefix) || strings.HasPrefix(prefix, ns)
}

func (cfg Config) validate() error {
	switch {
	case cfg.Repo == "":
		return errors.New("publish: config: empty repo")
	case cfg.Ledger == nil:
		return errors.New("publish: config: nil ledger")
	case cfg.CAS == nil:
		return errors.New("publish: config: nil CAS")
	case cfg.Signer == nil:
		return errors.New("publish: config: nil signer")
	case cfg.Intent == "":
		return errors.New("publish: config: empty intent digest")
	case cfg.Remote == "":
		return errors.New("publish: config: empty remote")
	}
	return nil
}

// refTipsAny renders RefTips for a canonical-JSON event body.
func refTipsAny(rts []RefTip) []any {
	out := make([]any, 0, len(rts))
	for _, rt := range rts {
		out = append(out, map[string]any{"ref": rt.Ref, "tip": rt.Tip})
	}
	return out
}

// strsAny renders a string slice for a canonical-JSON event body (always
// an array, never null).
func strsAny(ss []string) []any {
	out := make([]any, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

// appendEvent records one observational event as canonical JSON.
func appendEvent(led *ledger.Ledger, typ string, body map[string]any) error {
	payload, err := object.Canonical(body)
	if err != nil {
		return fmt.Errorf("publish: encode %s: %w", typ, err)
	}
	if _, err := led.Append(typ, payload); err != nil {
		return fmt.Errorf("publish: record %s: %w", typ, err)
	}
	return nil
}
