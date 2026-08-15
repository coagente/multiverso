package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/workspace"
)

func cmdIntentNew(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("intent new", stderr)
	dir := fs.String("dir", ".", "repository directory")
	title := fs.String("title", "", "intent title (required)")
	desc := fs.String("desc", "", "intent description")
	budgetCandidates := fs.Int("budget-candidates", 2, "max candidate worlds")
	budgetWallMS := fs.Int64("budget-wall-ms", 600000, "max wall-clock budget in milliseconds")
	policyRef := fs.String("policy", "", "pin this policy (name or mv0: digest) instead of the workspace default")
	oracleCmd := fs.String("oracle-cmd", "", "pin a synthesized command-oracle policy running CMD as the suite gate")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *title == "" {
		return usagef("intent new: --title is required")
	}
	if *budgetCandidates < 1 || *budgetWallMS < 1 {
		return usagef("intent new: budgets must be positive")
	}
	if *policyRef != "" && *oracleCmd != "" {
		return usagef("intent new: --policy and --oracle-cmd are mutually exclusive: each pins a different policy")
	}
	oracleArgv := strings.Fields(*oracleCmd)
	if *oracleCmd != "" && len(oracleArgv) == 0 {
		return usagef("intent new: --oracle-cmd is empty")
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("intent new: %w", err)
	}
	defer ws.Close()

	commit, tree, err := gitx.Head(*dir)
	if err != nil {
		return fmt.Errorf("intent new: %w", err)
	}

	// An intent PINS a policy digest: that pin is what every later race,
	// admission and replay is judged by, so resolving it happens once, here.
	polDig := ws.Config.DefaultPolicy
	switch {
	case *policyRef != "":
		polDig, err = pinPolicy(ws, *policyRef, stderr)
		if err != nil {
			return fmt.Errorf("intent new: %w", err)
		}
	case len(oracleArgv) > 0:
		// The migration path for `--oracle-cmd` (M1e decision 18): the
		// command moves INSIDE the pinned, attested artifact, where the
		// policy digest determines it — instead of riding on a race-time
		// flag two machines could spell differently.
		polDig, err = pinCommandPolicy(ws, oracleArgv, *budgetWallMS)
		if err != nil {
			return fmt.Errorf("intent new: %w", err)
		}
	}

	in := object.Intent{
		Schema:    object.SchemaIntent,
		Base:      object.Base{Commit: commit, Tree: tree},
		Spec:      object.Spec{Title: *title, Description: *desc},
		Budget:    object.Budget{MaxCandidates: *budgetCandidates, MaxWallMS: *budgetWallMS},
		Policy:    polDig,
		CreatedAt: nowRFC3339(),
	}
	dig, canon, err := object.Digest(in)
	if err != nil {
		return fmt.Errorf("intent new: %w", err)
	}
	// Objects live in CAS and the ledger; race.Run loads the intent from
	// CAS by this digest.
	if _, err := ws.CAS.Put(canon); err != nil {
		return fmt.Errorf("intent new: %w", err)
	}
	if _, err := ws.Ledger.Append(evIntentCreated, canon); err != nil {
		return fmt.Errorf("intent new: %w", err)
	}

	fmt.Fprintln(stdout, dig)
	return nil
}

// pinPolicy resolves a policy reference exactly as `mvo policy show` does,
// ingests its bytes into CAS, and records policy.created when the digest is
// new — a pinned policy must be resolvable for replay forever, whether or
// not the file it came from survives.
//
// Unlike `policy use`, this ACCEPTS a v0 policy: pinning a historical shape
// deliberately, per intent, is what replay and migration require. It says
// what that costs.
func pinPolicy(ws *workspace.Workspace, ref string, stderr io.Writer) (string, error) {
	st, err := loadState(ws.Ledger)
	if err != nil {
		return "", err
	}
	rp, err := resolvePolicy(ws, st, ref)
	if err != nil {
		return "", err
	}
	// A v0 policy may be pinned deliberately; a policy that gates NOTHING may
	// not be pinned at all. Every world would pass it, and admission would
	// sign an ADMIT no gate justified.
	if err := requireHardGates(rp.Pol); err != nil {
		return "", fmt.Errorf("policy %s: hard_gates: %w", rp.Digest, err)
	}
	if _, err := ws.CAS.Put(rp.Bytes); err != nil {
		return "", err
	}
	if err := recordPolicyIfNew(ws, st, rp.Digest, rp.Bytes); err != nil {
		return "", err
	}
	if rp.Pol.Schema != object.SchemaPolicyV1 {
		fmt.Fprintf(stderr, "mvo: intent new: policy %s is %s: its gate is not determined by the policy digest, so --oracle-cmd is required for every race of this intent (M1e decision 18)\n",
			rp.Digest, policy.SchemaShort(rp.Pol.Schema))
	}
	return rp.Digest, nil
}

// pinCommandPolicy synthesizes, records and pins the ephemeral
// command-oracle policy behind `mvo intent new --oracle-cmd` — a policy/v1
// artifact like any other: content-addressed, recorded once, replayable
// forever, and never written to the authoring directory (nothing installed
// it; this intent alone pinned it).
func pinCommandPolicy(ws *workspace.Workspace, argv []string, wallMS int64) (string, error) {
	canon, err := object.Canonical(policy.Command(argv, wallMS))
	if err != nil {
		return "", err
	}
	// Validated through the same door every other policy enters by: a
	// synthesized artifact gets no exemption from the vocabulary.
	pol, err := policy.Decode(canon)
	if err != nil {
		return "", err
	}
	if _, err := ws.CAS.Put(canon); err != nil {
		return "", err
	}
	st, err := loadState(ws.Ledger)
	if err != nil {
		return "", err
	}
	if err := recordPolicyIfNew(ws, st, pol.Digest, canon); err != nil {
		return "", err
	}
	return pol.Digest, nil
}
