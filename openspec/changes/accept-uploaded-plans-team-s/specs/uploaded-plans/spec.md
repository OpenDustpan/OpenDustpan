## ADDED Requirements

### Requirement: Upload a plan

When a caller with the plan grant uploads both plan parts for one unit, the server SHALL store them as one unit plan of a new change order marked as uploaded plan.

#### Scenario: Upload makes a marked change order
- **WHEN** a caller with the plan grant uploads both parts
- **THEN** the server stores both parts and marks the change order

### Requirement: Bind apply to hash

When an apply opens its apply claim through the local wrapper with the launcher token for a unit with an approved uploaded plan, the server SHALL refuse to open the claim unless the presented hash equals the approved plan hash (ADR 378 decision 3).

#### Scenario: Different hash is refused
- **WHEN** the apply claim presents another hash
- **THEN** the server refuses to open the claim and names the change order

#### Scenario: Same hash passes
- **WHEN** the apply claim presents the approved hash
- **THEN** the claim opens

### Requirement: Stale uploaded plan

When a state write to the unit happens after the upload, the server SHALL treat the uploaded plan as stale and fail the apply with 409 (ADR 378 decision 2).

#### Scenario: Stale plan is refused
- **WHEN** a state write follows the upload and the apply opens its claim
- **THEN** the server fails with 409 and tells the person to upload the current plan

### Requirement: No self-approval and no destroy upload

The server SHALL refuse the approval by the uploader and SHALL refuse an uploaded destroy plan (ADR 378 decision 4).

#### Scenario: Uploader approves
- **WHEN** the person who uploaded the plan approves it
- **THEN** the server refuses the approval
