## ADDED Requirements

### Requirement: List workspaces

When a selection names an organization, the server SHALL list its workspaces through the NEW tfe-api adapter.

#### Scenario: Known organization
- **WHEN** the adapter lists units for a selection
- **THEN** only the selected workspaces return with name and ID

#### Scenario: Empty organization
- **WHEN** the organization holds no workspace
- **THEN** the adapter answers an empty unit list

#### Scenario: Missing workspace
- **WHEN** the selection names a workspace that does not exist
- **THEN** the adapter answers the NEW error code import.source-not-found

### Requirement: List state versions

When a caller lists versions of one workspace, the server SHALL answer at most 100 versions per call oldest first with its ID, time, serial, lineage, MD5 and size.

#### Scenario: Paged versions
- **WHEN** the adapter lists versions after a version ID with a page limit of 100 versions per call
- **THEN** at most 100 versions per Versions call for one workspace return in oldest-first order

#### Scenario: Next page link
- **WHEN** the version list spans two pages
- **THEN** the adapter follows the next-page link through egresshttp and keeps the credential to the import origin

#### Scenario: Malformed body
- **WHEN** the source answers malformed JSON
- **THEN** the adapter answers the NEW error code import.source-unavailable with no retry

### Requirement: Open state bytes

When a caller opens one state version, the server SHALL stream its bytes through egresshttp with no cross-host redirect.

#### Scenario: One version
- **WHEN** the adapter opens one version
- **THEN** the bytes match the listed size and MD5

#### Scenario: Oversize stream
- **WHEN** streamed bytes pass 32 megabytes per version
- **THEN** the adapter aborts and returns a typed size error with no partial version

#### Scenario: Cross-host download
- **WHEN** a download URL names another host
- **THEN** the adapter fails and sends no credential

#### Scenario: Same-host redirect
- **WHEN** a download redirects on the same scheme host and port
- **THEN** the adapter follows it and keeps the credential

#### Scenario: Unknown version
- **WHEN** the version ID is unknown
- **THEN** the adapter answers the NEW error code import.source-not-found

### Requirement: State documents

When the source gives json-state documents, the server SHALL read them with the state file.

#### Scenario: Outputs present
- **WHEN** the source gives json-state documents
- **THEN** the adapter answers the json-state and json-state-outputs documents

#### Scenario: Outputs absent
- **WHEN** the source gives no json-state documents
- **THEN** the adapter answers the state file with empty outputs and no error

### Requirement: Credential refusal

When the source refuses the credential, the server SHALL answer the NEW error code import.source-denied.

#### Scenario: Bad credential
- **WHEN** the credential is wrong
- **THEN** the adapter lists nothing and returns the error

#### Scenario: Redacted credential
- **WHEN** code prints the NEW credential value
- **THEN** the output is [credential]

#### Scenario: Private address
- **WHEN** the source address is private with no allowlist entry
- **THEN** the adapter refuses it before any token leaves

#### Scenario: Timeout
- **WHEN** the source is slow past 5 seconds per request
- **THEN** the adapter aborts and answers the NEW error code import.source-unavailable

#### Scenario: Server error
- **WHEN** the source answers 500 or 429
- **THEN** the adapter retries at most 2 more times and then answers the NEW error code import.source-unavailable

### Requirement: Source lock

When the caller passes lockSource true, the server SHALL lock the workspace through the NEW Locker capability and unlock it after the read.

#### Scenario: Default read-only
- **WHEN** lockSource is off
- **THEN** the adapter sends only read requests

#### Scenario: Unlock after failed read
- **WHEN** the read fails
- **THEN** the adapter unlocks and then reports the read error

#### Scenario: Held lock
- **WHEN** the workspace is already locked
- **THEN** the adapter answers the NEW error code import.source-unavailable with no overwrite
