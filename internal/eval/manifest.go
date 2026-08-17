package eval

// THE EVAL RUN MANIFEST, AND ITS OWN KEY (§6).
//
// MODULE-LAYOUT DELTA, NAMED. The design specifies `eval-run.json` and its
// signature in §6 without assigning a file; this is it, next to the report
// renderer that consumes it.
//
// WIRE DELTAS: NONE. This manifest is not a ledger event, not a receipt, not a
// policy field and not a world field. The eval plane writes only to
// $MVO_EVAL_HOME and to stdout.
//
// ITS OWN KEY, AND WHY THAT IS THE POINT. Research ch. 9 §3 asks for the
// experiment's own evidence→decision→verdict chain to be signed. Doing that
// with the ADMISSION key would give the experiment the ability to mint an
// admission, so the eval plane has a separate keypair in a separate directory
// under the eval home, and its envelope carries a payload type that no
// verifier of attestations accepts. An eval envelope therefore cannot be
// mistaken for an attestation even by a careless reader, which is the only
// standard worth designing to.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/signing"
)

// PayloadTypeEvalRun is the DSSE payload type for a signed eval run. It is
// NOT signing.PayloadTypeInToto: an eval envelope must never verify as an
// attestation.
const PayloadTypeEvalRun = "application/vnd.multiverso.eval-run+json"

// CanaryVerdict values. The verdict is reported; the TOKEN is printed only on
// a hit, where printing it is the point.
const (
	CanaryClean    = "clean"
	CanaryLeak     = "leak"
	CanaryNotInUse = "not-in-force"
)

// RunManifest is eval-run.json.
type RunManifest struct {
	Schema  string `json:"schema"`
	Corpus  string `json:"corpus"`
	Version string `json:"version"`
	Split   string `json:"split"`
	// FreezeDigest is the digest of the freeze file this run was checked
	// against, and Drift/Unfreeze record what moved and why it was allowed.
	FreezeDigest string        `json:"freeze_digest"`
	Drift        []FreezeDrift `json:"drift"`
	Unfreeze     string        `json:"unfreeze"`
	// BinaryDigest is the racing binary; PolicyDigest is what the races were
	// decided under.
	BinaryDigest string   `json:"binary_digest"`
	PolicyDigest string   `json:"policy_digest"`
	Arms         []string `json:"arms"`
	Replicates   int      `json:"replicates"`
	BudgetLevel  string   `json:"budget_level"`
	// PolicyConfig is the policy file every instance was raced under, empty
	// for the workspace default `mvo init` writes, and PolicyByInstance
	// records what each instance ACTUALLY used — which is not always the cell
	// policy, because an instance carrying a PolicyHint keeps its hint (the
	// hint is what makes that instance scheduler-relevant at all).
	//
	// Both fields exist because the first end-to-end run printed a per-cell
	// header reading `on_evidence_incomplete: ON` for a cell that had raced
	// the shipped default: scripts/eval.sh looped over two policy
	// configurations and passed neither to the runner, and the two cells'
	// manifests carried a byte-identical policy_digest. A cell that cannot
	// say what it ran under is a cell whose caption is a guess.
	PolicyConfig     string            `json:"policy_config"`
	PolicyByInstance map[string]string `json:"policy_by_instance"`
	// Unbudgeted names the rows whose DERIVED budget came out as 0, which
	// `mvo intent new` reads as unbounded. They carry the cell's budget-level
	// label and are not budget-matched, so they are printed above the metrics
	// rather than left for a reader to infer from a budget_ms of 0.
	Unbudgeted []string `json:"unbudgeted"`
	// ReferenceSpread records, per instance, every reference replicate's own
	// minspend and S beside the median that became the treatment level. B1 is
	// derived from that median, so B is a measurement, and a measurement taken
	// once is a draw: across three runs of the same cell one instance's minspend
	// came out 1961 / 1537 / 983 ms and B1 collapsed to 0 on a different
	// instance each time.
	ReferenceSpread []string `json:"reference_spread"`

	Rows    []Row        `json:"rows"`
	Metrics []ArmMetrics `json:"metrics"`
	Paired  []Paired     `json:"paired"`
	Tests   []Inference  `json:"tests"`

	Census Census `json:"census"`
	// ArmAbsences records arms that produced NO NUMBER on an instance, with
	// the reason. It is deliberately NOT the skip census: decision 1b's
	// vocabulary is about an INSTANCE being unscorable, and "the ledger holds
	// no receipt this derived arm reads" is a fact about the ARM. Filing it
	// as `tool-absent` would make a fully scored instance read as skipped,
	// which is what the first end-to-end run did.
	ArmAbsences []string `json:"arm_absences"`
	// NonConsult carries one proof object per instance. A run with an
	// unproved instance is a run whose numbers are not about scheduling.
	NonConsult []NonConsultation `json:"non_consultation"`
	// CanaryVerdict is the one-word summary; CanaryID says which canary was
	// in force without printing it.
	CanaryVerdict string   `json:"canary_verdict"`
	CanaryIDs     []string `json:"canary_ids"`
	// Derivations is the population's provenance, declines included.
	Derivations map[string][]Derived `json:"derivations"`
	// EvalRunCount is the published leaderboard-query count for this corpus
	// AFTER this run's line was appended.
	EvalRunCount int `json:"eval_run_count"`
	// Captions are the labels every number in this manifest carries.
	Captions []string `json:"captions"`
	Notes    []string `json:"notes"`
}

