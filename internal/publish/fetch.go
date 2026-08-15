package publish

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/coagente/multiverso/internal/admit"
	"github.com/coagente/multiverso/internal/attest"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/signing"
)

// FetchConfig wires one consumer-side fetch + verification. Workspace-less
// and ledger-less (M1d decision 13): any clone with the remote configured;
// the trust root is the explicit public key.
type FetchConfig struct {
	Repo   string            // any clone with Remote configured; no workspace needed
	Remote string            //
	Short  string            // 12-hex namespace (the CLI shortens a full digest)
	Pub    ed25519.PublicKey // trusted key (decision 13)
}

// ItemReport is one verification unit's outcome.
type ItemReport struct {
	Path string `json:"path"`
	OK   bool   `json:"ok"`
	Err  string `json:"error"`
}

// WorldRow is one line of the fetch-race world table (display only).
type WorldRow struct {
	Ordinal int // 0 when no candidate ref publishes the world
	Dig     string
	Outcome string
	Gate    string // "pass" | "fail" | "error" | "-" (no suite receipt)
	Signed  int    // verified receipt envelopes naming the world
	Ref     string // "cand/<n>" | "-"
}

// Report is the fetch-race verification report. OK is true iff every item
// (evidence file and namespace ref) verified.
type Report struct {
	Short, IntentDig, Title, SelectDig, DecisionType, Winner string
	Admitted                                                 bool
	Freshness, FreshnessDetail                               string
	Refs                                                     []RefTip     // fetched namespace
	Items                                                    []ItemReport // path-sorted + per-ref entries
	Worlds                                                   []WorldRow   // display table rows
	OK                                                       bool
}

// verifier collects failures per path — collected, never fail-fast (M1d
// decision 14): a tampered file must be loud AND specific.
type verifier struct {
	seen  map[string]bool
	fails map[string][]string
}

func newVerifier() *verifier {
	return &verifier{seen: map[string]bool{}, fails: map[string][]string{}}
}

func (v *verifier) item(path string) { v.seen[path] = true }

func (v *verifier) fail(path, format string, a ...any) {
	v.seen[path] = true
	v.fails[path] = append(v.fails[path], fmt.Sprintf(format, a...))
}

func (v *verifier) report() ([]ItemReport, bool) {
	paths := sortedKeys(v.seen)
	items := make([]ItemReport, 0, len(paths))
	ok := true
	for _, p := range paths {
		errs := v.fails[p]
		it := ItemReport{Path: p, OK: len(errs) == 0, Err: strings.Join(errs, "; ")}
		if !it.OK {
			ok = false
		}
		items = append(items, it)
	}
	return items, ok
}

