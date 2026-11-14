---
version: 1
---

Issue: #1575.

## Problem

A developer lists stacks and workspaces while an operator restores the installation and gets mixed tags or a 500 driver error instead of one snapshot or a 503 try again.

## Evidence

- Postgres has snapshotOptions, inSnapshotConn, and readConflict helpers on main. Proof: pkg/store/postgres/postgres.go:1281 (func Store.inSnapshotConn); pkg/store/postgres/postgres.go:1293 (func Store.snapshotOptions); pkg/store/postgres/postgres.go:1323 (func readConflict)
- The two affected readers exist on main in the named files. Proof: pkg/store/postgres/workspace_page.go:15 (func Store.QueryWorkspaces); pkg/store/postgres/stack_page.go:17 (func Store.QueryStacks)
- SQLite has an inReadTx helper on main. Proof: pkg/store/sqlite/sqlite.go:1447 (func Store.inReadTx)
- The domain sentinel for an unavailable store exists on main. Proof: pkg/namespace/namespace.go:44 (namespace.ErrUnavailable)

## Fix boundary

- Postgres QueryWorkspaces keeps its statements but runs through inSnapshotConn with snapshotOptions and readConflict.
- Postgres QueryStacks and stackRange run ListStackPageInNamespace and ListStackPageInProject plus ListStackTagsOfStacks in one inSnapshotConn transaction with readConflict mapping.
- SQLite stack page and inventory QueryUnits run their statements in inReadTx.
- A guard fails on direct snapshot transactions and direct pool reads in page files, and swap-mid-read conformance covers the stack and workspace lists.

Out of scope:

- Other reads that skip the snapshot helpers beyond QueryWorkspaces, QueryStacks, sqlite stack page, and inventory QueryUnits.
- Restore swap behavior itself and the swap lock design.
- Adapter HTTP status mapping beyond returning namespace.ErrUnavailable from the store.

## Check

- Add failing swap-mid-read conformance cases with a fault hook between the page select and the tag select in NEW pkg/store/storetest/stack_workspace_swap_read_test.go. Check: NEW storetest swap-mid-read cases for QueryStacks and QueryWorkspaces fail on main
- Add a guard test in NEW pkg/store/postgres/snapshot_guard_test.go that fails on direct RepeatableRead, BeginTx, or s.queries pool reads in page files outside Store.snapshotOptions. Check: NEW TestSnapshotGuardFailsOnDirectSnapshotRead fails on main
- Route QueryWorkspaces through Store.inSnapshotConn with snapshotOptions and readConflict in `pkg/store/postgres/workspace_page.go:15`. Check: go test ./pkg/store/postgres/ ./pkg/store/storetest/
- Route QueryStacks and stackRange page selects plus ListStackTagsOfStacks through Store.inSnapshotConn in `pkg/store/postgres/stack_page.go:17`. Check: go test ./pkg/store/postgres/ ./pkg/store/storetest/
- Route the sqlite stack page through Store.inReadTx in `pkg/store/sqlite/stack_page.go:37`. Check: go test ./pkg/store/sqlite/ ./pkg/store/storetest/
- Route inventory QueryUnits through Store.inReadTx in `pkg/store/postgres/inventory_query.go:51` and its sqlite counterpart. Check: go test ./pkg/store/postgres/ ./pkg/store/sqlite/
- Verify Handler.listWorkspacePage maps namespace.ErrUnavailable to 503 try again in `internal/api/tfe/workspaces.go:459`. Check: go test ./internal/api/tfe/ with forced namespace.ErrUnavailable from QueryWorkspaces shows 503
- Wire check: call GET /api/v2/organizations/{}/workspaces with an access token against the real server during a staged swap in `internal/api/tfe/workspaces.go:459`; each page answers one snapshot or 503. Check: `GET /api/v2/organizations/{}/workspaces` with an access token during a staged restore swap. Confirmed by: CI (make e2e).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Postgres runs snapshot reads through inSnapshotConn with snapshotOptions. (`pkg/store/postgres/postgres.go:1281 (func Store.inSnapshotConn)`)
- snapshotOptions returns the options of a snapshot read. (`pkg/store/postgres/postgres.go:1293 (func Store.snapshotOptions)`)
- readConflict maps driver snapshot conflicts to the domain sentinel. (`pkg/store/postgres/postgres.go:1323 (func readConflict)`)
- SQLite runs reads through inReadTx. (`pkg/store/sqlite/sqlite.go:1447 (func Store.inReadTx)`)
- Postgres lists one workspace page through Store.QueryWorkspaces. (`pkg/store/postgres/workspace_page.go:15 (func Store.QueryWorkspaces)`)
- Postgres lists one stack page through Store.QueryStacks and stackRange. (`pkg/store/postgres/stack_page.go:17 (func Store.QueryStacks)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Should the two page reads use the existing snapshot helpers or add a new helper? Run both reads through the existing helpers: postgres Store.inSnapshotConn with Store.snapshotOptions and readConflict, sqlite Store.inReadTx. No ADR in the pack touches snapshot reads; the driver-error rule comes from the backend rules in CLAUDE.md.
- What test guards the fix? Extend the swap-mid-read conformance case to the stack and workspace lists and add a guard test against direct snapshot transactions and direct pool reads outside the helpers.
- Why does QueryWorkspaces need less change than QueryStacks? The workspace fix keeps the same statements and only changes the transaction options and error mapping; the stack fix moves page and tag selects into one snapshot transaction.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
