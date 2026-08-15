package publish

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
)

// namespaceState surveys local and remote namespace refs for one short.
func (f *fixture) namespaceState() (local, remote map[string]string) {
	f.t.Helper()
	local, err := gitx.ForEachRef(f.repo, Namespace(f.short()))
	if err != nil {
		f.t.Fatal(err)
	}
	return local, f.remoteRefs()
}

func TestPublishFirstAndIdempotent(t *testing.T) {
	f := newFixture(t)
	f.newIntent("first publish")
	f.race(true, false)
	short := f.short()

	res := f.mustPublish(false)
	if res.Short != short {
		t.Errorf("Short = %q, want %q", res.Short, short)
	}
	if len(res.Pushed) != 2 || len(res.UpToDate) != 0 || len(res.Removed) != 0 {
		t.Fatalf("first publish = %+v", res)
	}
	wantRefs := []string{CandRef(short, 1), EvidenceRef(short)}
	for i, rt := range res.Pushed {
		if rt.Ref != wantRefs[i] {
			t.Errorf("pushed[%d] = %q, want %q", i, rt.Ref, wantRefs[i])
		}
	}
	local, remote := f.namespaceState()
	if len(local) != 2 || len(remote) != 2 {
		t.Fatalf("local = %v, remote = %v", local, remote)
	}
	if !reflect.DeepEqual(local, remote) {
		t.Errorf("local and remote namespaces differ:\n%v\n%v", local, remote)
	}

	// Idempotent republish: identical content re-mints identical shas, the
	// plan diffs to zero, the push is skipped.
	res2 := f.mustPublish(false)
	if len(res2.Pushed) != 0 || len(res2.Removed) != 0 || len(res2.UpToDate) != 2 {
		t.Fatalf("republish = %+v", res2)
	}
	_, remote2 := f.namespaceState()
	if !reflect.DeepEqual(remote, remote2) {
		t.Errorf("remote changed across the no-op republish:\n%v\n%v", remote, remote2)
	}
}

func TestPublishIncludeRejectedDelta(t *testing.T) {
	f := newFixture(t)
	f.newIntent("include rejected")
	f.race(true, false)
	f.mustPublish(false)
	res := f.mustPublish(true)
	if len(res.Pushed) != 1 || res.Pushed[0].Ref != CandRef(f.short(), 2) {
		t.Fatalf("include-rejected delta = %+v, want just cand/2", res)
	}
	if len(res.UpToDate) != 2 || len(res.Removed) != 0 {
		t.Errorf("delta publish = %+v", res)
	}
	_, remote := f.namespaceState()
	if len(remote) != 3 {
		t.Errorf("remote = %v, want 3 refs", remote)
	}
}

func TestPublishReconcilesSuperseded(t *testing.T) {
	f := newFixture(t)
	f.newIntent("re-race reconcile")
	f.race(true, false)
	f.mustPublish(true) // cand/1, cand/2, evidence
	f.race(true)        // superseding single-candidate race, new content
	res := f.mustPublish(true)

	short := f.short()
	if !reflect.DeepEqual(res.Removed, []string{CandRef(short, 2)}) {
		t.Errorf("Removed = %v, want [%s]", res.Removed, CandRef(short, 2))
	}
	// cand/1 and evidence changed content → both pushed.
	if len(res.Pushed) != 2 {
		t.Errorf("Pushed = %+v, want cand/1 + evidence", res.Pushed)
	}
	local, remote := f.namespaceState()
	if len(local) != 2 || len(remote) != 2 {
		t.Errorf("after reconcile: local %v remote %v, want 2 refs each", local, remote)
	}
	if _, ok := remote[CandRef(short, 2)]; ok {
		t.Errorf("superseded cand/2 survived on the remote")
	}
	if _, ok := local[CandRef(short, 2)]; ok {
		t.Errorf("superseded cand/2 survived locally")
	}
}

func TestPublishNeverTouchesHeads(t *testing.T) {
	f := newFixture(t)
	f.newIntent("heads untouched")
	f.race(true, false)
	f.mustPublish(true)
	heads, err := gitx.LsRemote(f.repo, "origin", "refs/heads/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) != 0 {
		t.Errorf("publish pushed under refs/heads: %v", heads)
	}
}

// seedOutOfNamespace puts a branch and a tag on the remote whose NAMES
// contain the intent namespace path. They are not in the namespace, so
// reconciliation must never see — let alone delete — them.
func (f *fixture) seedOutOfNamespace() []string {
	f.t.Helper()
	head := gitT(f.t, f.repo, "rev-parse", "HEAD")
	refs := []string{
		"refs/heads/" + Namespace(f.short()) + "/wip",
		"refs/tags/release/" + Namespace(f.short()) + "/v1",
	}
	for _, ref := range refs {
		gitT(f.t, f.repo, "push", "-q", "origin", head+":"+ref)
	}
	return refs
}

