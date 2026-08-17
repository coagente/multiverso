package eval

import (
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The hidden runner and the probe child run under `python3 -S -B`: no site, so
// no sitecustomize.py from a candidate tree is imported. That hardening also
// means an extension module in lib-dynload is not guaranteed to be importable
// on every interpreter this ships to — CI proved it by failing on `binascii`,
// which is present on developer machines and absent from the runner image, and
// the whole diagnosis reaching us was "empty report".
//
// This test moves that discovery from a remote CI cycle into the suite: every
// module the embedded scripts import must import under exactly the flags they
// run with, on the interpreter running the tests.
func TestHardenedScriptsImportOnlyWhatThisInterpreterHasUnderDashS(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 not on PATH: %v", err)
	}

	// The embedded scripts are Go string literals in this package's sources,
	// so the import lines are recovered from the source text itself. A script
	// added later is covered without anyone remembering to update a list.
	src := packageSources(t)
	plain := regexp.MustCompile(`(?m)^import ([a-z_][a-z0-9_]*)$`)
	from := regexp.MustCompile(`(?m)^from ([a-z_][a-z0-9_.]*) import`)

	seen := map[string]bool{}
	for _, m := range plain.FindAllStringSubmatch(src, -1) {
		seen[m[1]] = true
	}
	for _, m := range from.FindAllStringSubmatch(src, -1) {
		seen[m[1]] = true
	}
	if len(seen) == 0 {
		t.Fatal("found no python import lines in this package's sources: the recovery regexps have drifted from how the scripts are written, so this test is not checking anything")
	}

	mods := make([]string, 0, len(seen))
	for m := range seen {
		mods = append(mods, m)
	}
	sort.Strings(mods)

	for _, mod := range mods {
		cmd := exec.Command(python, "-S", "-B", "-c", "import "+mod)
		cmd.Env = HiddenEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("the hardened scripts import %q, which does not import under `python3 -S -B` with the labeller's closed environment: %v\n%s", mod, err, strings.TrimSpace(string(out)))
		}
	}
}

// packageSources concatenates this package's .go sources so the embedded
// python can be scanned as text.
func packageSources(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}", ".").Output()
	if err != nil {
		t.Fatalf("locate package dir: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	entries, err := exec.Command("sh", "-c", "cat "+dir+"/*.go").Output()
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}
	return string(entries)
}
