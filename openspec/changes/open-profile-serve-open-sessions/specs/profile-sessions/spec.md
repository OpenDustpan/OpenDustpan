## ADDED Requirements

### Requirement: Serve session read

When the same credential reads a live open session, the server SHALL answer the values of its open.

#### Scenario: Read session
- **WHEN** The same credential reads a live open session.
- **THEN** The server answers the values of its open.

#### Scenario: Other principal
- **WHEN** Another principal reads a session ID that is not its own.
- **THEN** The read answers 404 with no value.

#### Scenario: Job open read
- **WHEN** A job token opens with consumer run and reads its session through its own credential.
- **THEN** The server answers the values of its open.

#### Scenario: Revoked access
- **WHEN** A revoked caller reads its session.
- **THEN** The read answers 403 and the session is gone.

#### Scenario: Ended session
- **WHEN** A caller reads an ended session.
- **THEN** The read answers 404.

### Requirement: Bound session lifecycle

While sessions live, the server SHALL bound each principal or job to 100 live sessions with duration 1 minute to 12 hours and sweep ended sessions.

#### Scenario: Limits and sweep
- **WHEN** A principal or job passes 100 live sessions or a session ends.
- **THEN** The open fails or the sweep deletes ended sessions.

#### Scenario: Rotated root data key
- **WHEN** A caller reads a session sealed with a rotated or revoked root data key.
- **THEN** The read answers 404 with no value.

#### Scenario: Imported key rotation
- **WHEN** A data key of an imported profile rotates while the root data key does not.
- **THEN** The read still answers the values of its open.

#### Scenario: Backup holds no sessions
- **WHEN** A backup runs while sessions live.
- **THEN** A restore holds no open session.
