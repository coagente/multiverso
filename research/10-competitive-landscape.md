# 10. Competitive & Product Landscape

> Part of the Multiverso research corpus - https://github.com/coagente/multiverso - Cutoff: 2026-08-12

## Why this matters for Multiverso

Multiverso claims a specific, unoccupied position: an evidence-native, Git-compatible **control plane** that combines (1) versioned intents, (2) parallel alternative candidates, (3) an evidence ledger cryptographically bound to exact world state, (4) an adaptive verification budget, and (5) policy-gated admission to trunk. Whether that position is actually unoccupied is an empirical question about a market that is moving extremely fast: between mid-2025 and mid-2026 the agent-coding market consolidated violently (OpenAI's Windsurf deal collapsed and Cognition took it; SpaceX agreed to buy Cursor's parent for $60B; Terragon and Bloop shut down), while every layer Multiverso touches — orchestration, review, merge queues, evals — got funded. This chapter maps who occupies which layer as of 2026-08-12, scores each player against the five capabilities, and answers the key question: **does anyone ship the combination?** (Spoiler: no — but three players could assemble most of it within a year.)

## State of the art

### (a) Agent coding products: the generation layer

**Cognition (Devin + Windsurf).** After OpenAI's ~$3B agreement to buy Windsurf collapsed in July 2025 over Microsoft-partnership complications, Cognition acquired Windsurf; Devin's ARR scaled from ~$1M (Sept 2024) to ~$73M (June 2025), and Sacra estimates ~$492M combined ARR by May 2026 ([Sacra](https://sacra.com/c/cognition/)). Cognition raised at a $10.2B valuation in September 2025, then $1B+ at ~$26B in May 2026 ([Sacra](https://sacra.com/c/cognition/), [TechFundingNews](https://techfundingnews.com/cognition-ai-25b-valuation-funding-talks-devin-software-engineer/)). Devin 2.0 (April 3, 2025) cut entry pricing from $500 to $20/month with ACU-metered usage, and introduced **parallel Devins** — "spin up multiple parallel Devins, each equipped with its own interactive, cloud-based IDE" — plus interactive planning sessions before autonomous execution ([Cognition blog](https://cognition.com/blog/devin-2), [VentureBeat](https://venturebeat.com/programming-development/devin-2-0-is-here-cognition-slashes-price-of-ai-software-engineer-to-20-per-month-from-500)). Parallel Devins parallelize across *tasks*, not alternative candidates for the same intent; the announcement says nothing about selecting among multiple candidate solutions ([Cognition blog](https://cognition.com/blog/devin-2)).

**Cursor (Anysphere).** Cursor 2.0 (October 29, 2025) shipped the Composer model and a multi-agent interface running agents in parallel via git worktrees or remote machines — and stated explicitly that "having multiple models attempt the same problem and picking the best result significantly improves the final output, especially for harder tasks," alongside a native browser tool so agents test their own work and an improved review surface for agent diffs ([Cursor blog](https://cursor.com/blog/2-0)). This is the most mainstream shipped instance of **parallel alternative candidates** — but selection is manual (human picks), with no evidence ledger and no admission policy. Anysphere raised a $2.3B Series D at $29.3B (Nov 2025) ([Value Add VC](https://valueaddvc.com/blog/cursor-ai-valuation-how-a-code-editor-became-a-9b-company)); on June 16, 2026, SpaceX agreed to acquire Anysphere for $60B in stock, expected to close Q3 2026 ([CNBC](https://www.cnbc.com/2026/06/16/spacex-spcx-cursor-acquisition-ipo.html), [Forbes](https://www.forbes.com/sites/sandycarter/2026/06/16/spacex-buys-cursor-in-largest-startup-acquisition-ever-at-60-billion/)). Background/cloud agents run in isolated VMs producing merge-ready PRs with logs (reported up to 8 concurrent) ([CloudZero](https://www.cloudzero.com/blog/claude-code-agents/), [Taskade](https://www.taskade.com/blog/anysphere-cursor-history) — secondary sources).

**OpenAI Codex.** Codex CLI (April 16, 2025) and Codex Cloud research preview (May 16, 2025) run each task in a separate cloud sandbox preloaded with the repo, returning diffs plus **terminal logs and test results for inspection** — a primitive evidence receipt, though not cryptographically bound to state ([Wikipedia: OpenAI Codex (AI agent)](https://en.wikipedia.org/wiki/OpenAI_Codex_(AI_agent)), [Codegen review](https://codegen.com/ai-tools/openai-codex/)). By March 2026 Codex passed 2M weekly active users; OpenAI merged Codex into the ChatGPT desktop app (July 2026) and shipped Codex Security (March 2026) ([Wikipedia](https://en.wikipedia.org/wiki/OpenAI_Codex_(AI_agent))). Teams queue 3–5 parallel sandboxed tasks; AGENTS.md declares which test commands to run; pricing is bundled into ChatGPT tiers plus token billing ([Codegen review](https://codegen.com/ai-tools/openai-codex/)).

**Google Jules.** Launched via Google Labs (Dec 2024), GA at Google I/O in May 2026; clones the repo into a cloud VM, **shows an editable plan before executing**, and opens a PR. Pricing (reported): free tier 15 tasks/day, Google AI Pro $19.99/mo (100 tasks/day, 15 concurrent), AI Ultra $124.99/mo (300/day, 60 concurrent) ([Google blog](https://blog.google/innovation-and-ai/models-and-research/google-labs/jules/), [HackUp pricing tracker](https://hackup.ai/ai-plans/jules/) — secondary). The editable plan is the closest any hyperscaler product comes to a first-class intent object, but it is ephemeral, not versioned.

**Anthropic Claude Code.** Subagents (isolated context windows), hooks, and — with Claude Opus 4.6, February 5, 2026 — **agent teams** in research preview: "multiple agents that work in parallel as a team and coordinate autonomously," with users able to take over any subagent ([Anthropic](https://www.anthropic.com/news/claude-opus-4-6)). Later 2026 releases reportedly added Dynamic Workflows (hundreds of parallel subagents per session) and fleet guardrails (concurrency caps, nesting depth limits) ([CloudZero](https://www.cloudzero.com/blog/claude-code-agents/), [Digital Applied](https://www.digitalapplied.com/blog/claude-code-subagent-depth-limits-budget-caps-2026) — secondary). Anthropic also shipped agent-based code review inside Claude Code ([InfoQ](https://www.infoq.com/news/2026/04/claude-code-review/)). Claude Code is a *harness*, not a control plane: no intent versioning, no evidence ledger, no admission gate beyond whatever Git/CI the user has.

**GitHub Copilot / Agent HQ.** Announced at GitHub Universe (October 2025): an open platform to orchestrate "any agent, any way you work," with **mission control** — a single command center to assign, steer, and track many agents in parallel across GitHub, VS Code, mobile, and CLI — plus Plan Mode and enterprise governance; agents from Anthropic, OpenAI, Google, Cognition, and xAI ship inside the Copilot subscription ([GitHub blog](https://github.blog/news-insights/company-news/welcome-home-agents/), [Visual Studio Magazine](https://visualstudiomagazine.com/articles/2025/10/28/github-introduces-agent-hq-to-orchestrate-any-agent-any-way-you-work.aspx), [GitHub blog: mission control](https://github.blog/ai-and-ml/github-copilot/how-to-orchestrate-agents-using-mission-control/)). Combined with GitHub's native merge queue, rulesets, and required checks, GitHub is the only player that owns both fleet orchestration *and* the admission gate — though neither is evidence-native.

**Factory.** Enterprise "Droids" that own workflows end-to-end (code, tests, review, docs); $150M Series C at $1.5B led by Khosla (April 16, 2026), ~$220M total raised; customers include Nvidia, MongoDB, Morgan Stanley ([Enterprise DNA](https://enterprisedna.co/resources/news/factory-ai-series-c-enterprise-coding-agents-2026/), [byteiota](https://byteiota.com/factory-ai-150m-funding-autonomous-coding-agents/) — secondary).

**OpenHands (All Hands AI).** MIT-licensed, model-agnostic agent platform (formerly OpenDevin, ~64K GitHub stars); $18.8M Series A led by Madrona (Nov 18, 2025; $23.8M total) to sell an enterprise cloud that "scales from a single agent to thousands" with "full visibility and governance across every run" ([BusinessWire](https://www.businesswire.com/news/home/20251118768131/en/OpenHands-Raises-$18.8M-Series-A-to-Bring-Open-Source-Cloud-Coding-Agents-to-Enterprises)). Governance telemetry, but no candidate comparison or evidence binding.

**Ona (ex-Gitpod).** Rebranded September 2025 from cloud dev environments to "mission control for your personal team of software engineering agents," sandboxed agents plus enterprise guardrails ([Ona](https://ona.com/stories/gitpod-is-now-ona), [The Register](https://www.theregister.com/software/2025/09/03/gitpod-rebrands-as-ona-now-an-ai-driven-dev-platform/295031)).

**The long tail.** Sweep pivoted to a JetBrains-native assistant ([sweep.dev](https://sweep.dev/)); Cosine's Genie sells ticket-to-PR agents with seat pricing from free to $200/seat ([Cosine/YC](https://www.ycombinator.com/companies/cosine), [recatools](https://recatools.com/ai-directory/cosine-genie/) — secondary); Magic remains a research lab ($515M raised, 100M-token context research, no shipped product) ([magic.dev](https://magic.dev)).

### (b) Parallel-candidate orchestrators: the layer that almost died

This is Multiverso's nearest-neighbor category — and its 2026 story is cautionary.

- **Sculptor (Imbue)**: desktop app running parallel coding agents in isolated containers (full repo copy each), with Pairing Mode syncing container state to the local IDE, merge-conflict routing back to agents, and a beta "Suggestions" reviewer; re-launched September 26, 2025 on Claude Code; free in beta, BYO Anthropic key ([Imbue blog](https://imbue.com/blog/sculptor-announce)).
- **Conductor (Melty Labs)**: free Mac app spawning parallel Claude Code/Codex agents in isolated git worktrees with a review-and-merge UI; used at Linear, Vercel, Notion, Stripe ([conductor overview](https://www.welcome.ai/company/conductor), [madewithlove review](https://madewithlove.com/blog/conductor-running-multiple-ai-coding-agents-in-parallel/) — secondary).
- **Vibe Kanban (Bloop)**: open-source kanban for orchestrating Claude Code/Gemini/Codex/Amp agents in parallel worktrees; Bloop announced shutdown April 10, 2026, with Vibe Kanban continuing as community-maintained Apache-2.0 ([vibekanban.com](https://www.vibekanban.com/), [Nimbalyst post-mortem](https://nimbalyst.com/blog/vibe-kanban-after-bloop-whats-next/)).
- **Terragon**: cloud background-agent orchestrator (Claude Code, Codex, Amp, Gemini; sandbox isolation; branch-per-task; auto PRs; `terry` CLI handoff) — **shut down January 16, 2026** for lack of traction, open-sourcing a snapshot ([terragon-oss on GitHub](https://github.com/terragon-labs/terragon-oss)).
- Others: Crystal, Nimbalyst (open-source multi-agent workspaces) ([Nimbalyst guide](https://nimbalyst.com/blog/best-agent-management-tools-2026/) — secondary).

Two adjacent products push past "run N, human picks": **Cursor 2.0's multi-model same-problem race** (above) and **Augment Code's Intent** (public beta February 26, 2026) — a spec-driven workspace where a *living specification* stays current as coordinator/specialist agents execute in isolated worktrees and a **Verifier Agent checks results against the spec before review** ([Open Orchestrators](https://openorchestrators.org/news/augment-code-intent-launch/)). Intent is the closest shipped analog to *versioned intents*; it does not do alternative candidates, evidence binding, or budget adaptation.

The pattern: pure orchestration UIs are free or dead. Terragon and Bloop could not monetize; Conductor and Sculptor are free BYO-subscription apps. Orchestration alone is a feature, not a business — the value pooled either below (model/agent vendors) or after (review/merge).

### (c) Agent-native VCS and review layer

- **GitButler**: Scott Chacon's agentic-era VCS — parallel virtual branches ("logical isolation similar to worktrees without requiring full repository copies"), agent-specific commands, rich metadata; a16z led the Series A (announced April 8, 2026; ~$17M reported, with one outlet reporting $22M — amounts conflict) ([a16z](https://a16z.com/announcement/investing-in-gitbutler/), [MLQ](https://mlq.ai/news/gitbutler-lands-17m-series-a-to-advance-version-control-for-ai-driven-development/), [StartupHub](https://www.startuphub.ai/ai-news/investors-news/2026/gitbutler-raises-22m-series-a)). Thesis matches Multiverso's data plane; no evidence or admission semantics.
- **Graphite**: $52M Series B led by Accel (March 17, 2025) launching Diamond (AI reviewer), later folded into "Graphite Agent" with Team plan at $40/user including unlimited AI review, stacked PRs, and a stack-aware **merge queue**; positions itself as "the canonical layer where humans and agents collaborate on code," reviewing tens of thousands of PRs weekly ([Graphite blog](https://graphite.com/blog/series-b-diamond-launch), [Graphite pricing post](https://graphite.com/blog/introducing-graphite-agent-and-pricing)). Review + queue under one roof = the most admission-shaped startup.
- **Greptile**: $25M Series A led by Benchmark (Sept 23, 2025); v3 (Nov 2025) through **v5 (Aug 5, 2026)**; shipped **TREX (June 2026), an execution layer that runs code to catch bugs static analysis misses** — i.e., dynamic evidence generation inside review — and argues "Software Needs an Independent Auditor": generation and verification must be separated ([Greptile blog](https://www.greptile.com/blog)). ~$30/user/mo ([Greptile vs CodeRabbit](https://www.greptile.com/greptile-vs-coderabbit)).
- **CodeRabbit**: $60M Series B led by Scale VP at ~$550M (Sept 16, 2025); 8,000+ paying customers; explicitly frames its product as "**quality gates** for AI coding" ([BusinessWire](https://www.businesswire.com/news/home/20250916401011/en/CodeRabbit-Raises-$60M-Series-B-Following-Unprecedented-Growth-as-Vibe-Coding-Triggers-a-Need-for-New-Code-Quality-Standards), [CodeRabbit blog](https://www.coderabbit.ai/blog/coderabbit-series-b-60-million-quality-gates-for-code-reviews)). ~$24/user/mo ([tech-insider comparison](https://tech-insider.org/au/coderabbit-vs-greptile-vs-copilot-2026/) — secondary).
- **Baz**: founded by Bridgecrew's Guy Eisenkot; extended seed to $17M co-led by Battery and boldstart (June 29, 2026), launching **Planner** — moving review upstream to the planning stage to "eliminate entire classes of bugs at superhuman scale" ([GlobeNewswire](https://www.globenewswire.com/news-release/2026/06/29/3319144/0/en/Agentic-Coding-Company-Baz-Announces-Planner-That-Lets-Engineering-Teams-Eliminate-Entire-Classes-of-Bugs-and-Vulnerabilities-at-Superhuman-Scale-and-an-Extended-Funding-Round-Co-L.html), [SiliconANGLE](https://siliconangle.com/2026/06/29/exclusive-agentic-coding-startup-baz-brings-code-reviews-planning-stage-extends-seed-funding-17m/)).
- **cubic** (YC X25): AI review for complex codebases, ~$30–40/dev/mo; ranked #1 on Martian's independent Code Review Bench at 61.8% F1 ([cubic.dev](https://www.cubic.dev/), [StartupHub](https://www.startuphub.ai/startups/cubic) — secondary).
- **Pierre**: collaborative code review rethink from ex-Twitter founders; $23.5M raised (CRV et al.) ([PitchBook](https://pitchbook.com/profiles/company/523179-82), [YC launch](https://www.ycombinator.com/launches/I6t-pierre-a-new-way-to-review-code)).

### (d) CI / merge admission infrastructure

**Mergify's State of Merge Queues 2026** (200K+ PRs, 477 orgs, through July 2026) is the best public dataset on agent-era admission: ~14.4% of private merges show AI-assistance markers ("a floor"); AI-assisted PRs broke main *half* as often as human PRs (1.9% vs 4.4%) despite being larger (137 vs 84 lines); break-main rates scale from 0.77% (2–5 devs) to 12.5% (40+ devs); 94% of merges are still processed one PR at a time ([Mergify report](https://mergify.com/reports/state-of-merge-queues-2026)). **Aviator** sells parallel queues, predictive batching, flaky-test quarantine, and priority lanes for teams shipping 1,000+ PRs/day (Notion, Figma) ([Aviator](https://www.aviator.co/merge-queue)); **Trunk.io** infers parallel merge lanes from impacted targets and auto-detects flakes by comparing failures across queue positions ([Trunk](https://trunk.io/merge-queue), [Trunk vs GitHub MQ](https://trunk.io/trunk-vs-github-merge-queue)); GitHub's native merge queue is the free default ([Tenki guide](https://tenki.cloud/blog/github-merge-queue-setup) — secondary). These systems *serialize and gate* admission — capability (5) in commodity form — and their flake handling is a primitive statistical-evidence engine. None knows anything about intents, candidates, or per-change evidence sufficiency; queue policies are static YAML, not adaptive.

### (e) Evidence: eval and testing layer

- **Braintrust / LangSmith**: eval-first vs trace-first LLM-app observability; Braintrust Pro $249/mo metering *scores*, LangSmith Plus $39/seat metering *traces* ([morphllm comparison](https://www.morphllm.com/comparisons/braintrust-vs-langsmith) — secondary). They keep evidence ledgers — but about LLM apps, keyed to app traces, not bound to repo snapshots or admission decisions.
- **QA Wolf**: hybrid AI+human "Coverage-as-a-Service" (Mapping AI, Automation AI, parallel run infra), now explicitly positioned for the "Agentic SDLC" — keeping deployment velocity when agents write the code — with pre-merge CI validation ([qawolf.com](https://www.qawolf.com)); ~$57M raised (most-funded AI-QA company) ([AgentMarketCap](https://agentmarketcap.ai/blog/2026/04/08/momentic-autonomous-qa-agent-testing-market-2026) — secondary).
- **Momentic**: AI-native E2E/API testing with autonomous exploratory test generation; $15M Series A led by Standard Capital (Nov 2025), ~$19.2M total ([QA Financial](https://qa-financial.com/momentic-capital-injection-underscores-investors-are-embracing-ai-native-testing/), [AgentMarketCap](https://agentmarketcap.ai/blog/2026/04/08/momentic-autonomous-qa-agent-testing-market-2026) — secondary).
- **Antithesis**: deterministic simulation testing — massively parallel fault-injecting exploration with *perfect reproduction* of any failure; $105M Series A led by Jane Street (Dec 2025) ([PR Newswire](https://www.prnewswire.com/news-releases/jane-street-leads-antithesiss-105m-series-a-to-make-deterministic-simulation-testing-the-new-standard-302631076.html), [antithesis.com](https://antithesis.com/)). Deterministic replay is the strongest oracle technology on the market and the closest thing to Multiverso's "receipts" ideal — but it is a testing destination, not a per-change evidence ledger wired to admission.

Over $1.5B of venture capital has flowed into AI testing platforms across 40+ startups, explicitly because agent-generated code made verification the bottleneck ([AgentMarketCap](https://agentmarketcap.ai/blog/2026/04/08/momentic-autonomous-qa-agent-testing-market-2026) — secondary).

### (f) Strategic analysis: product or feature?

Three lessons from 2025–2026:

1. **Bare orchestration is a feature.** Terragon (dead, Jan 2026), Bloop/Vibe Kanban (dead company, community project, Apr 2026), Conductor and Sculptor (free, BYO subscription). The "run N agents in worktrees" UI was commoditized within 12 months by Cursor 2.0/3.0, Claude Code agent teams, Codex parallel tasks, and GitHub mission control — each agent vendor absorbed it ([Cursor](https://cursor.com/blog/2-0), [Anthropic](https://www.anthropic.com/news/claude-opus-4-6), [GitHub](https://github.blog/news-insights/company-news/welcome-home-agents/)).
2. **Admission and verification monetize.** Graphite ($81M total), CodeRabbit ($88M, ~$550M valuation), Greptile ($30M), Aviator/Mergify/Trunk, QA Wolf/Momentic/Antithesis all sell *after-generation* trust. Mergify's data gives the macro driver: agent PR volume up, one-at-a-time merging still at 94%, break rates exploding with team size ([Mergify report](https://mergify.com/reports/state-of-merge-queues-2026)).
3. **Incumbent roadmaps converge on governance, not evidence.** GitHub Agent HQ's pitch is multi-vendor agents "wrapped in enterprise-grade governance" ([Eficode](https://www.eficode.com/blog/why-github-agent-hq-matters-for-engineering-teams-in-2026)); OpenHands sells "visibility and governance across every run" ([BusinessWire](https://www.businesswire.com/news/home/20251118768131/en/OpenHands-Raises-$18.8M-Series-A-to-Bring-Open-Source-Cloud-Coding-Agents-to-Enterprises)); Factory sells enterprise workflow ownership. Governance = who may run what; nobody sells *proof of what was verified, bound to what state, under what policy*.

**Who buys a control plane?** The realistic buyer is the platform-engineering team of a 50–500-engineer org that already pays for a merge queue plus one or two AI reviewers and is watching agent PR volume climb — the same budget line that bought Graphite/Aviator/CodeRabbit. A second buyer class is AI-enablement teams standardizing agent fleets across vendors (the Agent HQ audience) who need vendor-neutral audit. **Acquisition dynamics:** endpoint owners (GitHub/Microsoft, Cursor/SpaceX, Cognition, Anthropic, OpenAI) absorb orchestration features for free; admission-layer startups (Graphite, CodeRabbit, Aviator) are the natural acquirers-or-competitors for an evidence layer; the neutral-attestation position (analogous to Sigstore/SLSA/in-toto in supply chain — [slsa.dev](https://slsa.dev), [in-toto.io](https://in-toto.io)) is defensible precisely because no single agent vendor can credibly attest to its own agents.

## Comparison table

Rubric: **I** = versioned intents; **C** = parallel *alternative* candidates for one intent; **E** = evidence ledger bound to exact state; **B** = adaptive verification budget; **A** = policy-gated admission to trunk. ✔ shipped, ◐ partial/adjacent, ✗ absent.

| System (layer) | I | C | E | B | A | Notes |
|---|---|---|---|---|---|---|
| Cognition Devin/Windsurf | ◐ | ◐ | ✗ | ✗ | ✗ | Planning sessions ephemeral; parallel Devins = parallel tasks, not candidates ([Cognition](https://cognition.com/blog/devin-2)) |
| Cursor 2.x/3.x | ✗ | ✔ | ✗ | ✗ | ✗ | Multi-model same-problem race, human picks; browser self-testing ([Cursor](https://cursor.com/blog/2-0)) |
| OpenAI Codex | ✗ | ◐ | ◐ | ✗ | ✗ | Parallel sandboxes; terminal logs/test receipts per task, unbound ([Wikipedia](https://en.wikipedia.org/wiki/OpenAI_Codex_(AI_agent))) |
| Google Jules | ◐ | ✗ | ✗ | ✗ | ✗ | Editable pre-execution plan; VM per task ([Google](https://blog.google/innovation-and-ai/models-and-research/google-labs/jules/)) |
| Claude Code (teams/subagents) | ✗ | ◐ | ✗ | ✗ | ✗ | Parallel teams for decomposition; hooks ≠ admission policy ([Anthropic](https://www.anthropic.com/news/claude-opus-4-6)) |
| GitHub Agent HQ + merge queue | ◐ | ◐ | ✗ | ✗ | ✔ | Plan Mode + mission control + rulesets/required checks ([GitHub](https://github.blog/news-insights/company-news/welcome-home-agents/)) |
| Factory Droids | ◐ | ✗ | ✗ | ✗ | ✗ | Enterprise workflow ownership ([Enterprise DNA](https://enterprisedna.co/resources/news/factory-ai-series-c-enterprise-coding-agents-2026/)) |
| OpenHands cloud | ✗ | ✗ | ◐ | ✗ | ✗ | Run-level visibility/governance telemetry ([BusinessWire](https://www.businesswire.com/news/home/20251118768131/en/OpenHands-Raises-$18.8M-Series-A-to-Bring-Open-Source-Cloud-Coding-Agents-to-Enterprises)) |
| Sculptor / Conductor / Vibe Kanban | ✗ | ✔ | ✗ | ✗ | ✗ | Candidate UIs, human selection, no evidence semantics ([Imbue](https://imbue.com/blog/sculptor-announce)) |
| Augment Intent | ✔ | ✗ | ✗ | ✗ | ✗ | Living spec + Verifier Agent vs spec ([Open Orchestrators](https://openorchestrators.org/news/augment-code-intent-launch/)) |
| GitButler | ✗ | ◐ | ✗ | ✗ | ✗ | Parallel virtual branches; data-plane only ([a16z](https://a16z.com/announcement/investing-in-gitbutler/)) |
| Graphite (Agent + queue) | ✗ | ✗ | ◐ | ✗ | ✔ | AI review + stack-aware merge queue = admission-shaped ([Graphite](https://graphite.com/blog/series-b-diamond-launch)) |
| Greptile (v5 + TREX) | ✗ | ✗ | ◐ | ✗ | ◐ | Executes code during review; independence thesis ([Greptile](https://www.greptile.com/blog)) |
| CodeRabbit | ✗ | ✗ | ◐ | ✗ | ◐ | "Quality gates for AI coding" ([CodeRabbit](https://www.coderabbit.ai/blog/coderabbit-series-b-60-million-quality-gates-for-code-reviews)) |
| Baz Planner | ◐ | ✗ | ✗ | ✗ | ◐ | Review moved to planning stage ([SiliconANGLE](https://siliconangle.com/2026/06/29/exclusive-agentic-coding-startup-baz-brings-code-reviews-planning-stage-extends-seed-funding-17m/)) |
| Mergify / Aviator / Trunk / GH MQ | ✗ | ✗ | ◐ | ✗ | ✔ | Serialize admission; statistical flake evidence; static policies ([Mergify](https://mergify.com/reports/state-of-merge-queues-2026), [Aviator](https://www.aviator.co/merge-queue)) |
| Braintrust / LangSmith | ✗ | ✗ | ◐ | ✗ | ✗ | Eval/trace ledgers for LLM apps, not repo-state-bound ([morphllm](https://www.morphllm.com/comparisons/braintrust-vs-langsmith)) |
| QA Wolf / Momentic | ✗ | ✗ | ◐ | ✗ | ◐ | Test evidence into CI gates; no candidate/intent model ([QA Wolf](https://www.qawolf.com)) |
| Antithesis | ✗ | ✗ | ✔* | ✗ | ✗ | *Deterministic replay = strongest receipts; not per-change ledger ([Antithesis](https://antithesis.com/)) |
| **Multiverso (proposed)** | ✔ | ✔ | ✔ | ✔ | ✔ | The combination is the product |

**Verdict on the key question: CONFIRMED.** As of 2026-08-12, no shipped product combines even three of the five capabilities coherently, and **no product at all does (4) adaptive verification budget** — evidence-aware compute allocation across generation/testing/challenge is complete white space. The closest players and their assembly speed:

1. **GitHub/Microsoft** — already owns A (rulesets, required checks, merge queue), fleet orchestration (Agent HQ mission control), and a provenance primitive (artifact attestations for builds). Adding candidate comparison and a per-PR evidence ledger is roadmap-plausible in 6–12 months; adaptive budgeting is culturally furthest from them. Biggest threat by position, slowest by evidence-native DNA.
2. **Cursor (SpaceX)** — already ships C (multi-model races) plus agent self-verification via browser tool; enormous distribution. But it is vendor-locked to its own agents, has no admission surface, and the SpaceX integration creates 12+ months of strategic noise ([CNBC](https://www.cnbc.com/2026/06/16/spacex-spcx-cursor-acquisition-ipo.html)).
3. **Graphite (or Greptile + a merge queue)** — owns the admission path and is adding dynamic evidence (Greptile TREX literally executes code during review). A Graphite×Greptile-shaped product that scheduled candidates upstream of its queue would be Multiverso's most direct competitor; neither currently touches candidate generation or intent versioning.

Honorable mention: **Augment Intent** is alone in shipping versioned-intent semantics (living spec + verifier) and could add candidates fastest conceptually, but lacks distribution and any admission/evidence infrastructure.

## Implications for Multiverso design

1. **Do not build the orchestration UI as the wedge.** The market killed that product twice (Terragon, Bloop) and commoditized it into every agent vendor. Multiverso's UI story should be "works with your existing harness" (Claude Code, Codex, OpenHands) — the control plane consumes candidates, it doesn't compete to generate them.
2. **The admission gate is where money already flows.** Position Multiverso as the evidence-native upgrade to the merge-queue/AI-review budget line (Graphite $40/user, CodeRabbit $24, Greptile $30, Aviator/Mergify enterprise). Mergify's own data — 94% single-PR merging, 12.5% break rates at 40+ devs, agent PR volume doubling — is the sales deck ([Mergify](https://mergify.com/reports/state-of-merge-queues-2026)).
3. **Vendor neutrality is the defensible position.** GitHub will attest to GitHub agents; Cursor to Cursor. A Sigstore/SLSA-analogous neutral attestation layer for *code change admission* has no incumbent, and enterprise buyers running multi-vendor fleets (the explicit Agent HQ scenario) need exactly that.
4. **The adaptive budget scheduler is the only capability with zero shipped competition** — it is also the research contribution. Ship capabilities 1/2/3/5 as compatible infrastructure; ship 4 as the differentiating result. Benchmark against Cursor's manual best-of-N and fixed-N baselines, which now exist in production as comparators.
5. **Borrow the strongest oracle tech rather than rebuilding it.** Antithesis-style deterministic replay and QA Wolf/Momentic-style generated E2E suites are evidence *suppliers*; Multiverso's ledger should define the receipt format they plug into.
6. **Expect the window to be 12–24 months.** Greptile's TREX (execution during review), CodeRabbit's "quality gates," Baz's Planner, and GitHub's governance stack are all converging on evidence-gated admission from different directions. Multiverso's combination is unoccupied today, not indefinitely.

## Open questions

- Will GitHub extend artifact attestations (build provenance) to *change-level* evidence attestations, collapsing Multiverso's trust-plane differentiation?
- Does the SpaceX acquisition accelerate or freeze Cursor's infrastructure roadmap — and does its best-of-N stay human-adjudicated?
- Can a neutral evidence layer get adopted without owning either the agent or the repo host, or does it need to ship inside an existing admission product (merge queue / reviewer) to get distribution?
- Is Mergify's finding (AI PRs break main *less*) stable, or an artifact of early-adopter review discipline? If agent PRs become *safer* than human PRs by default, does the buyer's willingness to pay for admission evidence rise (audit/compliance) or fall (trust by default)?
- Where do eval ledgers (Braintrust/LangSmith) and code-change evidence converge — will one of them pivot to binding scores to repo snapshots?
- How much of the five-capability combination will enterprises accept as SaaS vs demand on-prem (the Aviator/OpenHands on-prem pattern suggests trust infrastructure skews self-hosted)?

## Sources

- Cognition revenue, valuation & funding — https://sacra.com/c/cognition/ — accessed 2026-08-12
- Coding startup Cognition AI eyes $25B valuation after Windsurf acquisition — https://techfundingnews.com/cognition-ai-25b-valuation-funding-talks-devin-software-engineer/ — 2026
- Devin 2.0 — https://cognition.com/blog/devin-2 — 2025-04-03
- Devin 2.0 is here: Cognition slashes price to $20 — https://venturebeat.com/programming-development/devin-2-0-is-here-cognition-slashes-price-of-ai-software-engineer-to-20-per-month-from-500 — 2025-04
- Cursor 2.0 blog — https://cursor.com/blog/2-0 — 2025-10-29
- SpaceX to acquire Cursor for $60 billion — https://www.cnbc.com/2026/06/16/spacex-spcx-cursor-acquisition-ipo.html — 2026-06-16
- SpaceX Buys Cursor In Largest Startup Acquisition Ever — https://www.forbes.com/sites/sandycarter/2026/06/16/spacex-buys-cursor-in-largest-startup-acquisition-ever-at-60-billion/ — 2026-06-16
- Cursor (Anysphere) Valuation 2026 — https://valueaddvc.com/blog/cursor-ai-valuation-how-a-code-editor-became-a-9b-company — 2026
- OpenAI Codex (AI agent) — Wikipedia — https://en.wikipedia.org/wiki/OpenAI_Codex_(AI_agent) — accessed 2026-08-12
- OpenAI Codex Review — Codegen — https://codegen.com/ai-tools/openai-codex/ — 2026
- Jules: Google's autonomous AI coding agent — https://blog.google/innovation-and-ai/models-and-research/google-labs/jules/ — accessed 2026-08-12
- Google Jules Pricing 2026 — https://hackup.ai/ai-plans/jules/ — 2026-07
- Claude Opus 4.6 — https://www.anthropic.com/news/claude-opus-4-6 — 2026-02-05
- Anthropic Introduces Agent-Based Code Review for Claude Code — https://www.infoq.com/news/2026/04/claude-code-review/ — 2026-04
- Claude Code Agents In 2026 — CloudZero — https://www.cloudzero.com/blog/claude-code-agents/ — 2026
- Claude Code Put Guardrails on Its Own Agent Fleets — https://www.digitalapplied.com/blog/claude-code-subagent-depth-limits-budget-caps-2026 — 2026-07
- Introducing Agent HQ: Any agent, any way you work — https://github.blog/news-insights/company-news/welcome-home-agents/ — 2025-10-28
- GitHub Introduces Agent HQ — Visual Studio Magazine — https://visualstudiomagazine.com/articles/2025/10/28/github-introduces-agent-hq-to-orchestrate-any-agent-any-way-you-work.aspx — 2025-10-28
- How to orchestrate agents using mission control — https://github.blog/ai-and-ml/github-copilot/how-to-orchestrate-agents-using-mission-control/ — 2026
- Why GitHub Agent HQ matters — Eficode — https://www.eficode.com/blog/why-github-agent-hq-matters-for-engineering-teams-in-2026 — 2026
- Factory AI Raises $150M Series C — https://enterprisedna.co/resources/news/factory-ai-series-c-enterprise-coding-agents-2026/ — 2026-04-16
- Factory AI Hits $1.5B — byteiota — https://byteiota.com/factory-ai-150m-funding-autonomous-coding-agents/ — 2026-04
- OpenHands Raises $18.8M Series A — https://www.businesswire.com/news/home/20251118768131/en/OpenHands-Raises-$18.8M-Series-A-to-Bring-Open-Source-Cloud-Coding-Agents-to-Enterprises — 2025-11-18
- Gitpod is now Ona — https://ona.com/stories/gitpod-is-now-ona — 2025-09-03
- Gitpod rebrands as Ona — The Register — https://www.theregister.com/software/2025/09/03/gitpod-rebrands-as-ona-now-an-ai-driven-dev-platform/295031 — 2025-09-03
- Sweep — AI for JetBrains IDEs — https://sweep.dev/ — accessed 2026-08-12
- Cosine — Y Combinator — https://www.ycombinator.com/companies/cosine — accessed 2026-08-12
- Cosine Genie — recatools — https://recatools.com/ai-directory/cosine-genie/ — 2026
- Magic — https://magic.dev — accessed 2026-08-12
- Sculptor: the missing UI for parallel coding agents — https://imbue.com/blog/sculptor-announce — 2025-09-26
- Conductor — welcome.ai profile — https://www.welcome.ai/company/conductor — accessed 2026-08-12
- Conductor by Melty Labs — madewithlove — https://madewithlove.com/blog/conductor-running-multiple-ai-coding-agents-in-parallel/ — 2026
- Vibe Kanban — https://www.vibekanban.com/ — accessed 2026-08-12
- Vibe Kanban After Bloop — Nimbalyst — https://nimbalyst.com/blog/vibe-kanban-after-bloop-whats-next/ — 2026-04
- terragon-oss (shutdown snapshot) — https://github.com/terragon-labs/terragon-oss — 2026-01-16
- Best Tools for Managing Parallel AI Coding Agents — Nimbalyst — https://nimbalyst.com/blog/best-agent-management-tools-2026/ — 2026
- Augment Code launches Intent — Open Orchestrators — https://openorchestrators.org/news/augment-code-intent-launch/ — 2026-02-26
- Investing in GitButler — a16z — https://a16z.com/announcement/investing-in-gitbutler/ — 2026-04-08
- GitButler Lands $17M Series A — MLQ — https://mlq.ai/news/gitbutler-lands-17m-series-a-to-advance-version-control-for-ai-driven-development/ — 2026
- GitButler Raises $22M Series A — StartupHub — https://www.startuphub.ai/ai-news/investors-news/2026/gitbutler-raises-22m-series-a — 2026
- Graphite raises $52M and launches Diamond — https://graphite.com/blog/series-b-diamond-launch — 2025-03-17
- Meet Graphite Agent — https://graphite.com/blog/introducing-graphite-agent-and-pricing — 2025-10
- Greptile blog (Series A, v3–v5, TREX) — https://www.greptile.com/blog — 2025-09-23 through 2026-08-05
- Greptile vs CodeRabbit — https://www.greptile.com/greptile-vs-coderabbit — accessed 2026-08-12
- CodeRabbit Raises $60M Series B — https://www.businesswire.com/news/home/20250916401011/en/CodeRabbit-Raises-$60M-Series-B-Following-Unprecedented-Growth-as-Vibe-Coding-Triggers-a-Need-for-New-Code-Quality-Standards — 2025-09-16
- Our $60M Series B and Quality Gates for AI Coding — https://www.coderabbit.ai/blog/coderabbit-series-b-60-million-quality-gates-for-code-reviews — 2025-09
- CodeRabbit vs Greptile vs Copilot — tech-insider — https://tech-insider.org/au/coderabbit-vs-greptile-vs-copilot-2026/ — 2026
- Baz announces Planner + extended funding — GlobeNewswire — https://www.globenewswire.com/news-release/2026/06/29/3319144/0/en/Agentic-Coding-Company-Baz-Announces-Planner-That-Lets-Engineering-Teams-Eliminate-Entire-Classes-of-Bugs-and-Vulnerabilities-at-Superhuman-Scale-and-an-Extended-Funding-Round-Co-L.html — 2026-06-29
- Baz brings code reviews to the planning stage — SiliconANGLE — https://siliconangle.com/2026/06/29/exclusive-agentic-coding-startup-baz-brings-code-reviews-planning-stage-extends-seed-funding-17m/ — 2026-06-29
- cubic — https://www.cubic.dev/ — accessed 2026-08-12
- cubic — StartupHub profile — https://www.startuphub.ai/startups/cubic — 2026
- Pierre — PitchBook profile — https://pitchbook.com/profiles/company/523179-82 — accessed 2026-08-12
- Pierre — YC launch — https://www.ycombinator.com/launches/I6t-pierre-a-new-way-to-review-code — 2023
- State of Merge Queues 2026 — Mergify — https://mergify.com/reports/state-of-merge-queues-2026 — 2026-07
- Aviator Merge Queue — https://www.aviator.co/merge-queue — accessed 2026-08-12
- Trunk Merge Queue — https://trunk.io/merge-queue — accessed 2026-08-12
- Trunk vs GitHub Merge Queue — https://trunk.io/trunk-vs-github-merge-queue — accessed 2026-08-12
- GitHub Merge Queue in 2026 — Tenki — https://tenki.cloud/blog/github-merge-queue-setup — 2026
- Braintrust vs LangSmith — morphllm — https://www.morphllm.com/comparisons/braintrust-vs-langsmith — 2026
- QA Wolf — https://www.qawolf.com — accessed 2026-08-12
- The Autonomous QA Gold Rush — AgentMarketCap — https://agentmarketcap.ai/blog/2026/04/08/momentic-autonomous-qa-agent-testing-market-2026 — 2026-04-08
- Investors are embracing AI-native testing (Momentic) — QA Financial — https://qa-financial.com/momentic-capital-injection-underscores-investors-are-embracing-ai-native-testing/ — 2025-11
- Jane Street Leads Antithesis's $105M Series A — PR Newswire — https://www.prnewswire.com/news-releases/jane-street-leads-antithesiss-105m-series-a-to-make-deterministic-simulation-testing-the-new-standard-302631076.html — 2025-12-03
- Antithesis — https://antithesis.com/ — accessed 2026-08-12
- SLSA — https://slsa.dev — accessed 2026-08-12
- in-toto — https://in-toto.io — accessed 2026-08-12
