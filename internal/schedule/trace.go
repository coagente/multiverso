package schedule

// The ALLOCATION TRACE (M2b §4): the scheduler's rows turned into canonical
// ledger payloads, and read back again. Instrumentation is the deliverable —
// without it M2d's experiment is uninterpretable, `mvo explain` has nothing
// to render, and PRD §11's evidence waste is not computable.
//
// Two rules govern everything in this file.
//
// RECORDED, NEVER RECOMPUTED (decision 17). A step's rows are appended as the
// scheduler computed them. `mvo explain --schedule` renders them and
// re-scores nothing, so the allocation rule may be rewritten, retuned or
// replaced, forever, without changing what a past race is reported to have
// bought — the same freedom M1e decision 21 bought `mvo explain` by making
// it derived, arrived at from the opposite direction.
//
// EXACTLY ONE COPY (decision 17). No CAS artifact duplicating the events,
// because two sources that can disagree is the failure M1f decision 9 exists
// to refuse. The events are OBSERVATIONAL: no payload digests, covered by
// the ledger hash chain that `mvo audit` already verifies, ignored by
// replay, and read by Decide never.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
)

// The four ledger event types of the allocation trace.
//
// schedule.step and oracle.skipped are not redundant and neither subsumes
// the other. A step row says "NOT THIS BATCH" — the purchase was considered,
// priced, and lost to something with a better score, and it may still be
// bought two batches later. oracle.skipped says "NOT EVER" — the world's
// rung was abandoned for good. An operator asking "where did the budget go?"
// needs the first; an operator asking "what did we never find out?" needs
// the second.
const (
	EventStarted       = "schedule.started"
	EventStep          = "schedule.step"
	EventFinished      = "schedule.finished"
	EventOracleSkipped = "oracle.skipped"
)

// Started is schedule.started: everything an allocation needs to be
// AUDITABLE after the fact. The cost table moves as the workspace's ledger
// grows — which is correct behaviour, and which would silently make two
// races incomparable — so the snapshot the race actually allocated against
// is recorded here rather than re-derived at read time from a fit that has
// since moved (decision 13).
//
// Every field is always serialized (M1b decision 5: no omitempty games).
// M2b1 adds FOUR fields, all observational, all additive, all ignored by
// replay and none carrying a payload digest. Their absence on a pre-M2b1
// trace is normalized rather than guessed at: selector "voc" (EXACT — no
// earlier binary could record a trace for any arm but the adaptive one),
// budget_basis "actual", and world_order EMPTY, which the renderer reports
// as "unknown (pre-M2b1 trace)" and never as digest order, because inventing
// a past ordering is inventing evidence.
type Started struct {
	Budget      StartBudget `json:"budget"`
	BudgetBasis string      `json:"budget_basis"` // BudgetBasisActual | BudgetBasisPredicted
	Constants   Constants   `json:"constants"`
	CostTable   []CostRow   `json:"cost_table"`
	Intent      string      `json:"intent"`
	Mode        string      `json:"mode"`     // ModeDecision | ModeCollectInert
	Parallel    int         `json:"parallel"` // the dispatch degree k
	// Rotation is the replicate's order rotation ρ = r mod N (decision 3).
	// Over N replicates every candidate holds the head position exactly once,
	// which turns the depth-first arm's positional advantage into a measured
	// variance component instead of a confound.
	Rotation int    `json:"rotation"`
	Schedule string `json:"schedule"` // ScheduleAdaptive | ScheduleFixed | ScheduleFixedBudget
	Selector string `json:"selector"` // SelectorNameVOC | SelectorNameLadder
	// WorldOrder is the CONTROL-PLANE world order this race allocated in —
	// candidate ordinal ascending, rotated by Rotation. It is recorded
	// because under a binding budget the verification order decides who gets
	// verified at all, and an order nobody recorded is an allocation nobody
	// can audit.
	WorldOrder []string `json:"world_order"`
}

// StartBudget is the race's pinned oracle budget. 0 means UNBOUNDED, which
// is M1 semantics: every recorded intent keeps racing exactly as it did
// (decision 12).
type StartBudget struct {
	MaxOracleMS int64 `json:"max_oracle_ms"`
}

