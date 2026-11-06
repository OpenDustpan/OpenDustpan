# 309. Bulk actions, batch records and the delete guard

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 October 2026

Changes:

- Changes ADR 153 decision 9: the override of the delete guard needs its own permission.
- Extends ADR 213 decision 5: the batch record and the first bulk actions.

## Context

The project owner made these decisions in the owner decision file 2026-10-17, entries dated 18 October. A check on 1 November 2026 found no ADR that states them. ADR 213 decision 5 sets bulk calls of up to 100 IDs. It leaves the first bulk actions open. ADR 153 decision 9 gives the guard of the Pulumi CLI.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **A batch is a record (owner, 2026-10-18).** The owner said: "Batch is a record of its own".
2. **The first bulk actions (owner, 2026-10-18).** The owner said: "First bulk actions: cancel, lock, unlock, ignore findings, plus bulk run start (plan/apply". The owner said also: "each goes through its own gates". The owner said also: "No bulk approve".
3. **A delete of a unit with resources (owner, 2026-10-18).** The owner said: "by default, a delete of a unit that still has resources is blocked". The owner said also: "An explicit override needs a separate permission". The owner said also: "The console shows a 'create destroy change order' button for units that can destroy". The owner said also: "(runner pool, or a unit that supports reverse GitOps, and so on)". (PM, for the owner): the `force` path of the Pulumi CLI must still pass the check of that permission. The implementation adds an e2e step for it.

## Consequences

- ADR 153 and ADR 213 get a pointer to this ADR.
- Each route of these decisions lands with its e2e step first.
