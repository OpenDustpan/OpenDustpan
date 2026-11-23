## ADDED Requirements

### Requirement: writeStateVersion atomic commit

When the server commits a workspace state version, the server SHALL commit the writeStateVersion rows and the current pointer in one fenced transaction.

#### Scenario: Failed writeStateVersion commit leaves no current state
- **WHEN** the database stops in the middle of a writeStateVersion commit
- **THEN** no new state version is current and the next plan shows the prior state

#### Scenario: Two writeStateVersion writers race
- **WHEN** two writeStateVersion requests commit at the same time with different fences
- **THEN** one writeStateVersion write wins and the other is rejected with a stale-fence error

### Requirement: lockWorkspace atomic acquire

When the server acquires a workspace lock, the server SHALL commit the lockWorkspace lock row in one fenced transaction.

#### Scenario: Failed lockWorkspace acquire leaves no held lock
- **WHEN** the database stops in the middle of a lockWorkspace acquire
- **THEN** the workspace has no held lock and the next apply acquires it

#### Scenario: Two lockWorkspace callers race
- **WHEN** two lockWorkspace requests arrive at the same time
- **THEN** one lockWorkspace acquire wins and the other is rejected

### Requirement: unlockWorkspace atomic release

When the server releases a workspace lock, the server SHALL commit the unlockWorkspace release in one fenced transaction.

#### Scenario: Failed unlockWorkspace release keeps a releasable lock
- **WHEN** the database stops in the middle of an unlockWorkspace release
- **THEN** the lock stays held and a retry with the same lock identity releases it

### Requirement: lockWorkspace committed lock survives

While a workspace lock from lockWorkspace is committed, the server SHALL keep the lock held with holder identity until release or force-unlock. The lock has no expiry.

#### Scenario: Committed lockWorkspace lock stays held after instance loss
- **WHEN** the holder instance is lost after the lockWorkspace commit
- **THEN** the lock stays held on every instance and a force-unlock releases it

### Requirement: writeStateVersion idempotent retry

When the server receives a repeated writeStateVersion request with the same lock identity after a lost reply, the server SHALL return the committed result without creating a duplicate state version. The retry is answered by the workspace service. A finalize of a version that is not pending stays refused by the store with `ErrUploadDenied`.

#### Scenario: writeStateVersion retry after lost reply makes no duplicate
- **WHEN** the client retries a writeStateVersion request with the same lock identity after a lost reply
- **THEN** the server returns the committed result without a second state version
