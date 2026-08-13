# M1b — Agent Adapters: Design & Contracts

> Implements [PRD](../../PRD.md) **AG-1** (AgentAdapter, six operations, normalized failure taxonomy), **AG-2** (control-plane-primary budget enforcement + honest cost capture), **AG-3** (transcript capture into CAS), **AG-4** (control-plane-owned diff capture), **AG-5** (candidate diversity controls v0), and closes M0's `MaxCandidates` TODO (CP-2). Builds on [M0](M0.md) and [M1a](M1a-admit-signing.md); every prior contract stays in force unless amended here. Grounded in [research ch. 16](../../research/16-agent-integration-surfaces.md). Exit criterion: `scripts/accept.sh` passes end-to-end, including a script-adapter race (unchanged-behavior proof) and a fake-claude-code race whose captured patch the oracle gates.
>
> Everything here is v0 and may break until M1 exit. Requirement IDs (AG-x, CP-x…) refer to the PRD. **Stdlib only; `go.mod`/`go.sum` untouched.** Tests never invoke real agent CLIs — all adapter tests run against the fake fixtures in `testdata/fakeagent/`. Challenger role (**AG-6**) and the inbound MCP server (**AG-7**) are explicitly OUT (v1).

## Module layout (delta over M1a)

```
internal/agent/             AgentAdapter contract, shared subprocess runner, outcome taxonomy
internal/agent/agent.go     Adapter, RunSpec, Budget, Run (handle), Event, RunResult, New()
internal/agent/runner.go    shared subprocess machinery: process group, watchdog, tee, env base
internal/agent/usd.go       micro-USD parsing/formatting (integer arithmetic, no floats)
internal/agent/prompt.go    RenderPrompt — the normative prompt template (AG-5, AI-control rule)
internal/agent/diff.go      Diff — control-plane diff capture (AG-4)
internal/agent/script.go    script adapter (M0 behavior refactored INTO an adapter)
internal/agent/claudecode.go  claude-code adapter (argv builder + stream-json parser)
internal/agent/codex.go     codex adapter (argv builder + JSONL parser)
testdata/fakeagent/claude   executable bash fixture emitting recorded stream-json
testdata/fakeagent/codex    executable bash fixture emitting recorded codex JSONL
```

Amended (never rewritten): `internal/object` gains the outcome constants, `World.{Context,Trace,Cost}` and `RunCost`; `internal/gitx` gains `AddAll`/`DiffCached`; `internal/race` swaps its patch loop for the adapter loop and gains `Candidate`; `cmd/mvo/race.go` gains the agent flags; `cmd/mvo/worlds.go` gains a cost column; `scripts/accept.sh` gains the fake-agent steps.

## Resolved design decisions

