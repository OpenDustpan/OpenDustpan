## ADDED Requirements

### Requirement: The connection document is validated at the boundary
The server SHALL accept a model connection document only when it is valid JSON of at most 32 KiB, has `version` 1, has no unknown field, and has no control character in a string. The server SHALL refuse the format `responses` with the NEW error `ErrFormatNotSupported`.

#### Scenario: A document with an unknown field
- **WHEN** a document has a field that the table does not list
- **THEN** the parse fails and no document is stored

#### Scenario: A document above the size limit
- **WHEN** a document has more than 32 KiB
- **THEN** the parse fails before the JSON decoder reads it

#### Scenario: The format responses
- **WHEN** a document has `format` `responses`
- **THEN** the parse fails with `ErrFormatNotSupported`

### Requirement: The connection has no default
The server SHALL NOT provide a default model, provider, base URL or credential.

#### Scenario: A new installation
- **WHEN** no operator wrote a connection
- **THEN** `GET /api/agent/connection` answers 404

### Requirement: A credential is a secret item
The server SHALL keep each credential as a sealed secret item of the scope `@server`. The document SHALL hold a reference only. No route SHALL answer a value.

#### Scenario: A plain value in a header
- **WHEN** a header with a secret-like name holds a plain value
- **THEN** the parse fails with `ErrPlainSecret` and the error names the route that stores a secret

#### Scenario: A list of secret items
- **WHEN** an administrator calls `GET /api/agent/secrets`
- **THEN** each entry has a reference, a name and a time, and no value

### Requirement: A used secret item cannot be deleted
The server SHALL answer 409 to `DELETE /api/agent/secrets/{ref}` while a stored document refers to the item.

#### Scenario: Delete while referenced
- **WHEN** the current or a pending document refers to the item
- **THEN** the answer is 409 and the item stays

### Requirement: The base URL is checked where it is used
The server SHALL check each model URL with `egress.CheckModelURL` against the always-denied list and the allowlist. A rule of the allowlist SHALL NOT allow an always-denied range.

#### Scenario: A metadata address
- **WHEN** a URL resolves to a link-local address
- **THEN** the check fails and the audit log has an entry

### Requirement: A disconnect removes everything in one transaction
`DELETE /api/agent/connection` SHALL remove the current document, each pending document and the reference rows in one transaction.

#### Scenario: Disconnect with a pending document
- **WHEN** a pending document exists and an administrator disconnects
- **THEN** no document and no reference row remains

### Requirement: Each route checks its action
Each route under the NEW path prefix `/api/agent/` SHALL check the action `server:manage-agent` on `@server`. The allowlist routes SHALL check `server:manage-settings`. A denial SHALL write an audit entry.

#### Scenario: A principal without the grant
- **WHEN** a principal without the grant calls any of the routes
- **THEN** the answer is 403 and the audit log has an entry
