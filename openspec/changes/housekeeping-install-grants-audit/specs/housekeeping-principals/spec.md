## ADDED Requirements

### Requirement: The install records its grants

The server SHALL write the stored grants of the housekeeping principals on `@server` at the install of an installation, once, in one transaction, with the server as the creator and the install time, and SHALL append one audit entry for each grant in the audit chain of `@server` in that transaction.

#### Scenario: Grants and entries after the install
- **WHEN** an operator starts a new installation and reads the access review and the audit log
- **THEN** each grant of a housekeeping principal on `@server` has the server as its creator and the install time (not 0), and the audit log has one entry for each of these grants

#### Scenario: A second start writes nothing
- **WHEN** an operator restarts the server of an installed installation
- **THEN** no second grant and no second audit entry exist

#### Scenario: A revoke stays revoked
- **WHEN** an operator revokes one of these grants and restarts the server
- **THEN** the grant is still revoked and the install does not write it again

#### Scenario: The install fails closed
- **WHEN** the store cannot write the grants or an audit entry
- **THEN** the transaction rolls back, no grant exists, and the server does not start

### Requirement: A restore keeps one set of install grants

A portable restore SHALL load the grants of the backup in place of the live grants, so that the restored installation holds each grant once, with the creator, the time and the revoke of the backup, and the install SHALL write no grant after the restore.

#### Scenario: Restore into an installed installation
- **WHEN** an operator restores a backup into an installation that already holds the install grants
- **THEN** each housekeeping grant on `@server` exists once, with the creator and time of the backup, and a grant that was revoked before the export is revoked
