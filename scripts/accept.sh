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
# M1f: the guard is rung O-1 and produces NO process at all; the suite is
# observed through the control-plane evidence stream, and the receipt says
# so rather than leaving a reader to assume the strongest reading.
guard = mine.get("tree-guard") or sys.exit("no tree-guard receipt for the winner")
assert guard["result"]["metrics"]["protected_modified"] == 0, guard["result"]["metrics"]
assert guard["result"]["metrics"]["harness_added"] == 0, guard["result"]["metrics"]
assert guard["execution"]["argv"] == [], guard["execution"]
assert guard["execution"]["evidence_regime"] == "control-plane", guard["execution"]
assert guard["execution"]["evidence_plugin"] == "", guard["execution"]
assert suite["execution"]["evidence_regime"] == "streamed", suite["execution"]
assert suite["execution"]["evidence_plugin"].startswith("sha256:"), suite["execution"]
assert suite["result"]["tools"].get("mvo-evidence") == "v0", suite["result"]["tools"]
' "$WORLD_A" || fail "the winner does not carry the ladder's guard + collect + suite receipts"
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
# M1f: the fake agent produces the SAME fix twice, so the two worlds tie on
# every ranking key that measures anything. Under M1e's default wall_ms_asc
# broke that tie on scheduler jitter and printed a signed rationale naming
# the stopwatch as decisive. The corrected default escalates instead — a
# correct refusal beats a confident wrong answer — and the very first agent
# race a new user runs is where they see it.
echo "$EXPLAIN2" | grep -q '^type: *ESCALATE$' || fail "fake-agent decision is not ESCALATE:
$EXPLAIN2"
echo "$EXPLAIN2" | grep -q 'escalation: on_ranking_tie' \
  || fail "the fake-agent tie was not escalated by on_ranking_tie:
$EXPLAIN2"
# The tier parenthetical is load-bearing: a reader must not have to know
# that T0 means `streamed`, nor be left to assume a stronger regime was
# available and declined.
echo "$EXPLAIN2" | grep -q '^evidence:  regime streamed (T0-worktree; isolated not deliverable in this binary), plugin sha256:' \
  || fail "explain does not print the evidence regime line:
$EXPLAIN2"
echo "$EXPLAIN2" | grep -q 'wall_ms_asc' \
  && fail "the shipped default still ranks by the stopwatch:
$EXPLAIN2"
# An ESCALATE has a LEADER, not a winner (M1e decision 21).
WINNER2="$(echo "$EXPLAIN2" | awk '/^leader:/ {print $2}')"
[ -n "$WINNER2" ] || fail "fake-agent explain printed no leader"

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
[ -n "$WINNER2_PAYLOAD" ] || fail "no world.created payload for fake-agent leader $WINNER2"
PATCH2_KEY="$(echo "$WINNER2_PAYLOAD" | python3 -c 'import json,sys; print(json.load(sys.stdin)["patch"])')"
TRACE2_KEY="$(echo "$WINNER2_PAYLOAD" | python3 -c 'import json,sys; print(json.load(sys.stdin)["trace"])')"
PATCH2_HEX="${PATCH2_KEY#sha256:}"
TRACE2_HEX="${TRACE2_KEY#sha256:}"
PATCH2_FILE="$REPO/.multiverso/cas/sha256/${PATCH2_HEX:0:2}/${PATCH2_HEX:2}"
TRACE2_FILE="$REPO/.multiverso/cas/sha256/${TRACE2_HEX:0:2}/${TRACE2_HEX:2}"
[ -s "$PATCH2_FILE" ] || fail "fake-agent leader patch $PATCH2_KEY is empty or missing"
grep -q 'diff --git' "$PATCH2_FILE" || fail "fake-agent leader patch is not a real captured diff"
[ -s "$TRACE2_FILE" ] || fail "fake-agent leader trace $TRACE2_KEY is empty or missing"
head -1 "$TRACE2_FILE" | grep -q '"type"' || fail "fake-agent leader trace first line carries no event"

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
    #
    # M1f: it also records the regime, and the regime must be one the run
    # ACTUALLY had. This assertion exists because its absence let a real
    # bug ship: `auto` resolved to `isolated` for every T1 race and the
    # receipt recorded it, while oracle execs carry no --user and run from
    # the read-write /work — so `evidence_regime: "isolated"` sat next to
    # `isolation_caps.user: "<invoking uid>"` and not one of that regime's
    # guarantees held. A receipt overstating how it was observed is the
    # study's finding with the label doing the laundering, so the tier that
    # would deliver `isolated` is exactly where it has to be checked.
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
    # No --user exec path ships, so no run may claim isolation from the
    # uid that owns its own worktree.
    regime, uid = ex["evidence_regime"], caps["user"]
    assert regime == "streamed", (
        "T1 suite receipt claims regime %r with oracle uid %r "
        "— the label must match the exec path" % (regime, uid))
    assert ex["evidence_plugin"].startswith("sha256:"), ex["evidence_plugin"]
' "$WINNER4" || fail "T1 suite receipt failed tier/caps/regime assertions"

    # And explain must tell the operator the same thing the receipt says.
    # Captured first: `mvo | grep -q` exits early under `pipefail` and the
    # SIGPIPE, not the match, decides the pipeline.
    EXPLAIN4B="$("$MVO" explain "$INTENT4" --dir "$REPO")"
    echo "$EXPLAIN4B" | grep -q '^evidence:  regime streamed' \
      || fail "explain does not print the T1 race's real evidence regime:
$EXPLAIN4B"
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
# Raced under the ADAPTIVE arm despite ranking by wall_ms_asc, and that is
# validation rule 25 under test rather than an oversight: every selector
# rank-two-keys counts is backed by a hard gate, so a world that declines a
# rung leaves the pass set and the key never compares it. Step 5h races the
# shape where that is NOT true and asserts the refusal.
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
# M1f: under the shipped default patch-cut and patch-wipe are stopped ONE
# RUNG EARLIER by paths-unmodified@guard (they delete a test file), so the
# O0 contract is exercised under no-paths.json — the default minus the
# guard gate, the guard oracle and the invariant. M1e's contract stays
# under live test rather than being deleted along with the behaviour it
# proves (M1f decision 5's fixtures stay meaningful).
cp "$ROOT/testdata/toyrepo/policies/no-paths.json" "$REPO/.multiverso/policies/no-paths.json"
"$MVO" policy use no-paths --dir "$REPO" >/dev/null || fail "policy use no-paths failed"
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

# --- 3k. the stream is the source, and the file is not (still under
# no-paths.json, so the harness seal is deliberately NOT what catches it).
#
# This is the block's central claim and the one the corpus cannot reach:
# every adversarial vector dies at rung O-1, so the protected-paths gate
# masks the mechanism the whole evidence rewrite exists for. Here the guard
# gate, the guard oracle and the invariant are all absent, and the study's
# vector 1 — a conftest.py writing a fabricated 500-test junit.xml at
# atexit — runs its Python to completion.
#
# Under M1e this exact patch passed all three hard gates with
# tests_passed=500 / tests_total=500 in a 3-test repository, won its race,
# landed on trunk, and then verified clean. The numbers below must be the
# STREAM's real ones, and the decision must be REJECT. ---
mkdir -p "$WORK/deep"
cp "$ROOT/testdata/toyrepo/patches-forge/patch-forge-artifact.patch" "$WORK/deep/01-cheat.patch"
INTENTK="$("$MVO" intent new --dir "$REPO" --title "guard removed: the stream holds alone" --budget-candidates 1)"
[ -n "$INTENTK" ] || fail "mvo intent new (deep-forgery intent) printed no digest"
"$MVO" race "$INTENTK" --dir "$REPO" --agent script --patches "$WORK/deep" \
  || fail "a REJECT race exited non-zero"
"$MVO" explain "$INTENTK" --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["type"] == "REJECT", r["type"]
c = r["candidates"][0]
assert c["pass"] is False, c
m = c["metrics"]
# The forged file claims 500. The stream counted the repository.
assert m["tests_total"] == 8, m
assert m["tests_failed"] == 2, m
assert m["tests_passed"] == 6, m
first = next(g for g in c["gates"] if g["result"] == "fail")
assert first["label"] == "status-pass@suite", first
' || fail "the forged junit.xml reached a metric: the evidence path is not severed"
# The receipt must also say HOW it was observed, and name the observer.
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded';" \
  | python3 -c '
import json, sys
recs = [json.loads(line) for line in sys.stdin if line.strip()]
suite = [r for r in recs if r["oracle"]["id"] == "pytest-suite"]
assert suite, "no pytest-suite receipt at all"
r = suite[-1]
ex = r["execution"]
assert ex["evidence_regime"] == "streamed", ex
assert ex["evidence_plugin"].startswith("sha256:"), ex
# A usable stream is what result.tools reports, and it is what
# escalation.require_evidence keys off.
assert r["result"]["tools"].get("mvo-evidence") == "v0", r["result"]["tools"]
' || fail "the suite receipt does not record a usable stream and its observer"

# --- 3j. the protected-paths gate: the study's five laundering vectors,
# all stopped at rung O-1 before any Python of theirs ever runs.
#
# This is M1f's headline. Two of them ship a new conftest.py (harness_added
# — the study's forgery patch's entire content); three edit an existing
# test file (protected_modified). None reaches a collect or a suite run, so
# five forgeries cost five git tree walks. ---
"$MVO" policy use default --dir "$REPO" >/dev/null || fail "policy use default failed"
mkdir -p "$WORK/forge"
cp "$ROOT/testdata/toyrepo/patches-forge/"*.patch "$WORK/forge/"
cp "$REPO/patches/patch-a.patch" "$WORK/forge/00-honest.patch"
INTENTF="$("$MVO" intent new --dir "$REPO" --title "protected paths" --budget-candidates 6)"
[ -n "$INTENTF" ] || fail "mvo intent new (forge intent) printed no digest"
"$MVO" race "$INTENTF" --dir "$REPO" --agent script --patches "$WORK/forge"
"$MVO" explain "$INTENTF" --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["type"] == "SELECT", (r["type"], r["rationale"])
winners = [c for c in r["candidates"] if c["pass"]]
assert len(winners) == 1, [c["world"] for c in winners]
losers = [c for c in r["candidates"] if not c["pass"]]
assert len(losers) == 5, len(losers)
for c in losers:
    first = next(g for g in c["gates"] if g["result"] == "fail")
    assert first["label"] == "paths-unmodified@guard", (c["world"], first)
    # Every later gate is not-evaluated: the ladder short-circuited at O-1.
    for g in c["gates"][1:]:
        assert g["result"] == "not-evaluated", (c["world"], g)
print(winners[0]["world"])
print(" ".join(c["world"] for c in losers))
' > "$WORK/forge.txt" || fail "the protected-paths gate did not stop all five laundering vectors"
FORGE_WINNER="$(sed -n 1p "$WORK/forge.txt")"
FORGE_LOSERS="$(sed -n 2p "$WORK/forge.txt")"
[ "$FORGE_WINNER" != "" ] || fail "no honest winner in the protected-paths race"
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded';" \
  | python3 -c '
import json, sys
losers = set(sys.argv[1].split())
recs = [json.loads(line) for line in sys.stdin if line.strip()]
kinds = {}
for r in recs:
    if r.get("world") in losers:
        kinds.setdefault(r["world"], []).append(r["oracle"]["id"])