// Constants are the compiled scheduler constants in force for this race.
// They are recorded, not looked up at read time, because they are exactly
// the numbers M2d is going to sweep: a trace whose constants were re-read
// from the binary would describe an allocation nobody made.
type Constants struct {
	// AdaptiveRule is the rule THE BINARY uses for --schedule=adaptive by
	// default — a property of the build, constant across the arms of one
	// protocol run, and NOT the per-run selector: `--selector=voc2` records
	// `selector: "voc2"` while `adaptive_rule` still reports whatever the
	// build defaults to, because what the freeze has to notice is the BINARY
	// moving. It is therefore NOT a way to date a trace, and the renderer that
	// tried to date one with it was wrong: M2b.2 ships `voc` as the default,
	// so "voc" is what a pre-M2b.2 ledger AND a current race both record.
	//
	// It is here because M2d's freeze pinned the scheduler's NUMBERS and not
	// its RULE: M2b.2 changes no constant and no policy field, so the
	// mechanism that exists to make post-freeze tuning impossible to do
	// quietly would have failed to notice a change to the allocation rule
	// itself. An ABSENT value in a freeze file or an old trace normalizes to
	// "voc" EXACTLY rather than by assumption — no binary before M2b.2 could
	// allocate by any other rule (M2b.2 decision 8).
	AdaptiveRule string           `json:"adaptive_rule"`
	ExecutorBP   map[string]int64 `json:"executor_bp"`
	RedundancyBP map[string]int64 `json:"redundancy_bp"`
}

// CostRow is one kind's cost model as the race actually held it.
//
// PerUnitMicroMS is thousandths of a millisecond per unit — cost.go's
// Coefficient unit, kept rather than converted — because DP-1 forbids floats
// in canonical JSON and the trace is canonical JSON. 0.4 ms/test is 400.
//
// A CostBasisDeclaredRank row carries FixedMS = 0 and PerUnitMicroMS = 0 and
// those zeros are NOT a measurement of zero: below MinSamples there is no
// coefficient and v0 does not invent one (decision 7c). The renderer prints
// no millisecond figure for such a row, and DeclaredRankRow is the only
// constructor that builds one, so the two cannot drift apart.
type CostRow struct {
	Basis          string `json:"basis"`     // CostBasisFit | CostBasisDeclaredRank
	Dominant       string `json:"dominant"`  // OracleProfile.Dominant
	Estimator      string `json:"estimator"` // EstimatorTheilSen; "" for declared-rank
	FixedMS        int64  `json:"fixed_ms"`
	Kind           string `json:"kind"`
	N              int    `json:"n"`
	PerUnitMicroMS int64  `json:"per_unit_micro_ms"`
	PluginAutoload string `json:"plugin_autoload"` // the SEAL the fitted population ran under
	Rank           int    `json:"rank"`            // declared ordinal rank of Dominant; 0 when fitted
	Unit           string `json:"unit"`
}

// FitRow builds a fitted cost row. The seal is not optional: M2a amendment
// 27 measured plugin autoloading as a 4.4x lever on fixed cost, so a
// coefficient without its population's seal is an average of two populations
// that differ by four.
func FitRow(c Coefficient) CostRow {
	prof, _ := policy.KindProfile(c.Kind)
	return CostRow{
		Basis: CostBasisFit, Dominant: prof.Dominant, Estimator: c.Estimator,
		FixedMS: c.FixedMS, Kind: c.Kind, N: c.N, PerUnitMicroMS: c.PerUnitMicroMS,
		PluginAutoload: c.Seal, Rank: 0, Unit: prof.Unit,
	}
}

// DeclaredRankRow builds the no-fit row: the declared ordinal rank of the
// kind's dominant cost term, used as a RELATIVE cost, with no millisecond
// figure anywhere. n is the sample count that fell short, recorded so a
// reader learns how close this workspace is to a real measurement.
func DeclaredRankRow(kind string, rank int64, n int) CostRow {
	prof, _ := policy.KindProfile(kind)
	return CostRow{
		Basis: CostBasisDeclaredRank, Dominant: prof.Dominant, Estimator: "",
		FixedMS: 0, Kind: kind, N: n, PerUnitMicroMS: 0,
		PluginAutoload: "", Rank: int(rank), Unit: prof.Unit,
	}
}

// CostTable renders the cost model a race allocated against as the snapshot
// schedule.started records: one row per kind this race can buy, fitted where
// the workspace could fit it and declared-rank where it could not, in kind
// order.
//
// It is a SNAPSHOT and that is the whole point (decision 13). The fit moves
// as the workspace's ledger grows — correct behaviour, and it would silently
// make two races incomparable — so an auditor reads the coefficients the
// race actually used rather than re-deriving a fit that has since moved.
func CostTable(t *Table, kinds []string, sampleN map[string]int) []CostRow {
	fitted := map[string]Coefficient{}
	for _, c := range t.Coefficients() {
		fitted[c.Kind] = c
	}
	rows := make([]CostRow, 0, len(kinds))
	for _, kind := range kinds {
		if c, ok := fitted[kind]; ok {
			rows = append(rows, FitRow(c))
			continue
		}
		rows = append(rows, DeclaredRankRow(kind, declaredRank(kind), sampleN[kind]))
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Kind < rows[j].Kind })
	return rows
}

