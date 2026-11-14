## ADDED Requirements

### Requirement: fixed-clock-exact-times

When the harness clock is fixed, the server SHALL stamp each harness record with the fixed time.

#### Scenario: fixed-clock-exact-times
- **WHEN** the harness clock is fixed
- **THEN** each record carries the fixed CreatedAt and the retention-derived expiry
