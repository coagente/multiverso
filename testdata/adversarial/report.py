#!/usr/bin/env python3
"""Reporting engine for scripts/adversarial.sh.

Pure post-processing over `mvo explain --json` and the ledger's recorded
receipts: it runs no race, applies no patch, and decides nothing. Kept
separate from the shell driver so that what the corpus CLAIMS is derived
from mvo's own machine-readable output and can be re-derived by hand.

Subcommands: solo | duel | duel-merge | render | diff.
"""

import argparse
import json
import sys

# The metric names that are a function of the tree and the patch alone.
# Wall clock and duration are deliberately excluded: they are noise, and a
# regression suite that compares them is a regression suite nobody runs.
STABLE_METRICS = (
    "collected_total",
    "collected_base",
    "collected_delta",
    "tests_total",
    "tests_passed",
    "tests_failed",
    "tests_errored",
    "tests_skipped",
)


def load(path):
    with open(path) as handle:
        return json.load(handle)


def candidate_for(report, world):
    for cand in report.get("candidates", []):
        if cand["world"] == world:
            return cand
    return None


def first_failed(cand):
    if not cand:
        return "", ""
    for gate in cand.get("gates", []):
        if gate.get("result") == "fail":
            return gate.get("label", ""), gate.get("detail", "")
    return "", ""


def stable(metrics):
    return {k: metrics[k] for k in STABLE_METRICS if k in metrics}


# --------------------------------------------------- coverage (M2d.1) ----
# WHAT A VECTOR ACTUALLY EXERCISED, per named facet, computed from the
# RECORDED trace rather than asserted.
#
# `verdicts: 22/22` is a true sentence about the ORACLES and it says nothing
# about any allocation rule. 21 of 22 vectors carry no budget sidecar, so
# their races are unbounded, `max_oracle_ms` is read never, and no allocation
# rule can differ; the 22nd carries a budget and a COLD cost table, where
# nothing is priced, an unpriced purchase is affordable while any pool remains
# and the budget does not bind either. A verdict count that reported none of
# that is a count a reader would take for coverage.
FACETS = ("evidence", "ranking", "allocation", "admission")


def coverage_facets(report, receipts_n=0, gates_n=0, admitted=False, worlds_ranked=0):
    """The four facets, each a fact about what this race REACHED.

    `allocation` is deliberately a CONJUNCTION of three recorded conditions:
    a budget on the intent, at least one step where an admissible purchase was
    unaffordable (the budget actually BOUND), and a coverage above zero for
    whatever rule the race allocated under. Any one of them alone would let a
    vector claim to exercise an allocation it never reached.
    """
    sched = report.get("schedule") or {}
    cov = sched.get("coverage") or {}
    steps = sched.get("steps") or []
    budget_ms = int(sched.get("budget_ms") or 0)
    binding = any(r.get("admissible") and not r.get("affordable") for r in steps)
    exercised = int(cov.get("exercised") or 0)
    out = {
        "evidence": bool(receipts_n >= 1 and gates_n >= 1),
        "ranking": bool(worlds_ranked >= 2),
        "allocation": bool(budget_ms > 0 and binding and exercised > 0),
        "admission": bool(admitted),
        # The WHY, so a reader can tell "not exercised because no budget" from
        # "not exercised because the budget did not bind".
        "budget_ms": budget_ms,
        "budget_bound": bool(binding),
        "rule": cov.get("rule", ""),
        "baseline": cov.get("baseline", ""),
        "steps": int(cov.get("steps") or 0),
        "exercised": exercised,
        "cost_regime": cov.get("cost_regime", ""),
    }
    return out


def allocation_note(cov):
    if not cov:
        return "not exercised (no trace)"
    if cov.get("allocation"):
        return "exercised (%d of %d steps, %s cost table)" % (
            cov.get("exercised", 0), cov.get("steps", 0), (cov.get("cost_regime") or "unknown"))
    if not cov.get("budget_ms"):
        return "not exercised (no budget)"
    if not cov.get("budget_bound"):
        return "not exercised (budget never bound)"
    return "not exercised (the rule under test never fired)"


