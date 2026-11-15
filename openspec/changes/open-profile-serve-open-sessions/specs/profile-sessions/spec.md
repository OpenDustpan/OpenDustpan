## ADDED Requirements

### Requirement: Read a session

When the same credential reads a live open session with Get, the server SHALL answer the values of its open.

#### Scenario: Live session
- **WHEN** the same credential calls Get on a live session
- **THEN** the server answers the values

#### Scenario: Other principal
- **WHEN** another principal calls Get with the session ID
- **THEN** the read answers 404 with no value

#### Scenario: After revoke
- **WHEN** a revoked caller calls Get on its session
- **THEN** the read answers 403 and the session is gone

### Requirement: Sweep ended sessions

When a session has ended, the server SHALL delete it from the store.

#### Scenario: Sweep
- **WHEN** a session has ended
- **THEN** the sweep deletes it

#### Scenario: Backup
- **WHEN** a backup runs while sessions live
- **THEN** the backup holds no session
