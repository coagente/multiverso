package race

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// Escalation rule names (CP-6). They are the JSON field names of
// object.EscalationSpec, so a rationale, an explain report, and the policy
// file all call a rule the same thing.
const (
	RuleAllWorldsFailedMachinery = "on_all_worlds_failed_machinery"
	RuleRequireEvidence          = "require_evidence"
	RuleMinCandidatesPassing     = "min_candidates_passing"
	RuleOnRankingTie             = "on_ranking_tie"
)

// Comparison step results.
const (
	StepTie  = "tie"
	StepNoOp = "no-op"
)

// GateResult is one hard gate's outcome for one candidate. Result is
// policy.GatePass, policy.GateFail, or policy.GateNotEvaluated — a gate
// after the ladder's first failure is NOT reported as a failure.
type GateResult struct {
	Label   string `json:"label"`
	Result  string `json:"result"`
	Detail  string `json:"detail"`  // failure reason; "" otherwise
	Receipt string `json:"receipt"` // counted receipt digest; "" when none
}

// KeyValue is one ranking key's value for one candidate. Known == false is
// the honest record of "no evidence"; it always loses (M1e decision 5).
// Text is the rendered form the rationale and explain print: "pass", "10",
// a world digest, or "-" for unknown.
type KeyValue struct {
	Key   string `json:"key"`
	Known bool   `json:"known"`
	Value int64  `json:"value"`
	Text  string `json:"text"`
}

// CandidateTrace is one world's full evaluation, in ranked position.
type CandidateTrace struct {
	Rank       int              `json:"rank"`    // 1-based, ranked order
	World      string           `json:"world"`   // world digest
	Ordinal    int              `json:"ordinal"` // 1-based index in the caller's input order
	Outcome    string           `json:"outcome"`
	Pass       bool             `json:"pass"`
	Gates      []GateResult     `json:"gates"`
	Metrics    map[string]int64 `json:"metrics"`
	Keys       []KeyValue       `json:"keys"`
	PatchBytes int64            `json:"patch_bytes"`

	world   object.World
	counted map[policy.Selector]countedReceipt
	failIdx int // index of the first failing gate; -1 when the candidate passes
}

// GateCell is the compact per-world verdict every world table renders:
// "pass", or the LABEL of the first gate that failed — which is the gate
// that stopped the ladder, and the only one an operator needs to read. It
// lives here, beside the trace it reads, so `mvo worlds` and a fetched
// race's world table cannot drift apart.
func (c CandidateTrace) GateCell() string {
	if c.Pass {
		return "pass"
	}
	for _, g := range c.Gates {
		if g.Result == policy.GateFail {
			return g.Label
		}
	}
	if c.Outcome != object.OutcomeCompleted {
		return "-"
	}
	return "fail"
}

// Step is one tied key on the way to a comparison's deciding key.
type Step struct {
	Index  int    `json:"index"`
	Key    string `json:"key"`
	Result string `json:"result"`
}

// Comparison is the leader measured against one other candidate: the keys
// that tied, and the key that decided.
type Comparison struct {
	Other       string `json:"other"`
	OtherRank   int    `json:"other_rank"`
	DecidedAt   int    `json:"decided_at"` // 1-based index into the effective key list
	Key         string `json:"key"`
	WinnerValue string `json:"winner_value"`
	OtherValue  string `json:"other_value"`
	Text        string `json:"text"` // "10 > 8" | "412 < 588" | "pass > fail" | "9 > -"
	Steps       []Step `json:"steps"`
}

