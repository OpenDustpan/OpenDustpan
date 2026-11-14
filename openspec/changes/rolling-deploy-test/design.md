## Context

This part adds only the exit-test step for rolling deploy. Version refusal and graceful drain ship in earlier parts. The test proves old and new binaries serve together behind one proxy.

## Decisions

1. **Reuse pool and fake migration in test setup only.** Reuse the high availability e2e pool with three serve binaries behind a round-robin TLS proxy. Replace binaries one at a time while apply loops run. The test setup writes the minimum-version row directly through the test store handle before the mixed pool starts, and restores it after. No flag, env var, or server branch carries the fake.
2. **Retry refused writes inside the loop.** A refused old-binary write returns the existing error code schema.binary-below-minimum with a clear version message and no write commit. The loop treats that error as retryable on another instance and repeats the command until exit 0. Zero failed commands counts CLI exit codes, not refused attempts.
3. **Abort and restore on failed replace.** If a new binary never turns ready within 120 s, or a loop command fails twice in a row, the step aborts, restores all three old binaries, and fails the test. A proxy that keeps sending to a down instance fails the proxy-record check.

## Structure

No new server structure. The test holds pool state in an unexported testPool type built only by newTestPool, which records binary version per instance and proxy record per request.

## Risks

- Test-only minimum row leaks into server logic.
- Flaky proxy record hides which instance served a request.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Test-only change, no server latency or storage target changes |  |  |
