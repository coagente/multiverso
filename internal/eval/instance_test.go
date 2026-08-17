package eval

// THE PUBLIC PROJECTION contains no node id, no gold patch and no canary — a
// FIELD-BY-FIELD assertion over the struct, plus a golden JSON so a new hidden
// field cannot be added without failing a test.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
)

// fullyPopulatedHidden is a hidden oracle with EVERY field non-zero, so a
// "does the projection contain any hidden value" test cannot pass because a
// field happened to be empty.
func fullyPopulatedHidden() HiddenOracle {
	return HiddenOracle{
		Schema:            SchemaOracle,
		Instance:          "i1",
		FailToPass:        []string{"NODE_F2P_SECRET"},
		PassToPass:        []string{"NODE_P2P_SECRET"},
		SuiteArgv:         []string{"{python}", "SUITE_ARGV_SECRET"},
		TimeoutMS:         12345,
		CanaryToken:       "CANARY_SECRET_TOKEN",
		CanaryID:          "canary-visible",
		GoldPatch:         "GOLD_PATCH_SECRET",
		StrippedHunks:     []string{"tests/test_secret.py"},
		GoldCandidate:     "GOLD_CANDIDATE_SECRET",
		StrengthenedSuite: "STRENGTHENED_SECRET",
		Files:             map[string]string{"HIDDEN_FILE_SECRET.py": "HIDDEN_BODY_SECRET"},
		Tier:              Tier1,
	}
}

func fullyPopulatedInstance() Instance {
	return Instance{
		Schema: SchemaInstance, ID: "i1", Corpus: CorpusLocalDerived, Version: LocalVersion,
		Family: FamilyGoldPresent, Repo: "repos/i1", RepoURL: "https://example.invalid/r",
		BaseCommit: "deadbeef", EnvImage: "python:3.12-alpine", T0OK: true,
		Task: "fix mean()",
		Candidates: []Candidate{
			{Ord: 0, ID: "gold", Source: SourceGold, Patch: "sha256:" + strings.Repeat("a", 64),
				PatchBytes: 10, ResultTree: "git:aaa", Expected: ExpectCorrect},
			{Ord: 1, ID: "off-by-one@1", Source: SourceDerived, Patch: "sha256:" + strings.Repeat("b", 64),
				PatchBytes: 20, ResultTree: "git:bbb", Generator: OpOffByOne, Seed: 1,
				Params: "line=x", Expected: ExpectIncorrect},
		},
		OracleDigest: "sha256:" + strings.Repeat("c", 64),
		CanaryID:     "canary-visible",
		PolicyHint:   "testdata/toyrepo/policies/no-paths.json",
		Notes:        []string{"a note"},
	}
}

func TestPublicProjectionCarriesNoSecret(t *testing.T) {
	inst := fullyPopulatedInstance()
	h := fullyPopulatedHidden()
	b, err := object.Canonical(inst.Project())
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	// Every hidden VALUE, by name, must be absent from the projection's bytes.
	secrets := map[string]string{
		"a fail_to_pass node id": h.FailToPass[0],
		"a pass_to_pass node id": h.PassToPass[0],
		"the canary token":       h.CanaryToken,
		"the gold patch":         h.GoldPatch,
		"gold's identity":        h.GoldCandidate,
		"a stripped test path":   h.StrippedHunks[0],
		"the strengthened suite": h.StrengthenedSuite,
		"a hidden file name":     "HIDDEN_FILE_SECRET.py",
		"a hidden file body":     "HIDDEN_BODY_SECRET",
		"the suite argv":         "SUITE_ARGV_SECRET",
	}
	for what, needle := range secrets {
		if strings.Contains(body, needle) {
			t.Errorf("the public projection contains %s (%q):\n%s", what, needle, body)
		}
	}
	// And two values that are harmless in principle but which D1 scans argv
	// for: keeping them out of the file the argv is built from is the cheapest
	// way to keep them out of the argv.
	for what, needle := range map[string]string{
		"the oracle digest": inst.OracleDigest,
		"the canary id":     inst.CanaryID,
	} {
		if strings.Contains(body, needle) {
			t.Errorf("the public projection contains %s (%q)", what, needle)
		}
	}
	// The source tag is withheld too: a `source: S1-gold` tag in a
	// world-readable file is gold's identity in machine-readable form.
	if strings.Contains(body, SourceGold) || strings.Contains(body, SourceDerived) {
		t.Errorf("the public projection carries candidate source tags:\n%s", body)
	}
}

