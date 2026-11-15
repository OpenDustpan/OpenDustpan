## ADDED Requirements

### Requirement: Read namespace and unit context

When a caller with read requests Read namespace and unit context of one unit, the server SHALL answer the entries of that stable unit ID.

#### Scenario: Read namespace and unit context for one unit
- **WHEN** A caller with unit read requests the context of one unit by stable ID.
- **THEN** The server answers the resolved entries with provenance and masked secrets.

#### Scenario: Unknown namespace first
- **WHEN** A caller requests Read namespace and unit context of an unknown namespace.
- **THEN** The server answers 404 with code context.object-not-found.

#### Scenario: Hidden start unit
- **WHEN** A caller without read requests Read namespace and unit context of one unit.
- **THEN** The server answers 404 with code context.object-not-found and appends a denied audit entry.

#### Scenario: Soft-deleted unit read
- **WHEN** A caller with read requests Read namespace and unit context of a soft-deleted unit.
- **THEN** The server answers the entries with deleted true.

### Requirement: Patch and delete context entries

When a caller with write patches Patch and delete context entries of one unit, the server SHALL store one entry per resource key and source.

#### Scenario: Patch and delete context entries for one section
- **WHEN** A caller with write patches one section of one unit.
- **THEN** The server stores the entry and answers the updated context.

#### Scenario: Patch unknown section
- **WHEN** A caller patches a section that the section catalog does not list.
- **THEN** The server answers 400 with code context.unknown-section.

#### Scenario: Patch invalid field
- **WHEN** A caller patches a field with an invalid value.
- **THEN** The server answers 400 with code context.invalid.

#### Scenario: Patch soft-deleted unit
- **WHEN** A caller runs Patch and delete context entries of a soft-deleted unit.
- **THEN** The server answers 409 with code context.unit-deleted.

#### Scenario: Delete unit context
- **WHEN** A caller with write runs Patch and delete context entries to delete the context of one unit.
- **THEN** The server deletes the entries of that stable unit ID and answers the empty context.

#### Scenario: Patch namespace context
- **WHEN** A caller with write runs Patch and delete context entries on namespace context.
- **THEN** The server stores the namespace entry by stable namespace ID.