// EscalationResult names the escalation rule that fired, with its frozen
// sentence. Rule == "" means no rule fired.
type EscalationResult struct {
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

// RaceTrace is the complete, derived explanation of one race decision.
// Everything in it is recomputed from (policy, worlds, receipts) by the
// same pure functions Decide uses, so improving the rendering can never
// invalidate a decision.
type RaceTrace struct {
	Policy      policy.Policy
	Intent      string
	Type        string
	Gates       []string // gate labels, ladder order
	Keys        []string // EFFECTIVE ranking key names, in order
	Candidates  []CandidateTrace
	Winner      string // leader's world digest; "" when no candidate passed
	PassCount   int
	Evidence    []string // every input receipt digest, sorted
	Escalation  EscalationResult
	Comparisons []Comparison // leader vs each other candidate, ranked order
	Rationale   string
}

// countedReceipt is a world's evidence for one selector: the smallest-digest
// receipt that is BOUND to the world and matches. unbound records the
// smallest-digest receipt that matched but judged another tree or env — kept
// so a gate can say so instead of claiming there was no evidence at all.
type countedReceipt struct {
	rec     *object.Receipt
	dig     string
	unbound string
}

// Trace is the pure evaluation of a race (CP-6, NFR-1): counted receipts,
// per-candidate gate results with reasons, the effective ranking key list,
// the pairwise comparison walk, the escalation verdict, and the frozen
// rationale sentence. It performs no I/O and reads no clock, and it is
// order-independent: shuffling worlds or receipts cannot change it.
func Trace(pol policy.Policy, worlds []object.RecordedWorld, receipts []object.RecordedReceipt) RaceTrace {
	t := RaceTrace{
		Policy:      pol,
		Gates:       pol.GateLabels(),
		Keys:        pol.KeyNames(),
		Evidence:    make([]string, 0, len(receipts)),
		Candidates:  []CandidateTrace{},
		Comparisons: []Comparison{},
	}
	for _, r := range receipts {
		t.Evidence = append(t.Evidence, r.Digest)
	}
	sort.Strings(t.Evidence)

	selectors := policySelectors(pol)
	for i, rw := range worlds {
		if t.Intent == "" {
			t.Intent = rw.World.Intent
		}
		c := CandidateTrace{
			Rank:       0,
			World:      rw.Digest,
			Ordinal:    i + 1,
			Outcome:    rw.World.Outcome,
			PatchBytes: rw.World.PatchBytes,
			world:      rw.World,
			counted:    countReceipts(rw, selectors, receipts),
			failIdx:    -1,
		}
		c.Metrics = mergedMetrics(c.counted)
		evalGates(pol, &c)
		if c.Pass {
			t.PassCount++
		}
		t.Candidates = append(t.Candidates, c)
	}

	// Ranking: the effective key list, each key comparing per decision 5.
	// world_digest_asc terminates it, so the order is a total order and the
	// sort is deterministic under any input permutation.
	sort.Slice(t.Candidates, func(i, j int) bool {
		return rankLess(pol, &t.Candidates[i], &t.Candidates[j])
	})
	for i := range t.Candidates {
		t.Candidates[i].Rank = i + 1
		t.Candidates[i].Keys = keyValues(pol, &t.Candidates[i])
	}
	if len(t.Candidates) > 0 {
		t.Comparisons = compareAll(pol, t.Candidates)
		if t.PassCount > 0 {
			t.Winner = t.Candidates[0].World
		}
	}

	base := TypeSelect
	if t.PassCount == 0 {
		base = TypeReject
	}
	t.Escalation = escalate(pol, &t)
	t.Type = base
	if t.Escalation.Rule != "" {
		t.Type = TypeEscalate
	}
	t.Rationale = rationale(pol, &t, base)
	return t
}

// policySelectors is every selector the compiled policy can read evidence
// through, deduplicated: gates, metric-bearing ranking keys, and the
// escalation rules' evidence requirements.
func policySelectors(pol policy.Policy) []policy.Selector {
	var out []policy.Selector
	seen := map[policy.Selector]bool{}
	add := func(s policy.Selector) {
		if s == (policy.Selector{}) || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if pol.Dialect == policy.DialectV0 {
		// M0 read one suite receipt per world whatever its gates and
		// ranking said, and printed its wall time in every SELECT sentence.
		add(policy.Selector{Family: policy.FamilySuite})
	}
	for _, g := range pol.Gates {
		add(g.Sel)
	}
	for _, k := range pol.Keys {
		add(k.Sel)
	}
	for _, r := range pol.Esc.RequireEvidence {
		add(r.Sel)
	}
	return out
}

// countReceipts picks each selector's counted receipt for one world: the
// smallest-digest receipt that names the world, is BOUND to it
// (freshness.valid_for == {world.tree, world.env} — evidence is bound or it
// is noise, PRD principle 2), and matches the selector. At most one, and
// order-independent.
func countReceipts(rw object.RecordedWorld, selectors []policy.Selector,
	receipts []object.RecordedReceipt) map[policy.Selector]countedReceipt {
	out := make(map[policy.Selector]countedReceipt, len(selectors))
	for _, sel := range selectors {
		var best countedReceipt
		for i := range receipts {
			rr := receipts[i]
			if rr.Receipt.World != rw.Digest || !sel.Match(rr.Receipt) {
				continue
			}
			if !bound(rw.World, rr.Receipt) {
				if best.unbound == "" || rr.Digest < best.unbound {
					best.unbound = rr.Digest
				}
				continue
			}
			if best.rec == nil || rr.Digest < best.dig {
				rec := rr.Receipt
				best.rec, best.dig = &rec, rr.Digest
			}
		}
		out[sel] = best
	}
	return out
}

// bound reports whether a receipt's freshness pins exactly the world it
// names. A receipt that names a world but judged a different tree or
// environment is inadmissible, not decisive (M1e decision 10).
func bound(w object.World, r object.Receipt) bool {
	return r.Freshness.ValidFor.Tree == w.Tree && r.Freshness.ValidFor.Env == w.Env
}

// mergedMetrics is the union of a candidate's counted receipts' metrics,
// merged in ascending receipt-digest order, so the map is order-independent.
// It is display data for mvo explain; every gate and key reads its own
// oracle's receipt directly.
//
// A name two DIFFERENT counted receipts disagree about is dropped rather
// than resolved by digest order: a policy may legally declare two instances
// of one kind, and a number no consumer can attribute to an oracle is worse
// than no number at all. Receipts that agree leave the value in place, and
// the single-instance case — every policy shipped — is untouched.
func mergedMetrics(counted map[policy.Selector]countedReceipt) map[string]int64 {
	recs := make([]countedReceipt, 0, len(counted))
	seen := make(map[string]bool, len(counted))
	for _, c := range counted {
		if c.rec == nil || seen[c.dig] {
			continue // one receipt may be counted through several selectors
		}
		seen[c.dig] = true
		recs = append(recs, c)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].dig < recs[j].dig })
	out := map[string]int64{}
	ambiguous := map[string]bool{}
	for _, c := range recs {
		for name, v := range c.rec.Result.Metrics {
			if prev, dup := out[name]; dup {
				if prev != v {
					ambiguous[name] = true
				}
				continue
			}
			out[name] = v
		}
	}
	for name := range ambiguous {
		delete(out, name)
	}
	return out
}

