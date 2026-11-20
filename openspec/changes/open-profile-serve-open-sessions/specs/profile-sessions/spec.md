## ADDED Requirements

### Requirement: Read a session

When the principal that opened a live session reads it, the server SHALL check profile:open again and answer the values of the open, or one path.

#### Scenario: Live session
- **WHEN** the same principal reads a live session
- **THEN** the server answers the values and writes no audit entry, because the open wrote one

#### Scenario: One path
- **WHEN** the same principal reads one path of a live session
- **THEN** the server answers that value only, and answers 404 for a path that the session does not hold

#### Scenario: Other principal
- **WHEN** another principal reads the session ID
- **THEN** the server answers 404 with no value and the session stays

#### Scenario: Ended session
- **WHEN** a principal reads a session after its end time
- **THEN** the server answers 404 with no value

#### Scenario: After revoke
- **WHEN** the principal lost profile:open and reads its session
- **THEN** the server answers 403 and deletes the session

#### Scenario: Store error
- **WHEN** the store fails during a read
- **THEN** the server answers 500 with no value and keeps the session

### Requirement: Sweep ended sessions

When a session has ended, the server SHALL delete it from the store.

#### Scenario: Sweep
- **WHEN** a session has ended and the sweep runs
- **THEN** the sweep deletes it and keeps each live session

### Requirement: Keep sessions out of backups

While sessions live, the backup of the installation SHALL hold no session, and a restore SHALL empty the table.

#### Scenario: Backup
- **WHEN** a backup runs while sessions live
- **THEN** the backup holds no session

#### Scenario: Restore
- **WHEN** a restore runs
- **THEN** the table of sessions is empty after it
