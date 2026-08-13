// Package attest builds in-toto Statement v1 attestations with the
// Multiverso admission predicate (PRD §5.4, G3). The statement's canonical
// bytes (object.Canonical) are the DSSE payload; object.DigestBytes over
// them is the statement digest recorded in the ledger.
package attest

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/gitx"
)

const (
	// StatementType is the in-toto Statement v1 type URI.
	StatementType = "https://in-toto.io/Statement/v1"
	// PredicateType is the Multiverso admission predicate type URI.
	PredicateType = "multiverso.dev/admission/v0"
)

// Statement is an in-toto Statement v1 carrying the admission predicate.
type Statement struct {
	Type          string    `json:"_type"` // StatementType
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"` // PredicateType
	Predicate     Predicate `json:"predicate"`
}

// Subject names what the attestation is about: the landing tree of the
// admitted commit on the trunk branch. The subject binds the landing tree
// and (via the predicate) the parent commit, not the enclosing commit —
// the trailer lives inside the commit message, so the commit sha cannot
// appear in its own attestation (design decision 1).
type Subject struct {
	Name   string            `json:"name"`   // "refs/heads/<branch>"
	Digest map[string]string `json:"digest"` // {"gitTree": "<40-hex, no prefix>"}
}

// Predicate is the multiverso.dev/admission/v0 predicate.
type Predicate struct {
	Intent         string   `json:"intent"`          // intent digest ("mv0:…")
	World          string   `json:"world"`           // winning world digest
	Decision       string   `json:"decision"`        // ADMIT decision digest
	SelectDecision string   `json:"select_decision"` // SELECT decision digest (race closure link)
	Evidence       []string `json:"evidence"`        // admission receipt digests, sorted
	Policy         string   `json:"policy"`          // policy digest
	BudgetConsumed Budget   `json:"budget_consumed"`
	ProducerKeyID  string   `json:"producer_key_id"` // signer key ID ("mv0:…")
	Trunk          Trunk    `json:"trunk"`
}

// Budget is the admission budget actually consumed.
type Budget struct {
	WallMS int64 `json:"wall_ms"` // Σ Cost.WallMS over Evidence receipts
}

// Trunk pins where the admission landed.
type Trunk struct {
	Branch       string `json:"branch"`        // e.g. "main"
	ParentCommit string `json:"parent_commit"` // trunk head the admission landed on (bare sha)
}

var treeHexRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// New validates required fields, sorts a copy of pred.Evidence, and builds
// the single-subject statement. landingTree arrives "git:"-prefixed
// (internal convention) and is stripped here: in-toto DigestSet values are
// bare lowercase hex. This is the only prefix boundary.
func New(branch, landingTree string, pred Predicate) (Statement, error) {
	required := []struct{ name, value string }{
		{"branch", branch},
		{"landing tree", landingTree},
		{"predicate intent", pred.Intent},
		{"predicate world", pred.World},
		{"predicate decision", pred.Decision},
		{"predicate select_decision", pred.SelectDecision},
		{"predicate policy", pred.Policy},
		{"predicate producer_key_id", pred.ProducerKeyID},
		{"predicate trunk branch", pred.Trunk.Branch},
		{"predicate trunk parent_commit", pred.Trunk.ParentCommit},
	}
	for _, r := range required {
		if r.value == "" {
			return Statement{}, fmt.Errorf("attest: empty %s", r.name)
		}
	}
	if len(pred.Evidence) == 0 {
		return Statement{}, fmt.Errorf("attest: empty predicate evidence")
	}
	hexTree, ok := strings.CutPrefix(landingTree, gitx.TreePrefix)
	if !ok || !treeHexRe.MatchString(hexTree) {
		return Statement{}, fmt.Errorf("attest: landing tree %q is not a %q-prefixed 40-hex digest",
			landingTree, gitx.TreePrefix)
	}

	p := pred
	p.Evidence = append([]string(nil), pred.Evidence...)
	sort.Strings(p.Evidence)

	return Statement{
		Type: StatementType,
		Subject: []Subject{{
			Name:   "refs/heads/" + branch,
			Digest: map[string]string{"gitTree": hexTree},
		}},
		PredicateType: PredicateType,
		Predicate:     p,
	}, nil
}
