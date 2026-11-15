# 375. AI-authored pull requests link a spec-ready OpenSpec change

Status: accepted · 2026-11-08. Decided by the project owner

Changes:

- Extends ADR 76 decision 2: an AI-authored pull request also names its
  spec-ready OpenSpec change.
- Extends ADR 317 decision 2: the author email of an agent commit marks a
  pull request as AI-authored.

## Context

Agent work starts only from an OpenSpec change whose proposal says "Open
questions: none" (`openspec/project.md`, the spec-ready gate). Nothing checks
that an agent pull request names such a change. The project owner decided on
8 November 2026 that CI checks it, and that the check applies to AI-authored
pull requests only.

## Decision

1. **CI fails an AI-authored pull request that links no spec-ready change
   (owner, 2026-11-08).** The owner said: "For AI issues only". A pull request
   is AI-authored when a commit has the author email of an agent commit (ADR
   317 decision 2) or when its body discloses an AI tool (ADR 76 decision 2).
   The body must name `openspec/changes/<id>` of a change whose proposal says
   "Open questions: none". An archived change counts.
2. **A pull request of a person is exempt (owner, 2026-11-08).** CI does not
   check a pull request with no AI signal.

## Consequences

- The OpenSpec change `ai-pr-links-spec-ready-change` adds the script, the
  Makefile target and the CI step.
- The error of the check names the one fix: the line that names a spec-ready
  change.
