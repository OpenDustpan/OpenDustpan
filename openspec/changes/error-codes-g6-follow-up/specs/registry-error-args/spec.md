## ADDED Requirements

### Requirement: Size limit argument

When a registry release exceeds its byte limit, the server SHALL carry limitBytes as a quantity argument.

#### Scenario: Large release
- **WHEN** a publish exceeds the byte limit
- **THEN** the answer carries code registry.too-large with errorArgs limitBytes.

### Requirement: Too many files

When a registry publish exceeds its file count bound, the server SHALL carry limit as a quantity argument.

#### Scenario: File count
- **WHEN** a publish has too many files
- **THEN** the answer carries the NEW code registry.too-many-files with errorArgs limit.

### Requirement: Protocol limit

When a registry publish exceeds its plugin protocol version bound, the server SHALL carry limit as a quantity argument.

#### Scenario: Protocol count
- **WHEN** a publish names too many protocol versions
- **THEN** the answer carries the NEW code registry.protocol-limit with errorArgs limit.

#### Scenario: Request-only causes
- **WHEN** a publish fails on a request-only value
- **THEN** the answer carries code registry.invalid with no argument.
