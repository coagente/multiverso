package oracle

// M2a: the observation parser's usability table.
//
// Every case here asserts ABSENT METRICS AND A FAILED GATE, never a pass.
// The fixtures in testdata/corpus/ are recorded bytes: no Python, no
// packages, no world.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// obsNonce is the nonce every recorded fixture's header carries.
const obsNonce = "0123456789abcdef0123456789abcdef"

// fixtureCorpusDigest is the pinned corpus digest every recorded
// observation fixture's session_start reports. The parser refuses a stream
// that replayed a DIFFERENT corpus (M2a's fourth usability rule), so the
// fixtures carry one and the table below covers the mismatch.
const fixtureCorpusDigest = "mv0:fixture"

func corpusFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpus", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// declaredCorpus loads the recorded four-case corpus object every
// observation fixture was written against.
func declaredCorpus(t *testing.T) Corpus {
	t.Helper()
	var c Corpus
	if err := json.Unmarshal(corpusFixture(t, "corpus-declared.json"), &c); err != nil {
		t.Fatalf("decode corpus fixture: %v", err)
	}
	if c.Schema != SchemaCorpus || len(c.Cases) != 4 {
		t.Fatalf("corpus fixture is not the recorded four-case corpus: %+v", c)
	}
	return c
}

func TestParseObservationUsability(t *testing.T) {
	corpus := declaredCorpus(t)
	for _, tc := range []struct {
		name       string
		fixture    string
		nonce      string
		usable     bool
		wantReason string
		observed   int64
	}{
		{
			name: "a complete run over every declared case", fixture: "obs-agree-a.txt",
			nonce: obsNonce, usable: true, observed: 4,
		},
		{
			// Corpus vector 17. A world that reports on cases the corpus
			// does not contain has told us it is not running our corpus,
			// so the WHOLE observation dies — not just the stray record.
			name: "a record for an undeclared case id", fixture: "obs-undeclared-id.txt",
			nonce: obsNonce, wantReason: `observation reports case "c9999"`,
		},
		{
			// Two answers for one input is not an observation, it is a
			// choice, and the control plane does not get to make it.
			name: "one case id, two answers", fixture: "obs-repeat-id.txt",
			nonce: obsNonce, wantReason: `observation reports case "c0001" twice`,
		},
		{
			// Rule 7: a run that never said it finished may have been
			// killed mid-corpus, so no tally derived from it is admissible.
			name: "killed mid-corpus", fixture: "obs-no-finish.txt",
			nonce: obsNonce, wantReason: "evidence stream has no session_finish",
		},
		{
			// The fourth usability rule. The runner hashes the bytes it
			// loaded, so a world that replayed a corpus nobody pinned says
			// so in its OWN stream — every case id below is declared and
			// every answer is well formed, and none of it counts, because
			// the inputs behind the answers were not the race's inputs.
			//
			// The finding this closes: the corpus is one file, and a
			// sibling that rewrote it poisoned the NEXT world's replay
			// while never touching its own observation. The victim's
			// stream then convicted the victim.
			name: "a corpus nobody pinned", fixture: "obs-corpus-mismatch.txt",
			nonce: obsNonce, wantReason: "observation replayed corpus mv0:poisoned, but mv0:fixture was pinned",
		},
		{
			// Corpus vector 18: the runner silenced entirely. The stream is
			// empty, so there is no header, so there is nothing at all.
			name: "the runner was silenced", fixture: "obs-silent.txt",
			nonce: obsNonce, wantReason: "stream header missing or nonce mismatch",
		},
		{
			// Rule 1: the nonce is the identity tag against staleness — a
			// stream left in a tree by an earlier run belongs to that run.
			name: "a replayed stream from another run", fixture: "obs-agree-a.txt",
			nonce: "ffffffffffffffffffffffffffffffff", wantReason: "stream header missing or nonce mismatch",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs := ParseObservation(corpusFixture(t, tc.fixture), tc.nonce, false, corpus, fixtureCorpusDigest)
			if obs.Usable != tc.usable {
				t.Fatalf("Usable = %v (%s), want %v", obs.Usable, obs.Reason, tc.usable)
			}
			if tc.usable {
				if obs.ObservedCount() != tc.observed {
					t.Errorf("observed = %d, want %d", obs.ObservedCount(), tc.observed)
				}
				return
			}
			// The honesty rule, made structural: an unusable observation
			// carries NO cases, so every corpus_* metric derived from it is
			// absent rather than partial.
			if len(obs.Cases) != 0 {
				t.Errorf("an unusable observation carries %d cases; every metric derived from it must be absent", len(obs.Cases))
			}
			if obs.Reason == "" {
				t.Error("an unusable observation must say why")
			}
			if tc.wantReason != "" && !contains(obs.Reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to contain %q", obs.Reason, tc.wantReason)
			}
		})
	}
}

