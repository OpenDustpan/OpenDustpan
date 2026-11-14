## ADDED Requirements

### Requirement: latest keyed read

When a caller reads the latest output state for an approved stable ID, the server SHALL return only the checkpoint row that the keyed query finds for that stable ID.

#### Scenario: Latest success
- **WHEN** Latest reads and one checkpoint row exists for the approved stable ID
- **THEN** the server returns that checkpoint with status 200.

#### Scenario: Latest name resolves to another ID
- **WHEN** Latest reads and the producer name resolves to a stack with a different stable ID than the approved ID
- **THEN** the server answers 403 with the NEW error code stack.outputs_refused and no checkpoint content.

#### Scenario: Latest deleted stack
- **WHEN** Latest reads and the stack row for the approved stable ID is deleted
- **THEN** the server answers 404 with no checkpoint content.

#### Scenario: Latest empty stack
- **WHEN** Latest reads and the keyed query finds no checkpoint row for the approved stable ID
- **THEN** the server answers 404 with no checkpoint content.

### Requirement: outputs keyed read

When a caller reads root outputs for an approved stable ID, the server SHALL return only outputs from the checkpoint row that the keyed query finds for that stable ID.

#### Scenario: Outputs success
- **WHEN** Outputs reads and one checkpoint row exists for the approved stable ID
- **THEN** the server returns those root outputs with status 200.

#### Scenario: Outputs name resolves to another ID
- **WHEN** Outputs reads and the producer name resolves to a stack with a different stable ID than the approved ID
- **THEN** the server answers 403 with the NEW error code stack.outputs_refused and no outputs.

#### Scenario: Outputs deleted stack
- **WHEN** Outputs reads and the stack row for the approved stable ID is deleted
- **THEN** the server answers 404 with no outputs.

#### Scenario: Outputs empty stack
- **WHEN** Outputs reads and the keyed query finds no checkpoint row for the approved stable ID
- **THEN** the server answers 404 with no outputs.
