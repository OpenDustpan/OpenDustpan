---
version: 5
---

Roadmap: `docs/roadmap.md:319` (Run triggers).

## Why

**Persona: developer.** Developer runs plans from a push, a schedule and a follows link. The developer creates a trigger on a stack or workspace. The server starts a plan job on each event and never starts an apply. The developer reads the plan and the record of the start.

The user path this change must make work:

1. **Docs page.** Open the run triggers guide and list the triggers of the stack or workspace.
2. **API.** Create a review-request-push, schedule or follows trigger on the unit.
3. **E2e.** Fire the trigger through POST .../triggers/{trigger_id}/fire with an access token of a person and read the started plan job.
4. **API.** Read the audit entry of the trigger start through the API.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| create trigger without grant | 403 manage action needed on the stack or workspace | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| save above the per-unit or per-source limit | 422 trigger.invalid: unit holds 20 triggers, or source holds 50 followers | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| save of a follows trigger that makes a cycle | 409 trigger.cycle: the loop names units a -> b -> a in order | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |
| fire of a disabled trigger | 409 trigger.disabled: the trigger is disabled | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |

Evidence for the claims above:

- A run triggers guide exists and this change extends it Proof: docs/guides/run-triggers.md
- The trigger domain package exists Proof: pkg/trigger
- The trigger HTTP adapter exists Proof: internal/api/triggers
- The trigger store exists Proof: pkg/store/internal/triggerstore
- A trigger e2e file exists Proof: e2e/triggers_test.go
- The cycle error code exists Proof: internal/errcode/catalog_trigger.go:7

## What Changes

- Declared auth actions and audit entries on the trigger routes
- Plan-only start through the job service for automatic and manual fires
- Save-time follows cycle refusal with the ordered unit path, plus shape and limit refusals
- Extended guide page for run triggers

Out of scope:

- Push intake, webhook signature and replay checks
- Schedule tick, due claim, catch-up and deduplicate
- Follows chain execution, depth guard and outputs-changed filter
- Creation of the stored grant of the namespace service principal
- Output sharing rules and secret output reads
- Pull request automation first slice comment and status
- Apply after the merge and pre-merge apply option
- Write-back commits
- CLI-driven remote execution
- Run order across units

Depends on:

- add-job-service-routes-start-1
- job-service-unit-settings-pool
- runners

## Usability

Where the path still costs the person time or surprises them:

- A cycle error names each unit in order.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Cycle error code exists (`internal/errcode/catalog_trigger.go:7`)
- Disabled error code exists (`internal/errcode/catalog_trigger.go:9`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 2 (current), 9 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14; ADR 369 changes decision 14; ADR 370 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 369, ADR 370, ADR 286, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 369, 370.
- Quote: "A trigger starts a job through the job service. It has no path of its own. The gates, the approval rules, the change freezes, the manual locks and the limits of open jobs apply to the job as to any job."
- Quote: "A trigger starts a plan and nothing else. The acceptance criteria name a plan for each kind. The port that a trigger uses to start a job can start a plan and nothing else."
- Effect: The change starts only a plan through the job service and refuses a follows cycle at save. Decision 14 is read with its later changes, not respecified.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a trigger start an apply? ADR 267 decision 9: plan only, with the same gates, locks and freezes as any job.
- How are follows cycles handled? ADR 267 decision 17: refused at save with the ordered path, same namespace only.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
