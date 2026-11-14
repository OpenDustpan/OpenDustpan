## Context

Part A2 of the chat theme persists notifications and delivers them. Issue 1172 names the stores of Outbox, Subscriptions and Deliveries in both dialects, the dispatcher, the delivery operation, and one emitting domain, and states that it follows A1. Existing code references the notify ports from services. A1 owns the notify model and the sink contract. This change owns only persistence, match, delivery rows, and dispatch, and owns the single retry and dead-letter rule that later notice work uses.

## Decisions

1. **Outbox then dispatch.** Append and dispatch stay in separate steps. The emitting domain appends rows to the outbox in the same transaction as its own write. A durable delivery operation claims a pending outbox row under a lease, snapshots the matching active subscriptions in the claim transaction, writes one delivery row per match, then calls sinks.
2. **Idempotency key per delivery.** Each delivery row carries a stable idempotency key made from the outbox row ID and the subscription ID, with a unique constraint on that pair. A reclaim after a crash replays the same keys and writes no second row per subscription.
3. **Dispatcher owns fan-out state.** The dispatcher keeps an unexported cursor and lease per delivery row. Only its constructor builds a valid dispatcher, and the constructor rejects a nil outbox, nil subscriptions, nil deliveries, or nil sink registry. The lease has an expiry from the server clock, and an expired lease becomes claimable again.
4. **Per-subscription isolation.** Per-subscription state isolates failures. The failure, retry, or dead-letter state of one delivery never blocks the send, retry, or mark of another delivery of the same outbox row.

## Structure

Unexported type dispatcher with unexported fields outbox, subscriptions, deliveries, sinks, and clock, built only by NewDispatcher which checks invariants.

## Risks

- Duplicate delivery after a crash between sink send and delivery mark.
- Two dispatchers claim the same outbox row.
- A failing sink blocks delivery to a working sink of the same notification.
- Unbounded growth of the outbox and deliveries tables.
