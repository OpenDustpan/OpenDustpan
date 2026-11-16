## ADDED Requirements

### Requirement: no change order

When a local terragrunt run-all apply completes, the server SHALL hold no change order for that run.

#### Scenario: local run makes no order
- **WHEN** a local terragrunt run-all apply over 3 units completes
- **THEN** each of the 3 workspaces holds its own new state version and 1 history entry and the run records no multi-unit object
