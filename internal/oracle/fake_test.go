package oracle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/cas"
	"github.com/coagente/multiverso/internal/policy"
)

// fakePython is a stand-in interpreter: a shell script that answers the
// tools probe, writes the native artifacts a real run would write, and
// exits with a chosen code. It exists so the ladder's behavior — argv,
// artifact pipeline, status mapping, metric parsing — is tested WITHOUT
// requiring pytest or any plugin to be installed (M1b's rule: tests never
// invoke real tools they cannot pin, and the design's rule: no test may
// require a plugin).
type fakePython struct {
	probe        map[string]string // what the tools probe reports
	probeExit    int               // non-zero ⇒ the probe could not run
	stdout       string
	stderr       string
	exit         int
	junit        string // written to the --junit-xml path when non-empty
	reportlog    string // written to the --report-log path when non-empty
	coverageJSON string // written by `-m coverage json -o PATH`
	coverageExit int
	sleep        int // seconds to sleep before the main run (timeout tests)
	// stream is the evidence stream body the fake writes to
	// $MVO_EVIDENCE_STREAM: the RECORDS ONLY, with the header supplied by
	// the fake so the nonce is always this run's. Empty means "write
	// nothing", which is how a candidate that silenced the plugin is
	// reproduced without any plugin being involved.
	stream string
	// headerNonce overrides the header's nonce (the replayed-stream case).
	headerNonce string
	// noHeader writes the records with no header at all.
	noHeader bool
}

// write emits the script and returns its path. The script dispatches on the
// invocation shape the oracles use: `-c <script>` is the probe, `-m
// coverage json -o PATH` is the report extraction, anything else is the
// run itself.
func (f fakePython) write(t *testing.T) string {
	t.Helper()
	probeJSON := ""
	if f.probe != nil {
		b, err := json.Marshal(f.probe)
		if err != nil {
			t.Fatalf("marshal probe: %v", err)
		}
		probeJSON = string(b)
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# fake python3 for oracle tests: not an interpreter, not a CLI.\n")
	b.WriteString("if [ \"$1\" = \"-c\" ]; then\n")
	b.WriteString(heredoc("", probeJSON, "MVO_PROBE"))
	fmt.Fprintf(&b, "exit %d\nfi\n", f.probeExit)
	b.WriteString("if [ \"$2\" = \"coverage\" ] && [ \"$3\" = \"json\" ]; then\n")
	b.WriteString(heredoc("> \"$5\"", f.coverageJSON, "MVO_COV"))
	fmt.Fprintf(&b, "exit %d\nfi\n", f.coverageExit)
	b.WriteString("junit_path=\"\"\nrl_path=\"\"\n")
	b.WriteString("for a in \"$@\"; do\n")
	b.WriteString("case \"$a\" in\n")
	b.WriteString("--junit-xml=*) junit_path=${a#--junit-xml=} ;;\n")
	b.WriteString("--report-log=*) rl_path=${a#--report-log=} ;;\n")
	b.WriteString("esac\ndone\n")
	if f.sleep > 0 {
		fmt.Fprintf(&b, "sleep %d\n", f.sleep)
	}
	if f.junit != "" {
		b.WriteString("if [ -n \"$junit_path\" ]; then\n")
		b.WriteString(heredoc("> \"$junit_path\"", f.junit, "MVO_JUNIT"))
		b.WriteString("fi\n")
	}
	if f.reportlog != "" {
		b.WriteString("if [ -n \"$rl_path\" ]; then\n")
		b.WriteString(heredoc("> \"$rl_path\"", f.reportlog, "MVO_RL"))
		b.WriteString("fi\n")
	}
	if f.stream != "" || f.noHeader {
		// The control-plane plugin's part, played by a shell script: the
		// framed stream over the FIFO the control plane created.
		b.WriteString("if [ -n \"$MVO_EVIDENCE_STREAM\" ]; then\n")
		if !f.noHeader {
			nonce := "$MVO_EVIDENCE_NONCE"
			if f.headerNonce != "" {
				nonce = f.headerNonce
			}
			fmt.Fprintf(&b, "printf 'mvo-evidence/v0\\t%%s\\n' \"%s\" >> \"$MVO_EVIDENCE_STREAM\"\n", nonce)
		}
		b.WriteString(heredoc(">> \"$MVO_EVIDENCE_STREAM\"", f.stream, "MVO_STREAM"))
		b.WriteString("fi\n")
	}
	b.WriteString(heredoc("", f.stdout, "MVO_OUT"))
	b.WriteString(heredoc(">&2", f.stderr, "MVO_ERR"))
	fmt.Fprintf(&b, "exit %d\n", f.exit)

	path := filepath.Join(t.TempDir(), "python3")
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	return path
}

// heredoc emits content through a QUOTED here-document (nothing in it is
// ever interpreted by the shell), optionally redirected: "" is stdout,
// ">&2" is stderr, "> \"$5\"" is a file named by an argument.
func heredoc(redirect, content, tag string) string {
	if content == "" {
		return ""
	}
	if redirect != "" {
		redirect = " " + redirect
	}
	return fmt.Sprintf("cat%s <<'%s'\n%s\n%s\n", redirect, tag, content, tag)
}

// newStore opens a throwaway CAS.
func newStore(t *testing.T) *cas.Store {
	t.Helper()
	store, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatalf("cas.Open: %v", err)
	}
	return store
}

// recordingStore records the order in which bytes reach CAS and can fail on
// demand: the EP-7 proof is that every artifact is stored BEFORE any of it
// is parsed, so a store that fails at artifact N must yield no receipt and
// no metrics at all.
type recordingStore struct {
	real   *cas.Store
	puts   [][]byte
	failOn int // 1-based index of the Put that fails; 0 = never fail
}

func (s *recordingStore) Put(b []byte) (string, error) {
	s.puts = append(s.puts, append([]byte(nil), b...))
	if s.failOn > 0 && len(s.puts) == s.failOn {
		return "", fmt.Errorf("recordingStore: injected failure on put %d", s.failOn)
	}
	return s.real.Put(b)
}

// testSpec is a resolved spec with the config digest a compiled policy
// would have assigned.
func testSpec(kind, python string) policy.Oracle {
	s := policy.Oracle{
		Name:   "o",
		Kind:   kind,
		Family: Family(kind),
		Config: "mv0:" + strings.Repeat("7", 64),
	}
	if python != "" {
		s.Argv = []string{python, "-m", "pytest"}
	}
	return s
}
