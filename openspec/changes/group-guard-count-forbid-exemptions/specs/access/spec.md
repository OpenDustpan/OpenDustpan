## ADDED Requirements

### Requirement: Guard widening group adds

When a group add widens a forbid limit, the server SHALL refuse the add unless the caller holds each limited action at the scope of the limit.

#### Scenario: Add to unless-named group without the actions
- **WHEN** a caller without the limited actions adds a principal to a group named in a forbid unless clause
- **THEN** the server refuses the add with 403 and writes an audit entry.

### Requirement: Guard widening group removes

When a group remove widens a forbid limit, the server SHALL refuse the remove unless the caller holds each limited action at the scope of the limit.

#### Scenario: Remove from when-named group without the actions
- **WHEN** a caller without the limited actions removes a principal from a group named in a forbid when clause
- **THEN** the server refuses the remove with 403 and writes an audit entry.

### Requirement: Conflicting limits

When a membership change widens any enabled forbid limit, the server SHALL refuse the change even when it narrows another limit.

#### Scenario: Widens one and narrows another
- **WHEN** a change widens one limit and narrows a second limit
- **THEN** the server refuses the change with 403.

#### Scenario: Disabled limit
- **WHEN** a limit that names the group is disabled or removed
- **THEN** the server ignores that limit in the check.

### Requirement: Holder path

When a caller who holds each limited action makes a widening change, the server SHALL apply the change.

#### Scenario: Holder makes the change
- **WHEN** a caller with the limited actions adds a principal to an unless-named group
- **THEN** the server applies the change and writes an audit entry.

### Requirement: Hidden limit refusal

When the caller cannot read the widened limit, the server SHALL refuse without the limit ID in the error.

#### Scenario: Caller cannot read the limit
- **WHEN** a widening change is refused to a caller who cannot read the limit
- **THEN** the server answers 403 without the limit ID.

### Requirement: Refusal record

When the server refuses a widening membership change, the server SHALL write one audit entry and no decision record.

#### Scenario: Refused change record
- **WHEN** the server refuses a widening change
- **THEN** the server writes one audit entry and no decision record.

### Requirement: Atomic check and write

When the server checks a widening change, the server SHALL read the limits and write the membership with no unchecked commit.

#### Scenario: Limit saved during the write
- **WHEN** a limit that names the group is saved during the membership write
- **THEN** the server refuses or retries the change with no unchecked commit.

### Requirement: Stable ID match

Where an access limit names a group, the server SHALL match the group by stable group ID.

#### Scenario: Rename and remake
- **WHEN** the group is renamed, or removed and made again, after the limit save
- **THEN** the guard applies to the group by its stable ID.

#### Scenario: Nested parent and cycle
- **WHEN** the named group is a parent in the chain, or the chain holds a cycle
- **THEN** the guard applies to the nested change and the traversal ends at the cycle.

### Requirement: Parser forms

Where a saved forbid policy tests a group in any when or unless form, the server SHALL treat the group as named by that limit.

#### Scenario: Negated, compound and set forms
- **WHEN** a limit names the group through negation, a compound expression, a set list or an is-in form
- **THEN** the server treats each form as a group reference.

### Requirement: Parse failure fails closed

If the saved limit set fails to parse, then the server SHALL refuse the membership change.

#### Scenario: Unparsable limit set
- **WHEN** the limit set fails to parse at write time
- **THEN** the server refuses the change with 403 and writes an audit entry.

### Requirement: Unnamed group stays as today

Where no access limit names the group, the server SHALL apply the current group grant guard.

#### Scenario: Unnamed group
- **WHEN** a caller changes a group that no limit names
- **THEN** the server applies the current guard only.

### Requirement: No extra fresh authentication

When the server checks a widening change, the server SHALL require no fresh authentication beyond what the membership route requires.

#### Scenario: No new step
- **WHEN** the server checks a widening change
- **THEN** the server decides with the authentication of the route alone.

### Requirement: Bulk batch atomic

When a bulk membership batch holds a widening entry without the limited actions, the server SHALL refuse the whole batch.

#### Scenario: One widening entry
- **WHEN** a batch holds one widening entry without the limited actions
- **THEN** the server refuses the batch with 403 and applies no entry.
