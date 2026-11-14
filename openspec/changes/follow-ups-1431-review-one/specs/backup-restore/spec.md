## ADDED Requirements

### Requirement: staging-name-entry-check

When the restore import receives a staging schema name, the server SHALL reject a name refused by checkStagingIdent before any staged query runs.

#### Scenario: Empty staging name
- **WHEN** the restore import receives an empty staging schema name
- **THEN** the import fails closed with no staged query run.

#### Scenario: Leading digit
- **WHEN** the restore import receives a staging schema name with a leading digit
- **THEN** the import fails closed with no staged query run.

#### Scenario: Overlong name
- **WHEN** the restore import receives a 64-char staging schema name
- **THEN** the import fails closed with no staged query run.

#### Scenario: Long valid name
- **WHEN** the restore import receives a 63-char staging schema name accepted by checkStagingIdent
- **THEN** staged checks including checkStagedSealedState run to their normal result.
