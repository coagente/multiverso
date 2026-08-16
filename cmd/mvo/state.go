package main

import (
	"encoding/json"
	"fmt"

	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/schedule"
)

// Ledger event types (M0 + M1a + M1e).
const (
	evPolicyCreated       = "policy.created"
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
	evPublishStarted      = "publish.started"
	evPublishFinished     = "publish.finished"
	evPruneExecuted       = "prune.executed"
	// M1e/M2a observational events the M2b explain surface reads for the
	// CONTROL-PLANE BOUNDS the bracket is allowed to use: the base tree's
	// collect measurement and the pinned corpus's case count. Both were
	// produced before any candidate existed, which is the only reason the
	// scheduler may read them at all (M1f, restated by M2b decision 3b).
	evBaselineRecorded = "baseline.recorded"
	evCorpusRecorded   = "corpus.recorded"
)

// scheduleEvents are M2b's four observational trace events. They carry no
// payload digests, are covered by the ledger hash chain, are IGNORED BY
// REPLAY, and reach Decide never — so collecting them here can change no
// decision, which is the property that lets the scheduler be rewritten
// without invalidating history.
var scheduleEvents = map[string]bool{
	schedule.EventStarted:       true,
	schedule.EventStep:          true,
	schedule.EventFinished:      true,
	schedule.EventOracleSkipped: true,
}

// policyRec is one recorded policy: the canonical bytes exactly as they
// were appended, and the digest they were recorded under. Policies are
// never re-serialized on the way back out (M1e decision 1).
type policyRec struct {
	Seq   int64
	Dig   string
	Bytes []byte
}

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

type publishFinishRec struct {
	Seq    int64
	TS     string
	Intent string
}

// scheduleEventRec is one raw allocation-trace event: the type and the
// canonical payload bytes exactly as recorded. The bytes are kept UNPARSED
// here and decoded by internal/schedule, which owns the shape — the trace is
// RECORDED EVIDENCE and this view must not become a second, drifting reading
// of it (M2b decision 17).
type scheduleEventRec struct {
	Seq     int64
	Type    string
	Payload []byte
}

// boundRec is one control-plane measurement the bracket may use as a
// ceiling: `baseline.recorded`'s collected_total or `corpus.recorded`'s case
// count, each with the intent it was measured for.
type boundRec struct {
	Seq    int64
	Intent string
	Value  int64
}

// ledgerState is the typed view of one full ledger scan. Payload digests
// double as object digests because payloads are the canonical object bytes.
type ledgerState struct {
	Events            int
	Policies          []policyRec              // seq order
	Intents           map[string]object.Intent // digest -> intent
	Worlds            []worldRec               // seq order
	Receipts          []receiptRec             // seq order
	Decisions         []decisionRec            // seq order
	RaceStarts        []raceStartRec           // seq order
	AdmissionStarts   []admissionStartRec      // seq order
	AdmissionFinishes []admissionFinishRec     // seq order
	PublishFinishes   []publishFinishRec       // seq order (prune's --older-than input)
	Schedules         []scheduleEventRec       // seq order — M2b's allocation trace
	Baselines         []boundRec               // seq order — collected_base per race
	Corpora           []boundRec               // seq order — pinned corpus cases per race
}

func loadState(led *ledger.Ledger) (*ledgerState, error) {
	st := &ledgerState{Intents: make(map[string]object.Intent)}
	err := led.Scan(func(e ledger.Event) error {
		st.Events++
		switch e.Type {
		case evPolicyCreated:
			st.Policies = append(st.Policies, policyRec{
				Seq: e.Seq, Dig: e.PayloadDig, Bytes: append([]byte(nil), e.Payload...),
			})
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
		case evPublishFinished:
			var body struct {
				Intent string `json:"intent"`
			}
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return fmt.Errorf("seq %d: decode publish.finished: %w", e.Seq, err)
			}
			st.PublishFinishes = append(st.PublishFinishes, publishFinishRec{
				Seq: e.Seq, TS: e.TS, Intent: body.Intent,
			})
		case evBaselineRecorded:
			var body struct {
				Intent         string `json:"intent"`
				CollectedTotal int64  `json:"collected_total"`
			}
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return fmt.Errorf("seq %d: decode baseline.recorded: %w", e.Seq, err)
			}
			st.Baselines = append(st.Baselines, boundRec{Seq: e.Seq, Intent: body.Intent, Value: body.CollectedTotal})
		case evCorpusRecorded:
			var body struct {
				Intent string `json:"intent"`
				Cases  int64  `json:"cases"`
			}
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return fmt.Errorf("seq %d: decode corpus.recorded: %w", e.Seq, err)
			}
			st.Corpora = append(st.Corpora, boundRec{Seq: e.Seq, Intent: body.Intent, Value: body.Cases})
		}
		if scheduleEvents[e.Type] {
			st.Schedules = append(st.Schedules, scheduleEventRec{
				Seq: e.Seq, Type: e.Type, Payload: append([]byte(nil), e.Payload...),
			})
		}
		// Other event types (race.finished, attestation.recorded,
		// key.generated, publish.started, prune.executed) carry no state the
		// CLI views need.
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

// scheduleWindow returns the allocation-trace events recorded inside one
// race's ledger window (minSeq, maxSeq), in seq order. The window is the
// same one decision replay uses, because a workspace holds many races and a
// trace assembled across two of them would describe an allocation nobody
// made.
func (st *ledgerState) scheduleWindow(minSeq, maxSeq int64) []schedule.Event {
	out := make([]schedule.Event, 0, len(st.Schedules))
	for _, se := range st.Schedules {
		if se.Seq <= minSeq || (maxSeq > 0 && se.Seq >= maxSeq) {
			continue
		}
		out = append(out, schedule.Event{Type: se.Type, Payload: se.Payload})
	}
	return out
}

// lastBound returns the last control-plane measurement recorded for an
// intent inside a ledger window, and whether one exists at all. Absence is
// returned honestly rather than as a zero: a bound of 0 and "no bound was
// measured" are different facts, and only the second makes the bracket fail
// open.
func lastBound(recs []boundRec, intentDig string, minSeq, maxSeq int64) (int64, bool) {
	var v int64
	found := false
	for _, r := range recs {
		if r.Intent != intentDig || r.Seq <= minSeq {
			continue
		}
		if maxSeq > 0 && r.Seq >= maxSeq {
			continue
		}
		v, found = r.Value, true
	}
	return v, found
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
