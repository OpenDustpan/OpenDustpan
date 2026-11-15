## ADDED Requirements

### Requirement: List

When a caller with namespace:manage-sweeps lists findings, the server SHALL answer only findings of that namespace in pages of at most 100.

#### Scenario: Foreign namespace
- **WHEN** the namespace ID is unknown
- **THEN** the server answers 404

### Requirement: Triage

When a caller with namespace:manage-sweeps calls SetTriage with a reason, the server SHALL move the finding forward only.

#### Scenario: Backward move
- **WHEN** SetTriage sets a resolved finding to open
- **THEN** the server answers 409 and keeps the triage
