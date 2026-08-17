package eval

// THE LEAK DETECTORS, each against a positive and a negative case, plus the
// canary over a synthesized workspace containing the token in each of ledger /
// CAS blob / world tree / transcript / artifact / scratch.
//
// The fuzz-shaped case that matters most is the one where the hidden path
// appears ONLY inside a receipt argv: that is D1's whole reason to exist, and it
// is the shape M1f's entry-point plugin vector taught us not to assume away.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/backend"
	"github.com/coagente/multiverso/internal/object"
)

func testNeedles() Needles {
	inst := Instance{ID: "i1", OracleDigest: "sha256:" + strings.Repeat("ab", 32)}
	h := HiddenOracle{
		FailToPass:  []string{"mean_single@CANARYTOKEN"},
		PassToPass:  []string{"total@CANARYTOKEN"},
		CanaryToken: "CANARYTOKEN",
		CanaryID:    "canary-test",
		GoldPatch:   "diff --git a/x b/x\n",
		Files: map[string]string{
			"mvo_hidden_run.py":    "# canary CANARYTOKEN\n",
			"mvo_hidden_checks.py": "# canary CANARYTOKEN\n",
		},
	}
	return NeedlesFor(inst, h, []byte("hidden-bytes"), "/eval/home")
}

func receiptWithArgv(argv []string) object.RecordedReceipt {
	r := object.Receipt{
		Oracle:    object.OracleRef{ID: "pytest-suite", Version: "v0", Config: "mv0:cfg"},
		Execution: object.Execution{Argv: argv},
		Result:    object.NewResult("pass"),
	}
	return object.RecordedReceipt{Digest: "mv0:receipt", Receipt: r}
}

func TestD1FiresOnAHiddenPathInAnArgvAndNowhereElse(t *testing.T) {
	n := testNeedles()
	// POSITIVE: the hidden path appears ONLY in a receipt argv. No tree, no
	// CAS key, no transcript carries it — this is the case that would slip
	// past D2..D5, and it is why D1 exists.
	rep := D1Argv(n, []object.RecordedReceipt{
		receiptWithArgv([]string{"python3", "-m", "pytest", "mvo_hidden_run.py"}),
	})
	if !rep.Void() {
		t.Fatalf("D1 did not fire on a hidden suite path in an argv")
	}
	if rep.Findings[0].NeedleKind != NeedleHiddenPath {
		t.Errorf("needle kind = %q", rep.Findings[0].NeedleKind)
	}
	// NEGATIVE: an ordinary argv.
	rep = D1Argv(n, []object.RecordedReceipt{
		receiptWithArgv([]string{"python3", "-m", "pytest", "-q", "test_stats.py"}),
	})
	if rep.Void() {
		t.Errorf("D1 fired on an ordinary argv: %v", rep.Lines())
	}
	// And the counters distinguish "clean" from "nothing scanned".
	if rep.Scanned["receipt"] != 1 {
		t.Errorf("D1 did not record what it scanned: %v", rep.Scanned)
	}
	if empty := D1Argv(n, nil); empty.Scanned["receipt"] != 0 {
		t.Errorf("an empty scan claims to have scanned something: %v", empty.Scanned)
	}
}

func TestD1AlsoScansOracleConfigAndDetail(t *testing.T) {
	n := testNeedles()
	r := receiptWithArgv([]string{"python3"})
	r.Receipt.Oracle.Config = "sha256:" + strings.Repeat("ab", 32) // the oracle digest
	if !D1Argv(n, []object.RecordedReceipt{r}).Void() {
		t.Errorf("D1 missed the oracle digest in OracleRef.Config")
	}
	r = receiptWithArgv([]string{"python3"})
	r.Receipt.Result.Detail = "first offending path: /eval/home/local-derived"
	if !D1Argv(n, []object.RecordedReceipt{r}).Void() {
		t.Errorf("D1 missed an eval-home path in Result.Detail")
	}
}

