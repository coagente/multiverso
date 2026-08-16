package object

// M0 object schemas (PRD §5 subset; see docs/design/M0.md) plus the M1e
// versioned policy artifact (CP-5; docs/design/M1e-policy-oracles.md).
const (
	SchemaIntent   = "multiverso.dev/intent/v0"
	SchemaWorld    = "multiverso.dev/world/v0"
	SchemaReceipt  = "multiverso.dev/receipt/v0"
	SchemaDecision = "multiverso.dev/decision/v0"
	SchemaPolicy   = "multiverso.dev/policy/v0"
	SchemaPolicyV1 = "multiverso.dev/policy/v1"
)

// Freshness bases (Freshness.Basis, GateSpec.Basis). The vocabulary is
// closed at three and ranked construction > dependency > probabilistic
// (M1e decision 11): a gate names the weakest evidence it will accept.
// M1's oracles emit only construction.
const (
	BasisConstruction  = "construction"
	BasisDependency    = "dependency"
	BasisProbabilistic = "probabilistic"
)

// World outcomes: the producing run's terminal state, normalized across
// agent adapters (AG-1, PRD §5.2; see docs/design/M1b-agent-adapters.md).
// Exactly one of the six appears in every world/v0 object. They live here
// because they are World-schema vocabulary (object has no deps).
const (
	OutcomeCompleted      = "COMPLETED"
	OutcomeBudgetExceeded = "BUDGET_EXCEEDED"
	OutcomeInterrupted    = "INTERRUPTED"
	OutcomeConfigError    = "CONFIG_ERROR"
	OutcomeProviderError  = "PROVIDER_ERROR"
	OutcomeCrash          = "CRASH"
)

// Isolation tiers (World.IsolationTier, Execution.IsolationTier) and
// network modes (IsolationCaps.Network). Recorded, never assumed (XP-1;
// see docs/design/M1c-containers-parallel.md).
const (
	TierT0Worktree  = "T0-worktree"
	TierT1Container = "T1-container"
	NetworkNone     = "none"
	NetworkDefault  = "default"
	NetworkHost     = "host"
)

// IsolationCaps records what actually confined an execution (XP-1/XP-2).
// All fields are always serialized; zero means uncapped, honestly.
type IsolationCaps struct {
	CapDrop      string `json:"cap_drop"`     // "ALL" | ""
	CPUMilli     int64  `json:"cpu_milli"`    // 0 = uncapped
	MemoryBytes  int64  `json:"memory_bytes"` // 0 = uncapped (MemoryMB << 20)
	Network      string `json:"network"`      // "none" | "default" | "host"
	PidsLimit    int64  `json:"pids_limit"`   // 0 = uncapped
	ReadOnlyRoot bool   `json:"read_only_root"`
	User         string `json:"user"` // effective user; "" = process default
}

// HostCaps is the T0 record: uncapped bare host, said plainly (PRD §9 —
// macOS has no cgroups, bare-host runs are uncappable and say so).
func HostCaps() IsolationCaps {
	return IsolationCaps{Network: NetworkHost}
}

// Base identifies the git state an intent starts from.
type Base struct {
	Commit string `json:"commit"`
	Tree   string `json:"tree"`
}

// Spec is the human description of an intent.
type Spec struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Budget bounds a race.
type Budget struct {
	MaxCandidates int   `json:"max_candidates"`
	MaxWallMS     int64 `json:"max_wall_ms"`
}

// Intent is a request for change against a base tree.
type Intent struct {
	Schema    string `json:"schema"` // multiverso.dev/intent/v0
	Base      Base   `json:"base"`
	Spec      Spec   `json:"spec"`
	Budget    Budget `json:"budget"`
	Policy    string `json:"policy"` // digest of Policy object
	CreatedAt string `json:"created_at"`
}

// Producer identifies what produced a world's patch.
type Producer struct {
	Adapter      string `json:"adapter"` // M0: "script@v0"
	Model        string `json:"model"`
	IdentityTier string `json:"identity_tier"` // M0: "claimed"
	Role         string `json:"role"`          // M0: "generator"
}

