package oracle

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// SchemaDifferentialReport is the control-plane-authored artifact every
// comparison receipt points at: one artifact, N referrers.
const SchemaDifferentialReport = "multiverso.dev/differential-report/v0"

// maxDistinguishing caps the report's distinguishing-case list. The
// overflow is COUNTED, never dropped silently. An unbounded list would be a
// candidate-driven denial of a maintainer's attention: the report is the
// ESCALATE payload, so its length is something a hostile candidate would
// otherwise get to choose.
const maxDistinguishing = 32

// valueRenderCap bounds the encoded value carried inline in a report and
// on the wire. Beyond it the record carries the fingerprint alone and says
// it was truncated.
const valueRenderCap = 512

// CohortMember is one world's usable observation, paired with the digest of
// the world it came from.
type CohortMember struct {
	World string
	Obs   Observation
}

// DifferentialInputs is everything the reducer reads. There is no world
// handle, no backend, no CAS and no clock in it, and that is the point:
// `corpus-differential` executes no candidate code and reads no candidate
// byte directly, so its receipt's evidence regime is `derived` and its
// evidence FLOOR is named rather than flattered (M2a decision 3).
type DifferentialInputs struct {
	Corpus       Corpus
	CorpusDigest string // "mv0:…"
	BaseTree     string // "git:…"
	// BaseObservation is the CAS key of the base tree's own observation
	// stream — the ANCHOR the "vs base" counts are measured against, and
	// never a cohort member. The base tree is nobody's candidate.
	BaseObservation string
	Base            Observation
	Members         []CohortMember
	// Spec identifies the declared reducer instance whose receipts these
	// are: kind, version and the resolved-config digest a gate selects on.
	Spec policy.Oracle
}

// DifferentialResult is what one cohort barrier produced.
type DifferentialResult struct {
	Report       json.RawMessage // canonical bytes, ready for CAS.Put
	ReportKey    string          // "sha256:…", the key those bytes will land under
	CohortDigest string          // "mv0:…" over the sorted world-digest list
	// Cohort is that list's canonical bytes. They are tiny and they are
	// STORED, because a receipt whose provenance names a digest nothing
	// can resolve is a citation to a missing page: `mvo audit`'s sweep
	// walks inputs[*], and "who was this world compared against" must be a
	// question the recorded closure can answer.
	Cohort   []byte
	Classes  int
	Compared int64
	// FirstDistinguishing is the smallest case id on which the cohort
	// disagrees; "" when it agrees everywhere. It is what the escalation
	// sentence hands the maintainer, because "your candidates disagree" is
	// useless without "…on this input".
	FirstDistinguishing string
	// Receipts is ONE RECEIPT PER COHORT MEMBER, in cohort order, each
	// bound to exactly one world and carrying that world's POSITION IN THE
	// COMPARISON as its metrics (M2a decision 1). World and
	// Freshness.ValidFor are completed by the orchestrator, which alone
	// knows a world's object digest — exactly as for every other kind.
	Receipts []object.Receipt
}

// reportClass is one behaviour class in the report.
type reportClass struct {
	AgreesWithBase bool     `json:"agrees_with_base"`
	ID             string   `json:"id"`
	Members        []string `json:"members"`
}

// reportObservation is one world's answer on a distinguishing case.
type reportObservation struct {
	FP        string          `json:"fp"`
	Outcome   string          `json:"outcome"`
	Truncated bool            `json:"truncated,omitempty"`
	Type      string          `json:"t,omitempty"`
	Value     json.RawMessage `json:"v,omitempty"`
	World     string          `json:"world,omitempty"`
}

// reportCase is one distinguishing case: the INPUT and what each candidate
// returned on it. This is the artifact the whole block exists to produce —
// the honest consumer of a behavioural split is a human, and the honest
// payload is the input plus both answers (M2a decision 7).
type reportCase struct {
	Args         []json.RawMessage          `json:"args"`
	Base         reportObservation          `json:"base"`
	Case         string                     `json:"case"`
	Kwargs       map[string]json.RawMessage `json:"kwargs"`
	Observations []reportObservation        `json:"observations"`
	Target       string                     `json:"target"`
}

