# Run the network fault tests

This guide shows how to run the network fault tests and what each case checks.

## What the tests do

Each test starts its own server on a fresh SQLite database.
It also starts a Toxiproxy process on a free port, unless `TOXIPROXY_URL` names a running server.
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

## Use a running server

Set `TOXIPROXY_URL` to use a Toxiproxy server that already runs:

```sh
TOXIPROXY_URL=http://127.0.0.1:8474 go test -tags faults -race -count=1 ./e2e/faults/...
```

With the variable set the tests use that server and start no binary.
Without it the tests start the binary named by `TOXIPROXY_BIN`, as `make faults` does.
Without either variable the tests fail and name `make faults`.

Set `TOXIPROXY_LISTEN_HOST` to change the host that new proxies listen on:

```sh
TOXIPROXY_LISTEN_HOST=0.0.0.0 TOXIPROXY_URL=http://127.0.0.1:8474 go test -tags faults -race -count=1 ./e2e/faults/...
```

The default host is `127.0.0.1`.
Use `0.0.0.0` when the Toxiproxy server runs in a container.

A Toxiproxy server in a container needs two more variables:

- `TOXIPROXY_PORT_BASE` gives each proxy a fixed port, from the base to the base plus 3.
  Publish these ports from the container to the same ports on the host.
  Without the variable, Toxiproxy picks a free port for each proxy.
- `TOXIPROXY_UPSTREAM_HOST` is the host that the proxies dial to reach the test server and PostgreSQL.
  With the variable set, the test server listens on all interfaces.
  Without it, the proxies dial `127.0.0.1` and the PostgreSQL host in the URL.

CI runs the suite this way:

```sh
docker run -d -p 8474:8474 -p 8475-8478:8475-8478 \
  --add-host=host.docker.internal:host-gateway \
  ghcr.io/shopify/toxiproxy:2.12.0
TOXIPROXY_URL=http://127.0.0.1:8474 TOXIPROXY_LISTEN_HOST=0.0.0.0 \
  TOXIPROXY_PORT_BASE=8475 TOXIPROXY_UPSTREAM_HOST=host.docker.internal \
  make faults
```

With `TOXIPROXY_URL` set, `make faults` downloads no binary.

Run the suite against PostgreSQL with the test URL:

```sh
DUSTPAN_TEST_POSTGRES_URL=postgres://user:pass@localhost:5432/dustpan make faults
```

With the URL set the run adds the cut-database case.
Without it the run keeps the SQLite cases only.
Point the URL at an empty database.
Each run uses a fresh schema, so runs never share data.
