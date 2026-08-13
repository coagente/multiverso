# M1c — T1 Containers & Parallel Race: Design & Contracts

> Implements [PRD](../../PRD.md) **XP-1** (the `ExecutionBackend` interface; T1 container backend; tier recorded in every world and receipt), **XP-2** (per-world resource caps; kill+record on breach), **XP-3** (env digest with container image digest), **NFR-2** (overhead), **NFR-4** (no default network egress in T1), and **parallel world execution** in `mvo race` (the CP-2 orchestration surface M1b left sequential). **XP-4 (warm-pool container reuse) is NOT implemented** — it is [v1] in the PRD and is deferred here with the interface shaped so a pool can arrive without contract change (decision 19). Builds on [M0](M0.md), [M1a](M1a-admit-signing.md), and [M1b](M1b-agent-adapters.md); every prior contract stays in force unless amended here. Exit criterion: `scripts/accept.sh` passes end-to-end, including a `--parallel 2` race whose decision type and winner match the serial run, and a docker-gated T1 step that skips gracefully when no daemon is reachable.
>
> Everything here is v0 and may break until M1 exit. Requirement IDs (XP-x, NFR-x…) refer to the PRD. **Stdlib only; `go.mod`/`go.sum` untouched.** Container work shells out to the `docker` CLI — a deliberate, documented deviation from PRD §9's "official Docker Go SDK" line: the SDK drags a large dependency tree into a codebase whose entire execution plane is thin shell-out adapters (the gitx precedent), and `go.mod` staying untouched is a repo invariant. Nothing here precludes swapping `dockerx` internals for the SDK later; `internal/backend` is the stable surface. Docker-gated tests **skip** when no daemon is reachable (CI has no docker) and never pull large images (one fixture image, built locally FROM `python:3.12-alpine`). Tests never invoke real agent CLIs (M1b rule; the fake fixtures now also ride inside the fixture image).

## Module layout (delta over M1b)

```
internal/backend/            ExecutionBackend contract (XP-1)
internal/backend/backend.go  Backend/World interfaces, New(), HostDir(), Config
internal/backend/t0.go       T0-worktree backend: M1b behavior behind the interface
internal/backend/t1.go       T1-container backend: world lifecycle over dockerx
internal/backend/env.go      env manifests (XP-3): T0 (byte-identical to M1b) + T1
internal/dockerx/            docker CLI shell-out (gitx pattern): daemon probe, image
                             resolve/pin, keeper run, exec/kill/rm, pure argv builders
testdata/t1image/Dockerfile  fixture image: python:3.12-alpine + bash + pytest + the
                             fake agent fixtures baked into /usr/local/bin
```

Amended (never rewritten): `internal/object` gains `IsolationCaps`, `Execution.IsolationCaps`, and tier/network constants; `internal/oracle` `Run` takes the world handle; `internal/agent` `RunSpec` gains `World` and the runner routes spawn/kill through it; `internal/race` gains `Backend`/`Parallel` and the two-phase worker pool; `internal/admit` passes `backend.HostDir`; `cmd/mvo/race.go` gains the exec/parallel flags; `cmd/mvo/worlds.go` gains a TIER column; `scripts/accept.sh` gains the parallel and T1 steps.

## Resolved design decisions

