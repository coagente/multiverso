"""mvo_evidence — the control plane's observer inside the test process.

M1f moves the trust boundary to the process boundary. This plugin is owned
by the control plane, embedded in the mvo binary, materialized read-only
outside the candidate's tree, and loaded by argv (`-p mvo_evidence`) before
any candidate code runs. It writes a framed, append-only event stream to a
FIFO the control plane created and reads LIVE, while the process runs.

Nothing here defends against an in-process adversary who patches this
module before the first failing test reports; the threat model says so in
as many words. What it does buy is the two properties an adversary cannot
take back:

  * observations already received are immutable — the atexit-class attacks
    the study reproduced are attacks on state read AFTER the fact, and
    there is no read after the fact any more;
  * absence never passes — a run whose stream is missing, malformed or
    unterminated yields ABSENT metrics, and a 0-exit with no usable stream
    is `status = error`, never `pass`.

Wire format: multiverso.dev/evidence-stream/v0, tab-separated, one record
per line, JSON payload last (so a payload may contain anything but a
newline).
"""

import hashlib
import json
import os
import sys
import time

SCHEMA = "mvo-evidence/v0"

_ENV_STREAM = "MVO_EVIDENCE_STREAM"
_ENV_NONCE = "MVO_EVIDENCE_NONCE"


class _Writer:
    """Append-only line writer over the control plane's FIFO.

    A write that fails is swallowed: the plugin must never turn an
    evidence-channel problem into a test failure, because that would let a
    candidate manufacture a failing suite for a competitor by breaking the
    channel. A broken channel shows up as an incomplete stream, which the
    control plane reads as absent metrics — a failed gate, not a pass.
    """

    def __init__(self, path, nonce):
        self._fd = None
        self._seq = 0
        self._nonce = nonce
        try:
            # The control plane holds the read end open (O_RDONLY |
            # O_NONBLOCK) before spawning us, so this never blocks.
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
            body = json.dumps(payload, sort_keys=True, separators=(",", ":"))
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


class MvoEvidence:
    """The plugin object. One instance per session, registered by argv."""

    def __init__(self, writer):
        self._w = writer
        self._t0 = time.time()
        self._runs = {}  # nodeid -> how many runs reported so far
        self._outcomes = {}  # nodeid -> latest outcome (for the tally)

    # --- session ------------------------------------------------------
    def pytest_sessionstart(self, session):
        try:
            version = session.config.pluginmanager.get_plugin("pytest").__version__
        except Exception:  # pragma: no cover - pytest always has a version
            version = ""
        argv = list(sys.argv)
        digest = hashlib.sha256(
            json.dumps(argv, sort_keys=True).encode("utf-8")
        ).hexdigest()
        rootdir = ""
        try:
            rootdir = str(session.config.rootpath)
        except Exception:
            pass
        self._w.record(
            "session_start",
            {
                "argv_digest": "sha256:" + digest,
                "pid": os.getpid(),
                "pytest": version,
                "rootdir": rootdir,
            },
        )

    def pytest_collection_finish(self, session):
        self._w.record("collected", {"count": len(session.items)})

    # --- per test -----------------------------------------------------
    def pytest_runtest_logreport(self, report):
        outcome = _classify(report)
        if outcome is None:
            return
        nodeid = report.nodeid
        run = self._runs.get(nodeid, 0) + 1
        self._runs[nodeid] = run
        self._outcomes[nodeid] = outcome
        self._w.record(
            "test",
            {
                "duration_ms": int(round(getattr(report, "duration", 0.0) * 1000)),
                "nodeid": nodeid,
                "outcome": outcome,
                "run": run,
            },
        )

    # --- finish -------------------------------------------------------
    def pytest_sessionfinish(self, session, exitstatus):
        tally = {"passed": 0, "failed": 0, "errored": 0, "skipped": 0}
        for outcome in self._outcomes.values():
            tally[_BUCKET[outcome]] += 1
        try:
            status = int(exitstatus)
        except (TypeError, ValueError):
            status = -1
        self._w.record(
            "session_finish",
            {
                "duration_ms": int(round((time.time() - self._t0) * 1000)),
                "errored": tally["errored"],
                "exitstatus": status,
                "failed": tally["failed"],
                "passed": tally["passed"],
                "skipped": tally["skipped"],
                "total": len(self._outcomes),
            },
        )
        self._w.close()


