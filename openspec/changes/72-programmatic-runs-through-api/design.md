## Context

An agent wants to start runs, read status and logs, and cancel runs from code, without use of the console by hand.

This change holds the place for RFC 72 programmatic runs through the API and an SDK. It refines scope, API operations, credentials, and SDK languages after runners and the run model land. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Programmatic runs ship after runners (RFC 6) and the run model (milestone 5) land. Token calls against the current job paths keep working as today.
2. **Access token credentials.** Calls carry an access token; the server checks the run action on the unit. A caller without the grant gets 403 and asks a namespace administrator for the grant.
3. **Start, read, cancel through one runs API.** The later refinement names the operations once the run model fixes them. Run limits and queue (107), options and re-runs (108), job kinds (109), and step results (110) stay in their own changes.
4. **SDK after the API.** SDK languages follow the stable runs API the same way C# and Java clients follow the stable REST API. No SDK ships before the API it wraps.

## Structure

No new structure now. The bug class is an SDK call that bypasses the grant check or a second runs path beside the console path; one runs API with per-call grant checks that each client uses is the structure that removes it. When the run model ships, a run request is an unexported value built only through its constructor, which checks the token, the unit grant, and the fence.

No custom analyzer and no new tool.

## Risks

- Automation waits for the runs API until runners and the run model land. Token calls against current paths plus `opendustpan status` and `opendustpan check` cover health and grant checks until then.
- A leaked token starts runs. Short token lifetimes, per-unit grants, and the audit record of each automatic action bound the effect.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Check the run action on the unit for each start, read, and cancel; answer 401 on a bad token and 403 on a missing grant. | Start a run without the grant; the answer is 403 and nothing starts. |
| service | Resolve the caller from the access token; fail closed on an unknown or ended token. | Present an unknown token; the server denies without touching state. |
| store | Carry the fence and the maintenance check on each run write. | Write a run row without a fence; the store refuses it. |
