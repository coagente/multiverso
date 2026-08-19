#!/usr/bin/env bash
# M2b1 comparison harness — the SAME fixture raced under two BUDGETED arms at
# the SAME budget in the SAME unit, R times, arms interleaved, with dispersion
# and a noise floor reported and no verdict printed below R = 3.
#
# WHAT CHANGED FROM M2b, AND WHY THE OLD NUMBERS WERE ANECDOTES. M2b's
# harness compared `--schedule=adaptive` under budget B against
# `--schedule=fixed`, which READS max_oracle_ms NEVER. Every "matched budget"
# figure it produced compared *adaptive under B* against *exhaustive under ∞*.
# The arm that fixes it is `--schedule=fixed-budget`: the same loop, the same
# budget object, the same affordability predicate and the same charge point,
# with the value terms removed and the ordering replaced by the control-plane
# world order. This harness gives both arms the same money and asserts, run by
# run, that it did.
#
# WHY A SHELL SCRIPT AND NOT A GO HARNESS. Unchanged from M2b: the comparison
# has to run through the BINARY (intent creation, policy pinning, the ledger
# and `mvo explain --schedule`'s derived waste number are the surface M2d
# compares), it is the repository's existing shape for end-to-end evidence,
# and the arms must be able to differ in flag spelling without a rebuild.
#
# THE SIXTEEN CONDITIONS (M2b1 §3) THIS SCRIPT ENFORCES MECHANICALLY:
#   F1  same policy, pinned by digest and asserted equal across arms
#   F3  same base state and cost table: ONE seeded workspace, COPIED per arm
#       per replicate, with schedule.started.cost_table asserted BYTE-EQUAL
#   F4  same budget, same unit, same basis: ONE intent digest, created before
#       the copy, so both arms literally consume the same object
#   F9  same dispatch degree, asserted equal
#   F10 same host conditions: arms INTERLEAVED within a replicate (A,B,A,B —
#       never all-A-then-all-B, which confounds arm with machine drift) and a
#       host-load probe timed immediately before each arm
#   F12 replication with variance reported (§4)
#   F15 same binary: built once, digest recorded
#
# AND THE ONES IT CANNOT: F2 (the arms cannot verify the same worlds — a world
# binds created_at, the agent RunCost and a transcript digest, so two runs of
# one patch produce different world digests by construction) and F7 (a race
# decided at the terminal `world_digest_asc` key was decided by a coin flip
# over candidate-authored bytes; such races are QUARANTINED into their own
# bucket and excluded from the paired comparison).
#
# NO FIGURE FROM THIS HARNESS MAY BE CALLED BUDGET-MATCHED IN PRD §11's SENSE.
# §11's budget is tokens + runner time + oracle cost + selection cost; this
# charges the third term and not all of it. The correct label is
# ORACLE-BUDGET-MATCHED and the script prints it on every report.
#
# NO REAL AGENT CLI IS EVER INVOKED (M1b rule): the script adapter replays the
# committed patches in testdata/toyrepo/patches. No network. POSIX.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ARM_A_FLAG="--schedule=adaptive"
ARM_B_FLAG="--schedule=fixed-budget"
BUDGET_FLAG="--budget-oracle-ms"
BASIS="actual"
POLICY=""          # "" = the workspace default `mvo init` writes
PATCHES="patches"  # under testdata/toyrepo
BUDGET=""          # "" = measure the reference arm's spend and match it
LEVEL=""           # B1 | B2 | B3 — derived from the reference run (§4)
REPLICATES=1
PARALLEL=1
WARMUP=2
PROBE_TOL_BP=2000  # 20%, F10's declared tolerance
STRICT=0
KEEP=""
JSON=0

usage() {
  cat >&2 <<'EOF'
usage: scripts/schedule-compare.sh [options]

  --replicates N         paired replicates per level (default 1; NO VERDICT below 3)
  --strict               exit non-zero when no verdict can be printed (R < 3)
  --budget MS            matched oracle budget for BOTH arms
  --level B1|B2|B3       derive the budget from the reference run instead (§4):
                           B1 = ceil(minspend x 1.1) — the tightest budget where
                                winning is possible at all
                           B2 = (B1 + S) / 2         — the middle of the band
                           B3 = S                    — the null case
  --budget-basis B       actual (default) or predicted (charges the pinned cost
                         table, which puts the allocation inside the determinism
                         tuple); BOTH arms always get the same basis
  --policy NAME|DIGEST   pin a policy for both arms (default: workspace default)
  --patches DIR          patch directory under testdata/toyrepo (default: patches)
  --parallel N           dispatch degree for BOTH arms (default: 1, the canonical
                         comparison; results at different k are never pooled)
  --warmup N             warm-up races used to fit the cost table (default: 2).
                         0 is the COLD instrument, kept because M2d.1 accept
                         step m2d1-9a has to reproduce the vacuum on purpose:
                         with nothing priced, every allocation rule collapses
                         to the exhaustive ladder and the comparison is VACUOUS
  --probe-tolerance-bp N host-probe tolerance in basis points (default: 2000 = 20%)
  --arm-a FLAG           first arm (default: --schedule=adaptive)
  --arm-b FLAG           second arm (default: --schedule=fixed-budget)
                         M2b.2's paired before/after is
                           --arm-a --selector=voc --arm-b --selector=voc2
                         which races the PUBLISHED allocation rule against its
                         revision under ONE binary, one cost table, one Decide
                         and one seeded workspace (F15) — the only way a
                         per-instance difference is attributable to the rule
  --budget-flag FLAG     intent flag carrying max_oracle_ms (default: --budget-oracle-ms)
  --keep DIR             keep the workspaces in DIR instead of a temp dir
  --json                 emit the comparison as one JSON object
  -h, --help             this message


exit codes: 0 compared, 1 a run or an assertion failed (INERTNESS VIOLATED is
              one of them), 2 usage, 3 no verdict (R < 3) under --strict,
            5 VACUOUS — the rule under test never fired, so no verdict,
            77 SKIP — this binary does not expose the arms to compare
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --replicates) REPLICATES="${2:?--replicates needs a value}"; shift 2 ;;
    --strict) STRICT=1; shift ;;
    --budget) BUDGET="${2:?--budget needs a value}"; shift 2 ;;
    --level) LEVEL="${2:?--level needs a value}"; shift 2 ;;
    --budget-basis) BASIS="${2:?--budget-basis needs a value}"; shift 2 ;;
    --policy) POLICY="${2:?--policy needs a value}"; shift 2 ;;
    --patches) PATCHES="${2:?--patches needs a value}"; shift 2 ;;
    --parallel) PARALLEL="${2:?--parallel needs a value}"; shift 2 ;;
    --warmup) WARMUP="${2:?--warmup needs a value}"; shift 2 ;;
    --probe-tolerance-bp) PROBE_TOL_BP="${2:?}"; shift 2 ;;
    --arm-a) ARM_A_FLAG="${2:?}"; shift 2 ;;
    --arm-b) ARM_B_FLAG="${2:?}"; shift 2 ;;
    --adaptive-flag) ARM_A_FLAG="${2:?}"; shift 2 ;;  # M2b spelling, kept
    --fixed-flag) ARM_B_FLAG="${2:?}"; shift 2 ;;     # M2b spelling, kept
    --budget-flag) BUDGET_FLAG="${2:?}"; shift 2 ;;
    --keep) KEEP="${2:?--keep needs a directory}"; shift 2 ;;
    --json) JSON=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "schedule-compare: unknown option $1" >&2; usage; exit 2 ;;
  esac
done

fail() { echo "FAIL: $*" >&2; exit 1; }

