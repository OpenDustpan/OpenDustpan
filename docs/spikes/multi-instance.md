# Spike: single-instance assumptions in the server

Status: spike, 2026-10-19. Documents only, no code. The product manager
asked for it. It audits `main` at commit `8a31541` for each assumption
that breaks when 2 or more instances of `opendustpan serve` run on one
PostgreSQL database and one object store. The proposals have the label
"(product manager)". [RFC 87](../rfc/0087-components-of-the-server-process.md)
proposes the split of the process into components and says which
component owns each item below.

## Why

The owner decided on 2026-10-18 that the instances of an installation
scale horizontally (ADRs 215 to 221, RFC 79). The Helm chart caps
`replicas` at 1 until the server work of ADR 72 is complete
(ADR 246 decision 12). This spike lists that work, and work that ADR 72
does not name.

## Method

- Three searches read the non-test Go code in `cmd/`, `pkg/` and
  `internal/`: background work, state in process memory, and the start
  sequence with files and storage.
- The author checked each blocking item against the code and the SQL in
  `pkg/store/postgres/queries.sql`.
- No test ran with 2 instances. Each verdict comes from reading the code.
  A smoke test with 2 instances must check the verdicts before the cap
  goes.

## What ADR 72 already covers

ADR 72 ("scale out on one database") decided most of the fix patterns on
2026-10-16. The code on `main` has part of it:

| ADR 72 decision | State on `main` |
| --- | --- |
| 1. A port for durable operations | Done: `pkg/durable`, with `durabletest`. The audit chain migration uses it. Backup and restore do not use it yet. |
| 2. A lease per operation with a fencing token | Done in the port: `ClaimDurableOperation` uses `FOR UPDATE OF o SKIP LOCKED` and increments `claim_token`. |
| 3. A schedule is a row | Done for 3 jobs: `inactivity-disable`, `deleted-sweep`, `signin-prune`. The audit chain check is still a ticker (M7). |
| 4. A write-path installation check | Not done (M5). |
| 5. Control tables outside the restore swap | Done on PostgreSQL: `finishSwap` keeps the `_control` schema. |
| 6. Pre-release compatibility code goes | Not audited here. |
| 7. One baseline migration | Done: `0100_baseline.sql`. |
| 8. A shared sign-in failure budget | Done: `authgate.Gate` uses a `FailureCounter` in the database (M21). |
| 9. An in-process cache first | Done: `pkg/blob/cache`, write-once keys only. |
| 10. Upgrades can skip releases | Not audited here. |
| 11. Rolling deploys, K = 2 | In part: `schema_migrations.min_binary_version` and the check at start exist. The version rows and the write-path check of the minimum do not (M6). |

## Audit

Severity:

- **Blocking**: it loses data, fails work of a live instance, gives a
  false alarm, or lets a write through maintenance mode. It must be fixed
  before the chart lifts the cap.
- **Medium**: wrong output or drift, with a workaround or with the chart
  values that ADR 246 sets. Fix it before a release that supports several
  instances outside the chart.
- **Low**: duplicate work or a small window, with no wrong data.
- **Safe**: no wrong result with 2 or more instances today.

Line numbers are on `8a31541`.

