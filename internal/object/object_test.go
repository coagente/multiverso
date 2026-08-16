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
			// M2b decision 12: budget.max_oracle_ms is ADDITIVE and always
			// serialized, so a NEW intent's bytes carry it and its digest
			// moves — accepted in writing, exactly as M1b/M1c/M1e/M1f
			// accepted it for the receipt envelope. Nothing recorded moves:
			// M1e decision 1 pairs every recorded object with the digest it
			// was recorded under and never re-serializes, so an M1-era
			// intent decodes with the field absent ⇒ 0 ⇒ unbounded ⇒ the
			// exhaustive ladder, and keeps racing exactly as it did.
			wantCanon: `{"base":{"commit":"3f786850e387550fdab836ed7e6dc881de23001b","tree":"git:89e6c98d92887913cadf06b2adb97f26cde4849b"},"budget":{"max_candidates":2,"max_oracle_ms":0,"max_wall_ms":600000},"created_at":"2026-01-02T03:04:05Z","policy":"mv0:` + strings.Repeat("0", 64) + `","schema":"multiverso.dev/intent/v0","spec":{"description":"","title":"Fix núcleo — 修复"}}`,
			wantDig:   "mv0:4fe3d5314bd2c998795a42cade03332e3b923d5f8b44288659c5f64805cec64e",
		},
		{
			// M1c: execution.isolation_caps is always serialized (XP-2; no
			// omitempty games) — receipt digests re-derived. M1e: so are
			// result.metrics and result.tools, EMPTY here because a command
			// oracle parses nothing — {} is "measured nothing", null would
			// be a lie about the shape of the record (EP-2).
			name: "receipt object, deeply nested",
			v: Receipt{
				Schema:      SchemaReceipt,
				World:       "mv0:" + strings.Repeat("1", 64),
				Oracle:      OracleRef{ID: "command", Version: "v0", Config: "mv0:" + strings.Repeat("2", 64)},
				Execution:   Execution{Argv: []string{"python3", "-m", "pytest", "-q"}, ExitCode: 1, DurationMS: 1234, IsolationTier: TierT0Worktree, IsolationCaps: HostCaps()},
				Result:      NewResult("fail", "sha256:aa11", "sha256:bb22"),
				Freshness:   Freshness{Basis: BasisConstruction, ValidFor: ValidFor{Tree: "git:89e6c98d92887913cadf06b2adb97f26cde4849b", Env: "mv0:" + strings.Repeat("3", 64)}},
				RecheckTier: "V1-replayable",
				Family:      "suite",
				// M2a decision 22: {0, ""} is the honest "unknown scaling
				// unit" a command oracle has — it parses nothing, so it
				// counts nothing. Inputs is {} and never null (decision 24).
				Cost:      Cost{WallMS: 1234},
				Inputs:    NoInputs(),
				CreatedAt: "2026-01-02T03:04:05Z",
			},
			wantCanon: `{"correlation":{"corpus":"","executor":"","generator":"","signal":""},"cost":{"unit":"","units":0,"wall_ms":1234},"created_at":"2026-01-02T03:04:05Z","execution":{"argv":["python3","-m","pytest","-q"],"duration_ms":1234,"evidence_plugin":"","evidence_regime":"","exit_code":1,"isolation_caps":{"cap_drop":"","cpu_milli":0,"memory_bytes":0,"network":"host","pids_limit":0,"read_only_root":false,"user":""},"isolation_tier":"T0-worktree"},"family":"suite","freshness":{"basis":"construction","valid_for":{"env":"mv0:` + strings.Repeat("3", 64) + `","tree":"git:89e6c98d92887913cadf06b2adb97f26cde4849b"}},"inputs":{},"oracle":{"config":"mv0:` + strings.Repeat("2", 64) + `","id":"command","version":"v0"},"recheck_tier":"V1-replayable","result":{"artifacts":["sha256:aa11","sha256:bb22"],"detail":"","metrics":{},"status":"fail","tools":{}},"schema":"multiverso.dev/receipt/v0","world":"mv0:` + strings.Repeat("1", 64) + `"}`,
			wantDig:   "mv0:aafe85b2fc95c16ccfe726f0c0159a77cc3dc7ab841da602be098aea854c9672",
		},
		{
			// M1e/EP-2: a pytest-suite receipt carrying metrics and tools.
			// Metric names sort lexicographically like every other key, the
			// values are INTEGERS (coverage in basis points, durations in
			// ms — DP-1 forbids floats), and a metric whose source was
			// unavailable is simply absent: there is no coverage_bp here
			// because this run measured no coverage.
			name: "receipt object with metrics and tools",
			v: Receipt{
				Schema:    SchemaReceipt,
				World:     "mv0:" + strings.Repeat("1", 64),
				Oracle:    OracleRef{ID: "pytest-suite", Version: "v0", Config: "mv0:" + strings.Repeat("2", 64)},
				Execution: Execution{Argv: []string{"python3", "-m", "pytest", "--junit-xml=.mvo-oracle/pytest-suite/junit.xml", "-p", "no:cacheprovider"}, ExitCode: 0, DurationMS: 1234, IsolationTier: TierT0Worktree, IsolationCaps: HostCaps()},
				Result: Result{
					Status: "pass",
					Metrics: map[string]int64{
						"tests_total": 8, "tests_passed": 8, "tests_failed": 0,
						"tests_errored": 0, "tests_skipped": 0, "duration_ms": 132,
					},
					Tools:     map[string]string{"pytest": "9.1.1"},
					Artifacts: []string{"sha256:aa11", "sha256:bb22", "sha256:cc33", "sha256:dd44"},
				},
				Freshness:   Freshness{Basis: BasisConstruction, ValidFor: ValidFor{Tree: "git:89e6c98d92887913cadf06b2adb97f26cde4849b", Env: "mv0:" + strings.Repeat("3", 64)}},
				RecheckTier: "V1-replayable",
				Family:      "suite",
				// The scaling denominator that makes wall_ms learnable: 1300 ms
				// for 8 tests and 1300 ms for 800 are the same number and mean
				// opposite things (M2a decision 22). Correlation is declared
				// per KIND and recorded per receipt, and Decide never reads it.
				Cost:        Cost{WallMS: 1300, Units: 8, Unit: "tests"},
				Inputs:      NoInputs(),
				Correlation: Correlation{Signal: "test-outcomes", Generator: "repo", Executor: "candidate-process"},
				CreatedAt:   "2026-01-02T03:04:05Z",
			},
			wantCanon: `{"correlation":{"corpus":"","executor":"candidate-process","generator":"repo","signal":"test-outcomes"},"cost":{"unit":"tests","units":8,"wall_ms":1300},"created_at":"2026-01-02T03:04:05Z","execution":{"argv":["python3","-m","pytest","--junit-xml=.mvo-oracle/pytest-suite/junit.xml","-p","no:cacheprovider"],"duration_ms":1234,"evidence_plugin":"","evidence_regime":"","exit_code":0,"isolation_caps":{"cap_drop":"","cpu_milli":0,"memory_bytes":0,"network":"host","pids_limit":0,"read_only_root":false,"user":""},"isolation_tier":"T0-worktree"},"family":"suite","freshness":{"basis":"construction","valid_for":{"env":"mv0:` + strings.Repeat("3", 64) + `","tree":"git:89e6c98d92887913cadf06b2adb97f26cde4849b"}},"inputs":{},"oracle":{"config":"mv0:` + strings.Repeat("2", 64) + `","id":"pytest-suite","version":"v0"},"recheck_tier":"V1-replayable","result":{"artifacts":["sha256:aa11","sha256:bb22","sha256:cc33","sha256:dd44"],"detail":"","metrics":{"duration_ms":132,"tests_errored":0,"tests_failed":0,"tests_passed":8,"tests_skipped":0,"tests_total":8},"status":"pass","tools":{"pytest":"9.1.1"}},"schema":"multiverso.dev/receipt/v0","world":"mv0:` + strings.Repeat("1", 64) + `"}`,
			wantDig:   "mv0:c56ced3198de52e87b2c5f02597882da20db3007d6d921c3c417581c9dbf8b47",
		},
		{
			// M1c: full T1 caps — every field, canonical key order pinned.
			name: "isolation caps, full T1 record",
			v: IsolationCaps{
				CapDrop:      "ALL",
				CPUMilli:     1500,
				MemoryBytes:  512 << 20,
				Network:      NetworkNone,
				PidsLimit:    256,
				ReadOnlyRoot: true,
				User:         "501:20",
			},
			wantCanon: `{"cap_drop":"ALL","cpu_milli":1500,"memory_bytes":536870912,"network":"none","pids_limit":256,"read_only_root":true,"user":"501:20"}`,
			wantDig:   "mv0:23cd3394b6b7e0363c3ed57412dd867f28f6b4521140073395f892e517da6038",
		},
		{
			// HostCaps golden: the honest uncapped-bare-host record (PRD §9).
			name:      "host caps",
			v:         HostCaps(),
			wantCanon: `{"cap_drop":"","cpu_milli":0,"memory_bytes":0,"network":"host","pids_limit":0,"read_only_root":false,"user":""}`,
			wantDig:   "mv0:c90466e7b0182bf02de142ce987a44cb3c34f003b13948127ce3fe368bdcd419",
		},
		{
			name:      "policy object with empty slices",
			v:         Policy{Schema: SchemaPolicy, HardGates: []string{}, Ranking: []string{}},
			wantCanon: `{"hard_gates":[],"ranking":[],"schema":"multiverso.dev/policy/v0"}`,
			wantDig:   "mv0:8dbcd0a12b566d973499b1a96dd68b7a01d2ec7a1bf9f2df1527d30f87b6da16",
		},
		{
			// M1b: context/trace/cost are always serialized — no omitempty
			// games, optional fields would make digests ambiguous. M1e adds
			// patch_bytes, recorded where the size is known so the
			// patch_size_asc ranking key needs no CAS access.
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
				PatchBytes:    412,
				Trace:         "sha256:" + strings.Repeat("b", 64),
				Cost:          RunCost{WallMS: 1234, USDMicro: 4200, TokensIn: 1300, TokensOut: 345, Source: "client-estimate"},
				Outcome:       OutcomeCompleted,
				CreatedAt:     "2026-01-02T03:04:05Z",
			},
			wantCanon: `{"context":"sha256:` + strings.Repeat("a", 64) + `","cost":{"source":"client-estimate","tokens_in":1300,"tokens_out":345,"usd_micro":4200,"wall_ms":1234},"created_at":"2026-01-02T03:04:05Z","env":"mv0:` + strings.Repeat("5", 64) + `","intent":"mv0:` + strings.Repeat("4", 64) + `","isolation_tier":"T0-worktree","outcome":"COMPLETED","patch":"sha256:` + strings.Repeat("6", 64) + `","patch_bytes":412,"producer":{"adapter":"claude-code@v0","identity_tier":"claimed","model":"claude-sonnet-5","role":"generator"},"schema":"multiverso.dev/world/v0","trace":"sha256:` + strings.Repeat("b", 64) + `","tree":"git:89e6c98d92887913cadf06b2adb97f26cde4849b"}`,
			wantDig:   "mv0:5b79b3d7a180ef0ba34740074a094076450599f0190b3cd216101aadd8ea6787",
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

