#!/usr/bin/env bash
# M2b comparison harness — the SAME fixture raced under the ADAPTIVE
# scheduler and under the FIXED M1 ladder at MATCHED TOTAL BUDGET, with both
# outcomes and both costs reported side by side.
#
# WHY A SHELL SCRIPT AND NOT A GO HARNESS. Three reasons, and the third is
# the one that decides it.
#
#  1. The comparison has to run through the BINARY. What M2d compares is two
#     arms of `mvo race`, including intent creation, policy pinning, the
#     ledger, and `mvo explain --schedule`'s derived waste number. A Go
#     harness calling race.Run() in-process would skip the CLI surface that
#     the arms actually differ on, and would be measuring a code path no
#     operator runs.
#  2. It is the repository's existing shape for end-to-end evidence:
#     scripts/accept.sh and scripts/adversarial.sh already drive the built
#     binary against a real git repo and assert over the ledger with
#     python3. A second, parallel harness idiom would double the maintenance
#     of the fixture setup without buying anything.
#  3. The arms must be able to differ in FLAG SPELLING without a rebuild.
#     The adaptive scheduler's flags are landing in a sibling change; this
#     harness takes them as parameters (--adaptive-flag / --fixed-flag /
#     --budget-flag) so a rename is a command-line edit rather than a code
#     edit, and so the harness can be pointed at any future arm.
#
# WHAT "MATCHED BUDGET" MEANS HERE. PRD §11's protocol is budget-matched
# arms: the same money, allocated differently. So the FIXED arm runs first at
# an unbounded budget and its total oracle spend S is MEASURED; the ADAPTIVE
# arm then runs with max_oracle_ms = S. Anything else compares two different
# amounts of money and calls the difference a scheduling result. --budget MS
# pins S explicitly for both arms instead, which is what a sweep wants.
#
# WHAT IT REPORTS. Per arm: the decision type, the subject, the pass count,
# the receipts bought (per world and rung), and Σ cost.wall_ms. For the
# adaptive arm additionally: the stop clause, the purchases DECLINED with
# their reasons, and evidence waste. Then the comparison verdict — same
# decision or not, Δ spend, Δ receipts — because a scheduler that saves
# money by deciding something else has not saved money.
#
# NO REAL AGENT CLI IS EVER INVOKED (M1b rule): the script adapter replays
# the two committed patches in testdata/toyrepo/patches. No network. POSIX.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ADAPTIVE_FLAG="--schedule=adaptive"
FIXED_FLAG="--schedule=fixed"
BUDGET_FLAG="--budget-oracle-ms"
POLICY=""          # "" = the workspace default the `mvo init` writes
PATCHES="patches"  # under testdata/toyrepo
BUDGET=""          # "" = measure the fixed arm and match it
PARALLEL=1
KEEP=""
JSON=0

usage() {
  cat >&2 <<'EOF'
usage: scripts/schedule-compare.sh [options]

  --budget MS            matched total oracle budget for BOTH arms
                         (default: measure the fixed arm's spend and match it)
  --policy NAME|DIGEST   pin a policy for both arms (default: workspace default)
  --patches DIR          patch directory under testdata/toyrepo (default: patches)
  --parallel N           dispatch degree for both arms (default: 1)
  --adaptive-flag FLAG   how to ask this binary for the adaptive arm
                         (default: --schedule=adaptive)
  --fixed-flag FLAG      how to ask for the fixed M1 ladder
                         (default: --schedule=fixed)
  --budget-flag FLAG     intent flag carrying max_oracle_ms
                         (default: --budget-oracle-ms)
  --keep DIR             keep both workspaces in DIR instead of a temp dir
  --json                 emit the comparison as one JSON object
  -h, --help             this message

exit codes: 0 compared, 1 a run or an assertion failed, 2 usage,
            77 SKIP — this binary has no adaptive scheduler to compare
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --budget) BUDGET="${2:?--budget needs a value}"; shift 2 ;;
    --policy) POLICY="${2:?--policy needs a value}"; shift 2 ;;
    --patches) PATCHES="${2:?--patches needs a value}"; shift 2 ;;
    --parallel) PARALLEL="${2:?--parallel needs a value}"; shift 2 ;;
    --adaptive-flag) ADAPTIVE_FLAG="${2:?}"; shift 2 ;;
    --fixed-flag) FIXED_FLAG="${2:?}"; shift 2 ;;
    --budget-flag) BUDGET_FLAG="${2:?}"; shift 2 ;;
    --keep) KEEP="${2:?--keep needs a directory}"; shift 2 ;;
    --json) JSON=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "schedule-compare: unknown option $1" >&2; usage; exit 2 ;;
  esac
