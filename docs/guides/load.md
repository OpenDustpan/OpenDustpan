# Run the load harness

This guide shows how to run the load harness and how to read its JSON report.
It follows ADR 40.

## What the harness does

The harness starts its own server and replays recorded CLI request sequences.
It needs no real CLI at run time.
It measures checkpoint saves, stack imports, lock contention, update contention, database latency, and list latency.
It writes the result as one JSON report for the nightly budget job.

With SQLite (the default) the harness runs one instance.
With `DUSTPAN_TEST_POSTGRES_URL` set it runs two instances on one database.
The two instances share one blob directory and one key-encryption key.
Each run uses a fresh namespace, so runs never share data.

## Run it

Run the harness with the make target:

```sh
make load
```

This writes `load-report.json` in the repo root.
It uses SQLite and the default 10,000 units, half stacks and half workspaces.

Run it directly to change the output path or the unit count:

```sh
go run ./load -out /tmp/load-report.json -units 1000
go run ./load -namespace load-nightly
```

Run it against PostgreSQL with the test URL:

```sh
DUSTPAN_TEST_POSTGRES_URL=postgres://user:pass@localhost:5432/dustpan go run ./load -out pg-report.json
```

Point the URL at an empty database.
The store migrates the schema on open, and each run uses a fresh schema.

Set `DUSTPAN_LOAD_TOKEN` to override the test token.
The default token is test-only and never leaves the local process.

## Cost in `make check`

The smoke test in `load/` adds about 2 seconds.
The `bench` target adds about 2 seconds.
It runs each benchmark once with `-benchtime 1x`:

```sh
go test -run '^$' -bench . -benchtime 1x ./pkg/stack/ ./pkg/store/sqlite/
```

The benchmarks cover delta apply, gzip at the configured level, and the checkpoint store path.

## Report fields

Times use milliseconds.
Byte counts use bytes.
`p50` is the median of the measured attempts.

Top-level fields of `Report`:

| Field | Meaning |
| --- | --- |
| `time` | UTC time of the run in RFC 3339 form |
| `store` | Store under test: `sqlite` or `postgres` |
| `namespace` | Namespace that held the run |
| `units` | Unit count the run seeded |
| `durationMs` | Wall time of the full run |
| `seedMs` | Wall time of the seed phase |
| `checkpoints` | One entry per checkpoint size, see below |
| `imports` | One entry per import size, see below |
| `workspaceLocks` | Lock contention result, see below |
| `updateConflicts` | Update starts on one stack at once, see below |
| `namespaceUpdates` | Update starts on many stacks in one namespace at once, see below |
| `database` | Multi-instance result, see below |
| `lists` | List latency result, see below |

Each entry of `checkpoints` holds one body size:

| Field | Meaning |
| --- | --- |
| `sizeBytes` | Deployment size that the run saved |
| `fullMs` | Time of the full (verbatim) save |
| `deltaMs` | Time of the delta save |
| `fullPeakAllocBytes` | Peak heap bytes sampled during the full save |
| `deltaPeakAllocBytes` | Peak heap bytes sampled during the delta save |

Peak values cover server and client together.
The server runs in-process, so the sampler meets both sides.

Each entry of `imports` holds one body size:

| Field | Meaning |
| --- | --- |
| `sizeBytes` | Deployment size that the run imported |
| `importMs` | Time of the import |
| `peakAllocBytes` | Peak heap bytes sampled during the import |
| `checkpointBytes` | Stored checkpoint size in bytes |

`workspaceLocks` holds the lock contention run on one workspace:

| Field | Meaning |
| --- | --- |
| `goroutines` | Worker count that locked at once |
| `rounds` | Lock rounds per worker |
| `locks` | Locks won |
| `conflicts` | 409 answers counted as refused |
| `p50Ms` | Median lock attempt latency |

`updateConflicts` and `namespaceUpdates` have the same fields.

`updateConflicts` starts all its updates on one stack. The stack lock lets one update start and refuses the others with 409, so this run measures the conflict path.

`namespaceUpdates` starts one update on each of many stacks in one namespace. No stack lock refuses a start, so this run measures contention on the namespace fence and on the store. Every update starts.

| Field | Meaning |
| --- | --- |
| `attempts` | Update starts attempted at once |
| `started` | Updates that started |
| `conflicts` | Starts refused with 409 |
| `p50Ms` | Median start attempt latency |

`database` holds the update start latency over the instances:

| Field | Meaning |
| --- | --- |
| `instances` | Server instance count: 1 or 2 |
| `storesOpen` | Store handle count: 1 or 2 |
| `firstP50Ms` | Median start latency on instance 0 |
| `secondP50Ms` | Median start latency on instance 1 |
| `secondPresent` | True when a second instance ran |

With one instance `secondP50Ms` stays 0 and `secondPresent` stays false.

`lists` holds the list latency with a full inventory:

| Field | Meaning |
| --- | --- |
| `units` | Unit count that the run seeded |
| `unitsListed` | Unit count that the inventory returned |
| `workspaceListP50Ms` | Median latency of one workspace page read |
| `workspaceListFullMs` | Time to walk all workspace pages |
| `inventoryMs` | Time of the inventory read |

## What this guide does not cover

These items are not part of the harness:

- Budget thresholds for the nightly job.
- Real CLI runs against the harness server.
- Fault injection and crash testing.
