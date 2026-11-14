## ADDED Requirements

### Requirement: Single snapshot stack page

When the server lists one stack page, the server SHALL read the page rows and the tag rows in one snapshot read.

#### Scenario: Page and tags come from one snapshot
- **WHEN** the server lists one stack page while a restore swap stages between the page select and the tag select
- **THEN** the page rows and the tag rows belong to the same snapshot

#### Scenario: Mid-range error rolls back
- **WHEN** one range of stackRange fails in the middle of a multi-range read
- **THEN** the server returns no partial page and rolls back the snapshot

### Requirement: Conflict returns unavailable

When a stack list snapshot read reports a serialization conflict, the server SHALL return namespace.ErrUnavailable.

#### Scenario: Deadlock becomes unavailable
- **WHEN** the stack snapshot read reports SQLSTATE 40P01 or 40001 at begin, statement, or commit
- **THEN** the server returns namespace.ErrUnavailable with no driver text

#### Scenario: Cancelled context aborts
- **WHEN** the caller context cancels during a multi-range stackRange read
- **THEN** the server returns the context error and keeps no partial page

### Requirement: Retry reuses cursor

When the client retries a stack page after namespace.ErrUnavailable, the server SHALL answer from the same cursor in one snapshot read.

#### Scenario: Cursor stays valid after swap
- **WHEN** the client retries the same stack page after namespace.ErrUnavailable
- **THEN** the retried page starts from the same cursor and returns one snapshot
