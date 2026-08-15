# Multiverso

> **Git versions accepted history. Multiverso versions the possibilities not yet accepted — and the evidence used to decide which one becomes history.**

**Multiverso** is an evidence-native, Git-compatible control plane for speculative software change produced by AI agents.

Given a versioned *intent*, Multiverso creates isolated candidate *worlds*, records their lineage, accumulates *evidence* cryptographically bound to the exact state of each candidate, and decides between them under a policy pinned before any candidate existed. Its decision function selects, rejects, or escalates — and admission re-runs the gates on the exact tree that will land, then signs an attestation for it.

Multiverso does not replace Git. It versions possibilities, evidence, and decisions *before* they become accepted history.

## Sixty seconds

Two candidate patches for one bug. One fixes it; the other fixes it and quietly breaks a second function.

```console
$ mvo init --dir "$DEMO"
initialized /tmp/mvo-demo/.multiverso (default policy mv0:c1f0b72f24aec3968626c782b5f0cc540124f35131cebaa6f3de7ff3b2a60487)

$ INTENT="$(mvo intent new --dir "$DEMO" --title "fix mean()" --desc "mean() divides by len-1")"
$ mvo race "$INTENT" --dir "$DEMO" --agent script --patches "$DEMO/patches"
SELECT mv0:18ba934906021f6660486bf0e41f966668a7615021707b83c8263349683cd41a (decision mv0:03a60e95…, 2 worlds)

$ mvo explain "$INTENT" --dir "$DEMO"
type:      SELECT
policy:    mv0:c1f0b72f…  (default, policy/v1)
winner:    mv0:18ba9349…

gates (ordered, first failure stops the ladder):
  RANK  WORLD          ORD  collect-nonempty@collect  collected-not-below@collect  status-pass@suite   RESULT
  1     mv0:18ba9349…  1    pass (total=8)            pass (delta=+0)              pass                PASS
  2     mv0:ad21cf57…  2    pass (total=8)            pass (delta=+0)              FAIL (status=fail)  FAIL

why mv0:18ba9349… won  (ranking [gate_pass, tests_passed_desc, wall_ms_asc, world_digest_asc]):
  vs mv0:ad21cf57… (rank 2): decided at key 1 gate_pass (pass > fail)

$ mvo admit "$INTENT" --dir "$DEMO"
ADMIT mv0:18ba9349…
commit:      f5b6bf4361af9802d7380c04463db9b0e304563d
attestation: sha256:cf3eabf82092483dda59807a0858d25f2be24d5582e6dab1a679b5aa5f474cb9

$ mvo verify HEAD --dir "$DEMO"
OK: attestation verified (7 checks)

$ mvo audit --dir "$DEMO"
OK: 25 events, 2 decisions replayed
```

*(Real output from the shipped `testdata/toyrepo` fixture. `$DEMO` is a scratch copy of it and `$INTENT` the digest `intent new` printed — the [quickstart](docs/quickstart.md) sets both up, command by command, and shows the lines elided here: `explain`'s full evidence block and rationale. Digests are truncated for width throughout; mvo itself always prints all 64 hex characters.)*

The interesting run is the one where a candidate goes green by **deleting tests**. The collected-count gate is ordered before the suite gate, so the ladder short-circuits and the cheating candidate's suite is never even run:

```console
  RANK  WORLD          ORD  collect-nonempty@collect           collected-not-below@collect               status-pass@suite  RESULT
  1     mv0:c50f7b96…  1    pass (total=8)                     pass (delta=+0)                           pass               PASS
  2     mv0:1721a328…  2    pass (total=5)                     FAIL (collected_delta=-3 (tolerance -0))  n/a                FAIL
  3     mv0:37796ba0…  3    FAIL (collected_total=0 (exit 5))  n/a                                       n/a                FAIL
```

### What the ladder catches, and what it does not

Test deletion is **one** laundering vector, and in M1 it is the only one the default policy catches. All of the following were verified against the shipped fixture and sail through as `PASS`:

| Vector | Caught by the default policy? |
|---|---|
| Delete tests (functions, or the whole file) | **yes** — `collected_delta` drops, or exit 5 |
| `@pytest.mark.skip` the failing tests | **no** — skipped tests are still collected; `delta=+0`, exit 0 |
| Weaken the assertions | **no** |
| Rewrite assertions to match the candidate's own wrong output | **no** — and in our four-candidate run this one *won* |
| A `conftest.py` overriding the process exit code | **no** — `status` reads the exit code; the same receipt records `tests_failed=2, status=pass` |
| A `conftest.py` writing the JUnit report the oracle reads | **no** — every metric fabricated |

The structural reason: **pytest-suite metrics are parsed from an artifact written inside the candidate's own writable tree**, so a candidate that runs code can author its own receipt numbers. Adding a `no-failed-tests` gate closes the exit-code override today (verified); skips, weakened assertions and forged reports need M2 (a `skipped-not-above` gate, O3 mutation, and an oracle that runs outside the candidate's write surface).

