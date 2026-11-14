Issue: #1165, #1315, #1318.

## Why

**Persona: developer.** A developer runs plans and applies on runners from the API. Today the developer reads one job through the REST API and starts no job, so remote runs stay unreachable.

The user path this change must make work:

1. **API.** Start a plan job for one stack or workspace through POST jobs route
2. **API.** List jobs of the namespace or unit and read one job with timeline and masked plan diff
3. **API.** Cancel, approve or reject, confirm, and redact logs through the job routes
4. **Docs page.** Follow the jobs page to start, cancel, and approve jobs from the docs page

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Start refused while the namespace has an active freeze. | 403 with code job.frozen names the active freeze and the reason the rule blocks the start. | `POST /api/dustpan/v1/namespaces/{namespace}/jobs` |
| Cancel of a job that already reached a final state fails. | 409 with code job.state names the current state and the get job command that shows it. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| Approve by a principal without the approve action fails. | 403 with code job.forbidden names the missing approve action and the get job command that shows the job. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |

Evidence for the claims above:

- The REST API today reads one job and no route creates a job. Proof: Issue #1165 body: Today the REST API reads one job (`GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}`), and no route or feature creates a job.
- The first slice implements Start and Get while Cancel, Approve, Confirm and RedactLog return ErrUnsupported. Proof: Issue #1315 body: job.Service from the first slice implements Start and Get. Cancel, Approve, Confirm and RedactLog return ErrUnsupported.
- The Approve stub refuses the call in pkg/job/service.go. Proof: pkg/job/service.go:347:// Approve implements Service. This version refuses it.
- The RedactLog stub refuses the call in pkg/job/service.go. Proof: pkg/job/service.go:357:// RedactLog implements Service. This version refuses it.

## What Changes

- Add job.Service Start, Get, List, Cancel, Approve, Reject, Confirm, and RedactLog over job.Store with audit entries
- Add REST routes to start, list, get, cancel, approve, reject, confirm, and redact logs, each with authorization action and object
- Add timeline and masked plan diff read routes for one job
- Accept break-glass flag in REST start body while the namespace break-freeze rule still blocks the start

Out of scope:

- Freeze routes
- TFE link for speculative runs
- Runner session routes
- Launcher binary

## Usability

Where the path still costs the person time or surprises them:

- The developer must learn one action per route; the docs page lists each route with its action in one table.
- Cancel of a finished job surprises; the error names the current state and the get command.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The proposal fails its own code check: route GET /api/dustpan/v1/me/reach, named in the error table for job.forbidden, is not in the API description or the route table. The change is not spec-ready until an existing route is named or the route is added. Resolution: answered: Replaced the me/reach help command with the existing get-one-job route; no new route is added in what_changes.
- R1.2 (scope): The proposal and design list Reject as a job route, and tasks 1.1 and 2.1 cover it, but the delta spec has no Reject requirement or scenario. The change misses part of its own item. Resolution: answered: Added a Reject job requirement with actor, time, and audit scenario; tasks already cover it.
- R1.3 (missing scenario): The design requires the break-glass flag and reason in the REST start body, and the service must still apply the namespace break-freeze rule. The spec has no requirement for break-glass: no scenario for who may set the flag, what the reason must contain, or what the refusal looks like when the freeze blocks the start. Resolution: answered: Added a break-glass start requirement with flag, reason, break-freeze rule, and freeze refusal scenario.
- R1.4 (missing scenario): The proposal promises a 403 job.frozen refusal when a start hits an active freeze, but no spec scenario covers this refusal. The break-freeze rule is also not defined in any requirement, so the start path under a freeze is unspecified. Resolution: answered: Added a freeze refusal requirement with a start-under-freeze scenario that returns 403 job.frozen.
- R1.5 (failure mode): The design risk 'List route leaks jobs across units without unit scoping' is not covered. The List requirement returns jobs only when a unit filter is given. A namespace-wide list without a filter, or a filter on a unit the caller cannot read, has no scenario or refusal. Resolution: answered: Added list scoping scenarios: namespace list returns only that namespace, unit filter returns only that unit, unreadable unit filter is refused.
- R1.6 (failure mode): The design risk 'Approve path binds approval to a stale plan' is not covered. The Approve requirement has no rule that ties the approval to the plan version it was shown, so a plan changed after review could be approved. Resolution: answered: Added a stale plan refusal requirement that binds each approval to the shown plan revision.
- R1.7 (missing scenario): The proposal promises a 409 job.state refusal when a finished job is canceled, and a 403 job.forbidden refusal when a caller lacks the approve action. Neither error has a scenario in the spec. Resolution: answered: Added cancel-finished 409 and approve-forbidden 403 scenarios under Cancel and Approve.
- R1.8 (wrong claim): The error table for the frozen start names GET .../jobs as the command that lists freezes. That route lists jobs, not freezes, and freeze routes are out of scope, so the help command does not match the error. Resolution: answered: Fixed the frozen-start help command to the existing start route; it no longer claims a freeze list route.
- R1.9 (failure mode): No requirement covers concurrent or repeated calls: a double start, a double approve, or a cancel racing a state change. There is no idempotency rule or conflict answer for these cases. Resolution: answered: Added a repeat-call requirement with same Idempotency-Key returning the first job and double approve and cancel-race scenarios.
- R1.10 (missing scenario): The Redact log requirement does not say which log version is named, how substitutions are validated, or what a hostile pattern does. The Start requirement does not require a reason in the audit entry, although the project requires each automatic action to record who and why. Resolution: answered: Added log version naming and substitution validation to Redact requirements and actor plus reason to the start audit scenario.
- R1.11 (wrong claim): The ADR check says 'No ADR is touched' and gives no proof for the negative claim, such as the rg command or the quoted decision lines. The decisions list is empty, so the claim has no evidence for the gate. Resolution: answered: Kept adr_check empty because the pack lists no ADR; considered now cites the empty adrs section of the pack as proof.

## Open questions

none

Questions considered and answered:

- Does any ADR decide the shape of the job routes? No. The context pack lists no ADR in its adrs section, so no quoted decision governs the job route shape. The change stays inside start, list, get, cancel, approve, reject, confirm, and redact log, and leaves freeze routes to the next part.
- Should the route and state use cancel or revoke? Cancel, because the roadmap scope for this part states that it uses cancel for the route name and the job state.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
