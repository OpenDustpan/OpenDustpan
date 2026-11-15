## ADDED Requirements

### Requirement: Single use

When a caller presents a valid break-glass pass, the server SHALL mark the pass used in the same transaction as the audit entry of the use.

#### Scenario: Second use
- **WHEN** a caller presents the same pass twice
- **THEN** the server refuses the second request and writes an audit entry

### Requirement: Expiry

If a pass is older than 15 minutes, then the server SHALL refuse the pass.

#### Scenario: Late use
- **WHEN** a caller presents a pass after 15 minutes
- **THEN** the server answers 403 and writes no state
