package admit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/coagente/multiverso/internal/attest"
	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/signing"
)

// Config wires one admission. All fields are required.
type Config struct {
	Repo      string         // git repo root (trunk = its checked-out branch)
	Ledger    *ledger.Ledger // event log
	CAS       *cas.Store     // object + artifact store
	Intent    string         // intent digest ("mv0:…") already in CAS
	SelectDig string         // digest of the SELECT decision being admitted
	Oracle    oracle.Oracle  // landing gate oracle (argv recovered via LandingOracleArgv)
	Signer    *signing.Signer
	AdmitDir  string // parent directory for landing worktrees
}

// Result is what an admission produced. Run returns (*Result, nil) for
// REJECT and ESCALATE too — a refused landing is evidence, not an error.
type Result struct {
	Decision       object.Decision
	DecisionDigest string
	Branch         string // trunk branch name
	ApplyReceipt   string // landing-apply receipt digest
	GateReceipt    string // landing suite receipt digest; "" on conflict
	Commit         string // admitted commit sha; "" unless ADMIT landed
	StatementDig   string // "mv0:…" of the canonical statement; "" unless landed
	AttestationKey string // CAS key of the DSSE bundle; "" unless landed
}

// Run executes one admission: apply the SELECT winner's patch onto the
// trunk head in a fresh worktree, recompute the admission-gate oracle on
// the exact landing tree (EP-3 v0), decide via the pure gate, and — on
// ADMIT — land a plumbing commit whose trailer names the signed DSSE
// bundle (TP-1). Conflicts are never resolved (CP-8): they escalate with
// the conflict set as receipt artifacts.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	var intent object.Intent
	if err := loadObject(cfg.CAS, cfg.Intent, &intent); err != nil {
		return nil, err
	}
	if intent.Schema != object.SchemaIntent {
		return nil, fmt.Errorf("admit: %s has schema %q, want %q", cfg.Intent, intent.Schema, object.SchemaIntent)
	}
	var sel object.Decision
	if err := loadObject(cfg.CAS, cfg.SelectDig, &sel); err != nil {
		return nil, err
	}
	if sel.Schema != object.SchemaDecision {
		return nil, fmt.Errorf("admit: %s has schema %q, want %q", cfg.SelectDig, sel.Schema, object.SchemaDecision)
	}
	if sel.Type != "SELECT" {
		return nil, fmt.Errorf("admit: decision %s has type %q, want SELECT", cfg.SelectDig, sel.Type)
	}
	if len(sel.Subject) == 0 {
		return nil, fmt.Errorf("admit: SELECT decision %s has empty subject", cfg.SelectDig)
	}
	winnerDig := sel.Subject[0]
	var winner object.World
	if err := loadObject(cfg.CAS, winnerDig, &winner); err != nil {
		return nil, err
	}
	if winner.Schema != object.SchemaWorld {
		return nil, fmt.Errorf("admit: %s has schema %q, want %q", winnerDig, winner.Schema, object.SchemaWorld)
	}
	policy, err := race.LoadPolicy(cfg.CAS, intent.Policy)
	if err != nil {
		return nil, fmt.Errorf("admit: %w", err)
	}

	// Trunk state. A detached HEAD errors here, before anything is
	// recorded.
	branch, err := gitx.CurrentBranch(cfg.Repo)
	if err != nil {
		return nil, fmt.Errorf("admit: trunk branch: %w", err)
	}
	trunkCommit, trunkTree, err := gitx.Head(cfg.Repo)
	if err != nil {
		return nil, fmt.Errorf("admit: %w", err)
	}

	if err := appendEvent(cfg.Ledger, "admission.started", map[string]any{
		"intent":          cfg.Intent,
		"select_decision": cfg.SelectDig,
		"trunk_branch":    branch,
		"trunk_commit":    trunkCommit,
		"trunk_tree":      trunkTree,
	}); err != nil {
		return nil, err
	}

	// Landing worktree at the trunk head; always removed on exit
	// (best-effort on error paths — conflict evidence survives as CAS
	// artifacts, not worktree state).
	if err := os.MkdirAll(cfg.AdmitDir, 0o755); err != nil {
		return nil, fmt.Errorf("admit: create admit dir: %w", err)
	}
	dir, err := os.MkdirTemp(cfg.AdmitDir, "admit-")
	if err != nil {
		return nil, fmt.Errorf("admit: create landing worktree dir: %w", err)
	}
	if err := gitx.AddWorktree(cfg.Repo, dir, trunkCommit); err != nil {
		os.Remove(dir)
		return nil, fmt.Errorf("admit: landing worktree: %w", err)
	}
	defer func() {
		_ = gitx.RemoveWorktree(cfg.Repo, dir)
		_ = os.Remove(dir) // in case worktree removal left the dir behind
	}()

	// Applicability is a property of (patch, trunk tree): the apply
	// receipt's freshness pins the pre-apply state (EP-3).
	trunkEnv, err := race.EnvDigest(cfg.CAS, dir)
	if err != nil {
		return nil, fmt.Errorf("admit: %w", err)
	}
	patch, err := cfg.CAS.Get(winner.Patch)
	if err != nil {
		return nil, fmt.Errorf("admit: load winner patch %s: %w", winner.Patch, err)
	}
	start := time.Now()
	stdout, stderr, applyErr := gitx.ApplyCapture(dir, patch)
	applyMS := time.Since(start).Milliseconds()

	stdoutKey, err := cfg.CAS.Put(stdout)
	if err != nil {
		return nil, fmt.Errorf("admit: store apply stdout: %w", err)
	}
	stderrKey, err := cfg.CAS.Put(stderr)
	if err != nil {
		return nil, fmt.Errorf("admit: store apply stderr: %w", err)
	}
	applyCfgDig, _, err := object.Digest(map[string]any{"strategy": "git-apply-index"})
	if err != nil {
		return nil, fmt.Errorf("admit: digest apply config: %w", err)
	}
	exitCode, status := 0, "pass"
	if applyErr != nil {
		// gitx does not surface git's own exit code; 1 marks the conflict.
		exitCode, status = 1, "fail"
	}
	applyRec := object.Receipt{
		Schema: object.SchemaReceipt,
		World:  winnerDig,
		Oracle: object.OracleRef{ID: OracleIDLandingApply, Version: "v0", Config: applyCfgDig},
		Execution: object.Execution{
			Argv:          []string{"git", "apply", "--index", "-"},
			ExitCode:      exitCode,
			DurationMS:    applyMS,
			IsolationTier: object.TierT0Worktree,
			IsolationCaps: object.HostCaps(),
		},
		Result: object.Result{Status: status, Artifacts: []string{stdoutKey, stderrKey}},
		Freshness: object.Freshness{
			Basis:    "construction",
			ValidFor: object.ValidFor{Tree: trunkTree, Env: trunkEnv},
		},
		RecheckTier: "V1-replayable",
		Family:      FamilyLandingApply,
		Cost:        object.Cost{WallMS: applyMS},
		CreatedAt:   nowRFC3339(),
	}
	applyDig, err := recordObject(cfg, "receipt.recorded", applyRec)
	if err != nil {
		return nil, err
	}

	res := &Result{Branch: branch, ApplyReceipt: applyDig}

	// Conflict path (CP-8): never resolve, never --3way, never rebase.
	if applyErr != nil {
		dec := Decide(policy, cfg.Intent, winnerDig, applyRec, nil)
		dec.CreatedAt = nowRFC3339()
		decDig, err := recordObject(cfg, "decision.recorded", dec)
		if err != nil {
			return nil, err
		}
		if err := appendFinished(cfg, "", "", decDig, "", dec.Type); err != nil {
			return nil, err
		}
		res.Decision, res.DecisionDigest = dec, decDig
		return res, nil
	}

	// Clean apply: recompute the admission gate on the exact landing tree
	// (EP-3 v0) — race receipts are valid only for the race tree.
	landingTree, err := gitx.WriteTree(dir)
	if err != nil {
		return nil, fmt.Errorf("admit: %w", err)
	}
	landingEnv, err := race.EnvDigest(cfg.CAS, dir)
	if err != nil {
		return nil, fmt.Errorf("admit: %w", err)
	}
	// Landing gates stay T0 on the host in M1c (decision 12): an intent
	// raced under T1 lands via a host-run gate whose receipt honestly
	// records T0-worktree + the host env digest; if the host lacks the
	// toolchain the gate fails → REJECT — honest, never silently skipped.
	gateRec, err := cfg.Oracle.Run(ctx, backend.HostDir(dir))
	if err != nil {
		return nil, fmt.Errorf("admit: landing oracle in %s: %w", dir, err)
	}
	gateRec.World = winnerDig
	gateRec.Freshness.ValidFor = object.ValidFor{Tree: landingTree, Env: landingEnv}
	gateDig, err := recordObject(cfg, "receipt.recorded", gateRec)
	if err != nil {
		return nil, err
	}
	res.GateReceipt = gateDig

	dec := Decide(policy, cfg.Intent, winnerDig, applyRec, &gateRec)
	dec.CreatedAt = nowRFC3339()
	decDig, err := recordObject(cfg, "decision.recorded", dec)
	if err != nil {
		return nil, err
	}
	res.Decision, res.DecisionDigest = dec, decDig

	if dec.Type != TypeAdmit {
		if err := appendFinished(cfg, "", "", decDig, "", dec.Type); err != nil {
			return nil, err
		}
		return res, nil
	}

	// ADMIT: build and sign the statement, then land by plumbing.
	evidence := []string{applyDig, gateDig}
	sort.Strings(evidence)
	stmt, err := attest.New(branch, landingTree, attest.Predicate{
		Intent:         cfg.Intent,
		World:          winnerDig,
		Decision:       decDig,
		SelectDecision: cfg.SelectDig,
		Evidence:       evidence,
		Policy:         dec.Policy,
		BudgetConsumed: attest.Budget{WallMS: applyRec.Cost.WallMS + gateRec.Cost.WallMS},
		ProducerKeyID:  cfg.Signer.KeyID,
		Trunk:          attest.Trunk{Branch: branch, ParentCommit: trunkCommit},
	})
	if err != nil {
		return nil, fmt.Errorf("admit: %w", err)
	}
	payload, err := object.Canonical(stmt)
	if err != nil {
		return nil, fmt.Errorf("admit: encode statement: %w", err)
	}
	stmtDig := object.DigestBytes(payload)
	env, err := signing.Sign(cfg.Signer, signing.PayloadTypeInToto, payload)
	if err != nil {
		return nil, fmt.Errorf("admit: %w", err)
	}
	bundle, err := object.Canonical(env)
	if err != nil {
		return nil, fmt.Errorf("admit: encode bundle: %w", err)
	}
	bundleKey, err := cfg.CAS.Put(bundle)
	if err != nil {
		return nil, fmt.Errorf("admit: store bundle: %w", err)
	}
	res.StatementDig, res.AttestationKey = stmtDig, bundleKey

	message := fmt.Sprintf("%s\n\nMultiverso-Intent: %s\nMultiverso-Decision: %s\nMultiverso-Attestation: %s\n",
		intent.Spec.Title, cfg.Intent, decDig, bundleKey)
	commit, err := gitx.CommitTree(cfg.Repo, landingTree, trunkCommit, message)
	if err != nil {
		landErr := fmt.Errorf("admit: land on %s: %w", branch, err)
		if evErr := appendFinished(cfg, bundleKey, "", decDig, landErr.Error(), "ERROR"); evErr != nil {
			return nil, evErr
		}
		return nil, landErr
	}
	// Whether the primary worktree can be fast-forwarded is judged against
	// the old tip: after UpdateRef a checked-out branch always reports the
	// index as differing from the new HEAD.
	syncable := false
	if cur, curErr := gitx.CurrentBranch(cfg.Repo); curErr == nil && cur == branch {
		if clean, cleanErr := gitx.StatusClean(cfg.Repo); cleanErr == nil && clean {
			syncable = true
		}
	}
	if err := gitx.UpdateRef(cfg.Repo, "refs/heads/"+branch, commit, trunkCommit); err != nil {
		// Compare-and-swap failed: trunk moved mid-admission. The ledger
		// honestly shows an ADMIT decision + attestation that did not
		// land; re-running admit is permitted (the CLI guard keys on
		// result == "ADMIT" only).
		landErr := fmt.Errorf("admit: land on %s: %w", branch, err)
		if evErr := appendFinished(cfg, bundleKey, "", decDig, landErr.Error(), "ERROR"); evErr != nil {
			return nil, evErr
		}
		return nil, landErr
	}
	res.Commit = commit

	// Working-tree sync, best-effort: a pure fast-forward of
	// already-committed content, only when the operator's tree was clean
	// and on the trunk branch.
	if syncable {
		if err := gitx.ResetHard(cfg.Repo); err != nil {
			fmt.Fprintf(os.Stderr, "mvo: admit: working tree lags %s (reset failed: %v)\n", branch, err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "mvo: admit: working tree lags %s (not clean or not on the trunk branch); commit %s landed\n",
			branch, commit)
	}

	if err := appendEvent(cfg.Ledger, "attestation.recorded", map[string]any{
		"bundle":       bundleKey,
		"commit":       commit,
		"decision":     decDig,
		"intent":       cfg.Intent,
		"key_id":       cfg.Signer.KeyID,
		"statement":    stmtDig,
		"subject_tree": landingTree,
		"trunk_branch": branch,
	}); err != nil {
		return nil, err
	}
	if err := appendFinished(cfg, bundleKey, commit, decDig, "", TypeAdmit); err != nil {
		return nil, err
	}
	return res, nil
}

