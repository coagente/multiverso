package publish

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/signing"
)

// Item is one file in the evidence tree (M1d decision 4: a file is named
// by the identifier other objects use to cite it).
type Item struct {
	Path  string // e.g. "receipts/mv0_<hex>.dsse.json"
	ID    string // the cited identifier the filename encodes
	Bytes []byte // canonical object bytes, or canonical envelope bytes
}

// Candidate is one publishable world of the SELECT race, ordinal-ordered.
type Candidate struct {
	Ordinal int
	Dig     string
	World   object.World
	Winner  bool
}

// Closure is everything one intent publishes (M1d decision 8: the intent,
// its policy, the latest SELECT decision, every subject world, every
// evidence receipt, and — when admitted — the ADMIT decision, both landing
// receipts, and the attestation bundle). Deterministic: two calls over the
// same ledger produce byte-identical Items (decision 9).
type Closure struct {
	IntentDig, Short string
	Intent           object.Intent
	SelectDig        string
	Select           object.Decision
	Admitted         bool   // a landed ADMIT exists (admission.finished result=="ADMIT")
	AdmitDig         string // "" unless Admitted
	Candidates       []Candidate
	Items            []Item // Path-sorted
}

// decRec / worldRec / startRec / admFinishRec / pubFinishRec are the typed
// rows of one ledger scan the publish package needs.
type decRec struct {
	Seq int64
	Dig string
	Dec object.Decision
}

type worldRec struct {
	Seq   int64
	Dig   string
	World object.World
}

type startRec struct {
	Seq    int64
	Intent string
}

type admFinishRec struct {
	Seq         int64
	Intent      string
	Result      string
	Decision    string
	Attestation string
}

type pubFinishRec struct {
	Seq    int64
	TS     string
	Intent string
}

// ledgerView is the publish-relevant slice of one full ledger scan.
type ledgerView struct {
	Intents     []string // intent digests in seq order
	Decisions   []decRec
	Worlds      []worldRec
	RaceStarts  []startRec
	AdmFinishes []admFinishRec
	PubFinishes []pubFinishRec
}

