package main

// M2a: `mvo explain`'s behaviour block — the class table and the
// distinguishing cases with their values.
//
// This rendering IS the product. A behavioural split is an ESCALATE, and an
// escalation that says only "your candidates disagree" is worth nothing to
// the maintainer who has to act on it. What they need is the INPUT and what
// each candidate returned on it, which is exactly what the
// control-plane-authored differential report holds.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/coagente/multiverso/internal/object"
	"github.com/coagente/multiverso/internal/oracle"
	"github.com/coagente/multiverso/internal/workspace"
)

// explainBehavior is the derived behaviour block. Like everything else in
// the report it is RECOMPUTED — from the receipts' comparison metrics and
// the report artifact in CAS — and stored nowhere.
type explainBehavior struct {
	Corpus            string                  `json:"corpus"`
	Provider          string                  `json:"provider"`
	Excluded          []string                `json:"excluded"`
	CasesCompared     int64                   `json:"cases_compared"`
	CasesIncomparable int64                   `json:"cases_incomparable"`
	CohortN           int64                   `json:"cohort_n"`
	Classes           []explainBehaviorClass  `json:"classes"`
	Distinguishing    []explainDistinguishing `json:"distinguishing"`
	Truncated         int64                   `json:"distinguishing_truncated"`
}

type explainBehaviorClass struct {
	ID             string   `json:"id"`
	Members        []string `json:"members"`
	AgreesWithBase bool     `json:"agrees_with_base"`
}

type explainDistinguishing struct {
	Case         string                  `json:"case"`
	Target       string                  `json:"target"`
	Call         string                  `json:"call"`
	Base         string                  `json:"base"`
	Observations []explainBehaviorAnswer `json:"observations"`
}

type explainBehaviorAnswer struct {
	World string `json:"world"`
	Value string `json:"value"`
}

// reportWire mirrors the differential report artifact's shape.
type reportWire struct {
	BaseObservation   string `json:"base_observation"`
	CasesCompared     int64  `json:"cases_compared"`
	CasesIncomparable int64  `json:"cases_incomparable"`
	Classes           []struct {
		AgreesWithBase bool     `json:"agrees_with_base"`
		ID             string   `json:"id"`
		Members        []string `json:"members"`
	} `json:"classes"`
	Cohort         []string `json:"cohort"`
	Corpus         string   `json:"corpus"`
	Excluded       []string `json:"excluded"`
	Provider       string   `json:"provider"`
	Distinguishing []struct {
		Args   []json.RawMessage          `json:"args"`
		Case   string                     `json:"case"`
		Kwargs map[string]json.RawMessage `json:"kwargs"`
		Target string                     `json:"target"`
		Base   struct {
			Outcome string          `json:"outcome"`
			Type    string          `json:"t"`
			Value   json.RawMessage `json:"v"`
		} `json:"base"`
		Observations []struct {
			Outcome string          `json:"outcome"`
			Type    string          `json:"t"`
			Value   json.RawMessage `json:"v"`
			World   string          `json:"world"`
		} `json:"observations"`
	} `json:"distinguishing"`
	Truncated int64 `json:"distinguishing_truncated"`
}

// behaviorBlock derives the block from the decision's receipts. It returns
// nil when no comparison receipt is present, which is every decision made
// under a policy with no differential — so an M1-era explain report is
// unchanged, byte for byte.
func behaviorBlock(ws *workspace.Workspace, byDigest map[string]object.Receipt, evidence []string) *explainBehavior {
	var reportKey string
	var cohortN int64
	for _, dig := range evidence {
		rec, ok := byDigest[dig]
		if !ok || rec.Oracle.ID != oracle.KindCorpusDifferential {
			continue
		}
		cohortN = rec.Result.Metrics["diff_cohort_n"]
		if len(rec.Result.Artifacts) > 0 {
			reportKey = rec.Result.Artifacts[0]
		}
		break
	}
	if reportKey == "" {
		return nil
	}
	raw, err := ws.CAS.Get(reportKey)
	if err != nil {
		// The report is gone. Saying nothing would be the over-claim this
		// project exists to remove, so the block still renders what the
		// receipts themselves carry and says the payload is missing.
		return &explainBehavior{CohortN: cohortN, Corpus: "(report unavailable)"}
	}
	var wire reportWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return &explainBehavior{CohortN: cohortN, Corpus: "(report unreadable)"}
	}
	out := &explainBehavior{
		Corpus: wire.Corpus,
		// Which provider produced the corpus is exactly the fact a reader
		// weighs the comparison by — decision 5 documents `repo-suite` as
		// nearly information-free — and the field was declared, serialized
		// in --json, and never assigned, so every consumer got "".
		Provider:          wire.Provider,
		Excluded:          wire.Excluded,
		CasesCompared:     wire.CasesCompared,
		CasesIncomparable: wire.CasesIncomparable,
		CohortN:           int64(len(wire.Cohort)),
		Classes:           []explainBehaviorClass{},
		Distinguishing:    []explainDistinguishing{},
		Truncated:         wire.Truncated,
	}
	if out.CohortN == 0 {
		out.CohortN = cohortN
	}
	for _, c := range wire.Classes {
		out.Classes = append(out.Classes, explainBehaviorClass{
			ID: c.ID, Members: c.Members, AgreesWithBase: c.AgreesWithBase,
		})
	}
	for _, d := range wire.Distinguishing {
		args := make([]string, 0, len(d.Args)+len(d.Kwargs))
		for _, a := range d.Args {
			args = append(args, oracle.RenderValue(a))
		}
		names := make([]string, 0, len(d.Kwargs))
		for k := range d.Kwargs {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			args = append(args, k+"="+oracle.RenderValue(d.Kwargs[k]))
		}
		row := explainDistinguishing{
			Case:         d.Case,
			Target:       d.Target,
			Call:         callText(d.Target, args),
			Base:         answerText(d.Base.Outcome, d.Base.Type, d.Base.Value),
			Observations: []explainBehaviorAnswer{},
		}
		for _, o := range d.Observations {
			row.Observations = append(row.Observations, explainBehaviorAnswer{
				World: o.World, Value: answerText(o.Outcome, o.Type, o.Value),
			})
		}
		out.Distinguishing = append(out.Distinguishing, row)
	}
	return out
}