func TestD2FiresOnTreeMembershipIncludingANameCollision(t *testing.T) {
	n := testNeedles()
	// POSITIVE: a hidden suite file is a member of a world tree.
	rep := D2Trees(n, map[string][]string{
		"git:abc": {"stats.py", "mvo_hidden_run.py"},
	})
	if !rep.Void() {
		t.Fatalf("D2 did not fire on a hidden path in a world tree")
	}
	// A NAME COLLISION fires it too, in a subdirectory: from inside the
	// harness a collision is indistinguishable from a leak, and resolving
	// that ambiguity in the experiment's favour is how a leaked instance gets
	// published.
	rep = D2Trees(n, map[string][]string{
		"git:def": {"tests/mvo_hidden_run.py"},
	})
	if !rep.Void() {
		t.Errorf("D2 did not fire on a basename collision in a subdirectory")
	}
	// NEGATIVE.
	rep = D2Trees(n, map[string][]string{"git:ghi": {"stats.py", "test_stats.py"}})
	if rep.Void() {
		t.Errorf("D2 fired on a clean tree: %v", rep.Lines())
	}
	if rep.Scanned["tree-entry"] != 2 {
		t.Errorf("D2 did not count what it scanned: %v", rep.Scanned)
	}
}

func TestD3FiresOnHiddenBytesInCASAndOnGoldOnlyWhenGoldWasWithheld(t *testing.T) {
	n := testNeedles()
	hiddenKey := CASKeyBytes([]byte("hidden-bytes"))
	// POSITIVE: the hidden bytes are in the workspace CAS.
	if !D3CAS(n, []string{hiddenKey}, true).Void() {
		t.Fatalf("D3 did not fire on the hidden bytes' digest in CAS")
	}
	goldKey := CASKeyBytes([]byte("diff --git a/x b/x\n"))
	// Gold's bytes in CAS are EXPECTED when gold is raced as a candidate.
	if D3CAS(n, []string{goldKey}, true).Void() {
		t.Errorf("D3 fired on gold's digest while gold was raced: that is the design, not a leak")
	}
	// And they are a leak when gold was deliberately withheld (family B).
	if !D3CAS(n, []string{goldKey}, false).Void() {
		t.Errorf("D3 missed gold's digest on an instance where gold was withheld")
	}
	if D3CAS(n, []string{"sha256:" + strings.Repeat("cd", 32)}, true).Void() {
		t.Errorf("D3 fired on an unrelated CAS key")
	}
}

func TestD4FiresOnANodeIDInATranscript(t *testing.T) {
	n := testNeedles()
	rep := D4Transcripts(n, []Doc{
		{Kind: "world.context", Ref: "w1", Bytes: []byte("please make mean_single@CANARYTOKEN pass")},
	})
	if !rep.Void() {
		t.Fatalf("D4 did not fire on a node id in a prompt")
	}
	rep = D4Transcripts(n, []Doc{
		{Kind: "world.context", Ref: "w1", Bytes: []byte("fix stats.mean(); do not edit tests")},
	})
	if rep.Void() {
		t.Errorf("D4 fired on an ordinary prompt: %v", rep.Lines())
	}
}

func TestD4DoesNotFireOnGoldsOwnText(t *testing.T) {
	// The retired gold-text needle, kept as a REGRESSION TEST. Gold is raced
	// as an ordinary candidate on every family-A instance, so its bytes are
	// legitimately the world's captured patch, and every S2 mutant shares its
	// hunk header. A detector that fired here would fire on the experiment
	// working — which the first end-to-end run of this block proved by
	// voiding three good instances.
	n := testNeedles()
	gold := "diff --git a/stats.py b/stats.py\nindex f206bcb..a1dd77c 100644\n" +
		"@@ -5,7 +5,7 @@ def mean(values):\n-    x\n+    y\n"
	rep := D4Transcripts(n, []Doc{
		{Kind: "world.context", Ref: "w1", Bytes: []byte(gold)},
		{Kind: "world.patch", Ref: "w1", Bytes: []byte(gold)},
	})
	if rep.Void() {
		t.Fatalf("a gold-text needle came back: %v", rep.Lines())
	}
}

