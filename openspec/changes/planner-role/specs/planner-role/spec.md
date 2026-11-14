## ADDED Requirements

### Requirement: planner-state-read

When a principal holds the planner role on a unit, the server SHALL allow state read operations for that unit with secrets masked.

#### Scenario: planner reads state
- **WHEN** A planner reads a stack with the Pulumi CLI or a workspace with OpenTofu.
- **THEN** The server answers the state with secrets masked, including old versions.

#### Scenario: planner access check
- **WHEN** CheckAccess runs for a planner read.
- **THEN** The server allows the read.

#### Scenario: masked read
- **WHEN** ReadMasked runs for a planner on client-encrypted state.
- **THEN** The server answers metadata with state unavailable and no plain text.

### Requirement: planner-write-refused

When a principal holds only the planner role on a unit, the server SHALL refuse update operations with 403.

#### Scenario: planner update refused
- **WHEN** A planner runs pulumi up, pulumi destroy, tofu apply or tofu destroy.
- **THEN** The server answers 403 with unit.forbidden, changes nothing and writes an audit entry.

### Requirement: planner-secret-refused

When a principal holds only the planner role, the server SHALL refuse secret reveal with 403.

#### Scenario: planner reveal refused
- **WHEN** A planner asks for the plain text of a secret value.
- **THEN** The server answers 403 with secret.forbidden and writes an audit entry.

### Requirement: planner-other-routes

When a principal holds only the planner role, the server SHALL refuse non-plan job start and token creation for another principal with 403.

#### Scenario: planner job refused
- **WHEN** A planner calls a non-plan job route or creates a token for another principal.
- **THEN** The server answers 403 and starts or creates nothing.

### Requirement: planner-plan-gate-closed

While the protected-branch rule is not enforced, the server SHALL refuse planner plan-job start with 403.

#### Scenario: plan-job start refused
- **WHEN** A planner calls server plan-job start.
- **THEN** The server answers 403 and starts no job.

### Requirement: planner-revocation

When a planner grant ends, the server SHALL refuse the next request that needs it with 403.

#### Scenario: grant removed
- **WHEN** The grant of a planner is removed.
- **THEN** The next request answers 403.
