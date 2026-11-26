## ADDED Requirements

### Requirement: Out-of-phase rule

When a job token reaches state.write, the server SHALL refuse any write outside the lock or lease of its plan unless the phase passed change.apply and the unit belongs to the job.

#### Scenario: Plan-phase body refused
- **WHEN** a job token of a phase that did not pass change.apply writes a state body
- **THEN** the server refuses the write with identity.ErrDenied

#### Scenario: Plan-phase lock allowed
- **WHEN** such a token writes only the lock or lease of its plan
- **THEN** the server allows the write when the gates allow

#### Scenario: Cross-unit refused
- **WHEN** a job token writes a unit outside its job
- **THEN** the server refuses the write with identity.ErrDenied

### Requirement: Evaluate with job claim

When a job token reaches state.write, the server SHALL evaluate the gates with the JobClaim of that token.

#### Scenario: Evaluation sees claim
- **WHEN** a job token makes a state.write call
- **THEN** GuardStateWrite evaluates with the job, the phase and PassedApply of that token

### Requirement: Unresolvable claim

If the claim of a job token does not resolve, then the server SHALL refuse the write with identity.ErrDenied.

#### Scenario: Cancelled job
- **WHEN** the job of a token is cancelled before the write
- **THEN** the server refuses the write and changes no job record

### Requirement: Fresh claim

When a job makes a state.write call, the server SHALL resolve its JobClaim again for that call.

#### Scenario: Phase passes between writes
- **WHEN** the phase passes change.apply between two writes
- **THEN** the second write uses a newly resolved claim

### Requirement: Deny gates first

When the job model allows a write with a job token, the server SHALL apply the gates of state.write to the write and SHALL write a decision record for it.

#### Scenario: Deny gate on cancel
- **WHEN** a deny gate refuses a Pulumi update cancel
- **THEN** the server refuses it even for the own lock of the token

#### Scenario: Deny gate on an allowed apply write
- **WHEN** a deny gate matches the unit and a token of a phase that passed change.apply writes that unit of its job
- **THEN** the server refuses the write and writes a decision record that names the gate

#### Scenario: Job model refuses first
- **WHEN** a token of a plan phase writes a unit outside its job and no gate matches
- **THEN** the server refuses the write with identity.ErrDenied and runs no gate

### Requirement: Unlocked-write rule

When a job token makes an unlocked-write, the server SHALL apply the same job-token rule.

#### Scenario: Plan-phase unlocked-write
- **WHEN** a token of a phase that did not pass change.apply makes an unlocked-write
- **THEN** the server refuses the write with identity.ErrDenied
