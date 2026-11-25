# Probe a long and a short database outage

The database outage probe checks that OpenTofu and the Pulumi CLI recover
after PostgreSQL stops. You run the probe in the high availability end-to-end
suite.

## Long outage

Before each outage, the suite stops PostgreSQL, checks that each instance
refuses a write and a workspace lock with `503`, and starts PostgreSQL again.
If the server does not have a part that the probe needs, the outage test
fails before it starts a CLI command. The failure message names the part:
`503 on write during the outage` or `503 on lock during the outage`.

The suite installs each Pulumi CLI and OpenTofu version of the suite matrix.
If the suite cannot install a version, the test fails and names that version. For
each version, the suite runs one OpenTofu apply and one Pulumi up through the
proxy. Then the suite stops PostgreSQL for 60 seconds.

During the stop, each instance answers `503` on `GET /health/ready`. Each API
write answers `503` with `Retry-After: 5`.

After PostgreSQL returns, each instance turns ready with no restart. If an
instance is not ready after 90 seconds, the test fails with a blocked
database restart. The
probe records how long each CLI version retried one refused state write
through the proxy. If a command did not end with exit code `0`, it must fail
with a `503` message.

The probe cancels any Pulumi update that the long stop left running. Then the
probe runs the same commands again, and `pulumi preview --expect-no-changes`
and `tofu plan -detailed-exitcode` must show no changes.

## Short outage

The suite measures the smallest retry window above 0 seconds from the long
outage, in the same run.
It sets the short stop to 50 percent of that smallest window.

During the short stop, the suite waits until the commands are near their
state writes. It stops PostgreSQL 2 seconds before the Pulumi step ends, so
that the checkpoint save at the end of the step meets the stop. The stop
lasts for the short window.

For each command, the result depends on the write kinds of its CLI version:

- If the version retries each of its write kinds (a window above 0 seconds),
  the command must end with exit code `0`.
- If the version does not retry a write kind (a window of 0 seconds), the
  command must fail with a non-zero exit code. The state must stay valid: no
  new OpenTofu state version, a Pulumi state that `pulumi stack export` can
  read, and no lock. The suite then runs the same command again. It must end
  with exit code `0` and add exactly one new state version.

After the short stop, OpenTofu has exactly one new state version. The next
`pulumi preview --expect-no-changes` and `tofu plan -detailed-exitcode` must
show no changes.

If the long outage measures no retry window above 0 seconds, both the long
outage test and the short outage test fail. The suite does not use a default
window.

## Commands

Run the two outage tests with PostgreSQL and at least two instances. Set
`DUSTPAN_TEST_POSTGRES_URL` to the URL of your PostgreSQL database:

```sh
DUSTPAN_TEST_POSTGRES_URL=postgres://localhost:5432/dustpan_e2e E2E_INSTANCES=2 \
  go test -tags e2e -run TestDatabaseOutage ./e2e/ha/
```

The run downloads the Pulumi and OpenTofu CLIs of the suite matrix.

The probe runs on Linux only. It skips when `E2E_INSTANCES` is below 2, and
when `DUSTPAN_TEST_POSTGRES_URL` has no value.

## Check the results

The retry report lists, for each CLI version and each write kind, the seconds
that the CLI retried one refused write of that kind in the long outage. The
write kinds are the Pulumi `checkpoint`, `events` and `complete` writes and
the OpenTofu `state version` write. A kind with only one refused write is not
retried, and the report shows `0s` for it. The report does not list a kind
that had no refused write. For example:

```text
retry report: pulumi 3.262.0 checkpoint: 0s, pulumi 3.262.0 events: 29s, tofu 1.12.0 state version: 30s
```

The suite measures the window in each outage separately. If a version runs in
more than one outage, the report shows its smallest window. If a version has
no refused write, the test fails and names that version. The short outage
uses that report in the same run.

## References

- [Recover from a database outage](database-outage-recovery.md)
- [High availability](high-availability.md)