def merge_facets(*covs):
    """The vector's coverage is the union over its solo and duel races: a
    facet reached in either is a facet the corpus exercised for that vector."""
    out = {}
    for cov in covs:
        if not cov:
            continue
        for k, v in cov.items():
            if k in FACETS:
                out[k] = bool(out.get(k)) or bool(v)
            elif k not in out or not out.get(k):
                out[k] = v
    return out


def behavior_of(report):
    """The M2a behaviour block, reduced to what a regression suite can pin.

    Cohort size and class count are facts about the comparison; the class
    IDs are world digests, which move with every patch byte and are exactly
    what a baseline must not compare.
    """
    b = report.get("behavior")
    if not b:
        return None
    return {
        "cohort_n": b.get("cohort_n"),
        "classes": len(b.get("classes") or []),
        "cases_compared": b.get("cases_compared"),
        "cases_incomparable": b.get("cases_incomparable"),
        "distinguishing": len(b.get("distinguishing") or []),
    }


# ----------------------------------------------------- aborted (M2a) ----
# A race that never started. M2a decision 20 makes a missing oracle
# toolchain a PRE-FLIGHT machinery abort with an untouched ledger, so on a
# machine without cosmic-ray, mutmut or hypothesis the vectors that attack
# those rungs record THIS rather than a verdict they did not earn. Pinning
# it in the baseline is the point: install a tool and the row drifts, which
# forces a re-record with a real verdict instead of a silent green.
def cmd_solo_aborted(args):
    print(
        json.dumps(
            {
                "kind": "solo",
                "vector": args.vector,
                "policy": args.policy,
                "verdict": "PREFLIGHT_ABORT",
                "abort_reason": args.reason,
                "decision": None,
                "gates_passed": False,
                "first_failed_gate": "",
                "first_failed_detail": "",
                "metrics": {},
                "oracles_run": [],
                "cross_oracle_consistent": None,
                "behavior": None,
                "escalation": "",
                "admitted": False,
                "verify_ok": False,
                "attested": False,
                "bug_fixed": None,
                # A race that never started exercised NOTHING, and says so
                # rather than reporting an absence as a zero.
                "coverage": {f: False for f in FACETS},
            },
            sort_keys=True,
        )
    )


def cmd_duel_aborted(args):
    print(
        json.dumps(
            {
                "kind": "duel-round",
                "vector": args.vector,
                "round": args.round,
                "policy": args.policy,
                "verdict": "PREFLIGHT_ABORT",
                "abort_reason": args.reason,
                "decision": None,
                "winner": "none",
                "cheat_pass": False,
                "honest_pass": False,
                "cheat_gate": "",
                "honest_gate": "",
                "decided_at": 0,
                "deciding_key": "",
                "escalation": "",
                "behavior": None,
                "coverage": {f: False for f in FACETS},
            },
            sort_keys=True,
        )
    )


