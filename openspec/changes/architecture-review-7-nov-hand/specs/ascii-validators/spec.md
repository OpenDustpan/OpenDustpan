## ADDED Requirements

### Requirement: boundary-parity

When a validator receives a boundary input, the server SHALL accept or reject exactly as its existing allowlist defines.

#### Scenario: boundary-bytes
- **WHEN** a validator receives a boundary input of '@', '[', backquote, '{', '/', ':', 0x7f, 0x80, multi-byte UTF-8, empty input, or NUL
- **THEN** the table test passes for each byte without changing production behavior

#### Scenario: class-edges
- **WHEN** a validator receives a class edge of 'A', 'Z', 'a', 'z', '0', '9', '+', '/', '=', or an input at its length limit
- **THEN** the table test passes for each edge without changing production behavior

#### Scenario: mutation-proof
- **WHEN** one boundary byte of a validator range is mutated by one
- **THEN** the table test fails on the mutated range

### Requirement: fuzz-parity

When a validator receives fuzz input, the server SHALL match the anchored byte oracle in the test.

#### Scenario: fuzz-parity
- **WHEN** a validator receives fuzz input
- **THEN** each fuzz input matches the anchored byte oracle

#### Scenario: url-oracle-scope
- **WHEN** the parseURL or checkPublicURL oracle receives a URL input
- **THEN** trimming, scheme, host, and port map to accept and any error maps to reject as the oracle defines
