---
version: 5
---

Issue: #1326, #1337.

## Why

**Persona: developer.** A developer starts plan and apply jobs from scripts. A comment that says the wrapper returns 400 when it does not, and polls that skip operations with no count, hide the cause of missing runs.

The user path this change must make work:

1. **API.** Start a job with an Idempotency-Key and retry after a timeout
2. **Docs page.** Read the job start note on the Idempotency-Key header

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A job start repeats with the same Idempotency-Key. | The server returns the stored answer of the first start and does not start a second job. | `curl -X POST -H 'Authorization: Bearer <token>' -H 'Idempotency-Key: <key>' -H 'Content-Type: application/json' -d '{"operation":"plan"}' https://server/api/dustpan/v1/namespaces/{namespace}/jobs` |

Evidence for the claims above:

- The comment on Route.IdempotencyKey sits in the route files of the operations packages. Proof: internal/api/registry/operations.go:14: // the route. The Idempotency-Key flag comes from Route.IdempotencyKey, so no
- Issue 1326 says the comment says the wrapper returns 400 and the wrapper does not enforce it. Proof: Nit: the comment on `Route.IdempotencyKey` says the wrapper returns 400, but the wrapper does not enforce it.
- Issue 1337 says skipped operations of the bound of 64 are not counted. Proof: Operations skipped because of the bound of 64 are not counted. Add a metric and a log line for them.
- The job store holds the claim code. Proof: pkg/store/internal/jobstore/apply.go:94: if err := bindClaim(ctx, tx, &cur, c); err != nil {

## What Changes

- Fix the comment on Route.IdempotencyKey in the operations packages so that it states what the wrapper does
- Count and log each operation that the claim poll skips because of the bound of 64

Out of scope:

- Source check of an agent actor: main already does it (sourceOf)
- Denial record naming of the refused action
- Comments on the bindClaim phase check
- Idempotency retention of 24 hours
- Poison operation skip
- End of job and durable record on cancel
- Use of the agent as a change key
- The giveBack race

## Usability

Where the path still costs the person time or surprises them:

- A comment that says the wrapper returns 400 costs time when the wrapper does not.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- sourceOf already returns the source kind of a start (`pkg/job/service_start.go:134 (func sourceOf)`)
- bindClaim binds the event of a claim (`pkg/store/internal/jobstore/apply.go:162 (func bindClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 6 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Each route of the tool-neutral API that changes data (`POST`, `PUT`, `PATCH`, `DELETE`) accepts the header `Idempotency-Key`."
- Effect: The comment fix states what the wrapper does for the header. No behavior of the header changes.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Do the kept items conflict with an ADR? No. A comment fix and a counter change no part of ADR 210 decision 6.
- Where do the other items go? They go to separate follow-up changes, because main covers some and others are separate concerns.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
