## ADDED Requirements

### Requirement: namespace-lock

When a manual lock covers a namespace, the server SHALL refuse each covered state-changing write with error code locked and never extend the cover to the installation scope.

#### Scenario: Lock namespace
- **WHEN** A holder with unit:lock on the namespace locks it with a reason.
- **THEN** Every unit of the namespace reports locked with the holder and the reason.

#### Scenario: Read while locked
- **WHEN** A reader reads a covered unit.
- **THEN** The unit keeps its status and reports the lock flag with holder and reason.

#### Scenario: Many units fan out
- **WHEN** A namespace lock covers many units.
- **THEN** Each covered unit reports the lock flag and a bulk answer holds one locked result per covered ID.

### Requirement: job-dispatch-waits

While a manual lock covers a unit, the server SHALL start no new job of the unit and dispatch no queued phase of the unit.

#### Scenario: Queued job waits
- **WHEN** A manual lock covers the unit of a queued job.
- **THEN** The job stays queued with reason locked and keeps its queue timeout.

#### Scenario: Running phase ends
- **WHEN** A manual lock arrives while a phase runs.
- **THEN** The phase runs to its end and no next phase of the unit dispatches.

#### Scenario: Concurrent claimers
- **WHEN** Two dispatchers claim the same queued phase in one transaction while a lock write commits.
- **THEN** Only one claim wins and the loser keeps the unit queued with reason locked.

### Requirement: running-job-tool-lock-passes

When a tool lock request carries the job token of a phase that started before the manual lock, the server SHALL allow the tool lock.

#### Scenario: Pre-lock phase locks tool
- **WHEN** A tool request carries the job token of a phase that started before the manual lock.
- **THEN** The tool lock succeeds and links to the phase.

### Requirement: job-token-race

When the lock time is equal to or earlier than the phase start time, the start time is missing, or a lock commits during the tool-lock request, the server SHALL refuse the tool lock.

#### Scenario: Lock between claim and tool lock
- **WHEN** The manual lock time is equal to or earlier than the recorded phase start time or the start time is missing.
- **THEN** The server refuses the tool lock with error code locked.

#### Scenario: Lock commits during request
- **WHEN** A manual lock commits after the tool-lock check reads and before the tool-lock write commits.
- **THEN** The server refuses the tool lock with error code locked.

### Requirement: bulk-lock

When a caller bulk-locks or bulk-unlocks from 1 to 100 IDs of one object kind, the server SHALL check each item on its own and answer one result per ID.

#### Scenario: Bulk lock mixed
- **WHEN** A holder bulk-locks up to 100 units where one unit is already locked.
- **THEN** The server answers 200 with one result per ID in request order.

### Requirement: bulk-edge-cases

When a bulk lock call names an unknown ID or a caller without unit:lock on the object asks unlock, the server SHALL answer one per-item result with not-found for the unknown ID and forbidden for the authorization refusal and change no other item.

#### Scenario: Too many IDs
- **WHEN** A caller sends more than 100 IDs.
- **THEN** The server answers 400 and changes nothing.

#### Scenario: Unknown ID and non-holder unlock
- **WHEN** A bulk call names an unknown ID and a caller without unit:lock on the object asks bulk-unlock.
- **THEN** The server answers not-found for the unknown ID and forbidden for the non-holder item and changes no other item.

#### Scenario: Locked conflict stays locked
- **WHEN** An authorized caller asks a state-changing write on a covered unit inside a bulk call.
- **THEN** The server answers error code locked for that item with holder and reason.

### Requirement: join-leave

When a unit joins a locked collection or a locked namespace, the server SHALL cover the unit from the moment of join.

#### Scenario: Unit joins locked scope
- **WHEN** A unit joins a locked collection or a locked namespace.
- **THEN** The unit reports locked from the moment of join.

#### Scenario: Unit leaves locked scope
- **WHEN** A unit leaves a locked collection while the namespace has no lock.
- **THEN** The unit reports unlocked unless another lock covers it.

### Requirement: import-hold-interaction

While the manual lock of an import covers a unit, the server SHALL accept a preview start on that unit unless a namespace lock also covers the unit.

#### Scenario: Preview under import hold
- **WHEN** A preview starts on a unit under the manual lock of an import with no namespace lock.
- **THEN** The server accepts the preview start and refuses a mutating update start.

#### Scenario: Namespace lock after import hold
- **WHEN** A namespace lock is added over a unit that already holds an import lock.
- **THEN** The server refuses the preview with error code locked.
