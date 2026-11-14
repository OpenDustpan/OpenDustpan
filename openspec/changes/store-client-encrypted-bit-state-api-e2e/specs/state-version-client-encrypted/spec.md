## ADDED Requirements

### Requirement: Show the bit

When a client reads a state version of a workspace, the server SHALL return the client-encrypted bit of that version.

#### Scenario: Encrypted state
- **WHEN** the real OpenTofu CLI applies with state encryption on and the client reads the state version
- **THEN** the field is true

#### Scenario: Plain state
- **WHEN** the real OpenTofu CLI applies with no state encryption and the client reads the state version
- **THEN** the field is false
