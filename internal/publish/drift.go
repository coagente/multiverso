package publish

import (
	"fmt"

	"github.com/coagente/multiverso/internal/gitx"
)

// Trunk-drift statuses (M1d decision 16). Display-only: nothing is ever
// written anywhere — receipts' valid_for.tree never changes and no ledger
// event is emitted. Staleness is what the operator sees; EP-3's
// recompute-on-admit is what the gate enforces.
const (
	DriftFresh   = "FRESH"
	DriftStale   = "STALE"
	DriftUnknown = "UNKNOWN"
)

// TrunkDrift classifies intent.base.commit against the repo's current
// branch head. Pure read (rev-parse/merge-base); never writes anything.
// When the state is uncomputable the status is UNKNOWN with the reason —
// honesty over blank. A STALE intent still races, publishes, and fetches
// normally; only admit confronts drift (M1a, unchanged).
func TrunkDrift(repo, baseCommit string) (status, detail string) {
	branch, err := gitx.CurrentBranch(repo)
	if err != nil {
		return DriftUnknown, "detached HEAD"
	}
	if !gitx.CommitExists(repo, baseCommit) {
		return DriftUnknown, fmt.Sprintf("base commit %s not found", sha12(baseCommit))
	}
	head, err := gitx.ResolveCommit(repo, "HEAD")
	if err != nil {
		return DriftUnknown, "no commit checked out"
	}
	if head == baseCommit {
		return DriftFresh, fmt.Sprintf("base %s == %s head", sha12(baseCommit), branch)
	}
	base, err := gitx.MergeBase(repo, baseCommit, head)
	if err != nil {
		return DriftUnknown, fmt.Sprintf("merge-base failed: %v", err)
	}
	if base == baseCommit {
		return DriftStale, fmt.Sprintf("%s advanced past base %s", branch, sha12(baseCommit))
	}
	return DriftStale, fmt.Sprintf("base %s is not an ancestor of %s head %s",
		sha12(baseCommit), branch, sha12(head))
}

// sha12 abbreviates a sha to its first 12 hex chars for display.
func sha12(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
