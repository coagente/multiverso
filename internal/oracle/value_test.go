package oracle

// M2a: the mvo-value/v0 encoding and the case fingerprint.
//
// Every test here is PURE Go over recorded bytes. The cross-language check
// at the bottom starts python3 when it is present and SKIPS WITH A NAMED
// REASON when it is not — a test that silently stops running is worse than
// no test, and a test that requires a package installed is a test that
// silently stops running.

import (
	"encoding/json"
	"math"
	"os/exec"
	"strings"
	"testing"
)

// pythonNaN is the bit pattern CPython's float("nan") carries. Go's
// math.NaN() is a different quiet NaN, and mvo-value/v0 would call the two
// different observations — which is correct under an encoding whose whole
// premise is that a float is its bits, and is why the fixtures pin this
// pattern rather than "some NaN".
var pythonNaN = math.Float64frombits(0x7ff8000000000000)

// The four float encodings the design turns on. Floats are BIT PATTERNS,
// not numbers: -0.0, NaN and the two infinities are DISTINCT observations,
// and sum(xs)/n differing from math.fsum(xs)/n in the last bit is a
// behavioural difference rather than rounding noise.
func TestEncodeFloatIsABitPattern(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float64
		want string
	}{
		{"zero", 0, `{"f":"0000000000000000"}`},
		{"negative zero", math.Copysign(0, -1), `{"f":"8000000000000000"}`},
		// Python's float("nan") is the CANONICAL quiet NaN; Go's
		// math.NaN() carries a different payload, and under this encoding
		// that is a different observation. Exactness is the point, and the
		// price is stated in value.go rather than hidden by normalizing.
		{"nan", pythonNaN, `{"f":"7ff8000000000000"}`},
		{"+inf", math.Inf(1), `{"f":"7ff0000000000000"}`},
		{"-inf", math.Inf(-1), `{"f":"fff0000000000000"}`},
		{"one", 1, `{"f":"3ff0000000000000"}`},
	} {
		if got := string(EncodeFloat(tc.in)); got != tc.want {
			t.Errorf("EncodeFloat(%v) = %s, want %s", tc.in, got, tc.want)
		}
	}
	// The point of the bit pattern, stated as a test: 0.0 and -0.0 compare
	// equal as numbers and are DIFFERENT observations.
	zero, _ := Fingerprint(EncodeFloat(0))
	negZero, _ := Fingerprint(EncodeFloat(math.Copysign(0, -1)))
	if zero == negZero {
		t.Error("0.0 and -0.0 fingerprint the same; the encoding lost the sign bit")
	}
	// And the one every float comparison gets wrong: NaN != NaN as a
	// number, but two worlds that both returned NaN AGREE.
	nanA, _ := Fingerprint(EncodeFloat(pythonNaN))
	nanB, _ := Fingerprint(EncodeFloat(pythonNaN))
	if nanA != nanB {
		t.Error("two NaN observations disagree; the fingerprint is comparing numbers, not bytes")
	}
}

// A raised exception fingerprints its TYPE and nothing else. Messages embed
// paths, addresses and object ids, so hashing them would make every world
// differ for no behavioural reason — and a differential that reports 100 %
// divergence reports nothing.
func TestFingerprintRaiseIgnoresTheMessage(t *testing.T) {
	a, err := FingerprintRaise("ValueError")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := FingerprintRaise("ValueError")
	if a != b {
		t.Errorf("two ValueErrors fingerprint differently: %s vs %s", a, b)
	}
	c, _ := FingerprintRaise("ZeroDivisionError")
	if a == c {
		t.Error("ValueError and ZeroDivisionError share a fingerprint")
	}
	// A raise never collides with a value: the pre-image names its kind.
	v, _ := Fingerprint(json.RawMessage(`"ValueError"`))
	if a == v {
		t.Error("raise ValueError and the string \"ValueError\" share a fingerprint")
	}
	if !strings.HasPrefix(a, "sha256:") {
		t.Errorf("fingerprint %q is not an artifact-shaped digest", a)
	}
}

