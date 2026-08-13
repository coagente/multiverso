package agent

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

// Env allowlist (decision 14), pure function: base set present when set in
// the parent, unlisted parent vars absent, extras copied by NAME, unset
// extras omitted, duplicates collapsed.
func TestBuildEnv(t *testing.T) {
	t.Setenv("PATH", "/fake/bin")
	t.Setenv("MVO_TEST_SECRET", "s3cret")   // NOT allowlisted → must not leak
	t.Setenv("MVO_TEST_EXTRA", "visible")   // allowlisted extra
	t.Setenv("ANTHROPIC_API_KEY", "sk-not") // API keys need explicit allowlisting

	env := buildEnv([]string{"MVO_TEST_EXTRA", "MVO_TEST_UNSET_XYZ", "MVO_TEST_EXTRA"})

	if !slices.Contains(env, "PATH=/fake/bin") {
		t.Errorf("env missing base PATH: %v", env)
	}
	count := 0
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "MVO_TEST_SECRET":
			t.Errorf("unlisted parent var leaked into the child env: %v", kv)
		case "ANTHROPIC_API_KEY":
			t.Errorf("API key leaked without explicit allowlisting: %v", kv)
		case "MVO_TEST_UNSET_XYZ":
			t.Errorf("unset name produced an entry: %v", kv)
		case "MVO_TEST_EXTRA":
			count++
		case "PATH", "HOME", "TMPDIR", "USER", "LANG", "LC_ALL":
			// base set, fine
		default:
			t.Errorf("unexpected env entry %q (allowlist is names-only)", kv)
		}
	}
	if count != 1 {
		t.Errorf("MVO_TEST_EXTRA appears %d times, want exactly 1", count)
	}
}

// Process-level allowlist proof: FAKE_AGENT_MODE set in the parent but NOT
// allowlisted never reaches the fixture, which then runs its default happy
// path.
func TestEnvAllowlistExcludesUnlistedVars(t *testing.T) {
	usePATHFixtures(t)
	t.Setenv("FAKE_AGENT_MODE", "bad-exit") // would CRASH if it leaked
	a, err := New("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	h, err := a.Start(t.Context(), RunSpec{WorldDir: t.TempDir(), Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != object.OutcomeCompleted {
		t.Errorf("outcome = %q, want COMPLETED — FAKE_AGENT_MODE leaked through the allowlist", res.Outcome)
	}
}

// lineWriter: the transcript keeps every byte; parsing sees lines split on
// \n, with an unterminated tail flushed at finish.
func TestLineWriterSplitsAndTees(t *testing.T) {
	var transcript bytes.Buffer
	type rec struct {
		line      string
		oversized bool
	}
	var got []rec
	lw := &lineWriter{transcript: &transcript, parse: func(raw []byte, oversized bool) {
		got = append(got, rec{string(raw), oversized})
	}}

	lw.Write([]byte("alpha\nbe"))
	lw.Write([]byte("ta\ngamma")) // gamma unterminated
	lw.finish(func() {})

	want := []rec{{"alpha", false}, {"beta", false}, {"gamma", false}}
	if !slices.Equal(got, want) {
		t.Errorf("parsed lines = %v, want %v", got, want)
	}
	if transcript.String() != "alpha\nbeta\ngamma" {
		t.Errorf("transcript = %q, want the verbatim byte stream", transcript.String())
	}
}

// An oversized line is truncated for parsing only (flagged oversized → one
// unknown event) while the transcript still holds every byte.
func TestLineWriterOversizedLine(t *testing.T) {
	var transcript bytes.Buffer
	var lines int
	var sawOversized bool
	var parsedLen int
	lw := &lineWriter{transcript: &transcript, parse: func(raw []byte, oversized bool) {
		lines++
		sawOversized = oversized
		parsedLen = len(raw)
	}}

	huge := bytes.Repeat([]byte("x"), maxLineBytes+4096)
	lw.Write(huge)
	lw.Write([]byte("\n"))

	if lines != 1 {
		t.Fatalf("parsed %d lines, want 1", lines)
	}
	if !sawOversized {
		t.Error("oversized flag not set")
	}
	if parsedLen != maxLineBytes {
		t.Errorf("parsed line length = %d, want cap %d", parsedLen, maxLineBytes)
	}
	if transcript.Len() != len(huge)+1 {
		t.Errorf("transcript = %d bytes, want %d (every byte kept)", transcript.Len(), len(huge)+1)
	}

	// The claude parser turns an oversized line into one tolerated
	// "unknown" event.
	p := &claudeParser{}
	if kind := p.line(huge[:maxLineBytes], true); kind != EventUnknown {
		t.Errorf("oversized line kind = %q, want unknown", kind)
	}
}
