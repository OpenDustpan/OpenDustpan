# 145. One notification model with sinks for email, chat and signed webhooks

Status: accepted · 2026-10-18. Answers part of RFC 23. Decisions 3, 13, 15, 24 and 27 and the identity consequence superseded in part by ADR 149. Route paths and `server:` action names superseded in part by ADR 210. Decisions 1 and 24 extended by ADR 234. Decision 5 extended by ADR 241. The home region amended by ADR 248 (primary installation)

Changed by:

- ADR 146 extends decision 5: the catalog gets the chat link made and replaced
  types.
- ADR 149 changes decisions 15 and 24: the creator rule, BeforeAttempt and the
  recipients.
- ADR 149 extends decisions 3, 13 and 27: dedupe-key hash, sealed refs, 404
  rule and the identity consequence.
- ADR 197 extends decision 5: six change-order notification types join the
  catalog.
- ADR 203 extends decision 5: the profile notification types.
- ADR 204 extends decision 5: the issuer notification types.
- ADR 210 changes: the route paths, action names and the after paging
  parameter.
- ADR 234 extends decisions 1 and 24: notices rendered in the language of the
  recipient.
- ADR 241 extends decision 5: the import event types.
- ADR 248 changes decision 9: each namespace has its own primary installation.
- ADR 262 extends: the open question on the first chat tools.
- ADR 315 extends: the event lock.held-too-long.

## Context

[RFC 23](../rfc/0023-events-and-notifications.md) proposes one internal
event stream, destinations as adapters (generic HTTP with an HMAC
signature, chat tools, email), and asks two questions: the delivery
guarantee, and whether a policy routes the stream or subscriptions are
enough.

On 17 October 2026 the project owner decided:

- One model, the model of RFC 23, with sinks: email, chat webhooks and a
  signed generic webhook.
- Chat offers safe actions only (ADR 146).
- Reminders and escalations use the business calendar (ADR 140).

On 18 October 2026 the project owner decided that the term is
**notification** and the package is `pkg/notify`. In `GLOSSARY.md`,
"Event" is one entry in the progress record of a Pulumi update, and
"event" is a word to avoid for an audit entry. A tool's own events, for
example the events of the job state table (ADR 100), keep their name.

The designs of 17 and 18 October need notifications: job states (ADR 100),
freezes (ADR 141), break-glass reviews (ADR 142), on-call handoffs (ADR
143), budgets (ADR 144) and incidents (ADR 147).

[ADR 60](0060-audit-log-port-and-log-sinks.md) ships the audit log to log
sinks. The audit log is the record. A notification is a fact for people
and tools to act on. The two stay apart: a notification sink is not an
audit sink.

## Decision

### The notification

1. **One shape.** `notify.Notification` has an ID with the prefix `ntf-`, a
   type from one catalog, a scope (a namespace or `@server`), the object and
   its stable ID, the subject and the agent that caused it, a severity
   (`info`, `warning` or `critical`), a summary of at most 500 bytes, up to
   10 links, up to 20 string attributes, up to 50 recipients, a dedupe key,
   a time and a sequence. `Notification.Validate` holds the bounds.
2. **Links.** A link has a title, a kind and an `http` or `https` URL of at
   most 2 KiB. The kinds are `console` and the kinds of a link in the
   context of an object (ADR 160): `docs`, `runbook`, `dashboard`,
   `repository` and `other`. The server never sends a request to a link,
   so a link can name an internal host.
3. **A notification never fails its write.** A domain store calls
   `Notification.Fit` before it appends. `Fit` cuts each text to its bound
   at a rune boundary, drops a link or an attribute that is not valid, and
   keeps the first entries up to each count. An empty summary becomes the
   type, and an empty dedupe key becomes the ID. So a long summary or a bad
   link from the context of a unit never fails a job, a freeze or a review.
   `Fit` does not change the ID, the type, the scope, the severity or the
   time. An error in those is an error of the code, and `Validate` refuses
   it.
