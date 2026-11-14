## ADDED Requirements

### Requirement: Create rule

When a caller runs create-rule, the server SHALL check the namespace action on that namespace.

#### Scenario: Create valid rule
- **WHEN** caller with the action and fresh session runs create-rule with a valid SPIFFE ID pattern
- **THEN** server stores the rule with stable IDs and writes an audit entry

### Requirement: Update rule

When a caller runs update-rule, the server SHALL require a current ETag in If-Match.

#### Scenario: Stale update refused
- **WHEN** update carries a stale ETag
- **THEN** server refuses with 412 and keeps the rule

### Requirement: Delete rule

When a caller runs delete-rule, the server SHALL remove that rule only.

#### Scenario: Delete audited
- **WHEN** caller with the action and fresh session runs delete-rule
- **THEN** server removes the rule and writes an audit entry

### Requirement: Refuse cross-namespace rule

If a trust rule names another namespace, then the server SHALL refuse the write.

#### Scenario: Refuse other namespace
- **WHEN** rule names another namespace
- **THEN** server refuses with 403 and writes an audit entry

### Requirement: Refuse own-installation issuer URL

If a trust rule names a JWT issuer URL of this installation, then the server SHALL refuse the write.

#### Scenario: Own issuer refused
- **WHEN** rule names an issuer URL of this installation
- **THEN** server refuses with 400
