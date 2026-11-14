## ADDED Requirements

### Requirement: Fan out to subscriptions

When the dispatcher reads an outbox entry, the server SHALL write one delivery for each matching active subscription.

#### Scenario: Match
- **WHEN** an outbox entry matches two active subscriptions
- **THEN** the server writes two deliveries

#### Scenario: No match
- **WHEN** an outbox entry matches no subscription
- **THEN** the server writes no delivery

### Requirement: Isolate deliveries

If one sink fails, then the server SHALL send the other deliveries of the same entry.

#### Scenario: One sink fails
- **WHEN** one of two sinks fails
- **THEN** the other delivery reads sent

### Requirement: Mark sent

When a sink accepts a pending delivery, the server SHALL mark the delivery sent.

#### Scenario: Accepted
- **WHEN** the sink accepts a pending delivery
- **THEN** the delivery reads sent and a second mark changes nothing

### Requirement: Key each delivery

When the dispatcher retries a delivery, the server SHALL send the same idempotency key.

#### Scenario: Retry
- **WHEN** a crash happens between send and mark
- **THEN** the retry sends the same key

### Requirement: Dead-letter

If a delivery fails for 24 hours, then the server SHALL mark it dead-letter.

#### Scenario: Limit
- **WHEN** retries pass 24 hours
- **THEN** the delivery reads dead-letter and a redeliver sets it to pending
