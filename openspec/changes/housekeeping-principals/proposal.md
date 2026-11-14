Decision: ADR 370. Order: this change lands before the changes of triggers, schedules and drift checks of milestones 5 and 6 (run-triggers, add-schedule-triggers-due-claim, start-plan-on-push-review, write-state-version-unit-state-follows-trigger-start, job-service-unit-settings-pool), or in the same pull request as the first of them.

## Why

**Persona: operator.** An operator reads the audit log and the access review of an installation. Today the housekeeping of the server (the sweeps, the retention, the cleanup of artifacts and saved views, the expiry of temporary administrators) writes its entries as `@server`, and no grant decides what it may do. The namespace service principal starts plans on a rule in code. The operator cannot see which grant allowed a sweep, and cannot stop one sweep without stopping the server. The operator needs each sweep to run as a named principal with stored grants that the operator can list, audit and revoke.

The user path this change must make work:

1. **API.** The operator lists the principals of the installation: `GET /api/dustpan/v1/principals?kind=service&source=builtin`. The answer lists one housekeeping principal for each purpose, for example `housekeeping:artifact-sweep`, with its stable ID.
2. **API.** The operator lists the grants of one housekeeping principal: `GET /api/dustpan/v1/principals/{principal}/grants`. Each grant has a role, an object, a creator (the install) and a time.
3. **API.** The operator reads the audit log: each entry of a sweep names the housekeeping principal as the actor.
4. **API.** The operator revokes the grant of the artifact sweep. The next artifact sweep is refused and writes a denial audit entry. The other sweeps run as before.
5. **Docs page.** `docs/explanation/housekeeping-principals.md` lists each housekeeping principal, its purpose, its role and its schedule, and how to revoke and grant again.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A sweep runs while its principal has no grant. | The audit entry `denied` names the housekeeping principal and the missing action. The sweep changes nothing. | `GET /api/dustpan/v1/principals/{principal}/grants` |
| A person tries to sign in or make a token as a housekeeping principal. | 403 `principal.housekeeping`. | `GET /api/dustpan/v1/principals/{principal}` |
| A grant to a housekeeping principal names a role that its purpose does not need. | 422 `access.grant-out-of-purpose`, with the purpose. | `GET /api/dustpan/v1/principals/{principal}` |

Evidence for the claims above:

- The authorizer decides the namespace service principal on a rule in code, before every other rule. Proof: `pkg/access/authorizer.go:119` and `pkg/access/authorizer.go:154 (authorizeNamespaceService)`.
- Housekeeping writes audit entries as `@server`, with no grant. Proof: `rg -n 'history.ServerSubject|SweepSubject|cleanupSubject' pkg internal --type go` lists the places in "What main already does".

## What Changes

- The namespace service principal is removed: its subject, its authorizer rule, its guard, its checks in the trigger and job start path, and its glossary term.
- NEW: one built-in service principal for each housekeeping purpose, made at the install, with the source `builtin`. A person cannot sign in as one, and no access token names one.
- NEW: the install step writes the grants of each housekeeping principal as stored grants, with the install as the creator and the install time. A role for each purpose holds only the actions that the purpose needs.
- NEW: each housekeeping run checks its action with the authorizer, as any principal does, and names its principal in each audit entry and run record.
- NEW: a revoke stops only the purpose of that principal.
- A docs page.

Out of scope:

- The run-as principal of an automation that a person configures (ADR 369 decision 7). The trigger changes build it.
- A route to make a new housekeeping principal. The set is fixed by the code, and only their grants are data.

Depends on:

