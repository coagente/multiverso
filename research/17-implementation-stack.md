# 17. Implementation Stack for the Control Plane

> Part of the Multiverso research corpus - https://github.com/coagente/multiverso - Cutoff: 2026-08-12

## Why this matters for Multiverso

Multiverso's control plane is four programs that must ship as one coherent artifact: a **CLI** (`mv`), a **local daemon** (world orchestration, evidence scheduler), a **ledger** (append-only, hash-chained record of intents, receipts, decisions), and a **CAS** (content-addressed store binding evidence to exact world state). The corpus already fixed two constraints that dominate the language choice: (1) the defensible wedge is the *vendor-neutral evidence-native admission layer* — "Sigstore/SLSA for agent changes" ([ch. 10](10-competitive-landscape.md)) — so the stack must natively speak DSSE, in-toto attestations, Sigstore, and TUF; and (2) the VCS strategy is **CLI adapters over library linkage** for both git and jj, because jj-lib has no stable API ([ch. 4](04-vcs-substrate.md)). A third, unusual constraint comes from the build plan itself: most of this code will be written by coding agents supervised by a small team, so "which language do frontier agents produce most reliably, and which language is cheapest for humans to review" is a first-order engineering criterion, not a footnote.

## State of the art

### (a) Supply-chain and trust libraries: Go is the home field

The evidence layer Multiverso must emit (DSSE envelopes carrying in-toto v1 attestations, signed via Sigstore, roots distributed via TUF) has one ecosystem where every piece is production-grade, and it is Go.

