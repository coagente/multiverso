package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/schedule"
	"github.com/coagente/multiverso/internal/workspace"
)

// cmdOracles renders the oracle MENU (M2a): every kind the registry knows,
// with the declared shape of its cost and the correlation structure of its
// evidence, plus the coefficients FITTED FROM THIS WORKSPACE's own
// `receipt.recorded` events.
//
// It is the input M2b's scheduler allocates over. The two halves are kept
// visibly apart on purpose: the profile table is DECLARED and carries no
// numbers, because cost coefficients are per repository and must be
// measured; the measurement block underneath is fitted, and a kind with
// fewer than three receipts prints "no local measurement" with the count
// rather than a two-point fit dressed up as a fact. A skipped measurement
// must never render like a measured one — the whole product is a refusal to
// let an absent thing look present.
func cmdOracles(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("oracles", stderr)
	dir := fs.String("dir", ".", "repository directory")
	jsonOut := fs.Bool("json", false, "emit the menu as JSON")
	polRef := fs.String("policy", "", "also mark the rungs this policy declares")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("oracles: %w", err)
	}
	defer ws.Close()
	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("oracles: %w", err)
	}

	declared := map[string][]string{}
	polName, polDigest := "", ""
	if *polRef != "" {
		resolved, err := resolvePolicy(ws, st, *polRef)
		if err != nil {
			return fmt.Errorf("oracles: %w", err)
		}
		polName, polDigest = resolved.Pol.Name, resolved.Digest
		for _, o := range resolved.Pol.Oracles {
			declared[o.Kind] = append(declared[o.Kind], o.Name)
		}
		for k := range declared {
			sort.Strings(declared[k])
		}
	}

	rows := menuRows(st.Receipts, kindsByConfig(st), declared)
	if *jsonOut {
		body := map[string]any{
			"schema": "multiverso.dev/oracle-menu/v0",
			"kinds":  rows,
		}
		if *polRef != "" {
			body["policy"] = map[string]string{"name": polName, "digest": polDigest}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", " ")
		if err := enc.Encode(body); err != nil {
			return fmt.Errorf("oracles: %w", err)
		}
		return nil
	}
	writeMenuHuman(stdout, rows, polRef, polName, polDigest)
	return nil
}

// menuRow is one kind's line. Measurement is nil when this workspace cannot
// support a fit; MeasurementNote then says why, in the same words the human
// table prints. The two are never both set: a reader of the JSON gets a
// coefficient or an explanation, never a coefficient AND a caveat it might
// skip.
type menuRow struct {
	Kind         string             `json:"kind"`
	Family       string             `json:"family"`
	Stage        string             `json:"stage"`
	Dominant     string             `json:"dominant"`
	Unit         string             `json:"unit"`
	Cap          string             `json:"cap"`
	Amortized    bool               `json:"amortized"`
	Discriminate string             `json:"discriminates"`
	Correlation  object.Correlation `json:"correlation"`
	Metrics      []string           `json:"metrics"`
	Declared     []string           `json:"declared_by_policy"`
	Measurement  *fit               `json:"measurement"`
	SampleN      int                `json:"measurement_n"`
	Note         string             `json:"measurement_note"`
	// SamplesByAutoload counts this kind's usable receipts per seal. The fit
	// uses ONE of these populations and says which; the map is what stops
	// the other one from disappearing.
	SamplesByAutoload map[string]int `json:"measurement_n_by_plugin_autoload"`
}

