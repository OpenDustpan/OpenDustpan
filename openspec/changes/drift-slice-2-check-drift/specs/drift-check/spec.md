## ADDED Requirements

### Requirement: pulumi-preview-steps

When the tool is pulumi and the purpose is drift-check, the server SHALL plan a refresh preview with the fixed Pulumi argv of the registered table.

#### Scenario: Out-of-band change shows drifted
- **WHEN** a developer changes a resource out of band and starts a drift check on a Pulumi stack
- **THEN** the unit shows drifted and the result holds the changed addresses

#### Scenario: Clean stack shows in-sync
- **WHEN** a developer starts a drift check on a stack with no out-of-band change
- **THEN** the unit shows in-sync

### Requirement: per-tool-drift-validation

When a drift check names a tool, the runner SHALL call ValidateDriftSteps to accept only the steps that RegisterDriftSteps stored for that tool.

#### Scenario: Registered steps pass
- **WHEN** the steps equal the registered Pulumi steps
- **THEN** the runner accepts the job

#### Scenario: Edited steps fail closed
- **WHEN** the steps differ from the registered Pulumi steps
- **THEN** the runner refuses the job with ErrInvalid

#### Scenario: Unknown tool refused
- **WHEN** a drift check names a tool with no registered table
- **THEN** the runner refuses the job with ErrInvalid

#### Scenario: Empty registry fails closed
- **WHEN** no drift table is registered
- **THEN** the runner refuses every drift check with ErrInvalid

### Requirement: pulumi-start-gate

If a pulumi job starts with a purpose other than drift-check, then the server SHALL refuse it with the error code job.unsupported.

#### Scenario: Drift check passes
- **WHEN** a developer starts a pulumi job with purpose drift-check
- **THEN** the server dispatches the job

#### Scenario: Other purpose refused
- **WHEN** a developer starts a pulumi job with another purpose
- **THEN** the server answers job.unsupported

### Requirement: second-check-conflict

When a Pulumi drift check starts while one check runs, the server SHALL answer drift.start-conflict.

#### Scenario: Second check conflicts
- **WHEN** a developer starts a second Pulumi drift check
- **THEN** the server answers drift.start-conflict and keeps the running check

### Requirement: preview-reduction

When a Pulumi drift preview finishes, the server SHALL store addresses and kinds with no attribute value and no secret value.

#### Scenario: Reduction to addresses
- **WHEN** a Pulumi refresh preview finishes with differences
- **THEN** the result holds addresses and kinds only

### Requirement: failed-check-keeps-result

If a Pulumi drift check fails or is canceled, then the server SHALL keep the latest drift result and time unchanged.

#### Scenario: CLI error keeps result
- **WHEN** a Pulumi preview exits with an error
- **THEN** the result and time stay unchanged and the job shows failed

#### Scenario: Canceled check keeps result
- **WHEN** a person cancels a running Pulumi drift check
- **THEN** the result and time stay unchanged

### Requirement: result-limit

When a Pulumi drift result holds more than 100 addresses, the server SHALL store 100 addresses and mark the result truncated.

#### Scenario: Long list truncates
- **WHEN** a preview reports more than 100 differences
- **THEN** the result holds 100 addresses and marks truncated

### Requirement: opentofu-regression

When the tool is opentofu, the server SHALL plan the same drift steps that pass ValidateDriftSteps on main.

#### Scenario: OpenTofu steps unchanged
- **WHEN** the server plans an OpenTofu drift check
- **THEN** ValidateDriftSteps accepts the steps