1. **The package is `internal/backend` + `internal/dockerx`, not `internal/exec`.** A package named `exec` shadows `os/exec` in every file that imports both — and the runner, the oracle, gitx, and race all import `os/exec`. `backend` names PRD §9's `ExecutionBackend` directly; `dockerx` mirrors `gitx` (thin shell-out, pure argv builders, stderr folded into errors). Two packages because policy and plumbing must not mix: `dockerx` knows docker and nothing about worlds; `backend` knows worlds and nothing about docker flags beyond calling `dockerx`.
2. **Docker via CLI shell-out** (header). Every `dockerx` command runs with a docker-client env allowlist — `PATH, HOME, USER, TMPDIR, DOCKER_HOST, DOCKER_CONTEXT, DOCKER_CONFIG, DOCKER_CERT_PATH, DOCKER_TLS_VERIFY, DOCKER_API_VERSION` copied from mvo's environment — so the operator's daemon selection works (control-plane configuration, not world input) and nothing else leaks into docker's own process.
3. **The worktree is bind-mounted at `/work`, never `docker cp`'d.** One mutable state: host-side evidence capture (AG-4 `agent.Diff`, `gitx.WriteTree`, `VerifyWorktreeRepo`) runs unchanged against the live worktree — no copy-back divergence window between "what the oracle saw" and "what the control plane captured", and no doubled I/O per world. On macOS the mount rides VirtioFS; the osxfs-era `:delegated`/`:cached` suffixes are ignored by VirtioFS (Docker Desktop ≥ 4.6) and are **not** passed — a dead option in evidence-adjacent argv is noise. Known consequence, accepted and beneficial: a worktree's `.git` is a *file* pointing at the main repo's absolute host path, which does not exist inside the container — the agent cannot read, repoint, or drive the shared repository from inside T1 (a strict improvement on T0's residual gap), and in-container `git` simply fails. Control-plane git never runs in the container.
4. **One long-lived keeper container per world; agent and oracle run via `docker exec`.** The keeper is `--entrypoint sleep <image> <ttl>` started with `--rm`: PID 1 is a sleep whose only job is to hold the pid namespace open. `docker exec` (no `-i`, no `-t`: stdin closed — agents must never block on reads, M1b rule) runs each step; exec'd processes join the container's cgroups, so the world's caps cover them (decision 6). TTL = intent `max_wall_ms` (in seconds, rounded up) + 600 s slack, 86400 s when the intent has no wall budget: a crashed `mvo` leaves no immortal container — the keeper expires and `--rm` removes it (NFR-3 v0 mitigation; the GC verb stays out). Zombie note: children of dead execs reparent to `sleep`, which never reaps — bounded at two execs per world and destroyed with the container; `--init` is not worth a host-binary dependency.
5. **Watchdog mapping (AG-2 → docker): pin `killReason` → `World.Kill()` → the unchanged M1b host-group escalation.** `World.Kill()` is `docker kill <cid>`: SIGKILL to PID 1 tears down the pid namespace, killing every in-container process — exec'd agent included (signaling the host `docker exec` client kills nothing inside the container; docker does not proxy exec signals). The M1b TERM → grace → KILL host-group path still runs, now aimed at the docker client, which frees the stdout/stderr pipes; `cmd.WaitDelay` unchanged. **T1 forfeits the graceful SIGTERM window** by design: hard enforcement is the tier's point (XP-2 "kill+record on breach"), the only loss is the CLI's final client-estimate cost flush — which AG-2 already ranks secondary to control-plane accounting — and the tee holds every byte streamed up to the kill. Rule 0 outcome precedence is untouched: watchdog/deadline → `BUDGET_EXCEEDED`, interrupt/cancel → `INTERRUPTED`, regardless of how the docker client exited. Container removal (`docker rm -f`, idempotent) is the ultimate cleanup on every path. `docker kill` and `docker rm -f` run under a **10 s timeout**: a wedged daemon (the failure mode that co-occurs with runaway containers) can only delay, never disable, the host-group escalation behind `World.Kill()` — or the oracle's `cmd.Cancel`, which must return before os/exec starts its `WaitDelay` timer — and the keeper TTL + `--rm` remain the backstop for the container itself.
6. **Caps are container-scoped and recorded in every receipt.** `--memory` (with `--memory-swap` set equal, so the cap cannot page out), `--cpus`, `--pids-limit` on the keeper bind every exec'd process via the shared cgroup. Breach semantics preserve the taxonomy: wall → watchdog → `BUDGET_EXCEEDED`; memory/pids → kernel-enforced kills that surface as non-zero exits classified by the **unchanged** M1b mapping tables (agent: typically `CRASH`; oracle: `fail`, or `error` on timeout) — no new outcome values; the receipt's `isolation_caps` is what makes the enforcement auditable. Schema change: `object.Execution` gains an always-serialized `isolation_caps` (PRD §5.3 requires it; no `omitempty` games, M1b decision 5 discipline). Consequence: receipt digests change, package goldens are re-derived, and **pre-M1c ledgers no longer replay** — sanctioned v0 breakage, same as M1b. World objects gain **no** new fields: T0 world digests are unchanged, and the T0 refactor is behavior-byte-compatible.
7. **Read-only root, tmpfs `/tmp`, `HOME=/tmp`.** T1 containers run `--read-only --tmpfs /tmp`; the only writable surfaces are `/tmp` and the bind-mounted `/work` — pytest caches and `__pycache__` land in `/work` (world-local, capture-visible: honest), pip/CLI state lands in `/tmp` (ephemeral). `HOME` is set to `/tmp` on every exec: host `HOME` passthrough is meaningless in T1 (no host credentials exist in the container), so keyfile-authenticated CLIs must authenticate via allowlisted env (`ANTHROPIC_API_KEY`, `CODEX_API_KEY` — M1b decision 14 names-only rule, unchanged). The T1 in-world env mapping, normative: filter `{PATH, HOME, TMPDIR, USER}` out of the caller-supplied pairs (container images own their `PATH`; `/tmp` owns temp), inject `HOME=/tmp`, keep the rest (`LANG`, `LC_ALL`, allowlisted extras) as **name-only** `-e NAME` flags sorted by name (deterministic argv for goldens); the VALUES travel in the docker client's process environment — `Command`'s hostEnv is the decision-2 allowlist **plus** the kept `NAME=value` pairs, which docker resolves the name-only flags from. Values never sit on the host command line: `/proc/<pid>/cmdline` is world-readable, and an allowlisted secret in the docker-exec argv would be open to every local process for the whole run — NFR-4, and M1b's cmd.Env posture preserved. `HOME=/tmp` alone rides inline as `-e HOME=/tmp`: it is a non-secret constant, and resolving it client-side would mean overriding the client's own `HOME`, breaking the operator's docker config lookup (decision 2). No read-only opt-out flag in M1c — an image that cannot run read-only with these two writable surfaces is v1 territory, documented under NOT.
8. **Non-root when the image supports it.** If `docker image inspect .Config.User` names a non-root user, the image's choice stands (no `--user` flag); an unset user — or an explicit root in any spelling (`root`, `0`, `0:0`, `root:root`) — does NOT count as naming one. Otherwise the keeper runs `--user <uid>:<gid>` of the invoking mvo process — on Docker Desktop/VirtioFS ownership is translated, on Linux hosts it makes `/work` writes correctly owned. Consequence, accepted: the user may lack a passwd entry; `HOME=/tmp` (decision 7) is what keeps CLIs functional. A second consequence, accepted and documented: on a Linux host, an image naming a non-root uid **different from mvo's** leaves its in-container writes under `/work` (e.g. `__pycache__`) owned by that uid, which mvo may be unable to remove at cleanup — chmod cannot cross a uid boundary without root, and keepers are already closed by worktree-removal time; Docker Desktop/VirtioFS ownership translation makes this a non-issue on macOS, and root/unset images are covered by the `--user` substitution. In-container cleanup machinery is v1 territory. The **effective** user string is recorded in `isolation_caps.user`.
9. **NFR-4: `--network none` is the default; `--allow-network` is the explicit opt-out.** With the flag, the container joins the default bridge and `isolation_caps.network` records `"default"`; without it, `"none"`. Hardening riders, always on for T1: `--cap-drop ALL` and `--security-opt no-new-privileges` (constant tier properties, defined here; the *variable* knobs are what receipts record). Network-off is **proven, not assumed**: the docker-gated test runs an in-container egress probe and the passing receipt is the proof (testing bar).
10. **Image references are digest-pinned at pre-flight and containers start from the pinned reference.** Resolution: `docker image inspect`; if the image is absent, `docker pull <ref>` once (stderr visible — the operator sees the one-time cost), then inspect again. `RepoDigests[0]` supplies the digest and the run reference (`repo@sha256:…`) — TOCTOU-free: what was recorded is what runs. Local-only images (built, never pushed: no RepoDigests) fall back to the image **ID** (config digest) for both — recorded as-is and honestly labeled by this contract: an ID pins content exactly but is not a registry manifest digest. `image_ref` records the operator's flag value verbatim.
11. **XP-3 env manifests: T1 is `{image_digest, image_ref, lockfiles?, os}`; T0 is byte-identical to M1b.** T1 manifest fields: `image_digest` (decision 10), `image_ref` (flag value), `lockfiles` (same names, same FIFO/symlink/size guards, same hashing as M1b `EnvDigest`, read from the **host** worktree — the bind mount makes the bytes identical and never requires an in-container read), `os` = the image's inspected `.Os` (`"linux"` — the world executes there; `runtime.GOOS` would be a lie). T0 manifests keep M0's exact `{"go":"none","os":runtime.GOOS,…}` bytes — T0 world digests do not move. **Freshness picks this up automatically, verified**: `race.Run` already sets `rec.Freshness.ValidFor = {Tree: world.Tree, Env: world.Env}` for every receipt (M0 contract, unchanged code path), so T1 receipts bind the image digest through `valid_for.env` with zero new plumbing; `mvo verify` is untouched (admission receipts are host-T0 records, decision 12). `race.EnvDigest` stays exported with its M1b body, now delegating to `backend`'s T0 manifest builder — `admit`'s call sites compile and behave unchanged.
12. **`Oracle.Run` takes the world handle** — PRD EP-1 is literally "run(world) → Receipt"; a string dir can no longer say where execution must happen. Receipts record the **in-world** argv (the verification command is the evidence; the `docker exec` wrapper is transport, reproducible from tier + caps + image digest). `admit` passes `backend.HostDir(landingDir)`: **landing gates stay T0 on the host** in M1c. Consequence, documented: an intent raced under T1 lands via a host-run gate whose receipt honestly records `T0-worktree` + the host env digest; if the host lacks the toolchain the gate fails → REJECT — honest, never silently skipped. `LandingOracleArgv` is unchanged (it recovers the in-world argv, which is exactly what the host gate runs). T1 landing gates are deferred (NOT list).
13. **The script adapter under T1 keeps its host-side apply.** The isolation tier governs where *untrusted* execution happens — the agent process and the oracle process; applying an operator-supplied patch is the control plane's own act (M0 behavior). A T1 script world therefore runs only its oracle in the container, and its `isolation_tier` honestly reads `T1-container`: every process the tier exists to confine ran confined. This is also what makes the docker-gated integration cheap: no agent CLI needs to exist in the image to prove the T1 pipeline.
14. **Tool-level sandbox flags are tier-dependent.** Under T1 the container *is* the sandbox (cap-drop ALL, no network, read-only root, caps — all recorded), so codex argv becomes `codex exec --json --sandbox danger-full-access --skip-git-repo-check …`: its Landlock/seatbelt sandbox is designed for bare hosts and can misbehave under a restricted kernel, and its git-repo check would trip over the deliberately dangling `.git` (decision 3). Under T0 both adapters keep their M1b argv byte-for-byte. claude-code is unchanged in both tiers (`bypassPermissions` was already the M1b call). Argv builders take the tier; goldens cover both.
15. **Parallelism is a two-phase bounded worker pool; `--parallel 1` reproduces M1b's schedule exactly.** Phase A generates worlds (bounded at N workers), a barrier joins, phase B runs oracles (bounded at N), then the decision. Two-phase rather than per-world pipelining because (a) with N=1 it degenerates to M1b's exact event order — the "default 1 = current behavior" guarantee is structural, not tested-into-place — and (b) scheduling policy is the M2 scheduler's turf; M1c buys wall-clock, not cleverness. Results land in a pre-sized slice indexed by candidate ordinal — no ordering ambiguity to repair afterward.
16. **The ledger serialization point is a race-owned mutex, not an appender goroutine.** One `recMu` guards every `Ledger.Append` in the race path (`recordObject` + `appendEvent`); CAS puts stay lock-free (temp-file + rename, idempotent, concurrent-safe). A channel-fed appender was rejected: an append failure must abort the *appending worker* synchronously (control-plane failure aborts the race, M1b decision 10), and a channel turns every append into a request/response round trip — the mutex is the same total order with less machinery. Per-world event order (`agent.started` → `agent.finished` → `world.created` → `receipt.recorded`) is preserved by goroutine locality — one worker owns one world; cross-world interleaving is allowed and correlated by `(intent, ordinal)` (M1b rule, unchanged). The ledger's own single-connection serialization is defense in depth, not the contract.
17. **Decision inputs are assembled deterministically: sorted by digest.** `race.Run` sorts worlds by world digest ascending and receipts by receipt digest ascending before calling `Decide`. `Decide` was already order-independent (it sorts evidence and ranks with a digest tie-break); M1c makes that an *invariant with a property test* (any permutation of inputs → byte-identical decision), which is exactly why audit replay — which assembles from ledger-scan order, i.e. completion order under parallelism — reproduces the decision byte-for-byte with **zero audit changes**. Honesty about what determinism means here: replay of a *recorded* race is exact; *cross-run* equality of full decision digests is not promised (CreatedAt and measured wall times differ run to run, and `wall_ms_asc` ranking can order two passing worlds differently under contention). The acceptance fixture pins decision **type and winner** across serial/parallel runs, which is schedule-independent there because exactly one candidate passes the gate. Worktree creation and removal stay serialized under one `repoMu` (`git worktree add/remove/prune` contend on repo-level locks; concurrent adds are flaky) — accepted and documented: worlds dir setup is O(N)·<1 s (NFR-2), dwarfed by agent runtime.
18. **T1 without a working daemon is a machinery error before any ledger event.** Pre-flight in `cmd/mvo` (before `race.Run`, hence before `race.started`): docker CLI on PATH → `docker version` daemon probe → image resolve+pin (decision 10) → `.Os == "linux"` check → for subprocess adapters, an **in-image** binary probe (`docker run --rm --name mvo-probe-<hex> --network none --entrypoint <bin> <pinned-ref> --version`) replacing M1b's host `exec.LookPath` (same rationale: never burn N `CONFIG_ERROR` worlds discovering a missing CLI). The probe is bounded at 60 s — a `--version` that never exits, or a wedged daemon, must not hang mvo at pre-flight — and on expiry the named probe container is force-removed (unlike the keeper it has no TTL backstop: its `--rm` fires only on exit). Any failure aborts with a clear one-line message and exit 1; the ledger stays empty of race events.
19. **XP-4 (warm reuse) is deferred, and `Backend.Open` is deliberately warm-pool-shaped.** A future pool pre-starts keepers and hands them out behind the same `Open(ctx, dir)` call (the bind mount becomes the only cold step, or moves to `docker cp`/volume cloning — a pool-internal choice). Nothing in this contract assumes cold starts; NFR-2's "< 5 s warm" target is explicitly out of M1c scope and honestly unmet until then.

