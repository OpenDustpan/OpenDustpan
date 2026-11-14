## ADDED Requirements

### Requirement: Default limit

The server SHALL end each new temporary administrator record and its token at the shorter of the session limit setting and the token policy maximum, with a default limit of 1 hour.

#### Scenario: Default expiry
- **WHEN** a temporary administrator is created with the default setting and a token policy maximum of 24 hours
- **THEN** the record and its token end 60 minutes after the create

#### Scenario: Token policy cap
- **WHEN** the session limit is 2 hours and the token policy maximum is 30 minutes
- **THEN** the record ends 30 minutes after the create

### Requirement: Create audit entry

When the server creates a temporary administrator, the server SHALL write an audit entry that names the limit in force.

#### Scenario: Limit in entry
- **WHEN** a temporary administrator is created with the default setting
- **THEN** the audit entry names the limit of 1 hour

### Requirement: New records only

When the session limit setting changes, the server SHALL apply the new limit to new records only.

#### Scenario: Running record
- **WHEN** the limit changes while a record runs
- **THEN** the running record keeps its stored end time

### Requirement: Range

If a patch sets the session limit outside 1 minute to 24 hours, then the server SHALL refuse it with a 422.

#### Scenario: Out of range
- **WHEN** a patch sets the limit to 25 hours
- **THEN** the server answers 422 that names the range and changes nothing

### Requirement: Host reset

When an operator runs the host command with the reset option and a reason, the server SHALL delete the stored session limit and write an audit entry with the reason.

#### Scenario: Reset with no API
- **WHEN** an operator runs the reset with a reason
- **THEN** the stored value is deleted, the effective value is 1 hour and an audit entry names the key and the reason

#### Scenario: Mode 3
- **WHEN** the installation chose recovery mode 3
- **THEN** the command refuses and names the mode
