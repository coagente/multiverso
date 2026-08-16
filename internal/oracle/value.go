package oracle

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
)

// mvo-value/v0 — the TOTAL, canonical encoding of an observed Python value,
// and the case fingerprint the differential compares (M2a decision 6).
//
// Three rules carry the honesty, and each of them is a decision that could
// have gone the easy way:
//
//   - FLOATS ARE BIT PATTERNS, NOT NUMBERS. `struct.pack('>d', x)` in hex.
//     DP-1 forbids floats in canonical JSON and a decimal rendering is not
//     portable, but exactness is the whole point: sum(xs)/n and
//     math.fsum(xs)/n differing in the last bit IS a behavioural
//     difference, and -0.0, NaN and the two infinities are distinct
//     observations rather than rounding noise.
//   - EXCEPTION MESSAGES ARE EXCLUDED; only the exception TYPE is hashed.
//     Messages embed paths, addresses and object ids, so hashing them would
//     make every world differ for no behavioural reason — and a
//     differential that reports 100 % divergence reports nothing.
//   - A VALUE THE ENCODING CANNOT REPRESENT IS `opaque`, AND TWO OPAQUE
//     OBSERVATIONS ARE NOT EQUAL. Absence of comparability is not
//     agreement. Opaque cases are excluded from the comparison denominator
//     and counted, exactly as an absent metric is recorded rather than
//     zeroed.
//
// The price of the first rule, stated rather than hidden: NaN has 2^52
// payloads and this encoding calls them different observations. CPython's
// float("nan") and every NaN IEEE-754 arithmetic produces on the platforms
// this runs on are the canonical 0x7ff8000000000000, so the case does not
// arise in practice — but a repository that manufactures a NaN payload with
// struct.unpack would see a divergence that is real at the bit level and
// probably not what its maintainer means by "behaviour". Normalizing NaN
// would remove that at the cost of the property the encoding exists for,
// and the honest trade is to say so here.
//
// | Python value                | encoding                                  |
// |-----------------------------|-------------------------------------------|
// | None, bool, int, str        | the JSON value (ints of any magnitude)    |
// | float                       | {"f":"<16 lowercase hex of >d bytes>"}    |
// | bytes                       | {"b":"<base64, no padding>"}              |
// | list                        | JSON array of encoded elements            |
// | tuple                       | {"t":[…]}                                 |
// | dict with all-str keys      | JSON object, keys sorted                  |
// | dict otherwise              | {"m":[[k,v],…]}, sorted by encoded key    |
// | set / frozenset             | {"s":[…]}, sorted by encoded member       |
// | anything else               | {"o":"<type(x).__qualname__>"} ⇒ opaque   |
const ValueSchema = "mvo-value/v0"

// Observation outcomes on the wire.
const (
	OutcomeValue   = "value"
	OutcomeRaise   = "raise"
	OutcomeOpaque  = "opaque"
	OutcomeError   = "error"
	OutcomeTimeout = "timeout"
)

// Encoded tag keys. A one-key object bearing one of these is the tagged
// form of a value the JSON grammar has no room for.
//
// The grammar is deliberately AMBIGUOUS in one place and it is worth
// naming: a Python dict {"f": "…"} encodes to the same bytes as a float.
// That costs nothing where it matters — the fingerprint is taken over the
// encoded bytes, so two observations that encode identically ARE identical
// observations — and it costs only a cosmetic mis-render in the human
// report, which is why the renderer, not the comparator, is where the
// heuristic lives.
const (
	tagFloat  = "f"
	tagBytes  = "b"
	tagTuple  = "t"
	tagMap    = "m"
	tagSet    = "s"
	tagOpaque = "o"
)

// Fingerprint is the case fingerprint of one observed VALUE: sha256 over
// the canonical JSON of {"kind":"value","v":<encoded>}, rendered
// "sha256:<hex>". The pre-image is canonical (DP-1), so Go and the Python
// runner agree byte-for-byte — which is what lets a pure Go test pin a
// fingerprint the runner will later produce.
func Fingerprint(encoded json.RawMessage) (string, error) {
	return fingerprintOf(map[string]any{"kind": OutcomeValue, "v": encoded})
}

// FingerprintRaise is the fingerprint of a raised exception: the TYPE only.
func FingerprintRaise(qualname string) (string, error) {
	return fingerprintOf(map[string]any{"kind": OutcomeRaise, "t": qualname})
}

