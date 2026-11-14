## Context

The e2e suite must drive each runner management route and each runner protocol step with a pinned real runner and a local launcher, with no mock runner and no retry that hides a failure.

## Decisions

1. **One real-runner flow covers all runner behavior.** One e2e flow creates a pool, mints a join token, joins a pinned real runner, runs a plan and an apply, cancels one job, drains and retires the runner, checks each route answer, each refusal, each action refusal, the Idempotency-Key replay, the claim expiry after a crash, the drain race rule and the cleanup, on SQLite and on PostgreSQL over TLS against the real binary.
2. **No retry mask in the e2e flow.** The flow uses the sandboxed process of the machine coordinator and the local launcher only, records each attempt, and fails when a step needs a retry to pass.

## Structure

No new structure. The change adds no stored reference and no new guard. It drives the current handlers in internal/api/runnerapi/routes.go through their routes, so a route without e2e cover fails the suite.

## Risks

- The pinned runner artifact can drift from the server and break the suite.
- A failed flow can leave a runner or a pool that blocks later runs.
