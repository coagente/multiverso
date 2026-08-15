#!/usr/bin/env bash
# M1e acceptance script — M1d's steps kept intact (admission, signed
# attestation, offline verification, tamper detection, publish → clone →
# fetch-race → prune) with the M1e policy/oracle steps: the races of steps
# 2/3b/3c/3d now run the SHIPPED DEFAULT v1 policy's Python ladder with no
# --oracle-cmd anywhere, and steps 3e-3i cover a lexicographic race decided
# by the SECOND ranking key, the collected-count guard stopping two
# test-deleting candidates, an ESCALATE on a tie, a rejected policy file,
# and a v0-policy race whose rationale still matches M0's frozen sentence
# (see docs/design/M1e-policy-oracles.md "Acceptance script").
# Exits non-zero on the first broken assertion. No real agent CLI is ever
# invoked, the network is never touched, and nothing larger than
# python:3.12-alpine is ever pulled.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

MVO="$WORK/mvo"
(cd "$ROOT" && go build -o "$MVO" ./cmd/mvo)

GIT="git -c user.name=m0 -c user.email=m0@example.invalid"

# --- 1. temp git repo from testdata/toyrepo (bug + failing pytest suite) ---
REPO="$WORK/toyrepo"
mkdir -p "$REPO"
cp -R "$ROOT/testdata/toyrepo/." "$REPO/"
$GIT -C "$REPO" init -q -b main
$GIT -C "$REPO" add -A
$GIT -C "$REPO" commit -qm "toyrepo baseline"

# --- 2. init; intent new; race the SCRIPT ADAPTER with the two shipped
# patches — NO --oracle-cmd: `mvo init` writes the v1 default policy and the
# race runs the oracle ladder the policy itself names (M1e decision 18/19) ---
"$MVO" init --dir "$REPO"
INTENT="$("$MVO" intent new --dir "$REPO" --title "fix mean()" --desc "mean divides by len-1")"
[ -n "$INTENT" ] || fail "mvo intent new printed no digest"
"$MVO" race "$INTENT" --dir "$REPO" --agent script --patches "$REPO/patches"

# --- 3. explain shows SELECT with patch-a's world as winner ---
PATCH_A_KEY="sha256:$(python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$REPO/patches/patch-a.patch")"
WORLD_A="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT payload_dig FROM events WHERE type='world.created' AND cast(payload AS text) LIKE '%$PATCH_A_KEY%';")"
[ -n "$WORLD_A" ] || fail "no world.created event references patch-a ($PATCH_A_KEY)"

# --- 3a. the ladder actually ran (EP-1/EP-2): the winner carries BOTH the
# O0 collect receipt (counts, delta against the measured base state) and the
# O1 suite receipt (JUnit-derived metrics, with the pytest version recorded
# as the structured source that produced them) ---
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded';" \
  | python3 -c '
import json, sys
world = sys.argv[1]
recs = [json.loads(line) for line in sys.stdin if line.strip()]
mine = {r["oracle"]["id"]: r for r in recs if r.get("world") == world}
col = mine.get("pytest-collect") or sys.exit("no pytest-collect receipt for the winner")
suite = mine.get("pytest-suite") or sys.exit("no pytest-suite receipt for the winner")
assert col["result"]["metrics"]["collected_total"] == 8, col["result"]["metrics"]
assert col["result"]["metrics"]["collected_delta"] == 0, col["result"]["metrics"]
assert suite["result"]["metrics"]["tests_passed"] == 8, suite["result"]["metrics"]
assert suite["result"]["tools"].get("pytest"), suite["result"]["tools"]
assert col["freshness"]["basis"] == "construction", col["freshness"]
' "$WORLD_A" || fail "the winner does not carry the ladder's collect + suite receipts"
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT cast(payload AS text) FROM events WHERE type='baseline.recorded';" \
  | python3 -c '
import json, sys
events = [json.loads(line) for line in sys.stdin if line.strip()]
assert events, "no baseline.recorded event"
assert events[0]["collected_total"] == 8, events[0]
assert events[0]["oracle"]["id"] == "pytest-collect", events[0]
' || fail "no baseline.recorded event measuring the base state at 8 collected tests"
EXPLAIN="$("$MVO" explain "$INTENT" --dir "$REPO")"
echo "$EXPLAIN" | grep -q '^type: *SELECT$' || fail "decision is not SELECT:
$EXPLAIN"
WINNER="$(echo "$EXPLAIN" | awk '/^winner:/ {print $2}')"
[ "$WINNER" = "$WORLD_A" ] || fail "winner $WINNER is not patch-a's world $WORLD_A"
# M1d: raced at trunk head, nothing has moved yet — the drift line is FRESH.
echo "$EXPLAIN" | grep -q '^freshness: FRESH' || fail "explain does not show freshness FRESH:
$EXPLAIN"

# --- 3b. fake-agent race (claude-code adapter against the fake fixture,
# while the base commit still carries the bug so the fake's fix produces a
# real diff). Never invokes a real CLI: PATH puts testdata/fakeagent first,
# and the guard below FAILS CLOSED before any spawn — if a fixture lost its
# executable bit (mode-stripping transports, core.fileMode=false) LookPath
# would silently fall through to a real, money-spending CLI. ---
for bin in claude codex; do
  RESOLVED="$(PATH="$ROOT/testdata/fakeagent:$PATH" command -v "$bin" || true)"
  [ "$RESOLVED" = "$ROOT/testdata/fakeagent/$bin" ] \
    || fail "fail closed: $bin resolves to '${RESOLVED:-nothing}', not the fake fixture — refusing to race"