func TestD5CanaryOverEverySurfaceOfASynthesizedWorkspace(t *testing.T) {
	n := testNeedles()
	// The canary planted in each of the six surfaces in turn, one at a time,
	// so a detector that only looked at one of them cannot pass.
	surfaces := []string{"ledger.payload", "workspace.cas", "world.tree", "world.trace", "artifact", "scratch"}
	for _, s := range surfaces {
		rep := D5Canary(n, []Doc{{Kind: s, Ref: "r", Bytes: []byte("noise CANARYTOKEN noise")}})
		if !rep.Void() {
			t.Errorf("D5 did not fire on the canary in %s", s)
		}
	}
	if D5Canary(n, []Doc{{Kind: "workspace.file", Ref: "r", Bytes: []byte("no needle here")}}).Void() {
		t.Errorf("D5 fired on a clean surface")
	}
	// A run with NO canary must not be reported as canary-clean: D5 cannot
	// detect what it was not given, and saying nothing would be a claim.
	rep := D5Canary(Needles{}, []Doc{{Kind: "x", Ref: "r", Bytes: []byte("anything")}})
	if !rep.Void() {
		t.Errorf("D5 with no canary in force reported clean")
	}
}

func TestD5CanaryCatchesATranscriptPlantNoOtherDetectorSees(t *testing.T) {
	// Acceptance step m2d-7b's second half, as a unit test: the canary in a
	// TRANSCRIPT, with clean trees, clean argv and clean CAS. Only D5 can see
	// it, which is the reason D5 exists.
	n := testNeedles()
	trees := map[string][]string{"git:abc": {"stats.py"}}
	if D2Trees(n, trees).Void() {
		t.Fatalf("the trees are not clean")
	}
	if D1Argv(n, []object.RecordedReceipt{receiptWithArgv([]string{"python3", "-m", "pytest"})}).Void() {
		t.Fatalf("the argv is not clean")
	}
	if D3CAS(n, []string{"sha256:" + strings.Repeat("cd", 32)}, true).Void() {
		t.Fatalf("the CAS is not clean")
	}
	doc := Doc{Kind: "world.trace", Ref: "w1", Bytes: []byte("...tool output... CANARYTOKEN ...")}
	if !D5Canary(n, []Doc{doc}).Void() {
		t.Fatalf("D5 missed a canary planted only in a transcript")
	}
}