## ExecutionBackend (`internal/backend`) — XP-1

```go
package backend

// Backend provisions isolation for worlds. Implementations: T0-worktree
// (bare host, M1b behavior), T1-container (docker). It is one of the five
// PRD §9 extension surfaces (ExecutionBackend).
type Backend interface {
    Tier() string // object.TierT0Worktree | object.TierT1Container
    // Open provisions isolation for one world whose worktree the
    // orchestrator has already created at dir. T0: a no-op wrapper.
    // T1: docker run of the keeper container. Errors are machinery
    // failures and abort the race.
    Open(ctx context.Context, dir string) (World, error)
}

// World is one provisioned world's execution surface.
type World interface {
    Tier() string
    Dir() string // host worktree path — the evidence-capture surface (AG-4)
    // Command maps an in-world invocation onto the host invocation that
    // executes it inside the world. env holds NAME=value pairs for the
    // in-world process; nil means the world's own default environment.
    //   T0: identity — hostArgv = argv, hostEnv = env (nil ⇒ the spawner
    //       inherits the ambient environment, exactly M0/M1b behavior).
    //   T1: hostArgv = docker exec argv with name-only -e flags (decision
    //       7 env mapping — values never sit on the host command line),
    //       hostEnv = the docker-client allowlist (decision 2) plus the
    //       NAME=value pairs the name-only flags resolve from.
    Command(argv, env []string) (hostArgv, hostEnv []string)
    // Kill terminates everything executing in the world. T0: no-op (the
    // runner's host process-group kill is authoritative). T1: docker kill
    // (pid-namespace teardown). Idempotent; a dead world is not an error.
    Kill() error
    Caps() object.IsolationCaps
    // EnvDigest builds the XP-3 env manifest for the world, stores its
    // canonical bytes in CAS, and returns its "mv0:" digest.
    EnvDigest(store *cas.Store) (string, error)
    // Close tears down the world's isolation (T1: docker rm -f;
    // idempotent). The worktree itself remains the orchestrator's to
    // remove. Safe to call more than once and after Kill.
    Close() error
}

// Config selects and parameterizes a backend. Zero caps mean uncapped and
// are recorded as such (weak defaults, honest labels).
type Config struct {
    Tier         string        // object.TierT0Worktree (default "") | object.TierT1Container
    Image        dockerx.Image // resolved, digest-pinned (T1 only; zero value for T0)
    CPUMilli     int64         // 0 = uncapped; 1500 = --cpus 1.5
    MemoryMB     int64         // 0 = uncapped
    PidsLimit    int64         // 0 = uncapped
    AllowNetwork bool          // false = --network none (NFR-4 default)
    KeeperTTL    time.Duration // container self-expiry (decision 4)
    IntentDigest string        // label for orphan identification
}

func New(cfg Config) (Backend, error) // unknown tier / T1 without image → error

// HostDir wraps a bare directory as a T0 world with no backend — the
// admission path's landing worktree and the T0 backend's own Open result.
func HostDir(dir string) World
```

