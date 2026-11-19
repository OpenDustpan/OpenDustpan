## ADDED Requirements

### Requirement: Pass a gate in error once

When a write evaluation ends in error in enforce mode and a valid pass matches the holder, the unit stable ID and the operation kind, the server SHALL permit the write once within 15 minutes.

#### Scenario: Error pass
- **WHEN** a gate in enforce mode errors and a valid unused pass matches
- **THEN** the decision permits the write and marks the pass used

#### Scenario: Replay
- **WHEN** the same pass is used a second time
- **THEN** the decision refuses the write

#### Scenario: Expired
- **WHEN** the pass is older than 15 minutes
- **THEN** the decision refuses the write

#### Scenario: Other unit or kind
- **WHEN** the pass is used on another unit or operation kind
- **THEN** the decision refuses the write and consumes no pass

### Requirement: Refuse a bad reason

If the reason is empty, longer than 500 characters or holds a control character, then the server SHALL build no pass.

#### Scenario: Bad reason
- **WHEN** the reason is empty, too long or holds a control character
- **THEN** the constructor returns an error and no pass exists

### Requirement: Deny still refuses

If a write evaluation holds a deny, then the server SHALL refuse the write when a pass exists.

#### Scenario: Deny with a pass
- **WHEN** the evaluation has a deny and a valid pass exists
- **THEN** the decision refuses the write and consumes no pass

#### Scenario: Matched gate whose message does not render
- **WHEN** a deny gate or an approval gate matches, its message fails to render or its approval rule is missing, and a valid pass exists
- **THEN** the decision refuses the write and consumes no pass

#### Scenario: Store returns a wrong pass
- **WHEN** the store returns a pass of another key or an expired pass
- **THEN** the decision refuses the write with gate.unavailable

### Requirement: Fail closed without a store

If no pass store is wired, then the server SHALL refuse a write that sets break-glass with gate.unavailable.

#### Scenario: Default port
- **WHEN** a change sets break-glass and the default port is in use
- **THEN** the write is refused with gate.unavailable and no pass is used
