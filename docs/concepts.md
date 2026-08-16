# Concepts

> Six objects and one rule. Read this before [the quickstart](quickstart.md) if you want to know *why*, or after it if you want to know *what just happened*.

## "I already have CI. What is this for?"

CI answers one question: **did this commit pass?** It is a boolean about a branch, evaluated once, by a config file that anyone with write access can change, producing a green check that survives nothing — not a rebase, not a re-run, not a question asked six months later.

That is enough when a human wrote the change, because the human is the accountable party and the diff is the artifact under review. It stops being enough when the change is produced by an agent, in bulk, and the interesting question is no longer "did it pass" but:

- **Which of these six candidates should land, and why that one?** CI has no notion of alternatives. It grades one branch at a time and has no vocabulary for a comparison.
- **Did it pass, or did it delete the tests?** A suite that goes green because three test functions vanished is indistinguishable, to CI, from a suite that goes green because the bug got fixed. (This is not hypothetical; it is [research ch. 19](../research/19-mvp-oracle-toolchain.md)'s laundering vector, and `pytest` exit code 5 makes it a one-line change.)
- **What exactly was that green check *about*?** CI reports a status against a commit sha. It does not record the tree the tests actually ran on, the environment they ran in, the container caps that confined them, or the version of the tool that produced the number — so after a rebase, the check is a claim about a state that no longer exists, and nothing in the system says so.
- **Can someone else re-derive the decision without trusting your CI?** They cannot. The green check is a database row in someone's SaaS.

Multiverso is what you get when you take those four questions seriously. It does not replace CI or Git. It records the possibilities that were considered, the evidence that was gathered about each, and the decision that was made — as immutable, content-addressed objects in a hash-chained ledger, ending in an ordinary Git commit with a signed attestation trailer.

The governing rule, from [PRD §4](../PRD.md#4-product-principles):

> **Oracles outrank agents.** Agent output is a *claim*. Only oracle receipts advance lifecycle state.

Everything below is machinery for keeping that rule honest.

## The chain

```text
Intent  →  N Worlds  →  Receipts  →  Decision  →  Attestation
   ↑          ↑            ↑           ↑             ↑
 pins a    isolated     evidence    pure fn      signed, offline-
 policy    candidate    bound to    of the       verifiable, on
 + a base  universe     a world     above        the commit
```

Every object is immutable, content-addressed (SHA-256 over canonical JSON), and appended to a hash-chained SQLite ledger in `.multiverso/`. Digests are `mv0:<64 hex>` for canonical objects and `sha256:<64 hex>` for raw bytes (patches, transcripts, attestation bundles).

## Intent

A request for change, **bound to an exact base state**.

```json
{"schema":"multiverso.dev/intent/v0",
 "base":{"commit":"4bd0d29c…","tree":"git:8bdf8770…"},
 "spec":{"title":"fix mean()","description":"mean() divides by len-1; make the suite pass"},
 "budget":{"max_candidates":2,"max_wall_ms":600000},
 "policy":"mv0:c1f0b72f…","created_at":"2026-08-15T08:42:03Z"}
```

Two fields carry the weight. `base` makes the intent a statement about a specific tree, not about "main" — so every piece of evidence gathered under it has a fixed denominator. `policy` **pins a policy digest at creation time**: the rules that will judge this intent are fixed before any candidate exists, which is what stops the gate from being adjusted to fit the winner.

`mvo intent new` prints the digest; that digest is the handle for every later verb.

> Why: budget as a first-class input, and evidence freshness relative to a pinned base — [ch. 3](../research/03-adaptive-verification-scheduling.md), [ch. 5](../research/05-speculative-admission.md).

## World

One candidate universe: an isolated worktree (or container), the agent that worked in it, the patch it produced, and the resulting tree.

```json
{"schema":"multiverso.dev/world/v0","intent":"mv0:225da76e…",
 "tree":"git:8f53a125…","env":"mv0:9a523958…","isolation_tier":"T0-worktree",
 "producer":{"adapter":"claude-code@v0","model":"fake-model",
             "identity_tier":"claimed","role":"generator"},
 "context":"sha256:165565dd…","patch":"sha256:0993b202…","patch_bytes":380,
 "trace":"sha256:66041100…",
 "cost":{"wall_ms":7,"usd_micro":4200,"tokens_in":1000,"tokens_out":200,
         "source":"client-estimate"},
 "outcome":"COMPLETED"}
```

Three things worth knowing:

- **The patch is captured by the control plane, not reported by the agent.** The agent edits files; mvo takes the `git diff`. An agent cannot describe its change; it can only make one.
- **`outcome` is a closed six-value taxonomy** — `COMPLETED`, `BUDGET_EXCEEDED`, `INTERRUPTED`, `CONFIG_ERROR`, `PROVIDER_ERROR`, `CRASH` — normalized across adapters, so "the model gave up" and "the CLI was missing" are different facts. A failed world is still a recorded world; agent failure is evidence.
- **`isolation_tier` and `cost.source` are recorded, never inflated.** A T0 worktree run says `T0-worktree` and a client-reported dollar figure says `client-estimate`, because a number whose provenance is not stated is not evidence.

> Why: isolation tiers as recorded facts rather than assumptions, and untrusted generators — [ch. 1](../research/01-parallel-exploration.md), [ch. 13](../research/13-ai-control-untrusted-generators.md), [ch. 16](../research/16-agent-integration-surfaces.md).

## Receipt (evidence)

The output of **one oracle run in one world**, with the state it judged pinned inside it.

```json
{"schema":"multiverso.dev/receipt/v0","world":"mv0:1d0de113…",
 "oracle":{"id":"pytest-suite","version":"v0","config":"mv0:a6a5a160…"},
 "execution":{"argv":["python3","-m","pytest","--junit-xml=.mvo-oracle/pytest-suite/junit.xml",
                      "-p","no:cacheprovider"],
              "exit_code":0,"duration_ms":223,
              "isolation_tier":"T1-container",
              "isolation_caps":{"cap_drop":"ALL","cpu_milli":1000,"memory_bytes":536870912,
                                "network":"none","pids_limit":256,"read_only_root":true,
                                "user":"501:20"}},
 "result":{"status":"pass",
           "metrics":{"duration_ms":14,"tests_errored":0,"tests_failed":0,
                      "tests_passed":8,"tests_skipped":0,"tests_total":8},
           "tools":{"pytest":"8.4.2"},
           "artifacts":["sha256:b376dec4…","sha256:e3b0c442…","sha256:721add0a…","sha256:54bdaaca…"]},
 "freshness":{"basis":"construction","valid_for":{"tree":"git:8f53a125…","env":"mv0:86d73b72…"}},
 "recheck_tier":"V1-replayable","family":"suite","cost":{"wall_ms":317}}
```

That receipt is real, and it is missing `coverage_bp` — the fixture image has no `coverage.py`, so the metric is absent rather than invented. That is the design, not an omission.

- **`result.artifacts` are content-addressed *before* parsing.** The raw JUnit XML, the coverage JSON, the reportlog JSONL go into CAS as bytes first; the metrics are derived from them second. A dispute about a number is settled by re-reading the artifact, not by trusting the parser.
- **Absent is not zero.** If `coverage.py` was not installed, `coverage_bp` is *missing* from `metrics` — never a fabricated `0`, never "assumed 100%". `tools` names the structured sources that were actually available. A gate that needs an absent metric fails; it does not guess.
- **`execution.isolation_caps` records what actually confined the run**, so a receipt is auditable as to how much it is worth.

> Why: evidence ledgers and provenance, and the oracle taxonomy — [ch. 7](../research/07-evidence-provenance-trust.md), [ch. 8](../research/08-oracles-verification.md).

## "Evidence bound to a world"

This is the phrase the whole design hangs on, so it is worth stating exactly.

A receipt carries `freshness.valid_for = {tree, env}`. A receipt counts as evidence for a world **only if** `receipt.world` names that world **and** `valid_for.tree` equals that world's tree **and** `valid_for.env` equals that world's env digest. Not "the receipt mentions the world" — the receipt must pin the exact state it judged, and that state must be the world's.

The consequence is the point: **there is no way to reuse a green result across a change.** Edit one byte, and the tree digest moves, and every existing receipt stops applying — automatically, structurally, with no cache-invalidation heuristic to get wrong. `mvo admit` therefore does not trust the race's receipts to justify landing: it re-runs the policy's gates on the exact tree that will land, and attests *those* receipts.

Contrast with a CI green check, which is attached to a commit sha and says nothing about what it ran on. That is the difference between evidence and a rumour.

> Why: freshness bases, and why invalidation must be structural — [ch. 5](../research/05-speculative-admission.md), [ch. 11](../research/11-knowledge-plane.md).

## Policy

The rules, as a **versioned, content-addressed artifact** — not a config file the CI can rewrite mid-flight.

```text
gates (ordered):
  1. collect-nonempty@collect         oracle=collect basis>=construction threshold=0
  2. collected-not-below@collect      oracle=collect basis>=construction threshold=0
  3. status-pass@suite                oracle=suite   basis>=construction threshold=0
ranking:   gate_pass,tests_passed_desc,wall_ms_asc,world_digest_asc
escalation: on_all_worlds_failed_machinery
```

- **Hard gates are ordered and short-circuit.** A candidate that fails gate 2 never pays for gate 3, and gate 3 is reported `n/a` — not-evaluated is not a failure.
- **Ranking is lexicographic, never a weighted sum.** Key 1 decides; ties fall to key 2. No score, no tunable weights, no way to launder a loss into a win by moving a coefficient. `gate_pass` is an implicit first key (the winner provably passed every gate) and `world_digest_asc` an implicit terminal key (the order is total).
- **Escalation is a closed set of named conditions**, not an expression language. A policy is data that reaches the decision core; an evaluator there would be a new execution surface inside the trust boundary.
- **Unknown = load error.** An unknown gate name, ranking key, freshness basis or oracle kind is refused when the policy is decoded, so the decision function never sees a name it cannot evaluate.

The default policy that `mvo init` writes is deliberately weak — three gates, no coverage requirement — because a default that fails when a plugin is missing would trade honesty for brittleness. Making it stricter is [an editing exercise](quickstart.md#author-your-own-policy).

> Why: closed vocabularies against an untrusted generator, and admission oracles — [ch. 13](../research/13-ai-control-untrusted-generators.md), [ch. 14](../research/14-formal-verification-admission-oracles.md).

## The oracle ladder

Verification is ordered cheapest-first, and stops at the first failure. In M1 two rungs ship:

| Rung | Oracle | What it measures | What it catches |
|---|---|---|---|
| **O0** | `pytest-collect` | `collected_total`, `collected_base`, `collected_delta` | tests deleted or made uncollectable — including exit code 5 ("no tests collected"), which is **fail**, never pass |
| **O1** | `pytest-suite` | `tests_passed/failed/errored/skipped`, `duration_ms`, `coverage_bp` | the suite itself, from JUnit XML; coverage from `coverage.py` when present |

`collected_delta` needs a denominator, so at race start mvo measures the **base tree** through the same backend, same tier, same image, and records it as a `baseline.recorded` event. At admission the same measurement runs against trunk before the patch is applied — the right denominator for "did this patch delete tests from what is landing".

The generic `command` oracle is the escape hatch for non-Python repos: any argv, exit code 0 is pass. It gets `status-pass` and nothing else, because a shell command that exits 0 tells you exactly one bit.

O2 (property/metamorphic) and O3 (mutation) are M2. The ladder's shape — spend the next unit of compute where it buys the most decision quality — is the research question this project exists to answer, and it is **not** answered yet; M1's ladder is fixed, not adaptive.

### What the gate ladder catches, and what it does not

**M1f moved the trust boundary.** Until it landed, pytest-suite metrics were
parsed from an artifact written inside the candidate's own writable tree, and
the [2026-08 design partner study](studies/2026-08-design-partner-study.md)
reproduced five laundering vectors that walked straight through the default
policy. The one-line summary that replaces the table that used to sit here:

> **The candidate no longer writes its own evidence, and a candidate that
> edits the harness no longer races. What remains open is a candidate that
> lies from inside the code a human is about to review — which is where
> review, mutation testing, and the AI-control literature take over.**

Three mechanisms do the work, in the order a race meets them.

**Rung O-1, the `tree-guard`.** Before any Python runs, the control plane
compares two git trees it holds: the intent's base tree and the candidate's.
The policy declares two frozen path classes — `protected` (test files: frozen
against modification and deletion, additions allowed, because adding a
regression test is the behaviour we want) and `harness` (`conftest.py`,
`pytest.ini`, `pyproject.toml`, `sitecustomize.py`, `*.pth`, …: frozen against
modification, deletion **and creation**, because a repository that has no
conftest today must not acquire one from an untrusted generator). It executes
nothing, it costs two `git ls-tree` walks, and it is the one gate whose
evidence an adversary provably cannot author: the candidate chose the content,
but it cannot change what the content *is*.

**The evidence stream.** Metrics no longer come from a file read after the
process exits. A control-plane-owned pytest plugin — embedded in the binary,
materialized read-only outside the candidate's tree, loaded by argv before any
candidate code — writes a framed event stream to a channel the control plane
created and reads **live**, while the process runs. The reader holds one handle
at a monotonically increasing offset and never rewinds, so the prefix it has
already consumed cannot be retracted; a candidate that truncates and rewrites
at `atexit` desynchronizes the record sequence, which makes the stream unusable
— a failed gate, never a forged pass. Three structural rules follow, and none
of them is policy-controlled:

- **S1 — absence never passes.** No usable stream ⇒ every stream-derived
  metric is absent, and a process that exited 0 with no usable stream is
  `status = error`, never `pass`. So the cheapest attack on a streaming oracle
  — silence the plugin — buys a failed gate, not a green one.
- **S2 — the exit code is cross-examined, not trusted.** Exit 0 against a
  stream reporting failures is `error`, with both numbers in the reason.
- **S3 — the second source must agree.** JUnit XML is a *cross-check*, not a
  source. When it disagrees with the stream the verdict is `error`:
  brittleness escalates, it never convicts and never acquits.

**Cross-oracle invariants.** `collect-equals-suite-total` compares O0's
collected count with O1's test total. A three-test repository reporting a
500-test suite is a contradiction the race states out loud:
`escalation.on_invariant_violation` is rule 0, above every other rule, so a
detected forgery reaches a human instead of being filed under "the tests
didn't pass".

The measured result, on the adversarial corpus
([`testdata/adversarial/`](../testdata/adversarial/README.md)) as of M2a —
**19 vectors, and the honest number is not 19 of 19**:

> Eighteen of nineteen are stopped or escalated. **One lands.** Vector
> `19-differential_conformity` passes every gate in a solo race, is SELECTed,
> admitted, and `mvo verify` accepts it — with the bug fully intact. It reads
> the input corpus off the filesystem at oracle time and special-cases exactly
> those inputs, leaving every other input wrong. Raced against an honest fix it
> escalates (the two land in different behaviour classes), but alone it wins.
> This is the classic overfit-to-the-oracle, and it is **open at T0 by
> construction**: the world must execute the corpus, so the world can read it.
> T1 closes the sibling half but not this one.

Thirteen of the stopped vectors die at rung O-1 with exactly one receipt each;
before M1f, eight of them landed on trunk with a signed attestation `mvo verify`
accepted. The corpus evidences the **STOPPED** rows below and only those: the
guard-caught vectors never reach the evidence stream, so the DETECTED row is
carried by `internal/oracle/evidence_test.go` and by acceptance step 3k, which
races a forgery under a policy with the guard gate, the guard oracle and the
invariant all removed. Three vectors (14–16) require `hypothesis` and `mutmut`
to be importable; without them the race aborts as *machinery* and those rows go
unevidenced rather than silently passing.

What is still open, stated as plainly as the rest:

| Vector | Status | Why |
|---|---|---|
| A new `conftest.py` (forged report, forged exit code) | **STOPPED** | `harness_added` at rung O-1, before any of its Python runs |
| Editing, skipping or deleting a test | **STOPPED** | `protected_modified` / `protected_deleted` |
| An `*.egg-info` / `*.dist-info` declaring a `pytest11` **entry-point plugin** | **STOPPED** | A red team landed this on trunk under the first cut of M1f: pytest imports entry-point plugins from any distribution metadata on `sys.path`, the candidate's tree root is on `sys.path`, and the module can be named anything — so no path glob was ever going to close it. Oracle runs now carry `PYTEST_DISABLE_PLUGIN_AUTOLOAD=1` (`evidence.plugin_autoload`), which removes the capability rather than naming the file; the metadata directory is in the harness set as well. Corpus vector `13-entrypoint_plugin` |
| The same forgeries moved into **application** code | **DETECTED** | S1/S2/S3: an absent stream, a lying exit code, or a disagreeing file is `status = error`, and every gate fails. Evidenced by unit tests and acceptance step 3k, not by the corpus |
| A 3-test repo reporting a 500-test suite, forged **coherently** across both oracles | **OPEN** | `collect-equals-suite-total` compares two numbers that are BOTH authored inside the test process. It catches the adversary who forged one and forgot the other — which is most hand-written cheats — and not the one who forged both to agree. A red team reproduced the coherent version end to end. Closing it needs a count derived outside the test process (control-plane static AST collection); that is not in M1 |
| Weakened or rewritten assertions, with the guard relaxed | **DETECTED as ambiguity** | Not detectable from test outcomes at all, and *not* by mutation either: diff-scoped mutation over a patch whose entire content is a test-file edit has an empty target set. What catches it is the M2a cross-candidate differential — the cheat and the honest fix land in different behaviour classes on a control-plane-authored corpus, and `on_behavioral_split` escalates with the input and both answers. That is ambiguity, not conviction: the differential does not say which class is right |
| A mutant of the candidate's own diff that **hangs** instead of failing | **DETECTED** | A red team ran it: a per-mutant timeout was its own bucket, excluded from `mutation_score_bp`'s denominator *and* from the only mutation gate, so a mutant nothing killed cost the candidate nothing while the receipt read 100 %. `mutation-survivors-not-above` now reads `mutants_survived + mutants_timeout`, and the report lists the timed-out mutants with their diffs |
| A `cosmic_ray/` directory in the tree root that **shadows the mutation tool** | **DETECTED** | A red team ran it: `python -m cosmic_ray.cli` resolves through the candidate's own tree, and a tool that enumerates nothing passed the survivor gate vacuously. The enumeration now runs under `PYTHONSAFEPATH=1`, and zero mutants over a non-empty diff target set is a machinery error with a named reason, not a pass. On CPython below 3.11 the environment switch is ignored and only the second layer applies |
| One world **rewriting the corpus another world will replay**, at T0 | **OPEN at T0, STOPPED at T1** | A red team ran both payoffs — eliminating an honest sibling by making it report an undeclared case, and suppressing a behavioural split by making two worlds agree. The corpus is now delivered per world, re-digested by the control plane either side of every replay, and each observation is bound to the bytes it loaded, so the attack is detected and attributed. It is not *prevented* at T0: the oracle and the candidate run as the same uid on the same filesystem. Under T1 the per-world corpus is a read-only bind into one container and no sibling can reach it |
| A cohort member that **shrinks the comparison** without emptying it | **OPEN** | The differential's denominator is the set of cases every member observed comparably, so a member that answers "opaque" on some cases removes exactly those from everyone's comparison. Members that contribute *nothing* are excluded and every member's contribution is recorded in the report; a partial shrink is visible and not refused |
| A candidate that **reads the corpus off the filesystem** at oracle time, at T0 | **OPEN at T0, STOPPED at T1** | The corpus lives outside the worlds tree at mode 0700 and is absent from every world-visible path while the generating agent runs, so the agent is never *delivered* the inputs its output will be compared on. At T0 a process running as the invoking uid can still find them by absolute path. The honest guarantee at T0 is delivery, not unreachability |
| Skips, with neither `paths-unmodified` nor `skips-not-above` declared | **OPEN** | A skipped test is still collected and still counted, so both sides of the invariant agree |
| An adversary who patches the plugin from an imported module before the first failing test reports | **OPEN** | Stated in M1f's threat model in as many words. Nothing in-process defeats in-process code, and no cryptography here pretends otherwise |
| A backdoor in application code that passes every honest test | **OPEN** | Research ch. 13's actual problem. Nothing in M1 addresses it |
| A candidate that **burns the oracle budget** so a rival's last hard gate is never purchased | **OPEN under a binding budget** | The scheduler's pool is shared and a purchase nobody has measured is affordable while any of it remains, so a rung that sleeps long enough spends past the bound (measured: 4 963 ms against a 2 000 ms budget) and its rival's suite is refused. A per-world cap was implemented, measured and reverted — it manufactured a false ESCALATE by refusing a rung the remaining budget could afford — so what ships instead is a real price for the pytest kinds (which is what makes the bound bind on any workspace with history), priced purchases ordered ahead of unpriced ones, and `BUDGET EXCEEDED by N ms` printed by `mvo explain --schedule`. See [M2b §2.7](design/M2b-adaptive-scheduler.md#residual-starved-admission) |
| A race that **admits while a rival's gate went unbought** — the starved admission | **OPEN by poverty, CLOSED by choice** | Withholding a hard-gate receipt drops a world out of the pass set, and `SELECT` is *not* monotone in the pass set: the tie or shortfall that would have escalated never materialises, and the race admits where the exhaustive ladder asked a human. A red team measured 8 of 8 admissions against 3 of 3 escalations at the same budget. The scheduler can no longer *choose* it — a hard gate on a live world is bought whatever the value model says — but a budget that genuinely runs out still leaves it. A policy declaring `on_evidence_incomplete` escalates instead (measured 4 of 4); the shipped default does not declare it until M2d measures its base rate. Every SELECT rationale now names the rivals it never measured, in every policy |

Because the guarantee depends on *how a run was observed*, every receipt
records it: `execution.evidence_regime` is one of `control-plane` (the guard —
no candidate code runs at all), `streamed` (any tier), or `in-tree` (M1e
behaviour, explicitly declared only). **Every run of a pytest oracle today is
`streamed`, on T0 and T1 alike** — and `mvo explain` prints it
unconditionally, because a guarantee nobody can see the absence of is a
guarantee nobody has.

`evidence.plugin_autoload` rides beside the regime in the same pinned policy:
`off` (the default) seals pytest's entry-point plugin discovery, `on` restores
it for a repository whose suite genuinely needs an installed plugin — a
deliberate, attested choice with a cost, printed by `mvo policy show`.

A fourth regime, `isolated` — a distinct oracle uid, a read-only worktree, and
a channel directory the oracle cannot write — is defined in the policy
vocabulary and **is not yet deliverable**: the T1 mounts are built, but oracle
execs do not yet carry `--user` or run from the read-only mount, so no run can
honestly claim it. A policy that declares `evidence.regime: "isolated"` is
therefore refused at pre-flight, as machinery, with the ledger untouched —
rather than downgraded quietly to `streamed` while the receipt says otherwise.
A receipt that overstates how it was observed is the exact failure this whole
mechanism exists to remove, and the label is not exempt from it.

> Why: the MVP toolchain and its verified tool versions, plus adaptive scheduling as the open problem — [ch. 19](../research/19-mvp-oracle-toolchain.md), [ch. 3](../research/03-adaptive-verification-scheduling.md), [ch. 8](../research/08-oracles-verification.md).

## Decision

A **pure function** of (policy, worlds, receipts) → one of `SELECT`, `REJECT`, `ESCALATE`, `ADMIT`.

```json
{"schema":"multiverso.dev/decision/v0","type":"SELECT","intent":"mv0:223b3112…",
 "subject":["mv0:18ba9349…","mv0:ad21cf57…"],
 "evidence":["mv0:0af6fb30…","mv0:33506af0…","mv0:9b4f5a5b…","mv0:c6375e2d…"],
 "policy":"mv0:c1f0b72f…",
 "rationale":"1/2 worlds passed hard gates [collect-nonempty@collect,collected-not-below@collect,status-pass@suite]; selected mv0:18ba9349… (sole world passing all hard gates); ranking [gate_pass,tests_passed_desc,wall_ms_asc,world_digest_asc]"}
```

- `subject` is the ranked candidate list, winner first. `evidence` lists **exactly what the decision saw** — not everything in the ledger, the specific receipt digests the gates and ranking keys read, including the loser's.
- `rationale` is a frozen sentence rendered by a dialect pinned to the policy's schema version. Re-wording a decision made under a policy pinned last year would rewrite history, and `mvo audit` compares the sentence byte-for-byte.
- **`ESCALATE` is a first-class outcome, not an error.** When two candidates tie on every ranking key, a coin flip is not a decision — the race exits 0 and records the ambiguity, naming both worlds. `mvo explain` prints the ranking walk with the deciding row marked `tie-break only — not a decision`, because titling it "why X won" would launder exactly the ambiguity the rule exists to report.

Purity is what makes `mvo audit` possible: given the ledger, anyone re-runs the same function over the same recorded inputs and must get the same bytes out.

> Why: candidate selection without a learned judge in the loop — [ch. 2](../research/02-candidate-generation-selection.md), [ch. 13](../research/13-ai-control-untrusted-generators.md).

## Attestation

The output side of "Git-compatible in, Git-compatible out": an ordinary commit, plus a DSSE-signed [in-toto](https://in-toto.io) Statement whose predicate names the whole closure.

```text
fix mean()

Multiverso-Intent: mv0:223b311241502607f2114f0c8c6859a84ff5c328292fa849aebff67db1e9374c
Multiverso-Decision: mv0:9b46d60850c3c2c8e2484503dadb5c1336c25f8870975cdf7894cb1d534dc3f5
Multiverso-Attestation: sha256:cf3eabf82092483dda59807a0858d25f2be24d5582e6dab1a679b5aa5f474cb9
```

The predicate (`multiverso.dev/admission/v0`) carries the intent, the winning world, the ADMIT decision, the SELECT decision that preceded it, the evidence closure, the policy digest, the budget consumed, the signing key ID, and the trunk branch + parent commit. The subject is the **landing tree**, not the commit — the trailer lives inside the commit message, so a commit sha cannot appear in its own attestation.

`mvo verify <commit>` runs seven checks offline against the repo and `.multiverso/` alone: content-address integrity of the bundle, signature, statement shape, subject (tree + parent), reference closure, freshness (every attested gate receipt judged the admitted tree), and budget arithmetic. Change one byte of the admitted tree and it fails at `subject` — verified in [the quickstart](quickstart.md#10-thirty-seconds-of-paranoia).

> Why: the predicate is ours to mint because no standard exists for agent-produced changes — [ch. 7](../research/07-evidence-provenance-trust.md), [ch. 18](../research/18-forge-integration.md).

## What this is not

- **Not an adaptive scheduler.** M1's ladder and candidate count are fixed. Dynamically allocating budget between generation and verification is the actual research question, and it is M2. Until then, "evidence-aware scheduling" is a claim about the roadmap, not about the binary.
- **Not a merge queue, a forge, or a CI replacement.** Admission updates a local branch ref; getting it to a remote is your ordinary `git push`.
- **Not production-hardened.** See the [Status section](../README.md#status) for the itemized list of what is complete, what is partial, and what is absent.

## Where to go next

- [`quickstart.md`](quickstart.md) — run all of the above on a toy repo in about five minutes.
- [`../PRD.md`](../PRD.md) — the full data model (§5), requirements by plane (§7), and MVP scope (§10). Requirement IDs (`CP-x`, `EP-x`…) are referenced throughout the code.
- [`design/`](design/) — one design doc per milestone, each with its resolved decisions and its explicit NOT-list.
- [`../research/`](../research/) — 20 chapters, 305 primary sources, the reasoning behind every choice above.
