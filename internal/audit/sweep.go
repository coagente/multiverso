// Package audit implements M1f's CAS integrity sweep: re-reading and
// re-hashing every object the ledger references, from a DECLARED table
// rather than a best-effort walk.
//
// The 2026-08 design partner study found `mvo audit` reporting OK after an
// attestation bundle had been deleted from CAS, and the quickstart
// claiming audit caught CAS edits it never inspected. Both are the same
// bug: audit verified the ledger's hash chain and the decision replay, and
// nothing at all about the blobs those records point at.
//
// What the sweep proves, and the limit stated next to it: it proves the
// RECORDED CLOSURE is intact and self-consistent. It cannot prove that
// something was recorded which never was. An adversary with write access
// to the ledger cannot forge the chain, but an operator who never ran an
// oracle has nothing for audit to miss.
package audit

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
)

// Ref is one referenced CAS object: the key, and who pointed at it. The
// referrer is carried so a failure names something an operator can act on
// rather than a bare digest.
type Ref struct {
	Key      string // "sha256:…"
	Referrer string // "attestation.recorded seq 29 (bundle)"

	// Within names a CAS object that CARRIES these bytes inline, for the one
	// reference whose target is not always a standalone blob.
	//
	// Pre-M1f `mvo admit` recorded attestation.recorded.statement WITHOUT
	// Put-ting the statement beside the bundle: the bytes live base64'd
	// inside the DSSE envelope and nowhere else. They are in the recorded
	// closure — they are simply nested — so a sweep that looks only for a
	// standalone blob accuses an intact M0–M1e workspace of having lost one,
	// which is the same over-claim M1f exists to remove, pointed the other
	// way. Resolving through the container is also strictly STRONGER than a
	// direct read: it proves the bundle's signed payload is the statement
	// the ledger names, which a standalone read never checked.
	Within string
}

// Problem is one swept object that did not check out.
type Problem struct {
	Key      string `json:"key"`
	Referrer string `json:"referrer"`
	Detail   string `json:"detail"` // "" for a missing object
}

// Report is the sweep's result.
type Report struct {
	Checked      int
	Missing      []Problem
	Corrupt      []Problem
	Unreferenced int
}

// OK reports whether the recorded closure is intact.
func (r Report) OK() bool { return len(r.Missing) == 0 && len(r.Corrupt) == 0 }

// eventRefs is the NORMATIVE referenced-set table (M1f). Anything not here
// is not swept, and Report.Checked says how many objects were examined so
// the claim is checkable.
//
//	every OBJECT event      the canonical bytes recorded under payload_dig
//	world.created           context, patch, trace, env
//	receipt.recorded        result.artifacts[*], execution.evidence_plugin
//	decision.recorded       evidence[*], policy, subject[*], intent
//	intent.created          policy
//	baseline.recorded       stdout, stderr, probe, evidence_stream
//	agent.finished          context, transcript, stderr
//	attestation.recorded    bundle, statement
//	policy.created          the payload's own digest
//
// Observational events whose payloads are not content-addressed
// (race.started, admission.*) contribute nothing: they are covered by the
// ledger's hash chain, which audit verifies separately. agent.finished is
// observational too, but it NAMES CAS blobs — and `stderr` is the only
// place a CONFIG_ERROR world's reason survives, so a sweep that skipped it
// would let the one record an operator needs be deleted under an `OK`.
var objectEvents = map[string]bool{
	"intent.created":    true,
	"world.created":     true,
	"receipt.recorded":  true,
	"decision.recorded": true,
	"policy.created":    true,
}

// Sweep collects the referenced set from the ledger, then re-reads and
// re-hashes every object in it.
func Sweep(led *ledger.Ledger, store *cas.Store) (Report, error) {
	refs, err := References(led)
	if err != nil {
		return Report{}, err
	}
	return Check(refs, store)
}

