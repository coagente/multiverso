# 9. Benchmarks & Experimental Design

> Part of the Multiverso research corpus - https://github.com/coagente/multiverso - Cutoff: 2026-08-12

## Why this matters for Multiverso

Multiverso's core research question is a *comparative* claim: under a fixed total budget B, an evidence-aware scheduler admits changes with **higher true correctness** and **lower false-admission rate** than a single agent, fixed best-of-N, or a test-only selector. Both halves of that claim are exactly where the current evaluation ecosystem is weakest:

1. **"True correctness" cannot be measured with SWE-bench's own oracle.** The best empirical estimate is that SWE-bench Verified resolution rates are inflated by **6.2 absolute percentage points** by patches that pass the benchmark tests but do not satisfy expected behavior ([Are "Solved Issues" in SWE-bench Really Solved Correctly?](https://arxiv.org/abs/2503.15223)). Multiverso's expected effect sizes (a few points of admission-rate improvement) are *smaller than the noise floor of the default oracle*. Measuring "false admissions" with an oracle that itself produces false positives is circular.
2. **"Under a fixed budget" has no leaderboard precedent.** SWE-bench-style leaderboards report raw resolved-%, which rewards unbounded test-time compute; the cost-controlled evaluation agenda ([AI Agents That Matter](https://arxiv.org/abs/2407.01502)) exists but has not been adopted by any major SWE leaderboard.

If Multiverso is evaluated naively — resolved-% on SWE-bench Verified, no cost control — the headline result would be unfalsifiable and probably wrong. This chapter surveys the critique literature, the benchmark landscape through 2026-08-12, and the methodological precedents, then assembles the strongest defensible protocol.

## State of the art

### 9.1 The rise, critique, and retirement of SWE-bench Verified

**Origin.** [SWE-bench](https://arxiv.org/abs/2310.06770) (ICLR 2024) collected 2,294 issue/PR pairs from 12 Python repositories; correctness = the human PR's `FAIL_TO_PASS` tests newly pass and `PASS_TO_PASS` tests keep passing, with those golden tests withheld from the model at solve time. Claude 2 solved 1.96% at release.

**Curation.** In August 2024 OpenAI released [SWE-bench Verified](https://openai.com/index/introducing-swe-bench-verified/): 93 Python-experienced developers annotated 1,699 random test-set samples; **38.3%** were flagged for underspecified problem statements and **61.1%** for unit tests that could unfairly reject valid solutions; **68.3%** of samples were filtered out overall, leaving 500 "verified" instances. Note the shape of this effort: human curation of *task validity*, not strengthening of *test adequacy* — a distinction that mattered later.

**Critique wave (2024–2026).** Every major weakness class was subsequently quantified:

- **Solution leakage and weak tests.** [SWE-Bench+](https://arxiv.org/abs/2410.06992) (Oct 2024) found **32.67%** of SWE-Agent+GPT-4's "successful" patches involved solution leakage (fix visible in the issue or comments) and **31.08%** passed only due to weak tests; removing both dropped resolution from 12.47% to 3.97%, and to **0.55%** on a fresh post-cutoff dataset.
- **Behavioral incorrectness of "resolved" patches.** [Wang, Pradel & Liu](https://arxiv.org/abs/2503.15223) (Mar 2025) ran three SOTA tools on Verified and introduced **PatchDiff**, a differential patch-testing technique that generates tests exposing behavioral discrepancies between the model patch and the human ground-truth patch: **7.8%** of "correct" patches fail the developer-written test suite, **29.6%** of plausible patches behave differently from the ground truth (46.8% divergent implementations, 27.3% over-broad changes), 28.6% of divergent patches are certainly incorrect on inspection — netting the **6.2 pp inflation** estimate.
- **Test adequacy.** [UTBoost](https://arxiv.org/abs/2506.09289) (Jun 2025) augmented tests with an LLM-driven generator and found insufficient suites in 23/300 Lite and 26/500 Verified instances; across leaderboard submissions this exposed **15.7%** more incorrect-but-counted patches on Verified (28.4% on Lite) and reordered **24.4%** of Verified leaderboard rankings.
- **Memorization/contamination.** [The SWE-Bench Illusion](https://arxiv.org/abs/2506.12286) (Jun 2025, Microsoft Research collaboration) showed SOTA models identify the buggy file path from the *issue text alone* — no repository access — up to **76%** of the time on SWE-bench repos, falling to ~53% on repos outside the benchmark: strong evidence of memorized repo-specific knowledge. [Saving SWE-Bench](https://arxiv.org/abs/2510.08996) (Oct 2025, CAIN 2026) mutated Verified problem statements into realistic chat-style queries and observed **>50%** performance drops on public benchmarks vs ~10–16% on comparable *internal* (uncontaminated) benchmarks.
- **Adversarial strengthening.** [SWE-ABS](https://arxiv.org/abs/2603.00520) (Feb 2026) combined coverage-driven test augmentation (program slicing) with mutation-driven adversarial test generation: roughly **one in five "solved" patches from the top-30 agents is semantically incorrect**; after strengthening, previously-passing patches were rejected at 19.71%, and the top agent fell from 78.80% to **62.20%** (and to fifth place).
- The weak-test failure mode is old news for anyone who read [EvalPlus](https://arxiv.org/abs/2305.01210) (NeurIPS 2023): 80× more tests on HumanEval cut pass@k by up to 19.3–28.9% and reordered model rankings. The field re-learned this lesson at repository scale.

**Retirement.** In February 2026 OpenAI announced it would [no longer report SWE-bench Verified](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/) ([announcement thread](https://x.com/OpenAIDevs/status/2026002219909427270)): SOTA progress had slowed from 74.9% to 80.9% over six months, and an audit of the 27.6% of the dataset that models most often failed found **at least 59.4% of audited problems have flawed test cases that reject functionally correct submissions** — i.e., much of the remaining "headroom" is benchmark artifact, compounded by contamination from open-source training corpora. OpenAI now recommends reporting SWE-bench Pro.

The arc — curation (2024) → quantified critique (2025) → adversarial repair (2026) → retirement (2026) — is the single most important cautionary tale for Multiverso's evaluation design.

### 9.2 The benchmark landscape through 2026-08-12

- **[SWE-bench Pro](https://arxiv.org/abs/2509.16941)** (Scale AI, Sep 2025): 1,865 long-horizon tasks across 41 repos; contamination resistance via GPL/copyleft repos (unattractive for proprietary training corpora) plus a **held-out commercial set from startup codebases** with a [private leaderboard](https://labs.scale.com/leaderboard/swe_bench_pro_private); a 731-instance public set. Frontier models: <45% public, <20% commercial. Weaknesses: still pass/fail unit-test oracle; human-augmented problem statements; Scale controls the private set.
- **[Multi-SWE-bench](https://arxiv.org/abs/2504.02605)** (ByteDance Seed, Apr 2025, NeurIPS 2025 D&B): 1,632 instances across Java, TypeScript, JavaScript, Go, Rust, C, C++ (beyond SWE-bench's Python), curated from 2,456 candidates by 68 annotators. Fixes the Python monoculture; inherits the weak-test oracle problem.
- **[SWE-bench-Live](https://arxiv.org/abs/2505.23419)** (Microsoft Research, May 2025, NeurIPS 2025 D&B): contamination resistance by *freshness* — issues created after 2024, with an LLM-agentic pipeline (RepoLaunch) that containerizes environments automatically; initial release 1,319 tasks / 93 repos, and per the [repo](https://github.com/microsoft/SWE-bench-Live), **50 newly verified issues added monthly**, plus MultiLang (743 tasks, 6 languages) and Windows (61 tasks) task sets, with frozen Lite/Verified splits for comparability. Weakness: automated curation means task-validity noise the original Verified effort spent 93 humans to remove.
- **[SWE-Gym](https://arxiv.org/abs/2412.21139)** (Dec 2024, ICML 2025): 2,438 real-task *training* environments — the first training gym for SWE agents; trained agents +19 pp absolute on Verified/Lite; also trains **verifiers** on agent trajectories for inference-time selection (32.0% Verified open-weight SOTA at the time). Its significance for Multiverso is the explicit agent/verifier split. Similarly, **[R2E-Gym](https://arxiv.org/abs/2504.07164)** (Apr 2025, COLM 2025) showed **hybrid verifiers**: execution-based verifiers suffer *low distinguishability* (weak tests can't separate candidates), execution-free verifiers are *biased toward stylistic features*; each alone saturates at ~42–43% on Verified while the hybrid reaches **51%** — direct evidence that heterogeneous evidence beats any single oracle, which is Multiverso's founding bet.
- **[Commit0](https://arxiv.org/abs/2412.01769)** (Cornell/Cohere, Dec 2024): generate 54 Python libraries *from scratch* from specs plus interactive unit tests — greenfield generation rather than brownfield repair; no agent fully reproduces a library. Useful as a task family where "the human patch" doesn't exist, forcing spec-based oracles.
- **[RepoBench](https://arxiv.org/abs/2306.03091)** (Jun 2023, ICLR 2024): repository-level *completion* (retrieval, next-line, pipeline) in Python/Java. Pre-agentic; measures context use, not change admission; largely superseded for Multiverso's purposes.
- **[SWE-Lancer](https://arxiv.org/abs/2502.12115)** (OpenAI, Feb 2025): 1,488 real Upwork tasks from the Expensify repo worth **$1M in real payouts**; graded by **triple-verified end-to-end browser tests** rather than unit tests, plus managerial-decision tasks graded against real hiring managers' choices; metric = *dollars earned*. Two precedents matter: E2E oracles are harder to game than unit tests, and economic value-weighting of tasks (not all merges are worth the same).
- **[Terminal-Bench](https://arxiv.org/abs/2601.11868)** (Stanford/Laude Institute + community, Jan 2026): 89 curated hard terminal tasks (SWE, sysadmin, data, security), each with a sandboxed environment, a human-written solution, and verification tests the agent must satisfy but does not see; frontier agents <65%. Precedent for state-of-environment verification beyond diffs.
- **[LiveCodeBench](https://arxiv.org/abs/2403.07974)** (Mar 2024): contest problems tagged with release dates, enabling before/after-cutoff contamination analysis — the canonical *temporal-holdout* precedent that SWE-bench-Live imports into repo-level evaluation.

### 9.3 Selection, budget, and metric precedents

- **Coverage scales, selection is the bottleneck.** [Large Language Monkeys](https://arxiv.org/abs/2407.21787) (Jul 2024) showed coverage (pass@any) grows log-linearly over four orders of magnitude of samples — DeepSeek-Coder on SWE-bench Lite: 15.9% (1 sample) → **56%** (250 samples). But coverage only converts into resolution where verification is automatic and sound.
- **[CodeMonkeys](https://arxiv.org/abs/2501.14723)** (Stanford, Jan 2025) is the closest published system to a Multiverso baseline: parallel multi-turn trajectories that co-generate edits and test scripts, then select. On Verified: **coverage 69.8%**, random selection 45.8%, majority voting on model-generated tests ~54%, a "selection state machine" (test-based filtering to top-3, then targeted discriminating tests) **57.4%** — i.e., a **12.4 pp selection regret** vs the oracle ceiling, with selection recovering roughly half the random-to-oracle gap while consuming only **5.8% of the $2,291.90 total budget** (edit generation 59.6%, test generation 19.2%). Their ensemble selection over existing leaderboard submissions ("Barrel of Monkeys") hit 66.2%.
- **[SWE-Search](https://arxiv.org/abs/2410.20285)** (Oct 2024) brought MCTS with a hybrid LLM value function to SWE agents — precedent for *adaptive* rather than fixed allocation across candidate branches. [SWE-RM](https://arxiv.org/abs/2512.21919) (Dec 2025) trained a 30B-A3B execution-free reward model for patch verification, explicitly evaluated on **classification accuracy and calibration** — the only work found that treats verifier calibration as a first-class metric — lifting Qwen3-Coder-Max 67.0%→74.6% on Verified via selection.
- **Budget-matched comparison.** [Snell et al.](https://arxiv.org/abs/2408.03314) (Aug 2024) established FLOPs-matched, prompt-difficulty-adaptive "compute-optimal" test-time scaling (>4× efficiency vs best-of-N). [AI Agents That Matter](https://arxiv.org/abs/2407.01502) (Jul 2024) showed agent papers systematically confound capability with spend — simple baselines Pareto-dominate complex agents (e.g., ~50× cheaper at equal HumanEval accuracy) — and found **7 of 17 agent benchmarks had no holdout set at all**. It argues all agent comparisons must be reported as cost/accuracy Pareto frontiers.
- **Hidden-test methodology.** The canonical precedent is Kaggle's public/private leaderboard split; [The Ladder](https://proceedings.mlr.press/v37/blum15.pdf) (Blum & Hardt, ICML 2015) formalized leaderboard-overfitting limits, and the [meta-analysis of ~120 Kaggle competitions](https://papers.nips.cc/paper/2019/file/ee39e503b6bedf0c98c388b7e8589aca-Paper.pdf) (Roelofs et al., NeurIPS 2019) used the public-vs-private gap to measure adaptive overfitting (finding it real but mostly modest — holdouts work). Within SWE evaluation: SWE-bench hides golden tests from the agent; CodeMonkeys' selector only ever sees *model-generated* tests; SWE-bench Pro keeps a commercial split fully private; Terminal-Bench verifies against tests the agent never sees.
- **Reliability metrics.** [τ-bench](https://arxiv.org/abs/2406.12045) (Sierra, Jun 2024) introduced **pass^k** — probability that *all* k i.i.d. runs succeed — as the dual of pass@k, showing GPT-4o's pass^8 <25% on retail tasks. For an admission system, pass^k-style reliability (and its cousin, model-swap stability) is closer to what production users care about than pass@k.

## Comparison table

| Benchmark / work | Year | What it measures | Oracle | Contamination defense | Hidden split | Cost-controlled | Known weaknesses |
|---|---|---|---|---|---|---|---|
| [SWE-bench / Verified](https://arxiv.org/abs/2310.06770) | 2023/24 | Python issue repair | F2P/P2P unit tests | none | golden tests hidden from agent | no | 6.2 pp inflation, leakage, memorization; [retired by OpenAI](https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/) 2026 |
| [SWE-bench+](https://arxiv.org/abs/2410.06992) | 2024 | leakage/weak-test audit | filtered tasks | post-cutoff issues | n/a | no | small; one-off |
| [PatchDiff study](https://arxiv.org/abs/2503.15223) | 2025 | behavioral correctness | differential tests vs human patch | n/a | n/a | no | needs ground-truth patch |
| [UTBoost](https://arxiv.org/abs/2506.09289) / [SWE-ABS](https://arxiv.org/abs/2603.00520) | 2025/26 | test adequacy repair | augmented/adversarial tests | n/a | n/a | no | offline, benchmark-repair only |
| [SWE-bench Pro](https://arxiv.org/abs/2509.16941) | 2025 | long-horizon repair | unit tests | GPL + private commercial set | private leaderboard | no | oracle still unit tests; vendor-held |
| [Multi-SWE-bench](https://arxiv.org/abs/2504.02605) | 2025 | 7-language repair | unit tests | none | golden tests hidden | no | weak-test problem inherited |
| [SWE-bench-Live](https://arxiv.org/abs/2505.23419) | 2025 | fresh-issue repair | unit tests | monthly fresh issues (+50/mo) | frozen splits | no | automated curation noise |
| [SWE-Gym](https://arxiv.org/abs/2412.21139) / [R2E-Gym](https://arxiv.org/abs/2504.07164) | 2024/25 | agent+verifier training | tests + learned verifiers | n/a | n/a | partially | training-oriented |
| [Commit0](https://arxiv.org/abs/2412.01769) | 2024 | greenfield library gen | spec unit tests | none | no | no | 54 libs, Python only |
| [SWE-Lancer](https://arxiv.org/abs/2502.12115) | 2025 | economic-value tasks | triple-verified E2E tests | none | E2E tests held out | $ metric | single repo (Expensify) |
| [Terminal-Bench](https://arxiv.org/abs/2601.11868) | 2026 | terminal/environment tasks | env-state verification suites | curation | verifier hidden | no | 89 tasks; not repo-repair |
| [CodeMonkeys](https://arxiv.org/abs/2501.14723) | 2025 | parallel gen + selection | model tests → golden tests | none | selector never sees golden tests | reports $ | fixed (non-adaptive) allocation |
| [AI Agents That Matter](https://arxiv.org/abs/2407.01502) | 2024 | evaluation methodology | — | — | holdout audit | Pareto frontiers | not SWE-specific |
| [τ-bench](https://arxiv.org/abs/2406.12045) | 2024 | agent reliability | pass^k | none | no | no | not SWE |

## Implications for Multiverso design

The following is, in our judgment, the strongest defensible protocol for the core research question.

**1. Task universe: never a single benchmark, never Verified as primary.** Primary: **SWE-bench-Live** monthly splits with instance creation dates strictly after every evaluated model's training cutoff (the LiveCodeBench temporal-holdout discipline). Secondary: a **Multi-SWE-bench** stratum (non-Python generalization) and a **Commit0** stratum (greenfield tasks with no human reference patch, forcing spec-driven oracles). SWE-bench Verified appears only in an appendix for comparability with prior work, never in headline claims. Run the SWE-bench-Illusion file-path probe on all evaluated models per repository and report it as a contamination covariate.

**2. Three-tier correctness oracle — the headline metric is Tier 2, not Tier 1.**
- *Tier 1 (benchmark verdict)*: stock `FAIL_TO_PASS`/`PASS_TO_PASS`. Reported only for comparability.
- *Tier 2 (strengthened verdict)*: UTBoost/SWE-ABS-style augmented + adversarial test suites, plus **PatchDiff differential testing against the human reference patch** where one exists. "Truly correct" = passes Tier 2 with no unexplained behavioral divergence.
- *Tier 3 (adjudication)*: stratified manual review of every Tier-1/Tier-2 disagreement and a random 10% audit of agreements, two independent raters with reported inter-rater agreement — the discipline OpenAI's 93-annotator Verified effort and the PatchDiff study both used.
Given SWE-ABS's finding that ~20% of top-agent "solved" patches are semantically wrong, any claimed false-admission-rate below ~5% measured with Tier-1 only is meaningless.

**3. Strict public/hidden evidence separation.** Everything visible to agents and to the Multiverso scheduler — generated tests, reproduction scripts, builds, lint, self-reported confidence — is *public evidence*. Tier-2/Tier-3 oracles are *hidden* and used exactly once per experimental cell for final scoring (Kaggle/Ladder discipline; CodeMonkeys' selector-never-sees-golden-tests precedent). The scheduler must be *judged* by hidden tests but must never *learn from* them, otherwise the experiment measures oracle leakage, not scheduling. Freeze and hash the hidden suites; Multiverso's own attestation machinery should sign the evidence→decision→verdict chain of the experiment itself, making the eval audit-reproducible.

**4. Budget definition and matching.** Define total budget per instance as
`B = c_tok · (input + output tokens across ALL roles: generation, testing, challenge, judging, selection) + c_cpu · runner-seconds + c_orc · oracle executions + c_sel · selection compute`,
normalized to dollars at published prices, with wall-clock reported separately. Every arm gets identical B (three levels: e.g., $2, $8, $32/instance to trace Pareto frontiers, per [Kapoor et al.](https://arxiv.org/abs/2407.01502)). Comparing 8 worlds against 1 agent without equal B only re-proves pass@k scaling ([Large Language Monkeys](https://arxiv.org/abs/2407.21787)); the single-agent baseline must be allowed to spend its whole B serially (more turns, self-repair, its own tests), which is exactly the [compute-optimal](https://arxiv.org/abs/2408.03314) serial-vs-parallel trade-off. Selection cost is inside B — CodeMonkeys shows it can be cheap (5.8%), but it must be counted.

**5. Arms.** All sharing model, scaffold, and B:
1. **Single agent** — one world, full budget, serial self-repair.
2. **Best-of-N random** — N worlds, uniform split, random admission (floor; CodeMonkeys: 45.8% vs 57.4% selected vs 69.8% oracle).
3. **Best-of-N public tests** — admit the candidate passing most agent-generated public tests.
4. **Best-of-N LLM judge** — execution-free judge/reward model (SWE-RM-style) picks; known to be biased toward stylistic features ([R2E-Gym](https://arxiv.org/abs/2504.07164)).
5. **Test-voting (CodeMonkeys-style)** — co-generated test scripts, majority voting plus a fixed selection state machine.
6. **Multiverso adaptive scheduler** — dynamic reallocation among generation, testing, and adversarial challenge based on accumulated evidence; may kill worlds early, spawn discriminating tests, or trigger REPAIR.
7. **Retrospective oracle** — admit any candidate that Tier-2 passes (coverage ceiling; not a real policy, defines regret).

**6. Metrics.** Per arm × budget × benchmark stratum:
- **True-correct admission rate (TCAR)**: admitted ∧ Tier-2 correct / instances.
- **False-admission rate (FAR)**: admitted ∧ Tier-2 incorrect / admitted — the headline safety metric; no leaderboard currently reports it.
- **Cost per truly-correct merge**: B_total / Tier-2-correct admissions (CodeMonkeys' $2,300-per-run accounting is the precedent).
- **Decision latency**: wall-clock intent→admission/rejection.
- **Selection regret**: oracle-arm TCAR − arm TCAR (formalizing CodeMonkeys' 12.4 pp gap).
- **Calibration**: Brier score / ECE of the scheduler's admission confidence against Tier-2 truth (precedent: [SWE-RM](https://arxiv.org/abs/2512.21919) verifier calibration); a well-calibrated ABSTAIN/REJECT is a product feature.
- **Evidence waste**: fraction of B spent on worlds/evidence that influenced no decision boundary (novel; no precedent found).
- **Post-integration regressions**: after admission, run the strengthened suite of the *next* k chronological instances of the same repo on the merged trunk (SWE-bench-Live's chronological stream makes this possible; no published precedent).
- **Reliability & model-swap stability**: pass^k over 3+ seeds ([τ-bench](https://arxiv.org/abs/2406.12045)); repeat all arms with ≥2 model families and report rank correlation of arms across models — a scheduler that only wins with one model is a prompt hack.

**7. Statistics and hygiene.** Paired per-instance design (all arms see identical instances); McNemar's test for paired admission outcomes and BCa-bootstrap CIs for rates; ≥300 instances per stratum to resolve ~5 pp TCAR differences at conventional power; pre-registered protocol, metrics, and stopping rules before the adaptive scheduler is tuned; hidden Tier-2 suites versioned and never used for development iterations (development uses a disjoint dev split).

## Open questions

1. **Who tests the tests?** Tier-2 oracles (UTBoost/SWE-ABS-style generated tests) are themselves LLM products; their false-*negative* rate (rejecting valid alternative implementations — the flaw OpenAI found in 59.4% of audited Verified failures) is unmeasured. Manual adjudication caps the error but doesn't scale.
2. **Behavioral divergence ≠ incorrectness.** PatchDiff found 29.6% divergence, but only ~28.6% of divergent patches are certainly wrong. Where should ADMIT draw the line on intentional-but-different behavior for underspecified intents?
3. **Greenfield oracles.** For Commit0-style tasks there is no human patch to diff against; differential evaluation collapses to spec-based testing. Can property-based test synthesis from the versioned Intent stand in?
4. **Budget fungibility.** Tokens, runner-seconds, and oracle executions are priced in dollars, but relative prices shift with hardware and model generations; results should be reported with sensitivity analysis over the price vector, or the "compute-optimal" claim may not transfer.
5. **Adaptive-scheduler overfitting to the eval.** An adaptive policy tuned on a dev split of SWE-bench-Live may exploit curation artifacts of RepoLaunch rather than general signal; the monthly-fresh test stream mitigates but doesn't eliminate this.
6. **Sequential trunk evaluation.** Post-integration regression measurement (metric 8) composes admissions over time — errors compound and instances stop being independent. No existing benchmark scores *trajectories of merges*; building one is an open contribution.

## Sources

- SWE-bench: Can Language Models Resolve Real-World GitHub Issues? - https://arxiv.org/abs/2310.06770 - Oct 2023 (ICLR 2024)
- Introducing SWE-bench Verified - https://openai.com/index/introducing-swe-bench-verified/ - Aug 2024
- Why SWE-bench Verified no longer measures frontier coding capabilities - https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/ - Feb 2026
- OpenAI Devs announcement (SWE-bench Pro recommendation) - https://x.com/OpenAIDevs/status/2026002219909427270 - Feb 2026
- SWE-Bench+: Enhanced Coding Benchmark for LLMs - https://arxiv.org/abs/2410.06992 - Oct 2024
- Are "Solved Issues" in SWE-bench Really Solved Correctly? An Empirical Study (PatchDiff) - https://arxiv.org/abs/2503.15223 - Mar 2025
- UTBoost: Rigorous Evaluation of Coding Agents on SWE-Bench - https://arxiv.org/abs/2506.09289 - Jun 2025
- The SWE-Bench Illusion: When State-of-the-Art LLMs Remember Instead of Reason - https://arxiv.org/abs/2506.12286 - Jun 2025
- SWE-ABS: Adversarial Benchmark Strengthening Exposes Inflated Success Rates - https://arxiv.org/abs/2603.00520 - Feb 2026
- Saving SWE-Bench: A Benchmark Mutation Approach for Realistic Agent Evaluation - https://arxiv.org/abs/2510.08996 - Oct 2025 (CAIN 2026)
- Is Your Code Generated by ChatGPT Really Correct? (EvalPlus) - https://arxiv.org/abs/2305.01210 - May 2023 (NeurIPS 2023)
- SWE-Bench Pro: Can AI Agents Solve Long-Horizon Software Engineering Tasks? - https://arxiv.org/abs/2509.16941 - Sep 2025
- SWE-Bench Pro private (commercial) leaderboard - https://labs.scale.com/leaderboard/swe_bench_pro_private - 2025
- Multi-SWE-bench: A Multilingual Benchmark for Issue Resolving - https://arxiv.org/abs/2504.02605 - Apr 2025 (NeurIPS 2025 D&B)
- SWE-bench Goes Live! - https://arxiv.org/abs/2505.23419 - May 2025 (NeurIPS 2025 D&B)
- microsoft/SWE-bench-Live (monthly updates, RepoLaunch) - https://github.com/microsoft/SWE-bench-Live - accessed Aug 2026
- Training Software Engineering Agents and Verifiers with SWE-Gym - https://arxiv.org/abs/2412.21139 - Dec 2024 (ICML 2025)
- R2E-Gym: Procedural Environments and Hybrid Verifiers for Scaling Open-Weights SWE Agents - https://arxiv.org/abs/2504.07164 - Apr 2025 (COLM 2025)
- Commit0: Library Generation from Scratch - https://arxiv.org/abs/2412.01769 - Dec 2024
- RepoBench: Benchmarking Repository-Level Code Auto-Completion Systems - https://arxiv.org/abs/2306.03091 - Jun 2023 (ICLR 2024)
- SWE-Lancer: Can Frontier LLMs Earn $1 Million from Real-World Freelance Software Engineering? - https://arxiv.org/abs/2502.12115 - Feb 2025
- Terminal-Bench: Benchmarking Agents on Hard, Realistic Tasks in Command Line Interfaces - https://arxiv.org/abs/2601.11868 - Jan 2026
- LiveCodeBench: Holistic and Contamination Free Evaluation of LLMs for Code - https://arxiv.org/abs/2403.07974 - Mar 2024
- Large Language Monkeys: Scaling Inference Compute with Repeated Sampling - https://arxiv.org/abs/2407.21787 - Jul 2024
- CodeMonkeys: Scaling Test-Time Compute for Software Engineering - https://arxiv.org/abs/2501.14723 - Jan 2025
- SWE-Search: Enhancing Software Agents with Monte Carlo Tree Search and Iterative Refinement - https://arxiv.org/abs/2410.20285 - Oct 2024
- Scaling LLM Test-Time Compute Optimally can be More Effective than Scaling Model Parameters - https://arxiv.org/abs/2408.03314 - Aug 2024
- AI Agents That Matter - https://arxiv.org/abs/2407.01502 - Jul 2024
- τ-bench: A Benchmark for Tool-Agent-User Interaction in Real-World Domains - https://arxiv.org/abs/2406.12045 - Jun 2024
- SWE-RM: Execution-free Feedback For Software Engineering Agents - https://arxiv.org/abs/2512.21919 - Dec 2025
- The Ladder: A Reliable Leaderboard for Machine Learning Competitions - https://proceedings.mlr.press/v37/blum15.pdf - 2015 (ICML)
- A Meta-Analysis of Overfitting in Machine Learning - https://papers.nips.cc/paper/2019/file/ee39e503b6bedf0c98c388b7e8589aca-Paper.pdf - 2019 (NeurIPS)
