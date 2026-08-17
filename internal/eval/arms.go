package eval

// THE ARMS (§5, decisions 11 and 12).
//
// WHAT SHIPS IS "SELECTOR ARMS OVER A FIXED CANDIDATE SET". It is not PRD
// §11's arm set, the report never calls it that, and the mapping is printed
// with every table. §11's seven arms each receive a budget B and SPEND it,
// mostly on generation; ours receive a candidate set that already exists and
// allocate verification over it. The budget term that dominates §11 is absent,
// so a comparison between our arms is a comparison of ALLOCATION RULES, and a
// comparison against §11's arms 1–5 is not available at any confidence.
//
// EVERY DERIVED ARM DECLARES ITS EVIDENCE FOOTPRINT AND IS CHARGED FOR IT
// (decision 12). An arm that read the whole ledger for free would dominate
// everything for free. A4 reads no receipt: footprint ∅, cost 0. A5 reads the
// suite rung on every world and is charged the recorded wall_ms of exactly
// those receipts. A9 reads the HIDDEN suite, whose cost is real and is reported
// in its own currency (scoring milliseconds), never mixed into the oracle pool.
// An arm without a DECLARED footprint does not print a number — and "declared
// empty" is a different thing from "undeclared", which is why that is a
// separate bool rather than a length check.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/schedule"
)

// Arm ids. The numbering is §5's, kept so a table row can be traced to the
// paragraph that says what it is not.
const (
	ArmFixedBudget          = "A1-fixed-budget"
	ArmAdaptive             = "A2-adaptive"
	ArmReference            = "A3-reference"
	ArmRandomSelection      = "A4-random-selection"
	ArmRepoSuiteMax         = "A5-repo-suite-max"
	ArmDifferentialMajority = "A6-differential-majority"
	ArmAllocationBound      = "A8-allocation-bound"
	ArmLabelRetrospective   = "A9-label-retrospective"
)

// Arm kinds.
const (
	KindRaced      = "raced"       // an `mvo race` under a scheduler flag
	KindDerived    = "derived"     // pure arithmetic over a recorded ledger
	KindScorerOnly = "scorer-only" // needs labels; exists only inside cmd/mvo-eval
)

// Arm is one arm's declaration: what it is, what it reads, and — the field
// that keeps the report honest — what it is NOT.
type Arm struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// PRDArm maps to PRD §11's arm list, and NotArm says why the mapping is
	// a surrogate rather than the arm. Both are printed with every table:
	// the mapping is the caption, not a footnote.
	PRDArm string `json:"prd_arm"`
	NotArm string `json:"not_arm"`
	// FootprintDeclared distinguishes "reads nothing" from "nobody said".
	FootprintDeclared bool     `json:"footprint_declared"`
	Footprint         []string `json:"footprint"` // oracle kinds this arm reads
	// RaceFlags are the flags a raced arm is invoked with. Empty for
	// derived arms, and the runner refuses to race an arm with no flags.
	RaceFlags []string `json:"race_flags"`
}

