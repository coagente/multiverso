package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// mkReceipts builds n receipts for one oracle config with the given
// (units, wall_ms) pairs. Only the two fields the fit reads are set: the
// menu is a projection of cost, not of the whole receipt.
func mkReceipts(cfg string, pairs ...[2]int64) []receiptRec {
	out := make([]receiptRec, 0, len(pairs))
	for i, p := range pairs {
		out = append(out, receiptRec{
			Seq: int64(i + 1),
			Receipt: object.Receipt{
				Oracle: object.OracleRef{ID: "x", Config: cfg},
				Result: object.Result{Status: "pass"},
				// Unit is set because menuRows skips a receipt whose unit is
				// "" — object.Cost's `Unit == "" iff Units == 0` sentinel for
				// UNKNOWN, and a fit over unknowns is a fit over a guess.
				Cost: object.Cost{Units: p[0], WallMS: p[1], Unit: "unit"},
			},
		})
	}
	return out
}

func rowFor(t *testing.T, rows []menuRow, kind string) menuRow {
	t.Helper()
	for _, r := range rows {
		if r.Kind == kind {
			return r
		}
	}
	t.Fatalf("no menu row for kind %q", kind)
	return menuRow{}
}

// THE MENU IS TOTAL over the registry. A scheduler that meets a kind with
// no row has to guess, and the table exists to remove guessing.
func TestOracleMenuIsTotalOverTheRegistry(t *testing.T) {
	rows := menuRows(nil, nil, nil)
	if len(rows) != len(policy.KnownKinds()) {
		t.Fatalf("menu has %d rows, registry has %d kinds", len(rows), len(policy.KnownKinds()))
	}
	for _, k := range policy.KnownKinds() {
		r := rowFor(t, rows, k)
		if r.Family != policy.KindFamily(k) {
			t.Errorf("%s: family %q, want %q", k, r.Family, policy.KindFamily(k))
		}
		if r.Stage != policy.StageWorld && r.Stage != policy.StageCohort {
			t.Errorf("%s: stage %q is neither world nor cohort", k, r.Stage)
		}
		if r.Measurement != nil {
			t.Errorf("%s: an empty ledger produced a measurement %+v", k, *r.Measurement)
		}
		if !strings.HasPrefix(r.Note, "no local measurement (n=0") {
			t.Errorf("%s: empty-ledger note = %q", k, r.Note)
		}
	}
	// The one cohort-stage kind, named rather than counted: M2b's barrier
	// exists for exactly this row.
	if got := rowFor(t, rows, policy.KindCorpusDifferential).Stage; got != policy.StageCohort {
		t.Errorf("corpus-differential stage = %q, want cohort", got)
	}
}

// FEWER THAN THREE RECEIPTS IS NOT A MEASUREMENT. Two points define a line
// exactly and say nothing about whether the model holds; a two-point fit
// rendered as a coefficient is the over-claim this whole project exists to
// remove, and the JSON must hand a reader null rather than a guess.
func TestOracleMenuRefusesATwoPointFit(t *testing.T) {
	cfg := "mv0:" + strings.Repeat("1", 64)
	kinds := map[string]attribution{cfg: {Kind: policy.KindPytestSuite, Autoload: policy.AutoloadOff}}
	for n := 0; n < minSamples; n++ {
		pairs := make([][2]int64, 0, n)
		for i := 0; i < n; i++ {
			pairs = append(pairs, [2]int64{int64(10 * (i + 1)), int64(400 + 3*i)})
		}
		rows := menuRows(mkReceipts(cfg, pairs...), kinds, nil)
		r := rowFor(t, rows, policy.KindPytestSuite)
		if r.Measurement != nil {
			t.Fatalf("n=%d produced a fit %+v", n, *r.Measurement)
		}
		if r.SampleN != n {
			t.Errorf("n=%d: SampleN = %d", n, r.SampleN)
		}
		want := "no local measurement (n=" + itoa(n) + ", need 3)"
		if r.Note != want {
			t.Errorf("n=%d: note = %q, want %q", n, r.Note, want)
		}
	}
}

// A COLUMN OF POINTS IS NOT A LINE. Three receipts that all scaled by the
// same unit count admit infinitely many (fixed, per-unit) pairs, so any pair
// we printed would be invented — and inventing a per-unit coefficient is
// exactly how a scheduler learns a repository is cheap when it is not.
func TestOracleMenuRefusesAFitWithNoUnitVariance(t *testing.T) {
	cfg := "mv0:" + strings.Repeat("2", 64)
	kinds := map[string]attribution{cfg: {Kind: policy.KindPytestCollect, Autoload: policy.AutoloadOff}}
	rows := menuRows(mkReceipts(cfg, [2]int64{8, 400}, [2]int64{8, 402}, [2]int64{8, 397}), kinds, nil)
	r := rowFor(t, rows, policy.KindPytestCollect)
	if r.Measurement != nil {
		t.Fatalf("a zero-variance sample produced a fit %+v", *r.Measurement)
	}
	if r.SampleN != 3 {
		t.Errorf("SampleN = %d, want 3", r.SampleN)
	}
	if !strings.Contains(r.Note, "units do not vary") {
		t.Errorf("note = %q, want it to name the zero variance", r.Note)
	}
}

