# Multiverso Research Corpus

> Exhaustive state-of-the-art research for **Multiverso** — an evidence-native, Git-compatible control plane for speculative software change produced by AI agents.
> Corpus cutoff: **2026-08-12** · 15 chapters · ~60,000 words · [240 sources](BIBLIOGRAPHY.md) · [13/13 founding-memo claims verified](VERIFICATION.md)

This corpus was produced by a multi-agent research workflow (12 dimension deep-dives + 3 adversarial fact-checkers + 1 completeness critic that triggered 3 additional chapters), with every factual claim cited to a primary source. It stress-tests the thesis of the founding memo ([`../RESEARCH.md`](../RESEARCH.md), Spanish) and maps the terrain Multiverso would occupy.

## The headline findings

**1. The conjunction is empty — but it is closing fast.**
Multiverso's claim rests on five capabilities: **C1** versioned intents, **C2** exploration of alternative implementations, **C3** adaptive allocation of verification compute, **C4** evidence cryptographically bound to exact candidate state, **C5** admission of the integrated state to trunk. Every capability *individually* is occupied; **no surveyed system holds more than two**, and the two-capability systems each occupy *different* pairs ([ch. 12](12-novelty-assessment.md)). Warning sign: [Claim Plane](https://arxiv.org/abs/2607.21909) (C1) and [Proof-or-Stop](https://arxiv.org/abs/2607.14890) (C4) appeared within *four weeks* of each other in July 2026. Estimated window before an incumbent assembles the combination: **12–24 months** ([ch. 10](10-competitive-landscape.md)).

**2. The pure white space is C3: the evidence-aware scheduler.**
No published system — academic or commercial — jointly and adaptively allocates a fixed total budget across candidate generation, testing, and adversarial challenge for repository-level code ([ch. 3](03-adaptive-verification-scheduling.md)). The nearest precedents each miss a piece: When-To-Solve-When-To-Verify (static ratio, math tasks), ZEBRA (phase-level, pre-execution), AB-MCTS (generation side only), and Stockfish's **Fishtest** — which has admitted code patches via sequential statistical tests (GSPRT) with explicit type-I/II error control since 2013, the best real-world ancestor of the ADMIT decision, but verification-only. This confirms the memo's core research question is unclaimed experimental territory.