// Arms is the full declaration table, in a fixed order.
func Arms() []Arm {
	return []Arm{
		{
			ID: ArmAdaptive, Kind: KindRaced,
			PRDArm:            "6 · Multiverso adaptive",
			NotArm:            "",
			FootprintDeclared: true,
			Footprint:         []string{"policy-ladder"},
			RaceFlags:         []string{"--schedule=adaptive"},
		},
		{
			ID: ArmFixedBudget, Kind: KindRaced,
			PRDArm:            "6 · Multiverso, fixed ladder at a matched budget",
			NotArm:            "",
			FootprintDeclared: true,
			Footprint:         []string{"policy-ladder"},
			RaceFlags:         []string{"--schedule=fixed-budget"},
		},
		{
			ID: ArmReference, Kind: KindRaced,
			PRDArm: "6 · reference (d*): the exhaustive ladder, unbudgeted",
			NotArm: "not an arm under test: it is the full-evidence decision every " +
				"budgeted arm is measured against",
			FootprintDeclared: true,
			Footprint:         []string{"policy-ladder"},
			// The reference is the BUDGETED ladder handed a budget of 0,
			// which `mvo intent new` defines as unbounded — acceptance step
			// m2b1-6a's null case, where it reaches the same decision as the
			// unbudgeted `--schedule=fixed` arm and additionally records a
			// trace. The 0 goes on the INTENT (that is where max_oracle_ms
			// lives), so it is not a race flag and does not appear here.
			RaceFlags: []string{"--schedule=fixed-budget"},
		},
		{
			ID: ArmRandomSelection, Kind: KindDerived,
			PRDArm: "2 · best-of-N random (surrogate)",
			NotArm: "arm 2's SELECTION half with arm 2's GENERATION half deleted. " +
				"Seeded and recorded (AG-3 forbids unrecorded randomness); M2b decision 13 " +
				"forbids a randomized POLICY, so this is not an mvo scheduler and gets no ADMIT path",
			FootprintDeclared: true,
			Footprint:         []string{}, // reads no receipt: cost 0, and that is declared
		},
		{
			ID: ArmRepoSuiteMax, Kind: KindDerived,
			PRDArm: "3 · best-of-N public tests (surrogate)",
			NotArm: "not arm 3: research ch. 9's \"public tests\" are AGENT-GENERATED, ours are " +
				"the repository's own suite — and the adversarial corpus already measured that " +
				"tests_passed_desc is exactly what let 10-padded_deletion beat the honest fix under M1e",
			FootprintDeclared: true,
			Footprint:         []string{policy.KindPytestSuite},
		},
		{
			ID: ArmDifferentialMajority, Kind: KindDerived,
			PRDArm: "5 · test-voting / CodeMonkeys (surrogate, weak)",
			NotArm: "not CodeMonkeys: the corpus is control-plane-authored rather than " +
				"model-authored, the vote is over behaviour classes rather than test outcomes, " +
				"vector 19 already games it, and S2's shared ancestry degenerates the partition. " +
				"An exploratory row",
			FootprintDeclared: true,
			Footprint:         []string{policy.KindCorpusDifferential},
		},
		{
			ID: ArmAllocationBound, Kind: KindDerived,
			PRDArm: "— (M2b.1 §5)",
			NotArm: "a bound on ALLOCATION, not on decision quality: if every candidate is " +
				"wrong, d* is wrong, and the bound bounds reaching a wrong answer efficiently",
			FootprintDeclared: true,
			Footprint:         []string{"recorded-ledger"},
		},
		{
			ID: ArmLabelRetrospective, Kind: KindScorerOnly,
			PRDArm: "7 · retrospective oracle",
			// The wording avoids naming the two headline metrics on purpose:
			// this string is printed on the NO-METRIC path too, and
			// acceptance step m2d-7a asserts that path prints neither of
			// their names anywhere.
			NotArm: "the regret DENOMINATOR and nothing else: it admits every candidate the " +
				"hidden oracle calls correct, so its hit rate is coverage and its " +
				"false-admission rate is zero by construction. Not a result. " +
				"Computed only inside the scorer",
			FootprintDeclared: true,
			Footprint:         []string{"hidden-suite"},
		},
	}
}

// ArmByID resolves an arm declaration.
func ArmByID(id string) (Arm, bool) {
	for _, a := range Arms() {
		if a.ID == id {
			return a, true
		}
	}
	return Arm{}, false
}

// ArmOutcome is what one arm decided on one instance, with its charged cost
// and every absence explicit.
type ArmOutcome struct {
	Arm       string `json:"arm"`
	Available bool   `json:"available"`
	// Absent names why there is no number. It is nonempty exactly when
	// Available is false.
	Absent   string `json:"absent"`
	Decision string `json:"decision"`
	Subject  string `json:"subject"` // world digest
	// Footprint and OracleCostMS are decision 12's charge. ScoringMS is
	// A9's separate currency and is never added to OracleCostMS.
	Footprint    []string `json:"footprint"`
	OracleCostMS int64    `json:"oracle_cost_ms"`
	ScoringMS    int64    `json:"scoring_ms"`
	Detail       string   `json:"detail"`
}

