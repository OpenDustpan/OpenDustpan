## ADDED Requirements

### Requirement: CreatePool and list pools

When the caller holds namespace:manage-runners, the server SHALL create a runner pool with CreatePool using purpose jobs or sweeps or both and a stable ID.

#### Scenario: Create jobs pool then list it
- **WHEN** the administrator calls CreatePool for purpose jobs in the namespace
- **THEN** the list holds the pool with purpose jobs and concurrency 1

### Requirement: CreatePool purpose immutable

If the caller changes the purpose set of a pool made by CreatePool, then the server SHALL refuse the change with runner.immutable.

#### Scenario: Change purpose after create
- **WHEN** the administrator calls CreatePool update that changes the purpose set of a pool
- **THEN** the server answers runner.immutable and keeps the first purpose set

### Requirement: CreatePool and CreateJoinToken limits

If the namespace holds 50 pools or the pool holds 100 open join tokens or concurrency is outside 1 to 64 or TTL is past 7 days, then the server SHALL refuse the CreatePool or CreateJoinToken write with runner.limit or runner.invalid.

#### Scenario: Create 51st pool
- **WHEN** the namespace already holds 50 pools and the caller calls CreatePool
- **THEN** the server answers runner.limit and stores no new pool

#### Scenario: Create 101st open token
- **WHEN** the pool already holds 100 open join tokens and the caller calls CreateJoinToken
- **THEN** the server answers runner.limit and stores no new token

### Requirement: DeletePool refused in use

If a job that is not terminal or a sweep target names the pool, then the server SHALL refuse DeletePool with runner.pool-in-use.

#### Scenario: Delete pool named by live job
- **WHEN** the administrator calls DeletePool for a pool that a live job names
- **THEN** the server answers runner.pool-in-use and keeps the pool

### Requirement: CreateJoinToken single use and RevokeJoinToken

When a runner joins with a CreateJoinToken secret, the server SHALL use the token once in one transaction and refuse reuse with runner.token-used.

#### Scenario: Join twice with one token
- **WHEN** two joins use the secret of one CreateJoinToken
- **THEN** the first join makes a runner and the second join fails with runner.token-used

#### Scenario: Join with revoked token
- **WHEN** a runner joins with the secret of one RevokeJoinToken token
- **THEN** the server refuses the join

#### Scenario: Join with expired token
- **WHEN** a runner joins after the 7-day TTL of the token
- **THEN** the server refuses the join

### Requirement: RetireRunner and drain

When the caller holds namespace:manage-runners, the server SHALL mark the runner retired with RetireRunner and expire its claims and end its sessions in the same transaction.

#### Scenario: Retire online runner
- **WHEN** the administrator calls RetireRunner for a runner
- **THEN** later requests with its key fail with runner.retired and its record stays

#### Scenario: Drained runner claims nothing new
- **WHEN** the administrator drains a runner
- **THEN** the runner keeps live claims but claims no new work

### Requirement: Isolation and namespace fence

When the caller sends an isolation kind outside the pool allowlist or a pool of another namespace, the server SHALL refuse the call.

#### Scenario: Create pool with process isolation not listed
- **WHEN** the administrator lists only firecracker isolation
- **THEN** the server answers runner.invalid

#### Scenario: Read pool of another namespace
- **WHEN** the caller reads a pool through the wrong namespace
- **THEN** the server answers runner.not-found

### Requirement: Audit pool management

When the server runs CreatePool or DeletePool or CreateJoinToken or RevokeJoinToken or RetireRunner, the server SHALL append the matching audit entry in the same transaction.

#### Scenario: Create pool writes audit
- **WHEN** the administrator calls CreatePool
- **THEN** the audit log holds pool-created with action namespace:manage-runners in the same transaction
