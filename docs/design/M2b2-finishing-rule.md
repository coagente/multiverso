# M2b.2 — The Finishing Rule: a revision of CP-4's allocation rule

> **This block exists because of one number and one mechanism.** [M2d](M2d-labelled-evaluation.md) put labels on [M2b.1](M2b1-budgeted-fixed-arm.md)'s disagreement and priced the adaptive rule's failure exactly: at the tightest budget, **`FRR_reachable = 2/2`** — on both instances where a correct candidate was on the table, where the policy would have selected it on full evidence, and where **the money to reach that decision was in the arm's pocket**, the adaptive arm rejected. The harness's own regret decomposition files all of it under **`regret_allocation`, avoidable branch** — not `regret_gates`, not `regret_generation`. It is a failure of the rule, not of the oracles, not of the candidate source, and not of the budget.
>
> **The mechanism was predicted by arithmetic in [M2b](M2b-adaptive-scheduler.md) and then raced twice.** The frontier offers exactly one rung per alive world; symmetric worlds tie on every term of the score; the tie-break falls through to world order; so the allocator advances everybody one rung and **finishes nobody**. Zero worlds hold a full gate set ⇒ nothing passes ⇒ the race rejects. Measured on the shipped default at B2 = 1 529 ms: **adaptive REJECT 5/5, 586 ms of 1 529 spent, 4 receipts, 0 complete worlds**, against **fixed-budget SELECT 3/5, 1 284 ms, 5 receipts, 1 complete world**.
>
> **Round robin is not a bug in the tie-break.** It is what a rule with **no notion of progress toward a completed world** does when the candidates are symmetric. [§1](#1-the-diagnosis-mechanically) takes the rule apart into the three separate defects that produce it, because a fix aimed at the tie-break would fix none of them.
>
> **The revision, in one line and argued in [§2](#2-the-revised-rule):** under scarcity — when the priced remaining ladders of the alive worlds cost more than the pool holds — the score's denominator becomes **the cost to FINISH the world** rather than the cost of the rung, and the equal-share rule becomes a **reservation**: the pool is committed to a prefix of worlds it can actually complete, and a committed world's allowance is the pool minus what its co-committed siblings still need. Neither half works alone. The denominator alone still gets the finishing rung refused by equal shares; the reservation alone is just the depth-first ladder with extra steps.
>
> **The fix introduces ZERO new constants.** [M2b §8](M2b-adaptive-scheduler.md#8-what-the-scheduler-cannot-know-yet)'s standing rule is that a knob nobody has calibrated must not ship, and the completion term is not a knob: it is a sum over the pinned cost table of rungs the policy itself declares. There is nothing here for an operator to tune and nothing for M2d to sweep.
>
> **What is preserved, and proved rather than promised** ([§3](#3-what-does-not-move)): `Decide` stays pure and total and gains nothing; **withholding monotonicity for the hard-gate pass set is untouched**, and its precondition — previously a prose caveat — becomes a **recorded boolean** on every step; the purchase law is unchanged (no rung is ever marked "skipped, assume fine"); replay is exact and every M0–M2d ledger replays byte-for-byte; and every input to the new rule is control-plane authored, which [§6](#6-the-scheduler-as-attack-surface-what-the-revision-adds) re-derives rather than asserts.
>
> **The measurement is pre-registered before it is run** ([§7](#7-pre-registration)). Every published number this fix should move is written down with the direction and size it should move, and — the part that makes it a pre-registration rather than a plan — **the results that would mean the fix did not work, and must not be spun as partial success**. Including the one nobody wants to write down: on a corpus of symmetric two-world races, the correct outcome of this fix is that **the adaptive arm converges on the fixed ladder**, and that is a success for the rule and a **null for the thesis**.
>
> Stdlib only; `go.mod`/`go.sum` untouched. No test invokes a real agent CLI. **ZERO AGENT SPEND.** POSIX only.

## Module layout (delta over M2d)

```
internal/schedule/selector.go     SelectorVOC RETAINED unchanged (the published
                                  rule, kept so the before/after is a paired race
                                  under ONE binary — F15); SelectorVOC2 new
internal/schedule/finish.go       finishMS, the scarcity test, the commit order,
                                  the commit set, the allowance rule — ALL PURE,
                                  all integer, zero Decide calls
internal/schedule/budget.go       share() joined by reserve(); ONE affordable(),
                                  ONE charge point, unchanged (F5)
internal/schedule/value.go        the bracket's pass outcomes gain a REACHABILITY
                                  condition (decision 4); fail-closed unchanged
internal/schedule/trace.go        +finish_ms, +committed, +score_basis on Considered;
                                  +commit_set, +scarce, +uncommitted_ms on Step;
                                  +adaptive_rule on Constants. Observational, additive.
cmd/mvo/race.go                   --selector={voc2|voc}; voc2 is the default for
                                  --schedule=adaptive
cmd/mvo/explain_schedule.go       the regime line, the FINISH and COMMIT columns
internal/eval/freeze.go           the freeze check reads Constants.adaptive_rule;
                                  ABSENT normalizes to "voc", exactly (§5)
scripts/eval.sh                   the adaptive cell races BOTH voc and voc2
```

**Untouched, and a diff that touches them is wrong:** `internal/race`'s `Decide`, `internal/policy`, `internal/object`, `internal/admit`, `internal/audit`, `internal/oracle`, `internal/eval/{instance,derive,label,leak,metrics,arms,score}.go`, `eval/corpora/*`, `eval/splits/*`, and **every byte of `eval/freeze/*.json`**. The eval harness gains one arm and one comparison; it gains no instance, no label, no metric and no scoring change. [§5](#5-the-instrument-what-moves-and-what-must-not) is the accounting.

---

## 1. The diagnosis, mechanically

M2b predicted the failure by arithmetic; M2b.1 raced it; M2d priced it. Three separate defects compound to produce it, and **only one of them is the tie-break everybody names**.

### 1.1 The arithmetic, replayed on the race that was actually run

The shipped default's ladder is `guard → collect → suite`, three rungs per world, two worlds. From the recorded totals — reference spend **S = 1 965 ms**, adaptive spend **586 ms over 4 receipts**, ladder spend **1 284 ms over 5 receipts** — the per-rung split closes exactly:

| quantity | ms | how it is recovered |
|---|---|---|
| a world's full ladder | **982** | S / 2 |
| `guard + collect` per world | **293** | adaptive's 586 over 4 receipts = 2 × (guard + collect) |
| `suite` per world | **689** | 982 − 293 |
| ladder arm's spend | **1 275** | 982 (one world finished) + 293 (the next world's prefix) ≈ 1 284 recorded |

Now the adaptive arm at **B2 = 1 529 ms**, step by step:

```
step 1-4:  buy w1.guard, w2.guard, w1.collect, w2.collect      spent 586, pool 943
step 5:    frontier = { w1.suite (689 ms), w2.suite (689 ms) }
           contenders = 2  ⇒  share = 943 / 2 = 471
           471 < 689 on BOTH rows  ⇒  both declined "exceeds this world's share"
           ⇒  S-budget, 0 complete worlds, passCount 0, REJECT
```

**The pool held 943 ms. The purchase that would have completed a world cost 689. Equal shares offered each world 471, and refused both.** That sentence is the whole failure: the money was there, twice over, and the allocator's own apportionment rule was what said no — to every world simultaneously, which is the one thing an apportionment rule must never do.

### 1.2 Defect one — the score prices a fragment of an indivisible good

```
score_bpps(w,o) = value_bp(w,o) × 1 000 / max(ĉost_ms(w,o), 1)
```

The denominator is **the rung's own cost**. That is the right denominator if evidence is fungible across worlds — buy the cheapest information anywhere, always. It is the wrong denominator under **[M2a's purchase law](M2a-oracle-menu.md#the-purchase-law)**, which makes a world's evidence *all-or-nothing*: an unpurchased hard gate leaves a required metric absent, an absent required metric fails the gate, and **`Decide` never names a failing world as `Subject`**. A world holding two of three rungs is worth exactly as much to the decision as a world holding zero — **nothing**.

So the score prices a fragment of a good that has no value until it is whole. Watch it work at k = 1 on the race above, where the *tie-break never fires at all*:

| step | frontier | scores | bought |
|---|---|---|---|
| 2 | `w1.collect` (v 5 000 / 293 ms), `w2.guard` (v 10 000 / 11 ms) | 17 064 vs 909 090 | **`w2.guard`** |
| 4 | `w1.suite` (v 2 500 / 689 ms), `w2.collect` (v 5 000 / 293 ms) | 3 628 vs 17 064 | **`w2.collect`** |

Value-per-rung-cost systematically prefers **the cheap early rung of a lagging world** over **the expensive last rung of the leading world** — which is round robin arriving through the front door of the arithmetic, with no tie in sight. The tie-break is the mechanism only at equal ladder positions; the denominator is the mechanism everywhere else. A fix aimed at the tie-break alone would have changed the order of steps 1 and 3 and nothing else.

### 1.3 Defect two — equal shares refuse the finishing purchase, on every world at once

[M2b decision 8](M2b-adaptive-scheduler.md#decision-8) divides the remaining pool by the number of worlds still buying. That rule has a property nobody stated: **the last rung of a ladder is the most expensive one** (cheap gates first is the whole point of a fidelity ladder), so the finishing purchase is *exactly* the purchase most likely to exceed a fractional share — and since the worlds are symmetric, it exceeds *every* world's share in the same step. The arm does not choose badly between worlds; it refuses all of them and stops.

M2b.1 disclosed a smaller instance of this as a sanctioned second difference between the arms: *"the adaptive arm tested a 296 ms purchase against a 164 ms share with 328 ms still in the pool and declined it, while the ladder — one contender by construction — tested against the pool."* That is the same defect at a smaller scale, already measured, already published, and already known to be doing work in the comparison.

And decision 8's own justification for equal shares — *"a world that burns its budget starves only itself"* — **was withdrawn by measurement** in M2b's own red-team pass: there is no per-world spend account anywhere, one global pool is charged, and a conformity cheat spent 4 963 ms of a 2 000 ms budget while its rival's suite was refused. **There is no containment property left for equal shares to protect.** What remains of decision 8 is a division that refuses finishing purchases.

### 1.4 Defect three — the bracket is not budget-aware, so it certifies unreachable outcomes

[M2b decision 3](M2b-adaptive-scheduler.md#decision-3)'s `pass-min`/`pass-max` outcomes **complete the world's whole remaining ladder** at one extreme — that is the amendment that made `flip` non-vacuous. But nothing conditions that completion on the world being *able to buy* the ladder it completes. At a binding budget the lookahead therefore answers *"could a world that pays for this rung reach a different decision?"* with money the world will never have. The consequence is that late in a starved race every prefix purchase still scores `flip = 1`, so the arm keeps buying prefixes of worlds it has no way to finish — which is spend that provably cannot influence a decision, i.e. evidence waste by [decision 18](M2b-adaptive-scheduler.md#decision-18)'s own definition, manufactured by the value term that exists to prevent it.

### 1.5 What the diagnosis does NOT say

- It does not say the tie-break is innocent. `world digest asc` was a coin flip over candidate-authored bytes and [M2b.1 decision 3](M2b1-budgeted-fixed-arm.md#decision-3) already replaced it with the control-plane world order, in **both** arms. That fix shipped and stands; it did not and could not fix this.
- It does not say the gates are wrong. `regret_gates` is the component that would say so and M2d computed it separately: the two lost instances are `regret_allocation`, avoidable branch.
- It does not say the budget is too small. `minspend ≤ B` is in `FRR_reachable`'s own predicate. Instances no allocator could have won are excluded by [F16](M2b1-budgeted-fixed-arm.md#f16) and are not in the 2/2.

---

## 2. The revised rule

Everything in this section is a **pure integer function** of `(pinned policy, world set, receipts bought so far, cost-table snapshot, remaining pool, control-plane world order)`. It calls `Decide` zero additional times. The metalevel cost ratio [M2b §2.1](M2b-adaptive-scheduler.md#21-state-and-why-it-is-free-to-hold) rests on — 0.07 % to 0.3 % of the purchase it prices — is unchanged, because the addition is `O(Σ_w L_w)` integer adds per step against 18 `Decide` calls that were already there.

<a id="decision-1"></a>
> **Decision 1 (the gate on the whole revision). The rule changes NOTHING unless the pool cannot finish every alive world.**
>
> ```
> finish_ms(w)   = Σ ĉost_ms(r)  over w's UNBOUGHT rungs, in policy gate order
> scarce         = Σ_{w alive} finish_ms(w) > remaining_pool
> ```
>
> When `scarce == false` — which includes **every unbounded race**, since `remaining_pool` is unbounded — the allocator is [M2b](M2b-adaptive-scheduler.md) verbatim: same score field, same denominator, same equal shares, same admissibility, same stop clauses. Not "equivalent". The same code path.
>
> **Why gate it at all, when the new rule is arguably better everywhere.** Three reasons, and the third is the one that decides it.
>
> 1. **Every null-case proof survives by construction.** [M2b decision 13](M2b-adaptive-scheduler.md#decision-13) (unbounded adaptive ≡ the M1 fixed ladder, acceptance step 5b), [M2b.1 decision 2](M2b1-budgeted-fixed-arm.md#decision-2) (the reference arm ≡ `--schedule=fixed`, step 6a), and every recorded rationale golden are properties of the non-scarce regime, and the non-scarce regime is untouched. A scheduler that cannot be shown to be inert where it should be inert is an artifact of its harness — that argument is M2b's and it is not weakened by the fact that M2b's rule was wrong under scarcity.
> 2. **The blast radius is auditable.** A reader can tell from the trace alone which rule allocated a race, because `scarce` is recorded per step.
> 3. **It is the precondition of the safety claim, and now it is machine-checkable.** [M2b decision 4](M2b-adaptive-scheduler.md#decision-4)'s restored claim reads: *`FAR(adaptive) ≤ FAR(exhaustive)` whenever the budget can pay for every hard gate of every live world.* That condition is **exactly `¬scarce`**, and until now it lived in prose. It is now a boolean on every `schedule.step`. A race whose every step records `scarce: false` carries the FAR claim with equality, provably, from its own ledger. See [§3.2](#32-withholding-monotonicity-and-the-far-claim).
>
> **When any remaining rung is priced `declared-rank`, `finish_ms` is UNKNOWN, `scarce` is undecidable, and the allocator falls back to M2b's rule for the whole race**, recorded as `commit_basis: "unpriced-fallback"` with the kinds named. A fix whose mechanism is arithmetic over predicted costs has no business inventing a cost, which is [decision 7c](M2b-adaptive-scheduler.md#decision-7)'s rule pointed at our own new term. The honest consequence is stated rather than discovered: **on a fresh workspace the revision is inert — which is also exactly where `max_oracle_ms` does not bind** ([decision 7d](M2b-adaptive-scheduler.md#decision-7)), so the rule is active precisely when the budget is real. Under `--budget-basis=predicted` the fallback is unreachable, because that basis already refuses at pre-flight when a buyable kind is unpriced.

<a id="decision-2"></a>
> **Decision 2. The score's denominator becomes the cost to FINISH the world. That is the completion term, and it introduces no constant.**
>
> ```
> value_bp(w,o)     = flip × discount_bp × executor_bp / 10 000        -- UNCHANGED
> score_bpps(w,o)   = value_bp × 1 000 / max(ĉost_ms(w,o), 1)          -- ¬scarce  (M2b)
> score_bpps(w,o)   = value_bp × 1 000 / max(finish_ms(w), 1)          -- scarce   (M2b.2)
> ```
>
> The row records `score_basis: "rung" | "finish"` so no reader and no aggregate can mistake one for the other. This is [M2b decision 7c](M2b-adaptive-scheduler.md#decision-7)'s discipline — *two score fields because there are two units* — applied to two denominators that measure two different things.
>
> **Why a denominator and not a multiplicative `completion_bp`.** A bonus term is the obvious shape and it is refused for the reason [§8 row 5](M2b-adaptive-scheduler.md#8-what-the-scheduler-cannot-know-yet) refuses `executor_bp`'s successor: *"what the constant should be, nobody knows."* A completion bonus would be a second hand-set number in the same expression as the first, with no evidence behind it and one more thing for M2d to sweep. The finish cost is not a constant — it is a sum over the pinned cost table of rungs the policy declares, in milliseconds, in the same currency as the budget it is being spent against. **The defect is a unit error, and the repair is to fix the unit, not to add a term that compensates for it.**
>
> **What it does.** At equal value, a world one cheap rung from completion outranks a world three expensive rungs from completion. That is *"a purchase that completes a world gate set is worth more than one that advances a world halfway"* stated as arithmetic rather than as a preference, and its justification is the purchase law: only a completed world can be `Subject`.
>
> **And it makes depth-first behaviour EMERGE rather than be imposed.** `finish_ms(w)` falls monotonically as `w` buys, so a world at the head of the order becomes *strictly more preferred with every purchase it makes*. There is no stickiness hack, no mode switch, no hysteresis constant. The only thing that can dislodge a partially-finished world from the head is its own `value_bp` collapsing — i.e. its bracket can no longer reach a decision change — and abandoning it is then the correct move. This is why [option (b)](#4-the-options-weighed) does not need a separate depth-first mode: the denominator produces one.

<a id="decision-3"></a>
> **Decision 3. Equal shares become a RESERVATION. The pool is committed to a prefix of worlds it can actually finish, and a committed world's allowance is the pool minus what its co-committed siblings still need.**
>
> ```
> commit order   = alive worlds by (score_bpps desc, finish_ms asc, world-order index asc)
> C              = the longest PREFIX of the commit order with Σ_{w∈C} finish_ms(w) ≤ pool
> uncommitted    = pool − Σ_{w∈C} finish_ms(w)
>
> allowance(w)   = pool − Σ_{v∈C, v≠w} finish_ms(v)        for w ∈ C
> allowance(w)   = uncommitted / |alive \ C|               for w ∉ C
>
> affordable(w,o) ⟺ ĉost_ms(w,o) ≤ allowance(w) ∧ ĉost_ms(w,o) ≤ pool     -- ONE predicate
> ```
>
> **The commitment invariant, which is the whole point of the block.** For every `w ∈ C`, `allowance(w) ≥ finish_ms(w)` by construction of `C`. Therefore **every remaining rung of every committed world is affordable at every step until that world is complete or eliminated**, unless a cost prediction is exceeded. §1.1's dead stop — pool 943, finishing rung 689, share 471, both refused — **cannot occur**. It is not made unlikely; it is made unrepresentable, and [§8](#8-wire-deltas-acceptance-testing-bar) asserts it as a property test rather than as prose.
>
> **Equal shares are the degenerate case, not a casualty.** When `C = ∅` — nothing is finishable — `uncommitted` is the whole pool and the rule is `pool / |alive|`: [M2b decision 8](M2b-adaptive-scheduler.md#decision-8) exactly. When `C` is everybody, every allowance exceeds every finish cost and affordability never binds: M2b's unbounded behaviour exactly. The new rule interpolates between the two cases M2b got right and replaces the case it got wrong. **`share = remaining / |contenders|` was an apportionment that imputed every world an equal *need*; `allowance` uses the need the policy and the cost table actually declare.**
>
> **Answering decision 8's own objection, which was the reason for equal shares.** Decision 8 refused unequal shares because *"a world granted a large share because it looked promising can burn it and take the race's budget with it."* Three answers. (i) **No world is granted anything for looking promising.** The allowance is a function of the *declared ladder* and the *fitted cost table*, both control-plane; a candidate cannot make its own ladder look short. (ii) **The containment property being protected does not exist** — §1.3, measured: one global pool, no per-world account, 4 963 ms spent against a 2 000 ms bound. (iii) The burn residual is **unchanged in kind**, still named, and still closed only by the deadline dispatch that needs `internal/oracle` to emit an errored *receipt* rather than an error return. [§6](#6-the-scheduler-as-attack-surface-what-the-revision-adds) re-derives its exploitability under the new rule.
>
> **Why a prefix and not a knapsack.** A knapsack maximizing the count of completed worlds is available and is refused: the objective is not *count*, it is *count among worlds that can still change the decision*, which is what the commit order already ranks by. A prefix is `O(n log n)`, total, deterministic, and expressible in one sentence in the trace (`committed: 1 of 2 worlds; mv0:aa1… needs 982 ms of 1 529`). A knapsack is none of those and has no better claim to the objective.
>
> **A world's membership in `C` is recomputed every step and is never recorded as a property of the world.** Elimination releases a world's whole reserve immediately and `C` re-forms in the same step — which is [successive halving's actual semantics](M2b-adaptive-scheduler.md#decision-8) (elimination releasing budget) and is the reason [§4](#4-the-options-weighed) does not need to eliminate anything.

<a id="decision-4"></a>
> **Decision 4. The bracket's PASS outcomes are conditioned on reachability. `fail-closed` is unchanged.**
>
> ```
> Bracket(w,o) ⊇ { fail-closed }                                       -- always
> Bracket(w,o) ∋ { pass-min, pass-max }  iff  finish_ms(w) ≤ allowance(w)
> ```
>
> A `pass` outcome completes the world's entire remaining ladder ([decision 3](M2b-adaptive-scheduler.md#decision-3)'s amendment, and the reason `flip` is not vacuous). If the budget cannot buy that ladder, the outcome is not a decision the race could reach, and certifying a purchase against it is certifying it against money that does not exist. `fail-closed` stays unconditional because a world can always fail — failing costs one rung, not a ladder.
>
> This is [option (d)](#4-the-options-weighed) — budget-aware lookahead — **in its exact, cheap form**: `finish_ms(w) ≤ allowance(w)` is one integer comparison against numbers decision 3 already computed. §4 says why the strong form is refused, with the arithmetic.
>
> **The direction of this term is conservative, and that is deliberate.** It can only ever turn a `flip` from 1 to 0, i.e. make a purchase look *less* valuable — the opposite of [decision 3b](M2b-adaptive-scheduler.md#decision-3b)'s *"the scheduler fails open"*. It is safe to invert the fail direction here, and only here, because the term is not a bound on a candidate-authored quantity: an unknown `finish_ms` does not evaluate to "unreachable", it makes the whole rule fall back ([decision 1](#decision-1)). There is no unknown for this term to fail closed on.

<a id="decision-5"></a>
> **Decision 5. [Decision 3c](M2b-adaptive-scheduler.md#decision-3c) is amended at exactly one point: the hard-gate override applies to worlds the budget can still FINISH.**
>
> ```
> completable(w)   =  finish_ms(w) ≤ allowance(w)
> admissible(w,o)  =  flip(w,o) == 1  OR  (o is read by a HARD GATE and w is alive and completable(w))
> ```
>
> Decision 3c exists because three separate ordering terms — the near-duplicate discount, the control-plane clamp, and `flip` itself — each refused a hard gate **while the budget could pay for it**, shrinking the pass set by *choice* and turning a SELECT into a REJECT. Every one of those three failures happened at a budget that could complete the world. The amendment leaves all three fixes intact: when `completable(w)`, decision 3c is verbatim.
>
> **What lapses, and why it is not the same thing.** For a world the pool cannot finish, buying its prefix cannot put it in the pass set — it can only spend money on a world that is already out for reasons of poverty. Refusing that purchase does not shrink the pass set relative to what the budget could have achieved; it is not a choice to withhold, it is the absence of an alternative. And the flip test still governs: **where such a purchase genuinely can move the decision, it is still bought.** The concrete case is worth spelling out because it shows the rule reading the policy rather than a heuristic:
>
> | policy | base decision, w1 complete & passing, w2 unmeasured | `fail-closed` on w2's guard | flip | bought? |
> |---|---|---|---|---|
> | shipped default | SELECT w1 (rationale names the rival it never measured) | w2 fails a gate ⇒ still SELECT w1 | **0** | **no** — and the 547 ms is not spent |
> | declares `on_evidence_incomplete` | ESCALATE (w2 has unpurchased hard gates) | w2 measured and failing ⇒ excluded from the predicate ⇒ SELECT w1 | **1** | **yes** |
>
> Under the rule that cares whether the rivals were measured, measuring them is decision-relevant and gets bought. Under the rule that does not, it is waste and does not. Neither answer is hand-written; both fall out of `Decide`.
>
> **The cost of the amendment, stated at full strength.** It makes the arm *stop earlier* with money in the pool, and a race that stops with a complete passing world and an unmeasured rival is [vector 25](M2b-adaptive-scheduler.md#new-adversarial-vectors) — the starved admission — which is **open for policies that do not declare `on_evidence_incomplete`**. That was true before this block and it is true after it. What changes is the *rate*: see [§3.3](#33-the-cost-this-fix-pays-and-it-is-not-hidden).

<a id="decision-6"></a>
> **Decision 6. `voc` is RETAINED as a selectable arm, and the published comparison is a paired race, not a comparison against a table.**
>
> `SelectorVOC` is not deleted, not edited and not "upgraded in place". `--selector={voc2|voc}` chooses; `voc2` is the default for `--schedule=adaptive`; `schedule.started.selector` records which ran.
>
> **This is required by the harness's own fairness conditions, not by nostalgia.** [F15](M2b1-budgeted-fixed-arm.md#f15) says both arms' `Decide`, oracles, cost model and clamps must be one build. Deleting `voc` would make the before/after comparison a comparison against **a different binary** — every number in §7 would be confounded with every other change in the tree since M2d, and no per-instance difference would be attributable. Retaining it makes the revision's effect a *treatment* in the same paired, interleaved, rotation-matched protocol that M2b.1 built and M2d used.
>
> One loop, **three** selectors. [M2b.1 decision 1](M2b1-budgeted-fixed-arm.md#decision-1) is unchanged and gains a third implementation; the frontier rule, `budget.affordable`, the charge point, the elimination rule, the stop clauses, the batch fill and the trace stay in `schedule.go` and are executed identically by all three.
>
> The `Selector` seam's `Contenders(frontier) int` becomes `Allowances(frontier, pool) []int64` — one entry per frontier row, in frontier order. The three implementations, and the reason this is a generalization rather than a replacement:
>
> | selector | `Allowances` | equal to |
> |---|---|---|
> | `voc` | `pool / len(frontier)` for every row | `budget.share(len(frontier))` — **a test asserts bit-identity with M2b** |
> | `voc2` | [decision 3](#decision-3)'s formula | `voc`'s value when `C = ∅`; unbounded when `C` is everybody |
> | `ladder` | `pool` for every row | `budget.share(1)` — today's `Contenders() == 1`, **behaviour unchanged** |
>
> The ladder arm reserves nothing and needs to: its *ordering* already serializes the spend, and changing it would move the reference arm of a published comparison, which is forbidden by [§5](#5-the-instrument-what-moves-and-what-must-not).

<a id="decision-7"></a>
> **Decision 7. No new stop clause, no new escalation rule, no new policy field, and `on_evidence_incomplete` still ships OFF.**
>
> A deferred purchase is `Admissible && !Affordable`, so `stopClause` returns **`S-budget`** — which is the true statement: the money ran out *for that world*. Adding an `S-committed` clause would fork the stop vocabulary that [M2b.1 decision 6](M2b1-budgeted-fixed-arm.md#decision-6) keeps shared so the arms' stop histograms remain comparable, and it would say something the budget clause already says.
>
> What changes is the **reason string**, because two different budget facts must not print the same sentence:
>
> ```
> reserved: the pool is committed to finishing %d world(s) (%s); this world needs
>           %d ms to finish and %d ms are uncommitted
> unreachable: this world's remaining ladder costs %d ms against a %d ms allowance,
>           so no pass outcome is purchasable and no bracket outcome moves the decision
> ```
>
> Both are carried into `oracle.skipped` at the terminal decline, exactly as [M2b decision 17](M2b-adaptive-scheduler.md#decision-17) requires — the operator's whole record of what was never found out. Neither may ever be printed on a row that was bought, and neither may carry the `decision-inert` sentence, which the existing mutual-exclusion test extends to cover.
>
> **`on_evidence_incomplete` is not promoted here.** It is the decision-layer answer to the starved admission and this block makes starved admissions more reachable ([§3.3](#33-the-cost-this-fix-pays-and-it-is-not-hidden)), which is an argument for measuring its base rate, not for flipping it on inside the block that changes the allocator. Promoting it in the same commit would confound the rule change with an escalation-policy change in every number in §7. [F14](M2b1-budgeted-fixed-arm.md#f14) already requires the whole protocol to be run twice, once under each configuration, and §7 pre-registers both columns.

---

## 3. What does not move

### 3.1 `Decide` is pure, total, and gains nothing

No new input, no new field, no new rule, no scheduler state routed into it. `finish_ms`, `scarce`, `C` and `allowance` are computed in `internal/schedule` from the cost-table snapshot and the policy's own ladder; `internal/race`'s `Decide` never sees them and could not read them if it wanted to — the scheduler holds a `DecideFn` and does not import `internal/race` ([M2b decision 1](M2b-adaptive-scheduler.md#decision-1)). The metalevel/object-level ratio the whole design rests on is preserved: **zero additional `Decide` calls per step**, plus `O(Σ_w L_w)` integer additions.

### 3.2 Withholding monotonicity, and the FAR claim

**The pass-set claim is untouched and its proof does not change one word.** The scheduler still only ever *withholds* receipts relative to exhaustion; it never fabricates one and never alters one; withholding makes a required metric absent; an absent required metric fails the gate. Therefore `passSet(R) ⊆ passSet(R*)` for every allocation the revised rule can make. **The randomized property test that pins it ([M2b §11](M2b-adaptive-scheduler.md#11-fixtures-acceptance-testing-bar), "the most valuable test in the block") is unchanged and must stay green with `voc2` in the generator's selector set** — that is an acceptance requirement, not a hope.

**The FAR claim is unchanged in strength and improved in legibility.** [Decision 4](M2b-adaptive-scheduler.md#decision-4) as amended reads: *`passSet(adaptive) ⊆ passSet(exhaustive)` always; `FAR(adaptive) ≤ FAR(exhaustive)` whenever the budget can pay for every hard gate of every live world, or the policy declares `on_evidence_incomplete`.* Under `¬scarce` the revised rule **is** M2b's rule and the claim holds exactly as before. Under `scarce`, the claim's precondition is false by definition — it was already false for M2b, which is why the "by poverty" branch was named OPEN. **This block does not narrow the claim; it names the boundary and records it.** `scarce: false` on every step of a race is now a from-the-ledger proof that the race ran inside the claim.

### 3.3 The cost this fix pays, and it is not hidden

**Adaptivity's failure mode moves from false rejection toward starved admission, and that is a real trade, not a free win.**

Under M2b's rule, a starved race finished nobody, so `passCount == 0`, so it recorded REJECT — a false rejection when a correct candidate was present, which is what M2d priced at `FRR_reachable = 2/2`. Under the revised rule a starved race finishes somebody, so `passCount` can be 1 with rivals unmeasured, so it can record SELECT — which is [vector 25](M2b-adaptive-scheduler.md#new-adversarial-vectors), the starved admission, and which `SELECT`'s non-monotonicity in the pass set makes reachable.

Three things are true about that and all three go in the doc:

1. **It is not a new hole.** The fixed-budget ladder has had exactly this behaviour since M2b.1 and it is [decision 4](M2b1-budgeted-fixed-arm.md#decision-4)'s explicit design point: *"both arms inherit the same starved-race semantics, including the same hole… An arm that handled starvation differently would be comparing a scheduler against a decision rule."* The revision makes the adaptive arm inherit it too, which is what makes the two arms comparable.
2. **The decision-layer answer exists, is measured, and ships off.** `on_evidence_incomplete` turns exactly this race into an ESCALATE (measured 4 of 4 in M2b). The shipped default does not declare it.
3. **The maximal commit set is the safest available choice, and it was chosen for that reason.** Every escalation rule that needs a *second* passing world — `on_ranking_tie`, `min_candidates_passing`, `on_behavioral_split` — is disarmed by withholding. `C` is the **longest** fitting prefix, not the cheapest single world: whenever the pool can finish two worlds it finishes two, and the tie or shortfall that would have escalated still materialises. Only when the pool can finish exactly one does the pass-set-of-one hazard arise, and at that budget it is unavoidable by any allocator.

§7 pre-registers this as a falsifier: **if adaptive's FAR rises above the ladder's at the same cell, the fix traded a false rejection for a false admission and must be reported that way.**

### 3.4 The purchase law, replay, and M1f

- **The purchase law is unchanged.** No rung is ever marked "skipped, assume fine"; there is no such state and there will not be one. A deferred rung is an *unbought* rung: its metric is absent, its gate fails, and `Decide` does not know or care why ([decision 14](M2b-adaptive-scheduler.md#decision-14)'s rule reports the state, never the cause). Every unbought rung of a starved world still gets its `oracle.skipped`.
- **Replay is exact.** Every new field is additive on **observational** `schedule.*` events, carries no payload digest, is covered by the ledger hash chain, and is ignored by replay. Every M0–M2d ledger replays byte-for-byte. The shipped default policy digest does not move (`mv0:f207c3fa…`), because no policy field is added, removed or reinterpreted.
- **Absent is absent.** A pre-M2b.2 trace has no `scarce`, no `commit_set` and no `finish_ms`. Those render as **`unknown (pre-M2b.2 trace)`**, never as `false`, `[]` or `0` — the same rule and the same argument as [M2b.1 decision 6](M2b1-budgeted-fixed-arm.md#decision-6)'s treatment of `world_order`: inventing a past allocation is inventing evidence. A `voc` row in a *new* trace carries `score_basis: "rung"` and no `finish_ms`, and renders `—`; a `voc2` row under `¬scarce` carries `score_basis: "rung"` **and** a `finish_ms` it did not divide by, which is a measurement worth having and is labelled as not-the-denominator.
- **M1f: nothing the rule consumes is candidate-authored.** Enumerated, because the rule is new and the claim must be re-derived rather than inherited:

| input to the new rule | authored by | already argued in |
|---|---|---|
| the ladder (which rungs remain) | the pinned policy | [M2b decision 2](M2b-adaptive-scheduler.md#decision-2) |
| `ĉost_ms` fixed + per-unit terms | the workspace's own fitted cost table, Theil–Sen / median-fixed | [M2b decision 7b/7d](M2b-adaptive-scheduler.md#decision-7) |
| `units` in that prediction | control-plane denominators (`collected_base`, corpus `cases_total`, `mutants_budget`) with candidate-authority units clamped at fit time | [M2b amendment A](M2b-adaptive-scheduler.md#amendment-a), vector 22 |
| which rungs a world has bought | the scheduler's own record of its own purchases | — |
| whether a world is alive | `Decide`'s own gate predicate over recorded receipts | [M2b decision 9](M2b-adaptive-scheduler.md#decision-9) |
| the tie-break / commit order tail | the **control-plane** world order, candidate ordinal ascending, rotated | [M2b.1 decision 3](M2b1-budgeted-fixed-arm.md#decision-3) |
| `remaining_pool` | the charge point, one global counter | [M2b.1 decision 5](M2b1-budgeted-fixed-arm.md#decision-5) |

A property test asserts it the way the bracket clamp is asserted: **no value entering `finish_ms`, the scarcity test, the commit order or the allowance is derived from a candidate-authored metric.**

---

## 4. The options weighed

Four were on the table. Two are in, one is in only in its weak form, one is refused. Each says why.

### (a) A completion term — **ADOPTED, as the score's denominator** ([decision 2](#decision-2))

It earns its place because it names the actual defect: the score priced a fragment of an indivisible good. It earns its *shape* — a denominator rather than a bonus — because a bonus would be a hand-set constant in the same expression as `executor_bp`, which [§8 row 5](M2b-adaptive-scheduler.md#8-what-the-scheduler-cannot-know-yet) already lists as a number with no evidence behind it. **Alone it is insufficient**: reorder the queue all you like, equal shares still refuse the finishing rung to every world simultaneously (§1.3). A version of this block that shipped only (a) would have changed the order of the four purchases in §1.1 and still recorded REJECT with 943 ms unspent.

### (b) Depth-first bias under scarcity — **ADOPTED, as the reservation** ([decision 3](#decision-3))

It earns its place because it is the half that moves *money* rather than *order*, and money was what refused the purchase. **Alone it is the wrong block**: a depth-first commitment with M2b's per-rung score is an allocator whose ordering fights its own reservation — the score keeps nominating the cheap rung of a lagging world while the reserve is held for the leader. And a depth-first commitment with *no* score is `SelectorLadder`, which already exists and is the arm we are being measured against.

The two halves are one mechanism, which is why they are one block: **(a) makes commitment emerge from the score** (`finish_ms` falls monotonically as a world buys, so the head world is self-reinforcing — no mode, no hysteresis, no constant), and **(b) makes the emergent commitment affordable** (the commitment invariant). Neither is a heuristic layered on the other.

### (c) Genuine successive halving — **REFUSED, and here is what would have licensed it**

Elimination on rank imports the assumption that **cheap-rung rank predicts final rank**, which [ch. 3 §3.1](../../research/03-adaptive-verification-scheduling.md) flags as the one thing LLM-generated evidence violates and which [M2b §8 row 8](M2b-adaptive-scheduler.md#8-what-the-scheduler-cannot-know-yet) refused on that basis. The question this block has to answer is whether M2d changed that.

**It did not.** M2d measured TCAR, FAR, FRR and the regret decomposition. **It did not measure the rank correlation between each rung's ordering and the final ordering** — the measurement §8 row 8 names as the thing that would license pruning ("only a measured correlation licenses rank-based pruning"). It could not have: coverage is 3/5 over five instance-slices spanning **two independent bugs**, three of `derive.go`'s seven operators decline on both fixtures, and every wrong candidate is one of four perturbations of a single-line change, uniformly trivial for the suite. A rank correlation fitted on that is a property of `derive.go`. So the licence is exactly as absent as it was, and taking it anyway would be fitting the scheduler to the candidate generator — which M2d's own "Explicitly NOT" list forbids by name.

**And deferral gets the benefit elimination was for, without the assumption.** Successive halving eliminates in order to free budget. The reservation frees budget by *not committing* it, and a deferred world is **resurrected the moment a committed world dies**, in the same step, because `C` is recomputed from scratch every step and holds no per-world state. Elimination is irreversible: a mis-ordering at rung 1 permanently loses the correct candidate, which is precisely the loss `FRR_reachable` exists to price and precisely the loss this block exists to remove. **The only thing that eliminates a world remains a failed hard gate**, and `Alive()` is unchanged.

### (d) Budget-aware lookahead — **ADOPTED IN ITS WEAK FORM, refused in its strong form on measured cost**

The **strong** form is available and exact: [M2b.1 §5](M2b1-budgeted-fixed-arm.md#5-the-retrospective-allocation-bound)'s `bound.go` already enumerates prefix-closed subsets and asks `Decide` whether any reaches a given decision. Run forward at allocation time it would answer *"can the remaining budget still reach a decision change after this purchase?"* — exactly the question (d) poses, with no estimate anywhere.

It is refused on arithmetic:

```
Π_w (L_w + 1) subsets per step × 15.8 µs
  2 worlds × 3 rungs   =     16 subsets  ≈ 0.25 ms/step     affordable
  6 worlds × 4 rungs   = 15 625 subsets  ≈ 0.25 s/step      ~24 steps ⇒ 6 s of metalevel
  6 worlds × 7 rungs   ≈ 262 000 subsets ≈ 4 s/step         indefensible
```

Against an object level of ~2 s for that race, the metalevel would go from **0.07–0.3 %** to **over 100 %** — [Hay et al.](../../research/03-adaptive-verification-scheduling.md)'s precondition for metareasoning being worth doing at all, violated by our own scheduler. A rule that is affordable only at two worlds is not a rule, and pruning the enumeration would make the "exact" claim false while keeping its name.

The **weak** form — [decision 4](#decision-4)'s reachability condition on the bracket's pass outcomes — is one integer comparison against a number [decision 3](#decision-3) already computed, and it captures the necessary condition (`a world must be completable before any pass outcome is reachable`) without the enumeration. The strong form stays where it belongs: **as a diagnostic**, `mvo explain --schedule --bound`, derived, off by default, refusing above 10⁶ subsets rather than approximating.

---

## 5. The instrument: what moves, and what must not

**The instrument must not move.** M2d froze an eval configuration, and this block changes the rule that configuration was frozen against. The freeze is supposed to notice. Everything below is written so that a reader can check that the freeze noticed for the right reason and that nothing else moved.

### 5.1 The defect this block found in the freeze, and the minimal fix

`eval/freeze/local-derived-v1.json` pins `policy_digest` and `constants` — `executor_bp.*`, `redundancy_bp.*`, `bound_cap`, `cost_min_samples` — plus an empty, unchecked `binary_digest`. **This revision changes no constant and no policy field.** It would therefore have passed the freeze check silently: the mechanism that exists to make post-freeze tuning impossible to do quietly would have failed to notice a change to *the allocation rule itself*, which is a larger change than any constant it does pin.

**The freeze pins the scheduler's numbers and not its rule. That is a defect in the check, and it is fixed in the direction of MORE refusal.**

<a id="decision-8"></a>
> **Decision 8. `schedule.started.constants` gains `adaptive_rule`, the freeze check compares it, and an ABSENT value in a freeze file normalizes to `"voc"` — exactly, not by assumption.**
>
> - `Constants.AdaptiveRule` is the rule **the binary uses for `--schedule=adaptive` by default** — a property of the build, constant across the arms of one protocol run. It is *not* the per-run selector: `--selector=voc` records `selector: "voc"` while `adaptive_rule` still reports `"voc2"`, because the binary is what moved.
> - It is rendered from the same constant the selector registry holds, exactly as `ExecutorConstants()` calls `ExecutorBP` rather than restating numbers. A snapshot that can disagree with the thing it snapshots is worse than no snapshot.
> - **No byte of any freeze file changes.** A frozen `constants` block with no `adaptive_rule` key means `"voc"` **exactly**: no binary before M2b.2 could allocate by any other rule. This is [M2b.1 decision 6](M2b1-budgeted-fixed-arm.md#decision-6)'s normalization argument, reused because it is the same argument. Writing a retroactive value into a frozen artifact would be editing the instrument.
> - The check therefore **refuses**, naming what moved: `adaptive_rule: "voc" -> "voc2"`.
>
> **This change can only make the freeze refuse more often, never less.** That direction is the whole justification for touching a verification path at all, and any future edit to this check must be able to make the same claim.

### 5.2 The unfreeze contract, executed rather than described

M2d [decision 6](M2d-labelled-evaluation.md#decision-6) says tuning after the freeze is not forbidden, it is made impossible to do quietly. The quiet-impossible version of this measurement is:

```
mvo-eval score --split eval --unfreeze \
  "M2b.2 revises the CP-4 allocation rule (adaptive_rule voc -> voc2: finish-cost score
   denominator + commit-set reservation) in response to M2d's FRR_reachable = 2/2 at B1,
   which the harness attributed to regret_allocation, avoidable. The frozen constants
   (executor_bp, redundancy_bp, bound_cap, cost_min_samples) and the frozen policy digest
   are UNCHANGED; adaptive_rule is what moved and the freeze check gained that field in
   the same block. Pre-registration: docs/design/M2b2-finishing-rule.md §7, written before
   any measurement. Corpus, split, salt, instances, labels, scoring and metrics unchanged."
```

The reason string, the diff and the timestamp are appended to the run log by the existing helper. **The eval-use counter stays honest**: this block declares its query count up front — the cells in [§7.4](#74-the-protocol-declared-before-it-is-run) and no others — and if the measurement disagrees with the pre-registration, **the pre-registration is what gets published**. A cell is not re-scored to chase a number; a second scoring of the same cell is a second leaderboard query and appears in the published count.

### 5.3 What is forbidden in this block, in writing

- No new eval instance, no removed instance, no changed candidate set, no changed split, no changed salt.
- No change to `internal/eval/{metrics,label,leak,instance,derive,score,arms}.go` beyond the freeze-check field. **In particular: `FRR_reachable`, `regret_*`, TCAR, FAR and the `unknown` interval arithmetic are the instrument that convicted the rule, and they do not move in the block that answers the conviction.**
- No change to the ladder arm's behaviour. It is the reference of every published comparison ([decision 6](#decision-6)'s table pins its allowance to today's value).
- **No new fixture may enter the M2d cells to make the adaptive arm look better.** The corpus is symmetric two-world races over two bugs, adaptivity has little room on it, and the honest response to that is [§7.5](#75-the-null-that-would-be-honest), not a fixture whose asymmetry was chosen after seeing which rule needed it. An asymmetric-ladder probe is named as future work in [Explicitly NOT in M2b.2](#explicitly-not-in-m2b2) and is explicitly barred from the headline cells.

---

## 6. The scheduler as attack surface: what the revision adds

[Ch. 13](../../research/13-ai-control-untrusted-generators.md)'s finding transfers unchanged — adaptive attacks that exploit protocol internals collapsed a resampling protocol's safety from 96 % to 17 % — and the revision adds one new internal: **which world the pool is committed to.**

| knowledge | attack | status |
|---|---|---|
| that the pool commits to a prefix of a control-plane order | **Commit handoff (NEW, vector 26).** A candidate that knows its ordinal fails its own cheapest gate immediately, is eliminated, releases its whole reserve, and hands the budget to the next world in the order — which under `"candidate k of N"` it knows is candidate *k+1* | **OPEN by construction, NOT exercisable today, and it is a conditioning channel rather than a steering one.** It cannot choose *who* benefits (the order is control-plane and rotated), only *that* someone does, and self-elimination is terminal for the attacker. It ships as a **named vector with no fixture**, like [vector 25](M2b-adaptive-scheduler.md#new-adversarial-vectors), because building it needs a colluding pair and an agent, and this block spends nothing on agents. It strengthens a prerequisite that already exists twice: **the generator prompt's `"candidate k of N"` must go before a budgeted arm faces untrusted generators** ([M2b decision 16](M2b-adaptive-scheduler.md#decision-16), [M2b.1 decision 3](M2b1-budgeted-fixed-arm.md#decision-3)) |
| that `finish_ms` gates commitment | **Make yourself cheap to finish** | **NOT AVAILABLE.** `finish_ms` is a sum over the *policy's* rungs priced by a *per-kind* fit with control-plane units. It is identical for two worlds at the same ladder position by construction; a world becomes cheaper to finish only by *having bought rungs*, and what it bought was the scheduler's choice |
| that `scarce` switches the rule | **Cross-race cost poisoning with a new payoff** ([vector 22](M2b-adaptive-scheduler.md#new-adversarial-vectors)) | **SAME CAPABILITY, SAME MITIGATION, NEW PAYOFF — and both directions are named.** Understating the fit ⇒ `scarce` false when it is true ⇒ the arm behaves like M2b, i.e. a denial-of-decision attack that reproduces the failure this block fixes, and admits nothing it could not admit before. Overstating ⇒ premature commitment ⇒ a world starved that the pool could have finished ⇒ `S-budget` with named unpurchased gates, which `on_evidence_incomplete` reports. **Neither direction produces an admission withholding monotonicity did not already permit.** The mitigation is unchanged and so is its residual: candidate-authority units clamped at fit time, Theil–Sen over least squares, and at the `n = 3` floor the median tolerates exactly one bad point |
| the remaining budget | **Budget burn** ([vector 24](M2b-adaptive-scheduler.md#new-adversarial-vectors)) | **UNCHANGED IN KIND, and the revision does not worsen the lever.** A burner is committed only if its *predicted* finish is short, and prediction is per-kind and control-plane, so a burner cannot buy its way to the head of the commit order. Once committed it overruns exactly as it overran before, `C` re-forms at the next step from the shrunken pool, and the tail sheds. The clean close is still the deadline dispatch, still a change to `internal/oracle`'s contract, still named rather than half-done |
| that pass outcomes are gated on completability | **Suppress your own reachability** | **INCENTIVE-INCOMPATIBLE, same shape as M2b's self-suppression.** A world that makes itself look uncompletable is a world that is not bought, and an unbought hard gate fails, and `Decide` never names a failing world as `Subject`. Self-suppression is self-elimination — and it is `Decide` that makes it so, not the scheduler |
| `mvo explain --schedule` output | the whole protocol at once | **On the AG-7 deny-list, unchanged.** `commit_set`, `finish_ms` and `scarce` join every `schedule.*` payload key in `TestRenderPromptAIControlRule`'s forbidden set and in acceptance step 5g's live-race assertion over recorded agent context and transcript blobs |

**The fail directions are unchanged where they matter.** `Decide` fails closed. The scheduler fails open on unknown bounds — and the one term in this block that fails *conservative* ([decision 4](#decision-4)) has no unknown to fail on, because an unknown `finish_ms` makes the whole rule fall back rather than making a purchase look worthless.

**`scripts/adversarial.sh`'s 22-vector baseline must be re-recorded and must be IDENTICAL.** M2d measured that corpus at 0 drifts in 4 consecutive runs and vector 24 at 0 drifts in 12 runs alone, so a single moved verdict is a signal and not noise. A vector whose verdict moves blocks this block from shipping regardless of what §7's numbers say.

---

## 7. Pre-registration

**Written before any measurement is taken.** Every number below is a prediction against a published figure, with a direction, a size where one is derivable, and a falsifier. The falsifiers are stated as observations, not as judgments, so that no result can be argued into being a partial success after the fact.

### 7.1 The mechanical predictions, checkable from the trace alone and needing no labels

These are the ones the arithmetic in §1.1 forces. They are the strongest part of the pre-registration because they do not depend on the eval corpus, on labels, or on which candidate happened to be correct.

| # | prediction | derivation |
|---|---|---|
| **P1** | At `default / B2 = 1 529 ms`, `voc2` records **≥ 1 complete world** in **≥ ⌈2R/3⌉** replicates. Published `voc`: **0 complete worlds, 5/5**. | Σ finish = 1 965 > 1 529 ⇒ scarce; `C = {head}` since 982 ≤ 1 529 < 1 965; commitment invariant ⇒ every rung of the head world is affordable |
| **P2** | At the same cell, `voc2` spends **≈ 982 ms** (one full ladder) and buys **3 receipts**, stopping `S-budget` with ≈ 547 ms **unspent**. Published `voc`: 586 ms / 4 receipts. Published ladder: 1 284 ms / 5 receipts | after the head finishes, the rival's `finish_ms` 982 > allowance 547 ⇒ not completable ⇒ [decision 5](#decision-5)'s override lapses ⇒ flip 0 under the shipped default ⇒ declined `unreachable` |
| **P3** | At the same cell **under a policy declaring `on_evidence_incomplete`**, `voc2` buys the rival's prefix — **5 receipts, ≈ 1 275 ms, ESCALATE** — and agrees with the ladder | flip = 1 on the rival's guard: `fail-closed` excludes the rival from the predicate and turns ESCALATE into SELECT, so the purchase moves the decision |
| **P4** | `voc2`'s **evidence waste** at `default / B2` is **≤ the ladder's** at the same cell, and strictly lower under the shipped default | the 293 ms the ladder spends on a world it cannot finish is exactly what P2's decline refuses; [decision 18](M2b-adaptive-scheduler.md#decision-18)'s bracket substitution scores it waste for both arms |
| **P5** | `voc2` ≡ `voc` **byte-for-byte in receipt set, decision and rationale** at `--budget-oracle-ms 0`, and at B3 the **receipt set and decision are unchanged** (step *order* may differ) | [decision 1](#decision-1)'s gate; at B3 a scarce step yields `C` = everybody or a prefix whose complement is completable from `uncommitted`, so everything is still bought |
| **P6** | `schedule.step.scarce` is **`false` on every step** of every unbounded race and **`true` on ≥ 1 step** of every B1 and B2 race in the protocol | the definition; this is the regime boundary being legible rather than a result |
| **P7** | All **22 adversarial vectors** match the recorded baseline, 22/22, in 4 consecutive runs | §6 |
| **P8** | The M2b.1 `schedule / B1` and `schedule / B2` cells — published as **ties** (SELECT 5/5 vs SELECT 5/5 at 1 218 / 1 226 ms; and SELECT 5/5 vs SELECT 4/5 + ESCALATE 1/5, 20 % against a 20 % floor) — **remain ties**, with `voc2`'s spend within the published arms' IQR | these cells were already near the informative band's top; if scarce fires the commitment invariant only makes completion *more* likely, and both arms already completed |

### 7.2 The labelled predictions, against M2d's published table

Published (shipped default, R = 3, reference replicated 3×, B from the median minspend, `local-derived@v1`, five instance-slices over two independent bugs, coverage 3/5):

| level | adaptive TCAR | fixed-budget TCAR | paired (both / adaptive-only / fixed-only / neither) | adaptive `FRR_reachable` (family A) | FAR |
|---|---|---|---|---|---|
| **B1** | **1/5** | **2/5** | 0 / 1 / 2 / 2 | **2/2 = 100 %** | — |
| B2 | 2/5 | 2/5 | 2 / 0 / 0 / 3 | 0/2 | — |
| B3 | 2/5 | 2/5 | 2 / 0 / 0 / 3 | 0/2 | — |

| # | prediction | reasoning, including where it is weak |
|---|---|---|
| **P9** | **`FRR_reachable`(voc2, B1, family A) ≤ 1/2**, and the mechanism behind any remaining failure is **"finished the wrong world"**, never **"finished no world"** | `minspend ≤ B` and `B1 = ceil(median minspend × 1.1)`, so the winner's ladder is affordable; the commitment invariant guarantees *a* world is finished |
| **P10** | **`FRR_reachable`(voc2, B1) is NOT predicted to reach 0/2, and predicting it would be over-claiming.** At B1 the pool affords roughly one full ladder, so the arm gets **one attempt**, and on symmetric worlds nothing available at commit time distinguishes the correct candidate from a wrong one. Expected per-instance success ≈ 1/N over the rotation | this is the honest statement of what the fix does: it converts a **systematic** loss (finish nobody, always) into a **stochastic** one (finish the wrong one, sometimes). Removing the stochastic half needs a ranking signal on cheap rungs, which is [option (c)](#4-the-options-weighed) and is unlicensed |
| **P11** | **TCAR(voc2, B1) ≥ TCAR(voc, B1) = 1/5**, target **2/5** (matching the ladder), ceiling **3/5** (= coverage, A9's own ceiling) | the ladder reaches 2/5 by finishing worlds in the control-plane order; `voc2` under scarcity on symmetric worlds ranks identically |
| **P12** | **TCAR(voc2, B2) = TCAR(voc2, B3) = 2/5**, unchanged from both published arms | at B2/B3 the published arms already agree and already complete worlds; P5 constrains B3 |
| **P13** | The **`toyrepo-mean-C` @ B1 instance that adaptive currently wins alone** stays won | whatever completed a world there under `voc` completes it under `voc2`: the reservation is a superset of the conditions under which `voc` finished anything |
| **P14** | **FAR stays `—` for `voc2` at every level on families A and B** | on this corpus every wrong candidate fails the suite gate (all labelled `incorrect`, reason `f2p-fail`, `expectation_violated` 0), so finishing a wrong world eliminates it rather than admitting it. **This is the prediction most likely to be wrong, and P17 is its falsifier** |
| **P15** | The **relaxed-guard cell does not move**: both arms admit a declared laundering vector on both adversarial instances, FAR 1/1 on families A and B, `d*` the same on full evidence, census `WON-BY-S3-adversarial`, caption `+ADVERSARIAL(declared)` | that result is a property of `no-paths.json`'s empty protected-path set and `tests_passed_desc` ranking, measured since M1e. **If it moves, something other than the allocation rule moved** |
| **P16** | The **paired disagreement between `voc2` and the ladder falls to ≤ both arms' self-disagreement at every level**, i.e. is reported as a **tie** | §7.5 |

### 7.3 Falsifiers — results that mean the fix did not work

**Each of these is an observation, not a judgment. If one is observed, the block reports it in those words.**

| # | falsifier | what it means |
|---|---|---|
| **F-1** | **Any race in which the priced remaining ladder of some alive world fits the remaining pool and the arm records 0 complete worlds.** Checkable from the trace alone, no labels needed | the commitment invariant does not hold, so the fix's central mechanism does not work. **One occurrence is a failure.** Not "rare", not "an edge case" |
| **F-2** | `FRR_reachable`(voc2, B1, family A) **= 2/2**, unchanged | the metric that convicted the rule is unmoved. The fix did not work |
| **F-3** | TCAR(voc2) **<** the published TCAR(voc) at **any** level (1/5, 2/5, 2/5) | a regression. Reported as a loss, never as "a different point on the trade-off curve" |
| **F-4** | FAR(voc2) at any cell is **a number greater than FAR(ladder) at that cell** — including `—` → any positive value while the ladder stays `—` | the fix traded a false rejection for a false admission. Reported in the FAR column with the same prominence as the TCAR column, and the rule is reconsidered rather than shipped |
| **F-5** | Any of the **22 adversarial vectors** changes its recorded verdict | the fix opened an attack surface. Blocks the block regardless of every other number |
| **F-6** | The **unbounded null case moves** (accept 5b / 6a: receipts, decision or rationale differ from `--schedule=fixed`), or B3's **receipt set or decision** changes | [decision 1](#decision-1)'s gate leaks and the revision is not confined to the regime it claims |
| **F-7** | `voc2` spends **≤ 586 ms** at `default / B2` (i.e. it still fails to use money it has), **or** overruns `max_oracle_ms` by more than the ladder does at the same cell | the arm is not actually buying differently, or it is buying without the bound |
| **F-8** | The **withholding-monotonicity property test** fails with `voc2` in the generator's selector set | `passSet(R) ⊆ passSet(R*)` is the one structural safety result in the project. Nothing ships |
| **F-9** | The **freeze check does not refuse** on an unmodified `eval/freeze/local-derived-v1.json` | [decision 8](#decision-8) did not land, and the instrument still cannot see rule changes |

### 7.4 The protocol, declared before it is run

- `scripts/schedule-compare.sh --replicates 5 --level B2 --policy default`, **three arms** (`voc`, `voc2`, `ladder`), `--parallel 1`, arms interleaved, one seeded workspace copied per arm per replicate, rotation ρ = r mod N, host-load probe per arm, cost table asserted byte-equal across arms. Repeated at B1 and B3. This produces P1–P8.
- `scripts/eval.sh` on `local-derived@v1`, shipped default policy, **both `on_evidence_incomplete` configurations** ([F14](M2b1-budgeted-fixed-arm.md#f14)), R = 3, reference replicated 3× with B from the median `minspend`, arms `{A1 ladder, A2a voc, A2b voc2}` plus the derived arms unchanged. This produces P9–P16. **Cells: 3 budget levels × 2 policy configurations. No others, and no cell is re-scored.**
- Every table carries, unchanged: `ORACLE-BUDGET-MATCHED`, `SYNTHETIC-CANDIDATES`, the label tier, the source census, and `n = 5-INSTANCE-SLICES-OVER-2-INDEPENDENT-BUGS`. The instance floor of 30 still refuses a p-value and a confidence interval on all of it. **This is a diagnosis of a scheduling rule and it will be captioned as one.**

### 7.5 The null that would be honest

**On a corpus of symmetric two-world races with one linear ladder, the correct behaviour of a good allocator is the ladder's behaviour**, because there is nothing to allocate between. [M2b §1](M2b-adaptive-scheduler.md#1-what-v0-schedules-and-what-it-does-not) said as much for the unbounded case — *"the scheduler does nothing, visibly and by construction"* — and the finding this block answers is that under a **binding** budget the old rule did something actively harmful where it should have done nothing.

So the expected outcome of this fix on this corpus is: **`voc2` ≡ `ladder` within noise at every level.** That is a **success for the rule** and a **null for the thesis**, and both halves get published in the same sentence. It is pre-registered here so that a convergence result cannot later be dressed up as a win, and so that a small `voc2` win cannot be dressed up as evidence for CP-4 when the arms' self-disagreement covers it.

**And the anti-spin commitment, in writing.** If the measurement says `voc2` ties the ladder everywhere, the honest conclusion is that **CP-4's adaptive claim is not falsifiable on symmetric two-world fixtures**, and the next block is a corpus with asymmetric worlds — different ladder positions, different per-world costs, more than one decision-relevant rung at a gate level — **not another rule**. Adding such a fixture *after* seeing which rule needs it is forbidden by [§5.3](#53-what-is-forbidden-in-this-block-in-writing); it is specified as its own block, with its own pre-registration, and its instances never enter the cells above.

---

## 8. Wire deltas, acceptance, testing bar

```go
// schedule.Considered — THREE additive observational fields.
type Considered struct {
    // … M2b/M2b.1 fields unchanged …
    Committed  bool   `json:"committed"`   // world ∈ C at this step
    FinishMS   int64  `json:"finish_ms"`   // Σ ĉost over this world's UNBOUGHT ladder; 0 ⇒ unknown
    ScoreBasis string `json:"score_basis"` // "rung" | "finish"; "" on a ladder row ⇒ renders "—"
}

// schedule.Step — THREE additive observational fields.
type Step struct {
    // … unchanged …
    CommitSet     []string `json:"commit_set"`     // world digests, in commit order
    Scarce        bool     `json:"scarce"`         // decision 1's regime, and decision 4's precondition
    UncommittedMS int64    `json:"uncommitted_ms"`
}

// schedule.Constants — ONE additive field, read by the M2d freeze check (decision 8).
type Constants struct {
    // … unchanged …
    AdaptiveRule string `json:"adaptive_rule"` // the BINARY's default rule: "voc" | "voc2"
}
```

**Everything else: nothing.** No receipt field, no policy field, no ranking key, no gate predicate, no metric, no oracle kind, no escalation rule, no `Decide` input, no ledger event, no new stop clause, no new scheduler constant. `Receipt`, `World`, `Intent`, `Decision` and `PolicyV1` are untouched, and the shipped default policy digest does not move.

**`scripts/accept.sh`**, six steps (`m2b2-8a…8f`); none changes the shipped default:

- **8a — the finishing invariant, as a race.** At a budget that can finish exactly one world and not two: assert ≥ 1 world holds a receipt for every hard gate, the stop is `S-budget`, and `schedule.step.commit_set` names that world. **This is F-1 as a test, and it is the block's failing-test-written-first.**
- **8b — the gate is inert.** `--selector=voc2 --budget-oracle-ms 0` against `--selector=voc` and against `--schedule=fixed`: receipt set, decision and rationale byte-identical, and `scarce: false` on every step.
- **8c — absent is absent.** A ladder row renders `—` for `finish_ms`/`committed`; a pre-M2b.2 trace renders `unknown (pre-M2b.2 trace)` and never `false`/`0`/`[]`; a `voc2` row under `¬scarce` carries `score_basis: "rung"` and its `finish_ms` is rendered as a measurement, not as the denominator.
- **8d — the reason strings are honest.** No bought row carries a decline reason; no `hard_gate: true` row carries the `decision-inert` sentence; the `reserved` and `unreachable` sentences are mutually exclusive and each names the arithmetic that produced it; every unbought rung of a starved world has its `oracle.skipped`.
- **8e — the freeze refuses.** `mvo-eval score --split eval` against the **unmodified** freeze file refuses, naming `adaptive_rule: "voc" -> "voc2"`, and succeeds with `--unfreeze "<reason>"` while appending the reason, the diff, the timestamp and one eval-use line.
- **8f — AG-7 under live race.** No `commit_set`, `finish_ms`, `scarce` or allowance figure appears in any world's captured agent context or transcript.

Plus, unchanged in force: every M0–M2d ledger replays byte-for-byte, `mvo audit` is OK over a ledger carrying the new fields, `adversarial.sh` is 22/22 against the recorded baseline, and the shipped default digest does not move.

**Testing bar.**

- `internal/schedule` (finish): `finish_ms` over hand-built ladders including the unpriced-fallback path; the scarcity test's boundary at exact equality; **the commitment invariant as a property test** — over generated policies, cost tables, budgets and world sets, for every step and every `w ∈ C`, every remaining rung of `w` is affordable; determinism of the commit order under permuted input order.
- `internal/schedule` (selector): **`SelectorVOC.Allowances` bit-identical to `budget.share(len(frontier))` for every frontier** (the retained arm is M2b, provably); `SelectorLadder.Allowances` bit-identical to `budget.share(1)` (the reference arm did not move); `voc2 ≡ voc` under `¬scarce` on generated inputs, receipt set and order both.
- `internal/schedule` (value): [decision 4](#decision-4)'s reachability condition — a bracket over an uncompletable world contains `fail-closed` only; the existing bracket-clamp invariant extended so **no value entering `finish_ms`, the scarcity test, the commit order or the allowance is derived from a candidate-authored metric**.
- `internal/schedule` (safety): the **withholding-monotonicity property test with `voc2` in the selector set** — `passSet(R) ⊆ passSet(R*)`, unchanged and non-negotiable.
- `internal/schedule` (waste): the starved race's waste under `voc2` against the same race under the ladder, pinning P4's direction on a recorded fixture.
- `internal/eval` (freeze): absent `adaptive_rule` normalizes to `"voc"`; the refusal names what moved; `--unfreeze` appends exactly one reason line and exactly one eval-use line.
- `cmd/mvo`: `explain --schedule` human and `--json` goldens for {scarce race, non-scarce race, unpriced fallback, ladder race, pre-M2b.2 trace}; the regime header line; **rows are rendered, no score is recomputed** (step 5d extended).
- `gofmt -l` clean; `go vet ./...` and `go test ./...` green **with no docker daemon, no network, no corpus, and with none of hypothesis, mutmut or cosmic-ray installed**; `go.mod`/`go.sum` unchanged; no test invokes a real agent CLI.

---

## 9. Which M2b/M2b.1 decisions this supersedes, and which survive

**Superseded, in whole or in part:**

| decision | what happens to it |
|---|---|
| [M2b decision 8](M2b-adaptive-scheduler.md#decision-8) — equal shares | **SUPERSEDED under scarcity** by [decision 3](#decision-3)'s reservation. It survives verbatim as the `C = ∅` case and as the whole `¬scarce` regime. Its containment justification was already withdrawn by measurement (§1.3) |
| [M2b §2.8](M2b-adaptive-scheduler.md#28-the-score-in-integers-with-deterministic-ties) — the score's denominator | **AMENDED under scarcity** by [decision 2](#decision-2). `score_basis` records which denominator ran; `value_bp` is unchanged |
| [M2b decision 3c](M2b-adaptive-scheduler.md#decision-3c) — the hard-gate override | **AMENDED at one point** by [decision 5](#decision-5): the override applies to worlds the budget can finish. All three red-team fixes it encodes are intact, because all three occurred at budgets that could finish the world |
| [M2b decision 3](M2b-adaptive-scheduler.md#decision-3) — the bracket | **AMENDED** by [decision 4](#decision-4): pass outcomes are conditioned on reachability. `fail-closed` and the clamp are untouched |
| [M2b.1 decision 1](M2b1-budgeted-fixed-arm.md#decision-1) — the `Selector` seam | **EXTENDED, not superseded**: `Contenders(frontier) int` becomes `Allowances(frontier, pool) []int64`, with both existing arms' behaviour pinned bit-identical by test |

**Surviving unchanged, and load-bearing here:** [M2b decision 1](M2b-adaptive-scheduler.md#decision-1) (the scheduler holds a reference to `Decide`, never a copy); [decision 2](M2b-adaptive-scheduler.md#decision-2) (the frontier is one rung per alive world in policy gate order — **every alive world still appears in `considered`**, which is what makes deferral auditable); [decision 3b](M2b-adaptive-scheduler.md#decision-3b) (the control-plane clamp); [decision 4](M2b-adaptive-scheduler.md#decision-4) (withholding monotonicity, at exactly its amended strength — §3.2); [decision 5](M2b-adaptive-scheduler.md#decision-5) (max not product, substitutes only); [decision 6](M2b-adaptive-scheduler.md#decision-6) (`executor_bp = 5 000`, still hand-set, still owed a sweep); [decision 7a–d](M2b-adaptive-scheduler.md#decision-7) (unit authority, Theil–Sen, no-fit-no-number, median-fixed); [decision 9](M2b-adaptive-scheduler.md#decision-9) (the exhaustion stop rule, no new clause); [decision 10](M2b-adaptive-scheduler.md#decision-10) (no repurchase, ever); [decision 11](M2b-adaptive-scheduler.md#decision-11) (research rows, excluded from waste); [decision 12](M2b-adaptive-scheduler.md#decision-12) (the budget is an intent field, `0` ⇒ unbounded); [decision 13](M2b-adaptive-scheduler.md#decision-13) (determinism over the recorded tuple — which gains **no new member**, since finish costs come from the already-recorded cost-table snapshot); [decision 14](M2b-adaptive-scheduler.md#decision-14) (`on_evidence_incomplete`, still off, still pure, still not promoted); [decision 15](M2b-adaptive-scheduler.md#decision-15) (`wall_ms_asc` refused, validation rule 25); [decision 16](M2b-adaptive-scheduler.md#decision-16) (the prompt ordinal prerequisite — **now with a second reason**, §6); [decision 17](M2b-adaptive-scheduler.md#decision-17) (the trace is observational, recorded never recomputed); [decision 18](M2b-adaptive-scheduler.md#decision-18) (evidence waste by bracket substitution, definition unchanged); [§8 row 8](M2b-adaptive-scheduler.md#8-what-the-scheduler-cannot-know-yet) (**no rank-based elimination**, reaffirmed with the licence question answered in §4c); [M2b.1 decisions 2–10](M2b1-budgeted-fixed-arm.md#2-the-budgeted-fixed-arm) (the arm labels, the control-plane world order and rotation, the partial world, one pool/one predicate/one charge point, `--budget-basis`, absent-is-absent, k = 1, rule 25's widening, R ≥ 3 and the anecdote rule, the retrospective allocation bound); [M2d decisions 1–12](M2d-labelled-evaluation.md) **in their entirety** except the freeze check's new field.

---

## Explicitly NOT in M2b.2

Rank-based elimination and any form of successive halving that prunes (§4c — still unlicensed, and M2d did not measure the correlation that would license it); promoting `on_evidence_incomplete` to the shipped default ([decision 7](#decision-7) — it waits on the base rate this block's protocol measures); per-world spend accounts and the deadline dispatch that would close the [starved-admission residual](M2b-adaptive-scheduler.md#residual-starved-admission) (still a change to `internal/oracle`'s contract); scheduling the cohort reducer or charging phase B2 (`finish_ms` covers scheduled world-stage rungs only, the same boundary the allocation bound draws); the strong budget-aware lookahead (§4d — refused on measured metalevel cost, retained as a diagnostic); any sequential statistical test, e-value or e-process; learned correlation, learned cost models, a calibrated `P(correct | receipts)`, the measured co-failure matrix, or the `executor_bp` sweep (**each needs a labelled stream over REAL candidates, which nothing in this block produces**); generation and mutation as scheduled actions; the adversarial challenger as an arm (AG-6); `race --reverify <world-digests>` ([F2](M2b1-budgeted-fixed-arm.md#f2)'s close, still a contract change, still the largest threat to any cross-arm number once generation is live); removing the generator prompt's `"candidate k of N"` (now required by **three** separate findings and still not done here); any new eval instance, split, label, metric or scoring change (§5.3); an asymmetric-ladder fixture — specified as its own block with its own pre-registration, and barred from these cells (§7.5); the paper.
