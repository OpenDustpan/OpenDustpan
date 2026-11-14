---
version: 1
---

Roadmap: `docs/roadmap.md:475` (Return 503 with Retry-After and keep live check green.).

## Why

**Persona: operator.** Operator runs two or more instances on one PostgreSQL database and must see a clean outage signal; during a database stop each instance must answer 503 with Retry-After on ready and on writes while live stays green so the load balancer moves traffic and the CLIs retry.

The user path this change must make work:

1. **API.** Operator watches GET /health/ready turn 503 and GET /health/live stay 200 during the stop
2. **CLI.** Developer runs tofu apply and pulumi up and reads the 503 message with the retry delay
3. **Docs page.** Operator reads the outage signals docs page

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 503 on GET /health/ready during a PostgreSQL stop | Instance answers 503 with Retry-After on GET /health/ready; fix the database, then run `curl -s http://127.0.0.1:8080/health/ready`. | `curl -s http://127.0.0.1:8080/health/ready` |
| 503 on a workspace write during a PostgreSQL stop | Instance answers 503 with Retry-After in the wire shape of the API; wait for the database, then run `tofu apply` again. | `tofu apply` |
| 503 on a stack write during a PostgreSQL stop | Instance answers 503 with Retry-After in the wire shape of the API; wait for the database, then run `pulumi up` again. | `pulumi up` |

Evidence for the claims above:

- Main has health tests for live checks nothing and ready OK. Proof: TestHealthLiveStillChecksNothing at pkg/dustpan/health_test.go:176 and TestHealthReadyOK at pkg/dustpan/health_test.go:193
- Main has TFE state version and lock handlers that this part maps to 503. Proof: Handler.writeStateVersion at internal/api/tfe/states.go:602, Handler.lockWorkspace at internal/api/tfe/locks.go:103, Handler.unlockWorkspace at internal/api/tfe/locks.go:216
- Main has a Pulumi HTTP adapter package that this part maps to 503. Proof: package internal/api/pulumi

## What Changes

- Return 503 with Retry-After on GET /health/ready during a PostgreSQL stop
- Return 503 with Retry-After on database writes in tool-neutral, Pulumi, and TFE-compatible shapes
- Keep GET /health/live at 200 with no store check and bound request time during the stop

Out of scope:

- Atomic state version and lock commits with fencing, handed to database-outage-atomic-state-locks; this part sends 503 but does not promise no half-written state version and no held lock
- Long and short outage e2e steps with retry measurement, handed to database-outage-e2e-probe
- Restore, backup, schedules, Helm replicas

Depends on:

- database-outage-atomic-state-locks
- database-outage-e2e-probe

## Usability

Where the path still costs the person time or surprises them:

- During a database stop the CLI shows a 503 message with one retry delay; the operator reads ready versus live from one docs page.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Store finalizes a state version. (`pkg/store/postgres/workspaces.go:661 (func Store.FinalizeStateVersion)`)
- TFE adapter writes state versions. (`internal/api/tfe/states.go:602 (func Handler.writeStateVersion)`)
- TFE adapter locks a workspace. (`internal/api/tfe/locks.go:103 (func Handler.lockWorkspace)`)
- TFE adapter unlocks a workspace. (`internal/api/tfe/locks.go:216 (func Handler.unlockWorkspace)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: task "Add failing server test for ready 503 on GET /health/ready" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>".

Questions considered and answered:

- Which ADR controls 503 with Retry-After during a database outage? No ADR in the pack touches health probes or 503 shape; the pack lists zero ADRs and zero RFCs, so the roadmap step controls the scope.
- Does this part make state version and lock writes atomic? No. This part returns 503 and keeps live green with a fixed 30 second Retry-After; atomic commits belong to database-outage-atomic-state-locks.
- Which Retry-After delay in seconds does the server send during a PostgreSQL stop? 30 seconds on ready and on writes; one fixed value keeps proxies and CLIs in step and is visible in the header.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
