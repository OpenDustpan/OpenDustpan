## ADDED Requirements

### Requirement: Single-statement unit page

When a caller calls QueryUnits for one page of units, the server SHALL read the labels with array_agg in the page query.

#### Scenario: Unit with no labels
- **WHEN** QueryUnits reads a unit with no labels
- **THEN** the page returns an empty label set from one statement

#### Scenario: Label order
- **WHEN** QueryUnits reads a unit with labels
- **THEN** the page returns labels ordered by label key

### Requirement: Mapped snapshot conflicts

When a state-at read hits 40P01 or 40001, the server SHALL map the error to unavailable.

#### Scenario: Conflict on begin
- **WHEN** StateAt begin hits 40P01
- **THEN** the call answers ErrUnavailable with 503 and Retry-After

#### Scenario: Conflict on workspace read
- **WHEN** a WorkspaceStateAt read hits 40001
- **THEN** the call answers ErrUnavailable with 503 and Retry-After

#### Scenario: Conflict on read after begin
- **WHEN** a StateAt select after begin hits 40001
- **THEN** the call answers ErrUnavailable with 503 and Retry-After
