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

When the dispatcher claims a pending outbox row, the server SHALL hold the row under a 60-second lease from the server clock.

#### Scenario: Single claim wins
- **WHEN** two dispatchers claim the same pending outbox row
- **THEN** only one dispatcher holds the lease

#### Scenario: Expired lease reclaimed
- **WHEN** the lease expires before the holder marks the row dispatched
- **THEN** another dispatcher claims the row with the stored match set

#### Scenario: Stale mark refused
- **WHEN** a slow holder marks after its lease expired
- **THEN** the server refuses the mark with a lease-mismatch error

### Requirement: Snapshot match set at claim

When the dispatcher claims a pending outbox row, the server SHALL store the matching active subscription set in the same claim transaction.

#### Scenario: Late subscription ignored
- **WHEN** a principal creates a subscription after the claim transaction
- **THEN** the dispatcher writes no delivery row for that subscription in this dispatch

#### Scenario: Reclaim reuses stored set
- **WHEN** the dispatcher reclaims an outbox row after a crash
- **THEN** the reclaim writes rows only for the stored set

### Requirement: Fan out to subscriptions in isolation

When the dispatcher claims a pending outbox row, the server SHALL fan out one delivery per matching active subscription without letting one failed delivery block another delivery.

#### Scenario: Match and deliver
- **WHEN** the dispatcher claims a pending outbox row
- **THEN** the server writes one delivery row per matching active subscription and sends the payload to each sink

#### Scenario: One sink fails
- **WHEN** one sink of the same notification fails
- **THEN** the deliveries to the working sinks still read sent

#### Scenario: No match
- **WHEN** a notification matches no active subscription
- **THEN** the server writes no delivery row and marks the outbox row dispatched

### Requirement: Keep fan-out idempotent on reclaim

When a dispatcher reclaims an outbox row, the server SHALL write no second delivery row for the same outbox row and subscription.

#### Scenario: Crash after partial fan-out
- **WHEN** the dispatcher crashes after it writes some delivery rows
- **THEN** the reclaim writes only the missing rows and marks the outbox row dispatched

### Requirement: Key each delivery

When the dispatcher writes a delivery row, the server SHALL assign a stable idempotency key derived from the outbox row ID and the subscription ID.

#### Scenario: Retry repeats key
- **WHEN** the dispatcher retries a delivery after a crash between send and mark
- **THEN** the sink sees the same idempotency key on each attempt

### Requirement: Retry then dead-letter then manual retry

When a sink send fails, the server SHALL retry with exponential backoff to retry limit 5 within 24 hours and then mark the delivery dead-letter.

#### Scenario: MarkSent
- **WHEN** a sink send succeeds
- **THEN** the delivery row reads sent with the sent time

#### Scenario: MarkDeadLetter
- **WHEN** a sink send fails at retry limit 5 within 24 hours
- **THEN** the delivery row reads dead-letter and automatic retry stops

#### Scenario: Manual retry
- **WHEN** a principal retries a dead-letter delivery
- **THEN** the delivery row returns to pending

#### Scenario: Retry refused
- **WHEN** a principal retries a delivery that is not dead-letter
- **THEN** the server answers 409 with code delivery.not-dead-letter

### Requirement: Retain and purge records

When 30 days pass after dispatch with no dead-letter delivery, the server SHALL purge the dispatched outbox row and its sent terminal delivery rows.

#### Scenario: Purge after retention
- **WHEN** 30 days pass after dispatch and no delivery reads dead-letter or failed
- **THEN** the server removes the outbox row and its delivery rows

#### Scenario: Dead-letter kept
- **WHEN** 30 days pass while a delivery reads dead-letter
- **THEN** the server keeps the outbox row and the dead-letter row

### Requirement: Guard read routes

When a principal reads subscriptions, outbox rows, or delivery rows, the server SHALL check the read grant on the namespace.

#### Scenario: Missing row
- **WHEN** a principal reads an absent subscription or delivery row
- **THEN** the server answers 404 with code subscription.not-found or delivery.not-found

#### Scenario: No grant
- **WHEN** a principal without the read grant reads deliveries
- **THEN** the server answers 403 with code subscription.forbidden

### Requirement: Guard write routes

When a principal creates a subscription or retries a dead-letter delivery, the server SHALL check the manage grant on the namespace.

#### Scenario: Create without grant
- **WHEN** a principal without the manage grant creates a subscription
- **THEN** the server answers 403 with code subscription.forbidden

#### Scenario: Manual retry without grant
- **WHEN** a principal without the manage grant retries a dead-letter delivery
- **THEN** the server answers 403 with code subscription.forbidden