// EvalKeysDir is where the eval key lives: under the eval home, never in a
// workspace's .multiverso/keys. The path itself is the separation.
func EvalKeysDir(home string) string { return filepath.Join(home, "keys") }

// LoadOrCreateEvalKey loads the eval signing key, creating it on first use.
func LoadOrCreateEvalKey(home string) (*signing.Signer, error) {
	dir := EvalKeysDir(home)
	if s, err := signing.Load(dir); err == nil {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("eval: create eval keys dir: %w", err)
	}
	s, err := signing.Generate(dir)
	if err != nil {
		return nil, fmt.Errorf("eval: generate eval key: %w", err)
	}
	return s, nil
}

// SignRunManifest returns the canonical bytes and their DSSE envelope.
func SignRunManifest(s *signing.Signer, m RunManifest) ([]byte, signing.Envelope, error) {
	b, err := object.Canonical(m)
	if err != nil {
		return nil, signing.Envelope{}, fmt.Errorf("eval: canonicalize run manifest: %w", err)
	}
	env, err := signing.Sign(s, PayloadTypeEvalRun, b)
	if err != nil {
		return nil, signing.Envelope{}, fmt.Errorf("eval: sign run manifest: %w", err)
	}
	return b, env, nil
}

// ---------------------------------------------------------------------------
// The report
// ---------------------------------------------------------------------------

