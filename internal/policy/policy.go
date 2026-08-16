// Package policy is the policy plane (CP-5): wire decode → validate →
// compile. Two frozen shapes live on the wire — multiverso.dev/policy/v0
// (M0's, frozen byte-for-byte) and multiverso.dev/policy/v1 (the CP-5
// artifact: ordered hard gates naming an oracle and a required freshness
// basis, a lexicographic ranking spec, and a closed escalation rule set) —
// and both compile to ONE in-memory Policy, so the decision functions have
// exactly one code path (M1e decision 2).
//
// Nothing here rewrites a policy's bytes: a policy's digest is what intents
// pin and what attestations name, so the compiled value carries the digest
// of the exact bytes it was loaded from and never a re-serialization of
// itself (M1e decision 1).
package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/object"
)

// Dialects: the frozen rationale renderer a compiled policy selects. A v0
// policy renders M0's sentence byte-for-byte forever; a v1 policy renders
// the M1e sentence. Re-wording a decision made under a pinned policy would
// rewrite history, and audit compares rationales byte-for-byte (decision 3).
const (
	DialectV0 = "v0"
	DialectV1 = "v1"
)

// Gate results, as reported by a compiled gate's evaluation. A gate after
// the first failure in a ladder is not-evaluated — never reported as a
// failure (M1e decision 12).
const (
	GatePass         = "pass"
	GateFail         = "fail"
	GateNotEvaluated = "not-evaluated"
)

// ReasonNoReceipt is the gate failure reason when the gate's oracle
// produced no counted receipt for the subject at all.
const ReasonNoReceipt = "no receipt"

// Policy is a validated, compiled policy: the single in-memory form both
// schema versions compile to. Digest is the digest of the exact bytes it
// was loaded from.
type Policy struct {
	Digest   string     // "mv0:…" as recorded/loaded
	Schema   string     // object.SchemaPolicy | object.SchemaPolicyV1
	Dialect  string     // DialectV0 | DialectV1
	Name     string     // "" for v0
	Gates    []Gate     // ordered: ladder order and evaluation order
	Keys     []Key      // effective ranking: gate_pass first, world_digest_asc last
	Ranking  []string   // the ranking list AS AUTHORED (the v0 renderer prints it)
	Esc      Escalation // compiled escalation rules
	Oracles  []Oracle   // declared instances, name-sorted
	Required []string   // oracle names the race must run, in ladder order
	// M1f. Paths is the compiled path grammar the tree-guard oracle walks;
	// Invariants is the compiled cross-oracle set Decide evaluates after
	// the gates; Evidence says how future runs are OBSERVED and never
	// enters Decide (decision 3). A v0 policy and an M1e v1 policy both
	// compile to the M1e-identical defaults: no invariants, an empty path
	// set, regime auto, crosscheck require.
	Paths      PathSet
	Invariants []Invariant // name-sorted, then role-map-sorted
	Evidence   EvidencePlan
}

// EvidencePlan is the compiled EvidenceSpec: the "" sentinels resolved to
// their M1f defaults exactly once, where they can be tested (decision 4).
type EvidencePlan struct {
	Regime         string // RegimeAuto | object.Regime*
	Crosscheck     string // CrosscheckRequire | CrosscheckOff
	PluginAutoload string // AutoloadOff | AutoloadOn
}

// Gate is one ordered hard gate.
type Gate struct {
	Predicate string // vocab constant
	Oracle    string // declared oracle name ("" in the v0 dialect)
	Basis     string // minimum acceptable basis
	Threshold int64
	Label     string // rendered gate name: "suite-pass" (v0) | "status-pass@suite" (v1)
	Sel       Selector
	// Scope is the RESOLVED gate scope: ScopeBoth (the "" default, and
	// what every M1 gate meant), ScopeRace or ScopeLanding.
	Scope string
	// AlwaysFails carries M0's fail-closed unknown-gate rule into the v0
	// dialect: what M0 could not attest, it did not admit, and replay must
	// reproduce that (M1e decision 7).
	AlwaysFails bool
	// RefuseAdditions mirrors paths.protected_additions into the gate that
	// reads it, so the predicate stays a pure function of (gate, receipt)
	// and never reaches back into the policy document.
	RefuseAdditions bool
}

