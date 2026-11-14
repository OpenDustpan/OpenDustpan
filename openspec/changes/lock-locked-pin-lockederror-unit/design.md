## Context

A blank LockedError.Unit turns a 409 lock.locked answer into a 500 answer because refusal.Locked fails closed on an invalid unit.

## Decisions

1. **Test-only pin of the refused unit.** Add no production code. Add one refused-unit assertion at each of the six producers named in the issue. Use distinct locked and refused objects in the Pulumi and trigger answer tests so a swap fails.

## Structure

No new type. No shared constructor on main rejects a blank unit, so per-producer test rows are the pin; a shared table is not possible because the six producers live in different packages with different fixtures.

## Risks

- A producer added later has no pin until its own test row asserts the unit.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[internal/api/refusal]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Test-only change; no latency, storage or limit changes. |  |  |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Trigger fire refused by a manual lock | LockedError.Unit is blank so refusal.Locked fails closed and the trigger fire route would answer 500 server.internal instead of 409 lock.locked. | Fix the producer to set the refused unit and re-run the refused-unit row. | Manual lock stays held; no write occurred. |