func TestHandoffFieldSetIsClosed(t *testing.T) {
	// A field-by-field assertion over the struct: adding a field to Handoff
	// must fail here, so nobody widens the projection by accident.
	want := map[string]bool{
		"Schema": true, "Instance": true, "BaseCommit": true,
		"EnvImage": true, "Task": true, "Candidates": true,
	}
	ty := reflect.TypeOf(Handoff{})
	got := map[string]bool{}
	for i := 0; i < ty.NumField(); i++ {
		got[ty.Field(i).Name] = true
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("Handoff's field set changed: got %v, want %v.\n"+
			"Widening the public projection is the one change in this package that "+
			"needs an argument, not a commit.", keys(got), keys(want))
	}
	cty := reflect.TypeOf(HandoffCandidate{})
	cwant := map[string]bool{"Ord": true, "File": true, "Patch": true, "PatchBytes": true}
	cgot := map[string]bool{}
	for i := 0; i < cty.NumField(); i++ {
		cgot[cty.Field(i).Name] = true
	}
	if !reflect.DeepEqual(cwant, cgot) {
		t.Errorf("HandoffCandidate's field set changed: got %v, want %v", keys(cgot), keys(cwant))
	}
}

func TestHandoffGoldenJSON(t *testing.T) {
	// A golden file, so a new hidden field cannot be added to Instance and
	// copied into the projection without failing a test.
	inst := fullyPopulatedInstance()
	b, err := object.Canonical(inst.Project())
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "handoff-golden.json")
	if os.Getenv("MVO_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (regenerate with MVO_UPDATE_GOLDEN=1)", err)
	}
	if strings.TrimSpace(string(want)) != strings.TrimSpace(string(b)) {
		t.Errorf("the public projection's canonical bytes moved.\n got: %s\nwant: %s", b, want)
	}
}

func TestValidateNamesEveryProblemAtOnce(t *testing.T) {
	bad := Instance{
		Schema: "wrong", Family: "nope",
		Candidates: []Candidate{
			{Ord: 0, ID: "a", Source: "S9-invented", Patch: "not-a-key"},
			{Ord: 0, ID: "b", Source: SourceGold, Patch: "sha256:x"},
		},
	}
	err := bad.Validate()
	if err == nil {
		t.Fatal("Validate accepted a malformed instance")
	}
	for _, want := range []string{"schema", "empty id", "family", "oracle_digest",
		"duplicate candidate ordinal", "not in the closed vocabulary", "not a CAS key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate did not report %q: %v", want, err)
		}
	}
	if err := fullyPopulatedInstance().Validate(); err != nil {
		t.Errorf("Validate rejected a well-formed instance: %v", err)
	}
}

func TestCandidateJoinIsByResultTree(t *testing.T) {
	inst := fullyPopulatedInstance()
	if c, ok := inst.CandidateByTree("git:bbb"); !ok || c.ID != "off-by-one@1" {
		t.Errorf("CandidateByTree(git:bbb) = (%v, %v)", c.ID, ok)
	}
	if _, ok := inst.CandidateByTree("git:zzz"); ok {
		t.Errorf("CandidateByTree matched an unknown tree")
	}
	// A candidate with no result tree cannot be joined, and must not match
	// the empty string.
	inst.Candidates[0].ResultTree = ""
	if _, ok := inst.CandidateByTree(""); ok {
		t.Errorf("the empty tree matched a candidate")
	}
}