// survives fails the test unless every ref still resolves on the remote.
func (f *fixture) survives(refs []string) {
	f.t.Helper()
	for _, ref := range refs {
		if out := gitT(f.t, f.repo, "ls-remote", "origin", ref); !strings.Contains(out, ref) {
			f.t.Errorf("out-of-namespace ref %s was deleted from the remote", ref)
		}
	}
}

func TestPublishLeavesOutOfNamespaceRefsAlone(t *testing.T) {
	f := newFixture(t)
	f.newIntent("namespace hygiene")
	f.race(true, false)
	f.mustPublish(true)
	strays := f.seedOutOfNamespace()

	// A republish reconciles the namespace against the remote survey. Only
	// refs actually under refs/multiverso/intent/<short>/ are reconcilable.
	res := f.mustPublish(true)
	if len(res.Removed) != 0 {
		t.Errorf("reconciliation removed %v, want nothing", res.Removed)
	}
	f.survives(strays)

	// And the same holds when the plan legitimately reconciles something:
	// the superseded cand/2 goes, the look-alike refs stay.
	f.race(true)
	res = f.mustPublish(true)
	if !reflect.DeepEqual(res.Removed, []string{CandRef(f.short(), 2)}) {
		t.Errorf("Removed = %v, want only the superseded cand/2", res.Removed)
	}
	f.survives(strays)
}

