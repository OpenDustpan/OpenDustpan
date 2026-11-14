## ADDED Requirements

### Requirement: Refuse streamed override without recorder

When a streamed override arrives without a recorder, the server SHALL refuse it in WriteStreamed before any placeholder or attempt write.

#### Scenario: Missing recorder observes nothing
- **WHEN** a streamed override arrives without a recorder
- **THEN** WriteStreamed refuses the override with zero placeholder declarations and zero attempt writes

#### Scenario: Early guard mutation
- **WHEN** only the early recorder guard is removed from WriteStreamed
- **THEN** the missing-recorder test fails
