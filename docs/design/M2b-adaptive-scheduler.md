# M2b — The Adaptive Scheduler: Design & Contracts

> This is CP-4, and it is the project's central research claim: **under a fixed total budget, does allocating compute adaptively admit changes with higher true correctness and lower false-admission rate than a single agent, a fixed best-of-N, or a test-only selector?** M0 through M2a exist to make that question measurable. [M2a](M2a-oracle-menu.md) built the menu — seven kinds spanning three orders of magnitude of cost, each declaring what it costs, what it scales by, what signal it reads and who authored its inputs. M2b is the function from a budget to a purchase.
>
> **The scoping decision is stated before the rule, because it bounds what the rule can claim.** CP-4's action set is {generate, mutate-candidate, run-oracle(o, w), pairwise-compare, stop+admit, stop+escalate}. **v0 schedules verification purchases only.** Generation and mutation need agent spend to exercise and labelled outcomes to price, and we have neither until [M2d](#explicitly-not-in-m2b)'s eval harness; scheduling them on a fixed prior would be inventing the number the paper is supposed to measure. That is a real narrowing of the claim and [§1](#1-what-v0-schedules-and-what-it-does-not) says exactly how much: v0 can show that adaptive *ordering* of verification beats fixed ordering **under a binding budget**, and it cannot show the generation-versus-verification trade at all.
>
> **The rule, in one line, and argued in [§2](#2-the-allocation-rule):** among the purchases each still-alive world is next entitled to make, buy the one maximizing `flip × correlation-discount × executor-weight / predicted-cost`, where `flip` is not an estimate but a **three-outcome bracketing lookahead over the pure decision function** — construct the bracketing synthetic outcomes of the prospective purchase, call `Decide` on each, and ask whether any of them changes the decision. Measured on this tree: `Decide` over six worlds costs **15.8 µs** ([`internal/race/decide_bench_test.go`](../../internal/race/decide_bench_test.go)), against a `pytest-suite` rung's 100–400 ms. **The metalevel is four orders of magnitude cheaper than the object level**, which is the precondition [Hay et al.](../../research/03-adaptive-verification-scheduling.md) require for metareasoning to be worth doing, and it is a precondition we hold only because `Decide` is pure and total. Purity was sold as a replay property; it turns out to be what makes the scheduler computable at all.
>
> **Stopping is not a statistical test, and [§3](#3-sequential-stopping) says why rather than shipping numerology.** Fishtest's GSPRT is the best real-world precedent for evidence-native admission and we have **none of its five assumptions**. What replaces type-I error control is structural: the **withholding monotonicity** of the gate lattice ([decision 4](#decision-4)) proves `FAR(adaptive) ≤ FAR(exhaustive)` for any policy and any candidate set. Adaptivity provably cannot cause a false admission. It can cause a false *rejection*, and that is the risk M2d must price.
>
> **The instrumentation is the deliverable** ([§4](#4-the-allocation-trace)). Every action considered, its value, its cost, what was bought and why, recorded as `schedule.step` ledger events — recorded evidence, never recomputed at audit time. PRD §11's `evidence waste` becomes computable for the first time — and its obvious definition turns out to be **broken by our own purchase law**, which [decision 18](#decision-18) demonstrates and then fixes by reusing the allocator's own bracket at audit time.
>
> **The scheduler is itself an attack surface** ([§6](#6-the-scheduler-as-attack-surface)) and [research ch. 13](../../research/13-ai-control-untrusted-generators.md)'s central finding transfers intact: adaptive attacks that exploit knowledge of protocol internals collapsed a resampling protocol's safety from 96% to 17%. AG-7 holds and gains cases. Three vectors are new and two of them are findings **against M2a**: `cost.units` is a candidate-authored number that M2a designed as a scheduler input, and `wall_ms_asc` becomes actively wrong-signed the moment allocation is adaptive — *the candidate we verified least wins the tiebreak*.
>
> Implements **CP-4** and **CP-2**'s budget half; consumes **EP-2**'s cost/correlation metadata to its intent; extends **AG-7** to a surface that did not exist when AG-7 was written. Grounded in [ch. 3](../../research/03-adaptive-verification-scheduling.md) (all four literatures, one rule picked from them) and [ch. 13](../../research/13-ai-control-untrusted-generators.md). Builds on [M0](M0.md)–[M2a](M2a-oracle-menu.md); **every prior contract stays in force unless amended here**, and three M2a amendments are recorded in [§7](#7-amendments-to-m2a).
>
> **Stdlib only; `go.mod`/`go.sum` untouched.** No test invokes a real agent CLI (M1b rule). POSIX only.
>
> **Compatibility, stated up front and proved by the acceptance script: under an unbounded budget and the shipped default policy the adaptive scheduler buys exactly what the M1 fixed ladder bought, in an order that decides identically** ([decision 13](#decision-13)) — the shipped default digest `mv0:f207c3fad59d0fc973e5f342ac54d8b1bc9e5c6cae2a2cff0b33477ddee3c543` does not move, every M0–M2a ledger replays byte-for-byte, and no receipt schema changes at all. **M2b adds no field to `Receipt`.**

## Module layout (delta over M2a)

```
internal/schedule/schedule.go     the loop: frontier, batch, dispatch, stop. Holds a
                                  DecideFn — NEVER a copy of the decision rule
internal/schedule/value.go        the flip lookahead (PURE): bracket construction,
                                  control-plane clamping, the three Decide calls
internal/schedule/cost.go         the cost table: Theil-Sen fit, unit authority + clamp,
                                  declared-rank fallback, affordability
internal/schedule/correlate.go    prior classes, the redundancy tiers, the discount (pure)
internal/schedule/budget.go       per-world shares, release-on-elimination, the ledger
internal/schedule/trace.go        allocation-trace rows -> canonical event payloads (pure)
internal/schedule/waste.go        evidence waste: bracket substitution + greedy-minimal (pure)
internal/policy/vocab.go          +on_evidence_incomplete, +allocation-sensitive key table
internal/race/schedule.go         phase B driven by the scheduler (phase B2 stays a barrier)
internal/race/decide_bench_test.go  the metalevel-cost measurement this design rests on
cmd/mvo/explain_schedule.go       mvo explain --schedule (RENDERS rows; recomputes nothing)
testdata/toyrepo/policies/schedule.json   the linear scheduled ladder + on_evidence_incomplete
testdata/adversarial/vectors/     22-24: three new vectors, one per new surface
```

Amended (never rewritten): `internal/object` gains one additive `Budget` field; `internal/policy` gains one escalation field, one validation rule and the allocation-sensitivity table; `internal/race` gains a scheduled phase B; `internal/audit` gains two observational event rows; `cmd/mvo/{explain,oracles,race,intent}.go`; `scripts/accept.sh` gains seven steps; `scripts/adversarial.sh` gains three vectors and a re-recorded baseline. **`Decide` gains nothing but one escalation rule that is a pure predicate over inputs it already holds.**

---

## 1. What v0 schedules, and what it does not

CP-4 names six actions. Each one is placed, with the reason, and the two that are refused are refused in writing rather than by omission.

| CP-4 action | v0 | why |
|---|---|---|
| `run-oracle(o, w)` | **IN** | The only action whose cost is measured (M2a `cost.units`/`cost.unit`, `mvo oracles`), whose effect on the decision is computable (`Decide` is pure), and whose price does not require a model of agent behaviour. |
| `pairwise-compare` | **HALF IN — the observations are scheduled, the reducer is NOT** | M2a's cross-candidate differential *is* the comparison primitive ([ch. 3 §3.4](../../research/03-adaptive-verification-scheduling.md)'s S\* distinguishing-inputs action, at repository scale), and the per-world `corpus-observe` purchases that feed it are scheduled rungs like any other. **The reducer itself is not scheduled in v0**: it runs as phase B2's unscheduled barrier, is charged to no budget, and no `schedule.step` names it ([§2.2](#22-the-frontier-an-asha-backbone-that-owns-termination-voc-that-owns-order-only), corrected). Closing the cohort is therefore not yet a recorded choice, and starving `observe` collapses the cohort silently. |
| `stop` | **IN, as ONE action** | The scheduler decides **when to stop buying**. It does **not** choose between admit and escalate — that is `Decide`'s job, and a scheduler that chose it would be deciding. This is the single most important line in the block: the two CP-4 stop actions collapse into one because the separation is load-bearing. |
| `generate` | **OUT** | Needs agent spend. Its value is `P(candidate N+1 beats the incumbent)`, which needs a labelled outcome stream that does not exist until M2d. **And it is not merely unpriced: it is unsafe today**, because the generator prompt carries `"You are candidate k of N"` ([`internal/agent/prompt_test.go`](../../internal/agent/prompt_test.go)) and under adaptive generation `k` and `N` *become scheduler state* — see [decision 16](#decision-16). |
| `mutate-candidate` | **OUT** | Same two reasons, plus a third: a mutated candidate is a new world with a new tree, so AG-4 capture, world digests and freshness all apply — that is orchestrator work, not allocation work, and bundling it here would hide a real cost behind a research claim. |
| `stop+admit` / adversarial challenge (AG-6) | **OUT** | AG-6 is [explicitly not in M2a](M2a-oracle-menu.md#explicitly-not-in-m2a) and remains not here. Ch. 3's open question — "no budget-allocation paper treats *attack the leading candidate* as a schedulable action; its VOC is negative for the candidate but positive for the decision" — is still open and we are not closing it on a prior. |

### What this costs the claim, said plainly

The paper's question is whether allocating between **generation, testing and adversarial challenge** wins. v0 allocates within testing only. Three sub-claims survive and are worth measuring; the fourth does not and is not claimed.

1. **Under a binding budget, adaptive ordering admits more true-correct changes than fixed ordering.** Real, measurable at PRD §11's three budget levels, and the mechanism is concrete: the last affordable purchases go to worlds that can still change the decision instead of to worlds already eliminated or already decided.
2. **Evidence waste falls.** Measurable for the first time, because [§4](#4-the-allocation-trace) makes it computable.
3. **FAR does not rise.** Not measured — *proved*, by [decision 4](#decision-4). This is the strongest result in the block and it is structural.
4. ~~Adaptive generation-vs-verification allocation beats a static split.~~ **Not claimed. Not measurable here.** [When To Solve, When To Verify](https://arxiv.org/abs/2504.01005) supplies the static curve and ZEBRA the pre-run phase allocation; the online joint version remains unpublished and remains ours to attempt, in M2c/M2d, with the prerequisites named.

### And the honest null case, which the acceptance script pins

**On the shipped default policy with an unbounded budget, the adaptive scheduler and the M1 fixed ladder buy the same receipts and decide identically.** Four gates over three oracles is one linear ladder per world; each world's frontier holds exactly one purchase; the worlds are symmetric, so every score ties and the tie-break falls through to world digest. The scheduler does nothing, visibly and by construction. What makes it differ is exactly three things — a **binding** budget, worlds standing at **different** rungs, or a policy with **more than one decision-relevant rung** at the same gate level — and `testdata/toyrepo/policies/schedule.json` is the fixture that supplies the third. Shipping a scheduler that cannot be shown to be inert where it should be inert is how a research result becomes an artifact of its harness.

---

## 2. The allocation rule

### 2.1 State, and why it is free to hold

At every step the scheduler holds the pinned policy `pol`, the fixed world set `W`, the receipts bought so far `R`, and the remaining budget. From those it derives, at a cost measured below:

- `d = Decide(pol, W, R)` — the decision **as if we stopped now**, and
- `T = Trace(pol, W, R)` — the per-candidate gate table, the counted receipts, the key values.

Both are pure, total and order-independent (M1e). The scheduler may call them on **partial** evidence as often as it likes with no side effects and no ledger writes. It follows that the scheduler never needs a model of what a receipt *means*: it asks the decision function.

> **Decision 1 (load-bearing).** <a id="decision-1"></a> **The scheduler holds a reference to the decision rule, never a copy.** `internal/schedule` takes `type DecideFn func(policy.Policy, []object.RecordedWorld, []object.RecordedReceipt) object.Decision` as a parameter and does not import `internal/race`. Two reasons, and the second is the one that matters. *Mechanically*, it breaks the import cycle that would otherwise force the decision logic into a shared package. *Structurally*, it makes a second, divergent copy of the decision rule **impossible to write** — a scheduler with its own idea of what passes is a scheduler that will eventually allocate against a decision nobody made, and the failure would be silent because the recorded decision would still be correct. The scheduler asks; it does not model.

**Measured, on this tree** (`go test ./internal/race -bench BenchmarkDecideLookahead`, Apple M4 Max, three runs at n=3000; the file is in the tree so the number is re-derivable rather than asserted):

| worlds × receipts | `Decide` |
|---|---|
| 2 × 3 | 6.7 µs |
| 6 × 3 | **15.8 µs** |
| 12 × 3 | 31.6 µs |

A step's lookahead is `|frontier| × 3` calls (three bracket outcomes, [§2.3](#23-the-flip-lookahead)). At six worlds that is 18 calls ≈ **0.28 ms**, against a `pytest-suite` rung's 100 ms sealed / 400 ms unsealed ([M2a amendment 27](M2a-oracle-menu.md#decision-27)). **The metalevel costs between 0.07% and 0.3% of the purchase it prices.** Every subsequent design choice in this section is affordable because of that ratio, and if a future kind makes it false the honest response is a cheaper lookahead, not a fabricated estimator.

### 2.2 The frontier: an ASHA backbone that owns termination, VOC that owns order only

[Ch. 3 implication 1](../../research/03-adaptive-verification-scheduling.md) is explicit, and it is adopted deliberately rather than rediscovered: *ship an ASHA-style asynchronous fidelity ladder as the robust backbone, with VOC estimates reordering the queue rather than owning termination.* Hay et al.'s counterexample — an "optimal" metalevel policy that never stops — is the reason.

<a id="decision-2"></a>
> **Decision 2. The policy's gate order is the fidelity ladder and the scheduler may not reorder it within a world.** For each world, its **frontier purchase** is its next unpurchased rung *in policy gate order*. A world may not buy rung k+1 before rung k. This preserves M1e decision 12's short-circuit verbatim (a world that fails a cheap gate never pays for an expensive one), preserves the property that recorded gate order equals evaluation order, and confines adaptivity to the two questions that are actually adaptive: **which world advances next**, and **whether a world advances at all**.

The frontier is therefore at most one purchase per alive world, plus the cohort reducer when its dependency is satisfied. A world is **alive** iff its outcome is `COMPLETED` and no evaluated hard gate has failed; an eliminated world has no frontier purchase and its budget share is released ([§2.7](#27-budget-shares-and-the-starvation-boundary)).

**The cohort reducer is NOT scheduled in v0, and this paragraph is the correction.** The design specified `corpus-differential` as a dependent purchase entering the frontier once ≥ 2 worlds hold a passing `corpus-observe` receipt, with the choice **closing the cohort** and the membership recorded at close time. **None of that shipped.** `Ladder()` enumerates `pol.Required`, which [`internal/policy/compile.go`](../../internal/policy/compile.go) builds from gate oracles, ranking-key oracles, invariant roles and `require_evidence` — a reducer named only by `on_behavioral_split` is not in it — and the reducer instead runs in **phase B2, an unscheduled barrier after phase B, in both arms**. Measured on the differential policy: the recorded receipts include `corpus-differential:1` while the trace's chosen set holds 7 purchases and its considered set never contains the kind at all; its wall time is charged to no budget. The consequence is not only bookkeeping — starving `observe` silently collapses the cohort (`diff_cohort_n = 1` in a measured race) and disarms `on_behavioral_split`, with no trace row showing that anything was decided. Until it is scheduled, `mvo explain --schedule` prints B2's receipts under a **"not scheduled"** heading with their spend, so the trace stops implying a completeness it does not have. [§1](#1-what-v0-schedules-and-what-it-does-not)'s `pairwise-compare` row and [§10](#10-orchestrator-changes) are corrected to match.

### 2.3 The flip lookahead

The corpus points at "expected improvement in decision quality per unit cost". The improvement term is where every VOC design either does real work or fabricates a prior. Ours does real work, and it does less than the name suggests:

<a id="decision-3"></a>
> **Decision 3. `flip` is a logical reachability test over the pure decision function, not a probability.** For a prospective purchase `(w, o)`, construct a small closed set of **bracket outcomes** — synthetic receipts spanning what the purchase could report — and call `Decide` once per outcome. `flip(w,o) = 1` iff some bracket outcome changes `Decide(pol, W, R)` in `Type` or `Subject`; otherwise `0`.
>
> **We do not estimate `P(the candidate is correct)` and we do not pretend to.** That number requires labelled outcomes and there are none until M2d ([§8](#8-what-the-scheduler-cannot-know-yet)). What v0 measures is **decision-relevance**: could this purchase matter at all? A purchase that cannot change the decision under *any* outcome is not bought, and that is not a heuristic — it is a fact about the pinned policy, computed exactly.

**The bracket, per kind, three outcomes:**

| outcome | shape | purpose |
|---|---|---|
| `fail-closed` | `status = error`, **metrics absent** | The M1f/M1e floor: an absent required metric fails every gate reading it. Reaches every elimination the purchase could cause. |
| `pass-min` | `status = pass`, every gate-read metric exactly at its threshold, every ranking-read metric at its **worst** value consistent with passing | The marginal survivor: does this world survive without overtaking anyone? |
| `pass-max` | `status = pass`, every ranking-read metric at its **best control-plane-bounded** value | The maximal challenger: could this world overtake the incumbent? |

Three receipts, three `Decide` calls, per frontier purchase — **and each pass outcome carries more than one receipt, which this table understated.** A bracket of exactly one receipt makes every rung but the last decision-inert (buying `guard` alone leaves the world failing `collect` and `suite` for want of a receipt, so `Decide` returns what it already returned), and a scheduler that refuses every zero-valued purchase then buys **nothing at all** — we ran that version and it stopped at step 1 with an empty receipt set on every race. So a pass outcome **completes the world's whole remaining ladder at the same extreme**: the question a frontier purchase must answer is not *"does this receipt move the decision?"* but *"could a world that pays for this rung reach a different decision?"*. `fail-closed` completes nothing, because a world that fails a gate climbs no further. `Bracket` may therefore return 1, 2 or 3 outcomes — `pass-min` and `pass-max` stand or fall independently — so "three `Decide` calls" is an upper bound, not a constant. A rung whose only consumer reads metric PRESENCE rather than a threshold (`require_evidence`) has its kind's declared metric vocabulary seeded too, because a synthetic receipt with an empty metric map is not evidence an honest run could have produced.

<a id="decision-3b"></a>
> **Decision 3b — and this is a security decision before it is an accuracy decision. Every bound in the bracket is a control-plane-held number; where none exists, the metric is UNKNOWN and `flip` is 1.**
>
> `pass-max`'s ceiling for `tests_passed` is **`collected_base`** — the base-tree collect measurement from M1e decision 13, produced before any candidate existed — never the candidate's own `collected_total`. Its ceiling for `corpus_cases_observed` is the pinned corpus object's `cases_total`. Its ceiling for `mutants_killed` is `mutants_budget`, the policy's own ceiling. The incumbent's key values are clamped by the **same** bounds when they are compared against the bracket.
>
> Without the clamp there is a clean starvation attack, and it is [vector 23](#new-adversarial-vectors): a candidate reports `tests_passed = 500` on an eight-test repository, becomes an unreachable incumbent, and every rival's `pass-max` then fails to overtake it — `flip = 0` on every rival, the scheduler stops buying, and the honest rivals are rejected for want of evidence they were never sold. M1f's `collect-equals-suite-total` invariant does **not** save us: [M1f decision 10's recorded limit](M1f-trust-boundary.md) is that a *coherent* forgery authors both numbers. Clamping in the scheduler costs nothing, touches `Decide` not at all — `Decide` still sees 500 and still decides whatever it decides — and refuses to let an unbounded self-report steer the budget.
>
> The generalization is the design principle of the whole block: **`Decide` fails closed, the scheduler fails open.** An absent metric fails a gate; an unknown bound makes a purchase look valuable and gets it bought. Two layers, two opposite fail directions, each safe in its own layer.
>
> **AMENDED after the red team, and the amendment is the price of the clamp.** *"Clamping in the scheduler costs nothing"* was false, and the counterexample is the honest candidate that ADDS tests. `pass-max` bounds a rival's `tests_passed` at `collected_base`, so a candidate carrying two new regression tests can never be seen to overtake an incumbent that is already at the base count: it ties, loses `world_digest_asc`, scores `flip = 0`, is declined, and — because the declined rung was hard-gated — never enters the pass set at all. Measured at **unbounded** budget, which [decision 13](#decision-13) says reproduces the M1 exhaustive ladder: adaptive bought 5 of 6 receipts and admitted the cheat in 3 of 6 races, against 4 of 4 SELECTs of the honest fix under `--schedule=fixed`.
>
> **The bound stays where it is.** Raising the ceiling to the world's own `collect` count is the obvious repair and it re-opens [vector 23](#new-adversarial-vectors) verbatim, because [M1f decision 10](M1f-trust-boundary.md) is explicit that a *coherent* forgery authors `collected_total` and `tests_passed` together. What changes instead is what a `flip` of 0 is allowed to DO ([decision 3c](#decision-3c)): the clamp may make a purchase look worthless, and it may not make a hard gate unbuyable.

<a id="decision-3c"></a>
> **Decision 3c — admissibility is not the score, and an ordering term may never refuse a hard gate.**
>
> ```
> admissible(w,o)  =  flip(w,o) == 1  OR  o is read by a HARD GATE and w is alive
> BUY              =  admissible AND affordable          -- value_bp only ORDERS the queue
> ```
>
> Three separate red-team findings collapse into this one line, and each of them ran:
>
> 1. **The near-duplicate discount refused a gate.** `value_bp = flip × discount_bp × executor_bp / 10 000` is 0 whenever `discount_bp` is 0, which two hard-gated instances of one kind reach by construction (same signal, same corpus). The rung was never bought, its gate failed for want of a receipt, and the adaptive arm recorded REJECT where the fixed ladder recorded SELECT. That is a correlation heuristic owning termination, which is exactly what [§2.2](#22-the-frontier-an-asha-backbone-that-owns-termination-voc-that-owns-order-only)'s ASHA argument exists to prevent.
> 2. **The control-plane clamp refused a gate** ([decision 3b](#decision-3b), above).
> 3. **A withheld hard gate is not a safe withholding at all** ([decision 4](#decision-4), amended below).
>
> The flip test survives intact — it is still computed, still recorded, still what orders the queue and still the research signal M2d correlates — but a rung a hard gate reads is bought on any live world the budget can pay for. The cost is stated plainly: **under a policy whose every rung is hard-gated, the adaptive arm now buys exactly what the fixed ladder buys whenever the budget allows**, and the remaining adaptivity is ORDER plus the ungated rungs (ranking-only, `require_evidence`-only, research). That is a narrower claim than the one this document opened with, and it is the claim the safety argument can actually carry.

### 2.4 The correlation discount

This is where [ch. 3 §3.5](../../research/03-adaptive-verification-scheduling.md)'s named white space is entered: *"nobody discounts marginal evidence value by correlation in the allocation loop."* M2a made it computable by declaring `Correlation{signal, corpus, generator, executor}` per kind and recording it per receipt. M2b spends it.

**Prior classes.** `correlation.generator` is a wire label; the discount needs the *authorship prior* behind it, because M2a's own rule 2 is about shared authorship ("ten tests written by one team are not ten either") while `control-plane` denotes a mechanical function of the tree. The mapping is a table, not a heuristic:

| `correlation.generator` | corpus provider | prior class |
|---|---|---|
| `control-plane` | — | `mechanical` |
| `base-tree` | `declared` | `mechanical` (the operator wrote the cases) |
| `base-tree` | `repo-suite`, `hypothesis` | `repo-authored` (derived from the repo's own tests) |
| `repo`, `repo+policy` | — | `repo-authored` |
| `model:<family>` | (the synthesis seam, M2a decision 5) | `model:<family>` |

Two `mechanical` priors **never** share; two `repo-authored` priors always do; two `model` priors share iff the family matches. That reproduces M2a's own enumeration of independent pairs exactly — `tree-guard × everything`, `mutation-diff × pytest-suite`, `corpus-differential × pytest-suite` — which is the check that the mapping is a reading of M2a rather than a new opinion.

**Redundancy tiers**, integer basis points, applied between a prospective purchase `o` and each receipt already bought **for the same world**:

| tier | condition | `red_bp` |
|---|---|---|
| near-duplicate | same `signal` **and** same `corpus` | 10 000 |
| same signal | same `signal`, different corpus | 7 000 |
| same prior | same prior class | 5 000 |
| independent | neither | 0 |

<a id="decision-5"></a>
> **Decision 5. The discount takes the MAX over already-bought receipts, not the product, and it prices SUBSTITUTES only.**
>
> `discount_bp(w,o) = 10 000 − max_{r ∈ bought(w)} red_bp(o, r)`.
>
> **Max, not product**, because M2a's rule is a *dominance* rule ("independent only when signal AND generator both differ") and the families are equivalence-class shaped. A product would punish a third `repo-authored` purchase twice for one shared prior, driving the discount to zero through arithmetic rather than through evidence.
>
> **Substitutes only.** Redundancy is evaluated between two purchases only when neither is a dependency of the other. `corpus-differential` shares `signal` *and* `corpus` with `corpus-observe` and would score `red_bp = 10 000` — discount zero, never bought, the differential extinguished by the machinery that exists to price it. It is a **complement**: it consumes the observation rather than duplicating it, and it is exempted by name in the table with that reason recorded. One exemption, one line, reviewed once — M1e decision 6's discipline, not a mini-language.

**Worked, on `menu.json`:** buying `pytest-suite` on a world that already holds `pytest-collect` — same prior class `repo-authored`, different signal (`test-outcomes` vs `test-identity`) — gives `red_bp = 5 000`, `discount_bp = 5 000`. Buying `mutation-diff` beside `pytest-suite` — `suite-adequacy` vs `test-outcomes`, `mechanical` vs `repo-authored` — gives `red_bp = 0`, `discount_bp = 10 000`. Buying `hypothesis-properties` beside `corpus-observe` — both `value-behavior`, different corpus — gives 7 000, so `discount_bp = 3 000`. The rule does something, it does the thing M2a said it should, and it is arguable line by line.

### 2.5 The executor weight, which is the block's one honest constant

<a id="decision-6"></a>
> **Decision 6. `executor_bp = 10 000` for `control-plane`, `5 000` for `candidate-process`, and the 5 000 is HAND-SET.** M2a's discount rule 4: *under M1f's residual, any number a candidate's own process reported is forgeable by a competent in-process adversary; buying a third candidate-process oracle buys independence from honest error, never from that adversary.* A scheduler minimizing false admissions should therefore prefer a control-plane-executed purchase per unit cost, and `tree-guard` and `corpus-differential` are the only two on the menu.
>
> **What the constant should be, nobody knows.** It is not calibrated, it is not fitted, and calling it 0.5 is a statement that a candidate-process receipt is worth half a control-plane one, for which we have no evidence whatsoever. **What replaces it, named:** M2d's adversarial arm sweeps `executor_bp ∈ {0, 2 500, 5 000, 7 500, 10 000}` at matched budget and reports FAR-adv per setting; the value that minimizes FAR-adv without costing TCAR becomes the default and *then* becomes a policy field. Until it is measured it stays a compiled constant recorded in `schedule.started`, because a knob nobody has calibrated must not be a policy field — a policy field invites an operator to tune it before anyone knows what it does, and CP-5's "the pinned policy determines the gate" should not quietly become "the pinned policy determines the heuristics too."

### 2.6 Cost, and who is allowed to author it

`ĉost_ms(w,o) = fixed(kind, seal) + per_unit(kind, seal) × units(w,o)`, from `mvo oracles`' fit keyed on `(kind, evidence.plugin_autoload)` — [M2a amendment 27](M2a-oracle-menu.md#decision-27)'s finding that the seal is a 4.4× lever on fixed cost, so a single-population fit averages two populations that differ by four.

<a id="decision-7"></a>
> **Decision 7. Predicted `units` come from control-plane denominators, the fit is robust, and a fit that does not exist prints no number.** Three parts, and part (a) is a correction to M2a.
>
> **(a) Unit authority.** M2a decision 22 gave receipts `cost.units` so `wall_ms` becomes learnable, and never asked who authors the unit count. For `pytest-suite` it is `tests_total` — a **streamed** metric, candidate-authorable. A candidate reporting `tests_total = 5 000` against `wall_ms = 400` flattens the per-unit coefficient for **every future race in the workspace** ([vector 22](#new-adversarial-vectors)). `OracleProfile` therefore gains a declared `UnitAuthority` — `control-plane` for `tree-guard`'s `paths_examined`, the corpus object's `cases_total` and `mutation-diff`'s control-plane mutant selection; `candidate` for the pytest kinds' counts — and **`candidate`-authority units are clamped at fit time to the race's own `baseline.recorded` `collected_total`**, which is a base-tree measurement the candidate did not author. No receipt field moves: the authority is declared per kind, where the rest of the profile already lives.
>
> **(b) Robust fitting.** `mvo oracles` fits by least squares, which one outlier drags arbitrarily far. It becomes **Theil–Sen** (the median of pairwise slopes), which tolerates a minority of poisoned samples. Honest limit, stated: at the `minSamples = 3` floor a median of three pairwise slopes survives exactly one bad point. That is weak. It is also strictly better than a mean, and the clamp in (a) is the layer that actually removes the capability.
>
> **(c) No fit, no number.** Below `minSamples` there is no coefficient, and v0 does **not** invent one. It falls back to the **declared ordinal rank** of `OracleProfile.Dominant` — `tree-walk` < `hashing` < `interpreter-start` < `case-replay` < `suite-run` < `suite-run-per-mutant` — uses rank position as a relative cost, and records `cost_basis: "declared-rank"` on the trace row. A millisecond figure nobody measured must never appear beside one somebody did; this is M2a's own `no local measurement (n=…)` rule, extended from the report to the allocator. Declared-rank rows are ordered in their own pass and carry `score_rank` rather than `score_bpps` ([§2.8](#28-the-score-in-integers-with-deterministic-ties)), because a rank position and a millisecond are not comparable quantities.
>
> **(d) A COLUMN OF POINTS IS NOT A LINE, BUT IT IS A MEASUREMENT — amended, and the amendment is why the budget ever binds.** Theil–Sen returns nothing unless some pair of samples has distinct `units`. For `pytest-collect` and `pytest-suite` the unit is the repository's test count, which is **constant within a workspace** — and (a)'s clamp pins any bigger past sample down to the current race's `collected_base`, removing what little variance a growing suite would have supplied. The two kinds whose cost dominates every race were therefore priced `declared-rank` *forever* on a single-repo workspace: `mvo oracles` reported `no local measurement (n=9, units do not vary: every receipt scaled 8)`, [§4.4](#44-mvo-explain---schedule)'s rendered `402 ms + 3.1 ms/test` was unreachable in practice, and since an unpriced purchase is affordable while any pool remains, **`max_oracle_ms` did not bind at all**. Refusing a fixed cost we have timed nine times because we could not time a slope is the honesty rule pointed backwards. So: when a population of ≥ `minSamples` has no unit variance, v0 fits **fixed = median(`wall_ms`), per-unit ABSENT**, records `estimator: "median-fixed"`, and prints `N ms fixed, no per-<unit> coefficient (units do not vary)`. The zero in `per_unit_micro_ms` is never rendered as a measured zero, and the honest expectation is stated here rather than discovered: **a single-repo workspace will normally have no per-unit coefficient for the pytest kinds.**

The trace records predicted and the receipt records actual, so `cost_error_ms = actual − predicted` falls out for free — which is exactly the calibration residual M2d needs to know whether the cost model is worth anything, at the cost of zero new recorded bytes. **Both sides of that subtraction cover the PRICED rows only.** A declared-rank purchase has no prediction, so summing predictions over the fitted rows and actuals over every receipt divided a partial sum by a total one and printed `residual: predicted 2 ms, actual 2073 ms (+103550.0%)` — a number that measures nothing. `mvo explain --schedule` reports the unpriced spend on its own line, and flags `BUDGET EXCEEDED by N ms` whenever `spent > max_oracle_ms`, so a budget-matched experiment cannot silently compare unequal spends.

### 2.7 Budget shares and the starvation boundary

The budget is `intent.budget.max_oracle_ms`, additive across parallel purchases, **`0` ⇒ unbounded ⇒ M1 semantics** ([decision 12](#decision-12)). It is not `max_wall_ms`, which bounds the race's clock and is not additive under parallelism.

<a id="decision-8"></a>
> **Decision 8. Each alive world holds an equal share of the remaining budget; an eliminated world's share returns to the pool.** `share = remaining_oracle_ms / |alive|`, recomputed whenever a world is eliminated. A purchase is affordable iff its predicted cost fits within its world's current share *and* the race's remaining budget.
>
> Equal shares look like the opposite of adaptivity, and the recomputation is what makes them adaptive: eliminating four of six worlds triples the survivors' shares. Adaptivity here is *elimination releasing budget*, which is precisely successive halving's semantics, rather than unequal initial allocation — and the reason is the attack. Unequal shares are a better allocator and they reintroduce cross-world starvation: a world granted a large share because it looked promising can burn it and take the race's budget with it.
>
> <a id="residual-starved-admission"></a>
> **The containment claim was WRONG AS WRITTEN, and this is its correction.** *"A world that burns its budget starves only itself"* is false of the shipped code and the red team demonstrated it. There is no per-world spend account anywhere: `Record` charges one global pool and `share()` divides the **shared** remainder by the contender count, so one world's wall time is subtracted from every sibling's share. Worse, a purchase with **no fit** is affordable while any pool remains (the fail-open direction, above), so on a fresh workspace — where by construction nothing has been measured — the bound does not bind at all. Measured against the **shipped default policy**: a conformity cheat that sleeps 3 s inside its own last rung spent **4 963 ms of a 2 000 ms budget**, its rival's suite was refused with `0 ms remain`, and the race SELECTed and admitted the cheat. The attacker pays nothing when it loses the digest ordering: the race simply completes.
>
> **What was done, and what was NOT.** The per-world cap (`worldSpent + cost <= share`) is the obvious repair and it was implemented, measured and reverted before the red team ever arrived — [`internal/schedule/budget.go`](../../internal/schedule/budget.go) records the numbers: at matched budget it turned a SELECT into an ESCALATE by refusing a 33 ms rung the remaining 38 ms could afford, which is [decision 4](#decision-4)'s false rejection manufactured by the allocator itself. That trade has not improved, so v0 does **not** re-introduce it. What v0 does instead is (i) price the pytest kinds for real ([decision 7d](#decision-7)), which is what makes `max_oracle_ms` bind on any workspace with history, (ii) order priced purchases ahead of unpriced ones ([§2.8](#28-the-score-in-integers-with-deterministic-ties)), and (iii) **report the breach**: `mvo explain --schedule` prints `BUDGET EXCEEDED by N ms` with the unpriced spend beside it. Re-measured after those three: 3 of 4 races now ESCALATE where 4 of 6 previously admitted the burner, and **1 of 4 still admits it on a fresh workspace**.
>
> **Named residual, OPEN: a starved admission.** Under declared-rank pricing the share does not bind, and a burn starves siblings. The decision-layer answer exists and works — `on_evidence_incomplete` ([decision 14](#decision-14)) turns exactly this race into an ESCALATE, measured 4 of 4 — but it ships off, so the shipped default remains exposed. What would close it at the allocator: the **deadline dispatch** §6 already promises and the code does not implement (run an unpriced rung under a timeout equal to its world's remaining share, so it self-eliminates through an errored receipt rather than being refused before it runs). That needs `internal/oracle`'s timeout path to produce an errored RECEIPT rather than an error return, which is a contract change and not an affordability fix. Recorded as an open vector in [§6](#6-the-scheduler-as-attack-surface), in `docs/concepts.md` and in the README's gate-coverage table, because a residual an operator cannot read about is a residual we are hiding.

### 2.8 The score, in integers, with deterministic ties

DP-1 forbids floats in canonical JSON and the trace is canonical JSON, so the score is integer basis points throughout — the `coverage_bp` / `mutation_score_bp` discipline, reused:

```
value_bp(w,o)   = flip(w,o) × discount_bp(w,o) × executor_bp(o) / 10 000        ∈ [0, 10 000]
score_bpps(w,o) = value_bp(w,o) × 1 000 / max(ĉost_ms(w,o), 1)                  -- bp per second
```

**Buy** every ADMISSIBLE ([decision 3c](#decision-3c)) affordable frontier purchase, in `argmax score` order, ties broken by `(ĉost_ms asc, world digest asc, oracle name asc)` — total, deterministic, replayable. `value_bp == 0` is a statement about ORDER, never about admissibility: a decision-inert rung nothing reads scores 0 *and* is inadmissible, and those are two facts that happened to coincide in the first draft.

**Two score fields, because there are two units.** `score_bpps` exists only for a row priced from a fit. A row priced at [decision 7c](#decision-7)'s declared ordinal rank records `score_rank` instead and is ordered in a **separate pass**, after the priced rows. One argmax over both was an argmax over milliseconds and rank positions under a single name — a fitted `guard` at 1 ms scoring 10 000 000 against an unfitted `mutation-diff` at rank 6 scoring 833 333 is not a comparison — and M2d would have aggregated the mixed field across kinds and races without ever seeing the seam. Priced rows going first is also what makes the bound bind: an unpriced purchase is affordable while any pool remains ([§2.7](#27-budget-shares-and-the-starvation-boundary)), so spending the pool on unpriced rungs first is precisely how it is overrun.

**Parallelism.** The M1c orchestrator runs worlds concurrently and a scheduler that picks one purchase at a time would serialize the race. The scheduler therefore dispatches the top-`k` affordable frontier purchases where `k` is the parallelism degree, and records **one `schedule.step` per batch**. This is ASHA's asynchronous halving and it carries ASHA's cost honestly: the scheduler commits to `k` purchases before seeing the first result, so its lookahead is stale by up to `k − 1` receipts. The step records `batch: k` and the staleness, so a reader can see which purchases were priced against a decision that had already moved.

---

## 3. Sequential stopping

### 3.1 Fishtest, and the five assumptions we do not have

[Fishtest](https://official-stockfish.github.io/docs/fishtest-wiki/Fishtest-Mathematics.html) is the best real-world precedent in the whole corpus: a GSPRT with explicit type-I/type-II error control that has admitted or rejected every patch to the world's strongest chess engine since 2013. Ch. 3 implication 2 recommends adopting its semantics. **We are not adopting them, and the five reasons are the design.**

| Fishtest assumption | Ours |
|---|---|
| **Evidence units are i.i.d. and exchangeable.** A game is a draw from a fixed distribution; "one more game" always exists. | Our units are **heterogeneous, one-shot rungs**. There is no second suite run — running it again gives the same answer, or reveals a flake, which is EP-6's problem and not evidence. [Decision 10](#decision-10) closes repurchase by construction. |
| **A single scalar metric with a known null.** H0 = "this patch gains 0 Elo". | Our decision is **lexicographically gated and multi-objective** by design (CP-5: no naive weighted sums). There is no scalar to run a likelihood ratio on, and inventing one would be exactly the weighted sum ch. 3 §3.6 and the PRD both refuse. |
| **Unlimited cheap supply.** A run buys hundreds of thousands of games. | We buy **3–7 receipts**, at 0.4 s to minutes each. Asymptotic error control over a sample of five is decoration. |
| **A stationary reference.** Patch vs. master under fixed conditions. | Ch. 3's non-stationarity open question: **repo-level intents are near-unique.** The base tree moves; the distribution the test would control error against does not exist twice. |
| **Non-adaptive sampling.** GSPRT assumes the stream is not chosen. | **Our scheduler chooses it**, deliberately, which is ch. 3's own open question: *"what sequential test remains valid under adaptive, correlated sampling?"* Applying a GSPRT to a stream we selected would be invalid on its face. |

### 3.2 What ships instead

<a id="decision-9"></a>
> **Decision 9. v0 ships an EXHAUSTION rule, explicitly not a statistical test, and the type-I error control Fishtest has we do not have.**
>
> ```
> STOP iff
>   (S-ranking)   no candidate in the pass set is missing a receipt for an
>                 oracle any ranking key reads,                                 AND
>   (S-frontier)  every remaining frontier purchase has value_bp == 0
> OR
>   (S-budget)    no remaining frontier purchase is affordable                  -- a STARVED stop
> OR
>   (S-empty)     the frontier is empty
> ```
>
> `S-ranking` is [decision 4](#decision-4)'s caveat made operational. `S-frontier` is the flip rule. `S-budget` is honest budget exhaustion, and it is the interesting one because of what it produces ([§3.4](#34-the-starved-stop-and-the-escalation-rule-m2a-owes-but-does-not-have)).
>
> **There is no `S-mandatory` clause, and its absence is the neatest result in the block.** The first draft carried one — *"do not stop while the prospective winner has an unpurchased hard-gate oracle"* — as [M2a's purchase law](M2a-oracle-menu.md#the-purchase-law) hoisted from an invariant into a stop predicate. **It is vacuous.** A world with an unpurchased hard-gate oracle has an absent required metric; the gate therefore fails; the world therefore does not pass; `Decide` therefore never names it `Subject`. *"The prospective winner has paid for everything"* is not a condition the scheduler maintains — **it is what a SELECT from `Decide` means.**
>
> That is the precise sense in which M2a's purchase law "lets M2b be adaptive without touching `Decide`": the law is enforced by the decision function, and the scheduler inherits it for free, in every allocation, including allocations nobody has thought of yet. The clause survives only as an assertion in the code that must never fire, plus a test that fires it by hand-constructing the impossible state — because an invariant this load-bearing should still be observed rather than assumed.

### 3.3 The safety claim, which is what replaces the error control

<a id="decision-4"></a>
> **Decision 4 — withholding monotonicity, and why the purchase law suffices.**
>
> **Claim.** For any policy `pol`, any world set `W`, and any allocation the scheduler makes producing receipts `R ⊆ R*` (where `R*` is what the exhaustive M1 ladder would have produced), the set of worlds passing every hard gate under `R` is a **subset** of the set passing under `R*`.
>
> **Proof sketch.** The scheduler only ever *withholds* receipts relative to exhaustion; it never fabricates one and never alters one (it holds no receipt-producing code — [decision 1](#decision-1)). Withholding the receipt a gate's selector counts makes that gate's required metric **absent**. An absent required metric **fails** the gate — structurally, not by policy: M1e's "a required metric that is absent fails the gate … never a fabricated zero", M1f's rule S1, and `evalGates`' evaluation of a selector with no counted receipt. Therefore every world that fails under exhaustion still fails, and some worlds that passed may now fail. ∎
>
> **AMENDED — the pass-set claim is true and the FAR claim did not follow from it.** The red team ran the counterexample and it is not subtle: **`SELECT` is not monotone in the pass set.** Every escalation rule that needs a SECOND passing world to fire — `on_ranking_tie`, `min_candidates_passing`, `on_behavioral_split` — is *disarmed* by withholding. Shrink the pass set from two worlds to one and the tie that would have escalated never materialises; the race admits where exhaustion asked a human. Measured on the adversarial fixture at a binding budget: **8 of 8 adaptive races SELECT + admit, against 3 of 3 ESCALATE (`on_ranking_tie`) under `--schedule=fixed` at the same budget**; 5 of those 8 admitted the cheat and left trunk buggy. `FAR(adaptive) ≤ FAR(exhaustive)` was therefore FALSE as shipped, and it was the strongest claim in the block.
>
> **What restores it, and how far.** Withholding is the mechanism, so the repair is to stop withholding the evidence that decides:
>
> - **By choice — CLOSED.** [Decision 3c](#decision-3c): a hard-gated rung on a live world is admissible whatever the flip, the discount or the clamp says. The scheduler can no longer *choose* to shrink the pass set, so with a budget that can pay, `passSet(R) == passSet(R*)` and the FAR claim holds with equality. Re-measured after the fix: the clamp attack ([decision 3b](#decision-3b)) went from 3 of 6 admitting the cheat to **4 of 4 selecting the honest fix, all 6 receipts, trunk fixed** — identical to the fixed arm.
> - **By poverty — OPEN, and named.** When the budget genuinely runs out, no rule can buy the receipt, and a starved race can still admit while a rival's hard gate was never purchased. That is [the residual](#residual-starved-admission) and it is a decision-layer fact, not a scheduling one: the honest verdict is ESCALATE, which is what [decision 14](#decision-14) produces for a policy that declares `on_evidence_incomplete` (measured: **4 of 4 ESCALATE** on the same fixture and budget) and what the shipped default does NOT do, because the rule ships off until M2d measures its base rate.
>
> **So the claim, stated at the strength it actually has: `passSet(adaptive) ⊆ passSet(exhaustive)` always; `FAR(adaptive) ≤ FAR(exhaustive)` whenever the budget can pay for every hard gate of every live world, or the policy declares `on_evidence_incomplete`.** Outside those two conditions the block ships a measured, named vector rather than a proof. It can also cause a false *rejection* or an escalation, which is the risk M2d must price.
>
> **The one caveat, and it is not small.** Monotonicity holds for the **pass set**, not for the **ranking**. Among worlds that all passed every hard gate, withholding a receipt that a *ranking key* reads makes that key unknown, unknown always loses (M1e decision 5), and the winner can change. `S-ranking` is the fix: no stop while any candidate in the pass set is missing a receipt for an oracle any ranking key reads. It is nearly free under every shipped policy — `tests_passed_desc` reads the suite, which is already hard-gated; `patch_size_asc` reads the world object and needs no purchase — and it is not free in general, and a policy where it binds pays for it visibly in the trace.
>
> **And one ranking key it cannot save**, which is [decision 15](#decision-15): `wall_ms_asc` sums `Cost.WallMS` over a world's counted receipts, so **the world that bought fewer rungs wins it**. That key is refused outright under adaptive allocation.

### 3.4 The starved stop, and the escalation rule M2a owes but does not have

M2a states that "a scheduler that runs out of budget before anyone finished the ladder produces an ESCALATE, which is the honest verdict." **That is false as the code stands, and the gap is exactly where an adaptive scheduler lands.**

`machineryFailure` in [`internal/race/trace.go`](../../internal/race/trace.go) treats a world as machinery-failed when its outcome is not `COMPLETED`, when a counted receipt has `status = error`, or when **the first gate's** oracle produced no receipt at all. A world that bought `guard` and `collect` (both passing) and never bought `suite` satisfies none of those: it is a `COMPLETED` world with no failing receipt and a first gate that *did* produce one. So `on_all_worlds_failed_machinery` does not fire, `passCount == 0`, and the race records **REJECT** — *"these candidates are bad"* — when the truth is *"we never bought the evidence."* That is the over-claim this project exists to remove, arriving through the front door of its own scheduler.

<a id="decision-14"></a>
> **Decision 14. One new escalation rule, `on_evidence_incomplete`, expressible as a PURE predicate over inputs `Decide` already holds.**
>
> ```
> fires iff  ≥ 1 world W with outcome == COMPLETED such that
>            every hard gate W's evidence could evaluate PASSED, and
>            ≥ 1 hard gate has NO counted receipt at all
> ```
>
> **The `passCount == 0` guard is GONE, and its removal is the red team's second correction.** The starved race that matters is the one that ADMITS: one world buys its whole ladder, its rival's last hard gate is never purchased, `passCount == 1`, and a rule guarded on `passCount == 0` cannot fire in the one case it was written for. Measured, shipped default plus the rule: **5 of 5 races admitted the burner and left trunk buggy while the rule sat silent.** Without the guard, the same fixture and budget escalate **4 of 4**. The predicate is untouched and still pure over `(pol, worlds, receipts)`; only the guard moved. It now **replaces SELECT as well as REJECT**, and the sentence's closing clause changes with it — *"did not buy enough evidence to reject these candidates"* when nothing passed, *"…to rank these candidates against each other"* when something did. Precedence is unchanged (1a, below machinery failure, above `on_behavioral_split`), and it still cannot collide with anything: a world that FAILED a gate it was measured against is excluded by the predicate, which is why an ordinary short-circuited ladder never trips it.
>
> **It reads no scheduler state, and it must not.** "The budget ran out" is not a function of `(pol, worlds, receipts)`, so routing it through `Decide` would break purity and replay at once. What *is* such a function is "this world passed everything it paid for and never paid for gate Y", and that is the honest sentence anyway — `Decide` does not know why the receipt is missing, and [M2a's purchase law](M2a-oracle-menu.md#the-purchase-law) says it must not care: *"`Decide` has no way to distinguish 'no receipt because we declined to buy' from 'no receipt because the oracle crashed' — nor should it, because the effect on the candidate is identical."* The rule reports the state, never the cause.
>
> **Precedence 1a**, below `on_all_worlds_failed_machinery` (broken machinery is a different statement from unbought evidence) and above `on_behavioral_split` (which requires `passCount ≥ 1`, so they cannot collide). It **replaces REJECT**.
>
> **It ships OFF** — `int`/`bool` zero ⇒ off ⇒ every pre-M2b policy's semantics reproduced exactly, M1f decision 3 satisfied, and no pre-M2b policy can declare it so no historical evaluation moves. The fixture turns it on. **Promoting it to the shipped default waits on M2d measuring the base rate**, which is M2a decision 9's discipline applied to our own rule rather than to somebody else's.
>
> Rule sentence, byte-exact:
>
> ```
> on_evidence_incomplete → "%d worlds passed every gate their evidence could evaluate and
>                           %d hard gate(s) were never purchased for the leading world
>                           (first unpurchased: %s@%s); this race did not buy enough
>                           evidence to reject these candidates"
> ```

### 3.5 What we would ship when we have data, and why we cannot now

Anytime-valid **e-values / e-processes** are the correct direction, and specifically because they are the one family valid under *both* optional stopping and adaptively chosen sampling — which is the pair of properties GSPRT lacks and we need. The target shape is a test martingale over the gate lattice in which each purchase contributes a likelihood ratio of `P(outcome | the change is correct)` against `P(outcome | it is not)`, with the correlation discount entering as a **reduction in the martingale's effective sample size** rather than as a multiplicative fudge — which is ch. 3 §3.5's open problem stated as a construction.

**It needs one thing we do not have: a calibrated `P(outcome | correctness)` per oracle kind.** That is a joint distribution over receipts and ground truth, and there is no labelled stream until M2d's three-tier oracle produces one. Fitting it from priors would be fabricating the very quantity the paper is meant to measure, on the very axis (FAR) that is the headline. So: **v0 ships no sequential test, M2d produces the calibration data, the e-process is fitted afterward or not at all** — and the allocation trace is designed so that the fitting is possible later without re-running a single race, which is [§4](#4-the-allocation-trace)'s second reason for existing.

---

## 4. The allocation trace

Instrumentation is the deliverable. Without it, M2d's experiment is uninterpretable, `mvo explain` has nothing to render, and PRD §11's `evidence waste` is not computable.

### 4.1 Recorded, never recomputed

<a id="decision-17"></a>
> **Decision 17. The trace is a sequence of OBSERVATIONAL ledger events, one per dispatch batch, and there is exactly one copy of it.**
>
> - `schedule.started` — the budget, the parallelism degree, the **cost table snapshot** (fitted coefficients, their `(kind, seal)` populations, their `n`, and every row's basis), and the compiled scheduler constants (`executor_bp`, the redundancy tiers, the mode). This is what makes an allocation *auditable*: a reader can see why the race allocated as it did without re-deriving a fit that has since moved.
> - `schedule.step` — one per batch: the full considered set with per-row `flip`, `flip_outcomes`, `discount_bp`, `executor_bp`, `value_bp`, `cost_ms`, `cost_basis`, `affordable`, `hard_gate`, `basis` (`decision` \| `research`) and the `declined` reason; the chosen purchases; the decision as it stood; the staleness.
> - `schedule.finished` — totals: purchases considered, bought, declined; budget spent and released; the stop clause that fired.
> - `oracle.skipped` — **M2a's event, kept and given its meaning**: the *terminal* decline, recorded when the scheduler abandons a world's rung for good. `schedule.step` says "not this batch"; `oracle.skipped` says "not ever". Both are needed and neither subsumes the other.
>
> **One copy.** No CAS artifact duplicating the events, because two sources that can disagree is the failure M1f decision 9 exists to refuse. Observational events carry no payload digests, are covered by the ledger hash chain that `mvo audit` already verifies, are **ignored by replay**, and reach `Decide` never. The `mvo audit` referenced-set table (M1f decision 22, normative) gains two rows — `schedule.started`, `schedule.step`, `schedule.finished`: no payload digests, exactly like `oracle.skipped`.

The scheduler's own outputs therefore enter no decision, which buys a property worth stating on its own: **the scheduler may be rewritten, retuned or replaced, forever, without invalidating a single recorded decision.** M1e decision 21 gave `mvo explain` that freedom by making it derived; M2b gets it by making the scheduler's product observational. A research component that can improve without breaking history is the only kind worth iterating on.

### 4.2 Payload shape (canonical JSON, integers only)

Step 3 of the race rendered in [§4.4](#44-mvo-explain---schedule) — the suite batch:

```json
{"batch":2,"budget":{"released_ms":0,"remaining_ms":9178,"spent_ms":822},
 "chosen":[{"oracle":"suite","reason":"highest score_bpps among affordable frontier","world":"mv0:aa1…"},
           {"oracle":"suite","reason":"highest score_bpps among affordable frontier","world":"mv0:bb2…"}],
 "considered":[
   {"affordable":true,"basis":"decision","cost_basis":"fit(pytest-suite,off) n=37","cost_ms":427,
    "declined":"","discount_bp":5000,"executor_bp":5000,"flip":1,
    "flip_outcomes":["fail-closed:REJECT(unchanged)","pass-max:REJECT->SELECT mv0:aa1…"],
    "hard_gate":true,"kind":"pytest-suite","oracle":"suite","score_bpps":5854,
    "value_bp":2500,"world":"mv0:aa1…"},
   {"affordable":true,"basis":"decision","cost_basis":"fit(pytest-suite,off) n=37","cost_ms":427,
    "declined":"","discount_bp":5000,"executor_bp":5000,"flip":1,
    "flip_outcomes":["fail-closed:REJECT(unchanged)","pass-max:REJECT->SELECT mv0:bb2…"],
    "hard_gate":true,"kind":"pytest-suite","oracle":"suite","score_bpps":5854,
    "value_bp":2500,"world":"mv0:bb2…"}],
 "decision_now":{"pass_count":0,"subject":[],"type":"REJECT"},
 "staleness":0,"step":3}
```

The considered set holds **exactly one purchase per alive world** — that is [decision 2](#decision-2)'s frontier, not an abbreviation. The declined `mutation-diff` row appears at step 4, when `aa1` has bought its suite and `mutate` becomes its next rung; `bb2` has been eliminated by then and contributes no row at all.

### 4.3 Evidence waste, computable at last

PRD §11 defines it as *budget spent on evidence that influenced no decision*. That is a counterfactual, and a counterfactual over a pure total function is just another call. **The obvious counterfactual is wrong, and it is wrong because of our own purchase law**, so the derivation is written out rather than asserted.

**The naive definition, and why it collapses.** Leave-one-out: a receipt influenced the decision iff `Decide(pol, W, R \ {r}) ≠ Decide(pol, W, R)`. Under [withholding monotonicity](#decision-4) *removing* a receipt makes its gate's metric absent, which **fails** the gate, which eliminates the world. So for every world that was going to be eliminated anyway, removal reproduces the same decision, and **every receipt bought on a non-winner reads as waste** — including the very receipt that told us the candidate was worse. The metric would report ~50% waste on a healthy two-world race and would score a scheduler best when it verified nothing. The purchase law is what makes the naive metric useless, which is worth knowing about the purchase law.

<a id="decision-18"></a>
> **Decision 18. Evidence waste is derived by BRACKET SUBSTITUTION — the allocator's own lookahead, replayed at audit time — and a second, greedy number is reported beside it.**
>
> A bought receipt `r` **influenced** the decision iff substituting it with either of its bracket outcomes changes `Decide` in `Type` or `Subject`:
>
> ```
> influenced(r) = Decide(pol, W, R[r := fail-closed]) ≠ Decide(pol, W, R)
>              OR Decide(pol, W, R[r := pass-max])    ≠ Decide(pol, W, R)
> ```
>
> This is decision 3's bracket **construction**, reused with the same machinery and the same control-plane-clamped ceilings — but the two are not the same PREDICATE, and the claim that waste equals unrealized ex-ante flip is withdrawn. The ex-ante flip completes the world's entire remaining ladder at one extreme ([§2.3](#23-the-flip-lookahead)); `Substitute()` rewrites exactly ONE recorded receipt and leaves the world's downstream receipts as they are. So `flip` asks *"could a world that pays for this rung reach a different decision?"* and `influenced` asks *"does this one receipt, alone, hold the decision up?"*. Both are worth measuring and neither is the other. Evidence waste is therefore, precisely, **the bought receipts no single-substitution bracket outcome can move**, which is still a statement about ladder order and discriminator cost — i.e. still about scheduling — and is still the only definition under which the metric measures the scheduler rather than the shape of the gate lattice.
>
> - `waste_ms` — Σ `cost.wall_ms` over bought receipts with `influenced(r) == false`. Two `Decide` calls per receipt: at 42 receipts and 15.8 µs, **1.3 ms** for a whole race.
> - `waste_greedy_ms` — greedy substitution in canonical digest order down to a minimal sufficient set. `O(n²)` calls ≈ **56 ms** at n = 42, deterministic because the order is canonical. Reported because single-receipt substitution **understates** waste whenever two receipts are jointly redundant but individually load-bearing; reporting only the first number would flatter the scheduler on exactly the case correlation discounting exists to catch, and the gap between the two is itself a publishable measurement.
>
> **Worked, on the race rendered below** (2 worlds × 3 rungs, 1 676 ms spent, `aa1` selected, `bb2` failed at suite): `aa1`'s three receipts are all influential (`fail-closed` on any of them turns SELECT into REJECT). `bb2`'s **suite** receipt is influential — under `pass-max` bounded by `collected_base = 8`, `bb2` would have passed and could take the subject. `bb2`'s **guard** and **collect** receipts are not: neither substitution moves anything, because `bb2` fails at suite either way. **Waste = 411 ms of 1 676 = 24.5%**, and the sentence it produces is the useful one — *we spent 400 ms collecting on a candidate the next rung was going to eliminate* — which is a statement about ladder order and discriminator cost, i.e. about scheduling.
>
> **Derived, not recorded** — the same split as `mvo explain` (M1e decision 21). The scheduler cannot compute it: at buy time it does not know the final decision. `mvo explain --schedule --json` computes it from the recorded trace, the recorded receipts and the recorded decision, so improving the definition never invalidates a race — which is a good thing, given that this definition is the second one.

<a id="decision-11"></a>
> **Decision 11. Research-mode purchases are labelled, and they are excluded from the waste metric by construction.** M2a decision 8 ships the mutation and property metrics with **no ranking key**, because every key derivable from them is wrong-signed — and correctly so. The consequence for a flip-driven scheduler is sharp and must be said out loud: **a rung nothing reads is decision-inert, scores `value_bp = 0`, and is never bought.** Under `menu.json`, `mutation-diff` and `hypothesis-properties` are bought only when hard-gated.
>
> But M2a also says the metrics ship *because "M2b's evaluation must correlate them against ground truth before anyone ranks by them"* — which requires buying them. `mvo race --collect-inert` is that mode: it buys decision-inert rungs on worlds that passed every gate, marks each trace row `basis: "research"`, and **the waste metric excludes research rows**, because a purchase whose stated purpose is to influence no decision is 100% waste under the PRD's definition and counting it would make the metric meaningless. Off by default. Named in `schedule.started`. The one place in this design where we buy evidence we know cannot matter, and it is labelled as such rather than smuggled in as diligence.

### 4.4 `mvo explain --schedule`

```
schedule (oracle budget 10000 ms, spent 1676 ms, 3 batches, stopped: S-frontier):
  STEP  WORLD      ORACLE   FLIP  DISC   EXEC      COST    SCORE   OUTCOME
  1     mv0:aa1…   guard       1  10000  10000     11 ms   909090  bought  (pass)
  1     mv0:bb2…   guard       1  10000  10000     11 ms   909090  bought  (pass)
  2     mv0:aa1…   collect     1  10000   5000    400 ms    12500  bought  (pass)
  2     mv0:bb2…   collect     1  10000   5000    400 ms    12500  bought  (pass)
  3     mv0:aa1…   suite       1   5000   5000    427 ms     5854  bought  (pass)
  3     mv0:bb2…   suite       1   5000   5000    427 ms     5854  bought  (fail)
  3     mv0:aa1…   mutate      0  10000   5000  rank-only     0  declined
          decision-inert: no gate, ranking key or escalation rule reads mutation-diff

  cost model: pytest-collect  fit(off) n=37  397 ms + 0.4 ms/test  (theil-sen)
              pytest-suite    fit(off) n=37  402 ms + 3.1 ms/test  (theil-sen)
              mutation-diff   declared-rank (no local measurement)
  residual:   predicted 1666 ms, actual 1676 ms (+0.6%), over 6 priced purchase(s)

  evidence waste: 411 ms of 1676 (24.5%), greedy 411 ms (24.5%)
    mv0:bb2…/guard     11 ms  — neither bracket outcome moves the decision
    mv0:bb2…/collect  400 ms  — bb2 was eliminated at the NEXT rung either way
```

**Two notes on that rendering, both corrections.** `mutate` appears there only under `--collect-inert`: `Ladder()` enumerates `pol.Required`, which is derived from what a gate, key, invariant or escalation rule reads, so a rung nothing reads is by construction absent from an ordinary race — and under research mode it is labelled `bought (research)` rather than declined. The decline sentence itself is now **derived from the term that refused the purchase**: `decision-inert: nothing reads it` only when the rung is genuinely read by no gate, key, invariant or escalation rule, and otherwise `no bracket outcome moves the decision at this world's control-plane ceiling [outcomes…]`. The first draft printed the first sentence for every zero-valued row, which put a permanently recorded false statement into `oracle.skipped` — `{"hard_gate": true, "kind": "pytest-suite", "declined": "…nothing reads pytest-suite"}` under a policy whose gates named that very kind. A row with `hard_gate: true` can no longer carry it, and a test asserts the two are mutually exclusive.

The `DISC` column is why the discount is worth having. `collect` follows `guard`, which is `mechanical`/`tree-bytes` against `collect`'s `repo-authored`/`test-identity` — both coordinates differ, so it is independent and undiscounted at 10 000. `suite` follows `collect`, which shares its `repo-authored` prior — same authors, same interpretation of the intent — so it is discounted to 5 000. Two consecutive purchases, two different answers, each traceable to a declared field on a receipt rather than to a tuning knob.

`mvo explain` **renders rows; it recomputes no score**. An acceptance step proves it: mutate the workspace's cost table after a race and assert the rendering is byte-identical.

---

## 5. Remaining resolved design decisions

<a id="decision-10"></a>
10. **No repurchase, ever, in v0 — and it is the honest way to close the flake question.** The scheduler never buys the same `(world, oracle, config digest)` twice. There is therefore no "one more game" axis, no accumulating sample, and nothing that could be mistaken for a sequential test over repeated draws. The alternative — repurchasing to resolve a flake — needs a per-oracle flake rate, which is EP-6 and is not measured. A scheduler that repurchased on a prior would be modelling flakes with a number it invented, and the receipt would carry two contradictory results with no rule for combining them. Closing the axis by construction costs a real capability and buys an honest one.

<a id="decision-12"></a>
12. **The budget is an INTENT field, additive, `0` ⇒ unbounded ⇒ M1 semantics.** `Budget.MaxOracleMS` joins `MaxCandidates` and `MaxWallMS`. It belongs on the intent because CP-2 puts budget there, because it is pinned at creation (which is exactly what PRD §11's three-budget-levels protocol needs — three intents differing only in this integer), and because it is **not a gate**: CP-5 says the pinned policy determines the gate and M2a already declined to put budgets in policy. An M1-era intent decodes with the field absent ⇒ `0` ⇒ unbounded ⇒ the exhaustive ladder, so **every recorded intent keeps racing exactly as it did**. Forward incompatibility is accepted, as in M1f decision 3: an M1 binary refuses an M2b intent at decode, which is the correct failure.

<a id="decision-13"></a>
13. **The scheduler is deterministic given `(policy, worlds, receipts, cost table, budget, constants)`, and all six are recorded.** Determinism is not fastidiousness here: the experiment needs a race to allocate the same way twice, and an auditor needs to see *why* it allocated as it did. The cost table moves as the workspace's ledger grows, which is correct behaviour and would silently make two runs incomparable — so `schedule.started` records the snapshot it used. Tie-breaks are total. No randomness anywhere: Thompson sampling ([AB-MCTS](https://arxiv.org/abs/2503.04412)) is the obvious next move on the *generation* side and it is out of scope here, partly because a stochastic scheduler needs a recorded seed and this project [does not expose seeds anywhere](../../PRD.md) (AG-3).

<a id="decision-15"></a>
15. **`wall_ms_asc` is refused under adaptive allocation, and the general rule is named.** `keyValue` computes it as the sum of `Cost.WallMS` over a world's **counted receipts** ([`internal/race/trace.go`](../../internal/race/trace.go)). Under exhaustion every world buys the same rungs, so the key is at least comparable — noise, which is why M1f decision 20 dropped it from the default. Under adaptive allocation it is **wrong-signed**: a world the scheduler declined to verify has fewer receipts, a smaller sum, and **wins the tiebreak**. "The candidate we verified least, wins" is the study's stopwatch failure rebuilt out of our own scheduler.

    A key is **allocation-sensitive** iff its value can change when a receipt is withheld from a world that still passes every hard gate. `wall_ms_asc` is the only one in today's vocabulary (`tests_passed_desc` and `coverage_desc` read one counted receipt's metric; `patch_size_asc` and `cost_asc` read the world object; `gate_pass` and `world_digest_asc` are structural). **Validation rule 25**: a policy declaring an allocation-sensitive key cannot be scheduled adaptively — `mvo race` refuses at pre-flight with the key named, ledger untouched, and the operator's two outs are removing the key or racing under the exhaustive ladder (`--schedule=fixed`), which is stated in the message. The table is a table, checked by a totality test over the key vocabulary, so a future key must be classified before it ships.

<a id="decision-16"></a>
16. **The generator prompt's `"candidate k of N"` becomes scheduler state the moment generation is scheduled, and that is a named prerequisite rather than a footnote.** Today `RenderPrompt(spec, k, N, task)` renders the ordinal, and `N` is the intent's pinned `max_candidates` — a property of the intent, not of the scheduler, so v0 leaks nothing M1 did not. Under adaptive generation, "you are candidate 7 of 7" tells the generator that six siblings exist and that the scheduler is still spawning, which is precisely the protocol-internal knowledge [ch. 13](../../research/13-ai-control-untrusted-generators.md) shows collapses a protocol's safety when the attacker can condition on it. **Before `generate` becomes an action, the ordinal must be decoupled** — a per-world nonce, or dropping the count. Recorded here so the prerequisite travels with the action rather than being discovered when it ships.

---

## 6. The scheduler as attack surface

Ch. 13's founding result, restated for this block: [Evaluating Control Protocols](https://arxiv.org/abs/2511.02997) found that adaptive attacks exploiting knowledge of protocol internals collapsed a resampling protocol's safety from 96% to **17%**, "demonstrating the importance of denying attack policies access to protocol internals." The scheduler is a protocol internal, and it is a new one.

### What a candidate could do if it knew the scheduler state

| knowledge | attack | v0 status |
|---|---|---|
| that `flip` drives purchases | **Self-suppression**: report metrics making every further purchase on itself decision-inert, so it is never mutation-tested or differentially compared | **Incentive-incompatible, and this is a property worth having.** A world nothing can flip is a world that cannot become the winner either: the rungs it declined are hard gates, an unpurchased hard gate fails, and `Decide` never names a failing world as `Subject` ([decision 9](#decision-9)). Self-suppression is self-elimination — and it is `Decide` that makes it so, not the scheduler. |
| the bracket's ceilings | **Rival starvation** ([vector 23](#new-adversarial-vectors)): report an unreachable `tests_passed` so no rival's `pass-max` can overtake, driving `flip = 0` on every rival and stopping the buying | **NEUTRALIZED** by [decision 3b](#decision-3b)'s control-plane clamp. The bracket's ceilings and the incumbent's clamped key values both come from `collected_base`, the corpus object and `mutants_budget` — none of them candidate-authored. |
| that `cost.units` feeds the fit | **Cross-race cost poisoning** ([vector 22](#new-adversarial-vectors)): report `tests_total = 5000` against 400 ms and flatten the per-unit coefficient for **every future race in this workspace** | **DETECTED / NEUTRALIZED** by [decision 7](#decision-7): `candidate`-authority units clamped to the race's own `baseline.recorded`, Theil–Sen instead of least squares. **Residual, OPEN and named**: at `n = 3` the median tolerates one bad point, so a workspace with three receipts and two adversaries is still steerable. The clamp, not the estimator, is what removes the capability. |
| the remaining budget | **Budget burn** ([vector 24](#new-adversarial-vectors)): make its own rungs slow enough to exhaust the pool and starve every sibling | **OPEN, measured, and this row previously claimed the opposite.** Equal shares do NOT contain a burn: the pool is global, and an unpriced purchase is affordable while any of it remains, so 4 963 ms went out against a 2 000 ms budget and the rival's suite was refused. See [the starved-admission residual](#residual-starved-admission) for what was fixed, what was refused, and what would close it. |
| that withholding shrinks the pass set | **Starved admission**: get a rival's last hard gate left unbought — by burning the pool, or by standing where the bracket cannot see the rival overtake — so the tie or shortfall that would have escalated never materialises and the race admits | **CLOSED where the scheduler CHOSE to withhold** ([decision 3c](#decision-3c): a hard gate on a live world is always admissible). **OPEN where the budget genuinely ran out**, unless the policy declares `on_evidence_incomplete` ([decision 14](#decision-14)). This is [decision 4](#decision-4)'s amended safety claim, and it is the one the red team broke. |
| the gate order, oracle argv, policy digest | steer the ladder | **STOPPED, unchanged** — AG-7 and M1b's negative test, extended below. |
| `mvo explain --schedule` output | the whole protocol at once | **The trace is on the AG-7 deny-list.** `--schedule` is an operator surface; the AG-7 MCP server (`get_intent`, `get_policies`, `report_evidence`) never returns it, never returns the cost table, and never returns a sibling's world digest. |

### AG-7 confirmed, with the negative test extended

The rule stands: **no policy digest, gate name, oracle argv or scheduler state in any agent prompt.** `TestRenderPromptAIControlRule` gains the M2b forbidden set — every `schedule.*` payload key, the budget numbers, the step index and batch size, every score and coefficient, every declared oracle *name*, and every sibling world digest — asserted over every `(k, N, task)` combination, exactly as M1b asserted it for the policy digest and M2a for the corpus digest. `scripts/accept.sh` step 5a asserts the same over the recorded agent **context** and **transcript** blobs of a live race, which is where a leak would actually arrive.

### The fail directions, which is why this is tolerable

`Decide` **fails closed**: an absent metric fails a gate, never passes one. The scheduler **fails open**: an unknown bound makes a purchase look valuable and gets it bought. Neither layer's failure mode helps an adversary — a candidate that confuses the scheduler causes *more* evidence to be purchased about itself, and a candidate that starves the scheduler cannot thereby pass a gate ([decision 4](#decision-4)). The residual is the one M1f named and M2a did not close: **a competent in-process forgery of any streamed number**, which now also steers the allocator's *order*. It cannot steer the allocator into an admission, because the purchase law is not an allocation decision.

<a id="new-adversarial-vectors"></a>
### New adversarial vectors (`testdata/adversarial/vectors/`)

| # | vector | mechanism | verdict |
|---|---|---|---|
| 22 | `schedule_cost_poison` | reports `tests_total = 5000` against a real 8-test suite to flatten the workspace's fitted per-unit coefficient for future races | **NEUTRALIZED** by the unit clamp; **residual OPEN** at the `n = 3` floor |
| 23 | `schedule_starvation` | reports a runaway `tests_passed` so every rival purchase becomes decision-inert and the honest rivals are never verified | **NEUTRALIZED** by the bracket clamp; asserted end to end — the honest rival's suite is bought and the race does **not** stop early |
| 24 | `schedule_budget_burn` | a suite that sleeps long enough to consume the whole oracle budget | **OPEN, measured** — see [the starved-admission residual](#residual-starved-admission). The share does NOT bind under declared-rank pricing and the burn starves siblings: 4 963 ms spent against a 2 000 ms budget, the rival's suite refused, the cheat admitted. Mitigated (priced kinds now bind, priced rows go first, the breach is printed) and **not closed** |
| 25 | starved admission (no fixture; it is the shape of 23 and 24 under any binding budget) | a race that stops on `S-budget` while a live rival's hard gate was never purchased admits the survivor, because `SELECT` is not monotone in the pass set | **OPEN for policies that do not declare `on_evidence_incomplete`, CLOSED for those that do** ([decision 14](#decision-14), measured 4/4 ESCALATE). The SELECT rationale now names every rival it did not measure, in every policy |

Each vector ships a `.policy` sidecar naming the fixture policy that buys the rung it attacks (M2a's convention), and `scripts/adversarial.sh`'s baseline is re-recorded — the diff between baselines is the proof of what this block closed and, for 22's residual, of what it did not.

---

## 7. Amendments to M2a

Recorded here rather than edited into M2a silently, because a design document that quietly absorbs its own corrections stops being reviewable.

<a id="amendment-a"></a>
**A. `cost.units` has an author, and M2a decision 22 did not ask who.** The denominator that makes `wall_ms` learnable is, for the pytest kinds, a **streamed** metric. M2a's own M1f-derived rule — *nothing the scheduler consumes may be authored by a candidate* — is violated by M2a's own cost model. Fixed by [decision 7](#decision-7)'s declared `UnitAuthority` and the clamp. No receipt field moves.

<a id="amendment-b"></a>
**B. `mvo oracles` fits by least squares, which one poisoned sample drags arbitrarily.** Replaced by Theil–Sen. The `no local measurement (n=…)` refusal and the `(kind, seal)` keying from [M2a amendment 27](M2a-oracle-menu.md#decision-27) are unchanged; only the estimator moves, and the `--json` coefficient gains `"estimator":"theil-sen"` so a reader cannot mistake one for the other.

<a id="amendment-c"></a>
**C. M2a's "a starved race produces an ESCALATE" is false against the shipped `machineryFailure`.** A partially-verified `COMPLETED` world is not machinery-failed, so a starved race records REJECT. [Decision 14](#decision-14) is the fix, and it is a pure predicate rather than a scheduler channel into `Decide`.

---

## 8. What the scheduler cannot know yet

The honest list. Each row names what is missing, what v0 does instead, and what would replace it. **"v0 uses a fixed constant here because we have no outcome data until M2d" is the correct answer wherever it appears, and it appears often.**

| # | provably unknown today | what v0 does instead | what replaces it |
|---|---|---|---|
| 1 | `P(the change is correct \| receipts)` — the quantity every VOC formula wants | Nothing. `flip` is a **logical reachability test** over `Decide`, not a probability, and the document never calls it one | M2d's three-tier oracle produces a labelled `(receipts, ground truth)` stream; the posterior is fitted from it, per repo stratum |
| 2 | **The value of a decision change.** All flips are worth the same in v0 | `value_bp` is binary in `flip`: a flip counts 1 whether it turns REJECT into SELECT or moves the subject between two passing worlds | Labelled outcomes weight a flip by ΔTCAR and ΔFAR, which is the only weighting that is not an opinion |
| 3 | **Per-kind outcome likelihoods**, needed for any e-value or SPRT | No sequential test ships; [§3.1](#31-fishtest-and-the-five-assumptions-we-do-not-have) says which Fishtest assumptions we lack and [§3.5](#35-what-we-would-ship-when-we-have-data-and-why-we-cannot-now) says what the martingale would look like | M2d's labels; then an e-process, valid under adaptive sampling, or nothing |
| 4 | **The true correlation between two kinds' outcomes.** M2a *declares* families; nobody has measured whether two `value-behavior` receipts actually co-fail | The four hand-set redundancy tiers (10 000 / 7 000 / 5 000 / 0), read straight off M2a's discount rule | A measured CAPA-style mistake-agreement matrix over M2d's labelled stream, per repo stratum. The tiers become a fitted matrix and the table becomes its default |
| 5 | **`executor_bp` — what a candidate-process receipt is actually worth** | Hand-set 5 000, i.e. "half". No evidence supports the number | M2d's adversarial arm sweeps it and reports FAR-adv per setting ([decision 6](#decision-6)) |
| 6 | **Cost coefficients on a fresh workspace** (`n < 3`) | The declared `Dominant` rank order as an ordinal proxy, row labelled `cost_basis: "declared-rank"`. **No millisecond figure is ever printed for an unmeasured kind** | Receipts. The fit is bootstrapped from the workspace's own history and the residual is reported beside it |
| 7 | **The base rate of behavioural splits** — M2a decision 9's open measurement | The differential is decision-relevant iff `on_behavioral_split > 0`. Binary, no prior over class counts | M2a decision 9's own deliverable: the distribution of class counts over a real intent stream |
| 8 | **Whether cheap-rung rank predicts final rank** — the assumption successive halving imports and [ch. 3 §3.1](../../research/03-adaptive-verification-scheduling.md) flags as the one LLM evidence violates | **v0 never eliminates on rank.** Only hard gates eliminate; the scheduler defers a world, it does not prune it. This is real conservatism and it costs real efficiency | M2d measures the rank correlation between each rung's ordering and the final ordering, per stratum. Only a measured correlation licenses rank-based pruning |
| 9 | **The marginal value of candidate N+1**, and of a mutation of the leader | Generation and mutation are **out of scope** ([§1](#1-what-v0-schedules-and-what-it-does-not)), not scheduled on a prior | Agent spend + M2d labels + the [decision 16](#decision-16) prompt-ordinal fix, all three |
| 10 | **Per-oracle flake rate** (EP-6) | No repurchase, ever ([decision 10](#decision-10)). The axis is closed by construction rather than modelled by a guess | EP-6's quarantine sets and measured flake rates |
| 11 | **Whether the whole thesis is true.** Whether adaptive allocation beats fixed best-of-N and test-voting on TCAR and FAR at equal budget | Nothing. This document builds the instrument; it asserts no result | PRD §11's budget-matched arms with paired McNemar and bootstrap CIs — **"or published null"**, which is in the roadmap in writing and is the outcome this document is designed to make publishable either way |

---

## 9. Wire deltas

```go
// object.Budget — ONE additive field. 0 ⇒ unbounded ⇒ the M1 exhaustive
// ladder, so every recorded intent races exactly as it always did.
type Budget struct {
    MaxCandidates int   `json:"max_candidates"`
    MaxWallMS     int64 `json:"max_wall_ms"`
    MaxOracleMS   int64 `json:"max_oracle_ms"` // NEW — the additive spend bound
}

// policy.EscalationSpec — ONE additive rule; bool zero = off = pre-M2b
// semantics exactly (M1f decision 3/4).
type EscalationSpec struct {
    // … M1e/M1f/M2a fields unchanged …
    OnEvidenceIncomplete bool `json:"on_evidence_incomplete"` // NEW — rule 1a
}

// policy.OracleProfile gains ONE declared field. No receipt field moves.
type OracleProfile struct {
    // … M2a fields unchanged …
    UnitAuthority string // NEW: "control-plane" | "candidate" — who authors cost.units
}
```

**Escalation precedence**, first match wins:

| # | rule | fires when | replaces |
|---|---|---|---|
| 0 | `on_invariant_violation` | (M1f, unchanged) | SELECT / REJECT |
| 1 | `on_all_worlds_failed_machinery` | (M1e, unchanged) | REJECT |
| **1a** | **`on_evidence_incomplete`** | **`passCount == 0` and ≥ 1 COMPLETED world failed only for want of a receipt** | **REJECT** |
| 1b | `on_behavioral_split` | (M2a, unchanged) | SELECT |
| 2–4 | `require_evidence`, `min_candidates_passing`, `on_ranking_tie` | (unchanged) | SELECT |

**Validation rule 25** (numbering continues M2a's 24): a policy declaring an **allocation-sensitive** ranking key (`wall_ms_asc`) is refused by `mvo race` at pre-flight when scheduling is adaptive, naming the key and both outs ([decision 15](#decision-15)). It is checked at pre-flight rather than at load because the same policy is perfectly valid under `--schedule=fixed`, and refusing it at load would brick a legitimate M1 configuration.

**Ranking keys: none added. Gate predicates: none added. Oracle kinds: none added. Metrics: none added. `Receipt`: unchanged.** The scheduler buys evidence; it does not invent any.

---

## 10. Orchestrator changes

`internal/race` phase B is driven by the scheduler instead of by a per-world loop. Phases 0, A and B2 keep their contracts.

- **Phase 0, A** — unchanged, including M2a amendment 29's corpus delivery rules.
- **Phase B — scheduled.** `schedule.started`; then loop: compute the frontier, score it, dispatch the top-`k` affordable purchases concurrently, record `schedule.step`, record each `receipt.recorded` exactly as today, re-evaluate. Receipts are recorded by the same code path with the same binding (`rec.World`, `rec.Freshness.ValidFor`) — **the scheduler decides *whether* a rung runs, never *how***. A world abandoned mid-ladder gets `oracle.skipped` per remaining rung. Under `max_oracle_ms == 0` the loop is provably equivalent to the M1 ladder ([decision 13](#decision-13)), which is a test, not a claim.
- **Phase B2 — STILL A BARRIER in v0, not an action (corrected).** `runCohortBarrier` runs after phase B in both arms; `corpus-differential` never enters the frontier, `Ladder()` never emits a cohort rung, and closing the cohort is not a recorded choice. Its receipts are recorded in cohort order exactly as M2a specifies, and `mvo explain --schedule` lists them under **"not scheduled"** with their spend so the trace does not read as complete. Making it an action is deferred work, not shipped work; the acceptance step below says "every phase-B receipt", and phase B2 is outside it by construction.
- **Decision inputs** assembled digest-sorted (M1c decision 17) — unchanged, so `mvo audit` reproduces byte-for-byte from ledger-scan order.
- `schedule.finished`; `decision.recorded`; `race.finished`; cleanup — unchanged.

---

## 11. Fixtures, acceptance, testing bar

**Fixtures — described as they SHIPPED, not as they were planned.** The first draft of this section described fixtures that do not exist, which is the one kind of documentation error that costs a reader an afternoon:

- `testdata/toyrepo/policies/schedule.json` is a **linear** `guard → collect → suite → observe` ladder with `on_evidence_incomplete: true`, `on_behavioral_split: 2`, a declared (unscheduled) `diff` instance and no allocation-sensitive key. It declares **no `props` oracle and no properties gate**, so it does not supply [§1](#1-what-v0-schedules-and-what-it-does-not)'s third differentiator (two decision-relevant rungs at one gate level); the shapes that exercise the allocator's admissibility rule are unit fixtures in [`internal/schedule/allocate_test.go`](../../internal/schedule/allocate_test.go) — two hard-gated instances of one kind (the near-duplicate discount) and a `require_evidence`-only rung. A `max_oracle_ms` is **not** a policy field ([decision 12](#decision-12) puts the budget on the intent), so the fixture carries none: step 5e derives a binding budget from a reference race instead, which is stronger than a hard-coded millisecond figure would have been.
- `testdata/schedule/` **does not exist and is not needed**: the pure renderer, waste and fit tests build their traces and cost tables in Go, in-file, which is why they run with no worlds, no Python and no docker.
- Adversarial vectors 22–24 ship **no `.policy` sidecar**; they race the shipped default, deliberately, because the default is what an operator gets.

**`scripts/accept.sh`**, seven steps inserted; none changes the shipped default.

- **5a — the trace exists and is complete.** Every phase-B `receipt.recorded` is preceded by a `schedule.step` naming it as `chosen`; every step's considered set is non-empty; `schedule.started` and `schedule.finished` bracket them; `mvo audit` is `OK` with the new events present. **Phase B2 is outside this claim** — the cohort reducer is not scheduled ([§2.2](#22-the-frontier-an-asha-backbone-that-owns-termination-voc-that-owns-order-only), [§10](#10-orchestrator-changes)) — and `mvo explain --schedule` prints its receipts under "not scheduled" so the gap is visible rather than implied away.
- **5b — the null case is null.** Race under the **shipped default** with `max_oracle_ms = 0`; assert the receipt set, the decision, and the rationale are byte-identical to the same race under `--schedule=fixed`. This is [decision 13](#decision-13) under test and it is the compatibility proof.
- **5c — replay is exact.** An M2a-era ledger replays byte-for-byte under the M2b binary (`chain_ok`, `replay_identical`, zero rationale diffs), and a new M2b race replays with `schedule.*` events present and ignored.
- **5d — the trace is recorded, not recomputed.** After a race, corrupt the workspace's fitted cost table and assert `mvo explain --schedule` renders **byte-identically**.
- **5e — the starved stop escalates instead of rejecting.** Race under `schedule.json` with a budget that cannot buy the last rung; assert ESCALATE with `on_evidence_incomplete` and the named unpurchased gate. Then the control: the *same* race under a policy without the rule asserts **REJECT** — the M2a-era behaviour, unchanged, so the rule's effect is visible rather than assumed.
- **5f — evidence waste is computed and non-trivial.** Under `--collect-inert`, assert `waste_ms > 0` for the inert rung, `0` for every gate rung, and that research rows are excluded from the total.
- **5g — AG-7 under live race.** No `schedule.*` payload key, budget figure, score, coefficient, declared oracle name or sibling world digest appears in any world's captured agent context or transcript.

**Testing bar.**

- `internal/schedule` (value): the bracket table per kind; **the control-plane clamp as an invariant** — a property test asserting no bracket bound is ever derived from a candidate-authored metric; `flip` correctness against hand-built decisions for {gate flip, subject flip, no flip}; purity (a fake `DecideFn` counting calls proves the exact call count).
- `internal/schedule` (correlate): the prior-class table's totality over every `(generator, provider)` pair; the redundancy tiers over every ordered kind pair, asserting M2a's three enumerated independent pairs come out independent; the complement exemption; `max` not product, pinned by a three-receipt case.
- `internal/schedule` (cost): Theil–Sen against least squares on a poisoned sample; the unit clamp; the `declared-rank` fallback emitting **no** millisecond figure; the `< minSamples` refusal.
- `internal/schedule` (waste): leave-one-out and greedy over recorded fixtures, including the **jointly-redundant pair** that leave-one-out under-reports — the case the two-number design exists for; determinism of the greedy order.
- **The safety property test, and it is the most valuable test in the block**: over randomly generated policies, worlds and receipt sets, for every subset `R ⊆ R*`, assert `passSet(R) ⊆ passSet(R*)`. [Decision 4](#decision-4) under test rather than in prose.
- **The ladder-order sanity test**: with the frontier constraint lifted, the score's ordering over a fresh race equals the policy's gate order — the rule reproduces the hand-authored ladder from first principles instead of inventing a different verification discipline.
- `internal/policy`: `on_evidence_incomplete` decode/validate; the allocation-sensitivity table's **totality** over the key vocabulary (a new key must be classified before it ships); rule 25's message; `Default()` golden with the **unchanged** M1f/M2a digest pinned.
- `internal/race` (`Decide`): `on_evidence_incomplete` against all six prior rules and its non-firing on every pre-M2b policy; byte-for-byte M2a goldens for every rationale form under a policy without it.
- `internal/audit`: the three new observational rows contribute no references and break no sweep. **Asserted by `scripts/accept.sh` step 5a (`mvo audit` is OK over a ledger carrying them) rather than by a unit test in `internal/audit`, which is where this row first claimed it lived.**
- `cmd/mvo`: `explain --schedule` human and `--json` goldens for {inert rung declined, starved stop, research mode, no-fit cost table}.
- `gofmt -l` clean; `go vet ./...` and `go test ./...` pass **with no docker daemon and with none of hypothesis, mutmut or cosmic-ray installed**; `go.mod`/`go.sum` unchanged; no test invokes a real agent CLI.

---

## Explicitly NOT in M2b

Generation and mutation as scheduled actions (**the central claim's other half** — needs agent spend, M2d labels, and [decision 16](#decision-16)'s prompt-ordinal fix); the adversarial challenger as an arm (AG-6, and ch. 3's open question about scoring it without double-counting); any sequential statistical test — SPRT, GSPRT, e-values, e-processes, test martingales ([§3.5](#35-what-we-would-ship-when-we-have-data-and-why-we-cannot-now)); learned correlation, learned cost models, learned difficulty routers; Thompson sampling or any randomized policy ([decision 13](#decision-13)); rank-based elimination of candidates (row 8 of [§8](#8-what-the-scheduler-cannot-know-yet)); repurchase and flake-aware allocation (EP-6); cross-intent portfolio scheduling (ch. 3's whole-portfolio open question); the eval harness, the budget-matched arms, the hidden-oracle discipline and the paper (**M2d**, and every number in [§8](#8-what-the-scheduler-cannot-know-yet)'s "what replaces it" column is owed by it); the fail-to-pass reproduction test that would give the differential a direction (M2c); LLM-synthesized corpora and properties; scheduler parameters as policy fields ([decision 6](#decision-6) — they arrive when they are measured, not before); `mvo doctor`; Windows.
