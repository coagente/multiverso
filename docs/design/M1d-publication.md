# M1d — Forge Publication: Design & Contracts

> Implements [PRD](../../PRD.md) **FI-1** (app-less, forge-neutral publication of candidates and evidence under `refs/multiverso/*`), the **TP-1 extension** (every receipt published outside the local ledger is DSSE-signed), **DP-4 partial** (evidence bundle export over git transport; file-based ledger bundle import/export stays v1), and **trunk-drift freshness surfacing** (FI-1's "freshness by merge-base drift polling", as a display concept). Builds on [M0](M0.md), [M1a](M1a-admit-signing.md), [M1b](M1b-agent-adapters.md), and [M1c](M1c-containers-parallel.md); every prior contract stays in force unless amended here. The [custom-refs probe](../probes/github-custom-refs.md) findings are **constraints**: 1k+ refs and bulk deletes work; default clones/fetches never see the namespace; the GitHub attestation store rejects local-key bundles (no mirroring in M1); refs are writable by anyone with push access, so **nothing may be location-trusted — self-authentication is mandatory**. Exit criterion: `scripts/accept.sh` passes end-to-end, including publish → clone → fetch-race → tamper-detection → prune against a **local bare remote**.
>
> Everything here is v0 and may break until M1 exit. Requirement IDs (FI-x, TP-x…) refer to the PRD. **Stdlib only; `go.mod`/`go.sum` untouched.** Tests push only to local bare fixture remotes (`git init --bare` in `t.TempDir()`) — never to the network. Real forges are exercised manually (M1 acceptance criterion 4), never by CI.

## Module layout (delta over M1c)

```
internal/publish/            publication engine
internal/publish/refs.go     ref names, intent-short, filename encoding, commit messages
internal/publish/closure.go  evidence closure from ledger+CAS (deterministic, replay-complete)
internal/publish/bundle.go   DSSE bundles for receipts + decisions (sign-on-publish)
internal/publish/publish.go  local ref build, push plan, reconciliation, ledger events
internal/publish/prune.go    retention policy + local/remote ref deletion
internal/publish/fetch.go    consumer side: fetch, offline verification, replay, summary
internal/publish/drift.go    trunk-drift status (display-only)
cmd/mvo/publish.go           mvo publish
cmd/mvo/prunecmd.go          mvo prune
cmd/mvo/fetchrace.go         mvo fetch-race
```

Amended (never rewritten): `internal/gitx` gains ref/tree/remote plumbing (below); `cmd/mvo/{worlds,explain}.go` gain the `freshness:` line; `cmd/mvo/{main,state}.go` gain the new verbs/event constants; `scripts/accept.sh` gains the publication steps. `internal/signing`, `internal/attest`, `internal/admit`, `internal/race` (except one exported constant reuse), `mvo verify`, and `mvo audit` are **untouched**.

## Resolved design decisions

1. **`<intent-short>` = the first 12 hex chars of the intent digest's hex part.** `mv0:` cannot appear in a refname (`:` is illegal in git refs), and 64-hex components make refs unreadable in every forge UI. 12 hex (48 bits) is collision-safe at any plausible intent count per repo; `mvo publish` still guards: if two recorded intents share a short, publish fails loudly (no silent disambiguation). The short is **discovery only** — the full digest travels inside every published object, and the consumer checks that the published intent's digest actually has the namespace's short prefix.
2. **Candidate refs are commit objects minted at a fixed epoch timestamp — the commit sha is a pure function of (base commit, world).** `refs/multiverso/intent/<short>/cand/<n>` points at a commit whose tree is the world's tree, whose sole parent is `intent.base.commit`, whose author/committer are the fixed `mvo <mvo@multiverso.invalid>` identity (M1a decision 7), and whose author+committer dates are pinned to `1970-01-01T00:00:00Z`. Publication commits are transport containers, not events — time lives in the ledger — and determinism is what makes idempotent republish *structural*: identical content re-mints identical shas, so the push plan diffs to zero. The parent link also connects every candidate to trunk history (`git log cand/n` reads naturally; the only novel objects are world trees and evidence blobs).
3. **Evidence is one deterministic commit per intent, rebuilt from content, never chained.** `refs/multiverso/intent/<short>/evidence` points at a single commit (same fixed identity/epoch, parent = `intent.base.commit`) whose tree holds one file per published item. Publication history is the ledger's job (`publish.started`/`publish.finished` record every tip movement); an evidence-ref parent chain would break content-determinism and add nothing a hash-chained ledger doesn't already hold. Content change ⇒ new tree ⇒ new sha ⇒ push; no change ⇒ same sha ⇒ no-op.
4. **Uniform naming rule: a file is named by the identifier other objects use to cite it.** Canonical objects (intent, policy, worlds) are named by their `mv0:` object digest; DSSE-wrapped items (receipts, decisions) are named by the **payload's** `mv0:` digest — the digest decisions and ledger events cite; the admission attestation is named by its **bundle CAS key** (`sha256:…`) — the identifier the commit trailer cites (M1a decision 2). Encoding: `:` → `_` (illegal/hostile in paths), extension `.json` for plain canonical objects and `.dsse.json` for envelopes. The mapping is trivially invertible, and the filename is only ever a *claim* — the verifier recomputes the digest from the bytes.
5. **Receipt signing is raw DSSE over the receipt's canonical bytes — not an in-toto Statement.** PayloadType `application/vnd.multiverso.receipt+json`. Rationale: (a) PRD §5.3 types `signature` as "DSSE" and the receipt already carries its own subject binding (`world`, `freshness.valid_for`) — an in-toto Statement would duplicate the binding into a second subject layer that verifiers must cross-check, a new tamper surface with zero information gain; (b) PAE binds the payloadType, so receipt envelopes are cryptographically domain-separated from admission attestations — the same key can never have a receipt signature replayed as an attestation or vice versa; (c) `internal/signing` already takes payloadType as a parameter — zero signing changes — and `mvo verify` (which asserts `application/vnd.in-toto+json` for bundles) is untouched; (d) G3 interop is not foreclosed: an in-toto Statement whose ResourceDescriptor names the receipt digest can be minted *around* the same canonical bytes when the FI-4 mirror or Sigstore arrives — the stored format never changes.
6. **The signature never enters the Receipt object.** A `signature` field inside the signed bytes is a fixed-point impossibility (M1a decision 1's lesson), and the ledger's receipt digests — which decisions cite — are over unsigned canonical bytes. TP-1's wording is honored literally: signing is a property of **publication**, not of the recorded object. The envelope payload is byte-identical to the ledger's canonical receipt; `object.DigestBytes(payload)` reproduces the cited digest.
7. **Published decisions are DSSE-signed too** (payloadType `application/vnd.multiverso.decision+json`), closing the one hole in the transitive trust chain. Without it: signed receipts pin world digests → world objects pin the intent digest → intent pins the policy — but a world that produced *no* receipt (`CONFIG_ERROR`, `CRASH`) is cited only by the decision's subject list, and an unsigned decision could be consistently re-fabricated around it. Signing the decision pins every subject world, the evidence list, the intent, and the policy directly. TP-1 names receipts as the floor, not the ceiling; the machinery (raw DSSE, decision 5) is already paid for. Signature proves *provenance*; replay (decision 14) proves *honest derivation* — both are checked, neither substitutes for the other.
8. **The published closure is replay-complete.** Default (`mvo publish`) contents: the intent, its policy, the latest SELECT decision, **every** world in the decision's subject (winners and losers — their objects, not their code), **every** receipt in the decision's evidence, and — when the intent has a landed ADMIT — the ADMIT decision, both landing receipts, and the attestation bundle. A published decision whose evidence set is partial would be exactly the unbound evidence PRD principle 2 forbids, and principle 3 (any third party re-derives why W won) requires the full `Decide` input set. Loser *code* (candidate refs) is retention-managed and ships only under `--include-rejected`; loser *evidence* is decision-load-bearing and always ships. DP-3 holds: world objects carry `context`/`trace` as CAS **keys**; the payloads (prompts, transcripts) never leave the private CAS.
9. **Sign-on-publish, not sign-on-record.** Envelopes are minted (or re-minted — Ed25519 is deterministic per RFC 8032, `object.Canonical` is deterministic, so re-minting is byte-stable) at publish time from ledger/CAS content. The ledger stays exactly as M0 defined it; no schema, digest, or replay path moves.
10. **Push discipline: explicit per-ref refspecs, plan-diff, compare-and-swap, reconciliation.** `mvo publish` builds local refs, runs `git ls-remote` on the namespace, and pushes **only refs whose remote value differs from the plan**, each as an explicit `<sha>:<ref>` refspec guarded by `--force-with-lease=<ref>:<observed-old>` (create ⇒ expect-absent lease) — the same CAS discipline as M1a's `UpdateRef`, so a concurrent publisher surfaces as a lease failure, never a silent clobber. Force is required and safe: a re-raced intent re-mints candidate commits that share only the base parent (non-fast-forward by construction), the namespace is tool-owned transport whose integrity story is content-addressing + signatures — never ref history — and the probe doc shows ref-level protection doesn't exist there anyway. **Publish additionally reconciles the intent's own namespace**: any `refs/multiverso/intent/<short>/*` ref (local or remote) not in the current plan is deleted (`:<ref>` refspec, leased). This makes "namespace ⊆ plan" an invariant, which is what lets fetch-race treat *any* unplanned ref as loud tamper evidence (decision 14) instead of guessing about strays from superseded races. Retention deletion of *valid* refs remains `mvo prune`'s job. `refs/heads/*` is never named in any refspec — structurally, not by convention: the ref builder only emits names under the namespace root.
11. **Local namespace refs double as GC pins (PRD §9).** World trees are written into the shared object db at race time but are unreachable once worktrees are removed; `mvo publish` creating `refs/multiverso/intent/<short>/*` locally is the pinning moment. The race→publish window rides git's default prune grace (`gc.pruneExpire` = 2 weeks on loose objects); if a tree has nonetheless vanished, `git commit-tree` fails loudly — never a silently thinner publication. Pin-at-race is noted for v1 if the window proves fragile in practice. Remote GC durability of custom-ref-only objects is the probe doc's open risk; the mitigation is unchanged (local repo is canonical per FI-4; `--remote` works against any mirror remote you control).
12. **Prune policy (FI-1 retention).** Loser candidate refs are always prunable. For a **non-admitted** intent the entire `<short>/*` namespace is prunable — nothing landed, and the ledger + CAS (never GC'd, append-only stands) remain the canonical record. For an **admitted** intent, the winner's candidate ref and the evidence ref are kept — they back the landed commit's attestation for remote verifiers — unless `--keep-admitted=false` (Go bool-flag spelling; default `true`) says otherwise, with the documented consequence that remote clones lose offline verification for that intent while local `mvo verify` still works. `--older-than DUR` is a guard, not a selector: prune refuses to act unless the latest `publish.finished` for the intent is older than DUR (publication commits are epoch-stamped by design, so the ledger is the only honest clock; an intent never published cannot satisfy the guard — error). Local deletion via `update-ref -d` per ref; remote via one bulk push of `:<ref>` refspecs (probe Q4: 600 deletes in 4 s). Prune **never** touches CAS or ledger.
13. **`mvo fetch-race` is workspace-less and ledger-less.** It runs in any clone with the remote configured — no `.multiverso/` required (that's the point: a second machine). The trust root is explicit: `--key PUB.pem` (PEM PKIX Ed25519, via `signing.LoadPublicKeyFile`), defaulting to `.multiverso/keys/local.pub` when a workspace happens to exist at `--dir`. No ledger on the consumer side ⇒ no events recorded; the fetch refspec is `+refs/multiverso/intent/<short>/*:…` with `--prune`, so the local namespace mirrors the remote exactly before verification (required for the unplanned-ref check to be sound).
14. **Verification = authenticate every item + close the cross-reference graph + replay every decision — reusing the shipped pure functions.** `race.Decide` and `admit.Decide` are the replay engines (single source of truth; no reimplementation), compared like `mvo audit` does (Type/Subject/Evidence/Rationale byte-for-byte, CreatedAt excluded). Failures are **collected, not fail-fast** — every bad item is named on stderr (`mvo: fetch-race: <path>: <detail>`) and the exit code is 1 if any check failed; a tampered file must be loud *and* specific. The full check list is normative below.
15. **fetch-race verifies the attestation as far as offline-without-the-commit allows; `mvo verify` remains the commit-anchored verifier.** The consumer clone may not have the admitted commit (or trunk at all); fetch-race checks the bundle's content-address, signature, statement shape, `producer_key_id`, that every predicate digest resolves within the published closure, the budget sum, and — the offline substitute for M1a's subject check — that the statement's subject `gitTree` equals the landing suite receipt's `freshness.valid_for.tree`. Full trailer/tree/parent anchoring stays TP-3 (`mvo verify`) territory on a workspace copy.
16. **Trunk drift is a display/status concept — never a ledger mutation.** Three states, computed at render time from git: **FRESH** (trunk head == `intent.base.commit`), **STALE/advanced** (base is the merge-base but not the head — trunk moved forward; receipts remain valid for their recorded trees, and EP-3's recompute-on-admit is the enforcement point, restated on the spot), **STALE/diverged** (merge-base ≠ base — history rewritten or base off-trunk). When the state is uncomputable (detached HEAD, base commit absent from the local object db) the line says **UNKNOWN** with the reason — honesty over blank. `mvo worlds`, `mvo explain`, and the fetch-race summary all render the same line from the same function; nothing is written anywhere.
17. **The doctor check is folded into publish pre-flight; no new verb.** `refs/multiverso/*` is outside `refs/heads/*`, so default `git push`/`git fetch`/`git clone` never touch it (probe Q2 — measured, not assumed). The one real hazard is an operator-configured `remote.<name>.push` refspec broad enough to sweep the namespace (`refs/*`-style mirrors): publish pre-flight reads `git config --get-all remote.<r>.push` and warns on stderr when a refspec covers `refs/multiverso` — two git calls, zero new surface. A standalone `mvo doctor` is deferred.
18. **No `context.Context` in publish/prune/fetch.** gitx is ctx-less by convention (M0); no oracle or agent runs here, and a hung network push is interruptible the way any git command is. Consistency beats plumbing a ctx nobody cancels.

## Ref layout (normative) — FI-1

```
refs/multiverso/intent/<short>/cand/<n>    n = 1-based candidate ordinal
refs/multiverso/intent/<short>/evidence    the evidence bundle commit
```

`<short>` per decision 1. Ordinal `n` = the world's position in `world.created` order within the race window that produced the published SELECT decision (ledger seq order — deterministic, replay-derivable). Candidate commit message (byte-exact; trailers contiguous):

```
multiverso: candidate <n> for intent <full intent digest>

Multiverso-Schema: multiverso.dev/candidate-ref/v0
Multiverso-Intent: <mv0:…>
Multiverso-World: <mv0:…>
Multiverso-Ordinal: <n>
```

Evidence commit message:

```
multiverso: evidence for intent <full intent digest>

Multiverso-Schema: multiverso.dev/evidence-ref/v0
Multiverso-Intent: <mv0:…>
Multiverso-Decision: <SELECT decision digest>
```

Evidence tree (one file per item; decision 4 naming; subdirectories by kind so the verifier knows how to check each item without sniffing):

```
intent/mv0_<hex>.json                 canonical Intent bytes
policy/mv0_<hex>.json                 canonical Policy bytes
worlds/mv0_<hex>.json                 canonical World bytes (one per decision subject)
decisions/mv0_<hex>.dsse.json         DSSE envelope; payload = canonical Decision bytes
receipts/mv0_<hex>.dsse.json          DSSE envelope; payload = canonical Receipt bytes
attestation/sha256_<hex>.dsse.json    the admission DSSE bundle, verbatim CAS bytes (admitted only)
```

**Self-authentication chain** (why no location is ever trusted): the trusted key authenticates receipt and decision envelopes → the signed decision pins its subject worlds, evidence receipts, intent, and policy by digest; signed receipts independently pin their world digests and `valid_for` trees → world objects (hash-checked against those digests) pin the intent digest → the intent pins the policy digest → candidate commits are pinned by tree equality against signed-cited worlds → decision replay (the same pure `Decide` functions) proves the published decision follows from the published evidence under the published policy. Every arrow is either a signature or a hash comparison the consumer computes itself; the ref names contribute nothing but discovery.

## DSSE bundles (`internal/publish/bundle.go`) — TP-1

```go
package publish

const (
    PayloadTypeReceipt  = "application/vnd.multiverso.receipt+json"
    PayloadTypeDecision = "application/vnd.multiverso.decision+json"
)

// SignItem wraps canonical object bytes in a one-signature DSSE envelope
// and returns the envelope's canonical bytes. Deterministic: same payload
// + key ⇒ same bytes (RFC 8032 + object.Canonical).
func SignItem(s *signing.Signer, payloadType string, payload []byte) ([]byte, error)

// OpenItem verifies an envelope's signature against pub, asserts the
// payloadType, and returns the payload bytes.
func OpenItem(envBytes []byte, payloadType string, pub ed25519.PublicKey) ([]byte, error)
```

Both are thin compositions over `signing.Sign`/`signing.Verify` + `object.Canonical` — no new crypto, no signing-package changes. PAE domain separation (decision 5) means the three payload types in the wild (`in-toto+json` for attestations, receipt, decision) can never be confused for one another even under the same key.

## Git backend additions (`internal/gitx`)

Same conventions as always (shell out, hooks disabled, `gitEnv()`, stderr folded into errors; raw-stdout paths where bytes are evidence — the `DiffCached` precedent).

```go
// PublishEpoch pins publication-commit timestamps (decision 2).
const PublishEpoch = "1970-01-01T00:00:00Z"

func HashObject(repo string, data []byte) (string, error)   // hash-object -w --stdin
type TreeEntry struct{ Mode, Type, SHA, Name string }       // Name = path for LsTreeRecursive
func Mktree(repo string, entries []TreeEntry) (string, error) // mktree (entries pre-sorted by Name)
func CommitTreeEpoch(repo, tree, parent, message string) (string, error)
    // CommitTree + GIT_{AUTHOR,COMMITTER}_DATE=PublishEpoch; fixed identity as M1a
func RefValue(repo, ref string) (string, error)             // rev-parse --verify --quiet; ("", nil) when absent
func ForEachRef(repo, prefix string) (map[string]string, error) // for-each-ref → ref→sha
func DeleteRef(repo, ref, oldCommit string) error           // update-ref -d <ref> <old> (CAS)
func LsRemote(repo, remote, pattern string) (map[string]string, error)
func Push(repo, remote string, refspecs []string, leases map[string]string) error
    // git push <remote> [--force-with-lease=<ref>:<old>]… <refspec>…
    // refspecs are explicit "<sha>:<ref>" (update/create) or ":<ref>" (delete);
    // lease value "" means expect-absent. Never called with anything under refs/heads.
func Fetch(repo, remote string, refspecs []string, prune bool) error
func LsTreeRecursive(repo, treeish string) ([]TreeEntry, error) // ls-tree -r -z
func CatBlob(repo, sha string) ([]byte, error)              // cat-file blob, raw bytes (no trimming)
func MergeBase(repo, a, b string) (string, error)           // ("", nil) when no common ancestor
func CommitExists(repo, sha string) bool                    // cat-file -e <sha>^{commit}
func RemotePushRefspecs(repo, remote string) ([]string, error) // config --get-all remote.<r>.push; missing → nil
```

`UpdateRef` (M1a) is reused for local ref writes: `git update-ref <ref> <new> <old>` with old `""` means "must not exist" — create-only CAS for free. `CommitTree` (real timestamps, admission commits) is unchanged.

## Publication engine (`internal/publish`)

### Refs & naming (`refs.go`)

```go
const (
    RefRoot  = "refs/multiverso/intent/" // + <short>/…
    ShortLen = 12
)

func IntentShort(dig string) (string, error) // "mv0:<64hex>" → first 12 hex; malformed → error
func Namespace(short string) string          // RefRoot + short
func CandRef(short string, ordinal int) string
func EvidenceRef(short string) string
func FileName(id string) string              // "mv0:…" → "mv0_…" (+ caller-side extension)
func ParseFileName(name string) (id string, dsse bool, err error) // inverse, extension-aware
```

### Closure (`closure.go`)

```go
// Item is one file in the evidence tree.
type Item struct {
    Path  string // e.g. "receipts/mv0_<hex>.dsse.json"
    ID    string // the cited identifier the filename encodes
    Bytes []byte // canonical object bytes, or canonical envelope bytes
}

// Candidate is one publishable world of the SELECT race, ordinal-ordered.
type Candidate struct {
    Ordinal int
    Dig     string
    World   object.World
    Winner  bool
}

// Closure is everything one intent publishes. Deterministic: two calls over
// the same ledger produce byte-identical Items (decision 9).
type Closure struct {
    IntentDig, Short     string
    Intent               object.Intent
    SelectDig            string
    Select               object.Decision
    Admitted             bool   // a landed ADMIT exists (admission.finished result=="ADMIT")
    AdmitDig             string // "" unless Admitted
    Candidates           []Candidate
    Items                []Item // Path-sorted
}

// BuildClosure scans the ledger for the intent's latest SELECT decision
// (same rule as mvo admit), assembles the decision-8 closure, and signs
// receipts/decisions with signer. Errors: no SELECT decision; intent-short
// collision (decision 1); closure objects missing from CAS.
func BuildClosure(led *ledger.Ledger, store *cas.Store, signer *signing.Signer,
    intentDig string) (*Closure, error)
```

Ordinals come from `world.created` order in the SELECT race's window; the subject set of the decision must equal the window's world set (mismatch ⇒ error — ledger inconsistency, fail loudly). When `Admitted`, the ADMIT decision (signed), both landing receipts (signed), and the attestation bundle (verbatim CAS bytes — it is already a DSSE bundle content-addressed by its CAS key) join `Items`.

### Publish (`publish.go`) — FI-1

```go
type Config struct {
    Repo            string
    Ledger          *ledger.Ledger
    CAS             *cas.Store
    Signer          *signing.Signer
    Intent          string // full digest, already recorded
    Remote          string // default "origin" at the CLI
    IncludeRejected bool
}

type RefTip struct{ Ref, Tip string }

type Result struct {
    Short    string
    Pushed   []RefTip // refs actually pushed this run (ref-sorted)
    UpToDate []string // planned refs already correct remotely
    Removed  []string // reconciliation deletions (decision 10)
}

func Run(cfg Config) (*Result, error)
```

Exact sequence:

1. Pre-flight (nothing recorded on failure — M1c decision 18 precedent): remote exists (`git remote get-url`), signer loads, `BuildClosure` succeeds; push-refspec sweep warning (decision 17).
2. **Plan**: candidate commits for the winner (+ all candidates under `--include-rejected`) via `HashObject`/`Mktree`/`CommitTreeEpoch`; evidence commit likewise. Plan = ref-sorted `[]RefTip` of everything the namespace must contain.
3. **Local refs**: for each planned ref, `RefValue` then `UpdateRef` CAS to the planned sha (skip when already correct); local namespace refs not in the plan are `DeleteRef`'d (reconciliation + GC-pin bookkeeping, decisions 10–11).
4. `LsRemote` the namespace. Compute `push` (planned ref whose remote value differs; lease = observed value or expect-absent), `upToDate` (matches), `remove` (remote refs outside the plan; lease = observed).
5. Append **`publish.started`** carrying the full plan.
6. Single `Push` with all update + delete refspecs and their leases. Nothing to push ⇒ the push is skipped entirely (idempotent no-op).
7. Append **`publish.finished`** with what actually happened; on push error the event carries `error` and `Run` returns it (exit 1). A lease failure names the concurrent-publisher remedy: re-run `mvo publish`.

### Prune (`prune.go`) — FI-1 retention

```go
type PruneConfig struct {
    Repo         string
    Ledger       *ledger.Ledger
    Intent       string
    Remote       string        // "" = local-only
    OlderThan    time.Duration // 0 = no age guard
    KeepAdmitted bool          // CLI default true
}

type PruneResult struct {
    Short                       string
    DeletedLocal, DeletedRemote []string
    Kept                        []string
}

func Prune(cfg PruneConfig) (*PruneResult, error)
```

Sequence: resolve short → apply the `--older-than` guard against the latest `publish.finished` ts (decision 12) → survey local refs (`ForEachRef`) and, when `Remote != ""`, remote refs (`LsRemote`) → partition per decision 12 (losers always deleted; winner+evidence deleted unless the intent is admitted and `KeepAdmitted`) → delete local (`DeleteRef` CAS per ref), delete remote (one bulk `Push` of `:<ref>` refspecs) → append **`prune.executed`**. An empty survey still records the event (the retention action's audit trail, honest about removing nothing). CAS and ledger are never touched — append-only stands.

### Fetch & verify (`fetch.go`) — the consumer side

```go
type FetchConfig struct {
    Repo   string            // any clone with Remote configured; no workspace needed
    Remote string
    Short  string            // 12-hex namespace (CLI shortens a full digest)
    Pub    ed25519.PublicKey // trusted key (decision 13)
}

type ItemReport struct{ Path string; OK bool; Err string }

type Report struct {
    Short, IntentDig, Title, SelectDig, DecisionType, Winner string
    Admitted                                                 bool
    Freshness, FreshnessDetail                               string
    Refs                                                     []RefTip     // fetched namespace
    Items                                                    []ItemReport // path-sorted + per-ref entries
    OK                                                       bool
}

func FetchRace(cfg FetchConfig) (*Report, error)
```

Normative checks (collected, decision 14; `error` return is reserved for machinery — unreachable remote, no namespace at all):

1. `Fetch` with `+refs/multiverso/intent/<short>/*:<same>` and `prune=true`; enumerate the namespace via `ForEachRef`. The evidence ref must exist.
2. **Per-file authentication** over `LsTreeRecursive(evidence)` + `CatBlob`: plain items — `object.DigestBytes(bytes)` equals the filename's id, schema field matches the directory kind; DSSE items — `OpenItem` verifies signature + payloadType, then the **payload** digest equals the filename's id; attestation — `sha256(bytes)` equals the filename's key, envelope verifies, statement/predicate types correct.
3. **Closure graph**: exactly one intent file, digest short == namespace short; policy digest == `intent.Policy` == `decision.Policy`; every `decision.Subject` world present; every `decision.Evidence` receipt present; every receipt's `World` among the worlds; every world's `Intent` == the intent digest; no file outside the six kinds.
4. **Candidate refs**: every `cand/<n>` commit — parent == `intent.base.commit`, trailers well-formed with ordinal == `n`, `Multiverso-World` present among published worlds, commit tree == that world's `tree` (prefix-stripped). Any namespace ref that is neither `evidence` nor a verifying `cand/<n>` fails (decision 10 makes this sound). The SELECT winner's candidate ref **must** be present — publishing code you can't fetch is the failure, not a variant.
5. **Replay**: `race.Decide(policy, worlds, receipts)` reproduces the published SELECT; when admitted, `admit.Decide(policy, intent, winner, apply, gate)` reproduces the published ADMIT (apply = the `landing-apply` receipt, gate = the landing suite receipt among the ADMIT evidence, M1a rules) — Type/Subject/Evidence/Rationale byte-for-byte, CreatedAt excluded.
6. **Attestation** (admitted only): predicate digests all resolve within the closure; `producer_key_id == KeyID(pub)`; `budget_consumed.wall_ms` equals the landing receipts' sum; subject `gitTree` == the landing suite receipt's `valid_for.tree` hex (decision 15).
7. **Freshness line** via `TrunkDrift` when the base commit exists locally; UNKNOWN otherwise.

### Drift (`drift.go`) — display-only

```go
const (
    DriftFresh   = "FRESH"
    DriftStale   = "STALE"
    DriftUnknown = "UNKNOWN"
)

// TrunkDrift classifies intent.base.commit against the repo's current
// branch head. Pure read (rev-parse/merge-base); never writes anything.
func TrunkDrift(repo, baseCommit string) (status, detail string)
```

Detail strings, byte-exact (`<sha12>` = 12-hex abbreviation, `<branch>` = current branch):

| Condition | status | detail |
|---|---|---|
| head == base | `FRESH` | `base <sha12> == <branch> head` |
| merge-base == base, head ≠ base | `STALE` | `<branch> advanced past base <sha12>` |
| merge-base ≠ base (or none) | `STALE` | `base <sha12> is not an ancestor of <branch> head <sha12>` |
| detached HEAD | `UNKNOWN` | `detached HEAD` |
| base commit absent | `UNKNOWN` | `base commit <sha12> not found` |

`mvo worlds` appends `freshness: <status> (<detail>)` after its table; `mvo explain` appends the same line after `rationale:`; the fetch-race summary includes it. **Receipts' `valid_for.tree` never changes and no ledger event is emitted — staleness is what the operator sees, EP-3's recompute-on-admit is what the gate enforces** (and M3's minimal re-verification refines). A STALE intent still races, publishes, and fetches normally; only `admit` confronts drift, exactly as M1a shipped it.

## Ledger event types (M1d additions)

Observational events (M1b precedent): no replay semantics, `mvo audit` ignores them, the hash chain covers them. Bodies are canonical-JSON maps, all keys always present, arrays ref-sorted.

| Type | Payload |
|---|---|
| `publish.started` | `{"include_rejected": false, "intent": "mv0:…", "refs": [{"ref": "refs/multiverso/intent/<short>/cand/2", "tip": "<sha>"}, …], "remote": "origin", "select_decision": "mv0:…"}` — `refs` is the full plan |
| `publish.finished` | `{"error": "", "intent": "mv0:…", "pushed": [{"ref": "…", "tip": "<sha>"}, …], "remote": "origin", "removed": ["…"], "up_to_date": ["…"]}` |
| `prune.executed` | `{"deleted_local": ["…"], "deleted_remote": ["…"], "intent": "mv0:…", "keep_admitted": true, "kept": ["…"], "older_than_ms": 0, "remote": ""}` |

`cmd/mvo/state.go` gains the three constants and a `PublishFinishes []publishFinishRec{Seq, TS, Intent}` tracker (prune's `--older-than` input); nothing else in state changes.

## CLI (`cmd/mvo`)

```
mvo publish <intent-digest> [--remote R] [--include-rejected] [--dir DIR]
mvo prune <intent-digest> [--remote R] [--older-than DUR] [--keep-admitted=BOOL] [--dir DIR]
mvo fetch-race <intent-short> [--remote R] [--key PUB.pem] [--json] [--dir DIR]
```

- `publish`: `--remote` defaults to `origin`. Exit 0 on success **including** the no-op republish; 1 on failure; 2 usage. Stdout contract:

  ```
  published refs/multiverso/intent/<short> to <remote> (<P> pushed, <U> up-to-date, <R> removed)
    <ref> <sha>          ← one line per pushed ref
  ```

- `prune`: `--remote` has **no default** — omitted means local-only (deleting remote refs is explicit, always). `--older-than` parses via `time.ParseDuration` (hours are the largest unit — `720h`, not `30d`). `--keep-admitted` is a bool defaulting to `true`; `--keep-admitted=false` is the escape hatch (decision 12). Stdout: `pruned refs/multiverso/intent/<short>: <L> local, <M> remote deleted, <K> kept` + one line per deleted ref.
- `fetch-race`: accepts the 12-hex short or a full `mv0:` digest (shortened). `--key` required when no workspace key exists at `--dir` (decision 13). Exit 0 iff every check passed. Human output:

  ```
  intent:    mv0:… (<title>)
  decision:  mv0:… SELECT
  winner:    mv0:…
  admitted:  yes|no
  freshness: <status> (<detail>)
  ORDINAL  WORLD    OUTCOME    GATE  SIGNED  REF
  1        mv0:…    COMPLETED  pass  2       cand/1
  …
  OK: race verified (<N> items, <M> refs)
  ```

  On failure every bad item prints `mvo: fetch-race: <path-or-ref>: <detail>` to stderr and the final line is `FAIL: <k> of <N> items failed verification`, exit 1. `--json` emits one line: `{"schema": "multiverso.dev/fetchrace-report/v0", "short": "…", "intent": "…", "title": "…", "decision": "…", "type": "SELECT", "winner": "…", "admitted": false, "freshness": "…", "items": [{"path": "…", "ok": true, "error": ""}, …], "refs": <int>, "ok": true}`.

Usage errors (exit 2): malformed digest/short; `--older-than` unparseable or ≤ 0; both a full digest and a short that disagree; unknown flags — the M0 flag discipline throughout. `usage()` gains the three verbs.

## Push exclusion & namespace hygiene — FI-1

Stated once, normatively: `refs/multiverso/*` lives outside `refs/heads/*`, so (probe Q2, measured) default `git push` (any `push.default`), `git clone`, and `git fetch` never transfer it; collaborators opt in explicitly or use `mvo fetch-race`. `mvo` itself only ever pushes explicit refspecs under `refs/multiverso/intent/<short>/` — `refs/heads/*` cannot appear in a publish or prune refspec by construction (decision 10). Landing the admitted commit on a real remote trunk is the operator's ordinary `git push` (the `Multiverso-Attestation` trailer rides the commit; M1a); publish never does it. The only known foot-gun — a mirror-style `remote.<r>.push = +refs/*:refs/*` config — is warned about at publish pre-flight (decision 17).

## Acceptance script (CI runs this)

`scripts/accept.sh` — M1c's steps kept intact; step 3 gains one assertion, and steps 6b–6g are inserted after step 6 (verify), before step 7 (second machine). All remotes are local bare repos under the script's temp dir; the network is never touched.

- **3 (amended)**: `mvo explain "$INTENT"` output additionally contains `freshness: FRESH` (raced at trunk head, nothing has moved yet).
- **6b — publish + idempotence**: `git init --bare "$TMP/origin.git"`; `git -C "$REPO" remote add origin "$TMP/origin.git"`. `mvo publish "$INTENT" --dir "$REPO"` exits 0; `git ls-remote origin 'refs/multiverso/*'` lists exactly the winner's `cand/<n>` and `evidence`, and **nothing under `refs/heads/`** was pushed (ls-remote heads empty). Second `mvo publish` exits 0 and prints `(0 pushed`; ls-remote output is byte-identical.
- **6c — include-rejected delta**: `mvo publish "$INTENT" --include-rejected` pushes only the loser's `cand/<n>` (delta push, prior refs untouched — output shows `1 pushed`).
- **6d — fetch-race roundtrip (second machine)**: `git clone "$TMP/origin.git" "$TMP/consumer"`; `mvo fetch-race <short> --dir "$TMP/consumer" --key "$REPO/.multiverso/keys/local.pub"` exits 0, output contains `OK: race verified`, `winner:` matching patch-a's world, `admitted:  yes`, and a `freshness: STALE` line (the clone's head is the admission commit, past the intent base).
- **6e — tamper detection**: in the bare remote, rewrite one published receipt blob (python3 + git plumbing: read the evidence commit, flip one byte in a `receipts/…dsse.json` blob, `hash-object -w`, `mktree`, `commit-tree`, forced `update-ref` of the evidence ref). `mvo fetch-race` in the consumer now exits non-zero and stderr names the exact `receipts/mv0_….dsse.json` path. Then `mvo publish "$INTENT" --include-rejected --dir "$REPO"` again (heals: lease against the observed tampered tip) and `mvo fetch-race` passes once more.
- **6f — prune per policy**: `mvo prune "$INTENT" --remote origin --dir "$REPO"` (admitted intent, defaults): ls-remote shows the loser's `cand/<n>` gone, winner + `evidence` retained; `mvo fetch-race` in the consumer still passes. Then `mvo prune "$INTENT" --remote origin --keep-admitted=false --dir "$REPO"`: the namespace is empty on the remote and locally; the ledger (sqlite) holds ≥ 2 `prune.executed` events; `.multiverso/cas` file count is unchanged from before the prunes (never GC'd).
- **6g — drift marker**: `mvo worlds "$INTENT" --dir "$REPO"` and `mvo explain "$INTENT" --dir "$REPO"` both show `freshness: STALE` with the `advanced past base` detail (the admission commit moved trunk).
- **7–10 (unchanged)**: the second-machine audit now also carries publish/prune events in the chain — replay is untouched (observational events), and the final clean audit still passes.

## Testing bar

- `internal/publish` (all remotes = `git init --bare` in `t.TempDir()`; no network, no real agents):
  - `refs.go`: `IntentShort`/`CandRef`/`EvidenceRef` goldens; `FileName`/`ParseFileName` round-trip + malformed rejection; short-collision guard.
  - `closure.go`: default vs `--include-rejected` item sets (goldens on Paths); replay-completeness invariant (`race.Decide` over the closure's decoded worlds+receipts reproduces the SELECT byte-for-byte); admitted closure includes ADMIT + landing receipts + attestation; ordinal derivation across **two races of one intent** (latest SELECT wins, superseded worlds absent); determinism (two `BuildClosure` calls ⇒ byte-identical `Items`); DP-3 negative (no context/trace payload bytes appear in any item — only their CAS keys inside world objects); no-SELECT error.
  - `bundle.go`: sign/open round-trip per payload type; cross-type confusion fails (a receipt envelope never opens as a decision envelope — PAE domain separation proven); tampered payload/sig fail; deterministic envelope bytes.
  - `publish.go`: first publish pushes plan, creates identical local refs; **idempotent republish** (empty `Pushed`, remote unchanged); content change (re-race) pushes only changed refs and reconciles superseded ones (`Removed`); `refs/heads` never appears on the remote; lease failure surfaced when a second clone moves a namespace ref between plan and push; `publish.started`/`finished` payload goldens; pre-flight failures record nothing.
  - `prune.go`: policy table — non-admitted intent fully wiped; admitted keeps winner+evidence by default; `KeepAdmitted=false` wipes; losers-only deletion exact; `--older-than` refuses a young publication and errors on never-published; local-only vs `--remote`; CAS/ledger byte-untouched (file census before/after); `prune.executed` golden.
  - `fetch.go`: publish → second clone → `FetchRace` OK with every item reported; **tamper matrix, each failing loudly with the item named** — flipped receipt-bundle byte (signature), two receipt files swapped (digest-name mismatch), doctored+re-digested decision (signature), consistently doctored *unsigned* world object (caught by the signed decision's subject pin), extra unplanned ref in the namespace, missing winner candidate ref, candidate commit tree ≠ world tree, wrong `--key` (every signed item fails), tampered attestation bundle, budget-sum mismatch; replay-is-load-bearing test (hand-built wrong-winner decision signed with the *right* key still fails on replay); admitted vs non-admitted verification paths.
  - `drift.go`: five-row table on temp repos (advance trunk; rewrite history for diverged; detached HEAD; missing base); byte-exact detail strings.
- `internal/gitx`: new plumbing lifecycle on temp repos — `HashObject`/`Mktree`/`CommitTreeEpoch` determinism (same inputs ⇒ same shas across calls); `CatBlob` raw-byte fidelity (trailing bytes preserved); `LsTreeRecursive` paths; `Push`/`LsRemote`/`Fetch(prune)` against a local bare incl. lease-failure and delete refspecs; `MergeBase` incl. no-common-ancestor `("", nil)`; `RefValue` absent ⇒ `("", nil)`; `DeleteRef` CAS; `RemotePushRefspecs` missing ⇒ nil.
- `cmd/mvo`: flag-matrix usage errors (bad short, bad `--older-than`, `--keep-admitted` parsing, missing `--key` without a workspace); `worlds`/`explain` freshness-line goldens (FRESH and STALE repos); fetch-race `--json` shape; audit over a ledger containing publish/prune events replays clean with zero mismatches (observational-event proof).
- `gofmt -l` clean, `go vet ./...` and `go test ./...` pass; `scripts/accept.sh` is the e2e test and runs in CI (its bare remotes are local). `go.mod`/`go.sum` unchanged. No test pushes anywhere but `t.TempDir()`.

## Explicitly NOT in M1d

GitHub attestation-store mirroring (probe Q5: impossible with local keys — v1+, needs Sigstore keyless per TP-3); commit statuses / required checks (FI-2), GitHub App / Checks API / merge queue (FI-3); `refs/attestations/*` + `gitCommit`-subject statements (FI-4 — the evidence ref carries the bundle meanwhile); file-based ledger bundle export/import (DP-4 full — the evidence ref is the M1 hand-off; `mvo audit` on a workspace copy remains the replay path); pushing `refs/heads/*` anywhere (landing on a real remote trunk is the operator's `git push`; bot identities / ruleset bypass are the §10 onboarding prerequisite, not code); publishing races whose latest decision is REJECT/ESCALATE (no SELECT ⇒ nothing to publish; the ledger holds the record); signing intent/world/policy objects individually (transitively pinned — decisions 5–7; revisit only if the chain gains unsigned roots); in-toto Statement wrappers for receipts (decision 5 keeps the door open, v1); key rotation / multiple trusted keys / revocation (TP-5); a `mvo doctor` verb (decision 17's pre-flight warning stands in); drift *polling* daemons or `--rebase-intent` (CP-1's flag arrives with REPAIR worlds, v1); retention policy as a policy-object field (flags only in M1); pin-at-race GC refs (decision 11 documents the window; v1 if fragile); automatic multi-remote mirroring (run publish per remote); Enterprise namespace-block handling (probe's open question — fails loudly at push if a policy blocks the namespace); Windows.
