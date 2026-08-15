package publish

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/coagente/multiverso/internal/ledger"
)

// casCensus lists every file under the fixture's CAS root.
func (f *fixture) casCensus() []string {
	f.t.Helper()
	// The CAS root is the sibling "cas" dir of the ledger; walk it.
	var files []string
	root := f.casRoot
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func (f *fixture) ledgerEvents() []string {
	f.t.Helper()
	var types []string
	if err := f.led.Scan(func(e ledger.Event) error {
		types = append(types, e.Type)
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
	return types
}

func (f *fixture) prune(cfg PruneConfig) *PruneResult {
	f.t.Helper()
	cfg.Repo, cfg.Ledger, cfg.Intent = f.repo, f.led, f.intentDig
	res, err := Prune(cfg)
	if err != nil {
		f.t.Fatalf("Prune: %v", err)
	}
	return res
}

func TestPruneNonAdmittedWipes(t *testing.T) {
	f := newFixture(t)
	f.newIntent("prune non-admitted")
	f.race(true, false)
	f.mustPublish(true)
	short := f.short()

	res := f.prune(PruneConfig{Remote: "origin", KeepAdmitted: true})
	wantGone := []string{CandRef(short, 1), CandRef(short, 2), EvidenceRef(short)}
	if !reflect.DeepEqual(res.DeletedLocal, wantGone) || !reflect.DeepEqual(res.DeletedRemote, wantGone) {
		t.Errorf("deleted = %+v, want %v on both sides", res, wantGone)
	}
	if len(res.Kept) != 0 {
		t.Errorf("kept = %v, want none (nothing landed)", res.Kept)
	}
	local, remote := f.namespaceState()
	if len(local) != 0 || len(remote) != 0 {
		t.Errorf("namespace survives: local %v remote %v", local, remote)
	}
}

func TestPruneAdmittedKeepsWinnerAndEvidence(t *testing.T) {
	f := newFixture(t)
	f.newIntent("prune admitted")
	f.race(true, false)
	f.admitIntent()
	f.mustPublish(true)
	short := f.short()

	res := f.prune(PruneConfig{Remote: "origin", KeepAdmitted: true})
	wantGone := []string{CandRef(short, 2)}
	wantKept := []string{CandRef(short, 1), EvidenceRef(short)}
	if !reflect.DeepEqual(res.DeletedLocal, wantGone) || !reflect.DeepEqual(res.DeletedRemote, wantGone) {
		t.Errorf("deleted = %+v, want %v", res, wantGone)
	}
	if !reflect.DeepEqual(res.Kept, wantKept) {
		t.Errorf("kept = %v, want %v", res.Kept, wantKept)
	}
	local, remote := f.namespaceState()
	for _, refs := range []map[string]string{local, remote} {
		if len(refs) != 2 {
			t.Errorf("namespace = %v, want winner + evidence", refs)
		}
		if _, ok := refs[CandRef(short, 2)]; ok {
			t.Errorf("loser cand/2 survived: %v", refs)
		}
	}
}

func TestPruneKeepAdmittedFalseWipes(t *testing.T) {
	f := newFixture(t)
	f.newIntent("prune keep-admitted=false")
	f.race(true, false)
	f.admitIntent()
	f.mustPublish(true)

	res := f.prune(PruneConfig{Remote: "origin", KeepAdmitted: false})
	if len(res.DeletedLocal) != 3 || len(res.DeletedRemote) != 3 || len(res.Kept) != 0 {
		t.Errorf("wipe = %+v", res)
	}
	local, remote := f.namespaceState()
	if len(local) != 0 || len(remote) != 0 {
		t.Errorf("namespace survives the wipe: local %v remote %v", local, remote)
	}
}

func TestPruneLocalOnly(t *testing.T) {
	f := newFixture(t)
	f.newIntent("prune local-only")
	f.race(true, false)
	f.mustPublish(true)

	res := f.prune(PruneConfig{Remote: "", KeepAdmitted: true})
	if len(res.DeletedLocal) != 3 || len(res.DeletedRemote) != 0 {
		t.Errorf("local-only prune = %+v", res)
	}
	local, remote := f.namespaceState()
	if len(local) != 0 {
		t.Errorf("local namespace survives: %v", local)
	}
	if len(remote) != 3 {
		t.Errorf("remote was touched without --remote: %v", remote)
	}
}

func TestPruneOlderThanGuard(t *testing.T) {
	t.Run("never published", func(t *testing.T) {
		f := newFixture(t)
		f.newIntent("never published")
		f.race(true)
		cfg := PruneConfig{Repo: f.repo, Ledger: f.led, Intent: f.intentDig,
			OlderThan: time.Hour, KeepAdmitted: true}
		if _, err := Prune(cfg); err == nil || !strings.Contains(err.Error(), "never published") {
			t.Errorf("prune of a never-published intent = %v", err)
		}
	})
	t.Run("young publication refused", func(t *testing.T) {
		f := newFixture(t)
		f.newIntent("young publication")
		f.race(true)
		f.mustPublish(false)
		cfg := PruneConfig{Repo: f.repo, Ledger: f.led, Intent: f.intentDig,
			OlderThan: time.Hour, KeepAdmitted: true}
		if _, err := Prune(cfg); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("prune of a young publication = %v", err)
		}
		local, _ := f.namespaceState()
		if len(local) == 0 {
			t.Errorf("refused prune still deleted refs")
		}
	})
	t.Run("old enough proceeds", func(t *testing.T) {
		f := newFixture(t)
		f.newIntent("old publication")
		f.race(true)
		f.mustPublish(false)
		// Ledger timestamps truncate to seconds, so any positive elapsed
		// time exceeds a nanosecond bound.
		res := f.prune(PruneConfig{OlderThan: time.Nanosecond, KeepAdmitted: true})
		if len(res.DeletedLocal) == 0 {
			t.Errorf("aged prune deleted nothing: %+v", res)
		}
	})
}

// The local namespace refs are the world trees' GC pins (M1d decision 11),
// so a remote-leg failure must not find them already deleted — and the
// ledger must not be left claiming a retention action that half-happened.
func TestPruneRemoteFailureLeavesLocalIntact(t *testing.T) {
	f := newFixture(t)
	f.newIntent("prune remote failure")
	f.race(true, false)
	f.mustPublish(true)

	before, _ := f.namespaceState()
	eventsBefore := f.ledgerEvents()
	cfg := PruneConfig{Repo: f.repo, Ledger: f.led, Intent: f.intentDig,
		Remote: "nosuchremote", KeepAdmitted: true}
	if _, err := Prune(cfg); err == nil || !strings.Contains(err.Error(), "nosuchremote") {
		t.Fatalf("prune against a missing remote = %v", err)
	}
	after, remote := f.namespaceState()
	if !reflect.DeepEqual(before, after) {
		t.Errorf("failed prune deleted local refs:\nbefore %v\nafter  %v", before, after)
	}
	if len(remote) != 3 {
		t.Errorf("remote namespace = %v, want untouched", remote)
	}
	if got := f.ledgerEvents(); len(got) != len(eventsBefore) {
		t.Errorf("failed prune recorded %v", got[len(eventsBefore):])
	}
}

// Local refs are already gone by the time the remote leg runs, so a failed
// remote delete must still leave the retention action's audit trail — an
// unrecorded deletion is a ledger that will forever claim it deleted
// nothing locally.
func TestPruneRemoteDeleteFailureStillRecords(t *testing.T) {
	f := newFixture(t)
	f.newIntent("prune push failure")
	f.race(true, false)
	f.mustPublish(true)
	short := f.short()

	// A concurrent actor moves a namespace ref between the survey and the
	// bulk delete: the lease goes stale and the atomic push deletes nothing.
	hookBeforePush = func() {
		gitT(f.t, f.origin, "update-ref", CandRef(short, 2), f.intent.Base.Commit)
	}
	defer func() { hookBeforePush = nil }()

	cfg := PruneConfig{Repo: f.repo, Ledger: f.led, Intent: f.intentDig,
		Remote: "origin", KeepAdmitted: true}
	if _, err := Prune(cfg); err == nil {
		t.Fatal("prune with a stale lease succeeded")
	}
	local, remote := f.namespaceState()
	if len(local) != 0 {
		t.Errorf("local namespace = %v, want deleted", local)
	}
	if len(remote) != 3 {
		t.Errorf("remote namespace = %v, want all 3 refs (the atomic delete failed)", remote)
	}

	var body map[string]any
	if err := f.led.Scan(func(e ledger.Event) error {
		if e.Type != "prune.executed" {
			return nil
		}
		return json.Unmarshal(e.Payload, &body)
	}); err != nil {
		t.Fatal(err)
	}
	if body == nil {
		t.Fatal("failed prune recorded no prune.executed event")
	}
	if deleted, _ := body["deleted_local"].([]any); len(deleted) != 3 {
		t.Errorf("deleted_local = %v, want the 3 refs the prune really deleted", body["deleted_local"])
	}
	if deleted, _ := body["deleted_remote"].([]any); len(deleted) != 0 {
		t.Errorf("deleted_remote = %v, want empty (nothing landed)", body["deleted_remote"])
	}
}

func TestPruneLeavesOutOfNamespaceRefsAlone(t *testing.T) {
	f := newFixture(t)
	f.newIntent("prune namespace hygiene")
	f.race(true, false)
	f.admitIntent()
	f.mustPublish(true)
	strays := f.seedOutOfNamespace()

	res := f.prune(PruneConfig{Remote: "origin", KeepAdmitted: true})
	if !reflect.DeepEqual(res.DeletedRemote, []string{CandRef(f.short(), 2)}) {
		t.Errorf("DeletedRemote = %v, want only the loser cand/2", res.DeletedRemote)
	}
	f.survives(strays)
}

func TestPruneNeverTouchesCASOrLedger(t *testing.T) {
	f := newFixture(t)
	f.newIntent("cas untouched")
	f.race(true, false)
	f.admitIntent()
	f.mustPublish(true)

	casBefore := f.casCensus()
	eventsBefore := f.ledgerEvents()
	f.prune(PruneConfig{Remote: "origin", KeepAdmitted: false})
	casAfter := f.casCensus()
	eventsAfter := f.ledgerEvents()

	if !reflect.DeepEqual(casBefore, casAfter) {
		t.Errorf("prune changed the CAS file census:\nbefore %d files\nafter %d files",
			len(casBefore), len(casAfter))
	}
	if len(eventsAfter) != len(eventsBefore)+1 || eventsAfter[len(eventsAfter)-1] != "prune.executed" {
		t.Errorf("ledger gained %v, want exactly one prune.executed",
			eventsAfter[len(eventsBefore):])
	}
	if err := f.led.VerifyChain(); err != nil {
		t.Errorf("chain broken after prune: %v", err)
	}
}

func TestPruneExecutedGolden(t *testing.T) {
	f := newFixture(t)
	f.newIntent("prune golden")
	f.race(true, false)
	f.admitIntent()
	f.mustPublish(true)
	short := f.short()
	f.prune(PruneConfig{Remote: "origin", OlderThan: time.Nanosecond, KeepAdmitted: true})

	var payload string
	if err := f.led.Scan(func(e ledger.Event) error {
		if e.Type == "prune.executed" {
			payload = string(e.Payload)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(
		`{"deleted_local":["%[1]s"],"deleted_remote":["%[1]s"],"intent":"%[2]s","keep_admitted":true,"kept":["%[3]s","%[4]s"],"older_than_ms":0,"remote":"origin"}`,
		CandRef(short, 2), f.intentDig, CandRef(short, 1), EvidenceRef(short))
	if payload != want {
		t.Errorf("prune.executed:\n got %s\nwant %s", payload, want)
	}
	// The payload is canonical JSON (audit hash chain covers it).
	var body map[string]any
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatal(err)
	}
}
