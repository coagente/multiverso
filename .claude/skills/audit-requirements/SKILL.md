---
name: audit-requirements
description: Audit Multiverso's shipped code against the PRD requirement by requirement, producing an honest DONE/PARTIAL/MISSING table with file:line evidence. Use before claiming a milestone is complete, before updating the README Status section, and whenever a design doc's deferrals need reconciling with reality.
---

# Audit the claims

A milestone claim is worth exactly as much as the check behind it. This skill produces the table that goes in the README's Status section — the one a hostile evaluator called "the most honest doc I have read from a vendor." That reputation is an asset and it is one inflated `DONE` away from being spent.

## Method

Fan the requirement set out across parallel auditors with the Workflow tool — one slice each, so no auditor is grading a hundred requirements by the end of its context. Slices that have worked: control + data planes (CP-*, DP-*), execution + agent planes (XP-*, AG-*), evidence + trust + forge (EP-*, TP-*, FI-*).

Give every auditor the same instructions:

1. Read the requirement's **full text** in `PRD.md` §7 — not its ID, not a summary. The gap is usually in a clause, not in the headline.
2. Find the implementation **and its tests**. Cite `file:line` for both.
3. Judge:
   - **DONE** — the behavior the requirement's full text promises is implemented *and* tested. Both.
   - **PARTIAL** — some clause is missing. Name exactly which, in plain language. Say whether a design doc consciously deferred it (cite the doc and line) or whether it is undocumented spec-vs-code drift — the second kind is more important, because nobody knows about it.
   - **MISSING** — not implemented.
4. **Be adversarial about DONE.** An inflated DONE is worse than an honest PARTIAL: the PARTIAL is a roadmap item, the DONE is a lie a user discovers at 3am. If a requirement says "recorded in every receipt" and one code path omits it, that is PARTIAL.
5. "There is a test" is not sufficient — read it. A test that asserts the function returns without error does not test the requirement.

## What to do with the result

- Update the README Status section: complete items in one table, partials in another **with the missing clause stated plainly in the row**. No hedging adverbs.
- Undocumented drift is the highest-value output. Either implement it, or record the deferral in the relevant design doc so the next audit sees a decision rather than a surprise.
- Put the counts in `BUILDLOG.md` honestly. `11 complete, 14 partial, 0 missing` is a better public artifact than `M1 done ✅`, and it is the reason people trust the rest.

## Precedent

The M1 closure audit (2026-08-15) returned 11 complete / 14 partial / 0 missing, and four of those partials were genuine spec-vs-code drift that no design doc had recorded — CP-6's `REPAIR`, three `Receipt` fields EP-2 names, EP-7's `seeds`, and TP-2's identity tier being one hardcoded literal rather than a modeled vocabulary. None of those would have surfaced from reading the design docs; all four came from reading the PRD's own sentences against the code.
