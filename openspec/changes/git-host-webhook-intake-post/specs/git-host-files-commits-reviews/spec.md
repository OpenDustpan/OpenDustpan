## ADDED Requirements

### Requirement: Read and commit methods

The server SHALL provide Head, ReadFile, Compare and FindCommit in the GitHub adapter behind their capabilities.

#### Scenario: Read file at ref
- **WHEN** a caller calls Head and ReadFile for a known path and ref
- **THEN** the adapter returns the file bytes and its blob identifier

#### Scenario: Compare and find commit
- **WHEN** a caller calls Compare for two refs and FindCommit for a known sha
- **THEN** the adapter returns the changed files and the commit record

#### Scenario: Check refused by host
- **WHEN** the Git host answers Check with an auth failure or a rate limit
- **THEN** the adapter returns a refusal that names the host and the one check command

#### Scenario: Unsupported call
- **WHEN** a caller calls a method that the host or auth kind cannot do
- **THEN** the adapter returns ErrUnsupported

### Requirement: Review methods

The server SHALL provide OpenReview, ReadReview and CloseReview in the GitHub adapter behind their capabilities.

#### Scenario: Open and read review
- **WHEN** a caller calls OpenReview and ReadReview for a known review number
- **THEN** the adapter returns the review with its head and state

#### Scenario: Close review
- **WHEN** a caller calls CloseReview for an open review
- **THEN** the adapter marks the review closed at the host
