---
version: 4
---

Issue: #1165, #1315, #1318.

## Why

**Persona: developer.** A developer runs plans and applies on runners from the API. The job service implements Start and Get only. Cancel, Approve, Confirm and RedactLog answer ErrUnsupported, so a started job cannot be canceled, approved or redacted.

The user path this change must make work:

1. **API.** Cancel a job that has not reached a final state
2. **API.** Approve or reject a job that waits for approval, bound to the shown plan revision
3. **API.** Confirm a job step and redact a secret from a log version
4. **Docs page.** Follow the jobs page for each route and its action

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Cancel of a job that already reached a final state fails. | 409 with code job.wrong-state (NEW: no catalog code names a job in the wrong state; update.wrong-state belongs to the Pulumi update) names the current state. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| Approve by a principal without the approve action fails. | 403 with code access.denied names the missing action. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| Approve of a stale plan revision fails. | 409 with code job.wrong-state names the current plan revision. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |

Evidence for the claims above:

- Cancel, Approve, Confirm and RedactLog return ErrUnsupported on main. Proof: Issue #1315 body: Cancel, Approve, Confirm and RedactLog return ErrUnsupported.
- The RedactLog service method refuses the call. Proof: pkg/job/service.go:357: RedactLog implements Service. This version refuses it.
- The store already has a RedactLog method. Proof: pkg/job/store.go:481 documents Store RedactLog.

## What Changes

- Implement Cancel, Approve, Reject, Confirm and RedactLog in pkg/job/service.go over job.Store, each with an audit entry
- Add REST routes for these five calls, each with its authorization action and object
- Accept the break-glass flag in the start body; the namespace break-freeze rule still applies

Out of scope:

- Freeze routes (next part)
- TFE speculative-run caller
- Runner session routes
- Launcher binary
- Forge lookup of the head revision

## Usability

Where the path still costs the person time or surprises them:

- The docs page lists each route with its action in one table.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The RedactLog service method refuses the call. (`pkg/job/service.go:357`)
- The Store has a RedactLog method. (`pkg/job/store.go:481`)
- One Store serves job.Store and job.FreezeStore. (`pkg/store/internal/jobstore/store.go:36`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR decide the job route shape? The pack adrs section is empty, so no decision is quoted. A provisional decision follows the rule of benchmark, persona impact and escape hatch: AWS-style explicit verbs, and Developer sees no surprise.
- Cancel or revoke? Cancel, as the roadmap scope says.
- Are start, list and get in scope? No. Main has Start and Get; this part covers only the stubs.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
