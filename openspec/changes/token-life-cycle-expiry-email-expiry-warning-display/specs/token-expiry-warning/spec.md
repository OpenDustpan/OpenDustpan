## ADDED Requirements

### Requirement: Warning field in token reads

When a caller reads an access token, the server SHALL include the expiry time, the seconds left from the server clock and the warning level in the response.

#### Scenario: Inside window
- **WHEN** a caller reads a token that expires within the first threshold
- **THEN** the response holds the expiry, the seconds left and the level early or urgent

#### Scenario: Expired
- **WHEN** a caller reads a token whose expiry passed
- **THEN** the response holds level expired and zero seconds left

#### Scenario: No expiry
- **WHEN** a caller reads a token with no expiry
- **THEN** the response holds a null expiry and level none

### Requirement: Threshold rule

When the server computes the warning level with newWarningState, the server SHALL apply inclusive boundaries, give urgent precedence and treat a threshold of 0 as a disabled window.

#### Scenario: Exact boundary
- **WHEN** a token expires exactly at the second threshold
- **THEN** the level is urgent

#### Scenario: Zero threshold
- **WHEN** an administrator sets a threshold to 0
- **THEN** that window never matches

### Requirement: Read authorization

If a caller lacks the token read action on the token, then the server SHALL refuse the read with 403.

#### Scenario: Refused read
- **WHEN** a caller without the grant reads a token
- **THEN** the server answers 403 and returns no warning field

### Requirement: Console banner data

When the console reads the current token inside the warning window, the server SHALL answer the same level and seconds left that the banner shows.

#### Scenario: Banner data
- **WHEN** the console reads the current token inside the window
- **THEN** the answer holds the level and the seconds left
