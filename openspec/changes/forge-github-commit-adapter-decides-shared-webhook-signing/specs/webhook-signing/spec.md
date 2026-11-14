## ADDED Requirements

### Requirement: Shared sign form

When the log-sink sender signs a batch, the server SHALL sign with the shared form over delivery ID, timestamp, and body and send header Dustpan-Notification-Id.

#### Scenario: Log-sink batch verifies
- **WHEN** the log-sink sender signs a batch with the shared Sign call
- **THEN** shared Verify accepts the batch with the sent delivery ID, timestamp, and body.

### Requirement: Old form refused

When a body signed with the old timestamp-body form arrives, the server SHALL refuse it.

#### Scenario: Old body refused
- **WHEN** a body signed with the old timestamp-body form arrives
- **THEN** shared Verify refuses the body.

### Requirement: Secret rotation

Where a webhook sink holds signing secrets, the server SHALL accept one or two active secrets and refuse an unknown secret.

#### Scenario: First secret accepted
- **WHEN** a webhook sink holds two active signing secrets
- **THEN** shared Verify accepts the delivery signed with the first active secret.

#### Scenario: Second secret accepted
- **WHEN** a webhook sink holds two active signing secrets
- **THEN** shared Verify accepts the delivery signed with the second active secret.

#### Scenario: Unknown secret refused
- **WHEN** a delivery arrives signed with a secret that no active sink secret matches
- **THEN** shared Verify refuses the delivery and repeats no secret text in the refusal.

### Requirement: Verify hostile inputs

When a delivery arrives with a tampered body, an expired timestamp, a missing delivery ID, or a malformed header, the server SHALL refuse it.

#### Scenario: Tampered body refused
- **WHEN** a delivery arrives with a valid timestamp but a changed body
- **THEN** shared Verify refuses the delivery.

#### Scenario: Expired timestamp refused
- **WHEN** a delivery arrives with a timestamp outside the tolerance window
- **THEN** shared Verify refuses the delivery.

#### Scenario: Missing delivery ID refused
- **WHEN** a delivery arrives with no delivery ID header
- **THEN** shared Verify refuses the delivery.

#### Scenario: Malformed header refused
- **WHEN** a delivery arrives with a malformed signature header
- **THEN** shared Verify refuses the delivery.

### Requirement: Secret limit

When an operator configures more than two active signing secrets on one webhook sink, the server SHALL refuse the sink config with the NEW error code `sink.too-many-secrets`.

#### Scenario: Three secrets refused at config
- **WHEN** an operator configures three active signing secrets on one webhook sink
- **THEN** the server refuses the sink config.

### Requirement: Delivery ID reuse

When a sender retries a batch, the server SHALL reuse the delivery ID of the first send.

#### Scenario: Retry reuses ID
- **WHEN** a sender retries a batch with the same delivery ID
- **THEN** the receiver drops the repeated delivery inside the tolerance window.
