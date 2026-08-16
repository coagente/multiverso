package oracle

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// mutmutTool is the second adapter, kept for repositories where mutmut's
// own diff scoping is close enough — and labelled, in the receipt, as
// STRICTLY WEAKER provenance (M2a decision 19).
//
// The difference is not a matter of taste. mutmut enumerates and selects
// its own mutants, reporting ids without positions, so the control plane
// cannot apply the per-line cap and cannot order the population by
// (path, line, col, operator, digest). What it CAN do is bound the run: the
// ids are taken in ascending numeric order and the first max_mutants of
// them are executed, which is deterministic and replayable even though the
// order is the tool's rather than ours. Receipts record
// inputs["mutant_selection"] = "tool" so a reader is never invited to
// assume the stronger reading.
type mutmutTool struct{}

// Name implements MutationTool.
func (mutmutTool) Name() string { return ToolMutmut }

// Selection implements MutationTool.
func (mutmutTool) Selection() string { return SelectionTool }

// mutmutCLI is driven through the interpreter for the same reason
// cosmic-ray is: a repository pinned to a virtualenv must be mutated by
// that virtualenv's tool.
const mutmutCLI = "mutmut"

// Config implements MutationTool: mutmut is configured entirely on argv, so
// there is no file — and, deliberately, no cache setting. Its cache is an
// evidence store; ours lives in scratch and is thrown away (decision 18).
func (mutmutTool) Config(TargetSet, MutationRun) []byte { return nil }

// EnumerateSteps is a POPULATION PASS followed by a listing.
//
// The population pass runs mutmut with a no-op runner: it generates the
// mutants over the patch-scoped paths and records them without ever
// executing the suite, so enumeration costs no suite runs. Every mutant is
// then listed as "survived" — nothing killed them, because nothing ran —
// which is exactly the enumeration we want and is why the outcomes from
// this pass are DISCARDED rather than counted.
func (mutmutTool) EnumerateSteps(python string, t TargetSet, run MutationRun) []MutationStep {
	populate := []string{python, "-m", mutmutCLI, "run",
		"--paths-to-mutate", strings.Join(run.Paths, ","),
		"--use-patch-file", run.PatchPath,
		"--runner", "true", // the no-op runner: enumerate, execute nothing
		"--simple-output", "--no-progress",
	}
	list := []string{python, "-m", mutmutCLI, "result-ids", "survived"}
	return []MutationStep{
		{Argv: withMutmutCache(populate, run)},
		{Argv: withMutmutCache(list, run), Parse: true},
	}
}

// ExecArgv runs ONE mutant by id, under the real suite command.
func (mutmutTool) ExecArgv(python string, m Mutant, run MutationRun) []string {
	argv := []string{python, "-m", mutmutCLI, "run", m.Ref,
		"--runner", shellJoin(run.TestArgv),
		"--simple-output", "--no-progress",
	}
	return withMutmutCache(argv, run)
}

// withMutmutCache pins the tool's state directory into control-plane
// scratch. mutmut writes `.mutmut-cache` in its working directory by
// default — which is the candidate's tree, where a planted cache asserting
// "all mutants killed" is a two-line forgery (corpus vector 15). The
// environment variable is set by the oracle; the flag is here so the path
// is also visible in execution.argv, where a reader can see it.
func withMutmutCache(argv []string, run MutationRun) []string {
	if run.SessionDir == "" {
		return argv
	}
	return append(argv, "--cache-path", path.Join(run.SessionDir, "mutmut-cache"))
}

// ParseEnumeration reads `mutmut result-ids` output: whitespace-separated
// integer ids.
//
// PURE, and tested over recorded output with no tool installed. A token
// that is not an id is skipped; output with no id at all is an error rather
// than an empty enumeration, because "mutmut generated nothing" and "we did
// not understand mutmut" must not produce the same zero.
func (mutmutTool) ParseEnumeration(stdout []byte) ([]Mutant, error) {
	out := []Mutant{}
	junk := 0
	for _, tok := range strings.Fields(string(stdout)) {
		if _, err := strconv.ParseInt(tok, 10, 64); err != nil {
			junk++
			continue
		}
		// No path, no line, no column: mutmut reports ids. The zero
		// position is what FilterToTarget and the report read as "tool
		// selection", and it is why max_per_line cannot be applied here.
		out = append(out, Mutant{Operator: "mutmut", Ref: tok}.WithDigest())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no mutant id in `mutmut result-ids` output (%d unrecognized token(s))", junk)
	}
	return out, nil
}

// ParseExec implements MutationTool: mutmut's `run` prints progress, not a
// per-mutant verdict, so there is no claim to read and no diff to store
// without a second `mutmut show <id>` invocation. The outcome comes from
// the evidence stream either way; what is lost is the survivor's diff in
// the report, which is a real cost of this adapter and is stated rather
// than papered over.
func (mutmutTool) ParseExec([]byte) (ToolClaim, error) { return ToolClaim{}, nil }
