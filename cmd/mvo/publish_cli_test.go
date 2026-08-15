package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const fakeDig = "mv0:aaaaaaaaaaaabbbbbbbbbbbbccccccccccccddddddddddddeeeeeeeeeeeeffff"

// addOrigin creates a bare remote named origin for the scenario repo.
func addOrigin(t *testing.T, sc *scenario) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitCLI(t, t.TempDir(), "init", "-q", "--bare", "-b", sc.branch, bare)
	gitCLI(t, sc.repo, "remote", "add", "origin", bare)
	return bare
}

func TestPublishPruneFetchRaceUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"publish malformed digest", []string{"publish", "not-a-digest"}},
		{"publish unknown flag", []string{"publish", fakeDig, "--bogus"}},
		{"prune malformed digest", []string{"prune", "zzz"}},
		{"prune older-than unparseable", []string{"prune", fakeDig, "--older-than", "30d"}},
		{"prune older-than zero", []string{"prune", fakeDig, "--older-than", "0s"}},
		{"prune older-than negative", []string{"prune", fakeDig, "--older-than", "-5m"}},
		{"prune keep-admitted non-bool", []string{"prune", fakeDig, "--keep-admitted=maybe"}},
		{"fetch-race malformed short", []string{"fetch-race", "xyz"}},
		{"fetch-race missing digest", []string{"fetch-race"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := mvo(t, tt.args...)
			if code != exitUsage {
				t.Errorf("exit = %d, want %d\nstderr: %s", code, exitUsage, stderr)
			}
		})
	}

	t.Run("fetch-race key required without a workspace", func(t *testing.T) {
		dir := t.TempDir() // no workspace, no key
		_, stderr, code := mvo(t, "fetch-race", "aaaaaaaaaaaa", "--dir", dir)
		if code != exitUsage || !strings.Contains(stderr, "--key is required") {
			t.Errorf("exit = %d, stderr = %q", code, stderr)
		}
	})
}

func TestWorldsExplainFreshnessLine(t *testing.T) {
	sc := newScenario(t)
	base12 := sc.parent[:12]

	// FRESH: trunk head == intent base, nothing has moved yet.
	freshLine := "freshness: FRESH (base " + base12 + " == " + sc.branch + " head)"
	for _, verb := range []string{"worlds", "explain"} {
		out := mustMvo(t, verb, sc.intentDig, "--dir", sc.repo)
		if !strings.Contains(out, freshLine) {
			t.Errorf("%s output misses %q:\n%s", verb, freshLine, out)
		}
	}

	// STALE/advanced: the admission commit moved trunk past the base.
	sc.admit(t)
	staleLine := "freshness: STALE (" + sc.branch + " advanced past base " + base12 + ")"
	for _, verb := range []string{"worlds", "explain"} {
		out := mustMvo(t, verb, sc.intentDig, "--dir", sc.repo)
		if !strings.Contains(out, staleLine) {
			t.Errorf("%s output misses %q:\n%s", verb, staleLine, out)
		}
	}
}

