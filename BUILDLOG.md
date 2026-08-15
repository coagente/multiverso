# Build Log

> Public journal of building Multiverso. Newest first. See [PRD.md](PRD.md) for the plan; milestones M0–M4.

## 2026-08-14 — M1d: forge publication (`mvo publish` / `fetch-race` / `prune`)

**Evidence now travels.** `mvo publish <intent>` puts a race's full closure on any git remote under `refs/multiverso/intent/<short>/` — candidate refs whose commits are *pure functions of content* (epoch-pinned timestamps, fixed identity: idempotent republish structurally diffs to zero) and one evidence commit whose tree holds the intent, policy, worlds, DSSE-signed receipts and decisions (TP-1: raw DSSE over canonical bytes, payload-type domain-separated by PAE), and — when admitted — the attestation bundle verbatim. `mvo fetch-race <short>` is the second machine's verb: workspace-less, ledger-less, it mirrors the namespace into any clone and verifies everything offline against one explicit public key — content addresses recomputed, signatures checked, the cross-reference graph closed, candidate commits pinned to signed-cited world trees, and **every decision replayed through the shipped `race.Decide`/`admit.Decide`** (a wrong-winner decision signed with the right key still fails). Failures are collected, never fail-fast: each bad item prints `mvo: fetch-race: <path>: <detail>`. `mvo prune` applies FI-1 retention — losers always prunable, an admitted intent keeps winner + evidence unless `--keep-admitted=false`, `--older-than` guards against pruning what was just published — and never touches CAS or ledger.

Design decisions worth reading in [docs/design/M1d-publication.md](docs/design/M1d-publication.md): nothing is location-trusted (the probe showed anyone with push access can move these refs, so self-authentication is the whole trust story — ref names are discovery only); push discipline is explicit per-ref refspecs under `--force-with-lease` CAS with plan-diff + namespace reconciliation, making "namespace ⊆ plan" an invariant fetch-race can treat any stray ref as tamper evidence; signatures live in envelopes, never inside signed objects (sign-on-publish, byte-stable re-minting); DP-3 holds — prompts and transcripts never leave the private CAS. Trunk drift became a render-time display line (`freshness: FRESH|STALE|UNKNOWN`) on `worlds`/`explain`/fetch-race — never a ledger mutation; EP-3's recompute-on-admit stays the enforcement point.

Tests: full tamper matrix against local bare remotes (flipped receipt bytes, swapped filenames, re-digested decisions, doctored unsigned worlds caught by the signed decision's subject pin, stray refs, missing winner, tree swaps, wrong key, tampered/mis-budgeted attestations), lease-failure surfacing, publication-event goldens, prune policy table, five-row drift table. Acceptance now runs publish → idempotent republish → include-rejected delta → clone + fetch-race → in-remote receipt tamper (fails naming the exact path) → healing republish → retention prune → drift markers, all against a bare remote in a temp dir. The network is never touched.

Review round, 9 findings, 8 applied: two real **BLOCKER**s. (1) The published world set was unbound — worlds are the one closure kind authenticated by digest alone, and the graph only checked the forward direction, so anyone with push access could splice a self-consistent World naming attacker-authored code into the evidence commit, publish it as `cand/<n>`, and have fetch-race call it verified. Fixed by reverse containment: every published world must be a subject of a signed decision, every published policy must be the intent's, and one world may claim only one candidate ref. (2) `git ls-remote` tail-matches its pattern from any slash boundary, so a namespace survey returned refs merely *containing* the namespace path — and publish/prune turn survey output into `:<ref>` delete refspecs, silently destroying a `refs/heads/refs/multiverso/…/wip` branch or a look-alike tag. Fixed by anchoring `gitx.LsRemote` to the pattern's fixed prefix, plus an assertion in `gitx.Push` that turns decision 10's "refs/heads cannot appear by construction" into an enforced refusal. Also: `git push` is not atomic by default (a mixed batch really did land some refspecs while `publish.finished` recorded `pushed: []` — now `--atomic`, so the ledger's claim and the remote agree), and prune deleted the local GC pins *before* surveying the remote, so a remote-leg failure wiped them with zero audit trail (now: both surveys and a remote pre-flight first, and `prune.executed` is appended on every path that mutated anything).

## 2026-08-13 — M1c: T1 containers + parallel races

