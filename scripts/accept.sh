#!/usr/bin/env bash
# M1b acceptance script — M1a's ten steps (admission, signed attestation,
# offline verification, tamper detection) with the race running through
# agent adapters: step 2 races the script adapter explicitly (the
# refactor-proof), step 3b races the fake claude-code fixture and asserts
# captured patch, transcript, and honest cost (see
# docs/design/M1b-agent-adapters.md "Acceptance script"). Exits non-zero
# on the first broken assertion. No real agent CLI is ever invoked.
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

# --- 7. second machine: audit replays both races (the fake race's SELECT
# included) AND the admission ---
COPY="$WORK/toyrepo-copy"
cp -R "$REPO" "$COPY"
"$MVO" audit --json --dir "$COPY" | python3 -c '
import json, sys
r = json.load(sys.stdin)
assert r.get("chain_ok") is True, r
assert r.get("replay_identical") is True, r
assert r.get("admissions", 0) >= 1, r
assert r.get("decisions", 0) >= 3, r
' || fail "audit --json on the copy did not replay all decisions identically"

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
