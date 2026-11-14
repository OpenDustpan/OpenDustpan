## Context

Shared core pkg/manuallock holds the lock check; tool adapters, job dispatch and REST routes call it. Namespace locks widen coverage to every unit of the namespace.

## Decisions

1. **One coverage check for every caller.** The service exposes Covered(tool, namespace, unit) backed by one query that checks unit, collection and namespace rows; callers pass the stable namespace ID and unit reference, never a display name.
2. **Dispatch check at claim time.** Job dispatch reads the lock in the same transaction that claims the next queued phase; a covered unit keeps state queued with reason locked and keeps its queue timeout.

## Structure

Unexported lockSet built only by manuallock.Coverage(store, fence); the constructor loads unit, collection and namespace rows in one transaction so no caller can check one level only.

## Risks

- A running job that has not taken its tool lock is refused unless the job-token exception is exact.
- Namespace lock fans out to many units in inventory and bulk answers.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Port adapter rejects a null or overlong reason and an unknown object kind. | Send empty reason and 501-character reason directly to the adapter; both fail. |
| service | Service checks coverage for unit, collection and namespace rows, orders phase start time against lock time with fail-closed on equal or missing times, and allows a job-token tool lock only when its phase started before the lock. | Cover a unit after phase start then send a fresh tool lock; it fails while the pre-lock phase token passes; a lock written between claim and tool lock refuses the tool lock when the phase start time is equal or missing. |
| HTTP handler | Handler checks unit:lock on the object of the lock and writes the audit entry with holder and reason, including denials. | Call lock without unit:lock on the namespace; the server answers 403 and writes a denial. |
