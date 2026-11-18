Issue: #3289.

Decision: ADR 370. Part 2 of 3 of the housekeeping principals. Part 1 is `housekeeping-principals-install` and this part depends on it. Part 3 is `housekeeping-principals`, which keeps the name of the whole work, so each change that waits for "housekeeping-principals" waits for all three parts. This part touches no file of the access token, identity and sign-in packages.

## Why

**Persona: operator.** An operator reads the audit log of an installation. Today eight housekeeping jobs write their entries as `@server` or run with no principal, and no grant decides what they may do. The operator needs each of them to run as its named principal, so that the audit log names it and a revoke of its grant stops that job alone.

The user path this part must make work:

1. **API.** The operator reads the audit log: each entry of one of these sweeps names its housekeeping principal as the actor, for example `housekeeping:artifact-sweep`, never `@server`.
2. **API.** The operator revokes the stored grant of the artifact sweep with the existing route `DELETE /api/server/principals/{principal}/grants/{id}`. The next artifact sweep changes nothing and writes a denial audit entry. The other sweeps run as before.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A sweep runs while its principal has no grant. | The audit entry `denied` names the housekeeping principal and the missing action. The sweep changes nothing. | `GET /api/server/principals/{principal}/grants` |

Evidence for the claims above:

- Housekeeping writes audit entries as `@server`, with no grant. Proof: the facts of "What main already does".

## What Changes

- NEW: each of these housekeeping services takes its principal and authorizes with the authorizer before it acts: the artifact sweep, the reconcile of jobs, the cleanup of saved views, the retention of history, the locks of local users, the reaper of agent runs, the sweep of superseded checkpoint blobs and the sweep of blobs of a workspace.
- NEW: the audit entry and the run record of each of these runs name its principal.
- NEW: a refused run changes nothing and writes an audit entry with the outcome `denied`.
- The wiring in `pkg/dustpan/dustpan.go` passes each principal to its service at construction.

Out of scope:

- The principals and their grants: part 1.
- The sweep of access tokens, the expiry of temporary administrators and the rotation of issuer keys: part 3.
- The refusal of a credential or of a grant for a housekeeping principal: part 3.
- The namespace service principal stays until the first trigger change.

Depends on:

- housekeeping-principals-install

Builds on (not a change):

- ADR 370 on main (pull request #3058)

## Usability

Where the path still costs the person time or surprises them:

- A revoked housekeeping grant stops that work until a person grants it again. The docs page of part 3 gives the grant command for each purpose.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The artifact sweep writes as `@server`. (`pkg/job/artifactsweep.go:654`)
- The reconcile of jobs writes as `@server`. (`pkg/job/reconcile.go:673`)
- The cleanup of saved views writes as `@server`. (`pkg/savedview/service.go:259 (Service.systemEntry)` and `pkg/savedview/service.go:293 (cleanupSubject)`)
- The retention of history writes as `@server`. (`pkg/history/history.go:156 (ServerSubject)` and `pkg/history/redaction.go:22 (RetentionReason)`)
- The locks of local users write as `@server`. (`internal/localusersvc/locks.go:415`)
- The reaper of agent runs. (`internal/agent/reaper.go:53 (Reap)`)
- The sweep of superseded checkpoint blobs runs with no principal. (`pkg/stack/superseded_sweep.go:72 (Updates.SweepSuperseded)`)
- The sweep of blobs of a workspace runs with no principal. (`pkg/workspace/service.go:2527 (Service.sweepUnreferenced)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 370: Housekeeping principals replace the namespace service principal** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The record of each run names it."
- Quote: "The install step writes the grants of each housekeeping principal as stored grants, with a creator and a time."
- Quote: "A revoke of the grant of one housekeeping principal stops only its own work, and the server writes the denial to the audit log."
- Effect: This part makes eight purposes run as their principals, authorize before they act, name the principal in each record, and stop alone when their grant is revoked. Part 1 made the principals and grants. Part 3 covers the other three purposes.
- Conflict: none.

**ADR 250: Defense in depth for security code** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Each layer must stop the attack alone, without help from the others."
- Effect: The authenticator, the authorizer and each housekeeping service refuse alone, each with its own hostile test.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which purposes are in this part? The eight of the design document ("Callers on main") that touch no file of the access token, identity or sign-in packages. Part 3 has the other three.
- Is `@server` removed? No. It stays as the object of a grant on the installation. A run never acts as `@server`.

Drafted by the PM from ADR 370, as part 2 of 3 of the change that was named `housekeeping-principals` alone.