// RunCost accounts what producing a world cost (AG-2). All fields are
// always serialized; usd_micro is integer micro-USD (1 USD = 1,000,000 —
// DP-1 forbids floats in canonical JSON). Source records honestly where
// the numbers came from: "client-estimate" for anything a CLI reported
// about itself, "none" when nothing was reported.
type RunCost struct {
	WallMS    int64  `json:"wall_ms"`
	USDMicro  int64  `json:"usd_micro"`
	TokensIn  int64  `json:"tokens_in"`
	TokensOut int64  `json:"tokens_out"`
	Source    string `json:"source"` // "none" | "client-estimate"
}

// World is one candidate universe: base + patch + resulting tree. Fields
// referencing raw byte artifacts (patch, context, trace) hold CAS keys
// ("sha256:…"); fields referencing canonical objects hold "mv0:" digests.
// Context and trace payloads live only in the local CAS and are never
// published (DP-3).
type World struct {
	Schema        string   `json:"schema"`         // multiverso.dev/world/v0
	Intent        string   `json:"intent"`         // intent digest
	Tree          string   `json:"tree"`           // git tree digest after the run ("git:<sha1>")
	Env           string   `json:"env"`            // digest of env manifest
	IsolationTier string   `json:"isolation_tier"` // "T0-worktree"
	Producer      Producer `json:"producer"`
	Context       string   `json:"context"` // CAS key of the prompt bytes (DP-3)
	Patch         string   `json:"patch"`   // CAS key of the control-plane-captured diff (AG-4)
	// PatchBytes is len(captured patch): the ranking input patch_size_asc
	// must be evaluable by a pure function with no CAS access, and the
	// control plane holds the bytes at capture time (M1e decision 22).
	PatchBytes int64   `json:"patch_bytes"`
	Trace      string  `json:"trace"`   // CAS key of the raw transcript (AG-3)
	Cost       RunCost `json:"cost"`    // production cost (AG-2)
	Outcome    string  `json:"outcome"` // Outcome* — the full six-value taxonomy (AG-1)
	CreatedAt  string  `json:"created_at"`
}

// OracleRef identifies the oracle that produced a receipt.
type OracleRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Config  string `json:"config"` // digest of argv+timeout
}

// Evidence regimes (Execution.EvidenceRegime, EvidenceSpec.Regime). The
// regime is a property of ONE oracle run, decided by capability at run
// time and RECORDED, never assumed (M1f decision 13/14): a receipt that
// does not say how it was observed invites the reader to assume the
// strongest reading, which is exactly the failure the 2026-08 design
// partner study found.
const (
	RegimeControlPlane = "control-plane" // no candidate code executes at all
	RegimeIsolated     = "isolated"      // T1 + distinct oracle uid + read-only worktree
	RegimeStreamed     = "streamed"      // framed stream over a FIFO; any tier
	RegimeInTree       = "in-tree"       // M1e behaviour, explicitly declared only
	// RegimeDerived is M2a decision 3: a receipt whose metrics are
	// CONTROL-PLANE ARITHMETIC over inputs that were themselves observed
	// inside candidate processes. The reducer executes no candidate code
	// and touches no candidate tree, so `control-plane` is the tempting
	// label — and it would be an over-claim, because every byte it
	// consumed was produced by a `streamed` run. A derived receipt
	// therefore also names its evidence FLOOR in inputs["evidence_floor"],
	// so a reader learns from the receipt alone that the comparison is
	// arithmetic over candidate-influenced observations.
	RegimeDerived = "derived"
)

// Evidence floor / input provenance keys (Receipt.Inputs). The vocabulary
// is CLOSED (M2a decision 24): Inputs is provenance, not evidence, so it
// may not grow a key a gate could learn to read.
const (
	InputCorpus          = "corpus"
	InputCohort          = "cohort"
	InputBaseTree        = "base_tree"
	InputBaseObservation = "base_observation"
	InputDiffTarget      = "diff_target"
	InputMutantSelection = "mutant_selection"
	InputEvidenceFloor   = "evidence_floor"
)

