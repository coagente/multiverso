package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/policy"
	"github.com/coagente/multiverso/internal/publish"
	"github.com/coagente/multiverso/internal/race"
	"github.com/coagente/multiverso/internal/workspace"
)

// schemaExplainReport identifies `mvo explain --json` (CP-6's M1 ESCALATE
// payload).
const schemaExplainReport = "multiverso.dev/explain-report/v0"

// diffCap bounds one candidate patch rendered by --diffs. The full bytes
// always remain in CAS, and the marker names the key — a truncated render is
// never mistaken for the artifact.
const diffCap = 64 << 10

type explainGate struct {
	Label     string `json:"label"`
	Gate      string `json:"gate"`
	Oracle    string `json:"oracle"`
	Basis     string `json:"basis"`
	Threshold int64  `json:"threshold"`
}

type explainPolicy struct {
	Digest  string        `json:"digest"`
	Schema  string        `json:"schema"`
	Name    string        `json:"name"`
	Gates   []explainGate `json:"gates"`
	Ranking []string      `json:"ranking"`
}

type explainDiff struct {
	Rank      int    `json:"rank"`
	World     string `json:"world"`
	CASKey    string `json:"cas_key"`
	Bytes     int64  `json:"bytes"`
	Truncated bool   `json:"truncated"`
	Patch     string `json:"patch"`
}

// explainReport is the derived CP-6 payload. NOTHING here is stored: every
// field is recomputed from the ledger, CAS and the pinned policy by the same
// pure functions Decide uses, so improving the rendering can never invalidate
// a decision (M1e decision 21).
type explainReport struct {
	Schema          string                `json:"schema"`
	Decision        string                `json:"decision"`
	Type            string                `json:"type"`
	Intent          string                `json:"intent"`
	Title           string                `json:"title"`
	Policy          explainPolicy         `json:"policy"`
	Winner          string                `json:"winner"`
	Escalation      race.EscalationResult `json:"escalation"`
	Candidates      []race.CandidateTrace `json:"candidates"`
	Trace           []race.Comparison     `json:"trace"`
	Evidence        []explainEvidence     `json:"evidence"`
	Rationale       string                `json:"rationale"`
	Freshness       string                `json:"freshness"`
	FreshnessDetail string                `json:"freshness_detail"`
	Diffs           []explainDiff         `json:"diffs,omitempty"`
	receiptByDigest map[string]object.Receipt
}

// explainEvidence annotates one evidence receipt: what produced it, for
// which world, with what verdict and metrics.
type explainEvidence struct {
	Digest  string           `json:"digest"`
	Oracle  string           `json:"oracle"`
	World   string           `json:"world"`
	Status  string           `json:"status"`
	Metrics map[string]int64 `json:"metrics"`
}

func cmdExplain(args []string, stdout, stderr io.Writer) error {
	digArg, rest := splitDigestArg(args)
	fs := newFlagSet("explain", stderr)
	dir := fs.String("dir", ".", "repository directory")
	jsonOut := fs.Bool("json", false, "emit the machine-readable explain report")
	diffs := fs.Int("diffs", 0, "append the top-N ranked candidates' captured patches")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	digArg, err := positionalDigest(digArg, fs, "explain")
	if err != nil {
		return err
	}
	if *diffs < 0 {
		return usagef("explain: --diffs must not be negative")
	}

	ws, err := workspace.Open(*dir)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}
	defer ws.Close()
	st, err := loadState(ws.Ledger)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}
	intentDig, intent, err := st.resolveIntent(digArg)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}

	var found *decisionRec
	for i := range st.Decisions {
		if st.Decisions[i].Decision.Intent == intentDig {
			found = &st.Decisions[i] // latest decision wins
		}
	}
	if found == nil {
		return fmt.Errorf("explain: no decision recorded for intent %s (run \"mvo race\" first)", intentDig)
	}
	dec := found.Decision

	// The pinned policy is what the decision was made under, whatever schema
	// version it was written in — CAS is never pruned, so it always resolves.
	pol, err := policy.Load(ws.CAS, dec.Policy)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}

	rep := explainReport{
		Schema:          schemaExplainReport,
		Decision:        found.Dig,
		Type:            dec.Type,
		Intent:          intentDig,
		Title:           intent.Spec.Title,
		Policy:          explainPolicyOf(pol),
		Escalation:      race.EscalationResult{},
		Candidates:      []race.CandidateTrace{},
		Trace:           []race.Comparison{},
		Evidence:        []explainEvidence{},
		Rationale:       dec.Rationale,
		receiptByDigest: map[string]object.Receipt{},
	}
	for _, rr := range st.Receipts {
		rep.receiptByDigest[rr.Dig] = rr.Receipt
	}
	if len(dec.Subject) > 0 && (dec.Type == race.TypeSelect || dec.Type == "ADMIT") {
		rep.Winner = dec.Subject[0]
	}

	// A race decision is re-derivable candidate by candidate; an admission
	// decision has one subject and no ranking, so it renders its evidence and
	// its rationale and stops there.
	if worlds, receipts, ok := raceWindow(st, found); ok {
		tr := race.Trace(pol, worlds, receipts)
		rep.Candidates = tr.Candidates
		rep.Trace = tr.Comparisons
		rep.Escalation = tr.Escalation
		if tr.Winner != "" {
			rep.Winner = tr.Winner
		}
	}
	for _, e := range dec.Evidence {
		rep.Evidence = append(rep.Evidence, evidenceRow(rep.receiptByDigest, e))
	}
	if *diffs > 0 {
		rep.Diffs = candidateDiffs(ws, rep.Candidates, *diffs)
	}
	// Trunk drift is a display concept, computed at render time — never a
	// ledger mutation (M1d decision 16).
	rep.Freshness, rep.FreshnessDetail = publish.TrunkDrift(*dir, intent.Base.Commit)

	if *jsonOut {
		if err := emitJSON(stdout, rep); err != nil {
			return fmt.Errorf("explain: %w", err)
		}
		return nil
	}
	writeExplain(stdout, rep)
	return nil
}