- **Sigstore clients.** [sigstore-go](https://github.com/sigstore/sigstore-go) is "considered stable and ready for production use" and "passes the `sigstore-conformance` signing and verification test suite"; it requires Go ≥1.23 and deliberately scopes down to bundle signing/verification, Rekor, TSA, and TUF-fetched trust roots. [sigstore-python](https://github.com/sigstore/sigstore-python) is mature (Python ≥3.10, DSSE attestations via `attest`, Rekor v2 support, SLSA Build L3 provenance for its own releases). [sigstore-rs](https://github.com/sigstore/sigstore-rs), by contrast, states it "will not be considered stable until the 1.0 release" and — critically for Multiverso — "does not handle verification of attestations yet." The official [language-client directory](https://docs.sigstore.dev/language_clients/language_client_overview/) lists Go/Java/JS/Python/Ruby/Rust, but only the Go and Python clients pair maturity with attestation support.
- **in-toto.** The [in-toto Attestation Framework](https://github.com/in-toto/attestation) (spec v1) ships protobuf-generated bindings for Go, Python, Java, and Rust, with Go the most mature; ch. 7's mandate to *mint a new predicate type* for agent-change evidence lands here. The legacy [in-toto-golang](https://github.com/in-toto/in-toto-golang) is verification-focused (admission controllers, SPIFFE/ITE-7); [in-toto-rs](https://github.com/in-toto/in-toto-rs) warns it "may not [be] suitable for production use… the API is unstable and you should be prepared to refactor on even patch releases." The strongest proof that the whole chain works in Go is [witness](https://witness.dev/docs/)/[go-witness](https://github.com/in-toto/go-witness): a CNCF in-toto project that already does attestation collection → DSSE signing (keyed, Fulcio keyless) → OPA/Rego policy verification, in Go, in production CI/CD.
- **DSSE.** Go has the reference-quality [`go-securesystemslib/dsse`](https://pkg.go.dev/github.com/secure-systems-lab/go-securesystemslib/dsse) implementing the [DSSE spec](https://github.com/secure-systems-lab/dsse) (multi-signer envelopes, PAE encoding); Python's `securesystemslib` and sigstore-python cover it; Rust and TS coverage exists but rides on the less-mature clients above.
- **TUF.** [go-tuf v2](https://github.com/theupdateframework/go-tuf) is the official Go implementation — the donated `go-tuf-metadata` codebase, deliberately modeled on [python-tuf](https://github.com/theupdateframework/python-tuf)'s Metadata API + Updater design — and is what sigstore-go uses for trust-root refresh.
- **Transparency log to reuse, not rebuild.** Sigstore's [Rekor v2 went GA](https://blog.sigstore.dev/rekor-v2-ga/) as a tile-based log on [Tessera](https://github.com/transparency-dev/tessera) (tlog-tiles: immutable content-addressed tiles, CDN-cacheable), including a **`rekor-server-posix`** binary with no cloud dependencies. If Multiverso's shared ledger ever needs to become a verifiable log for a team, embedding Tessera's POSIX driver (Go) is a weekend, not a quarter.

**Verdict (a):** Go ≥ Python ≫ TypeScript > Rust. Sigstore, Rekor, Fulcio, cosign, witness, and Tessera are themselves Go programs — choosing Go means Multiverso can *import* the admission-evidence industry instead of re-implementing it.

### (b) Git manipulation: shell out; the libraries are not ready for worktree-heavy work

Multiverso's world model is worktree-heavy (N isolated candidate checkouts) and merge-heavy (COMPOSE) — exactly the corners where embedded Git libraries are weakest.

- **go-git** (used by Kubernetes Prow and Flux, [README](https://github.com/go-git/go-git)) documents in [COMPATIBILITY.md](https://github.com/go-git/go-git/blob/main/COMPATIBILITY.md): worktree support **partial** ("creation and opening of linked worktrees via the `x/plumbing/worktree` package. Not all flags or subcommands are supported"), merge **partial ("fast-forward only")**, rebase **not supported**, cherry-pick partial. The stable line is still v5.x; [v6 remains in alpha](https://github.com/go-git/go-git/releases) as of the cutoff. Reading refs/objects: fine. Driving Multiverso's world lifecycle: not fine.
- **gitoxide (`gix`)** is the most promising pure implementation: clone/fetch/push/commit/status/merge (blob/tree/commit) and worktree *checkout* exist, but only `gix-lock` and `gix-tempfile` are marked production-grade, and the top-level `gix` crate is "usable… possibly incomplete functionality" ([README](https://github.com/GitoxideLabs/gitoxide)). It is real enough for production use at GitButler — which is migrating to "gitoxide being the primary solution and git2 only being used where gitoxide is lacking a feature" ([discussion](https://github.com/GitoxideLabs/gitoxide/discussions/1375)) — but that migration itself shows the API churn cost. libgit2 bindings (git2-rs, pygit2) are mature but libgit2 lags git itself, which is why GitButler is leaving it.
- **jj.** [jj v0.44.0 shipped 2026-08-05](https://github.com/jj-vcs/jj/blob/main/CHANGELOG.md); jj-lib remains an ["experimental version control system"](https://crates.io/crates/jj-lib) crate with no API-stability guarantee and MSRV bumps nearly every release (1.88 → 1.89 within two versions) — reaffirming ch. 4's CLI-adapter conclusion. The headline for Multiverso: **jj 0.43.0 (2026-07-01) introduced `jj run`** — "run a command over a set of changes, each with their own private working copy," with 0.44.0 adding `--ignore-errors`/`--passthrough` ([CHANGELOG](https://github.com/jj-vcs/jj/blob/main/CHANGELOG.md)). That is a working prototype of Multiverso's "execute oracle across N worlds" primitive, maintained by someone else. Adapters should treat it as an optional fast path, not a dependency.
- **Shelling out** to `git` (and `jj` when present) costs ~10ms per invocation and pays back total fidelity: worktrees (`git worktree add`), merge strategies (`-s ort`, `merge-tree` for in-index merges without checkout), sparse checkout, partial clone, signing — everything current, nothing reimplemented. This is how most production CI/CD and agent tooling already drives git.

**Verdict (b):** subprocess adapters over `git` (mandatory) and `jj` (optional accelerator), with a thin typed wrapper; use go-git/gix only for *read-only* fast paths (ref listing, object reads) if profiling demands it.

### (c) Ledger: SQLite in WAL mode, hash-chained, with an escape hatch to a real transparency log

SQLite is the industry default for local-first tools for documented reasons: it is a single-file, crash-safe, queryable application file format ([SQLite: appropriate uses / application file format](https://sqlite.org/appfileformat.html)), and in [WAL mode](https://sqlite.org/wal.html) "readers do not block writers and a writer does not block readers" while "there can only be one writer at a time" — precisely the daemon-writes/CLI-reads shape of Multiverso's ledger. The one hard WAL constraint — "all processes using a database must be on the same host computer; WAL does not work over a network filesystem" ([WAL docs](https://sqlite.org/wal.html)) — is compatible with a local daemon and rules out naive NFS sharing, which Multiverso shouldn't do anyway.

- **Pattern.** Event-sourced, append-only `events(seq INTEGER PRIMARY KEY, ts, kind, world_id, payload_json, payload_sha256, prev_hash, this_hash)` where `this_hash = SHA-256(prev_hash || canonical(payload))`. Enforce append-only with triggers (`BEFORE UPDATE/DELETE … RAISE(ABORT)`); derive all mutable views (world status, budget ledgers) as projections. This gives tamper-evidence locally; *tamper-proofing* for teams comes from periodically checkpointing the head hash into a Rekor/Tessera log (see (a)) rather than building consensus into the tool.
- **Durability/replication.** [Litestream v0.5.0](https://github.com/benbjohnson/litestream/releases) (Oct 2025) rearchitected streaming SQLite backup around the LTX format with hierarchical compaction ([analysis](https://simonwillison.net/2025/Oct/3/litestream/)), and its VFS became writable in Feb 2026 ([Fly.io](https://fly.io/blog/litestream-vfs/)) — continuous off-host backup of the ledger is a sidecar, zero code.
- **Drivers.** The historical Go objection (CGo) is gone: [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) is a CGo-free transpilation of SQLite, at v1.56.0 (2026-08-03) tracking SQLite 3.53.3 across darwin/linux/windows on amd64+arm64, with ~3.5k importing packages. Rust has rusqlite (bundled SQLite); TS has `bun:sqlite`/better-sqlite3; Python has stdlib `sqlite3`.
- **Embedded KV alternatives.** [bbolt](https://github.com/etcd-io/bbolt) (Go, B+tree, "one read-write transaction at a time," stable/fixed format, used by etcd, Consul, containerd) and [redb](https://github.com/cberner/redb) (Rust, "stable and maintained," MVCC, stable file format) are excellent — but they surrender SQL queryability, which the scheduler (evidence value-of-information queries, budget accounting) and humans (`sqlite3 ledger.db 'select …'`) both want. They are the fallback if profiling ever shows SQLite write amplification matters; it will not at MVP scale.

**Verdict (c):** SQLite/WAL as the only ledger at MVP; hash-chain in the schema; Litestream for backup; Tessera/Rekor for the future shared verifiable log. Do not use a KV store; do not build a bespoke log format.

### (d) Execution: official Docker SDKs are Go and Python; everything else is community

Multiverso runs oracles in isolated worlds and must record the isolation tier in every receipt (ch. 1).

- **Engine API.** Docker's [official SDKs are Go and Python](https://docs.docker.com/reference/api/engine/sdk/); Node's dockerode and Rust's [bollard](https://github.com/fussybeaver/bollard) (v0.21.0, Jun 2026; async, Docker+Podman, API version negotiation — [crates.io](https://crates.io/crates/bollard)) are explicitly "community supported… They haven't been tested by Docker." The Go SDK is the same client the Docker CLI itself uses.
- **Resource caps.** Container-level CPU/memory/pids limits are first-class in the Engine API/`HostConfig` ([resource constraints](https://docs.docker.com/engine/containers/resource_constraints/)); this is also the honest answer to "cgroups on macOS": there are none — cgroup enforcement happens inside the Linux VM that Docker Desktop/colima run, so **standardizing on containers as the default isolation tier** gives uniform caps on macOS and Linux. For rare host-native runs on Linux, `systemd-run --user --scope -p MemoryMax=… -p CPUQuota=…` is the supervision path; receipts must record `isolation: none` for bare host runs.
- **Dev environments.** The [devcontainer CLI](https://github.com/devcontainers/cli) is the reference implementation of the Dev Container spec — TypeScript, distributed via npm (`@devcontainers/cli`), driven entirely through subcommands (`up`, `build`, `exec`, `read-configuration`) — i.e., it is *already* a subprocess adapter regardless of Multiverso's language, and it lets Multiverso inherit a repo's existing `devcontainer.json` as the world image definition.
- **Testcontainers** officially supports Java, Go, .NET, Node.js, Python, and Rust against "a Docker-API compatible container runtime" ([docs](https://testcontainers.com/getting-started/)) — useful for Multiverso's *own* integration tests more than for its runtime.

**Verdict (d):** Go SDK for the daemon's container control; devcontainer CLI as an optional subprocess for repo-defined environments; containers as the default (and only cross-OS-uniform) resource-capped tier.

### (e) Distribution: Go and Rust ship single binaries; TS and Python do not, cleanly

- **Go**: `go build` yields a static-ish single binary per GOOS/GOARCH; [GoReleaser v2.17.1](https://github.com/goreleaser/goreleaser/releases) (Jul 2026) automates archives, Homebrew, winget, nixpkgs, Docker images, signing — and since [v2.5](https://goreleaser.com/blog/goreleaser-v2.5/)/[v2.6](https://goreleaser.com/blog/goreleaser-v2.6/) also builds Rust, Zig, Bun, Deno, and Python projects, so the release pipeline choice does not lock the language.
- **Rust**: [cargo-dist](https://github.com/axodotdev/cargo-dist/releases) is alive (v0.32.0, May 2026; Astral's fork was merged back in 0.29) and remains the best-in-class installer generator.
- **TypeScript**: [`bun build --compile`](https://bun.com/docs/bundler/executables) cross-compiles single-file executables for darwin/linux/windows ×(x64, arm64) — but binaries embed the Bun runtime at roughly 50–100MB, and Node SEA remains awkward. Workable, heavy.
- **Python**: no credible single-binary story (PyInstaller bundles are fragile across OS versions); uv mitigates *developer* installs, not end-user CLI distribution.
- **Plugins**: for oracle/evidence plugins, avoid in-process loading entirely (Go's `plugin` package is platform-limited; dylibs complicate attestation of what actually ran). The battle-tested pattern is subprocess RPC plugins — [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin) (Terraform/Vault's mechanism) — which also lets receipts hash the exact plugin binary. The instructive precedent: OpenAI **rewrote Codex CLI from TypeScript to Rust** citing Node as a hard install dependency, GC pauses in a long-running agent process, and native sandbox API access ([InfoQ](https://www.infoq.com/news/2025/06/codex-cli-rust-native-rewrite)) — the daemon-shaped half of that argument (small heap, no runtime dependency) applies equally to Go; the GC-pause half does not bite a control plane whose hot loop is orchestrating subprocesses.

### (f) Team-fit: what the evals actually say about agent-written languages

The honest reading of published per-language evals is *noisy and benchmark-dependent*, and none of them crowns a single "most reliable agent language":

- [SWE-bench Multilingual](https://www.swebench.com/multilingual.html) (300 tasks, 42 repos, 9 languages; Claude 3.7 Sonnet + SWE-agent, 43% overall): **Rust highest at 58.14%**, Java 53.49%, PHP 48.84%, Ruby 43.18%, JS/TS 34.88%, **Go 30.95%**, C/C++ 28.57% — with the authors noting difficulty distribution within a language doesn't obviously explain the gaps.
- [Multi-SWE-bench](https://arxiv.org/abs/2504.02605) (NeurIPS 2025; 2,132 instances, 68 annotators): the same frontier models that resolve ~45–52% of Python instances drop to Java 23.44%, Rust 15.90%, C++ 14.73%, TS 11.61%, Go 7.48%, JS 5.06% (best model+scaffold per language) — "strong performance in resolving Python issues but struggle to generalize effectively across other languages."
- [SWE-Bench ProMax](https://arxiv.org/abs/2608.09802) (COLM 2026; 170 large multilingual refactoring instances averaging 11.4 files): best model 41.2% overall, confirming large multi-file work is the frontier regardless of language.

Two conclusions survive the noise. First, **repo/task selection dominates language effects** — Go is bottom-tier in one benchmark and mid-tier in another — so no eval justifies picking a language *for* agent resolve-rate alone. Second, what demonstrably helps agents is a **fast, deterministic, high-signal feedback loop**: compile errors, formatters with one canonical style, and strict linters. Go and Rust both provide it; Go adds near-instant builds, `gofmt`'s single style, `go vet`/`golangci-lint`/`-race` as cheap oracles, and a deliberately small language surface — which is also what makes agent-generated Go cheap for a small human team to *review*, the actual bottleneck in an agent-built codebase (ch. 13's untrusted-generator framing applies to Multiverso's own development). Frontier agent CLIs split the same way the tradeoffs do: Claude Code and [Gemini CLI](https://github.com/google-gemini/gemini-cli) are TypeScript; Codex went Rust for daemon-like properties ([InfoQ](https://www.infoq.com/news/2025/06/codex-cli-rust-native-rewrite)).

## Comparison table

| Axis | Go | Rust | TypeScript | Python |
|---|---|---|---|---|
| Sigstore client | **stable, conformance-passing** ([sigstore-go](https://github.com/sigstore/sigstore-go)) | pre-1.0, "does not handle verification of attestations yet" ([sigstore-rs](https://github.com/sigstore/sigstore-rs)) | sigstore-js (npm provenance) | mature, DSSE attest, Rekor v2 ([sigstore-python](https://github.com/sigstore/sigstore-python)) |
| in-toto / DSSE | attestation bindings most mature + [go-witness](https://github.com/in-toto/go-witness) + [go-securesystemslib/dsse](https://pkg.go.dev/github.com/secure-systems-lab/go-securesystemslib/dsse) | [in-toto-rs](https://github.com/in-toto/in-toto-rs): "not suitable for production use" | thin | reference implementation |
| TUF | official [go-tuf v2](https://github.com/theupdateframework/go-tuf) | rust-tuf/tough (AWS-scoped) | tuf-js | reference [python-tuf](https://github.com/theupdateframework/python-tuf) |
| Git story | shell out; go-git worktree **partial**, merge FF-only ([COMPATIBILITY](https://github.com/go-git/go-git/blob/main/COMPATIBILITY.md)) | shell out; gix promising but churning ([gitoxide](https://github.com/GitoxideLabs/gitoxide)); jj-lib unstable | shell out (isomorphic-git inadequate) | shell out (pygit2 OK read-only) |
| SQLite | CGo-free [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) v1.56.0 | rusqlite (bundled) | bun:sqlite / better-sqlite3 | stdlib |
| Docker SDK | **official** ([docs](https://docs.docker.com/reference/api/engine/sdk/)) | community [bollard](https://github.com/fussybeaver/bollard) | community dockerode | **official** |
| Single binary | native + [GoReleaser](https://github.com/goreleaser/goreleaser/releases) | native + [cargo-dist](https://github.com/axodotdev/cargo-dist/releases) | 50–100MB via [bun compile](https://bun.com/docs/bundler/executables) | none credible |
| Agent evals | 7.5–31% (benchmark-dependent) | 16–58% (benchmark-dependent) | 12–35% | 45–52% (best-served) |
| Verdict | **primary** | runner-up (jj-lib adjacency, weakest trust libs) | UI/adapters only | prototyping/eval harness only |

## Recommendation for the PRD

**Language: Go, for the entire control plane (CLI + daemon + ledger + CAS).** The decisive axis is (a): Multiverso's product *is* evidence-native admission, and the DSSE/in-toto/Sigstore/TUF/transparency-log stack is production-grade in Go and nowhere else at once ([sigstore-go](https://github.com/sigstore/sigstore-go), [go-witness](https://github.com/in-toto/go-witness), [go-tuf v2](https://github.com/theupdateframework/go-tuf), [Tessera](https://github.com/transparency-dev/tessera)). Axes (d) and (e) reinforce it (official Docker SDK, trivial cross-compiled single binaries); axis (b) is language-neutral by design (subprocess adapters); axis (f) is a wash on evals and a win on review cost. Rust is the runner-up — it would buy jj-lib adjacency Multiverso has already decided not to use (ch. 4) at the price of reimplementing attestation verification ([sigstore-rs gap](https://github.com/sigstore/sigstore-rs)).

**Pinned starting stack** (versions as of 2026-08-12): Go ≥1.24; `sigstore-go` (signing/verify, TUF trust roots); `github.com/in-toto/attestation` Go bindings + `go-securesystemslib/dsse` (mint the `AgentChangeEvidence` predicate from ch. 7 as an in-toto v1 Statement); `modernc.org/sqlite` v1.56.x in WAL mode; official Docker Engine Go SDK; `hashicorp/go-plugin` for oracle plugins; GoReleaser v2.17.x for macOS/Linux (arm64+amd64) releases; `git` ≥2.44 required on PATH, `jj` ≥0.44 optional ([`jj run`](https://github.com/jj-vcs/jj/blob/main/CHANGELOG.md) fast path).

**Storage layout — Git ODB as the world CAS, files+SQLite as the evidence CAS:**

```
<repo>/.git/                      # worlds ARE git objects (commits/trees), pinned by
  refs/multiverso/worlds/<wid>    #   hidden refs so gc never collects candidates
  refs/multiverso/decisions/<id>  # admission commit + attestation ref
<repo>/.multiverso/               # gitignored control-plane state
  ledger.db (+ -wal)              # SQLite WAL: events (hash-chained), projections
  cas/sha256/ab/cd/<digest>       # evidence payloads (logs, junit, coverage), zstd
  attestations/<digest>.json      # DSSE envelopes (also indexed in ledger.db)
```

Rationale: candidate state already lives in Git's content-addressed object store — reuse it and pin with `refs/multiverso/*` ([Git internals](https://git-scm.com/book/en/v2/Git-Internals-Git-Objects)). But do **not** put evidence payloads in the ODB: git's primary hash remains SHA-1 in practice (the [SHA-256 transition](https://git-scm.com/docs/hash-function-transition) is still incomplete ecosystem-wide), receipts must be SHA-256, and megabytes of logs would travel on every push. In-toto v1 ResourceDescriptors carry both `gitCommit` and `sha256` digests, binding the two stores in one statement. The attestation for an ADMIT is committed as a note-like ref plus mirrored to the ledger, so `git clone` + `mv verify` reproduces the decision.

**What NOT to build** (each is a purchased/reused capability): a Git implementation (shell out; go-git can't do linked-worktree lifecycles — [COMPATIBILITY](https://github.com/go-git/go-git/blob/main/COMPATIBILITY.md)); a jj integration via jj-lib (CLI adapter; adopt `jj run` semantics); envelope/signature formats (DSSE + in-toto v1 + Sigstore bundles, verbatim); a transparency log (Rekor v2 / Tessera POSIX when multi-user arrives — [GA post](https://blog.sigstore.dev/rekor-v2-ga/)); a container runtime or devcontainer parser (Engine API + [`@devcontainers/cli`](https://github.com/devcontainers/cli) subprocess); backup/replication (Litestream sidecar); release engineering (GoReleaser). The only novel storage code Multiverso writes is the hash-chained event schema and the evidence CAS indexer — roughly 1–2k lines.

**Agent-built-codebase guardrails to write into the PRD:** repo-wide `gofmt`+`golangci-lint`+`go vet`+`-race` as mandatory pre-commit oracles (agents get deterministic, machine-readable failure signals); small-interface architecture (each subsystem behind one Go interface so agents work in bounded contexts); and the control plane should dogfood itself — its own PRs are intents with receipts — as soon as the Candidate Race MVP runs.

## Open questions

1. **`jj run` semantics vs Multiverso worlds:** `jj run` gives each change a private working copy ([CHANGELOG](https://github.com/jj-vcs/jj/blob/main/CHANGELOG.md)) but not the container isolation tier receipts require — is it a tier-0 fast path only, or can it host tier-1 (container-per-change) execution?
2. **Ref namespace pollution:** hundreds of `refs/multiverso/worlds/*` refs may confuse hosting UIs and slow `git fetch` if pushed; the PRD must specify push-exclusion (negative refspecs) and a GC policy for rejected worlds.
3. **SHA-256 repos:** if a customer runs a `sha256` object-format repo, dual-digest ResourceDescriptors need testing; ecosystem interop for SHA-256 Git remains incomplete ([transition doc](https://git-scm.com/docs/hash-function-transition)).
4. **Windows:** the corpus and this chapter target macOS/Linux; WSL2 likely suffices at MVP but is unvalidated (Docker Desktop + case-insensitive FS edge cases).
5. **Agent-eval gap:** no published eval measures agent reliability *writing Go daemons/CLIs specifically* (all are issue-fix benchmarks on existing repos); an internal eval — same PRD task, N agents, Go vs Rust vs TS — during week 1 of development would be cheap and would either confirm or veto this chapter's team-fit reasoning. (unverified — no such published study found as of cutoff)
6. **modernc.org/sqlite fidelity:** the CGo-free driver pins a fragile `modernc.org/libc` version pairing ([pkg.go.dev](https://pkg.go.dev/modernc.org/sqlite)); the fallback (mattn/go-sqlite3 + zig cc cross-compilation) should be a documented contingency, not a surprise.

## Sources

- Sigstore language clients directory — https://docs.sigstore.dev/language_clients/language_client_overview/
- sigstore-go (stable, conformance-passing) — https://github.com/sigstore/sigstore-go
- sigstore-rs (pre-1.0, no attestation verification) — https://github.com/sigstore/sigstore-rs
- sigstore-python — https://github.com/sigstore/sigstore-python
- in-toto Attestation Framework (spec v1, multi-language bindings) — https://github.com/in-toto/attestation
- in-toto-golang — https://github.com/in-toto/in-toto-golang
- in-toto-rs (unstable API warning) — https://github.com/in-toto/in-toto-rs
- witness / go-witness — https://witness.dev/docs/ · https://github.com/in-toto/go-witness
- DSSE spec / go-securesystemslib — https://github.com/secure-systems-lab/dsse · https://pkg.go.dev/github.com/secure-systems-lab/go-securesystemslib/dsse
- go-tuf v2 — https://github.com/theupdateframework/go-tuf · python-tuf — https://github.com/theupdateframework/python-tuf
- Rekor v2 GA (Tessera, posix binary) — https://blog.sigstore.dev/rekor-v2-ga/ · Tessera — https://github.com/transparency-dev/tessera
- go-git compatibility matrix — https://github.com/go-git/go-git/blob/main/COMPATIBILITY.md · releases — https://github.com/go-git/go-git/releases
- gitoxide — https://github.com/GitoxideLabs/gitoxide · GitButler adoption — https://github.com/GitoxideLabs/gitoxide/discussions/1375
- jj CHANGELOG (v0.44.0, `jj run`) — https://github.com/jj-vcs/jj/blob/main/CHANGELOG.md · jj-lib — https://crates.io/crates/jj-lib
- SQLite WAL — https://sqlite.org/wal.html · application file format — https://sqlite.org/appfileformat.html
- Litestream v0.5.0 — https://github.com/benbjohnson/litestream/releases · https://simonwillison.net/2025/Oct/3/litestream/ · VFS — https://fly.io/blog/litestream-vfs/
- modernc.org/sqlite (CGo-free) — https://pkg.go.dev/modernc.org/sqlite
- bbolt — https://github.com/etcd-io/bbolt · redb — https://github.com/cberner/redb
- Docker Engine SDKs (official: Go, Python) — https://docs.docker.com/reference/api/engine/sdk/ · resource constraints — https://docs.docker.com/engine/containers/resource_constraints/
- bollard — https://github.com/fussybeaver/bollard · https://crates.io/crates/bollard
- devcontainer CLI — https://github.com/devcontainers/cli · Testcontainers — https://testcontainers.com/getting-started/
- GoReleaser releases — https://github.com/goreleaser/goreleaser/releases · multi-language: https://goreleaser.com/blog/goreleaser-v2.5/ · https://goreleaser.com/blog/goreleaser-v2.6/
- cargo-dist — https://github.com/axodotdev/cargo-dist/releases
- Bun single-file executables — https://bun.com/docs/bundler/executables
- Codex CLI Rust rewrite — https://www.infoq.com/news/2025/06/codex-cli-rust-native-rewrite
- SWE-bench Multilingual — https://www.swebench.com/multilingual.html
- Multi-SWE-bench — https://arxiv.org/abs/2504.02605
- SWE-Bench ProMax — https://arxiv.org/abs/2608.09802
- hashicorp/go-plugin — https://github.com/hashicorp/go-plugin
- Git internals (object store) — https://git-scm.com/book/en/v2/Git-Internals-Git-Objects · SHA-256 transition — https://git-scm.com/docs/hash-function-transition
- Gemini CLI (TypeScript) — https://github.com/google-gemini/gemini-cli
