---
version: 7
---

Issue: #1172.

## Why

**Persona: namespace administrator.** A namespace administrator manages the stacks and workspaces of one namespace. The administrator connects a chat channel or a signed webhook to the namespace. The administrator expects each matching notification to arrive there at least once, and expects to see the state of each delivery.

Today the server stores the notifications, the sinks, the subscriptions and the deliveries. It shows the history of a subscription and it redelivers a stopped delivery. But nothing takes a notification from the outbox, makes the deliveries, sends them and records the result. The only message the server sends is the test message of a sink. The guides say so: the server does not yet send notifications to a destination by itself.

The user path this change must make work:

1. **API.** Create a sink with POST /api/dustpan/v1/namespaces/{namespace}/sinks and a subscription with POST /api/dustpan/v1/namespaces/{namespace}/subscriptions, with an access token of a principal that holds the notification action. These routes exist.
2. **Server.** An emitting domain appends a notification. The dispatcher routes it, sends it to the sink and records the result.
3. **API.** Read GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries. The answer shows `delivered`, or `pending` with `attempts`, `nextAt` and `lastError`, or `abandoned`.
4. **Docs page.** Follow the NEW page docs/guides/chat-notifications.md to find an `abandoned` delivery and send it again with POST /api/dustpan/v1/namespaces/{namespace}/deliveries/{id}/redeliver. This route exists.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The receiver is down or refuses the message. | The delivery stays `pending` with `lastError` `temporary` and the time of the next attempt. After 15 attempts it reads `abandoned`. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` |
| The subscription failed for 24 hours. | The subscription reads `suspended` with the class of the last error as the reason. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}` |
| The creator of the subscription lost access. | The deliveries read `skipped` and the subscription reads `suspended`. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}` |
| A principal without the grant redelivers. | The server answers 403 and names the missing action. This route exists. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` |

Evidence for the claims above (each checked on main):

- The three store ports exist with both SQL adapters. Proof: pkg/notify/ports.go:36 (Outbox), pkg/notify/ports.go:136 (Subscriptions), pkg/notify/ports.go:354 (Deliveries), internal/store/sqlite/notifications.go:172 (Append), internal/store/sqlite/notifications.go:841 (Enqueue), internal/store/sqlite/notifications.go:1042 (Claim), internal/store/sqlite/notifications.go:1143 (Record).
- The history and redeliver routes exist. Proof: internal/api/notifications/routes.go:92 and internal/api/notifications/routes.go:94.
- The state model and the retry rule exist. Proof: pkg/notify/ports.go:216-226 (pending, delivered, abandoned, skipped) and pkg/notify/route.go:864-879 (the retry constants and RetryWait).
- The routing and access functions have no production caller. Proof: `rg -n "notify\.Route\(|notify\.BeforeAttempt\(|notify\.RetryAt\(" --glob '!*_test.go' --glob '!pkg/notify/**'` prints nothing.
- Only the test route calls a sender. Proof: pkg/notify/sinks/test.go:60 calls Send, and pkg/dustpan/sinks.go:99-107 builds only the sink and subscription services.
- A service already appends to the Outbox. Proof: internal/removalalertsvc/removalalertsvc.go:256: s.cfg.Outbox.Append(ctx, fence, []notify.Notification{note.Fit()})
- A sink change already appends a notice. Proof: pkg/notify/route.go:627 (SinkChangedNotice).

## What Changes

- The dispatcher, in the NEW package internal/notifydispatch. It reads the outbox with its cursor, routes each notification with notify.Route and enqueues the deliveries. It claims the due deliveries, checks access with notify.BeforeAttempt and sends through the sender of the sink kind. It records the result with Deliveries.Record, retries with notify.RetryAt, abandons, and suspends a subscription that failed for notify.SuspendAfter.
- The port notify.Fetcher (NEW). It reads one stored notification by scope and ID. A delivery holds only the ID of its notification, and no port on main reads a notification by ID. Both SQL adapters implement it, and the conformance suite covers it. No table, column or migration changes.
- The exported function sinks.CheckAddress (NEW), made from the method checkAddress of the test route, so that the test route and the dispatcher apply one rule before a send.
- The function notify.SubscriptionFailingNotice (NEW): the notice subscription.suspended for a subscription that the dispatcher suspends.
- The wiring in pkg/dustpan: the server starts the dispatcher with the same senders as the test route, in the worker component only, and stops it with the server.
- The e2e step, the NEW docs page, and the edits of the pages that say the server does not send.