// differentialReport is the report artifact's wire shape.
//
// Three fields beyond M2a's sketch, all control-plane authored and all
// added because the sketch let a real denial go unrecorded:
//
//   - `provider` — which provider produced the corpus. Decision 5 documents
//     `repo-suite` as nearly information-free, so "which corpus is this"
//     is exactly the fact a reader needs to weigh a comparison, and
//     `mvo explain` prints it.
//   - `member_cases_comparable` — per world, how many cases THIS member
//     contributed to the intersection. The denominator is an intersection
//     over every member, so one member's opacity shrinks it for everyone;
//     recording each member's contribution is what makes the shrinkage
//     attributable instead of anonymous.
//   - `excluded` — members dropped for contributing NOTHING. A member that
//     observed no case comparably compared nothing, so it is not part of a
//     comparison; keeping it would let it delete the comparison for the
//     whole race while passing its own corpus-complete gate.
type differentialReport struct {
	BaseObservation         string           `json:"base_observation"`
	BaseTree                string           `json:"base_tree"`
	CasesCompared           int64            `json:"cases_compared"`
	CasesIncomparable       int64            `json:"cases_incomparable"`
	Classes                 []reportClass    `json:"classes"`
	Cohort                  []string         `json:"cohort"`
	Corpus                  string           `json:"corpus"`
	Distinguishing          []reportCase     `json:"distinguishing"`
	DistinguishingTruncated int64            `json:"distinguishing_truncated"`
	Excluded                []string         `json:"excluded"`
	MemberCasesComparable   map[string]int64 `json:"member_cases_comparable"`
	Provider                string           `json:"provider"`
	Schema                  string           `json:"schema"`
}

