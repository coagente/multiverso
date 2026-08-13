package signing

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

func mustGenerate(t *testing.T, keysDir string) *Signer {
	t.Helper()
	s, err := Generate(keysDir)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return s
}

// PAE golden vectors; the second is the DSSE spec's own.
func TestPAEGolden(t *testing.T) {
	tests := []struct {
		payloadType string
		payload     string
		want        string
	}{
		{
			"application/vnd.in-toto+json", "hello",
			"DSSEv1 28 application/vnd.in-toto+json 5 hello",
		},
		{
			"http://example.com/HelloWorld", "hello world",
			"DSSEv1 29 http://example.com/HelloWorld 11 hello world",
		},
	}
	for _, tt := range tests {
		if got := string(pae(tt.payloadType, []byte(tt.payload))); got != tt.want {
			t.Errorf("pae(%q, %q) = %q, want %q", tt.payloadType, tt.payload, got, tt.want)
		}
	}
}

func TestKeyID(t *testing.T) {
	s := mustGenerate(t, filepath.Join(t.TempDir(), "keys"))
	if !strings.HasPrefix(s.KeyID, object.DigestPrefix) {
		t.Errorf("KeyID = %q, want %q prefix", s.KeyID, object.DigestPrefix)
	}
	if len(s.KeyID) != len(object.DigestPrefix)+64 {
		t.Errorf("KeyID = %q, want %d hex chars after prefix", s.KeyID, 64)
	}
	if got := KeyID(s.Public); got != s.KeyID {
		t.Errorf("KeyID(pub) = %q, Signer.KeyID = %q", got, s.KeyID)
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	s := mustGenerate(t, filepath.Join(t.TempDir(), "keys"))
	payload := []byte(`{"hello":"world"}`)
	env, err := Sign(s, PayloadTypeInToto, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if env.PayloadType != PayloadTypeInToto {
		t.Errorf("PayloadType = %q, want %q", env.PayloadType, PayloadTypeInToto)
	}
	if len(env.Signatures) != 1 || env.Signatures[0].KeyID != s.KeyID {
		t.Fatalf("Signatures = %+v, want one signature with keyid %s", env.Signatures, s.KeyID)
	}
	got, err := Verify(env, s.Public)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("Verify payload = %q, want %q", got, payload)
	}
}

// Ed25519 signatures are deterministic (RFC 8032): the envelope bytes are
// a pure function of (statement, key) — the M1a trailer relies on this.
func TestSignDeterministic(t *testing.T) {
	s := mustGenerate(t, filepath.Join(t.TempDir(), "keys"))
	payload := []byte("payload")
	a, err := Sign(s, PayloadTypeInToto, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	b, err := Sign(s, PayloadTypeInToto, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if a.Payload != b.Payload || a.PayloadType != b.PayloadType ||
		len(a.Signatures) != 1 || len(b.Signatures) != 1 || a.Signatures[0] != b.Signatures[0] {
		t.Errorf("Sign not deterministic: %+v vs %+v", a, b)
	}
}

func TestVerifyFailures(t *testing.T) {
	dir := t.TempDir()
	s := mustGenerate(t, filepath.Join(dir, "keys"))
	other := mustGenerate(t, filepath.Join(dir, "other"))
	payload := []byte(`{"hello":"world"}`)
	env, err := Sign(s, PayloadTypeInToto, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(e *Envelope)
		pub     ed25519.PublicKey
		wantSub string
	}{
		{"tampered payload", func(e *Envelope) {
			raw, _ := base64.StdEncoding.DecodeString(e.Payload)
			raw[0] ^= 0xFF
			e.Payload = base64.StdEncoding.EncodeToString(raw)
		}, s.Public, "no signature verified"},
		{"tampered signature", func(e *Envelope) {
			raw, _ := base64.StdEncoding.DecodeString(e.Signatures[0].Sig)
			raw[0] ^= 0xFF
			e.Signatures[0].Sig = base64.StdEncoding.EncodeToString(raw)
		}, s.Public, "no signature verified"},
		{"tampered payload type", func(e *Envelope) {
			e.PayloadType = "application/vnd.other+json"
		}, s.Public, "no signature verified"},
		{"wrong key", func(e *Envelope) {}, other.Public, "no signature verified"},
		{"wrong keyid skipped and reported", func(e *Envelope) {
			e.Signatures[0].KeyID = "mv0:not-the-key"
		}, s.Public, "skipped keyids: mv0:not-the-key"},
		{"no signatures", func(e *Envelope) {
			e.Signatures = nil
		}, s.Public, "no signatures"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := env
			bad.Signatures = append([]Signature(nil), env.Signatures...)
			tt.mutate(&bad)
			_, err := Verify(bad, tt.pub)
			if err == nil {
				t.Fatal("Verify: want error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want substring %q", err, tt.wantSub)
			}
		})
	}
}

// DSSE decode leniency: standard or URL-safe alphabet, padded or raw.
func TestVerifyBase64Leniency(t *testing.T) {
	s := mustGenerate(t, filepath.Join(t.TempDir(), "keys"))
	payload := []byte{0xfb, 0xff, 0xfe, 0x01, 0x02} // exercises +/ vs -_ alphabets
	env, err := Sign(s, PayloadTypeInToto, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	recode := map[string]func([]byte) string{
		"std padded": base64.StdEncoding.EncodeToString,
		"std raw":    base64.RawStdEncoding.EncodeToString,
		"url padded": base64.URLEncoding.EncodeToString,
		"url raw":    base64.RawURLEncoding.EncodeToString,
	}
	rawSig, err := base64.StdEncoding.DecodeString(env.Signatures[0].Sig)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	for name, enc := range recode {
		t.Run(name, func(t *testing.T) {
			e := env
			e.Payload = enc(payload)
			e.Signatures = []Signature{{KeyID: s.KeyID, Sig: enc(rawSig)}}
			got, err := Verify(e, s.Public)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if string(got) != string(payload) {
				t.Errorf("payload = %x, want %x", got, payload)
			}
		})
	}
}

// Generate writes exactly {local.key, local.pub} under keysDir, dir 0700,
// files 0600 — and nowhere else (keys must never leave the keys dir).
func TestGenerateFileSetAndModes(t *testing.T) {
	root := t.TempDir()
	keysDir := filepath.Join(root, "keys")
	mustGenerate(t, keysDir)

	info, err := os.Stat(keysDir)
	if err != nil {
		t.Fatalf("stat keys dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("keys dir mode = %o, want 0700", got)
	}
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		t.Fatalf("read keys dir: %v", err)
	}
	want := map[string]bool{PrivName: true, PubName: true}
	if len(entries) != len(want) {
		t.Fatalf("keys dir holds %d entries, want %d", len(entries), len(want))
	}
	for _, e := range entries {
		if !want[e.Name()] {
			t.Errorf("unexpected file %s in keys dir", e.Name())
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", e.Name(), err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 0600", e.Name(), got)
		}
	}
	// Nothing was written outside keysDir.
	rootEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rootEntries) != 1 || rootEntries[0].Name() != "keys" {
		t.Errorf("root holds %v, want only keys/", rootEntries)
	}
}

func TestGenerateRefusesOverwrite(t *testing.T) {
	keysDir := filepath.Join(t.TempDir(), "keys")
	mustGenerate(t, keysDir)
	if _, err := Generate(keysDir); err == nil {
		t.Fatal("second Generate: want error, got nil")
	} else if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("error = %q, want 'refusing to overwrite'", err)
	}
	// A lone leftover file also blocks generation.
	lone := filepath.Join(t.TempDir(), "keys")
	if err := os.MkdirAll(lone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lone, PubName), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(lone); err == nil {
		t.Fatal("Generate over a lone pub file: want error, got nil")
	}
}

