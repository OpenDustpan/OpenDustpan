---
version: 5
---

Issue: #1374.

## Why

**Persona: namespace administrator.** A namespace administrator stops all job starts on a namespace during an incident or a change window, then ends the freeze when work can start again.

The user path this change must make work:

1. **API.** Create a freeze on a namespace with a reason through POST on the freeze collection.
2. **API.** Start a job during the freeze and read the 403 refusal with the one list command.
3. **API.** List freezes of the namespace and read the active freeze and its reason.
4. **API.** End the freeze through the end route and start the job again.
5. **Docs page.** Follow the freeze page to create, list, and end a freeze.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 403 freeze active refuses POST job start during a freeze | The server refused the job start because the namespace has an active freeze. List the freezes to read the reason, then wait for the freeze to end or ask a namespace administrator to end it. | `GET /api/dustpan/v1/namespaces/{namespace}/freezes` (new route of this change) |
| 403 no freeze action refuses freeze create or end without the action | The server refused the freeze action because the principal lacks the freeze manage action on the namespace. Ask a namespace administrator for a grant. | `GET /api/dustpan/v1/namespaces/{namespace}/freezes` (new route of this change) |
| 403 no break-freeze refuses break-glass start during a freeze | The server refused the start because break-freeze is missing. Ask a namespace administrator for the break-freeze grant or wait for the freeze to end. | `GET /api/dustpan/v1/namespaces/{namespace}/freezes` (new route of this change) |

Evidence for the claims above:

- The freeze record and port are in package pkg/job. Proof: package pkg/job where pkg/job/gate.go
- The pack names a freeze test on main. Proof: test TestFreeze where pkg/job/gate_test.go:764
- The route table already holds job routes on namespaces. Proof: route ANY/GET/POST /api/dustpan/v1/namespaces/{}/jobs where route table or API description

## What Changes

- New route POST /api/dustpan/v1/namespaces/{namespace}/freezes creates a freeze with reason, creator subject, and time, with the freeze manage action on the namespace
- New route POST /api/dustpan/v1/namespaces/{namespace}/freezes/current/end ends the active freeze with end record, with the freeze manage action on the namespace
- New route GET /api/dustpan/v1/namespaces/{namespace}/freezes lists freezes in time order with active state, with the namespace read action on the namespace
- Freeze guard on job start, cancel, and phase dispatch with break-glass start through break_freeze_reason for break-freeze holders on the namespace
- Audit entries for freeze create, freeze end, and break-glass start

Out of scope:

- Job lifecycle routes with cancel
- TFE link for remote execution
- Runner session routes for steps, logs, plans, and phase finish
- Launcher binary modes
- Freeze schedules and expiry

Depends on:

- add-job-service-routes-start-1
- `add-job-service-routes-start-1` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The freeze list shows the active freeze, its reason, its creator, and its time, so the team knows why starts stop.
- A refused start names the freeze and the one list command, so the starter knows the next step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Freeze test exists on main (`pkg/job/gate_test.go:764 (func TestFreeze)`)
- Route action classification test exists on main (`pkg/access/access_test.go:493 (func TestActionsWithoutRoutesAreUnclassified)`)
- Session renew path is registered in runnerapi routes (`internal/api/runnerapi/routes.go:171 (func Handler.launcherRoutes)`)
- Launcher session client posts to session paths (`pkg/launcher/session/session.go:237 (func Open)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change include job lifecycle or runner session routes? No. The roadmap split for this part covers freeze create, end, and list routes with guard checks on job start and on other mutating job actions such as cancel and phase dispatch. Job lifecycle routes with cancel, the TFE link, runner session routes, and the launcher binary stay in the sibling change and out of scope here.
- Does freeze already exist on main? In part. The record (`Freeze`, pkg/job/gate.go:449), the port (`FreezeStore`: CreateFreeze, EndFreeze, ListFreezes, pkg/job/gate.go:494), the table job_freezes and the start refusal (internal/api/jobs/start.go:193, reasonFrozen) exist, and TestFreeze is at pkg/job/gate_test.go:764. The REST routes and the single-active-freeze rule do not exist. The change adds the routes in the jobs adapter, the rule, and the guard on cancel and phase dispatch. internal/freeze is the schedule package and is not the record.
- Which ADR governs freeze? The pack adrs list is empty and names no ADR. No quoted decision applies, so the check is empty.
- Does the freeze guard stop actions other than job start? No. The guard stops job start and other mutating job actions (cancel and phase dispatch or claim) during an active freeze. Reads, list, and log reads keep working.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
