---
version: 7
---

Issue: #3931.

Roadmap: `docs/roadmap.md:475` (Return 503 with Retry-After and keep live check green.).

## Why

**Persona: operator.** Operator runs two or more instances on one PostgreSQL database and must see a clean outage signal; during a database stop each instance must answer 503 with Retry-After on ready and on writes while live stays green so the load balancer moves traffic and the CLIs retry.

The user path this change must make work:

1. **API.** Operator watches GET /health/ready turn 503 and GET /health/live stay 204 during the stop
2. **CLI.** Developer runs tofu apply and pulumi up and reads the 503 message with the retry delay

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 503 on GET /health/ready during a PostgreSQL stop | Instance answers 503 with Retry-After on GET /health/ready; fix the database, then run `curl -s http://127.0.0.1:8080/health/ready`. | `curl -s http://127.0.0.1:8080/health/ready` |
| 503 on a workspace write during a PostgreSQL stop | Instance answers 503 with Retry-After in the wire shape of the API; wait for the database, then run `tofu apply` again. | `tofu apply` |
| 503 on a stack write during a PostgreSQL stop | Instance answers 503 with Retry-After in the wire shape of the API; wait for the database, then run `pulumi up` again. | `pulumi up` |

Evidence for the claims above:

- Main has health tests for live checks nothing and ready OK. Proof: TestHealthLiveStillChecksNothing at pkg/dustpan/health_test.go:176 and TestHealthReadyOK at pkg/dustpan/health_test.go:193
- Main has TFE state version and lock handlers that already map a store outage to 503. Proof: Handler.createStateVersion of the TFE adapter, Handler.lockWorkspace at internal/api/tfe/locks.go:134, Handler.unlockWorkspace at internal/api/tfe/locks.go:266, and its Handler.writeError
- Main has a Pulumi HTTP adapter that already maps a store outage to 503. Proof: Handler.writeError at internal/api/pulumi/pulumi.go:1260
- Main answers a store outage with 503 and Retry-After 5 seconds in each API. Proof: problem.SetStoreRetryAfter and the constant problem.StoreRetryAfter (5 seconds) in package problem
- Main answers GET /health/live with 204 and GET /health/ready with 503 and no Retry-After when the store ping fails. Proof: the handler of GET /health/live at pkg/dustpan/dustpan.go:4130 and readiness.ServeHTTP at pkg/dustpan/health.go:288

## What Changes

- Add the Retry-After header to the 503 answer of GET /health/ready when the store check fails (main sends none)
- Use the same Retry-After value as the APIs (5 seconds, `problem.StoreRetryAfter`), through `problem.SetStoreRetryAfter`
- Keep GET /health/live at 204 with no store check (main answers 204, not 200)

Out of scope:

- Atomic state version and lock commits with fencing, handed to database-outage-atomic-state-locks; this part sends 503 but does not promise no half-written state version and no held lock
- Long and short outage e2e steps with retry measurement, handed to database-outage-e2e-probe
- Restore, backup, schedules, Helm replicas

Depends on:

- none

Removed from Depends on (14 Nov 2026, not a code dependency):

- database-outage-atomic-state-locks: a later part of the same split, handed over by this change
- database-outage-e2e-probe: a later part of the same split, handed over by this change

## Usability

Where the path still costs the person time or surprises them:

- During a database stop the CLI shows a 503 message with one retry delay; the operator reads ready versus live from one docs page.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- TFE adapter creates state versions and maps a store outage to 503 through writeError. (`internal/api/tfe/locks.go:134 (func Handler.lockWorkspace)`)
- TFE adapter locks a workspace. (`internal/api/tfe/locks.go:134 (func Handler.lockWorkspace)`)
- TFE adapter unlocks a workspace. (`internal/api/tfe/locks.go:266 (func Handler.unlockWorkspace)`)
- Readiness answers 503 on a failed store ping with a 2 second bound and no Retry-After. (`pkg/dustpan/health.go:288 (func readiness.ServeHTTP)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

ADR 298 keeps Retry-After 5; this change adds it to the ready 503.

## Open questions

none

Questions considered and answered:

- Which ADR controls 503 with Retry-After during a database outage? ADR 298 sets the 503 shape and Retry-After 5 on main; no ADR sets the health probe shape, so the roadmap step controls the rest of the scope.
- Does this part make state version and lock writes atomic? No. This part returns 503 and keeps live green with Retry-After 5; atomic commits belong to database-outage-atomic-state-locks.
- Which Retry-After delay in seconds does the server send during a PostgreSQL stop? 5 seconds on ready and on writes, the value that main sends on writes (ADR 298); the ready 503 gets the same header.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