T0 contract: `Open` returns `HostDir(dir)`; `Caps()` returns `object.HostCaps()` (the honest uncapped-bare-host record — PRD §9: macOS has no cgroups, bare-host runs are uncappable *and say so*); `EnvDigest` produces M0/M1b's exact manifest bytes. The T0 refactor is **byte-compatible**: same argv, same env, same manifests, same world digests.

## Docker shell-out (`internal/dockerx`)

Conventions: shell out to `docker`, stderr folded into errors, pure argv builders unit-tested by golden, client env per decision 2. No SDK, no socket protocol, no JSON API client — `--format` templates only.

```go
package dockerx

// Available checks the docker CLI is on PATH and the daemon answers
// `docker version` (10 s timeout). The error text is operator-facing.
func Available() error

// Image is a resolved, digest-pinned image reference (decision 10).
type Image struct {
    Ref    string // operator's flag value, verbatim ("python:3.12-alpine")
    Digest string // "sha256:…" — RepoDigests[0] digest, or the image ID for local-only images
    RunRef string // what containers start FROM: "repo@sha256:…", or the image ID
    OS     string // inspected .Os ("linux")
    User   string // inspected .Config.User ("" when unset)
}

// ResolveImage inspects ref, pulling once if absent, and pins it.
func ResolveImage(ref string) (Image, error)

// RunOpts parameterizes one keeper container (KeeperArgv is the pure,
// golden-tested builder; RunKeeper executes it and returns the cid).
type RunOpts struct {
    Image        Image
    HostDir      string // bind-mounted at /work
    CPUMilli     int64
    MemoryMB     int64
    PidsLimit    int64
    AllowNetwork bool
    User         string // "uid:gid" or "" (image's own user stands)
    TTLSeconds   int64
    IntentDigest string
    Name         string // "mvo-w-<12 hex>"
}
func KeeperArgv(o RunOpts) []string
// RunKeeper honors ctx (race deadline / first-failure cancellation): a
// hung `docker run` must not keep a race waiting in Open.
func RunKeeper(ctx context.Context, o RunOpts) (cid string, err error)

// ExecArgv builds the in-world invocation: docker exec -w workdir
// [-e NAME | -e NAME=value …] cid argv… (entries verbatim, pre-sorted by
// the caller; no -i, no -t). Name-only entries resolve from the docker
// client's environment — the decision-7 secret-safe transport.
func ExecArgv(cid, workdir string, env []string, argv []string) []string

// Both bounded at 10 s: a wedged daemon can only delay, never disable,
// the host-side escalation behind World.Kill (decision 5).
func Kill(cid string) error   // docker kill; "No such container" → nil (already dead)
func Remove(cid string) error // docker rm -f; "No such container" → nil (idempotent)

// ParseCPUMilli parses a CLI decimal ("1.5") into milli-CPUs. Strict:
// ^\d+(\.\d{1,3})?$ and > 0. FormatCPUMilli renders the shortest exact
// decimal for the --cpus flag. Integer arithmetic only (usd.go pattern);
// round-trip law: ParseCPUMilli(FormatCPUMilli(m)) == m for all m ≥ 1.
func ParseCPUMilli(s string) (int64, error)
func FormatCPUMilli(m int64) string
```