assert set(kinds) == losers, (set(kinds), losers)
for world, ks in kinds.items():
    assert sorted(ks) == ["tree-guard"], (world, ks)
' "$FORGE_LOSERS" \
  || fail "a laundering candidate reached a Python oracle (the guard did not short-circuit the ladder)"
# The violation kinds are exactly the two classes the design names, and each
# guard report names the offending path.
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded';" \
  | python3 -c '
import hashlib, json, os, sys
losers, casdir = set(sys.argv[1].split()), sys.argv[2]
recs = [json.loads(line) for line in sys.stdin if line.strip()]
kinds = []
for r in recs:
    if r.get("world") not in losers or r["oracle"]["id"] != "tree-guard":
        continue
    key = r["result"]["artifacts"][0].split(":", 1)[1]
    body = json.load(open(os.path.join(casdir, key[:2], key[2:])))
    assert body["schema"] == "multiverso.dev/tree-guard-report/v0", body["schema"]
    assert body["violations"], body
    for v in body["violations"]:
        kinds.append(v["kind"])
        assert v["path"], v
    assert r["result"]["detail"] == min(v["path"] for v in body["violations"]), r["result"]
want = ["harness_added", "harness_added", "protected_modified", "protected_modified", "protected_modified"]
assert sorted(kinds) == want, sorted(kinds)
' "$FORGE_LOSERS" "$REPO/.multiverso/cas/sha256" \
  || fail "the tree-guard reports do not name the expected violation kinds and paths"

# --- 3o. `mvo guard`, the adoption wedge: the one verb an evaluating
# maintainer can run before adopting anything. It writes NOTHING — no
# ledger event, no worktree, no race. ---
EVENTS_BEFORE="$(sqlite3 "$REPO/.multiverso/ledger.db" "SELECT count(*) FROM events;")"
"$MVO" guard --base HEAD --policy default --dir "$REPO" >/dev/null \
  || fail "mvo guard failed on a clean tree"
cp "$REPO/test_stats.py" "$WORK/test_stats.py.orig"
python3 - "$REPO/test_stats.py" <<'EOF'
import sys
p = sys.argv[1]
src = open(p).read().replace("assert stats.mean([3]) == 3.0", "assert stats.mean([3]) >= 0")
open(p, "w").write(src)
EOF
if GUARD_OUT="$("$MVO" guard --base HEAD --policy default --dir "$REPO" 2>&1)"; then
  fail "mvo guard exited 0 over a modified protected path:
$GUARD_OUT"
fi
echo "$GUARD_OUT" | grep -q 'VIOLATION: protected_modified *test_stats.py' \
  || fail "mvo guard did not name the modified test file:
$GUARD_OUT"
# `guard` exits 1 over a violation, and pipefail would make that the
# pipeline's status; the JSON on stdout is still the product.
"$MVO" guard --base HEAD --policy default --dir "$REPO" --json > "$WORK/guard.json" 2>/dev/null || true
python3 -c '
import json, sys
r = json.load(open(sys.argv[1]))
assert r["schema"] == "multiverso.dev/tree-guard-report/v0", r["schema"]
assert [v["kind"] for v in r["violations"]] == ["protected_modified"], r["violations"]
assert r["violations"][0]["path"] == "test_stats.py", r["violations"][0]
' "$WORK/guard.json" || fail "mvo guard --json does not emit the tree-guard-report shape"
cp "$WORK/test_stats.py.orig" "$REPO/test_stats.py"
EVENTS_AFTER="$(sqlite3 "$REPO/.multiverso/ledger.db" "SELECT count(*) FROM events;")"
[ "$EVENTS_BEFORE" = "$EVENTS_AFTER" ] \
  || fail "mvo guard appended ledger events ($EVENTS_BEFORE -> $EVENTS_AFTER); it must write nothing"

# --- 3q. the shipped `sealed.json` example is INSTALLABLE and RACEABLE.
# It used to declare evidence.regime "isolated" — a regime this binary
# cannot deliver — so it validated clean, installed as the workspace
# default, and then refused every race: a shipped example that bricks a
# workspace until the operator edits it back. Nothing caught that, because
# accept.sh never installed it. It does now, and `policy use` refuses an
# undeliverable regime at install time rather than at first race. ---
cp "$ROOT/testdata/toyrepo/policies/sealed.json" "$REPO/.multiverso/policies/sealed.json"
"$MVO" policy validate "$REPO/.multiverso/policies/sealed.json" --dir "$REPO" | grep -q '^OK: policy valid$' \
  || fail "the shipped sealed.json does not validate"
"$MVO" policy use sealed --dir "$REPO" >/dev/null \
  || fail "policy use sealed failed: a shipped example policy must be installable"
SEALED_INTENT="$("$MVO" intent new --dir "$REPO" --title "sealed fixture" --budget-candidates 2)"
"$MVO" race "$SEALED_INTENT" --dir "$REPO" --agent script --patches "$REPO/patches" >/dev/null \
  || fail "a race under the shipped sealed.json failed as machinery"
SEALED_EXPLAIN="$("$MVO" explain "$SEALED_INTENT" --dir "$REPO")"
echo "$SEALED_EXPLAIN" | grep -q '^type: *SELECT$' \
  || fail "the sealed.json race did not SELECT the honest patch:
$SEALED_EXPLAIN"
echo "$SEALED_EXPLAIN" | grep -q 'skips-not-above@suite' \
  || fail "the sealed.json ladder does not include the skips-not-above gate the docs cite:
$SEALED_EXPLAIN"

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
echo "$VALIDATE_OUT" | grep -q 'known: collect-nonempty, collected-not-below, corpus-complete, coverage-at-least, differential-cohort-at-least, mutation-survivors-not-above, no-failed-tests, paths-unmodified, properties-pass, property-cases-at-least, skips-not-above, status-pass' \
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

# --- 3r/3s. THE CROSS-CANDIDATE DIFFERENTIAL (EP-4). Two candidates that
# implement clamp correctly for every input the eight-test suite exercises,
# tie on every ranking key that measures anything, and DIVERGE on
# clamp(nan, 0, 10): nan versus 0.
#
# Under M1f this race ESCALATEs on on_ranking_tie and tells the maintainer
# nothing except that two digests tied. Under M2a it ESCALATEs on
# on_behavioral_split and hands them the input and both answers. That
# difference is the block. ---
cp "$ROOT/testdata/toyrepo/policies/differential.json" "$REPO/.multiverso/policies/differential.json"
"$MVO" policy validate "$REPO/.multiverso/policies/differential.json" --dir "$REPO" | grep -q '^OK: policy valid$' \
  || fail "the shipped differential policy does not validate"
"$MVO" policy use differential --dir "$REPO" >/dev/null || fail "policy use differential failed"
INTENT_DIFF="$("$MVO" intent new --dir "$REPO" --title "behavioural split")"
[ -n "$INTENT_DIFF" ] || fail "mvo intent new (differential intent) printed no digest"
"$MVO" race "$INTENT_DIFF" --dir "$REPO" --agent script --patches "$REPO/patches-behave" \
  || fail "the differential race exited non-zero"

# 3r — the corpus is materialized ON THE BASE TREE and never reaches a
# generator (M2a decision 13, AG-7): one corpus.recorded event, before every
# world.created, and no world's captured patch, agent context or transcript
# contains the corpus digest or a case id.
CORPUS_EVENTS="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT count(*) FROM events WHERE type='corpus.recorded';")"
[ "$CORPUS_EVENTS" -ge 1 ] || fail "no corpus.recorded event was appended"
CORPUS_SEQ="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT min(seq) FROM events WHERE type='corpus.recorded';")"
WORLD_SEQ="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT min(seq) FROM events WHERE type='world.created' AND seq > $CORPUS_SEQ;")"
[ -n "$WORLD_SEQ" ] && [ "$CORPUS_SEQ" -lt "$WORLD_SEQ" ] \
  || fail "corpus.recorded (seq $CORPUS_SEQ) does not precede the worlds it will be compared over"
CORPUS_DIG="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT json_extract(cast(payload AS text), '\$.corpus') FROM events WHERE type='corpus.recorded' ORDER BY seq LIMIT 1;")"
[ -n "$CORPUS_DIG" ] || fail "corpus.recorded names no corpus digest"
# The negative half: the corpus exists on disk while agents run, and no
# agent ever saw it. A generator that can read the corpus can special-case
# it, which is corpus vector 19 and is the reason phase A mounts nothing.
LEAK="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT count(*) FROM events WHERE type IN ('world.created','agent.started','agent.finished') AND (cast(payload AS text) LIKE '%$CORPUS_DIG%' OR cast(payload AS text) LIKE '%c0001%');")"
[ "$LEAK" = "0" ] || fail "the corpus digest or a case id reached a generator ($LEAK events)"

# 3r2 — and the corpus is not REACHABLE either, which is a different claim
# and the one that was false. Decision 13's "never inside a world's tree"
# was literally true while <raceDir>/corpus sat as the parent-SIBLING of
# every world's worktree, so `../corpus/corpus.json` from an ordinary pytest
# collection walked out with every case, target, argument and id. This step
# is the assertion whose absence let that stand: a world whose
# sitecustomize.py goes looking by every relative path a candidate would try
# must find NOTHING, and it fails itself loudly if it does.
#
# The honest scope, stated because the test cannot enforce more than it
# checks: at T0 the oracle and the candidate run as the same uid on the same
# filesystem, so an adversary who knows the workspace layout still finds the
# file by absolute path. What decision 13 delivers at T0 is "not DELIVERED to
# the generating agent", not "not REACHABLE by it".
INTENT_REACH="$("$MVO" intent new --dir "$REPO" --title "corpus reachability")"
"$MVO" race "$INTENT_REACH" --dir "$REPO" --agent script --patches "$REPO/patches-reach" \
  || fail "the corpus-reachability race exited non-zero"
"$MVO" explain "$INTENT_REACH" --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
probes = [c for c in r["candidates"] if not c["pass"]]
assert not probes, (
    "a world reached the pinned corpus by a relative path from its own cwd: "
    + json.dumps(probes))
' || fail "the corpus is reachable from a candidate world by a relative path; decision 13's
     guarantee is delivery, not unreachability, and the layout must not make the walk trivial"

# 3s — the differential escalates on a real split.
EXPLAIN_DIFF="$("$MVO" explain "$INTENT_DIFF" --dir "$REPO")"
echo "$EXPLAIN_DIFF" | grep -q '^type: *ESCALATE$' \
  || fail "the differential race did not ESCALATE:
$EXPLAIN_DIFF"
echo "$EXPLAIN_DIFF" | grep -q 'escalated by policy rule on_behavioral_split' \
  || fail "the rationale does not name on_behavioral_split:
$EXPLAIN_DIFF"
# The human rendering hands the maintainer the INPUT and both answers.
echo "$EXPLAIN_DIFF" | grep -q 'stats:clamp(nan, 0, 10)' \
  || fail "explain does not print the distinguishing call:
