## ADDED Requirements

### Requirement: Out-of-phase rule

When a job token reaches state.write, the server SHALL refuse any write outside the lock or lease of its plan unless the phase passed change.apply and the unit belongs to the job.

#### Scenario: Plan-phase state body refused
- **WHEN** a job token of a phase that did not pass change.apply writes other than the lock or lease of its plan
- **THEN** the server refuses the write with identity.ErrDenied

#### Scenario: Plan-phase lock allowed
- **WHEN** a job token of a phase that did not pass change.apply writes only the lock or lease of its plan
- **THEN** the server allows the write when the gates allow

#### Scenario: Token with no phase refused
- **WHEN** a job token reaches state.write with no resolvable phase
- **THEN** the server refuses the write with identity.ErrDenied

#### Scenario: Apply-phase own unit
- **WHEN** a job token of a phase that passed change.apply writes a unit of its job
- **THEN** the server evaluates the state.write gates and allows the write when the gates allow

#### Scenario: Cross-unit write refused
- **WHEN** a job token writes a unit outside the units of its job
- **THEN** the server refuses the write with identity.ErrDenied

### Requirement: Evaluate with job claim

When a job token reaches state.write, the server SHALL evaluate the gates with the JobClaim of that token.

#### Scenario: Evaluation sees job claim
- **WHEN** a job token makes a state.write call
- **THEN** the gate evaluation uses the job, the phase, and the PassedApply value of that token

### Requirement: Unresolvable claim fails closed

When the Claimer cannot resolve the JobClaim of a job token, the server SHALL refuse the state.write call with identity.ErrDenied.

#### Scenario: Claim source fails
- **WHEN** the Claimer cannot resolve the job, the phase, or the units of a job token on a Pulumi or TFE-compatible write
- **THEN** the server refuses the write with identity.ErrDenied and the job stays for a later write

#### Scenario: Cancelled job fails closed
- **WHEN** the job of a job token is cancelled or deleted before the write
- **THEN** the server refuses the write with identity.ErrDenied and changes no job record

### Requirement: Claim lifetime is one request

When a job makes a state.write call, the server SHALL resolve its JobClaim fresh for that call.

#### Scenario: Phase passes between writes
- **WHEN** a job makes two state.write calls and the phase passes change.apply between them
- **THEN** the second write is evaluated with a freshly resolved claim

### Requirement: Plan-lock definition

When a plan-phase job token writes, the server SHALL treat only the lease of its own Pulumi update and the lock of its own workspace by the same job as the lock or lease of its plan.

#### Scenario: Pulumi lease is the plan lock
- **WHEN** a Pulumi plan-phase job token calls the lease route of its own update
- **THEN** the server treats the lease create and renew of that update as the lock of its plan

#### Scenario: TFE lock is the plan lock
- **WHEN** an OpenTofu plan-phase job token calls the TFE lock route of its own workspace
- **THEN** the server treats the lock of that workspace by the same job as the lock of its plan

#### Scenario: Unlock of another job refused
- **WHEN** a plan-phase job token calls unlock for a lock that another job holds
- **THEN** the server refuses the write with identity.ErrDenied

#### Scenario: Held-lease body write refused
- **WHEN** a plan-phase job token holds its lease and writes a state body
- **THEN** the server refuses the state body and keeps the held lease unchanged

### Requirement: Deny gates and manual locks first

When a job token writes only the lock or lease of its plan, the server SHALL still apply each deny gate and each manual lock to the write.

#### Scenario: Deny gate on cancel
- **WHEN** a deny gate at state.write refuses a Pulumi update cancel or a force-unlock
- **THEN** the server refuses the cancel even when the token writes only its own lock

#### Scenario: Manual lock holds
- **WHEN** a plan-phase job token writes its own lock while a manual lock covers the unit
- **THEN** the server refuses the write under the manual lock rule

### Requirement: Unlocked-write job token rule

When a job token makes an unlocked-write, the server SHALL apply the same job-token rule with operation kind unlocked-write.

#### Scenario: Plan-phase unlocked-write refused
- **WHEN** a job token of a phase that did not pass change.apply makes an unlocked-write
- **THEN** the server refuses the write with identity.ErrDenied

#### Scenario: Apply-phase unlocked-write
- **WHEN** a job token of a phase that passed change.apply makes an unlocked-write for a unit of its job
- **THEN** the server evaluates the state.write gates with the JobClaim of the token

### Requirement: Refusals are recorded

When GuardStateWrite refuses a job-token write, the server SHALL write the request log and the audit entry for the refusal.

#### Scenario: Refusal writes audit entry
- **WHEN** GuardStateWrite refuses a job-token write
- **THEN** the server writes the request log and the audit entry for the refusal
