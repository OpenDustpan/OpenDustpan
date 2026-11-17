## ADDED Requirements

### Requirement: Reveal

When a caller with unit:read-secrets calls reveal with unit, version and one value path through Reveal, the server SHALL answer the plain value of that path with no other plain value.

#### Scenario: Reveal one value and one entry
- **WHEN** caller with unit:read-secrets reveals one path through Reveal
- **THEN** server answers that value with Reveal and writes one reveal entry with channel and principalKind before the first byte

#### Scenario: Reveal retry writes again
- **WHEN** caller repeats the same reveal call after a disconnect
- **THEN** each delivered answer has its own reveal entry

### Requirement: reveal refusals

If a reveal names an unknown version, an unknown path, a non-secret path or a Pulumi stack with another secrets provider, then the server SHALL refuse it with no secret bytes.

#### Scenario: unknown version
- **WHEN** reveal names a version that does not exist
- **THEN** server answers 404 with no secret bytes

#### Scenario: non-secret path
- **WHEN** reveal names a path that holds no secret
- **THEN** server refuses with 400 and reveals no value

#### Scenario: Pulumi other provider
- **WHEN** Pulumi stack uses another secrets provider
- **THEN** server refuses with 422 and reveals no value

### Requirement: shredded and redacted

If the target version is shredded or redacted, then the server SHALL refuse plain text with no content.

#### Scenario: shredded item
- **WHEN** caller reveals an item whose key is shredded
- **THEN** server answers 410 before any blob read, and `secret.ErrShredded` does not give 500

### Requirement: deny without grant

If a caller lacks unit:read-secrets on the unit, then the server SHALL deny the reveal with 403.

#### Scenario: read grant only
- **WHEN** caller holds unit:read on this unit and unit:read-secrets on another unit
- **THEN** server answers 403 with the code `unit.forbidden` and no secret bytes

### Requirement: bounds and failures

If a reveal fails to write its audit entry, then the server SHALL answer 500 with no content.

#### Scenario: audit failure
- **WHEN** reveal fails to write its audit entry, for a Pulumi stack or for an OpenTofu workspace
- **THEN** server answers 500 with no content

#### Scenario: OpenTofu hostile path
- **WHEN** an OpenTofu reveal names a path that is not a valid JSON Pointer
- **THEN** server answers 400 with the code `state.bad-path` and no content

### Requirement: refusals are audited

Each refusal of the reveal route SHALL write one audit entry, and the reveal route SHALL count refusals with the denial limiter that `39-reading-state-contents` defines.

#### Scenario: each refusal writes an entry
- **WHEN** reveal is refused for a bad path, a missing version, another secrets provider or an OpenTofu refusal
- **THEN** server writes one audit entry that holds no secret and names the reason

#### Scenario: refusals reach the limiter
- **WHEN** one caller sends repeated reveals that the server refuses
- **THEN** the limiter of `39-reading-state-contents` counts each refusal
