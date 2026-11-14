## Context

Job and token notices use the outbox, subscriptions, dispatcher and signed webhook sender owned by chat-a2-outbox-deliveries-dispatcher. The job service appends in its own transaction. The dispatcher delivers outside the write.

## Decisions

1. **Emit job and token notices from the owning job transaction.** Emit four facts from the job service: job started, job waits for approval, job ended in a terminal state, and access token near expiry. Each append carries scope, stable unit ID or token ID, dedupe key built as type plus job ID plus state plus attempt, and links to the console page. When the outbox write fails, the owning transaction rolls back and no job state commits. Job lifecycle facts come from the job service owned by add-job-service-routes-start.
2. **Reuse the dispatcher of chat-a2 with no new retry rule.** Reuse the subscription filter, enqueue idempotence, claim, retry, suspend and manual retry route of chat-a2-outbox-deliveries-dispatcher with no new rule. The dispatcher checks the creator reach before each delivery. Redeliver after suspension uses the manual retry route of chat-a2 and sends pending deliveries in per-scope sequence order.
3. **Sign each webhook delivery.** Sign each webhook POST per the signature rule with the active sink secrets. Keep at most two active secrets for rotation. Post through the egress guard. The sink test checks the receiver and never gates the delete of a secret.

## Structure

Unexported notify.dispatcher with constructor that takes Outbox, Subscriptions, Deliveries and Sender ports. Only the constructor builds delivery attempts, so a path without a fence, a reach check or a signature does not compile into the send path.

## Risks

- A receiver that needs ordered notices orders by sequence per ADR 145 decision 10, because delivery order is not guaranteed.
- A suspended subscription needs an explicit manual retry after fix; the server never deletes the subscription on failure.
- Delivery history retention of 30 days bounds redelivery; a retry past retention answers that the history is gone.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| outbox append | Fit drops oversize text, bad links and secret-shaped fields before append; body holds only names, IDs, summary and links. | Append a notification with a secret value, a plan dump and an oversize summary; the stored body holds none of them and the write succeeds. |
| service dispatch | Dispatcher rechecks creator reach on namespace:manage-notifications and namespace:read before each delivery and skips delivery when the grant is gone. | Remove the creator grant after subscribe, then trigger a job start; no POST leaves the server and the delivery stays pending with reason. |
| sender egress plus signature | Sender validates sink URL allowlist, enforces timeout and size bounds, signs with HMAC over id plus timestamp, and the receiver rejects outside the clock window. | Replay a signed POST after the window and forge a POST with a wrong secret; both fail verification and the notice is ignored. |
