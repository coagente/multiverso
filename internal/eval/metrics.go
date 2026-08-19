package eval

// TCAR, FAR, FRR AND REGRET — DEFINED OPERATIONALLY (§4, decisions 9 and 10).
//
// PURE and TOTAL. Every function here is arithmetic over a slice of rows; none
// of them reads a file, a clock or an environment variable, and every one of
// them has an answer for the empty slice, for all-unknown labels and for a
// single candidate. The arithmetic is the easy part of this block — the
// mechanism in leak.go is the deliverable — but it is the part that gets
// quoted, so the refusals are compiled in rather than remembered.
//
// THE THREE CHOICES THAT DECIDE WHAT THE NUMBERS MEAN (decision 9):
//
//	(a) ADMISSION = SELECT. The treatment that varies across arms is the
//	    scheduler, so making TCAR depend on the admission gate's
//	    re-verification would mix EP-3 into a scheduling comparison. Where
//	    `mvo admit` actually ran, TCAR_adm/FAR_adm are reported BESIDE the
//	    headline pair with a divergence census; where it did not, those
//	    columns are ABSENT and never equal to the SELECT columns.
//
//	(b) ESCALATE is not an admission and not a success. It enters neither
//	    TCAR's numerator nor FAR's denominator, because FAR's denominator is
//	    "what the system asserted was safe to land" and an escalation asserts
//	    nothing. But escalation is not free: TCAR's denominator is ALL
//	    INSTANCES, so escalating costs exactly as much TCAR as rejecting.
//	    This is the choice that decides whether the adaptive arm "looks good",
//	    and it is made against that arm's interest on purpose.
//
//	(c) Zero admissions ⇒ FAR = absent. Not 0 %, not 0/0 rendered as zero.
//	    "0 % FAR" for an arm that never acted is the single most misleading
//	    number this harness could print. It is the project's own
//	    absent-source-implies-absent-metric rule, and it bites in the
//	    cautious arm's FAVOUR — which is exactly why it must be a rule and
//	    not a judgement made after seeing the numbers.
//
// THE TWO DENOMINATORS DIFFER ON PURPOSE: instances for TCAR, admissions for
// FAR. A reviewer who assumes that is a typo must be able to find this
// sentence.

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/race"
)

// InstanceFloorDefault is the declared instance floor: below it there is no
// p-value and no confidence interval, only the paired decision table and the
// per-instance rows. The harness refuses rather than trusting discipline.
const InstanceFloorDefault = 30

// ReplicateFloor is M2b.1 decision 9's rule, inherited verbatim: no verdict
// below three replicates.
const ReplicateFloor = 3

// Rate is a metric that can be ABSENT. Absence is a first-class state with its
// own rendering, because the entire failure mode this type prevents is a 0/0
// printed as 0 %.
type Rate struct {
	Present bool `json:"present"`
	Num     int  `json:"num"`
	Den     int  `json:"den"`
}

// NewRate builds a present rate, or an absent one when the denominator is
// zero. There is no way to construct a present rate with a zero denominator,
// which is the point.
func NewRate(num, den int) Rate {
	if den <= 0 {
		return Rate{}
	}
	return Rate{Present: true, Num: num, Den: den}
}

// Value returns the ratio and whether it exists at all.
func (r Rate) Value() (float64, bool) {
	if !r.Present || r.Den == 0 {
		return 0, false
	}
	return float64(r.Num) / float64(r.Den), true
}

// String renders "—" for an absent rate. It never renders a number for one.
func (r Rate) String() string {
	v, ok := r.Value()
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%d/%d = %.1f%%", r.Num, r.Den, v*100)
}

// Band is a rate that may only be known within an interval, which is what
// `unknown` labels do to FAR. A point estimate is printed only when the
// interval is a point.
type Band struct {
	Present bool `json:"present"`
	Low     Rate `json:"low"`
	High    Rate `json:"high"`
	// Unscored is the count of admissions whose label is `unknown`. It is
	// what widens the band, and it is reported so a wide band has a cause.
	Unscored int `json:"unscored"`
}

// Point reports whether the band has collapsed to a single value.
func (b Band) Point() bool {
	if !b.Present {
		return false
	}
	lv, lok := b.Low.Value()
	hv, hok := b.High.Value()
	return lok && hok && lv == hv
}

func (b Band) String() string {
	if !b.Present {
		return "—"
	}
	if b.Point() {
		return b.Low.String()
	}
	lv, _ := b.Low.Value()
	hv, _ := b.High.Value()
	return fmt.Sprintf("[%.1f%%, %.1f%%] (%d unscored)", lv*100, hv*100, b.Unscored)
}

