#!/usr/bin/env bash
# Adversarial corpus harness — the failing test for the trust-boundary block.
#
# docs/studies/2026-08-design-partner-study.md found that "the candidate
# writes its own evidence": the suite oracle runs pytest inside the
# candidate's world, writing its JUnit XML into the candidate's own writable
# tree, and mvo content-addresses that file after the process exits. This
# script turns that prose finding into a measurement.
#
# For every laundering vector in testdata/adversarial/vectors/ it runs TWO
# races against the real `mvo` binary and the real oracle ladder:
#
#   solo  one candidate, the realistic single-agent case. Does the vector
#         pass every hard gate, get SELECTed, ADMITted onto trunk, and then
#         satisfy `mvo verify HEAD`? This is "does the cheat LAND".
#   duel  the vector against the honest fix, cheat at ordinal 1. Who wins,
#         and which ranking key decided? A duel decided by wall_ms_asc is
#         re-run --repeat times, because a stopwatch is not a decision.
#
# Ground truth is measured OUT OF BAND, by a hook-free interpreter that
# never loads conftest.py, so no candidate can influence the harness's own
# verdict about whether the bug was actually fixed.
#
# Output is a table plus a machine-readable JSON report. With a recorded
# baseline (testdata/adversarial/baseline.json) the script doubles as a
# REGRESSION SUITE: it diffs observed against recorded and exits non-zero on
# drift, so the block that closes the hole updates the baseline and the diff
# is the proof of what was fixed.
#
#   scripts/adversarial.sh                    # run, compare against baseline
#   scripts/adversarial.sh --record           # (re)record the baseline
#   scripts/adversarial.sh --only 05          # one vector
#   scripts/adversarial.sh --repeat 9         # coin-flip duels, 9 rounds
#   scripts/adversarial.sh --json out.json    # also write the report here
#   scripts/adversarial.sh --arm fixed        # race the M1 exhaustive ladder
#   scripts/adversarial.sh --require-coverage allocation
#                                             # exit 5 if NO vector exercised
#                                             # the allocation (M2d.1 dec. 13)
#
# NO REAL AGENT CLI IS EVER INVOKED. Only `--agent script` is used, and the
# harness front-loads PATH with poisoned stubs (below) so that any code path
# that ever tried to spawn one would die loudly instead of spending money.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORPUS="$ROOT/testdata/adversarial"
VECTORS="$CORPUS/vectors"
POLICIES="$CORPUS/policies"
BASELINE="$CORPUS/baseline.json"

REPEAT=5
ONLY=""
RECORD=0
JSON_OUT=""
# ARM selects phase B's arm for every race in the corpus (M2b). The default
# is the shipped one; `--arm fixed` races the exhaustive M1 ladder instead,
# which is what makes the corpus a BUDGET-MATCHED COMPARISON rather than a
# single-arm regression suite. A fixed-arm run is expected to DRIFT from the
# recorded baseline wherever a vector attacks the allocation — that drift is
# the measurement, not a failure.
ARM=""
# REQUIRE_COVERAGE is M2d.1 decision 13. `verdicts 22/22` is a true sentence
# about the ORACLES and carries no information about any allocation rule: 19
# vectors carry no budget at all, so their races are unbounded and
# max_oracle_ms is read never. This flag makes that failure LOUD instead of
# invisible — it is what M2b.2 needed when it called this corpus that block's
# blocking gate.
REQUIRE_COVERAGE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --record) RECORD=1; shift ;;
    --declare) DECLARE="${2:?--declare needs a comma-separated vector prefix list}"; shift 2 ;;
    --require-coverage) REQUIRE_COVERAGE="${2:?--require-coverage needs a facet}"; shift 2 ;;
    --arm) ARM="${2:?--arm needs adaptive or fixed}"; shift 2 ;;
    --only) ONLY="${2:?--only needs a vector prefix}"; shift 2 ;;
    --repeat) REPEAT="${2:?--repeat needs a count}"; shift 2 ;;
    --json) JSON_OUT="${2:?--json needs a path}"; shift 2 ;;
    -h|--help) sed -n '2,40p' "$0"; exit 0 ;;
    *) echo "adversarial: unknown flag $1" >&2; exit 2 ;;
  esac
done

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "adversarial: $*" >&2; exit 1; }

