## ADDED Requirements

### Requirement: Read namespace record

When a caller with the namespace gate read action reads a stored decision record of the namespace, the server SHALL return the stored record.

#### Scenario: Read own record
- **WHEN** a caller with the namespace gate read action reads an existing record ID of the namespace
- **THEN** the server returns the stored record with its gates, outcomes, requirements, and fixed controls

### Requirement: Denied read in scope

When a caller with namespace access but without the gate read action reads an existing record of that scope, the server SHALL answer 403 with code gate.denied and no record content beyond the ID and link.

#### Scenario: No read action
- **WHEN** a caller with namespace access but without the gate read action reads an existing record of that scope
- **THEN** the server answers 403 with code gate.denied and no record content beyond the ID and link

### Requirement: Missing or out-of-scope record

When a caller reads a decision record ID that is missing, expired, malformed, or outside the namespace, the server SHALL answer 404 with code gate.not-found and no record content.

#### Scenario: Missing ID
- **WHEN** the caller reads an ID that is missing, expired, or malformed
- **THEN** the server answers 404 with code gate.not-found and no record content

#### Scenario: Foreign or remade scope
- **WHEN** the caller reads an ID of another namespace or of a scope removed and made again under the same name
- **THEN** the server answers 404 with code gate.not-found and no record content

### Requirement: No namespace access

When a caller with no namespace access or with no sign-in reads any record ID of the namespace, the server SHALL answer 404 with code gate.not-found and no record content.

#### Scenario: No access or no sign-in
- **WHEN** a caller with no namespace access or with no sign-in reads any record ID of the namespace
- **THEN** the server answers 404 with code gate.not-found and no record content

### Requirement: Each block names its record

If a gate evaluation gives a refusal, an approval wait, or a warning, then the server SHALL include the decision record ID and a link to the record read route in the response for the step.

#### Scenario: Denied state write
- **WHEN** a gate denies a state.write write
- **THEN** the error body holds the gdr- record ID and a link to the record read route

#### Scenario: Propose and apply refusals
- **WHEN** a gate denies a change.propose, change.planned, or change.apply step
- **THEN** the response holds the gdr- record ID and a link to the record read route

#### Scenario: Approval wait and warning
- **WHEN** a gate evaluation gives an approval wait or a warning at any decision point
- **THEN** the response holds the gdr- record ID and a link to the record read route

### Requirement: Approval refusal interim command

When an approval outcome refuses a change before the change-order slice ships, the server SHALL name the decision record ID and the record read command in the refusal and name no change-order command.

#### Scenario: Approval refusal help
- **WHEN** an approval outcome refuses a change before the change-order slice ships
- **THEN** the refusal names the gdr- record ID and the docs read command and names no change-order command

### Requirement: Race with expiry

When retention expiry, purge, or restore removes a decision record during a read, the server SHALL answer 404 with code gate.not-found and no record content.

#### Scenario: Purge during read
- **WHEN** retention expiry, purge, or restore removes the record during the read
- **THEN** the server answers 404 with code gate.not-found and no record content

### Requirement: Installation record guard

Where a decision record explains an access limit on an installation object, the server SHALL require the installation gate read action to read it.

#### Scenario: Installation limit record
- **WHEN** the record explains an access limit on an installation object
- **THEN** only a caller with the installation gate read action opens the record

### Requirement: Secret masking

Where a stored fact document holds secret values, the server SHALL mask each secret value on a record read by a caller without the secrets read action of the scope.

#### Scenario: Masked facts on read
- **WHEN** the stored fact document holds secret values and the caller lacks the secrets read action of the scope
- **THEN** the read returns the record with each secret value masked and the secret marks kept
