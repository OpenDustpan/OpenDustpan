---
version: 1
---

Issue: #2577.

## Problem

An embedder who writes a third-party store runs the store conformance suites, but the workspace and update suites live in single large files where parallel edits conflict and review is slow.

## Evidence

- pkg/store/storetest exists with workspaces.go present. Proof: paths: pkg/store/storetest exists true; pkg/store/storetest/workspaces.go exists true
- RunWorkspace runs the workspace conformance suite on main. Proof: pkg/store/storetest/workspaces.go:51 (func RunWorkspace)
- RunUpdates runs the update-cycle conformance suite on main. Proof: pkg/store/storetest/updates.go:53 (func RunUpdates)
- No workspaceLockCases or workspaceStateCases slice exists on main. Proof: rg -n -F workspaceLockCases pkg internal cmd matches 0; rg -n -F workspaceStateCases pkg internal cmd matches 0

## Fix boundary

- Split pkg/store/storetest/workspaces.go by area into NEW workspaces_<area>.go files with one ordered case slice each.
- Split pkg/store/storetest/updates.go by area into NEW updates_<area>.go files with one ordered case slice each.
- Keep RunWorkspace and RunUpdates behavior identical with the same case names in the same order.

Out of scope:

- Move of port suites to pkg/<domain>/<domain>test
- Split of suites other than workspaces and updates
- Move of dustpan wiring composition root
- Move of e2e helpers

## Check

- Record the ordered workspace subtest names on main on SQLite and PostgreSQL into the NEW files /var/tmp/storetest-before-workspace-sqlite.txt and /var/tmp/storetest-before-workspace-postgres.txt. Check: `go test -v ./pkg/store/storetest/ -run RunWorkspace` writes the NEW file /var/tmp/storetest-before-workspace-sqlite.txt
- Record the ordered update subtest names on main on both dialects into the NEW files /var/tmp/storetest-before-updates-sqlite.txt and /var/tmp/storetest-before-updates-postgres.txt. Check: `go test -v ./pkg/store/storetest/ -run RunUpdates` writes the NEW file /var/tmp/storetest-before-updates-sqlite.txt
- Move workspace cases by area into the NEW files pkg/store/storetest/workspaces_lock.go, workspaces_state.go, workspaces_outputs.go, workspaces_history.go, workspaces_inventory.go and workspaces_delete.go, and concatenate in RunWorkspace. Check: `go test ./pkg/store/storetest/` passes with identical subtest names in identical order
- Move update cases by area into the NEW files pkg/store/storetest/updates_<area>.go and concatenate in RunUpdates. Check: `go test ./pkg/store/storetest/` passes with identical subtest names in identical order
- Write the after lists to the NEW files /var/tmp/storetest-after-workspace-sqlite.txt, /var/tmp/storetest-after-workspace-postgres.txt, /var/tmp/storetest-after-updates-sqlite.txt and /var/tmp/storetest-after-updates-postgres.txt from pkg/store/storetest, and diff them against the before lists. Check: `diff /var/tmp/storetest-before-workspace-sqlite.txt /var/tmp/storetest-after-workspace-sqlite.txt` shows zero lines changed, and the same for the other three pairs
- Run the full store conformance in pkg/store/storetest on both dialects and attach the before and after name lists. Confirmed by: CI (go test on SQLite and PostgreSQL). Check: `go test -timeout 60m ./pkg/store/storetest/` passes
- Run the lint and test gate for the moved suites. Confirmed by: the driver's gates. Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- RunWorkspace executes the conformance suite of the workspace ports. (`pkg/store/storetest/workspaces.go:51 (func RunWorkspace)`)
- RunUpdates executes the conformance suite of the update-cycle ports. (`pkg/store/storetest/updates.go:53 (func RunUpdates)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

1. Should port suites move to pkg/<domain>/<domain>test, and who decides it once? Decides: owner.
   - Options: Keep port suites in pkg/store/storetest in this part; Move port suites to pkg/<domain>/<domain>test in this part.
   - Recommended: Keep port suites in pkg/store/storetest in this part. Reason: This part is a pure file split with no move.

Not spec-ready: a code check fails: route `/var/tmp/storetest-before-workspace-sqlite.txt` (.tasks[0].items[4].check) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: route `/var/tmp/storetest-after-workspace-sqlite.txt` (.tasks[0].items[4].check) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: `internal/servertest/roundtrips_conflict_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: and 2 more.

Questions considered and answered:

- Does this change move port suites to pkg/<domain>/<domain>test? No. The scope of this part keeps the suites in pkg/store/storetest and only splits files by area with unchanged order.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
