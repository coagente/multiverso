# 20. OSS Strategy, License & Naming

> Part of the Multiverso research corpus - https://github.com/coagente/multiverso - Cutoff: 2026-08-12

## Why this matters for Multiverso

The corpus concluded that Multiverso's defensible wedge is the **vendor-neutral, evidence-native admission layer** — "Sigstore/SLSA-for-agent-changes" ([ch. 10](10-competitive-landscape.md)). Neutrality is not a marketing adjective; it is a *structural* property produced by three concrete choices made before the first commit: the license (does it carry a patent grant and a foundation-donation path?), the contribution policy (can the company unilaterally relicense later?), and the open-core line (do adopters trust that the format and verifier will stay free?). Every trust-infrastructure project that achieved ecosystem adoption — Sigstore, in-toto, SLSA, Zuul — made the same choices; every project that reserved a relicensing option (HashiCorp) or shipped source-available (GitButler) either forfeited neutrality or never claimed it. Naming is the other irreversible day-one decision: registry handles are first-come, and "multiverse" in English is already owned by two multi-billion-dollar companies. This chapter provides the collision evidence and a decision-ready recommendation for the PRD.

## State of the art

### (a) License precedents for trust and admission infrastructure

The pattern across the reference class is unambiguous — **every project whose product is trust chose Apache-2.0**:

