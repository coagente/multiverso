# 1. Parallel Agent Exploration & World Isolation

> Part of the Multiverso research corpus - https://github.com/coagente/multiverso - Cutoff: 2026-08-12

## Why this matters for Multiverso

Multiverso's core loop — versioned Intent → N isolated candidate Worlds → Evidence bound to each world's exact state → Decision → Admission to trunk — presupposes that a "World" is a real, forkable, inspectable thing. The industry has converged on parallel candidate exploration as a workflow (every major coding-agent vendor now ships some form of it), but it has *not* converged on what a candidate actually is. In practice, systems capture wildly different slices of state: some fork only a source tree (git worktrees), some fork a filesystem-plus-container environment, and a few fork entire running machines including memory and processes (Morph Infinibranch, Cognition's otterlink/blockdiff, Firecracker snapshots). One 2026 research paper — *Fork, Explore, Commit* — proposes OS-level primitives for exactly this pattern, with commit/abort semantics that look strikingly like a degenerate version of Multiverso's Decision plane (first-commit-wins, no composition, no evidence gating).

This chapter maps that landscape and answers the key question for Multiverso's data and execution planes: **which systems treat a candidate as a full WORLD (code + environment + processes + trace), and which treat it as merely a branch or diff** — and what none of them do that Multiverso proposes to.

## State of the art

### 1. The OS-primitive frontier: "Fork, Explore, Commit"

The most direct academic treatment of Multiverso's World concept is [Fork, Explore, Commit: OS Primitives for Agentic Exploration](https://arxiv.org/abs/2602.08199) (Cong Wang and Yusheng Zheng; arXiv 2602.08199, submitted 2026-02-09, revised 2026-03-19). The paper observes that agents doing "agentic exploration" — pursuing multiple solution paths in parallel and committing only the successful one — need isolated environments with *atomic commit and rollback for both filesystem and process state*, and that no single existing Linux mechanism provides this; composing namespaces, overlayfs, and cgroups in userspace "introduces race windows between steps, error-prone cleanup on partial failure" ([HTML v2](https://arxiv.org/html/2602.08199v2)).

Its proposed abstraction, the **branch context**, has four properties: (1) copy-on-write state isolation with independent filesystem views and process groups; (2) a structured lifecycle of fork → explore → commit/abort; (3) **first-commit-wins** resolution that automatically invalidates sibling branches; (4) nestable contexts forming a tree, enabling Tree-of-Thoughts-style hierarchical exploration where each level commits only to its immediate parent ([arXiv 2602.08199](https://arxiv.org/html/2602.08199v2)).

Two implementations are described:

- **BranchFS**, a FUSE filesystem with *file-level* (not block-level) copy-on-write: the first modification copies the whole file from the base (or nearest ancestor branch) into the branch's delta layer; unmodified files resolve by walking up the branch chain. Creation is O(1) and under 350 μs regardless of base tree size; commits run 317 μs (1 KB of changes) to ~2.1 ms (1 MB). In FUSE passthrough mode it reaches 82% of native read throughput (7,236 vs 8,800 MB/s). It requires no root privileges ([arXiv 2602.08199](https://arxiv.org/html/2602.08199v2)).
- A proposed **`branch()` syscall** that spawns processes into branch contexts with kernel-enforced isolation: per-branch mount namespaces, escape-proof process groups (no `setsid()`/`setpgid()` escape), exclusive commit groups, and optional signal/ptrace barriers between siblings (`BR_ISOLATE`). On commit, the kernel "wins the exclusive group race," bumps the parent's epoch counter, siblings get `-ESTALE` on their next commit attempt, and their memory-mapped regions raise `SIGBUS` ([arXiv 2602.08199](https://arxiv.org/html/2602.08199v2)).

**Limits (stated by the authors, and decisive for Multiverso):** external side effects — network, IPC, device I/O — "are not rolled back on abort" (they sketch future "effect gating" that would buffer external actions until commit); memory state is *not* branched today (a future `BR_MEMORY` flag); symlinks to paths outside the branch escape isolation, hardlinks lose linkage on copy-up, special files are unsupported; and critically, the system **"cannot combine results from multiple branches" — only single-winner semantics exist** ([arXiv 2602.08199](https://arxiv.org/html/2602.08199v2)). In Multiverso terms: FEC implements SELECT-by-race with no COMPOSE, no SERIALIZE, no evidence, and no attestation.

### 2. Isolation substrates: what can actually be forked

**Git worktrees** are the cheapest substrate: a second working directory checked out from the same repository, one branch per worktree. They isolate *source files only* — dependencies, build artifacts, running processes, ports, and databases are shared or duplicated ad hoc; practitioner guides consistently pair worktrees with per-session port and database isolation to avoid collisions ([MindStudio guide](https://www.mindstudio.ai/blog/parallel-ai-coding-agents-git-worktrees)). Lineage is whatever git records: commits and branches, with no environment identity at all.

**Copy-on-write filesystems.** [OverlayFS](https://docs.kernel.org/filesystems/overlayfs.html) layers a writable upper directory over read-only lowers with copy-up-on-write and whiteout markers for deletions — the mechanism underlying container image layers; it is file-granular, and modifying lower layers while mounted is prohibited. Block-granular alternatives (Btrfs, ZFS, device-mapper snapshots) offer cheaper snapshots of large trees; the FEC paper surveys all of these and finds none provide process lifecycle or commit semantics on their own ([arXiv 2602.08199](https://arxiv.org/html/2602.08199v2)).

**Containers** (Docker/OCI, gVisor) add environment isolation — dependencies, ports, process namespace — on a shared kernel. [Imbue's Sculptor work](https://imbue.com/blog/containers) (2025-11-17) shows the practical cost center is *startup*: pre-baking dependencies into devcontainer image layers took agent-sandbox start "from minutes to seconds" (~10x).

**MicroVMs.** [Firecracker](https://firecracker-microvm.github.io) (AWS, powers Lambda/Fargate) boots to user code in ~125 ms with <5 MiB overhead per microVM and creation rates up to 150 microVMs/s/host, with a jailer process as a second defense line. Its [snapshot support](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md) captures guest memory, vCPU and device state, resumes via `MAP_PRIVATE` copy-on-write mapping of the memory file, and offers diff snapshots (developer preview). Two documented caveats matter for world semantics: guest network connectivity "is not guaranteed to be preserved after resume," and clones resumed from one snapshot **share duplicated identities, random-number state, and cryptographic tokens** — Linux 5.18+ VMGenID triggers entropy reseeding, but application-level state duplication remains ([Firecracker docs](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)).

**Purpose-built VM forking for agents.** Two industrial systems go beyond stock Firecracker:

- **Cognition (Devin)** built the **otterlink** hypervisor and the open-source [blockdiff](https://cognition.com/blog/blockdiff) disk-snapshot format because EC2 snapshots took 30+ minutes; blockdiff compares physical block maps via the `FIEMAP` ioctl and stores only changed blocks, cutting a 20 GB snapshot to ~200 ms (~200x). This enables Devin's environment forking, sleep/wake session persistence, and in-session disk rollback ([blockdiff blog](https://cognition.com/blog/blockdiff), [GitHub](https://github.com/CognitionAI/blockdiff)).
- **Morph Cloud's Infinibranch** (announced [2024-11-19](https://cloud.morph.so/docs/blog/developers)) snapshots, branches, and restores *entire VM runtime state* — disk, memory, running processes and services — in under ~250 ms, explicitly marketed for tree-search agents: snapshot a running computation once, fork into e.g. 16 parallel branches that each continue from the identical point without replaying setup ([Morph docs](https://cloud.morph.so/docs/blog/developers), [morph.so](https://www.morph.so/)).

**Sandbox providers.**

- [E2B](https://e2b.dev/) runs each sandbox in its own Firecracker microVM (open source, Apache-2.0); its [persistence feature](https://docs.e2b.dev/sandbox/persistence) pauses a sandbox saving *both filesystem and memory* ("all running processes, loaded variables, data") and resumes from that exact state, with a `keepMemory: false` option for filesystem-only snapshots.
- [Modal](https://modal.com/resources/best-stateful-sandboxes-long-running-agent-sessions) builds sandboxes on gVisor and offers filesystem snapshots (GA), directory snapshots (beta), and memory snapshots (alpha) that capture filesystem plus in-memory state so execution resumes from the same point.
- [Daytona](https://www.daytona.io/docs/en/snapshots/) defines snapshots as "persistent, point-in-time captures of sandbox state, including the filesystem, installed packages, dependencies, and settings"; container-class sandboxes get cold (filesystem-only) snapshots while VM-class sandboxes support hot snapshots with an `includeMemory` parameter, and warm pools hand out pre-created sandboxes instantly.
- [Dagger's container-use](https://github.com/dagger/container-use) (Apache-2.0, experimental, ~4k stars) is an MCP server giving each agent an *environment* = Docker container + dedicated git branch (`container-use/<env>`); every change is auto-committed and it keeps "complete command history and logs of what agents actually did," reviewed and merged via ordinary git ([Dagger blog](https://dagger.io/blog/agent-container-use/)).
- [AgentFS](https://www.agentfs.ai/) (Turso, MIT) takes a different angle: an isolated copy-on-write filesystem backed by a single SQLite file, with "every operation logged to a portable SQLite file" — the whole agent session (every read/write) becomes one auditable, portable artifact whose changes a human can accept/reject before they touch real files.

### 3. Agent products that run parallel candidate exploration

**Claude Code (Anthropic).** Official docs support parallel sessions natively: `claude --worktree <name>` creates an isolated worktree checkout per session, subagents run research in separate context windows, and a background-agents "agent view" monitors parallel sessions from one screen ([Claude Code docs](https://code.claude.com/docs/en/common-workflows)). Secondary sources report native `--worktree` shipping ~February 2026, an `isolation: "worktree"` option routing subagents into their own worktrees, and a research-preview **Agent Teams** feature where coordinating sessions message each other; Claude Code's creator publicly recommends "3–5 git worktrees at once, each running its own Claude session" ([Developers Digest](https://www.developersdigest.tech/blog/git-worktrees-claude-code-parallel-agents-guide), [CloudZero](https://www.cloudzero.com/blog/claude-code-agents/)). Isolation is **code-only** (worktrees); environment, processes, and databases are the user's problem — hence the tooling folklore about per-worktree ports and DB branches ([MindStudio](https://www.mindstudio.ai/blog/parallel-ai-coding-agents-git-worktrees)).

**Conductor (Melty Labs)** is a free Mac app that orchestrates many parallel Claude Code / Codex agents, "each working in an isolated git worktree," with review-and-merge UX ([conductor.build docs](https://www.conductor.build/docs/guides/git-worktrees/run-claude-code-with-git-worktrees), [Product Hunt](https://www.producthunt.com/products/conductor-aa77ddef-e6d3-4805-a179-7b2e17b6e22e)). Same code-only isolation tier as raw worktrees.

**Sculptor (Imbue)** runs each parallel Claude Code agent in its **own Docker container** built from the project's devcontainer spec — full environment isolation, safe destructive experiments — with **Pairing Mode** bidirectionally syncing a chosen container's state into the local IDE for hands-on testing ([Imbue](https://imbue.com/sculptor/), [containers blog](https://imbue.com/blog/containers), [docs](https://docs.imbue.com/features/containers)). This is a genuine code+environment world, though not processes/memory-forkable.

**Cursor cloud agents** (formerly background agents) run "in isolated VMs in the cloud with full development environments": the VM clones the repo, works on a separate branch, and pushes changes; environments include "cloned repos, installed dependencies, secrets, startup commands, and network access," configured via agent-led setup, saved snapshots, or Dockerfiles, with unlimited parallel agents. Since ~February 2026 each agent gets a desktop + browser ("computer use") and produces "merge-ready PRs with artifacts to demo their changes" — screenshots, videos, logs — with remote-desktop takeover for humans ([Cursor docs](https://cursor.com/docs/cloud-agent)). A self-hosted variant runs the VMs in customer infrastructure ([Cursor blog](https://cursor.com/blog/self-hosted-cloud-agents)). This is one of the fullest commercial "worlds": code + env + processes + services + a demo-artifact trace — but the *output* is still just a branch/PR; the world itself is not a first-class, attestable object.

**OpenAI Codex (cloud)** provisions an isolated container per task, preloaded with the repo and dependencies from a per-repo setup script; during the agent phase **internet access is disabled by default** (configurable per environment), constraining the agent to provided code — a deliberate security/reproducibility stance ([Introducing Codex](https://openai.com/index/introducing-codex/), 2025-05; [Codex cloud docs](https://learn.chatgpt.com/docs/cloud), [agent security docs](https://developers.openai.com/codex/agent-approvals-security)). Users run several tasks in parallel and review summary + diff + task logs before opening a PR ([Codex cloud docs](https://learn.chatgpt.com/docs/cloud)). Notably, Codex's habit of citing terminal logs and test outputs in its final answer is a primitive form of evidence-reporting — but the evidence is prose-linked, not cryptographically bound to the sandbox state.

**Google Jules** runs each task in a "secure, short-lived virtual machine" (Ubuntu; Node/Python/Go/Java/Rust toolchains plus Docker preinstalled), destroyed after the task; setup scripts can be validated and captured via "Run and Snapshot" so future tasks on the repo reuse the environment snapshot ([Jules environment docs](https://jules.google/docs/environment/), [Google announcement](https://blog.google/innovation-and-ai/models-and-research/google-labs/jules/)). World tier: code + environment + processes for the task's lifetime; nothing persists but the diff/PR.

**Devin (Cognition)** is the deepest commercial world implementation: every session boots from a **VM snapshot** containing repos, tools, and dependencies ([Devin docs](https://docs.devin.ai/onboard-devin/repo-setup)), on the otterlink hypervisor with blockdiff enabling ~200 ms forking, rollback, and sleep/wake ([blockdiff blog](https://cognition.com/blog/blockdiff)). Since 2026-03-19, **managed Devins** let a coordinator Devin decompose a task and delegate to parallel workers, "each ... in its own isolated virtual machine with its own terminal, browser, and development environment"; the coordinator "scopes the work, assigns each piece, monitors progress, resolves any conflicts, and compiles the results," and can inspect workers' full work history to improve future delegation ([Cognition blog](https://cognition.com/blog/devin-can-now-manage-devins)). That is multi-world exploration with lineage-ish traces — but conflict resolution and result compilation are ad hoc coordinator judgment, not an auditable decision procedure.

**Terragon (Terragon Labs)** was a cloud orchestrator running Claude Code agents in parallel remote sandboxes; its site now serves a page titled "Terragon Shutdown" ([terragonlabs.com](https://www.terragonlabs.com)) — a datapoint that thin orchestration layers over rented sandboxes are commercially fragile.

**OpenHands** (ex-OpenDevin) splits a controller process from a per-task Docker sandbox where all shell/file/test actions execute ([runtime architecture docs](https://docs.openhands.dev/openhands/usage/architecture/runtime)); the [OpenHands Software Agent SDK](https://arxiv.org/html/2511.03690v1) (2025-11) made runtimes pluggable, and the community is adding a QEMU microVM backend for hardware-level isolation without Docker ([issue #13203](https://github.com/OpenHands/OpenHands/issues/13203)) plus hosted runtimes on Daytona ([Daytona blog](https://www.daytona.io/dotfiles/introducing-runtime-for-openhands-secure-ai-code-execution)). Container start times of 30–60 s per session are reported as the parallelism bottleneck ([Modal blog](https://modal.com/resources/best-sandbox-openhands)).

### The classification that matters: WORLD vs branch

Sorting every system by what a "candidate" actually contains:

- **Diff/branch only (code):** git worktrees, Claude Code `--worktree`, Conductor, Agent Teams.
- **Code + environment (container):** Sculptor, Dagger container-use, OpenHands (Docker runtime), Codex cloud, AgentFS (filesystem + audit log, no processes).
- **Code + environment + processes/services (VM, ephemeral):** Jules, Cursor cloud agents.
- **Full forkable world (code + env + processes + memory, snapshot-addressable):** Devin (otterlink/blockdiff), Morph Infinibranch, E2B paused sandboxes, Modal memory snapshots (alpha), Daytona VM hot snapshots, Firecracker snapshot/restore.
- **OS-native branch contexts (fs + process lifecycle, commit/abort semantics):** BranchFS/`branch()` — unique in having *transactional* semantics, but memory-less and merge-less.

Only the FEC paper gives worlds a *lifecycle with commit semantics*; only container-use and AgentFS record a *systematic trace* of what happened inside the world; only Devin/Morph-class systems can *fork a running world mid-flight*. **No system does all three.**

## Comparison table

| System | Kind | Isolation unit | Code | Env/deps | Processes/memory | Services/DB inside world | Fork *running* state | Lineage / trace recorded | Multi-candidate decision model |
|---|---|---|---|---|---|---|---|---|---|
| [Fork, Explore, Commit](https://arxiv.org/abs/2602.08199) | Paper + prototype (2026) | Branch context (FUSE fs + proc group) | Yes (CoW) | Partial (fs view only) | Processes yes; memory no (`BR_MEMORY` future) | No; external I/O not rolled back | No | Branch tree implicit; no persistent trace | **First-commit-wins**; no compose |
| Git worktree ([docs](https://git-scm.com/docs/git-worktree)) | Substrate | Working dir + branch | Yes | No | No | No | No | Git commits only | Human merge |
| [OverlayFS](https://docs.kernel.org/filesystems/overlayfs.html) | Substrate | Mount (upper/lower) | Yes (file-level CoW) | Yes (as image layers) | No | No | No | None | None |
| [Firecracker](https://firecracker-microvm.github.io) + [snapshots](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md) | Substrate (AWS OSS) | microVM | Yes | Yes | **Yes (memory+vCPU)** | Yes (in-guest) | Yes (resume clones; identity/entropy caveats) | None | None |
| [E2B](https://docs.e2b.dev/sandbox/persistence) | Sandbox provider | Firecracker microVM | Yes | Yes | **Yes (pause saves memory)** | Yes (in-guest) | Pause/resume; not N-way live fork | Logs via SDK | None |
| [Modal](https://modal.com/resources/best-stateful-sandboxes-long-running-agent-sessions) | Sandbox provider | gVisor sandbox | Yes | Yes | Memory snapshots (alpha) | Yes (in-sandbox) | Partial (alpha) | None built-in | None |
| [Daytona](https://www.daytona.io/docs/en/snapshots/) | Sandbox provider | Container or VM | Yes | Yes | VM hot snapshots (`includeMemory`) | Yes (in-sandbox) | VM class: yes | None built-in | None |
| [Morph Infinibranch](https://cloud.morph.so/docs/blog/developers) | Sandbox provider | microVM snapshot tree | Yes | Yes | **Yes (<250 ms full-VM branch)** | Yes (running services) | **Yes, N-way from live state** | Snapshot lineage tree | None (user-side) |
| [Dagger container-use](https://github.com/dagger/container-use) | OSS tool | Container + git branch | Yes | Yes | Container procs; no memory fork | Partial (in-container) | No | **Command history + auto-commits** | Human git merge |
| [AgentFS](https://www.agentfs.ai/) (Turso) | OSS tool | SQLite-backed CoW fs | Yes | No | No | No | No | **Every fs op logged to SQLite** | Human accept/reject |
| [Claude Code](https://code.claude.com/docs/en/common-workflows) worktrees / Agent Teams | Product | Git worktree per session | Yes | No | No | No | No | Git + session transcripts | Human merge; teams coordinate via messages |
| [Conductor](https://www.conductor.build/docs/guides/git-worktrees/run-claude-code-with-git-worktrees) | Product (Mac) | Git worktree per agent | Yes | No | No | No | No | Git | Human review/merge UI |
| [Sculptor](https://imbue.com/blog/containers) (Imbue) | Product (Mac) | Docker container per agent | Yes | **Yes (devcontainer)** | Container procs | Partial | No | Container + git | Human via Pairing Mode |
| [Cursor cloud agents](https://cursor.com/docs/cloud-agent) | Product | VM per agent | Yes | **Yes (snapshots/Dockerfile)** | **Yes (desktop, browser)** | Yes (in-VM) | No (snapshot for setup, not live fork) | **Demo artifacts: screenshots, video, logs** | Human PR review |
| [OpenAI Codex cloud](https://learn.chatgpt.com/docs/cloud) | Product | Container per task | Yes | Yes (setup script) | Container procs | Partial; **no internet by default** | No | Task logs cited in answer | Human review of diff+logs |
| [Google Jules](https://jules.google/docs/environment/) | Product | Ephemeral Cloud VM per task | Yes | Yes (snapshot of setup) | Yes (for task lifetime) | Yes (in-VM, Docker avail.) | No | Plan + activity feed | Human PR review |
| [Devin](https://cognition.com/blog/devin-can-now-manage-devins) + [blockdiff](https://cognition.com/blog/blockdiff) | Product | VM per (managed) Devin | Yes | Yes (VM snapshot) | **Yes (fork/rollback/sleep)** | Yes (in-VM) | **Yes (~200 ms disk snapshot)** | Work history reviewable by coordinator | **Coordinator Devin resolves conflicts (ad hoc)** |
| [OpenHands](https://docs.openhands.dev/openhands/usage/architecture/runtime) | OSS product | Docker sandbox per task (pluggable) | Yes | Yes | Container procs | Partial | No | Event stream | Human/orchestrator |

## Implications for Multiverso design

1. **Define Worlds as a declared tier, and stamp the tier into every Evidence receipt.** The market reality is three tiers — T0 branch/diff (worktree), T1 container (env), T2 VM snapshot (processes+memory+services). Evidence produced in a T0 world (tests run against shared local services) is categorically weaker than T2 evidence. No surveyed system records *under what isolation tier* its verification ran; Multiverso's receipts should make the tier a first-class, signed field, and its policy plane should let trunk admission require a minimum tier per intent class.

2. **First-commit-wins is the anti-pattern; treat FEC as the substrate, not the decision plane.** The FEC paper's race-based resolution admits whichever candidate finishes first, and explicitly cannot compose branches ([arXiv 2602.08199](https://arxiv.org/html/2602.08199v2)). Multiverso's SELECT/COMPOSE/SERIALIZE/REPAIR/REJECT vocabulary is precisely what's missing above the fork layer everywhere: Devin's coordinator "resolves conflicts" opaquely, all other products devolve to human PR review. This is Multiverso's clearest differentiation — but BranchFS-style O(1), rootless fs branching is an excellent candidate for cheap T0/T1 world materialization inside a runner.

3. **Fork-from-warm-state is solved technology; budget it, don't rebuild it.** Morph (<250 ms full-VM branch), Cognition blockdiff (~200 ms/20 GB disk snapshot), E2B pause/resume, Daytona hot snapshots, and Firecracker diff snapshots mean an evidence-aware scheduler can realistically treat "spawn one more candidate world from the post-setup snapshot" as a ~sub-second, ~zero-storage marginal action. That collapses the setup-cost asymmetry that makes fixed best-of-N wasteful, and directly enables Multiverso's dynamic reallocation between generation/testing/challenge under a fixed budget.

4. **Bind evidence to snapshot identity, and mind the clone-identity trap.** Firecracker documents that clones from one snapshot share RNG state and cryptographic tokens ([snapshot docs](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md)). For Multiverso's trust plane this cuts both ways: shared post-fork state is a reproducibility asset (identical starting worlds make differential evidence meaningful) but a signing hazard — per-world attestation keys must be injected *after* fork, never baked into the golden snapshot, and world IDs should hash (base snapshot ID, fork nonce, intent version).

5. **The trace layer to copy is container-use + AgentFS, not the big products.** Auto-committing every world mutation to a per-world git branch (container-use) and logging every operation to a portable, queryable file (AgentFS) are the two existing mechanisms closest to Multiverso's evidence-native lineage. A Multiverso world runner should do both: git-anchored state lineage for Git compatibility, plus an append-only operation log that the Attestation signs.

6. **External effects are the open frontier — adopt effect gating as policy, not mechanism.** No system rolls back network calls, third-party API writes, or shared-database mutations; FEC names "effect gating" as future work. Near-term, Multiverso should require worlds to *declare* their effect boundary (hermetic / read-only egress / gated egress), enforce it at the sandbox network layer (as Codex does with default-off internet), and mark any evidence produced by worlds with undeclared external effects as tainted.

## Open questions

- **Can compose be made transactional?** FEC proves single-winner atomic commit at OS level; composing K partial winners (Multiverso's COMPOSE) has no substrate support anywhere — is compose necessarily a new world (re-run evidence) or can disjoint delta layers be merged with evidence carried over under a proof of non-interference?
- **What is the minimal world for trustworthy evidence?** Is T1 (container) sufficient for most test oracles if services are declared and pinned, reserving T2 (VM memory fork) for stateful/adversarial challenges? This is an empirical question Multiverso's benchmark should answer per oracle class.
- **Fork topology under budget:** Morph-style mid-trajectory forking enables branching at *interesting states* rather than only at intent start. When should the scheduler spend budget on deep forks (exploit) vs fresh worlds (explore)? No published system schedules this; all use fixed, user-chosen N.
- **Lineage across substrate boundaries:** a candidate may start as a worktree (T0), escalate to a container (T1), then to a VM fork (T2) for final validation. What does a verifiable identity chain across substrate migrations look like — and can evidence from lower tiers be "promoted" with discounts rather than discarded?
- **Post-shutdown durability of worlds:** Terragon's shutdown and per-provider snapshot formats (blockdiff, Infinibranch, E2B) raise the question of a portable world-state interchange format; today only AgentFS's single-file SQLite session and OCI images are portable, and neither captures memory.

## Sources

- Fork, Explore, Commit: OS Primitives for Agentic Exploration - https://arxiv.org/abs/2602.08199 - submitted 2026-02-09, v2 2026-03-19
- Fork, Explore, Commit (full text, v2) - https://arxiv.org/html/2602.08199v2 - 2026-03-19
- Firecracker microVM (official site) - https://firecracker-microvm.github.io - accessed 2026-08-12
- Firecracker snapshot support docs - https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/snapshot-support.md - accessed 2026-08-12
- OverlayFS kernel documentation - https://docs.kernel.org/filesystems/overlayfs.html - accessed 2026-08-12
- git-worktree documentation - https://git-scm.com/docs/git-worktree - accessed 2026-08-12
- Blockdiff: How we built our own file format for VM disk snapshots (Cognition) - https://cognition.com/blog/blockdiff - 2025
- blockdiff (GitHub, open source) - https://github.com/CognitionAI/blockdiff - 2025
- Devin can now Manage Devins (Cognition) - https://cognition.com/blog/devin-can-now-manage-devins - 2026-03-19
- Devin docs: Classic configuration / VM snapshots - https://docs.devin.ai/onboard-devin/repo-setup - accessed 2026-08-12
- Introducing Infinibranch (Morph Cloud) - https://cloud.morph.so/docs/blog/developers - 2024-11-19
- Morph - https://www.morph.so/ - accessed 2026-08-12
- E2B - https://e2b.dev/ - accessed 2026-08-12
- E2B sandbox persistence docs - https://docs.e2b.dev/sandbox/persistence - accessed 2026-08-12
- Modal: Best stateful sandboxes for long-running agent sessions - https://modal.com/resources/best-stateful-sandboxes-long-running-agent-sessions - 2026
- Behind the scenes of Modal sandboxes (Amplify Partners) - https://www.amplifypartners.com/blog-posts/behind-the-scenes-of-modal-sandboxes - 2025
- Daytona snapshots documentation - https://www.daytona.io/docs/en/snapshots/ - accessed 2026-08-12
- container-use (Dagger, GitHub) - https://github.com/dagger/container-use - accessed 2026-08-12
- Containing Agent Chaos (Dagger blog) - https://dagger.io/blog/agent-container-use/ - 2025
- AgentFS (Turso) - https://www.agentfs.ai/ - accessed 2026-08-12
- Claude Code docs: Common workflows (parallel sessions with worktrees, subagents, background agents) - https://code.claude.com/docs/en/common-workflows - accessed 2026-08-12
- Claude Code Agent Teams, Subagents, and MCP: The 2026 Playbook (Developers Digest) - https://www.developersdigest.tech/blog/claude-code-agent-teams-subagents-2026 - 2026
- Git Worktrees + Claude Code: The 2026 Playbook (Developers Digest) - https://www.developersdigest.tech/blog/git-worktrees-claude-code-parallel-agents-guide - 2026
- Claude Code Agents in 2026 (CloudZero) - https://www.cloudzero.com/blog/claude-code-agents/ - 2026
- How to Run Parallel AI Coding Agents With Git Worktrees (MindStudio) - https://www.mindstudio.ai/blog/parallel-ai-coding-agents-git-worktrees - 2026
- Conductor docs: Run Claude Code with Git worktrees - https://www.conductor.build/docs/guides/git-worktrees/run-claude-code-with-git-worktrees - accessed 2026-08-12
- Conductor (Product Hunt) - https://www.producthunt.com/products/conductor-aa77ddef-e6d3-4805-a179-7b2e17b6e22e - 2025
- Sculptor: The missing UI for coding agents (Imbue) - https://imbue.com/sculptor/ - accessed 2026-08-12
- How we made sandboxed coding agents 10x faster to start (Imbue) - https://imbue.com/blog/containers - 2025-11-17
- Sculptor docs: Containers - https://docs.imbue.com/features/containers - accessed 2026-08-12
- Cursor docs: Cloud Agents - https://cursor.com/docs/cloud-agent - accessed 2026-08-12
- Run cloud agents in your own infrastructure (Cursor blog) - https://cursor.com/blog/self-hosted-cloud-agents - 2026
- Introducing Codex (OpenAI) - https://openai.com/index/introducing-codex/ - 2025-05
- Codex cloud documentation - https://learn.chatgpt.com/docs/cloud - accessed 2026-08-12
- Codex agent approvals & security - https://developers.openai.com/codex/agent-approvals-security - accessed 2026-08-12
- Jules environment documentation - https://jules.google/docs/environment/ - accessed 2026-08-12
- Jules: Google's autonomous AI coding agent (Google blog) - https://blog.google/innovation-and-ai/models-and-research/google-labs/jules/ - 2025
- Terragon Labs (site now titled "Terragon Shutdown") - https://www.terragonlabs.com - accessed 2026-08-12
- OpenHands runtime architecture docs - https://docs.openhands.dev/openhands/usage/architecture/runtime - accessed 2026-08-12
- The OpenHands Software Agent SDK (arXiv) - https://arxiv.org/html/2511.03690v1 - 2025-11
- OpenHands issue #13203: QEMU microVM runtime backend - https://github.com/OpenHands/OpenHands/issues/13203 - 2026
- Introducing Runtime for OpenHands (Daytona) - https://www.daytona.io/dotfiles/introducing-runtime-for-openhands-secure-ai-code-execution - 2025
- Best Code Execution Sandbox for OpenHands (Modal) - https://modal.com/resources/best-sandbox-openhands - 2026
