---
name: adversarial
description: Attack Multiverso's trust boundary — run the laundering corpus and then invent and execute NEW attacks that make a bad candidate win a race, forge evidence, or pass verification. Use after any change to oracles, policy, gates, backends, or the admission path, and before claiming any gate-coverage number.
---

# Attack the evidence

The governing rule (`docs/concepts.md`) is *"oracles outrank agents; agent output is a claim."* On 2026-08-15 a design partner study proved that rule was violated: the suite oracle ran inside the candidate's world and parsed a JUnit file the candidate could write. A ten-line `conftest.py` produced a DSSE-signed attestation certifying **500 passing tests in a three-test repository**, and `mvo verify` printed `OK`.

That is the standing lesson: **every cryptographic guarantee downstream faithfully certifies whatever number the adversary authored.** This skill exists so that never goes unnoticed again.

Read [`docs/studies/2026-08-design-partner-study.md`](../../../docs/studies/2026-08-design-partner-study.md) and `docs/design/M1f-trust-boundary.md` before starting.

## 1. Run the regression corpus

```bash
scripts/adversarial.sh --require-coverage allocation   # the default; spelled out so the assertion is visible
```

**Exit 5 is `VACUOUS`, and it is not a pass**: the verdicts matched the baseline and *no vector exercised the named facet*, so the run is evidence about the oracles and about nothing else. Do not reach for `--allow-vacuous` to turn it green — that flag declares a run that exercises nothing and stamps `VACUOUS` on every table, which then belongs in your report.

`testdata/adversarial/` holds one candidate patch per known laundering vector against a repo with a real bug. Report **per vector**, never as a count: an honest fix must win; every cheat must be caught, and caught *by a named gate*, not by luck. A cheat that loses on a ranking tiebreak is not caught — it is a coin flip that happened to land right.

## 2. Then attack it yourself

The corpus only contains attacks someone already thought of. Your job is the one nobody has. Work in a scratch directory outside the repo, build a small repo with a real bug, and try to make a wrong candidate win, or make a receipt say something false, or make `verify`/`audit` bless something they should not.

Attack surfaces worth probing, in rough order of how often they have paid off:

- **Anything that runs code the oracle trusts**: `conftest.py`, `sitecustomize.py`, `.pth` files, `PYTHONSTARTUP`, plugin entry points in `pyproject.toml`/`setup.cfg`, `pytest.ini` options, tox/nox config.
- **Artifact paths**: can the candidate write where the oracle reads, during the run or after it? Does the control plane hash the bytes it observed, or bytes it re-read from a path the candidate controls?
- **Consistent lying**: if two oracles cross-check each other, make both lie the same way.
- **Baseline poisoning**: `collected_delta` compares against a base-state measurement — can the candidate influence what the baseline measured?
- **Gate scope gaps**: put the cheat somewhere the protected-paths set does not cover.
- **Tier differences**: something blocked in T1 that works in T0, or vice versa.
- **Metric semantics**: skipped tests, xfail, empty parametrize, a suite that collects but does not execute.
- **The decision layer**: force a ranking tie so a tiebreak decides; make two worlds digest-collide in the ordinal display; race conditions under `--parallel`.
- **The trust layer**: delete or edit a CAS blob and see whether `audit` still says OK; sign with a different key and see whether `verify` distinguishes it without `--key`.

## Rules

- **Only report attacks you actually ran.** A theoretical attack is a hypothesis; this project's whole point is the difference. Paste the real output.
- **Never invoke a real agent CLI** (`claude`, `codex`) — they cost money. Use the script adapter with patches you write.
- Work outside the repo tree; never commit.
- A successful attack is a finding of the highest severity. Report it with a reproduction someone else can run.
- If you genuinely could not break something, say that too — but only after real attempts, and name what you tried. "I could not defeat the freshness/tree-env binding" is valuable precisely because the person saying it was trying to.

## Reporting

For each vector: **STOPPED** (a gate refuses it), **DETECTED** (it is recorded and visible but does not block), or **OPEN** (it wins). Anything OPEN must land in the user-facing gate-coverage table in `docs/concepts.md` and the README — this project states its gaps rather than discovering them in someone else's production.