// LandingOracleArgv recovers the race's suite oracle command from the
// SELECT decision's evidence: among receipts in sel.Evidence (loaded from
// CAS) with Family == "suite" and World == sel.Subject[0], the one with
// the smallest receipt digest wins (order-independent, same disambiguation
// as race.Decide). The gate that judged the race judges the landing;
// operators cannot swap gates at admit time.
func LandingOracleArgv(store *cas.Store, sel object.Decision) ([]string, error) {
	if len(sel.Subject) == 0 {
		return nil, errors.New("admit: SELECT decision has empty subject")
	}
	winner := sel.Subject[0]
	best := ""
	var argv []string
	for _, dig := range sel.Evidence {
		var rec object.Receipt
		if err := loadObject(store, dig, &rec); err != nil {
			return nil, err
		}
		if rec.Family != "suite" || rec.World != winner {
			continue
		}
		if best == "" || dig < best {
			best = dig
			argv = append([]string(nil), rec.Execution.Argv...)
		}
	}
	if best == "" {
		return nil, fmt.Errorf("admit: no suite receipt for winner %s in the SELECT decision's evidence", winner)
	}
	return argv, nil
}

func (cfg Config) validate() error {
	switch {
	case cfg.Repo == "":
		return errors.New("admit: config: empty repo")
	case cfg.Ledger == nil:
		return errors.New("admit: config: nil ledger")
	case cfg.CAS == nil:
		return errors.New("admit: config: nil CAS")
	case cfg.Intent == "":
		return errors.New("admit: config: empty intent digest")
	case cfg.SelectDig == "":
		return errors.New("admit: config: empty select decision digest")
	case cfg.Oracle == nil:
		return errors.New("admit: config: nil oracle")
	case cfg.Signer == nil:
		return errors.New("admit: config: nil signer")
	case cfg.AdmitDir == "":
		return errors.New("admit: config: empty admit dir")
	}
	return nil
}

