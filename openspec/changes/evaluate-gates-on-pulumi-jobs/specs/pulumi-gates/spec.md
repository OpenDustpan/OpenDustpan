## ADDED Requirements

### Requirement: Propose on Pulumi job start

When a Pulumi job starts, the server SHALL evaluate change.propose before the plan phase and name the fact schema version in the decision record.

#### Scenario: Enforced deny stops the job
- **WHEN** a gate in enforce mode denies change.propose at the start of a Pulumi job
- **THEN** the job does not start the plan phase and the record holds result deny

#### Scenario: Evaluate mode records only
- **WHEN** a gate in evaluate mode matches change.propose
- **THEN** the job starts and the record holds the outcome

### Requirement: Planned after Pulumi preview

When a Pulumi preview stores its plan, the server SHALL evaluate change.planned with fact schema change.planned/1.0.

#### Scenario: Enforced deny stops without wait
- **WHEN** a gate in enforce mode denies change.planned
- **THEN** the job ends refused with no approval wait

#### Scenario: New requirement waits
- **WHEN** a gate adds an approval requirement at change.planned
- **THEN** the change returns to approval wait

### Requirement: Apply before Pulumi update

When a Pulumi job reaches the update phase, the server SHALL evaluate change.apply with the facts of that moment.

#### Scenario: New deny at apply
- **WHEN** a gate denies change.apply after an allowed plan
- **THEN** the update phase does not dispatch

### Requirement: State write at update create

When a person creates a Pulumi update through the Pulumi Cloud-compatible API, the server SHALL evaluate state.write before the create with operation kind update.

#### Scenario: Enforced deny at create
- **WHEN** a gate in enforce mode denies state.write at the update create with an access-token credential
- **THEN** the server answers 403 with code and message and creates no update

#### Scenario: Person access-token evaluates state.write
- **WHEN** a person creates an update with an access-token credential
- **THEN** the server evaluates state.write once and the record holds operation kind update

#### Scenario: Runner job-token skips state.write
- **WHEN** a runner writes the update of its own job with a job-token credential
- **THEN** the server evaluates no state.write and the job path evaluates the three change points only

#### Scenario: Non-update kind skips this path
- **WHEN** the create carries a non-update operation kind such as a stack import
- **THEN** the server evaluates no update-kind state.write and writes no update-kind record
