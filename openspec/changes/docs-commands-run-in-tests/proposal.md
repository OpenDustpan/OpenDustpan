---
version: 2
---

## Why

**Persona: operator.** Operator installs and runs OpenDustpan from the docs before the first tag. Today only the compose quickstart runs in a test, so a copied command from another page fails on the first try.

The user path this change must make work:

1. **Docs page.** Operator opens the install guide page and copies each command block in order.
2. **CLI.** Operator pastes each command into a terminal in a clean work directory.
3. **E2e.** The test runner extracts the same blocks and runs the same text in a fresh per-page work directory.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Copied command fails as written | The docs check failed on page <page> block <index>; run `make docs-commands-check` to see the failing command and its output. | `make docs-commands-check` |

Evidence for the claims above:

- Docs source directories exist at docs/guides and docs/reference. Proof: paths: docs/guides exists, docs/reference exists
- Only the compose quickstart runs in a test today. Proof: e2e/compose_test.go:56 TestComposeQuickstart

## What Changes

- Extract each fenced shell command from docs/guides, docs/reference and README into a tested corpus.
- Run each corpus command as written in a test fixture and fail the gate on an unrun or failing block.
- Fix pages so every listed command passes as written.
- This change builds first: each sibling new page of this milestone adds its corpus entries in the same pull request that adds the page, covering the production install page, the install-from-artifacts page, the SDK guide, the runner guide, and the key rotation docs.

Out of scope:

- Tagging and publishing a release
- Rewrite of guide prose beyond command fixes
- Fences tagged console, indented code blocks and inline code

## Usability

Where the path still costs the person time or surprises them:

- Operator copies a long block and misses one line; pages keep one runnable block per step with its own check result.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main runs the compose quickstart in an e2e test. (`e2e/compose_test.go:56 (func TestComposeQuickstart)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which docs pages and which fence forms are in scope for the command check? The milestone roadmap text orders it: docs/guides, docs/reference and README are in scope. Fences tagged sh, bash or shell are in scope. All other forms are out of scope.
- Does the existing compose quickstart test stay as the model for new coverage? The pack proves e2e/compose_test.go:56 TestComposeQuickstart runs the compose quickstart today. It keeps passing. Blocks that need Docker run only when E2E_COMPOSE=1 and are skipped otherwise.
- Which fenced blocks may skip the run while the check still passes? A skipped block without a marker hides an unrun command. The change uses a closed marker and a closed reason allowlist, fails stale entries, and requires each skipped block to carry a marker.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
