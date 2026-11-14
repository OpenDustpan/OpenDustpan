---
version: 2
---

Roadmap: `docs/roadmap.md:310` (Events and notifications).

## Why

**Persona: namespace administrator.** A namespace administrator sets a webhook sink so the team learns at once when a job starts, waits for approval, ends, or when a token nears expiry, without reading logs for secrets.

The user path this change must make work:

1. **API.** Create a webhook sink in the namespace with its URL and signing secret.
2. **API.** Create a subscription on the sink with a type filter for job and token notices.
3. **E2e.** Start a plan and an apply job for a stack and a workspace and watch deliveries arrive signed.
4. **API.** Fix a failed receiver, redeliver, and rotate the signing secret with the test path.
5. **Docs page.** Follow the docs page to repeat the full path on a fresh install.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Delivery stops after a grant change | Delivery stays pending with reason; the creator lost namespace:manage-notifications or namespace:read. Check the pending reason in the deliveries list, restore the grant, then redeliver from the same list. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` |
| Webhook receiver down or refuses signature | Subscription suspended after 24 hours of failed deliveries with the error class as reason. Fix the receiver, then retry pending deliveries in sequence order. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` |
| Sink create refuses the URL | Sink create refused: webhook URL is not http or https or the egress guard blocks it. Fix the sink URL and test the sink. | `GET /api/dustpan/v1/namespaces/{namespace}/sinks/{id}` |

Evidence for the claims above:

- A notification destinations guide exists. Proof: docs/guides/notification-destinations.md
- Sink e2e coverage exists for audit sinks. Proof: e2e/audit_sinks_test.go
- Signature keyword appears in e2e and guides in this pack. Proof: e2e/e2e_masked_test.go, e2e/e2e_test.go, e2e/registry_test.go, e2e/workload_test.go, docs/guides/audit-log.md, docs/guides/observe.md

## What Changes

- Sinks and subscriptions serve job start, approval wait, job end and token expiry notices from the job service for runner and API-started jobs
- Signed webhook POST with retry, suspend after 24 hours, and no secret in any notice body
- Redeliver of pending deliveries in sequence order and probe of a sink through existing subscription and delivery routes
- Docs page and e2e step with a real receiver

Out of scope:

- Runner join, claim and log streaming
- Planner role and other approvals behavior
- Pull request status and comment rendering
- Console run page timeline and diff
- Chat and email senders beyond the webhook path
- Outbox, subscription, delivery store, retry, suspend and manual retry route owned by chat-a2-outbox-deliveries-dispatcher
- Job service routes owned by add-job-service-routes-start
- Sink probe and secret rotation routes owned by chat-a2-outbox-deliveries-dispatcher

Depends on:

- chat-a2-outbox-deliveries-dispatcher
- add-job-service-routes-start-1
- `chat-a2-outbox-deliveries-dispatcher` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Finding the suspended state needs the subscription read; the delivery error class shows there.
- Rotating a signing secret needs two steps in order; the docs recommend deleting the old secret only after the receiver moves.
- A receiver that ignores sequence shows duplicates out of order during retries; the docs page shows ordering by sequence.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Block test exists for sink URL guard. (`pkg/logsink/webhook/webhook_test.go:52 (TestWebhookSinkRefusesBlockedURL)`)
- Built-in roles hold the notification action. (`pkg/access/access_test.go:1681 (TestBuiltInRolesHoldTheNotificationAction)`)
- Subscription error codes are covered. (`internal/api/notifications/errcode_test.go:146 (TestSubscriptionErrorsHaveCodes)`)
- Expiry warning of an ended token is covered. (`pkg/accesstoken/accesstoken_test.go:1413 (TestExpiryWarningOfAnEndedToken)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 12 (current).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Events and webhooks in milestone 5. An expiry event and its webhook come with RFC 23 in milestone 5."
- Effect: This change ships the milestone 5 job lifecycle and token expiry notices through the signed webhook path.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "Expiry warnings. The warnings of ADR 53 decision 11 start 14 days and again 3 days before the expiry. Both are settings."
- Effect: This change uses the 14-day and 3-day warning times for the token expiry notice.
- Conflict: none.

**ADR 145: One notification model with sinks for email, chat and signed webhooks** (not owner-tagged).

- Decisions that apply: 4 (current), 5 (extended), 10 (current), 14 (current), 16 (current), 18 (current), 19 (current), 20 (changed), 21 (current), 22 (current), 27 (extended), 28 (current).
- Changed by: ADR 146 extends decision 5; ADR 149 changes decisions 15 and 24; ADR 149 extends decisions 3, 13 and 27; ADR 197 extends decision 5; ADR 203 extends decision 5; ADR 204 extends decision 5; ADR 210 changes; ADR 234 extends decisions 1 and 24; ADR 241 extends decision 5; ADR 248 changes decision 9; ADR 262 extends; ADR 315 extends; ADR 334 changes decisions 13 and 20. Chain to the end: ADR 146, ADR 149, ADR 197, ADR 203, ADR 204, ADR 210, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334, ADR 227, ADR 297, ADR 222, ADR 229, ADR 230, ADR 225, ADR 286, ADR 300, ADR 353, ADR 247, ADR 313, ADR 343, ADR 301, ADR 320, ADR 339, ADR 354, ADR 333, ADR 335, ADR 351, ADR 268, ADR 355, ADR 299, ADR 332, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 146, 149, 197, 203, 204, 210, 234, 241, 248, 262, 315, 334.
- Quote: "No secret in a notification. A notification never holds a secret value, a credential, state, a plan or a log."
- Quote: "One catalog of types. `notify.Types` lists every type, for example `job.failed`, `freeze.upcoming`, `break-glass.review-notice`, `on-call.handoff`, `budget.threshold` and `incident.opened`."
- Quote: "Delivery order is not guaranteed. Retries and claims by more than one instance can deliver a later notification first."
- Quote: "A sink that fails for a day. When every delivery of a subscription failed for 24 hours, the dispatcher suspends it, with the class of the last error as the reason, and sends `subscription.suspended`."
- Effect: This change follows the one notification model: no secret in body, catalog types, subscriptions, at-least-once delivery, retry, suspend, egress guard, signed delivery, rotation, routes and audit.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Wire dispatcher checks, enqueue idempotence, retry backoff, suspend an" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: task "Run e2e with a webhook sink and a matching subscription against the re" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- Which notification types does the job path emit? ADR 145 decision 5 holds one catalog in notify.Types with job types as examples including job.failed. This change emits the four facts of the roadmap step through that catalog and names no new catalog string in the spec.
- How does a webhook delivery prove its source? ADR 145 decision 21 defines the signed POST. This change follows it with no new header and no new signature rule.
- What happens when a sink fails? Retry, suspend, enqueue and manual retry are owned by chat-a2-outbox-deliveries-dispatcher per ADR 145 decisions 16, 17, 18 and 19. This change defines no retry rule.
- Does a job notification hold secrets or logs? ADR 145 decision 4 forbids secret values, credentials, state, plans and logs in a notification. This change holds only names, IDs, summary, sequence and links.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