4. **No secret in a notification.** A notification never holds a secret
   value, a credential, state, a plan or a log. It holds names, IDs, a
   summary and links. A reader follows a link to the console, where the
   actions of the reader decide what it sees.
5. **One catalog of types.** `notify.Types` lists every type, for example
   `job.failed`, `freeze.upcoming`, `break-glass.review-notice`,
   `on-call.handoff`, `budget.threshold` and `incident.opened`. The job
   types are the terminal states of ADR 100 and the two states a person
   acts on: `job.started`, `job.awaiting-approval`, `job.succeeded`,
   `job.failed`, `job.canceled`, `job.rejected` and `job.expired`. This
   answers the open question of ADR 100 on the list of job notifications.
   A new type is a change of the catalog and of the reference
   documentation. A filter that names no type of the catalog fails, so a
   typing error shows at the save.
6. **Runbooks and console links.** A notification about a unit carries a
   link to the page of the object in the console and, when the unit has
   one, a link to its runbook (ADR 143 decision 14).

### The outbox

7. **A notification commits with its change.** The store of each domain
   appends the notifications of a change in the transaction of the change,
   as it appends the audit entries. A notification never exists for a
   change that rolled back, and a committed change never loses its
   notification. `notify.Outbox.Append` is for a notification that no
   domain write carries, for example a notice of a schedule.
8. **One notification for each fact.** The outbox keeps one notification
   for each scope and dedupe key. An append of a second notification with
   the same scope and dedupe key skips it, with no error. So a schedule
   that runs twice, or a domain write that retries, makes no second
   notification.
9. **A sequence for each scope.** As the audit log of ADR 60 does, each
   scope has its own sequence. A namespace has its own home region, so one
   counter for the installation would join the writes of all namespaces.
   A dispatcher keeps one cursor for each scope. It lists the scopes with
   new notifications (`Outbox.Scopes`), reads the notifications after its
   cursor (`Outbox.After`), and moves its cursor from the old value only
   (`Outbox.MoveCursor`). So two instances never route one notification
   twice. An adapter that cannot keep the order of sequences for
   transactions that commit out of order waits for them, as the audit
   shipper of ADR 60 does.
10. **Delivery order is not guaranteed.** Retries and claims by more than
    one instance can deliver a later notification first. A receiver orders
    the notifications of a scope by `sequence`.
11. **Retention.** The server keeps notifications for 32 days and the
    delivery history for 30 days. A durable schedule deletes older rows.
    The dedupe key of a notification holds while the notification is
    kept. A domain that appends a notification with its dedupe key first
    and then saves its own status (ADR 144 decision 12) makes no second
    notification when it repeats the write within a month.

### Fences

12. **Every write carries a fence.** Each write of `Outbox`,
    `Subscriptions` and `Deliveries` carries the fence of the scope of its
    row: the fence of the namespace for a row in a namespace, and the zero
    fence for a row of `@server`. The store checks it in the transaction of
    the write. A stale fence fails with `namespace.ErrFenced`. A fence that
    does not belong to the scope of the row fails with `ErrInvalid` for a
    new row, and with `ErrNotFound` for a stored row that the caller names
    by its ID. The conformance suite runs each write with a zero fence, the
    fence of another namespace and a stale fence.

### Subscriptions and sinks

13. **A sink is a destination.** `notify.Sink` belongs to a scope and has a
    kind:
    - `email`: one address, through the SMTP relay of the installation.
    - `chat`: the incoming webhook URL of a chat tool. An adapter for each
      chat tool renders the message in the format of that tool.
    - `webhook`: an `https` URL that receives the notification as JSON,
      signed.

    The secrets of a sink are secret items of the scope (ADR 23): the
    signing keys of a webhook, and the URL of a chat webhook. The URL of a
    chat webhook holds its token, so the whole URL is the secret. The
    `Target` of a chat sink holds only the host of the URL
    (`notify.ChatHost`), and the chat sender sends only to a URL whose host
    is that `Target`. The server makes the signing key of a webhook and
    answers it once, at the create. The API never answers a secret
    again.