// Execution records how the oracle ran. Argv is the IN-WORLD invocation
// (the verification command is the evidence; any docker exec wrapper is
// transport, reproducible from tier + caps + image digest — M1c decision
// 12). IsolationCaps is always serialized (PRD §5.3; no omitempty games).
//
// EvidenceRegime and EvidencePlugin join the envelope in M1f (decision
// 14), next to the field they are most like: they say HOW the numbers in
// result.metrics were observed and by which observer. Both are always
// serialized. Replay of pre-M1f ledgers is unaffected — M1e decision 1
// pairs every recorded object with the digest it was recorded under and
// never re-serializes.
type Execution struct {
	Argv           []string      `json:"argv"`
	ExitCode       int           `json:"exit_code"`
	DurationMS     int64         `json:"duration_ms"`
	IsolationTier  string        `json:"isolation_tier"`
	IsolationCaps  IsolationCaps `json:"isolation_caps"`
	EvidenceRegime string        `json:"evidence_regime"`
	EvidencePlugin string        `json:"evidence_plugin"` // plugin content address; "" when none ran
}

// Result is the oracle's verdict plus evidence artifacts and the metrics
// parsed out of them (EP-2). Metrics are integers only (DP-1 forbids
// floats) and their ABSENCE is meaningful: a metric whose structured source
// was unavailable is missing, never a fabricated zero. Tools names the
// structured sources that were available and used, mapped to their
// versions. Both maps are always serialized and must be non-nil at
// construction — a nil map canonicalizes to null, which no M1e-recorded
// receipt may contain (use NewResult).
type Result struct {
	Status  string            `json:"status"`  // "pass" | "fail" | "error"
	Metrics map[string]int64  `json:"metrics"` // parsed metric name -> value
	Tools   map[string]string `json:"tools"`   // structured source -> version
	// Detail is the control-plane's one-line, NON-NUMERIC summary of the
	// verdict — the single string a pure gate predicate may quote when a
	// count is not enough to act on. M1f's paths-unmodified gate needs the
	// name of the first offending path in its fail reason, and Decide has
	// no CAS access: a name that is not in the receipt cannot reach a
	// rationale. Always serialized; "" for every kind that has nothing to
	// add, which is every kind but tree-guard.
	Detail    string   `json:"detail"`
	Artifacts []string `json:"artifacts"` // CAS digests, fixed kind order
}

// NewResult builds a Result with non-nil metric and tool maps and the given
// artifacts in caller order. Every producer of a receipt should build its
// Result through here or set both maps explicitly: {} is "measured nothing",
// null is a lie about the shape of the record.
func NewResult(status string, artifacts ...string) Result {
	if artifacts == nil {
		artifacts = []string{}
	}
	return Result{
		Status:    status,
		Metrics:   map[string]int64{},
		Tools:     map[string]string{},
		Detail:    "",
		Artifacts: artifacts,
	}
}

// ValidFor pins the state a receipt's freshness applies to.
type ValidFor struct {
	Tree string `json:"tree"`
	Env  string `json:"env"`
}

// Freshness states the basis on which a receipt is considered current.
type Freshness struct {
	Basis    string   `json:"basis"` // M0: "construction"
	ValidFor ValidFor `json:"valid_for"`
}

// Cost accounts what a receipt cost to produce.
//
// Units and Unit are M2a decision 22's scaling denominator. wall_ms alone
// is unlearnable — 400 ms for eight tests and 400 ms for eight hundred are
// the same number and mean opposite things — so every receipt records the
// count of whatever its kind scales by. With it a scheduler fits
// wall_ms ≈ fixed + per_unit × units per repository per kind from the
// ledger it already has. Unknown is {0, ""}: honest absence, as everywhere.
type Cost struct {
	WallMS int64  `json:"wall_ms"`
	Units  int64  `json:"units"`
	Unit   string `json:"unit"` // "" iff Units == 0
}

