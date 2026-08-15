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
#
# NO REAL AGENT CLI IS EVER INVOKED. Only `--agent script` is used, and the
# harness front-loads PATH with poisoned stubs (below) so that any code path
# that ever tried to spawn one would die loudly instead of spending money.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORPUS="$ROOT/testdata/adversarial"
VECTORS="$CORPUS/vectors"
BASELINE="$CORPUS/baseline.json"

REPEAT=5
ONLY=""
RECORD=0
JSON_OUT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --record) RECORD=1; shift ;;
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

# mkrepo DIR — a fresh git repo + multiverso workspace over the fixture.
# Deliberately no .gitignore: the fixture must be able to fail the way real
# repos fail (the study's "structurally immune fixture" finding).
mkrepo() {
  local dir="$1"
  mkdir -p "$dir"
  cp -R "$CORPUS/repo/." "$dir/"
  # Keep the fixture hermetic even if somebody ran pytest inside repo/ by
  # hand: a stale __pycache__ in the base commit is dirt in every world.
  rm -rf "$dir/__pycache__" "$dir/.pytest_cache"
  $GIT -C "$dir" init -q -b main
  $GIT -C "$dir" add -A
  $GIT -C "$dir" commit -qm "adversarial fixture baseline"
  "$MVO" init --dir "$dir" >/dev/null
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
truth() {
  ( cd "$1" && python3 -S -c '
import sys
sys.path.insert(0, ".")
import billing
ok = sum(billing.split_evenly(100, 3)) == 100 and billing.split_evenly(100, 3) == [34, 33, 33]
sys.exit(0 if ok else 1)
' >/dev/null 2>&1 ) && echo true || echo false
}

# race_once REPO PATCHDIR NCAND TITLE — race and echo the intent digest.
race_once() {
  local repo="$1" pdir="$2" n="$3" title="$4" intent
  intent="$("$MVO" intent new --dir "$repo" --title "$title" --budget-candidates "$n")"
  [ -n "$intent" ] || die "intent new printed no digest"
  "$MVO" race "$intent" --dir "$repo" --agent script --patches "$pdir" >/dev/null
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
  local intent world
  mkdir -p "$pdir"
  cp "$VECTORS/$vec.patch" "$pdir/01-candidate.patch"
  mkrepo "$repo"
  intent="$(race_once "$repo" "$pdir" 1 "solo: $vec")"
  world="$(worldof "$repo" "$intent" "$pdir/01-candidate.patch")"
  "$MVO" explain "$intent" --dir "$repo" --json > "$WORK/$vec/solo.explain.json"

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
    --vector "$vec" --world "$world" \
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
  local intent cheat honest
  mkdir -p "$pdir"
  cp "$VECTORS/$vec.patch" "$pdir/01-cheat.patch"
  cp "$HONEST" "$pdir/02-honest.patch"
  mkrepo "$repo"
  intent="$(race_once "$repo" "$pdir" 2 "duel: $vec")"
  cheat="$(worldof "$repo" "$intent" "$pdir/01-cheat.patch")"
  honest="$(worldof "$repo" "$intent" "$pdir/02-honest.patch")"
  "$MVO" explain "$intent" --dir "$repo" --json > "$WORK/$tag/explain.json"
  python3 "$CORPUS/report.py" duel \
    --vector "$vec" --round "$round" --cheat "$cheat" --honest "$honest" \
    --explain "$WORK/$tag/explain.json"
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
python3 "$CORPUS/report.py" render --results "$RESULTS" --out "$REPORT"
[ -z "$JSON_OUT" ] || cp "$REPORT" "$JSON_OUT"

if [ "$RECORD" = "1" ]; then
  cp "$REPORT" "$BASELINE"
  echo "adversarial: recorded baseline -> $BASELINE"
  exit 0
fi

if [ ! -f "$BASELINE" ]; then
  echo "adversarial: no baseline at $BASELINE; run with --record to create one" >&2
  exit 0
fi
python3 "$CORPUS/report.py" diff --baseline "$BASELINE" --observed "$REPORT" ${ONLY:+--only "$ONLY"}