done

fail() { echo "FAIL: $*" >&2; exit 1; }

if [ -n "$KEEP" ]; then
  WORK="$KEEP"; mkdir -p "$WORK"
else
  WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
fi

MVO="$WORK/mvo"
(cd "$ROOT" && go build -o "$MVO" ./cmd/mvo)

# --- capability probe -------------------------------------------------------
# The arms are flags on `mvo race` and one flag on `mvo intent new`. If this
# binary has none of them there is nothing to compare, and the honest answer
# is SKIP with the reason — not a fabricated comparison of one arm against
# itself, and not a green run that hides a missing feature.
RACE_HELP="$("$MVO" race --help 2>&1 || true)"
INTENT_HELP="$("$MVO" intent new --help 2>&1 || true)"
missing=""
# Go's flag package renders `-name` in its usage even for flags spelled
# `--name` on the command line, so the probe matches on the BARE NAME. A
# probe that insisted on the double dash would report every flag missing and
# SKIP a binary that has them all.
adapt_name="${ADAPTIVE_FLAG%%=*}"; adapt_name="${adapt_name#--}"; adapt_name="${adapt_name#-}"
fixed_name="${FIXED_FLAG%%=*}";    fixed_name="${fixed_name#--}";  fixed_name="${fixed_name#-}"
budget_name="${BUDGET_FLAG#--}";   budget_name="${budget_name#-}"
case "$RACE_HELP"   in *"$adapt_name"*)  ;; *) missing="$missing ${ADAPTIVE_FLAG%%=*}" ;; esac
case "$RACE_HELP"   in *"$fixed_name"*)  ;; *) missing="$missing ${FIXED_FLAG%%=*}" ;; esac
case "$INTENT_HELP" in *"$budget_name"*) ;; *) missing="$missing ${BUDGET_FLAG}" ;; esac
if [ -n "$missing" ]; then
  cat >&2 <<EOF
schedule-compare: SKIP — this binary does not expose the arms to compare.
  missing:$missing
  The adaptive scheduler's CLI surface (mvo race $ADAPTIVE_FLAG / $FIXED_FLAG and
  mvo intent new $BUDGET_FLAG) is not present in this build. Re-run once it
  lands, or pass --adaptive-flag/--fixed-flag/--budget-flag if it is spelled
  differently.
EOF
  exit 77
fi

GIT="git -c user.name=m2b -c user.email=m2b@example.invalid"

# One repository per arm. Separate workspaces are not fastidiousness: the
# cost table is fitted from the workspace's own receipts, so running the
# second arm in the first arm's workspace would let arm 1's receipts move
# arm 2's cost model — the two runs would differ by more than the thing
# being compared.
setup_repo() {
  local dir="$1"
  mkdir -p "$dir"
  cp -R "$ROOT/testdata/toyrepo/." "$dir/"
  rm -rf "$dir/__pycache__" "$dir/.pytest_cache"
  $GIT -C "$dir" init -q -b main
  $GIT -C "$dir" add -A
  $GIT -C "$dir" commit -qm "toyrepo baseline"
  "$MVO" init --dir "$dir" >/dev/null
  # A named fixture policy is an AUTHORED FILE until a verb installs it, so
  # --policy NAME has nothing to resolve until it is in the workspace. Both
  # arms get the same one, from the same bytes, which is what makes the
  # comparison a comparison.
  if [ -n "$POLICY" ] && [ -f "$dir/policies/$POLICY.json" ]; then
    cp "$dir/policies/$POLICY.json" "$dir/.multiverso/policies/$POLICY.json"
  fi
}

