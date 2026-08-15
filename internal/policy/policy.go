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
}

// Gate is one ordered hard gate.
type Gate struct {
	Predicate string // vocab constant
	Oracle    string // declared oracle name ("" in the v0 dialect)
	Basis     string // minimum acceptable basis
	Threshold int64
	Label     string // rendered gate name: "suite-pass" (v0) | "status-pass@suite" (v1)
	Sel       Selector
	// AlwaysFails carries M0's fail-closed unknown-gate rule into the v0
	// dialect: what M0 could not attest, it did not admit, and replay must
	// reproduce that (M1e decision 7).
	AlwaysFails bool
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
func (p Policy) GateOracles() []Oracle {
	seen := make(map[string]bool, len(p.Gates))
	out := make([]Oracle, 0, len(p.Gates))
	for _, g := range p.Gates {
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
	default:
		// Unreachable by construction: validation refused every unknown
		// predicate at load, and the v0 dialect compiles its unknown gates
		// to AlwaysFails. Fail closed anyway — what cannot be attested must
		// not be admitted.
		return false, "unknown gate"
	}
}

func absent(metric string) string { return metric + " absent (source unavailable)" }