// Row is one (instance, arm) observation: everything the metric arithmetic
// needs, and nothing it does not. It is built by the scorer from the ledger
// and the labels, and every field here is RECORDED rather than inferred.
type Row struct {
	Instance string `json:"instance"`
	Arm      string `json:"arm"`
	Family   string `json:"family"`
	Tier     int    `json:"tier"`
	// Policy is the policy this INSTANCE actually raced under, which is not
	// always the cell's: an instance carrying a PolicyHint keeps its hint. It is
	// a column rather than a caption because a cell that pooled two policies
	// under one policy's name is exactly the untagged aggregate decision 8
	// forbids — the harness printed a NOT UNIFORM warning and then pooled the
	// numbers anyway. Metrics are now computed per (arm, family, policy).
	Policy string `json:"policy"`
	// CostRegime is M2d.1 decision 11: `warm` or `cold`, DERIVED from the
	// recorded `schedule.started.cost_table` rather than from the flag that
	// was passed. It joins the cell key BESIDE the policy, for the policy's
	// own reason — warming changes what every arm can AFFORD, so a warm cell
	// and a cold cell are two experiments and a table that pooled them would
	// be the untagged aggregate M2d decision 8 already forbids.
	//
	// Every number M2d published is `cold`, and that normalizes EXACTLY
	// rather than by assumption: no binary before this block could warm an
	// eval workspace.
	CostRegime string `json:"cost_regime"`
	// Selector is the ALLOCATION RULE this row's arm raced under — "" for an
	// arm that computes no scarcity test (the ladder, the reference and every
	// derived arm), `voc` or `voc2` for the adaptive arm.
	//
	// It is a COLUMN rather than a per-run flag because that is blocker B1:
	// `--selector` used to be per RUN and B is derived inside a run from that
	// run's own reference races, so a `voc` run and a `voc2` run of the same
	// cell were handed DIFFERENT budgets — measured on one host, same instance,
	// same day: minspend 1553 against 1013 — under a caption reading
	// ORACLE-BUDGET-MATCHED. Both rules now race inside ONE run against ONE
	// reference draw, so they share the bound that derives B by construction,
	// and BudgetMismatches turns "by construction" into a measurement.
	Selector string `json:"selector"`
	// Cluster is the independent BUG this instance is a slice of: the fixture
	// repository. Five instances over two repositories are not five independent
	// observations, and every denominator prints its cluster count beside it.
	Cluster string `json:"cluster"`

	// Decision is the arm's MODAL decision over R replicates; Stable is
	// false when the mode did not reach ⌈2R/3⌉, in which case the row goes
	// into its own bucket and is excluded from the paired test.
	Decision   string `json:"decision"`
	Stable     bool   `json:"stable"`
	Replicates int    `json:"replicates"`
	ModalCount int    `json:"modal_count"`

	// WinnerLabel is the hidden oracle's verdict for the candidate the
	// arm's winning world carries: correct | incorrect | unknown, or "" when
	// the decision selected nothing.
	WinnerLabel string `json:"winner_label"`
	// WinnerSource is the winning candidate's source tag, resolved by looking
	// the WINNING WORLD'S TREE up in the instance's candidate set. It used to be
	// resolved by scanning the label list for the first label whose VERDICT
	// matched, which on any instance with two same-verdict candidates named the
	// alphabetically-first one — so a laundering vector that actually won was
	// reported as a derived mutant, and `+ADVERSARIAL(declared)` never fired.
	WinnerSource string `json:"winner_source"`
	// TableSources is every source tag ON THE TABLE for this instance, winner
	// or not. The caption rule needs it: a cell whose candidate sets mix S1/S2
	// with S3 must print +ADVERSARIAL(declared) whether or not an S3 candidate
	// happened to win, because the population is what the caption describes.
	TableSources []string `json:"table_sources"`
	// Avail is ∃c : L(i,c) = correct — a correct candidate was on the table.
	Avail bool `json:"avail"`
	// AvailUnknown is true when no candidate is labelled `correct` but at
	// least one is `unknown`: coverage is then a lower bound, and saying so
	// costs one bool.
	AvailUnknown bool `json:"avail_unknown"`

	// DStar and DStarWinnerLabel are the FULL-EVIDENCE decision and the
	// label of what it would select — M2b.1's A3 reference arm, computed
	// from the recorded exhaustive ledger.
	DStar            string `json:"dstar"`
	DStarWinnerLabel string `json:"dstar_winner_label"`
	// MinSpendMS is the least spend at which some prefix-respecting
	// allocation reaches d*, and BudgetMS is B. BoundAvailable is false when
	// the bound refused (an enumeration above its cap), and an unavailable
	// bound makes FRR_reachable absent rather than optimistic.
	MinSpendMS     int64 `json:"minspend_ms"`
	BudgetMS       int64 `json:"budget_ms"`
	BoundAvailable bool  `json:"bound_available"`

	// AdmitRan and AdmitResult record whether `mvo admit` was actually run
	// and what it said. Absent means absent: the TCAR_adm column does not
	// appear. They are populated from LedgerView.AdmitResult — the
	// admission.finished event the workspace's own ledger carries — so a
	// workspace where a developer ran admit produces the columns rather than
	// leaving the plumbing dead.
	AdmitRan    bool   `json:"admit_ran"`
	AdmitResult string `json:"admit_result"` // "confirmed" | "rejected" | ""

	// DECISION 12's CHARGE, carried rather than computed and thrown away.
	// OracleCostMS is what this arm was charged on the oracle axis — for a
	// derived arm the recorded wall_ms of exactly the receipts its declared
	// footprint reads, for a raced arm the oracle spend its own replicate
	// recorded. ScoringMS is A9's separate currency and is NEVER added to the
	// oracle pool. Footprint is what the arm declared it reads.
	//
	// They exist because arms.go computed all three correctly and the run loop
	// discarded them, so no arm — raced or derived — ever printed a number on
	// the cost/accuracy axis the decision exists to put them on.
	OracleCostMS int64    `json:"oracle_cost_ms"`
	ScoringMS    int64    `json:"scoring_ms"`
	Footprint    []string `json:"footprint"`

	// Expected is the generator's expectation, for the
	// expectation-violated census only.
	Expected string `json:"expected"`
}

// Admitted is decision 9(a): admission is SELECT, full stop.
func (r Row) Admitted() bool { return r.Decision == race.TypeSelect }

// Bucket names the mutually exclusive, exhaustive classification every scored
// instance falls into. The buckets exist so the regret decomposition CLOSES:
// four components that are each computed by their own predicate do not sum to
// anything in particular, and an identity that does not close is a bug in the
// harness rather than a result.
const (
	BucketHit        = "hit"               // admitted a correct candidate
	BucketGeneration = "regret-generation" // no correct candidate existed
	BucketGates      = "regret-gates"      // correct existed; full evidence would still not select it
	BucketAllocation = "regret-allocation" // d* selects correct, the arm did not admit
	BucketRanking    = "regret-ranking"    // the arm admitted an incorrect candidate while a correct one was selectable
	BucketUnscored   = "regret-unscored"   // the arm admitted; the label is `unknown`
)

