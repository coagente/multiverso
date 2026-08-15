# Quickstart

> From nothing to a signed, offline-verifiable commit in about five minutes, with no API keys and — after the clone — no network. Every command below was run against the shipped fixtures on macOS (Go 1.25, Python 3.12, pytest 8.4, git 2.51); every output block is pasted from that run.
>
> Two conventions in the blocks below.
>
> - **Digests, timings and commit shas will differ on your machine.** An intent digest includes its creation timestamp and base commit, and every world, decision and evidence digest descends from one. Only the *policy* digests are stable, because a policy is a file whose bytes you control: a stock `mvo init` always mints `mv0:f207c3fa…` for the default.
> - **`mv0:1234abcd…` in this document means a digest truncated for page width.** mvo itself always prints all 64 hex characters, in every verb, including inside `evidence:` and `rationale:` lines.
>
> Modulo those two things, output should match line for line.

**Contents**

1. [What you need](#what-you-need)
2. [Build](#build)
3. [Zero to an attested commit](#zero-to-an-attested-commit) — the five-minute path
4. [On your own repo](#on-your-own-repo) — Python and everything else
5. [Real agents](#real-agents-claude-code-and-codex) — what it costs, and what is actually enforced
6. [T1 containers](#t1-containers)
7. [Sharing a race](#sharing-a-race)
8. [Reference](#reference) — verbs, exit codes, layout, troubleshooting

## What you need

| Requirement | Why |
|---|---|
| **Go 1.25+** | to build `mvo`. No third-party Go dependencies beyond the SQLite driver already in `go.mod`. |
| **git 2.x**, with an identity | worlds are `git worktree`s; admission is a compare-and-swap on a local branch ref. The walkthrough commits, and `mvo admit` writes a commit — so `user.name` and `user.email` must resolve (`git config --global user.email you@example.com` if `git commit` ever told you to). |
| **Python 3 + pytest** | the default policy's oracle ladder is pytest-based, and runs it as `python3 -m pytest`. Skip only if you go straight to [`--oracle-cmd`](#a-non-python-repo). |
| `coverage.py` | enables the `coverage_bp` metric. Optional for [§3](#zero-to-an-attested-commit); **required by [Author your own policy](#author-your-own-policy)**, which builds a coverage floor gate. Absent means the metric is absent — never zero, and a coverage gate then has nothing to pass on. |
| `sqlite3` CLI *(optional)* | only for `scripts/accept.sh`, which queries `ledger.db` directly. `mvo` itself never shells out to it. |
| Docker *(optional)* | only for [T1 containers](#t1-containers), and for the one docker-gated step in `scripts/accept.sh`. |
| An agent CLI *(optional)* | only for [real agents](#real-agents-claude-code-and-codex) — and even there, [the shipped fakes](#rehearse-against-the-fixtures-first) stand in for it. Section 3 needs none. |

## Build

```bash
git clone https://github.com/coagente/multiverso
cd multiverso
go build -o mvo ./cmd/mvo
export PATH="$PWD:$PATH"
mvo help
```

`mvo` is a single static binary; `go build` writes it to the repo root, where `.gitignore` already excludes it.

**That `export` is a required step, not a suggestion.** Every block from here on calls the binary plainly as `mvo`, so it has to be findable by name. Run the rest of this document **from the repo root, in that same shell**: the fixtures the walkthrough copies (`testdata/toyrepo/`, `testdata/fakeagent/`) are named relative to it. If you would rather install `mvo` somewhere permanent, `cp mvo /usr/local/bin/` works just as well — `--dir` is what points it at a repo, so it never needs to live next to one.

Optional sanity check — `go test ./...` takes about half a minute from cold, `scripts/accept.sh` about a minute:

```bash
go test ./...
scripts/accept.sh
```

Neither invokes a real agent CLI: `accept.sh` puts `testdata/fakeagent/` on `PATH` for its agent steps. `go test ./...` never touches the network. `accept.sh` has one docker-gated T1 step, and if the `multiverso-t1-fixture:v1` image is not already built it runs `docker build`, which pulls `python:3.12-alpine` — that is the only network access in either command, and the largest thing either will ever pull. With no docker daemon, or with the build offline, that step prints `accept: T1 step SKIPPED (…)` and the script still passes.

## Zero to an attested commit

### 1. A toy repo with a real bug

The repo ships one. `testdata/toyrepo/` is a three-function Python module whose `mean()` divides by `len(values) - 1`, a pytest suite that catches it, and two candidate patches.

```bash
export DEMO=/tmp/mvo-demo
rm -rf "$DEMO" && mkdir -p "$DEMO"
cp -R testdata/toyrepo/. "$DEMO"/
rm -rf "$DEMO"/__pycache__
git -C "$DEMO" init -q -b main
git -C "$DEMO" add -A
git -C "$DEMO" commit -qm "toyrepo baseline"
```

The bug is real; the suite fails:

```console
$ (cd "$DEMO" && python3 -m pytest -q)
…
FAILED test_stats.py::test_mean_single - ZeroDivisionError: division by zero
FAILED test_stats.py::test_mean_pair - assert 6.0 == 3.0
2 failed, 6 passed in 0.05s
```

The two candidates in `patches/` are what an agent might plausibly produce:

- **`patch-a.patch`** fixes `mean()`. Correct.
- **`patch-b.patch`** fixes `mean()` *and* breaks `clamp()` by swapping `min` and `max`. Plausible-looking, wrong.

### 2. `mvo init`

```console
$ mvo init --dir "$DEMO"
initialized /tmp/mvo-demo/.multiverso (default policy mv0:f207c3fad59d0fc973e5f342ac54d8b1bc9e5c6cae2a2cff0b33477ddee3c543)
signing key: /tmp/mvo-demo/.multiverso/keys/local.key (PRIVATE, unencrypted — never commit or copy it)
git ignore:  /tmp/mvo-demo/.gitignore (rule already present; nothing written)
```

**Read those last two lines.** `local.key` is an unencrypted ed25519 private key, and it is the trust anchor for every attestation this workspace will ever produce: anyone holding it can mint a `mvo verify` that passes. It never needs to leave the machine, and nothing in mvo ever copies it.

**`init` requires a git worktree** and refuses anything else, creating nothing:

```console
$ mvo init --dir /tmp/not-a-repo
mvo: init: /tmp/not-a-repo is not a git repository (or does not exist); mvo requires a git worktree — run `git init` there first
$ echo $?
1
```

**Where the ignore rule goes.** `init` makes git ignore `.multiverso/`, and it writes that rule to **`.git/info/exclude`**, not to the tracked `.gitignore`:

```console
$ mvo init --dir "$FRESH"
…
git ignore:  /tmp/fresh/.git/info/exclude (untracked; survives `git reset --hard`)

$ git -C "$FRESH" status --short      # nothing: init leaves the tree CLEAN
$ git -C "$FRESH" check-ignore -v .multiverso/keys/local.key
.git/info/exclude:7:.multiverso/	.multiverso/keys/local.key
```

`.git/info/exclude` is untracked, so the rule survives `git reset --hard` and `git checkout -- .`, and `init` leaves your working tree clean. That matters more than it sounds: when `init` edited the tracked `.gitignore` instead, it dirtied the tree, `mvo admit` then warned that the tree lagged trunk, and the obvious remedy — `git reset --hard` — silently reverted mvo's own ignore line, after which the next `git add -A && git commit` swept `.multiverso/keys/local.key` into the repo. A `git push` would then have published the signing key.

A repo that already lists `.multiverso/` in its committed `.gitignore` (like the shipped fixture) keeps that, and nothing is written anywhere. If `.git/info/exclude` cannot be written, `init` falls back to `.gitignore` and says so loudly on stderr, naming the private key path — commit that change immediately if you see it.

That created five things:

```console
$ find "$DEMO/.multiverso" -maxdepth 1 -mindepth 1 | sort
/tmp/mvo-demo/.multiverso/cas
/tmp/mvo-demo/.multiverso/config.json
/tmp/mvo-demo/.multiverso/keys
/tmp/mvo-demo/.multiverso/ledger.db
/tmp/mvo-demo/.multiverso/policies
```

| | |
|---|---|
| `ledger.db` | hash-chained append-only SQLite event log — the source of truth |
| `cas/` | content-addressed blobs: patches, transcripts, raw oracle artifacts |
| `config.json` | `{schema, default_policy}` |
| `policies/` | authored policy files; `default.json` written now |
| `keys/` | ed25519 signing keypair, generated here, mode 0700 |

`init` refuses to run twice. `mvo init --keys` retrofits a keypair into a workspace that predates signing.

### 3. Read the policy before you use it

The policy is what will judge every candidate, and it is pinned into the intent at creation. Look at it first:

```console
$ mvo policy show default --dir "$DEMO"
digest:    mv0:f207c3fad59d0fc973e5f342ac54d8b1bc9e5c6cae2a2cff0b33477ddee3c543
schema:    multiverso.dev/policy/v1
name:      default
gates (ordered):
  1. paths-unmodified@guard           oracle=guard basis>=construction threshold=0
  2. collect-nonempty@collect         oracle=collect basis>=construction threshold=0
  3. collected-not-below@collect      oracle=collect basis>=construction threshold=0
  4. status-pass@suite                oracle=suite basis>=construction threshold=0
ranking:   gate_pass,tests_passed_desc,world_digest_asc
escalation: on_invariant_violation, on_all_worlds_failed_machinery, on_ranking_tie
oracles:
  collect      kind=pytest-collect config=mv0:0544af0df9926013d520bd5b9347c6fd0f33080fffafba45476d4fa8256dc1fd argv=python3 -m pytest
  guard        kind=tree-guard config=mv0:00fafab73e7e7852e6df8940d0a6387f8680917eb2a2677d7fcbf1ca8f2fcf5a argv=
  suite        kind=pytest-suite config=mv0:a6a5a160ea28c4c8386691caa64d493bef58adb411c2bd07332231c144cf20f7 argv=python3 -m pytest
required:  guard,collect,suite
paths (frozen against the candidate):
  protected  (modify/delete) **/*_test.py **/test_*.py test/** tests/**
  harness    (modify/delete/create) **/*.dist-info/** **/*.egg-info/** **/*.pth **/.gitignore **/conftest.py **/sitecustomize.py pyproject.toml pytest.ini setup.cfg tox.ini
  additions  allow
invariants (cross-oracle):
  collect-equals-suite-total   roles collect,suite
evidence:  regime auto, crosscheck require, plugin_autoload off
source:    /tmp/mvo-demo/.multiverso/policies/default.json
{"escalation":{"min_candidates_passing":0,…}}
```

Read that as: **check the candidate did not touch the harness or the tests → collect the tests → check nobody deleted any → run the suite**, in that order, stopping at the first failure. Then rank the survivors lexicographically: passing beats failing, more passing tests beats fewer, and if all of that ties, `on_ranking_tie` escalates to a human rather than letting the world digest pick a winner.

Four things in that output are worth a second look, because they are the whole of what M1f changed:

- **Rung O-1, `paths-unmodified@guard`, runs before any Python does.** It compares two git trees the control plane holds — the intent's base and the candidate's — and executes nothing. A candidate that edits a test or drops in a `conftest.py` is stopped here, for the cost of two tree walks, and never reaches a collect or a suite run.
- **`paths`** is what it compares. `protected` is frozen against modification and deletion (adding a regression test is allowed and encouraged); `harness` is frozen against modification, deletion **and creation** — a repo with no `conftest.py` today must not acquire one from an untrusted generator.
- **`ranking` has no `wall_ms_asc`.** It was the default third key until a study measured an assertion-weakening cheat beating the honest fix 6 races out of 10 on ~100 ms of scheduler jitter, each time with a signed rationale naming the stopwatch as decisive. Wall time is noise; a tie-break on noise is a coin flip wearing a signature. `on_ranking_tie` escalates instead. The key is still in the vocabulary if you want it.
- **`evidence`** says how a run will be *observed*: metrics come from a control-plane-owned pytest plugin streaming to a channel mvo reads live, `crosscheck require` makes JUnit XML a second opinion rather than a source, and `plugin_autoload off` sets `PYTEST_DISABLE_PLUGIN_AUTOLOAD=1` in the run so a candidate cannot ship an `*.egg-info` that loads its own plugin into the test process. If your suite genuinely needs an installed plugin (`pytest-asyncio`, `pytest-django`), set it to `"on"` — deliberately, in the pinned artifact, knowing what it costs.

The gate order is the whole trick. `collected-not-below` runs *before* `status-pass`, so a candidate that makes the suite green by deleting tests is stopped by the counts and its suite is never even run — and under the shipped default `paths-unmodified` stops it one rung earlier still. You will see both in [§4](#what-a-rejection-looks-like).

`mvo policy list` shows every policy the workspace knows, and which one is the default.

### 4. `mvo intent new`

```console
$ INTENT="$(mvo intent new --dir "$DEMO" \
    --title "fix mean()" \
    --desc "mean() divides by len-1; make the suite pass")"
$ echo "$INTENT"
mv0:223b311241502607f2114f0c8c6859a84ff5c328292fa849aebff67db1e9374c
```

The intent captures `HEAD` as its base commit and tree, pins the workspace's default policy digest, and takes a budget (`--budget-candidates`, default 2; `--budget-wall-ms`, default 600000). It prints one line: the digest, which is the handle for everything that follows.

That digest is now a permanent statement of intent — the rules that will judge this work were fixed before any candidate existed.

> **Keep the digest.** It is the handle for six verbs (`race`, `worlds`, `explain`, `admit`, `publish`, `prune`), and **there is no `mvo intent list` in M1**. Capturing it in a shell variable as above is the intended flow.
>
> If you lose one, it is recoverable: an object's digest is the SHA-256 of its canonical bytes, and the ledger stores exactly those bytes in the `intent.created` payload. So hashing the payload reproduces the digest —
>
> ```console
> $ sqlite3 .multiverso/ledger.db "select payload from events where type='intent.created'" \
>   | while read -r p; do
>       printf '%s' "$p" | shasum -a 256 | awk '{printf "mv0:%s  ", $1}'
>       printf '%s' "$p" | python3 -c 'import json,sys; print(json.load(sys.stdin)["spec"]["title"])'
>     done
> mv0:3b1a1d01854a20ac69c02c6989412c0aa4d05ba43fda28d23f670724e25fca85  fix mean()
> mv0:5b7e245ed5d6765cb7e2167d999bb174e4f43643f009571fc62aa1f8b424cefd  unraced
> ```
>
> That works because canonical JSON is byte-stable, which is the same property `mvo audit` relies on. It is still a workaround for a missing verb — save the digests.

### 5. `mvo race`

The **script adapter** takes candidate patches from a directory instead of calling an agent. One patch, one world, sorted by filename. No API keys, no network, no money.

```console
$ mvo race "$INTENT" --dir "$DEMO" --agent script --patches "$DEMO/patches"
SELECT mv0:18ba934906021f6660486bf0e41f966668a7615021707b83c8263349683cd41a (decision mv0:03a60e9594b34ab6646cb7c1d79f22e501d4ef8732d567e1524fe51f052ad73a, 2 worlds)

real	0m8.542s
```

In those eight seconds mvo: measured the base tree's test count (the denominator for `collected_delta`), created two `git worktree` worlds at the base commit, applied one patch in each, captured the resulting diff and tree digest, ran `pytest --collect-only` and then the suite in each world, stored every raw artifact in CAS, and ran the decision function.

Add `--parallel N` to run the worlds concurrently.

**What is order-independent, and what is not.** The decision inputs are digest-sorted before the pure function sees them, so *shuffling the inputs cannot change the result*: concurrency cannot reorder them, and re-running `mvo audit` over the same recorded inputs reproduces the same decision bytes forever. That is a real guarantee, and it is the one the ledger rests on.

**It does not make the winner stable across runs — unless the ranking says so.** A ranking key that reads a *measured* quantity is a key that reads noise. The default's third key used to be `wall_ms_asc`, and a design partner study measured what that costs: in a repo with a real off-by-one bug, an assertion-weakening cheat and the honest fix both passed every hard gate, tied on `tests_passed`, and were separated by ~100 ms of scheduler jitter — the cheat won 6 races out of 10, each time with a signed rationale naming `wall_ms_asc (3809 < 3922)` as the decisive key.

The shipped default no longer does that. Its ranking is `[gate_pass, tests_passed_desc]` and `escalation.on_ranking_tie` is **on**, so a genuine tie is reported as a tie:

```console
$ I="$(mvo intent new --dir "$TIE" --title "tie")"
$ mvo race "$I" --dir "$TIE" --agent script --patches "$TIE/patches-tie"
ESCALATE (decision mv0:…, 2 worlds)

$ mvo explain "$I" --dir "$TIE" | sed -n '/^escalation/,+2p'
escalation: on_ranking_tie
            mv0:f595… and mv0:f717… tie on every ranking key [gate_pass,tests_passed_desc]; only world_digest_asc would order them
```

A correct refusal beats a confident wrong answer. `world_digest_asc` is still appended as the final key so the *ordering* stays deterministic and replayable — but the rendering calls it "tie-break only — not a decision", and the escalation is what the operator acts on.

`wall_ms_asc` is still in the vocabulary. If you put it back, you are choosing a stopwatch as a tiebreak, and you should say so out loud in your policy review:

```json
{"escalation": {"on_ranking_tie": true},
 "ranking": ["gate_pass", "tests_passed_desc", "coverage_desc"]}
```

`--parallel` itself is just a speed knob (`real 0m8.5s` → `0m4.6s` on the two-candidate race), and it changes no decision the sorted inputs would not have produced anyway.

### 6. `mvo worlds` and `mvo explain`

The scoreboard:

```console
$ mvo worlds "$INTENT" --dir "$DEMO"
WORLD                                                                 OUTCOME    GATE               WALL_MS  USD_MICRO  TIER
mv0:18ba934906021f6660486bf0e41f966668a7615021707b83c8263349683cd41a  COMPLETED  pass               3879     0          T0-worktree
mv0:ad21cf57cb0b64d2ebf46f9226e025d22b9ad8807295e3ac124f2ea824827db0  COMPLETED  status-pass@suite  3924     0          T0-worktree
freshness: FRESH (base 4bd0d29c3feb == main head)
```

`OUTCOME` is the producing run's terminal state; `GATE` is `pass`, or the name of the **first** gate that failed — the one that stopped the ladder. `freshness` is computed at render time by comparing the intent's base against the current branch head; it is never stored.

The full argument:

```console
$ mvo explain "$INTENT" --dir "$DEMO"
decision:  mv0:03a60e9594b34ab6646cb7c1d79f22e501d4ef8732d567e1524fe51f052ad73a
type:      SELECT
intent:    mv0:223b311241502607f2114f0c8c6859a84ff5c328292fa849aebff67db1e9374c  (fix mean())
policy:    mv0:f207c3fad59d0fc973e5f342ac54d8b1bc9e5c6cae2a2cff0b33477ddee3c543  (default, policy/v1)
evidence:  regime streamed (T0-worktree; isolated not deliverable in this binary), plugin sha256:69b43012e0335506…
winner:    mv0:18ba934906021f6660486bf0e41f966668a7615021707b83c8263349683cd41a

gates (ordered, first failure stops the ladder):
  RANK  WORLD                                                                 ORD  paths-unmodified@guard  collect-nonempty@collect  collected-not-below@collect  status-pass@suite   INVARIANTS  RESULT
  1     mv0:18ba934906021f6660486bf0e41f966668a7615021707b83c8263349683cd41a  1    pass                    pass (total=8)            pass (delta=+0)              pass                ok          PASS
  2     mv0:ad21cf57cb0b64d2ebf46f9226e025d22b9ad8807295e3ac124f2ea824827db0  2    pass                    pass (total=8)            pass (delta=+0)              FAIL (status=fail)  n/a         FAIL

why mv0:18ba934906021f6660486bf0e41f966668a7615021707b83c8263349683cd41a won  (ranking [gate_pass, tests_passed_desc, world_digest_asc]):
  vs mv0:ad21cf57cb0b64d2ebf46f9226e025d22b9ad8807295e3ac124f2ea824827db0 (rank 2): decided at key 1 gate_pass (pass > fail)

evidence:  mv0:0af6fb304d460076f88ca6d9d0e490a1ed0d15bfaf359f03284821cdd50a357a  (pytest-suite@mv0:ad21cf57…, fail, coverage_bp=8406, duration_ms=34, tests_errored=0, tests_failed=2, tests_failed_first_run=2, tests_passed=6, tests_passed_after_rerun=0, tests_skipped=0, tests_total=8)
           mv0:33506af05411db8d660a35d90288657abe475a973b2989497368fd7c0fcb041a  (pytest-collect@mv0:ad21cf57…, pass, collected_base=8, collected_delta=0, collected_total=8)
           mv0:88077843c967808e185d21e9d9fe8758c2330e7e5e3a16093fb397ecc36bfc18  (tree-guard@mv0:18ba9349…, pass, harness_added=0, harness_deleted=0, harness_modified=0, paths_examined=2, protected_added=0, protected_deleted=0, protected_modified=0)
           mv0:9b4f5a5bdcc2b9593bfd4f05da2d1787785619a581ec36ffdda082978f5323ad  (pytest-collect@mv0:18ba9349…, pass, collected_base=8, collected_delta=0, collected_total=8)
           mv0:c6375e2def2a09efb82065b1477d8eb84bab3f9981144e32f27c0c051414ee4e  (pytest-suite@mv0:18ba9349…, pass, coverage_bp=8261, duration_ms=26, tests_errored=0, tests_failed=0, tests_failed_first_run=0, tests_passed=8, tests_passed_after_rerun=0, tests_skipped=0, tests_total=8)
           mv0:d3326332d586437c43a81d3a764cc84b4d25f4bf10134398e9b0e073bf3370a0  (tree-guard@mv0:ad21cf57…, pass, harness_added=0, harness_deleted=0, harness_modified=0, paths_examined=2, protected_added=0, protected_deleted=0, protected_modified=0)
rationale: 1/2 worlds passed hard gates [paths-unmodified@guard,collect-nonempty@collect,collected-not-below@collect,status-pass@suite]; selected mv0:18ba9349… (sole world passing all hard gates); ranking [gate_pass,tests_passed_desc,world_digest_asc]
freshness: FRESH (base 4bd0d29c3feb == main head)
```

`patch-b` broke `clamp()`, two tests failed, `status-pass` failed, done. Both candidates carry a `tree-guard` receipt too: neither touched a test file or a harness file, so both cleared rung O-1 and paid for the Python runs. Nothing in that table is stored: the gate results, the ranking walk and the metrics are all recomputed at render time from the ledger, CAS and the pinned policy, by the same pure functions the decision used. Improving the renderer can never change a decision.

`--diffs N` appends the top-N candidates' captured patches, so you can read the change without leaving the tool:

```console
$ mvo explain "$INTENT" --dir "$DEMO" --diffs 1
…
patch (rank 1) mv0:18ba9349…  [sha256:0993b2025fbe0218eb2e89f37d1137e3b006f8d52bc510ae555c3ba7a93ca2ce, 380 bytes]
diff --git a/stats.py b/stats.py
index f206bcb..a1dd77c 100644
--- a/stats.py
+++ b/stats.py
@@ -5,7 +5,7 @@ def mean(values):
     """Return the arithmetic mean of a non-empty sequence."""
     if not values:
         raise ValueError("mean() of empty sequence")
-    return sum(values) / (len(values) - 1)
+    return sum(values) / len(values)
 
 
 def clamp(value, low, high):
```

`--json` emits the machine-readable report (`multiverso.dev/explain-report/v0`) with the same content.

> **Save `explain` before you `admit`.** `mvo explain` resolves an intent to its **latest** decision. After an admission that is the ADMIT — one subject, no candidate table, no gate rows for the rejected candidates — and **there is no `--decision` flag in M1** to ask for the earlier one. The race decision is still in the ledger and still replayable, but this verb will not show it to you again.
>
> This bites at exactly the wrong moment: the natural order of work is race → land the good patch → *then* write the PR comment explaining why the others lost, and by then the table you wanted is unreachable. Redirect it to a file first:
>
> ```bash
> mvo explain "$INTENT" --dir "$DEMO" --json | tee explain.json
> mvo explain "$INTENT" --dir "$DEMO" --diffs 3 | tee explain.txt
> mvo admit "$INTENT" --dir "$DEMO"
> ```
>
> Recovering it afterwards means reading the decision object out of CAS by hand (`.multiverso/cas/sha256/ab/cd…`, per [Workspace layout](#workspace-layout)) and re-deriving the table yourself. Saving two files first is much cheaper.

### 7. `mvo admit`

```console
$ mvo admit "$INTENT" --dir "$DEMO"
ADMIT mv0:18ba934906021f6660486bf0e41f966668a7615021707b83c8263349683cd41a
commit:      f5b6bf4361af9802d7380c04463db9b0e304563d
attestation: sha256:cf3eabf82092483dda59807a0858d25f2be24d5582e6dab1a679b5aa5f474cb9
decision:    mv0:9b46d60850c3c2c8e2484503dadb5c1336c25f8870975cdf7894cb1d534dc3f5
```

Admission does **not** trust the race's receipts. It applies the winner's patch to the current trunk tree, re-runs the pinned policy's gates on the exact tree that will land, and attests *those* receipts. If trunk moved since the race, the gates are judging the new reality, not the old one. If a gate fails now, nothing lands.

The result is an ordinary Git commit:

```console
$ git -C "$DEMO" log --oneline
f5b6bf4 fix mean()
4bd0d29 toyrepo baseline

$ git -C "$DEMO" log -1 --format=%B
fix mean()

Multiverso-Intent: mv0:223b311241502607f2114f0c8c6859a84ff5c328292fa849aebff67db1e9374c
Multiverso-Decision: mv0:9b46d60850c3c2c8e2484503dadb5c1336c25f8870975cdf7894cb1d534dc3f5
Multiverso-Attestation: sha256:cf3eabf82092483dda59807a0858d25f2be24d5582e6dab1a679b5aa5f474cb9
```

Ordinary in the strict sense: any git client, any forge, any tool reads this commit without knowing Multiverso exists. The trailers are the only trace, and they are just text pointing at content-addressed objects.

`admit` updates the local branch ref by compare-and-swap. **Getting it to a remote is your ordinary `git push`** — mvo does not push trunk (see [Status](../README.md#status)).

> **If your working tree is not clean, read this.** Admission still lands the commit — it exits 0 and the attestation is good — but it cannot fast-forward your worktree, so it prints, *after* the success block:
>
> ```console
> note: trunk advanced to 9b0cccea358cdfc6d2d2aad1805cc01c6f199829 but your working tree was not clean, so it was not synced.
> note: your index now contains a STAGED REVERSION of the change just admitted — `git status` will show it, and `git commit` would undo the admitted fix.
> note: run `git reset --hard` to sync, or `git stash` first if you have work in progress.
> ```
>
> That second line is the whole point, and it is not a sync inconvenience. Trunk now points at a commit containing the fix while your index still holds the old content, so git reports the admitted change as staged **in reverse**:
>
> ```console
> $ git -C "$DEMO" status --short
> M  stats.py
> ?? notes.txt
>
> $ git -C "$DEMO" diff --cached HEAD
> -    return sum(values) / len(values)
> +    return sum(values) / (len(values) - 1)
> ```
>
> A reflexive `git commit` there reships the exact bug that was just signed as fixed. **An untracked file alone is enough to trigger this** — no tracked file need be modified; the `?? notes.txt` above is what made the tree unclean.

### 8. `mvo verify`

Offline verification of the attestation, against the repo and `.multiverso/` alone:

```console
$ mvo verify HEAD --dir "$DEMO"
commit:      f5b6bf4361af9802d7380c04463db9b0e304563d
attestation: sha256:cf3eabf82092483dda59807a0858d25f2be24d5582e6dab1a679b5aa5f474cb9
key:         mv0:aa906a862f15313ab8d4eb6e88147cdf0f18aa53c1297ff57af7aee9b873b5b0
OK: attestation verified (7 checks)
```

The seven checks, in order, fail-fast: `bundle_digest` (the trailer names a bundle whose bytes hash to its own CAS key), `signature` (DSSE over canonical bytes), `statement` (in-toto shape), `subject` (the attested tree is this commit's tree and the attested parent is this commit's parent), `references` (every digest the predicate names exists in CAS and was recorded in the ledger, and the ADMIT and SELECT decisions agree with it), `freshness` (every attested gate receipt judged the admitted tree), `budget` (the predicate's consumed wall time equals the sum over the receipts).

`--json` gives the same as a report.

#### `--key` is the trust anchor, not a convenience

By default `mvo verify` trusts `.multiverso/keys/local.pub` — **the workspace's own public key**. Inside the workspace that produced the attestation, that makes verification a *tautology*: it proves the bytes are internally consistent and signed by whoever holds this directory's key, which is the same party that made the claim.

A clone that runs `mvo init`, mints its own key, races a backdoored candidate and admits it produces output **visually identical** to a genuine verification:

```console
$ mvo verify HEAD --dir /tmp/rogue-clone
commit:      …
attestation: sha256:…
key:         mv0:093b32e1cdda801e1a2695a0d2996ee7b4990aa9bd4d4d72e3f2524e6e8cc79a
OK: attestation verified (7 checks)
```

Only `--key` catches it, and then the error is unambiguous:

```console
$ mvo verify HEAD --dir /tmp/rogue-clone --key /path/to/trusted/local.pub
mvo: verify: signature: signing: verify: no signature verified against key mv0:b2b8201560…
  (skipped keyids: mv0:093b32e1cd…)
$ echo $?
1
```

**So: pass `--key` with a public key you obtained out of band whenever the answer matters.** The default is for convenience inside your own workspace, not for judging someone else's work.

#### The reviewer workflow

A reviewer needs no workspace, no ledger and no trust in your machine — a clone and a public key are enough:

```bash
git clone --bare "$REMOTE" review.git        # or an ordinary clone
mvo fetch-race "$SHORT" --dir review --key /path/to/author.pub
```

`mvo publish` puts the closure under `refs/multiverso/intent/<short>/`, and the `evidence` ref's tree is plain files you can read with `git ls-tree` if you would rather not trust the verb either:

```console
$ git -C "$ORIGIN" ls-tree -r --name-only refs/multiverso/intent/<short>/evidence
attestation/sha256_1174b22f….dsse.json
decisions/mv0_079ba7c6….dsse.json
decisions/mv0_2817978c….dsse.json
intent/mv0_86d5e41e….json
policy/mv0_f207c3fa….json
receipts/mv0_0aecadf9….dsse.json
…
worlds/mv0_3a355409….json
```

#### Not yet supported

Naming these is cheaper than letting a security reviewer discover them:

| | Status in M1 |
|---|---|
| **Key rotation** | **None.** There is no re-signing path and no way to mark a key superseded. A rotated key orphans every attestation it signed. |
| **Revocation** | **None.** No CRL, no expiry, no revocation list. A leaked key stays valid forever. |
| **A trust store** | **None.** `--key` takes one PEM file per invocation. There is no keyring, no `known-signers` file, no TOFU, and no way to say "any of these three". |
| **`--signing-key`** | **Does not exist.** Signing always uses `.multiverso/keys/local.key`. |
| **CI key supply** | **Unsolved.** There is no documented way to give CI a signing key that is not "copy the private key onto the runner". |
| **Keyless / Sigstore** | Not in M1 — see [Status](../README.md#status). |

If a leaked key is your threat model, M1's answer today is: keep the key on one machine, publish the closure, and verify with `--key` everywhere else.

### 9. `mvo audit`

Replay the whole ledger and re-hash everything it points at:

```console
$ mvo audit --dir "$DEMO"
OK: 28 events, 2 decisions replayed, 44 CAS objects verified, 1 attestation signature(s) verified against the workspace key

$ mvo audit --dir "$DEMO" --json
{"schema":"multiverso.dev/audit-report/v1","events":28,"decisions":2,"admissions":1,
 "chain_ok":true,"replay_identical":true,
 "cas_checked":44,"cas_missing":[],"cas_corrupt":[],"cas_unreferenced":0,
 "attestations_checked":1,"attestations_verified":1,"attestations_verified_against":"workspace",
 "mismatches":[]}
```

Three independent properties:

- **`chain_ok`** recomputes every payload digest and chain hash over the event log. **Insertions and edits are detected** — any edited row breaks its own hash and every hash after it. **Tail truncation is detected only against a naive `delete`**: the check is that the last seq equals SQLite's `AUTOINCREMENT` high-water mark, and that mark lives in the ordinary, writable `sqlite_sequence` table. An operator with write access to the DB who deletes the tail *and* resets the mark gets a clean `OK`. An unwitnessed local chain therefore proves the log was not corrupted; it cannot prove it is *complete* against someone who can write to the file. Completeness needs an external witness — publishing the closure to a remote (`mvo publish`) is M1's only such anchor.
- **`replay_identical`** re-runs the shipped decision functions over the recorded inputs and compares type, subject, evidence and rationale **byte for byte** against what was recorded. A divergence prints the mismatch and exits 1.
- **`cas_checked` / `cas_missing` / `cas_corrupt`** is the integrity sweep: every CAS object the ledger references is re-read and re-hashed, from a **declared table** that is part of [the M1f contract](design/M1f-trust-boundary.md#the-referenced-set--normative) rather than a best-effort walk. Missing or corrupt is **exit 1**.

#### What `mvo audit` actually checks

`audit` reads the **ledger**, and then the closure the ledger points at. Both tamper shapes are failures now:

```console
$ printf 'TOTAL GARBAGE NOT EVEN JSON' > .multiverso/cas/sha256/d0/286dd4…   # the attestation bundle itself
$ mvo audit --dir "$DEMO"
CORRUPT: sha256:d0286dd4… cas: get sha256:d0286dd4…: content corrupted: bytes hash to sha256:3b4a6342…, referenced by attestation.recorded seq 27 (bundle)
mvo: audit: CAS sweep found 0 missing and 1 corrupt of 44 referenced objects
$ echo $?
1

$ rm .multiverso/cas/sha256/d0/286dd4…                                        # now delete it outright
$ mvo audit --dir "$DEMO"
MISSING: sha256:d0286dd4… referenced by attestation.recorded seq 27 (bundle)
mvo: audit: CAS sweep found 1 missing and 0 corrupt of 44 referenced objects
$ echo $?
1
```

Earlier versions of this page said audit did **not** do this, and that was true when it was written: a deleted attestation blob produced a clean `OK`. It does not any more, and the assertion is in `scripts/accept.sh` (step 9a) so it cannot quietly stop being true again.

**What the sweep proves, and what it does not.** It proves the **recorded closure is intact and self-consistent**. It cannot prove that something *was* recorded which never was: an adversary with write access to the ledger cannot forge the chain, but an operator who never ran an oracle has nothing for audit to miss. Objects in `cas/` that nothing references are **counted** (`cas_unreferenced`) and never failed — CAS legitimately holds more than the ledger names, from publication working sets and prior prunes.

Three flags matter here:

| flag | what it does |
|---|---|
| `--cas-sweep=false` | skip the sweep on a large ledger and a slow disk. The human line then says `(CAS sweep skipped)` and the JSON reports `"cas_checked":0` — **a skipped check never renders like a passed one** |
| `--require-decisions N` | exit 1 unless at least N decisions replayed. The CI knob |
| `--key PATH` | the trust anchor for the attestation signatures |

**`--key` is the difference between a check and a self-check.** Without it, audit verifies each DSSE bundle against **this workspace's own public key** — which a rogue clone reproduces exactly, since it signed the thing itself. That is why the human line names the anchor (`verified against the workspace key`) and the JSON reports `attestations_verified_against`. Pass `--key /path/to/trusted.pub` to make "verified" a statement about provenance.

Two further limits worth knowing before you wire `audit` into anything:

- **`audit` verifies THIS WORKSPACE's ledger. It is not an admission check.** It takes no commit argument, and it exits 0 on an empty workspace:

  ```console
  $ mvo audit --dir "$FRESH"
  OK: 2 events, 0 decisions replayed, 1 CAS objects verified (no races in this workspace — nothing was decided)
  $ echo $?
  0
  ```

  **Never wire that into a merge queue as the check that a commit is attested**: it would pass vacuously for anyone who deletes `.multiverso/` or never creates it, enforcing nothing while looking like it enforces everything. Use `--require-decisions` to make the vacuous case loud:

  ```console
  $ mvo audit --dir "$FRESH" --require-decisions 1
  SHORTFALL: 0 decisions replayed, --require-decisions 1
  mvo: audit: 0 decisions replayed, --require-decisions 1
  $ echo $?
  1
  ```

  Note that stdout carries **no `OK:` line** there. That is deliberate and tested: a CI check grepping for `OK:` must not read a failed audit as a pass.
- The verb that answers "is *this commit* attested" is still `mvo verify <commit>`. `mvo audit <commit>` does not exist in M1 — see [Status](../README.md#status).

### 10. Thirty seconds of paranoia

Attestation is only worth something if it breaks when you lie. Tamper with the admitted tree:

```console
$ cp -R "$DEMO" /tmp/mvo-demo-tampered
$ echo "# sneaky" >> /tmp/mvo-demo-tampered/stats.py
$ git -C /tmp/mvo-demo-tampered commit -q --amend --no-edit -a
$ mvo verify HEAD --dir /tmp/mvo-demo-tampered
mvo: verify: subject: subject tree git:8f53a125ef5c1ee11778e0c126dac3594018e79a, commit tree git:1ef1c11385792993e51d3fae2f8ac175b70f40fd
$ echo $?
1
```

The commit message, the trailers and the signature all survived the amend. The tree did not, and the tree is what the attestation is *about*.

That is the whole path: `init → intent → race → explain → admit → verify → audit`.

## On your own repo

### A Python repo

If your repo has a pytest suite, the path is identical — `mvo init` in the repo root, then intents and races as above. Two things to check first:

- **`python3 -m pytest --collect-only -q` must work from the repo root**, because that is what the collect oracle runs. If your suite needs `-c pytest.ini`, `--rootdir`, or a `PYTHONPATH`, author a policy whose oracle carries those `args` (below).
- **The default policy's `collected-not-below` gate has a denominator**: mvo measures your base tree's collected count at race start. If that measurement fails, the race aborts before recording anything — see [the pre-flight error](#no-python-tests-here).

Candidates come from an agent adapter for real work; the script adapter is for rehearsing the machinery against known patches.

### A non-Python repo

Use `mvo intent new --oracle-cmd CMD`. That synthesizes a `policy/v1` artifact whose single oracle is a `command` kind running your argv, records it in CAS, and pins it in the intent — so the command lives **inside** the pinned, attested policy, not on a race-time flag that two machines could spell differently.

A complete worked example with a bash "project":

```bash
export SHELLREPO=/tmp/mvo-shell
rm -rf "$SHELLREPO" && mkdir -p "$SHELLREPO/patches"
cat > "$SHELLREPO/calc.sh" <<'EOF'
#!/usr/bin/env bash
add() { echo $(( $1 - $2 )); }
EOF
cat > "$SHELLREPO/run-tests.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
source ./calc.sh
[ "$(add 2 3)" = "5" ] || { echo "FAIL: add 2 3 = $(add 2 3), want 5"; exit 1; }
[ "$(add 0 0)" = "0" ] || { echo "FAIL: add 0 0"; exit 1; }
echo "ok: 2 tests passed"
EOF
chmod +x "$SHELLREPO"/*.sh
# two candidates: one fixes the operator, one gets it wrong again
printf 'diff --git a/calc.sh b/calc.sh\n--- a/calc.sh\n+++ b/calc.sh\n@@ -1,2 +1,2 @@\n #!/usr/bin/env bash\n-add() { echo $(( $1 - $2 )); }\n+add() { echo $(( $1 + $2 )); }\n' > "$SHELLREPO/patches/fix.patch"
printf 'diff --git a/calc.sh b/calc.sh\n--- a/calc.sh\n+++ b/calc.sh\n@@ -1,2 +1,2 @@\n #!/usr/bin/env bash\n-add() { echo $(( $1 - $2 )); }\n+add() { echo $(( $1 * $2 )); }\n' > "$SHELLREPO/patches/wrong.patch"
git -C "$SHELLREPO" init -q -b main && git -C "$SHELLREPO" add -A && git -C "$SHELLREPO" commit -qm baseline
```

```console
$ mvo init --dir "$SHELLREPO"
initialized /tmp/mvo-shell/.multiverso (default policy mv0:f207c3fa…)

$ SH="$(mvo intent new --dir "$SHELLREPO" --title "fix add()" \
      --desc "add() subtracts instead of adding" \
      --oracle-cmd "bash run-tests.sh")"

$ mvo race "$SH" --dir "$SHELLREPO" --agent script --patches "$SHELLREPO/patches"
SELECT mv0:a88f12e1b0ed39e40513d644e30dd42e31e4827034668a71f68b5ee1706ec8dd (decision mv0:005c922e…, 2 worlds)

$ mvo explain "$SH" --dir "$SHELLREPO"
decision:  mv0:005c922ee955284cf1f12c3fbd7125faaaf078044933e6407f318c120bc0d5c8
type:      SELECT
intent:    mv0:1bc06fd587aa0a9abb1f0b24d0cfdaf399e366de84fbf4e013d734177eab061d  (fix add())
policy:    mv0:852a12775eabc6401b00690a0ffc0e1be5ad29fde4542f04689daecfe7b49b7a  (command, policy/v1)
winner:    mv0:a88f12e1b0ed39e40513d644e30dd42e31e4827034668a71f68b5ee1706ec8dd

gates (ordered, first failure stops the ladder):
  RANK  WORLD             ORD  status-pass@suite   RESULT
  1     mv0:a88f12e1…      1    pass                PASS
  2     mv0:c7996f11…      2    FAIL (status=fail)  FAIL

why mv0:a88f12e1… won  (ranking [gate_pass, wall_ms_asc, world_digest_asc]):
  vs mv0:c7996f11… (rank 2): decided at key 1 gate_pass (pass > fail)

evidence:  mv0:d97edfbe…  (command@mv0:c7996f11…, fail)
           mv0:ea738e9e…  (command@mv0:a88f12e1…, pass)
rationale: 1/2 worlds passed hard gates [status-pass@suite]; selected mv0:a88f12e1… (sole world passing all hard gates); ranking [gate_pass,wall_ms_asc,world_digest_asc]
freshness: FRESH (base 91b0e4ff9518 == main head)
```

`mvo admit` and `mvo verify` then work exactly as in §7–8. What you lose relative to the Python ladder is everything a command oracle cannot see: no `collected_delta`, so **a candidate that deletes tests is not caught**; no metrics, so ranking falls back to `wall_ms_asc`. An exit code is one bit of evidence. If your language has a machine-readable test report, wiring a new oracle kind is the natural next contribution.

### What a rejection looks like

The rest of this section needs a repo whose trunk still carries the bug (`$DEMO`'s does not — you admitted the fix). Make a second one the same way:

```bash
export GUARD=/tmp/mvo-guard
rm -rf "$GUARD" && mkdir -p "$GUARD"
cp -R testdata/toyrepo/. "$GUARD"/ && rm -rf "$GUARD"/__pycache__
git -C "$GUARD" init -q -b main
git -C "$GUARD" add -A
git -C "$GUARD" commit -qm "toyrepo baseline"
mvo init --dir "$GUARD"
```

Now race three candidates where two of them cheat — `patches-launder/` ships exactly that: one honest fix, one that deletes three tests, one that deletes the whole test file.

```console
$ G="$(mvo intent new --dir "$GUARD" --title "fix mean()" \
       --desc "three candidates, two of them cheat" --budget-candidates 3)"

$ mvo race "$G" --dir "$GUARD" --agent script --patches "$GUARD/patches-launder"
SELECT mv0:c50f7b96daab7d08f4648a26f993be62111c56915b931da9fcc6202bd5323218 (decision mv0:024a1375…, 3 worlds)

$ mvo explain "$G" --dir "$GUARD"
…
gates (ordered, first failure stops the ladder):
  RANK  WORLD          ORD  collect-nonempty@collect           collected-not-below@collect               status-pass@suite  RESULT
  1     mv0:c50f7b96…  1    pass (total=8)                     pass (delta=+0)                           pass               PASS
  2     mv0:1721a328…  2    pass (total=5)                     FAIL (collected_delta=-3 (tolerance -0))  n/a                FAIL
  3     mv0:37796ba0…  3    FAIL (collected_total=0 (exit 5))  n/a                                       n/a                FAIL
```

Read the `n/a` cells. **Neither cheating candidate's suite was ever run.** Both would have gone green — that is the entire point of deleting tests — and neither got the chance, because the collected-count gate is ordered first and the ladder short-circuits. `n/a` means not-evaluated, which is not the same as failed, and the table refuses to imply otherwise.

(Ranks 2 and 3 are both gate failures, so nothing in the ranking separates them except the terminal `world_digest_asc` key — which order they appear in varies between runs and carries no meaning.)

`exit 5` is pytest's "no tests collected". It is non-zero, so it is a failure, and mvo additionally records `collected_total=0` so the gate fails with a reason rather than for want of a metric.

### Author your own policy

> This subsection needs `coverage.py` importable by the `python3` that runs your suite — it builds a coverage-floor gate, and without the metric there is nothing for that gate to read.

Policies are files under `.multiverso/policies/<name>.json`, and the in-document `name` must equal the filename stem. Start from the default:

```bash
mvo policy show default --dir "$GUARD" --json > "$GUARD/.multiverso/policies/strict.json"
```

**A policy is content-addressed over the exact bytes of that file**, not over a re-canonicalised form — an artifact is addressed before it is parsed. So formatting is part of the identity, and reformatting alone mints a different policy: the `strict.json` produced below is `mv0:fe226d5d…`, but append a trailing newline to it and it becomes `mv0:253e5b4b…`; re-indent it two spaces and it becomes `mv0:26b83a0f…`; reorder its top-level keys and it becomes `mv0:ca95a54d…`. Same gates, same ranking, four different policies, all valid. That makes "open it in your editor" a poor instruction — most editors add a trailing newline on save — so edit it in place, keeping the compact, sorted, newline-free shape that `mvo policy show --json` emits:

```bash
python3 - "$GUARD/.multiverso/policies/strict.json" <<'PY'
import json, sys
path = sys.argv[1]
p = json.load(open(path))
p["name"] = "strict"
p["hard_gates"] += [
    {"basis": "construction", "gate": "no-failed-tests",   "oracle": "suite", "threshold": 0},
    {"basis": "construction", "gate": "coverage-at-least", "oracle": "suite", "threshold": 4000},
]
p["ranking"] = ["gate_pass", "coverage_desc", "tests_passed_desc", "wall_ms_asc"]
open(path, "w").write(json.dumps(p, sort_keys=True, separators=(",", ":")))
PY
```

That renames it to `strict`, keeps the three default gates and appends two — a `no-failed-tests` gate and a coverage floor of 40% (thresholds are integer basis points — no floats anywhere in canonical JSON) — and ranks by coverage before test count.

That `no-failed-tests` gate is worth more than it looks — though under the shipped default it is now the *third* thing standing between you and an exit-code forgery, not the first. A candidate that ships a `conftest.py` forcing `exitstatus = 0` is stopped at rung O-1 by `paths-unmodified@guard` (`harness_added`) before its Python ever runs, and if you deliberately relax that gate the run's metrics come from the control plane's evidence stream rather than from the process's exit code, so `status-pass` fails on its own (rule S2: exit 0 against a stream reporting failures is `status = error`). `no-failed-tests` is what closes the same vector for a policy that has relaxed *both* — it reads `tests_failed` directly:

```console
  RANK  WORLD          ORD  …  status-pass@suite  no-failed-tests@suite                  RESULT
  4     mv0:95576d0e…  2   …  pass               FAIL (tests_failed=2 tests_errored=0)  FAIL
```

It does **not** catch a candidate that forges the JUnit report itself, or one that skips tests rather than failing them — see [what the ladder catches](concepts.md#what-the-gate-ladder-catches-and-what-it-does-not).

**The escalation block is the other knob you will reach for**, and it is a sibling of `hard_gates`, not a gate:

```python
p["escalation"]["on_ranking_tie"] = True          # a real tie is reported, not resolved by noise
p["ranking"] = ["gate_pass", "tests_passed_desc", "coverage_desc"]   # note: no wall_ms_asc
```

Dropping `wall_ms_asc` is the point: while a *measured* quantity is in the ranking, two equally-correct candidates are separated by [scheduling noise rather than by evidence](#5-mvo-race), and `on_ranking_tie` never fires because the times are never exactly equal. The full four-key escalation vocabulary is `min_candidates_passing`, `on_ranking_tie`, `require_evidence`, `on_all_worlds_failed_machinery`; `testdata/toyrepo/policies/tie-escalate.json` in this clone is a complete worked file.

Validate before installing. Validation reports **every** problem at once, located by JSON path, with the closed vocabulary printed:

```console
$ mvo policy validate "$GUARD/.multiverso/policies/strict.json"
digest:    mv0:fe226d5dda210a9c5d0ebaef3d155846b80ef20281970b345ddf052a0ab49f82
schema:    multiverso.dev/policy/v1
name:      strict
gates (ordered):
  1. collect-nonempty@collect         oracle=collect basis>=construction threshold=0
  2. collected-not-below@collect      oracle=collect basis>=construction threshold=0
  3. status-pass@suite                oracle=suite basis>=construction threshold=0
  4. no-failed-tests@suite            oracle=suite basis>=construction threshold=0
  5. coverage-at-least@suite          oracle=suite basis>=construction threshold=4000
ranking:   gate_pass,coverage_desc,tests_passed_desc,wall_ms_asc,world_digest_asc
escalation: on_all_worlds_failed_machinery
oracles:
  collect      kind=pytest-collect config=mv0:0544af0d… argv=python3 -m pytest
  suite        kind=pytest-suite config=mv0:a6a5a160… argv=python3 -m pytest
required:  collect,suite
OK: policy valid
```

A typo is caught at load, not at 3 a.m.:

```console
$ mvo policy validate testdata/toyrepo/policies/bad-gate.json
mvo: policy validate: testdata/toyrepo/policies/bad-gate.json: hard_gates[1].gate: unknown gate "suite-passes" (known: collect-nonempty, collected-not-below, coverage-at-least, no-failed-tests, paths-unmodified, skips-not-above, status-pass)
```

Install it as the workspace default (or skip this and pin it per intent with `mvo intent new --policy strict`):

```console
$ mvo policy use strict --dir "$GUARD"
default policy mv0:fe226d5dda210a9c5d0ebaef3d155846b80ef20281970b345ddf052a0ab49f82 (strict, policy/v1)

$ mvo policy list --dir "$GUARD"
NAME     DIGEST             SCHEMA     GATES                                    RANKING                            STATE
default  mv0:f207c3fa…      policy/v1  paths-unmodified@guard,…                 gate_pass,tests_passed_desc        recorded
strict   mv0:fe226d5d…      policy/v1  …,no-failed-tests@suite,coverage-at-…    gate_pass,coverage_desc,…          recorded (default)
```

Editing a policy file mints a **new digest**. Intents pinned to the old one keep replaying against the old one, forever. That is the point: you cannot retroactively change what a past decision meant.

Now the coverage key actually decides a race between two candidates that both pass everything — `patches-tie/` holds two correct fixes, one of which adds an `import math` line and so covers one more statement:

```console
$ S="$(mvo intent new --dir "$GUARD" --title "two good candidates")"
$ mvo race "$S" --dir "$GUARD" --agent script --patches "$GUARD/patches-tie"
SELECT mv0:d71da43c204c9a924e3bb9c1ed038367367197353dbbd37a68f55f15ce730160 (decision mv0:6270f4d3…, 2 worlds)

$ mvo explain "$S" --dir "$GUARD"
…
  RANK  WORLD         ORD  collect-nonempty@collect  collected-not-below@collect  status-pass@suite  no-failed-tests@suite  coverage-at-least@suite  RESULT
  1     mv0:d71da43c…  2    pass (total=8)            pass (delta=+0)              pass               pass                   pass (bp=4665)           PASS
  2     mv0:0b9f1045…  1    pass (total=8)            pass (delta=+0)              pass               pass                   pass (bp=4651)           PASS

why mv0:d71da43c… won  (ranking [gate_pass, coverage_desc, tests_passed_desc, wall_ms_asc, world_digest_asc]):
  vs mv0:0b9f1045… (rank 2)
    1  gate_pass      pass  =  pass  tie
    2  coverage_desc  4665  >  4651  WINNER mv0:d71da43c…   ← decided here
```

**The vocabularies are closed.** Gates: `collect-nonempty`, `collected-not-below`, `coverage-at-least`, `no-failed-tests`, `paths-unmodified`, `skips-not-above`, `status-pass`. Ranking keys: `gate_pass`, `tests_passed_desc`, `coverage_desc`, `wall_ms_asc`, `cost_asc`, `patch_size_asc`, `world_digest_asc`. Oracle kinds: `command`, `pytest-collect`, `pytest-suite`, `tree-guard`. Freshness bases: `construction`, `dependency`, `probabilistic` (M1's oracles emit only `construction`). Escalation rules: `min_candidates_passing`, `on_ranking_tie`, `require_evidence`, `on_all_worlds_failed_machinery`, `on_invariant_violation`. Cross-oracle invariants: `collect-equals-suite-total`, `suite-parts-sum-to-total`. Path classes: `protected`, `harness`, with `protected_additions` ∈ {`allow`, `refuse`}. Evidence: `regime` ∈ {`auto`, `isolated`, `streamed`, `in-tree`}, `crosscheck` ∈ {`require`, `off`}, `plugin_autoload` ∈ {`off`, `on`}. There is no expression language and there will not be one.

### When mvo refuses to decide

A policy can declare that some situations are not decisions. `testdata/toyrepo/policies/tie-escalate.json` is the default's gates with `on_ranking_tie` on and `wall_ms_asc` dropped from the ranking, so the two `patches-tie` candidates tie on every key that remains:

```console
$ cp "$GUARD/policies/tie-escalate.json" "$GUARD/.multiverso/policies/"
$ mvo policy use tie-escalate --dir "$GUARD"
default policy mv0:85f9049ca864715a9c4e5859f8b22f00af0087b7afb040dd7365d23873175297 (tie-escalate, policy/v1)

$ E="$(mvo intent new --dir "$GUARD" --title "tie escalates")"
$ mvo race "$E" --dir "$GUARD" --agent script --patches "$GUARD/patches-tie"
ESCALATE (decision mv0:c3624690f0dba469c38bf61fbeaa8a83665a7328f06d062f3888ad5c2e2ba9b0, 2 worlds)
$ echo $?
0
```

```console
$ mvo explain "$E" --dir "$GUARD"
type:      ESCALATE
…
leader:    mv0:50989677…

ranking walk for leader mv0:50989677…  (no winner: escalated by on_ranking_tie; ranking [gate_pass, tests_passed_desc, world_digest_asc]):
  vs mv0:fb206788… (rank 2)
    1  gate_pass          pass           =  pass           tie
    2  tests_passed_desc  8              =  8              tie
    3  world_digest_asc   mv0:50989677…  <  mv0:fb206788…  tie-break only — not a decision

escalation: on_ranking_tie
            mv0:50989677… and mv0:fb206788… tie on every ranking key [gate_pass,tests_passed_desc]; only world_digest_asc would order them
```

Note the vocabulary: **leader**, not winner; **tie-break only — not a decision**. A coin flip on digest order is not evidence, and the renderer refuses to dress it up as one. An ESCALATE race exits 0 — a recorded decision is the product — but there is nothing to admit:

```console
$ mvo admit "$E" --dir "$GUARD"
mvo: admit: no SELECT decision for intent mv0:641da10d… (run "mvo race" first)
$ echo $?
1
```

The other escalation rule that fires in practice is `on_all_worlds_failed_machinery`: when no world produced conclusive evidence — every agent crashed, say — the race escalates rather than reporting a REJECT it did not earn. See [§5](#when-the-agent-fails).

### No Python tests here

Point the default (pytest) policy at a repo with no Python tests — that is, create an intent in `$SHELLREPO` *without* `--oracle-cmd`, so it pins the workspace default — and the race aborts at pre-flight, before recording anything:

```console
$ NP="$(mvo intent new --dir "$SHELLREPO" --title "no pytest here")"
$ mvo race "$NP" --dir "$SHELLREPO" --agent script --patches "$SHELLREPO/patches"
mvo: race: race: baseline: oracle "collect" on base tree git:2f5e89a5…: status=fail exit=5 collected_total=0; a collected-not-below gate has no honest denominator here
$ echo $?
1
```

A missing toolchain is machinery failure, not a failing candidate, and the ledger stays clean of race events. Fix: use `--oracle-cmd`, or author a policy whose oracles match your repo.

## Real agents (claude-code and codex)

> Everything up to here cost nothing. This section spends money.

### What actually happens

`mvo race --agent claude-code|codex` creates N worlds, and in each one spawns the vendor CLI headless with a rendered prompt, in the world's directory, with a filtered environment. When the process exits, the control plane captures `git diff` — **the agent's own description of what it did is never trusted, and never used**. The transcript goes to CAS verbatim as the world's `trace`; the diff becomes the `patch`.

The prompt is fixed and byte-exact:

```text
You are candidate {k} of {n}, working alone in an isolated git worktree.

# Task
{intent title + description, or --prompt / --prompt-file}

# Rules
- Modify files only inside the current directory.
- Do not commit, branch, push, or otherwise drive git; the control plane
  captures your working-tree changes when you exit.
- Do not read or modify .git or .multiverso.
- When the task is done, stop; do not wait for further input.
```

The rendered prompt **never contains** policy digests, gate names, ranking keys, oracle commands, sibling-world content or scheduler state. The generator is untrusted; anything it learns about the gate is a gaming surface.

The `candidate k of n` line is also the only prompt variation M1 ships — plus a model pool, below. Richer variation strategies are M2.

### Rehearse against the fixtures first

The repo ships fake agent CLIs (`testdata/fakeagent/{claude,codex}`) that emit the real event streams and edit files, without a network or a bill. **Verify they actually resolve before racing**, so a stripped executable bit cannot silently fall through to the real, money-spending CLI:

```console
$ for bin in claude codex; do
    PATH="$PWD/testdata/fakeagent:$PATH" command -v "$bin"
  done
/path/to/multiverso/testdata/fakeagent/claude
/path/to/multiverso/testdata/fakeagent/codex
```

Those two lines must be *your clone's* `testdata/fakeagent/` paths. Anything else — a path under `/usr/local/bin`, or no output at all — means the fixture did not win the `PATH` lookup, and the races below would reach for the real CLI.

Now race them, in a third copy of the toy repo:

```bash
export AGENTREPO=/tmp/mvo-agent
rm -rf "$AGENTREPO" && mkdir -p "$AGENTREPO"
cp -R testdata/toyrepo/. "$AGENTREPO"/ && rm -rf "$AGENTREPO"/__pycache__
git -C "$AGENTREPO" init -q -b main
git -C "$AGENTREPO" add -A
git -C "$AGENTREPO" commit -qm "toyrepo baseline"
mvo init --dir "$AGENTREPO"
A="$(mvo intent new --dir "$AGENTREPO" --title "fix mean()" \
      --desc "mean() divides by len-1" --budget-candidates 2 --budget-wall-ms 300000)"
```

This is a real `mvo race` against the `claude-code` adapter — only the binary it spawns is fake:

```console
$ PATH="$PWD/testdata/fakeagent:$PATH" FAKE_AGENT_MODE=happy \
  mvo race "$A" --dir "$AGENTREPO" --agent claude-code \
    --candidates 2 --model fake-model \
    --max-usd 0.25 --max-turns 8 --max-wall-ms 120000 \
    --agent-env FAKE_AGENT_MODE
SELECT mv0:fbff920f284b0a57e7a1a9c5d28fa249c060c92d4c98ba6952f77ee4966cba83 (decision mv0:45219dc3…, 2 worlds)

$ mvo worlds "$A" --dir "$AGENTREPO"
WORLD                                                                 OUTCOME    GATE  WALL_MS  USD_MICRO  TIER
mv0:75bfc0782714b9b291327c67308d40e95f59d328292ada05ec1496f0dc39f8fb  COMPLETED  pass  3902     4200       T0-worktree
mv0:fbff920f284b0a57e7a1a9c5d28fa249c060c92d4c98ba6952f77ee4966cba83  COMPLETED  pass  3856     4200       T0-worktree
freshness: FRESH (base 642c967ecdd5 == main head)
```

Both agents fixed the bug, so the gates cannot separate them and the ranking does:

```console
$ mvo explain "$A" --dir "$AGENTREPO"
…
why mv0:fbff920f… won  (ranking [gate_pass, tests_passed_desc, wall_ms_asc, world_digest_asc]):
  vs mv0:75bfc078… (rank 2)
    1  gate_pass          pass  =  pass  tie
    2  tests_passed_desc  8     =  8     tie
    3  wall_ms_asc        3856  <  3902  WINNER mv0:fbff920f…   ← decided here
```

`USD_MICRO 4200` is `$0.0042` — what the fixture *reported*. Every world records `cost.source`, and for both real CLIs it is `client-estimate`: the number the tool said about itself, labelled as such. mvo does not cross-check it against a provider usage API.

### Going live

Same command, real binary on `PATH`. **This is a template, not a step in the walkthrough** — filling in the two variables and running it spawns four real agent sessions and bills you for them:

```bash
mvo race "$YOUR_INTENT" --dir "$YOUR_REPO" --agent claude-code \
  --candidates 4 \
  --model claude-opus-5,claude-sonnet-5 \
  --max-usd 0.50 --max-turns 20 --max-wall-ms 900000 \
  --agent-env ANTHROPIC_API_KEY
```

`--dir` is spelled out on purpose: it defaults to `.`, and `.` is the multiverso clone if you are still standing where [Build](#build) left you.

**Credentials.** The child environment is an allowlist, not your shell: `PATH`, `HOME`, `TMPDIR`, `USER`, `LANG`, `LC_ALL`, plus whatever you name in `--agent-env`. No API key reaches a world unless you allowlist it by name. (`HOME` is on the base list, so a CLI that reads stored credentials from a dotfile will find them; a key in an env var will not arrive unless you say so.)

**Model pool.** `--model A,B` assigns models round-robin across candidates — candidate 1 gets A, candidate 2 gets B, candidate 3 gets A. mvo does not validate the strings: each is passed straight to the CLI's own `--model` (claude-code) or `-m` (codex), so whatever IDs your installed CLI accepts are the IDs to use here. This is the diversity mechanism, and it is the least-tested part of M1 (see [Status](../README.md#status)).

### What the budget flags actually do

Be precise about this, because the names promise more than the mechanism delivers:

| Flag | Enforced by | Reality |
|---|---|---|
| `--max-wall-ms` | **mvo's watchdog** | Real. SIGTERM to the process group, then SIGKILL. Under T1, `docker kill` tears down the whole container. This is the only stop that mvo itself enforces inside a world. |
| `--max-turns` | the CLI's own `--max-turns` | claude-code only. Recorded in the ledger for codex, unenforceable there. |
| `--max-usd` | the CLI's own `--max-budget-usd` | claude-code only. **For codex there is no dollar cap at all** — a codex world can spend up to the wall clock. |
| `--candidates` | mvo | Capped by the intent's `budget.max_candidates`; exceeding it is a usage error. |
| intent `--budget-wall-ms` | mvo | Bounds the whole race. On expiry, worlds are killed and the race records an ordinary REJECT — **it does not say "budget exhausted"**. |

There is no aggregate dollar ceiling across worlds. Nothing sums the per-world cost against a limit. If you want a hard cap on a race, the honest lever today is `--candidates` × `--max-usd` on claude-code, and the wall clock everywhere else.

### When the agent fails

Agent failure is evidence, not an error. With the fixture forced into a provider error:

```console
$ AF="$(mvo intent new --dir "$AGENTREPO" --title "agent fails" --desc "provider error path")"
$ PATH="$PWD/testdata/fakeagent:$PATH" FAKE_AGENT_MODE=provider-error \
  mvo race "$AF" --dir "$AGENTREPO" --agent claude-code --candidates 2 \
    --model fake-model --max-wall-ms 60000 --agent-env FAKE_AGENT_MODE
ESCALATE (decision mv0:a245cdde…, 2 worlds)

$ mvo worlds "$AF" --dir "$AGENTREPO"
WORLD          OUTCOME         GATE                      WALL_MS  USD_MICRO  TIER
mv0:2ccfbdc9…  PROVIDER_ERROR  collect-nonempty@collect  -        0          T0-worktree
mv0:6af386d3…  PROVIDER_ERROR  collect-nonempty@collect  -        0          T0-worktree

$ mvo explain "$AF" --dir "$AGENTREPO" | tail -6
escalation: on_all_worlds_failed_machinery
            no world produced conclusive evidence (mv0:2ccfbdc9… outcome=PROVIDER_ERROR, mv0:6af386d3… outcome=PROVIDER_ERROR)

evidence:  (none)
rationale: escalated by policy rule on_all_worlds_failed_machinery: no world produced conclusive evidence …
freshness: FRESH (base 642c967ecdd5 == main head)
```

The six outcomes are `COMPLETED`, `BUDGET_EXCEEDED`, `INTERRUPTED`, `CONFIG_ERROR`, `PROVIDER_ERROR`, `CRASH`. A missing CLI is caught at pre-flight, before any world is created, rather than burning N `CONFIG_ERROR` worlds discovering it:

```console
$ Y="$(mvo intent new --dir "$AGENTREPO" --title "preflight probe")"
$ PATH=/usr/bin:/bin "$(command -v mvo)" race "$Y" --dir "$AGENTREPO" --agent claude-code --candidates 1 --max-wall-ms 1000
mvo: race: adapter claude-code: binary "claude" not found in PATH: exec: "claude": executable file not found in $PATH
```

(The `$(command -v mvo)` is not decoration. `PATH=/usr/bin:/bin` is what hides `claude` from the child — and it would hide `mvo` from your shell too, so the binary has to be named by the absolute path the *outer* `PATH` still resolves. Writing `PATH=/usr/bin:/bin mvo race …` just gets you `command not found`.)

## T1 containers

T0 (the default) is a `git worktree` on your host: fast, uncapped, no isolation beyond a separate directory. T1 puts every untrusted process — agent *and* oracle — inside a docker container.

### Build an image

The image needs whatever your oracles run, plus the agent CLI if you are using one. The shipped fixture image is the reference:

```dockerfile
FROM python:3.12-alpine
RUN apk add --no-cache bash && pip install --no-cache-dir pytest==8.*
COPY fakeagent/claude fakeagent/codex /usr/local/bin/
```

```bash
docker build -f testdata/t1image/Dockerfile -t multiverso-t1-fixture:v1 testdata
```

### Race in it

Once more with a fresh copy of the toy repo. **Pass `--dir` as an absolute path** — see the gotcha below:

```bash
export T1REPO=/tmp/mvo-t1
rm -rf "$T1REPO" && mkdir -p "$T1REPO"
cp -R testdata/toyrepo/. "$T1REPO"/ && rm -rf "$T1REPO"/__pycache__
git -C "$T1REPO" init -q -b main
git -C "$T1REPO" add -A
git -C "$T1REPO" commit -qm "toyrepo baseline"
mvo init --dir "$T1REPO"
T="$(mvo intent new --dir "$T1REPO" --title "fix mean() under T1" --desc "container race")"
```

```console
$ mvo race "$T" --dir "$T1REPO" --agent script --patches "$T1REPO/patches" \
    --exec T1 --exec-image multiverso-t1-fixture:v1 \
    --memory-mb 512 --cpus 1 --pids 256
SELECT mv0:1d0de113fc1131ae5027a0432f8e1f3c96cb583e3fe64dbcacc558a2929af1b3 (decision mv0:43cf4824…, 2 worlds)

real	0m2.590s

$ mvo worlds "$T" --dir "$T1REPO"
WORLD          OUTCOME    GATE               WALL_MS  USD_MICRO  TIER
mv0:1d0de113…  COMPLETED  pass               653      0          T1-container
mv0:9aec8eec…  COMPLETED  status-pass@suite  628      0          T1-container
```

Before the race starts, mvo resolves the image tag to a **digest** and runs everything against that digest — so a tag moved mid-race cannot change what ran. With an agent adapter, it also probes that the CLI actually exists *in the image* (`docker run --entrypoint <bin> … --version`), because discovering a missing binary N worlds later is expensive.

### The cage is recorded, so it is auditable

Every T1 receipt carries what actually confined it. M1 ships no `mvo receipt` or `mvo evidence` verb, and `mvo explain --json` gives you a receipt's digest, oracle, world, status and metrics — not its execution block. To read the whole thing, go to the store: a receipt is a CAS blob, and `mv0:<hex>` maps to `.multiverso/cas/sha256/<first two hex chars>/<the remaining 62>`.

```bash
R="$(mvo explain "$T" --dir "$T1REPO" --json \
     | python3 -c 'import json,sys; print(next(e["digest"] for e in json.load(sys.stdin)["evidence"] if e["oracle"]=="pytest-suite")[4:])')"
python3 -m json.tool "$T1REPO/.multiverso/cas/sha256/${R:0:2}/${R:2}"
```

Abridged to the two fields that matter here (`user` is the uid:gid mvo ran the container as — yours, not root's):

```json
"execution": {
  "argv": ["python3","-m","pytest","--junit-xml=.mvo-oracle/pytest-suite/junit.xml","-p","no:cacheprovider"],
  "exit_code": 0, "duration_ms": 223,
  "isolation_tier": "T1-container",
  "isolation_caps": {
    "cap_drop": "ALL", "cpu_milli": 1000, "memory_bytes": 536870912,
    "network": "none", "pids_limit": 256, "read_only_root": true, "user": "501:20"
  }
},
"result": {
  "metrics": {"duration_ms":14,"tests_errored":0,"tests_failed":0,
              "tests_passed":8,"tests_skipped":0,"tests_total":8},
  "status": "pass", "tools": {"pytest": "8.4.2"}
}
```

Defaults: `--network none`, `--cap-drop ALL`, `--security-opt no-new-privileges`, read-only root with only `/tmp` and the bind-mounted `/work` writable, non-root user. `--allow-network` opts out of the network isolation explicitly, and the receipt records `"network":"default"` when you do.

Note what is **not** in that receipt: `coverage_bp`. The fixture image has no `coverage.py`, so the metric is absent rather than invented. Add `coverage` to your image if you want a coverage gate to work under T1.

The image digest also rides into the world's `env` manifest, so a T1 world and a T0 world of the same tree have **different env digests** — and a receipt from one is inadmissible as evidence for the other.

### Two things to know

- **`--dir` must be absolute under T1** (or run mvo from inside the repo). Docker resolves bind-mount sources itself and rejects a bare relative path as a volume name — illustrative, `…` standing in for the flags above:

  ```console
  $ mvo race "$T" --dir mvo-t1 … --exec T1 --exec-image multiverso-t1-fixture:v1
  mvo: race: race: base world: backend: open T1 world: dockerx: docker run … -v mvo-t1/.multiverso/worlds/race-…/base:/work …: exit status 125: docker: Error response from daemon: create mvo-t1/.multiverso/…: "…" includes invalid characters for a local volume name, only "[a-zA-Z0-9][a-zA-Z0-9_.-]" are allowed. If you intended to pass a host directory, use absolute path
  ```

- **Admission landing gates always run host-T0**, whatever tier the race used. An intent raced under T1 is admitted on evidence produced outside that isolation. This is a known M1 limitation, not a subtlety — see [Status](../README.md#status).

## Sharing a race

`mvo publish` puts a race's full closure on any git remote under `refs/multiverso/intent/<short>/`, outside `refs/heads`, so it never looks like a branch. A bare repo on disk stands in for a forge here:

```bash
export ORIGIN=/tmp/mvo-origin.git
rm -rf "$ORIGIN" && git init -q --bare "$ORIGIN"
git -C "$ORIGIN" symbolic-ref HEAD refs/heads/main
git -C "$DEMO" remote add origin "$ORIGIN" 2>/dev/null || git -C "$DEMO" remote set-url origin "$ORIGIN"
git -C "$DEMO" push -q origin main
```

```console
$ mvo publish "$INTENT" --dir "$DEMO"
published refs/multiverso/intent/223b31124150 to origin (2 pushed, 0 up-to-date, 0 removed)
  refs/multiverso/intent/223b31124150/cand/1 02f99b7e286edba38dcf99c67c00d8c1594c5cf0
  refs/multiverso/intent/223b31124150/evidence 200a0044c112bbe0d4eedb08819fc40fda6984a7
```

Candidate refs hold the candidate trees; the evidence ref holds the intent, policy, worlds, DSSE-signed receipts and decisions, and — when admitted — the attestation bundle. Prompts and transcripts stay in the private CAS and are never published. `--include-rejected` publishes losers' candidate refs too (losers' *evidence* always ships).

`223b31124150` there is the **intent short**: the first twelve hex characters after `mv0:`. It names the ref namespace, and it is the handle the other end fetches by — so derive it from your own digest rather than copying the one above:

```bash
SHORT="${INTENT#mv0:}" && SHORT="${SHORT:0:12}" && echo "$SHORT"
```

On the other end, `mvo fetch-race` verifies everything offline in any clone, with no workspace and no ledger, holding nothing but a public key:

```console
$ rm -rf /tmp/mvo-clone && git clone -q "$ORIGIN" /tmp/mvo-clone
$ mvo fetch-race "$SHORT" --dir /tmp/mvo-clone --key "$DEMO/.multiverso/keys/local.pub"
intent:    mv0:223b311241502607f2114f0c8c6859a84ff5c328292fa849aebff67db1e9374c (fix mean())
decision:  mv0:03a60e9594b34ab6646cb7c1d79f22e501d4ef8732d567e1524fe51f052ad73a SELECT
winner:    mv0:18ba934906021f6660486bf0e41f966668a7615021707b83c8263349683cd41a
admitted:  yes
freshness: STALE (main advanced past base 4bd0d29c3feb)
ORDINAL  WORLD          OUTCOME    GATE               SIGNED  REF
1        mv0:18ba9349…  COMPLETED  pass               5       cand/1
-        mv0:ad21cf57…  COMPLETED  status-pass@suite  2       -
OK: race integrity verified (16 items, 2 refs)
    integrity = content addresses, signatures against the key you passed, and decision replay.
    correctness: NOT asserted. Read the gate table above and `mvo explain` for what the oracles measured.
```

Sixteen items authenticated: content addresses recomputed, signatures checked against the one key you passed, the cross-reference graph closed, candidate commits pinned to signed-cited world trees, and **every decision replayed through the shipped decision function**. A wrong-winner decision signed with the right key still fails. Failures are collected rather than fail-fast, each printed as `mvo: fetch-race: <path>: <detail>`.

**Read the second and third lines literally.** "Verified" here means the bytes are self-consistent and signed by the key you supplied — it is a statement about *integrity*, not about whether the winning candidate is any good. This exact `OK` line prints over a race whose winner [forged its own JUnit report](concepts.md#what-the-gate-ladder-catches-and-what-it-does-not). The gate table above it is where the evidence is.

`freshness: STALE` is honest and expected here: the admitted commit moved `main` past the intent's base.

Retention:

```console
$ mvo prune "$INTENT" --dir "$DEMO" --remote origin
pruned refs/multiverso/intent/223b31124150: 0 local, 0 remote deleted, 2 kept
```

Losers are always prunable; an admitted intent keeps its winner and evidence unless you pass `--keep-admitted=false`. `--remote` has no default — deleting remote refs is always explicit. `--older-than DUR` refuses to prune anything published more recently. CAS and the ledger are never touched.

## Protected trunks and merge queues

Read this **before** you plan an integration, not after. It is the constraint most likely to make Multiverso a poor fit for your pipeline, and it follows from one fact:

> **An attestation survives a fast-forward of the exact admitted commit, and nothing else.**

The attestation's subject is the landing *tree*, and the trailer lives in the commit message. Any operation that rewrites either — which is what a merge queue does for a living — breaks verification. Verified, all three:

```console
# 1. Fast-forward of the exact admitted commit — the supported topology.
$ git checkout -q integration && git merge --ff-only main
$ mvo verify HEAD
OK: attestation verified (7 checks)

# 2. `git rebase main` — what a merge queue does to keep history linear.
$ git rebase -q main && mvo verify HEAD
mvo: verify: subject: subject tree git:8f53a125ef5c1ee11778e0c126dac3594018e79a, commit tree git:83c6473dc4d4c89f3235fb46d73bdc6e979b918e
$ echo $?
1

# 3. Squash merge — GitHub's "Squash and merge", and most queues' default.
$ git merge --squash feat && git commit -qm "squashed: fix mean()"
$ mvo verify HEAD
mvo: verify: bundle_digest: commit cc64788e49045843c199fc20b3993244ad20e6e3 has no Multiverso-Attestation trailer
$ echo $?
1
```

Case 2 fails because a rebase re-parents the commit and produces a different tree; case 3 because a squash mints a brand-new commit whose message never had the trailer. Neither is recoverable after the fact — you would have to re-run `mvo admit` against the new base.

### The two supported topologies

1. **Fast-forward-only trunk.** Protect `main` with linear history and *no* squash/rebase on merge, and let `mvo admit` produce the commit that gets fast-forwarded. This is the only configuration where the trailer on trunk verifies.
2. **An unprotected integration branch**, with `mvo admit` landing there and a bot identity holding ruleset bypass to move `main`. `mvo admit` updates a **local** branch ref by compare-and-swap; it does not push, so the identity that pushes needs whatever bypass your protection rules require.

If your queue rebases or squashes — GitHub merge queue, Bors, most `merge_group` setups — **Multiverso's attestation does not survive it today**, and no flag changes that in M1. This is a real gap, itemized in [Status](../README.md#status) under FI-1.

### CI snippet

Verify the attestation on the commit that actually landed, in a job that runs *after* the merge:

```yaml
# .github/workflows/verify-attestation.yml
on:
  push:
    branches: [main]

jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0                 # verify needs the parent commit
      - name: Fetch the evidence closure
        run: git fetch origin '+refs/multiverso/*:refs/multiverso/*'
      - uses: actions/setup-go@v5
        with: {go-version: '1.25'}
      - run: go install github.com/coagente/multiverso/cmd/mvo@latest
      - name: Verify
        run: mvo verify "$GITHUB_SHA" --key .github/trusted-signer.pub
```

Two things that snippet does deliberately. It passes **`--key`** with a public key committed to the repo — without it, verification is [a tautology](#--key-is-the-trust-anchor-not-a-convenience) against whatever key the runner happens to find. And it verifies **`mvo verify <commit>`**, not `mvo audit`: `audit` checks a local workspace's ledger, takes no commit, and [exits 0 on an empty workspace](#what-mvo-audit-actually-checks), so it enforces nothing as a required check.

## Reference

### Verbs

| Verb | Does |
|---|---|
| `mvo init [--keys]` | create `.multiverso/`; `--keys` retrofits a keypair |
| `mvo policy list \| show \| validate \| use` | inspect and install policies |
| `mvo intent new --title T […]` | record an intent pinned to `HEAD` and a policy; prints its digest |
| `mvo race <intent> […]` | generate N worlds, run the ladder, record a decision |
| `mvo worlds <intent>` | the scoreboard |
| `mvo explain <intent> [--json] [--diffs N]` | the full argument, derived at render time |
| `mvo admit <intent>` | re-gate on the landing tree, land it, sign an attestation |
| `mvo verify <commit> [--key PUB] [--json]` | seven offline checks. **`--key` is the trust anchor** — without it the default key makes verification a tautology inside the workspace that produced the attestation |
| `mvo audit [--json] [--key PUB] [--require-decisions N] [--cas-sweep=BOOL]` | hash chain + byte-exact replay of every decision **in this workspace's ledger**, plus a re-hash of every CAS object the ledger references. Takes no commit and exits 0 on an empty workspace — [never a merge-queue check](#what-mvo-audit-actually-checks). Without `--key` the signature check is a **self-check** against the workspace's own key, and the output says so |
| `mvo guard --base <rev> [--tree <rev>] [--policy P] [--json]` | the adoption wedge: compare two trees under a policy's path sets, exit 0 clean / 1 violating. No ledger writes, no race. `mvo guard --base HEAD~50` answers "would this gate have blocked my last fifty commits" |
| `mvo version` | build revision of this binary |
| `mvo publish <intent> [--remote R]` | push the signed closure to a remote namespace |
| `mvo fetch-race <short> [--key PUB]` | verify a published race in any clone |
| `mvo prune <intent> […]` | apply retention to published refs |

Every verb accepts `--dir <repo>` (default `.`). `mvo help` prints the full flag list.

### Exit codes

`0` success — including an `ESCALATE` race, whose product is a recorded decision. `1` failure: a gate failed, verification failed, a replay diverged, machinery broke. `2` usage error.

### Workspace layout

```text
.multiverso/
  ledger.db          hash-chained append-only SQLite event log — the source of truth
  ledger.db-wal      SQLite write-ahead log and shared-memory index; they appear on
  ledger.db-shm        first write and are not part of the workspace's content
  cas/sha256/ab/cd…  content-addressed blobs (patches, transcripts, raw oracle artifacts)
  config.json        {schema, default_policy}
  policies/*.json    authored policy files
  keys/              ed25519 keypair (0700); local.pub is what verifiers need
  worlds/            transient worktrees; removed after each race unless --keep-worlds
                       (the directory itself stays, usually empty)
  admit/             transient admission worktree, same deal
```

An artifact digest is its CAS path: `mv0:abcd…` (or `sha256:abcd…`) lives at `cas/sha256/ab/cd…`. That is how you read a receipt or an attestation bundle by hand — see [T1's receipt](#the-cage-is-recorded-so-it-is-auditable).

The ledger and CAS are append-only and are never pruned. `mvo prune` touches published git refs only.

Event types you will see in `ledger.db`: `policy.created`, `key.generated`, `intent.created`, `race.started`, `baseline.recorded`, `agent.started`, `agent.finished`, `world.created`, `receipt.recorded`, `decision.recorded`, `race.finished`, `admission.started`, `attestation.recorded`, `admission.finished`, `publish.started`, `publish.finished`, `prune.executed`.

### Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `workspace: open …: no such file` | no `.multiverso/` | `mvo init --dir <repo>` |
| `gitx: git rev-parse HEAD …: ambiguous argument 'HEAD'` | repo has no commits, or is not a git repo | commit something first |
| `baseline: oracle "collect" … exit=5 collected_total=0` | default policy needs collectable pytest tests | use `--oracle-cmd`, or author a policy for your repo |
| `--oracle-cmd is required with policy … (policy/v0)` | intent pinned a legacy v0 policy | pass `--oracle-cmd`, or pin a v1 policy |
| `--oracle-cmd is not permitted with policy … (policy/v1)` | a v1 policy names its own oracles | drop the flag; edit the policy instead |
| `N candidates exceed intent budget max_candidates=M` | race asked for more than the intent allows; the default is **2**, so three patches trip it | budgets are pinned at creation: `mvo intent new --budget-candidates N` on a **new** intent |
| `pre-flight probe did not complete: budget exhausted` | `--budget-wall-ms` is too small for the probe to run; says nothing about pytest | raise `--budget-wall-ms` on a new intent, or drop it |
| `is not a git repository … mvo requires a git worktree` | `mvo init --dir` pointed outside a worktree (often a typo) | `git init` there, or fix the path — nothing was created |
| `adapter claude-code: binary "claude" not found in PATH: …` | agent CLI missing (pre-flight; nothing was spawned) | install it, or use `--agent script` |
| `exec T1: docker daemon unavailable` | Docker not running | start it, or drop `--exec T1` |
| `includes invalid characters for a local volume name` | relative `--dir` under T1 | use an absolute path |
| `note: trunk advanced to <sha> but your working tree was not clean` | tree not clean at admit time — **an untracked file is enough** | the commit **did** land and exit was 0; your index holds a **staged reversion** of it, so `git reset --hard` (or `git stash` first), and do **not** reflexively `git commit` |
| `ARTIFACT MISSING OR ALTERED IN CAS` from `explain --diffs` | the patch blob is gone or its bytes changed | the evidence store is damaged; `mvo verify <commit>` — this is not an empty patch |
| a world with `OUTCOME CONFIG_ERROR` and no explanation | one label over several causes: an empty patch file, a patch that did not apply, a tracked `.multiverso/` | the World schema has no reason field in M1, but the adapter writes one to the world's captured **stderr** — read it [from CAS](#the-cage-is-recorded-so-it-is-auditable) via the `stderr` key on that world's `agent.finished` ledger event |
| `admit: no SELECT decision for intent …` | the race ended in ESCALATE or REJECT | read `mvo explain`; there is nothing to admit |
| `admit: intent already admitted (commit …)` | one admission per intent | create a new intent |
| `verify: subject: subject tree …, commit tree …` | the commit's tree is not the attested one | that is the check working |
| `policy use: … is policy/v0, which cannot name its oracles` | v0 cannot be a workspace default | author a v1 file; `mvo policy show default --json` is a good start |

### Do not do this

- **Do not point `--agent claude-code|codex` at anything you are not prepared to pay for.** There is no dry-run and no aggregate spend cap. Rehearse with `testdata/fakeagent/` on `PATH`.
- **Do not treat a green race as a merge.** `mvo admit` re-gates on the landing tree, and that is the gate that counts.
- **Do not hand-edit `ledger.db` or `cas/`.** An edited ledger row is caught by `mvo audit`. An edited or deleted **CAS blob is not** — see [what `audit` actually checks](#what-mvo-audit-actually-checks). Use `mvo verify <commit>` for the blobs an attestation depends on.

## Next

- [`concepts.md`](concepts.md) — what these objects are and why they are shaped this way.
- [Status](../README.md#status) — exactly what M1 does and does not do, itemized.
- [`design/`](design/) — per-milestone design docs, each with its resolved decisions and its NOT-list.
