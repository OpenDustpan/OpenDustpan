# Performance check, round 11

Compare `b4e1757` (main, new) with `519b3bd` (end of round 10, old); 27 commits. One host (4 CPUs, 16 GB, Linux 6.18, Go 1.27.3), local PostgreSQL 18.6 on port 5433 (fsync off), two worktrees, runs alternated. Absolute times differ from round 10 (other host load); compare old and new only.

## Result
- No regression above 10% that holds on a second look. No new round trip on a hot path. No issue filed.
- Improvements: OpenTofu lock 45 -> 31 round trips (p50 7.8 -> 5.7 ms, contended lock p50 34.7 -> 24.3 ms); Pulumi stack rm 51 -> 37 (p50 8.3 -> 6.7 ms). Both come from the gate and lock changes in the range. #2347 is therefore partly lower: lock is 31, unlock still 11. Stack rm is still 17 above the 20 it cost before the state.write gate (#2412, closed).
- Sign-in: no change. IP allowlist and trusted-proxy steps: no change.
- Not reproduced: one 30+30 restore at `b4e1757` did not end in the test time limit (1 of 77 runs; 0 of 80 at `519b3bd`). Its log shows `durable operation attempt failed` (attempt 1) then `claim lost` (attempt 2). Raw: raw/backup-new-2.txt. Watch item, not bisected: no repro in 6 more alternated passes.

## Round trips (PostgreSQL, TestRoundTrips*, 8 runs per commit, exact in all runs)
| Flow | 519b3bd | b4e1757 |
|---|---|---|
| Pulumi update cycle | 62 | 62 |
| Pulumi checkpoint save | 13 | 13 |
| Pulumi 1000 events | 1010 | 1010 |
| OpenTofu upload | 41 | 41 |
| OpenTofu lock | 45 | **31** |
| OpenTofu unlock | 11 | 11 |
| Lock, upload, unlock | 101 | **87** |
| Backup 3+3 / 30+30 | 357 / 438 | 357 / 438 |
| Restore 3+3 / 30+30 | 1044 / 1179 | 1044 / 1179 |
| Pulumi stack rm (tests/perf11_rmtrips_test.go) | 51 | **37** |
New tests in the suite at `b4e1757` (no old value in the suite): import 64, restore 65, rename 34, cancel 30, rm 37.

## Single-request latency (ms, median of 7 run percentiles; lock/unlock n=1000 per run, others as round 10)
| Flow | 519b3bd p50/p95/p99 | b4e1757 p50/p95/p99 | Change |
|---|---|---|---|
| OpenTofu lock | 7.77 / 10.77 / 13.18 | 5.66 / 7.96 / 10.24 | -27% / -26% / -22% |
| OpenTofu unlock | 2.37 / 3.58 / 4.57 | 2.27 / 3.38 / 4.42 | -4% / -6% / -3% |
| Pulumi checkpoint save | 5.39 / 7.32 / 9.23 | 5.35 / 7.52 / 9.76 | -1% / +3% / +6% |
| Pulumi update cycle | 22.19 / 27.77 / 39.91 | 23.02 / 29.54 / 37.05 | +4% / +6% / -7% |
| Pulumi stack rm | 8.33 / 11.46 / 13.91 | 6.72 / 9.43 / 12.50 | -19% / -18% / -10% |
| Contended lock, 16 clients (4 runs) | 34.7 / 42.6 / 44.9 | 24.3 / 28.2 / 29.4 | -30% |

## Sign-in (internal/api/server, in process, SQLite, 5 runs per commit alternated; tests/perf11_signin_test.go)
| Flow | 519b3bd p50/p95/p99 ms | b4e1757 p50/p95/p99 ms | allocs/op old -> new |
|---|---|---|---|
| Local password sign-in (n=100 per run) | 31.18 / 43.2 / 49.5 | 31.08 / 42.0 / 47.6 | 1028 -> 1029 (20.0 MB/op both, hash cost) |
| OIDC callback only, test provider (n=300) | 2.95 / 4.04 / 13.19 | 2.82 / 3.85 / 13.93 | 931 -> 934 (65.9 -> 66.2 KB) |
| OIDC start | 0.25 / 0.42 / 0.87 | 0.25 / 0.43 / 0.85 | |
The new Assertion check on the OIDC path adds 3 allocations (+0.3%) and no measurable time. Heap in use after the run: 3.2 MB (OIDC) and 45 MB (password), same for both commits.

## IP allowlist and trusted proxy on every request (pkg/dustpan, one namespace read through ServeHTTP, 9 runs of 20000 per commit; tests/perf11_proxy_bench_test.go)
| Case | 519b3bd ns/op (B, allocs) | b4e1757 ns/op (B, allocs) |
|---|---|---|
| Baseline, both off | 47158 (12262, 128) | 47428 (12263, 128) |
| Allowlist on | 65953 (13415, 161) | 65197 (13416, 161) |
| Trusted proxy only, request via proxy | 46053 (12310, 131) | 49614 (12311, 131) |
| Allowlist and trusted proxy | 64091 (13464, 164) | 65929 (13464, 164) |
Allocations are equal. The allowlist costs about 18 us and 33 allocations in both. The proxy-only case reads +7.7% (3.6 us) but the baseline is equal and the spread of the runs is 46-51 us; below the 10% line and not confirmed.

## PostgreSQL open (#2348), 3 runs, p50 ms
| | 519b3bd | b4e1757 |
|---|---|---|
| Open of a migrated schema | 18.8 / 21.8 / 20.1 | 21.2 / 21.2 / 20.0 |
| Fresh open (migrate) | 229 / 436 / 210 | 245 / 252 / 395 |
Unchanged within noise (#2348 stays as it is).

## Backup and restore time (median ms, 40 runs per size per commit; 37 at 30+30 new)
| Size | 519b3bd backup / restore | b4e1757 backup / restore |
|---|---|---|
| 3+3 | 1233 / 1656 | 1244 / 1636 |
| 30+30 | 1078 / 1716 | 1136 / 1650 |
The time is dominated by polling in the test. Round trips are equal (table above).

## Load harness (5 pairs per store, plus 6 SQLite pairs; raw/load-summary.txt, raw/load-sqlite-rerun-summary.txt)
Candidates above 10% in the first set, none confirmed:
- SQLite update conflicts p50 +22% (first 5 pairs) -> +12% in 6 more pairs with overlapping ranges (old 399-584, new 387-611); PostgreSQL -3%.
- PostgreSQL seed +20%: 2 of 5 new runs took 39-40 s, the other 3 took 27-30 s (old 28-41 s). Same two-mode noise as round 10.
- PostgreSQL workspace list p50 +84%: one new run of 286 ms; the other four are 46-54 ms (old 47-58 ms).
- SQLite 4 MB import +12% in the first set, -16% in the re-run.
- Workspace locks p50 (few samples per run): SQLite +6% then +74% (4-15 ms range); PostgreSQL -34%. Noise as in round 10; the dedicated contended test shows -30%.
Peak alloc of import and checkpoint: no change above +-10% that repeats (checkpoint delta, PostgreSQL, +8 to +11% on 2 of 6 sizes, other sizes -8% to +1%).

## Limits
fsync off; server and client in one process in the latency and sign-in tests (no network); load harness has about +-20% run noise on this host. OIDC allocation counters are process-wide (include the test provider). Load used 5 pairs, not 7. `make load` was run as the built binary of `./load` for each commit.
