# round-trip-tests Specification

## Purpose
The round-trip tests count the database round trips of a server action. This capability covers the round-trip test of a backup and when it runs. It does not cover the round-trip tests of other actions or the limits that they check.

## Requirements

### Requirement: The backup round-trip test skips without PostgreSQL

The test of the database round trips of a backup SHALL skip, and not fail, when `DUSTPAN_TEST_POSTGRES_URL` is not set.

#### Scenario: No database
- **WHEN** a contributor runs the test with `DUSTPAN_TEST_POSTGRES_URL` unset
- **THEN** the test reports SKIP with the name of the variable

#### Scenario: A database
- **WHEN** the variable names a PostgreSQL database
- **THEN** the test runs each size and checks the counts as before
