## ADDED Requirements

### Requirement: planner-state-read

When a principal holds the planner role on a unit, the server SHALL allow state read operations for that unit.

#### Scenario: planner reads stack and workspace state
- **WHEN** A principal with the planner role on the collection reads a stack with the Pulumi CLI or a workspace with OpenTofu.
- **THEN** The server answers the state and the masked plan with secrets masked.

#### Scenario: masked read covers old state versions
- **WHEN** A planner reads current or past state of the unit.
- **THEN** The server answers masked values for every state version including versions written before this change.

### Requirement: planner-backend-masking

When a principal with only the planner role reads a state file through the OpenTofu HTTP backend, the server SHALL mask each secret value in the answer.

#### Scenario: planner reads state file through HTTP backend
- **WHEN** A principal with only the planner role pulls state through the OpenTofu HTTP backend.
- **THEN** The server answers the state file with each secret value replaced by a masked placeholder.

### Requirement: planner-write-refused

When a principal holds only the planner role on a unit, the server SHALL refuse update operations with 403.

#### Scenario: planner update refused
- **WHEN** A principal with only the planner role runs pulumi up, pulumi destroy, tofu apply or tofu destroy.
- **THEN** The server answers 403 with error code unit.forbidden and changes nothing.

### Requirement: planner-secret-refused

When a principal holds only the planner role, the server SHALL refuse secret reveal with 403.

#### Scenario: planner secret reveal refused
- **WHEN** A principal with only the planner role asks for the plain text of a secret value.
- **THEN** The server answers 403 with error code secret.forbidden and writes an audit entry that names the principal, the planner role, the object and the time.

#### Scenario: planner state history export refused for secrets
- **WHEN** A principal with only the planner role calls a console or REST export of state history that holds secret values.
- **THEN** The server answers masked values or 403 with error code secret.forbidden for the plain-text part and writes an audit entry.

### Requirement: planner-other-routes

When a principal holds only the planner role, the server SHALL refuse non-plan job start with 403.

#### Scenario: planner non-plan job refused
- **WHEN** A principal with only the planner role calls a runner job route other than plan.
- **THEN** The server answers 403 and starts no job.

#### Scenario: planner token for another principal refused
- **WHEN** A principal with only the planner role asks to create a token for another principal.
- **THEN** The server answers 403 and creates no token.

### Requirement: planner-grant-union

When grants give different roles on different objects, the server SHALL evaluate each action on each object from the grants that cover that object.

#### Scenario: planner on one object and writer on another
- **WHEN** A principal holds the planner role on one collection and a write role on another collection of the same namespace.
- **THEN** The server allows writes on the writer object and refuses writes with 403 on the planner-only object.

### Requirement: planner-revocation

When a planner grant or group membership ends, the server SHALL refuse the next request that needs it with 403.

#### Scenario: grant removed during plan
- **WHEN** The planner grant or group membership of a principal ends during a plan or read.
- **THEN** The next read or plan request fails with 403.

### Requirement: planner-changeorder-author

When a principal holds the planner role, the server SHALL allow change-order author actions for that principal.

#### Scenario: planner authors a change order
- **WHEN** A principal with only the planner role authors a change order.
- **THEN** The server allows the author step and still refuses apply, destroy, approve and secret reveal.

### Requirement: planner-plan-gate-closed

When the protected-branch gate is not yet enforced, the server SHALL refuse server plan-job start by a planner with 403.

#### Scenario: planner plan-job start refused before milestone 5
- **WHEN** A principal with the planner role calls the server plan-job start before the protected-branch gate ships.
- **THEN** The server answers 403 and starts no job.

### Requirement: planner-plan-job-secrets

When a planner starts a plan job, the server SHALL deliver unit secrets to the job only where the commit is on a protected branch.

#### Scenario: planner plan job on unprotected branch
- **WHEN** A planner starts a plan job from a commit that is not on a protected branch.
- **THEN** The server starts the plan with masked values and delivers no unit secrets to the job.