$EXPLAIN_DIFF"
# NEITHER world is rendered as a winner: a correct refusal is not a win.
echo "$EXPLAIN_DIFF" | grep -q 'WINNER' && fail "an ESCALATE rendered a WINNER:
$EXPLAIN_DIFF"
echo "$EXPLAIN_DIFF" | grep -q ' won ' && fail "an ESCALATE rationale says a world won:
$EXPLAIN_DIFF"
"$MVO" explain "$INTENT_DIFF" --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["type"] == "ESCALATE", r["type"]
assert r["escalation"]["rule"] == "on_behavioral_split", r["escalation"]
# Both candidates passed every hard gate: the ladder had nothing left to
# say and the difference is real.
assert len(r["candidates"]) == 2, r["candidates"]
for c in r["candidates"]:
    assert c["pass"] is True, c
b = r["behavior"]
assert len(b["classes"]) == 2, b["classes"]
assert b["cases_compared"] == 4, b
d = b["distinguishing"]
assert len(d) == 1 and d[0]["case"] == "c0001", d
answers = sorted(o["value"] for o in d[0]["observations"])
assert answers == ["0", "nan"], answers
' || fail "explain --json does not carry the behavioural split with its distinguishing case"

# The control assertion: the mechanism has TEETH rather than firing on
# everything. Two behaviourally identical candidates produce ONE class and
# no split.
INTENT_SAME="$("$MVO" intent new --dir "$REPO" --title "no split")"
"$MVO" race "$INTENT_SAME" --dir "$REPO" --agent script --patches "$REPO/patches-agree" \
  || fail "the control differential race exited non-zero"
"$MVO" explain "$INTENT_SAME" --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["escalation"].get("rule") != "on_behavioral_split", r["escalation"]
b = r.get("behavior")
assert b is not None, "no behaviour block on a differential race"
assert len(b["classes"]) == 1, b["classes"]
' || fail "the differential fired on two candidates that behave identically"

# 3t — a comparison of one is not a comparison: diff_cohort_n is present and
# every other diff_* metric is ABSENT, never a fabricated zero.
INTENT_ONE="$("$MVO" intent new --dir "$REPO" --budget-candidates 1 --title "cohort of one")"
mkdir -p "$WORK/one" && cp "$REPO/patches-behave/patch-p.patch" "$WORK/one/"
"$MVO" race "$INTENT_ONE" --dir "$REPO" --agent script --patches "$WORK/one" \
  || fail "the one-candidate differential race exited non-zero"
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded' AND cast(payload AS text) LIKE '%corpus-differential%' ORDER BY seq DESC LIMIT 1;" \
  | python3 -c '
import json, sys
rec = json.loads(sys.stdin.read())
m = rec["result"]["metrics"]
assert m.get("diff_cohort_n") == 1, m
assert list(m) == ["diff_cohort_n"], m
assert rec["execution"]["evidence_regime"] == "derived", rec["execution"]
assert rec["inputs"]["evidence_floor"] == "streamed", rec["inputs"]
' || fail "a cohort of one did not record absence: every diff_* but diff_cohort_n must be missing"

# 3u — cohort starvation is SELF-elimination (corpus vector 18). A candidate
# that silences the corpus runner to shrink the cohort fails its OWN
# corpus-complete gate, and the honest world is not convicted of it.
INTENT_STARVE="$("$MVO" intent new --dir "$REPO" --title "cohort starvation")"
"$MVO" race "$INTENT_STARVE" --dir "$REPO" --agent script --patches "$REPO/patches-starve" \
  || fail "the starvation race exited non-zero"
"$MVO" explain "$INTENT_STARVE" --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
cands = r["candidates"]
assert len(cands) == 2, cands
starved = [c for c in cands if not c["pass"]]
honest = [c for c in cands if c["pass"]]
assert len(starved) == 1 and len(honest) == 1, cands
# The starving world stops at its OWN gate.
failed = [g["label"] for g in starved[0]["gates"] if g["result"] == "fail"]
assert failed == ["corpus-complete@observe"], failed
# And the surviving comparison declines to be a comparison rather than
# quietly comparing against a smaller denominator.
b = r.get("behavior")
assert b is not None and b["cohort_n"] == 1, b
' || fail "cohort starvation was not self-elimination"

# 3u2 — A CORPUS POLICY MUST STILL BE ABLE TO LAND SOMETHING. Validation
# rule 23 is decision 21's argument one rung below the cohort stage: the
# corpus a `corpus-complete` gate reads is materialized by the race's phase
# 0, and `mvo admit` has no phase 0, so a landing-scope corpus gate is not a
# gate that fails — it is a gate that cannot be evaluated, and an
# admission that aborts as machinery forever. The gate declares scope
# "race", `mvo admit` NAMES it as unevaluated, and the landing succeeds.
#
# Raced in a throwaway clone of the fixture so the main repo's trunk, its
# ledger and steps 4-9's admission are untouched.
REPO_ADM="$WORK/toyrepo-admit"
mkdir -p "$REPO_ADM"
cp -R "$ROOT/testdata/toyrepo/." "$REPO_ADM/"
rm -rf "$REPO_ADM/__pycache__" "$REPO_ADM/.pytest_cache"
$GIT -C "$REPO_ADM" init -q -b main
$GIT -C "$REPO_ADM" add -A
$GIT -C "$REPO_ADM" commit -qm "toyrepo baseline (admission under a corpus policy)"
"$MVO" init --dir "$REPO_ADM" >/dev/null
cp "$ROOT/testdata/toyrepo/policies/differential.json" "$REPO_ADM/.multiverso/policies/differential.json"
"$MVO" policy use differential --dir "$REPO_ADM" >/dev/null || fail "policy use differential (admission repo) failed"
INTENT_ADM="$("$MVO" intent new --dir "$REPO_ADM" --title "land under a corpus policy" --budget-candidates 1)"
mkdir -p "$WORK/adm-patches" && cp "$REPO_ADM/patches-behave/patch-p.patch" "$WORK/adm-patches/"
"$MVO" race "$INTENT_ADM" --dir "$REPO_ADM" --agent script --patches "$WORK/adm-patches" \
  || fail "the admission-under-a-corpus-policy race exited non-zero"
ADMIT_OUT="$("$MVO" admit "$INTENT_ADM" --dir "$REPO_ADM" 2>&1)" \
  || fail "mvo admit could not land under a policy with a corpus gate — a corpus policy that can
     never admit anything is the sealed.json failure rebuilt one rung lower:
$ADMIT_OUT"
printf '%s' "$ADMIT_OUT" | grep -q 'race-scope gates not evaluated at admission: corpus-complete@observe' \
  || fail "admission did not NAME the corpus gate it could not evaluate; a landing gate set weaker
     than the race's is a legitimate choice and must never be an invisible one:
$ADMIT_OUT"
"$MVO" verify HEAD --dir "$REPO_ADM" >/dev/null \
  || fail "the commit landed under a corpus policy does not verify"

"$MVO" policy use default --dir "$REPO" >/dev/null || fail "policy use default (after differential) failed"

# --- 3v. mutation (O3), or a NAMED SKIP. Neither cosmic-ray nor mutmut is
# installed on the machine M2a was written on, so the skip is the live path
# — and a skip that renders like a measurement is exactly the over-claim
# this project exists to remove. The absent-toolchain half is therefore
# ASSERTED rather than skipped: the race must abort at pre-flight, with the
# decision-20 sentence and an UNTOUCHED ledger. ---
cp "$ROOT/testdata/toyrepo/policies/mutation.json" "$REPO/.multiverso/policies/mutation.json"
"$MVO" policy validate "$REPO/.multiverso/policies/mutation.json" --dir "$REPO" | grep -q '^OK: policy valid$' \
  || fail "policy validate rejected the mutation fixture policy"
MUT_TOOL="$(python3 -c '
import importlib.metadata as m
for name in ("cosmic-ray", "mutmut"):
    try:
        m.version(name)
        print(name)
        break
    except Exception:
        pass
')"
"$MVO" policy use mutation --dir "$REPO" >/dev/null || fail "policy use mutation failed"
INTENT_MUT="$("$MVO" intent new --dir "$REPO" --title "mutation" --budget-candidates 2)"
[ -n "$INTENT_MUT" ] || fail "mvo intent new (mutation) printed no digest"
EVENTS_BEFORE="$(sqlite3 "$REPO/.multiverso/ledger.db" "SELECT count(*) FROM events;")"
if [ -z "$MUT_TOOL" ]; then
  echo "SKIP 3v (measured half): neither cosmic-ray nor mutmut is importable in this environment; asserting the pre-flight abort instead"
  if MUT_OUT="$("$MVO" race "$INTENT_MUT" --dir "$REPO" --agent script --patches "$REPO/patches" 2>&1)"; then
    fail "mvo race succeeded with no mutation toolchain installed:
$MUT_OUT"
  fi
  printf '%s' "$MUT_OUT" | grep -q 'cosmic-ray or mutmut' \
    || fail "the pre-flight abort does not name the missing toolchain:
$MUT_OUT"
  printf '%s' "$MUT_OUT" | grep -q 'machinery, never a failing candidate' \
    || fail "the pre-flight abort does not say a missing toolchain is machinery:
$MUT_OUT"
  EVENTS_AFTER="$(sqlite3 "$REPO/.multiverso/ledger.db" "SELECT count(*) FROM events;")"
  [ "$EVENTS_BEFORE" = "$EVENTS_AFTER" ] \
    || fail "a pre-flight abort wrote $((EVENTS_AFTER - EVENTS_BEFORE)) ledger event(s); the ledger must be untouched"
else
  "$MVO" race "$INTENT_MUT" --dir "$REPO" --agent script --patches "$REPO/patches" \
    || fail "the mutation race failed with $MUT_TOOL installed"
  sqlite3 "$REPO/.multiverso/ledger.db" \
    "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded';" \
    | python3 -c '
import json, sys
recs = [json.loads(line) for line in sys.stdin if line.strip()]
muts = [r for r in recs if r["oracle"]["id"] == "mutation-diff"]
assert muts, "no mutation-diff receipt was recorded"
for r in muts:
    m = r["result"]["metrics"]
    if r["result"]["status"] != "pass":
        continue
    assert m["mutants_tested"] <= m["mutants_budget"], m
    assert r["inputs"]["diff_target"].startswith("mv0:"), r["inputs"]
    assert r["inputs"]["mutant_selection"] in ("control-plane", "tool"), r["inputs"]
    assert r["cost"]["unit"] == "mutants", r["cost"]
    # A ratio over an empty denominator is ABSENT, never a zero score.
    if m.get("mutants_killed", 0) + m.get("mutants_survived", 0) == 0:
        assert "mutation_score_bp" not in m, m
print("mutation receipts ok")
' || fail "the mutation receipts do not honour the pinned ceiling and its provenance"
fi

# --- 3w. properties (O2p), or a NAMED SKIP. Same shape, and the same live
# path: hypothesis is absent here. When it IS present, the decision-15 rule
# is what is asserted — property_cases_* are either PRESENT with
# result.tools["hypothesis-observability"] == "stream", or ABSENT with
# "jsonl". Never present with jsonl. ---
cp "$ROOT/testdata/toyrepo/policies/properties.json" "$REPO/.multiverso/policies/properties.json"
mkdir -p "$REPO/props" && cp "$ROOT/testdata/toyrepo/props/mvo_props.py" "$REPO/props/mvo_props.py"
"$MVO" policy validate "$REPO/.multiverso/policies/properties.json" --dir "$REPO" | grep -q '^OK: policy valid$' \
  || fail "policy validate rejected the properties fixture policy"
