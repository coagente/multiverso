# 19. MVP Oracle Toolchain & Evaluation Harness

> Part of the Multiverso research corpus - https://github.com/coagente/multiverso - Cutoff: 2026-08-12

## Why this matters for Multiverso

Multiverso's core loop turns oracle output into **Evidence receipts** cryptographically bound to a world's exact tree. That only works if every oracle in the MVP (a) emits machine-readable results, (b) has deterministic-enough invocation semantics to be replayed, and (c) has a known cost profile so the evidence-aware scheduler (the white space identified in [ch. 3](03-adaptive-verification-scheduling.md)) can price it. Chapter 8 established that stock test suites are too weak to carry ADMIT decisions alone; this chapter specifies the concrete, currently-shipping tools — exact versions, flags, and output files as of 2026-08-12 — that form the MVP oracle ladder (Python first, then JS/TS and Rust), plus the evaluation harness that will produce the TCAR/FAR numbers specified in [ch. 9](09-benchmarks-evaluation.md).

## State of the art

### (a) Test runners and machine-readable output

**Python.** [pytest 9.1.1](https://pypi.org/project/pytest/) (released 2026-06-19, Python ≥3.10) is the default runner. Two output channels matter:

- `--junit-xml=path` writes JUnit XML, with `record_property` injecting extra per-test properties into the XML ([pytest output docs](https://docs.pytest.org/en/stable/how-to/output.html)).
- [pytest-reportlog](https://pypi.org/project/pytest-reportlog/) (`--report-log=FILE`) writes **one self-contained JSON object per line, flushed after each line**, so a supervisor can stream test events in real time — the pytest team positions it as the extensible replacement for result logs ([pytest issue #4488](https://github.com/pytest-dev/pytest/issues/4488)). The popular [pytest-json-report](https://pypi.org/project/pytest-json-report/) produces a richer single-file report (summary, per-test outcome/duration/traceback, captured output, environment) but its last release is **1.5.0 from 2022-03-15** — effectively unmaintained; Multiverso should not take a hard dependency on it.

pytest's [exit codes](https://docs.pytest.org/en/stable/reference/exit-codes.html) are load-bearing for admission: `0` all passed, `1` some failed, `2` interrupted, `3` internal error, `4` usage error, `5` **no tests collected**, `6` maximum warnings exceeded. Exit 5 is an adversarial hazard: a candidate that deletes or de-collects tests exits nonzero, but naive "`!= 1` means fine" logic (or `pytest || true` wrappers) has historically laundered it. The receipt must record the raw exit code, never a boolean.

Coverage: [coverage.py 7.15.4](https://pypi.org/project/coverage/) (2026-08-06, Python 3.10–3.15) provides `coverage json`, `coverage lcov`, `coverage xml` reporters and `coverage combine` for merging parallel data files ([commands reference](https://coverage.readthedocs.io/en/7.15.4/commands/index.html)). Dynamic contexts (`[run] dynamic_context = test_function`) attribute covered lines to individual tests — the cheap per-test impact map [ch. 11](11-knowledge-plane.md) calls for.

**JS/TS.** [Vitest 4.1.x](https://vitest.dev/guide/reporters) ships built-in machine-readable reporters: `--reporter=json --outputFile=…` (Jest-compatible JSON, includes `coverageMap` when coverage is on), `--reporter=junit` (with `suiteName`/`classnameTemplate`), `--reporter=github-actions`, and a `--reporter=blob` format designed for sharded runs merged later via `--merge-reports`. Jest offers [`--json --outputFile=… --testLocationInResults`](https://jestjs.io/docs/cli), with JUnit via the third-party `jest-junit` reporter.

**Rust.** [cargo-nextest](https://nexte.st/docs/machine-readable/) is the runner of choice (per-test process isolation, retries, JUnit XML as the primary machine-readable channel for runs). Its JSON story is still experimental: `NEXTEST_EXPERIMENTAL_LIBTEST_JSON=1 cargo nextest run --message-format libtest-json|libtest-json-plus` (the `-plus` variant adds a `nextest` metadata subobject), format version `0.1`, explicitly subject to change ([libtest-json docs](https://nexte.st/docs/machine-readable/libtest-json/), [tracking issue #1152](https://github.com/nextest-rs/nextest/issues/1152)); the upstream libtest JSON stabilization is an active [Rust project goal](https://rust-lang.github.io/rust-project-goals/2025h1/libtest-json.html). MVP guidance: parse nextest's JUnit XML as the stable interface, archive the libtest-json-plus stream as auxiliary evidence.

### (b) Mutation testing per language

- **Python — [mutmut 3.7.0](https://pypi.org/project/mutmut/)** (2026-07-31, Python ≥3.10, BSD-3; requires `fork`, so Linux/WSL only). Mutmut 3 rewrote the execution model around mutant *schemata* ("trampolines"): all mutants are compiled into the module once, activated per-run, avoiding reinstall-per-mutant. It "knows which tests to execute" per mutant, works **incrementally** across runs, runs in parallel, and can restrict mutation to covered lines via `mutate_only_covered_lines` using coverage.py data ([docs](https://mutmut.readthedocs.io/)). Commands: `mutmut run`, `mutmut results`, `mutmut browse` (TUI), `mutmut apply <id>`. Configuration lives in `setup.cfg`/`pyproject.toml` (`paths_to_mutate`/`source_paths`, `do_not_mutate`, pragmas).
- **Python — [cosmic-ray 8.7.0](https://pypi.org/project/cosmic-ray/)** (2026-08-09, Python 3.9–3.13) remains actively maintained; it uses a session database and pluggable distributors for distributed execution. It is the fallback if mutmut's fork requirement or trampoline model conflicts with a target repo; mutmut is the default for speed and incremental state.
- **JS/TS — [StrykerJS 9.6.1](https://github.com/stryker-mutator/stryker-js/releases)** (2026-04-10). Its `--incremental` mode stores results in `reports/stryker-incremental.json` and re-runs only mutants whose code or covering tests changed, via a git-like diff; in the team's own example an incremental run reused **3,731 of 3,965 mutant results, leaving 234 to execute** ([incremental docs](https://stryker-mutator.io/docs/stryker-js/incremental/), [announcement](https://stryker-mutator.io/blog/announcing-incremental-mode/)). Limitation: changes outside mutated/test files (dependencies, env) are invisible to the diff — a receipt-freshness hazard Multiverso's evidence typing ([ch. 5](05-speculative-admission.md)) must cover.
- **Rust — [cargo-mutants 27.1.0](https://mutants.rs/changelog.html)** (2026-06-02, MSRV 1.88). The best-instrumented tool of the set: `--in-diff FILE` tests **only mutants in a given diff** ([in-diff docs](https://mutants.rs/in-diff.html)) — a perfect match for candidate patches; `--shard k/n` and `--jobs` distribute work; `--baseline=skip` reuses a verified baseline; auto-timeout is **5× baseline test time, minimum 20 s**, tunable via `--timeout`/`--timeout-multiplier` ([timeouts](https://mutants.rs/timeouts.html)). Output is a full evidence bundle in `mutants.out/`: `mutants.json` (all generated mutants), `outcomes.json` (results + summary + tool version), per-mutant diffs and logs, `caught.txt`/`missed.txt`/`timeout.txt`/`unviable.txt` ([mutants-out docs](https://mutants.rs/mutants-out.html)).

**Cost model.** Mutation testing's cost is ~(#viable mutants × kill-time), where kill-time is bounded by the timeout multiplier; unscoped runs on real repos are hours-to-days. Google's production system concluded that "traditional mutation testing does not scale" at their size and ships **diff-scoped mutation with per-line mutant caps and operator selection**, generating "orders of magnitude fewer mutants" ([Petrović et al., Practical Mutation Testing at Scale](https://arxiv.org/abs/2102.11378)). For Multiverso this settles the invocation mode: mutation is a *diff-scoped, budget-capped, asynchronous* oracle (deepest rung of the ladder), never a default gate.

### (c) Property-based testing

- **Python — [Hypothesis 6.165.5](https://pypi.org/project/hypothesis/)** (2026-08-12). Its **observability mode** is the single most receipt-shaped feature in the toolbox: setting `HYPOTHESIS_EXPERIMENTAL_OBSERVABILITY` makes Hypothesis write JSONL files to `.hypothesis/observed/` with one record per test case containing `status`, `arguments` (structured JSON), `representation`, `how_generated`, `features` (`target()`/`event()` observations), per-file line `coverage`, `timing` breakdown, and `metadata` including the reproduction decorator and traceback ([integrations reference](https://hypothesis.readthedocs.io/en/latest/reference/integrations.html)). The [Tyche](https://github.com/tyche-pbt/tyche-extension) VS Code extension consumes this format natively. Caveat: the feature is explicitly experimental and may change without notice.
- **JS/TS — [fast-check](https://github.com/dubzzz/fast-check/releases)** (current 4.x line, latest v4.9.0) reports a failing counterexample with its `seed` and `path`, giving replayable repro strings.
- **Rust — [proptest 1.11.0](https://docs.rs/proptest/latest/proptest/)** (2026-07-04), "Hypothesis-like property-based testing and shrinking," with failure-persistence regression files committed to the repo.

**LLM-generated properties** are now demonstrated at ecosystem scale: [Vikram et al.](https://arxiv.org/abs/2307.04346) established the validity/soundness evaluation frame for GPT-4-written Hypothesis tests, and the 2025 **Agentic Property-Based Testing** paper (Maaz, DeVoe, Hatfield-Dodds, Carlini) ran an LLM agent that infers invariants, synthesizes and executes Hypothesis PBTs, and self-triages: across 100 popular Python packages, **56% of its bug reports were genuine, 86% of its top-21 prioritized reports were valid, and 3 maintainer patches were merged** ([arXiv:2510.09907](https://arxiv.org/abs/2510.09907)). For Multiverso this is the template for the *property-synthesis oracle*: generate properties once per intent (not per candidate), execute the same property corpus across all N worlds, and diff behavior — the cross-candidate differential oracle [ch. 8](08-oracles-verification.md) found no prior art for.

### (d) Flake handling

[pytest-rerunfailures 16.3](https://pytest-rerunfailures.readthedocs.io/latest/index.html) (2026-05-22, actively maintained under pytest-dev) provides `--reruns N` (optionally `--only-rerun <regex>` and per-test `@pytest.mark.flaky(reruns=n)`). Rerun-on-failure is simultaneously the detection mechanism (pass-on-rerun ⇒ flaky) and an evidence-quality hazard: a rerun-pass is strictly weaker evidence than a first-run pass, and the receipt must distinguish them.

CI products have converged on a **quarantine** state machine worth copying. [Datadog Flaky Test Management](https://docs.datadoghq.com/tests/flaky_management/) identifies tests by a stable fingerprint (hash of repo ID + fully-qualified test name), and its *quarantine* action "keep[s] the test running in the background, but failures don't affect CI status," tagged `@test.test_management.is_quarantined:true`; *disable* skips the test entirely; a *fix attempt* triggers up to 20 automatic retries on the fix commit. [BuildPulse](https://buildpulse.io/products/flaky-tests) similarly auto-detects and auto-quarantines the worst offenders from CI uploads. Multiverso's version: a per-repo quarantine set stored in the control plane; quarantined oracles still run and still emit receipts, but the scheduler assigns their evidence near-zero decision weight, and disagreement between candidates on a quarantined test is still differential signal.

### (e) Sandboxed execution costs

For the MVP, worlds execute oracles in Docker containers with explicit caps — all flags verified against the [docker run reference](https://docs.docker.com/reference/cli/docker/container/run/) and [resource-constraints docs](https://docs.docker.com/engine/containers/resource_constraints/): `--memory` / `--memory-swap`, `--cpus`, `--pids-limit` (fork-bomb guard), `--network none`, `--read-only` (with tmpfs work mounts), `--cap-drop`, `--security-opt no-new-privileges`, `--ulimit`. Every receipt records the image digest plus these caps as the *isolation tier* field mandated by [ch. 1](01-parallel-exploration.md).

Latency landscape (2026): plain Docker container cold start is ~1–2 s including image handling, while Firecracker microVMs boot in ~100–200 ms and snapshot-restore reaches ~49 ms p50 ([PandaStack](https://www.pandastack.ai/blog/firecracker-vs-docker/)); gVisor starts in ~50–100 ms and Kata Containers in ~150–300 ms ([MicroVM isolation survey](https://emirb.github.io/blog/microvm-2026/)); warm-pool patterns (pre-booted sandbox pools, e.g. Kubernetes `SandboxWarmPool`-style CRDs) bring effective start below one second at the orchestration layer ([AI-agent sandboxing guide](https://manveerc.substack.com/p/ai-agent-sandboxing-guide)). Practical consequence: sandbox spin-up (≤2 s with a warm image cache) is noise next to dependency install (tens of seconds to minutes) — so the MVP's real warm-pool artifact is the **pre-built per-repo environment image** (exactly what RepoLaunch/SWE-bench produce), not a fancy microVM. MicroVM forking is a Phase-C optimization ([ch. 1](01-parallel-exploration.md) found warm full-VM forking <250 ms already demonstrated).

### (f) Evaluation harness

- **[SWE-bench-Live](https://github.com/microsoft/SWE-bench-Live)** (Microsoft, NeurIPS 2025 D&B) is the contamination-resistant workhorse: **each month, 50 newly verified issues are added to the test split**, while `lite` and `verified` splits stay frozen for leaderboard comparability. Environments are built by **RepoLaunch**, an "LLM-based agentic tool" that delivers a testable containerized environment per repository, yielding instance-level Docker images ([paper](https://arxiv.org/abs/2505.23419)). Datasets on HuggingFace: `SWE-bench-Live/SWE-bench-Live` (Python; evaluated with the `swebench` library on the project's Python-only branch), `SWE-bench-Live/MultiLang` (**743 tasks, 6 languages, 381 repos**), and `SWE-bench-Live/Windows` (61 tasks). MultiLang evaluation: `python -m evaluation.evaluation --dataset SWE-bench-Live/MultiLang --instance_ids <id> --platform linux --patch_dir gold --output_dir logs/test --workers N`. MIT-licensed.
- **[SWE-bench Pro](https://github.com/scaleapi/SWE-bench_Pro-os)** (Scale AI): harder, long-horizon tasks; the public set (HuggingFace `ScaleAI/SWE-bench_Pro`, strong-copyleft repos to deter training contamination) has an open harness — `python swe_bench_pro_eval.py --raw_sample_path=… --patch_path=… --scripts_dir=run_scripts --num_workers=100` — with **prebuilt per-instance Docker images at `jefzda/sweap-images:<tag>`**. A held-out commercial/private set backs a [separate leaderboard](https://labs.scale.com/leaderboard/swe_bench_pro_private); top models score far lower than on Verified, and private-set scores drop further than public ones (public leaderboard: [labs.scale.com](https://labs.scale.com/leaderboard/swe_bench_pro_public)). Private-set access conditions are not published (unverified).
- **[Multi-SWE-bench](https://github.com/multi-swe-bench/multi-swe-bench)** (ByteDance Seed, NeurIPS 2025): **1,632 instances across Java, TypeScript, JavaScript, Go, Rust, C, C++**, with `mini` (400) and `flash` (300) subsets; evaluation via `python -m multi_swe_bench.harness.run_evaluation --config config.json` (config: `mode`, `workdir`, `patch_files`, `dataset_files`, `force_build`, `max_workers`…), pre-built images fetched by `scripts/download_images.sh`; datasets under the ByteDance-Seed HuggingFace org.
- **[Commit0](https://github.com/commit-0/commit0)** (Cornell/Cohere, [arXiv:2412.01769](https://arxiv.org/abs/2412.01769)): 54 Python libraries generated *from scratch* against specs + hidden unit tests, with `commit0 setup/build/test/evaluate` commands — the right harness for testing Multiverso's generative (not just repair) claims, where N-candidate divergence is largest.
- **Official SWE-bench evaluation** is available both locally via the dockerized harness ([docker setup guide](https://www.swebench.com/SWE-bench/guides/docker_setup/)) and via [sb-cli](https://www.swebench.com/sb-cli/), the cloud evaluation API (submit predictions, retrieve reports, quota-managed).

**Budget accounting.** Third parties run budget-accounted experiments by capturing the agent CLI's own accounting. Claude Code headless mode (`claude -p … --output-format json`) returns `total_cost_usd`, token usage, and a per-model breakdown in the result JSON — explicitly a client-side estimate ([headless docs](https://code.claude.com/docs/en/headless)) — enabling per-instance cost capture with a jq one-liner. Trajectory-level token counts differ by an order of magnitude across scaffold×model pairs (e.g., mini-SWE-agent ~1.9M tokens/instance under DeepSeek vs ~254K under GPT in one measurement study, [arXiv:2603.26337](https://arxiv.org/pdf/2603.26337)), so Multiverso's evaluation must record **per-instance, per-arm**: prompt/completion tokens, `total_cost_usd` (or provider-metered equivalent), oracle CPU-seconds, and wall-clock — the inputs the 7-arm budget-matched protocol of [ch. 9](09-benchmarks-evaluation.md) requires.

### (g) What each tool can put in a receipt

Every tool above emits (or can be wrapped to emit) the fields Multiverso needs. The common envelope: `{tool, version, argv, exit_code, started_at, duration_ms, input_tree_hash, env_image_digest, isolation_caps, output_artifact_hashes[]}`. Tool-specific payloads:

| Oracle | Native artifact | Receipt-grade fields available |
|---|---|---|
| pytest | JUnit XML + reportlog JSONL | per-test outcome/duration/traceback; exit code 0–6; warnings; `record_property` custom fields |
| coverage.py | `coverage.json` / lcov | per-file line+branch data; dynamic per-test contexts; data-file hash |
| Hypothesis | `.hypothesis/observed/*.jsonl` | per-case status, arguments, coverage, timing, `how_generated`, repro decorator, seed/DB state |
| mutmut / cargo-mutants / Stryker | results DB / `mutants.out/outcomes.json` / `stryker-incremental.json` | mutant list + per-mutant outcome, killing test, diff, timeout config, mutation score |
| cargo-nextest | JUnit XML; libtest-json-plus stream | per-test status/duration/retries; format version |
| vitest/jest | JSON (+`coverageMap`), JUnit | per-test status/duration/location; shard blobs for merge |
| pytest-rerunfailures | reportlog events | rerun count, first-run vs final status (flake bit) |
| Agent CLI | result JSON | `total_cost_usd`, tokens, model, session id |
| Sandbox | docker inspect | image digest, caps (`--memory/--cpus/--pids-limit/--network`), OOM-kill flag |

## Comparison table

MVP oracle matrix (all versions verified current as of 2026-08-12):

| Capability | Python (Phase A) | JS/TS (Phase B) | Rust (Phase B) |
|---|---|---|---|
| Runner | pytest **9.1.1**: `--junit-xml` + `--report-log` | Vitest **4.1.x**: `--reporter=json,junit`; Jest `--json` | cargo-nextest: JUnit stable; libtest-json experimental |
| Coverage | coverage.py **7.15.4**: `json`/`lcov`, dynamic contexts | Vitest `coverageMap` (v8/istanbul) | `cargo llvm-cov` (lcov) (unverified version) |
| Mutation | mutmut **3.7.0** (trampolines, covered-lines scope, incremental); cosmic-ray **8.7.0** fallback | StrykerJS **9.6.1** `--incremental` | cargo-mutants **27.1.0** `--in-diff`, `--shard`, `outcomes.json` |
| PBT | Hypothesis **6.165.5** + observability JSONL | fast-check **4.x** (seed/path repro) | proptest **1.11.0** (regression files) |
| Flake control | pytest-rerunfailures **16.3** `--reruns` | Vitest `retry` / jest-junit rerun data | nextest built-in retries |
| Diff-scoping | mutmut incremental + covered-lines | Stryker incremental JSON | `--in-diff` (native, best-in-class) |

## Recommendation for the PRD

**1. Ship the Python oracle ladder first (Phase A), as four rungs with fixed invocation modes.**

- **O0 build/smoke** — `pip install -e . && pytest --collect-only`; receipt = exit code + collected-test count (guards exit-5 laundering).
- **O1 suite** — `pytest --junit-xml=r.xml --report-log=r.jsonl -p no:cacheprovider`, with `coverage run -p` + `coverage combine` + `coverage json`; flake policy `--reruns 2` with first-run status preserved. This is the default gate for every candidate.
- **O2 property/differential** — Hypothesis with `HYPOTHESIS_EXPERIMENTAL_OBSERVABILITY` (pin the Hypothesis version in the receipt; the format is experimental); intent-level LLM-synthesized property corpus executed identically across all N worlds, behavior diffs recorded (the ch. 8 differential oracle).
- **O3 mutation (async, budget-capped)** — mutmut 3.7 with `mutate_only_covered_lines` scoped to the candidate diff's files, hard wall-clock budget from the scheduler; runs post-selection to strengthen evidence on the leading candidate, never blocks the race by default (Google's diff-scoped precedent).

**2. Standardize the receipt envelope now** — `{tool, version, argv, exit_code, duration_ms, seeds, input_tree_hash, env_image_digest, isolation_caps, artifact_hashes}` — and store every native artifact (JUnit XML, reportlog JSONL, coverage.json, observed/*.jsonl, outcomes.json) content-addressed. Do not build per-tool parsers into the trust boundary; hash-and-store first, parse into typed evidence second.

**3. Sandbox = Docker with explicit caps + pre-built env images.** `docker run --network none --read-only --memory 4g --cpus 2 --pids-limit 512 --cap-drop ALL --security-opt no-new-privileges <env-image>`. The warm-pool investment is the per-repo environment image cache (RepoLaunch-style), not microVMs; revisit Firecracker snapshotting only when scheduler inner-loop latency demands it.

**4. Evaluation: Phase A = SWE-bench-Live (Python), Phase B = Multi-SWE-bench mini + SWE-bench Pro public + Commit0.** Phase A runs the frozen `lite`/`verified` splits of SWE-bench-Live for comparability plus the newest monthly slice for contamination resistance, using the project's instance images; report TCAR/FAR under the ch. 9 protocol with per-instance budget capture from the agent CLI JSON (`total_cost_usd`, tokens) joined to oracle CPU-seconds. Phase B adds Multi-SWE-bench `mini` (400 instances) for JS/TS+Rust once the Phase-B oracles land, SWE-bench Pro public (`jefzda/sweap-images`) as the hard set, and Commit0 for the generative regime where N-candidate diversity matters most.

**5. Explicit non-picks.** pytest-json-report (unmaintained since 2022) — use reportlog + JUnit; cosmic-ray as default (mutmut is faster and incremental; keep cosmic-ray as fallback); nextest libtest-json as a stable interface (experimental; JUnit is the contract); building our own flake detector (adopt the fingerprint + quarantine state machine proven by Datadog/BuildPulse, implemented in the control plane).

## Open questions

1. **Hypothesis observability schema drift** — the feature is explicitly experimental; does the PRD pin Hypothesis per-repo and version the receipt parser, or contribute a stability guarantee upstream?
2. **nextest libtest-json stabilization** — format 0.1 is experimental with an open stabilization effort; when it lands, does Multiverso migrate the Rust receipt payload or keep JUnit as the contract forever?
3. **Mutation cost calibration** — the scheduler needs per-repo priors for kill-time distribution (cargo-mutants' 5×-baseline timeout is a starting heuristic); how are these priors bootstrapped on first contact with a repo?
4. **SWE-bench-Live monthly image logistics** — registry pull-rate limits and storage cost for ~50 new instance images/month across the fleet are unquantified; a local registry mirror is probably required (unverified).
5. **Quarantine store ownership** — does the flake quarantine set live in the repo (reviewable, like Stryker's incremental file) or in the control plane (consistent across candidates)? The control plane is recommended here, but it weakens Git-compatibility purity.
6. **SWE-bench Pro private set access** — conditions for third-party evaluation on the held-out commercial set are not published; budget Phase B assuming public-set-only.
7. **JS/TS coverage-to-test attribution** — coverage.py dynamic contexts have no exact Vitest equivalent surfaced in the reporters docs; per-test impact maps for JS may need Vitest API work (unverified).

## Sources

- pytest 9.1.1 — https://pypi.org/project/pytest/ · exit codes — https://docs.pytest.org/en/stable/reference/exit-codes.html · JUnit XML — https://docs.pytest.org/en/stable/how-to/output.html
- pytest-reportlog — https://pypi.org/project/pytest-reportlog/ · pytest issue #4488 — https://github.com/pytest-dev/pytest/issues/4488 · pytest-json-report 1.5.0 — https://pypi.org/project/pytest-json-report/
- coverage.py 7.15.4 — https://pypi.org/project/coverage/ · commands — https://coverage.readthedocs.io/en/7.15.4/commands/index.html
- Vitest reporters — https://vitest.dev/guide/reporters · Jest CLI — https://jestjs.io/docs/cli
- cargo-nextest machine-readable — https://nexte.st/docs/machine-readable/ · libtest-json — https://nexte.st/docs/machine-readable/libtest-json/ · tracking issue — https://github.com/nextest-rs/nextest/issues/1152 · Rust project goal — https://rust-lang.github.io/rust-project-goals/2025h1/libtest-json.html
- mutmut 3.7.0 — https://pypi.org/project/mutmut/ · docs — https://mutmut.readthedocs.io/ · cosmic-ray 8.7.0 — https://pypi.org/project/cosmic-ray/
- StrykerJS releases — https://github.com/stryker-mutator/stryker-js/releases · incremental — https://stryker-mutator.io/docs/stryker-js/incremental/ · announcement — https://stryker-mutator.io/blog/announcing-incremental-mode/
- cargo-mutants changelog — https://mutants.rs/changelog.html · in-diff — https://mutants.rs/in-diff.html · mutants.out — https://mutants.rs/mutants-out.html · timeouts — https://mutants.rs/timeouts.html
- Petrović et al., Practical Mutation Testing at Scale — https://arxiv.org/abs/2102.11378
- Hypothesis 6.165.5 — https://pypi.org/project/hypothesis/ · observability/integrations — https://hypothesis.readthedocs.io/en/latest/reference/integrations.html · Tyche — https://github.com/tyche-pbt/tyche-extension
- fast-check releases — https://github.com/dubzzz/fast-check/releases · proptest 1.11.0 — https://docs.rs/proptest/latest/proptest/
- Vikram et al., Can LLMs Write Good Property-Based Tests? — https://arxiv.org/abs/2307.04346 · Agentic PBT — https://arxiv.org/abs/2510.09907
- pytest-rerunfailures — https://pytest-rerunfailures.readthedocs.io/latest/index.html · Datadog Flaky Test Management — https://docs.datadoghq.com/tests/flaky_management/ · BuildPulse — https://buildpulse.io/products/flaky-tests
- docker run reference — https://docs.docker.com/reference/cli/docker/container/run/ · resource constraints — https://docs.docker.com/engine/containers/resource_constraints/
- Firecracker vs Docker — https://www.pandastack.ai/blog/firecracker-vs-docker/ · MicroVM isolation 2026 — https://emirb.github.io/blog/microvm-2026/ · AI-agent sandboxing — https://manveerc.substack.com/p/ai-agent-sandboxing-guide
- SWE-bench-Live — https://github.com/microsoft/SWE-bench-Live · paper — https://arxiv.org/abs/2505.23419 · leaderboard — https://swe-bench-live.github.io/
- SWE-bench Pro — https://github.com/scaleapi/SWE-bench_Pro-os · public leaderboard — https://labs.scale.com/leaderboard/swe_bench_pro_public · private leaderboard — https://labs.scale.com/leaderboard/swe_bench_pro_private
- Multi-SWE-bench — https://github.com/multi-swe-bench/multi-swe-bench · PyPI — https://pypi.org/project/multi-swe-bench/
- Commit0 — https://github.com/commit-0/commit0 · paper — https://arxiv.org/abs/2412.01769
- SWE-bench docker setup — https://www.swebench.com/SWE-bench/guides/docker_setup/ · sb-cli — https://www.swebench.com/sb-cli/
- Claude Code headless mode — https://code.claude.com/docs/en/headless
- Agent token-cost measurement — https://arxiv.org/pdf/2603.26337