// raceWindow assembles a race decision's replay inputs exactly as audit does:
// the worlds and receipts recorded between the decision's own race.started
// and the decision itself. The second return reports whether the decision is
// a race decision at all.
func raceWindow(st *ledgerState, dr *decisionRec) ([]object.RecordedWorld, []object.RecordedReceipt, bool) {
	raceStart := st.raceStartBefore(dr.Decision.Intent, dr.Seq)
	admStart := st.admissionStartBefore(dr.Decision.Intent, dr.Seq)
	if admStart != nil && admStart.Seq > raceStart {
		return nil, nil, false
	}
	worldRecs := st.worldsFor(dr.Decision.Intent, raceStart, dr.Seq)
	worldDigs := make(map[string]bool, len(worldRecs))
	worlds := make([]object.RecordedWorld, 0, len(worldRecs))
	for _, wr := range worldRecs {
		worldDigs[wr.Dig] = true
		worlds = append(worlds, object.RecordedWorld{Digest: wr.Dig, World: wr.World})
	}
	receipts := make([]object.RecordedReceipt, 0)
	for _, rr := range st.receiptsFor(worldDigs, raceStart, dr.Seq) {
		receipts = append(receipts, object.RecordedReceipt{Digest: rr.Dig, Receipt: rr.Receipt})
	}
	return worlds, receipts, true
}

func explainPolicyOf(pol policy.Policy) explainPolicy {
	out := explainPolicy{
		Digest:  pol.Digest,
		Schema:  pol.Schema,
		Name:    pol.Name,
		Gates:   []explainGate{},
		Ranking: pol.KeyNames(),
	}
	for _, g := range pol.Gates {
		out.Gates = append(out.Gates, explainGate{
			Label: g.Label, Gate: g.Predicate, Oracle: g.Oracle,
			Basis: g.Basis, Threshold: g.Threshold,
		})
	}
	return out
}

func evidenceRow(byDigest map[string]object.Receipt, dig string) explainEvidence {
	row := explainEvidence{Digest: dig, Metrics: map[string]int64{}}
	rec, ok := byDigest[dig]
	if !ok {
		return row
	}
	row.Oracle, row.World, row.Status = rec.Oracle.ID, rec.World, rec.Result.Status
	for k, v := range rec.Result.Metrics {
		row.Metrics[k] = v
	}
	return row
}

// candidateDiffs loads the top-n ranked candidates' captured patches, each
// truncated at diffCap with an explicit marker naming the CAS key of the full
// artifact (CP-6's "top-k candidate diffs").
func candidateDiffs(ws *workspace.Workspace, cands []race.CandidateTrace, n int) []explainDiff {
	out := []explainDiff{}
	for i, c := range cands {
		if i >= n {
			break
		}
		d := explainDiff{Rank: c.Rank, World: c.World}
		if b, err := ws.GetObject(c.World); err == nil {
			var world object.World
			if err := json.Unmarshal(b, &world); err == nil {
				d.CASKey = world.Patch
				if patch, err := ws.CAS.Get(world.Patch); err == nil {
					d.Bytes = int64(len(patch))
					if len(patch) > diffCap {
						d.Truncated = true
						patch = patch[:diffCap]
					}
					d.Patch = string(patch)
				}
			}
		}
		out = append(out, d)
	}
	return out
}