case "$BASIS" in actual|predicted) ;; *) echo "schedule-compare: --budget-basis must be actual or predicted" >&2; exit 2 ;; esac
[ "$REPLICATES" -ge 1 ] 2>/dev/null || { echo "schedule-compare: --replicates must be at least 1" >&2; exit 2; }
[ -z "$LEVEL" ] || case "$LEVEL" in B1|B2|B3) ;; *) echo "schedule-compare: --level must be B1, B2 or B3" >&2; exit 2 ;; esac
[ -z "$BUDGET" ] || [ -z "$LEVEL" ] || { echo "schedule-compare: --budget and --level are mutually exclusive" >&2; exit 2; }

if [ -n "$KEEP" ]; then
  WORK="$KEEP"; mkdir -p "$WORK"
else
  WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
fi

# F15: ONE BINARY for both arms, and its digest in the report. Two builds is
# two `Decide`s, two cost models and two sets of clamps.
MVO="$WORK/mvo"
(cd "$ROOT" && go build -o "$MVO" ./cmd/mvo)
BUILD_DIGEST="$(python3 - "$MVO" <<'PY'
import hashlib, sys
h = hashlib.sha256()
with open(sys.argv[1], "rb") as fh:
    for chunk in iter(lambda: fh.read(1 << 20), b""):
        h.update(chunk)
print("sha256:" + h.hexdigest())
PY
)"

# --- capability probe -------------------------------------------------------
# The arms are flags on `mvo race` and one flag on `mvo intent new`. If this
# binary has none of them there is nothing to compare, and the honest answer is
# SKIP with the reason — not a fabricated comparison of one arm against itself.
RACE_HELP="$("$MVO" race --help 2>&1 || true)"
INTENT_HELP="$("$MVO" intent new --help 2>&1 || true)"
missing=""
# Go's flag package renders `-name` in its usage even for flags spelled
# `--name` on the command line, so the probe matches on the BARE NAME.
a_name="${ARM_A_FLAG%%=*}"; a_name="${a_name#--}"; a_name="${a_name#-}"
b_name="${ARM_B_FLAG%%=*}"; b_name="${b_name#--}"; b_name="${b_name#-}"
budget_name="${BUDGET_FLAG#--}"; budget_name="${budget_name#-}"
a_value="${ARM_A_FLAG#*=}"; b_value="${ARM_B_FLAG#*=}"
case "$RACE_HELP"   in *"$a_name"*)      ;; *) missing="$missing ${ARM_A_FLAG%%=*}" ;; esac
case "$RACE_HELP"   in *"$b_name"*)      ;; *) missing="$missing ${ARM_B_FLAG%%=*}" ;; esac
case "$RACE_HELP"   in *"$a_value"*)     ;; *) missing="$missing $a_value" ;; esac
case "$RACE_HELP"   in *"$b_value"*)     ;; *) missing="$missing $b_value" ;; esac
case "$RACE_HELP"   in *"budget-basis"*) ;; *) missing="$missing --budget-basis" ;; esac
case "$INTENT_HELP" in *"$budget_name"*) ;; *) missing="$missing ${BUDGET_FLAG}" ;; esac
if [ -n "$missing" ]; then
  cat >&2 <<EOF