// CostBasisText renders the per-row `cost_basis` string a considered row
// carries: "fit(pytest-suite,off) n=37" or "declared-rank(rank 6, n=0)". One
// function, so the trace's per-row label and `mvo explain`'s cost-model
// block cannot disagree about what priced a purchase.
func (c CostRow) CostBasisText() string {
	if c.Basis == CostBasisDeclaredRank {
		return fmt.Sprintf("%s(rank %d, n=%d)", CostBasisDeclaredRank, c.Rank, c.N)
	}
	return Coefficient{Kind: c.Kind, Seal: c.PluginAutoload, N: c.N}.Basis()
}

// Considered is ONE prospective purchase as the scheduler priced it, and the
// full set of them for a batch is the substance of the trace: every action
// considered, its value, its cost, what was bought, and — the row an
// operator actually reads — what was DECLINED and why.
type Considered struct {
	// Admissible is the BUY test, kept separate from the score because they
	// answer different questions (decision 3c). A purchase is admissible iff
	// some bracket outcome moves the decision (flip == 1) OR a hard gate
	// reads the rung — withholding a hard-gated receipt fabricates a gate
	// failure, which is a decision change the ordering terms may not veto.
	Admissible bool   `json:"admissible"`
	Affordable bool   `json:"affordable"`
	Basis      string `json:"basis"` // BasisDecision | BasisResearch
	// Committed is M2b.2 decision 3: this world is in the commit set C at this
	// step — the pool is reserved to FINISH it, so every one of its remaining
	// rungs is affordable until it completes or is eliminated. It is
	// recomputed every step and is never a property of the world: elimination
	// releases a world's whole reserve immediately and C re-forms in the same
	// step.
	Committed bool   `json:"committed"`
	CostBasis string `json:"cost_basis"` // CostRow.CostBasisText()
	CostMS    int64  `json:"cost_ms"`    // PREDICTED; the receipt records actual
	Declined  string `json:"declined"`   // "" when bought
	// FinishMS is Σ ĉost_ms over this world's UNBOUGHT ladder, in policy gate
	// order: the cost to make the world capable of being `Subject` at all,
	// because under M2a's purchase law a world holding two of three rungs is
	// worth exactly what a world holding zero is worth. 0 means UNKNOWN — a
	// declared-rank rung has no millisecond figure and this rule does not
	// invent one — and a `voc` row carries no finish cost at all.
	FinishMS   int64 `json:"finish_ms"`
	DiscountBP int64 `json:"discount_bp"`
	ExecutorBP int64 `json:"executor_bp"`
	Flip       int   `json:"flip"` // 0 | 1 — a reachability test, NOT a probability
	// FlipOutcomes names what each bracket outcome would have decided:
	// "fail-closed:REJECT(unchanged)", "pass-max:REJECT->SELECT mv0:aa1…".
	// It is what makes flip auditable instead of asserted.
	FlipOutcomes []string `json:"flip_outcomes"`
	HardGate     bool     `json:"hard_gate"`
	Kind         string   `json:"kind"`   // registry kind
	Oracle       string   `json:"oracle"` // POLICY-LOCAL instance name
	// ScoreBPPS is basis points per SECOND and exists only for a row priced
	// from a fit. ScoreRank is the declared ordinal position (1..7) an
	// unpriced row is ordered by instead. They are separate fields because
	// they are separate units: one field carrying both would be a
	// milliseconds-and-rank average, and M2d would aggregate it across kinds
	// and races without ever seeing the seam.
	ScoreBPPS int64 `json:"score_bpps"`
	ScoreRank int64 `json:"score_rank"`
	// ScoreBasis says WHICH DENOMINATOR priced this row: ScoreBasisRung is
	// M2b's rung cost, ScoreBasisFinish is M2b.2's cost to finish the world
	// under scarcity. They share a unit and measure different things, so no
	// aggregate may pool them. "" is a ladder row, which computed no score at
	// all and renders "—".
	ScoreBasis string `json:"score_basis"`
	// Order is the LADDER arm's depth-first rank of this row within its own
	// step — 1 for the world the arm is currently completing — and 0 on a VOC
	// row, which has no depth-first rank at all. It is the one field a ladder
	// row carries that a VOC row does not, and it is the reason a reader can
	// reconstruct the arm's order from the trace alone.
	Order   int    `json:"order"`
	ValueBP int64  `json:"value_bp"`
	World   string `json:"world"`
}

// Bought reports whether this row was purchased in its own batch.
func (c Considered) Bought() bool { return c.Declined == "" }

