## ADDED Requirements

### Requirement: credentials-issued-audit

When the NEW method IssueCredentials issues runner credentials, the server SHALL write one EventCredentialsIssued audit entry in the same store transaction as the credential.

#### Scenario: Issuance writes the event
- **WHEN** The NEW method IssueCredentials issues runner credentials through ANY /runner/v1.
- **THEN** The audit log stores one EventCredentialsIssued entry in the same transaction as the credential.

#### Scenario: Audit write failure refuses issuance
- **WHEN** The EventCredentialsIssued write fails.
- **THEN** The server rolls back the credential and refuses issuance with runner.invalid and no credentials.

#### Scenario: Refused issuance writes nothing
- **WHEN** A credential request is refused or fails.
- **THEN** The audit log stores no EventCredentialsIssued entry.

#### Scenario: Producer set is complete
- **WHEN** The producer test lists audit events without a producer.
- **THEN** The producer test passes with EventCredentialsIssued removed from the unproduced set.

### Requirement: entry-contents

When the server writes the EventCredentialsIssued entry, the server SHALL store the requesting runner, the pool, the credential ID, the expiry and the reason without the secret credential value.

#### Scenario: Entry carries identity without secret
- **WHEN** The server writes the EventCredentialsIssued entry.
- **THEN** The entry carries the requesting runner as actor, the pool as object, the credential ID, the expiry and the reason, and carries no secret credential value.
