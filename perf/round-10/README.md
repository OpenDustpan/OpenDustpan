# Performance check, round 10

Compare `842af92` (end of round 9) with `519b3bd` (main at the time of the check; 65 commits later, linear history). The first clone was shallow; it was deepened with `git fetch --unshallow` before the bisect. One host (4 CPUs, 16 GB, Linux 6.18, Go 1.27.3), local PostgreSQL 18.6 on port 5433 (fsync off), two worktrees on the same machine, order of runs alternated.

## Result
- One regression above 10% with a bisect: Pulumi `stack rm` costs 51 round trips instead of 20 (p50 2.7 -> 6.4 ms). Bisected to `76ad8cb6` (the state.write gate on Pulumi import, restore, rename, rm and cancel). Issue #2412.
- The Pulumi checkpoint write is not gated at `519b3bd` (`PathPulumiUpdate` is on the unchecked list), so the state.write gate costs 0 on it. Checkpoint save: 13 round trips and p50 3.65 ms at both commits.
- No change in any round-trip count of `TestRoundTrips*` (8 runs per commit, exact).
- The known #2347 (OpenTofu lock 45, unlock 11) is unchanged. #2348 (PostgreSQL open of a migrated schema) is unchanged: 17.2-18.2 ms against 17.5-18.8 ms.
- The faultlog 500 line costs about +0.3 us over the old line and does not show in the 500 path.
- Not bisected: lock p99 +18% and unlock p99 +11% in the single-request test (see below).

## Round trips (PostgreSQL, `TestRoundTrips*`, 8 runs per commit; raw/roundtrips-*)
| Flow | 842af92 | 519b3bd | Change |
|---|---|---|---|
| Pulumi update cycle | 62 | 62 | 0 |
| Pulumi checkpoint save | 13 | 13 | 0 |
| Pulumi event batch of 1000 events | 1010 | 1010 | 0 |
| OpenTofu upload | 41 | 41 | 0 |
| OpenTofu lock | 45 | 45 | 0 |
| OpenTofu unlock | 11 | 11 | 0 |
| Lock, upload and unlock | 101 | 101 | 0 |
| Backup 3+3 | 357 | 357 | 0 |
| Restore 3+3 | 1044 | 1044 | 0 |
| Backup 30+30 | 438 | 438 | 0 |
| Restore 30+30 | 1179 | 1179 | 0 |
| **Pulumi stack rm (new probe, not in the suite)** | 20 | **51** | **+31** |

The stack rm probe is `tests/perf10_rmtrips_test.go`; statements in raw/rmtrips-*.txt.

## Single-request latency (`tests/perf10_latency_test.go`, PostgreSQL, 7 runs per commit alternated, median of the run percentiles, ms; raw/lat-*, lat-summary.txt)
Server and client in one process, so the numbers hold no network. Each run: 400 lock/unlock, 400 checkpoint saves, 100 update cycles, 200 stack rm.
| Flow | 842af92 p50 / p95 / p99 | 519b3bd p50 / p95 / p99 | Change p50 / p95 / p99 |
|---|---|---|---|
| OpenTofu lock | 5.67 / 7.97 / 9.78 | 5.64 / 8.31 / 11.54 | 0% / +4% / +18% |
| OpenTofu unlock | 1.72 / 2.55 / 3.27 | 1.76 / 2.61 / 3.62 | +3% / +2% / +11% |
| Pulumi checkpoint save | 3.65 / 5.97 / 7.14 | 3.65 / 5.56 / 7.23 | 0% / -7% / +1% |
| Pulumi update cycle (start, 2 saves, complete) | 15.37 / 20.41 / 22.94 | 16.54 / 20.87 / 22.86 | +8% / +2% / 0% |
| **Pulumi stack rm** | 2.73 / 3.88 / 4.89 | **6.44 / 9.21 / 10.76** | **+135% / +137% / +120%** |
| Contended lock, 16 clients, p50 (4 runs, `tests/perf10_contend_test.go`) | 27.9 | 28.0 | 0% |

Lock and unlock p99 come from 400 samples per run (the fourth-largest sample) and p50 and p95 do not move, with identical round trips. I treat them as tail noise and did not bisect; they need a larger sample before a bisect is worth the time. Say if you want it.

### Bisect of stack rm (raw/bisect-rm-*)
| Commit | Round trips | p50 (2 runs) |
|---|---|---|
| `1c856806` (parent of the gate change) | 20 | 2.74 / 2.63 ms |
| `76ad8cb6` Check the Pulumi import, restore, rename, rm and cancel at state.write | 51 | 6.86 / 6.72 ms |
| `bed01bdf` lint fixes of the same change | 51 | not run (trips only) |