// guardCountMetrics are the five counts whose non-zero value is ALWAYS a
// violation, in the order the fail reason prints them. protected_added is
// deliberately not here: it is a violation only under
// protected_additions == "refuse" (M1f decision 6).
var guardCountMetrics = []string{
	MetricProtectedModified, MetricProtectedDeleted,
	MetricHarnessModified, MetricHarnessDeleted, MetricHarnessAdded,
}

// firstOffender is the lexicographically first offending path the guard
// recorded in result.detail — enough to act on in a table cell, with the
// whole list one artifact away. A receipt that names none (an older
// implementation, or a hand-written fixture) renders "-": the counts are
// still the verdict.
func (g Gate) firstOffender(rec *object.Receipt) string {
	if rec == nil || rec.Result.Detail == "" {
		return "-"
	}
	return rec.Result.Detail
}

// Selector picks a world's counted receipt for one oracle instance (M1e
// decision 9): evidence selection is DATA the compiled policy carries, not
// a branch in Decide. Exactly one of Family / (ID, Config) is set.
type Selector struct {
	Family string // v0 dialect: receipt.family
	ID     string // v1: receipt.oracle.id (the kind)
	Config string // v1: receipt.oracle.config (resolved-config digest)
}

// Key is one effective ranking key.
type Key struct {
	Name   string
	Desc   bool     // true = descending (bigger is better)
	Metric string   // "" for gate_pass / cost_asc / patch_size_asc / world_digest_asc
	Sel    Selector // where Metric is read from (resolved at validation)
	// NoOp marks an unknown key name in a v0 policy: M0's rankLess ignored
	// names it did not know, so the compiled key must too.
	NoOp bool
}

// Escalation is the compiled, closed escalation rule set.
type Escalation struct {
	MinCandidatesPassing       int
	OnRankingTie               bool
	RequireEvidence            []Requirement // name-sorted
	OnAllWorldsFailedMachinery bool
	OnInvariantViolation       bool // M1f rule 0 — highest precedence
	// OnBehavioralSplit is M2a rule 1b, between machinery failure and
	// require_evidence: 0 = off, N = escalate at >= N behaviour classes.
	// It sits BELOW machinery failure (if nothing produced evidence, "they
	// disagree" is a false statement about a race that never ran) and
	// ABOVE require_evidence (a detected behavioural ambiguity is a
	// stronger reason to stop than a missing optional source).
	OnBehavioralSplit int
}

// Requirement names an oracle whose evidence the winner must carry.
type Requirement struct {
	OracleName string
	Sel        Selector
}

// Oracle is one resolved oracle instance. Config is the digest of the
// canonical resolved configuration WITHOUT Name (M1e decision 8): a receipt
// records what ran, not what an operator called it, so two policies running
// the identical command produce comparable evidence.
type Oracle struct {
	Name      string
	Kind      string
	Family    string
	Config    string
	Argv      []string
	Args      []string
	TimeoutMS int64
	Coverage  bool
	Reruns    int
	// Corpus is the resolved corpus declaration (M2a): CasesMax already
	// defaulted, so the ceiling a run enforces is a value, not a sentinel.
	Corpus object.CorpusSpec
	// Mutation is the resolved mutation budget (M2a decision 11), zero for
	// every kind but mutation-diff. Like Corpus it is RESOLVED: the oracle
	// reads a ceiling, never a sentinel that a second reader might
	// interpret as "unbounded".
	Mutation object.MutationSpec
}

// AppliesAt reports whether a gate is evaluated at the given scope.
// ScopeBoth gates apply everywhere, which is what every pre-M2a gate meant
// and what "" compiles to.
func (g Gate) AppliesAt(scope string) bool {
	switch g.Scope {
	case "", ScopeBoth:
		return true
	default:
		return g.Scope == scope
	}
}

// CorpusOracle returns the single declared instance that consumes a corpus
// — the one phase 0 must materialize for. Validation rule 18 makes "single"
// true whenever a differential is declared: two observers would make "the
// cohort" ambiguous and zero would make the reducer a function of nothing.
func (p Policy) CorpusOracle() (Oracle, bool) {
	for _, o := range p.Oracles {
		if o.Kind == KindCorpusObserve && o.Corpus.Provider != ProviderNone {
			return o, true
		}
	}
	return Oracle{}, false
}

// DifferentialOracle returns the declared cohort-stage reducer instance.
// Absent means the race runs no cohort barrier at all.
func (p Policy) DifferentialOracle() (Oracle, bool) {
	for _, o := range p.Oracles {
		if o.Kind == KindCorpusDifferential {
			return o, true
		}
	}
	return Oracle{}, false
}

