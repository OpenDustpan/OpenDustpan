# 66. No internal project references in code or user documentation

Status: accepted · 2026-10-14

Changed by:

- ADR 251 extends: the words that copy uses.

## Context

Code comments, test names, error text and user documentation often cite
internal project records, for example "ADR 47 decision 6", "RFC 19" or
"(#530)". A reader of the code or a user of the documentation cannot use
these references. They point at the project's own process, not at the
behavior.

The owner decided this on 2026-10-14: "I don't want to see ADR, RFC or other
such internal project references in code. Not in README.md or any other
user-facing documentation either."

## Decision

1. Code and user documentation contain no internal project references. An
   internal project reference is a citation of an ADR, an RFC, an issue or
   pull request number, a milestone, or a research note. Code includes
   comments, tests, error and log text, and generated code templates. User
   documentation includes `README.md` and everything under `docs/guides/`,
   `docs/reference/` and `docs/explanation/`.
2. Say the reason or the behavior itself. Write "a restore keeps the target's
   chain record", not "see ADR 64 decision 6".
3. These files may still contain such references: ADRs, RFCs,
   `docs/research/`, `docs/roadmap.md`, `docs/agents/`, `CLAUDE.md`,
   `GLOSSARY.md` and `CONTRIBUTING.md`, plus commit messages and pull request
   descriptions.
4. No repository-wide rewrite. An agent or a contributor who edits a line that
   contains an internal reference removes the reference in the same change.
   A new or changed line never adds one.
5. Pull requests that were open on 2026-10-14 are exempt.

## Consequences

- Reviewers refuse a diff that adds an internal reference to code or user
  documentation.
- Existing references disappear over time, as the lines around them change.
- An automatic check on added lines may come later. It is not part of this
  decision.
