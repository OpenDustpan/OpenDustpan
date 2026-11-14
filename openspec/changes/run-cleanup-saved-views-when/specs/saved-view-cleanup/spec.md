## ADDED Requirements

### Requirement: Cleanup after principal delete

When a principal is deleted, the server SHALL run savedview.Service.RemoveOwner for the deleted principal ID outside the principal-delete transaction.

#### Scenario: Views of deleted principal removed
- **WHEN** a principal that owns views in two namespaces is deleted and the cleanup operation runs
- **THEN** views owned by the deleted principal are gone in both namespaces and one view:delete audit entry with the view ID and no name exists for each deleted view

#### Scenario: Principal with no views
- **WHEN** a principal that owns zero views is deleted and the cleanup operation runs
- **THEN** the run returns 0 and writes zero audit entries

#### Scenario: Partial failure retries
- **WHEN** the first RemoveOwner run fails on the second namespace
- **THEN** the next run deletes the views of the failed namespace and writes one audit entry per view

#### Scenario: Repeated run is silent
- **WHEN** RemoveOwner runs again for the same deleted principal
- **THEN** the run returns 0 and writes zero audit entries

#### Scenario: Late view is cleaned
- **WHEN** a view for the deleted owner ID is created between delete commit and cleanup run
- **THEN** the next cleanup run deletes that view with one audit entry

#### Scenario: Retries exhausted
- **WHEN** the cleanup run fails 50 times
- **THEN** the record stays failed with one audit entry naming the owner ID

#### Scenario: Restored views of deleted principal cleaned
- **WHEN** a backup restored after principal delete brings back views of the deleted principal
- **THEN** the views match no reader and the next cleanup run deletes them
