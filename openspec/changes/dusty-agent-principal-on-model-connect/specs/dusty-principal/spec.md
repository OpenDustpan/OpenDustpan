## ADDED Requirements

### Requirement: dusty-is-an-agent-principal

The server SHALL make one agent principal with the subject `dusty` in one transaction of the store port, with the connecting administrator as its creator and first sponsor.

#### Scenario: first connect makes Dusty
- **WHEN** an administrator with `server:manage-agent` connects a model and no record with the subject `dusty` exists
- **THEN** the server stores the connection, one principal of the kind `agent` with the subject `dusty`, one sponsor that is the administrator, the creator that is the administrator, and one audit entry for each write

#### Scenario: second connect makes no second record
- **WHEN** the administrator connects a model again
- **THEN** the server changes the connection only, and the record, the sponsors and the grants stay as they were

#### Scenario: failure rolls back
- **WHEN** one write of ConnectModel fails
- **THEN** the server stores none of the writes of ConnectModel, because the store port writes them in one transaction

### Requirement: agent-record-sponsors

The server SHALL store 1 to 5 sponsors for each agent record, each a person that is not disabled, and SHALL refuse any other list.

#### Scenario: sponsors refused
- **WHEN** a request gives no sponsor, six sponsors, a repeated sponsor, a service principal or a disabled person
- **THEN** the server answers 400 with the NEW error code `agent.sponsor-invalid` and stores no row

### Requirement: dusty-ceiling-grants

The server SHALL store the ceiling grants of Dusty at connect: the role `agent-tools-read` on the installation and the role `agent-tools` on each namespace that the administrator lists, each with the administrator as creator.

#### Scenario: no namespace listed
- **WHEN** the administrator lists no namespace
- **THEN** the server stores the grant on the installation only

### Requirement: agent-has-no-sign-in

The server SHALL refuse a password, a passkey, an MFA factor, a console session, a sign-in method, a group membership, a protected role and the break-glass mark for a principal of the kind `agent`.

#### Scenario: group membership refused
- **WHEN** a request adds Dusty to a group
- **THEN** the server refuses it and changes no row

### Requirement: agent-created-by-connect-only

The server SHALL make an agent record only through the model connect request.

#### Scenario: create route refuses
- **WHEN** a caller sends `POST /api/server/principals` with the kind `agent`
- **THEN** the server answers 400 `principal.invalid` and stores no row

### Requirement: restore-keeps-agent-record-with-principal

The server SHALL back up and restore the agent record and the sponsors together with the principal of the kind `agent`, only in a restore that replaces access, and SHALL refuse a backup in which the three do not match.

#### Scenario: restore replaces access
- **WHEN** a caller with server:restore-access restores a backup that has an agent principal
- **THEN** the agent principal, its record and its sponsors come from the backup, and no record or sponsor stays without its principal

#### Scenario: restore without the access action
- **WHEN** a caller without server:restore-access restores a backup
- **THEN** the current agent principal, record and sponsors stay unchanged

#### Scenario: backup that does not match
- **WHEN** a backup has an agent record without its principal, an agent principal without its record, or a record with 0 or 6 sponsors
- **THEN** the restore is refused and changes no row