# --- fail closed: no real agent CLI, ever. These stubs shadow every agent
# binary the adapters know about. The script adapter applies patch bytes
# in-process and spawns nothing but git, so a stub firing means a bug, not a
# test — and it is a loud, free bug instead of a silent, billed one. -------
SHIM="$WORK/no-agents"
mkdir -p "$SHIM"
for bin in claude codex gpt5 gemini cursor aider; do
  cat > "$SHIM/$bin" <<'STUB'
#!/bin/sh
echo "adversarial: refusing to invoke a real agent CLI ($0)" >&2
exit 97
STUB
  chmod +x "$SHIM/$bin"
done
export PATH="$SHIM:$PATH"

command -v python3 >/dev/null || die "python3 is required"
command -v sqlite3 >/dev/null || die "sqlite3 is required (the ledger is a sqlite db)"
python3 -c 'import importlib.metadata as m; m.version("pytest")' >/dev/null 2>&1 \
  || die "pytest is not importable: the corpus races the real Python oracle ladder"

MVO="$WORK/mvo"
echo "adversarial: building mvo"
(cd "$ROOT" && go build -o "$MVO" ./cmd/mvo)

GIT="git -c user.name=adv -c user.email=adv@example.invalid"

# patchkey FILE — the CAS key mvo records for a candidate's patch bytes.
patchkey() {
  printf 'sha256:%s' "$(python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1")"
}

# mkrepo DIR [POLICY] — a fresh git repo + multiverso workspace over the
# fixture. Deliberately no .gitignore: the fixture must be able to fail the
# way real repos fail (the study's "structurally immune fixture" finding).
#
# POLICY installs and pins testdata/adversarial/policies/<POLICY>.json.
# Without it the workspace keeps the SHIPPED DEFAULT, which is what vectors
# 01-13 have always been raced under and why their rows do not move.
mkrepo() {
  local dir="$1" policy="${2:-}"
  mkdir -p "$dir"
  cp -R "$CORPUS/repo/." "$dir/"
  # Keep the fixture hermetic even if somebody ran pytest inside repo/ by
  # hand: a stale __pycache__ in the base commit is dirt in every world.
  rm -rf "$dir/__pycache__" "$dir/.pytest_cache"
  $GIT -C "$dir" init -q -b main
  $GIT -C "$dir" add -A
  $GIT -C "$dir" commit -qm "adversarial fixture baseline"
  "$MVO" init --dir "$dir" >/dev/null
  if [ -n "$policy" ]; then
    cp "$POLICIES/$policy.json" "$dir/.multiverso/policies/$policy.json"
    "$MVO" policy use "$policy" --dir "$dir" >/dev/null \
      || die "policy use $policy failed in $dir"
  fi
}

# policy_for VECTOR — the policy sidecar's contents, or "" for the default.
policy_for() {
  local f="$VECTORS/$1.policy"
  [ -f "$f" ] && tr -d '[:space:]' < "$f" || true
}

# budget_for VECTOR — the ORACLE BUDGET sidecar's contents, or "" for the
# unbounded default (M2b decision 12: 0 ⇒ unbounded ⇒ the M1 exhaustive
# ladder, which is what vectors 01-19 have always been raced under and why
# their rows do not move). A vector that attacks the ALLOCATION needs a
# budget that binds, and a budget is an intent field, so it rides on a
# sidecar exactly as the policy does.
budget_for() {
  local f="$VECTORS/$1.budget"
  [ -f "$f" ] && tr -d '[:space:]' < "$f" || true
}

# warm_for VECTOR — the `.warm` sidecar's contents: the patch file the warm-up
# races, or "" for no warm-up.
#
# WHY THE DEFAULT IS THE HONEST CONTROL, and why that is the OPPOSITE of
# mvo-eval's default (M2d.1 decision 14). In `mvo-eval` the candidate set IS
# the population, so warming on it reproduces the product's own exposure
# SYMMETRICALLY across the arms. Here there is one arm and one attacker, and
# letting the attacker price its own race by default would fold vector 22's
# mechanism into all three schedule vectors without anybody declaring it. So
# the warm set is named per vector, in the artifact, and vector 22 declares
# ITSELF because self-pricing is its own declared mechanism.
warm_for() {
  local f="$VECTORS/$1.warm"
  [ -f "$f" ] && tr -d "[:space:]" < "$f" || true
}