**→ [The full itemized table, with the run that produced it](docs/concepts.md#what-the-gate-ladder-catches-and-what-it-does-not)**

**→ [Quickstart](docs/quickstart.md)** — run all of this yourself in about five minutes, with no API keys and no network.
**→ [Concepts](docs/concepts.md)** — Intent, World, Receipt, Decision, Attestation, Policy, and why this exists when you already have CI.

## Core research question

> Under a fixed total budget, can an evidence-aware scheduler dynamically allocate compute between generation, testing, and adversarial challenge to admit changes with higher true correctness and a lower false-admission rate than a single agent, a fixed best-of-N, or a test-only selector?

**This question is not yet answered, and M1 does not attempt it.** M1's ladder and candidate count are fixed. The adaptive scheduler and the evaluation that would answer the question are M2. Everything shipped today is the substrate that makes the experiment possible: bound evidence, pinned policies, replayable decisions.

## Primitives

```text
Intent → N Worlds → Evidence → Decision → Admission
```

| Primitive | What it is | Ships in M1 |
|---|---|---|
| `Intent` | Versioned specification bound to a base commit + tree, with a budget and a **pinned** policy digest | yes, minus the `parent` version chain |
| `World` | An isolated candidate: code tree + env digest + producer identity + patch + transcript | yes |
| `Evidence` | Oracle output cryptographically bound to an exact world digest, with freshness basis, metrics and cost | yes; receipts carry no producer identity or dollar cost |
| `Decision` | `SELECT` \| `REJECT` \| `ESCALATE` \| `ADMIT` | yes. `REPAIR`, `COMPOSE`, `SERIALIZE`, `REVERT` are **not** implemented |
| `Attestation` | DSSE-signed in-toto Statement linking intent, world, decisions, evidence closure and policy to the landing tree | yes |

## Status

**M1 (Candidate Race v0) is code-complete and green** — 16 Go packages under `go test ./...`, plus `scripts/accept.sh` end to end. It is **not production software**: this is a research prototype, built in public, with the gaps below stated rather than hidden.

The two tables below are an audit against [PRD §7](PRD.md#7-functional-requirements), requirement by requirement. **Complete** means the shipped behaviour matches the requirement's full text. **Partial** names exactly what is missing — including the four items whose gaps are not recorded as deferrals in any design doc (CP-6's `REPAIR`, EP-2's three receipt fields, EP-7's `seeds`, TP-2's unvalidated identity tier).

### Complete

| ID | Requirement |
|---|---|
| **CP-3** | Ordered oracle ladder with per-world short-circuit at the first failed hard gate |
| **CP-5** | Policy as a versioned, content-addressed artifact: ordered hard gates naming an oracle and a minimum freshness basis, lexicographic ranking, closed escalation rules, closed vocabularies validated at load |
| **CP-8** | Admission re-runs the pinned policy's gates on the exact landing tree; a rebase conflict escalates and is never auto-resolved |
| **DP-1** | Hash-chained append-only ledger; canonical JSON; integer-only metrics (no floats) |
| **DP-2** | Content-addressed store for every artifact, addressed before parsing |
| **DP-3** | Prompts and transcripts stay in the private CAS and are never published |
| **AG-1** | Six-value normalized outcome taxonomy across adapters |
| **AG-4** | The patch is captured by the control plane (`git diff`), never reported by the agent |
| **EP-3** | Freshness enforced at admission: evidence bound to a tree that is not the landing tree does not count |
| **TP-1** | DSSE signing over canonical bytes, domain-separated by payload type |
| **TP-3** | Offline local verification (`mvo verify`, seven checks; `mvo fetch-race` in any clone from a public key alone) |

### Partial — what is missing, plainly

| ID | Ships | Missing |
|---|---|---|
| **CP-1** | Intents pin a base commit + tree and a policy digest | **No drift check on the race path.** An intent whose base has drifted races normally; drift is a display line (`freshness: STALE`), and `--rebase-intent` does not exist. Only `mvo admit` confronts drift, by re-gating on the landing tree. Intents have no `parent` field, so there is no version chain. |
| **CP-2** | Per-world watchdog: SIGTERM → SIGKILL to the process group, `BUDGET_EXCEEDED` recorded | **Race-level budget exhaustion is silent.** When the intent's wall budget expires, worlds are killed and the race records an ordinary REJECT — nothing says "budget exhausted", so exhaustion is indistinguishable from a broken oracle. Intent budgets are candidates + wall only; **there is no total-USD budget** (PRD §6's `--budget 25usd` is not implemented). |
| **CP-6** | `mvo explain` renders the gate table, the key-by-key ranking walk, evidence, escalation, top-k diffs; ESCALATE is a first-class outcome with a structured payload | **`REPAIR` is absent from the decision vocabulary and cannot be recorded** — a hand-written REPAIR decision would replay as a divergence and make `mvo audit` exit 1. `mvo audit` takes no commit argument (whole-ledger replay only). "Unaffordable gates" has no representation in the ESCALATE payload. |
| **XP-1** | T0 worktrees and T1 containers, tier recorded in every world and receipt | **No `devcontainer.json` awareness** — the image comes only from `--exec-image`. **Admission landing gates always run host-T0**, whatever tier the race used, so an intent raced under T1 is admitted on evidence produced outside that isolation. |
| **XP-2** | T1 caps (CPU, memory, pids, `cap-drop ALL`, read-only root, `--network none`) applied and recorded in every receipt | **Kill-and-record on breach is proven for TIME only.** No test exercises a memory or pids breach; a kernel OOM kill is recorded as a generic `CRASH`, with no breach-specific label. **The default tier T0 is uncapped by construction** — honestly recorded, but "resource caps per world" does not hold on the default path. |
| **XP-3** | Env manifest pins OS, lockfile hashes and (under T1) the resolved image digest; receipts pin `valid_for = {tree, env}` | **The declared env-var manifest is not in the digest.** Allowlisted env var names are recorded as an observational ledger field only, so two races differing solely in which env vars crossed into the world produce identical env digests. |
| **AG-2** | Budgets recorded per world; `--max-wall-ms` enforced by mvo's watchdog; `--max-turns` / `--max-usd` passed to claude-code's own flags; cost recorded with an honest `source` | **No event-fed token meter, so no mid-run kill on dollars.** Inside a world the only control-plane stop is TIME. **Codex has no dollar cap at all** — a codex world can spend up to the wall clock. No aggregate budget across worlds. Receipts carry wall time only; production cost lives on the World. No provider-usage-API cross-check. |
| **AG-3** | Worlds record adapter, requested model pin, tree, env, patch, transcript, cost and outcome | **The agent CLI's own version is recorded nowhere** (`producer.adapter` is *our* contract version, `claude-code@v0`). `model` is the **requested** pin and is empty when `--model` is unset; the model the tool actually reports is left unparsed inside the raw transcript. The base commit is reachable only through the intent, not on the world. |
| **AG-5** | Prompt variation by candidate ordinal; `--model A,B` assigns models round-robin | **The model pool has zero test coverage** — no test asserts round-robin assignment or that sibling worlds record different models. It is CLI-only logic; the race engine has no pool concept. Richer variation strategies are M2. |
| **EP-1** | O0 `pytest --collect-only` with collected counts and a measured base denominator; O1 suite gate from JUnit XML, reportlog and coverage.py; exit code 5 is fail, never pass | **No `coverage.py` dynamic per-test contexts** — coverage is a single repo-wide number, not per-test. Deferred to M3 with the impact map. |
| **EP-2** | Receipts carry status, integer metrics, the tools that produced them, content-addressed artifacts, freshness, isolation caps and wall cost | **Three named fields are absent**: `producer` (the identity that produced the *evidence* is recorded nowhere — only worlds carry a producer), `cost.usd`/`cost.tokens`, and `inputs`. |
| **EP-7** | One shared execution envelope across all oracles; artifacts content-addressed *before* parsing | **`seeds` is absent.** Nothing pins or records an oracle-side seed (no `PYTHONHASHSEED`, no test-order seed) even though receipts claim `recheck_tier: V1-replayable`. |
| **TP-2** | Every world records `producer.identity_tier` | **It is one hardcoded literal (`"claimed"`), not a modelled concept.** No vocabulary constants, and **no validator anywhere refuses an inflated tier** — a hand-crafted world claiming `"attested"` passes ingest, publication and verification unremarked. Receipts carry no producer at all, so the tier of whatever produced the evidence is recorded nowhere. |
| **FI-1** | `mvo publish` / `fetch-race` / `prune`: signed evidence closure under `refs/multiverso/*`, offline verification from a public key alone, tested against real remotes | **Admission is a local compare-and-swap `update-ref`, not a fast-forward push** — getting the commit onto a remote trunk is your ordinary `git push`. The trailer is `Multiverso-Attestation: sha256:<bundle CAS key>`, not the specified `Multiverso-Receipt`. "Freshness by merge-base drift polling" is a **render-time comparison against the local HEAD**; nothing fetches or polls a remote. Push-exclusion refspecs are not configured — the namespace living outside `refs/heads` plus a non-fatal warning on mirror-style push refspecs is the substitute. **An attestation survives a fast-forward of the exact admitted commit and nothing else: `git rebase` fails verification at the subject check, and a squash merge produces a commit with no trailer at all — so a rebasing or squashing merge queue is structurally incompatible with M1.** The two supported topologies (fast-forward-only trunk, or an unprotected integration branch with a bot identity holding ruleset bypass) and a CI snippet are in [the quickstart](docs/quickstart.md#protected-trunks-and-merge-queues). |

### Not in M1 at all

Adaptive verification scheduling (CP-4) · O2 property/metamorphic and O3 mutation oracles · the challenger agent (AG-6) · the knowledge plane and impact maps (KP-1/KP-2) · GitHub App check runs · Sigstore keyless signing · multi-intent composition · any UI beyond the CLI · Windows.

From the PRD's own §6 sketch, these verbs do not exist: `mvo evidence`, `mvo race --dry-run`, `mvo audit <commit>`, and `mvo intent new --budget 25usd`.

### M1 acceptance criteria

The four criteria are in [PRD §10](PRD.md#10-mvp-scope--candidate-race-v0-milestone-m1). Criterion 4 (candidates under `refs/multiverso/*` on a GitHub remote, admission with a verifiable trailer) is evidenced in [`docs/probes/m1-github-demo.md`](docs/probes/m1-github-demo.md), with the stated caveat that the admitted commit reaching the GitHub trunk is the operator's `git push` and is not shown. Criterion 3 (a design partner completing `init → intent → race → explain` on their own repo from shipped docs alone) is what [`docs/quickstart.md`](docs/quickstart.md) exists to serve. Criteria 1 and 2 are not claimed here.

## Documentation

- [`docs/quickstart.md`](docs/quickstart.md) — zero to an attested commit; then your own repo, real agents, containers, publication.
- [`docs/concepts.md`](docs/concepts.md) — the six objects, the oracle ladder, and what "evidence bound to a world" means.
- [`PRD.md`](PRD.md) — data model, requirements by plane, architecture, MVP scope, evaluation plan, roadmap M0–M4. Requirement IDs (`CP-x`, `EP-x`…) are referenced throughout the code.
- [`docs/design/`](docs/design/) — one design doc per milestone ([M0](docs/design/M0.md), [M1a signing](docs/design/M1a-admit-signing.md), [M1b adapters](docs/design/M1b-agent-adapters.md), [M1c containers](docs/design/M1c-containers-parallel.md), [M1d publication](docs/design/M1d-publication.md), [M1e policy & oracles](docs/design/M1e-policy-oracles.md)), each with its resolved decisions and its explicit NOT-list.
- [`BUILDLOG.md`](BUILDLOG.md) — the public build journal, newest first, including what each review round found.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — DCO, no CLA.

## Research

- [`research/`](research/) — the corpus: 20 chapters, ~76,000 words, 305 primary sources, cutoff 2026-08-12
  - [Executive summary & findings](research/README.md) — the five-capability novelty map and the white space
  - [Verification](research/VERIFICATION.md) — adversarial fact-check of the founding memo's 13 load-bearing claims (13/13 confirmed)
  - [Bibliography](research/BIBLIOGRAPHY.md) — deduplicated source index
- [`RESEARCH.md`](RESEARCH.md) — the founding scope memo (Spanish): verdict, scope, phased plan

**Headline finding:** every one of Multiverso's five capabilities exists somewhere individually; **no system on Earth ships more than two**, and nobody at all ships an adaptive verification budget. The conjunction is empty — and closing fast (estimated window: 12–24 months). That finding is about the design space, not about this binary: the adaptive budget is exactly the piece M1 does *not* ship.

## License

[Apache-2.0](LICENSE), with [DCO sign-off and no CLA](CONTRIBUTING.md).

---

Built in public by [coagente](https://github.com/coagente).