// Classify assigns a row its bucket. The order of the tests IS the semantics,
// so it is written as one readable cascade rather than as a set of predicates
// a reader must intersect in their head.
func Classify(r Row) string {
	switch {
	case r.Admitted() && r.WinnerLabel == VerdictCorrect:
		return BucketHit
	case !r.Avail:
		// No correct candidate existed. Attributable to the CANDIDATE
		// SOURCE, never to the scheduler. On family A it is 0 by
		// construction, which is why family B exists.
		return BucketGeneration
	case r.Admitted() && r.WinnerLabel == VerdictUnknown:
		// Not attributable until the label is upgraded (Tier 2/3). This
		// bucket is ours: §4 leaves it implicit, and leaving it implicit is
		// what would make the four-way sum silently fail to close.
		return BucketUnscored
	case !(r.DStar == race.TypeSelect && r.DStarWinnerLabel == VerdictCorrect):
		// A correct candidate existed and the POLICY would reject it on
		// full evidence: an oracle false negative, not a scheduling loss.
		return BucketGates
	case r.Admitted():
		// The arm admitted an incorrect candidate while a correct one
		// passed every gate. The CodeMonkeys 12.4 pp analogue, and the only
		// component a better RANKING fixes.
		return BucketRanking
	default:
		// REJECT or ESCALATE where full evidence would have selected a
		// correct candidate.
		return BucketAllocation
	}
}

// Regret is the decomposition, in COUNTS over instances. Counts rather than
// rates because the identity has to close exactly, and floating-point
// components that sum to 0.9999999 make a property test into a tolerance
// argument.
type Regret struct {
	Instances int `json:"instances"`
	Hits      int `json:"hits"`

	Generation int `json:"generation"`
	Gates      int `json:"gates"`
	Allocation int `json:"allocation"`
	Ranking    int `json:"ranking"`
	Unscored   int `json:"unscored"`

	// AllocationAvoidable and AllocationUnwinnable split the allocation
	// bucket by minspend ≤ B. An arm that rejects only where minspend > B is
	// doing the correct thing under poverty, and the same formula says so.
	// Unwinnable rows are excluded from the paired comparison (M2b.1 F16).
	AllocationAvoidable  int `json:"allocation_avoidable"`
	AllocationUnwinnable int `json:"allocation_unwinnable"`
	// AllocationUnknownBound counts allocation rows whose bound could not be
	// computed: they are neither avoidable nor unwinnable, and pretending
	// otherwise would put an enumeration limit into a scheduling number.
	AllocationUnknownBound int `json:"allocation_unknown_bound"`
}

// Total is 1 − TCAR expressed as a count: the whole gap to a perfect system.
func (g Regret) Total() int { return g.Instances - g.Hits }

// Attainable is coverage − TCAR expressed as a count: the gap a better
// SCHEDULER, RANKER or ORACLE could close, with generation regret removed.
func (g Regret) Attainable() int { return g.Total() - g.Generation }

// Closes asserts both identities. §4 writes `regret(a) = coverage − TCAR(a)`
// and ALSO lists `regret_generation = 1 − coverage` as one of that sum's four
// components; only one of those two can hold, so this type computes both and
// names them differently rather than picking one silently:
//
//	Total      = generation + gates + allocation + ranking + unscored
//	Attainable = gates + allocation + ranking + unscored
//
// A property test asserts both, and an identity that does not close is a bug
// in the harness, not a result.
func (g Regret) Closes() error {
	sum := g.Generation + g.Gates + g.Allocation + g.Ranking + g.Unscored
	if sum != g.Total() {
		return fmt.Errorf("eval: regret does not close: components %d (gen %d + gates %d + alloc %d + rank %d + unscored %d) != total %d (instances %d − hits %d)",
			sum, g.Generation, g.Gates, g.Allocation, g.Ranking, g.Unscored, g.Total(), g.Instances, g.Hits)
	}
	if sum-g.Generation != g.Attainable() {
		return fmt.Errorf("eval: attainable regret does not close: %d != %d", sum-g.Generation, g.Attainable())
	}
	split := g.AllocationAvoidable + g.AllocationUnwinnable + g.AllocationUnknownBound
	if split != g.Allocation {
		return fmt.Errorf("eval: allocation split does not close: avoidable %d + unwinnable %d + unknown-bound %d != %d",
			g.AllocationAvoidable, g.AllocationUnwinnable, g.AllocationUnknownBound, g.Allocation)
	}
	return nil
}

