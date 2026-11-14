## ADDED Requirements

### Requirement: Read a session

When the same credential reads a live open session, the server SHALL Get the values of its open.

#### Scenario: Live session
- **WHEN** the same credential reads a live session
- **THEN** the server answers the values

#### Scenario: Other principal
- **WHEN** another principal reads the session ID
- **THEN** the read answers 404 with no value

#### Scenario: After revoke
- **WHEN** a revoked caller reads its session
- **THEN** the read answers 403 and the session is gone

### Requirement: Sweep ended sessions

When a session has ended, the server SHALL Sweep it from the store.

#### Scenario: Sweep
- **WHEN** a session has ended
- **THEN** the sweep deletes it

#### Scenario: Backup
- **WHEN** a backup runs while sessions live
- **THEN** the backup holds no session
