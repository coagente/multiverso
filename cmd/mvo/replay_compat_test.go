package main

// M1e compatibility: a ledger written by M0–M1d replays byte-for-byte under
// the M1e binary. The fixtures here are the OLD canonical shapes — worlds
// without patch_bytes, receipts without metrics or tools — appended as raw
// payloads, exactly as the older binaries wrote them. Nothing about those
// decisions is re-derived from a re-serialization of the decoded objects
// (M1e decision 1), so adding fields to World and Receipt cannot move a
// recorded digest.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/workspace"
)

// legacyLedger appends a complete pre-M1e race window and returns the
// decision's recorded rationale.
type legacyLedger struct {
	t  *testing.T
	ws *workspace.Workspace
}

func (l *legacyLedger) append(typ string, body map[string]any) string {
	l.t.Helper()
	canon, err := object.Canonical(body)
	if err != nil {
		l.t.Fatalf("canonical %s: %v", typ, err)
	}
	if _, err := l.ws.CAS.Put(canon); err != nil {
		l.t.Fatalf("store %s: %v", typ, err)
	}
	if _, err := l.ws.Ledger.Append(typ, canon); err != nil {
		l.t.Fatalf("append %s: %v", typ, err)
	}
	return object.DigestBytes(canon)
}

// legacyWorld is the M1c/M1d world shape: no patch_bytes.
func legacyWorld(intent, name string) map[string]any {
	return map[string]any{
		"schema":         object.SchemaWorld,
		"intent":         intent,
		"tree":           "git:" + strings.Repeat(name[len(name)-1:], 40),
		"env":            "mv0:" + strings.Repeat("5", 64),
		"isolation_tier": object.TierT0Worktree,
		"producer": map[string]any{
			"adapter": "script@v0", "model": "", "identity_tier": "claimed", "role": "generator",
		},
		"context":    "sha256:" + strings.Repeat("a", 64),
		"patch":      "sha256:" + strings.Repeat("6", 64),
		"trace":      "sha256:" + strings.Repeat("b", 64),
		"cost":       map[string]any{"wall_ms": 5, "usd_micro": 0, "tokens_in": 0, "tokens_out": 0, "source": "none"},
		"outcome":    object.OutcomeCompleted,
		"created_at": "2026-01-02T03:04:05Z",
	}
}

// legacyReceipt is the M1c/M1d receipt shape: no result.metrics, no
// result.tools.
func legacyReceipt(worldDig, tree, status string, wallMS int64) map[string]any {
	exit := 0
	if status != "pass" {
		exit = 1
	}
	return map[string]any{
		"schema": object.SchemaReceipt,
		"world":  worldDig,
		"oracle": map[string]any{"id": "command", "version": "v0", "config": "mv0:" + strings.Repeat("2", 64)},
		"execution": map[string]any{
			"argv": []string{"python3", "-m", "pytest", "-q"}, "exit_code": exit,
			"duration_ms": wallMS, "isolation_tier": object.TierT0Worktree,
			"isolation_caps": map[string]any{
				"cap_drop": "", "cpu_milli": 0, "memory_bytes": 0, "network": "host",
				"pids_limit": 0, "read_only_root": false, "user": "",
			},
		},
		"result": map[string]any{"status": status, "artifacts": []string{"sha256:out"}},
		"freshness": map[string]any{
			"basis":     object.BasisConstruction,
			"valid_for": map[string]any{"tree": tree, "env": "mv0:" + strings.Repeat("5", 64)},
		},
		"recheck_tier": "V1-replayable",
		"family":       "suite",
		"cost":         map[string]any{"wall_ms": wallMS},
		"created_at":   "2026-01-02T03:04:06Z",
	}
}