| Project | License | Evidence |
|---|---|---|
| Sigstore (cosign) | Apache-2.0 | [sigstore/cosign](https://github.com/sigstore/cosign) (GitHub license API, verified 2026-08-12); an [OpenSSF/Linux Foundation project](https://openssf.org/projects/sigstore/) |
| in-toto | Apache-2.0 | [in-toto/in-toto LICENSE](https://github.com/in-toto/in-toto/blob/develop/LICENSE) ("Copyright 2018 New York University … Apache License, Version 2.0") |
| in-toto Attestation Framework | Apache-2.0 | [in-toto/attestation LICENSE](https://github.com/in-toto/attestation/blob/main/LICENSE) |
| SLSA (specification) | Community Specification License 1.0 | [slsa-framework/slsa LICENSE.md](https://github.com/slsa-framework/slsa/blob/main/LICENSE.md) — a spec license "not intended for source code" |
| Zuul (gating CI) | Apache-2.0, managed by the Open Infrastructure Foundation | [zuul-ci.org](https://zuul-ci.org/) footer: "collaboratively developed under the Apache 2 license and managed by the Open Infrastructure Foundation" |
| Jujutsu (jj) | Apache-2.0 | [jj-vcs/jj](https://github.com/jj-vcs/jj) (GitHub license API) |

The contrast class:

- **GitButler** is **FSL-1.1-MIT** (Sentry's Functional Source License: source-available, non-compete clause, each version converts to MIT after two years) — verified in [gitbutlerapp/gitbutler LICENSE.md](https://github.com/gitbutlerapp/gitbutler/blob/master/LICENSE.md). GitButler adopted it explicitly following Sentry ([GitButler is now Fair Source](https://blog.gitbutler.com/gitbutler-is-now-fair-source), [Sentry's FSL announcement](https://blog.sentry.io/introducing-the-functional-source-license-freedom-without-free-riding/), [TechCrunch on Fair Source, Sep 2024](https://techcrunch.com/2024/09/22/some-startups-are-going-fair-source-to-avoid-the-pitfalls-of-open-source-licensing/)). FSL did not block GitButler's growth as a *client app* — 21.5k stars and a [$17M Series A led by a16z announced 2026-04-08](https://blog.gitbutler.com/series-a) ([a16z](https://a16z.com/announcement/investing-in-gitbutler/)) — but nobody builds *on top of* GitButler as neutral infrastructure, and that is the point: FSL is a product license, not an infrastructure license.
- **Graphite** is fully closed source and raised a [$52M Series B led by Accel (March 2025)](https://graphite.com/blog/series-b-diamond-launch) with participation from Menlo Ventures' Anthology Fund (with Anthropic), Shopify Ventures, and Figma Ventures — proof that the admission/review market pays, and simultaneously proof that the vendor-neutral slot next to it is unoccupied.
- **Mergiraf** is **GPL-3.0-only** (verified in [Cargo.toml on Codeberg](https://codeberg.org/mergiraf/mergiraf)) — fine for a standalone merge driver invoked as a subprocess, but copyleft would be a real adoption barrier for a library/format vendors must embed.
- **Sapling** is **GPL-2.0** ([facebook/sapling](https://github.com/facebook/sapling), inherited from its Mercurial lineage) — Meta shipped it anyway because Sapling is a tool, not a format others must implement.

**Apache-2.0 vs MIT vs BUSL/FSL for a control plane.** Three deciding factors:

1. **Patent grant.** Apache-2.0 §3 grants contributors' patents and includes patent-retaliation termination; MIT is silent on patents. An attestation/admission layer sits in patent-adjacent territory (signing, transparency logs, policy engines); the [CNCF explained in 2017 why it recommends Apache-2.0](https://www.cncf.io/blog/2017/02/01/cncf-recommends-aslv2/) for exactly this reason.
2. **Foundation donation path.** The CNCF charter's default is code under Apache-2.0 and docs under CC-BY-4.0, and "all new inbound code contributions … shall be accompanied by a Developer Certificate of Origin sign-off and made under the Apache License, Version 2.0"; any other license requires Governing Board approval ([CNCF allowed-license policy](https://github.com/cncf/foundation/blob/main/policies-guidance/allowed-third-party-license-policy.md)). Dapr had to relicense MIT→Apache-2.0 to donate ([dapr/dapr#3911](https://github.com/dapr/dapr/issues/3911)). in-toto's whole arc — CNCF Sandbox 2019 → Incubation March 2022 → spec 1.0 June 2023 → **Graduated 2025-04-23** ([CNCF announcement](https://www.cncf.io/announcements/2025/04/23/cncf-announces-graduation-of-in-toto-security-framework-enhancing-software-supply-chain-integrity-across-industries/)) — ran on Apache-2.0 + DCO. If Multiverso ever wants its receipt format to live where in-toto and Sigstore live (CNCF/OpenSSF), Apache-2.0 is the ticket price.
3. **The BUSL cautionary tale.** HashiCorp switched Terraform from MPL-2.0 to BUSL in August 2023; the community forked it as OpenTofu, accepted by the Linux Foundation on 2023-09-20 with pledges from 140+ organizations ([OpenTofu fork announcement](https://opentofu.org/blog/opentofu-announces-fork-of-terraform/), [OpenTofu FAQ](https://opentofu.org/faq/), [Spacelift analysis](https://spacelift.io/blog/terraform-license-change)). The relicense was legally possible *because* HashiCorp's CLA aggregated rights. For a project selling neutrality, the mere *option* to pull a BUSL is a trust deficit.

### (b) Open-core patterns for platform-team tools

The line that works in 2026: **open = everything a single developer or single repo needs; paid = everything an organization needs.**

- **Buildkite**: the [agent is MIT-licensed open source](https://github.com/buildkite/agent) ("an open-source toolkit … for securely running build jobs on any device or network"); the coordinating control plane at [buildkite.com](https://buildkite.com/) is closed SaaS. The compute runs on your infrastructure; the orchestration state is the product.
- **Depot**: the [CLI is MIT](https://github.com/depot/cli); the accelerated remote builders at [depot.dev](https://depot.dev/) are the paid service.
- **Chainguard**: the build toolchain is Apache-2.0 open source ([apko](https://github.com/chainguard-dev/apko), [melange](https://github.com/chainguard-dev/melange)) and Wolfi is a free undistro; the paid catalog is 2,000+ hardened images with version pinning and CVE-remediation SLAs, with a free tier of ~50 `:latest` images ([Contrary Research breakdown](https://research.contrary.com/company/chainguard)). This monetized to $40M ARR and a [$356M Series D at $3.5B (April 2025)](https://www.securityweek.com/chainguard-raises-hefty-356m-series-d-at-3-5-billion-valuation/) plus a [$280M growth investment (October 2025)](https://siliconangle.com/2025/10/23/chainguard-secures-280m-expand-trusted-open-source-software-platform/). Chainguard is the single best model for Multiverso: *the format and tools are free; the continuously-operated trust service is paid.*
- **Astral**: tools permissive ([ruff MIT](https://github.com/astral-sh/ruff), [uv dual MIT/Apache-2.0](https://github.com/astral-sh/uv)), monetization via [pyx, a hosted Python-native registry (beta August 2025)](https://astral.sh/blog/introducing-pyx) — "sell software that vertically integrates with their open source tools to companies already using them" ([Simon Willison's summary](https://simonwillison.net/2025/Aug/13/pyx/)). Epilogue: [OpenAI announced its acquisition of Astral on 2026-03-19](https://openai.com/index/openai-to-acquire-astral/); post-acquisition, pyx was wound down and its GPU-packaging infrastructure open-sourced ([pydevtools report](https://pydevtools.com/blog/astral-winds-down-pyx-open-sources-gpu-packaging/), [Talk Python #552](https://talkpython.fm/episodes/show/552/astral-joins-openai)). Lesson: permissive tools + massive adoption made Astral strategic enough to acquire before the revenue model matured. The adjacent precedent — [Anthropic acquired Bun (2025-12-02), which stays MIT](https://bun.com/blog/bun-joins-anthropic) — confirms AI labs are now buying dev-toolchain adoption directly.
- **GitButler** (FSL, client free / cloud paid) shows the same split from the source-available side.

Mapped to Multiverso, the consensus line is: **open** — CLI, world/candidate engine, receipt & attestation formats, verifier, single-repo local evidence ledger, Git/jj adapters; **paid** — hosted org-wide evidence ledger (transparency-log-as-a-service), org dashboard, centralized policy management, SSO/SCIM, cross-repo analytics, compliance exports.

### (c) Governance for building in public

**DCO, not CLA.** The [Developer Certificate of Origin](https://developercertificate.org/) is a per-commit sign-off asserting the right to contribute; it does not assign or aggregate rights, so it makes future unilateral relicensing effectively impossible — which is precisely the credible commitment a neutrality-selling project wants. CNCF *requires* DCO for inbound contributions ([CNCF policy](https://github.com/cncf/foundation/blob/main/policies-guidance/allowed-third-party-license-policy.md)); Sigstore requires DCO sign-off ([cosign CONTRIBUTING.md](https://github.com/sigstore/cosign/blob/main/CONTRIBUTING.md)). The counter-examples: HashiCorp's CLA is what made the BUSL flip legal, and **jj still requires the Google CLA** for all contributions even after moving to the community `jj-vcs` org with elected maintainers ([jj contributing docs](https://github.com/jj-vcs/jj/blob/main/docs/contributing.md): "Contributions to this project must be accompanied by a Contributor License Agreement … cla.developers.google.com"; [jj GOVERNANCE.md](https://github.com/jj-vcs/jj/blob/main/GOVERNANCE.md)) — a persistent community friction point and a reminder that governance debt compounds.

**Roadmap-in-issues.** Standard practice at every scale: GitHub runs its [public roadmap as a repo](https://github.com/github/roadmap) (8.8k stars), Bun ran years on a [single pinned roadmap issue (#159, 183 comments)](https://github.com/oven-sh/bun/issues/159), and jj keeps a [roadmap document in-repo](https://github.com/jj-vcs/jj/blob/main/docs/roadmap.md). For a pre-1.0 project the Bun pattern (one pinned meta-issue + labeled milestone issues) has the best signal-to-ceremony ratio.

**Research artifact + product.** in-toto is the canonical dual-track: academic paper (USENIX Security 2019, NYU) → [spec + attestation framework in separate repos](https://github.com/in-toto/attestation) → implementations → CNCF graduation. Multiverso already has the research corpus living at `research/` inside the product repo (this document is part of it) — that is an asset, not a liability: it is the citable object reviewers and standards bodies engage with, while `spec/` and the code evolve at product speed. The corpus argues the durable contribution is the scheduler + FAR result ([ch. 12](12-novelty-assessment.md)); keeping the research in-tree keeps every claim diffable against the implementation.

### (d) Naming check: "multiverso" / "multiverse" collisions

All registry checks performed 2026-08-12 against live registry APIs.

**The English word is gone.** "Multiverse" is claimed by two multi-billion-dollar companies: **Multiverse Computing** (San Sebastián quantum-AI company; [$215M Series B, June 2025](https://techcrunch.com/2025/06/12/multiverse-computing-raises-215m-for-tech-that-could-radically-slim-ai-costs/); [$570M Series C at $2.3B valuation, July 2026](https://techfundingnews.com/multiverse-computing-570m-series-c-2-3b-valuation/)) and **Multiverse** (Euan Blair's London edtech; [$1.7B valuation, June 2022](https://techcrunch.com/2022/06/08/multiverse-nabs-220m-at-a-1-7b-valuation-to-expand-its-tech-apprenticeship-platform); later [$2.1B](https://sifted.eu/articles/euan-blairs-multiverse-70m-fundraise)). In devtools specifically, **multiverse is an Ubuntu repository component** ("software restricted by copyright or legal issues" — [Ubuntu repository docs](https://help.ubuntu.com/community/Repositories/Ubuntu)), an unfortunate connotation for a trust product, and the top GitHub result is a Minecraft plugin ([Multiverse/Multiverse-Core](https://github.com/Multiverse/Multiverse-Core), 1.1k stars). npm `multiverse` is squatted by a stale game engine (v0.1.0, last modified 2022).

**"Multiverso" (Spanish) is substantially clean.** Collision inventory:

- **GitHub**: the handle `multiverso` is a **dormant organization** (created 2014-10-31, 1 public repo, no activity since 2014-11-01 — GitHub users API). The most-starred repo of that name is [microsoft/Multiverso](https://github.com/microsoft/Multiverso) — a **archived** (MIT, 779 stars) parameter-server framework from the pre-2020 DMTK era; it is dead and in an unrelated domain, but it will share search results. `multiverso-dev` and `multiversohq` are unregistered (404). The project already lives at `coagente/multiverso`, which sidesteps the handle question.
- **npm**: `multiverso` — **available** (registry 404).
- **PyPI**: `multiverso` — **available** (404; caveat: PyPI 404 can also mean a previously-used name subject to reuse rules under PEP 541 — claim it early).
- **crates.io**: `multiverso` — **available** ("crate does not exist").
- **Homebrew core**: no formula matches `multiverso`, `multiverse`, `mvo`, `uvr`, or `race` (full formula index scanned).
- **Arch/Debian**: no packages named `multiverso`, `mvo`, or `uvr` (Arch packages API; Debian sources API).

**CLI binary candidates**, same-day evidence:

| Candidate | npm | PyPI | crates.io | Homebrew | Arch/Debian | Verdict |
|---|---|---|---|---|---|---|
| `multiverso` | available | available | available | free | free | Clean, but 10 chars — too long as a daily driver |
| `mvo` | squatted (empty v0.0.1, 2022) | available | available | free | free | **Best short form**; npm squat is irrelevant for a compiled CLI |
| `uvr` | available | **TAKEN — active** ([uvr 0.1.28](https://pypi.org/project/uvr/), a uv-ecosystem script runner) | available | free | free | Reject: live collision inside the uv ecosystem |
| `race` | taken | taken | **TAKEN** ([race crate](https://crates.io/crates/race), v0.1.15, 2.2k downloads) | free | old Debian `race` pkg existed (sarge) | Reject as binary; keep as subcommand (`mvo race`) |

`mvo` is one edit from coreutils `mv`, which is a *discoverability* feature (muscle-memory adjacency) and a negligible typo hazard (typing `mvo` when meaning `mv` fails loudly; the reverse invokes a harmless file move only with valid arguments).

### (e) Precedent: research-credibility-first launches in devtools

- **Sapling (Meta)**: [announced 2022-11-15](https://engineering.fb.com/2022/11/15/open-source/sapling-source-control-scalable/) with a deep engineering blog, a docs site, a working Git-compatible client, and a demo app (ReviewStack) — while explicitly holding back the server/VFS ("we hope to open-source these in the future"). Credibility came from scale claims + a runnable client on day one.
- **jj**: started by Google engineer Martin von Zweigbergk as a personal-time project; credibility staged through in-repo design docs, a **Git Merge 2022 talk about Google's plans for jj** ([jj README](https://github.com/jj-vcs/jj/blob/main/README.md)), then community governance ([GOVERNANCE.md](https://github.com/jj-vcs/jj/blob/main/GOVERNANCE.md) with elected maintainers) — 31k stars without a paper, on the strength of design writing plus a daily-usable tool.
- **GitButler**: closed client → [FSL "Fair Source" release](https://blog.gitbutler.com/gitbutler-is-now-fair-source) + [Open Source Pledge ($2,000/dev/year)](https://docs.gitbutler.com/community/open-source) → [$17M a16z Series A, April 2026](https://blog.gitbutler.com/series-a). Founder pedigree (Scott Chacon, GitHub co-founder) substituted for research artifacts.
- **Antithesis**: founded 2018, six years in stealth, [launched 2024-02-13 with $47M in seed funding](https://techcrunch.com/2024/02/13/antithesis-raises-47m-to-launch-an-automated-testing-platform-for-software/) and the FoundationDB deterministic-testing lineage as its entire credibility story ([launch PR](https://www.prnewswire.com/news-releases/antithesis-launches-out-of-stealth-to-revolutionize-software-reliability-302060173.html)).
- **in-toto**: the patient path — academic paper → CNCF Sandbox (2019) → spec 1.0 (2023) → [Graduated (April 2025)](https://www.cncf.io/announcements/2025/04/23/cncf-announces-graduation-of-in-toto-security-framework-enhancing-software-supply-chain-integrity-across-industries/) with adopters like Autodesk and SolarWinds.

Multiverso's analog: the corpus **is** the stealth-period artifact. The Antithesis/Sapling pattern says: launch with (1) the research corpus public, (2) a runnable `mvo race` demo on day one, (3) the receipt spec, and (4) a measured TCAR/FAR number as the headline claim ([ch. 9](09-benchmarks-evaluation.md)) — the Fishtest move: let the statistics carry the announcement.

## Comparison table

| Project | License | Contribution policy | Open/paid line | Outcome signal |
|---|---|---|---|---|
| [Sigstore](https://github.com/sigstore/cosign) | Apache-2.0 | DCO | All open; free public-good services (Rekor/Fulcio) | Default signing layer, [OpenSSF-governed](https://openssf.org/projects/sigstore/) |
| [in-toto](https://github.com/in-toto/in-toto) | Apache-2.0 | DCO (CNCF) | All open (spec + impls) | [CNCF Graduated 2025](https://www.cncf.io/announcements/2025/04/23/cncf-announces-graduation-of-in-toto-security-framework-enhancing-software-supply-chain-integrity-across-industries/) |
| [SLSA](https://github.com/slsa-framework/slsa) | Community Spec License 1.0 (spec) | OpenSSF WG | Spec open; vendors monetize conformance | Industry reference framework |
| [Zuul](https://zuul-ci.org/) | Apache-2.0 | OpenInfra Foundation | All open; vendors sell hosting | Neutral gating CI since 2012 |
| [jj](https://github.com/jj-vcs/jj) | Apache-2.0 | **Google CLA** (friction) | All open | 31k stars; community governance |
| [GitButler](https://github.com/gitbutlerapp/gitbutler) | **FSL-1.1-MIT** | — | Client fair-source; cloud paid | [$17M Series A](https://blog.gitbutler.com/series-a); no third-party ecosystem |
| Graphite | Closed | — | All paid | [$52M Series B](https://graphite.com/blog/series-b-diamond-launch); zero neutrality |
| [Mergiraf](https://codeberg.org/mergiraf/mergiraf) | GPL-3.0-only | — | All open | Adopted as subprocess tool only |
| [Buildkite agent](https://github.com/buildkite/agent) | MIT (agent) | — | Agent open / control plane SaaS | Durable hybrid since 2014 |
| [Chainguard](https://github.com/chainguard-dev/apko) | Apache-2.0 (tools) | — | Toolchain open / image catalog paid | [$3.5B valuation](https://www.securityweek.com/chainguard-raises-hefty-356m-series-d-at-3-5-billion-valuation/) |
| [Astral](https://github.com/astral-sh/uv) | MIT + Apache-2.0 (tools) | — | Tools open / pyx registry paid | [Acquired by OpenAI 2026-03](https://openai.com/index/openai-to-acquire-astral/) |
| [HashiCorp/Terraform](https://opentofu.org/blog/opentofu-announces-fork-of-terraform/) | MPL→**BUSL** | **CLA** (enabled flip) | — | Forked as OpenTofu within weeks |

## Recommendation for the PRD

**License: Apache-2.0 for everything published. No FSL, no BUSL, no MIT.** The receipt/attestation format, verifier, CLI, engine, and adapters go out under Apache-2.0 with per-file headers from day one. Rationale: (1) the patent grant matters in signing/attestation territory and MIT lacks it ([CNCF rationale](https://www.cncf.io/blog/2017/02/01/cncf-recommends-aslv2/)); (2) it keeps the CNCF/OpenSSF donation path open at zero cost — the receipt predicate should eventually sit next to [in-toto/attestation](https://github.com/in-toto/attestation), and CNCF's default requires Apache-2.0 + DCO ([policy](https://github.com/cncf/foundation/blob/main/policies-guidance/allowed-third-party-license-policy.md)); (3) every successful project in Multiverso's exact reference class (Sigstore, in-toto, Zuul, jj) chose it. FSL is explicitly rejected for the core: it works for end-user apps like GitButler, but Multiverso's thesis is that *other vendors* (merge queues, AI reviewers, agent frameworks) emit and verify its receipts — no vendor integrates a format whose license names them a prohibited competitor.

**Open-core line (Chainguard-shaped).** Open: `mvo` CLI, world engine, receipt format + reference verifier, in-toto predicate definition, local single-repo evidence ledger, Git/jj adapters. Paid (closed from day one — closed, not FSL, so the boundary is honest): hosted multi-repo evidence ledger with retention SLAs, org dashboard, policy management, SSO/SCIM, compliance exports. Never monetize verification of receipts — verification must be free everywhere forever, exactly as cosign verifies for free.

**Contribution policy: DCO + `git commit -s`, enforced by the DCO GitHub check. No CLA.** This deliberately forecloses the HashiCorp relicensing option and should be stated out loud in the README as a neutrality commitment ("we cannot rug-pull: contributions are DCO'd under Apache-2.0"). Adopt a `GOVERNANCE.md` on the jj model (named maintainers, documented decision process) before external contributors arrive, not after.

**Repo layout: monorepo now, spec split at donation time.** Keep `coagente/multiverso` as a single repo: `research/` (this corpus, citable), `spec/` (receipt + attestation predicate, versioned independently), `crates/` or `src/` (engine + CLI), `docs/`. Precedent: jj ships lib+cli+docs in one repo; in-toto split its attestation spec into its own repo only when it became a multi-implementation standard. Split `multiverso-spec` out the day a second implementation or a foundation conversation exists. Roadmap: one pinned meta-issue + milestone-labeled issues (the [Bun #159 pattern](https://github.com/oven-sh/bun/issues/159)).

**Naming: keep "Multiverso"; binary `mvo`; race is a subcommand.** "Multiverso" beats "Multiverse" on every axis: the English word is claimed by a [$2.3B quantum-AI company](https://techfundingnews.com/multiverse-computing-570m-series-c-2-3b-valuation/), a [$2.1B edtech](https://sifted.eu/articles/euan-blairs-multiverse-70m-fundraise), an [Ubuntu component meaning "restricted by copyright or legal issues"](https://help.ubuntu.com/community/Repositories/Ubuntu), and a Minecraft plugin — while "multiverso"'s only collisions are a dormant 2014 GitHub org and the archived [microsoft/Multiverso](https://github.com/microsoft/Multiverso). Ship the CLI as package `multiverso` with binary `mvo` (`multiverso` also installed as an alias). **Action items for week one:** publish placeholder releases to claim `multiverso` and `mvo` on crates.io and `multiverso` on PyPI and npm (all verified available 2026-08-12); register the `multiverso-dev` GitHub org (available) as a redirect guard; do not use `uvr` (live PyPI collision) or `race` (taken on npm/PyPI/crates) as binaries.

**Launch staging (Antithesis/Sapling pattern):** corpus public (done) → single deep technical launch post + runnable `mvo race` demo + receipt spec v0 on the same day → headline is a measured TCAR/FAR number on a budget-matched benchmark, not an architecture diagram.

## Open questions

1. **Trademark clearance.** Registry availability is not trademark clearance. A USPTO/EUIPO knockout search for "MULTIVERSO" in classes 9/42 is unverified here and must be commissioned before the name is load-bearing; Multiverse Computing's marks are the obvious adjacent filing to check.
2. **PyPI name history.** The `multiverso` PyPI 404 does not prove the name was never used (PEP 541 governs reuse of deleted names); confirm at claim time. (unverified)
3. **Spec license fork-in-the-road.** If the receipt predicate goes to an OpenSSF working group instead of CNCF, is the Community Specification License (SLSA's choice) required, or does Apache-2.0-for-spec-text suffice? Depends on the target WG's charter — resolve when the donation conversation starts.
4. **Acquisition posture.** Astral (OpenAI, 2026-03) and Bun (Anthropic, 2025-12) show permissive-licensed, high-adoption devtools are now acquired *for distribution* before revenue matures. Apache-2.0 + DCO means the code survives any acquirer, but the PRD should state whether the hosted-ledger business is built to be independent or to be strategic to a lab.
5. **jj dependency exposure.** If Multiverso ships jj adapters, jj's Google CLA and Apache-2.0 pose no *usage* problem, but upstreaming fixes requires contributors to sign Google's CLA — minor friction to document for contributors.

## Sources

- Sigstore cosign (Apache-2.0, DCO): https://github.com/sigstore/cosign · https://github.com/sigstore/cosign/blob/main/CONTRIBUTING.md · https://openssf.org/projects/sigstore/
- in-toto (Apache-2.0; CNCF graduation): https://github.com/in-toto/in-toto · https://github.com/in-toto/attestation · https://www.cncf.io/announcements/2025/04/23/cncf-announces-graduation-of-in-toto-security-framework-enhancing-software-supply-chain-integrity-across-industries/
- SLSA spec license: https://github.com/slsa-framework/slsa/blob/main/LICENSE.md
- CNCF licensing policy & rationale: https://github.com/cncf/foundation/blob/main/policies-guidance/allowed-third-party-license-policy.md · https://www.cncf.io/blog/2017/02/01/cncf-recommends-aslv2/ · https://github.com/dapr/dapr/issues/3911
- Zuul (Apache-2.0, OpenInfra): https://zuul-ci.org/
- jj (Apache-2.0, Google CLA, governance, Git Merge talk): https://github.com/jj-vcs/jj · https://github.com/jj-vcs/jj/blob/main/docs/contributing.md · https://github.com/jj-vcs/jj/blob/main/GOVERNANCE.md
- GitButler (FSL-1.1-MIT, Fair Source, Series A): https://github.com/gitbutlerapp/gitbutler/blob/master/LICENSE.md · https://blog.gitbutler.com/gitbutler-is-now-fair-source · https://blog.gitbutler.com/series-a · https://a16z.com/announcement/investing-in-gitbutler/ · https://docs.gitbutler.com/community/open-source
- Sentry FSL / Fair Source: https://blog.sentry.io/introducing-the-functional-source-license-freedom-without-free-riding/ · https://fsl.software/ · https://techcrunch.com/2024/09/22/some-startups-are-going-fair-source-to-avoid-the-pitfalls-of-open-source-licensing/
- Graphite Series B: https://graphite.com/blog/series-b-diamond-launch
- Mergiraf (GPL-3.0-only): https://codeberg.org/mergiraf/mergiraf
- HashiCorp BUSL → OpenTofu: https://opentofu.org/blog/opentofu-announces-fork-of-terraform/ · https://opentofu.org/faq/ · https://spacelift.io/blog/terraform-license-change
- Buildkite agent (MIT): https://github.com/buildkite/agent
- Depot CLI (MIT): https://github.com/depot/cli · https://depot.dev/
- Chainguard (Apache-2.0 tools; funding): https://github.com/chainguard-dev/apko · https://github.com/chainguard-dev/melange · https://research.contrary.com/company/chainguard · https://www.securityweek.com/chainguard-raises-hefty-356m-series-d-at-3-5-billion-valuation/ · https://siliconangle.com/2025/10/23/chainguard-secures-280m-expand-trusted-open-source-software-platform/
- Astral / pyx / OpenAI acquisition: https://github.com/astral-sh/uv · https://github.com/astral-sh/ruff · https://astral.sh/blog/introducing-pyx · https://simonwillison.net/2025/Aug/13/pyx/ · https://openai.com/index/openai-to-acquire-astral/ · https://simonwillison.net/2026/mar/19/openai-acquiring-astral/ · https://pydevtools.com/blog/astral-winds-down-pyx-open-sources-gpu-packaging/
- Anthropic/Bun: https://bun.com/blog/bun-joins-anthropic · https://simonwillison.net/2025/Dec/2/anthropic-acquires-bun/
- DCO: https://developercertificate.org/
- Roadmap practices: https://github.com/github/roadmap · https://github.com/oven-sh/bun/issues/159 · https://github.com/jj-vcs/jj/blob/main/docs/roadmap.md
- Sapling launch: https://engineering.fb.com/2022/11/15/open-source/sapling-source-control-scalable/ · https://github.com/facebook/sapling
- Antithesis launch: https://techcrunch.com/2024/02/13/antithesis-raises-47m-to-launch-an-automated-testing-platform-for-software/ · https://www.prnewswire.com/news-releases/antithesis-launches-out-of-stealth-to-revolutionize-software-reliability-302060173.html
- Naming collisions: https://github.com/microsoft/Multiverso · https://github.com/Multiverse/Multiverse-Core · https://help.ubuntu.com/community/Repositories/Ubuntu · https://techcrunch.com/2025/06/12/multiverse-computing-raises-215m-for-tech-that-could-radically-slim-ai-costs/ · https://techfundingnews.com/multiverse-computing-570m-series-c-2-3b-valuation/ · https://techcrunch.com/2022/06/08/multiverse-nabs-220m-at-a-1-7b-valuation-to-expand-its-tech-apprenticeship-platform · https://sifted.eu/articles/euan-blairs-multiverse-70m-fundraise · https://pypi.org/project/uvr/ · https://crates.io/crates/race
- Registry availability checks (npm registry API, PyPI JSON API, crates.io API, Homebrew formulae API, Arch packages API, Debian sources API, GitHub users/search API), performed live 2026-08-12.