# The declared property module is HARNESS-frozen (decision 14, corpus
# vector 16): a candidate that rewrites it dies at rung O-1.
"$MVO" policy show properties --dir "$REPO" | grep -q 'props/mvo_props.py' \
  || fail "the declared property module is not in the compiled harness set:
$("$MVO" policy show properties --dir "$REPO")"
HAS_HYP="$(python3 -c '
import importlib.metadata as m
try:
    print(m.version("hypothesis"))
except Exception:
    print("")
')"
"$MVO" policy use properties --dir "$REPO" >/dev/null || fail "policy use properties failed"
INTENT_PROP="$("$MVO" intent new --dir "$REPO" --title "properties" --budget-candidates 2)"
EVENTS_BEFORE="$(sqlite3 "$REPO/.multiverso/ledger.db" "SELECT count(*) FROM events;")"
if [ -z "$HAS_HYP" ]; then
  echo "SKIP 3w (measured half): hypothesis is not importable in this environment; asserting the pre-flight abort instead"
  if PROP_OUT="$("$MVO" race "$INTENT_PROP" --dir "$REPO" --agent script --patches "$REPO/patches" 2>&1)"; then
    fail "mvo race succeeded with no hypothesis installed:
$PROP_OUT"
  fi
  printf '%s' "$PROP_OUT" | grep -q 'hypothesis is not importable' \
    || fail "the pre-flight abort does not name hypothesis:
$PROP_OUT"
  EVENTS_AFTER="$(sqlite3 "$REPO/.multiverso/ledger.db" "SELECT count(*) FROM events;")"
  [ "$EVENTS_BEFORE" = "$EVENTS_AFTER" ] \
    || fail "a pre-flight abort wrote $((EVENTS_AFTER - EVENTS_BEFORE)) ledger event(s); the ledger must be untouched"
else
  "$MVO" race "$INTENT_PROP" --dir "$REPO" --agent script --patches "$REPO/patches" \
    || fail "the properties race failed with hypothesis installed"
  sqlite3 "$REPO/.multiverso/ledger.db" \
    "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded';" \
    | python3 -c '
import json, sys
recs = [json.loads(line) for line in sys.stdin if line.strip()]
props = [r for r in recs if r["oracle"]["id"] == "hypothesis-properties"]
assert props, "no hypothesis-properties receipt was recorded"
for r in props:
    m, tools = r["result"]["metrics"], r["result"]["tools"]
    source = tools.get("hypothesis-observability", "")
    has_cases = "property_cases_total" in m
    # Decision 15, byte for byte: one metric name, one provenance.
    assert not (has_cases and source == "jsonl"), (m, tools)
    if has_cases:
        assert source == "stream", tools
    if source == "jsonl":
        assert "property_cases_invalid" not in m, m
print("property receipts ok")
' || fail "the property receipts violate the decision-15 provenance rule"
fi
"$MVO" policy use default --dir "$REPO" >/dev/null || fail "policy use default (after properties) failed"

# --- 3x. THE COMPATIBILITY PROOF (M2a decision 25), under test rather than
# in prose. Two halves, and both are claims a reader of the design document
# would otherwise have to take on faith:
#
#   (a) the SHIPPED DEFAULT DOES NOT MOVE. `mvo policy show default
#       --builtin --json` re-digests to the M1f constant, and none of M2a's
#       four additive policy fields appears in its bytes — because a field
#       that always serialized would mint a new digest for every existing
#       policy, which is a silent replay break dressed as a schema addition.
#   (b) a ledger written by the M1f BINARY replays byte-for-byte under this
#       one. Not a ledger this binary wrote a minute ago: bytes produced
#       before M2a existed, which is the only version of the claim worth
#       making. The M1f binary is built from its own commit in a throwaway
#       worktree; when that commit cannot be resolved (a shallow clone, a
#       tarball) the half is SKIPPED BY NAME rather than quietly passing. ---
M2A_DEFAULT="mv0:f207c3fad59d0fc973e5f342ac54d8b1bc9e5c6cae2a2cff0b33477ddee3c543"
BUILTIN_JSON="$("$MVO" policy show default --builtin --json --dir "$REPO")"
BUILTIN_DIG="mv0:$(printf '%s' "$BUILTIN_JSON" | python3 -c 'import hashlib,sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())')"
[ "$BUILTIN_DIG" = "$M2A_DEFAULT" ] \
  || fail "the built-in default policy moved: $BUILTIN_DIG, want $M2A_DEFAULT
     M2a decision 25 says every new rung is opt-in and the shipped default is byte-identical to M1f's."
printf '%s' "$BUILTIN_JSON" | python3 -c '
import json, sys
raw = sys.stdin.read()
pol = json.loads(raw)
# The four additive v1 fields must be ABSENT from a policy that declares
# none of them. Their compiled defaults reproduce M1f semantics exactly
# (M1f decision 3), and their omission is what keeps every pre-M2a policy
# digest where it was.
for key in ("on_behavioral_split",):
    assert key not in pol["escalation"], ("escalation." + key, pol["escalation"])
for gate in pol["hard_gates"]:
    assert "scope" not in gate, gate
for oracle in pol["oracles"]:
    assert "corpus" not in oracle, oracle
    assert "mutation" not in oracle, oracle
' || fail "the shipped default carries an M2a field it does not declare"

M1F_COMMIT="$(git -C "$ROOT" rev-list -1 --grep '^M1f:' HEAD 2>/dev/null || true)"
if [ -z "$M1F_COMMIT" ]; then
  echo "SKIP 3x (replay half): no M1f commit reachable from HEAD in $ROOT (shallow clone or unpacked tarball); the byte-for-byte replay of PRE-M2a ledger bytes cannot be built here"
else
  OLDTREE="$WORK/m1f-src"
  git -C "$ROOT" worktree add -q --detach "$OLDTREE" "$M1F_COMMIT" \
    || fail "could not check out the M1f commit $M1F_COMMIT"
  OLDMVO="$WORK/mvo-m1f"
  (cd "$OLDTREE" && go build -o "$OLDMVO" ./cmd/mvo) \
    || fail "the M1f binary does not build from $M1F_COMMIT"
  OLDREPO="$WORK/m1f-repo"
  mkdir -p "$OLDREPO"
  cp -R "$OLDTREE/testdata/toyrepo/." "$OLDREPO/"
  rm -rf "$OLDREPO/__pycache__" "$OLDREPO/.pytest_cache"
  $GIT -C "$OLDREPO" init -q -b main
  $GIT -C "$OLDREPO" add -A
  $GIT -C "$OLDREPO" commit -qm "m1f-era baseline"
  "$OLDMVO" init --dir "$OLDREPO" >/dev/null || fail "the M1f binary could not init a workspace"
  OLD_INTENT="$("$OLDMVO" intent new --dir "$OLDREPO" --title "m1f-era race")"
  "$OLDMVO" race "$OLD_INTENT" --dir "$OLDREPO" --agent script --patches "$OLDREPO/patches" >/dev/null \
    || fail "the M1f binary could not race its own fixture"
  OLD_RATIONALE="$("$OLDMVO" explain "$OLD_INTENT" --dir "$OLDREPO" | grep '^rationale:')"
  # The M2a binary now reads bytes it did not write. audit re-derives every
  # recorded decision from the recorded inputs and compares the rationale
  # it produces against the recorded one.
  "$MVO" audit --dir "$OLDREPO" --require-decisions 1 --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["chain_ok"] is True, r
assert r["replay_identical"] is True, r
assert r["decisions"] >= 1, r
' || fail "an M1f-era ledger does not replay identically under the M2a binary"
  NEW_RATIONALE="$("$MVO" explain "$OLD_INTENT" --dir "$OLDREPO" | grep '^rationale:')"
  [ "$OLD_RATIONALE" = "$NEW_RATIONALE" ] \
    || fail "the M2a binary renders an M1f-era decision differently:
M1f: $OLD_RATIONALE
M2a: $NEW_RATIONALE"
  # And the M1f-era pinned policy still compiles to the same instance, so
  # every historical receipt still matches its own gate's selector.
  # (Captured, not piped: `grep -q` closes the pipe on its first match and
  # `set -o pipefail` would turn the writer's EPIPE into a false failure.)
  OLD_SHOW="$("$MVO" policy show default --dir "$OLDREPO")"
  printf '%s' "$OLD_SHOW" | grep -q "$M2A_DEFAULT" \
    || fail "the M1f-era workspace's default policy digest moved under the M2a binary:
$OLD_SHOW"
  git -C "$ROOT" worktree remove --force "$OLDTREE" >/dev/null 2>&1 || true
fi

# --- 3y. `mvo oracles` — the menu M2b allocates over. The assertion that
# matters is not that a number is printed: it is that an UNMEASURED kind
# never renders like a measured one. A scheduler that reads a fabricated
# coefficient buys the wrong rung, and a two-point fit dressed as a fact is
# the same over-claim as a skipped gate rendered green. ---
MENU="$("$MVO" oracles --dir "$REPO")" || fail "mvo oracles exited non-zero"
for kind in tree-guard pytest-collect pytest-suite hypothesis-properties \
            corpus-observe corpus-differential mutation-diff; do
  printf '%s' "$MENU" | grep -q "^$kind " \
    || fail "mvo oracles does not render the kind $kind:
$MENU"
done
printf '%s' "$MENU" | grep -q '^measured in this workspace:' \
  || fail "mvo oracles prints no measurement block:
$MENU"
"$MVO" oracles --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["schema"] == "multiverso.dev/oracle-menu/v0", r["schema"]
kinds = {k["kind"]: k for k in r["kinds"]}
for want in ("tree-guard", "pytest-collect", "pytest-suite", "hypothesis-properties",
             "corpus-observe", "corpus-differential", "mutation-diff"):
    assert want in kinds, sorted(kinds)
measured = 0
for name, k in kinds.items():
    # Every kind declares its shape without running anything.
    assert k["stage"] in ("world", "cohort"), k
    assert isinstance(k["correlation"], dict), k
    if k["measurement"] is None:
        # An absent measurement SAYS SO, and says how many samples it had.
        assert k["measurement_note"].startswith("no local measurement (n="), k
        assert ("n=%d" % k["measurement_n"]) in k["measurement_note"], k
    else:
        measured += 1
        m = k["measurement"]
        assert m["n"] >= 3, k
        # TWO ESTIMATORS, TWO CONTRACTS, and the difference is exactly what
        # was measured. Theil-Sen needs two samples with distinct unit counts
        # and reports a slope. A population whose unit count never varied —
        # which is EVERY pytest population on a single-repo workspace, since
        # the unit is the repository test count — measures a FIXED cost and
        # no slope at all: the median wall time is a real measurement and
        # the missing coefficient is stated rather than printed as a zero.
        if m["estimator"] == "median-fixed":
            assert m["units_min"] == m["units_max"], k
            assert m["per_unit_ms"] == 0, k
            assert "units do not vary" in k["measurement_note"], k
            assert "fixed cost only" in k["measurement_note"], k
        else:
            assert m["estimator"] == "theil-sen", k
            assert k["measurement_note"] == "", k
            assert m["units_min"] < m["units_max"], k
