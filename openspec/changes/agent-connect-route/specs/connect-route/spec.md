## ADDED Requirements

### Requirement: The connect route stores a pending document and starts a probe
`PUT /api/agent/connection` SHALL check the document, store it as a pending document with the ID of a new probe as its key, start that probe, and answer 202 with the probe ID. It SHALL NOT change the connection.

#### Scenario: A valid first connect
- **WHEN** an administrator sends a valid document with `If-Match: "0"` and no connection exists
- **THEN** the answer is 202 with a probe ID and `GET /api/agent/connection` shows the pending document

### Requirement: The route checks the version
The route SHALL answer 428 when `If-Match` is missing, 400 when it is not a quoted integer, and 412 when the version is not the current version of the connection. The promote step SHALL check the version again in its own transaction.

#### Scenario: A stale version
- **WHEN** the connection version changed between the `PUT` and the end of the probe
- **THEN** the probe ends `failed`, nothing is written, and the error tells the operator to read the connection and send the `PUT` again

### Requirement: A later PUT cancels earlier probes
A `PUT` SHALL cancel each earlier probe that has a pending document and remove those documents, in the same transaction as its own write.

#### Scenario: Two PUT requests
- **WHEN** a second `PUT` arrives while the first probe runs
- **THEN** the first probe ends `canceled` and changes nothing

### Requirement: A passed probe makes the document the connection
When the probe passes, its last step SHALL call `agent.ConnectModel`, which SHALL make the pending document the connection and write the records of Dusty in one transaction, and write one audit entry that holds the old and new document with references only.

#### Scenario: A passed probe
- **WHEN** a probe of a pending document passes with a current version
- **THEN** the document is the connection, Dusty exists as an agent principal, and the audit log has one entry of the kind `change`

### Requirement: The route refuses a secret value in the document
The route SHALL refuse a document where a field holds the value of a secret item, and the error SHALL name the field.

#### Scenario: A value in a header field
- **WHEN** a header field holds the value of a secret item
- **THEN** the answer is 400 and names the field