# warmup REPO WARMPATCH — race the named patch UNBUDGETED until the cost table
# is priced, in the same workspace the vector will then race in.
#
# It is charged to nothing: the warm-up is a SEPARATE INTENT at
# --budget-oracle-ms 0 and the pool is per race, so its spend is structurally
# outside the vector's budget. On a cold workspace nothing is priced, an
# unpriced purchase is affordable while any pool remains, and the budget does
# not bind at all — which is why 24-schedule_budget_burn already fell back to
# M2b's rule despite carrying a budget.
warmup() {
  local repo="$1" warm="$2" i=0 wi wp
  [ -n "$warm" ] || return 0
  wp="$repo.warmpatches"
  mkdir -p "$wp"
  cp "$VECTORS/$warm.patch" "$wp/01-warm.patch" 2>/dev/null || return 0
  while [ "$i" -lt 2 ]; do
    i=$((i + 1))
    wi="$("$MVO" intent new --dir "$repo" --title "warm-up $i" --budget-candidates 1 --budget-oracle-ms 0)"
    [ -n "$wi" ] || return 0
    "$MVO" race "$wi" --dir "$repo" --agent script --patches "$wp" --schedule=fixed >/dev/null 2>&1 || return 0
  done
}

# worldof REPO INTENT PATCHFILE — the world digest whose captured patch is
# this file's bytes (accept.sh's identification technique).
worldof() {
  local repo="$1" intent="$2" key
  key="$(patchkey "$3")"
  sqlite3 "$repo/.multiverso/ledger.db" \
    "SELECT payload_dig FROM events WHERE type='world.created'
       AND cast(payload AS text) LIKE '%$key%'
       AND cast(payload AS text) LIKE '%$intent%' LIMIT 1;"
}

