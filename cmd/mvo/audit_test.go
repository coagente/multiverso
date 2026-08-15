package main

// M1f-a: `mvo audit`'s output must not contradict its exit code.

import (
	"encoding/json"
	"strings"
	"testing"
)

// --require-decisions is the one knob decision 23 added for CI, and CI
// reads stdout. The OK line used to print first and the failure to be
// returned afterwards, so a log-scraping check keyed on `OK:` read a
// failed audit as a pass — the block's own rule ("a skipped check must
// never render identically to a passed one") violated on the flag whose
// entire purpose is to be scraped.
func TestAuditRequireDecisionsNeverPrintsOK(t *testing.T) {
	repo := guardRepo(t) // an initialized workspace with no races

	stdout, stderr, code := mvo(t, "audit", "--require-decisions", "1", "--dir", repo)
	if code == exitOK {
		t.Fatalf("audit exited 0 with zero decisions and --require-decisions 1\nstdout: %s", stdout)
	}
	if strings.Contains(stdout, "OK:") {
		t.Errorf("stdout carries an OK line on a FAILED audit:\n%s", stdout)
	}
	if !strings.Contains(stdout, "SHORTFALL: 0 decisions replayed, --require-decisions 1") {
		t.Errorf("stdout does not name the shortfall:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--require-decisions 1") {
		t.Errorf("stderr does not name the flag that failed:\n%s", stderr)
	}

	// The satisfiable case still says OK, so the guard did not simply
	// break the verb.
	stdout, _, code = mvo(t, "audit", "--require-decisions", "0", "--dir", repo)
	if code != exitOK || !strings.Contains(stdout, "OK:") {
		t.Errorf("audit --require-decisions 0 = %d\n%s", code, stdout)
	}
}

// The trust anchor the study named and the CLI section specified. Without
// --key, audit can only check the workspace's signatures against the
// workspace's OWN key — a self-check a rogue clone reproduces — and the
// output has to say which of the two happened.
func TestAuditKeyFlagExistsAndNamesTheAnchor(t *testing.T) {
	repo := guardRepo(t)

	stdout, stderr, code := mvo(t, "audit", "--key", "no-such-key.pub", "--dir", repo)
	if code == exitOK {
		t.Fatalf("audit accepted an unreadable trust anchor\nstdout: %s", stdout)
	}
	if strings.Contains(stderr, "not defined") {
		t.Fatalf("--key is not implemented: %s", stderr)
	}

	stdout, _, code = mvo(t, "audit", "--json", "--dir", repo)
	if code != exitOK {
		t.Fatalf("audit --json exit %d: %s", code, stdout)
	}
	var rep struct {
		Schema  string `json:"schema"`
		Against string `json:"attestations_verified_against"`
		Checked int    `json:"attestations_checked"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &rep); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout)
	}
	if rep.Schema != schemaAuditReport {
		t.Errorf("schema = %q, want %q", rep.Schema, schemaAuditReport)
	}
	// No attestations in this workspace, so no anchor is claimed: an empty
	// string is the honest record, not a name nothing was checked against.
	if rep.Checked != 0 || rep.Against != "" {
		t.Errorf("attestations_checked=%d verified_against=%q, want 0 and \"\"", rep.Checked, rep.Against)
	}
}
