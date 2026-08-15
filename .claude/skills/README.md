# Skills

Invocable, repo-scoped procedures for building and validating Multiverso. Each is a packaged version of something we were doing by hand — written down so it runs the same way every time, by anyone, including six months from now.

| Skill | What it does | When |
|---|---|---|
| [`gate`](gate/SKILL.md) | The full verification gate — fmt, vet, tests, race, both acceptance scripts, the adversarial corpus — with flake discipline | Before every commit; after every block |
| [`adversarial`](adversarial/SKILL.md) | Run the laundering corpus, then invent and **execute** new attacks on the trust boundary | After any change to oracles, policy, gates, backends, or admission |
| [`audit-requirements`](audit-requirements/SKILL.md) | Honest DONE/PARTIAL/MISSING audit against the PRD, with `file:line` evidence | Before claiming a milestone; before updating README Status |
| [`design-partner`](design-partner/SKILL.md) | Emulate partners evaluating the product from published docs alone | Before claiming usability; after docs or CLI changes |
| [`ship-block`](ship-block/SKILL.md) | The block pipeline: contract → build → integrate → adversarial review → fix → log → ship | Starting any substantial roadmap block |

They escalate in cost and in confidence: `gate` runs in a minute and tells you nothing is obviously broken; `adversarial` tells you whether the product's central claim still holds; `audit-requirements` tells you whether a milestone claim is true; `design-partner` tells you whether any of it is usable by someone who did not build it.

Two of these found things nothing else did. `design-partner` found that the candidate authors its own oracle evidence — the violation of the rule the entire product rests on — by simply refusing to let three agents read the source. `ship-block`'s adversarial review stage found a `git ls-remote` suffix match that turned namespace surveys into refspecs that could delete real branches. Neither was reachable from inside the build.

The rule they share: **an unrun check is not a passed check, and a gap you publish is worth more than a claim you cannot defend.**