func TestAuditReplaysPreM1eLedger(t *testing.T) {
	repo := initWorkspace(t)
	ws, err := workspace.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	l := &legacyLedger{t: t, ws: ws}

	// The M0 policy, recorded exactly as M0 recorded it.
	polDig := l.append("policy.created", map[string]any{
		"schema":     object.SchemaPolicy,
		"hard_gates": []string{"suite-pass"},
		"ranking":    []string{"gate_pass", "wall_ms_asc"},
	})
	intentDig := l.append("intent.created", map[string]any{
		"schema":     object.SchemaIntent,
		"base":       map[string]any{"commit": strings.Repeat("c", 40), "tree": "git:" + strings.Repeat("d", 40)},
		"spec":       map[string]any{"title": "legacy race", "description": ""},
		"budget":     map[string]any{"max_candidates": 2, "max_wall_ms": 600000},
		"policy":     polDig,
		"created_at": "2026-01-02T03:04:00Z",
	})
	l.append("race.started", map[string]any{"intent": intentDig})

	wa := legacyWorld(intentDig, "world-a")
	wb := legacyWorld(intentDig, "world-b")
	waDig := l.append("world.created", wa)
	wbDig := l.append("world.created", wb)
	l.append("receipt.recorded", legacyReceipt(waDig, wa["tree"].(string), "pass", 42))
	l.append("receipt.recorded", legacyReceipt(wbDig, wb["tree"].(string), "fail", 7))

	// The decision M0 recorded, written by hand in M0's frozen sentence.
	rationale := fmt.Sprintf(
		"1/2 worlds passed hard gates [suite-pass]; selected %s by ranking [gate_pass,wall_ms_asc] (wall_ms=42)",
		waDig)
	subject := []string{waDig, wbDig}
	evidence := []string{
		object.DigestBytes(mustCanonical(t, legacyReceipt(waDig, wa["tree"].(string), "pass", 42))),
		object.DigestBytes(mustCanonical(t, legacyReceipt(wbDig, wb["tree"].(string), "fail", 7))),
	}
	if evidence[1] < evidence[0] {
		evidence[0], evidence[1] = evidence[1], evidence[0]
	}
	l.append("decision.recorded", map[string]any{
		"schema":     object.SchemaDecision,
		"type":       "SELECT",
		"intent":     intentDig,
		"subject":    subject,
		"evidence":   evidence,
		"policy":     polDig,
		"rationale":  rationale,
		"created_at": "2026-01-02T03:04:07Z",
	})
	l.append("race.finished", map[string]any{"intent": intentDig, "decision": ""})
	ws.Close()

	// Replay: the M1e binary must reproduce that decision exactly.
	// --cas-sweep=false: this fixture is a hand-built ledger whose blob
	// references are synthetic, so there is nothing in CAS to sweep. What
	// is under test here is REPLAY compatibility, and the sweep is a
	// separate axis with its own tests — a skipped check that renders
	// identically to a passed one is exactly what M1f removes, which is
	// why it has to be asked for by name.
	stdout, stderr, code := mvo(t, "audit", "--dir", repo, "--cas-sweep=false")
	if code != exitOK {
		t.Fatalf("audit exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "decisions replayed") {
		t.Errorf("audit output = %q", stdout)
	}

	// And the fixtures really are pre-M1e: decoding them into today's
	// structs and re-serializing would mint a DIFFERENT digest, which is
	// exactly why Decide must never do that.
	ws2, err := workspace.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	raw, err := ws2.GetObject(waDig)
	if err != nil {
		t.Fatal(err)
	}
	var w object.World
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	if reDig, _, err := object.Digest(w); err != nil {
		t.Fatal(err)
	} else if reDig == waDig {
		t.Error("fixture is not exercising the property: the legacy world already carries the M1e fields")
	}
	if w.PatchBytes != 0 {
		t.Errorf("patch_bytes = %d, want the zero value for a pre-M1e world", w.PatchBytes)
	}
}

// A v0-policy decision and a v1-policy decision replay side by side in one
// ledger: two frozen dialects, one audit.
func TestAuditReplaysBothPolicySchemas(t *testing.T) {
	repo := initWorkspace(t)
	ws, err := workspace.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	l := &legacyLedger{t: t, ws: ws}

	polDig := l.append("policy.created", map[string]any{
		"schema":     object.SchemaPolicy,
		"hard_gates": []string{"suite-pass"},
		"ranking":    []string{"gate_pass", "wall_ms_asc"},
	})
	v1Canon := mustCanonical(t, policy.Command([]string{"python3", "-m", "pytest", "-q"}, 600000))
	if _, err := ws.CAS.Put(v1Canon); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Ledger.Append("policy.created", v1Canon); err != nil {
		t.Fatal(err)
	}
	v1Dig := object.DigestBytes(v1Canon)
	v1Pol, err := policy.Decode(v1Canon)
	if err != nil {
		t.Fatal(err)
	}
	cmdCfg := ""
	if o, ok := v1Pol.OracleByName("suite"); ok {
		cmdCfg = o.Config
	}

	for _, tc := range []struct {
		pol       string
		cfg       string
		rationale func(worldDig string) string
	}{
		{
			pol: polDig, cfg: "mv0:" + strings.Repeat("2", 64),
			rationale: func(w string) string {
				return fmt.Sprintf(
					"1/1 worlds passed hard gates [suite-pass]; selected %s by ranking [gate_pass,wall_ms_asc] (wall_ms=42)", w)
			},
		},
		{
			pol: v1Dig, cfg: cmdCfg,
			rationale: func(w string) string {
				return fmt.Sprintf(
					"1/1 worlds passed hard gates [status-pass@suite]; selected %s (sole world passing all hard gates); ranking [gate_pass,wall_ms_asc,world_digest_asc]", w)
			},
		},
	} {
		intentDig := l.append("intent.created", map[string]any{
			"schema":     object.SchemaIntent,
			"base":       map[string]any{"commit": strings.Repeat("c", 40), "tree": "git:" + strings.Repeat("d", 40)},
			"spec":       map[string]any{"title": "race under " + tc.pol, "description": ""},
			"budget":     map[string]any{"max_candidates": 2, "max_wall_ms": 600000},
			"policy":     tc.pol,
			"created_at": "2026-01-02T03:04:00Z",
		})
		l.append("race.started", map[string]any{"intent": intentDig})
		w := legacyWorld(intentDig, "world-a")
		wDig := l.append("world.created", w)
		rec := legacyReceipt(wDig, w["tree"].(string), "pass", 42)
		rec["oracle"].(map[string]any)["config"] = tc.cfg
		if tc.pol == v1Dig {
			rec["oracle"].(map[string]any)["id"] = policy.KindCommand
			rec["result"].(map[string]any)["metrics"] = map[string]any{}
			rec["result"].(map[string]any)["tools"] = map[string]any{}
		}
		recDig := l.append("receipt.recorded", rec)
		l.append("decision.recorded", map[string]any{
			"schema":     object.SchemaDecision,
			"type":       "SELECT",
			"intent":     intentDig,
			"subject":    []string{wDig},
			"evidence":   []string{recDig},
			"policy":     tc.pol,
			"rationale":  tc.rationale(wDig),
			"created_at": "2026-01-02T03:04:07Z",
		})
		l.append("race.finished", map[string]any{"intent": intentDig, "decision": ""})
	}
	ws.Close()

	// --cas-sweep=false: this fixture is a hand-built ledger whose blob
	// references are synthetic, so there is nothing in CAS to sweep. What
	// is under test here is REPLAY compatibility, and the sweep is a
	// separate axis with its own tests — a skipped check that renders
	// identically to a passed one is exactly what M1f removes, which is
	// why it has to be asked for by name.
	stdout, stderr, code := mvo(t, "audit", "--dir", repo, "--cas-sweep=false")
	if code != exitOK {
		t.Fatalf("audit exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "2 decisions replayed") {
		t.Errorf("audit output = %q, want both decisions replayed", stdout)
	}
}

// mustCanonical is the canonical encoding of a fixture body, which is what
// the older binaries appended and therefore what the digests name.
func mustCanonical(t *testing.T, v any) []byte {
	t.Helper()
	b, err := object.Canonical(v)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	return b
}
