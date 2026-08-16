package race

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// M2a decision 20, under test: a policy that declares a rung whose OWN
// toolchain is missing aborts at pre-flight — before race.started, with the
// ledger empty of race events and no world created.
//
// On the machine M2a was written on none of hypothesis, mutmut or
// cosmic-ray is installed, so this is the live path rather than a
// contingency: the fake interpreter reports pytest and nothing else,
// exactly as this workspace's real one does.
func TestPreflightRefusesAMissingRungToolchain(t *testing.T) {
	for _, tt := range []struct {
		name  string
		spec  object.OracleSpec
		gate  object.GateSpec
		wants []string
	}{
		{
			name: "mutation-diff without cosmic-ray or mutmut",
			spec: object.OracleSpec{Name: "mutate", Kind: policy.KindMutationDiff, Args: []string{}},
			gate: object.GateSpec{Gate: policy.GateMutationSurvivorsNotAbove, Oracle: "mutate",
				Basis: object.BasisConstruction},
			wants: []string{`policy requires oracle "mutate"`, "cosmic-ray or mutmut", "machinery, never a failing candidate"},
		},
		{
			name: "hypothesis-properties without hypothesis",
			spec: object.OracleSpec{Name: "props", Kind: policy.KindProperties, Args: []string{},
				Corpus: object.CorpusSpec{Provider: policy.ProviderHypothesis, Module: "props/mvo_props.py"}},
			gate: object.GateSpec{Gate: policy.GatePropertiesPass, Oracle: "props",
				Basis: object.BasisConstruction},
			wants: []string{`policy requires oracle "props"`, "hypothesis is not importable"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			python := fakePython(t, true) // pytest IS importable; the rung's own tool is not
			pol := ladderPolicy(python, filepath.Join(t.TempDir(), "spare-ran"))
			spec := tt.spec
			spec.Argv = []string{python, "-m", "pytest"}
			pol.Name = "m2a-preflight"
			pol.Oracles = append(pol.Oracles, spec)
			pol.HardGates = append(pol.HardGates, tt.gate)
			if spec.Corpus.Module != "" || spec.Corpus.File != "" {
				// Rule 24: a declared property module compiles into
				// paths.harness, and a freeze nothing checks is not a freeze.
				pol.Oracles = append(pol.Oracles, object.OracleSpec{
					Name: "guard", Kind: policy.KindTreeGuard, Argv: []string{}, Args: []string{},
				})
				pol.HardGates = append([]object.GateSpec{{
					Gate: policy.GatePathsUnmodified, Oracle: "guard", Basis: object.BasisConstruction,
				}}, pol.HardGates...)
			}
			cfg := newLadderConfig(t, pol, map[string]string{"a-fix.patch": fixPatch})

			_, err := Run(context.Background(), cfg)
			if err == nil {
				t.Fatal("Run: want a machinery error, got nil")
			}
			for _, want := range tt.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
			// The ledger is UNTOUCHED by race events: a missing toolchain
			// must never be recorded as something the candidates did.
			if n := len(eventsOfType(t, cfg.Ledger, "race.started")); n != 0 {
				t.Errorf("pre-flight failure recorded %d race.started events, want 0", n)
			}
			if n := len(eventsOfType(t, cfg.Ledger, "world.created")); n != 0 {
				t.Errorf("pre-flight failure recorded %d worlds, want 0", n)
			}
		})
	}
}
