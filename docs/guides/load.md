# Run the load harness

This guide shows how to run the load harness and how to read its JSON report.
It follows ADR 40.

## What the harness does

The harness starts its own server and replays recorded CLI request sequences.
It needs no real CLI at run time.
It measures checkpoint saves, stack imports, lock contention, update contention, database latency, and list latency.
It also measures journaled updates, and the runner and jobs routes with and without an IP allowlist.
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

## Measure journaled updates

The release build answers journal version 0 for every update, so the Pulumi CLI saves checkpoints and the server takes no journal batch.
In that build the harness reports `journal.supported` as `false` and sends no batch.
Build the harness with the `journalrecording` tag to measure journaled updates:

```sh
go run -tags journalrecording ./load -out /tmp/load-report.json
```

Use the tag for the nightly run, so that the report holds the journal timings.

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
| `journal` | Journaled update result, see below |
| `access` | Runner route and jobs route result, see below |

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

`journal` holds the journaled update runs.
Each run starts one update that offers journaling, sends the batches in order, and completes the update.
A batch holds an entry that opens and an entry that ends each resource, so 2 entries are 1 resource of about 1 KiB.
The harness gzips a batch of 1 KiB or more, as the Pulumi CLI does.

| Field | Meaning |
| --- | --- |
| `supported` | True when the server chose a journal version above 0 |
| `runs` | One entry for each entry count, see below |

Each entry of `runs` holds one entry count:

| Field | Meaning |
| --- | --- |
| `entriesPerBatch` | Journal entries in one batch: 2, 20 and 200 by default |
| `batches` | Batches sent in the run |
| `batchBytes` | Size of one batch before gzip |
| `sentBytes` | Total batch bytes before gzip |
| `appendP50Ms` | Median time of one batch append |
| `appendMaxMs` | Longest batch append |
| `appendTotalMs` | Sum of the append times |
| `completeMs` | Time to complete the update, which builds the checkpoint from the batches |
| `peakAllocBytes` | Peak heap bytes sampled during the appends |

`access` holds the runner route and the jobs route.
The runner route lists the runner pools of a namespace.
The jobs route reads one queued job.
Both routes run the namespace read check, which includes the IP allowlist check.
The harness times each route in four states:

| State | Server | Namespace |
| --- | --- | --- |
| `gateOff` | IP allowlists off | The run namespace |
| `gateNoList` | IP allowlists on, also for the bootstrap token | The run namespace, with no list |
| `gateList` | IP allowlists on | A namespace with a list of `listEntries` entries that holds the client |
| `gateRefused` | IP allowlists on | A namespace with a list that does not hold the client |

The refused request gets the answer of a route that must not tell that the object exists.
`refusedStatus` holds that status.
The refused timing covers the coded error answer and the audit entry of a refusal.

| Field | Meaning |
| --- | --- |
| `requests` | Requests in each timing, after 10 warm-up requests |
| `listEntries` | Entries in the list that holds the client |
| `runnerRoute`, `jobsRoute` | One entry for each route, see below |

Each route entry holds `method`, `path`, `refusedStatus`, and these medians in milliseconds:

| Field | Meaning |
| --- | --- |
| `gateOffP50Ms` | Median with IP allowlists off |
| `gateNoListP50Ms` | Median with IP allowlists on and no list |
| `gateListP50Ms` | Median with a list that holds the client |
| `gateRefusedP50Ms` | Median of a refused request |

The harness sends the requests one at a time from the local process.
So the medians show the cost on the server and not the load of many clients.

## What this guide does not cover

These items are not part of the harness:

- Budget thresholds for the nightly job.
- Real CLI runs against the harness server.
- Runner protocol routes that need a joined runner, and the start of a job through a pool.
- Fault injection and crash testing.
