package object

// M0 object schemas (PRD §5 subset; see docs/design/M0.md).
const (
	SchemaIntent   = "multiverso.dev/intent/v0"
	SchemaWorld    = "multiverso.dev/world/v0"
	SchemaReceipt  = "multiverso.dev/receipt/v0"
	SchemaDecision = "multiverso.dev/decision/v0"
	SchemaPolicy   = "multiverso.dev/policy/v0"
)

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

// World is one candidate universe: base + patch + resulting tree.
type World struct {
	Schema        string   `json:"schema"`         // multiverso.dev/world/v0
	Intent        string   `json:"intent"`         // intent digest
	Tree          string   `json:"tree"`           // git tree digest after patch ("git:<sha1>")
	Env           string   `json:"env"`            // digest of env manifest
	IsolationTier string   `json:"isolation_tier"` // "T0-worktree"
	Producer      Producer `json:"producer"`
	Patch         string   `json:"patch"`   // CAS digest of the .patch bytes
	Outcome       string   `json:"outcome"` // COMPLETED | CONFIG_ERROR | CRASH (M0 subset)
	CreatedAt     string   `json:"created_at"`
}

// OracleRef identifies the oracle that produced a receipt.
type OracleRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Config  string `json:"config"` // digest of argv+timeout
}

// Execution records how the oracle ran.
type Execution struct {
	Argv          []string `json:"argv"`
	ExitCode      int      `json:"exit_code"`
	DurationMS    int64    `json:"duration_ms"`
	IsolationTier string   `json:"isolation_tier"`
}

// Result is the oracle's verdict plus evidence artifacts.
type Result struct {
	Status    string   `json:"status"`    // "pass" | "fail" | "error"
	Artifacts []string `json:"artifacts"` // CAS digests (stdout, stderr)
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

// Policy declares hard gates and ranking for the decision function.
type Policy struct {
	Schema    string   `json:"schema"`     // multiverso.dev/policy/v0
	HardGates []string `json:"hard_gates"` // M0: ["suite-pass"]
	Ranking   []string `json:"ranking"`    // M0: ["gate_pass","wall_ms_asc"]
}
