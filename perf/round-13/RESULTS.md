# Performance check, round 13

Compare `76073e03` (old, end of round 12) with `e942f91b8` (main, new); 115 commits. One host (4 CPUs, Linux 6.18), local PostgreSQL 18 on port 5433 (fsync off), two worktrees. Baselines: branch `perf/round-12`, commit `5d3869e`.

## Result
- **One new hot-path round trip, filed: issue #2620.** Five Pulumi stack flows (rm, import, restore, rename, cancel) cost +1 round trip (+1.5% to +3.3%) when the stack is in a collection. Bisected to `c563d8b` (placement rule puts a new stack in a collection). The commit raised the budgets on purpose; the issue asks if the read can fold into an existing one.
- No latency regression above 10% that holds on a second look. No allocation growth on any benchmark (allocs/op equal, or within 0.01% on the journal and blob-collector rows).
- Fixed round-trip growth for backup and restore. Not a hot path, not per row, below 10%: no issue (same rule as round 12). Bisected: `c563d8b` +15 restore; `920028b` (forgelink tables) +6 backup, +16 restore.
- Candidate not confirmed: `BenchmarkDeltaApply256K` +13.1% in the first run. The delta code did not change in the range (`pkg/stack` changed in `stack.go` placement only). Rerun, 10 runs per commit: old 99-158 us, new 113-158 us, 11 allocs/op both. Host noise. Not filed.

## Round trips (PostgreSQL, TestRoundTrips*, exact)
| Flow | 76073e03 | c563d8b | 920028b | e942f91b8 |
|---|---|---|---|---|
| OpenTofu lock / unlock / upload | 31 / 11 / 41 | same | same | same |
| Lock, upload, unlock | 87 | 87 | 87 | 87 |
| Pulumi update cycle / checkpoint save / 1000 events | 62 / 13 / 1010 | same | same | same |
| Pulumi stack rm | 37 | **38** | 38 | **38** |
| Pulumi stack import | 64 | **65** | 65 | **65** |
| Pulumi stack restore | 65 | **66** | 66 | **66** |
| Pulumi stack rename | 34 | **35** | 35 | **35** |
| Pulumi cancel | 30 | **31** | 31 | **31** |
| Backup 3+3 / 30+30 | 361 / 442 | 361 / 442 | **367 / 448** | 367 / 448 |
| Restore 3+3 / 30+30 | 1052 / 1187 | **1067 / 1202** | **1083 / 1218** | 1083 / 1218 |

The `c563d8b` column is the commit itself; its parent equals `76073e03` on every row (raw/roundtrips-bis-parent.txt). `920028b^` equals `c563d8b` for backup and restore (raw/roundtrips-bis-920p.txt). Restore 3+3 +2.9%, 30+30 +2.6%; backup +1.7% and +1.4%.

## Latency, new baselines (ms; median of 3 runs per commit, old and new alternate)
| Flow | 76073e03 p50 | e942f91b8 p50 | allocs/op old -> new |
|---|---|---|---|
| Manual lock service Lock (SQLite, n=2000) | 0.361 | 0.331 | pair 895 -> 895 (40.2 KB both) |
| Manual lock service Unlock | 0.363 | 0.333 | |
| Manual lock PUT, HTTP | 0.845 | 0.812 | pair 1218 -> 1218 |
| Manual lock DELETE, HTTP | 0.806 | 0.768 | |
| Passkey register begin (HTTP, SQLite, n=288) | 0.633 | 0.650 | 454-456 -> 454-456 |
| Passkey register finish | 1.132 | 1.152 | 805 -> 809 (+0.5%) |
| Local password sign-in (n=100) | 34.97 | 30.97 | 1052 -> 1051 |
| OIDC callback (n=300) | 3.039 | 3.035 | 951 -> 951 |

Round-12 values on a quieter run: lock chain 0.33, passkey begin 0.56, finish 0.98. This host ran about 10% slower in absolute terms (old commit too), so compare columns, not rounds. p95 and p99 are in raw/lock-*.txt and raw/signin-*.txt.

## Go benchmarks (SQLite and in-process; 5 runs per commit, median)
See `bench-table.md` for every row (ns/op, allocs/op, B/op). Highlights:
- State write, Pulumi: `CheckpointSaveFull1000` 284 -> 263 us, `Delta1000` 509 -> 463 us (allocs 270 / 326 both).
- State write, journal build: all six `JournalBuild*` rows -3% to -8%; allocs equal within 0.01%.
- State write, OpenTofu: `Upload32MiB` -7.9%; allocs 226075 -> 226076.
- Store write check: `WriteCheck/*` -2% to +6% (one row +5.7%, ranges overlap); allocs 42 / 56 / 59 both.
- Authorizer and grants: `Authorizer/check` -0.6%, `CallerGrants` -3% to -6%; allocs equal.
- Lease check (SQLite): -17% / -9%; allocs equal. IP allowlist gate: -13% to -17%; allocs equal.
- The old run came second, so the 5-8% faster new numbers across the board are drift, not a gain.

## Not measured
- Job recorder, runner claim and audit write: the repository has no Go benchmark for them at either commit. The round-trip tests cover the update cycle and state writes only. No claim is made for these three.
- PostgreSQL benchmarks: `BenchmarkLeaseCheck/postgres` failed to connect in the `go test -bench` run (no PostgreSQL then; exit 1 in raw/bench-*.txt). The `pkg/store/postgres` package benchmarks (`FencedWrite`, `WriteCheck`, `Authorizer`) were not in the run. SQLite rows are complete. Round trips ran on PostgreSQL afterwards.
- Stack create with a placement rule (`CreateStackPlaced`) round trips: outside the measured window of the flows.
- Load harness: not run, no core number moved.

## Limits
fsync off; server and client in one process; allocation counters are process-wide. 3 runs per commit for the latency tests, 5 for the Go benchmarks. The round-12 tests (`perf/round-12/tests/perf12_*`) were copied into `pkg/dustpan` and `internal/api/server` of both worktrees; they are not committed here. `tests/cmp.py` compares medians (benchstat is not installed).
