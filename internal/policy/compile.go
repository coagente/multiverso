package policy

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
)

var (
	rePolicyName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	reOracleName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
)

// Validate reports EVERY problem with a v1 policy as one joined error
// (Problems splits it back apart for `mvo policy validate`). Unknown gate
// predicate, ranking key, freshness basis, oracle kind, or an oracle name a
// gate references but the policy does not declare are all refused here, at
// load — so the decision functions never see a name they cannot evaluate
// (M1e decision 7).
func Validate(p object.PolicyV1) error {
	var probs []error
	add := func(field, format string, a ...any) {
		probs = append(probs, Problem{Field: field, Detail: fmt.Sprintf(format, a...)})
	}

	if p.Schema != object.SchemaPolicyV1 {
		add("schema", "schema %q, want %q", p.Schema, object.SchemaPolicyV1)
	}
	if !rePolicyName.MatchString(p.Name) {
		add("name", "invalid policy name %q (want %s)", p.Name, rePolicyName)
	}

	// --- oracles ---------------------------------------------------------
	// declared holds every name the document declares, valid or not, so a
	// gate naming an oracle whose KIND is bad is not also accused of naming
	// an oracle that does not exist.
	declared := make(map[string]bool, len(p.Oracles))
	byName := make(map[string]object.OracleSpec, len(p.Oracles))
	byConfig := make(map[string]string, len(p.Oracles))
	for _, o := range p.Oracles {
		declared[o.Name] = true
	}
	for i, o := range p.Oracles {
		at := fmt.Sprintf("oracles[%d]", i)
		if !reOracleName.MatchString(o.Name) {
			add(at+".name", "invalid oracle name %q (want %s)", o.Name, reOracleName)
		} else if _, dup := byName[o.Name]; dup {
			add(at+".name", "duplicate oracle name %q", o.Name)
		}
		_, knownKind := kindDefs[o.Kind]
		if !knownKind {
			add(at+".kind", "unknown oracle kind %q (known: %s)", o.Kind, known(KnownKinds()))
		}
		if o.Kind == KindCommand && len(o.Argv) == 0 {
			add(at+".argv", "kind %q requires a non-empty argv", KindCommand)
		}
		if o.TimeoutMS < 0 {
			add(at+".timeout_ms", "timeout_ms %d is negative", o.TimeoutMS)
		}
		if o.Reruns < 0 {
			add(at+".reruns", "reruns %d is negative", o.Reruns)
		} else if o.Reruns > 0 && o.Kind != KindPytestSuite {
			add(at+".reruns", "reruns is only meaningful for kind %q, not %q", KindPytestSuite, o.Kind)
		}
		if !knownKind || o.Name == "" {
			continue
		}
		if _, dup := byName[o.Name]; !dup {
			byName[o.Name] = o
		}
		cfg, err := ConfigDigest(o)
		if err != nil {
			add(at, "resolve config: %v", err)
		} else if prev, dup := byConfig[cfg]; dup {
			add(at, "resolved config %s duplicates oracle %q: one instance under two names", cfg, prev)
		} else {
			byConfig[cfg] = o.Name
		}
	}

	// --- hard gates ------------------------------------------------------
	if len(p.HardGates) == 0 {
		add("hard_gates", "a policy must declare at least one hard gate")
	}
	for i, g := range p.HardGates {
		at := fmt.Sprintf("hard_gates[%d]", i)
		def, ok := gateDefs[g.Gate]
		if !ok {
			add(at+".gate", "unknown gate %q (known: %s)", g.Gate, known(KnownGates()))
		}
		if BasisRank(g.Basis) == 0 {
			add(at+".basis", "unknown freshness basis %q (known: %s)", g.Basis, known(KnownBases()))
		}
		if g.Threshold < 0 {
			add(at+".threshold", "threshold %d is negative", g.Threshold)
		}
		// gateDef.threshold is what makes "this gate takes a parameter"
		// decidable, so a number the predicate will silently discard is an
		// authoring error, not a harmless extra: the file would read as if
		// it demanded something it does not (the runtime surprise the closed
		// vocabulary exists to prevent).
		if ok && !def.threshold && g.Threshold != 0 {
			add(at+".threshold", "gate %q takes no threshold parameter (got %d)", g.Gate, g.Threshold)
		}
		if !declared[g.Oracle] {
			add(at+".oracle", "gate names oracle %q, which the policy does not declare", g.Oracle)
			continue
		}
		spec, usable := byName[g.Oracle]
		if !ok || !usable {
			continue // the gate or the oracle's kind is already reported
		}
		for _, m := range def.metrics {
			// Instance-level, not kind-level: an oracle that CAN emit a
			// metric but is configured never to is just as unsatisfiable as
			// one whose kind never emits it at all.
			if !SpecEmits(spec, m) {
				add(at+".gate", "gate %q needs metric %q, which oracle %q (kind %q) does not emit",
					g.Gate, m, g.Oracle, spec.Kind)
			}
		}
	}

	// --- ranking ---------------------------------------------------------
	seen := make(map[string]bool, len(p.Ranking))
	for i, name := range p.Ranking {
		at := fmt.Sprintf("ranking[%d]", i)
		def, ok := keyDefs[name]
		if !ok {
			add(at, "unknown ranking key %q (known: %s)", name, known(KnownKeys()))
			continue
		}
		if seen[name] {
			add(at, "duplicate ranking key %q", name)
			continue
		}
		seen[name] = true
		if name == KeyWorldDigestAsc && i != len(p.Ranking)-1 {
			add(at, "%q is the terminal ranking key: nothing can follow it", KeyWorldDigestAsc)
		}
		if def.metric == "" {
			continue
		}
		if _, err := resolveMetric(p.Oracles, name, def.metric); err != nil {
			add(at, "%s", err)
		}
	}

	// --- escalation ------------------------------------------------------
	if p.Escalation.MinCandidatesPassing < 0 {
		add("escalation.min_candidates_passing", "min_candidates_passing %d is negative",
			p.Escalation.MinCandidatesPassing)
	}
	for i, name := range p.Escalation.RequireEvidence {
		if !declared[name] {
			add(fmt.Sprintf("escalation.require_evidence[%d]", i),
				"requires evidence from oracle %q, which the policy does not declare", name)
		}
	}
	return errors.Join(probs...)
}

