## ADDED Requirements

### Requirement: plain-run-takes-whole-unit-lock

When a TFE-compatible lock request has no associated claim record, the server SHALL grant the whole-unit lock at the lock call before plan or apply.

#### Scenario: Plain apply locks at lock call
- **WHEN** developer runs plain tofu apply with no associated claim record and the workspace has no active claim or lock
- **THEN** server grants the whole-unit lock with reason local-without-wrapper and refuses any overlapping claim or second lock until release

#### Scenario: Second plain run refused
- **WHEN** a second plain run requests the lock on the same workspace from another principal or laptop
- **THEN** server refuses with the lock-held error naming holder identity and lock ID and changes nothing

#### Scenario: Claim run keeps claim path
- **WHEN** run presents an associated claim record from the claim model
- **THEN** server treats the request on the claim path and never as a plain lock

### Requirement: runner-job-waits-for-plain-holder

While a plain local run holds the whole-unit lock, the server SHALL hold a runner job that starts meanwhile in the FIFO whole-unit lock queue until the plain lock ends.

#### Scenario: Job starts during plain apply
- **WHEN** runner job starts while a plain run holds the whole-unit lock
- **THEN** server holds the runner job in the FIFO whole-unit lock queue behind the plain holder and starts its apply only after release, expiry, or force-unlock

#### Scenario: Two jobs wait in order
- **WHEN** two runner jobs wait behind the same plain holder
- **THEN** server starts the waiting jobs in FIFO order after the plain lock ends

#### Scenario: Restart with waiting job
- **WHEN** server restarts or fails over while a plain lock is held and a runner job waits in the queue
- **THEN** queue position and lock state survive restart and failover from SQL and the queued job starts after release

### Requirement: plain-lock-refused-during-claim

When a runner claim is active on a workspace, the server SHALL refuse a plain lock request on that workspace.

#### Scenario: Plain start during runner claim
- **WHEN** developer runs plain tofu apply while a runner claim is active
- **THEN** server refuses with the claim-conflict error naming the active claim and changes nothing

### Requirement: plain-lock-release

When a plain local run ends without unlock, the server SHALL release its whole-unit lock only by existing lock expiry or by operator force-unlock that requires unit:manage and writes a fence version and an audit entry.

#### Scenario: Plain run disconnects without unlock
- **WHEN** plain run crashes, loses the network, or never uploads state
- **THEN** lock ends only by existing lock expiry or by operator force-unlock with unit:manage, fence version, and audit entry, and a queued runner job starts

#### Scenario: Expired holder uploads late
- **WHEN** plain holder lock expired or was fenced before the state upload
- **THEN** server refuses the upload with the lineage error and the CLI reports the failed persist

### Requirement: client-encrypted-force-unlock-needs-switch

When a plain lock guards client-encrypted state, the server SHALL require the effective forced-write switch for force-unlock.

#### Scenario: Force-unlock client-encrypted plain lock
- **WHEN** operator force-unlocks a plain lock on client-encrypted state while the forced-write switch is off
- **THEN** server answers conflict, names the forced-write switch and the write-forced action, and changes nothing

### Requirement: history-names-plain-holder

When a plain whole-unit lock ends for any cause, the server SHALL record the holder principal, the lock interval, the end cause, and the reason on the workspace row and in history.

#### Scenario: Inspect history after plain apply
- **WHEN** developer reads the unit after a plain run unlocks
- **THEN** workspace read, history, and audit show holder principal, lock interval, end cause, and reason local-without-wrapper

#### Scenario: Inspect history after expiry or force-unlock
- **WHEN** plain lock ends by expiry or operator force-unlock
- **THEN** history and audit show holder principal, lock interval, end cause expiry or force-unlock, and reason local-without-wrapper

### Requirement: runner-wait-record-names-holder

While a runner job waits behind a plain holder, the server SHALL expose the plain holder identity in the wait record of the queued job.

#### Scenario: Read queued job status
- **WHEN** runner job waits in the queue behind a plain holder
- **THEN** wait record names the plain holder identity, the lock ID, and the wait cause

### Requirement: lock-held-error-names-wrapper

When the server refuses a lock because a plain lock is held, the server SHALL name the holder identity and the wrapper as the claim path in the refusal.

#### Scenario: Second locker reads refusal
- **WHEN** second plain run is refused while a plain lock is held
- **THEN** refusal names holder identity, lock ID, the retry with lock-timeout, and the wrapper as the claim path

#### Scenario: Wait with lock-timeout
- **WHEN** developer runs plain tofu apply with lock-timeout while a plain lock is held
- **THEN** client waits and retries the lock until release, expiry, or force-unlock

### Requirement: lock-authorization

When a caller requests a plain whole-unit lock, the server SHALL require unit run-plan or unit run-apply permission on the workspace object.

#### Scenario: No credential locks
- **WHEN** caller requests a plain lock with no credential
- **THEN** server answers unauthorized and creates no lock

#### Scenario: Caller without run permission locks
- **WHEN** caller without unit run-plan and without unit run-apply on the workspace requests a plain lock
- **THEN** server answers forbidden and creates no lock

#### Scenario: Plain state upload needs apply permission
- **WHEN** plain holder without unit run-apply uploads state
- **THEN** server refuses the upload and writes no version

### Requirement: upload-without-lock-refused

When a state upload arrives with no held lock and no active claim, the server SHALL refuse the upload.

#### Scenario: Plain upload with no lock
- **WHEN** plain run skips the lock call and uploads state with no held lock and no active claim
- **THEN** server refuses the state version with the lock error and writes no version