schedule-compare: SKIP — this binary does not expose the arms to compare.
  missing:$missing
  Both arms must be BUDGETED. \`--schedule=fixed\` is the UNBUDGETED exhaustive
  M1 ladder: it reads max_oracle_ms never, so comparing it against a budgeted
  arm compares B against infinity. Re-run once --schedule=fixed-budget lands,
  or pass --arm-a/--arm-b if the arms are spelled differently here.
EOF
  exit 77
fi

GIT="git -c user.name=m2b1 -c user.email=m2b1@example.invalid"

# --- F3: ONE SEEDED WORKSPACE, COPIED -------------------------------------
# The cost table is fitted from the workspace's OWN receipts, so two
# workspaces with different history price differently and allocate
# differently. Seeding once and COPYING the directory is what makes the
# ledgers that feed costSamples() byte-identical; the harness then asserts
# schedule.started.cost_table is byte-equal across the arms and aborts if it
# is not. That assertion is the whole reason the cost table is a recorded
# snapshot.
SEED="$WORK/seed"
mkdir -p "$SEED"
cp -R "$ROOT/testdata/toyrepo/." "$SEED/"
rm -rf "$SEED/__pycache__" "$SEED/.pytest_cache"
$GIT -C "$SEED" init -q -b main
$GIT -C "$SEED" add -A
$GIT -C "$SEED" commit -qm "toyrepo baseline"
"$MVO" init --dir "$SEED" >/dev/null
# A named fixture policy is an AUTHORED FILE until a verb installs it, so
# --policy NAME has nothing to resolve until it is in the workspace.
if [ -n "$POLICY" ] && [ -f "$SEED/policies/$POLICY.json" ]; then
  cp "$SEED/policies/$POLICY.json" "$SEED/.multiverso/policies/$POLICY.json"
fi

new_intent() {
  local dir="$1" title="$2" budget="$3"
  local args=(intent new --dir "$dir" --title "$title" --desc "schedule-compare")
  [ -n "$POLICY" ] && args+=(--policy "$POLICY")
  [ -n "$budget" ] && args+=("$BUDGET_FLAG" "$budget")
  "$MVO" "${args[@]}"
}

# WARM-UP: the cost model is fitted from receipts, and on a fresh workspace
# there are none — every rung is priced `declared-rank`, an unpriced purchase
# is affordable while any pool remains, and the budget does not bind at all.
# A comparison run on an unfitted workspace measures the overrun, not the
# allocation. The warm-up races are UNBUDGETED and are part of the seed, so
# both arms inherit the identical fit.
i=0
while [ "$i" -lt "$WARMUP" ]; do
  i=$((i + 1))
  WARM_INTENT="$(new_intent "$SEED" "warm-up $i" "")"
  "$MVO" race "$WARM_INTENT" --dir "$SEED" --agent script \
    --patches "$SEED/$PATCHES" --parallel "$PARALLEL" --schedule=fixed >/dev/null
done

# --- the REFERENCE run (decision 2): fixed-budget at an unbounded budget ---
# Same evidence set as `--schedule=fixed` — an unbounded budget buys every
# rung — but it EMITS A TRACE, so the exhaustive spend S, the cost-table
# snapshot, evidence waste and the allocation bound are computable for the
# reference too. That is what makes the reference an arm rather than a
# blank spot in the table.
REF_DIR="$WORK/reference"
cp -R "$SEED" "$REF_DIR"
REF_INTENT="$(new_intent "$REF_DIR" "reference (unbounded)" 0)"
"$MVO" race "$REF_INTENT" --dir "$REF_DIR" --agent script \
  --patches "$REF_DIR/$PATCHES" --parallel "$PARALLEL" "$ARM_B_FLAG" >/dev/null \
  || fail "the reference race failed"
REF_JSON="$("$MVO" explain "$REF_INTENT" --dir "$REF_DIR" --json --schedule --bound)"
REF_SPEND="$(printf '%s' "$REF_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["schedule"]["spent_ms"])')"
REF_MINSPEND="$(printf '%s' "$REF_JSON" | python3 -c '
import json, sys
b = (json.load(sys.stdin)["schedule"] or {}).get("bound") or {}
print(b.get("minspend_ms", 0) if b.get("available") else 0)')"
[ "$REF_SPEND" -gt 0 ] 2>/dev/null || fail "the reference race recorded no oracle spend"

# The three budget levels are defined on the INFORMATIVE BAND rather than on a
# round fraction of the exhaustive spend (§4): a fixed fraction is
# uninformative on instances where nothing was decidable and trivial on
# instances where everything was.
MATCHED="$BUDGET"
if [ -z "$MATCHED" ]; then
  MATCHED="$(BUDGET_LEVEL="$LEVEL" S="$REF_SPEND" M="$REF_MINSPEND" python3 <<'PY'
import math, os
S = int(os.environ["S"]); m = int(os.environ["M"])
level = os.environ["BUDGET_LEVEL"]
b1 = math.ceil(m * 1.1) if m > 0 else S
if level == "B1":
    print(max(1, b1))
elif level == "B2":
    print(max(1, (b1 + S) // 2))
else:
    # No level asked for, or B3: the null case, where both arms should match
    # the reference and an arm that does not has a bug, not a result.
    print(S)
PY
)"
fi
[ "$MATCHED" -gt 0 ] 2>/dev/null || fail "matched budget is not a positive integer: '$MATCHED'"

# F4: ONE INTENT DIGEST, created in the SEED before any copy, so both arms
# consume literally the same object rather than two objects that agree.
MATCHED_INTENT="$(new_intent "$SEED" "matched budget $MATCHED ms" "$MATCHED")"
[ -n "$MATCHED_INTENT" ] || fail "mvo intent new (matched) printed no digest"

# host_probe times ONE fixed control-plane action immediately before an arm
# (F10). Python interpreter start is the dominant fixed cost of every pytest
# rung on this fixture, so it is the right thing to probe: it moves with the
# machine's load in the same way the purchases do. It is CONTROL-PLANE work —
# no candidate authored it — and it is charged to no budget.
host_probe() {
  python3 - <<'PY'
import subprocess, sys, time
times = []
for _ in range(3):
    t0 = time.monotonic()
    subprocess.run([sys.executable, "-c", "pass"], check=False,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    times.append((time.monotonic() - t0) * 1000)
times.sort()
print(int(times[len(times) // 2]))
PY
}

# arm_report <dir> <intent> <arm> <probe_ms> -> one JSON object on stdout,
# assembled from the LEDGER and from `mvo explain --schedule --json`. Nothing
# here re-derives a number the binary already recorded.
arm_report() {
  local dir="$1" intent="$2" arm="$3" probe="$4"
  local explain
  explain="$("$MVO" explain "$intent" --dir "$dir" --json --schedule)"
  ARM="$arm" PROBE="$probe" EXPLAIN="$explain" python3 - "$dir" <<'PY'
import json, os, sqlite3, sys

repo = sys.argv[1]
arm  = os.environ["ARM"]
rep  = json.loads(os.environ["EXPLAIN"])

db = sqlite3.connect(os.path.join(repo, ".multiverso", "ledger.db"))
rows = db.execute("SELECT type, cast(payload AS text) FROM events ORDER BY seq").fetchall()

# The race WINDOW: this intent's LAST race alone. A workspace holds many races
# (the warm-ups are in this very ledger) and numbers assembled across two of
# them would describe a race nobody ran.
lo, hi = 0, len(rows)
for i, (typ, payload) in enumerate(rows):
    if typ == "race.started" and json.loads(payload).get("intent") == os.environ.get("INTENT", ""):
        lo = i
window = rows[lo:hi]
receipts = [json.loads(p) for t, p in window if t == "receipt.recorded"]
started = [json.loads(p) for t, p in window if t == "schedule.started"]
started = started[-1] if started else {}

# Sigma cost.wall_ms over every receipt this race bought: the money spent,
# taken from the receipts themselves rather than from the scheduler's own
# running total, so both arms are measured by the same yardstick.
spend = sum(int(r.get("cost", {}).get("wall_ms", 0)) for r in receipts)
per_world = {}
for r in receipts:
    per_world.setdefault(r["world"], []).append(r["oracle"]["id"])
for v in per_world.values():
    v.sort()

sched = rep.get("schedule") or {}
waste = sched.get("waste") or {}
cands = rep.get("candidates", [])
winner = rep.get("winner", "")
# The winner is identified across arms by its CANDIDATE ORDINAL, never by its
# world digest: a world binds created_at, the agent RunCost and a transcript
# digest, so the same patch produces a different world digest in every run
# (F2). Comparing digests would report "different subject" on every run,
# including the null case where the arms agree perfectly.
winner_ordinal = next((c.get("ordinal", 0) for c in cands if c.get("world") == winner), 0)
ord_of = {c.get("world", ""): c.get("ordinal", 0) for c in cands}

# F7: was this race decided at the TERMINAL world_digest_asc key? That is a
# coin flip over candidate-authored bytes, and it lands differently in each
# arm for reasons that are not the treatment. Such races are quarantined.
# F7 is about the WINNER: "a race whose winner was chosen at the terminal key
# was chosen by a coin flip that lands differently in each arm". A race with no
# winner has no such flip to quarantine — the ranking of two worlds that both
# failed decides nothing the comparison reads — so the test is conditioned on
# there being a winner at all. Over-quarantining is not conservatism here: it
# throws away the REJECT/ESCALATE replicates that carry most of the signal at a
# binding budget.
decided_at_digest = bool(winner) and any(
    c.get("key") == "world_digest_asc" for c in rep.get("trace", [])[:1])

# A world is COMPLETE when its ladder ended for a reason of its own — it
# bought every rung, or a hard gate failed and stopped it — rather than
# because the money ran out. The trace says which: an oracle.skipped row is
# the record of a rung nobody bought, so a world named in one is a world the
# budget truncated. This is the quantity §4 asks for, and it is the one the
# depth-first arm exists to move: the adaptive rule degenerates to round robin
# on symmetric worlds and completes NONE.
truncated = {sk["world"] for sk in (sched.get("skipped") or [])}
complete = sum(1 for c in cands if c.get("world", "") not in truncated)

out = {
    "arm": arm,
    "probe_ms": int(os.environ["PROBE"]),
    "decision": rep.get("type", ""),
    "winner_ordinal": winner_ordinal,
    "pass_set": sorted(c.get("ordinal", 0) for c in cands if c.get("pass")),
    "decided_at_world_digest": decided_at_digest,
    "receipts": len(receipts),
    "complete_worlds": complete,
    "spend_ms": spend,
    "per_world": per_world,
    # The fields the pairing is ASSERTED on (F1/F3/F4/F9): a comparison whose
    # arms disagree about any of them is not a comparison.
    "policy": rep.get("policy", {}).get("digest", ""),
    "cost_table": json.dumps(started.get("cost_table", []), sort_keys=True),
    "budget_ms": started.get("budget", {}).get("max_oracle_ms", 0),
    "budget_basis": started.get("budget_basis", ""),
    "parallel": started.get("parallel", 0),
    "rotation": started.get("rotation", 0),
    "world_order_len": len(started.get("world_order", []) or []),
    "selector": started.get("selector", ""),
    "trace_arm": started.get("schedule", ""),
    "stop": sched.get("stop", ""),
    "selection_us": sched.get("selection_us", 0),
    "considered": (sched.get("totals") or {}).get("considered", 0),
    "bought": (sched.get("totals") or {}).get("bought", 0),
    "declined_count": (sched.get("totals") or {}).get("declined", 0),
    "declined": [
        {"step": r["step"], "world": r["world"], "oracle": r["oracle"], "why": r["declined"]}
        for r in sched.get("steps", []) if r.get("declined")
    ],
    # M2d.1: COVERAGE, DERIVED AND RENDERED. `mvo explain --schedule --json`
    # computes it from the RECORDED steps of this race; nothing here
    # recomputes a score. A comparison that cannot say whether the rule under
    # test ever fired is a comparison of one rule against itself.
    "coverage": sched.get("coverage") or {},
    # The PURCHASE ORDER, keyed on the CANDIDATE ORDINAL and never on the
    # world digest: a world binds created_at, the agent RunCost and a
    # transcript digest, so the same patch produces a different digest in
    # every run (F2) and a digest-keyed comparison would report "different
    # subject" on every run including the perfect null.
    "purchase_order": [
        "%s.%s" % (ord_of.get(r["world"], "?"), r["oracle"])
        for r in sched.get("steps", []) if r.get("bought")
    ],
    "waste_ms": waste.get("waste_ms", 0),
    "waste_bp": waste.get("waste_bp", 0),
    "waste_available": bool(waste.get("available")),
    "overrun_ms": max(0, spend - int(started.get("budget", {}).get("max_oracle_ms", 0) or 0)),
}
print(json.dumps(out, sort_keys=True))
PY
}

run_arm() {
  local dir="$1" intent="$2" armflag="$3" rotation="$4"
  "$MVO" race "$intent" --dir "$dir" --agent script \
    --patches "$dir/$PATCHES" --parallel "$PARALLEL" \
    --budget-basis "$BASIS" --world-order-rotation "$rotation" "$armflag" >/dev/null
}

# --- the replicates ---------------------------------------------------------
# Replicate r gets its OWN COPY of the one seeded workspace; both arms in
# replicate r share the intent digest, the cost table, the host-probe window
# and the order rotation ρ(r) = r mod N. The two arms run ADJACENT IN TIME —
# A,B,A,B and never all-A-then-all-B, which would confound the arm with the
# machine's drift over the run.
PAIRS="$WORK/pairs.jsonl"
: > "$PAIRS"
r=0
while [ "$r" -lt "$REPLICATES" ]; do
  ROT="$r"
  A_DIR="$WORK/rep$r-a"; B_DIR="$WORK/rep$r-b"
  cp -R "$SEED" "$A_DIR"; cp -R "$SEED" "$B_DIR"

  PROBE_A="$(host_probe)"
  run_arm "$A_DIR" "$MATCHED_INTENT" "$ARM_A_FLAG" "$ROT" || fail "replicate $r: arm A failed"
  A_JSON="$(INTENT="$MATCHED_INTENT" arm_report "$A_DIR" "$MATCHED_INTENT" a "$PROBE_A")"

  PROBE_B="$(host_probe)"
  run_arm "$B_DIR" "$MATCHED_INTENT" "$ARM_B_FLAG" "$ROT" || fail "replicate $r: arm B failed"
  B_JSON="$(INTENT="$MATCHED_INTENT" arm_report "$B_DIR" "$MATCHED_INTENT" b "$PROBE_B")"

  # THE COMPARABILITY ASSERTIONS (F1/F3/F4/F9). The harness ABORTS rather than
  # reporting: a comparison whose arms held different budgets, different cost
  # tables, different policies or different dispatch degrees is not a
  # comparison, and printing it with a warning is how such a number ends up in
  # a paper.
  A_JSON="$A_JSON" B_JSON="$B_JSON" REP="$r" python3 <<'PY' || exit 1
import json, os, sys
a = json.loads(os.environ["A_JSON"]); b = json.loads(os.environ["B_JSON"])
rep = os.environ["REP"]
for field, why in [
    ("policy", "F1: the arms evaluated different gate lattices"),
    ("cost_table", "F3: the arms allocated against different cost models"),
    ("budget_ms", "F4: the arms were given different amounts of money"),
    ("budget_basis", "F4: the arms charged their pools differently"),
    ("parallel", "F9: the arms dispatched at different degrees; results at different k are never pooled"),
    ("rotation", "F12: the paired replicate ran two different world orders"),
    ("world_order_len", "decision 3: the arms ranked over different world sets"),
]:
    if a[field] != b[field]:
        sys.exit("FAIL: replicate %s, %s (%s: %r vs %r)" % (rep, why, field, a[field], b[field]))
if not a["cost_table"] or a["cost_table"] == "[]":
    sys.exit("FAIL: replicate %s: no cost-table snapshot was recorded; the arms cannot be shown to have priced alike" % rep)
if a["selector"] == b["selector"]:
    sys.exit("FAIL: replicate %s: both arms recorded selector %r — they are supposed to differ in exactly this" % (rep, a["selector"]))
PY
  printf '%s\n' "$(A_JSON="$A_JSON" B_JSON="$B_JSON" REP="$r" python3 -c '
import json, os
print(json.dumps({"replicate": int(os.environ["REP"]),
                  "a": json.loads(os.environ["A_JSON"]),
                  "b": json.loads(os.environ["B_JSON"])}, sort_keys=True))')" >> "$PAIRS"
  r=$((r + 1))
done

# --- the report -------------------------------------------------------------
PAIRS="$PAIRS" MATCHED="$MATCHED" REPLICATES="$REPLICATES" JSON="$JSON" \
  STRICT="$STRICT" BASIS="$BASIS" PARALLEL="$PARALLEL" LEVEL="${LEVEL:-}" \
  REF_SPEND="$REF_SPEND" REF_MINSPEND="$REF_MINSPEND" BUILD="$BUILD_DIGEST" \
  ARM_A="$ARM_A_FLAG" ARM_B="$ARM_B_FLAG" PROBE_TOL_BP="$PROBE_TOL_BP" python3 <<'PY'
import json, os, sys

pairs = [json.loads(line) for line in open(os.environ["PAIRS"]) if line.strip()]
R = int(os.environ["REPLICATES"])
matched = int(os.environ["MATCHED"])
as_json = os.environ["JSON"] == "1"
strict = os.environ["STRICT"] == "1"
tol_bp = int(os.environ["PROBE_TOL_BP"])
ref_spend = int(os.environ["REF_SPEND"]); ref_min = int(os.environ["REF_MINSPEND"])

def quantiles(xs):
    """median + IQR + min/max. NEVER a mean alone: the distribution this
    harness measures is small, bounded below and occasionally bimodal (an arm
    either buys the last rung or it does not), and a mean hides exactly the
    shape that matters."""
    if not xs:
        return {"n": 0}
    s = sorted(xs)
    def q(p):
        if len(s) == 1:
            return s[0]
        i = p * (len(s) - 1)
        lo, hi = int(i), min(int(i) + 1, len(s) - 1)
        return s[lo] + (s[hi] - s[lo]) * (i - lo)
    return {"n": len(s), "min": s[0], "p25": q(0.25), "median": q(0.5),
            "p75": q(0.75), "max": s[-1]}

# F10: a replicate whose two host probes differ by more than the declared
# tolerance is REPORTED AND EXCLUDED. The budget's unit is wall-clock
# milliseconds, so a machine that got busier between the two arms charged them
# differently for the same work — a difference that is not the treatment.
def probe_outlier(p):
    x, y = p["a"]["probe_ms"], p["b"]["probe_ms"]
    lo = min(x, y)
    if lo <= 0:
        return False
    return abs(x - y) * 10000 // lo > tol_bp

quarantine = {"probe_outlier": [], "decided_at_world_digest": []}
kept = []
for p in pairs:
    if probe_outlier(p):
        quarantine["probe_outlier"].append(p["replicate"])
        continue
    if p["a"]["decided_at_world_digest"] or p["b"]["decided_at_world_digest"]:
        # A race decided at the terminal key was decided by a coin flip over
        # candidate-authored bytes, and the flip lands differently in each arm.
        quarantine["decided_at_world_digest"].append(p["replicate"])
        continue
    kept.append(p)

def arm_stats(side):
    rows = [p[side] for p in kept]
    decisions = {}
    stops = {}
    for r in rows:
        decisions[r["decision"]] = decisions.get(r["decision"], 0) + 1
        stops[r["stop"] or "-"] = stops.get(r["stop"] or "-", 0) + 1
    modal = max(decisions, key=lambda k: (decisions[k], k)) if decisions else ""
    # THE NOISE FLOOR: the fraction of replicates whose decision differs from
    # this arm's OWN modal decision. A difference between the arms that does
    # not exceed both arms' self-disagreement is a TIE, and it is reported as
    # one in the text and not only in a footnote.
    self_dis = sum(1 for r in rows if r["decision"] != modal) / len(rows) if rows else 0.0
    return {
        "arm": rows[0]["arm"] if rows else side,
        "flag": os.environ["ARM_A" if side == "a" else "ARM_B"],
        "selector": rows[0]["selector"] if rows else "",
        "trace_arm": rows[0]["trace_arm"] if rows else "",
        "n": len(rows),
        "decisions": decisions,
        "modal_decision": modal,
        "self_disagreement": round(self_dis, 3),
        "stops": stops,
        "spend_ms": quantiles([r["spend_ms"] for r in rows]),
        "receipts": quantiles([r["receipts"] for r in rows]),
        "complete_worlds": quantiles([r["complete_worlds"] for r in rows]),
        "waste_ms": quantiles([r["waste_ms"] for r in rows if r["waste_available"]]),
        "selection_us": quantiles([r["selection_us"] for r in rows]),
        "overrun_count": sum(1 for r in rows if r["overrun_ms"] > 0),
        "overrun_max_ms": max([r["overrun_ms"] for r in rows], default=0),
    }

# ---------------------------------------------------------------------------
# M2d.1 — COVERAGE AND THE REFUSAL.
#
# COVERAGE and DIVERGENCE are two different questions and collapsing them
# would swallow M2b.2's genuine null:
#
#   coverage   = exercised steps / steps                        -- ONE trace
#   divergence = replicates whose PURCHASE ORDER differs / reps  -- the PAIR
#
#   coverage 0, divergence 0  -> VACUOUS, no verdict, exit 5
#   coverage >0, divergence 0 -> MEASURED-NULL, reported, exit 0. THIS IS A
#                                RESULT and must be publishable: it is exactly
#                                M2b.2 s7.5's pre-registered null.
#   coverage >0, divergence >0-> the ordinary verdict machinery
#   coverage 0, divergence >0 -> INERTNESS VIOLATED, a FAILURE, exit 1. The
#                                arms bought different things on steps where
#                                an arm declared itself inert, so the
#                                declaration is wrong and nothing downstream
#                                may be reported. This fourth row is the
#                                reason to trust the first three.
# ---------------------------------------------------------------------------
def coverage_of(side):
    # OVER EVERY REPLICATE, INCLUDING THE QUARANTINED ONES, AND THAT IS NOT A
    # LOOSENING. Coverage is DERIVED FROM THE RECORDED TRACE (M2d.1 decision
    # 8): whether a step's allocation depended on the rule is a fact about
    # bytes the race already wrote, and it does not move with how busy the host
    # was. Every quarantine rule here excludes a replicate from the PAIRED
    # COMPARISON for a reason that is not the treatment — F10's host probe, a
    # race decided at the terminal world-digest key — and that is a statement
    # about comparing two arms' decisions, not about whether either arm's rule
    # fired. Reading coverage off `kept` conflated the two, and at R=1 a single
    # quarantine emptied the sample, made `priced` empty, made `vacuous` False
    # for want of anything to be vacuous ABOUT, and let a run that measured
    # NOTHING exit 0 — observed on this host under load, on the very cold cell
    # accept step m2d1-9a exists to see refused.
    rows = [p[side]["coverage"] or {} for p in pairs]
    steps = sum(int(c.get("steps", 0)) for c in rows)
    # BLOCKER B3: TWO FIGURES. `consulted` is the steps on which the rule's own
    # regime ran — `commit_basis` becomes `reserved` the moment scarcity is
    # true, BEFORE the commit set is built — and `exercised` is the steps whose
    # ALLOCATION depended on it. Only the second may be captioned as coverage.
    consulted = sum(int(c.get("consulted", 0)) for c in rows)
    exercised = sum(int(c.get("exercised", 0)) for c in rows)
    races = sum(1 for c in rows if c.get("applicable") and c.get("known"))
    races_ex = sum(1 for c in rows if int(c.get("exercised", 0)) > 0)
    races_cons = sum(1 for c in rows if int(c.get("consulted", 0)) > 0)
    # W2's retirement, carried through: score_basis=finish is set by exactly
    # the scarcity test that sets commit_basis=reserved, so it IS `consulted`.
    # The identity is asserted rather than assumed, and a break is printed.
    finish_steps = sum(int(c.get("finish_basis_steps", 0)) for c in rows)
    w2_breaks = sum(int(c.get("w2_identity_breaks", 0)) for c in rows)
    # BLOCKER B4: the per-replicate cells this figure was pooled from. A
    # numerator summed over replicates is satisfiable by one step in one of
    # them, so the parts are counted and reported beside the whole.
    vacuous_reps = sum(1 for c in rows
                       if c.get("applicable") and c.get("known")
                       and int(c.get("steps", 0)) > 0 and int(c.get("exercised", 0)) == 0)
    wit = {}
    for c in rows:
        for w in c.get("witnesses") or []:
            acc = wit.setdefault(w["id"], {"name": w.get("name", ""), "steps": 0, "total": 0})
            acc["steps"] += int(w.get("steps", 0))
            acc["total"] += int(w.get("total", 0))
    regimes = sorted({g for c in rows for g in (c.get("regimes") or [])})
    tables = sorted({c.get("cost_regime", "") for c in rows if c.get("cost_regime")})
    first = rows[0] if rows else {}
    return {
        "rule": first.get("rule", ""),
        "baseline": first.get("baseline", ""),
        "applicable": bool(first.get("applicable")),
        "known": bool(first.get("known")),
        "steps": steps, "exercised": exercised, "consulted": consulted,
        "races": races, "races_exercised": races_ex, "races_consulted": races_cons,
        "finish_basis_steps": finish_steps, "w2_identity_breaks": w2_breaks,
        "vacuous_replicates": vacuous_reps,
        "witnesses": wit, "commit_basis": regimes,
        "cost_regime": tables[0] if len(tables) == 1 else ("mixed(%s)" % ",".join(tables) if tables else "unknown"),
        # A trace this binary priced and whose allocation never DEPENDED on its
        # rule. It keys on `exercised` and never on `consulted`: a run in which
        # the rule was asked on every step and mattered on none measured
        # nothing, whatever commit_basis says.
        "vacuous": bool(first.get("applicable") and first.get("known") and steps > 0 and exercised == 0),
    }

COV_A, COV_B = coverage_of("a"), coverage_of("b")
priced = [c for c in (COV_A, COV_B) if c["applicable"] and c["known"] and c["steps"] > 0]
# BLOCKER B4: PER ARM, NOT POOLED. `all` let an exercised arm rescue an inert
# one — the reviewer's "and by the wrong arm" — so ANY priced arm whose rule
# changed nothing it allocated refuses the whole comparison. A comparison in
# which one of the two rules never mattered is a comparison of one rule against
# itself just as surely as one in which neither did.
vacuous = any(c["exercised"] == 0 for c in priced)

# DIVERGENCE is the ORDER; INERTNESS VIOLATION is the SET, and they are two
# different questions for a reason that is measured rather than stylistic.
#
# An arm's declared baseline says what it collapses to when inert, and what
# that collapse MEANS differs by pair. `voc2` collapses to `voc` through voc's
# OWN CODE PATH, so M2b.2's testing bar can demand "receipt set and order
# both". `voc` collapses to the M1 EXHAUSTIVE LADDER, and M2b decision 13's
# null case — accept step 5b — states that equivalence over the EVIDENCE SET:
# the ladder is depth-first by construction and the adaptive arm interleaves by
# score, so at an unbounded budget the two buy the same six receipts in
# different orders, every time, by design. Measured here at B3 = S: identical
# receipts, 1 of 1 replicates order-divergent.
#
# So the FAILURE test is over the multiset of (ordinal, oracle) purchases —
# the claim that holds for every declared baseline — and the ORDER stays a
# reported measurement. Testing the violation on order would have made this
# harness call M2b's own published null case a broken predicate.
def purchase_set(order):
    return sorted(order)


# TRUNCATION IS NOT DIVERGENCE, AND UNDER THE SHIPPED CHARGE BASIS IT IS THE
# ONLY DIFFERENCE TWO INERT ARMS CAN HAVE.
#
# `--budget-basis=actual` charges the pool each receipt's MEASURED wall_ms, so
# where a ladder truncates is a stopwatch reading. On a COLD table it is the
# ONLY thing that varies at all: nothing is priced, `voc2` falls back to `voc`
# on every step, both arms rank identically and walk the same order, and an
# unpriced purchase is affordable while any pool remains — so one arm simply
# gets one rung further than the other before its spend crosses zero.
#
# MEASURED, and this is why the test moved rather than the claim: cold, B =
# 1 500 ms, one replicate — arm a bought `1.guard 2.guard 1.collect 2.collect`
# for 1 661 ms and arm b bought THE SAME FOUR PLUS `1.suite` for 3 275 ms, both
# stopping `S-budget`. One set is a strict SUBSET of the other. Calling that a
# falsification of the inertness declaration says the rule caused a difference
# that the rule could not have caused, and it made accept step m2d1-9a flake
# between exit 5 and exit 1 on the same command.
#
# So the falsifier requires a SYMMETRIC difference: each arm holding a purchase
# the other does not. That is the shape a genuine allocation difference has and
# the shape truncation can never have, and it is the same confound the
# `dependence_unwitnessed` line below already refuses to enforce on, for the
# same measured reason.
def truncation_only(pa, pb):
    a, b = set(pa), set(pb)
    return a <= b or b <= a


divergent = sum(1 for p in kept if p["a"]["purchase_order"] != p["b"]["purchase_order"])
differing = [p for p in kept
             if purchase_set(p["a"]["purchase_order"]) != purchase_set(p["b"]["purchase_order"])]
bought_differently = len(differing)
truncated = sum(1 for p in differing
                if truncation_only(p["a"]["purchase_order"], p["b"]["purchase_order"]))
# The replicates where NEITHER arm's purchases contain the other's: the only
# set difference a rule can be blamed for.
bought_incomparably = bought_differently - truncated
# AND IT IS A TEST OF A PAIR OF DECLARED RULES, not of a rule against a
# baseline that declares nothing. The violation says "the arms bought
# different things on steps where THE ARM DECLARED ITSELF INERT" — which needs
# TWO declarations to falsify. A ladder arm computes no scarcity test and makes
# no such declaration, so a voc-vs-ladder set difference at a budget that bound
# for one arm and not the other is a MEASUREMENT and is reported as one; only a
# pair of priced, inert rules can falsify the predicate.
#
# AND IT KEYS ON `consulted`, NOT ON `exercised` (blocker B3). The declaration
# being falsified is the INERTNESS predicate — "when this arm's own regime did
# not run, it IS its baseline" — so the steps it is a claim about are the ones
# where the regime did not run. Keying it on the new `exercised` figure would
# make it fire on a run where both rules ran, changed nothing they allocated
# and still bought different things — which is a falsification of the
# DEPENDENCE predicate and is reported separately below, under its own name.
#
# AND IT KEYS ON `bought_incomparably`, NOT ON `bought_differently`: a set
# difference that is pure TRUNCATION is a stopwatch artefact of the `actual`
# charge basis and not an allocation the rule produced (see `truncation_only`
# above, with the measurement that moved this line).
inertness_violated = (len(priced) >= 2
                      and all(c["consulted"] == 0 for c in priced)
                      and bought_incomparably > 0)
# THE DEPENDENCE PREDICATE'S OWN FALSIFIER (blocker B3). If every priced arm
# was CONSULTED and none of them DEPENDED, then by the predicate's own claim
# both arms allocated exactly as their baselines would have — so they must have
# bought the same set. A difference means `DependedVOC2` is missing a term.
#
# It is REPORTED and not enforced, and the reason is measured rather than
# squeamish: the shipped charge basis is `actual`, so the pool is charged real
# wall-clock and two arms' purchase sets are not a deterministic function of
# the rule alone. Enforcing it would make the accept gate flake on host jitter,
# which is the rubber stamp F-9 names pointed the other way. Under
# `--budget-basis=predicted` it is a hard falsifier and the line says so.
dependence_unwitnessed = (len(priced) >= 2
                          and all(c["exercised"] == 0 for c in priced)
                          and any(c["consulted"] > 0 for c in priced)
                          and bought_incomparably > 0)

A, B = arm_stats("a"), arm_stats("b")
table = {}
for p in kept:
    key = "%s/%s" % (p["a"]["decision"], p["b"]["decision"])
    table[key] = table.get(key, 0) + 1
agree = sum(n for k, n in table.items() if k.split("/")[0] == k.split("/")[1])
same_subject = sum(1 for p in kept if p["a"]["winner_ordinal"] == p["b"]["winner_ordinal"])

# ROTATION IS NOT NOISE, AND POOLING IT AS NOISE MAKES THE VERDICT FLIP WITH
# THE PARITY OF R. Replicate r rotates the control-plane world order by
# r mod N, so on an N-world fixture an arm whose decision depends on WHICH
# candidate holds the head is a DETERMINISTIC function of the rotation, not a
# stochastic draw. `noise_floor` is each arm's disagreement with its own modal
# decision, so that systematic effect is charged as noise while being the whole
# effect — and the arithmetic decides the verdict: measured on the tie fixture,
# R = 9 gives disagreement 55.6 % against a floor of 44.4 % (NOT a tie) and
# R = 10, same command and same fixture, gives 50.0 % against 50.0 % (a TIE).
#
# So the report stratifies. It also states the structural fact that makes the
# pooled test uninformative here: when one arm is deterministic and the arms'
# modal decisions differ, `disagreement >= 1 - self_disagreement(other)`, so a
# tie is reachable ONLY at an exact 50/50 split — the test cannot return a tie
# for any other split, whatever the underlying effect is.
worlds = max((p["a"]["world_order_len"] for p in kept), default=0)

def rho(p):
    """The EFFECTIVE rotation. The trace records the replicate index; the
    scheduler rotates by r mod N, so two replicates 2 apart on a two-world
    fixture put the same candidate at the head and are the same stratum."""
    return p["a"]["rotation"] % worlds if worlds else 0

by_rot = {}
for p in kept:
    rot = rho(p)
    cell = by_rot.setdefault(rot, {"n": 0, "agree": 0, "table": {}})
    key = "%s/%s" % (p["a"]["decision"], p["b"]["decision"])
    cell["n"] += 1
    cell["table"][key] = cell["table"].get(key, 0) + 1
    if p["a"]["decision"] == p["b"]["decision"]:
        cell["agree"] += 1
rotation = {
    "worlds": worlds,
    "cycles_complete": bool(worlds and R % worlds == 0),
    "strata": {str(k): v for k, v in sorted(by_rot.items())},
    "deterministic_arms": [
        side for side, arm in (("a", A), ("b", B))
        if arm["n"] and len(by_rot) > 1 and all(
            len({p[side]["decision"] for p in kept if rho(p) == rot}) <= 1
            for rot in by_rot
        )
    ],
}

# THE ANECDOTE RULE, ENFORCED BY THE HARNESS RATHER THAN BY DISCIPLINE.
# R = 1 measures one draw of a process whose own noise floor is unmeasured, so
# no verdict is printed below R = 3. Every number M2b's BUILDLOG quotes was
# produced at R = 1 and is, by this rule, an anecdote.
verdict_ok = R >= 3 and len(kept) >= 3
verdict = {
    "oracle_budget_matched_ms": matched,
    "budget_basis": os.environ["BASIS"],
    "parallel": int(os.environ["PARALLEL"]),
    "level": os.environ["LEVEL"] or "explicit",
    "reference_spend_ms": ref_spend,
    "reference_minspend_ms": ref_min,
    "replicates": R,
    "kept": len(kept),
    "quarantined": {k: len(v) for k, v in quarantine.items()},
    "paired_table": table,
    "agree": agree,
    "same_subject": same_subject,
    "noise_floor": max(A["self_disagreement"], B["self_disagreement"]),
    "rotation": rotation,
    "verdict_available": verdict_ok,
    "build": os.environ["BUILD"],
    # M2d.1: printed ALWAYS, including at 100 %.
    "coverage": {"a": COV_A, "b": COV_B},
    "purchase_order_divergence": divergent,
    "purchase_set_divergence": bought_differently,
    # Split out, because the falsifiers key on the second and a reader of the
    # artifact must be able to see which of the two moved.
    "purchase_set_truncation": truncated,
    "purchase_set_incomparable": bought_incomparably,
    "vacuous": vacuous,
    "inertness_violated": inertness_violated,
}
if verdict_ok:
    disagree_rate = 1 - agree / len(kept)
    verdict["disagreement_rate"] = round(disagree_rate, 3)
    verdict["tie"] = disagree_rate <= verdict["noise_floor"]
    # THE POOLED TIE TEST IS REPORTED WITH ITS OWN PRECONDITIONS ATTACHED.
    # It is only interpretable when the replicates cover a whole number of
    # rotation cycles (otherwise one rotation is over-represented and the rate
    # moves with the parity of R) and when neither arm is a deterministic
    # function of the rotation (otherwise the "noise floor" is the effect).
    verdict["tie_interpretable"] = bool(
        rotation["cycles_complete"] and not rotation["deterministic_arms"])

if as_json:
    print(json.dumps({"a": A, "b": B, "verdict": verdict, "pairs": kept,
                      "quarantine": quarantine}, sort_keys=True, indent=1))
    if inertness_violated:
        sys.exit(1)
    if vacuous:
        sys.exit(5)
    sys.exit(0 if (verdict_ok or not strict) else 3)

def q(x, unit=""):
    if not x or x.get("n", 0) == 0:
        return "-"
    return "%g%s [%g-%g]" % (x["median"], unit, x["min"], x["max"])

print("schedule-compare — ORACLE-BUDGET-MATCHED at %d ms, basis %s, k=%d, R=%d"
      % (matched, verdict["budget_basis"], verdict["parallel"], R))
print("  reference (unbudgeted ladder, traced): spent %d ms; allocation bound minspend %s"
      % (ref_spend, ("%d ms" % ref_min) if ref_min else "not computed"))
print("  build %s" % verdict["build"][:19])
print()
print("  %-6s %-24s %-9s %-10s %14s %10s %10s  %s"
      % ("ARM", "FLAG", "SELECTOR", "MODAL", "SPEND (med)", "RECEIPTS", "COMPLETE", "SELF-DISAGREE"))
for arm in (A, B):
    print("  %-6s %-24s %-9s %-10s %14s %10s %10s  %.0f%%"
          % (arm["arm"], arm["flag"], arm["selector"] or "-", arm["modal_decision"] or "-",
             q(arm["spend_ms"], " ms"), q(arm["receipts"]), q(arm["complete_worlds"]),
             arm["self_disagreement"] * 100))
print()
for arm in (A, B):
    print("  arm %s — decisions %s, stops %s" % (arm["arm"], arm["decisions"], arm["stops"]))
    print("           waste %s, selection %s, overruns %d (max %d ms)"
          % (q(arm["waste_ms"], " ms"), q(arm["selection_us"], " us"),
             arm["overrun_count"], arm["overrun_max_ms"]))
print()
def pct_text(n, d):
    # BLOCKER B4: a nonzero numerator NEVER prints as 0 %. The probe that
    # merged 99 vacuous races with one exercised step printed `1 of 199 steps
    # (0%)`, and a reader who saw the parenthesis read a zero nobody measured.
    if d <= 0:
        return "--"
    p = n * 100 // d
    return "<1%" if (p == 0 and n > 0) else "%d%%" % p