14. **A subscription routes notifications.** `notify.Subscription` sends
    the notifications of its scope that match its filter to one sink. A
    filter selects by type (`job.failed` or `job.*`), by object (a unit
    reference or `opentofu:shop/*`), and by a minimum severity. A
    subscription of `@server` receives the notifications of `@server` only.
    This answers RFC 23: subscriptions with filters are enough. A policy
    does not route notifications.
15. **The creator decides what flows out.** A subscription records its
    creator. Before each delivery, the dispatcher checks two things:
    - The creator still holds `namespace:manage-notifications` and
      `namespace:read` on the scope. When the creator lost either action,
      the dispatcher suspends the subscription, and sends the notification
      `subscription.suspended` to the administrators of the scope. A
      suspended subscription delivers nothing until a person with the
      action resumes it.
    - The creator can read the notification, through the `notify.Reader`
      port: the actions of `notify.ScopeActions` on the scope, and for a
      unit the read action of the unit with its unit and collection
      grants. A cost or budget notification also needs
      `namespace:read-cost`. When the creator cannot read the
      notification, the subscription skips it. So a grant that hides a
      unit also hides its notifications. `notify.Route` applies the check
      when the dispatcher routes.

    A notice to a person has the same rule: `notify.Readers` keeps only the
    recipients that can read the notification.

    Each delivery records its principal (`Delivery.Principal`): the
    creator of the subscription, or the recipient of a notice. Access can
    change between the route and an attempt, and a notice can wait for
    quiet hours. So before each attempt the delivery operation calls
    `notify.BeforeAttempt`. It calls `Reader.CanRead` for the principal
    and, for a delivery to a contact, `Contacts.ContactsOf`. When the
    principal can no longer read the notification, or the contact is no
    longer a contact of the principal, the operation records the delivery
    as `skipped` and sends nothing. When a port cannot decide, the
    operation tries again later, as for `ErrTemporary`.

### Delivery

16. **At least once.** The dispatcher enqueues one delivery for each
    matching subscription (`Deliveries.Enqueue`), and for each personal
    contact of each recipient. A second enqueue of the same scope,
    notification, subscription and contact makes no second delivery. An
    enqueue of a delivery ID that exists fails: with `ErrInvalid` in the
    scope of the fence, and with `ErrNotFound` in another scope, so it never
    replaces a delivery. A receiver can get a notification
    twice, so each delivery carries the notification ID, and the dedupe key
    names the fact.
17. **Claims.** A delivery operation takes the due deliveries of a scope
    with `Deliveries.Claim`, as a runner of `pkg/durable` claims an
    operation: in one transaction, with an owner, a lease and a new
    version. A claim whose lease passed is not live, so another instance
    takes the delivery when its holder stopped. `Deliveries.Record` stores
    the result only for the owner and the version of a live claim, so a
    holder that lost its claim cannot overwrite the result of the next
    one. It changes only the state, the count of attempts, the next
    attempt, the class of the last error and the time of delivery. It
    never moves a delivery to another subscription, contact or principal.
18. **Retries.** A failed attempt with `ErrTemporary` retries after 30
    seconds, then doubles the wait up to 1 hour, for at most 15 attempts.
    When the receiver sends `Retry-After`, the sender returns a
    `notify.RetryAfterError`, and the next attempt waits at least that
    long, up to 24 hours. `notify.ParseRetryAfter` reads the header: a
    count of seconds or an HTTP date. It caps the wait at 24 hours, makes
    a past date no wait, and ignores a negative or unparsable value. An attempt with `ErrPermanent` (an unknown
    address, or a 4xx other than 408 and 429) or with `egress.ErrRefused`
    stops at once. The state of a stopped delivery becomes `abandoned`. A
    person can send a delivered, abandoned or skipped delivery again
    (`Deliveries.Redeliver`). A pending delivery fails with `ErrConflict`,
    because a live claim can hold it. This answers RFC 23: at least once,
    with retries, a delivery history and a redeliver.