// FetchRace fetches the intent namespace and verifies it end-to-end:
// authenticate every item, close the cross-reference graph, replay every
// decision through the shipped pure Decide functions (M1d decision 14).
// The error return is reserved for machinery — unreachable remote, no
// namespace at all; verification failures live in the Report.
func FetchRace(cfg FetchConfig) (*Report, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	ns := Namespace(cfg.Short)

	// Mirror the namespace exactly (+prune) — required for the
	// unplanned-ref check to be sound.
	refspec := "+" + ns + "/*:" + ns + "/*"
	if err := gitx.Fetch(cfg.Repo, cfg.Remote, []string{refspec}, true); err != nil {
		return nil, fmt.Errorf("publish: fetch-race: %w", err)
	}
	refs, err := gitx.ForEachRef(cfg.Repo, ns)
	if err != nil {
		return nil, fmt.Errorf("publish: fetch-race: %w", err)
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("publish: fetch-race: no refs under %s on remote %s (nothing published for this intent?)", ns, cfg.Remote)
	}

	rep := &Report{Short: cfg.Short, Freshness: DriftUnknown, FreshnessDetail: "intent not verified"}
	for _, ref := range sortedKeys(refs) {
		rep.Refs = append(rep.Refs, RefTip{Ref: ref, Tip: refs[ref]})
	}
	v := newVerifier()
	for _, rt := range rep.Refs {
		v.item(rt.Ref)
	}

	evRef := EvidenceRef(cfg.Short)
	evTip, hasEv := refs[evRef]
	if !hasEv {
		v.fail(evRef, "evidence ref missing from the namespace")
		rep.Items, rep.OK = v.report()
		return rep, nil
	}

	// --- Per-file authentication over the evidence tree -----------------
	entries, err := gitx.LsTreeRecursive(cfg.Repo, evTip)
	if err != nil {
		return nil, fmt.Errorf("publish: fetch-race: %w", err)
	}
	var (
		intents      = map[string]object.Intent{}
		intentPaths  = map[string]string{}
		policies     = map[string][]byte{}
		policyPaths  = map[string]string{}
		worlds       = map[string]object.World{}
		worldPaths   = map[string]string{}
		decisions    = map[string]object.Decision{}
		decPaths     = map[string]string{}
		receipts     = map[string]object.Receipt{}
		receiptPaths = map[string]string{}
	)
	type attRec struct {
		path string
		stmt attest.Statement
	}
	var atts []attRec
	for _, e := range entries {
		path := e.Name
		v.item(path)
		dir, file, cut := strings.Cut(path, "/")
		if !cut || strings.Contains(file, "/") {
			v.fail(path, "not a <kind>/<file> path")
			continue
		}
		b, err := gitx.CatBlob(cfg.Repo, e.SHA)
		if err != nil {
			v.fail(path, "read blob: %v", err)
			continue
		}
		id, dsse, err := ParseFileName(file)
		if err != nil {
			v.fail(path, "%v", err)
			continue
		}
		switch dir {
		case "intent", "policy", "worlds":
			if dsse {
				v.fail(path, "plain kind %q named as a DSSE envelope", dir)
				continue
			}
			if got := object.DigestBytes(b); got != id {
				v.fail(path, "content digests to %s, filename claims %s", got, id)
				continue
			}
			switch dir {
			case "intent":
				var in object.Intent
				if err := decodeSchema(b, &in, object.SchemaIntent, func() string { return in.Schema }); err != nil {
					v.fail(path, "%v", err)
					continue
				}
				intents[id], intentPaths[id] = in, path
			case "policy":
				var pol object.Policy
				if err := decodeSchema(b, &pol, object.SchemaPolicy, func() string { return pol.Schema }); err != nil {
					v.fail(path, "%v", err)
					continue
				}
				policies[id], policyPaths[id] = b, path
			case "worlds":
				var w object.World
				if err := decodeSchema(b, &w, object.SchemaWorld, func() string { return w.Schema }); err != nil {
					v.fail(path, "%v", err)
					continue
				}
				worlds[id], worldPaths[id] = w, path
			}
		case "decisions":
			if !dsse {
				v.fail(path, "decisions must be DSSE envelopes")
				continue
			}
			payload, err := OpenItem(b, PayloadTypeDecision, cfg.Pub)
			if err != nil {
				v.fail(path, "%v", err)
				continue
			}
			if got := object.DigestBytes(payload); got != id {
				v.fail(path, "payload digests to %s, filename claims %s", got, id)
				continue
			}
			var d object.Decision
			if err := decodeSchema(payload, &d, object.SchemaDecision, func() string { return d.Schema }); err != nil {
				v.fail(path, "%v", err)
				continue
			}
			decisions[id], decPaths[id] = d, path
		case "receipts":
			if !dsse {
				v.fail(path, "receipts must be DSSE envelopes")
				continue
			}
			payload, err := OpenItem(b, PayloadTypeReceipt, cfg.Pub)
			if err != nil {
				v.fail(path, "%v", err)
				continue
			}
			if got := object.DigestBytes(payload); got != id {
				v.fail(path, "payload digests to %s, filename claims %s", got, id)
				continue
			}
			var r object.Receipt
			if err := decodeSchema(payload, &r, object.SchemaReceipt, func() string { return r.Schema }); err != nil {
				v.fail(path, "%v", err)
				continue
			}
			receipts[id], receiptPaths[id] = r, path
		case "attestation":
			if !dsse {
				v.fail(path, "the attestation must be a DSSE bundle")
				continue
			}
			sum := sha256.Sum256(b)
			if got := "sha256:" + hex.EncodeToString(sum[:]); got != id {
				v.fail(path, "bundle hashes to %s, filename claims %s", got, id)
				continue
			}
			var env signing.Envelope
			if err := json.Unmarshal(b, &env); err != nil {
				v.fail(path, "decode envelope: %v", err)
				continue
			}
			if env.PayloadType != signing.PayloadTypeInToto {
				v.fail(path, "payloadType %q, want %q", env.PayloadType, signing.PayloadTypeInToto)
				continue
			}
			payload, err := signing.Verify(env, cfg.Pub)
			if err != nil {
				v.fail(path, "%v", err)
				continue
			}
			var stmt attest.Statement
			if err := json.Unmarshal(payload, &stmt); err != nil {
				v.fail(path, "decode statement: %v", err)
				continue
			}
			if stmt.Type != attest.StatementType {
				v.fail(path, "_type %q, want %q", stmt.Type, attest.StatementType)
				continue
			}
			if stmt.PredicateType != attest.PredicateType {
				v.fail(path, "predicateType %q, want %q", stmt.PredicateType, attest.PredicateType)
				continue
			}
			atts = append(atts, attRec{path: path, stmt: stmt})
		default:
			v.fail(path, "unknown evidence kind %q", dir)
		}
	}

	// --- Closure graph ---------------------------------------------------
	var intentDig string
	var intent object.Intent
	if len(intents) == 1 {
		for d, in := range intents {
			intentDig, intent = d, in
		}
		if short, err := IntentShort(intentDig); err != nil || short != cfg.Short {
			v.fail(intentPaths[intentDig], "intent digest %s does not shorten to namespace %s", intentDig, cfg.Short)
		}
		rep.IntentDig, rep.Title = intentDig, intent.Spec.Title
	} else {
		v.fail(evRef, "evidence tree holds %d intent files, want exactly one", len(intents))
	}

	var sel, admitDec *object.Decision
	var selDig, admitDigest string
	for _, dig := range sortedKeys(decisions) {
		d := decisions[dig]
		path := decPaths[dig]
		switch d.Type {
		case "SELECT":
			if sel != nil {
				v.fail(path, "second SELECT decision in the closure")
				continue
			}
			c := d
			sel, selDig = &c, dig
		case admit.TypeAdmit:
			if admitDec != nil {
				v.fail(path, "second ADMIT decision in the closure")
				continue
			}
			c := d
			admitDec, admitDigest = &c, dig
		default:
			v.fail(path, "decision type %q does not belong in a published closure", d.Type)
			continue
		}
		if intentDig != "" && d.Intent != intentDig {
			v.fail(path, "decision intent %s, closure intent %s", d.Intent, intentDig)
		}
		if intentDig != "" && d.Policy != intent.Policy {
			v.fail(path, "decision policy %s, intent policy %s", d.Policy, intent.Policy)
		}
		if _, ok := policies[d.Policy]; !ok {
			v.fail(path, "policy %s not in the closure", d.Policy)
		}
		for _, s := range d.Subject {
			if _, ok := worlds[s]; !ok {
				v.fail(path, "subject world %s not in the closure", s)
			}
		}
		for _, e := range d.Evidence {
			if _, ok := receipts[e]; !ok {
				v.fail(path, "evidence receipt %s not in the closure", e)
			}
		}
	}
	if sel == nil {
		v.fail(evRef, "no SELECT decision in the closure")
	} else {
		rep.SelectDig, rep.DecisionType = selDig, sel.Type
	}
	rep.Admitted = admitDec != nil
	for _, dig := range sortedKeys(receipts) {
		if _, ok := worlds[receipts[dig].World]; !ok {
			v.fail(receiptPaths[dig], "receipt world %s not in the closure", receipts[dig].World)
		}
	}
	// Reverse containment binds the plain kinds — the only ones
	// authenticated by digest alone, where a self-consistent forgery is
	// free to mint — to the signed decisions. Forward containment (every
	// subject world is present) is not enough: without this, anyone with
	// push access could splice an extra World naming attacker-authored
	// code into the evidence commit and publish it under a candidate ref,
	// and the tool would call it verified.
	cited := map[string]bool{}
	for _, d := range []*object.Decision{sel, admitDec} {
		if d == nil {
			continue
		}
		for _, s := range d.Subject {
			cited[s] = true
		}
	}
	for _, dig := range sortedKeys(worlds) {
		if intentDig != "" && worlds[dig].Intent != intentDig {
			v.fail(worldPaths[dig], "world intent %s, closure intent %s", worlds[dig].Intent, intentDig)
		}
		if sel != nil && !cited[dig] {
			v.fail(worldPaths[dig], "world %s is not a subject of any signed decision in the closure", dig)
		}
	}
	for _, dig := range sortedKeys(policies) {
		if intentDig != "" && dig != intent.Policy {
			v.fail(policyPaths[dig], "policy %s is not the intent's policy %s", dig, intent.Policy)
		}
	}

	// --- Candidate refs --------------------------------------------------
	winner := ""
	if sel != nil && len(sel.Subject) > 0 {
		winner = sel.Subject[0]
		rep.Winner = winner
	}
	type candInfo struct {
		ordinal int
		ref     string
	}
	candByWorld := map[string]candInfo{}
	for _, rt := range rep.Refs {
		if rt.Ref == evRef {
			continue
		}
		rest, cut := strings.CutPrefix(rt.Ref, ns+"/cand/")
		n, aerr := strconv.Atoi(rest)
		// The ordinal must be its canonical decimal rendering: strconv.Atoi
		// also accepts "01" and "+1", which no plan can ever emit, and an
		// alias ref would otherwise slip past the "namespace ⊆ plan"
		// invariant that makes any unplanned ref tamper evidence (decision
		// 10).
		if !cut || aerr != nil || n < 1 || strconv.Itoa(n) != rest {
			v.fail(rt.Ref, "unplanned ref in the namespace (neither evidence nor cand/<n>)")
			continue
		}
		worldDig := verifyCandidateRef(v, cfg.Repo, rt.Ref, rt.Tip, n, intentDig, intent, worlds)
		if worldDig == "" {
			continue
		}
		// One world, one candidate ref: an ordinal is the world's position
		// in the race window, so no plan can publish a world twice. A
		// second ref over the same world is unplanned by construction —
		// the same soundness argument as the alias-ordinal check.
		if prev, dup := candByWorld[worldDig]; dup {
			v.fail(rt.Ref, "world %s is already published by %s — one world, one candidate ref", worldDig, prev.ref)
			continue
		}
		candByWorld[worldDig] = candInfo{ordinal: n, ref: rt.Ref}
	}
	if winner != "" {
		if _, ok := candByWorld[winner]; !ok {
			// Publishing code you can't fetch is the failure, not a variant.
			v.fail(ns, "no candidate ref publishes the SELECT winner %s", winner)
		}
	}

	// --- Replay (the same pure Decide functions — decision 14) -----------
	if sel != nil && intentDig != "" {
		if polBytes, ok := policies[sel.Policy]; ok {
			var pol object.Policy
			if json.Unmarshal(polBytes, &pol) == nil {
				replayWorlds := make([]object.World, 0, len(sel.Subject))
				complete := true
				for _, s := range sel.Subject {
					w, ok := worlds[s]
					if !ok {
						complete = false
						break
					}
					replayWorlds = append(replayWorlds, w)
				}
				replayReceipts := make([]object.Receipt, 0, len(sel.Evidence))
				for _, e := range sel.Evidence {
					r, ok := receipts[e]
					if !ok {
						complete = false
						break
					}
					replayReceipts = append(replayReceipts, r)
				}
				if complete {
					got := race.Decide(pol, replayWorlds, replayReceipts)
					if detail := diffDecision(*sel, got); detail != "" {
						v.fail(decPaths[selDig], "SELECT replay mismatch (%s)", detail)
					}
				}
				if admitDec != nil && winner != "" {
					replayAdmit(v, decPaths[admitDigest], pol, *admitDec, winner, receipts)
				}
			}
		}
	}

	// --- Attestation (admitted only; offline-without-the-commit, decision
	// 15: full trailer/tree/parent anchoring stays mvo verify's job) ------
	if admitDec == nil {
		for _, a := range atts {
			v.fail(a.path, "attestation present but no ADMIT decision in the closure")
		}
	} else if len(atts) == 0 {
		v.fail(decPaths[admitDigest], "admitted closure has no attestation bundle")
	} else if len(atts) > 1 {
		for _, a := range atts[1:] {
			v.fail(a.path, "more than one attestation bundle in the closure")
		}
	} else {
		verifyAttestation(v, atts[0].path, atts[0].stmt, intentDig, worlds, policies, decisions, receipts, cfg.Pub)
	}

	// --- World table + freshness -----------------------------------------
	if sel != nil {
		for _, dig := range sel.Subject {
			row := WorldRow{Dig: dig, Outcome: "?", Gate: "-", Ref: "-"}
			if w, ok := worlds[dig]; ok {
				row.Outcome = w.Outcome
			}
			if info, ok := candByWorld[dig]; ok {
				row.Ordinal = info.ordinal
				row.Ref = fmt.Sprintf("cand/%d", info.ordinal)
			}
			gateDig := ""
			for _, e := range sel.Evidence {
				r, ok := receipts[e]
				if !ok || r.World != dig || r.Family != "suite" {
					continue
				}
				if gateDig == "" || e < gateDig {
					gateDig = e
					row.Gate = r.Result.Status
				}
			}
			for _, rdig := range sortedKeys(receipts) {
				if receipts[rdig].World == dig {
					row.Signed++
				}
			}
			rep.Worlds = append(rep.Worlds, row)
		}
		sort.Slice(rep.Worlds, func(i, j int) bool {
			a, b := rep.Worlds[i], rep.Worlds[j]
			ao, bo := a.Ordinal, b.Ordinal
			if ao == 0 {
				ao = 1 << 30
			}
			if bo == 0 {
				bo = 1 << 30
			}
			if ao != bo {
				return ao < bo
			}
			return a.Dig < b.Dig
		})
	}
	if intentDig != "" {
		rep.Freshness, rep.FreshnessDetail = TrunkDrift(cfg.Repo, intent.Base.Commit)
	}

	rep.Items, rep.OK = v.report()
	return rep, nil
}

