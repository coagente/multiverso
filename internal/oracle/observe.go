package oracle

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle/pyplugin"
	"github.com/coagente/multiverso/internal/policy"
)

// artCorpusStream is the observation stream's artifact kind.
const artCorpusStream = "evidence-stream"

// Observer is an oracle that additionally yields the PARSED observation it
// produced. The differential's reducer needs the observation, not merely
// the receipt: the receipt carries counts, and a comparison needs the
// per-case fingerprints behind them.
//
// It is a second RETURN VALUE rather than per-world state smuggled through
// the Oracle interface, which is the shape M1f decision 18 warns against.
// The orchestrator asks for it by interface, exactly as it asks a backend
// for its image digest.
type Observer interface {
	Oracle
	// Observe runs the oracle and returns its receipt together with the
	// parsed observation. The observation is empty and Usable == false
	// whenever the receipt's corpus metrics are absent — the two travel
	// together, so a caller cannot read one while believing the other.
	Observe(ctx context.Context, w backend.World) (object.Receipt, Observation, error)
}

// observeOracle is rung O2a: replay the pinned corpus inside one world and
// record what it returned, case by case, onto the control plane's evidence
// stream.
//
// It is the cheapest code-executing rung in the system AND the one with the
// smallest attack surface, because it does not import pytest (M2a decision
// 12). Everything else about it is an ordinary M1f streaming oracle: the
// channel is created before the process is spawned, read live, and torn
// down after it exits; a run whose stream is missing, malformed or
// unterminated yields ABSENT metrics, never a fabricated zero.
type observeOracle struct {
	spec    policy.Oracle
	store   artifactStore
	timeout time.Duration
	cap     int64
	ev      evidencePlan

	// corpus is the PINNED corpus this world must replay, materialized on
	// the base tree before any candidate existed. corpus_cases_total comes
	// from here — from the control plane's own object — which is why no
	// candidate authors that number in any regime.
	corpus       Corpus
	corpusDigest string
	baseTree     string
	// corpusPath is where the runner reads the corpus: the host path on
	// T0, the read-only mount on T1. It is NEVER inside the worktree.
	corpusPath string
	// runner is the in-world path of mvo_corpus.py and runnerDigest its
	// content address, recorded in execution.evidence_plugin.
	runner       string
	runnerDigest string
	// worldRoot is the in-world worktree root, prepended to PYTHONPATH so
	// the candidate's own modules are importable.
	worldRoot string
}

// ID implements Oracle.
func (o *observeOracle) ID() string { return policy.KindCorpusObserve }

// Version implements Oracle.
func (o *observeOracle) Version() string { return oracleVersion }

// python is the interpreter the runner executes under: the head of the
// instance's runner prefix, so a repo pinned to a virtualenv is observed
// with THAT interpreter and not with whatever python is first on PATH.
func (o *observeOracle) python() string {
	if len(o.spec.Argv) > 0 && o.spec.Argv[0] != "" {
		return o.spec.Argv[0]
	}
	return policy.DefaultPytestPrefix()[0]
}

// argv is the in-world invocation. No pytest, no plugin flag, no
// conftest.py: an interpreter, a control-plane-owned script executed by
// path from a read-only directory, two paths, and the digest the delivered
// bytes must hash to.
//
// The digest travels on argv rather than being read out of the file,
// because a value read out of the file is a value whoever wrote the file
// gets to choose. The runner hashes what it loaded and reports both, and
// ParseObservation refuses an observation of a corpus nobody pinned.
func (o *observeOracle) argv() []string {
	return []string{
		o.python(), o.runner,
		"--replay", "--corpus", o.corpusPath,
		"--corpus-digest", o.corpusDigest,
		"--root", o.worldRoot,
	}
}

// Run implements Oracle.
func (o *observeOracle) Run(ctx context.Context, w backend.World) (object.Receipt, error) {
	rec, _, err := o.Observe(ctx, w)
	return rec, err
}

