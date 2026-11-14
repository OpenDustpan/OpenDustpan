## ADDED Requirements

### Requirement: Denial entry carries request fields

When the server refuses an output read, the server SHALL write SourceIP, UserAgent and RequestID in the denial audit entry.

#### Scenario: Denied read without rule records request fields
- **WHEN** the server refuses an output read with 403 for no sharing rule
- **THEN** the denial audit entry holds the SourceIP, UserAgent and RequestID of the request

#### Scenario: Denied read of client-encrypted state records request fields
- **WHEN** the server refuses an output read with 409 for client-encrypted state
- **THEN** the denial audit entry holds the SourceIP, UserAgent and RequestID of the request

#### Scenario: Empty fields stay empty
- **WHEN** the observed request fields are empty
- **THEN** the denial audit entry holds empty values and the read stays refused

### Requirement: Denial flood is limited

When denied output reads exceed the recorder budget, the server SHALL stop denial audit writes through the limiter.

#### Scenario: Flood of denied reads is limited
- **WHEN** denied output reads exceed the recorder budget
- **THEN** further denial entries stop while reads stay refused

### Requirement: Dropped entry stays refused

If the limiter drops the denial audit entry, then the server SHALL still refuse the output read.

#### Scenario: Dropped denial stays refused
- **WHEN** the limiter drops the denial audit entry
- **THEN** the read answers 403 or 409 with no output values

### Requirement: Append failure stays refused

If the denial audit append fails, then the server SHALL still refuse the output read.

#### Scenario: Audit append failure stays refused
- **WHEN** the denial audit append fails for a reason other than the limiter
- **THEN** the read answers 403 or 409 with no output values

### Requirement: Allowed read after rule writes no denial

When a sharing rule allows the output read, the server SHALL return the outputs without a denial audit entry.

#### Scenario: Read allowed after sharing rule
- **WHEN** a sharing rule names the consumer after an earlier refusal
- **THEN** the server returns the outputs and writes no denial entry
