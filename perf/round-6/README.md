# Performance check, round 6

Compare `11fa683` (end of round 5) with `origin/main` at `2e5f727` (262 commits later).
Same machine (4 CPUs Xeon 2.10 GHz, 16 GB, Linux 6.18, Go 1.27.3), runs back to back, alternating old and new.
SQLite and PostgreSQL 18.6 (UTF8 cluster, `max_connections` 200, local, port 5433).

## Method

- `scripts/bench.sh`: `make bench` scope (`./pkg/stack/ ./pkg/store/sqlite/`), `-benchtime 300ms`, 6 rounds each, compared with benchstat (`raw/benchstat.txt`). Re-run of the two suspects, 10 rounds (`raw/re-*.txt`).
- `scripts/load.sh`: `go run ./load` standard flows (10,000 units), 3 alternating rounds, SQLite then PostgreSQL. Each harness is built from its own commit.
- `scripts/scale.sh`: `-flows scale`, 1 and 2 instances, 32 clients, 500 units, 5 s warm-up, 30 s window, 2 alternating rounds, MinIO. The scale mode does not exist at `11fa683`. The harness of `main` drives both server binaries. The old binary runs behind `scripts/bin-old-wrap.sh`, which maps `--installation` to the old flag name `--region`.
- Round trip tests: `TestRoundTripsOfRequests` and `TestRoundTripsOfBackup` (PostgreSQL). They do not exist at `11fa683`. The old tree got the instrumentation of `bab1cc24` and the test file of `main` (the budgets of `main` then fail on the old tree, which is the comparison).
- Bisect: `scripts/at.sh`, `scripts/allocs.sh`, `scripts/bis-*.sh`, `scripts/rt-at.sh`.

## Noise

- Go benchmarks: the spread (±) of 6 runs is 5 to 25 percent for time, 0 percent for allocs and bytes.
- Load, SQLite, three runs: seed 35.1 to 35.4 s old. Spread under 1 percent.
- Load, PostgreSQL: seed 25.2 to 27.1 s old. One new run (the first, 37.7 s) is an outlier. Median is not a safe measure there.
- Scale: the same binary differs by up to 5 percent between two runs (528 and 550 requests each second on old, 527 and 556 on new).

## Results

See the report to the PM and the issue it links. Raw data are in `raw/`.
