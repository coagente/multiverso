package publish

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/signing"
)

func testSigner(t *testing.T) *signing.Signer {
	t.Helper()
	s, err := signing.Generate(filepath.Join(t.TempDir(), "keys"))
	if err != nil {
		t.Fatalf("signing.Generate: %v", err)
	}
	return s
}

func TestSignOpenRoundTrip(t *testing.T) {
	s := testSigner(t)
	payload := []byte(`{"schema":"multiverso.dev/receipt/v0","x":1}`)
	for _, pt := range []string{PayloadTypeReceipt, PayloadTypeDecision} {
		env, err := SignItem(s, pt, payload)
		if err != nil {
			t.Fatalf("SignItem(%s): %v", pt, err)
		}
		got, err := OpenItem(env, pt, s.Public)
		if err != nil {
			t.Fatalf("OpenItem(%s): %v", pt, err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("payload round-trip = %q, want %q", got, payload)
		}
	}
}

func TestCrossTypeConfusionFails(t *testing.T) {
	s := testSigner(t)
	payload := []byte(`{"schema":"multiverso.dev/receipt/v0"}`)
	env, err := SignItem(s, PayloadTypeReceipt, payload)
	if err != nil {
		t.Fatal(err)
	}
	// A receipt envelope must never open as a decision envelope: the
	// payloadType assertion catches the mislabel, and even a relabeled
	// envelope fails because PAE bound the original type into the
	// signature.
	if _, err := OpenItem(env, PayloadTypeDecision, s.Public); err == nil {
		t.Fatal("receipt envelope opened as a decision envelope")
	}
	relabeled := bytes.Replace(env,
		[]byte(PayloadTypeReceipt), []byte(PayloadTypeDecision), 1)
	if bytes.Equal(relabeled, env) {
		t.Fatal("relabel had no effect")
	}
	if _, err := OpenItem(relabeled, PayloadTypeDecision, s.Public); err == nil {
		t.Fatal("PAE domain separation failed: relabeled envelope verified")
	}
}

func TestTamperedItemFails(t *testing.T) {
	s := testSigner(t)
	payload := []byte(`{"schema":"multiverso.dev/decision/v0"}`)
	env, err := SignItem(s, PayloadTypeDecision, payload)
	if err != nil {
		t.Fatal(err)
	}
	// Tamper the payload characters inside the envelope JSON.
	tampered := bytes.Replace(env, []byte(`"payload":"`), []byte(`"payload":"A`), 1)
	if _, err := OpenItem(tampered, PayloadTypeDecision, s.Public); err == nil {
		t.Fatal("tampered payload verified")
	}
	// Wrong key.
	other := testSigner(t)
	if _, err := OpenItem(env, PayloadTypeDecision, other.Public); err == nil {
		t.Fatal("envelope verified against the wrong key")
	}
	// Garbage envelope bytes.
	if _, err := OpenItem([]byte("not json"), PayloadTypeDecision, s.Public); err == nil {
		t.Fatal("garbage bytes verified")
	}
}

func TestDeterministicEnvelopeBytes(t *testing.T) {
	s := testSigner(t)
	payload := []byte(`{"a":1}`)
	one, err := SignItem(s, PayloadTypeReceipt, payload)
	if err != nil {
		t.Fatal(err)
	}
	two, err := SignItem(s, PayloadTypeReceipt, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Errorf("envelope bytes differ across signings:\n%s\n%s", one, two)
	}
	if !strings.Contains(string(one), `"payloadType":"`+PayloadTypeReceipt+`"`) {
		t.Errorf("canonical envelope missing payloadType: %s", one)
	}
}
