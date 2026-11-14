## ADDED Requirements

### Requirement: Create rule

When a caller creates a trust rule, the server SHALL check the namespace action on that namespace.

#### Scenario: Create valid rule
- **WHEN** a caller with the action and a fresh session creates a rule with a valid spiffeIDPattern
- **THEN** the server stores the rule with stable IDs and writes an audit entry

### Requirement: Update rule

When a caller runs update-rule, the server SHALL require a current ETag in If-Match.

#### Scenario: Stale update refused
- **WHEN** an update request carries a stale ETag
- **THEN** the server refuses with 412 and keeps the rule

#### Scenario: Current update stored
- **WHEN** an update carries the current ETag and a fresh session
- **THEN** the server stores the rule with a new ETag and writes an audit entry

### Requirement: Delete rule

When a caller deletes a trust rule, the server SHALL remove that rule only.

#### Scenario: Delete audited
- **WHEN** a caller with the action and a fresh session deletes a rule
- **THEN** the server removes the rule and writes an audit entry

### Requirement: Refuse cross-namespace rule

If a trust rule names another namespace, then the server SHALL refuse the write.

#### Scenario: Refuse other namespace
- **WHEN** a rule names another namespace
- **THEN** the server refuses with 403 and writes an audit entry

### Requirement: Refuse own-installation issuer URL

If a trust rule names a JWT issuer URL of this installation, then the server SHALL refuse the write.

#### Scenario: Own issuer refused
- **WHEN** a rule names an issuer URL of this installation
- **THEN** the server refuses with 400
