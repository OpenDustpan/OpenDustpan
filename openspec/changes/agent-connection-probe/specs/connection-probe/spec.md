## ADDED Requirements

### Requirement: The probe is a durable operation
The server SHALL run a probe as a durable operation of the kind `agent.probe`. No route SHALL wait on a model call.

#### Scenario: Start a probe with no store
- **WHEN** an administrator sends `POST /api/agent/connection/probe` with a valid document
- **THEN** the answer is 202 with a probe ID, and the document is not stored as the connection

### Requirement: The checks run in order and stop at the first blocking failure
The probe SHALL run `reach`, `auth`, `tools`, `tool-result`, `transport`, `usage`, `reasoning` and `cache` in this order. A failure of `reach`, `auth`, `tools`, `tool-result` or `transport` SHALL stop the probe. A failure of `usage`, `reasoning` or `cache` SHALL be a warning.

#### Scenario: A model with no tool call
- **WHEN** the model gives no tool call in 2 attempts
- **THEN** the check `tools` fails and the later checks are `skipped`

#### Scenario: A base URL that resolves to a metadata address
- **WHEN** the `baseURL` of the document resolves to a link-local address
- **THEN** the check `reach` fails, no call goes out, and the audit log has an entry

#### Scenario: A server with no cache tokens
- **WHEN** the mode is `automatic` and the server reports no cache tokens
- **THEN** the check `cache` is a warning and the probe passes

### Requirement: A failure message never holds the server text
The result of a check SHALL hold the name of the check, the status class, the sentinel and a fixed message that names the field to look at. The result, the log and the audit entry SHALL NOT hold the error text of the server.

#### Scenario: The server answers a secret in its error body
- **WHEN** the error body of the server holds a canary secret
- **THEN** the canary is not in the result, the log or the audit entry

### Requirement: The probe has a known cost
Each probe call SHALL send the lower of `outputTokens.max` and 1,024 as the output token limit. A probe SHALL make at most 7 calls. Each call SHALL write one ledger row with the origin `probe`, keyed by probe ID, check name and attempt number.

#### Scenario: A repeat of a step
- **WHEN** a step runs a second time with the same key
- **THEN** no second ledger row exists

### Requirement: The installation limits probe starts
The server SHALL accept at most 6 probe starts in each minute for the installation. A start above the limit SHALL get 429 with `retry-after`.

#### Scenario: A seventh start
- **WHEN** 6 probes started in the last minute
- **THEN** the next start gets 429 with `retry-after`

### Requirement: A probe promotes only a current document
When a probe of a pending document passes, its last step SHALL make the document the connection in one transaction, and only when the `If-Match` version is still the version of the connection. Otherwise the probe SHALL end `failed` and remove its pending document.

#### Scenario: A changed connection
- **WHEN** the connection version changed after the start
- **THEN** the probe ends `failed` and its error tells the operator to read the connection and send the `PUT` again

### Requirement: A later start or a disconnect cancels earlier probes
A later start with a pending document SHALL cancel each earlier probe that has a pending document, in its own transaction. A disconnect SHALL cancel them in one transaction. A canceled probe SHALL end `canceled` and change nothing.

#### Scenario: Two starts
- **WHEN** a second `PUT` starts a probe while the first runs
- **THEN** the first probe ends `canceled` at its next step
