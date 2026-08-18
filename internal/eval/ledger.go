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
	"github.com/coagente/multiverso/internal/schedule"
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
	evRaceStarted       = "race.started"
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

	// ---------------------------------------------------------------------
	// M2d.1 decision 3 — THE RACE WINDOW.
	//
	// A warmed template's ledger holds the warm-up races, and every arm
	// workspace is a copy of it. Every field above is scoped to ONE race, and
	// that scoping is now a precondition rather than a convenience: without
	// it `oracleSpend` would carry the warm-up's spend and the arms would stop
	// being budget-matched IN THE REPORT even though they are matched IN THE
	// POOL, and `schedule.Bound` would enumerate over two races' receipts to
	// derive B, the experiment's own independent variable.
	//
	// THE WINDOW NARROWS WHAT IS MEASURED; IT MUST NEVER NARROW WHAT IS
	// SCANNED. The leak detectors and the canary keep reading the whole
	// workspace, including the warm-up races — a detector that respected the
	// window would stop looking at precisely the races nobody is reading,
	// which is where a leak would be least likely to be noticed.
	// ---------------------------------------------------------------------

	// Races is every intent this workspace raced, in ledger order. More than
	// one means the workspace was seeded from a warmed template.
	Races []string
	// Trace is THIS race's recorded allocation trace, assembled from the
	// events between this race's race.started and its decision. It is what
	// coverage is computed from, and it is empty for a fixed-ladder race.
	Trace schedule.Trace
	// OutsideSpendMS is Σ cost.wall_ms over every receipt in the workspace
	// that belongs to some OTHER race — the warm-up's spend. It is recorded
	// rather than discarded because an uncharged cost that is also unreported
	// is a cost nobody can audit (decision 4).
	OutsideSpendMS int64
	// SpendMS is Σ cost.wall_ms over THIS race's receipts alone: what the arm
	// was actually charged.
	SpendMS int64
}

// Race narrows a workspace to one race by intent. It is a READ, and reads
// invalidate nothing: the ledger is append-only and hash-chained, nothing is
// truncated, no event is excluded from the chain, and `mvo audit` stays OK
// over a warmed workspace.
//
// An intent this workspace never raced yields an EMPTY view with the error
// naming what was asked for, rather than the last race under a name that is
// not its own.
func (v LedgerView) Race(workspace, intent string) (LedgerView, error) {
	return readLedger(workspace, intent)
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
func ReadLedger(workspace string) (LedgerView, error) { return readLedger(workspace, "") }

// ReadLedgerRace reads ONE NAMED RACE out of a workspace that may hold
// several. It is decision 3's window and the precondition of warming: a
// warmed workspace's ledger holds the warm-up races, and every consumer of
// LedgerView silently assumed one race per workspace.
//
// An empty intent keeps ReadLedger's meaning — the LAST race.
func ReadLedgerRace(workspace, intent string) (LedgerView, error) {
	return readLedger(workspace, intent)
}

func readLedger(workspace, wantIntent string) (LedgerView, error) {
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
	type seqDecision struct {
		dec object.Decision
		dig string
		seq int64
	}
	var (
		policyBytes = map[string][]byte{}
		decisions   []seqDecision
		worlds      []object.RecordedWorld
		receipts    []object.RecordedReceipt
		intents     = map[string]object.Intent{}
		raceStart   = map[string]int64{}
		traceEvents []struct {
			ev  schedule.Event
			seq int64
		}
		admitByIntent = map[string]string{}
		lastAdmit     string
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
		case evRaceStarted:
			var r struct {
				Intent string `json:"intent"`
			}
			if err := json.Unmarshal(e.Payload, &r); err != nil {
				return fmt.Errorf("decode race.started at seq %d: %w", e.Seq, err)
			}
			if r.Intent != "" {
				if _, seen := raceStart[r.Intent]; !seen {
					v.Races = append(v.Races, r.Intent)
				}
				raceStart[r.Intent] = e.Seq
			}
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
			decisions = append(decisions, seqDecision{dec: d, dig: e.PayloadDig, seq: e.Seq})
		case evAdmissionFinished:
			var a struct {
				Result string `json:"result"`
				Intent string `json:"intent"`
			}
			if err := json.Unmarshal(e.Payload, &a); err == nil && a.Result != "" {
				lastAdmit = a.Result
				if a.Intent != "" {
					admitByIntent[a.Intent] = a.Result
				}
			}
		case schedule.EventStarted, schedule.EventStep, schedule.EventFinished, schedule.EventOracleSkipped:
			traceEvents = append(traceEvents, struct {
				ev  schedule.Event
				seq int64
			}{ev: schedule.Event{Type: e.Type, Payload: append([]byte(nil), e.Payload...)}, seq: e.Seq})
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

	// THE WINDOW. Without an intent this is ReadLedger's own meaning — the
	// last decision in the file. With one it is that intent's decision, and an
	// intent with no decision is reported as absent rather than silently
	// answered with somebody else's race.
	var chosen *seqDecision
	for i := range decisions {
		d := &decisions[i]
		if wantIntent == "" || d.dec.Intent == wantIntent {
			chosen = d
		}
	}
	if chosen == nil {
		if wantIntent != "" {
			return v, fmt.Errorf("eval: ledger %s records no decision for intent %s: "+
				"the window is empty and every consumer reports absence", dbPath, wantIntent)
		}
		return v, fmt.Errorf("eval: ledger %s records no decision: nothing to label", dbPath)
	}
	v.Decision = chosen.dec
	v.DecisionDigest = chosen.dig
	v.DecisionSeq = chosen.seq
	v.Intent = chosen.dec.Intent
	if r, ok := admitByIntent[v.Intent]; ok {
		v.AdmitResult = r
	} else if wantIntent == "" || len(admitByIntent) == 0 {
		// `admission.finished` did not carry an intent in every era, so a
		// workspace with one race keeps the old reading rather than losing the
		// column. A workspace with several races and no per-intent attribution
		// reports absence, which is the honest answer.
		if len(v.Races) <= 1 {
			v.AdmitResult = lastAdmit
		}
	}
	// The policy is the one the DECISION names, not the newest one in the
	// ledger: a workspace whose default moved after the race must still
	// replay the race that happened.
	pb, ok := policyBytes[v.Decision.Policy]
	if !ok {
		return v, fmt.Errorf("eval: ledger %s: decision names policy %s, which is not in the ledger",
			dbPath, v.Decision.Policy)
	}
	pol, err := policy.Decode(pb)
	if err != nil {
		return v, fmt.Errorf("eval: ledger %s: %w", dbPath, err)
	}
	v.Policy = pol
	// Worlds and receipts are restricted to the decided intent, so a
	// workspace with several races cannot silently pool them. This is the
	// conjunct that keeps a warmed arm's reported spend free of the warm-up's
	// milliseconds: a warm-up is a DIFFERENT INTENT and a DIFFERENT RACE, so
	// its receipts hang off worlds that are not in this window at all.
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
			v.SpendMS += r.Receipt.Cost.WallMS
			continue
		}
		v.OutsideSpendMS += r.Receipt.Cost.WallMS
	}
	// The allocation trace of THIS race: the schedule.* events between this
	// race's race.started and its decision. A trace assembled across two races
	// would describe an allocation nobody made.
	lo := raceStart[v.Intent]
	var window []schedule.Event
	for _, te := range traceEvents {
		if te.seq >= lo && te.seq <= v.DecisionSeq {
			window = append(window, te.ev)
		}
	}
	if tr, terr := schedule.Collect(window); terr == nil {
		v.Trace = tr
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
