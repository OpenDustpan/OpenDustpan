# Performance review, round 16

Range: `a39ce4470d33..4a78446d` (33 commits, 140 files; 42 non-test Go files). Head: `4a78446d11cb5fca33d01ab615cad05e8a50c620`.

## Method

- Local PostgreSQL 18.7 (`fsync=off`, 4 cores, one client machine), and SQLite. Round 15 ran SQLite only.
- Go benchmarks of the 8 packages that have them (`pkg/store/sqlite`, `pkg/store/postgres`, `pkg/dustpan`, `pkg/stack`, `pkg/workspace`, `pkg/ipallow`, `pkg/store/internal/blobrefs`, `internal/pulumijournal`) at the range start (`a39ce4470d33`) and the range end. Each used `-count=5 -benchtime=200ms -benchmem`. Compared with `benchstat`.
- Each non-test Go change in the range was read for round trips under a lock or transaction, N+1, missing indexes, unbounded lists and bodies.
- Three review areas ran in parallel: range commits with EXPLAIN on PostgreSQL; API handlers and backup; lock contention and stores (EXPLAIN of the generic plans of 803 of the 842 queries in `queries.sql`, on 200k synthetic rows per table and 1M audit rows in one namespace).

## Benchmark comparison

The 4 cores were shared with the review work, so wall time has noise of 10 to 40 percent. The two runs were not in the same time window. Use bytes and allocations as the signal. Raw `benchstat` output is below.

Result: no regression with a cause in the range.

- `LeaseCheck` and `IPAllowlistServeNamespaceRead*` allocate 2 to 3 more objects for each request (+0.5 to +1.6 percent; bytes +0.1 to +0.4 percent). Cause: `requestMetrics.startAdapter` in `pkg/dustpan/dustpan.go` calls `root.mux.Handler(r)` for each request (a second route match). A separate microbenchmark with 18 patterns: 1354 ns and 5 allocs without it, 1424 ns and 10 allocs with it, so about 70 ns for each request. Not worth a change.
- `WriteCheck` on PostgreSQL: +3 percent bytes, in the noise of the range.
- Wall-time differences on SQLite (`AuditAppend` +26 percent, `PrincipalLists/members_of_a_group` +48 percent, `CallerGrants/others=100000` +44 percent, `WriteCheck` -21 percent) have flat allocations, and the range does not touch these code paths. They are noise from the shared CPU.

## Findings

New issues:

| Issue | Finding | Measured |
|---|---|---|
| #3682 | `GetJobToken` scans `stacks` and `workspaces` (UNION derived table is not seeked) | 96 ms at 200k + 200k rows; 0.157 ms trimmed |
| #3683 | Audit list: `COUNT(*)` and `OFFSET` for each page | COUNT 66 to 199 ms, OFFSET 900000 203 ms at 1M entries |
| #3684 | `stack_tags` has no index for the tag filter | 14 ms at 200k rows |
| #3685 | mTLS exchange reads every trust rule of the namespace inside the fenced transaction (commit `4ca0bda5`, in range) | 172.7 ms at 100k rules, disk sort 27.8 MB; 0.56 ms at 100 rules |
| #3686 | Context routes call `ListUnits` to find one unit ID | 240 ms at 20k units (SQLite); GetUnit 0.27 ms |
| #3687 | Backup holds all blob references in memory | 165 MiB live, 1.9 GiB allocated, 4.5 s CPU at 1M chunks |

Seen again, not filed as new:

- #1424, per-namespace hot rows. Numbers for the update: `Audit.Append` in one namespace on PostgreSQL reaches 1757 / 1992 / 1924 ops/s at 1 / 8 / 32 goroutines (mean latency 0.57 / 4.0 / 16.7 ms); spread over 32 namespaces it reaches 6.9k to 7.0k ops/s; SQLite is flat at about 4.5k ops/s. `CreateWorkspace` (a plain fenced write, 4 goroutines) falls from 2497 ops/s (1.6 ms) to 691 ops/s (5.8 ms) while 8 goroutines append audit entries in the same namespace (SQLite: 2208 to 562 ops/s). `AppendServer` (advisory lock on `@server`): 1946 / 2218 / 2355 ops/s. An audit append inside the lock uses 6 to 7 sequential round trips (from code, not traced); `GetNamespace` in `auditFenceVersion` re-reads the failover version that `FOR UPDATE` just returned, and the chain-state read and `LastAuditEntry` could be one statement. Estimated gain about 2x, not measured. Proposed order: cut round trips in the lock; lock a per-scope chain-head row instead of `namespaces`; allocate the sequence from the `inventory_change_heads` pattern.
- #1422, durable claim scan. `ClaimDurableOperation` with 50k running operations whose claims are not expired: 46.6 ms for one attempt, 1.1k buffers. Proposed: two partial indexes `(not_before_ms, created_at_ms) WHERE state='pending'` and `(claim_expires_at_ms) WHERE state='running'`.
- #2127 and #1599: not re-measured.

