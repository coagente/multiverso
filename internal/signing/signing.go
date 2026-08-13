// Package signing implements Ed25519 keypairs and DSSE envelopes (TP-1):
// PEM key files under a workspace keys directory, PAE pre-authentication
// encoding, and Sign/Verify over arbitrary payloads. Stdlib only.
//
// Invariant: this package writes files only inside the keysDir it is
// handed; the only call sites pass Workspace.KeysDir(), which lives under
// the git-ignored .multiverso/ directory, so keys never enter history and
// are never written elsewhere.
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/coagente/multiverso/internal/object"
)

const (
	// PayloadTypeInToto is the DSSE payloadType for in-toto statements.
	PayloadTypeInToto = "application/vnd.in-toto+json"
	// PrivName is the private key file name inside a keys directory.
	PrivName = "local.key"
	// PubName is the public key file name inside a keys directory.
	PubName = "local.pub"
)

const (
	pemTypePriv = "PRIVATE KEY" // PKCS#8
	pemTypePub  = "PUBLIC KEY"  // PKIX
)

// Signer is a loaded local keypair.
type Signer struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
	KeyID   string // "mv0:" + hex(sha256(Public))
}

// KeyID derives the key identifier from a raw public key:
// object.DigestPrefix + hex(sha256(raw 32 bytes)) — encoding-independent,
// same prefix family as object digests.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return object.DigestPrefix + hex.EncodeToString(sum[:])
}

// Generate creates a fresh Ed25519 keypair under keysDir (created 0700):
// PrivName as PEM PKCS#8 and PubName as PEM PKIX, both mode 0600. It
// refuses to overwrite: an error is returned if either file exists.
func Generate(keysDir string) (*Signer, error) {
	privPath := filepath.Join(keysDir, PrivName)
	pubPath := filepath.Join(keysDir, PubName)
	for _, path := range []string{privPath, pubPath} {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("signing: refusing to overwrite existing key file %s", path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("signing: stat %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return nil, fmt.Errorf("signing: create keys dir %s: %w", keysDir, err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("signing: generate keypair: %w", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("signing: encode private key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("signing: encode public key: %w", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: pemTypePriv, Bytes: privDER})
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: pemTypePub, Bytes: pubDER})
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		return nil, fmt.Errorf("signing: write %s: %w", privPath, err)
	}
	if err := os.WriteFile(pubPath, pubPEM, 0o600); err != nil {
		os.Remove(privPath) // leave no half-written keypair behind
		return nil, fmt.Errorf("signing: write %s: %w", pubPath, err)
	}
	return &Signer{Private: priv, Public: pub, KeyID: KeyID(pub)}, nil
}

// Load reads the keypair from keysDir and checks that the stored public
// key matches the private key's derived public key.
func Load(keysDir string) (*Signer, error) {
	priv, err := loadPrivateKeyFile(filepath.Join(keysDir, PrivName))
	if err != nil {
		return nil, err
	}
	pub, _, err := LoadPublicKeyFile(filepath.Join(keysDir, PubName))
	if err != nil {
		return nil, err
	}
	derived, ok := priv.Public().(ed25519.PublicKey)
	if !ok || !derived.Equal(pub) {
		return nil, fmt.Errorf("signing: %s in %s does not match the private key", PubName, keysDir)
	}
	return &Signer{Private: priv, Public: pub, KeyID: KeyID(pub)}, nil
}

// LoadPublicKeyFile reads a PEM PKIX Ed25519 public key and returns it
// with its key ID.
func LoadPublicKeyFile(path string) (ed25519.PublicKey, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("signing: read public key %s: %w", path, err)
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != pemTypePub {
		return nil, "", fmt.Errorf("signing: %s is not a PEM %q block", path, pemTypePub)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("signing: parse public key %s: %w", path, err)
	}
	pub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, "", fmt.Errorf("signing: %s holds a %T, want Ed25519", path, parsed)
	}
	return pub, KeyID(pub), nil
}

func loadPrivateKeyFile(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("signing: read private key %s: %w", path, err)
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != pemTypePriv {
		return nil, fmt.Errorf("signing: %s is not a PEM %q block", path, pemTypePriv)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("signing: parse private key %s: %w", path, err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("signing: %s holds a %T, want Ed25519", path, parsed)
	}
	return priv, nil
}

// Envelope is a DSSE envelope (in-toto DSSE spec).
type Envelope struct {
	Payload     string      `json:"payload"`     // base64.StdEncoding
	PayloadType string      `json:"payloadType"` // PayloadTypeInToto for attestations
	Signatures  []Signature `json:"signatures"`
}

// Signature is one DSSE signature entry.
type Signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"` // base64.StdEncoding
}

// pae is the DSSE pre-authentication encoding:
// "DSSEv1" SP dec(len(type)) SP type SP dec(len(payload)) SP payload.
func pae(payloadType string, payload []byte) []byte {
	return fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload)
}

// Sign signs PAE(payloadType, payload) and returns a one-signature
// envelope. Ed25519 signatures are deterministic (RFC 8032), so the
// envelope is a pure function of (payload, payloadType, key).
func Sign(s *Signer, payloadType string, payload []byte) (Envelope, error) {
	if s == nil || len(s.Private) != ed25519.PrivateKeySize {
		return Envelope{}, errors.New("signing: sign: no private key loaded")
	}
	sig := ed25519.Sign(s.Private, pae(payloadType, payload))
	return Envelope{
		Payload:     base64.StdEncoding.EncodeToString(payload),
		PayloadType: payloadType,
		Signatures:  []Signature{{KeyID: s.KeyID, Sig: base64.StdEncoding.EncodeToString(sig)}},
	}, nil
}

// Verify checks that at least one signature verifies against pub over
// PAE(env.PayloadType, payload) and returns the decoded payload. The
// caller asserts env.PayloadType — PAE already binds it cryptographically.
// Signatures whose keyid is present and differs from KeyID(pub) are
// skipped (DSSE treats keyid as a hint) and reported if nothing verifies.
func Verify(env Envelope, pub ed25519.PublicKey) ([]byte, error) {
	if len(env.Signatures) == 0 {
		return nil, errors.New("signing: verify: envelope has no signatures")
	}
	payload, err := decodeBase64(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("signing: verify: decode payload: %w", err)
	}
	msg := pae(env.PayloadType, payload)
	want := KeyID(pub)
	var skipped []string
	for _, sig := range env.Signatures {
		if sig.KeyID != "" && sig.KeyID != want {
			skipped = append(skipped, sig.KeyID)
			continue
		}
		raw, err := decodeBase64(sig.Sig)
		if err != nil {
			continue
		}
		if ed25519.Verify(pub, msg, raw) {
			return payload, nil
		}
	}
	if len(skipped) > 0 {
		return nil, fmt.Errorf("signing: verify: no signature verified against key %s (skipped keyids: %s)",
			want, strings.Join(skipped, ", "))
	}
	return nil, fmt.Errorf("signing: verify: no signature verified against key %s", want)
}

// decodeBase64 accepts standard or URL-safe alphabets, padded or raw (DSSE
// spec leniency on decode; Sign always emits padded StdEncoding).
func decodeBase64(s string) ([]byte, error) {
	std := strings.NewReplacer("-", "+", "_", "/").Replace(s)
	if strings.HasSuffix(std, "=") {
		return base64.StdEncoding.DecodeString(std)
	}
	return base64.RawStdEncoding.DecodeString(std)
}