// emits reports whether a kind's metric vocabulary contains m.
func emits(kind, metric string) bool {
	for _, m := range kindDefs[kind].metrics {
		if m == metric {
			return true
		}
	}
	return false
}

// resolveMetric finds the unique declared oracle whose kind emits metric.
// Ambiguity is refused at load, never resolved by a coin flip at decide
// time; per-oracle key qualification (coverage_desc@suite-fast) is v1.
func resolveMetric(specs []object.OracleSpec, key, metric string) (object.OracleSpec, error) {
	hits := make([]object.OracleSpec, 0, len(specs))
	for _, o := range specs {
		// The candidate set is instance-level (SpecEmits): a coverage_desc
		// key against a coverage-disabled suite oracle resolves to nothing
		// and is refused, rather than resolving to an oracle that will never
		// produce the number.
		if SpecEmits(o, metric) {
			hits = append(hits, o)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Name < hits[j].Name })
	switch len(hits) {
	case 0:
		return object.OracleSpec{}, fmt.Errorf("ranking key %q needs metric %q, which no declared oracle emits", key, metric)
	case 1:
		return hits[0], nil
	default:
		names := make([]string, 0, len(hits))
		for _, o := range hits {
			names = append(names, o.Name)
		}
		return object.OracleSpec{}, fmt.Errorf("ranking key %q is ambiguous: oracles [%s] both emit %q",
			key, strings.Join(names, ", "), metric)
	}
}

// ConfigDigest is an oracle instance's identity in evidence: the digest of
// its RESOLVED run configuration with the policy-local name excluded (M1e
// decision 8). The registry copies it into receipt.oracle.config, so two
// policies that run the identical command produce comparable evidence.
// timeout_ms stays as declared (0 = "the intent's max_wall_ms"): resolving
// it against an intent would make the identity intent-dependent.
func ConfigDigest(spec object.OracleSpec) (string, error) {
	dig, _, err := object.Digest(map[string]any{
		"args":       nonNil(spec.Args),
		"argv":       ResolvedArgv(spec),
		"coverage":   spec.Coverage,
		"kind":       spec.Kind,
		"reruns":     spec.Reruns,
		"timeout_ms": spec.TimeoutMS,
	})
	if err != nil {
		return "", fmt.Errorf("policy: digest oracle config: %w", err)
	}
	return dig, nil
}

