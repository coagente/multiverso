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
  --warmup N             warm-up races used to fit the cost table (default: 2)
  --probe-tolerance-bp N host-probe tolerance in basis points (default: 2000 = 20%)
  --arm-a FLAG           first arm (default: --schedule=adaptive)
  --arm-b FLAG           second arm (default: --schedule=fixed-budget)
  --budget-flag FLAG     intent flag carrying max_oracle_ms (default: --budget-oracle-ms)
  --keep DIR             keep the workspaces in DIR instead of a temp dir
  --json                 emit the comparison as one JSON object
  -h, --help             this message

exit codes: 0 compared, 1 a run or an assertion failed, 2 usage,
            3 no verdict (R < 3) under --strict,
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

A, B = arm_stats("a"), arm_stats("b")
table = {}
for p in kept:
    key = "%s/%s" % (p["a"]["decision"], p["b"]["decision"])
    table[key] = table.get(key, 0) + 1
agree = sum(n for k, n in table.items() if k.split("/")[0] == k.split("/")[1])
same_subject = sum(1 for p in kept if p["a"]["winner_ordinal"] == p["b"]["winner_ordinal"])

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
    "verdict_available": verdict_ok,
    "build": os.environ["BUILD"],
}
if verdict_ok:
    disagree_rate = 1 - agree / len(kept)
    verdict["disagreement_rate"] = round(disagree_rate, 3)
    verdict["tie"] = disagree_rate <= verdict["noise_floor"]

if as_json:
    print(json.dumps({"a": A, "b": B, "verdict": verdict, "pairs": kept,
                      "quarantine": quarantine}, sort_keys=True, indent=1))
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
print("  paired decisions (arm a / arm b): %s" % (table or "-"))
print("  same subject (candidate ordinal): %d of %d" % (same_subject, len(kept)))
for name, reps in quarantine.items():
    if reps:
        print("  QUARANTINED %s: replicates %s (excluded from the paired comparison)" % (name, reps))
print()
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
