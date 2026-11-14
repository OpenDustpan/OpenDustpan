## ADDED Requirements

### Requirement: Refuse restored handoff token with missing runner

When a runner presents a handoff token whose stable runner ID has no runner record, the server SHALL refuse the request with `runner.not-found` before single-use consumption.

#### Scenario: Orphaned handoff token refused
- **WHEN** a runner presents a restored handoff token whose stable runner ID has no runner record
- **THEN** the server refuses the request with runner.not-found and no server error, without consuming the token

### Requirement: Refuse restored session with missing runner

When a launcher presents a runner session whose stable runner ID has no runner record, the server SHALL refuse the request with `runner.not-found`.

#### Scenario: Orphaned session refused
- **WHEN** a launcher presents a restored session whose stable runner ID has no runner record
- **THEN** the server refuses the request with runner.not-found and no server error

### Requirement: Existing runner still accepted

When a runner presents a handoff token whose stable runner ID has a runner record, the server SHALL accept the request.

#### Scenario: Valid credential works
- **WHEN** a runner presents a restored handoff token whose stable runner ID has a runner record
- **THEN** the server accepts the request