// Correlation is SCHEDULER metadata (M2a decision 23): declared per kind,
// recorded per receipt, and NEVER read by Decide. Family stays where it is
// and means what it always meant — it is the v0 dialect's evidence
// selector, and giving it a second job would break that.
//
// It exists so "how independent is this purchase from the last one?" is
// computable from the ledger rather than hard-coded in a heuristic.
// Generator is the field that makes "ten tests written by one model are not
// ten observations" machine-readable, and it is the seam LLM-synthesized
// corpora arrive through with no contract change.
type Correlation struct {
	Signal    string `json:"signal"`    // tree-bytes | test-identity | test-outcomes | value-behavior | suite-adequacy
	Corpus    string `json:"corpus"`    // "" | "repo-suite" | a corpus digest
	Generator string `json:"generator"` // control-plane | base-tree | repo | repo+policy | model:<family>
	Executor  string `json:"executor"`  // candidate-process | control-plane
}

// Receipt is the evidence record of one oracle run in one world.
//
// Inputs and Correlation are M2a's additive envelope fields. Both follow
// M1b decision 5 — no omitempty games — so new receipts get NEW DIGESTS,
// exactly as in M1b, M1c, M1e and M1f, and replay of pre-M2a ledgers is
// unaffected because M1e decision 1 pairs each recorded object with the
// digest it was recorded under and never re-serializes it.
type Receipt struct {
	Schema      string    `json:"schema"` // multiverso.dev/receipt/v0
	World       string    `json:"world"`  // world digest
	Oracle      OracleRef `json:"oracle"`
	Execution   Execution `json:"execution"`
	Result      Result    `json:"result"`
	Freshness   Freshness `json:"freshness"`
	RecheckTier string    `json:"recheck_tier"` // "V1-replayable"
	Family      string    `json:"family"`       // "suite"
	Cost        Cost      `json:"cost"`
	// Inputs records the CONTROL-PLANE-SUPPLIED run-time inputs a metric
	// was derived against: provenance, not evidence (M2a decision 24).
	// oracle.config is what the policy chose and result is what was
	// measured; between them sat an unrecorded gap. Keys are the closed
	// Input* vocabulary; the map is ALWAYS serialized and is {} for every
	// M1 kind. Decide never reads it — a provenance field a gate could
	// read is a metric with extra steps — but the CAS sweep and mvo
	// explain do.
	Inputs      map[string]string `json:"inputs"`
	Correlation Correlation       `json:"correlation"`
	CreatedAt   string            `json:"created_at"`
}

// NoInputs is the empty, non-nil provenance map every M1-era kind carries:
// {} is "supplied nothing", null is a lie about the shape of the record
// (the EP-2 rule, applied to the new field).
func NoInputs() map[string]string { return map[string]string{} }

// Decision records the pure-function outcome of a race (NFR-1: replay must
// reproduce Type, Subject, Evidence, Rationale byte-for-byte).
type Decision struct {
	Schema    string   `json:"schema"` // multiverso.dev/decision/v0
	Type      string   `json:"type"`   // SELECT | REJECT | ESCALATE
	Intent    string   `json:"intent"`
	Subject   []string `json:"subject"`  // world digest(s); winner first for SELECT
	Evidence  []string `json:"evidence"` // receipt digests, sorted
	Policy    string   `json:"policy"`
	Rationale string   `json:"rationale"`
	CreatedAt string   `json:"created_at"`
}

// Policy is the v0 policy shape (multiverso.dev/policy/v0), FROZEN. New
// policies are PolicyV1; v0 objects are still loaded, compiled, and
// replayed exactly as M0 decided them (M1e decision 2). Nothing may edit
// this shape again: its digest is what old intents pinned and what old
// attestations name.
type Policy struct {
	Schema    string   `json:"schema"`     // multiverso.dev/policy/v0
	HardGates []string `json:"hard_gates"` // M0: ["suite-pass"]
	Ranking   []string `json:"ranking"`    // M0: ["gate_pass","wall_ms_asc"]
}

