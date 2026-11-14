## ADDED Requirements

### Requirement: The version of the API description stays fixed before the first tag

`make pin-frozen` SHALL fail when `DocumentVersion`, info.version of the API description or the version line of openapi.pin differs from `0.36.0` before the first tag.

#### Scenario: Values unchanged
- **WHEN** a pull request leaves the three values at `0.36.0`
- **THEN** `make pin-frozen` passes

#### Scenario: Version raised
- **WHEN** a pull request changes info.version to `0.37.0` before the first tag
- **THEN** `make pin-frozen` fails and says that the first release sets the values
