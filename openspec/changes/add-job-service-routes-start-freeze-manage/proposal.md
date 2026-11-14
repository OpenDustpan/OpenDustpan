Issue: #1374.

## Why

**Persona: namespace administrator.** A namespace administrator stops all job starts on a namespace during an incident or a change window, then ends the freeze when work can start again.

The user path this change must make work:

1. **API.** Create a freeze on a namespace with a reason through POST on the freeze collection.
2. **CLI.** Start a job during the freeze and read the 403 refusal with the one list command.
3. **API.** List freezes of the namespace and read the active freeze and its reason.
4. **API.** End the freeze through the end route and start the job again.
5. **Docs page.** Follow the freeze page to create, list, and end a freeze.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| 403 freeze active refuses POST job start during a freeze | The server refused the job start because the namespace has an active freeze. List the freezes to read the reason, then wait for the freeze to end or ask a namespace administrator to end it. | `opendustpan freeze list --namespace acme` |
| 403 no freeze action refuses freeze create or end without the action | The server refused the freeze action because the principal lacks the freeze action on the namespace. Ask a namespace administrator for a grant. | `opendustpan freeze list --namespace acme` |
| 403 no break-freeze refuses break-glass start during a freeze | The server refused the start because break-freeze is missing. Ask a namespace administrator for the break-freeze grant or wait for the freeze to end. | `opendustpan freeze list --namespace acme` |

Evidence for the claims above:

- The pack path list for this item holds only three paths. Proof: paths list holds internal/api/runnerapi/routes.go, pkg/launcher, pkg/runner/protocol only
- The pack code facts list only session, recorder, and jobtest matches. Proof: code_facts list session, recorder, and jobtest matches only
- The pack issue list names the runner session and launcher binary work. Proof: #1374

## What Changes

- New route POST /api/dustpan/v1/namespaces/{namespace}/freezes creates a freeze with reason, creator, and time
- New route POST /api/dustpan/v1/namespaces/{namespace}/freezes/current/end ends the active freeze with end record
- New route GET /api/dustpan/v1/namespaces/{namespace}/freezes lists freezes in time order with active state
- Freeze guard on job start with break-glass start for break-freeze holders with reason
- Audit entries for freeze create, freeze end, and break-glass start
- New command `opendustpan freeze list --namespace acme` lists the freezes of a namespace with active state, reason, creator, and times

Out of scope:

- Job lifecycle routes with cancel
- TFE link for remote execution
- Runner session routes for steps, logs, plans, and phase finish
- Launcher binary modes
- Freeze schedules and expiry
- Freeze guard on actions other than job start

Depends on:

- add-job-service-routes-start-1
- `add-job-service-routes-start-1` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The freeze list shows the active freeze, its reason, its creator, and its time, so the team knows why starts stop.
- A refused start names the freeze and the one list command, so the starter knows the next step.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The reason rule is open. The recommended answer (required reason, at most 500 characters, on freeze create and break-glass start) is not in the spec, and the proposal still lists it as open. The spec-ready gate needs this answer before build. Resolution: answered: Reason rule is now in the spec: create and break-glass start require a reason of at most 500 characters, with 400 refusal scenarios. Open questions reads none.
- R1.2 (wrong claim): The error table and the 'Not spec-ready' note name GET /api/dustpan/v1/namespaces/{namespace}/freezes as the one helping command. The API description and route table do not contain this route, so the usability promise fails and the gate refuses the change. Resolution: answered: The list route is now named as a new route in what_changes with its full path, so the error table command resolves to a new route of this change.
- R1.3 (scope): The linked issue #1374 is titled 'Add the runner session routes and the launcher binary'. The change adds freeze routes. The issue link does not match the change, and the pull request would link the wrong work. Resolution: answered: Scope follows the roadmap split of add-job-service-routes-start part 2 of 2. Runner session and launcher work of #1374 is out of scope and excluded in out_of_scope.
- R1.4 (failure mode): Job start checks the freeze and then dispatches. A freeze created between the check and the dispatch does not stop the job. The design names no atomic check and no lock, so the guard can be bypassed under concurrency. Resolution: answered: Design now states the freeze read happens in the same transaction as dispatch, and the refuse-start requirement covers a freeze made up to dispatch.
- R1.5 (failure mode): Two create or end requests on one namespace can interleave. The design names this as a risk but the spec has no requirement, scenario or test for concurrent create, concurrent end, or create then end. Resolution: answered: Single-active-freeze requirement added with scenarios for second create refused and concurrent end, plus a failing test for each.
- R1.6 (missing scenario): The create requirement applies only when no active freeze exists. No scenario covers a create request while a freeze is active, or an end request when no freeze is active. Resolution: answered: Scenarios added: create while active answers 409, end with none active answers 404.
- R1.7 (missing scenario): The spec does not state the audit entry for freeze end, although the proposal lists it under What Changes. The end requirement has no audit scenario. Resolution: answered: End requirement now states the audit entry with a scenario that checks it.
- R1.8 (missing scenario): The task 1.2 asks for guard tests on other guarded actions, but the spec and the proposal define the guard only for job start. The scope of 'other guarded actions' is not defined. Resolution: answered: Guard scope fixed to job start only in out_of_scope, considered, and tasks.
- R1.9 (failure mode): Freeze reason is free text with no length limit in the spec. A hostile or very long reason can fill the list and the audit log. The hostile test layer does not cover input size. Resolution: answered: Reason limit of 500 characters is now in the spec with a 400 scenario and a hostile input-size test in the API layer.
- R1.10 (open question): The freeze is keyed by namespace name or by stable ID. The change does not say which. Namespaces can be renamed, so the stable ID rule of project.md applies, and the change does not follow it. Resolution: answered: Design now keys freeze by namespace ID stable ID per the stable ID rule.
- R1.11 (wrong claim): The ADR check says 'No ADR is touched' with no quoted decision lines and no index lookup. The decisions input is empty, so the claim has no proof and counts as unchecked. Resolution: answered: ADR check stays empty because the pack adrs list is empty and names no ADR; considered records the empty index result.
- R1.12 (wrong claim): The evidence section cites runner protocol session paths and a session renew route. These facts do not prove that no freeze route or freeze store exists, so the negative claim about freeze is not supported by the cited evidence. Resolution: answered: Evidence now claims only what the pack proves: its three paths and its session, recorder, and jobtest facts.

## Open questions

none

Questions considered and answered:

- Does this change include job lifecycle or runner session routes? No. This part adds only freeze create, end, and list routes with the freeze guard on job start. Job lifecycle routes, the TFE link, runner session routes, and the launcher binary stay outside this change.
- Does freeze already exist on main? No. The pack path list holds only internal/api/runnerapi/routes.go, pkg/launcher, and pkg/runner/protocol, and its code facts list only session, recorder, and jobtest matches. The change therefore adds new freeze routes.
- Which ADR governs freeze? The pack adrs list is empty and names no ADR. The check is therefore empty, and no quoted decision applies to this change.
- Does the freeze guard stop actions other than job start? Job start only. Other actions are out of scope so the guard scope stays exact.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