// fit is a THEIL–SEN fit of wall_ms ≈ fixed + per_unit × units over one
// kind's receipts in this workspace.
//
// M2a fitted by least squares, and M2b amendment B replaces it: one poisoned
// sample drags a least-squares line arbitrarily far, and `cost.units` is a
// candidate-authorable number for the pytest kinds, so the estimator was a
// remote-write primitive on every future race's cost model in the workspace.
// The median of pairwise slopes tolerates a minority of poisoned points. The
// honest limit is stated rather than implied: at the minSamples floor a
// median of three pairwise slopes survives exactly ONE bad point, which is
// weak — the unit clamp in the allocator is the layer that removes the
// capability, and this is the layer that makes one bad sample survivable.
// `estimator` is on the wire so a reader cannot mistake one fit for the other.
//
// BelowResolution is not a detail. `cost.wall_ms` is an integer count of
// milliseconds, so a rung that costs 30 µs records 0 for every sample and
// fits to the line y = 0 — which is a true statement about the recording
// and a false one about the cost. The flag says which of the two a reader
// is holding, and the human rendering never prints "0 ms + 0.0 ms/unit" as
// though it were a measurement of zero.
type fit struct {
	N               int     `json:"n"`
	FixedMS         float64 `json:"fixed_ms"`
	PerUnit         float64 `json:"per_unit_ms"`
	MinUnits        int64   `json:"units_min"`
	MaxUnits        int64   `json:"units_max"`
	BelowResolution bool    `json:"below_resolution"`
	// Autoload is the plugin-autoload seal the fitted population ran under
	// (M2a decision 27). A coefficient without it is an average over two
	// populations that differ by a factor of four.
	Autoload string `json:"plugin_autoload"`
	// Estimator names the line-fitter (M2b amendment B). It is recorded
	// because the number's meaning changed without its units changing, which
	// is exactly the kind of move a reader cannot detect from the value.
	Estimator string `json:"estimator"`
}

// minSamples is the smallest sample a coefficient may be printed from. Two
// points define a line exactly and tell you nothing about whether the model
// holds; three is the smallest number that can disagree with itself.
const minSamples = 3

// kindsByConfig maps an oracle CONFIG DIGEST to the kind that produced it,
// over every policy this ledger recorded. A receipt does not carry its
// kind — it carries `oracle.config`, and the config digest is a function of
// the kind among other things (policy.ConfigDigest) — so this is the exact
// attribution rather than a name-shaped guess. A receipt whose config is in
// no recorded policy stays UNATTRIBUTED and is excluded from every fit: a
// wrongly-attributed sample would corrupt a coefficient a scheduler spends
// money on.
func kindsByConfig(st *ledgerState) map[string]attribution {
	out := map[string]attribution{}
	ambiguous := map[string]bool{}
	for _, pr := range st.Policies {
		pol, err := policy.Decode(pr.Bytes)
		if err != nil {
			continue // a policy this binary cannot decode attributes nothing
		}
		for _, o := range pol.Oracles {
			if o.Config == "" || o.Kind == "" {
				continue
			}
			got := attribution{Kind: o.Kind, Autoload: pol.Evidence.PluginAutoload}
			if prev, seen := out[o.Config]; seen && prev != got {
				// One config digest declared by two policies whose SEALS
				// differ. The receipt cannot say which run it was, so it
				// attributes nothing — the same refusal this function already
				// applies to a config no recorded policy declares, for the
				// same reason: a wrongly-attributed sample corrupts a
				// coefficient a scheduler spends money on.
				ambiguous[o.Config] = true
				continue
			}
			out[o.Config] = got
		}
	}
	for cfg := range ambiguous {
		delete(out, cfg)
	}
	return out
}

// attribution is what a recorded policy tells us about a receipt whose
// oracle.config it declares: the KIND that produced it and the
// PLUGIN-AUTOLOAD SEAL it ran under.
//
// The seal is here because M2a decision 27 measured it as the largest single
// cost lever on the ladder — 0.45 s against 0.10 s for the same pytest over
// the same tree, a 4.4x difference — and concluded that "the fit must be
// keyed on (kind, plugin_autoload)". The receipt does not carry the seal:
// object.Receipt has no such field, and policy.ConfigDigest hashes only
// {args, argv, coverage, kind, reruns, timeout_ms, corpus?, mutation?}, so
// evidence.plugin_autoload never enters it. Keying on kind alone pooled two
// populations that differ fourfold and printed their average as a
// measurement.
//
// It is recoverable without moving a byte of the receipt schema: the receipt
// names its config digest, and the recorded POLICY that declares that digest
// carries the seal. That is the mechanism decision 27 should have named, and
// it is checkable from the ledger rather than asserted.
type attribution struct {
	Kind     string
	Autoload string
}

