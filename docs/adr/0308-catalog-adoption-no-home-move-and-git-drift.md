# 308. Catalog: adoption of units, no move of a request, and git drift

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 October 2026

Changes:

- Extends ADR 110: adoption of units, no move of a request, and git drift.

Changed by:

- ADR 360 extends decision 2: an upgrade to a new blueprint version does not move a request.

## Context

The project owner made these decisions in the owner decision file 2026-10-17, entries dated 18 October. A check on 1 November 2026 found no ADR that states them. ADR 110 does not decide them.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **Adoption of existing units (owner, 2026-10-18).** The owner said: "Adoption of existing units into a blueprint: yes, now (first Catalog slice)".
2. **No move of a request (owner, 2026-10-18).** The owner said: "No move of a request between git and the store in v1".
3. **Git edits are drift (owner, 2026-10-18).** The owner said: "Record git edits of a request file from the forge webhook". The owner said also: "as drift between git and the Catalog".

## Consequences

- ADR 110 gets a pointer to this ADR.
- Each route of these decisions lands with its e2e step first.
