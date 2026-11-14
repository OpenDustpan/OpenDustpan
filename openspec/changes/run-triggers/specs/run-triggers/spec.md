## ADDED Requirements

### Requirement: Manage unit triggers

When a principal with the manage action on a unit saves a trigger, the server SHALL store one trigger of one kind on that unit.

#### Scenario: Create list read replace delete
- **WHEN** a principal with the manage action creates a trigger on its unit
- **THEN** the server stores the trigger and lists it with its kind and fields

### Requirement: Plan-only start

When a saved trigger fires, the server SHALL start a plan job through the job service.

#### Scenario: Trigger fire starts plan
- **WHEN** a saved trigger fires through the job service
- **THEN** the server starts a plan job and never starts an apply job

#### Scenario: Missing grant refuses start
- **WHEN** the namespace service principal lacks the stored plan-start grant
- **THEN** the server refuses the start and writes a denial audit entry
