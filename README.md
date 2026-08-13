# Multiverso

> **Git versions accepted history. Multiverso versions the possibilities not yet accepted — and the evidence used to decide which one becomes history.**

**Multiverso** is an evidence-native, Git-compatible control plane for speculative software change produced by AI agents.

Given a versioned *intent*, Multiverso creates isolated candidate *worlds*, records their lineage, dynamically allocates budget between generation and verification, and accumulates *evidence* cryptographically bound to the exact state of each candidate. Its decision engine selects, rejects, or escalates candidates under explicit policies — and only admits to trunk an integrated state whose evidence remains fresh.

Multiverso does not replace Git. It versions possibilities, evidence, and decisions *before* they become accepted history.

## Core research question

> Under a fixed total budget, can an evidence-aware scheduler dynamically allocate compute between generation, testing, and adversarial challenge to admit changes with higher true correctness and a lower false-admission rate than a single agent, a fixed best-of-N, or a test-only selector?

## Primitives

```text
Intent → N Worlds → Evidence → Decision → Admission
```

| Primitive | What it is |
|---|---|
| `Intent` | Versioned specification bound to a base state: pre/postconditions, constraints, budget, admission policy |
| `World` | An isolated candidate: code tree + environment + agent identity + patch + execution trace + lineage |
| `Evidence` | Oracle output cryptographically bound to an exact world digest, with freshness, cost, and trust level |
| `Decision` | `SELECT` \| `COMPOSE` \| `SERIALIZE` \| `REPAIR` \| `REJECT` \| `ADMIT` |
| `Attestation` | Signed record linking intent, selected world, evidence, and policy version |

## Status

🌱 **Building in public.** This project is at the research stage.

- [`PRD.md`](PRD.md) — **the product requirements document**: data model, requirements by plane, architecture, MVP scope, evaluation plan, roadmap M0–M4
- [`RESEARCH.md`](RESEARCH.md) — the founding scope memo (Spanish): verdict, scope, and phased plan
- [`research/`](research/) — **the research corpus**: 20 chapters, ~76,000 words, 305 primary sources, cutoff 2026-08-12
  - [Executive summary & findings](research/README.md) — the five-capability novelty map, the white space (evidence-aware scheduling), and what it changes for the roadmap
  - [Verification](research/VERIFICATION.md) — adversarial fact-check of the memo's 13 load-bearing claims (13/13 confirmed)
  - [Bibliography](research/BIBLIOGRAPHY.md) — deduplicated source index

**Headline finding:** every one of Multiverso's five capabilities exists somewhere individually; **no system on Earth ships more than two**, and nobody at all ships an adaptive verification budget. The conjunction is empty — and closing fast (estimated window: 12–24 months).

## License

TBD

---

Built in public by [coagente](https://github.com/coagente).
