# Performance check, round 14

Compare `e942f91b8` (old, end of round 13) with `ce2265d3` (main, new). `git rev-list` counts 50 commits in the range; commit `a9c994aa` (titled as a profile change) holds many unrelated changes. One host (4 CPUs, Linux 6.18), local PostgreSQL 18 on port 5433 (fsync off), two worktrees. Method as in `perf/round-13`; `tests/cmp.py` compares medians (benchstat is not installed). Go benchmarks: `-count=6` in pkg/dustpan, pkg/stack, pkg/workspace, pkg/store/sqlite, pkg/store/postgres, internal/pulumijournal, pkg/ipallow, pkg/store/internal/blobrefs; old and new run one after the other per package.

## Issues filed
- #2933 OpenTofu lock +5 and upload +4 round trips (collection membership read, `Placer.Require`; `a9c994aa`).
- #2934 group list computes the protected flag with a reach walk per group (N+1; `97f48b65`; code review, not measured).
- #2935 delivery history keyset page filters the whole subscription on PostgreSQL (`58a8a346`; `EXPLAIN` on a stand-in table: 290,001 rows removed by filter, 68 ms against 0.07 ms for page one).
- #2936 `BenchmarkRunnerClaim` fails on SQLite (1000 open jobs limit).
- #2937 `BenchmarkCallerGrants` allocs +4.9%, B/op +10.4% (member state columns).

## Round trips (PostgreSQL, TestRoundTrips*, exact; same result in two runs)
| Flow | e942f91b8 | ce2265d3 |
|---|---|---|
| OpenTofu lock / unlock / upload | 31 / 11 / 41 | **36** / 11 / **45** |
| Lock, upload, unlock | 87 | **96** |
| Pulumi update cycle / checkpoint save / 1000 events | 62 / 13 / 1010 | same |
| Pulumi stack rm / import / restore / rename / cancel | 38 / 65 / 66 / 35 / 31 | same |
| Backup 3+3 / 30+30 | 367 / 448 | 371 / 452 (+4) |
| Restore 3+3 / 30+30 | 1083 / 1218 | 1091 / 1226 (+8) |
| Restore 3+3 with one failed swap | 1132 | 1140 |

Backup and restore: fixed growth (new tables `group_mappings`, `used_tickets`), below 1%, not per row. No issue (same rule as rounds 12 and 13). The new test sets a placement rule before the OpenTofu flows, so those rows come from the new test file; I could not run the old test file on the new code (harness did not build), see raw/roundtrips-newcode-oldtest.txt.

## Latency (SQLite manual lock; alternating runs)
Service lock p50 (ms), six runs each: old 0.331 0.356 0.371 0.399 0.420 0.425; new 0.400 0.342 0.355 0.438 0.432 0.426. Ranges overlap; the host drifts up over the run. HTTP PUT/DELETE p50 over three runs: old 0.82-0.88 / 0.76-0.83, new 0.78-0.83 / 0.73-0.77. allocs per pair equal (895 service, 1218 HTTP). No regression. Sign-in and passkey tests were not rerun: the range changed no code on those paths other than the ticket store (`used_tickets` insert on CAS tickets).

## Go benchmarks
See `bench-table.md`. allocs/op equal (within 0.01%) on every row except `CallerGrants` (#2937). Time changes of 10-19% on rows whose code did not change (IPAllowlist, DeploymentShape, ScanPointers) are host noise; rows moved in both directions (PrincipalLists -12% to -41%, WriteCheck -11% to -30%, with equal allocs). The old run came first for each package; do not read the time column as a gain.

## Static review of the changed queries
- New tables: `group_mappings` (key prefix serves the queries), `used_tickets` (PK and `until_ms` index serve the queries). `group_members_principal` serves `ListCurrentMembershipsOfPrincipal`.
- `ListRegistryAddresses` now has a keyset page. Its `COLLATE "C"` comparison and GROUP BY read the releases of a kind per page; not a hot path; not filed.
- `GetLatestDurableHeartbeat` orders by an expression with no index (also before the range); the table is pruned.
- Delivery history count query is bounded (`LIMIT max_rows`): 21 buffers, 0.5 ms.

## Not measured
- Runner claim (#2936). `BenchmarkAuditAppend` and `BenchmarkRecorderPutPlan` ran for the first time (new only, no comparison).
- Load harness; request time of the group list (#2934).
- The first PostgreSQL URL (unix socket form) failed in the round-trip tests; they ran over TCP afterward. The store benchmarks ran in the first pass.

## Limits
fsync off; server and client in one process; allocation counters process-wide; 6 runs per commit for Go benchmarks, 3 to 6 for latency.
