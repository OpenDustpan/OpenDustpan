## ADDED Requirements

### Requirement: Warning field in token API

When a caller reads an access token, the server SHALL include the expiry time, the seconds left from the server clock, and the warning level in the token response.

#### Scenario: Token inside warning window
- **WHEN** a caller reads a token that expires within the first warning threshold
- **THEN** the token response holds expiresAt, seconds left computed from the server clock, and warning level early or urgent

#### Scenario: Token outside warning window
- **WHEN** a caller reads a token that expires after the first warning threshold
- **THEN** the token response holds expiresAt with warning level none

#### Scenario: Expired token read
- **WHEN** a caller reads a token whose expiry passed
- **THEN** the response still holds expiresAt with warning level expired and zero seconds left

#### Scenario: Token with no expiry
- **WHEN** a caller reads a token with no expiry
- **THEN** the response holds null expiry with warning level none

### Requirement: Warning threshold rule

When the server computes the warning level, the server SHALL apply inclusive boundaries with urgent precedence and treat 0 as a disabled window.

#### Scenario: Exact boundary
- **WHEN** a token expires exactly at a threshold
- **THEN** the level is early at exactly 14 days and urgent at exactly 3 days

#### Scenario: Zero threshold
- **WHEN** an administrator sets a warning threshold to 0
- **THEN** that window never matches

### Requirement: Warning field authorization

If a caller lacks the token read grant, then the server SHALL refuse the token read with 403.

#### Scenario: Refused token read
- **WHEN** a caller without the token read grant reads a token
- **THEN** the server answers 403 with the missing action and object and returns no warning field

### Requirement: Console banner data

When the console reads the current token inside the warning window, the server SHALL answer the same warning level and time left used for the banner.

#### Scenario: Token near expiry in console
- **WHEN** the console reads the current token inside the warning window
- **THEN** the session token answer holds the same warning level and seconds left for the banner

#### Scenario: Token list near expiry
- **WHEN** a caller lists tokens and one token is inside the warning window
- **THEN** each listed token holds its own warning level and seconds left

### Requirement: CLI warning for both tools

When a tool-compatible request uses a token inside the warning window, the server SHALL carry the warning level and the time left in the response.

#### Scenario: Pulumi CLI request near expiry
- **WHEN** a Pulumi CLI request uses a token inside the warning window
- **THEN** the response carries the warning header and seconds left that the CLI shows

#### Scenario: OpenTofu request near expiry
- **WHEN** an OpenTofu request uses a token inside the warning window
- **THEN** the response carries the warning header and seconds left that the CLI shows
