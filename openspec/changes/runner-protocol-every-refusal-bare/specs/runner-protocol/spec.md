## ADDED Requirements

### Requirement: distinct-missing-release

When the Dustpan-Release header is missing, the server SHALL answer 400 with the NEW error code runner.missing-release.

#### Scenario: Missing header on join
- **WHEN** a join request arrives at ANY /runner/v1 with no Dustpan-Release header.
- **THEN** the server answers 400 with the missing-release code and no input detail.

#### Scenario: Missing header on poll
- **WHEN** a poll request arrives at ANY /runner/v1 with no Dustpan-Release header.
- **THEN** the server answers 400 with the missing-release code.

### Requirement: distinct-bad-release

When the Dustpan-Release value is neither the server release string nor major.minor.patch, the server SHALL answer 400 with the NEW error code runner.bad-release.

#### Scenario: Malformed release
- **WHEN** a request arrives at ANY /runner/v1 with release 1.2.
- **THEN** the server answers 400 with the bad-release code.

#### Scenario: Overlong release
- **WHEN** a request arrives at ANY /runner/v1 with a Dustpan-Release header longer than 64 characters.
- **THEN** the server answers 400 with the bad-release code.

### Requirement: distinct-bad-body

When the runner request body is not valid JSON of the expected shape, the server SHALL answer 400 with the NEW error code runner.bad-body.

#### Scenario: Malformed body
- **WHEN** a request arrives at ANY /runner/v1 with body `{`.
- **THEN** the server answers 400 with the bad-body code.

#### Scenario: Wrong shape body
- **WHEN** a join request arrives at ANY /runner/v1 with valid JSON that is not the join shape.
- **THEN** the server answers 400 with the bad-body code.

### Requirement: body-too-large

When the runner request body exceeds 1 MiB, the server SHALL answer 413 with the NEW error code runner.body-too-large.

#### Scenario: Large body
- **WHEN** a request arrives at ANY /runner/v1 with a body larger than 1 MiB or a declared Content-Length larger than 1 MiB.
- **THEN** the server answers 413 with the body-too-large code without reading past the limit and closes the connection after the answer.

#### Scenario: Chunked large body
- **WHEN** a request arrives at ANY /runner/v1 with chunked encoding, no Content-Length, and more than 1 MiB of body bytes.
- **THEN** the server answers 413 with the body-too-large code.

### Requirement: generic-bad-proof

When the Dustpan-Proof header fails verification, the server SHALL answer 400 with the NEW error code runner.bad-proof.

#### Scenario: Bad poll proof
- **WHEN** a poll request arrives at ANY /runner/v1 with header Dustpan-Proof v1 sub=x t=1 n=00 sig=AA.
- **THEN** the server answers 400 with the bad-proof code and no proof detail.

#### Scenario: Bad session proof
- **WHEN** a session request arrives at ANY /runner/v1 with a proof that fails verification.
- **THEN** the server answers 400 with the bad-proof code and no proof detail.

#### Scenario: Unknown subject proof
- **WHEN** a poll request arrives at ANY /runner/v1 with a well-formed proof for an unknown subject, an expired timestamp, or a replayed nonce.
- **THEN** the server answers 400 with the bad-proof code.

#### Scenario: Missing credential
- **WHEN** a poll request arrives at ANY /runner/v1 with no credential and no valid proof.
- **THEN** the server answers 400 with the bad-proof code.

### Requirement: check-order

While more than one fault is present, the server SHALL report faults in the order missing release, bad release, body too large, bad body, version state, then bad proof.

#### Scenario: Missing release wins
- **WHEN** a request arrives at ANY /runner/v1 with no Dustpan-Release header and a body larger than 1 MiB.
- **THEN** the server answers 400 with the missing-release code.

#### Scenario: Version wins over proof
- **WHEN** a request arrives at ANY /runner/v1 with release 0.0.1 and a bad proof.
- **THEN** the server answers 426 with the unsupported code.

### Requirement: dev-self-accept

When the peer release equals the server release string, the server SHALL accept the release value including dev.

#### Scenario: Dev against dev
- **WHEN** a dev build receives its own Dustpan-Release value.
- **THEN** the server accepts the release and continues to body and proof checks with no 426 answer.

#### Scenario: Other release on dev
- **WHEN** a dev build receives release 0.0.1 or 99.0.0.
- **THEN** the server answers 400 with the bad-release code.

#### Scenario: Cause stays server side
- **WHEN** a request arrives at ANY /runner/v1 with a refused release, body, or proof.
- **THEN** the server writes the parse cause to the server log only.

### Requirement: old-release-unsupported

When the peer release is older than the numbered server release, the server SHALL answer 426 with the NEW error code runner.version-unsupported.

#### Scenario: Old release
- **WHEN** a request arrives at ANY /runner/v1 with release 0.0.1 against a newer numbered server.
- **THEN** the server answers 426 with the unsupported code.

### Requirement: new-release-ahead

When the peer release is newer than the numbered server release, the server SHALL answer 426 with the error code version-ahead.

#### Scenario: New release
- **WHEN** a poll request arrives at ANY /runner/v1 with release 99.0.0 against an older numbered server.
- **THEN** the server answers 426 with the version-ahead code.

#### Scenario: Equal numbered release
- **WHEN** a join request arrives at ANY /runner/v1 with a numbered release equal to the server release string.
- **THEN** the server accepts the release and continues to body and proof checks.

#### Scenario: Dev peer on numbered server
- **WHEN** a request arrives at ANY /runner/v1 on a numbered server with release dev.
- **THEN** the server answers 400 with the bad-release code.
