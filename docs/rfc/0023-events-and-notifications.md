---
rfc: 23
title: Events, notifications and webhooks
status: accepted
created: 2026-10-10
adr: 145
---

# RFC 23. Events, notifications and webhooks

Status: accepted (ADR 145). It was a stub opened from the competitor review in
the research doc "IaC and TACOS landscape".

## Problem

Teams expect to hear about failed updates, drift and stuck locks. HCP
Terraform, Pulumi Cloud, Spacelift, Terramate, OTF and Terrakube send webhooks
and chat messages. Pulumi has a webhooks API that the CLI can manage, and TFE
has `notification-configurations`. RFC 13 sends webhooks for approvals only.

## First ideas

- One internal event stream: unit created, update or run started, finished,
  failed; drift found; lock held too long; approval waiting.
- Destinations as adapters: generic HTTP with an HMAC signature, Slack,
  Microsoft Teams, email.
- Serve the Pulumi Cloud webhooks API and TFE `notification-configurations`,
  each in its tool's own shape.
- Run triggers (RFC 24), drift (RFC 25) and approvals (RFC 13) all read the
  same stream.

## Open questions

- Delivery guarantees: at least once with retries, and a delivery history with
  redeliver (Terrakube has one)?
- Can a policy route events (Spacelift notification policies), or are
  subscriptions enough?

## Proposal for Milestone 5 (2026-10-30)

This section is ready to accept. ADR 145 decides the model, the outbox,
subscriptions, sinks, delivery, retries, the signature, the routes and the
audit entries. Milestone 5 needs three parts that the code does not have
yet: the stores and the routes of subscriptions and deliveries, the
dispatcher, and the notifications and the timeline of jobs. Facts about
other products are in the research note
[m5-precedents](../research/m5-precedents.md).

### State of the code on 2026-10-30

| Part | State |
| --- | --- |
| `pkg/notify` ports, `Notification`, `Fit`, `Route`, `Sign`, `Verify` | exist, with `notifytest` |
| Sinks: store, routes, test route, chat renderers (Slack, Teams, Mattermost, Matrix), webhook sender | exist |
| SQL adapters of `Outbox`, `Subscriptions` and `Deliveries` | missing |
| The dispatcher and the delivery operation | missing |
| A domain store that appends a notification in its transaction | missing for jobs, triggers and review requests |

### Questions and recommendations

1. **How the dispatcher wakes up.**
   - (a) The append of a notification starts the durable operation
     `notify.dispatch` with the start key `<scope>` in the same
     transaction, and a durable schedule `notify.dispatch` each minute is
     the fallback. An operation that runs drains the scope from its cursor
     and ends. A start while one runs is a no-op, and the next minute picks
     up what it missed.
   - (b) A durable schedule only, each minute.
   - (c) A listener on the database (PostgreSQL `LISTEN`).

   *Recommendation: (a).* A chat message about a failed apply that waits up
   to a minute is slow for a person who watches. (c) works for one dialect
   only. The fallback keeps the rule "a committed change never loses its
   notification" (ADR 145 decision 7) without a second queue.
2. **Which job transitions notify.** ADR 145 decision 5 fixes the seven job
   types. The dedupe key is `job/<job ID>/<state>`, so a retry of a
   transition makes no second notification.
   - (a) Subscriptions receive each type. Personal notices go to the
     eligible approvers at `job.awaiting-approval`, and to the starter at
     `job.failed`, `job.rejected` and `job.expired`.
   - (b) Subscriptions only. A person subscribes to each type.

   Precedent: HCP Terraform has a `run:needs_attention` trigger for a run
   that waits for a person. GitHub Actions notifies the person who started
   a workflow of its failure.
   *Recommendation: (a).* A gate that waits for an approver who gets no
   notice waits until it expires.
