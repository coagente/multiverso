# M1a — Admission & Local Signing: Design & Contracts

> Implements [PRD](../../PRD.md) **TP-1** (DSSE signing, local key), **TP-3 local** (`mvo verify` offline), **CP-8** (never auto-resolve landing conflicts), the **ADMIT** decision (CP-6 extension), and **EP-3 v0** (recompute admission-gate oracles on `admit`). Builds directly on [M0](M0.md); every M0 contract stays in force unless amended here. Exit criterion: `scripts/accept.sh` passes end-to-end, including tamper detection.
>
> Everything here is v0 and may break until M1 exit. Requirement IDs (TP-x, CP-x…) refer to the PRD. Stdlib only: Ed25519 is `crypto/ed25519`, envelopes are JSON — **no new dependencies, `go.mod` is untouched**.

## Module layout (delta over M0)

```
internal/signing/           Ed25519 keypair + DSSE envelope (Sign/Verify, PAE)
internal/attest/            in-toto Statement v1 builder + admission predicate
internal/admit/             admission orchestrator + pure admission gate (Decide)
cmd/mvo/admit.go            mvo admit
cmd/mvo/verify.go           mvo verify
scripts/accept.sh           full acceptance script (M0 steps + admission steps)
scripts/m0-accept.sh        thin wrapper: exec accept.sh (CI compatibility)
```

Amended (never rewritten): `internal/gitx` gains commit plumbing, `internal/race` exports `EnvDigest`, `internal/workspace` gains keys, `cmd/mvo/{main,initcmd,state,audit,explain}.go` gain the new verbs/events.

## Resolved design decisions

