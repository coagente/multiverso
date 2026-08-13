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

- [`RESEARCH.md`](RESEARCH.md) — the founding scope memo (Spanish): verdict, scope, and phased plan
- [`research/`](research/) — exhaustive state-of-the-art research (in progress)

## License

TBD

---

Built in public by [coagente](https://github.com/coagente).