# The JUnit equivalence classes, so the cross-check compares like with
# like: xfailed counts as skipped, xpassed as passed (unless strict, where
# pytest itself reports the report as failed).
_BUCKET = {
    "passed": "passed",
    "failed": "failed",
    "error": "errored",
    "skipped": "skipped",
    "xfailed": "skipped",
    "xpassed": "passed",
}


def _classify(report):
    """One report -> one outcome, or None when it carries no verdict."""
    when = getattr(report, "when", None)
    wasxfail = hasattr(report, "wasxfail")
    if when == "call":
        if report.passed:
            return "xpassed" if wasxfail else "passed"
        if report.failed:
            return "failed"
        if report.skipped:
            return "xfailed" if wasxfail else "skipped"
        return None
    # setup/teardown: a failure there is an ERROR, not a test failure, and
    # a skip at setup is the ordinary @pytest.mark.skip path.
    if report.failed:
        return "error"
    if when == "setup" and report.skipped:
        return "xfailed" if wasxfail else "skipped"
    return None


def _observation_status(record):
    """One Hypothesis observation -> its status, or None to ignore it.

    Both shapes are accepted because the API is explicitly experimental:
    older versions hand the callback a plain dict, newer ones a
    TestCaseObservation object. A shape we do not recognize yields None and
    NO record — the metrics are then absent, which is the whole of decision
    15's rule that one metric name has one provenance forever.
    """
    if isinstance(record, dict):
        kind = record.get("type", "test_case")
        status = record.get("status", "")
        prop = record.get("property", "")
    else:
        kind = getattr(record, "type", "test_case")
        status = getattr(record, "status", "")
        prop = getattr(record, "property", "")
    if kind != "test_case" or not status:
        return None
    return (str(prop)[:200], str(status))


def _register_hypothesis(writer):
    """Forward Hypothesis's per-example observations onto OUR stream.

    Hypothesis's own observability mode writes JSONL into a directory the
    test process can write, which makes those records candidate-authorable
    AFTER EXIT — coverage_bp's status, and unacceptable for a number a gate
    reads. Records forwarded here instead ride the framed stream the control
    plane reads live, so they carry its S1/S2/S3 protections.

    Every failure mode is silent and total: no hypothesis, no callback list,
    a renamed API, a raising callback — all of them end with no
    property_case records, which the control plane reads as ABSENT metrics
    and labels `hypothesis-observability: jsonl`. It never turns an
    observability problem into a test failure, because that would let a
    candidate manufacture a failing property run for a competitor.
    """
    try:
        from hypothesis.internal.observability import TESTCASE_CALLBACKS
    except Exception:
        return False

    def _forward(record):
        try:
            parsed = _observation_status(record)
            if parsed is None:
                return
            prop, status = parsed
            writer.record("property_case", {"property": prop, "status": status})
        except Exception:
            pass

    try:
        TESTCASE_CALLBACKS.append(_forward)
    except Exception:
        return False
    return True


def pytest_configure(config):
    path = os.environ.get(_ENV_STREAM, "")
    nonce = os.environ.get(_ENV_NONCE, "")
    if not path or not nonce:
        # No channel was provided: the plugin is inert. This is the
        # in-tree regime and every local `pytest -p mvo_evidence`.
        return
    writer = _Writer(path, nonce)
    if not writer.ok():
        return
    _register_hypothesis(writer)
    config.pluginmanager.register(MvoEvidence(writer), "mvo-evidence-session")