def cov_line(side, cov, arm):
    if not cov["steps"]:
        if not cov["applicable"]:
            return "  arm %s (%s): coverage -- (computes no scarcity test)" % (side, arm["selector"] or "-")
        if not cov["known"]:
            return "  arm %s (%s): coverage unknown (pre-M2b.2 trace)" % (side, arm["selector"] or "-")
        return "  arm %s (%s): coverage -- (no allocation trace recorded)" % (side, arm["selector"] or "-")
    return ("  arm %s (%s, baseline %s): EXERCISED %d of %d steps (%s), %d of %d races"
            % (side, cov["rule"] or "-", cov["baseline"] or "-",
               cov["exercised"], cov["steps"], pct_text(cov["exercised"], cov["steps"]),
               cov["races_exercised"], cov["races"]))

print("COVERAGE — did the ALLOCATION DEPEND on the rule under test? (M2d.1 decision 10 as amended")
print("by blocker B3; printed always, including at 100 %)")
for side, cov, arm in (("a", COV_A, A), ("b", COV_B, B)):
    print(cov_line(side, cov, arm))
    if cov["steps"]:
        # B3's demoted figure, printed directly beneath the headline. The gap
        # between the two is the whole of what the first version got wrong.
        print("      consulted (the rule's own regime ran, NOT coverage): %d of %d steps (%s), %d of %d races"
              % (cov["consulted"], cov["steps"], pct_text(cov["consulted"], cov["steps"]),
                 cov["races_consulted"], cov["races"]))
        if cov["vacuous_replicates"]:
            print("      VACUOUS PARTS: %d of %d replicate(s) exercised the rule on NO step — a pooled"
                  % (cov["vacuous_replicates"], cov["races"]))
            print("        numerator hides that, so the parts are printed beside the whole (B4)")
    for wid in sorted(cov["witnesses"]):
        w = cov["witnesses"][wid]
        extra = ""
        if wid == "W3" and w["total"] > w["steps"]:
            extra = "   (|C| = 0 on %d: equal shares, M2b decision 8)" % (w["total"] - w["steps"])
        print("      %s %-26s %d of %d%s" % (wid, w["name"], w["steps"], w["total"], extra))
    if cov["steps"] and cov["rule"] == "voc2":
        print("      W2 finish denominator      %d of %d   RETIRED AS A WITNESS: score_basis=finish is"
              % (cov["finish_basis_steps"], cov["steps"]))
        print("        set by exactly the scarcity test that sets commit_basis=reserved, so it IS the")
        print("        consulted set above — four witnesses were two facts (B4)")
        if cov["w2_identity_breaks"]:
            print("      W2 IDENTITY BROKEN on %d step(s): the construction that retires it no longer"
                  % cov["w2_identity_breaks"])
            print("        holds, and the consulted figure must be re-derived before it is quoted")
    if cov["commit_basis"]:
        print("      commit_basis observed: %s" % " / ".join(cov["commit_basis"]))
    print("      cost table: %s" % (cov["cost_regime"] or "unknown").upper())
