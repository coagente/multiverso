# Multiverso — Product Requirements Document

> **v1.0** · 2026-08-13 · Status: **approved for development** (survived a 3-lens adversarial review panel — engineering, product, research-consistency — with all findings dispositioned)
> Grounded in the [research corpus](research/README.md) (20 chapters, 305 primary sources, cutoff 2026-08-12). Claims below cite corpus chapters as `[ch. N]`.

---

## 1. Overview

**Multiverso is an evidence-native, Git-compatible control plane for speculative software change produced by AI agents.**

Given a versioned **Intent**, Multiverso creates N isolated candidate **Worlds**, records their lineage, accumulates **Evidence** cryptographically bound to the exact state of each candidate, and makes an auditable **Decision** — admitting to trunk only an integrated state whose evidence is fresh, emitted as an ordinary Git commit plus a signed **Attestation**.

> Git versions accepted history. Multiverso versions the possibilities not yet accepted — and the evidence used to decide which one becomes history.

### The problem

Agents now produce a large and growing share of candidate code changes (AI-assisted changes are already ≥14.4% of merges, measured as a floor [ch. 10]), and every layer of that pipeline has industrialized *except the decision*:

- Generating N parallel candidates is commodity: Cursor 2.0 races multiple models, Claude Code runs agent teams, Codex teams queue 3–5 parallel sandboxed tasks and Codex cloud fans out multiple attempts per task [ch. 10, 16].
- Selection is either manual or unaccountable: interactive products (Cursor 2.0, Codex) devolve to a human reading diffs; frontier best-of-N pipelines (TRAE, Anthropic's parallel test-time compute) ship automated selection — but none produces auditable evidence of why a candidate won, none binds tests to world state, and none reports a false-admission rate [ch. 2, 10]. The measured cost of bad selection is large — CodeMonkeys reaches 69.8% coverage but selects only 57.4% (12.4pp regret) [ch. 2].
- Verification evidence is ephemeral and unbound: test logs, review comments, and CI runs are not receipts tied to the exact state they judged; nothing survives as an audit trail [ch. 5, 7].
- Admission gates (merge queues, branch protections) are static YAML that knows nothing about intents, candidates, or evidence quality [ch. 5, 10].
- Default oracles are measurably weak: 15.7–28.4% of "resolved" SWE-bench patches are wrongly counted; human-suite-passing wrong programs exist for 77% of SWE-bench Verified instances [ch. 8, 9].

The result: organizations admit agent-produced changes on evidence that is weak, unbound, unpriced, and unauditable — or they burn senior-engineer time re-reviewing everything, which negates the fleet.

### Why now, why us

The research corpus establishes [ch. 12]: of Multiverso's five capabilities — **C1** versioned intents, **C2** alternative-candidate exploration, **C3** adaptive verification budgeting, **C4** state-bound evidence, **C5** integrated-state admission — every one exists somewhere individually and **no system ships more than two**. Budgeted adaptive verification of untrusted code exists in adjacent forms (AI-control protocols, 2023–2026 [ch. 13]; VOI-driven allocation at function level [ch. 3]); what is unoccupied is **C3 scoped to an evidence-aware scheduler over persistent N-candidate worlds for one versioned intent, allocating across generation and heterogeneous verification, terminating in Git-trunk admission with state-bound evidence** [ch. 3, 12, 13]. Claim Plane (C1) and Proof-or-Stop (C4) appeared four weeks apart in July 2026; the estimated window before an incumbent assembles the combination is **12–24 months** [ch. 10, 12]. Bare orchestration is a dead category (Terragon, Bloop); the money and the defensible position are at admission [ch. 10].

## 2. Goals and non-goals

### Goals

- **G1 — Product**: Ship a CLI-first candidate-race control plane a platform team can run today on a real repo with real agents, producing decisions that are *reproducible, explainable, and auditable* from an immutable ledger.
- **G2 — Research**: Answer the core question: *under a fixed total budget, can an evidence-aware scheduler allocate compute across generation, testing, and adversarial challenge to beat single-agent, fixed best-of-N, and test-only selection on true-correct admission rate (TCAR) and false-admission rate (FAR)?* [ch. 3, 9]
- **G3 — Standard**: Define the evidence formats (world digests, oracle receipts, admission attestations as in-toto predicates) so they can outlive our implementation — the "Sigstore/SLSA of agent changes" position [ch. 7, 10].

### Non-goals (explicit, from the founding memo and corpus)

- A new VCS, storage format, or patch theory — Git is the interop plane; Jujutsu only ever as an adapter [ch. 4].
- Polyglot semantic content-addressing, code CRDTs, general semantic merge [ch. 4, 6].
- Our own microVM/sandbox infrastructure — we integrate substrates (worktrees, Docker; Morph/E2B later), never build them [ch. 1].
- Coordinating multiple concurrent intents (COMPOSE/SERIALIZE across intents) — Phase D, out of MVP [ch. 6].
- Replacing GitHub/forges, desktop UI, IDE plugins.
- Glean or any heavyweight code-intelligence dependency in the critical path [ch. 11].

## 3. Users

| Persona | Situation | What Multiverso gives them | Priority |
|---|---|---|---|
| **P1 — AI-platform engineer** | Runs an agent fleet (Claude Code/Codex/Devin) against company repos; drowning in unreviewable PRs; accountable for what lands | A policy-gated admission pipeline: intents in, attested commits out, with an audit trail and a budget knob | MVP design partner (alpha) |
| **P2 — OSS maintainer** | Flood of AI PRs of unknown quality | Candidate races instead of PR piles; evidence attached to what gets admitted | v1+ |
| **P3 — Applied researcher (us + external)** | Needs an instrumented testbed for selection/scheduling experiments | The only harness that records budget-accounted, state-bound evidence for every candidate | MVP (day one) |

MVP honestly targets **P3 + one or two P1 design partners**. P2 follows once the GitHub App exists.

## 4. Product principles

1. **Oracles outrank agents.** Agent output is a *claim*; only oracle receipts advance lifecycle state (Proof-or-Stop's separation, generalized) [ch. 7, 13].
2. **Evidence is bound or it is noise.** Every receipt names the exact world digest (tree + env + policy) it judged, its freshness basis, and its cost. Unbound evidence is inadmissible at gates.
3. **Every decision is replayable.** Given the ledger, a third party can re-derive why candidate W won under policy P — no hidden state.
4. **Budget is a first-class input.** Every intent carries a budget; every receipt carries a cost; the scheduler's job is to spend the next unit where it buys the most decision quality [ch. 3].
5. **The generator is untrusted.** Threat model includes prompt-injected and reward-hacking generators; scheduler internals are not exposed to generation-side agents; generator and verifier identities are separated [ch. 13].
6. **Git-compatible in, Git-compatible out.** Input: any repo. Output: an ordinary commit + attestation. No lock-in, no forge replacement [ch. 4].
7. **Weak defaults, honest labels.** When only weak evidence is affordable (T0 isolation, stock tests), receipts say so — evidence *tier* is recorded, never inflated [ch. 1, 14].

## 5. Core concepts and data model

All objects are immutable, content-addressed (SHA-256), and stored in the ledger + CAS. Schemas are versioned; wire format is JSON (canonical serialization for digests).

### 5.1 Intent

```
Intent {
  schema: "multiverso.dev/intent/v0",
  base: { commit: <sha>, tree: <digest> },          // exact base state
  spec: { title, description, acceptance: [..] },    // human/machine-readable specification
  preconditions: [..], postconditions: [..],         // checkable where possible
  constraints: { paths_allow/deny, deps_policy, .. },
  budget: { total_usd, max_wall_clock, max_candidates },
  admission_policy: <policy digest>,                 // pins the policy version
  parent: <intent digest | null>                     // version chain
}
```

### 5.2 World

```
World {
  schema: "multiverso.dev/world/v0",
  intent: <digest>,
  parents: [<world digest>],                         // lineage (mutation, repair)
  tree: <git tree digest>,                           // code state
  env: <digest>,                                     // lockfiles, container image digest, env manifest
  isolation_tier: "T0-worktree" | "T1-container" | "T2-vm",   // recorded, never assumed [ch. 1]
  producer: {
    adapter: "claude-code@x.y", model: <string>,
    identity_tier: "claimed" | "receipted" | "attested",       // [ch. 7]
    role: "generator" | "challenger" | "repairer"
  },
  context: <digest>,                                 // prompt/context digest (content in private CAS)
  patch: <digest>,                                   // diff vs base
  trace: <digest>,                                   // transcript/event stream ref
  outcome: COMPLETED | BUDGET_EXCEEDED | INTERRUPTED // producing run's terminal state,
         | CONFIG_ERROR | PROVIDER_ERROR | CRASH     //   normalized per AG-1 [ch. 16]
}
```

### 5.3 Evidence (receipt)

```
Receipt {
  schema: "multiverso.dev/receipt/v0",
  world: <digest>,                                   // binds to exact state
  oracle: { id, version, config: <digest> },         // "tool"/"version" of the envelope
  execution: {                                       // canonical execution envelope (EP-7)
    argv, exit_code, duration_ms, seeds,
    isolation_tier, isolation_caps                   // XP-1: tier recorded in every receipt
  },
  inputs: <digest>,
  result: { status, metrics, artifacts: [<CAS ref>] },
  freshness: { basis: "construction" | "dependency" | "probabilistic",   // [ch. 5, 11]
               valid_for: { tree, env } },           // ≡ envelope's input_tree_hash + env_image_digest
  recheck_tier: "V0-testimonial" | "V1-replayable" | "V2-certified" | "V3-foundational",  // [ch. 14]
  family: <correlation family id>,                   // for correlation-aware weighting [ch. 3, 8]
  cost: { usd, tokens },                             // wall time lives in execution.duration_ms
  producer: <identity>,
  signature: <DSSE>                                  // REQUIRED for any receipt published
}                                                    //   outside the local ledger (FI-1, TP-1)
```

Each envelope field has exactly one canonical location; `artifact_hashes` ≡ `result.artifacts` (content-addressed *before* parsing).

### 5.4 Decision and Attestation

```
Decision {
  schema: "multiverso.dev/decision/v0",
  type: SELECT | REPAIR | REJECT | ESCALATE | ADMIT | REVERT,   // COMPOSE/SERIALIZE reserved for Phase D;
                                                                 // FREEZE (repo-wide admission halt after
                                                                 // incrimination) reserved for v1 [ch. 13]
  intent: <digest>, subject: [<world digest>],
  evidence: [<receipt digest>],                      // exactly what the decision saw
  policy: <digest>, rationale: <structured>,
  scheduler_trace: <digest>                          // allocation log: what was bought and why [ch. 3]
}

Attestation = in-toto Statement (DSSE-signed)
  predicateType: "multiverso.dev/admission/v0"
  subject: the admitted commit (and tree digest)
  predicate: { intent, winning world, decision, evidence closure, policy, budget consumed }
```

The predicate is ours to mint — no standard exists for agent-produced changes [ch. 7]. Signing: local key in v0; Sigstore keyless in v1.

## 6. How it works — user journey (MVP)

The binary is **`mvo`** (package name `multiverso`; collision analysis in [ch. 20]):

```bash
# 0. one-time, in any git repo
mvo init                          # creates .multiverso/ (ledger, config, policy)

# 1. declare the intent
mvo intent new "Fix #482: pagination off-by-one" \
    --budget 25usd --candidates 6 --policy default

# 2. run the race
mvo race <intent-id>
#    → spawns 6 worlds (git worktree or container), each driven by an agent adapter
#    → runs the oracle ladder per policy: O0 collect → O1 suite+coverage [→ O2 properties → O3 mutation]
#    → streams a live scoreboard; every event lands in the ledger

# 3. inspect
mvo worlds <intent-id>            # candidates, evidence summary, costs
mvo evidence <world-id>           # receipts with freshness + tier
mvo explain <intent-id>           # why the leader leads, under which policy

# 4. decide + admit
mvo admit <intent-id>
#    → hard gates → ranking → SELECT winner
#    → rebase winner on current trunk, re-run required oracles on the exact landing tree
#    → emit ordinary git commit + attestation; ledger records the decision
#    → on gate failure: REJECT (with evidence) or ESCALATE (structured handoff to human)
#    → on landing-rebase conflict: never auto-resolve — ESCALATE with the conflict set
#      as structured evidence (M1 default; policy-gated REPAIR world in v1) [CP-8]

# 5. audit / verify (anyone, later)
mvo audit <commit>                # replay the decision from the ledger
mvo verify <commit>               # verify the attestation chain offline
```

Headless mode (`--json`, exit codes) makes every verb CI-scriptable. `mvo race --dry-run` prices the plan before spending.

## 7. Functional requirements

Tagged by architecture plane; **[MVP]** = must ship in v0.1, **[v1]** = productionization, **[R]** = research track (Phase B+).

### Control plane

- **CP-1 [MVP]** Create, version, and digest Intents bound to an exact base state; reject races whose base has drifted unless `--rebase-intent`.
- **CP-2 [MVP]** Orchestrate N candidate worlds per intent under `max_candidates` and total budget; hard-stop on budget exhaustion with a ledger event.
- **CP-3 [MVP]** Fixed scheduler v0: generate N → run oracle ladder (cheap→expensive) → prune on hard-gate failure (successive-halving-shaped, static thresholds).
- **CP-4 [R]** Adaptive scheduler: action set {generate, mutate-candidate, run-oracle(o, w), pairwise-compare, stop+admit, stop+escalate}, chosen by expected decision-quality gain per cost, with correlation-discounted evidence value and sequential admission control (SPRT/e-value family) [ch. 3].
- **CP-5 [MVP]** Policy as versioned artifact: hard gates (ordered), ranking spec (lexicographic keys; no naive weighted sums), escalation rules, required freshness basis per gate [memo §6].
- **CP-6 [MVP]** Decisions (SELECT/REJECT/ESCALATE/ADMIT) recorded with full evidence closure; `mvo explain` renders them; deterministic replay from ledger (`mvo audit`). REPAIR is **schema-reserved in M1**: recorded if manually triggered, never emitted by the v0 scheduler. **M1 ESCALATE payload** (resolves what a human sees): `mvo explain` output + top-k candidate diffs + the failed/unaffordable gates, as structured JSON + terminal rendering; richer escalation UX deferred to M3.
- **CP-7 [v1]** REVERT (consuming post-admission signals [ch. 15]) and repo-wide FREEZE (admission halt after incrimination [ch. 13]) as first-class decisions.
- **CP-8 [MVP]** On landing-rebase conflict, `admit` never auto-resolves: emit ESCALATE with the conflict set as structured evidence (M1 default); in v1, policy may allow spawning a REPAIR world from the winner with the new trunk as base, re-entering the ladder. Non-goals (§2) forbid semantic merge here.

### Data plane

- **DP-1 [MVP]** Append-only, hash-chained ledger (local, embedded DB); every object content-addressed; no in-place mutation ever.
- **DP-2 [MVP]** CAS for artifacts (patches, traces, reports); Git objects reused where natural (trees, patches), sidecar blob store for the rest.
- **DP-3 [MVP]** Privacy split: prompts/context/transcripts stored as digests in the ledger, payloads in local private CAS; nothing sensitive in any shared/public log [ch. 7].
- **DP-4 [v1]** Ledger export/import (bundle) for audit hand-off; optional push of attestations to repo (`refs/attestations/*` or forge store, per ch. 18 findings).

### Execution plane

- **XP-1 [MVP]** `ExecutionBackend` interface; v0 backends: **T0 worktree** (fast, code-only) and **T1 container** (env isolation, devcontainer-aware); tier recorded in every world and receipt.
- **XP-2 [MVP]** Resource caps per world (CPU/mem/time); kill+record on breach.
- **XP-3 [MVP]** Env digest capture: lockfiles + container image digest + declared env vars manifest.
- **XP-4 [v1]** Warm-pool container reuse for spin-up cost; **[v2]** T2 microVM adapter (Morph/E2B-class) for full-state forking [ch. 1].

### Agent plane

- **AG-1 [MVP]** `AgentAdapter` interface with six operations — spawn, events (stream), interrupt, result, diff, transcript — over subprocess-first integration. MVP ships exactly two Tier-1 adapters [ch. 16]: **Claude Code** (`claude -p --output-format stream-json --bare`, native `--max-budget-usd` + `--max-turns`) and **Codex** (`codex exec --json`, sandbox modes; no native caps). Failure taxonomy normalized to `COMPLETED | BUDGET_EXCEEDED | INTERRUPTED | CONFIG_ERROR | PROVIDER_ERROR | CRASH`. OpenHands/Aider/opencode/Jules deferred.
- **AG-2 [MVP]** Budget enforcement is **control-plane-primary** (wall-clock watchdog + event-fed token meter → SIGTERM), tool-native caps secondary where they exist; actual cost captured into receipts. Reported `total_cost_usd` is a client-side estimate — cross-check against provider usage APIs in eval runs [ch. 16, 19].
- **AG-3 [MVP]** Transcript/event capture into CAS as the world's trace. Reproducibility is **evidential, not replay-based** (no seeds exposed anywhere; temperature removed on current Anthropic models): receipts record baseOid + binary patch + transcript digest + model ID + CLI version [ch. 16].
- **AG-4 [MVP]** Diff capture is control-plane-owned: `git worktree add` per candidate, `git add -A && git diff --binary <base>` — never trust agent-reported diffs [ch. 16].
- **AG-5 [MVP]** Candidate diversity controls: per-world prompt variation and model selection from a pool [ch. 2].
- **AG-6 [v1]** Challenger role: agents spawned to attack the leading candidate (adversarial test generation), budgeted like any oracle [ch. 8, 13]. Scheduler/policy internals are never included in generator context [ch. 13].
- **AG-7 [v1]** World-scoped **Multiverso MCP server** as the inbound surface (`get_intent`, `get_policies`, `report_evidence`), injected via `--strict-mcp-config`; denies sibling-candidate and scheduler visibility per the AI-control threat model [ch. 13, 16].

### Evidence plane

- **EP-1 [MVP]** `Oracle` interface: run(world) → Receipt; v0 ladder (Python-first, tool versions verified in [ch. 19]): **O0** build/smoke (`pytest --collect-only`, recording collected-test counts — exit-code-5 "no tests collected" is an adversarial laundering vector), **O1** suite gate (pytest + `--junit-xml` + pytest-reportlog JSONL; coverage.py with dynamic per-test contexts; pytest-rerunfailures with **first-run status preserved** — pass-after-rerun is weaker evidence), plus generic `CommandOracle`.
- **EP-2 [MVP]** Receipts carry: oracle id+version+config digest, execution envelope, result+metrics, artifacts, freshness basis, re-checkability tier, correlation family, cost, producer, and a **DSSE signature whenever published outside the local ledger** (TP-1, FI-1).
- **EP-3 [MVP]** Freshness invalidation v0: receipts valid only for exact {tree, env}; trunk movement invalidates admission-gate receipts (recompute on `admit`).
- **EP-4 [v1]** **O2** property/differential: Hypothesis observability mode (per-testcase JSONL — the most receipt-shaped output in the toolbox, but experimental: pin versions, version the parser) + an intent-level LLM-synthesized property corpus run identically across all N worlds — the **cross-candidate differential oracle** (no prior art *at repository scale on real patches*; function-level precedents: CodeT, SemanticVote, SEP [ch. 8, 19]).
- **EP-5 [v1]** **O3** mutation: async, diff-scoped, budget-capped only (full mutation "does not scale" — Google's production precedent): mutmut (Python), StrykerJS `--incremental` (JS/TS), cargo-mutants `--in-diff` (Rust). Incremental caches are blind to dependency/env changes — a freshness hazard the receipt typing must encode [ch. 19].
- **EP-6 [v1]** Flake handling: rerun policy with first-run status preserved, quarantine list (control-plane-resident, consistent across N worlds), flake-rate recorded per oracle [ch. 19].
- **EP-7 [MVP]** Execution envelope standardized across all oracles as the §5.3 `Receipt.execution` sub-object (canonical field mapping defined there); native artifacts stored content-addressed *before* parsing [ch. 19].

### Trust plane

- **TP-1 [MVP]** DSSE signing with a local key: the ADMIT attestation (our in-toto predicate) **and every receipt published outside the local ledger** (FI-1's self-authentication depends on this).
- **TP-2 [MVP]** Producer identity tiers recorded (claimed/receipted/attested) — honest labeling, no inflation [ch. 7].
- **TP-3 [MVP]** `mvo verify <commit>`: offline verification of the attestation chain against the local/trusted key. **[v1]** Sigstore keyless signing + verification.
- **TP-4 [v1]** Generator/verifier separation enforced: challenger and oracle receipts never produced by the generating agent's session [ch. 13].
- **TP-5 [v2]** Revocation/deny-list: mark evidence from a model/agent/dependency as suspect; re-derive affected admissions (policy-layer, since logs are append-only) [ch. 7].

### Knowledge plane

- **KP-1 [v1]** Cheap impact baseline: touched files + build graph (when available) + per-test coverage map → which oracles a change invalidates [ch. 11].
- **KP-2 [v2]** SCIP ingestion when present. Glean-class store: explicitly deferred.

### Forge integration

Three-stage design [ch. 18]:

- **FI-1 [MVP]** App-less v0, forge-neutral: candidates published to `refs/multiverso/intent/<id>/cand/<n>` (custom ref namespaces work on GitHub — GitButler's `refs/gitbutler/*` is shipping precedent — but rulesets don't protect them, so **receipts must be self-authenticating**: DSSE-signed per TP-1, content-addressed, never location-trusted). Admission = fast-forward push with a `Multiverso-Receipt` digest trailer; freshness by merge-base drift polling. Push-exclusion refspecs + retention policy for rejected worlds required. **Protected-trunk assumption for M1**: admission requires either a bot identity granted ruleset bypass on the target branch, or an unprotected integration branch; protected-trunk gating arrives with FI-2/FI-3. This is a design-partner onboarding prerequisite (§10).
- **FI-2 [v1]** Interim gate: fine-grained-PAT commit status `multiverso/admission` made required in branch protection (statuses are not App-gated; spoofable by push access — documented as interim only).
- **FI-3 [v1]** GitHub App (mandatory for Checks API): permissions `contents:write, checks:write, pull_requests:write, statuses:write, attestations:write, merge_queues:read`; one check run per PR head (race table in `output.summary`, ≤3 action buttons: re-race/repair/explain); ruleset pins the App as expected source so no other integration can spoof the context. **Merge queue from day one**: on `merge_group.checks_requested`, re-verify against the speculative queue head — GitHub's queue forces exactly the evidence-freshness problem we type. Throttle live check-run updates (~1/min; 500 content-writes/hr secondary limit ≈ 40 races/hr/installation).
- **FI-4 [v1]** Attestations: canonical receipts live **in git**; GitHub's attestations store is a mirror (App upload works with `attestations:write`; `gh attestation verify` assumes Actions OIDC, so we ship our own `mvo verify`). GitLab (Ultimate external status checks) / Gitea (AGit) via a 3-verb forge-adapter interface in v2.

## 8. Non-functional requirements

- **NFR-1 Reproducibility**: `mvo audit` replays any decision bit-for-bit from ledger + CAS on another machine.
- **NFR-2 Overhead**: control-plane overhead (ledger + orchestration, excluding agents/oracles) ≤ 5% of race wall-clock; worktree world creation < 1s; container world < 30s cold, < 5s warm.
- **NFR-3 Integrity**: ledger corruption detectable (hash chain); partial-failure races resumable; no zombie worlds (GC verb).
- **NFR-4 Security**: worlds have no default network egress in T1; secrets never enter world context unless policy-allowed; transcripts treated as sensitive by default.
- **NFR-5 Cost visibility**: every verb can `--dry-run` a price; every race ends with a cost report (per world, per oracle, per decision).
- **NFR-6 Single-binary install**: no daemon required for MVP; works offline except agent API calls.

## 9. Architecture

```
                        ┌────────────────────────────────────────────┐
                        │              multiverso CLI                │
                        │  intent · race · worlds · evidence ·       │
                        │  explain · admit · audit · verify          │
                        └───────┬────────────────────────────────────┘
                                │
   ┌───────────────┬────────────┼──────────────┬────────────────┐
   │               │            │              │                │
┌──▼───────┐  ┌────▼─────┐  ┌───▼──────┐  ┌────▼───────┐  ┌─────▼──────┐
│ Scheduler │  │ Policy   │  │ Ledger   │  │ CAS        │  │ Attestor   │
│ v0: fixed │  │ engine   │  │ append-  │  │ blobs +    │  │ DSSE/      │
│ v1: VOI   │  │ (gates,  │  │ only,    │  │ git objs   │  │ in-toto    │
│ [CP-3/4]  │  │ ranking) │  │ hash-    │  │            │  │ predicate  │
└──┬───────┘  └──────────┘  │ chained  │  └────────────┘  └────────────┘
   │                        └──────────┘
   │ orchestrates
   ├────────────────┬─────────────────────┐
┌──▼─────────────┐ ┌▼──────────────────┐ ┌▼───────────────────┐
│ AgentAdapter   │ │ ExecutionBackend  │ │ Oracles            │
│ claude-code v0 │ │ T0 worktree v0    │ │ build · test ·     │
│ codex v0 ...   │ │ T1 container v0   │ │ coverage · command │
└────────────────┘ │ T2 microVM v2     │ │ mutation/diff v1   │
                   └───────────────────┘ └────────────────────┘
                   RepositoryBackend: git v0 (jj adapter later) [ch. 4]
```

All five interfaces (`AgentAdapter`, `ExecutionBackend`, `Oracle`, `RepositoryBackend`, `Attestor`) are the extension surface; everything else is core.

**Stack decision** [ch. 17]: **Go**, for one decisive reason — the trust plane. `sigstore-go` is the only stable, conformance-passing Sigstore client with attestation support (sigstore-rs is pre-1.0 and can't verify attestations; in-toto-rs warns against production use); `go-tuf` v2 is the official TUF client; go-witness proves the full DSSE + in-toto + Fulcio + OPA chain works in Go in production; Rekor v2/Tessera (Go) can be embedded later as a shared verifiable log.

- **Git/jj**: shell out to `git` (and optionally `jj`) CLIs — go-git's worktree support is partial and its merge is FF-only; jj-lib has no stable API. `jj run` (private working copy per change) is a ready-made fast path for cross-world oracle execution.
- **Ledger**: SQLite in WAL mode (single writer, non-blocking readers), hash-chained append-only events table, via CGo-free `modernc.org/sqlite` (fallback contingency: mattn/go-sqlite3); Litestream for backup. The ledger-open path refuses network filesystems at startup (WAL hazard).
- **CAS**: Git's own object DB stores world state pinned under `refs/multiverso/*`. Ref semantics, stated once: local pin refs are excluded from *default user pushes* and from GC; `mvo` performs explicit tool-driven pushes of candidate/attestation refs per FI-1; rejected-world refs are pruned per FI-1's retention policy. Evidence payloads live in a SHA-256 file CAS + SQLite index (git is still SHA-1; ledgers shouldn't travel on push); in-toto ResourceDescriptors bind both digests.
- **Execution**: official Docker Go SDK; container caps are the uniform macOS/Linux isolation tier (macOS has no cgroups — bare-host runs are uncappable and get recorded as such); devcontainer CLI as subprocess.
- **Distribution**: single static binaries via GoReleaser. TypeScript only for future UI; Python only for the eval harness.
- **Do NOT build**: a git implementation, jj-lib bindings, envelope formats, a transparency log, a container runtime, release tooling.

Per-language agent-reliability evals are noisy and benchmark-dependent (Rust 58% vs Go 31% on one benchmark, Rust 16% vs Go 7% on another [ch. 17]), so the tiebreaker is review cost and deterministic feedback (gofmt/vet/race). A cheap week-1 bake-off (same task, N agents, Go vs Rust) runs in M0 before the choice calcifies.

## 10. MVP scope — "Candidate Race v0" (Milestone M1)

**In**: CP-1/2/3/5/6/8, DP-1/2/3, XP-1/2/3, AG-1/2/3/4/5, EP-1/2/3/7, TP-1/2/3(local), FI-1. One repo language tier (Python-first [ch. 19]; Windows targets explicitly excluded — mutmut requires fork), Claude Code + Codex adapters, T0+T1 execution, fixed policy, local signing.

**Out (explicitly)**: adaptive scheduler (M2), O2/O3 oracles (M2), GitHub App (M3), further adapters (OpenHands/Aider/Jules), microVMs, multi-intent anything, any UI beyond CLI.

**Design-partner onboarding prerequisite** (FI-1): a bot identity with ruleset bypass on the target branch, or an unprotected integration branch.

**M1 acceptance criteria**:
1. On a real repo, `intent → race(6) → admit` end-to-end with every decision replayable via `mvo audit` on a second machine.
2. 30 SWE-bench-Live instances run under budget accounting with zero unbound evidence (every receipt names its world digest).
3. A design partner completes `init → intent → race → explain` on their own repo using only shipped docs, with at most one support interaction.
4. Candidates for one intent visible under `refs/multiverso/*` on a GitHub remote; admission lands via fast-forward push with a valid, verifiable receipt trailer (`mvo verify` passes).

## 11. Evaluation plan (Milestone M2 — the paper)

Implements the corpus protocol per [ch. 9] — this section summarizes; ch. 9 governs:

- **Benchmarks**: SWE-bench-Live primary (post-training-cutoff instance dates are the primary contamination defense; monthly +50-issue refresh slice) + Multi-SWE-bench mini + Commit0. Sample size: ≥300 instances/stratum for the SWE-bench-Live and Multi-SWE-bench strata [ch. 9]; Commit0 (54 libraries) is an exploratory stratum reported without the power claim.
- **Three-tier oracle**: stock tests / strengthened + PatchDiff-style differential / stratified manual adjudication. Headline numbers on Tier 2.
- **Hidden-oracle discipline**: Tier-2/3 suites frozen and hashed *before* scheduler tuning; the scheduler and all development iterations never see them; dev uses a disjoint split — otherwise the experiment measures oracle leakage, not scheduling [ch. 9].
- **Budget-matched arms** (equal B = tokens + runner + oracle + selection): (1) single agent, (2) best-of-N random, (3) best-of-N public tests, (4) best-of-N LLM judge, (5) test-voting (CodeMonkeys-style), (6) Multiverso adaptive, (7) retrospective oracle (upper bound). **Three budget levels per instance** to trace cost/accuracy Pareto frontiers [ch. 9].
- **Metrics**: TCAR, FAR (+ FAR-adv under injected adversarial generators, with declared red-team affordances, reported as upper bounds [ch. 13]), cost per true-correct merge, wall-clock, selection regret, calibration, evidence waste, post-integration regressions, model-swap stability.
- **Statistics**: paired McNemar + bootstrap CIs, scheduler ablations, pre-registered protocol (public in this repo).
- **Success bar** (memo §11): at equal budget, beat fixed best-of-N *and* test-voting on both TCAR and FAR with CIs; anything less is a null result we publish anyway.

## 12. Roadmap

| Milestone | Contents | Exit criterion |
|---|---|---|
| **M0 — Walking skeleton** (~2–4 wks, Aug–Sep 2026) | Ledger + CAS + intent/world objects + T0 worlds + one oracle + `race` with N=2, no agents (scripted patches). **Week-1 checklist**: claim package names (`multiverso` + `mvo` on crates.io/PyPI/npm) [ch. 20]; probe GitHub custom-ref durability at 1k+ refs and the attestations-API cert-chain validation [ch. 18]; Go-vs-Rust agent bake-off [ch. 17]; commission trademark knockout search (classes 9/42) [ch. 20]. **Design-partner recruitment: owner Karim, target ≥1 committed partner by end of M0** | `mvo audit` replays a toy race deterministically |
| **M1 — Candidate Race v0** (Phase A, target Q4 2026) | Full MVP scope (§10) | M1 acceptance criteria |
| **M2 — Adaptive scheduler + eval** (Phase B, target Q1–Q2 2027) | CP-4, EP-4/EP-5 oracles (O2 + O3), AG-6 challenger, eval harness, the paper | Success bar of §11, or published null |
| **M3 — Exact-state admission + App** (Phase C, target Q3 2027) | *Delta over M1's EP-3 (which recomputes all admission-gate oracles on every admit)*: minimal re-verification — only re-run oracles the drift actually invalidates, via KP-1 impact maps; merge-queue speculative-head re-verification (FI-3); GitHub App check gate; Sigstore (TP-3 keyless) | Design partners gating real PRs |
| **M4 — Fleet + trust** (Phases D/E) | Multi-intent claims/composition, revocation, KP-2 knowledge plane | TBD after M2 results |

### License & OSS posture [ch. 20]

- **License**: **Apache-2.0** for everything published (the entire reference class — Sigstore, in-toto, SLSA, Zuul, jj — is Apache-2.0; the patent grant matters in attestation territory; CNCF/OpenSSF donation paths require it). **DCO per-commit, explicitly no CLA** — a credible no-rug-pull commitment; this deliberately forecloses a future FSL/BUSL flip, so paid components are closed-SaaS from day one, never source-available core.
- **Open-core line**: open = CLI, engine, receipt format + verifier, in-toto predicate, local single-repo ledger, adapters. Paid (later, honestly closed) = hosted multi-repo evidence ledger, org dashboard, policy management, SSO/SCIM. **Receipt verification free everywhere, forever.**
- **Repo**: monorepo (research/ + spec/ + code) under `coagente/multiverso`; split `multiverso-spec` only when a donation conversation or second implementation exists. Roadmap as one pinned meta-issue.
- **Naming**: "Multiverso" holds (English "Multiverse" is double-taken by $2B+ companies; `multiverso` collisions are dormant/archived). Binary `mvo`. Trademark knockout search pending (M0).
- **Launch staging** (Antithesis/Sapling pattern): corpus public (done) → deep technical post + runnable `mvo race` demo + receipt spec v0 the same day → the measured TCAR/FAR number as the headline when M2 lands.

## 13. Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Incumbent assembles the combination first (12–24 mo window) [ch. 10, 12] | Medium | Ship wedge fast; publish formats early (G3); the scheduler + FAR result is the moat, not the architecture |
| Adaptive scheduler doesn't beat baselines (G2 null) | Real | Protocol publishes either way; product value (audit, budget, admission gate) stands on M1 without M2 |
| Oracle weakness → false admissions in the wild | High impact | Honest evidence tiers, three-tier eval, adversarial arm, ESCALATE as default under low evidence |
| Agent CLI churn (flags/APIs move fast) | High | Thin adapters, pinned versions, conformance tests per adapter |
| jj instability tempts us into deep coupling | Low | RepositoryBackend interface; Git shell-out first; jj adapter only post-M2 [ch. 4] |
| Reward-hacking generators game our own oracles | Medium | Ch. 13 threat model from day one: hidden internals, generator/verifier separation, FAR-adv measured; O0 records collected-test counts against test-laundering [ch. 19] |
| Custom-ref durability on GitHub unproven (GC, limits, Enterprise policies) [ch. 18] | Medium | Week-1 probe; evidence mirrored to a Multiverso-controlled remote as contingency |
| Trademark conflict (Multiverse Computing / Multiverse Ltd, classes 9/42) [ch. 20] | Low-Med | Knockout search in M0 before the name becomes load-bearing; `mvo` binary is clean either way |
| Solo-ish team, big surface | High | Agent-built development with this very control loop as dogfood target; ruthless non-goals (§2) |

## 14. Open questions

*Resolved since draft*: stack (Go, §9), license (Apache-2.0 + DCO, §12), naming (`Multiverso`/`mvo`, §12), attestation storage (canonical in git, forge store as mirror, FI-4), M1 ESCALATE payload (CP-6), landing-rebase conflicts (CP-8).

| # | Question | Owner | Decide by |
|---|---|---|---|
| 1 | Trademark clearance for "Multiverso" (registry availability ≠ clearance) [ch. 20] | Karim | end of M0 |
| 2 | Mutation-cost priors: bootstrapped mutant-count × kill-time estimates so the scheduler can price O3 (only published heuristic: cargo-mutants' 5×-baseline timeout) [ch. 19] | eng | M2 design |
| 3 | How much of M2's adversarial arm (challenger agents) is affordable at MVP oracle prices? | eng | M2 design |
| 4 | Richer escalation UX beyond CP-6's M1 payload — and is escalation review a product surface in itself? | Karim | M3 design |
| 5 | When to propose the in-toto predicate upstream (in-toto/attestation) for vendor-neutral legitimacy: draft in-repo during M1, propose after spec v0 ships [ch. 17] | Karim | M1 exit |
| 6 | Acquisition posture: hosted-ledger business independent or strategic to a lab? (Astral→OpenAI, Bun→Anthropic precedents [ch. 20]) | Karim | before M3 partner/fundraise conversations |

## 15. Glossary

**Intent** — versioned, digest-addressed specification of a desired change bound to a base state. **World** — one isolated candidate: code + env + producer + trace + lineage. **Receipt** — oracle output bound to a world digest with freshness, tier, family, and cost. **Decision** — SELECT/REPAIR/REJECT/ESCALATE/ADMIT/REVERT with its evidence closure. **Attestation** — signed in-toto statement binding intent, winner, evidence, and policy to the admitted commit. **Oracle ladder** — cheap→expensive verification rungs. **TCAR/FAR** — true-correct admission rate / false-admission rate. **Freshness basis** — how a receipt's validity was established (construction/dependency/probabilistic). **Re-checkability tier** — V0 testimonial → V3 foundational proof.

---

*This PRD is a living document, built in public. Corpus: [research/](research/). Errors survived two adversarial review waves — if you find one, [open an issue](https://github.com/coagente/multiverso/issues).*
