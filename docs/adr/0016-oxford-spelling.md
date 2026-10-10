# 16. Oxford spelling for English text

Status: accepted · 2026-10-10

## Context

The project's English mixed British and American spelling: "colour" and
"licence" in the brand book, "license" and "behavior" in some headings and
comments, "canceled" in code comments next to a `StatusCancelled` constant.
The ASD-STE100 skill (ADR 14) sets no spelling. RFC 11 needs one spelling for
the English source that translations start from.

## Decision

- English text in this project uses Oxford spelling: British spelling with
  `-ize` endings. Examples: "organization", "recognize", "colour", "licence"
  (noun), "behaviour", "centre", "cancelled", "catalogue".
- The rule covers all prose that we write: docs, ADRs, RFCs, the glossary,
  code comments, commit messages, UI text, error messages and log messages.
- Machine-made text keeps its own spelling: identifiers, flags, API fields,
  commands, configuration keys, and names of other projects and licences.
  Examples: Go's `context.Canceled`, CSS `color`, `-no-color`, "SIL Open Font
  License".
- New identifiers that we name follow Oxford spelling too, as
  `StatusCancelled` already does, unless a tool's API fixes the word.
- Accepted ADRs before this one keep their spelling, because ADR 1 forbids
  edits to them. ADR 2 says "organised".

## Consequences

- The English source of the console is British English, and English (US) is
  a small variant (RFC 11).
- `-ize` matches the word that the Pulumi and TFE APIs use: "organization".
- golangci-lint's `misspell` used the US locale and rejected "cancelled" and
  "behaviour" in comments. Its UK locale rejects `-ize`. So `misspell` now
  runs with no locale: it still catches typos, and it accepts every variety
  of English.
- `make docs-lint` does not check spelling. Reviews check the spelling
  variety, until a word list check proves worth its cost.