Negligible, not filed: the second route match in `startAdapter` (above).

Checked and found fine:

- Range: `blobrefs.checkRegistryFile` (O(1) for each file); `CheckRegistryFileLimits` (one in-memory pass); `poolproxy.go` guard (uses the already-read `current`); `proxyendpoint.go`, `accesstoken/trust.go` (one policy read, as the JWT path); `localusersvc`, `proxysessionsvc`, `trustrule.CheckExchange`; `registryFilesRefusal`.
- Portable export streams one table at a time in a cursor loop; portable import batches 200 rows in each multi-row INSERT and transaction.
- All 45 `io.ReadAll` call sites under `internal/api` have a size limit.
- Session and token lookups use the unique digest indexes. `ListUpdates`, `ListStateVersions`, `ListStackPageInNamespace` and `ListInventory` use indexes.
- `FencedWrite` benchmark: 300 µs serial (430 µs with the installation check); 87 and 122 µs for each op at 8x parallel.

## Not covered

- Behavior with `fsync=on`. All lock-hold numbers are optimistic.
- Concurrent throughput of stack leases and updates, workspace locks, manual locks, job and runner claims, idempotency (code read for round trips only).
- PostgreSQL numbers for #3686, backup export, import and restore; an end-to-end seeded backup and restore memory run.
- Full N+1 read of `internal/api/jobs`, `runnerapi`, inventory views, collections and audit handlers. Noted, not measured: `readableNamespaces` and `listStacksAll` in `internal/api/pulumi/pulumi.go` (about lines 445 and 656) authorize once for each namespace, and `tfe/workspaces.go` (about line 383) loads all workspaces for a restricted-scope caller.
- `internal/store/*` packages. History and inventory projection write throughput. Exact statement count for each transaction (no `pg_stat_statements`).
- EXPLAIN used synthetic, degenerate data; grants and review-grant plans were ignored.
- The `gh` issue search is limited, so duplicate checks used keyword search. #2933, #2934, #2935, #2926 and #3539 did not come up. Open perf issues #2207, #2937, #2936, #2125, #2225, #1801 were not re-measured.
- The benchmark pairs did not run in one time window on a quiet machine.

## Raw benchstat (base `a39ce4470d33` vs head `4a78446d`; rows with a change)

