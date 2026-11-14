## ADDED Requirements

### Requirement: Reject hidden runes in unit references

When a unit reference holds a rune in Cc, Cf, Zl, or Zp in any part, the server SHALL refuse the reference as invalid.

#### Scenario: Bidi override in unit part
- **WHEN** reference holds U+202E in unit part
- **THEN** ParseObject returns an invalid-unit error and stores nothing

#### Scenario: Line separator in unit part
- **WHEN** reference holds U+2028 in unit part
- **THEN** ParseObject returns an invalid-unit error and stores nothing

#### Scenario: Paragraph separator in unit part
- **WHEN** reference holds U+2029 in unit part
- **THEN** ParseObject returns an invalid-unit error and stores nothing

#### Scenario: Isolate override in unit part
- **WHEN** reference holds U+2066 in unit part
- **THEN** ParseObject returns an invalid-unit error and stores nothing

#### Scenario: Zero-width space in unit part
- **WHEN** reference holds U+200B in unit part
- **THEN** ParseObject returns an invalid-unit error and stores nothing

#### Scenario: Control character in unit part
- **WHEN** reference holds newline or U+0007 in unit part
- **THEN** ParseObject returns an invalid-unit error and stores nothing

#### Scenario: Hidden rune in namespace part
- **WHEN** reference holds U+202E in namespace part
- **THEN** ParseObject returns an invalid-unit error and stores nothing

#### Scenario: Hidden rune in tool part
- **WHEN** reference holds U+202E in tool part
- **THEN** ParseObject returns an invalid-unit error and stores nothing

#### Scenario: Saved view reference
- **WHEN** saved view reference holds U+202E
- **THEN** saved view creation returns an invalid-unit error and stores nothing

#### Scenario: Error reply holds no raw hidden text
- **WHEN** reference holds U+202E
- **THEN** error reply holds no raw hidden rune

#### Scenario: Valid Unicode name stays accepted
- **WHEN** reference holds a Unicode name with no rune in Cc, Cf, Zl, or Zp
- **THEN** ParseObject accepts the reference