// ArmMetrics is one arm's full row of numbers, with every absence explicit.
type ArmMetrics struct {
	Arm       string `json:"arm"`
	Instances int    `json:"instances"`
	// Admissions is FAR's denominator.
	Admissions int `json:"admissions"`
	// UnscoredAdmissions widens FAR into a band.
	UnscoredAdmissions int `json:"unscored_admissions"`
	Unstable           int `json:"unstable"`

	TCAR Rate `json:"tcar"`
	FAR  Band `json:"far"`
	ESC  Rate `json:"esc"`
	// ESCJust is the fraction of escalations where nothing was there to
	// find: an escalation with ¬avail is the system correctly declining.
	ESCJust Rate `json:"esc_just"`
	// TCARAdm and FARAdm are the admit-confirmed columns. They are ABSENT
	// unless `mvo admit` actually ran, and never equal to the SELECT columns
	// by default.
	TCARAdm Rate `json:"tcar_adm"`
	FARAdm  Band `json:"far_adm"`
	// AdmitDivergences counts SELECTs that `mvo admit` rejected — a finding
	// in its own right.
	AdmitDivergences int `json:"admit_divergences"`

	Coverage Rate `json:"coverage"`
	// CoverageLowerBound is true when some candidate is `unknown` on an
	// instance with no `correct` one: coverage is then a floor.
	CoverageLowerBound bool `json:"coverage_lower_bound"`

	FRRLabel     Rate `json:"frr_label"`
	FRRGates     Rate `json:"frr_gates"`
	FRRReachable Rate `json:"frr_reachable"`

	Regret Regret `json:"regret"`
	// Buckets is the per-bucket census, printed with the decomposition so a
	// component has an address.
	Buckets map[string]int `json:"buckets"`
	// SourceCensus is decision 8: no untagged aggregate, ever. It counts
	// WINNERS by source.
	SourceCensus map[string]int `json:"source_census"`
	// TableSourceCensus counts INSTANCES whose candidate set contained a
	// source, winner or not. It is what the adversarial caption fires on: a
	// table over instances that raced declared attacks describes a population
	// with attacks in it, whether or not one of them won.
	TableSourceCensus map[string]int `json:"table_source_census"`
	// Clusters is the number of INDEPENDENT BUGS behind Instances. Five nested
	// slices of two repositories are not five independent observations, and
	// printing the instance count alone inflates every denominator and the
	// paired n.
	Clusters int `json:"clusters"`
	// Policies is the set of policies the rows raced under. Compute is called
	// per policy, so more than one here is a harness bug and RenderArm says so.
	Policies []string `json:"policies"`
	// Selectors is the set of allocation rules the rows raced under, and
	// BudgetsMS is the set of budgets they were handed, sorted and
	// deduplicated. BudgetsMS is what the ORACLE-BUDGET-MATCHED caption
	// PRINTS: a caption that asserts a matched budget without naming it is
	// exactly how two arms came to be compared at 1553 ms and 1013 ms under
	// one caption. B is per INSTANCE, so a multi-instance cell legitimately
	// carries several; the cross-arm question is BudgetMismatches'.
	Selectors []string `json:"selectors"`
	BudgetsMS []int64  `json:"budgets_ms"`

	// The cost/accuracy axis (decision 12). OracleCostTotalMS and
	// OracleCostMedianMS are the oracle charge; ScoringTotalMS is A9's separate
	// currency and is never summed into the oracle pool. Footprint is the union
	// of what the rows' arms declared.
	OracleCostTotalMS  int64    `json:"oracle_cost_total_ms"`
	OracleCostMedianMS int64    `json:"oracle_cost_median_ms"`
	ScoringTotalMS     int64    `json:"scoring_total_ms"`
	Footprint          []string `json:"footprint"`
	// TierCensus keeps label tiers visible, so no aggregate silently mixes
	// them.
	TierCensus map[int]int `json:"tier_census"`
	// ExpectationViolated is decision 7's cross-check census: derived-wrong
	// patches the oracle calls correct, gold that fails. A large census is
	// not an error — it is information about the oracle's strength.
	ExpectationViolated int `json:"expectation_violated"`
}

// Compute is the whole metric. It consumes the rows for ONE arm (rows for
// other arms are ignored, so a caller may hand it everything) and produces one
// ArmMetrics with every absence preserved.
func Compute(arm string, rows []Row) ArmMetrics {
	m := ArmMetrics{
		Arm:               arm,
		Buckets:           map[string]int{},
		SourceCensus:      map[string]int{},
		TableSourceCensus: map[string]int{},
		TierCensus:        map[int]int{},
	}
	clusters := map[string]bool{}
	policies := map[string]bool{}
	selectors := map[string]bool{}
	budgets := map[int64]bool{}
	footprint := map[string]bool{}
	var costs []int64
	var (
		hits           int
		admissions     int
		admIncorrect   int
		admUnknown     int
		escalations    int
		escJust        int
		avail          int
		frrLabelNum    int
		frrGatesNum    int
		frrGatesDen    int
		frrReachNum    int
		frrReachDen    int
		admRan         int
		admConfirmed   int
		admConfIncorr  int
		admConfUnknown int
	)
	g := Regret{}
	for _, r := range rows {
		if r.Arm != arm {
			continue
		}
		m.Instances++
		if !r.Stable {
			m.Unstable++
		}
		if r.WinnerSource != "" {
			m.SourceCensus[r.WinnerSource]++
		}
		for _, s := range r.TableSources {
			if s != "" {
				m.TableSourceCensus[s]++
			}
		}
		if r.Cluster != "" {
			clusters[r.Cluster] = true
		}
		if r.Policy != "" {
			policies[r.Policy] = true
		}
		if r.Selector != "" {
			selectors[r.Selector] = true
		}
		budgets[r.BudgetMS] = true
		for _, f := range r.Footprint {
			footprint[f] = true
		}
		m.OracleCostTotalMS += r.OracleCostMS
		m.ScoringTotalMS += r.ScoringMS
		costs = append(costs, r.OracleCostMS)
		if r.Tier != 0 {
			m.TierCensus[r.Tier]++
		}
		if r.Avail {
			avail++
		}
		if !r.Avail && r.AvailUnknown {
			m.CoverageLowerBound = true
		}
		if expectationViolated(r) {
			m.ExpectationViolated++
		}
		bucket := Classify(r)
		m.Buckets[bucket]++
		switch bucket {
		case BucketHit:
			hits++
		case BucketGeneration:
			g.Generation++
		case BucketGates:
			g.Gates++
		case BucketRanking:
			g.Ranking++
		case BucketUnscored:
			g.Unscored++
		case BucketAllocation:
			g.Allocation++
			switch {
			case !r.BoundAvailable:
				g.AllocationUnknownBound++
			case r.BudgetMS > 0 && r.MinSpendMS > r.BudgetMS:
				g.AllocationUnwinnable++
			default:
				g.AllocationAvoidable++
			}
		}
		if r.Admitted() {
			admissions++
			switch r.WinnerLabel {
			case VerdictIncorrect:
				admIncorrect++
			case VerdictUnknown:
				admUnknown++
			}
			if r.AdmitRan {
				admRan++
				if r.AdmitResult == AdmitConfirmed {
					admConfirmed++
					switch r.WinnerLabel {
					case VerdictIncorrect:
						admConfIncorr++
					case VerdictUnknown:
						admConfUnknown++
					}
				} else {
					m.AdmitDivergences++
				}
			}
		}
		if r.Decision == race.TypeEscalate {
			escalations++
			if !r.Avail {
				escJust++
			}
		}
		// FRR_label: a correct candidate existed and the arm did not take
		// it. REJECT and ESCALATE both count — they cost the same TCAR —
		// but they are separated by the two metrics below.
		if r.Avail && (r.Decision == race.TypeReject || r.Decision == race.TypeEscalate) {
			frrLabelNum++
		}
		// FRR_gates: restricted to instances where full evidence would NOT
		// have selected a correct candidate. That is an oracle false
		// negative, not a scheduling loss.
		if r.Avail && !(r.DStar == race.TypeSelect && r.DStarWinnerLabel == VerdictCorrect) {
			frrGatesDen++
			if r.Decision == race.TypeReject || r.Decision == race.TypeEscalate {
				frrGatesNum++
			}
		}
		// FRR_reachable is the number M2b.1's BUILDLOG asked for: the arm
		// rejected, a correct candidate was there, the policy would have
		// selected it on full evidence, AND the money to reach that decision
		// was in the arm's pocket. That is plain failure with no
		// interpretation left in it.
		if r.DStar == race.TypeSelect && r.DStarWinnerLabel == VerdictCorrect &&
			r.BoundAvailable && (r.BudgetMS == 0 || r.MinSpendMS <= r.BudgetMS) {
			frrReachDen++
			if r.Decision == race.TypeReject || r.Decision == race.TypeEscalate {
				frrReachNum++
			}
		}
	}
	g.Instances = m.Instances
	g.Hits = hits
	m.Regret = g
	m.Admissions = admissions
	m.UnscoredAdmissions = admUnknown
	m.TCAR = NewRate(hits, m.Instances)
	m.FAR = band(admIncorrect, admUnknown, admissions)
	m.ESC = NewRate(escalations, m.Instances)
	m.ESCJust = NewRate(escJust, escalations)
	m.Coverage = NewRate(avail, m.Instances)
	m.FRRLabel = NewRate(frrLabelNum, avail)
	m.FRRGates = NewRate(frrGatesNum, frrGatesDen)
	m.FRRReachable = NewRate(frrReachNum, frrReachDen)
	if admRan > 0 {
		m.TCARAdm = NewRate(admConfirmed-admConfIncorr-admConfUnknown, m.Instances)
		m.FARAdm = band(admConfIncorr, admConfUnknown, admConfirmed)
	}
	m.Clusters = len(clusters)
	m.Policies = sortedKeys(policies)
	m.Selectors = sortedKeys(selectors)
	m.Footprint = sortedKeys(footprint)
	m.BudgetsMS = sortedInt64s(budgets)
	m.OracleCostMedianMS = medianMS(costs)
	return m
}