// evalGates runs the hard gates in policy order. Under the v1 dialect the
// ladder STOPS at the first failure — gates after it are not-evaluated,
// exactly as the race never ran their oracles. The v0 dialect evaluates
// every gate, because M0 did and its recorded rationales list them all.
func evalGates(pol policy.Policy, c *CandidateTrace) {
	c.Pass = true
	c.Gates = make([]GateResult, 0, len(pol.Gates))
	for i, g := range pol.Gates {
		if c.failIdx >= 0 && pol.Dialect == policy.DialectV1 {
			c.Gates = append(c.Gates, GateResult{Label: g.Label, Result: policy.GateNotEvaluated})
			continue
		}
		cr := c.counted[g.Sel]
		ok, reason := g.Eval(cr.rec)
		if !ok {
			reason = gateReason(pol, c, cr, reason)
		}
		res := GateResult{Label: g.Label, Result: policy.GatePass, Receipt: cr.dig}
		if !ok {
			res.Result, res.Detail = policy.GateFail, reason
			c.Pass = false
			if c.failIdx < 0 {
				c.failIdx = i
			}
		}
		c.Gates = append(c.Gates, res)
	}
}

// gateReason refines a gate's failure reason with the most fundamental
// cause available. Under the v0 dialect it is M0's failReason, frozen: a
// pinned policy's recorded sentences must reproduce byte-for-byte.
func gateReason(pol policy.Policy, c *CandidateTrace, cr countedReceipt, reason string) string {
	if pol.Dialect == policy.DialectV0 {
		return failReasonV0(c, cr)
	}
	if reason != policy.ReasonNoReceipt {
		return reason
	}
	switch {
	case c.Outcome != object.OutcomeCompleted:
		return "outcome=" + c.Outcome
	case cr.unbound != "":
		return fmt.Sprintf("unbound receipt %s (judged another tree or env)", cr.unbound)
	default:
		return reason
	}
}

