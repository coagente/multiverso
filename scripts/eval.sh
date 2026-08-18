#!/usr/bin/env bash
# M2d — THE LABELLED-EVALUATION PROTOCOL.
#
# This script exists because of one row. M2b.1 raced two budgeted arms at a
# binding budget on the shipped default and got adaptive REJECT 5/5 against
# fixed-budget SELECT 3/5 — 60 % disagreement against a 40 % noise floor, the
# first difference in this project that exceeds both arms' own instability. What
# it could not say is WHICH ARM WAS RIGHT, because a REJECT where no candidate
# was any good is the correct answer and a REJECT where an honest fix sat there
# unbought is a failure. This script produces the labels that tell those apart,
# and it prints BOTH FAMILY COLUMNS side by side so the answer cannot be quoted
# one-sided.
#
# WHY IT IS NOT PART OF accept.sh. It needs a MATERIALIZED CORPUS and minutes of
# wall clock: each instance is raced once for the reference arm plus R times per
# budgeted arm, and every candidate is then scored on a fresh reconstruction.
# accept.sh gets four HERMETIC steps (m2d-7a…7d) instead — degradation, the leak
# tripwire, the import-graph assertion and label purity — which run in seconds
# with no corpus, no network and no docker.
#
# ZERO AGENT SPEND, ENFORCED. Every race is `--agent script` over patch bytes
# that already exist, and this script front-loads PATH with poisoned stubs for
# every agent CLI the adapters know about (the same mechanism
# scripts/adversarial.sh uses). A stub firing is a loud, free bug instead of a
# silent, billed one. There is no API call anywhere in this block.
#
# NO NETWORK. `mvo-eval fetch local-derived` reads committed fixtures and
# contacts nothing; it prints "URLs it will contact: NONE" and this script
# asserts that line. A network corpus would have to be fetched deliberately, by
# a different manifest, with --yes.
#
# WHAT ITS OUTPUT IS NOT, printed on every table by the harness itself:
# n = 1–2 repositories, SYNTHETIC-CANDIDATES, Tier-1 labels,
# ORACLE-BUDGET-MATCHED only, selection cost measured and uncharged, tokens and
# runner time unmeasured, and no agent output anywhere in it. It is a DIAGNOSIS
# OF A SCHEDULING RULE. If it says the rule loses, the response is to fix the
# rule — not to widen the corpus until the number flips.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

REPLICATES=3
LEVELS="B2"
INSTANCES=""
SPLIT=""
STRICT=0
FETCH=1
KEEP=""
JSON_DIR=""
ARMS="A2-adaptive,A1-fixed-budget"
POLICY_CONFIGS="default schedule"
UNFREEZE=""
# SELECTOR is M2b.2 decision 6: WHICH allocation rule the adaptive arm races
# under. Empty means the binary default, which is what a published cell should
# normally carry; `voc` reproduces the M2d numbers on a binary that ships the
# revision, which is the only way the before/after is a paired comparison
# rather than a comparison against a moving baseline.
SELECTOR=""