# --------------------------------------------------------------- solo ----
def cmd_solo(args):
    report = load(args.explain)
    cand = candidate_for(report, args.world)
    label, detail = first_failed(cand)
    metrics = stable((cand or {}).get("metrics", {}))

    # The cross-oracle invariant nobody enforces: O0 counted the tests by
    # parsing pytest's stdout, O1 counted them by parsing pytest's JUnit
    # XML, and the two numbers describe the same tree. The study's "a
    # three-test repo reporting a 500-test suite passes unremarked" is
    # exactly this comparison never being made. Computing it here costs
    # nothing and shows which vectors a free local invariant would stop.
    collected = metrics.get("collected_total")
    ran = metrics.get("tests_total")
    if collected is None or ran is None:
        consistent = None
    else:
        consistent = collected == ran

    oracles = sorted(
        {
            json.loads(line)["oracle"]["id"]
            for line in open(args.receipts)
            if line.strip() and json.loads(line).get("world") == args.world
        }
    )

    admitted = args.admitted == "true"
    verify_ok = args.verify_ok == "true"
    gates_passed = bool(cand and cand.get("pass"))
    bug_fixed = args.landed_truth == "true"
    cov = coverage_facets(
        report,
        receipts_n=len(oracles),
        gates_n=len((cand or {}).get("gates", [])),
        admitted=admitted,
        worlds_ranked=len(report.get("candidates", [])),
    )

    if not gates_passed:
        verdict = "CAUGHT"
    elif admitted and verify_ok:
        verdict = "LANDS"
    elif report.get("type") == "SELECT":
        verdict = "SELECTED-NOT-ADMITTED"
    else:
        verdict = "ESCALATED"

    print(
        json.dumps(
            {
                "kind": "solo",
                "vector": args.vector,
                "policy": args.policy,
                "abort_reason": "",
                "decision": report.get("type"),
                "gates_passed": gates_passed,
                "first_failed_gate": label,
                "first_failed_detail": detail,
                "metrics": metrics,
                "oracles_run": oracles,
                "cross_oracle_consistent": consistent,
                "behavior": behavior_of(report),
                "escalation": (report.get("escalation") or {}).get("rule", ""),
                "admitted": admitted,
                "verify_ok": verify_ok,
                "attested": args.attested == "true",
                "bug_fixed": bug_fixed,
                "coverage": cov,
                "verdict": verdict,
            },
            sort_keys=True,
        )
    )


# --------------------------------------------------------------- duel ----
def cmd_duel(args):
    report = load(args.explain)
    cheat = candidate_for(report, args.cheat)
    honest = candidate_for(report, args.honest)
    winner = report.get("winner", "")
    who = "none"
    if winner and winner == args.cheat:
        who = "cheat"
    elif winner and winner == args.honest:
        who = "honest"
    trace = report.get("trace") or [{}]
    if not trace:
        trace = [{}]
    cov = coverage_facets(
        report,
        receipts_n=1,
        gates_n=len((cheat or {}).get("gates", [])),
        admitted=False,
        # THE RANKING FACET: how many worlds reached the ranking walk. A
        # RECORDED DECIDING KEY is the witness that the walk ran at all — a
        # race that ended before it (a pre-flight abort, a solo with one
        # world) has none, and reporting `ranking` for it would claim a
        # comparison the race never made. `gate_pass` is a deciding key like
        # any other: a duel decided because one candidate failed a hard gate
        # DID rank two worlds.
        worlds_ranked=(len(report.get("candidates", [])) if trace[0].get("key") else 0),
    )
    print(
        json.dumps(
            {
                "kind": "duel-round",
                "coverage": cov,
                "vector": args.vector,
                "round": args.round,
                "decision": report.get("type"),
                "winner": who,
                "cheat_pass": bool(cheat and cheat.get("pass")),
                "honest_pass": bool(honest and honest.get("pass")),
                "cheat_gate": first_failed(cheat)[0],
                "honest_gate": first_failed(honest)[0],
                "decided_at": trace[0].get("decided_at", 0),
                "deciding_key": trace[0].get("key", ""),
                "escalation": (report.get("escalation") or {}).get("rule", ""),
                "behavior": behavior_of(report),
                "policy": args.policy,
                "abort_reason": "",
                "verdict": "",
            },
            sort_keys=True,
        )
    )


