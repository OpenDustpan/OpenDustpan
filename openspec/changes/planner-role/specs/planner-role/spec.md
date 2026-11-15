## ADDED Requirements

### Requirement: planner-state-read

When a principal holds the planner role on a unit, the server SHALL answer state reads of that unit with secrets masked.

#### Scenario: planner reads state
- **WHEN** a planner reads a stack or a workspace
- **THEN** the server answers the state with secrets masked

### Requirement: planner-raw-state-refused

When a principal holds only the planner role on a unit, the server SHALL answer the raw state download with 403 and the code access.denied. ADR 212 decision 1: a planner "cannot apply, destroy, approve or read a secret value". The raw download needs unit:read-secrets (ADR 41).

#### Scenario: planner raw state refused
- **WHEN** a planner runs pulumi stack export or tofu state pull
- **THEN** the server answers 403 with access.denied

### Requirement: planner-write-refused

When a principal holds only the planner role on a unit, the server SHALL refuse update operations with 403 and the code access.denied.

#### Scenario: planner update refused
- **WHEN** a planner runs pulumi up or tofu apply
- **THEN** the server answers 403, changes nothing and writes an audit entry

### Requirement: planner-secret-refused

When a principal holds only the planner role, the server SHALL refuse a secret reveal with 403.

#### Scenario: planner reveal refused
- **WHEN** a planner asks for the plain text of a secret value
- **THEN** the server answers 403 with access.denied and writes an audit entry

### Requirement: planner-authorize-decision

When a planner presents a token, the Authorizer.Authorize decision SHALL allow the viewer actions, unit:run-plan and job:read-plans, and refuse every other action with access.denied.

#### Scenario: Authorize for planner
- **WHEN** Authorize runs for a planner with a read action and then a write action
- **THEN** the read is allowed and the write is refused with access.denied

#### Scenario: token for another principal
- **WHEN** a planner asks Authorize to create a token for another principal
- **THEN** the decision is a refusal with access.denied
