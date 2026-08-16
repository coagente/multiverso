package policy

// The DECLARED cost and correlation menu (M2a, "Cost model — what M2b
// consumes"). It is data a scheduler reads WITHOUT running anything, and it
// never enters Decide.
//
// There are no numbers here on purpose. Costs are per-repository and must
// be MEASURED — M2a's measured coefficients are fitted from this
// workspace's own `receipt.recorded` events, and fewer than three receipts
// prints "no local measurement" rather than a two-point fit dressed up as a
// fact. What is declared here is the SHAPE: what dominates a kind's cost,
// what it scales by, what bounds it, what it discriminates, and how
// correlated its evidence is with everything else on the menu.

import "github.com/coagente/multiverso/internal/object"

// Oracle stages. A world-stage oracle is the M1 shape — Run(ctx, world) →
// Receipt, one world in, one receipt out. A COHORT-stage oracle is a pure
// reducer over many worlds' observations that still emits one receipt per
// world (M2a decision 1): the alternative, one receipt over N worlds, could
// not answer "which world does this judge?" and would have forked
// receipt/v0, M1a's attestation subject binding, mvo verify check 6, M1d's
// publication layout and M1e's counted-receipt rule at once.
const (
	StageWorld  = "world"
	StageCohort = "cohort"
)

// What a kind's evidence discriminates. `partition` is the differential's
// and it is not a verdict: it splits the cohort into behaviour classes and
// says nothing about which class is right (M2a decision 7).
const (
	DiscriminateVerdict   = "verdict"
	DiscriminateOrdinal   = "ordinal"
	DiscriminatePartition = "partition"
	DiscriminateNone      = ""
)

// Correlation signals — WHAT a kind reads. Two purchases with the same
// signal and the same corpus are near-duplicates; genuine independence
// needs signal AND generator to differ (M2a's discount rule).
const (
	SignalTreeBytes     = "tree-bytes"
	SignalTestIdentity  = "test-identity"
	SignalTestOutcomes  = "test-outcomes"
	SignalValueBehavior = "value-behavior"
	SignalSuiteAdequacy = "suite-adequacy"
)

// Who WROTE the inputs a kind reads, and who EXECUTED it. Executor bounds
// the value of everything: under M1f's residual, any number a candidate's
// own process reported is forgeable by a competent in-process adversary, so
// buying a third candidate-process oracle buys independence from honest
// error, never from that adversary.
const (
	GeneratorControlPlane = "control-plane"
	GeneratorBaseTree     = "base-tree"
	GeneratorRepo         = "repo"
	GeneratorRepoPolicy   = "repo+policy"

	ExecutorControlPlane     = "control-plane"
	ExecutorCandidateProcess = "candidate-process"
)

// Scaling units recorded in cost.unit.
const (
	UnitPaths      = "paths"
	UnitTests      = "tests"
	UnitCases      = "cases"
	UnitWorldCases = "world-cases"
	UnitMutants    = "mutants"
)

// OracleProfile is the DECLARED shape of a kind's cost and of the
// correlation structure of its evidence.
type OracleProfile struct {
	Stage        string // StageWorld | StageCohort
	Dominant     string // what dominates the cost
	Unit         string // the scaling unit recorded in cost.unit
	Cap          string // the policy field that bounds it; "" = unbounded
	Amortized    bool   // the cost is per RACE, not per world
	Discriminate string // DiscriminateVerdict | Ordinal | Partition | None
	Corr         object.Correlation
}

