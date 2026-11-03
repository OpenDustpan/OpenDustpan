# Performance check, round 9

Compare `6c35afa` (end of round 8) with `842af92` (main at the time of the check; 85 commits later, linear history; includes `b72ef46`). One host (4 CPUs, 16 GB, Linux 6.18, Go 1.27.3), local PostgreSQL 18.6 on port 5433 (fsync off), built from two worktrees on the same machine. The first main that `git ls-remote` returned was a stale ref (`d50ddaf`, older than `6c35afa`). Those runs were thrown away. All data here is from `842af92`.

## Result
- Two new round trips on a hot path (OpenTofu lock and unlock), each bisected to a commit. Issues filed (see the end).
- One regression above 10% on the server start path: PostgreSQL open of an already migrated schema, +18% (+3 ms), bisected to `f9b77c88`. Issue filed.
- No other regression above 10% survives a re-run (load harness, Go benchmarks, sign-in, name locks).
- Improvements: restore round trips -12.6% (3+3) and -11.4% (30+30), now exact; SQLite fresh migration -82%; SQLite first start -59%; PostgreSQL first start -13%.

## Round trips (PostgreSQL, `TestRoundTrips*`, 8 runs per commit; raw/roundtrips-*)
| Flow | 6c35afa | 842af92 | Change |
|---|---|---|---|
| Pulumi update cycle | 62 | 62 | 0 |
| Pulumi checkpoint save | 13 | 13 | 0 |
| Pulumi event batch of 1000 events | 1010 | 1010 | 0 |
| OpenTofu upload | 41 | 41 | 0 |
| OpenTofu lock | 42 | **45** | **+3 (+7.1%)** |
| OpenTofu unlock | 10 | **11** | **+1 (+10.0%)** |
| Lock, upload and unlock | 97 | **101** | **+4 (+4.1%)** |
| Backup 3+3 | 357 (8/8) | 357 (8/8) | 0 |
| Restore 3+3 | 1195 (7/8), 1194 (1/8) | 1044 (8/8) | -151 (-12.6%) |
| Backup 30+30 | 438 (6/8), 437, 431 | 438 (8/8) | 0 |
| Restore 30+30 | 1330 (6/8), 1331, 1329, 1337 | 1179 (8/8) | -151 (-11.4%) |

The restore counts at `842af92` are exact: 8 runs, the same count each time, and `TestRoundTripsOfBackup` passes 8/8 (the model gives the count; no ceiling). At `6c35afa` the count varied by up to 8 between runs, and the ceiling failed in 1 of 8 runs (restore 30+30: 1337 > 1332). The drop of 151 comes from one baseline migration (the restore stages the schema with fewer migration steps).

### Bisect of the lock round trips (raw/bisect-lock-*, raw/bisect-*.txt)
Manual binary search over the 28 first-parent commits `6c35afa..4b457b8c`, then the two budget commits:
- `3d076f58` "Key the gate rows of a unit on its stable unit ID": lock 42 -> 44, lock, upload and unlock 97 -> 99. Each of the two gate read transactions of a lock now starts with `GetInventoryUnit`. Parent `7f6866b0`: 42.
- `5d832944` (merge of the last release write): lock 44 -> 45, unlock 10 -> 11, lock, upload and unlock 99 -> 101. One `UpdateInventoryLastRelease` statement for each lock save.
The commits `58a13111` and `5d832944` only raise the budgets in `roundtrips_test.go` for these.

## Load harness (`scripts/load2.sh`, `scripts/load3.sh`; 7 pairs per store, order alternated; raw/load-summary.txt)
The harness reports p50 only (no p95 or p99). Mean of p50 over 7 runs, in ms:
| Store | Metric | 6c35afa | 842af92 | Change |
|---|---|---|---|---|
| SQLite | seed | 50216 | 50470 | +0.5% |
| SQLite | total duration | 56354 | 57412 | +1.9% |
| SQLite | OpenTofu workspace locks p50 | 7.1 | 7.6 | +6.4% |
| SQLite | Pulumi update conflicts p50 | 386 | 374 | -3.0% |
| SQLite | Pulumi namespace updates p50 | 444 | 469 | +5.6% |
| SQLite | workspace list p50 | 20.4 | 22.0 | +7.8% |
| SQLite | inventory list | 2.7 | 3.1 | +17% (1 ms steps: 3 vs 3-4) |
| PostgreSQL | seed | 26336 | 28233 | +7.2% |
| PostgreSQL | total duration | 31828 | 33348 | +4.8% |
| PostgreSQL | OpenTofu workspace locks p50 | 8.8 | 9.7 | +9.7% (range 2-25 ms in both) |
| PostgreSQL | Pulumi update conflicts p50 | 109 | 117 | +7.0% |
| PostgreSQL | Pulumi namespace updates p50 | 98.1 | 105.1 | +7.1% |
| PostgreSQL | workspace list p50 | 53.1 | 47.1 | -11.5% |
| PostgreSQL | inventory list | 4.0 | 6.6 | +65% (one 19 ms outlier; median 4 vs 5) |

At n=3 SQLite namespace updates showed +19% and workspace list +25%; at n=7 they are +5.6% and +7.8%, inside the +-20% run noise of this host. The lock round trips add about 3 statements to a lock; the lock p50 is not above noise. No bisect needed for load.

