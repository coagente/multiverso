# Probe: GitHub custom-ref durability & attestations API

> M0 week-1 checklist item ([PRD §12](../../PRD.md)), resolving open risks from [research ch. 18](../../research/18-forge-integration.md). Executed 2026-08-13 against a throwaway public repo ([coagente/multiverso-ref-probe](https://github.com/coagente/multiverso-ref-probe)) with `git version` (system) and `gh` over HTTPS. Numbers are single-run, residential connection — treat as order-of-magnitude.

## Questions & answers

**Q1. Does GitHub accept and serve 1k+ refs under `refs/multiverso/*`?** → **Yes.**
- Created 1,200 refs (`refs/multiverso/intent/i<1..200>/cand/<1..6>`, all pointing at one commit) locally via `git update-ref --stdin`; single `git push origin 'refs/multiverso/*:refs/multiverso/*'` succeeded in **7.2s**.
- `git ls-remote origin 'refs/multiverso/*'` returns all 1,200, in **<1s**.

**Q2. Do custom refs pollute normal clones/fetches?** → **No.**
- Fresh `git clone` (default refspec): **0.53s**, fetches **zero** `refs/multiverso/*` refs — the default fetch refspec only covers `refs/heads/*`.
- Explicit opt-in fetch `git fetch origin 'refs/multiverso/*:refs/multiverso/*'`: **0.72s**, all 1,200 arrive.
- Consequence for FI-1: publishing candidates/evidence refs is invisible to collaborators unless they opt in. ✅

**Q3. Are custom refs reachable via the REST API?** → **Yes.**
- `GET /repos/{o}/{r}/git/matching-refs/multiverso/intent/i7/` → 6 refs; paginated `matching-refs/multiverso/` → all 1,200. Standard pagination applies (100/page → 12 requests for the full namespace; fine under primary rate limits, mind the secondary limit noted in ch. 18).

**Q4. Can we bulk-prune (retention policy for rejected worlds)?** → **Yes.**
- Single push deleting 600 refs: **4.0s**. Remote count verified at 600 afterwards.

**Q5. Does `POST /repos/{o}/{r}/attestations` accept self-signed (local-key) DSSE bundles?** → **No — and this is a useful finding.**
- A syntactically valid Sigstore bundle (v0.3 mediaType, DSSE envelope, publicKey verification material, no Rekor entry) is rejected with:
  `422: failed to verify log inclusion: not enough verified log entries from transparency log: 0 < 1`
- GitHub validates transparency-log inclusion server-side. **M1's local-key receipts cannot be mirrored to the GitHub attestation store.** Mirroring becomes possible only with Sigstore keyless signing (TP-3, v1) or a private Rekor whose log GitHub federates (Enterprise path).
- PRD impact: none — FI-4 already makes git the canonical receipt store and the GitHub store a mirror; this probe confirms the mirror is v1+, not M1. ✅

## Not answered here (needs time or scale)

- **GC of objects reachable only from custom refs**: our refs point at commits also reachable from `main`; whether GitHub's GC preserves objects reachable *only* via `refs/multiverso/*` over months remains unproven. Mitigation stands (ch. 18): mirror evidence to a Multiverso-controlled remote.
- Behavior at 10k–100k refs on busy monorepos; Enterprise policy blocks on non-standard namespaces.

## Raw timings

| Operation | Result |
|---|---|
| push 1,200 new refs (one push) | 7.2s |
| `ls-remote` the namespace (1,200) | <1s |
| fresh default clone (ignores namespace) | 0.53s |
| explicit namespace fetch (1,200 refs) | 0.72s |
| bulk delete 600 refs (one push) | 4.0s |
| POST self-signed attestation bundle | 422 (tlog inclusion required) |
