## ADDED Requirements

### Requirement: Subscription routing

When a person creates a subscription with a type filter, the server SHALL enqueue deliveries only for notices that match the filter.

#### Scenario: Create subscription
- **WHEN** a person creates a subscription with a type filter on a webhook sink
- **THEN** only notices with matching type and object enqueue a delivery to that sink

#### Scenario: Sink deleted
- **WHEN** a subscription sink is deleted
- **THEN** no further delivery enqueues and pending rows stay pending until retention ends

### Requirement: Job start notice

When a job starts, the server SHALL append a start notice in the scope of the unit.

#### Scenario: Plan starts from API
- **WHEN** a job with operation plan starts from the API on a unit with a matching subscription
- **THEN** a start notice exists with the unit reference and one delivery is enqueued for each matching subscription

#### Scenario: Plan starts from runner
- **WHEN** a job with operation plan starts from a runner claim
- **THEN** the same single start notice exists with the same dedupe key shape

### Requirement: Approval wait notice

When a job enters awaiting-approval, the server SHALL append an approval-wait notice in the scope of the unit.

#### Scenario: Apply waits
- **WHEN** a job enters awaiting-approval
- **THEN** an approval-wait notice exists and each matching webhook sink gets a signed POST

### Requirement: Job end notice

When a job reaches a terminal state, the server SHALL append the terminal notice of that state in the scope of the unit.

#### Scenario: Apply ends
- **WHEN** a job reaches a terminal state
- **THEN** one terminal notice exists with the end state and links to the job and to the state version

#### Scenario: Approval rejected
- **WHEN** an approval wait ends by rejection
- **THEN** one terminal notice exists with the rejecting actor

#### Scenario: Approval timed out
- **WHEN** an approval wait ends by timeout
- **THEN** one terminal notice exists with the timeout

#### Scenario: Cancel before start
- **WHEN** a queued job is canceled before its first phase starts
- **THEN** one terminal notice exists with the actor and no duplicate start notice exists

### Requirement: Duplicate start guard

When the same scope and dedupe key is appended twice, the server SHALL keep one notice.

#### Scenario: Job restarts after crash
- **WHEN** a job transaction retries with the same scope and dedupe key
- **THEN** no second start notice with the same dedupe key exists

#### Scenario: Retried attempt new key
- **WHEN** a job retries with a new attempt number and a new dedupe key
- **THEN** a new start notice exists for the new attempt

### Requirement: Token expiry notice

When an access token enters its expiry warning window, the server SHALL append an expiry notice in the scope of the token owner.

#### Scenario: Token near expiry
- **WHEN** an access token passes 14 days and again 3 days before expiry
- **THEN** an expiry notice exists for the owner with the expiry time and no secret

#### Scenario: Token renewed in window
- **WHEN** a token in its warning window is rotated, renewed or revoked
- **THEN** no further expiry notice with the old expiry exists

#### Scenario: Short lifetime
- **WHEN** a token has a lifetime under 14 days or is made inside the window
- **THEN** only the warnings the token still meets exist

#### Scenario: Schedule restarts
- **WHEN** the expiry schedule restarts after a stop
- **THEN** no duplicate notice exists for a stored dedupe key

### Requirement: Signed delivery

When the dispatcher delivers to a webhook sink, the server SHALL sign the POST per the signature rule.

#### Scenario: Webhook POST
- **WHEN** the dispatcher delivers a notice to a webhook sink
- **THEN** the POST carries identity, timestamp and one signature per active secret, and the receiver verifies with the shared secret

#### Scenario: Clock skew
- **WHEN** a signed POST arrives outside the receiver clock window
- **THEN** the receiver refuses the POST and the attempt counts as a temporary failure

#### Scenario: Replay
- **WHEN** a signed POST arrives twice
- **THEN** a duplicate POST with the same notification ID and timestamp is ignored by idempotence

### Requirement: Secret rotation

When a webhook sink holds two active signing secrets, the server SHALL send both signatures on each delivery.

#### Scenario: Rotate signing secret
- **WHEN** a webhook sink holds two active signing secrets
- **THEN** each delivery carries both signatures until the old secret is deleted

#### Scenario: Only secret deleted
- **WHEN** the only signing secret of a sink is deleted
- **THEN** further deliveries fail closed with an audit entry and no unsigned send

### Requirement: Retry and suspend

When a delivery fails with a temporary error, the server SHALL retry with backoff for at most 15 attempts.

#### Scenario: Receiver fails for a day
- **WHEN** every delivery of a subscription fails for 24 hours
- **THEN** the dispatcher suspends the subscription with the error class as reason, keeps pending deliveries pending, and emits a suspend notice

#### Scenario: Receiver asks retry later
- **WHEN** a receiver answers with a retry delay
- **THEN** the sender waits for the asked period before the next attempt

#### Scenario: New notice while suspended
- **WHEN** a new job notice arrives while the subscription is suspended
- **THEN** the notice enqueues as pending and waits for redeliver

### Requirement: Suspended Claim redelivery order

When the dispatcher redelivers a suspended subscription, the server SHALL send pending deliveries in per-scope sequence order.

#### Scenario: Claim redeliver after fix
- **WHEN** a person redelivers a suspended subscription after the receiver is fixed
- **THEN** pending deliveries send in per-scope sequence order and the receiver restores order by sequence

#### Scenario: History gone
- **WHEN** a person redelivers after 30 days of delivery history
- **THEN** the redeliver answers that the history is gone with the retention bound

### Requirement: Creator reach

When the dispatcher prepares a delivery, the server SHALL check that the subscription creator still holds the manage and read actions on the scope.

#### Scenario: Grant removed
- **WHEN** the subscription creator no longer holds the manage and read actions on the scope
- **THEN** no POST leaves the server, the delivery stays pending with reason, and an audit entry records the skip

### Requirement: Sink URL guard

When a sink URL is not valid or the egress guard blocks it, the server SHALL refuse the sink or fail the attempt without sending.

#### Scenario: Bad URL at create
- **WHEN** a sink create gives a URL that is not http or https or that the egress guard blocks
- **THEN** the create is refused and nothing is stored

#### Scenario: Blocked URL at delivery
- **WHEN** a stored sink URL becomes blocked by the egress guard
- **THEN** the attempt fails, retries per backoff, and writes an audit entry

### Requirement: No secret in body

While a notice exists, the server SHALL keep secret values, credentials, state, plans and logs out of its body.

#### Scenario: Job with secrets
- **WHEN** a job that used a profile with secrets ends
- **THEN** the body holds names, IDs, summary, sequence and links, and holds no secret value, state, plan or log

#### Scenario: Oversize body after Fit
- **WHEN** a notice exceeds the body bound after Fit
- **THEN** the stored body stays within 256 KiB with cut text and dropped links and holds no secret

### Requirement: Order by sequence

When the server delivers notices of one scope, the server SHALL include the per-scope sequence in each body.

#### Scenario: Out of order arrival
- **WHEN** retries deliver a later notice before an earlier notice
- **THEN** the receiver restores order by the sequence field of the scope
