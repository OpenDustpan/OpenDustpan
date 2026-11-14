## Context

Job start and job claim carry review comments from prior reviews. The fixes are small corrections in the job service and the claim path, with no new API and no schema change.

## Decisions

1. **Fix the review items in place.** Keep retention in the durable idempotency store so answers survive restart and failover; fix the Route comment in place; check the source; write the refused action with actor, reason, and time; skip a poison operation for that poll and retry it on the next poll; end job and durable record both as canceled; count and log poll-bound skips and process them on later polls.

## Structure

No new type and no new constructor. The fix stays in the existing unexported sourceOf in pkg/job/service_start.go and the existing unexported bindClaim in pkg/store/internal/jobstore/apply.go, with the Route comment fixed in place.

## Risks

- A 24 hour durable retention stores more replay records.
- Ending both records as canceled changes what automation reads for canceled jobs.
