package gitx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initBare creates a bare repo and registers it as remote "origin" of repo.
func initBare(t *testing.T, repo string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, bare, "init", "-q", "--bare")
	git(t, repo, "remote", "add", "origin", bare)
	return bare
}

func TestHashObjectMktreeCommitTreeEpochDeterminism(t *testing.T) {
	repo := initRepo(t)
	blob1, err := HashObject(repo, []byte("evidence\n"))
	if err != nil {
		t.Fatalf("HashObject: %v", err)
	}
	blob2, err := HashObject(repo, []byte("evidence\n"))
	if err != nil {
		t.Fatalf("HashObject: %v", err)
	}
	if blob1 != blob2 {
		t.Errorf("HashObject not deterministic: %s vs %s", blob1, blob2)
	}
	if !shaRe.MatchString(blob1) {
		t.Errorf("blob sha = %q, want 40-hex", blob1)
	}

	entries := []TreeEntry{{Mode: "100644", Type: "blob", SHA: blob1, Name: "a.json"}}
	tree1, err := Mktree(repo, entries)
	if err != nil {
		t.Fatalf("Mktree: %v", err)
	}
	tree2, err := Mktree(repo, entries)
	if err != nil {
		t.Fatalf("Mktree: %v", err)
	}
	if tree1 != tree2 {
		t.Errorf("Mktree not deterministic: %s vs %s", tree1, tree2)
	}

	head := git(t, repo, "rev-parse", "HEAD")
	c1, err := CommitTreeEpoch(repo, tree1, head, "msg\n\nTrailer: x\n")
	if err != nil {
		t.Fatalf("CommitTreeEpoch: %v", err)
	}
	c2, err := CommitTreeEpoch(repo, tree1, head, "msg\n\nTrailer: x\n")
	if err != nil {
		t.Fatalf("CommitTreeEpoch: %v", err)
	}
	if c1 != c2 {
		t.Errorf("CommitTreeEpoch not deterministic: %s vs %s", c1, c2)
	}
	raw := git(t, repo, "cat-file", "commit", c1)
	if !strings.Contains(raw, "author mvo <mvo@multiverso.invalid> 0 +0000") ||
		!strings.Contains(raw, "committer mvo <mvo@multiverso.invalid> 0 +0000") {
		t.Errorf("epoch commit does not pin identity+epoch:\n%s", raw)
	}
}

