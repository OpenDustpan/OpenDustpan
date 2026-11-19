## ADDED Requirements

### Requirement: Route the outbox

When the dispatcher reads a notification of a scope, the server SHALL write one delivery for each matching active subscription of that scope and then move the cursor of the scope, and SHALL NOT move the cursor past a notification that it could not route.

#### Scenario: Match
- **WHEN** a notification matches two active subscriptions
- **THEN** the server writes two pending deliveries

#### Scenario: No match
- **WHEN** a notification matches no active subscription
- **THEN** the server writes no delivery and moves the cursor

#### Scenario: Suspended subscription
- **WHEN** a notification matches only a suspended subscription
- **THEN** the server writes no delivery

#### Scenario: Port error
- **WHEN** a port fails while the server routes a notification
- **THEN** the cursor stays before that notification and the next round routes it

#### Scenario: Two instances
- **WHEN** two instances route the same scope at the same time
- **THEN** each notification gets one delivery for each subscription

#### Scenario: Another installation
- **WHEN** the scope answers ErrNotHome
- **THEN** the server writes no delivery and does not move the cursor

#### Scenario: Server scope
- **WHEN** a notification of the server scope matches a subscription of the server scope
- **THEN** the server writes a delivery with the zero fence

### Requirement: Send each delivery

When a delivery is due, the server SHALL claim it, check the access of its creator, check the address, send the notification to the sink with the notification ID as the idempotency key, and record the result for the live claim only.

#### Scenario: Delivered
- **WHEN** the sink accepts the message
- **THEN** the delivery reads `delivered` with one more attempt and an empty lastError

#### Scenario: Isolation
- **WHEN** one of two sinks fails
- **THEN** the server still sends the delivery of the other sink

#### Scenario: Same key after a retry
- **WHEN** a delivery is sent twice
- **THEN** both sends carry the same notification ID

#### Scenario: No sender
- **WHEN** the sink kind has no sender
- **THEN** the delivery reads `abandoned` with the class `permanent`

#### Scenario: Lost claim
- **WHEN** the lease of a claim lapsed and another holder claimed the delivery
- **THEN** the late record answers ErrConflict and the row keeps the result of the other holder

### Requirement: Retry and abandon

If a send fails, then the server SHALL retry a temporary failure with the wait of notify.RetryAt, SHALL abandon a permanent or refused failure at once, and SHALL abandon a delivery after the 15th failed attempt.

#### Scenario: Temporary failure
- **WHEN** the receiver answers with status 500
- **THEN** the delivery reads `pending` with attempts, nextAt and lastError `temporary`

#### Scenario: Permanent failure
- **WHEN** the receiver answers with status 400
- **THEN** the delivery reads `abandoned` with lastError `permanent`

#### Scenario: Last attempt
- **WHEN** the 15th attempt fails with a temporary error
- **THEN** the delivery reads `abandoned`

#### Scenario: Redeliver
- **WHEN** a person redelivers an abandoned delivery and the receiver accepts
- **THEN** the delivery reads `delivered` and the notification ID is the same as before

### Requirement: Refuse unsafe destinations

The server SHALL check the address of each send with the strict rules before the sender runs, and SHALL NOT store an address, a secret or the text of an error in a delivery.

#### Scenario: Refused address
- **WHEN** the address of a sink is in a refused range, or has a refused port, or is outside the strict host list
- **THEN** the delivery reads `abandoned` with lastError `refused` and the sender is not called

#### Scenario: Sender layer alone
- **WHEN** the address check allows an address and the address is in a refused range
- **THEN** the dial guard of the sender refuses the connection

#### Scenario: Error text
- **WHEN** the sender fails with an error that holds the URL and a token
- **THEN** the delivery, the log and the history answer hold neither

### Requirement: Check the creator before each send

Before each attempt, the server SHALL check that the creator of the subscription still holds the notification actions, and SHALL record the delivery as skipped and suspend the subscription when it does not.

#### Scenario: Creator lost access
- **WHEN** the creator lost one of the actions
- **THEN** the delivery reads `skipped`, the subscription reads `suspended` and the sender is not called

#### Scenario: Port cannot decide
- **WHEN** the access port answers with an error that is not a refusal
- **THEN** the delivery stays `pending` with the next attempt time

### Requirement: Suspend a failing subscription

When the deliveries of a subscription failed for 24 hours without a delivered one, the server SHALL append the notice subscription.suspended and then suspend the subscription with the class of the last error as the reason.

#### Scenario: Suspend
- **WHEN** the oldest failing delivery of a subscription is 24 hours old and no later delivery succeeded
- **THEN** the subscription reads `suspended` and the notice subscription.suspended is in the outbox

#### Scenario: A success resets
- **WHEN** a delivery succeeded after the failures
- **THEN** the subscription stays active

#### Scenario: Repeat after a stop
- **WHEN** the process stopped after the notice and before the suspend
- **THEN** the next round suspends the subscription and the outbox holds one notice

### Requirement: Stop cleanly

When the context of the dispatcher ends, the server SHALL wait for all its goroutines, SHALL record each send that finished, and SHALL record nothing for a send that did not finish.

#### Scenario: Cancel with a send in flight
- **WHEN** the context ends while a send is in flight
- **THEN** Run returns after its goroutines end and the delivery has no result for that attempt

### Requirement: Read a notification by ID

The server SHALL give the dispatcher the stored notification of a scope and an ID, and SHALL answer ErrNotFound for an absent one.

#### Scenario: Found
- **WHEN** a notification is stored in a scope
- **THEN** the read by scope and ID returns it

#### Scenario: Other scope
- **WHEN** the ID is stored in another scope
- **THEN** the read answers ErrNotFound
