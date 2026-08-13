package object

import (
	"strings"
	"testing"
)

// intentLike declares fields deliberately out of alphabetical order to
// prove canonicalization sorts keys regardless of struct declaration.
type intentLike struct {
	Schema string   `json:"schema"`
	Title  string   `json:"title"`
	N      int      `json:"n"`
	Tags   []string `json:"tags"`
}

// Golden values cross-checked against an independent implementation
// (python json.dumps sort_keys, separators=(",",":"), ensure_ascii=False).
func TestDigestGolden(t *testing.T) {
	tests := []struct {
		name      string
		v         any
		wantCanon string
		wantDig   string
	}{
		{
			name:      "map keys sorted",
			v:         map[string]any{"b": 2, "a": 1},
			wantCanon: `{"a":1,"b":2}`,
			wantDig:   "mv0:43258cff783fe7036d8a43033f830adfc60ec037382473548ac742b888292777",
		},
		{
			name:      "struct fields sorted, utf-8 and escapes",
			v:         intentLike{Schema: SchemaIntent, Title: "héllo\n\"w\"", N: 7, Tags: []string{"b", "a"}},
			wantCanon: "{\"n\":7,\"schema\":\"multiverso.dev/intent/v0\",\"tags\":[\"b\",\"a\"],\"title\":\"héllo\\n\\\"w\\\"\"}",
			wantDig:   "mv0:85b28f7001ce84b30302d536dbef2c32ba1bffade20811b51f54ca87e0f80e74",
		},
		{
			name:      "nested sort, null, bool, control char",
			v:         map[string]any{"a": nil, "b": map[string]any{"y": true, "x": []int{1, 2}}, "ctl": "\x01"},
			wantCanon: `{"a":null,"b":{"x":[1,2],"y":true},"ctl":"\u0001"}`,
			wantDig:   "mv0:ef7d6cf9bc6db45e58a5335379d09200d52f28c183b5274b278e9af6b43a3808",
		},
		{
			name:      "empty object, array, string",
			v:         map[string]any{"empty_obj": map[string]any{}, "empty_arr": []any{}, "empty_str": ""},
			wantCanon: `{"empty_arr":[],"empty_obj":{},"empty_str":""}`,
			wantDig:   "mv0:2a8781af9ac37e30b45f58e83894528acb1472c33bdf4c2ef0d6b6d86e4f190f",
		},
		{
			name: "unicode passes through raw, no HTML escaping",
			v: map[string]any{
				"emoji": "\U0001F680\U0001F30C",
				"es":    "señal",
				"jp":    "日本語",
				"mixed": "a\x01b<&>",
			},
			wantCanon: `{"emoji":"🚀🌌","es":"señal","jp":"日本語","mixed":"a\u0001b<&>"}`,
			wantDig:   "mv0:b9b68d9cfaa73f6f0367578e56a2d48d7f51da88863fc7e4b33004c2c869f5d6",
		},
		{
			name: "intent object",
			v: Intent{
				Schema: SchemaIntent,
				Base:   Base{Commit: "3f786850e387550fdab836ed7e6dc881de23001b", Tree: "git:89e6c98d92887913cadf06b2adb97f26cde4849b"},
				Spec:   Spec{Title: "Fix núcleo — 修复", Description: ""},
				Budget: Budget{MaxCandidates: 2, MaxWallMS: 600000},
				Policy: "mv0:" + strings.Repeat("0", 64),
				// CreatedAt participates in the digest (NFR-1): objects are
				// immutable records of what happened.
				CreatedAt: "2026-01-02T03:04:05Z",
			},
			wantCanon: `{"base":{"commit":"3f786850e387550fdab836ed7e6dc881de23001b","tree":"git:89e6c98d92887913cadf06b2adb97f26cde4849b"},"budget":{"max_candidates":2,"max_wall_ms":600000},"created_at":"2026-01-02T03:04:05Z","policy":"mv0:` + strings.Repeat("0", 64) + `","schema":"multiverso.dev/intent/v0","spec":{"description":"","title":"Fix núcleo — 修复"}}`,
			wantDig:   "mv0:2ddec07fc097fe911028cb010dd293a0b042ab56618febe5567fb286ef39464c",
		},
		{
			name: "receipt object, deeply nested",
			v: Receipt{
				Schema:      SchemaReceipt,
				World:       "mv0:" + strings.Repeat("1", 64),
				Oracle:      OracleRef{ID: "command", Version: "v0", Config: "mv0:" + strings.Repeat("2", 64)},
				Execution:   Execution{Argv: []string{"python3", "-m", "pytest", "-q"}, ExitCode: 1, DurationMS: 1234, IsolationTier: "T0-worktree"},
				Result:      Result{Status: "fail", Artifacts: []string{"sha256:aa11", "sha256:bb22"}},
				Freshness:   Freshness{Basis: "construction", ValidFor: ValidFor{Tree: "git:89e6c98d92887913cadf06b2adb97f26cde4849b", Env: "mv0:" + strings.Repeat("3", 64)}},
				RecheckTier: "V1-replayable",
				Family:      "suite",
				Cost:        Cost{WallMS: 1234},
				CreatedAt:   "2026-01-02T03:04:05Z",
			},
			wantCanon: `{"cost":{"wall_ms":1234},"created_at":"2026-01-02T03:04:05Z","execution":{"argv":["python3","-m","pytest","-q"],"duration_ms":1234,"exit_code":1,"isolation_tier":"T0-worktree"},"family":"suite","freshness":{"basis":"construction","valid_for":{"env":"mv0:` + strings.Repeat("3", 64) + `","tree":"git:89e6c98d92887913cadf06b2adb97f26cde4849b"}},"oracle":{"config":"mv0:` + strings.Repeat("2", 64) + `","id":"command","version":"v0"},"recheck_tier":"V1-replayable","result":{"artifacts":["sha256:aa11","sha256:bb22"],"status":"fail"},"schema":"multiverso.dev/receipt/v0","world":"mv0:` + strings.Repeat("1", 64) + `"}`,
			wantDig:   "mv0:3055fb611efeb67820bb712e3696f9ebaa1c49deb547a3504be8327f09f1905e",
		},
		{
			name:      "policy object with empty slices",
			v:         Policy{Schema: SchemaPolicy, HardGates: []string{}, Ranking: []string{}},
			wantCanon: `{"hard_gates":[],"ranking":[],"schema":"multiverso.dev/policy/v0"}`,
			wantDig:   "mv0:8dbcd0a12b566d973499b1a96dd68b7a01d2ec7a1bf9f2df1527d30f87b6da16",
		},
		{
			// M1b: context/trace/cost are always serialized — no omitempty
			// games, optional fields would make digests ambiguous.
			name: "world object with context, trace, and cost",
			v: World{
				Schema:        SchemaWorld,
				Intent:        "mv0:" + strings.Repeat("4", 64),
				Tree:          "git:89e6c98d92887913cadf06b2adb97f26cde4849b",
				Env:           "mv0:" + strings.Repeat("5", 64),
				IsolationTier: "T0-worktree",
				Producer:      Producer{Adapter: "claude-code@v0", Model: "claude-sonnet-5", IdentityTier: "claimed", Role: "generator"},
				Context:       "sha256:" + strings.Repeat("a", 64),
				Patch:         "sha256:" + strings.Repeat("6", 64),
				Trace:         "sha256:" + strings.Repeat("b", 64),
				Cost:          RunCost{WallMS: 1234, USDMicro: 4200, TokensIn: 1300, TokensOut: 345, Source: "client-estimate"},
				Outcome:       OutcomeCompleted,
				CreatedAt:     "2026-01-02T03:04:05Z",
			},
			wantCanon: `{"context":"sha256:` + strings.Repeat("a", 64) + `","cost":{"source":"client-estimate","tokens_in":1300,"tokens_out":345,"usd_micro":4200,"wall_ms":1234},"created_at":"2026-01-02T03:04:05Z","env":"mv0:` + strings.Repeat("5", 64) + `","intent":"mv0:` + strings.Repeat("4", 64) + `","isolation_tier":"T0-worktree","outcome":"COMPLETED","patch":"sha256:` + strings.Repeat("6", 64) + `","producer":{"adapter":"claude-code@v0","identity_tier":"claimed","model":"claude-sonnet-5","role":"generator"},"schema":"multiverso.dev/world/v0","trace":"sha256:` + strings.Repeat("b", 64) + `","tree":"git:89e6c98d92887913cadf06b2adb97f26cde4849b"}`,
			wantDig:   "mv0:049c3cea7c06a38839c3083c6a5d4248756052cc93dc628c767f8f530d958f18",
		},
		{
			name:      "run cost zero value serializes every field",
			v:         RunCost{Source: "none"},
			wantCanon: `{"source":"none","tokens_in":0,"tokens_out":0,"usd_micro":0,"wall_ms":0}`,
			wantDig:   "mv0:a9df3066548b262b2ac0e0f4989a0ae9d450b96600a6bbbeb36c11c8dc44788e",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dig, canon, err := Digest(tt.v)
			if err != nil {
				t.Fatalf("Digest: %v", err)
			}
			if string(canon) != tt.wantCanon {
				t.Errorf("canonical = %q, want %q", canon, tt.wantCanon)
			}
			if dig != tt.wantDig {
				t.Errorf("digest = %q, want %q", dig, tt.wantDig)
			}
		})
	}
}

