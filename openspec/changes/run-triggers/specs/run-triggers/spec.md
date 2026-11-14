## ADDED Requirements

### Requirement: Manage unit triggers

When a principal with the manage action on a unit saves a trigger, the server SHALL store one trigger of one kind on that unit.

#### Scenario: Create without grant
- **WHEN** a principal without the manage action on the unit creates a trigger
- **THEN** the server refuses with 403 and writes a denial audit entry

#### Scenario: Cross-namespace source refused
- **WHEN** a principal saves a follows trigger that names a unit of another namespace
- **THEN** the server refuses with 422 and stores no row

### Requirement: Refuse follows cycle at save

When a saved follows trigger closes a loop, the server SHALL refuse the save with the ordered unit path.

#### Scenario: Direct cycle refused
- **WHEN** a principal saves a follows trigger that closes a loop
- **THEN** the server refuses with 409 and names the units of the loop in order

### Requirement: Enforce trigger limits

When a trigger save exceeds the per-unit or per-source limit, the server SHALL refuse the save.

#### Scenario: Unit limit refused
- **WHEN** a principal saves a twenty-first trigger on one unit
- **THEN** the server refuses with 422 and names the per-unit limit

### Requirement: Plan-only start

When an enabled trigger fires, the server SHALL start a plan job through the job service.

#### Scenario: Manual fire keeps caller
- **WHEN** a principal with the plan-start grant fires an enabled trigger
- **THEN** the server starts a plan job and starts no apply job

#### Scenario: Missing automatic grant refuses
- **WHEN** the namespace service principal lacks the stored plan-start grant
- **THEN** the server refuses the start and writes a denial audit entry
