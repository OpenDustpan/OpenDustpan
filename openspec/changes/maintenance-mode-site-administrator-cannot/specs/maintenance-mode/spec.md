## ADDED Requirements

### Requirement: StartSession in maintenance mode

While maintenance mode is active, the server SHALL run StartSession to allow a site administrator to start a console session.

#### Scenario: Fresh sign in during maintenance
- **WHEN** a site administrator proves identity and starts a console session while maintenance mode is active
- **THEN** the server runs StartSession, creates the console session, and writes its audit entry.

#### Scenario: Non-administrator refused
- **WHEN** an authenticated caller who is not a site administrator starts a console session while maintenance mode is active
- **THEN** the server refuses the session start with maintenance.in-maintenance and no account detail.

#### Scenario: Failed sign-in keeps sign-in error
- **WHEN** sign-in proof fails while maintenance mode is active
- **THEN** the server returns the sign-in error and creates no session.

#### Scenario: Unauthenticated caller refused
- **WHEN** an unauthenticated caller starts a console session while maintenance mode is active
- **THEN** the server refuses the session start with maintenance.in-maintenance.

### Requirement: Session audit atomicity

If the session audit entry write fails in maintenance mode, then the server SHALL create no session.

#### Scenario: Audit write fails
- **WHEN** the session audit entry write fails in maintenance mode
- **THEN** the server creates no session and returns the write error.

### Requirement: Other writes stay refused

While maintenance mode is active, the server SHALL refuse each write that is not the site administrator session start or the authorized audit chain approval.

#### Scenario: Other write during exception
- **WHEN** a site administrator attempts a session policy write while maintenance mode is active
- **THEN** the server refuses the write with maintenance.in-maintenance.

#### Scenario: Refresh stays refused
- **WHEN** a site administrator refreshes a session while maintenance mode is active
- **THEN** the server refuses the refresh with maintenance.in-maintenance.

#### Scenario: Sign-out stays refused
- **WHEN** a site administrator signs out while maintenance mode is active
- **THEN** the server refuses the sign-out with maintenance.in-maintenance.

### Requirement: Approval with fresh session

When a site administrator presents a fresh session from maintenance mode sign-in, the server SHALL accept the audit chain approval.

#### Scenario: Approve chain with fresh session
- **WHEN** a site administrator presents the fresh session from maintenance mode sign-in at the audit chain route
- **THEN** the server accepts the audit chain change.

#### Scenario: Refused write names help
- **WHEN** the server refuses a write in maintenance mode
- **THEN** the refusal names the one command that helps.