// Chosen is one purchase the batch dispatched.
type Chosen struct {
	Oracle string `json:"oracle"`
	Reason string `json:"reason"`
	World  string `json:"world"`
}

// ReasonTopScore is the VOC arm's ordinary purchase reason, kept as a
// constant so every step spells it identically and a reader can grep a ledger
// for the purchases that were NOT ordinary.
const ReasonTopScore = "highest score_bpps among affordable frontier"

// ReasonLadderOrder is the LADDER arm's ordinary purchase reason. It is a
// separate sentence because it is a separate fact: the depth-first arm buys
// no score at all, and recording "highest score_bpps" beside a row whose
// score is absent would put a rule nobody ran into the permanent record.
const ReasonLadderOrder = "next rung of the leading world in control-plane order"

// ReasonFinishScore is the VOC2 arm's purchase reason UNDER SCARCITY, and it
// is a separate sentence because it is a separate fact: the score that put
// this row at the head of the queue was divided by the cost to FINISH the
// world, not by the cost of the rung, and a trace that spelled them the same
// way would hide the one change the block exists to make.
const ReasonFinishScore = "highest score_bpps (finish denominator) among affordable frontier purchases"

// There is no cohort-close reason, and its absence is recorded rather than
// left as a gap. §2.2 and §10 describe `corpus-differential` as a dependent
// purchase whose choice CLOSES the cohort; v0 does not schedule it — phase
// B2 runs it as a barrier over every world that observed, after phase B, in
// both arms (§10, amended). A constant naming a decision nothing makes is
// worse than no constant: it reads like an implemented path. `mvo explain
// --schedule` reports B2's receipts under "not scheduled" instead, so the
// trace stops implying completeness it does not have.

// BudgetState is the budget as it stood at the end of a batch. Released is
// separate from spent because an eliminated world's share returning to the
// pool is the mechanism that makes equal shares adaptive (decision 8), and a
// reader has to be able to see it happen.
type BudgetState struct {
	ReleasedMS  int64 `json:"released_ms"`
	RemainingMS int64 `json:"remaining_ms"`
	SpentMS     int64 `json:"spent_ms"`
}

// DecisionNow is Decide's verdict AS IF THE RACE STOPPED HERE. It costs one
// pure call (15.8 µs over six worlds) and it is the single most useful field
// in the trace for a reader reconstructing why a purchase looked valuable.
type DecisionNow struct {
	PassCount int      `json:"pass_count"`
	Subject   []string `json:"subject"`
	Type      string   `json:"type"`
}

// Step is one dispatch batch. The scheduler commits to k purchases before
// seeing the first result, so its lookahead is stale by up to k-1 receipts —
// ASHA's asynchronous halving, and ASHA's cost, recorded rather than
// glossed. Staleness says how many results the batch commits to before
// seeing the first, so a reader can see which purchases were scored against
// a decision that had already moved.
type Step struct {
	Batch  int         `json:"batch"`
	Budget BudgetState `json:"budget"`
	Chosen []Chosen    `json:"chosen"`
	// CommitBasis names WHICH RULE apportioned this step's pool:
	// CommitBasisNotScarce (M2b's, because the pool can finish everybody),
	// CommitBasisReserved (M2b.2's reservation), or CommitBasisUnpriced with
	// the kinds named (finish_ms is unknown, so the scarcity test is
	// undecidable and the race falls back to M2b for the whole race). "" is an
	// arm that holds no such concept, and renders "—".
	//
	// It is recorded because a reader has to be able to tell FROM THE TRACE
	// ALONE which rule allocated a race — the second of the three reasons the
	// revision is gated on scarcity at all.
	CommitBasis string `json:"commit_basis"`
	// CommitSet is the world digests the pool is committed to finishing, IN
	// COMMIT ORDER (M2b.2 decision 3). Empty under ¬scarce and on every arm
	// that reserves nothing.
	CommitSet   []string     `json:"commit_set"`
	Considered  []Considered `json:"considered"`
	DecisionNow DecisionNow  `json:"decision_now"`
	// Scarce is decision 1's regime AND decision 4's precondition, which
	// until now lived in prose: M2b decision 4's restored FAR claim holds
	// "whenever the budget can pay for every hard gate of every live world",
	// and that condition is exactly ¬scarce. A race whose every step records
	// `scarce: false` therefore carries the FAR claim with equality, provably,
	// from its own ledger.
	Scarce    bool `json:"scarce"`
	Staleness int  `json:"staleness"`
	Step      int  `json:"step"`
	// UncommittedMS is the pool the reservation deliberately did not commit:
	// what the worlds outside C divide equally, and what a world eliminated
	// mid-step releases back into.
	UncommittedMS int64 `json:"uncommitted_ms"`
}

