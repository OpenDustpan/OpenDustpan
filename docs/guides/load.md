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

The refused request gets the same answer on both routes: status 403 with the error code `access.ip-not-allowed`.
`refusedStatus` and `refusedCode` hold that status and code.
A different answer ends the run.
The refused timing covers that answer only.
The server writes an audit entry only for the first refusal of a principal, action and scope in a window.
The timed requests therefore write no audit entry.

| Field | Meaning |
| --- | --- |
| `requests` | Requests in each timing, after 10 warm-up requests |
| `listEntries` | Entries in the list that holds the client |
| `runnerRoute`, `jobsRoute` | One entry for each route, see below |

Each route entry holds `method`, `path`, `refusedStatus`, `refusedCode`, and these medians in milliseconds:

| Field | Meaning |
| --- | --- |
| `gateOffP50Ms` | Median with IP allowlists off |
| `gateNoListP50Ms` | Median with IP allowlists on and no list |
| `gateListP50Ms` | Median with a list that holds the client |
| `gateRefusedP50Ms` | Median of a refused request |

The harness sends the requests one at a time from the local process.
So the medians show the cost on the server and not the load of many clients.

## Measure the blob flows

The blob flow mode times the CLI flows that write a blob, and a backup and a restore.
It does not seed the 10,000 units of the nightly run.

```sh
go run ./load -flows blobs -out /tmp/blob-report.json
```

The mode runs one instance with a backup target.
The server runs with `Run`, as the server binary does, so its backup worker runs.
The run first writes a data set: `-dataset-units` workspaces with one state version each and as many stacks with one checkpoint each.
It takes a backup of the data set, then restores it with `force`.
Then it times `-reps` OpenTofu state uploads on one workspace (lock, upload, unlock) and `-reps` Pulumi updates, each on its own stack.
A failed attempt counts in the report and does not stop the run.

These flags change the mode:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-blobs` | `fs` | Live blob store: `fs` (a directory) or `s3` |
| `-s3-probe` | `true` | Open the S3 store with the conditional-write probe, as the server binary does |
| `-reps` | `20` | Timed state uploads and timed checkpoint saves |
| `-state-bytes` | `65536` | Size of each timed state |
| `-checkpoint-bytes` | `262144` | Size of each timed checkpoint |
| `-dataset-units` | `20` | Workspaces and stacks in the data set |
| `-backup` | `true` | Take the backup and the restore |
| `-http-timeout` | `120` | Bound of each request in seconds |
| `-max-duration` | `0` | Bound of the whole run. `0` sets no bound |

With `-blobs s3` the run reads the S3 server from `DUSTPAN_TEST_S3_ENDPOINT`, `DUSTPAN_TEST_S3_BUCKET`, `DUSTPAN_TEST_S3_ACCESS_KEY` and `DUSTPAN_TEST_S3_SECRET_KEY`.
These are the same variables as `make test-s3` and the e2e suite.
Each run writes under its own key prefix.
A store that ignores `If-None-Match` fails the probe, so run it with `-s3-probe=false`.

The report holds `config`, `datasetMs`, `durationMs` and these fields:

| Field | Meaning |
| --- | --- |
| `backup` | The backup of the data set: `ran`, `ms`, `state` and `error` |
| `restore` | The restore of that backup. A restore first takes a backup of the live data, so its time holds that backup too |
| `stateUpload` | The declare of a state version and the PUT of its content |
| `stateCycle` | Lock, state upload and unlock |
| `checkpointSave` | The first checkpoint save of an update |
| `updateCycle` | Update create and start, checkpoint save and complete |

Each flow entry holds `samples`, `p50Ms`, `p99Ms`, `maxMs`, `errors` and `firstError`.
The S3 latency benchmark (`make bench-s3`) runs this mode for each network profile.
See [S3 latency benchmark](../reference/s3-latency-benchmark.md).

## Measure several instances

The scale mode runs real `opendustpan serve` processes, not in-process servers.
It starts `-instances` processes of one installation on one PostgreSQL database and one S3-compatible bucket.
It puts a round-robin balancer in front of them.
The balancer checks `/health/ready` of each instance every 250 ms, sends each request to the next healthy instance, and never retries a request.
It answers 503 with `Retry-After: 1` when no instance is healthy.

```sh
go build -o /tmp/opendustpan ./cmd/opendustpan
DUSTPAN_TEST_POSTGRES_URL='postgres://<user>:<password>@localhost:5432/<empty database>' \
DUSTPAN_TEST_S3_ENDPOINT=http://127.0.0.1:9000 DUSTPAN_TEST_S3_BUCKET=<bucket> \
DUSTPAN_TEST_S3_ACCESS_KEY=<key> DUSTPAN_TEST_S3_SECRET_KEY=<secret> \
go run ./load -flows scale -binary /tmp/opendustpan -instances 3 -clients 32 -units 500 -out scale-report.json
```

Use a new, empty database for each run.
The mode makes the namespace, seeds `-units` units, and makes one workspace for each client.
Each client then repeats one cycle until the run stops:

1. Create a stack, then run one Pulumi update on it: create, start, one checkpoint save, complete.
2. Run one OpenTofu apply on its own workspace: lock, declare a state version, upload the state, unlock.
3. Read one page of 100 workspaces, and read the inventory.

These flags change the mode:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-binary` | (none) | The path of the `opendustpan` binary. Required |
| `-instances` | `1` | The number of server processes |
| `-clients` | `16` | The number of simulated users |
| `-warmup` | `10s` | The time of load before the measurement |
| `-duration` | `60s` | The time of the measurement |
| `-think` | `0` | The wait of each client between two cycles. With `0` the clients run as fast as they can |
| `-restart` | `false` | After the measurement, stop instance 0 with SIGTERM, start it again, and keep the load for `-restart-tail` |
| `-restart-tail` | `20s` | The time of load after the restarted instance is ready |
| `-store-pid` | `0` | The process ID of the object store, to measure its CPU |
| `-units` | `10000` | The units to seed. Use a smaller number, for example 500 |

