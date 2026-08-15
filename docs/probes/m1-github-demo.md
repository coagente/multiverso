# M1 acceptance criterion 4: end-to-end against a real GitHub remote

> Executed 2026-08-15 against [coagente/multiverso-ref-probe](https://github.com/coagente/multiverso-ref-probe) with the M1e binary. This is the criterion the PRD states as: *"Candidates for one intent visible under `refs/multiverso/*` on a GitHub remote; admission lands via fast-forward push with a valid, verifiable receipt trailer (`mvo verify` passes)."* — **PASSED**, with one honest caveat below.

## The scenario

A real repo with a real bug: `median()` returns the upper element for even-length inputs, and a test suite that catches it. Two candidate patches, both generated as real `git diff` output:

- **patch-a** — fixes the function.
- **patch-b** — deletes the failing test. The classic way an agent "passes" a suite.

## What happened

```
$ mvo race <intent> --patches patches
SELECT mv0:5a22dd01… (decision mv0:f1c7e42e…, 2 worlds)
```

`mvo explain` renders exactly why:

```
gates (ordered, first failure stops the ladder):
  RANK  WORLD         ORD  collect-nonempty@collect  collected-not-below@collect               status-pass@suite  RESULT
  1     mv0:5a22dd01…  1    pass (total=2)            pass (delta=+0)                           pass               PASS
  2     mv0:01384362…  2    pass (total=1)            FAIL (collected_delta=-1 (tolerance -0))  n/a                FAIL

why mv0:5a22dd01… won  (ranking [gate_pass, tests_passed_desc, wall_ms_asc, world_digest_asc]):
  vs mv0:01384362… (rank 2): decided at key 1 gate_pass (pass > fail)
```

**The test-deleting candidate never reached the suite oracle.** `collected-not-below` is ordered before `status-pass`, so the collected-count guard (research ch. 19's laundering vector) stopped it at `collected_delta=-1`, and the `n/a` in the suite column is the ladder short-circuiting — evidence not wasted on a candidate already disqualified.

Admission, on the trunk that had meanwhile been pushed:

```
$ mvo admit <intent>
ADMIT mv0:5a22dd01…
commit:      65e14301f088ca3e32a49182aff4cab51a87b69e
attestation: sha256:39f74268d05bf4a4249f88a1c4ef497d0b5a68ec0cabd670277e4a921e31d0b6

$ git log -1 --format=%b
Multiverso-Intent: mv0:000eaa0aaa79…
Multiverso-Decision: mv0:0ca7f057b05a…
Multiverso-Attestation: sha256:39f74268d05b…

$ mvo verify 65e14301f088ca3e32a49182aff4cab51a87b69e
OK: attestation verified (7 checks)
```

Publication to the real remote, then verification from a **different clone that has never seen the workspace**, holding nothing but the public key:

```
$ mvo publish <intent> --remote origin
published refs/multiverso/intent/000eaa0aaa79 to origin (2 pushed, 0 up-to-date, 0 removed)

$ git ls-remote origin 'refs/multiverso/*'
a47c8552…  refs/multiverso/intent/000eaa0aaa79/cand/1
c7fde3a0…  refs/multiverso/intent/000eaa0aaa79/evidence

# fresh clone, second "machine"
$ mvo fetch-race 000eaa0aaa79 --remote origin --key trusted.pub
intent:    mv0:000eaa0aaa79… (Fix median for even-length inputs)
decision:  mv0:f1c7e42e… SELECT
winner:    mv0:5a22dd01…
admitted:  yes
freshness: STALE (main advanced past base 95e912ff9912)
ORDINAL  WORLD          OUTCOME    GATE                         SIGNED  REF
1        mv0:5a22dd01…  COMPLETED  pass                         5       cand/1
-        mv0:01384362…  COMPLETED  collected-not-below@collect  1       -
OK: race verified (15 items, 2 refs)
```

Fifteen items authenticated, both decisions replayed through the same pure `Decide` the publisher ran, and the freshness line honestly reports STALE because trunk moved past the intent's base after admission.

## Caveats worth stating

- **Admission wrote to an unprotected branch.** This is the M1 assumption the PRD already records (FI-1): protected-trunk gating needs the GitHub App check run, which is M3. A design partner needs either a bot identity with ruleset bypass or an integration branch.
- **The first attempt failed, and that was informative.** Hand-written patches with a wrong `@@` context count produced `CONFIG_ERROR` worlds — recorded as evidence, with `mvo explain` naming `outcome=CONFIG_ERROR` as the gate failure rather than silently dropping the candidate. Agent failure is evidence; machinery failure is an error. The distinction held.
- Both candidates here came from scripted patches, not live agents, to keep the demo free of API spend. The adapter path has its own coverage (M1b fixtures + the `MVO_LIVE_AGENT_TEST` smoke).
