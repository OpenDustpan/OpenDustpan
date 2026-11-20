# job-claim Specification

## Purpose
Define how the durable claimer reports the operations that one claim skips past its bound. The skip count goes to an observer, and the server shows it as a counter on the metrics route.

## Requirements

### Requirement: bound-count

When operations exceed the poll bound of 64, the server SHALL count each skipped operation in the poll-bound skip metric.

#### Scenario: Poll bound reached
- **WHEN** operations exceed the poll bound of 64
- **THEN** each skipped operation is counted in the poll-bound skip metric and logged with its job reference
