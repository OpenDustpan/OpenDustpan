# Run the network fault tests

This guide shows how to run the network fault tests and what each case checks.

## What the tests do

Each test starts its own server on a fresh SQLite database and a
Toxiproxy process on a free port.
The client sends its requests through a Toxiproxy proxy.
Toxiproxy is a test-only tool.
It never ships in the server binary.

The cases are:

- Latency: 500 ms of latency on the health path.
  The request still succeeds and takes at least 500 ms.
- Timeout: a toxic that stops data.
  The request fails with a timeout error.
  The server answers health again after the toxic is removed.
- Cut body: a `limit_data` toxic on a large checkpoint upload.
  The upload fails.
  The export still reads the old version.
- Cut database: a proxy in front of PostgreSQL that is cut during a write.
  The write fails.
  The next write after the toxic is removed succeeds.

## Run them

Run the suite with the make target:

```sh
make faults
```

The target downloads the pinned Toxiproxy binary, checks its SHA-256,
and runs the tests with the binary path in `TOXIPROXY_BIN`.
The tests carry the `faults` build tag.
Plain `go test ./...` and `make check` skip them.

Run the suite against PostgreSQL with the test URL:

```sh
DUSTPAN_TEST_POSTGRES_URL=postgres://user:pass@localhost:5432/dustpan make faults
```

With the URL set the run adds the cut-database case.
Without it the run keeps the SQLite cases only.
Point the URL at an empty database.
Each run uses a fresh schema, so runs never share data.
