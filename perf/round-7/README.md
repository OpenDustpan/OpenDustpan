# Performance check, round 7

Compare `2e5f727` (end of round 6) with `da75bb7` (main after train 31, 50 commits later).
One host (4 CPUs, 16 GB, Linux 6.18, Go 1.27.3), alternating old and new, SQLite and a local PostgreSQL 18.6 (port 5433).

## Method and limits
- `scripts/bench.sh`: Go benchmarks, benchtime 300 ms, packages stack, store/sqlite, store/postgres, dustpan, workspace, ipallow, pulumijournal. n=3 only: the PostgreSQL package takes about 4 minutes each run, so the budget stopped the run at 3 rounds each (benchstat shows "need >= 6 samples"). Time is noisy on this host (about 2x slower than in round 6). Allocations are exact. No suspect needed an n=10 re-run.
- `scripts/load.sh`: standard load flows, 3 alternating runs, SQLite and PostgreSQL. `scripts/sum.py` summarizes.
- Round trips: `TestRoundTripsOfRequests` and `TestRoundTripsOfBackup` on PostgreSQL, both trees (`raw/roundtrips-*.txt`), bisect in `raw/bisect-*.log`.
- Scale run: not done (budget).

## Results
- #2207 unchanged: CheckpointSaveFull1000 255 allocs, 10.17 KiB at both commits (time 251 us old, 241 us new, within noise).
- Load (3 runs): SQLite seed 50.9 s old, 48.8 s new (-4%); PostgreSQL seed 38.3 s old, 35.6 s new (-7%). No flow above noise. One PostgreSQL workspace-list outlier (5.9 s, other two runs 2.3 s).
- Allocations: no change in the SQLite, journal, delta, gzip and IP allowlist benchmarks. LeaseCheck on PostgreSQL shows +60 allocs with the check and +40 without it (266 to 326, 229 to 269), so the deadline check itself adds none. Time is noise-bound at n=3.
- Round trips (PostgreSQL): Pulumi update cycle 58 to 62 (+4) since `8e50c93`, filed as #2240. New test: 1000-event batch is 1010 round trips (#2225). Checkpoint save 13, OpenTofu flows unchanged. Backup 3+3: 335 to 355, restore 1139 to 1180; the same commit varies by about 12 between runs, so growth is from new tables and reads (#2127, #2125).
