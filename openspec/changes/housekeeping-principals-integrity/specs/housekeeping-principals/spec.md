## ADDED Requirements

### Requirement: The housekeeping principals are the ones that the code defines

A restore SHALL refuse a backup that holds a principal row with an ID that starts with `prn-housekeeping-`, or with the source `builtin`, that is not exactly the built-in row of one housekeeping purpose, and SHALL refuse a backup that lacks the row of a purpose. The refusal SHALL return `backup.ErrCorrupt` and change nothing.

#### Scenario: A person takes a housekeeping ID
- **WHEN** a backup holds `prn-housekeeping-history-retention` with the kind `person` and the source `oidc`
- **THEN** the restore is refused as corrupt and the installation is unchanged

#### Scenario: A built-in source with an unknown ID
- **WHEN** a backup holds a row with the source `builtin` and an ID that is not a housekeeping purpose
- **THEN** the restore is refused as corrupt

### Requirement: A namespace creation fails closed on a changed principal

The creation of a namespace SHALL read the principal row of each housekeeping grant in the same transaction and SHALL create no namespace and no grant when a row is missing or is not the built-in row of its purpose. The server SHALL write one audit entry with the outcome `denied` that names the principal.

#### Scenario: A row changed in the database
- **WHEN** the row `prn-housekeeping-history-retention` has the kind `person` and an operator creates a namespace
- **THEN** no namespace exists, no grant of the creation exists, and the audit log has a denied entry with that ID

### Requirement: A built-in principal cannot be made or deleted

The server SHALL refuse a subject that starts with `housekeeping:` in the creation of a principal, and SHALL refuse the delete of a principal with the source `builtin`, with the error `reserved subject`. A revoke of a grant of a housekeeping principal SHALL stay possible.

#### Scenario: Delete a built-in principal
- **WHEN** a person with `server:manage-principals` deletes `housekeeping:token-sweep`
- **THEN** the server answers `reserved subject` and the principal and its grants remain