// failReasonV0 is M0's per-candidate explanation, most fundamental cause
// first, reproduced character for character.
func failReasonV0(c *CandidateTrace, cr countedReceipt) string {
	switch {
	case c.Outcome != object.OutcomeCompleted:
		return "outcome=" + c.Outcome
	case cr.rec == nil:
		return "no suite receipt"
	default:
		return "status=" + cr.rec.Result.Status
	}
}

// keyValue resolves one ranking key for one candidate: (known, value) plus
// the rendered text. Every key is TOTAL — unknown always loses, in both
// directions, and no key can panic, divide, or order by accident.
func keyValue(k policy.Key, c *CandidateTrace) KeyValue {
	kv := KeyValue{Key: k.Name, Text: "-"}
	switch {
	case k.NoOp:
		return kv
	case k.Name == policy.KeyGatePass:
		kv.Known, kv.Value, kv.Text = true, 0, "fail"
		if c.Pass {
			kv.Value, kv.Text = 1, "pass"
		}
	case k.Name == policy.KeyWorldDigestAsc:
		kv.Known, kv.Text = true, c.World
	case k.Name == policy.KeyWallMSAsc:
		var sum int64
		seen := map[string]bool{}
		for _, cr := range c.counted {
			if cr.rec == nil || seen[cr.dig] {
				continue
			}
			seen[cr.dig] = true
			sum += cr.rec.Cost.WallMS
			kv.Known = true
		}
		if kv.Known {
			kv.Value, kv.Text = sum, strconv.FormatInt(sum, 10)
		}
	case k.Name == policy.KeyCostAsc:
		if c.world.Cost.Source != "none" {
			kv.Known, kv.Value = true, c.world.Cost.USDMicro
			kv.Text = strconv.FormatInt(kv.Value, 10)
		}
	case k.Name == policy.KeyPatchSizeAsc:
		// 0 is a real empty patch, never "unknown".
		kv.Known, kv.Value = true, c.world.PatchBytes
		kv.Text = strconv.FormatInt(kv.Value, 10)
	case k.Metric != "":
		if cr := c.counted[k.Sel]; cr.rec != nil {
			if v, ok := cr.rec.Result.Metrics[k.Metric]; ok {
				kv.Known, kv.Value = true, v
				kv.Text = strconv.FormatInt(v, 10)
			}
		}
	}
	return kv
}

func keyValues(pol policy.Policy, c *CandidateTrace) []KeyValue {
	out := make([]KeyValue, 0, len(pol.Keys))
	for _, k := range pol.Keys {
		out = append(out, keyValue(k, c))
	}
	return out
}