# The cohort rung is the one M2b needs a barrier for, and it must say so.
assert kinds["corpus-differential"]["stage"] == "cohort", kinds["corpus-differential"]
assert kinds["corpus-differential"]["discriminates"] == "partition", kinds["corpus-differential"]
print("oracle menu ok (%d of %d kinds measured in this workspace)" % (measured, len(kinds)))
' || fail "mvo oracles --json renders a measurement that is not one"
# A kind this workspace never ran must be unmeasured, with its count. This
# is the negative half: the command has to be capable of saying "I do not
# know" about something.
#
# WHICH kinds those are is a property of THIS MACHINE, not of the code, and
# hard-coding the list made the assertion environment-dependent: steps 3v and
# 3w race the mutation and property ladders whenever their toolchains are
# importable, so on a machine with hypothesis installed the properties rung
# really was bought and n == 1 is the TRUE answer. The assertion that
# survives both environments is the rule itself — below minSamples there is
# no fit, and the count reported is the real one — plus n == 0 for exactly
# the rungs whose toolchain is missing here.
NEVER_RAN=""
[ -z "$MUT_TOOL" ] && NEVER_RAN="$NEVER_RAN mutation-diff"
[ -z "$HAS_HYP" ] && NEVER_RAN="$NEVER_RAN hypothesis-properties"
"$MVO" oracles --dir "$REPO" --json | NEVER_RAN="$NEVER_RAN" python3 -c '
import json, os, sys
kinds = {k["kind"]: k for k in json.load(sys.stdin)["kinds"]}
for absent in os.environ["NEVER_RAN"].split():
    k = kinds[absent]
    assert k["measurement"] is None, (absent, k)
    assert k["measurement_n"] == 0, (absent, k)
    assert k["measurement_note"] == "no local measurement (n=0, need 3)", (absent, k)
# The rule, over every kind, and it is one rule in both directions: a
# coefficient exists only where one could be fitted, and where none could be
# the command says so AND reports the sample count it actually has. Below the
# floor the note names the floor; at or above it, the note names the reason
# the samples could not produce a slope (all-equal unit counts, say). Never a
# number, never a silence.
for name, k in kinds.items():
    n = k["measurement_n"]
    if k["measurement"] is None:
        assert k["measurement_note"].startswith("no local measurement (n=%d" % n), (name, k)
        if n < 3:
            assert k["measurement_note"] == ("no local measurement (n=%d, need 3)" % n), (name, k)
    else:
        assert n >= 3, (name, k)
        if k["measurement"]["estimator"] == "median-fixed":
            # A fixed cost measured n times with no slope to measure. The
            # note is not silence and it is not a number nobody measured: it
            # names which half of the model exists and which does not.
            assert "units do not vary" in k["measurement_note"], (name, k)
            assert k["measurement"]["per_unit_ms"] == 0, (name, k)
        else:
            assert k["measurement_note"] == "", (name, k)
# And the command has to be CAPABLE of saying "I do not know": at least one
# kind on the menu must report n == 0 and render it as such.
unbought = [name for name, k in kinds.items() if k["measurement_n"] == 0]
assert unbought, "every kind on the menu reports a sample; the n=0 rendering is untested here"
' || fail "mvo oracles claims a measurement for a rung this workspace never bought"
# The menu fixture policy declares every rung, which is what M2b allocates
# over. It must validate, and `--policy` must attribute its instances.
cp "$ROOT/testdata/toyrepo/policies/menu.json" "$REPO/.multiverso/policies/menu.json"
"$MVO" policy validate "$REPO/.multiverso/policies/menu.json" --dir "$REPO" | grep -q '^OK: policy valid$' \
  || fail "the menu fixture policy (every rung) does not validate"
MENU_POL="$("$MVO" oracles --dir "$REPO" --policy menu)" || fail "mvo oracles --policy menu exited non-zero"
printf '%s' "$MENU_POL" | grep -q 'declared by policy menu' \
  || fail "mvo oracles --policy does not attribute the policy's instances:
$MENU_POL"
MENU_DECLARED="$(printf '%s\n' "$MENU_POL" | sed -n '/declared by policy/,/^$/p')"
for kind in tree-guard pytest-collect pytest-suite corpus-observe corpus-differential \
            hypothesis-properties mutation-diff; do
  printf '%s' "$MENU_DECLARED" | grep -q "^  $kind " \
    || fail "mvo oracles --policy menu does not list an instance for $kind:
$MENU_DECLARED"
done

# --- 3p. docs freshness: the reader-facing pages must agree with the
# binary about the two things a reader copies verbatim — the shipped
# default policy digest and the closed gate vocabulary. The design partner
# study's finding was that docs/quickstart.md had gone factually false; it
# then went false again in the opposite direction the moment the default
# changed. A grep is a poor proof of correctness and a perfectly good proof
# of NON-ROT, which is the failure that actually happened twice. ---
DOC_DIG_SHORT="$(printf '%s' "$DEFAULT_DIG" | cut -c1-12)"
for doc in "$ROOT/docs/quickstart.md" "$ROOT/docs/design/M1f-trust-boundary.md"; do
  grep -q "$DOC_DIG_SHORT" "$doc" \
    || fail "$(basename "$doc") does not mention the shipped default policy digest $DEFAULT_DIG
     (it pins an older one — re-record its transcripts)"
done
GATE_VOCAB='collect-nonempty, collected-not-below, corpus-complete, coverage-at-least, differential-cohort-at-least, mutation-survivors-not-above, no-failed-tests, paths-unmodified, properties-pass, property-cases-at-least, skips-not-above, status-pass'
grep -qF "$GATE_VOCAB" "$ROOT/docs/quickstart.md" \
  || fail "docs/quickstart.md does not carry the current known-gate list:
     $GATE_VOCAB"
for phrase in 'paths-unmodified@guard' 'plugin_autoload' 'multiverso.dev/audit-report/v1'; do
  grep -qF "$phrase" "$ROOT/docs/quickstart.md" \
    || fail "docs/quickstart.md never mentions $phrase"
done
# The two sentences the study found factually false, and their opposites.
grep -qF 'It does not sweep' "$ROOT/docs/quickstart.md" \
  && fail "docs/quickstart.md still claims mvo audit does not sweep CAS; it does (step 9a proves it)"

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
  --oracle-cmd "python3 -m pytest -q" --schedule=fixed
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

# =====================================================================
# M2b — THE ADAPTIVE SCHEDULER (docs/design/M2b-adaptive-scheduler.md §11).
#
# Seven steps, and none of them changes the shipped default. The block's
# claim is narrow and worth pinning exactly: the scheduler decides WHAT
# EVIDENCE TO BUY and `Decide` decides what the evidence means, so under an
# unbounded budget the two arms are indistinguishable (5b), the trace is
# recorded evidence rather than a recomputation (5a, 5d), a race that runs
# out of money says so instead of blaming the candidates (5e), the spend
# that influenced nothing is computable (5f), and none of it leaks into an
# agent's prompt (5g). 5h is validation rule 25.
# =====================================================================

# --- 5a. the trace exists and is complete. Every phase-B receipt is
# preceded by a schedule.step naming it as CHOSEN, every step's considered
# set is non-empty, schedule.started and schedule.finished bracket them,
# and `mvo audit` is OK with the new events present. ---
INTENT_SCHED="$("$MVO" intent new --dir "$REPO" --title "m2b: adaptive trace")"
[ -n "$INTENT_SCHED" ] || fail "mvo intent new (m2b trace intent) printed no digest"
"$MVO" race "$INTENT_SCHED" --dir "$REPO" --agent script --patches "$REPO/patches" \
  || fail "the adaptive race failed"
DEFAULT_POL_JSON="$("$MVO" policy show default --dir "$REPO" --json)"
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT seq || '|' || type || '|' || cast(payload AS text) FROM events ORDER BY seq;" \
  | INTENT="$INTENT_SCHED" POLJSON="$DEFAULT_POL_JSON" python3 -c '
import json, os, sys

intent = os.environ["INTENT"]
pol = json.loads(os.environ["POLJSON"])
kind_of = {o["name"]: o["kind"] for o in pol["oracles"]}

rows = []
for line in sys.stdin:
    line = line.rstrip("\n")
    if not line:
        continue
    seq, typ, payload = line.split("|", 2)
    rows.append((int(seq), typ, json.loads(payload)))

# The race WINDOW: this intent alone. A trace assembled across two races
# would describe an allocation nobody made.
lo = max(s for s, t, p in rows if t == "race.started" and p.get("intent") == intent)
hi = min((s for s, t, p in rows if t == "race.finished" and s > lo), default=10**9)
win = [(s, t, p) for s, t, p in rows if lo < s < hi]

started  = [(s, p) for s, t, p in win if t == "schedule.started"]
steps    = [(s, p) for s, t, p in win if t == "schedule.step"]
finished = [(s, p) for s, t, p in win if t == "schedule.finished"]
receipts = [(s, p) for s, t, p in win if t == "receipt.recorded"]

assert len(started) == 1, ("schedule.started count", len(started))
assert len(finished) == 1, ("schedule.finished count", len(finished))
assert steps, "the race recorded no schedule.step"
assert receipts, "the race recorded no receipts"
assert started[0][0] < min(s for s, _ in steps), "schedule.started does not precede the steps"
assert max(s for s, _ in steps) < finished[0][0], "schedule.finished does not follow the steps"

# schedule.started carries what makes the allocation auditable after the
# fit moves: the budget, the arm, the constants and the cost-table snapshot.
st = started[0][1]
assert st["schedule"] == "adaptive", st
assert st["budget"]["max_oracle_ms"] == 0, st
assert st["constants"]["executor_bp"], st
assert st["constants"]["redundancy_bp"], st
assert isinstance(st["cost_table"], list), st

# Every step considered something, and the considered set holds at most one
# purchase per world (decision 2s frontier).
for seq, step in steps:
    assert step["considered"], ("empty considered set at step", step["step"])
    seen = set()
    for row in step["considered"]:
        assert row["world"] not in seen, ("two frontier rows for one world", row["world"])
        seen.add(row["world"])

# EVERY receipt is preceded by a step that named it as chosen, and the
# chosen set and the receipt set agree exactly, per (world, kind).
chosen, chosen_seq = [], {}
for seq, step in steps:
    for c in step["chosen"]:
        key = (c["world"], kind_of.get(c["oracle"], c["oracle"]))
        chosen.append(key)
        chosen_seq.setdefault(key, seq)
bought = [(p["world"], p["oracle"]["id"]) for _, p in receipts]
assert sorted(chosen) == sorted(bought), ("chosen != bought", sorted(chosen), sorted(bought))
for seq, p in receipts:
    key = (p["world"], p["oracle"]["id"])
    assert chosen_seq[key] < seq, ("receipt recorded before it was chosen", key)

fin = finished[0][1]
assert fin["stop"] in ("S-ranking", "S-frontier", "S-budget", "S-empty"), fin
assert fin["violation"] == "", ("the purchase-law assertion fired", fin["violation"])
assert fin["bought"] == len(receipts), (fin["bought"], len(receipts))
print("m2b 5a: %d steps, %d receipts, stop %s" % (len(steps), len(receipts), fin["stop"]))
' || fail "5a: the allocation trace is missing, unbracketed, or does not name what was bought"
"$MVO" audit --dir "$REPO" | grep -q '^OK:' \
  || fail "5a: mvo audit is not OK with the schedule.* events present"