new_intent() {
  local dir="$1" title="$2" budget="$3"
  local args=(intent new --dir "$dir" --title "$title" --desc "schedule-compare")
  [ -n "$POLICY" ] && args+=(--policy "$POLICY")
  [ -n "$budget" ] && args+=("$BUDGET_FLAG" "$budget")
  "$MVO" "${args[@]}"
}

run_arm() {
  local dir="$1" intent="$2" armflag="$3"
  "$MVO" race "$intent" --dir "$dir" --agent script \
    --patches "$dir/$PATCHES" --parallel "$PARALLEL" "$armflag" >/dev/null
}

# arm_report <dir> <intent> -> one JSON object on stdout, assembled from the
# LEDGER and from `mvo explain --schedule --json`. Nothing here re-derives a
# number the binary already recorded.
arm_report() {
  local dir="$1" intent="$2" arm="$3"
  local explain
  explain="$("$MVO" explain "$intent" --dir "$dir" --json --schedule)"
  ARM="$arm" INTENT="$intent" EXPLAIN="$explain" python3 - "$dir" <<'PY'
import json, os, sqlite3, sys

repo = sys.argv[1]
arm  = os.environ["ARM"]
rep  = json.loads(os.environ["EXPLAIN"])

db = sqlite3.connect(os.path.join(repo, ".multiverso", "ledger.db"))
rows = db.execute("SELECT type, cast(payload AS text) FROM events ORDER BY seq").fetchall()

receipts = [json.loads(p) for t, p in rows if t == "receipt.recorded"]
# Σ cost.wall_ms over every receipt the race bought: the money spent, taken
# from the receipts themselves rather than from the scheduler's own running
# total, so the two arms are measured by the same yardstick even though only
# one of them keeps a budget.
spend = sum(int(r.get("cost", {}).get("wall_ms", 0)) for r in receipts)
per_world = {}
for r in receipts:
    per_world.setdefault(r["world"], []).append({
        "oracle": r["oracle"]["id"],
        "status": r["result"]["status"],
        "wall_ms": int(r.get("cost", {}).get("wall_ms", 0)),
    })
for v in per_world.values():
    v.sort(key=lambda x: (x["oracle"], x["status"]))

sched = rep.get("schedule") or {}
waste = sched.get("waste") or {}
# The winner is identified across the two arms by its CANDIDATE ORDINAL, not
# by its world digest. A world object binds the intent it was created for, and
# the two arms necessarily hold different intents (different max_oracle_ms is
# the whole point), so the same patch produces a different world digest in each
# workspace. Comparing digests would report "different subject" on every run,
# including the null case where the arms agree perfectly — a harness that can
# never report agreement is measuring itself.
cands = rep.get("candidates", [])
winner = rep.get("winner", "")
winner_ordinal = next((c.get("ordinal", 0) for c in cands if c.get("world") == winner), 0)
out = {
    "arm": arm,
    "decision": rep.get("type", ""),
    "winner": winner,
    "winner_ordinal": winner_ordinal,
    "subject": sorted(c.get("ordinal", 0) for c in cands if c.get("pass")),
    "pass_count": sum(1 for c in cands if c.get("pass")),
    "rationale": rep.get("rationale", ""),
    "receipts": len(receipts),
    "spend_ms": spend,
    "per_world": per_world,
    "trace_recorded": bool(sched.get("recorded")),
    "trace_arm": sched.get("arm", ""),
    "budget_ms": sched.get("budget_ms", 0),
    "stop": sched.get("stop", ""),
    "considered": (sched.get("totals") or {}).get("considered", 0),
    "bought": (sched.get("totals") or {}).get("bought", 0),
    "declined_count": (sched.get("totals") or {}).get("declined", 0),
    "declined": [
        {"step": r["step"], "world": r["world"], "oracle": r["oracle"], "why": r["declined"]}
        for r in sched.get("steps", []) if r.get("declined")
    ],
    "predicted_ms": sched.get("predicted_ms", 0),
    "waste_ms": waste.get("waste_ms", 0),
    "waste_bp": waste.get("waste_bp", 0),
    "greedy_ms": waste.get("greedy_ms", 0),
    "waste_available": bool(waste.get("available")),
}
print(json.dumps(out, sort_keys=True))
PY
}

