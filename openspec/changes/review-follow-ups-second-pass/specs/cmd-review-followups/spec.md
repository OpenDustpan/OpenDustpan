## ADDED Requirements

### Requirement: devreset-next-step

If dev reset fails, then the server SHALL name the next step in the error text.

#### Scenario: devreset-symlinked-old
- **WHEN** dev reset meets a symlinked old entry
- **THEN** the error text names the next step to run