**3. The founding memo's factual basis is solid.**
All 13 load-bearing claims (CodeMonkeys 66.2%, AgenticFlict 27.67%, SWE-bench Verified 6.2pp inflation, CodeCRDT's real numbers, jj governance, Mergiraf fallback, Proof-or-Stop, Claim Plane, CoAgent, Fork-Explore-Commit, GitButler agent CLI, SLSA Build L2 scope) were **CONFIRMED against primary sources** by adversarial fact-checkers → [VERIFICATION.md](VERIFICATION.md).

**4. The selection headroom is measured and real.**
Coverage (pass@k) scales log-linearly over four orders of magnitude, but *selection* lags: CodeMonkeys reaches 69.8% coverage yet selects only 57.4% (12.4pp regret), spending just 5.8% of its $2,292 budget on selection ([ch. 2](02-candidate-generation-selection.md)). The pass@k→selection@k gap on SWE-bench is 12–15 points. Closing that gap with scheduled evidence is the prize.

**5. Default oracles are too weak to carry the decision.**
UTBoost found 15.7–28.4% of "resolved" SWE-bench patches wrongly counted; PatchDiff measured 6.2pp resolution-rate inflation; a 2026 probe study found 77% of SWE-bench Verified instances admit a surviving wrong program; OpenAI retired Verified in Feb 2026 ([ch. 8](08-oracles-verification.md), [ch. 9](09-benchmarks-evaluation.md)). Any credible evaluation needs a three-tier oracle (stock tests / strengthened + differential / stratified manual) and budget-matched baselines — the corpus specifies the full protocol with 7 arms and headline metrics **TCAR** (true-correct admission rate) and **FAR** (false-admission rate), neither of which is reported by any existing leaderboard.

**6. The market already voted on where the value is.**
Bare parallel-agent orchestration was commoditized within ~12 months (Terragon shut down, Bloop shut down, Conductor and Sculptor went free). Money concentrates at **admission and verification**: Graphite ($52M B), CodeRabbit ($60M B), Greptile ($25M A), Antithesis ($105M A) ([ch. 10](10-competitive-landscape.md)). Mergify's 2026 merge-queue study: AI-authored PRs already account for 14.4% of merges. The defensible wedge is the **vendor-neutral, evidence-native admission layer** — Sigstore/SLSA-for-agent-changes — sold to platform teams already paying for merge queues and AI reviewers, not another orchestration UI.

**7. Three literatures the memo missed — found by the completeness critic.**
- **AI control** ([ch. 13](13-ai-control-untrusted-generators.md)): Greenblatt et al. (ICML 2024) and Ctrl-Z already allocate a fixed auditing budget over code from *untrusted* models with admit/block/edit decisions and safety–usefulness frontiers. This is the nearest occupied territory to C3 and must be cited and distinguished; it also donates the adversarial threat model (FAR-adv, red-team/blue-team methodology) the memo lacked.
- **Formal methods as oracles** ([ch. 14](14-formal-verification-admission-oracles.md)): the Verus/Dafny verified-codegen wave, diff-time static analysis (Meta Infer's ~70% diff-time fix rate), and proof-carrying code — the archetype of "evidence bound to artifact, checked at admission." Proof artifacts stored in CAS make veracity *machine-checkable* for this oracle class, partially retracting ch. 7's "veracity is out of reach."
- **Post-admission runtime evidence** ([ch. 15](15-runtime-evidence-progressive-delivery.md)): canary analysis (Kayenta, Argo Rollouts, Azure Gandalf) is industry's actual mechanism for catching false admissions. No canary system connects its verdict back to the admission decision; Multiverso's receipt chain (intent → tree → artifact → canary verdict) and a graduated ADMIT with *evidence debt* are the extension.

## Chapters

| # | Chapter | One-line finding |
|---|---|---|
| 1 | [Parallel Agent Exploration & World Isolation](01-parallel-exploration.md) | Warm full-VM forking is solved (<250ms); nobody binds evidence to world state or records isolation tier |
| 2 | [Best-of-N Generation & Candidate Selection](02-candidate-generation-selection.md) | Generate-and-select rules the leaderboards; budget splits are static, patch-level COMPOSE doesn't exist |
| 3 | [Adaptive Compute Allocation & Verification Scheduling](03-adaptive-verification-scheduling.md) | Four literatures supply parts; the joint generate/verify scheduler for repo-level code is unpublished |
| 4 | [VCS Substrate: Jujutsu, GitButler, Patch Theory & Structural Merge](04-vcs-substrate.md) | jj has the primitives but no stable API; use CLI adapters; conflicted worlds stay control-plane-internal |
| 5 | [Speculative Admission, Merge Queues & Evidence Freshness](05-speculative-admission.md) | Merge queues serialize independent changes; none selects among rivals or types evidence freshness |
| 6 | [Concurrent Multi-Agent Coordination & Conflict](06-multi-agent-coordination.md) | Claim Plane + CoAgent cover pre-write admission and repair; the N-candidates × M-intents regime is untouched |
| 7 | [Evidence Ledgers, Provenance & Trust](07-evidence-provenance-trust.md) | Crypto gives integrity+identity, never veracity; no in-toto predicate exists for agent changes — mint one |
| 8 | [Oracles: Mutation, Differential & Adversarial Verification](08-oracles-verification.md) | Human suites are measurably weak; cross-candidate differential testing at repo scale has no prior art |
| 9 | [Benchmarks & Experimental Design](09-benchmarks-evaluation.md) | SWE-bench Verified can't carry the claims; full budget-matched 7-arm protocol with TCAR/FAR specified |
| 10 | [Competitive & Product Landscape](10-competitive-landscape.md) | Nobody ships >2 of the 5 capabilities; adaptive verification budget is pure white space; window 12–24 mo |
| 11 | [Knowledge Plane: Code Intelligence for Impact & Invalidation](11-knowledge-plane.md) | MVP baseline (build graph + coverage + SCIP) suffices; Glean-class store pays off only at scheduler-inner-loop scale |
| 12 | [Novelty Assessment & Academic Positioning](12-novelty-assessment.md) | Every capability occupied singly, conjunction empty; strongest claim is the scheduler measured by FAR |
| 13 | [AI Control: Budgeted Verification Against Untrusted Generators](13-ai-control-untrusted-generators.md) | Control protocols are the nearest prior art to C3; donates FAR-adv and the subversion threat model |
| 14 | [Formal Methods as Admission Oracles](14-formal-verification-admission-oracles.md) | Proof-carrying receipts make veracity machine-checkable; extends the oracle ladder with formal rungs |
| 15 | [Post-Admission Runtime Evidence: Canary & Progressive Delivery](15-runtime-evidence-progressive-delivery.md) | Canary verdicts never flow back to admission decisions; graduated ADMIT with evidence debt proposed |

Supporting documents: **[VERIFICATION.md](VERIFICATION.md)** (adversarial fact-check of the founding memo) · **[BIBLIOGRAPHY.md](BIBLIOGRAPHY.md)** (240 deduplicated primary sources).

## What this changes for the roadmap

The corpus converges on concrete design deltas to the founding memo:

1. **Frame the paper around the scheduler + FAR, not the architecture.** The architecture is an integration claim (fragile, closing fast); the measured scheduling result under a fixed budget is the durable contribution ([ch. 12](12-novelty-assessment.md)).
2. **Adopt the AI-control threat model now.** The generator must be modeled as potentially adversarial (prompt-injected, reward-hacking); report FAR-adv alongside FAR; keep protocol internals hidden from generators ([ch. 13](13-ai-control-untrusted-generators.md)).
3. **Type evidence by re-checkability (V0 testimonial → V3 foundational proof) and by invalidation basis** (sound-by-construction / sound-by-dependency / probabilistic). Record isolation tier in every receipt ([ch. 14](14-formal-verification-admission-oracles.md), [ch. 5](05-speculative-admission.md), [ch. 1](01-parallel-exploration.md)).
4. **Discount correlated evidence in the allocation loop** — ten tests from one model are not ten observations; correlation-aware value-of-information appears nowhere and is a publishable component on its own ([ch. 3](03-adaptive-verification-scheduling.md), [ch. 8](08-oracles-verification.md)).
5. **Sequential statistical admission control** (SPRT/e-values à la Fishtest) over correlated LLM evidence is the missing statistical machinery for ADMIT ([ch. 3](03-adaptive-verification-scheduling.md)).
6. **Cross-candidate differential testing at repo scale** (shared input corpora executed across N worlds, behavior diffed) has zero prior art and is a cheap, high-signal oracle unique to the N-worlds design ([ch. 8](08-oracles-verification.md)).
7. **Graduated admission**: ADMIT(canary) → ADMIT(fleet) with explicit evidence debt, and REVERT as a first-class automated decision ([ch. 15](15-runtime-evidence-progressive-delivery.md)).

## Method & provenance

Generated 2026-08-12 by a 19-agent research workflow (Claude Fable 5): 12 parallel dimension researchers with mandatory primary-source fetching, 3 adversarial fact-checkers over the memo's load-bearing claims, 1 completeness critic, 3 gap-filler researchers. ~1.47M tokens, 719 tool calls, ~38 minutes wall-clock. Every chapter carries inline citations; unverifiable statements are marked. Errors that survived this process are ours — [open an issue](https://github.com/coagente/multiverso/issues).