// verifyCandidateRef runs the M1d check-4 list on one cand/<n> commit and
// returns the world digest its trailer names ("" when the trailer itself
// failed). Failures collect on the ref.
func verifyCandidateRef(v *verifier, repo, ref, tip string, n int,
	intentDig string, intent object.Intent, worlds map[string]object.World) string {
	parent, err := gitx.ParentOf(repo, tip)
	if err != nil {
		v.fail(ref, "commit parent: %v", err)
		return ""
	}
	if intentDig != "" && parent != intent.Base.Commit {
		v.fail(ref, "commit parent %s, intent base %s", parent, intent.Base.Commit)
	}
	msg, err := gitx.CommitMessage(repo, tip)
	if err != nil {
		v.fail(ref, "commit message: %v", err)
		return ""
	}
	if got := trailerValue(msg, "Multiverso-Schema"); got != "multiverso.dev/candidate-ref/v0" {
		v.fail(ref, "Multiverso-Schema trailer %q, want %q", got, "multiverso.dev/candidate-ref/v0")
	}
	if intentDig != "" {
		if got := trailerValue(msg, "Multiverso-Intent"); got != intentDig {
			v.fail(ref, "Multiverso-Intent trailer %q, want %q", got, intentDig)
		}
	}
	if got := trailerValue(msg, "Multiverso-Ordinal"); got != strconv.Itoa(n) {
		v.fail(ref, "Multiverso-Ordinal trailer %q, ref ordinal %d", got, n)
	}
	worldDig := trailerValue(msg, "Multiverso-World")
	world, ok := worlds[worldDig]
	if !ok {
		v.fail(ref, "Multiverso-World trailer %q is not a published world", worldDig)
		return ""
	}
	tree, err := gitx.TreeOf(repo, tip)
	if err != nil {
		v.fail(ref, "commit tree: %v", err)
		return worldDig
	}
	if tree != world.Tree {
		v.fail(ref, "commit tree %s, world tree %s", tree, world.Tree)
	}
	return worldDig
}

