package schedule

// The cost table (M2b §2.6): who is allowed to author a cost, how the fit is
// made robust, and what is printed when there is no fit.
//
//	ĉost_ms(w,o) = fixed(kind, seal) + per_unit(kind, seal) × units(w,o)
//
// keyed on (kind, evidence.plugin_autoload) — M2a amendment 27's finding
// that the seal is a 4.4× lever on fixed cost, so a single-population fit
// averages two populations that differ by four.

import (
	"fmt"
	"sort"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// MinSamples is the smallest sample a coefficient may be printed from, and
// it is M2a's number kept rather than a new one. Below it there is no
// coefficient and v0 does NOT invent one.
const MinSamples = 3

// EstimatorTheilSen names the fit in every row that carries one, so a reader
// cannot mistake it for M2a's least squares (amendment B).
const EstimatorTheilSen = "theil-sen"

// EstimatorMedianFixed names the FIXED-COST-ONLY fit: the median wall time of
// a population whose unit count never varied, with NO per-unit coefficient
// at all.
//
// It exists because the two kinds whose cost dominates every real race —
// pytest-collect and pytest-suite — scale by the repository's test count,
// which is CONSTANT within a workspace. Theil–Sen needs two samples with
// distinct units and never gets them, so a single-repo workspace priced both
// kinds `declared-rank` forever: the millisecond half of §2.6 never ran, and
// a budget that only binds priced purchases therefore never bound.
//
// Refusing to print a fixed cost we measured nine times because we could not
// measure a slope is not honesty, it is throwing away the measurement we
// have. The slope stays UNKNOWN — per_unit is 0 and the estimator name says
// why, so nobody reads that zero as "measured zero ms per test".
const EstimatorMedianFixed = "median-fixed"

// CostBasisDeclaredRank is the basis string of a row with no local
// measurement. A millisecond figure nobody measured must never appear beside
// one somebody did — M2a's own `no local measurement (n=…)` rule, extended
// from the report to the allocator.
const CostBasisDeclaredRank = "declared-rank"

// Sample is one (units, wall_ms) observation of one kind under one seal,
// taken from a recorded receipt. An ERRORED receipt is not a purchase of
// anything and a receipt with no unit at all is an UNKNOWN, so neither is a
// sample — the caller filters both, exactly as `mvo oracles` does.
type Sample struct {
	Kind   string
	Seal   string // evidence.plugin_autoload the population ran under
	Units  int64
	WallMS int64
}

// SamplesFromReceipts reduces recorded receipts to fit samples, dropping the
// two shapes that are not measurements: an errored run (its wall time is
// whatever the machinery spent before it gave up, and at units = 0 it lands
// exactly on the intercept a scheduler reads as the kind's FIXED cost) and a
// receipt with no unit (object.Cost documents `Unit == "" iff Units == 0`,
// and {0, ""} is the sentinel for UNKNOWN — fitting over unknowns is fitting
// over a guess).
func SamplesFromReceipts(recs []object.Receipt, seal string) []Sample {
	out := make([]Sample, 0, len(recs))
	for _, r := range recs {
		if r.Result.Status == "error" || r.Cost.Unit == "" {
			continue
		}
		out = append(out, Sample{Kind: r.Oracle.ID, Seal: seal, Units: r.Cost.Units, WallMS: r.Cost.WallMS})
	}
	return out
}

// Coefficient is one fitted (kind, seal) population. PerUnitMicroMS is
// thousandths of a millisecond per unit: DP-1 forbids floats in canonical
// JSON and the trace is canonical JSON, so the slope is carried as a scaled
// integer rather than rounded to zero.
type Coefficient struct {
	Kind           string
	Seal           string
	N              int
	FixedMS        int64
	PerUnitMicroMS int64
	Estimator      string
}

// Basis renders the coefficient the way a trace row records it.
func (c Coefficient) Basis() string {
	return fmt.Sprintf("fit(%s,%s) n=%d", c.Kind, sealLabel(c.Seal), c.N)
}

func sealLabel(seal string) string {
	if seal == "" {
		return "unknown"
	}
	return seal
}

// Cost is one predicted purchase price. Measured == false means NO
// MILLISECOND FIGURE EXISTS: MS is 0, Rank carries the declared ordinal
// position the allocator orders by instead, and Basis says so.
type Cost struct {
	MS       int64
	Rank     int64
	Basis    string
	Measured bool
}

// render is how a cost appears in a declined row's sentence: a millisecond
// figure when one was measured, and the declared rank otherwise. NO
// MILLISECOND FIGURE IS EVER PRINTED FOR AN UNMEASURED KIND — M2a's own `no
// local measurement (n=…)` rule, extended from the report to the allocator.
func (c Cost) render() string {
	if c.Measured {
		return fmt.Sprintf("%d ms", c.MS)
	}
	return fmt.Sprintf("declared rank %d (no local measurement)", c.Rank)
}

// Divisor is the score's denominator, never below 1 so the score is total.
func (c Cost) Divisor() int64 {
	v := c.MS
	if !c.Measured {
		v = c.Rank
	}
	if v < 1 {
		return 1
	}
	return v
}

// Table is the fitted cost model plus the declared-rank fallback. It is a
// SNAPSHOT: the workspace's ledger grows and the fit moves with it, which is
// correct behaviour and would silently make two runs incomparable, so the
// table a race allocated against is recorded in schedule.started
// (decision 13).
type Table struct {
	seal  string
	fits  map[string]Coefficient
	order []string // fitted keys, sorted — the recorded snapshot's row order
	// sampleN counts THIS SEAL's usable samples per kind, including the
	// populations that fell short of MinSamples: a reader of a
	// declared-rank row learns how close this workspace is to a real
	// measurement instead of just that it has none.
	sampleN map[string]int
}

func fitKey(kind, seal string) string { return kind + "\x00" + seal }

// NewTable fits one coefficient per (kind, seal) population by Theil–Sen,
// after clamping candidate-authored unit counts.
//
// (a) UNIT AUTHORITY — a correction to M2a. M2a decision 22 gave receipts
// cost.units so wall_ms becomes learnable, and never asked WHO AUTHORS the
// unit count. For pytest-suite it is tests_total, a streamed metric a
// candidate can author: reporting tests_total = 5 000 against wall_ms = 400
// flattens the per-unit coefficient for every future race in the workspace
// (adversarial vector 22). Units declared `candidate` are therefore clamped
// at FIT TIME to the race's own base-tree collect measurement, which the
// candidate did not author.
//
// (b) ROBUST FITTING. Least squares is dragged arbitrarily far by one
// outlier. Theil–Sen — the median of pairwise slopes — tolerates a minority
// of poisoned samples. Honest limit, stated: at the MinSamples floor a
// median of three pairwise slopes survives exactly one bad point. That is
// weak. It is also strictly better than a mean, and the clamp in (a) is the
// layer that actually removes the capability.
func NewTable(samples []Sample, seal string, b Bounds) *Table {
	t := &Table{seal: seal, fits: map[string]Coefficient{}, sampleN: map[string]int{}}
	pops := map[string][]Sample{}
	for _, s := range samples {
		if authority(s.Kind) == policy.AuthorityCandidate && b.CollectedBase > 0 && s.Units > b.CollectedBase {
			s.Units = b.CollectedBase
		}
		if s.Seal == seal {
			t.sampleN[s.Kind]++
		}
		k := fitKey(s.Kind, s.Seal)
		pops[k] = append(pops[k], s)
	}
	for k, ss := range pops {
		if len(ss) < MinSamples {
			continue
		}
		est := EstimatorTheilSen
		fixed, per, ok := theilSen(ss)
		if !ok {
			// No pair with distinct units: the SLOPE is unmeasurable here,
			// the FIXED cost is not. Fall back to the median wall time and
			// label it, rather than dropping a kind we have measured nine
			// times to `declared-rank` (§2.6, amended).
			fixed, per, ok = medianFixed(ss)
			est = EstimatorMedianFixed
		}
		if !ok {
			continue
		}
		t.fits[k] = Coefficient{
			Kind: ss[0].Kind, Seal: ss[0].Seal, N: len(ss),
			FixedMS: fixed, PerUnitMicroMS: per, Estimator: est,
		}
		t.order = append(t.order, k)
	}
	sort.Strings(t.order)
	return t
}

// Coefficients returns the fitted rows in a deterministic order: the
// snapshot schedule.started records.
func (t *Table) Coefficients() []Coefficient {
	if t == nil {
		return nil
	}
	out := make([]Coefficient, 0, len(t.order))
	for _, k := range t.order {
		out = append(out, t.fits[k])
	}
	return out
}

// SampleCounts returns this seal's usable sample count per kind — what a
// declared-rank row records as its `n`.
func (t *Table) SampleCounts() map[string]int {
	if t == nil {
		return map[string]int{}
	}
	out := make(map[string]int, len(t.sampleN))
	for k, v := range t.sampleN {
		out[k] = v
	}
	return out
}

// Predict prices one rung. A kind with a fitted coefficient for THIS race's
// seal is priced in milliseconds; every other kind falls back to the
// declared ordinal rank of its OracleProfile.Dominant, and no millisecond
// figure is printed for it.
func (t *Table) Predict(r Rung) Cost {
	if t != nil {
		if c, ok := t.fits[fitKey(r.Kind, t.seal)]; ok {
			ms := c.FixedMS + (c.PerUnitMicroMS*r.Units)/1000
			if ms < 1 {
				ms = 1
			}
			return Cost{MS: ms, Basis: c.Basis(), Measured: true}
		}
	}
	return Cost{Rank: declaredRank(r.Kind), Basis: CostBasisDeclaredRank}
}

// dominantRank is the declared ordinal cost order of OracleProfile.Dominant
// (M2b decision 7c). It is a RELATIVE cost, never a time.
var dominantRank = map[string]int64{
	"tree-walk":            1,
	"hashing":              2,
	"interpreter-start":    3,
	"case-replay":          4,
	"suite-run":            5,
	"suite-run-per-mutant": 6,
}

// rankUnknown prices a kind whose dominant cost this table does not know as
// the most expensive thing on the menu. Pricing an unknown CHEAP would let a
// rung nobody has measured crowd out one somebody has.
const rankUnknown = 7

func declaredRank(kind string) int64 {
	prof, ok := policy.KindProfile(kind)
	if !ok {
		return rankUnknown
	}
	if r, ok := dominantRank[prof.Dominant]; ok {
		return r
	}
	return rankUnknown
}

// authority is the declared author of a kind's cost.units. An unknown kind
// is treated as candidate-authored: the clamp is the layer that removes a
// capability, so it must not be skipped for a kind nobody classified.
func authority(kind string) string {
	if prof, ok := policy.KindProfile(kind); ok && prof.UnitAuthority != "" {
		return prof.UnitAuthority
	}
	return policy.AuthorityCandidate
}

// TheilSen is the exported estimator, so `mvo oracles` and the allocator fit
// the SAME line from the same samples (M2b amendment B). Two estimators over
// one cost model is two cost models: the report would print one coefficient
// and the scheduler would spend against another, and the disagreement would
// be invisible because both numbers are internally consistent. One function,
// one answer, one place to correct it.
//
// perUnitMicroMS is thousandths of a millisecond per unit — DP-1 forbids
// floats in canonical JSON and the trace is canonical JSON, so the slope is
// carried as a scaled integer rather than rounded to zero.
func TheilSen(samples []Sample) (fixedMS, perUnitMicroMS int64, ok bool) {
	return theilSen(samples)
}

// MedianFixed is the exported fixed-cost-only estimator, so `mvo oracles`
// and the allocator report the same number for a population whose unit count
// never varied — the same "one function, one answer" discipline TheilSen is
// exported for.
func MedianFixed(samples []Sample) (fixedMS, perUnitMicroMS int64, ok bool) {
	return medianFixed(samples)
}

// theilSen fits wall_ms ≈ fixed + per_unit × units as the MEDIAN of pairwise
// slopes, with the intercept the median of (y − slope·x). ok is false when
// no pair has distinct x — that is not a fit with a large error bar, it is
// no fit at all, and M2a's rule is that no fit prints no number.
func theilSen(ss []Sample) (fixedMS, perUnitMicroMS int64, ok bool) {
	var slopes []float64
	for i := 0; i < len(ss); i++ {
		for j := i + 1; j < len(ss); j++ {
			dx := float64(ss[j].Units - ss[i].Units)
			if dx == 0 {
				continue
			}
			slopes = append(slopes, float64(ss[j].WallMS-ss[i].WallMS)/dx)
		}
	}
	if len(slopes) == 0 {
		return 0, 0, false
	}
	slope := median(slopes)
	res := make([]float64, 0, len(ss))
	for _, s := range ss {
		res = append(res, float64(s.WallMS)-slope*float64(s.Units))
	}
	intercept := median(res)
	if intercept < 0 {
		// A negative fixed cost is not a cost. Reporting it would make a
		// cheap purchase look free and could make it look NEGATIVE, which
		// the score's divisor cannot mean.
		intercept = 0
	}
	if slope < 0 {
		slope = 0
	}
	return int64(intercept + 0.5), int64(slope*1000 + 0.5), true
}

// medianFixed is the fixed-cost-only estimator for a population with no unit
// variance: fixed = median(wall_ms), per_unit ABSENT (0, labelled
// EstimatorMedianFixed). It is a measurement of what this kind costs at this
// repository's size and nothing more — extrapolating it to a different size
// is exactly the number it declines to invent.
func medianFixed(ss []Sample) (fixedMS, perUnitMicroMS int64, ok bool) {
	if len(ss) == 0 {
		return 0, 0, false
	}
	ws := make([]float64, 0, len(ss))
	for _, s := range ss {
		ws = append(ws, float64(s.WallMS))
	}
	m := median(ws)
	if m < 0 {
		m = 0
	}
	return int64(m + 0.5), 0, true
}

// median is the ordinary median: the middle of an odd sample, the mean of
// the two middles of an even one. It sorts a COPY.
func median(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
