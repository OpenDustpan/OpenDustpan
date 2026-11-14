## ADDED Requirements

### Requirement: strict-shape

When a page token holds a member outside the wire shape, the server SHALL reject the token.

#### Scenario: Extra member rejected
- **WHEN** a client sends a page token that decodes as JSON but holds a member outside the wire shape
- **THEN** the server rejects the token with an invalid-token error and runs no store range read.

#### Scenario: Empty token starts list
- **WHEN** a client sends a list call with no page token or an empty pageToken
- **THEN** the server treats the call as the first page.

### Requirement: trailing-data

When a page token holds non-whitespace data after the first JSON object, the server SHALL reject the token.

#### Scenario: Trailing data rejected
- **WHEN** a client sends a page token with non-whitespace data after the first JSON object
- **THEN** the server rejects the token with an invalid-token error.

#### Scenario: Trailing whitespace accepted
- **WHEN** a client sends a page token with only whitespace after the first JSON object
- **THEN** the server decodes the first JSON object.

### Requirement: length-fingerprint-encoding

When a page token fails the length cap, the fingerprint check, or base64 and JSON decode, the server SHALL reject the token.

#### Scenario: Overlong token rejected
- **WHEN** a client sends a page token longer than the 4 KiB cap
- **THEN** the server rejects the token with an invalid-token error and runs no store range read.

#### Scenario: Fingerprint mismatch rejected
- **WHEN** a client sends a deleted-units, registry provider, or namespace page token with a wrong filter fingerprint
- **THEN** the server rejects the token with an invalid-token error.

#### Scenario: Malformed token rejected
- **WHEN** a client sends a page token that is not valid base64 or not valid JSON
- **THEN** the server rejects the token with an invalid-token error.

#### Scenario: Valid token accepted
- **WHEN** a client sends a page token that shared Encode made for the same filter
- **THEN** the server decodes the cursor and runs the store read.

### Requirement: cursor-field-values

When a decoded deleted-units, registry provider, or namespace cursor holds a field outside the valid set, the server SHALL reject the token.

#### Scenario: Bad tool rejected
- **WHEN** a client sends a deleted-units cursor with a tool outside the known tool list
- **THEN** the server rejects the token with an invalid-token error and runs no store range read.

#### Scenario: Bad name rejected
- **WHEN** a client sends a deleted-units, registry provider, or namespace cursor with a name or unit ID that the domain parsers refuse
- **THEN** the server rejects the token with an invalid-token error.

#### Scenario: Valid cursor runs read
- **WHEN** a client sends a deleted-units, registry provider, or namespace cursor with valid field values and the correct fingerprint
- **THEN** the server uses the cursor as the range start of the store read.
