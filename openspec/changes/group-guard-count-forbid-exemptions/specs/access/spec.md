## ADDED Requirements

### Requirement: Guard widening group adds

When a group add widens a forbid limit, the server SHALL refuse the add unless the caller holds the rights for a widening change.

#### Scenario: Add to unless-named group without the rights
- **WHEN** a caller without the rights for a widening change adds a principal to a group named in a forbid unless clause
- **THEN** the server refuses the add with 403 and writes an audit entry that names the limit ID.

### Requirement: Guard widening group removes

When a group remove widens a forbid limit, the server SHALL refuse the remove unless the caller holds the rights for a widening change.

#### Scenario: Remove from when-named group without the rights
- **WHEN** a caller without the rights for a widening change removes a principal from a group named in a forbid when clause
- **THEN** the server refuses the remove with 403 and writes an audit entry that names the limit ID.

### Requirement: Conflicting limits

When a membership change widens any forbid limit, the server SHALL refuse the change unless the caller holds the rights for a widening change.

#### Scenario: Change widens one limit and narrows another
- **WHEN** a membership change widens one forbid limit while it narrows a second forbid limit
- **THEN** the server refuses the change with 403 and names the widened limit ID.

### Requirement: Admin path

When a caller with the rights for a widening change makes a widening add or remove, the server SHALL apply the change.

#### Scenario: Widening change by a holder of the rights
- **WHEN** a caller with the rights for a widening change makes a widening add or remove
- **THEN** the server applies the change and writes an audit entry that names the limit ID, the group, the principal, and the caller.

#### Scenario: Site administrator without the limited actions
- **WHEN** a site administrator without the rights for the widening change tries a widening add or remove
- **THEN** the server refuses the change with 403 and names the widened limit ID.

### Requirement: Break-glass exemption

When a break-glass session makes a widening add or remove, the server SHALL apply the change without the widening guard.

#### Scenario: Widening change in a break-glass session
- **WHEN** a break-glass session makes a widening add or remove
- **THEN** the server applies the change without the widening guard and the audit entry says the session is a break-glass session.

### Requirement: Atomic check and write

When the server checks a widening membership change, the server SHALL read the limit set and write membership in one fenced transaction.

#### Scenario: Limit saved during the membership write
- **WHEN** a limit that names the group is saved concurrent with the membership write
- **THEN** the check sees the new limit and the widening change is refused or retried with no unchecked commit.

### Requirement: Stable ID match

Where an access limit names a group, the server SHALL match the group by stable group ID at membership write time.

#### Scenario: Rename and remove and make again
- **WHEN** a named group is renamed or removed and made again between the limit save and the membership write
- **THEN** the server still applies the widening guard to the group.

#### Scenario: Nested parent group
- **WHEN** the named group is a parent of the changed group in the principal parent chain
- **THEN** the server applies the widening guard to the nested membership change.

### Requirement: Parse failure fails closed

When the saved limit set fails to parse at membership write time, the server SHALL refuse the membership change.

#### Scenario: Unparsable limit set at membership time
- **WHEN** the saved limit set for the group fails to parse at membership write time
- **THEN** the server refuses the membership change with 403 and writes an audit entry.

### Requirement: Unguarded group stays as today

Where no access limit names the group, the server SHALL apply the current group grant guard without a limit check.

#### Scenario: Change in unnamed group
- **WHEN** a caller changes membership of a group that no access limit names
- **THEN** the server applies the current group grant guard without a limit check.

### Requirement: No extra fresh authentication

When the server checks a widening membership change, the server SHALL require no fresh authentication beyond what the membership route already requires.

#### Scenario: Widening check without new step
- **WHEN** the server checks a widening membership change
- **THEN** the server decides with the authentication of the membership route alone.

### Requirement: Bulk and sync path

When a bulk or sync write widens a forbid limit, the server SHALL refuse each widening entry unless the caller holds the rights for a widening change.

#### Scenario: Bulk or sync write widens a limit
- **WHEN** a bulk membership change or a sync write adds or removes a principal in a group named by a forbid limit
- **THEN** the server refuses each widening entry with 403 under the same guard.
