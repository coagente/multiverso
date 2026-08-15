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

// Execution records how the oracle ran. Argv is the IN-WORLD invocation
// (the verification command is the evidence; any docker exec wrapper is
// transport, reproducible from tier + caps + image digest — M1c decision
// 12). IsolationCaps is always serialized (PRD §5.3; no omitempty games).
type Execution struct {
	Argv          []string      `json:"argv"`
	ExitCode      int           `json:"exit_code"`
	DurationMS    int64         `json:"duration_ms"`
	IsolationTier string        `json:"isolation_tier"`
	IsolationCaps IsolationCaps `json:"isolation_caps"`
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
	Status    string            `json:"status"`    // "pass" | "fail" | "error"
	Metrics   map[string]int64  `json:"metrics"`   // parsed metric name -> value
	Tools     map[string]string `json:"tools"`     // structured source -> version
	Artifacts []string          `json:"artifacts"` // CAS digests, fixed kind order
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
type Cost struct {
	WallMS int64 `json:"wall_ms"`
}

// Receipt is the evidence record of one oracle run in one world.
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
	CreatedAt   string    `json:"created_at"`
}

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
type PolicyV1 struct {
	Schema     string         `json:"schema"`     // SchemaPolicyV1
	Name       string         `json:"name"`       // authored name, e.g. "default"
	Oracles    []OracleSpec   `json:"oracles"`    // declared instances, name-sorted
	HardGates  []GateSpec     `json:"hard_gates"` // ORDERED: ladder and evaluation order
	Ranking    []string       `json:"ranking"`    // ORDERED lexicographic keys
	Escalation EscalationSpec `json:"escalation"`
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
}

// GateSpec is one ordered hard gate: a predicate over one oracle's counted
// receipt, plus the weakest freshness basis that receipt may carry.
type GateSpec struct {
	Gate      string `json:"gate"`      // closed vocabulary (internal/policy)
	Oracle    string `json:"oracle"`    // a name declared in Oracles
	Basis     string `json:"basis"`     // Basis* — the weakest acceptable evidence
	Threshold int64  `json:"threshold"` // per-gate meaning; 0 when the gate takes no parameter
}

// EscalationSpec is the CLOSED, purely declarative escalation rule set
// (M1e decision 6: no expression language, no eval). Zero values mean
// "rule off".
type EscalationSpec struct {
	MinCandidatesPassing       int      `json:"min_candidates_passing"` // 0 = off
	OnRankingTie               bool     `json:"on_ranking_tie"`         // max_risk/ambiguity
	RequireEvidence            []string `json:"require_evidence"`       // oracle names, sorted
	OnAllWorldsFailedMachinery bool     `json:"on_all_worlds_failed_machinery"`
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
