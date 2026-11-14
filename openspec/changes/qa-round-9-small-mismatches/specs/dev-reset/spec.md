## ADDED Requirements

### Requirement: Failed remake names copies

When a dev reset fails after it moved the data aside, the dev command SHALL name the kept copies and the put-back steps in the error.

#### Scenario: Failed remake
- **WHEN** a dev reset fails after it moved the data aside
- **THEN** the dev error names dev.db.reset.bak, dev.db.reset.bak.old when present, and the steps ls D and cp of the kept copy over dev.db

#### Scenario: Half-made database
- **WHEN** a failed remake left a half-made dev.db beside a kept copy
- **THEN** the next dev start warns that a kept copy holds the real data

### Requirement: Kept copy guard

When a kept copy holds data and the current database holds zero namespace rows, the dev command SHALL refuse to replace the kept copy.

#### Scenario: Empty database with kept copy
- **WHEN** a dev reset runs while a kept copy holds data and the database holds zero namespace rows
- **THEN** the dev reset refuses and names the kept copy and the put-back steps

#### Scenario: Kept copy itself empty
- **WHEN** the kept copy holds zero namespace rows
- **THEN** the dev reset proceeds and replaces the empty kept copy

### Requirement: Dev refusal text

When no reset ran yet, the dev refusal SHALL state that no kept copy exists.

#### Scenario: Refusal before reset
- **WHEN** dev refuses before any reset ran
- **THEN** the dev refusal states that no kept copy exists yet
