# Probe a long and a short database outage

The database outage probe checks that OpenTofu and the Pulumi CLI recover
after PostgreSQL stops. You run the probe in the high availability end-to-end
suite.

## Long outage

Before each outage, the suite stops PostgreSQL, checks that each instance
refuses a write with `503`, and starts PostgreSQL again. If a write is not
refused, the outage test fails before it starts a CLI command.

The suite runs one OpenTofu apply and one Pulumi up through the proxy. Then
the suite stops PostgreSQL for 60 seconds.

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

The suite measures the smallest retry window from the long outage, in the
same run.
It sets the short stop to 50 percent of that smallest window.

During the short stop, the suite waits until the commands are near their
state writes, then stops PostgreSQL for the short window. Both commands must
end with exit code `0`. The next `pulumi preview --expect-no-changes` and
`tofu plan -detailed-exitcode` must show no changes.

If the long outage measures no retry window, both the long outage test and
the short outage test fail. A version that has only one refused write has no
retry window. The suite does not use a default window.

## Commands

Run the two outage tests with PostgreSQL and at least two instances. Set
`DUSTPAN_TEST_POSTGRES_URL` to the URL of your PostgreSQL database:

```sh
E2E_INSTANCES=2 go test -tags e2e -run TestDatabaseOutage ./e2e/ha/
```

The probe runs on Linux only. It skips when `E2E_INSTANCES` is below 2, and
when `DUSTPAN_TEST_POSTGRES_URL` has no value.

## Check the results

The retry report lists each CLI version and the seconds it retried one state
write in the long outage. A version with no refused write shows
`not measured`. The short outage uses that report in the same run.

## References

- [Recover from a database outage](database-outage-recovery.md)
- [High availability](high-availability.md)
