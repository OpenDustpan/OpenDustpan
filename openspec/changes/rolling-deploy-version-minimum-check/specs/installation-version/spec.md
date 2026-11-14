## ADDED Requirements

### Requirement: stored-minimum-gates-writes

When the binary version of the serving instance is below the stored minimum binary version, the server SHALL refuse the write with 409 schema.binary-below-minimum.

#### Scenario: Old TFE write refused
- **WHEN** an instance below the stored minimum receives ANY /api/v2/state-versions and the NEW VersionStore.CheckWriteAllowed method runs in the write transaction
- **THEN** server returns 409 with error code schema.binary-below-minimum and writes no state version.

#### Scenario: Old Pulumi checkpoint refused
- **WHEN** an instance below the stored minimum receives a Pulumi checkpoint write and the NEW VersionStore.CheckWriteAllowed method runs
- **THEN** server returns the Pulumi error shape with code schema.binary-below-minimum and writes no checkpoint.

#### Scenario: Lock and secret writes refused
- **WHEN** an instance below the stored minimum receives a workspace lock or secret write and the NEW VersionStore.CheckWriteAllowed method runs
- **THEN** server returns 409 with error code schema.binary-below-minimum and writes nothing.

#### Scenario: Read allowed
- **WHEN** an instance below the stored minimum receives a state read
- **THEN** server returns 200 with the stored data.

### Requirement: atomic-minimum-raise

When two instances raise the stored minimum at the same time, the server SHALL keep the greatest version.

#### Scenario: Concurrent raise keeps greater
- **WHEN** two instances call the NEW VersionStore.RaiseMinimum method at the same time
- **THEN** stored minimum equals the greatest requested version and never moves down.

#### Scenario: Instance start records and raises
- **WHEN** a new-build instance starts and calls the NEW VersionStore.RecordInstance method then the NEW VersionStore.RaiseMinimum method
- **THEN** instance row exists with its binary version and minimum rises to the new release version with an audit entry.

### Requirement: version-edge-rules

When the stored minimum is unparseable or unreadable, the server SHALL refuse writes with 409 schema.binary-below-minimum.

#### Scenario: Lower raise is no-op
- **WHEN** a raise with the NEW VersionStore.RaiseMinimum method requests a version below the stored minimum
- **THEN** stored minimum is unchanged.

#### Scenario: Equal version allowed
- **WHEN** binary version equals the stored minimum and the NEW VersionStore.CheckWriteAllowed method runs
- **THEN** the write commits.

#### Scenario: Bad minimum fails closed
- **WHEN** the stored minimum is unparseable or unreadable and the NEW VersionStore.CheckWriteAllowed method runs
- **THEN** writes return 409 schema.binary-below-minimum and reads return 200.

#### Scenario: Dev-build never gates
- **WHEN** a dev-build calls the NEW VersionStore.RecordInstance method
- **THEN** the minimum is unchanged and writes are allowed.
