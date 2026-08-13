# Verification of the Founding Memo's Claims

> Adversarial fact-check of the load-bearing claims in [`../RESEARCH.md`](../RESEARCH.md), performed 2026-08-12 by three independent fact-checking agents instructed to hunt primary sources and refute. **Result: 13/13 CONFIRMED.**

| # | Claim (as made in the memo) | Verdict | Primary source |
|---|---|---|---|
| 1 | CodeMonkeys' "Barrel of Monkeys" ensemble scores 66.2% on SWE-bench Verified, beating its best member | ✅ CONFIRMED | [arXiv:2501.14723](https://arxiv.org/abs/2501.14723) |
| 2 | Repeated sampling scales coverage (pass@k) predictably (Large Language Monkeys) | ✅ CONFIRMED | [arXiv:2407.21787](https://arxiv.org/abs/2407.21787) |
| 3 | SWE-bench Verified resolution rates are inflated ~6.2pp by patches that don't satisfy expected behavior | ✅ CONFIRMED | [arXiv:2503.15223](https://arxiv.org/abs/2503.15223) |
| 4 | AgenticFlict: 27.67% textual conflict rate in simulable agent PRs; textual-only by admission | ✅ CONFIRMED | [arXiv:2604.03551](https://arxiv.org/abs/2604.03551) |
| 5 | CodeCRDT reports 100% structural convergence with 5–10% semantic conflicts (NOT "~80% of complex conflicts") | ✅ CONFIRMED | [arXiv:2510.18893](https://arxiv.org/abs/2510.18893) |
| 6 | Claim Plane admits versioned ChangeIntents (base, resources, dependencies, scope) *before* writes | ✅ CONFIRMED | [arXiv:2607.21909](https://arxiv.org/abs/2607.21909) |
| 7 | CoAgent treats multi-agent coordination as concurrency control with invalidation notifications and selective repair | ✅ CONFIRMED | [arXiv:2606.15376](https://arxiv.org/abs/2606.15376) |
| 8 | Fork-Explore-Commit: isolated fs+process branch contexts, CoW, commit/abort, first-commit-wins | ✅ CONFIRMED | [arXiv:2602.08199](https://arxiv.org/abs/2602.08199) |
| 9 | Jujutsu: formal governance, elected maintainers, single-company cap; still experimental; jj-lib unstable; bookmarks/metadata live outside Git | ✅ CONFIRMED | [jj governance](https://docs.jj-vcs.dev/latest/governance/GOVERNANCE/), [README](https://github.com/jj-vcs/jj) |
| 10 | Mergiraf falls back to line-based merge on parse errors and may retain unresolved conflicts | ✅ CONFIRMED | [mergiraf.org/architecture](https://mergiraf.org/architecture.html) |
| 11 | GitButler links agent sessions to parallel branches and ships an agent-oriented CLI (`but`) | ✅ CONFIRMED | [GitButler docs](https://docs.gitbutler.com/ai-agents/parallel-agents) |
| 12 | GitHub Artifact Attestations give SLSA v1.0 Build L2 (L3 with reusable workflows), not agent-authorship provenance | ✅ CONFIRMED | [GitHub docs](https://docs.github.com/en/actions/concepts/security/artifact-attestations) |
| 13 | Proof-or-Stop: lifecycle gates on fresh, source-state-bound evidence; agent outputs are claims, not state | ✅ CONFIRMED | [arXiv:2607.14890](https://arxiv.org/abs/2607.14890) |

## Notable exact findings

**Claim 1 (CodeMonkeys).** The 66.2% is the *selection mechanism applied over a mixed ensemble* (CodeMonkeys' edits + top-4 leaderboard submissions), beating the best member alone (Blackbox AI Agent, 62.8%). CodeMonkeys standalone resolves 57.4% at ~$2,300 total budget.

**Claim 3 (SWE-bench inflation).** "Are 'Solved Issues' in SWE-bench Really Solved Correctly?" (Wang, Pradel, Liu; ICSE 2026) via PatchDiff: 29.6% of plausible patches behave differently from ground truth; 7.8% of all patches pass SWE-bench's tests but fail the developer suite; reported rates inflated by 6.2 absolute points.

**Claim 5 (CodeCRDT).** The abstract reports "100% convergence with zero merge failures" alongside "semantic conflict rates (5–10%)". No statement resembling "resolves ~80% of complex conflicts" appears anywhere — the memo was right to order that phrasing removed.

**Claim 9 (Jujutsu).** Governance: "Maintainers are elected by a voting process"; "At most 1/3 of the maintainers may be paid for their contributions by a single company." README: "Jujutsu is an **experimental version control system**"; jj-lib is at 0.44.0, pre-1.0, no semver guarantee. The memo's correction of the outdated "hobby project" caveat is warranted.

**Claim 13 (Proof-or-Stop).** Reported results: 10/10 unattended scenarios with zero false-DONE declarations; 18 tamper classes rejected with zero false accepts; hidden-fail amplification cut from 31 to 2 per 1,800 injected cells. Scope limits: single lifecycle, single host, no protection against compromised runners or semantic incorrectness — exactly the space above which Multiverso sits.

## Method

Each fact-checker was instructed to *refute*: locate the primary source (arXiv page/PDF, official docs, repository), quote the exact wording and numbers, and return CONFIRMED / PARTIALLY_CONFIRMED / REFUTED / UNVERIFIABLE with no invented URLs. Full verdict text with quotes is preserved in the workflow journal.