// profiles is TOTAL over the registry's kinds: a switch test asserts every
// kind has one, because a scheduler that meets a kind with no declared
// profile has to guess, and guessing is what this table exists to remove.
var profiles = map[string]OracleProfile{
	KindCommand: {
		Stage: StageWorld, Dominant: "process-run", Unit: "", Discriminate: DiscriminateVerdict,
		// A command oracle runs an operator-authored command inside the
		// candidate's process. Its SIGNAL is unknown to us — that is what
		// "command" means — and "" is the honest record of an unknown, not
		// a default.
		Corr: object.Correlation{Generator: GeneratorRepo, Executor: ExecutorCandidateProcess},
	},
	KindTreeGuard: {
		Stage: StageWorld, Dominant: "tree-walk", Unit: UnitPaths, Discriminate: DiscriminateVerdict,
		Corr: object.Correlation{
			Signal: SignalTreeBytes, Generator: GeneratorControlPlane, Executor: ExecutorControlPlane,
		},
	},
	KindPytestCollect: {
		Stage: StageWorld, Dominant: "interpreter-start", Unit: UnitTests, Discriminate: DiscriminateVerdict,
		Corr: object.Correlation{
			Signal: SignalTestIdentity, Generator: GeneratorRepo, Executor: ExecutorCandidateProcess,
		},
	},
	KindPytestSuite: {
		Stage: StageWorld, Dominant: "suite-run", Unit: UnitTests, Discriminate: DiscriminateVerdict,
		Corr: object.Correlation{
			Signal: SignalTestOutcomes, Generator: GeneratorRepo, Executor: ExecutorCandidateProcess,
		},
	},
	KindCorpusObserve: {
		Stage: StageWorld, Dominant: "case-replay", Unit: UnitCases, Cap: "corpus.cases_max",
		// A raw observation discriminates NOTHING on its own: it is the
		// input the reducer partitions. Saying "verdict" here would invite
		// a scheduler to buy it as if it decided something.
		Discriminate: DiscriminateNone,
		Corr: object.Correlation{
			Signal: SignalValueBehavior, Generator: GeneratorBaseTree, Executor: ExecutorCandidateProcess,
		},
	},
	KindCorpusDifferential: {
		Stage: StageCohort, Dominant: "hashing", Unit: UnitWorldCases, Discriminate: DiscriminatePartition,
		Corr: object.Correlation{
			Signal: SignalValueBehavior, Generator: GeneratorBaseTree, Executor: ExecutorControlPlane,
		},
	},
	KindProperties: {
		Stage: StageWorld, Dominant: "case-replay", Unit: UnitCases, Cap: "corpus.cases_max",
		Discriminate: DiscriminateVerdict,
		// The properties come from the REPOSITORY's own @given tests plus
		// the policy-declared module, so the generator is repo+policy and
		// the discount rule reads it as strongly correlated with the
		// suite: same authors, same prior.
		Corr: object.Correlation{
			Signal: SignalValueBehavior, Generator: GeneratorRepoPolicy, Executor: ExecutorCandidateProcess,
		},
	},
	KindMutationDiff: {
		Stage: StageWorld, Dominant: "suite-run-per-mutant", Unit: UnitMutants, Cap: "mutation.max_mutants",
		// Ordinal, and RECORDED rather than ranked: every candidate-
		// comparing key derivable from it is signed by something the
		// candidate chooses (decision 8).
		Discriminate: DiscriminateOrdinal,
		// The mutants are enumerated over a target set the CONTROL PLANE
		// derived from the captured patch, which is why the generator is
		// control-plane while the executor is still the candidate's own
		// process. That pairing is what makes mutation-diff × pytest-suite
		// one of the three genuinely independent pairs on the menu:
		// adequacy of the tests versus outcome of the tests.
		Corr: object.Correlation{
			Signal: SignalSuiteAdequacy, Generator: GeneratorControlPlane, Executor: ExecutorCandidateProcess,
		},
	},
}

// KindProfile returns a kind's declared profile and whether it has one.
func KindProfile(kind string) (OracleProfile, bool) {
	p, ok := profiles[kind]
	return p, ok
}

// KindStage reports whether a kind runs per world or per cohort. Unknown
// kinds report StageWorld: validation refuses them long before this, and
// the world stage is the one that needs no barrier.
func KindStage(kind string) string {
	if p, ok := profiles[kind]; ok && p.Stage != "" {
		return p.Stage
	}
	return StageWorld
}

// RaceStagedInputs reports whether a kind's run-time INPUTS are produced by
// the race itself rather than by the policy and the tree.
//
// `corpus-observe` replays a corpus that phase 0 materialized on the base
// tree before any world existed. Admission has no phase 0, so the oracle
// has nothing to replay there: the gate over it is not failed, it is
// unevaluatable, and an unevaluatable hard gate is an admission that can
// never succeed. Validation rule 23 uses this to require scope "race" on
// such gates, which is decision 21's argument one rung below the cohort
// stage it was written for.
func RaceStagedInputs(kind string) bool {
	switch kind {
	case KindCorpusObserve:
		return true
	default:
		return false
	}
}

// KindCorrelation returns the correlation descriptor a kind's receipts
// carry. Corpus is filled in per RUN (it is a digest, not a constant), so
// producers copy this and set it.
func KindCorrelation(kind string) object.Correlation { return profiles[kind].Corr }

// KindUnit returns the scaling unit a kind's receipts record in cost.unit.
func KindUnit(kind string) string { return profiles[kind].Unit }
