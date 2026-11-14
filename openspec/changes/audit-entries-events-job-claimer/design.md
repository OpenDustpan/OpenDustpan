## Context

The job claimer applies runner-driven and server-driven job events. Each automatic action needs a record of who or what did it and why.

## Decisions

1. **Coverage and atomicity.** Covered events are EventClaim, EventRelease, EventPhaseFailed, and the finish events that the claimer applies. Rejected, lost-race, duplicate, and no-op events write no entry. Each covered event writes exactly one entry in the same transaction as the state commit; a failed audit write fails the event commit.
2. **Subject and object.** Subject is the stable runner ID from the claim for runner-driven events. Server-driven events use the server actor with reason lease-expiry, restart-retry, or server-release. The entry stores the job ID and namespace ID, never the runner name. A removed runner still reads back by stable ID.

## Structure

Unexported claimer audit writer with a constructor that requires the audit port; the writer maps each covered event to one audit kind.

## Risks

- Duplicate runner delivery writes a duplicate entry.
- Runner removed before read leaves an entry whose subject ID has no live runner.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| storage growth | 1 entry | entries/event | one audit entry per covered claimer event per namespace |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Apply EventClaim. | Runner retries the same claim after a timeout. | Claimer detects the duplicate and returns the prior result with no new entry. | Job stays claimed; one audit entry exists. |
| Apply EventRelease, EventPhaseFailed, or a finish event. | Runner retries the same release or finish after a timeout. | Claimer detects the duplicate and returns the prior result with no new entry. | Job holds the first terminal state; one audit entry exists. |
| Apply competing EventClaim events. | Two runners race for one job. | Loser receives a rejection and retries no write. | One holder keeps the job; only the winning claim has an entry. |
| Commit state and audit entry in one transaction. | Audit write fails during the commit. | Claimer fails the event commit and returns an error; the runner retries the event. | Job state unchanged and no entry written. |
