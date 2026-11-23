## Context

The job service takes an idempotency store. The durable store must keep a record of a job start for 24 hours, also after a restart or a failover.

## Decisions

1. **Fixed retention.** The retention of a job-start record is 24 hours. The time is a constant of the NEW Retention type.
2. **Expiry stored with the record.** The store codec writes the expiry time as Unix milliseconds. A read after the expiry gives no record.

## Structure

NEW: an unexported field of the Retention type in pkg/idempotency holds the duration. Only its constructor makes a value, and the constructor refuses a value of less than 24 hours. A caller cannot make a record with a short retention.

## Risks

- A record that lives longer makes the table larger.
- A clock difference between replicas moves the expiry.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[pkg/idempotency]
P2[internal/store/codec]
P2 --> P1
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| retention of a job-start idempotency record | 24 | hours | durable store of one installation |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Store the record | The store write fails. | The agent retries with the same key. | No job exists or the job and its record exist together. |
| Read after a failover | The new primary has no record. | The agent retries with the same key. | The server starts the job once and stores the record. |
