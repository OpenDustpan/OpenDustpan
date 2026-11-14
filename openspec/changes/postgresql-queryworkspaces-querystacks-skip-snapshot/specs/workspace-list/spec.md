## ADDED Requirements

### Requirement: Snapshot workspace page

When the server lists one workspace page, the server SHALL read the page in a snapshotOptions transaction.

#### Scenario: Workspace page takes swap lock
- **WHEN** the server lists one workspace page during a restore swap
- **THEN** the page read waits on the swap lock and belongs to one snapshot

### Requirement: Workspace conflict returns unavailable

When a workspace list snapshot read reports a serialization conflict, the server SHALL return namespace.ErrUnavailable.

#### Scenario: Workspace deadlock becomes unavailable
- **WHEN** the workspace snapshot read reports SQLSTATE 40P01 or 40001 at begin, statement, or commit
- **THEN** the server returns namespace.ErrUnavailable with no driver text
