// Package publish implements FI-1's app-less, forge-neutral publication of
// candidates and evidence under refs/multiverso/*: deterministic
// publication commits, DSSE-signed receipts and decisions (TP-1),
// retention pruning, workspace-less consumer verification (fetch-race),
// and the trunk-drift display status. Nothing published is ever
// location-trusted — self-authentication (signatures + content addresses +
// decision replay) is the only trust chain. See
// docs/design/M1d-publication.md.
package publish

import (
	"fmt"
	"strings"

	"github.com/coagente/multiverso/internal/object"
)

const (
	// RefRoot is the namespace root every published ref lives under; the
	// ref builders below only ever emit names beneath it, so refs/heads
	// cannot appear in a publish or prune refspec by construction (M1d
	// decision 10).
	RefRoot = "refs/multiverso/intent/" // + <short>/…
	// ShortLen is the length of an intent-short: the first 12 hex chars of
	// the intent digest's hex part (M1d decision 1 — discovery only; the
	// full digest travels inside every published object).
	ShortLen = 12
)

// IntentShort derives the ref-namespace short from a full intent digest
// ("mv0:<64 hex>" → first 12 hex). Malformed digests are an error.
func IntentShort(dig string) (string, error) {
	hexPart, ok := strings.CutPrefix(dig, object.DigestPrefix)
	if !ok || !isHex(hexPart, 64) {
		return "", fmt.Errorf("publish: %q is not an intent digest (%s<64 hex>)", dig, object.DigestPrefix)
	}
	return hexPart[:ShortLen], nil
}

// Namespace is the ref namespace of one intent-short.
func Namespace(short string) string { return RefRoot + short }

// CandRef names candidate ordinal's ref within short's namespace.
func CandRef(short string, ordinal int) string {
	return fmt.Sprintf("%s/cand/%d", Namespace(short), ordinal)
}

// EvidenceRef names the evidence bundle ref within short's namespace.
func EvidenceRef(short string) string { return Namespace(short) + "/evidence" }

// FileName encodes an identifier ("mv0:…" or "sha256:…") as an evidence
// tree file name: ":" → "_" (illegal/hostile in paths). The caller appends
// the extension (".json" plain, ".dsse.json" envelope). The filename is
// only ever a claim — the verifier recomputes the digest from the bytes.
func FileName(id string) string { return strings.ReplaceAll(id, ":", "_") }

// ParseFileName inverts FileName, extension-aware: "mv0_<hex>.json" →
// ("mv0:<hex>", false), "mv0_<hex>.dsse.json" / "sha256_<hex>.dsse.json" →
// (id, true). Anything else is malformed.
func ParseFileName(name string) (id string, dsse bool, err error) {
	base := name
	switch {
	case strings.HasSuffix(base, ".dsse.json"):
		dsse = true
		base = strings.TrimSuffix(base, ".dsse.json")
	case strings.HasSuffix(base, ".json"):
		base = strings.TrimSuffix(base, ".json")
	default:
		return "", false, fmt.Errorf("publish: file name %q has neither a .json nor a .dsse.json extension", name)
	}
	prefix, hexPart, ok := strings.Cut(base, "_")
	if !ok || (prefix != "mv0" && prefix != "sha256") || !isHex(hexPart, 64) {
		return "", false, fmt.Errorf("publish: file name %q does not encode an mv0: or sha256: identifier", name)
	}
	return prefix + ":" + hexPart, dsse, nil
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
