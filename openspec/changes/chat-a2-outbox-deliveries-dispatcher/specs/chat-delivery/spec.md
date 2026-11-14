## ADDED Requirements

### Requirement: Persist emitted notifications

When a domain emits a notification, the server SHALL append one pending row to the outbox in the same transaction.

#### Scenario: Emit in transaction
- **WHEN** the emitting domain commits its own write
- **THEN** the outbox holds one pending row with the namespace, the event name, and the payload

#### Scenario: Removal alert emits
- **WHEN** the removal alert service records a removal alert
- **THEN** the outbox holds one pending server-scope removal alert row

### Requirement: Reject hostile payload

When a notification exceeds the size limit or holds invalid UTF-8, the server SHALL refuse the append with 422.

#### Scenario: Above limit
- **WHEN** a domain appends a notification above the size limit
- **THEN** the server answers 422 with code notification.oversize and appends no row

#### Scenario: At limit
- **WHEN** a domain appends a notification exactly at the size limit
- **THEN** the server appends one pending row

#### Scenario: Invalid text
- **WHEN** a domain appends a notification with invalid UTF-8
- **THEN** the server answers 422 with code notification.invalid and appends no row

### Requirement: Reject duplicate subscription

When a principal creates a duplicate active subscription, the server SHALL refuse the create with 409.

#### Scenario: Same sink and filter
- **WHEN** a principal creates a subscription with the same sink and filter as an active subscription
- **THEN** the server answers 409 with code subscription.conflict

### Requirement: Claim outbox row under lease

When the dispatcher claims a pending outbox row, the server SHALL hold the row under a lease with an expiry.

#### Scenario: Single claim wins
- **WHEN** two dispatchers claim the same pending outbox row
- **THEN** only one dispatcher holds the lease and the other sees the row as claimed

#### Scenario: Expired lease reclaimed
- **WHEN** the lease expires before the holder marks the row dispatched
- **THEN** another dispatcher claims the row again

### Requirement: Snapshot match set at claim

When the dispatcher claims a pending outbox row, the server SHALL fix the matching active subscription set in the same claim transaction.

#### Scenario: Late subscription ignored
- **WHEN** a principal creates a subscription after the claim transaction
- **THEN** the dispatcher writes no delivery row for that subscription in this dispatch

#### Scenario: Paused after claim ignored
- **WHEN** a principal pauses a matched subscription after the claim transaction
- **THEN** the dispatcher still writes the delivery row fixed at claim time

### Requirement: Fan out to subscriptions in isolation

When the dispatcher claims a pending outbox row, the server SHALL fan out one delivery per matching active subscription without letting one failed delivery block another delivery.

#### Scenario: Match and deliver
- **WHEN** the dispatcher claims a pending outbox row
- **THEN** the server writes one delivery row per matching active subscription and sends the payload to each sink

#### Scenario: One sink fails
- **WHEN** one sink of the same notification fails
- **THEN** the deliveries to the working sinks still read sent

#### Scenario: No match or paused
- **WHEN** a notification matches no active subscription or the subscription is paused at claim time
- **THEN** the server writes no delivery row for that subscription and marks the outbox row dispatched

### Requirement: Keep fan-out idempotent on reclaim

When a dispatcher reclaims an outbox row, the server SHALL write no second delivery row for the same outbox row and subscription.

#### Scenario: Crash after partial fan-out
- **WHEN** the dispatcher crashes after it writes some delivery rows and before it marks the outbox row dispatched
- **THEN** the reclaim writes delivery rows only for matched subscriptions with no row and marks the outbox row dispatched

### Requirement: Key each delivery

When the dispatcher writes a delivery row, the server SHALL assign a stable idempotency key derived from the outbox row ID and the subscription ID.

#### Scenario: Retry repeats key
- **WHEN** the dispatcher retries a delivery after a crash between send and mark
- **THEN** the sink sees the same idempotency key and keeps one send

### Requirement: Retry then dead-letter then manual retry

When a sink send fails, the server SHALL retry with backoff to a retry limit and then mark the delivery dead-letter.

#### Scenario: Retry with backoff
- **WHEN** a sink send fails before the retry limit
- **THEN** the delivery row reads failed with the error code and the next retry time

#### Scenario: Dead-letter after limit
- **WHEN** a sink send fails at the retry limit
- **THEN** the delivery row reads dead-letter and the dispatcher stops automatic retry

#### Scenario: Manual retry
- **WHEN** a principal retries a dead-letter delivery
- **THEN** the delivery row returns to pending with a new next retry time

#### Scenario: Retry refused
- **WHEN** a principal retries a delivery that is not dead-letter
- **THEN** the server answers 409 with code delivery.not-dead-letter

#### Scenario: Race with automatic retry
- **WHEN** a manual retry races an automatic retry of the same delivery
- **THEN** only one retry wins and the other sees the updated state

### Requirement: Track delivery state

When a sink send ends, the server SHALL record the outcome on the delivery row.

#### Scenario: Success and failure
- **WHEN** a sink send succeeds or fails
- **THEN** the delivery row reads sent with the sent time, or reads failed with the error code and the next retry time

#### Scenario: Error truncated
- **WHEN** a sink returns an oversize error message
- **THEN** the delivery row keeps at most 1000 characters of the sink error

### Requirement: Retain and purge records

When the retention period passes after dispatch, the server SHALL purge the dispatched outbox row and its sent terminal delivery rows.

#### Scenario: Purge after retention
- **WHEN** the retention period passes after dispatch
- **THEN** the server removes the dispatched outbox row and its sent terminal delivery rows

#### Scenario: Dead-letter kept
- **WHEN** the retention period passes while a delivery still reads dead-letter
- **THEN** the server keeps the dead-letter row until manual retry or explicit delete

### Requirement: Guard read routes

When a principal reads subscriptions, outbox rows, or delivery rows, the server SHALL check the read grant on the namespace.

#### Scenario: Missing row
- **WHEN** a principal reads an absent subscription, outbox row, or delivery row
- **THEN** the server answers 404 with code subscription.not-found or delivery.not-found

#### Scenario: No grant
- **WHEN** a principal without the read grant reads subscriptions, outbox rows, or delivery rows
- **THEN** the server answers 403 with code subscription.forbidden