print("  purchase-order divergence: %d of %d replicate(s) bought a different (ordinal, oracle) SEQUENCE"
      % (divergent, len(kept)))
print("  purchase-set divergence:   %d of %d replicate(s) bought a different (ordinal, oracle) SET"
      % (bought_differently, len(kept)))
if truncated:
    print("      of which %d is TRUNCATION (one arm's purchases are a SUBSET of the other's): the"
          % truncated)
    print("      `%s` charge basis pays each receipt's measured wall_ms, so the two arms stopped at"
          % os.environ["BASIS"])
    print("      different points along the SAME order. %d replicate(s) bought INCOMPARABLE sets,"
          % bought_incomparably)
    print("      which is the only shape an allocation difference can take.")
if bought_differently and len(priced) < 2:
    print("      (only %d arm declares an inertness predicate here, so a set difference is a"
          % len(priced))
    print("       MEASUREMENT and not a falsification: it takes two declarations to falsify one.)")
print()

if inertness_violated:
    print("INERTNESS VIOLATED: the arms bought INCOMPARABLE SETS on %d replicate(s) whose every step"
          % bought_incomparably)
    print("  was declared INERT. The inertness predicate in M2d.1 decision 5 is WRONG for these arms,")
    print("  the coverage number above is not measuring what it claims, and NOTHING DOWNSTREAM MAY BE")
    print("  REPORTED. This is a failure of the coverage mechanism, not a result about the arms.")
    sys.exit(1)