// Render renders the report. Three rules are structural rather than stylistic:
//
//  1. THE CENSUS PRINTS ABOVE THE METRICS, never in a footnote.
//  2. A CELL WITH NO SCORED INSTANCES PRINTS NO METRIC LINE AT ALL — not a
//     zero, not a dash in a table of numbers, no line. Acceptance step m2d-7a
//     asserts the ABSENCE of a number, which is the only way to test the rule,
//     so this function must not name the metrics it is declining to print.
//  3. EVERY NUMBER CARRIES ITS LABEL SET.
func (m RunManifest) Render() []string {
	var out []string
	out = append(out, fmt.Sprintf("eval run: corpus %s@%s split=%s arms=%s",
		m.Corpus, m.Version, orDash(m.Split), strings.Join(m.Arms, ",")))
	// The policy is printed by the HARNESS, from what it actually raced, and
	// per instance where the instances differ. A cell header that announces a
	// policy configuration the runner was never given is the bug this line
	// exists to make impossible to repeat.
	out = append(out, fmt.Sprintf("policy: %s %s", orDash(m.PolicyConfig), m.PolicyDigest))
	if len(m.PolicyByInstance) > 0 {
		var odd []string
		for _, id := range sortedKeys(m.PolicyByInstance) {
			if m.PolicyByInstance[id] != m.PolicyConfig {
				odd = append(odd, fmt.Sprintf("%s=%s", id, orDash(m.PolicyByInstance[id])))
			}
		}
		if len(odd) > 0 {
			out = append(out, "    NOT UNIFORM — these instances kept their own PolicyHint: "+
				strings.Join(odd, " ")+" (the hint is what makes them scheduler-relevant at all)")
		}
	}
	if m.Unfreeze != "" {
		out = append(out, "UNFREEZE: "+m.Unfreeze)
	}
	for _, d := range m.Drift {
		out = append(out, fmt.Sprintf("FREEZE DRIFT: %s frozen=%s now=%s", d.What, d.Frozen, d.Now))
	}
	// 1. The census, above everything.
	if !m.Census.Empty() {
		out = append(out, "skip census:")
		for _, l := range m.Census.Lines() {
			out = append(out, "  "+l)
		}
	}
	for _, u := range m.Unbudgeted {
		out = append(out, "NOT BUDGET-MATCHED: "+u)
	}
	for _, s := range m.ReferenceSpread {
		out = append(out, "reference spread: "+s)
	}
	for _, a := range m.ArmAbsences {
		out = append(out, "arm absent: "+a)
	}
	out = append(out, fmt.Sprintf("canary: %s (%s)", m.CanaryVerdict, strings.Join(m.CanaryIDs, " ")))
	for _, nc := range m.NonConsult {
		status := "PROVED"
		if !nc.Proved {
			status = "NOT PROVED"
		}
		out = append(out, fmt.Sprintf("non-consultation %s: %s (%d workspace(s) scanned, %d surface(s) unreadable)",
			nc.Instance, status, nc.WorkspacesScanned, nc.Leak.SkippedTotal()))
		for _, r := range nc.Refusals {
			out = append(out, "    refusal: "+r)
		}
	}
	// 2. The metrics — or nothing.
	scored := 0
	for _, mm := range m.Metrics {
		scored += mm.Instances
	}
	if scored == 0 {
		out = append(out, "no metric line is printed: no instance produced a scored row "+
			"(absent source implies absent metric; a zero here would be a claim nobody measured)")
		out = append(out, ArmMapping()...)
		return out
	}
	for _, mm := range m.Metrics {
		out = append(out, RenderArm(mm)...)
	}
	for i, p := range m.Paired {
		out = append(out, fmt.Sprintf("paired %s vs %s: both=%d %s-only=%d %s-only=%d neither=%d n=%d instance-slice(s) over %d independent bug(s) excluded=%d",
			p.ArmA, p.ArmB, p.BothHit, p.ArmA, p.AOnly, p.ArmB, p.BOnly, p.NeitherHit,
			p.Instances, p.Clusters, p.Excluded))
		for _, k := range sortedKeys(p.ExcludedWhy) {
			out = append(out, fmt.Sprintf("    excluded %s: %d", k, p.ExcludedWhy[k]))
		}
		if i < len(m.Tests) {
			t := m.Tests[i]
			if !t.Available {
				out = append(out, "    "+t.Refused)
			} else {
				out = append(out, fmt.Sprintf("    McNemar exact p=%.4f (discordant %d); %s CI [%.3f, %.3f] seed=%d resamples=%d",
					t.PValue, t.Discordant, t.Method, t.CILow, t.CIHigh, t.Seed, t.Resamples))
			}
		}
	}
	out = append(out, ArmMapping()...)
	out = append(out, "labels on every number above: "+strings.Join(m.Captions, " "))
	for _, n := range m.Notes {
		out = append(out, "note: "+n)
	}
	return out
}

