package policy

// M2a O3 — the diff-scoped mutation rung's policy vocabulary, and O2p's one
// authoring rule. Both live here rather than in vocab.go because both are
// about a BUDGET or a HARNESS PATH, and neither is a metric or a predicate.
//
// Google's recipe, transplanted with its constraints intact (ch. 8 §8.1:
// exhaustive mutation "does not scale"; diff-based mutation with per-line
// caps and operator selection yields "orders of magnitude fewer mutants").
// The cap is therefore not a tuning knob: it is the thing that makes the
// rung purchasable at all, which is why it is pinned in the attested policy
// (decision 11) rather than passed on a command line.

import (
	"fmt"
	"sort"

	"github.com/coagente/multiverso/internal/object"
)

// Mutation tools. `auto` — the "" default — resolves to cosmic-ray when it
// is importable and mutmut otherwise, and the RESOLVED name is recorded in
// result.tools, never the word "auto": a receipt must not record a word
// that means "it depends".
//
// cosmic-ray is the v0 default, reversing ch. 19's recommendation, and the
// reason is stated rather than implied (decision 19). Ch. 19 preferred
// mutmut "for speed and incremental state"; M2a forbids incremental state
// (decision 18) and requires control-plane mutant SELECTION (decision 17),
// which inverts the trade — cosmic-ray's init → session → exec model is the
// only one of the two that lets the control plane enumerate mutants, apply
// the canonical order and the cap, and then execute exactly that set.
const (
	MutationToolAuto      = "auto"
	MutationToolCosmicRay = "cosmic-ray"
	MutationToolMutmut    = "mutmut"
)

// KnownMutationTools returns the legal mutation.tool values, sorted.
func KnownMutationTools() []string {
	return []string{MutationToolAuto, MutationToolCosmicRay, MutationToolMutmut}
}

// Mutation budget defaults (M2a's wire schema). They are small on purpose:
// a first mutation run that costs a minute is a rung nobody schedules
// twice.
const (
	DefaultMaxMutants = 20
	DefaultMaxPerLine = 2
	// MutantTimeoutFactor is cargo-mutants' heuristic (ch. 19): a mutant
	// that takes five times the unmutated suite is hung, not slow. It
	// applies only when the policy declares no explicit per-mutant bound.
	MutantTimeoutFactor = 5
)

// Mutation operator FAMILIES. The vocabulary is ours, not a tool's, and
// that is deliberate: cosmic-ray and mutmut spell their operators
// differently, neither is installed on the machine this was written on, and
// a policy that pinned one tool's private spelling would be unportable and
// unverifiable at load. A family is what an operator DOES; the adapter maps
// it onto the tool's own names, and a family the selected tool cannot
// express is a load error naming both (rule 20).
const (
	OpArithmetic = "arithmetic"   // binary and unary arithmetic replacement
	OpComparison = "comparison"   // <, <=, ==, !=, >, >= replacement
	OpBoolean    = "boolean"      // and/or/not, True/False replacement
	OpConstant   = "constant"     // numeric and string constant replacement
	OpControl    = "control-flow" // break/continue, loop-body elision
	OpException  = "exception"    // raised/caught exception replacement
	OpDecorator  = "decorator"    // decorator removal
)

// KnownMutationOperators returns the legal mutation.operators values,
// sorted.
func KnownMutationOperators() []string {
	return []string{OpArithmetic, OpBoolean, OpComparison, OpConstant,
		OpControl, OpDecorator, OpException}
}

// ResolvedMutation applies the wire defaults exactly once, here, where they
// can be tested: a caller reading MaxMutants gets a CEILING, never a
// sentinel that some other caller might read as "unbounded". Operators are
// returned sorted and deduplicated so an instance's identity in evidence
// does not depend on the order an author happened to type.
//
// A spec of any other kind resolves to the ZERO value, not to the defaults:
// a pytest-suite instance carrying a mutation budget it will never spend
// would read as if the policy had bought something it did not.
func ResolvedMutation(spec object.OracleSpec) object.MutationSpec {
	if spec.Kind != KindMutationDiff {
		return object.MutationSpec{}
	}
	out := spec.Mutation
	if out.Tool == "" {
		out.Tool = MutationToolAuto
	}
	if out.MaxMutants == 0 {
		out.MaxMutants = DefaultMaxMutants
	}
	if out.MaxPerLine == 0 {
		out.MaxPerLine = DefaultMaxPerLine
	}
	ops := append([]string(nil), spec.Mutation.Operators...)
	sort.Strings(ops)
	out.Operators = dedup(ops)
	return out
}

