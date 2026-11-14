## ADDED Requirements

### Requirement: Hint intake route

The server SHALL serve POST /webhooks/{hook ID} outside /api/dustpan/v1 and answer 204 with an empty body for every well-formed request.

#### Scenario: Valid push delivery
- **WHEN** GitHub posts a push event with a valid signature to POST /webhooks/{hook ID}
- **THEN** the server records the hint, counts no reject and answers 204 with an empty body

#### Scenario: Bad signature
- **WHEN** GitHub posts with a bad X-Hub-Signature-256
- **THEN** the server counts git_host_event_rejected_total, writes the reject reason class without the secret and answers 204

#### Scenario: Unknown hook ID
- **WHEN** a caller posts to a hook ID that the installation map does not hold
- **THEN** the server counts a reject and answers 204 with an empty body

#### Scenario: Traversal in hook ID
- **WHEN** a caller posts with dot segments or slashes in the hook ID
- **THEN** the server counts a reject, opens no other path and answers 204

#### Scenario: Oversize body
- **WHEN** a caller posts a body above the size bound
- **THEN** the server stops the read at the bound, counts a reject and answers 204

#### Scenario: Oversize body with bad signature
- **WHEN** a caller posts a body above the size bound with a bad signature
- **THEN** the server stops the read at the bound, counts one reject and answers 204

### Requirement: Event hint parsing

When the intake receives a GitHub event, the server SHALL verify X-Hub-Signature-256 against the two stored event secrets and cause no second effect when the state of the effect shows the hint already applied.

#### Scenario: Replayed delivery
- **WHEN** the same delivery ID arrives twice and the state of the effect shows the hint already applied
- **THEN** the server causes no second effect

#### Scenario: Short secret refused
- **WHEN** an operator saves an event secret of fewer than 32 bytes
- **THEN** the server refuses the secret and keeps the prior secret active

#### Scenario: Effect state unavailable
- **WHEN** a verified delivery arrives while the effect state cannot be read
- **THEN** the server drops without effect, counts a reject and answers 204

#### Scenario: Slow parse
- **WHEN** a delivery body does not finish parsing within the parse time bound
- **THEN** the server stops the parse at the time bound, counts a reject and answers 204

### Requirement: Replica drop

Where federation is on, the server SHALL drop an event that a member cannot serve for its namespaces.

#### Scenario: Non-primary member
- **WHEN** a federation member receives an event for a namespace whose primary is another member
- **THEN** the member drops the event and the primary catches the change by poll

### Requirement: Poll fallback

The server SHALL catch a missed Git host change by poll and start the same hint as the webhook path.

#### Scenario: Single member without event
- **WHEN** a push lands while the webhook delivery is missing on an installation without federation
- **THEN** the next poll reads the change and starts the same hint

### Requirement: Stable hook hostname

The server SHALL keep a stable hook hostname that survives promotion of a namespace to another member.

#### Scenario: Promotion keeps URL
- **WHEN** a namespace fails over to another member
- **THEN** the stored webhook URL still routes to the new primary with no change at the Git host

#### Scenario: Reject redaction
- **WHEN** the intake rejects a delivery or mints a repo token
- **THEN** the log holds only the reason class and the token path holds no plain token or secret
