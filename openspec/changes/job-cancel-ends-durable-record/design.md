## Context

The pack shows DurableRecorder (pkg/job/recorder.go:56) and newDurableRecorder (pkg/dustpan/runners.go:226). The job and its durable record can end in two separate writes. A failure between them leaves one open.

## Decisions

1. **One transaction.** Each end of a job as canceled writes the job state and the durable record in the same transaction. This is the same rule that the state write follows with startDurableOperation (internal/store/postgres/state_close.go:91).

2. **Running jobs go to canceling.** A cancel of a running job follows the state machine (pkg/job/state.go: Running with EventCancel gives Canceling) and the durable store contract (a cancel of a running operation only records the request). The executor ends both records later, in one transaction.

## Structure

NEW: one store method on the job port that ends a job as canceled and ends its durable record together. The cancel of a job with no executor and the end by the executor of a canceling job both call it. Callers have no separate way to end the record of a job on the cancel path.

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
