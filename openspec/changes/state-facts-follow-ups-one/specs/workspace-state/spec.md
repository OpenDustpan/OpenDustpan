## ADDED Requirements

### Requirement: Strict envelope for new writes

When a new state document carries encrypted_data, the server SHALL accept it only as the exact five-member envelope that IsClientEncrypted checks.

#### Scenario: Look-alike refused at upload
- **WHEN** an upload sends a document with encrypted_data plus an outside member
- **THEN** the server refuses the upload with invalid state and writes nothing.

#### Scenario: Real tofu envelope accepted
- **WHEN** state matches the five-member envelope of tofu 1.11.14 or 1.13.2
- **THEN** the server accepts the state.

#### Scenario: Stored look-alike stays readable
- **WHEN** an already-stored bare encrypted_data document is read
- **THEN** the server reads it with no refusal.

### Requirement: Limit code

When state is unreadable because it exceeds a limit, the server SHALL refuse with gate.limit.

#### Scenario: Over-limit state
- **WHEN** the state exceeds a limit and the ErrLimit branch is deleted
- **THEN** the server refuses with gate.limit.
