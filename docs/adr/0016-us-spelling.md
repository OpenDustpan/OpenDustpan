# 16. US spelling for English text

Status: accepted · 2026-10-10

## Context

The project's English mixed American and British spelling: "color" in code
and CSS, "colour" and "licence" in the docs, "canceled" in Go comments next
to a `StatusCancelled` constant. RFC 11 needs one spelling for the English
source that translations start from.

Code tools assume US spelling. golangci-lint's `misspell` already checks Go
comments with the US locale. CSS (`color`, `center`), most API fields and most
contributors use it too. There is no word list for Oxford spelling, so a
British rule could only be checked in review.

## Decision

- All English text in both repositories uses US spelling: docs, ADRs, RFCs,
  the glossary, code comments, commit messages, UI text, error messages and
  log messages. Examples: "color", "license", "behavior", "organization",
  "canceled", "catalog", "center".
- Identifiers that we name use US spelling too. `stack.StatusCancelled`
  becomes `stack.StatusCanceled`.
- Wire values and other projects' names keep their own spelling. The Pulumi
  CLI's update status `"cancelled"` stays, with its `//nolint:misspell`.
- `misspell` keeps the US locale, so the gate checks Go comments.
- The project owner allowed one exception to ADR 1: this change corrects
  spelling in accepted ADRs 2, 8, 12 and 13. The meaning of each ADR does
  not change. Later spelling corrections in accepted ADRs need the same approval.

## Consequences

- The console's English source is US English. English (UK) is a small
  variant catalog that lists only the words that differ (RFC 11).
- UI text says "Canceled", while the Pulumi CLI's wire value stays
  `"cancelled"`.
- `make docs-lint` does not check spelling. For Markdown, reviews check it.