func TestPublishPruneFetchRaceCLI(t *testing.T) {
	sc := newScenario(t)
	sc.admit(t)
	bare := addOrigin(t, sc)
	short := strings.TrimPrefix(sc.intentDig, "mv0:")[:12]

	// Publish: winner cand + evidence, output contract.
	out := mustMvo(t, "publish", sc.intentDig, "--dir", sc.repo)
	head := "published refs/multiverso/intent/" + short + " to origin (2 pushed, 0 up-to-date, 0 removed)"
	if !strings.HasPrefix(out, head+"\n") {
		t.Fatalf("publish output:\n%s\nwant prefix %q", out, head)
	}
	if !strings.Contains(out, "  refs/multiverso/intent/"+short+"/cand/1 ") ||
		!strings.Contains(out, "  refs/multiverso/intent/"+short+"/evidence ") {
		t.Errorf("publish output misses pushed ref lines:\n%s", out)
	}
	// Idempotent republish exits 0 and pushes nothing.
	out = mustMvo(t, "publish", sc.intentDig, "--dir", sc.repo)
	if !strings.Contains(out, "(0 pushed, 2 up-to-date, 0 removed)") {
		t.Errorf("republish output:\n%s", out)
	}

	// The operator lands trunk with an ordinary push (publish never does).
	gitCLI(t, sc.repo, "push", "-q", "origin", sc.branch)

	// fetch-race on a second machine: JSON shape + verification pass.
	consumer := filepath.Join(t.TempDir(), "consumer")
	gitCLI(t, t.TempDir(), "clone", "-q", bare, consumer)
	key := filepath.Join(sc.repo, ".multiverso", "keys", "local.pub")
	jsonOut := mustMvo(t, "fetch-race", short, "--dir", consumer, "--key", key, "--json")
	var rep struct {
		Schema    string `json:"schema"`
		Short     string `json:"short"`
		Intent    string `json:"intent"`
		Title     string `json:"title"`
		Decision  string `json:"decision"`
		Type      string `json:"type"`
		Winner    string `json:"winner"`
		Admitted  bool   `json:"admitted"`
		Freshness string `json:"freshness"`
		Items     []struct {
			Path  string `json:"path"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"items"`
		Refs int  `json:"refs"`
		OK   bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &rep); err != nil {
		t.Fatalf("fetch-race --json: %v\n%s", err, jsonOut)
	}
	if rep.Schema != schemaFetchRaceReport || rep.Short != short || rep.Intent != sc.intentDig {
		t.Errorf("report identity = %+v", rep)
	}
	if !rep.OK || !rep.Admitted || rep.Type != "SELECT" || rep.Refs != 2 || len(rep.Items) == 0 {
		t.Errorf("report = %+v", rep)
	}
	if !strings.HasPrefix(rep.Freshness, "STALE (") {
		t.Errorf("freshness = %q, want STALE (the clone head is the admission commit)", rep.Freshness)
	}
	for _, it := range rep.Items {
		if !it.OK {
			t.Errorf("item %s failed: %s", it.Path, it.Error)
		}
	}
	// Human output too.
	human := mustMvo(t, "fetch-race", short, "--dir", consumer, "--key", key)
	if !strings.Contains(human, "OK: race verified (") ||
		!strings.Contains(human, "admitted:  yes") ||
		!strings.Contains(human, "winner:    mv0:") {
		t.Errorf("fetch-race human output:\n%s", human)
	}

	// Prune (defaults, admitted): nothing deletable — winner + evidence
	// are the namespace and both are kept.
	out = mustMvo(t, "prune", sc.intentDig, "--remote", "origin", "--dir", sc.repo)
	if !strings.Contains(out, "pruned refs/multiverso/intent/"+short+": 0 local, 0 remote deleted, 2 kept") {
		t.Errorf("prune output:\n%s", out)
	}
	// Full wipe.
	out = mustMvo(t, "prune", sc.intentDig, "--remote", "origin", "--keep-admitted=false", "--dir", sc.repo)
	if !strings.Contains(out, ": 2 local, 2 remote deleted, 0 kept") {
		t.Errorf("wipe output:\n%s", out)
	}

	// Audit still replays clean over a ledger carrying publish/prune
	// events — observational events, no replay semantics.
	auditOut := mustMvo(t, "audit", "--json", "--dir", sc.repo)
	var audit struct {
		ChainOK         bool `json:"chain_ok"`
		ReplayIdentical bool `json:"replay_identical"`
		Decisions       int  `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(auditOut), &audit); err != nil {
		t.Fatalf("audit --json: %v\n%s", err, auditOut)
	}
	if !audit.ChainOK || !audit.ReplayIdentical || audit.Decisions < 2 {
		t.Errorf("audit = %+v", audit)
	}

	// The ledger carries the four M1d events.
	ws := openWS(t, sc.repo)
	st, err := loadState(ws.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.PublishFinishes) != 2 || st.PublishFinishes[0].Intent != sc.intentDig {
		t.Errorf("PublishFinishes = %+v", st.PublishFinishes)
	}
}
