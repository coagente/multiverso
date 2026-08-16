# Build Log

> Public journal of building Multiverso. Newest first. See [PRD.md](PRD.md) for the plan; milestones M0–M4.

## 2026-08-15 — M2a: an oracle menu worth scheduling over

**M2's thesis is an adaptive scheduler, and before this block there was
nothing to schedule.** Three rungs — `tree-guard`, `pytest-collect`,
`pytest-suite` — about four hundred milliseconds and two `git ls-tree` walks.
A scheduler is a function from a budget to a purchase; with one plausible
purchase it is a constant. M2a builds the menu: four new oracle kinds
spanning three orders of magnitude of cost, each declaring what it costs, what
it scales by, what signal it reads, and who authored its inputs.

The centrepiece is the **cross-candidate differential** (EP-4). Research ch. 8
puts candidate-vs-candidate behavioural diffing at *function* scale in the
literature and finds **nobody doing it at repository scale on real patches**.
We already pay for N isolated worlds of one intent; comparing them costs one
interpreter start per world and a hash table. Two candidates implement
`clamp` correctly for all eight tests, tie on every ranking key that measures
anything, and diverge on `clamp(nan, 0, 10)`: `nan` versus `0`. Under M1f that
race ESCALATEd on `on_ranking_tie` and told the maintainer that two digests
tied. Under M2a it ESCALATEs on `on_behavioral_split` and hands them the input
and both answers. That difference is the block.

