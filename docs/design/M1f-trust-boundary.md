# M1f — The Trust Boundary: Design & Contracts

> Answers the finding of [the 2026-08 design partner study](../studies/2026-08-design-partner-study.md): **the candidate writes its own evidence.** `docs/concepts.md` states the governing rule — *"Oracles outrank agents. Agent output is a claim. Only oracle receipts advance lifecycle state."* — and M1e's suite oracle violated it, running pytest inside the candidate's world, writing JUnit XML into the candidate's own writable tree, and content-addressing that file after the process exited. Five laundering vectors were reproduced end to end; four passed every hard gate; one won a race and landed on trunk.
>
> M1f moves the boundary. It severs evidence production from the candidate's world (a control-plane-owned event stream replaces the in-tree report file), adds the one gate that closes three vectors at once (**protected paths**, evaluated by comparing tree blob hashes — not by counting tests), adds **cross-oracle invariants** so a three-test repository can no longer report a 500-test suite, corrects the shipped default policy (drop `wall_ms_asc`, turn on `escalation.on_ranking_tie`), and stops `mvo audit` over-claiming by sweeping CAS for everything the ledger references.
>
> It also states plainly what it does **not** close. A determined adversary who executes Python inside the test process can corrupt anything pytest itself reports; no in-process mechanism prevents that, and this document ships no cryptography pretending otherwise. Every receipt therefore records **which evidence regime produced it**, so a reader knows which guarantee applies to the numbers in front of them.
>
> Implements the remainder of **EP-7** to its intent (native artifacts content-addressed before parsing — now also *produced* outside the parsed party's reach), **TP-4**'s M1-shaped half (generator/verifier separation: the generator's process no longer authors the verifier's output), **AG-4**'s discipline extended from diffs to evidence (control-plane-owned capture), and **NFR-1** (audit verifies what it claims to verify). Grounded in [research ch. 13](../../research/13-ai-control-untrusted-generators.md) — the untrusted-generator threat model this block finally implements — and [ch. 8](../../research/08-oracles-verification.md) (buy independence, not volume; passing visible tests is adversarially worthless as sole evidence). Builds on [M0](M0.md), [M1a](M1a-admit-signing.md), [M1b](M1b-agent-adapters.md), [M1c](M1c-containers-parallel.md), [M1d](M1d-publication.md), [M1e](M1e-policy-oracles.md); every prior contract stays in force unless amended here.
>
> **Stdlib only; `go.mod`/`go.sum` untouched** (`embed` is stdlib). Tests never invoke real agent CLIs (M1b rule). Python tooling is invoked by oracles at runtime, inside worlds; no test may require a plugin to be installed. POSIX only (the evidence channel is a FIFO); Windows stays out of scope, as in M1e.
>
> **Compatibility, stated up front and proved by the acceptance script: M1f replays every M0–M1e ledger byte-for-byte.** M1e decision 1 is why — nothing about a recorded decision is re-derived from a re-serialization of its inputs — and M1f adds one rule of its own (decision 3) that keeps it true through a policy-schema amendment: *every additive `policy/v1` field's compiled default must reproduce M1e semantics exactly, and no new rationale text may be emitted on any input an M1e policy can produce.*

## Module layout (delta over M1e)

```
internal/oracle/stream.go        evidence stream: FIFO lifecycle, framed reader, cap, parse
internal/oracle/pyplugin/        mvo_evidence.py — the control-plane pytest plugin
internal/oracle/pyplugin/embed.go  //go:embed + its content address (computed once)
internal/oracle/guard.go         tree-guard oracle: two tree walks, the violation report
internal/policy/paths.go         the closed glob grammar, its matcher (no regexp),
                                 PathSpec validation + compile
                                 (planned as internal/oracle/pathset.go + a policy half;
                                  shipped as one file, because the matcher IS policy data)
internal/policy/invariant.go     the closed invariant vocabulary + compiled form
internal/race/invariant.go       invariant evaluation over counted receipts (pure)
internal/audit/sweep.go          CAS integrity sweep: referenced-set walk, rehash, report
cmd/mvo/guard.go                 mvo guard — the adoption wedge (no ledger, no race)
testdata/toyrepo/patches-forge/  one reproduction per study vector, plus two variants
                                 that move the forgery out of the harness and into
                                 application code (the second line of defence)
testdata/toyrepo/policies/       no-paths.json, sealed.json, paths-relaxed.json
testdata/stream/                 recorded evidence-stream fixtures for the pure parser
```

Amended (never rewritten): `internal/object` gains two `Execution` fields, the regime constants, and three additive `PolicyV1` fields; `internal/policy` gains two gate predicates, one oracle kind, one family, six metrics, the path/invariant vocabularies and their validation rules; `internal/oracle` rewrites `pytest.go` around the stream and gains the guard kind in the registry; `internal/backend` gains `OpenOpts` (three extra mounts) and a distinct oracle uid; `internal/race` builds oracle instances **per world**, evaluates invariants, and gains escalation rule 0; `internal/admit` escalates on an invariant violation; `cmd/mvo/{audit,explain,policy,race,worlds}.go` gain the sweep, the renderings, the pre-flight and `--builtin`; `scripts/accept.sh` gains six steps and amends three.

## Threat model — who we are defending against, and where the line is

Chapter 13's posture, made concrete for the one thing M1 actually runs: a pytest process, in a tree an untrusted generator wrote.

**The adversary's capabilities.** The candidate is arbitrary source code. During an oracle run it obtains **arbitrary Python execution inside the test process** — not merely through `conftest.py`, but through every module the tests import, including the module under test. There is no configuration of pytest that removes this: the tests *are* Python, and the code they exercise *is* the candidate's. Anything reachable from inside that process is therefore adversary-controlled: `sys.modules`, `atexit`, the process exit status, any file the process's uid can write, and any plugin object registered in the same interpreter.

**What follows, stated without hedging.** *Nothing pytest reports about itself is trustworthy against a determined in-process adversary.* A design that claims otherwise is lying. What a control plane **can** hold is:

1. **Bytes it hashed itself.** The candidate tree's blobs are captured by the control plane (AG-4). The adversary chose the content; it cannot change what the content *is*. This is the only fully trustworthy evidence M1f has, and it is the foundation of the protected-paths gate.
2. **Observations it made before the adversary could react.** An append-only stream the control plane reads live is immutable in its already-received prefix. The atexit-class attacks — the two the study reproduced — are attacks on *state read after the fact*. They die when nothing is read after the fact.
3. **Disagreement between sources the adversary must corrupt coherently.** Forging one number is cheap; forging a set of numbers that a second, independently derived source also reports is strictly harder, and forgetting one is a detection.
4. **Capabilities it can actually remove.** A uid that cannot write a directory cannot create a file in it. This is an OS guarantee, not an inference — and it is the only thing in this document that upgrades "detected" to "impossible".

**Where the line lands.** M1f is an **ACL-2-class** defence in ch. 13's framing: it defeats an attacker without knowledge of protocol internals and without cross-context memory, and it converts several attacks-with-knowledge from silent successes into loud failures. It does not defeat an attacker who reads this document, writes their forgery into application code, and patches the plugin's report hook before the first failing test reports. That residual is enumerated in [What is stopped, what is detected, what is open](#what-is-stopped-what-is-detected-what-is-open), and it is why the receipt carries a regime label instead of a pass/fail badge.

## Resolved design decisions

1. **The trust boundary is the process boundary, and evidence crosses it as a stream, not as a file.** Metrics are derived from a framed event stream that a control-plane-owned pytest plugin writes to a FIFO the control plane created and reads **live**, while the process runs. Nothing in the candidate's tree is read as a metric source after the process exits. This is the whole fix for the two vectors the study reproduced: both are "write the answer at the end and let mvo find it", and after M1f there is no end-of-run read. The JUnit file, the reportlog and the `--collect-only` summary line survive **only as cross-check sources** (decision 9) and as stored artifacts; they never supply a number that reaches a gate.
2. **The candidate can append to the stream; it can never be silenced into a pass.** An in-process adversary can `os.write` forged records — that is unremovable. What the control plane refuses is *absence*: a run whose stream is missing, malformed, or lacks a well-formed terminal record yields **absent metrics**, and a process that exited 0 with no usable stream is `status = error`, never `pass` (rule S1, [Structural rules](#structural-rules-s1s3)). So the cheapest attack on a streaming oracle — unregister the plugin from `conftest.py` — buys a failed gate, not a green one. This is the honesty rule ("an absent source yields an absent metric; a gate that cannot be evaluated fails") made load-bearing rather than decorative.
3. **`policy/v1` is amended additively; no `policy/v2` is minted — and the rule that makes that safe is written down.** Three fields are added (`paths`, `invariants`, `evidence`). An additive `v1` field is legal **iff its compiled default reproduces M1e semantics exactly**, and no new rationale text may be emitted on any input an M1e policy can produce. Both hold: `invariants` defaults empty (no new evaluation, no new sentence); `paths` is only consulted by a gate no M1e policy can name; `evidence` selects how *future* runs are observed and never enters `Decide`. Minting v2 would fork every pinned intent and force a migration for a purely additive change, against M1e decision 2's rule that the bytes are the identity. Forward incompatibility is accepted and expected: an M1e binary refuses an M1f policy at load (M1e decision 7's `DisallowUnknownFields`), which is the correct failure — a binary that cannot enforce a policy must not pretend to.
4. **Additive `v1` fields use `""` as the sentinel for the M1f default, never a Go zero-value boolean.** `evidence.regime: ""` compiles to `auto`; `evidence.crosscheck: ""` compiles to `require`; `paths.protected_additions: ""` compiles to `allow`. A `bool` whose `false` is the weaker setting would make an old policy silently *less* checked the moment a field was added — the exact "weak default, silent label" failure this project exists to refuse. Tri-state strings are verbose; they are also the only encoding where absence and "off" are distinguishable, which is the same reason `result.metrics` records absence rather than zero.
5. **Protected paths are a gate over tree blob hashes, not over test counts.** For every path in the **base** tree matching the policy's declared sets, the candidate tree must carry an identical blob (and mode); deletion counts as modification; a *new* file matching the harness set is a violation. This is the single change that closes vectors 3, 4 and 5 in one move, it costs two `git ls-tree` walks and no process execution, and it is the one gate whose evidence the adversary provably cannot author. Counting tests cannot do this job: `@pytest.mark.skip`, a weakened assertion and a rewritten expected value all leave `collected_delta = 0`, which is precisely how the study's cheats passed.
6. **Two path classes, because "add a test" is the behaviour we want and "add a conftest" is the attack.** `protected` paths are frozen against modification and deletion, and additions are allowed by default (a candidate that adds a regression test is doing the right thing). `harness` paths are frozen against modification, deletion **and creation** — the study's forgery patch's entire content was a new `conftest.py`, and a repository that has no conftest today must not acquire one from an untrusted generator. Collapsing the two classes would either forbid new tests (useless) or permit new conftests (the vector).
7. **The path matcher is a closed glob grammar, not a regexp.** Same justification as M1e decision 6: a policy is data that reaches the decision core, and a regexp engine is an unbounded execution surface with catastrophic-backtracking failure modes. The grammar is: patterns are matched against repo-root-relative, forward-slashed paths; `**` matches zero or more whole segments; `*` and `?` and `[...]` match within one segment (Go `path.Match` semantics per segment); no alternation, no negation, no anchors. A pattern with no `**` and no `/` matches **only at the repo root**, so `conftest.py` is root-only and `**/conftest.py` is required for nested files — explicit, testable, and surprising to nobody who reads `mvo policy show`.
8. **The guard is an oracle, not a special case in `Decide`.** `tree-guard` is a registry kind whose receipt carries the violation counts as metrics and the full violation list as a CAS artifact, and `paths-unmodified@guard` is an ordinary gate reading an ordinary counted receipt. Everything downstream then works for free: ladder ordering (the guard is rung O-1 and short-circuits before any Python runs, so a test-editing candidate never costs a suite run), `mvo explain`'s gate table, the ESCALATE payload, admission re-gating, replay, and publication. Putting it in `Decide` would have made the decision function impure and unreplayable.
9. **Cross-source disagreement is `status = error`, not `status = fail` — brittleness escalates, it never convicts and never acquits.** The stream is the metric source; JUnit XML and the `--collect-only` summary line are re-derivations of the same session by a different mechanism. Honest runs agree. When they disagree, either someone forged one of them or a pytest/plugin version changed a counting rule — and mvo cannot tell which. `error` is the honest verdict: it fails every gate (so nothing lands) while `escalation.on_all_worlds_failed_machinery` routes it to a human rather than recording "these candidates are bad". `evidence.crosscheck: "off"` exists for repositories whose plugins genuinely break the equivalence; it is a pinned, attested policy choice, not a runtime flag.
10. **Cross-oracle invariants are a declared, closed, named set — the same shape as escalation rules, for the same four reasons.** An invariant is `{name, oracles: {role → declared oracle name}}`: the vocabulary fixes which metrics each role supplies and how they are compared; the policy only says *which instances* fill the roles. Totality (a closed struct cannot fail to evaluate), threat model (no evaluator over evidence inside the trust boundary), explainability (one frozen sentence per invariant), and validation (unknown name = load error) — M1e decision 6's argument transfers unchanged. Two ship; adding a third is a table entry, reviewed once, not a mini-language shipped forever.

    > **Limit, stated after the red team proved it (2026-08-15).** A cross-check between two sources that are BOTH produced inside the trust boundary cannot detect a **coherent** forgery. `collect-equals-suite-total` compares a collect run's count with a suite run's total; an adversary with in-process execution authors both, and forging them to agree costs one extra line. The red team did exactly that — a repo whose real suite is `2 failed, 6 passed` produced `collect (collected_total=500)`, `suite (tests_passed=500, tests_total=500)`, `INVARIANTS: ok`, and landed on trunk. What the invariant actually buys is threat-model point 3: it catches the adversary who forged one number and forgot the other, which is a real and common failure of hand-written cheats and is why vector `09-collect_count_forgery` was caught under M1e. It is **not** a detector of a competent forgery, and the [What is stopped](#what-is-stopped-what-is-detected-what-is-open) table says so. Making it one requires a count derived OUTSIDE the test process — control-plane static AST collection is the obvious candidate and is not in M1f.
11. **A violated invariant makes the world fail *and* escalates the race.** Failing alone is not enough: a silent REJECT would file a *detected forgery* under the same heading as "the tests didn't pass", and the operator would never learn that something lied to them. `escalation.on_invariant_violation` is therefore rule **0** — the highest precedence, above `on_all_worlds_failed_machinery` — and it is ON in the shipped default. Inserting a rule at the front of a fixed precedence list cannot change any historical evaluation, because no M1e policy can declare an invariant. At admission the same violation is an **ESCALATE**, not a REJECT, for the same reason: one subject, no ranking, and a contradiction on the landing tree must reach a human.
12. **Metrics that the candidate can author are labelled, not banned.** `coverage_bp` comes from a coverage.py data file the test process writes; it is candidate-influenceable under *every* regime and there is no cheap fix. It stays in the vocabulary, stays out of the default policy, and is documented in the [metric provenance table](#metric-provenance) as what it is. Deleting it would be tidier and less honest: a coverage gate raises the cost of laundering and proves nothing, and saying so is more useful than pretending the metric does not exist.
13. **Three regimes, chosen by capability, recorded in every receipt.** `control-plane` (the guard: no candidate code runs at all), `isolated` (T1 + a distinct oracle uid + a read-only worktree + a write-only FIFO in a directory the oracle uid cannot write), `streamed` (any tier: the FIFO exists but the oracle uid can replace it — replacement yields an absent stream and a failed gate, never a forged pass). `in-tree` remains as a fourth, explicitly-declared value reproducing M1e behaviour for operators who need it; it is never selected by `auto`. The default tier is T0, so **the default regime on a laptop is `streamed`** — said loudly here and printed by `mvo explain`, because a guarantee nobody can see the absence of is a guarantee nobody has.
14. **The receipt says which regime applied.** `execution.evidence_regime` and `execution.evidence_plugin` (the content address of the plugin source that observed the run) join the EP-7 envelope, next to `isolation_tier` — the field they are most like. A receipt that does not say how it was observed invites the reader to assume the strongest reading, which is exactly the failure the study found in `mvo verify`'s `OK: attestation verified (7 checks)` over a forged number.
15. **No MAC on the evidence stream, and the reason is worth stating.** A message authentication code needs a key, the key must be readable by the plugin, the plugin runs in the process the adversary controls, and the adversary gains control at import time — before the first test reports. A ratchet that destroys prior key state does not help either, because the adversary is present from the start. Shipping a MAC would produce a page of crypto that stops nothing and a sentence in the README that stops a reviewer from reading further. The `MVO_EVIDENCE_NONCE` in the stream header is an **identity tag against staleness** — it proves the stream belongs to the run we started, defeating a replayed stream left in a tree — and it authenticates nothing against an in-process adversary. That is what it is documented as.
16. **`--noconftest` was considered and rejected; sealing `conftest.py` is the viable form of the same idea.** `--noconftest` disables the file honest projects use for fixtures, `sys.path` setup and markers — it breaks correct repositories far more often than it inconveniences an attacker, who has the test modules and the module under test either way. It also does not disable `pytest.ini`/`pyproject.toml` `addopts` or `sitecustomize.py`, both of which the harness path set does close.

    > **Correction (red team, 2026-08-15).** The sentence this decision originally ended on — *"It also does not disable … entry-point plugins … The harness path set closes the same surface without breaking anyone"* — **was false about entry-point plugins, and the red team walked through it end to end under the shipped default.** pytest imports `pytest11` entry points from any `*.egg-info` / `*.dist-info` directory on `sys.path`; the candidate tree root is on `sys.path` when the oracle runs `python3 -m pytest`; and the module such metadata names may be called **anything**. A path set is a finite list of globs, so it cannot enumerate "any module named by any metadata directory" — the surface is not glob-shaped and no version of decision 6 was ever going to close it. The entry-point plugin loads **after** `-p mvo_evidence`, in the same interpreter, which was enough to pop `MVO_EVIDENCE_STREAM`/`_NONCE` out of the environment, leave the control plane's own observer inert, and author a complete self-consistent forged stream, a matching JUnit file and an exit status of 0. The result was `SELECT`, `mvo admit` onto trunk, `mvo verify HEAD` → `OK: attestation verified (7 checks)`, and `mvo audit` → `OK`, with the bug intact. Reproduced as corpus vector `13-entrypoint_plugin`. **Decision 24 is the fix**, and it is a capability removal rather than a glob.

24. **Entry-point plugin autoloading is SEALED by default: `evidence.plugin_autoload`.** Oracle pytest runs carry `PYTEST_DISABLE_PLUGIN_AUTOLOAD=1`, so the only plugins that load are the ones mvo names on argv. This is decision 13's fourth lever — *capabilities it can actually remove* — applied to the one surface decision 16 wrongly believed a path set covered. The tri-state field follows decision 4: `""` ⇒ `"off"`, so an M1e-era policy, which cannot name the field, gets the seal rather than the hole.

    `"on"` is the escape hatch, and it exists because sealing autoload is not free: a repository whose suite genuinely needs an installed plugin (`pytest-asyncio`, `pytest-django`, `anyio`) will collect nothing without it. That is a **pinned, attested policy choice with a stated cost**, exactly the shape of `crosscheck: "off"` (decision 9) — not a runtime flag, and visible in `mvo policy show`'s `evidence:` line. The two optional plugins mvo itself requests (`pytest-reportlog`, `pytest-rerunfailures`) are named on argv explicitly under the seal, so the probe's finding still reaches the run.

    Depth, not a single line: `**/*.egg-info/**` and `**/*.dist-info/**` also join the default **harness** set. A glob cannot close the surface, but a candidate that ships distribution metadata into a repository that had none is a fact a reviewer should see, and the guard stops that patch at rung O-1 before any Python runs. Both defences are under live test — corpus vector 13 dies at the guard under the shipped default, and dies at `status-pass@suite` with the real numbers (`tests_failed=2`) under `no-paths.json`, where the guard is not what catches it.

25. **`.gitignore` is sealed, for a sharper reason than any other harness entry.** It decides what `git add -A` stages, and the guard's entire evidence is a git tree. A candidate that may edit `.gitignore` can hide a harness file from the only oracle whose evidence it cannot author — pytest loads a `conftest.py` that `.gitignore` names, and the tree comparison never sees it. The race path was already defended (a force-applied file is in the world's index, and the drift between that tree and a fresh snapshot is a `tree_drift`); `mvo guard` was not, because it has no recorded world tree to drift against. `mvo guard` therefore also lists path-set files that exist in the working tree and are excluded by ignore rules, and **refuses to print the clean verdict** when there are any: a false clean verdict from the adoption wedge is worse than no verdict at all.
17. **`pyproject.toml` is sealed by default, and the friction is acknowledged rather than engineered away.** The threat is one line: `addopts = "-p no:mvo_evidence"`. Detecting only that would require parsing TOML and reasoning about semantics — a semantic gate, when the whole strength of this gate is that it compares bytes. So the whole file is sealed at the repo root (not recursively; a sub-package's `pyproject.toml` is not the pytest config and sealing it would block ordinary dependency work). The cost is that a candidate cannot add a dependency, which is an intent that *should* reach a human. The defence is layered anyway: an injected `-p no:mvo_evidence` produces an absent stream, which fails the gate (decision 2), so the seal is depth, not the only line. `testdata/toyrepo/policies/paths-relaxed.json` shows the one line to delete, and the docs say what deleting it costs.
18. **Oracle instances are built per world, not per race.** The guard needs the candidate's tree digest, which is per world; making `oracle.New` per-world is a two-line orchestrator change with no I/O cost, and it removes the temptation to smuggle per-world state through the `Oracle` interface or a type assertion. `oracle.Params` gains `Repo`, `BaseTree` and `CandidateTree`.
19. **The guard's base tree is `intent.base.tree` in a race and the pre-apply trunk tree at admission** — the same denominator logic as M1e decision 13, for the same reason: "did this patch touch the harness" is a question about what the patch does to what is landing, and at admission that is trunk.
20. **The default ranking drops `wall_ms_asc` and turns on `escalation.on_ranking_tie`.** The study measured an assertion-weakening cheat winning 6 of 10 identical races on ~100 ms of wall-clock jitter, each time with a signed rationale naming `wall_ms_asc (3809 < 3922)` as the decisive key. Wall time is noise, and a tie-break on noise is a coin flip wearing a signature. The corrected default ranks `[gate_pass, tests_passed_desc]` and escalates when the leader ties the runner-up on every key: **a correct refusal beats a confident wrong answer**, and `mvo explain` already renders an ESCALATE without the word "won" (M1e decision 21). `wall_ms_asc` stays in the vocabulary — policies that genuinely want it can have it — and stays out of what we ship to a new user.
21. **The new default mints a new policy digest, and that is what pinning is for.** Existing intents keep the digest they pinned; their races and replays resolve the old policy from CAS (never pruned, M1d) and decide exactly as they always did. Upgrading the binary does **not** rewrite `.multiverso/policies/default.json` in an initialized workspace — mutating an operator's pinned artifact behind their back is precisely the thing content addressing exists to prevent. Adoption is two explicit commands (`mvo policy show default --builtin --json > … && mvo policy use default`), and `mvo policy list` shows both digests with their states.
22. **`mvo audit` gains a CAS integrity sweep over everything the ledger references, and the referenced set is declared in this document.** The study found audit reporting `OK` after an attestation blob was deleted, and the quickstart claiming audit caught CAS edits it never inspected. The sweep re-reads and re-hashes every digest the ledger references, from a table that is part of the contract (not a best-effort walk), and reports `cas_missing` / `cas_corrupt` as **exit-1 failures**. Unreferenced objects are counted and never failed — CAS legitimately holds more than one ledger references. And the sweep's limits are documented next to it: it proves the recorded closure is intact; it cannot prove something was recorded that never was.
23. **Nothing verified is never `OK`, and CI gets an explicit knob rather than a redefinition.** `mvo audit` on a workspace with no decisions keeps exit 0 and keeps saying so in words (the fix already shipped); making it fail would redefine the verb's meaning for everyone who runs it as a smoke test. `--require-decisions N` is the CI knob, and it fails loudly with the count it found. Silence about scope is the over-claim; a wrong exit code is not the fix for it.

## Evidence regimes

> **Implementation amendment (integration gate, 2026-08-15).** Two things in this section describe a design that did not ship as written, and the receipts must match the code rather than the plan.
>
> 1. **`isolated` is defined but NOT deliverable, and is therefore never selected.** `t1Mounts` builds all four mounts, but `t1World.Command` execs with workdir `/work` (read-write) and no `--user`, so an oracle runs as the invoking uid — the uid that owns both the worktree and the evidence directory. None of the three "what the candidate cannot do" cells in the table below hold. The first cut of `resolveRegime` nevertheless returned `isolated` for every T1 race and recorded it in `execution.evidence_regime`; a real T1 race under the shipped default produced receipts reading `evidence_regime: "isolated"` beside `isolation_caps.user: "501:20"`. That is a signed claim stronger than the evidence behind it — the study's finding, with the label doing the laundering. **`auto` now resolves to `streamed` on every tier, and a policy declaring `isolated` is refused at pre-flight as machinery** (message below), never downgraded quietly. `internal/race/evidence_test.go` pins this, including a guard rail so that flipping `isolatedExecAvailable` must accompany the `--user`/`/work-ro` exec change.
> 2. **The channel is a control-plane-owned file tailed live, not a FIFO.** A bind-mounted FIFO on Docker Desktop is a pipe inside the VM, unconnected to the host reader; the first in-container write blocked forever and hung T1 acceptance. The properties that mattered came from the live read and the parse rules, not the inode type, and are preserved and tested — see the `evidenceChannel` doc comment in `internal/oracle/stream.go`. Read "FIFO" below as "the channel".
>
> The refusal, byte-shape:
>
> ```
> mvo: race: policy mv0:… requires evidence regime "isolated", which needs oracle execs under a distinct uid with a read-only worktree; this binary does not ship that exec path (oracles run as the invoking uid with a writable cwd), so the guarantee cannot be delivered — set evidence.regime to "auto" (resolves to "streamed") or "in-tree"
> ```

The regime is a property of one oracle run, decided by capability at run time, constrained by policy, and recorded in the receipt.

| regime | how evidence leaves the candidate's world | what the candidate cannot do | requires |
|---|---|---|---|
| `control-plane` | no candidate code executes; the control plane compares two git trees it holds | anything — the candidate never runs | nothing (`tree-guard` only) |
| `isolated` | framed stream → mode-`0622` FIFO in a directory owned by the control plane's uid; worktree mounted read-only at `/work-ro`; scratch for cross-check files | create or modify **any** file in its own tree during the run; unlink, truncate, read or replace the channel; author the JUnit file the cross-check reads | T1 + docker + a distinct oracle uid |
| `streamed` | framed stream → FIFO reachable by the oracle uid | delete records already received; author a metric after exit; produce a pass by silencing the plugin | any tier |
| `in-tree` | M1e behaviour: metrics parsed from files in the candidate's tree after exit | nothing | explicit `evidence.regime: "in-tree"` |

`evidence.regime: "auto"` (the compiled default) selects `isolated` when the tier is T1 and the platform supplies a distinct oracle uid, `streamed` otherwise. `evidence.regime: "isolated"` **requires** it: a race under a T0 tier aborts at pre-flight as machinery, one line, ledger untouched (M1e decision 15's shape):

```
mvo: race: policy mv0:… requires evidence regime "isolated", which needs isolation tier T1-container (this race is T0-worktree); re-run with --tier T1 or set evidence.regime to "auto"
```

### T1 mount layout (`internal/backend`)

`Backend.Open` gains an options struct; `t1Backend.Open` mounts four paths on the one keeper container (M1c decision 3's "one mutable state" is preserved — `/work` and `/work-ro` are the same host directory):

```go
type OpenOpts struct {
    EvidenceDir string // host dir, control-plane-owned; bind → /mvo/evidence
    ScratchDir  string // host dir, oracle-uid-writable;  bind → /mvo/scratch
    PluginDir   string // host dir, read-only;            bind → /mvo/plugin
}
func (b Backend) Open(ctx context.Context, dir string, opts OpenOpts) (World, error)
```

| container path | host source | mode | who writes it |
|---|---|---|---|
| `/work` | the worktree | rw | the agent (phase A) |
| `/work-ro` | the same worktree | **ro** | nobody — the oracle's cwd under `isolated` |
| `/mvo/evidence` | `<raceDir>/ev/<world>/<kind>/` | dir `0755`, owned by the invoking uid; contains only `stream` (FIFO, `0622`) | the plugin, append-only |
| `/mvo/scratch` | `<raceDir>/scratch/<world>/<kind>/` | `0777` | the test process (JUnit, coverage data) |
| `/mvo/plugin` | `<wsDir>/plugin/<digest>/` | **ro**, `0555` | nobody |

Under `isolated`, oracle execs carry `--user 65534:65534 --workdir /work-ro`. uid 65534 can traverse and read the worktree (default `0755`/`0644`), can **write** the FIFO (mode `0622`), cannot **unlink** it (the containing directory is not writable by it), cannot **read** it back, and cannot write anywhere in `/work-ro`. A repository with `0600` files fails the oracle run with an EACCES-shaped `status = error` and escalates — honestly, and visibly, rather than silently degrading.

Under `streamed` (T0, or T1 without a distinct uid) the same FIFO and scratch directories exist; under T0 their in-world paths are the host paths. The candidate's uid **can** unlink and replace the FIFO. It gains nothing: the control plane holds the read end open, so a replacement is a file nobody reads, the stream ends with no terminal record, and every stream-derived metric is absent (rule S1). Replacing the channel is a way to fail, not a way to pass.

## The evidence stream

### The plugin

`internal/oracle/pyplugin/mvo_evidence.py` is embedded in the binary, content-addressed once at init, written to `<wsDir>/plugin/<digest>/mvo_evidence.py` (mode `0444`; **directory `0755`, amended — see below**) on first use, and `CAS.Put` before any run consumes it. Its digest is recorded in every receipt it produced (`execution.evidence_plugin`), so "which observer saw this" is an auditable fact, not a version guess.

> **Implementation amendment (integration gate).** The plugin FILE is `0444`; the containing **directory stays `0755`, not the `0555` this document and the T1 mount table specify.** Sealing it would only make the workspace undeletable: on T0 everything runs as one uid, so no file mode is a seal there, and under `isolated` the real seal is the read-only bind mount at `/mvo/plugin` plus the distinct oracle uid — an OS guarantee rather than an inference. Since `isolated` does not ship (see the amendment under [Evidence regimes](#evidence-regimes)), the `0555` cell in the T1 mount table describes a path no run takes today.

It is loaded by argv, from a read-only path, before any candidate code:

```
PYTEST_DISABLE_PLUGIN_AUTOLOAD=1                      # unless evidence.plugin_autoload == "on"
PYTHONPATH=<plugin dir>[:<the operator's PYTHONPATH>]  # PREPENDED, never assigned over
MVO_EVIDENCE_STREAM=<fifo path>   MVO_EVIDENCE_NONCE=<32 hex>
<prefix> -p mvo_evidence -p no:cacheprovider …
```

`-p` plugins load at startup, ahead of `conftest.py` collection, so the plugin is registered before the candidate's first line runs. A `conftest.py` that unregisters it, or a `pytest.ini` `addopts` that adds `-p no:mvo_evidence`, produces a stream with no terminal record — which is a failed gate, never a pass (decision 2). Both files are in the default harness set anyway (decision 6).

**`PYTHONPATH` is prepended, not assigned.** The plugin directory must come first — it is read-only and outside the candidate's tree — but an operator's `PYTHONPATH` is how a src-layout repo, an uninstalled package or a virtualenv-less checkout finds its own code. The first cut emitted an absolute `PYTHONPATH=<plugin dir>`; Go's `os/exec` keeps the LAST occurrence of a duplicated key, so appending that to `os.Environ()` silently discarded the operator's value on **every T0 run under the shipped default**, and an honest repository would have failed `collect-nonempty` on the first race a new user ever ran. On T1 nothing is inherited by construction — the image owns its environment exactly as it owns `PATH` (M1c decision 7's filter list) — so an image-level `PYTHONPATH` is **not** carried through. That is a stated T1 limitation: the merge cannot be performed client-side without a shell in the container, and inventing one to expand a variable is a worse trade than saying so.

**`PYTEST_DISABLE_PLUGIN_AUTOLOAD=1` is set unless the policy says otherwise** (decision 24). Under the seal the two optional plugins mvo requests are named on argv — `-p pytest_reportlog.plugin` beside `--report-log=…`, `-p pytest_rerunfailures` beside `--reruns N` — so a probe finding still reaches the run instead of becoming an unknown-option usage error.

### Wire format (`multiverso.dev/evidence-stream/v0`)

Tab-separated, one record per line, JSON payload last (so a payload can contain anything but a newline):

```
mvo-evidence/v0	<nonce>
1	session_start	{"argv_digest":"sha256:…","pid":41,"pytest":"8.4.1","rootdir":"/work-ro"}
2	collected	{"count":8}
3	test	{"duration_ms":3,"nodeid":"test_stats.py::test_mean_pair","outcome":"failed","run":1}
…
11	session_finish	{"duration_ms":812,"errored":0,"exitstatus":1,"failed":2,"passed":6,"skipped":0,"total":8}
```

Outcomes are the JUnit equivalence classes, so the cross-check compares like with like: `passed`, `failed`, `error` (a setup/teardown failure), `skipped`, `xfailed` (→ counted as skipped), `xpassed` (→ counted as passed unless strict). One `test` record per `(nodeid, run)`; `--reruns` produces several for one nodeid.

### Control-plane reader — normative

Created **before** the process is spawned, torn down after it exits:

1. `syscall.Mkfifo(<ev>/stream, 0o622)`; the control plane opens the read end `O_RDONLY|O_NONBLOCK` and reads it in a goroutine.
2. The process runs. Records are consumed as they arrive; a **64 MiB cap** (the EP-7 artifact cap) bounds the buffer.
3. On `cmd.Wait()` return the read end is closed, which terminates the reader. Whatever arrived is what there is. Nothing is read from the FIFO — or from anywhere else — afterwards.
4. The raw stream bytes reach CAS as the `evidence-stream` artifact **before** anything is parsed (EP-7 order, unchanged).

Parse rules — any violation makes the stream **unusable**, which makes every stream-derived metric **absent**:

| # | rule | on violation |
|---|---|---|
| 1 | line 1 is `mvo-evidence/v0\t<nonce>` with the nonce this run generated | unusable — `stream header missing or nonce mismatch` |
| 2 | `seq` strictly increases from 1; no gap, repeat or decrease | unusable — `stream sequence broken at line %d` |
| 3 | exactly one `session_start`, at seq 1 | unusable |
| 4 | at most one `session_finish`; **records after it are discarded** and noted | usable; note appended to the stderr artifact |
| 5 | unknown record kinds are ignored and counted | usable; note appended |
| 6 | total bytes ≤ 64 MiB | unusable — `evidence stream exceeds the 64 MiB cap` |
| 7 | a `session_finish` is present | absent ⇒ **incomplete**: metrics absent; see S1 |

Every note is appended to the stored stderr artifact — the M1b/M1c control-plane-note precedent — so an operator sees *why* a metric is missing without a second channel.

### Metric derivation

| metric | derived from |
|---|---|
| `collected_total` | the `collected` record's `count` (both kinds; the suite run emits it too) |
| `tests_total` | distinct nodeids with ≥1 `test` record |
| `tests_passed` / `tests_failed` / `tests_errored` / `tests_skipped` | each nodeid's **highest-seq** outcome |
| `tests_failed_first_run` | nodeids whose **lowest-seq** outcome ∈ {failed, error} |
| `tests_passed_after_rerun` | nodeids whose first run failed and whose final outcome passed |
| `duration_ms` | `session_finish.duration_ms` |
| `coverage_bp` | coverage.py JSON in scratch — **candidate-writable, see decision 12** |

The stream subsumes `pytest-reportlog` for first-run metrics: under a streaming regime `tests_failed_first_run` no longer requires a plugin to be installed, and `--report-log` is requested only under `in-tree`. The reportlog parser stays, tested, for that path. `result.tools` gains `"mvo-evidence": "v0"` whenever a usable stream was produced, so `escalation.require_evidence`'s usability test (M1e rule 2) keeps working unchanged.

### Structural rules (S1–S3)

Not policy-controlled, in the manner of M1e decision 14's "exit 5 is fail, never pass":

- **S1 — absence never passes.** No usable stream ⇒ every stream-derived metric absent. If the process exited 0, `status = error`, reason `no usable evidence stream (%s)`. If it exited non-zero, `status = fail` (the process said so, and there is nothing to contradict).
- **S2 — the exit code is cross-examined, not trusted.** `status = error` when the process exited 0 while the stream reports `failed > 0` or `errored > 0`, or when `session_finish.exitstatus` differs from the observed exit code. Reasons, byte-exact: `exit_code=0 but the evidence stream reports failed=%d errored=%d`; `evidence stream reports exitstatus=%d but the process exited %d`. For `pytest-collect`, exit 5 keeps forcing `collected_total = 0` (M1e decision 14), **and** a stream reporting a non-zero count against exit 5 is a disagreement: `error`, `exit_code=5 (no tests collected) but the evidence stream reports collected=%d`.
- **S3 — the second source must agree** (when `evidence.crosscheck == "require"`, the compiled default, and a second source exists). For `pytest-suite`: the tuple `(total, failed, errored, skipped)` from JUnit XML must equal the stream's, else `status = error`, reason `junit-xml and the evidence stream disagree: junit(total=%d,failed=%d,errored=%d,skipped=%d) stream(total=%d,failed=%d,errored=%d,skipped=%d)`. For `pytest-collect`: the `--collect-only` summary line's count must equal the stream's, else `error`, reason `collect-only stdout reports %d collected, the evidence stream reports %d`. Under `isolated` no JUnit file can be written into a read-only worktree, so it is written to `/mvo/scratch` — where the candidate can still author it, which is exactly why it is a cross-check and not a source.

## Protected paths — the `tree-guard` oracle

### What it compares

Inputs: the **base tree** (decision 19) and the **candidate tree**, both git tree digests the control plane holds. The candidate tree is re-derived from the worktree through a **temporary index** (`GIT_INDEX_FILE` in a temp file, `git add -A && git write-tree`) so the operator's index is never touched and `.gitignore` semantics match exactly what AG-4's patch capture sees. It must equal the world's recorded `tree`; a mismatch is itself a violation (`tree_drift`), which is how "an earlier oracle wrote into the tree" or "something raced us" surfaces as a failure rather than as a quiet difference.

Two `git ls-tree -r --full-tree` walks, one comparison, no process executes in the world. `execution.argv` is `[]` and `execution.exit_code` is `0` in every case — the guard produces no process, so its exit code is not a verdict source and S2 does not apply to it. The verdict is `result.status`: `pass` (no violations), `fail` (violations), `error` (a tree could not be read, or the snapshot failed).

### Violation classes and metrics

| class | condition | metric |
|---|---|---|
| protected modified | path in base matches `protected`, blob or mode differs | `protected_modified` |
| protected deleted | path in base matches `protected`, absent from candidate | `protected_deleted` |
| protected added | path only in candidate, matches `protected` | `protected_added` (violation iff `protected_additions == "refuse"`) |
| harness modified | path in base matches `harness`, blob or mode differs | `harness_modified` |
| harness deleted | path in base matches `harness`, absent from candidate | `harness_deleted` |
| harness added | path only in candidate, matches `harness` | `harness_added` — **always** a violation |
| — | paths considered on both sides | `paths_examined` |
| tree drift | the snapshot differs from the world's recorded `tree` | **none** — see below |

**`tree_drift` borrows no path metric.** It means "an earlier oracle wrote into the tree, or something raced us": machinery, not a statement about the candidate's diff. The first cut incremented `protected_modified` and appended a violation with `path: ""`, so a receipt read `protected_modified=1 … (first: -)` when nothing protected had been touched — a false statement about the candidate's diff, in a content-addressed receipt and in a signed rationale, and reachable in practice (`testdata/adversarial/vectors/11-cross_world_sabotage.patch` mutates sibling worlds' trees, so an honest victim would have been convicted of editing a file it never touched). Drift now yields `status = error` with **metrics absent**, the same shape as a tree that could not be read: the gate fails on the receipt's own error status, and `escalation.on_all_worlds_failed_machinery` routes it to a human. `result.detail` names both trees. The report artifact still carries the `tree_drift` violation, so the operator sees what happened.

`paths-unmodified@guard` passes iff every violating count is 0 and `paths_examined` is present. Any required metric absent ⇒ the gate **fails** (`%s absent (source unavailable)`), never passes. Fail reason, byte-exact:

```
protected_modified=%d protected_deleted=%d harness_modified=%d harness_deleted=%d harness_added=%d (first: %s)
```

`(first: %s)` names the **lexicographically first** offending path — enough to act on in a table cell, with the whole list one artifact away. Note that this is *not* the first entry of the report artifact, which sorts by `(kind, path)`: the two orders disagree whenever a late-alphabet harness addition sits beside an early-alphabet protected modification, and the first cut shipped the kind-sorted one. Violations that name no path (`tree_drift`) are skipped; `""` renders as `-`.

### The report artifact

`Result.Artifacts` = `[<report key>]`, canonical JSON, `multiverso.dev/tree-guard-report/v0`:

```json
{"schema":"multiverso.dev/tree-guard-report/v0",
 "base_tree":"git:9c1f2a3b…","candidate_tree":"git:4d5e6f70…",
 "allowed_additions":["tests/test_regression.py"],
 "violations":[{"base_blob":"a1b2c3d4…","candidate_blob":"e5f60718…","kind":"protected_modified","path":"test_stats.py"},
               {"base_blob":"","candidate_blob":"9a8b7c6d…","kind":"harness_added","path":"conftest.py"}]}
```

Violations are sorted by `(kind, path)`. The report is control-plane authored — the one artifact in the system whose bytes no candidate influenced.

## Cross-oracle invariants

### Wire and compiled shape

```go
// InvariantSpec names one closed invariant and binds its ROLES to declared
// oracle instances. The vocabulary fixes which metric each role supplies
// and how they are compared; the policy only says which instance fills
// which role (decision 10).
type InvariantSpec struct {
    Name    string            `json:"name"`    // closed vocabulary
    Oracles map[string]string `json:"oracles"` // role -> declared oracle name
}
```
```go
// policy.Invariant is the compiled form: each role resolved to the
// Selector that picks its counted receipt.
type Invariant struct {
    Name  string
    Roles map[string]Selector
}
```

### The vocabulary (`internal/policy/invariant.go`)

| `name` | roles | holds iff | violation detail (byte-exact) |
|---|---|---|---|
| `collect-equals-suite-total` | `collect`, `suite` | `collect.collected_total == suite.tests_total` | `collected_total=%d != tests_total=%d` |
| `suite-parts-sum-to-total` | `suite` | `tests_total == tests_passed + tests_failed + tests_errored + tests_skipped` | `tests_total=%d != passed+failed+errored+skipped=%d` |

A required metric absent ⇒ the invariant is **violated**, detail `%s absent (source unavailable)` — the honesty rule again: an invariant that cannot be evaluated does not hold by default.

Validation rules, additional to M1e's list:

9. Every `invariants[i].name` is in the vocabulary; the `oracles` map's key set equals the invariant's declared role set exactly (missing or extra role = load error, named); every value is a declared oracle whose **instance** emits the metric that role supplies (M1e rule 5's instance-level test, reused).
10. `collect-equals-suite-total` additionally requires the two named oracles to have **equal `args`**: two pytest invocations given different selection arguments legitimately collect different counts, and an invariant that fires on a correct configuration is a bug generator. Load error: `invariants[0]: collect-equals-suite-total: oracles "collect" and "suite" must declare identical args (got ["-k","fast"] and [])`.
11. No two invariants share a name **and** an identical role map.

### Evaluation

In `Decide`, after gates, **only for worlds that passed every hard gate** — a world stopped at rung O-1 has no suite receipt, and its invariants are `not-evaluated`, exactly as later gates are. Then:

```
pass(W)  ≡  every hard gate passed  ∧  every declared invariant holds
```

A world that violates an invariant is not machinery-failed (its outcome is COMPLETED and its receipts are `pass`); it is a candidate whose evidence contradicts itself, and escalation rule 0 (decision 11) makes the race say so.

## Policy wire schema — the additive `v1` delta

```go
type PolicyV1 struct {
    Schema     string          `json:"schema"`
    Name       string          `json:"name"`
    Oracles    []OracleSpec    `json:"oracles"`
    HardGates  []GateSpec      `json:"hard_gates"`
    Ranking    []string        `json:"ranking"`
    Escalation EscalationSpec  `json:"escalation"`
    Paths      PathSpec        `json:"paths"`      // NEW
    Invariants []InvariantSpec `json:"invariants"` // NEW, name-sorted
    Evidence   EvidenceSpec    `json:"evidence"`   // NEW
}

// PathSpec declares the two frozen path classes (decision 6). Patterns are
// sorted in canonical form; the grammar is decision 7's.
type PathSpec struct {
    Protected          []string `json:"protected"`           // frozen against modify/delete
    Harness            []string `json:"harness"`             // frozen against modify/delete/CREATE
    ProtectedAdditions string   `json:"protected_additions"` // "" ⇒ "allow" | "refuse"
}

// EvidenceSpec selects how a run is OBSERVED. It never enters Decide; it
// constrains future runs only (decision 3).
type EvidenceSpec struct {
    Regime     string `json:"regime"`     // "" ⇒ "auto" | "isolated" | "streamed" | "in-tree"
    Crosscheck string `json:"crosscheck"` // "" ⇒ "require" | "off"
    // NEW (decision 24). "" ⇒ "off": PYTEST_DISABLE_PLUGIN_AUTOLOAD=1, so
    // the only plugins that load are the ones mvo names on argv. "on"
    // restores entry-point discovery for suites that genuinely need an
    // installed plugin, at the cost decision 24 states.
    PluginAutoload string `json:"plugin_autoload"` // "" ⇒ "off" | "on"
}

// EscalationSpec gains one rule; zero value = off = M1e semantics.
type EscalationSpec struct {
    MinCandidatesPassing       int      `json:"min_candidates_passing"`
    OnRankingTie               bool     `json:"on_ranking_tie"`
    RequireEvidence            []string `json:"require_evidence"`
    OnAllWorldsFailedMachinery bool     `json:"on_all_worlds_failed_machinery"`
    OnInvariantViolation       bool     `json:"on_invariant_violation"` // NEW — rule 0
}
```

### Closed vocabulary deltas (`internal/policy/vocab.go`)

**Gate predicates** (added to M1e's five):

| `gate` | passes iff | `threshold` | fail reason format |
|---|---|---|---|
| `paths-unmodified` | every violating guard count is 0 | unused | the guard reason above |
| `skips-not-above` | `metrics.tests_skipped <= threshold` | max tolerated skipped tests, ≥ 0 | `tests_skipped=%d (want <= %d)` |

`skips-not-above` exists because the metric already existed and no gate in the closed vocabulary could read it — the study's third vector was recorded in every receipt and was unreadable by any gate. It takes a parameter, so `threshold == 0` is legal and meaningful (M1e validation rule 5's "threshold must be 0 when the predicate takes no parameter" does not apply).

**Oracle kinds:**

| kind | family | metrics |
|---|---|---|
| `tree-guard` | `tree` | `protected_modified`, `protected_deleted`, `protected_added`, `harness_modified`, `harness_deleted`, `harness_added`, `paths_examined` |

`pytest-collect` and `pytest-suite` keep their M1e metric vocabularies; `pytest-suite` no longer needs `pytest-reportlog` present to emit the first-run pair under a streaming regime.

**Ranking keys:** unchanged. `wall_ms_asc` is removed from the *shipped default*, not from the vocabulary (decision 20).

**Additional validation rules:**

12. A `paths-unmodified` gate requires `paths.protected` ∪ `paths.harness` to be non-empty: `hard_gates[0]: paths-unmodified requires policy.paths to declare at least one pattern`.
13. Every pattern parses under the decision-7 grammar; an unparseable pattern is a load error naming the pattern and the offending construct.
14. `evidence.regime` ∈ {`""`, `auto`, `isolated`, `streamed`, `in-tree`}; `evidence.crosscheck` ∈ {`""`, `require`, `off`}; `evidence.plugin_autoload` ∈ {`""`, `off`, `on`}; `paths.protected_additions` ∈ {`""`, `allow`, `refuse`}. Unknown values are refused by name with the known set, as everywhere else. A policy declaring a regime this binary cannot **deliver** is additionally refused at `mvo policy use`, in the same sentence `mvo race`'s pre-flight produces: a shipped example that installs cleanly and then refuses every race is a workspace bricked by a file the docs told you to read.
15. A `tree-guard` oracle declares no `argv`, `args`, `coverage`, `reruns`, or `timeout_ms` (it runs no process): non-zero values are a load error.

### The shipped default (`policy.Default()`) — new digest

`mv0:f207c3fad59d0fc973e5f342ac54d8b1bc9e5c6cae2a2cff0b33477ddee3c543`. It moved twice inside this block: once for M1f's own additions, and again for the red-team fixes (`evidence.plugin_autoload`, and `**/*.dist-info/**`, `**/*.egg-info/**`, `**/.gitignore` in the harness set). Both moves are free for recorded work — decision 21 — and `scripts/accept.sh` step 3p greps the reader-facing docs for this digest so they cannot silently pin an older one.

```json
{"escalation":{"min_candidates_passing":0,"on_all_worlds_failed_machinery":true,"on_invariant_violation":true,"on_ranking_tie":true,"require_evidence":[]},
 "evidence":{"crosscheck":"require","plugin_autoload":"off","regime":"auto"},
 "hard_gates":[{"basis":"construction","gate":"paths-unmodified","oracle":"guard","threshold":0},
               {"basis":"construction","gate":"collect-nonempty","oracle":"collect","threshold":0},
               {"basis":"construction","gate":"collected-not-below","oracle":"collect","threshold":0},
               {"basis":"construction","gate":"status-pass","oracle":"suite","threshold":0}],
 "invariants":[{"name":"collect-equals-suite-total","oracles":{"collect":"collect","suite":"suite"}}],
 "name":"default",
 "oracles":[{"args":[],"argv":[],"coverage":true,"kind":"pytest-collect","name":"collect","reruns":0,"timeout_ms":0},
            {"args":[],"argv":[],"coverage":false,"kind":"tree-guard","name":"guard","reruns":0,"timeout_ms":0},
            {"args":[],"argv":[],"coverage":true,"kind":"pytest-suite","name":"suite","reruns":0,"timeout_ms":0}],
 "paths":{"harness":["**/*.dist-info/**","**/*.egg-info/**","**/*.pth","**/.gitignore","**/conftest.py","**/sitecustomize.py","pyproject.toml","pytest.ini","setup.cfg","tox.ini"],
          "protected":["**/*_test.py","**/test_*.py","test/**","tests/**"],"protected_additions":"allow"},
 "ranking":["gate_pass","tests_passed_desc"],
 "schema":"multiverso.dev/policy/v1"}
```

Ladder order matters: the guard is rung **O-1**, so a candidate that edited a test never pays for a collect or a suite run, and the gate that stopped it is the one named in the rationale and in the `mvo worlds` GATE column.

`skips-not-above` is deliberately **not** in the default. It is absolute (the collect baseline measures collected counts, not skips, and a suite baseline would double the race's fixed cost), so a threshold of 0 would fail every repository with a legitimate platform skip on the first run a new user ever does. Vector 3 is closed in the default by the protected-paths gate; `skips-not-above` is what closes it for operators who deliberately relax that gate, and it ships in `testdata/toyrepo/policies/sealed.json` with a worked threshold.

**What the new digest means for existing work** (decision 21): pinned intents keep their pinned digest and decide exactly as before; `mvo audit` replays them against the policy resolved from CAS; `mvo policy list` shows the old digest as `recorded (default)` until an operator adopts the new one; upgrading the binary changes nothing on disk. The adoption path is printed by `mvo policy list` when the workspace default is an older built-in:

```
mvo: policy list: the workspace default mv0:4f2… predates this binary's built-in default mv0:8c7…
     (adds paths-unmodified@guard, collect-equals-suite-total, on_ranking_tie; drops wall_ms_asc)
     adopt with: mvo policy show default --builtin --json > .multiverso/policies/default.json && mvo policy use default
```

## Receipt & envelope extensions (`internal/object`)

```go
type Execution struct {
    Argv          []string      `json:"argv"`
    ExitCode      int           `json:"exit_code"`
    DurationMS    int64         `json:"duration_ms"`
    IsolationTier string        `json:"isolation_tier"`
    IsolationCaps IsolationCaps `json:"isolation_caps"`
    EvidenceRegime string       `json:"evidence_regime"` // NEW — decision 14
    EvidencePlugin string       `json:"evidence_plugin"` // NEW — plugin content address, "" when none ran
}

const (
    RegimeControlPlane = "control-plane"
    RegimeIsolated     = "isolated"
    RegimeStreamed     = "streamed"
    RegimeInTree       = "in-tree"
)
```

`Result` gains one field for the same reason:

```go
type Result struct {
    Status    string            `json:"status"`
    Metrics   map[string]int64  `json:"metrics"`
    Tools     map[string]string `json:"tools"`
    Detail    string            `json:"detail"`    // NEW
    Artifacts []string          `json:"artifacts"`
}
```

`Result.Detail` is the control plane's one-line, **non-numeric** summary of the verdict — the single string a pure gate predicate may quote when a count is not enough to act on. `paths-unmodified` needs the name of the first offending path in its fail reason and `Decide` has no CAS access, so a name that is not in the receipt cannot reach a rationale. It is **always serialized** and is `""` for every kind but `tree-guard`, where it carries the lexicographically first offending path (or, on a drifted tree, the drift sentence).

All three fields are always serialized (M1b decision 5 — no `omitempty` games). New receipts get new digests, as in M1b, M1c and M1e; **replay of pre-M1f ledgers is unaffected**, because M1e decision 1 pairs every recorded object with the digest it was recorded under and never re-serializes.

`Result.Artifacts` order gains one kind, inserted after the probe: `stdout`, `stderr`, `tools-probe`, `evidence-stream`, `junit-xml`, `reportlog`, `coverage-json`; `tree-guard` receipts carry exactly `[tree-guard-report]`. Absent kinds are skipped and `result.tools` says which optional sources were used, so the list stays unambiguous without positional guessing.

## Metric provenance

The table a reviewer needs and did not have. "Who can author this number" is the only question that matters once the receipt is signed.

| metric | authoritative source | authorable by the candidate |
|---|---|---|
| `protected_*`, `harness_*`, `paths_examined` | git trees compared by the control plane | **no** — in every regime |
| `collected_base`, `collected_delta` | a control-plane run on the **base** tree (M1e decision 13) | no, relative to this candidate |
| `collected_total`, `tests_*`, `duration_ms` | the plugin's stream | **only in-process**: cannot be suppressed into a pass (S1), cannot be authored after exit, can be forged by patching the plugin before the first failing report |
| `coverage_bp` | coverage.py JSON in scratch | **yes, in every regime** (decision 12) — cross-check only, not in the default policy |

## Decision function deltas (`internal/race`, `internal/admit`)

Signatures are unchanged. `Decide` stays pure, total and order-independent over `(pol, worlds, receipts)`.

Evaluation order gains one step between 3 (gates) and 4 (ranking):

> **3b. Invariants**, per world that passed every hard gate: each declared invariant is evaluated against that world's counted receipts. `pass(W)` requires all of them to hold. A world that failed a gate has `not-evaluated` invariants.

Escalation, amended precedence — first match wins:

| # | rule | fires when | replaces |
|---|---|---|---|
| **0** | `on_invariant_violation` | ≥ 1 world violated ≥ 1 declared invariant | SELECT / REJECT |
| 1 | `on_all_worlds_failed_machinery` | (M1e, unchanged) | REJECT |
| 2 | `require_evidence` | (M1e, unchanged) | SELECT |
| 3 | `min_candidates_passing` | (M1e, unchanged) | SELECT |
| 4 | `on_ranking_tie` | (M1e, unchanged) | SELECT |

**New rationale text, emitted only on inputs no M1e policy can produce** (decision 3's compatibility rule):

```
REJECT per-world clause (invariant):  "%s violated invariant %s (%s)"
                                       ← world, invariant name, detail
rule 0 sentence:                      "%d of %d worlds produced self-contradictory evidence: %s"
                                       ← per world, ranked order, joined ", ":
                                         "%s violated %s (%s)"
```

Every other format string — the SELECT sentences, the REJECT gate clause, the four M1e rule sentences, the whole v0 dialect — is untouched, byte for byte. A policy with `invariants: []` (every policy M1e could write) reaches none of the new text.

`admit.Decide` keeps M1a's outcome table and gains one rule, evaluated **before** the ADMIT/REJECT split and reachable only when the pinned policy declares invariants:

```
any declared invariant violated on the landing evidence  ⇒  ESCALATE
   "landing evidence violates invariant %s (%s)"
```

Rationale: at admission there is one subject and no ranking, so REJECT would read "this candidate is bad" when what happened is "the landing tree's evidence contradicts itself". CP-8's ESCALATE is the existing shape for "a human must look at this".

## `mvo audit` — the CAS integrity sweep

### The referenced set — normative

The sweep is a declared table, not a best-effort walk. Anything not in this table is not swept, and the report says how many objects it examined so the claim is checkable.

| referrer | digests swept |
|---|---|
| every ledger event | `payload_dig` (the canonical object bytes, `Put` at record time) |
| `world.created` | `context`, `patch`, `trace`, `env` |
| `receipt.recorded` | `result.artifacts[*]`, **`execution.evidence_plugin`** |
| `decision.recorded` | `evidence[*]`, `policy`, `subject[*]`, `intent` |
| `intent.created` | `policy` |
| `baseline.recorded` | `stdout`, `stderr`, `probe`, **`evidence_stream`** |
| `agent.finished` | **`context`, `transcript`, `stderr`** |
| `attestation.recorded` | `bundle`, `statement` |
| `policy.created` | the payload's own digest |

The three bold rows were missing from the first cut, and each was a blob that could be deleted under an `OK`:

- **`execution.evidence_plugin`** — decision 14 sells the plugin digest as "an auditable fact, not a version guess", and `race.setupEvidence`/`admit.openLandingEvidence` deliberately `CAS.Put` the source so a receipt naming it always resolves. A fact nothing re-reads is a version guess with a colon in it.
- **`baseline.recorded.evidence_stream`** — the baseline collect run streams like any other, and `collected_base` is the denominator `collected-not-below` divides by. Its observation record was in CAS, referenced by nothing, and inflating `cas_unreferenced`.
- **`agent.finished.stderr`** — named in no other event, and the only place a `CONFIG_ERROR` world's reason survives. (`context` and `transcript` are also named by `world.created`; dedup collapses the overlap.)

Both digest spellings are handled (`mv0:` object digests map to `sha256:` CAS keys through `object.CASKey`; artifact keys are already `sha256:`).

For each referenced key: read it, re-hash it, compare. Then:

- **missing** — referenced and absent ⇒ `cas_missing`, **exit 1**. This is the attestation-blob deletion the study found reporting `OK`.
- **corrupt** — present, but the stored bytes hash to something else ⇒ `cas_corrupt`, **exit 1**. This is the CAS edit the quickstart claimed audit caught.
- **unreferenced** — present, never referenced ⇒ counted in `cas_unreferenced`, **never a failure**. CAS legitimately holds more than one ledger references (publication working sets, prior prunes).

When a signer is resolvable, every `attestation.recorded` bundle's DSSE signature is verified and counted — **against the anchor the report names**. `--key PATH` supplies an external public key, exactly as `mvo verify --key` and `mvo fetch-race --key` do, and an unreadable one is refused rather than silently downgraded. Without it the anchor is the workspace's own key, which is a **self-check a rogue clone reproduces byte-for-byte** — the study's exact finding — so `attestations_verified_against` is `"workspace"` or `"supplied"` in the JSON and the human line reads `… verified against the workspace key`. A count with no named anchor is the over-claim; naming it is the whole fix. `mvo verify` remains the attestation verb and its seven checks are unchanged; audit's job here is only "is the closure the ledger points at intact and self-consistent".

**What the sweep still does not prove**, stated next to it in the docs and in `--json`'s absence of any field claiming it: that something *was* recorded which never was. An adversary with write access to the ledger cannot forge the chain, but an operator who never ran an oracle has nothing for audit to miss. Audit verifies the recorded closure; it does not verify that the closure is complete with respect to the world.

### Report and output

`--json` schema bumps to `multiverso.dev/audit-report/v1` (a report, not a content-addressed object — no replay implications, and scripts pinning the string must update):

```json
{"schema":"multiverso.dev/audit-report/v1","events":31,"decisions":3,"admissions":1,
 "chain_ok":true,"replay_identical":true,
 "cas_checked":412,"cas_missing":[],"cas_corrupt":[],"cas_unreferenced":4,
 "attestations_checked":1,"attestations_verified":1,"attestations_verified_against":"workspace",
 "mismatches":[]}
```

Human output:

```
OK: 31 events, 3 decisions replayed, 412 CAS objects verified, 1 attestation signature(s) verified against the workspace key
```
```
MISSING: sha256:ab12… referenced by attestation.recorded seq 29 (bundle)
CORRUPT: sha256:cd34… stored bytes hash to sha256:ef56…, referenced by receipt mv0:7a1… (result.artifacts[3])
```

Empty workspace keeps its honest line and exit 0 (decision 23):

```
OK: 4 events, 0 decisions replayed, 6 CAS objects verified (no races in this workspace — nothing was decided)
```

## CLI

```
mvo audit [--dir DIR] [--json] [--key PATH] [--require-decisions N] [--cas-sweep=BOOL]
mvo guard --base <rev|tree> [--tree <rev|tree>] [--policy <name|digest>] [--json] [--dir DIR]
mvo policy show <ref> [--builtin] [--json] [--dir DIR]
mvo race <intent> … [--tier T0|T1]        # pre-flight now also checks the regime
mvo explain <intent> [--json] [--diffs N]
```

- **`audit --require-decisions N`** — exit 1 when fewer than N decisions replayed, with `mvo: audit: 0 decisions replayed, --require-decisions 1` on stderr and `SHORTFALL: 0 decisions replayed, --require-decisions 1` on stdout. The CI knob decision 23 argues for. **The `OK:` line is guarded on this check like every other**: the first cut printed `OK:` and *then* returned the failure, so a log-scraping CI check keyed on `OK:` read a failed audit as a pass — the block's own rule ("a skipped check must never render identically to a passed one") violated on the one flag whose entire purpose is to be scraped.
- **`audit --key PATH`** — the trust anchor. Named here because the sweep section's promise of it was not implemented in the first cut: `mvo audit --key foo.pub` exited 2 with `flag provided but not defined: -key`.
- **`audit --cas-sweep=false`** — skips the sweep for a large ledger on a slow disk; the human line then says `(CAS sweep skipped)` and the JSON reports `"cas_checked":0`. A skipped check that renders identically to a passed one is the over-claim we are removing, so it never does.
- **`mvo guard`** — the adoption wedge, and the one verb an evaluating maintainer can run before adopting anything: compare two trees under a policy's path sets, print violations, exit 0 clean / 1 violating. No ledger writes, no worktree, no race. `--tree` defaults to the working tree (snapshotted through a temporary index, exactly as the oracle does). `mvo guard --base HEAD~50 --policy default` answers "would this gate have blocked my last fifty commits" in one command.
  It also lists, as `IGNORED:` lines, path-set files that exist in the working tree and that git's exclude rules keep out of the snapshot, and it **refuses to print the clean verdict** when there are any (`INCOMPLETE: …`, exit 1). Decision 25 is why: the guard compares git trees, `git add -A` honours `.gitignore`, and pytest does not. A `mvo guard` that reported `OK` over a tree carrying a live ignored `conftest.py` is a false clean verdict from the verb people use to decide whether to trust the gate at all. The RACE path does not need this — a force-applied file is in the world's index, and the drift between that tree and a fresh snapshot is a `tree_drift` — which is exactly why the gap was only ever in the advisory verb.
- **`policy show --builtin`** — prints the binary's built-in policy of that name regardless of what the workspace file says. This is how decision 21's adoption two-liner is written down, and how an operator diffs their pinned default against the shipped one.
- **`race`** pre-flight gains the regime check (the message under [Evidence regimes](#evidence-regimes)) alongside M1e's pytest probe. Both abort as machinery before `race.started`; the ledger stays empty of race events.

### `mvo explain` — additions

```
decision:  mv0:7c3…
type:      ESCALATE
policy:    mv0:8c7…  (default, policy/v1)
evidence:  regime streamed (T0-worktree; isolated not deliverable in this binary), plugin sha256:1f4…

gates (ordered, first failure stops the ladder):
  RANK  WORLD     ORD  paths-unmodified@guard         collect-nonempty@collect  collected-not-below@collect  status-pass@suite  INVARIANTS  RESULT
  1     mv0:aa1…   1   pass                           pass (total=8)            pass (delta=+0)              pass               ok          PASS
  2     mv0:bb2…   3   pass                           pass (total=8)            pass (delta=+0)              pass               VIOLATED    FAIL
  3     mv0:cc3…   2   FAIL (protected_modified=1 … (first: test_stats.py))  n/a  n/a                        n/a                n/a         FAIL

invariants:
  mv0:bb2…  collect-equals-suite-total  VIOLATED  collected_total=8 != tests_total=500

escalation:
  rule on_invariant_violation: 1 of 3 worlds produced self-contradictory evidence:
    mv0:bb2… violated collect-equals-suite-total (collected_total=8 != tests_total=500)

ranking walk for leader mv0:aa1…  (no winner: escalated by on_invariant_violation; ranking [gate_pass,tests_passed_desc,world_digest_asc])
  …
```

The `evidence:` line is new and unconditional: a reader must not have to know that T0 means `streamed`. The parenthetical says *"isolated not deliverable in this binary"* rather than *"isolated requires --tier T1"*, because the latter points at a flag that would not help — `isolated` does not ship (see the amendment under [Evidence regimes](#evidence-regimes)), and a line that sends an operator to re-run with `--tier T1` for a guarantee no tier delivers is the label doing the laundering again. `mvo policy show` renders the third evidence field beside the first two: `evidence:  regime auto, crosscheck require, plugin_autoload off`. The INVARIANTS column appears only when the policy declares invariants (so M1e-policy goldens are unchanged). `--json` gains `evidence`, per-candidate `invariants`, and `escalation.rule = "on_invariant_violation"` inside the existing `multiverso.dev/explain-report/v0` shape — additive fields on a report, as with audit.

`mvo worlds`' GATE column renders `paths-unmodified@guard` like any other first-failed gate.

## What is stopped, what is detected, what is open

Definitions, used strictly. **STOPPED**: the capability is removed; the vector cannot produce the false outcome. **DETECTED**: the vector executes and produces a signal that fails the run — never a pass. **OPEN**: neither; no coverage is claimed.

Under the **shipped default policy** (protected-paths gate on, invariants on, `crosscheck: require`):

| # | vector (as reproduced in the study) | verdict | mechanism |
|---|---|---|---|
| 1 | `conftest.py` writes a fabricated `junit.xml` at `atexit` | **STOPPED** | the patch's content *is* a new `conftest.py` ⇒ `harness_added` ⇒ `paths-unmodified@guard` fails at rung O-1, before any Python runs |
| 2 | `pytest_sessionfinish` sets `session.exitstatus = 0` | **STOPPED** | same: a new `conftest.py` ⇒ `harness_added` |
| 3 | `@pytest.mark.skip` on the failing tests | **STOPPED** | the marks modify an existing test file ⇒ `protected_modified` |
| 4 | assertion weakening (`== 500` → `>= 0`) | **STOPPED** | `protected_modified` |
| 5 | assertion rewriting to match the candidate's own output | **STOPPED** | `protected_modified` |
| 6 | **an `*.egg-info` declaring a `pytest11` entry-point plugin** (red team, 2026-08-15) | **STOPPED** | two ways: `evidence.plugin_autoload: "off"` sets `PYTEST_DISABLE_PLUGIN_AUTOLOAD=1`, so the module never loads (decision 24); and the metadata directory is in the harness set, so the patch dies at rung O-1. Corpus vector `13-entrypoint_plugin` |
| — | the 500-test receipt in a 3-test repository, forged **incoherently** | **DETECTED** | `collect-equals-suite-total` ⇒ world fails ⇒ rule 0 ⇒ ESCALATE naming the world and the numbers |
| — | the 500-test receipt in a 3-test repository, forged **coherently** | **OPEN** | see the limit under decision 10. Both sources the invariant compares are authored inside the trust boundary; an adversary who forges the collect count and the suite total to agree passes it. The red team reproduced this end to end. What closes it is a count derived outside the test process (control-plane static AST collection), which M1f does not ship |

With the guard gate **relaxed or absent** — the honest second line, and the reason the stream exists at all:

| # | vector, moved into application code (no harness file) | verdict | mechanism |
|---|---|---|---|
| 1 | `stats.py` writes a fabricated `junit.xml` at `atexit` | **DETECTED** | metrics come from the stream; the file is a cross-check ⇒ S3 disagreement ⇒ `status = error` ⇒ every gate fails. (The "under `isolated` the write fails outright — STOPPED" upgrade this row originally claimed is **not reachable today**: `isolated` does not ship, see the amendment under [Evidence regimes](#evidence-regimes). DETECTED is the whole of what M1f delivers here.) |
| 2 | `stats.py` registers an `atexit` handler calling `os._exit(0)` | **DETECTED** | S2: exit 0 against a stream reporting `failed=2` ⇒ `status = error` |
| 1′ | the candidate unregisters the plugin or injects `-p no:mvo_evidence` | **DETECTED** | S1: no usable stream ⇒ metrics absent, and a 0-exit with no stream is `error`, never `pass` |
| 6 | the entry-point plugin, guard gate absent | **STOPPED** | `PYTEST_DISABLE_PLUGIN_AUTOLOAD=1` is a capability removal, not a gate: under `no-paths.json` vector 13 runs and reports its REAL numbers (`tests_failed=2, tests_passed=6`) and dies at `status-pass@suite`. This is the second line of defence under live test |
| 3 | skips, with `skips-not-above` declared | **DETECTED** | `tests_skipped` is finally readable by a gate |
| 3 | skips, with neither gate declared | **OPEN** | and `collect-equals-suite-total` does **not** help: a skipped test is collected and counted, so both sides agree |
| 4, 5 | assertion weakening / rewriting | **OPEN** | not detectable from test outcomes at all. This is what O3 mutation testing (EP-5, M2) is for: a suite that cannot kill mutants is a suite that stopped asserting |
| — | an adversary that patches the plugin's report hook from an imported module before the first failing test reports | **OPEN** | stated in the threat model, and the red team's entry-point attack is a *delivery vehicle* for it that has now been removed rather than a refutation of it. The forgery must live in the reviewed diff, in application code, and must be coherent across the stream, the cross-check source and O0's count — all three of which the same in-process adversary authors. It is possible, it is not detected, and M1f does not claim otherwise |
| — | a backdoor in application code that passes every honest test | **OPEN** | ch. 13's actual problem. Nothing in M1 addresses it |
| — | a path-set file hidden from the guard by an in-tree ignore rule | **STOPPED in a race, REPORTED by `mvo guard`** | in a race the file is force-applied into the world's index, so the snapshot drifts from the recorded tree and the run escalates as machinery; `.gitignore` is itself in the harness set (decision 25). `mvo guard` has no world tree to drift against, so it lists the file and refuses the clean verdict |

The one-line summary that belongs in `docs/concepts.md` and the README, replacing the M1e table: **the candidate no longer writes its own evidence, and a candidate that edits the harness no longer races. What remains open is a candidate that lies from inside the code a human is about to review — which is where review, mutation testing, and the AI-control literature take over.**

## Fixtures

- `testdata/toyrepo/patches-forge/` — one reproduction per study vector, each minimal and each with a comment naming the vector: `patch-forge-artifact` (new `conftest.py`, `atexit` JUnit forgery), `patch-forge-exit` (new `conftest.py`, `pytest_sessionfinish`), `patch-skip` (`@pytest.mark.skip` on the two failing tests), `patch-weaken` (`== 3.0` → `>= 0`), `patch-rewrite` (expected values edited to the buggy output). All five are stopped by the guard; all five are in the acceptance script.
- `testdata/toyrepo/patches-forge-deep/` — **NOT SHIPPED.** The plan below stands as the design; the in-application-code variants are unit-tested instead (`internal/oracle/evidence_test.go`), and the end-to-end half that did ship is acceptance step 3k. The plan was: the same forgeries with **no harness file**, so the second line of defence is exercised rather than assumed: `patch-forge-instats` (the `atexit` JUnit forgery, written from `stats.py`), `patch-exit-instats` (`os._exit(0)` from an `atexit` handler in `stats.py`), `patch-inflate` (a forged JUnit claiming 500 tests, for the invariant), `patch-disable-plugin` (a `pytest.ini` whose `addopts` carries `-p no:mvo_evidence`, for S1). Raced under `no-paths.json`, where the harness seal is deliberately not what catches them.
- `testdata/toyrepo/policies/` — `no-paths.json` (the default minus the guard gate, the guard oracle **and** the invariant — keeps M1e's O0-guard contract under live test, so decision 5's fixtures stay meaningful), `no-paths-invariants.json` (the same, with `collect-equals-suite-total` and `on_invariant_violation` back on — the invariant tested in isolation from the guard; **ships, but is not installed by `scripts/accept.sh`**), `sealed.json` (guard + `skips-not-above` threshold 0 + `no-failed-tests` + `evidence.regime: "auto"` — **amended:** it originally declared `"isolated"`, a regime this binary cannot deliver, so the shipped example validated, installed as the workspace default, and then refused every race), `paths-relaxed.json` (the default with `pyproject.toml` removed from the harness set, and a docs note saying what that costs).
- `testdata/stream/` — recorded evidence streams for the **pure** parser: `stream-pass.txt`, `stream-fail.txt`, `stream-rerun.txt`, `stream-no-finish.txt`, `stream-seq-gap.txt`, `stream-after-finish.txt`, `stream-bad-nonce.txt`, `stream-unknown-kind.txt`. As with `testdata/oracle/`, the parser tests need no Python and no plugins.
- `testdata/plainrepo/` — **NOT SHIPPED**, and the acceptance step that used it (11) is not shipped either. The lesson it encodes is real and is instead carried by `testdata/adversarial/repo/`, which has no `.gitignore`, no `conftest.py`, no `pytest.ini` and no CI config, and which every corpus vector races against. The paragraph below stands as the reasoning.
- `testdata/toyrepo/` gains **nothing** by default — and that is the point. The study's closing lesson was that `testdata/toyrepo/.gitignore` already contained `.multiverso/`, so `mvo init` changed nothing there and the fixture was *structurally immune to our worst bug*. A second fixture, `testdata/plainrepo/`, is therefore added with **no** `.gitignore`, **no** `conftest.py` and **no** `pyproject.toml`, and the ignore-rule and harness-addition steps run against it. A fixture that cannot fail the way real repos fail certifies the docs, not the product.

## Acceptance script (CI runs this)

`scripts/accept.sh` — M1e's steps kept, three amended and six inserted.

- **2, 3a (amended)** — `mvo init` writes the corrected default, so the ladder is four rungs. Step 3a additionally asserts the winner carries a `tree-guard` receipt with `metrics.protected_modified == 0`, `execution.argv == []` and `execution.evidence_regime == "control-plane"`; and that the `pytest-suite` receipt carries `execution.evidence_regime == "streamed"`, a non-empty `execution.evidence_plugin`, and `result.tools["mvo-evidence"] == "v0"`. Steps 2/3 still SELECT patch-a (patch-b breaks `clamp`, so exactly one world passes and the tie rule cannot fire).
- **3f (amended)** — the O0 collected-count guard now runs under `policy use no-paths`, because under the new default `patch-cut` is stopped one rung earlier. M1e's contract (`patch-cut` fails `collected-not-below@collect` with `collected_delta=-3`; `patch-wipe` fails `collect-nonempty@collect` with `exit_code == 5`; neither loser has a suite receipt) stays under live test rather than being deleted along with the behaviour it proves.
- **3j — the protected-paths gate, all five vectors (new)** — `policy use default`; `intent new --budget-candidates 6`; race `patches-forge` plus the honest `patch-a`. Assert `SELECT` with patch-a's world winning; **each** of the five cheat worlds failed `paths-unmodified@guard` as its first gate; the violation kinds are exactly `harness_added, harness_added, protected_modified, protected_modified, protected_modified`; **no cheat world has a `pytest-collect` or `pytest-suite` receipt** (the ladder short-circuited at rung O-1, so five forgeries cost five tree walks); and each guard receipt's `tree-guard-report` artifact names the offending path.
- **3k — the stream is the source, and the file is not (new)** — `policy use no-paths`; race `patches-forge-deep`. Assert: `patch-forge-instats`'s world has `status == "error"` with the S3 disagreement reason in its stderr artifact; `patch-exit-instats`'s world has `execution.exit_code == 0`, `status == "error"` and the S2 reason; **no world reports `tests_passed` from the forged file** — every recorded `tests_*` metric equals the stream's. Then a control assertion that the mechanism has teeth rather than merely erroring on everything: the honest `patch-a` in the same race passes with `status == "pass"`.
  > **Shipped in reduced form (integration gate).** `patches-forge-deep/` was not built, so the in-application-code variants are unit-tested only (`internal/oracle/evidence_test.go`). What step 3k *does* run is the load-bearing half, and it was worth insisting on because without it the block's central mechanism had **no end-to-end coverage at all**: the adversarial corpus stops eleven of its twelve laundering vectors at rung O-1 (the twelfth, `12-planted_artifact`, at `status-pass@suite`), so not one of them reaches the stream under the shipped default. Step 3k races the study's vector 1 (`patch-forge-artifact` — the `conftest.py` writing a fabricated 500-test `junit.xml` at `atexit`) under `no-paths.json`, with the guard gate, the guard oracle and the invariant all absent. It asserts the metrics are the stream's (`tests_total=8, tests_failed=2, tests_passed=6`, not the forged 500), that the first failed gate is `status-pass@suite`, that the decision is `REJECT`, and that the suite receipt records `evidence_regime: "streamed"`, a non-empty `evidence_plugin` and `result.tools["mvo-evidence"] == "v0"`. Under M1e this exact patch passed every hard gate and landed on trunk.
- **3l, 3m, 3n, 11 — NOT SHIPPED.** `scripts/accept.sh`'s step markers run 1, 2, 3, 3a–3k, 3o, 3p, 3q, 4–10 (plus 9a). The four steps below were designed and not built; what stands in for each is named inline. `BUILDLOG.md` disclosed this and this document did not, which is the wrong way round for a normative contract.
- **3l — a plugin that cannot be silenced into a pass (new)** — *not shipped; `internal/oracle/evidence_test.go`'s S1 table is the standing test.* — a patch whose `pytest.ini` sets `addopts = -p no:mvo_evidence`, raced under `no-paths.json` (so the harness seal is not what catches it). Assert `status == "error"`, the S1 reason `no usable evidence stream`, `metrics` carries **no** `tests_*` key at all, and the world failed `status-pass@suite`. The negative form of the honesty rule, under live test.
- **3m — the cross-oracle invariant (new)** — *not shipped; `internal/race/invariant_test.go` and `internal/policy/invariant_test.go` are the standing tests.* — `policy use no-paths-invariants` (no guard, invariant on); race `patch-inflate` + `patch-a`. Assert the decision type is `ESCALATE`, the rationale contains `escalated by policy rule on_invariant_violation`, `mvo explain --json` reports the violating world with detail `collected_total=8 != tests_total=500`, and the honest world is rendered as **leader, not winner** (no `WINNER` and no `won` in the output — M1e decision 21's rule, now exercised by rule 0).
- **3n — the corrected default ranking (new)** — *not shipped as its own step; accept.sh step 3's fake-agent race asserts the ESCALATE-on-tie under the shipped default, which is the study's central product fix, and `internal/race/decide_test.go` covers the key-2 SELECT.* — race `patches-rank`'s two candidates under the **shipped default** (both pass every gate; `patch-c` adds two passing tests). Assert `SELECT` at key 2 `tests_passed_desc`. Then race `patches-tie` under the shipped default and assert `ESCALATE` with `on_ranking_tie` — under M1e's default this race was decided by `wall_ms_asc`, i.e. by jitter, and the assertion that it no longer can be is the study's central product fix under test. Also assert `mvo explain` never prints `wall_ms_asc` for a default-policy race.
- **3o — `mvo guard`, the adoption wedge (new)** — `mvo guard --base HEAD --policy default` on a clean tree exits 0; after `sed`-ing one assertion in `test_stats.py`, it exits 1 and names `test_stats.py` with kind `protected_modified`; `--json` emits the `tree-guard-report` shape. No ledger event is appended by either run (asserted by event count).
- **3p — docs freshness (new, red-team pass)** — grep `docs/quickstart.md` and this document for the shipped default policy digest, and the quickstart for the current known-gate list, `paths-unmodified@guard`, `plugin_autoload`, `multiverso.dev/audit-report/v1`, and the *absence* of the old "It does not sweep" claim. A grep is a poor proof of correctness and a perfectly good proof of non-rot, which is the failure that actually happened — twice, in opposite directions, in the one document design partners read.
- **3q — the shipped `sealed.json` is installable and raceable (new, red-team pass)** — `policy validate`, `policy use sealed`, then a real race that must SELECT the honest patch and whose ladder must include `skips-not-above@suite`. The fixture used to declare `evidence.regime: "isolated"`, so it validated, installed as the workspace default, and then refused every race; nothing caught it because accept.sh never installed it.
- **7 (amended)** — the second-machine `mvo audit --json` additionally asserts `cas_checked > 0`, `cas_missing == []`, `cas_corrupt == []`, `attestations_verified >= 1`, and the schema string `multiverso.dev/audit-report/v1`. `MIN_DECISIONS` rises by the new races.
- **9a — CAS tamper, the two shapes the study found unreported (new)** — (a) delete the attestation bundle blob from CAS; assert `mvo audit` exits 1 with a `MISSING:` line naming the digest and `attestation.recorded seq N (bundle)`. Restore it. (b) Flip one byte inside a stored `evidence-stream` artifact; assert a `CORRUPT:` line naming both digests and the referring receipt. Restore it, and assert the final audit is clean — the study's two `OK`s, now failures.
- **10 (amended)** — the final clean audit runs with `--require-decisions 1`.
- **11 — the plain-repo fixture (new)** — *not shipped (see `testdata/plainrepo/` above).* — `mvo init` in `testdata/plainrepo` (no `.gitignore`, no harness files): assert the ignore rule went to `.git/info/exclude` and that `git status --porcelain` is empty (the key-disclosure chain, tested on a repo that can actually exhibit it); then race a candidate that adds a `conftest.py` and assert `harness_added`.

## Testing bar

- `internal/oracle` (stream): pure-parser table over `testdata/stream/` for every rule 1–7 (header, nonce mismatch, sequence gap/repeat/decrease, missing/duplicate `session_finish`, records after finish discarded + noted, unknown kind ignored + noted, over-cap); metric derivation including reruns (first-run vs final per nodeid) and the JUnit equivalence classes (`xfailed`→skipped, `xpassed`→passed); FIFO lifecycle tests with a **fake writer process** (no pytest required): a writer that never opens, a writer that opens and never finishes, a writer that floods past the cap, a writer that unlinks and replaces the FIFO — each asserting *absent metrics and a failed gate*, never a pass; the S1/S2/S3 table across {exit 0, exit 1, exit 5} × {stream ok, incomplete, absent} × {junit agrees, disagrees, absent}.
- `internal/oracle` (guard): the glob grammar table (root-only vs `**/`, `*` not crossing `/`, `[...]` per segment, unparseable patterns); every violation class including mode-only changes and `tree_drift`; the report artifact's canonical bytes and sort order; a base tree that cannot be read ⇒ `status = error`, metrics absent, gate fails; `argv == []` and `evidence_regime == "control-plane"` asserted as invariants of the kind; and a test that the guard performs **no** process execution (a backend fake that fails any `Command` call).
- `internal/oracle` (registry conformance, extended): each implementation emits a subset of its declared metric vocabulary; `tree-guard` emits `{}` tools; a receipt from every kind carries a non-empty `evidence_regime`.
- `internal/policy`: decode/validate table for rules 9–15 (unknown invariant name, wrong role set, role naming an oracle that cannot emit the metric, unequal `args` under `collect-equals-suite-total`, duplicate invariant, `paths-unmodified` with empty path sets, unparseable pattern, unknown enum values, a configured `tree-guard`); `Default()` canonical-bytes golden with the **new** digest pinned; a golden proving an M1e-era `policy/v1` file (no `paths`/`invariants`/`evidence`) still decodes, compiles to M1e-identical gates/keys/escalation, and yields `regime=auto, crosscheck=require, protected_additions=allow`; `policy show --json | policy validate` round-trip.
- `internal/race` (`Decide`/`Trace`): invariant evaluation table (holds, violated, metric absent, receipt absent, not-evaluated after a gate failure); rule 0's precedence over all four M1e rules, and its non-firing when `invariants` is empty; **byte-for-byte M1e goldens for every rationale form under a policy with no invariants** — the compatibility proof, sitting beside M1e's own v0 goldens; permutation invariance extended to guard receipts.
- `internal/race` (orchestrator): per-world oracle construction; the guard running first and short-circuiting the ladder (a world failing `paths-unmodified` has exactly **one** receipt); regime selection (`auto` → `streamed` under T0, `isolated` under a T1 fake with a distinct uid); the `evidence.regime: "isolated"` pre-flight abort leaving the ledger empty; the plugin materialized once, mode `0444`, and its digest recorded.
- `internal/backend`: `OpenOpts` mount construction goldens for T1 (four mounts, correct `:ro` flags, `--user` on oracle execs only); T0 identity mapping unchanged; the docker-gated tests skip cleanly with a named reason when no daemon is present.
- `internal/admit`: the invariant ESCALATE (before the ADMIT/REJECT split, only with a declaring policy); the landing guard's base tree is the pre-apply trunk tree; M1a/M1e sentences reproduced byte-for-byte under policies without invariants.
- `internal/audit`: the referenced-set walk over a synthetic ledger covering every row of the table; missing and corrupt injection for each referrer kind; unreferenced objects counted and not failed; `--cas-sweep=false` reporting `cas_checked: 0` and saying so; `--require-decisions` exit codes; the empty-workspace line.
- `internal/object`: `Execution` golden canonical bytes and digest stability re-derived; regime constants exhaustively covered by a switch test; nil-map constructor test unchanged.
- `cmd/mvo`: `explain` human and `--json` goldens for SELECT / REJECT / ESCALATE-on-invariant / a guard-failed world / an M1e-policy decision (unchanged output); `mvo guard` output goldens and exit codes; `policy show --builtin`; `policy list`'s adoption hint; the audit human and JSON goldens for clean, missing and corrupt.
- The **Python plugin** is tested two ways, neither requiring pytest to be installed for the unit suite: a pure Go test asserting the embedded source's digest matches the recorded constant and that the file is written `0444`; and an end-to-end step, skipped with a named reason when pytest is absent, asserting that a real pytest run over `testdata/toyrepo` produces a stream whose derived metrics equal the JUnit-derived ones (**the S3 equivalence, proved rather than assumed** — this is the test that catches a pytest version changing a counting rule).
- `gofmt -l` clean; `go vet ./...` and `go test ./...` pass with no docker daemon and no pytest plugins present; `scripts/accept.sh` is the e2e test and runs in CI. `go.mod`/`go.sum` unchanged. No test invokes a real agent CLI, pulls an image larger than `python:3.12-alpine`, or requires network.

## Explicitly NOT in M1f

Any claim to defeat an in-process adversary who patches the plugin (threat model, said three times because it will be misread once); a MAC, signature or attestation *inside* the test process (decision 15); per-test subprocess isolation or a pytest-xdist-style worker protocol; T2 microVM isolation (XP-4); O2 property/differential and O3 mutation oracles (EP-4/EP-5, M2) — which is what actually closes vectors 4 and 5 when the guard is relaxed; LLM monitor receipts and suspicion scores (ch. 13's `monitor-verdict`, v1); FREEZE as a decision type after incrimination (CP-7); a semantic protected-paths gate (TOML/AST-aware sealing — decision 17); path sets derived from an impact map (KP-1, M3); baseline *suite* measurement, and therefore any baseline-relative skip or coverage gate; quarantine sets and flake policy beyond M1e's first-run rule (EP-6); JS/TS and Rust oracle kinds; policy signing or per-policy trust roots (TP-5); key supply/pinning for CI deployment (a named M1g prerequisite, called out by the study and not fixed here); deterministic world digests across runs (also outstanding, also not this block); `mvo doctor`; Windows.
