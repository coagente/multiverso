package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/workspace"
)

// cmdPolicy dispatches the policy verbs (CP-5). A policy is an authored,
// versioned, content-addressed artifact: these verbs inspect and install
// them, and never mutate one — editing a file and re-installing it mints a
// NEW digest, so intents pinned to the old one keep replaying against it.
func cmdPolicy(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("usage: mvo policy list|show|validate|use [args] [--dir DIR]")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "list":
		return cmdPolicyList(rest, stdout, stderr)
	case "show":
		return cmdPolicyShow(rest, stdout, stderr)
	case "validate":
		return cmdPolicyValidate(rest, stdout, stderr)
	case "use":
		return cmdPolicyUse(rest, stdout, stderr)
	default:
		return usagef("policy: unknown verb %q (want list, show, validate, use)", verb)
	}
}

// policyRow is one row of `mvo policy list`.
type policyRow struct {
	Name   string
	Digest string
	Schema string
	Gates  string
	Rank   string
	State  string
}

func cmdPolicyList(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("policy list", stderr)
	dir := fs.String("dir", ".", "repository directory")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("policy list: %w", err)
	}
	defer ws.Close()
	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("policy list: %w", err)
	}

	recorded := make(map[string]bool, len(st.Policies))
	rows := make(map[string]policyRow)
	addRow := func(name, dig string, b []byte, fromFile bool) {
		pol, err := policy.Decode(b)
		row := policyRow{Name: name, Digest: dig, Schema: "?", Gates: "?", Rank: "?"}
		if err == nil {
			if row.Name == "" {
				row.Name = pol.Name
			}
			row.Schema = policy.SchemaShort(pol.Schema)
			row.Gates = strings.Join(pol.GateLabels(), ",")
			row.Rank = strings.Join(pol.KeyNames(), ",")
		} else {
			// An unreadable file is still a fact about the workspace; it is
			// listed, named by what it is, not silently dropped.
			row.Schema = "invalid"
		}
		if row.Name == "" {
			row.Name = "-"
		}
		// A file-backed row wins: its stem is the name `policy show` and
		// `policy use` take, whatever the document calls itself.
		if _, ok := rows[dig]; ok && !fromFile {
			return
		}
		rows[dig] = row
	}

	for _, pr := range st.Policies {
		recorded[pr.Dig] = true
		addRow("", pr.Dig, pr.Bytes, false)
	}
	entries, err := os.ReadDir(ws.PoliciesDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("policy list: read %s: %w", ws.PoliciesDir(), err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(ws.PoliciesDir(), e.Name()))
		if err != nil {
			return fmt.Errorf("policy list: %w", err)
		}
		addRow(strings.TrimSuffix(e.Name(), ".json"), object.DigestBytes(b), b, true)
	}

	digs := make([]string, 0, len(rows))
	for dig := range rows {
		digs = append(digs, dig)
	}
	sort.Strings(digs)

	tw := tabwriter.NewWriter(stdout, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDIGEST\tSCHEMA\tGATES\tRANKING\tSTATE")
	for _, dig := range digs {
		row := rows[dig]
		// A file whose bytes no longer digest to a recorded policy is the
		// honest "you edited this; nothing has used it yet".
		switch {
		case recorded[dig] && dig == ws.Config.DefaultPolicy:
			row.State = "recorded (default)"
		case recorded[dig]:
			row.State = "recorded"
		default:
			row.State = "unrecorded (file only)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Name, row.Digest, row.Schema, row.Gates, row.Rank, row.State)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("policy list: %w", err)
	}
	writeAdoptionHint(stdout, ws)
	return nil
}

// writeAdoptionHint says so, in words, when the workspace default predates
// this binary's built-in default (M1f decision 21). Upgrading the binary
// does NOT rewrite .multiverso/policies/default.json — mutating an
// operator's pinned artifact behind their back is precisely the thing
// content addressing exists to prevent — so adoption is two explicit
// commands, and this is where they are printed.
func writeAdoptionHint(w io.Writer, ws *workspace.Workspace) {
	shipped, err := builtinPolicy("default")
	if err != nil {
		return
	}
	current := ws.Config.DefaultPolicy
	// The ONLY thing that ends adoption is the workspace actually racing
	// under the shipped digest. Keying the early return on the file as well
	// silently dropped the hint for an operator who had run the first of
	// the two commands and not the second — exactly halfway, still racing
	// under the old pinned digest, and no longer told so.
	if current == "" || current == shipped.Digest {
		return
	}
	file := ws.PolicyFile("default")
	remainder := fmt.Sprintf("mvo policy show default --builtin --json > %s && mvo policy use default", file)
	if b, err := os.ReadFile(file); err == nil && object.DigestBytes(b) == shipped.Digest {
		// The file is already the shipped policy; only the pin is stale.
		remainder = "mvo policy use default"
	}
	fmt.Fprintf(w, "mvo: policy list: the workspace default %s predates this binary's built-in default %s\n",
		current, shipped.Digest)
	fmt.Fprintf(w, "     (adds paths-unmodified@guard, collect-equals-suite-total, on_ranking_tie; drops wall_ms_asc)\n")
	fmt.Fprintf(w, "     adopt with: %s\n", remainder)
}

// resolvedPolicy is a policy found by reference, with the bytes it was
// found as. Bytes are never re-serialized from the compiled form: the
// digest is the identity, and only these bytes have it.
type resolvedPolicy struct {
	Digest string
	Bytes  []byte
	Pol    policy.Policy
	Source string // "cas" | "<file>" | "ledger"
}

// resolvePolicy resolves a reference deterministically: a full "mv0:"
// digest names CAS; otherwise the file .multiverso/policies/<name>.json;
// otherwise the LATEST ledger-recorded policy whose in-document name is
// <name>. `mvo policy show` and `mvo intent new --policy` share it, so a
// reference means the same thing wherever it is typed.
func resolvePolicy(ws *workspace.Workspace, st *ledgerState, ref string) (resolvedPolicy, error) {
	if strings.HasPrefix(ref, object.DigestPrefix) {
		b, err := ws.GetObject(ref)
		if err != nil {
			return resolvedPolicy{}, fmt.Errorf("no policy %s in CAS: %w", ref, err)
		}
		pol, err := policy.Decode(b)
		if err != nil {
			return resolvedPolicy{}, err
		}
		return resolvedPolicy{Digest: ref, Bytes: b, Pol: pol, Source: "cas"}, nil
	}
	path := ws.PolicyFile(ref)
	if b, err := os.ReadFile(path); err == nil {
		pol, err := policy.Decode(b)
		if err != nil {
			return resolvedPolicy{}, fmt.Errorf("%s: %w", path, err)
		}
		return resolvedPolicy{Digest: object.DigestBytes(b), Bytes: b, Pol: pol, Source: path}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return resolvedPolicy{}, fmt.Errorf("read %s: %w", path, err)
	}
	for i := len(st.Policies) - 1; i >= 0; i-- {
		pr := st.Policies[i]
		pol, err := policy.Decode(pr.Bytes)
		if err != nil || pol.Name != ref {
			continue
		}
		return resolvedPolicy{Digest: pr.Dig, Bytes: pr.Bytes, Pol: pol, Source: "ledger"}, nil
	}
	return resolvedPolicy{}, fmt.Errorf("no policy %q: not a digest, no %s, and no recorded policy by that name",
		ref, path)
}

func cmdPolicyShow(args []string, stdout, stderr io.Writer) error {
	refArg, rest := splitDigestArg(args)
	fs := newFlagSet("policy show", stderr)
	dir := fs.String("dir", ".", "repository directory")
	jsonOut := fs.Bool("json", false, "print only the canonical policy bytes")
	// --builtin prints the BINARY's policy of that name regardless of what
	// the workspace file says. This is how M1f decision 21's adoption
	// two-liner is written down, and how an operator diffs their pinned
	// default against the shipped one: upgrading the binary must never
	// rewrite an operator's pinned artifact behind their back.
	builtin := fs.Bool("builtin", false, "print this binary's built-in policy of that name")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if refArg == "" && len(fs.Args()) > 0 {
		refArg = fs.Args()[0]
	}
	if refArg == "" {
		return usagef("policy show: a policy name or digest is required")
	}

	var rp resolvedPolicy
	if *builtin {
		var err error
		if rp, err = builtinPolicy(refArg); err != nil {
			return fmt.Errorf("policy show: %w", err)
		}
	} else {
		ws, err := workspace.Open(*dir)
		if err != nil {
			return fmt.Errorf("policy show: %w", err)
		}
		defer ws.Close()
		st, err := loadState(ws.Ledger)
		if err != nil {
			return fmt.Errorf("policy show: %w", err)
		}
		if rp, err = resolvePolicy(ws, st, refArg); err != nil {
			return fmt.Errorf("policy show: %w", err)
		}
	}
	// --json prints ONLY the policy's bytes, with no trailing newline of our
	// own: the round trip
	//   mvo policy show default --json > .multiverso/policies/mine.json
	// must produce a file that digests to exactly the policy shown, and a
	// byte we add is a byte that forks the digest.
	if *jsonOut {
		fmt.Fprintf(stdout, "%s", rp.Bytes)
		return nil
	}
	writePolicySummary(stdout, rp.Pol)
	// Which of the three resolution paths answered — provenance, so an
	// operator can tell a file they are editing from the bytes that are
	// actually recorded.
	fmt.Fprintf(stdout, "source:    %s\n", rp.Source)
	fmt.Fprintf(stdout, "%s\n", rp.Bytes)
	return nil
}

// builtinPolicy renders the binary's own policy of that name. Only the
// shipped default has one: `command` is synthesized per intent from an
// argv, so there is nothing constant to print.
func builtinPolicy(name string) (resolvedPolicy, error) {
	if name != "default" {
		return resolvedPolicy{}, fmt.Errorf("no built-in policy %q (this binary ships: default)", name)
	}
	b, err := object.Canonical(policy.Default())
	if err != nil {
		return resolvedPolicy{}, err
	}
	pol, err := policy.Decode(b)
	if err != nil {
		return resolvedPolicy{}, err
	}
	return resolvedPolicy{Digest: object.DigestBytes(b), Bytes: b, Pol: pol, Source: "built-in"}, nil
}

// writePolicySummary renders what a policy MEANS: the ordered gates with
// their oracle, minimum basis and threshold; the effective ranking key list
// (including the implicit first and terminal keys); the escalation rules
// that are on; and the declared oracles with their resolved argv and config
// digest — the identity their receipts will carry.
func writePolicySummary(w io.Writer, pol policy.Policy) {
	fmt.Fprintf(w, "digest:    %s\n", pol.Digest)
	fmt.Fprintf(w, "schema:    %s\n", pol.Schema)
	name := pol.Name
	if name == "" {
		name = "-"
	}
	fmt.Fprintf(w, "name:      %s\n", name)
	fmt.Fprintln(w, "gates (ordered):")
	for i, g := range pol.Gates {
		oracle := g.Oracle
		if oracle == "" {
			oracle = "(family " + g.Sel.Family + ")"
		}
		fmt.Fprintf(w, "  %d. %-32s oracle=%s basis>=%s threshold=%d\n",
			i+1, g.Label, oracle, g.Basis, g.Threshold)
	}
	fmt.Fprintf(w, "ranking:   %s\n", strings.Join(pol.KeyNames(), ","))
	rules := escalationRules(pol.Esc)
	if len(rules) == 0 {
		rules = []string{"(none)"}
	}
	fmt.Fprintf(w, "escalation: %s\n", strings.Join(rules, ", "))
	fmt.Fprintln(w, "oracles:")
	if len(pol.Oracles) == 0 {
		fmt.Fprintf(w, "  (none declared: policy/v0 names a gate, not the command that decides it)\n")
	}
	for _, o := range pol.Oracles {
		fmt.Fprintf(w, "  %-12s kind=%s config=%s argv=%s\n",
			o.Name, o.Kind, o.Config, strings.Join(append(o.Argv, o.Args...), " "))
	}
	fmt.Fprintf(w, "required:  %s\n", strings.Join(pol.Required, ","))
	writePathsAndInvariants(w, pol)
	// Unconditional: a reader must not have to know that T0 means
	// `streamed`. A guarantee nobody can see the absence of is a
	// guarantee nobody has (M1f decision 13).
	fmt.Fprintf(w, "evidence:  regime %s, crosscheck %s, plugin_autoload %s\n",
		pol.Evidence.Regime, pol.Evidence.Crosscheck, pol.Evidence.PluginAutoload)
}

// writePathsAndInvariants renders the two M1f sections, and only when the
// policy declares them: an M1e-era policy's rendering is unchanged.
func writePathsAndInvariants(w io.Writer, pol policy.Policy) {
	if !pol.Paths.Empty() {
		fmt.Fprintln(w, "paths (frozen against the candidate):")
		for _, cls := range []struct {
			label    string
			patterns []policy.Pattern
			note     string
		}{
			{"protected", pol.Paths.Protected, "modify/delete"},
			{"harness", pol.Paths.Harness, "modify/delete/create"},
		} {
			if len(cls.patterns) == 0 {
				continue
			}
			raws := make([]string, 0, len(cls.patterns))
			for _, p := range cls.patterns {
				raws = append(raws, p.Raw)
			}
			fmt.Fprintf(w, "  %-10s (%s) %s\n", cls.label, cls.note, strings.Join(raws, " "))
		}
		fmt.Fprintf(w, "  %-10s %s\n", "additions", pol.Paths.ProtectedAdditions)
	}
	if len(pol.Invariants) > 0 {
		fmt.Fprintln(w, "invariants (cross-oracle):")
		for _, inv := range pol.Invariants {
			roles := make([]string, 0, len(inv.Roles))
			for _, role := range inv.RoleNames() {
				roles = append(roles, role)
			}
			fmt.Fprintf(w, "  %-28s roles %s\n", inv.Name, strings.Join(roles, ","))
		}
	}
}

// escalationRules lists the rules that are ON, in evaluation order.
func escalationRules(e policy.Escalation) []string {
	var out []string
	// Rule 0 first: it is the highest precedence, and an operator reading
	// this line reads it in the order the rules actually fire.
	if e.OnInvariantViolation {
		out = append(out, "on_invariant_violation")
	}
	if e.OnAllWorldsFailedMachinery {
		out = append(out, "on_all_worlds_failed_machinery")
	}
	if len(e.RequireEvidence) > 0 {
		names := make([]string, 0, len(e.RequireEvidence))
		for _, r := range e.RequireEvidence {
			names = append(names, r.OracleName)
		}
		out = append(out, "require_evidence="+strings.Join(names, "+"))
	}
	if e.MinCandidatesPassing > 0 {
		out = append(out, fmt.Sprintf("min_candidates_passing=%d", e.MinCandidatesPassing))
	}
	if e.OnRankingTie {
		out = append(out, "on_ranking_tie")
	}
	return out
}

// requireHardGates is the ingest boundary's structural floor, and it is the
// one rule the v0 shape cannot state for itself: `Validate` enforces "at
// least one hard gate" for v1, but a v0 document is decoded exactly as M0
// decoded it (frozen, never re-validated), so `{"hard_gates":[]}` compiles
// to a policy that gates nothing. Such a policy makes every world pass, and
// makes `admit` sign an ADMIT no gate ever justified.
//
// It is refused where a policy ENTERS the workspace — validate, use, and
// the per-intent pin — and nowhere else: `policy.Decode` stays total so that
// historical ledgers, published closures and `mvo audit` keep replaying
// whatever was recorded, exactly as recorded.
func requireHardGates(pol policy.Policy) error {
	if len(pol.Gates) == 0 {
		return errors.New("a policy must declare at least one hard gate")
	}
	return nil
}

// cmdPolicyValidate decodes, validates and compiles a policy FILE and
// reports every problem it has, one per line. Invalid content is a failure
// (exit 1), not CLI misuse.
func cmdPolicyValidate(args []string, stdout, stderr io.Writer) error {
	fileArg, rest := splitDigestArg(args)
	fs := newFlagSet("policy validate", stderr)
	_ = fs.String("dir", ".", "repository directory")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if fileArg == "" && len(fs.Args()) > 0 {
		fileArg = fs.Args()[0]
	}
	if fileArg == "" {
		return usagef("policy validate: a policy file is required")
	}
	b, err := os.ReadFile(fileArg)
	if err != nil {
		return fmt.Errorf("policy validate: %w", err)
	}
	pol, err := policy.Decode(b)
	if err != nil {
		return policyProblems("policy validate", fileArg, err)
	}
	if err := requireHardGates(pol); err != nil {
		return fmt.Errorf("policy validate: %s: hard_gates: %w", fileArg, err)
	}
	writePolicySummary(stdout, pol)
	fmt.Fprintln(stdout, "OK: policy valid")
	return nil
}

// policyProblems renders every validation problem as its own line, each
// prefixed exactly as the CLI prints errors ("mvo: " is added by main to
// the first line). Authoring wants the whole list, not the first line.
func policyProblems(verb, file string, err error) error {
	probs := policy.Problems(err)
	if len(probs) == 0 {
		return fmt.Errorf("%s: %s: %w", verb, file, err)
	}
	lines := make([]string, 0, len(probs))
	for _, p := range probs {
		lines = append(lines, fmt.Sprintf("%s: %s: %s", verb, file, p.Error()))
	}
	return errors.New(strings.Join(lines, "\nmvo: "))
}

// cmdPolicyUse installs a file-backed policy as the workspace default: it
// validates the file, requires its in-document name to equal the filename
// stem, puts its bytes in CAS, records policy.created when the digest is
// new, and points config.default_policy at it.
func cmdPolicyUse(args []string, stdout, stderr io.Writer) error {
	nameArg, rest := splitDigestArg(args)
	fs := newFlagSet("policy use", stderr)
	dir := fs.String("dir", ".", "repository directory")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if nameArg == "" && len(fs.Args()) > 0 {
		nameArg = fs.Args()[0]
	}
	if nameArg == "" {
		return usagef("policy use: a policy name is required")
	}
	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("policy use: %w", err)
	}
	defer ws.Close()

	path := ws.PolicyFile(nameArg)
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("policy use: %w", err)
	}
	pol, err := policy.Decode(b)
	if err != nil {
		return policyProblems("policy use", filepath.Base(path), err)
	}
	// A shape whose gate the digest does not determine must never become the
	// SILENT default for everything created afterwards (M1e decision 18):
	// two machines could satisfy the same attested policy with different
	// suites. Pinning one per intent stays possible and deliberate.
	if pol.Schema != object.SchemaPolicyV1 {
		return fmt.Errorf("policy use: %s is %s, which cannot name its oracles; author a policy/v1 file (see mvo policy show default --json)",
			filepath.Base(path), policy.SchemaShort(pol.Schema))
	}
	if pol.Name != nameArg {
		return fmt.Errorf("policy use: %s declares name %q: a policy file's name must equal its filename stem",
			filepath.Base(path), pol.Name)
	}
	if err := requireHardGates(pol); err != nil {
		return fmt.Errorf("policy use: %s: hard_gates: %w", filepath.Base(path), err)
	}
	// Refused at INSTALL time, where the operator can act on it. A policy
	// declaring a regime this binary cannot deliver used to validate
	// cleanly, install cleanly, and then refuse every race — a workspace
	// bricked until someone edited the file back.
	if err := race.CheckRegime("policy use", pol); err != nil {
		return err
	}

	dig := object.DigestBytes(b)
	if _, err := ws.CAS.Put(b); err != nil {
		return fmt.Errorf("policy use: %w", err)
	}
	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("policy use: %w", err)
	}
	if err := recordPolicyIfNew(ws, st, dig, b); err != nil {
		return fmt.Errorf("policy use: %w", err)
	}
	if err := ws.SetDefaultPolicy(dig); err != nil {
		return fmt.Errorf("policy use: %w", err)
	}
	fmt.Fprintf(stdout, "default policy %s (%s, %s)\n", dig, pol.Name, policy.SchemaShort(pol.Schema))
	return nil
}

// recordPolicyIfNew appends policy.created for a digest the ledger has not
// seen. Idempotent: a policy already recorded is not recorded again, so
// re-running `use` neither forks the digest nor duplicates history.
func recordPolicyIfNew(ws *workspace.Workspace, st *ledgerState, dig string, canon []byte) error {
	for _, pr := range st.Policies {
		if pr.Dig == dig {
			return nil
		}
	}
	if _, err := ws.Ledger.Append(evPolicyCreated, canon); err != nil {
		return fmt.Errorf("record policy: %w", err)
	}
	return nil
}