done
INTENT2="$("$MVO" intent new --dir "$REPO" --title "fix mean() via agent" --desc "agent race")"
[ -n "$INTENT2" ] || fail "mvo intent new (agent intent) printed no digest"
PATH="$ROOT/testdata/fakeagent:$PATH" FAKE_AGENT_MODE=happy \
  "$MVO" race "$INTENT2" --dir "$REPO" --agent claude-code --candidates 2 \
    --model fake-model --max-usd 0.25 --max-turns 8 --max-wall-ms 60000 \
    --agent-env FAKE_AGENT_MODE

EXPLAIN2="$("$MVO" explain "$INTENT2" --dir "$REPO")"
echo "$EXPLAIN2" | grep -q '^type: *SELECT$' || fail "fake-agent decision is not SELECT:
$EXPLAIN2"
WINNER2="$(echo "$EXPLAIN2" | awk '/^winner:/ {print $2}')"
[ -n "$WINNER2" ] || fail "fake-agent explain printed no winner"

# Every fake-agent world: COMPLETED, honest client-estimate cost of 4200
# micro-USD (the fixture's total_cost_usd 0.0042).
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT cast(payload AS text) FROM events WHERE type='world.created';" \
  | python3 -c '
import json, sys
intent = sys.argv[1]
worlds = [json.loads(line) for line in sys.stdin if line.strip()]
mine = [w for w in worlds if w["intent"] == intent]
assert len(mine) == 2, f"want 2 fake-agent worlds, got {len(mine)}"
for w in mine:
    assert w["outcome"] == "COMPLETED", w["outcome"]
    assert w["cost"]["usd_micro"] == 4200, w["cost"]
    assert w["cost"]["source"] == "client-estimate", w["cost"]
    assert w["producer"]["adapter"] == "claude-code@v0", w["producer"]
' "$INTENT2" || fail "fake-agent worlds failed outcome/cost assertions"

# The winner carries a REAL captured patch (which the passing suite receipt
# proves the oracle gated) and a non-empty transcript in the CAS.
WINNER2_PAYLOAD="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT cast(payload AS text) FROM events WHERE type='world.created' AND payload_dig='$WINNER2';")"
[ -n "$WINNER2_PAYLOAD" ] || fail "no world.created payload for fake-agent winner $WINNER2"
PATCH2_KEY="$(echo "$WINNER2_PAYLOAD" | python3 -c 'import json,sys; print(json.load(sys.stdin)["patch"])')"
TRACE2_KEY="$(echo "$WINNER2_PAYLOAD" | python3 -c 'import json,sys; print(json.load(sys.stdin)["trace"])')"
PATCH2_HEX="${PATCH2_KEY#sha256:}"
TRACE2_HEX="${TRACE2_KEY#sha256:}"
PATCH2_FILE="$REPO/.multiverso/cas/sha256/${PATCH2_HEX:0:2}/${PATCH2_HEX:2}"
TRACE2_FILE="$REPO/.multiverso/cas/sha256/${TRACE2_HEX:0:2}/${TRACE2_HEX:2}"
[ -s "$PATCH2_FILE" ] || fail "fake-agent winner patch $PATCH2_KEY is empty or missing"
grep -q 'diff --git' "$PATCH2_FILE" || fail "fake-agent winner patch is not a real captured diff"
[ -s "$TRACE2_FILE" ] || fail "fake-agent winner trace $TRACE2_KEY is empty or missing"
head -1 "$TRACE2_FILE" | grep -q '"type"' || fail "fake-agent winner trace first line carries no event"

# --- 3c. parallel determinism: a --parallel 2 race of the same patches
# reaches the same decision type and the same winner (identified by its
# context CAS key — patch-a's content hash) as step 2's serial run.
# Schedule-independent here: patch-a is the unique gate-passer (M1c
# decision 17). ---
INTENT3="$("$MVO" intent new --dir "$REPO" --title "fix mean() in parallel" --desc "parallel race")"
[ -n "$INTENT3" ] || fail "mvo intent new (parallel intent) printed no digest"
"$MVO" race "$INTENT3" --dir "$REPO" --agent script --patches "$REPO/patches" \
  --parallel 2
EXPLAIN3="$("$MVO" explain "$INTENT3" --dir "$REPO")"
echo "$EXPLAIN3" | grep -q '^type: *SELECT$' || fail "parallel decision is not SELECT:
$EXPLAIN3"
WINNER3="$(echo "$EXPLAIN3" | awk '/^winner:/ {print $2}')"
WORLD3_A="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT payload_dig FROM events WHERE type='world.created' AND cast(payload AS text) LIKE '%$PATCH_A_KEY%' AND cast(payload AS text) LIKE '%$INTENT3%';")"
[ -n "$WORLD3_A" ] || fail "no world.created event for the parallel race references patch-a"
[ "$WINNER3" = "$WORLD3_A" ] || fail "parallel winner $WINNER3 is not patch-a's world $WORLD3_A (serial and parallel disagree)"

