## ADDED Requirements

### Requirement: Reduce plan to summary

The runner SHALL reduce a refresh-only plan of a drift check to a summary of addresses with the kind changed or deleted.

#### Scenario: Difference gives an address
- **WHEN** the refresh-only plan has a difference
- **THEN** the summary holds the address and kind and no attribute name or value

### Requirement: Cap the summary

The server SHALL cut a drift summary at 1000 addresses and set truncated.

#### Scenario: Large drift
- **WHEN** the plan differs in 1001 addresses
- **THEN** the stored result holds 1000 addresses and truncated is true

### Requirement: Record at phase end

When the plan phase of a drift check ends with a valid summary, the server SHALL record the result with the end time of the phase.

#### Scenario: Valid summary
- **WHEN** the plan phase ends with a valid summary
- **THEN** the drift read shows drifted or in-sync with the phase end time

### Requirement: Missing summary fails the check

If the summary is missing or fails Validate, then the server SHALL fail the phase and keep the last result.

#### Scenario: No artifact
- **WHEN** the plan phase ends with no summary artifact
- **THEN** the phase fails and the latest result is unchanged

#### Scenario: Hostile summary
- **WHEN** the summary has a control character, a duplicate address, or an address over 1024 characters
- **THEN** the phase fails and the latest result is unchanged

### Requirement: Early artifact is held

When the summary arrives before the plan phase ends, the server SHALL hold it as the job artifact and read only the latest one at phase end.

#### Scenario: Early arrival
- **WHEN** the summary arrives before the plan phase ends
- **THEN** the reconcile uses the latest artifact at phase end

#### Scenario: Replaced artifact
- **WHEN** the runner leaves a second summary for the same job
- **THEN** only the latest artifact counts

### Requirement: Use token identity

The server SHALL take the unit and job of a summary from the job token, never from the summary body.

#### Scenario: Foreign unit
- **WHEN** the summary names another unit
- **THEN** the reconcile refuses the summary and keeps the last result
