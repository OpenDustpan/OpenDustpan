## ADDED Requirements

### Requirement: Private ranges from pool

When a tunnel host resolves to a private address, the server SHALL open it only inside a range that the runner pool allows.

#### Scenario: Private host outside range
- **WHEN** a tunnel host resolves to a private address outside the ranges that the pool allows
- **THEN** the proxy refuses the connection.