// THE FITTED CASE. Exact points on wall = 400 + 3·units must recover the
// coefficients exactly, because a fit that cannot reproduce a line it was
// handed cannot be trusted on a cloud.
func TestOracleMenuFitsWhatItCan(t *testing.T) {
	cfg := "mv0:" + strings.Repeat("3", 64)
	kinds := map[string]attribution{cfg: {Kind: policy.KindPytestSuite, Autoload: policy.AutoloadOff}}
	rows := menuRows(mkReceipts(cfg,
		[2]int64{8, 424}, [2]int64{16, 448}, [2]int64{40, 520}), kinds, nil)
	r := rowFor(t, rows, policy.KindPytestSuite)
	if r.Measurement == nil {
		t.Fatalf("three varying points produced no fit: %q", r.Note)
	}
	if got := r.Measurement.FixedMS; got < 399.99 || got > 400.01 {
		t.Errorf("fixed = %v, want 400", got)
	}
	if got := r.Measurement.PerUnit; got < 2.99 || got > 3.01 {
		t.Errorf("per-unit = %v, want 3", got)
	}
	if r.Measurement.MinUnits != 8 || r.Measurement.MaxUnits != 40 {
		t.Errorf("units range = %d..%d, want 8..40", r.Measurement.MinUnits, r.Measurement.MaxUnits)
	}
	if r.Note != "" {
		t.Errorf("a measured kind also carried a note: %q", r.Note)
	}
}

// A RECEIPT WHOSE CONFIG NO RECORDED POLICY CLAIMS IS UNATTRIBUTED, and an
// unattributed sample is dropped rather than guessed into the nearest kind.
// A wrongly attributed sample corrupts a coefficient somebody spends money
// on, which is worse than a missing one.
func TestOracleMenuDropsUnattributedReceipts(t *testing.T) {
	known := "mv0:" + strings.Repeat("4", 64)
	stray := "mv0:" + strings.Repeat("5", 64)
	kinds := map[string]attribution{known: {Kind: policy.KindPytestSuite, Autoload: policy.AutoloadOff}}
	recs := append(mkReceipts(known, [2]int64{8, 424}, [2]int64{16, 448}, [2]int64{40, 520}),
		mkReceipts(stray, [2]int64{1, 1}, [2]int64{2, 2}, [2]int64{3, 3})...)
	rows := menuRows(recs, kinds, nil)
	if got := rowFor(t, rows, policy.KindPytestSuite).SampleN; got != 3 {
		t.Errorf("pytest-suite n = %d, want 3 (the stray config must not join it)", got)
	}
	total := 0
	for _, r := range rows {
		total += r.SampleN
	}
	if total != 3 {
		t.Errorf("attributed %d samples in total, want 3", total)
	}
}

