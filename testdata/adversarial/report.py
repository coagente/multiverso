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
                "decision": report.get("type"),
                "gates_passed": gates_passed,
                "first_failed_gate": label,
                "first_failed_detail": detail,
                "metrics": metrics,
                "oracles_run": oracles,
                "cross_oracle_consistent": consistent,
                "admitted": admitted,
                "verify_ok": verify_ok,
                "attested": args.attested == "true",
                "bug_fixed": bug_fixed,
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
    print(
        json.dumps(
            {
                "kind": "duel-round",
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
            },
            sort_keys=True,
        )
    )


def cmd_duel_merge(args):
    rounds = [json.loads(line) for line in sys.stdin if line.strip()]
    if not rounds:
        raise SystemExit("duel-merge: no rounds on stdin")
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
            {"vector": name, "solo": data.get("solo"), "duel": data.get("duel")}
            for name, data in sorted(rows.items())
        ],
    }
    with open(args.out, "w") as handle:
        json.dump(report, handle, indent=1, sort_keys=True)
        handle.write("\n")

    print()
    print("VECTOR                       SOLO RACE                                DUEL vs HONEST FIX")
    print("                             gates  decision  admit  verify  fixed?   outcome        deciding key")
    print("-" * 108)
    landed = 0
    for entry in report["vectors"]:
        solo = entry["solo"] or {}
        duel = entry["duel"] or {}
        gates = "PASS" if solo.get("gates_passed") else "FAIL"
        duel_cell = duel.get("verdict", "-")
        if duel_cell == "STOPWATCH":
            duel_cell = "STOPWATCH %d/%d" % (duel.get("cheat_wins", 0), duel.get("honest_wins", 0))
        if solo.get("verdict") == "LANDS":
            landed += 1
        print(
            "%-28s %-6s %-9s %-6s %-7s %-8s %-14s %s"
            % (
                entry["vector"],
                gates,
                solo.get("decision", "-"),
                "yes" if solo.get("admitted") else "no",
                "OK" if solo.get("verify_ok") else "-",
                "YES" if solo.get("bug_fixed") else "no",
                duel_cell,
                duel.get("deciding_key", "-") or "-",
            )
        )
    print("-" * 108)
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
    print()


# --------------------------------------------------------------- diff ----
SOLO_KEYS = (
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
    "verdict",
)
# Round tallies are a stopwatch sample, not a fact: only the LABEL is
# comparable for a duel the ranking cannot decide.
DUEL_KEYS = ("cheat_pass", "honest_pass", "cheat_gate", "honest_gate", "deciding_key", "verdict")


def cmd_diff(args):
    base = {v["vector"]: v for v in load(args.baseline)["vectors"]}
    obs = {v["vector"]: v for v in load(args.observed)["vectors"]}
    drift = []
    for name, got in sorted(obs.items()):
        if args.only and not name.startswith(args.only):
            continue
        want = base.get(name)
        if want is None:
            drift.append("%s: not in the recorded baseline" % name)
            continue
        for mode, keys in (("solo", SOLO_KEYS), ("duel", DUEL_KEYS)):
            w, g = want.get(mode) or {}, got.get(mode) or {}
            if bool(w) != bool(g):
                drift.append("%s/%s: present in one report only" % (name, mode))
                continue
            for key in keys:
                if w.get(key) != g.get(key):
                    drift.append(
                        "%s/%s.%s: baseline %r, observed %r" % (name, mode, key, w.get(key), g.get(key))
                    )
    if not args.only:
        for name in sorted(set(base) - set(obs)):
            drift.append("%s: in the baseline but not observed" % name)

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
    p.add_argument("--vector", required=True)
    p.add_argument("--round", type=int, required=True)
    p.add_argument("--cheat", required=True)
    p.add_argument("--honest", required=True)
    p.add_argument("--explain", required=True)
    p.set_defaults(fn=cmd_duel)

    p = sub.add_parser("duel-merge")
    p.add_argument("--vector", required=True)
    p.set_defaults(fn=cmd_duel_merge)

    p = sub.add_parser("render")
    p.add_argument("--results", required=True)
    p.add_argument("--out", required=True)
    p.set_defaults(fn=cmd_render)

    p = sub.add_parser("diff")
    p.add_argument("--baseline", required=True)
    p.add_argument("--observed", required=True)
    p.add_argument("--only", default="")
    p.set_defaults(fn=cmd_diff)

    args = ap.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
