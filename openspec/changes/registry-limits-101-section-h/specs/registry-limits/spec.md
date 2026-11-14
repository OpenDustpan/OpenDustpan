## ADDED Requirements

### Requirement: RecordUpload hourly cap

When a principal exceeds 60 RecordUpload calls in one fixed UTC hour, the server SHALL refuse the upload.

#### Scenario: 61st RecordUpload refused
- **WHEN** A principal sends a 61st accepted RecordUpload in one fixed UTC hour
- **THEN** The server answers 429 with registry.upload-rate-limited and Retry-After seconds to the next UTC hour

### Requirement: RecordUpload counting scope

When the server counts RecordUpload calls, the server SHALL count only accepted uploads per principal subject stable ID across all namespaces.

#### Scenario: Failed RecordUpload calls do not count
- **WHEN** An upload fails validation or is refused as too large
- **THEN** The hourly count stays unchanged

### Requirement: RecordUpload count release

When the blob write or publish of a counted RecordUpload fails, the server SHALL release the hourly count by rollback of the same SQL transaction.

#### Scenario: RecordUpload blob failure releases count
- **WHEN** The blob write fails after the atomic increment of RecordUpload
- **THEN** The hourly count stays unchanged

### Requirement: Version cap

When PublishVersion targets an address that already holds 1,000 versions, the server SHALL refuse the publish.

#### Scenario: 1,001st version refused
- **WHEN** PublishVersion targets an address with 1,000 versions
- **THEN** The server answers 409 with registry.version-limit and stores no new version

### Requirement: Version count scope

When the server counts versions of one registry address, the server SHALL count every stored version of that address.

#### Scenario: Yanked versions count
- **WHEN** An address holds 1,000 versions including yanked ones
- **THEN** The server refuses the publish with registry.version-limit

### Requirement: File size cap

When a staged registry file exceeds 512 MiB, the server SHALL refuse the upload.

#### Scenario: Oversize file refused during stream
- **WHEN** A staged file exceeds 512 MiB with a false Content-Length
- **THEN** The server aborts the read, answers 413 with registry.too-large, and deletes any partial blob

### Requirement: Release size cap

When a registry release archive set exceeds 2 GiB, the server SHALL refuse the publish.

#### Scenario: Oversize release refused
- **WHEN** A release archive set exceeds 2 GiB at publish
- **THEN** The server answers 413 with registry.too-large and stores no version

### Requirement: Configurable limits

Where the installation sets a registry limit, the server SHALL use that value instead of the default.

#### Scenario: Operator raises a cap
- **WHEN** A site administrator sets a registry limit within bounds
- **THEN** Later writes use the new value and the settings read shows it

### Requirement: Settings validation

When a settings write names an unknown registry limit key or an out-of-bounds value, the server SHALL refuse the write.

#### Scenario: Bad value refused
- **WHEN** A settings write names an unknown key or a value below 1
- **THEN** The server answers 400 with registry.invalid and keeps the prior value

### Requirement: Lowered cap

When a new registry cap falls below stored data, the server SHALL refuse only new excess writes.

#### Scenario: Reads continue
- **WHEN** A new cap falls below stored versions
- **THEN** Reads succeed and new excess publishes refuse