if dependence_unwitnessed:
    print("DEPENDENCE UNWITNESSED: every priced arm was CONSULTED and none of them DEPENDED, yet the")
    print("  arms bought INCOMPARABLE SETS on %d replicate(s). By the dependence predicate's own claim"
          % bought_incomparably)
    print("  both arms allocated exactly as their baselines would have, so a set difference means the")
    print("  predicate is MISSING A TERM. Reported and not enforced ONLY because the shipped charge")
    print("  basis is `actual`, so a purchase set is not a deterministic function of the rule alone;")
    print("  under --budget-basis=predicted this line is a falsification of blocker B3's repair.")
    print()

if vacuous:
    dead = [c for c in priced if c["exercised"] == 0]
    print("VACUOUS (%d of %d priced arm(s) exercised the rule on NO step): NO VERDICT" % (len(dead), len(priced)))
    for c in dead:
        print("  --selector=%-6s EXERCISED %d of %d steps, CONSULTED %d of %d"
              % (c["rule"], c["exercised"], c["steps"], c["consulted"], c["steps"]))
    observed = sorted({g for c in dead for g in c["commit_basis"]}) or ["no commit_basis at all"]
    if all(c["consulted"] == 0 for c in dead):
        print("  the rule under test never fired: commit_basis was %s on every" % " / ".join(observed))
        print("  step of every replicate, so each arm ran its own BASELINE and this is not a comparison")
        print("  of two rules:")
    else:
        # B3: THE TWO REFUSALS ARE DIFFERENT SENTENCES AND MAY NOT BE
        # INTERCHANGED. "the rule never ran" is a claim about the code path;
        # this one is a claim about the allocation, and it is the true one for
        # a `reserved`-on-every-step run whose commit set was always empty.
        print("  the rule under test RAN and CHANGED NOTHING IT ALLOCATED: commit_basis was %s,"
              % " / ".join(observed))
        print("  and no step recorded a commit set, a withheld pass outcome, a lapsed hard-gate")
        print("  override or a moved queue head — so every allowance and every order was the")
        print("  baseline's, and this is not a comparison of two rules:")
    for c in dead:
        print("    --selector=%-6s allocated exactly as %s would have"
              % (c["rule"], c["baseline"] or "its undeclared baseline"))
    print("  Warm the workspace (--warmup auto / --warmup N) or drop the claim.")
    print()
    print("  NOTE: on a cold workspace no kind carries a local fit, so an unpriced purchase is")
    print("  affordable while any pool remains and THE BUDGET DOES NOT BIND AT ALL — measured")
    print("  2 164 ms spent against a 1 500 ms bound, stopping S-empty rather than S-budget.")
    sys.exit(5)