**The measured costs, because the whole point is that a scheduler needs
numbers.** Anything that imports pytest costs ~400 ms fixed on this tree,
which dwarfs the tests: `--collect-only` over the 8-test toyrepo is
0.39–0.88 s real while collection reports 0.00 s. `corpus-observe` — a bare
interpreter, no pytest, no conftest, no plugin system — is **~25 ms**, and 500
trivial cases add 0.12 ms. Cheap *and* the smallest attack surface on the
ladder is an unusual combination and the scheduler should exploit it. The
largest single lever turned out to be a security control: sealing pytest's
entry-point autoload (M1f's `evidence.plugin_autoload`) takes the same run
from 0.45 s to 0.10 s. `mvo oracles` fits `wall_ms ≈ fixed + per_unit × units`
per kind from this workspace's own receipts and prints "no local measurement
(n=…)" below three samples, because a two-point fit rendered as a fact is the
same over-claim as a skipped gate rendered green.

**What the red team broke, in the order it hurt.**

*The corpus was one file, and a world could poison the one its neighbour would
read.* Phase 0 wrote it beside every worktree, owned by the candidate's own
uid, and the runner re-read it from disk on every replay — so a poisoner
changed the NEXT world's inputs and never its own observation. Both payoffs
ran. Declare a case id nobody pinned, and the honest sibling's stream
faithfully reports it, its metrics go absent, and it is convicted of tampering
it did not do while the poisoner is SELECTed, ADMITted, `mvo verify HEAD` OK.
Or preserve every id, rewrite one argument so both worlds agree, and the split
simply never fires — reproduced 3/3 under `--parallel 2`. Neither touched a
protected or harness path. Three of our sentences were false at once, including
`A WORLD CAN ONLY REMOVE ITSELF`, written twice in the source as a flat claim.

*And the field that would have caught it was dead.* `mvo_corpus.py` emitted
`"corpus": corpus.get("digest","")` over an object with no `digest` member —
the digest is the content address of the canonical bytes and is never inside
them — so every stream this tree could produce recorded `""`, and nothing on
the Go side read it. The runner now hashes the bytes it actually loaded, the
pinned digest travels on argv, and an observation of a corpus nobody pinned is
unusable. Beside it: one corpus copy per world, written only after the agents
are gone, re-digested by the control plane before and after every replay, with
a mismatch aborting the **race** as machinery rather than failing a world —
because a world handed an altered file is a victim, not a suspect.

*Under T1 we mounted the corpus into every candidate world for the whole of
generation.* `docker inspect` of the candidate keepers showed
`<raceDir>/corpus -> /mvo/corpus ro=true` mid-race, which is the exact
opposite of what decision 13, `backend.OpenOpts`'s own comment and our verdict
on corpus vector 19 all say. The keeper is opened before the agent starts
inside it, and phase 0 has already written the corpus by then. Each world now
gets its own corpus directory, created **empty** at open time and filled only
after phase A joins.

*"A world can only remove itself" failed twice more, and neither needed a
forgery.* A stream with a header, a `session_start`, a **subset** of the
declared ids and a `session_finish` breaks no framing rule — so it parsed as
usable while the oracle's own status was `fail`, entered the cohort anyway,
and, because the comparison denominator is an intersection over every member,
deleted the distinguishing case for every honest sibling. And a world that
observed *every* case and answered `opaque` on all of them passed its own gate
while collapsing the denominator to zero for the entire race, silently,
because nothing gates or escalates on `diff_cases_compared`. The cohort now
requires the receipt's `pass`; a member that compares nothing is excluded and
the exclusion recorded with every member's contribution beside it.

*A mutant that hangs was a free pass.* A per-mutant timeout is correctly
excluded from `mutation_score_bp`'s denominator — and it was also excluded
from the only mutation gate M2a ships, so a mutant the suite provably failed
to kill cost the candidate nothing. Two killed, one 90-second sleep:
`mutants_survived=0`, `mutation_score_bp=10000`, gate PASS, race SELECT, and
the report printing `survivors: 0` beside it. The gate now reads
`survived + timeout`; the score still does not, because what a ratio means and
what the tests did are different questions.

*The mutation tool resolved through the candidate's tree.* `python -m
cosmic_ray.cli` runs with cwd = the worktree, so `sys.path[0]` is the
candidate's; a six-line `cosmic_ray/cli.py` whose `dump` prints nothing gave
`mutants_candidates=0` beside our own `mutation_lines_targeted=18`, and the
gate passed vacuously. No glob was ever going to close it — the shadow is an
ordinary directory named after a tool. Enumeration now runs under
`PYTHONSAFEPATH=1`, and zero mutants over a non-empty target set is machinery
with a named reason, not a pass.

*And a compiled freeze had no gate to enforce it.* A policy-declared corpus
file or property module joins `paths.harness` — that is decision 14's entire
defence for the property module — and nothing required a `paths-unmodified`
gate to exist. The shipped `differential.json` was exactly that shape:
`mvo policy validate` printed `paths (frozen against the candidate)` over a
freeze the policy could not keep, and a candidate that rewrote the corpus
passed every hard gate. Validation rule 24 is the converse of rule 12, both
fixtures carry the guard, and `mvo policy show` now prints
`paths (declared, NOT ENFORCED)` for a path block no gate reads.

**What a differential result does and does not establish.** A partition of the
cohort into two or more behaviour classes on a shared, control-plane-authored
corpus proves exactly one thing: **the intent as specified, plus the
repository's own tests, do not determine behaviour on the inputs that
separated them.** It does not say which class is right, and every ranking key
we could derive from it is wrong-signed. "Agree with the most siblings" is a
popularity contest among samples from one model prior — CAPA says those errors
are becoming *more* correlated, and the minority-correct case is the case this
product exists for. "Agree with the base tree" ranks *did nothing* above *did
the job*: we verified it on the adversarial corpus, where vectors 06–08 leave
the bug in place and therefore agree with base while the honest fix does not.
So zero ranking keys ship. The output is an escalation to a human with the
distinguishing input attached, and the metrics are recorded so M2b can
correlate them against ground truth before anyone builds on them.

The floor under it is `streamed`, and the receipt says so: `evidence_regime:
"derived"` with `inputs["evidence_floor"]: "streamed"`, never the flattering
`control-plane`. The reducer runs no candidate code, and every byte it
consumes came out of a candidate's process. A forged observation is a forged
statement about a *sibling* — that is the new attack class this design
introduces, it is stated in the design doc in as many words, and it is
tolerable for exactly one reason: the output is a question for a maintainer,
not a verdict that can reject a rival. If the differential ever acquires a
hard gate, that analysis has to be redone.

**Still open, and written down as open** in the design doc, `docs/concepts.md`
and the README: at T0 the corpus copies are owned by the uid the candidate's
process runs as, so a sibling can still reach another world's directory —
detected and attributed now, not prevented; a cohort member that answers
"opaque" on *some* cases still shrinks everyone's comparison; a candidate can
read the corpus off the filesystem at oracle time at T0, so what decision 13
delivers there is "not delivered to the generating agent", not "not reachable
by it"; and below CPython 3.11 `PYTHONSAFEPATH` is ignored, so tool shadowing
is caught by the contradiction check alone. Two corpus providers ship, not the
three decision 5 promised. `hypothesis`, `mutmut` and `cosmic-ray` are all
absent on this machine, so every degradation path in the block is the path
this tree actually takes — the acceptance script asserts the pre-flight
machinery abort with an untouched ledger rather than skipping quietly.

Compatibility, proved rather than claimed: the shipped default policy does not
move (`mv0:f207c3fa…`), every new rung is opt-in, and every M0–M1f ledger
replays byte-for-byte. Receipt digests do move — three additive fields — which
is free for the same reason it was in M1b, M1c, M1e and M1f: nothing about a
recorded decision is ever re-derived from a re-serialization of its inputs.

**Numbers:** 4 new oracle kinds, 5 gate predicates, 22 metrics, 1 escalation
rule, 24 validation rules, 19 adversarial vectors, `scripts/accept.sh` at 10 new
steps. `gofmt`, `go vet`, `go test ./...`, `accept.sh`, `m0-accept.sh` and
`adversarial.sh` all green.

**And the honest number is not 19 of 19.** With `hypothesis` and `mutmut`
installed afterwards — the three vectors that had aborted as *machinery* now
actually run, and all three are caught — the corpus reads **eighteen stopped or
escalated, one landed**. `19-differential_conformity` passes every gate in a
solo race, is SELECTed, admitted, and `mvo verify` accepts it, with the bug
fully intact. It reads the input corpus off the filesystem at oracle time and
special-cases exactly those inputs, leaving every other input wrong. Raced
against an honest fix it escalates, because the two land in different behaviour
classes — but alone, it wins.

That is open at T0 **by construction**, and worth stating without softening: the
world has to execute the corpus, so the world can read it. We can withhold the
inputs from the *generating* agent, and do; we cannot withhold them from the
process that must run them. T1 closes the sibling half — one world can no longer
reach another's corpus — and leaves this half exactly where it was. It is the
classic overfit-to-the-oracle, it lives in application code a reviewer is about
to read, and the differential's answer to it is ambiguity rather than
conviction. The README and `docs/concepts.md` now lead with this rather than
with the eighteen.

## 2026-08-15 — M1f: moving the trust boundary

**We red-teamed our own fix and it lost twice.** M1f shipped that morning
claiming the candidate no longer writes its own evidence. By the afternoon an
adversarial pass had landed **two** forgeries on trunk under the **shipped
default policy**, each with `mvo verify HEAD` → `OK: attestation verified
(7 checks)` and `mvo audit` → `OK`, and each with the bug fully intact. Both
went in through the same door, and the door was a sentence in our own design
doc that was simply false.

**Decision 16 said the harness path set closes the entry-point-plugin
surface. It does not, and it never could.** pytest imports `pytest11` entry
points from *any* `*.egg-info` or `*.dist-info` directory on `sys.path`; the
candidate's tree root is on `sys.path` when the oracle runs `python3 -m
pytest`; and the module such metadata names may be called **anything**. A path
set is a finite list of globs. "Any module named by any metadata directory" is
not glob-shaped, so no version of the protected-paths gate was ever going to
catch it — the tree-guard passed `evilmod.py` and `evil.egg-info/` as ordinary
allowed additions, `harness_added=0`, `protected_modified=0`. The plugin loaded
*after* `-p mvo_evidence`, in the same interpreter, popped
`MVO_EVIDENCE_STREAM`/`_NONCE` out of the environment so our own observer
configured inert, and authored a complete self-consistent forged stream, a
matching JUnit file at the argv `--junit-xml` path, and `exitstatus = 0`. The
suite receipt then recorded `evidence_regime: streamed`, `evidence_plugin:
sha256:69b4301…` — **the honest embedded plugin's digest** — and
`tests_passed: 8`. The label did the laundering, exactly as the study said it
would: `execution.evidence_plugin` was an embedded constant, never re-derived
from whatever actually wrote the bytes.

**The fix is a capability removal, not a better glob.** Oracle pytest runs now
carry `PYTEST_DISABLE_PLUGIN_AUTOLOAD=1`, so the only plugins that load are the
ones mvo names on argv. It is a policy field — `evidence.plugin_autoload`,
`""` ⇒ `"off"`, tri-state for decision 4's reason — because sealing autoload
is not free: a suite that genuinely needs `pytest-asyncio` or `pytest-django`
collects nothing without it. `"on"` is the escape hatch, and it is a pinned,
attested choice with a stated cost, the same shape as `crosscheck: "off"`.
Depth behind it: `**/*.egg-info/**` and `**/*.dist-info/**` join the default
harness set, so a candidate that ships distribution metadata into a repo that
had none also dies at rung O-1, where a reviewer can see it. Both defences are
under live test — the attack is now corpus vector `13-entrypoint_plugin`, it
dies at the guard under the shipped default, and under `no-paths.json` (guard
gate, guard oracle and invariant all absent) its plugin never loads at all and
the receipt carries the real numbers: `tests_total=8, tests_failed=2,
tests_passed=6`, REJECT at `status-pass@suite`. That second run is the only
measurement in the corpus that exercises the evidence path, which is the whole
reason the vector was worth adding rather than patching around.

**The second break does not have a fix, so it has a row in the table
instead.** `collect-equals-suite-total` was sold as the answer to the study's
signature failure — a three-test repo reporting a 500-test suite. Make both
oracles lie *consistently* and the invariant holds: the red team's second
patch forged `collected=500` on the collect run (winning the stdout
cross-check by writing a trailing `500 tests collected in 0.01s` at `atexit`,
because `parseCollected` takes the last matching line and `atexit` runs after
pytest's summary) and `tests_total=500, tests_passed=500` on the suite run, and
`mvo explain` printed `INVARIANTS: ok` over an 8-test repository whose real
suite is `2 failed, 6 passed`. **A cross-check between two sources both
produced inside the trust boundary cannot detect a coherent forgery.** What
the invariant actually buys is the careless adversary who forges one number
and forgets the other — real, common, and why vector 09 was caught under M1e —
and it is now written down as exactly that, in the design doc's decision 10,
in the README's table and in `docs/concepts.md`. The 500-in-3 row is split:
**DETECTED** when incoherent, **OPEN** when not. Closing it needs a count
derived outside the test process — control-plane static AST collection — and
that is not in M1.

**`mvo guard`, the verb we pitch as the adoption wedge, was blind to
`.gitignore`.** It snapshots the working tree through `git add -A`, which
honours the in-tree ignore rules; pytest does not. A `conftest.py` physically
present in the tree and named by `.gitignore` produced `examined: 1 path(s)`
and `OK: no protected or harness path was modified` — a false clean verdict
from the one command an evaluating maintainer runs to decide whether to trust
the gate at all. The race path was already defended (a force-applied file is
in the world's index, and the drift between that tree and a fresh snapshot is
a `tree_drift`), which is why the gap was only ever in the advisory verb.
`mvo guard` now lists such files as `IGNORED:` and refuses to print the clean
verdict (`INCOMPLETE:`, exit 1), and `**/.gitignore` joins the harness set —
it decides what the guard is able to see, which makes it harness in the
sharpest sense available.

**Three CAS references were outside the sweep, and each was a blob you could
delete under an `OK`.** `execution.evidence_plugin` (decision 14 calls the
plugin digest "an auditable fact, not a version guess"; a fact nothing
re-reads is a version guess with a colon in it), `baseline.recorded`'s evidence
stream (the observation record for `collected_base`, the denominator
`collected-not-below` divides by — it was in CAS, referenced by nothing, and
inflating `cas_unreferenced`), and `agent.finished.stderr` (named in no other
event, and the only place a `CONFIG_ERROR` world's reason survives). All three
are in the normative table now, all three are in the missing-injection test,
and a two-race workspace that used to report `cas_unreferenced: 4` reports
`0`.

**`mvo audit` printed `OK:` and then failed.** `--require-decisions N` — the
one flag decision 23 added for CI, whose entire purpose is to be scraped —
was checked *after* the output block, so an empty workspace produced
`OK: 2 events, 0 decisions replayed …` on stdout followed by exit 1. Any CI
check keyed on `OK:` read a failed audit as a pass. The check is in the guard
now and the failure prints `SHORTFALL:`. And `--key`, which the contract's CLI
section specifies and the sweep section promises, did not exist:
`mvo audit --key foo.pub` exited 2 with `flag provided but not defined`. It
exists, an unreadable anchor is refused rather than silently downgraded, and
both the human line and the JSON name which anchor was used — because
"verified" against the workspace's own key is a **self-check a rogue clone
reproduces**, which is precisely the confusion the study documented.

**Two guard-receipt bugs that put false statements into signed rationales.**
`result.detail` took `violations[0].Path` from a list sorted by `(kind, path)`,
so the contract's "lexicographically first offending path" named the wrong
file whenever the two orders disagreed — and the correct implementation,
`GuardReport.FirstOffender`, was sitting there as dead code. And `tree_drift`
incremented `protected_modified` with an empty path, so a receipt read
`protected_modified=1 … (first: -)` when nothing protected had been touched.
Drift is machinery — "an earlier oracle wrote into the tree, or something
raced us" — and it is reachable: `11-cross_world_sabotage` mutates sibling
worlds' trees, so an honest victim would have been convicted of editing a file
it never touched. It now yields `status = error` with metrics **absent**, the
same shape as an unreadable tree, and escalates as machinery.

**And one that would have failed an honest repository on its first race.**
`streamEnv` emitted an absolute `PYTHONPATH=<plugin dir>`; `os/exec`
deduplicates env keys keeping the last, so appending that to `os.Environ()`
silently discarded the operator's `PYTHONPATH` on every T0 run under the
shipped default. A src-layout repo, an uninstalled package, or any repo whose
tests import through `PYTHONPATH` would have collected zero tests on the very
first race a new user ran, and blamed us for it correctly. It is prepended
now. On T1 nothing is inherited by construction — the image owns its
environment as it owns `PATH` — and that is stated in the contract rather than
papered over.

**The quickstart had gone factually false again, in the opposite direction.**
The study's finding was two false sentences about what `mvo audit` checks; the
fix made audit stronger and nobody re-read the page, so it now told readers
that audit *does not* sweep CAS, printed transcripts showing `OK` after
deleting an attestation blob (both are exit 1 today, asserted by step 9a),
pinned the M1e default policy digest, taught a three-gate ladder ranked by
`wall_ms_asc`, and listed a closed gate vocabulary missing two of its members.
Every one of those is re-recorded against the shipped binary. **Step 3p is the
part that matters**: accept.sh now greps the reader-facing pages for the
current default digest, the known-gate list, and the absence of the retracted
sweep claim. A grep is a poor proof of correctness and a perfectly good proof
of non-rot, which is the failure that has now happened twice.

**Smaller, and all of them real.** `testdata/toyrepo/policies/sealed.json`
declared `evidence.regime: "isolated"`, a regime this binary cannot deliver,
so the shipped example the docs point at validated clean, installed as the
workspace default, and then refused every race — a workspace bricked by a file
we told people to read. It is `"auto"` now, `mvo policy use` refuses an
undeliverable regime at install time in the same sentence `race` used to
produce, and accept.sh step 3q installs and races it. `mvo policy list`'s
adoption hint disappeared for an operator who had run the first of its two
commands and not the second, leaving them half-adopted and unprompted; it keys
on the pin alone now and prints the remaining command with a `--dir`-correct
path. `mvo explain`'s evidence line gained the tier parenthetical the contract
calls load-bearing, in the form that is true today — `regime streamed
(T0-worktree; isolated not deliverable in this binary)` — rather than pointing
at a `--tier T1` that would not help. And the contract itself stopped
presenting work that did not ship: acceptance steps 3l/3m/3n/11,
`patches-forge-deep/` and `testdata/plainrepo/` are marked NOT SHIPPED with
the unit tests that stand in for them, the module layout names the files the
code actually lives in, `Result.Detail` is documented as the schema addition
it is, the plugin directory's `0755` is recorded as an amendment against the
specified `0555`, and "all eleven laundering vectors at rung O-1" is corrected
to eleven of twelve, with `12-planted_artifact` at `status-pass@suite`.

**Where the boundary is now.** Twelve of twelve laundering vectors stopped,
eleven at rung O-1 with exactly one receipt each. Open, and said plainly: an
adversary who patches the observer from inside the test process; a **coherent**
forgery across both oracles; weakened or rewritten assertions once the guard
is relaxed; and a backdoor that passes every honest test. The default policy
digest moved twice inside this block, which is what pinning is for — every
recorded intent still resolves its own policy from CAS and replays byte for
byte, and `scripts/accept.sh` proves it against a real M0–M1e ledger.

The lesson is cheaper to write than it was to learn: **a claim in a design
doc is not a defence.** Decision 16's sentence about entry-point plugins was
written, reviewed, shipped, and wrong, and the only thing that found it was
someone trying to break the thing we had just declared fixed.

---

## 2026-08-15 — M1f: the trust boundary moved

**The candidate no longer writes its own evidence.** The design partner study
found that suite metrics were parsed from a JUnit file written inside the
candidate's own writable tree, and the adversarial corpus we built *before*
touching any of it measured the damage precisely: **eight of eleven laundering
vectors landed on trunk with a signed attestation `mvo verify HEAD` accepted,
eight of them leaving the bug fully in place.** Re-running that corpus today:
**eleven of eleven stopped, ten at rung O-1 with exactly one receipt each, and
only the honest fix lands.** The baseline diff is in
[`testdata/adversarial/README.md`](testdata/adversarial/README.md) — red table
and green table side by side, because "we changed the oracle" is not a result
and "these rows moved from LANDS to CAUGHT" is.

Three mechanisms, in the order a race meets them.

**Rung O-1, `tree-guard`.** A registry oracle that executes nothing: it
compares the intent's base tree against the candidate's, both git trees the
control plane holds, under two declared path classes. `protected` (test files)
is frozen against modification and deletion, additions allowed — adding a
regression test is the behaviour we want. `harness` (`conftest.py`,
`pytest.ini`, `pyproject.toml`, `sitecustomize.py`, `*.pth`, …) is frozen
against modification, deletion **and creation**, because the study's forgery
patch's entire content was a new `conftest.py`. Two `git ls-tree` walks, no
process, and the one gate whose evidence an adversary provably cannot author.
The glob grammar is closed and hand-written rather than a regexp, for M1e
decision 6's reason: a policy is data that reaches the decision core.

**The evidence stream.** A control-plane-owned pytest plugin, embedded in the
binary and materialized read-only outside the candidate's tree, writes a
framed event stream to a control-plane channel that mvo reads **live**.
Nothing in the candidate's tree is read as a metric source after the process
exits, which is the whole of the two vectors the study reproduced. Three
structural rules, none policy-controlled: absence never passes (a 0-exit with
no usable stream is `error`); the exit code is cross-examined against the
stream; and JUnit XML is demoted to a *cross-check* whose disagreement is
`error` — brittleness escalates, it never convicts and never acquits.

**Cross-oracle invariants.** `collect-equals-suite-total` makes "500 tests in
an eight-test repository" a contradiction rather than a signed fact, and
`escalation.on_invariant_violation` is rule 0 — above every M1e rule — so a
detected forgery reaches a human instead of being filed under "the tests
didn't pass".

**The shipped default changed, and that is what pinning is for.** It gains the
guard gate and the invariant, and it **drops `wall_ms_asc`** in favour of
`on_ranking_tie`: the study measured a cheat winning 6 of 10 identical races on
~100 ms of jitter, each time with a signed rationale naming the stopwatch as
decisive. A correct refusal beats a confident wrong answer. Existing intents
keep the digest they pinned and replay exactly as before; upgrading the binary
rewrites nothing on disk, and `mvo policy list` prints the two-command
adoption path. `mvo audit` gained the CAS integrity sweep the study caught it
lacking — it re-reads and re-hashes everything the ledger references from a
declared table, so a deleted attestation blob and an edited artifact are now
exit-1 failures naming the digest and the record that pointed at it — plus
`--require-decisions N` for CI and `--cas-sweep=false`, which says it was
skipped rather than rendering like a pass. `mvo guard` is the adoption wedge:
two trees, one policy, exit 0 or 1, and no ledger write at all.

**One design deviation, recorded because it is load-bearing.** M1f specified a
FIFO for the evidence channel. A bind-mounted FIFO on Docker Desktop is a pipe
*inside the VM*, unconnected to the host's read end, so the first in-container
write blocks forever — the channel deadlocked under T1, the exact tier the
`isolated` regime requires. The channel is now a control-plane-owned file
tailed live. The properties that mattered were never the inode type: the
reader holds one handle at a monotonically increasing offset and never
rewinds, so the already-received prefix is immutable; a candidate that
truncates and rewrites at exit desynchronizes the sequence, which makes the
stream unusable — a failed gate, never a forged pass; and where a distinct
oracle uid exists, the containing directory is unwritable by it, so the
channel can be appended to and neither unlinked nor replaced.

**The integration gate found the label lying, and that is fixed here.** `auto`
resolved to `isolated` for every T1 race and `execution.evidence_regime`
recorded it — while `t1World.Command` execs with workdir `/work` (read-write)
and no `--user`. A real T1 race under the shipped default produced receipts
reading `evidence_regime: "isolated"` next to `isolation_caps.user: "501:20"`,
the invoking uid, which owns both the worktree and the evidence directory: not
one of the three guarantees the regime table promises under `isolated` held,
and `mvo explain` printed the claim to the operator. That is the study's
finding with the *label* doing the laundering — a signed claim stronger than
the evidence behind it — so a regime is now chosen strictly by the capability
that ships. `auto` is `streamed` on every tier; a policy declaring `isolated`
is refused at pre-flight as machinery, naming the missing exec path, rather
than downgraded quietly. `internal/race/evidence_test.go` pins the table the
design asked for and nobody wrote, including the guard rail that flipping
`isolatedExecAvailable` must accompany the `--user` change.

**A second integrity claim the gate found inverted.** The new CAS sweep reads
`attestation.recorded.statement` as a standalone blob, which M1f's `admit` is
the first to `Put`. Every M0–M1e ledger names a statement whose bytes live
only inside the DSSE envelope, so `mvo audit` exited 1 on intact pre-M1f
workspaces with `MISSING: … (statement)` — audit accusing a workspace of
losing something it never lost, which is the over-claim this block removes,
pointed the other way. The sweep now resolves that one reference through its
container when it is not standalone, re-hashing the extracted bytes: strictly
stronger than the direct read, because it also proves the bundle's signed
payload is the statement the ledger names. Verified against a real M1e-built
ledger: `25 events, 2 decisions replayed, 31 CAS objects verified`.

**The corpus proves the guard, not the stream — so the missing step got
built.** All eleven laundering vectors ship a harness file or edit a test, so
all eleven die at rung O-1 and **not one reaches the evidence stream**, the
mechanism the block is named after. Read carelessly, 11/12 credits the stream
for the guard's work. Step 3k now races the study's vector 1 — the
`conftest.py` writing a fabricated 500-test `junit.xml` at `atexit` — under
`no-paths.json`, with the guard gate, the guard oracle and the invariant all
absent: the receipt reads `tests_total=8, tests_failed=2, tests_passed=6`, the
world fails `status-pass@suite`, and the decision is REJECT. Under M1e that
same patch passed every hard gate and landed on trunk. The corpus README now
says outright which column measures what.

Building it surfaced a latent acceptance bug: step 9a's evidence-stream
extractor `break`s out of its stdin loop, SIGPIPE-ing `sqlite3`, which
`pipefail` turns into a 141 that aborts the script. It had only ever passed
because the receipts fitted in the pipe buffer; one more race was enough. It
now drains stdin. The same trap took the new T1 explain assertion (`mvo … |
grep -q` exits on match and the SIGPIPE, not the match, decides the pipeline),
which is why the script's own idiom captures first.

**What is still open, said as plainly as the rest.** An adversary who patches
the observer from inside the test process wins, and nothing in-process defeats
in-process code; weakened and rewritten assertions are undetectable from test
outcomes and wait on O3 mutation testing; and a backdoor that passes every
honest test is ch. 13's actual problem, which M1 does not address. The
`isolated` regime itself is open: the mounts are built, the exec path is not.
`patches-forge-deep/` and steps 3l/3m/3n/11 remain unbuilt; the mechanisms
they cover are under unit test, and step 3k covers the load-bearing half of
3k/3l end to end.
Every receipt therefore records **which evidence regime observed it** — today
that is `streamed` for every pytest run, on T0 and T1 alike. `mvo explain`
prints that line unconditionally, because a guarantee nobody can see the
absence of is a guarantee nobody has, and the label is not exempt.

---

## 2026-08-15 — Design partner study: three "no"s, and the hole they found

**We ran M1 acceptance criterion 3 as a real experiment and it failed, which is the most useful thing that has happened to this project.** Three emulated design partners — a fintech platform engineer, a burnt-out OSS maintainer, and a staff engineer whose explicit brief was to break the value proposition — each built their own repo and attempted adoption reading only the README, quickstart and concepts docs. No source. No design docs. No PRD. A fourth agent then verified every claim against the code before any of it was believed. All three completed `init → intent → race → explain`; all three needed **three** support interactions against a bar of one; all three declined adoption. [The full study is published](docs/studies/2026-08-design-partner-study.md).

**The finding is architectural: the candidate writes its own evidence.** `concepts.md` states the rule the whole product rests on — *"Oracles outrank agents. Agent output is a claim."* — but the suite oracle runs pytest inside the candidate's world, writing its JUnit XML into the candidate's own writable tree. A patch consisting of nothing but a `conftest.py` that sets `session.exitstatus = 0` and writes a fabricated `junit.xml` at `atexit` passed every hard gate, produced a receipt claiming **500 passing tests in a three-test repository**, won the race, landed on trunk with the real bug untouched — and then `mvo verify` printed `OK: attestation verified (7 checks)` and `mvo audit` printed `OK`. The admission re-gate blessed it too, because the same conftest is in the landing tree. Every downstream guarantee faithfully certified a number the adversary authored. The skeptic's phrasing is going in the design doc: *CI's green check is a rumour everyone knows is a rumour; Multiverso signs the rumour and hands it to a reviewer who has been told oracles outrank agents.*

Three more verified criticals. The shipped default ranking ends in `wall_ms_asc` with tie-escalation off, so in a repo where an honest fix and an assertion-weakening cheat both pass every gate, **the cheat won 6 of 10 identical races on ~100ms of wall-clock jitter** — each time with a confident signed rationale naming the stopwatch as the decisive key. The gate ladder catches exactly one laundering vector out of five: test deletion is stopped cold, while skipping tests, weakening assertions, rewriting assertions to match the candidate's own wrong output, and forging the artifact all pass clean (`tests_skipped` is recorded in every receipt and no gate in the vocabulary can read it). And in six commands from documented steps only, the unencrypted private signing key reached git history: `mvo init` edited the *tracked* `.gitignore`, the quickstart's own remedy for the resulting warning was `git reset --hard`, which reverts that line. **Our fixture was structurally immune** — `testdata/toyrepo/.gitignore` already had the entry, so the documented path could never fire it. A fixture that cannot fail the way real repos fail certifies the docs, not the product.

What survived is the reason to keep going. The skeptic reported his going-in thesis wrong: admission re-gating caught a change a green GitHub check would have waved through, the freshness/tree-env binding held against direct attack, and the publication path beat all three tamper attempts with errors naming the file and the reason. Nobody rejected the concept — every objection lives in the shell.

Fifteen doc and CLI-ergonomics fixes shipped with this entry, gate green: the ignore rule moved to the untracked `.git/info/exclude` (closing the key chain), two **factually false** sentences about what `mvo audit` checks corrected, a "what the gate ladder does not catch" table added from the five verified vectors, `--key` documented as *the* trust anchor rather than a convenience, and a merge-queue section stating the fact that decides that question: an attestation survives a fast-forward of the exact admitted commit and nothing else. Also fixed: a flaky docker teardown assertion in our own suite — a project whose thesis is evidence quality does not get to ship a test that asserts before the daemon has finished reaping.

The four criticals are the next block of work, ahead of M2's scheduler. Building budget allocation on evidence the evaluated party can forge would be building on sand.

## 2026-08-15 — M1 closure: the audit, the docs, and a real remote

**M1 (Candidate Race v0) is code-complete.** What that sentence is worth depends entirely on how it was checked, so: three auditors read [PRD §7](PRD.md#7-functional-requirements) requirement by requirement against the shipped code and its tests, instructed that an inflated DONE is worse than an honest PARTIAL. The result — **11 complete, 14 partial, 0 missing** — is now the [README's Status section](README.md#status), gaps named in plain language rather than buried. Four of those partials are genuine spec-vs-code drift that no design doc had recorded as a deferral (CP-6's `REPAIR`, three `Receipt` fields EP-2 names, EP-7's `seeds`, TP-2's identity tier being one hardcoded literal instead of a modeled vocabulary); the other ten were conscious deferrals, now cited to the design doc and line that deferred them.

**The docs got the same treatment as the code.** A writer produced [`docs/quickstart.md`](docs/quickstart.md) and [`docs/concepts.md`](docs/concepts.md) from the audited reality, then a second agent — allowed to read *only* the README and quickstart, never the source or the design docs — followed them literally on a cold machine and recorded every place they lied. Nineteen failures, three of them blockers: the build step left `mvo` uncallable, the missing-agent pre-flight demo stripped its own binary off `PATH`, and the "author your own policy" section printed a JSON fragment with a `/* comment */` in it that no JSON parser would accept. Eighteen were fixed and the corrected quickstart re-run end to end from cold. One finding was load-bearing enough to become documentation in its own right: a policy is content-addressed over its **exact file bytes**, so reformatting a policy mints a different policy — and therefore a different pinned digest.

**And it ran against a real remote.** [`docs/probes/m1-github-demo.md`](docs/probes/m1-github-demo.md) records M1 acceptance criterion 4 executed on a public GitHub repo: a race whose losing candidate goes green by deleting the failing test, stopped by `collected-not-below` at `collected_delta=-1` **before the suite oracle ever runs**; an admission landing a commit with intent/decision/attestation trailers; and a fresh clone that had never seen the workspace authenticating 15 published items and replaying both decisions from nothing but a public key. The first attempt failed — hand-written patches with a wrong hunk header — and that failure was itself informative: the worlds recorded `CONFIG_ERROR` and `mvo explain` named it as the gate failure rather than quietly dropping the candidates. Agent failure is evidence; machinery failure is an error; the distinction held under a case nobody designed for.

Remaining M1 acceptance criteria need a human: a design partner running the quickstart on their own repo (criterion 3), and the 30-instance SWE-bench-Live budget-accounted run (criterion 2), which wants the M2 evaluation harness.

## 2026-08-14 — M1e: versioned policies + the real oracle ladder

**The policy is now an artifact, and the gate is now Python.** `multiverso.dev/policy/v1` (CP-5 to its full text) is an ordered list of hard gates that each name a declared oracle instance, a pass predicate and the weakest freshness basis they accept; a **lexicographic** ranking spec (never a weighted sum); and a closed set of escalation conditions. `mvo policy list|show|validate|use` authors and installs them; `mvo intent new --policy` pins one per intent; `mvo init` writes a default that runs the real ladder — **O0** `pytest --collect-only` (collected counts) then **O1** the suite gate (JUnit XML + reportlog + coverage.py), with `collect-nonempty` and `collected-not-below` ordered *before* `status-pass` so a candidate that deletes tests is stopped by the counts and never reaches a green suite (EP-1, EP-2, EP-7). ESCALATE is a first-class race outcome, and `mvo explain` renders *why* the winner won, key by key, from the ledger + CAS + the pinned policy — derived at render time, never stored (CP-6).

Design decisions worth reading in [docs/design/M1e-policy-oracles.md](docs/design/M1e-policy-oracles.md): **digests are never recomputed from decoded objects** (decision 1) — `Decide` takes the object *paired with the digest it was recorded under*, so M1e adds fields to `World` and `Receipt` and still replays pre-M1e ledgers byte-for-byte, ending the "this milestone breaks replay" tax M1b and M1c both paid; v0 is **frozen on the wire and upgraded only in memory**, with a rationale renderer frozen per schema version, because re-wording a decision made under a policy pinned in 2026 rewrites history and audit compares the sentence byte-for-byte (decisions 2–3); `gate_pass` is an implicit *first* ranking key and `world_digest_asc` an implicit *terminal* one, so the winner provably passes every hard gate and the order is total (decisions 4–5); escalation is a fixed struct of named conditions — **no expression language ever**, because a policy is data that reaches the decision core (decision 6); unknown gate/key/basis/kind = load error, so `Decide`'s `default:` branches are unreachable rather than fail-closed guesses (decision 7); exit code 5 is **fail, never pass**, with `collected_total = 0` recorded explicitly (decision 14, research ch. 19's laundering vector); a missing toolchain aborts at pre-flight as *machinery*, never as a failing candidate (decision 15); a metric derived from an absent source is **absent** — never zero, never "assumed 100 %" (decision 16); and first-run status is parsed from the reportlog so a pass-after-rerun is recorded as strictly weaker, separately named evidence (decision 17). `--oracle-cmd` survives only where it is honest — under a v0 policy, whose digest never determined its gate — and `mvo intent new --oracle-cmd` is the migration: the command moves *inside* the pinned, attested artifact.

Review round, 12 findings, 11 applied. The **BLOCKER** was a hole the v0 shape could still express: `{"hard_gates":[]}` compiles to a policy that gates *nothing* (v1 validation requires a gate; v0 is decoded exactly as M0 decoded it, never re-validated), so every world passed, `admit.Decide`'s gate loop was vacuously satisfied, and an ADMIT was **signed, attested and landed** on the strength of no gate at all — with the ledger left permanently un-auditable, because admission derived its evidence from one landing oracle while replay derived it from zero gate selectors. Fixed at the ingest boundary (`policy validate`/`use`, `intent new --policy`), with two more locks behind it: `landingSpecs` refuses a gate-less policy as machinery before anything is recorded, and `admit.Decide` fails closed with REJECT — what cannot be attested must not be admitted. `policy.Decode` stays total on purpose, so historical ledgers and published closures keep replaying exactly what they recorded. Three **MAJOR**s: the ladder's short-circuit ignored gate *order*, so with interleaved gate oracles the recorded rationale (and `mvo worlds`' GATE column) named a gate that never ran while the gate that actually failed was reported "not-evaluated" — decision 12 inverted; `mvo explain` laundered an ESCALATE into a win, printing `why X won` and `WINNER X ← decided here` three lines above the block reporting that a rule refused to call it a decision — under `on_ranking_tie` crediting the exact digest coin flip the rule exists to refuse; and `escalation.require_evidence` could not fire in any race this orchestrator produces (the winner passed every gate, so its ladder always ran to completion and the receipt was always *there*) — it now tests what decision 16 documents, an oracle whose structured source was unavailable and whose receipt carries none of its metrics, driven through `race.Run` in the test rather than a hand-built receipt slice. Also: validation went from kind-level to **instance-level** (a `coverage-at-least` gate on a `coverage:false` suite oracle, or on a prefix coverage.py cannot wrap, is now refused at load instead of failing forever at 3 a.m. — and the validator reads the *same* predicate the oracle does, so the two cannot drift); `gateDef.threshold` stopped being a dead field (a threshold on a gate that takes none is an authoring error, not a harmless extra); gate cells render the measurement from **that gate's own receipt** and `--json` drops any metric two counted receipts disagree about, because a number a consumer cannot attribute to an oracle is worse than no number; and `fetch-race` stopped assuming one `suite`-family landing receipt — it now checks the attested subject tree against *every* gate receipt (a legal collect-only policy was being rejected) and renders its world table's GATE cell through the same trace `mvo worlds` uses. One finding was **contract-side only** and the doc was amended to match the code: `race.Config` gains `LegacyOracle`, never `Policy` — the compiled policy can only come from `intent.Policy`, which is the safer design the contract's own step 1 already prescribed. The one skip: re-deriving `mvo audit`'s admission evidence from "one receipt per gate oracle" instead of from the pinned policy's gate selectors — the two derivations now provably agree (a gate-less policy never reaches admission at all), and making replay depend on `admit.Run`'s plumbing rather than on the policy's own selectors would trade a proof for a coincidence.

Acceptance grew five steps, all against the toy repo: a lexicographic race decided by the **second** ranking key, a test-deleting candidate stopped by the collected-count guard (and provably never reaching the suite oracle), an ESCALATE on a tie, a rejected policy file with its byte-exact error, and a legacy-v0 race whose rationale still matches M0's frozen sentence — then one `mvo audit` replaying **two policy schema versions in one ledger** byte-for-byte. Parser tests run against committed `testdata/oracle/` fixtures, so no test needs pytest, coverage.py or any plugin installed; the plugin-dependent paths degrade honestly and say so.

## 2026-08-14 — M1d: forge publication (`mvo publish` / `fetch-race` / `prune`)

**Evidence now travels.** `mvo publish <intent>` puts a race's full closure on any git remote under `refs/multiverso/intent/<short>/` — candidate refs whose commits are *pure functions of content* (epoch-pinned timestamps, fixed identity: idempotent republish structurally diffs to zero) and one evidence commit whose tree holds the intent, policy, worlds, DSSE-signed receipts and decisions (TP-1: raw DSSE over canonical bytes, payload-type domain-separated by PAE), and — when admitted — the attestation bundle verbatim. `mvo fetch-race <short>` is the second machine's verb: workspace-less, ledger-less, it mirrors the namespace into any clone and verifies everything offline against one explicit public key — content addresses recomputed, signatures checked, the cross-reference graph closed, candidate commits pinned to signed-cited world trees, and **every decision replayed through the shipped `race.Decide`/`admit.Decide`** (a wrong-winner decision signed with the right key still fails). Failures are collected, never fail-fast: each bad item prints `mvo: fetch-race: <path>: <detail>`. `mvo prune` applies FI-1 retention — losers always prunable, an admitted intent keeps winner + evidence unless `--keep-admitted=false`, `--older-than` guards against pruning what was just published — and never touches CAS or ledger.

Design decisions worth reading in [docs/design/M1d-publication.md](docs/design/M1d-publication.md): nothing is location-trusted (the probe showed anyone with push access can move these refs, so self-authentication is the whole trust story — ref names are discovery only); push discipline is explicit per-ref refspecs under `--force-with-lease` CAS with plan-diff + namespace reconciliation, making "namespace ⊆ plan" an invariant fetch-race can treat any stray ref as tamper evidence; signatures live in envelopes, never inside signed objects (sign-on-publish, byte-stable re-minting); DP-3 holds — prompts and transcripts never leave the private CAS. Trunk drift became a render-time display line (`freshness: FRESH|STALE|UNKNOWN`) on `worlds`/`explain`/fetch-race — never a ledger mutation; EP-3's recompute-on-admit stays the enforcement point.

Tests: full tamper matrix against local bare remotes (flipped receipt bytes, swapped filenames, re-digested decisions, doctored unsigned worlds caught by the signed decision's subject pin, stray refs, missing winner, tree swaps, wrong key, tampered/mis-budgeted attestations), lease-failure surfacing, publication-event goldens, prune policy table, five-row drift table. Acceptance now runs publish → idempotent republish → include-rejected delta → clone + fetch-race → in-remote receipt tamper (fails naming the exact path) → healing republish → retention prune → drift markers, all against a bare remote in a temp dir. The network is never touched.

Review round, 9 findings, 8 applied: two real **BLOCKER**s. (1) The published world set was unbound — worlds are the one closure kind authenticated by digest alone, and the graph only checked the forward direction, so anyone with push access could splice a self-consistent World naming attacker-authored code into the evidence commit, publish it as `cand/<n>`, and have fetch-race call it verified. Fixed by reverse containment: every published world must be a subject of a signed decision, every published policy must be the intent's, and one world may claim only one candidate ref. (2) `git ls-remote` tail-matches its pattern from any slash boundary, so a namespace survey returned refs merely *containing* the namespace path — and publish/prune turn survey output into `:<ref>` delete refspecs, silently destroying a `refs/heads/refs/multiverso/…/wip` branch or a look-alike tag. Fixed by anchoring `gitx.LsRemote` to the pattern's fixed prefix, plus an assertion in `gitx.Push` that turns decision 10's "refs/heads cannot appear by construction" into an enforced refusal. Also: `git push` is not atomic by default (a mixed batch really did land some refspecs while `publish.finished` recorded `pushed: []` — now `--atomic`, so the ledger's claim and the remote agree), and prune deleted the local GC pins *before* surveying the remote, so a remote-leg failure wiped them with zero audit trail (now: both surveys and a remote pre-flight first, and `prune.executed` is appended on every path that mutated anything).

## 2026-08-13 — M1c: T1 containers + parallel races

**Worlds can now run confined, and races run wide.** `mvo race --exec T1 --exec-image REF` puts every untrusted process — the agent and the oracle — inside a docker keeper container: digest-pinned image (TOCTOU-free: what was recorded is what runs), `--network none` by default (NFR-4, *proven* by an in-container egress probe whose passing receipt is the recorded evidence), `--cap-drop ALL`, read-only root with only `/tmp` and the bind-mounted `/work` writable, and per-world CPU/memory/pids caps that land in every receipt's new always-serialized `isolation_caps` (XP-2: enforcement you can audit). `--parallel N` runs the race as a two-phase bounded worker pool — generate, barrier, verify — where `--parallel 1` reproduces the sequential schedule *structurally*, decision inputs are digest-sorted before `Decide` (permutation-invariance now pinned by a property test), and audit replay needs zero changes.

Architecture per [docs/design/M1c-containers-parallel.md](docs/design/M1c-containers-parallel.md): the PRD §9 `ExecutionBackend` surface is `internal/backend` (T0-worktree byte-compatible with M1b — T0 world digests do not move — plus T1-container) over `internal/dockerx`, a gitx-style docker CLI shell-out (deliberate deviation from the PRD's Docker SDK line: `go.mod` stays untouched, adapters stay thin). One long-lived `sleep`-keeper container per world holds the pid namespace; agent and oracle join it via `docker exec`, so the watchdog's `World.Kill()` (`docker kill` → PID-1 teardown) reliably ends everything the tier confines. Worktrees are bind-mounted, never copied — host-side diff/transcript capture (AG-3/AG-4) runs unchanged, and the worktree's dangling `.git` pointer means an in-container agent cannot even see the shared repository. Containers are transport, not evidence: no container ID ever enters the ledger; the pinned image digest rides the XP-3 env manifest into every T1 receipt's `valid_for.env`.

Docker-gated tests skip cleanly when no daemon is reachable (CI) and run against a local fixture image (`python:3.12-alpine` + bash + pytest + the fake agents baked in — built once, never pushed, nothing larger ever pulled); the fake claude demonstrably runs *inside* the container while capture stays control-plane-owned. The parallel orchestrator's tests run under `go test -race`. Acceptance now includes a `--parallel 2` race whose decision type and winner match the serial run, and a T1 race whose suite receipt records `T1-container` + `network:none` — with a graceful skip when docker is absent. XP-4 warm pools stay deferred (v1), with `Backend.Open` deliberately shaped so a pool can arrive without a contract change.

## 2026-08-13 — M1b: real agents drive the race (Claude Code + Codex adapters)

**Worlds are now agent-produced.** `mvo race --agent claude-code|codex|script` spawns each candidate through an `AgentAdapter` whose shape enforces the trust rules at compile time: **Diff and Transcript are not adapter methods** — the control plane captures the diff itself (`git add -A && git diff --binary --cached <base-tree>`, after verifying the worktree's git state wasn't tampered with by the agent) and tees the raw event stream into CAS as the authoritative transcript. Agent-reported anything is never trusted.

Highlights from [docs/design/M1b-agent-adapters.md](docs/design/M1b-agent-adapters.md): six-value outcome taxonomy with control-plane-kill precedence (watchdog → BUDGET_EXCEEDED beats whatever the stream claims); costs as integer micro-USD tagged `client-estimate` (no floats in canonical JSON, no invented price tables — codex worlds honestly carry `usd_micro:0` + tokens); prompts rendered from a normative template with a negative test asserting **no policy digest, gate names, or oracle argv ever reach an agent prompt** (the AI-control rule, ch. 13); agent failure is *evidence* (a recorded world), machinery failure is an *error* (the race aborts). Budget enforcement is control-plane-primary: process-group SIGTERM → 5s grace → SIGKILL.

Testing without burning money: 8-mode fake-CLI fixtures (`testdata/fakeagent/`) cover the full outcome matrix; after the fixture PATH override, tests assert `exec.LookPath` resolves to the fixture and **fail closed** if the real CLI could leak through. Live smoke exists but only behind `MVO_LIVE_AGENT_TEST=1`.

Review round earned its cost again: 7 findings, including a real **BLOCKER** — the SIGKILL escalation skipped the process *group* when the leader had already exited, letting orphaned grandchildren keep spending. Fixed, plus evidence-capture failures now produce CRASH worlds instead of aborting the race.

## 2026-08-13 — M1a: signed admission lands (`mvo admit` + `mvo verify`)

**The trust plane exists.** `mvo admit` takes a race's SELECT decision, applies the winner onto the *current* trunk in a temp worktree, re-runs the suite against the exact landing tree (EP-3 v0: recompute-on-admit), and — only if the gate passes — commits via git plumbing with a `Multiverso-Attestation: sha256:…` trailer, recording an ADMIT decision plus a DSSE-signed in-toto attestation (predicate `multiverso.dev/admission/v0`). `mvo verify <commit>` re-checks everything offline: 7 fail-fast checks from bundle digest through signature, subject binding, reference existence, evidence freshness against the commit's tree, and budget consistency. Conflict on landing → ESCALATE with the conflict set as receipt artifacts (CP-8: never auto-resolve). Rebase-race safety via compare-and-swap `update-ref` — if trunk moves mid-admit, the admission records `result=ERROR` and is retryable.

Design decisions worth reading in [docs/design/M1a-admit-signing.md](docs/design/M1a-admit-signing.md): the attestation subject binds **landing tree + parent commit** (a trailer-referenced statement can't contain its own commit hash — fixed-point impossibility); the landing gate is a **pure function** of (policy, apply-receipt, gate-receipt), so `mvo audit` replays admissions exactly like race decisions; the landing oracle argv is recovered from the winner's race receipt, never from a flag — no gate-swapping at admit time.

Pipeline: architect → builder → integrator → 2 adversarial reviewers (crypto lens found zero real defects; conformance lens: 2 MINOR, both fixed) → fixer. Ed25519 + DSSE + PAE all stdlib, golden-vector tested against the DSSE spec. Keys live in `.multiverso/keys/` (0600), never committed.

## 2026-08-13 — M0 walking skeleton: built, reviewed, green

**The skeleton walks.** `mvo init → intent new → race → explain → audit` works end-to-end on the toy fixture: two candidate worlds from scripted patches, a suite oracle per world, a pure decision function that SELECTs the passing candidate, and an audit that replays every decision from the hash-chained ledger byte-for-byte — and fails loudly when a ledger byte is flipped.

**How it was built** (all agent-built, human-supervised, in one 70-minute pipeline):
- **Go-vs-Rust bake-off** (same storage-core spec, one agent each, judge re-ran both suites + a 10-case cross-implementation canonicalization probe): both implementations passed everything; canonical bytes were byte-identical across languages in 9/10 edge cases. **Verdict: narrow Go win** (clarity, test quality; Rust's minimal-dep constraint forced ~45 lines of hand-rolled hex/date code). One Rust idea back-ported: reject floats in canonical JSON per DP-1. PRD's Go default stands, now with evidence.
- **Build**: storage (object/cas/ledger) → engine (gitx/oracle/race/workspace) ∥ CLI (cmd/mvo + toyrepo fixture + acceptance script) → integrator (module compiled clean on first assembly).
- **Adversarial review**: 2 reviewers, 14 real findings — best catches: `mvo audit` printed OK even when replay diverged; `VerifyChain` couldn't detect deleted tail rows (fixed: seq contiguity + AUTOINCREMENT high-water cross-check); `GIT_DIR`/`GIT_INDEX_FILE` env leakage into gitx subprocesses; worktree path collisions across races. All fixed, 4 skips with written justification.
- Full gate verified independently after the workflow: `gofmt` / `go vet` / `go test ./...` (8 packages) / `scripts/m0-accept.sh` — all green.

**M0 exit criterion status**: audit-replay determinism ✅ (acceptance step 4 copies the workspace to a second location and replays). Remaining M0 items are the human-gated week-1 tasks below.

## 2026-08-13 — M0 kickoff

**Done today:**
- Project scaffolding: Go module (`github.com/coagente/multiverso`), Apache-2.0 LICENSE, DCO contributing policy, CI (build/vet/fmt/test), this build log.
- [M0 design doc](docs/design/M0.md): object schemas, ledger/CAS contracts, CLI verbs, acceptance script for the walking skeleton.
- Week-1 checklist progress:
  - [x] GitHub custom-ref durability probe → [docs/probes/github-custom-refs.md](docs/probes/github-custom-refs.md) — 1.2k refs fine; **finding**: GitHub's attestation store rejects self-signed bundles (tlog inclusion required) → mirror is v1+, git stays canonical
  - [x] Go-vs-Rust agent bake-off → narrow Go win, see entry above
  - [ ] **Needs Karim**: claim package names (`multiverso` + `mvo` on crates.io / PyPI / npm) — requires account credentials
  - [ ] **Needs Karim**: commission trademark knockout search for "MULTIVERSO", classes 9/42
  - [ ] **Needs Karim**: design-partner recruitment (target ≥1 committed partner by end of M0)

**M0 scope** (from [PRD §12](PRD.md)): ledger + CAS + intent/world objects + T0 worktree worlds + one oracle + `mvo race` with N=2 scripted patches (no agents yet). Exit: `mvo audit` replays a toy race deterministically.

---

## 2026-08-12 → 13 — Research + PRD

- 20-chapter research corpus (305 primary sources), 13/13 founding-memo claims verified against primary sources.
- PRD v1.0 approved for development after a 3-lens adversarial review panel (30 findings, all dispositioned).
- Headline: nobody ships >2 of Multiverso's 5 capabilities; the white space is the evidence-aware scheduler; window ~12–24 months.
