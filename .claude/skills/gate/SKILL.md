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
scripts/adversarial.sh                        # the laundering corpus, if present
```

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

If you cannot run a step (no docker, no pytest, no network), say that the step did not run. An unrun check is not a passed check. This project publishes its gaps; the gate is where that habit starts.
