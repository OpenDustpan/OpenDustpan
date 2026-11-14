## ADDED Requirements

### Requirement: list-history

When a caller with read grant requests history of one unit that Append stored, the server SHALL return its items ordered by time.

#### Scenario: list by unit
- **WHEN** a developer with read grant requests history of one unit
- **THEN** the server returns items that Append stored ordered by time with actor and redacted flag

#### Scenario: list unknown unit
- **WHEN** a developer requests history of a reference that never existed
- **THEN** the server returns 404 unit.not-found

### Requirement: history-roles

Where history actions exist, the server SHALL grant NEW namespace:read-history to auditor, viewer, writer, maintainer and admin.

#### Scenario: read grant
- **WHEN** a caller with NEW namespace:read-history requests history
- **THEN** the server returns 200

#### Scenario: redact denied
- **WHEN** a caller without NEW namespace:redact-history requests Redact
- **THEN** the server returns 403 history.denied with an audit entry

### Requirement: blame-resource

When a caller with read grant requests blame of one resource key, the server SHALL return the item that last changed that key.

#### Scenario: blame key
- **WHEN** a developer requests blame of a known key
- **THEN** the server returns the item with actor and time

#### Scenario: blame unknown key
- **WHEN** a developer requests blame of a key with no change record
- **THEN** the server returns 404 history.unknown-key

### Requirement: point-in-time

When a caller with read grant requests a unit with query at, the server SHALL return its state as of that time.

#### Scenario: read at time
- **WHEN** a developer requests a unit with at in Unix milliseconds
- **THEN** the server returns the state as of at

#### Scenario: bad at
- **WHEN** a developer sends at that is negative
- **THEN** the server returns 400 history.bad-at

#### Scenario: at before first item
- **WHEN** a developer requests at before the first item
- **THEN** the server returns 404 history.no-state

### Requirement: history-limits

When history holds more than 100 items for one page, the server SHALL return at most 100 items plus an opaque cursor.

#### Scenario: page limit
- **WHEN** a developer requests a history of 150 items
- **THEN** the server returns 100 items and a cursor

### Requirement: retention-visible

When a caller reads the settings of a namespace, the server SHALL return its effective job artifact retention in days.

#### Scenario: effective value
- **WHEN** a developer reads the namespace settings
- **THEN** the server returns effective retention 30 days for the namespace
