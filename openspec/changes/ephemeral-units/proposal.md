---
version: 2
---

Roadmap: `docs/roadmap.md:367` (Ephemeral units).

## Why

**Persona: developer.** A Developer makes a short lived stack or workspace from a blueprint for a test. The Developer wants the server to warn before the end and remove the stack or workspace after the end, without manual cleanup.

The user path this change must make work:

1. **API.** Request a stack or workspace from a blueprint with a time to live through the API.
2. **API.** Read the expiry and the warning in the unit read and in the webhook sink.
3. **API.** Approve or cancel the waiting destroy through the review request where an approval rule applies.
4. **Docs page.** Follow the create and extend commands on the docs page.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Create without a time to live fails with 422 time-to-live required. | The server refuses the request and names the field. Run the create call again with a time to live inside the maximum. | `POST /api/dustpan/v1/namespaces/{namespace}/units` |
| Extend past the maximum fails with 422 time-to-live past maximum. | The server refuses the extension and names the maximum. Run the extend call again with a smaller value. | `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/extend` |
| Destroy with an unmet approval rule waits with 409 approval wait on the destroy action. | The server holds the destroy in approval wait and names the review request. Approve the review request, then check the job status. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}` |

Evidence for the claims above:

- A guide for blueprints exists in the docs. Proof: docs/guides/publish-a-blueprint.md
- Runner e2e tests exist for jobs and claims. Proof: e2e/jobs_api_test.go
- Approval rule e2e tests exist. Proof: e2e/approval_rules_test.go
- No e2e test in the pack names ephemeral units. Proof: Blueprint e2e_count 0, Runner e2e list holds no ephemeral file, Approval rule e2e list holds no ephemeral file
- The pack names no ADR for this item. Proof: adrs []
- Coverage matrix touches unit list routes with weak refusal cells. Proof: GET /api/dustpan/v1/namespaces/{namespace}/units op listUnits

## What Changes

- Ephemeral stacks and workspaces from blueprints carry a time to live and an expiry time inside 30 days plus one 7 day extension.
- One warning with idempotency key goes to the webhook sink 24 hours before expiry, once per unit with retry after outage.
- A destroy job runs on a runner at expiry in the same transaction as the expiry state change, with approval wait on the review request and no self approval where an approval rule applies.
- The time to live extends inside the maximum, or expiry is removed before destroy create.
- New POST /api/dustpan/v1/namespaces/{namespace}/units/{unit}/extend extends the time to live.

Out of scope:

- environment-promotion
- artifact-registry
- curated-upstreams
- upgrade-pull-requests
- job-proxy
- console-3

Depends on:

- blueprints
- `blueprints` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The Developer learns the expiry only at create time when the unit list hides it.
- The warning in the webhook sink names no next step to extend or approve.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Dev ephemeral test leaves no file. (`cmd/opendustpan/dev_test.go:476 (TestDevEphemeralLeavesNoFile)`)
- Unit client-encrypted error code is defined. (`internal/errcode/catalog_unit.go:7 (unit.client-encrypted)`)
- Access test covers unit read without outputs. (`pkg/access/access_test.go:1778 (TestCustomRoleWithUnitReadOnlyDoesNotReadOutputs)`)
- Webhook sink refuses blocked URL. (`pkg/logsink/webhook/webhook_test.go:52 (TestWebhookSinkRefusesBlockedURL)`)
- Scheduler waits first delay before first claim. (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)
- Blueprint read refuses hostile rows. (`pkg/store/postgres/self_service_blueprints_internal_test.go:93 (TestSelfServiceBlueprintReadRefusesHostileRows)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: design structure: a limit with no number; give the number, the unit and the scope.

Questions considered and answered:

- What limits the time to live and the warning time? No ADR in the pack touches this change (adrs [] in the pack). The spec uses provisional limits: 30 days from create plus one 7 day extension, warning 24 hours before expiry, destroy never self approves. The PM confirms them at review.
- How does the destroy at expiry run and where does approval wait apply? Pack lists e2e/jobs_api_test.go and e2e/approval_rules_test.go with no ephemeral file. The destroy reuses the job path and waits on the review request of the unit. This change adds no approvals route.
- Can a unit be extended after expiry processing started? Extend is allowed before the destroy job is created, including during approval wait. After the commit, extend is refused with 409.
- What stops an endless destroy retry and a double tick on several replicas? At most 5 destroy attempts with exponential backoff, then state expiry-failed with an alert. The expiry tick takes the unit row lock so one replica wins.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