Keeper argv, normative shape (flags appear iff their knob is set; order fixed):

```
docker run -d --rm --name <Name>
  --label dev.multiverso=1 --label dev.multiverso.intent=<intent digest>
  --network none                        # omitted with AllowNetwork
  --cap-drop ALL --security-opt no-new-privileges
  --read-only --tmpfs /tmp
  -v <HostDir>:/work -w /work
  [--memory <N>m --memory-swap <N>m] [--cpus <FormatCPUMilli>] [--pids-limit <N>]
  [--user <uid>:<gid>]
  --entrypoint sleep <RunRef> <TTLSeconds>
```

`--entrypoint sleep` (not a bare command) so image ENTRYPOINTs can never reinterpret the keeper. `--memory-swap` equals `--memory`: the cap must not page out. `sleep <integer seconds>` because `sleep infinity` is not portable to busybox.

## T1 world lifecycle — watchdog mapping (XP-2, AG-2)

| M1b runner semantics (T0) | T1 mapping |
|---|---|
| spawn argv in worldDir, own process group (`Setpgid`) | spawn `docker exec …` on the host (same group discipline); the world process runs inside the container |
| watchdog fires: pin reason → SIGTERM pgid → grace → SIGKILL pgid | pin reason → `World.Kill()` (`docker kill` → SIGKILL PID 1 → pid-namespace teardown kills every in-container process) → the same host-group escalation, now freeing the docker client and its pipes |
| `Interrupt()` | identical path, reason `"interrupt"` |
| ctx deadline / cancel (`watchCtx`) | identical path, reason from `ctx.Err()` |
| exit code from `ProcessState` | `docker exec` propagates the in-world exit code (137 after a kill); rule 0 pins the outcome before any code is read |
| `cmd.WaitDelay` 5 s | unchanged |
| oracle timeout: SIGKILL the group | `runCtx.Err()` → `World.Kill()` then the group kill; receipt status `"error"` unchanged |

