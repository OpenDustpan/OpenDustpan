## Context

First registry slice stores providers and modules without section H guards. This change adds guards at the service boundary with stored counters and installation settings.

## Decisions

1. **Atomic SQL guards with row lock and streaming byte count.** Enforce all four caps in pkg/registry before blob completion: one atomic conditional increment of the hourly counter in the same transaction as the upload row; SELECT FOR UPDATE on the address head row, then count check, then insert; count bytes while streaming and abort at the cap.
2. **Fixed window, per-request settings read, and pruning.** The window starts at each UTC hour from database time. The 429 answer carries Retry-After. Settings keys are registry.uploadsPerHour, registry.versionsPerAddress, registry.maxFileBytes, registry.maxReleaseBytes, read per request with no cache. Lowering a cap below stored data stays allowed. Counter rows older than 2 hours are deleted in the same write transaction.

## Structure

Unexported limitChecker in NEW file pkg/registry/limits.go, built only by newLimitChecker from validated settings; the constructor refuses unknown keys, values below 1 and values above bounds.

## Risks

- Fixed window lets a burst span two windows
- Lock wait on the address head row under concurrent publish

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[pkg/registry]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Limits only refuse excess work with no latency target change |  |  |

## Contracts

**registry address**

- Invariant: A published version never changes
- Invariant: A stored version count never exceeds its limit under concurrent publish
- `PublishVersion`: pre: Caller holds registry:publish on the namespace; The address head row is locked with SELECT FOR UPDATE before the count check. Post: If the address holds the cap the call refuses with registry.version-limit and stores no version.
- `RecordUpload`: pre: Caller holds registry:publish on the namespace; The counter increments atomically in the transaction of the upload row. Post: If the principal reached the hourly cap the call refuses with registry.upload-rate-limited and stores no blob; A later failure rolls back the count.

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Atomic increment of the hourly counter | The transaction fails or conflicts | The client sends the upload again | The count is unchanged |
| Blob write after the increment | The blob write fails | The client sends the upload again | The transaction rolls back and the count is released; partial blob is deleted |
| Publish under the address head lock | The publish transaction fails | The client publishes again | No new version is stored and the version count is unchanged |