# --- 3d. T1 container race (docker-gated; skips gracefully when no daemon
# is reachable — CI has no docker). The script adapter applies patches on
# the host (M1c decision 13); the pytest oracle runs INSIDE the pinned
# container. ---
T1_RAN=0
if ! docker version >/dev/null 2>&1; then
  echo "accept: T1 step SKIPPED (no docker daemon)"
else
  T1_IMAGE="multiverso-t1-fixture:v1"
  if ! docker image inspect "$T1_IMAGE" >/dev/null 2>&1; then
    echo "accept: building fixture image $T1_IMAGE (one-time)"
    if ! docker build -f "$ROOT/testdata/t1image/Dockerfile" -t "$T1_IMAGE" "$ROOT/testdata"; then
      echo "accept: T1 step SKIPPED (fixture image build failed — offline?)"
      T1_IMAGE=""
    fi
  fi
  if [ -n "$T1_IMAGE" ]; then
    T1_RAN=1
    INTENT4="$("$MVO" intent new --dir "$REPO" --title "fix mean() under T1" --desc "container race")"
    [ -n "$INTENT4" ] || fail "mvo intent new (T1 intent) printed no digest"
    "$MVO" race "$INTENT4" --dir "$REPO" --agent script --patches "$REPO/patches" \
      --exec T1 --exec-image "$T1_IMAGE" --memory-mb 512 --cpus 1 --pids 256
    EXPLAIN4="$("$MVO" explain "$INTENT4" --dir "$REPO")"
    echo "$EXPLAIN4" | grep -q '^type: *SELECT$' || fail "T1 decision is not SELECT:
$EXPLAIN4"
    WINNER4="$(echo "$EXPLAIN4" | awk '/^winner:/ {print $2}')"
    WORLD4_A="$(sqlite3 "$REPO/.multiverso/ledger.db" \
      "SELECT payload_dig FROM events WHERE type='world.created' AND cast(payload AS text) LIKE '%$PATCH_A_KEY%' AND cast(payload AS text) LIKE '%$INTENT4%';")"
    [ -n "$WORLD4_A" ] || fail "no world.created event for the T1 race references patch-a"
    [ "$WINNER4" = "$WORLD4_A" ] || fail "T1 winner $WINNER4 is not patch-a's world $WORLD4_A (the container did not gate it)"

    # XP-3: the T1 winner's env manifest in CAS carries the pinned image
    # digest, and its env digest differs from the T0 winner's (step 2).
    ENV4="$(sqlite3 "$REPO/.multiverso/ledger.db" \
      "SELECT cast(payload AS text) FROM events WHERE type='world.created' AND payload_dig='$WINNER4';" \
      | python3 -c 'import json,sys; print(json.load(sys.stdin)["env"])')"
    ENV1="$(sqlite3 "$REPO/.multiverso/ledger.db" \
      "SELECT cast(payload AS text) FROM events WHERE type='world.created' AND payload_dig='$WORLD_A';" \
      | python3 -c 'import json,sys; print(json.load(sys.stdin)["env"])')"
    [ "$ENV4" != "$ENV1" ] || fail "T1 winner env digest equals the T0 winner's ($ENV4) — XP-3 image pinning missing"
    ENV4_HEX="${ENV4#mv0:}"
    ENV4_FILE="$REPO/.multiverso/cas/sha256/${ENV4_HEX:0:2}/${ENV4_HEX:2}"
    [ -f "$ENV4_FILE" ] || fail "T1 env manifest $ENV4 not in CAS"
    grep -q '"image_digest":"sha256:' "$ENV4_FILE" \
      || fail "T1 env manifest carries no image digest: $(cat "$ENV4_FILE")"

    # XP-1/XP-2/NFR-4: the T1 suite receipt records the container tier and
    # the network-off cap.
    sqlite3 "$REPO/.multiverso/ledger.db" \
      "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded';" \
      | python3 -c '
import json, sys
world = sys.argv[1]
recs = [json.loads(line) for line in sys.stdin if line.strip()]
mine = [r for r in recs if r.get("world") == world and r.get("family") == "suite"]
assert mine, f"no suite receipt for T1 winner {world}"
for r in mine:
    ex = r["execution"]
    assert ex["isolation_tier"] == "T1-container", ex["isolation_tier"]
    caps = ex["isolation_caps"]
    assert caps["network"] == "none", caps
    assert caps["cap_drop"] == "ALL", caps
    assert caps["read_only_root"] is True, caps
    assert caps["memory_bytes"] == 512 << 20, caps
    assert caps["cpu_milli"] == 1000, caps
    assert caps["pids_limit"] == 256, caps
' "$WINNER4" || fail "T1 suite receipt failed tier/caps assertions"
  fi
fi

# The four M1e policy fixtures are authored files until a verb installs
# them; copying them in is the authoring surface, nothing more.
cp "$REPO/policies/rank-two-keys.json" "$REPO/policies/tie-escalate.json" \
   "$REPO/policies/legacy-v0.json" "$REPO/.multiverso/policies/"