| ID | Item: file and symbol | Today | With 2+ instances on one PostgreSQL | Fix pattern | ADR 72 | Severity |
| --- | --- | --- | --- | --- | --- | --- |
| M1 | Start step that fails backups: `pkg/dustpan/dustpan.go:1813` `startUp` calls `FailRunningBackups`, SQL `UPDATE backups SET state = 'failed' … WHERE state = 'running'` | Each start fails every running backup, because it assumes that a running row belongs to its own earlier process. | A start, a restart or a rolling update of one instance fails the live backup of another. `FinishBackup … AND state = 'running'` then changes 0 rows, so a complete backup shows as failed. | Backup becomes a durable operation (M3). Delete the start step. A takeover fails or resumes only an operation whose lease expired. | Decisions 1, 2, Consequences | Blocking |
| M2 | Start step that fails restores: `dustpan.go:1745` `failStoppedRestores`, then `fenceStoppedRestore` and `BumpFailoverVersions` (`UPDATE namespaces SET failover_version = failover_version + 1`, no `WHERE`) | Each start that finds a restore in `checking` or `applying` fences every namespace and fails the restore. | A start during a live restore on another instance increments the failover version of every namespace. Each live Pulumi update lease and OpenTofu lock on each instance stops working. The restore goroutine continues under a failed row, and `runningElsewhere` then lets a second restore start. | Restore becomes a durable operation (M4). Fence and fail only on takeover of an expired lease. Delete the start step. | Decisions 1, 2, Consequences | Blocking |
| M3 | Backup work: `pkg/backup/service.go:380` `claim` (`mu`, `running bool`), `:215` `go s.finish(…)` | One slot in process memory. The work runs in a plain goroutine. A second running row fails on the partial unique index `backups_one_running`. | The index stops 2 concurrent backups, but the work has no lease and no owner. A crashed instance leaves the row `running` until a start runs M1, which then also hits live work. | A durable operation kind `backup`, with steps. Delete `claim`, `running` and M1. | Decision 1, Consequences | Blocking |
| M4 | Restore work: `pkg/backup/restore_service.go:309` `claim`, `:117` `inflight` map, `:367` `runningElsewhere`, `:386` `yieldToEarlierRunner`, `:183` `go s.finish`, `pkg/store/postgres/postgres.go:52` `stagingMu`, `stagingSchema` | The in-process `inflight` map answers the status during the swap. `yieldToEarlierRunner` breaks a tie with the clock of the application, not of the database. The staging schema name is in process memory. | A status poll that goes to another instance gets 404 during the swap. Two clocks with skew can make both instances yield, or neither. After a crash, no other instance can find or delete the staging schema. | A durable operation kind `restore`. Store the status and the staging schema name in the operation row, in the control schema. Delete `inflight`, `yieldToEarlierRunner` and `stagingMu`. | Decisions 1, 2, 5, Consequences | Blocking |
| M5 | Maintenance gate: `pkg/dustpan/maintenance.go` `withMaintenance`, `pkg/maintenance/maintenance.go:53` `DefaultCacheLimit = 2 * time.Second`, `postgres.go:783` `inFencedTx` reads only the failover version | The HTTP middleware of the tool adapters reads the installation state from a cache of 2 s. No write transaction reads the installation state. No `pg_advisory_xact_lock_shared` exists. Writes at installation scope have no gate. | After one instance enters maintenance mode, the other instances accept tool writes for up to 2 s, after the restore checked for active work. Token, session, principal and grant writes are never stopped. The swap then deletes a write from that window. The watcher keeps its last state on a read error, so it fails open. | Decision 4: every write transaction (`inTx`, `inFencedTx`) takes the shared advisory transaction lock and reads the state in the statement that reads the fence. Enter takes the exclusive form. | Decision 4 | Blocking |
| M6 | Version rows and the write-path minimum (ADR 72 decision 11) | Only `schema_migrations.min_binary_version` and the check at start exist. No table of version rows. The write path does not compare the minimum. | A contract migration cannot see an old binary that still runs. An old instance can write after the minimum goes up. This blocks the `RollingUpdate` strategy that ADR 246 decision 13 pairs with the lift of the cap. | One row for each instance at start, deleted at a clean stop, with an installation API route to list and delete rows. Add the minimum to the statement of M5. | Decision 11 | Blocking |
| M7 | Audit chain check and checkpoint signature: `pkg/dustpan/auditchain.go:1160` `startCheck` (`time.NewTicker`), `:1253` `checkpoints.Write`, `pkg/audit/checkpoint.go:409` `good`, `bad`, `sticky` | Each instance checks every scope each hour and signs a checkpoint. The good and bad results stay in process memory. | Duplicate work. `Write` reads the last entry (sequence N), then lists stored checkpoints. When another instance stored a checkpoint above N in between, `checkHead` marks the chain bad: a false `ErrCheckpointMismatch`. It stays for the epoch in that process. | A durable schedule `audit-chain-check`. The checkpoint write reads the last entry and the highest checkpoint under one advisory lock (`LockServerAuditScope`). Or it compares only checkpoints at or below N. | Consequences ("the audit chain check becomes a schedule") | Blocking |
| M8 | OpenTofu state version restore: `pkg/workspace/service.go:1397` `lockKey("restore/"…)` with `stripes [lockStripes]sync.Mutex` | The stripe in process memory is the only guard from the retry check to the finalize. The code comment says that without it, 2 concurrent restores of one version both write a restored version. | Two requests to 2 instances both pass the check and write 2 restored state versions of one workspace. | `pg_advisory_xact_lock(hashtextextended('restore/' \|\| id, 0))` in the store transaction, as `LockGateRefusal` does, or `SELECT … FOR UPDATE` on the workspace row. Keep the stripe as a local optimization or delete it. | Not covered | Blocking |
| M9 | Key-encryption key generation: `cmd/opendustpan/main.go:668` `loadOrMakeKey`, `:573` `refuseNewKeyFile`, `:890` `O_CREATE\|O_EXCL` | `--generate-key-encryption-key` refuses only when wrapped keys exist. The file guard works on one disk. | On an empty database, 2 instances with separate disks each make a different key. Each wraps data keys with its own key. At the next start `checkWrapperIDs` refuses every instance. The chart is not affected: it passes `DUSTPAN_KEY_ENCRYPTION_KEY` from a secret. | Store the wrapper ID of the key in the `installation` row with a conditional insert at the first start. An instance with another key refuses to start, also on an empty database. | Not covered | Medium |
| M10 | Audit chain start step: `auditchain.go:535` `startUpAuditChain`, `SaveChainRecord` (unconditional save), `:734` `enterMaintenance` | Read, compare with the `--audit-chain` flag of the process, then save. | Two instances with different flags race on the chain record. One instance can put the installation into maintenance mode. | A compare-and-swap on the chain record (a version column). Later, keep the chain setting as installation state, not as a flag of each process. | Not covered | Medium |
| M11 | Local storage with PostgreSQL: `main.go:1209` `openBlobs` (default `fs`, `blobs` in the working directory), `:1405` `openBackupTarget` (`dir:`) | `serve` accepts `fs` and `dir:` with PostgreSQL. | Each instance writes blobs and backups to its own disk. Reads on another instance do not find them. Readiness still answers ok. | The chart rule of ADR 246 decision 12 (more than 1 instance needs `s3` and a target that is not `dir`). In `serve`: write an installation ID marker blob with a conditional put, and refuse to start when the marker names another installation. | ADR 246 decision 12 | Medium |
| M12 | Audit signing key files: `cmd/opendustpan/auditcheckpoints.go` `readSigningKeyFile`, `addVerifyKeys` | Each instance reads its own files. | Instances with different keys sign checkpoints that the other instances cannot check. | Store the public key ID in the database at the first signature. Refuse a different key at start. | Not covered | Medium |
| M13 | Audit sink lag and last error: `pkg/audit/ship.go:344` (`if !held { continue }`), `:159` `lastErr`, `pkg/dustpan/auditsinks.go:83` | Only the lease holder updates the lag gauge. Other instances export 0. A former holder exports its last value. `LastError` is in process memory. | `dustpan_audit_sink_lag_seconds` is wrong on most instances. The status API answer depends on which instance serves it. | Compute the lag from the shared cursor on each instance. Store the last error on the lease row. | Not covered | Medium |
| M14 | Settings of each process: `cmd/opendustpan/reload.go:137` (reload on SIGHUP and a poll), flags such as `--soft-delete-retention`, `--ip-allowlists*`, `--disable-pulumi-journaling`, `--max-*` | Each instance reads its own flags and file. | Instances can drift. The schedule claim applies the retention of the instance that claims the tick. A load balancer gives different answers for one request. Each reload writes one audit entry for each instance. | Store a hash of the installation-wide settings in the version row (M6). Warn on drift at start and in the installation API. Move settings that must agree into the database over time. | Not covered | Medium |
| M15 | Audit file sinks: `pkg/dustpan/auditsinks.go:93` `startAuditShipper`, lease key `audit-sink/<sink>` | One lease for each sink name, for the whole installation. | A file sink writes on the disk of the lease holder. When the lease moves, the file continues on another host. | Refuse a file sink with more than 1 instance. Or key the lease of a file sink by instance, and document one file for each instance. | Not covered | Medium |
| M16 | Readiness: `pkg/dustpan/health.go` `newReadinessHandler`, routes at `dustpan.go:3036` | `/health/ready` pings the store and the blob store. | An instance whose audit chain gate refuses every request reports ready. The load balancer still sends traffic to it. | Add the chain gate to readiness. RFC 87 proposes readiness for each component. | Not covered | Medium |
| M17 | Inventory retention sweep: `pkg/inventory/sweep.go:150` `s.mu.TryLock()` | One sweep at a time in each process. The API route `internal/api/server/deleted.go:97` also starts it. | 2 sweeps can run at the same time. The purges are conditional, so the effect is duplicate work and counts. `ErrSweepRunning` is local. | An advisory transaction lock for each purge batch, or accept duplicate work. | Not covered | Low |
| M18 | Schedule interval: `EnsureSchedule` / `SetDurableScheduleInterval` (`queries.sql:2333`) | `next_run_at_ms = LEAST(…)`, last writer sets the interval. | Instances with different intervals overwrite each other. | Same fix as M14. | Decision 3 in part | Low |
| M19 | Pulumi blob orphan sweep: `pkg/stack/updates.go:1210` `sweepStackOnStart`, `:1140` `sweepUpdateBlobs` | The grace period compares the local clock with the blob time. | Clock skew between instances shortens the grace period. Deletes are idempotent. | Use the database clock or the clock of the object store for the grace period. | Not covered | Low |
| M20 | Ticker fallback: `pkg/dustpan/schedules.go:214` `startSweeps` when `s.durable == nil` | `serve` sets the durable store for both dialects, so `serve` never takes this path. | A program that embeds the SDK without `WithDurableStore` runs each job on each instance. | Refuse more than 1 instance without a durable store, or document the rule in the SDK. | Decision 3 | Low |
| M21 | Sign-in throttle: `internal/authgate/authgate.go:174` | Token buckets and a waiting cap in process memory. The failure budget is shared in the database. | About 1 extra guess for each instance in each window. The waiting cap becomes 4 for each instance. | Accept. | Decision 8 (done) | Low |
| S1 | Migrations: `pkg/store/postgres/postgres.go:829` `migrateTx` | `pg_advisory_xact_lock(migrationLockID)`. The minimum check runs in the same transaction. | One instance migrates, the others wait. | — | Decision 10 | Safe |
| S2 | Durable schedules and operations: `pkg/dustpan/schedules.go:171`, `:228`, `pkg/store/postgres/durable_schedules.go:122` `claimTick` | A conditional `UPDATE` on the database clock. The start key is `TickKey(name, dueAt)`. A lease with a fencing token. | One run for each tick. The step bodies are idempotent. | — | Decisions 1 to 3 | Safe |
| S3 | Audit chain migration runner: `auditchain.go:227` `newChainRunner` | A durable operation kind. | One runner claims it. | — | Decision 2 | Safe |
| S4 | Audit shipper: `pkg/audit/ship.go:315`, `pkg/store/postgres/audit_sink_lease.go` | A lease for each sink with a fencing token on each cursor write. | One shipper for each sink. Delivery is at least once by design. | — | Decision 2 | Safe |
| S5 | Sign-in provider binding: `pkg/dustpan/signin.go:246` `bindSignInProviders` | `INSERT … ON CONFLICT DO NOTHING`, then compare. | First writer wins. An instance with another issuer refuses to start. | — | Not needed | Safe |
| S6 | Sessions, sign-in requests and data keys | Rows in the database. `CreateInstallationSecretKey` inserts, then reads again. | Any instance serves any request. | — | Not needed | Safe |
| S7 | Blob cache: `pkg/blob/cache` | Write-once key families only. | No stale read, because no key changes. | — | Decision 9 | Safe |
| S8 | Temporary admin: `cmd/opendustpan/host.go:279` `host create-temp-admin`, `tempadmin.Sweep` | An offline command writes the database in one transaction. The sweep is in a durable schedule. | No file on an instance. The sweep accepts "ended first". | — | Not needed | Safe |
| S9 | Metrics: `internal/metrics` | A registry for each process. | No wrong value when the scraper reads each pod. Sum the counters. M13 is the exception. | — | Not needed | Safe |
| S10 | Leader election, registry, calls between instances | None exist. | Nothing needs a leader. | — | Decision 2 | Safe |
| S11 | Event fan-out | No SSE, websocket or pub/sub in process. The runner protocol has no server handler yet. | Nothing to fan out today. A future long poll must read the database. | — | Not needed | Safe |
| S12 | SQLite: `pkg/store/sqlite/processguard.go` | `flock` on `<db>-lock` refuses a second process. | SQLite stays 1 instance by design. | — | Consequences | Safe |
| S13 | Library ports that `serve` does not start yet: `pkg/job/executor.go:45`, `pkg/runner/runner.go:137`, `pkg/agent/reaper.go:32` | Durable kinds and schedule rows. | No wrong result when `serve` starts them. | — | Decisions 1 to 3 | Safe |

