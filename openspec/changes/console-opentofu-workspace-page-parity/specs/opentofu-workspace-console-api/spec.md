## ADDED Requirements

### Requirement: forceUnlock action

When a principal calls force-unlock on the TFE-compatible route or the http backend, the server SHALL require unit:write-forced on the workspace unit.

#### Scenario: Maintainer refused
- **WHEN** a principal with only the maintainer role calls force-unlock
- **THEN** the server answers 403 and names unit:write-forced.

#### Scenario: Custom role releases
- **WHEN** a principal with a custom role that holds unit:write-forced calls force-unlock
- **THEN** the server releases the lock.

### Requirement: forceUnlock reason

If a force-unlock request on the inventory route carries no reason of 1 to 500 characters after trim, then the server SHALL refuse it with 422.

#### Scenario: Missing reason
- **WHEN** the request carries no reason
- **THEN** the server answers 422 and changes nothing.

#### Scenario: Reason too long
- **WHEN** the reason has 501 characters
- **THEN** the server answers 422 and changes nothing.

### Requirement: Can-force-unlock flag

When a principal reads a workspace, the server SHALL report permissions.can-force-unlock true only when that principal holds unit:write-forced on the unit.

#### Scenario: Flag follows action
- **WHEN** a principal reads a workspace
- **THEN** the flag matches the action.
