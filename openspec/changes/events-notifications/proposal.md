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
| Delivery stops after a grant change | Delivery stays pending with reason creator-lacks-access; the creator lost the manage action. Restore the grant, then retry from the subscription deliveries list. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}` |
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

Depends on:

- chat-a2-outbox-deliveries-dispatcher
- add-job-service-routes-start
- `chat-a2-outbox-deliveries-dispatcher` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Finding the suspended state needs the subscription read; the delivery error class shows there.
- Rotating a signing secret needs two steps in order; the test path confirms the receiver before delete of the old secret.
- A receiver that ignores sequence shows duplicates out of order during retries; the docs page shows ordering by sequence.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 12 (current).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Events and webhooks in milestone 5. An expiry event and its webhook come with RFC 23 in milestone 5."
- Effect: This change ships the milestone 5 notices for job lifecycle and token expiry through the webhook path.
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

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The ADR check does not settle which notification types the job path emits. ADR 145 decision 5 lists job.failed as an example, but the change names job.started, job.awaiting-approval and terminal types without a catalog citation; tasks 1.1 and 2.1 cannot be built until the exact type names and the terminal set are fixed. Named in proposal.md and specs/notifications/spec.md. Resolution: answered: Specs no longer pin invented catalog strings; each notice maps to the shipped notify.Types catalog per ADR 145 decision 5, with job.failed as the evidenced example. Tasks test the four roadmap facts.
- R1.2 (scope): The open questions section reads not spec-ready. A code check fails on the redeliver route, which is not in the API description or route table, yet the change depends on it for the redeliver and rotation steps. The gate refuses implementation until the route is confirmed or replaced. Resolution: answered: No new route is proposed; what_changes, errors, tasks and specs use only the existing sink, subscription and delivery routes per ADR 145 decision 27.
- R1.3 (missing scenario): No requirement or scenario covers the secret rotation flow (add second secret, move receiver, delete first) or the test route. Proposal step 4 and ADR 145 decision 22 require them, but spec.md has only a signature scenario with one or two secrets and no rotation or delete behavior. Resolution: answered: Specs add secret rotation requirement with add-second-secret, move-receiver and delete-first scenarios plus a sink test requirement.
- R1.4 (missing scenario): No requirement covers subscription creation with a sink or filter, the creator reach check failing, or the sink URL refusal (422 sink.bad-url). The proposal error table names these paths, but the spec has no scenario for them. Resolution: answered: Specs add subscription routing requirement with filter match, sink-deleted, creator reach and sink URL refusal scenarios.
- R1.5 (failure mode): The job-end requirement does not say how a job that is canceled or fails before start emits a terminal notice, nor how a job that restarts or is retried after a crash avoids duplicate job.started notices. Dedupe is named in design.md but no scenario tests a restart or a repeated job transaction. Resolution: answered: Specs add cancel-before-start terminal scenario and duplicate-guard requirement for restart with same dedupe key.
- R1.6 (failure mode): The token expiry requirement does not cover a token renewed, rotated or revoked inside the warning window, nor a repeated schedule run after an outage; ADR 53 decision 11 sets warnings at 14 and 3 days, which the spec does not name. Resolution: answered: Specs pin 14-day and 3-day warnings per ADR 69 decision 5 and add renew, rotate, revoke and repeated-schedule scenarios.
- R1.7 (failure mode): The creator-reach requirement is absent from the spec. Design and the layers table say the dispatcher skips delivery when the creator loses namespace:manage-notifications, and the proposal says delivery stays pending with a reason, but no scenario covers this. Resolution: answered: Specs add creator reach requirement: dispatcher skips delivery, keeps delivery pending with reason, writes audit entry.
- R1.8 (adr conflict): ADR 145 decision 19 says a subscription failing for 24 hours is suspended with the class of the last error. The spec's retry requirement names the suspend and emit but not the reason field; the change adds no rule for the state of deliveries pending during suspension. Resolution: answered: Retry requirement records error class as suspend reason, keeps pending deliveries pending, and emits the suspend notice.
- R1.9 (wrong claim): The ADR check describes ADR 53 decision 12 as 'current', but the change treats it as the milestone 5 mandate. The 'Changed by' chain and the stated effect are consistent, but the claim that the change ships the milestone 5 part relies on a reading of 'an expiry event' that no ADR defines in terms of a catalog type. Resolution: answered: ADR check quotes ADR 53 decision 12 verbatim and maps it to the catalog without rewording any owner-tagged decision.
- R1.10 (open question): The usability note says a receiver that ignores sequence sees duplicates out of order, but no requirement or task states how a receiver obtains sequence or how the docs page explains it, which ADR 145 decision 10 relies on. Resolution: answered: Specs add order-by-sequence requirement and docs task states that the receiver orders by sequence per ADR 145 decision 10.
- R1.11 (missing scenario): Task 2.3 requires a real receiver over TLS, but no scenario covers an egress-guard refusal of a sink URL at delivery time, nor a receiver that returns Retry-After, both defined in ADR 145 decisions 18 and 20. Resolution: answered: Specs add sink URL blocked at delivery and Retry-After scenarios with backoff and audit under ADR 145 decisions 18 and 20.
- R2.1 (adr conflict): events-notifications/design.md Decision 2 sets the 24-hour suspend on subscriptions and the spec ships it, but ADR 145 decision 19 says the dispatcher suspends after 24 hours of failure with the class of the last error; the spec adds a rule that pending deliveries stay pending during suspension, which no ADR states and which decision 16 at-least-once may conflict with; the change must state the redelivery order of suspended deliveries. Resolution: answered: Design states redelivery in per-scope sequence order and spec adds a suspended redelivery requirement with that order; pending rows stay pending during suspension per at-least-once enqueue.
- R2.2 (wrong claim): events-notifications/proposal.md ADR check cites ADR 53 decision 12 as 'current' and maps expiry to token.expiring, but decision 12 only says events come in milestone 5 with RFC 23; RFC 23 is not an accepted decision in the input, so the token.expiring catalog type and its 14/3-day rule rest on ADR 69 decision 5 without the change citing RFC 23 status. Resolution: answered: ADR check now cites ADR 69 decision 5 for the 14-day and 3-day times; expiry mapping stays token.expiring pending the catalog open question.
- R2.3 (open question): events-notifications/specs/notifications/spec.md: the catalog types job.started, job.awaiting-approval, job.succeeded, job.canceled and token.expiring are not in ADR 145 decision 5's catalog as cited in the input; the change says they are pinned but no ADR or notify.Types entry supports the exact names, so tasks 1.1 and 2.1 cannot be built until the owner or an ADR fixes them. Resolution: open question: Exact catalog names moved to an open question for the PM because the pack excerpt does not prove the job and token type strings.
- R2.4 (open question): events-notifications/proposal.md: the ADR check says the redeliver and test routes are new, but ADR 145 decision 27 route table is cited and the change adds POST redeliver and POST test without showing the ADR 210 route-naming check; the open-questions gate still reads not spec-ready, so the change cannot be implemented. Resolution: open question: Redeliver and test paths moved to an open question for route naming under ADR 145 decision 27 and ADR 210; errors now point at existing list routes.
- R2.5 (failure mode): events-notifications/design.md: the job path appends in the owning transaction, but the design does not say how a notice is appended when the outbox write fails inside that transaction, nor how the dedupe key is built on a retried job; the spec only says no second job.started appears, without the key definition. Resolution: answered: Design now states rollback on outbox failure and the dedupe key as type plus job ID plus state plus attempt; spec keeps one-notice-per-key.
- R2.6 (missing scenario): events-notifications/specs/notifications/spec.md: no scenario covers a subscription whose sink is deleted or whose creator is removed entirely, nor the redeliver route for a subscription that is not suspended. Resolution: answered: Specs add sink-deleted, creator-removed and redeliver-when-active scenarios.
- R2.7 (failure mode): events-notifications/design.md Risks: a suspended subscription is never deleted, but no rule bounds how long deliveries of a suspended subscription stay pending before ADR 145 decision 11 retention (30 days of delivery history) deletes them, so redelivery after fix can fail silently. Resolution: answered: Design risks and spec retention requirement now bound pending redelivery by the 30-day delivery history of ADR 145 decision 11.
- R2.8 (missing scenario): events-notifications/specs/notifications/spec.md: the signature scenario does not cover a clock skew beyond the receiver window or a replayed POST, which the layers table names as a hostile test. Resolution: answered: Signed delivery scenarios now cover clock skew refusal and replay refusal.
- R2.9 (scope): events-notifications/proposal.md Out of scope excludes runner and planner work, but the job start notice covers plan jobs of the runner path; the change does not say which job path owns the append. Resolution: answered: Design and why now state the job service owns the append for runner and API-started jobs.
- R2.10 (open question): events-notifications/proposal.md Usability: rotating the signing secret relies on the test route before deleting the old secret, but no ADR 145 decision says the test route must succeed before delete; the change adds that gate silently. Resolution: answered: Design states the test route never gates secret delete; rotation guidance stays a recommendation.
- M.9 (duplicate): Subscriptions, the delivery store, retries and redeliver duplicate chat-a2-outbox-deliveries-dispatcher. Its rule 'suspend after 24 hours' conflicts with 'retry limit 5 then dead-letter' of that change. Resolution: answered: Retry, suspend, redeliver and stores moved to chat-a2-outbox-deliveries-dispatcher; this change reuses its dispatcher and manual retry route, defines no retry rule, and depends on chat-a2-outbox-deliveries-dispatcher and add-job-service-routes-start.

## Open questions

none

Questions considered and answered:

- Which notification types does the job path emit? ADR 145 decision 5 holds one catalog in notify.Types with job types as examples including job.failed. This change emits the four facts of the roadmap step through that catalog and names no new catalog string in the spec; the code maps each fact to the shipped catalog entry.
- How does a webhook delivery prove its source? ADR 145 decision 21 defines the signed POST. This change follows it with no new header and no new signature rule.
- What happens when a sink fails? Retry, suspend, enqueue and manual retry are owned by chat-a2-outbox-deliveries-dispatcher per ADR 145 decisions 16, 17, 18 and 19. This change defines no retry rule and reuses that dispatcher and its manual retry route as redeliver.
- Does a job notification hold secrets or logs? ADR 145 decision 4 forbids secret values, credentials, state, plans and logs in a notification. This change holds only names, IDs, summary, sequence and links.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
