# M1e — Versioned Policy & the Real Oracle Ladder: Design & Contracts

> Implements [PRD](../../PRD.md) **CP-5** to its full text (policy as a versioned artifact: ordered hard gates naming an oracle and a required freshness basis, lexicographic ranking spec, escalation rules), **EP-1** to its full text (the v0 Python oracle ladder — **O0** `pytest --collect-only` with collected-test counts, **O1** suite gate with JUnit XML + reportlog + coverage — alongside the generic `CommandOracle`), **EP-2** (receipts carry metrics), **EP-7** (native artifacts content-addressed *before* parsing), and the remainder of **CP-6** (`mvo explain` renders *why* the winner won, key by key; ESCALATE as a first-class race outcome with the CP-6 payload). Builds on [M0](M0.md), [M1a](M1a-admit-signing.md), [M1b](M1b-agent-adapters.md), [M1c](M1c-containers-parallel.md), and [M1d](M1d-publication.md); every prior contract stays in force unless amended here. Grounded in [research ch. 19](../../research/19-mvp-oracle-toolchain.md) (verified tool versions; the exit-code-5 laundering vector). Exit criterion: `scripts/accept.sh` passes end-to-end, including a lexicographic race decided by the **second** ranking key, a test-deleting candidate stopped by the collected-count guard, an ESCALATE-on-tie race, a rejected policy file, and an audit that replays all of it.
>
> This is the last code block of M1. Requirement IDs (CP-x, EP-x…) refer to the PRD. **Stdlib only; `go.mod`/`go.sum` untouched.** Python tooling (pytest, coverage.py, plugins) is invoked by oracles **at runtime, inside worlds**; no test may require a plugin to be installed — parser tests run against committed fixtures, and end-to-end steps degrade honestly when a plugin is absent. Tests never invoke real agent CLIs (M1b rule).

## Module layout (delta over M1d)

```
internal/policy/             the policy plane: wire decode → validate → compile
internal/policy/policy.go    compiled Policy/Gate/Key/Escalation/Oracle + Decode/Load
internal/policy/vocab.go     the CLOSED vocabularies: gates, ranking keys, bases, oracle
                             kinds + their metric vocabularies (validation's source of truth)
internal/policy/compile.go   v0 → compiled (legacy dialect), v1 → compiled; Default/CommandPolicy
internal/oracle/pytest.go    pytest-collect + pytest-suite oracles (argv, probe, run, parse)
internal/oracle/junit.go     JUnit XML → metrics (encoding/xml, stdlib)
internal/oracle/reportlog.go pytest-reportlog JSONL → first-run metrics (EP-6 scaffold)
internal/oracle/coverage.go  coverage.py JSON → coverage_bp (integer line counts, no floats)
internal/oracle/registry.go  New(spec) → Oracle; kind → implementation; family assignment
internal/race/baseline.go    base-state collect measurement (collected_delta's denominator)
internal/race/trace.go       ranking comparison trace (pure; explain's engine)
cmd/mvo/policy.go            mvo policy list|show|validate|use
testdata/toyrepo/patches-rank/     patch-a, patch-c    (ranking decided by key 2)
testdata/toyrepo/patches-launder/  patch-a, patch-cut, patch-wipe (O0 guard)
testdata/toyrepo/patches-tie/      patch-x, patch-y    (tie on every key → ESCALATE)
testdata/toyrepo/policies/         rank-two-keys.json, tie-escalate.json,
                                   legacy-v0.json, bad-gate.json
testdata/oracle/                   recorded junit.xml / reportlog.jsonl / coverage.json
                                   fixtures for the pure parsers (no plugin required)
```

Amended (never rewritten): `internal/object` gains `PolicyV1`, `Result.Metrics`, `Result.Tools`, `World.PatchBytes`, `RecordedWorld`/`RecordedReceipt`, freshness-basis constants; `internal/race` gains the ladder, the baseline, and new `Decide` inputs; `internal/admit` runs the policy's landing oracles and takes plural gate receipts; `internal/oracle` gains the registry and the pytest kinds; `internal/workspace` writes a v1 default policy and gains policy resolution; `cmd/mvo/{main,state,explain,race,intent,audit,verify}.go` gain the verb, the flags, the renderer, and the amended replay; `internal/publish/fetch.go` decodes policies through `internal/policy`; `scripts/accept.sh` gains five steps.

## Resolved design decisions