// PolicyV1 is a versioned policy artifact (CP-5): ordered hard gates that
// each name an oracle, a pass predicate, and the weakest freshness basis
// they accept; a lexicographic ranking spec (NEVER a weighted sum); and a
// closed set of escalation conditions. Every field is always serialized;
// every list is order-significant.
// The three M1f fields are ADDITIVE (M1f decision 3): an additive v1 field
// is legal iff its compiled default reproduces M1e semantics exactly, and
// no new rationale text may be emitted on any input an M1e policy can
// produce. `invariants` defaults empty (no new evaluation, no new
// sentence); `paths` is only consulted by a gate no M1e policy can name;
// `evidence` selects how FUTURE runs are observed and never enters Decide.
type PolicyV1 struct {
	Schema     string          `json:"schema"`     // SchemaPolicyV1
	Name       string          `json:"name"`       // authored name, e.g. "default"
	Oracles    []OracleSpec    `json:"oracles"`    // declared instances, name-sorted
	HardGates  []GateSpec      `json:"hard_gates"` // ORDERED: ladder and evaluation order
	Ranking    []string        `json:"ranking"`    // ORDERED lexicographic keys
	Escalation EscalationSpec  `json:"escalation"`
	Paths      PathSpec        `json:"paths"`      // M1f: the two frozen path classes
	Invariants []InvariantSpec `json:"invariants"` // M1f: name-sorted, closed vocabulary
	Evidence   EvidenceSpec    `json:"evidence"`   // M1f: how a run is OBSERVED
}

// PathSpec declares the two frozen path classes (M1f decision 6).
// `protected` is frozen against modification and deletion; `harness` is
// frozen against modification, deletion AND creation — a repository with
// no conftest.py today must not acquire one from an untrusted generator.
// Patterns use the closed glob grammar (decision 7), no regexp.
//
// ProtectedAdditions is a tri-state STRING, never a bool: "" must be
// distinguishable from an explicit "allow", or an old policy would become
// silently less checked the moment a field was added (decision 4).
type PathSpec struct {
	Protected          []string `json:"protected"`
	Harness            []string `json:"harness"`
	ProtectedAdditions string   `json:"protected_additions"` // "" ⇒ "allow" | "refuse"
}

// InvariantSpec names one closed invariant and binds its ROLES to declared
// oracle instances. The vocabulary fixes which metric each role supplies
// and how they are compared; the policy only says which instance fills
// which role (M1f decision 10).
type InvariantSpec struct {
	Name    string            `json:"name"`    // closed vocabulary
	Oracles map[string]string `json:"oracles"` // role -> declared oracle name
}

// EvidenceSpec selects how a run is OBSERVED. It never enters Decide; it
// constrains future runs only (M1f decision 3). Both fields are tri-state
// strings for decision 4's reason.
type EvidenceSpec struct {
	Regime     string `json:"regime"`     // "" ⇒ "auto" | "isolated" | "streamed" | "in-tree"
	Crosscheck string `json:"crosscheck"` // "" ⇒ "require" | "off"
	// PluginAutoload governs pytest's setuptools ENTRY-POINT plugin
	// autoloading inside the oracle run. "" ⇒ "off": the run sets
	// PYTEST_DISABLE_PLUGIN_AUTOLOAD=1, and the only plugins that load are
	// the ones mvo names on argv.
	//
	// M1f shipped without this and the red team walked straight through:
	// pytest imports pytest11 entry points declared by ANY *.egg-info or
	// *.dist-info on sys.path, the candidate tree root IS on sys.path, and
	// the plugin module may be named anything — so no finite harness glob
	// closes the surface. An entry-point plugin loads after `-p
	// mvo_evidence`, in the same interpreter, and can take the channel over
	// before the observer ever configures.
	//
	// "on" restores autoloading for repositories whose suites genuinely
	// need an installed plugin (pytest-asyncio, pytest-django). It is a
	// PINNED, ATTESTED policy choice with a stated cost, exactly like
	// crosscheck: "off" — not a runtime flag.
	PluginAutoload string `json:"plugin_autoload"` // "" ⇒ "off" | "on"
}