usage() {
  cat >&2 <<'EOF'
usage: scripts/eval.sh [options]

  --replicates N       replicates per arm per level (default 3; NO VERDICT below 3)
  --levels L[,L...]    budget levels derived per instance from the reference run's
                       own bound (default B2):
                         B1 = ceil(minspend x 1.1) — the tightest budget where
                              winning is possible at all
                         B2 = (B1 + S) / 2         — the middle of the band
                         B3 = S                    — the null case
  --instances A,B      restrict to these instance ids
  --split dev|eval     restrict to a split half (the file's own recorded function)
  --arms A,B           budgeted arms to race (default adaptive + fixed-budget)
  --selector RULE      allocation rule the ADAPTIVE arm races under: voc (M2b's
                       published rule, the binary default) or voc2 (M2b.2's
                       finishing rule). Run the protocol once per rule to get
                       M2b.2's paired before/after under ONE binary; the arm
                       table, the instances, the split, the labels, the scoring
                       and the metrics do not move, and the rule is recorded in
                       each cell's manifest notes.
                       TWO RUNS SCORE EVERY DECLARED CELL TWICE, so the
                       published eval-use count doubles and §5.2 requires it to
                       say so. And on a FRESH workspace nothing is priced, so
                       finish_ms is unknown and voc2 falls back to voc on every
                       step: as the harness stands, both runs produce IDENTICAL
                       cells. See M2b.2 §7.4's amendment before quoting either
  --policy-configs "…" evidence-incompleteness configurations to run (default
                       "default schedule": the shipped default has
                       on_evidence_incomplete OFF, policies/schedule.json has it ON,
                       and M2b.1 F14 requires both directions)
  --no-fetch           skip materialization; use the corpus already in the eval home
  --strict             exit non-zero when nothing could be scored
  --unfreeze REASON    proceed despite freeze drift on the eval split, recorded
  --keep DIR           keep the run's workspaces here instead of a temp dir
  --json DIR           write one signed run manifest per cell here
  -h, --help           this message

exit codes: 0 ran, 1 a run or an assertion failed, 2 usage,
            3 nothing scorable under --strict, 4 a leak detector fired,
            77 SKIP — a named prerequisite is absent
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --replicates) REPLICATES="${2:?--replicates needs a value}"; shift 2 ;;
    --levels) LEVELS="${2:?--levels needs a value}"; shift 2 ;;
    --instances) INSTANCES="${2:?--instances needs a value}"; shift 2 ;;
    --split) SPLIT="${2:?--split needs a value}"; shift 2 ;;
    --arms) ARMS="${2:?--arms needs a value}"; shift 2 ;;
    --selector) SELECTOR="${2:?--selector needs a rule}"; shift 2 ;;
    --policy-configs) POLICY_CONFIGS="${2:?--policy-configs needs a value}"; shift 2 ;;
    --no-fetch) FETCH=0; shift ;;
    --strict) STRICT=1; shift ;;
    --unfreeze) UNFREEZE="${2:?--unfreeze needs a reason}"; shift 2 ;;
    --keep) KEEP="${2:?--keep needs a directory}"; shift 2 ;;
    --json) JSON_DIR="${2:?--json needs a directory}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "eval.sh: unknown flag $1" >&2; usage; exit 2 ;;
  esac
done

WORK="$(mktemp -d)"
cleanup() { [ -n "$KEEP" ] || rm -rf "$WORK"; }
trap cleanup EXIT

fail() { echo "eval.sh: FAIL: $*" >&2; exit 1; }
skip() { echo "eval.sh: SKIP: $*" >&2; exit 77; }

# --- fail closed: no real agent CLI, ever. -----------------------------------
SHIM="$WORK/no-agents"
mkdir -p "$SHIM"
for bin in claude codex gpt5 gemini cursor aider; do
  cat > "$SHIM/$bin" <<'STUB'
#!/bin/sh
echo "eval.sh: refusing to invoke a real agent CLI ($0)" >&2
exit 97
STUB
  chmod +x "$SHIM/$bin"
done
export PATH="$SHIM:$PATH"

command -v git >/dev/null || skip "git is required"
command -v python3 >/dev/null || skip "python3 is required: the hidden oracle runs under it"
python3 -c 'import importlib.metadata as m; m.version("pytest")' >/dev/null 2>&1 \
  || skip "pytest is not importable: the races run the real Python oracle ladder"

# The eval home lives OUTSIDE the repository and outside every workspace. A run
# that has not been given one gets a temporary one, so nothing is ever written
# into a developer's real cache by accident.
if [ -z "${MVO_EVAL_HOME:-}" ]; then
  export MVO_EVAL_HOME="$WORK/evalhome"
fi
mkdir -p "$MVO_EVAL_HOME"
chmod 700 "$MVO_EVAL_HOME"

MVO="$WORK/mvo"
MVOEVAL="$WORK/mvo-eval"
echo "eval.sh: building both binaries (they are SEPARATE on purpose: the racer"
echo "         cannot contain a symbol that opens the eval home — decision 2)"
(cd "$ROOT" && go build -o "$MVO" ./cmd/mvo)
(cd "$ROOT" && go build -o "$MVOEVAL" ./cmd/mvo-eval)

