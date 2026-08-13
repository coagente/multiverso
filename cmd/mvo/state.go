package main

import (
	"encoding/json"
	"fmt"

	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
)

// Ledger event types (M0 + M1a).
const (
	evIntentCreated       = "intent.created"
	evWorldCreated        = "world.created"
	evReceiptRecorded     = "receipt.recorded"
	evDecisionRecorded    = "decision.recorded"
	evRaceStarted         = "race.started"
	evRaceFinished        = "race.finished"
	evAdmissionStarted    = "admission.started"
	evAdmissionFinished   = "admission.finished"
	evAttestationRecorded = "attestation.recorded"
	evKeyGenerated        = "key.generated"
)

type worldRec struct {
	Seq   int64
	Dig   string
	World object.World
}

type receiptRec struct {
	Seq     int64
	Dig     string
	Receipt object.Receipt
}

type decisionRec struct {
	Seq      int64
	Dig      string
	Decision object.Decision
}

type raceStartRec struct {
	Seq    int64
	Intent string
}

type admissionStartRec struct {
	Seq            int64
	Intent         string
	SelectDecision string
}

type admissionFinishRec struct {
	Seq    int64
	Intent string
	Result string
	Commit string
}

// ledgerState is the typed view of one full ledger scan. Payload digests
// double as object digests because payloads are the canonical object bytes.
type ledgerState struct {
	Events            int
	Intents           map[string]object.Intent // digest -> intent
	Worlds            []worldRec               // seq order
	Receipts          []receiptRec             // seq order
	Decisions         []decisionRec            // seq order
	RaceStarts        []raceStartRec           // seq order
	AdmissionStarts   []admissionStartRec      // seq order
	AdmissionFinishes []admissionFinishRec     // seq order
}

func loadState(led *ledger.Ledger) (*ledgerState, error) {
	st := &ledgerState{Intents: make(map[string]object.Intent)}
	err := led.Scan(func(e ledger.Event) error {
		st.Events++
		switch e.Type {
		case evIntentCreated:
			var in object.Intent
			if err := json.Unmarshal(e.Payload, &in); err != nil {
				return fmt.Errorf("seq %d: decode intent: %w", e.Seq, err)
			}
			st.Intents[e.PayloadDig] = in
		case evWorldCreated:
			var w object.World
			if err := json.Unmarshal(e.Payload, &w); err != nil {
				return fmt.Errorf("seq %d: decode world: %w", e.Seq, err)
			}
			st.Worlds = append(st.Worlds, worldRec{Seq: e.Seq, Dig: e.PayloadDig, World: w})
		case evReceiptRecorded:
			var r object.Receipt
			if err := json.Unmarshal(e.Payload, &r); err != nil {
				return fmt.Errorf("seq %d: decode receipt: %w", e.Seq, err)
			}
			st.Receipts = append(st.Receipts, receiptRec{Seq: e.Seq, Dig: e.PayloadDig, Receipt: r})
		case evDecisionRecorded:
			var d object.Decision
			if err := json.Unmarshal(e.Payload, &d); err != nil {
				return fmt.Errorf("seq %d: decode decision: %w", e.Seq, err)
			}
			st.Decisions = append(st.Decisions, decisionRec{Seq: e.Seq, Dig: e.PayloadDig, Decision: d})
		case evRaceStarted:
			var body struct {
				Intent string `json:"intent"`
			}
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return fmt.Errorf("seq %d: decode race.started: %w", e.Seq, err)
			}
			st.RaceStarts = append(st.RaceStarts, raceStartRec{Seq: e.Seq, Intent: body.Intent})
		case evAdmissionStarted:
			var body struct {
				Intent         string `json:"intent"`
				SelectDecision string `json:"select_decision"`
			}
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return fmt.Errorf("seq %d: decode admission.started: %w", e.Seq, err)
			}
			st.AdmissionStarts = append(st.AdmissionStarts, admissionStartRec{
				Seq: e.Seq, Intent: body.Intent, SelectDecision: body.SelectDecision,
			})
		case evAdmissionFinished:
			var body struct {
				Intent string `json:"intent"`
				Result string `json:"result"`
				Commit string `json:"commit"`
			}
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return fmt.Errorf("seq %d: decode admission.finished: %w", e.Seq, err)
			}
			st.AdmissionFinishes = append(st.AdmissionFinishes, admissionFinishRec{
				Seq: e.Seq, Intent: body.Intent, Result: body.Result, Commit: body.Commit,
			})
		}
		// Other event types (race.finished, policy.created,
		// attestation.recorded, key.generated) carry no state the CLI
		// views need.
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load ledger state: %w", err)
	}
	return st, nil
}

// resolveIntent matches arg against recorded intent digests. Exact match
// only: the M0 CLI contract takes full digests.
func (st *ledgerState) resolveIntent(arg string) (string, object.Intent, error) {
	in, ok := st.Intents[arg]
	if !ok {
		return "", object.Intent{}, fmt.Errorf("no intent %q in ledger", arg)
	}
	return arg, in, nil
}

// worldsFor returns the worlds recorded for an intent within the ledger
// seq window (minSeq, maxSeq); a bound of 0 means unbounded on that side.
func (st *ledgerState) worldsFor(intentDig string, minSeq, maxSeq int64) []worldRec {
	var out []worldRec
	for _, wr := range st.Worlds {
		if wr.World.Intent != intentDig || wr.Seq <= minSeq {
			continue
		}
		if maxSeq > 0 && wr.Seq >= maxSeq {
			continue
		}
		out = append(out, wr)
	}
	return out
}

// receiptsFor returns receipts whose world is in worldDigs within the
// ledger seq window (minSeq, maxSeq); a bound of 0 means unbounded.
func (st *ledgerState) receiptsFor(worldDigs map[string]bool, minSeq, maxSeq int64) []receiptRec {
	var out []receiptRec
	for _, rr := range st.Receipts {
		if !worldDigs[rr.Receipt.World] || rr.Seq <= minSeq {
			continue
		}
		if maxSeq > 0 && rr.Seq >= maxSeq {
			continue
		}
		out = append(out, rr)
	}
	return out
}

// raceStartBefore returns the seq of the nearest race.started event for
// the intent before seq, or 0 when none was recorded. It bounds decision
// replay to the worlds of the decision's own race: a later race for the
// same intent must not leak earlier races' worlds into the replay.
func (st *ledgerState) raceStartBefore(intentDig string, seq int64) int64 {
	var best int64
	for _, rs := range st.RaceStarts {
		if rs.Intent == intentDig && rs.Seq < seq && rs.Seq > best {
			best = rs.Seq
		}
	}
	return best
}

// admissionStartBefore returns the nearest admission.started event for the
// intent before seq, or nil when none was recorded. Audit discriminates
// replay paths with it: a decision replays via admit.Decide iff its
// nearest admission.started is nearer than its nearest race.started.
func (st *ledgerState) admissionStartBefore(intentDig string, seq int64) *admissionStartRec {
	var best *admissionStartRec
	for i := range st.AdmissionStarts {
		as := &st.AdmissionStarts[i]
		if as.Intent == intentDig && as.Seq < seq && (best == nil || as.Seq > best.Seq) {
			best = as
		}
	}
	return best
}
