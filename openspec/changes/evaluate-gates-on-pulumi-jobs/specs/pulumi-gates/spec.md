## ADDED Requirements

### Requirement: Propose on Pulumi job start

When a Pulumi job starts, the server SHALL evaluate change.propose before the plan phase and name the fact schema version in the decision record.

#### Scenario: Enforced deny stops the job
- **WHEN** a gate in enforce mode denies change.propose at the start of a Pulumi job
- **THEN** the job does not start the plan phase and the record holds result deny with the gate ID

#### Scenario: Evaluate mode records only
- **WHEN** a gate in evaluate mode matches change.propose at the start of a Pulumi job
- **THEN** the job starts and the record holds the evaluated outcome

#### Scenario: Engine error in enforce fails closed
- **WHEN** the engine reports a diagnostic error at change.propose on a Pulumi job
- **THEN** the job does not start and the record holds result error with the engine message

#### Scenario: Engine error in evaluate records only
- **WHEN** the engine reports a diagnostic error for a gate in evaluate mode at change.propose
- **THEN** the job starts and the record holds result error with the engine message

#### Scenario: Break-glass still meets gates
- **WHEN** a break-glass session starts a Pulumi job
- **THEN** the evaluation runs and the record holds the outcome

### Requirement: Planned after Pulumi preview

When a Pulumi preview stores its plan, the server SHALL evaluate change.planned with fact schema change.planned/1.0.

#### Scenario: Enforced deny stops the apply
- **WHEN** a gate in enforce mode denies change.planned after the Pulumi preview stores its plan
- **THEN** the job ends refused with no approval wait and the CLI shows the refusal text

#### Scenario: Engine error in enforce fails closed
- **WHEN** the engine reports a diagnostic error at change.planned on a Pulumi job
- **THEN** the job does not move to the update phase and the record holds result error

#### Scenario: Engine error in evaluate records only
- **WHEN** the engine reports a diagnostic error for a gate in evaluate mode at change.planned
- **THEN** the job moves on and the record holds result error

#### Scenario: Break-glass still meets gates
- **WHEN** a break-glass session runs a Pulumi preview that stores its plan
- **THEN** the evaluation runs and the record holds the outcome

### Requirement: Apply before Pulumi update

When a Pulumi job reaches the update phase, the server SHALL evaluate change.apply with the facts of that moment and name the fact schema version in the decision record.

#### Scenario: New deny at apply
- **WHEN** a gate denies change.apply with the facts of that moment after an allowed plan
- **THEN** the update phase does not dispatch and the record holds the new deny

#### Scenario: New requirement waits
- **WHEN** a gate adds a new approval requirement at change.apply with the facts of that moment
- **THEN** the change returns to approval wait and the record holds the new requirement

#### Scenario: Recheck holds at dispatch
- **WHEN** a restart or a concurrent update changes the facts between the apply evaluation and the dispatch
- **THEN** the dispatch stops and the record holds the recheck result

#### Scenario: Engine error in enforce fails closed
- **WHEN** the engine reports a diagnostic error at change.apply on a Pulumi job
- **THEN** the update phase does not dispatch and the record holds result error

#### Scenario: Engine error in evaluate records only
- **WHEN** the engine reports a diagnostic error for a gate in evaluate mode at change.apply
- **THEN** the dispatch goes on and the record holds result error

### Requirement: State write at update create

When a person creates a Pulumi update through the Pulumi Cloud-compatible API, the server SHALL evaluate state.write before the create with operation kind update.

#### Scenario: Enforced deny at create
- **WHEN** a gate in enforce mode denies state.write at the create of an update from the Pulumi CLI
- **THEN** the server answers 403 with code and message and creates no update

#### Scenario: Evaluate mode at create
- **WHEN** a gate in evaluate mode matches state.write at the create of an update from the Pulumi CLI
- **THEN** the update is created and the record holds the evaluated outcome

#### Scenario: Engine error in evaluate records only
- **WHEN** the engine reports a diagnostic error for a gate in evaluate mode at the Pulumi update create
- **THEN** the update is created and the record holds result error

#### Scenario: Runner update skips state write
- **WHEN** a runner writes the update of its own Pulumi job
- **THEN** the job path evaluates only the change points and creates the update without a second state.write

#### Scenario: Final gate and fixed control
- **WHEN** a final gate or a fixed control refuses at the same point as a gate
- **THEN** the answer names the gate and the record shows the fixed control state with the decision read

#### Scenario: Deny on cancel
- **WHEN** a deny gate at state.write matches a Pulumi update cancel
- **THEN** the server answers 403 with code and message and the record holds result deny with the gate ID

#### Scenario: Deny on force-unlock
- **WHEN** a deny gate at state.write matches a Pulumi force-unlock
- **THEN** the server answers 403 with code and message and the record holds result deny with the gate ID

#### Scenario: Client-encrypted body goes on
- **WHEN** a Pulumi update body is client-encrypted and the server cannot read it
- **THEN** state.available is false, the record says why, and the write goes on under the gates that apply

#### Scenario: Break-glass still meets gates
- **WHEN** a break-glass session creates a Pulumi update
- **THEN** the evaluation runs and the record holds the outcome