// Reduce is the cohort-stage reducer: a PURE function of the corpus, the
// base observation, the cohort's observations and the cohort's world
// digests, emitting one receipt per member plus the report they all point
// at.
//
// Pure is not an aesthetic here. It is what keeps every M1 contract intact:
// `Decide` stays a per-world predicate over a per-world counted receipt,
// replay stays a re-run of the same arithmetic over the same recorded
// bytes, and the alternative — a receipt whose valid_for names one tree
// while its metrics derive from N — would have been M1f's "label doing the
// laundering" in a new costume.
//
// It also takes no clock: CreatedAt is left empty for the orchestrator to
// stamp, exactly as Decide leaves it, because a value that varies per call
// is a value that cannot be permutation-tested.
func Reduce(in DifferentialInputs) (DifferentialResult, error) {
	// Each member's CONTRIBUTION to the intersection, measured against the
	// base before anyone is admitted. A member that observed no case
	// comparably — every outcome `opaque`, or `error`, which the shipped
	// runner emits for any unresolvable target — contributes nothing, and
	// admitting it would collapse `diff_cases_compared` to 0 for the entire
	// race while its OWN corpus-complete gate passed: it observed every
	// declared case, it just refused to say anything comparable about any of
	// them. That is a denial of the comparison to every sibling, which is
	// what "a world can only remove itself" is supposed to forbid, so a
	// zero-contribution member is dropped and the drop is recorded.
	//
	// The residual is stated rather than hidden: a member that contributes
	// SOME cases still shrinks the denominator for everyone, and no rule
	// here refuses that. `member_cases_comparable` is what makes it visible.
	contrib := map[string]int64{}
	var members []CohortMember
	excluded := []string{}
	for _, m := range sortedMembers(in.Members) {
		n := memberComparable(m, in.Base, in.Corpus)
		contrib[m.World] = n
		if n == 0 {
			excluded = append(excluded, m.World)
			continue
		}
		members = append(members, m)
	}
	cohort := make([]string, 0, len(members))
	for _, m := range members {
		cohort = append(cohort, m.World)
	}
	cohortDig, cohortCanon, err := object.Digest(cohort)
	if err != nil {
		return DifferentialResult{}, fmt.Errorf("oracle: differential: digest cohort: %w", err)
	}

	// The denominator: cases every cohort member AND the base observed
	// comparably. Cases excluded are counted, never quietly dropped —
	// "we compared 4 of 6" is a fact a reader needs to weigh the rest.
	var compared []string
	for _, cs := range in.Corpus.Cases {
		if !comparableEverywhere(cs.ID, in.Base, members) {
			continue
		}
		compared = append(compared, cs.ID)
	}
	sort.Strings(compared)
	incomparable := int64(len(in.Corpus.Cases) - len(compared))

	// The partition: worlds with an identical fingerprint vector over the
	// compared cases are one class, named by its smallest member digest.
	classOf := map[string]string{} // world -> class id
	vectors := map[string][]string{}
	byVector := map[string][]string{}
	for _, m := range members {
		vec := make([]string, 0, len(compared))
		for _, id := range compared {
			c, _ := m.Obs.Get(id)
			vec = append(vec, c.FP)
		}
		key := vectorKey(vec)
		vectors[m.World] = vec
		byVector[key] = append(byVector[key], m.World)
	}
	baseVec := make([]string, 0, len(compared))
	for _, id := range compared {
		c, _ := in.Base.Get(id)
		baseVec = append(baseVec, c.FP)
	}
	classes := make([]reportClass, 0, len(byVector))
	for key, worlds := range byVector {
		sort.Strings(worlds)
		id := worlds[0]
		for _, w := range worlds {
			classOf[w] = id
		}
		classes = append(classes, reportClass{
			AgreesWithBase: key == vectorKey(baseVec),
			ID:             id,
			Members:        worlds,
		})
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].ID < classes[j].ID })

	// Distinguishing cases: compared cases the cohort does not agree on.
	var distinguishing []string
	for _, id := range compared {
		fps := map[string]bool{}
		for _, m := range members {
			c, _ := m.Obs.Get(id)
			fps[c.FP] = true
		}
		if len(fps) > 1 {
			distinguishing = append(distinguishing, id)
		}
	}
	first := ""
	if len(distinguishing) > 0 {
		first = distinguishing[0]
	}

	report := differentialReport{
		BaseObservation:       in.BaseObservation,
		BaseTree:              in.BaseTree,
		CasesCompared:         int64(len(compared)),
		CasesIncomparable:     incomparable,
		Classes:               classes,
		Cohort:                cohort,
		Corpus:                in.CorpusDigest,
		Distinguishing:        []reportCase{},
		Excluded:              excluded,
		MemberCasesComparable: contrib,
		Provider:              in.Corpus.Provider,
		Schema:                SchemaDifferentialReport,
	}
	if report.MemberCasesComparable == nil {
		report.MemberCasesComparable = map[string]int64{}
	}
	if report.Classes == nil {
		report.Classes = []reportClass{}
	}
	for i, id := range distinguishing {
		if i >= maxDistinguishing {
			report.DistinguishingTruncated = int64(len(distinguishing) - maxDistinguishing)
			break
		}
		cs, _ := in.Corpus.Case(id)
		baseObs, _ := in.Base.Get(id)
		rc := reportCase{
			Args:         nonNilRaw(cs.Args),
			Base:         renderObservation(baseObs, ""),
			Case:         id,
			Kwargs:       nonNilKwargs(cs.Kwargs),
			Observations: []reportObservation{},
			Target:       cs.Target,
		}
		for _, m := range members {
			c, _ := m.Obs.Get(id)
			rc.Observations = append(rc.Observations, renderObservation(c, m.World))
		}
		report.Distinguishing = append(report.Distinguishing, rc)
	}
	reportBytes, err := object.Canonical(report)
	if err != nil {
		return DifferentialResult{}, fmt.Errorf("oracle: differential: encode report: %w", err)
	}

	out := DifferentialResult{
		Report:              reportBytes,
		ReportKey:           object.CASKeyBytes(reportBytes),
		CohortDigest:        cohortDig,
		Cohort:              cohortCanon,
		Classes:             len(classes),
		Compared:            int64(len(compared)),
		FirstDistinguishing: first,
		Receipts:            make([]object.Receipt, 0, len(members)),
	}
	for _, m := range members {
		out.Receipts = append(out.Receipts, in.receiptFor(m, members, compared, classOf, vectors, baseVec, out, incomparable))
	}
	return out, nil
}