## Go benchmarks (`go test -bench . -benchmem`, 3 runs, raw/bench-*.txt, bench-summary.txt)
B/op and allocs/op are identical (within 1) for all 48 benchmarks in `pkg/stack`, `pkg/store/sqlite`, `pkg/store/postgres`, `pkg/workspace`, `internal/pulumijournal` and `pkg/dustpan`. The first pass showed 14 time deltas above +10% (the new commit ran second). A reversed-order re-run (bench2-*) left 3 positive deltas (CheckpointSaveFull1000, DeploymentShape1M, ScanPointers). A 20-sample interleaved run (bench3-*) shows them at -11.4%, -10.5% and -2.5%: run-order noise, not regressions. The known issue #2207 (SQLite checkpoint save, 18 more allocations) is not changed: allocs/op 270 (Full) and 326 (Delta) at both commits.

## Migration and start time (raw/migrate-*, raw/start-results.txt, scripts/start.sh)
Store open on a fresh schema or database, n=20 per run, 3 runs (p50 of each run):
| Operation | 6c35afa | 842af92 | Change |
|---|---|---|---|
| SQLite fresh open (migrate) | 112-118 ms | 19.6-21.3 ms | -82% |
| SQLite open of a migrated file | 3.8-4.1 ms | 3.0-3.8 ms | -10% |
| PostgreSQL fresh open (migrate) | 346 / 412 / 493 ms | 374 / 388 / 431 ms | within run noise |
| PostgreSQL open of a migrated schema | 16.1 / 16.7 / 17.3 ms | 19.4 / 19.7 / 20.3 ms | **+18% (+3 ms)** |
PostgreSQL fresh open does not get faster although 62 migration files are now 1 file. Observation, no issue: the time sits in something other than the file count.

Bisect of the PostgreSQL reopen (raw/bisect-migrate-*): `5d832944` (parent of `f9b77c88`) 14.6-16.3 ms; `f9b77c88` 19.6-21.8 ms; `9abbcd80` 18.5-22.7 ms; `2f01ccac` 19.4-20.0 ms; `842af92` 21.3-23.8 ms. The step is `f9b77c88` (one baseline per dialect, content check).

Server start, from process start to `GET /health/ready` = 200, 7 runs, median:
| Case | 6c35afa | 842af92 |
|---|---|---|
| SQLite, new database | 167 ms | 68 ms |
| SQLite, restart | 52 ms | 43 ms |
| PostgreSQL, new database | 379 ms | 329 ms |
| PostgreSQL, restart | 95 ms | 97 ms |

## Sign-in with a local password (tests/perf8_argon_test.go, tests/perf9_burst_test.go; raw/signin-*, raw/burst-*)
Argon2id: 19456 KiB, 2 iterations, 1 lane. Budget 81920 KiB, wait 2 s. One verify (n=10, 3 runs, median of the 3 run medians):
| | 6c35afa | 842af92 |
|---|---|---|
| p50 | 31.3 ms | 31.4 ms |
| p95 | 37.4 ms | 38.5 ms |
| allocs per verify | 36 | 32 (-11%) |
| bytes per verify | 19,925,9xx | 19,925,6xx |

Concurrent sign-ins, 30 bursts per size, 3 runs (raw/burst-*), p50 / p95 / p99 of one verify in ms (range over the 3 runs of the p95):
| Concurrent | 6c35afa p50 | 6c35afa p95 | 842af92 p50 | 842af92 p95 |
|---|---|---|---|---|
| 4 | 32-38 | 159-284 | 34-43 | 153-236 |
| 8 | 51-54 | 74-79 | 52-55 | 76-87 |
| 16 | 82-92 | 141-170 | 83-96 | 141-178 |
| 32 | 147-172 | 268-326 | 145-170 | 267-320 |
No busy errors (0 of 5,400 verifies at each commit). The peak budget stays at 77824 KiB, never above 81920 KiB (single-burst runs, raw/signin-*). Peak heap in use at 16 concurrent: 134-153 MiB; peak Sys 169 MiB at 16-64 concurrent (old 189 MiB at 32-64). The memory is about 2x the budget, as in round 8. A single 16-sample burst (raw/signin-*) showed a new p95 of 211-276 ms against 123-159 ms; the 30-burst run does not confirm it (141-178 vs 141-170), so it is burst noise.

## Name-lock table (raw/namelock-*, tests/perf8_namelock_test.go for 6c35afa, tests/perf9_namelock_test.go for 842af92 because the store API changed)
2 runs per commit. 100,000 failed sign-ins (2 counts each, 16 workers): 2501 and 2427 per second at 6c35afa, 2476 and 2371 at 842af92. One pair of adds p50 6.2-6.4 ms at 6c35afa, 6.2-6.6 ms at 842af92 (p95 9.0-9.4, p99 11.0-11.3 at both). Prune with the default limit: 21 runs, per-run p50 72 and 85 ms at 6c35afa, 79 and 85 ms at 842af92. The known issue #2252 (prune limit of 10,000 rows per hour) is not changed.

## Limits
- Load numbers carry about +-20% run noise on this host; the harness reports p50 only.
- The round-trip tests give counts, not latency.
- `raw/migrate-new-*.txt` has a compile error for the PostgreSQL package (the old name-lock test); the PostgreSQL numbers for 842af92 are in `raw/migrate-pg-new-*.txt`.
- PostgreSQL has fsync off, so absolute PostgreSQL latency is lower than on a durable disk. Round-trip counts are not affected.