// sortedInt64s is a set of budgets rendered in a fixed order, so a caption is
// a function of the rows and never of map iteration.
func sortedInt64s(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// medianMS is the median of a set of charged costs. A median rather than a mean
// because one instance's ladder can be an order of magnitude longer than
// another's, and a mean over five is a description of the longest one.
func medianMS(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// AdmitConfirmed and AdmitRejected are the two things `mvo admit` can say
// about a SELECT the eval plane recorded.
const (
	AdmitConfirmed = "confirmed"
	AdmitRejected  = "rejected"
)

// band computes FAR as an interval: `unknown` labels are counted first as
// correct (the low end) and then as incorrect (the high end). A point estimate
// is printed only when the interval is a point, which happens exactly when no
// admission is unscored.
func band(incorrect, unknown, admissions int) Band {
	if admissions <= 0 {
		// Decision 9(c): zero admissions ⇒ no denominator ⇒ no FAR.
		return Band{}
	}
	return Band{
		Present:  true,
		Low:      NewRate(incorrect, admissions),
		High:     NewRate(incorrect+unknown, admissions),
		Unscored: unknown,
	}
}

// expectationViolated is decision 7's cross-check, and it is REPORTED and
// never asserted: a test that asserted mutants are wrong would be the
// assumed-label bug in test form.
func expectationViolated(r Row) bool {
	switch r.Expected {
	case ExpectIncorrect:
		return r.WinnerLabel == VerdictCorrect
	case ExpectCorrect:
		return r.WinnerLabel == VerdictIncorrect
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Stability: the modal decision over R replicates
// ---------------------------------------------------------------------------

// ModalThreshold is ⌈2R/3⌉.
func ModalThreshold(replicates int) int {
	if replicates <= 0 {
		return 0
	}
	return (2*replicates + 2) / 3
}

// Modal returns the modal decision, its count and whether the mode reached
// ⌈2R/3⌉. Ties break to the lexicographically smallest decision so the
// function is deterministic — an unstable row is excluded from the paired test
// anyway, so the tie-break decides only what gets printed.
func Modal(decisions []string) (string, int, bool) {
	if len(decisions) == 0 {
		return "", 0, false
	}
	counts := map[string]int{}
	for _, d := range decisions {
		counts[d]++
	}
	keys := sortedKeys(counts)
	best, bestN := keys[0], counts[keys[0]]
	for _, k := range keys[1:] {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best, bestN, bestN >= ModalThreshold(len(decisions))
}

// SelfDisagreement is an arm's own run-to-run instability: the fraction of
// replicates that differ from the mode. M2b.1's rule — no difference is called
// a win unless it exceeds both arms' own self-disagreement — needs this
// number, and it is what turned a 4-vs-5 into a tie.
func SelfDisagreement(decisions []string) Rate {
	if len(decisions) == 0 {
		return Rate{}
	}
	_, n, _ := Modal(decisions)
	return NewRate(len(decisions)-n, len(decisions))
}

// ---------------------------------------------------------------------------
// Statistics, and the refusals
// ---------------------------------------------------------------------------

// Paired is the 2×2 discordance table of the paired indicator
// `adm ∧ L(win) = correct` for two arms over the same instances.
type Paired struct {
	ArmA string `json:"arm_a"`
	ArmB string `json:"arm_b"`
	// BothHit, AOnly, BOnly, NeitherHit are McNemar's four cells.
	BothHit    int `json:"both_hit"`
	AOnly      int `json:"a_only"`
	BOnly      int `json:"b_only"`
	NeitherHit int `json:"neither_hit"`
	Instances  int `json:"instances"`
	// Clusters is how many INDEPENDENT BUGS the paired instances come from.
	// The paired n over five nested slices of two repositories is not a paired
	// n over five independent observations, and a table that printed only the
	// instance count would invite exactly that reading. The instance floor
	// already refuses a p-value here; this makes the refusal legible.
	Clusters    int            `json:"clusters"`
	Excluded    int            `json:"excluded"`
	ExcludedWhy map[string]int `json:"excluded_why"`
}

// Pair builds the paired table. Rows are matched by instance id, and a row is
// EXCLUDED — with the reason counted — when either arm's decision is unstable
// or when the instance is unwinnable by any allocator (M2b.1 F16: pooling
// those dilutes a real effect and manufactures a null).
func Pair(armA, armB string, rows []Row) Paired {
	p := Paired{ArmA: armA, ArmB: armB, ExcludedWhy: map[string]int{}}
	byInst := map[string]map[string]Row{}
	for _, r := range rows {
		if r.Arm != armA && r.Arm != armB {
			continue
		}
		if byInst[r.Instance] == nil {
			byInst[r.Instance] = map[string]Row{}
		}
		byInst[r.Instance][r.Arm] = r
	}
	clusters := map[string]bool{}
	for _, id := range sortedKeys(byInst) {
		a, aok := byInst[id][armA]
		b, bok := byInst[id][armB]
		if !aok || !bok {
			p.Excluded++
			p.ExcludedWhy["missing-arm"]++
			continue
		}
		if !a.Stable || !b.Stable {
			p.Excluded++
			p.ExcludedWhy[string(SkipUnstable)]++
			continue
		}
		if unwinnable(a) && unwinnable(b) {
			p.Excluded++
			p.ExcludedWhy["unwinnable-by-any-allocator"]++
			continue
		}
		ah := a.Admitted() && a.WinnerLabel == VerdictCorrect
		bh := b.Admitted() && b.WinnerLabel == VerdictCorrect
		p.Instances++
		if a.Cluster != "" {
			clusters[a.Cluster] = true
		}
		switch {
		case ah && bh:
			p.BothHit++
		case ah:
			p.AOnly++
		case bh:
			p.BOnly++
		default:
			p.NeitherHit++
		}
	}
	p.Clusters = len(clusters)
	return p
}

func unwinnable(r Row) bool {
	return r.BoundAvailable && r.BudgetMS > 0 && r.MinSpendMS > r.BudgetMS
}

// Inference is the statistical verdict, or the refusal that replaces it.
type Inference struct {
	Available bool   `json:"available"`
	Refused   string `json:"refused"`
	Floor     int    `json:"floor"`
	// PValue is McNemar's EXACT two-sided p-value over the discordant
	// pairs, computed in exact rational arithmetic — no normal
	// approximation, no continuity correction, because b + c here is small
	// enough that an approximation would be a choice with no upside.
	PValue     float64 `json:"p_value"`
	Discordant int     `json:"discordant"`
	// CI is a BCa bootstrap interval for the paired difference in TCAR.
	// Method records which interval was actually produced: BCa degenerates
	// when the jackknife has no spread, and a degenerate BCa reported as BCa
	// is a lie about the method.
	CILow     float64 `json:"ci_low"`
	CIHigh    float64 `json:"ci_high"`
	Method    string  `json:"method"`
	Seed      int64   `json:"seed"`
	Resamples int     `json:"resamples"`
}

// TestPaired runs McNemar's exact test and a BCa bootstrap, or refuses. Below
// the declared instance floor it prints nothing inferential and says
// `n too small for an inferential statistic` — the report still carries the
// paired 3×3 decision table and the per-instance rows, which is what a reader
// can actually act on at n = 2.
func TestPaired(p Paired, floor int, seed int64) Inference {
	if floor <= 0 {
		floor = InstanceFloorDefault
	}
	inf := Inference{Floor: floor, Seed: seed}
	if p.Instances < floor {
		inf.Refused = fmt.Sprintf("n too small for an inferential statistic: %d scored instance(s) < floor %d",
			p.Instances, floor)
		return inf
	}
	inf.Available = true
	inf.Discordant = p.AOnly + p.BOnly
	inf.PValue = mcNemarExact(p.AOnly, p.BOnly)
	low, high, method := bcaPairedDiff(p, seed)
	inf.CILow, inf.CIHigh, inf.Method = low, high, method
	inf.Resamples = bootstrapResamples
	return inf
}

// mcNemarExact is the two-sided exact binomial test on the discordant pairs:
// p = 2 · P(X ≤ min(b,c)) with X ~ Bin(b+c, ½), capped at 1. Computed with
// big.Int binomial coefficients and one final division, so the result is exact
// up to the last float64 rounding rather than accumulated.
func mcNemarExact(b, c int) float64 {
	n := b + c
	if n == 0 {
		return 1
	}
	k := b
	if c < k {
		k = c
	}
	sum := new(big.Int)
	for i := 0; i <= k; i++ {
		sum.Add(sum, binom(n, i))
	}
	total := new(big.Int).Lsh(big.NewInt(1), uint(n))
	q := new(big.Rat).SetFrac(sum, total)
	p, _ := q.Float64()
	p *= 2
	if p > 1 {
		p = 1
	}
	return p
}

func binom(n, k int) *big.Int {
	return new(big.Int).Binomial(int64(n), int64(k))
}

// bootstrapResamples is fixed and recorded. A resample count chosen per run is
// a tuning knob on a confidence interval.
const bootstrapResamples = 2000

// bcaPairedDiff computes a BCa interval for TCAR(A) − TCAR(B) over the paired
// per-instance indicators. The resampling is SEEDED by an explicit stream
// written out here rather than taken from math/rand: math/rand's sequence is
// not a compatibility promise, and a confidence interval that moves when the
// toolchain moves is not reproducible.
//
// When the jackknife has no spread the acceleration is undefined; the function
// then falls back to the percentile interval and SAYS SO in the method string.
func bcaPairedDiff(p Paired, seed int64) (low, high float64, method string) {
	// Reconstruct the per-instance paired differences from the 2×2 table.
	// The table is a sufficient statistic for this estimator, so the
	// reconstruction loses nothing.
	var d []float64
	for i := 0; i < p.AOnly; i++ {
		d = append(d, 1)
	}
	for i := 0; i < p.BOnly; i++ {
		d = append(d, -1)
	}
	for i := 0; i < p.BothHit+p.NeitherHit; i++ {
		d = append(d, 0)
	}
	n := len(d)
	if n == 0 {
		return 0, 0, "absent"
	}
	theta := mean(d)
	rng := newStream(uint64(seed) ^ 0x9e3779b97f4a7c15)
	boots := make([]float64, 0, bootstrapResamples)
	for b := 0; b < bootstrapResamples; b++ {
		s := 0.0
		for i := 0; i < n; i++ {
			s += d[int(rng.next()%uint64(n))]
		}
		boots = append(boots, s/float64(n))
	}
	sort.Float64s(boots)
	// z0: the bias correction.
	less := 0
	for _, b := range boots {
		if b < theta {
			less++
		}
	}
	frac := float64(less) / float64(len(boots))
	// Jackknife acceleration.
	jack := make([]float64, n)
	total := 0.0
	for _, v := range d {
		total += v
	}
	for i := range d {
		jack[i] = (total - d[i]) / float64(n-1+boolToInt(n == 1))
	}
	jbar := mean(jack)
	num, den := 0.0, 0.0
	for _, j := range jack {
		diff := jbar - j
		num += diff * diff * diff
		den += diff * diff
	}
	percentile := func(alpha float64) float64 {
		idx := int(alpha * float64(len(boots)))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(boots) {
			idx = len(boots) - 1
		}
		return boots[idx]
	}
	if den == 0 || frac <= 0 || frac >= 1 {
		return percentile(0.025), percentile(0.975), "percentile-bootstrap (BCa degenerate: no jackknife spread or a boundary bias correction)"
	}
	a := num / (6 * math.Pow(den, 1.5))
	z0 := probit(frac)
	adj := func(alpha float64) float64 {
		z := probit(alpha)
		return normalCDF(z0 + (z0+z)/(1-a*(z0+z)))
	}
	return percentile(adj(0.025)), percentile(adj(0.975)), "BCa-bootstrap"
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// stream is a small explicit PRNG (splitmix64). Written out so a recorded seed
// reproduces a recorded interval on any Go version.
type stream struct{ s uint64 }

func newStream(seed uint64) *stream { return &stream{s: seed} }

func (s *stream) next() uint64 {
	s.s += 0x9e3779b97f4a7c15
	z := s.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func normalCDF(z float64) float64 { return 0.5 * (1 + math.Erf(z/math.Sqrt2)) }

// probit is the inverse normal CDF (Acklam's rational approximation, relative
// error < 1.15e-9). Stdlib has no inverse erf, and a bisection over Erf would
// be slower and no more honest.
func probit(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	a := [6]float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := [5]float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01}
	c := [6]float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	dd := [4]float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
		3.754408661907416e+00}
	const plow = 0.02425
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((dd[0]*q+dd[1])*q+dd[2])*q+dd[3])*q + 1)
	case p > 1-plow:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((dd[0]*q+dd[1])*q+dd[2])*q+dd[3])*q + 1)
	default:
		q := p - 0.5
		r := q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	}
}