Count: 8 blocking (M1 to M8), 8 medium (M9 to M16), 5 low (M17 to M21),
13 safe (S1 to S13).

## Proposed slice order (product manager)

Smallest first. Each slice is one pull request with its own tests. Slices
1 to 7 lift the cap. Slice 8 and the medium items can come after. The
split into components (RFC 87) is not needed to lift the cap: with the
default `all`, each instance runs every component, and the fixes below
make that safe.

| Slice | Items | Size | What it gives |
| --- | --- | --- | --- |
| 1 | M8 | 1 query, 1 store method for each dialect, a conformance test with 2 concurrent restores | No duplicate OpenTofu state versions from a restore. |
| 2 | M7 | Move `startCheck` to a durable schedule. Read under `LockServerAuditScope` in `checkpoints.Write` | No duplicate check and no false checkpoint mismatch. |
| 3 | M3, M1 | Backup as a durable operation kind. Delete `claim`, `running` and the start step | A start no longer fails a live backup. A crash hands the backup to another instance. |
| 4 | M4, M2 | Restore as a durable operation kind. Status and staging schema in the control schema. Delete `inflight`, `yieldToEarlierRunner` and the start step | A start no longer fences every namespace. Any instance answers the status. |
| 5 | M5 | The shared and exclusive advisory lock in each write transaction, also at installation scope. A benchmark in the PostgreSQL adapter | Maintenance mode stops every write on every instance at once. |
| 6 | M6 | Version rows, the installation API route, the minimum in the statement of slice 5, the test matrix with 2 old binaries | Rolling updates are safe. |
| 7 | Chart | Lift the cap. Restore the `s3` rule. `RollingUpdate` for `s3`. `PodDisruptionBudget`. A smoke test with 2 instances (ADR 246 decisions 12 to 14) | `replicas` above 1 in the chart. |
| 8 | M9 to M16 | One small pull request each | Guards against drift and wrong output. |

Slices 1 and 2 are small and independent. Slices 3 and 4 share the
durable kind pattern. Slice 5 must come before slice 6, because slice 6
adds to the statement of slice 5.

## Owner questions

1. Is the severity line right? This spike makes M6 blocking because
   ADR 246 decision 13 pairs the lift of the cap with `RollingUpdate`. A
   first release with several instances and `Recreate` could ship without
   M6.
2. M11, M12, M14: should `serve` refuse a mismatch at start, or only warn?
   This spike proposes a refusal for M11 and M12, and a warning for M14.
3. M15: refuse file sinks with several instances, or one file for each
   instance?
</content>
</invoke>
