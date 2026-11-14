## ADDED Requirements

### Requirement: Fail on unknown read

When a supported CLI sends a read that its contract file does not list, the e2e contract check SHALL fail.

#### Scenario: Unknown read
- **WHEN** a CLI sends a read that no contract file lists
- **THEN** the contract check fails and names the method and the path

#### Scenario: Refused read
- **WHEN** a CLI sends a read that the contract file records as refused with a reason
- **THEN** the contract check passes
