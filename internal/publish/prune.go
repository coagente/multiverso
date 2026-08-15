package publish

import (
	"errors"
	"fmt"
	"time"

	"github.com/coagente/multiverso/internal/gitx"
	"github.com/coagente/multiverso/internal/ledger"
)

// PruneConfig wires one retention prune (FI-1).
type PruneConfig struct {
	Repo         string
	Ledger       *ledger.Ledger
	Intent       string
	Remote       string        // "" = local-only (deleting remote refs is explicit, always)
	OlderThan    time.Duration // 0 = no age guard
	KeepAdmitted bool          // CLI default true
}

// PruneResult is what a prune deleted and kept.
type PruneResult struct {
	Short                       string
	DeletedLocal, DeletedRemote []string
	Kept                        []string
}

// Prune applies the M1d retention policy (decision 12): loser candidate
// refs are always prunable; a non-admitted intent's whole namespace is
// prunable (ledger + CAS remain the canonical record); an admitted
// intent keeps the winner's candidate ref and the evidence ref — they back
// the landed commit's attestation for remote verifiers — unless
// KeepAdmitted is false. CAS and ledger are never touched: append-only
// stands.
func Prune(cfg PruneConfig) (*PruneResult, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	short, err := IntentShort(cfg.Intent)
	if err != nil {
		return nil, err
	}
	v, err := scanLedger(cfg.Ledger)
	if err != nil {
		return nil, err
	}

	// The --older-than guard, not a selector: prune refuses to act unless
	// the latest publish.finished for the intent is older than the bound.
	// Publication commits are epoch-stamped by design, so the ledger is the
	// only honest clock; an intent never published cannot satisfy the
	// guard.
	if cfg.OlderThan > 0 {
		var last *pubFinishRec
		for i := range v.PubFinishes {
			if v.PubFinishes[i].Intent == cfg.Intent {
				last = &v.PubFinishes[i]
			}
		}
		if last == nil {
			return nil, fmt.Errorf("publish: prune: intent %s was never published — --older-than has nothing to age against", cfg.Intent)
		}
		ts, err := time.Parse(time.RFC3339, last.TS)
		if err != nil {
			return nil, fmt.Errorf("publish: prune: parse publish.finished timestamp %q: %w", last.TS, err)
		}
		if age := time.Since(ts); age < cfg.OlderThan {
			return nil, fmt.Errorf("publish: prune: latest publication of %s is %s old, younger than --older-than %s — refusing",
				cfg.Intent, age.Round(time.Second), cfg.OlderThan)
		}
	}

	// The keep set: winner candidate ref + evidence ref, iff the intent is
	// admitted and KeepAdmitted holds.
	admitted := false
	for _, af := range v.AdmFinishes {
		if af.Intent == cfg.Intent && af.Result == "ADMIT" {
			admitted = true
		}
	}
	keep := map[string]bool{}
	if admitted && cfg.KeepAdmitted {
		sel, err := latestSelect(v, cfg.Intent)
		if err != nil {
			return nil, err
		}
		for _, cand := range sel.Worlds {
			if cand.Winner {
				keep[CandRef(short, cand.Ordinal)] = true
			}
		}
		keep[EvidenceRef(short)] = true
	}

	res := &PruneResult{
		Short:         short,
		DeletedLocal:  []string{},
		DeletedRemote: []string{},
		Kept:          []string{},
	}
	keptSet := map[string]bool{}

	// Pre-flight, then BOTH surveys, then the partition — before anything
	// is deleted. The local namespace refs are the world trees' GC pins
	// (decision 11), so a failure on the remote leg must not find them
	// already gone: a survey (or remote) error returns with nothing
	// mutated and nothing recorded, publish's pre-flight discipline (M1c
	// decision 18).
	if cfg.Remote != "" {
		if _, err := gitx.RemoteURL(cfg.Repo, cfg.Remote); err != nil {
			return nil, fmt.Errorf("publish: prune: remote %q: %w", cfg.Remote, err)
		}
	}
	local, err := gitx.ForEachRef(cfg.Repo, Namespace(short))
	if err != nil {
		return nil, fmt.Errorf("publish: prune: %w", err)
	}
	remote := map[string]string{}
	if cfg.Remote != "" {
		remote, err = gitx.LsRemote(cfg.Repo, cfg.Remote, Namespace(short)+"/*")
		if err != nil {
			return nil, fmt.Errorf("publish: prune: %w", err)
		}
	}
	var localDeletes []string
	for _, ref := range sortedKeys(local) {
		if keep[ref] {
			keptSet[ref] = true
			continue
		}
		localDeletes = append(localDeletes, ref)
	}
	var remoteDeletes, refspecs []string
	leases := map[string]string{}
	for _, ref := range sortedKeys(remote) {
		if keep[ref] {
			keptSet[ref] = true
			continue
		}
		remoteDeletes = append(remoteDeletes, ref)
		refspecs = append(refspecs, ":"+ref)
		leases[ref] = remote[ref]
	}

	// An empty survey still records the event — the retention action's
	// audit trail, honest about removing nothing. Every exit path after
	// the first deletion records too: refs are gone either way, and a
	// deletion the ledger never saw is a permanently wrong record.
	record := func() error {
		res.Kept = sortedKeys(keptSet)
		return appendEvent(cfg.Ledger, "prune.executed", map[string]any{
			"deleted_local":  strsAny(res.DeletedLocal),
			"deleted_remote": strsAny(res.DeletedRemote),
			"intent":         cfg.Intent,
			"keep_admitted":  cfg.KeepAdmitted,
			"kept":           strsAny(res.Kept),
			"older_than_ms":  cfg.OlderThan.Milliseconds(),
			"remote":         cfg.Remote,
		})
	}

	// Local deletion: update-ref -d per ref, compare-and-swap on the
	// surveyed value.
	for _, ref := range localDeletes {
		if err := gitx.DeleteRef(cfg.Repo, ref, local[ref]); err != nil {
			delErr := fmt.Errorf("publish: prune: %w", err)
			if recErr := record(); recErr != nil {
				return nil, recErr
			}
			return nil, delErr
		}
		res.DeletedLocal = append(res.DeletedLocal, ref)
	}

	// Remote deletion: one bulk push of ":<ref>" refspecs, leased on the
	// observed values (probe Q4: bulk deletes are cheap). The push is
	// atomic, so a failure deleted nothing remotely — the event says so.
	if len(refspecs) > 0 {
		if hookBeforePush != nil {
			hookBeforePush()
		}
		if err := gitx.Push(cfg.Repo, cfg.Remote, refspecs, leases); err != nil {
			pushErr := fmt.Errorf("publish: prune: delete on %s: %w", cfg.Remote, err)
			if recErr := record(); recErr != nil {
				return nil, recErr
			}
			return nil, pushErr
		}
		res.DeletedRemote = remoteDeletes
	}

	if err := record(); err != nil {
		return nil, err
	}
	return res, nil
}

func (cfg PruneConfig) validate() error {
	switch {
	case cfg.Repo == "":
		return errors.New("publish: prune: config: empty repo")
	case cfg.Ledger == nil:
		return errors.New("publish: prune: config: nil ledger")
	case cfg.Intent == "":
		return errors.New("publish: prune: config: empty intent digest")
	}
	return nil
}
