# 145. One event model with sinks for email, chat and signed webhooks

Status: accepted · 2026-10-18 · answers part of RFC 23

## Context

[RFC 23](../rfc/0023-events-and-notifications.md) proposes one internal
event stream, destinations as adapters (generic HTTP with an HMAC
signature, chat tools, email), and asks two questions: the delivery
guarantee, and whether a policy routes events or subscriptions are enough.

On 17 October 2026 the project owner decided:

- One event model, the model of RFC 23, with sinks: email, chat webhooks
  and a signed generic webhook.
- Chat actions are limited to safe ones (ADR 146).
- Reminders and escalations use the business calendar (ADR 140).

The designs of 17 and 18 October need events: job states (ADR 100), freezes
(ADR 141), break-glass reviews (ADR 142), on-call handoffs (ADR 143),
budgets (ADR 144) and incidents (ADR 147).

[ADR 60](0060-audit-log-port-and-log-sinks.md) ships the audit log to log
sinks. The audit log is the record. An event is a fact for people and tools
to act on. The two stay apart: an event sink is not an audit sink.

## Decision

### The event

1. **One shape.** `event.Event` has an ID with the prefix `evt-`, a type
   from one catalog, a scope (a namespace or `@server`), the object and its
   stable ID, the subject and the agent that caused it, a severity (`info`,
   `warning` or `critical`), a summary of at most 500 bytes, up to 10
   `https` links, up to 20 string attributes, up to 50 recipients, a dedupe
   key, a time and a sequence. `Event.Validate` holds the bounds.
2. **No secret in an event.** An event never holds a secret value, a
   credential, state, a plan or a log. It holds names, IDs, a summary and
   links. A reader follows a link to the console, where the actions of the
   reader decide what it sees.
3. **One catalog of types.** `event.Types` lists every type, for example
   `job.failed`, `freeze.upcoming`, `break-glass.review-notice`,
   `on-call.handoff`, `budget.threshold` and `incident.opened`. A new type
   is a change of the catalog and of the reference documentation. A filter
   that names no type of the catalog fails, so a typing error shows at the
   save.
4. **Runbooks and console links.** An event about a unit carries a link to
   the page of the object in the console and, when the unit has one, a link
   to its runbook (ADR 143 decision 14).

### The outbox

5. **An event commits with its change.** The store of each domain appends
   the events of a change in the transaction of the change, as it appends
   the audit entries. An event never exists for a change that rolled back,
   and a committed change never loses its event. `event.Outbox.Append` is
   for an event that no domain write carries, for example a notice of a
   schedule.
6. **One order.** The store gives each event the next sequence of the
   installation. A dispatcher reads the events after its cursor
   (`Outbox.After`) and moves its cursor from the old value only
   (`Outbox.MoveCursor`). So two instances never route one event twice. An
   adapter that cannot keep the order of sequences for transactions that
   commit out of order waits for them, as the audit shipper of ADR 60 does.
7. **Retention.** The server keeps events and the delivery history for 30
   days. A durable schedule deletes older rows.

### Subscriptions and sinks

8. **A sink is a destination.** `event.Sink` belongs to a scope and has a
   kind:
   - `email`: an address, through the SMTP relay of the installation.
   - `chat`: the incoming webhook URL of a chat tool. An adapter for each
     chat tool renders the message in the format of that tool.
   - `webhook`: an `https` URL that receives the event as JSON, signed.

   The secret of a sink (the signing key of a webhook, the token of a chat
   webhook) is a secret item of the scope (ADR 23). The server makes the
   signing key of a webhook and answers it once, at the create. The API
   never answers it again.
9. **A subscription routes events.** `event.Subscription` sends the events
   of its scope that match its filter to one sink. A filter selects by type
   (`job.failed` or `job.*`), by object (a unit reference or
   `opentofu:shop/*`), and by a minimum severity. A subscription of
   `@server` receives the events of `@server` only. This answers RFC 23:
   subscriptions with filters are enough. A policy does not route events.
10. **The creator decides what flows out.** A subscription records its
    creator. Before each delivery, the dispatcher checks that the creator
    still holds `namespace:manage-notifications` and `namespace:read` on
    the scope. When the creator lost either action, the dispatcher suspends
    the subscription, and sends the event `subscription.suspended` to the
    administrators of the scope. A suspended subscription delivers nothing
    until a person with the action resumes it.

### Delivery

11. **At least once.** The dispatcher enqueues one delivery for each
    matching subscription (`Deliveries.Enqueue`), and for each personal
    contact of each recipient. A second enqueue of the same event,
    subscription and contact is skipped. A durable operation sends each due
    delivery through the `event.Sender` of its kind. A receiver can get an
    event twice, so each delivery carries the event ID, and the dedupe key
    names the fact.
12. **Retries.** A failed attempt with `ErrTemporary` retries after 30
    seconds, then doubles the wait up to 1 hour, for at most 15 attempts.
    An attempt with `ErrPermanent` (an unknown address, or a 4xx other than
    408 and 429) stops at once. A stopped delivery is abandoned. A person
    can send it again (`Deliveries.Redeliver`). This answers RFC 23: at
    least once, with retries, a delivery history and a redeliver.