// An opaque observation is the encoding admitting it could not represent
// what it saw. It is COUNTED and it compares equal to nothing.
func TestParseObservationCountsOpaque(t *testing.T) {
	corpus := declaredCorpus(t)
	obs := ParseObservation(corpusFixture(t, "obs-opaque.txt"), obsNonce, false, corpus, fixtureCorpusDigest)
	if !obs.Usable {
		t.Fatalf("unusable: %s", obs.Reason)
	}
	if obs.Opaque != 1 {
		t.Errorf("opaque = %d, want 1", obs.Opaque)
	}
	// The observation IS present — the world reported on the case — but it
	// is not comparable, which is a different statement from absence.
	c, ok := obs.Get("c0002")
	if !ok {
		t.Fatal("the opaque case is missing from the observation entirely")
	}
	if c.Comparable() {
		t.Error("an opaque observation reports itself comparable")
	}
	if c.FP != "" {
		t.Errorf("an opaque observation carries fingerprint %q; opaque has no fingerprint", c.FP)
	}
}

// Absence of comparability is NOT agreement (M2a decision 6). This is the
// one rule a naive implementation gets wrong, so it is asserted directly on
// the type rather than only through the reducer.
func TestTwoOpaqueObservationsAreNotEqual(t *testing.T) {
	a := CaseObservation{ID: "c1", Outcome: OutcomeOpaque, Type: "Decimal"}
	b := CaseObservation{ID: "c1", Outcome: OutcomeOpaque, Type: "Decimal"}
	if a.Comparable() || b.Comparable() {
		t.Fatal("an opaque observation must never be comparable")
	}
	// Two identical opaque records must not be able to enter a comparison
	// at all — the denominator excludes them, so they can neither agree
	// nor disagree.
	if comparableEverywhere("c1", Observation{Cases: map[string]CaseObservation{"c1": a}},
		[]CohortMember{{World: "mv0:b", Obs: Observation{Cases: map[string]CaseObservation{"c1": b}}}}) {
		t.Error("two opaque observations entered the comparison denominator")
	}
}

// Rule 5, reused across dialects: a suite stream read by the observation
// parser (and vice versa) is USABLE with its foreign records ignored and
// counted. This is what let M2a add a record kind with no wire version
// bump, and it is what keeps an M1f-era reader working against an M2a
// stream.
func TestObservationToleratesForeignRecordKinds(t *testing.T) {
	corpus := declaredCorpus(t)
	raw := "mvo-evidence/v0\t" + obsNonce + "\n" +
		"1\tsession_start\t{\"corpus\":\"" + fixtureCorpusDigest + "\",\"runner\":\"mvo_corpus/v0\"}\n" +
		"2\tcollected\t{\"count\":8}\n" +
		"3\tcase\t{\"fp\":\"sha256:aa\",\"id\":\"c0001\",\"outcome\":\"value\",\"v\":1}\n" +
		"4\tsession_finish\t{\"cases\":1,\"duration_ms\":1,\"errored\":0,\"exitstatus\":0,\"opaque\":0}\n"
	obs := ParseObservation([]byte(raw), obsNonce, false, corpus, fixtureCorpusDigest)
	if !obs.Usable {
		t.Fatalf("a stream carrying a foreign record kind was rejected: %s", obs.Reason)
	}
	if obs.ObservedCount() != 1 {
		t.Errorf("observed = %d, want 1", obs.ObservedCount())
	}
	if !contains(obs.Notes, "unknown evidence record kind") {
		t.Errorf("notes = %q, want the ignored record counted for the operator", obs.Notes)
	}
	// The same bytes through the SUITE reader: usable, and the case record
	// is what gets ignored there.
	s := ParseStream([]byte(raw), obsNonce, false)
	if !s.Usable {
		t.Errorf("the suite reader rejected a corpus stream: %s", s.Reason)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
