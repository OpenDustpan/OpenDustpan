## ADDED Requirements

### Requirement: Default limit

The server SHALL end each new temporary administrator record and its token at the shorter of the session limit setting and the token policy maximum, with a default limit of 1 hour.

#### Scenario: Default expiry
- **WHEN** a temporary administrator is created with the default setting and a token policy maximum of 24 hours or more
- **THEN** the record and its token end 60 minutes after the create and later use is refused

#### Scenario: Token policy cap
- **WHEN** the token policy maximum is shorter than the limit
- **THEN** the record ends at the token policy maximum

#### Scenario: Create during patch
- **WHEN** a creation runs while a patch lowers the limit
- **THEN** the creation uses the value before or after the commit and stores that end time, and the audit entry names the limit used

### Requirement: New records only

When the session limit setting changes, the server SHALL apply the new limit to new records only.

#### Scenario: Change limit
- **WHEN** an administrator changes the limit
- **THEN** new records use it and an earlier record keeps its stored end time

#### Scenario: Lower token policy maximum
- **WHEN** the token policy maximum is lowered later
- **THEN** a running record keeps its stored end time

### Requirement: Range

If a patch sets the session limit outside 1 minute to 24 hours, then the server SHALL refuse it with a 422.

#### Scenario: Out of range
- **WHEN** a patch sets the limit to 25 hours
- **THEN** the server answers 422 that names the range and changes nothing

#### Scenario: Out of range with fresh step
- **WHEN** a patch sets the limit to 25 hours with a fresh step
- **THEN** the server answers 422 and changes nothing

#### Scenario: Longer limit needs a fresh step
- **WHEN** a patch raises the limit with no fresh step
- **THEN** the server answers 403 with code mfa.fresh-step-required

#### Scenario: Shorter limit needs no step
- **WHEN** a patch lowers the limit with no fresh step
- **THEN** the limit changes

### Requirement: Bad stored value

If the stored session limit is outside the range, then the server SHALL refuse to create the temporary administrator.

#### Scenario: Fail closed
- **WHEN** a creation reads a stored limit outside the range
- **THEN** the creation fails, an audit entry and a log line name the key, and no record exists

#### Scenario: Recovery by API
- **WHEN** an administrator patches the key to null
- **THEN** the effective value is 1 hour and the next creation works

### Requirement: Host reset

When an operator runs the host command with the reset option, the server SHALL delete the stored session limit and write an audit entry.

#### Scenario: Reset with no API
- **WHEN** an operator runs the host command with the reset option and no administrator is available
- **THEN** the stored value is deleted, the effective value is 1 hour and an audit entry names the key and the operator reason

### Requirement: Audit of the limit

When the server creates a temporary administrator, it SHALL write the limit used in the audit entry.

#### Scenario: Limit in entry
- **WHEN** a record is created
- **THEN** the audit entry names the limit and the end time
