---
version: 1
---

Issue: #1030. Also: #3158.

Short form: tech debt (the second half of add-sql-adapter-quota-counter). Owner lane: M6.

Waits for: #2847

## Problem

**Persona: embedder.** An embedder that runs the server on PostgreSQL needs the
quota counter there too. The change add-sql-adapter-quota-counter, which #2847
implements, adds the tables of both dialects, the shared suite and the SQLite
counter only. Its PostgreSQL tasks (the counter, its concurrency test, the
check of the reservation keys, the tests of the changed-row checks and the
restore test) need a change of their own, so that no task stays open in a
merged change.

## Evidence

- #2847 says: "This change adds the SQL tables of the quota counter and the
  SQLite counter. The PostgreSQL counter is the next change." Its files hold
  no `pkg/store/postgres/quota.go`.
- The suite `quotatest.Run` (`internal/quota/quotatest/quotatest.go`) and the
  check of the reservation keys (#3158) come with #2847.

## Fix boundary

- `pkg/store/postgres/quota.go` (NEW): `quota.Counter` and `quota.Tx` on
  PostgreSQL, with the same behavior as the SQLite counter of #2847, and the
  same refusal of a reservation key that no check covers.
- The PostgreSQL tests: the suite, the concurrency test, the two changed-row
  tests and the restore test.
- Out of scope: the tables, the queries and the backup lists, which #2847 adds
  for both dialects.

## Check

`go test -timeout 60m ./pkg/store/postgres/ -run Quota -count=1` passes on
PostgreSQL, and `make check` reports zero findings (Confirmed by: CI).

## ADR check

No ADR is changed. The change follows the design of
add-sql-adapter-quota-counter, which holds the ADR check. Conflict: none.

## Open questions

none. Considered: keep the PostgreSQL tasks in add-sql-adapter-quota-counter
(refused: #2847 merges that change without them, and a merged change must
have no open task).

Depends on:

- add-sql-adapter-quota-counter
