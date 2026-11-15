## ADDED Requirements

### Requirement: Production install fixtures

Where production install, the server SHALL start with TLS from certificate files behind a reverse proxy with PostgreSQL, blob store, and scheduled backups on.

#### Scenario: Follow production page
- **WHEN** Operator follows docs page as written with install bits from the linked install-from-artifacts page.
- **THEN** Server serves TLS from files behind the proxy, stores state in PostgreSQL, blob store holds blobs, backup check passes, and pulumi up and tofu apply succeed through the proxy.

### Requirement: Missing TLS files

When a TLS certificate file is missing or its key file is unreadable, the server SHALL refuse to start and report the file path.

#### Scenario: Start without files
- **WHEN** Certificate file is missing or key file is unreadable.
- **THEN** Server refuses to start and reports the missing file path without repeating key material.

### Requirement: Unreachable database

When PostgreSQL is unreachable, the server SHALL refuse state writes and report store unreachable.

#### Scenario: Store unreachable
- **WHEN** PostgreSQL is unreachable.
- **THEN** Server refuses state writes and reports store unreachable.

### Requirement: Proxy headers

When the request lacks X-Forwarded-Proto or X-Forwarded-Host from an allowlisted proxy address, the server SHALL refuse the request.

#### Scenario: Missing headers
- **WHEN** Request lacks X-Forwarded-Proto or X-Forwarded-Host from an allowlisted proxy address.
- **THEN** Server refuses the request and reports the missing header name.

#### Scenario: Forged headers
- **WHEN** Request carries proxy headers from an address outside the NEW allowlisted proxy addresses.
- **THEN** Server ignores the headers and refuses the request with an audit entry.

### Requirement: Expired certificate

When the server certificate is expired, the server SHALL fail the TLS handshake without serving the request.

#### Scenario: Expired certificate
- **WHEN** Server certificate is expired.
- **THEN** TLS handshake fails and the connecting client reports certificate expired.

### Requirement: Backup check

When a scheduled backup is missing or corrupt, the server SHALL report backup check failure on the backup route.

#### Scenario: Failing backup check
- **WHEN** Scheduled backup is missing or corrupt.
- **THEN** Backup check reports failure and names the failing backup.

### Requirement: Blob store

When the blob store is unreachable, the server SHALL refuse blob writes and report blob store unreachable.

#### Scenario: Missing blob store
- **WHEN** Blob store is unreachable.
- **THEN** Server reports blob store unreachable and refuses blob writes.

### Requirement: Secrets handling

When any production install step fails, the server SHALL repeat no secret value or key material in errors or logs.

#### Scenario: Secret redaction
- **WHEN** Any production install step fails.
- **THEN** Error and log output repeat no secret value or key material.

### Requirement: Key permissions

When the TLS key file is readable by group or others, the server SHALL refuse to start.

#### Scenario: Open key file
- **WHEN** TLS key file is readable by group or others.
- **THEN** Server refuses to start and reports the key file path.

### Requirement: Proxy example

Where the reverse proxy section, the server docs SHALL pin one NEW nginx example with required headers, body size limit, and timeouts.

#### Scenario: Pinned proxy config
- **WHEN** Operator reads the reverse proxy section.
- **THEN** Docs page shows the NEW nginx example with required headers, body size limit of 32 megabyte per request, and read timeout of 120 second per state upload.

### Requirement: Certificate files

When the operator replaces certificate files and restarts, the server SHALL load the new files at start.

#### Scenario: Restart with new files
- **WHEN** Operator replaces certificate files and restarts the server.
- **THEN** Server serves the new certificate after restart.

### Requirement: Key file backup

When scheduled backups turn on, the server docs SHALL instruct the operator to back up the NEW key file copy together with the database.

#### Scenario: Back up key file with database
- **WHEN** Operator turns scheduled backups on.
- **THEN** Docs page instructs the operator to copy the NEW key file with each database backup and to follow the restore rule of key-encryption-key-rotation.
