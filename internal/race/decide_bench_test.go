package race

import (
	"fmt"
	"testing"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

func benchInputs(nWorlds int) (policy.Policy, []object.RecordedWorld, []object.RecordedReceipt) {
	b, err := object.Canonical(policy.Default())
	if err != nil {
		panic(err)
	}
	pol, err := policy.Decode(b)
	if err != nil {
		panic(err)
	}
	var ws []object.RecordedWorld
	var rs []object.RecordedReceipt
	for i := 0; i < nWorlds; i++ {
		w := object.World{Schema: object.SchemaWorld, Tree: fmt.Sprintf("git:%040d", i), Outcome: object.OutcomeCompleted}
		dig := fmt.Sprintf("mv0:%060d", i)
		ws = append(ws, object.RecordedWorld{Digest: dig, World: w})
		for j, kind := range []string{"tree-guard", "pytest-collect", "pytest-suite"} {
			r := object.Receipt{
				Schema: object.SchemaReceipt, World: dig,
				Oracle: object.OracleRef{ID: kind, Version: "v0"},
				Result: object.Result{Status: "pass", Metrics: map[string]int64{
					"paths_examined": 10, "collected_total": 8, "collected_delta": 0,
					"tests_total": 8, "tests_passed": 8,
				}},
				Freshness: object.Freshness{ValidFor: object.ValidFor{Tree: w.Tree, Env: w.Env}},
			}
			rs = append(rs, object.RecordedReceipt{Digest: fmt.Sprintf("mv0:%030d%030d", i, j), Receipt: r})
		}
	}
	return pol, ws, rs
}

// BenchmarkDecideLookahead measures the METALEVEL cost of one lookahead
// call. M2b's allocation rule evaluates a prospective purchase by calling
// Decide on synthetic outcomes, so the whole scheduler is only worth having
// if a Decide call is orders of magnitude cheaper than the purchase it
// prices. Measured on this tree: ~14 us at six worlds against a ~100-400 ms
// pytest rung. See docs/design/M2b-adaptive-scheduler.md.
func BenchmarkDecideLookahead(b *testing.B) {
	for _, n := range []int{2, 6, 12} {
		pol, ws, rs := benchInputs(n)
		b.Run(fmt.Sprintf("worlds=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = Decide(pol, ws, rs)
			}
		})
	}
}