# --- arm 1: the FIXED M1 ladder --------------------------------------------
FIXED_DIR="$WORK/fixed"
setup_repo "$FIXED_DIR"
FIXED_INTENT="$(new_intent "$FIXED_DIR" "schedule-compare: fixed ladder" "$BUDGET")"
[ -n "$FIXED_INTENT" ] || fail "mvo intent new (fixed arm) printed no digest"
run_arm "$FIXED_DIR" "$FIXED_INTENT" "$FIXED_FLAG"
FIXED_JSON="$(arm_report "$FIXED_DIR" "$FIXED_INTENT" fixed)"

# The matched budget: whatever the exhaustive ladder actually spent, unless
# the caller pinned one.
MATCHED="$BUDGET"
if [ -z "$MATCHED" ]; then
  MATCHED="$(printf '%s' "$FIXED_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["spend_ms"])')"
fi
[ "$MATCHED" -gt 0 ] 2>/dev/null || fail "matched budget is not a positive integer: '$MATCHED' (the fixed arm recorded no oracle spend)"

# --- arm 2: the ADAPTIVE scheduler at the matched budget --------------------
ADAPT_DIR="$WORK/adaptive"
setup_repo "$ADAPT_DIR"
ADAPT_INTENT="$(new_intent "$ADAPT_DIR" "schedule-compare: adaptive" "$MATCHED")"
[ -n "$ADAPT_INTENT" ] || fail "mvo intent new (adaptive arm) printed no digest"
run_arm "$ADAPT_DIR" "$ADAPT_INTENT" "$ADAPTIVE_FLAG"
ADAPT_JSON="$(arm_report "$ADAPT_DIR" "$ADAPT_INTENT" adaptive)"

# --- the comparison ---------------------------------------------------------
FIXED_JSON="$FIXED_JSON" ADAPT_JSON="$ADAPT_JSON" MATCHED="$MATCHED" JSON="$JSON" python3 <<'PY'
import json, os, sys

fixed = json.loads(os.environ["FIXED_JSON"])
adapt = json.loads(os.environ["ADAPT_JSON"])
matched = int(os.environ["MATCHED"])
as_json = os.environ["JSON"] == "1"

same_decision = fixed["decision"] == adapt["decision"]
# The patches are replayed in the same order in both workspaces, so the
# candidate ORDINAL is the identity that survives the crossing; the world
# digest does not (it binds the intent, and the arms hold different intents).
same_subject = fixed["winner_ordinal"] == adapt["winner_ordinal"]
same_pass_set = fixed["subject"] == adapt["subject"]
verdict = {
    "matched_budget_ms": matched,
    "same_decision": same_decision,
    "same_subject": same_subject,
    "same_pass_set": same_pass_set,
    "delta_spend_ms": adapt["spend_ms"] - fixed["spend_ms"],
    "delta_receipts": adapt["receipts"] - fixed["receipts"],
    "adaptive_within_budget": adapt["spend_ms"] <= matched,
}

if as_json:
    print(json.dumps({"fixed": fixed, "adaptive": adapt, "verdict": verdict},
                     sort_keys=True, indent=1))
    sys.exit(0)

def short(d):
    return (d[:12] + "…") if len(d) > 12 else (d or "-")

