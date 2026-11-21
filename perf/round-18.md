# Performance check, review round 18

Range: `4daa46bceabe..909ee0bd4` on main (80 commits). Local checks only.

## Result

One finding: issue #3911.

## Finding 1: masked state read repeats the full decode for each page (#3911)

`pkg/stack/masked_read.go:144` and `pkg/workspace/masked_read.go:315` load, decode,
scan, walk and sort the whole version on each request. A page holds 200 markers.
The cursor holds only an offset.

Benchmark of `maskedValuePaths` (temporary internal test, removed after the run;
Xeon 2.1 GHz, 4 cores). The feature is new in the range, so there is no baseline.

| Version size | Markers | Pages | Time per page | Alloc per page | All pages |
|---|---|---|---|---|---|
| 295 KB | 1000 | 5 | 6.7 ms | 2.5 MB | 34 ms |
| 1.5 MB | 8000 | 40 | 31.8 ms | 12.5 MB | 1.3 s |
| 3.1 MB | 16000 | 80 | 66.4 ms | 25.4 MB | 5.3 s |

## Checked, no finding

- `in_collection` EXISTS in the workspace read queries
  (`internal/store/*/queries.sql`): `collection_members` has `unit_id` as primary
  key. SQLite plan: `SEARCH m USING INDEX sqlite_autoindex_collection_members_1 (unit_id=?)`.
  The PostgreSQL plan was not run; the primary key gives the same lookup.
- `GetWorkspaceStateVersion`: primary key lookup on `state_versions` joined to
  `workspaces` by primary key.
- Event receipt bound: a one-row counter with a row lock replaces `LOCK TABLE` and
  `COUNT(*)`. Writers still take turns, but the lock ends with the transaction and
  the full count is gone. Not measured under load.
- `pkg/job/match.go` matching: bounded by `MaxApprovalGates`.
- `go test -bench` at the base commit was not run: the changed hot path does not
  exist there.

## Not done

- No local PostgreSQL 18 EXPLAIN (not started).
- The store move (`pkg/store` to `internal/store`) was not benchmarked; the diff
  of both `queries.sql` files shows no other query change.
