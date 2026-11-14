---
version: 1
---

## Problem

Operator removes trial and closed namespaces and needs deleted namespaces to disappear on a fixed schedule without manual steps.

## Evidence

- Main already has deleted-unit list, restore and sweep routes. Proof: GET /api/dustpan/v1/namespaces/{}/deleted-units in route table or API description
- Main already has a deleted-unit sweep route. Proof: ANY /api/server/deleted-units/sweep in route table or API description

## Fix boundary

- Batched purge of namespaces 30 days after deleted with a bounded batch ordered by delete time
- Docs page for the full namespace lifecycle including the purge timing

Out of scope:

- New namespace states
- Suspend, disable and delete transitions and notices
- Namespace filter and operator state routes

## Check

- Add failing server test for batched purge in NEW internal/namespacesvc/purge_test.go that lists candidates with GET /api/dustpan/v1/namespaces/{}/deleted-units with access token before purge Check: TestPurgeDeletedNamespacesAfter30Days in NEW internal/namespacesvc/purge_test.go
- Add failing server test for retention in NEW internal/namespacesvc/purge_test.go that reads state with GET /api/dustpan/v1/namespaces/{}/deleted-units with access token Check: TestKeepDeletedNamespaceBefore30Days in NEW internal/namespacesvc/purge_test.go
- Add failing server test for restore-wins recheck in NEW internal/namespacesvc/purge_test.go that drives restore with ANY /api/inventory/deleted-units/{}/{}/restore with access token Check: TestPurgeRechecksStateInTransaction in NEW internal/namespacesvc/purge_test.go
- Add failing server test for purge audit in NEW internal/namespacesvc/purge_test.go that reads the audit with GET /api/dustpan/v1/namespaces/{}/deleted-units with access token Check: TestPurgeWritesAuditEntry in NEW internal/namespacesvc/purge_test.go
- Implement bounded batched purge in NEW internal/namespacesvc/purge.go with per-namespace commit, stored cursor and single-runner fencing Check: `go test ./internal/namespacesvc -run TestPurge -count=1` passes
- Add docs page for the full namespace lifecycle with purge timing and preview list in NEW docs/guides/namespace-lifecycle.md Check: Docs page for namespace lifecycle renders
- Wire the scheduled purge task and run the e2e preview step with GET /api/dustpan/v1/namespaces/{}/deleted-units with access token then complete the usability check from the docs page alone Check: E2E step lists deleted units with GET /api/dustpan/v1/namespaces/{}/deleted-units with access token and `make check` reports zero findings

Depends on:

- add-suspended-disabled-deleted-states-namespace-routes
- add-suspended-disabled-deleted-states-timed-steps-notices
- `add-suspended-disabled-deleted-states-timed-steps-notices` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has source-filter coverage routes used as the e2e coverage pattern. (`e2e/coverage_sourcefilter.go:16 (func coverageSourceFilterRoutes)`)
- Main tests restore and purge of grants. (`pkg/access/access_test.go:679 (func TestRestoreAndPurgeGrants)`)
- Main tests that a worker finishes what the API started in split topology. (`pkg/dustpan/components_split_test.go:32 (func TestSplitTopologyWorkerFinishesWhatAPIStarted)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change add new namespace states or notices? No. States, filter, routes and timed steps come from earlier parts. This part adds only the batched purge at 30 days after deleted plus the docs page.
- Does the purge need a new API route? No. The purge runs as a scheduled server task over namespaces in state deleted past 30 days. It adds no route.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