// Observe runs the corpus and parses what came back.
func (o *observeOracle) Observe(ctx context.Context, w backend.World) (object.Receipt, Observation, error) {
	switch {
	case o.store == nil:
		return object.Receipt{}, Observation{}, fmt.Errorf("oracle: %s: nil CAS store", o.ID())
	case w == nil:
		return object.Receipt{}, Observation{}, fmt.Errorf("oracle: %s: nil world", o.ID())
	case o.spec.Config == "":
		return object.Receipt{}, Observation{}, fmt.Errorf("oracle: %s: spec carries no resolved config digest", o.ID())
	case o.corpusPath == "":
		return object.Receipt{}, Observation{}, fmt.Errorf("oracle: %s: no corpus was delivered to this world", o.ID())
	case o.corpusDigest == "":
		// Without the pinned digest the runner has nothing to be held to and
		// the fourth usability rule cannot be evaluated. A gate that cannot
		// be evaluated fails, and an oracle whose binding is missing is
		// machinery — not a candidate that did something wrong.
		return object.Receipt{}, Observation{}, fmt.Errorf("oracle: %s: no pinned corpus digest was supplied, so the observation cannot be bound to the bytes it replayed", o.ID())
	}
	start := time.Now()
	runCtx, cancel := ctx, func() {}
	if o.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, o.timeout)
	}
	defer cancel()

	nonce, err := newNonce()
	if err != nil {
		return object.Receipt{}, Observation{}, fmt.Errorf("oracle: %s: %w", o.ID(), err)
	}
	if err := ensureDir(o.ev.hostEvidence, 0o755); err != nil {
		return object.Receipt{}, Observation{}, fmt.Errorf("oracle: %s: evidence dir: %w", o.ID(), err)
	}
	ch, err := openEvidenceChannel(o.ev.hostEvidence, nonce, o.artifactCap())
	if err != nil {
		return object.Receipt{}, Observation{}, err
	}
	// However this returns, the channel is torn down: a channel whose
	// reader outlives the run is a channel a later run could be talked
	// into reading.
	defer ch.Close()

	argv := o.argv()
	proc := runInWorld(runCtx, w, argv, o.worldEnv(w, nonce))

	// EP-7 order, unchanged: the RAW stream bytes reach CAS before
	// anything is parsed.
	raw := ch.Close()
	obs := ParseObservation(raw, nonce, ch.Over(), o.corpus, o.corpusDigest)
	streamKey, err := o.store.Put(obs.Raw)
	if err != nil {
		return object.Receipt{}, Observation{}, fmt.Errorf("oracle: %s: store %s: %w", o.ID(), artCorpusStream, err)
	}

	metrics := map[string]int64{}
	tools := map[string]string{}
	status := proc.Status
	notes := obs.Notes
	if obs.Usable {
		tools[ToolEvidence] = StreamVersion
		// corpus_cases_total is the CONTROL PLANE's number — the pinned
		// corpus object's own case count — and it is emitted beside the
		// three the world reported so a reader can see the denominator
		// the world was measured against, not the one it claims.
		metrics[policy.MetricCorpusCasesTotal] = int64(len(o.corpus.Cases))
		metrics[policy.MetricCorpusCasesObserved] = obs.ObservedCount()
		metrics[policy.MetricCorpusCasesOpaque] = obs.Opaque
		metrics[policy.MetricCorpusCasesErrored] = obs.Errored
		switch {
		case proc.ExitCode == 0 && obs.ObservedCount() == int64(len(o.corpus.Cases)):
			status = StatusPass
		case proc.ExitCode == 0:
			// The process said it finished and the stream says it did not
			// finish the corpus. That is a disagreement between two things
			// the same process reported, and `fail` is the honest verdict
			// — the world did not do what it was asked.
			status = StatusFail
		default:
			status = StatusFail
		}
	} else {
		// S1 — absence never passes. No usable stream ⇒ every
		// stream-derived metric absent, and a process that exited 0 with
		// no usable stream is `error`, never `pass`. This is corpus vector
		// 18's whole result: silencing the runner to shrink the cohort
		// eliminates the silencer.
		notes += "mvo: oracle: " + obs.Reason + "\n"
		if proc.ExitCode == 0 {
			status = StatusError
		} else {
			status = StatusFail
		}
	}

	stdoutKey, err := o.store.Put(proc.Stdout)
	if err != nil {
		return object.Receipt{}, Observation{}, fmt.Errorf("oracle: %s: store %s: %w", o.ID(), artStdout, err)
	}
	stderrKey, err := o.store.Put(append(append([]byte(nil), proc.Stderr...), notes...))
	if err != nil {
		return object.Receipt{}, Observation{}, fmt.Errorf("oracle: %s: store %s: %w", o.ID(), artStderr, err)
	}

	corr := policy.KindCorrelation(policy.KindCorpusObserve)
	corr.Corpus = o.corpusDigest
	rec := object.Receipt{
		Schema: object.SchemaReceipt,
		Oracle: object.OracleRef{ID: o.ID(), Version: o.Version(), Config: o.spec.Config},
		Execution: object.Execution{
			Argv:           argv,
			ExitCode:       proc.ExitCode,
			DurationMS:     proc.WallMS,
			IsolationTier:  w.Tier(),
			IsolationCaps:  w.Caps(),
			EvidenceRegime: o.ev.regime,
			EvidencePlugin: o.runnerDigest,
		},
		Result: object.Result{
			Status:    status,
			Metrics:   metrics,
			Tools:     tools,
			Detail:    "",
			Artifacts: []string{stdoutKey, stderrKey, streamKey},
		},
		Freshness:   object.Freshness{Basis: object.BasisConstruction},
		RecheckTier: recheckTier,
		Family:      policy.FamilyBehavior,
		Cost: sizedCost(time.Since(start).Milliseconds(),
			int64(len(o.corpus.Cases)), policy.UnitCases),
		Inputs: map[string]string{
			object.InputBaseTree: o.baseTree,
			object.InputCorpus:   o.corpusDigest,
		},
		Correlation: corr,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	return rec, obs, nil
}

