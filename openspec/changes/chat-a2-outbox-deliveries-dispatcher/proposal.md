---
version: 6
---

Issue: #1172.

## Why

**Persona: namespace administrator.** A namespace administrator manages the stacks and workspaces of one namespace. The administrator needs chat notices about runs and changes to reach each subscribed channel with at-least-once delivery, and needs to see the state of each delivery.

The user path this change must make work:

1. **API.** Create a subscription with POST /api/dustpan/v1/namespaces/{namespace}/subscriptions with an access token of a principal that holds the notification action.
2. **API.** Cause the emitting domain to append a notification, then read GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries.
3. **Docs page.** Follow the docs page to find a failed delivery and redeliver it with POST /api/dustpan/v1/namespaces/{namespace}/deliveries/{id}/redeliver.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A delivery or subscription is absent. | The server answers 404 with the error code of the missing object. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions` |
| A principal without the grant redelivers. | The server answers 403 and names the missing action. | `GET /api/dustpan/v1/namespaces/{namespace}/subscriptions/{id}/deliveries` |

Evidence for the claims above:

- The Outbox port exists in pkg/notify. Proof: pkg/notify/ports.go:36
- The Subscriptions port exists in pkg/notify. Proof: pkg/notify/ports.go:136
- A service already appends to the Outbox port. Proof: internal/removalalertsvc/removalalertsvc.go:256: s.cfg.Outbox.Append(ctx, fence, []notify.Notification{note.Fit()})
- Subscription and delivery routes exist. Proof: The pack main list names GET/POST /api/dustpan/v1/namespaces/{}/subscriptions and POST /api/dustpan/v1/namespaces/{}/deliveries/{}/redeliver.
- The backup code already names portable outbox tables. Proof: pkg/backup/portable_notification_sinks.go:54

## What Changes

- Stores of Outbox, Subscriptions and Deliveries in both dialects, on the A1 ports
- The dispatcher and the delivery operation
- The removal alert service stays the first emitting domain

Out of scope:

- The notify model and the sink contract (A1)
- Other emitting domains
- Console views
- Step events-notifications as a whole

Depends on:

- none

Removed from Depends on (14 Nov 2026, not a code dependency):

- chat-a1-notify-model-and-sink-contract: the A1 model is on main; no such change exists

## Usability

Where the path still costs the person time or surprises them:

- The administrator cannot see why a message did not arrive unless each delivery shows its state and next retry time.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Outbox port exists. (`pkg/notify/ports.go:36`)
- Subscriptions port exists. (`pkg/notify/ports.go:136`)
- The removal alert service appends to the Outbox. (`internal/removalalertsvc/removalalertsvc.go:256`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR decide the claim and lease design of the outbox? The pack supplies no ADR text and lists no ADR. This change builds on the A1 ports on main (pkg/notify/ports.go:36) and adds no claim model of its own.
- Which domain emits first? The removal alert service. It already appends through the Outbox port (internal/removalalertsvc/removalalertsvc.go:256).

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
