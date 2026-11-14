## ADDED Requirements

### Requirement: Reject hidden runes in audit objects

When an audit entry holds a rune in Cc, Cf, Zl, or Zp in Object or ObjectID, the server SHALL refuse the entry as invalid.

#### Scenario: Hidden rune in Object
- **WHEN** audit entry Object holds U+202E
- **THEN** Validate returns an invalid-object error

#### Scenario: Hidden rune in ObjectID
- **WHEN** audit entry ObjectID holds U+2028
- **THEN** Validate returns an invalid-object error

#### Scenario: Valid entry stays unchanged
- **WHEN** Object and ObjectID hold no rune in Cc, Cf, Zl, or Zp
- **THEN** Validate accepts the entry without change

#### Scenario: Reason check stays
- **WHEN** audit entry Reason holds a hidden rune
- **THEN** Validate still refuses hidden runes in Reason

### Requirement: Clean denied audit objects first

When the server writes a denied audit entry, the server SHALL apply refusal.Clean to Object and ObjectID before Validate.

#### Scenario: Denied raw reference is cleaned
- **WHEN** a denied audit entry carries a raw reference with a hidden rune through refusal.Clean
- **THEN** the stored refusal record holds the cleaned value and Validate accepts it