## Load harness (`scripts/load.sh`, 7 pairs per store; raw/load-summary.txt, raw/load-*.json)
The harness reports p50 only. Mean over 7 runs, ms:
| Store | Metric | 842af92 | 519b3bd | Change |
|---|---|---|---|---|
| SQLite | seed | 46322 | 47291 | +2.1% |
| SQLite | total duration | 52076 | 53267 | +2.3% |
| SQLite | workspace locks p50 | 8.5 | 4.7 | -44% (range 3-19 ms) |
| SQLite | update conflicts p50 | 359 | 308 | -14% |
| SQLite | namespace updates p50 | 419 | 408 | -3% |
| SQLite | workspace list p50 | 18.9 | 21.2 | +12% (range 15-25 ms) |
| SQLite | Pulumi import 256 KB / 1 MB / 4 MB | 9.9 / 17.6 / 57.2 | 11.1 / 18.9 / 60.0 | +13% / +7% / +5% |
| SQLite | checkpoint full 4 MB | 45.3 | 37.0 | -18% |
| PostgreSQL | seed | 30356 | 28459 | -6% |
| PostgreSQL | total duration | 35575 | 33633 | -5% |
| PostgreSQL | workspace locks p50 | 6.2 | 9.9 | +59% (range 2.4-36 ms) |
| PostgreSQL | update conflicts p50 | 98.5 | 97.6 | -1% |
| PostgreSQL | namespace updates p50 | 86.7 | 97.3 | +12% |
| PostgreSQL | Pulumi import 256 KB / 1 MB / 4 MB | 19.2 / 27.9 / 69.8 | 28.0 / 34.3 / 72.8 | +46% / +23% / +4% |

Re-run of PostgreSQL only, 5 pairs (`scripts/load-pg-rerun.sh`, raw/load-pg-rerun-summary.txt, raw/load-pgr-*.json):
| Metric | 842af92 | 519b3bd | Change |
|---|---|---|---|
| namespace updates p50 | 92.4 | 86.4 | -6.5% (the +12% does not hold) |
| update conflicts p50 | 94.7 | 87.7 | -7.5% |
| import 256 KB / 1 MB / 4 MB | 22.2 / 27.7 / 65.2 | 23.8 / 29.7 / 62.2 | +7% / +7% / -4.5% |
| workspace locks p50 | 3.9 | 8.3 | +112% (range 1.8-6.4 against 2.8-16.9) |
| seed | 22308 | 27372 | +23% (2 of 5 new runs took 35-36 s; the other 3 took 21.4-22.4 s, the old runs 21.6-22.9 s) |

The workspace-lock p50 of the harness is 3 locks and about 125 refusals per run, so a few samples decide it. The dedicated contended-lock test (16 clients, 60 rounds, 4 runs for each commit) shows no change (27.9 against 28.0 ms), and the lock round trips are equal, so I treat the harness lock number as noise. The PostgreSQL import numbers (+46% first set, +7% re-run) are consistent with a gate cost of a few ms on a small import (the import route is gated at `76ad8cb6`), but the re-run does not confirm it. The cost is part of #2412.

## state.write gate and faultlog (new this round)
Micro-benchmarks, `-count=5`, median ns/op (raw/mb-*.txt; tests/perf10_gate_test.go, perf10_log500_internal_test.go, perf10_faultlog_test.go):
| Benchmark | 842af92 | 519b3bd | allocs/op old -> new |
|---|---|---|---|
| `Admitter.Admit`, guard that allows, no store | 96 ns | 94 ns | 0 -> 0 |
| `Admitter.Admit`, `Off` guard | 95 ns | 92 ns | 0 -> 0 |
| Pulumi adapter `writeError` for an unclassified 500, JSON log handler to discard (includes `httptest.NewRecorder`) | 48.7 us | 48.4 us | 85 -> 83 (38.5 KB/op both) |
| Same, level Error off (no line) | 46.0 us | 45.6 us | 85 -> 83 |
| `faultlog.LogInternal` alone (new only) | 1.12-1.45 us, 0 allocs | | |
| The old line (`ErrorContext`, status and error_code) alone | 0.83-1.0 us, 2 allocs (96 B) | | |

- The 500 log line: about +0.3 us over the old line, with fewer allocations, and 5% of the cost of a 500 answer in the adapter (the line itself is about 2.7 us of 48 us in both). It is not a regression.
- The Admitter itself costs about 95 ns with no allocation. The cost of a gate is the store reads, not the admitter: see #2412 (+31 round trips, +3.7 ms p50 for a rm on a local database).
- A Pulumi checkpoint write has no gate at `519b3bd`, so no gate cost exists there. The gated Pulumi routes are import, restore, rename, rm and cancel only.

## Backup and restore time (tests/perf10_latency_test.go TestPerf10Backup; raw/backup-*)
5 runs per size per commit, 2 passes alternated, median ms (the time is dominated by polling in the test harness, so it shows no more than "not much slower"):
| Size | 842af92 backup / restore | 519b3bd backup / restore |
|---|---|---|
| 3+3 | 1213 / 1538 | 1202 / 1538 |
| 30+30 | 1313 / 1668 | 1405 / 1579 |
Round trips of these are in the first table (equal).

## PostgreSQL open (#2348) (tests/perf9_migrate_pg_test.go from round 9, n=20, 3 runs; raw/pgopen-*)
| | 842af92 | 519b3bd |
|---|---|---|
| Open of a migrated schema, p50 | 17.2 / 18.2 / 18.0 ms | 17.9 / 18.8 / 17.5 ms |
| Fresh open (migrate), p50 | 218 / 362 / 332 ms | 268 / 334 / 324 ms |
Unchanged.

## Not measured
- Memory and allocation of whole requests: only the micro-benchmarks and the load harness peak-alloc fields (raw/load-*.json) give them; no peak-alloc change above noise except the PostgreSQL import peak (+40%, one run set, not in the re-run).
- Server start time and the SQLite open (round 9 items) were not run this round.
- Rename, import, restore and cancel round trips: not probed one by one.

## Limits
- Load numbers carry about +-20% run noise on this host. PostgreSQL has fsync off, so absolute latency is lower than on a durable disk; round-trip counts are not affected.
- The latency test runs the server and client in one process with the round-trip tracer on the store (same for both commits).