# --- 3e. lexicographic ranking, case (a): the FIRST key ties and the
# SECOND decides. patch-a and patch-c both pass every hard gate; patch-c
# adds two passing tests, so tests_passed_desc (effective key 2) picks it.
# This is the proof that ranking is lexicographic and not a score. ---
"$MVO" policy use rank-two-keys --dir "$REPO" | grep -q 'rank-two-keys, policy/v1' \
  || fail "policy use rank-two-keys did not install the v1 policy"
INTENT5="$("$MVO" intent new --dir "$REPO" --title "rank by second key" --desc "two passing candidates")"
[ -n "$INTENT5" ] || fail "mvo intent new (ranking intent) printed no digest"
"$MVO" race "$INTENT5" --dir "$REPO" --agent script --patches "$REPO/patches-rank"
PATCH_C_KEY="sha256:$(python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$REPO/patches-rank/patch-c.patch")"
WORLD_C="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT payload_dig FROM events WHERE type='world.created' AND cast(payload AS text) LIKE '%$PATCH_C_KEY%';")"
[ -n "$WORLD_C" ] || fail "no world.created event references patch-c ($PATCH_C_KEY)"
EXPLAIN5="$("$MVO" explain "$INTENT5" --dir "$REPO")"
echo "$EXPLAIN5" | grep -q '^type: *SELECT$' || fail "ranking decision is not SELECT:
$EXPLAIN5"
WINNER5="$(echo "$EXPLAIN5" | awk '/^winner:/ {print $2}')"
[ "$WINNER5" = "$WORLD_C" ] || fail "ranking winner $WINNER5 is not patch-c's world $WORLD_C"
echo "$EXPLAIN5" | grep -q 'at ranking key 2 tests_passed_desc (10 > 8)' \
  || fail "the rationale does not name the deciding key:
$EXPLAIN5"
echo "$EXPLAIN5" | grep -qE '^ +2 +tests_passed_desc .*WINNER' \
  || fail "the comparison trace does not show key 2 deciding:
$EXPLAIN5"
"$MVO" explain "$INTENT5" --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["schema"] == "multiverso.dev/explain-report/v0", r["schema"]
assert r["type"] == "SELECT", r["type"]
assert r["trace"][0]["decided_at"] == 2, r["trace"][0]
assert r["trace"][0]["key"] == "tests_passed_desc", r["trace"][0]
assert r["candidates"][0]["metrics"]["tests_passed"] == 10, r["candidates"][0]["metrics"]
assert r["candidates"][1]["metrics"]["tests_passed"] == 8, r["candidates"][1]["metrics"]
' || fail "explain --json does not report the second key as decisive"

# --- 3f. collected-count guard, case (b): two laundering candidates, one
# per mechanism, both stopped by O0 while their suites would have looked
# green — and neither ever reached the suite, because the ladder
# short-circuits at the first failed hard gate ---
"$MVO" policy use default --dir "$REPO" >/dev/null || fail "policy use default failed"
INTENT6="$("$MVO" intent new --dir "$REPO" --title "launder guard" --budget-candidates 3)"
[ -n "$INTENT6" ] || fail "mvo intent new (launder intent) printed no digest"
"$MVO" race "$INTENT6" --dir "$REPO" --agent script --patches "$REPO/patches-launder"
"$MVO" explain "$INTENT6" --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["type"] == "SELECT", r["type"]
byrank = {c["rank"]: c for c in r["candidates"]}
win = byrank[1]
assert win["pass"] is True, win
assert win["metrics"]["collected_delta"] == 0, win["metrics"]
losers = [c for c in r["candidates"] if not c["pass"]]
assert len(losers) == 2, losers
def first_failed(c):
    return next(g for g in c["gates"] if g["result"] == "fail")
cut = [c for c in losers if c["metrics"].get("collected_delta") == -3]
wipe = [c for c in losers if c["metrics"].get("collected_total") == 0]
assert len(cut) == 1 and len(wipe) == 1, [c["metrics"] for c in losers]
assert first_failed(cut[0])["label"] == "collected-not-below@collect", first_failed(cut[0])
assert "collected_delta=-3 (tolerance -0)" in first_failed(cut[0])["detail"], first_failed(cut[0])
assert first_failed(wipe[0])["label"] == "collect-nonempty@collect", first_failed(wipe[0])
# Every gate after the first failure is not-evaluated — never reported as a
# failure the candidate did not earn.
for c in losers:
    seen_fail = False
    for g in c["gates"]:
        if seen_fail:
            assert g["result"] == "not-evaluated", (c["world"], g)
        seen_fail = seen_fail or g["result"] == "fail"
print(cut[0]["world"], wipe[0]["world"])
' > "$WORK/launder.txt" || fail "the collected-count guard did not stop both laundering candidates"
CUT_WORLD="$(awk '{print $1}' "$WORK/launder.txt")"
WIPE_WORLD="$(awk '{print $2}' "$WORK/launder.txt")"
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded';" \
  | python3 -c '
import json, sys
cut, wipe = sys.argv[1], sys.argv[2]
recs = [json.loads(line) for line in sys.stdin if line.strip()]
for world in (cut, wipe):
    kinds = sorted(r["oracle"]["id"] for r in recs if r.get("world") == world)
    assert kinds == ["pytest-collect"], (world, kinds)
