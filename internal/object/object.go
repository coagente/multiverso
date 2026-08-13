// Package object implements canonical JSON serialization and content
// digests per M0 design (DP-1, NFR-1): keys sorted lexicographically at
// every level, no insignificant whitespace, UTF-8 output. It also defines
// the M0 object types (Intent, World, Receipt, Decision, Policy).
package object

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// DigestPrefix tags digests of canonical object bytes.
const DigestPrefix = "mv0:"

// CASKey converts an object digest ("mv0:<hex>") into the CAS key of the
// object's canonical bytes ("sha256:<hex>") — both name the same sha256,
// so the conversion is a prefix swap.
func CASKey(dig string) (string, error) {
	hexPart, ok := strings.CutPrefix(dig, DigestPrefix)
	if !ok {
		return "", fmt.Errorf("object: not an object digest: %q", dig)
	}
	return "sha256:" + hexPart, nil
}

// Canonical returns the canonical JSON encoding of v. Any value accepted
// by encoding/json is normalized through generic decoding so that struct
// field order never influences the output.
func Canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("object: marshal %T: %w", v, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep integers exact; v0 schemas forbid floats (DP-1)
	var g any
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("object: normalize %T: %w", v, err)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, g); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Digest returns "mv0:"+hex(sha256(canonical(v))) and the canonical bytes.
func Digest(v any) (string, []byte, error) {
	b, err := Canonical(v)
	if err != nil {
		return "", nil, err
	}
	return DigestBytes(b), b, nil
}

// DigestBytes digests raw bytes that are already canonical (e.g. a ledger
// payload blob).
func DigestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return DigestPrefix + hex.EncodeToString(sum[:])
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(x.String())
	case string:
		writeString(buf, x)
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("object: unsupported canonical type %T", v)
	}
	return nil
}

// writeString emits s as a JSON string with the minimal, deterministic
// escape set (JCS-style): quote, backslash, and control characters only;
// all other runes pass through as UTF-8.
func writeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}
