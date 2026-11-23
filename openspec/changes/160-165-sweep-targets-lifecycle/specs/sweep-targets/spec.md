## ADDED Requirements

### Requirement: Create target

When a holder of namespace:manage-sweeps sends a valid target, the server SHALL store it with a stable trg- ID through CreateTarget and write an audit entry that holds the command.

#### Scenario: Create then list
- **WHEN** the holder creates a target with a sweeps pool of the namespace
- **THEN** the list holds the target with its ID, scopes, command and enabled flag, and the audit log holds one entry with the command

### Requirement: Refuse a pool without the sweep purpose

If the named pool lacks the sweeps purpose or belongs to another namespace, then the server SHALL refuse the write with the error code unmanaged.pool-not-sweeps.

#### Scenario: Pool of another namespace refused
- **WHEN** the holder names a sweeps pool of another namespace
- **THEN** the server answers 422 unmanaged.pool-not-sweeps and stores nothing

#### Scenario: Jobs pool refused
- **WHEN** the holder names a pool of the namespace with the jobs purpose
- **THEN** the server answers 422 unmanaged.pool-not-sweeps

### Requirement: Refuse bad command

If the command or a scope breaks its allowlist, then the server SHALL refuse the write with the error code unmanaged.command-invalid.

#### Scenario: Hostile commands
- **WHEN** the command has a NUL byte, 257 arguments, more than 131072 bytes or a relative program path, or a scope has an unknown provider
- **THEN** the server answers 422 unmanaged.command-invalid, repeats nothing of the input and stores nothing

### Requirement: Change and delete target

When a holder of namespace:manage-sweeps changes or deletes a target, the server SHALL apply it through UpdateTarget or DeleteTarget and write an audit entry.

#### Scenario: Delete target
- **WHEN** the holder deletes a target with no open sweep
- **THEN** the target is gone and the audit log holds the entry

### Requirement: Pool in use

If a target names a pool, then the server SHALL refuse the delete of that pool with runner.ErrPoolInUse.

#### Scenario: Pool delete refused
- **WHEN** an administrator deletes a pool that a target names
- **THEN** the server refuses and the pool stays

### Requirement: Start sweep

When a holder of namespace:start-sweep starts an enabled target with no open sweep, the server SHALL create a queued sweep and its durable operation through StartScan in one transaction.

#### Scenario: Start queues
- **WHEN** the holder starts an enabled target
- **THEN** the server answers a queued sweep with a swp- ID, created by StartScan

#### Scenario: Disabled target
- **WHEN** the holder starts a disabled target
- **THEN** the server answers 409 unmanaged.target-disabled

### Requirement: One open sweep

While a target has an open sweep, the server SHALL refuse a second start with the error code unmanaged.scan-busy.

#### Scenario: Two starts race
- **WHEN** two StartScan calls for one target run at the same time
- **THEN** one answers queued and the other answers 409 unmanaged.scan-busy

### Requirement: Cancel sweep

When a holder of namespace:start-sweep cancels a queued or dispatched sweep, the server SHALL move it to canceled through AdvanceScan and stop its durable operation in the same transaction.

#### Scenario: Cancel queued
- **WHEN** the holder cancels a queued sweep, which calls AdvanceScan with the cancel event
- **THEN** the sweep is canceled and no runner can claim it

#### Scenario: Cancel running
- **WHEN** the holder cancels a running sweep
- **THEN** the server answers 409 unmanaged.scan-not-cancelable and the status stays

### Requirement: List sweeps

When a reader lists the sweeps of a target, the server SHALL answer each sweep with its stable ID, status and times, newest first.

#### Scenario: List by target
- **WHEN** the reader lists the sweeps of a target
- **THEN** the answer is newest first

### Requirement: Refuse unknown or unauthorized

If a target or a sweep is unknown, or the caller lacks the action, then the server SHALL answer 404 or 403 and change nothing.

#### Scenario: Unknown and missing right
- **WHEN** the caller names an unknown target, or lacks namespace:start-sweep
- **THEN** the server answers 404 unmanaged.target-not-found or 403