// ---------------------------------------------------------------------------
// Labels every number carries (decision 8 and §4's last bullet)
// ---------------------------------------------------------------------------

// Caption labels. Every number this plane prints carries its label set: what
// budget was matched, what the candidates are, what tier the labels are, and
// the source census. A number without them is a number about something else.
const (
	LabelOracleBudgetMatched = "ORACLE-BUDGET-MATCHED"
	LabelSyntheticCandidates = "SYNTHETIC-CANDIDATES"
	LabelAdversarialDeclared = "+ADVERSARIAL(declared)"
	LabelSelectorArms        = "SELECTOR-ARMS-OVER-A-FIXED-CANDIDATE-SET"
)

// budgetCaption renders ORACLE-BUDGET-MATCHED **with the budget in it**.
//
// BLOCKER B1: the caption used to be a bare assertion. Two arms of one cell were
// raced at 1553 ms and 1013 ms — same instance, same host, same day — and the
// cell printed `ORACLE-BUDGET-MATCHED` above both, because the label was a
// constant and B was never in it. A caption that names the number it is
// claiming to have matched cannot make that claim falsely: a reader comparing
// two cells sees `B=1553ms` over one and `B=1013ms` over the other.
//
// B is derived PER INSTANCE, so a cell over several instances legitimately
// carries several budgets and the caption lists them. The cross-arm question —
// did the arms of ONE instance get the same B — is BudgetMismatches', and it is
// a refusal rather than a caption.
func budgetCaption(budgets []int64) string {
	switch len(budgets) {
	case 0:
		return LabelOracleBudgetMatched
	case 1:
		if budgets[0] <= 0 {
			// `mvo intent new` reads 0 as UNBOUNDED, so a cell whose rows
			// carry 0 is the one cell that was handed infinite money.
			return LabelOracleBudgetMatched + "(B=UNBOUNDED)"
		}
		return fmt.Sprintf("%s(B=%dms)", LabelOracleBudgetMatched, budgets[0])
	}
	parts := make([]string, 0, len(budgets))
	for _, b := range budgets {
		parts = append(parts, fmt.Sprint(b))
	}
	return fmt.Sprintf("%s(B={%s}ms per instance)", LabelOracleBudgetMatched, strings.Join(parts, ","))
}

