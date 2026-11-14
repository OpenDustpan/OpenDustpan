## ADDED Requirements

### Requirement: Count skips

When the server skips a poison job start, the server SHALL add one to the skip counter.

#### Scenario: One skip
- **WHEN** a poison job start is skipped
- **THEN** the metrics output shows the counter raised by one

### Requirement: Alert at threshold

When the skip counter reaches 10, the server SHALL send one operator alert.

#### Scenario: Tenth skip
- **WHEN** the tenth skip is counted
- **THEN** one alert is sent and a record names the cause
