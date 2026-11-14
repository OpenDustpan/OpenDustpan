## Context

The test forces a PostgreSQL deadlock between the restore swap and a conflicting transaction and expects the swap to lose. PostgreSQL may pick either side.

## Decisions

1. **Retry until swap is victim.** Retry the setup up to 10 attempts. Victim identity comes from which handle returns 40P01: the conflicting handle means wrong victim, so roll back, close and retry; the swap handle means right victim, so the assertion runs. Any other error, timeout or connection loss fails at once. Exhausted bound fails with the attempt count and last victim identity. No lock_timeout. No product code change. Lock ordering is not used because it would remove the deadlock under test.

## Structure

Test-only change in internal/servertest: an unexported helper that opens the conflicting transaction and the swap on fresh connections for each attempt, classifies the victim, rolls back and closes both handles on a wrong victim, and repeats with bound 10.

## Risks

- A retry loop that retries any error would hide a real swap bug; the strict filter retries only wrong-victim 40P01.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[internal/servertest]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Test-only change; no latency, storage or rate target changes. |  |  |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Open conflicting transaction then run restore swap. | PostgreSQL picks the conflicting transaction as the deadlock victim. | Roll back both, close both connections, repeat on fresh connections, up to 10 attempts. | No swap committed; next attempt starts clean. |
| Exhaust retry bound. | All 10 attempts end with the wrong victim. | No further retry; fail with attempt count 10 and last victim identity. | Both handles rolled back and closed; the test fails. |
| Inspect which handle got the error. | Lock timeout, connection loss, context cancel or a non-40P01 error. | No retry; roll back, close and fail with the error. | Both handles rolled back and closed; the test fails. |