19. **A sink that fails for a day.** When every delivery of a subscription
    failed for 24 hours, the dispatcher suspends it, with the class of the
    last error as the reason, and sends `subscription.suspended`.
20. **Outbound rules.** The chat and webhook senders post through one dial
    guard, which the email sender does not need. The package `pkg/egress`
    holds the rules, and `pkg/egress/egresshttp` applies them:
    - `Sink.Validate` calls `egress.CheckURL` for `https` only, when a
      person creates or changes a sink. The test route of a sink runs the check
      again before it sends. The check refuses another scheme, user
      information, a fragment, `localhost`, and an IP address that is not
      a public unicast address, for example `169.254.169.254` or `::1`.
    - The senders use `egresshttp.Client(egress.NoRedirect,
      egress.HTTPSOnly)`. Its dial guard checks the resolved address of
      each connection, so a DNS answer that changes after the check
      cannot reach a private, loopback or link-local address. The senders
      follow no redirect, and read at most `egress.MaxBodyBytes` of an
      answer.
    - A delivery stores only the class of its last error (`refused`,
      `permanent` or `temporary`, from `notify.Class`). It never stores the
      address, the text of the network error, the body of the answer or a
      secret. The store refuses any other text.

### The signed webhook

21. **The signature.** A webhook delivery is an HTTP `POST` of the JSON of
    the notification, with three headers:
    - `Dustpan-Notification-Id`: the notification ID,
    - `Dustpan-Timestamp`: the time of the request in Unix seconds,
    - `Dustpan-Signature`: one `v1=` value for each active signing secret
      of the sink, joined with `,`, for example `v1=<a>,v1=<b>`. Each value
      is the hex HMAC-SHA256, keyed with one secret, over the notification
      ID, `.`, the timestamp, `.` and the body.

    `notify.Sign` makes the value, and `notify.Verify` shows a receiver how
    to check it: the receiver accepts the delivery when one value matches
    one of its secrets. `Verify` compares each pair with `hmac.Equal` and
    does not stop at the first match. It refuses more than two values and a
    timestamp more than 5 minutes from its clock. The notification ID is in
    the signature, so a receiver can drop a second delivery of one
    notification with trust. The log webhook of ADR 60 signs a batch with
    no ID. The two schemes stay apart, because a batch of log records has
    no single ID.
22. **Two active secrets.** A webhook sink has one or two active signing
    secrets. To rotate, a person adds a second secret, moves the receiver
    to it, and then deletes the first. During the change, each delivery
    carries both signatures, so the receiver refuses no delivery. This
    answers the open question on rotation.
23. **The JSON.** The body has camelCase fields: `schemaVersion` (1), `id`,
    `type`, `scope`, `object`, `objectId`, `subject`, `agent`, `severity`,
    `summary`, `links`, `attributes`, `dedupeKey`, `at` (Unix
    milliseconds) and `sequence`. A new field is an addition. A change of a
    field gets a new `schemaVersion`.

### Notices to people

24. **Recipients.** A notice names recipients: principals or groups. The
    `notify.Contacts` port gives the contacts of each person: the verified
    email address, and the chat identity that the person linked (ADR 146).
    A chat sink of the kind `chat` cannot send a direct message through an
    incoming webhook. A personal chat notice needs a chat adapter that can
    send one. Without it, personal notices go by email. The delivery
    operation reads the contacts again before each attempt (decision 15).
25. **Quiet hours.** A personal notice of the severity `info` or `warning`
    waits for the business hours of the calendar of the person (ADR 140
    decision 4). A `critical` notice goes at once. A delivery to a
    subscription goes at once: the team that made the subscription chose
    its channel. `notify.Quiet` holds the rule. A notice that waits is
    checked again before it goes (decision 15), so it never reaches a
    person who lost access while it waited.
