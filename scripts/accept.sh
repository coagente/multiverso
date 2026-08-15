#!/usr/bin/env bash
# M1d acceptance script — M1c's steps kept intact (admission, signed
# attestation, offline verification, tamper detection; step 2 races the
# script adapter, step 3b the fake claude-code fixture, step 3c --parallel
# 2, step 3d the docker-gated T1 step) with the M1d publication steps:
# step 3 additionally asserts the FRESH drift line, and steps 6b-6g cover
# publish → clone → fetch-race → tamper-detection → prune against a LOCAL
# bare remote (see docs/design/M1d-publication.md "Acceptance script").
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
# patches — the refactor-proof: the entire remaining pipeline must pass
# unchanged on the adapter-refactored engine ---
"$MVO" init --dir "$REPO"
INTENT="$("$MVO" intent new --dir "$REPO" --title "fix mean()" --desc "mean divides by len-1")"
[ -n "$INTENT" ] || fail "mvo intent new printed no digest"
"$MVO" race "$INTENT" --dir "$REPO" --agent script --patches "$REPO/patches" --oracle-cmd "python3 -m pytest -q"

# --- 3. explain shows SELECT with patch-a's world as winner ---
PATCH_A_KEY="sha256:$(python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$REPO/patches/patch-a.patch")"
WORLD_A="$(sqlite3 "$REPO/.multiverso/ledger.db" \
  "SELECT payload_dig FROM events WHERE type='world.created' AND cast(payload AS text) LIKE '%$PATCH_A_KEY%';")"
[ -n "$WORLD_A" ] || fail "no world.created event references patch-a ($PATCH_A_KEY)"
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
    --agent-env FAKE_AGENT_MODE --oracle-cmd "python3 -m pytest -q"

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
  --parallel 2 --oracle-cmd "python3 -m pytest -q"
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
      --exec T1 --exec-image "$T1_IMAGE" --memory-mb 512 --cpus 1 --pids 256 \
      --oracle-cmd "python3 -m pytest -q"
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
echo "$FR_OUT" | grep -q 'OK: race verified' || fail "fetch-race did not verify:
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
# the parallel race, and (when it ran) the T1 race — AND the admission;
# the chain now also carries publish/prune events (observational) ---
COPY="$WORK/toyrepo-copy"
cp -R "$REPO" "$COPY"
MIN_DECISIONS=4
[ "$T1_RAN" = "1" ] && MIN_DECISIONS=5
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