// RenderArm renders one arm's row block.
func RenderArm(m ArmMetrics) []string {
	head := fmt.Sprintf("arm %s over %d instance(s)", m.Arm, m.Instances)
	if m.Clusters > 0 {
		head += fmt.Sprintf(" = %d independent bug(s)", m.Clusters)
	}
	out := []string{head + ":"}
	if len(m.Policies) > 1 {
		out = append(out, "    POOLED OVER MORE THAN ONE POLICY "+strings.Join(m.Policies, " ")+
			" — this line is an untagged aggregate and must not be quoted; "+
			"Compute is called per policy and a cell reaching here is a harness bug")
	}
	out = append(out, fmt.Sprintf("    TCAR        %s   (denominator: instances attempted)", m.TCAR))
	out = append(out, fmt.Sprintf("    FAR         %s   (denominator: admissions)", m.FAR))
	out = append(out, fmt.Sprintf("    ESC         %s   ESC_just %s", m.ESC, m.ESCJust))
	if m.TCARAdm.Present || m.FARAdm.Present {
		out = append(out, fmt.Sprintf("    TCAR_adm    %s   FAR_adm %s   admit divergences %d",
			m.TCARAdm, m.FARAdm, m.AdmitDivergences))
	} else {
		out = append(out, "    TCAR_adm / FAR_adm: absent — `mvo admit` was not run, "+
			"and an admit column equal to the select column would be a claim nobody verified")
	}
	cov := m.Coverage.String()
	if m.CoverageLowerBound {
		cov += " (LOWER BOUND: some candidates are unknown)"
	}
	out = append(out, "    coverage    "+cov)
	out = append(out, fmt.Sprintf("    FRR_label   %s", m.FRRLabel))
	out = append(out, fmt.Sprintf("    FRR_gates   %s", m.FRRGates))
	out = append(out, fmt.Sprintf("    FRR_reach   %s   <- the arm rejected, a correct candidate was there, "+
		"full evidence would have selected it, and the money was in the arm's pocket", m.FRRReachable))
	g := m.Regret
	out = append(out, fmt.Sprintf("    regret      total %d/%d = generation %d + gates %d + allocation %d + ranking %d + unscored %d",
		g.Total(), g.Instances, g.Generation, g.Gates, g.Allocation, g.Ranking, g.Unscored))
	out = append(out, fmt.Sprintf("                attainable (coverage - TCAR) %d; allocation split: avoidable %d, unwinnable %d, unknown-bound %d",
		g.Attainable(), g.AllocationAvoidable, g.AllocationUnwinnable, g.AllocationUnknownBound))
	if err := g.Closes(); err != nil {
		out = append(out, "    IDENTITY DOES NOT CLOSE: "+err.Error())
	}
	if m.Unstable > 0 {
		out = append(out, fmt.Sprintf("    unstable    %d row(s) whose modal decision did not reach the 2R/3 threshold: own bucket, excluded from the paired test", m.Unstable))
	}
	if m.ExpectationViolated > 0 {
		out = append(out, fmt.Sprintf("    expectation-violated %d: the generator's guess and the oracle disagree. "+
			"This is INFORMATION about the oracle's strength, not an error", m.ExpectationViolated))
	}
	// DECISION 12's COST AXIS, printed. An arm that read the whole ledger for
	// free would dominate everything for free, so the charge is a column beside
	// the accuracy and not a sentence in a design document. A9's scoring
	// milliseconds keep their own column: they are the cost of producing LABELS
	// and pooling them into the oracle budget would make the retrospective arm
	// look expensive at verification, which it is not.
	fp := "{} (declared empty: reads no receipt, charged 0)"
	if len(m.Footprint) > 0 {
		fp = strings.Join(m.Footprint, ",")
	}
	out = append(out, fmt.Sprintf(
		"    charged     oracle %d ms total / %d ms median per instance; scoring %d ms (A9's own currency, never pooled); footprint %s",
		m.OracleCostTotalMS, m.OracleCostMedianMS, m.ScoringTotalMS, fp))
	out = append(out, "    "+strings.Join(Captions(m), " "))
	return out
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// FamilyColumns splits rows by family AND by the policy each instance actually
// raced under, so the report can print BOTH DIRECTIONS SIDE BY SIDE, always,
// and so no printed cell is ever an aggregate over two policies.
//
// The family half is decision 8b: the same 5/5 REJECT is a plain failure on
// family A and the only correct answer on family B, and printing one column
// without the other is how this harness would produce a wrong published claim.
//
// The POLICY half is a fix. `inst.PolicyHint` overrides the cell's --policy
// unconditionally — an instance whose laundering vectors die at rung O-1 under
// the shipped guards is not scheduler-relevant without its relaxed-guard
// policy — so a cell captioned `default B1` could contain two instances raced
// under `no-paths`, whose empty protected-path set and `tests_passed_desc`
// ranking the adversarial corpus already measured as letting a padded deletion
// beat the honest fix. The harness printed a NOT UNIFORM warning and then
// pooled the numbers anyway, which is precisely the untagged aggregate
// decision 8 exists to stop. Splitting here makes every printed cell uniform
// by construction and puts the policy in the cell's own name.
func FamilyColumns(rows []Row) map[string][]Row {
	out := map[string][]Row{}
	for _, r := range rows {
		key := r.Family
		if r.Policy != "" {
			key += "@" + r.Policy
		}
		out[key] = append(out[key], r)
	}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool {
			if out[k][i].Instance != out[k][j].Instance {
				return out[k][i].Instance < out[k][j].Instance
			}
			return out[k][i].Arm < out[k][j].Arm
		})
	}
	return out
}