// BudgetMismatches is BLOCKER B1's refusal, as a pure function of the rows.
//
// THE TWO RULES MUST BE COMPARED AT THE SAME BUDGET. B is derived per instance
// from that instance's reference races, so within one instance every arm — both
// allocation rules, the ladder, and every derived arm charged against the same
// bound — must have been handed exactly one B. More than one means the cell's
// arms were not budget-matched, whatever its caption says, and M2d.1's own
// BUILDLOG entry is what that costs: "THIS INVALIDATES EVERY NUMBER".
//
// It is keyed on the INSTANCE alone, and deliberately not on the cell key: one
// instance has one reference draw and therefore one B, whatever family, policy
// or cost regime its rows carry, so grouping any more finely would let a
// mismatch hide between two cells of the same instance.
func BudgetMismatches(rows []Row) []string {
	type seen struct {
		budgets map[int64][]string
		order   []int64
	}
	byInst := map[string]*seen{}
	var instances []string
	for _, r := range rows {
		s := byInst[r.Instance]
		if s == nil {
			s = &seen{budgets: map[int64][]string{}}
			byInst[r.Instance] = s
			instances = append(instances, r.Instance)
		}
		if _, ok := s.budgets[r.BudgetMS]; !ok {
			s.order = append(s.order, r.BudgetMS)
		}
		arm := r.Arm
		if r.Selector != "" {
			arm += " (--selector=" + r.Selector + ")"
		}
		s.budgets[r.BudgetMS] = append(s.budgets[r.BudgetMS], arm)
	}
	sort.Strings(instances)
	var out []string
	for _, id := range instances {
		s := byInst[id]
		if len(s.order) < 2 {
			continue
		}
		sort.Slice(s.order, func(i, j int) bool { return s.order[i] < s.order[j] })
		var parts []string
		for _, b := range s.order {
			arms := append([]string(nil), s.budgets[b]...)
			sort.Strings(arms)
			parts = append(parts, fmt.Sprintf("B=%dms: %s", b, strings.Join(arms, ", ")))
		}
		out = append(out, fmt.Sprintf(
			"%s: the arms of this cell were handed %d DIFFERENT budgets — %s. "+
				"B is derived from ONE reference draw per instance; arms that did not share it "+
				"are not budget-matched and no number computed over them means anything",
			id, len(s.order), strings.Join(parts, "; ")))
	}
	return out
}

