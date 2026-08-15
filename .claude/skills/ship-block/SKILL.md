---
name: ship-block
description: Build and ship one Multiverso milestone block end to end — architect a binding contract, build it, integrate, review adversarially, fix, log, commit. Use when starting any substantial piece of work from the PRD roadmap (a milestone block, a new plane, a new oracle).
---

# Ship a block

Every block of M0 and M1 was built this way. The adversarial review stage is not ceremony: it caught a process-group kill that let orphaned agents keep spending, secrets in a `docker exec` argv, a `git ls-remote` suffix match that could delete real branches, and an empty-gate policy that let an ADMIT be signed and landed on the strength of no gate at all. None of those came from the builders; all came from someone paid to attack the result.

Use the Workflow tool. The stages:

## 1. Design — one architect, one binding contract

The architect reads the PRD requirements in scope, the relevant research chapters, and **every prior design doc** (conventions live there), then writes `docs/design/<block>.md` in the established style: numbered decisions each with rationale, wire schemas with JSON tags, Go signatures, CLI grammar, ledger event payloads, acceptance steps, a testing bar, and an explicit **NOT** list.

The contract is binding for everything downstream. Where it and a builder disagree, the contract wins — unless the builder proves it wrong, in which case amend the doc in the same pass. Resolve every open question in the doc rather than leaving it for the builder; a builder facing an ambiguity will invent an answer and nobody will know.

## 2. Build — parallel where the seams are clean

Split along package boundaries the contract already separates, and tell each builder explicitly what it does *not* own. Builders code against the contract's signatures for packages being written in parallel.

Standing constraints: stdlib-only Go; never modify `go.mod`/`go.sum`; never commit or push; leave the tree dirty for the integrator; `gofmt -w`, `go vet` and `go test` green for your own packages before finishing; **never invoke a real agent CLI in tests** (fixtures exist in `testdata/fakeagent/`).

## 3. Integrate — one agent owns "it all works together"

Wire the pieces, run the whole gate (see the `gate` skill), fix cross-package mismatches, and report every fix. Prefer changing callers to match the contract; fix the violator when a package deviated.

## 4. Review — at least two lenses, one of them hostile

Reviewers are read-only and report only **real** defects with file paths. Lenses that have paid off:

- **domain-specific attack**: concurrency and isolation for a parallelism block, crypto and binding for a signing block, process control for an adapter block, trust and publication safety for a forge block
- **contract conformance + regressions**: diff the implementation against every normative statement; verify earlier milestones still pass; flag over-engineering — a walking skeleton does not need a plugin registry

For anything touching evidence, oracles or gates, make one reviewer a **red team that actually runs attacks** rather than reading code (see the `adversarial` skill).

## 5. Fix — verify before applying

The fixer verifies each finding against the code first: reviewers are wrong sometimes, and a written "skipped because X" is a fine outcome. A red-team attack that actually ran and succeeded outranks the contract and must be fixed or documented as an open vector. Then the full gate again.

## 6. Log and ship

- `BUILDLOG.md` entry at the top, in the voice of the existing entries: what shipped, the design decisions worth reading, **what the review caught**. Write it for someone deciding whether to trust this project, not for a changelog.
- Commit with a message that explains the *why*, naming what review found. Push. Check CI.
- If the block changed what users see, the docs change in the same pass — and a doc claim you have not run is a doc bug (see `design-partner`).

## The habit that matters

State gaps rather than hiding them. Every honest PARTIAL, published null result and un-caught attack vector in this repo is a deposit; a single overclaim spends the balance. The one hostile evaluator we exposed to the docs called the Status table the most honest thing he had read from a vendor — that is the asset being compounded here.
