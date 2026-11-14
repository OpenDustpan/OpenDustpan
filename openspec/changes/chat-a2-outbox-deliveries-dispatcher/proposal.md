---
version: 2
---

Issue: #1172.

## Why

**Persona: namespace administrator.** A namespace administrator manages the stacks and workspaces of one namespace and needs a record that chat notices about runs and changes reach the right channel once per subscription with at-least-once delivery.

The user path this change must make work:

1. **API.** Create a subscription for the namespace from the docs page through the API.
2. **API.** Trigger the emitting domain action and read the outbox and delivery rows.
3. **Docs page.** Follow the docs page to inspect a failed delivery and retry it.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Duplicate subscription create for the same sink and filter. | The server answers 409 with code subscription.conflict and message subscription exists, run `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions`. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions` |
| Append of a notification above the size limit. | The server answers 422 with code notification.oversize and message notification too large, run `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries`. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` |
| Append of a notification with invalid UTF-8. | The server answers 422 with code notification.invalid and message notification invalid, run `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries`. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` |
| Read of an outbox row, delivery row, or subscription that is absent. | The server answers 404 with code delivery.not-found and message delivery not found, run `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries`. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` |

Evidence for the claims above:

- Current code already references the notify Outbox port from a service. Proof: internal/removalalertsvc/removalalertsvc.go:67:	// Outbox receives the alert notification of the server scope.
- Current code already references the notify Subscriptions and Deliveries ports from a service. Proof: internal/subscriptionsvc/subscriptionsvc.go:5:// notify.Subscriptions and notify.Deliveries.
- The backup domain already names portable outbox tables. Proof: pkg/backup/portable_notification_sinks.go:54:// portableOutboxTables is the portable description of the outbox tables: the
- Issue #1172 scopes this part to stores in both dialects, the dispatcher, the delivery operation, and one emitting domain, and states that it follows A1. Proof: Part A2 of the chat theme (#930, RFC 98). Follows A1.

## What Changes

- Outbox, subscriptions, and deliveries stores in both dialects
- Dispatcher and delivery operation with retry and delivery state
- One emitting domain that appends a notification in its write transaction

Out of scope:

- Notify model and chat sink contract, owned by A1
- Chat sink kinds beyond the one emitting domain and the test sink
- Console views for outbox and deliveries
- Full milestone step events-notifications

Depends on:

- chat-a1-notify-model-and-sink-contract

## Usability

Where the path still costs the person time or surprises them:

- The namespace administrator cannot tell why a chat message never arrived without a per-delivery state and the next retry time.
- Subscription filters surprise when two subscriptions overlap and both fire for one event.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Notify Outbox port exists in pkg/notify. (`pkg/notify/ports.go:36`)
- Notify Subscriptions port exists in pkg/notify. (`pkg/notify/ports.go:136`)
- Subscriptions service reads subscriptions and deliveries ports. (`internal/subscriptionsvc/subscriptionsvc.go:58`)
- Removal alert service appends through the Outbox port. (`internal/removalalertsvc/removalalertsvc.go:256`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What are the retry limit, backoff schedule, lease expiry, clock source, and retention period?
   - Options: Retry limit 5 with exponential backoff within 24 hours, lease 60 seconds from the server clock, retention 30 days for terminal rows; Retry limit 10 with fixed delay, lease 300 seconds, retention 90 days for terminal rows.
   - Provisional decision (owner review 14 Nov): Exponential backoff for up to 24 hours with retry limit 5, then dead-letter. Use a lease of 60 seconds from the server clock and a retention of 30 days for sent terminal rows. Show all effective values in the API. Provisional decision (owner review 14 Nov): as stated in the reason.
   - Reason: One retry rule for all sinks avoids two behaviors. Provisional decision (owner review 14 Nov): retry limit 5 with exponential backoff within 24 hours then dead-letter, lease 60 seconds from the server clock, retention 30 days for sent terminal rows; dead-letter rows stay until manual retry or explicit delete. Benchmark: AWS: EventBridge retries for up to 24 hours by default, then sends to a dead-letter queue.
   - Persona impact: The administrator sees one predictable rule. Escape hatch: A manual retry recovers any dead-letter row.
2. Which domain is the one emitting domain of A2, and what is the A1 sink contract it uses?
   - Options: Removal alert service as the first emitting domain; Job state service as the first emitting domain.
   - Provisional decision (owner review 14 Nov): Make the removal alert service the first emitting domain with the A1 sink contract as written. The job service follows. Provisional decision (owner review 14 Nov): as stated in the reason.
   - Reason: The removal alert service already references the Outbox port. Provisional decision (owner review 14 Nov): removal alert service as the first emitting domain with the A1 sink contract as written; job service follows. Benchmark: Google: ship the first emitter that the launch needs, then add others.
   - Persona impact: The namespace administrator gets removal notices on one path. Escape hatch: The job service can emit next with no change to the outbox.

Questions considered and answered:

- Do any supplied ADRs already define the subscribed events or the delivery signature? No. The pack supplies no ADR text (adrs is empty) and the docs ADR index check names no chat, notify, outbox, or dispatcher decision. The issue body scopes A2 to stores, the dispatcher, the delivery operation, and one emitting domain that follows A1.
- Does this change define the chat sink contract? No. The issue body states this part follows A1. This change only persists rows, matches subscriptions, and dispatches with at-least-once delivery and a stable idempotency key.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
