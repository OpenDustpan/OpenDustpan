# 195. The tool-neutral word for a plan and a preview

Status: accepted · 2026-10-18 · part of ADR 190

## Context

The Pulumi CLI calls the run that shows changes and applies nothing a
"preview". OpenTofu calls it a "plan". A change order holds such a result
for units of both tools (ADR 190), so shared code, the tool-neutral API and
the console need one word for it. The project manager asked for a decision
in an ADR, or an RFC question. User-facing text keeps the word of each tool
(ADR 13).

Facts:

- The job model already uses `plan` as a tool-neutral word: the operation
  `plan`, the phase kind `plan`, and the stored plan with the prefix
  `pln-` (ADR 100 decision 3, ADR 102).
- `GLOSSARY.md` lists "Preview" as a term of the Pulumi CLI, with
  _Avoid_ "Plan". It lists "Plan" as a term of OpenTofu, with _Avoid_
  "Preview (for OpenTofu)". It flags that "plan" has four meanings.
- Other words have other owners. "Change set" is a word of other tools. "Dry
  run" names a flag. "Proposal" says nothing about the content.

## Decision

1. **Shared code and the tool-neutral API say `plan`.** The word is
   `plan` in Go names, API paths, JSON fields and audit entries, as in the
   job model. A change order has a plan, a plan revision has unit plans,
   and each unit plan names a stored plan. The tool-neutral API never says
   `preview`. **One-way door:** API fields and audit entries hold the
   word.
2. **User-facing text for one unit keeps the word of its tool.** A page, a
   label or a message about one unit says "preview" for a Pulumi unit and
   "plan" for an OpenTofu unit. The API answers the `tool` of each unit
   (`pulumi` or `opentofu`), and never a word for the plan. The console
   maps the tool to its word. The core has no switch on the tool for this
   word.
3. **User-facing text for units of two tools says "proposed changes".**
   The heading of a change order, of its revisions and of its review
   view, says "Proposed changes" when its units are of more than one tool.
   When all its units are of one tool, the heading uses the word of that
   tool: "Preview" or "Plan". The console picks the heading from the
   `tool` of each unit in the answer of the API.
4. **The terms of the change order.** These terms join `GLOSSARY.md` with
   this rule: "plan revision" and "unit plan" in shared code and docs, and
   the word of the tool, or "proposed changes", in the console.

## Consequences

- No new word joins the code. The job model and the change order use one
  word.
- A Pulumi user never reads "plan" for a preview of a stack in the
  console. The API answers `plan` for both tools.
- The flagged ambiguity "Plan" in `GLOSSARY.md` gets two more meanings: a
  plan revision and a unit plan. Docs name which one.

## Open questions

- Does the CLI of OpenDustpan, when it exists, follow the rule of the
  console or the rule of the API?