// TestTierAndNetworkConstants pins the isolation-tier and network-mode
// vocabulary (XP-1, NFR-4): the exact strings appear in world and receipt
// schemas and must never drift.
func TestTierAndNetworkConstants(t *testing.T) {
	tiers := map[string]string{
		TierT0Worktree:  "T0-worktree",
		TierT1Container: "T1-container",
	}
	for got, want := range tiers {
		if got != want {
			t.Errorf("tier constant = %q, want %q", got, want)
		}
	}
	networks := map[string]string{
		NetworkNone:    "none",
		NetworkDefault: "default",
		NetworkHost:    "host",
	}
	for got, want := range networks {
		if got != want {
			t.Errorf("network constant = %q, want %q", got, want)
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

// `Unit == "" iff Units == 0` is the invariant Cost documents, and it is the
// difference between "this rung scaled by nothing" and "we do not know what
// this rung scaled by" — decision 22's {0, ""} sentinel for UNKNOWN.
//
// It was violated on every machinery path in the mutation rung, which reads
// its unit count out of a metrics map it had just deleted the key from, and
// in the reducer when a cohort of two or more compared zero cases. A
// zero-unit sample with a named unit is not merely untidy: it enters M2b's
// least-squares fit at x = 0, which is exactly the intercept a scheduler
// reads as the kind's FIXED cost, and an errored receipt's wall time can be
// a whole baseline suite run.
func TestCostUnitIsEmptyIffUnitsIsZero(t *testing.T) {
	for _, tc := range []struct {
		name string
		cost Cost
		ok   bool
	}{
		{"a measured purchase", Cost{WallMS: 400, Units: 8, Unit: "tests"}, true},
		{"honest unknown", Cost{WallMS: 400}, true},
		{"a named unit with no count", Cost{WallMS: 400, Unit: "mutants"}, false},
		{"a count with no unit", Cost{WallMS: 400, Units: 8}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holds := (tc.cost.Unit == "") == (tc.cost.Units == 0)
			if holds != tc.ok {
				t.Errorf("Cost%+v: invariant holds = %v, want %v", tc.cost, holds, tc.ok)
			}
		})
	}
	// And the receipt golden above carries the legal shape, so the canonical
	// bytes this package pins can never be a counterexample.
	c := Cost{WallMS: 1300, Units: 8, Unit: "tests"}
	if (c.Unit == "") != (c.Units == 0) {
		t.Fatalf("the pinned receipt golden's cost %+v violates its own invariant", c)
	}
}