func TestSkipVocabularyIsClosedAndTheCensusIsOrdered(t *testing.T) {
	var c Census
	if !c.Empty() {
		t.Fatal("a fresh census is not empty")
	}
	c.Add("i1", SkipUnstable, "modal below the threshold")
	c.Add("i2", SkipCorpusAbsent, "no corpus")
	c.Add("i3", SkipReason("invented-reason"), "whatever")
	lines := c.Lines()
	if len(lines) != 3 {
		t.Fatalf("census lines = %v", lines)
	}
	// Fixed order: corpus-absent before unstable, invented last and loud.
	if !strings.Contains(lines[0], string(SkipCorpusAbsent)) {
		t.Errorf("census is not in the fixed order: %v", lines)
	}
	if !strings.Contains(lines[2], "INVALID") {
		t.Errorf("a reason outside the vocabulary did not print loudly: %v", lines)
	}
	if c.Fatal() {
		t.Errorf("census is fatal without a fatal reason")
	}
	c.Add("i4", SkipLeakDetected, "canary hit")
	if !c.Fatal() {
		t.Errorf("a leak-detected skip did not make the census fatal")
	}
	if !SkipDigestMismatch.Fatal() || !SkipLeakDetected.Fatal() {
		t.Errorf("the two non-skip reasons are not marked fatal")
	}
	if SkipToolAbsent.Fatal() || SkipImageAbsent.Fatal() {
		t.Errorf("an ordinary skip is marked fatal")
	}
	if SkipReason("nope").Valid() {
		t.Errorf("an invented reason validated")
	}
}

func TestStoreRefusesAWorldReadableHome(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir); err == nil {
		t.Fatalf("OpenStore accepted a world-readable eval home: a hidden oracle anyone can read is not hidden")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir); err != nil {
		t.Fatalf("OpenStore refused a 0700 home: %v", err)
	}
}

func TestDigestLinkIsOneWayAndAMismatchIsHardError(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	h := fullyPopulatedHidden()
	dig, err := store.WriteHidden(CorpusLocalDerived, LocalVersion, h)
	if err != nil {
		t.Fatal(err)
	}
	inst := fullyPopulatedInstance()
	inst.OracleDigest = dig
	if err := store.WriteInstance(inst); err != nil {
		t.Fatal(err)
	}
	got, raw, err := store.LoadHidden(inst)
	if err != nil {
		t.Fatalf("LoadHidden on a matching link: %v", err)
	}
	if got.CanaryToken != h.CanaryToken {
		t.Errorf("round trip lost the canary")
	}
	if DigestBytes(raw) != dig {
		t.Errorf("the raw bytes do not digest to the recorded link")
	}
	// The hidden file must be 0600, not merely intended to be.
	st, err := os.Stat(store.OraclePathFor(inst))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("the hidden oracle is mode %04o, want 0600", st.Mode().Perm())
	}
	// A drifted corpus is a DIFFERENT corpus: hard error, not a skip.
	inst.OracleDigest = "sha256:" + strings.Repeat("f", 64)
	_, _, err = store.LoadHidden(inst)
	if err == nil {
		t.Fatal("LoadHidden accepted a broken digest link")
	}
	if !strings.Contains(err.Error(), string(SkipDigestMismatch)) ||
		!strings.Contains(err.Error(), "hard error") {
		t.Errorf("the mismatch does not name itself as a hard error: %v", err)
	}
}

func TestSplitIsARecordedFunctionAndACuratedListIsDetectable(t *testing.T) {
	salt := "salt"
	s := SplitFile{Schema: SchemaSplit, Corpus: "c", Version: "v", Salt: salt}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		if AssignSplit(salt, id) == SplitDev {
			s.Dev = append(s.Dev, id)
		} else {
			s.Eval = append(s.Eval, id)
		}
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("a computed split does not verify: %v", err)
	}
	// Move one id to the other half: recomputing the HMAC detects it.
	if len(s.Eval) == 0 {
		t.Skip("this salt put every id in dev")
	}
	moved := s.Eval[0]
	s.Eval = s.Eval[1:]
	s.Dev = append(s.Dev, moved)
	err := s.Verify()
	if err == nil {
		t.Fatalf("a hand-picked split verified")
	}
	if !strings.Contains(err.Error(), moved) {
		t.Errorf("the refusal does not name the id that moved: %v", err)
	}
	// An unsalted split is a list, not a split.
	if err := (SplitFile{Schema: SchemaSplit}).Verify(); err == nil {
		t.Errorf("an unsalted split verified")
	}
}

