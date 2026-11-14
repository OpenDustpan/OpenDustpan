---
version: 1
---

Issue: #1425.

## Problem

An Embedder runs store suites and waits 85 s instead of 32 s because each hostile approvers case rebuilds, exports, and stages a fresh store.

## Evidence

- Store suites slowed down and the growth comes from TestDevDataConformance. Proof: Issue #1425: pkg/store/sqlite went from 31.6 s at 8291c75b to 67.8 s at 1f465e0d and 85.4 s at 9464ad0; TestDevDataConformance sum 73.7 s and 93.7 s.
- Each hostile approvers case opens a fresh store, writes fixtures, exports, stages, and runs the swap. Proof: Issue #1425 CPU profile section: Each hostile case opens a fresh store (all migrations), writes fixtures, takes a full portable export, stages it (all migrations again) and runs the swap.
- pkg/store/sqlite and pkg/store/postgres test packages exist. Proof: Pack paths: pkg/store/postgres exists, pkg/store/sqlite exists.

## Fix boundary

- Reuse one migrated template database copy per test instead of migrating per fresh store
- Build the dev-data export and staged area once per hostile-body group and vary only the approvers row

Out of scope:

- SQLite connection and statement parsing fix
- Production request path changes

## Check

- In pkg/store/storetest/restore_operation.go:778 (func testRestoreOpEnter) reuse one built export and staged copy per hostile-body group, copy per case and vary only the approvers row, with a check that each varied row reaches strip and swap. Check: `go test -count=1 ./pkg/store/sqlite/ -run TestDevDataConformance -v` passes with unchanged assertions and lower wall time
- In pkg/store/storetest and the sqlite and postgres package test setup files behind pkg/store/postgres/postgres_test.go:909 (TestDevDataConformance) give each store test a copy of one migrated template database with unique names, no sessions on the postgres template, checkpointed sqlite copy, parallel-safe copies, and fail-closed setup. Check: `go test -count=1 ./pkg/store/sqlite/ ./pkg/store/postgres/` passes without weakened assertions and with per-copy cleanup
- In pkg/store/sqlite/wal_test.go:779 (TestWALGuardBusyBackoff) confirm timing-sensitive suites pass without flakes under load. Check: `go test -count=1 ./pkg/store/sqlite/ -run 'TestWALGuardBusyBackoff|TestJobConformance|TestDurableScheduleConformance'` passes
- Run full store suites and lint for pkg/store/sqlite and pkg/store/postgres changes. Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- TestDevDataConformance already runs in store suites. (`pkg/store/postgres/postgres_test.go:909 (TestDevDataConformance)`)
- SQLite stores open through sqlite.Open. (`pkg/store/sqlite/sqlite.go:346 (func Open)`)
- Postgres stores open through postgres.Open. (`pkg/store/postgres/postgres.go:227 (func Open)`)
- Timing-sensitive store tests already exist and fail under load. (`pkg/store/sqlite/wal_test.go:779 (TestWALGuardBusyBackoff)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the fix change store behavior, migrations, or ADRs? No. Issue #1425 states migration no, ADR change no, and no test is skipped or weakened. Only fixture setup changes in pkg/store/storetest and the sqlite and postgres package test setup files.
- Does this change include the SQLite connection and statement fix? No. The SQLite connection and statement-parsing fix stays a separate issue. This change keeps only fixture reuse: shared export plus template-database copies.
- How does the template copy stay safe on PostgreSQL and on SQLite in WAL mode? PostgreSQL template copy uses CREATE DATABASE with TEMPLATE from a template with no open sessions, unique per-package names, serialized build, retry once on template-busy, and cleanup of copies. SQLite copy checkpoints the template and copies only the main database file with fresh random keys per copy.
- Does the change alter any exported storetest API used by SDK store authors? No. Changes stay inside pkg/store/storetest and the sqlite and postgres package test files on main. No exported storetest API changes, so third-party store authors see no break.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