func TestDigestStability(t *testing.T) {
	values := []any{
		map[string]any{"z": []any{"a", 1, true}, "a": map[string]any{"k": "v"}},
		World{
			Schema:        SchemaWorld,
			Intent:        "mv0:" + strings.Repeat("4", 64),
			Tree:          "git:89e6c98d92887913cadf06b2adb97f26cde4849b",
			Env:           "mv0:" + strings.Repeat("5", 64),
			IsolationTier: "T0-worktree",
			Producer:      Producer{Adapter: "script@v0", Model: "", IdentityTier: "claimed", Role: "generator"},
			Context:       "sha256:" + strings.Repeat("9", 64),
			Patch:         "sha256:" + strings.Repeat("6", 64),
			Trace:         "sha256:" + strings.Repeat("a", 64),
			Cost:          RunCost{WallMS: 42, Source: "none"},
			Outcome:       OutcomeCompleted,
			CreatedAt:     "2026-01-02T03:04:05Z",
		},
		Decision{
			Schema:    SchemaDecision,
			Type:      "SELECT",
			Intent:    "mv0:" + strings.Repeat("4", 64),
			Subject:   []string{"mv0:" + strings.Repeat("7", 64)},
			Evidence:  []string{"mv0:" + strings.Repeat("8", 64)},
			Policy:    "mv0:" + strings.Repeat("0", 64),
			Rationale: "gate suite-pass: 1/2 passed; winner by wall_ms_asc",
			CreatedAt: "2026-01-02T03:04:05Z",
		},
	}
	for _, v := range values {
		first, _, err := Digest(v)
		if err != nil {
			t.Fatalf("Digest(%T): %v", v, err)
		}
		for i := 0; i < 50; i++ {
			got, _, err := Digest(v)
			if err != nil {
				t.Fatalf("Digest(%T) run %d: %v", v, i, err)
			}
			if got != first {
				t.Fatalf("digest of %T unstable on run %d: %q != %q", v, i, got, first)
			}
		}
	}
}