// References walks the ledger and returns the referenced set, deduplicated
// and sorted by key. The FIRST referrer of a key wins, so the reported
// blame is the earliest record that depended on the object.
func References(led *ledger.Ledger) ([]Ref, error) {
	seen := map[string]Ref{}
	order := []string{}
	addWithin := func(key, referrer, within string) {
		if key == "" {
			return // absence is legal: an artifact that was never produced
		}
		casKey := key
		if k, err := object.CASKey(key); err == nil {
			// mv0: object digests and sha256: artifact keys name the same
			// sha256, so the conversion is a prefix swap.
			casKey = k
		}
		if _, dup := seen[casKey]; dup {
			return
		}
		seen[casKey] = Ref{Key: casKey, Referrer: referrer, Within: within}
		order = append(order, casKey)
	}
	add := func(key, referrer string) { addWithin(key, referrer, "") }
	err := led.Scan(func(e ledger.Event) error {
		at := fmt.Sprintf("%s seq %d", e.Type, e.Seq)
		if objectEvents[e.Type] {
			add(e.PayloadDig, at+" (payload)")
		}
		switch e.Type {
		case "world.created":
			var w object.World
			if err := json.Unmarshal(e.Payload, &w); err != nil {
				return fmt.Errorf("audit: %s: %w", at, err)
			}
			add(w.Context, at+" (context)")
			add(w.Patch, at+" (patch)")
			add(w.Trace, at+" (trace)")
			add(w.Env, at+" (env)")
		case "receipt.recorded":
			var r object.Receipt
			if err := json.Unmarshal(e.Payload, &r); err != nil {
				return fmt.Errorf("audit: %s: %w", at, err)
			}
			for i, a := range r.Result.Artifacts {
				add(a, fmt.Sprintf("%s (result.artifacts[%d])", at, i))
			}
			// M1f decision 14 sells the plugin digest as "an auditable
			// fact, not a version guess". A fact nothing re-reads is a
			// version guess with a colon in it: race.setupEvidence and
			// admit.openLandingEvidence deliberately Put the plugin source
			// so that a receipt naming a digest always resolves to the
			// bytes that observed the run, and the sweep is what holds
			// them to it.
			add(r.Execution.EvidencePlugin, at+" (execution.evidence_plugin)")
		case "decision.recorded":
			var d object.Decision
			if err := json.Unmarshal(e.Payload, &d); err != nil {
				return fmt.Errorf("audit: %s: %w", at, err)
			}
			for i, ev := range d.Evidence {
				add(ev, fmt.Sprintf("%s (evidence[%d])", at, i))
			}
			add(d.Policy, at+" (policy)")
			for i, s := range d.Subject {
				add(s, fmt.Sprintf("%s (subject[%d])", at, i))
			}
			add(d.Intent, at+" (intent)")
		case "intent.created":
			var in object.Intent
			if err := json.Unmarshal(e.Payload, &in); err != nil {
				return fmt.Errorf("audit: %s: %w", at, err)
			}
			add(in.Policy, at+" (policy)")
		case "baseline.recorded":
			// evidence_stream joins the three M1e artifacts: the baseline
			// collect run streams like any other, and collected_base is
			// the one number a `collected-not-below` gate divides by. Its
			// observation record belongs inside the audited closure like
			// every candidate's. Absent ("" on an M1e ledger, or under the
			// in-tree regime) adds nothing — absence is legal.
			for _, field := range []string{"stdout", "stderr", "probe", "evidence_stream"} {
				add(stringField(e.Payload, field), at+" ("+field+")")
			}
		case "agent.finished":
			// context and transcript are already named by world.created;
			// dedup collapses the overlap. stderr is named nowhere else,
			// and it is where a CONFIG_ERROR world's reason lives.
			for _, field := range []string{"context", "transcript", "stderr"} {
				add(stringField(e.Payload, field), at+" ("+field+")")
			}
		case "attestation.recorded":
			bundle := stringField(e.Payload, "bundle")
			add(bundle, at+" (bundle)")
			// The statement is resolvable two ways (see Ref.Within): as its
			// own blob since M1f, and inside the bundle's DSSE payload in
			// every ledger ever written.
			addWithin(stringField(e.Payload, "statement"), at+" (statement)", bundle)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(order)
	out := make([]Ref, 0, len(order))
	for _, key := range order {
		out = append(out, seen[key])
	}
	return out, nil
}

// Check re-reads and re-hashes every reference and counts what CAS holds
// beyond them.
//
//   - missing: referenced and absent ⇒ a failure. This is the attestation
//     blob the study found audit reporting OK over.
//   - corrupt: present, but the stored bytes hash to something else ⇒ a
//     failure. This is the CAS edit the quickstart claimed audit caught.
//   - unreferenced: present, never referenced ⇒ COUNTED, never failed.
func Check(refs []Ref, store *cas.Store) (Report, error) {
	rep := Report{Missing: []Problem{}, Corrupt: []Problem{}}
	referenced := make(map[string]bool, len(refs))
	for _, ref := range refs {
		referenced[ref.Key] = true
		rep.Checked++
		if !store.Has(ref.Key) {
			// Not a standalone blob. It is still in the recorded closure if
			// its declared container carries it — the pre-M1f attestation
			// shape (Ref.Within). Re-hashing the extracted bytes is the same
			// integrity test the standalone path applies, so nothing is
			// waved through: a container that is absent, unparseable, or
			// carrying different bytes leaves the reference MISSING.
			if carriedBy(store, ref) {
				continue
			}
			rep.Missing = append(rep.Missing, Problem{Key: ref.Key, Referrer: ref.Referrer})
			continue
		}
		// Get re-hashes the stored bytes against the key it was asked for,
		// so a flipped byte surfaces here and never reaches a consumer.
		if _, err := store.Get(ref.Key); err != nil {
			rep.Corrupt = append(rep.Corrupt, Problem{
				Key: ref.Key, Referrer: ref.Referrer, Detail: err.Error(),
			})
		}
	}
	present, err := store.Keys()
	if err != nil {
		return rep, err
	}
	for _, key := range present {
		if !referenced[key] {
			rep.Unreferenced++
		}
	}
	return rep, nil
}

// carriedBy reports whether ref.Within resolves to a DSSE envelope whose
// base64 payload hashes to ref.Key. It is deliberately narrow: one shape,
// one comparison, no recursion and no guessing. A false here means the
// bytes are not in the recorded closure at all, which is a real failure.
func carriedBy(store *cas.Store, ref Ref) bool {
	if ref.Within == "" {
		return false
	}
	// Get re-hashes the container against its own key, so a corrupt bundle
	// can never be the thing that vouches for a statement.
	raw, err := store.Get(ref.Within)
	if err != nil {
		return false
	}
	var env struct {
		Payload string `json:"payload"` // base64.StdEncoding, DSSE
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Payload == "" {
		return false
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(payload)
	return "sha256:"+hex.EncodeToString(sum[:]) == ref.Key
}

// stringField pulls one top-level string out of an observational event's
// canonical JSON payload. A field that is absent or not a string yields ""
// — absence is legal, and a sweep must never invent a reference.
func stringField(payload []byte, name string) string {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(payload, &body); err != nil {
		return ""
	}
	raw, ok := body[name]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}