# The import-graph property, asserted here too: a script that raced with a
# binary able to read labels would be measuring nothing.
if (cd "$ROOT" && go list -deps ./cmd/mvo | grep -q '^github.com/coagente/multiverso/internal/eval$'); then
  fail "cmd/mvo depends on internal/eval: the racing binary can open the eval home"
fi

if [ "$FETCH" = "1" ]; then
  echo "eval.sh: materializing the local-derived corpus (no network)"
  FETCH_OUT="$WORK/fetch.out"
  "$MVOEVAL" fetch local-derived --repo-root "$ROOT" >"$FETCH_OUT" 2>&1 \
    || { cat "$FETCH_OUT"; fail "fetch local-derived"; }
  grep -q 'URLs it will contact: NONE' "$FETCH_OUT" \
    || fail "the local corpus did not declare that it contacts nothing"
  grep -c 'declined' "$FETCH_OUT" >/dev/null || true
  sed -n '1,200p' "$FETCH_OUT"
fi

[ -n "$JSON_DIR" ] && mkdir -p "$JSON_DIR"

RC=0
CELL=0
IFS=',' read -r -a LEVEL_LIST <<< "$LEVELS"
for policy in $POLICY_CONFIGS; do
  for level in "${LEVEL_LIST[@]}"; do
    CELL=$((CELL + 1))
    # THE POLICY THE CELL NAMES IS THE POLICY THE CELL RACES. This loop used
    # to print a header claiming `on_evidence_incomplete: ON` and pass nothing
    # to the runner, which has no such flag — so both cells raced the shipped
    # default and their manifests carried a byte-identical policy_digest. F14
    # was captioned and not delivered. The policy file is now resolved here,
    # asserted to exist, and handed over; `mvo-eval run` refuses a --policy it
    # cannot read rather than falling back.
    POLICY_FILE=""
    if [ "$policy" != "default" ]; then
      POLICY_FILE="$ROOT/testdata/toyrepo/policies/$policy.json"
      [ -f "$POLICY_FILE" ] || fail "policy config '$policy' names no file at $POLICY_FILE"
    fi
    echo
    echo "=============================================================="
    echo "CELL $CELL — policy=$policy level=$level R=$REPLICATES arms=$ARMS"
    if [ -n "$POLICY_FILE" ]; then
      echo "  policy file: $POLICY_FILE"
      echo "  on_evidence_incomplete: $(python3 -c 'import json,sys; print("ON" if json.load(open(sys.argv[1]))["escalation"]["on_evidence_incomplete"] else "OFF")' "$POLICY_FILE")  (read from the file, not asserted here)"
    else
      echo "  policy file: none — the shipped default \`mvo init\` writes"
      echo "  on_evidence_incomplete: OFF"
    fi
    echo "  (M2b.1 F14 requires both directions; a table from one of them is"
    echo "   a table about one escalation rule, not about the scheduler)"
    echo "  Instances carrying a PolicyHint keep their hint; the harness prints"
    echo "  which ones, because a cell is not always uniform."
    echo "=============================================================="
    # Every optional flag is appended with an `if`, never spliced from a
    # possibly-empty array: macOS ships bash 3.2, where expanding an empty
    # array under `set -u` is a fatal error rather than nothing. That is how
    # this script first failed.
    args=(run --mvo "$MVO" --arms "$ARMS" --replicates "$REPLICATES"
          --level "$level" --repo-root "$ROOT")
    [ -n "$POLICY_FILE" ] && args+=(--policy "$POLICY_FILE")
    # THE WORK ROOT IS NAMESPACED PER CELL. `--keep DIR` used to hand the same
    # directory to every cell, and the runner reuses it verbatim: cell 2's
    # `mvo init --dir <workRoot>/<inst>/ref/ws` then failed with "already
    # initialized", every instance became SKIP preflight-abort, the canary read
    # `not-in-force`, non-consultation read NOT PROVED, no metric line printed —
    # and eval.sh printed its caveats block and exited 0. One real cell and N-1
    # empty ones with no failure signal, under the exact flag a reviewer uses to
    # inspect workspaces.
    [ -n "$KEEP" ] && args+=(--keep "$KEEP/cell-$policy-$level")
    [ -n "$INSTANCES" ] && args+=(--instances "$INSTANCES")
    [ -n "$SPLIT" ] && args+=(--split "$SPLIT")
    [ -n "$UNFREEZE" ] && args+=(--unfreeze "$UNFREEZE")
    [ -n "$SELECTOR" ] && args+=(--selector "$SELECTOR")
    [ "$STRICT" = "1" ] && args+=(--strict)
    if [ -n "$JSON_DIR" ]; then
      args+=(--json "$JSON_DIR/cell-$policy-$level.json")
    fi
    CELL_OUT="$WORK/cell-$policy-$level.out"
    set +e
    "$MVOEVAL" "${args[@]}" 2>&1 | tee "$CELL_OUT"
    code=${PIPESTATUS[0]}
    set -e
    case "$code" in
      0) ;;
      3) echo "eval.sh: cell $CELL scored nothing (--strict)"; RC=3 ;;
      4) echo "eval.sh: cell $CELL VOIDED an instance: a leak detector fired" >&2; exit 4 ;;
      77) echo "eval.sh: cell $CELL skipped: a named prerequisite is absent"; ;;
      *) fail "cell $CELL exited $code" ;;
    esac
    # A CELL THAT SCORED ZERO AFTER ONE SCORED MORE THAN ZERO IS A FAILURE, not
    # a caveat. That asymmetry is the whole signal: "no instance scored" is a
    # legitimate outcome when nothing can score (no corpus, no python), and a
    # bug when the previous cell scored fine under the same prerequisites.
    if grep -q 'no metric line is printed' "$CELL_OUT"; then
      SCORED=0
    else
      SCORED=1
    fi
    if [ "$SCORED" = "0" ] && [ "${ANY_SCORED:-0}" = "1" ]; then
      fail "cell $CELL ($policy/$level) scored ZERO instances while an earlier cell scored some.