// TestOutcomeConstants pins the six-value outcome taxonomy (AG-1): the
// exact strings are World-schema vocabulary and must never drift.
func TestOutcomeConstants(t *testing.T) {
	got := []string{
		OutcomeCompleted, OutcomeBudgetExceeded, OutcomeInterrupted,
		OutcomeConfigError, OutcomeProviderError, OutcomeCrash,
	}
	want := []string{
		"COMPLETED", "BUDGET_EXCEEDED", "INTERRUPTED",
		"CONFIG_ERROR", "PROVIDER_ERROR", "CRASH",
	}
	if len(got) != 6 {
		t.Fatalf("outcome taxonomy has %d values, want exactly 6", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("outcome[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCanonicalRejectsNonJSON(t *testing.T) {
	if _, err := Canonical(make(chan int)); err == nil {
		t.Fatal("Canonical(chan) succeeded, want error")
	}
}

func TestCASKey(t *testing.T) {
	hex64 := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		dig     string
		want    string
		wantErr bool
	}{
		{"object digest", "mv0:" + hex64, "sha256:" + hex64, false},
		{"already a cas key", "sha256:" + hex64, "", true},
		{"empty", "", "", true},
		{"bare hex", hex64, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CASKey(tt.dig)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("CASKey(%q) = %q, want error", tt.dig, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CASKey(%q): %v", tt.dig, err)
			}
			if got != tt.want {
				t.Errorf("CASKey(%q) = %q, want %q", tt.dig, got, tt.want)
			}
		})
	}
}
