## ADDED Requirements

### Requirement: One principal for each housekeeping purpose

The server SHALL make one built-in service principal for each housekeeping purpose at the install, and SHALL write its grants as stored grants with the install as the creator and the install time.

#### Scenario: Listed after the install
- **WHEN** an operator lists the principals of a new installation
- **THEN** each housekeeping purpose has one principal of kind `service` and source `builtin`, and each has stored grants with a creator and a time

### Requirement: The principal list filters by source

The principal list SHALL keep only the principals of the source in the `source` query parameter, and SHALL count only those rows in its total and its cursor.

#### Scenario: Built-in principals
- **WHEN** an operator lists the principals with `kind=service&source=builtin`
- **THEN** the answer holds only the built-in principals, and the total counts only them

#### Scenario: Source that is not valid
- **WHEN** an operator lists the principals with a `source` that is not valid
- **THEN** the server answers 400

### Requirement: A restore keeps the housekeeping principals of the backup

A portable restore SHALL replace the housekeeping principals and grants of the new baseline with the principals and grants of the backup, so that a grant that was revoked before the backup is still revoked after the restore.

#### Scenario: Restore into a new installation
- **WHEN** an operator restores a portable backup into a new installation
- **THEN** each housekeeping purpose has one principal and the grants that the backup holds, and no row is twice in the installation

#### Scenario: Revoked grant
- **WHEN** an operator restores a backup in which a grant of a housekeeping principal was revoked
- **THEN** the grant is revoked after the restore