// appendFinished records admission.finished; every key is always present
// ("" when not applicable).
func appendFinished(cfg Config, attestation, commit, decision, errMsg, result string) error {
	return appendEvent(cfg.Ledger, "admission.finished", map[string]any{
		"attestation": attestation,
		"commit":      commit,
		"decision":    decision,
		"error":       errMsg,
		"intent":      cfg.Intent,
		"result":      result,
	})
}

// recordObject digests v, stores its canonical bytes in CAS, and appends
// them to the ledger under typ. Returns the object digest.
func recordObject(cfg Config, typ string, v any) (string, error) {
	dig, canon, err := object.Digest(v)
	if err != nil {
		return "", fmt.Errorf("admit: digest %s: %w", typ, err)
	}
	if _, err := cfg.CAS.Put(canon); err != nil {
		return "", fmt.Errorf("admit: store %s: %w", typ, err)
	}
	if _, err := cfg.Ledger.Append(typ, canon); err != nil {
		return "", fmt.Errorf("admit: record %s: %w", typ, err)
	}
	return dig, nil
}

func appendEvent(led *ledger.Ledger, typ string, body map[string]any) error {
	payload, err := object.Canonical(body)
	if err != nil {
		return fmt.Errorf("admit: encode %s: %w", typ, err)
	}
	if _, err := led.Append(typ, payload); err != nil {
		return fmt.Errorf("admit: record %s: %w", typ, err)
	}
	return nil
}

// loadObject fetches an object's canonical bytes from CAS by its "mv0:"
// digest and decodes them into v.
func loadObject(store *cas.Store, dig string, v any) error {
	key, err := object.CASKey(dig)
	if err != nil {
		return fmt.Errorf("admit: %w", err)
	}
	b, err := store.Get(key)
	if err != nil {
		return fmt.Errorf("admit: load %s: %w", dig, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("admit: decode %s: %w", dig, err)
	}
	return nil
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