Out of scope, each with its proof on main:

- The notification model, the sink contract, the stores, the history route and the redeliver route. They exist (see Evidence).
- Personal notices: the Recipients of a notification, the contacts of a person, quiet hours and the email sender. Proof: `rg -n "ContactsOf" --glob '!*_test.go' --glob '!pkg/notify/**' .` prints nothing, and no email sender exists in pkg/notify. The dispatcher makes deliveries for subscriptions only and ignores Recipients. A delivery that has a contact is recorded as skipped.
- Routes for sinks and subscriptions of the server scope. Proof: `rg -n "server" internal/api/notifications/routes.go` prints no route path of that scope. The dispatcher routes the server scope, so the removal alert is tested at the service level with a subscription from a store call.
- The retention purge of notifications and of the delivery history. Proof: `rg -n -i "purge" pkg/notify internal/store/sqlite/notifications.go` finds no purge of notifications.
- Chat action buttons. The dispatcher sends no actions, as the test route does.
- Console views and the language of the recipient.

Depends on:

- notify-scope-namespace-id: it adds the first production notify.Reader (its task 3.3). The dispatcher needs a Reader because notify.Route and notify.BeforeAttempt call one. Proof: `rg -n "CanRead\(" --glob '!*_test.go' pkg/dustpan internal` finds no implementation on main.

Removed from Depends on (14 Nov 2026, not a code dependency):

- chat-a1-notify-model-and-sink-contract: the A1 model is on main

## Usability

- The administrator sees why a message did not arrive: each delivery shows its state, its attempts, its lastError class and its next attempt time. The history route shows all four on main.
- A suspended subscription is silent unless a notice tells the administrators. The dispatcher appends subscription.suspended for each suspend.
- The retry values are fixed constants of pkg/notify and are not a setting. The guide states them: first retry after 30 seconds, doubling to 1 hour, at most 15 attempts, suspend after 24 hours. The change adds no API field and no setting.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Outbox port with Append, Scopes, After, Cursor and MoveCursor. (`pkg/notify/ports.go:36`)
- Subscriptions port with PutSink, SinkSecrets, Sinks, PutSubscription and Subscriptions. PutSubscription sets CreatedBy to the subject of the audit entry. (`pkg/notify/ports.go:136`)
- Deliveries port with Enqueue, DueScopes, Claim, Record, History, CountBefore and Redeliver. Record needs the live claim. (`pkg/notify/ports.go:354`)
- The delivery states pending, delivered, abandoned and skipped. (`pkg/notify/ports.go:217`)
- notify.Route matches the subscriptions of a notification. (`pkg/notify/route.go:765`)
- notify.BeforeAttempt checks the access of the creator before an attempt. (`pkg/notify/route.go:803`)
- notify.SendsFor holds the three conditions of a send. (`pkg/notify/route.go:837`)
- notify.RetryWait gives the wait of an attempt, from the constants FirstRetry (30 seconds), MaxRetryWait (1 hour), MaxAttempts (15) and SuspendAfter (24 hours). (`pkg/notify/route.go:879`)
- notify.RetryAt gives the next attempt time or says to stop. (`pkg/notify/route.go:985`)
- notify.Class gives the class refused, permanent or temporary. (`pkg/notify/route.go:1010`)
- notify.SinkChangedNotice is the notice that PutSink appends. (`pkg/notify/route.go:627`)
- notify.Sender sends one message, with the notification ID as the idempotency key. (`pkg/notify/ports.go:507`)
- SinkSealer.OpenDestination opens the secret items for one send. (`pkg/notify/seal.go:217`)
- The test route checks the address before a send with checkAddress. (`pkg/notify/sinks/test.go:211`)
- The history and redeliver routes. (`internal/api/notifications/routes.go:92`)
- The audit shipper is a background loop that Server.Run starts with workerOnly. (`pkg/dustpan/auditsinks.go:93`)
- The removal alert service appends to the Outbox. (`internal/removalalertsvc/removalalertsvc.go:256`)

## ADR check

Each quote is copied from docs/adr/. The Changed-by chain and the owner tags come from docs/adr/INDEX.md and the ADR text.

