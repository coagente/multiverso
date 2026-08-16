# M2b1 — The Budgeted Fixed Arm: Design & Contract

> **The one thing that makes the M2 thesis measurable, and it is not a scheduler.** [M2b](M2b-adaptive-scheduler.md) shipped an adaptive allocator, an allocation trace and a comparison harness. It also shipped, in its own BUILDLOG entry, the reason none of its numbers settle anything: **`--schedule=fixed` is the UNBUDGETED exhaustive M1 ladder.** It reads `max_oracle_ms` never. Every "matched budget" figure published so far compares *adaptive under B* against *exhaustive under ∞*, and the budget-truncated fixed numbers beside them are **arithmetic replayed over a recorded ledger, not races that were run**. M2b1 builds the missing arm: a fixed ladder that is given the same money and stops when it runs out.
>
> **The arm, in one line:** depth-first over worlds in a **control-plane-authored order**, buying each world's rungs in policy gate order, charging every purchase to the same pool through the same affordability predicate, stopping on `S-budget` or `S-empty`, and emitting the same `schedule.*` trace so the two arms are comparable field by field. It is not a new loop. It is [M2b §2](M2b-adaptive-scheduler.md)'s loop with the value terms removed and the ordering replaced — **one loop, two selectors** ([decision 1](#decision-1)), because the cheapest way to guarantee two arms share a cost model, an affordability rule, a charge point, a stop vocabulary and a trace shape is to make them literally the same code.
>
> **The intellectual core is [§3](#3-what-a-fair-comparison-requires), not [§2](#2-the-budgeted-fixed-arm).** Sixteen conditions under which a number from this harness means anything, each with how it is enforced — and for five of them, the honest answer is *not enforced, and here is what that costs*. The largest: **the two arms cannot verify the same worlds**, because a world object carries `created_at`, the agent's `RunCost` and a transcript digest, so two runs of the same patch produce different world digests by construction. That is also why [decision 3](#decision-3) refuses world-digest order outright: it is a coin flip over candidate-authored bytes, and M2b measured that coin flip deciding whether the fixed ladder completes the winning world.
>
> **A third arm ships, and it is derived rather than raced** ([§5](#5-the-retrospective-allocation-bound)). PRD §11's arm 7 — the retrospective oracle — needs ground-truth labels and stays in M2d. What is computable today, exactly, from a recorded exhaustive ledger, is the **retrospective allocation bound**: the minimum spend at which *some* prefix-respecting allocation reaches the full-evidence decision. It costs ~15 000 `Decide` calls (≈0.25 s at six worlds) and it converts "adaptive won by two points" into "adaptive won two of the eleven points that were available", which is the difference between a result and a rounding error. Without it a small win is uninterpretable and a null is unfalsifiable.
>
> **Scope discipline, stated up front.** `Decide` gains nothing. `Receipt`, `World`, `Intent`, `Decision` and `PolicyV1` gain nothing — **no policy field, no ranking key, no gate predicate, no metric, no oracle kind.** Every new field is additive on an **observational** `schedule.*` event, ignored by replay, carrying no payload digest. Every M0–M2b ledger replays byte-for-byte; the shipped default policy stays `mv0:f207c3fa…`. Stdlib only; `go.mod`/`go.sum` untouched; no test invokes a real agent CLI (M1b rule); POSIX only.

## Module layout (delta over M2b)

```
internal/schedule/selector.go     the Selector seam: SelectorVOC (M2b's rule,
                                  moved, unchanged) and SelectorLadder (new,
                                  depth-first over a given world order) — PURE
internal/schedule/schedule.go     the loop, parameterized by Selector; the budget,
                                  affordability, charge point and stop clauses stay
                                  where they are and are shared verbatim
internal/schedule/bound.go        the retrospective allocation bound: prefix-closed
                                  enumeration over a recorded receipt set (PURE)
internal/schedule/trace.go        +selector, +budget_basis, +world_order, +rotation
                                  on Started; +order on Considered; +selection_us
                                  on Finished. Observational, additive.
internal/race/schedule.go         ScheduleFixedBudget routed through scheduledVerify;
                                  the control-plane world order handed in, never derived
cmd/mvo/race.go                   --schedule=fixed-budget, --budget-basis,
                                  --world-order-rotation; rule 25 extended to the arm
cmd/mvo/explain_schedule.go       ladder rows render "—" where VOC rows render numbers;
                                  --bound renders §5
scripts/schedule-compare.sh       --replicates, seeded-workspace pairing, the cost-table
                                  equality assertion, the ANECDOTE banner
```

Amended (never rewritten): `internal/schedule` gains one interface and four observational fields; `internal/race` gains one arm branch and one config field; `cmd/mvo` gains three flags and one extended pre-flight refusal; `scripts/accept.sh` gains six steps. **`internal/policy`, `internal/object`, `internal/admit`, `internal/audit` and `Decide` are untouched.**

---

## 1. What is broken, in the code rather than in the abstract

| fact | where | consequence |
|---|---|---|
| `verifyAll` under `ScheduleFixed` calls `r.pool(completed, r.verify)` and returns | [`internal/race/schedule.go`](../../internal/race/schedule.go) | the budget object is never constructed; `max_oracle_ms` is read never |
| that path emits no `schedule.started`, no `schedule.step`, no `schedule.finished` | same | the fixed arm has **no stop reason, no spend total, no considered set** |
| `mvo explain --schedule` returns `Recorded: false` without a trace, and `schedule.Waste` takes the trace as an input | [`cmd/mvo/explain_schedule.go`](../../cmd/mvo/explain_schedule.go) | **evidence waste is uncomputable for the fixed arm** — the headline instrumentation metric exists for one arm only |
| the fixed path runs whole ladders per world under the M1c worker pool | `r.pool` | there is no purchase-level ordering at all, so "what would it have bought first" is not a question the code can answer |
| `world.created_at`, `world.cost` (agent `RunCost`) and `world.trace` are in the digest pre-image | [`internal/object/types.go`](../../internal/object/types.go) | **two runs of the same patch produce different world digests**, so digest order is not stable and cross-arm world identity is impossible |

The BUILDLOG's own summary of the last two rows is the sentence this block exists to retire: *"the fixed ladder's success is luck — it is depth-first in world-digest order, digests are not stable across runs, and whether it completes the winning world is a coin flip."*

---

## 2. The budgeted fixed arm

<a id="decision-1"></a>
> **Decision 1 (load-bearing). One loop, two selectors — the arms differ in ORDERING and in nothing else.**
>
> ```go
> // Pure. No I/O, no clock, no Decide call it does not declare.
> type Selector interface {
>     // Rank orders an already-computed frontier. It may NOT filter it for
>     // any reason but admissibility, and it may not touch the budget.
>     Rank(frontier []Purchase, st State) []Ranked
>     Name() string // "voc" | "ladder"
> }
> ```
>
> The frontier rule ([M2b decision 2](M2b-adaptive-scheduler.md#decision-2)), the affordability predicate, the charge point, the elimination rule, the stop clauses, the batch dispatch, the no-repurchase rule ([M2b decision 10](M2b-adaptive-scheduler.md#decision-10)) and the whole trace stay in `schedule.go` and are executed identically by both arms.
>
> **Why this and not a second loop.** A second loop is the obvious implementation and it silently breaks eight of [§3](#3-what-a-fair-comparison-requires)'s sixteen conditions in one commit — a second affordability rule, a second charge point, a second treatment of unpriced rungs, a second stop vocabulary, a second trace shape. Every one of those differences would show up in the published comparison as *scheduling*. **The comparison's validity is a property of the code layout**, and this is the layout that makes the invalid version hard to write. It is [M2b decision 1](M2b-adaptive-scheduler.md#decision-1)'s argument — the scheduler asks, it does not model — applied one level up: the arms share, they do not each implement.
>
> The test that pins it: under an unbounded budget, on any policy, **the two selectors buy the identical receipt set**; they may differ only in the order of `schedule.step` rows.

<a id="decision-2"></a>
> **Decision 2. A THIRD arm label, `fixed-budget`. `fixed` keeps its meaning exactly.**
>
> `--schedule=fixed` remains the unbudgeted exhaustive M1 ladder on the M1c worker pool, unchanged, untraced. `--schedule=fixed-budget` is the new arm. **The label is not reused**, because `schedule.started.schedule` records the label and not the semantics: a `"fixed"` in an old ledger and a `"fixed"` in a new one would mean different things depending on which binary wrote it, and no reader could tell them apart. That is the honesty rule pointed at our own instrumentation.
>
> The experiment's **reference arm is `--schedule=fixed-budget --budget-oracle-ms 0`**, not `--schedule=fixed`: same evidence set (an unbounded budget buys every rung), but it emits a trace, so evidence waste, spend and the cost-table snapshot are computable for the reference too. Acceptance step 6a proves the equivalence — receipts, decision and rationale byte-identical to `--schedule=fixed` — which is [M2b decision 13](M2b-adaptive-scheduler.md#decision-13)'s null case extended to the new arm.

<a id="decision-3"></a>
> **Decision 3. The world order is CONTROL-PLANE AUTHORED, handed to the scheduler, and recorded. World-digest order is refused.**
>
> `schedule.Config` gains `Order []string` — world digests in the orchestrator's own order, which is **candidate ordinal ascending** (candidate *k* owns slot *k−1*, [`internal/race/race.go`](../../internal/race/race.go) phase A). The ladder selector ranks the frontier by `(order index of the world)`, and the same list becomes the **terminal tie-break of the VOC selector**, replacing `world digest asc`. It is recorded in `schedule.started.world_order`.
>
> **Three reasons digest order is refused, and the first one is dispositive.**
>
> 1. **A world digest is a function of candidate-authored bytes.** `world.tree` is the candidate's own tree. [M1f](M1f-trust-boundary.md)'s rule is absolute: *nothing a scheduler or an arm consumes may be authored by a candidate.* Under a binding budget the verification order decides who gets verified at all, so ordering on the digest hands a candidate a lever on whether its rivals are ever measured — grind the patch until the digest sorts low, get bought first, and let the budget run out on the rest. This is [vector 23](M2b-adaptive-scheduler.md#new-adversarial-vectors)'s starvation with a different mechanism, and no bracket clamp touches it. **M2b's own adaptive tie-break has this defect today**; decision 3 removes it from both arms rather than importing it into a new one.
> 2. **It is not stable, so it cannot be paired.** `world.created_at`, `world.cost` and `world.trace` are in the pre-image, so the digest moves on every run of the identical patch. Digest order is therefore *randomization without a recorded seed* — which AG-3 forbids anywhere in this project, and which makes replicates unpairable: two replicates of one arm differ in their verification order for reasons nothing records.
> 3. **Neutrality is not the property we need.** The objection digest order answers is *"ordinal 1 always goes first, so the fixed arm has a positional advantage"* — which is true and real. Randomizing it makes the advantage invisible instead of absent. What removes it is [decision 6](#decision-6)'s **rotation across replicates**: replicate *r* rotates the order list by *r mod N*, so over *N* replicates every candidate holds the head position exactly once, the advantage becomes a measured variance component instead of a confound, and the whole thing stays deterministic and recorded.
>
> Ordinal order is not innocent either and the residual is named: the generator prompt renders `"candidate k of N"` ([M2b decision 16](M2b-adaptive-scheduler.md#decision-16)), so a candidate knows its ordinal, and under a budgeted depth-first arm knowing your ordinal is knowing whether you will be verified. It cannot *change* its ordinal, so this is a conditioning channel, not a steering one — but it is exactly the protocol-internal knowledge [research ch. 13](../../research/13-ai-control-untrusted-generators.md) shows collapses a protocol's safety, and it joins decision 16's prerequisite list: **before a budgeted arm is used against untrusted generators, the ordinal must leave the prompt.**

<a id="decision-4"></a>
> **Decision 4. A partially verified world is an ordinary world with an unpurchased gate. The arm invents no state for it.**
>
> When the money runs out mid-ladder, the world keeps the prefix it bought, the remaining rungs are never purchased, and each gets one `oracle.skipped {world, oracle, reason: "budget exhausted after N ms"}` — [M2a's purchase law](M2a-oracle-menu.md#the-purchase-law) verbatim: *the scheduler may not mark a rung "skipped, assume fine"; there is no such state and there will not be one.* An unpurchased hard gate leaves a required metric absent, an absent required metric fails the gate, and `Decide` never names a failing world as `Subject`. Nothing in `Decide` learns that a budget existed.
>
> The consequence is that **both arms inherit the same starved-race semantics**, including the same hole: `SELECT` is not monotone in the pass set ([M2b decision 4](M2b-adaptive-scheduler.md#decision-4)), so a starved fixed arm can admit exactly where a starved adaptive arm can, and `on_evidence_incomplete` ([M2b decision 14](M2b-adaptive-scheduler.md#decision-14)) is the decision-layer answer for both. **That is the point.** An arm that handled starvation differently would be comparing a scheduler against a decision rule.
>
> `machineryFailure` is unchanged and still does not fire on a partially verified `COMPLETED` world ([M2b amendment C](M2b-adaptive-scheduler.md#amendment-c)) — so under a policy that does not declare `on_evidence_incomplete`, **both** arms record REJECT for a race that never bought the evidence. The comparison must therefore be run under both configurations ([F14](#3-what-a-fair-comparison-requires)); reporting only the one where the rule is on would flatter both arms in the same direction and hide the shipped default's behaviour.

<a id="decision-5"></a>
> **Decision 5. Same pool, same predicate, same charge point, same overrun — and the overrun is reported per arm.**
>
> `budget.affordable` ([`internal/schedule/budget.go`](../../internal/schedule/budget.go)) is called by both selectors with no change, including its two known weaknesses, which are now **shared** rather than asymmetric: an unpriced (`declared-rank`) purchase is affordable while any pool remains, and the pool is charged *after the fact* from the receipt's own `cost.wall_ms`, so the bound is overrun by up to one batch. Both arms print `BUDGET EXCEEDED by N ms` with the unpriced spend beside it.
>
> **Equal shares are a VOC-arm concept and the ladder selector does not use them.** `share = remaining / |contenders|` exists to make elimination release budget ([M2b decision 8](M2b-adaptive-scheduler.md#decision-8)); a depth-first arm has one contender at a time by construction, so its affordability test is `cost ≤ remaining` — which is what `affordable` already computes when `contenders == 1`. No second predicate. The [starved-admission residual](M2b-adaptive-scheduler.md#residual-starved-admission) applies to both arms and is not closed here.
>
> <a id="decision-5b"></a>
> **Decision 5b — `--budget-basis={actual|predicted}`, because actual wall-clock is not in the determinism tuple.** Under `actual` (the default, and the honest one) the pool is charged the receipt's measured `wall_ms`, and two replicates at one budget can buy different things because the machine was busier. Under `predicted` the pool is charged the **pinned cost table's prediction** for that purchase, which puts spend back inside [M2b decision 13](M2b-adaptive-scheduler.md#decision-13)'s determinism tuple: given `(policy, worlds, receipts, cost table, budget, constants, order)` the allocation is a pure function, both arms are exactly replayable, and **every difference between the arms is allocation rather than jitter**. Its cost is stated rather than hidden: it measures allocation under a *model* of cost, and the model's error is already recorded as the calibration residual. `predicted` refuses at pre-flight when any buyable kind is unpriced in this workspace, naming the kinds — a basis under which half the rungs are free is not a basis. **Both arms must run the same basis; the harness asserts it.**

<a id="decision-6"></a>
> **Decision 6. The arm reports itself in the same fields, and the fields it does not compute are ABSENT — never zero.**
>
> The ladder selector computes no `flip`, no `discount_bp`, no `executor_bp`, no `value_bp`, no `score_bpps`. It does not call `Decide` for lookahead at all. Under the "absent source implies absent metric" rule those columns are **not** reported as zeros a reader could aggregate:
>
> - `schedule.started.selector` records `"ladder"` (`""` ⇒ `"voc"`, exact rather than assumed: no pre-M2b1 binary could record a trace for any arm but the adaptive one).
> - A ladder row carries `order` — its depth-first rank — and is **required by test** to carry `flip=0, flip_outcomes=[], discount_bp=0, executor_bp=0, value_bp=0, score_bpps=0, score_rank=0`. The invariant is asserted, not hoped for, and the fields stay serialized because M1b decision 5 forbids `omitempty` games.
> - `mvo explain --schedule` renders `—` in those columns for a ladder race and prints `selector: ladder (depth-first, world order recorded)` in the header. A reader who sees a `0` under FLIP is looking at a VOC row that scored zero, which is a different fact.
>
> **Stop clauses:** the ladder arm can emit `S-empty` (every alive world's ladder complete) and `S-budget` (the starved stop). It **cannot** emit `S-frontier` (no flip test) or `S-ranking` (it does not stop on ranking completeness). That is documented as arm-specific rather than treated as a defect; `mvo explain` prints `stop: S-budget` for both arms with the same meaning, which is the field the comparison actually reads.
>
> **`decision_now` is still recorded** — one `Decide` call per batch, ~16 µs, the single most useful field for a reader reconstructing a race. Recording an observation does not make it a selector: it enters no ranking, and the invariant test above is what proves it.
>
> **`selection_us`** is added to `schedule.finished`: the arm's own metalevel time, measured. It is **reported, not charged** ([F8](#3-what-a-fair-comparison-requires)).

<a id="decision-7"></a>
> **Decision 7. Depth-first and parallel dispatch are incompatible, and the canonical comparison runs at k = 1.**
>
> A world's rung *k+1* needs rung *k*'s result, so a strictly depth-first arm has exactly one dispatchable purchase at a time. At `--parallel k > 1` the arm fills the batch from the next `k` worlds in order — **depth-first priority fill**, which is no longer pure depth-first: money is committed to world 3's rung while world 1's next rung is pending, which is the truncation behaviour the arm exists to avoid. So:
>
> - The published comparison runs **both arms at `--parallel 1`**, recorded in `schedule.started.parallel`.
> - At `k > 1` the arm still runs, still records `k`, and `mvo explain` labels it `depth-first priority fill (k=%d)`. Results at different `k` are **never pooled**, and the harness refuses to compare two arms whose recorded `parallel` differs.
>
> This is not a limitation of the arm; it is a property of a binding budget. Both arms commit money before seeing results at `k > 1` (ASHA's staleness, already recorded), and the effect is larger for the depth-first arm because its purchases are strictly dependent.

<a id="decision-8"></a>
> **Decision 8. Validation rule 25 extends to any arm that can withhold, and `fixed-budget` REFUSES rather than falling back.**
>
> `wall_ms_asc` sums a world's counted receipts, so the world verified least wins the tiebreak — wrong-signed under *any* budgeted arm, not only the adaptive one ([M2b decision 15](M2b-adaptive-scheduler.md#decision-15)). The pre-flight refusal's predicate is unchanged (an allocation-sensitive key **and** evidence no hard gate backs); only its arm test widens.
>
> Where `scheduleArm` silently falls back to the exhaustive ladder for an adaptive race, `fixed-budget` **errors**. A silent fallback from a budgeted arm to an unbudgeted one turns a matched-budget experiment into an unmatched one and records nothing that says so — which is precisely the failure this whole block exists to correct.

---

## 3. What a fair comparison requires

This is the section a reviewer attacks first. Sixteen conditions. **Five are not enforced, and each says what that costs.** Nothing here is a promise about future work: an unmet condition is a limitation of every number this harness produces until it is met.

| # | condition | enforcement, or the honest gap |
|---|---|---|
| <a id="f1"></a>**F1** | **Same policy, pinned by digest.** Both arms evaluate the identical gate lattice, ranking and escalation set. | **ENFORCED.** `mvo race --policy <digest>`; the harness asserts `decision.policy` equal across arms and aborts otherwise. A policy pinned by *name* resolves per workspace and is refused by the harness. |
| <a id="f2"></a>**F2** | **Same candidates.** Both arms verify the same trees, and ordinal *k* means the same patch in both. | **PARTIALLY ENFORCED, and this is the largest gap.** With the script adapter (committed patches) phase A is deterministic, so `world.tree` per ordinal is identical and the harness asserts it. **It is unenforceable with a live agent adapter**, and unenforceable *in principle* today: a world binds `created_at`, the agent `RunCost` and a transcript digest, so no two runs produce the same world object, and there is no mode that re-verifies a recorded world set. **Cost:** with real agents the arms differ by generation variance plus allocation, confounded, and no per-instance difference is attributable. **What would close it:** a `race --reverify <world-digests>` path that runs phase B over recorded worlds — a contract change (M2d/M3), named here rather than assumed. |
| <a id="f3"></a>**F3** | **Same base state and same cost table.** Affordability is priced from a workspace-local Theil–Sen/median-fixed fit over that workspace's ledger; two workspaces with different history price differently. | **ENFORCED, mechanically.** One workspace is seeded (`mvo init` + warm-up races), the intents are created, and the directory is then **copied** per arm per replicate, so the ledgers that feed `costSamples()` are byte-identical. The harness asserts `schedule.started.cost_table` **byte-equal across arms** and aborts on any difference. This assertion is the whole reason `cost_table` is a recorded snapshot ([M2b decision 13](M2b-adaptive-scheduler.md#decision-13)). |
| <a id="f4"></a>**F4** | **Same budget, same unit, same basis.** | **ENFORCED.** The arm is a `mvo race` flag and the budget is an intent field, so both arms consume **one intent digest** created before the workspace copy. Asserted equal from `schedule.started.budget.max_oracle_ms` and `.budget_basis` ([decision 5b](#decision-5b)). |
| <a id="f5"></a>**F5** | **Same affordability predicate and charge point**, including the fail-open treatment of unpriced rungs and the post-hoc charge. | **ENFORCED by construction** ([decision 1](#decision-1), [decision 5](#decision-5)): one `budget` object, one `affordable`, one `Record`. A test asserts both selectors reach them through the same call path. |
| <a id="f6"></a>**F6** | **Same tie-breaking in ALLOCATION.** Under a binding budget the order decides who is measured. | **ENFORCED** ([decision 3](#decision-3)): both arms rank on the recorded control-plane `world_order`; the VOC arm's terminal `world digest asc` tie-break is removed. |
| <a id="f7"></a>**F7** | **Same tie-breaking in the DECISION.** `world_digest_asc` is the implicit terminal ranking key of every policy, and digests differ between arms by construction ([F2](#f2)). | **NOT ENFORCEABLE. Detected and quarantined instead.** A race whose winner was chosen at the terminal key was chosen by a coin flip that lands differently in each arm, for reasons that are not the treatment. The harness reads `mvo explain`'s ranking walk, labels such races `decided-at-world-digest`, **reports them in a separate bucket and excludes them from the paired decision comparison**. Fixtures used for published comparisons declare `on_ranking_tie`, so a tie escalates identically in both arms instead of resolving arbitrarily. **Cost if ignored:** a pure-noise disagreement rate between arms, indistinguishable from a scheduling effect. |
| <a id="f8"></a>**F8** | **Same cost-accounting boundary**, and an honest statement of what PRD §11's budget this actually is. | **ENFORCED between the arms; UNMET against PRD §11**, per the table below. |
| <a id="f9"></a>**F9** | **Same dispatch degree**, and the same wall-clock bound. | **ENFORCED.** `parallel` asserted equal and canonically 1 ([decision 7](#decision-7)). `max_wall_ms` must be 0 or generous for both: a depth-first arm at `k=1` takes far longer in wall-clock than a parallel adaptive arm, so a shared clock bound truncates one arm for a reason unrelated to the treatment. Asserted from the shared intent. |
| <a id="f10"></a>**F10** | **Same host conditions**, since the budget's unit is wall-clock milliseconds. | **PARTIALLY ENFORCED.** Arms are interleaved within a replicate (A,B,A,B — never all-A-then-all-B, which confounds arm with machine drift), and each replicate records a **host-load probe**: one fixed control-plane rung timed immediately before each arm, reported as `probe_ms`. Replicates whose two probes differ by more than a declared tolerance (default 20%) are reported and excluded. **Removed entirely, not merely bounded, by `--budget-basis=predicted`** ([decision 5b](#decision-5b)), which is why that basis exists. |
| <a id="f11"></a>**F11** | **Same seal, tier and toolchain.** [M2a amendment 27](M2a-oracle-menu.md#decision-27) measured pytest plugin autoload as a **4.4× lever on fixed cost** — a bigger effect than any scheduling difference this harness can measure. | **ENFORCED, from three different places, because they live in three different places.** The seal is a policy field and is asserted equal from the recorded cost-table rows' `plugin_autoload`. The tier and image are `mvo race` flags (`--exec`, `--exec-image`), passed identically by the harness and asserted from `world.isolation_tier` and `world.env` across arms. The toolchain probe is the same binary on the same host ([F15](#f15)). |
| <a id="f12"></a>**F12** | **Same replication and pairing**, with variance reported. | **ENFORCED** — [§4](#4-replication-and-variance). |
| <a id="f13"></a>**F13** | **Same instrumentation available to both arms**, so no metric exists for one arm only. | **ENFORCED by [decision 2](#decision-2)** (the reference arm is `fixed-budget --budget-oracle-ms 0`, which traces). Evidence waste, spend, considered/bought/declined counts and the stop clause now exist for every arm. Waste's definition is unchanged ([M2b decision 18](M2b-adaptive-scheduler.md#decision-18)) and its bracket bounds come from the same control-plane clamps in both arms. |
| <a id="f14"></a>**F14** | **Same escalation configuration**, including `on_evidence_incomplete`, which turns a measured 4-of-4 admission into a 4-of-4 escalation on the same fixture and budget. | **ENFORCED as equality across arms** (one pinned policy). **Not resolvable as a single configuration:** the rule ships **off** in the default, so the published comparison runs the full protocol **twice**, once under a policy declaring it and once without, and reports both. Reporting only the "on" configuration would describe a product nobody runs. |
| <a id="f15"></a>**F15** | **Same binary.** Both arms' `Decide`, oracles, cost model and clamps are one build. | **ENFORCED.** The harness builds one binary and uses it for both arms; the build digest is recorded in the comparison report. |
| <a id="f16"></a>**F16** | **A headroom denominator.** A difference between arms is uninterpretable without knowing how much difference was available. | **ENFORCED** — [§5](#5-the-retrospective-allocation-bound). Instances where the full-evidence decision is unreachable at budget *B* by *any* allocation are reported separately: neither arm could have won them, and pooling them dilutes both directions. |

### PRD §11's budget, and which quarter of it this harness charges

PRD §11 defines the matched budget as **tokens + runner time + oracle cost + selection cost**. This harness charges **one term, and not all of it**.

| PRD §11 term | charged? | where it actually is |
|---|---|---|
| **tokens** (agent spend) | **NO** | `world.cost` — `tokens_in/out`, `usd_micro`, with `source: "client-estimate" \| "none"`. Under the script adapter, which is what every fixture and every test uses, it is structurally **zero**: generation cost here is not merely uncharged, it is unmeasured. |
| **runner time** (world creation, generation wall, container open/teardown) | **NO** | `world.cost.wall_ms` and the `world.*` ledger events. Not additive into any pool. |
| **oracle cost** | **PARTIALLY** | `max_oracle_ms` charges Σ `cost.wall_ms` over **scheduled phase-B rungs only**. Uncharged: phase 0 corpus materialization (amortized, control-plane), **phase B2's cohort reducer** (explicitly charged to no budget, [M2b §10](M2b-adaptive-scheduler.md#10-orchestrator-changes) — reported under "not scheduled" with its spend), and EP-3's admission re-verification. |
| **selection cost** | **NO — measured and reported** | `schedule.finished.selection_us`. Measured at 0.07–0.3% of the purchase it prices ([M2b §2.1](M2b-adaptive-scheduler.md#21-state-and-why-it-is-free-to-hold)); charging it would change no decision at that magnitude, and the field exists so the claim is re-checkable rather than inherited from a benchmark run once. |

**Therefore:** no figure from this harness may be labelled *budget-matched* in PRD §11's sense. The correct label is **oracle-budget-matched**, and it must appear on every plot and in every caption. Between *these two* arms the omission is symmetric — both consume the same generated worlds and the same phase 0/B2 — so generation cost is a constant that cancels in the **difference**. It does **not** cancel in any ratio, in *cost per true-correct merge*, or in any comparison against PRD §11's arms 1–5, whose budgets are dominated by the term this harness does not charge.

---

## 4. Replication and variance

M2b observed the same budget producing different decisions run to run. The cause is named: **actual wall-clock is not in the determinism tuple**, and the pool is charged actual milliseconds, so jitter changes what the next purchase can afford. Both arms have it; a single run at a budget level is an anecdote in both.

<a id="decision-9"></a>
> **Decision 9. R = 10 paired replicates per (fixture, budget level, policy configuration), arms interleaved, and no comparison verdict is printed below R = 3.**
>
> **Pairing, concretely.** Replicate *r* gets its own copy of the one seeded workspace; both arms in replicate *r* share the intent digest, the cost table, the host-load probe window and the **order rotation** *ρ(r) = r mod N* ([decision 3](#decision-3)). The two arms run adjacent in time. `schedule.started.rotation` records *ρ*.
>
> **Why 10, honestly.** It is a *screening* count, not a power calculation. At R = 10 a clean 10-of-10 sweep supports an exact one-sided 95% lower bound of about 0.69 — so R = 10 can justify "≥69% of runs", and nothing sharper. Its job is (a) to detect harness nondeterminism at all and (b) to **size the noise floor** a per-instance difference must clear. The paper's power comes from PRD §11's ≥300 instances per stratum, where at fixed compute more instances beat more replicates: **M2d runs R ≥ 3 per instance per arm** and uses the noise floor measured here as the threshold below which a per-instance difference is reported as a tie.
>
> **What is reported, per arm per level** — every one of these, or the number is not published:
>
> | quantity | form |
> |---|---|
> | decision | histogram over {SELECT, REJECT, ESCALATE} with the escalation rule named, n = R |
> | spend, receipts bought, complete worlds, waste | median + IQR + min/max, never a mean alone |
> | stop clause | histogram (`S-budget` vs `S-empty` is the arm's own account of whether the money bound) |
> | budget overrun | count of replicates with `spent > max_oracle_ms`, and the max overrun |
> | **self-disagreement** | fraction of replicates whose decision ≠ that arm's modal decision — **the noise floor** |
> | paired disagreement | the full 3×3 arm-A × arm-B decision table, and McNemar's exact test only once M2d supplies labels |
> | quarantined | counts for `decided-at-world-digest` ([F7](#f7)) and host-probe outliers ([F10](#f10)) |
>
> **A difference between arms that does not exceed both arms' self-disagreement rate is reported as a tie**, in the text and in the abstract, not only in a footnote.
>
> **The anecdote rule is enforced by the harness, not by discipline.** `scripts/schedule-compare.sh` prints its comparison verdict only at `--replicates >= 3`; below that it prints the arms side by side under an `ANECDOTE (R=1): no verdict` banner and exits non-zero in `--strict` mode. Every number M2b's own BUILDLOG quotes was produced at R = 1 and is, by this rule, an anecdote — which is the correct retrospective verdict on it.

**Three budget levels, defined on the informative band rather than on a round fraction.** PRD §11 requires three levels per instance. A fixed fraction of the exhaustive spend is uninformative on some instances (nothing was decidable) and trivial on others (everything was). The reference arm already measures the exhaustive spend *S*, and [§5](#5-the-retrospective-allocation-bound) measures `minspend` — the least money at which *any* allocation reaches the full-evidence decision — from the same run. So:

| level | budget | what a result at this level means |
|---|---|---|
| **B1** | `ceil(minspend × (1 + m))`, default margin *m* = 10%, recorded | the tightest budget where winning is possible at all. An arm that reaches the full-evidence decision here allocated near-optimally |
| **B2** | `(B1 + S) / 2` | the middle of the informative band |
| **B3** | `S` | the null case: both arms should match the reference, and an arm that does not has a bug, not a result |

The margin exists because `minspend` is computed from the reference run's *actual* milliseconds while the arms spend their own; under `--budget-basis=predicted` the margin is 0 and B1 is exact.

---

## 5. The retrospective allocation bound

**In scope, and it is nearly free.** PRD §11's arm 7 is the retrospective oracle: *the best achievable decision given all evidence.* Its full form — best given **ground truth** — needs labels and stays in M2d. But the question this block has to answer is narrower and is answerable exactly, today, from data already on disk:

> **Given everything the exhaustive ladder bought, what is the cheapest allocation of that same evidence that reaches the same decision — and is it reachable at budget *B* at all?**

<a id="decision-10"></a>
> **Decision 10. The retrospective allocation bound is a PURE, DERIVED function of a recorded exhaustive ledger. It records nothing, invalidates nothing, and can be improved forever.**
>
> Inputs: the reference race's pinned policy `pol`, its worlds `W`, its full receipt set `R*` with each receipt's recorded `cost.wall_ms`, and its recorded decision `d* = Decide(pol, W, R*)`.
>
> **Feasible allocations** are the **prefix-closed** subsets `S ⊆ R*`: per world, a prefix of the policy's gate order. That is exactly the constraint both arms operate under ([M2b decision 2](M2b-adaptive-scheduler.md#decision-2)), which is what keeps the bound tight and fair — it bounds *allocators*, not oracles.
>
> ```
> cost(S)      = Σ cost.wall_ms over S            -- counterfactual: the reference run's measurements
> reachable(B) = ∃ S feasible : cost(S) ≤ B ∧ Decide(pol,W,S) ≡ d*     -- ≡ on Type and Subject
> minspend     = min { cost(S) : Decide(pol,W,S) ≡ d* }
> ```
>
> **Cost.** `Π_w (L_w + 1)` subsets, where `L_w` is the number of rungs world *w* actually bought. Six worlds × four rungs = 5⁶ = 15 625 subsets × 15.8 µs ≈ **0.25 s**. Six worlds × seven rungs = 8⁶ ≈ 262 000 ≈ **4 s per instance**, which a 300-instance stratum absorbs. Above a declared cap (10⁶ subsets) the bound **refuses and says so** — no approximation is reported under the same name as an exact bound.
>
> **What it buys.** Three things a paper cannot do without:
> 1. **A denominator.** "Adaptive reached the full-evidence decision on 61% of instances at B1, fixed on 52%, and the bound says 68% were reachable" turns a 9-point gap into 9 of the 16 available points. Without it, 9 points is a number with no scale.
> 2. **An exclusion criterion.** Instances with `minspend > B` are unwinnable by any allocator; pooling them dilutes a real effect and manufactures a null.
> 3. **A regret metric that needs no labels.** Per arm per instance: reached `d*` or not, and spend over `minspend`.
>
> **What it is NOT, said plainly.** It is not PRD §11 arm 7. It bounds *allocation of a fixed evidence set*, not selection and not generation: if every candidate is wrong, `d*` is wrong and the bound is a bound on reaching a wrong answer efficiently. Only M2d's three-tier labels turn it into an upper bound on **decision quality**. The name says so — `allocation bound`, never `oracle`.
>
> **Three caveats, recorded with it.** (i) Costs are counterfactual, taken from one run's measurements; under `--budget-basis=predicted` they are the pinned model's instead and the bound is exact in the arms' own currency. (ii) It assumes a world's rung-*k* receipt does not depend on what other worlds bought — true for world-stage oracles, **false for the cohort-stage reducer**, so v0 computes the bound over **world-stage rungs only** and treats phase-B2 receipts as present in every subset, which is what the unscheduled barrier actually does in both arms. The reducer is a pure function of the cohort's observations, so an exact extension is possible and is named, not hand-waved. (iii) Flake: `R*` is one draw; a flaky rung makes the bound a bound on that draw. No repurchase means we cannot do better ([M2b decision 10](M2b-adaptive-scheduler.md#decision-10)).
>
> Surfaced as `mvo explain <intent> --schedule --bound` (off by default; it costs a quarter-second) and as a `bound` block in `--json`. **Derived, not recorded** — [M1e decision 21](M1e-policy-oracles.md)'s discipline: improving the definition never invalidates a race.

---

## 6. Wire deltas, acceptance, testing bar

```go
// schedule.Started — FOUR additive fields, observational, ignored by replay.
// Absent on a pre-M2b1 trace ⇒ selector "voc" (exact: no earlier binary
// could record a trace for any other arm), budget_basis "actual",
// world_order unknown — rendered as "unknown (pre-M2b1 trace)", never as
// digest order, because inventing a past ordering is inventing evidence.
type Started struct {
    // … M2b fields unchanged …
    BudgetBasis string   `json:"budget_basis"` // "actual" | "predicted"
    Rotation    int      `json:"rotation"`     // the replicate's order rotation
    Selector    string   `json:"selector"`     // "voc" | "ladder"
    WorldOrder  []string `json:"world_order"`  // CONTROL-PLANE order, world digests
}

type Considered struct {
    // … M2b fields unchanged …
    Order int `json:"order"` // depth-first rank; 0 on a VOC row
}

type Finished struct {
    // … M2b fields unchanged …
    SelectionUS int64 `json:"selection_us"` // the arm's own metalevel time: REPORTED, not charged
}
```

**Arm constant:** `ScheduleFixedBudget = "fixed-budget"`. **No other wire change anywhere.** No receipt field, no policy field, no ranking key, no gate predicate, no metric, no oracle kind, no escalation rule, no `Decide` input.

**`scripts/accept.sh`**, six steps; none changes the shipped default.

- **6a — the new null case.** `--schedule=fixed-budget --budget-oracle-ms 0` against `--schedule=fixed`: receipt set, decision and rationale byte-identical. The reference arm is the M1 ladder plus a trace, and this is the proof.
- **6b — the budget binds and the partial world is honest.** At a binding budget: stop is `S-budget`, ≥1 world holds a strict ladder prefix, every unbought rung has an `oracle.skipped` naming budget exhaustion, no rung is marked passed, and the decision is REJECT/ESCALATE per [F14](#f14)'s two policy configurations — with the control asserting the *same* race under the other configuration.
- **6c — the arms are comparable.** Paired run: `cost_table`, `budget.max_oracle_ms`, `budget_basis`, `parallel`, `world_order` and the policy digest byte-equal across arms; the harness aborts if any differs.
- **6d — absent is absent.** No ladder row carries a non-zero VOC field (property test over generated traces); `mvo explain --schedule` renders `—` and never `0` for a ladder race; a VOC row scoring 0 still renders `0`.
- **6e — the bound.** On the reference ledger: `minspend ≤ S`; the cheapest sufficient allocation re-decides `d*` under `Decide`; two computations agree byte-for-byte; the enumeration cap refuses rather than approximating.
- **6f — replication.** `--replicates 3` prints dispersion and a verdict; `--replicates 1` prints the `ANECDOTE` banner and no verdict.

Plus, unchanged in force: an M2b-era ledger replays byte-for-byte under the M2b1 binary, `mvo audit` is OK over a ledger carrying the new fields, and the shipped default policy digest does not move.

**Testing bar.**

- `internal/schedule` (selector): the ladder order over a hand-built frontier; rotation totality over `N` (every candidate holds the head exactly once in `N` replicates); the **equivalence test** — under an unbounded budget, on generated policies and worlds, the two selectors buy the identical receipt set and differ only in step order.
- `internal/schedule` (budget): both selectors reach one `affordable` and one `Record` (call-path assertion); `predicted` basis refuses when a buyable kind is unpriced, naming it.
- `internal/schedule` (bound): exhaustive agreement against a brute-force reference on a 2×2 case; prefix-closure as a property; determinism; the cap refusal; a case where `minspend < S` strictly, so the bound is shown to measure something.
- `internal/race`: `fixed-budget` errors rather than falling back under rule 25 ([decision 8](#decision-8)); the world order handed in is the orchestrator's, never derived from a digest (a test that permutes digests and asserts the order is unchanged).
- `cmd/mvo`: `explain --schedule` human and `--json` goldens for a ladder race, a starved ladder race, and a `--bound` block; the `—` rendering.
- `gofmt -l` clean; `go vet ./...` and `go test ./...` green with no docker daemon and with none of hypothesis, mutmut or cosmic-ray installed; `go.mod`/`go.sum` unchanged; no test invokes a real agent CLI.

---

## Explicitly NOT in M2b1

Re-verifying a **recorded** world set without regenerating it ([F2](#f2)'s close, and the single largest remaining threat to any cross-arm number — a contract change, M2d/M3); PRD §11's arms 1–5 (single agent, best-of-N random / public-tests / LLM-judge, test-voting) and arm 7 proper (the ground-truth retrospective oracle — **needs M2d's three-tier labels**); charging tokens, runner time or selection cost, i.e. PRD §11's budget as actually defined; scheduling the cohort reducer or charging phase B2; per-world spend accounts and the deadline dispatch that would close the [starved-admission residual](M2b-adaptive-scheduler.md#residual-starved-admission); any change to `Decide`, to a policy field, to a ranking key or to a receipt; removing the generator prompt's `"candidate k of N"` ([decision 3](#decision-3)'s named prerequisite, still M2c/M2d's); any sequential statistical test; the eval harness, the benchmark strata, the hidden-oracle discipline and the paper (**M2d**).