// The encoding table, checked as a VALIDATOR over every row a declared
// corpus may carry. A hand-written corpus file is data an operator wrote,
// so its arguments are checked before a race is built on them.
func TestValidEncoded(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		ok   bool
	}{
		{"null", `null`, true},
		{"bool", `true`, true},
		{"int", `-3`, true},
		{"huge int", `123456789012345678901234567890`, true},
		{"string", `"nan"`, true},
		{"float", `{"f":"7ff8000000000000"}`, true},
		{"bytes", `{"b":"aGVsbG8"}`, true},
		{"list", `[1,{"f":"0000000000000000"},"x"]`, true},
		{"tuple", `{"t":[1,2]}`, true},
		{"str-keyed dict", `{"a":1,"b":[2]}`, true},
		{"non-str-keyed dict", `{"m":[[1,"a"],[2,"b"]]}`, true},
		{"set", `{"s":[1,2,3]}`, true},
		{"opaque", `{"o":"Decimal"}`, true},
		{"nested", `{"t":[{"s":[{"f":"0000000000000000"}]},{"m":[[{"t":[1]},null]]}]}`, true},
		{"bad float width", `{"f":"7ff8"}`, false},
		{"bad float hex", `{"f":"zzzzzzzzzzzzzzzz"}`, false},
		{"bad base64", `{"b":"!!!!"}`, false},
		{"tuple not a list", `{"t":5}`, false},
		{"map pair too short", `{"m":[[1]]}`, false},
		{"not json", `{`, false},
	} {
		err := ValidEncoded(json.RawMessage(tc.in))
		if tc.ok && err != nil {
			t.Errorf("%s: ValidEncoded(%s) = %v, want ok", tc.name, tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: ValidEncoded(%s) accepted an encoding it cannot represent", tc.name, tc.in)
		}
	}
}

// The renderer is what a maintainer actually reads. "your six candidates
// disagree on clamp(nan, 0, 10): five return nan, one returns 0" is the
// sentence the block exists to produce, so nan must print as nan.
func TestRenderValue(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`null`, "None"},
		{`true`, "True"},
		{`-3`, "-3"},
		{`"hi"`, `"hi"`},
		{`{"f":"7ff8000000000000"}`, "nan"},
		{`{"f":"7ff0000000000000"}`, "inf"},
		{`{"f":"fff0000000000000"}`, "-inf"},
		{`{"f":"8000000000000000"}`, "-0.0"},
		{`{"f":"4008000000000000"}`, "3.0"},
		{`[1,2,3]`, "[1, 2, 3]"},
		{`{"t":[1,2]}`, "(1, 2)"},
		{`{"s":[1,2]}`, "{1, 2}"},
		{`{"o":"Decimal"}`, "<Decimal>"},
		{`{"m":[[1,"a"]]}`, `{1: "a"}`},
		{`{"a":1}`, `{"a": 1}`},
	} {
		if got := RenderValue(json.RawMessage(tc.in)); got != tc.want {
			t.Errorf("RenderValue(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The cross-language check: the runner's fingerprints and Go's must be the
// same bytes, because a fingerprint computed in one language and compared
// in the other is the whole comparison.
//
// It runs the EMBEDDED runner source, so it cannot drift from what ships.
func TestFingerprintMatchesTheEmbeddedRunner(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("skipping the cross-language fingerprint check with a named reason: python3 is not on PATH")
	}
	dir := t.TempDir()
	runner, _, err := MaterializeCorpusRunner(dir)
	if err != nil {
		t.Fatalf("MaterializeCorpusRunner: %v", err)
	}
	script := `
import importlib.util, json, math, sys
spec = importlib.util.spec_from_file_location("mvo_corpus", sys.argv[1])
mc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mc)
out = {}
for name, value in [
    ("nan", float("nan")), ("neg_zero", -0.0), ("zero", 0.0),
    ("inf", float("inf")), ("int", 7), ("str", "nan"),
    ("list", [1, 2, 3]), ("tuple", (1, 2)), ("none", None), ("true", True),
]:
    enc = mc.encode(value)
    out[name] = [mc._dumps(enc), mc.fingerprint_value(enc)]
out["raise"] = ["", mc.fingerprint_raise("ValueError")]
print(json.dumps(out, sort_keys=True))
`
	cmd := exec.Command(py, "-c", script, runner)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("run the embedded runner: %v", err)
	}
	var got map[string][2]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse runner output %q: %v", raw, err)
	}
	want := map[string]json.RawMessage{
		"nan":      EncodeFloat(pythonNaN),
		"neg_zero": EncodeFloat(math.Copysign(0, -1)),
		"zero":     EncodeFloat(0),
		"inf":      EncodeFloat(math.Inf(1)),
		"int":      json.RawMessage(`7`),
		"str":      json.RawMessage(`"nan"`),
		"list":     json.RawMessage(`[1,2,3]`),
		"tuple":    json.RawMessage(`{"t":[1,2]}`),
		"none":     json.RawMessage(`null`),
		"true":     json.RawMessage(`true`),
	}
	for name, enc := range want {
		pair, ok := got[name]
		if !ok {
			t.Errorf("%s: the runner produced no encoding", name)
			continue
		}
		if pair[0] != string(enc) {
			t.Errorf("%s: runner encoded %s, Go encodes %s", name, pair[0], enc)
		}
		fp, err := Fingerprint(enc)
		if err != nil {
			t.Fatal(err)
		}
		if pair[1] != fp {
			t.Errorf("%s: runner fingerprint %s, Go fingerprint %s", name, pair[1], fp)
		}
	}
	fp, _ := FingerprintRaise("ValueError")
	if got["raise"][1] != fp {
		t.Errorf("raise: runner fingerprint %s, Go fingerprint %s", got["raise"][1], fp)
	}
}