1. **The attestation subject binds the landing *tree* and *parent commit*, not `gitCommit`.** A statement embedded (by digest) in the commit message cannot name the enclosing commit's own hash: trailer ∈ message → commit sha depends on trailer; trailer = digest(statement) and statement ⊇ commit sha would require a hash fixed point. So the subject digest is `{gitTree: <landing tree>}` and the predicate pins `trunk.parent_commit` — together they bind the commit's entire content and history position; the only unattested fields are committer identity/timestamps/message, which the trailer itself occupies. `gitCommit` subjects arrive in v1 when attestations move post-commit to `refs/attestations/*` (FI-4); nothing in this format precludes that — the DigestSet simply gains a `gitCommit` entry. `mvo verify` compensates by checking both tree and parent (below).
2. **Trailer value = the CAS key of the DSSE bundle bytes** (`sha256:<hex>`). The envelope is serialized with `object.Canonical`, and Ed25519 signatures are deterministic (RFC 8032), so the bundle bytes — hence the trailer — are a pure function of (statement, key).
3. **Landing receipts name the winner world; `freshness.valid_for` names the landing state.** No new World object is minted for the landing tree in M1a (World.parents lineage arrives with REPAIR worlds, v1). EP-3 semantics stay honest: race receipts are valid only for the race tree, so `admit` recomputes and records receipts whose `valid_for.tree` is the landing tree.
4. **The landing apply is itself a receipt** (oracle id `landing-apply`). CP-8's "conflict set as structured evidence" becomes receipt artifacts (git's stderr), and the admission gate becomes a pure function of receipts + policy — replayable by `mvo audit` exactly like race decisions.
5. **The landing oracle command is recovered from the winner's race suite receipt** (`Execution.Argv`), never from a flag. The gate that judged the race judges the landing; operators cannot swap gates at admit time.
6. **One admission per intent.** `mvo admit` refuses when an `admission.finished` event with `result=="ADMIT"` exists for the intent. Re-admission after a failed landing (trunk moved mid-admit) is allowed; REVERT is v1 (CP-7).
7. **Commits are created by plumbing** (`git commit-tree` + `git update-ref` with compare-and-swap on the old tip) under the fixed identity `mvo <mvo@multiverso.invalid>`; the operator's working tree is only fast-forwarded when it is clean and on the trunk branch. Real bot identity is FI-1 territory.
8. **Keys are PEM** (PKCS#8 private, PKIX public), mode 0600, under `.multiverso/keys/` (0700) only; key ID = `"mv0:" + hex(sha256(raw 32-byte public key))` — encoding-independent, same prefix family as object digests.
9. **The predicate carries `select_decision`** in addition to the ADMIT `decision`, and its `evidence` lists only the admission (landing) receipts. The race evidence closure is reachable via `select_decision` by digest; keeping landing receipts alone in `evidence` makes the freshness check crisp (every suite receipt in it must be valid for the commit's tree).
10. **Audit discriminates replay paths by the nearest preceding start event**: a decision replays via `admit.Decide` iff the closest `admission.started` for its intent is nearer than the closest `race.started`; otherwise via `race.Decide` (M0 behavior). `race.Decide` is untouched.
11. **`mvo verify` is workspace-local**: it needs the repo's git objects plus `.multiverso/{cas,ledger.db,keys}`. Bundle export/import for third-party verification is DP-4 (v1).
12. **`mvo init --keys`** generates a keypair into an existing (pre-M1a) workspace; plain `mvo init` still refuses to re-init and now always generates keys in fresh workspaces.
13. **Sigstore path (v1) is not precluded**: the DSSE envelope here is byte-compatible with what `sigstore-go` consumes (`payloadType` `application/vnd.in-toto+json`, PAE, `signatures[].keyid` optional); keyless signing wraps this same envelope in a Sigstore bundle with a Fulcio cert — the statement and predicate never change.

## Signing (`internal/signing`) — TP-1

Ed25519 via `crypto/ed25519`, PEM via `encoding/pem` + `crypto/x509` (both support Ed25519 in stdlib).

### Key layout

```
.multiverso/keys/            0700, created by mvo init (or mvo init --keys)
.multiverso/keys/local.key   PEM "PRIVATE KEY" (PKCS#8), 0600
.multiverso/keys/local.pub   PEM "PUBLIC KEY"  (PKIX),   0600
```

**Invariant: this package writes files only inside the `keysDir` it is handed; the only call sites pass `Workspace.KeysDir()`.** `.multiverso/` is already git-ignored by M0's `mvo init`; keys therefore never enter history and are never written elsewhere (a unit test asserts the exact file set, location, and modes after `Generate`).

### API

```go
package signing

const (
    PayloadTypeInToto = "application/vnd.in-toto+json"
    PrivName          = "local.key"
    PubName           = "local.pub"
)

// Signer is a loaded local keypair.
type Signer struct {
    Private ed25519.PrivateKey
    Public  ed25519.PublicKey
    KeyID   string // "mv0:" + hex(sha256(Public))
}

func Generate(keysDir string) (*Signer, error)  // refuses if either file exists
func Load(keysDir string) (*Signer, error)      // checks pub matches priv.Public()
func LoadPublicKeyFile(path string) (ed25519.PublicKey, string, error) // key, key ID
func KeyID(pub ed25519.PublicKey) string         // object.DigestPrefix + hex(sha256(raw 32 bytes))

// Envelope is a DSSE envelope (in-toto DSSE spec).
type Envelope struct {
    Payload     string      `json:"payload"`     // base64.StdEncoding
    PayloadType string      `json:"payloadType"` // PayloadTypeInToto for attestations
    Signatures  []Signature `json:"signatures"`
}

type Signature struct {
    KeyID string `json:"keyid"`
    Sig   string `json:"sig"` // base64.StdEncoding
}

// Sign signs PAE(payloadType, payload) and returns a one-signature envelope.
func Sign(s *Signer, payloadType string, payload []byte) (Envelope, error)

// Verify checks that at least one signature verifies against pub over
// PAE(env.PayloadType, payload) and returns the decoded payload. The caller
// asserts env.PayloadType — PAE already binds it cryptographically.
func Verify(env Envelope, pub ed25519.PublicKey) ([]byte, error)
```

### PAE (pre-authentication encoding)

```
PAE(type, payload) = "DSSEv1" SP dec(len(type)) SP type SP dec(len(payload)) SP payload
```

Lengths are byte counts as ASCII decimal, no leading zeros. Golden test vectors (the second is the DSSE spec's own):

```
PAE("application/vnd.in-toto+json", "hello")       = "DSSEv1 28 application/vnd.in-toto+json 5 hello"
PAE("http://example.com/HelloWorld", "hello world") = "DSSEv1 29 http://example.com/HelloWorld 11 hello world"
```

Base64: emit `StdEncoding` (padded); on decode accept standard or URL-safe, padded or raw (DSSE spec leniency) via one helper. `Verify` skips signatures whose `keyid` is present and differs from `KeyID(pub)` (reported in the error if nothing verifies — better diagnostics; DSSE treats keyid as a hint). Empty `signatures` is an error.

## Attestation (`internal/attest`) — PRD §5.4, G3

in-toto Statement v1 with our admission predicate. Canonicalization is `internal/object` (`object.Canonical` over the struct); the statement's canonical bytes are the DSSE payload, and `object.DigestBytes(payload)` is the **statement digest** recorded in the ledger.

```go
package attest

const (
    StatementType = "https://in-toto.io/Statement/v1"
    PredicateType = "multiverso.dev/admission/v0"
)

type Statement struct {
    Type          string    `json:"_type"`         // StatementType
    Subject       []Subject `json:"subject"`
    PredicateType string    `json:"predicateType"` // PredicateType
    Predicate     Predicate `json:"predicate"`
}

type Subject struct {
    Name   string            `json:"name"`   // "refs/heads/<branch>"
    Digest map[string]string `json:"digest"` // {"gitTree": "<40-hex, no prefix>"}
}

type Predicate struct {
    Intent         string   `json:"intent"`          // intent digest ("mv0:…")
    World          string   `json:"world"`           // winning world digest
    Decision       string   `json:"decision"`        // ADMIT decision digest
    SelectDecision string   `json:"select_decision"` // SELECT decision digest (race closure link)
    Evidence       []string `json:"evidence"`        // admission receipt digests, sorted
    Policy         string   `json:"policy"`          // policy digest
    BudgetConsumed Budget   `json:"budget_consumed"`
    ProducerKeyID  string   `json:"producer_key_id"` // signer key ID ("mv0:…")
    Trunk          Trunk    `json:"trunk"`
}

type Budget struct {
    WallMS int64 `json:"wall_ms"` // Σ Cost.WallMS over Evidence receipts
}

type Trunk struct {
    Branch       string `json:"branch"`        // e.g. "main"
    ParentCommit string `json:"parent_commit"` // trunk head the admission landed on (bare sha)
}

// New validates required fields, sorts a copy of pred.Evidence, and builds
// the single-subject statement. landingTree arrives "git:"-prefixed
// (internal convention) and is stripped here: in-toto DigestSet values are
// bare lowercase hex. This is the only prefix boundary.
func New(branch, landingTree string, pred Predicate) (Statement, error)
```

Example predicate (values elided):

```json
{
  "intent": "mv0:aa…", "world": "mv0:bb…",
  "decision": "mv0:cc…", "select_decision": "mv0:dd…",
  "evidence": ["mv0:ee…", "mv0:ff…"],
  "policy": "mv0:11…",
  "budget_consumed": {"wall_ms": 1234},
  "producer_key_id": "mv0:22…",
  "trunk": {"branch": "main", "parent_commit": "3e5a…"}
}
```

**Bundle** = `object.Canonical(Envelope)` bytes, stored in CAS; its CAS key (`sha256:<hex>`) is the **attestation digest** used in the trailer and events.

## Git backend additions (`internal/gitx`)

Same conventions as M0 (shell out, hooks disabled, `gitEnv()`, stderr folded into errors). New:

```go
// Committer identity for admission commits (deterministic; real bot
// identity is FI-1, v1). Set via GIT_{AUTHOR,COMMITTER}_{NAME,EMAIL} on the
// commit-tree command env so ambient env vars cannot override it.
const (
    CommitterName  = "mvo"
    CommitterEmail = "mvo@multiverso.invalid"
)

func CurrentBranch(repo string) (string, error)          // symbolic-ref --short HEAD; error when detached
func ResolveCommit(repo, rev string) (string, error)     // rev-parse --verify <rev>^{commit}
func TreeOf(repo, commit string) (string, error)         // rev-parse <commit>^{tree}, TreePrefix-ed
func ParentOf(repo, commit string) (string, error)       // rev-parse --verify <commit>^ (root commit → error)
func CommitMessage(repo, commit string) (string, error)  // log -1 --format=%B <commit>
func CommitTree(repo, tree, parent, message string) (string, error)
    // commit-tree <bare tree sha> -p <parent>, message on stdin, fixed identity
func UpdateRef(repo, ref, newCommit, oldCommit string) error
    // update-ref <ref> <new> <old> — compare-and-swap; fails if the ref moved
func StatusClean(repo string) (bool, error)              // status --porcelain is empty
func ResetHard(repo string) error                        // reset --hard --quiet (only called when StatusClean)

// ApplyCapture is Apply with the streams captured for evidence: applies the
// patch (git apply --index, then add -A) and returns git's stdout/stderr
// bytes; applyErr is non-nil on conflict. Apply stays as-is for race.
func ApplyCapture(dir string, patch []byte) (stdout, stderr []byte, applyErr error)
```

`CommitTree` accepts `tree` with or without `TreePrefix` and strips it. `TreeDigest`, `Head`, `AddWorktree`, `RemoveWorktree`, `Apply`, `WriteTree` are unchanged.

## Admission orchestrator (`internal/admit`) — CP-8, EP-3, TP-1

### Contracts

```go
package admit

// Oracle/receipt identifiers for the landing-apply receipt.
const (
    OracleIDLandingApply = "landing-apply"
    FamilyLandingApply   = "landing-apply"
)

// Decision types this package can emit (decision/v0).
const (
    TypeAdmit    = "ADMIT"
    TypeReject   = "REJECT"
    TypeEscalate = "ESCALATE"
)

// Config wires one admission. All fields are required.
type Config struct {
    Repo      string          // git repo root (trunk = its checked-out branch)
    Ledger    *ledger.Ledger
    CAS       *cas.Store
    Intent    string          // intent digest ("mv0:…") already in CAS
    SelectDig string          // digest of the SELECT decision being admitted
    Oracle    oracle.Oracle   // landing gate oracle (argv recovered via LandingOracleArgv)
    Signer    *signing.Signer
    AdmitDir  string          // parent directory for landing worktrees
}

// Result is what an admission produced. Run returns (*Result, nil) for
// REJECT and ESCALATE too — a refused landing is evidence, not an error.
type Result struct {
    Decision       object.Decision
    DecisionDigest string
    Branch         string // trunk branch name
    ApplyReceipt   string // landing-apply receipt digest
    GateReceipt    string // landing suite receipt digest; "" on conflict
    Commit         string // admitted commit sha; "" unless ADMIT landed
    StatementDig   string // "mv0:…" of the canonical statement; "" unless landed
    AttestationKey string // CAS key of the DSSE bundle; "" unless landed
}

func Run(ctx context.Context, cfg Config) (*Result, error)

// LandingOracleArgv recovers the race's suite oracle command from the
// SELECT decision's evidence: among receipts in sel.Evidence (loaded from
// CAS) with Family == "suite" and World == sel.Subject[0], the one with the
// smallest receipt digest wins (order-independent, same disambiguation as
// race.Decide). Error when none exists.
func LandingOracleArgv(store *cas.Store, sel object.Decision) ([]string, error)

// Decide is the pure admission gate (CP-6, NFR-1): Type, Subject, Evidence,
// Rationale depend only on (policy, intent, world, apply, gate). gate is
// nil when the apply conflicted. CreatedAt is left empty for the recorder.
func Decide(policy object.Policy, intent, world string,
    apply object.Receipt, gate *object.Receipt) object.Decision
```

`internal/race` change: `envDigest` is exported as `EnvDigest(store *cas.Store, dir string) (string, error)` (same body); `admit` reuses it and `race.LoadPolicy`.

### Run — exact sequence and ledger events

1. Load intent, SELECT decision, and winner world (`sel.Subject[0]`) from CAS; validate schemas, `sel.Type == "SELECT"`, non-empty subject. Load policy via `race.LoadPolicy(cfg.CAS, intent.Policy)`.
2. `branch := gitx.CurrentBranch(repo)` (detached HEAD → error, nothing recorded); `trunkCommit, trunkTree := gitx.Head(repo)`.
3. Append **`admission.started`** (body below).
4. `os.MkdirAll(cfg.AdmitDir, 0o755)`; `os.MkdirTemp(cfg.AdmitDir, "admit-")`; `gitx.AddWorktree(repo, dir, trunkCommit)`. The worktree is always removed on exit (best-effort on error paths; no keep flag in M1a — conflict evidence survives as CAS artifacts).
5. `patch := CAS.Get(winner.Patch)`; `stdout, stderr, applyErr := gitx.ApplyCapture(dir, patch)` timed. Build and record the **landing-apply receipt** (`receipt.recorded`):
   - `World` = winner world digest; `Oracle` = `{id: "landing-apply", version: "v0", config: digest of {"strategy": "git-apply-index"}}`.
   - `Execution` = `{argv: ["git","apply","--index","-"], exit_code: 0|1, duration_ms, isolation_tier: "T0-worktree"}` (exit_code is 0 on success, 1 on conflict — gitx does not surface git's own code).
   - `Result` = `{status: "pass"|"fail", artifacts: [stdout key, stderr key]}` — on conflict, stderr is the CP-8 conflict set.
   - `Freshness` = `{basis: "construction", valid_for: {tree: trunkTree, env: race.EnvDigest(CAS, dir) computed **before** apply}}` — applicability is a property of (patch, trunk tree); trunk movement invalidates it (EP-3).
   - `RecheckTier` = `"V1-replayable"`, `Family` = `"landing-apply"`, `Cost.WallMS` = apply duration.
6. **Conflict path (CP-8)**: `dec := Decide(policy, intent, winnerDig, applyRec, nil)` → ESCALATE; stamp `CreatedAt`; record **`decision.recorded`**, then **`admission.finished`** (`result: "ESCALATE"`). Return the Result. *Never* attempt resolution, `--3way`, or rebase.
7. Clean apply: `landingTree := gitx.WriteTree(dir)`; `landingEnv := race.EnvDigest(CAS, dir)` (post-apply). Run `cfg.Oracle.Run(ctx, dir)`; fill `World` = winner digest, `Freshness.ValidFor = {tree: landingTree, env: landingEnv}`; record **`receipt.recorded`** (the **landing suite receipt** — EP-3 v0 recompute).
8. `dec := Decide(policy, intent, winnerDig, applyRec, &gateRec)`; stamp `CreatedAt`; record **`decision.recorded`**.
9. REJECT → **`admission.finished`** (`result: "REJECT"`); return.
10. ADMIT → build the statement: `attest.New(branch, landingTree, Predicate{Intent, World, Decision: decDig, SelectDecision: cfg.SelectDig, Evidence: sorted[applyDig, gateDig], Policy: dec.Policy, BudgetConsumed: {WallMS: apply.Cost.WallMS + gate.Cost.WallMS}, ProducerKeyID: signer.KeyID, Trunk: {branch, trunkCommit}})`. `payload := object.Canonical(stmt)`; `env := signing.Sign(signer, PayloadTypeInToto, payload)`; `bundle := object.Canonical(env)`; `bundleKey := CAS.Put(bundle)`.
11. Commit message (subject line = intent title; trailers contiguous at the end):

    ```
    <intent.Spec.Title>

    Multiverso-Intent: <intent digest>
    Multiverso-Decision: <ADMIT decision digest>
    Multiverso-Attestation: <bundleKey>
    ```

    `commit := gitx.CommitTree(repo, landingTree, trunkCommit, message)`; then `gitx.UpdateRef(repo, "refs/heads/"+branch, commit, trunkCommit)`. A failed compare-and-swap (trunk moved mid-admission) appends **`admission.finished`** with `result: "ERROR"` and returns an error — the ledger honestly shows an ADMIT decision + attestation that did not land; re-running `mvo admit` is permitted (guard keys on `result == "ADMIT"` only).
12. Working-tree sync, best-effort: iff the primary worktree has `branch` checked out and `gitx.StatusClean(repo)`, run `gitx.ResetHard(repo)` (pure fast-forward of already-committed content); otherwise leave it and note on stderr that the working tree lags the branch. The clean/on-branch precondition is sampled against the old tip — between commit-tree and update-ref — because a checked-out branch always reports dirty against the new HEAD once the ref moves; only the `ResetHard` itself runs after step 11's update-ref.
13. Append **`attestation.recorded`**, then **`admission.finished`** (`result: "ADMIT"`). Return.

### Decide — pure admission gate

Gate evaluation (same semantics as race, restricted to the landing suite receipt): gate `"suite-pass"` passes iff `gate != nil && gate.Result.Status == "pass"`; **unknown gates fail** (what it cannot attest, it must not admit); a gate receipt with status `"error"` (timeout/spawn) → REJECT, never ADMIT.

Outcomes (Subject is always `[world]`; Evidence is sorted receipt digests):

| Condition | Type | Evidence | Rationale (exact fmt) |
|---|---|---|---|
| `apply.Result.Status != "pass"` | ESCALATE | `[applyDig]` | `"landing apply of %s onto trunk tree %s failed (exit %d); conflicts are never auto-resolved (CP-8) — conflict set in apply receipt artifacts"` ← `world, apply.Freshness.ValidFor.Tree, apply.Execution.ExitCode` |
| all hard gates pass | ADMIT | sorted `[applyDig, gateDig]` | `"landing gates [%s] passed on tree %s; admitting %s"` ← `join(policy.HardGates, ","), gate.Freshness.ValidFor.Tree, world` |
| any hard gate fails | REJECT | sorted digests of the non-nil receipts | `"landing gates [%s] failed on tree %s; %s"` ← gates, tree (from gate when non-nil, else apply), detail |

REJECT detail: per failed gate, joined by `", "` — `"suite-pass (no landing suite receipt)"` when `gate == nil`, `"suite-pass (status=%s)"` otherwise, `"%s (unknown gate)"` for unknown gates.

**Determinism rule (NFR-1)**: `Decide` performs no I/O and reads no clock; audit replay must reproduce `Type`, `Subject`, `Evidence`, `Rationale` byte-for-byte from recorded receipts alone. It recomputes the policy digest itself, like `race.Decide`.

## Ledger event types (M1a additions)

Bodies are canonical-JSON maps (`object.Canonical`), all keys always present (`""` when not applicable). Object-bearing events (`receipt.recorded`, `decision.recorded`) carry the object's canonical bytes, exactly as in M0.

| Type | Payload |
|---|---|
| `admission.started` | `{"intent": "mv0:…", "select_decision": "mv0:…", "trunk_branch": "main", "trunk_commit": "<bare sha>", "trunk_tree": "git:<sha>"}` |
| `receipt.recorded` | canonical Receipt bytes (landing-apply receipt, landing suite receipt) — unchanged M0 type |
| `decision.recorded` | canonical Decision bytes (ADMIT \| REJECT \| ESCALATE) — unchanged M0 type |
| `attestation.recorded` | `{"bundle": "sha256:…", "commit": "<bare sha>", "decision": "mv0:…", "intent": "mv0:…", "key_id": "mv0:…", "statement": "mv0:…", "subject_tree": "git:<sha>", "trunk_branch": "main"}` |
| `admission.finished` | `{"attestation": "sha256:…"\|"", "commit": "<sha>"\|"", "decision": "mv0:…"\|"", "error": ""\|"<message>", "intent": "mv0:…", "result": "ADMIT"\|"REJECT"\|"ESCALATE"\|"ERROR"}` |
| `key.generated` | `{"key_id": "mv0:…", "public_key": "<base64 std of raw 32-byte pubkey>"}` |

Event order per admission: `admission.started` → `receipt.recorded` (apply) → [`receipt.recorded` (gate)] → `decision.recorded` → [`attestation.recorded`] → `admission.finished`. A crash between `started` and `decision.recorded` leaves an incomplete admission — audit tolerates it (no decision, nothing to replay), same as a crashed race.

## Workspace (`internal/workspace`) — key management

- `Init` additionally creates `keys/` and generates the keypair, appending `key.generated` **after** `policy.created` (deterministic fresh-workspace event order: `policy.created`, `key.generated`).
- New methods:

```go
func (w *Workspace) KeysDir() string   // filepath.Join(w.Dir, "keys")
func (w *Workspace) AdmitDir() string  // filepath.Join(w.Dir, "admit") — landing worktree parent
func (w *Workspace) GenerateKeys() (*signing.Signer, error) // signing.Generate + key.generated event
func (w *Workspace) Signer() (*signing.Signer, error)
    // signing.Load(w.KeysDir()); on fs.ErrNotExist the error says: run `mvo init --keys`
```

No import cycle: `signing` imports only stdlib + `internal/object` (for `DigestPrefix`).

## CLI (`cmd/mvo`)

```
mvo init [--keys]
     --keys: on an EXISTING workspace, generate the keypair iff missing
     (error if present); without it, init refuses to re-init as in M0
mvo admit <intent-digest> [--dir DIR]
mvo verify <commit> [--key PUB.pem] [--json] [--dir DIR]
```

`usage()` gains:

```
  admit <intent-digest>             land the SELECT winner on trunk with a signed attestation
  verify <commit> [--key PUB] [--json]  verify the admission attestation offline
```

Exit codes unchanged (0 ok, 1 failure, 2 usage). `mvo admit` exits 0 iff the ADMIT decision landed (commit + ref updated); ESCALATE, REJECT, and errors exit 1.

### `cmd/mvo/admit.go`

1. `loadState`, `resolveIntent`. Guard: error if any `admission.finished` event for the intent has `result == "ADMIT"` (`"intent already admitted (commit %s)"`).
2. Latest SELECT: the highest-seq `decision.recorded` for the intent with `Type == "SELECT"`; none → `"no SELECT decision for intent %s (run \"mvo race\" first)"`.
3. `argv := admit.LandingOracleArgv(ws.CAS, sel)`; oracle = `&oracle.CommandOracle{Argv: argv, Timeout: time.Duration(intent.Budget.MaxWallMS) * time.Millisecond, CAS: ws.CAS}` (mirrors `cmdRace`).
4. `signer := ws.Signer()`; `admit.Run(ctx, admit.Config{Repo: ws.Root, Ledger, CAS, Intent, SelectDig, Oracle, Signer, AdmitDir: ws.AdmitDir()})`.
5. Output on ADMIT (stdout):

   ```
   ADMIT <winner world digest>
   commit:      <sha>
   attestation: <sha256:…>
   decision:    <mv0:…>
   ```

   On REJECT/ESCALATE: return `fmt.Errorf("admit: %s: %s", dec.Type, dec.Rationale)` → exit 1.

`state.go` additions: constants `evAdmissionStarted`, `evAdmissionFinished`, `evAttestationRecorded`, `evKeyGenerated`; `ledgerState` tracks `AdmissionStarts []admissionStartRec{Seq, Intent, SelectDecision}` and `AdmissionFinishes []admissionFinishRec{Seq, Intent, Result, Commit}` (other new events carry no state the CLI needs). `explain` prints the `winner:` line for `ADMIT` as well as `SELECT`.

## `mvo verify <commit>` — TP-3 (local, offline)

Requires: the repo's git objects and `.multiverso/{cas,ledger.db}`; trusted key defaults to `.multiverso/keys/local.pub`, overridden by `--key <path>` (PEM PKIX Ed25519). Fully offline; the trailer is the only thing taken from the commit — everything else is verified against CAS/ledger content.

Checks, in order, fail-fast (the first failure names the check and detail, exit 1):

1. **bundle_digest** — resolve `<commit>` (`gitx.ResolveCommit`); read the message; the **last** line matching `^Multiverso-Attestation: (sha256:[0-9a-f]{64})$` is the bundle key (none → fail). `CAS.Get(key)`; `sha256(bundle bytes)` must equal the key (content-address integrity — catches the acceptance byte-flip).
2. **signature** — decode Envelope JSON; `env.PayloadType == "application/vnd.in-toto+json"`; `signing.Verify(env, pub)` returns the payload. Report the signing `keyid`.
3. **statement** — decode Statement; `_type == StatementType`, `predicateType == PredicateType`; exactly one subject whose digest map is exactly `{"gitTree": <40-hex>}`.
4. **subject** — subject `gitTree` == bare hex of `gitx.TreeOf(commit)`; subject name == `"refs/heads/" + predicate.trunk.branch`; `predicate.trunk.parent_commit == gitx.ParentOf(commit)` (tree + parent bind the commit's full content and position — decision 1).
5. **references** — all of:
   - `predicate.intent`, `predicate.world`, `predicate.policy`, `predicate.decision`, `predicate.select_decision`, and every `predicate.evidence[i]` present in CAS (via `object.CASKey` + `Has`);
   - one ledger scan confirms matching `payload_dig` events: `intent.created` for intent, `world.created` for world, `decision.recorded` for both decisions, `receipt.recorded` for each evidence digest;
   - decoded ADMIT decision is consistent: `Type == "ADMIT"`, `Intent == predicate.intent`, `Subject[0] == predicate.world`, `Policy == predicate.policy`, `Evidence` equals `predicate.evidence` exactly (both sorted);
   - decoded SELECT decision: `Type == "SELECT"`, `Intent == predicate.intent`, `Subject[0] == predicate.world`;
   - `predicate.producer_key_id == KeyID(trusted pub)`.
6. **freshness** (EP-3) — load each evidence receipt from CAS; every receipt's `World == predicate.world`; every receipt with `Family == "suite"` has `Freshness.ValidFor.Tree == "git:" + <commit tree hex>` (the landing gate judged exactly the admitted tree); the `landing-apply` receipt has `Freshness.ValidFor.Tree == "git:" + <parent commit's tree hex>`.
7. **budget** — `predicate.budget_consumed.wall_ms` == Σ `Cost.WallMS` over the evidence receipts.

Human output on success:

```
commit:      <sha>
attestation: <sha256:…>
key:         <mv0:…>
OK: attestation verified (7 checks)
```

On failure: `mvo: verify: <check>: <detail>` on stderr, exit 1.

`--json` (single line, like audit): 

```json
{"schema": "multiverso.dev/verify-report/v0", "commit": "<sha>",
 "attestation": "sha256:…", "key_id": "mv0:…",
 "checks": {"bundle_digest": true, "signature": true, "statement": true,
            "subject": true, "references": true, "freshness": true, "budget": true},
 "ok": true, "error": "<omitempty>"}
```

Checks default `false`; each is set `true` when it passes; on failure `ok=false`, `error` = `"<check>: <detail>"`, and the report is still emitted before exit 1.

## `mvo audit` extension — NFR-1, CP-6

`VerifyChain` unchanged. Replay now discriminates per decision at seq `S` for intent `I`: let `r` = seq of nearest preceding `race.started` for `I`, `a` = seq of nearest preceding `admission.started` for `I`; **admission replay iff `a > r`**, else race replay (M0 path, including `a == r == 0`).

Admission replay inputs (all from the ledger scan / CAS — no git, no clock):

- `policy := race.LoadPolicy(ws.CAS, dec.Policy)`.
- The `admission.started` at `a` names `select_decision`; that digest must match a recorded `decision.recorded` with `Type == "SELECT"` (else a mismatch entry `"select decision %s not in ledger"`); `winner := sel.Subject[0]`.
- Window receipts: `receipt.recorded` events with `a < seq < S` and `World == winner`. Apply receipt = smallest-digest one with `Oracle.ID == "landing-apply"`; gate receipt = smallest-digest one with `Family == "suite"` (nil if none). Missing apply receipt → mismatch entry.
- `replayed := admit.Decide(policy, dec.Intent, winner, apply, gate)`; compare with `diffDecision` (Type/Subject/Evidence/Rationale byte-for-byte, CreatedAt excluded) exactly as for races.

`auditReport` gains `"admissions": <int>` (decisions replayed via the admission path); `Decisions` continues to count all replayed decisions; schema string stays `multiverso.dev/audit-report/v0` (additive field, v0 discipline). Audit is deliberately **signature-agnostic**: cryptographic verification of attestations is `mvo verify`'s job; audit proves the decisions the attestation cites were honestly derived.

## Acceptance script (CI runs this)

`scripts/accept.sh` — M0's five steps, extended; `scripts/m0-accept.sh` becomes a two-line wrapper (`exec "$(dirname …)/accept.sh" "$@"`) so existing CI keeps working.

1. Temp git repo from `testdata/toyrepo` (unchanged).
2. `mvo init`; `mvo intent new`; `mvo race` with the two shipped patches (unchanged).
3. `mvo explain` shows SELECT with patch-a's world as winner (unchanged).
4. **Admit**: `PRE=$(git log -1 --format=%H)`; `mvo admit "$INTENT" --dir "$REPO"` exits 0; `git log -1 --format=%H` differs from `$PRE`.
5. **Trailer**: `git log -1 --format=%B` contains a line matching `^Multiverso-Attestation: sha256:[0-9a-f]{64}$`.
6. **Verify**: `mvo verify HEAD --dir "$REPO"` exits 0; `mvo verify HEAD --json --dir "$REPO"` reports `ok == true`; and `mvo verify HEAD --key "$REPO/.multiverso/keys/local.pub" --dir "$REPO"` exits 0 (exercises `--key`).
7. **Second machine**: copy the repo dir; `mvo audit --json` there asserts `chain_ok` and `replay_identical` — the replay now includes the admission decision.
8. **Tamper (bundle)**: in the copy, extract the trailer digest, locate `.multiverso/cas/sha256/<h:0:2>/<h:2>`, flip one byte (python3, `b[0] ^= 0xFF`); `mvo verify HEAD` in the copy must now FAIL (non-zero exit; the bundle_digest check names the corruption).
9. **Tamper (ledger)**: corrupt one ledger payload byte in the copy (M0 step 5); `mvo audit` there fails with a chain error.
10. **End-to-end**: `mvo audit` in the ORIGINAL repo still prints the OK line and exits 0. Then `echo "accept: OK"`.

## Testing bar

- `internal/signing`: PAE golden vectors (both above); sign/verify round-trip; tampered payload, tampered sig, wrong key, wrong keyid all fail; base64 leniency on decode; `Generate` writes exactly `{local.key, local.pub}` under keysDir with modes 0600 (dir 0700) and refuses overwrite; `Load` rejects mismatched key files.
- `internal/attest`: golden canonical statement bytes (digest stability); `New` sorts evidence, strips the `git:` prefix, and rejects empty required fields.
- `internal/admit`: `Decide` table tests — ESCALATE on apply fail, ADMIT on pass, REJECT on gate fail / gate error / nil gate / unknown gate; byte-for-byte rationale stability; `LandingOracleArgv` disambiguation (smallest digest) and missing-receipt error. Integration test on a temp repo: full admit lands a commit whose trailer resolves to a verifying bundle; a conflicting trunk commit before admit yields ESCALATE with the conflict stderr in CAS; second admit after a landed one is refused.
- `internal/gitx`: new function lifecycle on a temp repo, including `UpdateRef` compare-and-swap failure and `CurrentBranch` on detached HEAD.
- `cmd/mvo`: verify happy path plus one test per failing check (7); audit replays admission decisions and reports a mismatch for a doctored admission decision.
- `gofmt -l` clean, `go vet ./...` and `go test ./...` pass; `scripts/accept.sh` is the e2e test and runs in CI. `go.mod`/`go.sum` unchanged.

## Explicitly NOT in M1a

Agent adapters (AG-1), T1 containers, `refs/multiverso/*` publication and receipt signing on publish (FI-1 — no publish surface exists yet), Sigstore keyless (TP-3 v1), REPAIR worlds / policy-gated conflict repair (CP-8 v1), REVERT/FREEZE (CP-7), bundle export/import (DP-4), gitCommit-subject attestations in `refs/attestations/*` (FI-4), `mvo evidence` verb, `--dry-run` pricing, key rotation/revocation (TP-5), protected-trunk/bot identity, parallel execution, Windows.
