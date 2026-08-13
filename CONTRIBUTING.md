# Contributing to Multiverso

Multiverso is built in public. Issues, discussion, and PRs are welcome.

## License and sign-off

- All contributions are licensed under [Apache-2.0](LICENSE).
- We use the **[Developer Certificate of Origin](https://developercertificate.org/)** (DCO), **not a CLA**. Sign off every commit:

```bash
git commit -s -m "your message"
```

The `Signed-off-by:` trailer certifies you have the right to submit the work under Apache-2.0. This is deliberate: DCO-only means no entity (including us) can relicense your contribution out from under the community. See [research ch. 20](research/20-oss-strategy-license-naming.md) for the reasoning.

## Ground rules

- **Oracles outrank agents**: PRs that touch the evidence or trust planes need tests that demonstrate the property they claim, not descriptions of it.
- AI-assisted contributions are welcome (most of this codebase is agent-built, human-supervised) — you sign off, you own it.
- Match the surrounding code. `gofmt`, `go vet`, and the test suite must pass.

## Where things are

- [`PRD.md`](PRD.md) — what we're building and why; requirement IDs (CP-x, EP-x…) are referenced in code comments.
- [`research/`](research/) — the research corpus behind every design decision.
- [`docs/design/`](docs/design/) — milestone design docs.
- [`BUILDLOG.md`](BUILDLOG.md) — the public build journal.