**Worlds can now run confined, and races run wide.** `mvo race --exec T1 --exec-image REF` puts every untrusted process — the agent and the oracle — inside a docker keeper container: digest-pinned image (TOCTOU-free: what was recorded is what runs), `--network none` by default (NFR-4, *proven* by an in-container egress probe whose passing receipt is the recorded evidence), `--cap-drop ALL`, read-only root with only `/tmp` and the bind-mounted `/work` writable, and per-world CPU/memory/pids caps that land in every receipt's new always-serialized `isolation_caps` (XP-2: enforcement you can audit). `--parallel N` runs the race as a two-phase bounded worker pool — generate, barrier, verify — where `--parallel 1` reproduces the sequential schedule *structurally*, decision inputs are digest-sorted before `Decide` (permutation-invariance now pinned by a property test), and audit replay needs zero changes.

Architecture per [docs/design/M1c-containers-parallel.md](docs/design/M1c-containers-parallel.md): the PRD §9 `ExecutionBackend` surface is `internal/backend` (T0-worktree byte-compatible with M1b — T0 world digests do not move — plus T1-container) over `internal/dockerx`, a gitx-style docker CLI shell-out (deliberate deviation from the PRD's Docker SDK line: `go.mod` stays untouched, adapters stay thin). One long-lived `sleep`-keeper container per world holds the pid namespace; agent and oracle join it via `docker exec`, so the watchdog's `World.Kill()` (`docker kill` → PID-1 teardown) reliably ends everything the tier confines. Worktrees are bind-mounted, never copied — host-side diff/transcript capture (AG-3/AG-4) runs unchanged, and the worktree's dangling `.git` pointer means an in-container agent cannot even see the shared repository. Containers are transport, not evidence: no container ID ever enters the ledger; the pinned image digest rides the XP-3 env manifest into every T1 receipt's `valid_for.env`.

Docker-gated tests skip cleanly when no daemon is reachable (CI) and run against a local fixture image (`python:3.12-alpine` + bash + pytest + the fake agents baked in — built once, never pushed, nothing larger ever pulled); the fake claude demonstrably runs *inside* the container while capture stays control-plane-owned. The parallel orchestrator's tests run under `go test -race`. Acceptance now includes a `--parallel 2` race whose decision type and winner match the serial run, and a T1 race whose suite receipt records `T1-container` + `network:none` — with a graceful skip when docker is absent. XP-4 warm pools stay deferred (v1), with `Backend.Open` deliberately shaped so a pool can arrive without a contract change.

## 2026-08-13 — M1b: real agents drive the race (Claude Code + Codex adapters)

**Worlds are now agent-produced.** `mvo race --agent claude-code|codex|script` spawns each candidate through an `AgentAdapter` whose shape enforces the trust rules at compile time: **Diff and Transcript are not adapter methods** — the control plane captures the diff itself (`git add -A && git diff --binary --cached <base-tree>`, after verifying the worktree's git state wasn't tampered with by the agent) and tees the raw event stream into CAS as the authoritative transcript. Agent-reported anything is never trusted.

Highlights from [docs/design/M1b-agent-adapters.md](docs/design/M1b-agent-adapters.md): six-value outcome taxonomy with control-plane-kill precedence (watchdog → BUDGET_EXCEEDED beats whatever the stream claims); costs as integer micro-USD tagged `client-estimate` (no floats in canonical JSON, no invented price tables — codex worlds honestly carry `usd_micro:0` + tokens); prompts rendered from a normative template with a negative test asserting **no policy digest, gate names, or oracle argv ever reach an agent prompt** (the AI-control rule, ch. 13); agent failure is *evidence* (a recorded world), machinery failure is an *error* (the race aborts). Budget enforcement is control-plane-primary: process-group SIGTERM → 5s grace → SIGKILL.

Testing without burning money: 8-mode fake-CLI fixtures (`testdata/fakeagent/`) cover the full outcome matrix; after the fixture PATH override, tests assert `exec.LookPath` resolves to the fixture and **fail closed** if the real CLI could leak through. Live smoke exists but only behind `MVO_LIVE_AGENT_TEST=1`.

Review round earned its cost again: 7 findings, including a real **BLOCKER** — the SIGKILL escalation skipped the process *group* when the leader had already exited, letting orphaned grandchildren keep spending. Fixed, plus evidence-capture failures now produce CRASH worlds instead of aborting the race.

## 2026-08-13 — M1a: signed admission lands (`mvo admit` + `mvo verify`)

**The trust plane exists.** `mvo admit` takes a race's SELECT decision, applies the winner onto the *current* trunk in a temp worktree, re-runs the suite against the exact landing tree (EP-3 v0: recompute-on-admit), and — only if the gate passes — commits via git plumbing with a `Multiverso-Attestation: sha256:…` trailer, recording an ADMIT decision plus a DSSE-signed in-toto attestation (predicate `multiverso.dev/admission/v0`). `mvo verify <commit>` re-checks everything offline: 7 fail-fast checks from bundle digest through signature, subject binding, reference existence, evidence freshness against the commit's tree, and budget consistency. Conflict on landing → ESCALATE with the conflict set as receipt artifacts (CP-8: never auto-resolve). Rebase-race safety via compare-and-swap `update-ref` — if trunk moves mid-admit, the admission records `result=ERROR` and is retryable.

Design decisions worth reading in [docs/design/M1a-admit-signing.md](docs/design/M1a-admit-signing.md): the attestation subject binds **landing tree + parent commit** (a trailer-referenced statement can't contain its own commit hash — fixed-point impossibility); the landing gate is a **pure function** of (policy, apply-receipt, gate-receipt), so `mvo audit` replays admissions exactly like race decisions; the landing oracle argv is recovered from the winner's race receipt, never from a flag — no gate-swapping at admit time.

Pipeline: architect → builder → integrator → 2 adversarial reviewers (crypto lens found zero real defects; conformance lens: 2 MINOR, both fixed) → fixer. Ed25519 + DSSE + PAE all stdlib, golden-vector tested against the DSSE spec. Keys live in `.multiverso/keys/` (0600), never committed.

## 2026-08-13 — M0 walking skeleton: built, reviewed, green

**The skeleton walks.** `mvo init → intent new → race → explain → audit` works end-to-end on the toy fixture: two candidate worlds from scripted patches, a suite oracle per world, a pure decision function that SELECTs the passing candidate, and an audit that replays every decision from the hash-chained ledger byte-for-byte — and fails loudly when a ledger byte is flipped.

**How it was built** (all agent-built, human-supervised, in one 70-minute pipeline):
- **Go-vs-Rust bake-off** (same storage-core spec, one agent each, judge re-ran both suites + a 10-case cross-implementation canonicalization probe): both implementations passed everything; canonical bytes were byte-identical across languages in 9/10 edge cases. **Verdict: narrow Go win** (clarity, test quality; Rust's minimal-dep constraint forced ~45 lines of hand-rolled hex/date code). One Rust idea back-ported: reject floats in canonical JSON per DP-1. PRD's Go default stands, now with evidence.
- **Build**: storage (object/cas/ledger) → engine (gitx/oracle/race/workspace) ∥ CLI (cmd/mvo + toyrepo fixture + acceptance script) → integrator (module compiled clean on first assembly).
- **Adversarial review**: 2 reviewers, 14 real findings — best catches: `mvo audit` printed OK even when replay diverged; `VerifyChain` couldn't detect deleted tail rows (fixed: seq contiguity + AUTOINCREMENT high-water cross-check); `GIT_DIR`/`GIT_INDEX_FILE` env leakage into gitx subprocesses; worktree path collisions across races. All fixed, 4 skips with written justification.
- Full gate verified independently after the workflow: `gofmt` / `go vet` / `go test ./...` (8 packages) / `scripts/m0-accept.sh` — all green.

**M0 exit criterion status**: audit-replay determinism ✅ (acceptance step 4 copies the workspace to a second location and replays). Remaining M0 items are the human-gated week-1 tasks below.

## 2026-08-13 — M0 kickoff

**Done today:**
- Project scaffolding: Go module (`github.com/coagente/multiverso`), Apache-2.0 LICENSE, DCO contributing policy, CI (build/vet/fmt/test), this build log.
- [M0 design doc](docs/design/M0.md): object schemas, ledger/CAS contracts, CLI verbs, acceptance script for the walking skeleton.
- Week-1 checklist progress:
  - [x] GitHub custom-ref durability probe → [docs/probes/github-custom-refs.md](docs/probes/github-custom-refs.md) — 1.2k refs fine; **finding**: GitHub's attestation store rejects self-signed bundles (tlog inclusion required) → mirror is v1+, git stays canonical
  - [x] Go-vs-Rust agent bake-off → narrow Go win, see entry above
  - [ ] **Needs Karim**: claim package names (`multiverso` + `mvo` on crates.io / PyPI / npm) — requires account credentials
  - [ ] **Needs Karim**: commission trademark knockout search for "MULTIVERSO", classes 9/42
  - [ ] **Needs Karim**: design-partner recruitment (target ≥1 committed partner by end of M0)

**M0 scope** (from [PRD §12](PRD.md)): ledger + CAS + intent/world objects + T0 worktree worlds + one oracle + `mvo race` with N=2 scripted patches (no agents yet). Exit: `mvo audit` replays a toy race deterministically.

---

## 2026-08-12 → 13 — Research + PRD

- 20-chapter research corpus (305 primary sources), 13/13 founding-memo claims verified against primary sources.
- PRD v1.0 approved for development after a 3-lens adversarial review panel (30 findings, all dispositioned).
- Headline: nobody ships >2 of Multiverso's 5 capabilities; the white space is the evidence-aware scheduler; window ~12–24 months.
