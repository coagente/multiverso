package publish

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/signing"
)

// DSSE payload types for published items (TP-1). Raw DSSE over the
// object's canonical bytes, not an in-toto Statement (M1d decision 5): the
// payload is byte-identical to the ledger's canonical object, so
// object.DigestBytes(payload) reproduces the digest decisions and ledger
// events cite. PAE binds the payloadType, so receipt envelopes, decision
// envelopes, and admission attestations are cryptographically
// domain-separated even under the same key.
const (
	PayloadTypeReceipt  = "application/vnd.multiverso.receipt+json"
	PayloadTypeDecision = "application/vnd.multiverso.decision+json"
)

// SignItem wraps canonical object bytes in a one-signature DSSE envelope
// and returns the envelope's canonical bytes. Deterministic: same payload
// + key ⇒ same bytes (RFC 8032 + object.Canonical), which is what makes
// sign-on-publish re-minting byte-stable (M1d decision 9).
func SignItem(s *signing.Signer, payloadType string, payload []byte) ([]byte, error) {
	env, err := signing.Sign(s, payloadType, payload)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
	b, err := object.Canonical(env)
	if err != nil {
		return nil, fmt.Errorf("publish: encode envelope: %w", err)
	}
	return b, nil
}

// OpenItem verifies an envelope's signature against pub, asserts the
// payloadType, and returns the payload bytes.
func OpenItem(envBytes []byte, payloadType string, pub ed25519.PublicKey) ([]byte, error) {
	var env signing.Envelope
	if err := json.Unmarshal(envBytes, &env); err != nil {
		return nil, fmt.Errorf("publish: decode envelope: %w", err)
	}
	if env.PayloadType != payloadType {
		return nil, fmt.Errorf("publish: payloadType %q, want %q", env.PayloadType, payloadType)
	}
	payload, err := signing.Verify(env, pub)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
	return payload, nil
}