// The push is atomic, so a multi-refspec batch with one stale lease lands
// nothing — which is exactly what publish.finished claims when it records
// empty pushed/removed arrays.
func TestPublishFailedBatchLandsNothing(t *testing.T) {
	f := newFixture(t)
	f.newIntent("atomic batch")
	f.race(true, false)
	f.mustPublish(true) // cand/1, cand/2, evidence
	f.race(true)        // supersede: cand/1 + evidence change, cand/2 is removed

	_, before := f.namespaceState()
	evRef := EvidenceRef(f.short())
	hookBeforePush = func() {
		gitT(f.t, f.origin, "update-ref", evRef, f.intent.Base.Commit)
	}
	defer func() { hookBeforePush = nil }()

	if _, err := Run(f.publishCfg(true)); err == nil {
		t.Fatal("publish with a moved lease succeeded")
	}
	_, after := f.namespaceState()
	for ref, sha := range before {
		if ref == evRef {
			continue // the concurrent publisher moved this one, not us
		}
		if after[ref] != sha {
			t.Errorf("ref %s moved to %q despite the failed batch (was %q)", ref, after[ref], sha)
		}
	}
	if _, ok := after[CandRef(f.short(), 2)]; !ok {
		t.Errorf("the failed batch still applied its delete refspec: %v", after)
	}

	// The ledger's claim matches the remote: nothing pushed, nothing removed.
	var last map[string]any
	if err := f.led.Scan(func(e ledger.Event) error {
		if e.Type != "publish.finished" {
			return nil
		}
		var body map[string]any
		if err := json.Unmarshal(e.Payload, &body); err != nil {
			return err
		}
		last = body
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if pushed, _ := last["pushed"].([]any); len(pushed) != 0 {
		t.Errorf("publish.finished claims pushes: %v", last)
	}
	if removed, _ := last["removed"].([]any); len(removed) != 0 {
		t.Errorf("publish.finished claims removals: %v", last)
	}
	if last["error"] == "" {
		t.Errorf("failed publish recorded no error: %v", last)
	}
}

func TestPublishLeaseFailureSurfaces(t *testing.T) {
	f := newFixture(t)
	f.newIntent("lease failure")
	f.race(true, false)
	res := f.mustPublish(false)
	short := f.short()
	evRef := EvidenceRef(short)
	var candTip string
	for _, rt := range res.Pushed {
		if rt.Ref != evRef {
			candTip = rt.Tip
		}
	}

	// A "concurrent publisher" scenario: the remote evidence ref is moved
	// before this publisher surveys (so the plan wants to heal it), then
	// moved again — to a third commit, the intent base — between the
	// survey and the push. The lease (taken at survey time) no longer
	// matches, so the push must fail loudly, never silently clobber.
	gitT(t, f.origin, "update-ref", evRef, candTip)
	hookBeforePush = func() {
		gitT(f.t, f.origin, "update-ref", evRef, f.intent.Base.Commit)
	}
	defer func() { hookBeforePush = nil }()

	_, err := Run(f.publishCfg(false))
	if err == nil {
		t.Fatal("publish with a moved lease succeeded")
	}
	if !strings.Contains(err.Error(), "re-run `mvo publish`") {
		t.Errorf("error %q does not name the concurrent-publisher remedy", err)
	}
	// publish.finished carries the error; pushed/removed honestly empty.
	var finishes []map[string]any
	if scanErr := f.led.Scan(func(e ledger.Event) error {
		if e.Type == "publish.finished" {
			var body map[string]any
			if err := json.Unmarshal(e.Payload, &body); err != nil {
				return err
			}
			finishes = append(finishes, body)
		}
		return nil
	}); scanErr != nil {
		t.Fatal(scanErr)
	}
	if len(finishes) != 2 {
		t.Fatalf("publish.finished events = %d, want 2", len(finishes))
	}
	last := finishes[1]
	if last["error"] == "" {
		t.Errorf("failed publish recorded no error: %v", last)
	}
	if pushed, ok := last["pushed"].([]any); !ok || len(pushed) != 0 {
		t.Errorf("failed publish claims pushes: %v", last)
	}
}

func TestPublishEventGoldens(t *testing.T) {
	f := newFixture(t)
	f.newIntent("event goldens")
	f.race(true, false)
	res := f.mustPublish(false)

	refsJSON := func(rts []RefTip) string {
		parts := make([]string, 0, len(rts))
		for _, rt := range rts {
			parts = append(parts, fmt.Sprintf(`{"ref":"%s","tip":"%s"}`, rt.Ref, rt.Tip))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	wantStarted := fmt.Sprintf(
		`{"include_rejected":false,"intent":"%s","refs":%s,"remote":"origin","select_decision":"%s"}`,
		f.intentDig, refsJSON(res.Pushed), f.selDig)
	wantFinished := fmt.Sprintf(
		`{"error":"","intent":"%s","pushed":%s,"remote":"origin","removed":[],"up_to_date":[]}`,
		f.intentDig, refsJSON(res.Pushed))

	var started, finished string
	if err := f.led.Scan(func(e ledger.Event) error {
		switch e.Type {
		case "publish.started":
			started = string(e.Payload)
		case "publish.finished":
			finished = string(e.Payload)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if started != wantStarted {
		t.Errorf("publish.started:\n got %s\nwant %s", started, wantStarted)
	}
	if finished != wantFinished {
		t.Errorf("publish.finished:\n got %s\nwant %s", finished, wantFinished)
	}
}

func TestPublishPreflightRecordsNothing(t *testing.T) {
	countPublishEvents := func(t *testing.T, led *ledger.Ledger) int {
		t.Helper()
		n := 0
		if err := led.Scan(func(e ledger.Event) error {
			if strings.HasPrefix(e.Type, "publish.") {
				n++
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("missing remote", func(t *testing.T) {
		f := newFixture(t)
		f.newIntent("no remote")
		f.race(true)
		cfg := f.publishCfg(false)
		cfg.Remote = "nosuch"
		if _, err := Run(cfg); err == nil || !strings.Contains(err.Error(), "nosuch") {
			t.Fatalf("publish to a missing remote = %v", err)
		}
		if n := countPublishEvents(t, f.led); n != 0 {
			t.Errorf("pre-flight failure recorded %d publish events", n)
		}
	})
	t.Run("no select decision", func(t *testing.T) {
		f := newFixture(t)
		f.newIntent("never raced")
		if _, err := Run(f.publishCfg(false)); err == nil ||
			!strings.Contains(err.Error(), "no SELECT decision") {
			t.Fatalf("publish without a SELECT = %v", err)
		}
		if n := countPublishEvents(t, f.led); n != 0 {
			t.Errorf("pre-flight failure recorded %d publish events", n)
		}
	})
}

func TestRefspecCoversNamespace(t *testing.T) {
	tests := []struct {
		spec string
		want bool
	}{
		{"+refs/*:refs/*", true},
		{"refs/*:refs/*", true},
		{"refs/multiverso/*:refs/multiverso/*", true},
		{"refs/multiverso/intent/abc/*:refs/backup/*", true},
		{"refs/heads/*:refs/heads/*", false},
		{"refs/heads/main", false},
		{"main", false},
		{"+refs/tags/*:refs/tags/*", false},
	}
	for _, tt := range tests {
		if got := refspecCoversNamespace(tt.spec); got != tt.want {
			t.Errorf("refspecCoversNamespace(%q) = %v, want %v", tt.spec, got, tt.want)
		}
	}
}
