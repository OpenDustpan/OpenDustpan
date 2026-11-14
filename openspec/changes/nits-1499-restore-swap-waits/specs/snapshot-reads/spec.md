## ADDED Requirements

### Requirement: QueryUnits single-statement unit page

When a caller calls QueryUnits for one page of units, the server SHALL read the labels with array_agg in the page query.

#### Scenario: QueryUnits unit with no labels
- **WHEN** QueryUnits reads a unit with no labels
- **THEN** the page returns an empty label set from one statement

#### Scenario: QueryUnits label order
- **WHEN** QueryUnits reads a unit with labels
- **THEN** the page returns labels ordered by label key

### Requirement: StateAt mapped snapshot conflicts

When a state-at read hits 40P01 or 40001, the server SHALL map the error to unavailable.

#### Scenario: StateAt conflict on begin
- **WHEN** StateAt begin hits 40P01
- **THEN** the call answers ErrUnavailable with 503 and Retry-After

#### Scenario: WorkspaceStateAt conflict on workspace read
- **WHEN** a WorkspaceStateAt read hits 40001
- **THEN** the call answers ErrUnavailable with 503 and Retry-After

#### Scenario: StateAt conflict on read after begin
- **WHEN** a StateAt select after begin hits 40001
- **THEN** the call answers ErrUnavailable with 503 and Retry-After
