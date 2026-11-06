# Performance check, round 12

Compare `76073e0` (main, new) with `b4e1757` (end of round 11, old); 40 commits. One host (4 CPUs, Linux 6.18, Go 1.27.3), local PostgreSQL 18.6 on port 5433 (fsync off), two worktrees, runs alternated. 5 runs per commit (weekly usage at the warning level), not 7. The load harness was skipped: no core number moved. Compare old and new only.

## Result
- No regression above 10% that holds on a second look. No new round trip on a hot path. No issue filed.
- Round trips: every request flow is equal (lock 31, unlock 11, upload 41, update cycle 62, checkpoint 13, stack rm 37). Backup and restore cost a fixed +4 and +8 round trips (+1.1% at 3+3, +0.9% at 30+30). Bisected to `e53d529` (passkey store: the new passkey tables). Not a hot path, not per row, below 10%: no issue.
- Sign-in service on the new path: latency unchanged; allocations +2.2% (password) and +1.9% (OIDC callback).
- Candidate not confirmed: manual lock DELETE p95 +12.4% in the first 5 pairs; with 8 more pairs (13 in total) it is +0.8% (old 1.00-1.23, new 1.13-1.21 ms). PUT p99 +11.9% over 13 pairs is a tail of 500 samples with overlapping ranges (old 1.77-3.12, new 2.02-4.63 ms). Not filed.

## Round trips (PostgreSQL, TestRoundTrips*, 5 runs per commit, exact in all runs)
| Flow | b4e1757 | 76073e0 |
|---|---|---|
| Pulumi update cycle | 62 | 62 |
| Pulumi checkpoint save | 13 | 13 |
| Pulumi 1000 events | 1010 | 1010 |
| Pulumi stack rm / import / restore / rename / cancel | 37 / 64 / 65 / 34 / 30 | same |
| OpenTofu upload | 41 | 41 |
| OpenTofu lock | 31 | 31 |
| OpenTofu unlock | 11 | 11 |
| Lock, upload, unlock | 87 | 87 |
| Backup 3+3 / 30+30 | 357 / 438 | **361 / 442** |
| Restore 3+3 / 30+30 | 1044 / 1179 | **1052 / 1187** |

## Latency (ms, median of the per-run percentiles; summary.txt has every metric)
| Flow | b4e1757 p50/p95/p99 | 76073e0 p50/p95/p99 | allocs/op old -> new |
|---|---|---|---|
| OpenTofu lock (PostgreSQL, n=1000 per run) | 4.61 / 6.26 / 7.97 | 4.60 / 6.36 / 8.24 | pair 1817 -> 1817 |
| OpenTofu unlock | 1.86 / 2.65 / 3.45 | 1.86 / 2.66 / 3.32 | |
| Local password sign-in (SQLite, in process, n=100) | 29.03 / 37.06 / 40.27 | 28.79 / 36.04 / 40.01 | 1028 -> 1051 (20.0 MB/op both) |
| OIDC callback (n=300) | 2.58 / 3.70 / 10.31 | 2.60 / 3.68 / 6.27 | 933 -> 951 (66.2 -> 67.2 KB) |
| Manual lock PUT, HTTP, unit lock (13 pairs, n=500) | 0.78 / 1.21 / 2.07 | 0.77 / 1.17 / 2.32 | pair 1218 -> 1218 |
| Manual lock DELETE, HTTP | 0.73 / 1.13 / 1.83 | 0.72 / 1.14 / 1.77 | |

## New in this round (no old value: the route set exists at the old commit, the chain does not)
| Flow | p50 / p95 / p99 ms | allocs/op | round trips |
|---|---|---|---|
| Manual lock service Lock through the one authorizer chain (full server, SQLite, n=2000) | 0.33 / 0.66 / 1.33 | pair (Lock + Unlock) 895, 40.3 KB | not counted (SQLite) |
| Manual lock service Unlock | 0.33 / 0.63 / 1.29 | | |
| Passkey register begin (HTTP, SQLite, n=288) | 0.56 / 0.82 / 1.08 | 456, 32.7 KB | 5 statements (PostgreSQL, service call) |
| Passkey register finish (HTTP, SQLite, n=288) | 0.98 / 1.59 / 2.16 | 807, 54.6 KB | 13 statements (PostgreSQL, service call) |
The manual lock HTTP route has equal cost at both commits, so the one authorizer chain adds no measurable time on the request path. The statement counts of the passkey flows are the pgx tracer statements of `mfasvc.Service` on PostgreSQL, with begin and commit; the finish is one challenge take, then one transaction of the audit scope lock, the local user and person reads, the passkey count, the insert and the audit chain append (see tests/perf12_mfa_pg_test.go, PERF12_SQL=1).

## PostgreSQL open, p50 ms (5 schemas, 5 opens each)
| | b4e1757 | 76073e0 |
|---|---|---|
| Open of a migrated schema | 18.6 | 18.8 |
| Fresh open (migrate) | 229 | 219 |

## Backup and restore wall time (ms, one backup plus one restore, median of 10 runs per commit)
2906 old, 2949 new (+1.5%). Dominated by polling in the test; use the round trip counts.

## Limits
fsync off; server and client in one process; sign-in and passkey HTTP latency on SQLite; allocation counters are process-wide (include the test client and provider). Passkey begin budget (10 per person per 5 minutes) forced the HTTP passkey test to spread ceremonies over 4 people and 8 fixtures. The round 11 test files were not on its branch, so the tests are rewritten and stored in `tests/`: copy each file into the package named in its first line to run it. The load harness was not run.