w = [r for r in recs if r.get("world") == wipe][0]
assert w["execution"]["exit_code"] == 5, w["execution"]
assert w["result"]["metrics"]["collected_total"] == 0, w["result"]["metrics"]
assert w["result"]["status"] == "fail", w["result"]
' "$CUT_WORLD" "$WIPE_WORLD" \
  || fail "a laundering candidate reached the suite oracle (the ladder did not short-circuit)"

# --- 3g. escalation on a tie, case (c): two distinct trees with identical
# evidence tie on every ranking key; a coin flip is not a decision ---
"$MVO" policy use tie-escalate --dir "$REPO" >/dev/null || fail "policy use tie-escalate failed"
INTENT7="$("$MVO" intent new --dir "$REPO" --title "tie escalates")"
[ -n "$INTENT7" ] || fail "mvo intent new (tie intent) printed no digest"
# A recorded decision is the product: an ESCALATE race exits 0.
"$MVO" race "$INTENT7" --dir "$REPO" --agent script --patches "$REPO/patches-tie" \
  || fail "an ESCALATE race exited non-zero"
EXPLAIN7="$("$MVO" explain "$INTENT7" --dir "$REPO")"
echo "$EXPLAIN7" | grep -q '^type: *ESCALATE$' || fail "tie decision is not ESCALATE:
$EXPLAIN7"
echo "$EXPLAIN7" | grep -q 'escalated by policy rule on_ranking_tie' \
  || fail "the rationale does not name the escalation rule:
$EXPLAIN7"
"$MVO" explain "$INTENT7" --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["type"] == "ESCALATE", r["type"]
assert r["escalation"]["rule"] == "on_ranking_tie", r["escalation"]
assert len(r["candidates"]) == 2, r["candidates"]
for c in r["candidates"]:
    assert c["pass"] is True, c
    assert c["world"] in r["escalation"]["detail"], r["escalation"]
' || fail "explain --json does not carry the tie escalation naming both worlds"

# --- 3h. policy validation, case (d): one typo, one expected error, every
# problem printed, exit 1 (invalid content is a failure, not CLI misuse) ---
if VALIDATE_OUT="$("$MVO" policy validate "$ROOT/testdata/toyrepo/policies/bad-gate.json" 2>&1)"; then
  fail "policy validate accepted bad-gate.json:
$VALIDATE_OUT"
fi
echo "$VALIDATE_OUT" | grep -q 'hard_gates\[1\].gate: unknown gate "suite-passes"' \
  || fail "policy validate does not locate the unknown gate:
$VALIDATE_OUT"
echo "$VALIDATE_OUT" | grep -q 'known: collect-nonempty, collected-not-below, coverage-at-least, no-failed-tests, status-pass' \
  || fail "policy validate does not print the known gate vocabulary:
$VALIDATE_OUT"
"$MVO" policy validate "$REPO/.multiverso/policies/default.json" --dir "$REPO" | grep -q '^OK: policy valid$' \
  || fail "policy validate rejected the shipped default policy"
"$MVO" policy use default --dir "$REPO" >/dev/null || fail "policy use default failed"
DEFAULT_DIG="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["default_policy"])' "$REPO/.multiverso/config.json")"
"$MVO" policy list --dir "$REPO" | grep -qE "^default +$DEFAULT_DIG +policy/v1 .*recorded \(default\)$" \
  || fail "policy list does not show the default as recorded:
$("$MVO" policy list --dir "$REPO")"
SHOWN="$("$MVO" policy show "$DEFAULT_DIG" --json --dir "$REPO")"
SHOWN_DIG="mv0:$(printf '%s' "$SHOWN" | python3 -c 'import hashlib,sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())')"
[ "$SHOWN_DIG" = "$DEFAULT_DIG" ] \
  || fail "policy show --json is not byte-stable: re-digests to $SHOWN_DIG, want $DEFAULT_DIG"

# --- 3i. v0 compatibility: a legacy policy never becomes the silent
# default, is pinnable per intent (with a warning), still requires
# --oracle-cmd, and still renders M0's frozen rationale — while a v1-pinned
# intent refuses the flag outright (M1e decision 18) ---
if USE_OUT="$("$MVO" policy use legacy-v0 --dir "$REPO" 2>&1)"; then
  fail "policy use accepted a policy/v0 file:
$USE_OUT"
fi
echo "$USE_OUT" | grep -q 'is policy/v0, which cannot name its oracles' \
  || fail "policy use refusal does not say why:
$USE_OUT"
INTENT8="$("$MVO" intent new --dir "$REPO" --title "legacy policy" --policy legacy-v0 2>"$WORK/pin.err")"
[ -n "$INTENT8" ] || fail "mvo intent new --policy legacy-v0 printed no digest"
grep -q 'is policy/v0' "$WORK/pin.err" || fail "pinning a v0 policy printed no warning: $(cat "$WORK/pin.err")"
grep -q 'M1e decision 18' "$WORK/pin.err" || fail "the v0 warning does not cite the decision: $(cat "$WORK/pin.err")"
"$MVO" race "$INTENT8" --dir "$REPO" --agent script --patches "$REPO/patches" \
  --oracle-cmd "python3 -m pytest -q"
