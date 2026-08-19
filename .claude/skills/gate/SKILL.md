---
name: gate
description: Run Multiverso's full verification gate — gofmt, vet, tests, race detector, both acceptance scripts, and the adversarial corpus — and report honestly, distinguishing real failures from flakes. Use before every commit, after every build block, and whenever you are about to claim something is green.
---

# The gate

Multiverso's claim to be about evidence quality is worth nothing if its own verification is sloppy. This skill runs the gate and reports what actually happened.

## Run it

From the repo root, in this order — stop and report at the first *real* failure rather than pushing through:

```bash
gofmt -l .                                    # must print nothing
go vet ./...
go test -count=1 ./...                        # -count=1 defeats the cache; a cached pass is not evidence
go test -race -count=1 ./internal/race/... ./internal/publish/...
scripts/accept.sh                             # end-to-end, must end with "accept: OK"
scripts/m0-accept.sh                          # the M0 wrapper, still must pass
scripts/adversarial.sh --require-coverage allocation   # the laundering corpus, if present
```

**The corpus has three outcomes, not two.** `--require-coverage allocation` is the shipped default of `scripts/adversarial.sh`, and it is spelled out above so that a reader of this file can see what the run asserts rather than having to trust a default:

| exit | meaning | what to report |
|---|---|---|
| 0 | verdicts match the recorded baseline **and** the named facet was exercised by ≥ 1 vector | green, with the `coverage:` line quoted |
| 1 | **drift** — a vector's verdict moved | a failure; name the moved rows |
| **5** | **`VACUOUS`** — every verdict matched and **nothing exercised the facet** | **not a pass.** The run measured nothing about any allocation rule |

Exit 5 is the gate this repository was missing: M2b.2 called the corpus its blocking gate and the corpus was passing `22/22` while 21 of 22 vectors carried no budget at all. **Never silence it with `--allow-vacuous` to obtain a green** — that flag exists to *declare* a run that exercises nothing (a `--arm fixed` run, for instance, exercises no allocation rule by construction), and it stamps `VACUOUS` on every table it prints. If you pass it, the word `VACUOUS` goes in your report too.

`go test ./...` skips docker-gated tests when no daemon is reachable. If docker IS available, they must run — a suite that silently skipped its container coverage is not green, it is untested. Check for `SKIP` lines and say so.

## Flake discipline

A test that fails once and passes on retry is **not** a pass. When something fails:

1. Re-run that test alone with `-count=3`. Passing 3/3 means the failure was flaky, not that it was imaginary.
2. Determine whether it is *our* flake (a race, a timing assumption, an async teardown asserted synchronously) or genuine breakage from the change under test. Check by stashing the working tree and re-running.
3. **Fix our flakes rather than tolerating them.** Precedent: `internal/backend/docker_test.go` asserted a container was gone immediately after `Close`, but `docker rm` returns before the daemon finishes reaping — the fix was to poll with a deadline, which is strictly stricter than the flaky version, not looser.
4. Never report a suite as green because a rerun happened to pass. Say "flaky, diagnosed as X, fixed" or "flaky, not yet diagnosed" — both are honest; "green" is not.

## Reporting

State plainly:
- which steps ran and which were skipped, and why
- any failure with the actual output, not a paraphrase
- for the adversarial corpus: per-vector caught / not caught, never a summary count that hides an uncaught vector
- for the adversarial corpus: the `coverage:` line beside the verdict count, always. `22/22` is a statement about the **oracles**; a facet exercised by no vector is a claim the run cannot support, and reporting the count without the coverage line is how "22/22" became evidence for an allocation rule it never touched

If you cannot run a step (no docker, no pytest, no network), say that the step did not run. An unrun check is not a passed check. This project publishes its gaps; the gate is where that habit starts.