// GatesAt returns the compiled gates that apply at one scope, in ladder
// order. `mvo admit` uses it to drop race-scope gates from the landing
// evaluation — and then NAMES them on its own output line, because a
// landing gate set weaker than the race's is a legitimate policy choice and
// must never be an invisible one (M2a decision 21).
func (p Policy) GatesAt(scope string) []Gate {
	out := make([]Gate, 0, len(p.Gates))
	for _, g := range p.Gates {
		if g.AppliesAt(scope) {
			out = append(out, g)
		}
	}
	return out
}

// EnforcesPaths reports whether any compiled gate actually READS the
// compiled path set. A freeze nothing checks is not a freeze, and a
// renderer that says "frozen against the candidate" over an unread path set
// is making a claim the policy cannot keep. Validation rule 24 forbids the
// corpus-derived case outright; this is what keeps the rendering honest for
// the hand-authored one.
func (p Policy) EnforcesPaths() bool {
	for _, g := range p.Gates {
		if g.Predicate == GatePathsUnmodified {
			return true
		}
	}
	return false
}

// GatesNotAt returns the compiled gates EXCLUDED at one scope, in ladder
// order: exactly the list `mvo admit` prints as "not evaluated at
// admission".
func (p Policy) GatesNotAt(scope string) []Gate {
	out := make([]Gate, 0, len(p.Gates))
	for _, g := range p.Gates {
		if !g.AppliesAt(scope) {
			out = append(out, g)
		}
	}
	return out
}

// Problem is one validation failure, located by its JSON field path.
// Validate reports every problem at once: authoring wants the whole list,
// not the first line.
type Problem struct {
	Field  string
	Detail string
}

func (p Problem) Error() string { return p.Field + ": " + p.Detail }

// Problems extracts the individual validation failures joined into err, in
// report order. It returns nil for an error that carries none.
func Problems(err error) []Problem {
	var out []Problem
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		// Joined errors are walked BEFORE errors.As, which would otherwise
		// match the first Problem in the tree and hide the rest.
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			for _, sub := range multi.Unwrap() {
				walk(sub)
			}
			return
		}
		var p Problem
		if errors.As(e, &p) {
			out = append(out, p)
		}
	}
	walk(err)
	return out
}

// Decode discriminates on the "schema" field, validates, and compiles. The
// compiled policy's Digest is object.DigestBytes(b): the bytes ARE the
// identity, and no field of the returned value is ever re-serialized to
// recover it.
func Decode(b []byte) (Policy, error) {
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return Policy{}, fmt.Errorf("policy: decode: %w", err)
	}
	dig := object.DigestBytes(b)
	switch head.Schema {
	case object.SchemaPolicy:
		// v0 is FROZEN and is decoded exactly as M0 decoded it (plain
		// Unmarshal, no strictness upgrade): its recorded decisions were
		// derived that way and replay must reproduce them (decision 7).
		var pol object.Policy
		if err := json.Unmarshal(b, &pol); err != nil {
			return Policy{}, fmt.Errorf("policy: decode %s: %w", object.SchemaPolicy, err)
		}
		return compileV0(dig, pol), nil
	case object.SchemaPolicyV1:
		pol, err := decodeV1(b)
		if err != nil {
			return Policy{}, err
		}
		if err := Validate(pol); err != nil {
			return Policy{}, err
		}
		compiled, err := Compile(dig, pol)
		if err != nil {
			return Policy{}, err
		}
		return compiled, nil
	default:
		return Policy{}, Problem{
			Field: "schema",
			Detail: fmt.Sprintf("unknown policy schema %q (known: %s, %s)",
				head.Schema, object.SchemaPolicy, object.SchemaPolicyV1),
		}
	}
}

// decodeV1 decodes the v1 wire shape with unknown fields REFUSED: a typo'd
// "hard_gate" must not silently mean "no gates" (validation rule 8).
func decodeV1(b []byte) (object.PolicyV1, error) {
	var pol object.PolicyV1
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pol); err != nil {
		if name, ok := unknownField(err); ok {
			return pol, Problem{Field: name, Detail: "unknown field (the policy schema is closed)"}
		}
		return pol, fmt.Errorf("policy: decode %s: %w", object.SchemaPolicyV1, err)
	}
	return pol, nil
}