Outcome taxonomy preserved (decision 5): rule 0 first, then the unchanged per-adapter tables. Cleanup ladder (NFR-3): `World.Close()` per world after its oracle phase (immediately at the barrier for non-`COMPLETED` worlds); deferred `Close` on all error paths; `--rm` + keeper TTL for a crashed control plane. Containers are **always** removed — `--keep-worlds` keeps worktrees (evidence surface), never containers (transport; they hold nothing the CAS and worktree don't).

## Resource caps & security posture (`internal/object`) — XP-2, NFR-4

```go
// IsolationCaps records what actually confined an execution (XP-1/XP-2).
// All fields always serialized; zero means uncapped, honestly.
type IsolationCaps struct {
    CapDrop      string `json:"cap_drop"`       // "ALL" | ""
    CPUMilli     int64  `json:"cpu_milli"`      // 0 = uncapped
    MemoryBytes  int64  `json:"memory_bytes"`   // 0 = uncapped (MemoryMB << 20)
    Network      string `json:"network"`        // "none" | "default" | "host"
    PidsLimit    int64  `json:"pids_limit"`     // 0 = uncapped
    ReadOnlyRoot bool   `json:"read_only_root"`
    User         string `json:"user"`           // effective user; "" = process default
}

const (
    TierT0Worktree  = "T0-worktree"
    TierT1Container = "T1-container"
    NetworkNone     = "none"
    NetworkDefault  = "default"
    NetworkHost     = "host"
)

// HostCaps is the T0 record: uncapped bare host, said plainly (PRD §9).
func HostCaps() IsolationCaps
// {cap_drop:"", cpu_milli:0, memory_bytes:0, network:"host",
//  pids_limit:0, read_only_root:false, user:""}
```

`object.Execution` gains `IsolationCaps IsolationCaps \`json:"isolation_caps"\`` (always serialized; decision 6 consequences apply). The `race`/`oracle` `isolationTier` string constants are replaced by the `object` constants; receipts take tier and caps from the world handle, never from a package constant.

## Env digest (`internal/backend/env.go`) — XP-3

T0 manifest: byte-identical to M1b (`{"go":"none","os":runtime.GOOS}` + `lockfiles` when present) — the builder moves here; `race.EnvDigest` keeps its signature and delegates (admit unchanged, no import cycle: `backend` imports only `cas`/`object`/`dockerx`).

T1 manifest (canonical JSON, keys sorted as always):

```json
{"image_digest":"sha256:…","image_ref":"python:3.12-alpine",
 "lockfiles":{"requirements.txt":"sha256:…"},"os":"linux"}
```

`lockfiles` omitted when none found (same key-shape rule as T0); hashing rules, guards, and the 64 MiB cap are M1b's, applied to the **host** worktree (decision 11). Freshness flow: automatic via the existing `race.Run` assignment — verified, no new plumbing; the docker-gated test asserts a T0 and a T1 race over the same tree produce **different** `freshness.valid_for.env` values, and that the T1 world's manifest in CAS carries the pinned digest.

## Oracle changes (`internal/oracle`) — EP-1, EP-7

```go
type Oracle interface {
    ID() string
    Version() string
    Run(ctx context.Context, w backend.World) (object.Receipt, error)
}
```

`CommandOracle.Run`: `hostArgv, hostEnv := w.Command(o.Argv, nil)`; `cmd.Env = hostEnv` (nil ⇒ inherit — exactly M0 for T0); on timeout/cancel, `w.Kill()` then the existing group SIGKILL. The receipt records `Execution.Argv = o.Argv` (in-world argv — decision 12), `Execution.IsolationTier = w.Tier()`, `Execution.IsolationCaps = w.Caps()`. Status mapping (pass/fail/error) unchanged. `admit` calls `cfg.Oracle.Run(ctx, backend.HostDir(dir))`; the landing-apply receipt records `TierT0Worktree` + `HostCaps()`.

## Agent runner changes (`internal/agent`) — AG-1/2/3/4

`RunSpec` gains `World backend.World` (required for subprocess adapters; the script adapter ignores it — its git operations are control-plane host work, decision 13). `startProc`: `hostArgv, hostEnv := spec.World.Command(argv, buildEnv(spec.Env))`; `kill()` calls `spec.World.Kill()` after pinning the reason and before the host-group signaling. `buildEnv` is unchanged (T0 pairs are what M1b produced; T1 filters/augments per decision 7 inside `Command`). Argv builders take the tier (decision 14). Everything else — tee, transcript, events channel, stderr capture, watchdog timer, outcome mapping — is **host-side and unchanged**: only the process crosses the container boundary; the control plane keeps the pipes, the timers, and the git (AG-3/AG-4 ownership is structural, not per-tier).

## Parallel race (`internal/race`) — CP-2, NFR-1, NFR-2

```go
type Config struct {
    // … all M1b fields unchanged …
    Backend  backend.Backend // NEW, required
    Parallel int             // NEW, required ≥ 1; 1 = the M1b schedule
}
```

Run sequence:

1. Validation, intent/policy load, CP-2 cap, race ctx deadline, `raceDir` — unchanged. `race.started` body gains three observational keys (below).
2. **Phase A — generation**, bounded at `Parallel` workers; candidate k owns slot `runs[k-1]`:
   under `repoMu`: `gitx.AddWorktree`; then `cfg.Backend.Open(ctx, dir)` → world handle; context CAS put; `agent.started`; `Adapter.Start(spec{…, World: wh})` → `Wait`; post-run capture (identity check, diff, tree — M1b semantics verbatim, host-side); `agent.finished`; `wh.EnvDigest(CAS)`; `world.created`. Every ledger append under `recMu` (decision 16).
3. **Barrier.** Non-`COMPLETED` worlds' handles are `Close`d here.
4. **Phase B — verification**, bounded at `Parallel`: for each `COMPLETED` world, `cfg.Oracle.Run(ctx, wh)`; fill `World`/`Freshness.ValidFor` (unchanged M0 rule); `receipt.recorded`; `wh.Close()`.
5. Assemble `Decide` inputs **sorted by digest** (decision 17); `Decide`; `decision.recorded`; `race.finished`; worktree cleanup under `repoMu`.

Failure semantics under parallelism: agent failure stays evidence (per-world, race continues — M1b decision 10 verbatim); the first **control-plane** failure in any worker cancels the race ctx, all workers drain (their in-flight kills follow the ctx path honestly: `INTERRUPTED`… but their worlds may not reach `world.created` — an incomplete race, which audit already tolerates), and `Run` returns the first error. Already-recorded events stay recorded. Deferred cleanup closes every opened world handle and removes worktrees exactly as M1b did.

## Ledger event types (M1c amendments)

No new event types. `race.started` gains three always-present observational keys (audit reads only `intent` — unaffected):

| Type | Payload |
|---|---|
| `race.started` | `{"adapter": "script@v0", "candidates": 2, "exec_image_digest": ""\|"sha256:…", "exec_tier": "T0-worktree"\|"T1-container", "intent": "mv0:…", "parallel": 1}` |

Container IDs appear nowhere in the ledger: containers are transport, not evidence — the image digest is the evidence and lives in the env manifest, receipts (`valid_for.env`), and `race.started`.

## CLI (`cmd/mvo`)

```
mvo race <intent-digest> [--agent script|claude-code|codex] --oracle-cmd CMD
         [--parallel N] [--exec T0|T1] [--keep-worlds] [--dir DIR]
    --exec T1 additionally:
         --exec-image REF              required; tag resolved and digest-pinned
         [--memory-mb N] [--cpus DEC] [--pids N]
         [--allow-network]
```

Flag discipline (usage errors, exit 2, same pattern as `agentOnlyFlags`): `--exec` other than `T0`/`T1`; any of `{exec-image, memory-mb, cpus, pids, allow-network}` without `--exec T1`; `--exec T1` without `--exec-image`; `--parallel < 1`; `--memory-mb` set but `< 6` (docker's own floor); `--cpus` failing `ParseCPUMilli`; `--pids` set but `< 1`. `--parallel` and `--exec` compose freely with every adapter.

T1 pre-flight (decision 18) runs after flag validation and before `race.Run`; failure messages are one-liners of the form `mvo: race: exec T1: docker daemon unavailable (is Docker running?): <detail>`. `KeeperTTL` derives from the intent budget (decision 4). For subprocess adapters the in-image probe replaces the host `LookPath` (which still runs for T0, unchanged).

`mvo worlds` gains a `TIER` column (from `World.IsolationTier`). `usage()` gains the new flags on the `race` lines. All other verbs unchanged.

## NFR-2 — overhead accounting

- Image pull happens at pre-flight, **outside** the race window, once per image; the ledger's `race.started` timestamp postdates it by construction.
- T1 world provision = one `docker run` of a keeper: ~100–500 ms against a warm daemon — comfortably inside the "< 30 s cold" budget. "< 5 s warm" is XP-4's number and is honestly out of scope until the pool exists (decision 19).
- Each `docker exec` adds ~50–100 ms over a bare fork/exec — two per world, invisible next to agent/oracle runtime.
- Serialized ledger appends are sub-millisecond each; `recMu` contention at `--parallel N` is noise against the ≤ 5 % control-plane budget.
- Worktree creation stays serialized (decision 17): O(N) · < 1 s in phase A's critical path, accepted and documented.
- A formal ≤ 5 % measurement (race wall minus Σ agent/oracle durations) is M2 eval-harness territory; M1c's obligation is keeping every fixed cost out of the per-world loop, which the pre-flight/keeper split does.

## Fixture image & docker test guard (`testdata/t1image/`)

```dockerfile
FROM python:3.12-alpine
RUN apk add --no-cache bash && pip install --no-cache-dir pytest==8.*
COPY fakeagent/claude fakeagent/codex /usr/local/bin/
```

Built with context `testdata/` (`docker build -f testdata/t1image/Dockerfile -t multiverso-t1-fixture:v1 testdata`) so the fake agent fixtures ride inside — bash because the fixtures are bash, and alpine ships ash. Built **once per machine** by the test helper / accept.sh when absent (the build needs network — a one-time developer cost; CI never reaches it: no daemon → skip). Never pushed; the tag version bumps when the Dockerfile or fixtures change. This is the "tiny base, built once, reused" fixture the repo rules require; no test pulls anything larger than `python:3.12-alpine`.

Test guards (shared helper, `internal/dockerx` + reused via small copies where import direction forbids):

- `requireDocker(t)`: `MVO_SKIP_DOCKER_TESTS=1` → `t.Skip`; `exec.LookPath("docker")` fails → skip; `docker version` (5 s) fails → skip. Skip messages name the reason. CI (no docker) skips everything; locally the tests run.
- `ensureFixtureImage(t)`: inspect `multiverso-t1-fixture:v1`; build on miss; skip (with reason) if the build fails — offline machines degrade gracefully.

## Acceptance script (CI runs this)

`scripts/accept.sh` — M1b's steps kept intact; two insertions after step 3b, before admit (while the base commit still carries the bug, same rationale as 3b):

3c. **Parallel determinism**: `INTENT3=$(mvo intent new …)`; `mvo race "$INTENT3" --agent script --patches "$REPO/patches" --parallel 2 --oracle-cmd "python3 -m pytest -q"`. Assert: decision type is `SELECT` and the winner world's `context` CAS key equals patch-a's content hash — the same world-by-patch identification step 3 uses — i.e. **decision type and winner match the serial run of step 2** (schedule-independent here: patch-a is the unique gate-passer, decision 17).
3d. **T1 step (docker-gated, graceful skip)**: if `docker version` fails → `echo "accept: T1 step SKIPPED (no docker daemon)"` and continue. Else ensure the fixture image (build if missing; on build failure, skip with the reason). Then `INTENT4=$(mvo intent new …)`; `mvo race "$INTENT4" --agent script --patches "$REPO/patches" --exec T1 --exec-image multiverso-t1-fixture:v1 --memory-mb 512 --cpus 1 --pids 256 --oracle-cmd "python3 -m pytest -q"`. Assert: `SELECT` with patch-a's world winning (the container really gated it); the winner's env manifest in CAS contains `"image_digest":"sha256:`; the T1 winner's `env` digest **differs** from the T0 winner's (XP-3); the suite receipt records `"isolation_tier":"T1-container"` and `"network":"none"` (sqlite + python3 over `receipt.recorded` payloads, same style as step 3b's assertions).

Steps 4–10 unchanged (admit intent 1, trailer, verify ×3, second-machine audit — which now also replays the parallel race and, when it ran, the T1 race — bundle tamper, ledger tamper, final clean audit). Step 7's decisions bound bumps to `>= 4`.

## Testing bar

- `internal/dockerx` (no docker): `KeeperArgv` goldens — full caps, no caps, `--allow-network` (network flag omitted), user present/absent, `--memory`+`--memory-swap` pairing, `--entrypoint sleep` position; `ExecArgv` goldens (sorted `-e` flags, no `-i`/`-t`); `ParseCPUMilli`/`FormatCPUMilli` round-trip + rejection tables (`"1.5"`→1500, `"0"`/`"1.2345"`/`".5"`/`"1,5"` rejected); Kill/Remove "No such container" → nil mapping. Docker-gated: `Available`; `ResolveImage` on the fixture image (digest non-empty, `sha256:` prefix; local-only ID fallback exercised by building an untagged child); keeper lifecycle (run → exec `true` → kill → `docker inspect` fails → `Remove` idempotent).
- `internal/backend` (no docker): `New` selection table (default T0; unknown tier and T1-without-image errors); `HostDir` — `Command` identity with nil and non-nil env, `Kill` no-op, `Caps == object.HostCaps()`; **T0 `EnvDigest` byte-identical to the M1b golden** (regression pin: world digests must not move); T1 manifest golden canonical bytes + digest ≠ the T0 digest for the same dir; T1 `Command` env mapping golden (PATH/HOME/TMPDIR/USER filtered, `HOME=/tmp` injected, extras as sorted name-only flags with their values in hostEnv and never on the argv). Docker-gated: `Open`/`Command`/`Kill`/`Close` against the fixture image; `docker inspect` shows the caps (memory, pids, cap-drop, network none); read-only proven in-world (`touch /etc/x` fails; `touch /tmp/x` and `touch /work/x` succeed).
- `internal/oracle`: T0 receipts carry `HostCaps` + tier from the handle (goldens re-derived once for the schema change). Docker-gated: command oracle inside the container (pass); oracle `Timeout` against an in-container `sleep 60` → receipt status `"error"` **and** the container is gone (the Kill mapping proven); **NFR-4 egress proof**: oracle argv `python3 -c "import socket,sys; …"` that exits 0 iff a connect to `1.1.1.1:443` (3 s timeout) **fails** → receipt status `"pass"` is the recorded proof of no egress; the same receipt's caps record `network: "none"`. No test ever requires real egress (`--allow-network` is covered by argv goldens only).
- `internal/agent`: runner kill path calls `World.Kill` before group signaling, and pins the reason first (fake-World spy, no docker); T0 identity `World` keeps every M1b outcome-mapping/watchdog/fixture test green **unchanged**; codex/claude argv goldens per tier (decision 14). Docker-gated: T1 race with `--agent claude-code` against the baked-in fixture (`FAKE_AGENT_MODE=happy` via the env allowlist → `-e` flag): worlds `COMPLETED`, captured patch real (`diff --git`), transcript non-empty, `usd_micro == 4200` — the agent demonstrably ran inside the container while capture stayed control-plane-owned.
- `internal/race`: `--parallel 3` vs `--parallel 1` over the same candidates (script and fake-claude variants) → equal decision `Type`, equal winner-by-`context`, equal evidence counts; both ledgers `VerifyChain` clean and `mvo audit`-replayable; per-world event order asserted per ordinal (`agent.started` < `agent.finished` < `world.created` < `receipt.recorded`); `Decide` permutation-invariance property test (shuffled inputs → byte-identical decision); `Parallel < 1` config error; a mixed race (COMPLETED + CRASH) under `--parallel 2` decides correctly. **The race package's parallel tests run under `go test -race`** — no interleaving corruption, no data races.
- `cmd/mvo`: flag-matrix usage errors (table in "CLI"); T1-without-docker pre-flight → clean machinery error naming docker, exit 1, and **zero race events in the ledger** (test pins `PATH` to an empty dir via `t.Setenv`, so it is deterministic on machines that have docker); worlds TIER column.
- `internal/object`: `IsolationCaps`/`Execution` golden canonical bytes + digest stability re-derived; tier/network constants exhaustive; `HostCaps` golden.
- `gofmt -l` clean, `go vet ./...` and `go test ./...` pass with no daemon present (every docker test skips); `scripts/accept.sh` is the e2e test and runs in CI with the T1 step self-skipping. `go.mod`/`go.sum` unchanged. No test invokes a real agent CLI or pulls an image larger than `python:3.12-alpine`.

## Explicitly NOT in M1c

Warm-pool container reuse (XP-4, v1 — `Open` stays cold; decision 19); devcontainer.json awareness (XP-1's "devcontainer-aware" arrives with policy-resident image selection in v1 — M1c's image source is the `--exec-image` flag only, digest-pinned at pre-flight); policy-object image pinning; T1 landing gates in `admit` (decision 12); T2 microVMs; the live scoreboard consumer (M1b forecast amended: it slips to the M2 scheduler work — the events channel stays observability-only and races stay quiet); OOM-specific outcome taxonomy (kernel kills stay within the six values; refined breach labeling is v1); seccomp/AppArmor profiles beyond `--cap-drop ALL` + `no-new-privileges`; network allowlists / egress proxies (`--allow-network` is all-or-nothing); a read-only-root opt-out flag; in-container agent CLI provisioning (T1 images must ship the CLI; the in-image probe fails honestly otherwise); `docker cp`/volume-clone world materialization; userns-remap; per-step exec users; a container GC verb (keeper TTL + `--rm` is the v0 mitigation); pipelined per-world scheduling (two-phase only; the M2 scheduler owns anything smarter); intent-level parallelism policy (flag-only); Podman/nerdctl compatibility claims (docker CLI semantics are the contract; lookalikes are untested); Windows.