The report holds these fields:

| Field | Meaning |
| --- | --- |
| `machine` | CPU count and model, memory, operating system, architecture and Go version of the host |
| `instances`, `clients`, `thinkMs`, `units` | The settings of the run |
| `steady` | The requests that began in the measurement window: `requests`, `errors`, `errorRate`, `requestsPerSec`, `errorStatuses` and one entry in `routes` for each route |
| `resources` | The CPU in cores, averaged over the window, of the server processes, PostgreSQL, the object store and the harness. Also the client connections to PostgreSQL (mean and maximum), the transactions each second, and the CPU cost of 1000 requests in core-milliseconds |
| `restart` | With `-restart`: the stop time and the start time of the instance. Also the requests `during` the restart (SIGTERM to ready) and `after` it. The mode sets `stopError` or `startError` when the stop or the start failed. The report is then not the report of a restart |
| `clientErrors` | The client calls that failed, over the whole run |

Each entry of `routes` holds `requests`, `errors`, `p50Ms`, `p95Ms`, `p99Ms` and `maxMs`.
A request is an error when the connection fails or the status is 400 or above.
The times run from the start of the request to the response headers.
The harness groups the requests by what the server does, for example `tofu lock` and `pulumi checkpoint save`, not by the name of a unit.

The mode reads the CPU time of the processes from `/proc`, so it runs on Linux only.
A field that nobody measured is absent from `resources`. A field whose read failed at the start or at the end of the window is absent too. `resources.unmeasured` names each absent field. A value of 0 is a measured 0. The PostgreSQL CPU needs two things. The data directory that the database reports must hold a readable `postmaster.pid`. The database must also run in the process namespace of the harness. The object store CPU needs `-store-pid`. The mode stops at the start when it cannot read `/proc`.
The mode starts each server process as the leader of its own process group, and a stop sends SIGTERM to the group. Known limit: if you send SIGKILL to the harness, the server processes stay running and you must stop them yourself. A SIGINT or a SIGTERM to the harness stops them.
The harness, the balancer, the server processes, PostgreSQL and the object store share the CPUs of one host.
So the numbers depend on that host.
The [measured sizing page](measured-sizing.md) shows a run and its limits.

## What this guide does not cover

These items are not part of the harness:

- Budget thresholds for the nightly job.
- Real CLI runs against the harness server.
- Runner protocol routes that need a joined runner, and the start of a job through a pool.
- Fault injection and crash testing.