// Finished is schedule.finished: the totals and, above all, THE STOP CLAUSE
// (decision 9). "We stopped because nothing left could change the decision"
// and "we stopped because the money ran out" are opposite facts about a
// race, and only one of them is a statement about the candidates.
type Finished struct {
	Bought     int         `json:"bought"`
	Budget     BudgetState `json:"budget"`
	Considered int         `json:"considered"`
	Declined   int         `json:"declined"`
	// RankingIncomplete records decision 4's one honest corner. Withholding
	// monotonicity holds for the PASS SET, not for the RANKING: among worlds
	// that all passed every hard gate, withholding a receipt a ranking key
	// reads makes that key unknown, unknown always loses (M1e decision 5),
	// and the winner can change. True means the race stopped with a passing
	// candidate missing such a receipt, so the ORDER below the pass set is
	// not covered by the safety claim — and the operator has to know that.
	RankingIncomplete bool `json:"ranking_incomplete"`
	// SelectionUS is the arm's own metalevel time in microseconds: the
	// frontier walk, the lookahead, the ranking and the batch fill, measured.
	// It is REPORTED AND NOT CHARGED (M2b1 F8): PRD §11's budget includes
	// selection cost, this harness charges only oracle milliseconds, and the
	// field exists so the claim that selection is 0.07–0.3% of the purchase
	// it prices stays re-checkable rather than inherited from a benchmark run
	// once.
	SelectionUS int64  `json:"selection_us"`
	Steps       int    `json:"steps"`
	Stop        string `json:"stop"` // Stop* — S-budget is the STARVED stop
	// Violation is the purchase-law assertion (decision 9), and it MUST
	// always be empty. "The prospective winner has paid for everything" is
	// not a condition the scheduler maintains — it is what a SELECT from
	// Decide MEANS, because an unpurchased hard gate leaves a required
	// metric absent and an absent required metric fails the gate. It is
	// recorded rather than merely asserted because an invariant this
	// load-bearing should be OBSERVED, and a non-empty value means the
	// decision rule handed to the scheduler admitted a world that did not
	// pay — a broken decision rule, not a scheduling bug.
	Violation string `json:"violation"`
}

// Skipped is oracle.skipped, M2a's event with M2b's meaning: the TERMINAL
// decline. Reason is the sentence an operator reads to learn what was never
// found out about a world.
type Skipped struct {
	Oracle string `json:"oracle"`
	Reason string `json:"reason"`
	World  string `json:"world"`
}

// Payload returns the canonical JSON bytes of one event body: keys sorted at
// every level, integers exact, no floats (DP-1). It is the same encoder
// every recorded object goes through, so a trace payload is byte-stable
// across Go versions and struct reorderings.
func Payload(v any) ([]byte, error) {
	b, err := object.Canonical(v)
	if err != nil {
		return nil, fmt.Errorf("schedule: encode trace event: %w", err)
	}
	return b, nil
}

// AppendFn is the ledger seam. internal/race passes its SERIALIZED appender
// (the race-owned mutex that gives every append one total order); the pure
// tests pass a collector. This package never imports internal/ledger, so
// nothing here can write to a log the caller did not hand it.
type AppendFn func(typ string, payload []byte) error

// Recorder is the Sink the scheduler writes its trace through: it turns
// authored rows into canonical event payloads and appends them. The
// scheduler authors and this records, which is what keeps the allocation
// rule free of the storage layer and keeps the payload shape in one place.
type Recorder struct{ append AppendFn }

// NewRecorder wraps an appender. A nil appender makes every method return an
// error rather than panic: a race that cannot record its trace must fail at
// the call site, loudly, instead of allocating in silence.
func NewRecorder(fn AppendFn) *Recorder { return &Recorder{append: fn} }

func (r *Recorder) emit(typ string, v any) error {
	if r == nil || r.append == nil {
		return fmt.Errorf("schedule: no ledger appender for %s", typ)
	}
	payload, err := Payload(v)
	if err != nil {
		return err
	}
	if err := r.append(typ, payload); err != nil {
		return fmt.Errorf("schedule: record %s: %w", typ, err)
	}
	return nil
}

// Started records the allocation's preamble: the budget, the parallelism
// degree, the COST TABLE SNAPSHOT and the compiled scheduler constants. That
// snapshot is what makes an allocation auditable — the fit moves as the
// workspace's ledger grows, which is correct behaviour and would silently
// make two races incomparable, so a reader sees the coefficients the race
// actually used rather than re-deriving a fit that has since moved.
func (r *Recorder) Started(s Started) error { return r.emit(EventStarted, normalizeStarted(s)) }

// Step records one dispatch batch.
func (r *Recorder) Step(s Step) error { return r.emit(EventStep, normalizeStep(s)) }

