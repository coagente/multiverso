package eval

// READING A SEALED LEDGER FROM THE EVAL PLANE.
//
// MODULE-LAYOUT DELTA, NAMED. The design's module list does not assign the
// ledger reader a file: it says the eval plane "is a READER of ledgers", and
// this is that reader. It lives in its own file because cmd/mvo already has a
// typed ledger view (cmd/mvo/state.go) that this package MUST NOT import — it
// is package main — and a second reading of the same events is exactly the
// drift risk M2b decision 17 warns about. The mitigation is that this reader
// derives nothing: it decodes recorded payloads into the recorded objects and
// hands them to the SAME `race.Decide` and `schedule.Bound` the product uses.
// Nothing here re-implements a decision.
//
// It also does not WRITE. Open uses the ledger's own Open (which would create
// a database if the path were absent), so a missing ledger is checked first
// and reported as absent — the eval plane must never bring a workspace into
// existence as a side effect of measuring one.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/race"
)

// Ledger event types this plane reads. It reads no schedule-trace events and
// writes none of anything.
const (
	evPolicyCreated     = "policy.created"
	evIntentCreated     = "intent.created"
	evWorldCreated      = "world.created"
	evReceiptRecorded   = "receipt.recorded"
	evDecisionRecorded  = "decision.recorded"
	evAdmissionFinished = "admission.finished"
)

// LedgerView is one race as the eval plane sees it: the recorded objects with
// the digests they were recorded under, never a re-serialization.
type LedgerView struct {
	Path     string
	Policy   policy.Policy
	Intent   string
	Worlds   []object.RecordedWorld
	Receipts []object.RecordedReceipt
	Decision object.Decision
	// DecisionDigest and DecisionSeq are the seal's raw material.
	DecisionDigest string
	DecisionSeq    int64
	Events         int64
	ChainVerified  bool
	// AdmitResult is what `mvo admit` said, or "" when it never ran. Absent
	// is absent: the TCAR_adm column does not appear when this is empty.
	AdmitResult string
	// CASKeys is every key in the workspace CAS, for D3.
	CASKeys []string
}

// Seal returns the ordering witness for this view.
func (v LedgerView) Seal() Seal {
	return Seal{
		Ledger:         v.Path,
		Intent:         v.Intent,
		DecisionDigest: v.DecisionDigest,
		DecisionType:   v.Decision.Type,
		DecisionSeq:    v.DecisionSeq,
		Events:         v.Events,
		ChainVerified:  v.ChainVerified,
	}
}

// ReadLedger reads the LAST race in a workspace: its pinned policy, its
// worlds, their receipts and its decision. "Last" because the eval plane races
// one intent per workspace on purpose — one race per workspace is what makes
// the join unambiguous, and a harness that guessed which of several races it
// meant would be a harness with a bug nobody could see.
func ReadLedger(workspace string) (LedgerView, error) {
	dbPath := filepath.Join(workspace, ".multiverso", "ledger.db")
	if _, err := os.Stat(dbPath); err != nil {
		return LedgerView{}, fmt.Errorf("eval: no ledger at %s: %w", dbPath, err)
	}
	led, err := ledger.Open(dbPath)
	if err != nil {
		return LedgerView{}, fmt.Errorf("eval: open ledger %s: %w", dbPath, err)
	}
	defer led.Close()

	v := LedgerView{Path: dbPath}
	var (
		policyBytes  = map[string][]byte{}
		lastDecision *object.Decision
		worlds       []object.RecordedWorld
		receipts     []object.RecordedReceipt
		intents      = map[string]object.Intent{}
	)
	err = led.Scan(func(e ledger.Event) error {
		v.Events++
		switch e.Type {
		case evPolicyCreated:
			policyBytes[e.PayloadDig] = append([]byte(nil), e.Payload...)
		case evIntentCreated:
			var in object.Intent
			if err := json.Unmarshal(e.Payload, &in); err != nil {
				return fmt.Errorf("decode intent at seq %d: %w", e.Seq, err)
			}
			intents[e.PayloadDig] = in
		case evWorldCreated:
			var w object.World
			if err := json.Unmarshal(e.Payload, &w); err != nil {
				return fmt.Errorf("decode world at seq %d: %w", e.Seq, err)
			}
			worlds = append(worlds, object.RecordedWorld{Digest: e.PayloadDig, World: w})
		case evReceiptRecorded:
			var r object.Receipt
			if err := json.Unmarshal(e.Payload, &r); err != nil {
				return fmt.Errorf("decode receipt at seq %d: %w", e.Seq, err)
			}
			receipts = append(receipts, object.RecordedReceipt{Digest: e.PayloadDig, Receipt: r})
		case evDecisionRecorded:
			var d object.Decision
			if err := json.Unmarshal(e.Payload, &d); err != nil {
				return fmt.Errorf("decode decision at seq %d: %w", e.Seq, err)
			}
			lastDecision = &d
			v.DecisionDigest = e.PayloadDig
			v.DecisionSeq = e.Seq
		case evAdmissionFinished:
			var a struct {
				Result string `json:"result"`
			}
			if err := json.Unmarshal(e.Payload, &a); err == nil && a.Result != "" {
				v.AdmitResult = a.Result
			}
		}
		return nil
	})
	if err != nil {
		return LedgerView{}, fmt.Errorf("eval: scan ledger %s: %w", dbPath, err)
	}
	if err := led.VerifyChain(); err != nil {
		return LedgerView{}, fmt.Errorf("eval: ledger %s: %w", dbPath, err)
	}
	v.ChainVerified = true
	if lastDecision == nil {
		return v, fmt.Errorf("eval: ledger %s records no decision: nothing to label", dbPath)
	}
	v.Decision = *lastDecision
	v.Intent = lastDecision.Intent
	// The policy is the one the DECISION names, not the newest one in the
	// ledger: a workspace whose default moved after the race must still
	// replay the race that happened.
	pb, ok := policyBytes[lastDecision.Policy]
	if !ok {
		return v, fmt.Errorf("eval: ledger %s: decision names policy %s, which is not in the ledger",
			dbPath, lastDecision.Policy)
	}
	pol, err := policy.Decode(pb)
	if err != nil {
		return v, fmt.Errorf("eval: ledger %s: %w", dbPath, err)
	}
	v.Policy = pol
	// Worlds and receipts are restricted to the decided intent, so a
	// workspace with several races cannot silently pool them.
	worldSet := map[string]bool{}
	for _, w := range worlds {
		if w.World.Intent == v.Intent {
			v.Worlds = append(v.Worlds, w)
			worldSet[w.Digest] = true
		}
	}
	for _, r := range receipts {
		if worldSet[r.Receipt.World] {
			v.Receipts = append(v.Receipts, r)
		}
	}
	if store, err := cas.Open(filepath.Join(workspace, ".multiverso", "cas")); err == nil {
		if keys, err := store.Keys(); err == nil {
			sort.Strings(keys)
			v.CASKeys = keys
		}
	}
	return v, nil
}