3. **The job timeline for the run page (RFC 52).** The console run page
   needs each transition of a job with its time, its actor and its reason.
   Notifications are not this record: they are kept 32 days and filtered by
   subscription.
   - (a) A **job event** table: one row for each transition, written by
     `job.Store` in the transaction of the transition, kept as long as the
     job record. The route `GET /api/dustpan/v1/jobs/{job_id}/events`
     answers them with `job:read`. "Event" keeps its meaning in the job
     model (ADR 145 context: a tool's own events keep their name).
   - (b) Read the audit entries of the job.
   - (c) Derive the timeline from the fields of the job.

   *Recommendation: (a).* The audit log has the entries of people, not the
   transitions that an executor makes (ADR 103 decision 9: "The end of a
   job by an executor writes no entry"). (c) loses the order of a replan
   and of a takeover.
4. **Types for triggers and review requests.** New types in the catalog:
   `trigger.start-refused` (a start that the job service refused, with the
   error class), `trigger.saver-lost-access` (ADR 267 decision 14),
   `review-request.plan-ready`, `review-request.apply-blocked` and
   `review-request.hold-left-open` (RFC 27), and `access-token.expiring`
   (7 days before the expiry, with the dedupe key
   `<token ID>/expiring`) and `access-token.expired`, which ADR 53 decision
   12 puts in Milestone 5. *Recommendation:* add them
   with the slice that writes them.
5. **The webhook APIs of the tools (open question of ADR 145).** HCP
   Terraform serves `notification-configurations`, and the `tfe` provider
   manages them. Pulumi Cloud serves webhooks, and the `pulumiservice`
   provider manages them. Neither CLI sends these calls.
   - (a) Map each one onto a sink and a subscription, with the e2e step of
     its provider, after Milestone 5.
   - (b) Serve them in Milestone 5.
   - (c) Never. Teams use the API of OpenDustpan and its own providers.

   *Owner decision.* Recommendation: (a). The roadmap item of Milestone 5
   is the model of ADR 145. These APIs are compatibility for a provider, not
   for a CLI, and the providers of OpenDustpan (ADR 270) cover the same need.
6. **The format of a generic webhook.** ADR 145 decision 21 uses its own
   headers (`Dustpan-Signature: v1=<hex>`). The Standard Webhooks spec uses
   `webhook-id`, `webhook-timestamp` and `webhook-signature` with base64,
   and GitLab sends that form.
   *Recommendation:* keep ADR 145. A receiver library of the spec cannot
   read our hex value, but a change is a new ADR, and nothing in Milestone
   5 needs it.

### Ports

- SQL adapters of `notify.Outbox`, `notify.Subscriptions` and
  `notify.Deliveries` in both dialects, each running `notifytest`.
- `notify.Dispatcher` (new, in `pkg/notify`): `Drain(ctx, scope)` reads
  after the cursor, calls `Route`, enqueues deliveries and moves the cursor.
  Its conformance cases: two dispatchers on one scope route each
  notification once, and a notification that commits out of order waits.
- `job.Store` appends the notifications and the job events of a transition
  in its transaction. `jobtest` gets a case for each type and for the
  dedupe key.
- `job.Events` (new): `List(ctx, job, after)` for the timeline route.

### End-to-end steps

1. Create a webhook sink and a subscription for `job.*` on a unit. A plan
   job of the real OpenTofu CLI on a test runner ends. The receiver of the
   e2e suite gets `job.started` and `job.succeeded`, checks the signature
   with `notify.Verify`, and drops a second delivery of one ID.
2. The receiver answers 503 three times, then 204. The delivery history
   shows four attempts and the state `delivered`. A redeliver sends it
   again with the same notification ID.
3. A person loses `unit:read` on the unit. The next job notification of
   that unit is `skipped` for the subscription of that person.
4. `GET /jobs/{job_id}/events` lists `queued`, `dispatched`, `running` and
   `succeeded` in order, with the actor of each.
5. An access token that expires in 7 days gives `access-token.expiring`
   once, also when the schedule runs twice.

### Security tests (ADR 250)

| Step | Test |
| --- | --- |
| Check at the boundary | A filter with an unknown type, a sink URL with `localhost`, an IP literal, user info or `http`: each refused. |
| Contain | A summary with CR, LF and control characters reaches the chat renderer and the JSON body escaped. A header value never holds CR or LF. |
| Limit | A receiver that answers with a body of 100 MiB: the sender reads at most `egress.MaxBodyBytes`. A receiver that never answers: the attempt ends at its timeout. A redirect is not followed. |
| Authorization at the point of use | A subscription whose creator lost `namespace:manage-notifications` is suspended before the next delivery. A delivery to a person who lost the read of the unit is skipped. |
| Fail closed | A DNS answer that changes to `127.0.0.1` after the check: the dial guard refuses, and the delivery is `abandoned` with the class `refused`. |

### Work items

M5-1 is the work item that builds the runner launcher routes, in progress
on 2026-10-30. An item that depends on M5-1 waits until it merges. Each
item that adds a migration is in the conflict group `store-migrations`. So
no two of them collide on the migration number or on the hashes of
`TestFrozenMigrations`.

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| EV-1 | SQL `Outbox`, `Subscriptions`, `Deliveries` in both dialects, migrations, backup tables | L | store-migrations | none |
| EV-2 | Subscription and delivery routes, redeliver, audit entries, docs page "Notifications" | M | api-notifications | EV-1 |
| EV-3 | `notify.Dispatcher`, the `notify.dispatch` operation and schedule, the delivery operation with retries and `BeforeAttempt` | L | notify-dispatch | EV-1 |
| EV-4 | Job notifications and job events in `job.Store` (with the migration of the job event table), the timeline route, `jobtest` cases | M | store-migrations | EV-1, M5-1 |
| EV-5 | Personal notices of question 2 through `notify.Contacts` | M | notify-dispatch | EV-3, EV-4 |
| EV-6 | e2e steps 1 to 5 | M | e2e-steps | EV-2, EV-3, EV-4, EV-7 |
| EV-7 | The notifications of an access token that expires (question 4), from a durable schedule | M | notify-dispatch | EV-3 |

## Outcome

Accepted as [ADR 145](../adr/0145-one-event-model-and-sinks.md), one event
model and sinks, on 2026-10-18. A question of this RFC that ADR 145 does not
decide needs a new RFC.
