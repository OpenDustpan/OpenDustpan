## ADDED Requirements

### Requirement: Automatic token-disable audit entry

When the server automatically disables an access token for expiry, the server SHALL write one audit entry with actor server and the fixed disable reason.

#### Scenario: Automatic disable writes a server-actor entry
- **WHEN** the server automatically disables an access token for expiry
- **THEN** the audit log holds one entry with actor server, the stable token ID, and the fixed disable reason for that token ID

### Requirement: Automatic-disable atomicity

When the audit write for an automatic token disable fails, the server SHALL refuse the disable.

#### Scenario: Audit write failure refuses the disable
- **WHEN** the audit write for an automatic token disable fails
- **THEN** the token stays enabled and the audit log holds no entry for that disable

### Requirement: No secret value in the entry

When the server writes the automatic-disable audit entry, the server SHALL store the stable token ID and omit the secret value.

#### Scenario: Entry carries the token ID and no secret
- **WHEN** the server writes the automatic-disable audit entry
- **THEN** the entry carries the stable token ID and carries no secret value

### Requirement: Repeated disable writes no second entry

When the automatic-disable rule fires again for an already-disabled token, the server SHALL write no second audit entry.

#### Scenario: Second disable of an already-disabled token
- **WHEN** the automatic-disable rule fires again for an already-disabled token
- **THEN** the audit log holds no second entry for that token

### Requirement: Administrator reads the disable entry

When a site administrator with the audit read grant lists audit entries for the disabled token ID, the server SHALL return the disable entry.

#### Scenario: Site administrator lists entries for the token ID
- **WHEN** a site administrator with the audit read grant lists audit entries for the disabled token ID
- **THEN** the list returns the disable entry with actor server and the fixed disable reason
