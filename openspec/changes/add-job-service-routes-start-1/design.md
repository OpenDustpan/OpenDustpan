## Context

Job writes go through pkg/job service over job.Store with namespace fence checks. REST routes map to service calls. Audit entries record each state change with actor and reason.

## Decisions

1. **Service owns state changes.** Service owns state changes for start, list, get, cancel, approve, reject, confirm, and redact log. HTTP handlers map request fields to service inputs and translate domain sentinels to error codes. No handler writes the store directly.
2. **Break-glass flag in REST body.** Break-glass start carries a break-glass flag and reason in the REST body. The service still applies the namespace break-freeze rule and refuses when the rule blocks the start.
3. **Plan binding and repeat calls.** Approve binds to the shown plan revision. The service compares the shown revision with the current revision and refuses a stale approval. Repeat calls with the same Idempotency-Key return the first result without a second write.

## Structure

Unexported job service struct with constructor job.NewService that checks Store, clock, and ID maker invariants; only the constructor makes a service value.

## Risks

- List route leaks jobs across units without unit scoping.
- Approve path binds approval to a stale plan.
