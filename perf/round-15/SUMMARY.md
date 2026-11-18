# Round 15: performance review

Range: ce2265d36..a39ce4470d33 (493 commits).

## Findings

- #3539: workspace state version finalize with a job origin adds three reads (clock, state version, job row with decode) inside the workspace row lock. Found by reading the code. Lock hold time not measured.

## Measurements (SQLite, 4 cores, base against head)

| Benchmark | Base | Head |
|---|---|---|
| CheckpointSaveFull1000 | about 210 us | about 245 us |
| CheckpointSaveDelta1000 | about 390 us | about 430 us |
| RunnerClaim (200x) | about 0.82 ms | about 0.71 ms |
| AuditAppend (200x) | about 0.19 ms | about 0.22 ms |
| RecorderPutPlan (200x) | about 4.3 ms | about 4.6 ms |

Run variance is about 15%. Allocations rose by 5 to 7% on RecorderPutPlan, RunnerClaim and AuditAppend. None of this is a proven regression.

## Checked, no finding

- New unbounded `:many` queries in `pkg/store/postgres/queries.sql` (ListSupersededBlobs, GrantsOfSubjects, ListTrustBundleVersions, ListUnmanagedTargets, ListQuotaReservationKeys, ListActiveGroupNamesOfPrincipal, ListGateObjectsOfKind, NamespacesWithList): the first five are bounded per stack, subject, bundle, namespace or reservation, and GrantsOfSubjects uses an index seek. The last three I did not read.
- One extra `PublishUnitState` upsert per checkpoint write (one row for each unit, primary key): costs about 10 to 15% on SQLite.

## Not covered

- PostgreSQL benchmarks (no local PostgreSQL started).
- internal/api/server, runnerapi, runnerpools, jobs, trustrules, decisionrecord, registry/service, notifications, inventory: no read of the handlers for N+1 or size limits.
- Backup, restore and blobguard; unmanaged_scans, quota, breakglass, jobtokens stores.
- Lock contention under concurrency (the unit_state row is a per-unit write hotspot: not tested).
- openspec, docs, clients, e2e, CI: out of scope.
