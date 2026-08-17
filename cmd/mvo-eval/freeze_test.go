package main

// THE FREEZE REFUSAL, at the command level.
//
// Decision 6's mechanism is not "we promise not to tune after the freeze" — it
// is that scoring the EVAL SPLIT recomputes the policy digest and the scheduler
// constants, compares them against the freeze file, and REFUSES while naming
// what moved. Tuning afterwards is not forbidden; it is made impossible to do
// quietly, which is a different and achievable thing.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRepoRoot builds a directory with just the eval/ files `run` reads, so the
// freeze can be moved without touching the repository's own committed one.
func fakeRepoRoot(t *testing.T, policyDigest string, constants map[string]int64) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"eval/freeze", "eval/splits", "eval/corpora"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The split file must be the RECORDED FUNCTION or LoadSplit refuses it, so
	// it is copied from the repository rather than invented.
	src, err := os.ReadFile("../../eval/splits/local-derived-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "eval/splits/local-derived-v1.json"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	fz := map[string]any{
		"schema":         "multiverso.dev/eval-freeze/v0",
		"corpus":         "local-derived",
		"version":        "v0",
		"frozen_at":      "2026-08-17T00:00:00Z",
		"policy_digest":  policyDigest,
		"constants":      constants,
		"binary_digest":  "",
		"oracle_digests": map[string]string{},
	}
	b, err := json.MarshalIndent(fz, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "eval/freeze/local-derived-v1.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	// A stand-in for `mvo`: the freeze check runs BEFORE the binary is used, so
	// this only has to exist. Using /bin/true would make the test depend on a
	// path that is not on every platform.
	stub := filepath.Join(root, "mvo-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestScoringTheEvalSplitRefusesUnderAMovedPolicyDigest(t *testing.T) {
	bin := buildEvalBinary(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// A corpus must be present, or the run degrades before it ever reaches the
	// freeze check — and a test that passed for that reason would be testing
	// nothing.
	out, code := runEval(t, bin, home, "fetch", "local-derived", "--repo-root", "../..")
	if code != 0 {
		t.Skipf("cannot materialize the corpus here: %s", out)
	}

	// A freeze whose policy digest and one constant have both moved.
	consts := map[string]int64{"executor_bp.candidate-process": 1234}
	root := fakeRepoRoot(t, "mv0:0000000000000000000000000000000000000000000000000000000000000000", consts)

	out, code = runEval(t, bin, home, "run", "--repo-root", root, "--split", "eval",
		"--mvo", filepath.Join(root, "mvo-stub"))
	if code == 0 {
		t.Fatalf("scoring the eval split proceeded under freeze drift:\n%s", out)
	}
	if !strings.Contains(out, "refusing to score the eval split") {
		t.Errorf("the refusal does not say what it is refusing:\n%s", out)
	}
	if !strings.Contains(out, "policy_digest") {
		t.Errorf("the refusal does not NAME the policy digest as what moved:\n%s", out)
	}
	if !strings.Contains(out, "constants.executor_bp.candidate-process") {
		t.Errorf("the refusal does not name the moved constant:\n%s", out)
	}
	if !strings.Contains(out, "--unfreeze") {
		t.Errorf("the refusal does not name the escape hatch:\n%s", out)
	}

	// With --unfreeze it proceeds, and the reason is carried in the manifest
	// rather than swallowed. (The run itself fails later because /bin/true is
	// not mvo; what matters is that it got PAST the freeze check.)
	out, _ = runEval(t, bin, home, "run", "--repo-root", root, "--split", "eval",
		"--mvo", filepath.Join(root, "mvo-stub"), "--unfreeze", "measuring the effect of the new constant")
	if strings.Contains(out, "refusing to score the eval split") {
		t.Errorf("--unfreeze did not lift the refusal:\n%s", out)
	}

	// And the DEV split is not gated at all: on dev instances a developer may
	// look at everything and tune anything. Dev freedom is a developer's,
	// never a program's — the gates always see the public projection.
	out, _ = runEval(t, bin, home, "run", "--repo-root", root, "--split", "dev", "--mvo", filepath.Join(root, "mvo-stub"))
	if strings.Contains(out, "refusing to score the eval split") {
		t.Errorf("the dev split was gated by the freeze:\n%s", out)
	}
}

func TestAnUnmovedFreezeDoesNotRefuse(t *testing.T) {
	// The committed freeze pins the SHIPPED DEFAULT policy digest and the live
	// scheduler constants. If this test fails, one of them moved and every
	// frozen number is about a different configuration — which is exactly what
	// the freeze exists to make loud.
	bin := buildEvalBinary(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(home, "mvo-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runEval(t, bin, home, "run", "--repo-root", "../..", "--split", "eval", "--mvo", stub)
	// With no corpus this degrades to a named skip long before the freeze
	// matters; either way it must not report drift.
	if strings.Contains(out, "FREEZE DRIFT") || strings.Contains(out, "refusing to score the eval split") {
		t.Errorf("the committed freeze reports drift against the live binary (exit %d):\n%s", code, out)
	}
}
