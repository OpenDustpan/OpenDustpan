## ADDED Requirements

### Requirement: namespace-lock

When a manual lock covers a namespace, the server SHALL refuse each state-changing write that a unit lock refuses on every unit of the namespace.

#### Scenario: Lock namespace
- **WHEN** a holder with unit:lock on the namespace locks it with a reason
- **THEN** every unit of the namespace reports the lock flag with holder and reason

### Requirement: job-dispatch-waits

While a manual lock covers a unit, the server SHALL dispatch no queued phase of the unit.

#### Scenario: Queued job waits
- **WHEN** a manual lock covers the unit of a queued job
- **THEN** the job stays queued with reason locked and keeps its queue timeout

#### Scenario: Running phase ends
- **WHEN** a lock arrives while a phase runs
- **THEN** the phase runs to its end

### Requirement: running-job-tool-lock

When a tool lock request carries the job token of a phase that started before the manual lock, the server SHALL allow the tool lock.

#### Scenario: Pre-lock phase
- **WHEN** the phase start time is before the lock time
- **THEN** the tool lock succeeds

#### Scenario: Equal time
- **WHEN** the lock time equals the phase start time
- **THEN** the server refuses with lock.locked

### Requirement: bulk-lock

When a caller bulk-locks or bulk-unlocks from 1 to 100 IDs, the server SHALL check each item on its own and answer one result per ID.

#### Scenario: Mixed result
- **WHEN** one of the IDs is already locked
- **THEN** the answer is 200 with one result per ID in request order

#### Scenario: Too many IDs
- **WHEN** a call holds 101 IDs
- **THEN** the server answers 400 and changes nothing

### Requirement: import-hold-preview

While the manual lock of an import covers a unit, the server SHALL accept a preview start on that unit.

#### Scenario: Preview under import hold
- **WHEN** a preview starts on a unit under an import hold
- **THEN** the server accepts the preview start