// worldEnv builds the in-world environment: the channel the runner appends
// to, its nonce, and PYTHONPATH.
//
// PYTHONPATH is PREPENDED, never assigned over — M1f's rule, verbatim,
// because assigning over an operator's PYTHONPATH was a shipped bug once
// and cost every src-layout repository its first race. The worktree root
// comes first so the candidate's own modules are what the corpus's targets
// resolve to; that is the entire point of running the corpus in a world.
func (o *observeOracle) worldEnv(w backend.World, nonce string) []string {
	extra := []string{
		envPyPath + "=" + o.worldRoot,
		envStream + "=" + o.inWorldStream(),
		envNonce + "=" + nonce,
	}
	if w.Tier() != object.TierT0Worktree {
		// On T1 nothing is inherited by construction: the image owns its
		// environment exactly as it owns PATH (M1c decision 7).
		return extra
	}
	merged := make([]string, 0, len(extra))
	for _, kv := range extra {
		if name, val, ok := strings.Cut(kv, "="); ok && name == envPyPath {
			kv = envPyPath + "=" + prependPath(val, os.Getenv(envPyPath))
		}
		merged = append(merged, kv)
	}
	return append(os.Environ(), merged...)
}

func (o *observeOracle) inWorldStream() string {
	if o.ev.inWorldEvidence != "" {
		return path.Join(o.ev.inWorldEvidence, streamFile)
	}
	return filepath.ToSlash(filepath.Join(o.ev.hostEvidence, streamFile))
}

func (o *observeOracle) artifactCap() int64 {
	if o.cap > 0 {
		return o.cap
	}
	return artifactCapBytes
}

// MaterializeCorpusRunner writes the embedded corpus runner into the
// already-materialized plugin directory (mode 0444) and returns its path
// and content address. Idempotent: the digest names the content.
//
// It shares the plugin directory rather than claiming one of its own
// because that directory is what T1 bind-mounts read-only at /mvo/plugin,
// and a second read-only mount for one more script would be plumbing with
// no property behind it.
func MaterializeCorpusRunner(dir string) (runnerPath, digest string, err error) {
	digest = pyplugin.CorpusDigest
	file := filepath.Join(dir, pyplugin.CorpusFilename)
	if b, err := os.ReadFile(file); err == nil && object.CASKeyBytes(b) == digest {
		return file, digest, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("oracle: materialize corpus runner: %w", err)
	}
	// Written through a temp file and renamed: two racing worlds must
	// never see a half-written runner.
	tmp, err := os.CreateTemp(dir, ".mvo_corpus-")
	if err != nil {
		return "", "", fmt.Errorf("oracle: materialize corpus runner: %w", err)
	}
	if _, err := tmp.Write(pyplugin.CorpusSource); err != nil {
		_ = tmp.Close()
		return "", "", fmt.Errorf("oracle: materialize corpus runner: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", "", fmt.Errorf("oracle: materialize corpus runner: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o444); err != nil {
		return "", "", fmt.Errorf("oracle: materialize corpus runner: %w", err)
	}
	if err := os.Rename(tmp.Name(), file); err != nil {
		return "", "", fmt.Errorf("oracle: materialize corpus runner: %w", err)
	}
	return file, digest, nil
}

// CorpusRunnerDigest is the embedded runner's content address — the value
// every corpus-observe receipt records in execution.evidence_plugin.
func CorpusRunnerDigest() string { return pyplugin.CorpusDigest }

// CorpusRunnerSource is the embedded runner's exact bytes, for the CAS put
// that makes every recorded digest resolvable.
func CorpusRunnerSource() []byte { return append([]byte(nil), pyplugin.CorpusSource...) }