print("schedule-compare — same fixture, two arms, matched total budget %d ms" % matched)
print()
print("  %-10s %-9s %-14s %8s %9s %9s  %s" %
      ("ARM", "DECISION", "SUBJECT", "RECEIPTS", "SPEND", "PREDICTED", "STOP"))
for a in (fixed, adapt):
    print("  %-10s %-9s %-14s %8d %7d ms %7d ms  %s" %
          (a["arm"], a["decision"] or "-", short(a["winner"]), a["receipts"],
           a["spend_ms"], a["predicted_ms"], a["stop"] or "-"))
print()

for a in (fixed, adapt):
    print("  %s arm — receipts bought:" % a["arm"])
    if not a["per_world"]:
        print("    (none)")
    for world in sorted(a["per_world"]):
        for r in a["per_world"][world]:
            print("    %-14s %-22s %-6s %6d ms" %
                  (short(world), r["oracle"], r["status"], r["wall_ms"]))
    print()

if adapt["trace_recorded"]:
    print("  adaptive arm — considered %d, bought %d, declined %d (arm recorded: %s)" %
          (adapt["considered"], adapt["bought"], adapt["declined_count"], adapt["trace_arm"] or "-"))
    for d in adapt["declined"]:
        print("    step %d  %-14s %-12s %s" % (d["step"], short(d["world"]), d["oracle"], d["why"]))
    if adapt["waste_available"]:
        print("  adaptive arm — evidence waste %d ms (%.1f%%), greedy %d ms" %
              (adapt["waste_ms"], adapt["waste_bp"] / 100.0, adapt["greedy_ms"]))
    else:
        # Absent source implies absent metric.
        print("  adaptive arm — evidence waste not computable from this ledger")
else:
    print("  adaptive arm recorded NO allocation trace: the arms are not distinguishable in this run")
print()

print("verdict:")
print("  same decision:   %s (%s vs %s)" % (same_decision, fixed["decision"], adapt["decision"]))
print("  same subject:    %s (candidate #%s vs #%s)" %
      (same_subject, fixed["winner_ordinal"] or "-", adapt["winner_ordinal"] or "-"))
print("  same pass set:   %s (%s vs %s)" % (same_pass_set, fixed["subject"], adapt["subject"]))
print("  delta spend:     %+d ms (%d -> %d)" % (verdict["delta_spend_ms"], fixed["spend_ms"], adapt["spend_ms"]))
print("  delta receipts:  %+d (%d -> %d)" % (verdict["delta_receipts"], fixed["receipts"], adapt["receipts"]))
print("  adaptive within the matched budget: %s" % verdict["adaptive_within_budget"])
if not verdict["adaptive_within_budget"]:
    # Not a harness failure and not a scheduler bug: a purchase is committed
    # against a PREDICTED cost and charged at its ACTUAL one, so the bound is
    # overrun by at most one batch. On a fresh workspace no coefficient has
    # been fitted yet, every row is priced `declared-rank`, and an unpriced
    # purchase is affordable while any budget remains — so the overrun is the
    # last batch in full. It is visible here rather than hidden because a
    # budget that silently does not bind is worse than one that overruns
    # loudly.
    print("        (overrun %+d ms — purchases commit against a PREDICTED cost and are"
          % (adapt["spend_ms"] - matched))
    print("         charged the ACTUAL one; with no fitted coefficient the prediction is")
    print("         rank-only, so the bound is overrun by the last batch)")
if not same_decision:
    # This is the result the experiment exists to find, and it is never a
    # PASS or a FAIL on its own: under withholding monotonicity adaptivity
    # cannot cause a false ADMISSION, but it can cause a false REJECTION, and
    # which one happened is a question about ground truth that this harness
    # does not hold. M2d's labelled arm is what prices it.
    print()
    print("  NOTE: the arms decided differently. That is a measurement, not a failure:")
    print("        adaptivity provably cannot cause a false admission (decision 4), but it")
    print("        CAN cause a false rejection, and only labelled outcomes (M2d) can say")
    print("        which of the two this is.")
PY
