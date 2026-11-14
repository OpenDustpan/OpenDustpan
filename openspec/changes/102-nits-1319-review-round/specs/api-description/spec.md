## ADDED Requirements

### Requirement: authorizer-subset

When the server handles each described operation, the server SHALL record at least one authorizer call per operation unless the operation is in the public exempt allowlist, and keep all recorded pairs as a subset of ActionObjects.

#### Scenario: subset holds
- **WHEN** each described operation records authorizer calls
- **THEN** the test passes

#### Scenario: drift fails
- **WHEN** an authorizer call is not in ActionObjects
- **THEN** the test fails and names the extra pair

#### Scenario: skipped operation fails
- **WHEN** an operation records zero calls and is not in the exempt allowlist
- **THEN** the test fails for that operation
