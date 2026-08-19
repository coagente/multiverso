# M2d.1 — The Instrument Can Test: warming, the refusal, and coverage

> **This block exists because of one sentence in [M2b.2](M2b2-finishing-rule.md)'s own BUILDLOG entry, and the sentence is about the instrument rather than about the rule.** *"`mvo-eval` builds a fresh workspace per instance, so no oracle kind has a local fit, `finish_ms` is UNKNOWN, and [decision 1](M2b2-finishing-rule.md#decision-1)'s fallback fires on every step … every cell is identical between the two rules."* And the corpus has the same hole from the other side: `scripts/adversarial.sh` builds a fresh workspace per vector, so nothing is priced, and **21 of the 22 vectors carry no budget at all**. So `adversarial 22/22` and `FAR unchanged` are **true statements about `voc`**, and they carry no information about the rule M2b.2 added.
>
> **The generalization is worse than the instance, and it is why this is its own block.** The harness cannot currently test *any* allocation rule. Not `voc2`, not its successor, not a per-world cost signal, not an asymmetric corpus. Every future scheduler measurement is vacuous in the same way, and the vacuity is invisible: a run at zero coverage prints exactly what a run at full coverage prints.
>
> **Three things ship, and the second matters most.**
>
> 1. **WARMING** ([§2](#2-warming)) — `mvo-eval` prices the cost table per instance the way [`schedule-compare.sh`](../../scripts/schedule-compare.sh) has since [M2b.1](M2b1-budgeted-fixed-arm.md), by racing an unbudgeted warm-up into a **template** that every arm and every replicate then inherits by copy. Warming is a **predicate on the cost table**, not a race count; it is charged to no arm; it is amortized across arms, replicates and budget levels; and what it cost is recorded.
> 2. **THE REFUSAL** ([§3](#3-the-refusal)) — the harness **refuses to report a comparison between two rules when the rule under test provably never fired**. Not a caveat, not a footnote: a named non-verdict, an exit code, and a gate that fails. This is the project's own principle — *an unrun check is not a passed check* — turned on its own instrument. It is asserted in three harnesses, and `scripts/accept.sh` gains a **pair** of steps: the refusal must fire on the exact configuration that produced M2b.2's vacuous claim, and must **not** fire on the warmed one, because a refusal that fires on every run is the rubber stamp [M2b.2 F-9](M2b2-finishing-rule.md#73-falsifiers--results-that-mean-the-fix-did-not-work) already named.
> 3. **COVERAGE AS A REPORTED NUMBER** ([§4](#4-coverage-as-a-reported-number)) — every comparison and every corpus run prints what fraction of its steps exercised the rule under test, per mechanism, always, including at 100 %. A run at 0 % measured nothing and is printed where a failure is printed.
>
> **Scope fence, and it is absolute.** This block **does not revise the allocation rule**: `AdaptiveRuleDefault` stays `voc`, `voc2` stays behind `--selector`, [accept step m2b2-8e](M2b2-finishing-rule.md#8-wire-deltas-acceptance-testing-bar) stays exactly as it is, and **M2b.2's verdict is not re-opened**. It **takes no labelled measurement on the eval split**: the demonstration runs on the **dev half** the split file already assigns, so the published leaderboard-query count does not move ([M2d decision 6](M2d-labelled-evaluation.md#decision-6)). It makes the instrument capable and honest; the measurement is the next block's, and [§9](#9-what-this-block-does-not-do-and-the-blocks-it-names) names it.
>
> `Decide` stays pure and total and gains nothing. Replay stays exact; every M0–M2d ledger replays byte-for-byte. The [M1f](M1f-trust-boundary.md) rule is absolute and is re-derived in [decision 4](#decision-4) rather than inherited. Absent source implies absent metric — which here means **a pre-M2b.2 ledger's coverage is `unknown`, never `0`**. Stdlib only; `go.mod`/`go.sum` untouched; POSIX; **ZERO AGENT SPEND**.

## Module layout (delta over M2b.2)

```
internal/schedule/trace.go       +allowance_ms, +pass_withheld on Considered.
                                 TWO additive observational fields. Nothing else
                                 on the wire moves.
internal/schedule/coverage.go    NEW, PURE: the inertness predicates, the exercise
                                 witnesses, and Coverage(trace) -> Report. DERIVED,
                                 never recorded (decision 8)
internal/eval/ledger.go          LedgerView gains a RACE WINDOW: View.Race(intent).
                                 Every consumer that means "this race" says so
internal/eval/warm.go            NEW: the warm predicate, the template cache keyed on
                                 (instance, policy digest), the recorded warm report
internal/eval/run.go             Race() may be handed a warmed TEMPLATE instead of a
                                 bare repo; copyTree stops skipping .multiverso when
                                 the source is a template
internal/eval/corpus.go          FreezeFile gains the `instrument` block; ABSENT
                                 normalizes to {warmup 0, cold, actual} EXACTLY
internal/eval/metrics.go         `cost_regime` joins the cell key beside `policy`;
                                 no metric definition moves
cmd/mvo-eval/run.go              --warmup {auto|N|0}; the coverage block; the VACUOUS
                                 refusal and exit 5
scripts/eval.sh                  --warmup passthrough; maps exit 5; prints coverage
                                 above the metrics
scripts/schedule-compare.sh      the coverage block per arm, the paired purchase-order
                                 divergence, the VACUOUS banner and exit 5
scripts/adversarial.sh           per-vector coverage facets; `.budget` and `.warm`
                                 sidecars honoured; --require-coverage
testdata/adversarial/report.py   coverage block per vector; the summary stops
                                 reporting 22/22 alone; baseline carries coverage
testdata/adversarial/vectors/    22 and 23 gain .budget sidecars; 22, 23, 24 gain
                                 .warm sidecars; baseline RE-RECORDED as a declared
                                 change with the old rows printed beside the new
scripts/accept.sh                +5 steps (m2d1-9a…9e), including the refusal PAIR
```

**Untouched, and a diff that touches them is wrong:** `internal/race`'s `Decide`, `internal/policy`, `internal/object`, `internal/admit`, `internal/audit`, `internal/oracle`, `internal/schedule/{selector,finish,budget,value,schedule}.go` (the allocation rule itself), `internal/eval/{derive,label,leak,score,arms}.go`, `eval/corpora/*`, `eval/splits/*`. The eval harness gains no instance, no candidate, no label, no metric and no scoring change.

---

## 1. What is vacuous, measured on this host

Measured with the committed binary on darwin/arm64, `pytest 8.4.0`, no docker, `--parallel 1`, `testdata/toyrepo` (2 candidates), `--selector=voc2`, budget 1 500 ms. Every number below is reproducible with `mvo` alone.

| warm-up races | samples per kind at fit time | cost table | `commit_basis` per step | `scarce` | spend / bound | stop |
|---|---|---|---|---|---|---|
| **0–1** | 2 (< `MinSamples` = 3) | all `declared-rank` | `unpriced-fallback(pytest-collect,pytest-suite,tree-guard)` on **6 of 6** | false on 6 of 6 | **2 164 / 1 500 ms** | `S-empty` |
| **2** | 4 | all `fit` | `reserved` on **6 of 6**, \|C\| ≥ 1 on 3 of 6 | true on 6 of 6 | **1 380 / 1 500** | `S-budget` |
| **3** | 6 | all `fit` | `reserved` on 6 of 6 | true on 6 of 6 | 1 370 / 1 500 | `S-budget` |

**Two separate failures, and the second was never named.** The first is M2b.2's: with no fit the revision falls back to M2b's rule on every step and the two rules are one rule. The second is that **on a cold workspace the budget does not bind**. [M2b.1 decision 5](M2b1-budgeted-fixed-arm.md#decision-5) makes an unpriced purchase affordable while any pool remains, so with *every* rung unpriced the arm buys until the pool crosses zero: measured **2 164 ms spent against a 1 500 ms bound, a 44 % overrun, stopping `S-empty` rather than `S-budget`** — it bought the entire ladder for both worlds. A cell that cannot bind its own budget is not an oracle-budget-matched cell in the sense its caption claims, whichever rule allocates it.

**And with two warm-up races the two rules stop being one rule.** One seeded template, copied per arm, one intent, B = 1 500 ms:

| selector | purchase order (world, rung) | spend | decision | coverage |
|---|---|---|---|---|
| `voc` | w1.guard, w2.guard, w1.collect, w2.collect | 647 ms | **REJECT** | `commit_basis` `""` and `score_basis` `rung` on **every** row: **0 of 5 steps** |
| `voc2` | w1.guard, w1.collect, **w1.suite**, w2.guard, w2.collect | 1 361 ms | **SELECT** | `reserved` **6 of 6**, \|C\| ≥ 1 on **3 of 6**, `score_basis` `finish` on every row |

That is §1.1's dead stop and its repair, on this host, in one command each — and none of it is reachable from `mvo-eval` or from the adversarial corpus as they stand. **`scripts/eval.sh` and `scripts/adversarial.sh` are the two harnesses that gate this repository's scheduler claims, and both of them run the left column.**

**The corpus, per vector, structurally.** 21 of 22 vectors carry no `.budget` sidecar, so their races are unbounded, so `max_oracle_ms` is read never and no allocation rule can differ. The 22nd (`24-schedule_budget_burn`, B = 400 ms) carries a budget and a cold table, which is the first row of the table above. Two of the three vectors *named* for the scheduler — `22-schedule_cost_poison` and `23-schedule_starvation` — carry **no budget**, and vector 22 additionally attacks a **cost fit that does not exist** in the workspace it runs in. It is vacuous twice over.

---

## 2. Warming

<a id="decision-1"></a>
> **Decision 1. Warming is a PREDICATE ON THE COST TABLE, not a count of races. `--warmup auto` warms until every kind the pinned policy can buy carries a local fit, or refuses by name.**
>
> ```
> warm(workspace, policy) ⟺ ∀ kind k with declared_by_policy(k) ≠ ∅ :
>                              measurement(k) ≠ null           (i.e. n ≥ MinSamples)
> ```
>
> The predicate is read from **`mvo oracles --json --policy <name>`**, which already reports `declared_by_policy`, `measurement_n`, `measurement` and a `measurement_note` naming the shortfall (*"no local measurement (n=0, need 3)"*). It is a read-only verb over the workspace's own ledger and it costs no race, so the loop is *race → re-read → stop*.
>
> **Why not a count.** A count is wrong in both directions and the corpus proves both. Each warm-up race contributes **one sample per kind per world that actually reaches that rung**, so on `testdata/toyrepo` (2 candidates) one race yields 2 < 3 and the table stays `declared-rank` — measured above — while on `advrepo-split-A` (8 candidates) one race yields 8 and two would be waste. And a world that dies at rung O-1 contributes **nothing** to the kinds behind it: under the shipped default eleven of the twelve laundering vectors stop at `paths-unmodified@guard` with one receipt each ([M2d §3 S3](M2d-labelled-evaluation.md#3-candidate-sources-with-zero-agent-spend)), so a "two races is enough" rule prices the guard and leaves the suite unpriced, and the fallback fires anyway on the kind that matters.
>
> **The cap and the refusal.** `auto` stops at **3** races and, if the predicate still fails, records `warm_incomplete` naming the kinds that stayed unpriced, and the instance's coverage is **0 by construction and reported as such** — not silently raced as though it were warm. `--warmup N` pins a count for a reader who wants one; `--warmup 0` is the cold instrument, kept because [accept step m2d1-9a](#8-wire-deltas-acceptance-testing-bar) needs to reproduce the vacuum on purpose.
>
> **`--budget-basis=predicted` is NOT an alternative route to a priced table, and M2b.2 §7.6 lists it as if it were.** Measured: on a cold workspace it **refuses at pre-flight** — *"needs a fitted cost for every buyable kind; this workspace has no local measurement for pytest-collect, pytest-suite, tree-guard"*. It is a *consequence* of warming, not a substitute for it, and it becomes available only after decision 1's predicate holds.

<a id="decision-2"></a>
> **Decision 2. Warming happens ONCE per (instance, policy digest) into a TEMPLATE, and every arm and every replicate inherits it by COPY. The cost table is a property of the host and the repository, not of the arm.**
>
> This is [M2b.1 F3](M2b1-budgeted-fixed-arm.md#f3) verbatim, and `schedule-compare.sh` has enforced it since M2b.1: *"One workspace is seeded (`mvo init` + warm-up races), the intents are created, and the directory is then **copied** per arm per replicate, so the ledgers that feed `costSamples()` are byte-identical."* `mvo-eval` copies a bare repository and runs `mvo init` per race, so today every arm builds its own table — trivially equal because all of them are empty, and **not equal at all** the moment anything is priced.
>
> | key member | why it is in the key |
> |---|---|
> | instance | the fixture's own timings; a different repository prices differently |
> | **policy digest** | `costSamples()` admits a receipt only when the pinned policy declares its `Oracle.Config`, and the fit is keyed on the **seal** (`plugin_autoload`), which is a policy field — [M2a amendment 27](M2a-oracle-menu.md#decision-27) measured it as a **4.4× lever on fixed cost**. Reuse across policies is **refused rather than reasoned about**, at the cost of one extra warm-up per policy |
> | binary digest | the racing binary is what took the measurements ([F15](M2b1-budgeted-fixed-arm.md#f15)) |
>
> **Amortization, with the assumptions visible.** Measured warm-up cost is **≈ 2.8 s** per race on `testdata/toyrepo` at 2 candidates and **≈ 1.55 s** at 1 candidate on `testdata/adversarial/repo`, of which the oracle ladder is ≈ 0.98 s per candidate and the rest is process and workspace setup. `local-derived` carries 4–8 candidates per instance, so **one race, ≈ 5–9 s, warms an instance**. A full protocol cell already races 3 reference replicates + 2 arms × R = 3 replicates = **9 races per instance**, and there are 8 distinct (instance, policy) pairs across the six declared cells. So warming costs **≈ 56 s against a protocol already measured in tens of minutes — under 4 %** — *if* it is amortized, and **9×** that if it is not. The amortization is the whole reason decision 2 is a decision.
>
> **Determinism under replication, stated at its real strength.** Copying makes the table **byte-identical across every arm and every replicate of one protocol run**, which is the property the comparison needs and the one `schedule-compare.sh` already asserts. It does **not** make the table identical across *protocol runs* — it is a wall-clock fit and the host drifts — so `cost_table` is recorded in `schedule.started` (it already is), its digest is recorded in the run manifest, and **the harness asserts byte-equality across arms and aborts otherwise**, which `mvo-eval` does not do today and must. Full determinism of the allocation additionally needs `--budget-basis=predicted` ([M2b.1 decision 5b](M2b1-budgeted-fixed-arm.md#decision-5b)); it is **not** made the default here, because under a copied table plus `predicted` the replicate self-disagreement collapses to the rotation effect, and self-disagreement is the noise floor every verdict in this project is measured against ([M2b.1 decision 9](M2b1-budgeted-fixed-arm.md#decision-9)). It ships as a declared second configuration and is named in [§9](#9-what-this-block-does-not-do-and-the-blocks-it-names).

<a id="decision-3"></a>
> **Decision 3. `LedgerView` gains a RACE WINDOW, and this is the precondition of warming rather than a convenience. The window narrows what is MEASURED; it must never narrow what is SCANNED.**
>
> A warmed template's ledger holds the warm-up races, and every arm workspace is a copy of it. `internal/eval`'s `LedgerView` is **workspace-wide**: `ReadLedger` collects every world, every receipt and the last decision in the file. Every consumer silently assumes one race per workspace, and each one breaks differently once that is false:
>
> | consumer | what it would report | why it matters |
> |---|---|---|
> | `oracleSpend(view)` | Σ over **every** receipt in the workspace | the arm's spend would carry the warm-up's, and the arms would stop being budget-matched **in the report** even though they are matched **in the pool** |
> | `schedule.Bound(view.Worlds, view.Receipts)` | a prefix-closed enumeration over two races' receipts | `minspend` derives **B**, the experiment's independent variable |
> | `view.DStar()`, `view.Winner()` | the last decision over a merged world set | the full-evidence decision every FRR metric is defined against |
> | `Scorer.ScoreBatch(view, …)` | scores the warm-up worlds too | triples the scoring wall clock and inflates the batch |
>
> So `View.Race(intent)` scopes to the events at or after that intent's `race.started`, exactly as `schedule-compare.sh`'s `arm_report` already does in prose — *"A workspace holds many races (the warm-ups are in this very ledger) and numbers assembled across two of them would describe a race nobody ran."* The shell harness has had this notion since M2b.1; this lifts it into the code that will need it.
>
> **And the asymmetry is a rule, not an implementation detail.** The four [leak detectors and the canary](M2d-labelled-evaluation.md#decision-5) keep scanning the **whole workspace**, including the warm-up races. A detector that respected the window would stop looking at precisely the races nobody is reading — which is where a leak would be least likely to be noticed. `WorkspacesScanned` counts the template's descendants as it counts everything else.
>
> **The ledger is not edited.** No truncation, no rewrite, no "warm-up events excluded from the chain". The ledger is append-only and hash-chained, `mvo audit` must stay OK over a warmed workspace, and every M0–M2d ledger must still replay byte-for-byte. The window is a **read**, and reads invalidate nothing.

<a id="decision-4"></a>
> **Decision 4. The warm set is the instance's PUBLIC CANDIDATE SET, the warm intent carries `--budget-oracle-ms 0`, and the arm's pool never sees a warm-up millisecond.**
>
> **Charging.** The budget is an intent field and the pool is per-race ([M2b decision 12](M2b-adaptive-scheduler.md#decision-12), [M2b.1 decision 5](M2b1-budgeted-fixed-arm.md#decision-5)). A warm-up is a *different intent* and a *different race*, so its spend is structurally outside every arm's pool — there is no accounting to get right, only a report to get right, and [decision 3](#decision-3) is what gets the report right. The warm-up's own spend, race count and resulting table digest are recorded in the run manifest as `warmup: {races, spend_ms, table_digest, kinds_fitted[], kinds_unfitted[]}`, because an uncharged cost that is also unreported is a cost nobody can audit.
>
> **What the warm set is, and why it is not something new.** It is the **public projection's candidate set** — the same patch bytes the arms race, already in the workspace, already handed over by `HandoffPatches`. No new bytes cross the hidden/public boundary, so **D1–D5 and the non-consultation witness are unchanged**, and the one trap this could have walked into is closed for free: on a **family-B** instance gold is not in the public candidate set at all ([M2d decision 8b](M2d-labelled-evaluation.md#decision-8b)), so warming can never plant gold in a workspace where gold was withheld — the case [D3](M2d-labelled-evaluation.md#decision-5) exists to catch.
>
> **M1f, re-derived rather than inherited, because a new input to affordability is a new input to the trust boundary.**
>
> | fact | authored by | consequence |
> |---|---|---|
> | which patches are warmed | the instance's public projection — control plane | a candidate cannot choose to be warmed or not |
> | how long a warm-up rung took | the **candidate's own process**, for candidate-process kinds | this is the exposure, and it is named below |
> | which kind that timing prices | `Table.Predict` keyed on `(kind, units)` — **per kind, never per world** | a slow candidate raises the price of a rung for **everybody**, including itself |
> | units in the prediction | control-plane denominators, candidate-authority units clamped at fit time | [M2b amendment A](M2b-adaptive-scheduler.md#amendment-a), vector 22 |
> | which table each arm allocates against | one template, copied | **identical across arms by construction**, asserted byte-equal |
>
> **The exposure, stated at full strength and then bounded.** Warming on the candidate set hands candidates the same lever on the cost table that [vector 22](M2b-adaptive-scheduler.md#new-adversarial-vectors) already names: understate the fit and `scarce` reads false when it is true; overstate it and the pool commits prematurely. Three things bound it and none of them is new. (i) It is **exactly the product's own exposure** — `costSamples()` fits over the workspace's past receipts, which in any real workspace are past candidates' receipts; an experiment that warmed on synthetic timings would be measuring a scheduler nobody runs. (ii) The fit is **per kind and shared**, so the lever is not steerable toward a world: a candidate cannot make *itself* cheap to finish, which [M2b.2 §6](M2b2-finishing-rule.md#6-the-scheduler-as-attack-surface-what-the-revision-adds) already records as NOT AVAILABLE. (iii) The table is **copied**, so whatever poison lands is **symmetric across the arms**, and a comparison is a difference. The mitigations are unchanged — clamped units, Theil–Sen over least squares, and at the `n = 3` floor a median that tolerates exactly one bad point — and so is the residual.
>
> **The adversarial corpus takes the opposite default, for the opposite reason** — see [decision 14](#decision-14): a vector is *not* a fair sample of what the workspace has raced, so there the warm set is the **honest control**, with vector 22 declaring itself by sidecar because pricing itself is its declared mechanism.

**What warming does NOT fix, said here so no reader has to discover it.** `Table.Predict` holds nothing per world, so on symmetric worlds `finish_ms` is identical at equal ladder positions and the commit order falls through to `st.OrderIndex` — [M2b.2 §7.5](M2b2-finishing-rule.md#75-the-null-that-would-be-honest) measured exactly this and withdrew the adaptivity claim on the strength of it. **Warming removes the vacuum; it does not create signal.** A fully warmed, fully covered protocol on a symmetric two-world corpus is still expected to reproduce the ladder, and that is a real null, not a failure of this block. The per-world cost signal and the asymmetric corpus stay where M2b.2 put them: [§9](#9-what-this-block-does-not-do-and-the-blocks-it-names).

---

## 3. The refusal

<a id="decision-5"></a>
> **Decision 5 (the load-bearing one). Every arm declares an INERTNESS PREDICATE over RECORDED fields — the condition under which it is provably a named baseline — and the predicate is pinned by a property test rather than believed.**
>
> ```
> inert(arm, step)  — a predicate over the recorded schedule.step and its rows
> baseline(arm)     — the arm this one collapses to when inert
> exercised(step)   ⟺ ¬inert(arm, step)
> coverage(race)    = |{ steps : exercised }| / |steps|
> ```
>
> | arm | collapses to | inert on a step when (recorded) | pinned by |
> |---|---|---|---|
> | `voc2` | `voc` | `commit_basis ∈ {"", not-scarce}` **or** `commit_basis` has prefix `unpriced-fallback` | *"`voc2 ≡ voc` under `¬scarce` on generated inputs, receipt set and order both"* — [M2b.2's testing bar](M2b2-finishing-rule.md#8-wire-deltas-acceptance-testing-bar), already green |
> | `voc` | the M1 exhaustive ladder | no row has `admissible ∧ ¬affordable`, and the race's stop is not `S-budget` | [M2b decision 13](M2b-adaptive-scheduler.md#decision-13)'s null case, accept step 5b |
> | `ladder` | the M1 exhaustive ladder | same | [M2b.1 decision 2](M2b1-budgeted-fixed-arm.md#decision-2), accept step 6a |
>
> **This generalizes past `voc2`, which is the point.** The second row catches a vacuity nobody has named: **a budgeted comparison at a budget that never binds is a comparison of two arms that are both the exhaustive ladder**. That is measurably what a cold `mvo-eval` cell was ([§1](#1-what-is-vacuous-measured-on-this-host): 2 164 ms spent against a 1 500 ms bound, stop `S-empty`), and nothing in the harness said so. Any future rule ships with its own row, its own baseline and its own pinning test, or it does not ship.
>
> **The predicate is read off the LEDGER, never asked of the arm.** This is `reservesPerWorld`'s discipline held one level up — *"It reads the apportionment the arm produced rather than asking the arm what kind of arm it is … the one thing it may key on is the number the rule handed it."* A rule that could self-report its own coverage would be a rule grading its own homework, which is the failure this whole block exists to remove.
>
> **The four EXERCISE WITNESSES, one per mechanism M2b.2 added, all computable from recorded fields.** Reporting one aggregate would hide the state M2b.2 actually produces, in which the denominator moved on every step and the reservation reserved on half of them:
>
> | witness | decision it belongs to | recorded condition |
> |---|---|---|
> | **W2** — the denominator moved | [2](M2b2-finishing-rule.md#decision-2) | ∃ row with `score_basis == "finish"` |
> | **W3** — the reservation reserved | [3](M2b2-finishing-rule.md#decision-3) | `\|commit_set\| ≥ 1` (with `C = ∅` the allowance **is** `budget.share`, i.e. M2b decision 8 exactly) |
> | **W4** — a pass outcome was withheld | [4](M2b2-finishing-rule.md#decision-4) | ∃ row with `pass_withheld == true` |
> | **W5** — the hard-gate override lapsed | [5](M2b2-finishing-rule.md#decision-5) | ∃ row with `hard_gate ∧ ¬admissible` — **impossible under `voc`**, where `admissible = flip ∨ hard_gate` |
>
> Measured on [§1](#1-what-is-vacuous-measured-on-this-host)'s warmed race: **W2 on 6 of 6 steps, W3 on 3 of 6, W4 and W5 on none.** The witnesses are independent and the report says so; a single "62 %" would have hidden that the reservation stopped reserving the moment the head world completed, which is [decision 5's amendment](M2b2-finishing-rule.md#decision-5) doing exactly what it says.
>
> **`commit_basis: reserved` names the REGIME, not the act.** A scarce step with `|C| = 0` records `reserved` and allocates by equal shares. The wire vocabulary is **not** changed here — moving a recorded field's meaning would move every golden and is not this block's business — and coverage reports `|commit_set|` separately so no reader can mistake one for the other. Named for a later block in [§9](#9-what-this-block-does-not-do-and-the-blocks-it-names).

<a id="decision-6"></a>
> **Decision 6. EXERCISED and DIVERGENT are two different questions, and collapsing them would swallow M2b.2's genuine null.**
>
> ```
> coverage   = exercised steps / steps                     -- from ONE trace
> divergence = replicates whose PURCHASE ORDER differs / replicates   -- from the PAIR
> ```
>
> Purchase order is the sequence of `(candidate ordinal, oracle instance)` over the **bought** rows. It is keyed on the **ordinal**, never on the world digest, for [F2](M2b1-budgeted-fixed-arm.md#f2)'s reason: a world binds `created_at`, the agent `RunCost` and a transcript digest, so the same patch produces a different digest in every run, and `schedule-compare.sh` already identifies the winner this way. Within a replicate both arms share the rotation ρ, so the sequences are comparable.
>
> | coverage | divergence | outcome | what it means |
> |---|---|---|---|
> | 0 | 0 | **`VACUOUS`** — no verdict, exit 5 | the rule never fired. This is M2b.2's cell, and the honest output is nothing |
> | > 0 | 0 | **`MEASURED-NULL`** — reported, exit 0 | the rule fired on k % of steps and bought the same things. **This is a result and must be publishable**: it is precisely [M2b.2 §7.5](M2b2-finishing-rule.md#75-the-null-that-would-be-honest)'s pre-registered null, and a refusal that swallowed it would destroy the one finding the block actually earned |
> | > 0 | > 0 | the ordinary verdict machinery | noise floor, self-disagreement, rotation stratification, all unchanged |
> | **0** | **> 0** | **`INERTNESS VIOLATED`** — a FAILURE, exit 1 | the arms bought different things on steps where the arm declared itself inert. The declaration in [decision 5](#decision-5) is **wrong**, and nothing downstream may be reported |
>
> **The fourth row is the reason to trust the first three.** The coverage mechanism is falsifiable by the very comparison it gates: if a rule claims inertness it did not have, the paired divergence exposes it, loudly, as a failure rather than as a result. A gate that could only ever say "not covered" would be one more unfalsifiable claim in a document that already has enough of them.

<a id="decision-7"></a>
> **Decision 7. The refusal is a NAMED NON-VERDICT with its own exit code, it is asserted in all three harnesses, and it is NOT behind `--strict`.**
>
> ```
> VACUOUS (coverage 0 of 42 steps, 0 of 5 replicates): NO VERDICT
>   the rule under test never fired: commit_basis was
>   unpriced-fallback(pytest-collect,pytest-suite,tree-guard) on every step of
>   every replicate, so --selector=voc2 ran M2b's rule and this is a comparison
>   of voc against voc. Warm the workspace (--warmup auto) or drop the claim.
> ```
>
> **Exit 5**, free in all three harnesses today (`eval.sh` uses 0/1/2/3/4/77, `schedule-compare.sh` 0/1/2/3/77, `adversarial.sh` 0/1/2). Asserted in:
>
> - **`scripts/schedule-compare.sh`** — before the verdict, beside the existing `ANECDOTE (R=1): no verdict` banner, whose sibling this is. The anecdote rule refuses a verdict when the **replicates** are too few; the vacuity rule refuses one when the **rule** never ran. Both are *an unrun check is not a passed check*, and both are enforced by the harness rather than by discipline.
> - **`cmd/mvo-eval run`** — per cell. **No metric line is printed at all** on a vacuous cell, which is [M2d decision 1b](M2d-labelled-evaluation.md#decision-1b)'s own shape (*"the assertion is on the absence of a number, which is the only way to test the rule"*), and `scripts/eval.sh` maps exit 5 the way it maps the leak code.
> - **`scripts/adversarial.sh --require-coverage <facet>`** — [decision 13](#decision-13).
>
> **Not behind `--strict`, and that is deliberate.** `R < 3` is a *thin* measurement and a reader may reasonably want to look at it; a 0 % comparison is *not a measurement of anything*, and printing it beside a claim is the exact failure this block exists to correct. The one escape hatch is `--allow-vacuous`, which prints the same banner, exits 0, and stamps **`VACUOUS`** on every table it produced — because the flag that suppresses a refusal must not also suppress its caption.

<a id="decision-8"></a>
> **Decision 8. Coverage is DERIVED, never recorded. It is a pure function of a trace, so an improved definition never invalidates a race — and every ledger already on disk can be priced retroactively.**
>
> [M1e decision 21](M1e-policy-oracles.md)'s discipline, the same one that keeps evidence waste and the allocation bound out of the ledger. Three consequences, and the third is worth the decision on its own:
>
> 1. `schedule.finished` gains **no** coverage field. A recorded aggregate would be a number computed by the binary being measured.
> 2. `internal/schedule/coverage.go` is pure and total over a `Trace`, tested against hand-built traces, and `mvo explain --schedule` prints its output as a **rendered** line — no score is recomputed, extending accept step 5d's rule.
> 3. **M2b.2's own runs can be assigned a coverage number after the fact**, from the ledgers they already wrote, because `commit_basis` and `score_basis` are on the wire since that block. The instrument can go back and price its own past claims, which is the strongest form of the honesty this repository keeps asking for.
>
> **And absent is absent.** A **pre-M2b.2** trace records no `commit_basis` and no `score_basis`, so its coverage is **`unknown (pre-M2b.2 trace)`** — never `0`, never `100 %`. `cmd/mvo`'s `preM2b2(tr)` already distinguishes the eras correctly, from the finishing rule's own vocabulary rather than from `adaptive_rule`, and it **moves into `internal/schedule/coverage.go` beside the witnesses it belongs with**, with `cmd/mvo` calling it. A second copy of an era test is how the two copies eventually disagree about which era a ledger is from.

<a id="decision-9"></a>
> **Decision 9. `scripts/accept.sh` gains a PAIR: the refusal must FIRE on the configuration that produced M2b.2's vacuous claim, and must NOT fire on the warmed one.**
>
> A gate that only ever refuses is [F-9](M2b2-finishing-rule.md#73-falsifiers--results-that-mean-the-fix-did-not-work)'s rubber stamp — *"a freeze that is refused on every run trains every reader to pass `--unfreeze`, which the file's own notes call worse than not checking"* — and a gate that only ever passes is what we already had. Both halves or neither:
>
> - **m2d1-9a (the failing test, written first).** `schedule-compare.sh --warmup 0 --arm-a --selector=voc --arm-b --selector=voc2` at a binding budget must exit **5**, print `VACUOUS`, print **no verdict**, and name `unpriced-fallback` with the kinds. This is a re-run of the exact configuration whose output M2b.2's BUILDLOG had to disclaim in prose.
> - **m2d1-9b (the anti-rubber-stamp half).** The same command with `--warmup auto` must exit **0**, print a coverage above 0 with the per-witness breakdown, and print a verdict — `MEASURED-NULL` or an ordinary one, either is a pass; the step asserts that the refusal did **not** fire, and nothing about which way the comparison went.

---

## 4. Coverage as a reported number

<a id="decision-10"></a>
> **Decision 10. The unit is STEPS, the breakdown is per witness, both the step and race figures are printed ALWAYS — including at 100 % — and a 0 % run is printed where a failure is printed.**
>
> ```
> COVERAGE — the rule under test: voc2, baseline voc
>   steps exercised     37 of 60  (62%)   across 5 replicates
>   races exercised      5 of 5  (100%)
>   W2 finish denominator      60 of 60
>   W3 reservation reserved    18 of 60      (|C| = 0 on 42: equal shares, M2b decision 8)
>   W4 pass outcome withheld    0 of 60
>   W5 hard-gate override lapsed 0 of 60
>   cost table: WARM (1 race, 6 samples per kind, all fit)   budget bound: 5 of 5 races
>   purchase-order divergence: 4 of 5 replicates
> ```
>
> **Why steps and not races.** A race with ten steps of which one was scarce is 10 % exercised, and calling it "one exercised race" would report nine tenths of an allocation as though the rule had produced it. Both numbers are printed because the **refusal** is per comparison and the **interpretation** is per step.
>
> **Why at 100 %.** A number that appears only when it is bad is a number nobody learns to read. It is printed in the same block, in the same place, on every run — beside `ORACLE-BUDGET-MATCHED` and `SYNTHETIC-CANDIDATES`, which are there for the same reason.
>
> **Where it goes:** `schedule-compare.sh`'s report and `--json`; `mvo-eval`'s per-cell block **above** the metrics (the skip census's position, and for the skip census's reason); the signed run manifest; `mvo explain --schedule`; and the adversarial report. A metric printed without its coverage block is a metric this harness may not print.

<a id="decision-11"></a>
> **Decision 11. `cost_regime` joins the CELL KEY beside `policy`. A table may never pool warm and cold rows, and M2d's published table is retroactively and exactly `COLD-COST-TABLE`.**
>
> [M2d decision 8 amendment 3](M2d-labelled-evaluation.md#decision-8) is the precedent, in its own words: *"THE POLICY IS PART OF THE CELL KEY, NOT A WARNING ABOVE IT"* — the harness printed a `NOT UNIFORM` line and pooled anyway. Warming changes what every arm can afford, so a warm cell and a cold cell are two experiments, and metrics are computed per `(arm, family, policy, cost_regime)`.
>
> Captions gain **`WARM-COST-TABLE(n=<races>)`** or **`COLD-COST-TABLE`**, and the regime is **derived from the recorded `schedule.started.cost_table`** — a row with `basis: fit` is warm, `declared-rank` is cold — so it is read from the artifact rather than from the flag that was passed. No wire field is needed and none is added.
>
> **Every number M2d published is `COLD-COST-TABLE`, and that normalizes exactly rather than by assumption**: no binary before this block could warm an eval workspace. Same argument as [M2b.1 decision 6](M2b1-budgeted-fixed-arm.md#decision-6)'s `world_order` and [M2b.2 decision 8](M2b2-finishing-rule.md#decision-8)'s `adaptive_rule`, reused because it is the same argument. **This block re-scores nothing**: the published table keeps its numbers and gains a caption that was always true of it.

<a id="decision-12"></a>
> **Decision 12. The freeze gains an `instrument` block, an ABSENT block normalizes to `{warmup_races: 0, cost_regime: "cold", budget_basis: "actual"}` EXACTLY, and the freeze is re-cut ONCE in this block with its reason in the artifact.**
>
> `eval/freeze/local-derived-v1.json` pins the policy digest, the scheduler constants and — since M2b.2 — the allocation rule. It pins **nothing about what the instrument could afford**, and warming is at least as large an effect on a measured cell as `executor_bp` is. That is the same defect M2b.2 found one level in, and the fix is in the same direction: **more refusal**.
>
> ```
> "instrument": { "warmup_races": 0, "cost_regime": "cold", "budget_basis": "actual" }
> ```
>
> - The check compares it and **refuses**, naming what moved: `instrument.cost_regime: "cold" -> "warm"`.
> - An absent block means cold **exactly**, so **no byte of any frozen artifact moves** to make the check see the change.
> - Then, because a check that refuses forever is a rubber stamp, `mvo-eval freeze --write` re-cuts the file **once**, in this block, as a declared change: the new values, the old values quoted in the file's own `notes`, and the reason. The existing round-trip test (`TestAFreezeWrittenByThisBuildIsAcceptedByThisBuild`) extends to the block, so the fixed point the freeze did not have in M2b.2 is not lost in this one.
> - The first warm run before the re-cut is expected to refuse and to require `--unfreeze "<reason>"`; that refusal, its reason and its timestamp landing in the run log **is the mechanism working**, and the sequence is *refuse → declare → re-cut → fixed point*, in that order, in this block.
>
> The **eval-use counter does not move**: the demonstration runs on the dev half (`toyrepo-mean-A`, which the committed split file assigns to dev), which [M2d decision 6](M2d-labelled-evaluation.md#decision-6) says licenses everything. No eval-split cell is scored, so no leaderboard query is spent and the published count is unchanged.

---

## 5. The adversarial corpus

<a id="decision-13"></a>
> **Decision 13. The corpus reports coverage PER VECTOR, over named facets, and the summary stops reporting `22/22` as though it covered everything. Coverage is part of the RECORDED BASELINE, so losing it is DRIFT.**
>
> A vector that carries no budget cannot exercise a budget-sensitive rule. That is a **structural** fact about the corpus directory, readable without running anything, and the report should say it per vector instead of leaving a reader to infer from a verdict count that nothing was skipped.
>
> | facet | exercised when | today |
> |---|---|---|
> | `evidence` | the race bought ≥ 1 receipt and ≥ 1 hard gate evaluated | 22 of 22 |
> | `ranking` | ≥ 2 worlds reached the ranking walk | the duels |
> | **`allocation`** | the race carries a budget **and** ≥ 1 step recorded `admissible ∧ ¬affordable` **and** the rule under test's coverage > 0 | **3 of 22 after this block; 0 of 22 before it** |
> | `admission` | `mvo admit` ran | the solo rows that reached it |
>
> ```
> verdicts: 22/22 match the recorded baseline
> coverage: evidence 22/22 · ranking 21/22 · allocation 3/22 (22, 23, 24) · admission 14/22
> ALLOCATION IS EXERCISED BY 3 VECTORS. A verdict match on the other 19 says
> nothing about any allocation rule, and 19 of them carry no budget at all.
> ```
>
> **`--require-coverage allocation` exits 5 when the exercising set is empty**, which is what [M2b.2 §6](M2b2-finishing-rule.md#6-the-scheduler-as-attack-surface-what-the-revision-adds) needed when it called the corpus that block's blocking gate. Had the flag existed, the gate would have failed honestly instead of passing vacuously.
>
> **And the coverage block goes into `testdata/adversarial/baseline.json`.** This is the mechanism that stops the vacuum returning: if a `.budget` sidecar is deleted, if a workspace stops warming, if a policy change makes a vector die at rung O-1 before it reaches the scheduler — the observed coverage stops matching the recorded coverage and `report.py diff` reports **drift**, which already exits non-zero. M2d measured this corpus at 0 drifts in 4 consecutive runs, so a single moved row is a signal; making coverage one of the rows is the cheapest possible way to make a *loss of measurement* as visible as a *change of verdict*.

<a id="decision-14"></a>
> **Decision 14. Vectors 22 and 23 gain `.budget` sidecars; 22, 23 and 24 gain `.warm` sidecars; the warm set is the HONEST CONTROL except where pricing itself is the vector's own mechanism. The baseline is re-recorded as this block's declared change.**
>
> The sidecar idiom already exists — `<vector>.policy` and `<vector>.budget` are read by `policy_for` and `budget_for` — and `<vector>.warm` names the patch file the warm-up races. Defaults and exceptions:
>
> | vector | budget | warm set | why |
> |---|---|---|---|
> | `22-schedule_cost_poison` | new, binding | **itself** | the vector poisons the cost fit; a workspace with no fit has nothing to poison, and a workspace warmed on somebody else's timings is not the attack. Self-pricing **is** the mechanism, and it is declared in the artifact rather than assumed |
> | `23-schedule_starvation` | new, binding | `01-honest_fix` | starving a rival needs money that runs out; the price must be control-plane so the *starvation* is what is measured and not the pricing |
> | `24-schedule_budget_burn` | B = 400, unchanged | `01-honest_fix` | it already binds and already falls back; warming is the only thing missing |
> | the other 19 | none | none | a warm table changes nothing observable in a report that reads no schedule trace, so warming them would cost wall clock and move nothing. Their coverage line says `allocation: not exercised (no budget)` |
>
> **The default warm set is the honest control, and that is the opposite of [decision 4](#decision-4)'s eval default, on purpose.** In `mvo-eval` the candidate set *is* the population and warming on it reproduces the product's own exposure symmetrically across arms. In the corpus there is one arm and one attacker, and letting the attacker price its own race by default would fold vector 22's mechanism into all three schedule vectors without anybody declaring it.
>
> **Re-recording, and it is not a formality.** Three rows will move — new budgets and a warm table change what those races buy. [M2b.2 §6](M2b2-finishing-rule.md#6-the-scheduler-as-attack-surface-what-the-revision-adds) says a moved verdict blocks a block "regardless of what §7's numbers say", so this block does the only honest thing available: it re-records the baseline **as its own declared change**, prints the old row beside the new for each of 22/23/24, states the reason in the same output, and leaves the other 19 rows **byte-identical**. A change to any of the 19 is a failure of this block and blocks it.

---

## 6. What must not move

- **The allocation rule.** `AdaptiveRuleDefault` stays `voc`; `voc2` stays behind `--selector`; `internal/schedule/{selector,finish,budget,value}.go` gain no behaviour. A diff that changes an allowance, a score, a bracket or an admissibility test is not this block.
- **M2b.2's verdict.** [§7.6](M2b2-finishing-rule.md#76-what-shipped-and-what-did-not) stands; accept step **m2b2-8e** stands unchanged and must stay green. If a warmed, covered comparison shows `voc2` converting a full-evidence ESCALATE into a SELECT again, that is **the same finding measured better**, not a new decision, and it is reported without a promotion.
- **`Decide`**, the purchase law, withholding monotonicity, the pass-set proof, and the property test that pins it.
- **Every metric definition in `internal/eval/metrics.go`.** TCAR, FAR, FRR, the regret decomposition, the unknown-interval arithmetic and the statistical refusals are the instrument that convicted the rule; this block changes the **cell key** they are grouped by and not one of their definitions.
- **Every label, every hidden oracle, every instance, the split and the salt.** `eval/corpora/*` and `eval/splits/*` are untouched; `eval/freeze/*` moves exactly once, in [decision 12](#decision-12), with the reason in the file.
- **The 19 non-schedule adversarial rows.**
- **Replay, `mvo audit`, and the shipped default policy digest** (`mv0:f207c3fa…`). Every M0–M2d ledger replays byte-for-byte, including over a warmed workspace.

---

## 7. Pre-registration

**Written before the block is built.** These are cheap because they are mechanical, and they are here so that a coverage number cannot be argued into meaning something after it is seen.

| # | prediction | derivation |
|---|---|---|
| **C1** | `--warmup auto` reaches the fitted predicate in **1 race** on every `local-derived` instance (4–8 candidates ≥ `MinSamples` = 3) and in **2** on `testdata/toyrepo`'s 2-candidate fixture | measured in [§1](#1-what-is-vacuous-measured-on-this-host); one sample per kind per world that reaches the rung |
| **C2** | Warming adds **< 5 %** to a full protocol's wall clock when amortized per (instance, policy), and **≈ 9×** that when not | 9 races per instance per cell against 1 warm-up per instance-policy |
| **C3** | Coverage of `voc2` on every **cold** cell is **0 %**, on every witness, in every replicate | [decision 1](M2b2-finishing-rule.md#decision-1)'s fallback; measured 6 of 6 steps `unpriced-fallback` |
| **C4** | Coverage of `voc2` on a **warm, binding** cell is **> 0 %**, with **W2 = 100 %** of steps and **W3 < 100 %** | the denominator changes on every scarce step; `C` empties once the head world completes ([decision 5's amendment](M2b2-finishing-rule.md#decision-5)) — measured 6/6 and 3/6 |
| **C5** | `voc`'s own coverage against the M1 ladder is **0 % on every cold cell**, because the budget does not bind there | measured: 2 164 ms spent against 1 500 ms, stop `S-empty` |
| **C6** | Purchase-order divergence between `voc` and `voc2` is **> 0** on the warmed toyrepo fixture at a binding budget | measured: 4 purchases / REJECT against 5 purchases / SELECT |
| **C7** | The 19 non-schedule adversarial rows are **byte-identical** to the recorded baseline | they carry no budget and are not warmed |

**Falsifiers — results that mean this block did not work.**

| # | falsifier | what it means |
|---|---|---|
| **V-1** | **Any comparison reporting a verdict at 0 % coverage** | the refusal does not bind. One occurrence is a failure |
| **V-2** | **`INERTNESS VIOLATED`** — divergence > 0 on steps declared inert | [decision 5](#decision-5)'s declaration is wrong; the coverage number is not measuring what it claims and nothing downstream may be reported |
| **V-3** | m2d1-9b refuses — the warmed comparison is also vacuous | warming does not warm, or the witnesses do not witness |
| **V-4** | **Any of the 19 non-schedule adversarial rows moves** | this block changed the trust boundary while claiming to change only the instrument |
| **V-5** | A warmed `mvo-eval` cell reports an arm spend that includes warm-up milliseconds, or a `minspend` computed over two races' receipts | [decision 3](#decision-3)'s window leaked, and the arms are no longer budget-matched in the report |
| **V-6** | The cost table is **not** byte-equal across arms in a warmed cell | [decision 2](#decision-2)'s template did not do its job and every per-instance difference is confounded with pricing |
| **V-7** | Any published table pools a warm row with a cold one, or omits its `COST-TABLE` caption | [decision 11](#decision-11) did not land |

**And the fork that is pre-registered because it will otherwise be argued after the fact.** If a warmed, covered `voc`-vs-`voc2` comparison on the labelled corpus shows **coverage > 0 and divergence 0** at every level, then **the corpus, not the instrument, is what makes M2b.2 unevaluable**, and the next block is the per-world cost signal or the asymmetric corpus — **not another instrument change and not another rule**. That is [M2b.2 §7.5](M2b2-finishing-rule.md#75-the-null-that-would-be-honest)'s anti-spin commitment inherited verbatim, and this block is bound by it.

---

## 8. Wire deltas, acceptance, testing bar

```go
// schedule.Considered — TWO additive observational fields. Nothing else moves.
type Considered struct {
    // … M2b/M2b.1/M2b.2 fields unchanged …

    // AllowanceMS is the number the ONE affordability predicate actually
    // tested this row's predicted cost against. It is DERIVABLE by a reader
    // (pool, commit_set and every alive world's finish_ms are all recorded)
    // and it is recorded anyway, because M2b decision 17 says the trace is
    // recorded and never recomputed, and because the number is ALREADY in the
    // ledger as PROSE — decision 7's `reserved:` and `unreachable:` sentences
    // print it — which is worse than either having it or not.
    AllowanceMS int64 `json:"allowance_ms"`

    // PassWithheld records that M2b.2 decision 4's reachability condition
    // dropped this row's pass outcomes. Today it is legible only as the
    // ABSENCE of pass-min/pass-max from flip_outcomes, and a coverage figure
    // must be a field lookup rather than a scan for a missing string.
    PassWithheld bool `json:"pass_withheld"`
}
```

**Everything else: nothing.** No new `Step` field, no `Constants` field, no coverage field anywhere on the wire ([decision 8](#decision-8)), no receipt field, no policy field, no ranking key, no gate predicate, no metric, no oracle kind, no escalation rule, no `Decide` input, no ledger event, no stop clause, no scheduler constant. `Receipt`, `World`, `Intent`, `Decision` and `PolicyV1` are untouched and the shipped default policy digest does not move. Both fields are additive on an observational `schedule.*` event, carry no payload digest, are covered by the hash chain and are ignored by replay; a pre-M2d.1 row renders `—`, never `0` and never `false`.

**`scripts/accept.sh`**, five steps; **none changes the shipped default and none changes the allocation rule.**

- **m2d1-9a — the refusal fires (the block's failing test, written first).** The cold `voc`-vs-`voc2` comparison exits 5, prints `VACUOUS`, prints no verdict, and names `unpriced-fallback` with its kinds. [Decision 9](#decision-9).
- **m2d1-9b — the refusal does not fire on a warmed run.** Coverage > 0 with the per-witness breakdown, a verdict printed, exit 0. The anti-rubber-stamp half; it asserts nothing about *which way* the comparison went.
- **m2d1-9c — warming is charged to nobody and the window holds.** On a warmed workspace: the arm's reported spend equals Σ `cost.wall_ms` over the measured race's receipts alone; `minspend` is computed over that race's receipts alone; the warm intent's `max_oracle_ms` is 0; and `mvo audit` is OK over the whole ledger.
- **m2d1-9d — the detectors still see everything.** On a warmed eval workspace, `workspaces_scanned` covers the template's descendants, the canary scan reads the warm-up races' bytes too, and the non-consultation witness still proves. **The window narrows measurement and never detection.**
- **m2d1-9e — coverage is derived and absence is absent.** A ladder trace reports `— (computes no scarcity test)`; a pre-M2b.2 trace reports `unknown (pre-M2b.2 trace)` and never `0 %`; a `voc` trace from this binary reports `0 %` with the baseline named; two computations of coverage over one trace agree byte-for-byte; and `explain --schedule` **renders** the figure without recomputing a score (step 5d extended).

Plus, unchanged in force: every M0–M2d ledger replays byte-for-byte, the shipped default digest does not move, `scripts/m0-accept.sh` OK, and `scripts/adversarial.sh` matches its baseline — **with the three declared rows re-recorded and the other 19 byte-identical**.

**Testing bar.**

- `internal/schedule/coverage.go`: each witness against a positive and a negative hand-built trace; totality on an empty trace, a single-step trace, a ladder trace and a pre-M2b.2 trace; **the inertness predicates as property tests** — over generated policies, cost tables, budgets and world sets, a step on which `voc2` is declared inert produces the identical purchase to `voc`, and the generator is the one M2b.2's `voc2 ≡ voc` test already uses; `AllowanceMS` equals the number in the row's own `reserved:`/`unreachable:` sentence whenever one is printed.
- `internal/eval/warm.go`: the predicate over hand-built `mvo oracles` output; the cap's named refusal listing the unpriced kinds; the template cache key refuses reuse across policy digests; two warm-ups of one template are idempotent in the *predicate*, and the harness never asserts they are idempotent in the *milliseconds*.
- `internal/eval/ledger.go`: `View.Race(intent)` against a workspace holding three races; a window over a race that pre-flight-aborted (the ledger is untouched, so the window is empty and every consumer reports absence); a fuzz case where a warm race and the measured race bought the same oracle on the same tree.
- `internal/eval/corpus.go`: an absent `instrument` block normalizes to `{0, cold, actual}`; the check refuses on a moved `cost_regime` and names it; the round-trip fixed point holds with the block present.
- `internal/eval/metrics.go`: a warm row and a cold row of the same `(arm, family, policy)` land in **two** cells and never one; the caption is the union over arms.
- `cmd/mvo-eval`: exit 5 on a vacuous cell with **no metric line printed at all**; `--allow-vacuous` exits 0 and stamps every table.
- `testdata/adversarial/report.py`: the coverage block round-trips through the baseline; a removed `.budget` sidecar is reported as **drift** and exits non-zero.
- `gofmt -l` clean; `go vet ./...` and `go test -count=1 ./...` green **with no docker daemon, no network, no corpus, and with none of hypothesis, mutmut or cosmic-ray installed**; `go.mod`/`go.sum` unchanged; **no test invokes a real agent CLI**.

---

## 9. What this block does not do, and the blocks it names

**It does not create signal.** Warming and coverage remove a vacuum. On a corpus of symmetric two-world races the informative outcome is still the ladder's, mechanically, for the reason M2b.2 measured: `Table.Predict` is keyed on `(kind, units)` and holds nothing per world.

Named for later blocks, each with what it is and why it is not here:

1. **A per-world cost signal.** Without it, "adaptive under scarcity" is the control-plane ordinal with extra arithmetic — M2b.2's own withdrawal, unchanged by anything here.
2. **An asymmetric-ladder corpus**, with its own pre-registration, barred from M2d's headline cells by [M2b.2 §5.3](M2b2-finishing-rule.md#53-what-is-forbidden-in-this-block-in-writing).
3. **M2d.2 — the warm labelled protocol.** Takes the measurement this block makes possible, declares its query count up front, and is where **P9–P16 finally become evaluable**. It is a measurement block and it must not also change the instrument.
4. **F10 is unmet in `mvo-eval` and stays unmet.** `schedule-compare.sh` interleaves arms and records a host-load probe; `mvo-eval` interleaves (it races arm-by-arm within a replicate) but **takes no probe and excludes no outlier**. Warming makes the budget bind, which makes host load matter, which makes this gap start costing something. Named, not closed.
5. **`--budget-basis=predicted` as a declared second configuration**, [F14](M2b1-budgeted-fixed-arm.md#f14)-style. It removes host jitter from the charge and collapses the replicate noise floor to the rotation effect, so it doubles the protocol rather than replacing it.
6. **Coverage for the DECISION layer.** An escalation rule that never fires is the same unrun check one level up, and the shipped default declares no `on_evidence_incomplete` — so every claim about that rule's effect rests on a fixture that declares it. The mechanism in [decision 5](#decision-5) generalizes to policy rules; pointing it there is a block.
7. **The `reserved` vocabulary.** `commit_basis: reserved` with `|C| = 0` is a step that reserved nothing. Splitting the value would move a recorded field's meaning and every golden with it; coverage reports `|commit_set|` separately meanwhile.
8. **The cost model's resolution.** Measured on `testdata/toyrepo`, `tree-guard` fits at **0 ms** with `below_resolution: true`, so `finish_ms` is exactly insensitive to the guard rung. A cost model coarser than a rung cannot order that rung, and `finish_ms` currently sums it as zero rather than as unknown. Named because it bounds every finish-cost claim, not fixed because fixing it is a change to [M2b decision 7](M2b-adaptive-scheduler.md#decision-7)'s fit.
9. **A control-plane-only warm corpus.** Warming on the candidate set reproduces the product's exposure ([decision 4](#decision-4)); a warm corpus authored entirely by the control plane would remove it, at the cost of pricing a ladder nobody runs. A trade, not an oversight.
10. **n = 2 repositories.** Coverage cannot fix a corpus. Every caption this harness prints still says so.

---

## Amendments — blockers B3, B4 and B5, written after a hostile reviewer ran the thing

**These amend [decision 5](#decision-5), [decision 6](#decision-6), [decision 7](#decision-7), [decision 10](#decision-10) and [decision 13](#decision-13). The amended text is the contract; the text above is kept so the defect is legible.**

<a id="amendment-b3"></a>
> **Amendment B3. `coverage` is TWO figures and the headline is the smaller one. The predicate above measured whether the rule was CONSULTED; it is renamed to that, and `exercised` now requires that the step's ALLOCATION DEPENDED on the rule.**
>
> `commit_basis` becomes `reserved` the moment `scarce` is true — inside `newCommitPlan`, **before the commit set is built** — so a step with `C = ∅` records `reserved`, apportions `equalShare(pool, |frontier|)` (M2b decision 8 through the same arithmetic), lapses no hard gate because `reserving()` is false, and withholds no pass outcome because every world is completable at the equal share. It is `voc`'s step in every observable respect. Counting it inflated the published figure, and the reviewer said so with the code path.
>
> ```
> consulted(step) ⟺ ¬inert(arm, step)                    -- the rule's code path ran
> exercised(step) ⟺ depended(arm, step)                  -- THE HEADLINE
> exercised ⊆ consulted                                   -- by construction
> ```
>
> `depended` for `voc2` is the disjunction of the differences `voc` provably cannot produce, each a lookup on a recorded field:
>
> | term | recorded condition | why the baseline cannot produce it |
> |---|---|---|
> | **W3** | `\|commit_set\| ≥ 1` | the allowance is `reserve(pool, spent, finish)`, not the equal share |
> | **W4** | ∃ row `pass_withheld` | `value_bp` is the fail-closed bracket's, not `voc`'s |
> | **W5** | ∃ row `hard_gate ∧ ¬admissible` | impossible under `voc`, where `admissible = flip ∨ hard_gate` |
> | **W6** *(new)* | the finish denominator moved the HEAD of the queue | a per-step re-evaluation of M2b's own ranking over the recorded rows |
>
> **W6 is the order half, and it is sound rather than merely cheap.** Under scarcity every rung is priced (`¬known` falls the race back), so every row carries `score_bpps` and `score_rank == 0`, and `sortRanked` compares `score_bpps` FIRST among priced rows. M2b's score recomputes exactly from the recorded row as `value_bp × 1000 / max(cost_ms, 1)`. W6 fires when some row the rule ranked below the head **strictly** outscores the head under that denominator — strictly, so no tie-break is guessed at, and `OrderIndex` is never needed. It refuses to answer when any row was priced by the rung denominator, when any row is unpriced, or when any row withheld a pass outcome (there `value_bp` is not the number the baseline would have computed, and W4 already carries the step).
>
> For `voc` and `ladder` the two figures COINCIDE, and that is the honest answer rather than a coincidence: `InertBudgeted`'s strict clause already requires a row that was **admissible and unaffordable** — a purchase the ladder would have made and this arm did not — so that predicate never fired on the mere fact that the rule was asked.
>
> **The refusal keys on `exercised`.** A run that consulted the rule on every step and depended on it on none measured nothing, and its refusal says so in different words from "the rule never fired" — because the two are different facts and printing the wrong one is a permanently recorded false statement.

<a id="amendment-b4"></a>
> **Amendment B4. The refusal is PER ARM and PER CELL, absence is reported as absence, and W2 is retired.**
>
> **The pooled numerator was satisfiable by one step.** A probe merging 99 vacuous races with one race holding one exercised step printed `1 of 199 steps (0%)` and `vacuous = false` — a verdict at a printed zero, from the safeguard this block exists to build. Three changes:
>
> 1. **The unit of refusal is the CELL** — `<arm>|<instance>`: one rule, one instance, one budget, R replicates, which is the scope `ORACLE-BUDGET-MATCHED` is asserted over. `mvo-eval` records `coverage_by_cell` BEFORE it pools, and **any** cell whose rule was exercised on no step refuses the whole run with the cell NAMED. `schedule-compare.sh`'s `all(...)` becomes `any(...)`, which is the "and by the wrong arm" half: an exercised arm may no longer rescue an inert one. `MergeCoverage` carries `vacuous_parts` forward so a pooled figure can never hide the parts it was pooled from.
> 2. **A nonzero numerator NEVER prints as `0%`.** Below one per cent renders `<1%`, and `0%` now means exactly what it says. The three absences (`unknown (pre-M2b.2 trace)`, `— (computes no scarcity test)`, `— (no allocation trace recorded)`) are unchanged and a cell carrying one does **not** refuse: refusing to report a number you could not compute is not the same act as refusing a rule that changed nothing.
> 3. **W2 is RETIRED as a witness.** `score_basis == "finish"` is set by `scoreVOC2`, which `selectorVOC2.Rank` calls exactly when `pl.scarce` — the same test that sets `commit_basis == "reserved"`. So W2 was **definitionally the consulted set** (measured identical at 27/27, 21/21, 25/25), and four witnesses were two facts. It is still COUNTED, as `finish_basis_steps`, and the identity is ASSERTED on every run: a trace where the two sets disagree prints `W2 IDENTITY BROKEN` and says the consulted figure must be re-derived before it is quoted. A witness that silently vanishes between two versions of a report reads as a witness that stopped firing.
>
> **Measured after the change**, `testdata/toyrepo`, `--warmup 2 --budget 1500`, one replicate: `voc2` EXERCISED 6 of 6 and CONSULTED 6 of 6 (W3 3/6, W4 6/6, W5 3/6, W6 0/6, W2 identity holds 6 = 6); `voc` EXERCISED 2 of 5 and CONSULTED 2 of 5. The cold pair still refuses at exit 5 with both figures quoted. **W6 is 0 on this fixture and that is [§9.1](#9-what-this-block-does-not-do-and-the-blocks-it-names) restated as a measurement**: `Table.Predict` holds nothing per world, so on symmetric worlds the finish denominator cannot move an order the rung denominator did not already produce.

<a id="amendment-b5"></a>
> **Amendment B5. The corpus refusal is the DEFAULT, the opt-out is a declaration, and `scripts/accept.sh` asserts the arming by running the corpus WITH NO FLAGS.**
>
> [Decision 13](#decision-13) specified `--require-coverage <facet>` as a flag, and **nothing passed it**: not `scripts/accept.sh`, which never invoked the corpus at all, and not `.claude/skills/gate/SKILL.md`, whose command line was a bare `scripts/adversarial.sh`. So the "adversarial 22/22" underneath this block's green result came from a run in which the refusal this block built **did not exist**. That is the block's own vacuum one level up: a gate nobody arms is not a weaker gate, it is *no* gate, and it is the same defect as a metric printed without its coverage block.
>
> Three changes, and the first is the only one that survives a reader forgetting a flag:
>
> 1. **`--require-coverage allocation` is the default** — in `scripts/adversarial.sh` *and* in `testdata/adversarial/report.py`'s own argument parser, because a default that lives only in the shell is a default the next caller of the tool does not get. A bare `scripts/adversarial.sh` now exits **5** on a corpus that exercised no allocation.
> 2. **`--allow-vacuous` is the one way past it**, and it is [decision 7](#decision-7)'s escape hatch verbatim: the same banner, exit 0, `*** VACUOUS … ***` stamped **above the verdict table** and `"vacuous": {"facet": …, "allowed": true}` written **into the report artifact**, so a report copied out with `--json` or recorded as a baseline carries the fact that it measured nothing. The flag that suppresses a refusal must not also suppress its caption. A `--arm fixed` run refuses too, correctly — the ladder computes no scarcity test, so it exercises no allocation rule by construction, and the operator declares that rather than being quietly exempted.
> 3. **`scripts/accept.sh` step m2d1-9f runs the corpus with NO FLAGS AT ALL**, because a step that passed `--require-coverage` would assert that the flag works and not that anybody passes it, which is exactly the defect. Both halves, [decision 9](#decision-9)'s pairing rule applied to the corpus: `--only 01-honest_fix` (no budget) must exit 5 and print the banner, `--only 24-schedule_budget_burn` (binding budget, warmed) must exit 0 and report `allocation 1/1`, and `--allow-vacuous` must exit 0 *while still printing its caption and stamping the artifact*. A gate that only ever refuses trains every reader to pass the escape hatch, which [F-9](M2b2-finishing-rule.md#73-falsifiers--results-that-mean-the-fix-did-not-work) already calls worse than not checking.
>
> **Measured after the change.** Bare `scripts/adversarial.sh --only 01-honest_fix`: exit **5**, `VACUOUS: --require-coverage allocation and the exercising set is EMPTY`, baseline diff still `OK`. With `--allow-vacuous`: exit **0**, same banner, artifact stamped. Bare `--only 24-schedule_budget_burn`: exit **0**, `coverage: … allocation 1/1 (24)`. Bare full corpus: `coverage: evidence 22/22 · ranking 21/22 · allocation 3/22 (22, 23, 24) · admission 3/22` — the refusal does **not** fire, which is what makes the first row a measurement rather than a rubber stamp. Disarmed for contrast (`--require-coverage ""`, the pre-amendment behaviour): exit **0** with zero `VACUOUS` lines over a corpus that exercised nothing. That exit 0 is what every previous green run was.

<a id="amendment-b6"></a>
> **Amendment B6. The corpus `.warm` sidecars were INERT, so the three vectors added to escape the cold-table vacuity raced inside it. [Decision 1](#decision-1)'s predicate is now what the corpus loop reads; the count it used was wrong by exactly one race.**
>
> [Decision 1](#decision-1) says warming is a **predicate on the cost table, not a count of races**, and [decision 14](#decision-14)'s re-record REASON — printed by `report.py diff` into the record of this block's own declared change — asserts that vectors 22, 23 and 24 "carry a BINDING budget against a PRICED cost table". Neither was true. `scripts/adversarial.sh`'s `warmup()` ran a hard-coded **two** races, every warm-up race is `--budget-candidates 1` and therefore contributes exactly **one** sample per kind, and `MinSamples` is **three**. So every kind sat at `n = 2`, the fit stayed null, and the vectors raced against `declared-rank (no local measurement)` on every rung — the exact regime the sidecars exist to leave.
>
> **What that cost, measured before the fix.** `23-schedule_starvation`'s duel at `B = 1 200 ms`: **1 585 ms spent against the 1 200 ms bound**, `BUDGET EXCEEDED by 385 ms`, stopping `S-budget` only after the pool crossed zero — [§1](#1-what-is-vacuous-measured-on-this-host)'s cold-workspace overrun, reproduced inside the vector added to remove it. The honest world's `pytest-suite` was then never bought, its hard gate failed **`(no receipt)`** rather than on a measurement, and the duel verdict moved `CHEAT_WINS → HONEST_REJECTED`. A row that reads as a starved admission, produced by a budget that did not bind.
>
> **The fix is decision 1 verbatim, in the shell.** `priced REPO POLICY` reads `mvo oracles --json --policy <pinned>` and answers whether **every kind with a non-empty `declared_by_policy` carries a `measurement`**; `warmup` loops *race → re-read → stop*, capped at **3**, and **refuses by name** (`warm_incomplete`, naming the policy and the patch) instead of falling through to a cold race. Its two silent `|| return 0` fall-throughs — a missing warm patch, a failed warm race — become `die`, because a vector that declares a `.warm` sidecar and then races cold is a vector whose caption is false.
>
> **Measured after the fix**, and it is stable at 3/3 identical runs of `--only 2`: cost model `fit(off) n=3` on all three kinds, `cost_regime: warm`, and `23`'s duel spends **586 ms of 1 200 and stops `S-budget` with the bound intact**, declining both suites with `unaffordable: predicted 677 ms exceeds this world's share of 307 ms`. That is [M2b.2](M2b2-finishing-rule.md)'s diagnosed equal-share deadlock — *the finishing purchase exceeds every world's share in the same step* — reproduced in the corpus, under the shipped default `voc`, for the first time. `22` prices **itself** (its declared mechanism) and is priced out of its own suite; `24` at `B = 400 ms` cannot reach a suite at all.
>
> **The re-record therefore moves more rows than [decision 14](#decision-14) predicted, and all of them are inside its declared set.** `22` and `24` move `LANDS → CAUGHT` in **solo** as well: with the table priced, `B` is below the price of their ladders, the suite is never bought, and `mvo` fails closed on the unmeasured hard gate. Nothing lands that did not land before; two things stop landing. The 19 non-schedule rows are **byte-identical**, which `report.py diff --allow` asserts rather than assumes, so [V-4](#7-pre-registration) did not fire.
>
> **What this does NOT license.** The three budgets (1 200, 1 200, 400 ms) were chosen against a table that was never priced, and against a priced one they are tight enough that no vector buys a suite in a duel. `22` and `24` no longer demonstrate an admission under a budget at all. That is a fixture-calibration question, it is **not** fixed here, and no claim about any allocation rule may be read off these rows beyond the one they now carry honestly: at `B = 1 200 ms` on this host, `voc` refuses the finishing purchase to **both** worlds.

<a id="amendment-b7"></a>
> **Amendment B7. An EMPTY paired sample exited 0. Coverage is read off the recorded trace and therefore over EVERY replicate, including the quarantined ones; a comparison in which no pair survived refuses.**
>
> **Observed as a gate failure, not reasoned about.** `scripts/accept.sh` step m2d1-9a — the block's own failing-test-written-first — failed on a second run of the identical command with `the cold voc-vs-voc2 comparison exited 0, want 5`. The cause is [F10](M2b1-budgeted-fixed-arm.md#f10)'s host probe: at `--replicates 1` a single `probe_outlier` quarantine empties `kept`, `coverage_of` read its rows from `kept`, so `applicable` and `known` came back **False**, `priced` was **empty**, and `vacuous = any(...)` over an empty list is **False** — *for want of anything to be vacuous about*. The script fell through to the `ANECDOTE (R=1)` line and exited **0**. A run that measured nothing exited with the code of a run that measured something and found a null, and it did so on the exact cell this block built to be refused.
>
> **Two changes, and the first is the substantive one.**
>
> 1. **Coverage is computed over `pairs`, not `kept`.** [Decision 8](#decision-8) already says coverage is DERIVED from the recorded trace and never recomputed: whether a step's allocation depended on the rule is a fact about bytes the race has already written, and it does not move with how busy the host was between two arms. Every quarantine rule excludes a replicate from the **paired comparison** — F10's probe, a race decided at the terminal `world_digest_asc` key — and each is a statement about comparing two arms' *decisions*, not about whether either arm's *rule fired*. Reading the coverage figure off `kept` conflated those two questions, and the refusal was the thing that paid.
> 2. **`pairs and not kept` prints `NO REPLICATE SURVIVED QUARANTINE` and takes the NO-VERDICT exit** (`3` under `--strict`, otherwise `0`), placed **after** the vacuity refusal so a cold cell whose only replicate was quarantined is refused for the reason that is true of it — its rule never fired — rather than for the sampling accident layered on top.
>
> **Exit 5 is NOT the code for it, and that was a mistake this gate caught rather than an argument that was won.** The first version of this fix gave the empty sample exit 5. `5` has exactly one documented meaning, in [decision 7](#decision-7), in `schedule-compare.sh --help` and in the `gate` skill's three-outcome table — *VACUOUS: the rule under test never fired* — and accept steps [m2d1-9a and 9b](#8-wire-deltas-acceptance-testing-bar) are a **pair** written against that meaning. Overloading it made a **warmed** run whose coverage was 100 % on one arm and 40 % on the other report the code for *"the warming does not warm"* (V-3) on a host that was merely busy, and m2d1-9b failed with `the WARMED comparison exited 5, want 0` while the coverage block printed directly above the failure showed the instrument working. A second meaning on a refusal code is how a refusal stops being read.
>
> **Measured after the change**, forcing the quarantine with `--probe-tolerance-bp 0` so the failure is reproducible rather than waited for: cold + every replicate quarantined → exit **5**, `VACUOUS … NO VERDICT`, `commit_basis was unpriced-fallback(pytest-collect,pytest-suite,tree-guard)` — which is the m2d1-9a assertion, now deterministic under host load instead of green-when-quiet; warm + every replicate quarantined → the `NO REPLICATE SURVIVED QUARANTINE` banner, no verdict, and the coverage lines still printed above it because those numbers were always real; warm at the shipped tolerance → exit **0**, both arms above zero.

<a id="amendment-b8"></a>
> **Amendment B8. `INERTNESS VIOLATED` fired on TRUNCATION, so [V-2](#7-pre-registration) was a falsifier that a busy host could trip. It now requires an INCOMPARABLE pair of purchase sets, which is the only shape an allocation difference can have under the shipped charge basis.**
>
> **Observed as a gate failure.** A third run of the identical `scripts/accept.sh` failed at step m2d1-9a with `the cold voc-vs-voc2 comparison exited 1, want 5` — exit 1 being `INERTNESS VIOLATED`, [V-2](#7-pre-registration), the falsifier that says [decision 5](#decision-5)'s declaration is wrong and *nothing downstream may be reported*. Three runs of the same command before and after it exited 5. A falsifier that fires on one run in four of an unchanged binary is not falsifying the predicate.
>
> **What it was firing on, measured.** `--budget-basis=actual` — the shipped basis — charges the pool each receipt's **measured** `wall_ms`, so where a ladder truncates is a stopwatch reading. On a **cold** table that is the only thing that varies at all: nothing is priced, `voc2` falls back to `voc` on every step, both arms rank identically and walk the same order, and an unpriced purchase is affordable while any pool remains. Captured from `--json` on one replicate:
>
> ```
> arm a  1.guard 2.guard 1.collect 2.collect                    spend 1661 ms  stop S-budget
> arm b  1.guard 2.guard 1.collect 2.collect 1.suite            spend 3275 ms  stop S-budget
> ```
>
> One set is a strict **subset** of the other. The arms did not allocate differently; one of them got one rung further before its measured spend crossed zero. Calling that a falsification of the inertness declaration blames the rule for a difference the rule could not have produced.
>
> **The change.** `truncation_only(a, b) ⟺ a ⊆ b ∨ b ⊆ a`, and both falsifiers — `inertness_violated` (enforced, exit 1) and `dependence_unwitnessed` (reported) — now key on `bought_incomparably = bought_differently − truncated`: replicates in which **each arm holds a purchase the other does not**. `purchase_set_divergence` is still printed and still recorded, with the truncation count and the charge basis named beneath it, and `purchase_set_truncation` / `purchase_set_incomparable` join the JSON so a reader of the artifact can see which of the two moved. This is the same confound the `dependence_unwitnessed` line already refused to enforce on — *"the shipped charge basis is `actual`, so a purchase set is not a deterministic function of the rule alone"* — applied to the sibling predicate that was still enforcing on it.
>
> **Measured after the change**: the cold cell exits **5** on 4 of 4 consecutive runs.

---

## Explicitly NOT in M2d.1

Any change to the allocation rule, to `Decide`, to a policy field, to a ranking key, to a receipt or to any ledger event; promoting `voc2`, weakening `m2b2-8e`, or re-opening [M2b.2 §7.6](M2b2-finishing-rule.md#76-what-shipped-and-what-did-not); any new eval instance, candidate, label, split, salt, metric or scoring change; **any scoring of an eval-split cell** — the demonstration is on the dev half and the published leaderboard-query count does not move; re-scoring or re-captioning M2d's published numbers beyond the `COLD-COST-TABLE` label that was always true of them; the per-world cost signal and the asymmetric corpus (§9.1–2); the warm labelled protocol and P9–P16 (§9.3); F10's host-load probe in `mvo-eval` (§9.4); `--budget-basis=predicted` as a shipped default (§9.5); coverage for escalation rules (§9.6); splitting the `commit_basis` vocabulary (§9.7); sub-millisecond cost resolution (§9.8); a control-plane-authored warm corpus (§9.9); rank-based elimination and any form of successive halving; per-world spend accounts and the deadline dispatch that would close the [starved-admission residual](M2b-adaptive-scheduler.md#residual-starved-admission); `race --reverify <world-digests>` ([F2](M2b1-budgeted-fixed-arm.md#f2)'s close, still a contract change, still the largest threat to any cross-arm number once generation is live); removing the generator prompt's `"candidate k of N"`; challenger agents (AG-6); Tier-2 strengthened suites and Tier-3 adjudication; any agent CLI invocation, any API call, any generated candidate; any sequential statistical test; the paper.
