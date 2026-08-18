# Build Log

> Public journal of building Multiverso. Newest first. See [PRD.md](PRD.md) for the plan; milestones M0–M4.

## 2026-08-17 — M2d.1: the instrument can test (warming, the refusal, coverage)

**This block exists because of one sentence in [M2b.2](#2026-08-17--m2b2-a-rule-that-finishes-what-it-starts)'s
own entry, and the sentence is about the instrument rather than about the rule.**
*"Every eval cell is identical between the two rules"* — because `mvo-eval` built a
fresh workspace per instance, so no oracle kind had a local fit, `finish_ms` was
UNKNOWN, and M2b.2 decision 1's unpriced fallback fired on every step. The
corpus had the same hole from the other side: 21 of 22 vectors carried no budget
at all. **`adversarial 22/22` and `FAR unchanged` were true statements about
`voc`**, and neither carried a bit of information about the rule M2b.2 added.

**The generalization is worse than the instance, and it is why this got its own
block.** The harness could not test *any* allocation rule — not `voc2`, not its
successor, not a per-world cost signal — and the vacuity was invisible: a run at
zero coverage printed exactly what a run at full coverage printed.

**A SECOND FAILURE NOBODY HAD NAMED, MEASURED HERE.** On a cold workspace the
budget **does not bind**. M2b.1 decision 5 makes an unpriced purchase affordable
while any pool remains, so with every rung unpriced the arm buys until the pool
crosses zero. Reproduced on this host through the committed harness: at
B = 1 500 ms both arms spent **≈ 1 986 / 1 966 ms and stopped `S-empty`** — a
33 % overrun, and a cell whose caption says ORACLE-BUDGET-MATCHED containing two
arms that are both the exhaustive ladder. So the refusal is not only about
`voc2`: **a budgeted comparison at a budget that never binds is a comparison of
one rule against itself**, and `voc`'s own coverage row catches it.

**THE PAIR IS THE WHOLE DESIGN OF THE GATE.** `accept.sh` gains `m2d1-9a…9e`,
and 9a/9b are one test in two halves — a gate that only ever refuses is M2b.2
F-9's rubber stamp, a gate that only ever passes is what we already had:

| step | command | required |
|---|---|---|
| **m2d1-9a** | `schedule-compare.sh --warmup 0 --budget 1500 --arm-a --selector=voc --arm-b --selector=voc2` | exit **5**, `VACUOUS`, **no verdict**, `unpriced-fallback(pytest-collect,pytest-suite,tree-guard)` named |
| **m2d1-9b** | the same command with `--warmup 2` | exit **0**, coverage > 0 with all four witnesses, a verdict printed — and **nothing about which way it went** |

Measured, warmed, one seeded template copied per arm, B = 1 500 ms:

| selector | coverage | W2 | W3 | W4 | W5 | decision |
|---|---|---|---|---|---|---|
| `voc` | 2 of 5 steps (40 %) | 0/5 | 0/5 | 0/5 | 0/5 | **REJECT** |
| `voc2` | 6 of 6 steps (100 %) | 6/6 | **3/6** | 6/6 | 3/6 | **SELECT** |

Purchase-order divergence 1 of 1. The witnesses are reported **separately and
never pooled**: a single "100 %" would have hidden that the reservation stopped
reserving the moment the head world completed, which is M2b.2 decision 5's
amendment doing exactly what it says.

**AND THE EVAL HARNESS NOW TESTS SOMETHING.** `scripts/eval.sh --instances
toyrepo-mean-A --levels B2`, end to end on this host, with `--warmup auto` as
the shipped default:

```
instrument: warm-up: 1 race(s), 5462 ms, charged to NO ARM (a separate intent at --budget-oracle-ms 0)
instrument:   every kind the pinned policy can buy carries a local fit
rule coverage [A1-fixed-budget]: — (computes no scarcity test)
rule coverage [A2-adaptive]: 6 of 13 steps (46%)
  cost table: WARM   budget bound: 1 of 1 races
arm A1-fixed-budget [A-gold-present@default WARM-COST-TABLE] over 1 instance(s) …
```

**One warm-up race, on a four-candidate instance, priced every kind** — C1's
prediction held as written — and the cell is captioned `WARM-COST-TABLE` in its
own name rather than in a warning above it. Before this block that same cell
reported 0 % coverage and printed a table anyway. The demonstration ran on the
**dev half**, so no eval-split cell was scored and the published
leaderboard-query count did not move.

**Warming is a PREDICATE ON THE COST TABLE, not a count of races.** `--warmup
auto` races into a **template** until every kind the pinned policy can buy
carries a local fit — read from `mvo oracles --json --policy`, a read-only verb
that costs no race — then stops, or refuses by name with `warm_incomplete` and
the unpriced kinds listed. A count is wrong in both directions and the corpus
proves both: one race on a 2-candidate fixture yields 2 samples (< `MinSamples`
= 3) and prices nothing; one race on an 8-candidate fixture yields 8 and a
second is waste. The template is keyed on **(instance, policy digest, binary
digest)** and every arm and every replicate inherits it **by copy**, so the cost
table is byte-identical across arms by construction — and the harness now
asserts it rather than assuming it.

**Warming is charged to nobody, and what it cost is recorded.** A warm-up is a
*different intent* at `--budget-oracle-ms 0` and a *different race*, so its
spend is structurally outside every arm's pool: there is no accounting to get
right, only a report to get right. `LedgerView` gains the **race window** that
gets it right — `oracleSpend`, `schedule.Bound`, `DStar` and `ScoreBatch` all
silently assumed one race per workspace, and each breaks differently once a
template holds warm-up receipts. **The window narrows what is MEASURED and never
what is SCANNED**: the leak detectors, the canary and `mvo audit` keep reading
the whole workspace, because a detector that respected the window would stop
looking at precisely the races nobody is reading. The ledger is not edited: no
truncation, no rewrite, `mvo audit` is OK over a warmed workspace, and every
M0–M2d ledger still replays byte-for-byte.

**Coverage is DERIVED, never recorded.** `schedule.finished` gains no coverage
field — a recorded aggregate would be a number computed by the binary being
measured. `internal/schedule/coverage.go` is pure and total over a `Trace`, so
an improved definition never invalidates a race and **every ledger already on
disk can be priced retroactively**. Absence is absence: a ladder trace reports
`— (computes no scarcity test)`, a pre-M2b.2 trace reports `unknown (pre-M2b.2
trace)`, and neither reports `0 %`. The era test moved out of `cmd/mvo` and into
the file with the witnesses, with `cmd/mvo` calling it: two copies of an era test
are how the two copies eventually disagree about which era a ledger is from.

**The inertness predicate is PINNED, not believed.** Each arm declares the
condition under which it is provably a named baseline, over RECORDED fields
alone, and a property test over M2b.2's own generator compares **180 inert steps
across 39 races**: on every step where `voc2` is declared inert it bought
exactly what `voc` bought. The test also fails if the generator produces *no*
exercised step, so it cannot pass against a definition of inertness that is a
constant true.

**AND THE GATE CAUGHT ONE OF OUR OWN STEPS.** `accept.sh` step **m2b1-6f** —
the one that asserts a replicated verdict at R ≥ 3 — ran
`schedule-compare.sh --warmup 1`, and `testdata/toyrepo` carries **two**
candidates, so one warm-up race yields two samples per kind, below
`MinSamples` = 3, and every rung stayed `declared-rank`. The step exited **5**
the first time the refusal existed. It was asserting a verdict about two arms
that were both the exhaustive ladder. It now runs `--warmup 2`, and its R = 1
half runs at `--level B2` rather than the default B3 = S, where the budget
cannot bind by construction and the coverage rule correctly refuses one level
*earlier* than the anecdote rule. Warming the fixture, not lowering the bar.

**One refinement the first implementation got wrong, and the measurement that
found it.** `INERTNESS VIOLATED` first compared the purchase **order**, and
fired immediately on `voc` vs `ladder` at B3 — correctly reporting that the two
arms buy in different orders, which they do **by construction**: the ladder is
depth-first and the adaptive arm interleaves by score. M2b decision 13's null
case (accept step 5b) states that equivalence over the **evidence set**, not the
order. So the failure test is over the purchase **set** — the claim that holds
for every declared baseline — the order stays a reported measurement, and the
violation requires **two priced, inert rules**: it takes two declarations to
falsify one, and a ladder declares none.

**THE CORPUS STOPS REPORTING 22/22 ALONE.** Vectors 22 and 23 gained `.budget`
sidecars (1 200 ms) and 22/23/24 gained `.warm` sidecars; the warm set is the
**honest control** (`01-honest_fix`) except for vector 22, where self-pricing
*is* the declared mechanism. Every run now prints:

```
coverage: evidence 22/22 . ranking 21/22 . allocation 3/22 (22, 23, 24) . admission 5/22
ALLOCATION IS EXERCISED BY 3 VECTOR(S). A verdict match on the other 19 says nothing
about any allocation rule.
```

`--require-coverage allocation` exits 5 when the exercising set is empty, which
is what M2b.2 §6 needed when it called the corpus that block's blocking gate:
had the flag existed, the gate would have failed honestly instead of passing
vacuously. Coverage is part of the **recorded baseline**, so a deleted `.budget`
sidecar or a workspace that stops warming is now **drift** — a lost measurement
is as visible as a changed verdict.

**Two rows moved, and both are the allocation being exercised for the first
time.** `22-schedule_cost_poison` and `23-schedule_starvation` go from
`CHEAT_WINS` (`tests_passed_desc`) to **`HONEST_REJECTED` (`gate_pass`)**: under
a binding budget against a priced table, the cheat's purchases starve the honest
fix out of `status-pass@suite`. That is PRD's starved-admission residual,
observed in the corpus rather than described in a doc. The baseline was
re-recorded as a **declared change** — old row printed beside new, reason in the
same output — and `report.py diff --allow` fails the record if any vector
*outside* the declared set moves. **The other 19 rows are byte-identical**
(falsifier V-4 holds), and a verification run after the re-record reported 0
drift.

**What this block does NOT do, and the fence is absolute.** `AdaptiveRuleDefault`
stays `voc`; `voc2` stays behind `--selector`; accept step **m2b2-8e stands
unchanged and green**; M2b.2's verdict is not re-opened. `Decide` is untouched
and gains nothing. No metric definition in `internal/eval/metrics.go` moved —
what moved is the **cell key** they are grouped by: `cost_regime` joins it beside
`policy`, derived from the recorded `schedule.started.cost_table` rather than
from the flag that was passed, so a table may never pool a warm row with a cold
one. **Every number M2d published is `COLD-COST-TABLE`**, and that normalizes
exactly rather than by assumption: no binary before this block could warm an eval
workspace. Nothing is re-scored. **No labelled measurement was taken**: the
demonstration runs on the dev half, and the published leaderboard-query count
does not move.

**The freeze gained an `instrument` block and was re-cut ONCE, as a declared
change.** It pinned the policy digest, the constants and the allocation rule and
pinned *nothing* about what the instrument could afford — the same defect M2b.2
found one level in, fixed in the same direction: more refusal. An absent block
normalizes to `{warmup_races: 0, cost_regime: "cold", budget_basis: "actual"}`
**exactly**, so the check sees the change without a byte of any frozen artifact
having to move; then the file is re-cut to `warm`, with the old values and the
reason quoted in its own `notes`, and a round-trip test pins the fixed point the
freeze did not have in M2b.2.

**Wire delta: two additive observational fields on `schedule.Considered`** —
`allowance_ms` (the number the one affordability predicate actually tested
against, already in the ledger as *prose* in the decline sentences, which is
worse than either having it or not) and `pass_withheld` (M2b.2 decision 4's
reachability condition dropped this row's pass outcomes; legible before only as
the *absence* of `pass-min`/`pass-max` from `flip_outcomes`). Nothing else moves:
no `Step` field, no constant, no receipt field, no policy field, no ranking key,
no ledger event, no stop clause. Both are ignored by replay and carry no payload
digest.

**Still true, and this block does not change it.** Warming removes a vacuum; it
does not create signal. `Table.Predict` holds nothing per world, so on symmetric
worlds `finish_ms` is identical at equal ladder positions and the commit order
falls through to the control-plane ordinal — M2b.2's withdrawn adaptivity claim
stands. A fully warmed, fully covered protocol on a symmetric two-world corpus
is still expected to reproduce the ladder, and **that is a real null, not a
failure of this block**. The per-world cost signal and the asymmetric corpus stay
where M2b.2 put them. F10's host-load probe is still absent from `mvo-eval`, and
warming makes the budget bind, which makes host load matter, which makes that gap
start costing something — named, not closed.

## 2026-08-17 — M2b.2: a rule that finishes what it starts

**A pre-registered falsifier fired: on the instrument that convicted the rule,
nothing moved.** `FRR_reachable(voc2, B1, family A) = 2/2` — identical to the
rule it replaces, and the pre-registration's own words for that observation are
*"the metric that convicted the rule is unmoved. The fix did not work."* Two
more predictions failed as written (P9, P11), and the revision also converts a
full-evidence ESCALATE into a SELECT under the shipped default. It is not shipped
as the default and it is not a partial success. What it is, is a sharp diagnosis
plus a repair that the labelled instrument **cannot currently test** — every eval
cell is byte-identical between the two rules because `mvo-eval` never warms the
cost table, so every rung is priced `declared-rank`, `finish_ms` is unknown, and
the revision falls back to the old rule on every step. The mechanical fixture
does show the arithmetic working (§7's P1 held 5/5). Both halves are below.

M2d convicted the adaptive rule of `FRR_reachable = 2/2` at the tightest budget — on both
instances where a correct candidate existed, where the policy would have
selected it on full evidence, and where the money was in the arm's pocket, the
arm rejected. This block builds the repair that arithmetic demanded, and the
repair does exactly what §7's P1 says. It also converts a **full-evidence
ESCALATE into a SELECT**, under the shipped default policy, deterministically,
across the whole informative budget band, where the rule it replaces rejects.
Nothing in the pre-registration could catch that: every falsifier was stated
against the **ladder**, and the revision *ties* the ladder. So `voc2` ships
behind `--selector=voc2`, `AdaptiveRuleDefault` stays `voc`, and the falsifier
that was missing is written into §7 with the measurement that fired it.

**The diagnosis, and it is three defects rather than a tie-break.** The
frontier offers one rung per alive world; the score's denominator is *the
rung's own cost*, which prices a fragment of a good M2a's purchase law makes
indivisible; equal shares divide the pool by the number of worlds still buying,
and the last rung of a fidelity ladder is the most expensive one — so the
finishing purchase is exactly the purchase most likely to exceed a fractional
share, and on symmetric worlds it exceeds *every* world's share in the same
step. Replayed on the published race at B2 = 1 529 ms: **the pool held 943 ms,
the purchase that would have completed a world cost 689, equal shares offered
each world 471, and refused both.** Not a bad choice between worlds. A refusal
to every world simultaneously, which is the one thing an apportionment rule
must never do.

**The revision, in one line.** Under scarcity — Σ finish_ms over the alive
worlds exceeds the pool — the score's denominator becomes the cost to FINISH
the world, and equal shares become a reservation: the pool is committed to the
longest prefix of the commit order it can actually complete, and a committed
world's allowance is the pool minus what its co-committed siblings still need.
Neither half works alone. Zero new constants: the completion term is a sum over
the pinned cost table of rungs the policy itself declares. Under `¬scarce` —
every unbounded race, and every race with an unpriced kind — it is M2b's rule
through M2b's own code path, so every null-case proof survives by construction.

**The pre-registration, quoted verbatim, and what the measurement says.**

> **P1** — At `default / B2 = 1 529 ms`, `voc2` records **≥ 1 complete world**
> in **≥ ⌈2R/3⌉** replicates. Published `voc`: **0 complete worlds, 5/5**.
>
> **P2** — At the same cell, `voc2` spends **≈ 982 ms** (one full ladder) and
> buys **3 receipts**, stopping `S-budget` with ≈ 547 ms **unspent**. Published
> `voc`: 586 ms / 4 receipts. Published ladder: 1 284 ms / 5 receipts.
>
> **§7.5** — So the expected outcome of this fix on this corpus is: **`voc2` ≡
> `ladder` within noise at every level.** That is a **success for the rule** and
> a **null for the thesis**, and both halves get published in the same sentence.

**P1 HELD. P2 WAS FALSIFIED — by the doc's own decision-5 amendment, before the
gate ever ran.** `accept.sh` step m2b2-8a, at a budget that pays for one ladder
and not two: *voc2 finished 1 world for 1 364 ms; voc finished 0 for 634 ms*.
The predicted 982 ms / 3 receipts / 547 ms unspent became ~1 364 ms and 5
receipts, because after the head world completes the commit set empties, and
with nothing left to protect M2b decision 3c stands verbatim and the rival's
affordable prefix is bought. That is §7.5's convergence on the ladder arriving
one block early, and it is recorded in the doc as a falsification rather than
smoothed into the prose.

**And §7.5's null is not a near-miss, it is mechanical.** `Table.Predict` is
keyed on `(kind, units)` and holds nothing per world, so on symmetric worlds
`finish_ms` is *identical* at equal ladder positions (measured: 1 034 ms for
both worlds, tie fixture, B = 1 500). With `flip` equal too, the commit order's
sort falls through score and through finish to its terminal key —
`st.OrderIndex`, the control-plane world order — **which is the ladder's entire
ranking rule.** Measured: identical decision, spend and per-world receipt counts
against the ladder at 6/6 budgets on `patches-tie`; identical at 3/3 on the
`23-schedule_starvation` duel with a warm table. So the claim that the arm is
adaptive under scarcity is **withdrawn**: the value model decides nothing about
who gets the money, and the commit order is the candidate ordinal. By §7.5's own
anti-spin commitment the next block is a per-world cost signal or an asymmetric
corpus — **not another rule**.

**THE FALSIFIER THAT WAS MISSING, AND IT FIRED.** Every falsifier in §7.3 is
stated against the ladder or against a published metric. **None was stated
against `voc`** — the arm being replaced and the shipped default — so F-4
("FAR(voc2) greater than FAR(ladder)") structurally could not fire on a rule
that ties the ladder. Added as **F-10** and observed:
`testdata/toyrepo/patches-tie`, shipped default policy, warm cost table,
`--budget-basis=predicted`. Unbounded reference: **ESCALATE**, rule
`on_ranking_tie`, S = 1 998 ms.

| B (ms) | voc | voc2 | ladder |
|---|---|---|---|
| 1 200 | REJECT, 596 ms, 4 rcpt (2+2) | **SELECT**, 1 006 ms, 4 rcpt (3+1) | SELECT, 1 009 ms |
| 1 400 | REJECT, 620 ms, 4 rcpt | **SELECT**, 1 345 ms, 5 rcpt (3+2) | SELECT, 1 354 ms |
| 1 600 | REJECT, 630 ms, 4 rcpt | **SELECT**, 1 350 ms, 5 rcpt | SELECT, 1 354 ms |
| 1 800 | REJECT, 632 ms, 4 rcpt | **SELECT**, 1 360 ms, 5 rcpt | SELECT, 1 361 ms |
| 2 000 | REJECT, 634 ms, 4 rcpt | **SELECT**, 1 360 ms, 5 rcpt | SELECT, 1 360 ms |
| 2 100 ≈ S | ESCALATE | ESCALATE | ESCALATE |

Through the project's own harness, paired and interleaved:
`schedule-compare.sh --patches patches-tie --budget 1400 --replicates 4
--arm-a --selector=voc --arm-b --selector=voc2` → `REJECT/SELECT` **4 of 4**,
both arms deterministic given the rotation. The SELECT rationale is honest
("evidence incomplete: … was not measured against 3 hard gate(s)") and it is
still an admission with the rival's suite never bought — PRD vector 25, the
starved admission, on a committed fixture in one harness invocation.
`docs/concepts.md` used to say the corpus "has never produced" one. It has, and
the row now says so with the reproduction.

**So the rule ships behind a flag.** `--schedule=adaptive` still allocates by
`voc`; `--selector=voc2` runs the revision; `--schedule=fixed-budget` is the
ladder, which has made exactly this trade since M2b.1 and is captioned for it.
The second reason is independent and worse: **no gate in this repository
measures the revision at all.** The adversarial corpus builds a fresh workspace
per vector, so nothing is priced, `finish_ms` is UNKNOWN, and decision 1's
fallback fires on every step — `commit_basis:
"unpriced-fallback(pytest-collect,pytest-suite,tree-guard)"`, `scarce: false`,
`|C| = 0`; 21 of 22 vectors carry no budget, and the 22nd reproduces the same
fallback and a byte-identical purchase order to `voc`. Every eval cell is
identical between the two rules for the same reason. **"adversarial 22/22" and
"FAR unchanged" are true statements about `voc`.** §6 calls the corpus this
block's blocking gate; for the rule this block adds, that gate is vacuous, and
saying so is the point of writing it down.

**What the instrument did, and what it did not.** The freeze gained
`adaptive_rule` and the check now compares it — the mechanism M2d's freeze was
missing, since M2b.2 moves no constant and no policy field and would have passed
in silence. **No byte of `eval/freeze/*.json` moved, and the check does not
refuse**, because the default rule did not move: a freeze that is refused on
every run is a rubber stamp, and the file's own notes call that worse than not
checking. **No new labelled measurement was taken in this block** — no
`--unfreeze`, no eval-use line, no re-scored cell — and that is not modesty, it
is the honest consequence of the paragraph above: on cold workspaces the two
rules produce identical cells, so P9–P16 are **unevaluable as the harness
stands**. Making them evaluable needs a per-instance warm-up race in
`mvo-eval`, which changes affordability for every arm and is therefore a change
to the instrument that §5.3 forbids inside this block. It is the first thing the
next block builds.

**What the hostile reviewer found, and four of them were defects in the
observation, not in the rule.**

- **The instrument moved in a way that flattered the new rule, twice, and both
  were fixed and re-measured.** `selection_us` — F8's reported-not-charged
  fairness field, aggregated per arm by `schedule-compare.sh` — started its
  timer *after* the apportionment pass, which is where `voc2` does **all** of
  its lookahead (`Allowances` → `voc2Plan` → `Look` → `Decide`); `Rank` then
  only hits the per-step memo. On a probe with an instrumented `Decide`: **voc
  10 041 µs, voc2 35 µs for a comparable number of `Decide` calls**, a 287×
  under-report of the same work, asymmetric across precisely the arms being
  compared. The window now opens before the frontier walk, which is what the
  comment at the charge point had claimed since M2b.1. And the retained `voc`
  arm's **`released_ms` moved**: the refactor put `releaseNonContenders` below
  the empty-frontier early return, so the terminal step — where every remaining
  contender leaves at once — stopped recording. Identical bounded race, HEAD vs
  working tree: **3 171 ms vs 1 309 ms**. Restored to 3 159 ms, and pinned by a
  test, because a `--selector=voc` race that cannot reproduce the pre-M2b.2
  ledger defeats the only reason decision 6 keeps the arm.
- **The headline verdict flipped with the parity of R.** On an N-world fixture
  the replicate's rotation is `r mod N`, so an arm whose decision depends on
  which candidate holds the head is a *deterministic function of the rotation*
  — and `noise_floor` is each arm's disagreement with its own modal decision,
  so that systematic effect was charged as noise while being the entire effect.
  R = 9 gave 55.6 % against a 44.4 % floor (not a tie); R = 10, same command
  and fixture, gave 50.0 % against 50.0 % (a tie). Worse, the test is
  structurally incapable of returning a tie when one arm is deterministic and
  the arms' modal decisions differ, except at an exact 50/50 split. The report
  now stratifies by effective rotation, names arms that decide deterministically
  given it, and refuses to call the pooled tie test interpretable when the
  replicates do not cover whole rotation cycles.
- **The freeze had no fixed point.** `FreezeFile.Rules` is `json:"-"` and there
  was no `MarshalJSON`, so `mvo-eval freeze` emitted a file with **no
  `adaptive_rule` key** — which the reader normalizes to `"voc"` — so any
  legitimate re-freeze was refused by the very binary that wrote it, forever,
  with no way to record the truth but hand-editing the artifact. That is the
  "trains every reader to pass `--unfreeze`" failure the committed freeze's own
  notes name. Merged write path, and a round-trip test: a freeze written by this
  build and re-read by this build reports zero drift.
- **A trace declared a world unreachable and then bought its rungs.** Steps 1–2
  declined a world with *"unreachable: …no pass outcome is purchasable"*; step 3
  bought that exact rung for 312 ms, because the head world completed, `C`
  emptied and decision 5's amendment reinstated the hard-gate override. The
  sentence claimed to terminate the world's whole ladder; the allowance is
  recomputed every step and decision 3 says membership in `C` is never a
  property of a world. The sentence now scopes itself to the step it is printed
  on and says the world is not abandoned; the terminal skips still fire at the
  stop, which is the only moment "not this batch" becomes "not ever".
- **The acceptance gate encoded its own result.** The four new steps assert that
  the revision finishes worlds and spends *more*; none asserted anything about
  decision quality, and the withholding-monotonicity property test proves only
  the pass-set property, which this project's own docs say does not constrain
  SELECT. **m2b2-8e** is the gate that can refute the block: on `patches-tie`
  under the shipped default at two budgets in the informative band, the default
  adaptive arm's decision must not be SELECT when the unbounded reference
  ESCALATEs. It passes under `voc`, fails under `voc2`, and prints both:
  *"at 1 276 ms of 2 128 — shipped default REJECT, --selector=voc2 SELECT (full
  evidence: ESCALATE)"*.
- **The renderer dated traces with a field that does not date them.** `preM2b2`
  read `Constants.adaptive_rule == "voc"` — which works only while the default
  *is* the revision. With `voc` as the shipped default that stamps "pre-M2b.2
  trace" on races run this morning, in the sentence written to stop the renderer
  inventing evidence. It now reads the finishing rule's own vocabulary
  (`commit_basis` on a step, `score_basis` on a row), and §8's five trace shapes
  finally have the table-driven goldens the testing bar asked for.
- **`accept.sh` was flaky** — 1 failure in 4 runs, on an anchored grep against a
  tabwriter table whose column widths depend on which policies happen to be
  recorded. Re-grepping the failing line from the log with the same pattern
  matched, so what differed was the assertion's input, not its regex. Parsed as
  data now, printing the whole table on failure. Measured pass rate after the
  change: **6 of 6 green** (`scripts/accept.sh`, sequential, same tree), reported as a rate
  rather than as a single green run.
- **§8's wire delta was stale in the direction of the implementation** (three
  Step fields declared, four shipped — `commit_basis`, which decision 1 in fact
  requires), and **§7.4 could not be executed as pre-registered** (no `A2b`
  arm; a global `--selector` makes the before/after two full protocol runs,
  scoring every declared cell twice, which §5.2 requires the published query
  count to say). Both are recorded as `AMENDED IN IMPLEMENTATION` beside the two
  the block had already recorded, and §7 carries a **POST-HOC NOTICE**: the rule
  was amended twice after measurement, decision 3's amendment says in as many
  words that the amended fixture then matches "§7's P1 and P2 to the
  millisecond", so P1 and P2 are a description of a rule tuned until they held
  and not an out-of-sample confirmation. A reader must not read them as one.