# --- 5b. the null case is null (decision 13). Same intent, same patches,
# unbounded budget, raced BOTH ways: the two arms buy the same rungs in the
# same worlds and reach the same decision with the same rationale.
#
# "Byte-identical" is asserted MODULO WORLD DIGEST and nothing else, and the
# reason is a fact about the artifact rather than a weakening: a world binds
# the race that created it, so two races of one intent mint different world
# digests for identical trees. The comparison therefore rewrites each
# digest to the patch that produced it — content-addressed, stable across
# races — and then demands equality byte for byte. ---
INTENT_NULL="$("$MVO" intent new --dir "$REPO" --title "m2b: null case")"
[ -n "$INTENT_NULL" ] || fail "mvo intent new (m2b null intent) printed no digest"
"$MVO" race "$INTENT_NULL" --dir "$REPO" --agent script --patches "$REPO/patches" --schedule=fixed \
  || fail "5b: the fixed-arm race failed"
NULL_FIXED="$("$MVO" explain "$INTENT_NULL" --dir "$REPO" --json)"
"$MVO" race "$INTENT_NULL" --dir "$REPO" --agent script --patches "$REPO/patches" --schedule=adaptive \
  || fail "5b: the adaptive-arm race failed"
NULL_ADAPT="$("$MVO" explain "$INTENT_NULL" --dir "$REPO" --json)"
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT type || '|' || payload_dig || '|' || cast(payload AS text) FROM events WHERE type='world.created' ORDER BY seq;" \
  | FIXED="$NULL_FIXED" ADAPT="$NULL_ADAPT" INTENT="$INTENT_NULL" python3 -c '
import json, os, sys

intent = os.environ["INTENT"]
worlds = {}   # world digest -> the patch CAS key that produced it
for line in sys.stdin:
    typ, dig, payload = line.rstrip("\n").split("|", 2)
    p = json.loads(payload)
    if p.get("intent") == intent:
        worlds[dig] = p["patch"]

def label(text):
    # Rewrite every world digest to its patch CONTENT HASH: the identity
    # that survives being raced twice. Everything else in the projection is
    # a gate result, a metric or a key value, none of which may move.
    for dig, patch in worlds.items():
        text = text.replace(dig, "world<%s>" % patch)
    return text

def projection(doc):
    # Everything that is a JUDGEMENT, and nothing that is a stopwatch. The
    # receipt digests and duration_ms of two runs of the same suite differ
    # by construction and say nothing about allocation; the gate results,
    # the key VALUES, the comparison walk and the rationale are what the two
    # arms must agree on, and the receipt SETS are compared separately below
    # against the ledger itself.
    return {
        "type": doc["type"],
        "rationale": doc["rationale"],
        "escalation": doc.get("escalation", {}),
        "candidates": [{
            "world": c["world"], "pass": c["pass"], "outcome": c["outcome"],
            "rank": c["rank"], "ordinal": c["ordinal"],
            "gates": [(g["label"], g["result"], g["detail"]) for g in c["gates"]],
            "keys": [(k["key"], k["known"], k["text"]) for k in c["keys"]
                     if k["key"] != "wall_ms_asc"],
        } for c in doc["candidates"]],
        "trace": doc.get("trace", []),
    }

fixed, adapt = json.loads(os.environ["FIXED"]), json.loads(os.environ["ADAPT"])
a = label(json.dumps(projection(fixed), sort_keys=True))
b = label(json.dumps(projection(adapt), sort_keys=True))
assert a == b, "the two arms do not agree:\nfixed:    %s\nadaptive: %s" % (a[:600], b[:600])
assert fixed["type"] == "SELECT", fixed["type"]
print("m2b 5b: the arms are indistinguishable under an unbounded budget (%s)" % fixed["type"])
' || fail "5b: the adaptive scheduler is not inert under an unbounded budget"

# The receipt SETS must match too, world by world and rung by rung — the
# explain report is a view, and this is the evidence itself.
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT json_object('seq', seq, 'type', type, 'dig', coalesce(payload_dig,''), 'payload', cast(payload AS text))
     FROM events WHERE type IN ('race.started','world.created','receipt.recorded') ORDER BY seq;" \
  | INTENT="$INTENT_NULL" python3 -c '
import json, os, sys

# One JSON object per row rather than a delimiter-joined line: a payload is
# arbitrary bytes and any separator we picked could appear inside one.
intent = os.environ["INTENT"]
rows = []
for line in sys.stdin:
    if not line.strip():
        continue
    r = json.loads(line)
    rows.append((int(r["seq"]), r["type"], r["dig"], json.loads(r["payload"])))

starts = [s for s, t, d, p in rows if t == "race.started" and p.get("intent") == intent]
assert len(starts) == 2, ("want two races of the null intent", len(starts))
patch_of = {d: p["patch"] for s, t, d, p in rows if t == "world.created" and p.get("intent") == intent}

def rungs(lo, hi):
    out = []
    for s, t, d, p in rows:
        if t != "receipt.recorded" or not (lo < s < hi):
            continue
        out.append((patch_of[p["world"]], p["oracle"]["id"], p["result"]["status"]))
    return sorted(out)

first = rungs(starts[0], starts[1])
second = rungs(starts[1], 10**9)
assert first and second, (len(first), len(second))
assert first == second, ("the arms bought different evidence", first, second)
print("m2b 5b: both arms bought the same %d receipts" % len(first))
' || fail "5b: the two arms bought different receipts under an unbounded budget"

# --- 5d. the trace is RECORDED, not recomputed. The cost table is fitted
# from the workspace's own receipts and it has moved since 5a (5b added six
# more). If `mvo explain --schedule` recomputed anything it would render
# differently now; it renders byte-identically, because every number came
# off the ledger. ---
SCHED_RENDER_A="$("$MVO" explain "$INTENT_SCHED" --dir "$REPO" --schedule)"
INTENT_MOVE="$("$MVO" intent new --dir "$REPO" --title "m2b: move the cost table")"
"$MVO" race "$INTENT_MOVE" --dir "$REPO" --agent script --patches "$REPO/patches" \
  || fail "5d: the cost-table-moving race failed"
SCHED_RENDER_B="$("$MVO" explain "$INTENT_SCHED" --dir "$REPO" --schedule)"
[ "$SCHED_RENDER_A" = "$SCHED_RENDER_B" ] \
  || fail "5d: explain --schedule re-renders after the cost table moved; the trace is being recomputed, not read
--- before ---
$SCHED_RENDER_A
--- after ---
$SCHED_RENDER_B"
# And an unmeasured kind never renders like a measured one, in the allocator
# exactly as in the menu: a declared-rank row prints NO millisecond figure.
"$MVO" explain "$INTENT_SCHED" --dir "$REPO" --json --schedule | python3 -c '
import json, sys
sched = json.load(sys.stdin)["schedule"]
assert sched["recorded"] is True, sched
for row in sched["cost_model"]:
    if row["measured"]:
        assert row["n"] >= 3, row
    else:
        assert row["basis"] == "declared-rank", row
        assert row["fixed_ms"] == 0 and row["per_unit_micro_ms"] == 0, row
print("m2b 5d: %d cost rows, none of them an unmeasured kind wearing a measured kinds clothes" % len(sched["cost_model"]))
' || fail "5d: the recorded cost model renders a millisecond figure for a kind nobody measured"

# --- 5e. the starved stop ESCALATES instead of rejecting, and the control
# shows the rule is what did it.
#
# M2a claimed "a scheduler that runs out of budget produces an ESCALATE".
# That was false against the shipped machineryFailure: a COMPLETED world
# that passed everything it bought and never bought the rest has no failing
# receipt and a first gate that did produce one, so the race recorded
# REJECT — "these candidates are bad" — about evidence nobody purchased.
# on_evidence_incomplete (M2b decision 14) is the fix, and it ships OFF. ---
cp "$ROOT/testdata/toyrepo/policies/schedule.json" "$REPO/.multiverso/policies/schedule.json"
"$MVO" policy validate "$REPO/.multiverso/policies/schedule.json" --dir "$REPO" | grep -q '^OK: policy valid$' \
  || fail "5e: the M2b schedule fixture policy does not validate"
# Captured, not piped: `policy show` prints the policy's canonical bytes
# after the human table, `grep -q` exits on its first match, and under
# `set -o pipefail` the writer's EPIPE would fail the script on a hit.
SCHED_SHOW="$("$MVO" policy show schedule --dir "$REPO")"
printf '%s' "$SCHED_SHOW" | grep -q 'on_evidence_incomplete' \
  || fail "5e: the fixture policy does not declare on_evidence_incomplete:
$SCHED_SHOW"
"$MVO" policy use schedule --dir "$REPO" >/dev/null || fail "5e: policy use schedule failed"

# The reference race, unbounded, which is also 5f's subject. Its receipts
# are what the starving budget is DERIVED from: a hard-coded millisecond
# figure is a guess about this machine, and the two ways it can be wrong are
# both silent — too small and no world buys its first rung (machinery
# failure, a different sentence entirely), too large and nothing starves.
INTENT_FULL="$("$MVO" intent new --dir "$REPO" --title "m2b: schedule ladder, unbounded")"
"$MVO" race "$INTENT_FULL" --dir "$REPO" --agent script --patches "$REPO/patches" \
  || fail "5e: the unbounded reference race failed"
CHEAP_MS="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT json_object('type', type, 'payload', cast(payload AS text))
     FROM events WHERE type IN ('race.started','receipt.recorded') ORDER BY seq;" \
  | INTENT="$INTENT_FULL" python3 -c '
import json, os, sys

# BOTH worlds guards plus ONE collect, plus a hair. Both bounds matter and
# both are derived rather than guessed.
#
#   - Every world must be able to buy its FIRST rung, or the race is
#     machinery-failed (`no receipt` on gate 1) and rule 1 fires instead of
#     rule 1a — a different sentence about a different situation.
#   - The bound must be small against the WHOLE ladder, because a purchase
#     the cost model cannot price is affordable while any budget remains: the
#     pool empties by ACTUAL spend, so a budget close to the ladders total
#     cost is a race between "exhausted" and "finished" and the winner is
#     whatever the machine felt like that second.
#
# Guards cost single-digit milliseconds and a collect costs hundreds, so this
# is roughly a quarter of the ladder and starves it four purchases in.
intent, live = os.environ["INTENT"], False
guards, collects = 0, [0]
for line in sys.stdin:
    if not line.strip():
        continue
    r = json.loads(line)
    p = json.loads(r["payload"])
    if r["type"] == "race.started":
        live = p.get("intent") == intent
        continue
    if not live:
        continue
    ms = int(p.get("cost", {}).get("wall_ms", 0))
    if p["oracle"]["id"] == "tree-guard":
        guards += ms
    elif p["oracle"]["id"] == "pytest-collect":
        collects.append(ms)
print(guards + max(collects) + 20)
')"
[ "$CHEAP_MS" -gt 50 ] 2>/dev/null \
  || fail "5e: could not derive a starving budget from the reference race (got '$CHEAP_MS')"

