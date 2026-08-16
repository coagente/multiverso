"""mvo_corpus — the control plane's corpus runner inside a world.

This is the cheapest code-executing rung in the system and also the one with
the smallest attack surface, which is an unusual combination and a
deliberate one (M2a decision 12).

IT DOES NOT GO THROUGH PYTEST, and that is a security decision before it is
a cost decision. There is no conftest.py collection, no pytest.ini/
pyproject.toml `addopts`, no `-p` plugin argument and no pytest11
entry-point autoloading — the entire surface M1f decisions 16, 17 and 24
were fighting is simply not present here. The candidate still gets arbitrary
execution at import time; that is unremovable and the threat model says so.
What it does not get is an observer registered inside a plugin system it
configures.

Two modes:

  --materialize --plan FILE   resolve every proposed target and decode every
                              proposed argument ON THE BASE TREE, print the
                              surviving cases and the drop counts as JSON on
                              stdout. Nothing is observed here; this fixes
                              the INPUTS before any candidate exists.

  --replay --corpus FILE      execute the pinned corpus in this world and
                              write one `case` record per case onto the M1f
                              evidence stream, live, while the process runs.

Wire format: the M1f evidence stream, unchanged — same header, same nonce,
same strictly increasing seq, same parse rules. `case` is the one additive
record kind (M2a).

mvo-value/v0, the encoding, in three rules that each carry the honesty:

  * FLOATS ARE BIT PATTERNS. struct.pack('>d', x) in hex. sum(xs)/n and
    math.fsum(xs)/n differing in the last bit IS a behavioural difference,
    and -0.0, NaN and the two infinities are distinct observations.
  * EXCEPTION MESSAGES ARE EXCLUDED; only the type is fingerprinted.
    Messages embed paths, addresses and object ids, and a differential that
    reports 100 % divergence reports nothing.
  * A VALUE THE ENCODING CANNOT REPRESENT IS `opaque`, AND TWO OPAQUE
    OBSERVATIONS ARE NOT EQUAL. Absence of comparability is not agreement.
"""

import base64
import hashlib
import importlib
import json
import os
import struct
import sys
import time

SCHEMA = "mvo-evidence/v0"
RUNNER = "mvo_corpus/v0"

_ENV_STREAM = "MVO_EVIDENCE_STREAM"
_ENV_NONCE = "MVO_EVIDENCE_NONCE"

# Beyond this many bytes an encoded value is carried as its fingerprint
# alone. The value rides along for the human-facing report, and a report a
# candidate can make arbitrarily large is a denial of a maintainer's
# attention.
_VALUE_CAP = 512


class _Opaque(Exception):
    """Raised internally when a value falls outside mvo-value/v0."""

    def __init__(self, name):
        super().__init__(name)
        self.name = name


def _dumps(obj):
    # sort_keys + the tightest separators + ensure_ascii=False is exactly
    # the control plane's canonical JSON (DP-1), so a fingerprint computed
    # here and a fingerprint computed in Go are the same bytes.
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def encode(x):
    """Python value -> mvo-value/v0. Raises _Opaque for anything outside it."""
    if x is None or isinstance(x, bool):
        return x
    if isinstance(x, int):
        return x
    if isinstance(x, str):
        return x
    if isinstance(x, float):
        return {"f": struct.pack(">d", x).hex()}
    if isinstance(x, (bytes, bytearray)):
        return {"b": base64.b64encode(bytes(x)).decode("ascii").rstrip("=")}
    if isinstance(x, list):
        return [encode(e) for e in x]
    if isinstance(x, tuple):
        return {"t": [encode(e) for e in x]}
    if isinstance(x, dict):
        if all(isinstance(k, str) for k in x):
            return {k: encode(v) for k, v in x.items()}
        pairs = [[encode(k), encode(v)] for k, v in x.items()]
        pairs.sort(key=lambda kv: _dumps(kv[0]))
        return {"m": pairs}
    if isinstance(x, (set, frozenset)):
        members = [encode(e) for e in x]
        members.sort(key=_dumps)
        return {"s": members}
    raise _Opaque(type(x).__qualname__)