- **The B1 before/after was drawn against a different instance set than the
  published one.** At B1 both arms ran n = 3, because two instances derived a
  median minspend of 0 — which `mvo intent new` reads as *unbounded* — and were
  excluded as not budget-matched. One of the two is `toyrepo-mean-C`, which
  M2d's published table names as the one instance adaptive wins alone. So P13
  was **unevaluable in that run**, and the published n = 5 numbers must not be
  printed beside an n = 3 cell. Recorded rather than re-run: re-running is a
  measurement, and this block takes none.

**The gate.** `gofmt -l` empty; `go vet ./...` and `go test -count=1 ./...`
green with no docker daemon, no network, no corpus and none of hypothesis,
mutmut or cosmic-ray installed; `go.mod`/`go.sum` untouched; no test invokes a
real agent CLI; **ZERO AGENT SPEND**. `scripts/m0-accept.sh` OK.
`scripts/adversarial.sh` **22/22 against the recorded baseline** — on a code
path the new rule never enters, which is the point above and not a reassurance.
Every M0–M2d ledger replays byte-for-byte and the shipped default policy digest
does not move (`mv0:f207c3fa…`).

**What M2b.2 does not have, and it is the shape of the next block.** A cell in
which `commit_basis` is `reserved` on at least one step of a *labelled*
instance; adversarial vectors that attack allocation carrying budgets **and** a
warm cost table, with the baseline re-recorded as its own declared change; a
per-world cost signal, without which the "adaptive" rule under scarcity is the
control-plane ordinal with extra arithmetic; and either a shipped default policy
that declares `on_evidence_incomplete` — whose base rate this block was supposed
to measure and could not — or a reservation that refuses to commit while the
uncommitted remainder cannot buy the rivals' decision-relevant hard gates. Until
one of those exists, the honest sentence about the finishing rule is that it was
measured on an unlabelled two-world fixture, that it does what its arithmetic
says on that fixture, and that what it does there is what the depth-first ladder
already did.