// compareKey orders two candidates by one key: negative when a ranks first,
// positive when b does, zero on a tie. A candidate with a known value
// outranks one without in BOTH directions, so "no evidence" can never beat
// "evidence".
func compareKey(pol policy.Policy, k policy.Key, a, b *CandidateTrace) int {
	if k.NoOp {
		return 0
	}
	av, bv := keyValue(k, a), keyValue(k, b)
	if k.Name == policy.KeyWorldDigestAsc {
		return strings.Compare(a.World, b.World)
	}
	switch {
	case av.Known != bv.Known:
		if av.Known {
			return -1
		}
		return 1
	case !av.Known || av.Value == bv.Value:
		return 0
	case k.Desc:
		if av.Value > bv.Value {
			return -1
		}
		return 1
	default:
		if av.Value < bv.Value {
			return -1
		}
		return 1
	}
}

func rankLess(pol policy.Policy, a, b *CandidateTrace) bool {
	for _, k := range pol.Keys {
		if cmp := compareKey(pol, k, a, b); cmp != 0 {
			return cmp < 0
		}
	}
	return a.World < b.World
}

// compareAll walks the leader against every other candidate in ranked
// order, recording the keys that tied and the key that decided.
func compareAll(pol policy.Policy, cands []CandidateTrace) []Comparison {
	out := make([]Comparison, 0, len(cands))
	for i := 1; i < len(cands); i++ {
		out = append(out, compare(pol, &cands[0], &cands[i]))
	}
	return out
}

func compare(pol policy.Policy, win, other *CandidateTrace) Comparison {
	c := Comparison{Other: other.World, OtherRank: other.Rank, Steps: []Step{}}
	for i, k := range pol.Keys {
		if cmp := compareKey(pol, k, win, other); cmp != 0 {
			wv, ov := keyValue(k, win), keyValue(k, other)
			c.DecidedAt, c.Key = i+1, k.Name
			c.WinnerValue, c.OtherValue = wv.Text, ov.Text
			c.Text = comparisonText(k, wv, ov)
			return c
		}
		result := StepTie
		if k.NoOp {
			result = StepNoOp
		}
		c.Steps = append(c.Steps, Step{Index: i + 1, Key: k.Name, Result: result})
	}
	// Unreachable: world_digest_asc terminates every effective key list and
	// two candidates are two distinct worlds.
	return c
}

// comparisonText renders a deciding key as the rationale prints it:
// "10 > 8" for a descending key, "412 < 588" for an ascending one,
// "pass > fail" for gate_pass, and "9 > -" when the loser has no evidence.
func comparisonText(k policy.Key, winner, other KeyValue) string {
	op := "<"
	if k.Desc || !other.Known {
		op = ">"
	}
	return fmt.Sprintf("%s %s %s", winner.Text, op, other.Text)
}