def decode(v):
    """mvo-value/v0 -> Python value. Raises _Opaque for anything it cannot build."""
    if v is None or isinstance(v, (bool, int, str)):
        return v
    if isinstance(v, float):
        # A bare JSON float never appears in a well-formed encoding: DP-1
        # forbids it and floats travel as bit patterns. Refusing it beats
        # silently accepting a lossy decimal.
        raise _Opaque("float")
    if isinstance(v, list):
        return [decode(e) for e in v]
    if isinstance(v, dict):
        if len(v) == 1:
            tag, body = next(iter(v.items()))
            if tag == "f" and isinstance(body, str):
                return struct.unpack(">d", bytes.fromhex(body))[0]
            if tag == "b" and isinstance(body, str):
                pad = "=" * (-len(body) % 4)
                return base64.b64decode(body + pad)
            if tag == "t" and isinstance(body, list):
                return tuple(decode(e) for e in body)
            if tag == "s" and isinstance(body, list):
                return set(decode(e) for e in body)
            if tag == "m" and isinstance(body, list):
                return {decode(k): decode(val) for k, val in body}
            if tag == "o":
                raise _Opaque(body if isinstance(body, str) else "?")
        return {k: decode(val) for k, val in v.items()}
    raise _Opaque(type(v).__qualname__)


def fingerprint_value(encoded):
    return _sha256({"kind": "value", "v": encoded})


def fingerprint_raise(qualname):
    return _sha256({"kind": "raise", "t": qualname})


def _sha256(obj):
    return "sha256:" + hashlib.sha256(_dumps(obj).encode("utf-8")).hexdigest()


def resolve(target):
    """module:qualname -> the callable, or None when it does not resolve here."""
    if ":" not in target:
        return None
    mod_name, qual = target.split(":", 1)
    try:
        obj = importlib.import_module(mod_name)
    except Exception:
        return None
    for part in qual.split("."):
        try:
            obj = getattr(obj, part)
        except Exception:
            return None
    return obj


class _Writer:
    """Append-only line writer over the control plane's channel.

    A write that fails is swallowed. The runner must never turn an
    evidence-channel problem into a candidate's failure — that would let one
    candidate manufacture a failure for a competitor by breaking the channel
    — and a broken channel already shows up as an incomplete stream, which
    the control plane reads as ABSENT metrics: a failed gate, not a pass.
    """

    def __init__(self, path, nonce):
        self._fd = None
        self._seq = 0
        try:
            self._fd = os.open(path, os.O_WRONLY | os.O_APPEND)
        except OSError:
            self._fd = None
            return
        self._raw("%s\t%s\n" % (SCHEMA, nonce))

    def ok(self):
        return self._fd is not None

    def _raw(self, text):
        if self._fd is None:
            return
        try:
            os.write(self._fd, text.encode("utf-8", "replace"))
        except OSError:
            self._fd = None

    def record(self, kind, payload):
        self._seq += 1
        try:
            body = _dumps(payload)
        except (TypeError, ValueError):
            body = "{}"
        # A newline inside a payload would forge a record boundary.
        body = body.replace("\n", " ").replace("\r", " ")
        self._raw("%d\t%s\t%s\n" % (self._seq, kind, body))

    def close(self):
        if self._fd is None:
            return
        try:
            os.close(self._fd)
        except OSError:
            pass
        self._fd = None


def _observe(case):
    """Run one case and return its wire record payload."""
    out = {"id": case.get("id", "")}
    target = case.get("target", "")
    fn = resolve(target)
    if fn is None:
        out["outcome"] = "error"
        out["t"] = "unresolved"
        return out
    try:
        args = [decode(a) for a in case.get("args") or []]
        kwargs = {k: decode(v) for k, v in (case.get("kwargs") or {}).items()}
    except _Opaque as exc:
        out["outcome"] = "error"
        out["t"] = exc.name
        return out
    try:
        value = fn(*args, **kwargs)
    except Exception as exc:  # noqa: BLE001 - the exception TYPE is the observation
        out["outcome"] = "raise"
        out["t"] = type(exc).__qualname__
        out["fp"] = fingerprint_raise(out["t"])
        return out
    try:
        enc = encode(value)
    except _Opaque as exc:
        # Absence of comparability is not agreement: an opaque observation
        # compares equal to NOTHING, including another opaque one.
        out["outcome"] = "opaque"
        out["t"] = exc.name
        return out
    out["outcome"] = "value"
    out["fp"] = fingerprint_value(enc)
    body = _dumps(enc)
    if len(body.encode("utf-8")) <= _VALUE_CAP:
        out["v"] = enc
    else:
        out["truncated"] = True
    return out