func unknownField(err error) (string, bool) {
	const prefix = "json: unknown field "
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return "", false
	}
	return strings.Trim(strings.TrimPrefix(msg, prefix), `"`), true
}

// Load fetches a policy's canonical bytes from CAS by digest and decodes
// them, cross-checking that the bytes digest back to the requested digest.
// This is how the ledger/CAS supplies HISTORICAL policies: every policy
// ever used is content-addressed in CAS and CAS is never pruned (M1d), so
// an intent pinned in the past always resolves for replay.
func Load(store *cas.Store, digest string) (Policy, error) {
	key, err := object.CASKey(digest)
	if err != nil {
		return Policy{}, fmt.Errorf("policy: %w", err)
	}
	b, err := store.Get(key)
	if err != nil {
		return Policy{}, fmt.Errorf("policy: load %s: %w", digest, err)
	}
	if got := object.DigestBytes(b); got != digest {
		return Policy{}, fmt.Errorf("policy: load %s: bytes digest to %s", digest, got)
	}
	pol, err := Decode(b)
	if err != nil {
		return Policy{}, fmt.Errorf("policy: load %s: %w", digest, err)
	}
	return pol, nil
}

// GateLabels returns the compiled gates' labels in ladder order.
func (p Policy) GateLabels() []string {
	out := make([]string, 0, len(p.Gates))
	for _, g := range p.Gates {
		out = append(out, g.Label)
	}
	return out
}

// GateLabelsAt returns the labels of the gates that apply at one scope, in
// ladder order. A policy with no scoped gates returns exactly GateLabels(),
// which is what keeps every M1a/M1e/M1f admission sentence byte-for-byte
// unchanged (M2a decision 21 under M1f decision 3's compatibility rule).
func (p Policy) GateLabelsAt(scope string) []string {
	out := make([]string, 0, len(p.Gates))
	for _, g := range p.Gates {
		if g.AppliesAt(scope) {
			out = append(out, g.Label)
		}
	}
	return out
}

// KeyNames returns the EFFECTIVE ranking key names in order.
func (p Policy) KeyNames() []string {
	out := make([]string, 0, len(p.Keys))
	for _, k := range p.Keys {
		out = append(out, k.Name)
	}
	return out
}

// OracleByName returns the declared instance called name.
func (p Policy) OracleByName(name string) (Oracle, bool) {
	for _, o := range p.Oracles {
		if o.Name == name {
			return o, true
		}
	}
	return Oracle{}, false
}

// CollectOracle returns the declared instance whose collected_delta a hard
// gate reads — the one a base-state measurement must run (M1e decision 13).
// It is absent when no collected-not-below gate exists: a policy that never
// compares counts needs no denominator, and measuring one anyway would be
// evidence waste.
func (p Policy) CollectOracle() (Oracle, bool) {
	for _, g := range p.Gates {
		if g.Predicate != GateCollectedNotBelow {
			continue
		}
		if o, ok := p.OracleByName(g.Oracle); ok {
			return o, true
		}
	}
	return Oracle{}, false
}

// GateOracles returns the declared instances the hard gates name, in gate
// order, deduplicated: exactly the oracles an admission must recompute on
// the landing tree (M1e decision 20).
func (p Policy) GateOracles() []Oracle { return p.GateOraclesAt(ScopeBoth) }

// GateOraclesAt is GateOracles restricted to the gates that apply at one
// scope. `mvo admit` passes ScopeLanding, so a race-scope oracle is never
// recomputed on the landing tree — running a cohort-stage oracle over one
// subject would produce a receipt whose every comparison metric is absent
// and a gate that can never succeed.
//
// ScopeBoth means "every gate", because a both-scope gate applies
// everywhere: that is the identity case and it is what every pre-M2a
// caller gets.
func (p Policy) GateOraclesAt(scope string) []Oracle {
	seen := make(map[string]bool, len(p.Gates))
	out := make([]Oracle, 0, len(p.Gates))
	for _, g := range p.Gates {
		if scope != ScopeBoth && !g.AppliesAt(scope) {
			continue
		}
		if seen[g.Oracle] {
			continue
		}
		seen[g.Oracle] = true
		if o, ok := p.OracleByName(g.Oracle); ok {
			out = append(out, o)
		}
	}
	return out
}

// SchemaShort renders a policy schema for tables and headers:
// "policy/v1", "policy/v0".
func SchemaShort(schema string) string {
	if short, ok := strings.CutPrefix(schema, "multiverso.dev/"); ok {
		return short
	}
	return schema
}

