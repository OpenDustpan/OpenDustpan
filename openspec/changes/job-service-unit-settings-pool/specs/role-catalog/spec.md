## ADDED Requirements

### Requirement: Planner job actions

The server SHALL give the planner role the actions job:read, job:read-plans, job:read-logs and unit:run-plan.

#### Scenario: Planner starts a plan
- **WHEN** a principal with only the planner role starts a plan with no override
- **THEN** the server accepts the start

### Requirement: Maintainer management actions

The server SHALL give the maintainer role the actions of the writer role, unit:define-steps and namespace:manage-runners.

#### Scenario: Planner names a pool
- **WHEN** a principal with only the planner role names a pool in the request
- **THEN** the server answers 403 with the action namespace:manage-runners

#### Scenario: Maintainer names a location
- **WHEN** a principal with the maintainer role names a code location in the request
- **THEN** the server accepts the start

### Requirement: Viewer job read

The server SHALL give the viewer role the action job:read.

#### Scenario: Viewer reads a job
- **WHEN** a principal with only the viewer role reads a job
- **THEN** the server answers the job and no secret value