INTENT_STARVE="$("$MVO" intent new --dir "$REPO" --title "m2b: starved" --budget-oracle-ms "$CHEAP_MS")"
"$MVO" race "$INTENT_STARVE" --dir "$REPO" --agent script --patches "$REPO/patches" \
  || fail "5e: the starved race failed"
STARVED="$("$MVO" explain "$INTENT_STARVE" --dir "$REPO" --schedule)"
echo "$STARVED" | grep -q '^type: *ESCALATE$' \
  || fail "5e: a starved race did not ESCALATE:
$STARVED"
echo "$STARVED" | grep -q '^escalation: on_evidence_incomplete$' \
  || fail "5e: the starved race escalated for the wrong reason:
$STARVED"
echo "$STARVED" | grep -q 'hard gate(s) were never purchased for the leading world (first unpurchased: ' \
  || fail "5e: the escalation does not name the gate nobody bought:
$STARVED"
echo "$STARVED" | grep -q 'stopped: S-budget' \
  || fail "5e: the starved race did not record the STARVED stop clause:
$STARVED"
# THE CONTROL. The same rungs, the same derived budget, under a policy that
# does NOT declare the rule: the M2a-era behaviour, REJECT, unchanged.
# Without this the rule's effect would be assumed rather than shown.
cp "$ROOT/testdata/toyrepo/policies/differential.json" "$REPO/.multiverso/policies/differential.json"
"$MVO" policy use differential --dir "$REPO" >/dev/null || fail "5e: policy use differential failed"
INTENT_STARVE2="$("$MVO" intent new --dir "$REPO" --title "m2b: starved control" --budget-oracle-ms "$CHEAP_MS")"
"$MVO" race "$INTENT_STARVE2" --dir "$REPO" --agent script --patches "$REPO/patches" \
  || fail "5e: the control race failed"
STARVED2="$("$MVO" explain "$INTENT_STARVE2" --dir "$REPO" --schedule)"
echo "$STARVED2" | grep -q '^type: *REJECT$' \
  || fail "5e: the control race (no on_evidence_incomplete) did not REJECT, so the rule's effect is not attributable:
$STARVED2"
echo "$STARVED2" | grep -q 'stopped: S-budget' \
  || fail "5e: the control race did not starve, so it is not a control:
$STARVED2"

# --- 5f. evidence waste is computed, and research purchases are excluded
# from it BY CONSTRUCTION (decision 11/18). --collect-inert buys the rungs
# nothing reads — the mutation and property metrics M2a ships unranked,
# which M2d must correlate against ground truth before anyone ranks by them
# — and a purchase whose stated purpose is to influence no decision is 100%
# waste under PRD §11's definition, so counting it would make the metric
# meaningless. ---
"$MVO" policy use schedule --dir "$REPO" >/dev/null || fail "5f: policy use schedule failed"
"$MVO" explain "$INTENT_FULL" --dir "$REPO" --json --schedule | python3 -c '
import json, sys
sched = json.load(sys.stdin)["schedule"]
waste = sched["waste"]
assert waste["available"] is True, waste
assert waste["spent_ms"] > 0, waste
# The two numbers of decision 18, and the invariant between them: greedy
# substitution can only find MORE waste than single-receipt substitution,
# because it is the same test applied to a set rather than to one row.
assert 0 <= waste["waste_ms"] <= waste["spent_ms"], waste
assert waste["greedy_ms"] >= waste["waste_ms"], waste
assert waste["waste_bp"] == (waste["waste_ms"] * 10000) // waste["spent_ms"], waste
# A receipt is influential or it is waste. Never both, never neither.
assert len(waste["rows"]) >= len(waste["wasted"]), waste
for row in waste["wasted"]:
    assert row["reason"], row
print("m2b 5f: waste %d of %d ms, greedy %d ms" % (waste["waste_ms"], waste["spent_ms"], waste["greedy_ms"]))
' || fail "5f: evidence waste is not computable from the recorded trace"
# The research mode, on the policy that has an inert rung to buy.
cp "$ROOT/testdata/toyrepo/policies/menu.json" "$REPO/.multiverso/policies/menu.json"
INERT_SKIP=""
[ -z "$HAS_HYP" ] && INERT_SKIP="hypothesis"
[ -z "$MUT_TOOL" ] && INERT_SKIP="${INERT_SKIP:+$INERT_SKIP and }a mutation toolchain"
if [ -n "$INERT_SKIP" ]; then
  echo "SKIP 5f (research half): $INERT_SKIP is not importable here, so the inert rungs M2a ships unranked cannot be bought"
else
  cp "$ROOT/testdata/toyrepo/policies/inert.json" "$REPO/.multiverso/policies/inert.json"
  "$MVO" policy validate "$REPO/.multiverso/policies/inert.json" --dir "$REPO" | grep -q '^OK: policy valid$' \
    || fail "5f: the inert-rung fixture policy does not validate"
  "$MVO" policy use inert --dir "$REPO" >/dev/null || fail "5f: policy use inert failed"
  INTENT_INERT="$("$MVO" intent new --dir "$REPO" --title "m2b: collect-inert")"
  "$MVO" race "$INTENT_INERT" --dir "$REPO" --agent script --patches "$REPO/patches" --collect-inert \
    || fail "5f: the --collect-inert race failed"
  "$MVO" explain "$INTENT_INERT" --dir "$REPO" --json --schedule | python3 -c '
import json, sys
sched = json.load(sys.stdin)["schedule"]
assert sched["collect_inert"] is True, sched["mode"]
research = [r for r in sched["steps"] if r["basis"] == "research"]
assert research, "--collect-inert considered no research rung; the mode is inert itself"
for r in research:
    # A research row is decision-inert BY DEFINITION — that is what makes it
    # research — and it may still be declined for one batch before its turn
    # comes, exactly like any other frontier row.
    assert r["value_bp"] == 0, r
    assert r["declined"] in ("", "not this batch"), r
bought = [r for r in research if r["bought"]]
assert bought, "--collect-inert considered the inert rungs and bought none of them"
waste = sched["waste"]
assert waste["available"] is True, waste
# Decision 11, byte for byte: the research spend is reported SEPARATELY and
# is excluded from the waste total.
assert waste["research_ms"] > 0, waste
# spent_ms is the DECISION spend: the research purchases are not in it, so
# the exclusion is structural rather than a subtraction somebody could
# forget. waste is a fraction of what was spent to decide, and nothing else.
assert waste["waste_ms"] <= waste["spent_ms"], waste
decision_ms = sum(r["cost_ms"] for r in waste["rows"] if r["basis"] != "research")
research_ms = sum(r["cost_ms"] for r in waste["rows"] if r["basis"] == "research")
assert decision_ms == waste["spent_ms"], (decision_ms, waste["spent_ms"])
assert research_ms == waste["research_ms"], (research_ms, waste["research_ms"])
for row in waste["rows"]:
    if row["basis"] == "research":
        assert "excluded from the waste metric" in row["reason"], row
for row in waste["wasted"]:
    assert row["basis"] != "research", row
print("m2b 5f: research spend %d ms, excluded from waste %d ms" % (waste["research_ms"], waste["waste_ms"]))
' || fail "5f: --collect-inert did not buy a research rung, or the waste metric counted one"
fi
"$MVO" policy use default --dir "$REPO" >/dev/null || fail "5f: policy use default (restore) failed"

# --- 5g. AG-7 under a LIVE race, which is where a leak would actually
# arrive. No schedule.* payload key, budget figure, score, coefficient,
# stop clause, declared oracle NAME or sibling world digest appears in any
# world's captured agent context or transcript. ---
INTENT_AG7="$("$MVO" intent new --dir "$REPO" --title "m2b: ag-7 under a live race")"
PATH="$ROOT/testdata/fakeagent:$PATH" FAKE_AGENT_MODE=happy \
  "$MVO" race "$INTENT_AG7" --dir "$REPO" --agent claude-code --candidates 2 \
    --model fake-model --max-usd 0.25 --max-turns 8 --max-wall-ms 60000 \
    --agent-env FAKE_AGENT_MODE \
  || fail "5g: the AG-7 race failed"
sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT payload_dig || '|' || cast(payload AS text) FROM events WHERE type='world.created';" \
  | INTENT="$INTENT_AG7" CASDIR="$REPO/.multiverso/cas" python3 -c '
import json, os, sys

intent, casdir = os.environ["INTENT"], os.environ["CASDIR"]
mine = []
for line in sys.stdin:
    dig, payload = line.rstrip("\n").split("|", 1)
    p = json.loads(payload)
    if p.get("intent") == intent:
        mine.append((dig, p))
assert len(mine) == 2, ("want 2 AG-7 worlds", len(mine))

def blob(key):
    hexs = key.split(":", 1)[1]
    with open(os.path.join(casdir, "sha256", hexs[:2], hexs[2:]), "rb") as fh:
        return fh.read().decode("utf-8", "replace")

# The forbidden set: every schedule.* payload key, the scheduler vocabulary,
# the compiled constants, and every DECLARED ORACLE NAME. A prompt that
# named the ladder would let a generator condition on the protocol.
forbidden = [
    "schedule.started", "schedule.step", "schedule.finished", "oracle.skipped",
    "score_bpps", "value_bp", "discount_bp", "executor_bp", "cost_basis",
    "flip_outcomes", "max_oracle_ms", "declared-rank", "theil-sen",
    "S-frontier", "S-budget", "S-ranking", "S-empty",
    "collect-inert", "paths-unmodified", "collect-nonempty", "status-pass",
    "tree-guard", "pytest-collect", "pytest-suite", "wall_ms_asc",
]
digests = [dig for dig, _ in mine]
for dig, p in mine:
    for field in ("context", "trace"):
        text = blob(p[field])
        for token in forbidden:
            assert token not in text, (dig, field, token)
        # And no SIBLING world digest: a generator must not learn that it
        # has siblings, let alone which.
        for other in digests:
            if other != dig:
                assert other not in text, (dig, field, "sibling digest")
print("m2b 5g: 2 worlds, %d forbidden tokens, none present in context or transcript" % len(forbidden))
' || fail "5g: scheduler state leaked into an agent context or transcript (AG-7)"

# --- 5h. VALIDATION RULE 25 (decision 15), and BOTH halves of its
# definition. A key is allocation-sensitive iff its value can change when a
# receipt is withheld from a world THAT STILL PASSES EVERY HARD GATE — so
# `wall_ms_asc` over a policy whose every counted selector is hard-gated is
# NOT refusable (withholding drops the world out of the pass set, where no
# key compares it), and `wall_ms_asc` beside evidence no gate backs is. The
# refusal names the key, names both outs, and leaves the ledger untouched. ---
cp "$ROOT/testdata/toyrepo/policies/schedule-wall.json" "$REPO/.multiverso/policies/schedule-wall.json"
"$MVO" policy validate "$REPO/.multiverso/policies/schedule-wall.json" --dir "$REPO" | grep -q '^OK: policy valid$' \
  || fail "5h: the rule-25 fixture policy does not validate (it must be a LEGAL policy that only the adaptive arm refuses)"
