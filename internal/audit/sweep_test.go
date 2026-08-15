package audit

// M1f: the CAS integrity sweep. The 2026-08 design partner study found
// `mvo audit` reporting OK after an attestation bundle had been deleted
// from CAS. Every row here is one referrer kind from the normative table,
// with the missing-and-corrupt injection the study's two findings are.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/ledger"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/signing"
)

// synthetic builds a ledger + CAS covering EVERY row of the referenced-set
// table, with all blobs present.
type synthetic struct {
	led     *ledger.Ledger
	store   *cas.Store
	casRoot string
	keys    map[string]string // label -> CAS key
}

func newSynthetic(t *testing.T) *synthetic {
	t.Helper()
	dir := t.TempDir()
	led, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	t.Cleanup(func() { led.Close() })
	store, err := cas.Open(filepath.Join(dir, "cas"))
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	s := &synthetic{led: led, store: store, casRoot: filepath.Join(dir, "cas"), keys: map[string]string{}}

	put := func(label, body string) string {
		key, err := store.Put([]byte(body))
		if err != nil {
			t.Fatalf("put %s: %v", label, err)
		}
		s.keys[label] = key
		return key
	}
	// Object events: the canonical bytes ARE the payload, stored at record
	// time, so payload_dig resolves.
	record := func(typ string, v any) string {
		dig, canon, err := object.Digest(v)
		if err != nil {
			t.Fatalf("digest %s: %v", typ, err)
		}
		if _, err := store.Put(canon); err != nil {
			t.Fatalf("store %s: %v", typ, err)
		}
		if _, err := led.Append(typ, canon); err != nil {
			t.Fatalf("append %s: %v", typ, err)
		}
		key, _ := object.CASKey(dig)
		s.keys[typ] = key
		return dig
	}
	event := func(typ string, body map[string]any) {
		canon, err := object.Canonical(body)
		if err != nil {
			t.Fatalf("canonical %s: %v", typ, err)
		}
		if _, err := led.Append(typ, canon); err != nil {
			t.Fatalf("append %s: %v", typ, err)
		}
	}

	polDig := record("policy.created", object.Policy{
		Schema: object.SchemaPolicy, HardGates: []string{"suite-pass"}, Ranking: []string{"gate_pass"},
	})
	intentDig := record("intent.created", object.Intent{
		Schema: object.SchemaIntent,
		Base:   object.Base{Commit: strings.Repeat("c", 40), Tree: "git:" + strings.Repeat("d", 40)},
		Spec:   object.Spec{Title: "t"}, Budget: object.Budget{MaxCandidates: 1, MaxWallMS: 1},
		Policy: polDig, CreatedAt: "2026-01-02T03:04:00Z",
	})
	ctxKey := put("context", "prompt bytes")
	patchKey := put("patch", "diff bytes")
	traceKey := put("trace", "transcript bytes")
	envDig, envCanon, err := object.Digest(map[string]any{"os": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(envCanon); err != nil {
		t.Fatal(err)
	}
	envKey, _ := object.CASKey(envDig)
	s.keys["env"] = envKey

	worldDig := record("world.created", object.World{
		Schema: object.SchemaWorld, Intent: intentDig, Tree: "git:" + strings.Repeat("a", 40),
		Env: envDig, IsolationTier: object.TierT0Worktree,
		Context: ctxKey, Patch: patchKey, Trace: traceKey,
		Outcome: object.OutcomeCompleted, CreatedAt: "2026-01-02T03:04:01Z",
	})
	stdoutKey := put("stdout", "collected 3 tests")
	stderrKey := put("stderr", "")
	probeKey := put("probe", `{"pytest":"8.4.1"}`)
	baseStreamKey := put("baseline_stream", "mvo-evidence/v0\tbase\n")
	event("baseline.recorded", map[string]any{
		"collected_total": 3, "intent": intentDig,
		"stdout": stdoutKey, "stderr": stderrKey, "probe": probeKey,
		// collected_base is the denominator collected-not-below divides by,
		// and this is the only record of how it was observed.
		"evidence_stream": baseStreamKey,
	})
	// agent.finished names a stderr blob that appears in NO other event —
	// and it is where a CONFIG_ERROR world's reason survives.
	agentStderrKey := put("agent_stderr", "mvo: script adapter: patch did not apply\n")
	event("agent.finished", map[string]any{
		"world": worldDig, "context": ctxKey, "transcript": traceKey,
		"stderr": agentStderrKey, "outcome": "CONFIG_ERROR",
	})
	artKey := put("artifact", "junit bytes")
	pluginKey := put("evidence_plugin", "# mvo_evidence.py\n")
	receiptDig := record("receipt.recorded", object.Receipt{
		Schema: object.SchemaReceipt, World: worldDig,
		Oracle: object.OracleRef{ID: "pytest-suite", Version: "v0", Config: "mv0:" + strings.Repeat("2", 64)},
		// The receipt names the observer that produced it; decision 14
		// calls that "an auditable fact, not a version guess", which is
		// only true if something audits it.
		Execution: object.Execution{
			EvidenceRegime: object.RegimeStreamed,
			EvidencePlugin: pluginKey,
		},
		Result:    object.Result{Status: "pass", Metrics: map[string]int64{}, Tools: map[string]string{}, Artifacts: []string{artKey}},
		Freshness: object.Freshness{Basis: object.BasisConstruction},
		CreatedAt: "2026-01-02T03:04:02Z",
	})
	decDig := record("decision.recorded", object.Decision{
		Schema: object.SchemaDecision, Type: "SELECT", Intent: intentDig,
		Subject: []string{worldDig}, Evidence: []string{receiptDig}, Policy: polDig,
		Rationale: "selected", CreatedAt: "2026-01-02T03:04:03Z",
	})
	bundleKey := put("bundle", "dsse envelope bytes")
	stmtKey := put("statement", "in-toto statement bytes")
	event("attestation.recorded", map[string]any{
		"bundle": bundleKey, "statement": strings.Replace(stmtKey, "sha256:", "mv0:", 1),
		"decision": decDig, "intent": intentDig,
	})
	return s
}

// Every row of the declared table is swept, and CAS objects nobody
// references are COUNTED, never failed.
func TestSweepReferencedSet(t *testing.T) {
	s := newSynthetic(t)
	// An unreferenced object: CAS legitimately holds more than one ledger
	// references (publication working sets, prior prunes).
	if _, err := s.store.Put([]byte("an orphan from a previous prune")); err != nil {
		t.Fatal(err)
	}
	rep, err := Sweep(s.led, s.store)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("sweep failed on an intact closure: missing=%+v corrupt=%+v", rep.Missing, rep.Corrupt)
	}
	if rep.Unreferenced != 1 {
		t.Errorf("cas_unreferenced = %d, want 1 — counted, never a failure", rep.Unreferenced)
	}
	// Each referrer kind must actually be in the referenced set.
	refs, err := References(s.led)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range refs {
		got[r.Key] = r.Referrer
	}
	for _, label := range []string{
		"context", "patch", "trace", "env", // world.created
		"artifact", "evidence_plugin", // receipt.recorded
		"stdout", "stderr", "probe", "baseline_stream", // baseline.recorded
		"agent_stderr",        // agent.finished
		"bundle", "statement", // attestation.recorded
		"policy.created", "intent.created", "world.created",
		"receipt.recorded", "decision.recorded",
	} {
		key := s.keys[label]
		if key == "" {
			t.Fatalf("fixture is missing %q", label)
		}
		if _, ok := got[key]; !ok {
			t.Errorf("%s (%s) is not in the referenced set", label, key)
		}
	}
	if rep.Checked != len(refs) {
		t.Errorf("checked = %d, want %d — the report must say how much it examined", rep.Checked, len(refs))
	}
}

// The study's two findings, as failures: a deleted attestation bundle and
// an edited stored artifact.
func TestSweepDetectsMissingAndCorrupt(t *testing.T) {
	t.Run("a deleted attestation bundle", func(t *testing.T) {
		s := newSynthetic(t)
		removeFromCAS(t, s, s.keys["bundle"])
		rep, err := Sweep(s.led, s.store)
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Missing) != 1 {
			t.Fatalf("missing = %+v, want exactly the bundle", rep.Missing)
		}
		if rep.Missing[0].Key != s.keys["bundle"] {
			t.Errorf("missing key = %s, want %s", rep.Missing[0].Key, s.keys["bundle"])
		}
		if !strings.Contains(rep.Missing[0].Referrer, "attestation.recorded") ||
			!strings.Contains(rep.Missing[0].Referrer, "(bundle)") {
			t.Errorf("referrer = %q, want it to name attestation.recorded and the field",
				rep.Missing[0].Referrer)
		}
		if rep.OK() {
			t.Error("the sweep reported OK over a deleted attestation blob")
		}
	})

	t.Run("one flipped byte inside a stored artifact", func(t *testing.T) {
		s := newSynthetic(t)
		corruptInCAS(t, s, s.keys["artifact"])
		rep, err := Sweep(s.led, s.store)
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Corrupt) != 1 {
			t.Fatalf("corrupt = %+v, want exactly the artifact", rep.Corrupt)
		}
		if !strings.Contains(rep.Corrupt[0].Referrer, "receipt.recorded") {
			t.Errorf("referrer = %q, want it to name the receipt", rep.Corrupt[0].Referrer)
		}
		if !strings.Contains(rep.Corrupt[0].Detail, "bytes hash to") {
			t.Errorf("detail = %q, want it to name what the bytes hash to", rep.Corrupt[0].Detail)
		}
	})

	// Every referrer kind, injected in turn: the sweep must blame the right
	// record whichever pointer went bad.
	for _, label := range []string{"context", "patch", "trace", "env", "artifact", "probe", "statement",
		// The three M1f references the first cut of the table left out:
		// the observer a receipt names, the baseline's own stream, and the
		// only surviving record of a CONFIG_ERROR world's reason.
		"evidence_plugin", "baseline_stream", "agent_stderr"} {
		// A fixture label is not always the payload field name (one event
		// already owns "stderr"), so the blame string is asserted against
		// what the referrer must actually say.
		wantIn := map[string]string{
			"evidence_plugin": "receipt.recorded",
			"baseline_stream": "baseline.recorded seq",
			"agent_stderr":    "agent.finished seq",
		}[label]
		if wantIn == "" {
			wantIn = label
		}
		t.Run("missing "+label, func(t *testing.T) {
			s := newSynthetic(t)
			removeFromCAS(t, s, s.keys[label])
			rep, err := Sweep(s.led, s.store)
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Missing) != 1 || rep.Missing[0].Key != s.keys[label] {
				t.Fatalf("missing = %+v, want exactly %s", rep.Missing, label)
			}
			if !strings.Contains(rep.Missing[0].Referrer, wantIn) {
				t.Errorf("referrer = %q, want it to name %q", rep.Missing[0].Referrer, wantIn)
			}
		})
	}
}