// replayAdmit reproduces the published ADMIT through admit.Decide (M1a
// rules: apply = smallest-digest landing-apply receipt, gate =
// smallest-digest suite receipt among the ADMIT evidence).
func replayAdmit(v *verifier, path string, pol object.Policy, dec object.Decision,
	winner string, receipts map[string]object.Receipt) {
	evidence := append([]string(nil), dec.Evidence...)
	sort.Strings(evidence)
	var apply, gate *object.Receipt
	for _, dig := range evidence {
		r, ok := receipts[dig]
		if !ok {
			return // graph check already failed; replay would only add noise
		}
		if r.Oracle.ID == admit.OracleIDLandingApply && apply == nil {
			c := r
			apply = &c
		}
		if r.Family == "suite" && gate == nil {
			c := r
			gate = &c
		}
	}
	if apply == nil {
		v.fail(path, "no landing-apply receipt among the ADMIT evidence")
		return
	}
	got := admit.Decide(pol, dec.Intent, winner, *apply, gate)
	if detail := diffDecision(dec, got); detail != "" {
		v.fail(path, "ADMIT replay mismatch (%s)", detail)
	}
}

// verifyAttestation runs the M1d check-6 list: every predicate digest
// resolves within the published closure, the producer key matches the
// trust root, the budget sums, and the statement's subject gitTree equals
// the landing suite receipt's valid_for tree — the offline substitute for
// M1a's commit-anchored subject check.
func verifyAttestation(v *verifier, path string, stmt attest.Statement, intentDig string,
	worlds map[string]object.World, policies map[string][]byte,
	decisions map[string]object.Decision, receipts map[string]object.Receipt,
	pub ed25519.PublicKey) {
	pred := stmt.Predicate
	if intentDig != "" && pred.Intent != intentDig {
		v.fail(path, "predicate intent %s, closure intent %s", pred.Intent, intentDig)
	}
	if _, ok := worlds[pred.World]; !ok {
		v.fail(path, "predicate world %s not in the closure", pred.World)
	}
	if _, ok := policies[pred.Policy]; !ok {
		v.fail(path, "predicate policy %s not in the closure", pred.Policy)
	}
	if _, ok := decisions[pred.Decision]; !ok {
		v.fail(path, "predicate decision %s not in the closure", pred.Decision)
	}
	if _, ok := decisions[pred.SelectDecision]; !ok {
		v.fail(path, "predicate select_decision %s not in the closure", pred.SelectDecision)
	}
	if want := signing.KeyID(pub); pred.ProducerKeyID != want {
		v.fail(path, "predicate producer_key_id %s, trusted key %s", pred.ProducerKeyID, want)
	}
	var sum int64
	var gateDig string
	var gate *object.Receipt
	complete := true
	for _, dig := range pred.Evidence {
		r, ok := receipts[dig]
		if !ok {
			v.fail(path, "predicate evidence %s not in the closure", dig)
			complete = false
			continue
		}
		sum += r.Cost.WallMS
		if r.Family == "suite" && (gateDig == "" || dig < gateDig) {
			c := r
			gateDig, gate = dig, &c
		}
	}
	if complete && pred.BudgetConsumed.WallMS != sum {
		v.fail(path, "predicate wall_ms %d, landing receipts sum to %d", pred.BudgetConsumed.WallMS, sum)
	}
	if len(stmt.Subject) != 1 {
		v.fail(path, "statement has %d subjects, want exactly one", len(stmt.Subject))
		return
	}
	if gate == nil {
		if complete {
			v.fail(path, "no landing suite receipt among the predicate evidence")
		}
		return
	}
	subjectTree := stmt.Subject[0].Digest["gitTree"]
	if want := strings.TrimPrefix(gate.Freshness.ValidFor.Tree, gitx.TreePrefix); subjectTree != want {
		v.fail(path, "statement subject gitTree %s, landing suite receipt valid_for tree %s", subjectTree, want)
	}
}

