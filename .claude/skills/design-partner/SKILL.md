---
name: design-partner
description: Emulate design partners evaluating Multiverso from published docs alone — each builds their own repo, attempts adoption, and reports friction and an adoption verdict. Use before claiming the product is usable, after any docs or CLI change, and whenever you want to know what a real user hits that we cannot see from inside.
---

# Emulate a design partner

M1 acceptance criterion 3: *"a design partner completes `init → intent → race → explain` on their own repo using only shipped docs, with at most one support interaction."*

The first run of this (2026-08-15) produced three declines and found the architectural hole that reordered the roadmap — the candidate authoring its own oracle evidence. Nothing internal found it; three agents reading only the docs found it in an afternoon. That is what this skill is for.

## The experimental setup, which is the whole value

Run partners in parallel with the Workflow tool, each with a **distinct persona**, and enforce these rules or the result is theater:

1. **Docs only.** A partner may read `README.md`, `docs/quickstart.md`, `docs/concepts.md` — the surface a real user finds — and nothing else. Not the source, not `docs/design/*`, not the PRD, not the research corpus, not the build log. Wanting to read source *is the finding*: "I had to read Go to learn where the key lives."
2. **Their own repo.** Each partner builds a realistic small project with real code, real tests and a real bug. Never the shipped `testdata/toyrepo` fixture. This matters more than it sounds: our fixture already had `.multiverso/` in its `.gitignore`, which made it **structurally immune to the worst bug in the product** — a private-key disclosure that fires on every real repo. A fixture that cannot fail the way customers fail certifies the docs, not the product.
3. **Never invoke a real agent CLI** (`claude`, `codex`) — they cost money. Script adapter with hand-written patches, or the shipped fakes.
4. **Work outside the repo**, in scratch. Never commit.
5. **Count support interactions**: each distinct thing unresolvable from docs plus tool output alone. The bar is one.
6. **Blunt, or worthless.** A partner who writes "great tool!" has failed the assignment. Quote real output; state expected vs happened; name where you would have given up.

## Personas that earned their keep

- **Platform engineer**, accountable for what lands on a protected trunk, already running a merge queue. Asks the questions a security reviewer asks: what exactly is signed, what happens if someone tampers, how does this wire into CI. Found the key-disclosure chain and that attestations do not survive a merge queue.
- **Solo OSS maintainer**, no budget, ten minutes of patience, drowning in AI PRs. Asks whether a rejection is something a contributor can verify without trusting the maintainer. Found that the ladder catches one laundering vector out of five.
- **Skeptical staff engineer** whose explicit brief is to break the value proposition, not to complete a happy path. Found the forgeable-evidence hole. Also reported his going-in thesis wrong, which is the finding that says keep going.

Vary them for what you are testing. A persona who agrees with the pitch tells you nothing.

## Then verify before believing

Never write up partner claims raw. Run a triage pass that **may** read the source and reproduces each claim in a clean room. Partners misread things; a partner's "product bug" is sometimes a doc gap or their own mistake, and saying which claims did not survive verification is part of the report. Then split the output:

- **doc and CLI-ergonomics fixes** — apply now; they are cheap and they compound
- **product bugs** — reproductions into the roadmap, not patched in the same pass
- **strategic findings** — things that are not defects but mean the product or its positioning is wrong. These were the most valuable output last time: *"our fixture is structurally immune to our worst bug"* and *"the pitch and the shipped default disagree"* were both strategic, not bugs.

## Publish it

The study goes in `docs/studies/` **with the declines intact**. Three "no"s published honestly is a stronger artifact than a testimonial, and it is only credible if the fix list ships alongside it.