// writeExplain renders the human surface: the header, the ordered gate table
// (first failure stops the ladder), the key-by-key comparison that decided
// the winner, the evidence with its metrics, and the frozen rationale.
func writeExplain(w io.Writer, rep explainReport) {
	fmt.Fprintf(w, "decision:  %s\n", rep.Decision)
	fmt.Fprintf(w, "type:      %s\n", rep.Type)
	fmt.Fprintf(w, "intent:    %s  (%s)\n", rep.Intent, rep.Title)
	name := rep.Policy.Name
	if name == "" {
		name = "-"
	}
	fmt.Fprintf(w, "policy:    %s  (%s, %s)\n", rep.Policy.Digest, name, policy.SchemaShort(rep.Policy.Schema))
	if rep.Winner != "" {
		// An ESCALATE has a leader, not a winner: the ranking put it first,
		// and a policy rule refused to call that a decision. Naming it
		// "winner" would launder exactly the ambiguity the rule reported.
		label := "winner:   "
		if rep.Type == race.TypeEscalate {
			label = "leader:   "
		}
		fmt.Fprintf(w, "%s %s\n", label, rep.Winner)
	}

	if len(rep.Candidates) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "gates (ordered, first failure stops the ladder):")
		tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
		header := []string{"  RANK", "WORLD", "ORD"}
		for _, g := range rep.Policy.Gates {
			header = append(header, g.Label)
		}
		header = append(header, "RESULT")
		fmt.Fprintln(tw, strings.Join(header, "\t"))
		for _, c := range rep.Candidates {
			row := []string{fmt.Sprintf("  %d", c.Rank), c.World, fmt.Sprintf("%d", c.Ordinal)}
			for _, g := range c.Gates {
				row = append(row, gateCell(g, rep.gateMetrics(g)))
			}
			result := "FAIL"
			if c.Pass {
				result = "PASS"
			}
			row = append(row, result)
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
		_ = tw.Flush()
	}

	if len(rep.Trace) > 0 && rep.Winner != "" {
		// An escalated race was not decided. Titling its ranking walk "why X
		// won" and marking a row "WINNER X ← decided here" — three lines
		// above the block reporting that a policy rule refused to call it a
		// decision — would launder exactly the ambiguity the rule reported,
		// and under on_ranking_tie the only key that separates the leaders is
		// the terminal digest order: the coin flip the rule exists to refuse.
		escalated := rep.Escalation.Rule != ""
		fmt.Fprintln(w, "")
		if escalated {
			fmt.Fprintf(w, "ranking walk for leader %s  (no winner: escalated by %s; ranking [%s]):\n",
				rep.Winner, rep.Escalation.Rule, strings.Join(rep.Policy.Ranking, ", "))
		} else {
			fmt.Fprintf(w, "why %s won  (ranking [%s]):\n", rep.Winner, strings.Join(rep.Policy.Ranking, ", "))
		}
		byWorld := make(map[string]race.CandidateTrace, len(rep.Candidates))
		for _, c := range rep.Candidates {
			byWorld[c.World] = c
		}
		leader := rep.Candidates[0]
		for _, cmp := range rep.Trace {
			other := byWorld[cmp.Other]
			verdict, lead := "WINNER "+rep.Winner+"   ← decided here", "decided"
			if escalated {
				verdict, lead = "leads here   ← not a decision (escalated)", "leads"
				if cmp.Key == policy.KeyWorldDigestAsc {
					verdict, lead = "tie-break only — not a decision", "separated only by the tie-break"
				}
			}
			if len(cmp.Steps) == 0 {
				fmt.Fprintf(w, "  vs %s (rank %d): %s at key %d %s (%s)\n",
					cmp.Other, cmp.OtherRank, lead, cmp.DecidedAt, cmp.Key, cmp.Text)
				continue
			}
			fmt.Fprintf(w, "  vs %s (rank %d)\n", cmp.Other, cmp.OtherRank)
			tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
			for _, s := range cmp.Steps {
				lv, rv := keyText(leader, s.Index), keyText(other, s.Index)
				fmt.Fprintf(tw, "    %d\t%s\t%s\t=\t%s\t%s\n", s.Index, s.Key, lv, rv, s.Result)
			}
			lv, rv := keyText(leader, cmp.DecidedAt), keyText(other, cmp.DecidedAt)
			op := "<"
			if strings.Contains(cmp.Text, ">") {
				op = ">"
			}
			fmt.Fprintf(tw, "    %d\t%s\t%s\t%s\t%s\t%s\n", cmp.DecidedAt, cmp.Key, lv, op, rv, verdict)
			_ = tw.Flush()
		}
	}

	if rep.Escalation.Rule != "" {
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "escalation: %s\n", rep.Escalation.Rule)
		fmt.Fprintf(w, "            %s\n", rep.Escalation.Detail)
	}

	fmt.Fprintln(w, "")
	label := "evidence: "
	if len(rep.Evidence) == 0 {
		fmt.Fprintf(w, "%s (none)\n", label)
	}
	for _, e := range rep.Evidence {
		fmt.Fprintf(w, "%s %s%s\n", label, e.Digest, evidenceNote(e))
		label = "          "
	}
	fmt.Fprintf(w, "rationale: %s\n", rep.Rationale)
	fmt.Fprintf(w, "freshness: %s (%s)\n", rep.Freshness, rep.FreshnessDetail)

	for _, d := range rep.Diffs {
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "patch (rank %d) %s  [%s, %d bytes]\n", d.Rank, d.World, d.CASKey, d.Bytes)
		fmt.Fprint(w, d.Patch)
		if !strings.HasSuffix(d.Patch, "\n") && d.Patch != "" {
			fmt.Fprintln(w, "")
		}
		if d.Truncated {
			fmt.Fprintf(w, "… (truncated, full patch %s)\n", d.CASKey)
		}
	}
}

