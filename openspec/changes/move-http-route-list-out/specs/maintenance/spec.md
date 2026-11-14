## ADDED Requirements

### Requirement: Reducing table matches gate and control path

When the maintenance gate evaluates a request, the server SHALL treat the request as reducing only where the gate predicate and the internal table agree.

#### Scenario: Server entry without control-path method
- **WHEN** the table lists an installation-API route with no mapped store control-path method
- **THEN** the two-way test fails

#### Scenario: Control-path method without table entry
- **WHEN** a mapped store control-path method has no matching table entry
- **THEN** the two-way test fails

#### Scenario: Gate and table disagree
- **WHEN** the gate predicate marks a route reducing with no table entry
- **THEN** the two-way test fails

### Requirement: Reducing table rejects malformed entries

When the server builds the internal reducing-route table, the server SHALL reject an entry with an invalid method or path shape.

#### Scenario: Bad method entry
- **WHEN** a table entry names a method outside GET, POST, PUT, PATCH, DELETE
- **THEN** the constructor rejects the entry

#### Scenario: Bad path entry
- **WHEN** a table entry has no /api/ prefix, has wrong case, has a query string, or repeats a method and path pair
- **THEN** the constructor rejects the entry

#### Scenario: Empty table
- **WHEN** the constructor gets no entry
- **THEN** the constructor rejects the table