// DerivedInput is everything a derived arm may read.
type DerivedInput struct {
	View LedgerView
	// Salt seeds A4. It is recorded with the outcome, so the "random" arm
	// is reproducible.
	Salt     string
	Instance string
	BudgetMS int64
	// Labels maps world digest -> verdict, and is supplied ONLY by the
	// scorer. A9 refuses without it; every other arm ignores it.
	Labels map[string]string
	// ScoringMS is the measured cost of producing those labels, in A9's own
	// currency.
	ScoringMS int64
}

// RunDerived evaluates one derived arm. It is pure over its input: no
// filesystem, no clock, no process. An arm that cannot act reports
// Available=false with the reason, and never a decision it did not reach.
func RunDerived(arm Arm, in DerivedInput) ArmOutcome {
	out := ArmOutcome{Arm: arm.ID, Footprint: arm.Footprint}
	if !arm.FootprintDeclared {
		out.Absent = "footprint-undeclared: an arm that has not declared what it reads does not print a number"
		return out
	}
	if arm.Kind == KindRaced {
		out.Absent = "raced-arm: this arm is produced by `mvo race`, not derived from a ledger"
		return out
	}
	worlds := aliveWorlds(in.View)
	if len(worlds) == 0 {
		out.Absent = "no worlds in the ledger: absent source implies absent metric"
		return out
	}
	switch arm.ID {
	case ArmRandomSelection:
		idx := int(hmacIndex(in.Salt, in.Instance) % uint64(len(worlds)))
		out.Available = true
		out.Decision = race.TypeSelect
		out.Subject = worlds[idx].Digest
		out.Detail = fmt.Sprintf("HMAC(salt, %q) mod %d = %d", in.Instance, len(worlds), idx)
		return out
	case ArmRepoSuiteMax:
		return pickByMetric(out, in, worlds, policy.KindPytestSuite, policy.MetricTestsPassed)
	case ArmDifferentialMajority:
		return pickByMetric(out, in, worlds, policy.KindCorpusDifferential, policy.MetricDiffClassSize)
	case ArmAllocationBound:
		return runBound(out, in)
	case ArmLabelRetrospective:
		return runRetrospective(out, in, worlds)
	default:
		out.Absent = "unknown arm " + arm.ID
		return out
	}
}