func TestLoadRoundTrip(t *testing.T) {
	keysDir := filepath.Join(t.TempDir(), "keys")
	gen := mustGenerate(t, keysDir)
	loaded, err := Load(keysDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.KeyID != gen.KeyID {
		t.Errorf("loaded KeyID = %q, want %q", loaded.KeyID, gen.KeyID)
	}
	if !loaded.Public.Equal(gen.Public) {
		t.Error("loaded public key differs from generated")
	}
	if !loaded.Private.Equal(gen.Private) {
		t.Error("loaded private key differs from generated")
	}
}

func TestLoadRejectsMismatchedKeys(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	mustGenerate(t, a)
	mustGenerate(t, b)
	// Swap b's public key into a: the pair no longer matches.
	pub, err := os.ReadFile(filepath.Join(b, PubName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, PubName), pub, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(a); err == nil {
		t.Fatal("Load with mismatched keys: want error, got nil")
	} else if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("error = %q, want 'does not match'", err)
	}
}

// Workspace.Signer relies on fs.ErrNotExist surviving Load's wrapping to
// suggest `mvo init --keys`.
func TestLoadMissingKeysIsErrNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "keys"))
	if err == nil {
		t.Fatal("Load on empty dir: want error, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false for %q", err)
	}
}

func TestLoadPublicKeyFileRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-key.pub")
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadPublicKeyFile(path); err == nil {
		t.Fatal("LoadPublicKeyFile on garbage: want error, got nil")
	}
}
