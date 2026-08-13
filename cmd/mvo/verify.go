package main

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/coagente/multiverso/internal/admit"
	"github.com/coagente/multiverso/internal/attest"
	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/signing"
	"github.com/coagente/multiverso/internal/workspace"
)

const schemaVerifyReport = "multiverso.dev/verify-report/v0"

// verifyChecks in evaluation order; the report keys default to false and
// flip true as each check passes.
var verifyChecks = []string{
	"bundle_digest", "signature", "statement", "subject",
	"references", "freshness", "budget",
}

var (
	attestationTrailerRe = regexp.MustCompile(`(?m)^Multiverso-Attestation: (sha256:[0-9a-f]{64})$`)
	bareTreeHexRe        = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type verifyReport struct {
	Schema      string          `json:"schema"`
	Commit      string          `json:"commit"`
	Attestation string          `json:"attestation"`
	KeyID       string          `json:"key_id"`
	Checks      map[string]bool `json:"checks"`
	OK          bool            `json:"ok"`
	Error       string          `json:"error,omitempty"`
}

// cmdVerify checks an admission attestation offline (TP-3 local): the
// trailer is the only thing taken from the commit — everything else is
// verified against the repo's git objects and .multiverso/{cas,ledger.db}
// content, with the trusted key defaulting to the workspace public key.
func cmdVerify(args []string, stdout, stderr io.Writer) error {
	rev, rest := splitDigestArg(args)
	fs := newFlagSet("verify", stderr)
	dir := fs.String("dir", ".", "repository directory")
	keyPath := fs.String("key", "", "trusted public key PEM (default .multiverso/keys/local.pub)")
	jsonOut := fs.Bool("json", false, "emit a machine-readable report")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	extra := fs.Args()
	if rev == "" && len(extra) > 0 {
		rev, extra = extra[0], extra[1:]
	}
	if rev == "" {
		return usagef("verify: commit required")
	}
	if len(extra) > 0 {
		return usagef("verify: unexpected arguments after commit: %s (place flags before the commit)",
			strings.Join(extra, " "))
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	defer ws.Close()

	trusted := *keyPath
	if trusted == "" {
		trusted = filepath.Join(ws.KeysDir(), signing.PubName)
	}
	pub, _, err := signing.LoadPublicKeyFile(trusted)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}

	report := verifyReport{Schema: schemaVerifyReport, Checks: make(map[string]bool, len(verifyChecks))}
	for _, c := range verifyChecks {
		report.Checks[c] = false
	}
	checkErr := runVerify(ws, *dir, rev, pub, &report)
	report.OK = checkErr == nil
	if checkErr != nil {
		report.Error = checkErr.Error()
	}

	// The report is emitted even on failure, before exit 1.
	if *jsonOut {
		if err := emitJSON(stdout, report); err != nil {
			return fmt.Errorf("verify: %w", err)
		}
	} else if checkErr == nil {
		fmt.Fprintf(stdout, "commit:      %s\n", report.Commit)
		fmt.Fprintf(stdout, "attestation: %s\n", report.Attestation)
		fmt.Fprintf(stdout, "key:         %s\n", report.KeyID)
		fmt.Fprintf(stdout, "OK: attestation verified (%d checks)\n", len(verifyChecks))
	}
	if checkErr != nil {
		return fmt.Errorf("verify: %w", checkErr)
	}
	return nil
}

// runVerify runs the seven checks in order, fail-fast; the returned error
// is "<check>: <detail>". It fills report.Commit/Attestation/KeyID as they
// become known and flips each report check to true when it passes.
func runVerify(ws *workspace.Workspace, repo, rev string, pub ed25519.PublicKey, report *verifyReport) error {
	fail := func(check, format string, a ...any) error {
		return fmt.Errorf("%s: %s", check, fmt.Sprintf(format, a...))
	}

	// 1. bundle_digest — the trailer names a bundle whose bytes hash to
	// its CAS key (content-address integrity).
	commit, err := gitx.ResolveCommit(repo, rev)
	if err != nil {
		return fail("bundle_digest", "resolve %q: %v", rev, err)
	}
	report.Commit = commit
	msg, err := gitx.CommitMessage(repo, commit)
	if err != nil {
		return fail("bundle_digest", "%v", err)
	}
	trailers := attestationTrailerRe.FindAllStringSubmatch(msg, -1)
	if len(trailers) == 0 {
		return fail("bundle_digest", "commit %s has no Multiverso-Attestation trailer", commit)
	}
	bundleKey := trailers[len(trailers)-1][1]
	report.Attestation = bundleKey
	bundle, err := ws.CAS.Get(bundleKey) // Get re-hashes: a flipped byte fails here
	if err != nil {
		return fail("bundle_digest", "%v", err)
	}
	report.Checks["bundle_digest"] = true

	// 2. signature.
	var env signing.Envelope
	if err := json.Unmarshal(bundle, &env); err != nil {
		return fail("signature", "decode envelope: %v", err)
	}
	if env.PayloadType != signing.PayloadTypeInToto {
		return fail("signature", "payloadType %q, want %q", env.PayloadType, signing.PayloadTypeInToto)
	}
	payload, err := signing.Verify(env, pub)
	if err != nil {
		return fail("signature", "%v", err)
	}
	report.KeyID = signing.KeyID(pub)
	report.Checks["signature"] = true

	// 3. statement.
	var stmt attest.Statement
	if err := json.Unmarshal(payload, &stmt); err != nil {
		return fail("statement", "decode statement: %v", err)
	}
	if stmt.Type != attest.StatementType {
		return fail("statement", "_type %q, want %q", stmt.Type, attest.StatementType)
	}
	if stmt.PredicateType != attest.PredicateType {
		return fail("statement", "predicateType %q, want %q", stmt.PredicateType, attest.PredicateType)
	}
	if len(stmt.Subject) != 1 {
		return fail("statement", "%d subjects, want exactly one", len(stmt.Subject))
	}
	subj := stmt.Subject[0]
	if len(subj.Digest) != 1 || !bareTreeHexRe.MatchString(subj.Digest["gitTree"]) {
		return fail("statement", "subject digest %v is not exactly {gitTree: <40-hex>}", subj.Digest)
	}
	report.Checks["statement"] = true
	pred := stmt.Predicate

	// 4. subject — tree + parent bind the commit's full content and
	// history position (design decision 1).
	commitTree, err := gitx.TreeOf(repo, commit)
	if err != nil {
		return fail("subject", "%v", err)
	}
	if got := gitx.TreePrefix + subj.Digest["gitTree"]; got != commitTree {
		return fail("subject", "subject tree %s, commit tree %s", got, commitTree)
	}
	if want := "refs/heads/" + pred.Trunk.Branch; subj.Name != want {
		return fail("subject", "subject name %q, want %q", subj.Name, want)
	}
	parent, err := gitx.ParentOf(repo, commit)
	if err != nil {
		return fail("subject", "%v", err)
	}
	if pred.Trunk.ParentCommit != parent {
		return fail("subject", "predicate parent_commit %s, commit parent %s", pred.Trunk.ParentCommit, parent)
	}
	report.Checks["subject"] = true

	// 5. references — everything the predicate names exists in CAS, was
	// recorded in the ledger, and is internally consistent.
	refs := append([]string{pred.Intent, pred.World, pred.Policy, pred.Decision, pred.SelectDecision},
		pred.Evidence...)
	for _, dig := range refs {
		key, err := object.CASKey(dig)
		if err != nil {
			return fail("references", "%v", err)
		}
		if !ws.CAS.Has(key) {
			return fail("references", "%s not in CAS", dig)
		}
	}
	recorded := map[string]map[string]bool{
		evIntentCreated:    {},
		evWorldCreated:     {},
		evDecisionRecorded: {},
		evReceiptRecorded:  {},
	}
	if err := ws.Ledger.Scan(func(e ledger.Event) error {
		if m, ok := recorded[e.Type]; ok {
			m[e.PayloadDig] = true
		}
		return nil
	}); err != nil {
		return fail("references", "%v", err)
	}
	ledgerWants := []struct{ typ, dig string }{
		{evIntentCreated, pred.Intent},
		{evWorldCreated, pred.World},
		{evDecisionRecorded, pred.Decision},
		{evDecisionRecorded, pred.SelectDecision},
	}
	for _, ev := range pred.Evidence {
		ledgerWants = append(ledgerWants, struct{ typ, dig string }{evReceiptRecorded, ev})
	}
	for _, w := range ledgerWants {
		if !recorded[w.typ][w.dig] {
			return fail("references", "no %s event for %s in ledger", w.typ, w.dig)
		}
	}
	var admDec object.Decision
	if err := loadFromCAS(ws, pred.Decision, &admDec); err != nil {
		return fail("references", "%v", err)
	}
	switch {
	case admDec.Type != admit.TypeAdmit:
		return fail("references", "decision %s has type %q, want ADMIT", pred.Decision, admDec.Type)
	case admDec.Intent != pred.Intent:
		return fail("references", "ADMIT decision intent %s, predicate intent %s", admDec.Intent, pred.Intent)
	case len(admDec.Subject) == 0 || admDec.Subject[0] != pred.World:
		return fail("references", "ADMIT decision subject %v, predicate world %s", admDec.Subject, pred.World)
	case admDec.Policy != pred.Policy:
		return fail("references", "ADMIT decision policy %s, predicate policy %s", admDec.Policy, pred.Policy)
	case !slices.Equal(admDec.Evidence, pred.Evidence):
		return fail("references", "ADMIT decision evidence %v, predicate evidence %v", admDec.Evidence, pred.Evidence)
	}
	var selDec object.Decision
	if err := loadFromCAS(ws, pred.SelectDecision, &selDec); err != nil {
		return fail("references", "%v", err)
	}
	switch {
	case selDec.Type != "SELECT":
		return fail("references", "decision %s has type %q, want SELECT", pred.SelectDecision, selDec.Type)
	case selDec.Intent != pred.Intent:
		return fail("references", "SELECT decision intent %s, predicate intent %s", selDec.Intent, pred.Intent)
	case len(selDec.Subject) == 0 || selDec.Subject[0] != pred.World:
		return fail("references", "SELECT decision subject %v, predicate world %s", selDec.Subject, pred.World)
	}
	if want := signing.KeyID(pub); pred.ProducerKeyID != want {
		return fail("references", "predicate producer_key_id %s, trusted key %s", pred.ProducerKeyID, want)
	}
	report.Checks["references"] = true

	// 6. freshness (EP-3) — the landing gate judged exactly the admitted
	// tree, and the apply receipt pinned the parent's tree.
	parentTree, err := gitx.TreeOf(repo, parent)
	if err != nil {
		return fail("freshness", "%v", err)
	}
	receipts := make([]object.Receipt, 0, len(pred.Evidence))
	for _, dig := range pred.Evidence {
		var rec object.Receipt
		if err := loadFromCAS(ws, dig, &rec); err != nil {
			return fail("freshness", "%v", err)
		}
		if rec.World != pred.World {
			return fail("freshness", "receipt %s has world %s, predicate world %s", dig, rec.World, pred.World)
		}
		switch rec.Family {
		case "suite":
			if rec.Freshness.ValidFor.Tree != commitTree {
				return fail("freshness", "suite receipt %s valid for tree %s, commit tree %s",
					dig, rec.Freshness.ValidFor.Tree, commitTree)
			}
		case admit.FamilyLandingApply:
			if rec.Freshness.ValidFor.Tree != parentTree {
				return fail("freshness", "landing-apply receipt %s valid for tree %s, parent tree %s",
					dig, rec.Freshness.ValidFor.Tree, parentTree)
			}
		}
		receipts = append(receipts, rec)
	}
	report.Checks["freshness"] = true

	// 7. budget.
	var sum int64
	for _, rec := range receipts {
		sum += rec.Cost.WallMS
	}
	if pred.BudgetConsumed.WallMS != sum {
		return fail("budget", "predicate wall_ms %d, receipts sum to %d", pred.BudgetConsumed.WallMS, sum)
	}
	report.Checks["budget"] = true
	return nil
}

// loadFromCAS fetches an object's canonical bytes by "mv0:" digest and
// decodes them into v.
func loadFromCAS(ws *workspace.Workspace, dig string, v any) error {
	b, err := ws.GetObject(dig)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode %s: %w", dig, err)
	}
	return nil
}
