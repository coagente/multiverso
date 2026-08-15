package race

// M1f: the regime a receipt records must be what ACTUALLY happened.
//
// The design's testing bar asked for exactly this table and it did not
// ship, so nothing caught the following: `resolveRegime` returned
// `isolated` for every T1 race, and `execution.evidence_regime` recorded
// it, while `t1World.Command` execs with workdir /work (read-write) and no
// `--user`. A real T1 race under the shipped default produced receipts
// reading `evidence_regime: "isolated"` with `isolation_caps.user:
// "501:20"` — the invoking uid, which owns the worktree and the evidence
// directory — so not one of the three guarantees the regime table promises
// under `isolated` held.
//
// That is the study's finding with the label doing the laundering: a
// signed claim stronger than the evidence behind it. These tests pin the
// regime to the capability that actually ships.

import (
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

func TestResolveRegimeNeverClaimsUnavailableIsolation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		declare string
		tier    string
		want    string
		wantErr string
	}{
		// `auto` is the compiled default of every policy ever written,
		// including every M1e one, so this row is what almost every run
		// gets.
		{name: "auto under T0", declare: policy.RegimeAuto, tier: object.TierT0Worktree, want: object.RegimeStreamed},
		{
			name:    "auto under T1 does not upgrade to a regime the exec path cannot deliver",
			declare: policy.RegimeAuto, tier: object.TierT1Container, want: object.RegimeStreamed,
		},
		{name: "empty compiles like auto", declare: "", tier: object.TierT1Container, want: object.RegimeStreamed},

		// An explicitly declared `isolated` is REFUSED, not downgraded:
		// silently observing a policy at a weaker regime than it demands is
		// the failure this whole block exists to remove.
		{
			name:    "declared isolated is refused under T0",
			declare: object.RegimeIsolated, tier: object.TierT0Worktree,
			wantErr: "does not ship that exec path",
		},
		{
			name:    "declared isolated is refused under T1 too",
			declare: object.RegimeIsolated, tier: object.TierT1Container,
			wantErr: "does not ship that exec path",
		},

		// An explicitly declared regime that IS deliverable passes through.
		{name: "declared streamed", declare: object.RegimeStreamed, tier: object.TierT0Worktree, want: object.RegimeStreamed},
		{name: "declared in-tree", declare: object.RegimeInTree, tier: object.TierT0Worktree, want: object.RegimeInTree},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pol := policy.Policy{
				Digest:   "mv0:" + strings.Repeat("a", 64),
				Evidence: policy.EvidencePlan{Regime: tc.declare},
			}
			got, err := resolveRegime(pol, tc.tier)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("resolveRegime = %q, want an error naming the missing capability", got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				// The refusal must name the policy and the way out, or an
				// operator has a wall instead of a next step.
				if !strings.Contains(err.Error(), pol.Digest) {
					t.Errorf("error = %q, want it to name the policy", err)
				}
				if !strings.Contains(err.Error(), policy.RegimeAuto) {
					t.Errorf("error = %q, want it to name the setting to use instead", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRegime: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolveRegime = %q, want %q", got, tc.want)
			}
			// Whatever comes back is a regime a receipt can honestly carry:
			// never the unresolved sentinel, never a capability we lack.
			if got == policy.RegimeAuto {
				t.Error("resolveRegime returned the unresolved sentinel")
			}
			if got == object.RegimeIsolated && !isolatedExecAvailable {
				t.Error("resolveRegime claimed isolated without an isolated exec path")
			}
		})
	}
}

// The guard rail for whoever ships the `--user` exec path: flipping
// isolatedExecAvailable must go together with the exec change, and this
// test says so at the place the constant is read.
func TestIsolatedRegimeMatchesTheShippedExecPath(t *testing.T) {
	if isolatedExecAvailable {
		t.Skip("an isolated exec path ships; the T1 backend tests own the uid/read-only-cwd assertions")
	}
	pol := policy.Policy{Digest: "mv0:x", Evidence: policy.EvidencePlan{Regime: policy.RegimeAuto}}
	for _, tier := range []string{object.TierT0Worktree, object.TierT1Container} {
		got, err := resolveRegime(pol, tier)
		if err != nil {
			t.Fatalf("resolveRegime(%s): %v", tier, err)
		}
		if got == object.RegimeIsolated {
			t.Fatalf("tier %s resolved to %q with no isolated exec path", tier, got)
		}
	}
}