// M1f compatibility, and the reason this test exists: `mvo admit` only
// began Put-ting the statement beside the bundle in M1f. Every M0–M1e
// ledger names a statement digest whose bytes live ONLY inside the DSSE
// envelope, and the first cut of the sweep reported those intact
// workspaces as `MISSING: … (statement)` and exited 1 — an audit accusing
// a workspace of losing something it never lost, which is the M1f
// over-claim pointed the other way.
//
// Reproduced against a real pre-M1f ledger before it was fixed; frozen
// here so it cannot come back.
func TestSweepResolvesPreM1fStatementInsideTheBundle(t *testing.T) {
	stmt := []byte(`{"_type":"https://in-toto.io/Statement/v1","subject":[]}`)
	signer, err := signing.Generate(t.TempDir())
	if err != nil {
		t.Fatalf("signing.Generate: %v", err)
	}
	env, err := signing.Sign(signer, signing.PayloadTypeInToto, stmt)
	if err != nil {
		t.Fatalf("signing.Sign: %v", err)
	}
	bundle, err := object.Canonical(env)
	if err != nil {
		t.Fatalf("canonical envelope: %v", err)
	}

	// The pre-M1f shape, built standalone so the ledger holds exactly one
	// attestation and the assertions are about it alone: the bundle bytes
	// are in CAS, the statement bytes are not, and the event names both.
	// bundleBytes is what actually lands in CAS, so a caller can hand the
	// event a container carrying a different payload.
	preM1f := func(t *testing.T, bundleBytes []byte) *synthetic {
		t.Helper()
		dir := t.TempDir()
		led, err := ledger.Open(filepath.Join(dir, "ledger.db"))
		if err != nil {
			t.Fatalf("ledger.Open: %v", err)
		}
		t.Cleanup(func() { led.Close() })
		store, err := cas.Open(filepath.Join(dir, "cas"))
		if err != nil {
			t.Fatalf("cas.Open: %v", err)
		}
		s := &synthetic{led: led, store: store, casRoot: filepath.Join(dir, "cas"), keys: map[string]string{}}
		bundleKey, err := store.Put(bundleBytes)
		if err != nil {
			t.Fatalf("put bundle: %v", err)
		}
		s.keys["bundle"] = bundleKey
		canon, err := object.Canonical(map[string]any{
			"bundle": bundleKey, "statement": object.DigestBytes(stmt),
			"decision": "mv0:" + strings.Repeat("7", 64), "intent": "mv0:" + strings.Repeat("8", 64),
		})
		if err != nil {
			t.Fatalf("canonical event: %v", err)
		}
		if _, err := led.Append("attestation.recorded", canon); err != nil {
			t.Fatalf("append attestation.recorded: %v", err)
		}
		return s
	}

	t.Run("an intact pre-M1f closure sweeps clean", func(t *testing.T) {
		s := preM1f(t, bundle)
		rep, err := Sweep(s.led, s.store)
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if !rep.OK() {
			t.Fatalf("the sweep failed an intact pre-M1f workspace: missing=%+v corrupt=%+v",
				rep.Missing, rep.Corrupt)
		}
		// It is CHECKED, not skipped: a reference nobody examined must never
		// render the same as one that was verified.
		var found bool
		refs, err := References(s.led)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range refs {
			if key, _ := object.CASKey(object.DigestBytes(stmt)); r.Key == key {
				found = true
				if r.Within == "" {
					t.Error("the statement reference declares no container to resolve through")
				}
			}
		}
		if !found {
			t.Error("the statement is not in the referenced set")
		}
	})

	// The container vouches for the statement only when it really carries
	// it. Nothing here is waved through on the strength of a field name.
	t.Run("a bundle carrying different bytes leaves it missing", func(t *testing.T) {
		other, err := signing.Sign(signer, signing.PayloadTypeInToto, []byte(`{"_type":"other"}`))
		if err != nil {
			t.Fatal(err)
		}
		canon, err := object.Canonical(other)
		if err != nil {
			t.Fatal(err)
		}
		s := preM1f(t, canon)
		rep, err := Sweep(s.led, s.store)
		if err != nil {
			t.Fatal(err)
		}
		if rep.OK() {
			t.Fatal("a bundle whose payload is a DIFFERENT statement vouched for the reference")
		}
	})

	t.Run("a deleted bundle leaves the statement missing too", func(t *testing.T) {
		s := preM1f(t, bundle)
		removeFromCAS(t, s, s.keys["bundle"])
		rep, err := Sweep(s.led, s.store)
		if err != nil {
			t.Fatal(err)
		}
		if rep.OK() {
			t.Fatal("the sweep reported OK with neither the statement nor its bundle in CAS")
		}
		var sawStatement bool
		for _, m := range rep.Missing {
			if strings.Contains(m.Referrer, "(statement)") {
				sawStatement = true
			}
		}
		if !sawStatement {
			t.Errorf("missing = %+v, want the statement named", rep.Missing)
		}
	})
}