// aliveWorlds is the candidate set the derived arms choose from, in world
// digest order so every arm sees the same order and a tie-break is stable.
func aliveWorlds(v LedgerView) []object.RecordedWorld {
	out := make([]object.RecordedWorld, 0, len(v.Worlds))
	for _, w := range v.Worlds {
		if w.World.Outcome == object.OutcomeCompleted {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		// A ledger whose worlds all failed to complete still has a
		// candidate set for the arms that do not read receipts; using it
		// rather than reporting nothing keeps A4 comparable.
		out = append(out, v.Worlds...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Digest < out[j].Digest })
	return out
}

// pickByMetric implements A5 and A6: the world with the highest value of a
// recorded metric on a declared oracle kind wins, ties break to the smallest
// world digest, and the arm is CHARGED the recorded wall_ms of exactly the
// receipts it read.
func pickByMetric(out ArmOutcome, in DerivedInput, worlds []object.RecordedWorld, kind, metric string) ArmOutcome {
	type scored struct {
		world string
		value int64
		cost  int64
	}
	var have []scored
	for _, w := range worlds {
		var best *scored
		for _, r := range in.View.Receipts {
			if r.Receipt.World != w.Digest || r.Receipt.Oracle.ID != kind {
				continue
			}
			v, ok := r.Receipt.Result.Metrics[metric]
			if !ok {
				// Absent metric is absent: a receipt that did not measure
				// the metric cannot vote, and substituting zero would let
				// an errored rung outrank a passing one.
				continue
			}
			s := scored{world: w.Digest, value: v, cost: r.Receipt.Cost.WallMS}
			if best == nil || s.value > best.value {
				b := s
				best = &b
			}
		}
		if best != nil {
			have = append(have, *best)
		}
	}
	if len(have) == 0 {
		out.Absent = fmt.Sprintf("no %s receipt carries %s in this ledger: the arm's footprint is empty here, so it has no number",
			kind, metric)
		return out
	}
	sort.Slice(have, func(i, j int) bool {
		if have[i].value != have[j].value {
			return have[i].value > have[j].value
		}
		return have[i].world < have[j].world
	})
	for _, s := range have {
		out.OracleCostMS += s.cost
	}
	out.Available = true
	out.Decision = race.TypeSelect
	out.Subject = have[0].world
	out.Detail = fmt.Sprintf("%s=%d on %d/%d worlds", metric, have[0].value, len(have), len(worlds))
	return out
}

// runBound is A8: M2b.1's retrospective allocation bound, which stays what its
// name says. It calls schedule.Bound with the product's own Decide, so it
// re-implements nothing.
func runBound(out ArmOutcome, in DerivedInput) ArmOutcome {
	rep, err := schedule.Bound(schedule.BoundInput{
		Policy:   in.View.Policy,
		Worlds:   in.View.Worlds,
		Receipts: in.View.Receipts,
		Decide:   race.Decide,
		BudgetMS: in.BudgetMS,
	})
	if err != nil {
		out.Absent = "bound refused: " + err.Error()
		return out
	}
	if !rep.Available {
		out.Absent = "bound unavailable: " + rep.Refused
		return out
	}
	out.Available = true
	out.Decision = rep.Decision
	out.Subject = rep.Subject
	out.OracleCostMS = rep.MinSpendMS
	out.Detail = fmt.Sprintf("minspend %d ms of %d ms spent; reachable at B=%d: %v",
		rep.MinSpendMS, rep.TotalMS, rep.BudgetMS, rep.Reachable)
	return out
}

// runRetrospective is A9: admit any candidate the hidden oracle labels
// `correct`. TCAR = coverage and FAR = 0 BY CONSTRUCTION — it is the regret
// denominator and nothing else, and it is the one arm that may read a label.
func runRetrospective(out ArmOutcome, in DerivedInput, worlds []object.RecordedWorld) ArmOutcome {
	if in.Labels == nil {
		out.Absent = "no labels supplied: A9 exists only inside the scorer (decision 2), " +
			"and a label-reading arm without labels must produce nothing rather than a guess"
		return out
	}
	out.ScoringMS = in.ScoringMS
	for _, w := range worlds {
		if in.Labels[w.Digest] == VerdictCorrect {
			out.Available = true
			out.Decision = race.TypeSelect
			out.Subject = w.Digest
			out.Detail = "the hidden oracle labels this candidate correct"
			return out
		}
	}
	out.Available = true
	out.Decision = race.TypeReject
	unknown := 0
	for _, w := range worlds {
		if in.Labels[w.Digest] == VerdictUnknown || in.Labels[w.Digest] == "" {
			unknown++
		}
	}
	out.Detail = fmt.Sprintf("no candidate is labelled correct (%d of %d unknown or unlabelled)",
		unknown, len(worlds))
	return out
}

// hmacIndex is A4's seeded choice: HMAC-SHA256(salt, instance) read as a
// big-endian uint64. Recorded, reproducible, and never math/rand.
func hmacIndex(salt, id string) uint64 {
	mac := hmac.New(sha256.New, []byte(salt))
	mac.Write([]byte(id))
	return binary.BigEndian.Uint64(mac.Sum(nil)[:8])
}

// ArmMapping renders decision 11's table for the report. It is printed with
// EVERY table, because the difference between "arm 3" and "a surrogate for arm
// 3 with the generation half deleted" is the difference between a claim and an
// over-claim.
func ArmMapping() []string {
	out := []string{"arm mapping (" + LabelSelectorArms + "):"}
	for _, a := range Arms() {
		line := fmt.Sprintf("  %-24s %-12s PRD §11 arm: %s", a.ID, a.Kind, a.PRDArm)
		out = append(out, line)
		if a.NotArm != "" {
			out = append(out, "      NOT: "+a.NotArm)
		}
		if a.FootprintDeclared {
			if len(a.Footprint) == 0 {
				out = append(out, "      footprint: {} (declared empty: reads no receipt, charged 0)")
			} else {
				out = append(out, fmt.Sprintf("      footprint: %v", a.Footprint))
			}
		} else {
			out = append(out, "      footprint: UNDECLARED — this arm prints no number")
		}
	}
	out = append(out,
		"  PRD §11 arms 1 (serial self-repair) and 4 (LLM judge) are ABSENT: both generate, which is spend.")
	return out
}
