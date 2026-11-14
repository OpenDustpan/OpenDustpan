## ADDED Requirements

### Requirement: Replaceable checks

When an embedder passes custom Validators with NEW WithValidators, the server SHALL use those functions for the four checks.

#### Scenario: Default checks run
- **WHEN** the caller uses pkg/preferences with no custom Validators
- **THEN** the patch path calls the checks from the NEW package pkg/locale

#### Scenario: Custom digits replace default
- **WHEN** the caller passes custom Validators with NEW WithValidators
- **THEN** the patch path accepts the custom digits list and rejects other values