**ADR 145 decisions 9, 15 to 20 and 28 (owner decisions of 17 and 18 October 2026).** Changed by ADR 146, 149, 197, 203, 204, 210, 234, 241, 248, 262, 315 and 334. The current rule is this ADR with those. Quotes:

- Decision 9: "A dispatcher keeps one cursor for each scope. It lists the scopes with new notifications (`Outbox.Scopes`), reads the notifications after its cursor (`Outbox.After`), and moves its cursor from the old value only (`Outbox.MoveCursor`). So two instances never route one notification twice."
- Decision 16: "The dispatcher enqueues one delivery for each matching subscription (`Deliveries.Enqueue`)." A second enqueue of the same key makes no second delivery.
- Decision 17: "`Deliveries.Record` stores the result only for the owner and the version of a live claim."
- Decision 18: a temporary failure retries after 30 seconds, doubling to 1 hour, for at most 15 attempts. An attempt with ErrPermanent or with egress.ErrRefused stops at once, and the state becomes `abandoned`.
- Decision 19: "When every delivery of a subscription failed for 24 hours, the dispatcher suspends it, with the class of the last error as the reason, and sends `subscription.suspended`."
- Decision 15: when the creator lost `namespace:manage-notifications` or `namespace:read`, the dispatcher suspends the subscription.
- Decision 20: the senders use the egress client with its dial guard, and a delivery stores only the class of its last error.
- Decision 28: "A delivery writes no entry. The delivery history is its record." This change follows it. An attempt writes no audit entry. A suspend changes a subscription, and PutSubscription takes an audit entry, so each suspend writes one.

**ADR 149 decisions 11, 13 and 14.** Decision 13 defines notify.SendsFor. Decision 14: before each attempt the operation calls notify.BeforeAttempt with the stored subscription. A refused attempt records `skipped` and sends nothing. When a port cannot decide, the operation tries again later as for ErrTemporary. The dispatcher does this. Decision 11: PutSubscription sets CreatedBy to the subject of the audit entry, so a dispatcher suspend sets CreatedBy to the dispatcher subject. A person who resumes the subscription becomes its creator.

**ADR 334 decision 2 (owner, 2026-11-07).** "The strict host list applies twice (owner, 2026-11-07)." The server checks at save time, and the sender checks again at send time, as separate layers. The dispatcher adds a third layer (sinks.CheckAddress) and removes none.

**ADR 250 decision 3 (owner), changed by ADR 382.** "Fail closed and record: ... Every denial and every use of a privilege goes to the audit log." The dispatcher fails closed everywhere. A skipped delivery is recorded as a state in the delivery history, as ADR 145 decision 28 says. It is not an audit entry. Open point for the PM, not an open question of the design: ADR 250 and ADR 145 decision 28 read differently here, and this change follows the narrower, newer rule for deliveries.

**ADR 370 (owner), decision 2.** "A housekeeping run never acts as `@server`." The closed list of eleven purposes has no notification dispatcher. The suspend needs an audit subject. This change takes it from Config.Actor, and the wiring passes history.ServerSubject, as the other background writers of main do. A later change that adds a purpose for the dispatcher changes that one value. This is no decision about the owner rule.

**ADR 248 decision 3.** Only the primary installation writes. The dispatcher takes its fence from WritableHere and skips a scope that answers ErrNotHome.

**ADR 362 (owner).** The dispatcher stores only IDs that the ports give and adds no stored reference. It passes each scope through as the port gives it, so notify-scope-namespace-id needs no edit here.

**ADR 66 and ADR 74.** The code and the user docs hold no internal reference. The dispatcher sends only to a destination that a principal saved.

## Open questions

none

Questions considered and answered:

- Which domain emits first? The removal alert service. It writes to the server scope, and no route makes a subscription of that scope. So the e2e step uses the notice that PutSink appends, and the removal alert is tested at the dispatcher level.
- How does the dispatcher read the notification of a delivery? Through the NEW port notify.Fetcher.
- Where does the dispatcher run? In the worker component only, like the audit shipper. Two instances are safe: MoveCursor gives ErrConflict to the second router, and Claim never gives one delivery to two live claims.
- Is a setting needed? No. The poll interval, the batch sizes and the lease are constants of the package.
- What happens to a notification with Recipients? The dispatcher ignores Recipients and routes to the matching subscriptions.
- What stops a loop with the suspend notice? notify.Route skips a subscription that is not active.
- What happens when the sink kind has no sender (email)? The delivery is abandoned with the class permanent.
