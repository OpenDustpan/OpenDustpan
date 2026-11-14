## ADDED Requirements

### Requirement: Refuse old backup

If a backup contains a status column, then the server SHALL refuse the restore.

#### Scenario: Restore backup with status column
- **WHEN** a backup contains a status column
- **THEN** the server refuses the restore with ErrSchemaDiverged and writes no rows
