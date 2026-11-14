## ADDED Requirements

### Requirement: Run as stored principal

When a trigger starts a plan, the server SHALL authorize the stored run-as service principal of that trigger through stored grants only.

#### Scenario: Trigger start uses stored grant
- **WHEN** a trigger starts a plan and the run-as principal holds a stored unit:run-plan grant on the unit
- **THEN** the server starts the plan as the run-as principal and records it as the actor in the job and the audit entry.

### Requirement: Refuse without grant

When a trigger starts without a stored unit:run-plan grant for its run-as principal, the server SHALL refuse the start.

#### Scenario: No grant refuses with audit
- **WHEN** a trigger starts and its run-as principal holds no stored unit:run-plan grant
- **THEN** the server refuses with 403, writes an audit entry with the run-as principal and the unit, and creates no job.

### Requirement: Apply start needs both actions

When a trigger, schedule, or follows start requests an apply, the server SHALL require a stored unit:run-apply grant for the run-as principal in addition to unit:run-plan.

#### Scenario: Apply start without run-apply is refused
- **WHEN** a trigger start requests an apply and the run-as principal holds unit:run-plan but holds no stored unit:run-apply grant on the unit
- **THEN** the server refuses with 403, writes an audit entry with the run-as principal and the unit, and creates no job.

### Requirement: Refuse revoked or suspended principal

When the grant of the run-as principal was revoked or the principal is suspended or deleted, the server SHALL refuse each start of that trigger.

#### Scenario: Revoked grant stops schedule catch-up manual fire and follows starts
- **WHEN** the grant of the run-as principal was revoked or the principal is suspended or deleted
- **THEN** each schedule catch-up, manual fire, and follows start is refused with an audit entry naming the run-as principal and no job is created.

### Requirement: Refuse person run-as

When the run-as reference of a trigger names a person, the server SHALL refuse the save and each start.

#### Scenario: Person named as run-as is refused
- **WHEN** the run-as reference names a person principal
- **THEN** the server refuses the save and the start with 403 and writes an audit entry.

### Requirement: Refuse without act-as right

When the author of a trigger cannot act as the named service principal, the server SHALL refuse the save.

#### Scenario: Author without act-as is refused
- **WHEN** the author cannot act as the named service principal
- **THEN** the server refuses the save with 403 and writes an audit entry.

### Requirement: Fail closed without run-as

When a trigger has no stored run-as principal, the server SHALL refuse each start of that trigger.

#### Scenario: Trigger without run-as fails closed
- **WHEN** a trigger has no stored run-as principal
- **THEN** the server refuses the start, writes an audit entry, and creates no job.

### Requirement: Read outputs as run-as

When a job that a trigger started reads producer outputs, the server SHALL check unit:read-outputs for the run-as principal on the producer.

#### Scenario: Denied producer read
- **WHEN** the run-as principal holds no unit:read-outputs on the producer
- **THEN** the server refuses the read with 403 and writes an audit entry naming the run-as principal and the producer.