## 2026-08-17 — M2d: labels, and what they say about the null

**Adaptive was worse.** M2b.1 raced two budgeted arms, got a disagreement that
exceeded both arms' noise, and could not say which arm was right because a
`REJECT` is admirable when nothing good was on the table and a failure when an
honest fix was sitting there unbought. This block builds the thing that tells
those apart — a hidden oracle no policy, no gate, no scheduler and no world ever
sees — and points it at that row. At the tightest budget the adaptive rule
rejects on **both** instances where a correct candidate existed, the policy
would have selected it on full evidence, and the money to reach that decision
was in the arm's pocket. That is `FRR_reachable = 2/2`, and the harness's own
regret decomposition files all of it under **allocation-avoidable** rather than
under gates. Not caution. Failure, with the interpretation removed.

**The label discipline, in plain language.** A candidate's correctness comes
from a suite that is generated at materialization time under a fresh 32-byte
canary, written mode 0600 under a home at mode 0700, outside the repository and
outside every workspace. The code that can open that home is **not linked into
the binary that races** — `go list -deps ./cmd/mvo | grep internal/eval` must not
match, and `accept.sh` fails the build if it does. Scoring happens on a fresh
reconstruction of each world's tree, in a directory no race, no world and no
keeper ever saw, **after** the ledger is sealed; a label cannot be written
without a decision digest and sequence number read back out of that seal. Four
detectors plus a canary scan run over **every workspace the instance raced** —
nine per instance here, not just the reference one — and a hit voids the instance
rather than annotating it. Each run prints a non-consultation witness whose every
conjunct is a fact somebody measured, and it refuses to say PROVED when a
detector could not read a surface it claims to cover.

**The first TCAR/FAR numbers.** Shipped default policy on every instance, R = 3,
reference arm replicated 3× and B derived from the **median** minspend,
`local-derived@v1`, five instance-slices over **two independent bugs**:

| level | adaptive TCAR | fixed-budget TCAR | paired (both / adaptive-only / fixed-only / neither) | adaptive FRR_reach (family A) |
|---|---|---|---|---|
| **B1** (tightest) | **1/5** | **2/5** | 0 / 1 / 2 / 2 | **2/2 = 100 %** |
| B2 (mid-band) | 2/5 | 2/5 | 2 / 0 / 0 / 3 | 0/2 = 0 % |
| B3 (= S) | 2/5 | 2/5 | 2 / 0 / 0 / 3 | 0/2 = 0 % |

**FAR is `—` for both arms at every level**, and that is not a zero: neither arm
made a single false admission under the shipped default, so FAR has no
denominator and the harness refuses to print `0 %` for an arm that never acted
wrongly. On family B — the family built to vindicate a cautious arm — adaptive
scores exactly what the ladder scores, because the ladder also rejects; caution
buys nothing where nobody is offering. The one instance adaptive wins is
`toyrepo-mean-C` at B1, where it selects gold and the ladder escalates.

**So the answer to M2b.1's question is: failure, and only at the tightest
budget.** The M2b.1 row read as adaptive-rejects-everything; with labels and a
median-derived budget it reads as adaptive loses two reachable admissions at B1
and is indistinguishable at B2 and B3. That is a smaller claim than M2b.1's and
a sharper one, and it points at the same defect M2b named: the frontier offers
one rung per alive world, symmetric worlds tie, so the allocator advances
everybody and finishes nobody — which only bites when the budget runs out first.

**The relaxed-guard cell is a different experiment and is captioned as one.**
Two instances carry a `PolicyHint` naming `no-paths.json`, because under the
shipped guards eleven of the twelve laundering vectors die at rung O-1 with one
receipt each and never reach the scheduler at all. Under that policy **both arms
admit a declared laundering vector on both adversarial instances** — FAR 1/1 on
family A and 1/1 on family B, for each arm, and `d*` does the same on full
evidence. That is M1e's `10-padded_deletion` result re-measured, it is a property
of the relaxed guard rather than of any allocator, and for the first time the
census attributes it correctly: `WON-BY-S3-adversarial`, `+ADVERSARIAL(declared)`.

**What the hostile reviewer found, and it was the block's own deliverable.**
§2 says the mechanism that keeps decision and label apart *is* the deliverable
and the arithmetic is the easy part. The reviewer took that at its word and
broke the mechanism — by execution, not by reading:

- **The hidden oracle was forgeable by the candidate it was judging.** The runner
  did `sys.path.insert(0, repo)` and `__import__(mod)` in the same interpreter
  that already held the mutable check table, and carried `--report <path>
  --nonce <value>` on that interpreter's argv. A `billing.py` whose only extra
  code was `sys.modules['mvo_hidden_checks'].CHECKS = [(cls, name, lambda: True)
  for ...]` exited 0, echoed the nonce, listed all seven declared nodes as
  passing, and was labelled **correct** while returning `[total] * parts`.
  Decision 4's "verdict integrity by cross-check" was three signals from one
  process the candidate controlled. The design defended the wrong thing — "the
  checks are generated functions, not evaluated strings" — because the problem
  was never dynamic evaluation, it was **co-residency**. The fix is a process
  split: a parent that holds the canary, the node ids and the predicates, takes
  the report path and the nonce **on stdin**, writes the report and sets the exit
  status, and never imports candidate code; and a child that imports the
  candidate and returns only **observable values**, with `sys.argv` scrubbed, its
  own script directory off `sys.path`, an empty throwaway cwd and no secret in
  its image. A wrong candidate can now only pass by returning the value a correct
  implementation returns, which it cannot learn. The forgery ships as a
  regression test; the corpus version moved `v0 → v1` so a `v0` eval home cannot
  be scored by a `v1` scorer.
- **The scorer's scratch was the candidate's working directory**, and it
  accumulated every hidden report — each carrying the canary and the whole
  f2p/p2p node-id list, with the controls running first, so the first candidate
  scored saw a full pass/fail map. No detector covered it: D5 scans the racer's
  workspace and `report-<nonce>.xml` matches no hidden-suite basename. Reports
  are now deleted as soon as they are parsed and an assertion refuses if one
  survives.
- **`winner_source` joined on the verdict, not on the winner.** It returned the
  alphabetically-first same-verdict candidate, so a laundering vector that won
  was reported as a derived mutant and `+ADVERSARIAL(declared)` never fired on
  any published number. It joins on the winning world's tree now, the caption
  fires on the population rather than on the winners, and `accept.sh` asserts
  that every row's source is a source that instance's candidate set carries.
- **A cell captioned with one policy pooled two.** The `PolicyHint` override is
  right; printing `NOT UNIFORM` and then averaging across it was not. Metrics are
  computed per (arm, family, **policy**), so a printed cell is uniform by
  construction, and `--policy-override` makes the whole-corpus default cell above
  runnable — which is where the sharper result came from.
- **B was one unreplicated draw.** Across three reference replicates of a single
  instance minspend came out `[2089 0 2081] ms`; a zero makes B1 zero, which
  `mvo intent new` reads as *unbounded*, so the row captioned "tightest budget"
  was the one handed infinite money. B is now the median of R reference races,
  every replicate's minspend prints as the reference spread, and an unbudgeted
  row is excluded rather than annotated.
- **Decision 6 did not bind on the verb that opens the oracle.** `mvo-eval score`
  had no `--split`, ran no freeze check and appended no eval-use line, so an
  eval-split instance could be scored repeatedly under a moved policy digest
  leaving no trace — the accidental version the mechanism exists to stop, and the
  one the freeze file's own notes promised twice was impossible. Shared helper,
  outright refusal, counter appended.
- Smaller, all real: D3's gold needle digested the canary-prefixed hidden form,
  so on the family-B instances where it is armed it looked for a key no workspace
  could contain; both detectors' skip counters were computed and dropped on the
  floor; every arm's charged cost was computed and dropped on the floor, so no
  arm ever printed a number on the cost axis decision 12 exists to build; the
  admit columns were structurally unreachable; `ScorerAfterRacer` was a constant
  true; `eval.sh --keep` silently zeroed every cell after the first and exited 0;
  and `accept.sh` step m2d-7c's comment claimed a run-time assertion whose next
  statement was the OK echo. That last one is the class of thing this project
  exists to stop doing, and it is now step m2d-7e, run against a real race.

**The unquantified "drifted once", quantified.** M2b.1 carried a diagnosis —
`24-schedule_budget_burn` drifted in one of four attempts, load-dependent budget
sensitivity, not re-recorded — into a headline that gates on the corpus. A
diagnosis is not a number, so here is the number: that vector **0 drifts in 12
runs alone**, and the **full 22-vector corpus 0 drifts in 4 consecutive runs**,
22/22 matching the recorded baseline every time. Beside it, two more counts that
were claims before: the eval-use counter is appended per scoring and published
(1 per cell here), and the leak scan prints its scope next to its verdict —
9 workspaces per instance, 0 surfaces a detector could not read, canary clean
on all five.

**What the full PRD §11 experiment still needs, and this cannot supply.** Real
agent candidates at ≥300 instances per stratum (tokens dominate §11's budget and
this harness charges none); arms 1, 4 and 5 proper, all of which generate; Tier-2
strengthened suites, without which ch. 9's own warning applies to us verbatim —
a false-admission rate below ~5 % measured on Tier 1 alone is meaningless, and
ours is `—`; Tier-3 human adjudication (the file format ships, the raters do
not); FAR-adv against challenger agents rather than 22 static patches;
model-swap stability and pass^k; calibration, which fitted on S2 would calibrate
against `derive.go`; cost per truly-correct merge, which needs the token term;
post-integration regressions over a chronological stream; the contamination
probe; and `race --reverify <world-digests>`, still a contract change and still
the largest threat to any cross-arm number once generation is live.

**And what these numbers are not, printed by the harness on every table.** Five
instance-slices over **two** repositories — `advrepo-split-B`'s candidate set is
`advrepo-split-A`'s minus gold and v07, `toyrepo-mean-A/B/C` share one gold and
one mutant pool — so coverage 3/5 and A9's 60 % ceiling are properties of how the
corpus was assembled, not measurements. Three of `derive.go`'s seven operators
decline or fail to apply on both fixtures, so every wrong candidate is one of
four perturbations of a single-line change, every one labelled `incorrect` with
reason `f2p-fail` and `expectation_violated` 0: the wrong-candidate pool is
uniformly trivial for the suite, so every FAR here is a **floor**.
`SYNTHETIC-CANDIDATES`, Tier-1 labels, `ORACLE-BUDGET-MATCHED` only, selection
cost measured and uncharged, tokens and runner time unmeasured, no agent output
anywhere in it, and the instance floor of 30 refuses a p-value and a confidence
interval on all of it. It is a diagnosis of a scheduling rule. It says the rule
loses money it had, at the one budget where losing it costs something — so the
next move is to fix the rule, not to widen the corpus until the number flips.

