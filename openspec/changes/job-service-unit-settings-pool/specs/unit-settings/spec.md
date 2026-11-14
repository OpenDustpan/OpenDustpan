## ADDED Requirements

### Requirement: Stored code location and pool

Where unit settings are stored, the server SHALL hold the code location, runner pool, profile reference and default steps of the unit.

#### Scenario: Save and read location
- **WHEN** A namespace administrator saves the code location, pool, profile reference and steps on the unit settings.
- **THEN** A later start with no request location uses the stored location.

#### Scenario: Renamed profile still resolves
- **WHEN** A profile named in unit settings is renamed.
- **THEN** A later start uses the same profile through its stable ID.

#### Scenario: Removed profile fails closed
- **WHEN** A profile named in unit settings is removed.
- **THEN** The next start answers 422 and creates no job.