EXPLAIN8="$("$MVO" explain "$INTENT8" --dir "$REPO")"
echo "$EXPLAIN8" | grep -q '^type: *SELECT$' || fail "legacy-v0 decision is not SELECT:
$EXPLAIN8"
echo "$EXPLAIN8" | grep -qE '^rationale: [0-9]+/[0-9]+ worlds passed hard gates \[suite-pass\]; selected mv0:[0-9a-f]{64} by ranking \[gate_pass,wall_ms_asc\] \(wall_ms=[0-9]+\)$' \
  || fail "the v0 rationale is not M0's frozen sentence:
$EXPLAIN8"
set +e
"$MVO" race "$INTENT5" --dir "$REPO" --agent script --patches "$REPO/patches-rank" \
  --oracle-cmd "python3 -m pytest -q" >"$WORK/v1flag.out" 2>&1
V1_FLAG_CODE=$?
set -e
[ "$V1_FLAG_CODE" = "2" ] \
  || fail "--oracle-cmd against a v1-pinned intent exited $V1_FLAG_CODE, want 2 (usage):
$(cat "$WORK/v1flag.out")"
grep -q -- '--oracle-cmd is not permitted with policy' "$WORK/v1flag.out" \
  || fail "the decision-18 refusal is missing:
$(cat "$WORK/v1flag.out")"

# --- 4. admit lands a new commit on trunk ---
PRE="$($GIT -C "$REPO" log -1 --format=%H)"
"$MVO" admit "$INTENT" --dir "$REPO" || fail "mvo admit exited non-zero"
POST="$($GIT -C "$REPO" log -1 --format=%H)"
[ "$PRE" != "$POST" ] || fail "admit did not land a commit (HEAD still $PRE)"

# --- 5. the commit message carries the attestation trailer ---
$GIT -C "$REPO" log -1 --format=%B | grep -qE '^Multiverso-Attestation: sha256:[0-9a-f]{64}$' \
  || fail "admitted commit has no Multiverso-Attestation trailer:
$($GIT -C "$REPO" log -1 --format=%B)"

# --- 6. verify: human, --json, and explicit --key ---
"$MVO" verify HEAD --dir "$REPO" || fail "mvo verify HEAD failed"
"$MVO" verify HEAD --json --dir "$REPO" | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r.get("ok") is True, r
' || fail "verify --json did not report ok=true"
"$MVO" verify HEAD --key "$REPO/.multiverso/keys/local.pub" --dir "$REPO" \
  || fail "mvo verify --key failed"