```
### internal_pulumijournal.txt
JournalBuildSmallestState-4                        66.65Mi ± ∞ ¹   66.65Mi ± ∞ ¹  -0.01% (p=0.032 n=5)
JournalBuildSmallestState-4                         599.9k ± ∞ ¹   599.8k ± ∞ ¹  -0.00% (p=0.016 n=5)
### pkg_dustpan.txt
IPAllowlistServeNamespaceReadOff-4            11.98Ki ± ∞ ¹   12.00Ki ± ∞ ¹  +0.12% (p=0.008 n=5)
LeaseCheck/sqlite/with-check-4                31.14Ki ± ∞ ¹   31.23Ki ± ∞ ¹  +0.28% (p=0.008 n=5)
LeaseCheck/sqlite/without-check-4             27.10Ki ± ∞ ¹   27.21Ki ± ∞ ¹  +0.42% (p=0.008 n=5)
LeaseCheck/postgres/without-check-4           24.80Ki ± ∞ ¹   24.88Ki ± ∞ ¹  +0.33% (p=0.008 n=5)
IPAllowlistServeNamespaceRead-4                 158.0 ± ∞ ¹    160.0 ± ∞ ¹  +1.27% (p=0.008 n=5)
IPAllowlistServeNamespaceReadOff-4              125.0 ± ∞ ¹    127.0 ± ∞ ¹  +1.60% (p=0.008 n=5)
LeaseCheck/sqlite/with-check-4                  623.0 ± ∞ ¹    626.0 ± ∞ ¹  +0.48% (p=0.008 n=5)
LeaseCheck/sqlite/without-check-4               502.0 ± ∞ ¹    505.0 ± ∞ ¹  +0.60% (p=0.008 n=5)
LeaseCheck/postgres/with-check-4                329.0 ± ∞ ¹    332.0 ± ∞ ¹  +0.91% (p=0.008 n=5)
LeaseCheck/postgres/without-check-4             272.0 ± ∞ ¹    275.0 ± ∞ ¹  +1.10% (p=0.008 n=5)
### pkg_ipallow.txt
### pkg_stack.txt
GzipBestSpeedDecompress-4             88.14µ ± ∞ ¹   101.61µ ± ∞ ¹  +15.28% (p=0.008 n=5)
GzipBestSpeedDecompress-4            34.00Mi ± ∞ ¹   29.49Mi ± ∞ ¹  -13.27% (p=0.008 n=5)
### pkg_store_internal_blobrefs.txt
CollectorChunks-4                            3.397M ± ∞ ¹      3.397M ± ∞ ¹  -0.00% (p=0.032 n=5)
### pkg_store_postgres.txt
PrincipalLists/services_with_a_prefix-4                                   2.750m ± ∞ ¹    2.416m ± ∞ ¹  -12.17% (p=0.032 n=5)
PrincipalLists/members_of_a_group-4                                       698.0µ ± ∞ ¹    576.7µ ± ∞ ¹  -17.38% (p=0.008 n=5)
PrincipalLists/groups_of_a_principal_in_80_groups-4                       1.824m ± ∞ ¹    1.336m ± ∞ ¹  -26.74% (p=0.008 n=5)
ProtectedGroups/one_group_at_a_time-4                                     157.2m ± ∞ ¹    184.6m ± ∞ ¹  +17.44% (p=0.008 n=5)
WriteCheck/state_and_minimum,_none_set-4                                 2.626Ki ± ∞ ¹   2.710Ki ± ∞ ¹  +3.20% (p=0.040 n=5)
WriteCheck/state_and_minimum,_one_set-4                                  2.751Ki ± ∞ ¹   2.835Ki ± ∞ ¹  +3.05% (p=0.016 n=5)
### pkg_store_sqlite.txt
WriteCheck/state_only-4                                                 26.34µ ± ∞ ¹    20.73µ ± ∞ ¹  -21.29% (p=0.016 n=5)
WriteCheck/state_and_minimum,_none_set-4                                48.12µ ± ∞ ¹    36.81µ ± ∞ ¹  -23.50% (p=0.032 n=5)
CountManagedResources3000-4                                             25.75m ± ∞ ¹    28.29m ± ∞ ¹   +9.87% (p=0.008 n=5)
RunnerClaim-4                                                           940.5µ ± ∞ ¹   1063.7µ ± ∞ ¹  +13.11% (p=0.032 n=5)
AuditAppend-4                                                           235.7µ ± ∞ ¹    296.6µ ± ∞ ¹  +25.85% (p=0.032 n=5)
PrincipalLists/members_of_a_group-4                                     282.7µ ± ∞ ¹    417.0µ ± ∞ ¹  +47.52% (p=0.016 n=5)
CallerGrants/others=100000-4                                            3.808m ± ∞ ¹    5.496m ± ∞ ¹  +44.34% (p=0.008 n=5)
Authorizer/list_objects-4                                               3.281m ± ∞ ¹    2.916m ± ∞ ¹  -11.10% (p=0.032 n=5)
CheckpointSaveFull1000-4                                               14.10Ki ± ∞ ¹   14.10Ki ± ∞ ¹  +0.03% (p=0.024 n=5)
RunnerClaim-4                                                          39.35Ki ± ∞ ¹   39.59Ki ± ∞ ¹  +0.60% (p=0.016 n=5)
PrincipalLists/principals_with_a_prefix-4                              95.99Ki ± ∞ ¹   95.97Ki ± ∞ ¹  -0.02% (p=0.040 n=5)
PrincipalLists/members_of_a_group-4                                    12.86Ki ± ∞ ¹   12.85Ki ± ∞ ¹  -0.03% (p=0.040 n=5)
CallerGrants/others=20000-4                                            364.9Ki ± ∞ ¹   365.0Ki ± ∞ ¹  +0.05% (p=0.008 n=5)
Authorizer/list_objects-4                                              463.3Ki ± ∞ ¹   463.4Ki ± ∞ ¹  +0.01% (p=0.008 n=5)
RunnerClaim-4                                                            752.0 ± ∞ ¹    755.0 ± ∞ ¹  +0.40% (p=0.008 n=5)
CountManagedResources3000-4                367.9Mi ± ∞ ¹   334.9Mi ± ∞ ¹  -8.98% (p=0.008 n=5)
### pkg_workspace.txt
```
