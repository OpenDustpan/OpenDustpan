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

### Requirement: deny without grant

If a caller lacks the required action on the unit, then the server SHALL deny the read with 403.

#### Scenario: sibling grant denied
- **WHEN** caller holds unit:read on another unit with no grant on this unit
- **THEN** server answers 403 with no secret bytes

### Requirement: bounds and failures

If a version is above 5 MB, then the server SHALL refuse the read with 413 and no content.

#### Scenario: over size refused
- **WHEN** version is above 5 MB
- **THEN** server refuses with 413 and no content
