package publish

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/admit"
	"github.com/coagente/multiverso/internal/attest"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/signing"
)

// gitT runs git in dir with identity and signing pinned, failing the test
// on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := []string{
		"-c", "user.name=mvo-test",
		"-c", "user.email=mvo-test@invalid",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixture is a fabricated control-plane state for publish tests: a temp
// git repo whose object db holds the world trees, a ledger + CAS + signer,
// a recorded race (optionally admitted), and a local bare remote named
// "origin". No network, no real agents, no workspace.
type fixture struct {
	t       *testing.T
	repo    string
	origin  string
	led     *ledger.Ledger
	store   *cas.Store
	casRoot string
	signer  *signing.Signer

	policy object.Policy
	polDig string
	envDig string

	intentDig string
	intent    object.Intent

	races       int
	worldDigs   []string // latest race, ordinal order
	worlds      []object.World
	receiptDigs []string
	receipts    []object.Receipt
	selDig      string
	sel         object.Decision

	admitDig     string
	applyDig     string
	gateDig      string
	attKey       string
	landingWall  int64 // apply + gate wall sum
	budgetDoctor int64 // added to the attested budget (tamper fixture)

	// doctorSelect, when set, rewrites the SELECT decision before it is
	// recorded — the replay-is-load-bearing tamper fixture.
	doctorSelect func(object.Decision) object.Decision
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	repo := t.TempDir()
	gitT(t, repo, "init", "-q", "-b", "main")
	gitT(t, repo, "commit", "-q", "--allow-empty", "-m", "baseline")

	side := t.TempDir()
	led, err := ledger.Open(filepath.Join(side, "ledger.db"))
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	t.Cleanup(func() { led.Close() })
	casRoot := filepath.Join(side, "cas")
	store, err := cas.Open(casRoot)
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	signer, err := signing.Generate(filepath.Join(side, "keys"))
	if err != nil {
		t.Fatalf("signing.Generate: %v", err)
	}

	f := &fixture{t: t, repo: repo, led: led, store: store, casRoot: casRoot, signer: signer}
	f.policy = object.Policy{
		Schema:    object.SchemaPolicy,
		HardGates: []string{"suite-pass"},
		Ranking:   []string{"gate_pass", "wall_ms_asc"},
	}
	f.polDig = f.recordObj("policy.created", f.policy)

	envDig, envCanon, err := object.Digest(map[string]any{"os": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(envCanon); err != nil {
		t.Fatal(err)
	}
	f.envDig = envDig

	f.origin = filepath.Join(t.TempDir(), "origin.git")
	gitT(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", f.origin)
	gitT(t, repo, "remote", "add", "origin", f.origin)
	return f
}

func (f *fixture) recordObj(typ string, v any) string {
	f.t.Helper()
	dig, canon, err := object.Digest(v)
	if err != nil {
		f.t.Fatalf("digest %s: %v", typ, err)
	}
	if _, err := f.store.Put(canon); err != nil {
		f.t.Fatalf("store %s: %v", typ, err)
	}
	if _, err := f.led.Append(typ, canon); err != nil {
		f.t.Fatalf("record %s: %v", typ, err)
	}
	return dig
}

func (f *fixture) appendEvt(typ string, body map[string]any) {
	f.t.Helper()
	payload, err := object.Canonical(body)
	if err != nil {
		f.t.Fatalf("encode %s: %v", typ, err)
	}
	if _, err := f.led.Append(typ, payload); err != nil {
		f.t.Fatalf("append %s: %v", typ, err)
	}
}

func (f *fixture) newIntent(title string) {
	f.t.Helper()
	commit := gitT(f.t, f.repo, "rev-parse", "HEAD")
	tree := gitx.TreePrefix + gitT(f.t, f.repo, "rev-parse", "HEAD^{tree}")
	f.intent = object.Intent{
		Schema:    object.SchemaIntent,
		Base:      object.Base{Commit: commit, Tree: tree},
		Spec:      object.Spec{Title: title, Description: "fixture"},
		Budget:    object.Budget{MaxCandidates: 4, MaxWallMS: 60000},
		Policy:    f.polDig,
		CreatedAt: "2026-08-13T00:00:00Z",
	}
	f.intentDig = f.recordObj("intent.created", f.intent)
}

// worldTree writes a distinct one-file tree into the repo object db and
// returns its TreePrefix-ed sha — the world's code state, publishable by
// commit-tree.
func (f *fixture) worldTree(content string) string {
	f.t.Helper()
	blob, err := gitx.HashObject(f.repo, []byte(content))
	if err != nil {
		f.t.Fatal(err)
	}
	tree, err := gitx.Mktree(f.repo, []gitx.TreeEntry{
		{Mode: "100644", Type: "blob", SHA: blob, Name: "stats.txt"},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return gitx.TreePrefix + tree
}

// race records one race window for the intent: one COMPLETED world per
// entry of pass (its suite receipt status), decided by race.Decide. Wall
// times ascend by ordinal, so the earliest passing world wins.
func (f *fixture) race(pass ...bool) {
	f.t.Helper()
	f.races++
	f.appendEvt("race.started", map[string]any{
		"adapter": "script@v0", "candidates": len(pass), "exec_image_digest": "",
		"exec_tier": object.TierT0Worktree, "intent": f.intentDig, "parallel": 1,
	})
	f.worlds, f.worldDigs = nil, nil
	f.receipts, f.receiptDigs = nil, nil
	for i := range pass {
		tree := f.worldTree(fmt.Sprintf("race %d candidate %d of %s\n", f.races, i+1, f.intentDig))
		ctxKey, err := f.store.Put(fmt.Appendf(nil, "secret prompt %d of race %d", i+1, f.races))
		if err != nil {
			f.t.Fatal(err)
		}
		traceKey, err := f.store.Put(fmt.Appendf(nil, "secret transcript %d of race %d", i+1, f.races))
		if err != nil {
			f.t.Fatal(err)
		}
		patchKey, err := f.store.Put(fmt.Appendf(nil, "patch %d of race %d", i+1, f.races))
		if err != nil {
			f.t.Fatal(err)
		}
		w := object.World{
			Schema:        object.SchemaWorld,
			Intent:        f.intentDig,
			Tree:          tree,
			Env:           f.envDig,
			IsolationTier: object.TierT0Worktree,
			Producer: object.Producer{
				Adapter: "script@v0", IdentityTier: "claimed", Role: "generator",
			},
			Context:   ctxKey,
			Patch:     patchKey,
			Trace:     traceKey,
			Cost:      object.RunCost{WallMS: 100, Source: "none"},
			Outcome:   object.OutcomeCompleted,
			CreatedAt: "2026-08-13T00:00:01Z",
		}
		dig := f.recordObj("world.created", w)
		f.worlds = append(f.worlds, w)
		f.worldDigs = append(f.worldDigs, dig)
	}
	for i, ok := range pass {
		status, exit := "pass", 0
		if !ok {
			status, exit = "fail", 1
		}
		wall := int64(10 * (i + 1))
		r := object.Receipt{
			Schema: object.SchemaReceipt,
			World:  f.worldDigs[i],
			Oracle: object.OracleRef{ID: "command", Version: "v0", Config: f.polDig},
			Execution: object.Execution{
				Argv: []string{"true"}, ExitCode: exit, DurationMS: wall,
				IsolationTier: object.TierT0Worktree, IsolationCaps: object.HostCaps(),
			},
			Result: object.Result{Status: status, Artifacts: []string{}},
			Freshness: object.Freshness{
				Basis:    "construction",
				ValidFor: object.ValidFor{Tree: f.worlds[i].Tree, Env: f.worlds[i].Env},
			},
			RecheckTier: "V1-replayable",
			Family:      "suite",
			Cost:        object.Cost{WallMS: wall},
			CreatedAt:   "2026-08-13T00:00:02Z",
		}
		dig := f.recordObj("receipt.recorded", r)
		f.receipts = append(f.receipts, r)
		f.receiptDigs = append(f.receiptDigs, dig)
	}
	dec := race.Decide(f.policy, f.worlds, f.receipts)
	dec.CreatedAt = "2026-08-13T00:00:03Z"
	if f.doctorSelect != nil {
		dec = f.doctorSelect(dec)
	}
	f.sel = dec
	f.selDig = f.recordObj("decision.recorded", dec)
	f.appendEvt("race.finished", map[string]any{"decision": f.selDig, "intent": f.intentDig})
}

// admitIntent records a landed admission for the latest SELECT winner:
// landing receipts, the ADMIT decision admit.Decide produces, and a signed
// attestation bundle whose budget is the landing wall sum (+ the doctor
// delta for the budget-mismatch tamper fixture).
func (f *fixture) admitIntent() {
	f.t.Helper()
	winnerDig := f.sel.Subject[0]
	var winner object.World
	for i, dig := range f.worldDigs {
		if dig == winnerDig {
			winner = f.worlds[i]
		}
	}
	f.appendEvt("admission.started", map[string]any{
		"intent": f.intentDig, "select_decision": f.selDig,
		"trunk_branch": "main", "trunk_commit": f.intent.Base.Commit,
		"trunk_tree": f.intent.Base.Tree,
	})
	apply := object.Receipt{
		Schema: object.SchemaReceipt,
		World:  winnerDig,
		Oracle: object.OracleRef{ID: admit.OracleIDLandingApply, Version: "v0", Config: f.polDig},
		Execution: object.Execution{
			Argv: []string{"git", "apply", "--index", "-"}, ExitCode: 0, DurationMS: 5,
			IsolationTier: object.TierT0Worktree, IsolationCaps: object.HostCaps(),
		},
		Result: object.Result{Status: "pass", Artifacts: []string{}},
		Freshness: object.Freshness{
			Basis:    "construction",
			ValidFor: object.ValidFor{Tree: f.intent.Base.Tree, Env: f.envDig},
		},
		RecheckTier: "V1-replayable",
		Family:      admit.FamilyLandingApply,
		Cost:        object.Cost{WallMS: 5},
		CreatedAt:   "2026-08-13T00:00:04Z",
	}
	f.applyDig = f.recordObj("receipt.recorded", apply)
	gate := apply
	gate.Oracle = object.OracleRef{ID: "command", Version: "v0", Config: f.polDig}
	gate.Execution.Argv = []string{"true"}
	gate.Execution.DurationMS = 7
	gate.Freshness.ValidFor = object.ValidFor{Tree: winner.Tree, Env: f.envDig}
	gate.Family = "suite"
	gate.Cost = object.Cost{WallMS: 7}
	f.gateDig = f.recordObj("receipt.recorded", gate)
	f.landingWall = 12

	dec := admit.Decide(f.policy, f.intentDig, winnerDig, apply, &gate)
	dec.CreatedAt = "2026-08-13T00:00:05Z"
	if dec.Type != admit.TypeAdmit {
		f.t.Fatalf("fixture admission decided %s: %s", dec.Type, dec.Rationale)
	}
	f.admitDig = f.recordObj("decision.recorded", dec)

	evidence := []string{f.applyDig, f.gateDig}
	sort.Strings(evidence)
	stmt, err := attest.New("main", winner.Tree, attest.Predicate{
		Intent:         f.intentDig,
		World:          winnerDig,
		Decision:       f.admitDig,
		SelectDecision: f.selDig,
		Evidence:       evidence,
		Policy:         f.polDig,
		BudgetConsumed: attest.Budget{WallMS: f.landingWall + f.budgetDoctor},
		ProducerKeyID:  f.signer.KeyID,
		Trunk:          attest.Trunk{Branch: "main", ParentCommit: f.intent.Base.Commit},
	})
	if err != nil {
		f.t.Fatalf("attest.New: %v", err)
	}
	payload, err := object.Canonical(stmt)
	if err != nil {
		f.t.Fatal(err)
	}
	env, err := signing.Sign(f.signer, signing.PayloadTypeInToto, payload)
	if err != nil {
		f.t.Fatal(err)
	}
	bundle, err := object.Canonical(env)
	if err != nil {
		f.t.Fatal(err)
	}
	f.attKey, err = f.store.Put(bundle)
	if err != nil {
		f.t.Fatal(err)
	}
	f.appendEvt("attestation.recorded", map[string]any{
		"bundle": f.attKey, "commit": "", "decision": f.admitDig, "intent": f.intentDig,
		"key_id": f.signer.KeyID, "statement": object.DigestBytes(payload),
		"subject_tree": winner.Tree, "trunk_branch": "main",
	})
	f.appendEvt("admission.finished", map[string]any{
		"attestation": f.attKey, "commit": "", "decision": f.admitDig,
		"error": "", "intent": f.intentDig, "result": "ADMIT",
	})
}

func (f *fixture) short() string {
	f.t.Helper()
	s, err := IntentShort(f.intentDig)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *fixture) buildClosure() *Closure {
	f.t.Helper()
	cl, err := BuildClosure(f.led, f.store, f.signer, f.intentDig)
	if err != nil {
		f.t.Fatalf("BuildClosure: %v", err)
	}
	return cl
}

func (f *fixture) publishCfg(includeRejected bool) Config {
	return Config{
		Repo: f.repo, Ledger: f.led, CAS: f.store, Signer: f.signer,
		Intent: f.intentDig, Remote: "origin", IncludeRejected: includeRejected,
	}
}

func (f *fixture) mustPublish(includeRejected bool) *Result {
	f.t.Helper()
	res, err := Run(f.publishCfg(includeRejected))
	if err != nil {
		f.t.Fatalf("publish.Run: %v", err)
	}
	return res
}

// clone creates a consumer clone of the bare origin (branch-less clones
// are fine: the namespace fetch is what matters).
func (f *fixture) clone() string {
	f.t.Helper()
	dst := filepath.Join(f.t.TempDir(), "consumer")
	gitT(f.t, f.t.TempDir(), "clone", "-q", f.origin, dst)
	return dst
}

func (f *fixture) fetchRace(consumer string) *Report {
	f.t.Helper()
	rep, err := FetchRace(FetchConfig{
		Repo: consumer, Remote: "origin", Short: f.short(), Pub: f.signer.Public,
	})
	if err != nil {
		f.t.Fatalf("FetchRace: %v", err)
	}
	return rep
}

// remoteRefs surveys origin's multiverso namespace.
func (f *fixture) remoteRefs() map[string]string {
	f.t.Helper()
	refs, err := gitx.LsRemote(f.repo, "origin", "refs/multiverso/*")
	if err != nil {
		f.t.Fatal(err)
	}
	return refs
}

// tamperEvidence rewrites the origin's evidence commit through git
// plumbing: mutate edits the path → bytes map (delete via nil), and the
// rebuilt tree is committed with the original parent + message and
// force-set on the evidence ref.
func (f *fixture) tamperEvidence(mutate func(files map[string][]byte)) {
	f.t.Helper()
	t := f.t
	ref := EvidenceRef(f.short())
	tip := gitT(t, f.origin, "rev-parse", ref)
	entries, err := gitx.LsTreeRecursive(f.origin, tip)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, e := range entries {
		b, err := gitx.CatBlob(f.origin, e.SHA)
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name] = b
	}
	mutate(files)

	byDir := map[string][]gitx.TreeEntry{}
	for path, b := range files {
		if b == nil {
			continue
		}
		dir, file, ok := strings.Cut(path, "/")
		if !ok {
			t.Fatalf("tamper path %q has no directory", path)
		}
		sha, err := gitx.HashObject(f.origin, b)
		if err != nil {
			t.Fatal(err)
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
		sha, err := gitx.Mktree(f.origin, entries)
		if err != nil {
			t.Fatal(err)
		}
		rootEntries = append(rootEntries, gitx.TreeEntry{Mode: "040000", Type: "tree", SHA: sha, Name: dir})
	}
	root, err := gitx.Mktree(f.origin, rootEntries)
	if err != nil {
		t.Fatal(err)
	}
	parent := gitT(t, f.origin, "rev-parse", ref+"^")
	msg := gitT(t, f.origin, "log", "-1", "--format=%B", tip)
	newTip, err := gitx.CommitTreeEpoch(f.origin, root, parent, msg+"\n")
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, f.origin, "update-ref", ref, newTip)
}

// spliceWorld forges a self-consistent World object over attacker-authored
// content, writes its tree into the bare origin, and splices the object into
// the published evidence tree. It returns the bare tree sha (for
// commit-tree) and the forged world's digest. Nothing about the object is
// malformed — the filename really does encode its own digest — so only the
// signed decisions' subject set can catch it.
func (f *fixture) spliceWorld(content string) (tree, dig string) {
	f.t.Helper()
	blob, err := gitx.HashObject(f.origin, []byte(content))
	if err != nil {
		f.t.Fatal(err)
	}
	tree, err = gitx.Mktree(f.origin, []gitx.TreeEntry{
		{Mode: "100644", Type: "blob", SHA: blob, Name: "stats.txt"},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	w := object.World{
		Schema:        object.SchemaWorld,
		Intent:        f.intentDig,
		Tree:          gitx.TreePrefix + tree,
		Env:           f.envDig,
		IsolationTier: object.TierT0Worktree,
		Producer: object.Producer{
			Adapter: "script@v0", IdentityTier: "claimed", Role: "generator",
		},
		Context:   f.worlds[0].Context,
		Patch:     f.worlds[0].Patch,
		Trace:     f.worlds[0].Trace,
		Cost:      object.RunCost{WallMS: 1, Source: "none"},
		Outcome:   object.OutcomeCompleted,
		CreatedAt: "2026-08-13T00:00:01Z",
	}
	dig, canon, err := object.Digest(w)
	if err != nil {
		f.t.Fatal(err)
	}
	f.tamperEvidence(func(files map[string][]byte) {
		files["worlds/"+FileName(dig)+".json"] = canon
	})
	return tree, dig
}

// onePath returns the single evidence path with the given prefix, failing
// unless exactly one matches.
func onePath(t *testing.T, files map[string][]byte, prefix string) string {
	t.Helper()
	var hits []string
	for p := range files {
		if strings.HasPrefix(p, prefix) {
			hits = append(hits, p)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("paths with prefix %q = %v, want exactly one", prefix, hits)
	}
	return hits[0]
}

// pathsWithPrefix returns the sorted evidence paths with the given prefix.
func pathsWithPrefix(files map[string][]byte, prefix string) []string {
	var hits []string
	for p := range files {
		if strings.HasPrefix(p, prefix) {
			hits = append(hits, p)
		}
	}
	sort.Strings(hits)
	return hits
}

// failedItem returns the report item for path, requiring it to have
// failed.
func failedItem(t *testing.T, rep *Report, path string) ItemReport {
	t.Helper()
	for _, it := range rep.Items {
		if it.Path == path {
			if it.OK {
				t.Fatalf("item %s verified OK, want failure", path)
			}
			return it
		}
	}
	t.Fatalf("no report item for %s; items: %+v", path, rep.Items)
	return ItemReport{}
}