// Match reports whether a receipt matches the selector. The v0 dialect
// selects by family (M0's rule); v1 selects by the receipt's own oracle
// identity — the registry kind plus the resolved-config digest — so the
// policy-local name never has to enter a receipt (decision 8).
func (s Selector) Match(r object.Receipt) bool {
	if s.Family != "" {
		return r.Family == s.Family
	}
	return r.Oracle.ID == s.ID && r.Oracle.Config == s.Config
}

// Eval applies the gate predicate to its counted receipt and reports
// whether the gate passes, with the failure reason. rec == nil means the
// gate's oracle produced no counted receipt for the subject; callers with a
// more fundamental cause to report (a world that never completed, a receipt
// that judged another tree) substitute their own reason for that case.
//
// Every predicate first requires that the receipt exists, carries a basis
// at least as strong as the gate's, and did not error: an inconclusive run
// is never a pass. A required metric that is ABSENT fails the gate — never
// a fabricated zero.
func (g Gate) Eval(rec *object.Receipt) (bool, string) {
	if g.AlwaysFails {
		return false, "unknown gate"
	}
	if rec == nil {
		return false, ReasonNoReceipt
	}
	if BasisRank(rec.Freshness.Basis) < BasisRank(g.Basis) {
		return false, fmt.Sprintf("basis=%s (want >= %s)", rec.Freshness.Basis, g.Basis)
	}
	if rec.Result.Status == "error" {
		return false, "status=error"
	}
	metric := func(name string) (int64, bool) {
		v, ok := rec.Result.Metrics[name]
		return v, ok
	}
	switch g.Predicate {
	case GateStatusPass, GateSuitePass:
		if rec.Result.Status == "pass" {
			return true, ""
		}
		return false, "status=" + rec.Result.Status
	case GateCollectNonempty:
		total, ok := metric(MetricCollectedTotal)
		if !ok {
			return false, absent(MetricCollectedTotal)
		}
		if total >= 1 {
			return true, ""
		}
		return false, fmt.Sprintf("collected_total=%d (exit %d)", total, rec.Execution.ExitCode)
	case GateCollectedNotBelow:
		delta, ok := metric(MetricCollectedDelta)
		if !ok {
			return false, absent(MetricCollectedDelta)
		}
		if delta >= -g.Threshold {
			return true, ""
		}
		return false, fmt.Sprintf("collected_delta=%d (tolerance -%d)", delta, g.Threshold)
	case GateNoFailedTests:
		failed, okF := metric(MetricTestsFailed)
		if !okF {
			return false, absent(MetricTestsFailed)
		}
		errored, okE := metric(MetricTestsErrored)
		if !okE {
			return false, absent(MetricTestsErrored)
		}
		if failed == 0 && errored == 0 {
			return true, ""
		}
		return false, fmt.Sprintf("tests_failed=%d tests_errored=%d", failed, errored)
	case GateCoverageAtLeast:
		bp, ok := metric(MetricCoverageBP)
		if !ok {
			return false, absent(MetricCoverageBP)
		}
		if bp >= g.Threshold {
			return true, ""
		}
		return false, fmt.Sprintf("coverage_bp=%d (want >= %d)", bp, g.Threshold)
	case GateSkipsNotAbove:
		skipped, ok := metric(MetricTestsSkipped)
		if !ok {
			return false, absent(MetricTestsSkipped)
		}
		if skipped <= g.Threshold {
			return true, ""
		}
		return false, fmt.Sprintf("tests_skipped=%d (want <= %d)", skipped, g.Threshold)
	case GateCorpusComplete:
		// A world that observed fewer cases than the pinned corpus
		// declares did not run our corpus, whatever else it did. The
		// metrics are absent rather than partial when the observation is
		// unusable (cohort starvation, corpus vector 18), and an absent
		// metric FAILS the gate — which is how a world that silenced the
		// runner to shrink the cohort eliminates itself.
		observed, ok := metric(MetricCorpusCasesObserved)
		if !ok {
			return false, absent(MetricCorpusCasesObserved)
		}
		total, ok := metric(MetricCorpusCasesTotal)
		if !ok {
			return false, absent(MetricCorpusCasesTotal)
		}
		if observed == total {
			return true, ""
		}
		return false, fmt.Sprintf("corpus_cases_observed=%d of %d", observed, total)
	case GatePropertiesPass:
		failed, okF := metric(MetricPropertiesFailed)
		if !okF {
			return false, absent(MetricPropertiesFailed)
		}
		errored, okE := metric(MetricPropertiesErrored)
		if !okE {
			return false, absent(MetricPropertiesErrored)
		}
		if failed == 0 && errored == 0 {
			return true, ""
		}
		return false, fmt.Sprintf("properties_failed=%d properties_errored=%d", failed, errored)
	case GatePropertyCasesAtLeast:
		// The metric is ABSENT under the JSONL fallback (M2a decision 15),
		// and absence fails the gate. That is the intended shape: a policy
		// that demands a searched case budget and gets records it cannot
		// trust has not had its demand met, and saying so is the whole
		// point of one metric name with one provenance.
		total, ok := metric(MetricPropertyCasesTotal)
		if !ok {
			return false, absent(MetricPropertyCasesTotal)
		}
		if total >= g.Threshold {
			return true, ""
		}
		return false, fmt.Sprintf("property_cases_total=%d (want >= %d)", total, g.Threshold)
	case GateMutationSurvivorsNotAbove:
		// The ABSOLUTE count of mutants the tests did not kill, never the
		// score. Under diff-scoping a larger diff makes this gate strictly
		// harder, which is the direction we want, and padding the diff with
		// trivially-killed lines cannot reduce it (corpus vector 14) —
		// padding adds mutants that may themselves survive.
		//
		// TIMEOUTS COUNT. A mutant that hung is the strongest available
		// evidence that the suite did not discriminate against it, and it is
		// emphatically not "the tests caught it". Reading `mutants_survived`
		// alone made a hang the cheapest possible escape: the only mutation
		// gate M2a ships cost the candidate nothing for a mutant nobody
		// killed, while the receipt asserted a 100 % score beside it. A red
		// team ran it end to end against a stand-in tool — three mutants, two
		// killed, one 90-second sleep — and the gate passed with
		// `mutants_survived=0`. Decision 17 still governs the SCORE's
		// denominator, which is a different question from what the gate
		// reads; the score is gated by nothing and says so.
		survived, ok := metric(MetricMutantsSurvived)
		if !ok {
			return false, absent(MetricMutantsSurvived)
		}
		timeout, ok := metric(MetricMutantsTimeout)
		if !ok {
			return false, absent(MetricMutantsTimeout)
		}
		if survived+timeout <= g.Threshold {
			return true, ""
		}
		return false, fmt.Sprintf("mutants_survived=%d mutants_timeout=%d (want survived+timeout <= %d)",
			survived, timeout, g.Threshold)
	case GateDifferentialCohortAtLeast:
		n, ok := metric(MetricDiffCohortN)
		if !ok {
			return false, absent(MetricDiffCohortN)
		}
		if n >= g.Threshold {
			return true, ""
		}
		return false, fmt.Sprintf("diff_cohort_n=%d (want >= %d)", n, g.Threshold)
	case GatePathsUnmodified:
		// The gate passes iff every VIOLATING count is 0 and
		// paths_examined is present. protected_added is a violation only
		// when the policy refuses additions: a candidate that adds a
		// regression test is doing the right thing (decision 6).
		examined, ok := metric(MetricPathsExamined)
		if !ok {
			return false, absent(MetricPathsExamined)
		}
		_ = examined
		counts := make([]int64, 0, 6)
		for _, name := range guardCountMetrics {
			v, ok := metric(name)
			if !ok {
				return false, absent(name)
			}
			counts = append(counts, v)
		}
		added, ok := metric(MetricProtectedAdded)
		if !ok {
			return false, absent(MetricProtectedAdded)
		}
		violating := counts[0]+counts[1]+counts[2]+counts[3]+counts[4] > 0 ||
			(g.RefuseAdditions && added > 0)
		if !violating {
			return true, ""
		}
		return false, fmt.Sprintf(
			"protected_modified=%d protected_deleted=%d harness_modified=%d harness_deleted=%d harness_added=%d (first: %s)",
			counts[0], counts[1], counts[2], counts[3], counts[4], g.firstOffender(rec))
	default:
		// Unreachable by construction: validation refused every unknown
		// predicate at load, and the v0 dialect compiles its unknown gates
		// to AlwaysFails. Fail closed anyway — what cannot be attested must
		// not be admitted.
		return false, "unknown gate"
	}
}

func absent(metric string) string { return metric + " absent (source unavailable)" }