print("  paired decisions (arm a / arm b): %s" % (table or "-"))
print("  same subject (candidate ordinal): %d of %d" % (same_subject, len(kept)))
for name, reps in quarantine.items():
    if reps:
        print("  QUARANTINED %s: replicates %s (excluded from the paired comparison)" % (name, reps))
print()

# AN EMPTY PAIRED SAMPLE SAYS SO IN ITS OWN WORDS, and it says so through the
# NO-VERDICT channel rather than through exit 5. Every quarantine rule here
# removes a replicate for a reason that is not the treatment, and at R=1 a
# single quarantine empties the sample; the script then printed `ANECDOTE
# (R=1)`, which is true of the sample size and silent about the fact that
# nothing was compared at all.
#
# EXIT 5 IS NOT THE CODE FOR IT, and giving it that code was a mistake caught
# by the gate rather than by argument. `5` has ONE documented meaning here and
# in `.claude/skills/gate/SKILL.md` — VACUOUS: the rule under test never fired
# — and accept steps m2d1-9a and 9b are a PAIR written against exactly that
# meaning. Overloading it made a WARMED run whose coverage was 100 % on one arm
# and 40 % on the other report the code for "the warming does not warm", on a
# host that was merely busy. A second meaning on a refusal code is how a
# refusal stops being read.
#
# So the banner is loud, no verdict follows it, and the exit is the anecdote
# rule's own: `--strict` turns no-verdict into 3, and without it the caller
# reads the banner. Placed AFTER the vacuity refusal on purpose: a cold cell
# whose only replicate was quarantined is refused for the reason that is TRUE
# of it — its rule never fired — and not for the sampling accident on top.
if pairs and not kept:
    print("NO REPLICATE SURVIVED QUARANTINE: all %d replicate(s) were excluded, so no arm was"
          % len(pairs))
    print("  compared to any other. The coverage figures above are still real — they are read off")
    print("  each race's recorded trace, which no quarantine touches — but the PAIRED comparison")
    print("  has an EMPTY SAMPLE and there is no verdict of any kind below:")
    for name, reps in quarantine.items():
        if reps:
            print("    %-24s replicates %s" % (name, reps))
    print("  Re-run with more replicates, or on a quieter host if the probe is what excluded them.")
    sys.exit(3 if strict else 0)