func TestCommittedSplitAndManifestVerify(t *testing.T) {
	// The FILES IN THE REPOSITORY, checked: the split recomputes and the
	// manifest's pinned fixture digests still match the fixtures. A drifted
	// fixture must fail here rather than at 3 a.m. in a scoring run.
	root := repoRoot(t)
	s, err := LoadSplit(filepath.Join(root, "eval", "splits", "local-derived-v1.json"))
	if err != nil {
		t.Fatalf("the committed split does not verify: %v", err)
	}
	ids := map[string]bool{}
	for _, id := range append(append([]string{}, s.Dev...), s.Eval...) {
		ids[id] = true
	}
	for _, id := range LocalInstanceIDs() {
		if !ids[id] {
			t.Errorf("instance %s is in the corpus but in no split half: it would be scored on no split", id)
		}
	}
	m, err := LoadManifest(filepath.Join(root, "eval", "corpora", "local-derived-v1.manifest.json"))
	if err != nil {
		t.Fatalf("the committed manifest does not load: %v", err)
	}
	if m.Network {
		t.Errorf("the local-derived manifest declares network access")
	}
	if len(m.URLs()) != 0 {
		t.Errorf("the local-derived manifest names URLs: %v", m.URLs())
	}
	if err := m.VerifySources(root); err != nil {
		t.Fatalf("%v", err)
	}
	// Every fixture the generator reads is pinned.
	pinned := map[string]bool{}
	for _, s := range m.Sources {
		pinned[s.Path] = true
	}
	for _, p := range LocalSources() {
		if !pinned[p] {
			t.Errorf("fixture %s is read by the generator but not pinned in the manifest", p)
		}
	}
	// And the network template refuses rather than fetching.
	tmpl, err := LoadManifest(filepath.Join(root, "eval", "corpora", "swebench-live-lite.manifest.json"))
	if err != nil {
		t.Fatalf("the swebench-live template does not load: %v", err)
	}
	if !tmpl.Network {
		t.Errorf("the swebench-live template does not declare network access")
	}
	if len(tmpl.Instances) != 0 {
		t.Errorf("the swebench-live template carries instance rows: it must refuse, not fetch")
	}
	if len(tmpl.URLs()) == 0 {
		t.Errorf("the swebench-live template names no URL: the disclosure IS the URL list")
	}
}

func TestFreezeNamesWhatMoved(t *testing.T) {
	root := repoRoot(t)
	fz, err := LoadFreeze(filepath.Join(root, "eval", "freeze", "local-derived-v1.json"))
	if err != nil {
		t.Fatalf("the committed freeze does not load: %v", err)
	}
	// Nothing moved.
	if d := fz.CheckFreeze(fz.PolicyDigest, fz.Constants, nil); len(d) != 0 {
		t.Errorf("an unmoved world reported drift: %+v", d)
	}
	// The committed freeze pins the SHIPPED DEFAULT policy digest. If this
	// fails, the default policy moved and every frozen number is about a
	// different policy.
	if fz.PolicyDigest == "" {
		t.Errorf("the freeze pins no policy digest")
	}
	// A moved policy digest and a moved constant are both named.
	consts := SchedulerConstants()
	consts["executor_bp.candidate-process"] = 1234
	d := fz.CheckFreeze("mv0:moved", consts, nil)
	var what []string
	for _, x := range d {
		what = append(what, x.What)
	}
	joined := strings.Join(what, " ")
	if !strings.Contains(joined, "policy_digest") {
		t.Errorf("drift did not name the policy digest: %v", what)
	}
	if !strings.Contains(joined, "constants.executor_bp.candidate-process") {
		t.Errorf("drift did not name the moved constant: %v", what)
	}
	// An oracle digest the run did not score cannot have drifted.
	fz.OracleDigests = map[string]string{"i1": "sha256:aaa"}
	if d := fz.CheckFreeze(fz.PolicyDigest, fz.Constants, nil); len(d) != 0 {
		t.Errorf("an unscored instance reported oracle drift: %+v", d)
	}
	if d := fz.CheckFreeze(fz.PolicyDigest, fz.Constants,
		map[string]string{"i1": "sha256:bbb"}); len(d) != 1 {
		t.Errorf("a moved oracle digest was not reported: %+v", d)
	}
}

