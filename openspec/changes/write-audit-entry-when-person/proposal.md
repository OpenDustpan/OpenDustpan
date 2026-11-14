---
version: 1
---

Issue: #2130, #2351, #2401.

## Problem

A Developer restores an earlier state of a stack or workspace to undo a bad change and must later prove who restored what from which version and why. Without the entry the next update acts on a state that no record explains.

## Evidence

- Unit state restore paths exist for both tools on main. Proof: Routes ANY /api/stacks/{}/{}/{}/restore/{} and ANY /api/v2/state-versions/{}/actions/restore; internal/api/tfe/state_write.go:253 (func Handler.writeStateVersionCreate); pkg/store/postgres/workspaces.go:661 (func Store.FinalizeStateVersion); pkg/store/postgres/unit_state.go:239 (func restoreUnitState).
- Installation restore writes audit through appendEvents and FinishRestore on main. Proof: pkg/backup/restore_steps.go:361 (func RestoreService.appendEvents); pkg/store/postgres/restore_operation.go:517 (func Store.FinishRestore).
- Issue #2401 names four ended-restore paths with no end entry and log lines without cause. Proof: Issue text cites pkg/backup/restore_steps.go:367-371, restore_steps.go:362-364, restore_service.go:750-752, restore_service.go:727-730 and restore_service.go:601-603.
- No ADR text applies to this change in the pack. Proof: Pack field adrs is an empty list, so there are no decision lines to quote.

## Fix boundary

- Write one audit entry in the same transaction as each Pulumi stack and OpenTofu workspace state restore, with actor, credential kind, unit, both versions, reason and state.write decision record id or null.
- Always attempt the installation restore end entry, also when RestoreEvents or readRestoreInput fails; retry a missing end entry once on the next sweep; keep success only at the real end.
- Make the audit append a construction-checked port with a conformance case and log failures with restore id, error class and cause.

Out of scope:

- Installation backup restore entries beyond the ended-restore gaps named in #2351 and #2401.
- Deleted-unit restore route and console recovery pages.

## Check

- Confirm the two live unit restore paths in pkg/backup/restore_steps.go:50 (func readRestoreInput). Check: Read pkg/backup/restore_steps.go:50 (func readRestoreInput) and pkg/store/postgres/unit_state.go:239 (func restoreUnitState) to list both live paths.
- Write failing unit restore tests in pkg/backup/restore_steps.go:361 (func RestoreService.appendEvents). Check: Failing test TestUnitRestoreWritesAuditEntry fails on main in pkg/backup/restore_steps.go:361 (func RestoreService.appendEvents).
- Write the unit restore audit entry for both tools in pkg/store/postgres/unit_state.go:239 (func restoreUnitState). Check: TestUnitRestoreWritesAuditEntry passes after the change in pkg/store/postgres/unit_state.go:239 (func restoreUnitState).
- Fix the ended-restore audit paths in pkg/backup/restore_steps.go:361 (func RestoreService.appendEvents). Check: TestRestoreEndEntryPaths passes after the change in pkg/backup/restore_steps.go:361 (func RestoreService.appendEvents).
- Wire the service and store; add no new route. The existing routes keep their authorization actions. Check: The route test passes for ANY /api/stacks/{}/{}/{}/restore/{} and ANY /api/v2/state-versions/{}/actions/restore.
- Add an e2e step against ANY /api/stacks/{}/{}/{}/restore/{} with an access token that restores and reads ANY /api/audit/entries. Confirmed by: CI (make e2e). Check: The e2e step calls ANY /api/stacks/{}/{}/{}/restore/{} with an access token and reads ANY /api/audit/entries. Confirmed by: CI (make e2e).
- Write the docs page for the unit restore audit entry. Confirmed by: CI (make docs-lint). Check: The page names the entry fields and its commands run as written. Confirmed by: CI (make docs-lint).
- Run the usability check where a Developer restores from the docs page alone. A person's step. Check: A Developer restores from the docs page alone. A person's step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Store can finish a restore row. (`pkg/store/postgres/restore_operation.go:517 (func Store.FinishRestore)`)
- Store can fail stale ended restore rows. (`pkg/store/postgres/restore_operation.go:617 (func Store.FailEndedRestores)`)
- RestoreService appends restore audit events. (`pkg/backup/restore_steps.go:361 (func RestoreService.appendEvents)`)
- Restore input decode exists. (`pkg/backup/restore_steps.go:50 (func readRestoreInput)`)
- Postgres restores unit state. (`pkg/store/postgres/unit_state.go:239 (func restoreUnitState)`)
- Error cause helper exists for log lines. (`internal/api/faultlog/faultlog.go:133 (func Cause)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change cover installation backup restore audit too? No. This change covers only unit state restores for both tools plus the ended-restore gaps of #2351 and #2401. Installation backup restore entries beyond those gaps are out of scope.
- Must the audit entry commit in the same transaction as the state change? Yes for unit restore: the entry commits in the same transaction as the state row. For installation restore the end entry is attempted at the end and retried once by the next sweep under a unique constraint per restore id.
- Is the deleted-unit restore route in scope? No. The deleted-unit restore route at internal/api/inventory/deleted.go:401 (func Handler.restoreUnit) is out of scope. The first task confirms the two live paths only.
- Where does the restore reason come from when the tool APIs are fixed? No new tool field. The entry records a reason only when the calling route supplies one, else empty. A reason over 500 characters is refused with error code audit.reason-invalid and no echo of the reason.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
