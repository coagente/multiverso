package oracle

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CaseObservation is what one world did with one corpus case.
//
// FP is the case fingerprint (mvo-value/v0), and it is the ONLY thing the
// reducer compares. Value is the encoded value itself, carried for the
// human-facing report and only when its encoding is at most 512 bytes;
// otherwise Truncated is set and the fingerprint stands alone. A
// maintainer needs to see `clamp(nan, 0, 10) → nan` versus `→ 0`; nobody
// needs to see a megabyte of it.
type CaseObservation struct {
	ID        string
	Outcome   string // OutcomeValue | OutcomeRaise | OutcomeOpaque | OutcomeError | OutcomeTimeout
	FP        string // "sha256:…"; "" for opaque, error and timeout
	Value     json.RawMessage
	Type      string // exception qualname (raise) or Python type name (opaque)
	Truncated bool
}

// Comparable reports whether this observation can be compared with another
// world's. Only `value` and `raise` are: an opaque observation is the
// encoding admitting it could not represent what it saw, and TWO OPAQUE
// OBSERVATIONS ARE NOT EQUAL (M2a decision 6). Absence of comparability is
// not agreement, and a differential that quietly treated it as agreement
// would be manufacturing consensus out of ignorance.
func (o CaseObservation) Comparable() bool {
	return (o.Outcome == OutcomeValue || o.Outcome == OutcomeRaise) && o.FP != ""
}

// Observation is one world's parsed corpus run.
//
// Usable == false means every corpus_* and diff_* metric is ABSENT for this
// world, which fails its own corpus-complete gate. That is the whole of
// corpus vector 18 (cohort starvation): a world that silences the runner to
// shrink the cohort and move a rival's unilateral counts eliminates ITSELF,
// and diff_cohort_n records the shrinkage so every surviving number still
// has a named denominator.
//
// "A world can only remove itself" was written here as a flat claim and it
// was not one. Three ways round it have been executed, and each is now
// closed by a named mechanism rather than by this sentence:
//
//   - a USABLE BUT SHORT observation used to enter the cohort while failing
//     its own gate, and the intersection denominator then dropped the
//     distinguishing case for every honest sibling — the cohort now filters
//     on the observe receipt's verdict (internal/race.cohort);
//   - a member that answered `opaque` or `error` EVERYWHERE passed its own
//     gate and collapsed the denominator to zero for the whole race — a
//     zero-contribution member is now excluded and the exclusion recorded
//     (oracle.Reduce);
//   - one shared corpus file let a candidate rewrite the inputs a SIBLING
//     would replay — the corpus is now delivered per world, re-digested by
//     the control plane either side of every replay, and bound to the bytes
//     it was loaded from by the fourth usability rule below.
//
// What remains, and is recorded as an open vector rather than described as
// closed: a member that contributes SOME comparable cases still shrinks the
// denominator for every other member, and at T0 the corpus copies are owned
// by the uid the candidate's own process runs as.
type Observation struct {
	Raw      []byte
	Usable   bool
	Complete bool
	Reason   string
	Notes    string

	// CorpusDigest is the digest the RUNNER computed over the corpus bytes
	// it actually loaded, as reported in session_start. It is kept even
	// when it matched, because "which corpus did this world replay" is a
	// question the recorded stream must answer on its own.
	CorpusDigest string

	Cases   map[string]CaseObservation
	Order   []string // declared-case order, for deterministic iteration
	Opaque  int64
	Errored int64
	// Finish tallies, as the runner reported them. They are kept beside
	// the per-case derivation rather than trusted in place of it, for the
	// same reason M1f keeps session_finish's totals beside the per-nodeid
	// derivation: a disagreement should be visible, not reconciled.
	FinishCases, FinishOpaque, FinishErrored int64
	DurationMS                               int64
	ExitStatus                               int
}

// ObservedCount is the number of DECLARED cases this world produced a
// record for — corpus_cases_observed, and the numerator of the
// corpus-complete gate.
func (o Observation) ObservedCount() int64 { return int64(len(o.Cases)) }

// Get returns the observation of one case.
func (o Observation) Get(id string) (CaseObservation, bool) {
	c, ok := o.Cases[id]
	return c, ok
}