1. **The six AG-1 operations collapse to one constructor, three handle methods, and two control-plane functions.** `Spawn` → `Adapter.Start(ctx, RunSpec) (Run, error)`; `Events` → `Run.Events() <-chan Event`; `Interrupt` → `Run.Interrupt()`; `Result` → `Run.Wait() (*RunResult, error)`. **`Diff` and `Transcript` are deliberately NOT adapter operations**: diff capture is `agent.Diff(worldDir, baseTree)` — one control-plane implementation for every adapter, so no adapter code path can ever supply its own diff (AG-4: never trust agent-reported diffs; the compiler enforces it) — and the transcript is the raw stdout byte stream captured by the shared runner and returned in `RunResult.Transcript` (AG-3: the control plane tees the pipe; the adapter never hands us "its" transcript). A handle (not six free functions) because spawn-to-result shares lifecycle state — the process group, the watchdog timer, the tee buffer — that must not be reachable as separable operations on a not-yet-started or already-reaped run.
2. **`Run` is a small interface, not a concrete struct.** claude-code and codex share one concrete subprocess runner (`*procRun`); the script adapter completes synchronously in-process (its only subprocesses are the same local `git` calls M0 made), which keeps the race tests hermetic and fast. The orchestrator sees only the four-method surface either way.
3. **Cost unit is integer micro-USD** (`usd_micro`, 1 USD = 1,000,000). DP-1/M0 forbid floats in canonical JSON; micro-USD makes Claude Code's smallest observed estimates (~10⁻⁴ USD) exactly representable with headroom, and sums without overflow (int64 caps at ~9.2 × 10¹² USD). Tool-reported decimal costs are parsed by decimal-string arithmetic (never `float64` on the primary path) and rounded **half-up at the 6th fractional digit**. Cost source is recorded honestly: `"client-estimate"` for anything a CLI reported about itself, `"none"` when nothing was reported (script worlds). No price table ships in M1b — codex reports tokens, not dollars, so codex worlds carry `usd_micro: 0` with non-zero tokens; that combination *is* the honest record (AG-2: cross-checking against provider usage APIs is eval-harness territory, M2).
4. **World production cost lives on the World, not in a Receipt.** Receipts are oracle evidence about a world; the generation run is not an oracle. `World` gains a `cost` sub-object (`RunCost`), leaving `object.Cost`, receipt digests, and M1a's attestation budget math untouched. The M2 scheduler prices generation from `World.Cost` and verification from `Receipt.Cost`.
5. **`World` gains `context`, `trace`, and `cost` as always-serialized fields** (PRD §5.2; no schema bump — v0 has room). No `omitempty` games: optional fields make digests ambiguous. Consequence: world digests change, so package goldens are re-derived, and **pre-M1b ledgers no longer replay** (decoded old worlds re-digest differently) — acceptable v0 breakage, same discipline as "may break until M1 exit". Convention stated once: fields referencing raw byte artifacts (`patch`, `context`, `trace`) hold CAS keys (`sha256:…`); fields referencing canonical objects hold `mv0:` digests.
6. **`World.Patch` is the control-plane-captured diff for every adapter — including script.** AG-4 admits no exceptions: after the run, `git add -A && git diff --binary --cached <base-tree>` in the world dir is the patch, period. The script adapter's *input* patch bytes are its prompt (decision 7) and survive as `World.Context`, so the acceptance script's world-by-patch-hash lookup keeps working via the context key. Staging first captures untracked files; diffing the index against the base tree is also robust against an agent that commits despite instructions (the index reflects final worktree content regardless of where HEAD moved).
7. **The script adapter's prompt IS the patch.** `RunSpec.Prompt` is always the literal text handed to the adapter: rendered prompt for agent CLIs, raw patch bytes (as a string) for script. This keeps `RunSpec` uniform, makes `World.Context = CAS key of the prompt bytes` meaningful for all adapters, and preserves M0 semantics exactly — the script adapter pipes the bytes to `git apply --index -` just as `race` did.
8. **Outcome precedence: the control plane's kill reason outranks anything the process said.** The runner pins a `killReason` (sync.Once) *before* signaling; the mapper consults it first: watchdog fired or ctx deadline exceeded → `BUDGET_EXCEEDED`; `Interrupt()` or ctx cancellation → `INTERRUPTED`. Only an unkilled run is classified from its event stream and exit code (per-adapter tables below). A run killed by the watchdog that still manages to flush a `success` result stays `BUDGET_EXCEEDED` — control-plane-primary means exactly this (AG-2).
9. **Watchdog kills the process group, gracefully then hard**: at `Budget.MaxWall`, SIGTERM to the pgid (Claude Code aborts the turn, runs SessionEnd, exits 143 — and gets a chance to flush its result event with cost), then SIGKILL to the pgid after a 5 s grace (`killGrace`, overridable in tests). Same `Setpgid` + `cmd.WaitDelay` pattern as M1a's oracle. If `Budget.MaxWall == 0` no per-run timer is armed, but the race-level ctx deadline (intent `max_wall_ms`) still applies and still maps to `BUDGET_EXCEEDED`.
10. **Agent failure is evidence; machinery failure is an error.** Mirrors `oracle.Run`: every terminal agent state — including `CRASH` and `CONFIG_ERROR` — yields a recorded World and the race continues (M0 already did this for failed applies). `Start`/`Wait` return a non-nil error only when the evidence itself could not be produced (invalid spec, pipe/CAS plumbing failure), which aborts the race like an oracle error does. To avoid burning N worlds discovering a missing CLI, `cmd/mvo` pre-flights `exec.LookPath` on the adapter binary before `race.started`.
11. **`Events()` is observability, the transcript is the record.** The runner tees every stdout byte into the transcript buffer unconditionally; the normalized `Event` channel is buffered (64) and drops under a slow consumer rather than ever blocking or truncating evidence. Tests assert against `RunResult` and the transcript, never against channel completeness. (The live scoreboard consumer arrives with M1c parallelism.)
12. **`--permission-mode bypassPermissions` for claude-code, deviating from ch. 16's suggested `dontAsk`.** In `-p` mode an unanswerable permission prompt is a crippled run, and `dontAsk` denies anything not pre-allowed — M1b curates no allowlists, so the candidate could not edit files. Worlds are control-plane-owned isolated worktrees whose isolation tier is *recorded, never assumed* (T0 in M1b; the real safety boundary is T1 containers, XP-1); the agent's output is untrusted by construction and gated by oracles. Codex gets the OS-level equivalent the task defines: `--sandbox workspace-write`.
13. **Candidate diversity v0 (AG-5) = per-world prompt variation line + model pool.** The template's first line names the candidate ordinal ("candidate {K} of {N}"), and `--model` accepts a comma-separated pool assigned round-robin across worlds. The ordinal line doubles as a world-digest uniqueness guarantee: two candidates that produce identical trees, transcripts, and costs still differ in `context`. This does **not** violate the AI-control rule — the ordinal reveals no sibling content, no policy, no gates, no scheduler state (see "Prompt template").
14. **Env allowlist, names-only.** The agent subprocess env = a fixed base set (`PATH, HOME, TMPDIR, USER, LANG, LC_ALL`, values from `mvo`'s own environment; unset names omitted) plus `RunSpec.Env` extras copied the same way (`--agent-env NAME[,NAME…]`). Nothing else leaks (NFR-4: no secret enters a world unless explicitly allowlisted). `HOME` passthrough is what lets locally-authenticated CLIs find their credentials; API-key vars (`ANTHROPIC_API_KEY`, `CODEX_API_KEY`) must be allowlisted explicitly. The allowlisted **names** (never values) are recorded in the `agent.started` event; folding them into the XP-3 env manifest is deferred (it would churn `EnvDigest`, which M1a's admission shares).
15. **CLI version capture is evidential-by-transcript in M1b.** `system/init` (claude-code) and session headers (codex) carry tool version info inside the captured transcript; a dedicated `cli_version` field on the World waits for receipt publication (FI-1), where AG-3's `(baseOid, patch, transcriptDigest, modelId, cliVersion)` binding is assembled. Spawning `claude --version` per race buys nothing the transcript doesn't already hold. Flag drift on unpinned CLIs is caught honestly by the `CONFIG_ERROR` mapping (non-zero exit before the init event).
16. **No native cap without a native flag; no meter kill without a price.** Native caps are passed where they exist (claude-code: `--max-budget-usd`, `--max-turns`); codex has none, so codex runs are watchdog-only — `MaxTurns`/`MaxUSDMicro` on a codex spec are recorded in `agent.started` but unenforceable natively, and the doc says so rather than pretending. An event-fed token meter that kills mid-run needs a price table we refuse to ship (decision 3); it arrives with the M2 scheduler.

## AgentAdapter (`internal/agent`) — AG-1