func scanLedger(led *ledger.Ledger) (*ledgerView, error) {
	v := &ledgerView{}
	err := led.Scan(func(e ledger.Event) error {
		switch e.Type {
		case "intent.created":
			v.Intents = append(v.Intents, e.PayloadDig)
		case "decision.recorded":
			var d object.Decision
			if err := json.Unmarshal(e.Payload, &d); err != nil {
				return fmt.Errorf("publish: seq %d: decode decision: %w", e.Seq, err)
			}
			v.Decisions = append(v.Decisions, decRec{Seq: e.Seq, Dig: e.PayloadDig, Dec: d})
		case "world.created":
			var w object.World
			if err := json.Unmarshal(e.Payload, &w); err != nil {
				return fmt.Errorf("publish: seq %d: decode world: %w", e.Seq, err)
			}
			v.Worlds = append(v.Worlds, worldRec{Seq: e.Seq, Dig: e.PayloadDig, World: w})
		case "race.started":
			var body struct {
				Intent string `json:"intent"`
			}
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return fmt.Errorf("publish: seq %d: decode race.started: %w", e.Seq, err)
			}
			v.RaceStarts = append(v.RaceStarts, startRec{Seq: e.Seq, Intent: body.Intent})
		case "admission.finished":
			var body struct {
				Intent      string `json:"intent"`
				Result      string `json:"result"`
				Decision    string `json:"decision"`
				Attestation string `json:"attestation"`
			}
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return fmt.Errorf("publish: seq %d: decode admission.finished: %w", e.Seq, err)
			}
			v.AdmFinishes = append(v.AdmFinishes, admFinishRec{
				Seq: e.Seq, Intent: body.Intent, Result: body.Result,
				Decision: body.Decision, Attestation: body.Attestation,
			})
		case "publish.finished":
			var body struct {
				Intent string `json:"intent"`
			}
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return fmt.Errorf("publish: seq %d: decode publish.finished: %w", e.Seq, err)
			}
			v.PubFinishes = append(v.PubFinishes, pubFinishRec{Seq: e.Seq, TS: e.TS, Intent: body.Intent})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// checkShortCollision fails loudly when any other recorded intent shares
// intentDig's short (M1d decision 1: no silent disambiguation — 48 bits is
// collision-safe at any plausible intent count, so a collision is worth an
// operator's attention).
func checkShortCollision(intentDig, short string, recorded []string) error {
	for _, other := range recorded {
		if other == intentDig {
			continue
		}
		otherShort, err := IntentShort(other)
		if err != nil {
			continue // non-digest payloads never reach here; be lenient
		}
		if otherShort == short {
			return fmt.Errorf("publish: intent-short collision: %s and %s both shorten to %s", intentDig, other, short)
		}
	}
	return nil
}

// selectRace is the latest SELECT decision for an intent plus its race
// window's worlds in world.created (ordinal) order.
type selectRace struct {
	Seq    int64
	Dig    string
	Dec    object.Decision
	Worlds []Candidate // ordinal-ordered; Winner flagged
}

// latestSelect finds the intent's latest SELECT decision (same rule as mvo
// admit) and derives candidate ordinals from world.created order within
// the race window that produced it. The decision's subject set must equal
// the window's world set — a mismatch is ledger inconsistency and fails
// loudly.
func latestSelect(v *ledgerView, intentDig string) (*selectRace, error) {
	var sel *decRec
	for i := range v.Decisions {
		d := &v.Decisions[i]
		if d.Dec.Intent == intentDig && d.Dec.Type == "SELECT" {
			sel = d
		}
	}
	if sel == nil {
		return nil, fmt.Errorf("publish: no SELECT decision for intent %s (run \"mvo race\" first)", intentDig)
	}
	var raceStart int64
	for _, rs := range v.RaceStarts {
		if rs.Intent == intentDig && rs.Seq < sel.Seq && rs.Seq > raceStart {
			raceStart = rs.Seq
		}
	}
	subject := make(map[string]bool, len(sel.Dec.Subject))
	for _, dig := range sel.Dec.Subject {
		subject[dig] = true
	}
	var cands []Candidate
	for _, wr := range v.Worlds {
		if wr.World.Intent != intentDig || wr.Seq <= raceStart || wr.Seq >= sel.Seq {
			continue
		}
		cands = append(cands, Candidate{
			Ordinal: len(cands) + 1,
			Dig:     wr.Dig,
			World:   wr.World,
			Winner:  len(sel.Dec.Subject) > 0 && wr.Dig == sel.Dec.Subject[0],
		})
		if !subject[wr.Dig] {
			return nil, fmt.Errorf("publish: world %s in the race window is not a subject of SELECT decision %s (ledger inconsistency)", wr.Dig, sel.Dig)
		}
	}
	if len(cands) != len(sel.Dec.Subject) {
		return nil, fmt.Errorf("publish: SELECT decision %s has %d subjects but its race window holds %d worlds (ledger inconsistency)",
			sel.Dig, len(sel.Dec.Subject), len(cands))
	}
	return &selectRace{Seq: sel.Seq, Dig: sel.Dig, Dec: sel.Dec, Worlds: cands}, nil
}

// BuildClosure scans the ledger for the intent's latest SELECT decision,
// assembles the decision-8 closure, and signs receipts/decisions with
// signer (sign-on-publish, decision 9). DP-3 holds: world objects carry
// context/trace as CAS keys; the payloads never join Items.
func BuildClosure(led *ledger.Ledger, store *cas.Store, signer *signing.Signer,
	intentDig string) (*Closure, error) {
	if signer == nil {
		return nil, fmt.Errorf("publish: nil signer")
	}
	short, err := IntentShort(intentDig)
	if err != nil {
		return nil, err
	}
	v, err := scanLedger(led)
	if err != nil {
		return nil, err
	}
	recorded := false
	for _, dig := range v.Intents {
		if dig == intentDig {
			recorded = true
			break
		}
	}
	if !recorded {
		return nil, fmt.Errorf("publish: no intent %s in ledger", intentDig)
	}
	if err := checkShortCollision(intentDig, short, v.Intents); err != nil {
		return nil, err
	}

	sel, err := latestSelect(v, intentDig)
	if err != nil {
		return nil, err
	}

	intentBytes, err := getCAS(store, intentDig)
	if err != nil {
		return nil, err
	}
	var intent object.Intent
	if err := json.Unmarshal(intentBytes, &intent); err != nil {
		return nil, fmt.Errorf("publish: decode intent %s: %w", intentDig, err)
	}
	if intent.Policy != sel.Dec.Policy {
		return nil, fmt.Errorf("publish: intent %s pins policy %s but SELECT decision %s cites %s (ledger inconsistency)",
			intentDig, intent.Policy, sel.Dig, sel.Dec.Policy)
	}
	policyBytes, err := getCAS(store, intent.Policy)
	if err != nil {
		return nil, err
	}

	cl := &Closure{
		IntentDig:  intentDig,
		Short:      short,
		Intent:     intent,
		SelectDig:  sel.Dig,
		Select:     sel.Dec,
		Candidates: sel.Worlds,
	}

	items := map[string]Item{} // path → item (dedupes shared receipts)
	add := func(kind, id string, dsse bool, b []byte) {
		ext := ".json"
		if dsse {
			ext = ".dsse.json"
		}
		path := kind + "/" + FileName(id) + ext
		items[path] = Item{Path: path, ID: id, Bytes: b}
	}
	addSigned := func(kind, payloadType, dig string) error {
		payload, err := getCAS(store, dig)
		if err != nil {
			return err
		}
		env, err := SignItem(signer, payloadType, payload)
		if err != nil {
			return err
		}
		add(kind, dig, true, env)
		return nil
	}

	add("intent", intentDig, false, intentBytes)
	add("policy", intent.Policy, false, policyBytes)
	for _, cand := range sel.Worlds {
		worldBytes, err := getCAS(store, cand.Dig)
		if err != nil {
			return nil, err
		}
		add("worlds", cand.Dig, false, worldBytes)
	}
	if err := addSigned("decisions", PayloadTypeDecision, sel.Dig); err != nil {
		return nil, err
	}
	for _, dig := range sel.Dec.Evidence {
		if err := addSigned("receipts", PayloadTypeReceipt, dig); err != nil {
			return nil, err
		}
	}

	// A landed ADMIT (admission.finished result=="ADMIT") extends the
	// closure with the ADMIT decision, both landing receipts, and the
	// attestation bundle — verbatim CAS bytes, already a DSSE bundle
	// content-addressed by its CAS key (M1a decision 2).
	var admitted *admFinishRec
	for i := range v.AdmFinishes {
		af := &v.AdmFinishes[i]
		if af.Intent == intentDig && af.Result == "ADMIT" {
			admitted = af
		}
	}
	if admitted != nil {
		cl.Admitted, cl.AdmitDig = true, admitted.Decision
		admitBytes, err := getCAS(store, admitted.Decision)
		if err != nil {
			return nil, err
		}
		var admitDec object.Decision
		if err := json.Unmarshal(admitBytes, &admitDec); err != nil {
			return nil, fmt.Errorf("publish: decode ADMIT decision %s: %w", admitted.Decision, err)
		}
		if err := addSigned("decisions", PayloadTypeDecision, admitted.Decision); err != nil {
			return nil, err
		}
		for _, dig := range admitDec.Evidence {
			if err := addSigned("receipts", PayloadTypeReceipt, dig); err != nil {
				return nil, err
			}
		}
		bundle, err := store.Get(admitted.Attestation)
		if err != nil {
			return nil, fmt.Errorf("publish: load attestation %s: %w", admitted.Attestation, err)
		}
		add("attestation", admitted.Attestation, true, bundle)
	}

	paths := make([]string, 0, len(items))
	for p := range items {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	cl.Items = make([]Item, 0, len(paths))
	for _, p := range paths {
		cl.Items = append(cl.Items, items[p])
	}
	return cl, nil
}

// getCAS fetches an object's canonical bytes by "mv0:" digest.
func getCAS(store *cas.Store, dig string) ([]byte, error) {
	key, err := object.CASKey(dig)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
	b, err := store.Get(key)
	if err != nil {
		return nil, fmt.Errorf("publish: load %s: %w", dig, err)
	}
	return b, nil
}
