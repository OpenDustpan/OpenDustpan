## ADDED Requirements

### Requirement: Stored code location and pool

Where unit settings are stored, the server SHALL hold the code location, runner pool, profile reference and default steps of the unit.

#### Scenario: Save and read location
- **WHEN** an administrator saves the code location on the unit settings
- **THEN** a later start with no request location uses it