func menuRows(receipts []receiptRec, kindOf map[string]attribution, declared map[string][]string) []menuRow {
	type sample struct{ units, wall int64 }
	byPop := map[attribution][]sample{}
	for _, rr := range receipts {
		at, ok := kindOf[rr.Receipt.Oracle.Config]
		if !ok || at.Kind == "" {
			continue
		}
		// An ERRORED receipt is not a purchase of anything: its wall time is
		// whatever the machinery spent before it gave up — a mutation error
		// carries a full baseline suite run — and its unit count was dropped
		// with the rest of its metrics. Sampled at x = 0 it lands exactly on
		// the intercept a scheduler reads as the kind's FIXED cost.
		//
		// So is a receipt with no unit at all: object.Cost documents
		// `Unit == "" iff Units == 0`, and `{0, ""}` is the sentinel for
		// UNKNOWN. Fitting over unknowns is fitting over a guess.
		if rr.Receipt.Result.Status == oracle.StatusError || rr.Receipt.Cost.Unit == "" {
			continue
		}
		byPop[at] = append(byPop[at], sample{units: rr.Receipt.Cost.Units, wall: rr.Receipt.Cost.WallMS})
	}

	kinds := policy.KnownKinds()
	rows := make([]menuRow, 0, len(kinds))
	for _, kind := range kinds {
		prof, _ := policy.KindProfile(kind)
		row := menuRow{
			Kind:         kind,
			Family:       policy.KindFamily(kind),
			Stage:        prof.Stage,
			Dominant:     prof.Dominant,
			Unit:         prof.Unit,
			Cap:          prof.Cap,
			Amortized:    prof.Amortized,
			Discriminate: prof.Discriminate,
			Correlation:  prof.Corr,
			Metrics:      policy.KindMetrics(kind),
			Declared:     declared[kind],
		}
		if row.Declared == nil {
			row.Declared = []string{}
		}
		if row.Metrics == nil {
			row.Metrics = []string{}
		}

		// One population per SEAL, and the largest is the one fitted. The
		// per-seal counts are recorded beside it so a reader can see that
		// there was more than one population and which of them the
		// coefficient came from — an average of two populations that differ
		// fourfold is not a measurement of either.
		var obs []sample
		fitted := ""
		row.SamplesByAutoload = map[string]int{}
		for at, ss := range byPop {
			if at.Kind != kind {
				continue
			}
			seal := at.Autoload
			if seal == "" {
				seal = policy.AutoloadOff // the compiled sentinel resolves to the seal
			}
			row.SamplesByAutoload[seal] += len(ss)
			if len(ss) > len(obs) || (len(ss) == len(obs) && seal < fitted) {
				obs, fitted = ss, seal
			}
		}
		row.SampleN = len(obs)
		switch {
		case len(obs) < minSamples:
			row.Note = fmt.Sprintf("no local measurement (n=%d, need %d)", len(obs), minSamples)
		default:
			samples := make([]schedule.Sample, 0, len(obs))
			lo, hi := obs[0].units, obs[0].units
			allZero := true
			for _, s := range obs {
				samples = append(samples, schedule.Sample{Units: s.units, WallMS: s.wall})
				if s.wall != 0 {
					allZero = false
				}
				if s.units < lo {
					lo = s.units
				}
				if s.units > hi {
					hi = s.units
				}
			}
			// The SAME estimator the allocator spends against, called through
			// the same function (M2b amendment B). Two implementations of one
			// cost model is two cost models.
			estimator := schedule.EstimatorTheilSen
			fixed, perMicro, ok := schedule.TheilSen(samples)
			if !ok {
				// Every receipt scaled by the same unit count. There are
				// infinitely many (fixed, per-unit) pairs through that
				// column of points, so no SLOPE may be printed — but the
				// FIXED cost was measured n times, and reporting "no local
				// measurement" about a kind this workspace has timed nine
				// times is the honesty rule pointed the wrong way. The
				// median wall time is the measurement; the slope stays
				// absent, and the note says which is which.
				fixed, perMicro, ok = schedule.MedianFixed(samples)
				if !ok {
					row.Note = fmt.Sprintf("no local measurement (n=%d, units do not vary: every receipt scaled %d)",
						len(obs), lo)
					break
				}
				estimator = schedule.EstimatorMedianFixed
				row.Note = fmt.Sprintf("fixed cost only (n=%d, units do not vary: every receipt scaled %d, so no per-unit coefficient exists)",
					len(obs), lo)
			}
			row.Measurement = &fit{
				N: len(obs), FixedMS: float64(fixed), PerUnit: float64(perMicro) / 1000,
				MinUnits: lo, MaxUnits: hi, BelowResolution: allZero,
				Autoload: fitted, Estimator: estimator,
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func writeMenuHuman(w io.Writer, rows []menuRow, polRef *string, polName, polDigest string) {
	fmt.Fprintf(w, "%-22s %-9s %-7s %-15s %-14s %-10s %-13s %s\n",
		"KIND", "FAMILY", "STAGE", "SIGNAL", "GENERATOR", "DISCRIM.", "SCALES BY", "CAP")
	for _, r := range rows {
		fmt.Fprintf(w, "%-22s %-9s %-7s %-15s %-14s %-10s %-13s %s\n",
			r.Kind, dash(r.Family), dash(r.Stage), dash(r.Correlation.Signal), dash(r.Correlation.Generator),
			dash(r.Discriminate), dash(r.Unit), dash(r.Cap))
	}
	if polRef != nil && *polRef != "" {
		fmt.Fprintf(w, "\ndeclared by policy %s (%s):\n", polName, polDigest)
		any := false
		for _, r := range rows {
			if len(r.Declared) == 0 {
				continue
			}
			any = true
			fmt.Fprintf(w, "  %-22s %s\n", r.Kind, strings.Join(r.Declared, " "))
		}
		if !any {
			fmt.Fprintln(w, "  (none)")
		}
	}
	fmt.Fprintln(w, "\nmeasured in this workspace:")
	for _, r := range rows {
		if r.Measurement == nil {
			fmt.Fprintf(w, "  %-22s %s\n", r.Kind, r.Note)
			continue
		}
		if r.Measurement.BelowResolution {
			// Every sample recorded 0 ms. The rung is cheaper than the
			// resolution of the number it is recorded in, and saying
			// "fixed 0 ms + 0.0 ms/unit" would dress that up as a
			// measurement of zero.
			fmt.Fprintf(w, "  %-22s below the 1 ms resolution of cost.wall_ms     (n=%d, units %d..%d)\n",
				r.Kind, r.Measurement.N, r.Measurement.MinUnits, r.Measurement.MaxUnits)
			continue
		}
		// The estimator is named beside the number for the same reason it is
		// on the wire: M2b amendment B changed what the coefficient MEANS
		// without changing its units, and a reader cannot tell a median of
		// pairwise slopes from a least-squares line by looking at it.
		fmt.Fprintf(w, "  %-22s fixed %.0f ms  +  %s ms/%s     (n=%d, units %d..%d, plugin_autoload %s, %s)\n",
			r.Kind, r.Measurement.FixedMS, sigfig(r.Measurement.PerUnit), unitSingular(r.Unit),
			r.Measurement.N, r.Measurement.MinUnits, r.Measurement.MaxUnits,
			dash(r.Measurement.Autoload), dash(r.Measurement.Estimator))
	}
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// sigfig renders a per-unit coefficient without rounding a real cost to
// zero. `%.1f` turns 0.0155 ms/case into "0.0", which reads as free; the
// per-unit term of a cheap rung is exactly where that matters, because it
// is the term a scheduler multiplies by a large number.
func sigfig(v float64) string {
	a := v
	if a < 0 {
		a = -a
	}
	switch {
	case a >= 1:
		return fmt.Sprintf("%.1f", v)
	case a >= 0.01:
		return fmt.Sprintf("%.3f", v)
	default:
		return fmt.Sprintf("%.5f", v)
	}
}

// unitSingular renders "0.4 ms/test" rather than "0.4 ms/tests".
func unitSingular(unit string) string {
	if unit == "" {
		return "unit"
	}
	return strings.TrimSuffix(unit, "s")
}
