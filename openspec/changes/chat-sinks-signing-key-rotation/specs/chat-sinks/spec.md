## ADDED Requirements

### Requirement: Rotate signing key

When a caller with namespace:manage-notifications sends the NEW rotate route for a webhook sink with fewer than two active refs, the server SHALL seal a server-made secret under a new ref and keep two active signing secrets.

#### Scenario: RotateSigningKey keeps two keys
- **WHEN** a caller sends the NEW rotate route with If-Match
- **THEN** the sink has two active refs and the answer holds the new secret once

#### Scenario: RotateSigningKey third rotate refused
- **WHEN** a caller sends the NEW rotate route while two refs are active
- **THEN** the server refuses with sink.conflict and changes nothing

#### Scenario: RotateSigningKey stale version
- **WHEN** a caller sends the NEW rotate route with a stale If-Match
- **THEN** the server refuses with 412

#### Scenario: RotateSigningKey replay
- **WHEN** a caller repeats the NEW rotate route with the same Idempotency-Key
- **THEN** the server answers the same ref id without the secret

#### Scenario: RotateSigningKey non-webhook
- **WHEN** a caller sends the NEW rotate route for a chat sink
- **THEN** the server refuses with sink.invalid

#### Scenario: RotateSigningKey unknown sink
- **WHEN** a caller sends the NEW rotate route for an unknown sink id
- **THEN** the server refuses with sink.not-found

#### Scenario: RotateSigningKey no grant
- **WHEN** a caller without namespace:manage-notifications sends the NEW rotate route
- **THEN** the server denies the call and writes an audit entry

### Requirement: Confirm retires old key

When a caller with namespace:manage-notifications sends the NEW confirm route with the old key id, the server SHALL retire the named old signing secret ref.

#### Scenario: ConfirmRotation confirms
- **WHEN** a caller sends the NEW confirm route with the old key id
- **THEN** one ref stays active and deliveries carry one signature

#### Scenario: ConfirmRotation unknown ref
- **WHEN** a caller sends the NEW confirm route with an unknown key id
- **THEN** the server refuses with sink.not-found and keeps both refs

#### Scenario: ConfirmRotation sole key
- **WHEN** a caller sends the NEW confirm route while one ref is active
- **THEN** the server refuses with sink.conflict

### Requirement: Refuse installation scope

If a caller creates a chat or webhook sink in scope @server, then the server SHALL refuse with sink.invalid.

#### Scenario: Server scope
- **WHEN** a caller creates a webhook sink in scope @server
- **THEN** the server refuses with sink.invalid

### Requirement: Two signatures on delivery

While two signing secrets are active, the server SHALL send both signatures on each webhook delivery with the key id of each signature.

#### Scenario: Local receiver
- **WHEN** the test route sends to the allowlisted local receiver during rotation
- **THEN** both signatures verify, each with its key id

### Requirement: Audit rotation

When a rotate or confirm call runs, the server SHALL write a change audit entry with no secret value.

#### Scenario: RotateSigningKey and ConfirmRotation audit entry
- **WHEN** rotate succeeds
- **THEN** the audit entry names the sink and the key id and holds no secret