func TestEvalRunsLogAppendsExactlyOneLinePerScoring(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if runs, err := store.ReadEvalRuns("c"); err != nil || len(runs) != 0 {
		t.Fatalf("a missing log is not zero runs: %v %v", runs, err)
	}
	for i := 0; i < 3; i++ {
		if err := store.AppendEvalRun("c", EvalRun{
			BinaryDigest: "sha256:bin", PolicyDigest: "mv0:pol",
			Arms: []string{"A1", "A2"}, InstanceCount: 5, Split: SplitEval,
		}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := store.ReadEvalRuns("c")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Errorf("eval-runs.log holds %d lines after 3 scorings", len(runs))
	}
	if runs[0].TS == "" || runs[0].InstanceCount != 5 {
		t.Errorf("a run line lost its content: %+v", runs[0])
	}
}

func TestRunManifestIsSignedWithAKeyThatIsNotTheAdmissionKey(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	signer, err := LoadOrCreateEvalKey(home)
	if err != nil {
		t.Fatal(err)
	}
	// The key lives under the eval home, never in a workspace's
	// .multiverso/keys: the path itself is the separation.
	if !strings.HasPrefix(EvalKeysDir(home), home) {
		t.Errorf("the eval key dir is not under the eval home")
	}
	if strings.Contains(EvalKeysDir(home), ".multiverso") {
		t.Errorf("the eval key dir is inside a workspace key store: %s", EvalKeysDir(home))
	}
	man := RunManifest{Schema: SchemaRun, Corpus: CorpusLocalDerived}
	_, env, err := SignRunManifest(signer, man)
	if err != nil {
		t.Fatal(err)
	}
	if env.PayloadType != PayloadTypeEvalRun {
		t.Errorf("payload type = %q, want %q", env.PayloadType, PayloadTypeEvalRun)
	}
	// An eval envelope must never verify as an attestation.
	if env.PayloadType == "application/vnd.in-toto+json" {
		t.Errorf("the eval envelope claims to be an in-toto statement")
	}
	// Loading again returns the same key rather than minting a second one.
	again, err := LoadOrCreateEvalKey(home)
	if err != nil {
		t.Fatal(err)
	}
	if again.KeyID != signer.KeyID {
		t.Errorf("a second load minted a different key: %s vs %s", again.KeyID, signer.KeyID)
	}
}

func TestNoMetricLineWhenNothingWasScored(t *testing.T) {
	// Acceptance step m2d-7a's assertion, as a unit test: the ABSENCE of a
	// number is the only thing worth asserting here, so the renderer must not
	// even NAME the two headline metrics on this path.
	var man RunManifest
	man.Schema = SchemaRun
	man.Corpus = CorpusLocalDerived
	man.Version = LocalVersion
	man.Census.Add("i1", SkipCorpusAbsent, "the corpus is absent")
	out := strings.Join(man.Render(), "\n")
	for _, forbidden := range []string{"TCAR", "FAR"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the no-metric report names %q:\n%s", forbidden, out)
		}
	}
	if !strings.Contains(out, "no metric line is printed") {
		t.Errorf("the no-metric report does not say so:\n%s", out)
	}
	// The census prints ABOVE everything, not in a footnote.
	censusAt := strings.Index(out, string(SkipCorpusAbsent))
	noticeAt := strings.Index(out, "no metric line is printed")
	if censusAt < 0 || noticeAt < 0 || censusAt > noticeAt {
		t.Errorf("the census does not print above the metrics:\n%s", out)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// repoRoot finds the repository root from the test's working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Skipf("cannot locate the repository root from %s", dir)
	}
	return dir
}

var _ = json.Marshal
