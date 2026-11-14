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
- **WHEN** the run-as principal of the trigger lacks the grant to start a plan
- **THEN** the server refuses the start and writes a denial audit entry

### Requirement: Run-as principal of a trigger

Each trigger SHALL name its run-as principal by stable ID, and the start and the reads of its jobs SHALL use that principal.

#### Scenario: Audit names the actor and the configurer
- **WHEN** a trigger starts a plan
- **THEN** the audit entry names the run-as principal as the actor, the trigger, and the person who saved the trigger as the configurer

### Requirement: Run-as principal checks

The server SHALL refuse a run-as principal that is not a service principal, or that the author cannot act as. A run-as principal that the author creates in the same request SHALL have no grant, and the author SHALL give it only grants that the author could give.

#### Scenario: Person refused
- **WHEN** a trigger names a person as its run-as principal
- **THEN** the server answers 422 `trigger.run-as-not-service` and stores nothing

#### Scenario: No act-as edge
- **WHEN** the author names a service principal that the author cannot act as
- **THEN** the server answers 403 `trigger.run-as-denied` and stores nothing

#### Scenario: Created in the same request
- **WHEN** the author creates the run-as principal in the same request with a grant that the author could not give
- **THEN** the server refuses the request and creates neither the principal nor the trigger

#### Scenario: Suspended run-as principal
- **WHEN** the run-as principal of one trigger is suspended
- **THEN** that trigger starts nothing and writes a denial audit entry, and the other triggers of the unit start as before

#### Scenario: Reads use the run-as principal
- **WHEN** a job that the trigger started reads an output of another unit
- **THEN** the read uses the grants of the run-as principal, never the grants of the person who saved the trigger
