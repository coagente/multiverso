# M2d — Labelled Evaluation: Design & Contract

> **This block exists because of one measurement and one sentence.** [M2b.1](M2b1-budgeted-fixed-arm.md) raced the two budgeted arms on the shipped default policy at a binding budget (B2 = 1 529 ms, `testdata/toyrepo`, k = 1, R = 5) and got: **adaptive REJECT 5/5, 586 ms spent of 1 529, four receipts, zero complete worlds**, against **fixed-budget SELECT 3/5, 1 284 ms, five receipts, one complete world** — 60 % disagreement against a 40 % noise floor, the first difference in the project that exceeds both arms' own run-to-run instability. The sentence is the BUILDLOG's: *"A `REJECT` where the truth is 'no candidate was any good' is the correct answer; a `REJECT` where an honest fix was sitting there unbought is a failure. We do not have the labels yet."* **M2d is the labels.** Every decision below is subordinate to making that one row readable, and nothing that does not serve it is in scope.
>
> **The metric, in one line:** a candidate's correctness comes from a **hidden oracle** that no policy, no gate, no oracle, no scheduler and no world ever sees; the arm's decision comes from the ledger; TCAR and FAR are pure arithmetic over the join. The arithmetic is the easy part. **The mechanism that keeps the two apart is the deliverable** ([§2](#2-hidden-oracle-discipline)) — [research ch. 9 §3](../../research/09-benchmarks-evaluation.md) is blunt that without it "the experiment measures oracle leakage, not scheduling," and our own trust boundary was breached exactly once by a mechanism nobody had enumerated ([vector 13](../../testdata/adversarial/README.md)), so enumeration is not the defense. A tripwire is.
>
> **The scoping decision that makes the block runnable today, stated before anything else.** **ZERO AGENT SPEND.** No `claude`, no `codex`, no API call, in the harness or in any test. Candidates come from the instance's gold patch, from mechanically derived perturbations of it, from the 22 laundering vectors we already ship, and from recorded worlds of past runs (**of which this repository currently has none** — every fixture is a scripted patch). That buys a real experiment about **selection and allocation over a fixed candidate set**, and it buys nothing whatsoever about **generation**. Mechanically derived wrong patches are **not a sample of what agents produce** ([§3](#3-candidate-sources-with-zero-agent-spend)), and every table this harness prints carries **`SYNTHETIC-CANDIDATES`** next to M2b.1's **`ORACLE-BUDGET-MATCHED`** for exactly that reason.
>
> **Scope discipline.** `Decide` gains nothing. `Receipt`, `World`, `Intent`, `Decision`, `PolicyV1` gain nothing — no field, no gate, no metric, no ranking key, no oracle kind, no escalation rule. **The eval harness adds no ledger event and no wire field anywhere.** It is a *reader* of ledgers and a *writer* of eval-plane artifacts that live outside the workspace. The strongest form of that discipline is [decision 2](#decision-2): the label-reading code is **not linked into the `mvo` binary at all**. Stdlib only; `go.mod`/`go.sum` untouched; POSIX; degrades to "skip, with a reason" whenever the corpus, an image or a tool is absent.

## Module layout (delta over M2b.1)

```
internal/eval/instance.go   the labelled instance: schema, load, and the PUBLIC
                            PROJECTION — the only shape the racer ever receives
internal/eval/derive.go     mechanical candidate derivation: pure, seeded, recorded
                            by (generator id, seed, params); proposes, never labels
internal/eval/label.go      label store, three-tier vocabulary, `unknown` propagation,
                            the positive/negative scoring controls
internal/eval/leak.go       the four leak detectors + the canary scan (PURE over
                            recorded inputs: argv, tree listings, CAS keys, bytes)
internal/eval/metrics.go    TCAR, FAR, FRR, regret decomposition, unknown-intervals,
                            the statistical refusals — PURE and TOTAL
internal/eval/arms.go       eval-plane derived selector arms + their evidence footprints
cmd/mvo-eval/main.go        the SECOND BINARY: fetch, derive, race, score, report,
                            adjudicate, import-worlds. The only code that can open
                            the eval home. NOT linked into cmd/mvo (decision 2)
eval/corpora/*.manifest.json  instance ids + expected digests (small, text, in repo)
eval/splits/*.json            dev/eval assignment + salt + split digest (in repo)
eval/freeze/*.json            frozen hidden-suite digests + the policy digest and
                              scheduler constants AT FREEZE TIME (in repo)
scripts/eval.sh             the full protocol. NOT part of accept.sh: it needs a
                            fetched corpus and minutes of wall clock
scripts/accept.sh           +4 hermetic steps (m2d-7a…7d): degradation, the leak
                            tripwire, the import-graph assertion, metric absence
```

**Untouched:** `internal/policy`, `internal/object`, `internal/admit`, `internal/audit`, `internal/schedule`, `internal/race`, `internal/oracle`, `cmd/mvo`. If a diff to this block touches any of them, the block is wrong.

---

## 1. The labelled instance

An **evaluation instance** is the smallest thing that can produce one row of TCAR: a repository at a base commit, a task, a candidate set, and an oracle that says which candidates are actually correct. Three of those four are ordinary; the fourth is the whole problem, because it must exist and must be unreachable at the same time.

<a id="decision-1"></a>
> **Decision 1 (load-bearing). An instance is TWO files with a one-way digest link: a PUBLIC part the racer may read, and a HIDDEN part only the scorer may read. Nothing merges them.**
>
> ```
> $MVO_EVAL_HOME/<corpus>/<version>/
>   instance/<id>.json     PUBLIC: repo url, base_commit, env image ref, task text,
>                          candidates[] (source tag + patch CAS key), oracle_digest,
>                          canary_id  — and NOTHING ELSE
>   oracle/<id>.json       HIDDEN, mode 0600: fail_to_pass[] node ids, pass_to_pass[]
>                          node ids, suite argv template, timeout, canary token,
>                          gold_patch (test hunks stripped), strengthened_suite?
>   labels/<id>/<cand>.json  written by the scorer, AFTER the ledger is sealed
> ```
>
> `strengthened_suite` is present only when the *corpus itself* ships one; **we generate none** (that is LLM work, [§3](#3-candidate-sources-with-zero-agent-spend)), so in v0 the label tier is **Tier 1** on every instance and the field exists so a Tier-2 upgrade never requires re-racing.
>
> `instance.oracle_digest` is `sha256` of the hidden bytes. It appears in the public part so a report can prove *which* oracle scored a row, and it is a **digest, not a path** — a digest cannot be opened. The public part carries no node id, no test file name, no gold patch and no canary token. **`candidates[]` in the public part carries a `source` tag and a patch CAS key, and never a label**: a public label is a public oracle.
>
> **Why the gold patch is in the HIDDEN half.** It is a candidate *and* an oracle. As a candidate it must be raced; as the reference for differential labelling it must be secret. The resolution is that the racer receives gold's *patch bytes* (as an ordinary candidate, source-tagged, indistinguishable in shape from a derived one) while the scorer receives gold's *identity*. The racer knows some candidate is gold only if it can tell from the bytes — which a human reviewer can and our machinery cannot, and which [§3](#3-candidate-sources-with-zero-agent-spend) names as a limitation rather than hiding.

### 1.1 Representation, caching, and the fetch that is not silent

The repository holds **manifests only**: `eval/corpora/<corpus>-<version>.manifest.json` = corpus name, upstream URL, dataset revision, instance ids, and per-instance `(public_digest, oracle_digest, image_ref)`. It is text, it is small, it is reviewable, and it is the thing the paper cites. **No instance payload, no image, no test suite and no gold patch is ever committed** — the repository stays free of large fixtures, per the standing rule.

The corpus itself lives at `$MVO_EVAL_HOME` (default `~/.cache/multiverso/eval/`), **outside the repository and outside every workspace**, mode 0700. It gets there by exactly one route: `mvo-eval fetch <corpus>`, which **prints every URL it will contact before contacting it**, requires `--yes` for a non-interactive run, verifies each instance against the manifest digest, and refuses to write anything on a mismatch. Fetching downloads the SWE-bench-Live dataset rows and (optionally, `--images`) the per-instance RepoLaunch images ([ch. 19 §f](../../research/19-mvp-oracle-toolchain.md)). **This is the only network access in the block, it is opt-in, and it is named here so no reader discovers it from a stack trace.**

Two corpora, and the difference matters:

| corpus | needs network? | instances | what it can support |
|---|---|---|---|
| **`local-derived`** (ships) | **no** | `testdata/toyrepo`, `testdata/adversarial/repo` | the full mechanism, the full metric arithmetic, the M2b.1 diagnosis, the leak tripwire, FAR-adv over the 22 vectors. **n = 2 repositories: a diagnosis, never a rate with a confidence interval** |
| **`swebench-live-<slice>`** | **yes, explicitly** | frozen `lite`/`verified` splits + the newest monthly +50 slice | PRD §11's instance counts for the *selector* arms only; ch. 9's temporal-holdout contamination defense |

`local-derived` exists so that the harness is testable, acceptance-gated and honest today with zero downloads. Its ground truth is not borrowed: `testdata/adversarial` already measures correctness **out of band**, by `python3 -S -c` with an explicit `sys.path`, so no `conftest.py`, no `sitecustomize.py` and no pytest plugin can reach the interpreter that decides whether the bug is gone. **That is already a hidden oracle**, built in M1f for a different reason. M2d generalizes its mechanism rather than inventing one.

### 1.2 Degradation: skip with a reason, never fabricate

<a id="decision-1b"></a>
> **Decision 1b. Every absence is a NAMED SKIP from a closed vocabulary, recorded per instance, and it propagates into the metric as ABSENCE rather than as a zero.**
>
> | skip reason | trigger | consequence |
> |---|---|---|
> | `corpus-absent` | `$MVO_EVAL_HOME/<corpus>` missing | every instance skipped; **no TCAR/FAR line is printed at all** |
> | `instance-absent` | manifest lists it, cache lacks it | that row skipped; denominators shrink and the shrink is printed |
> | `digest-mismatch` | cached bytes ≠ manifest digest | **hard error**, not a skip: a corpus that drifted is a different corpus |
> | `image-absent` | no docker daemon, or image not pulled | skipped unless the instance declares `t0_ok` |
> | `tool-absent` | policy needs mutmut / hypothesis / cosmic-ray | the race refuses at pre-flight with the ledger untouched ([M2a decision 20](M2a-oracle-menu.md)); the row records `PREFLIGHT_ABORT` and its refusal sentence |
> | `gold-fails-control` | gold patch does not pass the hidden suite | **instance dropped as INVALID** (§1.3), counted, never scored |
> | `leak-detected` | any [§2](#2-hidden-oracle-discipline) detector fires | **instance VOIDED**, harness exits non-zero |
> | `unstable` | arm's decision not modal at ≥ ⌈2R/3⌉ | reported in its own bucket, excluded from the paired test |
>
> `scripts/eval.sh` exits **0** with a skip census when the corpus is absent and **non-zero** under `--strict`. A run whose census is nonempty prints the census *above* the metrics, not in a footnote. **Absent source implies absent metric** is the project rule; here it means an arm with zero admissions has **no FAR at all** ([decision 9](#decision-9)), and a corpus with zero scored instances has no numbers to report.

### 1.3 Instance validity: the gold patch is the positive control

Before an instance can produce a row, the scorer runs two controls in the same environment it will use for scoring:

- **negative control** — the base tree, unpatched: must **fail** ≥1 `fail_to_pass` and **pass** all `pass_to_pass`. A base tree that already passes F2P means the instance's task is not the task.
- **positive control** — the gold patch (test hunks stripped, per SWE-bench convention, and *the strip is recorded*): must pass both sets.

Either control failing drops the instance as `gold-fails-control`, with the observed counts kept. This is not ceremony: SWE-bench-Live's curation is automated, and ch. 9 records that the 93-human Verified effort existed because automated curation leaves task-validity noise. **Dropping invalid instances is the only defense we can run without humans**, and the drop rate is itself a published number.

---

## 2. Hidden-oracle discipline

This is the section a reviewer attacks first, so it specifies **mechanisms and detectors**, never intentions. The threat is not a malicious developer; it is the ordinary way leaks happen — a path in an argv, a fixture copied into a fixture, a test that "just needs the node ids", a policy file that names the strengthened suite. M1f's history is the argument: the entry-point plugin vector was assumed closed by a path glob set and was not, and what actually closed it was **removing the capability** plus **a nonce that would have detected the breach**.

<a id="decision-2"></a>
> **Decision 2 (the strongest mechanism in the block). TWO BINARIES. The code that can read a label is not linked into the binary that races.**
>
> `mvo` — the product binary — must not, at any optimization level, contain a symbol that opens `$MVO_EVAL_HOME`. There is no `mvo eval` subcommand and there will not be one. The scorer is `cmd/mvo-eval`, a separate `main` in the same module, and the property is **asserted mechanically**, not reviewed:
>
> ```
> go list -deps ./cmd/mvo | grep -q internal/eval   # MUST NOT match
> ```
>
> `scripts/accept.sh` step **m2d-7c** runs exactly that and fails the build if it matches. The consequence is structural: a leak through the racing binary would require someone to add an import that the acceptance script rejects. Compare the alternative — one binary with a flag that "must not be set during a race" — which is a promise, and promises are what [F7](M2b1-budgeted-fixed-arm.md#f7) and the design-partner study taught us not to ship.
>
> The same split applies at run time: `mvo-eval race` **exec's** `mvo` with an environment from which every `MVO_EVAL_*` variable has been *removed* (not overwritten — removed, so `os.Getenv` returns absent rather than empty), a cwd outside `$MVO_EVAL_HOME`, and an argv containing only the public projection's fields. The racer is handed a temporary **public instance file** written to the workspace; the hidden file's path never appears in its argv, its env, its cwd subtree, or any file it can reach.

<a id="decision-3"></a>
> **Decision 3. The hidden suite NEVER enters a world tree, because it never enters any tree a race touched. Scoring happens on a FRESH RECONSTRUCTION, after the ledger is sealed.**
>
> This is the precedent [M2a decision 13/29](M2a-oracle-menu.md#decision-13) established for the differential corpus, applied to the oracle. The corpus lives under `<workspace>/.multiverso/corpora/<race>/` at mode 0700, one directory per world, written only *after* phase A joins, so no world-visible path holds the inputs while an agent runs. M2d goes one step further, because a label does not have to be delivered to anyone:
>
> 1. The race runs to completion; the decision is recorded; the ledger is sealed.
> 2. For each candidate, the scorer reconstructs its tree **from `world.tree` via `gitx.LsTreeRecursive`/`CatBlob`** into a brand-new directory under the scorer's own tmpdir — a directory no race, no world, no oracle and no keeper ever saw.
> 3. The hidden suite is mounted **read-only, outside the reconstructed repo root**, at `/mvo/hidden` (T1) or a sibling path (T0). It is never copied into the repo tree, never staged, never committed, never in `git status`.
> 4. The suite runs; its report is written to **control-plane scratch outside the reconstructed tree**; the reconstruction is deleted.
>
> So the claim "no hidden test entered a world tree" is not a policy — it is arithmetic over directory identity. And the ordering claim "the label did not exist when the decision was made" is **process-structural**: the scorer process is spawned after the racer process exits. Timestamps corroborate; they do not prove, and the doc says so rather than pretending a clock is evidence.

<a id="decision-4"></a>
> **Decision 4. The labeller is inside the trust boundary too. Scoring executes candidate code, so it is hardened like an oracle and cross-checked like one.**
>
> A scoring run imports the candidate's modules — that is the point of it — so the candidate gets to execute code during its own labelling. Everything M1f learned applies:
>
> - `PYTEST_DISABLE_PLUGIN_AUTOLOAD=1` (M2a amendment 27's seal; the entry-point plugin route is a capability, not a glob problem), `-p no:cacheprovider`, `PYTHONNOUSERSITE=1`, explicit `sys.path`, `python3 -S` for the out-of-band check — the exact recipe `scripts/adversarial.sh` already uses for its ground truth.
> - Hidden tests are collected from `/mvo/hidden`, so the reconstructed repo's own `conftest.py` is **not on the collection rootdir path**; candidate *hook* code does not load, candidate *module* code does (unavoidable, and it is what is being measured).
> - T1 where an image exists: `--network none`, distinct oracle uid, reconstructed tree mounted **read-only**, `--pids-limit`, `--memory`. The suite cannot write to the tree it is judging.
> - **THE PROCESS THAT RUNS CANDIDATE CODE IS NOT THE PROCESS THAT PRODUCES THE VERDICT.** *(Amended after a reviewer reproduced a forgery against the first implementation. It shipped a single-process runner that did `sys.path.insert(0, repo)` and `__import__(mod)` in the interpreter that already held the mutable check table, and carried `--report <path> --nonce <value>` on that interpreter's argv. A candidate whose only extra code was `sys.modules['mvo_hidden_checks'].CHECKS = [(cls, name, lambda: True) for ...]` exited 0, echoed the nonce, listed every declared node as passing, and was labelled `correct` while returning `[total] * parts`. "Verdict integrity by cross-check" was three signals from one process the candidate controlled, so it was not a cross-check at all — and the defence the design named, "the checks are generated functions, not evaluated strings", defends against the wrong thing: the problem is co-residency, not dynamic evaluation.)* The oracle is therefore **two processes**: a **parent** that holds the canary, the node ids and the predicates, receives the report path and the nonce **on stdin**, writes the report and sets the exit status, and **never imports candidate code**; and a **child** that imports the candidate and returns **only observable values** over an inherited unlinked descriptor, with `sys.argv` scrubbed to `argv[0]`, its own script directory removed from `sys.path`, an empty throwaway cwd, and no canary, node id, report path or nonce anywhere in its image. A check is a pair — *calls* evaluated in the child, *predicate* evaluated in the parent — so a wrong candidate can only pass by **returning the value a correct implementation would return**, which it cannot learn. The corpus version moved `v0 → v1` because the generated shape moved, so a `v0` eval home cannot be scored by a `v1` scorer.
> - **Verdict integrity by cross-check**: the label is derived from the *agreement* of exit code, JUnit XML written to control-plane scratch, and a per-run nonce echoed in the report. Any disagreement yields `unknown` — **never `correct`**. This is the cross-oracle count invariant the adversarial corpus computes, pointed at the labeller. It is worth having **only because of the split above**: the three signals now come from a process the candidate never entered.
> - **The scratch is drained.** Every report carries the canary and the whole node-id list, so each one is deleted as soon as it is parsed and `Scorer.AssertScratchDrained` refuses if any survived. The first implementation set the runner's cwd *to* that scratch, so candidate module code ran in the directory holding ten of the oracle's own answers, and the controls run first — the very first candidate scored saw a full pass/fail map. No detector covered it: D5 scans the racer's workspace, and `report-<nonce>.xml` matches no hidden-suite basename.
> - **Batch controls**: every scoring batch re-runs the §1.3 positive and negative controls. If either moves inside a batch, the **whole batch's labels become `unknown`** and the batch is re-run. Controls catch toolchain and environment drift, which is the failure mode that silently converts a `correct` into an `incorrect` and would show up in FAR as if it were a scheduler effect.

<a id="decision-5"></a>
> **Decision 5. Four independent detectors plus a canary. A leak VOIDS the instance; it does not annotate it.**
>
> Enumeration is not the defense, so the fifth item is the one that catches what the first four did not think of.
>
> | # | detector | what it scans | why it can be trusted |
> |---|---|---|---|
> | **D1** | **argv/config scan** | every receipt's `Execution.Argv` and `OracleRef.Config` in the race's ledger, for any hidden node id, any path under `$MVO_EVAL_HOME`, or the `oracle_digest` | receipts record the **in-world argv** ([`internal/object/types.go`](../../internal/object/types.go) `Execution`), so this is a *total* scan of what actually ran, not a sample of what was intended |
> | **D2** | **tree membership** | `git ls-tree -r` over every `world.tree` and the base tree, against the hidden suite's path set | a hidden test file cannot be in a candidate tree without this firing. A *name collision* (a candidate happens to write `tests/test_issue_1234.py`) fires it too, and the instance is voided rather than accepted — a collision is indistinguishable from a leak from inside the harness |
> | **D3** | **CAS absence** | the workspace CAS for the hidden bytes' `sha256` and for the gold patch's | the ledger is content-addressed; if hidden bytes ever passed through the control plane they are in CAS under a key we can compute |
> | **D4** | **prompt/transcript scan** | every world's captured patch, agent context and transcript, for node ids, the canary, and the `oracle_digest` | this is [`accept.sh` step 3r](M2a-oracle-menu.md)'s negative test — "no corpus digest or case id in any prompt" — with the hidden oracle's identifiers added to the needle set |
> | **D5** | **THE CANARY** | a random 32-byte hex token, embedded in every hidden test file, in the node-id lists and in the gold patch header, grepped over **the entire workspace**: ledger, every CAS blob, every world tree, every scratch and evidence dir, every artifact, and the harness's own logs | it needs no enumeration of leak paths. It is M1f's evidence nonce reasoning applied to the experiment: the value of a nonce is that it detects the breach you did not model |
>
> **On a hit:** the instance is `leak-detected`, its row is dropped from every metric, the canary token and the hitting path are printed, and the harness **exits non-zero**. There is no "reported with a caveat" mode, because a leaked instance's TCAR is not a noisy measurement of scheduling — it is a measurement of something else entirely.
>
> **Plus one refusal at the bind-mount layer, RESTATED to match its mechanism.** The original row said "a test asserts that no `backend.OpenOpts` constructed anywhere in an eval run names a path under `$MVO_EVAL_HOME`". It cannot: by [decision 2](#decision-2) every `OpenOpts` in an eval run is constructed inside the separate `mvo` process, which does not link `internal/eval`, so the eval plane never observes one. What ships is a **predicate** (`eval.CheckMounts`) tested against hand-built `OpenOpts`, kept for the day an in-process mount surface exists, and **not** counted among the detectors `Prove()` requires — a detector that never runs must not be reported as one that did. What actually covers the run is three *measured* facts recorded per instance in the non-consultation witness: `Race` refuses a `WorkRoot` inside the eval home, the repository is **copied out** of the eval home before any race, and `argvClean` asserts no eval-home path reached the racer's argv. The M2a amendment 29 bug is still the reason the row exists; the claim is now the size of the mechanism.
>
> **And every detector reports what it could NOT read.** `WalkFiles`' size cap and `TranscriptDocs`' CAS misses are counted into `Report.Skipped` and `NonConsultation.Prove` **refuses `Proved=true` on a nonzero count**. Both counters existed and both were discarded by the only production caller, so a `ledger.db` above 8 MiB or an unservable blob narrowed D4/D5 silently while the report said the detectors ran: "we scanned three of six transcripts" and "we scanned six" are different claims.
>
> **The detectors run over EVERY workspace the instance raced, not the reference one.** Two arms × R replicates produce every reported decision; scanning only the reference meant "canary clean" described 1 of 11 workspaces per instance, and specifically not the ones the numbers came from. The count is printed beside the verdict so the scope is legible.
>
> **And one candidate that goes looking.** `testdata/toyrepo/patches-reach/` already ships a candidate that hunts the pinned corpus by every relative path a candidate would try and **fails itself if it finds one**. M2d adds the hidden-oracle analogue — a candidate that greps its own tree, its parents, `/mvo`, and its environment for the canary and for anything shaped like a node-id list, and fails itself on a hit. It is the assertion whose absence let decision 13 read as unreachability for a whole block.

<a id="decision-6"></a>
> **Decision 6. The dev/eval split is a recorded function, the eval hidden suites are frozen and hashed BEFORE tuning, and every eval scoring is COUNTED.**
>
> Ch. 9 §7's requirement is that hidden suites are "versioned and never used for development iterations (development uses a disjoint dev split)." The mechanism:
>
> - **Split assignment is deterministic and recorded, not curated.** `eval/splits/<corpus>-<version>.json` holds a `salt` and assigns instance *i* to dev iff `HMAC-SHA256(salt, i)`'s first byte < 77 (≈30 % dev / 70 % eval). Ids are listed in plaintext in both halves — you need them to run — and the file's own digest is committed. A hand-picked split is detectable: recompute the HMAC.
> - **What "dev" licenses.** On dev instances you may look at everything, including the hidden suites, and tune anything. On eval instances you may look at **the public projection only**. The scheduler, the policy and every gate see the public projection on *both* splits, always: dev freedom is a developer's, never a program's.
> - **The freeze file.** `eval/freeze/<corpus>-<version>.json` records, at freeze time: every eval instance's `oracle_digest`, the **shipped default policy digest**, the **scheduler constants** (`executor_bp`, the redundancy tiers, the bracket clamps), and the binary digest. `mvo-eval score --split eval` **refuses** when the current policy digest or constants differ from the frozen ones, naming what moved, unless `--unfreeze "<reason>"` is passed — and the reason string, the diff and the timestamp are appended to the run log. Tuning after the freeze is not forbidden; it is made **impossible to do quietly**.
> - **The eval-use counter.** `$MVO_EVAL_HOME/<corpus>/eval-runs.log` is append-only: one line per eval scoring with (timestamp, binary digest, policy digest, arm set, instance count). Each eval scoring is a leaderboard query in Blum & Hardt's sense, and **the query count is published in the paper**. That is the Ladder discipline stated as a number rather than as a claim of restraint.
> - **All three traces bind on `score` as well as on `run`.** *(Amended: they did not. `mvo-eval score` is the verb that actually opens the hidden oracle, runs it and writes labels, and it had no `--split` flag, ran no freeze check and appended no run line — all three lived in `run`'s `finish()`. An eval-split instance could therefore be scored repeatedly under a moved policy digest or moved scheduler constants leaving no freeze mismatch, no `--unfreeze` reason and no run-count inflation: exactly the accidental version the mechanism exists to stop, and exactly what the freeze file's own notes promised twice was impossible.)* The freeze check is now a shared helper both verbs call, `score` takes `--split`/`--unfreeze`, `score` **refuses outright** on an instance the split file assigns to `eval` unless `--split eval` (which makes the freeze bind) or `--unfreeze` is passed, and every scoring appends its own line.
>
> **The honest gap:** none of this stops a determined developer from reading a hidden suite on the eval split. It makes the reading *leave a trace* (freeze mismatch, unfreeze reason, run-count inflation) and it stops the accidental version, which is the one that actually happens. Sequential-analysis-grade protection would require a third party to hold the suites, and we say so instead of implying we have it.

---

## 3. Candidate sources with zero agent spend

<a id="decision-7"></a>
> **Decision 7. The generator PROPOSES, the hidden oracle LABELS. No source carries an assumed label — not even the ones built to be wrong.**
>
> A "revert one hunk" mutant can be semantically neutral (an equivalent mutant, the oldest known problem in mutation testing); a "weaken the condition" mutant can weaken a condition the tests never reach. Stamping such a patch `incorrect` because we *meant* it to be wrong would put the harness's intentions into the numerator of FAR. **Every candidate is scored.** The only place an expectation appears is as a **cross-check**: `derive.go` records an `expected` field, and the report prints an `expectation-violated` census (derived-wrong patches the oracle calls correct, gold that fails). A large census is not an error — it is information about the oracle's strength, which is ch. 9's open question 1.

| # | source | what it IS | what it CAN represent | what it CANNOT represent — stated so no number outruns it |
|---|---|---|---|---|
| **S1** | **instance gold patch** (test hunks stripped) | the known-correct candidate; also §1.3's positive control | that a correct candidate was *on the table*; the ceiling every arm is measured against; instance validity | **anything about agents.** It is the human patch: minimal, idiomatic, localized to the right file by construction. A selector that could smell "human-authored" would win for free, and we cannot prove ours does not. **Gold's presence makes coverage 1.0**, which is strictly easier than any real cohort |
| **S2** | **mechanically derived wrong patches** — revert a hunk; flip/weaken a comparison or boundary; apply gold's edit to a sibling function; truncate to the first *k* hunks or lines; transplant another instance's gold patch | a seeded, reproducible, zero-cost population of plausible-shaped failures | **local** failure modes: incomplete fixes, off-by-one, wrong-target edits, partial application. Enough to exercise ranking, gates and allocation across a nontrivial candidate set | **NOT a sample of what agents produce, and this is the sentence that bounds the whole block.** Real agent failures are whole-solution rewrites that are plausible and wrong, over-broad changes, repo-scale localization misses, and reward hacking. S2 mutants are perturbations of a *known-correct patch*: they touch the right file, in the right function, with the right shape — the part real agents most often get wrong. Consequences, all in the same direction: (a) they are **systematically easier for a test suite to catch**, so FAR measured on S2 is a **lower bound** and flatters every arm and every oracle; (b) they share a common ancestor, so M2a's cross-candidate differential sees **artificially high agreement** and behavioral partitions degenerate — the rung most distorted is the one M2a shipped; (c) their diversity is a function of our generator's operator list, not of a model's failure distribution, so **any TCAR/FAR from S1+S2 is a property of `derive.go` as much as of the scheduler** |
| **S3** | **the laundering corpus** — 22 vectors in [`testdata/adversarial/vectors`](../../testdata/adversarial/) | known-wrong **and** adversarial, with ground truth already measured out of band | **FAR-adv** against *named mechanisms*; the `executor_bp ∈ {0, 2 500, 5 000, 7 500, 10 000}` sweep [M2b decision 6](M2b-adaptive-scheduler.md#decision-6) owes; a regression suite for the trust boundary | **not a sample of anything.** 22 hand-built attacks on one 40-line fixture: **n = 1 repository**, so a table of mechanisms, never a rate with a CI. Per ch. 13 the affordances are *declared*, so measured FAR-adv is a **lower bound** on a real adversary's success. And the operational trap: under the shipped default **eleven of the twelve laundering vectors (02–13) die at rung O-1 on `paths-unmodified@guard` with exactly one receipt each**, and the twelfth at `status-pass@suite`, so they never reach the scheduler at all — an allocation rule cannot differ where the first rung kills everything. To make them scheduler-relevant they must be raced under the relaxed-guard policies (`no-paths.json`, `policies/differential.json`), which is a different experiment and must be labelled as one |
| **S4** | **patches recorded from earlier real runs** | the only real-agent sample obtainable at zero marginal spend | genuine agent output distribution, if any existed | **this repository has none.** Every fixture is a scripted patch; `live_test.go` is opt-in, never CI, and produced no recorded corpus. `mvo-eval import-worlds <ledger>` is specified (extract `(base_commit, world.patch, world.tree, adapter id/version, model id)` triples, `source: "recorded-run"`), so the path exists the day a real run is recorded — and until then **S4 is an empty corpus, and pooling it with S1/S2 in one number is forbidden by [decision 8](#decision-8)'s tagging rule** |
| **S5** | **the null candidate** (empty patch) | "the agent did nothing" | — | **our machinery cannot represent it.** An empty prompt makes the script adapter return `CONFIG_ERROR` and no world is created ([`internal/agent/script.go`](../../internal/agent/script.go)), so a real and common agent outcome is structurally absent from every candidate set this harness can build. Named, not fixed |

<a id="decision-8"></a>
> **Decision 8. Every candidate carries its `source` tag through to the report, and a metric computed over mixed sources prints the source census beside it. There is no untagged aggregate.**
>
> `SYNTHETIC-CANDIDATES` on every plot and caption, next to M2b.1's `ORACLE-BUDGET-MATCHED`. A table mixing S1/S2 with S3 additionally prints `+ADVERSARIAL(declared)`. The rule exists because the single most likely way this block produces a wrong published claim is a pooled FAR over gold + mutants + laundering vectors, reported as though it were FAR over agent output.
>
> **Three amendments, each because the first implementation satisfied the rule with a MIS-tagged aggregate, which is worse than an untagged one.**
>
> 1. **The source census is joined by IDENTITY.** `winnerSource` resolved a winner's source by scanning the labels for the first one whose *verdict* matched. Labels are sorted by candidate id, so any instance with two same-verdict candidates named the alphabetically-first — on `advrepo-split-B` the fixed arm selected `v10-padded_deletion` (S3) and the manifest recorded S2-derived. The join is now the winning world's own `world.tree` through `Instance.CandidateByTree`, the documented key.
> 2. **`+ADVERSARIAL(declared)` fires on the POPULATION, not on the winners.** A census over winners printed zero occurrences of "ADVERSARIAL" across a full two-cell protocol run despite S3 candidates on three of five instances. Every row now carries its instance's whole source set, and the caption fires whenever a declared attack was on the table.
> 3. **THE POLICY IS PART OF THE CELL KEY, NOT A WARNING ABOVE IT.** `inst.PolicyHint` overrides the cell's `--policy` unconditionally — that is deliberate, since eleven of the twelve laundering vectors die at rung O-1 under the shipped guards — but the harness printed a `NOT UNIFORM` line and then **pooled the numbers anyway**, so a row captioned `default B1` was two policies, one of them the relaxed `no-paths` whose empty protected-path set and `tests_passed_desc` ranking the adversarial corpus already measured as letting `10-padded_deletion` beat the honest fix. Metrics are now computed per `(arm, family, policy)`, so every printed cell is uniform by construction and says which policy it is; `--policy-override` clears the hints when a genuinely uniform cell over the whole corpus is wanted.
> 4. **The global caption is the UNION over every arm's census**, not `Metrics[0]`'s — the first arm on the first family — which understated the tier and source mix of every other cell in the same report.
> 5. **Every denominator prints its CLUSTER count.** The five instances are nested slices of two repositories (`advrepo-split-B`'s candidate set is `advrepo-split-A`'s minus gold and v07; `toyrepo-mean-A/B/C` share one gold and one mutant pool), so a caption reads `n=5-INSTANCE-SLICES-OVER-2-INDEPENDENT-BUGS`. Treating five nested slices as five independent observations inflates every rate and the paired *n*.

<a id="decision-8b"></a>
> **Decision 8b. Three instance FAMILIES, because the M2b.1 ambiguity is only resolvable by running both directions.**
>
> The 5/5 REJECT is admirable or catastrophic depending on whether a correct candidate was there. So build both worlds and read both columns:
>
> | family | composition | REJECT is | what it measures |
> |---|---|---|---|
> | **A — `gold-present`** | gold + (N−1) derived wrong | **a failure** | false-rejection rate; missed admissions; the M2b.1 row's bad case |
> | **B — `all-wrong`** | N derived wrong, no correct candidate | **the only correct answer** | FAR and over-admission directly; the M2b.1 row's good case |
> | **C — `mixed`** | *k* correct of N (gold + a mutant the oracle calls correct) | wrong unless gates say otherwise | ranking quality among admissibles — CodeMonkeys' selection-regret analogue |
>
> Family B is the family that can vindicate a cautious arm, and it is exactly the family a naive harness omits because "no candidate is correct" looks like a broken fixture. It is not: it is the majority case in the literature at low sample counts, and an admission system's whole value proposition is behaving well in it.

### What the full PRD §11 experiment still needs, and this cannot supply

Named exhaustively, because the block's credibility rests on the list being complete rather than short:

1. **Real agent candidates** at ≥300 instances per stratum × arms × 3 budget levels. Tokens dominate PRD §11's budget `B`; a harness that charges no tokens cannot compare arms whose budgets are tokens.
2. **PRD §11 arms 1–5** — every one of them *generates* (single agent with serial self-repair, best-of-N random / public-tests / LLM-judge, test-voting). [§5](#5-the-arms) says which surrogates exist and why they are not those arms.
3. **Tier-2 strengthened suites** as ch. 9 defines them: UTBoost/SWE-ABS-style augmented and adversarial test generation is LLM work. Without it, headline numbers sit on Tier 1, and ch. 9's own warning applies to us: *"any claimed false-admission-rate below ~5 % measured with Tier-1 only is meaningless."*
4. **Tier-3 adjudication** — two independent human raters over every Tier-1/Tier-2 disagreement plus a 10 % audit of agreements, with inter-rater agreement reported. Human labor. The *file format* is specified (`labels/<id>/<cand>.adjudication.json`, signed with the eval key) so labels can be upgraded without re-racing a single instance, and the tier is carried on every label so no aggregate silently mixes tiers.
5. **FAR-adv with challenger agents** (AG-6) rather than 22 static patches.
6. **Model-swap stability** (≥2 model families, rank correlation of arms) and **pass^k reliability** over seeds — both need generation.
7. **Calibration** (Brier/ECE against truth) — needs a confidence output *and* a real candidate distribution; fitting it on S2 would calibrate against `derive.go`.
8. **Cost per truly-correct merge** — needs the token term.
9. **Post-integration regressions** over SWE-bench-Live's chronological stream — needs admissions onto real trunks over time.
10. **The contamination covariate** (the SWE-Bench-Illusion file-path probe per model) — model inference, i.e. spend.
11. **`race --reverify <world-digests>`** — [M2b.1 F2](M2b1-budgeted-fixed-arm.md#f2)'s close. Without it, arms cannot verify *the same worlds* once generation is live, and every cross-arm difference is confounded with generation variance. It is a contract change and it is still not in this block.

---

## 4. TCAR and FAR, defined operationally

**B IS A MEASUREMENT, SO IT IS REPLICATED.** `B1 = ceil(minspend × 1.1)` is derived from the reference race's own bound, and taking that race once makes the experiment's independent variable a noisy draw: across three runs of the same cell one instance's `minspend` came out 1961 / 1537 / 983 ms, and B1 collapsed to 0 — which `mvo intent new` reads as *unbounded* — on a different instance each time. The reference arm is raced `--reference-replicates` times (default 3), B is derived from the **median**, every replicate's own minspend is printed as the `reference spread`, and a row whose derived budget is 0 is **excluded** from a budget-matched cell rather than annotated inside it.
>
Fix an instance *i*, an arm *a*, a budget level, a policy configuration, and R ≥ 3 replicates ([M2b.1 decision 9](M2b1-budgeted-fixed-arm.md#decision-9): the paper's power comes from instances, the replicates size the noise floor). Let `L(i,c) ∈ {correct, incorrect, unknown}` be the hidden oracle's label for candidate *c*, and let the arm's decision be its **modal** decision over R (non-modal at < ⌈2R/3⌉ ⇒ `unstable`, own bucket, excluded from the paired test).

<a id="decision-9"></a>
> **Decision 9. ADMISSION = `SELECT`. ESCALATE is NOT an admission and NOT a success. An arm with zero admissions has NO FAR — absent, not zero.**
>
> Three choices, each with the reason it beats its alternative:
>
> **(a) `SELECT`, with `admit`-confirmed reported beside it.** *(Amended: `LedgerView` parsed `admission.finished` into an `AdmitResult` that nothing ever read, so `Row.AdmitRan` was never set and the columns were structurally unreachable — even scoring a workspace where a developer had run `mvo admit` rendered "absent". The report was not wrong, but the plumbing that makes the divergence census observable was a no-op. Rows are now populated from the replicate's own ledger view.)* The treatment that varies across arms is the scheduler; making TCAR depend on EP-3's admission re-verification would mix the admission gate into a scheduling comparison. So the headline pair is `TCAR_sel`/`FAR_sel` over the race's `Decision.Type`, and whenever `mvo admit` was actually run the report carries `TCAR_adm`/`FAR_adm` **beside** it plus a census of divergences (a `SELECT` that `admit` rejected is a finding in its own right). Where admit was not run the columns are **absent, never equal to the SELECT columns**.
>
> **(b) `ESCALATE` enters neither TCAR's numerator nor FAR's denominator.** FAR's denominator is "what the system asserted was safe to land," and an escalation asserts nothing — it hands the decision to a human. Putting escalations in FAR's denominator would let any arm drive FAR to zero by escalating everything, which is the precise perverse incentive an admission system must not be scored on. But escalation is **not free**: TCAR's denominator is *all instances*, so escalating costs exactly as much TCAR as rejecting. **This is the choice that decides whether adaptive "looks good", and it is made against adaptive's interest on purpose.** M2b.1's REJECT-5/5 row therefore scores `TCAR = 0` on family A and is charged nothing on FAR — and the missing side of that ledger is supplied by the false-rejection metrics below, not by bending FAR.
>
> **(c) Zero admissions ⇒ `FAR = —`.** Not 0 %, not 0/0 rendered as zero. FAR's denominator is admissions; an arm that admitted nothing has no denominator, and "0 % FAR" for an arm that never acted is the single most misleading number this harness could print. This is the project's own "absent source implies absent metric" rule, and it bites here in adaptive's favor — which is why it must be a *rule* and not a judgment call made after seeing the numbers.
>
> ```
> avail(i)      = ∃c : L(i,c) = correct                        -- a correct candidate was on the table
> adm(i,a)      = [ D(i,a) = SELECT ]                          -- winner w ↦ candidate ord(w), recorded by the race
> TCAR(a)       = |{ i : adm ∧ L(win) = correct }| / |I|        -- denominator: instances ATTEMPTED  (ch. 9)
> FAR(a)        = |{ i : adm ∧ L(win) = incorrect }| / |{ i : adm }|   -- denominator: ADMISSIONS   (ch. 9)
> ESC(a)        = |{ i : D = ESCALATE }| / |I|                  -- escalation burden
> ESC_just(a)   = |{ i : D = ESCALATE ∧ ¬avail(i) }| / |{ i : D = ESCALATE }|   -- nothing was there to find
> ```
>
> **The two denominators differ on purpose** (instances for TCAR, admissions for FAR) — that is ch. 9 §6 verbatim, and a reviewer who assumes it is a typo must be able to find this sentence.
>
> **`unknown` labels never become a verdict.** They are excluded from numerators and from FAR's denominator, counted as `unscored_admissions`, and whenever that count is nonzero **FAR is reported as an interval** `[FAR_low, FAR_high]` computed by counting every unknown first as correct then as incorrect. A point estimate is printed only when the interval is a point.

<a id="decision-10"></a>
> **Decision 10. The false-rejection metric composes the LABEL with M2b.1's ALLOCATION BOUND. That composition is the block's actual contribution, and it is what reads the 5/5 REJECT row.**
>
> `REJECT` and `ESCALATE` both cost TCAR, but they do not cost it for the same *reason*, and the reason is what tells us whether the scheduler is broken:
>
> ```
> FRR_label(a)     = |{ i : D ∈ {REJECT, ESCALATE} ∧ avail(i) }| / |{ i : avail(i) }|
> FRR_gates(a)     = as above, restricted to i where d*(i) ≠ SELECT(correct)
>                    -- a correct candidate existed but the POLICY would reject it on full
>                    -- evidence: an oracle false negative, ch. 9 OQ1, NOT a scheduling loss
> FRR_reachable(a) = |{ i : D ∈ {REJECT, ESCALATE} ∧ d*(i) = SELECT(w) ∧ L(w) = correct
>                          ∧ minspend(i) ≤ B }| / |{ i : d*(i) = SELECT(correct) ∧ minspend ≤ B }|
> ```
>
> `d*` is the full-evidence decision and `minspend` the least spend at which *some* prefix-respecting allocation reaches it — both already computed, exactly, by [M2b.1 §5](M2b1-budgeted-fixed-arm.md#5-the-retrospective-allocation-bound) from the recorded exhaustive ledger. **`FRR_reachable` is the number the M2b.1 BUILDLOG asked for**: the arm rejected, a correct candidate was there, the policy would have selected it on full evidence, and *the money to reach that decision was in the arm's pocket*. That is "plain failure" with no interpretation left in it. An arm that rejects only where `minspend > B` is doing the correct thing under poverty, and the same formula says so.
>
> **Selection regret against the retrospective best available candidate**, decomposed so a gap has an address:
>
> ```
> coverage        = |{ i : avail(i) }| / |I|        -- = TCAR of the LABEL-RETROSPECTIVE arm (§5, A9)
> regret(a)       = coverage − TCAR(a)
>                 = regret_generation + regret_gates + regret_allocation + regret_ranking
> ```
>
> - `regret_generation = 1 − coverage` — no correct candidate existed. **Attributable to the candidate source, never to the scheduler.** On family A it is 0 *by construction*, which is why family B exists.
> - `regret_gates` — a correct candidate existed and the policy rejects it on full evidence (oracle false negative).
> - `regret_allocation` — `d*` selects a correct candidate, the arm at *B* did not, split by `minspend ≤ B` (avoidable) vs `>` (unwinnable by any allocator, and excluded from the paired comparison per [F16](M2b1-budgeted-fixed-arm.md#f16)).
> - `regret_ranking` — the arm admitted an *incorrect* candidate while a correct one passed every gate. This is the CodeMonkeys 12.4 pp analogue and the only component a better *ranking* fixes.
>
> The four components are computed independently and **asserted to sum to `regret(a)`** by a property test. An identity that does not close is a bug in the harness, not a result.

### Statistics, and the refusals

Paired per-instance design; all arms see identical instances ([F1–F15](M2b1-budgeted-fixed-arm.md#3-what-a-fair-comparison-requires) carry over unchanged and are re-asserted per instance). **McNemar's exact test** on the paired indicator `adm ∧ L(win)=correct`, and **BCa bootstrap CIs** over instances. And, in the house style, the harness refuses rather than trusting discipline:

- **no p-value and no CI below a declared instance floor** (default 30 scored instances per cell); below it, the report prints the paired 3×3 decision table and the per-instance rows, and says `n too small for an inferential statistic`.
- **no verdict below R = 3** replicates, and **no difference called a win unless it exceeds both arms' own self-disagreement** — M2b.1 decision 9's rule, inherited verbatim, because it is what turned a 4-vs-5 into a tie.
- **no metric printed for a cell whose skip census is nonempty** without the census printed above it.
- **every number carries its label set**: `ORACLE-BUDGET-MATCHED`, `SYNTHETIC-CANDIDATES`, tier of the labels, and the source census.

---

## 5. The arms

<a id="decision-11"></a>
> **Decision 11. What ships is "SELECTOR ARMS OVER A FIXED CANDIDATE SET". It is not PRD §11's arm set, the report never calls it that, and the mapping is printed with every table.**
>
> PRD §11's seven arms each receive a budget `B` and *spend it*, mostly on generation. Ours receive a candidate set that already exists and allocate verification over it. The difference is not a detail: the budget term that dominates §11 is absent, so a comparison between our arms is a comparison of **allocation rules**, and a comparison against §11's arms 1–5 is not available at any confidence.

| PRD §11 arm | runnable at zero spend? | what ships instead, and what it is not |
|---|---|---|
| 1 · single agent, serial self-repair | **NO** — generation | nothing. The serial-vs-parallel compute trade ch. 9 §4 requires is unmeasurable here |
| 2 · best-of-N random | **surrogate** | **A4 `random-selection`** — eval-plane, derived: pick ordinal `HMAC(salt, i) mod N` from the recorded world set. Seeded and recorded (AG-3 forbids unrecorded randomness; [M2b decision 13](M2b-adaptive-scheduler.md#decision-13) forbids a randomized *policy*, so this is **not** an `mvo` scheduler and gets no ADMIT path). It is arm 2's *selection* half with arm 2's *generation* half deleted |
| 3 · best-of-N public tests | **surrogate** | **A5 `repo-suite-max`** — admit the candidate with the highest `tests_passed` in its recorded suite receipt. Zero spend, derived from the ledger. **Not arm 3**: ch. 9's "public tests" are *agent-generated*; ours are the repository's own suite, and the adversarial corpus already measured that `tests_passed_desc` is exactly what let `10-padded_deletion` beat the honest fix under M1e |
| 4 · best-of-N LLM judge | **NO** — API spend | nothing. Out of scope and named as such |
| 5 · test-voting (CodeMonkeys) | **surrogate, weak** | **A6 `differential-majority`** — the largest behavior class on M2a's control-plane corpus wins. **Not CodeMonkeys**: the corpus is control-plane-authored rather than model-authored, the vote is over behavior classes rather than test outcomes, vector 19 already games it, and S2's shared ancestry ([§3](#3-candidate-sources-with-zero-agent-spend)) degenerates the partition. Reported as an exploratory row |
| 6 · Multiverso adaptive | **YES** | **A2 `adaptive`** (M2b VOC) and **A1 `fixed-budget`** (M2b.1 ladder), plus **A3 `fixed`/reference** = `fixed-budget --budget-oracle-ms 0`, which yields `d*`. These three are the experiment |
| 7 · retrospective oracle | **YES, for the first time** | **A9 `label-retrospective`** — admit any candidate the hidden oracle labels `correct`. `TCAR = coverage`, `FAR = 0` by construction; it is the regret denominator and nothing else. Computed **only inside the scorer** ([decision 2](#decision-2)). Alongside it, **A8 `allocation-bound`** (M2b.1 §5) stays what its name says: a bound on *allocation*, not on decision quality |

<a id="decision-12"></a>
> **Decision 12. Every derived arm declares its EVIDENCE FOOTPRINT and is charged for it. An arm that reads the whole ledger for free would dominate everything for free.**
>
> A4 reads no receipt: footprint ∅, oracle cost 0. A5 reads the suite rung on every world: footprint `{suite}`, cost = Σ recorded `cost.wall_ms` over those receipts. A6 reads the corpus rung on every world. A9 reads the *hidden* suite on every candidate, whose cost is real and is reported in its own currency (scoring milliseconds), never mixed into the oracle pool. **A raced arm is charged the oracle spend its own replicate recorded**, so raced and derived arms land on one axis. The footprint and its cost are printed with the arm, so every arm — raced or derived — lands on the same cost/accuracy axis ch. 9 §4 demands. A derived arm without a declared footprint does not print a number.
>
> *(Amended: `arms.go` computed all three charges correctly and the run loop discarded `OracleCostMS`, `ScoringMS` and `Footprint` on the floor. `eval.Row` had no cost field and `RenderArm` printed no cost line, so no arm — raced or derived — ever printed a number on the axis this decision exists to put them on, and A9's scoring milliseconds, the one cost that must never be pooled with the oracle budget, were never shown at all. Row carries all three now and `RenderArm` prints a `charged` line with the scoring currency in its own column.)*

---

## 6. Wire deltas, acceptance, testing bar

**Wire deltas: none.** No ledger event, no receipt field, no policy field, no world field, no decision field, no schedule-trace field. The eval plane writes only to `$MVO_EVAL_HOME` and to stdout. Its own run manifest (`eval-run.json`: corpus version, split, freeze digest, binary digest, arm set, per-instance rows, skip census, canary verdict) is signed with a **separate eval key** — never the admission key — so ch. 9 §3's "sign the evidence→decision→verdict chain of the experiment itself" is satisfied without giving the experiment the ability to mint an admission.

**`scripts/accept.sh`** gains four **hermetic** steps (no corpus, no network, no docker, seconds):

- **m2d-7a — the corpus is absent and the harness says so.** With `MVO_EVAL_HOME` pointed at an empty dir: every instance `corpus-absent`, **no TCAR/FAR line printed at all**, exit 0; under `--strict`, non-zero. The assertion is on the *absence of a number*, which is the only way to test the rule.
- **m2d-7b — the leak tripwire fires (the block's failing test, written first).** A `local-derived` instance whose candidate patch deliberately contains a hidden test file, a hidden node id, and the canary. All of D2, D4 and D5 must fire; the instance is `leak-detected`; the harness exits non-zero; the printed message names the token and the path. A second case plants the canary in a *transcript* rather than a tree, which only D5 can catch — that is the reason D5 exists.
- **m2d-7c — the racing binary cannot read a label.** `go list -deps ./cmd/mvo | grep -q internal/eval` must not match.
- **m2d-7e — the run-time half, read off a REAL race.** *(This step exists because m2d-7c's comment claimed it and the next statement was the OK echo.)* One instance, R = 1, one arm: the run manifest's non-consultation witness must carry `env_scrubbed`, `argv_clean`, `cwd_outside_eval_home` and `proved` all true, a `workspaces_scanned` count above one, measured racer-exit and scorer-start instants in that order, a `policy` column on every row, and a `winner_source` that is a source the instance's own candidate set carries.
- **m2d-7d — labels are pure and controls bind.** Two scorings of the same `(world tree, oracle digest, env digest)` produce byte-identical label files; a scoring whose negative control passes F2P yields `unknown` for the whole batch, never `correct`; every label carries a non-empty env identity; `score` appends its eval-use line; and `score` **refuses** an eval-split instance that was not declared as one.

**`scripts/eval.sh`** (not in `accept.sh`; needs a corpus and minutes): the full protocol — derive candidates, race all arms at three budget levels ([M2b.1 §4](M2b1-budgeted-fixed-arm.md#4-replication-and-variance)'s B1/B2/B3, derived per instance from `minspend` and `S`) under both `on_evidence_incomplete` configurations ([F14](M2b1-budgeted-fixed-arm.md#f14)), R ≥ 3, arms interleaved, score, report.

**Testing bar.**

- `internal/eval/metrics.go`: property tests that the regret decomposition **sums exactly**; that FAR is `absent` at zero admissions and never `0`; that `unknown` widens the interval monotonically; that ESCALATE moves TCAR exactly as much as REJECT and moves FAR not at all; totality on adversarial inputs (empty instance set, all-unknown labels, single candidate).
- `internal/eval/leak.go`: each detector against a positive and a negative case; the canary scan over a synthesized workspace containing the token in each of ledger / CAS blob / world tree / transcript / artifact / scratch; **a fuzz case where the hidden path appears only inside a receipt argv** (D1's reason to exist); and **D3's gold needle over the bytes a racer could actually hold** — it digested the canary-prefixed hidden form, which is armed only on the family-B instances where gold is withheld and is precisely the form no workspace could ever contain, so the one check meant to catch "gold reached a workspace where gold was withheld" could not fire.
- `internal/eval/score.go`: **the forgery, as a regression test.** A candidate that rewrites `sys.modules['mvo_hidden_checks'].CHECKS`, reads `--report`/`--nonce` off `sys.argv`, writes its own all-pass report and calls `os._exit(0)` must not be labelled `correct` — and gold in the same batch must still be `correct`, so the hardening cannot pass by refusing everything. Beside it, a candidate that records what its process can see: one argv entry, no hidden directory on `sys.path`, no check table in `sys.modules`, no report in its cwd.
- `internal/eval/derive.go`: determinism under a fixed seed; every operator produces an applying patch on the fixture or reports why; the `expectation-violated` census is *reported* and never *asserted* (a test that asserted mutants are wrong would be the assumed-label bug in test form).
- `internal/eval/instance.go`: the public projection contains no node id, no gold patch, no canary — a field-by-field assertion over the struct, plus a golden JSON so a new hidden field cannot be added without failing a test.
- `cmd/mvo-eval`: `fetch` prints every URL and contacts none under `--dry-run`; `score --split eval` refuses under a moved policy digest and names what moved; `eval-runs.log` appends exactly one line per scoring.
- `gofmt -l` clean; `go vet ./...` and `go test ./...` green **with no docker daemon, no network, and no corpus present**; `go.mod`/`go.sum` unchanged; **no test invokes a real agent CLI**, and `scripts/eval.sh` front-loads `PATH` with the poisoned `claude`/`codex`/`gpt5`/`gemini`/`cursor`/`aider` stubs the adversarial harness already ships.

---

## 7. What this block will say about the 5/5 REJECT — and what it still will not

The first output of M2d is not a paper. It is one table: the shipped default policy, at M2b.1's B2, on `local-derived` families A and B, R ≥ 3, both arms.

- **On family A** (gold present, `d*` selects it, `minspend ≤ B`): a 5/5 REJECT scores `TCAR = 0`, `FAR = —`, `FRR_reachable = 1.0`. That is **plain failure**, in the exact form M2b.1 diagnosed — the frontier offers one rung per alive world, symmetric worlds tie, the allocator advances everybody and finishes nobody — and the fixed ladder's 3/5 SELECT is worth exactly as much TCAR as the fraction of those three that are gold.
- **On family B** (every candidate wrong): the same 5/5 REJECT is the **only correct answer**; the fixed ladder's 3/5 SELECT is **three false admissions**, and adaptive's caution is admirable in the strict sense that it is the higher-scoring behavior.
- **Both columns will be printed, always, side by side.** The BUILDLOG's honest statement — *"which arm was right is undecidable"* — becomes *"it depends on this named property of the candidate set, and here is the number on each side."*

**What that table will still not be.** n = 1–2 repositories, synthetic candidates, Tier-1 labels, oracle-budget-matched only, selection cost measured and uncharged, tokens and runner time unmeasured, no agent output anywhere in it. It is a **diagnosis of a scheduling rule**, and it will be captioned as one. If it says the rule loses, the response is to fix the rule — not to widen the corpus until the number flips.

---

## Explicitly NOT in M2d

Any agent CLI invocation, any API call, any generated candidate, and therefore PRD §11 arms 1, 4 and 5 proper and the generation-vs-verification trade ([M2b §1](M2b-adaptive-scheduler.md)); Tier-2 strengthened suites (LLM test generation) and Tier-3 adjudication *automation* — the file format ships, the raters do not; challenger agents (AG-6) and FAR-adv beyond the 22 declared vectors; calibration fitting, the posterior `P(correct | receipts)`, the measured co-failure matrix, the `executor_bp` default change, and rank-based elimination — **each of those needs a labelled stream over REAL candidates, which this block does not produce, and shipping them off S2 would fit the scheduler to `derive.go`**; cost per truly-correct merge and any token or runner-time accounting; post-integration regression measurement over the chronological stream; model-swap stability and pass^k; the SWE-Bench-Illusion contamination probe; `race --reverify <world-digests>` ([M2b.1 F2](M2b1-budgeted-fixed-arm.md#f2)'s close, still the largest threat to any cross-arm number once generation is live, still a contract change); removing the generator prompt's `"candidate k of N"` ([M2b.1 decision 3](M2b1-budgeted-fixed-arm.md#decision-3)'s named prerequisite); any change to `Decide`, to a policy field, to a ranking key, to a receipt, or to any ledger event; any sequential statistical test; Multi-SWE-bench, SWE-bench Pro and Commit0 strata (Phase B); the paper.