# --- 6b. publish + idempotence (FI-1): winner candidate + signed evidence
# closure land under refs/multiverso/* on a LOCAL bare remote; nothing
# under refs/heads/ is ever pushed by mvo; an identical republish re-mints
# identical shas so the push plan diffs to zero ---
SHORT="${INTENT#mv0:}"; SHORT="${SHORT:0:12}"
NS="refs/multiverso/intent/$SHORT"
ORIGIN="$WORK/origin.git"
git init -q --bare -b main "$ORIGIN"
$GIT -C "$REPO" remote add origin "$ORIGIN"
"$MVO" publish "$INTENT" --dir "$REPO" || fail "mvo publish exited non-zero"
LSR1="$(git -C "$REPO" ls-remote origin 'refs/multiverso/*' | sort)"
[ "$(echo "$LSR1" | awk '{print $2}' | sort)" = "$NS/cand/1
$NS/evidence" ] || fail "published namespace is not exactly winner cand/1 + evidence:
$LSR1"
[ -z "$(git -C "$REPO" ls-remote origin 'refs/heads/*')" ] \
  || fail "publish pushed something under refs/heads"
REPUB_OUT="$("$MVO" publish "$INTENT" --dir "$REPO")" || fail "republish exited non-zero"
echo "$REPUB_OUT" | grep -q '(0 pushed' || fail "republish was not a no-op:
$REPUB_OUT"
LSR2="$(git -C "$REPO" ls-remote origin 'refs/multiverso/*' | sort)"
[ "$LSR1" = "$LSR2" ] || fail "the no-op republish changed the remote namespace:
$LSR1
vs
$LSR2"
# git ls-remote tail-matches its pattern from any slash boundary, so refs
# whose NAMES merely contain the namespace path come back from a namespace
# survey. Seed one branch and one tag of that shape: reconciliation and
# retention must survey their own namespace only, never delete these.
STRAY_BRANCH="refs/heads/$NS/wip"
STRAY_TAG="refs/tags/release/$NS/v1"
$GIT -C "$REPO" push -q origin "HEAD:$STRAY_BRANCH"
$GIT -C "$REPO" push -q origin "HEAD:$STRAY_TAG"
assert_strays() {
  git -C "$REPO" ls-remote origin "$STRAY_BRANCH" | grep -q "$STRAY_BRANCH" \
    || fail "$1 deleted the out-of-namespace branch $STRAY_BRANCH"
  git -C "$REPO" ls-remote origin "$STRAY_TAG" | grep -q "$STRAY_TAG" \
    || fail "$1 deleted the out-of-namespace tag $STRAY_TAG"
}
# nsRefs prints only the refs actually under refs/multiverso/ (the look-alike
# refs above are what an unanchored survey would wrongly include).
nsRefs() { git -C "$REPO" ls-remote origin 'refs/multiverso/*' | awk '$2 ~ /^refs\/multiverso\// {print $2}' | sort; }
"$MVO" publish "$INTENT" --dir "$REPO" >/dev/null || fail "republish over look-alike refs exited non-zero"
assert_strays "publish reconciliation"

# --- 6c. include-rejected delta: only the loser's cand/<n> is new; prior
# refs are untouched ---
INCL_OUT="$("$MVO" publish "$INTENT" --include-rejected --dir "$REPO")" \
  || fail "publish --include-rejected exited non-zero"
echo "$INCL_OUT" | grep -q '(1 pushed' || fail "include-rejected was not a 1-ref delta:
$INCL_OUT"
git -C "$REPO" ls-remote origin "$NS/cand/2" | grep -q "$NS/cand/2" \
  || fail "loser cand/2 missing after --include-rejected"
assert_strays "publish --include-rejected"

# --- 6d. fetch-race roundtrip (second machine): a plain clone + the
# workspace public key verifies the whole closure offline. Landing trunk on
# the remote is the operator's ordinary git push — never mvo's ---
$GIT -C "$REPO" push -q origin main
CONSUMER="$WORK/consumer"
git clone -q "$ORIGIN" "$CONSUMER"
PUBKEY="$REPO/.multiverso/keys/local.pub"
FR_OUT="$("$MVO" fetch-race "$SHORT" --dir "$CONSUMER" --key "$PUBKEY")" \
  || fail "fetch-race exited non-zero:
$FR_OUT"
echo "$FR_OUT" | grep -q 'OK: race integrity verified' || fail "fetch-race did not verify:
$FR_OUT"
# The verb proves integrity, not correctness, and must keep saying so: this
# line printed verbatim over a race whose winner forged its own JUnit report.
echo "$FR_OUT" | grep -q 'correctness: NOT asserted' || fail "fetch-race does not disclaim correctness:
$FR_OUT"
echo "$FR_OUT" | grep -q "winner:    $WORLD_A" || fail "fetch-race winner is not patch-a's world:
$FR_OUT"
echo "$FR_OUT" | grep -q 'admitted:  yes' || fail "fetch-race does not report the admission:
$FR_OUT"
echo "$FR_OUT" | grep -q 'freshness: STALE' || fail "fetch-race freshness is not STALE (clone head is the admission commit):
$FR_OUT"

# --- 6e. tamper detection: rewrite one published receipt blob in the bare
# remote via git plumbing; fetch-race must fail naming the exact path; a
# republish heals (lease against the observed tampered tip) ---
EVREF="$NS/evidence"
OLD_TIP="$(git -C "$ORIGIN" rev-parse "$EVREF")"
RPATH="$(git -C "$ORIGIN" ls-tree -r --name-only "$EVREF" | grep '^receipts/' | head -1)"
[ -n "$RPATH" ] || fail "no receipts/ file in the published evidence tree"
BLOB="$(git -C "$ORIGIN" rev-parse "$EVREF:$RPATH")"
git -C "$ORIGIN" cat-file blob "$BLOB" > "$WORK/receipt.blob"
python3 -c '
import sys
path = sys.argv[1]
b = bytearray(open(path, "rb").read())
i = b.index(b"\"payload\":\"") + len(b"\"payload\":\"")
b[i] = ord("B") if b[i] == ord("A") else ord("A")
open(path, "wb").write(bytes(b))
' "$WORK/receipt.blob"
NEW_BLOB="$(git -C "$ORIGIN" hash-object -w "$WORK/receipt.blob")"
REC_TREE="$(git -C "$ORIGIN" rev-parse "$EVREF:receipts")"
ROOT_TREE="$(git -C "$ORIGIN" rev-parse "$EVREF^{tree}")"
NEW_REC_TREE="$(git -C "$ORIGIN" ls-tree "$REC_TREE" | sed "s/$BLOB/$NEW_BLOB/" | git -C "$ORIGIN" mktree)"
NEW_ROOT="$(git -C "$ORIGIN" ls-tree "$ROOT_TREE" | sed "s/$REC_TREE/$NEW_REC_TREE/" | git -C "$ORIGIN" mktree)"
NEW_TIP="$($GIT -C "$ORIGIN" commit-tree "$NEW_ROOT" -p "$(git -C "$ORIGIN" rev-parse "$EVREF^")" -m tampered)"
git -C "$ORIGIN" update-ref "$EVREF" "$NEW_TIP" "$OLD_TIP"
if TAMPER_OUT="$("$MVO" fetch-race "$SHORT" --dir "$CONSUMER" --key "$PUBKEY" 2>&1)"; then
  fail "fetch-race verified a tampered receipt:
$TAMPER_OUT"
fi
echo "$TAMPER_OUT" | grep -q 'receipts/mv0_' || fail "tamper failure does not name a receipts path:
$TAMPER_OUT"
echo "$TAMPER_OUT" | grep -qF "$RPATH" || fail "tamper failure does not name $RPATH:
$TAMPER_OUT"
"$MVO" publish "$INTENT" --include-rejected --dir "$REPO" >/dev/null \
  || fail "healing publish exited non-zero"
"$MVO" fetch-race "$SHORT" --dir "$CONSUMER" --key "$PUBKEY" >/dev/null \
  || fail "fetch-race still fails after the healing publish"
assert_strays "the healing publish"

# --- 6f. prune per policy: defaults keep the admitted winner + evidence;
# --keep-admitted=false wipes the namespace; CAS and ledger stay untouched ---
CAS_COUNT_BEFORE="$(find "$REPO/.multiverso/cas" -type f | wc -l | tr -d ' ')"
"$MVO" prune "$INTENT" --remote origin --dir "$REPO" || fail "prune (defaults) exited non-zero"
LSR3="$(nsRefs)"
[ "$LSR3" = "$NS/cand/1
$NS/evidence" ] || fail "prune (defaults) did not keep exactly winner + evidence:
$LSR3"
assert_strays "prune (defaults)"
"$MVO" fetch-race "$SHORT" --dir "$CONSUMER" --key "$PUBKEY" >/dev/null \
  || fail "fetch-race fails after the retention prune"
"$MVO" prune "$INTENT" --remote origin --keep-admitted=false --dir "$REPO" \
  || fail "prune --keep-admitted=false exited non-zero"
[ -z "$(nsRefs)" ] || fail "remote namespace survived --keep-admitted=false"
[ -z "$(git -C "$REPO" for-each-ref 'refs/multiverso')" ] \
  || fail "local namespace survived --keep-admitted=false"
assert_strays "prune --keep-admitted=false"
PRUNE_EVENTS="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT count(*) FROM events WHERE type='prune.executed';")"
[ "$PRUNE_EVENTS" -ge 2 ] || fail "prune.executed events = $PRUNE_EVENTS, want >= 2"
CAS_COUNT_AFTER="$(find "$REPO/.multiverso/cas" -type f | wc -l | tr -d ' ')"
[ "$CAS_COUNT_BEFORE" = "$CAS_COUNT_AFTER" ] \
  || fail "prune changed the CAS file count ($CAS_COUNT_BEFORE -> $CAS_COUNT_AFTER)"

# --- 6g. drift marker: the admission commit moved trunk past the intent
# base, so worlds and explain both render STALE/advanced ---
"$MVO" worlds "$INTENT" --dir "$REPO" | grep -q 'freshness: STALE (main advanced past base' \
  || fail "mvo worlds does not show the STALE/advanced drift line"
"$MVO" explain "$INTENT" --dir "$REPO" | grep -q 'freshness: STALE (main advanced past base' \
  || fail "mvo explain does not show the STALE/advanced drift line"

# --- 7. second machine: audit replays every race — script, fake-agent,
# the parallel race, (when it ran) the T1 race, the ranking race, the
# laundering race, the ESCALATE tie and the legacy-v0 race — AND the
# admission, THROUGH TWO POLICY SCHEMA VERSIONS IN ONE LEDGER; the chain
# also carries publish/prune events (observational) ---
COPY="$WORK/toyrepo-copy"
cp -R "$REPO" "$COPY"
MIN_DECISIONS=8
[ "$T1_RAN" = "1" ] && MIN_DECISIONS=9
"$MVO" audit --json --dir "$COPY" | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r.get("chain_ok") is True, r
assert r.get("replay_identical") is True, r
assert r.get("admissions", 0) >= 1, r
assert r.get("decisions", 0) >= int(sys.argv[1]), r
' "$MIN_DECISIONS" || fail "audit --json on the copy did not replay all decisions identically"

# --- 8. tamper (bundle): flip one byte; verify must fail on bundle_digest ---
TRAILER_DIG="$($GIT -C "$COPY" log -1 --format=%B \
  | sed -n 's/^Multiverso-Attestation: sha256:\([0-9a-f]\{64\}\)$/\1/p' | tail -1)"
[ -n "$TRAILER_DIG" ] || fail "could not extract the attestation trailer digest in the copy"
BUNDLE_FILE="$COPY/.multiverso/cas/sha256/${TRAILER_DIG:0:2}/${TRAILER_DIG:2}"
[ -f "$BUNDLE_FILE" ] || fail "bundle $TRAILER_DIG not found in the copy's CAS"
python3 -c '
import sys
path = sys.argv[1]
b = bytearray(open(path, "rb").read())
b[0] ^= 0xFF
open(path, "wb").write(b)
' "$BUNDLE_FILE"
if VERIFY_OUT="$("$MVO" verify HEAD --dir "$COPY" 2>&1)"; then
  fail "verify succeeded on a tampered bundle:
$VERIFY_OUT"
fi
echo "$VERIFY_OUT" | grep -q 'bundle_digest' \
  || fail "verify failed for a reason other than bundle_digest:
$VERIFY_OUT"

# --- 9. tamper (ledger): corrupt one payload byte; audit must fail ---
sqlite3 "$COPY/.multiverso/ledger.db" \
  "UPDATE events SET payload = X'58' || substr(payload, 2) WHERE seq = 1;"
if AUDIT_OUT="$("$MVO" audit --dir "$COPY" 2>&1)"; then
  fail "audit succeeded on a corrupted ledger:
$AUDIT_OUT"
fi
echo "$AUDIT_OUT" | grep -qi 'digest mismatch\|chain\|verify' \
  || fail "audit failed for an unexpected reason:
$AUDIT_OUT"

# --- 10. the original repo still audits clean end-to-end ---
"$MVO" audit --dir "$REPO" | grep -q '^OK:' || fail "audit on the original repo did not print the OK line"

echo "accept: OK"