INTENT_R25="$("$MVO" intent new --dir "$REPO" --title "m2b: rule 25" --policy schedule-wall)"
[ -n "$INTENT_R25" ] || fail "5h: mvo intent new --policy schedule-wall printed no digest"
EVENTS_BEFORE="$(sqlite3 "$REPO/.multiverso/ledger.db" "SELECT count(*) FROM events;")"
set +e
"$MVO" race "$INTENT_R25" --dir "$REPO" --agent script --patches "$REPO/patches" >"$WORK/r25.out" 2>&1
R25_CODE=$?
set -e
[ "$R25_CODE" = "2" ] \
  || fail "5h: an allocation-sensitive policy raced adaptively exited $R25_CODE, want 2 (usage):
$(cat "$WORK/r25.out")"
grep -q 'wall_ms_asc' "$WORK/r25.out" || fail "5h: the rule-25 refusal does not name the key:
$(cat "$WORK/r25.out")"
grep -q 'validation rule 25' "$WORK/r25.out" || fail "5h: the refusal does not cite the rule:
$(cat "$WORK/r25.out")"
grep -q -- '--schedule=fixed' "$WORK/r25.out" || fail "5h: the refusal does not offer the exhaustive ladder as an out:
$(cat "$WORK/r25.out")"
grep -q "remove wall_ms_asc" "$WORK/r25.out" || fail "5h: the refusal does not offer removing the key as an out:
$(cat "$WORK/r25.out")"
EVENTS_AFTER="$(sqlite3 "$REPO/.multiverso/ledger.db" "SELECT count(*) FROM events;")"
[ "$EVENTS_BEFORE" = "$EVENTS_AFTER" ] \
  || fail "5h: a pre-flight refusal wrote $((EVENTS_AFTER - EVENTS_BEFORE)) ledger event(s); the ledger must be untouched"
# The second out works, and it is the arm M1 always had.
"$MVO" race "$INTENT_R25" --dir "$REPO" --agent script --patches "$REPO/patches" --schedule=fixed \
  || fail "5h: the documented out (--schedule=fixed) does not race the policy rule 25 refused"
R25_EXPLAIN="$("$MVO" explain "$INTENT_R25" --dir "$REPO" --schedule)"
printf '%s' "$R25_EXPLAIN" | grep -q 'no allocation trace recorded' \
  || fail "5h: a fixed-ladder race renders an allocation trace it never produced:
$R25_EXPLAIN"
"$MVO" policy use default --dir "$REPO" >/dev/null || fail "5h: policy use default (restore) failed"

# --- 5c. replay is exact, in both directions. An M2a-era ledger — bytes
# written before the scheduler existed — replays byte-for-byte under this
# binary, and a new M2b race replays with the schedule.* events PRESENT and
# IGNORED. The second half is the one the design rests on: observational
# events reach Decide never, so the scheduler can be rewritten, retuned or
# replaced without invalidating a single recorded decision. ---
"$MVO" audit --dir "$REPO" --require-decisions 1 --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["chain_ok"] is True, r
assert r["replay_identical"] is True, r
assert r["mismatches"] == [], r["mismatches"]
assert r["cas_missing"] == [] and r["cas_corrupt"] == [], r
assert r["decisions"] >= 8, r
print("m2b 5c: %d events, %d decisions replayed with the trace present and ignored" % (r["events"], r["decisions"]))
' || fail "5c: a workspace carrying allocation traces does not replay identically"
M2A_COMMIT="$(git -C "$ROOT" rev-list -1 --grep '^M2a:' HEAD 2>/dev/null || true)"
if [ -z "$M2A_COMMIT" ]; then
  echo "SKIP 5c (pre-M2b half): no M2a commit reachable from HEAD in $ROOT; the byte-for-byte replay of PRE-SCHEDULER ledger bytes cannot be built here"
else
  M2ATREE="$WORK/m2a-src"
  git -C "$ROOT" worktree add -q --detach "$M2ATREE" "$M2A_COMMIT" \
    || fail "5c: could not check out the M2a commit $M2A_COMMIT"
  M2AMVO="$WORK/mvo-m2a"
  (cd "$M2ATREE" && go build -o "$M2AMVO" ./cmd/mvo) \
    || fail "5c: the M2a binary does not build from $M2A_COMMIT"
  M2AREPO="$WORK/m2a-repo"
  mkdir -p "$M2AREPO"
  cp -R "$M2ATREE/testdata/toyrepo/." "$M2AREPO/"
  rm -rf "$M2AREPO/__pycache__" "$M2AREPO/.pytest_cache"
  $GIT -C "$M2AREPO" init -q -b main
  $GIT -C "$M2AREPO" add -A
  $GIT -C "$M2AREPO" commit -qm "m2a-era baseline"
  "$M2AMVO" init --dir "$M2AREPO" >/dev/null || fail "5c: the M2a binary could not init a workspace"
  M2A_INTENT="$("$M2AMVO" intent new --dir "$M2AREPO" --title "m2a-era race")"
  "$M2AMVO" race "$M2A_INTENT" --dir "$M2AREPO" --agent script --patches "$M2AREPO/patches" >/dev/null \
    || fail "5c: the M2a binary could not race its own fixture"
  M2A_RATIONALE="$("$M2AMVO" explain "$M2A_INTENT" --dir "$M2AREPO" | grep '^rationale:')"
  "$MVO" audit --dir "$M2AREPO" --require-decisions 1 --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["chain_ok"] is True, r
assert r["replay_identical"] is True, r
' || fail "5c: an M2a-era ledger does not replay identically under the M2b binary"
  M2B_RATIONALE="$("$MVO" explain "$M2A_INTENT" --dir "$M2AREPO" | grep '^rationale:')"
  [ "$M2A_RATIONALE" = "$M2B_RATIONALE" ] \
    || fail "5c: the M2b binary renders an M2a-era decision differently:
M2a: $M2A_RATIONALE
M2b: $M2B_RATIONALE"
  # A race that recorded no trace says so, rather than rendering an empty
  # table that reads like "the scheduler considered nothing".
  # Captured, not piped: `grep -q` exits on its first match and the sentence
  # is printed mid-report, so under `set -o pipefail` the writer's EPIPE
  # would turn a HIT into a failure.
  M2A_SCHED="$("$MVO" explain "$M2A_INTENT" --dir "$M2AREPO" --schedule)"
  printf '%s' "$M2A_SCHED" | grep -q 'no allocation trace recorded for this race' \
    || fail "5c: a pre-M2b race does not say that it has no allocation trace:
$M2A_SCHED"
  git -C "$ROOT" worktree remove --force "$M2ATREE" >/dev/null 2>&1 || true
fi

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
# laundering race, the deep-forgery race, the protected-paths race, the
# ESCALATE tie and the legacy-v0 race — AND the admission, THROUGH TWO
# POLICY SCHEMA VERSIONS IN ONE LEDGER; the chain also carries
# publish/prune events (observational) ---
COPY="$WORK/toyrepo-copy"
cp -R "$REPO" "$COPY"
MIN_DECISIONS=10
[ "$T1_RAN" = "1" ] && MIN_DECISIONS=11
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

# --- 9a. CAS tamper, the two shapes the study found audit reporting OK
# over: a deleted attestation blob and an edited stored artifact. Both are
# now exit-1 failures naming the digest and the record that referenced it. ---
ATT_DIG="$($GIT -C "$REPO" log -1 --format=%B \
  | sed -n 's/^Multiverso-Attestation: sha256:\([0-9a-f]\{64\}\)$/\1/p' | tail -1)"
[ -n "$ATT_DIG" ] || fail "could not extract the attestation trailer digest"
ATT_FILE="$REPO/.multiverso/cas/sha256/${ATT_DIG:0:2}/${ATT_DIG:2}"
[ -f "$ATT_FILE" ] || fail "attestation bundle $ATT_DIG not in CAS"
mv "$ATT_FILE" "$WORK/bundle.bak"
if SWEEP_OUT="$("$MVO" audit --dir "$REPO" 2>&1)"; then
  fail "audit reported OK after the attestation bundle was deleted from CAS:
$SWEEP_OUT"
fi
echo "$SWEEP_OUT" | grep -q "^MISSING: sha256:$ATT_DIG referenced by attestation.recorded seq .* (bundle)$" \
  || fail "audit did not report the missing bundle with its referrer:
$SWEEP_OUT"
mv "$WORK/bundle.bak" "$ATT_FILE"
"$MVO" audit --dir "$REPO" >/dev/null || fail "audit did not recover after the bundle was restored"

# (b) flip one byte inside a stored evidence-stream artifact.
# Drains stdin before printing: breaking out of the loop early closes the
# pipe under sqlite3's feet, and `pipefail` turns that SIGPIPE into a 141
# that aborts the whole script. It only ever passed because the receipts
# fitted in the pipe buffer — adding one race is enough to break it.
STREAM_KEY="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT cast(payload AS text) FROM events WHERE type='receipt.recorded';" \
  | python3 -c '
import json, sys
keys = []
for line in sys.stdin:
    if not line.strip():
        continue
    r = json.loads(line)
    if r["oracle"]["id"] == "pytest-suite" and len(r["result"]["artifacts"]) >= 4:
        keys.append(r["result"]["artifacts"][3])
print(keys[0] if keys else "")
')"
[ -n "$STREAM_KEY" ] || fail "no evidence-stream artifact found in any suite receipt"
STREAM_HEX="${STREAM_KEY#sha256:}"
STREAM_FILE="$REPO/.multiverso/cas/sha256/${STREAM_HEX:0:2}/${STREAM_HEX:2}"
cp "$STREAM_FILE" "$WORK/stream.bak"
python3 -c '
import sys
b = bytearray(open(sys.argv[1], "rb").read())
b[0] ^= 0xFF
open(sys.argv[1], "wb").write(b)
' "$STREAM_FILE"
if SWEEP_OUT="$("$MVO" audit --dir "$REPO" 2>&1)"; then
  fail "audit reported OK over an edited CAS artifact:
$SWEEP_OUT"
fi
echo "$SWEEP_OUT" | grep -q "^CORRUPT: $STREAM_KEY .*referenced by receipt.recorded" \
  || fail "audit did not report the corrupt artifact with both digests and its referrer:
$SWEEP_OUT"
cp "$WORK/stream.bak" "$STREAM_FILE"

# --- 10. the original repo still audits clean end-to-end, with the CI knob
# that makes "nothing verified" impossible to mistake for a pass ---
AUDIT_FINAL="$("$MVO" audit --dir "$REPO" --require-decisions 1)" \
  || fail "audit on the original repo failed:
$AUDIT_FINAL"
echo "$AUDIT_FINAL" | grep -q '^OK:' || fail "audit on the original repo did not print the OK line"
echo "$AUDIT_FINAL" | grep -q 'CAS objects verified' \
  || fail "audit's OK line does not report the CAS sweep:
$AUDIT_FINAL"
"$MVO" audit --dir "$REPO" --json | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r["schema"] == "multiverso.dev/audit-report/v1", r["schema"]
assert r["cas_checked"] > 0, r
assert r["cas_missing"] == [], r["cas_missing"]
assert r["cas_corrupt"] == [], r["cas_corrupt"]
assert r["attestations_verified"] >= 1, r
' || fail "audit --json does not report a clean v1 sweep"
# A skipped check must never render identically to a passed one.
"$MVO" audit --dir "$REPO" --cas-sweep=false | grep -q '(CAS sweep skipped)' \
  || fail "audit --cas-sweep=false does not say the sweep was skipped"

echo "accept: OK"
