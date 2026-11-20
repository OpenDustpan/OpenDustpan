## ADDED Requirements

### Requirement: Intake route

The server SHALL take a delivery at POST /webhooks/{hook ID}, outside the versioned API, with no sign-in. The server SHALL read at most 2 MiB of the body and SHALL answer 204 with an empty body, and the same headers, for every request to the route: an accepted delivery and each kind of refused delivery.

#### Scenario: Valid pull request delivery
- **WHEN** GitHub posts a pull request event of an assigned installation, signed with the current event secret
- **THEN** the server records one hint and answers 204 with an empty body

#### Scenario: Ping
- **WHEN** GitHub posts a ping signed with the current event secret
- **THEN** the server records one hint and answers 204

#### Scenario: Bad signature
- **WHEN** a caller posts a body with a signature that does not match any active event secret
- **THEN** the server records no hint, counts a reject with the reason signature, and answers 204 with the same bytes as for a valid delivery

#### Scenario: Unknown hook ID
- **WHEN** a caller posts to a hook ID that the registration does not hold
- **THEN** the server counts a reject with the reason unknown-hook and answers 204 with the same bytes

#### Scenario: Oversize body
- **WHEN** a caller posts a body above 2 MiB
- **THEN** the server stops the read, counts a reject with the reason too-large and answers 204

#### Scenario: Event outside the port
- **WHEN** GitHub posts a signed event of a kind that the port does not name, for example push
- **THEN** the server records no hint, counts a reject with the reason unsupported and answers 204

### Requirement: Signature check

The GitHub adapter SHALL accept an event only when the header X-Hub-Signature-256 holds `sha256=` and the 64 hex digits of the HMAC-SHA256 of the body under one of the given secrets, compared in constant time. The adapter SHALL ignore a secret of fewer than 32 bytes, SHALL NOT read the header X-Hub-Signature, and SHALL read the event name and the delivery ID only after a match.

#### Scenario: Changed body
- **WHEN** one byte of a signed body changes
- **THEN** ParseEvent returns ErrSignature

#### Scenario: Short or empty secrets only
- **WHEN** the list holds no secret of 32 bytes or more
- **THEN** ParseEvent returns ErrSignature for every request

### Requirement: Rejects are counted and recorded

For each refused delivery the server SHALL count dustpan_git_host_event_rejected_total with one reason label from a fixed set, and SHALL write an audit entry through the denial limiter. The entry SHALL hold the reason class only: no header, body, hook ID or signature.

#### Scenario: Burst of bad signatures
- **WHEN** a caller posts 10000 bad signatures in one minute
- **THEN** the metric counts 10000, the audit log holds one entry for the reason in that minute, and the count of the others goes out with the next entry

#### Scenario: Audit store fails
- **WHEN** the audit store refuses the entry of a reject
- **THEN** the answer stays 204 and the failure counts on the audit failure metric

### Requirement: Old event secret window

During a rotation the server SHALL keep at most two event secrets. The server SHALL check an event against the old secret only after the check against the current secret fails, only before the end of the window, and only when the registration holds the whole old set. The window ends 7 days after the rotation, or at the confirmation of the rotation.

#### Scenario: Rotation by a new event secret
- **WHEN** the operator puts a registration with a different event secret and the same API address
- **THEN** the registration holds the new secret as current, the former current secret as old, and an end of the window 7 days later, written in one step

#### Scenario: Old secret in the window
- **WHEN** an event is signed with the old secret before the end of the window
- **THEN** the server accepts the event and writes an audit entry of the use of the old secret in the same transaction as the hint

#### Scenario: Old secret after the end
- **WHEN** an event is signed with the old secret at the end of the window or later
- **THEN** the server refuses the event, counts a reject, and writes an audit entry of the reject

#### Scenario: Old secret after confirmation
- **WHEN** the operator confirms the rotation and an event is signed with the old secret
- **THEN** the server refuses the event and writes an audit entry of the reject

#### Scenario: Third secret
- **WHEN** the operator puts a third event secret during an open window and an event is signed with the first secret
- **THEN** the server refuses the event, because the registration keeps two secrets

#### Scenario: Partial old set
- **WHEN** a stored registration holds only some of the three old fields
- **THEN** the store refuses to write it, and a read of such a row fails closed

#### Scenario: Window shown
- **WHEN** a caller reads the registration while a window is open
- **THEN** the answer holds oldSecretEndsAt as Unix milliseconds, and no secret

#### Scenario: Confirm with no window
- **WHEN** the operator posts the confirmation and no window is open
- **THEN** the server answers 409 and writes no change

### Requirement: Replay

The server SHALL drop a verified delivery whose delivery ID, or whose body digest, it recorded in the last 24 hours, and SHALL record a new delivery once. The record and the check SHALL be one atomic step.

#### Scenario: Redelivery by GitHub
- **WHEN** GitHub sends the same delivery ID again
- **THEN** the server records no second hint and counts a reject with the reason duplicate

#### Scenario: Replay with a new delivery ID
- **WHEN** a caller sends a signed body again with a new X-GitHub-Delivery value
- **THEN** the server records no second hint

#### Scenario: Receipts at the bound
- **WHEN** the table of receipts holds 100000 receipts of the last 24 hours
- **THEN** the server drops the delivery, counts the reason receipts-full and answers 204

### Requirement: Installation map and replica drop

The server SHALL record the hint of a pull request event only for an installation that the map assigns to a namespace, and only while this installation is the primary installation of that namespace.

#### Scenario: Unassigned installation
- **WHEN** a signed pull request event names an installation that the map does not hold
- **THEN** the server records no hint and counts the reason not-assigned

#### Scenario: Non-primary member
- **WHEN** a member receives a signed event for a namespace of another primary installation
- **THEN** the member answers 204, records no hint and counts the reason not-primary
