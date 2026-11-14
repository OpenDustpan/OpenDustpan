## ADDED Requirements

### Requirement: Export namespace backup

When a principal with the export grant calls the NEW export route, the server SHALL stream one portable-format archive of that namespace.

#### Scenario: Export own namespace
- **WHEN** A principal with the NEW action namespace-backup:export on the namespace ID calls the NEW export route.
- **THEN** The server streams one portable-format archive with only rows of that namespace plus table-list shared rows, with sealed values unchanged.

#### Scenario: Hidden namespace stays hidden
- **WHEN** A principal calls the NEW export route for an unknown or hidden namespace name.
- **THEN** The server answers 404 with the NEW error code namespace-backup.not-found.

#### Scenario: Missing credential
- **WHEN** A call to the NEW export route has no or an invalid credential.
- **THEN** The server answers 401 with the NEW error code namespace-backup.unauthenticated.

#### Scenario: Refuse without grant
- **WHEN** A principal without the NEW action namespace-backup:export calls the NEW export route for a visible namespace.
- **THEN** The server denies the call with 403 and the NEW error code namespace-backup.forbidden.

#### Scenario: Consistent snapshot
- **WHEN** Writes occur during a namespace export.
- **THEN** The archive reflects one read-only snapshot across all namespace tables despite concurrent writes.

### Requirement: Audit namespace export

When a namespace export completes or is refused, the server SHALL write one audit entry for it.

#### Scenario: Record export allow
- **WHEN** A namespace export completes.
- **THEN** The audit log holds one entry with the namespace ID, the principal, and the archive byte count.

#### Scenario: Record export deny or abort
- **WHEN** A namespace export is refused, exceeds the limit, or aborts mid-stream.
- **THEN** The audit log holds one entry with the namespace ID, the principal, and the reason, with no byte count, and an audit write failure fails the export closed.

### Requirement: Limit namespace export

When a namespace export passes 512 MiB of output, the server SHALL stop the export.

#### Scenario: Stop oversize export before body
- **WHEN** A namespace export pre-count passes 512 MiB of output.
- **THEN** The server answers 413 with the NEW error code namespace-backup.too-large before any body byte.