// validateMutation applies M2a validation rule 20 to one declared oracle.
//
// The negative half is as load-bearing as the positive one: a mutation
// budget on an oracle that runs no mutants reads as if the policy bought
// something it did not, which is the runtime surprise the closed vocabulary
// exists to prevent (the M1f rule-15 argument, reused).
func validateMutation(at string, o object.OracleSpec, add addProblem) {
	m := o.Mutation
	if o.Kind != KindMutationDiff {
		if !m.IsZero() {
			add(at+".mutation", "mutation is a %q setting, not %q", KindMutationDiff, o.Kind)
		}
		return
	}
	// A mutation run is a suite run per mutant, so coverage would measure
	// a mutated tree — a number nobody can act on — and a corpus is the
	// differential's input, not this rung's.
	if o.Coverage {
		add(at+".coverage", "kind %q measures suite adequacy, not coverage: coverage must be unset", KindMutationDiff)
	}
	if o.Corpus != (object.CorpusSpec{}) {
		add(at+".corpus", "kind %q consumes no corpus: corpus must be unset", KindMutationDiff)
	}
	if m.MaxMutants < 0 {
		add(at+".mutation.max_mutants", "max_mutants %d is negative", m.MaxMutants)
	}
	if m.MaxPerLine < 0 {
		add(at+".mutation.max_per_line", "max_per_line %d is negative", m.MaxPerLine)
	}
	if m.TimeoutPerMutant < 0 {
		add(at+".mutation.timeout_per_mutant_ms", "timeout_per_mutant_ms %d is negative", m.TimeoutPerMutant)
	}
	validateEnum(at+".mutation.tool", m.Tool, KnownMutationTools(), add)
	validateOperators(at, m, add)
}

// validateOperators applies rule 20's operator clause: sorted, unique, and
// in the selected tool's declared set.
//
// mutmut selects its own mutants and exposes no operator switch, so
// declaring operators against it is refused BY NAME rather than silently
// ignored — the honest form of decision 19's "strictly weaker provenance,
// labelled rather than silently equivalent".
func validateOperators(at string, m object.MutationSpec, add addProblem) {
	if len(m.Operators) == 0 {
		return
	}
	if m.Tool == MutationToolMutmut {
		add(at+".mutation.operators",
			"tool %q selects its own mutants and exposes no operator switch; declare tool %q to choose operators",
			MutationToolMutmut, MutationToolCosmicRay)
	}
	legal := make(map[string]bool, len(KnownMutationOperators()))
	for _, op := range KnownMutationOperators() {
		legal[op] = true
	}
	seen := make(map[string]bool, len(m.Operators))
	for i, op := range m.Operators {
		field := fmt.Sprintf("%s.mutation.operators[%d]", at, i)
		switch {
		case !legal[op]:
			add(field, "unknown mutation operator %q (known: %s)", op, known(KnownMutationOperators()))
		case seen[op]:
			add(field, "duplicate mutation operator %q", op)
		case i > 0 && m.Operators[i-1] > op:
			add(field, "mutation operators must be sorted (%q follows %q)", op, m.Operators[i-1])
		}
		seen[op] = true
	}
}

// validateProperties applies rule 16's property clause: a policy that gates
// on a hypothesis-properties oracle must name the property module.
//
// Where the properties come from is decision 14: the repository's own
// @given tests, PLUS a policy-declared module — and the module path joins
// the compiled harness set, so a candidate that rewrites it to make every
// property vacuous dies at rung O-1 before any Python runs (corpus vector
// 16). "The repo has @given tests" is not checkable at load, so the module
// is required exactly when a gate depends on the answer; a properties
// oracle nobody gates on may rely on the repository alone.
func validateProperties(p object.PolicyV1, byName map[string]object.OracleSpec, add addProblem) {
	for i, g := range p.HardGates {
		spec, ok := byName[g.Oracle]
		if !ok || spec.Kind != KindProperties || spec.Corpus.Module != "" {
			continue
		}
		add(fmt.Sprintf("hard_gates[%d].oracle", i),
			"gate %q reads oracle %q (kind %q), which declares no corpus.module: the property module is the harness-frozen source of the properties, and a repository's own @given tests cannot be verified at load",
			g.Gate, g.Oracle, KindProperties)
	}
}