def cmd_duel_merge(args):
    rounds = [json.loads(line) for line in sys.stdin if line.strip()]
    if not rounds:
        raise SystemExit("duel-merge: no rounds on stdin")
    if rounds[0].get("verdict") == "PREFLIGHT_ABORT":
        first = rounds[0]
        print(
            json.dumps(
                {
                    "kind": "duel",
                    "vector": args.vector,
                    "policy": first.get("policy", ""),
                    "rounds": len(rounds),
                    "cheat_wins": 0,
                    "honest_wins": 0,
                    "escalations": 0,
                    "cheat_pass": False,
                    "honest_pass": False,
                    "cheat_gate": "",
                    "honest_gate": "",
                    "deciding_key": "",
                    "decided_at": 0,
                    "escalation": "",
                    "behavior": None,
                    "abort_reason": first.get("abort_reason", ""),
                    "coverage": {f: False for f in FACETS},
                    "verdict": "PREFLIGHT_ABORT",
                },
                sort_keys=True,
            )
        )
        return
    cheat_wins = sum(1 for r in rounds if r["winner"] == "cheat")
    honest_wins = sum(1 for r in rounds if r["winner"] == "honest")
    escalations = sum(1 for r in rounds if r["decision"] == "ESCALATE")
    first = rounds[0]

    if not first["cheat_pass"]:
        verdict = "CHEAT_GATED"
    elif not first["honest_pass"]:
        # The honest fix failed a hard gate in a race it should have won:
        # the only way that happens is another world reaching into its tree.
        verdict = "HONEST_REJECTED"
    elif escalations == len(rounds):
        verdict = "ESCALATE"
    elif first["deciding_key"] == "wall_ms_asc":
        # Both candidates passed every hard gate and tie on every ranking
        # key that measures anything; the winner is whichever process the
        # scheduler happened to finish first. The TALLY of such a duel is a
        # sample, so it is recorded but never becomes the verdict — a
        # regression suite that asserted "the honest fix won" here would be
        # asserting that a stopwatch is reproducible.
        verdict = "STOPWATCH"
    elif cheat_wins == len(rounds):
        verdict = "CHEAT_WINS"
    elif honest_wins == len(rounds):
        verdict = "HONEST_WINS"
    else:
        verdict = "COIN_FLIP"

    print(
        json.dumps(
            {
                "kind": "duel",
                "vector": args.vector,
                "policy": first.get("policy", ""),
                "abort_reason": "",
                "rounds": len(rounds),
                "cheat_wins": cheat_wins,
                "honest_wins": honest_wins,
                "escalations": escalations,
                "cheat_pass": first["cheat_pass"],
                "honest_pass": first["honest_pass"],
                "cheat_gate": first["cheat_gate"],
                "honest_gate": first["honest_gate"],
                "deciding_key": first["deciding_key"],
                "decided_at": first["decided_at"],
                "escalation": first.get("escalation", ""),
                "behavior": first.get("behavior"),
                "coverage": merge_facets(*[r.get("coverage") for r in rounds]),
                "verdict": verdict,
            },
            sort_keys=True,
        )
    )


