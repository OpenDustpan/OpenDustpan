## ADDED Requirements

### Requirement: compat-cases

The harness SHALL hold conformance bodies for unknown enum values, merge-patch null, and absent fields.

#### Scenario: unknown-enum-value-is-kept
- **WHEN** the harness sends a value outside the fixed set
- **THEN** the adapter keeps the unknown value and the case passes

#### Scenario: merge-patch-null-clears-a-field
- **WHEN** the harness clears a field with merge patch
- **THEN** the adapter sends null for the cleared field and the case passes

#### Scenario: absent-field-is-not-sent
- **WHEN** the harness leaves an optional field absent
- **THEN** the adapter omits the field from the wire body

### Requirement: error-cases

The harness SHALL hold conformance bodies for typed error arguments and encoded query parameters.

#### Scenario: error-args-keep-their-json-types
- **WHEN** the server returns error arguments
- **THEN** the harness keeps each argument in its JSON type and the case passes

#### Scenario: query-parameter-is-encoded
- **WHEN** the harness calls an operation with a query parameter
- **THEN** the adapter encodes the parameter and the case passes

### Requirement: paginate-bound

While paging, the adapter SHALL stop after MaxPages pages.

#### Scenario: paginate-stops-at-max-pages
- **WHEN** the server keeps returning new cursors
- **THEN** the adapter stops after MaxPages pages and reports truncation