// trailerValue returns the value of the first "Key: value" line in msg.
func trailerValue(msg, key string) string {
	for _, line := range strings.Split(msg, "\n") {
		if val, ok := strings.CutPrefix(line, key+": "); ok {
			return val
		}
	}
	return ""
}

// diffDecision compares the replay-deterministic fields (NFR-1);
// CreatedAt is a record of when the original happened and is excluded —
// the same comparison mvo audit makes.
func diffDecision(recorded, replayed object.Decision) string {
	switch {
	case recorded.Type != replayed.Type:
		return fmt.Sprintf("type: recorded %q, replayed %q", recorded.Type, replayed.Type)
	case !slices.Equal(recorded.Subject, replayed.Subject):
		return fmt.Sprintf("subject: recorded %v, replayed %v", recorded.Subject, replayed.Subject)
	case !slices.Equal(recorded.Evidence, replayed.Evidence):
		return fmt.Sprintf("evidence: recorded %v, replayed %v", recorded.Evidence, replayed.Evidence)
	case recorded.Rationale != replayed.Rationale:
		return fmt.Sprintf("rationale: recorded %q, replayed %q", recorded.Rationale, replayed.Rationale)
	}
	return ""
}

// decodeSchema unmarshals canonical bytes and asserts the schema field so
// the verifier knows how to check each item without sniffing.
func decodeSchema(b []byte, v any, want string, got func() string) error {
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if got() != want {
		return fmt.Errorf("schema %q, want %q", got(), want)
	}
	return nil
}

func (cfg FetchConfig) validate() error {
	switch {
	case cfg.Repo == "":
		return errors.New("publish: fetch-race: config: empty repo")
	case cfg.Remote == "":
		return errors.New("publish: fetch-race: config: empty remote")
	case !isHex(cfg.Short, ShortLen):
		return fmt.Errorf("publish: fetch-race: config: short %q is not %d hex chars", cfg.Short, ShortLen)
	case len(cfg.Pub) != ed25519.PublicKeySize:
		return errors.New("publish: fetch-race: config: no trusted public key")
	}
	return nil
}

// sortedKeys returns a map's keys in ascending order — deterministic
// iteration for deterministic failure attribution.
func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
