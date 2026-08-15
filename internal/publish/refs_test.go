package publish

import (
	"strings"
	"testing"
)

const (
	testHexA = "aaaaaaaaaaaabbbbbbbbbbbbccccccccccccddddddddddddeeeeeeeeeeeeffff"
	testDigA = "mv0:" + testHexA
)

func TestIntentShort(t *testing.T) {
	short, err := IntentShort(testDigA)
	if err != nil {
		t.Fatalf("IntentShort: %v", err)
	}
	if short != "aaaaaaaaaaaa" {
		t.Errorf("short = %q, want %q", short, "aaaaaaaaaaaa")
	}
	for _, bad := range []string{
		"",
		testHexA,                         // no prefix
		"sha256:" + testHexA,             // wrong prefix
		"mv0:" + testHexA[:63],           // short hex
		"mv0:" + testHexA + "0",          // long hex
		"mv0:" + strings.Repeat("Z", 64), // non-hex
	} {
		if _, err := IntentShort(bad); err == nil {
			t.Errorf("IntentShort(%q) accepted", bad)
		}
	}
}

func TestRefNameGoldens(t *testing.T) {
	short := "aaaaaaaaaaaa"
	if got := Namespace(short); got != "refs/multiverso/intent/aaaaaaaaaaaa" {
		t.Errorf("Namespace = %q", got)
	}
	if got := CandRef(short, 3); got != "refs/multiverso/intent/aaaaaaaaaaaa/cand/3" {
		t.Errorf("CandRef = %q", got)
	}
	if got := EvidenceRef(short); got != "refs/multiverso/intent/aaaaaaaaaaaa/evidence" {
		t.Errorf("EvidenceRef = %q", got)
	}
}

func TestFileNameRoundTrip(t *testing.T) {
	tests := []struct {
		id   string
		ext  string
		dsse bool
	}{
		{testDigA, ".json", false},
		{testDigA, ".dsse.json", true},
		{"sha256:" + testHexA, ".dsse.json", true},
	}
	for _, tt := range tests {
		name := FileName(tt.id) + tt.ext
		if strings.ContainsAny(name, ":") {
			t.Errorf("FileName(%q) leaves a colon: %q", tt.id, name)
		}
		id, dsse, err := ParseFileName(name)
		if err != nil {
			t.Errorf("ParseFileName(%q): %v", name, err)
			continue
		}
		if id != tt.id || dsse != tt.dsse {
			t.Errorf("ParseFileName(%q) = (%q, %v), want (%q, %v)", name, id, dsse, tt.id, tt.dsse)
		}
	}
}

func TestParseFileNameRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"mv0_" + testHexA,                // no extension
		"mv0_" + testHexA + ".txt",       // wrong extension
		"mv0-" + testHexA + ".json",      // wrong separator
		"mv1_" + testHexA + ".json",      // unknown prefix
		"mv0_" + testHexA[:60] + ".json", // short hex
		"mv0_" + strings.Repeat("Z", 64) + ".json", // non-hex
		"mv0_.json",
		".json",
	} {
		if id, _, err := ParseFileName(bad); err == nil {
			t.Errorf("ParseFileName(%q) accepted as %q", bad, id)
		}
	}
}

func TestCheckShortCollision(t *testing.T) {
	self := testDigA
	other := "mv0:" + "aaaaaaaaaaaa" + strings.Repeat("0", 52) // same short, different digest
	distinct := "mv0:" + strings.Repeat("b", 64)
	if err := checkShortCollision(self, "aaaaaaaaaaaa", []string{self, distinct}); err != nil {
		t.Errorf("no-collision case errored: %v", err)
	}
	// The intent itself appearing twice in the scan is not a collision.
	if err := checkShortCollision(self, "aaaaaaaaaaaa", []string{self, self}); err != nil {
		t.Errorf("self-duplicate case errored: %v", err)
	}
	err := checkShortCollision(self, "aaaaaaaaaaaa", []string{self, other})
	if err == nil {
		t.Fatal("collision accepted")
	}
	if !strings.Contains(err.Error(), "collision") {
		t.Errorf("error %q does not name the collision", err)
	}
}
