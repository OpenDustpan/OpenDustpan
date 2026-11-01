# 310. Run order, run options, per-run values, sources without git and state operations as jobs

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 October 2026

Changes:

- Changes ADR 267 decision 22: the run order.
- Extends ADR 254 decision 4: the run options target, replace and refresh-only.
- Extends ADR 254 decision 5: per-run values.
- Extends ADR 199 decision 1: sources and units that have no git commit.
- Changes ADR 304 decision 8: `state rm` and `state mv` as jobs.

## Context

The project owner made these decisions in the owner decision file 2026-10-17, entries dated 18 October. A check on 1 November 2026 found no ADR that states them.

ADR 267 decision 22 leaves the run order to a separate RFC. ADR 304 decision 8 says that the questions of RFC 109 stay open, among them `state rm` and `state mv` as jobs. The text "stay open" in ADR 304 decision 8 is drafting text. It is not a decision of the owner. The owner line of 18 October decides.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **Run order (owner, 2026-10-18).** The owner said: "Run order: declared AND inferred edges order runs". (PM, for the owner): the graph RFC decides the source of inferred edges. See "Not decided here".
2. **Run options (owner, 2026-10-18).** The owner said: "Run options: target, replace and refresh-only are all allowed". The owner said also: "Each still makes a plan, shows in the change order and passes approval rules". The owner said also: "targeted runs are marked in the change order and audit, and an approval rule can treat target or replace as dangerous". (PM, for the owner): the run options are tool-neutral: target, replace and refresh only. Each tool adapter maps them to the flags of its tool.
3. **Per-run values (owner, 2026-10-18).** The owner said: "Per-run values: allowed, secrets too (sealed for the run only, never shown)".
4. **Sources without git (owner, 2026-10-18).** The owner said: "Non-git sources: API upload, then OCI (compare Pulumi no-code stacks)".
5. **Units without git (owner, 2026-10-18).** The owner said: "Non-git units: teams upload their stacks/workspaces (code), and the variables/inputs come from profiles, like Pulumi no-code stacks". The owner said also: "Write-back to the uploaded code: no (option A)". The owner said also: "Dusty and Catalog upgrades may update the inputs/variables (profile values), through the normal profile change rules". (PM, for the owner): each automatic update of a profile value by Dusty or by a Catalog upgrade writes a record. The record names the actor and the reason. The update follows the normal profile change rules.
6. **State operations as jobs (owner, 2026-10-18).** The owner said: "`state rm` and `state mv`: also runner jobs (reviewed job kind with a preview and approval rules)". The owner said also: "next to `moved`/`removed` blocks in code". This closes the question of RFC 109 for these two commands. (PM, for the owner): the job kind is tool-neutral: "state operations: remove and move an address". Each tool adapter maps it to a command of its tool. The OpenTofu adapter uses `state rm` and `state mv`. The Pulumi adapter uses `pulumi state delete` and `pulumi state move`. The question of `import` and the question of a gate stay as ADR 304 decision 8 states them.

## Not decided here

- The source of the inferred edges of the run order. The graph RFC decides it.

## Consequences

- ADR 199, ADR 254, ADR 267 and ADR 304 get a pointer to this ADR.
- Each route of these decisions lands with its e2e step first.
