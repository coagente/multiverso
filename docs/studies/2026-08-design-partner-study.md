# Design partner study — three partners, three "no"s, one architectural hole

> Run 2026-08-15 against the M1 tree. Three emulated design partners with distinct personas each built their own repo and attempted adoption reading **only** `README.md`, `docs/quickstart.md` and `docs/concepts.md` — no source, no design docs, no PRD. A fourth agent then verified every claim against the code before anything here was written down. This is M1 acceptance criterion 3.
>
> **Criterion 3: NOT MET.** Three of three partners completed `init → intent → race → explain` on their own repos, but each needed **three** support interactions against a bar of one — and all three declined adoption.

## The finding that matters

**The candidate writes its own evidence.**

`docs/concepts.md` states the governing rule of the whole product: *"Oracles outrank agents. Agent output is a claim. Only oracle receipts advance lifecycle state."* But the suite oracle runs pytest **inside the candidate's world**, writing its JUnit XML **into the candidate's own writable tree**, and mvo content-addresses that file after the process exits. The untrusted party authors the evidence.

Reproduced end to end in about ten lines. A candidate patch whose entire content is a `conftest.py` that sets `session.exitstatus = 0` in `pytest_sessionfinish` and writes a fabricated `junit.xml` at `atexit`:

- passed all three default hard gates — `pass (total=3) | pass (delta=+0) | pass | PASS`
- produced a receipt reading `tests_passed=500, tests_total=500` **in a three-test repository**
- won the race and landed on trunk, its diff containing nothing but the cheat, the real bug untouched
- and then `mvo verify HEAD` printed `OK: attestation verified (7 checks)` and `mvo audit` printed `OK: 31 events, 3 decisions replayed`

The admission re-gate — the primitive the skeptic went in trying to break and could not — blessed it too, because the same `conftest.py` is in the landing tree. The existing scrub (`internal/oracle/pytest.go`) defends only against a *pre-planted* artifact; it does nothing about a process that writes the file during or after its own run.

So DSSE, in-toto, the content-addressed closure, publication, `fetch-race` — every one of them faithfully certifies a number the adversary wrote. Content-addressing an artifact "before parsing" is worth nothing when the adversary authored the bytes. The skeptic put it better than we would have:

> CI's green check is a rumour everyone knows is a rumour. Multiverso signs the rumour and hands it to a reviewer who has been told oracles outrank agents.

**This is the trust boundary being in the wrong place, and it is the whole product, not a backlog item.** It is the next block of work.

## The other verified criticals

**The default ranking decides by stopwatch.** The shipped default is `[gate_pass, tests_passed_desc, wall_ms_asc]` with `on_ranking_tie` off. In a repo with a real off-by-one bug, three candidates — skip the failing tests, weaken the assertions, and the honest fix — **all three passed every hard gate**. Across ten identical races the assertion-weakening cheat won 6 times and the honest fix 4, separated by ~100ms of wall-clock jitter, each time with a confident signed rationale naming `wall_ms_asc (3809 < 3922)` as the decisive key. A tool that ships the wrong patch 40% of the time *while printing a signature* is worse than no tool, because the maintainer stops reading the diff.

**The gate ladder catches one laundering vector out of five.** Test deletion is caught (well — `collected_delta=-2` before the suite oracle even runs). Skipping failing tests, weakening assertions, rewriting assertions to match the candidate's own wrong output, and forging the artifact all pass cleanly. `tests_skipped` is recorded in every suite receipt and **no gate in the closed vocabulary can read it.**

**The private signing key can enter git history by following our own documented recovery step.** `mvo init` edited the *tracked* `.gitignore` and never committed it; the quickstart's remedy for the resulting "working tree lags main" warning was `git reset --hard`, which reverts that line; the next ordinary `git add -A && git commit` commits `.multiverso/`, and `git show HEAD:.multiverso/keys/local.key` returns the unencrypted PKCS#8 private key. Six commands, documented steps only. **Fixed in this pass** — the rule now goes to the untracked `.git/info/exclude`, which `reset --hard` cannot revert.

And the reason we never saw it: `testdata/toyrepo/.gitignore` already contains `.multiverso/`, so `mvo init` changes nothing there. **Our fixture was structurally immune to our worst bug.** A fixture that cannot fail the way real repos fail certifies the docs, not the product.

## What survived contact

Worth recording, because it is the reason to keep going. The skeptic went in expecting "CI with extra steps plus a blockchain-shaped hash chain" and reported his going-in thesis was wrong:

- **Admission re-gating is real and CI cannot do it.** He raced an honest fix, landed a colleague's new contract test on trunk, then admitted — and got `REJECT: landing gates [collect-nonempty@collect …]`. A green GitHub check would have waved it through.
- **The freshness / tree-env binding held.** He could not make a receipt speak for a tree it was not produced from, nor make a decision replay differently than recorded.
- **The publication path beat all three tamper attempts**, each time with an error naming the file and the reason. `fetch-race` verified a race in a bare clone from a public key alone.
- The vocabulary — `n/a` is not a failure, "tie-break only, not a decision" — was called "the work of people who thought hard about not laundering ambiguity."

Nobody rejected the concept. Every objection was in the shell: key handling, evidence integrity, the default policy, and honesty about what is not covered.

## What was fixed in this pass

Fifteen documentation and CLI-ergonomics fixes, gate green. The load-bearing ones:

- the ignore rule moved to `.git/info/exclude`, closing the key-disclosure chain
- `docs/quickstart.md` had two **factually false** sentences about what `mvo audit` checks — corrected, plus a new section stating plainly what it does *not* inspect
- a new "what the gate ladder catches, and what it does not" table in `docs/concepts.md` and the README, populated from the five verified laundering vectors
- `--key` documented as **the** trust anchor rather than a convenience, with the verified rogue-clone scenario: a fresh workspace signing with its own key produces a visually identical `OK: attestation verified (7 checks)`
- a new "Protected trunks and merge queues" section stating the fact that decides the question: **an attestation survives a fast-forward of the exact admitted commit and nothing else.** A rebase invalidates the subject check; a squash strips the trailers
- `mvo audit` on an empty workspace no longer prints a bare `OK` — it says nothing was verified
- an ESCALATE no longer renders like a win

## What is explicitly not fixed here

The four criticals above are architecture and product-default changes, not doc fixes, and they are the next block of work rather than a patch. Also outstanding: `mvo audit` performs no integrity sweep over CAS (an attestation blob can be deleted and audit still reports OK); no cross-oracle invariant (a three-test repo reporting a 500-test suite passes unremarked); world digests are nondeterministic across runs; and there is no way to supply or pin a signing key, which blocks any CI deployment.

Full verified bug list with reproductions: 14 entries, 4 critical, in the triage output archived with this study.
