## ADDED Requirements

### Requirement: Capped rate limit wait

When GitHub asks for a wait above 60 seconds per waitTime call, the server SHALL cap the waitTime result to 60 seconds.

#### Scenario: Caps hostile Retry-After of one hour
- **WHEN** GitHub answers with a one hour retry header
- **THEN** the waitTime result is 60 seconds and the check reports rate limited at once

#### Scenario: Keeps a wait below the cap
- **WHEN** GitHub asks for a 5 second wait
- **THEN** the waitTime result equals 5 seconds

#### Scenario: Falls back on hostile headers
- **WHEN** headers are missing, malformed, negative, or carry a past date
- **THEN** waitTime returns 5 seconds

### Requirement: One no-registration answer

When no registration exists for an existing connection, the server SHALL answer status 409 with the same coded body on check and on create.

#### Scenario: Check with no registration
- **WHEN** a check runs on an existing connection with no registration
- **THEN** the server answers 409 with the coded body that create answers

#### Scenario: Create with no registration
- **WHEN** a create runs where no registration exists
- **THEN** the server answers 409 with the same coded body as check

#### Scenario: Check on a missing connection
- **WHEN** a check runs on a connection ID that does not exist
- **THEN** the server answers 404
