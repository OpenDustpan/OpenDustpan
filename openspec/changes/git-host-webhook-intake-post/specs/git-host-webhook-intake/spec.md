## ADDED Requirements

### Requirement: Hint intake route

The server SHALL answer 204 with an empty body for every request to POST /webhooks/{hook ID}.

#### Scenario: Valid push
- **WHEN** GitHub posts a push event with a valid signature
- **THEN** the server records the hint and answers 204

#### Scenario: Bad signature
- **WHEN** a caller posts a bad X-Hub-Signature-256
- **THEN** the server counts a reject and answers 204

#### Scenario: Unknown hook ID
- **WHEN** a caller posts to an unknown hook ID
- **THEN** the server counts a reject and answers 204

#### Scenario: Oversize body
- **WHEN** a caller posts a body above the size bound
- **THEN** the server stops the read, counts a reject and answers 204

### Requirement: Replay

When the state of the effect shows a hint already applied, the server SHALL cause no second effect.

#### Scenario: Replayed delivery
- **WHEN** the same delivery arrives twice
- **THEN** the server applies the hint once

### Requirement: Replica drop

While a member does not serve the namespace of an event, the server SHALL drop the event.

#### Scenario: Non-primary member
- **WHEN** a member receives an event for a namespace of another primary
- **THEN** the member drops the event without a fetch
