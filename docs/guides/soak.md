# Run the soak harness

This guide shows how to run the soak harness and how to read its reports.

## What the run does

The run starts one server binary and drives many real Pulumi CLI and
OpenTofu processes against it for a set duration.
Each worker holds one stack or one workspace and repeats a small change:
`pulumi up` with a changed output, or `tofu apply` with a changed output.
The driver samples the server every 10 seconds and writes a CSV of the
samples with a short Markdown verdict.

Each sample holds goroutines and heap bytes from `GET /api/server/metrics`,
open file descriptors of the server process from `/proc/<pid>/fd`, and the
error rate of CLI runs that ended in the interval.

The database connection series always stays empty.
The server exports no database pool gauge, and neither store exposes
`sql.DBStats`, so no gauge was added.
Its verdict is always "no data" and never fails the run.

## Run it

Run the full run with the make target:

```sh
make soak
```

This runs 4 Pulumi workers and 4 OpenTofu workers for 30 minutes against
SQLite, and writes timestamped CSV and Markdown files to `soak-reports/`.

Prove the harness with a short run:

```sh
make soak SOAK_DURATION=60s
```

Set the worker counts and the report directory:

```sh
make soak SOAK_DURATION=3m SOAK_PULUMI=2 SOAK_TOFU=2 SOAK_REPORT_DIR=my-reports
```

Run it against PostgreSQL with the test URL:

```sh
DUSTPAN_TEST_POSTGRES_URL=postgres://user:pass@localhost:5432/dustpan make soak
```

With the URL set the run uses PostgreSQL, else it uses SQLite.
One run covers one store.
Point the URL at an empty database.
Each run uses a fresh schema, so runs never share data.

The run needs Linux for the OpenTofu workers.
Off Linux the Pulumi workers still run, and the log says why the OpenTofu
workers stay down.
It also needs `E2E_SERVER=binary`, which the make target sets.
The first label of `E2E_PULUMI_VERSIONS` and `E2E_TOFU_VERSIONS` selects the
CLI releases.

## Growth rule

The verdict fits a least-squares slope per minute over the last two thirds
of the samples.
The first third is warm-up and never counts.
A run with fewer than 4 samples reports "too few samples" instead of a
slope.

The run fails when a slope passes its threshold, or when failed CLI runs
over total CLI runs passes 0.01:

| Metric | Threshold per minute |
| --- | --- |
| Goroutines | 1.0 |
| Heap bytes | 1048576 (1 MiB) |
| Open file descriptors | 1.0 |
| Database connections | 1.0 (no data, never fails) |

The first nightly run records values, and a person accepts them as budgets.
Until then these thresholds are starting points, not budgets.

Short runs of about a minute prove the harness only: workers, sampling
and reports. Their verdict may fail on noise: with few samples a small
change reads as a steep slope, so the verdict applies to full-length runs.

## Report files

Each run writes two files with the store and the UTC start time in the
name, for example `soak-sqlite-20260102-150405.csv` and the matching `.md`.
The test's working directory is the e2e package, so the default directory
`soak-reports/` means `e2e/soak-reports/` when run with `make soak`:

- The CSV holds one row per 10 s sample: elapsed seconds, goroutines, heap
  bytes, file descriptors, database connections (empty), CLI runs,
  failures, and the interval error rate.
- The Markdown holds the verdict per metric with its slope and threshold,
  the totals, and the overall verdict.

## The nightly run

A nightly workflow, `nightly-perf.yml`, runs this soak run on PostgreSQL with the load harness and the Toxiproxy fault tests.
It runs on a schedule and on manual dispatch, never on a pull request.
It uses no secret beyond the repository token, so a pull request from a fork does not run it.

`perf/run-soak.sh` runs `make soak` and and writes `measurements.json`, a list of `name`, `p50`, `p95`, `max`, `unit` and `scope`.
The soak entries hold the p50, p95 and max of the samples of goroutines, heap bytes, open file descriptors and error rate.
The compare program checks each measurement against `perf/budgets.json`, which holds one entry for each measurement name:

```json
{ "soak.goroutines": { "p50": 200, "p95": 300, "max": 400, "unit": "count", "scope": "soak" } }
```

A figure above its budget fails the job.
The tolerance is zero.
A measurement with no budget entry is record-only: the job passes and the artifact holds the value.
A budget entry that the run did not produce gives a warning and does not fail the job.
The job opens one issue with the title `[nightly-perf] <measurement>`.
A repeat breach adds a comment to that issue.
The job closes the issue when the measurement is within its budget again.
A run that ends with no measurement fails and files no issue.
A service container, the harness or a timeout failed, and the job log names the step.

Read a failed run with `gh run view <run-id>`.
After a fix, run the workflow again:

```sh
gh workflow run nightly-perf.yml
```

To accept the first values as budgets, copy them from the `measurements.json` artifact into `perf/budgets.json` in a pull request that a person reviews.
No job changes a budget.
Nightly figures come from shared runners, so they are never a service level agreement.

## What this guide does not cover

These items are not part of the harness:

- Fault injection and crash testing.

The harness reuses the e2e helpers, but not the load/ helpers:
synthetic replay does not fit a real-CLI soak.
