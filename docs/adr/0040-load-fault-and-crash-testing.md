# 40. Load, fault and crash testing, and the sizing guide

Status: accepted · 2026-10-11

## Context

The gates of `make check` and the e2e suite show that the server is correct
for one client at a time. They do not show:

- how much memory, CPU, database and blob storage an installation needs,
- what happens with many clients, large checkpoints and many locks at once,
- what happens when the database, the blob store or the network is slow or
  fails in the middle of a write,
- what happens when the server process stops in the middle of a write.

The risks of this server are not a high request rate. They are large bodies
(issue 208), contention for locks and leases, the write order of blobs and
SQL rows (ADR 26, ADR 37), several instances (ADR 38) and our own S3 client
(ADR 31).

The e2e suite records every request of the real CLIs (`e2e/recorder.go`). It
has a TCP proxy that can stop the database for one server (`e2e/dbproxy.go`).

An operator who installs OpenDustpan needs an architecture guide and sizing
numbers that come from measurements, not from guesses.

The project owner accepted this approach on 2026-10-11, and asked that the
results feed the documentation on architecture and sizing.

## Decision

1. **Fault wrappers at the ports.** Test-only wrappers for the SQL store and
   the blob store fail, stall or time out at a chosen call. Tests use them for
   the crash windows of each write path, for example after the blob write and
   before the SQL commit. They need no new tool.
2. **Crash tests.** In the binary mode of the e2e suite, a test stops the
   server with SIGKILL at chosen points during a write. After the next start, it
   checks the invariants:
   - no SQL row names a missing blob,
   - no lease, lock or upload capability stays usable when it must not,
   - the server starts, and the CLI can continue or retry.
3. **Load harness in Go.** A harness replays the recorded request sequences of
   the real CLIs with many synthetic stacks and workspaces. It adds no load
   tool and no JavaScript. It measures:
   - peak memory for each checkpoint and import size,
   - time to save a checkpoint, with full uploads and with delta uploads,
   - contention: many runs on one workspace, many updates in one namespace,
   - database connections with one and with several instances,
   - latency of the list and inventory routes with 10,000 units.
4. **Soak run with real CLIs.** A run of many concurrent `pulumi up` and
   `tofu apply` processes for 30 minutes checks for leaks and errors over
   time.
5. **Network faults with Toxiproxy.** A Toxiproxy container in CI sits between
   the server and PostgreSQL, the S3-compatible server and the CLIs. Tests use
   its HTTP API for latency, bandwidth limits, timeouts and connections that
   close in the middle of a body. The Go module gets no new dependency. The e2e
   `DBProxy` can move to Toxiproxy later.
6. **Where each one runs.** Fault wrappers and Go benchmarks for hot paths
   (delta apply, gzip, the checkpoint store) run in `make check`. Crash tests
   run with the e2e suite. The load harness, the soak run and the Toxiproxy
   tests run in a nightly job, not on each pull request.
7. **Budgets.** The nightly job compares each measurement with a budget kept in
   the repository. A regression above the budget fails the job and opens an
   issue.
8. **Sizing guide.** The operations documents get an architecture and sizing
   guide. Its tables come from a load run on each release, not from nightly
   runs, so the numbers stay stable between releases. Each table names the
   release, the hardware and the date. The guide covers:
   - single instance with SQLite, and several instances with PostgreSQL,
   - memory and CPU for each instance, the database size, connections and
     the blob storage that each update adds,
   - the largest checkpoint and the number of units that a size supports.

## Consequences

- Toxiproxy runs only in CI, as a container. Contributors can run it
  locally with Docker.
- The nightly job costs CI minutes. The job is a workflow file, so a local
  agent session or a person writes it.
- The sizing guide has numbers only after the first release run. Until then,
  it says so.
- The budgets need a first run to set them. The first nightly run records
  values, and a person accepts them.