// The human and JSON renderings must agree about which kinds were measured.
// A skipped measurement that renders like a measured one in either surface
// is the failure the whole command is written to avoid.
func TestOracleMenuRenderingsAgree(t *testing.T) {
	cfg := "mv0:" + strings.Repeat("6", 64)
	kinds := map[string]attribution{cfg: {Kind: policy.KindCorpusObserve, Autoload: policy.AutoloadOff}}
	rows := menuRows(mkReceipts(cfg,
		[2]int64{4, 26}, [2]int64{8, 30}, [2]int64{16, 38}), kinds,
		map[string][]string{policy.KindCorpusObserve: {"observe"}})

	var human strings.Builder
	ref := "differential"
	writeMenuHuman(&human, rows, &ref, "differential", "mv0:abc")
	out := human.String()
	if !strings.Contains(out, "corpus-observe         fixed") {
		t.Errorf("human output does not render the fitted kind:\n%s", out)
	}
	if !strings.Contains(out, "ms/case") {
		t.Errorf("human output does not render the singular unit:\n%s", out)
	}
	if !strings.Contains(out, "declared by policy differential (mv0:abc)") {
		t.Errorf("human output does not name the policy:\n%s", out)
	}
	for _, k := range policy.KnownKinds() {
		if !strings.Contains(out, k) {
			t.Errorf("human output omits kind %s", k)
		}
	}
	// Every unmeasured kind says so, in the same words the JSON note uses.
	for _, r := range rows {
		if r.Measurement == nil && !strings.Contains(out, r.Note) {
			t.Errorf("human output omits the note for %s: %q", r.Kind, r.Note)
		}
	}

	b, err := json.Marshal(map[string]any{"schema": "multiverso.dev/oracle-menu/v0", "kinds": rows})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Kinds []struct {
			Kind        string          `json:"kind"`
			Measurement json.RawMessage `json:"measurement"`
			N           int             `json:"measurement_n"`
			Note        string          `json:"measurement_note"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, k := range decoded.Kinds {
		measured := string(k.Measurement) != "null"
		if measured != (k.Note == "") {
			t.Errorf("%s: measurement=%s and note=%q are both set or both empty", k.Kind, k.Measurement, k.Note)
		}
		if !measured && string(k.Measurement) != "null" {
			t.Errorf("%s: an unmeasured kind's JSON coefficient is %s, want null", k.Kind, k.Measurement)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A RUNG CHEAPER THAN ITS OWN UNIT OF RECORD. `cost.wall_ms` is an integer
// count of milliseconds, so the pure reducer — measured at ~1 µs per
// world-case — records 0 for every sample and fits exactly to y = 0. That
// is a true statement about the RECORDING and a false one about the cost,
// and "fixed 0 ms + 0.0 ms/world-case" is the second one wearing the
// first's clothes.
func TestOracleMenuNamesASubMillisecondRung(t *testing.T) {
	cfg := "mv0:" + strings.Repeat("7", 64)
	kinds := map[string]attribution{cfg: {Kind: policy.KindCorpusDifferential, Autoload: policy.AutoloadOff}}
	rows := menuRows(mkReceipts(cfg,
		[2]int64{8, 0}, [2]int64{800, 0}, [2]int64{2400, 0}), kinds, nil)
	r := rowFor(t, rows, policy.KindCorpusDifferential)
	if r.Measurement == nil {
		t.Fatalf("three varying points produced no fit: %q", r.Note)
	}
	if !r.Measurement.BelowResolution {
		t.Error("a fit over samples that all recorded 0 ms is not flagged below-resolution")
	}
	var human strings.Builder
	writeMenuHuman(&human, rows, nil, "", "")
	out := human.String()
	if !strings.Contains(out, "below the 1 ms resolution of cost.wall_ms") {
		t.Errorf("human output presents a below-resolution fit as a measurement of zero:\n%s", out)
	}
	if strings.Contains(out, "fixed 0 ms") {
		t.Errorf("human output printed a zero coefficient as a fact:\n%s", out)
	}
}

// A PER-UNIT TERM IS WHAT A SCHEDULER MULTIPLIES BY A LARGE NUMBER, so
// rounding a real one to "0.0" is the one place a rendering choice becomes
// a wrong purchase. corpus-observe really does cost ~0.015 ms/case.
func TestOracleMenuDoesNotRoundASmallCoefficientToZero(t *testing.T) {
	for _, tt := range []struct {
		v    float64
		want string
	}{
		{3.1, "3.1"},
		{0.0154, "0.015"},
		{0.00024, "0.00024"},
	} {
		if got := sigfig(tt.v); got != tt.want {
			t.Errorf("sigfig(%v) = %q, want %q", tt.v, got, tt.want)
		}
	}
	cfg := "mv0:" + strings.Repeat("8", 64)
	kinds := map[string]attribution{cfg: {Kind: policy.KindCorpusObserve, Autoload: policy.AutoloadOff}}
	// wall = 30 + 0.015·units, rounded to the integer millisecond the
	// receipt actually stores.
	rows := menuRows(mkReceipts(cfg,
		[2]int64{4, 30}, [2]int64{100, 32}, [2]int64{400, 36}), kinds, nil)
	var human strings.Builder
	writeMenuHuman(&human, rows, nil, "", "")
	if strings.Contains(human.String(), "0.0 ms/case") {
		t.Errorf("a real per-case cost rendered as zero:\n%s", human.String())
	}
}

// THE FIT IS KEYED ON (kind, plugin_autoload), because the seal is the
// largest single cost lever on the ladder (M2a decision 27: 0.45 s against
// 0.10 s for the same pytest over the same tree).
//
// Decision 27 justified shipping nothing by saying `mvo oracles` "fits from
// receipts, which already carry the instance whose config digest names the
// seal". No receipt carries the seal — object.Receipt has no such field and
// policy.ConfigDigest does not hash evidence.plugin_autoload — so the fit
// pooled two populations that differ fourfold and printed their average.
// The seal IS recoverable: the receipt names its config digest and the
// recorded policy that declares it carries the seal.
func TestMenuFitIsKeyedOnTheAutoloadSeal(t *testing.T) {
	const sealed, open = "mv0:sealed", "mv0:open"
	// Four sealed samples at 100 ms fixed, three unsealed at 450 ms. Pooled,
	// the intercept lands between them and describes neither run.
	rs := append(
		mkReceipts(sealed, [2]int64{8, 100}, [2]int64{16, 100}, [2]int64{24, 100}, [2]int64{32, 100}),
		mkReceipts(open, [2]int64{8, 450}, [2]int64{16, 450}, [2]int64{24, 450})...)
	kinds := map[string]attribution{
		sealed: {Kind: policy.KindPytestCollect, Autoload: policy.AutoloadOff},
		open:   {Kind: policy.KindPytestCollect, Autoload: policy.AutoloadOn},
	}
	r := rowFor(t, menuRows(rs, kinds, nil), policy.KindPytestCollect)
	if r.Measurement == nil {
		t.Fatalf("no fit over seven samples: %s", r.Note)
	}
	if r.Measurement.Autoload != policy.AutoloadOff {
		t.Errorf("fitted population = %q, want the larger one (%q)", r.Measurement.Autoload, policy.AutoloadOff)
	}
	if r.Measurement.N != 4 {
		t.Errorf("n = %d, want 4: the two seals are separate populations, not one sample of seven", r.Measurement.N)
	}
	if got := r.Measurement.FixedMS; got < 99.9 || got > 100.1 {
		t.Errorf("fixed = %.2f ms, want ~100 — a pooled fit would land between the two populations", got)
	}
	// The population that was NOT fitted does not vanish: a reader can see
	// there were two and which one the coefficient came from.
	if r.SamplesByAutoload[policy.AutoloadOn] != 3 || r.SamplesByAutoload[policy.AutoloadOff] != 4 {
		t.Errorf("samples by seal = %v, want off:4 on:3", r.SamplesByAutoload)
	}
}

// A config digest declared by two policies with DIFFERENT seals attributes
// nothing — the same refusal already applied to a config no recorded policy
// declares, and for the same reason.
func TestAmbiguousAutoloadAttributionIsDropped(t *testing.T) {
	const cfg = "mv0:shared"
	kinds := map[string]attribution{} // what kindsByConfig returns after the delete
	r := rowFor(t, menuRows(mkReceipts(cfg, [2]int64{8, 100}, [2]int64{16, 200}, [2]int64{24, 300}),
		kinds, nil), policy.KindPytestSuite)
	if r.Measurement != nil {
		t.Fatalf("an unattributable config produced a fit %+v", *r.Measurement)
	}
	if r.SampleN != 0 {
		t.Errorf("n = %d, want 0", r.SampleN)
	}
}

// AN ERRORED RECEIPT IS NOT A PURCHASE, and neither is one with no unit.
//
// A mutation receipt that errored on a red baseline carries a full baseline
// suite run in cost.wall_ms and no mutants_tested at all, so it enters the
// fit at x = 0 — exactly the intercept M2b reads as the kind's fixed cost.
// `{0, ""}` is object.Cost's sentinel for UNKNOWN (decision 22), and fitting
// over unknowns is fitting over a guess.
func TestMenuExcludesErroredAndUnitlessReceipts(t *testing.T) {
	const cfg = "mv0:mut"
	rs := mkReceipts(cfg, [2]int64{4, 400}, [2]int64{8, 800}, [2]int64{12, 1200})
	// One errored receipt whose wall time is a whole baseline suite run.
	bad := mkReceipts(cfg, [2]int64{0, 900})[0]
	bad.Receipt.Result.Status = "error"
	bad.Receipt.Cost.Unit = "mutants"
	// One machinery receipt with the honest unknown sentinel.
	unknown := mkReceipts(cfg, [2]int64{0, 900})[0]
	unknown.Receipt.Cost.Unit = ""
	rs = append(rs, bad, unknown)

	kinds := map[string]attribution{cfg: {Kind: policy.KindMutationDiff, Autoload: policy.AutoloadOff}}
	r := rowFor(t, menuRows(rs, kinds, nil), policy.KindMutationDiff)
	if r.Measurement == nil {
		t.Fatalf("no fit: %s", r.Note)
	}
	if r.Measurement.N != 3 {
		t.Errorf("n = %d, want 3: neither the errored nor the unit-less receipt is a sample", r.Measurement.N)
	}
	if got := r.Measurement.FixedMS; got < -0.1 || got > 0.1 {
		t.Errorf("fixed = %.2f ms, want ~0 — a 900 ms sample at x=0 would land straight on the intercept", got)
	}
}
