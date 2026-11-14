## ADDED Requirements

### Requirement: Save over-limit refused

If a principal saves a time to live above the namespace maximum, then the server SHALL refuse the save.

#### Scenario: Save above maximum
- **WHEN** a principal saves a time to live above the namespace maximum
- **THEN** the server refuses with the NEW error code unit-ttl.over-limit

### Requirement: Save stores checked value

When a principal with the NEW action unit:set-time-to-live calls SaveTimeToLive with a valid value, the server SHALL store the checked value.

#### Scenario: Valid save
- **WHEN** SaveTimeToLive gets a future at date within the maximum
- **THEN** the stored values equal the checked input

### Requirement: Computed expiry

When a unit holds an at date, an inactive-for duration, or both, the server SHALL answer the earlier computed expiry time with its source.

#### Scenario: Earlier wins
- **WHEN** a unit holds both an at date and an inactive-for duration
- **THEN** the server answers the earlier time with its source

### Requirement: Pool expiry waits

When an expired unit has a runner pool, the server SHALL open one destroy change order that waits for approval rules.

#### Scenario: Pool unit expires
- **WHEN** an expired unit has a runner pool
- **THEN** one destroy change order waits for approval rules and no delete starts

### Requirement: Git expiry deletes

When an expired unit has no runner pool and has a git source, the server SHALL open a write-back delete.

#### Scenario: Git unit expires
- **WHEN** an expired unit has no pool and has a git source
- **THEN** a write-back delete opens and no destroy change order opens

### Requirement: Notice-only expiry

When an expired unit has no runner pool and no git source, the server SHALL show an in-app expiry notice and write an audit entry.

#### Scenario: No path expires
- **WHEN** an expired unit has no pool and no git source
- **THEN** an in-app notice shows on the unit and an audit entry records it and no delete starts

### Requirement: Delete blocked with resources

While an expired unit holds managed resources, the server SHALL block its delete.

#### Scenario: Resources present
- **WHEN** an expired unit holds resources
- **THEN** the delete is blocked and the in-app notice repeats each 24 hours with an audit entry
