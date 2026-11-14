## ADDED Requirements

### Requirement: embedded-required-tag

When a dustpan required tag is promoted through an embedded pointer, the server SHALL raise a field error instead of dropping the tag.

#### Scenario: dropped tag errors
- **WHEN** a dustpan required tag is promoted through an embedded pointer
- **THEN** the load fails with a field error

### Requirement: single-helper

Where a test needs kinds keyed by action, the server SHALL keep one shared test helper for kindsOf.

#### Scenario: one helper
- **WHEN** a test needs kinds keyed by action
- **THEN** both test packages call the shared helper
