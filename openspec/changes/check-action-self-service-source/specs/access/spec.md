## ADDED Requirements

### Requirement: reach-refusal-without-count

When the reach check refuses a token request, the server SHALL omit the count of unreadable objects from the refusal text.

#### Scenario: Token reach refusal
- **WHEN** principalReachRefusal runs after a denied token reach check
- **THEN** the refusal names the denied decision and the access-review help, with no count of unreadable objects, while the audit record keeps the count
