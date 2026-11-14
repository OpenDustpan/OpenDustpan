---
version: 2
---

Issue: #1326, #1337.

## Why

**Persona: developer.** A developer starts plan and apply jobs from scripts that retry after timeouts. Lost retries, wrong denial names, and stalled claim polls cost runs and hide the cause.

The user path this change must make work:

1. **API.** Start a plan or apply job with Idempotency-Key and retry after a timeout
2. **API.** Read the job state after a cancel and after a poison read failure
3. **Docs page.** Follow the job start and claim notes on the docs page

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Retry of a job start with the same key returns the first answer within 24 hours. | The server returns the stored answer for the same key, principal, and route. | `curl -H 'Authorization: Bearer <token>' -H 'Idempotency-Key: <key>' -H 'Content-Type: application/json' -d '{"operation":"plan"}' https://server/api/dustpan/v1/namespaces/{namespace}/jobs` |
| Job start with a non-agent source for an agent actor fails. | The server refuses the start with 400 and error code job.bad-source and names the refused source. | `curl -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' -d '{"operation":"plan","source":"api"}' https://server/api/dustpan/v1/namespaces/{namespace}/jobs` |
| Retry with the same key and a different body fails. | The server refuses the retry with 409 and error code job.idempotency-conflict without starting a second job. | `curl -H 'Authorization: Bearer <token>' -H 'Idempotency-Key: <key>' -H 'Content-Type: application/json' -d '{"operation":"apply"}' https://server/api/dustpan/v1/namespaces/{namespace}/jobs` |

Evidence for the claims above:

- Issue 1326 asks for 24 hour retention and lists the Route comment, sourceOf, and denial action nits. Proof: These items come from the round 1 review of #1321: Idempotency keys must be kept for 24 h, as ADR 210 d6 requires; the comment on Route.IdempotencyKey says the wrapper returns 400; sourceOf accepts a source that is not an agent source; the denial record always names unit:run-plan
- The quoted ADR 210 decision requires the Idempotency-Key header on mutating routes and keys the stored answer by caller, route, and key. Proof: Each route of the tool-neutral API that changes data (`POST`, `PUT`, `PATCH`, `DELETE`) accepts the header `Idempotency-Key`.
- Issue 1337 asks for poison skip with count, cancel state agreement, bindClaim comment, and bound count. Proof: These items come from the round 3 review of #1307: A poison operation: when the read of its job or its namespace fails, the poll returns an error and does not go on to the next operation; After a cancel where a change began, the durable record says canceled but the job ends failed
- The job start source check lives in pkg/job/service_start.go. Proof: pkg/job/service_start.go:78: source, err := sourceOf(a, req.Source) and pkg/job/service_start.go:134: func sourceOf(a Actor, given SourceKind) (SourceKind, error) {
- The claim bind check lives in pkg/store/internal/jobstore/apply.go. Proof: pkg/store/internal/jobstore/apply.go:162: func bindClaim(ctx context.Context, tx Tx, cur *job.Job, c *job.Change) error {

## What Changes

- Keep job-start idempotency answers for 24 hours in the durable store and fix the Route.IdempotencyKey comment
- Refuse a non-agent source when the actor is an agent
- Write the refused action with actor, reason, and time in the job-start denial record
- Skip a poison operation for that poll, count it, and retry it on the next poll
- End the job and the durable record both as canceled on cancel after a change began
- Count and log operations skipped by the bound of 64, process them on later polls, and mark the bindClaim phase check as defense in depth

Out of scope:

- Use of the agent as a change key
- The giveBack race

## Usability

Where the path still costs the person time or surprises them:

- Retry of a job start with the same key surprises when the window is shorter than one work day.
- A denial that names the wrong action costs time when the person holds the named action.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Job start resolves the caller source through sourceOf (`pkg/job/service_start.go:78`)
- sourceOf returns the source kind of a start (`pkg/job/service_start.go:134`)
- Claim apply binds each event to its claim through bindClaim (`pkg/store/internal/jobstore/apply.go:162`)
- Job start has an idempotency test (`pkg/job/service_test.go:984`)
- Idempotency key classification lives in the store codec (`pkg/store/internal/codec/idempotency.go:163`)
- Claim apply binds the event of a claim in its transaction (`pkg/store/internal/jobstore/apply.go:94`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 6 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "**Every mutating route takes an `Idempotency-Key` (owner decision 2026-10-18).**"
- Quote: "Each route of the tool-neutral API that changes data (`POST`, `PUT`, `PATCH`, `DELETE`) accepts the header `Idempotency-Key`."
- Quote: "The server keys a stored answer by the caller (the principal), the route (the method and the path) and the key."
- Effect: The change keeps the mutating job start route under the Idempotency-Key rule, keys the stored answer by caller, route, and key, and sets the 24 hour retention from issue 1326. The retention value does not change the quoted rule.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the idempotency retention fix need an RFC? ADR 210 decision 6 requires each mutating route to accept Idempotency-Key and keys the stored answer by caller, route, and key. The quoted text does not state a retention window, so the 24 hour window comes from issue 1326 with no ADR conflict and no RFC.
- Do sourceOf, denial action, poison skip, cancel state, bindClaim comment, or bound count contradict an ADR? The pack names pkg/job/service_start.go and pkg/store/internal/jobstore/apply.go as the places to fix, with no ADR that allows the current behavior, so the fixes are code corrections.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