### Six operations → Go surface

| AG-1 operation | Go surface | Trust note |
|---|---|---|
| Spawn | `Adapter.Start(ctx, RunSpec) (Run, error)` | control plane builds argv/env |
| Events (stream) | `Run.Events() <-chan Event` | observability only (decision 11) |
| Interrupt | `Run.Interrupt()` | TERM group → grace → KILL group |
| Result | `Run.Wait() (*RunResult, error)` | outcome per mapping tables below |
| Diff | `agent.Diff(worldDir, baseTree)` — **not on the interface** | AG-4: control-plane-owned, one impl |
| Transcript | `RunResult.Transcript []byte` — captured by the runner's tee | AG-3: raw pipe bytes, verbatim |

### Contracts

```go
package agent

// Adapter turns a RunSpec into a running agent inside one world. It is the
// generator-side extension surface (PRD §9). Implementations: script,
// claude-code, codex.
type Adapter interface {
    ID() string      // "script" | "claude-code" | "codex"
    Version() string // OUR adapter contract version ("v0"), not the CLI's
    Start(ctx context.Context, spec RunSpec) (Run, error)
}

// New returns the adapter registered under name; unknown names error.
// Producer.Adapter is always ID()+"@"+Version() ("script@v0" — unchanged
// from M0 — "claude-code@v0", "codex@v0").
func New(name string) (Adapter, error)

// RunSpec is one candidate run. WorldDir is the agent's cwd and the only
// place it may write (enforced by isolation tier, recorded not assumed).
type RunSpec struct {
    WorldDir string   // absolute path to the world worktree
    Prompt   string   // literal prompt text; script: the patch bytes as a string
    Model    string   // model pin passed to the CLI; "" = tool default
    Budget   Budget
    Env      []string // extra parent env var NAMES to pass through (base set always included)
}

// Budget bounds one run (AG-2). Control-plane wall clock is primary; the
// other two are native caps forwarded where the tool supports them.
type Budget struct {
    MaxWall     time.Duration // watchdog: kill the process group at this age; 0 = no per-run timer
    MaxTurns    int           // native cap where supported; 0 = uncapped
    MaxUSDMicro int64         // native cap where supported, micro-USD; 0 = uncapped
}

// Run is a live (or, for script, already-finished) agent run.
type Run interface {
    // Events streams best-effort normalized events; closed when the run
    // ends. May drop under a slow consumer — the transcript is the record.
    Events() <-chan Event
    // Interrupt requests termination: SIGTERM to the process group, then
    // SIGKILL after the grace period. Wait then reports INTERRUPTED.
    // Idempotent; no-op after exit.
    Interrupt()
    // Wait blocks until the terminal state and returns the result.
    // Idempotent. error is reserved for evidence-collection failure
    // (decision 10); a crashed/killed/failed agent is a RunResult.
    Wait() (*RunResult, error)
}

// Event is one normalized stream event. Kind ∈ {"init","item","retry",
// "result","unknown"}; Raw is the verbatim line (also in the transcript).
type Event struct {
    Kind string
    Raw  json.RawMessage
}

// RunResult is the terminal record of one run.
type RunResult struct {
    Outcome    string         // object.Outcome* — exactly one of the six
    ExitCode   int            // process exit code; -1 when signaled / never ran
    KilledBy   string         // "watchdog" | "interrupt" | "" (native/self exit)
    Cost       object.RunCost // wall measured by the runner; usd/tokens parsed from the stream
    NumTurns   int            // turns reported/counted; 0 when unknown
    SessionID  string         // tool session/thread id when reported; ""
    Transcript []byte         // raw stdout event-stream bytes, verbatim (AG-3)
    Stderr     []byte         // raw stderr bytes
}

// Diff captures the world's changes as a binary patch (AG-4): git add -A,
// then git diff --binary --cached <baseTree>. It never consults adapter
// output. baseTree arrives "git:"-prefixed and is stripped here.
func Diff(worldDir, baseTree string) ([]byte, error)
```

### Shared runner (`runner.go`)

One implementation drives both subprocess adapters; each supplies an argv builder and a line parser (pure functions, unit-tested with goldens):

- **Spawn**: `exec.Command` plus an explicit ctx-watcher goroutine — CommandContext semantics implemented manually, because CommandContext's default `Cancel` would SIGKILL only the group leader without pinning a kill reason, contradicting decisions 8–9; the watcher pins `killReason` from `ctx.Err()` and takes the TERM → grace → KILL process-group path. `Dir = WorldDir`, `SysProcAttr{Setpgid: true}`, stdin = `/dev/null` (agents must never block on reads), env per decision 14.
- **Tee**: every stdout byte goes into the transcript buffer *before* parsing. The line parser reads the tee'd stream with a 10 MiB per-line cap; an oversized or unparseable line becomes one `unknown` event and parsing continues — the transcript still holds it byte-for-byte. Stderr is captured whole, unparsed.
- **Watchdog**: `time.AfterFunc(MaxWall)` → pin `killReason = "watchdog"` → SIGTERM pgid → after `killGrace` (5 s; test-overridable) SIGKILL pgid, **unconditionally on the group**: the leader dying to the initial SIGTERM (the common case — real CLIs exit promptly on it) must never cancel the escalation, or a grandchild that traps SIGTERM would survive the budget kill and keep spending; a dead group is a harmless ESRCH, and the escalation timer anchored at SIGTERM bounds the pid-reuse window to `killGrace`. `cmd.WaitDelay = 5s` bounds Wait if an escaped process holds the pipes (M1a oracle precedent). `ctx.Done()` takes the same path with `killReason` from `ctx.Err()` (`DeadlineExceeded` → budget, `Canceled` → interrupt).
- **No output size cap in M1b**: the wall cap bounds runaway streams in practice; an explicit cap (kill + honest truncation marker) is noted for v1.
- The script adapter bypasses the runner entirely: `Start` applies the patch synchronously via `gitx.Apply` and returns a completed `Run` (empty transcript, closed events channel).