// receiptFor builds one member's comparison receipt.
func (in DifferentialInputs) receiptFor(me CohortMember, members []CohortMember, compared []string,
	classOf map[string]string, vectors map[string][]string, baseVec []string,
	res DifferentialResult, incomparable int64) object.Receipt {
	n := int64(len(members))
	metrics := map[string]int64{policy.MetricDiffCohortN: n}
	classID := classOf[me.World]
	if n >= 2 {
		// A comparison of one is not a comparison. Below two members every
		// other diff_* metric is ABSENT — not zero — and diff_cohort_n is
		// recorded precisely so a reader can see WHY the others are missing.
		classSize := int64(0)
		for _, m := range members {
			if classOf[m.World] == classID {
				classSize++
			}
		}
		mine := vectors[me.World]
		var divergent, unilateral, vsBase, unilateralVsBase int64
		for i := range compared {
			fp := mine[i]
			others := 0
			othersAgreeWithBase := true
			for _, m := range members {
				if m.World == me.World {
					continue
				}
				if vectors[m.World][i] != fp {
					others++
				}
				if vectors[m.World][i] != baseVec[i] {
					othersAgreeWithBase = false
				}
			}
			if others > 0 {
				divergent++
			}
			if others == len(members)-1 {
				unilateral++
			}
			if fp != baseVec[i] {
				vsBase++
				if othersAgreeWithBase {
					unilateralVsBase++
				}
			}
		}
		metrics[policy.MetricDiffClasses] = int64(res.Classes)
		metrics[policy.MetricDiffClassSize] = classSize
		metrics[policy.MetricDiffCasesCompared] = int64(len(compared))
		metrics[policy.MetricDiffCasesIncomparable] = incomparable
		metrics[policy.MetricDiffCasesDivergent] = divergent
		metrics[policy.MetricDiffCasesUnilateral] = unilateral
		// The last two are RECORDED AND NOT CONSUMED (M2a decision 7):
		// "agrees with base" is indistinguishable from "did nothing"
		// without a fail-to-pass reproduction test, so a key over them
		// would rank the candidate that changed nothing above the honest
		// fix. M2b's evaluation needs the numbers first.
		metrics[policy.MetricDiffCasesVsBase] = vsBase
		metrics[policy.MetricDiffCasesUnilateralVBase] = unilateralVsBase
	} else {
		classID = ""
	}

	corr := policy.KindCorrelation(policy.KindCorpusDifferential)
	corr.Corpus = in.CorpusDigest
	return object.Receipt{
		Schema: object.SchemaReceipt,
		// The reducer knows which world each receipt judges — that is the
		// whole of decision 1 — so unlike every per-world oracle it fills
		// World itself. The orchestrator still completes Freshness.ValidFor
		// and CreatedAt, which are facts about the recording, not the
		// comparison.
		World: me.World,
		Oracle: object.OracleRef{
			ID: policy.KindCorpusDifferential, Version: oracleVersion, Config: in.Spec.Config,
		},
		Execution: object.Execution{
			// No process ran: argv is empty, the exit code is not a
			// verdict source, and the observer that saw this receipt's
			// inputs is named by the receipts those inputs came from, not
			// by this one.
			Argv:           []string{},
			ExitCode:       0,
			IsolationTier:  object.TierT0Worktree,
			IsolationCaps:  object.IsolationCaps{},
			EvidenceRegime: object.RegimeDerived,
			EvidencePlugin: "",
		},
		Result: object.Result{
			Status:    StatusPass,
			Metrics:   metrics,
			Tools:     map[string]string{},
			Detail:    behaviorDetail(classID, res.CohortDigest, in.CorpusDigest, res.FirstDistinguishing),
			Artifacts: []string{res.ReportKey},
		},
		Freshness:   object.Freshness{Basis: object.BasisConstruction},
		RecheckTier: recheckTier,
		Family:      policy.FamilyBehavior,
		// No wall clock: the reducer is pure, and a duration measured around
		// a pure function is a measurement of the machine, not of the
		// purchase. What a scheduler can learn from is the SIZE of the
		// comparison, which is exactly cost.units — and `Unit` is set only
		// when that size was actually measured, because object.Cost
		// documents `Unit == "" iff Units == 0` and a cohort that compared
		// nothing has an unknown scale, not a scale of zero world-cases.
		Cost: sizedCost(0, n*int64(len(compared)), policy.UnitWorldCases),
		Inputs: map[string]string{
			object.InputBaseObservation: in.BaseObservation,
			object.InputBaseTree:        in.BaseTree,
			object.InputCohort:          res.CohortDigest,
			object.InputCorpus:          in.CorpusDigest,
			// The weakest regime among the inputs. Every configuration
			// that ships reads `streamed` observations, so a reader of a
			// differential receipt learns from the RECEIPT ALONE that the
			// comparison is control-plane arithmetic over
			// candidate-influenced numbers.
			object.InputEvidenceFloor: object.RegimeStreamed,
		},
		Correlation: corr,
	}
}

