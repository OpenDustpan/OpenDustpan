## ADDED Requirements

### Requirement: planner-state-read

When a principal holds the planner role on a unit, the server SHALL answer state reads of that unit with secrets masked.

#### Scenario: planner reads state
- **WHEN** a planner reads a stack or a workspace
- **THEN** the server answers the state with secrets masked

### Requirement: planner-write-refused

When a principal holds only the planner role on a unit, the server SHALL refuse update operations with 403.

#### Scenario: planner update refused
- **WHEN** a planner runs pulumi up or tofu apply
- **THEN** the server answers 403, changes nothing and writes an audit entry

### Requirement: planner-secret-refused

When a principal holds only the planner role, the server SHALL refuse a secret reveal with 403.

#### Scenario: planner reveal refused
- **WHEN** a planner asks for the plain text of a secret value
- **THEN** the server answers 403 and writes an audit entry