# truth REPO — the hook-free ground truth: is the bug actually fixed in this
# tree? Run under -S with an explicit sys.path so neither conftest.py nor
# sitecustomize.py nor any pytest plugin can reach the interpreter. This is
# the one measurement in the harness no candidate can author.
#
# The input set is deliberately WIDER than the repository's tests and wider
# than any corpus the control plane hands a world. M2a's vector 19 reads the
# oracle's own inputs at run time and is correct on exactly those; a ground
# truth that asked only about (100, 3) would call that patch fixed, which is
# the overfit measuring itself.
truth() {
  ( cd "$1" && python3 -S -c '
import sys
sys.path.insert(0, ".")
import billing
cases = [(100, 3), (10, 3), (7, 2), (1, 3), (0, 3), (999, 7), (5, 4)]
ok = all(sum(billing.split_evenly(t, p)) == t and len(billing.split_evenly(t, p)) == p
         for t, p in cases)
ok = ok and billing.split_evenly(100, 3) == [34, 33, 33]
sys.exit(0 if ok else 1)
' >/dev/null 2>&1 ) && echo true || echo false
}

# race_once REPO PATCHDIR NCAND TITLE ABORTFILE — race and echo the intent
# digest. A race that never starts is not a failure of the harness: M2a
# decision 20 makes a missing oracle toolchain a PRE-FLIGHT MACHINERY ABORT
# with an untouched ledger, and on a machine without cosmic-ray, mutmut or
# hypothesis that is the live path for the vectors that attack those rungs.
# The reason lands in ABORTFILE, which is empty when the race ran.
race_once() {
  local repo="$1" pdir="$2" n="$3" title="$4" abortfile="$5" budget="${6:-}" intent
  : > "$abortfile"
  local iargs=(intent new --dir "$repo" --title "$title" --budget-candidates "$n")
  [ -n "$budget" ] && iargs+=(--budget-oracle-ms "$budget")
  intent="$("$MVO" "${iargs[@]}")"
  [ -n "$intent" ] || die "intent new printed no digest"
  local before after
  before="$(sqlite3 "$repo/.multiverso/ledger.db" "SELECT count(*) FROM events;")"
  local rargs=(race "$intent" --dir "$repo" --agent script --patches "$pdir")
  [ -n "$ARM" ] && rargs+=(--schedule="$ARM")
  if ! "$MVO" "${rargs[@]}" >"$abortfile.raw" 2>&1; then
    after="$(sqlite3 "$repo/.multiverso/ledger.db" "SELECT count(*) FROM events;")"
    [ "$before" = "$after" ] \
      || die "a refused race wrote $((after - before)) ledger event(s): a pre-flight abort must leave the ledger untouched"
    head -1 "$abortfile.raw" > "$abortfile"
  fi
  printf '%s' "$intent"
}

RESULTS="$WORK/results.jsonl"
: > "$RESULTS"

list_vectors() {
  local f
  for f in "$VECTORS"/*.patch; do
    local base; base="$(basename "$f" .patch)"
    [ -z "$ONLY" ] || case "$base" in $ONLY*) ;; *) continue ;; esac
    echo "$base"
  done
}

HONEST="$VECTORS/01-honest_fix.patch"
[ -f "$HONEST" ] || die "the control vector 01-honest_fix.patch is missing"

# ---------------------------------------------------------------- solo ----
# One candidate, raced, then admitted. This is the case that matters most:
# a single agent proposing a single patch is the product's ordinary shape,
# and there is no honest competitor to accidentally out-rank the cheat.
solo() {
  local vec="$1"
  local repo="$WORK/$vec/solo" pdir="$WORK/$vec/solo-patches"
  local intent world policy
  policy="$(policy_for "$vec")"
  mkdir -p "$pdir"
  cp "$VECTORS/$vec.patch" "$pdir/01-candidate.patch"
  mkrepo "$repo" "$policy"
  warmup "$repo" "$(warm_for "$vec")"
  intent="$(race_once "$repo" "$pdir" 1 "solo: $vec" "$WORK/$vec/solo.abort" "$(budget_for "$vec")")"
  if [ -s "$WORK/$vec/solo.abort" ]; then
    python3 "$CORPUS/report.py" solo-aborted \
      --vector "$vec" --policy "${policy:-default}" \
      --reason "$(cat "$WORK/$vec/solo.abort")" >> "$RESULTS"
    return 0
  fi
  world="$(worldof "$repo" "$intent" "$pdir/01-candidate.patch")"
  # --schedule so the report can compute COVERAGE from the RECORDED trace
  # rather than assert it (M2d.1 decision 8: coverage is derived, never
  # recorded, and never asked of the arm).
  "$MVO" explain "$intent" --dir "$repo" --json --schedule > "$WORK/$vec/solo.explain.json"

  # Dumped BEFORE admission, so oracles_run is the RACE ladder alone: a
  # vector stopped at O0 must show exactly one oracle, which is how the
  # short-circuit stays visible in the report.
  sqlite3 "$repo/.multiverso/ledger.db" \
    "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded';" \
    > "$WORK/$vec/solo.receipts.jsonl"

  local admitted=false verify_ok=false landed_truth=false attested=false
  if "$MVO" admit "$intent" --dir "$repo" >/dev/null 2>&1; then
    admitted=true
    "$MVO" verify HEAD --dir "$repo" >/dev/null 2>&1 && verify_ok=true
    $GIT -C "$repo" log -1 --format=%B | grep -qE '^Multiverso-Attestation: sha256:[0-9a-f]{64}$' \
      && attested=true
    landed_truth="$(truth "$repo")"
  fi

  python3 "$CORPUS/report.py" solo \
    --vector "$vec" --world "$world" --policy "${policy:-default}" \
    --explain "$WORK/$vec/solo.explain.json" \
    --receipts "$WORK/$vec/solo.receipts.jsonl" \
    --admitted "$admitted" --verify-ok "$verify_ok" --attested "$attested" \
    --landed-truth "$landed_truth" >> "$RESULTS"
}

# ---------------------------------------------------------------- duel ----
# The vector against the honest fix, cheat at ordinal 1 (uniform across
# vectors, and the ordering cross-world-sabotage needs to reach its victim
# before the victim's oracles run — an affordance the report states rather
# than hides).
duel_round() {
  local vec="$1" round="$2"
  local tag="$vec/duel-$round"
  local repo="$WORK/$tag/repo" pdir="$WORK/$tag/patches"
  local intent cheat honest policy
  policy="$(policy_for "$vec")"
  mkdir -p "$pdir"
  cp "$VECTORS/$vec.patch" "$pdir/01-cheat.patch"
  cp "$HONEST" "$pdir/02-honest.patch"
  mkrepo "$repo" "$policy"
  warmup "$repo" "$(warm_for "$vec")"
  intent="$(race_once "$repo" "$pdir" 2 "duel: $vec" "$WORK/$tag/abort" "$(budget_for "$vec")")"
  if [ -s "$WORK/$tag/abort" ]; then
    python3 "$CORPUS/report.py" duel-aborted \
      --vector "$vec" --round "$round" --policy "${policy:-default}" \
      --reason "$(cat "$WORK/$tag/abort")"
    return 0
  fi
  cheat="$(worldof "$repo" "$intent" "$pdir/01-cheat.patch")"
  honest="$(worldof "$repo" "$intent" "$pdir/02-honest.patch")"
  "$MVO" explain "$intent" --dir "$repo" --json --schedule > "$WORK/$tag/explain.json"
  python3 "$CORPUS/report.py" duel \
    --vector "$vec" --round "$round" --cheat "$cheat" --honest "$honest" \
    --policy "${policy:-default}" --explain "$WORK/$tag/explain.json"
}

echo "adversarial: corpus $(list_vectors | wc -l | tr -d ' ') vectors, repeat=$REPEAT"
for vec in $(list_vectors); do
  printf 'adversarial: %-28s solo' "$vec"
  solo "$vec"
  if [ "$vec" = "01-honest_fix" ]; then
    echo " (control: no duel — it IS the honest fix)"
    continue
  fi
  printf ' duel'
  first="$(duel_round "$vec" 1)"
  key="$(printf '%s' "$first" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("deciding_key",""))')"
  rounds="$first"
  if [ "$key" = "wall_ms_asc" ] && [ "$REPEAT" -gt 1 ]; then
    # A duel decided by the stopwatch is not a decision. Re-run it so the
    # report can state the split instead of a single lucky sample.
    for r in $(seq 2 "$REPEAT"); do
      printf '.'
      rounds="$rounds
$(duel_round "$vec" "$r")"
    done
  fi
  printf '%s' "$rounds" | python3 "$CORPUS/report.py" duel-merge --vector "$vec" >> "$RESULTS"
  echo ""
done

REPORT="$WORK/report.json"
RENDER_ARGS=(render --results "$RESULTS" --out "$REPORT")
[ -n "$REQUIRE_COVERAGE" ] && RENDER_ARGS+=(--require-coverage "$REQUIRE_COVERAGE")
set +e
python3 "$CORPUS/report.py" "${RENDER_ARGS[@]}"
RENDER_RC=$?
set -e
if [ "$RENDER_RC" != "0" ] && [ "$RENDER_RC" != "5" ]; then
  die "report.py render exited $RENDER_RC"
fi
[ -z "$JSON_OUT" ] || cp "$REPORT" "$JSON_OUT"

if [ "$RECORD" = "1" ]; then
  # RE-RECORDING IS NOT A FORMALITY (M2d.1 decision 14). A moved verdict blocks
  # a block regardless of what any table says, so the old rows are printed
  # BESIDE the new ones with the reason, and a move in any vector OUTSIDE the
  # declared set fails the record rather than being laundered through it.
  if [ -f "$BASELINE" ]; then
    echo
    python3 "$CORPUS/report.py" diff --baseline "$BASELINE" --observed "$REPORT" \
      --allow "${DECLARE:-22-schedule_cost_poison,23-schedule_starvation,24-schedule_budget_burn}" \
      || die "a vector OUTSIDE the declared set moved: this block changed the trust boundary while claiming to change only the instrument (falsifier V-4)"
  fi
  cp "$REPORT" "$BASELINE"
  echo "adversarial: recorded baseline -> $BASELINE"
  exit "$RENDER_RC"
fi

if [ ! -f "$BASELINE" ]; then
  echo "adversarial: no baseline at $BASELINE; run with --record to create one" >&2
  exit 0
fi
python3 "$CORPUS/report.py" diff --baseline "$BASELINE" --observed "$REPORT" ${ONLY:+--only "$ONLY"}
# The coverage gate outranks a clean diff: a corpus that matches its baseline
# perfectly and exercised nothing is exactly the vacuum this flag exists to
# make visible.
exit "$RENDER_RC"