// escalate evaluates the CLOSED escalation rule set in its fixed order —
// first match wins and supplies the reason (M1e decision 6). Rule 1
// replaces REJECT; rules 2-4 replace SELECT.
func escalate(pol policy.Policy, t *RaceTrace) EscalationResult {
	esc := pol.Esc
	// 1. The machinery never produced a verdict. REJECT means "the
	//    candidates are bad"; saying so when nothing was measured would be a
	//    lie about the evidence.
	if esc.OnAllWorldsFailedMachinery && t.PassCount == 0 && len(t.Candidates) > 0 {
		reasons := make([]string, 0, len(t.Candidates))
		all := true
		for i := range t.Candidates {
			c := &t.Candidates[i]
			reason, machinery := machineryFailure(pol, c)
			if !machinery {
				all = false
				break
			}
			reasons = append(reasons, reason)
		}
		if all {
			return EscalationResult{
				Rule:   RuleAllWorldsFailedMachinery,
				Detail: fmt.Sprintf("no world produced conclusive evidence (%s)", strings.Join(reasons, ", ")),
			}
		}
	}
	if t.PassCount == 0 {
		return EscalationResult{}
	}
	win := &t.Candidates[0]
	// 2. The winner carries no USABLE evidence from an oracle the policy
	//    requires. A missing counted receipt is one way; the way that
	//    actually happens is a receipt whose structured source was
	//    unavailable, so it carries none of its kind's metrics. The winner
	//    passed every hard gate by construction, so its ladder ran to
	//    completion and the receipt is always present — testing existence
	//    alone would make this rule unfireable in any race the orchestrator
	//    produces. What decision 16 points operators at is precisely the
	//    other half: "the source was absent, route a human rather than
	//    reject". A kind that declares no metric vocabulary at all
	//    (`command`) is judged on existence only — having no vocabulary is
	//    not the same as having no evidence.
	for _, req := range esc.RequireEvidence {
		cr := win.counted[req.Sel]
		if cr.rec == nil {
			return EscalationResult{
				Rule: RuleRequireEvidence,
				Detail: fmt.Sprintf("winner %s has no counted receipt from required oracle %q",
					win.World, req.OracleName),
			}
		}
		if vocab := policy.KindMetrics(req.Sel.ID); len(vocab) > 0 && !emitsAny(cr.rec, vocab) {
			return EscalationResult{
				Rule: RuleRequireEvidence,
				Detail: fmt.Sprintf("winner %s has no usable evidence from required oracle %q: receipt %s carries none of its metrics",
					win.World, req.OracleName, cr.dig),
			}
		}
	}
	// 3. Too few candidates cleared the gates to choose between.
	if n := esc.MinCandidatesPassing; n > 0 && t.PassCount < n {
		return EscalationResult{
			Rule: RuleMinCandidatesPassing,
			Detail: fmt.Sprintf("%d of %d worlds passed, policy requires at least %d",
				t.PassCount, len(t.Candidates), n),
		}
	}
	// 4. The ranking cannot separate the leaders: only the digest tie-break
	//    would, and a coin flip is not a decision (the PRD's ambiguity case).
	if esc.OnRankingTie && t.PassCount >= 2 {
		next := &t.Candidates[1]
		if tiedToTerminal(pol, win, next) {
			return EscalationResult{
				Rule: RuleOnRankingTie,
				Detail: fmt.Sprintf("%s and %s tie on every ranking key [%s]; only %s would order them",
					win.World, next.World, strings.Join(rankingKeysBeforeTerminal(pol), ","),
					policy.KeyWorldDigestAsc),
			}
		}
	}
	return EscalationResult{}
}

// emitsAny reports whether a receipt carries at least one metric of a
// vocabulary. Absence of every one of them is the honest record of "the
// structured source was unavailable" — never a zero, so this is the only
// way to see it.
func emitsAny(rec *object.Receipt, vocab []string) bool {
	for _, m := range vocab {
		if _, ok := rec.Result.Metrics[m]; ok {
			return true
		}
	}
	return false
}

// machineryFailure reports whether a candidate failed for want of working
// machinery rather than for want of quality, with the sentence that says so.
func machineryFailure(pol policy.Policy, c *CandidateTrace) (string, bool) {
	if c.Outcome != object.OutcomeCompleted {
		return fmt.Sprintf("%s outcome=%s", c.World, c.Outcome), true
	}
	for _, cr := range c.counted {
		if cr.rec != nil && cr.rec.Result.Status == "error" {
			return fmt.Sprintf("%s status=error", c.World), true
		}
	}
	if len(pol.Gates) > 0 {
		if cr := c.counted[pol.Gates[0].Sel]; cr.rec == nil {
			return fmt.Sprintf("%s no receipt", c.World), true
		}
	}
	return "", false
}

// tiedToTerminal reports whether two candidates tie on every effective key
// except the terminal world_digest_asc.
func tiedToTerminal(pol policy.Policy, a, b *CandidateTrace) bool {
	for _, k := range pol.Keys {
		if k.Name == policy.KeyWorldDigestAsc {
			continue
		}
		if compareKey(pol, k, a, b) != 0 {
			return false
		}
	}
	return true
}

func rankingKeysBeforeTerminal(pol policy.Policy) []string {
	out := make([]string, 0, len(pol.Keys))
	for _, k := range pol.Keys {
		if k.Name == policy.KeyWorldDigestAsc {
			continue
		}
		out = append(out, k.Name)
	}
	return out
}
