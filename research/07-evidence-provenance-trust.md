# 7. Evidence Ledgers, Provenance & Trust

> Part of the Multiverso research corpus - https://github.com/coagente/multiverso - Cutoff: 2026-08-12

## Why this matters for Multiverso

Multiverso's admission loop only works if a Decision (SELECT/COMPOSE/SERIALIZE/REPAIR/REJECT/ADMIT) is made over *evidence*, not over what an agent *says* about a world. That requires the evidence plane and trust plane to deliver three distinct properties that are routinely conflated:

- **Integrity** — the evidence record was not altered after production (hashes, signatures, transparency logs).
- **Identity** — we know *which principal* (human, CI runner, agent, model) produced the record (certificates, OIDC, TEE quotes).
- **Veracity** — the record's *claim about the world* is actually true (the tests really ran, against exactly this tree, and they really measure what we think they measure).

Every system surveyed below delivers the first two to some degree; **none delivers veracity by cryptography alone**. Sigstore's own verification model is explicit that a valid signature proves an artifact "was signed while its certificate was valid" by a bound identity — nothing about the artifact's quality ([Sigstore security model](https://docs.sigstore.dev/about/security/)). GitHub is equally blunt: "Artifact attestations are *not* a guarantee that an artifact is secure" ([GitHub Docs: Artifact attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations)). Multiverso's contribution is to *narrow* the veracity gap operationally — fresh, state-bound, independently produced evidence, adversarial challenge under a scheduler — while being honest in the attestation about what remains assumed. This chapter maps the building blocks and specifies what a minimal Multiverso attestation predicate should contain.

## State of the art

### Proof-or-Stop: evidence-gated lifecycle control (the closest prior art)

The most directly relevant work is **Proof-or-Stop: Don't Trust the Agent, Trust the Evidence** (Huang, Hsia, Sun, Shi, Huang, White; submitted 2026-07-16), which formalizes exactly the claim/evidence separation Multiverso needs ([arXiv:2607.14890](https://arxiv.org/abs/2607.14890)). Its model, from the [full text](https://arxiv.org/html/2607.14890v1):

- **Agent outputs are claims, not evidence.** "A natural-language report from an agent is not an E_c: it provides neither verified integrity nor attested execution and carries no binding." Evidence is "a structured record (not prose) describing the outcome of a checkable action: a test run, a build, a scope check, a reviewer verdict."
- **Binding to exact source state.** Every piece of evidence carries a binding β(E) = ⟨materialHash, headHash, storyFilesHash, policyHash, commandSetHash⟩, where `materialHash` is the SHA-256 of the canonical tracked source tree (via `git ls-tree`, excluding lifecycle metadata), `headHash` is the commit identity, and policy/command-set digests pin the checking configuration itself.
- **Receipt identity.** Each execution receipt is ρ(E) = ⟨cmd, args, cwd, exit, outputDigest⟩; signed receipts additionally carry producer identity (actor, lane, host/session, signing-key identity).
- **Freshness is digest equality against the live state.** Evidence "produced against an earlier source state is rejected the instant the source tree changes, because materialHash_E ≠ materialHash(H)." A lifecycle transition ϕᵢ→ϕᵢ₊₁ is permitted only if every required criterion has admissible (integrity-verified, identity-bound, fresh, complete) evidence; otherwise the system repairs within a bounded loop, degrades honestly, escalates, or stops.
- **Gates per transition**: plan→dev checks scope-contract existence; dev→review checks the diff stays within declared scope against current materialHash; review→test checks current-round reviewer identity and freshness; test→done requires a full-test receipt with all hashes matching. High-risk changes additionally require a multi-lane assurance floor of at least 3 rounds with ≥2 independent verdicts each ((R,K)=(3,2)).
- **Results**: in a 9,240-cell ablation, gating reduced visible-pass/hidden-fail amplification from 31/1,800 injected cells (naive loop) to 2/1,800; an offline signed receipt-bundle verifier rejected 18 tampering classes (stale hashes, forged/empty signatures, key mismatch, digest mismatch, non-passing receipts, missing build proof, malformed bundles) with 0 false accepts and 0 false rejects.
- **Honest limits**: local-key receipts "provide authenticated integrity, producer identity, and freshness under the stated single-host trust assumption; they do not independently prove execution truth against a compromised runner or semantic correctness of the claim."

This is the sharpest published statement of the integrity/identity/veracity split for agent workflows. Its scope, however, is a *single linear story lifecycle on a single host* — no multi-world speculation, no evidence-aware budget allocation, no composition decisions.

### in-toto: the attestation vocabulary

The **in-toto Attestation Framework** is the de facto interchange format for signed supply-chain metadata. It defines four layers: a type-specific **Predicate**, a **Statement** binding the predicate to subject artifacts, a **DSSE Envelope** for authentication/serialization, and a **Bundle** for grouping ([in-toto attestation spec](https://github.com/in-toto/attestation/blob/main/spec/README.md)). A [Statement](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md) has exactly three fields — `subject` (artifacts identified by ResourceDescriptors, i.e., digest sets; cryptographic digests "strongly recommended"), `predicateType` (a versioned URI), and `predicate` (arbitrary typed payload). Classic in-toto additionally defines a signed **Layout**: the supply-chain owner declares steps, authorized actors per step, and artifact-flow rules; verification replays recorded attestations against the layout ([in-toto and SLSA](https://slsa.dev/blog/2023/05/in-toto-and-slsa)).

Predicates directly relevant to Multiverso:

- **test-result v0.1** (`https://in-toto.io/attestation/test-result/v0.1`): `result` ∈ {PASSED, WARNED, FAILED}, `configuration` (ResourceDescriptors of the test config), `url` (CI log), and `passedTests`/`warnedTests`/`failedTests` lists; the subject is the tested source (typically a git commit digest) ([test-result predicate](https://github.com/in-toto/attestation/blob/main/spec/predicates/test-result.md)). This is a natural base for Multiverso oracle receipts, though it lacks environment digests and freshness semantics.
- **SCAI v0.3** (`https://in-toto.io/attestation/scai/v0.3`): attribute assertions about artifact *behavior* — each with `attribute`, optional `target`, `conditions`, and an `evidence` ResourceDescriptor; when evidence is omitted "a consumer MAY choose to evaluate the attestation on the basis of the producer's identity" ([SCAI predicate](https://github.com/in-toto/attestation/blob/main/spec/predicates/scai.md)). SCAI is the right shape for "this world exhibits property P, per evidence E."
- **Custom predicates** are explicitly supported. The [new-predicate guidelines](https://github.com/in-toto/attestation/blob/main/docs/new_predicate_guidelines.md) require: articulate the use case and why existing predicates don't fit, provide concrete examples, use lowerCamelCase fields, RFC 3339 timestamps with `Z` and descriptive names (`builtAt`, not `timestamp`), follow the monotonic principle (a statement remains true as fields are added), optionally restrict valid subject types, and submit protobuf bindings if contributing upstream. "Your predicate is yours" — Multiverso can mint `https://multiverso.dev/attestation/...` types without upstream approval.

**in-toto witness** (with **Archivista** as attestation store) shows what a shipped evidence plane looks like: a pluggable framework that attests command executions and pipeline steps across GitLab/GitHub/AWS/GCP, signs via keys, Sigstore keyless, or SPIFFE/SPIRE, supports RFC 3161 timestamp authorities, and verifies with an embedded OPA Rego policy engine ([in-toto/witness](https://github.com/in-toto/witness)).

### Sigstore: integrity + identity, explicitly not veracity

**Sigstore** supplies the trust plane's signing machinery: **Fulcio** issues short-lived certificates (~10 minutes) binding an ephemeral key to an OIDC identity (email, service account, or CI workflow identity); **Rekor** is an append-only Merkle-tree transparency log of signing events; **cosign** generates ephemeral keys and discards them after signing ([Sigstore overview](https://docs.sigstore.dev/about/overview/)). Verification confirms: signature validity, identity binding, certificate chain to Sigstore's TUF-managed root (five keyholders from different organizations, public root ceremony), and Rekor inclusion proof ([Sigstore security model](https://docs.sigstore.dev/about/security/)).

What it does **not** give you:

- **No veracity.** Verification proves the artifact "comes from its expected source and has not been tampered with after its creation" — nothing about whether the content's claims are true ([Sigstore overview](https://docs.sigstore.dev/about/overview/)).
- **Identity is only as strong as the OIDC provider.** A compromised OIDC account or provider yields valid-looking certificates; the stated mitigation is detection via log monitoring, and "if no third parties monitor the logs, then any misbehavior by Rekor and Fulcio might go undetected" ([Sigstore security model](https://docs.sigstore.dev/about/security/)).
- **Revocation is replaced by expiry + timestamps.** Fulcio deliberately avoids CRLs: certificates are valid ~10 minutes, and verifiers accept signatures proven (via Rekor/RFC 3161 timestamps) to have been made while the certificate was valid; the TUF root itself natively supports key revocation ([Why you can't use Sigstore without Sigstore](https://blog.sigstore.dev/why-you-cant-use-sigstore-without-sigstore-de1ed745f6fc/)). Consequence for Multiverso: you cannot "un-sign" an attestation — revocation must live in the **policy layer**, not the log.

### SLSA v1.x: build levels and residual builder trust

The SLSA Build track defines levels L0–L3: L0 no provenance; L1 provenance exists (possibly unsigned); L2 a hosted build platform generates and *signs* provenance; L3 adds hardened isolation — builds cannot influence one another and signing material is inaccessible to user-defined build steps, making provenance "unforgeable" by the platform's *users* ([SLSA security levels](https://slsa.dev/spec/v1.0/levels), [SLSA build requirements](https://slsa.dev/spec/v1.2/build-requirements)). The [SLSA v1.1 provenance predicate](https://slsa.dev/spec/v1.1/provenance) (`https://slsa.dev/provenance/v1`) structures this as `buildDefinition` (buildType, externalParameters — which must be complete at L3, internalParameters, resolvedDependencies with actual digests) and `runDetails` (`builder.id` — "the transitive closure of the trusted build platform", metadata, byproducts).

**GitHub Artifact Attestations** ships this at scale: default attestations give SLSA v1.0 **Build L2** — "a link between your artifact and its build instructions" — containing workflow, repo/org, environment, commit SHA, trigger event, and OIDC token data; signing runs through Sigstore (public repos use the Public Good instance with public transparency logs; **private repos use GitHub's dedicated Sigstore instance without transparency logging** — a deliberate privacy design). Build L3 is reached by moving the build into a shared reusable workflow, separating build definition from the calling repository ([GitHub Docs: Artifact attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations), [GitHub Docs: SLSA L3 via reusable workflows](https://docs.github.com/actions/security-guides/using-artifact-attestations-and-reusable-workflows-to-achieve-slsa-v1-build-level-3), [GitHub blog](https://github.blog/enterprise-software/devsecops/enhance-build-security-and-reach-slsa-level-3-with-github-artifact-attestations/)).

The critical residue: at *every* level, "the builder represents all entities necessarily trusted to faithfully execute the build and record provenance accurately" ([SLSA v1.1 provenance](https://slsa.dev/spec/v1.1/provenance)). SLSA L3 protects against tenant tampering, not against the platform itself. Research is attacking this residue with TEEs — e.g., **Kettle: attested builds for verifiable software provenance** ([arXiv:2605.08363](https://arxiv.org/pdf/2605.08363), 2026) — but shipped systems all assume builder honesty.

SLSA also defines the **Verification Summary Attestation (VSA)** (`https://slsa.dev/verification_summary/v1`): a trusted verifier asserts `verificationResult` PASSED/FAILED against a `policy` (URI + digest), with `verifiedLevels`, optional `timeVerified`, and `inputAttestations` listing the evidence consumed — enabling consumers to rely on the verifier's judgment "without needing to have access to all of the attestations" ([SLSA VSA](https://slsa.dev/spec/v1.1/verification_summary)). The VSA is the closest existing analogue to a Multiverso **Decision record**.

### Agent and model identity: today it is a client-reported string

The uncomfortable baseline: in every mainstream LLM API, the `model` field in a response is a **provider-asserted string**, and the agent identity in orchestrator logs is **self-reported**. Cai, Shi, Zhao, and Song's audit study ("Are You Getting What You Pay For?", 2025-04-07) shows software-only verification fails: output-based statistical tests are query-intensive and unreliable against subtle swaps (quantization, fine-tuning, hidden system prompts), and log-probability methods are "defeated by inherent inference nondeterminism in production"; they conclude TEEs "provide provable cryptographic guarantees of model integrity with only a modest performance overhead" ([arXiv:2504.04715](https://arxiv.org/abs/2504.04715)). We found no evidence that any major first-party API provider (OpenAI, Anthropic, Google) ships signed per-request inference receipts as of the cutoff (unverified beyond absence of evidence).

What *does* exist, in increasing order of assurance:

- **Weights hashes for local models.** **OpenSSF Model Signing (OMS) v1.0** (launched 2025-04-04 by the OpenSSF AI/ML WG with Google, NVIDIA, HiddenLayer) produces a detached Sigstore-format signature bundle over a manifest of per-file SHA-256 hashes of model artifacts, PKI-agnostic (bare keys, cert chains, or Sigstore keyless); adopted by NVIDIA NGC and Kaggle ([OpenSSF launch post](https://openssf.org/blog/2025/04/04/launch-of-model-signing-v1-0-openssf-ai-ml-working-group-secures-the-machine-learning-supply-chain/), [OMS spec](https://github.com/ossf/model-signing-spec), [Sigstore blog](https://blog.sigstore.dev/model-transparency-v1.0/)). This proves *which weights you loaded*, not what the inference did. **AttestLLM** extends this to on-device attestation of billion-scale LLMs against replacement/forgery ([arXiv:2509.06326](https://arxiv.org/abs/2509.06326), 2025-09).
- **TEE-attested inference (shipped).** Azure confidential VMs with NVIDIA H100 GPUs (NCC H100 v5) reached **general availability in September 2024**, combining AMD SEV-SNP CPU TEEs with H100 confidential-computing mode; "attestation enables a relying party to cryptographically verify the security claims of both the CPU and GPU TEEs" ([Microsoft announcement](https://techcommunity.microsoft.com/blog/azureconfidentialcomputingblog/general-availability-azure-confidential-vms-with-nvidia-h100-tensor-core-gpus/4242644), [NVIDIA blog](https://blogs.nvidia.com/blog/azure-confidential-vm-h100-general-availability)). Google Cloud's equivalent on A3/H100 entered preview in late 2024 ([Google Cloud blog](https://cloud.google.com/blog/products/identity-security/how-confidential-accelerators-can-boost-ai-workload-security)). The GPU produces an NVIDIA-signed report over firmware measurements verifiable via the NVIDIA attestation service or Intel Trust Authority's composite CPU+GPU attestation ([Intel Trust Authority docs](https://docs.trustauthority.intel.com/main/articles/articles/ita/concept-gpu-attestation.html)); reported throughput overhead for CC mode on H100 is in the 2–5% range for LLM inference, with caveats that HBM contents and multi-GPU NVLink traffic are not hardware-protected ([Spheron 2026 guide](https://www.spheron.network/blog/confidential-gpu-computing-nvidia-tee-encrypted-vram/)). **Phala on OpenRouter** (February 2025) is the first marketplace deployment: DeepSeek R1 70B served from H100 CC mode + Intel TDX, returning a dual attestation bundle (TDX quote + NVIDIA GPU attestation) containing the enclave's public key, with responses **signed by the enclave key** so a client can verify that a specific attested runtime produced a specific output ([Phala announcement](https://phala.com/posts/GPU-TEEs-is-Alive-on-OpenRouter)).
- **Inference receipts and gateway provenance (papers, 2026).** **AEX** proposes a non-intrusive signed `attestation` object added to existing JSON LLM APIs, binding a client-visible request to the complete or streamed response across multi-hop gateways ([arXiv:2603.14283](https://arxiv.org/html/2603.14283), 2026-03). **Evidence-Bound Gateway-Path Provenance** (Wang & Tian, 2026-06) runs the gateway itself in a TEE ("Attested Gateway Runtime") that signs an "Inference Evidence Chain" binding runtime attestation, routing policy, fallback transcript, endpoint observation, and an encrypted stream commitment — while explicitly leaving the upstream model provider *outside* the TCB: it proves the gateway's honesty about the path, not model-execution correctness ([arXiv:2606.22560](https://arxiv.org/html/2606.22560)). **TOPLOC** offers a probabilistic alternative: locality-sensitive hashes over activations for trustless verifiable inference ([arXiv:2501.16007](https://arxiv.org/pdf/2501.16007), 2025-01).
- **Receiver-attested receipts.** **Notarized Agents / Sello** (Figuera, 2026-06-02) inverts logging: the *service receiving* an agent action signs the receipt, encrypts it (HPKE) to the agent owner's key, and publishes it to a witness-cosigned transparency log — tamper-evident audit "without trusting the agent or its operator," with receipt contents confidential ([arXiv:2606.04193](https://arxiv.org/abs/2606.04193)). For Multiverso, this pattern maps to *oracles signing their own verdicts* rather than the orchestrator recording them.

### Privacy: what must never enter a public transparency log

Rekor entries are public, append-only, and unremovable — "entries are never mutated or removed"; deletion attempts change the Merkle root ([Chainguard: Introduction to Rekor](https://edu.chainguard.dev/open-source/sigstore/rekor/an-introduction-to-rekor/)). This collides with both GDPR erasure rights and, more acutely for Multiverso, with the reality that **prompts, retrieved context, repo contents, and secrets leak business logic**. Established mitigations: (1) GitHub's split — public repos log to the public Rekor, private repos use a *private* Sigstore instance with no transparency log ([GitHub Docs](https://docs.github.com/en/actions/concepts/security/artifact-attestations)); (2) the **digest-only pattern** — the public/shared log carries only Statements whose subjects and evidence references are cryptographic digests, while payloads (prompts, transcripts, test logs) live in a private CAS/attestation store like Archivista ([in-toto/witness](https://github.com/in-toto/witness)); (3) Sello's encrypted-receipt-in-public-log construction ([arXiv:2606.04193](https://arxiv.org/abs/2606.04193)). The rule for Multiverso: **nothing enters an append-only log that is not either a digest or already public.**

### Freshness and revocation

Two complementary mechanisms exist:

1. **State-equality freshness (Proof-or-Stop):** evidence carries the digests of the exact tree/policy/command-set it was produced against; the gate recomputes the live digests and rejects on any mismatch — freshness is not a TTL but an equality check ([arXiv:2607.14890](https://arxiv.org/html/2607.14890v1)). In in-toto terms: put the tree digest in `subject`, put environment/policy digests in the predicate, and make the verifier recompute both at decision time.
2. **Time-validity + root revocation (Sigstore/TUF):** short-lived certificates plus verifiable timestamps prove "signed while valid"; the TUF root distribution channel natively supports revoking compromised CAs/log keys ([Sigstore blog](https://blog.sigstore.dev/why-you-cant-use-sigstore-without-sigstore-de1ed745f6fc/), [Sigstore security model](https://docs.sigstore.dev/about/security/)).

What no shipped system provides: **semantic revocation** — "everything admitted on evidence produced by model M / agent A / dependency D between t₁ and t₂ is now suspect." Since log entries cannot be retracted, this must be a policy-layer construct: a signed, versioned deny-list (by model digest, agent identity, policy hash, or dependency digest) consulted at verification time, with VSAs re-issued (`verificationResult: FAILED`) for affected artifacts — the VSA's `inputAttestations` field is what makes recomputing the blast radius tractable ([SLSA VSA](https://slsa.dev/spec/v1.1/verification_summary)).

## Comparison table

| System | Kind / date | Integrity | Identity | Veracity | State-bound freshness | Agent/model identity | Privacy posture |
|---|---|---|---|---|---|---|---|
| [Proof-or-Stop](https://arxiv.org/abs/2607.14890) | Paper + impl, 2026-07 | Signed receipts, 18 tamper classes rejected | Producer + signing key (local-key) | No (explicit: no execution truth vs compromised runner) | **Yes** — digest equality vs live tree/policy/commands | Actor/lane labels; model self-reported | Local, single host |
| [in-toto attestation](https://github.com/in-toto/attestation/blob/main/spec/README.md) | Spec, active | DSSE envelope | Signer of envelope | No (format only) | Only if predicate encodes it | No standard predicate for agents/models | Digests in subject; payload up to author |
| [witness/Archivista](https://github.com/in-toto/witness) | Shipped OSS | Signed step attestations | Keys/Sigstore/SPIFFE | No | Partial (materials/products digests) | No | Private attestation store |
| [Sigstore](https://docs.sigstore.dev/about/overview/) | Shipped infra | Signatures + Rekor inclusion | OIDC-bound short-lived certs | **No** (by design) | Timestamps only | No | Public log; entries unremovable |
| [SLSA v1.1 + VSA](https://slsa.dev/spec/v1.1/provenance) | Spec, v1.x | Signed provenance (L2+) | builder.id | No — builder trusted at all levels | Commit digest in provenance | No | N/A |
| [GitHub Artifact Attestations](https://docs.github.com/en/actions/concepts/security/artifact-attestations) | Shipped, GA | Sigstore-signed | Workflow OIDC identity | No ("not a guarantee that an artifact is secure") | Commit SHA binding | No | Private Sigstore instance for private repos |
| [OMS model signing v1.0](https://openssf.org/blog/2025/04/04/launch-of-model-signing-v1-0-openssf-ai-ml-working-group-secures-the-machine-learning-supply-chain/) | Shipped, 2025-04 | File-hash manifest signed | Signer identity | No (weights ≠ behavior) | N/A | **Weights digest** (local models) | Detached bundle |
| [Phala/OpenRouter GPU TEE](https://phala.com/posts/GPU-TEEs-is-Alive-on-OpenRouter) | Shipped, 2025-02 | Enclave-signed responses | TDX + NVIDIA GPU attestation | Partial — attested runtime ran attested code | Per-deployment measurement | **Yes** — hardware-attested model serving | Encrypted in TEE |
| [Gateway-path provenance](https://arxiv.org/html/2606.22560) | Paper, 2026-06 | Signed Inference Evidence Chain | TEE-measured gateway | Path honesty only; provider outside TCB | Nonce-fresh attestation | Route/endpoint observation | Encrypted streams |
| [Sello / Notarized Agents](https://arxiv.org/abs/2606.04193) | Paper, 2026-06 | Receiver-signed receipts, witnessed log | Receiver keys | Receiver's observation only | Log timestamps | Agent actions, not model identity | **HPKE-encrypted receipts in public log** |

## Implications for Multiverso design

**1. Adopt in-toto Statement/DSSE as the wire format; mint two custom predicates.** Don't invent an envelope. Define `https://multiverso.dev/attestation/world-evidence/v0.1` (per oracle run, SCAI/test-result-shaped) and `https://multiverso.dev/attestation/admission-decision/v0.1` (VSA-shaped), following the [in-toto predicate guidelines](https://github.com/in-toto/attestation/blob/main/docs/new_predicate_guidelines.md).

**2. Minimal viable world-evidence predicate.** Subject = the world's tree state. Predicate:

```json
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [{ "name": "world/w-17", "digest": { "gitTree": "…", "gitCommit": "…" } }],
  "predicateType": "https://multiverso.dev/attestation/world-evidence/v0.1",
  "predicate": {
    "intent":    { "uri": "…", "digest": { "sha256": "…" } },
    "baseState": { "gitCommit": "…", "gitTree": "…" },
    "binding": {
      "envDigest":     { "sha256": "…" },   // container/microVM image + toolchain lockfiles
      "policyDigest":  { "sha256": "…" },   // admission policy in force
      "oracleDigest":  { "sha256": "…" }    // oracle command set / harness version
    },
    "producer": {
      "role": "generator|tester|challenger",
      "orchestratorIdentity": "…",           // Fulcio/OIDC identity of the runner
      "model": {
        "claimedId": "…",                     // ALWAYS labeled as claimed
        "weightsDigest": null,                // OMS digest when local
        "inferenceReceipt": null,             // TEE quote / signed receipt when available
        "assuranceTier": "claimed|receipted|attested"
      },
      "promptDigest": { "sha256": "…" },      // digest-only; payload in private CAS
      "contextDigest": { "sha256": "…" }
    },
    "receipts": [{
      "cmd": ["…"], "cwd": "…", "exit": 0,
      "outputDigest": { "sha256": "…" },
      "startedAt": "2026-08-12T00:00:00Z", "finishedAt": "…",
      "result": "PASSED"
    }],
    "budget": { "tokensUsed": 0, "cpuSeconds": 0, "wallSeconds": 0 }
  }
}
```

This merges Proof-or-Stop's β(E)/ρ(E) bindings ([arXiv:2607.14890](https://arxiv.org/html/2607.14890v1)) with in-toto [test-result](https://github.com/in-toto/attestation/blob/main/spec/predicates/test-result.md) semantics, and adds what neither has: model-identity tiering and budget accounting (which the scheduler needs).

**3. Freshness is equality, not TTL.** The admission gate MUST recompute `gitTree`, `envDigest`, `policyDigest`, `oracleDigest` at decision time and reject on any mismatch — Proof-or-Stop demonstrates this closes the stale-evidence hole with zero false accepts in its tamper suite ([arXiv:2607.14890](https://arxiv.org/html/2607.14890v1)). Wall-clock validity is only a secondary bound.

**4. Tier model identity honestly; never launder a string into a proof.** `assuranceTier` must be first-class: `claimed` (API `model` field — the norm today, unverifiable per [arXiv:2504.04715](https://arxiv.org/abs/2504.04715)), `receipted` (provider/gateway-signed receipt, AEX-style), `attested` (TEE quote binding weights + runtime, Phala/Azure-style). Admission policy can then say "REPAIR decisions may use claimed-tier generators, but ADMIT requires attested-tier or independent-oracle evidence."

**5. Oracles sign their own verdicts.** Follow Sello's receiver-attestation insight ([arXiv:2606.04193](https://arxiv.org/abs/2606.04193)) and in-toto witness practice: the test runner / challenger signs its receipt with its *own* identity (Fulcio keyless in CI; local keys otherwise), so the orchestrator cannot fabricate oracle output. Generator and verifier identities must be distinct principals — this is Multiverso's analogue of SLSA L3's "signing material inaccessible to build steps" ([SLSA levels](https://slsa.dev/spec/v1.0/levels)).

**6. Digest-only in any shared log; payloads in private CAS.** Prompts, contexts, diffs, and test logs never enter a transparency log — only their digests, following GitHub's private-Sigstore precedent ([GitHub Docs](https://docs.github.com/en/actions/concepts/security/artifact-attestations)) and Rekor's irreversibility ([Chainguard](https://edu.chainguard.dev/open-source/sigstore/rekor/an-introduction-to-rekor/)). A per-project private Merkle log (or Archivista instance) gives tamper-evidence without disclosure.

**7. Decision records as VSAs; revocation as re-verification.** Emit an admission-decision attestation listing `inputAttestations` (all world-evidence consumed), the policy digest, and the decision type. Revocation-by-model/agent/policy/dependency is a signed deny-list consulted at verify time plus batch re-issuance of FAILED VSAs over the affected closure — the only pattern compatible with append-only logs ([SLSA VSA](https://slsa.dev/spec/v1.1/verification_summary), [Sigstore blog](https://blog.sigstore.dev/why-you-cant-use-sigstore-without-sigstore-de1ed745f6fc/)).

**8. What remains unavoidably assumed** (state it in the attestation's trust-model field): (a) runner/execution integrity unless the oracle runs in a TEE — Proof-or-Stop's "compromised runner" caveat; (b) oracle validity — tests measure what we believe they measure (the veracity gap; adversarial challenge reduces but cannot eliminate it); (c) OIDC provider and Sigstore/TUF root honesty, detectable-not-preventable via log monitoring ([Sigstore security model](https://docs.sigstore.dev/about/security/)); (d) for `claimed`-tier models, the entire provider stack; (e) semantic correctness of admitted code — never proven, only bounded by evidence.

## Open questions

1. **Can freshness-as-equality survive composition?** COMPOSE merges worlds into a new tree whose digest no evidence was produced against. Does Multiverso re-run oracles post-merge (Proof-or-Stop's answer, at budget cost), or can rerun-selection be evidence-aware (only oracles whose input closure intersects the merged diff)?
2. **What is the minimal *independent* identity for an agent?** Orchestrator OIDC identity + claimed model string is one principal wearing two hats. Do we need per-agent workload identities (SPIFFE-style, as witness supports) so that generator/tester/challenger separation is cryptographic rather than conventional?
3. **Will first-party inference receipts arrive?** TEE marketplaces (Phala/OpenRouter) shipped in 2025; gateway-attestation designs (AEX, AGR/IEC) are 2026 papers. If frontier providers never sign responses, is claimed-tier + cross-provider disagreement testing (N models as mutual challengers) an acceptable substitute?
4. **Deny-list governance.** Who signs Multiverso revocation statements, how are they distributed (TUF-style?), and how do we bound re-verification cost when a popular model version is revoked?
5. **Private transparency economics.** Is a witness-cosigned private log (Sello-style) worth running per organization, or is a hash-chained table in the trunk repo (evidence refs as git notes/commits) sufficient tamper-evidence for the single-org threat model?
6. **Standardization path.** Should the world-evidence predicate be contributed to in-toto upstream (gaining ecosystem verifiers) at the cost of freezing schema early, given no agent-change predicate exists there today?

## Sources

- Proof-or-Stop: Don't Trust the Agent, Trust the Evidence - https://arxiv.org/abs/2607.14890 - 2026-07-16
- Proof-or-Stop (full text, HTML) - https://arxiv.org/html/2607.14890v1 - 2026-07-16
- in-toto Attestation Framework spec - https://github.com/in-toto/attestation/blob/main/spec/README.md - accessed 2026-08-12
- in-toto Statement v1 - https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md - accessed 2026-08-12
- in-toto new predicate guidelines - https://github.com/in-toto/attestation/blob/main/docs/new_predicate_guidelines.md - accessed 2026-08-12
- in-toto test-result predicate v0.1 - https://github.com/in-toto/attestation/blob/main/spec/predicates/test-result.md - accessed 2026-08-12
- in-toto SCAI predicate v0.3 - https://github.com/in-toto/attestation/blob/main/spec/predicates/scai.md - accessed 2026-08-12
- in-toto witness - https://github.com/in-toto/witness - accessed 2026-08-12
- in-toto and SLSA - https://slsa.dev/blog/2023/05/in-toto-and-slsa - 2023-05
- Sigstore overview - https://docs.sigstore.dev/about/overview/ - accessed 2026-08-12
- Sigstore security model - https://docs.sigstore.dev/about/security/ - accessed 2026-08-12
- Why you can't use Sigstore without Sigstore - https://blog.sigstore.dev/why-you-cant-use-sigstore-without-sigstore-de1ed745f6fc/ - accessed 2026-08-12
- An Introduction to Rekor (Chainguard Academy) - https://edu.chainguard.dev/open-source/sigstore/rekor/an-introduction-to-rekor/ - accessed 2026-08-12
- SLSA security levels (v1.0) - https://slsa.dev/spec/v1.0/levels - accessed 2026-08-12
- SLSA build requirements (v1.2) - https://slsa.dev/spec/v1.2/build-requirements - accessed 2026-08-12
- SLSA provenance predicate (v1.1) - https://slsa.dev/spec/v1.1/provenance - accessed 2026-08-12
- SLSA Verification Summary Attestation (v1.1) - https://slsa.dev/spec/v1.1/verification_summary - accessed 2026-08-12
- GitHub Docs: Artifact attestations - https://docs.github.com/en/actions/concepts/security/artifact-attestations - accessed 2026-08-12
- GitHub Docs: SLSA v1 Build L3 via reusable workflows - https://docs.github.com/actions/security-guides/using-artifact-attestations-and-reusable-workflows-to-achieve-slsa-v1-build-level-3 - accessed 2026-08-12
- GitHub blog: Reach SLSA Level 3 with Artifact Attestations - https://github.blog/enterprise-software/devsecops/enhance-build-security-and-reach-slsa-level-3-with-github-artifact-attestations/ - accessed 2026-08-12
- Launch of Model Signing v1.0 (OpenSSF) - https://openssf.org/blog/2025/04/04/launch-of-model-signing-v1-0-openssf-ai-ml-working-group-secures-the-machine-learning-supply-chain/ - 2025-04-04
- OpenSSF Model Signing (OMS) Specification - https://github.com/ossf/model-signing-spec - accessed 2026-08-12
- Taming the Wild West of ML: Practical Model Signing with Sigstore - https://blog.sigstore.dev/model-transparency-v1.0/ - 2025-04
- Are You Getting What You Pay For? Auditing Model Substitution in LLM APIs - https://arxiv.org/abs/2504.04715 - 2025-04-07 (rev. 2025-09-29)
- Phala: GPU TEEs live on OpenRouter - https://phala.com/posts/GPU-TEEs-is-Alive-on-OpenRouter - 2025-02
- GA: Azure confidential VMs with NVIDIA H100 GPUs - https://techcommunity.microsoft.com/blog/azureconfidentialcomputingblog/general-availability-azure-confidential-vms-with-nvidia-h100-tensor-core-gpus/4242644 - 2024-09
- NVIDIA blog: H100 in Azure confidential VMs GA - https://blogs.nvidia.com/blog/azure-confidential-vm-h100-general-availability - 2024-09
- Google Cloud: confidential accelerators for AI workloads - https://cloud.google.com/blog/products/identity-security/how-confidential-accelerators-can-boost-ai-workload-security - 2024
- Intel Trust Authority: GPU remote attestation - https://docs.trustauthority.intel.com/main/articles/articles/ita/concept-gpu-attestation.html - accessed 2026-08-12
- Confidential GPU Computing on Cloud (Spheron, 2026 guide) - https://www.spheron.network/blog/confidential-gpu-computing-nvidia-tee-encrypted-vram/ - 2026
- AEX: Non-Intrusive Multi-Hop Attestation and Provenance for LLM APIs - https://arxiv.org/html/2603.14283 - 2026-03
- Evidence-Bound Gateway-Path Provenance for Third-Party LLM Inference - https://arxiv.org/html/2606.22560 - 2026-06
- Notarized Agents: Receiver-Attested Confidential Receipts for AI Agent Actions - https://arxiv.org/abs/2606.04193 - 2026-06-02
- AttestLLM: Efficient Attestation Framework for Billion-scale On-device LLMs - https://arxiv.org/abs/2509.06326 - 2025-09
- TOPLOC: A Locality Sensitive Hashing Scheme for Trustless Verifiable Inference - https://arxiv.org/pdf/2501.16007 - 2025-01
- Kettle: Attested builds for verifiable software provenance - https://arxiv.org/pdf/2605.08363 - 2026-05
