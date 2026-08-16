# M2a — The Oracle Menu: Design & Contracts

> M2's thesis is an **adaptive scheduler** (CP-4, [research ch. 3](../../research/03-adaptive-verification-scheduling.md)). A scheduler is a function from a budget to a purchase; today the ladder has three rungs — `tree-guard`, `pytest-collect`, `pytest-suite` — the whole of which costs about four hundred milliseconds and a pair of `git ls-tree` walks on the fixture repositories in this tree. **There is nothing to schedule.** M2a builds the menu: four new oracle kinds spanning three orders of magnitude of cost, each declaring what it costs, what it scales by, what signal it reads, and who authored its inputs — so that M2b's scheduler has something to choose between and something to choose *with*.
>
> The centrepiece is the **cross-candidate differential** (EP-4). [Research ch. 8 §8.4](../../research/08-oracles-verification.md) establishes that candidate-vs-candidate behavioural diffing is proven at *function* scale (CodeT, SemanticVote, SEP) and that **no surveyed system performs it at repository scale on real patches**. Multiverso already pays for N isolated worlds of one intent; the marginal cost of comparing them is one interpreter start per world plus a hash table. This document resolves the interface question that has blocked it — `Oracle` is `Run(ctx, world) → Receipt`, one world in, one receipt out, and a differential is a function of many worlds — and then resolves the harder question underneath it: **what a difference MEANS**. The answer, derived rather than asserted in [decision 7](#decision-7), is that a difference is evidence of *ambiguity*, not of correctness; that the only honest v0 consumer of it is a human; and that every candidate ranking key we could derive from it is wrong-signed. We ship the escalation and refuse the keys, and [Ranking keys we are NOT shipping, and why](#ranking-keys-we-are-not-shipping-and-why) is the most load-bearing section of this document.
>
> Also here: **O2 property-based** (Hypothesis, with its observability mode when the format parses and honest absence when it does not), **O3 diff-scoped mutation** (Google's production recipe: mutate only the diff, cap the budget, and never run a full mutation pass), and the **cost/correlation metadata** M2b consumes — receipts gain a scaling denominator, a correlation descriptor, and a provenance map for their run-time inputs.
>
> Implements **EP-4** and **EP-5** to their v1 text, extends **EP-2** (receipts carry cost and correlation the scheduler can *use*, not merely read), and establishes the contract CP-4 will allocate over. Grounded in [ch. 8](../../research/08-oracles-verification.md) (the ladder, the correlation warning, the differential white space), [ch. 3](../../research/03-adaptive-verification-scheduling.md) (§3.5: N generated tests are not N observations; nobody discounts marginal evidence value by correlation in the allocation loop), and [ch. 19](../../research/19-mvp-oracle-toolchain.md) (verified tool versions, the incremental-cache freshness hazard). Builds on [M0](M0.md), [M1a](M1a-admit-signing.md), [M1b](M1b-agent-adapters.md), [M1c](M1c-containers-parallel.md), [M1d](M1d-publication.md), [M1e](M1e-policy-oracles.md), [M1f](M1f-trust-boundary.md); **every prior contract stays in force unless amended here**, and [M1f's rule is absolute](#trust-boundary): an oracle's metrics must not derive from bytes the candidate can author, every new oracle states its evidence regime, and every new surface is attacked before it ships.
>
> **Stdlib only; `go.mod`/`go.sum` untouched.** Python tooling is invoked by oracles at runtime, inside worlds; no test may require a package to be installed, and **on the machine this document was written on `hypothesis`, `mutmut` and `cosmic-ray` are all absent** (`pytest 8.4.0` and `coverage 7.8.2` are present) — so every degradation path in here is the path this tree actually takes today, not a hypothetical. Tests never invoke real agent CLIs (M1b rule). POSIX only.
>
> **Compatibility, stated up front and proved by the acceptance script: the shipped default policy does not move** (it stays `mv0:f207c3fad59d0fc973e5f342ac54d8b1bc9e5c6cae2a2cff0b33477ddee3c543`), every new rung is opt-in, and **every M0–M1f ledger replays byte-for-byte** under the M2a binary. Receipt digests *do* move — three fields are added — and M1e decision 1 is why that is free: nothing about a recorded decision is re-derived from a re-serialization of its inputs.

## Module layout (delta over M1f)

```
internal/oracle/value.go           mvo-value/v0: the total, canonical encoding of an
                                   observed Python value, and the case fingerprint
internal/oracle/corpus.go          the corpus object, case-id rules, provider dispatch
internal/oracle/pyplugin/mvo_corpus.py   the corpus runner: --materialize and --replay
                                   (embedded, content-addressed, read-only mount; NO pytest)
internal/oracle/observe.go         corpus-observe: the per-world observation oracle (O2a)
internal/oracle/differential.go    the reducer: partition, report artifact, one comparison
                                   receipt per world (O2b) — PURE, no process, no world
internal/oracle/properties.go      hypothesis-properties (O2p) + the observability parser
internal/oracle/difftarget.go      captured patch → mutation target line set (pure)
internal/oracle/mutation.go        mutation-diff (O3): the MutationTool adapter surface,
                                   canonical mutant ordering, the cap, partial-result rules
internal/oracle/mutation_cosmic.go cosmic-ray adapter — enumerate → select → exec (default)
internal/oracle/mutation_mutmut.go mutmut adapter — tool-selected mutants, labelled as such
internal/policy/profile.go         OracleProfile: the DECLARED cost + correlation menu
internal/race/corpus.go            phase 0: materialization + base observation on the base
                                   tree; corpus.recorded
internal/race/cohort.go            phase B2: cohort assembly, the reducer's run
cmd/mvo/oracles.go                 mvo oracles — the menu, with measured coefficients
testdata/toyrepo/patches-behave/   patch-p, patch-q: identical gates, identical ranking
                                   values, DIVERGENT behaviour on one uncovered input
testdata/toyrepo/corpora/          clamp-nan.json — the declared-corpus fixture
testdata/toyrepo/props/            mvo_props.py — the policy-declared property module
testdata/toyrepo/policies/         differential.json, properties.json, mutation.json, menu.json
testdata/corpus/                   recorded corpus objects + observation streams (pure tests)
testdata/mutation/                 recorded cosmic-ray / mutmut outputs (pure parser tests)
testdata/adversarial/vectors/      14-19: six new laundering vectors, one per new surface
```

Amended (never rewritten): `internal/object` gains `Cost.Units`/`Cost.Unit`, `Receipt.Inputs`, `Receipt.Correlation`, the `derived` regime constant, and four additive `PolicyV1`/`GateSpec`/`OracleSpec`/`EscalationSpec` fields; `internal/policy` gains five gate predicates, four oracle kinds, three families, twenty-two metrics, the corpus/mutation/scope vocabularies and their validation rules, and the profile table; `internal/oracle` gains four kinds in the registry and the value encoder; `internal/race` gains phase 0 and phase B2; `internal/admit` gains gate scope; `internal/audit` gains three referenced-digest rows; `cmd/mvo/{oracles,explain,race,policy}.go`; `scripts/accept.sh` gains eight steps; `scripts/adversarial.sh` gains six vectors and a re-recorded baseline.

## The menu

The whole point of this block, on one page. Costs are **measured on this tree** where a `measured` note appears and are **declared engineering estimates** otherwise; the [Cost model](#cost-model--what-m2b-consumes) section says how M2b replaces both with per-repo fits.

| rung | kind | family | stage | signal | fixed cost | marginal cost | discriminates | evidence regime |
|---|---|---|---|---|---|---|---|---|
| O-1 | `tree-guard` | `tree` | world | tree bytes | ~10 ms (2 `git ls-tree`) | ~40 µs/path | verdict | `control-plane` |
| O0 | `pytest-collect` | `collect` | world | test identity | **~400 ms measured** | ~0.5–5 ms/test file | verdict | `streamed` |
| O1 | `pytest-suite` | `suite` | world | test outcomes | **~400 ms measured** | Σ test durations | verdict | `streamed` |
| O2p | `hypothesis-properties` | `property` | world | value behaviour | ~600–900 ms | cases × per-case | verdict | `streamed` |
| O2a | `corpus-observe` | `behavior` | world | value behaviour | **~25 ms measured** | cases × per-case | — (raw observation) | `streamed` |
| O2b | `corpus-differential` | `behavior` | **cohort** | value behaviour | ~1 ms | O(cohort × cases) hash | **partition** | `derived` |
| O3 | `mutation-diff` | `mutation` | world | suite adequacy | one baseline suite run | mutants × covering-test time | ordinal (recorded, not ranked) | `streamed` |

Two facts in that table decide most of M2b's shape.

**First: the fixed cost of anything that imports pytest is ~400 ms, and on this tree it dwarfs the tests.** Measured, three runs each: `python3 -m pytest --collect-only -q` over the 8-test `testdata/toyrepo` is `0.39/0.40/0.88 s` real while collection itself reports `0.00s`; the full suite over `testdata/adversarial/repo` is `0.43/0.43/0.44 s` real while the tests report `0.04s`. A bare interpreter that imports the module under test and calls it 500 times costs `0.02 s` total, of which the 500 calls are `0.12 ms`. **A scheduler that models oracle cost as "test time" will be wrong by an order of magnitude on small repositories and right on large ones**, which is exactly why the cost model below is `fixed + per-unit × units` with both terms fitted per repository rather than a single number.

**Second: `corpus-observe` is the cheapest code-executing rung in the system and also the one with the smallest attack surface** ([decision 12](#decision-12)) — it does not import pytest, so no `conftest.py`, no `pytest.ini` `addopts`, no entry-point plugin and no `-p` argument is involved at all. Cheap *and* comparatively trustworthy is an unusual combination and the scheduler should exploit it.

## The purchase law

The one invariant that lets M2b be adaptive without touching `Decide`, stated before anything else because every other decision assumes it.

> **A world that has not paid for every hard gate's oracle cannot pass.**

It is already true. A gate whose oracle produced no counted receipt fails (M1e: "a required metric that is absent fails the gate … never a fabricated zero"; M1f made absence-never-passes structural as rule S1). M2a's contribution is to *name* it and to forbid the version of adaptivity that would break it:

- The scheduler may decline **any** purchase, at the price of that world's candidacy. That is precisely successive halving's semantics — you stop paying for candidates you have already eliminated — and it needs no new machinery.
- The scheduler may **not** mark a rung "skipped, assume fine". There is no such state and there will not be one. A skipped gate that renders like a passed gate is the over-claim this project exists to remove, and `Decide` has no way to distinguish "no receipt because we declined to buy" from "no receipt because the oracle crashed" — nor should it, because the effect on the candidate is identical.
- A declined purchase is recorded as an **observational** ledger event `oracle.skipped {world, oracle, reason}` (M1b's observational-event precedent: no replay semantics, covered by the hash chain, ignored by `mvo audit`'s replay). It exists so an operator can see where the budget went, not so a gate can read it.
- **Corollary, and it is the scheduler's whole job**: the eventual winner must have paid for everything the policy demands. Adaptivity buys *nothing* on the leader and *everything* on the losers. Any scheme that saves money on the leader is saving it by weakening the admission evidence.

`escalation.on_all_worlds_failed_machinery` already routes a starved race to a human rather than recording "these candidates are bad", so a scheduler that runs out of budget before anyone finished the ladder produces an ESCALATE, which is the honest verdict.

## Resolved design decisions

<a id="decision-1"></a>
1. **The cross-candidate differential is a two-phase oracle: a per-world observer that fits the existing `Oracle` interface unchanged, and a control-plane reducer that emits ONE COMPARISON RECEIPT PER WORLD.** The three options were weighed and two are refused:

   - *A second interface for N-world oracles* (`RunAll(ctx, []World) → Receipt`) fails on the receipt, not on the interface. One receipt over N worlds cannot answer "which world does this judge?", so `Receipt.World` and `Freshness.ValidFor` would have to become plural — forking `receipt/v0`, and with it M1a's attestation subject binding, `mvo verify` check 6, M1d's publication layout and M1e's counted-receipt rule. And `Decide`'s gates are per-world predicates over a per-world counted receipt; a race-level receipt would need an attribution rule *inside* `Decide`, which is where impurity goes to hide.
   - *Receipts that reference sibling world digests* is the same failure wearing a smaller change. A receipt whose `valid_for.tree` names one tree while its metrics derive from N trees is lying about what it judged. That is M1f's "the label doing the laundering", and it is refused for the same reason `evidence_regime: "isolated"` was refused on a tier that could not deliver it.
   - *Two phases* keeps every contract. `corpus-observe` is an ordinary `Run(ctx, world) → Receipt` oracle bound to exactly the world it observed. The reducer is a **pure function** of `(corpus, base observation, the cohort's observations, the cohort's world digests)` and emits N receipts, each bound to exactly one world, each carrying that world's *position in the comparison* as its metrics. `Decide` is untouched. Replay is untouched. Ladder short-circuiting is untouched. The orchestrator gains one barrier ([phase B2](#orchestrator-changes)), which is a scheduling change, not an evidence change.

<a id="decision-2"></a>
2. **The cohort's identity travels in the receipt and the report, never in the selector's config digest.** A comparison receipt's numbers are only meaningful relative to a named cohort, so the cohort must be recorded — but `oracle.config` is the *policy-resolvable* configuration, and a policy cannot know at authoring time which worlds a future race will produce. Putting the cohort in the config digest would make the gate's selector unmatchable forever. So: `inputs["cohort"]` carries the digest of the sorted cohort world-digest list, `inputs["corpus"]` the corpus digest, `result.detail` carries `behavior class mv0:… (cohort mv0:…)` — non-numeric, one line, quotable by a pure gate predicate — and the full cohort membership, partition and distinguishing cases live in the control-plane-authored `differential-report` artifact. Across races the question does not arise: worlds are per-race objects, so a receipt bound to a world is bound to its race.

<a id="decision-3"></a>
3. **A fourth evidence regime, `derived`, and the rule that a derived receipt must name its evidence floor.** The reducer executes no candidate code and touches no candidate tree, so `control-plane` is the tempting label — and it would be an over-claim, because every byte the reducer consumes was produced by a `streamed` run inside a candidate's process. M1f decision 14 exists precisely to stop a receipt from inviting the strongest reading. Therefore `execution.evidence_regime = "derived"`, with `argv == []`, `exit_code == 0`, `evidence_plugin == ""`, and **`inputs["evidence_floor"]` naming the weakest regime among the inputs** (`streamed`, in every configuration that ships). A reader of a differential receipt learns, from the receipt alone, that the comparison is control-plane arithmetic over candidate-influenced observations.

<a id="decision-4"></a>
4. **The input corpus is a control-plane-materialized, content-addressed object produced on the BASE tree and replayed as data in every world.** This is M1e decision 13's move — "the base-state measurement is an input, not evidence about a candidate" — applied to inputs instead of counts, and it is what makes cross-candidate comparison *possible at all*:
   - **Identity.** Comparison requires that every world executed the same inputs. A generator run inside each world does not give that: Hypothesis stops early on failure, so a world that fails on case 3 never sees cases 4…100, and comparing a 3-case observation with a 100-case one is comparing nothing.
   - **Trust.** A corpus a candidate can influence is a corpus a candidate can choose to agree on. Materializing on the base tree means the inputs are fixed before any candidate exists, from code the candidate did not write.
   - **Determinism.** The corpus is bytes in CAS with a digest in the ledger, so `mvo audit` replays a differential decision exactly like any other.
   - **Cost.** One extra worktree and one or two short interpreter runs per race, amortized over N worlds — the same fixture the M1e collect baseline already opens, at the same tier, image and env.

   A corpus of zero cases aborts the race as machinery before any world is created, with the M1e-shaped one-liner and an untouched ledger: a differential over an empty corpus produces numbers that are all fictions.

<a id="decision-5"></a>
5. **Two corpus providers ship in v0 — see [amendment 28](#decision-28) — and neither needs an agent.** `repo-suite` (case = a test node-id, observation = the stream's per-test outcome; **free**, because the collect baseline already produced the node-id list and the suite run already produced the outcomes). `declared` (the policy names a JSON corpus file; zero dependencies, fully deterministic, and the honest answer for a repository that has neither Hypothesis nor an appetite for one). `hypothesis` (materialized on the base tree from the repo's own `@given` tests plus the policy-declared property module, under a pinned seed with `derandomize=True` and `database=None`). **`repo-suite` is documented as the honest floor and as nearly information-free**: among candidates that passed the suite gate, every world's per-test outcome vector is identical, so the partition has one class and the differential says "no divergence" — which is true and useless. Its residual value is on worlds that failed and on skip/xfail structure. Ship it because it costs nothing; do not put it in a fixture policy and pretend it discriminates.

<a id="decision-6"></a>
6. **A behavioural difference is a difference in a total case FINGERPRINT, and un-encodable values never compare equal.** [`mvo-value/v0`](#the-value-encoding-mvo-valuev0) is a total, canonical encoding of an observed Python value; the fingerprint is `sha256` over the canonical JSON of `{"kind": …}`. Three rules carry the honesty:
   - **Floats are bit patterns, not numbers.** `struct.pack('>d', x)` in hex. DP-1 forbids floats in canonical JSON and a decimal rendering is not portable, but exactness is the point: `sum(xs)/n` and `math.fsum(xs)/n` differing in the last bit *is* a behavioural difference, and `-0.0`, `NaN` and the two infinities are distinct observations, not rounding noise.
   - **Exception *messages* are excluded from the fingerprint; only the exception type is hashed.** Messages embed paths, addresses and object ids, so hashing them would make every world differ for no behavioural reason — a differential that reports 100% divergence reports nothing.
   - **A value the encoding cannot represent is `opaque`, and two `opaque` observations are NOT equal.** Absence of comparability is not agreement. Opaque cases are excluded from the comparison denominator and counted, exactly as an absent metric is recorded rather than zeroed.

<a id="decision-7"></a>
7. **What a difference MEANS — and why the honest answer is "a human".** This is the question the block turns on, so the derivation is written out rather than summarized.

   Two candidates that differ on input *x* have told us: **at most one of them is right, and the evidence we hold does not say which.** That is a fact about the *evidence*, not about the candidates. Three tempting readings are each wrong:

   - *"The majority is right."* This is a popularity contest among samples drawn from one model prior. Ch. 8 §8.4 names the failure directly — "agreement-based selection suffers common-mode failure when all candidates share the same model prior and thus the same bug — consensus evidence is cheap but *correlated*" — and ch. 3 §3.5 supplies the trend: CAPA shows model errors are becoming *more* correlated as capability rises. The minority-correct case is not exotic; it is the case where this product would earn its keep.
   - *"The one that agrees with the base tree is safest."* **This is the trap, and it is why no differential ranking key ships.** The intent is to change behaviour. A candidate that changed nothing agrees with base everywhere and would win such a key; the honest fix, which changed behaviour on exactly the inputs the intent was about, would lose. We verified the shape of this on the adversarial corpus: vectors 06–08 leave `split_evenly` broken, so they agree with base on the corpus while `01-honest_fix` does not. A ranking key on "agrees with base" would rank the cheats above the fix. That is the study's stopwatch failure in a new costume, and we refuse it.
   - *"Divergence is a gate."* A candidate can be the only correct one and diverge from five wrong siblings. A hard gate on divergence rejects the correct patch.

   What is left is the true statement: **a partition of the cohort into ≥2 behaviour classes on a shared, control-plane-authored corpus proves that the intent as specified, plus the repository's own tests, do not determine behaviour on the inputs that separated them.** The honest consumer of that is a human, and the honest artifact is *the input and what each candidate returned on it*. So `escalation.on_behavioral_split` is the v0 decision-layer output, and `mvo explain` renders the distinguishing cases with values.

   **This is novel and it is worth shipping.** No system in ch. 8's survey does candidate-vs-candidate behavioural diffing at repository scale, and none of them hands a maintainer the sentence "your six candidates disagree on `clamp(nan, 0, 10)`: five return `nan`, one returns `0`". The correct machine output for genuine ambiguity is a well-evidenced refusal, and M1f already built the vocabulary for refusing without laundering ("a correct refusal beats a confident wrong answer"; an ESCALATE never renders like a win).

   What would make it directional is named rather than hand-waved: **a fail-to-pass reproduction test** (EP-4's other half, ch. 8 implication 1). With one, the corpus partitions into *intent* cases and *regression* cases, and "changed behaviour on intent cases AND agrees with base on regression cases" is correctly signed. That is M2c's prerequisite, not M2a's.

<a id="decision-8"></a>
8. **Zero new ranking keys, and the refusal is a product statement.** Every candidate-comparing key derivable from the new oracles is signed by something the candidate chooses: agreement is signed by the correlated population ([decision 7](#decision-7)); `mutation_score_bp` is a ratio whose denominator is the candidate's own diff, so it is inflated by padding the diff with trivially-killed lines (corpus vector 14); `mutants_survived` as a *key* punishes the larger honest patch. M1e decision 6's discipline applies — a new key is a table entry reviewed once, and reviewing this one produces a no. The metrics are all recorded, because M2b's evaluation must correlate them against ground truth before anyone ranks by them. See [Ranking keys we are NOT shipping](#ranking-keys-we-are-not-shipping-and-why).

<a id="decision-9"></a>
9. **`on_behavioral_split` ships OFF, and the base rate is a measurement M2b owes.** With six candidates from one model prior, splits may be routine; an escalation rule that fires on most races spends the operator's attention until they stop reading. We do not know the rate. Shipping it on would be guessing with somebody else's attention, and shipping it off with a fixture policy that turns it on is the honest form: the rule takes a **parameter** (the number of classes), the fixture uses `2`, and measuring the distribution of class counts across a real intent stream is an explicit M2b deliverable and a publishable number.

<a id="decision-10"></a>
10. **Mutation ships exactly ONE gate — `mutation-survivors-not-above` — and no score gate.** A survivor is a concrete, actionable artifact: a mutant of a line *the candidate wrote* that no test killed. The absolute count is correctly signed under diff-scoping — a larger diff makes the gate strictly harder, which is the direction we want — and it is immune to the padding attack, because padding adds mutants that may themselves survive. `mutation_score_bp` is a ratio over a denominator the candidate chooses; it is recorded, rendered, and gated by nothing. `mutation-effort-at-least` was designed and dropped: it punishes the minimal patch for being minimal.

<a id="decision-11"></a>
11. **The budget cap is a policy-declared CEILING; the scheduler may spend less, never more; the receipt records all three numbers.** CP-5's rule is that the pinned policy determines the gate, and it survives: the policy fixes the predicate, the threshold and the ceiling. Within the ceiling M2b may buy fewer mutants, and `mutants_budget` (the ceiling), `mutants_candidates` (what the tool generated over the target set) and `mutants_tested` (what actually ran) are all in the receipt, so a reader can see how partial a partial score is without consulting the policy.

<a id="decision-12"></a>
12. **The corpus runner does NOT go through pytest, and that is a security decision before it is a cost decision.** `mvo_corpus.py` is embedded in the binary, content-addressed, materialized into the read-only plugin directory beside `mvo_evidence.py`, and executed as `python3 <plugin>/mvo_corpus.py --replay …`. It imports the target modules and calls them; there is no `conftest.py` collection, no `pytest.ini`/`pyproject.toml` `addopts`, no `-p` plugin argument and no `pytest11` entry-point autoloading — **the entire surface M1f decisions 16, 17 and 24 were fighting is simply not present.** The candidate still gets arbitrary execution at import time (that is unremovable, and the threat model says so), but the observer is not a plugin inside a plugin system the candidate configures. The cost consequence falls out for free: measured, a bare interpreter plus module import is ~25 ms against pytest's ~400 ms, and 500 trivial cases add 0.12 ms.

<a id="decision-13"></a>
13. **The corpus is never DELIVERED to the generating agent — and at T0 that is delivery, not unreachability. See [amendment 29](#decision-29), which is where this decision was found to be false as built.** Ch. 13's untrusted-generator posture and AG-7's rule — scheduler and policy internals are never in generator context — extend to the oracle's inputs: a generator that can read the corpus can special-case it. Phase 0 runs before phase A, so the corpus exists on disk while agents run; it is therefore materialized **outside the worlds tree entirely**, under `<workspace>/.multiverso/corpora/<race>/` at mode 0700, with one subdirectory per world. A world's own corpus directory is created EMPTY before its keeper opens (a T1 container's mounts are fixed at open time) and the corpus file is written into it only after phase A has joined, so no world-visible path holds the inputs while an agent runs. M1b's negative test (no policy digest, gate name or oracle argv ever reaches an agent prompt) gains a case for the corpus digest and the corpus bytes, and `scripts/accept.sh` step 3r2 races a candidate that goes looking for the corpus by relative path and fails itself if it finds one.

<a id="decision-14"></a>
14. **Properties in v0 are EXACTLY the policy-declared module's ([amendment 31](#decision-31)), and that module path JOINS the compiled harness path set — enforced by [validation rule 24](#closed-vocabulary-deltas), not merely compiled into it.** A property module the candidate can edit is a property module that asserts whatever the candidate wants — the same class of hole as an editable `conftest.py`, and closed by the same gate. `OracleSpec.Corpus.Module` therefore compiles into `paths.harness`, so the tree-guard freezes it at rung O-1 and a candidate that touches it never reaches a Python run (corpus vector 16). This is legal under M1f decision 3 because no policy predating M2a can declare a property oracle, so no prior policy's compiled path set moves.

<a id="decision-15"></a>
15. **`property_cases_*` are emitted only when the per-case records reached the control-plane stream; under the JSONL fallback they are ABSENT.** Hypothesis's observability mode writes JSONL into a directory the test process can write, which makes those records candidate-authorable-after-exit — `coverage_bp`'s status (M1f decision 12), and unacceptable for a metric a gate reads. `mvo_evidence.py` therefore attempts to register a per-case callback and forward observations onto the evidence stream; when that works the records carry the stream's S1/S2/S3 protections and the metrics are emitted. When it does not — the API is explicitly experimental (ch. 19 open question 1) — the JSONL is stored as an artifact, `result.tools["hypothesis-observability"] = "jsonl"` says so, and **the metrics are absent**. One metric name, one provenance, forever: a metric whose trustworthiness varied silently by code path would be worse than no metric.

<a id="decision-16"></a>
16. **The mutation target set is derived by the control plane from the AG-4 captured patch, and its digest is recorded.** The added and modified line ranges per file come from bytes the control plane captured and hashed; the candidate chose the content but cannot change what the content *is* (M1f threat-model point 1). Files matching `paths.protected` or `paths.harness` are excluded — mutating a test file measures nothing — as are non-Python files. `inputs["diff_target"]` carries the digest of the canonical target set, so a reader can check what was in scope.

<a id="decision-17"></a>
17. **Count-truncation is admissible partial evidence; clock-truncation is machinery error.** A run stopped at `max_mutants` tested a **deterministic, control-plane-selected** prefix of the mutant population (canonical order `(path, line, col, operator, mutant_digest)`, after `max_per_line`), so it is replayable and its denominator is recorded — admissible, with `basis = construction`, because the mutants it *did* test were tested against the exact tree. A run stopped by the oracle's wall clock tested whatever the machine got through, which is not reproducible and not a fact about the candidate: `status = error`, metrics absent, and `escalation.on_all_worlds_failed_machinery` routes it to a human. Determinism is the dividing line, and it is the same line M1f drew between a failed gate and a brittle one. *Per-mutant* timeouts are different and normal: they are classified `mutants_timeout`, counted, and **excluded from `mutation_score_bp`'s denominator** — a mutant that hangs is not a mutant the tests killed. That last clause is exactly why the GATE reads the bucket even though the score does not: see [amendment 30](#decision-30), where a red team turned "excluded from the denominator" into "excluded from the only mutation gate M2a ships".

<a id="decision-18"></a>
18. **No incremental mutation cache, ever, and the tool's state directory lives in control-plane scratch.** Ch. 19 flags incremental caches as a freshness hazard ("blind to dependency/env changes"); M1f makes it sharper. mutmut's cache and cosmic-ray's session database are *evidence stores*, and under M1e/M1f semantics they would live in the candidate's writable tree, where a planted cache asserting "all mutants killed" is a two-line forgery (corpus vector 15). Every run gets a fresh session in `/mvo/scratch`, incremental mode is off, and the cache path is never inside the worktree.

<a id="decision-19"></a>
19. **cosmic-ray is the v0 default mutation adapter, reversing ch. 19's recommendation, with the reason stated.** Ch. 19 preferred mutmut "for speed and incremental state". M2a forbids incremental state ([decision 18](#decision-18)) and requires control-plane mutant *selection* ([decision 17](#decision-17)), which inverts the trade: cosmic-ray's `init` → session → `exec` model is the only one of the two that lets the control plane enumerate mutants, apply the canonical order and the cap, and then execute exactly that set. mutmut remains a supported adapter for repositories where its scoping is close enough, and its receipts record `inputs["mutant_selection"] = "tool"` rather than `"control-plane"` — an honest label on strictly weaker provenance rather than a silent equivalence.

<a id="decision-20"></a>
20. **A missing oracle toolchain is a pre-flight machinery abort, never a failing candidate.** M1e decision 15's rule, extended: the tools probe grows `hypothesis`, `mutmut` and `cosmic-ray`, and a policy requiring a kind whose tool is not importable aborts before `race.started` with the ledger untouched. On the machine this document was written on, all three are absent, so this is the live path, not a contingency.

<a id="decision-21"></a>
21. **Gate scope (`race` | `landing` | `both`), because a cohort of one is not a comparison.** `admit` has one subject; a differential gate there would have `diff_cohort_n = 1`, absent metrics, a failed gate and an admission that can never succeed — the `sealed.json` failure M1f's red team found, rebuilt. `GateSpec.Scope` defaults to `""` ⇒ `both`, which reproduces M1e/M1f semantics exactly; a gate over a cohort-stage kind **must** declare `race` (a `landing` or `both` scope on such a kind is a load error, named); and `mvo admit` prints a `race-scope gates not evaluated at admission: […]` line and renders them `n/a (race-scope)`. A landing gate set weaker than the race's is a legitimate policy choice and must never be an invisible one.

<a id="decision-22"></a>
22. **Receipts gain a scaling denominator: `cost.units` and `cost.unit`.** `cost.wall_ms` alone is unlearnable — 400 ms for eight tests and 400 ms for eight hundred are the same number and mean opposite things. With the unit count recorded, M2b fits `wall_ms ≈ fixed + per_unit × units` per repository per kind from the ledger it already has, which is the difference between a scheduler with priors and a scheduler with measurements. The existing kinds are retro-filled (`tree-guard` → `paths_examined` paths; `pytest-collect` → `collected_total` tests; `pytest-suite` → `tests_total` tests), which is free because their bytes move anyway. Unknown is `{0, ""}` — honest absence, as everywhere.

<a id="decision-23"></a>
23. **Correlation becomes a four-field descriptor on every receipt, and `family` is frozen.** `family` is load-bearing (it is the v0 dialect's selector) and must not acquire new meanings. The new `Receipt.Correlation{signal, corpus, generator, executor}` is declared per kind, recorded per receipt, and never read by `Decide` — it is scheduler metadata, and it exists so the answer to ch. 3 §3.5's open question ("nobody discounts marginal evidence value by correlation in the allocation loop") is *computable from the ledger* rather than hard-coded in a heuristic. `generator = "model:<family>"` is the field that makes "ten tests written by one model are not ten observations" machine-readable, and it is the seam LLM-synthesized corpora and properties arrive through with no contract change.

<a id="decision-24"></a>
24. **`Receipt.Inputs` records the control-plane-supplied run-time inputs a metric was derived against — provenance, not evidence.** `oracle.config` is what the policy chose; `result` is what was measured; between them sat an unrecorded gap that M1e papered over by promoting `collected_base` to a metric. `Inputs` is a closed-key, always-serialized `map[string]string` (`corpus`, `cohort`, `base_tree`, `base_observation`, `diff_target`, `mutant_selection`, `evidence_floor`), `{}` for every M1 kind. **`Decide` never reads it** — a provenance field a gate could read is a metric with extra steps — but `mvo audit`'s CAS sweep does, and `mvo explain` renders it.

<a id="decision-25"></a>
25. **The shipped default policy does not move, and every new rung is opt-in.** M1e decision 19's rule holds with more force here than it did there: a default gate that fails when Hypothesis is missing, or that adds tens of seconds of mutation to a first race, would trade honesty for brittleness at the moment a new user meets the tool. `mv0:f207c3fa…` stays the built-in default byte-for-byte, `scripts/accept.sh` step 3p's digest grep keeps passing unchanged, and the new kinds ship as validated, tested, fixture-policy'd opt-ins. **Receipt digests do move** (three additive fields), which is the M1b/M1c/M1e/M1f precedent, and replay of every prior ledger is unaffected because M1e decision 1 pairs each recorded object with the digest it was recorded under and never re-serializes.

## The corpus plane

### The corpus object (`multiverso.dev/corpus/v0`)

Materialized once per race on the base tree, `CAS.Put`, and announced by a `corpus.recorded` ledger event (observational, like `baseline.recorded`: a Receipt must bind a World, and the base tree is nobody's candidate).

```json
{"schema":"multiverso.dev/corpus/v0",
 "provider":"declared",
 "provenance":"corpora/clamp-nan.json",
 "base_tree":"git:9c1f2a3b…",
 "seed":"",
 "cases":[{"args":[{"f":"7ff8000000000000"},0,10],"id":"c0001","kwargs":{},"target":"stats:clamp"},
          {"args":[[1,2,3]],"id":"c0002","kwargs":{},"target":"stats:mean"}],
 "dropped":{"not_representable":0,"target_unresolved":0}}
```

- `id` is `c%04d`, assigned in materialization order; ids are the **only** case identity, and a corpus is immutable, so an id means the same input in every world and in every replay.
- `target` is `module:qualname`, resolved by the runner with `importlib`.
- Cases whose arguments the encoding cannot represent, or whose target does not resolve **on the base tree**, are dropped at materialization and counted. A dropped case is never silently re-added by a candidate that happens to define the missing name.
- `seed` is 32 hex for `hypothesis`, `""` otherwise.

### The value encoding (`mvo-value/v0`)

Total, canonical, and safe for DP-1 canonical JSON (no float ever appears as a JSON number):

| Python value | encoding |
|---|---|
| `None`, `bool`, `int`, `str` | the JSON value (ints of any magnitude) |
| `float` | `{"f":"<16 lowercase hex of struct.pack('>d', x)>"}` |
| `bytes` | `{"b":"<base64, no padding>"}` |
| `list` | JSON array of encoded elements |
| `tuple` | `{"t":[…]}` |
| `dict` with all-`str` keys | JSON object, keys sorted |
| `dict` otherwise | `{"m":[[k,v],…]}`, sorted by encoded key |
| `set` / `frozenset` | `{"s":[…]}`, sorted by encoded member |
| anything else | `{"o":"<type(x).__qualname__>"}` ⇒ the observation is **opaque** |

Fingerprint pre-image, canonical JSON, hashed with SHA-256 and rendered `sha256:<hex>`:

```
value   {"kind":"value","v":<encoded>}
raise   {"kind":"raise","t":"<type(e).__qualname__>"}
opaque  — no fingerprint; the case is opaque and compares equal to nothing
```

### Observation records (evidence stream `v0`, additive)

The corpus runner writes the M1f evidence stream — same header, same nonce, same strictly-increasing `seq`, same parse rules 1–7, same 64 MiB cap, same live control-plane read. One additive record kind:

```
1	session_start	{"argv_digest":"sha256:…","corpus":"mv0:3ab…","corpus_expected":"mv0:3ab…","pid":41,"runner":"mvo_corpus/v0"}
2	case	{"fp":"sha256:7ff8…","id":"c0001","outcome":"value","v":{"f":"7ff8000000000000"}}
3	case	{"id":"c0002","outcome":"opaque","t":"Decimal"}
4	case	{"fp":"sha256:1b9c…","id":"c0003","outcome":"raise","t":"ValueError"}
5	session_finish	{"cases":3,"duration_ms":31,"errored":0,"exitstatus":0,"opaque":1}
```

`outcome` ∈ `value | raise | opaque | error | timeout`. `v` (the encoded value, for the human-facing report) is included **only when its encoding is ≤ 512 bytes**; otherwise the record carries `"truncated":true` and the fingerprint alone. Unknown record kinds are ignored and counted (rule 5), so an M1f-era reader tolerates a corpus stream and an M2a reader tolerates a suite stream.

**Usability rules, additional to S1–S3.** An observation is *usable* iff the stream is usable, a `session_finish` is present, every `case` record's `id` is declared by the pinned corpus, no id repeats, **and `session_start.corpus` equals the corpus digest the race pinned**. A record naming an undeclared id makes the whole observation **unusable** — a world that reports on cases the corpus does not contain has told us it is not running our corpus (corpus vector 17). Unusable ⇒ every `corpus_*` and `diff_*` metric absent ⇒ the world's `corpus-complete` gate fails.

The fourth rule is [amendment 29](#decision-29)'s, and the field it reads had to be built before it could be read: `session_start.corpus` was written as `corpus.get("digest","")` over an object that has no `digest` member — the digest is the content address of the canonical bytes and is never inside them — so it was `""` in every stream this tree could produce. The runner now hashes the bytes it actually loaded, the control plane passes the pinned digest on argv as `--corpus-digest`, and the record carries both. A world that replayed a corpus nobody pinned says so in its OWN stream.

### Providers and the synthesis seam

| provider | inputs from | needs | cost | correlation `generator` |
|---|---|---|---|---|
| `repo-suite` | the base collect run's node-id list | nothing | **zero marginal** | `repo` |
| `declared` | a policy-named JSON file, harness-frozen | nothing | one parse | `repo+policy` |
| `hypothesis` | the repo's `@given` tests + the declared property module, on the base tree | `hypothesis` | one base-tree run | `repo+policy` |

**`hypothesis` DOES NOT SHIP as a corpus provider in v0** ([amendment 28](#decision-28)). `CorpusProviderSupported` returns true for `declared` and `repo-suite` only; a policy declaring `hypothesis` validates and then aborts the race at pre-flight with the decision-20 sentence and an untouched ledger. The row above is what the provider will be, not what this binary materializes.

**The seam for LLM-synthesized corpora, which are OUT of this block** ([Explicitly NOT in M2a](#explicitly-not-in-m2a)): a synthesized corpus is a `declared` corpus whose `provenance` names the producing model and whose receipts carry `correlation.generator = "model:<family>"`. The corpus object, the runner, the observation format, the reducer, the gates and the escalation rule are all unchanged. Adding synthesis is adding a producer and a provenance string — **no contract change**, which is the requirement.

## O2a/O2b — the cross-candidate differential

### `corpus-observe` (per world, rung O2a)

```
python3 <plugin>/mvo_corpus.py --replay --corpus <this world's corpus path> \
       --corpus-digest <the pinned mv0:…> --root <worktree>
```

Environment: `MVO_EVIDENCE_STREAM`, `MVO_EVIDENCE_NONCE`, `PYTHONPATH` **prepended** with the worktree root (M1f's prepend rule, verbatim — assigning over an operator's `PYTHONPATH` was a shipped bug once). **Each world gets its OWN copy of the corpus**, written by the control plane after phase A joins: a read-only per-world mount under T1, a per-world host path under T0. It is never written into the worktree, never staged, and never present in any world-visible path during phase A ([decision 13](#decision-13) as amended by [29](#decision-29)). The control plane re-digests that copy immediately before and immediately after every replay; a mismatch aborts the RACE as machinery and never fails a world.

| metric | meaning |
|---|---|
| `corpus_cases_total` | cases in the pinned corpus (identical in every world; from the corpus object) |
| `corpus_cases_observed` | cases this world produced a record for |
| `corpus_cases_opaque` | observations the encoding could not represent |
| `corpus_cases_errored` | cases the runner could not execute (target unresolved at run time, etc.) |

Status: `pass` iff the observation is usable and `corpus_cases_observed == corpus_cases_total`; `fail` on a non-zero exit with a usable stream; `error` under S1/S2. Family `behavior`, regime `streamed`, `cost.unit = "cases"`, `cost.units = corpus_cases_total`.

### `corpus-differential` (cohort stage, rung O2b) — the reducer

Pure. No world handle, no process, no candidate byte read directly. Inputs: the corpus object, the base observation, and the usable observations of the cohort.

- **The cohort** is every world whose `corpus-observe` receipt is `pass` **and** whose observation is usable, sorted by world digest — *both* halves, because a usable-but-short observation breaks no framing rule while failing the oracle's own status ([amendment 29](#decision-29)). The base observation is the **anchor**, never a member.
- **A member that contributes NO comparable case is excluded from the cohort**, and the exclusion is recorded in the report beside every member's contribution ([amendment 29](#decision-29)). A world that answered `opaque` or `error` on every case observed each of them, so it passes `corpus-complete` — and it compared nothing, so it is not part of a comparison.
- **The comparison denominator** `diff_cases_compared` is the set of cases observed non-opaquely by *every* remaining cohort member **and** by the base. Cases excluded are counted in `diff_cases_incomparable`.
- **The partition**: worlds with an identical fingerprint vector over the compared cases are one class. A class is named by its smallest member world digest.
- **`diff_cohort_n < 2` ⇒ every `diff_*` metric except `diff_cohort_n` is ABSENT.** A comparison of one is not a comparison, and `diff_cohort_n` itself is recorded so a reader can see *why* the others are missing.

One receipt per cohort member:

| metric | meaning |
|---|---|
| `diff_cohort_n` | worlds in the cohort |
| `diff_classes` | behaviour classes in the partition |
| `diff_class_size` | size of **this** world's class |
| `diff_cases_compared` | the denominator |
| `diff_cases_incomparable` | cases dropped from the denominator |
| `diff_cases_divergent` | compared cases where this world differs from ≥1 cohort member |
| `diff_cases_unilateral` | compared cases where this world is a singleton |
| `diff_cases_vs_base` | compared cases where this world differs from the base observation |
| `diff_cases_unilateral_vs_base` | compared cases where this world differs from base **and every other member agrees with base** |

The last two are **recorded and not consumed**: [decision 7](#decision-7) shows "agrees with base" is indistinguishable from "did nothing" without a reproduction test, and M2b's evaluation needs the numbers to correlate against ground truth before anyone builds on them.

`Result.Detail` is `behavior class mv0:aa1… (cohort mv0:7f3…, corpus mv0:3ab…, first distinguishing case c0001)` — non-numeric, one line, with `-` for any fact this receipt does not have. The two extra fields are [amendment 32](#decision-32)'s: `Decide` may read neither `Inputs` nor CAS, and `on_behavioral_split`'s frozen sentence names both of them, so `result.detail` is the only channel that can carry them. `Result.Artifacts` is `[differential-report]`, the same key for every member of the cohort (one artifact, N referrers). `Inputs` carries `corpus`, `cohort`, `base_tree`, `base_observation`, `evidence_floor`. `Execution` is `{argv: [], exit_code: 0, evidence_regime: "derived", evidence_plugin: ""}`. `cost.unit = "world-cases"`, `cost.units = diff_cohort_n × diff_cases_compared`.

### The report artifact (`multiverso.dev/differential-report/v0`)

```json
{"schema":"multiverso.dev/differential-report/v0",
 "corpus":"mv0:3ab…","provider":"declared",
 "base_observation":"sha256:…","base_tree":"git:9c1f2a3b…",
 "cohort":["mv0:aa1…","mv0:bb2…"],
 "excluded":[],"member_cases_comparable":{"mv0:aa1…":4,"mv0:bb2…":4},
 "cases_compared":4,"cases_incomparable":0,
 "classes":[{"agrees_with_base":true,"id":"mv0:aa1…","members":["mv0:aa1…"]},
            {"agrees_with_base":false,"id":"mv0:bb2…","members":["mv0:bb2…"]}],
 "distinguishing":[{"args":[{"f":"7ff8000000000000"},0,10],"case":"c0001","target":"stats:clamp",
   "base":{"fp":"sha256:1c4…","outcome":"value","v":{"f":"7ff8000000000000"}},
   "observations":[{"fp":"sha256:1c4…","outcome":"value","v":{"f":"7ff8000000000000"},"world":"mv0:aa1…"},
                   {"fp":"sha256:9d0…","outcome":"value","v":0,"world":"mv0:bb2…"}]}],
 "distinguishing_truncated":0}
```

`distinguishing` is capped at 32 cases (by case id) with the overflow counted; it is the ESCALATE payload, and an unbounded one would be a candidate-driven denial of a maintainer's attention.

### `mvo explain` — the behaviour block

```
behavior (corpus mv0:3ab…, provider declared, 4 cases compared, 0 incomparable):
  CLASS      WORLDS      vs BASE
  mv0:aa1…   mv0:aa1…    same
  mv0:bb2…   mv0:bb2…    changed

  distinguishing cases (2 classes):
    c0001  stats:clamp(nan, 0, 10)
             base      → nan
             mv0:aa1…  → nan
             mv0:bb2…  → 0

escalation:
  rule on_behavioral_split: 2 worlds split into 2 behavior classes over 4 compared cases
    of corpus mv0:3ab…; the shared evidence does not say which behavior is intended
    (first distinguishing case c0001)
```

The ranking walk above it is retitled and re-marked exactly as M1e decision 21 requires — the words `won` and `WINNER … ← decided here` appear only under SELECT.

## O2p — the property oracle

Kind `hypothesis-properties`, family `property`, rung O2p. Probe first (the M1e tools probe, extended), then:

```
[python3 -m coverage run]                                  # iff spec.coverage && coverage.py present
  <prefix> -p mvo_evidence -p no:cacheprovider
           --junit-xml=… [<the declared property module>] [args…]
HYPOTHESIS_EXPERIMENTAL_OBSERVABILITY=<scratch>  PYTEST_DISABLE_PLUGIN_AUTOLOAD=1
```

| source | metrics | provenance |
|---|---|---|
| the evidence stream | `properties_total`, `properties_passed`, `properties_failed`, `properties_errored` | in-process forgeable only; S1/S2/S3 apply |
| the stream, via the observability callback | `property_cases_total`, `property_cases_invalid` | same |
| `.hypothesis/observed/*.jsonl` | **nothing** — stored as an artifact, metrics absent ([decision 15](#decision-15)) | candidate-authorable after exit |

`property_cases_invalid` is the honest-degradation metric that PBT usually hides: a property whose `assume()` filters rejected almost every draw has *searched nothing* while reporting a pass. `property-cases-at-least` is the gate for it, and it is correctly signed — the case budget is fixed by the corpus config and identical across worlds, so a low count means the search collapsed, not that the candidate is small.

Where properties come from in v0 is [decision 14](#decision-14): the repo's own `@given` tests, plus `corpus.module` — a policy-declared module path that **compiles into the harness path set**, so a candidate that edits it dies at rung O-1.

## O3 — the diff-scoped mutation oracle

Kind `mutation-diff`, family `mutation`, rung O3. Google's recipe, transplanted with its constraints intact (ch. 8 §8.1: exhaustive mutation "does not scale"; diff-based mutation with per-line caps and operator selection yields "orders of magnitude fewer mutants").

**Sequence**, all state in `/mvo/scratch`, never in the worktree:

1. **Target set** — the control plane parses the AG-4 captured patch into `{path: [line ranges]}`, drops `protected`/`harness`/non-Python paths, canonicalizes, digests → `inputs["diff_target"]` ([decision 16](#decision-16)). An **empty target set is not an error**: `mutants_candidates = 0`, `mutation_score_bp` absent, `mutants_survived = 0`, and the gate passes vacuously — a patch that changed no mutable source line has nothing for mutation to say about it, and pretending otherwise would be the fabricated-zero failure.
2. **Baseline** — one suite run on the unmutated tree in the same session, to establish that the tests pass before mutants are introduced and to fix the per-mutant timeout at `5 × baseline` (cargo-mutants' heuristic, ch. 19). A failing baseline is `status = error`: mutation over a red suite measures nothing.
3. **Enumerate** — the adapter lists the mutants the tool would generate over the target set.
4. **Select** — `max_per_line` first, then canonical order `(path, line, col, operator, mutant_digest)`, then the first `max_mutants`. Control-plane, deterministic, replayable ([decision 17](#decision-17)).
5. **Execute** — the selected set only, under the per-mutant timeout, with the whole-oracle wall bound from the spec.

| metric | meaning |
|---|---|
| `mutation_lines_targeted` | diff lines in scope (control-plane derived — **not candidate-authorable**) |
| `mutants_budget` | the policy's ceiling |
| `mutants_candidates` | mutants the tool generated over the target set, before the cap |
| `mutants_tested` | mutants actually executed |
| `mutants_killed` / `mutants_survived` / `mutants_timeout` / `mutants_unviable` | outcomes |
| `mutation_score_bp` | `killed·10000 / (killed + survived)`; absent when the denominator is 0 |

Timeouts and unviable mutants are excluded from the score's denominator and counted separately, because "the mutant hung" and "the mutant did not import" are not "the tests caught it".

`Result.Artifacts` = `[mutation-report]`, `multiverso.dev/mutation-report/v0`: the target set, the selected mutants with their diffs, and each survivor's mutant diff — the actionable half, and the only reason a mutation number is worth a maintainer's time.

## Cost model — what M2b consumes

### The declared profile (`internal/policy/profile.go`)

Static, closed, per kind; printed by `mvo oracles`; **no numbers**, because numbers are per-repository and must be measured.

```go
// OracleProfile is the DECLARED shape of a kind's cost and of the
// correlation structure of its evidence. It is data the scheduler reads
// without running anything, and it never enters Decide.
type OracleProfile struct {
    Stage       string // "world" | "cohort"
    Dominant    string // what dominates: "tree-walk" | "interpreter-start" |
                       // "suite-run" | "case-replay" | "suite-run-per-mutant" | "hashing"
    Unit        string // the scaling unit recorded in cost.unit
    Cap         string // the policy field that bounds it; "" = unbounded
    Amortized   bool   // the cost is per RACE, not per world (corpus materialization)
    Discriminate string // "verdict" | "ordinal" | "partition" | "none"
    Corr        Correlation
}
```

| kind | dominant | unit | cap | discriminates | signal | generator | executor |
|---|---|---|---|---|---|---|---|
| `tree-guard` | tree-walk | `paths` | — | verdict | `tree-bytes` | `control-plane` | `control-plane` |
| `pytest-collect` | interpreter-start | `tests` | — | verdict | `test-identity` | `repo` | `candidate-process` |
| `pytest-suite` | suite-run | `tests` | — | verdict | `test-outcomes` | `repo` | `candidate-process` |
| `hypothesis-properties` | case-replay | `cases` | `corpus.cases_max` | verdict | `value-behavior` | `repo+policy` | `candidate-process` |
| `corpus-observe` | case-replay | `cases` | `corpus.cases_max` | none | `value-behavior` | `base-tree` | `candidate-process` |
| `corpus-differential` | hashing | `world-cases` | — | **partition** | `value-behavior` | `base-tree` | `control-plane` |
| `mutation-diff` | suite-run-per-mutant | `mutants` | `mutation.max_mutants` | ordinal | `suite-adequacy` | `control-plane` | `candidate-process` |

### The discount rule M2b implements

Ch. 3 §3.5's open problem is "how much is the next purchase actually worth, given what we already bought?" M2a does not solve it; it makes it *computable*, and states the rule the scheduler is expected to start from:

1. **Same `signal` AND same `corpus` ⇒ near-duplicate.** `corpus-observe` and `corpus-differential` read the same bytes; buying both is buying one observation and one arithmetic pass over it. A second `pytest-suite` instance with different args is a second sample of the same signal.
2. **Same `generator` ⇒ strongly correlated.** Everything the repository's own authors wrote — suite, collect, the repo's properties — shares a prior. Ten tests written by one model are not ten observations; ten tests written by one *team* are not ten either.
3. **Independent only when `signal` AND `generator` both differ.** By that test the genuinely independent pairs on this menu are `tree-guard` × everything (it reads bytes, not behaviour), `mutation-diff` × `pytest-suite` (adequacy of the tests vs outcome of the tests), and `corpus-differential` × `pytest-suite` (value behaviour on control-plane inputs vs test outcomes on repo inputs). Ch. 8's ordering agrees: compile/typecheck, reference-differential, PBT/MT and adversarial counterexamples are the most mutually independent sources; suite, coverage and model-written example tests are one cluster.
4. **`executor = "candidate-process"` bounds the value of everything.** Under M1f's residual, any number a candidate's own process reported is forgeable by a competent in-process adversary. Buying a third candidate-process oracle does not buy independence from that adversary; it buys independence from *honest error*. The scheduler should price those two things separately and the receipts now let it.

### Measured coefficients: `mvo oracles`

```
mvo oracles [--json] [--policy <ref>] [--dir DIR]

KIND                   FAMILY    STAGE   SIGNAL          GENERATOR      DISCRIM.   SCALES BY     CAP
tree-guard             tree      world   tree-bytes      control-plane  verdict    paths         —
pytest-collect         collect   world   test-identity   repo           verdict    tests         —
pytest-suite           suite     world   test-outcomes   repo           verdict    tests         —
hypothesis-properties  property  world   value-behavior  repo+policy    verdict    cases         corpus.cases_max
corpus-observe         behavior  world   value-behavior  base-tree      —          cases         corpus.cases_max
corpus-differential    behavior  cohort  value-behavior  base-tree      partition  world-cases   —
mutation-diff          mutation  world   suite-adequacy  control-plane  ordinal    mutants       mutation.max_mutants

measured in this workspace:
  pytest-collect       fixed 397 ms  +  0.4 ms/test     (n=37)
  pytest-suite         fixed 402 ms  +  3.1 ms/test     (n=37)
  corpus-observe       no local measurement (n=2, need 3)
  mutation-diff        no local measurement (n=0)
```

Fitted by least squares over `(cost.units, cost.wall_ms)` pairs from this workspace's `receipt.recorded` events, per kind. **Fewer than three receipts prints "no local measurement" and the count** — a two-point fit rendered as a measurement is the over-claim, and a scheduler that reads the JSON gets `null` rather than a guess.

## Policy wire schema — the additive `v1` delta

Every field below satisfies M1f decision 3: its compiled default reproduces M1f semantics exactly, and no new rationale text is emitted on any input a pre-M2a policy can produce.

```go
type PolicyV1 struct {
    // … M1e/M1f fields unchanged …
    // (no new top-level field: budgets are M2b, and caps belong on the
    //  instance whose config digest the policy pins — CP-5.)
}

type GateSpec struct {
    Gate      string `json:"gate"`
    Oracle    string `json:"oracle"`
    Basis     string `json:"basis"`
    Threshold int64  `json:"threshold"`
    Scope     string `json:"scope"` // NEW: "" ⇒ "both" | "race" | "landing"
}

type OracleSpec struct {
    // … M1e fields unchanged …
    Corpus   CorpusSpec   `json:"corpus"`   // NEW: corpus-observe, hypothesis-properties
    Mutation MutationSpec `json:"mutation"` // NEW: mutation-diff
}

// CorpusSpec declares where the shared input corpus comes from. The
// provider vocabulary is closed; Module is a repo-relative path that
// COMPILES INTO paths.harness (decision 14).
type CorpusSpec struct {
    Provider string `json:"provider"`  // "" ⇒ none | "repo-suite" | "declared" | "hypothesis"
    File     string `json:"file"`      // provider=declared: the corpus JSON, repo-relative
    Module   string `json:"module"`    // provider=hypothesis: the property module
    CasesMax int64  `json:"cases_max"` // 0 ⇒ 100; the materialization ceiling
    Seed     string `json:"seed"`      // provider=hypothesis: 32 hex; "" ⇒ derived from the base tree
}

// MutationSpec is the budget cap as a first-class, PINNED parameter
// (decision 11): a ceiling the scheduler may spend under, never over.
type MutationSpec struct {
    Tool             string   `json:"tool"`                // "" ⇒ "auto" | "cosmic-ray" | "mutmut"
    MaxMutants       int64    `json:"max_mutants"`         // 0 ⇒ 20
    MaxPerLine       int64    `json:"max_per_line"`        // 0 ⇒ 2
    Operators        []string `json:"operators"`           // [] ⇒ the tool's default set, sorted
    TimeoutPerMutant int64    `json:"timeout_per_mutant_ms"` // 0 ⇒ 5 × the baseline suite run
}

type EscalationSpec struct {
    // … M1e/M1f rules unchanged …
    OnBehavioralSplit int `json:"on_behavioral_split"` // NEW: 0 = off; N = escalate at ≥ N classes
}
```

`OnBehavioralSplit` is an int whose zero means off, matching `MinCandidatesPassing` rather than M1f decision 4's tri-state strings — and the distinction is real: decision 4 forbids a zero-value that makes an *old policy silently less checked*, whereas "escalation rule off" is exactly what every pre-M2a policy had. Reproducing the prior state is the requirement; a tri-state here would encode a difference that does not exist.

### Closed vocabulary deltas

**Gate predicates** (added to M1e's five and M1f's two):

| `gate` | passes iff | `threshold` | fail reason format |
|---|---|---|---|
| `corpus-complete` | `corpus_cases_observed == corpus_cases_total` | unused | `corpus_cases_observed=%d of %d` |
| `properties-pass` | `properties_failed == 0 && properties_errored == 0` | unused | `properties_failed=%d properties_errored=%d` |
| `property-cases-at-least` | `property_cases_total >= threshold` | cases | `property_cases_total=%d (want >= %d)` |
| `differential-cohort-at-least` | `diff_cohort_n >= threshold` | worlds, ≥ 2 | `diff_cohort_n=%d (want >= %d)` |
| `mutation-survivors-not-above` | `mutants_survived + mutants_timeout <= threshold` ([amendment 30](#decision-30)) | mutants | `mutants_survived=%d mutants_timeout=%d (want survived+timeout <= %d)` |

**Oracle kinds:**

| kind | family | stage | metrics |
|---|---|---|---|
| `corpus-observe` | `behavior` | world | `corpus_cases_total`, `corpus_cases_observed`, `corpus_cases_opaque`, `corpus_cases_errored` |
| `corpus-differential` | `behavior` | cohort | `diff_cohort_n`, `diff_classes`, `diff_class_size`, `diff_cases_compared`, `diff_cases_incomparable`, `diff_cases_divergent`, `diff_cases_unilateral`, `diff_cases_vs_base`, `diff_cases_unilateral_vs_base` |
| `hypothesis-properties` | `property` | world | `properties_total`, `properties_passed`, `properties_failed`, `properties_errored`, `property_cases_total`, `property_cases_invalid`, `duration_ms`, `coverage_bp` |
| `mutation-diff` | `mutation` | world | `mutation_lines_targeted`, `mutants_budget`, `mutants_candidates`, `mutants_tested`, `mutants_killed`, `mutants_survived`, `mutants_timeout`, `mutants_unviable`, `mutation_score_bp` |

**Ranking keys: none added.** See below.

**Additional validation rules** (numbering continues M1f's 15):

16. `corpus.provider` ∈ the closed set; `declared` requires `file`; `hypothesis` requires `module` **or** a repo with `@given` tests (unverifiable at load, so `module` is required when the policy also declares a `hypothesis-properties` gate); `cases_max ≥ 0`; `seed` is `""` or 32 lowercase hex.
17. A `corpus-differential` oracle declares no `argv`, `args`, `coverage`, `reruns`, `timeout_ms`, `corpus` or `mutation` (it runs no process and reads no configuration of its own) — non-zero values are a load error, the `tree-guard` rule (M1f 15) reused.
18. A policy declaring a `corpus-differential` oracle must also declare exactly one `corpus-observe` oracle, and the two are bound at compile time. Two observers would make "the cohort" ambiguous; zero would make the reducer a function of nothing.
19. Every gate over a **cohort-stage** kind declares `scope: "race"`. `both`/`landing` is a load error naming the kind: `hard_gates[3]: differential-cohort-at-least@diff is a cohort-stage gate and must declare scope "race" (admission has one subject, so a cohort of one is not a comparison)`.
20. `mutation.max_mutants ≥ 0`, `max_per_line ≥ 0`, `timeout_per_mutant_ms ≥ 0`; `tool` ∈ {`""`, `auto`, `cosmic-ray`, `mutmut`}; every `operators` entry is in the selected tool's declared set, sorted and unique.
21. `escalation.on_behavioral_split ≥ 0`, and `> 0` requires a declared `corpus-differential` oracle: a rule that can never fire is an authoring bug, and M1e decision 7's "unknown = load error" generalizes to "unreachable = load error".
22. `corpus.module` and `corpus.file` must not match `paths.protected`; they are appended to the compiled `paths.harness` set ([decision 14](#decision-14)) and a pattern that would make them unreachable is a load error.
24. **A policy whose compiled `paths.harness` is non-empty because of `corpus.file` or `corpus.module` MUST declare a `paths-unmodified` hard gate.** The converse of rule 12, and the rule [decision 14](#decision-14) assumed without stating: rule 12 refuses a `paths-unmodified` gate with nothing to guard, and nothing refused paths to guard with no gate. The load error names the path — `corpora/clamp-nan.json compiles into paths.harness but no paths-unmodified gate evaluates it; a freeze nothing checks is not a freeze` — and rule 12 now counts the corpus-derived paths, so a policy whose only frozen path is its corpus satisfies both. It costs no prior policy anything: no policy predating M2a can declare a corpus. Added during integration; see [amendment 33](#decision-33).
23. Every gate over a kind whose run-time INPUTS the race materializes — `corpus-observe`, whose corpus phase 0 produces on the base tree — declares `scope: "race"`. `both`/`landing` is a load error naming the reason: `hard_gates[2]: corpus-complete@observe reads a corpus materialized by the race's phase 0 and must declare scope "race" (admission has no phase 0, so the corpus it replays does not exist there)`. Added during integration; see [decision 26](#decision-26) for why rule 19 was not enough.

### Ranking keys we are NOT shipping, and why

Four keys were designed, argued and refused. Recording the refusal is the point: the next person to want one should have to beat this argument, not rediscover it.

| refused key | reads | why it is wrong-signed |
|---|---|---|
| `diff_class_size_desc` ("agree with the most siblings") | `diff_class_size` | A popularity contest among samples from one model prior. Ch. 8 §8.4's common-mode caveat; ch. 3 §3.5's CAPA result (errors are becoming *more* correlated). It selects *for* the shared bug, and the case where it fails is exactly the case the product exists for. |
| `diff_unilateral_asc` ("changed least vs base") | `diff_cases_unilateral_vs_base` | Ranks "did nothing" above "did the job". The intent is to change behaviour; the honest fix is the world that differs from base. Verified against the adversarial corpus: vectors 06–08 leave the bug intact and therefore agree with base, while `01-honest_fix` does not. |
| `mutation_score_desc` | `mutation_score_bp` | A ratio whose denominator is the candidate's own diff. Inflated by padding the diff with lines whose every mutant is trivially killed (corpus vector 14). |
| `mutants_survived_asc` | `mutants_survived` | Punishes the larger honest patch for being larger, which `patch_size_asc` already does honestly and without pretending to measure adequacy. |

The metrics all ship. The keys do not. What ships instead is the escalation rule and one cohort gate, and the directional key waits for the fail-to-pass reproduction test that would give it a sign ([decision 7](#decision-7)).

### Escalation — one rule added

Precedence, first match wins. The list is a name, not an index; inserting `on_behavioral_split` changes no historical evaluation because no pre-M2a policy can declare it (M1f decision 11's argument, reused).

| # | rule | fires when | replaces |
|---|---|---|---|
| 0 | `on_invariant_violation` | (M1f, unchanged) | SELECT / REJECT |
| 1 | `on_all_worlds_failed_machinery` | (M1e, unchanged) | REJECT |
| **1b** | **`on_behavioral_split`** | **`diff_classes >= N` over a cohort of ≥ 2** | **SELECT** |
| 2 | `require_evidence` | (M1e, unchanged) | SELECT |
| 3 | `min_candidates_passing` | (M1e, unchanged) | SELECT |
| 4 | `on_ranking_tie` | (M1f default, unchanged) | SELECT |

It sits **below** machinery failure (if nothing produced evidence, "they disagree" is a false statement about a race that never ran) and **above** `require_evidence` (a detected behavioural ambiguity is a stronger reason to stop than a missing optional source). It also sits **below the `pass_count == 0` guard**, and that is load-bearing rather than incidental: the table above says rule 1b REPLACES SELECT and rule 1 is the only REJECT-replacer, and evaluating 1b above the guard turned a REJECT into "the shared evidence does not say which behavior is intended" for a race in which every world failed a hard gate — where the shared evidence says exactly that, about all of them. Reproduced under a gate order of `[collect-nonempty, corpus-complete@observe, status-pass@suite]`, which the shipped `menu.json` also has; fixed, with a `Decide` golden. Rule sentence, byte-exact:

```
on_behavioral_split → "%d worlds split into %d behavior classes over %d compared cases of
                       corpus %s; the shared evidence does not say which behavior is
                       intended (first distinguishing case %s)"
```

## Receipt & envelope extensions (`internal/object`)

```go
type Cost struct {
    WallMS int64  `json:"wall_ms"`
    Units  int64  `json:"units"` // NEW — the denominator that makes wall_ms learnable
    Unit   string `json:"unit"`  // NEW — the kind's declared scaling unit; "" iff Units == 0
}

// Correlation is SCHEDULER metadata: declared per kind, recorded per
// receipt, never read by Decide (decision 23). Family stays where it is and
// means what it always meant — it is the v0 dialect's selector.
type Correlation struct {
    Signal    string `json:"signal"`    // tree-bytes | test-identity | test-outcomes |
                                        // value-behavior | suite-adequacy
    Corpus    string `json:"corpus"`    // "" | "repo-suite" | a corpus digest
    Generator string `json:"generator"` // control-plane | base-tree | repo | repo+policy | model:<family>
    Executor  string `json:"executor"`  // candidate-process | control-plane
}

type Receipt struct {
    // … M1e/M1f fields unchanged, INCLUDING Family …
    Inputs      map[string]string `json:"inputs"`      // NEW — provenance, not evidence
    Correlation Correlation       `json:"correlation"` // NEW
}

const RegimeDerived = "derived" // NEW — decision 3
```

`Inputs` keys are a closed vocabulary: `corpus`, `cohort`, `base_tree`, `base_observation`, `diff_target`, `mutant_selection`, `evidence_floor`. The map is always serialized and is `{}` for every M1 kind. All three fields follow M1b decision 5 — no `omitempty` games — so **new receipts get new digests**, exactly as in M1b, M1c, M1e and M1f, and **replay of pre-M2a ledgers is unaffected** because M1e decision 1 never re-serializes a recorded object.

### Metric provenance (delta)

The table a reviewer needs, extended. "Who can author this number" remains the only question that matters once the receipt is signed.

| metric | authoritative source | authorable by the candidate |
|---|---|---|
| `corpus_cases_total` | the corpus object, materialized on the **base** tree | **no**, in every regime |
| `mutation_lines_targeted` | the AG-4 captured patch, parsed by the control plane | **no** |
| `corpus_cases_*` (observed/opaque/errored) | the run's evidence stream | only in-process: cannot be suppressed into a pass (S1), cannot be authored after exit, can be forged by an adversary already executing in the process |
| `properties_*` | the run's evidence stream | same |
| `property_cases_*` | the stream, via the observability callback | same — and **absent** under the JSONL fallback ([decision 15](#decision-15)) |
| `mutants_*`, `mutation_score_bp` | the run's evidence stream | same |
| `diff_*` | the **control-plane reducer**, over cohort observations | **not directly — and this is a new attack class, stated in full below** |

## Trust boundary

### Evidence regime per new oracle

| oracle | regime | what the candidate cannot do |
|---|---|---|
| `corpus-observe` | `streamed` | delete records already received; author a metric after exit; produce a pass by silencing the runner (S1); report on a case the corpus does not declare |
| `corpus-differential` | `derived` | run any code in this receipt's production; influence the partition except through its own observation |
| `hypothesis-properties` | `streamed` | as `pytest-suite`, plus: edit the declared property module (harness-frozen at rung O-1) |
| `mutation-diff` | `streamed` | choose which mutants run (control-plane selection under `cosmic-ray`); plant a cache the run will read; make the target set exclude a line it wrote |

### The new attack class this design introduces, stated plainly

**A forged observation is a forged statement about a sibling.** Every other oracle's metrics describe one world; the differential's describe a world *relative to a cohort*, so a candidate that forges its own `case` records changes not only its own `diff_*` numbers but the `diff_cases_unilateral*` counts of every other world. Nothing in this design prevents that: the reducer is control-plane arithmetic over inputs that are, at the floor, `streamed` — which is why `evidence_regime` is `derived` and `inputs["evidence_floor"]` names `streamed` ([decision 3](#decision-3)) rather than the flattering `control-plane`.

It is tolerable for exactly one reason, and it is the same reason the differential cannot convict: **its output is an escalation to a human with the inputs attached**, not a verdict. A forged observation that manufactures a split costs the adversary a maintainer reading the distinguishing case — where the forgery is visible, because the maintainer can run `stats.clamp(nan, 0, 10)` themselves. A forged observation that *suppresses* a split hides a difference that the rest of the ladder was never going to catch anyway. Neither converts into a landed patch. If the differential ever acquires a hard gate that can reject a rival, this analysis must be redone — which is a second, independent reason [decision 8](#decision-8) ships no ranking key.

### New corpus vectors (`testdata/adversarial/vectors/`)

Six, one per new surface, added to the thirteen M1e/M1f vectors and to `scripts/adversarial.sh`'s recorded baseline.

| # | vector | mechanism | verdict |
|---|---|---|---|
| 14 | `mutation_padding` | pads the diff with lines whose every mutant is trivially killed, inflating `mutation_score_bp` | **NEUTRALIZED** — the only mutation gate reads `mutants_survived` (absolute), padding does not reduce it, and no ranking key reads the ratio ([decision 10](#decision-10)) |
| 15 | `mutation_cache_plant` | ships a `.mutmut-cache` / cosmic-ray session DB asserting every mutant killed | **STOPPED** — the session lives in `/mvo/scratch`, incremental mode is off, the path is explicit ([decision 18](#decision-18)); and the file is a tree addition the reviewer sees |
| 16 | `property_module_edit` | rewrites the policy-declared property module so every property is vacuous | **STOPPED** — the declared module path compiles into `paths.harness`, so the guard fails at rung O-1 before any Python runs ([decision 14](#decision-14)) |
| 17 | `corpus_case_forgery` | emits `case` records for ids the corpus does not declare, and a fabricated fingerprint for a declared case it never executed | **DETECTED (undeclared ids) / OPEN (declared ids)** — an undeclared id makes the whole observation unusable ⇒ metrics absent ⇒ `corpus-complete` fails; a forged fingerprint for a declared case is the M1f in-process residual, restated and not re-solved |
| 18 | `cohort_starvation` | silences the corpus runner to shrink the cohort and change a rival's unilateral counts | **DETECTED** — no usable observation ⇒ this world's `corpus_*` and `diff_*` metrics absent ⇒ its own `corpus-complete` gate fails (self-elimination), and `diff_cohort_n` records the shrinkage so every surviving number has a named denominator. With a differential *hard* gate and N=2 this is a denial of the comparison to the whole race, which is why the shipped fixture uses escalation rather than a gate and why `differential-cohort-at-least` is documented as an operator choice with a stated cost |
| 18b | `short_observation` | emits a well-formed stream carrying a SUBSET of the declared case ids | **DETECTED** — found by the red team, and it worked. The stream breaks no framing rule, so it parsed as *usable* while the oracle's own status was `fail`, and the cohort filtered on parseability alone: the already-eliminated world entered the comparison and, because the denominator is an intersection over every member, deleted the distinguishing case for every honest sibling. Reproduced end to end: `cohort_n=3, compared=1, classes=1, distinguishing=0`, and the race degraded to a blind `on_ranking_tie`. Closed by filtering the cohort on the observe RECEIPT's verdict ([amendment 29](#decision-29)) |
| 18c | `opaque_everywhere` | observes every declared case and answers `opaque`/`error` on all of them | **DETECTED** — and this one passes its own gate: `corpus_cases_observed == corpus_cases_total`, so `corpus-complete` is satisfied while the intersection collapses to zero for the WHOLE race. Reproduced: `pass=True` on all three worlds, `cases_compared=0`, `classes=1`, the split gone, `on_ranking_tie` instead. Nothing gated or escalated on `diff_cases_compared`, so it was silent. A member contributing zero comparable cases is now excluded and the exclusion recorded ([amendment 29](#decision-29)). **The residual is OPEN**: a member contributing SOME cases still shrinks the denominator for everyone, and `member_cases_comparable` in the report is what makes it visible rather than what prevents it |
| 18d | `corpus_poisoning` | rewrites the shared corpus file a SIBLING world will replay | **DETECTED at T0, STOPPED at T1** — found by the red team, and both payoffs ran. The corpus was one file, a sibling of every world's worktree, owned by the uid the candidate's process runs as, and re-read from disk on every replay. *Elimination*: declare a case id nobody pinned, and the honest sibling's observation becomes unusable — the victim convicted of tampering it did not do, the poisoner SELECTed and ADMITted. *Suppression*: preserve every id and rewrite one argument so both worlds agree; `on_behavioral_split` never fires (reproduced 3/3 under `--parallel 2`). Neither touched a protected or harness path. Now: one corpus copy per world, delivered after phase A; the control plane re-digests it either side of every replay and aborts the RACE as machinery on a mismatch; and the observation itself is bound to the bytes it loaded, so a poisoned replay is self-reporting. At T1 the per-world directory is a read-only bind and no sibling container can reach it. **At T0 the residual is OPEN and named**: same uid, same filesystem, so a sibling that walks to another world's directory can still write it — what the fix removes is the shared object and the silent outcome, not the filesystem |
| 20 | `mutation_tool_shadow` | ships a `cosmic_ray/` package in the tree root that shadows the installed tool | **DETECTED** — found by the red team, and it worked. `python -m cosmic_ray.cli` runs with cwd = the worktree, so `sys.path[0]` is the candidate's tree; a six-line `cosmic_ray/cli.py` whose `init` exits 0 and whose `dump` prints nothing gave `mutants_candidates=0` beside the control plane's own `mutation_lines_targeted=18`, and the survivor gate passed vacuously. No harness glob was ever going to close it — the shadow is an ordinary directory named after a tool. Two layers now: the enumeration runs under `PYTHONSAFEPATH=1` (the interpreter's own switch, so cwd is not prepended; the mutant EXECUTION steps deliberately keep cwd, because cosmic-ray's worker spawns the repository's suite), and `mutants_candidates == 0` over a NON-EMPTY target set is a machinery ERROR with a named reason rather than a vacuous pass. **Residual, OPEN**: interpreters older than 3.11 ignore `PYTHONSAFEPATH`, so on those the contradiction check is the only layer. Honest negative alongside it: a planted `cosmic_ray-99.9.9.dist-info` forging the probe's VERSION is stopped by the tree-guard on `harness_added=1` |
| 21 | `mutation_hang` | makes one mutant of its own diff exceed `5 ×` the baseline suite | **DETECTED** — found by the red team, and it worked. A per-mutant timeout is classified `mutants_timeout` and excluded from `mutation_score_bp`'s denominator ([decision 17](#decision-17)) — and it was also excluded from the only mutation gate M2a ships, so a mutant the suite provably failed to kill cost the candidate nothing. Run end to end against a stand-in tool: `mutants_killed=2, mutants_survived=0, mutants_timeout=1, mutation_score_bp=10000`, gate PASS, race SELECT, and the report printed `survivors: 0` beside it. `mutation-survivors-not-above` now reads `survived + timeout` ([amendment 30](#decision-30)) and the report lists timed-out mutants with their diffs. The SCORE still excludes them, because decision 17 is about what a ratio means and the gate is about what the tests did |
| 19 | `differential_conformity` | reads the corpus at run time and special-cases exactly its inputs, keeping the bug everywhere else | **OPEN** — the corpus is delivered to the world because the world must execute it. The forgery must live in the reviewed diff, in application code, and it is the classic overfit-to-the-oracle. Withholding the inputs from the executing process is not possible; withholding them from the *generating* agent is, and is done ([decision 13](#decision-13)) |

### What is stopped, what is detected, what is open — the delta

Definitions unchanged from M1f (STOPPED = the capability is removed; DETECTED = the vector executes and produces a signal that fails the run; OPEN = no coverage claimed).

**A correction to M1f's table, which over-claimed on our behalf.** M1f's row for vectors 4 and 5 (assertion weakening and rewriting, under a relaxed guard) reads *"not detectable from test outcomes at all. This is what O3 mutation testing (EP-5, M2) is for."* **That is only true of whole-suite mutation, which does not scale and which M2a does not ship.** Diff-scoped mutation over a patch whose entire content is a test-file edit has an **empty target set**: `mutation_lines_targeted = 0`, no mutants, no score, and the survivor gate passes vacuously ([O3 step 1](#o3--the-diff-scoped-mutation-oracle)). Mutation says nothing about those vectors.

What *does* address them under a relaxed guard is the **differential**: a candidate that rewrote assertions to match its own wrong output still behaves wrongly on the corpus, so it lands in a different behaviour class from the honest fix on every corpus case that exercises the defect, and `on_behavioral_split` escalates with the input and both outputs attached. That is DETECTED-as-ambiguity, not DETECTED-as-conviction — the differential does not say which class is right — and it is the honest strength of the mechanism rather than the one M1f's table promised.

| vector | M1f verdict | M2a verdict | mechanism |
|---|---|---|---|
| 4, 5 — assertion weakening / rewriting, guard relaxed | OPEN ("what O3 is for") | **DETECTED as ambiguity** | the cheat and the honest fix fall in different behaviour classes on the corpus ⇒ `on_behavioral_split` escalates with the distinguishing input. **Not** mutation: the target set is empty |
| tests that never assert what the patch does | OPEN | **DETECTED** | `mutation-survivors-not-above` over the diff's own lines: a suite that cannot kill a mutant of a line the candidate just wrote is a suite that stopped asserting there |
| two candidates that pass every gate and behave differently | OPEN (decided by `world_digest_asc`, or escalated by `on_ranking_tie` with no explanation) | **DETECTED, with the input** | `on_behavioral_split` + the `differential-report`'s distinguishing cases. This is the product |
| a competent in-process forgery of any streamed number | OPEN | **OPEN** | unchanged, and now with more surfaces: the corpus runner, the property run and the mutation run are all `streamed`. M2a adds no new defence here and claims none |
| a backdoor in application code that passes every honest test **and** agrees with every sibling on the corpus | OPEN | **OPEN** | ch. 13's actual problem. The differential narrows it — the backdoor must now also be behaviourally invisible on control-plane-chosen inputs — and does not close it |
| **a sibling that rewrites another world's corpus copy at T0** | — | **OPEN at T0, STOPPED at T1** | vector 18d. The corpus copies are control-plane written and re-digested either side of every replay, and the observation is bound to the bytes it loaded — so the attack is detected and attributed rather than silent. It is not *prevented* at T0, because the oracle and the candidate run as the same uid on the same filesystem, which is the same residual `evidence_regime: "isolated"` was refused for. At T1 the per-world corpus is a read-only bind into one container and no sibling can reach it |
| **a cohort member that shrinks the comparison denominator without emptying it** | — | **OPEN** | vector 18c's residual. A member that answers `opaque` on SOME cases removes exactly those cases from every sibling's comparison. Zero-contribution members are excluded and every member's contribution is recorded in the report, so the shrinkage is attributable; nothing refuses it. Refusing it needs a rule about how much of the corpus a comparison must cover, which is a policy parameter M2a does not ship |
| **a candidate that reads the corpus off the filesystem at T0** | — | **OPEN at T0, STOPPED at T1** | vector 19's other half. The corpus lives outside the worlds tree at 0700 and is not present in any world-visible path during phase A, so the generating agent is not DELIVERED the inputs — but at T0 an oracle-time process running as the same uid can still find them by absolute path. `scripts/accept.sh` step 3r2 pins the relative-path half; the absolute-path half is the T0 residual and is not claimed closed |
| **a mutation tool shadowed by a package in the candidate's tree** | — | **DETECTED** | vector 20. `PYTHONSAFEPATH=1` on the enumeration removes the capability on CPython ≥ 3.11; below that, the contradiction check (`mutation_lines_targeted > 0` with `mutants_candidates == 0` is machinery, not a pass) is the only layer, and it catches the demonstrated attack rather than the capability |

## Orchestrator changes

`internal/race` gains one phase before and one after the existing two.

- **Phase 0 — corpus materialization**, after `race.started`, before phase A, only when the policy requires a corpus-consuming oracle. One worktree at `intent.base.commit` through the same backend, tier, image and env as the candidates (the M1e baseline's fixture, reused and, when a baseline is also required, the *same* worktree — one open, two measurements). Materialize → `CAS.Put` the corpus → run the base observation → `CAS.Put` the observation stream → one `corpus.recorded` event carrying both digests → close and remove. Abort as machinery, ledger untouched, on: an unusable base observation, a corpus of zero cases, or a base observation that is not complete over the corpus.
- **Phase A** — unchanged, and the corpus is **not** mounted ([decision 13](#decision-13)).
- **Phase B** — unchanged: per-world ladder, policy order, short-circuit at the first failed hard gate. `corpus-observe` and `mutation-diff` are ordinary world-stage rungs, so a world stopped at O-1 pays for neither.
- **Phase B2 — the cohort barrier.** After phase B joins, assemble the cohort (usable `corpus-observe` observations, sorted by world digest), run the reducer once, and record its N receipts in cohort order. No process executes; nothing is opened; a race with fewer than two cohort members still records its receipts, with `diff_cohort_n` present and every other `diff_*` absent.
- **Decision inputs** are assembled exactly as M1c decision 17 requires — worlds and receipts digest-sorted — so `Decide` remains order-independent and `mvo audit`, which assembles from ledger-scan order, reproduces it byte-for-byte.

`internal/admit` gains gate scope ([decision 21](#decision-21)): race-scope gates are excluded from `landingSpecs`, named on their own output line, and rendered `n/a (race-scope)`.

`mvo audit`'s referenced-set table (M1f decision 22, normative) gains three rows:

| referrer | digests swept |
|---|---|
| `corpus.recorded` | `corpus`, `base_observation`, `stdout`, `stderr` |
| `receipt.recorded` | **`inputs[*]` where the value is a digest** (in addition to `result.artifacts[*]` and `execution.evidence_plugin`) |
| `oracle.skipped` | — (observational, no payload digests) |

## Fixtures

- **`testdata/toyrepo/patches-behave/`** — `patch-p` and `patch-q`, the fixture the whole differential exists to justify. Both implement `clamp` correctly for every input the eight-test suite exercises; `patch-p` uses `min(max(value, low), high)` and `patch-q` uses `min(max(low, value), high)`. They pass every hard gate, tie on every ranking key that measures anything, and **diverge on `clamp(nan, 0, 10)`: `nan` versus `0`.** Verified while writing this document:

  ```
  (nan, 0, 10)  min(max(v,lo),hi) → nan   min(max(lo,v),hi) → 0    DIVERGE
  (5, 0, 10)    → 5     → 5     same
  (-3, 0, 10)   → 0     → 0     same
  (99, 0, 10)   → 10    → 10    same
  ```

  Under M1f this race ESCALATEs on `on_ranking_tie` and tells the maintainer nothing except that two digests tied. Under M2a it ESCALATEs on `on_behavioral_split` and hands them the input and both answers. That difference is the block.
- **`testdata/toyrepo/corpora/clamp-nan.json`** — the declared-corpus fixture: four cases over `stats:clamp` and `stats:mean`, one of which is the NaN case. Zero dependencies, so the acceptance script's differential steps run on a machine with no Hypothesis — which is this one.
- **`testdata/toyrepo/props/mvo_props.py`** — the policy-declared property module, used by `properties.json` and by the harness-set test (vector 16).
- **`testdata/toyrepo/policies/`** — `differential.json` (guard + collect + suite + `corpus-complete@observe`, `corpus-differential`, `on_behavioral_split: 2`, **no differential hard gate** — the DoS-resistant shape of [vector 18](#new-corpus-vectors-testdataadversarialvectors)); `properties.json`; `mutation.json` (`max_mutants: 20`, `mutation-survivors-not-above: 0`); `menu.json` (every rung, for M2b's scheduler to allocate over).

  `differential.json` shipped without the guard for the whole of the block's first integration — no `tree-guard` oracle, no `paths-unmodified` gate, `paths.harness: []` — while `mvo policy validate` printed `paths (frozen against the candidate): harness (modify/delete/create) corpora/clamp-nan.json` over a freeze the policy could not keep, and a candidate that rewrote the corpus passed every hard gate. It now carries the guard, [rule 24](#closed-vocabulary-deltas) refuses the shape at load, and `mvo policy show` prints `paths (declared, NOT ENFORCED — no paths-unmodified gate reads them)` for a hand-authored path block no gate reads. Its `paths.harness` list stays EMPTY on purpose: the only frozen path is the compiled corpus file, so `**/sitecustomize.py` — the mechanism vectors 17, 18 and 19 use to reach the corpus rung — is not frozen out from under them.
- **`testdata/toyrepo/patches-reach/`** — `patch-p` plus a world that goes looking for the pinned corpus by every relative path a candidate would try and FAILS ITSELF if it finds one. It is the assertion whose absence let [decision 13](#decision-13) read as unreachability for a block (`scripts/accept.sh` step 3r2).
- **`testdata/corpus/`** — recorded corpus objects and observation streams for the **pure** reducer and parser tests: `corpus-declared.json`, `obs-agree-a.txt`, `obs-agree-b.txt`, `obs-split.txt`, `obs-opaque.txt`, `obs-undeclared-id.txt`, `obs-no-finish.txt`, `obs-repeat-id.txt`. No Python, no packages, no world.
- **`testdata/mutation/`** — recorded `cosmic-ray` session dumps and `mutmut results` output for the pure adapter parsers, since neither tool is installed here and no test may require one.

## Acceptance script (CI runs this)

`scripts/accept.sh` — M1f's steps kept, eight inserted. All new steps install a `testdata/toyrepo/policies/` file; none changes the shipped default.

- **3r — the corpus is materialized on the base tree and never reaches a generator (new)** — assert a single `corpus.recorded` event precedes every `world.created`; assert its `corpus` digest resolves in CAS and re-digests; assert **no world's captured patch, agent context or transcript contains the corpus digest or any corpus case id**. The negative half is the AG-7/ch.13 rule under live test, in the same shape as M1b's "no policy digest in a prompt" test.
- **3r2 — the corpus is not REACHABLE from a world either (new, [amendment 29](#decision-29))** — race `patches-reach`: a world whose `sitecustomize.py` goes looking for the pinned corpus by every relative path a candidate would try, and fails itself loudly if it finds one. Assert every world passes. This is the assertion whose absence let "materialized under `<raceDir>/corpus/`, never inside a world's tree" read as unreachability while the directory sat as the parent-sibling of every worktree. It pins the relative-path half only: at T0 the absolute-path half is an open vector, not a closed one.
- **3s — the differential escalates on a real split (new)** — `policy use differential`; race `patches-behave`. Assert: both worlds `pass` every hard gate; the decision type is `ESCALATE`; the rationale contains `escalated by policy rule on_behavioral_split`; `mvo explain --json` reports `escalation.rule == "on_behavioral_split"`, two classes, and a `distinguishing[0].case == "c0001"` whose two observations differ; `mvo explain` (human) prints `stats:clamp(nan, 0, 10)` with both returned values; **neither world is rendered as a winner** (no `WINNER`, no ` won `). Then the control assertion that the mechanism has teeth rather than firing on everything: race `patches-rank` under the same policy and assert **`SELECT`** with one behaviour class.
- **3t — a comparison of one is not a comparison (new)** — `intent new --budget-candidates 1`; race under `differential.json`. Assert the comparison receipt exists, `diff_cohort_n == 1`, and **no other `diff_*` key is present in `result.metrics`** — absence, not zero.
- **3u — cohort starvation is self-elimination (new)** — race `patch-p` plus adversarial vector 18. Assert the starving world's `corpus-observe` receipt carries no `corpus_cases_observed`, that it failed `corpus-complete@observe` as its first gate, and that the honest world is not convicted of the sibling's behaviour.
- **3v — mutation, or a named skip (new)** — probe for `cosmic-ray`/`mutmut`; when absent, **skip with the exact reason printed** and assert `mvo race` under `mutation.json` aborts at pre-flight with the decision-20 message and an untouched ledger. When present: race `patches`; assert `mutants_tested <= mutants_budget`, `inputs["diff_target"]` non-empty, `inputs["mutant_selection"] == "control-plane"` under cosmic-ray, the honest fix's `mutants_survived == 0`, and the `mutation-report` artifact lists every survivor with its diff.
- **3w — properties, or a named skip (new)** — same shape for `hypothesis`. When present, assert `properties_*` are stream-derived and that `property_cases_*` are **either** present with `result.tools["hypothesis-observability"] == "stream"` **or** absent with `== "jsonl"` — never present with `jsonl`.
- **3x — the compatibility proof (new)** — `mvo policy show default --builtin --json` re-digests to `mv0:f207c3fad59d0fc973e5f342ac54d8b1bc9e5c6cae2a2cff0b33477ddee3c543`, unchanged; and an M1f-era ledger fixture replays byte-for-byte under the M2a binary (`chain_ok`, `replay_identical`, zero rationale diffs), which is the decision-25 claim under test rather than in prose.
- **3y — `mvo oracles` (new)** — the menu renders every kind with its profile; a kind with fewer than three local receipts prints `no local measurement (n=…)` and its `--json` coefficient is `null`; a kind with more prints a fitted `fixed`/`per-unit` pair. A skipped measurement must never render like a measured one.
- **3u2 — a corpus policy must still be able to LAND something (added during integration, [decision 26](#decision-26))** — race one candidate under `differential.json` in a throwaway clone, then `mvo admit`. Assert the admission succeeds, that it prints `race-scope gates not evaluated at admission: corpus-complete@observe`, and that `mvo verify HEAD` passes. Without this step the fixture policy raced fine and could never land anything, which is exactly how the hole shipped.
- **`scripts/adversarial.sh`** gains vectors 14–19 and a re-recorded `baseline.json`; the diff between the old and new baselines is the proof of what this block closed and, for vectors 17 and 19, of what it did not. Each new vector carries a `vectors/<nn>-<name>.policy` sidecar naming the fixture policy that BUYS the rung it attacks (`policies/{differential,mutation,properties}.json`); vectors 01–13 have no sidecar and keep the shipped default, which is why their rows do not move. A vector whose rung the machine cannot buy records `PREFLIGHT_ABORT` with the refusal sentence and the baseline pins it — installing the tool drifts the baseline and forces a re-record with a real verdict, rather than leaving a green row nobody bought.

## Testing bar

- `internal/oracle` (value + corpus): the `mvo-value/v0` encoder table over every row, including `-0.0` vs `0.0`, `NaN`, both infinities, nested containers, non-`str` dict keys, sets sorted by encoded form, and the `opaque` fallback; fingerprint goldens; the rule that two `opaque` observations are unequal; corpus materialization drop-counting for unrepresentable arguments and unresolvable targets; case-id assignment and immutability.
- `internal/oracle` (observation parser): the usability table over `testdata/corpus/` — undeclared id, repeated id, missing `session_finish`, sequence gap, over-cap, unknown record kind ignored-and-counted — each asserting **absent metrics and a failed gate, never a pass**.
- `internal/oracle` (reducer): purity and permutation invariance over cohort order (a property test, extended from M1c's); partition correctness including all-agree, all-differ, one singleton, and opaque-driven exclusion; `diff_cohort_n < 2` ⇒ every other metric absent; the report artifact's canonical bytes, sort order and 32-case truncation; a fake backend whose `Command` fails, asserting the reducer performs **no** process execution; `evidence_regime == "derived"` and `inputs["evidence_floor"] == "streamed"` as invariants of the kind.
- `internal/oracle` (properties): the observability parser over recorded JSONL; the decision-15 rule under test — metrics present iff the records arrived on the stream, absent under the JSONL fallback, and `result.tools` distinguishing them; a version whose record shape does not parse yields absence, never a fabricated count.
- `internal/oracle` (mutation): `difftarget` over recorded patches, including binary hunks, renames, deletions, new files, and the protected/harness exclusion; canonical mutant ordering and cap application as a pure function; the count-truncation vs clock-truncation table ([decision 17](#decision-17)); the empty-target-set path (`mutants_candidates == 0`, score absent, gate passes vacuously); adapter parsers over `testdata/mutation/` with no tool installed; the failing-baseline `status = error` path.
- `internal/policy`: decode/validate table for rules 16–22 (unknown provider, `declared` without a file, cohort-stage gate without `scope: "race"`, `corpus-differential` with two observers or none, unreachable `on_behavioral_split`, a `corpus.module` that `paths.protected` would swallow, unknown mutation tool or operator); the profile table's totality (a switch test over every kind); `Default()` canonical-bytes golden with the **unchanged** M1f digest pinned; a golden proving an M1f-era `policy/v1` file still decodes, compiles to M1f-identical gates, keys, escalation **and path set**, and yields `scope=both` on every gate.
- `internal/race` (`Decide`/`Trace`): `on_behavioral_split` precedence against all five prior rules and its non-firing when no differential oracle is declared; **byte-for-byte M1f goldens for every rationale form under a policy without a differential oracle** — the compatibility proof, beside M1e's and M1f's; the new gate predicates against {pass, fail, metric absent, receipt absent, unbound receipt, wrong basis, status error}.
- `internal/race` (orchestrator): phase 0's abort paths (empty corpus, unusable base observation, incomplete base observation) leaving zero worlds recorded; the corpus absent from phase A (mount table + prompt/context negative test); phase B2 with 0, 1, 2 and N cohort members; cohort assembly excluding worlds whose observation is unusable; `oracle.skipped` recorded and ignored by replay.
- `internal/admit`: race-scope gates excluded from `landingSpecs`, named in the output, and absent from the attestation's evidence list; M1a/M1e/M1f sentences reproduced byte-for-byte under policies with no scoped gates.
- `internal/object`: `Cost`, `Receipt` golden canonical bytes and digest stability re-derived; `Inputs`/`Correlation` non-nil constructor tests (`{}` not `null`); the `derived` regime covered by the exhaustive switch test.
- `internal/audit`: the three new referenced-set rows, with missing and corrupt injection for each; `inputs[*]` digest values swept and non-digest values ignored.
- `cmd/mvo`: `oracles` human and `--json` goldens for the measured and unmeasured cases; `explain` goldens for a behavioural split, a cohort of one, and a mutation receipt with survivors; `policy validate` messages for rules 16–22.
- **The Python runner** is tested two ways, neither requiring any package: a pure Go test asserting the embedded source's digest matches the recorded constant and that it is written `0444`; and an end-to-end step, skipped with a named reason when a package is absent, asserting a real run's derived metrics equal the fixture's recorded ones.
- `gofmt -l` clean; `go vet ./...` and `go test ./...` pass **with no docker daemon and with none of hypothesis, mutmut or cosmic-ray installed**; `scripts/accept.sh` and `scripts/adversarial.sh` are the e2e tests and run in CI. `go.mod`/`go.sum` unchanged. No test invokes a real agent CLI, pulls an image larger than `python:3.12-alpine`, or requires network.

## Integration amendments (recorded during the block's integration pass)

Two agents built the differential and the property/mutation rungs in parallel against the text above. Integrating them produced one design change and one measurement that contradicts a number in this document; a red-team pass over the result produced six more, every one of which ran rather than being argued. All of them are recorded here rather than fixed silently, because a design document that quietly absorbs its own corrections stops being reviewable.

<a id="decision-26"></a>
26. **Validation rule 23 — a gate over a kind whose INPUTS the race materializes must declare `scope: "race"`, exactly as a cohort-stage gate must.** [Decision 21](#decision-21) was written about cohort-stage kinds and stopped one rung too high. `corpus-observe` is world-stage, so rule 19 let a policy declare `corpus-complete@observe` at the default `both` scope — and the corpus that gate reads is materialized by [phase 0](#orchestrator-changes), which admission does not have. `mvo admit` therefore aborted as machinery (`oracle: corpus-observe: no pinned corpus`) on every landing, forever: **the shipped `differential.json` fixture could race but could never land anything.** It is decision 21's own failure mode — "an admission that can never succeed", the `sealed.json` shape M1f's red team found — rebuilt one rung lower and shipped by both agents without either noticing, because no test in either half ever admitted under a corpus policy.

    The fix is the rule decision 21 should have stated: `policy.RaceStagedInputs(kind)` is true for `corpus-observe`, a gate over such a kind must declare `scope: "race"`, and a `both`/`landing` scope is a named load error. `mvo admit` then prints `race-scope gates not evaluated at admission: corpus-complete@observe` and lands — a weaker landing gate set, made **visible**, which is what decision 21 requires of every scope reduction. It costs no prior policy anything (no pre-M2a policy can declare a corpus) and it is checked at load, not at admission, so the dead end is caught while a policy is being authored rather than after a race has been paid for. `scripts/accept.sh` step 3u2 admits under `differential.json` end to end, which is the assertion whose absence let this ship.

<a id="decision-27"></a>
27. **The plugin-autoload seal is the largest single cost lever on the ladder, and the cost model must be keyed on it.** [The menu](#the-menu) reports pytest's fixed cost as "~400 ms measured". Measured again on the integration machine (Apple M4 Max, CPython 3.12, pytest 8.4.0, the two-file `testdata/toyrepo` in a clean directory, five runs each):

    ```
    python3 -m pytest -q                                    0.46 / 0.46 / 0.45 s
    PYTEST_DISABLE_PLUGIN_AUTOLOAD=1 python3 -m pytest -q    0.10 / 0.10 / 0.11 s
    ```

    **A 4.4× difference, on the same machine, the same tree and the same pytest.** M1f's `evidence.plugin_autoload: "off"` shipped as a *security* control — entry-point plugin autoloading is corpus vector 13's whole surface — and it turns out to dominate the fixed cost of every pytest-based rung. Two consequences for M2b: a scheduler that fits one `fixed` coefficient per kind will fit the average of two populations that differ by a factor of four, so **the fit must be keyed on `(kind, plugin_autoload)`**; and the honest statement of what the seal buys is "it removes an attack surface *and* three quarters of the rung's fixed cost", which is a rare direction for a security control and worth saying out loud. The declared profile is unchanged — it carries no numbers by design. **The original text then said `mvo oracles` "fits from receipts, which already carry the instance whose config digest names the seal", and that was false as written**: `object.Receipt` has no `plugin_autoload` field and `policy.ConfigDigest` hashes only `{args, argv, coverage, kind, reruns, timeout_ms, corpus?, mutation?}`, so `evidence.plugin_autoload` never enters the config digest and never reaches a receipt. The seal is nonetheless recoverable from the ledger without moving a byte of the receipt schema: a receipt names its config digest, and the recorded POLICY that declares that digest carries the seal. `mvo oracles` therefore attributes each receipt to `(kind, plugin_autoload)` through the recorded policies, fits ONE population per seal, records the fitted seal on the coefficient and the per-seal sample counts beside it, and treats a config digest declared by two policies with different seals as UNATTRIBUTED — the same refusal it already applies to a config no recorded policy declares.

### The red-team pass (recorded after the block's first integration)

Six findings below were **executed**, not argued. Each ran end to end on this tree, each
either landed a candidate it should not have or suppressed a signal it should have raised,
and each is recorded here with what it broke, what closed it, and what did not close.
A design document that quietly absorbs its own corrections stops being reviewable, and a
red-team result that outranks the contract has to be written down as outranking it.

<a id="decision-28"></a>
28. **Two corpus providers ship in v0, not three; [decision 5](#decision-5) is corrected.**
    `CorpusProviderSupported` returns true for `declared` and `repo-suite` only.
    `hypothesis` validates cleanly at policy load — `validateCorpus` accepts it and only
    requires a module — and then aborts the race at `preflightCorpus` with "this binary
    cannot materialize it". So the dead end surfaces after a policy is authored and a race
    is attempted, rather than while it is being authored.

    It is recorded as an amendment rather than fixed at load, and the reason is
    `testdata/toyrepo/policies/properties.json`: `corpus.provider: "hypothesis"` there
    declares where the O2p *properties* come from, not a corpus the race materializes, and
    refusing the string at load would refuse that policy too. The honest correction is to
    the text: decision 5's "three providers ship" and the providers table's `hypothesis`
    row describe the seam, not this binary.

<a id="decision-29"></a>
29. **The corpus plane's cross-world surface, in four parts — the largest single correction
    in the block.** [Decision 13](#decision-13), the cohort definition and the two
    "A WORLD CAN ONLY REMOVE ITSELF" comments were all stronger than the code.

    - **The corpus was one shared, mutable file** (vector 18d). Phase 0 wrote it to
      `<raceDir>/corpus/corpus.json` and handed every world that path; it was a sibling of
      every worktree, owned by the candidate's own uid, and `mvo_corpus.py` re-read it from
      disk on every replay. A world could poison the corpus the NEXT world would replay,
      and never its own observation. Both payoffs ran: eliminate the honest sibling by
      declaring an undeclared case id (the victim's stream then convicted the victim), or
      suppress the split by rewriting one argument so both worlds agreed. Now: one copy per
      world, written after phase A joins, re-digested by the control plane before and after
      every replay with a mismatch aborting the RACE as machinery, and the observation bound
      to the bytes it loaded.
    - **`session_start.corpus` was dead.** The runner wrote `corpus.get("digest","")` over
      an object with no `digest` member, so the one field that would have made poisoning
      self-detecting was `""` in every stream in the tree, and nothing on the Go side read
      it. The runner now hashes the bytes it loaded, the pinned digest travels on argv as
      `--corpus-digest`, and `ParseObservation` refuses a mismatch. Fixture:
      `testdata/corpus/obs-corpus-mismatch.txt`.
    - **The corpus was mounted into every candidate world for the whole of phase A**
      under T1. `openOptsFor` set `CorpusDir` unconditionally, the keeper is opened before
      `Adapter.Start` runs the agent inside it (M1c decision 4), and phase 0 has already
      written the corpus by then: `docker inspect` of the candidate keepers showed
      `<raceDir>/corpus -> /mvo/corpus ro=true` on every one of them, mid-generation. That
      is the exact opposite of decision 13, of `backend.OpenOpts`'s own comment, and of the
      M2a verdict on vector 19. The mount is now per world and its directory is EMPTY until
      phase A has joined.
    - **The cohort admitted worlds that had already failed their own gate** (vectors 18b
      and 18c). `cohort()` filtered on parseability alone, so a usable-but-short observation
      — `fail` status, absent-metric gate failure, and still a member — deleted the
      distinguishing case from every sibling through the intersection denominator; and a
      member answering `opaque` everywhere passed its own gate while collapsing the
      denominator to zero for the whole race. The cohort now requires the receipt's `pass`,
      a zero-contribution member is excluded, and every member's contribution is recorded.

    **What is NOT closed, and is now an open vector rather than a claim**: at T0 the corpus
    copies are owned by the uid the candidate's process runs as, so a sibling that walks to
    another world's directory can still write it — detected and attributed, not prevented.
    And a member that contributes SOME comparable cases still shrinks the denominator for
    everyone. Both rows are in the delta table above, in `docs/concepts.md` and in the
    README.

<a id="decision-30"></a>
30. **`mutation-survivors-not-above` reads `mutants_survived + mutants_timeout`.**
    [Decision 17](#decision-17) excludes a per-mutant timeout from `mutation_score_bp`'s
    denominator, which is right — a ratio whose denominator includes mutants nothing
    measured is not a score. What did not follow, and shipped anyway, is that the bucket
    reached no gate at all: `mutation-survivors-not-above` is the only mutation gate M2a
    ships ([decision 10](#decision-10)), it read `mutants_survived` alone, and a mutant the
    suite provably failed to kill therefore cost the candidate nothing.

    Run end to end against a stand-in tool implementing the adapter's declared contract:
    three mutants, two killed, one a 90-second sleep — `mutants_killed=2,
    mutants_survived=0, mutants_timeout=1, mutation_score_bp=10000`, gate PASS, race SELECT,
    and the mutation report listing `survivors: 0` beside a mutant nothing killed. The
    candidate authors both the baseline duration and the mutated code's runtime, so the
    5 × baseline bound is a number it can straddle: this is decision 17's "make the
    expensive part slow" arriving somewhere decision 17 was not looking, because the failure
    is not non-determinism but a bucket that no predicate read.

    The gate now reads both counts and both are required (absent ⇒ fail, as everywhere).
    The report gains a `timeouts` key carrying the mutants with their diffs. The SCORE is
    unchanged: decision 17 is normative about what a ratio means, and it is a different
    question from what the tests did.

<a id="decision-31"></a>
31. **O2p runs the declared property module and nothing else; [decision 14](#decision-14)
    is corrected.** `propertiesPlan` appends `corpus.module` as a positional pytest
    argument, which RESTRICTS collection to that path — so `properties_total` counts test
    nodes in the declared module alone, and counts non-`@given` nodes in it, because it is
    a plain pytest node count. Decision 14 and the O2p prose said "the repository's own
    `@given` tests plus the policy-declared module", while the O2p argv sketch showed the
    positional argument; the two halves of the document disagreed with each other.

    The implementation is what ships, and it is also the defensible half: the harness freeze
    covers exactly the declared module, so "the repository's own property tests" would have
    been an unfrozen source feeding a gate. The prose is corrected rather than the code.

<a id="decision-32"></a>
32. **`Result.Detail` carries four fields, not two.** The §corpus-differential text fixed
    it as `behavior class mv0:aa1… (cohort mv0:7f3…)`; `behaviorDetail` emits the class, the
    cohort, the corpus and the first distinguishing case. The reason is in the code and is
    sound — `Decide` may read neither `Inputs` ([decision 24](#decision-24)) nor CAS, and
    `on_behavioral_split`'s frozen sentence names both extra facts — but the string
    `internal/race.parseBehaviorDetail` parses back was pinned nowhere normative, so a
    hand-edited or older receipt silently failed to fire the rule. The four-field form is
    now the specified one.

<a id="decision-33"></a>
33. **Validation rule 24, and the fixture that proved it was missing.** [Decision
    14](#decision-14)'s defence for the property module is that `corpus.module` compiles
    into `paths.harness`, "closed by the gate that already closes `conftest.py`, at rung
    O-1". Nothing required that gate to exist. Rule 12 forbids the converse — a
    `paths-unmodified` gate with nothing to guard — and there was no rule forbidding paths
    to guard with no gate.

    The shipped `differential.json` was exactly that shape: no tree-guard oracle, no
    `paths-unmodified` gate, `paths.harness: []`. `mvo policy validate` printed
    `paths (frozen against the candidate): harness (modify/delete/create)
    corpora/clamp-nan.json` followed by `OK: policy valid` — a claim the policy could not
    keep — and a candidate under it that rewrote the corpus passed every hard gate. For the
    corpus file the impact is small (the corpus is materialized from the base tree); for a
    property module it is total, since a vacuous `@given` module passes `properties-pass`
    while asserting nothing, which is the whole of what vector 16 claims to close — and
    vector 16 has never executed on this machine.

    Rule 24 is the converse: a compiled harness set that is non-empty *because of* a corpus
    declaration requires a `paths-unmodified` hard gate, named at load. Rule 12 now counts
    those same paths, so a policy whose only frozen path is its corpus satisfies both. Both
    shipped `differential.json` fixtures carry the guard; the adversarial copy keeps an
    empty `paths.harness` deliberately, so vectors 17–19 still reach the corpus rung, and
    `testdata/adversarial/README.md` says why. `mvo policy show` prints
    `paths (declared, NOT ENFORCED — no paths-unmodified gate reads them)` for a
    hand-authored path block no gate reads.

## Explicitly NOT in M2a

The adaptive scheduler itself and any budget allocation in policy (CP-4 — that is M2b, and this document is its input); value-of-information estimation, correlation *learning*, and the sequential admission test (SPRT/e-value family, ch. 3 implication 2); the fail-to-pass reproduction test oracle and the intent/regression corpus split that would give the differential a direction (EP-4's other half, M2c — [decision 7](#decision-7)); **LLM-synthesized properties and corpora**, which need agent spend and arrive through the `declared`/`synthesized` provider seam with no contract change ([decision 5](#decision-5)); the challenger agent (AG-6) and hacker-fixer loops (ch. 8 §8.5); LLM judges, critics and `monitor-verdict` receipts (ch. 8 §8.7, ch. 13); directed fuzzing on the diff (ch. 8 §8.6, rung 7); metamorphic relations as a distinct oracle kind; reference-based differential against a human patch (benchmark/REPAIR modes, ch. 9); JS/TS and Rust kinds (StrykerJS `--incremental`, cargo-mutants `--in-diff`, fast-check, proptest — Phase B); quarantine sets and flake policy beyond M1e's first-run rule (EP-6); per-test coverage contexts and the KP-1 impact map (M3); the `isolated` evidence regime and T2 microVMs (XP-4 — M1f's amendment stands, `auto` still resolves to `streamed` on every tier); a semantic protected-paths gate; policy signing and per-policy trust roots (TP-5); key supply and pinning for CI (still the named M1g prerequisite); deterministic world digests across runs; `mvo doctor`; Windows.
