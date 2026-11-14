## ADDED Requirements

### Requirement: Shared hidden-rune refusal

The server SHALL refuse a new input value that holds a hidden rune of the shared Cc, Cf, Zl and Zp set.

#### Scenario: Line separator in stack page input
- **WHEN** a stack page input holds U+2028
- **THEN** the server refuses the value with an invalid-input error

#### Scenario: Zero width space in token name
- **WHEN** an access token name holds U+200B
- **THEN** the server refuses the value with an invalid-input error

#### Scenario: Paragraph separator in trust-rule input
- **WHEN** a trust-rule input holds U+2029
- **THEN** the server refuses the value with an invalid-input error

### Requirement: Cursor refusal

The server SHALL refuse a deleted-unit cursor that holds a hidden rune of the shared set.

#### Scenario: Paragraph separator in cursor
- **WHEN** a deleted-unit cursor holds U+2029
- **THEN** the server refuses the cursor with an invalid-input error

### Requirement: Stored values stay readable

The server SHALL read a stored row that was saved before the stricter set landed.

#### Scenario: Old stored value still reads
- **WHEN** a stored row saved before the change holds a newly refused rune and the read uses a clean cursor
- **THEN** the read answers the stored value without a new refusal

### Requirement: Chat line feed stays allowed

Where the chat message body allows line breaks, the server SHALL accept U+000A and refuse each other hidden rune of the shared set.

#### Scenario: Line feed in chat message
- **WHEN** a chat message holds U+000A and no other hidden rune
- **THEN** the server accepts the message

#### Scenario: Zero width space in chat message
- **WHEN** a chat message holds U+200B
- **THEN** the server refuses the message with an invalid-input error