func TestCatBlobRawFidelity(t *testing.T) {
	repo := initRepo(t)
	// Trailing newlines and NULs must survive verbatim — the bytes are
	// evidence.
	data := []byte("payload with trailing bytes\n\n\x00tail\n")
	sha, err := HashObject(repo, data)
	if err != nil {
		t.Fatalf("HashObject: %v", err)
	}
	got, err := CatBlob(repo, sha)
	if err != nil {
		t.Fatalf("CatBlob: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("CatBlob = %q, want %q", got, data)
	}
}

func TestLsTreeRecursivePaths(t *testing.T) {
	repo := initRepo(t)
	blob, err := HashObject(repo, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	sub, err := Mktree(repo, []TreeEntry{
		{Mode: "100644", Type: "blob", SHA: blob, Name: "one.json"},
		{Mode: "100644", Type: "blob", SHA: blob, Name: "two.json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := Mktree(repo, []TreeEntry{
		{Mode: "040000", Type: "tree", SHA: sub, Name: "receipts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := LsTreeRecursive(repo, root)
	if err != nil {
		t.Fatalf("LsTreeRecursive: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Name != "receipts/one.json" || entries[1].Name != "receipts/two.json" {
		t.Errorf("paths = %q, %q", entries[0].Name, entries[1].Name)
	}
	for _, e := range entries {
		if e.Type != "blob" || e.SHA != blob {
			t.Errorf("entry %+v, want blob %s", e, blob)
		}
	}
}

func TestRefValueAbsentAndPresent(t *testing.T) {
	repo := initRepo(t)
	got, err := RefValue(repo, "refs/multiverso/intent/abc/evidence")
	if err != nil || got != "" {
		t.Errorf("RefValue(absent) = (%q, %v), want (\"\", nil)", got, err)
	}
	head := git(t, repo, "rev-parse", "HEAD")
	if err := UpdateRef(repo, "refs/multiverso/intent/abc/cand/1", head, ""); err != nil {
		t.Fatalf("UpdateRef create: %v", err)
	}
	got, err = RefValue(repo, "refs/multiverso/intent/abc/cand/1")
	if err != nil || got != head {
		t.Errorf("RefValue(present) = (%q, %v), want (%q, nil)", got, err, head)
	}
}

func TestDeleteRefCAS(t *testing.T) {
	repo := initRepo(t)
	head := git(t, repo, "rev-parse", "HEAD")
	ref := "refs/multiverso/intent/abc/cand/1"
	if err := UpdateRef(repo, ref, head, ""); err != nil {
		t.Fatal(err)
	}
	// Wrong old value: the compare-and-swap must refuse.
	wrong := strings.Repeat("1", 40)
	if err := DeleteRef(repo, ref, wrong); err == nil {
		t.Fatal("DeleteRef with a wrong old value succeeded")
	}
	if got, _ := RefValue(repo, ref); got != head {
		t.Fatalf("ref moved after refused delete: %q", got)
	}
	if err := DeleteRef(repo, ref, head); err != nil {
		t.Fatalf("DeleteRef: %v", err)
	}
	if got, _ := RefValue(repo, ref); got != "" {
		t.Errorf("ref survives delete: %q", got)
	}
}

func TestForEachRef(t *testing.T) {
	repo := initRepo(t)
	head := git(t, repo, "rev-parse", "HEAD")
	refs, err := ForEachRef(repo, "refs/multiverso")
	if err != nil {
		t.Fatalf("ForEachRef: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("empty namespace = %v, want none", refs)
	}
	for _, ref := range []string{
		"refs/multiverso/intent/abc/cand/1",
		"refs/multiverso/intent/abc/evidence",
	} {
		if err := UpdateRef(repo, ref, head, ""); err != nil {
			t.Fatal(err)
		}
	}
	refs, err = ForEachRef(repo, "refs/multiverso/intent/abc")
	if err != nil {
		t.Fatalf("ForEachRef: %v", err)
	}
	if len(refs) != 2 || refs["refs/multiverso/intent/abc/cand/1"] != head ||
		refs["refs/multiverso/intent/abc/evidence"] != head {
		t.Errorf("refs = %v", refs)
	}
}

func TestPushLsRemoteFetchLifecycle(t *testing.T) {
	repo := initRepo(t)
	bare := initBare(t, repo)
	head := git(t, repo, "rev-parse", "HEAD")
	ref := "refs/multiverso/intent/abc/cand/1"

	// Create with an expect-absent lease.
	if err := Push(repo, "origin", []string{head + ":" + ref}, map[string]string{ref: ""}); err != nil {
		t.Fatalf("Push create: %v", err)
	}
	remote, err := LsRemote(repo, "origin", "refs/multiverso/*")
	if err != nil {
		t.Fatalf("LsRemote: %v", err)
	}
	if remote[ref] != head {
		t.Fatalf("remote = %v, want %s at %s", remote, ref, head)
	}

	// A second epoch commit to move the ref to.
	tree := git(t, repo, "rev-parse", "HEAD^{tree}")
	next, err := CommitTreeEpoch(repo, tree, head, "moved\n")
	if err != nil {
		t.Fatal(err)
	}

	// Lease failure: the remote holds head, the lease expects something
	// else — a concurrent publisher must surface loudly, never clobber.
	wrong := strings.Repeat("1", 40)
	if err := Push(repo, "origin", []string{next + ":" + ref}, map[string]string{ref: wrong}); err == nil {
		t.Fatal("Push with a stale lease succeeded")
	}
	if remote, _ = LsRemote(repo, "origin", "refs/multiverso/*"); remote[ref] != head {
		t.Fatalf("remote moved despite the failed lease: %v", remote)
	}
	// Correct lease: the update lands (force-with-lease covers non-FF).
	if err := Push(repo, "origin", []string{next + ":" + ref}, map[string]string{ref: head}); err != nil {
		t.Fatalf("Push update: %v", err)
	}

	// Consumer fetch + prune mirror.
	consumer := t.TempDir()
	git(t, consumer, "init", "-q")
	git(t, consumer, "remote", "add", "origin", bare)
	spec := "+refs/multiverso/intent/abc/*:refs/multiverso/intent/abc/*"
	if err := Fetch(consumer, "origin", []string{spec}, true); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got, _ := RefValue(consumer, ref); got != next {
		t.Fatalf("consumer ref = %q, want %q", got, next)
	}
	// Delete refspec with lease; then a pruning fetch drops the mirror.
	if err := Push(repo, "origin", []string{":" + ref}, map[string]string{ref: next}); err != nil {
		t.Fatalf("Push delete: %v", err)
	}
	if remote, _ = LsRemote(repo, "origin", "refs/multiverso/*"); len(remote) != 0 {
		t.Fatalf("remote after delete = %v, want empty", remote)
	}
	if err := Fetch(consumer, "origin", []string{spec}, true); err != nil {
		t.Fatalf("Fetch --prune: %v", err)
	}
	if got, _ := RefValue(consumer, ref); got != "" {
		t.Errorf("consumer still holds pruned ref: %q", got)
	}
}

// git ls-remote tail-matches its pattern from any slash boundary, so a
// branch or tag whose NAME merely contains the namespace path comes back
// from an unanchored survey — and the callers turn survey output into
// delete refspecs.
func TestLsRemoteAnchorsToPattern(t *testing.T) {
	repo := initRepo(t)
	initBare(t, repo)
	head := git(t, repo, "rev-parse", "HEAD")
	ns := "refs/multiverso/intent/abc123abc123"
	inside := ns + "/cand/1"
	outside := []string{
		"refs/heads/" + ns + "/wip",
		"refs/tags/release/" + ns + "/v1",
	}
	git(t, repo, "push", "-q", "origin", head+":"+inside)
	for _, ref := range outside {
		git(t, repo, "push", "-q", "origin", head+":"+ref)
	}

	refs, err := LsRemote(repo, "origin", ns+"/*")
	if err != nil {
		t.Fatalf("LsRemote: %v", err)
	}
	if len(refs) != 1 || refs[inside] != head {
		t.Errorf("LsRemote returned out-of-namespace refs: %v", refs)
	}
	// The raw plumbing really does return them — the anchor is load-bearing,
	// not defensive noise.
	raw := git(t, repo, "ls-remote", "origin", ns+"/*")
	for _, ref := range outside {
		if !strings.Contains(raw, ref) {
			t.Fatalf("fixture is not exercising tail-matching: %q missing from\n%s", ref, raw)
		}
	}
}

func TestPushRefusesOutOfNamespaceDestination(t *testing.T) {
	repo := initRepo(t)
	initBare(t, repo)
	head := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "push", "-q", "origin", head+":refs/heads/main")

	for _, spec := range []string{":refs/heads/main", head + ":refs/heads/main", "main"} {
		err := Push(repo, "origin", []string{spec}, map[string]string{"refs/heads/main": head})
		if err == nil || !strings.Contains(err.Error(), "outside refs/multiverso/") {
			t.Errorf("Push(%q) = %v, want a namespace refusal", spec, err)
		}
	}
	if refs, _ := LsRemote(repo, "origin", "refs/heads/*"); refs["refs/heads/main"] != head {
		t.Errorf("refused push still moved refs/heads/main: %v", refs)
	}
}

// A mixed update+delete batch with one stale lease must land NOTHING:
// publish and prune record push outcomes in an append-only ledger, so a
// partially applied batch is a permanently wrong record.
func TestPushIsAtomic(t *testing.T) {
	repo := initRepo(t)
	initBare(t, repo)
	head := git(t, repo, "rev-parse", "HEAD")
	ns := "refs/multiverso/intent/abc123abc123"
	stale, fresh := ns+"/cand/1", ns+"/cand/2"
	git(t, repo, "push", "-q", "origin", head+":"+stale)

	tree := git(t, repo, "rev-parse", "HEAD^{tree}")
	next, err := CommitTreeEpoch(repo, tree, head, "moved\n")
	if err != nil {
		t.Fatal(err)
	}
	// cand/1's lease is stale; cand/2 would create cleanly on its own.
	err = Push(repo, "origin",
		[]string{next + ":" + fresh, next + ":" + stale},
		map[string]string{stale: strings.Repeat("1", 40), fresh: ""})
	if err == nil {
		t.Fatal("push with a stale lease succeeded")
	}
	refs, lsErr := LsRemote(repo, "origin", ns+"/*")
	if lsErr != nil {
		t.Fatal(lsErr)
	}
	if len(refs) != 1 || refs[stale] != head {
		t.Errorf("failed batch landed something: %v", refs)
	}
}

func TestMergeBase(t *testing.T) {
	repo := initRepo(t)
	first := git(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "second")
	second := git(t, repo, "rev-parse", "HEAD")
	base, err := MergeBase(repo, first, second)
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if base != first {
		t.Errorf("MergeBase = %q, want %q", base, first)
	}

	// A disjoint root: no common ancestor is ("", nil), not an error.
	git(t, repo, "checkout", "-q", "--orphan", "island")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "island root")
	island := git(t, repo, "rev-parse", "HEAD")
	base, err = MergeBase(repo, first, island)
	if err != nil || base != "" {
		t.Errorf("MergeBase(disjoint) = (%q, %v), want (\"\", nil)", base, err)
	}
}

func TestCommitExists(t *testing.T) {
	repo := initRepo(t)
	head := git(t, repo, "rev-parse", "HEAD")
	if !CommitExists(repo, head) {
		t.Errorf("CommitExists(HEAD) = false")
	}
	if CommitExists(repo, strings.Repeat("1", 40)) {
		t.Errorf("CommitExists(bogus) = true")
	}
}

func TestRemotePushRefspecsAndURL(t *testing.T) {
	repo := initRepo(t)
	bare := initBare(t, repo)

	specs, err := RemotePushRefspecs(repo, "origin")
	if err != nil || specs != nil {
		t.Errorf("RemotePushRefspecs(unset) = (%v, %v), want (nil, nil)", specs, err)
	}
	git(t, repo, "config", "--add", "remote.origin.push", "refs/heads/*:refs/heads/*")
	git(t, repo, "config", "--add", "remote.origin.push", "+refs/*:refs/*")
	specs, err = RemotePushRefspecs(repo, "origin")
	if err != nil {
		t.Fatalf("RemotePushRefspecs: %v", err)
	}
	if len(specs) != 2 || specs[0] != "refs/heads/*:refs/heads/*" || specs[1] != "+refs/*:refs/*" {
		t.Errorf("specs = %v", specs)
	}

	url, err := RemoteURL(repo, "origin")
	if err != nil || url != bare {
		t.Errorf("RemoteURL = (%q, %v), want (%q, nil)", url, err, bare)
	}
	if _, err := RemoteURL(repo, "nosuch"); err == nil {
		t.Errorf("RemoteURL(nosuch) succeeded")
	}
}