- ADR 370 on main (pull request #3058)

## Usability

Where the path still costs the person time or surprises them:

- A revoked housekeeping grant stops that work until a person grants it again. The docs page gives the grant command for each purpose.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The subject prefix of the namespace service principal. (`pkg/identity/namespace_service.go:21 (NamespaceServicePrefix)`)
- The principal that the server builds for a start. (`pkg/identity/namespace_service.go:61 (NamespaceService)`)
- The rule in code that gives it `unit:run-plan`. (`pkg/access/authorizer.go:154 (authorizeNamespaceService)`)
- The same rule in the allowlist gate. (`pkg/identity/namespace_service.go:108 (NamespaceServiceRule.Authorize)`)
- The wiring of that rule and of the guard. (`pkg/dustpan/dustpan.go:890`, `pkg/dustpan/dustpan.go:916`)
- The guard that refuses a credential with that subject. (`pkg/principal/namespace_service_guard.go:24 (RefuseNamespaceServiceSubjects)`)
- The refusal of a grant to it. (`pkg/access/service.go:1610`)
- The dispatcher makes it for each start. (`pkg/trigger/dispatcher.go:249`)
- The trigger and job start checks. (`pkg/trigger/manager.go:459`, `pkg/trigger/trigger.go:264`, `pkg/job/trigger.go:47`, `pkg/job/trigger.go:77`)
- The artifact sweep writes as `@server`. (`pkg/job/artifactsweep.go:617`)
- The reconcile of jobs writes as `@server`. (`pkg/job/reconcile.go:673`)
- The cleanup of saved views writes as `@server`. (`pkg/savedview/service.go:292 (cleanupSubject)`)
- The sweep of access tokens writes as `@server`. (`pkg/accesstoken/policy.go:197 (SweepEntry)`)
- The expiry of temporary administrators writes as `@server`. (`pkg/tempadmin/service.go:148 (Service.Sweep)`)
- The retention of history writes as `@server`. (`pkg/history/history.go:156 (ServerSubject)`)
- The locks of local users write as `@server`. (`internal/localusersvc/locks.go:415`)
- The reaper of agent runs. (`internal/agent/reaper.go:53 (Reap)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 370: Housekeeping principals replace the namespace service principal** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The namespace service principal is removed (owner, 2026-11-14)."
- Quote: "The install step writes the grants of each housekeeping principal as stored grants, with a creator and a time."
- Quote: "A revoke of the grant of one housekeeping principal stops only its own work, and the server writes the denial to the audit log."
- Effect: This change builds all four decisions.
- Conflict: none.

**ADR 338: The namespace service principal holds a stored grant** (owner-tagged (all decisions)).

- Decisions that apply: none (superseded).
- Changed by: ADR 369 changes decision 3; ADR 370 supersedes. Chain to the end: ADR 369, ADR 370.
- Current rule: ADR 370.
- Quote: "The permission is a stored grant (owner, 2026-11-07)."
- Effect: The principal and its grant are removed. The rule of ADR 338 that a permission is a stored grant now holds for each housekeeping principal.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decisions 1, 2, 4 and 5).

- Decisions that apply: 1 (changed).
- Changed by: ADR 325 changes decisions 1, 5 and 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "Until the first tag, a pull request changes the schema by an edit of the baseline of each dialect, in place."
- Effect: The install grants and the `builtin` source are baseline edits. No migration code.
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

- Which purposes get a principal? Each caller that today writes as `@server` or runs without a principal: the artifact sweep, the reconcile of jobs, the cleanup of saved views, the sweep of access tokens, the expiry of temporary administrators, the retention of history, the locks of local users, the reaper of agent runs, and the reapers of blobs and checkpoints. Task 0.1 checks the list on main again with `rg`.
- Does a housekeeping principal belong to a namespace? No. It is a principal of the installation. Its grants name the objects that its purpose touches.
- Is `@server` removed? No. It stays as the object of a grant on the installation (`pkg/identity/identity.go:736`). A run never acts as `@server`.
- Who is the creator of the install grants? The install, recorded as the creator with the install time, as ADR 338 decision 1 did for the namespace grant.
- What about the existing namespaces? No installation exists, so no migration (ADR 322).

Drafted by the PM from ADR 370.