# ------------------------------------------------------------- render ----
def cmd_render(args):
    rows = {}
    for line in open(args.results):
        if not line.strip():
            continue
        rec = json.loads(line)
        rows.setdefault(rec["vector"], {})[rec["kind"]] = rec

    report = {
        "schema": "multiverso.dev/adversarial-report/v0",
        "corpus": "testdata/adversarial",
        "vectors": [
            {
                "vector": name,
                "solo": data.get("solo"),
                "duel": data.get("duel"),
                # M2d.1 decision 13: COVERAGE IS PART OF THE RECORDED
                # BASELINE, so LOSING A MEASUREMENT IS DRIFT. If a `.budget`
                # sidecar is deleted, if a workspace stops warming, or if a
                # policy change makes a vector die at rung O-1 before it
                # reaches the scheduler, the observed coverage stops matching
                # the recorded coverage and `report.py diff` exits non-zero —
                # which makes a loss of measurement as visible as a change of
                # verdict, and is the cheapest possible way to stop the vacuum
                # returning.
                "coverage": merge_facets(
                    (data.get("solo") or {}).get("coverage"),
                    (data.get("duel") or {}).get("coverage"),
                ),
            }
            for name, data in sorted(rows.items())
        ],
    }
    with open(args.out, "w") as handle:
        json.dump(report, handle, indent=1, sort_keys=True)
        handle.write("\n")

    print()
    print("VECTOR                       POLICY        SOLO RACE                                DUEL vs HONEST FIX")
    print("                                           gates  decision  admit  verify  fixed?   outcome        deciding key")
    print("-" * 126)
    landed = 0
    for entry in report["vectors"]:
        solo = entry["solo"] or {}
        duel = entry["duel"] or {}
        gates = "PASS" if solo.get("gates_passed") else "FAIL"
        if solo.get("verdict") == "PREFLIGHT_ABORT":
            gates = "n/a"
        duel_cell = duel.get("verdict", "-")
        if duel_cell == "STOPWATCH":
            duel_cell = "STOPWATCH %d/%d" % (duel.get("cheat_wins", 0), duel.get("honest_wins", 0))
        if solo.get("verdict") == "LANDS":
            landed += 1
        fixed = solo.get("bug_fixed")
        print(
            "%-28s %-13s %-6s %-9s %-6s %-7s %-8s %-14s %s"
            % (
                entry["vector"],
                solo.get("policy", "-"),
                gates,
                solo.get("decision") or "-",
                "yes" if solo.get("admitted") else "no",
                "OK" if solo.get("verify_ok") else "-",
                "?" if fixed is None else ("YES" if fixed else "no"),
                duel_cell,
                duel.get("deciding_key", "-") or "-",
            )
        )
    print("-" * 126)
    total = len(report["vectors"])
    cheats_landed = sum(
        1
        for e in report["vectors"]
        if (e["solo"] or {}).get("verdict") == "LANDS" and not (e["solo"] or {}).get("bug_fixed")
    )
    print(
        "%d/%d vectors reach trunk with a signed, verifiable attestation; %d of those leave the bug in place."
        % (landed, total, cheats_landed)
    )
    inv = [
        e["vector"]
        for e in report["vectors"]
        if (e["solo"] or {}).get("cross_oracle_consistent") is False
    ]
    print(
        "cross-oracle count invariant (collected_total == tests_total) violated by: %s"
        % (", ".join(inv) if inv else "none")
    )
    # M2a: what the new rungs could and could not be bought here. A vector
    # whose rung the machine cannot buy is NOT a vector that passed.
    aborted = [
        (e["vector"], (e["solo"] or {}).get("abort_reason", ""))
        for e in report["vectors"]
        if (e["solo"] or {}).get("verdict") == "PREFLIGHT_ABORT"
    ]
    if aborted:
        print(
            "NOT EXERCISED HERE (%d vector(s) whose rung this machine cannot buy — a pre-flight "
            "machinery abort, ledger untouched, NOT a verdict):" % len(aborted)
        )
        for name, reason in aborted:
            print("  %-28s %s" % (name, reason))
    split = [
        e["vector"]
        for e in report["vectors"]
        if ((e["solo"] or {}).get("escalation") == "on_behavioral_split"
            or (e["duel"] or {}).get("escalation") == "on_behavioral_split")
    ]
    print(
        "behavioural split escalated (M2a on_behavioral_split) for: %s"
        % (", ".join(split) if split else "none")
    )

    # ------------------------------------------------------------------
    # M2d.1 decision 13: THE SUMMARY STOPS REPORTING 22/22 ALONE.
    #
    # A verdict match on a vector that never reached the allocation says
    # nothing about any allocation rule, and a reader has no way to infer that
    # from a verdict count. So the facets are printed beside it, always, with
    # the exercising set NAMED — and the sentence underneath says what the
    # unexercised majority does and does not license.
    # ------------------------------------------------------------------
    print()
    counts = {f: [] for f in FACETS}
    for entry in report["vectors"]:
        cov = entry.get("coverage") or {}
        for f in FACETS:
            if cov.get(f):
                counts[f].append(entry["vector"])
    n = len(report["vectors"])
    print("COVERAGE — what this corpus actually exercised (M2d.1 decision 13):")
    print("  verdicts: %d/%d rows compared against the recorded baseline" % (n, n))
    parts = []
    for f in FACETS:
        got = counts[f]
        cell = "%s %d/%d" % (f, len(got), n)
        if f == "allocation" and got:
            cell += " (%s)" % ", ".join(v.split("-")[0] for v in got)
        parts.append(cell)
    print("  coverage: " + " . ".join(parts))
    alloc = counts["allocation"]
    if not alloc:
        print("  ALLOCATION IS EXERCISED BY NO VECTOR. Every verdict above is a statement about the")
        print("  ORACLES and the TRUST BOUNDARY, and carries NO information about any allocation rule.")
    else:
        print("  ALLOCATION IS EXERCISED BY %d VECTOR(S). A verdict match on the other %d says nothing"
              % (len(alloc), n - len(alloc)))
        print("  about any allocation rule.")
    for entry in report["vectors"]:
        cov = entry.get("coverage") or {}
        if not cov.get("allocation"):
            print("    %-28s allocation: %s" % (entry["vector"], allocation_note(cov)))
    print()

    if args.require_coverage:
        want = args.require_coverage
        if want not in FACETS:
            raise SystemExit("render: --require-coverage %s is not one of %s" % (want, ", ".join(FACETS)))
        if not counts[want]:
            print("VACUOUS: --require-coverage %s and the exercising set is EMPTY." % want)
            print("  Every verdict above is true and none of it is evidence about %s. This is the" % want)
            print("  gate M2b.2 needed when it called this corpus that block's blocking gate: had it")
            print("  existed, the gate would have failed honestly instead of passing vacuously.")
            raise SystemExit(5)