// callText renders the input the way a maintainer would type it:
// `stats:clamp(nan, 0, 10)`.
func callText(target string, args []string) string {
	return target + "(" + strings.Join(args, ", ") + ")"
}

// answerText renders one world's answer. A raise shows its TYPE, because
// the message is deliberately not part of the observation; an opaque or
// errored case says so rather than showing nothing.
func answerText(outcome, typeName string, value json.RawMessage) string {
	switch outcome {
	case oracle.OutcomeRaise:
		return "raise " + typeName
	case oracle.OutcomeOpaque:
		return "<opaque " + typeName + ">"
	case oracle.OutcomeError, oracle.OutcomeTimeout:
		return "(" + outcome + ")"
	}
	if len(value) == 0 {
		return "(value too large; fingerprint only)"
	}
	return oracle.RenderValue(value)
}

// writeBehavior renders the block. It sits ABOVE the escalation line, so a
// reader meets the evidence before the verdict it produced.
func writeBehavior(w io.Writer, b *explainBehavior) {
	if b == nil {
		return
	}
	fmt.Fprintln(w)
	// The provider is in the header because it is what a reader weighs the
	// whole comparison by: `repo-suite` is documented as nearly
	// information-free, so "4 cases compared" means something very different
	// under it than under a declared corpus.
	fmt.Fprintf(w, "behavior (corpus %s, provider %s, %d cases compared, %d incomparable):\n",
		short(b.Corpus), dash(b.Provider), b.CasesCompared, b.CasesIncomparable)
	if len(b.Excluded) > 0 {
		// A member that compared NOTHING is not part of a comparison, and
		// its exclusion is said out loud: a denominator that shrank must
		// never shrink anonymously.
		ex := make([]string, 0, len(b.Excluded))
		for _, e := range b.Excluded {
			ex = append(ex, short(e))
		}
		fmt.Fprintf(w, "  excluded from the cohort (no comparable case): %s\n", strings.Join(ex, " "))
	}
	if b.CohortN < 2 {
		// A comparison of one is not a comparison, and the rendering must
		// not imply otherwise: no class table, no distinguishing cases,
		// and the reason said out loud.
		fmt.Fprintf(w, "  cohort of %d: a comparison of one is not a comparison, so every diff_* metric but diff_cohort_n is absent\n", b.CohortN)
		return
	}
	fmt.Fprintf(w, "  %-14s %-14s %s\n", "CLASS", "WORLDS", "vs BASE")
	for _, c := range b.Classes {
		members := make([]string, 0, len(c.Members))
		for _, m := range c.Members {
			members = append(members, short(m))
		}
		vsBase := "changed"
		if c.AgreesWithBase {
			vsBase = "same"
		}
		fmt.Fprintf(w, "  %-14s %-14s %s\n", short(c.ID), strings.Join(members, " "), vsBase)
	}
	if len(b.Distinguishing) == 0 {
		return
	}
	fmt.Fprintf(w, "\n  distinguishing cases (%d classes):\n", len(b.Classes))
	for _, d := range b.Distinguishing {
		fmt.Fprintf(w, "    %s  %s\n", d.Case, d.Call)
		fmt.Fprintf(w, "             %-12s → %s\n", "base", d.Base)
		for _, o := range d.Observations {
			fmt.Fprintf(w, "             %-12s → %s\n", short(o.World), o.Value)
		}
	}
	if b.Truncated > 0 {
		fmt.Fprintf(w, "    … and %d more distinguishing case(s), not listed (the report is capped at %d)\n",
			b.Truncated, 32)
	}
}

// short abbreviates a digest for a table cell.
func short(dig string) string {
	if len(dig) <= 12 {
		return dig
	}
	return dig[:12] + "…"
}
