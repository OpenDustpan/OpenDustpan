## ADDED Requirements

### Requirement: Hint intake route

The server SHALL answer 204 with an empty body for every request to POST /webhooks/{hook ID}, handled by HandleDelivery.

#### Scenario: Valid push
- **WHEN** GitHub posts a push event with a valid signature
- **THEN** HandleDelivery records the hint and the server answers 204

#### Scenario: Bad signature
- **WHEN** a caller posts a bad X-Hub-Signature-256
- **THEN** HandleDelivery counts a reject and the server answers 204

#### Scenario: Unknown hook ID
- **WHEN** a caller posts to an unknown hook ID
- **THEN** the server counts a reject and answers 204

#### Scenario: Oversize body
- **WHEN** a caller posts a body above the size bound
- **THEN** the server stops the read, counts a reject and answers 204

### Requirement: Old event secret window

During a rotation the server SHALL keep at most two event secrets and SHALL accept the old secret for the check of an event only until 7 days pass or the operator confirms the rotation.

#### Scenario: Old secret in the window
- **WHEN** an event is signed with the old secret inside the window
- **THEN** the server accepts the event and writes an audit entry

#### Scenario: Old secret after the window
- **WHEN** an event is signed with the old secret after the window ends
- **THEN** the server refuses the event and writes an audit entry of the refusal

#### Scenario: Old secret after confirm
- **WHEN** an event is signed with the old secret after the operator confirms the rotation
- **THEN** the server refuses the event and writes an audit entry of the refusal

#### Scenario: Third secret
- **WHEN** an event is signed with a third (older) secret
- **THEN** the server refuses the event and writes an audit entry of the refusal

#### Scenario: Window shown
- **WHEN** a caller reads the registration in the API
- **THEN** the answer shows the effective end of the window

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
