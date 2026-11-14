## ADDED Requirements

### Requirement: session-renew-fence-namespace

When a session renew carries a fence that names another namespace, the server SHALL refuse the renew with runner.invalid.

#### Scenario: Fence of another namespace
- **WHEN** a session renew carries a fence of another namespace
- **THEN** the server refuses with runner.invalid and leaves the session unchanged

#### Scenario: Own namespace
- **WHEN** a session renew carries the fence of its own namespace
- **THEN** the server renews the session

### Requirement: runner-renew-fence-namespace

When a runner renewal carries a fence that names another namespace, the server SHALL refuse the renewal with runner.invalid.

#### Scenario: Fence of another namespace
- **WHEN** a runner renewal carries a fence of another namespace
- **THEN** the server refuses with runner.invalid and leaves the runner unchanged

### Requirement: claim-on-session-routes

When a runner calls a session route, the server SHALL authorize ActionRunnerClaim on the pool of that runner.

#### Scenario: Key of another pool
- **WHEN** a runner calls a session route with a key of another pool
- **THEN** the server refuses with runner.invalid

#### Scenario: Claim of another job
- **WHEN** a runner presents a claim for another job
- **THEN** the server refuses with runner.invalid

### Requirement: claim-on-runner-key-routes

When a caller uses a runner-key route, the server SHALL authorize ActionRunnerClaim on the pool of the key.

#### Scenario: Key of another pool
- **WHEN** a caller uses a runner key on another pool
- **THEN** the server refuses with runner.invalid