// An empty ledger has an empty referenced set, and the sweep says so
// rather than failing: "nothing verified" is not a failure, it is a fact.
func TestSweepEmptyLedger(t *testing.T) {
	dir := t.TempDir()
	led, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer led.Close()
	store, err := cas.Open(filepath.Join(dir, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Sweep(led, store)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !rep.OK() || rep.Checked != 0 || rep.Unreferenced != 0 {
		t.Errorf("report = %+v, want an honest zero", rep)
	}
}

func casPath(t *testing.T, s *synthetic, key string) string {
	t.Helper()
	hex := strings.TrimPrefix(key, "sha256:")
	// The store roots at <dir>/cas; Keys() is the only public enumeration,
	// so the path is rebuilt from the layout the package documents.
	all, err := s.store.Keys()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range all {
		if k == key {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s is not in the store", key)
	}
	return filepath.Join(s.casRoot, "sha256", hex[:2], hex[2:])
}

func removeFromCAS(t *testing.T, s *synthetic, key string) {
	t.Helper()
	if err := os.Remove(casPath(t, s, key)); err != nil {
		t.Fatalf("remove %s: %v", key, err)
	}
}

func corruptInCAS(t *testing.T, s *synthetic, key string) {
	t.Helper()
	p := casPath(t, s, key)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		b = []byte("x")
	}
	b[0] ^= 0xff
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