// ResolvedArgv applies the kind's argv default: pytest kinds default to the
// python3 -m pytest runner prefix; command kinds carry their own full argv.
func ResolvedArgv(spec object.OracleSpec) []string {
	if len(spec.Argv) > 0 {
		return append([]string(nil), spec.Argv...)
	}
	switch spec.Kind {
	case KindPytestCollect, KindPytestSuite:
		return DefaultPytestPrefix()
	default:
		return []string{}
	}
}

// Compile turns a VALIDATED v1 policy into the in-memory form the decision
// functions evaluate. dig is the digest of the bytes it was loaded from.
func Compile(dig string, p object.PolicyV1) (Policy, error) {
	out := Policy{
		Digest:  dig,
		Schema:  object.SchemaPolicyV1,
		Dialect: DialectV1,
		Name:    p.Name,
		Ranking: append([]string{}, p.Ranking...),
	}

	byName := make(map[string]Oracle, len(p.Oracles))
	for _, spec := range p.Oracles {
		cfg, err := ConfigDigest(spec)
		if err != nil {
			return Policy{}, err
		}
		o := Oracle{
			Name:      spec.Name,
			Kind:      spec.Kind,
			Family:    KindFamily(spec.Kind),
			Config:    cfg,
			Argv:      ResolvedArgv(spec),
			Args:      nonNil(spec.Args),
			TimeoutMS: spec.TimeoutMS,
			Coverage:  spec.Coverage,
			Reruns:    spec.Reruns,
		}
		byName[o.Name] = o
		out.Oracles = append(out.Oracles, o)
	}
	sort.Slice(out.Oracles, func(i, j int) bool { return out.Oracles[i].Name < out.Oracles[j].Name })

	selFor := func(name string) Selector {
		o := byName[name]
		return Selector{ID: o.Kind, Config: o.Config}
	}

	var required []string
	for _, g := range p.HardGates {
		out.Gates = append(out.Gates, Gate{
			Predicate: g.Gate,
			Oracle:    g.Oracle,
			Basis:     g.Basis,
			Threshold: g.Threshold,
			Label:     g.Gate + "@" + g.Oracle,
			Sel:       selFor(g.Oracle),
		})
		required = append(required, g.Oracle)
	}

	keys, keyOracles, err := effectiveKeys(p, byName)
	if err != nil {
		return Policy{}, err
	}
	out.Keys = keys
	required = append(required, keyOracles...)

	out.Esc = Escalation{
		MinCandidatesPassing:       p.Escalation.MinCandidatesPassing,
		OnRankingTie:               p.Escalation.OnRankingTie,
		OnAllWorldsFailedMachinery: p.Escalation.OnAllWorldsFailedMachinery,
	}
	reqNames := append([]string{}, p.Escalation.RequireEvidence...)
	sort.Strings(reqNames)
	for _, name := range dedup(reqNames) {
		out.Esc.RequireEvidence = append(out.Esc.RequireEvidence,
			Requirement{OracleName: name, Sel: selFor(name)})
		required = append(required, name)
	}
	// Required is the set the race must actually run, in ladder order:
	// declared-but-unrequired oracles are never run (evidence waste is a
	// measured PRD metric).
	out.Required = dedup(required)
	return out, nil
}

// effectiveKeys builds the effective ranking list: gate_pass first, the
// policy's keys next (minus any explicit gate_pass/world_digest_asc), and
// world_digest_asc last (M1e decision 4). Listing either explicitly is
// legal and changes nothing.
func effectiveKeys(p object.PolicyV1, byName map[string]Oracle) ([]Key, []string, error) {
	keys := []Key{{Name: KeyGatePass, Desc: true}}
	var oracles []string
	for _, name := range p.Ranking {
		if name == KeyGatePass || name == KeyWorldDigestAsc {
			continue
		}
		def, ok := keyDefs[name]
		if !ok {
			return nil, nil, Problem{Field: "ranking", Detail: fmt.Sprintf("unknown ranking key %q", name)}
		}
		k := Key{Name: name, Desc: def.desc, Metric: def.metric}
		if def.metric != "" {
			spec, err := resolveMetric(p.Oracles, name, def.metric)
			if err != nil {
				return nil, nil, Problem{Field: "ranking", Detail: err.Error()}
			}
			o := byName[spec.Name]
			k.Sel = Selector{ID: o.Kind, Config: o.Config}
			oracles = append(oracles, o.Name)
		}
		keys = append(keys, k)
	}
	keys = append(keys, Key{Name: KeyWorldDigestAsc})
	return keys, oracles, nil
}

