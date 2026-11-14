## ADDED Requirements

### Requirement: Read namespace record

When a caller with the namespace gate read action reads a stored decision record of the namespace, the server SHALL return the stored record.

#### Scenario: Own record
- **WHEN** a caller with the read action reads an existing record of the namespace
- **THEN** the server returns the stored record

### Requirement: Denied read in scope

If a caller with namespace access but without the gate read action reads an existing record, then the server SHALL answer 403 with code gate.denied.

#### Scenario: No read action
- **WHEN** such a caller reads an existing record
- **THEN** the server answers 403 with code gate.denied and no record content

### Requirement: Missing or foreign record

If a record ID is missing, expired, malformed or outside the namespace, then the server SHALL answer 404 with code gate.not-found.

#### Scenario: Foreign ID
- **WHEN** the caller reads a record ID of another namespace
- **THEN** the server answers 404 with code gate.not-found and no record content

### Requirement: Read installation record

Where a decision record explains an access limit on an installation object, the server SHALL require installation:read-gates to read it.

#### Scenario: Installation allowed
- **WHEN** a caller with installation:read-gates reads the record on the installation route
- **THEN** the server returns the record

#### Scenario: Installation denied
- **WHEN** a caller without the action reads it
- **THEN** the server answers 403 with code gate.denied

### Requirement: Refusal names the record

If a gate refuses a state write, then the server SHALL name the decision record ID and the read route in the refusal.

#### Scenario: Denied state write
- **WHEN** a gate denies a state write
- **THEN** the error holds the record ID and the read route

### Requirement: Stored masked form

Where a stored fact document holds secret values, the server SHALL return each secret value in its stored masked form.

#### Scenario: Masked facts
- **WHEN** a record with secret facts is read
- **THEN** each secret value shows in its masked form