func fingerprintOf(v any) (string, error) {
	canon, err := object.Canonical(v)
	if err != nil {
		return "", fmt.Errorf("oracle: fingerprint: %w", err)
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// EncodeFloat renders a float64 the way the runner does: the 16 lowercase
// hex digits of its IEEE-754 big-endian bytes. It exists so a Go test can
// state "this is what NaN encodes to" without starting an interpreter.
func EncodeFloat(f float64) json.RawMessage {
	bits := math.Float64bits(f)
	var b [8]byte
	for i := 0; i < 8; i++ {
		b[7-i] = byte(bits >> (8 * i))
	}
	return json.RawMessage(`{"f":"` + hex.EncodeToString(b[:]) + `"}`)
}

// ValidEncoded reports whether raw is a well-formed mvo-value/v0 encoding,
// naming the first construct that is not. A declared corpus file is data an
// operator wrote by hand, so its arguments are checked before a race is
// built on them rather than after a runner fails to decode them.
func ValidEncoded(raw json.RawMessage) error {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}
	return validEncodedValue(v)
}

func validEncodedValue(v any) error {
	switch x := v.(type) {
	case nil, bool, string, json.Number:
		return nil
	case float64:
		// Unreachable through ValidEncoded (UseNumber), and refused rather
		// than accepted if a caller ever hands us one: DP-1 forbids a float
		// in canonical JSON, and a decimal rendering of a bit pattern is
		// exactly the lossy step this encoding exists to avoid.
		return fmt.Errorf("bare JSON float %v (floats encode as {\"f\":\"<hex>\"})", x)
	case []any:
		for i, e := range x {
			if err := validEncodedValue(e); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
		return nil
	case map[string]any:
		return validEncodedObject(x)
	default:
		return fmt.Errorf("unsupported JSON type %T", v)
	}
}

func validEncodedObject(m map[string]any) error {
	if len(m) == 1 {
		for tag, v := range m {
			switch tag {
			case tagFloat:
				s, ok := v.(string)
				if !ok || len(s) != 16 {
					return fmt.Errorf(`{"f":…} wants 16 hex digits`)
				}
				if _, err := hex.DecodeString(s); err != nil {
					return fmt.Errorf(`{"f":…}: %w`, err)
				}
				return nil
			case tagBytes:
				s, ok := v.(string)
				if !ok {
					return fmt.Errorf(`{"b":…} wants a base64 string`)
				}
				if _, err := base64.RawStdEncoding.DecodeString(s); err != nil {
					return fmt.Errorf(`{"b":…}: %w`, err)
				}
				return nil
			case tagTuple, tagSet:
				list, ok := v.([]any)
				if !ok {
					return fmt.Errorf(`{%q:…} wants an array`, tag)
				}
				for i, e := range list {
					if err := validEncodedValue(e); err != nil {
						return fmt.Errorf("%s[%d]: %w", tag, i, err)
					}
				}
				return nil
			case tagMap:
				list, ok := v.([]any)
				if !ok {
					return fmt.Errorf(`{"m":…} wants an array of pairs`)
				}
				for i, e := range list {
					pair, ok := e.([]any)
					if !ok || len(pair) != 2 {
						return fmt.Errorf("m[%d]: wants a [key,value] pair", i)
					}
					for _, half := range pair {
						if err := validEncodedValue(half); err != nil {
							return fmt.Errorf("m[%d]: %w", i, err)
						}
					}
				}
				return nil
			case tagOpaque:
				if _, ok := v.(string); !ok {
					return fmt.Errorf(`{"o":…} wants a type name`)
				}
				return nil
			}
		}
	}
	// A plain object: a Python dict with all-string keys.
	for k, v := range m {
		if err := validEncodedValue(v); err != nil {
			return fmt.Errorf("%q: %w", k, err)
		}
	}
	return nil
}

// RenderValue renders an encoded value the way `mvo explain` prints it:
// short, Python-shaped, and never longer than the reader needs. It is
// display only — nothing downstream parses it back — so a value the
// renderer cannot shorten honestly is printed as its encoded form rather
// than as a guess.
func RenderValue(raw json.RawMessage) string {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	return renderEncoded(v)
}

func renderEncoded(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case json.Number:
		return x.String()
	case string:
		return quote(x)
	case []any:
		return "[" + renderList(x) + "]"
	case map[string]any:
		return renderEncodedObject(x)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func renderEncodedObject(m map[string]any) string {
	if len(m) == 1 {
		for tag, v := range m {
			switch tag {
			case tagFloat:
				if s, ok := v.(string); ok {
					return renderFloatHex(s)
				}
			case tagBytes:
				if s, ok := v.(string); ok {
					return "b64:" + s
				}
			case tagTuple:
				if list, ok := v.([]any); ok {
					return "(" + renderList(list) + ")"
				}
			case tagSet:
				if list, ok := v.([]any); ok {
					return "{" + renderList(list) + "}"
				}
			case tagMap:
				if list, ok := v.([]any); ok {
					parts := make([]string, 0, len(list))
					for _, e := range list {
						pair, ok := e.([]any)
						if !ok || len(pair) != 2 {
							continue
						}
						parts = append(parts, renderEncoded(pair[0])+": "+renderEncoded(pair[1]))
					}
					return "{" + strings.Join(parts, ", ") + "}"
				}
			case tagOpaque:
				if s, ok := v.(string); ok {
					return "<" + s + ">"
				}
			}
		}
	}
	keys := sortedStringKeys(m)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, quote(k)+": "+renderEncoded(m[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func renderList(list []any) string {
	parts := make([]string, 0, len(list))
	for _, e := range list {
		parts = append(parts, renderEncoded(e))
	}
	return strings.Join(parts, ", ")
}

// renderFloatHex turns the 16-hex bit pattern back into the shortest text
// that names the same double. nan, inf, -inf and -0.0 are spelled the way
// Python spells them, because "the two candidates disagree on nan" is the
// sentence a maintainer has to act on.
func renderFloatHex(h string) string {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 8 {
		return "float:" + h
	}
	var bits uint64
	for _, c := range b {
		bits = bits<<8 | uint64(c)
	}
	f := math.Float64frombits(bits)
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case f == 0 && math.Signbit(f):
		return "-0.0"
	}
	out := fmt.Sprintf("%v", f)
	if !strings.ContainsAny(out, ".eE") {
		out += ".0"
	}
	return out
}

// quote renders a string the way a Python repr would, minimally.
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `"` + s + `"`
	}
	return string(b)
}

func sortedStringKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
