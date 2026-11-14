---
version: 2
---

Issue: #1316, #1346, #1383, #1384.

## Why

**Persona: developer.** A developer tries to run a plan on a runner without naming the pool or code location on each call. The server must take those inputs from the settings of the stack or workspace so trigger starts and API starts work the same way, with refusals that name the one command that helps.

The user path this change must make work:

1. **Docs page.** Namespace administrator saves code location, pool, profile reference and steps on the unit from the docs page.
2. **API.** Developer starts a plan job with no location; the server uses the stored settings.
3. **API.** Schedule or push trigger starts a plan on a runner with the stored settings; the job reads outputs of its source under the sharing rule and the saver check.
4. **E2e.** The e2e step against the real CLI proves the refusal and the trigger read denial.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Start with a pool or location override without the needed grant. | 403 with the missing action and no state change. | `GET /api/server/namespaces/{namespace}/grants` |
| Trigger or API start with no location in request and none in unit settings. | 422 with the missing field and no job created. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/settings` |
| Job-token read of outputs of another unit with no sharing rule or with a saver who lost access. | 403 on the output read with no secret value in the body. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs` |

Evidence for the claims above:

- The trigger code records that location, pool and steps come from the settings of the unit. Proof: pkg/trigger/manager.go:271 and pkg/trigger/jobstarter/jobstarter.go:46
- The saver resolver exists and the read check type exists. Proof: pkg/dustpan/triggers.go:250 and pkg/trigger/readcheck.go:15
- No non-test code calls the trigger output-read check. Proof: rg -n -F --glob '!*_test.go' 'trigger.OutputReadCheck.Allow' pkg internal cmd matches 0
- The planner checks the location in the dustpan service. Proof: pkg/dustpan/jobservice.go:606 (jobPlanner.checkLocation) and test pkg/dustpan/jobservice_internal_test.go:328

## What Changes

- Unit settings hold code location, runner pool, profile reference and default steps, with namespace defaults as fallback.
- Job start resolves inputs from request override, then unit settings, then defaults, and snapshots them on the job.
- Job-token output reads enforce the sharing rule plus the trigger saver and manual-fire caller check.
- Built-in roles hold the start and read actions as the owner decides in the open question.

Out of scope:

- Pulumi default step design of the runner recorder
- Profile provider port, open sessions and secret resolve
- Write-back, stored-plan binding, pull-request automation and console run page

Depends on:

- add-job-service-routes-start-1

## Usability

Where the path still costs the person time or surprises them:

- A developer who names a pool without the grant gets a 403 that names the action and the one command that helps.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- OutputReadCheck.Allow decides each read. (`pkg/trigger/readcheck.go:66`)
- Planner location check. (`pkg/dustpan/jobservice.go:606`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current), 14 (changed).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "A trigger starts a job through the job service. It has no path of its own."
- Quote: "The runner pool is the only routing key (6.1). A trigger has no pool and no label."
- Effect: Trigger starts use the job service, take the pool from the unit settings and run as the namespace service principal.
- Conflict: none.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (not owner-tagged).

- Decisions that apply: 1 (changed), 2 (changed).
- Changed by: ADR 241 changes decision 10; ADR 244 changes decision 10; ADR 265 changes decision 9; ADR 267 extends decision 1; ADR 271 changes decisions 7 and 8; ADR 273 changes decision 1; ADR 273 extends; ADR 286 changes; ADR 300 changes decision 2; ADR 300 changes decision 10; ADR 309 extends decision 5; ADR 353 changes decision 2. Chain to the end: ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 286, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 241, 244, 265, 267, 271, 273, 286, 300, 309, 353.
- Quote: "**A job reads the outputs of another unit on two paths, each with two checks.**"
- Effect: Reads of outputs keep two checks at each read. Plan-only jobs get own-unit secrets only from trusted code.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "**A job token reads only what a rule names and the starter can read (15.5, option c).**"
- Effect: The saver check uses unit:read-outputs.
- Conflict: none.

**ADR 338: The namespace service principal holds a stored grant** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The principal still holds nothing else (owner, 2026-11-07). The grant covers the start of a plan only."
- Effect: The service principal keeps only the plan start grant.
- Conflict: none.

## Open questions

1. Which built-in roles hold unit:run-plan, unit:define-steps, job:read, job:read-plans, job:read-logs and namespace:manage-runners? Decides: owner.
   - Options: Reader gets job:read. Planner gets unit:run-plan and the job read actions. Lead gets unit:define-steps. Administrator keeps namespace:manage-runners; Maintainer gets unit:run-plan and unit:define-steps.
   - Recommended: Reader gets job:read. Planner gets unit:run-plan and the job read actions. Lead gets unit:define-steps. Administrator keeps namespace:manage-runners. Reason: It gives least privilege and keeps pool choice with administrators.

Not spec-ready: a code check fails: task "Add the e2e step, docs page and routes with actions." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>"; a code check fails: quality[0] (correctness): give a number, a unit and a scope; a code check fails: a security change needs "threats.dfd": a Mermaid flowchart with each trust boundary as a subgraph.

Questions considered and answered:

- Where do pool and location of a trigger start come from? ADR 267 decision 4: the unit settings. A request location is an override.
- Do manual locks stop a trigger start? ADR 267 decision 2: yes, as for any job.
- Does namespace:manage-runners depend on grants alone? Yes, intended: the gate service types the object as a namespace. The route still checks the grant and writes the audit entry.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
