## ADDED Requirements

### Requirement: durable-restore

The server SHALL run an installation restore as durable kind restore with fenced copy, swap, drop-old, and cleanupStage steps.

#### Scenario: copy kill resumes on taker
- **WHEN** holder is killed during copy.
- **THEN** taker reruns copy with the new fencing token and the restore ends succeeded.

#### Scenario: stale swap refused
- **WHEN** two workers hold old and new tokens at swap.
- **THEN** only the live token commits the swap and the stale token commits zero rows.

#### Scenario: failover bump fences writers
- **WHEN** swap commits the failover version bump.
- **THEN** in-flight writes with the old fence version are refused and the CLI retries the command.

### Requirement: staging-name

When an operator starts a restore, the server SHALL store the staging name in the restore row.

#### Scenario: start records staging name
- **WHEN** an operator starts a restore.
- **THEN** the restore row holds restore-staging-<restore-id> and start answers the restore ID.

### Requirement: non-failing-start

When a prior staging name of the same restore ID exists, the server SHALL start without failure and bump the failover version.

#### Scenario: start reuses same staging
- **WHEN** an operator starts a restore whose row already holds staging for that restore ID.
- **THEN** start answers running and the worker resumes from the stored staging name of that restore ID.

### Requirement: drop-old

When drop-old meets a lock timeout, the server SHALL retry with a 5 s lock wait.

#### Scenario: drop-old retries on lock timeout
- **WHEN** drop-old meets a live lock.
- **THEN** the worker waits 5 s, retries with backoff for 12 attempts, and then drops the old data.

#### Scenario: drop-old exhausts attempts
- **WHEN** drop-old still holds no lock after 12 attempts.
- **THEN** the restore ends failed with code restore.lock-wait and the last lock error on the row.

### Requirement: swap-idempotent

When swap commits, the server SHALL record the swapped flag and the old-data name in the restore row in the same transaction.

#### Scenario: kill mid-swap runs once
- **WHEN** the holder is killed after the swap committed.
- **THEN** the taker reads the swapped flag and moves to drop-old without a second swap.

### Requirement: cancel

When an operator cancels a running restore, the server SHALL end the restore as canceled.

#### Scenario: cancel stops copy
- **WHEN** an operator cancels a restore during copy.
- **THEN** the next step sees canceled and commits no further writes.

### Requirement: restore-running-refused

While one restore runs, the server SHALL refuse a second start with 409.

#### Scenario: second start refused
- **WHEN** an operator starts a restore while one restore runs.
- **THEN** the server answers 409 with code restore.running and the running restore ID.

### Requirement: worker-absent-503

When no worker claimed work for 2 lease periods, the server SHALL refuse a restore start with 503.

#### Scenario: no worker for 2 leases refuses start
- **WHEN** no worker claimed work for 2 lease periods at start.
- **THEN** the server answers 503 with code restore.worker-absent and Retry-After.

### Requirement: restore-auth

When the caller lacks the restore right, the server SHALL refuse start and cancel with 403.

#### Scenario: start without right refused
- **WHEN** an operator without the restore right starts a restore.
- **THEN** the server answers 403 with code restore.forbidden.

#### Scenario: cancel after revoke refused
- **WHEN** an operator without the restore right cancels a restore.
- **THEN** the server answers 403 with code restore.forbidden.