// OracleSpec declares one oracle instance. Kind selects the registry
// implementation; the remaining fields are its resolved configuration.
type OracleSpec struct {
	Name string   `json:"name"` // policy-local, ^[a-z0-9][a-z0-9._-]{0,31}$
	Kind string   `json:"kind"` // "command" | "pytest-collect" | "pytest-suite"
	Argv []string `json:"argv"` // command: the full argv (required)
	// pytest-*: runner prefix, default python3 -m pytest
	Args      []string `json:"args"`       // pytest-*: extra args after the fixed flags
	TimeoutMS int64    `json:"timeout_ms"` // 0 = the intent's max_wall_ms
	Coverage  bool     `json:"coverage"`   // pytest-suite: measure coverage when coverage.py is present
	Reruns    int      `json:"reruns"`     // pytest-suite: --reruns N when BOTH plugins are present
	// Corpus declares where the shared input corpus comes from (M2a).
	// OMITZERO, unlike every other field on this struct, and the reason is
	// the compatibility claim M2a makes and proves: the shipped default
	// policy must NOT move (it stays mv0:f207c3fa…). A field that always
	// serialized would rewrite the bytes of every policy that never heard
	// of a corpus, minting a new digest for a purely additive change and
	// forcing a migration M1e decision 2 exists to prevent. Absence is
	// exactly "this instance consumes no corpus", which is what every
	// pre-M2a policy meant.
	Corpus CorpusSpec `json:"corpus,omitzero"`
	// Mutation is the diff-scoped mutation oracle's configuration (M2a
	// decision 11). OMITZERO for the same reason as Corpus: a pre-M2a
	// policy declares no mutation rung, so its bytes must not move.
	Mutation MutationSpec `json:"mutation,omitzero"`
}

// MutationSpec is the mutation budget cap as a FIRST-CLASS, PINNED
// parameter (M2a decision 11). CP-5's rule is that the pinned policy
// determines the gate, and it survives here in a sharper form: the policy
// fixes the predicate, the threshold AND the ceiling. Within the ceiling a
// scheduler may buy fewer mutants; it may never buy more, and the receipt
// records all three numbers — the ceiling, what the tool generated, and
// what actually ran — so a reader can see how partial a partial score is
// without consulting the policy.
//
// There is no incremental-cache field and there will not be one (decision
// 18): mutmut's cache and cosmic-ray's session database are EVIDENCE
// STORES, and under M1e/M1f semantics they would live in the candidate's
// writable tree, where a planted cache asserting "all mutants killed" is a
// two-line forgery. Every run gets a fresh session in control-plane
// scratch.
type MutationSpec struct {
	Tool             string   `json:"tool,omitempty"`                  // "" ⇒ "auto" | "cosmic-ray" | "mutmut"
	MaxMutants       int64    `json:"max_mutants,omitempty"`           // 0 ⇒ 20
	MaxPerLine       int64    `json:"max_per_line,omitempty"`          // 0 ⇒ 2
	Operators        []string `json:"operators,omitempty"`             // [] ⇒ the tool's default set, sorted
	TimeoutPerMutant int64    `json:"timeout_per_mutant_ms,omitempty"` // 0 ⇒ 5 × the baseline suite run
}

// IsZero reports whether the spec declares nothing at all — the test
// `omitzero` applies, and the test ConfigDigest applies before it folds a
// mutation configuration into an instance's identity in evidence. A slice
// field makes the struct incomparable, so this cannot be `== MutationSpec{}`.
func (m MutationSpec) IsZero() bool {
	return m.Tool == "" && m.MaxMutants == 0 && m.MaxPerLine == 0 &&
		len(m.Operators) == 0 && m.TimeoutPerMutant == 0
}

