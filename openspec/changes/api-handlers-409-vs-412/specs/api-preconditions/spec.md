## ADDED Requirements

### Requirement: Write approval rule with If-Match

When a caller calls Write approval rule with If-Match with a stale version, the server SHALL answer 412.

#### Scenario: Stale rule write
- **WHEN** a caller calls Write approval rule with If-Match with a stale version
- **THEN** the server answers 412 with code approval.precondition-failed and leaves the rule unchanged

#### Scenario: Stale rule delete
- **WHEN** a caller sends DELETE to an approval rule with a stale If-Match
- **THEN** the server answers 412 and leaves the rule unchanged

#### Scenario: Matching write
- **WHEN** a caller sends PUT to an approval rule with the current If-Match
- **THEN** the server commits the next version and answers 200

### Requirement: Create conflict stays 409

If a create conflicts with an existing approval rule and the request carries no If-Match, then the server SHALL answer 409.

#### Scenario: Create when exists
- **WHEN** a caller creates an approval rule that exists with no If-Match
- **THEN** the server answers 409 and changes nothing

### Requirement: Unexpected header replay

When a named collection, lock, approval, preferences or context mutating route receives an Idempotency-Key, the server SHALL store and replay its answer by caller, route and key.

#### Scenario: Retry lock write
- **WHEN** a caller retries PUT of a collection lock with the same caller, route and key
- **THEN** the server replays the stored answer and applies the change once

#### Scenario: Retry preferences write
- **WHEN** a caller retries PATCH of preferences with the same caller, route and key
- **THEN** the server replays the stored answer and applies the change once
