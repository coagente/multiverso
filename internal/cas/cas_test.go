package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPutRoundtrip(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{"text", []byte("hello, multiverso\n")},
		{"empty", nil},
		{"binary", []byte{0x00, 0xff, 0x10, 0x80}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			key, err := s.Put(tt.content)
			if err != nil {
				t.Fatalf("Put: %v", err)
			}
			sum := sha256.Sum256(tt.content)
			if want := "sha256:" + hex.EncodeToString(sum[:]); key != want {
				t.Fatalf("key = %q, want %q", key, want)
			}

			// Idempotent re-put: same key, no error.
			again, err := s.Put(tt.content)
			if err != nil {
				t.Fatalf("second Put: %v", err)
			}
			if again != key {
				t.Fatalf("second Put key = %q, want %q", again, key)
			}

			if !s.Has(key) {
				t.Errorf("Has(%q) = false, want true", key)
			}
			got, err := s.Get(key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if string(got) != string(tt.content) {
				t.Errorf("Get = %q, want %q", got, tt.content)
			}
		})
	}
}

func TestLayout(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	key, err := s.Put([]byte("layout"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	hexDigest := key[len("sha256:"):]
	want := filepath.Join(root, "sha256", hexDigest[:2], hexDigest[2:])
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected blob at %s: %v", want, err)
	}
	// No leftover temp files next to the blob.
	entries, err := os.ReadDir(filepath.Dir(want))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("blob dir has %d entries, want 1", len(entries))
	}
}

func TestMissingAndInvalidKeys(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	missing := "sha256:" + strings.Repeat("0", 64)
	if s.Has(missing) {
		t.Error("Has(missing) = true, want false")
	}
	if _, err := s.Get(missing); err == nil {
		t.Error("Get(missing) succeeded, want error")
	}
	for _, bad := range []string{"", "sha256:", "sha256:zz", "md5:abc", "sha256:" + "g" + missing[len("sha256:")+1:]} {
		if s.Has(bad) {
			t.Errorf("Has(%q) = true, want false", bad)
		}
		if _, err := s.Get(bad); err == nil {
			t.Errorf("Get(%q) succeeded, want error", bad)
		}
	}
}

// A blob whose bytes no longer hash to its key must never be served as
// authentic content (race and audit act on Get's result).
func TestGetDetectsCorruptedBlob(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	key, err := s.Put([]byte("authentic content"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	hexDigest := key[len("sha256:"):]
	path := filepath.Join(root, "sha256", hexDigest[:2], hexDigest[2:])
	if err := os.WriteFile(path, []byte("tampered content"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if b, err := s.Get(key); err == nil {
		t.Fatalf("Get on corrupted blob returned %q, want error", b)
	} else if !strings.Contains(err.Error(), "corrupted") {
		t.Fatalf("Get error = %q, want corruption error", err)
	}
}
