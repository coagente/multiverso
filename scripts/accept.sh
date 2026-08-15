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
echo "$VALIDATE_OUT" | grep -q 'known: collect-nonempty, collected-not-below, coverage-at-least, no-failed-tests, paths-unmodified, skips-not-above, status-pass' \
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
GATE_VOCAB='collect-nonempty, collected-not-below, coverage-at-least, no-failed-tests, paths-unmodified, skips-not-above, status-pass'
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
