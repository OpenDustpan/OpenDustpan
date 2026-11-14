## Context

The states and operator routes come from part 1. This part adds time-driven steps with a warning before each step. The worker owns execution through a durable claim.

## Decisions

1. **Stored schedule owns each timed step.** Each timed step is a stored schedule record with the namespace ID, the source state, the target state and the due time. Only the worker claim runs the step. A repeat poll with the same record ID makes no second transition.
2. **Stored lead notice makes repeats safe.** Each lead notice is a stored record with the namespace ID, the target state and the due time. A repeat run skips a namespace with an existing notice for the same step.
3. **Due time and notices are visible and audited.** The read-namespace answer shows the current state, the due time and the lead notice time. The audit entry of an automatic step names the schedule record and the notice record.

## Structure

Unexported type namespaceTimer with constructor newNamespaceTimer that checks the lead time and the target state. Only the constructor makes a value.

## Risks

- A missed schedule delays a timed step past its due time.
- A duplicate notice spams namespace administrators.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| schedule skew | 24 | hours | each timed namespace step |
| notice lead time | 7 | days | each timed namespace step |

## Contracts

**namespace**

- Invariant: A stored namespace reference uses the stable namespace ID.
- Invariant: A timed step runs only from a stored durable schedule record.
- Invariant: A state transition checks the current state before it writes.
- `NEW ScheduleTimedStep`: pre: The namespace exists in the source state of the step.. Post: A stored schedule record exists for the namespace ID and the target state..
- `NEW SendLeadNotice`: pre: The stored schedule record exists and the lead time is reached.. Post: One stored notice record exists for the namespace ID and the timed step..

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Run timed step from stored schedule | Worker stops before commit of the step. | The durable claim reruns the step on the next worker poll with the same record ID. | Namespace stays in the source state with the schedule record pending. |
| Send 7-day lead notice | Notice send fails. | The worker retries with the same notice record ID and makes no second record. | Schedule stays pending and the step still runs at its due time with a missed-notice mark. |
