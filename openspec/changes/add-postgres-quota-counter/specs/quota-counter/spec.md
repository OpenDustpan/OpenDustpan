## ADDED Requirements

### Requirement: postgres-quota-counter

When the server runs on PostgreSQL, the quota counter SHALL pass the same conformance suite as the SQLite counter, and SHALL refuse a reservation key that no check covers.

#### Scenario: the suite on PostgreSQL
- **WHEN** the quota conformance suite runs against the PostgreSQL store
- **THEN** every case passes, the case of a reservation key without a check included
