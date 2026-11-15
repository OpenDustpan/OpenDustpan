## Context

The pack shows DurableRecorder (pkg/job/recorder.go:56) and newDurableRecorder (pkg/dustpan/runners.go:226). The job and its durable record can end in two separate writes. A failure between them leaves one open.

## Decisions

1. **One transaction.** The cancel writes the job state and the durable record in the same transaction. This is the same rule that the state write follows with startDurableOperation (pkg/store/postgres/state_close.go:91).

## Structure

NEW: one store method on the job port that ends a job as canceled and ends its durable record together. Callers have no separate way to end the record of a job on the cancel path.

## Risks

- A store adapter that does not support the joint write fails the conformance suite.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| Consistency of the cancel | 0 jobs that end canceled with an open durable record | jobs | each cancel on SQLite and PostgreSQL |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Cancel transaction | The database write fails. | The client sends the cancel again. | The job and the durable record are unchanged. |