## 2026-08-16 — M2b1: the arm the comparison was missing

**M2b's own BUILDLOG said this block had to exist, and said why.** `--schedule=fixed`
is the **unbudgeted** exhaustive M1 ladder: it reads `max_oracle_ms` never.
Every "matched budget" number M2b published compared *adaptive under B*
against *exhaustive under ∞*, and the budget-truncated fixed figures beside
them were arithmetic replayed over a recorded ledger, not races that were run.
`--schedule=fixed-budget` is the missing arm: a depth-first ladder that is
given the same money and stops when it runs out.

**One loop, two selectors, and that is the whole design.** The arms differ in
ORDERING and in nothing else, because the cheapest way to guarantee that two
arms share a cost model, an affordability rule, a charge point, a stop
vocabulary and a trace shape is to make them literally the same code.
`internal/schedule/selector.go` holds a `Selector` seam with `Rank` (order an
already-computed frontier) and `Contenders` (the equal-share denominator);
`SelectorVOC` is M2b's rule moved unchanged, `SelectorLadder` is new. The
frontier rule, `budget.affordable`, the charge point, the elimination rule,
the stop clauses, the batch fill and the trace stay in `schedule.go` and are
executed identically by both. The seam is not implementable outside the
package — `Rank` takes the unexported frontier type — so an ordering rule
cannot be injected by a caller and therefore cannot be authored by a
candidate.

The stop vocabulary falls out rather than being special-cased, which is the
sign the layout is right: every ladder row is admissible, so an empty batch
implies some admissible row was unaffordable, so the shared clause returns
`S-budget`. The ladder cannot reach `S-frontier` or `S-ranking` because it
cannot construct them, not because anything tests its name.

