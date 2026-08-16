// Package pyplugin embeds mvo_evidence.py — the control-plane-owned pytest
// plugin that writes M1f's evidence stream — and its content address.
//
// The source is embedded rather than shipped alongside the binary for one
// reason: "which observer saw this run" must be an auditable fact, not a
// version guess. Every receipt the plugin produced records Digest in
// execution.evidence_plugin, and the bytes reach CAS before any run
// consumes them.
package pyplugin

import (
	_ "embed"

	"github.com/coagente/multiverso/internal/object"
)

// Filename is the module name pytest loads it under: `-p mvo_evidence`
// resolves this file on PYTHONPATH.
const Filename = "mvo_evidence.py"

// Source is the plugin's exact bytes.
//
//go:embed mvo_evidence.py
var Source []byte

// Digest is Source's content address ("sha256:…"), computed once at init.
// It is the value receipts record and the directory name the plugin is
// materialized under, so two binaries with different plugins never share a
// materialized copy.
var Digest = object.CASKeyBytes(Source)

// CorpusFilename is the corpus runner's module name. It is executed BY PATH
// (`python3 <plugin dir>/mvo_corpus.py`), never imported as a pytest
// plugin: the corpus rung does not go through pytest at all, which is what
// removes conftest.py collection, pytest.ini addopts, `-p` arguments and
// pytest11 entry-point autoloading from its attack surface in one step
// (M2a decision 12).
const CorpusFilename = "mvo_corpus.py"

// CorpusSource is the corpus runner's exact bytes.
//
//go:embed mvo_corpus.py
var CorpusSource []byte

// CorpusDigest is CorpusSource's content address. It is recorded in every
// corpus-observe receipt's execution.evidence_plugin, for the same reason
// the pytest observer's is: "which observer saw this run" must be an
// auditable fact, not a version guess.
var CorpusDigest = object.CASKeyBytes(CorpusSource)