// CorpusSpec declares where the shared input corpus comes from. The
// provider vocabulary is CLOSED; Module and File are repo-relative paths
// that COMPILE INTO paths.harness (M2a decision 14): a corpus or property
// module the candidate can edit is one that asserts whatever the candidate
// wants — the same class of hole as an editable conftest.py, closed by the
// same gate, at rung O-1, before any Python runs.
type CorpusSpec struct {
	Provider string `json:"provider,omitempty"`  // "" ⇒ none | "repo-suite" | "declared" | "hypothesis"
	File     string `json:"file,omitempty"`      // provider=declared: the corpus JSON, repo-relative
	Module   string `json:"module,omitempty"`    // provider=hypothesis: the property module
	CasesMax int64  `json:"cases_max,omitempty"` // 0 ⇒ 100; the materialization ceiling
	Seed     string `json:"seed,omitempty"`      // provider=hypothesis: 32 hex; "" ⇒ derived from the base tree
}

// GateSpec is one ordered hard gate: a predicate over one oracle's counted
// receipt, plus the weakest freshness basis that receipt may carry.
type GateSpec struct {
	Gate      string `json:"gate"`      // closed vocabulary (internal/policy)
	Oracle    string `json:"oracle"`    // a name declared in Oracles
	Basis     string `json:"basis"`     // Basis* — the weakest acceptable evidence
	Threshold int64  `json:"threshold"` // per-gate meaning; 0 when the gate takes no parameter
	// Scope is M2a decision 21: "" ⇒ "both" | "race" | "landing". A cohort
	// of one is not a comparison, so a differential gate at admission —
	// which has exactly one subject — would carry diff_cohort_n=1, absent
	// metrics, a failed gate and an admission that can never succeed.
	// OMITEMPTY for the same digest-stability reason as OracleSpec.Corpus:
	// "" compiles to "both", which is what every pre-M2a gate meant, so
	// its bytes must not move.
	Scope string `json:"scope,omitempty"`
}

// EscalationSpec is the CLOSED, purely declarative escalation rule set
// (M1e decision 6: no expression language, no eval). Zero values mean
// "rule off".
type EscalationSpec struct {
	MinCandidatesPassing       int      `json:"min_candidates_passing"` // 0 = off
	OnRankingTie               bool     `json:"on_ranking_tie"`         // max_risk/ambiguity
	RequireEvidence            []string `json:"require_evidence"`       // oracle names, sorted
	OnAllWorldsFailedMachinery bool     `json:"on_all_worlds_failed_machinery"`
	// OnInvariantViolation is M1f rule 0 — the HIGHEST precedence, above
	// on_all_worlds_failed_machinery (M1f decision 11). A silent REJECT
	// would file a detected forgery under "the tests didn't pass".
	// Inserting a rule at the front of the list cannot change any
	// historical evaluation: no M1e policy can declare an invariant.
	OnInvariantViolation bool `json:"on_invariant_violation"`
	// OnBehavioralSplit is M2a rule 1b: 0 = off, N = escalate when the
	// cohort partitions into at least N behaviour classes. An int whose
	// zero means off — matching MinCandidatesPassing rather than M1f
	// decision 4's tri-state strings — and the distinction is real:
	// decision 4 forbids a zero-value that makes an OLD policy silently
	// LESS checked, whereas "escalation rule off" is exactly what every
	// pre-M2a policy had. Reproducing the prior state is the requirement.
	//
	// It ships OFF in the default (M2a decision 9): with six candidates
	// from one model prior, splits may be routine, and an escalation rule
	// that fires on most races spends the operator's attention until they
	// stop reading. We do not know the rate; shipping it on would be
	// guessing with somebody else's attention.
	OnBehavioralSplit int `json:"on_behavioral_split,omitempty"`
}

// RecordedWorld and RecordedReceipt are the identity-carrying forms of a
// recorded object: the decoded value paired with the digest under which it
// was RECORDED, never a re-serialization of the decoded struct (M1e
// decision 1). Passing these to the decision functions is what lets M1e add
// fields to World and Receipt while still replaying pre-M1e ledgers
// byte-for-byte: nothing about an old decision is re-derived from a newer
// Go struct.
type RecordedWorld struct {
	Digest string
	World  World
}

// RecordedReceipt pairs a receipt with the digest it was recorded under.
type RecordedReceipt struct {
	Digest  string
	Receipt Receipt
}