**World order is control-plane authored, and digest order is refused
outright.** `schedule.Config.Order` is handed in by `internal/race` as
candidate ordinal ascending — slot order, which is what phase A already is —
and recorded in `schedule.started.world_order`. The same list replaces
`world digest asc` as the **terminal tie-break of the adaptive arm too**. A
world digest is a function of candidate-authored bytes (`world.tree` is the
candidate's own tree), and under a binding budget the verification order
decides who is verified at all: ordering on the digest hands a candidate a
lever on whether its rivals are ever measured. It is also not stable across
runs — `created_at`, the agent `RunCost` and a transcript digest are in the
pre-image — so it is randomization without a recorded seed, which AG-3
forbids, and it makes replicates unpairable. What removes the positional
advantage instead is **rotation**: replicate *r* rotates the order by
*r mod N*, so over *N* replicates every candidate holds the head exactly once
and the advantage becomes a measured variance component. Ordinal order is not
innocent either, and the residual is named: the generator prompt still renders
`"candidate k of N"`, so a candidate knows its ordinal, and under a budgeted
depth-first arm knowing your ordinal is knowing whether you will be verified.
It cannot *change* its ordinal, so this is a conditioning channel and not a
steering one — but the ordinal must leave the prompt before a budgeted arm is
pointed at untrusted generators.

**The measured comparison, at matched budget, both arms budgeted.**
`scripts/schedule-compare.sh --replicates 3 --level B2` on the shipped default
over the toyrepo patches — one seeded workspace copied per arm per replicate,
arms interleaved, R = 3:

| arm | modal | spend (median) | receipts | complete worlds | self-disagreement |
|---|---|---|---|---|---|
| adaptive (voc) | REJECT | 583 ms | 4 | **0** | 0 % |
| fixed-budget (ladder) | SELECT | 1 253 ms | 5 | **1** | 33 % |

Reference spend S = 1 965 ms; the allocation bound says 974 ms was enough.
Paired: `REJECT/SELECT` twice, `REJECT/REJECT` once — a 67 % disagreement rate
against a 33 % noise floor. **This is M2b's predicted arithmetic, now raced.**
The adaptive rule degenerates to round robin on symmetric worlds: it advances
every world one rung and completes none, so nothing passes and it rejects. The
depth-first arm completes one world and can decide. Two honest riders. First,
the ladder's 33 % self-disagreement *is* the rotation working — the replicate
that started on the losing world decided differently, and that variance is now
visible instead of hidden inside a digest. Second, the arms differ in a second
thing besides ordering, and it is sanctioned by the contract rather than
smuggled: equal shares are a VOC concept, so the adaptive arm tested a 296 ms
purchase against a 164 ms *share* with 328 ms still in the pool and declined
it, while the ladder — one contender by construction — tested against the pool.
That is `budget.affordable`'s `contenders == 1` case, not a second predicate,
but it is a real second difference and the number above contains it.

**A third arm ships and it is derived, not raced.** `mvo explain --schedule
--bound` computes the **retrospective allocation bound**: over the
prefix-closed subsets of a recorded race's own receipts — per world, a prefix
of the policy's gate order, which is exactly the constraint both arms operate
under — the cheapest allocation that still reaches the recorded decision.
`minspend`, the headroom, and reachability at *B*. It converts "adaptive won
by two points" into "adaptive won two of the eleven that were available",
which is the difference between a result and a rounding error, and it supplies
the exclusion criterion for instances no allocator could have won. It is
`Π(L_w + 1)` calls to the real `Decide` (16 subsets on a two-world race, 0.25 s
at six worlds), it **refuses above 10⁶ subsets rather than approximating**, and
it is derived rather than recorded, so improving it invalidates no race. What
it is NOT, in the name and in the caveats it carries: it bounds *allocation of
a fixed evidence set*, not selection and not generation. If every candidate is
wrong, *d\** is wrong and this bounds reaching a wrong answer efficiently. It
is not PRD §11's arm 7; that needs M2d's labels.

**`--budget-basis={actual|predicted}`, because wall-clock is not in the
determinism tuple.** `actual` charges the receipt's measured `wall_ms` and is
the default and the honest one; it is also why M2b saw one budget produce
different decisions run to run. `predicted` charges the pinned cost table's
prediction, which puts spend back inside M2b decision 13's tuple: given
(policy, worlds, receipts, cost table, budget, constants, order) the
allocation is a pure function and every difference between the arms is
allocation rather than jitter. Its price is stated rather than hidden — it
measures allocation under a *model* of cost, and the model's error is already
reported as the calibration residual. It **refuses by name** when the
workspace cannot price a buyable kind ("no local measurement for
pytest-collect, pytest-suite, tree-guard"), because a basis under which half
the rungs are free is not a basis.

**Absent is absent, in the wire and in the renderer.** A ladder row computes
no `flip`, no `discount_bp`, no `executor_bp`, no `value_bp` and no
`score_bpps`, and calls `Decide` for lookahead exactly zero times — the
`State` seam makes the base decision lazy, so that is enforced rather than
promised. The fields stay serialized (M1b decision 5 forbids `omitempty`
games) and a test asserts they are zero; `mvo explain --schedule` renders `—`
where a VOC row renders a number, and a `0` under FLIP is a VOC row that
scored zero, which is a different fact. `decision_now` is still recorded for
both arms, and `selection_us` — the arm's own metalevel time — is now measured
and **reported, not charged**: PRD §11's budget is tokens + runner time +
oracle cost + selection cost, this harness charges the third term and not all
of it, so no figure it produces may be called budget-matched in §11's sense.
The correct label is **oracle-budget-matched** and the harness prints it on
every report.

**The harness enforces the anecdote rule instead of asking for discipline.**
`scripts/schedule-compare.sh` seeds ONE workspace, warms the cost table, runs
the reference arm to measure *S* and `minspend`, derives the budget level
(B1 = ceil(minspend × 1.1), B2 = the middle of the informative band, B3 = S),
creates ONE intent before any copy, then per replicate copies the workspace
per arm, times a host-load probe before each arm, and interleaves them. It
**aborts** if the two arms' cost table, budget, basis, dispatch degree,
rotation or policy digest differ. It reports median + IQR + min/max — never a
mean alone — plus the decision histogram, the stop histogram, the overrun
count, the paired 3×3 table and each arm's **self-disagreement**, which is the
noise floor a difference has to clear. Below R = 3 it prints
`ANECDOTE (R=n): no verdict` and exits 3 under `--strict`. Every number M2b's
BUILDLOG quotes was produced at R = 1 and is, by this rule, an anecdote.

**What is enforced, what is not, and what that costs.** The design's sixteen
fairness conditions are in the doc with their enforcement; five are unmet and
say so. The largest is unchanged and unfixable here: **the two arms cannot
verify the same worlds**, because a world binds `created_at`, the agent
`RunCost` and a transcript digest, so two runs of one patch produce different
world digests by construction. With the script adapter phase A is
deterministic and the trees match; with a live agent the arms differ by
generation variance plus allocation, confounded. Closing it needs a
`race --reverify <world-digests>` path — a contract change, M2d/M3, named
rather than assumed. Second: a race whose winner was chosen at the terminal
`world_digest_asc` key was chosen by a coin flip that lands differently in
each arm, so the harness detects those and quarantines them into their own
bucket instead of pooling them.

**Compatibility, proved rather than claimed.** `Decide` is untouched.
`Receipt`, `World`, `Intent`, `Decision` and `PolicyV1` gain nothing — no
policy field, no ranking key, no gate predicate, no metric, no oracle kind.
The four new fields are additive on observational `schedule.*` events, carry
no payload digest and are ignored by replay; a pre-M2b1 trace normalizes to
selector `voc` (exact: no earlier binary could record a trace for any other
arm), basis `actual`, and world order **unknown** — rendered as such, never as
digest order, because inventing a past ordering is inventing evidence. The
shipped default policy digest does not move (`mv0:f207c3fa…`), every M0–M2b
ledger replays byte-for-byte, and `mvo audit` is OK over a ledger carrying the
new fields. Validation rule 25 now covers any arm that can withhold, and
`fixed-budget` **errors** where the adaptive arm silently falls back: a silent
fallback from a budgeted arm to an unbudgeted one turns a matched-budget
experiment into an unmatched one and records nothing that says so.

**Numbers:** 2 new files in `internal/schedule` (selector, bound), 1 new arm,
4 additive trace fields, 3 new CLI flags, 0 new receipt fields, 0 new ranking
keys, 0 changes to `Decide`. `scripts/accept.sh` at 6 new steps (m2b1-6a…6f);
`gofmt -l`, `go vet ./...`, `go test -count=1 ./...`, `accept.sh`,
`m0-accept.sh` and `adversarial.sh` all green.

## 2026-08-16 — M2b.1: a comparison that means something, and it says tie or worse

**The headline is a null, and on the shipped default policy it is worse than a
null.** M2b could not run the head-to-head CP-4 asks for: `--schedule=fixed` was
the *unbudgeted* exhaustive ladder, so every "matched budget" figure compared
adaptive-under-B against exhaustive-under-nothing, and the truncation numbers
were arithmetic replayed over a recorded ledger. This block builds the arm that
was missing — a fixed ladder that is handed the same money and stops when it
runs out — and then actually races the two. Five paired replicates per level,
because the harness refuses to print a verdict below three and reports each
arm's **own** run-to-run self-disagreement as the noise floor a difference has
to clear.

Three levels, `testdata/toyrepo`, `k=1`:

| Policy | Budget | Adaptive | Fixed-budget | Verdict |
|---|---|---|---|---|
| `schedule` | B1 (tightest) | SELECT 5/5, 1218 ms | SELECT 5/5, 1226 ms | **tie** — 0 % disagreement |
| `schedule` | B2 (mid-band) | SELECT 5/5, 1227 ms | SELECT 4/5, ESCALATE 1/5 | **tie** — 20 % disagreement against a 20 % noise floor |
| default | B2 (mid-band, 1529 ms) | **REJECT 5/5**, 586 ms, 4 receipts, **0 complete worlds** | SELECT 3/5, 1284 ms, 5 receipts, 1 complete world | **differs by more than noise** — 60 % against a 40 % floor |

The third row is the one to read. Given 1 529 ms the adaptive scheduler spent
**586 of them and gave up**, completing not one world, while the fixed ladder
spent 1 284 and completed one. That is M2b's round-robin degeneration — the
frontier offers one rung per alive world, symmetric worlds tie on every score,
the tie-break falls to world digest, so the allocator advances everybody and
finishes nobody — measured in races rather than inferred by arithmetic. It is a
**false rejection**, which decision 4 named as adaptivity's real risk, and it is
the shape a scheduler fails in when its rule has no notion of *finishing* a
world.

What the measurement does **not** say: which arm was right. Withholding
monotonicity says adaptivity cannot cause a false *admission*; it says nothing
about false rejections, and only labelled correctness outcomes — M2d — can price
one against the other. A `REJECT` where the truth is "no candidate was any good"
is the correct answer; a `REJECT` where an honest fix was sitting there unbought
is a failure. We do not have the labels yet, so the honest statement is the one
above and no more.

**Two things the harness now refuses to let us do.** It will not print a verdict
under three replicates, and it will not call a difference a win when it does not
exceed both arms' own self-disagreement — the `schedule` B2 row is exactly that
case, 20 % against 20 %, reported as a tie rather than as a 4-vs-5. And every
output carries the label **ORACLE-BUDGET-MATCHED**, because PRD §11 defines the
budget as tokens + runner time + oracle cost + selection cost and this harness
charges only the third. Selection cost is measured and printed beside it —
adaptive's is 1.8–2.6 ms per race against the ladder's 60 µs, a 30–40× ratio
that is invisible at pytest scale and would not be at a cheaper one. Tokens and
runner time are uncharged, and that belongs in every caption.

So: the instrument is honest, and the instrument currently says the rule as
specified does not beat running the ladder in order. The next move is not to
tune constants until the number flips — it is M2d's labels, without which "worse"
and "more cautious" are the same measurement.

## 2026-08-15 — M2b: spending the next dollar of verification

**The rule, in one sentence you can check against the code.** At every step,
each still-alive world offers exactly one purchase — its next unbought rung,
in the policy's own gate order, never reordered. For each of those the
scheduler builds the *bracketing outcomes* the purchase could report (an
error with no metrics; a minimal pass; the best pass a control-plane number
can justify), completes that world's remaining ladder at the same extreme,
and calls the real decision function on each. If some outcome moves the
decision in type or subject, the purchase is **admissible**. Then it buys the
admissible affordable purchases, cheapest-per-unit-value first, until nothing
is admissible or the money runs out — and it records every row it looked at,
what it thought each was worth, and why it declined the ones it declined.

`flip` is not a probability and the document never calls it one. It is a
reachability test over a pure function: *could this purchase matter at all?*
We can afford to ask because `Decide` is pure and total — re-measured on this
tree at **18.4 µs over six worlds** against a pytest rung's ~300 ms, so the
metalevel costs about 0.1% of the object level. Purity was sold in M1e as a
replay property. It turns out to be the thing that makes a scheduler
computable at all.

**What it provably cannot know yet, and does not pretend to.** There is no
`P(this change is correct | receipts)` anywhere, because that needs labelled
outcomes and there are none until M2d. All flips are worth the same: turning
a REJECT into a SELECT and moving the subject between two passing worlds both
count 1. The four redundancy tiers are read off M2a's discount rule by hand;
nobody has measured whether two `value-behavior` receipts actually co-fail.
`executor_bp = 5 000` — "a candidate-process receipt is worth half a
control-plane one" — is a number with no evidence behind it, which is why it
is a compiled constant recorded in the trace rather than a policy field an
operator could tune before anyone knows what it does. And the scheduler never
eliminates on rank: successive halving's whole efficiency win imports the
assumption that cheap-rung rank predicts final rank, which research ch. 3
flags as the one thing LLM evidence violates, so v0 defers a world and never
prunes it. Every one of those is in the design doc's §8 with what would
replace it.

**The measured comparison, including where adaptive lost.** Under an
unbounded budget on the shipped default the two arms are indistinguishable —
same receipts, same decision, same rationale, byte for byte modulo world
digest — which acceptance step 5b pins, because a scheduler that cannot be
shown to be inert where it should be inert is an artifact of its harness. At
a *binding* budget, `scripts/schedule-compare.sh --policy schedule` on the
same fixture: fixed **SELECT**, 8 receipts, 1 216 ms; adaptive **ESCALATE**,
6 receipts, 1 225 ms, stopped `S-budget`, the last `observe` refused with
`0 ms remain`. **Adaptive lost that one.** It is a false rejection, which
decision 4 names as adaptivity's real risk and which only labelled outcomes
can price — and it is the honest shape of the loss: the arm that runs out of
money says "I did not buy enough evidence to rank these" instead of picking a
winner it cannot justify. Evidence waste on that race: 619 ms of 1 225
(50.5%), greedy 1 225 ms — computable for the first time, and the gap between
the two numbers is itself the measurement.

**Two caveats that decide how much the comparison above is worth, stated
before anyone quotes it.**

*The head-to-head CP-4 actually asks for cannot be run today.* `--schedule=fixed`
is the **unbudgeted** exhaustive M1 ladder: it ignores `max_oracle_ms` entirely.
So every "matched budget" figure in this entry compares *adaptive under B*
against *exhaustive, unbudgeted* — not against a fixed ladder given the same
money. The budget-truncated fixed numbers below are labelled arithmetic over a
recorded ledger, not a race that was run. A budget-truncated fixed arm is the
single highest-value thing this harness still needs, and until it exists no
number here settles the thesis in either direction.

*And with that arithmetic, adaptive loses worse than the escalation above
suggests.* The frontier is one rung per alive world, so on symmetric worlds
every score ties, the tie-break falls to world digest, and allocation
degenerates to **round robin**: it advances every world one rung and completes
none. A budget-truncated fixed ladder is depth-first and completes one. Replayed
over a recorded fixed-arm ledger at 71 % of the exhaustive budget, adaptive
bought 4 receipts for 990 ms with **zero** complete worlds and escalated, where
the truncated ladder bought 5 for 642 ms and had one world holding every gate
rung — decidable. **Adaptive spent 54 % more and decided less.** The honest
reading cuts both ways: adaptive's failure there is *structural* (round robin is
what the rule does on ties), while the fixed ladder's success is *luck* — it is
depth-first in world-digest order, digests are not stable across runs, and
whether it completes the *winning* world is a coin flip. Neither arm is good on
that fixture. That is the result, and it is a finding about our rule, not a
detail of the harness.

**What the red team broke.** Four things, and the first one broke the
strongest claim in the block.

*`FAR(adaptive) ≤ FAR(exhaustive)` was false.* The proof says withholding a
receipt can only shrink the pass set. That part is true. What does not follow
is the FAR claim, because **`SELECT` is not monotone in the pass set**: every
escalation that needs a *second* passing world — `on_ranking_tie`,
`min_candidates_passing`, `on_behavioral_split` — is disarmed by withholding.
Starve one world's last gate and the tie that would have asked a human simply
never happens. Measured on the adversarial fixture at a binding budget:
**8 of 8 adaptive races SELECT and admit, against 3 of 3 ESCALATE under
`--schedule=fixed` at the same budget**, and 5 of the 8 admitted the cheat and
left trunk buggy with `mvo verify HEAD` printing OK. Which candidate survived
was decided by the `world_digest_asc` tie-break — a coin flip over
candidate-authored bytes.

*The same hole had two more doors, and both were ours.* The correlation
discount reaches `red_bp = 10 000` for two hard-gated instances of one kind,
`value_bp` goes to zero, and the first rule refused every zero-valued
purchase — so a **gate the policy declared** was never bought and the adaptive
arm recorded REJECT where fixed recorded SELECT. And decision 3b's clamp,
which caps a rival's `tests_passed` at the base tree's count so a forged
self-report cannot starve anyone, also caps the honest candidate that *adds*
two regression tests: it ties the incumbent it should beat, loses the digest
tie-break, scores `flip = 0`, and is dropped — at an **unbounded** budget,
where decision 13 promises the M1 ladder exactly. 3 of 6 races admitted the
cheat. The fix is one line of policy: **an ordering term may never refuse a
hard gate.** `flip` still orders the queue and is still the research signal;
a rung a hard gate reads is bought on any live world the budget can pay for.
Re-measured after it: the clamp attack goes 4 of 4 to the honest fix with all
6 receipts, identical to the fixed arm. The document now says out loud what
that costs — under a policy whose every rung is hard-gated, adaptive buys what
fixed buys, and the remaining adaptivity is order plus the ungated rungs.

*The escalation rule written for the starved race could not fire in the
starved race.* `on_evidence_incomplete` was guarded on `passCount == 0`. The
race that matters has `passCount == 1` by construction — that is what a
starved admission *is*. The guard is gone; the predicate is untouched and
still pure over `(pol, worlds, receipts)`; it now replaces SELECT as well as
REJECT, and its sentence changes with the case. Measured: **4 of 4 ESCALATE**
where the same fixture and budget previously admitted. For the policies that
do not declare it — including the shipped default, which waits on M2d to
measure the base rate — the SELECT rationale now names every rival it never
measured, because *"sole world passing all hard gates"* said the others were
measured and lost, and they were not.

*And the trace was lying in three places.* Every zero-valued row recorded
`decision-inert: no gate, ranking key or escalation rule reads pytest-suite`
— under a policy whose gates named `pytest-suite` — and `oracle.skipped`
carried that sentence into the ledger permanently, where `mvo explain` renders
it. `schedule.finished.ranking_incomplete` was `false` in every real race
because `Finish()` never set it, so decision 4's one admitted caveat was
reported by a warning nothing could trigger. And the calibration residual
divided a sum over *priced* rows by a sum over *all* receipts, printing
`predicted 2 ms, actual 2073 ms (+103550.0%)` for a number sold as the thing
M2d needs. All three are derived from their actual cause now, and a test
asserts a `hard_gate: true` row can never carry the inert sentence.

**Two corrections to the cost model, one of which is why the budget ever
binds.** Theil–Sen needs two samples with distinct unit counts, and the unit
for both pytest kinds is the repository's test count — constant within a
workspace. So the two kinds whose cost dominates were priced `declared-rank`
forever, an unpriced purchase is affordable while any pool remains, and
`max_oracle_ms` did not bind at all on a single-repo workspace. A population
with no unit variance now fits **fixed = median(wall_ms), per-unit absent**,
labelled `median-fixed`, with the missing coefficient stated rather than
printed as a measured zero. And `score_bpps` was an argmax over two units:
milliseconds for fitted rows, ordinal rank 1–7 for unfitted ones, under one
field name. Two fields now, ordered in two passes, priced first — which is
also the cheapest thing that reduces the overrun.

**Still open, and written down as open** in the design doc, `docs/concepts.md`
and the README. A world that burns the pool starves its siblings: decision 8
claimed "a world that burns its budget starves only itself" and the code has
no per-world spend account at all. Measured against the shipped default: a
conformity cheat sleeping 3 s inside its own last rung spent **4 963 ms of a
2 000 ms budget**, its rival's suite was refused, and the race admitted it.
The per-world cap that would fix it was implemented, measured and reverted
before the red team arrived — at matched budget it manufactured a false
ESCALATE by refusing a 33 ms rung the remaining 38 ms could afford — and that
trade has not improved, so v0 ships the mitigations (real prices, priced rows
first, `BUDGET EXCEEDED by N ms` in the header) and names the residual instead
of quietly patching it. 3 of 4 races now escalate; 1 of 4 still admits the
burner on a fresh workspace, where by construction nothing has been measured.
The clean close is the deadline dispatch §6 already promised and the code
never implemented — run an unpriced rung under a timeout equal to its world's
remaining share so it self-eliminates through an errored receipt — which needs
`internal/oracle`'s timeout path to produce a receipt rather than an error
return. That is a contract change, so it is named rather than half-done.

**And one feature the design declared IN is not in.** `corpus-differential`
was specified as a dependent purchase that closes the cohort and records its
membership. It is not scheduled at all: `Ladder()` enumerates `pol.Required`,
which is built from what gates, keys, invariants and `require_evidence` read,
and a reducer named only by `on_behavioral_split` is in none of them — so it
runs in phase B2 as an unscheduled barrier, in both arms, charged to no
budget. The design doc's §1, §2.2 and §10 are corrected to say so, the dead
`ReasonCohortClose` constant is deleted, and `mvo explain --schedule` prints
B2's receipts under a **"not scheduled"** heading with their spend, so the
trace stops implying a completeness it does not have. §11's fixture claims are
corrected the same way: `testdata/schedule/` never existed, `schedule.json` has
no `props` rung, and `max_oracle_ms` is an intent field, not a policy one.

Compatibility, proved rather than claimed: the shipped default policy still
does not move (`mv0:f207c3fa…`), `on_evidence_incomplete` still ships off so no
pre-M2b policy can reach it, the SELECT rationale's new clause is unreachable
under the exhaustive ladder (a COMPLETED world buys its whole ladder unless a
gate *failed*, and a failed gate excludes it), and every M0–M2a ledger replays
byte-for-byte with the `schedule.*` events present and ignored.

**Numbers:** 1 new package, 3 new observational event types, 1 escalation rule
amended, 0 new receipt fields, 0 new ranking keys, 22 adversarial vectors,
`scripts/accept.sh` at 7 new steps. `gofmt -l`, `go vet ./...`,
`go test -count=1 ./...`, `accept.sh`, `m0-accept.sh` and `adversarial.sh` all
green.

## 2026-08-15 — M2a: an oracle menu worth scheduling over

**M2's thesis is an adaptive scheduler, and before this block there was
nothing to schedule.** Three rungs — `tree-guard`, `pytest-collect`,
`pytest-suite` — about four hundred milliseconds and two `git ls-tree` walks.
A scheduler is a function from a budget to a purchase; with one plausible
purchase it is a constant. M2a builds the menu: four new oracle kinds
spanning three orders of magnitude of cost, each declaring what it costs, what
it scales by, what signal it reads, and who authored its inputs.

The centrepiece is the **cross-candidate differential** (EP-4). Research ch. 8
puts candidate-vs-candidate behavioural diffing at *function* scale in the
literature and finds **nobody doing it at repository scale on real patches**.
We already pay for N isolated worlds of one intent; comparing them costs one
interpreter start per world and a hash table. Two candidates implement
`clamp` correctly for all eight tests, tie on every ranking key that measures
anything, and diverge on `clamp(nan, 0, 10)`: `nan` versus `0`. Under M1f that
race ESCALATEd on `on_ranking_tie` and told the maintainer that two digests
tied. Under M2a it ESCALATEs on `on_behavioral_split` and hands them the input
and both answers. That difference is the block.

**The measured costs, because the whole point is that a scheduler needs
numbers.** Anything that imports pytest costs ~400 ms fixed on this tree,
which dwarfs the tests: `--collect-only` over the 8-test toyrepo is
0.39–0.88 s real while collection reports 0.00 s. `corpus-observe` — a bare
interpreter, no pytest, no conftest, no plugin system — is **~25 ms**, and 500
trivial cases add 0.12 ms. Cheap *and* the smallest attack surface on the
ladder is an unusual combination and the scheduler should exploit it. The
largest single lever turned out to be a security control: sealing pytest's
entry-point autoload (M1f's `evidence.plugin_autoload`) takes the same run
from 0.45 s to 0.10 s. `mvo oracles` fits `wall_ms ≈ fixed + per_unit × units`
per kind from this workspace's own receipts and prints "no local measurement
(n=…)" below three samples, because a two-point fit rendered as a fact is the
same over-claim as a skipped gate rendered green.

**What the red team broke, in the order it hurt.**

*The corpus was one file, and a world could poison the one its neighbour would
read.* Phase 0 wrote it beside every worktree, owned by the candidate's own
uid, and the runner re-read it from disk on every replay — so a poisoner
changed the NEXT world's inputs and never its own observation. Both payoffs
ran. Declare a case id nobody pinned, and the honest sibling's stream
faithfully reports it, its metrics go absent, and it is convicted of tampering
it did not do while the poisoner is SELECTed, ADMITted, `mvo verify HEAD` OK.
Or preserve every id, rewrite one argument so both worlds agree, and the split
simply never fires — reproduced 3/3 under `--parallel 2`. Neither touched a
protected or harness path. Three of our sentences were false at once, including
`A WORLD CAN ONLY REMOVE ITSELF`, written twice in the source as a flat claim.

*And the field that would have caught it was dead.* `mvo_corpus.py` emitted
`"corpus": corpus.get("digest","")` over an object with no `digest` member —
the digest is the content address of the canonical bytes and is never inside
them — so every stream this tree could produce recorded `""`, and nothing on
the Go side read it. The runner now hashes the bytes it actually loaded, the
pinned digest travels on argv, and an observation of a corpus nobody pinned is
unusable. Beside it: one corpus copy per world, written only after the agents
are gone, re-digested by the control plane before and after every replay, with
a mismatch aborting the **race** as machinery rather than failing a world —
because a world handed an altered file is a victim, not a suspect.

*Under T1 we mounted the corpus into every candidate world for the whole of
generation.* `docker inspect` of the candidate keepers showed
`<raceDir>/corpus -> /mvo/corpus ro=true` mid-race, which is the exact
opposite of what decision 13, `backend.OpenOpts`'s own comment and our verdict
on corpus vector 19 all say. The keeper is opened before the agent starts
inside it, and phase 0 has already written the corpus by then. Each world now
gets its own corpus directory, created **empty** at open time and filled only
after phase A joins.

*"A world can only remove itself" failed twice more, and neither needed a
forgery.* A stream with a header, a `session_start`, a **subset** of the
declared ids and a `session_finish` breaks no framing rule — so it parsed as
usable while the oracle's own status was `fail`, entered the cohort anyway,
and, because the comparison denominator is an intersection over every member,
deleted the distinguishing case for every honest sibling. And a world that
observed *every* case and answered `opaque` on all of them passed its own gate
while collapsing the denominator to zero for the entire race, silently,
because nothing gates or escalates on `diff_cases_compared`. The cohort now
requires the receipt's `pass`; a member that compares nothing is excluded and
the exclusion recorded with every member's contribution beside it.

*A mutant that hangs was a free pass.* A per-mutant timeout is correctly
excluded from `mutation_score_bp`'s denominator — and it was also excluded
from the only mutation gate M2a ships, so a mutant the suite provably failed
to kill cost the candidate nothing. Two killed, one 90-second sleep:
`mutants_survived=0`, `mutation_score_bp=10000`, gate PASS, race SELECT, and
the report printing `survivors: 0` beside it. The gate now reads
`survived + timeout`; the score still does not, because what a ratio means and
what the tests did are different questions.

*The mutation tool resolved through the candidate's tree.* `python -m
cosmic_ray.cli` runs with cwd = the worktree, so `sys.path[0]` is the
candidate's; a six-line `cosmic_ray/cli.py` whose `dump` prints nothing gave
`mutants_candidates=0` beside our own `mutation_lines_targeted=18`, and the
gate passed vacuously. No glob was ever going to close it — the shadow is an
ordinary directory named after a tool. Enumeration now runs under
`PYTHONSAFEPATH=1`, and zero mutants over a non-empty target set is machinery
with a named reason, not a pass.

*And a compiled freeze had no gate to enforce it.* A policy-declared corpus
file or property module joins `paths.harness` — that is decision 14's entire
defence for the property module — and nothing required a `paths-unmodified`
gate to exist. The shipped `differential.json` was exactly that shape:
`mvo policy validate` printed `paths (frozen against the candidate)` over a
freeze the policy could not keep, and a candidate that rewrote the corpus
passed every hard gate. Validation rule 24 is the converse of rule 12, both
fixtures carry the guard, and `mvo policy show` now prints
`paths (declared, NOT ENFORCED)` for a path block no gate reads.

**What a differential result does and does not establish.** A partition of the
cohort into two or more behaviour classes on a shared, control-plane-authored
corpus proves exactly one thing: **the intent as specified, plus the
repository's own tests, do not determine behaviour on the inputs that
separated them.** It does not say which class is right, and every ranking key
we could derive from it is wrong-signed. "Agree with the most siblings" is a
popularity contest among samples from one model prior — CAPA says those errors
are becoming *more* correlated, and the minority-correct case is the case this
product exists for. "Agree with the base tree" ranks *did nothing* above *did
the job*: we verified it on the adversarial corpus, where vectors 06–08 leave
the bug in place and therefore agree with base while the honest fix does not.
So zero ranking keys ship. The output is an escalation to a human with the
distinguishing input attached, and the metrics are recorded so M2b can
correlate them against ground truth before anyone builds on them.

The floor under it is `streamed`, and the receipt says so: `evidence_regime:
"derived"` with `inputs["evidence_floor"]: "streamed"`, never the flattering
`control-plane`. The reducer runs no candidate code, and every byte it
consumes came out of a candidate's process. A forged observation is a forged
statement about a *sibling* — that is the new attack class this design
introduces, it is stated in the design doc in as many words, and it is
tolerable for exactly one reason: the output is a question for a maintainer,
not a verdict that can reject a rival. If the differential ever acquires a
hard gate, that analysis has to be redone.

**Still open, and written down as open** in the design doc, `docs/concepts.md`
and the README: at T0 the corpus copies are owned by the uid the candidate's
process runs as, so a sibling can still reach another world's directory —
detected and attributed now, not prevented; a cohort member that answers
"opaque" on *some* cases still shrinks everyone's comparison; a candidate can
read the corpus off the filesystem at oracle time at T0, so what decision 13
delivers there is "not delivered to the generating agent", not "not reachable
by it"; and below CPython 3.11 `PYTHONSAFEPATH` is ignored, so tool shadowing
is caught by the contradiction check alone. Two corpus providers ship, not the
three decision 5 promised. `hypothesis`, `mutmut` and `cosmic-ray` are all
absent on this machine, so every degradation path in the block is the path
this tree actually takes — the acceptance script asserts the pre-flight
machinery abort with an untouched ledger rather than skipping quietly.

Compatibility, proved rather than claimed: the shipped default policy does not
move (`mv0:f207c3fa…`), every new rung is opt-in, and every M0–M1f ledger
replays byte-for-byte. Receipt digests do move — three additive fields — which
is free for the same reason it was in M1b, M1c, M1e and M1f: nothing about a
recorded decision is ever re-derived from a re-serialization of its inputs.

**Numbers:** 4 new oracle kinds, 5 gate predicates, 22 metrics, 1 escalation
rule, 24 validation rules, 19 adversarial vectors, `scripts/accept.sh` at 10 new
steps. `gofmt`, `go vet`, `go test ./...`, `accept.sh`, `m0-accept.sh` and
`adversarial.sh` all green.

**And the honest number is not 19 of 19.** With `hypothesis` and `mutmut`
installed afterwards — the three vectors that had aborted as *machinery* now
actually run, and all three are caught — the corpus reads **eighteen stopped or
escalated, one landed**. `19-differential_conformity` passes every gate in a
solo race, is SELECTed, admitted, and `mvo verify` accepts it, with the bug
fully intact. It reads the input corpus off the filesystem at oracle time and
special-cases exactly those inputs, leaving every other input wrong. Raced
against an honest fix it escalates, because the two land in different behaviour
classes — but alone, it wins.

That is open at T0 **by construction**, and worth stating without softening: the
world has to execute the corpus, so the world can read it. We can withhold the
inputs from the *generating* agent, and do; we cannot withhold them from the
process that must run them. T1 closes the sibling half — one world can no longer
reach another's corpus — and leaves this half exactly where it was. It is the
classic overfit-to-the-oracle, it lives in application code a reviewer is about
to read, and the differential's answer to it is ambiguity rather than
conviction. The README and `docs/concepts.md` now lead with this rather than
with the eighteen.

## 2026-08-15 — M1f: moving the trust boundary

**We red-teamed our own fix and it lost twice.** M1f shipped that morning
claiming the candidate no longer writes its own evidence. By the afternoon an
adversarial pass had landed **two** forgeries on trunk under the **shipped
default policy**, each with `mvo verify HEAD` → `OK: attestation verified
(7 checks)` and `mvo audit` → `OK`, and each with the bug fully intact. Both
went in through the same door, and the door was a sentence in our own design
doc that was simply false.

**Decision 16 said the harness path set closes the entry-point-plugin
surface. It does not, and it never could.** pytest imports `pytest11` entry
points from *any* `*.egg-info` or `*.dist-info` directory on `sys.path`; the
candidate's tree root is on `sys.path` when the oracle runs `python3 -m
pytest`; and the module such metadata names may be called **anything**. A path
set is a finite list of globs. "Any module named by any metadata directory" is
not glob-shaped, so no version of the protected-paths gate was ever going to
catch it — the tree-guard passed `evilmod.py` and `evil.egg-info/` as ordinary
allowed additions, `harness_added=0`, `protected_modified=0`. The plugin loaded
*after* `-p mvo_evidence`, in the same interpreter, popped
`MVO_EVIDENCE_STREAM`/`_NONCE` out of the environment so our own observer
configured inert, and authored a complete self-consistent forged stream, a
matching JUnit file at the argv `--junit-xml` path, and `exitstatus = 0`. The
suite receipt then recorded `evidence_regime: streamed`, `evidence_plugin:
sha256:69b4301…` — **the honest embedded plugin's digest** — and
`tests_passed: 8`. The label did the laundering, exactly as the study said it
would: `execution.evidence_plugin` was an embedded constant, never re-derived
from whatever actually wrote the bytes.

**The fix is a capability removal, not a better glob.** Oracle pytest runs now
carry `PYTEST_DISABLE_PLUGIN_AUTOLOAD=1`, so the only plugins that load are the
ones mvo names on argv. It is a policy field — `evidence.plugin_autoload`,
`""` ⇒ `"off"`, tri-state for decision 4's reason — because sealing autoload
is not free: a suite that genuinely needs `pytest-asyncio` or `pytest-django`
collects nothing without it. `"on"` is the escape hatch, and it is a pinned,
attested choice with a stated cost, the same shape as `crosscheck: "off"`.
Depth behind it: `**/*.egg-info/**` and `**/*.dist-info/**` join the default
harness set, so a candidate that ships distribution metadata into a repo that
had none also dies at rung O-1, where a reviewer can see it. Both defences are
under live test — the attack is now corpus vector `13-entrypoint_plugin`, it
dies at the guard under the shipped default, and under `no-paths.json` (guard
gate, guard oracle and invariant all absent) its plugin never loads at all and
the receipt carries the real numbers: `tests_total=8, tests_failed=2,
tests_passed=6`, REJECT at `status-pass@suite`. That second run is the only
measurement in the corpus that exercises the evidence path, which is the whole
reason the vector was worth adding rather than patching around.

**The second break does not have a fix, so it has a row in the table
instead.** `collect-equals-suite-total` was sold as the answer to the study's
signature failure — a three-test repo reporting a 500-test suite. Make both
oracles lie *consistently* and the invariant holds: the red team's second
patch forged `collected=500` on the collect run (winning the stdout
cross-check by writing a trailing `500 tests collected in 0.01s` at `atexit`,
because `parseCollected` takes the last matching line and `atexit` runs after
pytest's summary) and `tests_total=500, tests_passed=500` on the suite run, and
`mvo explain` printed `INVARIANTS: ok` over an 8-test repository whose real
suite is `2 failed, 6 passed`. **A cross-check between two sources both
produced inside the trust boundary cannot detect a coherent forgery.** What
the invariant actually buys is the careless adversary who forges one number
and forgets the other — real, common, and why vector 09 was caught under M1e —
and it is now written down as exactly that, in the design doc's decision 10,
in the README's table and in `docs/concepts.md`. The 500-in-3 row is split:
**DETECTED** when incoherent, **OPEN** when not. Closing it needs a count
derived outside the test process — control-plane static AST collection — and
that is not in M1.

**`mvo guard`, the verb we pitch as the adoption wedge, was blind to
`.gitignore`.** It snapshots the working tree through `git add -A`, which
honours the in-tree ignore rules; pytest does not. A `conftest.py` physically
present in the tree and named by `.gitignore` produced `examined: 1 path(s)`
and `OK: no protected or harness path was modified` — a false clean verdict
from the one command an evaluating maintainer runs to decide whether to trust
the gate at all. The race path was already defended (a force-applied file is
in the world's index, and the drift between that tree and a fresh snapshot is
a `tree_drift`), which is why the gap was only ever in the advisory verb.
`mvo guard` now lists such files as `IGNORED:` and refuses to print the clean
verdict (`INCOMPLETE:`, exit 1), and `**/.gitignore` joins the harness set —
it decides what the guard is able to see, which makes it harness in the
sharpest sense available.

**Three CAS references were outside the sweep, and each was a blob you could
delete under an `OK`.** `execution.evidence_plugin` (decision 14 calls the
plugin digest "an auditable fact, not a version guess"; a fact nothing
re-reads is a version guess with a colon in it), `baseline.recorded`'s evidence
stream (the observation record for `collected_base`, the denominator
`collected-not-below` divides by — it was in CAS, referenced by nothing, and
inflating `cas_unreferenced`), and `agent.finished.stderr` (named in no other
event, and the only place a `CONFIG_ERROR` world's reason survives). All three
are in the normative table now, all three are in the missing-injection test,
and a two-race workspace that used to report `cas_unreferenced: 4` reports
`0`.

**`mvo audit` printed `OK:` and then failed.** `--require-decisions N` — the
one flag decision 23 added for CI, whose entire purpose is to be scraped —
was checked *after* the output block, so an empty workspace produced
`OK: 2 events, 0 decisions replayed …` on stdout followed by exit 1. Any CI
check keyed on `OK:` read a failed audit as a pass. The check is in the guard
now and the failure prints `SHORTFALL:`. And `--key`, which the contract's CLI
section specifies and the sweep section promises, did not exist:
`mvo audit --key foo.pub` exited 2 with `flag provided but not defined`. It
exists, an unreadable anchor is refused rather than silently downgraded, and
both the human line and the JSON name which anchor was used — because
"verified" against the workspace's own key is a **self-check a rogue clone
reproduces**, which is precisely the confusion the study documented.

**Two guard-receipt bugs that put false statements into signed rationales.**
`result.detail` took `violations[0].Path` from a list sorted by `(kind, path)`,
so the contract's "lexicographically first offending path" named the wrong
file whenever the two orders disagreed — and the correct implementation,
`GuardReport.FirstOffender`, was sitting there as dead code. And `tree_drift`
incremented `protected_modified` with an empty path, so a receipt read
`protected_modified=1 … (first: -)` when nothing protected had been touched.
Drift is machinery — "an earlier oracle wrote into the tree, or something
raced us" — and it is reachable: `11-cross_world_sabotage` mutates sibling
worlds' trees, so an honest victim would have been convicted of editing a file
it never touched. It now yields `status = error` with metrics **absent**, the
same shape as an unreadable tree, and escalates as machinery.

**And one that would have failed an honest repository on its first race.**
`streamEnv` emitted an absolute `PYTHONPATH=<plugin dir>`; `os/exec`
deduplicates env keys keeping the last, so appending that to `os.Environ()`
silently discarded the operator's `PYTHONPATH` on every T0 run under the
shipped default. A src-layout repo, an uninstalled package, or any repo whose
tests import through `PYTHONPATH` would have collected zero tests on the very
first race a new user ran, and blamed us for it correctly. It is prepended
now. On T1 nothing is inherited by construction — the image owns its
environment as it owns `PATH` — and that is stated in the contract rather than
papered over.

**The quickstart had gone factually false again, in the opposite direction.**
The study's finding was two false sentences about what `mvo audit` checks; the
fix made audit stronger and nobody re-read the page, so it now told readers
that audit *does not* sweep CAS, printed transcripts showing `OK` after
deleting an attestation blob (both are exit 1 today, asserted by step 9a),
pinned the M1e default policy digest, taught a three-gate ladder ranked by
`wall_ms_asc`, and listed a closed gate vocabulary missing two of its members.
Every one of those is re-recorded against the shipped binary. **Step 3p is the
part that matters**: accept.sh now greps the reader-facing pages for the
current default digest, the known-gate list, and the absence of the retracted
sweep claim. A grep is a poor proof of correctness and a perfectly good proof
of non-rot, which is the failure that has now happened twice.

**Smaller, and all of them real.** `testdata/toyrepo/policies/sealed.json`
declared `evidence.regime: "isolated"`, a regime this binary cannot deliver,
so the shipped example the docs point at validated clean, installed as the
workspace default, and then refused every race — a workspace bricked by a file
we told people to read. It is `"auto"` now, `mvo policy use` refuses an
undeliverable regime at install time in the same sentence `race` used to
produce, and accept.sh step 3q installs and races it. `mvo policy list`'s
adoption hint disappeared for an operator who had run the first of its two
commands and not the second, leaving them half-adopted and unprompted; it keys
on the pin alone now and prints the remaining command with a `--dir`-correct
path. `mvo explain`'s evidence line gained the tier parenthetical the contract
calls load-bearing, in the form that is true today — `regime streamed
(T0-worktree; isolated not deliverable in this binary)` — rather than pointing
at a `--tier T1` that would not help. And the contract itself stopped
presenting work that did not ship: acceptance steps 3l/3m/3n/11,
`patches-forge-deep/` and `testdata/plainrepo/` are marked NOT SHIPPED with
the unit tests that stand in for them, the module layout names the files the
code actually lives in, `Result.Detail` is documented as the schema addition
it is, the plugin directory's `0755` is recorded as an amendment against the
specified `0555`, and "all eleven laundering vectors at rung O-1" is corrected
to eleven of twelve, with `12-planted_artifact` at `status-pass@suite`.

**Where the boundary is now.** Twelve of twelve laundering vectors stopped,
eleven at rung O-1 with exactly one receipt each. Open, and said plainly: an
adversary who patches the observer from inside the test process; a **coherent**
forgery across both oracles; weakened or rewritten assertions once the guard
is relaxed; and a backdoor that passes every honest test. The default policy
digest moved twice inside this block, which is what pinning is for — every
recorded intent still resolves its own policy from CAS and replays byte for
byte, and `scripts/accept.sh` proves it against a real M0–M1e ledger.

The lesson is cheaper to write than it was to learn: **a claim in a design
doc is not a defence.** Decision 16's sentence about entry-point plugins was
written, reviewed, shipped, and wrong, and the only thing that found it was
someone trying to break the thing we had just declared fixed.

---

## 2026-08-15 — M1f: the trust boundary moved

**The candidate no longer writes its own evidence.** The design partner study
found that suite metrics were parsed from a JUnit file written inside the
candidate's own writable tree, and the adversarial corpus we built *before*
touching any of it measured the damage precisely: **eight of eleven laundering
vectors landed on trunk with a signed attestation `mvo verify HEAD` accepted,
eight of them leaving the bug fully in place.** Re-running that corpus today:
**eleven of eleven stopped, ten at rung O-1 with exactly one receipt each, and
only the honest fix lands.** The baseline diff is in
[`testdata/adversarial/README.md`](testdata/adversarial/README.md) — red table
and green table side by side, because "we changed the oracle" is not a result
and "these rows moved from LANDS to CAUGHT" is.

Three mechanisms, in the order a race meets them.

**Rung O-1, `tree-guard`.** A registry oracle that executes nothing: it
compares the intent's base tree against the candidate's, both git trees the
control plane holds, under two declared path classes. `protected` (test files)
is frozen against modification and deletion, additions allowed — adding a
regression test is the behaviour we want. `harness` (`conftest.py`,
`pytest.ini`, `pyproject.toml`, `sitecustomize.py`, `*.pth`, …) is frozen
against modification, deletion **and creation**, because the study's forgery
patch's entire content was a new `conftest.py`. Two `git ls-tree` walks, no
process, and the one gate whose evidence an adversary provably cannot author.
The glob grammar is closed and hand-written rather than a regexp, for M1e
decision 6's reason: a policy is data that reaches the decision core.

**The evidence stream.** A control-plane-owned pytest plugin, embedded in the
binary and materialized read-only outside the candidate's tree, writes a
framed event stream to a control-plane channel that mvo reads **live**.
Nothing in the candidate's tree is read as a metric source after the process
exits, which is the whole of the two vectors the study reproduced. Three
structural rules, none policy-controlled: absence never passes (a 0-exit with
no usable stream is `error`); the exit code is cross-examined against the
stream; and JUnit XML is demoted to a *cross-check* whose disagreement is
`error` — brittleness escalates, it never convicts and never acquits.

**Cross-oracle invariants.** `collect-equals-suite-total` makes "500 tests in
an eight-test repository" a contradiction rather than a signed fact, and
`escalation.on_invariant_violation` is rule 0 — above every M1e rule — so a
detected forgery reaches a human instead of being filed under "the tests
didn't pass".

**The shipped default changed, and that is what pinning is for.** It gains the
guard gate and the invariant, and it **drops `wall_ms_asc`** in favour of
`on_ranking_tie`: the study measured a cheat winning 6 of 10 identical races on
~100 ms of jitter, each time with a signed rationale naming the stopwatch as
decisive. A correct refusal beats a confident wrong answer. Existing intents
keep the digest they pinned and replay exactly as before; upgrading the binary
rewrites nothing on disk, and `mvo policy list` prints the two-command
adoption path. `mvo audit` gained the CAS integrity sweep the study caught it
lacking — it re-reads and re-hashes everything the ledger references from a
declared table, so a deleted attestation blob and an edited artifact are now
exit-1 failures naming the digest and the record that pointed at it — plus
`--require-decisions N` for CI and `--cas-sweep=false`, which says it was
skipped rather than rendering like a pass. `mvo guard` is the adoption wedge:
two trees, one policy, exit 0 or 1, and no ledger write at all.

**One design deviation, recorded because it is load-bearing.** M1f specified a
FIFO for the evidence channel. A bind-mounted FIFO on Docker Desktop is a pipe
*inside the VM*, unconnected to the host's read end, so the first in-container
write blocks forever — the channel deadlocked under T1, the exact tier the
`isolated` regime requires. The channel is now a control-plane-owned file
tailed live. The properties that mattered were never the inode type: the
reader holds one handle at a monotonically increasing offset and never
rewinds, so the already-received prefix is immutable; a candidate that
truncates and rewrites at exit desynchronizes the sequence, which makes the
stream unusable — a failed gate, never a forged pass; and where a distinct
oracle uid exists, the containing directory is unwritable by it, so the
channel can be appended to and neither unlinked nor replaced.

**The integration gate found the label lying, and that is fixed here.** `auto`
resolved to `isolated` for every T1 race and `execution.evidence_regime`
recorded it — while `t1World.Command` execs with workdir `/work` (read-write)
and no `--user`. A real T1 race under the shipped default produced receipts
reading `evidence_regime: "isolated"` next to `isolation_caps.user: "501:20"`,
the invoking uid, which owns both the worktree and the evidence directory: not
one of the three guarantees the regime table promises under `isolated` held,
and `mvo explain` printed the claim to the operator. That is the study's
finding with the *label* doing the laundering — a signed claim stronger than
the evidence behind it — so a regime is now chosen strictly by the capability
that ships. `auto` is `streamed` on every tier; a policy declaring `isolated`
is refused at pre-flight as machinery, naming the missing exec path, rather
than downgraded quietly. `internal/race/evidence_test.go` pins the table the
design asked for and nobody wrote, including the guard rail that flipping
`isolatedExecAvailable` must accompany the `--user` change.

**A second integrity claim the gate found inverted.** The new CAS sweep reads
`attestation.recorded.statement` as a standalone blob, which M1f's `admit` is
the first to `Put`. Every M0–M1e ledger names a statement whose bytes live
only inside the DSSE envelope, so `mvo audit` exited 1 on intact pre-M1f
workspaces with `MISSING: … (statement)` — audit accusing a workspace of
losing something it never lost, which is the over-claim this block removes,
pointed the other way. The sweep now resolves that one reference through its
container when it is not standalone, re-hashing the extracted bytes: strictly
stronger than the direct read, because it also proves the bundle's signed
payload is the statement the ledger names. Verified against a real M1e-built
ledger: `25 events, 2 decisions replayed, 31 CAS objects verified`.

**The corpus proves the guard, not the stream — so the missing step got
built.** All eleven laundering vectors ship a harness file or edit a test, so
all eleven die at rung O-1 and **not one reaches the evidence stream**, the
mechanism the block is named after. Read carelessly, 11/12 credits the stream
for the guard's work. Step 3k now races the study's vector 1 — the
`conftest.py` writing a fabricated 500-test `junit.xml` at `atexit` — under
`no-paths.json`, with the guard gate, the guard oracle and the invariant all
absent: the receipt reads `tests_total=8, tests_failed=2, tests_passed=6`, the
world fails `status-pass@suite`, and the decision is REJECT. Under M1e that
same patch passed every hard gate and landed on trunk. The corpus README now
says outright which column measures what.

Building it surfaced a latent acceptance bug: step 9a's evidence-stream
extractor `break`s out of its stdin loop, SIGPIPE-ing `sqlite3`, which
`pipefail` turns into a 141 that aborts the script. It had only ever passed
because the receipts fitted in the pipe buffer; one more race was enough. It
now drains stdin. The same trap took the new T1 explain assertion (`mvo … |
grep -q` exits on match and the SIGPIPE, not the match, decides the pipeline),
which is why the script's own idiom captures first.

**What is still open, said as plainly as the rest.** An adversary who patches
the observer from inside the test process wins, and nothing in-process defeats
in-process code; weakened and rewritten assertions are undetectable from test
outcomes and wait on O3 mutation testing; and a backdoor that passes every
honest test is ch. 13's actual problem, which M1 does not address. The
`isolated` regime itself is open: the mounts are built, the exec path is not.
`patches-forge-deep/` and steps 3l/3m/3n/11 remain unbuilt; the mechanisms
they cover are under unit test, and step 3k covers the load-bearing half of
3k/3l end to end.
Every receipt therefore records **which evidence regime observed it** — today
that is `streamed` for every pytest run, on T0 and T1 alike. `mvo explain`
prints that line unconditionally, because a guarantee nobody can see the
absence of is a guarantee nobody has, and the label is not exempt.

---

## 2026-08-15 — Design partner study: three "no"s, and the hole they found

**We ran M1 acceptance criterion 3 as a real experiment and it failed, which is the most useful thing that has happened to this project.** Three emulated design partners — a fintech platform engineer, a burnt-out OSS maintainer, and a staff engineer whose explicit brief was to break the value proposition — each built their own repo and attempted adoption reading only the README, quickstart and concepts docs. No source. No design docs. No PRD. A fourth agent then verified every claim against the code before any of it was believed. All three completed `init → intent → race → explain`; all three needed **three** support interactions against a bar of one; all three declined adoption. [The full study is published](docs/studies/2026-08-design-partner-study.md).

**The finding is architectural: the candidate writes its own evidence.** `concepts.md` states the rule the whole product rests on — *"Oracles outrank agents. Agent output is a claim."* — but the suite oracle runs pytest inside the candidate's world, writing its JUnit XML into the candidate's own writable tree. A patch consisting of nothing but a `conftest.py` that sets `session.exitstatus = 0` and writes a fabricated `junit.xml` at `atexit` passed every hard gate, produced a receipt claiming **500 passing tests in a three-test repository**, won the race, landed on trunk with the real bug untouched — and then `mvo verify` printed `OK: attestation verified (7 checks)` and `mvo audit` printed `OK`. The admission re-gate blessed it too, because the same conftest is in the landing tree. Every downstream guarantee faithfully certified a number the adversary authored. The skeptic's phrasing is going in the design doc: *CI's green check is a rumour everyone knows is a rumour; Multiverso signs the rumour and hands it to a reviewer who has been told oracles outrank agents.*

Three more verified criticals. The shipped default ranking ends in `wall_ms_asc` with tie-escalation off, so in a repo where an honest fix and an assertion-weakening cheat both pass every gate, **the cheat won 6 of 10 identical races on ~100ms of wall-clock jitter** — each time with a confident signed rationale naming the stopwatch as the decisive key. The gate ladder catches exactly one laundering vector out of five: test deletion is stopped cold, while skipping tests, weakening assertions, rewriting assertions to match the candidate's own wrong output, and forging the artifact all pass clean (`tests_skipped` is recorded in every receipt and no gate in the vocabulary can read it). And in six commands from documented steps only, the unencrypted private signing key reached git history: `mvo init` edited the *tracked* `.gitignore`, the quickstart's own remedy for the resulting warning was `git reset --hard`, which reverts that line. **Our fixture was structurally immune** — `testdata/toyrepo/.gitignore` already had the entry, so the documented path could never fire it. A fixture that cannot fail the way real repos fail certifies the docs, not the product.

What survived is the reason to keep going. The skeptic reported his going-in thesis wrong: admission re-gating caught a change a green GitHub check would have waved through, the freshness/tree-env binding held against direct attack, and the publication path beat all three tamper attempts with errors naming the file and the reason. Nobody rejected the concept — every objection lives in the shell.

Fifteen doc and CLI-ergonomics fixes shipped with this entry, gate green: the ignore rule moved to the untracked `.git/info/exclude` (closing the key chain), two **factually false** sentences about what `mvo audit` checks corrected, a "what the gate ladder does not catch" table added from the five verified vectors, `--key` documented as *the* trust anchor rather than a convenience, and a merge-queue section stating the fact that decides that question: an attestation survives a fast-forward of the exact admitted commit and nothing else. Also fixed: a flaky docker teardown assertion in our own suite — a project whose thesis is evidence quality does not get to ship a test that asserts before the daemon has finished reaping.

The four criticals are the next block of work, ahead of M2's scheduler. Building budget allocation on evidence the evaluated party can forge would be building on sand.

## 2026-08-15 — M1 closure: the audit, the docs, and a real remote

**M1 (Candidate Race v0) is code-complete.** What that sentence is worth depends entirely on how it was checked, so: three auditors read [PRD §7](PRD.md#7-functional-requirements) requirement by requirement against the shipped code and its tests, instructed that an inflated DONE is worse than an honest PARTIAL. The result — **11 complete, 14 partial, 0 missing** — is now the [README's Status section](README.md#status), gaps named in plain language rather than buried. Four of those partials are genuine spec-vs-code drift that no design doc had recorded as a deferral (CP-6's `REPAIR`, three `Receipt` fields EP-2 names, EP-7's `seeds`, TP-2's identity tier being one hardcoded literal instead of a modeled vocabulary); the other ten were conscious deferrals, now cited to the design doc and line that deferred them.

**The docs got the same treatment as the code.** A writer produced [`docs/quickstart.md`](docs/quickstart.md) and [`docs/concepts.md`](docs/concepts.md) from the audited reality, then a second agent — allowed to read *only* the README and quickstart, never the source or the design docs — followed them literally on a cold machine and recorded every place they lied. Nineteen failures, three of them blockers: the build step left `mvo` uncallable, the missing-agent pre-flight demo stripped its own binary off `PATH`, and the "author your own policy" section printed a JSON fragment with a `/* comment */` in it that no JSON parser would accept. Eighteen were fixed and the corrected quickstart re-run end to end from cold. One finding was load-bearing enough to become documentation in its own right: a policy is content-addressed over its **exact file bytes**, so reformatting a policy mints a different policy — and therefore a different pinned digest.

**And it ran against a real remote.** [`docs/probes/m1-github-demo.md`](docs/probes/m1-github-demo.md) records M1 acceptance criterion 4 executed on a public GitHub repo: a race whose losing candidate goes green by deleting the failing test, stopped by `collected-not-below` at `collected_delta=-1` **before the suite oracle ever runs**; an admission landing a commit with intent/decision/attestation trailers; and a fresh clone that had never seen the workspace authenticating 15 published items and replaying both decisions from nothing but a public key. The first attempt failed — hand-written patches with a wrong hunk header — and that failure was itself informative: the worlds recorded `CONFIG_ERROR` and `mvo explain` named it as the gate failure rather than quietly dropping the candidates. Agent failure is evidence; machinery failure is an error; the distinction held under a case nobody designed for.

Remaining M1 acceptance criteria need a human: a design partner running the quickstart on their own repo (criterion 3), and the 30-instance SWE-bench-Live budget-accounted run (criterion 2), which wants the M2 evaluation harness.

## 2026-08-14 — M1e: versioned policies + the real oracle ladder

**The policy is now an artifact, and the gate is now Python.** `multiverso.dev/policy/v1` (CP-5 to its full text) is an ordered list of hard gates that each name a declared oracle instance, a pass predicate and the weakest freshness basis they accept; a **lexicographic** ranking spec (never a weighted sum); and a closed set of escalation conditions. `mvo policy list|show|validate|use` authors and installs them; `mvo intent new --policy` pins one per intent; `mvo init` writes a default that runs the real ladder — **O0** `pytest --collect-only` (collected counts) then **O1** the suite gate (JUnit XML + reportlog + coverage.py), with `collect-nonempty` and `collected-not-below` ordered *before* `status-pass` so a candidate that deletes tests is stopped by the counts and never reaches a green suite (EP-1, EP-2, EP-7). ESCALATE is a first-class race outcome, and `mvo explain` renders *why* the winner won, key by key, from the ledger + CAS + the pinned policy — derived at render time, never stored (CP-6).

Design decisions worth reading in [docs/design/M1e-policy-oracles.md](docs/design/M1e-policy-oracles.md): **digests are never recomputed from decoded objects** (decision 1) — `Decide` takes the object *paired with the digest it was recorded under*, so M1e adds fields to `World` and `Receipt` and still replays pre-M1e ledgers byte-for-byte, ending the "this milestone breaks replay" tax M1b and M1c both paid; v0 is **frozen on the wire and upgraded only in memory**, with a rationale renderer frozen per schema version, because re-wording a decision made under a policy pinned in 2026 rewrites history and audit compares the sentence byte-for-byte (decisions 2–3); `gate_pass` is an implicit *first* ranking key and `world_digest_asc` an implicit *terminal* one, so the winner provably passes every hard gate and the order is total (decisions 4–5); escalation is a fixed struct of named conditions — **no expression language ever**, because a policy is data that reaches the decision core (decision 6); unknown gate/key/basis/kind = load error, so `Decide`'s `default:` branches are unreachable rather than fail-closed guesses (decision 7); exit code 5 is **fail, never pass**, with `collected_total = 0` recorded explicitly (decision 14, research ch. 19's laundering vector); a missing toolchain aborts at pre-flight as *machinery*, never as a failing candidate (decision 15); a metric derived from an absent source is **absent** — never zero, never "assumed 100 %" (decision 16); and first-run status is parsed from the reportlog so a pass-after-rerun is recorded as strictly weaker, separately named evidence (decision 17). `--oracle-cmd` survives only where it is honest — under a v0 policy, whose digest never determined its gate — and `mvo intent new --oracle-cmd` is the migration: the command moves *inside* the pinned, attested artifact.

Review round, 12 findings, 11 applied. The **BLOCKER** was a hole the v0 shape could still express: `{"hard_gates":[]}` compiles to a policy that gates *nothing* (v1 validation requires a gate; v0 is decoded exactly as M0 decoded it, never re-validated), so every world passed, `admit.Decide`'s gate loop was vacuously satisfied, and an ADMIT was **signed, attested and landed** on the strength of no gate at all — with the ledger left permanently un-auditable, because admission derived its evidence from one landing oracle while replay derived it from zero gate selectors. Fixed at the ingest boundary (`policy validate`/`use`, `intent new --policy`), with two more locks behind it: `landingSpecs` refuses a gate-less policy as machinery before anything is recorded, and `admit.Decide` fails closed with REJECT — what cannot be attested must not be admitted. `policy.Decode` stays total on purpose, so historical ledgers and published closures keep replaying exactly what they recorded. Three **MAJOR**s: the ladder's short-circuit ignored gate *order*, so with interleaved gate oracles the recorded rationale (and `mvo worlds`' GATE column) named a gate that never ran while the gate that actually failed was reported "not-evaluated" — decision 12 inverted; `mvo explain` laundered an ESCALATE into a win, printing `why X won` and `WINNER X ← decided here` three lines above the block reporting that a rule refused to call it a decision — under `on_ranking_tie` crediting the exact digest coin flip the rule exists to refuse; and `escalation.require_evidence` could not fire in any race this orchestrator produces (the winner passed every gate, so its ladder always ran to completion and the receipt was always *there*) — it now tests what decision 16 documents, an oracle whose structured source was unavailable and whose receipt carries none of its metrics, driven through `race.Run` in the test rather than a hand-built receipt slice. Also: validation went from kind-level to **instance-level** (a `coverage-at-least` gate on a `coverage:false` suite oracle, or on a prefix coverage.py cannot wrap, is now refused at load instead of failing forever at 3 a.m. — and the validator reads the *same* predicate the oracle does, so the two cannot drift); `gateDef.threshold` stopped being a dead field (a threshold on a gate that takes none is an authoring error, not a harmless extra); gate cells render the measurement from **that gate's own receipt** and `--json` drops any metric two counted receipts disagree about, because a number a consumer cannot attribute to an oracle is worse than no number; and `fetch-race` stopped assuming one `suite`-family landing receipt — it now checks the attested subject tree against *every* gate receipt (a legal collect-only policy was being rejected) and renders its world table's GATE cell through the same trace `mvo worlds` uses. One finding was **contract-side only** and the doc was amended to match the code: `race.Config` gains `LegacyOracle`, never `Policy` — the compiled policy can only come from `intent.Policy`, which is the safer design the contract's own step 1 already prescribed. The one skip: re-deriving `mvo audit`'s admission evidence from "one receipt per gate oracle" instead of from the pinned policy's gate selectors — the two derivations now provably agree (a gate-less policy never reaches admission at all), and making replay depend on `admit.Run`'s plumbing rather than on the policy's own selectors would trade a proof for a coincidence.

Acceptance grew five steps, all against the toy repo: a lexicographic race decided by the **second** ranking key, a test-deleting candidate stopped by the collected-count guard (and provably never reaching the suite oracle), an ESCALATE on a tie, a rejected policy file with its byte-exact error, and a legacy-v0 race whose rationale still matches M0's frozen sentence — then one `mvo audit` replaying **two policy schema versions in one ledger** byte-for-byte. Parser tests run against committed `testdata/oracle/` fixtures, so no test needs pytest, coverage.py or any plugin installed; the plugin-dependent paths degrade honestly and say so.

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
