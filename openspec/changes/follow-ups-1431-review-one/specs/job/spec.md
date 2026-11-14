## ADDED Requirements

### Requirement: shared-chunk-rule

When a staged item-key check validates a job chunk row, the server SHALL apply the pkg/job.CheckChunkField bounds rule with unchanged sentinel errors.

#### Scenario: Hostile chunk row through postgres
- **WHEN** the postgres staged item-key check receives a chunk row outside the shared bounds
- **THEN** the staged check returns the same sentinel error as before the change.

#### Scenario: Hostile chunk row through sqlite
- **WHEN** the sqlite staged item-key check receives a chunk row outside the shared bounds
- **THEN** the staged check returns the same sentinel error as before the change.