// Finished records the totals and the stop clause, then one oracle.skipped
// per terminal decline.
//
// The two are separate events on purpose. schedule.finished is a summary a
// reader consumes as a whole; oracle.skipped is M2a's per-rung record, and
// M2a's contract for it — {world, oracle, reason}, observational, ignored by
// replay — is unchanged. Folding the declines into the summary alone would
// break every consumer M2a wrote them for.
func (r *Recorder) Finished(f Finished, skipped []Skipped) error {
	if err := r.emit(EventFinished, f); err != nil {
		return err
	}
	for _, s := range skipped {
		if err := r.emit(EventOracleSkipped, s); err != nil {
			return err
		}
	}
	return nil
}

// Skipped records one terminal decline on its own, for the orchestrator's
// abandon-the-rest path (a world dropped mid-ladder gets one per remaining
// rung).
func (r *Recorder) Skipped(s Skipped) error { return r.emit(EventOracleSkipped, s) }

// normalizeStarted, normalizeStep and normalizeFinished replace nil slices
// with empty ones. A nil slice canonicalizes to `null`, and `null` is a lie
// about the shape of the record: "the scheduler considered nothing" and "the
// field is missing" are different facts and only one of them can be true
// (the EP-2 rule, applied to the trace).
func normalizeStarted(s Started) Started {
	// A pre-M2b1 trace carries neither field, and the defaults are EXACT
	// rather than assumed: only the adaptive arm could record a trace before
	// M2b1, and only actual wall-clock could charge the pool. WorldOrder is
	// left EMPTY on purpose — "unknown" is a fact and digest order would be
	// an invention.
	if s.Selector == "" {
		s.Selector = SelectorNameVOC
	}
	if s.BudgetBasis == "" {
		s.BudgetBasis = BudgetBasisActual
	}
	if s.WorldOrder == nil {
		s.WorldOrder = []string{}
	}
	if s.CostTable == nil {
		s.CostTable = []CostRow{}
	}
	if s.Constants.ExecutorBP == nil {
		s.Constants.ExecutorBP = map[string]int64{}
	}
	if s.Constants.RedundancyBP == nil {
		s.Constants.RedundancyBP = map[string]int64{}
	}
	// An ABSENT adaptive_rule means "voc" EXACTLY rather than by assumption:
	// no binary before M2b.2 could allocate by any other rule. This is M2b1
	// decision 6's normalization argument reused, because it is the same
	// argument — and it is the reason no byte of any frozen artifact has to
	// move for the freeze check to see the change (M2b.2 decision 8).
	if s.Constants.AdaptiveRule == "" {
		s.Constants.AdaptiveRule = SelectorNameVOC
	}
	return s
}

func normalizeStep(s Step) Step {
	if s.Chosen == nil {
		s.Chosen = []Chosen{}
	}
	if s.CommitSet == nil {
		s.CommitSet = []string{}
	}
	rows := make([]Considered, 0, len(s.Considered))
	for _, c := range s.Considered {
		if c.FlipOutcomes == nil {
			c.FlipOutcomes = []string{}
		}
		rows = append(rows, c)
	}
	s.Considered = rows
	if s.DecisionNow.Subject == nil {
		s.DecisionNow.Subject = []string{}
	}
	return s
}

// Trace is one race's complete recorded allocation trace, in ledger order.
// It is what `mvo explain --schedule` renders and what the waste metric
// reads. Nothing in it is recomputed: every field came off the ledger.
//
// HasStarted / HasFinished are explicit because their absence is meaningful.
// A trace with steps and no schedule.finished is a race that died
// mid-allocation, and reporting it as a completed allocation with no stop
// clause would be exactly the over-claim this project exists to remove.
type Trace struct {
	Started     Started
	HasStarted  bool
	Steps       []Step
	Finished    Finished
	HasFinished bool
	Skipped     []Skipped
}

// Empty reports whether the ledger recorded no allocation trace at all — a
// race run under the fixed ladder with no scheduler wired, or one run by a
// binary that predates M2b. Callers render "no allocation trace recorded",
// never a fabricated one: absent source implies absent metric.
func (t Trace) Empty() bool { return !t.HasStarted && len(t.Steps) == 0 }

// Rows returns every considered row across every step, in (step, input)
// order — the order the scheduler priced them in.
func (t Trace) Rows() []Considered {
	out := make([]Considered, 0, len(t.Steps)*2)
	for _, s := range t.Steps {
		out = append(out, s.Considered...)
	}
	return out
}

