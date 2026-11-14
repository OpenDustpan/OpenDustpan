## ADDED Requirements

### Requirement: Inventory fixed sets use open enums

Where a member is Unit.status, UnitState.state, or ManualLock.kind, the server SHALL describe it as an open enum of the known values.

#### Scenario: Manual lock kind
- **WHEN** a client reads the API description for a manual lock
- **THEN** the description shows the kind member as an open enum with the known kinds.

#### Scenario: Unit status and unit state
- **WHEN** a client reads the API description for an inventory unit
- **THEN** the description shows Unit.status and UnitState.state as open enums with the known states.

### Requirement: Job fixed sets use open enums

Where a member is Job.state, Job.executor, Job.operation, Job.wait, JobPhase.kind, or JobApproval.verdict, the server SHALL describe it as an open enum of the known values.

#### Scenario: Job members
- **WHEN** a client reads the API description for a job
- **THEN** the description shows Job.state, Job.executor, Job.operation, and Job.wait as open enums with the known values.

#### Scenario: Phase kind and approval verdict
- **WHEN** a client reads the API description for a job phase or approval
- **THEN** the description shows JobPhase.kind and JobApproval.verdict as open enums with the known values.

#### Scenario: Unknown job value
- **WHEN** a client sends a job member value outside the known set on a member that main accepts
- **THEN** the server accepts the value and the description still lists the known values.

### Requirement: Sink and preference sets use open enums

Where a member is NotificationSink.kind, NotificationSink.platform, preference theme, preference colorMode, preference hourCycle, or preference dateStyle, the server SHALL describe it as an open enum of the known values.

#### Scenario: Notification sink kind
- **WHEN** a client reads the API description for a notification sink
- **THEN** the description shows the kind and platform members as open enums with the known values.

#### Scenario: Preference members
- **WHEN** a client reads the API description for preferences
- **THEN** the description shows theme, colorMode, hourCycle, and dateStyle as open enums with the known values.

### Requirement: Runtime behavior stays unchanged

When stored data or a client holds a value outside the known set, the server SHALL apply the runtime behavior of main without a new error code.

#### Scenario: Stored unknown value
- **WHEN** stored data holds a value outside the known set
- **THEN** the server returns the stored value on a later read with no description error.

#### Scenario: Empty value
- **WHEN** a member holds an empty string
- **THEN** the server handles the value as main does with no new error code.

#### Scenario: Additive new value
- **WHEN** a later release adds one value to a converted set
- **THEN** old clients keep working and the description lists the added value as part of the open enum.

### Requirement: Free-text members stay strings

Where a member is outside the closed convert list, the server SHALL describe it as plain string.

#### Scenario: Stays-string member
- **WHEN** a client reads a member outside the closed convert list
- **THEN** the description shows the member as plain string.
