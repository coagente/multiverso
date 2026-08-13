package attest

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

func goldenPredicate() Predicate {
	return Predicate{
		Intent:         "mv0:" + strings.Repeat("aa", 32),
		World:          "mv0:" + strings.Repeat("bb", 32),
		Decision:       "mv0:" + strings.Repeat("cc", 32),
		SelectDecision: "mv0:" + strings.Repeat("dd", 32),
		Evidence: []string{ // deliberately unsorted; New must sort a copy
			"mv0:" + strings.Repeat("ff", 32),
			"mv0:" + strings.Repeat("ee", 32),
		},
		Policy:         "mv0:" + strings.Repeat("11", 32),
		BudgetConsumed: Budget{WallMS: 1234},
		ProducerKeyID:  "mv0:" + strings.Repeat("22", 32),
		Trunk:          Trunk{Branch: "main", ParentCommit: strings.Repeat("3e5a", 10)},
	}
}

// Golden canonical statement bytes: the digest recorded in the ledger (and
// signed into the trailer bundle) must never drift for the same inputs.
func TestStatementGolden(t *testing.T) {
	stmt, err := New("main", "git:"+strings.Repeat("09", 20), goldenPredicate())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	canon, err := object.Canonical(stmt)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}

	want := `{"_type":"https://in-toto.io/Statement/v1",` +
		`"predicate":{` +
		`"budget_consumed":{"wall_ms":1234},` +
		`"decision":"mv0:` + strings.Repeat("cc", 32) + `",` +
		`"evidence":["mv0:` + strings.Repeat("ee", 32) + `","mv0:` + strings.Repeat("ff", 32) + `"],` +
		`"intent":"mv0:` + strings.Repeat("aa", 32) + `",` +
		`"policy":"mv0:` + strings.Repeat("11", 32) + `",` +
		`"producer_key_id":"mv0:` + strings.Repeat("22", 32) + `",` +
		`"select_decision":"mv0:` + strings.Repeat("dd", 32) + `",` +
		`"trunk":{"branch":"main","parent_commit":"` + strings.Repeat("3e5a", 10) + `"},` +
		`"world":"mv0:` + strings.Repeat("bb", 32) + `"},` +
		`"predicateType":"multiverso.dev/admission/v0",` +
		`"subject":[{"digest":{"gitTree":"` + strings.Repeat("09", 20) + `"},"name":"refs/heads/main"}]}`
	if string(canon) != want {
		t.Errorf("canonical statement =\n%s\nwant\n%s", canon, want)
	}

	// Digest stability over the golden bytes.
	const wantDig = "mv0:2971599e8aa40773a2ca140a5509d29a28c54245a9e69a8702a00dff90378246"
	if got := object.DigestBytes(canon); got != wantDig {
		t.Errorf("statement digest = %s, want %s", got, wantDig)
	}
}

func TestNewSortsEvidenceCopy(t *testing.T) {
	pred := goldenPredicate()
	stmt, err := New("main", "git:"+strings.Repeat("09", 20), pred)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := stmt.Predicate.Evidence
	if len(got) != 2 || got[0] >= got[1] {
		t.Errorf("statement evidence = %v, want sorted ascending", got)
	}
	// The caller's slice is untouched (New sorts a copy).
	if pred.Evidence[0] != "mv0:"+strings.Repeat("ff", 32) {
		t.Errorf("caller evidence mutated: %v", pred.Evidence)
	}
}

func TestNewStripsTreePrefix(t *testing.T) {
	hex := strings.Repeat("09", 20)
	stmt, err := New("main", "git:"+hex, goldenPredicate())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(stmt.Subject) != 1 {
		t.Fatalf("subject = %v, want exactly one", stmt.Subject)
	}
	if got := stmt.Subject[0].Digest["gitTree"]; got != hex {
		t.Errorf("gitTree = %q, want bare hex %q", got, hex)
	}
	if got := stmt.Subject[0].Name; got != "refs/heads/main" {
		t.Errorf("subject name = %q, want refs/heads/main", got)
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	hex40 := strings.Repeat("09", 20)
	tests := []struct {
		name    string
		branch  string
		tree    string
		mutate  func(p *Predicate)
		wantSub string
	}{
		{"empty branch", "", "git:" + hex40, func(p *Predicate) {}, "empty branch"},
		{"empty landing tree", "main", "", func(p *Predicate) {}, "empty landing tree"},
		{"unprefixed tree", "main", hex40, func(p *Predicate) {}, "40-hex"},
		{"short tree", "main", "git:09", func(p *Predicate) {}, "40-hex"},
		{"uppercase tree", "main", "git:" + strings.Repeat("AB", 20), func(p *Predicate) {}, "40-hex"},
		{"empty intent", "main", "git:" + hex40, func(p *Predicate) { p.Intent = "" }, "empty predicate intent"},
		{"empty world", "main", "git:" + hex40, func(p *Predicate) { p.World = "" }, "empty predicate world"},
		{"empty decision", "main", "git:" + hex40, func(p *Predicate) { p.Decision = "" }, "empty predicate decision"},
		{"empty select decision", "main", "git:" + hex40, func(p *Predicate) { p.SelectDecision = "" }, "empty predicate select_decision"},
		{"empty policy", "main", "git:" + hex40, func(p *Predicate) { p.Policy = "" }, "empty predicate policy"},
		{"empty key id", "main", "git:" + hex40, func(p *Predicate) { p.ProducerKeyID = "" }, "empty predicate producer_key_id"},
		{"empty trunk branch", "main", "git:" + hex40, func(p *Predicate) { p.Trunk.Branch = "" }, "empty predicate trunk branch"},
		{"empty parent commit", "main", "git:" + hex40, func(p *Predicate) { p.Trunk.ParentCommit = "" }, "empty predicate trunk parent_commit"},
		{"empty evidence", "main", "git:" + hex40, func(p *Predicate) { p.Evidence = nil }, "empty predicate evidence"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pred := goldenPredicate()
			tt.mutate(&pred)
			_, err := New(tt.branch, tt.tree, pred)
			if err == nil {
				t.Fatal("New: want error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want substring %q", err, tt.wantSub)
			}
		})
	}
}
