# 204. A user-facing feature needs its docs page in the same pull request

Status: accepted · 2026-10-18 · accepts part of RFC 57

## Context

Decided by the project manager under the owner's decision rule, 2026-10-18.

[RFC 57](../rfc/0057-documentation.md) asks "does a feature count as done
without" its docs. The definition of done in `CLAUDE.md` lists the checks
of `make check`. It names no docs. The user documentation is `README.md`,
`docs/guides/`, `docs/reference/` and `docs/explanation/` (ADR 66).

## Decision

1. **Docs in the same pull request.** A pull request that adds or changes a
   feature that a person or a client can see adds or changes its page in
   the user documentation. A feature that a person or a client can see has
   a route, a setting, a command, a flag, a console page or an error that a
   user reads. A pull request that changes only internal code needs no
   page.
2. **No page, not done.** A reviewer refuses such a pull request when the
   page is not in it. A later pull request for the docs does not make the
   first one done.
3. **The page follows the voice rules.** The page is in STE (ADR 14) and has
   no internal references (ADR 66). `make docs-lint` checks the guides, the
   reference and the explanations.

Benchmark: Google's engineering practices for code review tell a reviewer to
check that a change also updates the documentation when it changes how
users build, test, use or release the code.

## Consequences

- `CLAUDE.md` (`AGENTS.md`) adds this rule to its definition of done.
- A design pull request with ADRs only adds no page. The pull request that
  makes the feature adds it.
