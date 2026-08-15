package oracle

// M1f: the embedded observer. Neither test requires pytest to be
// installed, which is the rule — a test that needs a plugin installed is a
// test that silently stops running.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle/pyplugin"
)

// The plugin's content address is a FACT about the bytes, recomputed here
// rather than trusted: "which observer saw this run" is what every
// streamed receipt records in execution.evidence_plugin, and a digest that
// could drift from its source would make that record a version guess.
func TestPluginDigestMatchesItsSource(t *testing.T) {
	if got := object.CASKeyBytes(pyplugin.Source); got != pyplugin.Digest {
		t.Errorf("pyplugin.Digest = %s, want %s (the digest of the embedded bytes)", pyplugin.Digest, got)
	}
	if PluginDigest() != pyplugin.Digest {
		t.Errorf("PluginDigest() = %s, want %s", PluginDigest(), pyplugin.Digest)
	}
	if !strings.HasPrefix(pyplugin.Digest, "sha256:") {
		t.Errorf("digest %q is not an artifact key", pyplugin.Digest)
	}
	// The source really is the observer, not a stub: it must register on
	// pytest_configure and write the framed header.
	src := string(pyplugin.Source)
	for _, want := range []string{
		"def pytest_configure(", "pytest_sessionfinish", "pytest_runtest_logreport",
		"MVO_EVIDENCE_STREAM", "MVO_EVIDENCE_NONCE", "mvo-evidence/v0",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the embedded plugin does not contain %q", want)
		}
	}
}

// The observer is materialized read-only, under its own content address,
// and materializing twice is a no-op.
func TestMaterializePlugin(t *testing.T) {
	root := t.TempDir()
	dir, dig, err := MaterializePlugin(root)
	if err != nil {
		t.Fatalf("MaterializePlugin: %v", err)
	}
	if dig != pyplugin.Digest {
		t.Errorf("digest = %s, want %s", dig, pyplugin.Digest)
	}
	if filepath.Base(dir) != strings.ReplaceAll(pyplugin.Digest, ":", "-") {
		t.Errorf("dir = %s, want it named by the content address", dir)
	}
	file := filepath.Join(dir, pyplugin.Filename)
	st, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// 0444: the observer is not writable by anyone, including us.
	if st.Mode().Perm() != 0o444 {
		t.Errorf("mode = %v, want 0444", st.Mode().Perm())
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if object.CASKeyBytes(b) != pyplugin.Digest {
		t.Error("the materialized file is not the embedded source")
	}
	// Idempotent: the digest names the content, so a second call over the
	// same bytes changes nothing.
	dir2, dig2, err := MaterializePlugin(root)
	if err != nil || dir2 != dir || dig2 != dig {
		t.Errorf("second MaterializePlugin = (%s, %s, %v), want the first result", dir2, dig2, err)
	}
}