def _replay(corpus_path, root, expected_digest):
    if root:
        sys.path.insert(0, root)
    # The BYTES are read first and hashed before anything is decoded out of
    # them, because what this record has to answer is "which corpus did this
    # world actually replay" — not "which corpus did it believe it was
    # given". The control plane passes the pinned digest on argv and the
    # runner reports both, so a corpus that changed on disk between the
    # pinning and the replay is visible in the observation itself instead of
    # being inferred from a case id nobody declared.
    with open(corpus_path, "rb") as fh:
        raw = fh.read()
    loaded_digest = "mv0:" + hashlib.sha256(raw).hexdigest()
    corpus = json.loads(raw.decode("utf-8"))
    cases = corpus.get("cases") or []

    writer = _Writer(os.environ.get(_ENV_STREAM, ""), os.environ.get(_ENV_NONCE, ""))
    if not writer.ok():
        # No channel, no observation. Failing loudly here would be a lie
        # about the candidate; the control plane sees an empty stream and
        # records absent metrics, which fails the gate.
        return 3
    t0 = time.time()
    argv_digest = "sha256:" + hashlib.sha256(
        json.dumps(list(sys.argv), sort_keys=True).encode("utf-8")
    ).hexdigest()
    writer.record(
        "session_start",
        {
            "argv_digest": argv_digest,
            # `corpus` is the digest of the bytes THIS PROCESS LOADED, not a
            # field copied out of them. The corpus object has no `digest`
            # member — the digest is the content address of its canonical
            # bytes and is never inside them — so the field this record used
            # to carry was `""` in every stream this tree could produce, and
            # the one check that would have caught a swapped corpus was dead
            # on arrival.
            "corpus": loaded_digest,
            "corpus_expected": expected_digest,
            "pid": os.getpid(),
            "runner": RUNNER,
        },
    )
    opaque = 0
    errored = 0
    for case in cases:
        payload = _observe(case)
        if payload.get("outcome") == "opaque":
            opaque += 1
        elif payload.get("outcome") in ("error", "timeout"):
            errored += 1
        writer.record("case", payload)
    writer.record(
        "session_finish",
        {
            "cases": len(cases),
            "duration_ms": int(round((time.time() - t0) * 1000)),
            "errored": errored,
            "exitstatus": 0,
            "opaque": opaque,
        },
    )
    writer.close()
    return 0


def _materialize(plan_path, root):
    if root:
        sys.path.insert(0, root)
    with open(plan_path, "r", encoding="utf-8") as fh:
        plan = json.load(fh)
    kept = []
    dropped = {"not_representable": 0, "target_unresolved": 0}
    for case in plan.get("cases") or []:
        target = case.get("target", "")
        if resolve(target) is None:
            # A case whose target does not resolve ON THE BASE TREE is
            # dropped and counted, and never silently re-added by a
            # candidate that happens to define the missing name.
            dropped["target_unresolved"] += 1
            continue
        try:
            args = [encode(decode(a)) for a in case.get("args") or []]
            kwargs = {
                k: encode(decode(v)) for k, v in (case.get("kwargs") or {}).items()
            }
        except (_Opaque, ValueError, TypeError):
            dropped["not_representable"] += 1
            continue
        kept.append({"args": args, "kwargs": kwargs, "target": target})
    sys.stdout.write(_dumps({"cases": kept, "dropped": dropped}))
    sys.stdout.write("\n")
    return 0


def main(argv):
    mode = ""
    corpus = ""
    corpus_digest = ""
    plan = ""
    root = ""
    i = 1
    while i < len(argv):
        a = argv[i]
        if a in ("--replay", "--materialize"):
            mode = a[2:]
        elif a == "--corpus" and i + 1 < len(argv):
            i += 1
            corpus = argv[i]
        elif a == "--corpus-digest" and i + 1 < len(argv):
            i += 1
            corpus_digest = argv[i]
        elif a == "--plan" and i + 1 < len(argv):
            i += 1
            plan = argv[i]
        elif a == "--root" and i + 1 < len(argv):
            i += 1
            root = argv[i]
        else:
            sys.stderr.write("mvo_corpus: unknown argument %r\n" % (a,))
            return 2
        i += 1
    if mode == "replay" and corpus:
        return _replay(corpus, root, corpus_digest)
    if mode == "materialize" and plan:
        return _materialize(plan, root)
    sys.stderr.write(
        "mvo_corpus: usage: --replay --corpus FILE [--corpus-digest MV0] | --materialize --plan FILE\n"
    )
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