1. **Digests are never recomputed from decoded objects.** M0's `Decide` re-serialized every receipt and the policy to derive the evidence list and `decision.policy`. That silently made *every* schema addition a replay break (M1b and M1c both had to sanction "pre-milestone ledgers no longer replay"). M1e ends it: `Decide` takes `object.RecordedWorld`/`object.RecordedReceipt` — the decoded object **paired with the digest under which it was recorded** — and the compiled policy carries the digest of the bytes it was loaded from. Every caller already has those digests (the race recorder returns them, `mvo audit` reads `payload_dig`, `publish`/`fetch-race` verify them against filenames). The immediate payoff is the compatibility rule this milestone owes: **M1e adds fields to `Receipt.Result` and `World` and still replays pre-M1e ledgers byte-for-byte**, because nothing about an old decision is re-derived from a re-serialization of its inputs. Round-tripping a canonical object through a newer Go struct is not a proof of anything; the recorded bytes are.
2. **Policy is versioned by schema (`multiverso.dev/policy/v1`) *and* the v0 shape is deterministically upgraded in memory — never on disk.** Both horns of the fork, each where it belongs. On the wire there are two frozen shapes; in memory there is one compiled `policy.Policy`. A v0 object (`{schema, hard_gates:[string], ranking:[string]}`) compiles to the same in-memory kind of value a v1 object does, so `Decide` has exactly one code path. Nothing rewrites a v0 policy's bytes: its digest is what old intents pinned and what old attestations name, and an "upgrade on read, store the upgrade" design would either fork the digest (breaking the pin) or mutate a content-addressed object (forbidden). The compiled value carries `Schema` and its **dialect**, which is what makes decision 3 possible.
3. **The recorded rationale is rendered by a dialect frozen per policy schema version.** A v0-compiled policy renders M0's sentence byte-for-byte; a v1 policy renders the M1e sentence (which names the decisive ranking key). The rationale is part of what a pinned policy *means*: retroactively re-wording a decision made under a policy pinned in 2026 would rewrite history, and audit compares the rationale byte-for-byte. `Decide` therefore holds exactly two renderers, one per shipped schema version, each frozen on release. Rich, evolving explanation lives in `mvo explain`, which *derives* the full comparison trace from the same recorded evidence and may improve freely — the stored sentence is a summary, not the explanation.
4. **`gate_pass` is an implicit first ranking key and `world_digest_asc` an implicit terminal key.** M0 sorted all candidates by the policy's key list and took `cands[0]` as the winner whenever *any* candidate passed — a policy whose ranking did not start with `gate_pass` would have SELECTed a **gate-failing** world. M1e makes the invariant structural instead of conventional: the effective key list is `gate_pass` ++ the policy's keys (minus any explicit `gate_pass`) ++ `world_digest_asc`. Listing either explicitly is legal and changes nothing (M0's `["gate_pass","wall_ms_asc"]` is exactly the effective list), so v0 compatibility is free and the winner provably passes every hard gate.
5. **Ranking keys are total by construction: unknown always loses.** Every key resolves to `(known bool, value int64)`; a candidate with a known value outranks one without, in *both* directions; two unknowns tie and fall through. "No evidence" can therefore never beat "evidence", and no key can panic, divide, or order by accident. This reproduces M0's `MaxInt64`-for-missing behavior for `wall_ms_asc` exactly.
6. **Escalation is a fixed struct of named conditions with integer/boolean parameters — no expression language, no eval.** Justification, four-fold: (a) totality — a closed struct cannot fail to evaluate, cannot loop, and cannot surprise `Decide` at runtime, which is the whole point of a pure decision function; (b) threat model — a policy is data that reaches the decision core, and an evaluator over evidence is a new execution surface inside the trust boundary (ch. 13's untrusted-generator posture applies doubly to anything the generator could ever influence); (c) explainability — each condition maps to one frozen sentence and one JSON object, so `mvo explain` is a table lookup rather than a pretty-printer for arbitrary ASTs; (d) validation — "unknown name = load error" is only decidable when the name space is closed. When someone genuinely needs arithmetic over metrics, the honest answer is a new named condition in this table, reviewed once, not a mini-language shipped forever.
7. **Unknown = validation error at load, never a runtime surprise.** Unknown gate predicate, ranking key, freshness basis, oracle kind, escalation field, or an oracle name a gate references but the policy does not declare — all are refused when the policy is decoded, wherever it is decoded (`mvo policy validate`, `mvo init`, `mvo race`, `mvo admit`, `mvo audit`, `mvo fetch-race`). `Decide` never sees a name it cannot evaluate, so its `default:` branches are unreachable-by-construction rather than fail-closed guesses. A *v0* policy naming an unknown gate keeps M0's fail-closed evaluation (unknown gate ⇒ gate fails) because that is what its recorded decisions were derived under and replay must reproduce it — the strictness upgrade applies to the shape that can express the vocabulary, never retroactively to decisions already made. v0 policies stay loadable, inspectable, and deliberately pinnable, but never become the workspace default (`mvo policy use` refuses them; `mvo intent new --policy` accepts one with a warning — see CLI).
8. **An oracle instance's identity in evidence is `(receipt.oracle.id, receipt.oracle.config)` — the policy-local name never enters the receipt.** The receipt already types exactly this (PRD §5.3 `oracle: {id, version, config}`): `id` is the registry **kind** (`command`, `pytest-collect`, `pytest-suite`), `config` is the digest of the *resolved run configuration* with the name excluded. A receipt should record what ran, not what an operator called it; and two policies that run the identical command produce comparable evidence. The validator refuses two declared oracles with equal resolved configs (they would be the same instance under two names), so the mapping name → `(id, config)` is injective within a policy.
9. **Evidence selection is data the compiled policy carries, not a branch in `Decide`.** Each gate/key carries a `Selector`: v1 selects by `(kind, config digest)`, the v0 dialect selects by `family == "suite"` (M0's rule). For each `(world, selector)` the **counted receipt** is the smallest-digest receipt bound to that world and matching the selector — order-independent, at most one, and byte-identical to M0's "smallest receipt digest wins" disambiguation. `Decide` applies selectors uniformly; only the compiler knows dialects.
10. **A receipt only counts as evidence for a world when it is *bound* to that world.** M0 matched on `receipt.world == world digest` alone. M1e additionally requires `receipt.freshness.valid_for == {world.tree, world.env}` — PRD principle 2 ("evidence is bound or it is noise") enforced at the gate rather than assumed at the recorder. Honestly recorded ledgers are unaffected (`race.Run` has always set `valid_for` from the world it judged), so this hardens without breaking replay; a receipt that names a world but judged a different tree is now inadmissible instead of decisive.
11. **Freshness basis is a *minimum* rank per gate, enforced from day one, and the vocabulary stays closed at three.** Rank: `construction` (3) > `dependency` (2) > `probabilistic` (1); an unrecognized basis on a *receipt* ranks 0 and satisfies nothing. A gate's `basis` is the weakest evidence it will accept. M1's oracles emit only `construction` — the maximum — so in M1 no gate can fail on basis alone; the check is live and vacuously satisfied rather than absent, so that when M3's impact-map-derived `dependency` receipts (KP-1) arrive, policies written today already refuse them where they must. No tier stronger than `construction` exists or will be invented here: a policy naming a fourth basis is a load error.
12. **The oracle registry is closed, and the race runs exactly the oracles the policy requires.** Required set = the oracles named by hard gates ∪ those resolved for metric-bearing ranking keys ∪ those named in `escalation.require_evidence`. Declared-but-unrequired oracles are never run (evidence waste is a measured PRD metric, §11), and the ladder **short-circuits per world at the first failed hard gate** — gates are ordered, so a world that fails O0 never pays for O1 (CP-3's successive-halving shape, made real). Gates after the first failure are `not-evaluated`, never reported as failures.
13. **`collected_delta` needs a base-state measurement, and that measurement is an input, not evidence about a candidate.** At race start the orchestrator opens one extra world-shaped worktree at `intent.base.commit` **through the same backend** (same tier, same image, same env) and runs the policy's collect oracle on it. The result is recorded as a `baseline.recorded` ledger event with its raw artifacts in CAS — *not* as a Receipt, because a Receipt must bind a World and the base tree is nobody's candidate; minting a baseline World would put a non-candidate into `Decide`'s subject list. Each candidate's collect receipt then records `collected_base` (the input, recorded so the delta is auditable) and `collected_delta = collected_total − collected_base`. Replay reads the delta from the receipt, so no new replay input exists. At admission the same measurement runs on the **trunk** tree before the patch is applied — exactly the right denominator for "did this patch delete tests from what is landing".
14. **Exit code 5 is fail, never pass — and the receipt records the raw code.** `pytest` exit 5 ("no tests collected") is the laundering vector ch. 19 names: it is non-zero, so the M0 status mapping already refuses it, and M1e never introduces a tolerant wrapper, a `|| true`, or an "exit != 1 means fine" shortcut anywhere. On exit 5 the collect oracle additionally records `collected_total = 0` explicitly (the documented meaning of the code) so the `collect-nonempty` gate fails with a *reason* rather than for want of a metric. The subtler vector — deleting *some* tests so the suite stays green — is what `collected-not-below` exists for, and it is in the shipped default policy.
15. **A repo without pytest fails at pre-flight as machinery, never as a receipt.** Before `race.started`, when the policy requires a pytest kind, mvo runs the tools probe (decision 16) in the base world environment: T0 on the host, T1 in the pinned image (M1c decision 18's probe pattern). No `pytest` in the probe output ⇒ abort, exit 1, one line: `mvo: race: policy requires oracle "suite" (pytest-suite) but pytest is not importable in this environment (T1 image multiverso-t1-fixture:v1); Multiverso's oracle ladder is Python-first (PRD §10) — author a command-kind policy for other languages`. The ledger stays empty of race events. A missing toolchain is not a failing candidate and must never be recorded as one.
16. **Structured evidence is discovered by an explicit probe and recorded as `result.tools`; absence is the record.** One `python3 -c` probe per pytest-kind oracle run reports `importlib.metadata` versions for `pytest`, `coverage`, `pytest-reportlog`, `pytest-rerunfailures`. The keys of `result.tools` are exactly the structured sources that were available and used; values are their versions. A metric derived from an absent source is **absent** — never zero, never "assumed 100%". A gate that needs an absent metric **fails** (weak defaults, honest labels); a policy that would rather route a human than reject uses `escalation.require_evidence`.
17. **Flake handling is implemented, not merely un-precluded — because first-run status is a *parsing* problem, not a runner problem.** `--reruns N` is requested only when `pytest-rerunfailures` **and** `pytest-reportlog` are both present, because JUnit XML records only the final outcome: without the JSONL there is no honest way to see the first run, so we do not ask for reruns at all. When both are present, `result.status` is derived from **first-run** outcomes (`exit_code == 0 && tests_failed_first_run == 0`) and `tests_passed_after_rerun` is recorded as a separate, weaker metric (EP-6's quarantine machinery stays v1). The cost is one JSONL scan we need anyway; the alternative — shipping fields we do not fill — is the kind of half-evidence this project exists to eliminate.
18. **`mvo race --oracle-cmd` survives only where it is honest: under a v0 policy.** A v0 policy names a gate (`suite-pass`) but not the command that decides it, so the pinned policy digest did **not** determine the gate — two machines could satisfy the same attested policy with different suites. That is the CP-5 hole this milestone closes. Therefore: under a v0 policy `--oracle-cmd` is still **required** and behaves exactly as in M0–M1d; under a v1 policy it is a usage error (`--oracle-cmd is not permitted with policy mv0:… (policy/v1): the policy names its own oracles`). The migration path is sugar at the point where pinning actually happens: `mvo intent new --oracle-cmd CMD` synthesizes a v1 command-oracle policy, records it, and pins it in the intent. Deprecating the flag outright would break every existing script for no gain; keeping it as a race-time override would keep the hole open under a new name.
19. **`mvo init` writes a v1 default policy that runs the Python ladder.** Gates, ordered: `collect-nonempty@collect`, `collected-not-below@collect` (threshold 0), `status-pass@suite`. Ranking: `[gate_pass, tests_passed_desc, wall_ms_asc]`. Escalation: `on_all_worlds_failed_machinery: true` only. `no-failed-tests` and `coverage-at-least` are shipped, validated, and tested, but stay out of the default: a default gate that fails when a plugin is missing or an output file is unwritable would trade honesty for brittleness at the moment a new user first meets the tool.
20. **Amends M1a decision 5 (landing gates come from the pinned policy, not from receipt archaeology).** M1a recovered the landing oracle's argv from the winner's race receipt because the policy could not say. A v1 policy can, so `admit` resolves its landing oracles from `intent.policy` — the same pinned, attested artifact, and now covering *several* oracles. `admit.LandingOracleArgv` remains, used only for v0 policies, where it is still the only source. The M1a invariant is strengthened, not relaxed: operators still cannot swap gates at admit time, and now they cannot swap them per machine either.
21. **`mvo explain` is the product surface, and it is derived, never stored.** The full key-by-key comparison trace, the per-candidate gate table with reasons and metrics, the escalation reason, and (with `--diffs N`) the top-k candidate patches are recomputed from the ledger + CAS + the pinned policy at render time by the same pure functions `Decide` uses (`race.Trace`). Nothing new is recorded, `--json` emits the structured CP-6 ESCALATE payload, and improving the rendering never invalidates a decision.
22. **`World` gains `patch_bytes`; `Receipt.Result` gains `metrics` and `tools`.** All three are always serialized (no `omitempty` games — M1b decision 5). `patch_size_asc` must be evaluable by a pure function with no CAS access, and the control plane holds the patch bytes at capture time, so the size is recorded where it is known. New-object digests move, as in M1b and M1c — but by decision 1 **replay of pre-M1e ledgers is unaffected**, which is a stronger guarantee than either of those milestones offered, and the acceptance script proves it with a race decided under a legacy v0 policy.

## Policy wire schemas (`internal/object`)

v0 stays **frozen, byte-for-byte**. It is the shape M0–M1d recorded, the shape old intents pin, and nothing may edit it again:

```go
// Policy is the v0 policy shape (multiverso.dev/policy/v0), FROZEN. New
// policies are PolicyV1; v0 objects are still loaded, compiled, and
// replayed exactly as M0 decided them (M1e decision 2).
type Policy struct {
    Schema    string   `json:"schema"`     // multiverso.dev/policy/v0
    HardGates []string `json:"hard_gates"` // ["suite-pass"]
    Ranking   []string `json:"ranking"`    // ["gate_pass","wall_ms_asc"]
}
```

v1 is the CP-5 shape. Every field is always serialized; every list is order-significant:

```go
const SchemaPolicyV1 = "multiverso.dev/policy/v1"

// PolicyV1 is a versioned policy artifact (CP-5): ordered hard gates that
// each name an oracle, a pass predicate, and the weakest freshness basis
// they accept; a lexicographic ranking spec (NEVER a weighted sum); and a
// closed set of escalation conditions.
type PolicyV1 struct {
    Schema     string          `json:"schema"`     // SchemaPolicyV1
    Name       string          `json:"name"`       // authored name, e.g. "default"
    Oracles    []OracleSpec    `json:"oracles"`    // declared instances, name-sorted
    HardGates  []GateSpec      `json:"hard_gates"` // ORDERED: ladder order and evaluation order
    Ranking    []string        `json:"ranking"`    // ORDERED lexicographic keys
    Escalation EscalationSpec  `json:"escalation"`
}

// OracleSpec declares one oracle instance. Kind selects the registry
// implementation; the remaining fields are its resolved configuration.
type OracleSpec struct {
    Name      string   `json:"name"`       // policy-local, ^[a-z0-9][a-z0-9._-]{0,31}$
    Kind      string   `json:"kind"`       // "command" | "pytest-collect" | "pytest-suite"
    Argv      []string `json:"argv"`       // command: the full argv (required)
                                           // pytest-*: runner prefix, default ["python3","-m","pytest"]
    Args      []string `json:"args"`       // pytest-*: extra args appended after the fixed flags
    TimeoutMS int64    `json:"timeout_ms"` // 0 = the intent's max_wall_ms
    Coverage  bool     `json:"coverage"`   // pytest-suite: measure coverage when coverage.py is present
    Reruns    int      `json:"reruns"`     // pytest-suite: --reruns N when BOTH plugins are present
}

// GateSpec is one ordered hard gate: a predicate over one oracle's counted
// receipt, plus the weakest freshness basis that receipt may carry.
type GateSpec struct {
    Gate      string `json:"gate"`      // closed vocabulary (below)
    Oracle    string `json:"oracle"`    // a name declared in Oracles
    Basis     string `json:"basis"`     // "construction" | "dependency" | "probabilistic"
    Threshold int64  `json:"threshold"` // per-gate meaning; 0 when the gate takes no parameter
}

// EscalationSpec is the CLOSED, purely declarative escalation rule set
// (decision 6). Zero values mean "rule off".
type EscalationSpec struct {
    MinCandidatesPassing       int      `json:"min_candidates_passing"`         // 0 = off
    OnRankingTie               bool     `json:"on_ranking_tie"`                 // the PRD's max_risk/ambiguity case
    RequireEvidence            []string `json:"require_evidence"`               // oracle names, sorted
    OnAllWorldsFailedMachinery bool     `json:"on_all_worlds_failed_machinery"`
}
```

Example (the shipped default, canonical form — this is exactly what `mvo init` writes to `.multiverso/policies/default.json`):

```json
{"escalation":{"min_candidates_passing":0,"on_all_worlds_failed_machinery":true,"on_ranking_tie":false,"require_evidence":[]},
 "hard_gates":[{"basis":"construction","gate":"collect-nonempty","oracle":"collect","threshold":0},
               {"basis":"construction","gate":"collected-not-below","oracle":"collect","threshold":0},
               {"basis":"construction","gate":"status-pass","oracle":"suite","threshold":0}],
 "name":"default",
 "oracles":[{"args":[],"argv":[],"coverage":true,"kind":"pytest-collect","name":"collect","reruns":0,"timeout_ms":0},
            {"args":[],"argv":[],"coverage":true,"kind":"pytest-suite","name":"suite","reruns":0,"timeout_ms":0}],
 "ranking":["gate_pass","tests_passed_desc","wall_ms_asc"],
 "schema":"multiverso.dev/policy/v1"}
```

### Closed vocabularies (`internal/policy/vocab.go`)

**Gate predicates.** Each evaluates the gate's **counted receipt** (decision 9). Every predicate first requires that the receipt *exists*, is *bound* (decision 10), carries a basis of at least the gate's rank (decision 11), and has `result.status != "error"`; any of those failing is the gate's failure reason.

| `gate` | passes iff | `threshold` | fail reason format |
|---|---|---|---|
| `status-pass` | `result.status == "pass"` | unused | `status=%s` |
| `collect-nonempty` | `metrics.collected_total >= 1` | unused | `collected_total=%d (exit %d)` |
| `collected-not-below` | `metrics.collected_delta >= -threshold` | max tolerated drop, ≥ 0 | `collected_delta=%d (tolerance -%d)` |
| `no-failed-tests` | `metrics.tests_failed == 0 && metrics.tests_errored == 0` | unused | `tests_failed=%d tests_errored=%d` |
| `coverage-at-least` | `metrics.coverage_bp >= threshold` | basis points (8735 = 87.35 %) | `coverage_bp=%d (want >= %d)` |
| *(v0 only)* `suite-pass` | `result.status == "pass"` on the family selector | — | M0's `failReason` text, unchanged |

A required metric that is **absent** fails the gate with `%s absent (source unavailable)` — never a fabricated zero.

**Ranking keys.** Every key yields `(known, value)`; unknown always loses (decision 5).

| key | dir | value | known when |
|---|---|---|---|
| `gate_pass` | pass first | 1 if every hard gate passed | always (implicit first key, decision 4) |
| `tests_passed_desc` | desc | `metrics.tests_passed` of the resolved oracle's counted receipt | metric present |
| `coverage_desc` | desc | `metrics.coverage_bp` | metric present |
| `wall_ms_asc` | asc | Σ `cost.wall_ms` over **all** counted receipts of the world | ≥ 1 counted receipt |
| `cost_asc` | asc | `world.cost.usd_micro` | `world.cost.source != "none"` |
| `patch_size_asc` | asc | `world.patch_bytes` | always (0 is a real empty patch) |
| `world_digest_asc` | asc | world digest (string order) | always (implicit terminal key) |

Metric-bearing keys resolve to **exactly one** oracle instance at validation time: the unique declared oracle whose kind's metric vocabulary contains the key's metric. Zero candidates ⇒ load error (`ranking key %q needs metric %q, which no declared oracle emits`); two or more ⇒ load error (`ranking key %q is ambiguous: oracles [%s] both emit %q`) — per-oracle key qualification (`coverage_desc@suite-fast`) is v1. Ambiguity is refused at load, never resolved by a coin flip at decide time (decision 7).

**Freshness bases** — `construction` (3) > `dependency` (2) > `probabilistic` (1); any other value is a load error on a policy and rank 0 on a receipt.

**Oracle kinds and their metric vocabularies** — the validator's source of truth, and a conformance test asserts each implementation emits exactly its declared set (or a subset, when a source was unavailable):

| kind | family | metrics |
|---|---|---|
| `command` | `suite` | *(none)* |
| `pytest-collect` | `collect` | `collected_total`, `collected_base`, `collected_delta` |
| `pytest-suite` | `suite` | `tests_total`, `tests_passed`, `tests_failed`, `tests_errored`, `tests_skipped`, `duration_ms`, `coverage_bp`, `tests_failed_first_run`, `tests_passed_after_rerun` |

### Compiled policy (`internal/policy`)

```go
package policy

// Policy is a validated, compiled policy: the single in-memory form both
// schema versions compile to (decision 2). Digest is the digest of the
// exact bytes it was loaded from — never recomputed from this struct
// (decision 1).
type Policy struct {
    Digest   string      // "mv0:…" as recorded/loaded
    Schema   string      // object.SchemaPolicy | object.SchemaPolicyV1
    Dialect  string      // DialectV0 | DialectV1 — selects the frozen rationale renderer
    Name     string      // "" for v0
    Gates    []Gate      // ordered: ladder order and evaluation order
    Keys     []Key       // effective ranking: gate_pass first, world_digest_asc last
    Esc      Escalation  // compiled escalation rules
    Oracles  []Oracle    // declared instances, name-sorted
    Required []string    // oracle names the race must run, in ladder order (decision 12)
}

type Gate struct {
    Predicate string   // vocab constant
    Oracle    string   // declared oracle name ("" in the v0 dialect)
    Basis     string   // minimum acceptable basis
    Threshold int64
    Label     string   // rendered gate name: "suite-pass" (v0) | "status-pass@suite" (v1)
    Sel       Selector
}

// Selector picks a world's counted receipt for one oracle instance
// (decision 9). Exactly one of Family / (ID, Config) is set.
type Selector struct {
    Family string // v0 dialect: receipt.family
    ID     string // v1: receipt.oracle.id (the kind)
    Config string // v1: receipt.oracle.config (resolved-config digest)
}

type Key struct {
    Name   string
    Desc   bool     // true = descending
    Metric string   // "" for gate_pass / cost_asc / patch_size_asc / world_digest_asc
    Sel    Selector // where Metric is read from (resolved at validation)
}

type Escalation struct {
    MinCandidatesPassing       int
    OnRankingTie               bool
    RequireEvidence            []Requirement // {OracleName, Sel}, name-sorted
    OnAllWorldsFailedMachinery bool
}

// Oracle is one resolved instance. Config is the digest of the canonical
// resolved configuration WITHOUT Name (decision 8); Family is the
// correlation family its receipts carry.
type Oracle struct {
    Name, Kind, Family, Config string
    Argv, Args                 []string
    TimeoutMS                  int64
    Coverage                   bool
    Reruns                     int
}

const (
    DialectV0 = "v0"
    DialectV1 = "v1"
)

// Decode discriminates on the "schema" field, validates, and compiles.
// Digest is object.DigestBytes(b): the bytes ARE the identity.
func Decode(b []byte) (Policy, error)

// Load fetches a policy's canonical bytes from CAS by digest and decodes
// them, cross-checking that the bytes digest back to the requested digest.
// This is how the ledger/CAS supplies HISTORICAL policies: every policy
// ever used is content-addressed in CAS, CAS is never pruned (M1d), so an
// intent pinned in the past always resolves for replay.
func Load(store *cas.Store, digest string) (Policy, error)

// Validate reports every problem with a v1 policy as one joined error
// (authoring wants the whole list, not the first line).
func Validate(p object.PolicyV1) error

// Default is the v1 policy mvo init writes (decision 19).
func Default() object.PolicyV1

// Command synthesizes the ephemeral command-oracle policy behind
// `mvo intent new --oracle-cmd` (decision 18).
func Command(argv []string, timeoutMS int64) object.PolicyV1
```

`race.LoadPolicy` is **removed**; `admit`, `audit`, `race`, and `publish/fetch` call `policy.Load`/`policy.Decode` (import direction: `policy` → `object`, `cas`; `oracle` → `policy`; `race`/`admit` → both; no cycles).

**Validation rules** (all are load errors; `mvo policy validate` prints them all):

1. `schema` is v0 or v1; anything else is refused by name.
2. v1: `name` matches `^[a-z0-9][a-z0-9._-]{0,63}$`; oracle names match `^[a-z0-9][a-z0-9._-]{0,31}$` and are unique.
3. Oracle `kind` ∈ the registry; `command` requires non-empty `argv`; `pytest-*` reject a non-pytest `argv` prefix only insofar as it must be non-empty after defaulting; `timeout_ms ≥ 0`; `reruns ≥ 0`; `reruns > 0` on a non-`pytest-suite` kind is an error.
4. No two declared oracles share a resolved config digest (decision 8).
5. ≥ 1 hard gate; each `gate` ∈ the predicate vocabulary; each `oracle` is declared; `basis` ∈ the three; `threshold ≥ 0`, **and `threshold == 0` when the predicate takes no parameter** (`gateDef.threshold` is what makes "this gate takes a parameter" decidable; a number a predicate silently discards makes the file read as if it demanded something it does not); the predicate's metric must be emitted by the named oracle **instance** — not merely by its kind (a `coverage-at-least` gate on a `pytest-collect` oracle is refused at load, and so is one on a `pytest-suite` oracle configured `coverage: false` or given a runner prefix `coverage.py` cannot wrap: both would fail on `coverage_bp absent (source unavailable)` for every candidate, forever). Validation and the suite oracle read the *same* wrappable-prefix predicate (`policy.CoverageWrappable`), so what the validator promises and what the implementation does cannot drift.
6. Every ranking key ∈ the key vocabulary; no key appears twice; nothing follows an explicit `world_digest_asc` (unreachable keys are an authoring bug); metric keys resolve uniquely, and against **instances** (rule 5), so a `coverage_desc` key over a coverage-disabled oracle is a load error rather than a permanently unknown value.
7. `escalation.min_candidates_passing ≥ 0`; every `require_evidence` name is declared.
8. Unknown JSON fields are rejected (`json.Decoder.DisallowUnknownFields` over the raw bytes before canonical decoding) — a typo'd `hard_gate` must not silently mean "no gates".

**v0 → compiled (the legacy dialect), normative table:**

| v0 input | compiled |
|---|---|
| `hard_gates: ["suite-pass"]` | one `Gate{Predicate: "suite-pass", Label: "suite-pass", Basis: "construction", Sel: {Family: "suite"}}` |
| `hard_gates: [<anything else>]` | a `Gate` that always fails, `Label` = the raw name (M0's fail-closed unknown-gate rule, preserved for replay) |
| `ranking: [...]` | keys mapped by name; `gate_pass`/`wall_ms_asc` as in the vocabulary; **unknown v0 key names compile to a no-op key** (M0's `rankLess` ignored them) |
| — | `Esc` = all rules off; `Required` = `["suite"]` (the synthetic name of the family selector); `Dialect = DialectV0` |

M0's decisions are reproduced exactly: same counted receipt, same effective key order, same fail-closed behavior, same sentence.

**The zero-gate floor.** Rule 5's "≥ 1 hard gate" is a v1 rule, and v0 is decoded exactly as M0 decoded it — never re-validated — so `{"schema":"…/policy/v0","hard_gates":[]}` compiles to a policy that gates *nothing*: `evalGates` runs no gate, every world passes, and `admit.Decide`'s gate loop is vacuously satisfied, which would sign and land an ADMIT no gate ever justified. It is refused at the **ingest boundary** — `mvo policy validate`, `mvo policy use`, and `mvo intent new --policy`, with the same message v1 gives (`a policy must declare at least one hard gate`) — and *not* in `policy.Decode`, which stays total so historical ledgers, published closures and `mvo audit` keep replaying whatever was recorded, exactly as recorded. Admission holds the second lock: `landingSpecs` refuses a gate-less policy as **machinery** before anything is recorded, and `admit.Decide` fails closed with REJECT (evidence = the apply receipt alone, which is also what audit's replay derives from the policy's own empty selector set — so the two derivations cannot disagree).

## Decision functions (`internal/race`, `internal/admit`) — CP-5, CP-6, NFR-1

```go
// object (no deps): the identity-carrying form of a recorded object. The
// digest is what the ledger/CAS recorded, never a re-serialization
// (decision 1).
type RecordedWorld   struct { Digest string; World   World }
type RecordedReceipt struct { Digest string; Receipt Receipt }
```

```go
// race.Decide is the v0 decision function (CP-6), now over the compiled
// policy. Pure, TOTAL, and order-independent: Type, Subject, Evidence and
// Rationale depend only on (pol, worlds, receipts). CreatedAt is left
// empty for the recorder.
func Decide(pol policy.Policy, worlds []object.RecordedWorld, receipts []object.RecordedReceipt) object.Decision

// Trace is the same evaluation, exposed for mvo explain (decision 21):
// per-candidate gate results with reasons and metric values, the effective
// key list, and the pairwise comparison walk. Pure; no I/O.
func Trace(pol policy.Policy, worlds []object.RecordedWorld, receipts []object.RecordedReceipt) RaceTrace
```

Evaluation order, exactly:

1. **Evidence list** = every input receipt's digest, sorted (unchanged).
2. **Counted receipts**: for each world and each selector, the smallest-digest receipt that is bound (decision 10) and matches.
3. **Gates**, in policy order, per world: the first failing gate stops evaluation for that world; later gates are `not-evaluated`. `pass(W)` iff no gate failed.
4. **Ranking**: sort candidates by the effective key list (decision 4), each key comparing per decision 5. The winner is `cands[0]`, which passes every hard gate by construction.
5. **Escalation** (decision 6), evaluated in this fixed order — first match wins and supplies the reason:

| # | rule | fires when | replaces |
|---|---|---|---|
| 1 | `on_all_worlds_failed_machinery` | `passCount == 0`, ≥ 1 world, and **every** world is machinery-failed: `outcome != COMPLETED`, or a counted receipt has `status == "error"`, or the first gate's oracle produced no receipt at all | REJECT |
| 2 | `require_evidence` | `passCount ≥ 1` and the prospective winner has no **usable** evidence from some required oracle: no counted receipt at all, **or** a counted receipt carrying none of that oracle kind's declared metrics (a kind with an empty metric vocabulary, `command`, is judged on existence alone) | SELECT |
| 3 | `min_candidates_passing: N` | `0 < passCount < N` | SELECT |
| 4 | `on_ranking_tie` | `passCount ≥ 2` and the winner ties the next passing candidate on **every** key up to the terminal `world_digest_asc` | SELECT |

Rule 1 exists because REJECT means "the candidates are bad"; when the machinery never produced a verdict, saying so is the honest decision (PRD risk row: ESCALATE as the default under low evidence). Rules 2–4 keep `Subject` in ranked order so the human sees the leader first — that, plus `mvo explain --json`, is CP-6's M1 ESCALATE payload.

Rule 2 tests *usability*, not existence, and the difference is the whole rule: the prospective winner passed every hard gate, so its ladder ran to completion and a counted receipt from every required oracle is always present. "Missing receipt" alone would therefore be unfireable in any race this orchestrator produces. What decision 16 points operators at is the other half — an oracle whose structured source was unavailable still emits a receipt, with the metric **absent** — so the rule fires on a receipt that carries none of its kind's metric vocabulary. Both halves are tested through `race.Run`, never through a hand-built receipt slice: a rule whose trigger no orchestrator can reach protects nothing.

**Rationale, v1 dialect (byte-exact formats):**

```
SELECT (≥2 passing):  "%d/%d worlds passed hard gates [%s]; selected %s over %s at ranking key %d %s (%s); ranking [%s]"
                       ← passCount, len(worlds), gate labels joined ",", winner, runner-up,
                         1-based index in the EFFECTIVE key list, key name,
                         "10 > 9" | "412 < 588" | "pass > fail" | "9 > -" , effective keys joined ","
SELECT (1 passing):   "%d/%d worlds passed hard gates [%s]; selected %s (sole world passing all hard gates); ranking [%s]"
REJECT (≥1 world):    "0/%d worlds passed hard gates [%s]; %s"
                       ← per world, ranked order, joined "; ":
                         "%s failed [%s] (%s)" ← world, first failing gate label, its fail reason
REJECT (0 worlds):    "no candidate worlds"
ESCALATE:             "escalated by policy rule %s: %s; %s"
                       ← rule name, rule sentence, the SELECT/REJECT sentence that would have been emitted
```

Rule sentences (byte-exact): `all_worlds_failed_machinery` → `"no world produced conclusive evidence (%s)"` with per-world `"%s outcome=%s"` / `"%s status=error"` / `"%s no receipt"` joined `", "`; `require_evidence` → `"winner %s has no counted receipt from required oracle %q"`, or `"winner %s has no usable evidence from required oracle %q: receipt %s carries none of its metrics"` when the receipt exists but its structured source did not; `min_candidates_passing` → `"%d of %d worlds passed, policy requires at least %d"`; `on_ranking_tie` → `"%s and %s tie on every ranking key [%s]; only world_digest_asc would order them"`.

**Rationale, v0 dialect:** M0's two format strings, frozen, reproduced character for character (`"%d/%d worlds passed hard gates [%s]; selected %s by ranking [%s] (wall_ms=%d)"` and the REJECT form with `failReason`).

```go
// admit.Decide is the pure landing gate (CP-6). gates carries one entry
// per hard-gate oracle of the pinned policy (M1a's single `gate *Receipt`
// generalizes; decision 20). Evidence = apply + gates, sorted.
func Decide(pol policy.Policy, intent, world string,
    apply object.RecordedReceipt, gates []object.RecordedReceipt) object.Decision
```

`admit.Decide` keeps M1a's outcome table (apply failed ⇒ ESCALATE with the CP-8 sentence; all gates pass ⇒ ADMIT; any gate fails ⇒ REJECT) and adds two total rules: the landing receipts must agree on one non-empty `valid_for.tree` (a disagreement is REJECT, `"landing gate receipts disagree on the landing tree"`), and the same gate vocabulary, basis ranks and metric rules apply. Escalation rules are **not** evaluated at admission: there is one subject, no ranking, and CP-8's conflict path is the only escalation admission knows. The v0 dialect reproduces M1a's sentences byte-for-byte.

## Receipt & World extensions (`internal/object`) — EP-2

```go
type Result struct {
    Status    string            `json:"status"`    // "pass" | "fail" | "error"
    Metrics   map[string]int64  `json:"metrics"`   // NEW: integers only (DP-1 forbids floats)
    Tools     map[string]string `json:"tools"`     // NEW: available structured sources → version
    Artifacts []string          `json:"artifacts"` // CAS keys, fixed kind order (below)
}

type World struct {
    // … unchanged M1b/M1c fields …
    PatchBytes int64 `json:"patch_bytes"` // NEW: len(captured patch); ranking input (decision 22)
}

const (
    BasisConstruction  = "construction"
    BasisDependency    = "dependency"
    BasisProbabilistic = "probabilistic"
)
```

Maps are always non-nil at construction (`{}` when empty): a nil map canonicalizes to `null`, which no M1e-recorded receipt may contain, and a unit test asserts it. Metric **absence is meaningful** and is the only honest record of an unavailable source; no oracle ever writes a placeholder value.

`Result.Artifacts` order is fixed by kind, absent kinds skipped: `stdout`, `stderr`, `tools-probe`, `junit-xml`, `reportlog`, `coverage-json`. `result.tools` says which of the optional three can be present, so the list is unambiguous without positional guessing.

## Oracle registry (`internal/oracle`) — EP-1, EP-7

```go
// Oracle is unchanged (EP-1: run(world) → Receipt; M1c's world handle).
type Oracle interface {
    ID() string      // the registry KIND — "command" | "pytest-collect" | "pytest-suite"
    Version() string // our oracle contract version ("v0"); the TOOL's version is in result.tools
    Run(ctx context.Context, w backend.World) (object.Receipt, error)
}

// New builds the instance a compiled policy declares. Baseline is the
// base-state collected count (decision 13); zero means "not measured", in
// which case collect oracles omit collected_base/collected_delta rather
// than inventing a denominator.
type Params struct {
    Spec     policy.Oracle
    CAS      *cas.Store
    Timeout  time.Duration // spec.TimeoutMS, or the intent's max_wall_ms when 0
    Baseline int64
}
func New(p Params) (Oracle, error) // unknown kind → error (unreachable: validation refused it)
```

Every kind fills the receipt the same way: `Oracle{ID: kind, Version: "v0", Config: spec.Config}`, `Execution{Argv: <in-world argv>, ExitCode, DurationMS, IsolationTier: w.Tier(), IsolationCaps: w.Caps()}`, `Freshness{Basis: BasisConstruction}` (M1's oracles measure by construction, always), `RecheckTier: "V1-replayable"`, `Family: <kind's family>`, `Cost{WallMS}`. `World` and `Freshness.ValidFor` are still completed by the orchestrator (M0 contract, unchanged).

**Working directory.** Each pytest-kind oracle writes into `./.mvo-oracle/<kind>/` (relative, so argv is tier-independent and deterministic) and **removes that directory host-side before the run** — the worktree is agent-writable, so a planted `junit.xml` must not be readable as evidence, and a stale file from a previous run must not be parsed as this one's. The bind mount (M1c decision 3) makes the host removal work under T1 with no extra exec. Oracle output never reaches a captured patch or tree: the race captures both in phase A, before any oracle runs, and `admit` writes the landing tree before the gates run.

**EP-7 order, normative, per run:** run the process → read each output file under a **64 MiB cap** → `CAS.Put` every artifact (stdout, stderr, probe, and each present output file) → **only then** parse the in-memory bytes into metrics. An over-cap file is neither stored nor parsed; the note `mvo: oracle: <file> exceeds the 64 MiB artifact cap; not stored, metrics absent` is appended to the stderr artifact bytes (the M1b/M1c control-plane-note precedent), the source stays out of `result.tools`, and any gate needing its metrics fails.

### `pytest-collect` (O0)

```
<prefix> --collect-only -q -p no:cacheprovider [args…]      # prefix defaults to python3 -m pytest
```

Status: M0's mapping (0 → pass, non-zero → fail, timeout/spawn → error). **Exit 5 is fail** (decision 14).

`collected_total` is parsed from the last summary line of stdout — `^(\d+) tests? collected` or `^no tests collected` → 0 — falling back to counting node-id lines (non-empty, containing `::` or ending `.py`) when no summary line is present. Exit 5 forces `collected_total = 0` regardless. When neither source yields a value the metric is **absent** and `collect-nonempty` fails on `collected_total absent (source unavailable)`. When a baseline was measured: `collected_base = baseline`, `collected_delta = collected_total − baseline`.

### `pytest-suite` (O1)

Probe first (decision 16), then:

```
[python3 -m coverage run]                                   # iff spec.coverage && coverage.py present
  <prefix> --junit-xml=.mvo-oracle/pytest-suite/junit.xml -p no:cacheprovider
           [--report-log=.mvo-oracle/pytest-suite/reportlog.jsonl]   # iff pytest-reportlog present
           [--reruns <N>]                                            # iff reruns>0 AND both plugins present
           [args…]
```

This single invocation is the receipt's `execution.argv` and `exit_code` (it *is* what ran the suite). When coverage was used, a second command — `python3 -m coverage json -o .mvo-oracle/pytest-suite/coverage.json` — extracts the report; its failure drops the coverage source (absent metrics, `coverage` absent from `result.tools`) and **never** changes the receipt's status or exit code: artifact extraction is not verification.

| source | metrics | parser |
|---|---|---|
| JUnit XML (always; core pytest) | `tests_total`, `tests_passed` = tests − failures − errors − skipped, `tests_failed`, `tests_errored`, `tests_skipped`, `duration_ms` | `encoding/xml` token walk summing **every** `<testsuite>` element (a `<testsuites>` root and a bare `<testsuite>` are both handled); `time` parsed by decimal-string arithmetic into ms (the `usd.go` discipline), `strconv.ParseFloat` + half-up rounding only as the exponent-form fallback |
| coverage.json (iff coverage.py) | `coverage_bp` = `(covered_lines·10000 + total/2) / total` from `totals.covered_lines` / `totals.num_statements`, `total > 0` | integer arithmetic over the two integer fields — the reported `percent_covered` float is never parsed (DP-1 discipline, no float on the primary path) |
| reportlog JSONL (iff pytest-reportlog) | `tests_failed_first_run`, `tests_passed_after_rerun` | line-wise JSON scan of `TestReport` records with `when == "call"`, keyed by `nodeid`: the first record per nodeid is the first-run outcome; a later passing record for a nodeid that first failed increments `tests_passed_after_rerun` |

**Status** = pass iff `exit_code == 0` **and**, when first-run metrics exist, `tests_failed_first_run == 0`; fail on any non-zero exit; error on timeout/spawn (M0). EP-6's rule is thereby structural: *the gate sees the first run*, and a pass-after-rerun is recorded as strictly weaker, separately named evidence (decision 17).

**Tools probe** (byte-exact script, run as `<python> -c <script>` where `<python>` is the spec's prefix head, default `python3`):

```python
import json,importlib.metadata as m
o={}
for n in ("pytest","coverage","pytest-reportlog","pytest-rerunfailures"):
    try: o[n]=m.version(n)
    except Exception: pass
print(json.dumps(o,sort_keys=True))
```

Its stdout is stored as the `tools-probe` artifact and parsed into `result.tools`. A probe that fails to run at all leaves `result.tools` empty, which fails every metric gate honestly — and cannot happen in practice, because pre-flight (decision 15) ran the same probe before the race.

## Base-state measurement (`internal/race/baseline.go`) — the collected-count denominator

Runs once per race, after `race.started`, before phase A, **only when** the policy requires `collected_delta` (a `collected-not-below` gate exists):

1. `gitx.AddWorktree(repo, <raceDir>/base, intent.base.commit)` under `repoMu`; `Backend.Open` it (same tier, image, and caps as the candidates — a delta measured under a different environment is not a delta).
2. Run the policy's collect oracle with `Baseline: 0`.
3. Append `baseline.recorded` (payload below), storing stdout/stderr/probe in CAS.
4. Close the handle and remove the worktree.

A baseline whose status is not `pass`, or whose `collected_total` is absent or 0, aborts the race with a machinery error before any world is created — a repo whose base tree collects nothing cannot give the guard meaning, and racing on it would produce receipts whose `collected_delta` was a fiction. Cost: one worktree (< 1 s) plus, under T1, one keeper (~0.5 s); it is recorded, so NFR-5's cost report accounts for it.

`admit` performs the same measurement in the landing worktree **before applying the patch** (trunk tree = the right denominator for what is landing) and records the same event inside the admission window.

## Race orchestrator changes (`internal/race`) — CP-3, EP-1

`Config` drops `Oracle oracle.Oracle` and gains `LegacyOracle oracle.Oracle` (the v0 dialect's supplied verifier only, decision 18) plus `OracleTimeout time.Duration`; the **compiled policy is loaded from `intent.Policy`, never passed in** (step 1 below), so it can only ever come from the attested pin. The orchestrator builds the required instances itself via `oracle.New`. Sequence, amending M1c:

1. Validation, intent load, **`policy.Load(cfg.CAS, intent.Policy)`**, CP-2 cap, race ctx, `raceDir` — unchanged otherwise. `race.started` gains two observational keys: `policy` (digest) and `oracles` (the required set, ladder order).
2. **Baseline** (above), when required.
3. **Phase A — generation**, unchanged (worktree, backend Open, agent run, host-side capture, `world.created` — now also recording `patch_bytes`).
4. Barrier, unchanged.
5. **Phase B — verification**, bounded at `Parallel`, one *ladder* per COMPLETED world: for each required oracle in ladder order, run it, record `receipt.recorded`, then walk **`pol.Gates` in policy order** over the receipts the world has accumulated so far — a gate whose oracle has not run yet is a rung still to climb (keep going), the first gate that *fails* **stops this world's ladder** (decision 12). Walking policy order rather than "the gates naming the oracle that just ran" is what makes the stop agree with `Trace`: gates may interleave oracles, and stopping on a later gate before an earlier gate's oracle had run would report the never-run gate as the failure and the failing gate as not-evaluated — decision 12 inverted, and the wrong gate frozen into the rationale and the `mvo worlds` GATE column. Oracles required only by ranking keys run after the gate oracles, and are skipped for a world that already failed a gate (its ranking values become unknown and it sorts after measured failures — deterministic, and it cannot affect the winner, who passed every gate).
6. Decision inputs assembled digest-sorted (M1c decision 17, unchanged), now as `RecordedWorld`/`RecordedReceipt`; `Decide`; `decision.recorded`; `race.finished`; cleanup — unchanged.

`race.Run` returns a non-`SELECT` decision (REJECT, ESCALATE) exactly as it always has: a recorded decision is a success for the verb (`mvo race` exits 0), and the scriptable signal is `mvo explain --json`'s `type`. Changing that exit-code contract would silently break every existing caller for no evidential gain.

## Admission changes (`internal/admit`) — EP-3, decision 20

`admit.Config` drops `Oracle oracle.Oracle` and gains `OracleTimeout time.Duration`: like the race, admission builds its oracles from the pinned policy (v1) or from `LandingOracleArgv` (v0). `cmd/mvo/admit.go` therefore no longer constructs an oracle — one fewer place where an operator's environment could decide what a gate means. `Result` gains `GateReceipts []string` (digest-sorted) and keeps `GateReceipt` as the first of them for output compatibility.

1. Load the intent, SELECT decision, winner world, and **compiled policy** (from `intent.policy`).
2. Landing worktree at trunk; **baseline** collect when required; `race.EnvDigest` before apply — unchanged otherwise.
3. Apply → the landing-apply receipt (unchanged, `Family: "landing-apply"`).
4. Write the landing tree, then run **every hard-gate oracle** of the policy, in gate order, on the landing tree (EP-3 v0 recompute), recording one `receipt.recorded` each. The v0 path runs the single oracle recovered by `LandingOracleArgv` (M1a, unchanged). No short-circuit here: there is one candidate and the operator deserves the full gate picture in the ESCALATE/REJECT payload.
5. `admit.Decide(pol, intent, winner, apply, gates)`; record; ADMIT ⇒ attestation over `Evidence = sorted(apply ++ gates)` and `budget_consumed.wall_ms` = Σ over them (existing code path, now summing N receipts).

## Ledger events (M1e additions and amendments)

| Type | Payload |
|---|---|
| `policy.created` | canonical Policy/PolicyV1 bytes — **unchanged type**, now appended whenever a policy digest is first seen (`init`, `policy use`, `intent new --oracle-cmd`, a `--oracle-cmd` race under a v0 policy). Idempotent: a digest already present in the ledger is not re-appended. |
| `baseline.recorded` | `{"collected_total": 8, "duration_ms": 812, "exit_code": 0, "intent": "mv0:…", "oracle": {"config": "mv0:…", "id": "pytest-collect", "version": "v0"}, "probe": "sha256:…", "stderr": "sha256:…", "stdout": "sha256:…", "tree": "git:…"}` |
| `race.started` | gains `"oracles": ["collect","suite"]` and `"policy": "mv0:…"` (always present; `oracles` in ladder order). Audit still reads only `intent`. |

`baseline.recorded` is observational (M1b precedent): no replay semantics, `mvo audit` ignores it, the hash chain covers it. The delta it produced lives in the receipts, which *are* replay inputs.

## CLI (`cmd/mvo`)

```
mvo policy list [--dir DIR]
mvo policy show <name|digest> [--json] [--dir DIR]
mvo policy validate <file> [--dir DIR]
mvo policy use <name> [--dir DIR]

mvo intent new --title T [--desc D] [--budget-candidates N] [--budget-wall-ms MS]
               [--policy <name|digest>] [--oracle-cmd CMD]
mvo race <intent-digest> … [--oracle-cmd CMD]        # v0-pinned intents only (decision 18)
mvo explain <intent-digest> [--json] [--diffs N] [--dir DIR]
```

- **`policy list`** — one row per policy known to the workspace, file-backed and ledger-recorded, digest-sorted: `NAME  DIGEST  SCHEMA  GATES  RANKING  STATE`, where STATE ∈ `recorded`, `recorded (default)`, `unrecorded (file only)`. A file whose bytes no longer digest to a recorded policy shows as `unrecorded` — the honest rendering of "you edited this file; nothing has used it yet".
- **`policy show <ref>`** — resolution order, deterministic: a full `mv0:` digest → CAS; else `.multiverso/policies/<name>.json`; else the latest ledger-recorded policy whose `name` is `<name>`. Human output prints digest, schema, name, the ordered gates with oracle + basis + threshold, the effective ranking key list, the escalation rules that are ON, and the declared oracles with their resolved argv and config digest; then the canonical JSON. `--json` prints **only** the canonical bytes, so authoring a variant is `mvo policy show default --json > .multiverso/policies/mine.json`.
- **`policy validate <file>`** — decode + validate + compile; on success print the digest it *would* have, the summary, and `OK: policy valid`; on failure print every problem, one per line, as `mvo: policy validate: <file>: <field>: <detail>`, exit **1** (invalid content is a failure, not CLI misuse). Unknown gate example, byte-exact:
  `mvo: policy validate: bad-gate.json: hard_gates[1].gate: unknown gate "suite-passes" (known: collect-nonempty, collected-not-below, coverage-at-least, no-failed-tests, status-pass)`.
- **`policy use <name>`** — validate `.multiverso/policies/<name>.json`, require its in-document `name` to equal the filename stem, put its bytes in CAS, append `policy.created` if the digest is new, and set `config.default_policy`. Refuses a v0 file (`policy use: legacy-v0.json is policy/v0, which cannot name its oracles; author a policy/v1 file (see mvo policy show default --json)`): a shape whose gate the digest does not determine must never become the *silent* default for everything created afterwards. Editing a file and re-running `use` mints a **new** digest; nothing is ever mutated, and intents pinned to the old digest keep replaying against it.
- **`intent new --policy <ref>`** pins an explicit policy instead of the workspace default, resolving `<ref>` exactly as `policy show` does and ingesting a file-backed policy into CAS with a `policy.created` event when its digest is new. Unlike `use`, it **accepts a v0 policy** — pinning a historical shape deliberately, per intent, is what replay and migration require — and prints one stderr warning: `mvo: intent new: policy mv0:… is policy/v0: its gate is not determined by the policy digest, so --oracle-cmd is required for every race of this intent (M1e decision 18)`. `--oracle-cmd CMD` instead synthesizes, records, and pins `policy.Command(argv, intent.budget.max_wall_ms)`; `--policy` and `--oracle-cmd` are mutually exclusive.
- **`race --oracle-cmd`**: required when the intent's policy is v0, a usage error when it is v1 (decision 18).

### `mvo explain` — the explainability surface (CP-6)

```
decision:  mv0:7c3…
type:      SELECT
intent:    mv0:1a9…  (fix mean())
policy:    mv0:4f2…  (default, policy/v1)
winner:    mv0:aa1…

gates (ordered, first failure stops the ladder):
  RANK  WORLD     ORD  collect-nonempty@collect  collected-not-below@collect  status-pass@suite  RESULT
  1     mv0:aa1…   1   pass                      pass (delta=+2)              pass               PASS
  2     mv0:bb2…   2   pass                      pass (delta=0)               pass               PASS
  3     mv0:cc3…   3   pass                      FAIL (collected_delta=-3 (tolerance -0))  n/a    FAIL

why mv0:aa1… won  (ranking [gate_pass, tests_passed_desc, wall_ms_asc, world_digest_asc]):
  vs mv0:bb2… (rank 2)
    1  gate_pass          pass   =  pass    tie
    2  tests_passed_desc  10     >  8       WINNER mv0:aa1…   ← decided here
  vs mv0:cc3… (rank 3): decided at key 1 gate_pass (pass > fail)

evidence:  mv0:d1…  (collect@mv0:aa1…, pass, collected_total=10)
           …
rationale: 2/3 worlds passed hard gates [collect-nonempty@collect,collected-not-below@collect,status-pass@suite]; selected mv0:aa1… over mv0:bb2… at ranking key 2 tests_passed_desc (10 > 8); ranking [gate_pass,tests_passed_desc,wall_ms_asc,world_digest_asc]
freshness: FRESH (base 9c1f2a3b4d5e == main head)
```

The measurement in a gate cell (`pass (delta=+2)`) is read from **that gate's own counted receipt**, looked up by `GateResult.Receipt` — never from the candidate's merged metric map, which is the union of every counted receipt. A policy may legally declare two instances of one kind, both emitting the same metric names, so a number printed beside a gate label is an attribution claim and must come from the receipt the gate was evaluated against. `race.mergedMetrics` (the `--json` report's `candidates[].metrics`) correspondingly **drops** any name two counted receipts disagree about: a number no consumer can attribute to an oracle is worse than no number.

For ESCALATE, an `escalation:` block precedes `rationale:` naming the rule, its parameters, and the candidates it implicates — and the ranking block above it is **retitled and re-marked**: `ranking walk for leader <digest>  (no winner: escalated by <rule>; ranking […])`, with the deciding row rendered `leads here   ← not a decision (escalated)` or, when the only separating key is the terminal `world_digest_asc`, `tie-break only — not a decision`. The walk is still shown (the tie *is* the evidence), but the words `won` and `WINNER … ← decided here` appear only under SELECT: an ESCALATE that renders as a win launders exactly the ambiguity the rule reported, and under `on_ranking_tie` it would credit the digest coin flip decision 6 exists to refuse. `--diffs N` appends the top-N ranked candidates' captured patches from CAS, each truncated at 64 KiB with an explicit `… (truncated, full patch <cas key>)` marker — completing CP-6's M1 ESCALATE payload ("explain output + top-k candidate diffs + the failed/unaffordable gates"). `--json` emits one line, schema `multiverso.dev/explain-report/v0`:

```json
{"schema":"multiverso.dev/explain-report/v0","decision":"mv0:…","type":"SELECT","intent":"mv0:…",
 "title":"fix mean()","policy":{"digest":"mv0:…","schema":"multiverso.dev/policy/v1","name":"default",
   "gates":[{"label":"collect-nonempty@collect","gate":"collect-nonempty","oracle":"collect","basis":"construction","threshold":0}],
   "ranking":["gate_pass","tests_passed_desc","wall_ms_asc","world_digest_asc"]},
 "winner":"mv0:…","escalation":{"rule":"","detail":""},
 "candidates":[{"rank":1,"world":"mv0:…","ordinal":1,"outcome":"COMPLETED","pass":true,
   "gates":[{"label":"collect-nonempty@collect","result":"pass","detail":"","receipt":"mv0:…"}],
   "metrics":{"collected_delta":2,"collected_total":10,"tests_passed":10},
   "keys":[{"key":"tests_passed_desc","known":true,"value":10}],"patch_bytes":412}],
 "trace":[{"other":"mv0:…","other_rank":2,"decided_at":2,"key":"tests_passed_desc",
   "winner_value":"10","other_value":"8","steps":[{"index":1,"key":"gate_pass","result":"tie"}]}],
 "rationale":"…","freshness":"FRESH","freshness_detail":"…"}
```

`mvo worlds` gains a `GATE` column rendering the first failed gate label instead of a bare `fail`.

## Audit, verify, and fetch-race amendments

- **`mvo audit`** — replay loads the pinned policy with `policy.Load` (v0 and v1 alike), passes ledger-supplied digests as `RecordedWorld`/`RecordedReceipt`, and, on the admission path, gathers **all** window receipts for the winner that match the policy's gate selectors (smallest digest per selector) instead of the single smallest-digest suite receipt. `diffDecision` is unchanged. Because no input is re-serialized (decision 1), a ledger written by M0–M1d replays byte-for-byte under the M1e binary; the acceptance script proves the equivalent within one ledger by deciding a race under a v0 policy.
- **`mvo verify`** — check 6 (freshness) generalizes from "every `family == "suite"` receipt" to "**every evidence receipt except the `landing-apply` one** must have `valid_for.tree == "git:" + <commit tree hex>`"; the `landing-apply` receipt keeps the parent commit's tree. Simpler, stricter, and correct for N gate receipts (the old rule would have silently skipped a `collect`-family landing receipt). Checks 1–5 and 7, the seven-check success line, the `--json` report shape, and the schema string are unchanged.
- **`internal/publish`** — `fetch.go` decodes the published `policy/*.json` through `policy.Decode` (accepting both schemas, rejecting anything else) and replays through the amended `Decide` signatures with the digests it already verified against filenames. Its attestation check makes the **same** generalization `mvo verify`'s check 6 does: the statement's subject `gitTree` must equal the `valid_for.tree` of **every** predicate evidence receipt except the `landing-apply` one (whose pre-apply tree only the commit can anchor — decision 15 keeps that in `mvo verify`), rather than of one smallest-digest `suite`-family receipt, which would reject a correctly signed closure whose policy gates only a `pytest-collect` oracle. Its world table's `GATE` cell is derived from `race.Trace` over the closure's own worlds, receipts and policy — the same `CandidateTrace.GateCell` the local `mvo worlds` renders — so a world stopped by the O0 guard names the gate that stopped it instead of showing `-` for want of a suite receipt. The evidence-tree layout, naming, signing, and every other M1d contract are untouched: a v1 policy is just a policy object.

## Fixtures

- `testdata/toyrepo/patches-rank/` — `patch-a` (fix `mean`), `patch-c` (fix `mean` **and** add two passing tests). Both pass every gate; `patch-c` wins on `tests_passed_desc` (10 vs 8) at effective key 2.
- `testdata/toyrepo/patches-launder/` — `patch-a`; `patch-cut` (fix `mean`, delete the three `clamp` tests → suite exits 0, `collected_delta = -3`); `patch-wipe` (fix `mean`, delete `test_stats.py` entirely → `pytest` exits **5**, `collected_total = 0`). The two laundering candidates are stopped by O0's counts, one by each mechanism, while their suites would have looked green.
- `testdata/toyrepo/patches-tie/` — `patch-x` (`sum(values) / len(values)`), `patch-y` (`math.fsum(values) / len(values)`): distinct trees, identical gate results and identical `tests_passed`, so they tie on every key of the tie policy. Distinct patches are required on purpose — two byte-identical worlds are **one** world under content-addressing, which is a different (and uninteresting) situation.
- `testdata/toyrepo/policies/` — `rank-two-keys.json` (ranking `[gate_pass, tests_passed_desc, wall_ms_asc]`), `tie-escalate.json` (ranking `[gate_pass, tests_passed_desc]`, `on_ranking_tie: true` — no `wall_ms_asc`, because wall time is noise and a tie fixture must be deterministic), `legacy-v0.json` (the frozen M0 shape), `bad-gate.json` (`"gate": "suite-passes"` — one typo, one expected error). Each v1 file's in-document `name` equals its filename stem, since `mvo policy use` requires it; all four are canonical JSON, so `mvo policy validate` reports the digest the workspace will record.
- `testdata/oracle/` — recorded `junit-pass.xml`, `junit-fail.xml`, `junit-suites-root.xml`, `junit-malformed.xml`, `reportlog-rerun.jsonl`, `coverage.json`. The parsers are pure functions over these bytes, so the parser tests need **no** Python and no plugins at all.

## Acceptance script (CI runs this)

`scripts/accept.sh` — M1d's steps kept intact, with amendments and five insertions. All new steps use the shipped default policy or a `testdata/toyrepo/policies/` file copied into `.multiverso/policies/`.

- **2, 3b, 3c, 3d (amended)**: the races drop `--oracle-cmd` — the v1 default policy drives the ladder (proving decision 18's migration and that the T1 image satisfies the pytest pre-flight). Step 2 additionally asserts, over `receipt.recorded` payloads, that the winner has **two** receipts: `oracle.id == "pytest-collect"` with `metrics.collected_total == 8` and `metrics.collected_delta == 0`, and `oracle.id == "pytest-suite"` with `metrics.tests_passed == 8` and non-empty `result.tools["pytest"]`; and that a `baseline.recorded` event exists with `collected_total == 8`.
- **3e — lexicographic ranking (case a)**: `policy use rank-two-keys`; intent; race `patches-rank`. Assert `SELECT`; the winner is `patch-c`'s world (identified by its `context` CAS key); `mvo explain` contains `at ranking key 2 tests_passed_desc (10 > 8)` and the trace line `2  tests_passed_desc` with `WINNER`; `mvo explain --json` reports `trace[0].decided_at == 2`. **The first key ties and the second decides** — the proof that ranking is lexicographic and not a score.
- **3f — collected-count guard (case b)**: `policy use default`; `intent new --budget-candidates 3`; race `patches-launder`. Assert `SELECT` with `patch-a`'s world winning; `patch-cut`'s world failed `collected-not-below@collect` with `collected_delta=-3`; `patch-wipe`'s world failed `collect-nonempty@collect` with `execution.exit_code == 5` and `metrics.collected_total == 0`; and **neither losing world has a `pytest-suite` receipt** — the ladder short-circuited, and no test-deleting candidate ever reached a green suite.
- **3g — escalation on a tie (case c)**: `policy use tie-escalate`; intent; race `patches-tie`. Assert the decision type is `ESCALATE`, the rationale contains `escalated by policy rule on_ranking_tie`, both worlds passed every gate, and `mvo explain --json` carries `escalation.rule == "on_ranking_tie"` naming both worlds. `mvo race` exits 0 (a recorded decision is the product).
- **3h — policy validation (case d)**: `mvo policy validate testdata/…/bad-gate.json` exits **1** and prints `hard_gates[1].gate: unknown gate "suite-passes"` with the known list; `mvo policy validate .multiverso/policies/default.json` exits 0; `mvo policy list` shows `default` as the recorded default; `mvo policy show <default digest> --json` re-digests to that digest (byte-stability of the authoring round trip).
- **3i — v0 compatibility**: copy `legacy-v0.json` into `.multiverso/policies/`; assert `mvo policy use legacy-v0` **fails** (v0 never becomes the silent default); pin it per-intent with `mvo intent new --policy legacy-v0` (which ingests, records, and warns on stderr); race `patches` with `--oracle-cmd "python3 -m pytest -q"`; assert `SELECT` and that the recorded rationale matches M0's frozen sentence (`grep -E '^rationale: [0-9]+/[0-9]+ worlds passed hard gates \[suite-pass\]; selected mv0:[0-9a-f]{64} by ranking \[gate_pass,wall_ms_asc\] \(wall_ms=[0-9]+\)$'`). Then assert `mvo race … --oracle-cmd` against a **v1**-pinned intent exits 2 with the decision-18 message.
- **4–6g (unchanged)**: admit, trailer, verify ×3, publish/fetch-race/prune round trip — the fetch-race consumer now verifies a closure whose policy is v1 and whose SELECT replays through the new `Decide`.
- **7 (amended, case e)**: the second-machine `mvo audit --json` asserts `chain_ok`, `replay_identical`, `admissions ≥ 1`, and `decisions ≥ 8` (`≥ 9` when the docker-gated T1 step ran, as M1d's `MIN_DECISIONS` already switches) — the four new races (two SELECTs, one ESCALATE, one legacy-v0 SELECT) replay byte-for-byte alongside the M1a–M1d decisions, **through two different policy schema versions in one ledger**.
- **8–10 (unchanged)**: bundle tamper, ledger tamper, final clean audit.

## Testing bar

- `internal/policy`: `Decode` accepts both schemas and rejects, by table, every validation rule of the list above (unknown gate/key/basis/kind/escalation field, undeclared oracle name, duplicate key, key after `world_digest_asc`, duplicate resolved config, gate metric outside its oracle's vocabulary, ambiguous and unsatisfiable metric keys, bad names, unknown JSON fields); `Digest` equals `object.DigestBytes(input)` and is **never** a re-serialization (a test decodes a policy with a field order and an unusual-but-legal encoding and asserts the digest is the input's); v0 compile goldens (gates, effective keys, dialect, `Required`); `Default()` canonical-bytes golden (the shipped default's digest is pinned); `Command()` golden; round-trip `policy show --json | policy validate`.
- `internal/race` (`Decide`/`Trace`): table tests for every gate predicate × {pass, fail, metric absent, receipt absent, unbound receipt, wrong basis, status error}; ranking tests for every key including unknown-loses in both directions and the implicit first/terminal keys; the **second-key** decision case; escalation tests for all four rules including their fixed precedence and the "would-have-been" sentence; permutation invariance (M1c's property test, extended to receipts with metrics); **byte-for-byte M0 goldens under a v0-compiled policy** — the compatibility proof — including the unknown-v0-gate fail-closed path and the unknown-v0-key no-op path; `Trace` agrees with `Decide` on winner and order for every table case.
- `internal/oracle`: pure-parser tests over `testdata/oracle/` (JUnit single/`<testsuites>`/malformed/attribute-missing; coverage integer arithmetic incl. half-up rounding and `num_statements == 0`; reportlog first-run vs rerun accounting); argv goldens per kind and tier (relative output paths, flags present iff the corresponding plugin is in the probe result, `--reruns` only when both plugins are present); the exit-5 table (status `fail`, `collected_total == 0`, never `pass` — the laundering regression test); the summary-line parser table incl. `no tests collected` and the node-id fallback; artifact ordering and the **EP-7 order** (a CAS-failure injection proves artifacts are stored before parsing); the 64 MiB cap path (not stored, not parsed, note appended, metrics absent); host-side pre-run removal of a **planted** `junit.xml` (an agent-authored file must never be read as evidence); registry conformance (each implementation's emitted metric keys ⊆ its declared vocabulary); `command` receipts carry `{}` metrics and `{}` tools. Plugin-dependent end-to-end tests skip with a named reason when the plugin is absent; no test requires one.
- `internal/race` (orchestrator): required-set computation (declared-but-unrequired oracles never run); ladder short-circuit (a world failing gate 1 has exactly one receipt); baseline lifecycle incl. the abort paths (baseline fails / collects zero → machinery error, zero worlds recorded); `race.started` payload golden.
- `internal/admit`: plural gate receipts (ADMIT with 2 gates, REJECT on the second, the landing-tree-disagreement REJECT); v0 dialect reproduces M1a's sentences byte-for-byte; the landing baseline runs before apply.
- `internal/object`: `Result`/`World` golden canonical bytes and digest stability re-derived; a nil metrics/tools map is caught by a constructor test; `RecordedWorld`/`RecordedReceipt` carry digests unmodified.
- `cmd/mvo`: `policy list|show|validate|use` output goldens and the flag/usage matrix (unknown name, v0 `use` refusal, name/filename mismatch, `--policy` + `--oracle-cmd` conflict, `--oracle-cmd` against a v1 intent); `explain` human and `--json` goldens for SELECT, REJECT, ESCALATE, and a v0-policy decision; `--diffs` truncation marker; audit over a ledger holding v0- and v1-policy decisions replays clean.
- `gofmt -l` clean, `go vet ./...` and `go test ./...` pass with no docker daemon and no pytest plugins present; `scripts/accept.sh` is the e2e test and runs in CI. `go.mod`/`go.sum` unchanged. No test invokes a real agent CLI, pulls an image larger than `python:3.12-alpine`, or requires network.

## Explicitly NOT in M1e

O2 property/differential and O3 mutation oracles (EP-4/EP-5, M2); the adaptive scheduler and any policy-resident budget allocation (CP-4, M2); weighted-sum scoring in any form (PRD principle — lexicographic only, forever); an expression language, arithmetic, or user-defined predicates in policy (decision 6); per-oracle-qualified ranking keys (`coverage_desc@suite-fast`); policy-resident isolation-tier/image selection and devcontainer awareness (M1c's NOT list stands); retention or publication settings as policy fields (M1d's flags stand); quarantine sets, flake-rate tracking, and rerun *policy* beyond the first-run/after-rerun receipt fields (EP-6, v1); JS/TS and Rust oracle kinds (vitest/jest, cargo-nextest — Phase B); `coverage` dynamic per-test contexts and the KP-1 impact map (M3); `dependency`- or `probabilistic`-basis receipts (the ranks are enforced, nothing emits them yet); REPAIR worlds and policy-gated conflict repair (CP-8 v1); policy signing or per-policy trust roots (policies are pinned by digest and travel inside signed decisions — TP-5 territory); multi-key rotation; a policy registry beyond `.multiverso/policies/` (hosted policy management is the paid plane, §12); `mvo doctor`; Windows.
