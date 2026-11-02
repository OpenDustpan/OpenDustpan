# Performance check, round 8

Compare `da75bb7` (end of round 7) with `6c35afa` (main, 50 commits later). One host (4 CPUs, 16 GB, Linux 6.18, Go 1.27.3), local PostgreSQL 18.6 on port 5433 (fsync off).

## Result
No regression above 10% that survives a re-run. No new round trip on a hot path. No issue filed.

## Round trips (PostgreSQL, `TestRoundTrips*`, 2 runs per commit; raw/roundtrips-*)
| Flow | da75bb7 | 6c35afa |
|---|---|---|
| Pulumi update cycle | 62 | 62 |
| Pulumi checkpoint save | 13 | 13 |
| Pulumi event batch of 1000 events | 1010 | 1010 |
| OpenTofu upload / lock / unlock | 41 / 42 / 10 | 41 / 42 / 10 |
| Lock, upload and unlock | 97 | 97 |
| Backup 3+3 | 355, 350 | 346, 357 |
| Restore 3+3 | 1165, 1185 | 1194, 1195 |
| Backup 30+30 | 436, 432 | 438, 438 |
| Restore 30+30 | 1315, 1319 | 1330, 1330 |

Restore grows 1-2% (new tables). The same commit varies by up to 20 between runs. The old commit failed its own restore ceiling (1185 > 1183) in run 2, so the ceiling is flaky at the old commit too.

## Load harness (`scripts/load.sh`, SQLite 9 pairs, PostgreSQL 3 pairs; raw/load-summary.txt)
- Seed: SQLite 60.9 s old, 62.7 s new (+2.9%); PostgreSQL 37.2 s old, 34.6 s new (-7.0%).
- Candidate: SQLite `namespaceUpdates` p50 was +17.7% in pairs 1-6 (old first in each pair). Pairs 7-9 ran new first: new 436-475 ms, old 493-618 ms. Run-order effect, not a regression. A 200-unit run showed no difference. Over 9 pairs: namespaceUpdates +6.3%, updateConflicts +3.0%. No bisect needed.
- PostgreSQL `namespaceUpdates` +5.3%, `updateConflicts` +4.2%, lists +0.2% to +4.2%: noise at n=3.

## Sign-in with a local password (new commit only: the feature does not exist at da75bb7)
Argon2id default: 19456 KiB, 2 iterations, 1 lane. Budget 81920 KiB, wait 2 s. Verify of one correct password: p50 34.9 ms, p95 40.4 ms, 36 allocs and 19.9 MB allocated per verify (3 runs in raw/signin-new-*.txt).
| Concurrent sign-ins | p50 | p95 | p99 | busy errors | peak budget KiB | peak heap in use | peak Sys |
|---|---|---|---|---|---|---|---|
| 1 | 41 ms | 41 ms | 41 ms | 0 | 19456 | 19.8 MiB | 48 MiB |
| 4 | 104 ms | 263 ms | 263 ms | 0 | 77824 | 76.9 MiB | 88 MiB |
| 16 | 98 ms | 168 ms | 168 ms | 0 | 77824 | 133.9 MiB | 148 MiB |
| 64 | 381 ms | 686 ms | 713 ms | 0 | 77824 | 153.0 MiB | 168 MiB |
The budget holds (never above 81920 KiB). It counts live hash memory only: heap in use and Sys reach about 2x the budget at 16 or more concurrent sign-ins, because each verify allocates 19.9 MB and the GC frees it later. Plan the container memory limit at about 2x the budget plus the base.

## Name-lock table (new commit only; `tests/perf8_namelock_test.go`, raw/namelock-new-1.txt)
- 100,000 failed sign-ins (2 counts each, 16 workers): 52.8 s, 1894 sign-ins/s; one pair of adds p50 8.1 ms, p95 11.7 ms, p99 13.7 ms. 200,000 rows, 32.8 MiB.
- Prune with the default limit of 10,000 rows per run: 21 runs, p50 76.9 ms, max 222 ms, 1.92 s in total; table empty after.
- Observation, not a regression: the server runs one prune per hour, so it deletes at most 10,000 rows per hour. A sustained rate of more than about 2.8 new aged-out rows per second makes the table grow with no limit.

## Limits
Go benchmarks (`make bench` scope) were not run this round; the round-7 allocation benchmarks have no changed code in scope. Load numbers on SQLite carry about +-20% run noise on this host.