// ParseObservation applies the M1f framing rules plus M2a's four
// additional USABILITY rules, and derives the corpus metrics. It is PURE:
// no I/O, no clock — the recorded fixtures in testdata/corpus/ drive it
// directly, with no Python and no world.
//
// The additional rules, and why each is a whole-observation kill rather
// than a per-record skip:
//
//	a session_finish is present  — a run that never said it finished may
//	                               have been killed mid-corpus, so no
//	                               tally derived from it is admissible;
//	every case id is DECLARED    — a world that reports on cases the
//	                               corpus does not contain has told us it
//	                               is not running our corpus (vector 17);
//	no id repeats                — two answers for one input is not an
//	                               observation, it is a choice, and the
//	                               control plane does not get to make it;
//	the corpus digest MATCHES    — the runner hashes the bytes it actually
//	                               loaded and reports them; a stream whose
//	                               session_start names a different corpus
//	                               than the pinned one replayed something
//	                               else, whatever it says about the cases.
//
// The fourth rule is the one that makes a swapped corpus self-reporting
// rather than deniable. The corpus is one file on disk, and at T0 it is
// owned by the uid the candidate's own process runs as; a world that
// rewrote it poisoned the NEXT world's replay and never its own
// observation, so the victim was the one convicted — its stream faithfully
// reported a corpus nobody pinned. Binding the observation to the bytes
// behind it moves the evidence back onto the run that produced it. The
// delivery-side half of the same finding is per-world corpus copies plus
// the control plane's own before/after digest check, which aborts the race
// as machinery rather than failing anyone.
//
// corpusDigest is the pinned "mv0:…". Empty means the caller has no pinning
// to check against — the pure-parser tests' shape — and the rule is skipped
// rather than failed, because refusing a binding nobody offered would fail
// on the control plane's own absence rather than on the candidate's.
func ParseObservation(raw []byte, nonce string, over bool, corpus Corpus, corpusDigest string) Observation {
	o := Observation{Raw: raw, Cases: map[string]CaseObservation{}, Order: []string{}}
	f := parseFrames(raw, nonce, over, obsKinds)
	if f.reason != "" {
		o.Reason = f.reason
		return o
	}
	o.Notes = f.notes

	seen := map[string]bool{}
	for _, r := range f.recs {
		switch r.kind {
		case recSessionStart:
			var p struct {
				Corpus string `json:"corpus"`
			}
			if json.Unmarshal(r.payload, &p) != nil {
				return unusableObservation(o, fmt.Sprintf(
					"observation has a malformed session_start record at seq %d", r.seq))
			}
			o.CorpusDigest = p.Corpus
			if corpusDigest != "" && p.Corpus != corpusDigest {
				return unusableObservation(o, fmt.Sprintf(
					"observation replayed corpus %s, but %s was pinned for this race",
					dashDigest(p.Corpus), corpusDigest))
			}
		case recCase:
			var p struct {
				FP        string          `json:"fp"`
				ID        string          `json:"id"`
				Outcome   string          `json:"outcome"`
				T         string          `json:"t"`
				Truncated bool            `json:"truncated"`
				V         json.RawMessage `json:"v"`
			}
			if err := json.Unmarshal(r.payload, &p); err != nil || p.ID == "" {
				return unusableObservation(o, fmt.Sprintf(
					"observation has a malformed case record at seq %d", r.seq))
			}
			if !corpus.Declares(p.ID) {
				return unusableObservation(o, fmt.Sprintf(
					"observation reports case %q, which corpus %s does not declare", p.ID, corpusName(corpus)))
			}
			if seen[p.ID] {
				return unusableObservation(o, fmt.Sprintf("observation reports case %q twice", p.ID))
			}
			seen[p.ID] = true
			o.Order = append(o.Order, p.ID)
			o.Cases[p.ID] = CaseObservation{
				ID: p.ID, Outcome: p.Outcome, FP: p.FP, Value: p.V, Type: p.T, Truncated: p.Truncated,
			}
			switch p.Outcome {
			case OutcomeOpaque:
				o.Opaque++
			case OutcomeError, OutcomeTimeout:
				o.Errored++
			}
		case recSessionFinish:
			var p struct {
				Cases      int64 `json:"cases"`
				DurationMS int64 `json:"duration_ms"`
				Errored    int64 `json:"errored"`
				ExitStatus int   `json:"exitstatus"`
				Opaque     int64 `json:"opaque"`
			}
			if json.Unmarshal(r.payload, &p) != nil {
				continue
			}
			o.Complete = true
			o.FinishCases, o.FinishOpaque, o.FinishErrored = p.Cases, p.Opaque, p.Errored
			o.DurationMS, o.ExitStatus = p.DurationMS, p.ExitStatus
		}
	}
	if !o.Complete {
		// Rule 7, applied exactly as the suite reader applies it: the
		// derived fields are DROPPED rather than reported alongside
		// "incomplete", because a number a caller could still read is a
		// number a caller will eventually read.
		raw, notes := o.Raw, o.Notes
		o = Observation{Raw: raw, Notes: notes, Cases: map[string]CaseObservation{}, Order: []string{}}
		o.Reason = "evidence stream has no session_finish"
		return o
	}
	sort.Strings(o.Order)
	o.Usable = true
	return o
}

// unusableObservation drops everything derived so far and returns the
// reason. Nothing partial survives: a caller that could still read a case
// out of an unusable observation is a caller that eventually will, and the
// honesty rule is that an unusable source yields NO metrics rather than
// some of them.
func unusableObservation(o Observation, reason string) Observation {
	return Observation{
		Raw: o.Raw, Notes: o.Notes, Reason: reason,
		Cases: map[string]CaseObservation{}, Order: []string{},
	}
}

// dashDigest renders a corpus digest a stream did not carry. An empty
// string in a failure sentence reads as a formatting bug; "(none)" reads as
// what it is — a runner that never said which bytes it loaded.
func dashDigest(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// corpusName renders a corpus for a parse error. It has no digest to hand
// — the parser is pure and the digest is the caller's — so it names what it
// can: the provider and the case count.
func corpusName(c Corpus) string {
	return fmt.Sprintf("%s/%d cases", c.Provider, len(c.Cases))
}
