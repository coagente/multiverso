package main

import (
	"io"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

func TestSplitDigestArg(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantDig  string
		wantRest []string
	}{
		{"digest first", []string{"mv0:ab", "--dir", "x"}, "mv0:ab", []string{"--dir", "x"}},
		{"flags only", []string{"--dir", "x"}, "", []string{"--dir", "x"}},
		{"empty", nil, "", nil},
		{"leading dash", []string{"-h"}, "", []string{"-h"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dig, rest := splitDigestArg(tt.args)
			if dig != tt.wantDig {
				t.Errorf("digest = %q, want %q", dig, tt.wantDig)
			}
			if len(rest) != len(tt.wantRest) {
				t.Fatalf("rest = %v, want %v", rest, tt.wantRest)
			}
			for i := range rest {
				if rest[i] != tt.wantRest[i] {
					t.Errorf("rest[%d] = %q, want %q", i, rest[i], tt.wantRest[i])
				}
			}
		})
	}
}

func TestPositionalDigest(t *testing.T) {
	tests := []struct {
		name    string
		peeled  string
		rest    []string // parsed by the flag set, as after splitDigestArg
		want    string
		wantErr string // substring of the usage error, "" for success
	}{
		{"peeled digest", "mv0:ab", nil, "mv0:ab", ""},
		{"trailing digest", "", []string{"mv0:ab"}, "mv0:ab", ""},
		{"missing digest", "", nil, "", "intent digest required"},
		// stdlib flag stops at the first positional: a flag after a
		// trailing digest must be a usage error, not silently dropped.
		{"flag after trailing digest", "", []string{"mv0:ab", "--keep-worlds"}, "", "--keep-worlds"},
		{"extra arg after peeled digest", "mv0:ab", []string{"--dir", "x", "stray"}, "", "stray"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newFlagSet("test", io.Discard)
			fs.String("dir", ".", "")
			if err := fs.Parse(tt.rest); err != nil {
				t.Fatalf("Parse(%v): %v", tt.rest, err)
			}
			got, err := positionalDigest(tt.peeled, fs, "test")
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("positionalDigest = %q, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("positionalDigest: %v", err)
			}
			if got != tt.want {
				t.Errorf("positionalDigest = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDiffDecision(t *testing.T) {
	base := object.Decision{
		Type:      "SELECT",
		Subject:   []string{"mv0:w1", "mv0:w2"},
		Evidence:  []string{"mv0:r1", "mv0:r2"},
		Rationale: "gate suite-pass",
		CreatedAt: "2026-01-01T00:00:00Z",
	}
	tests := []struct {
		name     string
		mutate   func(d *object.Decision)
		wantDiff bool
	}{
		{"identical", func(d *object.Decision) {}, false},
		{"created_at excluded", func(d *object.Decision) { d.CreatedAt = "2030-01-01T00:00:00Z" }, false},
		{"type", func(d *object.Decision) { d.Type = "REJECT" }, true},
		{"subject order", func(d *object.Decision) { d.Subject = []string{"mv0:w2", "mv0:w1"} }, true},
		{"evidence", func(d *object.Decision) { d.Evidence = []string{"mv0:r1"} }, true},
		{"rationale", func(d *object.Decision) { d.Rationale = "other" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replayed := base
			tt.mutate(&replayed)
			detail := diffDecision(base, replayed)
			if (detail != "") != tt.wantDiff {
				t.Errorf("diffDecision = %q, wantDiff %v", detail, tt.wantDiff)
			}
		})
	}
}
