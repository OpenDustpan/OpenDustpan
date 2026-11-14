## ADDED Requirements

### Requirement: ReadMasked

When a caller with unit:read reads one state version through ReadMasked, the server SHALL answer structure with each secret value replaced by a marker.

#### Scenario: ReadMasked masked values
- **WHEN** caller with unit:read reads one pinned version through ReadMasked
- **THEN** server answers structure with markers and the list of value paths, and no plain secret value

#### Scenario: ReadMasked OpenTofu sensitive marks
- **WHEN** caller with unit:read reads one OpenTofu version through ReadMasked
- **THEN** server masks each sensitive output, each sensitive_attributes path and each sensitive_values path

### Requirement: value paths and pages

When the server answers a masked read, the server SHALL key each marker by its JSON Pointer value path for that version.

#### Scenario: paged markers
- **WHEN** state holds more than 200 markers
- **THEN** server answers 200 markers per page with an opaque cursor bound to unit, version and caller

#### Scenario: cursor reuse refused
- **WHEN** caller reuses a cursor on another unit, another version or after 10 minutes
- **THEN** server refuses the page with 400

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
- **THEN** server answers 410 before any blob read

### Requirement: deny without grant

If a caller lacks the required action on the unit, then the server SHALL deny the read with 403.

#### Scenario: sibling grant denied
- **WHEN** caller holds unit:read on another unit with no grant on this unit
- **THEN** server answers 403 with no secret bytes

### Requirement: bounds and failures

If a reveal fails to write its audit entry, then the server SHALL answer 500 with no content.

#### Scenario: audit failure
- **WHEN** reveal fails to write its audit entry
- **THEN** server answers 500 with no content

#### Scenario: over size refused
- **WHEN** version is above 5 MB
- **THEN** server refuses with 413 and no content
