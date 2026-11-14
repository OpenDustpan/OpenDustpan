---
version: 1
---

Issue: #3018.

## Problem

Deploy failures show long provider error chains with no clear next step, and the raw detail fills the summary line.

## Evidence

- The server already sends stable error codes with arguments. Proof: internal/errcode/catalog_request.go:7 defines error code `request.body-invalid`.
- Restore errors already keep driver detail out of user text. Proof: pkg/backup/restore_service_test.go:801 holds `TestRestoreErrorTextHoldsNoDriverDetail`.

## Fix boundary

- Record the 1am-readable error rule as a backlog design: summary line with what failed, object, field, and next command; separate named upstream detail; request ID link.

Out of scope:

- Change of any error code meaning or argument
- Change of Pulumi, TFE-compatible, or OpenTofu HTTP backend shapes or text
- Error constructor, verbose flag, and console detail view

## Check

- Refine this change when its RFC is accepted; the NEW file openspec/changes/design-idea-error-output-person/tasks.md holds the refined tasks Check: `git status --short openspec/changes/design-idea-error-output-person/tasks.md`

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Request error answers use a catalog of stable dotted codes. (`internal/errcode/catalog_request.go:7 (request.body-invalid)`)
- Restore error text holds no driver detail. (`pkg/backup/restore_service_test.go:801 (func TestRestoreErrorTextHoldsNoDriverDetail)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 276: Every error answer has a code and its arguments** (owner-tagged: decision 1, 4, 5).

- Decisions that apply: 1 (current), 4 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Every error answer has a code (owner). Each 4xx and 5xx answer of each HTTP adapter has a stable machine code."
- Quote: "The `message` of the camelCase shapes and the `title` and the `detail` of JSON:API stay as they are."
- Quote: "In the camelCase shapes, `code` stays the HTTP status, because the Pulumi CLI reads it."
- Effect: No wire change in this change. Tool-compatible shapes keep their codes, message, title and detail. Any separate detail field or request ID needs a later RFC.
- Conflict: none.

**ADR 364: Unexported fields and golangci-lint before added checks (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Fields are unexported, so that only a constructor can make a value, and the constructor checks the invariants of the value."
- Effect: A later error value follows visibility first: unexported fields with a constructor that checks invariants, with no new tool.
- Conflict: none.

## Open questions

1. Which error shapes may carry the separate detail field and request ID? Decides: owner.
   - Options: Tool-neutral shapes only after an RFC that changes ADR 276; All shapes including Pulumi and TFE-compatible shapes.
   - Recommended: Tool-neutral shapes only after an RFC that changes ADR 276 Reason: ADR 276 decisions 4 and 5 fix tool-compatible text and fields, so only tool-neutral shapes can change and only through an RFC.
2. How does the person reveal the separate detail and find the full record by request ID? Decides: PM.
   - Options: A flag shows the detail and the log lookup by request ID; The console only shows the detail with no flag.
   - Recommended: A flag shows the detail and the log lookup by request ID Reason: The pack proves no verbose view on main, so the reveal path needs a later RFC.
3. Which fields does the later error constructor require? Decides: PM.
   - Options: Constructor requires summary, object, field, and next step; Constructor requires summary and next step only.
   - Recommended: Constructor requires summary, object, field, and next step Reason: ADR 364 asks for a constructor that checks invariants, but the pack names no error type on main.

Not spec-ready: a code check fails: `openspec/changes/design-idea-error-output-person/tasks.md` is named by the design or a task but is not in the touches of the stack plan.

Questions considered and answered:

- Does this change replace error codes or change tool-compatible shapes? ADR 276 decisions 1, 4 and 5 answer it: codes stay, message and title and detail text stays, and camelCase code stays the HTTP status. This change makes no wire change.
- How is the constructor rule enforced without a new tool? ADR 364 decision 1 answers it: unexported fields with a constructor that checks invariants, plus existing golangci-lint rules such as exhaustruct, with no new tool.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
