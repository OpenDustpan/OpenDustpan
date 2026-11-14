## ADDED Requirements

### Requirement: Dual unitId field

While the migration window lasts, the server SHALL answer each Unit item with both unitId and unitID holding the same stable ID.

#### Scenario: List units exposes both spellings
- **WHEN** an agent calls listUnits
- **THEN** each item holds unitId equal to unitID

#### Scenario: Get unit by ID exposes both spellings
- **WHEN** an agent calls the get unit route
- **THEN** the item holds unitId equal to unitID

#### Scenario: Wildcard list exposes both spellings
- **WHEN** an agent lists units across namespaces with -
- **THEN** each item holds unitId equal to unitID and names its namespace

### Requirement: Dual deleted-unit field

While the migration window lasts, the server SHALL answer each DeletedUnit item with both unitId and unitID holding the same stable ID.

#### Scenario: List deleted units exposes both spellings
- **WHEN** an agent lists deleted units
- **THEN** each item holds unitId equal to unitID

### Requirement: Dual context field

While the migration window lasts, the server SHALL answer each ContextObject with both unitId and unitID holding the same stable ID.

#### Scenario: Context object exposes both spellings
- **WHEN** an agent reads a ContextObject
- **THEN** the object holds unitId equal to unitID

### Requirement: Dual versionId field

While the migration window lasts, the server SHALL answer the closed job object with both versionId and versionID holding the same value.

#### Scenario: Closed job exposes both spellings
- **WHEN** an agent calls getJob on a closed job
- **THEN** the closed object holds versionId equal to versionID

#### Scenario: Open job omits closed version
- **WHEN** an agent calls getJob on a job that is not closed
- **THEN** the answer holds neither versionId nor versionID in the closed object

### Requirement: Dual sequence and operation fields

While the migration window lasts, the server SHALL answer each history and job store body with both sequenceId and operationId beside the old spellings holding the same values.

#### Scenario: History body exposes both spellings
- **WHEN** an agent reads a history or job store body
- **THEN** the body holds sequenceId equal to sequenceID and operationId equal to operationID

### Requirement: Dual acronym fields

While the migration window lasts, the server SHALL answer each acronym field with both the camelCase name and the old name holding the same value.

#### Scenario: Audit entry exposes new spellings
- **WHEN** an agent reads an audit entry
- **THEN** the entry holds tokenId, sourceIp, requestId, objectId and jobId beside the old names with equal values

#### Scenario: Installation objects expose new spellings
- **WHEN** an agent reads an installation object
- **THEN** each object holds principalId, backupId, grantId and clientId beside the old names with equal values

#### Scenario: Runner and agent results expose new spellings
- **WHEN** an agent reads a runner or agent-result object
- **THEN** each object holds keyId and consentId beside the old names with equal values

### Requirement: Dual URL fields

While the migration window lasts, the server SHALL answer each URL field with both the url spelling and the old URL spelling holding the same value.

#### Scenario: URL fields expose url spelling
- **WHEN** an agent reads an object with a URL field
- **THEN** each object holds authorizationUrl, tokenUrl, userinfoUrl, serverUrl, consentUrl, confirmUrl and reviewUrl beside the old URL names with equal values

### Requirement: Deprecated mark

While the migration window lasts, the server SHALL mark each old acronym spelling deprecated in the API description.

#### Scenario: Description marks old names
- **WHEN** an agent reads the API description
- **THEN** the API description marks unitID and versionID deprecated and keeps the new names current

### Requirement: Request input accepts both

When a request carries a renamed field, the server SHALL accept the new spelling and the old spelling with equal values and refuse different values with 400.

#### Scenario: Request with new name
- **WHEN** an agent sends only the new spelling
- **THEN** the server accepts the value

#### Scenario: Request with old name
- **WHEN** an agent sends only the old spelling
- **THEN** the server accepts the value

#### Scenario: Request with conflicting spellings
- **WHEN** an agent sends both spellings with different values
- **THEN** the server answers 400 with the NEW error code request.field-conflict

### Requirement: New routes use camelCase

The server SHALL name each field of each new route of this milestone with camelCase names unitId and versionId.

#### Scenario: New route uses new names
- **WHEN** an agent calls each new route of this milestone
- **THEN** the answer and the API description hold unitId and versionId only

### Requirement: Removal before stable

When the reader move completes and the migration window ends, the server SHALL answer only the camelCase names.

#### Scenario: Old names removed
- **WHEN** the reader move completes and the migration window ends
- **THEN** answers hold only unitId and versionId and the reflector rejects unitID and versionID