// Captions renders the label set for a metric computed over these rows. It is
// a function of the rows so a caption cannot drift from the data: the
// adversarial label appears exactly when an S3 candidate is in the census.
func Captions(m ArmMetrics) []string {
	out := []string{budgetCaption(m.BudgetsMS), LabelSyntheticCandidates, LabelSelectorArms}
	for _, s := range m.Selectors {
		out = append(out, "RULE-"+strings.ToUpper(s))
	}
	// The adversarial label fires on the POPULATION, not on the winners. A cell
	// whose candidate sets mix S1/S2 with S3 is a table about a population with
	// declared attacks in it whether or not an attack won, and firing only on
	// winners is how a full protocol run printed zero occurrences of
	// "ADVERSARIAL" while three of five instances raced laundering vectors.
	if m.SourceCensus[SourceAdversarial] > 0 || m.TableSourceCensus[SourceAdversarial] > 0 {
		out = append(out, LabelAdversarialDeclared)
	}
	tiers := make([]int, 0, len(m.TierCensus))
	for t := range m.TierCensus {
		tiers = append(tiers, t)
	}
	sort.Ints(tiers)
	for _, t := range tiers {
		out = append(out, fmt.Sprintf("TIER-%d(n=%d)", t, m.TierCensus[t]))
	}
	srcs := sortedKeys(m.SourceCensus)
	for _, s := range srcs {
		out = append(out, fmt.Sprintf("WON-BY-%s(n=%d)", s, m.SourceCensus[s]))
	}
	for _, s := range sortedKeys(m.TableSourceCensus) {
		out = append(out, fmt.Sprintf("ON-TABLE-%s(n=%d)", s, m.TableSourceCensus[s]))
	}
	if m.Clusters > 0 {
		out = append(out, fmt.Sprintf("n=%d-INSTANCE-SLICES-OVER-%d-INDEPENDENT-BUGS",
			m.Instances, m.Clusters))
	}
	for _, p := range m.Policies {
		out = append(out, "POLICY-"+p)
	}
	return out
}

// UnionCaptions is the caption set for a WHOLE REPORT, computed from the ROWS
// rather than from any one arm's metrics. The global line used to be built from
// `Metrics[0]` — the first arm on the first family — so it understated the tier
// and source mix of every other cell in the same report; and taking it from the
// per-cell metrics instead would report the report's instance count as one,
// because Compute is called per (arm, family, policy) and every such cell here
// holds a single instance.
//
// So the two counts a reader would quote — how many instances, over how many
// independent bugs — are counted over DISTINCT instances and DISTINCT clusters,
// and the source and tier censuses are counted once per instance rather than
// once per row, because a source census that multiplied by the number of arms
// would describe the arm list rather than the population.
func UnionCaptions(rows []Row) []string {
	u := ArmMetrics{
		SourceCensus:      map[string]int{},
		TableSourceCensus: map[string]int{},
		TierCensus:        map[int]int{},
	}
	if len(rows) == 0 {
		return []string{LabelOracleBudgetMatched, LabelSyntheticCandidates, LabelSelectorArms}
	}
	insts := map[string]bool{}
	clusters := map[string]bool{}
	pol := map[string]bool{}
	seenInst := map[string]bool{}
	for _, r := range rows {
		insts[r.Instance] = true
		if r.Cluster != "" {
			clusters[r.Cluster] = true
		}
		if r.Policy != "" {
			pol[r.Policy] = true
		}
		// The winner census still counts rows: "which arm's pick came from
		// where" is a per-row fact and pooling it is the point.
		if r.WinnerSource != "" {
			u.SourceCensus[r.WinnerSource]++
		}
		if seenInst[r.Instance] {
			continue
		}
		seenInst[r.Instance] = true
		for _, s := range r.TableSources {
			if s != "" {
				u.TableSourceCensus[s]++
			}
		}
		if r.Tier != 0 {
			u.TierCensus[r.Tier]++
		}
	}
	u.Instances = len(insts)
	u.Clusters = len(clusters)
	u.Policies = sortedKeys(pol)
	return Captions(u)
}