13. **A sink that fails for a day.** When every delivery of a subscription
    failed for 24 hours, the dispatcher suspends it, with the last error as
    the reason, and sends `subscription.suspended`.
14. **Outbound rules.** The chat and webhook senders post over `https`
    through the egress rules of the installation. They refuse a private,
    loopback or link-local address, never follow a redirect, and read at
    most 64 KiB of an answer. The last error of a delivery holds the status
    and a short reason, never the body of the answer or a secret.

### The signed webhook

15. **The signature.** A webhook delivery is an HTTP `POST` of the JSON of
    the event, with three headers:
    - `Dustpan-Event-Id`: the event ID,
    - `Dustpan-Timestamp`: the time of the request in Unix seconds,
    - `Dustpan-Signature`: `v1=` and the hex HMAC-SHA256, keyed with the
      secret of the sink, over the event ID, `.`, the timestamp, `.` and
      the body.

    `event.Sign` makes the value, and `event.Verify` shows a receiver how to
    check it: a constant-time compare, and a refusal of a timestamp more
    than 5 minutes from its clock. The event ID is in the signature, so a
    receiver can drop a second delivery of one event with trust. The log
    webhook of ADR 60 signs a batch with no ID. The two schemes stay apart,
    because a batch of log records has no single ID.
16. **The JSON.** The body has camelCase fields: `schemaVersion` (1), `id`,
    `type`, `scope`, `object`, `objectId`, `subject`, `agent`, `severity`,
    `summary`, `links`, `attributes`, `dedupeKey`, `at` (Unix
    milliseconds) and `sequence`. A new field is an addition. A change of a
    field gets a new `schemaVersion`.

### Notices to people

17. **Recipients.** A notice names recipients: principals or groups. The
    `event.Contacts` port gives the contacts of each person: the verified
    email address, and the chat identity that the person linked (ADR 146).
    A chat sink of the kind `chat` cannot send a direct message through an
    incoming webhook. A personal chat notice needs a chat adapter that can
    send one. Without it, personal notices go by email.
18. **Quiet hours.** A personal notice of the severity `info` or `warning`
    waits for the business hours of the calendar of the person (ADR 140
    decision 4). A `critical` notice goes at once. A delivery to a
    subscription goes at once: the team that made the subscription chose
    its channel. `event.Quiet` holds the rule.
19. **Reminders and escalations.** The features that need them (ADR 142)
    compute their steps on the business calendar and write one event for
    each step, with recipients and a dedupe key such as `bgr-1/overdue`.
    The event model sends each one. It does not compute steps itself.

### API, actions and audit

20. **Routes.**

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/namespaces/{ns}/events?after=&type=` | `namespace:read` | namespace |
    | `GET /api/namespaces/{ns}/sinks[/{id}]`, `.../subscriptions[/{id}]` | `namespace:read` | namespace |
    | `POST`, `PUT`, `DELETE` of sinks and subscriptions of a namespace | `namespace:manage-notifications` | namespace |
    | `GET /api/namespaces/{ns}/subscriptions/{id}/deliveries` | `namespace:read` | namespace |
    | `POST /api/namespaces/{ns}/deliveries/{id}/redeliver` | `namespace:manage-notifications` | namespace |
    | `POST /api/namespaces/{ns}/sinks/{id}/test` | `namespace:manage-notifications` | namespace |
    | the same routes under `/api/server/` | `server:observe`, `server:manage-notifications` | server |

    The event list filters the events by the read action of the caller on
    each object, so a caller sees no event of a unit that it cannot read.
21. **Audit entries.** A create, a change and a delete of a sink or a
    subscription, a redeliver and a test write one entry each, of the kind
    `change`, in the scope of the sink. The entry never holds a secret of a
    sink. A delivery writes no entry. The delivery history is its record.
22. **Ports with conformance first.** `event.Outbox`, `event.Subscriptions`
    and `event.Deliveries` get the suite `pkg/event/eventtest`, with a
    reference adapter in memory. `event.Sender` gets its suite with its
    first adapter, against a test server.

## Requests to other designs

- **Each domain store** that writes a change with an event (jobs, freezes,
  reviews, incidents, budgets) appends the event in the transaction of the
  change, next to its audit entry.
- **Audit (ADR 60).** No change. Event sinks and log sinks stay apart.

## Consequences

- Notifications, reminders, escalations and integrations read one stream.
  A new feature adds types to the catalog, not a new delivery path.
- At least once means a receiver can get an event twice. The event ID and
  the dedupe key let it drop the second.
- The email sender talks SMTP with the standard library and adds no
  dependency. Each chat adapter is new code for the format of its tool.
- `GLOSSARY.md` needs the terms Event, Sink, Subscription, Delivery and
  Notice.

## Open questions

- Does the server serve the webhook API of the Pulumi Cloud-compatible API
  and the `notification-configurations` of the TFE-compatible API (RFC 23)?
  They are tool-compatible APIs and must keep the shapes of their tools. A
  later ADR can map them to subscriptions.
- Rotation of the signing key of a webhook: one key at a time, or two keys
  for a period, with two signatures in the header?
- Which chat tools get an adapter first?
- Is 30 days of retention right for the delivery history, or does it follow
  the retention of the audit log?