## Outcome taxonomy — AG-1

The six outcomes move to `internal/object` (they are World-schema vocabulary; `object` has no deps):

```go
const (
    OutcomeCompleted      = "COMPLETED"
    OutcomeBudgetExceeded = "BUDGET_EXCEEDED"
    OutcomeInterrupted    = "INTERRUPTED"
    OutcomeConfigError    = "CONFIG_ERROR"
    OutcomeProviderError  = "PROVIDER_ERROR"
    OutcomeCrash          = "CRASH"
)
```

`internal/race` keeps `race.OutcomeCompleted`/`race.OutcomeConfigError` as aliases (M0 API compatibility). `race.Decide` needs no change: non-`COMPLETED` worlds get no oracle run, therefore no suite receipt, therefore fail `suite-pass` — the existing `failReason` prints `outcome=BUDGET_EXCEEDED` etc. generically.

**Rule 0 for every adapter (decision 8), evaluated before the tables below**: `killReason == "watchdog"` or ctx deadline exceeded → `BUDGET_EXCEEDED`; `Interrupt()` called or ctx canceled → `INTERRUPTED`. What follows classifies only runs the control plane did not kill.

### claude-code mapping (first matching row wins)

| # | Condition | Outcome |
|---|---|---|
| 1 | in-run `exec` failure (binary vanished, bad flags: process exited non-zero **and** no `system`/`init` event was parsed) | `CONFIG_ERROR` |
| 2 | `result` event, `subtype == "success"`, `is_error` false, exit 0 | `COMPLETED` |
| 3 | `result` event, `subtype == "error_max_turns"` or any subtype with prefix `"error_max_"` (native cap fired: `--max-turns`, `--max-budget-usd`; the flag-derived subtype family, prefix-matched to survive drift) | `BUDGET_EXCEEDED` |
| 4 | `result` event with `is_error` true (e.g. `error_during_execution`) **and** the stream carried a `system`/`api_retry` event whose `error` category ∈ {`rate_limit`, `overloaded`, `billing_error`} — or the result error text matches `(?i)rate.?limit\|overloaded\|billing` | `PROVIDER_ERROR` |
| 5 | `result` event with `is_error` true, no provider signal | `CRASH` |
| 6 | exit non-zero after `system`/`init`, no `result` event | `CRASH` |
| 7 | exit 0 without a `result` event, or `success` result with non-zero exit (stream contract violated; honesty over optimism) | `CRASH` |

### codex mapping (first matching row wins)

| # | Condition | Outcome |
|---|---|---|
| 1 | process exited non-zero **and** no `thread.started` event was parsed (bad flags, missing auth, missing binary at exec time) | `CONFIG_ERROR` |
| 2 | exit 0, ≥1 `turn.completed`, no `turn.failed` | `COMPLETED` |
| 3 | `turn.failed` whose error text matches `(?i)rate.?limit\|overloaded\|quota\|insufficient_quota\|429` | `PROVIDER_ERROR` |
| 4 | `turn.failed` otherwise | `CRASH` |
| 5 | exit non-zero after `thread.started`, no `turn.failed` | `CRASH` |
| 6 | exit 0 without any `turn.completed` | `CRASH` |

(No native-cap row: codex has no caps — watchdog-only, decision 16.)

### script mapping

| # | Condition | Outcome |
|---|---|---|
| 1 | ctx canceled before/while applying | `INTERRUPTED` (deadline → `BUDGET_EXCEEDED`) |
| 2 | `git apply --index` succeeds | `COMPLETED` |
| 3 | patch empty/unreadable or apply conflict | `CONFIG_ERROR` (exact M0 behavior) |

`PROVIDER_ERROR`, `CRASH`, and `BUDGET_EXCEEDED`-by-watchdog are unreachable for script in practice and never synthesized.

## Adapters

### claude-code (`claudecode.go`)

Argv (order fixed; budget flags appended only when the corresponding limit > 0):

```
claude -p <prompt> --output-format stream-json --bare
       --permission-mode bypassPermissions
       [--model <model>]
       [--max-turns <n>]
       [--max-budget-usd <decimal>]        // FormatUSDMicro, e.g. 250000 → "0.25"
```

`--bare` keeps the run hermetic (no hooks/skills/CLAUDE.md auto-discovery — ch. 16 calls it "the recommended mode for scripted calls"); `bypassPermissions` per decision 12. No MCP flags in M1b (AG-7 is out).

Stream-json parsing (NDJSON; every line lands in the transcript first):

- `{"type":"system","subtype":"init",…}` → `Event{Kind:"init"}`; marks the process as launched-clean (mapping row 1 threshold). Reported model/version stay evidential in the transcript (decision 15).
- `{"type":"system","subtype":"api_retry","error":…}` → `Event{Kind:"retry"}`; error category remembered for mapping row 4.
- `{"type":"result",…}` → `Event{Kind:"result"}`; terminal. Harvest: `total_cost_usd` (as `json.Number` → decimal-string parse → half-up micro-USD; exponent forms fall back to `strconv.ParseFloat` + `math.Round(f*1e6)`), `usage` → `tokens_in = input_tokens + cache_creation_input_tokens + cache_read_input_tokens`, `tokens_out = output_tokens` (missing fields are 0), `num_turns`, `session_id`. `Cost.Source = "client-estimate"` whenever a result event carried cost or usage.
- `{"type":"assistant"|"user",…}` → `Event{Kind:"item"}`.
- Anything else — unknown `type`, unparseable line — → `Event{Kind:"unknown"}`, tolerated, never fatal.