// gateMetrics is the metrics of the gate's OWN counted receipt — never the
// candidate's merged map. A policy may legally declare two instances of one
// kind (distinct resolved configs), and both emit the same metric names, so
// a number rendered next to a gate label is an attribution claim: it has to
// come from the receipt that gate was actually evaluated against, not from
// whichever counted receipt happened to sort first.
func (rep explainReport) gateMetrics(g race.GateResult) map[string]int64 {
	if g.Receipt == "" {
		return nil
	}
	return rep.receiptByDigest[g.Receipt].Result.Metrics
}

// gateCell renders one gate's outcome. A gate the ladder never reached is
// "n/a" — not-evaluated is not a failure, and the table must not imply one.
func gateCell(g race.GateResult, metrics map[string]int64) string {
	switch g.Result {
	case policy.GatePass:
		if note := passNote(g.Label, metrics); note != "" {
			return "pass " + note
		}
		return "pass"
	case policy.GateFail:
		return "FAIL (" + g.Detail + ")"
	default:
		return "n/a"
	}
}

// passNote shows the measurement a metric gate passed on, so a green table
// still carries numbers. metrics is the GATE's own receipt's map (see
// gateMetrics) — reading a merged, cross-oracle map here would be the worst
// kind of display bug in an evidence tool: a correct verdict beside a number
// another oracle measured.
func passNote(label string, metrics map[string]int64) string {
	predicate, _, _ := strings.Cut(label, "@")
	switch predicate {
	case policy.GateCollectedNotBelow:
		if v, ok := metrics[policy.MetricCollectedDelta]; ok {
			return fmt.Sprintf("(delta=%+d)", v)
		}
	case policy.GateCollectNonempty:
		if v, ok := metrics[policy.MetricCollectedTotal]; ok {
			return fmt.Sprintf("(total=%d)", v)
		}
	case policy.GateCoverageAtLeast:
		if v, ok := metrics[policy.MetricCoverageBP]; ok {
			return fmt.Sprintf("(bp=%d)", v)
		}
	}
	return ""
}

// keyText is a candidate's rendered value for the 1-based effective key
// index, or "-" when the trace does not carry it.
func keyText(c race.CandidateTrace, index int) string {
	if index < 1 || index > len(c.Keys) {
		return "-"
	}
	return c.Keys[index-1].Text
}

func evidenceNote(e explainEvidence) string {
	if e.Oracle == "" {
		return ""
	}
	parts := []string{e.Oracle + "@" + e.World, e.Status}
	names := make([]string, 0, len(e.Metrics))
	for k := range e.Metrics {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", k, e.Metrics[k]))
	}
	return "  (" + strings.Join(parts, ", ") + ")"
}
