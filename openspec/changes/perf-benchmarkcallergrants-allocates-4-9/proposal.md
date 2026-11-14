---
version: 1
---

Issue: #2937.

## Problem

Caller-grants allocations rose 4.9% in count and 10.4% in bytes per op after member state columns widened each member row on the authorization hot path.

## Evidence

- A CallerGrants benchmark exists on main in the postgres store test. Proof: pkg/store/postgres/postgres_test.go:2284
- The issue runs the CallerGrants benchmark against the sqlite store package. Proof: go test -run '^$' -bench CallerGrants -benchmem -count=6 ./pkg/store/sqlite
- Service.CallerGrants is the authorization read named by the issue. Proof: pkg/access/caller_access.go:211 (func Service.CallerGrants)
- A shared benchmark helper drives the CallerGrants benchmark. Proof: pkg/access/accesstest/bench_caller.go:33 (func BenchCallerGrants)
- Generated sqlite store code carries member source fields on main. Proof: pkg/store/sqlite/internal/db/queries.sql.go:312:  CAST(COALESCE(m.source, '') AS TEXT) AS member_source,
- The postgres query source carries the same member source fields. Proof: pkg/store/postgres/queries.sql:1609:  m.added_at_ms, m.source AS member_source, m.source_id AS member_source_id, m.state AS member_state,

## Fix boundary

- Remove member_source, member_source_id, and member_suspended from the caller-grants read in pkg/store for both dialects while keeping suspended-member exclusion by filter.
- Keep the three fields in the group membership views that need them.

Out of scope:

- Store source and source_id as one value.
- Time-per-op tuning; ns/op shows host noise in the issue.

## Check

- Record the failing CallerGrants benchmark on main, median of 6, in pkg/store/sqlite. No route: the test records benchmark allocations only. Check: go test -run '^$' -bench CallerGrants -benchmem -count=6 ./pkg/store/sqlite shows allocs/op above 3146 on main
- Add the NEW test TestCallerGrantsOmitsMemberSource in pkg/store/sqlite. It asserts the caller-grants query selects no member_source, member_source_id, or member_suspended column. No route: the test reads query text only. Check: go test ./pkg/store/sqlite -run TestCallerGrantsOmitsMemberSource fails on main
- Add the NEW test TestCallerGrantsRefusesSuspendedMember in pkg/store/sqlite and the same test in pkg/store/postgres. No route: the test calls the store only. Check: go test ./pkg/store/sqlite ./pkg/store/postgres -run TestCallerGrantsRefusesSuspendedMember passes on both dialects
- Narrow the caller-grants queries in pkg/store/postgres/queries.sql and the sqlite query source in pkg/store/sqlite to the old column set. Keep the suspended-member exclusion filter. No route: the test calls the store only. Check: go test -run '^$' -bench CallerGrants -benchmem -count=6 ./pkg/store/sqlite meets the quality budgets
- Keep the wide member source fields in the group membership queries at pkg/store/postgres/queries.sql:1609 and the sqlite query source. Add the NEW test TestMembershipViewKeepsMemberSource on both dialects. No route: the test calls the store only. Check: go test ./pkg/store/... -run TestMembershipViewKeepsMemberSource passes
- Regenerate sqlc output with make generate for pkg/store/sqlite/internal/db/queries.sql.go and pkg/store/postgres/internal/db/queries.sql.go. No route: the agent regenerates store code only. Check: make generate gives no diff beyond the narrowed caller-grants query
- Run affected store tests with the race detector. No route: the test calls the store only. Check: go test -race ./pkg/store/sqlite passes
- Confirm the tree passes all checks. No route: the agent runs static checks only. Check: make check with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- BenchmarkCallerGrants measures the caller-grants read. (`pkg/store/postgres/postgres_test.go:2284`)
- Service.CallerGrants serves the authorization read. (`pkg/access/caller_access.go:211 (func Service.CallerGrants)`)
- BenchCallerGrants helper drives the benchmark. (`pkg/access/accesstest/bench_caller.go:33 (func BenchCallerGrants)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: task "Regenerate sqlc output with make generate for pkg/store/sqli" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Confirm the tree passes all checks. No route: the agent runs" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: `internal/servertest/roundtrips_conflict_test.go` is in the touches of the stack plan, but the change names no such file: the plan is stale; run plan-refresh; a code check fails: and 2 more.

Questions considered and answered:

- Split the caller-grants query from the membership views or store source and source_id as one value? Keep the caller-grants read at the old column set and keep the three fields only in the group membership views. The issue names that split and it keeps the authorization hot path narrow.
- Does an ADR in the pack govern the caller-grants column set? No ADR is listed in the pack for this item, so the pack gives no ADR that governs the caller-grants column set.
- Does the change alter a route or a request flow? No. Only store query text changes, so no servertest round-trip file changes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