Malformed or negative cost reports → `usd_micro: 0` (absence over garbage); the raw claim survives in the transcript.

### codex (`codex.go`)

Argv:

```
codex exec --json --sandbox workspace-write [-m <model>] <prompt>
```

cwd is pinned to the world dir (worlds are git worktrees, so codex's git-repo check passes; `-C` would only duplicate cwd with a nondeterministic temp path in argv). `--ephemeral` is deliberately not passed: session files enable `codex exec resume` for REPAIR (v1). `CODEX_API_KEY` reaches the process only via the env allowlist.

JSONL parsing: `thread.started` → `init` (+ `thread_id` → `SessionID`); `turn.started`/`item.started`/`item.completed` → `item`; `turn.completed` → `item`, accumulating `usage`: `tokens_in += input_tokens + cached_input_tokens`, `tokens_out += output_tokens`, `NumTurns++`; `turn.failed` → `result` (error remembered for mapping); unknown → `unknown`, tolerated. `Cost.Source = "client-estimate"` when any usage was reported; `usd_micro` stays 0 (decision 3).

### script (`script.go`)

M0's behavior, verbatim, behind the interface: `Start` reads `spec.Prompt` as patch bytes and runs `gitx.Apply(worldDir, patch)` (`git apply --index`, atomic on conflict). `COMPLETED` on success, `CONFIG_ERROR` on failure — no receipts for failed worlds, race continues, exactly as M0 (CP-3). Transcript = empty byte stream (its CAS key is the well-known empty-bytes key — "the captured stream was empty" is the honest record), `Cost = {wall_ms: measured, usd_micro: 0, tokens: 0, source: "none"}`, events channel closed-empty. Race tests stay hermetic: no processes beyond local `git`.

## Micro-USD (`usd.go`) — AG-2, decision 3

```go
// ParseUSDMicro parses a CLI-flag decimal ("0.25", "1", "0.0042") into
// micro-USD. Strict: ^\d+(\.\d{1,6})?$ — more than 6 fractional digits or
// signs/exponents are a usage error. Integer arithmetic only.
func ParseUSDMicro(s string) (int64, error)

// FormatUSDMicro renders micro-USD as the shortest exact decimal for
// native flags: 250000 → "0.25", 1000000 → "1", 4200 → "0.0042".
func FormatUSDMicro(m int64) string
```

Round-trip law: `ParseUSDMicro(FormatUSDMicro(m)) == m` for all m ≥ 0. Tool-*reported* numbers (not flags) are parsed leniently with half-up rounding at 6 places: `"0.0031415"` → `3142`.

## Diff capture (`internal/gitx` additions) — AG-4

```go
func AddAll(dir string) error
    // git add -A (stages untracked files; idempotent)
func DiffCached(dir, baseTree string) ([]byte, error)
    // git diff --binary --cached <bare tree sha>; returns RAW stdout bytes —
    // no trimming (the trailing newline is significant to git apply).
    // Own exec path with bytes.Buffer: gitx.run()'s TrimSpace would corrupt
    // patches. baseTree accepted with or without TreePrefix.
```

`agent.Diff` = `AddAll` then `DiffCached`. `.multiverso/` is git-ignored since M0's init, so `add -A` never stages the workspace. An empty diff is legal (agent did nothing): the world records the base tree and the empty-bytes patch key; a `diff-nonempty` hard gate is policy territory (v1) — the oracle judges the tree either way. Same conventions as all gitx: hooks disabled, `gitEnv()`, stderr folded into errors. `Apply`, `WriteTree`, and every M0/M1a function are unchanged.

**AG-4 hardening (the world's git state is agent-writable after the run):** before capture, the orchestrator verifies the world's git identity is still the control plane's — `git rev-parse --git-common-dir` in the worktree must resolve to `cfg.Repo`'s own `.git` — because the worktree's `.git` pointer is a plain file the agent can repoint at a fabricated repository; a mismatch (or an unreadable identity) records the world as `CRASH` instead of trusting agent-chosen "evidence". Every gitx command additionally runs with `-c core.fsmonitor=false -c core.excludesFile=/dev/null` (no config-driven command execution, no ambient user-level ignore rules shaping capture). **Known residual gap:** an agent-*written* in-tree `.gitignore` can hide files the agent created from `git add -A`, so the captured patch/tree may omit worktree content the race oracle saw. The impact is confined to race-level evidence — `admit` re-applies the patch and re-runs the gate on the exact landing tree, so nothing hidden can land — and an ignored-files cross-check (`git status --ignored` against the captured tree) is v1 territory.

## World object extension (`internal/object`) — PRD §5.2, AG-2/3

```go
// RunCost accounts what producing a world cost (AG-2). All fields always
// serialized; usd_micro is integer micro-USD (decision 3).
type RunCost struct {
    WallMS    int64  `json:"wall_ms"`
    USDMicro  int64  `json:"usd_micro"`
    TokensIn  int64  `json:"tokens_in"`
    TokensOut int64  `json:"tokens_out"`
    Source    string `json:"source"` // "none" | "client-estimate"
}

type World struct {
    Schema        string   `json:"schema"`         // multiverso.dev/world/v0 (unchanged)
    Intent        string   `json:"intent"`
    Tree          string   `json:"tree"`
    Env           string   `json:"env"`
    IsolationTier string   `json:"isolation_tier"` // "T0-worktree" in M1b
    Producer      Producer `json:"producer"`       // adapter "claude-code@v0" etc., model = pinned model
    Context       string   `json:"context"`        // NEW: CAS key of the prompt bytes (DP-3)
    Patch         string   `json:"patch"`          // CAS key of the CAPTURED diff (decision 6)
    Trace         string   `json:"trace"`          // NEW: CAS key of the raw transcript (AG-3)
    Cost          RunCost  `json:"cost"`           // NEW: production cost (AG-2)
    Outcome       string   `json:"outcome"`        // full six-value taxonomy now
    CreatedAt     string   `json:"created_at"`
}
```

`Producer.Model` records the *pinned* model string from the spec (what we asked for); the model the tool claims it used is evidential in the transcript. `context` and `trace` payloads live only in the local CAS and are never published (DP-3; FI-1's publication surface excludes them). Consequences of the field additions: `internal/object` golden vectors for World are re-derived; every test constructing a World sets the new fields; audit of pre-M1b ledgers reports replay mismatches (decision 5) — new ledgers replay bit-for-bit as before.

## Prompt template (`prompt.go`) — AG-5, AI-control rule

```go
// RenderPrompt builds the world-scoped prompt: variation header + task.
// k is the 1-based candidate ordinal, n the candidate count. task is the
// operator override (--prompt/--prompt-file); "" renders the intent spec.
func RenderPrompt(spec object.Spec, k, n int, task string) string
```

Normative template (byte-exact; `{K}`/`{N}`/`{TASK}` substituted):

```
You are candidate {K} of {N}, working alone in an isolated git worktree.

# Task
{TASK}

# Rules
- Modify files only inside the current directory.
- Do not commit, branch, push, or otherwise drive git; the control plane
  captures your working-tree changes when you exit.
- Do not read or modify .git or .multiverso.
- When the task is done, stop; do not wait for further input.
```

`{TASK}` = the `--prompt`/`--prompt-file` text when given, else `"Title: {spec.Title}\n\n{spec.Description}"`.

**AI-control rule (PRD AG-7 note, ch. 13), normative**: the rendered prompt NEVER contains policy digests, hard gates, ranking keys, oracle commands, sibling-world content or status, scheduler state, or budget internals beyond what the tool's own flags already expose. The generator is untrusted; anything it learns about the gate is a gaming surface. A unit test renders the prompt for a race and asserts none of (policy digest, `hard_gates` values, oracle argv) appear. The candidate ordinal is the AG-5 v0 variation hook (decision 13) and stays; richer variation strategies (temperature-free prompt perturbation, model-pool weighting) are M2 scheduler territory.

## Race orchestrator changes (`internal/race`) — CP-2, CP-3

```go
// Candidate is one world's generation spec.
type Candidate struct {
    Prompt string       // literal prompt text (script: patch bytes as string)
    Model  string
    Budget agent.Budget
    Env    []string     // extra parent env var NAMES passed through (decision 14)
}

type Config struct {
    Repo       string
    Ledger     *ledger.Ledger
    CAS        *cas.Store
    Intent     string
    Adapter    agent.Adapter // NEW, required
    Candidates []Candidate   // NEW, required, len ≥ 1; replaces PatchesDir
    WorldsDir  string
    Oracle     oracle.Oracle
    KeepWorlds bool
}
```

`PatchesDir` moves to the CLI: `cmd/mvo` builds `Candidates` (from patch files for script; from N × rendered prompts for agents). **CP-2 enforcement (closes the M0 TODO)**: `len(Candidates) > intent.Budget.MaxCandidates` → error before `race.started`.

Run sequence per candidate k (sequential — parallelism is M1c):

1. `gitx.AddWorktree` at `intent.Base.Commit` (unchanged).
2. `contextKey := CAS.Put([]byte(cand.Prompt))`; append **`agent.started`**.
3. `h := cfg.Adapter.Start(ctx, agent.RunSpec{WorldDir, Prompt, Model, Budget, Env})`; `res := h.Wait()`. Errors abort the race (decision 10); outcomes never do.
4. `patch := agent.Diff(dir, intent.Base.Tree)`; `patchKey := CAS.Put(patch)`.
5. `tree := gitx.WriteTree(dir)` — always the *actual* post-run tree (for a failed script apply, `git apply --index` is atomic, so this equals the base tree — M0-equivalent).
6. `traceKey := CAS.Put(res.Transcript)`; `stderrKey := CAS.Put(res.Stderr)`; append **`agent.finished`**.
7. `env := EnvDigest(CAS, dir)` (unchanged); build the World (fields above, `Outcome: res.Outcome`); record **`world.created`**.
8. Oracle runs only in `COMPLETED` worlds (unchanged M0 rule); receipts, decision, `race.finished`, cleanup — all unchanged.

**Post-run capture failures are the world's evidence, not the race's error** (decision 10 applied to steps 4–7): the worktree is the agent's write surface, so when the world's git identity check, `agent.Diff`, or `gitx.WriteTree` fails after the run, the world is recorded with `Outcome: CRASH`, the error text appended to its stderr CAS artifact, and the race continues; `agent.finished` keeps the run's *own* outcome (both facts are honest). `EnvDigest` hashes only plain, readable lockfiles under a size cap — a writer-less FIFO or a device symlink at a lockfile name is skipped, never opened (it would hang or read unboundedly) — so an `EnvDigest` error is always a control-plane failure. Only control-plane failures (CAS, ledger) abort the race, and a damaged worktree that `git worktree remove` refuses to delete is cleaned up by plain removal + `git worktree prune` rather than failing an already-recorded race.

`race.started` body becomes `{"adapter": "script@v0", "candidates": <int>, "intent": "mv0:…"}` (the old `patches` filename list is gone; world order is evident from `world.created` order, and filenames were never replay inputs). Audit reads only `intent` from it — unaffected.

## Ledger event types (M1b additions)

Observational events: they carry cost/debug state, **no replay semantics** — `race.Decide`/`admit.Decide` replay inputs are unchanged, and `mvo audit` ignores these types (the hash chain still covers them). Bodies are canonical-JSON maps, all keys always present. Correlate `started`/`finished` by (intent, ordinal) within a race window.

| Type | Payload |
|---|---|
| `agent.started` | `{"adapter": "claude-code@v0", "budget": {"max_turns": 8, "max_usd_micro": 250000, "max_wall_ms": 60000}, "context": "sha256:…", "env": ["FAKE_AGENT_MODE"], "intent": "mv0:…", "model": "…", "ordinal": 1}` |
| `agent.finished` | `{"context": "sha256:…", "exit_code": 0, "intent": "mv0:…", "killed_by": "", "num_turns": 3, "ordinal": 1, "outcome": "COMPLETED", "session": "…", "stderr": "sha256:…", "tokens_in": 1200, "tokens_out": 345, "transcript": "sha256:…", "usd_micro": 4200, "wall_ms": 1234}` |

A `BUDGET_EXCEEDED` `agent.finished` is CP-2's "hard-stop with a ledger event" for the per-world budget; `killed_by` distinguishes the control-plane watchdog (`"watchdog"`) from native caps (`""` — the tool exited itself). A crash between `agent.started` and `world.created` leaves an incomplete world; audit tolerates it exactly like a crashed race.

## CLI (`cmd/mvo`)

```
mvo race <intent-digest> [--agent script|claude-code|codex] --oracle-cmd CMD
         [--keep-worlds] [--dir DIR]
    script (default):
         --patches DIR                       required; candidates = patch files, sorted
    claude-code | codex:
         [--prompt TEXT | --prompt-file P]   task override (default: intent spec)
         [--model NAME[,NAME...]]            model pool, round-robin (AG-5)
         [--candidates N]                    default intent.budget.max_candidates
         [--max-usd USD]                     per-world, decimal ≤6 dp → micro-USD
         [--max-turns N]                     per-world
         [--max-wall-ms MS]                  per-world watchdog; default intent.budget.max_wall_ms
         [--agent-env NAME[,NAME...]]        extra env passthrough (names only)
```

Flag discipline (usage errors, exit 2): `--patches` with an agent adapter; any agent flag with `--agent script`; both `--prompt` and `--prompt-file`; `--candidates` exceeding `intent.Budget.MaxCandidates`; malformed `--max-usd`. Script candidate count = patch-file count, now also capped by `MaxCandidates` (CP-2). Budget flags map to `agent.Budget` verbatim; `--max-wall-ms 0` is a usage error for agent adapters (an uncapped paid agent run is never launched implicitly — pass the intent's own bound explicitly if that is intended by design; the default already is the intent bound). Pre-flight: `exec.LookPath` on the adapter binary (decision 10). `mvo race` stdout contract is unchanged (`SELECT <world> (decision …, N worlds)`); costs are visible via `mvo worlds`, which gains a `usd_micro` column sourced from `World.Cost` (NFR-5). Intent objects are untouched — an intent-level total-USD budget arrives with the M2 scheduler.

`usage()` gains the agent flags on the `race` line. Everything else (`init`, `intent`, `worlds`, `explain`, `admit`, `verify`, `audit`) is unchanged.

## Fake agent fixtures (`testdata/fakeagent/`) — the test matrix

Two executable bash scripts named exactly `claude` and `codex` (PATH override finds them; the executable bit is committed). Rules: **never call a real agent or the network**; mode via `FAKE_AGENT_MODE` env (reaches them through the allowlist — itself a test of decision 14); argv is ignored entirely (argv construction is covered separately by pure-function goldens per adapter); all event lines are recorded/realistic shapes for their CLI.

| `FAKE_AGENT_MODE` | claude fixture behavior | codex fixture behavior | Expected outcome |
|---|---|---|---|
| `happy` (default) | init → edit cwd (see below) → assistant → result `success`, `total_cost_usd: 0.0042`, usage, `num_turns: 3`; exit 0 | thread.started → item.completed → edit cwd → turn.completed with usage; exit 0 | `COMPLETED`, real diff |
| `cost-report` | as happy but `total_cost_usd: 0.0031415`, usage `{input_tokens:1200, cache_read_input_tokens:100, output_tokens:345}` | as happy but usage `{input_tokens:1200, cached_input_tokens:100, output_tokens:345}` | `COMPLETED`; goldens: claude `usd_micro==3142` (half-up), `tokens_in==1300`, `tokens_out==345`; codex `usd_micro==0`, same tokens, source `client-estimate` |
| `slow` | init → spawn `sleep 60` child → wait | same | watchdog fires → `BUDGET_EXCEEDED`, `killed_by=="watchdog"`, whole process group dead (child too) |
| `bad-exit` | init → assistant → exit 3, no result | thread.started → exit 3, no turn.failed | `CRASH` |
| `garbage-output` | init → non-JSON lines + unknown-type JSON → exit 0, no result | same shapes | unknown events tolerated; `CRASH` (no terminal event on a zero exit); transcript preserves garbage byte-for-byte |
| `native-cap` | init → result `subtype: "error_max_turns"`, exit 1 | n/a (codex has no caps; mode behaves as happy) | claude: `BUDGET_EXCEEDED`, `killed_by==""` |
| `provider-error` | init → api_retry `{error: "rate_limit"}` → result `error_during_execution`, `is_error: true`, exit 1 | thread.started → turn.failed `{error: {message: "rate limit exceeded"}}`, exit 1 | `PROVIDER_ERROR` |
| `usage-error` | usage text on stderr, exit 2, **no stdout events** | same | `CONFIG_ERROR` (no init event) |

Edit-cwd behavior (shared helper inside each script): if `stats.py` exists in cwd, rewrite it with the fixed `mean()` (semantically patch-a's change — this is what makes the acceptance race's captured patch real and oracle-gated); otherwise write `AGENT_TOUCH.txt` containing `fake agent was here` (unit tests in bare temp worktrees still get a non-empty diff). `CONFIG_ERROR`-by-missing-binary is additionally covered by a unit test whose PATH lacks the fixture; `INTERRUPTED` by calling `Interrupt()`/canceling ctx against `slow`.

## Live smoke test (optional, never CI)

One test per subprocess adapter in `internal/agent/live_test.go`, running only when `MVO_LIVE_AGENT_TEST=1` (else `t.Skip` — the default everywhere, including CI; real CLIs cost money). When opted in, a missing CLI is a test *failure* (explicit opt-in must not silently no-op). Claude: temp git repo, prompt "Create a file named HELLO.txt containing exactly: hello", `--max-turns 2`, `--max-budget-usd 0.05`, wall 120 s; asserts `COMPLETED`, non-empty diff, `usd_micro > 0`, non-empty transcript. Codex equivalent with token assertions. These tests document flag reality drift (decision 15): if a pinned flag vanished upstream, the live test fails with `CONFIG_ERROR` while the fake matrix stays green.

## Acceptance script (CI runs this)

`scripts/accept.sh` — M1a's ten steps kept intact with two amendments and one insertion (renumbered):

1–2. Unchanged, except step 2's race now passes `--agent script` explicitly — the refactor-proof: the *entire* remaining pipeline (explain, admit, verify, audit, both tampers) must pass unchanged on the adapter-refactored engine.
3. Unchanged — the world-by-patch-hash sqlite lookup keeps working because the script world's `context` field carries the same CAS key the `patch` field used to (decision 7); the `LIKE '%$PATCH_A_KEY%'` match is oblivious to which field holds it.
3b. **Fake-agent race** (inserted before admit, while the base commit still carries the bug so the fake's fix produces a real diff):
   ```bash
   INTENT2="$("$MVO" intent new --dir "$REPO" --title "fix mean() via agent" --desc "agent race")"
   PATH="$ROOT/testdata/fakeagent:$PATH" FAKE_AGENT_MODE=happy \
     "$MVO" race "$INTENT2" --dir "$REPO" --agent claude-code --candidates 2 \
       --model fake-model --max-usd 0.25 --max-turns 8 --max-wall-ms 60000 \
       --agent-env FAKE_AGENT_MODE --oracle-cmd "python3 -m pytest -q"
   ```
   Assertions (via `mvo explain`, sqlite + python3 JSON over `world.created` payloads, and the CAS):
   - decision is `SELECT`;
   - each world: `outcome == "COMPLETED"`, `cost.usd_micro == 4200`, `cost.source == "client-estimate"`;
   - the winner's `patch` CAS object is non-empty and contains `diff --git` (a real captured patch — which the passing suite receipt proves the oracle gated);
   - the winner's `trace` CAS object is non-empty (first line contains `"type"`).
4–10. Unchanged (admit intent 1, trailer, verify ×3, second-machine audit — which now also replays the fake race's SELECT — bundle tamper, ledger tamper, final clean audit). The step-7 audit assertion additionally checks `decisions ≥ 3`.

## Testing bar

- `internal/agent`: outcome-mapping table tests per adapter driven by the fake fixtures (every row of every table, including rule 0 precedence: watchdog kill beats a flushed `success` result); watchdog kills the whole process group (`slow` child dies; asserted via pgid probe); `Interrupt()` and ctx-cancel → `INTERRUPTED`, ctx-deadline → `BUDGET_EXCEEDED`; argv-builder goldens per adapter (budget flags appear iff limits set; `FormatUSDMicro` values exact); stream parsers tolerate unknown/garbage/oversized lines with the transcript byte-identical to fixture output; `ParseUSDMicro`/`FormatUSDMicro` round-trip + rejection tables; half-up rounding golden (`0.0031415` → 3142); token accumulation goldens; `RenderPrompt` goldens + the AI-control negative test (no policy digest, gate names, or oracle argv in any rendered prompt); env allowlist test (unlisted parent vars absent from the child env; base set present); `Diff` captures untracked files and survives an in-world commit; script adapter reproduces M0 apply/CONFIG_ERROR behavior byte-for-byte.
- `internal/gitx`: `AddAll`/`DiffCached` lifecycle on a temp repo; `DiffCached` output is raw (trailing newline preserved) and `git apply`-able onto the base tree, binary file included.
- `internal/object`: World/RunCost golden canonical bytes + digest stability re-derived; outcome constants exhaustive.
- `internal/race`: orchestrator with the script adapter reproduces M0's decision goldens (re-derived digests); `MaxCandidates` enforcement; a mixed race (one `COMPLETED`, one `CRASH` world) decides correctly with the crash recorded as evidence; event order per world (`agent.started` → `agent.finished` → `world.created`).
- `cmd/mvo`: flag-matrix usage errors; `mvo worlds` cost column; pre-flight LookPath error message.
- `gofmt -l` clean, `go vet ./...` and `go test ./...` pass; `scripts/accept.sh` is the e2e test and runs in CI. `go.mod`/`go.sum` unchanged. No test ever invokes a real agent CLI (`MVO_LIVE_AGENT_TEST` gates the only exception, off by default).

## Explicitly NOT in M1b

Challenger role (AG-6), inbound MCP server / `--strict-mcp-config` injection (AG-7), parallel world execution and the live scoreboard (M1c), event-fed token-meter kills and any price table (M2 scheduler), provider-usage-API cost reconciliation (AG-2 eval, M2), T1 container isolation for agent runs (XP-1 T1 — worlds stay T0 here), intent-level total-USD budgets, OpenHands/Aider/opencode/Jules adapters, resume/`--fork-session` REPAIR loops (v1), a `cli_version` World field (FI-1), transcript size caps, `mvo evidence` verb, `--dry-run` pricing (NFR-5 full), prompt/context publication controls beyond the local CAS (DP-3 is satisfied locally), Windows.