// DetailPrefix is the fixed head of a comparison receipt's one-line,
// NON-NUMERIC summary.
const DetailPrefix = "behavior class "

// behaviorDetail renders result.detail for a comparison receipt.
//
// M2a specifies `behavior class mv0:aa1… (cohort mv0:7f3…)`. Two more
// non-numeric facts are carried here, and the reason is that the
// on_behavioral_split rule's frozen sentence names both of them — the
// corpus and the first distinguishing case — while `Decide` may read
// neither `Inputs` (decision 24: a provenance field a gate could read is a
// metric with extra steps) nor CAS (it is pure). Result.Detail is the one
// channel the design already provides for exactly this: "the single string
// a pure gate predicate may quote when a count is not enough to act on".
// Extending it is therefore the smallest honest change; inventing a second
// channel, or letting Decide reach into provenance, would both be larger.
//
// Shape, total over every input, with "-" for a fact this receipt does not
// have (a cohort of one has no class and nothing to distinguish):
//
//	behavior class <class> (cohort <cohort>, corpus <corpus>, first distinguishing case <case>)
func behaviorDetail(class, cohort, corpus, firstCase string) string {
	return fmt.Sprintf("%s%s (cohort %s, corpus %s, first distinguishing case %s)",
		DetailPrefix, dash(class), dash(cohort), dash(corpus), dash(firstCase))
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// sortedMembers returns the cohort in world-digest order with duplicates
// dropped. Sorting here is what makes Reduce PERMUTATION-INVARIANT: the
// partition, the class names, the report bytes and every receipt are the
// same whatever order the orchestrator's workers finished in.
func sortedMembers(in []CohortMember) []CohortMember {
	out := make([]CohortMember, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, m := range in {
		if m.World == "" || seen[m.World] {
			continue
		}
		seen[m.World] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].World < out[j].World })
	return out
}

// memberComparable counts the corpus cases this member and the base BOTH
// observed comparably — the member's contribution to the intersection,
// computed independently of every other member so that dropping a
// zero-contribution member cannot depend on the order they are dropped in.
func memberComparable(m CohortMember, base Observation, corpus Corpus) int64 {
	n := int64(0)
	for _, cs := range corpus.Cases {
		if c, ok := base.Get(cs.ID); !ok || !c.Comparable() {
			continue
		}
		if c, ok := m.Obs.Get(cs.ID); ok && c.Comparable() {
			n++
		}
	}
	return n
}

// comparableEverywhere reports whether one case can enter the denominator:
// the base and every cohort member must have observed it comparably.
func comparableEverywhere(id string, base Observation, members []CohortMember) bool {
	if c, ok := base.Get(id); !ok || !c.Comparable() {
		return false
	}
	for _, m := range members {
		c, ok := m.Obs.Get(id)
		if !ok || !c.Comparable() {
			return false
		}
	}
	return true
}

// vectorKey joins a fingerprint vector into a map key. A fingerprint is
// hex with a fixed prefix, so no separator can appear inside one and the
// join is injective.
func vectorKey(vec []string) string {
	out := ""
	for _, fp := range vec {
		out += fp + "\x00"
	}
	return out
}

// renderObservation projects one observation into the report, dropping an
// inline value that exceeds the render cap and saying so.
func renderObservation(c CaseObservation, world string) reportObservation {
	out := reportObservation{FP: c.FP, Outcome: c.Outcome, Type: c.Type, World: world}
	switch {
	case c.Truncated || len(c.Value) > valueRenderCap:
		out.Truncated = true
	case len(c.Value) > 0:
		out.Value = c.Value
	}
	return out
}

func nonNilRaw(in []json.RawMessage) []json.RawMessage {
	if in == nil {
		return []json.RawMessage{}
	}
	return in
}

func nonNilKwargs(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return map[string]json.RawMessage{}
	}
	return in
}
