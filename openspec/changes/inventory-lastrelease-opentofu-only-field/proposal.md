---
version: 1
---

Issue: #2336.

## Problem

A problem of the shared core: the inventory entry carries LastRelease only for OpenTofu workspaces. Shared code must hold no field that one tool alone needs. The record stays on the workspace row and the workspace API shows it.

## Evidence

- The inventory defines a last-release type in the shared package. Proof: pkg/inventory/inventory.go:77 (type LastRelease), from the pack main facts
- The tool-neutral inventory API serves the last release. Proof: internal/api/inventory/operations.go:179 names apidesc.Named[lastReleaseEntry]("UnitLastRelease")
- The Postgres store reads the last release for the inventory. Proof: pkg/store/postgres/inventory.go:677 (func inventoryLastRelease)
- The inventory reference page documents the field. Proof: docs/reference/inventory-api.md exists in the pack paths; the issue text says line 86 describes it

## Fix boundary

- Remove LastRelease from the pkg/inventory entry and lastRelease from the tool-neutral unit view.
- Remove the inventory last-release columns from each dialect baseline and from the store code that reads them.
- Keep the record on the workspace row with its 24-hour match.

Out of scope:

- A change of the 24-hour match or of the repeated-unlock rule.
- A tool-neutral last-release definition for the Pulumi CLI.
- A compatibility alias for lastRelease.

## Check

- Write a test that an inventory entry of a workspace and of a stack has no last release. Check: TestInventoryEntryHasNoLastRelease fails on main
- Write a test that a repeated unlock within 24 hours still succeeds on the workspace row. Check: TestWorkspaceRepeatedUnlockUsesWorkspaceRow
- Remove the field from pkg/inventory, internal/api/inventory, pkg/store/postgres/inventory.go and the SQLite adapter, and the columns from both baselines. Check: sqlc diff is clean
- Run the unlock and force-unlock e2e step against the real OpenTofu CLI on ANY /api/v2/workspaces/{}/actions/unlock with an access token. Check: make e2e
- Update docs/reference/inventory-api.md. Check: make docs-lint
- Do the usability check from the docs page, then run make check. Check: make check with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines the inventory LastRelease type. (`pkg/inventory/inventory.go:77 (type LastRelease)`)
- Main defines the Lock type that both tools fill. (`pkg/inventory/inventory.go:67 (type Lock)`)
- Main reads the inventory last release in the Postgres store. (`pkg/store/postgres/inventory.go:677 (func inventoryLastRelease)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 13: Separate tool models; shared access control and inventory** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 19 extends; ADR 41 changes; ADR 150 changes; ADR 247 extends; ADR 265 extends; ADR 337 extends. Chain to the end: ADR 19, ADR 41, ADR 150, ADR 247, ADR 265, ADR 337, ADR 47, ADR 248, ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 154, ADR 199, ADR 210, ADR 242, ADR 153, ADR 346, ADR 286, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 19, 41, 150, 247, 265, 337.
- Quote: "Tool-specific views read the tool's own model."
- Effect: The inventory holds no OpenTofu-only field.
- Conflict: none.

**ADR 337: The last released lock is in the workspace API and not in the inventory** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The record is still kept on the workspace row."
- Quote: "Only the API that shows it changes."
- Effect: The record stays on the workspace row. Only the inventory field is removed.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: a change on locks, leases, durable operations or job phases needs "failure_modes": each step, its failure, the retry and the state after it; a code check fails: task "Run the unlock and force-unlock e2e step against the real Op" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Do the usability check from the docs page, then run make che" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: and 2 more.

Questions considered and answered:

- Does this change need an RFC first? No. ADR 337 decides the outcome (owner): the record stays on the workspace row and leaves the inventory.
- Does the record become tool-neutral for the Pulumi CLI? No. ADR 337 decision 2 keeps the rule unchanged.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