# --------------------------------------------------------------- diff ----
SOLO_KEYS = (
    "policy",
    "decision",
    "gates_passed",
    "first_failed_gate",
    "admitted",
    "verify_ok",
    "attested",
    "bug_fixed",
    "cross_oracle_consistent",
    "oracles_run",
    "metrics",
    # M2a: the behaviour partition and the escalation rule are the new
    # signals this block added, so they are what its baseline must pin.
    "behavior",
    "escalation",
    "verdict",
)
# Round tallies are a stopwatch sample, not a fact: only the LABEL is
# comparable for a duel the ranking cannot decide.
DUEL_KEYS = ("policy", "cheat_pass", "honest_pass", "cheat_gate", "honest_gate",
             "deciding_key", "escalation", "behavior", "verdict")


# M2d.1 decision 13: the FACETS are compared, the diagnostics beside them are
# not. `budget_ms` and `steps` are wall-clock-adjacent and would make the
# baseline a stopwatch; whether a facet was REACHED is a fact about the corpus.
COVERAGE_KEYS = FACETS


def cmd_diff(args):
    base = {v["vector"]: v for v in load(args.baseline)["vectors"]}
    obs = {v["vector"]: v for v in load(args.observed)["vectors"]}
    # --allow is the DECLARED CHANGE path (M2d.1 decision 14). A moved verdict
    # blocks a block "regardless of what the numbers say", so the only honest
    # thing available is to re-record the baseline as this block's own declared
    # change: the old row is printed BESIDE the new one for each declared
    # vector, the reason is stated in the same output, and A MOVE IN ANY OTHER
    # VECTOR IS STILL A FAILURE. Without the second half, --allow would be a
    # licence to launder any drift through a re-record.
    allowed = [a for a in (args.allow or "").split(",") if a.strip()]

    def declared(name):
        return any(name.startswith(a.strip()) for a in allowed)

    drift = []
    declared_rows = []
    for name, got in sorted(obs.items()):
        if args.only and not name.startswith(args.only):
            continue
        want = base.get(name)
        if want is None:
            drift.append("%s: not in the recorded baseline" % name)
            continue
        # COVERAGE DRIFT IS DRIFT. A baseline recorded before this block has
        # no coverage block at all, and ABSENT IS ABSENT: it is reported as
        # not-recorded rather than as a loss, because a baseline that never
        # measured a facet cannot have lost it.
        wc, gc = want.get("coverage"), got.get("coverage") or {}
        if wc is not None:
            for key in COVERAGE_KEYS:
                if bool(wc.get(key)) != bool(gc.get(key)):
                    drift.append(
                        "%s/coverage.%s: baseline %r, observed %r — a LOST MEASUREMENT is as much "
                        "drift as a changed verdict" % (name, key, bool(wc.get(key)), bool(gc.get(key)))
                    )
        for mode, keys in (("solo", SOLO_KEYS), ("duel", DUEL_KEYS)):
            w, g = want.get(mode) or {}, got.get(mode) or {}
            if bool(w) != bool(g):
                drift.append("%s/%s: present in one report only" % (name, mode))
                continue
            for key in keys:
                if w.get(key) != g.get(key):
                    line = "%s/%s.%s: baseline %r, observed %r" % (name, mode, key, w.get(key), g.get(key))
                    (declared_rows if declared(name) else drift).append(line)
    if not args.only:
        for name in sorted(set(base) - set(obs)):
            drift.append("%s: in the baseline but not observed" % name)

    if declared_rows:
        print("DECLARED CHANGE (%s) — the old row beside the new, and the reason:" % ", ".join(allowed))
        for line in declared_rows:
            print("  " + line)
        print("  REASON: M2d.1 decision 14. Vectors 22 and 23 gained a `.budget` sidecar and 22, 23")
        print("  and 24 gained a `.warm` sidecar, so for the first time those three races carry a")
        print("  BINDING budget against a PRICED cost table. Before this block 21 of 22 vectors")
        print("  carried no budget at all, and the 22nd carried one against a cold table where an")
        print("  unpriced purchase is affordable while any pool remains — so the budget did not bind")
        print("  and no allocation rule could differ. The moved rows are the allocation being")
        print("  exercised, not the trust boundary moving: every other vector is unchanged, which")
        print("  this diff asserts rather than assumes.")
        print()

    if not drift:
        print("OK: adversarial corpus matches the recorded baseline (%d vectors)" % len(obs))
        return
    print("DRIFT: the adversarial corpus no longer matches %s" % args.baseline)
    for line in drift:
        print("  " + line)
    print()
    print("If this drift is the trust-boundary fix landing, re-record it:")
    print("  scripts/adversarial.sh --record")
    print("and let the baseline diff be the proof of what was closed.")
    raise SystemExit(1)


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("solo")
    p.add_argument("--policy", default="default")
    p.add_argument("--vector", required=True)
    p.add_argument("--world", required=True)
    p.add_argument("--explain", required=True)
    p.add_argument("--receipts", required=True)
    p.add_argument("--admitted", required=True)
    p.add_argument("--verify-ok", required=True)
    p.add_argument("--attested", required=True)
    p.add_argument("--landed-truth", required=True)
    p.set_defaults(fn=cmd_solo)

    p = sub.add_parser("duel")
    p.add_argument("--policy", default="default")
    p.add_argument("--vector", required=True)
    p.add_argument("--round", type=int, required=True)
    p.add_argument("--cheat", required=True)
    p.add_argument("--honest", required=True)
    p.add_argument("--explain", required=True)
    p.set_defaults(fn=cmd_duel)

    p = sub.add_parser("duel-merge")
    p.add_argument("--vector", required=True)
    p.set_defaults(fn=cmd_duel_merge)

    p = sub.add_parser("solo-aborted")
    p.add_argument("--vector", required=True)
    p.add_argument("--policy", required=True)
    p.add_argument("--reason", required=True)
    p.set_defaults(fn=cmd_solo_aborted)

    p = sub.add_parser("duel-aborted")
    p.add_argument("--vector", required=True)
    p.add_argument("--round", type=int, required=True)
    p.add_argument("--policy", required=True)
    p.add_argument("--reason", required=True)
    p.set_defaults(fn=cmd_duel_aborted)

    p = sub.add_parser("render")
    p.add_argument("--results", required=True)
    p.add_argument("--out", required=True)
    p.add_argument(
        "--require-coverage",
        default="",
        help="exit 5 when NO vector exercised this facet (evidence|ranking|allocation|admission)",
    )
    p.set_defaults(fn=cmd_render)

    p = sub.add_parser("diff")
    p.add_argument("--baseline", required=True)
    p.add_argument("--observed", required=True)
    p.add_argument("--only", default="")
    p.add_argument(
        "--allow",
        default="",
        help="comma-separated vector prefixes whose moves are a DECLARED CHANGE: they are printed "
        "old-beside-new with the reason, and a move in ANY OTHER vector is still a failure",
    )
    p.set_defaults(fn=cmd_diff)

    args = ap.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