26. **Reminders and escalations.** The features that need them (ADR 142)
    compute their steps on the business calendar and write one
    notification for each step, with recipients and a dedupe key such as
    `bgr-1/overdue`. The notification model sends each one. It does not
    compute steps itself.

### API, actions and audit

27. **Routes.**

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/namespaces/{ns}/notifications?after=&type=` | `namespace:read` | namespace |
    | `GET /api/namespaces/{ns}/sinks[/{id}]`, `.../subscriptions[/{id}]` | `namespace:read` | namespace |
    | `POST`, `PUT`, `DELETE` of sinks and subscriptions of a namespace | `namespace:manage-notifications` | namespace |
    | `GET /api/namespaces/{ns}/subscriptions/{id}/deliveries` | `namespace:read` | namespace (`Deliveries.History` reads only the deliveries of `{ns}`) |
    | `POST /api/namespaces/{ns}/deliveries/{id}/redeliver` | `namespace:manage-notifications` | namespace |
    | `POST /api/namespaces/{ns}/sinks/{id}/test` | `namespace:manage-notifications` | namespace |
    | the same routes under `/api/server/` | `server:observe`, `server:manage-notifications` | server |

    The notification list filters the notifications with `notify.Reader`
    for the caller, so a caller sees no notification of a unit that it
    cannot read, and no cost or budget notification without
    `namespace:read-cost`. A read of a sink shows its kind, its name and
    its target: the address of an email sink, the URL of a webhook, and
    only the host of a chat webhook. It never shows a secret. The delivery
    history of a subscription shows only the notification ID and the
    class of the error, not the summary.
28. **Audit entries.** A create, a change and a delete of a sink or a
    subscription, a redeliver and a test write one entry each, of the kind
    `change`, in the scope of the sink. The entry never holds a secret of a
    sink. A delivery writes no entry. The delivery history is its record.
29. **Ports with conformance first.** `notify.Outbox`,
    `notify.Subscriptions` and `notify.Deliveries` get the suite
    `pkg/notify/notifytest`, with a reference adapter in memory.
    `notify.Sender` gets its suite with its first adapter, against a test
    server.

## Requests to other designs

- **Each domain store** that writes a change with a notification (jobs,
  freezes, reviews, incidents, budgets) calls `Notification.Fit` and
  appends the notification in the transaction of the change, next to its
  audit entry, with the fence of the change.
- **Identity.** The actions `namespace:manage-notifications` and
  `server:manage-notifications` are new. The identity package implements
  `notify.Reader` and `notify.Contacts`. `Reader.CanRead` checks each
  action of `notify.ScopeActions`, which includes `namespace:read-cost`
  (ADR 144) for a cost or budget notification.
- **Audit (ADR 60).** No change. Notification sinks and log sinks stay
  apart.

## Consequences

- Reminders, escalations and integrations read one stream. A new feature
  adds types to the catalog, not a new delivery path.
- At least once means a receiver can get a notification twice, and in
  another order. The notification ID, the dedupe key and the sequence let
  it drop the second and put them in order.
- A grant that hides a unit costs one read check for each delivery.
- The email sender talks SMTP with the standard library and adds no
  dependency. Each chat adapter is new code for the format of its tool.
- `GLOSSARY.md` needs the terms Notification, Notification sink,
  Subscription, Delivery and Notice.

## Open questions

- Does the server serve the webhook API of the Pulumi Cloud-compatible API
  and the `notification-configurations` of the TFE-compatible API (RFC 23)?
  They are tool-compatible APIs and must keep the shapes of their tools. A
  later ADR can map them to subscriptions.
- Which chat tools get an adapter first?
- Is 30 days of retention right for the delivery history, or does it follow
  the retention of the audit log?