// compileV0 compiles M0's frozen policy shape into the same in-memory form,
// reproducing M0's decisions exactly: the family selector, the fail-closed
// unknown gate, the ignored unknown ranking key, and the M0 rationale
// dialect (M1e decision 2 and the normative v0 table).
func compileV0(dig string, p object.Policy) Policy {
	out := Policy{
		Digest:   dig,
		Schema:   object.SchemaPolicy,
		Dialect:  DialectV0,
		Ranking:  append([]string{}, p.Ranking...),
		Required: []string{FamilySuite},
	}
	suiteSel := Selector{Family: FamilySuite}
	for _, name := range p.HardGates {
		g := Gate{
			Predicate: name,
			Basis:     object.BasisConstruction,
			Label:     name,
			Sel:       suiteSel,
		}
		if name != GateSuitePass {
			// M0's fail-closed rule: a gate it could not evaluate failed.
			g.AlwaysFails = true
		}
		out.Gates = append(out.Gates, g)
	}
	out.Keys = []Key{{Name: KeyGatePass, Desc: true}}
	for _, name := range p.Ranking {
		if name == KeyGatePass || name == KeyWorldDigestAsc {
			continue
		}
		// M0's rankLess honored exactly two names; every other name — known
		// to M1e or not — was a no-op, and replay must keep it one.
		if name != KeyWallMSAsc {
			out.Keys = append(out.Keys, Key{Name: name, NoOp: true})
			continue
		}
		out.Keys = append(out.Keys, Key{Name: KeyWallMSAsc, Sel: suiteSel})
	}
	out.Keys = append(out.Keys, Key{Name: KeyWorldDigestAsc})
	return out
}

// Default is the v1 policy `mvo init` writes to
// .multiverso/policies/default.json (M1e decision 19): the Python ladder,
// ordered so a test-deleting candidate is stopped by O0's counts before its
// suite is ever run. no-failed-tests and coverage-at-least are shipped and
// tested but stay out of the default: a default gate that fails when a
// plugin is missing would trade honesty for brittleness at the moment a new
// user first meets the tool.
func Default() object.PolicyV1 {
	return object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "default",
		Oracles: []object.OracleSpec{
			{Name: "collect", Kind: KindPytestCollect, Argv: []string{}, Args: []string{}, Coverage: true},
			{Name: "suite", Kind: KindPytestSuite, Argv: []string{}, Args: []string{}, Coverage: true},
		},
		HardGates: []object.GateSpec{
			{Gate: GateCollectNonempty, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: GateCollectedNotBelow, Oracle: "collect", Basis: object.BasisConstruction},
			{Gate: GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking: []string{KeyGatePass, KeyTestsPassedDesc, KeyWallMSAsc},
		Escalation: object.EscalationSpec{
			RequireEvidence:            []string{},
			OnAllWorldsFailedMachinery: true,
		},
	}
}

// Command synthesizes the command-oracle policy behind
// `mvo intent new --oracle-cmd` (M1e decision 18): the migration path that
// puts the gate's command INSIDE the pinned, attested artifact, where the
// policy digest determines it.
func Command(argv []string, timeoutMS int64) object.PolicyV1 {
	return object.PolicyV1{
		Schema: object.SchemaPolicyV1,
		Name:   "command",
		Oracles: []object.OracleSpec{{
			Name:      "suite",
			Kind:      KindCommand,
			Argv:      append([]string{}, argv...),
			Args:      []string{},
			TimeoutMS: timeoutMS,
		}},
		HardGates: []object.GateSpec{
			{Gate: GateStatusPass, Oracle: "suite", Basis: object.BasisConstruction},
		},
		Ranking:    []string{KeyGatePass, KeyWallMSAsc},
		Escalation: object.EscalationSpec{RequireEvidence: []string{}},
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return append([]string{}, s...)
}

func dedup(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
