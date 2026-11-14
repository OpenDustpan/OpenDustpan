---
version: 2
---

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
| Start refused while the namespace has an active freeze. | 403 with code job.frozen names the active freeze; check active jobs with the list jobs call. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs` |
| Cancel of a job that already reached a final state fails. | 409 with code job.state names the current state and the get job call that shows it. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |
| Approve by a principal without the approve action fails. | 403 with code job.forbidden names the missing approve action and the get job call that shows the job. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |

Evidence for the claims above:

- The REST API today reads one job and no route creates a job. Proof: Issue #1165 body: Today the REST API reads one job (`GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}`), and no route or feature creates a job.
- The first slice implements Start and Get while Cancel, Approve, Confirm and RedactLog return ErrUnsupported. Proof: Issue #1315 body: job.Service from the first slice implements Start and Get. Cancel, Approve, Confirm and RedactLog return ErrUnsupported.
- The RedactLog stub refuses the call in pkg/job/service.go. Proof: pkg/job/service.go:357:// RedactLog implements Service. This version refuses it.

## What Changes

- Add job.Service Start, Get, List, Cancel, Approve, Reject, Confirm, and RedactLog over job.Store with audit entries
- Add REST routes to start, list, get, cancel, approve, reject, confirm, and redact logs, each with authorization action and object
- Add timeline and masked plan diff read routes for one job
- Accept break-glass flag in REST start body while the namespace break-freeze rule still blocks the start

Out of scope:

- Freeze routes
- TFE speculative-run caller that starts a plan job
- Forge lookup of Location.Revision when the request gives no revision
- Runner session routes
- Launcher binary

## Usability

Where the path still costs the person time or surprises them:

- The developer must learn one action per route; the docs page lists each route with its action in one table.
- Cancel of a finished job surprises; the error names the current state and the get command.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- RedactLog is refused by a stub service. (`pkg/job/service.go:357`)
- RedactLog replaces each match in a log version. (`pkg/job/store.go:481`)
- Store serves job.Store and job.FreezeStore from one Runner. (`pkg/store/internal/jobstore/store.go:36`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR decide the shape of the job routes? No. The pack adrs section is empty, so no quoted decision governs job route shape. The change adds start, list, get, cancel, approve, reject, confirm, and redact log routes and leaves freeze routes to the next part.
- Should the route and state use cancel or revoke? Cancel, because the roadmap scope for this part uses cancel for the route name and the job state.
- Does this part cover the TFE speculative-run caller or forge lookup of the revision? Left out. The roadmap scope leaves the TFE speculative-run caller and forge revision lookup to their own changes. This part leaves Location.Revision empty when the request gives no revision.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
