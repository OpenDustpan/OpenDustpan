---
version: 1
---

Issue: #2348.

## Problem

An operator restarts the server and each open pays about 3 ms extra. The fix removes the extra round trips and keeps each refusal check.

## Evidence

- postgres.Open lives on main in pkg/store/postgres. Proof: pkg/store/postgres/postgres.go:227 (func Open)
- The open path uses hasVersionColumn and verifyRecorded with migrate.Check. Proof: pkg/store/postgres/postgres.go:1587 (func hasVersionColumn); pkg/store/postgres/postgres.go:1637 (func verifyRecorded); pkg/store/internal/migrate/migrate.go:153 (func Check)
- Issue 2348 reports migrated PostgreSQL open rose from 16.7 ms to 19.7 ms p50 and names the extra information_schema and per-migration SELECTs as cause. Proof: Found in the round 9 performance check (`6c35afa` vs `842af92`, PostgreSQL 18.6). Raw data: branch `perf/round-9`, `raw/migrate-*`, `raw/bisect-migrate-*`, test `tests/perf9_migrate_pg_test.go`.

## Fix boundary

- Read schema_migrations columns once per open transaction and reuse the result
- Read recorded migration rows in one single SELECT in verifyRecorded and compare in Go

Out of scope:

- Fresh database open time on PostgreSQL
- SQLite open path
- Profiling of file count versus time

## Check

- Add query-count test for migrated open in NEW file pkg/store/postgres/open_roundtrip_test.go that asserts at most 2 schema_migrations metadata queries Check: `go test ./pkg/store/postgres/ -run TestMigratedOpenQueryCount` in NEW file pkg/store/postgres/open_roundtrip_test.go fails on main by counting more than 2 metadata queries
- Batch column read and recorded-row read in pkg/store/postgres/postgres.go:227 (func Open) Check: Same test in NEW file pkg/store/postgres/open_roundtrip_test.go passes after the edit to pkg/store/postgres/postgres.go:227 (func Open)
- Show tampered name, tampered sha256, unknown version, duplicate version, and NULL sha256 still refuse open in NEW file pkg/store/postgres/open_roundtrip_test.go Check: `go test ./pkg/store/postgres/ -run TestMigratedOpenRefusesTamperedRow` in NEW file pkg/store/postgres/open_roundtrip_test.go passes, covering scenarios tampered-name-refuses, tampered-sha256-refuses, unknown-version-refuses, duplicate-version-refuses, and null-sha256-refuses
- Run make check with zero findings Check: `make check` with zero findings after the wiring in pkg/store/postgres/postgres.go:227 (func Open)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- postgres.Open opens the PostgreSQL store. (`pkg/store/postgres/postgres.go:227 (func Open)`)
- hasVersionColumn checks schema_migrations columns on open. (`pkg/store/postgres/postgres.go:1587 (func hasVersionColumn)`)
- verifyRecorded refuses a database that records an unknown version. (`pkg/store/postgres/postgres.go:1637 (func verifyRecorded)`)
- migrate.Check plans recorded against embedded migrations. (`pkg/store/internal/migrate/migrate.go:153 (func Check)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does cutting open round trips break an ADR decision? Pack adrs list is empty, so no ADR decision lines apply. The fix keeps verifyRecorded separate from migrate.Check with its own Go comparison, so no ADR conflict arises.
- Is there a contract or e2e path to keep? postgres.Open at pkg/store/postgres/postgres.go:227 with hasVersionColumn at pkg/store/postgres/postgres.go:1587 and verifyRecorded at pkg/store/postgres/postgres.go:1637. No route or user-visible behavior changes, so no console, CLI, or e2e path changes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
