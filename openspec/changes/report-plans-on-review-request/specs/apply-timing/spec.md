## ADDED Requirements

### Requirement: Override forbidden

If a wider level forbids overrides, then the server SHALL refuse a narrower value that differs from it with status 409 that names that level.

#### Scenario: Namespace forbids
- **WHEN** a unit sets pre-merge while the namespace forbids overrides
- **THEN** the write is refused with 409 and names the namespace setting

#### Scenario: Collection forbids
- **WHEN** the namespace allows overrides, the collection forbids them and a unit sets a different value
- **THEN** the write is refused with 409 and names the collection setting

### Requirement: Later forbid

If a wider level starts to forbid overrides, then the server SHALL use the wider value as the effective timing.

#### Scenario: Forbid after unit value
- **WHEN** a unit holds pre-merge and the namespace then forbids overrides with post-merge
- **THEN** the effective value is post-merge, names the namespace level and the unit value stays stored

### Requirement: Unit value wins

Where every wider level allows overrides, the server SHALL use the unit value as the effective timing.

#### Scenario: Unit value wins
- **WHEN** the namespace and the collection allow overrides and the unit sets pre-merge
- **THEN** the effective value is pre-merge and names the unit level

### Requirement: Inherit

If a unit has no value, then the server SHALL use the value of the narrowest wider level that sets one.

#### Scenario: Unit unset
- **WHEN** the unit has no value and the collection sets pre-merge
- **THEN** the effective value is pre-merge and names the collection level

### Requirement: Value allowlist

If a timing value is not post-merge or pre-merge, then the server SHALL refuse the write with status 400.

#### Scenario: Unknown value
- **WHEN** a write sets the value later
- **THEN** the write is refused with 400 and nothing changes

### Requirement: Timing audit

When a person writes an apply timing value, the server SHALL write an audit entry with the level, the old value, the new value and the principal.

#### Scenario: Write audited
- **WHEN** a namespace administrator sets the namespace value
- **THEN** one audit entry holds the level, both values and the principal