// DStar is the FULL-EVIDENCE decision over everything the ledger holds: M2b.1's
// A3 reference arm, computed by calling the product's own `race.Decide` on the
// complete receipt set. It is not a re-implementation of anything.
func (v LedgerView) DStar() object.Decision {
	return race.Decide(v.Policy, v.Worlds, v.Receipts)
}

// WorldByDigest resolves a decision subject.
func (v LedgerView) WorldByDigest(dig string) (object.RecordedWorld, bool) {
	for _, w := range v.Worlds {
		if w.Digest == dig {
			return w, true
		}
	}
	return object.RecordedWorld{}, false
}

// Winner returns the winning world of a SELECT decision, or false for any
// other decision type. A REJECT has no subject to label, and inventing one is
// how an escalation becomes an admission in a spreadsheet.
func (v LedgerView) Winner() (object.RecordedWorld, bool) {
	if v.Decision.Type != race.TypeSelect || len(v.Decision.Subject) == 0 {
		return object.RecordedWorld{}, false
	}
	return v.WorldByDigest(v.Decision.Subject[0])
}

// TreeListings maps every world tree (plus the base tree) to its path set for
// D2. It shells out to git through gitx, so the listing is git's own answer
// rather than a walk of a worktree that may have been cleaned up.
func TreeListings(repo string, v LedgerView, baseTree string) (map[string][]string, error) {
	out := map[string][]string{}
	refs := []string{}
	if baseTree != "" {
		refs = append(refs, baseTree)
	}
	for _, w := range v.Worlds {
		refs = append(refs, w.World.Tree)
	}
	for _, ref := range refs {
		treeish := ref
		// gitx records trees as "git:<sha1>"; git itself wants the sha1.
		if len(ref) > 4 && ref[:4] == "git:" {
			treeish = ref[4:]
		}
		ents, err := gitx.LsTreeRecursive(repo, treeish)
		if err != nil {
			// A tree git no longer has is not a leak; it is also not
			// evidence of cleanliness, so the caller sees the error and
			// decides. D2 is only trustworthy over trees it could read.
			return out, fmt.Errorf("eval: ls-tree %s in %s: %w", treeish, repo, err)
		}
		paths := make([]string, 0, len(ents))
		for _, e := range ents {
			paths = append(paths, e.Name)
		}
		sort.Strings(paths)
		out[ref] = paths
	}
	return out, nil
}

// TranscriptDocs collects the byte surfaces D4 scans: each world's captured
// patch, its prompt context and its transcript, read out of the workspace CAS.
// A CAS key the store does not have is recorded as a miss rather than skipped
// silently, because "we scanned three of six transcripts" and "we scanned six"
// are different claims.
func TranscriptDocs(workspace string, v LedgerView) ([]Doc, int, error) {
	store, err := cas.Open(filepath.Join(workspace, ".multiverso", "cas"))
	if err != nil {
		return nil, 0, fmt.Errorf("eval: open cas: %w", err)
	}
	var docs []Doc
	misses := 0
	add := func(kind, ref, key string) {
		if key == "" {
			return
		}
		b, err := store.Get(key)
		if err != nil {
			misses++
			return
		}
		docs = append(docs, Doc{Kind: kind, Ref: ref + " " + key, Bytes: b})
	}
	for _, w := range v.Worlds {
		add("world.patch", w.Digest, w.World.Patch)
		add("world.context", w.Digest, w.World.Context)
		add("world.trace", w.Digest, w.World.Trace)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Ref < docs[j].Ref })
	return docs, misses, nil
}