func TestWalkFilesFeedsD5FromARealDirectory(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a.txt", "sub/b.txt", "sub/deep/c.bin"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "clean"
		if rel == "sub/deep/c.bin" {
			body = "CANARYTOKEN"
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	docs, skipped, err := WalkFiles(root, "workspace.file")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 || skipped != 0 {
		t.Fatalf("walked %d docs, skipped %d", len(docs), skipped)
	}
	rep := D5Canary(testNeedles(), docs)
	if !rep.Void() {
		t.Fatalf("D5 missed the canary in a nested file")
	}
	if !strings.Contains(rep.Findings[0].Ref, "c.bin") {
		t.Errorf("the finding does not name the hitting path: %+v", rep.Findings[0])
	}
}

func TestCheckMountsRefusesAnEvalHomeBindMount(t *testing.T) {
	// The row that exists because M2a amendment 29 found the mount surface
	// had quietly made the pinned corpus world-visible for a whole block.
	home := "/eval/home"
	bad := backend.OpenOpts{CorpusDir: "/eval/home/local-derived/v0"}
	if len(CheckMounts(bad, home)) == 0 {
		t.Fatalf("CheckMounts allowed a corpus mount under the eval home")
	}
	worse := backend.OpenOpts{EvidenceDir: home}
	if len(CheckMounts(worse, home)) == 0 {
		t.Fatalf("CheckMounts allowed the eval home itself as an evidence mount")
	}
	ok := backend.OpenOpts{
		EvidenceDir: "/tmp/ws/.multiverso/evidence",
		ScratchDir:  "/tmp/ws/.multiverso/scratch",
		CorpusDir:   "/tmp/ws/.multiverso/corpora/r1",
	}
	if f := CheckMounts(ok, home); len(f) != 0 {
		t.Errorf("CheckMounts refused ordinary workspace mounts: %+v", f)
	}
	// A neighbouring directory whose name merely PREFIXES the home is not
	// under it: "/eval/home-2" must not fire.
	if f := CheckMounts(backend.OpenOpts{CorpusDir: "/eval/home-2/x"}, home); len(f) != 0 {
		t.Errorf("CheckMounts fired on a sibling path that only shares a prefix: %+v", f)
	}
}

func TestNonConsultationRefusesWhenADetectorDidNotRun(t *testing.T) {
	// A proof whose premises were skipped is a sentence, not a proof.
	nc := NonConsultation{
		ArgvClean: true, EnvScrubbed: true, EnvClean: true, CWDOutsideEvalHome: true,
		HiddenModeOK: true, HomeModeOK: true, HiddenOutsideWorkspace: true,
		LabelsAfterSeal: true, ScorerAfterRacer: true,
	}
	nc.Prove()
	if nc.Proved {
		t.Fatalf("Prove() succeeded with no detector having run: %+v", nc.Refusals)
	}
	// With all five detectors run and nothing found, it proves.
	for _, d := range []string{DetectorArgv, DetectorTree, DetectorCAS, DetectorTranscript, DetectorCanary} {
		nc.Leak.Detectors = append(nc.Leak.Detectors, d)
	}
	nc.Prove()
	if !nc.Proved {
		t.Fatalf("Prove() failed with every premise met: %v", nc.Refusals)
	}
	if len(nc.Residuals) == 0 {
		t.Errorf("a proof object with no stated residuals is a stronger claim than the evidence supports")
	}
	// One finding is enough to unprove it.
	nc.Leak.Findings = append(nc.Leak.Findings, Finding{Detector: DetectorCanary, Needle: "CANARYTOKEN"})
	nc.Prove()
	if nc.Proved {
		t.Errorf("Prove() succeeded with a leak finding present")
	}
	// And each structural fact is individually load-bearing.
	for i, mut := range []func(*NonConsultation){
		func(x *NonConsultation) { x.ArgvClean = false },
		func(x *NonConsultation) { x.EnvScrubbed = false },
		func(x *NonConsultation) { x.EnvClean = false },
		func(x *NonConsultation) { x.CWDOutsideEvalHome = false },
		func(x *NonConsultation) { x.HiddenModeOK = false },
		func(x *NonConsultation) { x.HomeModeOK = false },
		func(x *NonConsultation) { x.HiddenOutsideWorkspace = false },
		func(x *NonConsultation) { x.LabelsAfterSeal = false },
		func(x *NonConsultation) { x.ScorerAfterRacer = false },
	} {
		good := NonConsultation{
			ArgvClean: true, EnvScrubbed: true, EnvClean: true, CWDOutsideEvalHome: true,
			HiddenModeOK: true, HomeModeOK: true, HiddenOutsideWorkspace: true,
			LabelsAfterSeal: true, ScorerAfterRacer: true,
			Leak: Report{Detectors: []string{DetectorArgv, DetectorTree, DetectorCAS, DetectorTranscript, DetectorCanary}},
		}
		mut(&good)
		good.Prove()
		if good.Proved {
			t.Errorf("mutation %d did not unprove the witness", i)
		}
	}
}

// D3's GOLD NEEDLE MUST BE THE BYTES A RACER COULD HOLD.
//
// It was not. NeedlesFor digested `h.GoldPatch`, which the hidden half prefixes
// with `# mvo-hidden-canary <token>` so that a leak of gold's TEXT trips D5. The
// racer, when gold is raced at all, receives the STRIPPED patch without that
// header. D3 arms the gold needle only when gold is withheld — the family-B
// instances — so on exactly the instances where it is armed it was looking for
// a key no workspace could ever contain. The one check meant to catch "gold's
// bytes reached a workspace where gold was withheld" could not fire.
func TestD3FiresWhenGoldsRACEABLEBytesAreInAFamilyBWorkspace(t *testing.T) {
	gold := "diff --git a/stats.py b/stats.py\n--- a/stats.py\n+++ b/stats.py\n@@ -1 +1 @@\n-a\n+b\n"
	canary := "CANARY" + strings.Repeat("e", 26)
	h := HiddenOracle{
		CanaryToken: canary,
		// The hidden half's form: canary header, then gold.
		GoldPatch: "# mvo-hidden-canary " + canary + "\n" + gold,
		Files:     map[string]string{"mvo_hidden_run.py": "x"},
	}
	n := NeedlesFor(Instance{ID: "b"}, h, []byte("hidden"), "/eval/home")

	raceable := CASKeyBytes([]byte(gold))
	prefixed := CASKeyBytes([]byte(h.GoldPatch))
	if raceable == prefixed {
		t.Fatalf("the two forms digest the same: this test cannot detect the bug")
	}
	if n.GoldPatchDigest != raceable {
		t.Fatalf("the gold needle is %s, want the RACEABLE form %s (it was digesting the "+
			"canary-prefixed hidden form %s, which no workspace can contain)",
			n.GoldPatchDigest, raceable, prefixed)
	}
	// Positive case: gold's raceable bytes are in a family-B workspace CAS.
	rep := D3CAS(n, []string{"sha256:" + strings.Repeat("0", 64), raceable}, false)
	if !rep.Void() {
		t.Fatalf("D3 did not fire on gold's raceable bytes in a workspace where gold was withheld")
	}
	if rep.Findings[0].NeedleKind != NeedleGoldDigest {
		t.Errorf("the finding names %q, want %q", rep.Findings[0].NeedleKind, NeedleGoldDigest)
	}
	// Negative case: gold WAS raced, so its bytes are legitimately in CAS.
	if D3CAS(n, []string{raceable}, true).Void() {
		t.Errorf("D3 fired on gold's bytes in a workspace where gold was raced as a candidate: " +
			"a detector that fires on the experiment working is not a detector")
	}
}

// A skip count refuses the proof. Both counters existed and both were dropped
// by the only production caller, so a surface a detector could not read was
// indistinguishable from one it read and found clean.
func TestASkippedSurfaceRefusesTheNonConsultationProof(t *testing.T) {
	nc := NonConsultation{}
	for _, d := range []string{DetectorArgv, DetectorTree, DetectorCAS, DetectorTranscript, DetectorCanary} {
		nc.Leak.note(d, "x", 1)
	}
	nc.ArgvClean, nc.EnvScrubbed, nc.EnvClean = true, true, true
	nc.CWDOutsideEvalHome, nc.HiddenModeOK, nc.HomeModeOK = true, true, true
	nc.HiddenOutsideWorkspace, nc.LabelsAfterSeal, nc.ScorerAfterRacer = true, true, true
	nc.Prove()
	if !nc.Proved {
		t.Fatalf("a clean witness did not prove: %v", nc.Refusals)
	}
	nc.Leak.NoteSkipped("workspace.file", 1)
	nc.Prove()
	if nc.Proved {
		t.Errorf("the witness proved with 1 unreadable surface: 'we scanned three of six' " +
			"and 'we scanned six' are different claims")
	}
	if len(nc.Refusals) != 1 || !strings.Contains(nc.Refusals[0], "unreadable") {
		t.Errorf("the refusal does not name the unread surface: %v", nc.Refusals)
	}
}