Same corpus, same prerequisites, different result: that is a harness bug, not a skip.
$(sed -n '1,40p' "$CELL_OUT")"
    fi
    [ "$SCORED" = "1" ] && ANY_SCORED=1
  done
done

cat <<'CAVEATS'

--------------------------------------------------------------------------------
WHAT THE TABLES ABOVE ARE, AND WHAT THEY ARE NOT
--------------------------------------------------------------------------------
They are a DIAGNOSIS OF A SCHEDULING RULE over a fixed candidate set, and every
one of these bounds is printed by the harness beside the numbers themselves:

  * n = 1–2 REPOSITORIES. A diagnosis, never a rate with a confidence interval.
    The harness refuses a p-value and a CI below its declared instance floor and
    prints the paired decision table instead.
  * SYNTHETIC-CANDIDATES. Gold plus mechanical perturbations of gold plus 22
    declared laundering vectors. S2 mutants touch the right file, in the right
    function, with the right shape — the part real agents most often get wrong —
    so they are systematically EASIER for a suite to catch and every FAR here is
    a LOWER BOUND that flatters every arm and every oracle.
  * NO AGENT OUTPUT. S4 (patches recorded from real runs) is an EMPTY corpus in
    this repository. Nothing here says anything about generation.
  * TIER-1 LABELS only. Tier 2 needs LLM-generated strengthened suites, which is
    spend; research ch. 9's warning applies to us verbatim — a claimed
    false-admission rate below ~5 % measured with Tier 1 alone is meaningless.
  * ORACLE-BUDGET-MATCHED, not budget-matched. PRD §11's budget is
    tokens + runner time + oracle cost + selection cost; this charges the third.
  * SELECTOR ARMS OVER A FIXED CANDIDATE SET, not PRD §11's arm set. Arms 1 and
    4 generate, which is spend, and they are ABSENT rather than approximated.
--------------------------------------------------------------------------------
CAVEATS

exit "$RC"
