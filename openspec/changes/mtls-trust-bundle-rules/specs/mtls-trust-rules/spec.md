## ADDED Requirements

### Requirement: Rule CRUD with auth

When a caller manages trust rules, the server SHALL check the namespace action on that namespace.

#### Scenario: List rules of own namespace
- **WHEN** caller with read grant lists trust rules
- **THEN** server answers only the rules of that namespace

### Requirement: Rule stores stable references

When the server stores a trust rule, the server SHALL store the stable rule ID, the principal subject, and the bundle version reference.

#### Scenario: Rule read shows stable IDs
- **WHEN** caller reads a saved rule
- **THEN** server answers the stable rule ID, the principal subject, and the bundle version reference

### Requirement: Refuse own issuer

If a trust rule names an issuer of this installation, then the server SHALL refuse the write.

#### Scenario: Own issuer refused
- **WHEN** rule names an issuer at or under a URL of an issuer of this installation
- **THEN** server refuses with 400 and stores nothing

### Requirement: Refuse cross-namespace access

If a trust rule names another namespace, then the server SHALL refuse the write.

#### Scenario: Refuse other namespace
- **WHEN** rule names another namespace or a principal outside it
- **THEN** server refuses with 403 and writes an audit entry

### Requirement: Fresh auth for rule change

When a caller creates, updates, or deletes a trust rule, the server SHALL require a fresh authentication.

#### Scenario: Stale auth refused
- **WHEN** rule create, update, or delete arrives with stale authentication
- **THEN** server refuses with fresh-authentication-required

### Requirement: Refuse bad SPIFFE ID

If a trust rule holds a SPIFFE ID pattern with a bad shape, then the server SHALL refuse the write.

#### Scenario: Bad pattern refused
- **WHEN** rule holds a SPIFFE ID pattern with a bad shape
- **THEN** server refuses with 400 and names the field

### Requirement: Rotated bundle version

When the bundle version of a rule rotates out or expires, the server SHALL mark the rule and refuse its use.

#### Scenario: Expired version shown
- **WHEN** the bundle version of a rule rotates out or its CA expires
- **THEN** server marks the rule with the expired version and refuses its use

### Requirement: Delete rule

When a caller deletes a trust rule, the server SHALL remove that rule only.

#### Scenario: Delete audited
- **WHEN** caller with the namespace action deletes a rule with fresh authentication
- **THEN** server removes the rule and writes an audit entry with actor, namespace, and rule ID

### Requirement: Audit rule change

When a bundle save or a rule change completes, the server SHALL write an audit entry.

#### Scenario: Change audited
- **WHEN** bundle save or rule change succeeds or is refused
- **THEN** audit log holds actor, namespace, rule ID, and reason