if not verdict_ok:
    print("  ANECDOTE (R=%d): no verdict." % R)
    print("  A single run at a budget level measures one draw of a process whose own")
    print("  noise floor is unmeasured. Re-run with --replicates 3 or more; every number")
    print("  M2b's BUILDLOG quotes was produced at R=1 and is, by this rule, an anecdote.")
    sys.exit(3 if strict else 0)

print("verdict:")
print("  disagreement rate: %.0f%% (%d of %d replicates decided differently)"
      % (verdict["disagreement_rate"] * 100, len(kept) - agree, len(kept)))
print("  noise floor:       %.0f%% (the larger arm's self-disagreement)" % (verdict["noise_floor"] * 100))
rot = verdict["rotation"]
if rot["worlds"]:
    print("  by rotation (N=%d worlds, rho = r mod N):" % rot["worlds"])
    for k in sorted(rot["strata"], key=int):
        cell = rot["strata"][k]
        print("    rho=%s  n=%d  agree %d  %s" % (k, cell["n"], cell["agree"], cell["table"]))
if not rot["cycles_complete"]:
    print("  ROTATION CYCLES INCOMPLETE: R=%d over %d worlds. One rotation is over-represented," % (R, rot["worlds"]))
    print("  so the pooled rate and the noise floor both move with the parity of R. Re-run with")
    print("  R a multiple of %d before quoting either number." % max(rot["worlds"], 1))
if rot["deterministic_arms"]:
    print("  ARM(S) %s DECIDE DETERMINISTICALLY GIVEN THE ROTATION: their 'self-disagreement' is"
          % ", ".join(a.upper() for a in rot["deterministic_arms"]))
    print("  the rotation effect, not noise, so the disagreement rate and the noise floor below are")
    print("  measuring the SAME THING. Read the stratified table above instead. And note the")
    print("  structural limit: when one arm is deterministic and the arms' modal decisions differ,")
    print("  a tie is reachable only at an exact 50/50 split, whatever the underlying effect is.")
if not verdict["tie_interpretable"]:
    print("  => the pooled tie test below is NOT interpretable for this cell.")
if verdict["tie"]:
    print("  TIE: the difference between the arms does not exceed both arms' own")
    print("       self-disagreement. Reported as a tie, in the text and in the abstract.")
else:
    print("  The arms differ by more than the measured noise floor. That is a MEASUREMENT,")
    print("  not a pass or a fail: withholding monotonicity says adaptivity cannot cause a")
    print("  false ADMISSION, but it can cause a false REJECTION, and only labelled")
    print("  outcomes (M2d) can say which of the two this is.")
print()
print("  NOT budget-matched in PRD §11's sense: tokens, runner time and selection cost")
print("  are uncharged (selection cost is measured and reported). The label is")
print("  ORACLE-BUDGET-MATCHED and it belongs on every plot and in every caption.")
PY
