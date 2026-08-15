package policy

import (
	"fmt"
	"sort"
)

// The closed invariant vocabulary (M1f decision 10). An invariant is
// {name, oracles: {role → declared oracle name}}: the vocabulary fixes
// which metrics each role supplies and how they are compared; the policy
// only says which INSTANCES fill the roles.
//
// Four reasons, transferred unchanged from M1e decision 6's argument for
// the closed escalation set. Totality: a closed struct cannot fail to
// evaluate. Threat model: no evaluator over evidence runs inside the trust
// boundary. Explainability: one frozen sentence per invariant. Validation:
// an unknown name is a load error, not a runtime surprise.
const (
	InvariantCollectEqualsSuiteTotal = "collect-equals-suite-total"
	InvariantSuitePartsSumToTotal    = "suite-parts-sum-to-total"
)

// Invariant roles. They are vocabulary, not free text: the role set of an
// instance must equal its invariant's declared set exactly.
const (
	RoleCollect = "collect"
	RoleSuite   = "suite"
)

// metricLookup resolves one (role, metric) pair to its recorded value.
type metricLookup func(role, metric string) int64

// invariantDef declares one invariant: its role set, the metrics each role
// must supply, whether the named oracles must carry equal args, and the
// frozen predicate over the resolved metrics.
type invariantDef struct {
	roles    []string
	metrics  map[string][]string
	equalArg []string // roles whose declared args must be identical (rule 10)
	holds    func(m metricLookup) (bool, string)
}

var invariantDefs = map[string]invariantDef{
	InvariantCollectEqualsSuiteTotal: {
		roles: []string{RoleCollect, RoleSuite},
		metrics: map[string][]string{
			RoleCollect: {MetricCollectedTotal},
			RoleSuite:   {MetricTestsTotal},
		},
		// Two pytest invocations given different selection arguments
		// legitimately collect different counts, and an invariant that
		// fires on a correct configuration is a bug generator (rule 10).
		equalArg: []string{RoleCollect, RoleSuite},
		holds: func(m metricLookup) (bool, string) {
			collected := m(RoleCollect, MetricCollectedTotal)
			total := m(RoleSuite, MetricTestsTotal)
			if collected == total {
				return true, ""
			}
			return false, fmt.Sprintf("collected_total=%d != tests_total=%d", collected, total)
		},
	},
	InvariantSuitePartsSumToTotal: {
		roles: []string{RoleSuite},
		metrics: map[string][]string{
			RoleSuite: {
				MetricTestsTotal, MetricTestsPassed, MetricTestsFailed,
				MetricTestsErrored, MetricTestsSkipped,
			},
		},
		holds: func(m metricLookup) (bool, string) {
			total := m(RoleSuite, MetricTestsTotal)
			parts := m(RoleSuite, MetricTestsPassed) + m(RoleSuite, MetricTestsFailed) +
				m(RoleSuite, MetricTestsErrored) + m(RoleSuite, MetricTestsSkipped)
			if total == parts {
				return true, ""
			}
			return false, fmt.Sprintf("tests_total=%d != passed+failed+errored+skipped=%d", total, parts)
		},
	},
}

// KnownInvariants returns the vocabulary, sorted — the "(known: …)" tail
// of every load error that names one.
func KnownInvariants() []string { return sortedKeys(invariantDefs) }

// InvariantRoles returns an invariant's declared role set, sorted.
func InvariantRoles(name string) []string {
	def, ok := invariantDefs[name]
	if !ok {
		return nil
	}
	out := append([]string(nil), def.roles...)
	sort.Strings(out)
	return out
}

// InvariantMetrics returns the metrics one role of an invariant supplies,
// sorted — validation rule 9's instance-level test reads this.
func InvariantMetrics(name, role string) []string {
	def, ok := invariantDefs[name]
	if !ok {
		return nil
	}
	out := append([]string(nil), def.metrics[role]...)
	sort.Strings(out)
	return out
}

// Invariant is the compiled form: each role resolved to the Selector that
// picks its counted receipt.
type Invariant struct {
	Name  string
	Roles map[string]Selector
}

// RoleNames returns this instance's roles, sorted — a stable render order
// for explain and for the load errors that list them.
func (inv Invariant) RoleNames() []string {
	out := make([]string, 0, len(inv.Roles))
	for role := range inv.Roles {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}

// Holds applies the invariant to the metrics `lookup` resolves per (role,
// metric). A required metric that is ABSENT makes the invariant VIOLATED,
// with detail "%s absent (source unavailable)" — the honesty rule again:
// an invariant that cannot be evaluated does not hold by default. Metrics
// are read in the vocabulary's sorted order so the reported absence is
// deterministic.
func Holds(inv Invariant, lookup func(role, metric string) (int64, bool)) (bool, string) {
	def, ok := invariantDefs[inv.Name]
	if !ok {
		// Unreachable: validation refused every unknown name at load. Fail
		// closed anyway — what cannot be checked must not be asserted.
		return false, fmt.Sprintf("unknown invariant %q", inv.Name)
	}
	vals := map[string]int64{}
	for _, role := range InvariantRoles(inv.Name) {
		for _, m := range InvariantMetrics(inv.Name, role) {
			v, present := lookup(role, m)
			if !present {
				return false, absent(m)
			}
			vals[role+"\x00"+m] = v
		}
	}
	return def.holds(func(role, metric string) int64 { return vals[role+"\x00"+metric] })
}