// PredictedMS sums the PREDICTED cost of every bought row THAT CARRIED A
// PREDICTION. A declared-rank row has none — cost_ms is 0 and that zero is
// not a measurement of zero (decision 7c) — so including it would put a
// partial sum on one side of the calibration residual and a total on the
// other, which is how a real race printed "predicted 2 ms, actual 2073 ms".
// The unpriced spend is a real number and belongs beside the residual, not
// inside it; `mvo explain --schedule` reports it on its own line.
func (t Trace) PredictedMS() int64 {
	var total int64
	for _, c := range t.Rows() {
		if c.Bought() && !strings.HasPrefix(c.CostBasis, CostBasisDeclaredRank) {
			total += c.CostMS
		}
	}
	return total
}

// Purchase is one trace row joined to the receipt it bought (or, for a
// declined row, to nothing).
type Purchase struct {
	Row     Considered
	Step    int
	Receipt string          // recorded receipt digest; "" when none was found
	Rec     *object.Receipt // nil when none was found
}

// Join matches every BOUGHT trace row to the receipt it bought.
//
// The join is not a name match and cannot be: a trace row names the
// POLICY-LOCAL oracle instance ("suite"), while a receipt names the registry
// kind plus the resolved-config digest — M1e decision 8 keeps the
// policy-local name out of receipts on purpose. The PINNED POLICY is what
// maps one to the other, which is why it is a parameter, and why a trace
// read against a different policy joins nothing rather than joining wrongly.
//
// Ties are broken by smallest digest, the same order-independent rule
// Decide's counted receipt uses. Under decision 10 (no repurchase, ever)
// there is at most one candidate anyway; the tie-break is there so that a
// hand-built or future multi-purchase trace still reads deterministically.
func (t Trace) Join(pol policy.Policy, receipts []object.RecordedReceipt) []Purchase {
	out := make([]Purchase, 0, len(t.Steps))
	used := map[string]bool{}
	for _, s := range t.Steps {
		for _, row := range s.Considered {
			if !row.Bought() {
				continue
			}
			p := Purchase{Row: row, Step: s.Step}
			if spec, ok := pol.OracleByName(row.Oracle); ok {
				best := ""
				var bestRec *object.Receipt
				for i := range receipts {
					rr := &receipts[i]
					if rr.Receipt.World != row.World || used[rr.Digest] {
						continue
					}
					if rr.Receipt.Oracle.ID != spec.Kind || rr.Receipt.Oracle.Config != spec.Config {
						continue
					}
					if best == "" || rr.Digest < best {
						best, bestRec = rr.Digest, &rr.Receipt
					}
				}
				if bestRec != nil {
					used[best] = true
					p.Receipt, p.Rec = best, bestRec
				}
			}
			out = append(out, p)
		}
	}
	return out
}

// BasisOf maps recorded receipt digests to the basis of the purchase that
// bought them. A receipt with no trace row — a fixed-ladder purchase, or one
// from a race that predates the scheduler — maps to BasisDecision: it was
// bought to decide something, which is what every M1 rung was for.
func (t Trace) BasisOf(pol policy.Policy, receipts []object.RecordedReceipt) map[string]string {
	out := make(map[string]string, len(receipts))
	for _, p := range t.Join(pol, receipts) {
		if p.Receipt == "" {
			continue
		}
		basis := p.Row.Basis
		if basis == "" {
			basis = BasisDecision
		}
		out[p.Receipt] = basis
	}
	return out
}

// DeclinedRows returns every row that was considered and NOT bought, in
// trace order, paired with its step. This is the half of the record that
// exists nowhere else: a receipt proves what was bought, and nothing but the
// trace proves what was weighed and refused.
func (t Trace) DeclinedRows() []Purchase {
	out := make([]Purchase, 0, 4)
	for _, s := range t.Steps {
		for _, row := range s.Considered {
			if !row.Bought() {
				out = append(out, Purchase{Row: row, Step: s.Step})
			}
		}
	}
	return out
}

// CostBasisByKind returns the cost basis string each kind was priced under,
// taken from the RECORDED rows rather than from the current cost table. A
// kind whose rows all say `declared-rank` had no local measurement at the
// time of the race, and no millisecond figure may be printed for it however
// many receipts the workspace has accumulated since.
func (t Trace) CostBasisByKind() map[string]string {
	out := map[string]string{}
	for _, r := range t.Rows() {
		if r.Kind == "" || r.CostBasis == "" {
			continue
		}
		if prev, ok := out[r.Kind]; ok && prev != r.CostBasis {
			// Two different bases for one kind in one race means the cost
			// table moved mid-race, which it cannot: the snapshot is taken
			// once. Keep the first and let the rendering show it rather than
			// inventing a merged label.
			continue
		}
		out[r.Kind] = r.CostBasis
	}
	return out
}

