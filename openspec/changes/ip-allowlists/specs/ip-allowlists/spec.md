## ADDED Requirements

### Requirement: Flag default off

The server SHALL ship with the installation feature flag off.

#### Scenario: Fresh installation
- **WHEN** the installation never turned the feature on
- **THEN** requests pass with no allowlist check

### Requirement: Deny when any list denies

When the client address sits outside any opted-in installation, namespace or token list, the server SHALL answer 403 with the refusing scope.

#### Scenario: Address misses one list
- **WHEN** the address matches one opted-in list but not another
- **THEN** the server answers 403 with error code ip-allowlist.denied and the scope

### Requirement: Invalid entry

When a settings write holds an invalid CIDR, the server SHALL answer 400.

#### Scenario: Bad CIDR
- **WHEN** a write holds 999.999.0.0/16
- **THEN** the server answers 400 and stores no change

### Requirement: Check order

When the client address sits outside the opted-in installation list, the server SHALL answer 403 before credential validation.

#### Scenario: Denied network
- **WHEN** the address is outside the installation list
- **THEN** the server answers 403 without checking the credential

### Requirement: Recorded address

The server SHALL check the recorded client address from the installation trusted-proxy handling.

#### Scenario: Spoofed header
- **WHEN** a request carries a proxy header from an untrusted source
- **THEN** the server ignores the header

### Requirement: Denial audit

When a request arrives from outside an opted-in list, the server SHALL write an audit entry with the scope and the address.

#### Scenario: Denied request
- **WHEN** a token request arrives from a denied address
- **THEN** the server writes an audit entry

### Requirement: Empty list

When a scope opts in with an empty list, the server SHALL deny each address for that scope.

#### Scenario: Empty list
- **WHEN** a scope opts in with an empty list
- **THEN** the server answers 403 for each address
