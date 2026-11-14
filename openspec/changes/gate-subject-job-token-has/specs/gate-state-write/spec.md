## ADDED Requirements

### Requirement: GuardStateWrite job-token subject

When the caller uses a job token, the server SHALL fill Subject.Job with the job ID, the phase, the units of the job, and the stored PassedApply value, and fail closed when the job cannot be read.

#### Scenario: GuardStateWrite own plan lock allows
- **WHEN** a job token takes the lock of its own plan
- **THEN** the server allows the lock of its own plan

#### Scenario: GuardStateWrite other job lock refuses
- **WHEN** a job token takes the plan lock of another job
- **THEN** the server refuses with 403 and writes an audit entry

#### Scenario: GuardStateWrite unreadable job refuses
- **WHEN** a job token writes state and its job cannot be read
- **THEN** the server refuses with 403 and writes an audit entry

### Requirement: GuardStateWrite TFE unlocked declare

When a TFE state-version declare uses the unlocked override, the server SHALL evaluate state.write with operation kind unlocked-write before the transaction.

#### Scenario: Unlocked declare refused
- **WHEN** a TFE declare uses the unlocked override under a deny gate
- **THEN** the server refuses with 403 in JSON:API shape that names the one command and says resources may exist that no stored state records

#### Scenario: Unlocked declare allows
- **WHEN** a TFE declare uses the unlocked override with no deny gate
- **THEN** the server stores the state version

#### Scenario: Unlocked declare by person checks action
- **WHEN** a person declare uses the unlocked override
- **THEN** the server evaluates action unit:write-unlocked

#### Scenario: Unlocked declare by apply token after pass
- **WHEN** an apply job token uses the unlocked override
- **THEN** the server allows the declare when the phase passed change.apply and refuses it before the pass

### Requirement: jobTokenWrite PassedApply

When a job token writes state that is not its plan lock, the server SHALL allow the write only when the phase passed change.apply.

#### Scenario: jobTokenWrite before pass refuses
- **WHEN** an apply job token writes non-lock state before its phase passed change.apply
- **THEN** the server refuses the write and writes the denial to the audit log

#### Scenario: jobTokenWrite after fail refuses
- **WHEN** an apply job token writes non-lock state after its phase failed change.apply
- **THEN** the server refuses the write and writes the denial to the audit log

#### Scenario: jobTokenWrite after pass allows
- **WHEN** an apply job token writes non-lock state after its phase passed change.apply
- **THEN** the server allows the write to a unit of its own job

### Requirement: jobTokenWrite scope

When a job token requests force-unlock, restore, or delete, the server SHALL refuse the request.

#### Scenario: Force-unlock refuses
- **WHEN** a job token requests a force-unlock
- **THEN** the server refuses with 403 and writes an audit entry

#### Scenario: Delete refuses
- **WHEN** a job token requests a workspace delete or restore
- **THEN** the server refuses with 403 and writes an audit entry

#### Scenario: Same-job HTTP backend lock allows
- **WHEN** a job token sends LOCK for a unit of its own job as its plan lock
- **THEN** the server allows the lock

### Requirement: Phase PassedApply record

When a phase passes change.apply, the server SHALL record the NEW phase flag PassedApply for that phase.

#### Scenario: Pass records flag
- **WHEN** a phase passes change.apply
- **THEN** later job-token writes of its own job see PassedApply as true

#### Scenario: Fail records flag
- **WHEN** a phase fails change.apply
- **THEN** later non-lock writes stay refused
