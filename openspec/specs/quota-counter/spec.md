# quota-counter Specification

## Purpose
The quota counter limits how much of a quota key a principal may reserve, record and settle. This capability covers the PostgreSQL counter and its match with the SQLite counter. It does not cover the quota limits, the checks that name each key, or the quota routes.

## Requirements

### Requirement: postgres-quota-counter

When the server runs on PostgreSQL, the quota counter SHALL pass the same conformance suite as the SQLite counter, and SHALL refuse a reservation key that no check covers.

#### Scenario: the suite on PostgreSQL
- **WHEN** the quota conformance suite runs against the PostgreSQL store
- **THEN** every case passes, the case of a reservation key without a check included