// Unmeasured reports whether a kind was priced WITHOUT a local fit. The
// renderer asks before printing any millisecond figure, because M2a's rule —
// "no local measurement (n=…)", never a two-point fit dressed up as a fact —
// is exactly as binding on the allocator as it was on the report.
func (t Trace) Unmeasured(kind string) bool {
	return strings.HasPrefix(t.CostBasisByKind()[kind], CostBasisDeclaredRank)
}

// OracleOf maps recorded receipt digests to the POLICY-LOCAL oracle name of
// the purchase that bought them, so a rendering can say `bb2…/collect`
// rather than `bb2…/pytest-collect`.
func (t Trace) OracleOf(pol policy.Policy, receipts []object.RecordedReceipt) map[string]string {
	out := make(map[string]string, len(receipts))
	for _, p := range t.Join(pol, receipts) {
		if p.Receipt != "" {
			out[p.Receipt] = p.Row.Oracle
		}
	}
	return out
}

// CostRowFor returns the recorded cost-table row for a kind.
func (t Trace) CostRowFor(kind string) (CostRow, bool) {
	for _, c := range t.Started.CostTable {
		if c.Kind == kind {
			return c, true
		}
	}
	return CostRow{}, false
}

// DecodeStarted, DecodeStep, DecodeFinished and DecodeSkipped read an event
// payload back. They tolerate unknown fields by construction (encoding/json
// ignores them), which is the forward compatibility M1f decision 3 asks for:
// a later scheduler may record more, and this binary must still render what
// it understands rather than refusing the whole trace.
func DecodeStarted(b []byte) (Started, error) {
	var s Started
	if err := json.Unmarshal(b, &s); err != nil {
		return Started{}, fmt.Errorf("schedule: decode %s: %w", EventStarted, err)
	}
	return normalizeStarted(s), nil
}

// DecodeStep decodes one schedule.step payload.
func DecodeStep(b []byte) (Step, error) {
	var s Step
	if err := json.Unmarshal(b, &s); err != nil {
		return Step{}, fmt.Errorf("schedule: decode %s: %w", EventStep, err)
	}
	return normalizeStep(s), nil
}

// DecodeFinished decodes one schedule.finished payload.
func DecodeFinished(b []byte) (Finished, error) {
	var f Finished
	if err := json.Unmarshal(b, &f); err != nil {
		return Finished{}, fmt.Errorf("schedule: decode %s: %w", EventFinished, err)
	}
	return f, nil
}

// DecodeSkipped decodes one oracle.skipped payload.
func DecodeSkipped(b []byte) (Skipped, error) {
	var s Skipped
	if err := json.Unmarshal(b, &s); err != nil {
		return Skipped{}, fmt.Errorf("schedule: decode %s: %w", EventOracleSkipped, err)
	}
	return s, nil
}

// Event is one ledger row as Collect needs it: a type and payload bytes. It
// is a local shape rather than an internal/ledger import, so this package
// stays free of the storage layer and the pure tests need no database.
type Event struct {
	Type    string
	Payload []byte
}

// Collect assembles a Trace from a race's ledger events in seq order.
//
// The caller supplies the events of ONE race window — between that race's
// race.started and its decision.recorded — because a workspace holds many
// races and a trace assembled across two of them would describe an
// allocation nobody made.
//
// Steps are ordered by their RECORDED step index, not by ledger position:
// under parallel dispatch the appends are serialized by a mutex whose order
// is not the batch order, and a rendering that walked ledger order would
// print batch 3 above batch 2 on a machine with a different scheduler.
func Collect(events []Event) (Trace, error) {
	var t Trace
	for _, e := range events {
		switch e.Type {
		case EventStarted:
			s, err := DecodeStarted(e.Payload)
			if err != nil {
				return Trace{}, err
			}
			t.Started, t.HasStarted = s, true
		case EventStep:
			s, err := DecodeStep(e.Payload)
			if err != nil {
				return Trace{}, err
			}
			t.Steps = append(t.Steps, s)
		case EventFinished:
			f, err := DecodeFinished(e.Payload)
			if err != nil {
				return Trace{}, err
			}
			t.Finished, t.HasFinished = f, true
		case EventOracleSkipped:
			s, err := DecodeSkipped(e.Payload)
			if err != nil {
				return Trace{}, err
			}
			t.Skipped = append(t.Skipped, s)
		}
	}
	sort.SliceStable(t.Steps, func(i, j int) bool { return t.Steps[i].Step < t.Steps[j].Step })
	sort.SliceStable(t.Skipped, func(i, j int) bool {
		if t.Skipped[i].World != t.Skipped[j].World {
			return t.Skipped[i].World < t.Skipped[j].World
		}
		return t.Skipped[i].Oracle < t.Skipped[j].Oracle
	})
	return t, nil
}
